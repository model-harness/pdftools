package image

import (
	"fmt"

	"github.com/model-harness/pdftools/filter"
	"github.com/model-harness/pdftools/objects"
)

// maxFormDepth bounds recursion into Form XObjects.
//
// A form may reference itself, directly or through a cycle, and a crafted file
// can nest them without limit. The seen set catches the cycle for anything
// reached by indirect reference — which in practice is everything — and this is
// the backstop for a chain of direct objects, which no producer emits but a
// hostile file may. 8 is the same bound extract uses for the same reason.
const maxFormDepth = 8

// maxSamples caps an image's sample count at 256 million.
//
// Width and Height are attacker-controlled integers, and their product decides
// the size of the buffer Encode allocates. A dictionary claiming 2^31 by 2^31
// costs nothing to write and would ask for an exabyte. The cap is checked at read
// time so the bad dictionary is rejected before anything is allocated, and it
// sits well above the corpus's largest image at 24.7 million samples.
const maxSamples = 256 << 20

// Reader extracts image XObjects from a store.
type Reader struct {
	s objects.Store
	// seen deduplicates by indirect reference across the whole document. One
	// XObject drawn on 1,023 pages is one image; without this the specification
	// reports thousands and every one of them is the same bytes.
	seen map[objects.Ref]bool
}

// NewReader returns a Reader over s.
func NewReader(s objects.Store) *Reader {
	return &Reader{s: s, seen: make(map[objects.Ref]bool)}
}

// Images returns every image XObject in the document, in page order, each once.
//
// An image that cannot be read is skipped rather than failing the run: a damaged
// or unsupported image among hundreds should not cost the caller the other
// hundreds. Count the results against Errors to see what was lost.
func (r *Reader) Images() ([]*Image, error) {
	var out []*Image
	for n := 1; n <= r.s.PageCount(); n++ {
		ims, err := r.Page(n)
		if err != nil {
			// A page whose dictionary will not resolve is a structural fault, not
			// an image fault, and the remaining pages are still worth reading.
			continue
		}
		out = append(out, ims...)
	}
	return out, nil
}

// Page returns the images first reached on a 1-based page.
//
// Deduplication is document-wide and stateful, so calling Page over an ascending
// range yields each image once, attributed to the first page that draws it.
// Calling Page twice for the same number yields nothing the second time.
func (r *Reader) Page(n int) ([]*Image, error) {
	page, err := r.s.Page(n)
	if err != nil {
		return nil, err
	}
	res, ok := objects.GetDict(r.s, page, "Resources")
	if !ok {
		return nil, nil
	}
	var out []*Image
	r.walk(res, n, 0, &out)
	return out, nil
}

// walk collects images from a resource dictionary, recursing into Form XObjects.
//
// Forms matter: 7 of the corpus's 239 images are inside one, so a walker that
// only looked at page-level resources would silently miss them. That is the same
// defect that cost the font subsystem 21 of 247 fonts before it was fixed.
func (r *Reader) walk(res objects.Dict, page, depth int, out *[]*Image) {
	if depth >= maxFormDepth || res == nil {
		return
	}
	xobjs, ok := objects.GetDict(r.s, res, "XObject")
	if !ok {
		return
	}
	for name, raw := range xobjs {
		ref, isRef := raw.(objects.Ref)
		if isRef {
			if r.seen[ref] {
				continue
			}
			r.seen[ref] = true
		}
		v, err := r.s.Resolve(raw)
		if err != nil {
			continue
		}
		st, isStream := v.(*objects.Stream)
		if !isStream {
			continue
		}
		switch sub, _ := objects.GetName(r.s, st.Dict, "Subtype"); sub {
		case "Image":
			im, err := r.image(st, ref, name, page)
			if err != nil {
				continue
			}
			*out = append(*out, im)
		case "Form":
			// A form with no /Resources inherits the invoking dictionary's
			// (§8.10.1), which is how an image inside such a form is reached at all.
			inner := res
			if d, ok := objects.GetDict(r.s, st.Dict, "Resources"); ok {
				inner = d
			}
			r.walk(inner, page, depth+1, out)
		}
	}
}

// image builds an Image from an image XObject stream.
func (r *Reader) image(st *objects.Stream, ref objects.Ref, name objects.Name, page int) (*Image, error) {
	im := &Image{Ref: ref, Name: name, Page: page}

	w, wok := objects.GetInt(r.s, st.Dict, "Width")
	if !wok {
		w, wok = objects.GetInt(r.s, st.Dict, "W")
	}
	h, hok := objects.GetInt(r.s, st.Dict, "Height")
	if !hok {
		h, hok = objects.GetInt(r.s, st.Dict, "H")
	}
	if !wok || !hok || w <= 0 || h <= 0 {
		// The two mandatory keys. Without them the samples cannot be laid out at
		// all, so there is nothing to salvage.
		return nil, fmt.Errorf("image: missing or invalid /Width or /Height")
	}
	if w > maxSamples || h > maxSamples || w*h > maxSamples {
		return nil, fmt.Errorf("image: %d×%d exceeds the %d-sample limit", w, h, maxSamples)
	}
	im.Width, im.Height = int(w), int(h)

	if v, ok := objects.GetBool(r.s, st.Dict, "ImageMask"); ok && v {
		im.Stencil = true
	} else if v, ok := objects.GetBool(r.s, st.Dict, "IM"); ok && v {
		im.Stencil = true
	}

	bpc, ok := objects.GetInt(r.s, st.Dict, "BitsPerComponent")
	if !ok {
		bpc, ok = objects.GetInt(r.s, st.Dict, "BPC")
	}
	switch {
	case im.Stencil:
		// §8.9.6.2: a stencil mask's /BitsPerComponent, if present, must be 1, and
		// is optional. Forcing 1 rather than trusting a stray value keeps the
		// unpacker's row stride correct.
		im.BitsPerComponent = 1
	case ok && validBPC(bpc):
		im.BitsPerComponent = int(bpc)
	default:
		// Absent or nonsensical on a non-stencil image. 8 is the overwhelming
		// majority — 235 of 239 here — and is the only guess that makes the stride
		// arithmetic work at all.
		im.BitsPerComponent = 8
	}

	im.Decode = r.numArray(st.Dict, "Decode", "D")
	r.colorSpace(st, im)
	r.codec(st, im)
	if im.Codec == CodecCCITT {
		im.CCITT = readCCITTParams(r.s, st)
	}

	if sm, ok := objects.GetStream(r.s, st.Dict, "SMask"); ok {
		// A soft mask is itself an image XObject, so it is read the same way. It is
		// not entered into the seen set: it belongs to this image, and a mask shared
		// between two base images must appear with both.
		if m, err := r.image(sm, objects.Ref{}, name+".smask", page); err == nil {
			m.Matte = r.numArray(sm.Dict, "Matte", "")
			im.SMask = m
		}
	}
	return im, nil
}

func validBPC(n int64) bool {
	switch n {
	case 1, 2, 4, 8, 16:
		return true
	}
	return false
}

// codec classifies the filter chain and fills Data.
//
// filter.DecodeChain stops at the first image filter and reports it, which is
// exactly the split this needs: the byte-oriented filters come off, the image
// codec stays on. A Flate-then-DCT chain therefore yields a decompressed JPEG,
// and a Flate-only chain yields unpacked samples.
func (r *Reader) codec(st *objects.Stream, im *Image) {
	data, err := filter.DecodeChain(st, r.s)
	im.Data = data

	// Which codec, if any, the chain stopped at. Reading it from the chain rather
	// than from the error string keeps this independent of that message's wording.
	im.Codec = CodecRaw
	for _, name := range st.Filters {
		if !filter.IsImage(name) {
			continue
		}
		switch name {
		case "DCTDecode", "DCT":
			im.Codec = CodecJPEG
		case "CCITTFaxDecode", "CCF":
			im.Codec = CodecCCITT
		case "JBIG2Decode":
			im.Codec = CodecJBIG2
		case "JPXDecode":
			im.Codec = CodecJPX
		}
		break
	}
	if err != nil && im.Codec == CodecRaw {
		// A byte-filter failure on raw samples leaves Data truncated. filter keeps
		// the recovered prefix deliberately, and Encode pads a short buffer, so the
		// partial image is still written — which for a damaged file is the outcome
		// that recovers something.
		return
	}
}

// colorSpace resolves /ColorSpace to a family, a component count, and an
// /Indexed palette.
func (r *Reader) colorSpace(st *objects.Stream, im *Image) {
	if im.Stencil {
		// A stencil mask has no colour space of its own: it selects where the
		// current fill colour is painted. One component, one bit.
		im.Components = 1
		return
	}
	raw, ok := st.Dict["ColorSpace"]
	if !ok {
		raw, ok = st.Dict["CS"]
	}
	if !ok {
		return
	}
	v, err := r.s.Resolve(raw)
	if err != nil {
		return
	}
	switch cs := v.(type) {
	case objects.Name:
		im.ColorSpaceFamily = cs
		im.Components = componentsOf(cs)
	case objects.Array:
		if len(cs) == 0 {
			return
		}
		fam, isName := cs[0].(objects.Name)
		if !isName {
			return
		}
		im.ColorSpaceFamily = fam
		switch fam {
		case "ICCBased":
			// An ICCBased space carries its component count in the stream's /N,
			// which is mandatory (§8.6.5.5) and is the only place it is stated.
			if len(cs) < 2 {
				return
			}
			if str, ok := r.stream(cs[1]); ok {
				if n, ok := objects.GetInt(r.s, str.Dict, "N"); ok && (n == 1 || n == 3 || n == 4) {
					im.Components = int(n)
				}
			}
		case "Indexed", "I":
			// [/Indexed base hival lookup]. An indexed sample is one component —
			// an index — and the palette expands it to the base space's components.
			if len(cs) < 4 {
				return
			}
			im.Components = 1
			im.ColorSpaceFamily = "Indexed"
			if b, err := r.s.Resolve(cs[1]); err == nil {
				im.Base, im.HiVal = baseFamily(b), 0
			}
			if n, ok := objects.AsNum(mustResolve(r.s, cs[2])); ok {
				im.HiVal = int(n)
			}
			im.Palette = r.lookup(cs[3])
		case "DeviceN":
			// [/DeviceN names alternate tint]. The name array's length is the
			// component count.
			if len(cs) > 1 {
				if arr, ok := mustResolve(r.s, cs[1]).(objects.Array); ok {
					im.Components = len(arr)
				}
			}
		case "Separation":
			im.Components = 1
		case "CalRGB", "Lab":
			im.Components = 3
		case "CalGray":
			im.Components = 1
		default:
			im.Components = componentsOf(fam)
		}
	}
}

// lookup reads an /Indexed palette, which may be a string or a stream.
func (r *Reader) lookup(o objects.Object) []byte {
	v, err := r.s.Resolve(o)
	if err != nil {
		return nil
	}
	switch t := v.(type) {
	case objects.String:
		return []byte(t)
	case *objects.Stream:
		if t.Decoded == nil {
			if err := r.s.Decode(t); err != nil {
				return nil
			}
		}
		return t.Decoded
	}
	return nil
}

func (r *Reader) stream(o objects.Object) (*objects.Stream, bool) {
	v, err := r.s.Resolve(o)
	if err != nil {
		return nil, false
	}
	st, ok := v.(*objects.Stream)
	return st, ok
}

// numArray reads a numeric array under key, or alt when key is absent. Returns
// nil when neither is present, which is the common case for both /Decode and
// /Matte and must stay distinguishable from an empty array.
func (r *Reader) numArray(d objects.Dict, key, alt objects.Name) []float64 {
	arr, ok := objects.GetArray(r.s, d, key)
	if !ok && alt != "" {
		arr, ok = objects.GetArray(r.s, d, alt)
	}
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, item := range arr {
		v, err := r.s.Resolve(item)
		if err != nil {
			return nil
		}
		f, isNum := objects.AsNum(v)
		if !isNum {
			return nil
		}
		out = append(out, f)
	}
	return out
}

func mustResolve(s objects.Store, o objects.Object) objects.Object {
	v, err := s.Resolve(o)
	if err != nil {
		return objects.Null{}
	}
	return v
}

// baseFamily reduces an /Indexed base space to its family name.
func baseFamily(o objects.Object) objects.Name {
	switch t := o.(type) {
	case objects.Name:
		return t
	case objects.Array:
		if len(t) > 0 {
			if n, ok := t[0].(objects.Name); ok {
				return n
			}
		}
	}
	return ""
}

// componentsOf returns the component count of a device colour space, or 0 for a
// name this package does not recognize.
//
// Zero rather than a guess: the count sets the row stride, and a wrong stride
// does not produce a slightly wrong image, it produces a diagonal smear. Better
// to decline than to emit that.
func componentsOf(n objects.Name) int {
	switch n {
	case "DeviceGray", "G", "CalGray":
		return 1
	case "DeviceRGB", "RGB", "CalRGB":
		return 3
	case "DeviceCMYK", "CMYK":
		return 4
	}
	return 0
}

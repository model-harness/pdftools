package image

import (
	"bytes"
	"fmt"
	stdimage "image"
	"image/color"
	"image/png"
	"io"
	"math"

	"golang.org/x/image/ccitt"

	"github.com/3rg0n/pdf-spec/bits"
	"github.com/3rg0n/pdf-spec/objects"
)

// Encode writes im to w in the format Ext reports.
//
// For CodecJPEG that is a byte-for-byte copy: the stream already is a JPEG, and
// re-encoding it would lose a generation of quality for no gain. Every other
// supported codec is decoded to pixels and written as PNG, which is lossless and
// carries the alpha channel a soft mask needs.
//
// The soft mask is not composited in. Encode writes the base image only; the
// caller writes im.SMask separately. See the package comment for why.
func Encode(w io.Writer, im *Image) error {
	switch im.Codec {
	case CodecJPEG:
		// Passthrough. Not validated as a JPEG first: a stream this repo cannot
		// parse may still be one a viewer opens, and truncating the user's data
		// because the stdlib decoder disliked a marker would lose more than it
		// protects.
		_, err := w.Write(im.Data)
		return err
	case CodecRaw:
		img, err := decodeRaw(im)
		if err != nil {
			return err
		}
		return png.Encode(w, img)
	case CodecCCITT:
		img, err := decodeCCITT(im)
		if err != nil {
			return err
		}
		return png.Encode(w, img)
	}
	return fmt.Errorf("%w: %s", ErrUnsupported, im.Codec)
}

// decodeCCITT decodes a Group 3 or Group 4 fax stream.
//
// The parameters live in /DecodeParms, not in the data: unlike a JPEG, a CCITT
// stream carries no header saying how wide it is or which variant it uses, so a
// decoder that ignored the dictionary would produce nothing usable. /K selects
// the variant, /BlackIs1 the polarity, and /EncodedByteAlign whether rows start
// on byte boundaries.
//
// No image in this repo's corpus is CCITT, so the parameter handling here is
// pinned by fixtures built from ITU T.4 and T.6 code tables rather than by a real
// file — see encode_test.go. That is a weaker guarantee than the rest of the
// package has, and it is the reason CCITT is wired rather than owned.
func decodeCCITT(im *Image) (stdimage.Image, error) {
	p := im.CCITT

	sf := ccitt.Group4
	if p.K == 0 {
		sf = ccitt.Group3
	} else if p.K > 0 {
		// Mixed 1-D/2-D. x/image/ccitt has no separate sub-format for it: a K>0
		// stream's rows each announce their own coding, so the Group3 reader
		// handles it.
		sf = ccitt.Group3
	}

	// A CCITT stream is coded at /Columns and carries no width of its own, so it
	// has to be decoded at /Columns even when the image dictionary disagrees —
	// decoding at /Width would misread every row's codes. /Width is what the rest
	// of this package is consistent with, though: the row stride and the soft
	// mask's dimensions are all in its terms, so the result is cropped back to it
	// below. The two disagreeing means the file is inconsistent, and this is the
	// reading that recovers something from it.
	width := im.Width
	if p.Columns > 0 && p.Columns != width {
		width = p.Columns
	}

	// The two defaults already agree, so /BlackIs1 maps straight through rather
	// than inverted. x/image/ccitt's Invert=false means black is the 0 byte, and
	// §7.4.6's /BlackIs1=false means 0 bits are black — the same convention. Wiring
	// this the other way round decodes every fax image as a photographic negative,
	// which is why the polarity has a test of its own.
	opts := &ccitt.Options{Align: p.EncodedByteAlign, Invert: p.BlackIs1}

	dst := stdimage.NewGray(stdimage.Rect(0, 0, width, im.Height))
	if err := ccitt.DecodeIntoGray(dst, bytes.NewReader(im.Data), ccitt.MSB, sf, opts); err != nil {
		// Partial output is kept for the same reason filter keeps a truncated
		// Flate stream: the rows decoded before the break are real, and a fax
		// stream that ends early is a damaged file rather than a hostile one.
		if dst.Bounds().Empty() {
			return nil, fmt.Errorf("image: ccitt: %w", err)
		}
		// x/image/ccitt applies Invert only after its row loop completes, so the
		// rows recovered from a truncated stream have not been inverted. Doing it
		// here keeps a damaged /BlackIs1 image the right way round; without it the
		// partial recovery above hands back a photographic negative, which is the
		// same failure the polarity test exists to catch, reached by the error path.
		if opts.Invert {
			for i := range dst.Pix {
				dst.Pix[i] = ^dst.Pix[i]
			}
		}
	}
	if width != im.Width {
		return dst.SubImage(stdimage.Rect(0, 0, im.Width, im.Height)), nil
	}
	return dst, nil
}

// decodeRaw unpacks packed component samples into a pixel image.
//
// This is where bits is load-bearing. Samples below 8 bits per component are
// packed several to a byte and every row restarts on a byte boundary (§8.9.5.1),
// so a 5-pixel 1-bit row occupies one byte with three bits of padding. Reading
// that with byte arithmetic works for the first row and skews every one after it.
func decodeRaw(im *Image) (stdimage.Image, error) {
	if im.Stencil {
		return decodeStencil(im)
	}
	if im.Components == 0 {
		return nil, fmt.Errorf("image: %s: unknown component count, cannot lay out samples",
			im.ColorSpaceFamily)
	}
	// 16-bit samples need no special case: the unpacker reads whatever width the
	// dictionary declares and normalizes by that width's maximum, so a 16-bit
	// sample arrives as the same 0..1 value an 8-bit one would. The precision below
	// 1/255 is then lost to the 8-bit PNG, which is a real loss and applies to no
	// image in this corpus.
	return decodeSamples(im)
}

// decodeStencil turns a 1-bit stencil mask into a transparency image.
//
// A stencil paints the current fill colour through its unmasked samples, and the
// extractor does not know what that colour was — it is a graphics-state value at
// the Do operator, and the same mask may be painted in different colours in
// different places. Black-on-transparent is the honest rendering: it shows the
// shape the mask describes without inventing a colour it never carried.
//
// A sample of 0 paints by default, and /Decode [1 0] reverses that (§8.9.6.2).
func decodeStencil(im *Image) (stdimage.Image, error) {
	invert := len(im.Decode) >= 2 && im.Decode[0] == 1
	dst := stdimage.NewNRGBA(stdimage.Rect(0, 0, im.Width, im.Height))
	r := bits.NewReader(im.Data, bits.MSB)

	for y := 0; y < im.Height; y++ {
		for x := 0; x < im.Width; x++ {
			v, err := r.Bit()
			if err != nil {
				// Truncated: the rest stays transparent, which is what an
				// unpainted mask sample means anyway.
				return dst, nil
			}
			paint := v == 0
			if invert {
				paint = !paint
			}
			if paint {
				dst.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
		r.Align()
	}
	return dst, nil
}

// decodeSamples unpacks colour samples and converts them to RGBA.
//
// Every colour space is reduced to RGB here because PNG has no CMYK or Lab; the
// conversions are the naive ones, which is correct for the device spaces and an
// approximation for the CIE-based ones. An ICC profile is not applied — honouring
// one means colour management, which is a rendering concern and not this
// package's.
func decodeSamples(im *Image) (stdimage.Image, error) {
	bpc := uint(im.BitsPerComponent)
	maxVal := float64(int(1)<<bpc - 1)
	comps := im.Components
	dst := stdimage.NewNRGBA(stdimage.Rect(0, 0, im.Width, im.Height))
	r := bits.NewReader(im.Data, bits.MSB)
	smask := alphaAt(im)

	sample := make([]float64, comps)
	for y := 0; y < im.Height; y++ {
		for x := 0; x < im.Width; x++ {
			truncated := false
			for c := 0; c < comps; c++ {
				v, err := r.Bits(bpc)
				if err != nil {
					truncated = true
					break
				}
				sample[c] = float64(v) / maxVal
			}
			if truncated {
				// Stop at the first short sample rather than emitting half a
				// pixel. The remainder stays transparent.
				return dst, nil
			}
			applyDecode(im.Decode, sample)
			cr, cg, cb := toRGB(im, sample)
			a := uint8(255)
			if smask != nil {
				a = smask(x, y)
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: cr, G: cg, B: cb, A: a})
		}
		// Rows are byte-aligned, and this is the call that keeps a 1-, 2-, or
		// 4-bit image from skewing.
		r.Align()
	}
	return dst, nil
}

// applyDecode remaps samples through /Decode, which gives a [min max] pair per
// component (§8.9.5.2). Ignored when its length does not match the component
// count, because a mismatched array is a producer error and applying it partially
// would recolour some channels and not others.
func applyDecode(dec []float64, sample []float64) {
	if len(dec) != 2*len(sample) {
		return
	}
	for i := range sample {
		lo, hi := dec[2*i], dec[2*i+1]
		sample[i] = lo + sample[i]*(hi-lo)
	}
}

// toRGB converts one sample to 8-bit RGB.
func toRGB(im *Image, sample []float64) (uint8, uint8, uint8) {
	switch {
	case im.ColorSpaceFamily == "Indexed" && len(im.Palette) > 0:
		return paletteLookup(im, sample[0])
	case len(sample) == 1:
		g := clamp8(sample[0])
		return g, g, g
	case len(sample) == 3:
		if im.ColorSpaceFamily == "Lab" {
			return labToRGB(sample)
		}
		return clamp8(sample[0]), clamp8(sample[1]), clamp8(sample[2])
	case len(sample) == 4:
		// CMYK. The subtractive conversion, not an ICC-managed one.
		k := sample[3]
		return clamp8((1 - sample[0]) * (1 - k)),
			clamp8((1 - sample[1]) * (1 - k)),
			clamp8((1 - sample[2]) * (1 - k))
	}
	// A DeviceN or Separation space with some other component count. The first
	// component read as ink coverage is the conventional fallback, and a
	// Separation's tint is exactly that.
	g := clamp8(1 - sample[0])
	return g, g, g
}

// paletteLookup resolves an /Indexed sample through its lookup table.
//
// The sample was normalized to 0..1 by the caller, so it is scaled back to an
// index. An index past the table is clamped rather than treated as an error:
// §8.6.6.3 makes out-of-range indices legal and clamps them.
func paletteLookup(im *Image, norm float64) (uint8, uint8, uint8) {
	maxVal := float64(int(1)<<uint(im.BitsPerComponent) - 1)
	idx := int(norm*maxVal + 0.5)
	if idx > im.HiVal {
		idx = im.HiVal
	}
	if idx < 0 {
		idx = 0
	}
	n := componentsOf(im.Base)
	if n == 0 {
		n = 3
	}
	off := idx * n
	if off+n > len(im.Palette) {
		// A truncated palette. Grey rather than a guess at the missing entry.
		return 0, 0, 0
	}
	switch n {
	case 1:
		g := im.Palette[off]
		return g, g, g
	case 3:
		return im.Palette[off], im.Palette[off+1], im.Palette[off+2]
	case 4:
		c := float64(im.Palette[off]) / 255
		m := float64(im.Palette[off+1]) / 255
		yl := float64(im.Palette[off+2]) / 255
		k := float64(im.Palette[off+3]) / 255
		return clamp8((1 - c) * (1 - k)), clamp8((1 - m) * (1 - k)), clamp8((1 - yl) * (1 - k))
	}
	return 0, 0, 0
}

// labToRGB converts CIELAB to sRGB through XYZ with a D50 white point, which is
// PDF's default for a Lab space that does not say otherwise.
//
// The /Decode default for Lab is not 0..1 per component, so the caller's
// normalization has already scaled L into 0..1 and a,b into 0..1 across their
// -100..100 range; this undoes that.
func labToRGB(sample []float64) (uint8, uint8, uint8) {
	l := sample[0] * 100
	a := sample[1]*255 - 128
	b := sample[2]*255 - 128

	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - b/200
	const xn, yn, zn = 0.9642, 1.0, 0.8249
	x := xn * finv(fx)
	y := yn * finv(fy)
	z := zn * finv(fz)

	rr := 3.1339*x - 1.6170*y - 0.4906*z
	gg := -0.9785*x + 1.9160*y + 0.0333*z
	bb := 0.0720*x - 0.2290*y + 1.4057*z
	return clamp8(gamma(rr)), clamp8(gamma(gg)), clamp8(gamma(bb))
}

func finv(t float64) float64 {
	if t > 6.0/29.0 {
		return t * t * t
	}
	return 3 * (6.0 / 29.0) * (6.0 / 29.0) * (t - 4.0/29.0)
}

func gamma(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	// The sRGB transfer function, not a plain 1/2.2.
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// alphaAt returns a per-pixel alpha lookup from the soft mask, or nil when there
// is none.
//
// The mask may be a different size from the base image — 4 of the corpus's 143
// are — so coordinates are scaled by nearest neighbour rather than assumed to
// match. Assuming they match reads outside the mask's buffer for a smaller mask
// and crops it for a larger one.
func alphaAt(im *Image) func(x, y int) uint8 {
	sm := im.SMask
	if sm == nil || sm.Codec != CodecRaw || sm.Width <= 0 || sm.Height <= 0 {
		// A DCT-encoded soft mask — 2 of the corpus's 143 — would have to be run
		// through the JPEG decoder to become alpha. Left opaque instead: the base
		// image is still written faithfully, and the mask is written as its own
		// file, which is where its data remains available.
		return nil
	}
	bpc := uint(sm.BitsPerComponent)
	maxVal := float64(int(1)<<bpc - 1)
	// Unpack the mask once. Doing it per pixel would re-read the bit stream from
	// the start for every sample of the base image.
	alpha := make([]uint8, sm.Width*sm.Height)
	r := bits.NewReader(sm.Data, bits.MSB)
unpack:
	for y := 0; y < sm.Height; y++ {
		for x := 0; x < sm.Width; x++ {
			v, err := r.Bits(bpc)
			if err != nil {
				// Truncated mask: the remainder is opaque, which shows the base
				// image rather than erasing it.
				for i := y*sm.Width + x; i < len(alpha); i++ {
					alpha[i] = 255
				}
				break unpack
			}
			f := float64(v) / maxVal
			if len(sm.Decode) == 2 {
				f = sm.Decode[0] + f*(sm.Decode[1]-sm.Decode[0])
			}
			alpha[y*sm.Width+x] = clamp8(f)
		}
		r.Align()
	}
	sw, sh := sm.Width, sm.Height
	bw, bh := im.Width, im.Height
	return func(x, y int) uint8 {
		mx, my := x, y
		if sw != bw {
			mx = x * sw / bw
		}
		if sh != bh {
			my = y * sh / bh
		}
		if mx < 0 || my < 0 || mx >= sw || my >= sh {
			return 255
		}
		return alpha[my*sw+mx]
	}
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

// CCITTParams are /DecodeParms for a CCITTFaxDecode stream (§7.4.6), with the
// spec's defaults already applied.
type CCITTParams struct {
	// K selects the variant: negative is pure Group 4 two-dimensional, 0 is pure
	// Group 3 one-dimensional, positive is mixed. Default 0.
	K int

	// Columns is the samples per row, default 1728 — a fax page width, and the
	// reason a CCITT stream with no /Columns is almost never the width the image
	// dictionary claims.
	Columns int

	// Rows is the row count, default 0 meaning "as many as the data holds".
	Rows int

	// BlackIs1 inverts the polarity: false, the default, means 0 is black.
	BlackIs1 bool

	// EncodedByteAlign makes each row start on a byte boundary.
	EncodedByteAlign bool
}

// defaultCCITT is the spec's defaults, which apply when /DecodeParms is absent.
var defaultCCITT = CCITTParams{Columns: 1728}

// readCCITTParams pulls the CCITT parameters out of a stream's /DecodeParms.
//
// Positionally aligned with /Filter, so the parameters belong to whichever entry
// is the CCITT one — a Flate-then-CCITT chain puts them second. Getting this
// wrong silently applies a Flate predictor's parameters to the fax decoder.
func readCCITTParams(s objects.Store, st *objects.Stream) CCITTParams {
	out := defaultCCITT
	idx := -1
	for i, name := range st.Filters {
		if name == "CCITTFaxDecode" || name == "CCF" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return out
	}
	raw, ok := st.Dict["DecodeParms"]
	if !ok {
		raw, ok = st.Dict["DP"]
		if !ok {
			return out
		}
	}
	v, err := s.Resolve(raw)
	if err != nil {
		return out
	}
	arr := objects.ArrayOrSingle(v)
	// A single dictionary applies to the filter it accompanies even in a chain of
	// one; past that, position decides.
	var d objects.Dict
	if len(arr) == 1 {
		d, _ = mustResolve(s, arr[0]).(objects.Dict)
	} else if idx < len(arr) {
		d, _ = mustResolve(s, arr[idx]).(objects.Dict)
	}
	if d == nil {
		return out
	}
	if n, ok := objects.GetInt(s, d, "K"); ok {
		out.K = int(n)
	}
	if n, ok := objects.GetInt(s, d, "Columns"); ok && n > 0 {
		out.Columns = int(n)
	}
	if n, ok := objects.GetInt(s, d, "Rows"); ok && n > 0 {
		out.Rows = int(n)
	}
	if v, ok := objects.GetBool(s, d, "BlackIs1"); ok {
		out.BlackIs1 = v
	}
	if v, ok := objects.GetBool(s, d, "EncodedByteAlign"); ok {
		out.EncodedByteAlign = v
	}
	return out
}

package image

import (
	"bytes"
	"compress/zlib"
	"testing"

	"github.com/3rg0n/pdf-spec/objects"
)

// flateOf compresses data the way a producer would, so a test exercising a
// Flate-then-something chain has a stream that actually decodes.
func flateOf(data []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

// A synthetic store rather than a fixture file. The questions this package has to
// answer are about specific dictionary shapes — an /Indexed array whose lookup is a
// stream, a form that inherits its parent's resources, the same XObject referenced
// from two pages — and a real PDF cannot be edited to isolate one of them. The
// corpus tests in cmd/pdfspec cover what synthetic dictionaries cannot: that the
// same logic survives real producers.
type memStore struct {
	objs  map[objects.Ref]objects.Object
	pages []objects.Dict
}

func (m *memStore) Resolve(o objects.Object) (objects.Object, error) {
	for i := 0; i < 8; i++ {
		ref, isRef := o.(objects.Ref)
		if !isRef {
			return o, nil
		}
		v, ok := m.objs[ref]
		if !ok {
			return objects.Null{}, nil
		}
		o = v
	}
	return objects.Null{}, nil
}

func (m *memStore) Trailer() (objects.Dict, error) { return objects.Dict{}, nil }
func (m *memStore) Catalog() (objects.Dict, error) { return objects.Dict{}, nil }
func (m *memStore) PageCount() int                 { return len(m.pages) }

func (m *memStore) Page(n int) (objects.Dict, error) {
	if n < 1 || n > len(m.pages) {
		return nil, objects.ErrNotFound
	}
	return m.pages[n-1], nil
}

func (m *memStore) PageContent(int) ([]byte, error) { return nil, nil }

func (m *memStore) Decode(s *objects.Stream) error {
	s.Decoded = s.Raw
	return nil
}

func (m *memStore) Version() string { return "2.0" }
func (m *memStore) Encrypted() bool { return false }
func (m *memStore) Close() error    { return nil }

// img builds an unfiltered image XObject.
func img(w, h int, extra objects.Dict) *objects.Stream {
	d := objects.Dict{
		"Subtype":          objects.Name("Image"),
		"Width":            objects.Int(w),
		"Height":           objects.Int(h),
		"BitsPerComponent": objects.Int(8),
		"ColorSpace":       objects.Name("DeviceRGB"),
	}
	for k, v := range extra {
		d[k] = v
	}
	return &objects.Stream{Dict: d, Raw: make([]byte, w*h*3)}
}

func pageWith(xobjs objects.Dict) objects.Dict {
	return objects.Dict{"Resources": objects.Dict{"XObject": xobjs}}
}

func TestReadsBasicImage(t *testing.T) {
	s := &memStore{
		objs:  map[objects.Ref]objects.Object{{Num: 1}: img(4, 3, nil)},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	ims, err := NewReader(s).Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 1 {
		t.Fatalf("got %d images, want 1", len(ims))
	}
	im := ims[0]
	if im.Width != 4 || im.Height != 3 {
		t.Errorf("dimensions = %dx%d, want 4x3", im.Width, im.Height)
	}
	if im.Components != 3 || im.BitsPerComponent != 8 {
		t.Errorf("components = %d, bpc = %d; want 3, 8", im.Components, im.BitsPerComponent)
	}
	if im.Codec != CodecRaw {
		t.Errorf("codec = %s, want raw", im.Codec)
	}
	if im.Name != "Im0" || im.Page != 1 {
		t.Errorf("attribution = %s p%d, want Im0 p1", im.Name, im.Page)
	}
}

// One XObject drawn on many pages is one image. Without deduplication a logo on
// all 1,023 pages of the specification reports 1,023 images that are the same
// bytes, and the corpus's real count of 239 becomes many thousands.
func TestDeduplicatesByIndirectReference(t *testing.T) {
	shared := objects.Ref{Num: 1}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{shared: img(2, 2, nil)},
		pages: []objects.Dict{
			pageWith(objects.Dict{"Im0": shared}),
			pageWith(objects.Dict{"Logo": shared}),
			pageWith(objects.Dict{"Im0": shared}),
		},
	}
	ims, err := NewReader(s).Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 1 {
		t.Fatalf("got %d images, want 1", len(ims))
	}
	// Attributed to the first page that drew it, under the name used there.
	if ims[0].Page != 1 || ims[0].Name != "Im0" {
		t.Errorf("attribution = %s p%d, want Im0 p1", ims[0].Name, ims[0].Page)
	}
}

// 7 of the corpus's 239 images sit inside a Form XObject, so a walker that only
// looked at page-level resources would miss them silently. That is the same defect
// that cost the font subsystem 21 of its 247 fonts.
func TestFindsImagesInsideFormXObjects(t *testing.T) {
	form := &objects.Stream{
		Dict: objects.Dict{
			"Subtype": objects.Name("Form"),
			"Resources": objects.Dict{"XObject": objects.Dict{
				"Inner": objects.Ref{Num: 3},
			}},
		},
	}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			{Num: 2}: form,
			{Num: 3}: img(2, 2, nil),
		},
		pages: []objects.Dict{pageWith(objects.Dict{"Fm0": objects.Ref{Num: 2}})},
	}
	ims, err := NewReader(s).Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 1 {
		t.Fatalf("got %d images, want 1 from inside the form", len(ims))
	}
	if ims[0].Name != "Inner" {
		t.Errorf("name = %s, want Inner", ims[0].Name)
	}
}

// A form with no /Resources inherits the invoking dictionary's (§8.10.1), which is
// the only way an image inside such a form is reachable at all.
func TestFormInheritsResources(t *testing.T) {
	form := &objects.Stream{Dict: objects.Dict{"Subtype": objects.Name("Form")}}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			{Num: 2}: form,
			{Num: 3}: img(2, 2, nil),
		},
		pages: []objects.Dict{pageWith(objects.Dict{
			"Fm0": objects.Ref{Num: 2},
			"Im0": objects.Ref{Num: 3},
		})},
	}
	ims, err := NewReader(s).Images()
	if err != nil {
		t.Fatal(err)
	}
	// The image is found once — directly, or through the form's inherited
	// resources — never twice, because the seen set spans both paths.
	if len(ims) != 1 {
		t.Fatalf("got %d images, want 1", len(ims))
	}
}

// A form that references itself is a cycle a naive walker follows forever. Real
// files do not do this; a crafted one will.
func TestSelfReferentialFormTerminates(t *testing.T) {
	form := &objects.Stream{
		Dict: objects.Dict{
			"Subtype": objects.Name("Form"),
			"Resources": objects.Dict{"XObject": objects.Dict{
				"Self": objects.Ref{Num: 2},
				"Im0":  objects.Ref{Num: 3},
			}},
		},
	}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			{Num: 2}: form,
			{Num: 3}: img(2, 2, nil),
		},
		pages: []objects.Dict{pageWith(objects.Dict{"Fm0": objects.Ref{Num: 2}})},
	}
	ims, err := NewReader(s).Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 1 {
		t.Errorf("got %d images, want 1", len(ims))
	}
}

// /Width and /Height are attacker-controlled and their product sizes the buffer
// Encode allocates. A dictionary claiming billions of samples in each direction
// costs nothing to write and would ask for an exabyte, so it is rejected at read
// time — before anything is allocated.
func TestAbsurdDimensionsAreRejected(t *testing.T) {
	for _, tc := range []struct{ w, h int64 }{
		{1 << 40, 1 << 40},
		{maxSamples, maxSamples},
		{0, 10},
		{10, -1},
	} {
		st := &objects.Stream{Dict: objects.Dict{
			"Subtype":    objects.Name("Image"),
			"Width":      objects.Int(tc.w),
			"Height":     objects.Int(tc.h),
			"ColorSpace": objects.Name("DeviceRGB"),
		}}
		s := &memStore{
			objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
			pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
		}
		ims, err := NewReader(s).Images()
		if err != nil {
			t.Fatal(err)
		}
		if len(ims) != 0 {
			t.Errorf("%dx%d was accepted", tc.w, tc.h)
		}
	}
}

// The filter chain decides the codec, and DecodeChain stops at the first image
// filter — which is how a Flate-then-DCT stream yields a decompressed JPEG rather
// than either a compressed one or an error.
func TestCodecFromFilterChain(t *testing.T) {
	for _, tc := range []struct {
		filters []objects.Name
		want    Codec
	}{
		{nil, CodecRaw},
		{[]objects.Name{"FlateDecode"}, CodecRaw},
		{[]objects.Name{"DCTDecode"}, CodecJPEG},
		{[]objects.Name{"DCT"}, CodecJPEG},
		{[]objects.Name{"CCITTFaxDecode"}, CodecCCITT},
		{[]objects.Name{"JBIG2Decode"}, CodecJBIG2},
		{[]objects.Name{"JPXDecode"}, CodecJPX},
		// The chain stops at the image filter wherever it sits.
		{[]objects.Name{"ASCII85Decode", "DCTDecode"}, CodecJPEG},
	} {
		st := img(2, 2, nil)
		st.Filters = tc.filters
		if len(tc.filters) > 0 && tc.filters[0] == "FlateDecode" {
			// Flate must be real data or the chain errors; the codec is what is
			// under test, so use a stream that decodes.
			st.Raw = flateOf(make([]byte, 12))
		}
		s := &memStore{
			objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
			pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
		}
		ims, err := NewReader(s).Images()
		if err != nil || len(ims) != 1 {
			t.Fatalf("%v: got %d images, %v", tc.filters, len(ims), err)
		}
		if ims[0].Codec != tc.want {
			t.Errorf("%v: codec = %s, want %s", tc.filters, ims[0].Codec, tc.want)
		}
	}
}

// A stencil mask's /BitsPerComponent is optional and must be 1 (§8.9.6.2). Forcing
// it keeps the row stride right when a producer leaves it out or writes something
// else.
func TestStencilForcesOneBitPerComponent(t *testing.T) {
	st := &objects.Stream{Dict: objects.Dict{
		"Subtype":          objects.Name("Image"),
		"Width":            objects.Int(8),
		"Height":           objects.Int(1),
		"ImageMask":        objects.Bool(true),
		"BitsPerComponent": objects.Int(8), // wrong, and must be ignored
	}, Raw: []byte{0xFF}}
	s := &memStore{
		objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	ims, _ := NewReader(s).Images()
	if len(ims) != 1 {
		t.Fatalf("got %d images", len(ims))
	}
	if !ims[0].Stencil {
		t.Error("Stencil = false with /ImageMask true")
	}
	if ims[0].BitsPerComponent != 1 {
		t.Errorf("bpc = %d, want 1", ims[0].BitsPerComponent)
	}
	if ims[0].Components != 1 {
		t.Errorf("components = %d, want 1", ims[0].Components)
	}
}

func TestColorSpaceComponentCounts(t *testing.T) {
	for _, tc := range []struct {
		cs   objects.Object
		fam  objects.Name
		want int
	}{
		{objects.Name("DeviceGray"), "DeviceGray", 1},
		{objects.Name("DeviceRGB"), "DeviceRGB", 3},
		{objects.Name("DeviceCMYK"), "DeviceCMYK", 4},
		{objects.Name("G"), "G", 1},
		{objects.Name("CMYK"), "CMYK", 4},
		{objects.Array{objects.Name("CalRGB"), objects.Dict{}}, "CalRGB", 3},
		{objects.Array{objects.Name("Lab"), objects.Dict{}}, "Lab", 3},
		{objects.Array{objects.Name("Separation"), objects.Name("Spot")}, "Separation", 1},
		{objects.Array{objects.Name("DeviceN"),
			objects.Array{objects.Name("a"), objects.Name("b")}}, "DeviceN", 2},
		// A space this package does not recognize yields 0, which blocks
		// re-encoding rather than guessing a stride.
		{objects.Name("VendorSpace"), "VendorSpace", 0},
	} {
		st := img(2, 2, objects.Dict{"ColorSpace": tc.cs})
		s := &memStore{
			objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
			pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
		}
		ims, _ := NewReader(s).Images()
		if len(ims) != 1 {
			t.Fatalf("%v: got %d images", tc.cs, len(ims))
		}
		if ims[0].Components != tc.want {
			t.Errorf("%v: components = %d, want %d", tc.cs, ims[0].Components, tc.want)
		}
		if ims[0].ColorSpaceFamily != tc.fam {
			t.Errorf("%v: family = %s, want %s", tc.cs, ims[0].ColorSpaceFamily, tc.fam)
		}
	}
}

// ICCBased is 2 of the corpus's 239 images, and its component count lives in the
// profile stream's /N — the only place it is stated (§8.6.5.5).
func TestICCBasedReadsComponentCountFromN(t *testing.T) {
	profile := &objects.Stream{Dict: objects.Dict{"N": objects.Int(4)}}
	st := img(2, 2, objects.Dict{
		"ColorSpace": objects.Array{objects.Name("ICCBased"), objects.Ref{Num: 5}},
	})
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: st,
			{Num: 5}: profile,
		},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	ims, _ := NewReader(s).Images()
	if len(ims) != 1 {
		t.Fatalf("got %d images", len(ims))
	}
	if ims[0].Components != 4 {
		t.Errorf("components = %d, want 4 from the profile's /N", ims[0].Components)
	}

	// A malformed ICCBased array must not panic on the missing element.
	bad := img(2, 2, objects.Dict{"ColorSpace": objects.Array{objects.Name("ICCBased")}})
	s2 := &memStore{
		objs:  map[objects.Ref]objects.Object{{Num: 1}: bad},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	if ims, _ := NewReader(s2).Images(); len(ims) != 1 || ims[0].Components != 0 {
		t.Errorf("truncated ICCBased array: got %d images", len(ims))
	}
}

// An /Indexed lookup table may be a string or a stream, and both occur.
func TestIndexedPaletteFromStringOrStream(t *testing.T) {
	palette := []byte{1, 2, 3, 4, 5, 6}
	for _, lookup := range []objects.Object{
		objects.String(palette),
		&objects.Stream{Dict: objects.Dict{}, Raw: palette},
	} {
		st := img(2, 2, objects.Dict{
			"ColorSpace": objects.Array{
				objects.Name("Indexed"), objects.Name("DeviceRGB"),
				objects.Int(1), lookup,
			},
		})
		s := &memStore{
			objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
			pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
		}
		ims, _ := NewReader(s).Images()
		if len(ims) != 1 {
			t.Fatalf("got %d images", len(ims))
		}
		im := ims[0]
		if im.Components != 1 {
			t.Errorf("indexed components = %d, want 1", im.Components)
		}
		if im.Base != "DeviceRGB" || im.HiVal != 1 {
			t.Errorf("base = %s, hival = %d; want DeviceRGB, 1", im.Base, im.HiVal)
		}
		if string(im.Palette) != string(palette) {
			t.Errorf("palette = %v, want %v", im.Palette, palette)
		}
	}
}

// A soft mask is read as its own nested image, and /Matte travels with it because
// its presence changes what the base samples mean.
func TestSoftMaskAndMatteAreRead(t *testing.T) {
	mask := &objects.Stream{Dict: objects.Dict{
		"Subtype":          objects.Name("Image"),
		"Width":            objects.Int(2),
		"Height":           objects.Int(2),
		"BitsPerComponent": objects.Int(8),
		"ColorSpace":       objects.Name("DeviceGray"),
		"Matte":            objects.Array{objects.Int(0), objects.Int(0), objects.Int(0)},
	}, Raw: []byte{0, 0, 0, 0}}
	st := img(2, 2, objects.Dict{"SMask": objects.Ref{Num: 9}})
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: st,
			{Num: 9}: mask,
		},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	ims, _ := NewReader(s).Images()
	if len(ims) != 1 {
		t.Fatalf("got %d images", len(ims))
	}
	im := ims[0]
	if im.SMask == nil {
		t.Fatal("no soft mask read")
	}
	if !im.Alpha() {
		t.Error("Alpha() = false with a soft mask")
	}
	if len(im.SMask.Matte) != 3 {
		t.Errorf("/Matte = %v, want three components", im.SMask.Matte)
	}
	if !im.Premultiplied() {
		t.Error("Premultiplied() = false with /Matte present")
	}
	// The mask is not entered into the dedup set: one shared between two base
	// images has to appear with both, and it is not an image of its own.
	if len(ims) != 1 {
		t.Errorf("the mask leaked into the top-level list: %d images", len(ims))
	}
}

// /DecodeParms is positionally aligned with /Filter, so in a Flate-then-CCITT
// chain the CCITT parameters are the second entry. Reading the first applies a
// predictor's parameters to the fax decoder, which decodes at the wrong width.
func TestCCITTParamsPositionInChain(t *testing.T) {
	st := img(8, 1, objects.Dict{
		"DecodeParms": objects.Array{
			objects.Dict{"Predictor": objects.Int(12)},
			objects.Dict{"K": objects.Int(-1), "Columns": objects.Int(8),
				"BlackIs1": objects.Bool(true)},
		},
	})
	st.Filters = []objects.Name{"FlateDecode", "CCITTFaxDecode"}
	st.Raw = flateOf([]byte{0x36, 0xC0})
	s := &memStore{
		objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	ims, _ := NewReader(s).Images()
	if len(ims) != 1 {
		t.Fatalf("got %d images", len(ims))
	}
	im := ims[0]
	if im.Codec != CodecCCITT {
		t.Fatalf("codec = %s, want ccitt", im.Codec)
	}
	if im.CCITT.K != -1 || im.CCITT.Columns != 8 || !im.CCITT.BlackIs1 {
		t.Errorf("params = %+v, want the second /DecodeParms entry", im.CCITT)
	}
}

// A single /DecodeParms dictionary applies to the CCITT filter even when the chain
// has other entries before it, because that form is not positional.
func TestCCITTParamsSingleDict(t *testing.T) {
	st := img(8, 1, objects.Dict{
		"DecodeParms": objects.Dict{"K": objects.Int(-1), "Columns": objects.Int(8)},
	})
	st.Filters = []objects.Name{"CCITTFaxDecode"}
	st.Raw = []byte{0x36, 0xC0}
	s := &memStore{
		objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	ims, _ := NewReader(s).Images()
	if len(ims) != 1 || ims[0].CCITT.Columns != 8 || ims[0].CCITT.K != -1 {
		t.Errorf("params = %+v", ims[0].CCITT)
	}
}

// A CCITT stream with no /DecodeParms gets the spec's defaults, and /Columns
// defaulting to 1728 rather than to the image's own width is the surprising one.
func TestCCITTParamsAbsentUsesDefaults(t *testing.T) {
	st := img(8, 1, nil)
	st.Filters = []objects.Name{"CCITTFaxDecode"}
	st.Raw = []byte{0x36, 0xC0}
	s := &memStore{
		objs:  map[objects.Ref]objects.Object{{Num: 1}: st},
		pages: []objects.Dict{pageWith(objects.Dict{"Im0": objects.Ref{Num: 1}})},
	}
	ims, _ := NewReader(s).Images()
	if len(ims) != 1 {
		t.Fatalf("got %d images", len(ims))
	}
	if ims[0].CCITT != defaultCCITT {
		t.Errorf("params = %+v, want %+v", ims[0].CCITT, defaultCCITT)
	}
}

// Page-scoped reads are still deduplicated document-wide, because the caller may
// walk pages itself and must not get the same image twice.
func TestPageIsDeduplicatedAcrossCalls(t *testing.T) {
	shared := objects.Ref{Num: 1}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{shared: img(2, 2, nil)},
		pages: []objects.Dict{
			pageWith(objects.Dict{"Im0": shared}),
			pageWith(objects.Dict{"Im0": shared}),
		},
	}
	r := NewReader(s)
	first, _ := r.Page(1)
	second, _ := r.Page(2)
	if len(first) != 1 {
		t.Errorf("page 1: got %d, want 1", len(first))
	}
	if len(second) != 0 {
		t.Errorf("page 2: got %d, want 0 — the image was already reported", len(second))
	}
}

// A page with no /Resources, no /XObject, or a non-stream XObject entry is a
// routine shape in real files, not an error.
func TestDegenerateResourcesYieldNothing(t *testing.T) {
	for _, page := range []objects.Dict{
		{},
		{"Resources": objects.Dict{}},
		{"Resources": objects.Dict{"XObject": objects.Dict{}}},
		{"Resources": objects.Dict{"XObject": objects.Dict{"Im0": objects.Name("bogus")}}},
		{"Resources": objects.Dict{"XObject": objects.Dict{"Im0": objects.Ref{Num: 404}}}},
	} {
		s := &memStore{objs: map[objects.Ref]objects.Object{}, pages: []objects.Dict{page}}
		ims, err := NewReader(s).Images()
		if err != nil {
			t.Errorf("%v: %v", page, err)
		}
		if len(ims) != 0 {
			t.Errorf("%v: got %d images, want 0", page, len(ims))
		}
	}
}

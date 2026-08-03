package image

import (
	"bytes"
	stdimage "image"
	"image/color"
	"image/png"
	"testing"
)

// pixels renders im through Encode and reads the PNG back, so the assertions are
// against the bytes a caller actually gets rather than against an intermediate.
func pixels(t *testing.T, im *Image) stdimage.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, im); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := png.Decode(&buf)
	if err != nil {
		t.Fatalf("the PNG this package wrote does not decode: %v", err)
	}
	return got
}

func rgba(t *testing.T, img stdimage.Image, x, y int) color.NRGBA {
	t.Helper()
	r, g, b, a := img.At(x, y).RGBA()
	if a == 0 {
		return color.NRGBA{}
	}
	// Undo the alpha premultiplication image/color applies, so the assertions read
	// as the colours they are.
	return color.NRGBA{
		R: uint8(r * 0xFFFF / a >> 8),
		G: uint8(g * 0xFFFF / a >> 8),
		B: uint8(b * 0xFFFF / a >> 8),
		A: uint8(a >> 8),
	}
}

// A JPEG is copied out byte for byte. This is the package's central promise: 56 of
// the corpus's 245 images are DCT and the largest is 6049x4090, so a re-encode
// would cost a quality generation and seconds of CPU for a file that was already
// a JPEG.
func TestJPEGPassesThroughUnchanged(t *testing.T) {
	// Not a real JPEG. That is the point: Encode must not parse it, so a stream
	// this build cannot decode still comes out intact.
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x99, 0xFF, 0xD9}
	im := &Image{Codec: CodecJPEG, Data: data, Width: 1, Height: 1}

	var buf bytes.Buffer
	if err := Encode(&buf, im); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("JPEG was rewritten:\n got %x\nwant %x", buf.Bytes(), data)
	}
	if ext, err := im.Ext(); err != nil || ext != "jpg" {
		t.Errorf("Ext = %q, %v; want jpg", ext, err)
	}
}

// 8-bit RGB is the corpus's dominant case: 233 of 245 images are DeviceRGB and
// 241 are 8 bits per component.
func TestRGB8(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 2, Height: 2, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		Data: []byte{
			255, 0, 0, 0, 255, 0,
			0, 0, 255, 255, 255, 255,
		},
	}
	img := pixels(t, im)
	for _, tc := range []struct {
		x, y int
		want color.NRGBA
	}{
		{0, 0, color.NRGBA{R: 255, A: 255}},
		{1, 0, color.NRGBA{G: 255, A: 255}},
		{0, 1, color.NRGBA{B: 255, A: 255}},
		{1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255}},
	} {
		if got := rgba(t, img, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
		}
	}
}

// Sub-byte samples are the reason bits exists. A row of 1-bit samples is padded to
// a byte boundary, so a 3-pixel row is one byte with five bits of padding — and an
// implementation that read the stream as a continuous bit sequence would shift
// every row after the first, producing a diagonal skew that looks like a decoder
// bug rather than an alignment one.
func TestRowPaddingAtOneBitPerSample(t *testing.T) {
	// 3x3 DeviceGray at 1 bpc. Each row is one byte, three significant bits.
	// Rows: 101, 010, 111.
	im := &Image{
		Codec: CodecRaw, Width: 3, Height: 3, BitsPerComponent: 1,
		ColorSpaceFamily: "DeviceGray", Components: 1,
		Data: []byte{0b10100000, 0b01000000, 0b11100000},
	}
	img := pixels(t, im)
	want := [3][3]uint8{
		{255, 0, 255},
		{0, 255, 0},
		{255, 255, 255},
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			got := rgba(t, img, x, y)
			if got.R != want[y][x] {
				t.Errorf("(%d,%d) = %d, want %d — rows are not byte-aligned",
					x, y, got.R, want[y][x])
			}
		}
	}
}

// 4-bit samples pack two to a byte, and a row of three needs two bytes with the
// second half-padded.
func TestFourBitSamplesAndPadding(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 3, Height: 2, BitsPerComponent: 4,
		ColorSpaceFamily: "DeviceGray", Components: 1,
		// Row 0: 0x0, 0xF, 0x8 then padding. Row 1: 0xF, 0x0, 0xF.
		Data: []byte{0x0F, 0x80, 0xF0, 0xF0},
	}
	img := pixels(t, im)
	// 0xF of 15 is 1.0 -> 255; 0x8 of 15 is 0.533 -> 136.
	want := [2][3]uint8{{0, 255, 136}, {255, 0, 255}}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			if got := rgba(t, img, x, y).R; got != want[y][x] {
				t.Errorf("(%d,%d) = %d, want %d", x, y, got, want[y][x])
			}
		}
	}
}

// /Decode remaps sample values, and [1 0] on a greyscale image inverts it.
func TestDecodeArrayInverts(t *testing.T) {
	base := func(dec []float64) *Image {
		return &Image{
			Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data: []byte{0, 255}, Decode: dec,
		}
	}
	plain := pixels(t, base(nil))
	if got := rgba(t, plain, 0, 0).R; got != 0 {
		t.Errorf("no /Decode: first sample = %d, want 0", got)
	}
	inv := pixels(t, base([]float64{1, 0}))
	if got := rgba(t, inv, 0, 0).R; got != 255 {
		t.Errorf("/Decode [1 0]: first sample = %d, want 255", got)
	}
	if got := rgba(t, inv, 1, 0).R; got != 0 {
		t.Errorf("/Decode [1 0]: second sample = %d, want 0", got)
	}
	// A /Decode whose length does not match the component count is a producer
	// error, and applying it partially would recolour some channels and not others.
	odd := pixels(t, base([]float64{1, 0, 1}))
	if got := rgba(t, odd, 0, 0).R; got != 0 {
		t.Errorf("mismatched /Decode was applied anyway: got %d, want 0", got)
	}
}

// A stencil mask carries no colour of its own — it paints the graphics state's
// fill colour, which an extractor does not know. Black-on-transparent shows the
// shape without inventing a colour, and /Decode [1 0] reverses which samples paint.
func TestStencilMaskIsTransparentWhereUnpainted(t *testing.T) {
	data := []byte{0b01000000} // sample 0 paints, sample 1 does not
	im := &Image{
		Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 1,
		Stencil: true, Components: 1, Data: data,
	}
	img := pixels(t, im)
	if got := rgba(t, img, 0, 0); got.A != 255 {
		t.Errorf("sample 0 should paint: alpha = %d", got.A)
	}
	if got := rgba(t, img, 1, 0); got.A != 0 {
		t.Errorf("sample 1 should not paint: alpha = %d", got.A)
	}

	inv := &Image{
		Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 1,
		Stencil: true, Components: 1, Data: data, Decode: []float64{1, 0},
	}
	img = pixels(t, inv)
	if got := rgba(t, img, 0, 0); got.A != 0 {
		t.Errorf("/Decode [1 0]: sample 0 should not paint: alpha = %d", got.A)
	}
	if got := rgba(t, img, 1, 0); got.A != 255 {
		t.Errorf("/Decode [1 0]: sample 1 should paint: alpha = %d", got.A)
	}
}

// A soft mask supplies per-sample alpha, which is the corpus's common case at 143
// of 245 images.
func TestSoftMaskBecomesAlpha(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		Data: []byte{255, 0, 0, 0, 0, 255},
		SMask: &Image{
			Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data: []byte{255, 0},
		},
	}
	if !im.Alpha() {
		t.Error("Alpha() = false with an /SMask present")
	}
	img := pixels(t, im)
	if got := rgba(t, img, 0, 0); got.A != 255 || got.R != 255 {
		t.Errorf("opaque sample = %v, want fully opaque red", got)
	}
	if got := rgba(t, img, 1, 0); got.A != 0 {
		t.Errorf("masked sample alpha = %d, want 0", got.A)
	}
}

// 4 of the corpus's 143 soft masks are a different size from their base image, so
// the coordinates are scaled rather than assumed to match. Assuming they match
// reads outside a smaller mask's buffer.
func TestSoftMaskOfDifferentSizeIsScaled(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 4, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceGray", Components: 1,
		Data: []byte{255, 255, 255, 255},
		SMask: &Image{
			// Half the width: each mask sample covers two base samples.
			Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data: []byte{255, 0},
		},
	}
	img := pixels(t, im)
	for x, want := range []uint8{255, 255, 0, 0} {
		if got := rgba(t, img, x, 0).A; got != want {
			t.Errorf("x=%d alpha = %d, want %d", x, got, want)
		}
	}
}

// /Matte says the base samples are premultiplied against a colour, so they are not
// the colours they appear to be. 136 of the corpus's 143 soft masks carry it, all
// [0 0 0]. Encode does not un-premultiply — that is a rendering decision — but the
// condition has to be reportable, or a consumer composites wrong and never knows.
func TestPremultipliedIsReported(t *testing.T) {
	plain := &Image{SMask: &Image{}}
	if plain.Premultiplied() {
		t.Error("no /Matte reported as premultiplied")
	}
	matted := &Image{SMask: &Image{Matte: []float64{0, 0, 0}}}
	if !matted.Premultiplied() {
		t.Error("/Matte [0 0 0] not reported as premultiplied")
	}
	// No mask at all cannot be premultiplied, whatever else is set.
	if (&Image{}).Premultiplied() {
		t.Error("an image with no /SMask reported as premultiplied")
	}
}

// An /Indexed image stores one index per sample and expands it through a palette,
// so the component count is 1 regardless of the base space's.
func TestIndexedPalette(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 3, Height: 1, BitsPerComponent: 2,
		ColorSpaceFamily: "Indexed", Base: "DeviceRGB", Components: 1, HiVal: 2,
		Palette: []byte{255, 0, 0, 0, 255, 0, 0, 0, 255},
		// Indices 0, 1, 2 at 2 bits each: 00 01 10, then padding.
		Data: []byte{0b00011000},
	}
	img := pixels(t, im)
	want := []color.NRGBA{
		{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255},
	}
	for x, w := range want {
		if got := rgba(t, img, x, 0); got != w {
			t.Errorf("x=%d = %v, want %v", x, got, w)
		}
	}

	// An index past /HiVal is legal and clamps (§8.6.6.3) rather than reading off
	// the end of the palette.
	over := &Image{
		Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 2,
		ColorSpaceFamily: "Indexed", Base: "DeviceRGB", Components: 1, HiVal: 1,
		Palette: []byte{255, 0, 0, 0, 255, 0},
		Data:    []byte{0b11000000}, // index 3, past HiVal 1
	}
	if got := rgba(t, pixels(t, over), 0, 0); got != (color.NRGBA{G: 255, A: 255}) {
		t.Errorf("out-of-range index = %v, want the clamped entry (green)", got)
	}
}

func TestCMYK(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceCMYK", Components: 4,
		// Pure cyan, then pure black.
		Data: []byte{255, 0, 0, 0, 0, 0, 0, 255},
	}
	img := pixels(t, im)
	if got := rgba(t, img, 0, 0); got.R != 0 || got.G != 255 || got.B != 255 {
		t.Errorf("cyan = %v, want R=0 G=255 B=255", got)
	}
	if got := rgba(t, img, 1, 0); got.R != 0 || got.G != 0 || got.B != 0 {
		t.Errorf("K=1 = %v, want black", got)
	}
}

// A truncated stream is a damaged file, not a hostile one, and the rows recovered
// before the break are real. This must not panic or read past the buffer.
func TestTruncatedDataYieldsPartialImage(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 4, Height: 4, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		Data: []byte{255, 0, 0, 0, 255, 0}, // two of sixteen pixels
	}
	img := pixels(t, im)
	if b := img.Bounds(); b.Dx() != 4 || b.Dy() != 4 {
		t.Errorf("bounds = %v, want the declared 4x4", b)
	}
	if got := rgba(t, img, 0, 0); got.R != 255 {
		t.Errorf("the recovered prefix was discarded: (0,0) = %v", got)
	}
	if got := rgba(t, img, 3, 3); got.A != 0 {
		t.Errorf("beyond the data should be transparent, got %v", got)
	}
}

// A colour space this package cannot count components for sets no stride, and a
// wrong stride does not make a slightly wrong image — it makes a diagonal smear.
// Declining is the correct outcome.
func TestUnknownComponentCountIsRefused(t *testing.T) {
	im := &Image{
		Codec: CodecRaw, Width: 2, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "SomeVendorSpace", Components: 0,
		Data: []byte{1, 2, 3, 4, 5, 6},
	}
	if err := Encode(&bytes.Buffer{}, im); err == nil {
		t.Fatal("want an error for an uncountable colour space")
	}
}

// JBIG2 and JPX are recognized so a report can name them, and refused so a caller
// is never handed a file with the wrong contents. Neither appears in this corpus.
func TestUnsupportedCodecsAreNamedAndRefused(t *testing.T) {
	for _, c := range []Codec{CodecJBIG2, CodecJPX} {
		im := &Image{Codec: c, Width: 1, Height: 1, Data: []byte{0}}
		if _, err := im.Ext(); err == nil {
			t.Errorf("%s: Ext returned an extension for a codec that cannot be written", c)
		}
		if err := Encode(&bytes.Buffer{}, im); err == nil {
			t.Errorf("%s: Encode succeeded", c)
		}
		if c.String() == "unknown" {
			t.Errorf("codec %d has no name, so no report can say what the file holds", c)
		}
	}
}

// No image in this repo's corpus is CCITT, so these fixtures are built from the
// ITU T.6 code tables by hand rather than taken from a file. That makes them a
// weaker guarantee than the rest of the package's tests — they check that the
// parameters are wired to the decoder correctly, not that the decoder is right —
// and it is the reason CCITT is borrowed rather than owned.
//
// 0x36 0xC0 is Group 4 horizontal mode: 001 (horizontal) + 1011 (white run 4) +
// 011 (black run 4), which is 0011 0110 11 padded to two bytes.
func TestCCITTGroup4(t *testing.T) {
	const g4FourWhiteFourBlack = "\x36\xC0"
	im := &Image{
		Codec: CodecCCITT, Width: 8, Height: 1, BitsPerComponent: 1,
		ColorSpaceFamily: "DeviceGray", Components: 1,
		Data:  []byte(g4FourWhiteFourBlack),
		CCITT: CCITTParams{K: -1, Columns: 8},
	}
	if ext, err := im.Ext(); err != nil || ext != "png" {
		t.Fatalf("Ext = %q, %v; want png", ext, err)
	}
	img := pixels(t, im)
	// PDF's default polarity is 0 for black, so the first four samples are white.
	for x := 0; x < 4; x++ {
		if got := rgba(t, img, x, 0).R; got != 255 {
			t.Errorf("x=%d = %d, want 255 (white)", x, got)
		}
	}
	for x := 4; x < 8; x++ {
		if got := rgba(t, img, x, 0).R; got != 0 {
			t.Errorf("x=%d = %d, want 0 (black)", x, got)
		}
	}
}

// /BlackIs1 inverts the polarity, and getting it backwards produces a photographic
// negative — a failure that is obvious on inspection and invisible to a test that
// only checks the decode succeeded.
func TestCCITTBlackIs1Inverts(t *testing.T) {
	data := []byte{0x36, 0xC0}
	base := func(blackIs1 bool) *Image {
		return &Image{
			Codec: CodecCCITT, Width: 8, Height: 1, BitsPerComponent: 1,
			ColorSpaceFamily: "DeviceGray", Components: 1, Data: data,
			CCITT: CCITTParams{K: -1, Columns: 8, BlackIs1: blackIs1},
		}
	}
	normal := rgba(t, pixels(t, base(false)), 0, 0).R
	inverted := rgba(t, pixels(t, base(true)), 0, 0).R
	if normal == inverted {
		t.Fatalf("/BlackIs1 changed nothing: both %d", normal)
	}
	if normal != 255 || inverted != 0 {
		t.Errorf("default = %d (want 255), /BlackIs1 = %d (want 0)", normal, inverted)
	}
}

// Group 4 codes rows against the row above, so a two-row image exercises the
// vertical modes a single-row fixture cannot reach. 0xC0 is V0 (1) for each of two
// all-white rows against an imaginary white reference line.
func TestCCITTMultipleRows(t *testing.T) {
	im := &Image{
		Codec: CodecCCITT, Width: 8, Height: 2, BitsPerComponent: 1,
		ColorSpaceFamily: "DeviceGray", Components: 1,
		Data:  []byte{0xC0},
		CCITT: CCITTParams{K: -1, Columns: 8},
	}
	img := pixels(t, im)
	if b := img.Bounds(); b.Dy() != 2 {
		t.Fatalf("bounds = %v, want 2 rows", b)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			if got := rgba(t, img, x, y).R; got != 255 {
				t.Errorf("(%d,%d) = %d, want 255", x, y, got)
			}
		}
	}
}

// A truncated CCITT stream keeps its recovered rows, and those rows have to carry
// the same polarity a complete decode would. x/image/ccitt applies Invert only
// after its row loop finishes, so the error path is a second place the polarity can
// be wrong — and it is invisible to a test that only decodes clean fixtures.
func TestCCITTTruncatedStreamKeepsPolarity(t *testing.T) {
	// One row of eight white samples, declared as four rows. Rows 2-4 have no data.
	base := func(blackIs1 bool) *Image {
		return &Image{
			Codec: CodecCCITT, Width: 8, Height: 4, BitsPerComponent: 1,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data:  []byte{0xC0},
			CCITT: CCITTParams{K: -1, Columns: 8, BlackIs1: blackIs1},
		}
	}
	// Without /BlackIs1 the recovered row is white, matching TestCCITTMultipleRows.
	if got := rgba(t, pixels(t, base(false)), 0, 0).R; got != 255 {
		t.Errorf("truncated, default polarity: (0,0) = %d, want 255", got)
	}
	// With it, the same row must come out black — not left uninverted at 255.
	if got := rgba(t, pixels(t, base(true)), 0, 0).R; got != 0 {
		t.Errorf("truncated, /BlackIs1: (0,0) = %d, want 0 — the partial rows were not inverted", got)
	}
}

// A CCITT stream carries no width, so /Columns is load-bearing in a way a JPEG's
// dimensions are not. The default is 1728, which is almost never the image's own
// width — so an implementation that defaulted /Columns and then trusted it would
// decode every CCITT image at fax-page width.
func TestCCITTColumnsDefaultIsFaxWidth(t *testing.T) {
	if defaultCCITT.Columns != 1728 {
		t.Errorf("default /Columns = %d, want 1728 per §7.4.6", defaultCCITT.Columns)
	}
	if defaultCCITT.K != 0 || defaultCCITT.BlackIs1 || defaultCCITT.EncodedByteAlign {
		t.Errorf("defaults drifted from §7.4.6: %+v", defaultCCITT)
	}
}

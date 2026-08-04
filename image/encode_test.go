package image

import (
	"bytes"
	stdimage "image"
	"image/color"
	"image/png"
	"math"
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

// /Matte says the base samples are pre-blended against a colour, so they are not the
// colours they appear to be. 136 of the corpus's 143 soft masks carry it, all [0 0 0].
// The condition has to be reportable whether or not Encode can act on it, because a
// consumer that composites blended samples again gets a colour shift and no indication.
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

// matted builds a base image whose samples are pre-blended against matte at the given
// per-pixel alpha, by running §11.6.5.3's forward computation. Building the input with
// the forward formula and asserting the output against the original c is what makes the
// test about the inversion rather than about a hand-computed constant.
func matted(orig [][3]float64, alpha []uint8, matte []float64) *Image {
	data := make([]byte, 0, len(orig)*3)
	for i, c := range orig {
		a := float64(alpha[i]) / 255
		for k := 0; k < 3; k++ {
			// c′ = m + α × (c − m)
			blended := matte[k] + a*(c[k]-matte[k])
			data = append(data, clamp8(blended))
		}
	}
	return &Image{
		Codec: CodecRaw, Width: len(orig), Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		Data: data,
		SMask: &Image{
			Codec: CodecRaw, Width: len(orig), Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data:  append([]byte(nil), alpha...),
			Matte: matte,
		},
	}
}

// The core of the fix: pre-blended samples are inverted back to the colours the file's
// author had, per §11.6.5.3's c = m + (c′ − m) / α.
//
// Without the inversion a half-transparent red over a black matte is stored as 0x80,0,0
// and written out as that — a dark red at 50% alpha, which composites to a quarter-
// intensity red instead of a half-intensity one. The stored value is not the colour.
func TestMatteIsUnblended(t *testing.T) {
	black := []float64{0, 0, 0}
	orig := [][3]float64{
		{1, 0, 0},      // saturated red
		{1, 0, 0},      // the same red, at a different alpha
		{0.5, 0.25, 1}, // an arbitrary colour
		{1, 1, 1},      // white, the worst case for amplification
	}
	alpha := []uint8{255, 128, 64, 32}
	im := matted(orig, alpha, black)

	if !im.Premultiplied() {
		t.Fatal("Premultiplied() = false for an image built with a /Matte")
	}
	if !im.Recoverable() {
		t.Fatal("Recoverable() = false for a raw image with a raw mask and a 3-component matte")
	}

	img := pixels(t, im)
	for x, want := range orig {
		got := rgba(t, img, x, 0)
		if got.A != alpha[x] {
			t.Errorf("x=%d alpha = %d, want %d", x, got.A, alpha[x])
		}
		// One 8-bit step of slack per channel. The round trip stores c′ in 8 bits and
		// the inversion multiplies that quantization by 1/α, so at α=32/255 a single
		// stored step is eight recovered ones; asserting exact equality would be
		// asserting that 8-bit storage is lossless, which it is not.
		tol := int(math.Ceil(255 / (float64(alpha[x]) / 255) / 255))
		for k, ch := range []uint8{got.R, got.G, got.B} {
			exp := clamp8(want[k])
			if diff := int(ch) - int(exp); diff > tol || diff < -tol {
				t.Errorf("x=%d channel %d = %d, want %d ±%d (recovered colour is wrong)",
					x, k, ch, exp, tol)
			}
		}
	}
}

// The ordering §11.6.5.3 requires against /Decode: "This computation shall use actual
// colour component values, with the effects of the Filter and Decode transformations
// already performed." So /Matte is in the post-Decode domain — Table 144 calls its numbers
// "valid colour components in that colour space" — and the inversion has to run after the
// remap, not on the raw normalized sample.
//
// Only an inverting /Decode can show the difference, which is why this is a separate test
// from TestMatteIsUnblended: with the default [0 1] per component the remap is the identity
// and either order passes. Here the true colour is a mid grey, /Decode [1 0 1 0 1 0]
// inverts, and the matte is white. Swap the two operations and the recovered value comes
// out on the wrong side of the matte — a visible colour error, not a rounding one.
func TestMatteIsInvertedInPostDecodeUnits(t *testing.T) {
	white := []float64{1, 1, 1}
	const (
		trueVal = 0.6 // the colour the author had, in post-Decode units
		alpha   = 128
	)
	a := float64(alpha) / 255
	// c′ = m + α × (c − m), in post-Decode units.
	blended := white[0] + a*(trueVal-white[0])
	// /Decode [1 0] maps a raw sample v to 1−v, so the byte that yields c′ is 1−c′.
	raw := clamp8(1 - blended)

	im := &Image{
		Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		Decode: []float64{1, 0, 1, 0, 1, 0},
		Data:   []byte{raw, raw, raw},
		SMask: &Image{
			Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data:  []byte{alpha},
			Matte: white,
		},
	}
	if !im.Recoverable() {
		t.Fatal("Recoverable() = false for a raw DeviceRGB image with a raw mask")
	}
	got := rgba(t, pixels(t, im), 0, 0)
	// Two 8-bit steps: one from storing c′, doubled by the 1/α amplification at α=128.
	const tol = 2
	want := clamp8(trueVal)
	for k, ch := range []uint8{got.R, got.G, got.B} {
		if diff := int(ch) - int(want); diff > tol || diff < -tol {
			t.Errorf("channel %d = %d, want %d ±%d — /Decode and the unblend are in different units",
				k, ch, want, tol)
		}
	}
}

// α = 0 is the case §11.6.5.3 calls out: the inverted formula divides by zero, and the
// spec permits "an arbitrary value for c ... within the range of colour component values".
// The matte colour is the choice, so a fully transparent pixel keeps what the file gave
// it. What must never happen is a NaN or an Inf reaching the encoder, which clamp8 would
// silently turn into black or white.
func TestMatteAtZeroAlphaIsTheMatteColour(t *testing.T) {
	matte := []float64{0.25, 0.5, 0.75}
	im := &Image{
		Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		// Whatever a producer left in a fully transparent sample. At α=0 the forward
		// formula makes c′ = m, so this is what a conforming writer stores.
		Data: []byte{clamp8(matte[0]), clamp8(matte[1]), clamp8(matte[2])},
		SMask: &Image{
			Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data: []byte{0}, Matte: matte,
		},
	}
	// Read as NRGBA rather than through rgba(), which goes via At().RGBA() and so
	// premultiplies: at α=0 that returns 0,0,0,0 and the stored colour is unreadable
	// through it. The bytes are in the PNG either way — this asserts what was written.
	// That the colour is invisible to a premultiplied accessor is exactly the spec's
	// point that "the arbitrary value of c does not affect output".
	img := pixels(t, im)
	nrgba, ok := img.(*stdimage.NRGBA)
	if !ok {
		t.Fatalf("PNG decoded to %T, want *image.NRGBA", img)
	}
	got := nrgba.NRGBAAt(0, 0)
	if got.A != 0 {
		t.Fatalf("alpha = %d, want 0", got.A)
	}
	for k, ch := range []uint8{got.R, got.G, got.B} {
		if want := clamp8(matte[k]); ch != want {
			t.Errorf("channel %d = %d, want the matte's %d — a NaN or Inf would land here", k, ch, want)
		}
	}
}

// A non-zero matte, because [0 0 0] — all 136 in the corpus — is the one colour where
// the formula degenerates to a plain divide by alpha and a sign error or a dropped m
// term is invisible.
func TestMatteAgainstANonBlackColour(t *testing.T) {
	white := []float64{1, 1, 1}
	orig := [][3]float64{{0, 0, 0}, {0.2, 0.4, 0.6}}
	alpha := []uint8{128, 200}
	im := matted(orig, alpha, white)

	img := pixels(t, im)
	for x, want := range orig {
		got := rgba(t, img, x, 0)
		tol := int(math.Ceil(255 / (float64(alpha[x]) / 255) / 255))
		for k, ch := range []uint8{got.R, got.G, got.B} {
			exp := clamp8(want[k])
			if diff := int(ch) - int(exp); diff > tol || diff < -tol {
				t.Errorf("x=%d channel %d = %d, want %d ±%d", x, k, ch, exp, tol)
			}
		}
	}
}

// Recoverable is the contract that tells a consumer whether it is looking at recovered
// colour or at samples that had to be passed through blended. Each false here is a case
// where inverting would produce a worse image than not inverting.
func TestRecoverableExclusions(t *testing.T) {
	raw := func(m ...float64) *Image {
		return &Image{
			Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceRGB", Components: 3, Data: []byte{0, 0, 0},
			SMask: &Image{
				Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
				ColorSpaceFamily: "DeviceGray", Components: 1,
				Data: []byte{128}, Matte: m,
			},
		}
	}

	if !(&Image{Codec: CodecJPEG}).Recoverable() {
		t.Error("an image with no /Matte is not recoverable; nothing needs recovering")
	}
	if !raw(0, 0, 0).Recoverable() {
		t.Error("a raw image with a raw mask and a matching matte should be recoverable")
	}

	// A DCT base is never decoded — the codec is preserved byte for byte — so its
	// samples reach the output blended whatever else is true. 5 of the corpus's 136.
	jpegBase := raw(0, 0, 0)
	jpegBase.Codec = CodecJPEG
	if jpegBase.Recoverable() {
		t.Error("a DCT base reported recoverable, but its samples are never decoded")
	}

	// A DCT mask cannot become the per-pixel divisor without running the JPEG decoder
	// the base path exists to avoid.
	jpegMask := raw(0, 0, 0)
	jpegMask.SMask.Codec = CodecJPEG
	if jpegMask.Recoverable() {
		t.Error("a DCT mask reported recoverable, but it supplies no per-pixel alpha")
	}

	// Table 144 requires n components. Inverting some channels and not others is a
	// colour shift unrelated to the file's intent.
	if raw(0, 0).Recoverable() {
		t.Error("a 2-component matte on a 3-component image reported recoverable")
	}

	// Indexed pre-blends the palette, not the index the sample carries.
	idx := raw(0, 0, 0)
	idx.ColorSpaceFamily = "Indexed"
	idx.Components = 3
	if idx.Recoverable() {
		t.Error("an Indexed parent reported recoverable; the blend applies to the palette")
	}

	// Lab's matte is in Lab units against a sample normalized to 0..1.
	lab := raw(0, 0, 0)
	lab.ColorSpaceFamily = "Lab"
	if lab.Recoverable() {
		t.Error("a Lab parent reported recoverable; the matte's units differ from the sample's")
	}

	// Table 143 requires a matted mask to match its parent's dimensions, and all 136 of the
	// corpus's do. A file that breaks it would have alphaAt resampling the mask, and the α
	// that divides would not be the α that multiplied — a guess at the weight rather than
	// the weight. The 4 differently-sized masks in the corpus are all unmatted, so this
	// exclusion costs nothing there and only fires on a malformed file.
	sized := raw(0, 0, 0)
	sized.Width = 2
	sized.Data = []byte{0, 0, 0, 0, 0, 0}
	if sized.Recoverable() {
		t.Error("a matted mask of a different size reported recoverable; §11.6.5.3 forbids the shape")
	}
	// The same shape without a /Matte is legal and must stay unaffected: nothing is being
	// inverted, so a resampled mask is just alpha and the exclusion must not reach it.
	sized.SMask.Matte = nil
	if !sized.Recoverable() {
		t.Error("an unmatted mask of a different size reported unrecoverable")
	}
}

// An unrecoverable image must come out exactly as it did before this behaviour existed:
// blended samples, correct alpha, no arithmetic applied. The failure this guards is a
// half-applied inversion, which is worse than none because it is neither the stored
// value nor the true one.
func TestUnrecoverableIsLeftBlended(t *testing.T) {
	const blended = 0x80 // a half-alpha red pre-blended against black
	im := &Image{
		Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		Data: []byte{blended, 0, 0},
		SMask: &Image{
			Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data: []byte{128},
			// Two components against a three-component space: unrecoverable.
			Matte: []float64{0, 0},
		},
	}
	if im.Recoverable() {
		t.Fatal("Recoverable() = true for a mismatched matte")
	}
	got := rgba(t, pixels(t, im), 0, 0)
	if got.R != blended {
		t.Errorf("R = %d, want the stored %d — an unrecoverable image must pass through", got.R, blended)
	}
	if got.A != 128 {
		t.Errorf("alpha = %d, want 128", got.A)
	}
}

// The other direction of the same guard: a mask with no /Matte at all leaves the samples
// alone. Partial alpha is what makes this test say anything — at 0 or 255 the inversion is
// a no-op or a divide-by-one, so an accidentally applied unblend would pass unnoticed. At
// α=64 against a phantom black matte it would multiply the stored value by four.
//
// Not a hypothetical shape. pymupdf/img-transparent.pdf and 5 images in
// adobe-samples/sampleInvoice.pdf carry exactly this, which the corpus itself does not.
func TestUnmattedSamplesArePassedThrough(t *testing.T) {
	const stored = 0x40
	im := &Image{
		Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceRGB", Components: 3,
		Data: []byte{stored, stored, stored},
		SMask: &Image{
			Codec: CodecRaw, Width: 1, Height: 1, BitsPerComponent: 8,
			ColorSpaceFamily: "DeviceGray", Components: 1,
			Data: []byte{64},
		},
	}
	if im.Premultiplied() {
		t.Fatal("Premultiplied() = true with no /Matte")
	}
	got := rgba(t, pixels(t, im), 0, 0)
	if got.R != stored || got.G != stored || got.B != stored {
		t.Errorf("got %v, want all channels at the stored %#x — an unmatted image must not be unblended",
			got, stored)
	}
	if got.A != 64 {
		t.Errorf("alpha = %d, want 64", got.A)
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

package image

import (
	"bytes"
	"errors"
	stdimage "image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

// grayJPEG is a w×h JPEG of one grey level, which a lossy codec reproduces to within a level or
// two — the only kind of JPEG a test can assert a pixel value of.
func grayJPEG(t *testing.T, w, h int, v uint8) []byte {
	t.Helper()
	g := stdimage.NewGray(stdimage.Rect(0, 0, w, h))
	for i := range g.Pix {
		g.Pix[i] = v
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, g, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func grayRaw(w, h int, v uint8) *Image {
	return &Image{Codec: CodecRaw, Width: w, Height: h, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceGray", Components: 1, Data: bytes.Repeat([]byte{v}, w*h)}
}

func grayDCT(t *testing.T, w, h int, v uint8) *Image {
	return &Image{Codec: CodecJPEG, Width: w, Height: h, BitsPerComponent: 8,
		ColorSpaceFamily: "DeviceGray", Components: 1, Data: grayJPEG(t, w, h, v)}
}

func near(a, b uint8, tol int) bool {
	d := int(a) - int(b)
	return d >= -tol && d <= tol
}

// TestPixelsRefusesWhatEncodeDegrades pins each case Pixels's comment names as an error, because
// each is one where Encode writes *something* and a rasterizer drawing that something would draw a
// wrong picture with nothing to say so.
func TestPixelsRefusesWhatEncodeDegrades(t *testing.T) {
	mismatched := grayRaw(1, 1, 0)
	// A /Matte of three components on a one-component image: no inversion is defined.
	mismatched.SMask = grayRaw(1, 1, 128)
	mismatched.SMask.Matte = []float64{0, 0, 0}

	cmyk := grayDCT(t, 8, 8, 128)
	cmyk.Components = 4
	decoded := grayDCT(t, 8, 8, 128)
	decoded.Decode = []float64{1, 0}

	for _, c := range []struct {
		name string
		im   *Image
		want string
	}{
		{"stencil", &Image{Codec: CodecRaw, Width: 1, Height: 1, Stencil: true, Data: []byte{0}}, "stencil"},
		{"uninvertible matte", mismatched, "/Matte"},
		{"DCT CMYK", cmyk, "CMYK"},
		{"DCT with /Decode", decoded, "/Decode"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Pixels(c.im)
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want ErrUnsupported naming %q", err, c.want)
			}
		})
	}
}

// TestPixelsAcceptsAnIdentityDecode is the counterpart of the /Decode refusal: [0 1] is what the
// absence of /Decode means, so refusing it would refuse a JPEG over nothing.
func TestPixelsAcceptsAnIdentityDecode(t *testing.T) {
	im := grayDCT(t, 8, 8, 128)
	im.Decode = []float64{0, 1}
	if _, err := Pixels(im); err != nil {
		t.Errorf("Pixels: %v, want decoded — [0 1] leaves every sample as it is", err)
	}
}

// TestJPEGHeaderMustAgreeWithTheDictionary pins the check that keeps the one-image sample cap
// meaningful: the decoder allocates what the header says, and the cap was checked against the
// dictionary.
func TestJPEGHeaderMustAgreeWithTheDictionary(t *testing.T) {
	im := grayDCT(t, 8, 8, 128)
	im.Width = 800
	_, err := Pixels(im)
	if err == nil || !strings.Contains(err.Error(), "jpeg header is 8×8 and the dictionary says 800×8") {
		t.Errorf("err = %v, want the header and dictionary sizes named", err)
	}
}

// TestPixelsAppliesEveryMaskPairing pins the soft mask as alpha for each codec pairing, and once.
//
// decodeSamples applies a raw mask to a raw image itself, so Pixels must apply one only for the
// other pairings — a raw pair masked twice at half alpha would come out at a quarter. The DCT
// pairings are the ones Encode never needed, because a JPEG is copied out and its mask with it.
func TestPixelsAppliesEveryMaskPairing(t *testing.T) {
	for _, c := range []struct {
		name       string
		base, mask func() *Image
	}{
		{"raw on raw", func() *Image { return grayRaw(8, 8, 200) }, func() *Image { return grayRaw(8, 8, 128) }},
		{"raw on DCT", func() *Image { return grayDCT(t, 8, 8, 200) }, func() *Image { return grayRaw(8, 8, 128) }},
		{"DCT on raw", func() *Image { return grayRaw(8, 8, 200) }, func() *Image { return grayDCT(t, 8, 8, 128) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			im := c.base()
			im.SMask = c.mask()
			px, err := Pixels(im)
			if err != nil {
				t.Fatalf("Pixels: %v", err)
			}
			got := px.NRGBAAt(4, 4)
			if !near(got.A, 128, 2) || !near(got.R, 200, 2) {
				t.Errorf("centre = %v, want grey 200 at alpha 128", got)
			}
		})
	}
}

// TestPixelsUnblendsAMatteOnADCTBase pins the matte inversion for the pairing decodeSamples
// cannot reach. White pre-blended at half alpha against black is stored as 128; the colour is 255.
func TestPixelsUnblendsAMatteOnADCTBase(t *testing.T) {
	im := grayDCT(t, 8, 8, 128)
	im.SMask = grayRaw(8, 8, 128)
	im.SMask.Matte = []float64{0}
	px, err := Pixels(im)
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	if got := px.NRGBAAt(4, 4); !near(got.R, 255, 3) || !near(got.A, 128, 2) {
		t.Errorf("centre = %v, want white at alpha 128 — the matte was not inverted", got)
	}
}

// TestApplyMaskScalesByNearestNeighbour pins the scaling rule a mask of a different size from its
// image follows, which is alphaAt's: x·mw/bw, so a two-sample mask over four samples covers them
// in halves.
func TestApplyMaskScalesByNearestNeighbour(t *testing.T) {
	dst := stdimage.NewNRGBA(stdimage.Rect(0, 0, 4, 1))
	for x := 0; x < 4; x++ {
		dst.SetNRGBA(x, 0, color.NRGBA{A: 255})
	}
	m := stdimage.NewNRGBA(stdimage.Rect(0, 0, 2, 1))
	m.SetNRGBA(0, 0, color.NRGBA{R: 0, A: 255})
	m.SetNRGBA(1, 0, color.NRGBA{R: 255, A: 255})
	applyMask(dst, m)
	for x, want := range []uint8{0, 0, 255, 255} {
		if got := dst.NRGBAAt(x, 0).A; got != want {
			t.Errorf("x=%d alpha = %d, want %d", x, got, want)
		}
	}
}

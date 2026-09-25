package icc

import (
	"encoding/binary"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/model-harness/pdftools/internal/iccbuild"
)

// sRGB's own D50-relative colorants, 4 decimal places, and its real tone curve, all taken from
// IEC 61966-2-1 and used throughout this file as the profile that should reproduce sRGB exactly.
var (
	srgbR = [3]float64{0.4361, 0.2225, 0.0139}
	srgbG = [3]float64{0.3851, 0.7169, 0.0971}
	srgbB = [3]float64{0.1431, 0.0606, 0.7141}
)

func srgbTRC() []byte { return iccbuild.Para(3, 2.4, 1/1.055, 0.055/1.055, 1/12.92, 0.04045) }

func rgbProfile(r, g, b [3]float64, trc []byte) []byte {
	return iccbuild.Profile("RGB ",
		iccbuild.Tag{Sig: "rXYZ", Data: iccbuild.XYZ(r[0], r[1], r[2])},
		iccbuild.Tag{Sig: "gXYZ", Data: iccbuild.XYZ(g[0], g[1], g[2])},
		iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(b[0], b[1], b[2])},
		iccbuild.Tag{Sig: "rTRC", Data: trc},
		iccbuild.Tag{Sig: "gTRC", Data: trc},
		iccbuild.Tag{Sig: "bTRC", Data: trc},
	)
}

// TestGrayGamma checks the gamma path against the exact math a u8Fixed8 gamma computes, with no
// s15Fixed16 rounding involved, so the tolerance can be as tight as float64 itself: 1e-12.
func TestGrayGamma(t *testing.T) {
	p, err := Parse(iccbuild.Gray(2.2))
	if err != nil {
		t.Fatal(err)
	}
	// iccbuild.Gamma rounds 2.2*256 to the nearest integer, 563, so the curve this profile
	// actually carries is gamma = 563/256, not 2.2.
	gamma := 563.0 / 256
	for _, x := range []float64{0, 0.1, 0.5, 0.9, 1} {
		want := encode(math.Pow(x, gamma))
		r, g, b := p.SRGB([]float64{x})
		if math.Abs(r-want) > 1e-12 || r != g || g != b {
			t.Errorf("Gray(2.2).SRGB(%v) = %v,%v,%v, want %v,%v,%v", x, r, g, b, want, want, want)
		}
	}

	p1, err := Parse(iccbuild.Gray(1.0))
	if err != nil {
		t.Fatal(err)
	}
	r, _, _ := p1.SRGB([]float64{0.5})
	if want := encode(0.5); math.Abs(r-want) > 1e-12 {
		t.Errorf("Gray(1.0).SRGB(0.5) = %v, want %v", r, want)
	}
}

// TestCurveForms exercises every curve form the specification distinguishes: the exact numbers
// come from evaluating each formula by hand and are within a few parts in 1e5 of the parsed
// curve's own s15Fixed16/u8Fixed8/uint16 quantization.
func TestCurveForms(t *testing.T) {
	// curv, no entries: the identity.
	if fn, err := parseCurve(iccbuild.Curve()); err != nil {
		t.Fatal(err)
	} else if got := fn(0.37); math.Abs(got-0.37) > 1e-12 {
		t.Errorf("curv identity at 0.37 = %v, want 0.37", got)
	}

	// curv, one entry: a u8Fixed8 gamma. 128 -> 128/256 = 0.5.
	if fn, err := parseCurve(iccbuild.Gamma(0.5)); err != nil {
		t.Fatal(err)
	} else if got, want := fn(0.25), math.Pow(0.25, 0.5); math.Abs(got-want) > 1e-6 {
		t.Errorf("curv gamma(0.5) at 0.25 = %v, want %v", got, want)
	}

	// curv, a 3-entry table 0, 65535, 0: a tent peaking at x=0.5. At x=0.25, half way between the
	// first two breakpoints (0 and 0.5), interpolation gives (0+1)/2 = 0.5; by symmetry x=0.75
	// gives the same 0.5 on the way back down.
	if fn, err := parseCurve(iccbuild.Curve(0, 65535, 0)); err != nil {
		t.Fatal(err)
	} else {
		if got := fn(0.25); math.Abs(got-0.5) > 1e-9 {
			t.Errorf("curv table at 0.25 = %v, want 0.5", got)
		}
		if got := fn(0.75); math.Abs(got-0.5) > 1e-9 {
			t.Errorf("curv table at 0.75 = %v, want 0.5", got)
		}
	}

	// para type 0: Y = X^g. g=2.2 at x=0.5.
	if fn, err := parseCurve(iccbuild.Para(0, 2.2)); err != nil {
		t.Fatal(err)
	} else if got, want := fn(0.5), math.Pow(0.5, 2.2); math.Abs(got-want) > 1e-4 {
		t.Errorf("para type 0 at 0.5 = %v, want %v", got, want)
	}

	// para type 1: Y=(aX+b)^g if X>=-b/a else 0. a=1, b=-0.2, g=2: breakpoint at X=0.2.
	if fn, err := parseCurve(iccbuild.Para(1, 2, 1, -0.2)); err != nil {
		t.Fatal(err)
	} else {
		if got := fn(0.1); got != 0 {
			t.Errorf("para type 1 below breakpoint = %v, want 0", got)
		}
		if got, want := fn(0.3), math.Pow(0.1, 2); math.Abs(got-want) > 1e-4 {
			t.Errorf("para type 1 above breakpoint = %v, want %v", got, want)
		}
	}

	// para type 2: Y=(aX+b)^g+c if X>=-b/a else c. Same a,b,g as above, c=0.05.
	if fn, err := parseCurve(iccbuild.Para(2, 2, 1, -0.2, 0.05)); err != nil {
		t.Fatal(err)
	} else {
		if got := fn(0.1); math.Abs(got-0.05) > 1e-4 {
			t.Errorf("para type 2 below breakpoint = %v, want 0.05", got)
		}
		if got, want := fn(0.3), math.Pow(0.1, 2)+0.05; math.Abs(got-want) > 1e-4 {
			t.Errorf("para type 2 above breakpoint = %v, want %v", got, want)
		}
	}

	// para type 3: Y=(aX+b)^g if X>=d else cX. a=1,b=0,g=2,c=0.5,d=0.3.
	if fn, err := parseCurve(iccbuild.Para(3, 2, 1, 0, 0.5, 0.3)); err != nil {
		t.Fatal(err)
	} else {
		if got, want := fn(0.1), 0.5*0.1; math.Abs(got-want) > 1e-4 {
			t.Errorf("para type 3 below d = %v, want %v", got, want)
		}
		if got, want := fn(0.5), math.Pow(0.5, 2); math.Abs(got-want) > 1e-4 {
			t.Errorf("para type 3 at/above d = %v, want %v", got, want)
		}
	}

	// para type 4: Y=(aX+b)^g+e if X>=d else cX+f. a=1,b=0,g=2,c=0.5,d=0.3,e=0.01,f=0.02.
	if fn, err := parseCurve(iccbuild.Para(4, 2, 1, 0, 0.5, 0.3, 0.01, 0.02)); err != nil {
		t.Fatal(err)
	} else {
		if got, want := fn(0.1), 0.5*0.1+0.02; math.Abs(got-want) > 1e-4 {
			t.Errorf("para type 4 below d = %v, want %v", got, want)
		}
		if got, want := fn(0.5), math.Pow(0.5, 2)+0.01; math.Abs(got-want) > 1e-4 {
			t.Errorf("para type 4 at/above d = %v, want %v", got, want)
		}
	}
}

// TestSRGBColorantsIdentity checks that the sRGB-to-D50 matrix this package builds at init agrees
// with lcms's own sRGB profile: parsing a profile with sRGB's exact colorants and a pure gamma of
// 1.0 should give back M' ~= I, so SRGB is just enc() per channel. The colorants are stated to only
// four decimal places, so the worst-case error measured across the matrix is a few thousandths, not
// float noise, and that measured bound is what is asserted.
func TestSRGBColorantsIdentity(t *testing.T) {
	p, err := Parse(rgbProfile(srgbR, srgbG, srgbB, iccbuild.Gamma(1.0)))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{0, 0.1, 0.37, 0.5, 0.9, 1} {
		r, g, b := p.SRGB([]float64{v, v, v})
		want := encode(v)
		// Measured worst case for these colorants is a few thousandths; 0.004 gives headroom
		// without hiding a construction error, which would be off by tenths or worse.
		for _, got := range []float64{r, g, b} {
			if math.Abs(got-want) > 0.004 {
				t.Errorf("SRGB(%v,%v,%v) = %v (channel), want %v within 0.004", v, v, v, got, want)
			}
		}
	}
}

// TestSRGBRealTRC repeats the identity check with sRGB's actual parametric tone curve (para type
// 3) instead of a flat gamma, over a grid, and requires the round trip to land within half a level
// at 8 bits, 0.5/255. It checks convert, the computed conversion, because that is what makes this
// profile one isSRGB accepts — and SRGB then returns its input instead.
func TestSRGBRealTRC(t *testing.T) {
	p, err := Parse(rgbProfile(srgbR, srgbG, srgbB, srgbTRC()))
	if err != nil {
		t.Fatal(err)
	}
	if !p.srgb {
		t.Error("isSRGB rejected sRGB's own colorants and tone curve")
	}
	for i := 0; i <= 20; i++ {
		v := float64(i) / 20
		r, g, b := p.convert([]float64{v, v, v})
		for _, got := range []float64{r, g, b} {
			if math.Abs(got-v) > 0.5/255 {
				t.Errorf("SRGB(%v,%v,%v) = %v, want within 0.5/255 of %v", v, v, v, got, v)
			}
		}
	}
}

// TestIsSRGB brackets isSRGB's one-level tolerance from both sides. scaled is sRGB's curve scaled
// down so that white encodes l levels low: encode's power segment is 1.055*y^(1/2.4) - 0.055, so a
// curve scaled by s^2.4 moves every encoded value above the toe by 1.055*(1-s)*y^(1/2.4), and most
// at white, where that is l/255 for s = 1 - l/(1.055*255). Measured on isSRGB's grid, l = 0.9 is
// 0.90 levels off for gray and 0.91 for sRGB's four-decimal colorants, and l = 1.1 is 1.10 and 1.11.
// Scaling leaves black at 0; raising black instead is what black point compensation undoes, which
// TestBlackPointCompensation checks.
func TestIsSRGB(t *testing.T) {
	scaled := func(l float64) []byte {
		s := 1 - l/(1.055*255)
		return iccbuild.Para(3, 2.4, s/1.055, s*0.055/1.055, math.Pow(s, 2.4)/12.92, 0.04045)
	}
	gray := func(trc []byte) []byte { return iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: trc}) }
	oneScaled := func(ch int) []byte {
		trc := [3][]byte{srgbTRC(), srgbTRC(), srgbTRC()}
		trc[ch] = scaled(1.1)
		return iccbuild.Profile("RGB ",
			iccbuild.Tag{Sig: "rXYZ", Data: iccbuild.XYZ(srgbR[0], srgbR[1], srgbR[2])},
			iccbuild.Tag{Sig: "gXYZ", Data: iccbuild.XYZ(srgbG[0], srgbG[1], srgbG[2])},
			iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(srgbB[0], srgbB[1], srgbB[2])},
			iccbuild.Tag{Sig: "rTRC", Data: trc[0]},
			iccbuild.Tag{Sig: "gTRC", Data: trc[1]},
			iccbuild.Tag{Sig: "bTRC", Data: trc[2]})
	}
	adobeR := [3]float64{0.6097, 0.3111, 0.0195}
	adobeG := [3]float64{0.2053, 0.6257, 0.0609}
	adobeB := [3]float64{0.1492, 0.0632, 0.7446}
	for _, c := range []struct {
		name string
		raw  []byte
		want bool
	}{
		{"gray, sRGB curve", gray(srgbTRC()), true},
		{"gray, sRGB curve 0.90 levels low at white", gray(scaled(0.9)), true},
		{"gray, sRGB curve 1.10 levels low at white", gray(scaled(1.1)), false},
		// deficit(x) = (s-1)*(x+0.055) is linear in x (see scaled above), so it is largest at white
		// (x=1) and next largest at the grid's second point from the end (x=15/16). At l=1.02, measured
		// deficit is -0.003992 at white (|.| > tol=1/255=0.003922, fails) and -0.003755 at x=15/16
		// (|.| < tol, passes), and every smaller x passes by a wider margin still: this is the one row
		// in the grid where white is the only failing point, so a loop that stops one short of it
		// (i < steps rather than i <= steps) would never see the failure and wrongly call this sRGB.
		{"gray, sRGB curve 1.02 levels low, only white fails", gray(scaled(1.02)), false},
		{"gray, gamma 2.2", iccbuild.Gray(2.2), false},
		{"sRGB colorants and curve", rgbProfile(srgbR, srgbG, srgbB, srgbTRC()), true},
		{"sRGB colorants, curves 0.91 levels low at white", rgbProfile(srgbR, srgbG, srgbB, scaled(0.9)), true},
		{"sRGB colorants, curves 1.11 levels low at white", rgbProfile(srgbR, srgbG, srgbB, scaled(1.1)), false},
		{"sRGB colorants, red curve scaled", oneScaled(0), false},
		{"sRGB colorants, green curve scaled", oneScaled(1), false},
		{"sRGB colorants, blue curve scaled", oneScaled(2), false},
		{"sRGB colorants, gamma 2.2", rgbProfile(srgbR, srgbG, srgbB, iccbuild.Gamma(2.2)), false},
		{"Adobe RGB colorants, sRGB curve", rgbProfile(adobeR, adobeG, adobeB, srgbTRC()), false},
	} {
		p, err := Parse(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.srgb != c.want {
			t.Errorf("%s: isSRGB = %v, want %v", c.name, p.srgb, c.want)
		}
	}
}

// TestSRGBProfileReturnsItsInput checks what an sRGB profile does once isSRGB accepts it: SRGB
// returns the colour it was given, clamped and with a missing component read as 0, exactly as
// DeviceRGB and DeviceGray would draw it, and Image leaves every pixel as it found it.
func TestSRGBProfileReturnsItsInput(t *testing.T) {
	rgb, err := Parse(rgbProfile(srgbR, srgbG, srgbB, srgbTRC()))
	if err != nil {
		t.Fatal(err)
	}
	gray, err := Parse(iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: srgbTRC()}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		p    *Profile
		in   []float64
		want [3]float64
	}{
		{rgb, []float64{0.3, 0.7, 0.137}, [3]float64{0.3, 0.7, 0.137}},
		{rgb, []float64{-1, 2, math.NaN()}, [3]float64{0, 1, 0}},
		{rgb, []float64{0.5}, [3]float64{0.5, 0, 0}},
		{gray, []float64{0.3}, [3]float64{0.3, 0.3, 0.3}},
		{gray, []float64{2}, [3]float64{1, 1, 1}},
	} {
		r, g, b := c.p.SRGB(c.in)
		if got := [3]float64{r, g, b}; got != c.want {
			t.Errorf("SRGB(%v) = %v, want %v", c.in, got, c.want)
		}
	}

	for _, p := range []*Profile{rgb, gray} {
		img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
		for i := range img.Pix {
			img.Pix[i] = uint8(i * 7)
		}
		want := append([]uint8(nil), img.Pix...)
		p.Image(img)
		for i := range want {
			if img.Pix[i] != want[i] {
				t.Fatalf("Image changed byte %d from %d to %d", i, want[i], img.Pix[i])
			}
		}
	}
}

// TestAdobeRGB checks a real, non-sRGB working space: pure green is outside sRGB's gamut, so the
// matrix step must send at least one channel negative before it is clamped to exactly 0, and a mid
// gray must land close to the same mid gray Gray(2.2) produces, since both are the same tone curve
// applied to a colour with equal channels.
func TestAdobeRGB(t *testing.T) {
	r := [3]float64{0.6097, 0.3111, 0.0195}
	g := [3]float64{0.2053, 0.6257, 0.0609}
	b := [3]float64{0.1492, 0.0632, 0.7446}
	p, err := Parse(rgbProfile(r, g, b, iccbuild.Gamma(2.2)))
	if err != nil {
		t.Fatal(err)
	}

	gr, gg, gb := p.SRGB([]float64{0, 1, 0})
	if gr != 0 {
		t.Errorf("AdobeRGB pure green: R = %v, want exactly 0 (out of gamut, clamped)", gr)
	}
	if gb != 0 {
		t.Errorf("AdobeRGB pure green: B = %v, want exactly 0 (out of gamut, clamped)", gb)
	}
	if gg <= 0 {
		t.Errorf("AdobeRGB pure green: G = %v, want a positive value", gg)
	}

	mr, mg, mb := p.SRGB([]float64{0.5, 0.5, 0.5})
	if math.Abs(mr-mg) > 1e-3 || math.Abs(mg-mb) > 1e-3 {
		t.Errorf("AdobeRGB mid gray channels = %v,%v,%v, want equal within 1e-3", mr, mg, mb)
	}

	gp, err := Parse(iccbuild.Gray(2.2))
	if err != nil {
		t.Fatal(err)
	}
	want, _, _ := gp.SRGB([]float64{0.5})
	if math.Abs(mr-want) > 1e-3 {
		t.Errorf("AdobeRGB mid gray = %v, want within 1e-3 of Gray(2.2).SRGB(0.5) = %v", mr, want)
	}
}

// TestImageMatchesSRGB checks that the 8-bit lookup-table path Image uses agrees with the
// continuous path SRGB uses, for every 8-bit gray level and a 16-step RGB grid, to within the one
// level of rounding both paths are allowed by construction: Image quantizes twice (input sample,
// output encode) where SRGB quantizes never.
func TestImageMatchesSRGB(t *testing.T) {
	gray1 := iccbuild.Gray(1.0)
	gray22 := iccbuild.Gray(2.2)
	rgb18 := rgbProfile(srgbR, srgbG, srgbB, iccbuild.Gamma(1.8))
	adobe22 := rgbProfile(
		[3]float64{0.6097, 0.3111, 0.0195},
		[3]float64{0.2053, 0.6257, 0.0609},
		[3]float64{0.1492, 0.0632, 0.7446},
		iccbuild.Gamma(2.2))

	check := func(name string, raw []byte, gray bool) {
		p, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
		type sample struct{ x, y, r, g, b, a int }
		var samples []sample
		n := 0
		for v := 0; v < 256; v += 17 { // 16 steps plus 255, walking the whole 8-bit gray range.
			for w := 0; w < 256; w += 85 { // 3 steps, giving a small but real RGB grid.
				x, y := n%16, n/16
				n++
				var r, g, b int
				if gray {
					r, g, b = v, v, v
				} else {
					r, g, b = v, w, 255-w
				}
				a := 200 + n
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)})
				samples = append(samples, sample{x, y, r, g, b, a})
			}
		}
		p.Image(img)
		for _, s := range samples {
			off := img.PixOffset(s.x, s.y)
			var in []float64
			if gray {
				in = []float64{float64(s.r) / 255}
			} else {
				in = []float64{float64(s.r) / 255, float64(s.g) / 255, float64(s.b) / 255}
			}
			wr, wg, wb := p.SRGB(in)
			want := [3]uint8{
				uint8(math.Round(255 * wr)), uint8(math.Round(255 * wg)), uint8(math.Round(255 * wb)),
			}
			got := [3]uint8{img.Pix[off], img.Pix[off+1], img.Pix[off+2]}
			for c := 0; c < 3; c++ {
				d := int(got[c]) - int(want[c])
				if d < -1 || d > 1 {
					t.Errorf("%s: pixel (%d,%d,%d) channel %d = %d, SRGB gives %d, off by more than 1",
						name, s.r, s.g, s.b, c, got[c], want[c])
				}
			}
			if got := img.Pix[off+3]; got != uint8(s.a) {
				t.Errorf("%s: pixel (%d,%d,%d) alpha changed from %d to %d", name, s.r, s.g, s.b, s.a, got)
			}
		}
	}
	check("Gray(1.0)", gray1, true)
	check("Gray(2.2)", gray22, true)
	check("sRGB colorants, gamma 1.8", rgb18, false)
	check("AdobeRGB, gamma 2.2", adobe22, false)
}

// TestImageAlphaAndSubImage checks the two properties Image's contract states beyond pixel colour:
// alpha passes through untouched, and a SubImage converts only the rectangle it addresses, leaving
// the pixels outside it exactly as Image found them.
func TestImageAlphaAndSubImage(t *testing.T) {
	p, err := Parse(iccbuild.Gray(2.2))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := uint8((x + y*4) * 16)
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: uint8(100 + x + y)})
		}
	}
	before := make([]uint8, len(img.Pix))
	copy(before, img.Pix)

	sub := img.SubImage(image.Rect(1, 1, 3, 3)).(*image.NRGBA)
	p.Image(sub)

	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			off := img.PixOffset(x, y)
			inRect := x >= 1 && x < 3 && y >= 1 && y < 3
			if before[off+3] != img.Pix[off+3] {
				t.Errorf("(%d,%d): alpha changed from %d to %d", x, y, before[off+3], img.Pix[off+3])
			}
			if !inRect {
				if img.Pix[off] != before[off] || img.Pix[off+1] != before[off+1] || img.Pix[off+2] != before[off+2] {
					t.Errorf("(%d,%d): outside the sub-image's rect but converted", x, y)
				}
				continue
			}
			// Inside the rect, the pixel must match what Image gives any pixel of this value, not
			// merely differ from before: a gamma curve can (and here, at one input, does) have a
			// fixed point where the converted level equals the original, so "changed" is not the
			// right test for "converted".
			want, _, _ := p.SRGB([]float64{float64(before[off]) / 255})
			gotLevel := int(img.Pix[off])
			wantLevel := int(math.Round(255 * want))
			if d := gotLevel - wantLevel; d < -1 || d > 1 {
				t.Errorf("(%d,%d): converted to %d, want within 1 of %d", x, y, gotLevel, wantLevel)
			}
		}
	}
}

// TestComponentsAndClamping covers Components' two values and the clamp SRGB applies to an input
// outside the profile's own valid range: NaN, a negative value and a value above 1 must not reach a
// curve unclamped.
func TestComponentsAndClamping(t *testing.T) {
	g, err := Parse(iccbuild.Gray(2.2))
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Components(); got != 1 {
		t.Errorf("Gray Components() = %d, want 1", got)
	}
	r, err := Parse(rgbProfile(srgbR, srgbG, srgbB, iccbuild.Gamma(2.2)))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Components(); got != 3 {
		t.Errorf("RGB Components() = %d, want 3", got)
	}

	for _, v := range []float64{math.NaN(), -1, 2} {
		rr, gg, bb := g.SRGB([]float64{v})
		for _, got := range []float64{rr, gg, bb} {
			if math.IsNaN(got) || got < 0 || got > 1 {
				t.Errorf("SRGB(%v) = %v, want a finite value in [0,1]", v, got)
			}
		}
	}

	// Missing components read as 0, rather than panicking: Components() is 3 but only one value
	// is given.
	rr, gg, bb := r.SRGB([]float64{0.5})
	wr, wg, wb := r.SRGB([]float64{0.5, 0, 0})
	if rr != wr || gg != wg || bb != wb {
		t.Errorf("SRGB with 1 of 3 components = %v,%v,%v, want same as explicit zeros %v,%v,%v", rr, gg, bb, wr, wg, wb)
	}
}

// TestParametricCurvesFollowLcms checks each case the specification leaves undefined in a
// parametric curve — a slope of 0, where the break -b/a divides by it, and a base aX+b at or below 0,
// whose power is complex, 0^0 or infinite — against what lcms's DefaultEvalParametricFn returns,
// which is what pdfium draws. Each want is lcms's; without the guard, the curve would give the value
// in the comment.
func TestParametricCurvesFollowLcms(t *testing.T) {
	// breakA, breakB are s15Fixed16-exact (A/65536, B/65536 for integers A = -66831, B = 33675), so
	// breakX, computed the same way parsePara's closure computes it, is the float64 x = -b/a a hostile
	// profile's own reader would divide to find. Algebraically a*x+b is 0 there, but breakA*breakX
	// rounds to 1.1102230246251565e-16 above 0 (measured), so parsePara's guard for a < 0 at the break
	// (x < -b/a, false; a*x+b <= 0, false since it is a hair over 0) falls through to the power branch:
	// (a*x+b)^g = 1.11e-16^0.01 = 0.6926945065848448 (measured), far from 0 because g is small. lcms
	// (cmsgamma.c line 384, case 2) runs the identical arithmetic and gives the same value.
	breakA, breakB := -66831.0/65536, 33675.0/65536
	breakX := -breakB / breakA
	for _, c := range []struct {
		name    string
		curve   []byte
		x, want float64
	}{
		// type 1: (aX+b)^g at or above -b/a, else 0.
		{"type 1, a = 0", iccbuild.Para(1, 1, 0, 1), 0.5, 0},                         // 1
		{"type 1, a under lcms's 1e-4", iccbuild.Para(1, 1, 3.0/65536, 1), 0.5, 0},   // 1
		{"type 1, a just over 1e-4", iccbuild.Para(1, 0.5, 7.0/65536, 0), 1, 0.0103}, // not flat: sqrt(7/65536)
		{"type 1, a < 0, below the break", iccbuild.Para(1, 2, -1, 0.5), 0.25, 0},    // 0.0625
		{"type 1, a < 0, above the break", iccbuild.Para(1, 2, -1, 0.5), 1, 0},       // (-0.5)^2 = 0.25
		{"type 1, at the break, g = 0", iccbuild.Para(1, 0, 2, -1), 0.5, 0},          // 0^0 = 1
		// type 1, a < 0, at a break that rounds below -b/a (see breakA/breakB/breakX above): a
		// signed check of a (a < flat) would read breakA as flat and return 0, unlike the correct
		// |a| < flat, which is false here (|breakA| ~= 1.02) and falls through to the power branch.
		{"type 1, a < 0, at a break that rounds below -b/a", iccbuild.Para(1, 0.01, breakA, breakB), breakX, 0.6927},
		// type 2: (aX+b)^g + c at or above -b/a, else c.
		{"type 2, a = 0", iccbuild.Para(2, 1, 0, 1, 0.3), 0.5, 0},                        // 1.3, clipped to 1
		{"type 2, below the break", iccbuild.Para(2, 2.2, 1, -0.5, 0.1), 0.25, 0.1},      // 0
		{"type 2, at the break", iccbuild.Para(2, 2.2, 1, -0.5, 0.1), 0.5, 0},            // 0^2.2 + c = 0.1
		{"type 2, above the break", iccbuild.Para(2, 2.2, 1, -0.5, 0.1), 1, 0.3176},      // the same
		{"type 2, a < 0, below the break", iccbuild.Para(2, 2, -1, 0.5, 0.3), 0.25, 0.3}, // 0 under a check of a, not |a|
		{"type 2, a < 0, above the break", iccbuild.Para(2, 2, -1, 0.5, 0.3), 1, 0},      // (-0.5)^2 + c = 0.55
		// type 3: (aX+b)^g at or above d, else cX.
		{"type 3, negative base", iccbuild.Para(3, 2, 1, -0.8, 1, 0.3), 0.5, 0},               // (-0.3)^2 = 0.09
		{"type 3, below d", iccbuild.Para(3, 2, 1, -0.8, 1, 0.3), 0.1, 0.1},                   // the same
		{"type 3, negative base, fractional g", iccbuild.Para(3, 2.4, -1, 0, 1, 0.5), 0.7, 0}, // NaN, clipped to 0
		{"type 3, base 0, g = 0", iccbuild.Para(3, 0, 1, -0.5, 0, 0), 0.5, 0},                 // 0^0 = 1
		{"type 3, base over 0, g = 0", iccbuild.Para(3, 0, 1, -0.5, 0, 0), 1, 1},              // the same
		// type 3, x == d exactly: lcms (cmsgamma.c, its type 4) takes the power segment when
		// R >= d, not the linear one, so g=1,a=1,b=0,c=0,d=0.5 must give (aX+b)^g = 0.5 at x=0.5,
		// not cX = 0. d = 0.5 is exact in s15Fixed16 (32768/65536), so x == d bit-exact.
		{"type 3, at d exactly, takes the power segment", iccbuild.Para(3, 1, 1, 0, 0, 0.5), 0.5, 0.5}, // cX = 0
		// type 4: (aX+b)^g + e at or above d, else cX + f.
		{"type 4, negative base", iccbuild.Para(4, 2, 1, -0.8, 1, 0.3, 0.2, 0), 0.5, 0.2}, // 0.09 + e = 0.29
		{"type 4, below d", iccbuild.Para(4, 2, 1, -0.8, 1, 0.3, 0.2, 0.05), 0.1, 0.15},   // the same
		{"type 4, above d", iccbuild.Para(4, 2, 1, -0.8, 1, 0.3, 0.2, 0), 1, 0.24},        // the same
		{"type 4, base 0, g = 0", iccbuild.Para(4, 0, 1, -0.5, 0, 0, 0.2, 0), 0.5, 0.2},   // 0^0 + e, clipped to 1
		// type 4, x == d exactly: lcms (cmsgamma.c, its type 5) likewise takes the power segment
		// when R >= d. g=1,a=1,b=0,c=0,d=0.5,e=0,f=0 gives (aX+b)^g+e = 0.5, not cX+f = 0.
		{"type 4, at d exactly, takes the power segment", iccbuild.Para(4, 1, 1, 0, 0, 0.5, 0, 0), 0.5, 0.5}, // cX+f = 0
	} {
		fn, err := parseCurve(c.curve)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := fn(c.x); math.Abs(got-c.want) > 1e-4 {
			t.Errorf("%s: at %v = %v, want %v", c.name, c.x, got, c.want)
		}
	}
}

// TestBlackPoint checks blackPoint's four readings of a black's lightness against CIELAB computed
// here, independently: kept at or under L* 50, clipped to 50 above it with the black's a* and b*
// kept, and set to 0 — black itself — above 95 or below 0.
func TestBlackPoint(t *testing.T) {
	near := func(name string, got, want vec3, tol float64) {
		t.Helper()
		for i := range got {
			if math.Abs(got[i]-want[i]) > tol {
				t.Errorf("%s: blackPoint = %v, want %v", name, got, want)
				return
			}
		}
	}
	scale := func(v vec3, k float64) vec3 { return vec3{v[0] * k, v[1] * k, v[2] * k} }

	near("black", blackPoint(vec3{}), vec3{}, 1e-12)
	// Y 0.1 is L* 116·0.1^(1/3) - 16 = 37.9, and a chromatic black at L* 38 is kept whole.
	near("L* 38, gray", blackPoint(scale(d50, 0.1)), scale(d50, 0.1), 1e-12)
	near("L* 38, chromatic", blackPoint(vec3{0.12, 0.1, 0.05}), vec3{0.12, 0.1, 0.05}, 1e-12)
	// Y 0.3 is L* 61.9: clipped to 50, which is Y ((50+16)/116)^3.
	y50 := math.Pow(66.0/116, 3)
	near("L* 62, gray", blackPoint(scale(d50, 0.3)), scale(d50, y50), 1e-12)
	// The same clip keeps a* = 500(fx - fy) and b* = 200(fy - fz), so X and Z move with L*.
	fx, fy, fz := math.Cbrt(0.35/d50[0]), math.Cbrt(0.3), math.Cbrt(0.2/d50[2])
	a, b := 500*(fx-fy), 200*(fy-fz)
	near("L* 62, chromatic", blackPoint(vec3{0.35, 0.3, 0.2}),
		vec3{math.Pow(66.0/116+a/500, 3) * d50[0], y50, math.Pow(66.0/116-b/200, 3) * d50[2]}, 1e-12)
	// Y 0.9 is L* 96, which lcms reads as a negative profile's black, and takes as black.
	near("L* 96", blackPoint(scale(d50, 0.9)), vec3{}, 1e-12)

	// The four rows below each isolate one boundary of the switch: a chromatic black (a* = 6,
	// b* = 4, kept through any clip) placed exactly at L* -5, 45, 55 and 93. inv is blackPoint's own
	// piecewise inverse of f, used here in reverse to build an xyz whose f(x/d50), f(y/d50), f(z/d50)
	// land at the chosen L*, a*, b* exactly, and again to predict blackPoint's clipped output.
	inv := func(t float64) float64 {
		if t <= 24.0/116 {
			return 108.0 / 841 * (t - 16.0/116)
		}
		return t * t * t
	}
	fromLab := func(l, ca, cb float64) vec3 {
		fy := (l + 16) / 116
		return vec3{inv(fy+ca/500) * d50[0], inv(fy) * d50[1], inv(fy-cb/200) * d50[2]}
	}

	// L* -5 (fy = 11/116, below the linear threshold 24/116, so X, Y and Z all take blackPoint's
	// linear inverse, not cbrt): the same case clips this to L* 0 as clips L* > 95. Dropping "l < 0"
	// from that case, as one mutant does, leaves l at -5 instead, changing every one of X, Y, Z.
	near("L* -5, chromatic, clipped to L* 0", blackPoint(fromLab(-5, 6, 4)), fromLab(0, 6, 4), 1e-12)
	// L* 45 is under the L* > 50 clip, so it is kept whole. A mutant lowering that clip's threshold
	// to L* > 40 would clip it to 50 instead.
	near("L* 45, chromatic, kept whole", blackPoint(fromLab(45, 6, 4)), fromLab(45, 6, 4), 1e-12)
	// L* 55 is just over the L* > 50 clip, so it is clipped to 50. A mutant raising that clip's
	// threshold to L* > 60 would leave it at 55 instead.
	near("L* 55, chromatic, clipped to L* 50", blackPoint(fromLab(55, 6, 4)), fromLab(50, 6, 4), 1e-12)
	// L* 93 is under the L* > 95 clip and over the L* > 50 one, so it is clipped to 50, same as L*
	// 55 above. A mutant lowering the L* > 95 clip's threshold to L* > 90 would clip it to 0 instead.
	near("L* 93, chromatic, clipped to L* 50", blackPoint(fromLab(93, 6, 4)), fromLab(50, 6, 4), 1e-12)
}

// TestBlackPointCompensation checks that a profile whose black is lighter than black draws its
// black as black and its white as white, both through SRGB and through Image, gray and RGB — the
// one-axis y' = (y - t)/(1 - t) and the per-axis matrix and offset. Uncompensated, the black is
// enc(0.05) = 0.25, 63 levels high. The chromatic row lifts red's curve alone, so its black is
// red's colorant, and only compensating each axis by its own black lands it on black.
func TestBlackPointCompensation(t *testing.T) {
	lifted := iccbuild.Para(2, 2.2, math.Pow(0.95, 1/2.2)+0.1, -0.1, 0.05)
	for _, c := range []struct {
		name string
		raw  []byte
	}{
		{"gray", iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: lifted})},
		{"RGB", rgbProfile(srgbR, srgbG, srgbB, lifted)},
		{"RGB, red's black lifted", iccbuild.Profile("RGB ",
			iccbuild.Tag{Sig: "rXYZ", Data: iccbuild.XYZ(srgbR[0], srgbR[1], srgbR[2])},
			iccbuild.Tag{Sig: "gXYZ", Data: iccbuild.XYZ(srgbG[0], srgbG[1], srgbG[2])},
			iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(srgbB[0], srgbB[1], srgbB[2])},
			iccbuild.Tag{Sig: "rTRC", Data: lifted},
			iccbuild.Tag{Sig: "gTRC", Data: srgbTRC()},
			iccbuild.Tag{Sig: "bTRC", Data: srgbTRC()})},
	} {
		p, err := Parse(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.srgb {
			t.Fatalf("%s: isSRGB, which would skip the conversion under test", c.name)
		}
		n := 3
		if p.gray {
			n = 1
		}
		for _, v := range []float64{0, 1} {
			in := []float64{v, v, v}[:n]
			r, g, b := p.SRGB(in)
			for ch, got := range []float64{r, g, b} {
				if math.Abs(got-v) > 1e-3 {
					t.Errorf("%s: SRGB(%v) channel %d = %v, want %v", c.name, in, ch, got, v)
				}
			}
			img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			level := uint8(255 * v)
			copy(img.Pix, []uint8{level, level, level, 255})
			p.Image(img)
			for ch, got := range img.Pix[:3] {
				if got != level {
					t.Errorf("%s: Image of %v channel %d = %d, want %d", c.name, in, ch, got, level)
				}
			}
		}
	}
}

// findTagEntry returns the offset of the 12-byte tag table entry for sig, or -1, by reading the
// table the way Parse itself does. The rejection tests below use it to corrupt one entry's offset
// or size in a profile that iccbuild otherwise assembled correctly, which is the only way to reach
// the boundary checks Parse applies to the table rather than to a tag's own contents.
func findTagEntry(b []byte, sig string) int {
	n := int(binary.BigEndian.Uint32(b[128:132]))
	for i := 0; i < n; i++ {
		e := 132 + i*12
		if string(b[e:e+4]) == sig {
			return e
		}
	}
	return -1
}

func TestRejections(t *testing.T) {
	valid := func() []byte { return rgbProfile(srgbR, srgbG, srgbB, iccbuild.Gamma(2.2)) }

	cases := []struct {
		name string
		b    []byte
	}{
		{"short", make([]byte, 131)},
		{"bad magic", func() []byte {
			b := iccbuild.Gray(2.2)
			copy(b[36:40], "xxxx")
			return b
		}()},
		{"class link", func() []byte {
			b := iccbuild.Gray(2.2)
			copy(b[12:16], "link")
			return b
		}()},
		{"data space CMYK", func() []byte {
			b := valid()
			copy(b[16:20], "CMYK")
			return b
		}()},
		{"PCS Lab", func() []byte {
			b := valid()
			copy(b[20:24], "Lab ")
			return b
		}()},
		{"tag count past end", func() []byte {
			b := valid()
			binary.BigEndian.PutUint32(b[128:132], 1_000_000)
			return b
		}()},
		{"tag offset+size past end", func() []byte {
			b := valid()
			e := findTagEntry(b, "rXYZ")
			binary.BigEndian.PutUint32(b[e+8:e+12], uint32(len(b)))
			return b
		}()},
		{"tag offset+size overflows uint32", func() []byte {
			b := valid()
			e := findTagEntry(b, "rXYZ")
			binary.BigEndian.PutUint32(b[e+4:e+8], 0xFFFFFFF0)
			binary.BigEndian.PutUint32(b[e+8:e+12], 0x20)
			return b
		}()},
		{"A2B0 present", iccbuild.Profile("RGB ",
			iccbuild.Tag{Sig: "rXYZ", Data: iccbuild.XYZ(srgbR[0], srgbR[1], srgbR[2])},
			iccbuild.Tag{Sig: "gXYZ", Data: iccbuild.XYZ(srgbG[0], srgbG[1], srgbG[2])},
			iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(srgbB[0], srgbB[1], srgbB[2])},
			iccbuild.Tag{Sig: "rTRC", Data: iccbuild.Gamma(2.2)},
			iccbuild.Tag{Sig: "gTRC", Data: iccbuild.Gamma(2.2)},
			iccbuild.Tag{Sig: "bTRC", Data: iccbuild.Gamma(2.2)},
			iccbuild.Tag{Sig: "A2B0", Data: make([]byte, 12)},
		)},
		{"missing kTRC", iccbuild.Profile("GRAY")},
		{"missing gXYZ", iccbuild.Profile("RGB ",
			iccbuild.Tag{Sig: "rXYZ", Data: iccbuild.XYZ(srgbR[0], srgbR[1], srgbR[2])},
			iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(srgbB[0], srgbB[1], srgbB[2])},
			iccbuild.Tag{Sig: "rTRC", Data: iccbuild.Gamma(2.2)},
			iccbuild.Tag{Sig: "gTRC", Data: iccbuild.Gamma(2.2)},
			iccbuild.Tag{Sig: "bTRC", Data: iccbuild.Gamma(2.2)},
		)},
		{"TRC type sf32", iccbuild.Profile("GRAY",
			iccbuild.Tag{Sig: "kTRC", Data: append([]byte("sf32"), make([]byte, 8)...)},
		)},
		{"curv count past its tag", func() []byte {
			data := iccbuild.Curve(0, 1, 2, 3, 4)
			binary.BigEndian.PutUint32(data[8:12], 100)
			return iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: data})
		}()},
		{"para type 5", iccbuild.Profile("GRAY",
			iccbuild.Tag{Sig: "kTRC", Data: iccbuild.Para(5, 1, 2, 3, 4, 5, 6, 7)},
		)},
		{"para parameters past its tag", func() []byte {
			data := iccbuild.Para(4, 1, 2, 3, 4, 5, 6, 7)
			return iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: data[:len(data)-4]})
		}()},
		{"XYZ tag shorter than 20", func() []byte {
			data := iccbuild.XYZ(0.5, 0.5, 0.5)
			return iccbuild.Profile("RGB ",
				iccbuild.Tag{Sig: "rXYZ", Data: data[:16]},
				iccbuild.Tag{Sig: "gXYZ", Data: iccbuild.XYZ(srgbG[0], srgbG[1], srgbG[2])},
				iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(srgbB[0], srgbB[1], srgbB[2])},
				iccbuild.Tag{Sig: "rTRC", Data: iccbuild.Gamma(2.2)},
				iccbuild.Tag{Sig: "gTRC", Data: iccbuild.Gamma(2.2)},
				iccbuild.Tag{Sig: "bTRC", Data: iccbuild.Gamma(2.2)},
			)
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.b); err == nil {
				t.Fatalf("Parse succeeded, want an error")
			} else if len(err.Error()) < 5 || err.Error()[:5] != "icc: " {
				t.Errorf("error %q does not start with %q", err.Error(), "icc: ")
			}
		})
	}
}

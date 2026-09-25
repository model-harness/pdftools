package native

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/icc"
	"github.com/model-harness/pdftools/internal/iccbuild"
	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// errRefStore wraps a real Store and fails to resolve one chosen reference, so a colour space
// behind it hits colourSpaceAt's own resolve error — which objects/pdfcpu's Resolve never
// produces on its own, since a missing object resolves to Null and not an error.
type errRefStore struct {
	objects.Store
	bad objects.Ref
}

func (e errRefStore) Resolve(o objects.Object) (objects.Object, error) {
	if ref, ok := o.(objects.Ref); ok && ref == e.bad {
		return nil, errors.New("boom")
	}
	return e.Store.Resolve(o)
}

// Fixtures shared by every test in this file: two gray and two RGB matrix/TRC profiles, distinguished
// by gamma and colorants so a test that reads the wrong one shows up as a wrong pixel rather than an
// accidental match.
var (
	gray22  = iccbuild.Gray(2.2)
	gray10  = iccbuild.Gray(1.0)
	srgb18  = iccbuild.RGB(1.8, [3]float64{0.4361, 0.2225, 0.0139}, [3]float64{0.3851, 0.7169, 0.0971}, [3]float64{0.1431, 0.0606, 0.7141})
	adobe22 = iccbuild.RGB(2.2, [3]float64{0.6097, 0.3111, 0.0195}, [3]float64{0.2053, 0.6257, 0.0609}, [3]float64{0.1492, 0.0632, 0.7446})
	// grayLifted and adobeLifted start their curves at 0.05, not 0: a black lighter than black,
	// which black point compensation carries back to it.
	grayLifted  = iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: liftedPara2(2.2, 0.05)})
	adobeLifted = iccbuild.Profile("RGB ",
		iccbuild.Tag{Sig: "rXYZ", Data: iccbuild.XYZ(0.6097, 0.3111, 0.0195)},
		iccbuild.Tag{Sig: "gXYZ", Data: iccbuild.XYZ(0.2053, 0.6257, 0.0609)},
		iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(0.1492, 0.0632, 0.7446)},
		iccbuild.Tag{Sig: "rTRC", Data: liftedPara2(2.2, 0.05)},
		iccbuild.Tag{Sig: "gTRC", Data: liftedPara2(2.2, 0.05)},
		iccbuild.Tag{Sig: "bTRC", Data: liftedPara2(2.2, 0.05)})
)

// iccStream is an ICCBased profile stream object with /N given explicitly rather than read from the
// profile, because several tests below deliberately mismatch the two.
func iccStream(profile []byte, n int) string {
	return fmt.Sprintf("<</N %d/Length %d>>\nstream\n%s\nendstream", n, len(profile), profile)
}

// iccStreamAlt is iccStream with an /Alternate colour space, for §8.6.5.5's fallback.
func iccStreamAlt(profile []byte, n int, alt string) string {
	return fmt.Sprintf("<</N %d/Alternate%s/Length %d>>\nstream\n%s\nendstream", n, alt, len(profile), profile)
}

// colourPDF writes a page whose /Resources/ColorSpace is the dictionary given, with extra objects
// numbered from 5 — the same shape as xoPDF and gsPDF, and its own function because what is under
// test here is which colour space produces which pixel, not an XObject or an ExtGState.
func colourPDF(t *testing.T, w, h int, colorSpaces, stream string, extra ...string) string {
	t.Helper()
	page := fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 %d %d]"+
		"/Resources<</ColorSpace<<%s>>>>/Contents 4 0 R>>", w, h, colorSpaces)
	return buildPDF(t, append(pageObjs(page, stream+"\n"), extra...), "colour.pdf")
}

// patchStream lays out one 10×10 patch per colour tuple in values, side by side, each set with
// cs/scn and filled. width is what the page needs to hold all of them.
func patchStream(values [][]float64) (stream string, width int) {
	var s strings.Builder
	for i, v := range values {
		s.WriteString("/CS0 cs")
		for _, c := range v {
			fmt.Fprintf(&s, " %.6f", c)
		}
		fmt.Fprintf(&s, " scn %d 0 10 10 re f\n", i*10)
	}
	return s.String(), len(values) * 10
}

// grayValues is every gray level i/64, i in 0..64: 65 patches.
func grayValues() [][]float64 {
	out := make([][]float64, 65)
	for i := range out {
		out[i] = []float64{float64(i) / 64}
	}
	return out
}

// rgbValues is the 6×6×6 grid of j/5 per channel: 216 patches.
func rgbValues() [][]float64 {
	var out [][]float64
	for ri := 0; ri < 6; ri++ {
		for gi := 0; gi < 6; gi++ {
			for bi := 0; bi < 6; bi++ {
				out = append(out, []float64{float64(ri) / 5, float64(gi) / 5, float64(bi) / 5})
			}
		}
	}
	return out
}

// TestICCColourAgreesWithPdfium is the ICC acceptance test: this package's colour(), through each
// fixture's profile, against pdfium's own CMM, over every gray level and the 6×6×6 RGB grid.
//
// # Why the quantization check comes first
//
// pdfium truncates every scn/SCN component to a byte before its CMM ever sees it, and icc.SRGB does
// not. So the two are not directly comparable at all — they are comparable through the *same*
// quantization, and asserting that pdfium's pixel equals this package's profile fed
// floor(255·v)/255, exactly, is the proof that quantization is the only thing separating them. A
// spike over all four fixtures and all 65+65+216+216 patches found exactly that, with one
// exception: one patch of adobe22 (device RGB 1, 0.6, 0.2) disagreed by 1 level in the blue channel,
// which is a rounding-tie between this package's round-half-away-from-zero and whatever pdfium's CMM
// does at the same tie, not a quantization difference — so that fixture's tolerance is 1 and the
// others are 0. grayLifted and adobeLifted, whose black point lcms compensates, are 0 as well.
//
// # The native-vs-pdfium bounds
//
// Measured directly, not derived from the quantization check: gray22 differs from pdfium by at most
// 1 level (its own rounding, once through a different code path than the quantization proof above),
// gray10 by up to 6 near black (a linear TRC's curve is steepest where 8-bit quantization costs the
// most), srgb18 by 0, adobe22 by 1 (the same boundary patch), grayLifted by 1 and adobeLifted by 0.
func TestICCColourAgreesWithPdfium(t *testing.T) {
	for _, c := range []struct {
		name      string
		profile   []byte
		n         int
		values    [][]float64
		quantTol  int
		nativeTol int
	}{
		{"gray22", gray22, 1, grayValues(), 0, 1},
		{"gray10", gray10, 1, grayValues(), 0, 6},
		{"srgb18", srgb18, 3, rgbValues(), 0, 0},
		{"adobe22", adobe22, 3, rgbValues(), 1, 1},
		{"grayLifted", grayLifted, 1, grayValues(), 0, 1},
		{"adobeLifted", adobeLifted, 3, rgbValues(), 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			stream, width := patchStream(c.values)
			path := colourPDF(t, width, 10, "/CS0[/ICCBased 5 0 R]", stream, iccStream(c.profile, c.n))

			ref, err := pdfium.Open(path)
			if err != nil {
				t.Fatalf("pdfium: %v", err)
			}
			defer func() { _ = ref.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			want, err := ref.Page(1, o)
			if err != nil {
				t.Fatalf("pdfium Page: %v", err)
			}
			got := renderNative(t, path, 72)

			p, err := icc.Parse(c.profile)
			if err != nil {
				t.Fatalf("icc.Parse: %v", err)
			}

			for i, v := range c.values {
				x := i*10 + 5
				pr, pg, pb := rgbAt(want.Image, x, 5)
				nr, ng, nb := rgbAt(got.Image, x, 5)

				q := make([]float64, len(v))
				for j, comp := range v {
					q[j] = math.Floor(255*comp) / 255
				}
				sr, sg, sb := p.SRGB(q)
				wr, wg, wb := int(math.Round(sr*255)), int(math.Round(sg*255)), int(math.Round(sb*255))
				if len(v) == 1 {
					wg, wb = wr, wr
				}
				pv, wv := [3]int{pr, pg, pb}, [3]int{wr, wg, wb}
				for ch := 0; ch < 3; ch++ {
					if d := absInt(pv[ch] - wv[ch]); d > c.quantTol {
						t.Errorf("patch %v channel %d: pdfium %d, quantized profile %d, want within %d",
							v, ch, pv[ch], wv[ch], c.quantTol)
					}
				}
				nv := [3]int{nr, ng, nb}
				for ch := 0; ch < 3; ch++ {
					if d := absInt(pv[ch] - nv[ch]); d > c.nativeTol {
						t.Errorf("patch %v channel %d: native %d, pdfium %d, want within %d",
							v, ch, nv[ch], pv[ch], c.nativeTol)
					}
				}
			}

			// A stroke of one value reaches the same paint as a fill of the same value, since both
			// go through the identical space.colour — checked exactly, with no pdfium involved.
			v := c.values[len(c.values)/2]
			var comps strings.Builder
			for _, x := range v {
				fmt.Fprintf(&comps, "%.6f ", x)
			}
			fillPath := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]",
				fmt.Sprintf("/CS0 cs %sscn 0 0 10 10 re f", comps.String()), iccStream(c.profile, c.n))
			strokePath := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]",
				fmt.Sprintf("/CS0 CS 10 w %sSCN 0 5 m 10 5 l S", comps.String()), iccStream(c.profile, c.n))
			fr, fg, fb := rgbAt(renderNative(t, fillPath, 72).Image, 5, 5)
			sr2, sg2, sb2 := rgbAt(renderNative(t, strokePath, 72).Image, 5, 5)
			if fr != sr2 || fg != sg2 || fb != sb2 {
				t.Errorf("fill of %v = (%d,%d,%d), stroke of the same value = (%d,%d,%d), want equal",
					v, fr, fg, fb, sr2, sg2, sb2)
			}
		})
	}
}

// TestICCImagesAgreeWithPdfium is the image half of the ICC acceptance test: a 256×1 image whose
// samples are 0..255, every component equal, drawn one device pixel per sample so each pixel
// samples exactly one image sample.
//
// nativeTol is measured at 0 for both gray fixtures: bilinear sampling at an exact 1:1 scale lands
// squarely on each sample centre, so there is no resampling error to add to the profile's own.
// adobe22, whose samples are equal in all three components, differs by 1 level on 16 of its 768
// channel samples, the same 1 its fills show in TestICCColourAgreesWithPdfium. The second bound
// guards against a regression that skips the profile entirely — gray10's linear TRC through sRGB's
// encoding moves every sample by a lot, worst measured at 73 of 255, and gray22's gentler curve and
// adobe22's, the same gamma, still move samples by 9.
func TestICCImagesAgreeWithPdfium(t *testing.T) {
	for _, c := range []struct {
		name       string
		profile    []byte
		n          int
		nativeTol  int
		rawDiffMin int
	}{
		{"gray22", gray22, 1, 0, 5},
		{"gray10", gray10, 1, 0, 30},
		{"adobe22", adobe22, 3, 1, 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			var samples strings.Builder
			for i := 0; i < 256; i++ {
				fmt.Fprint(&samples, strings.Repeat(fmt.Sprintf("%02X", i), c.n))
			}
			im := fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 256/Height 1/ColorSpace[/ICCBased 6 0 R]"+
				"/BitsPerComponent 8/Filter/ASCIIHexDecode/Length %d>>\nstream\n%s>\nendstream",
				samples.Len()+1, samples.String())
			page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 256 10]" +
				"/Resources<</XObject<</Im0 5 0 R>>>>/Contents 4 0 R>>"
			path := buildPDF(t, append(pageObjs(page, "q 256 0 0 10 0 0 cm /Im0 Do Q\n"),
				im, iccStream(c.profile, c.n)), "iccimg.pdf")

			ref, err := pdfium.Open(path)
			if err != nil {
				t.Fatalf("pdfium: %v", err)
			}
			defer func() { _ = ref.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			want, err := ref.Page(1, o)
			if err != nil {
				t.Fatalf("pdfium Page: %v", err)
			}
			got := renderNative(t, path, 72)

			worstRawDiff := 0
			for i := 0; i < 256; i++ {
				pr, pg, pb := rgbAt(want.Image, i, 5)
				nr, ng, nb := rgbAt(got.Image, i, 5)
				pv, nv := [3]int{pr, pg, pb}, [3]int{nr, ng, nb}
				for ch := range pv {
					if d := absInt(pv[ch] - nv[ch]); d > c.nativeTol {
						t.Errorf("sample %d channel %d: native %d, pdfium %d, want within %d", i, ch, nv[ch], pv[ch], c.nativeTol)
					}
					if d := absInt(nv[ch] - i); d > worstRawDiff {
						worstRawDiff = d
					}
				}
			}
			if worstRawDiff < c.rawDiffMin {
				t.Errorf("worst |native - raw sample| = %d, want at least %d — a regression that"+
					" skips the profile would leave this near 0", worstRawDiff, c.rawDiffMin)
			}
		})
	}
}

// sampledCurve is a curv table of n entries sampling x^g.
func sampledCurve(n int, g float64) []byte {
	table := make([]uint16, n)
	for i := range table {
		table[i] = uint16(math.Round(65535 * math.Pow(float64(i)/float64(n-1), g)))
	}
	return iccbuild.Curve(table...)
}

// liftedPara2 is para type 2 whose black is c and whose white is exactly 1: (aX+b)^g + c with
// a+b = (1-c)^(1/g).
func liftedPara2(g, c float64) []byte {
	top := math.Pow(1-c, 1/g)
	return iccbuild.Para(2, g, top+0.1, -0.1, c)
}

// TestICCCurvesAgreeWithPdfium draws every curve form the icc package reads, as a gray profile's one
// curve and as all three of an Adobe RGB profile's, through a 256×1 image against pdfium. Gray's
// samples are 0..255 and RGB's (i, i+85, i+170) mod 256, so the matrix sees colours, not grays.
//
// Every bound is the worst measured. RGB's 1 is lcms's own: pdfium reaches a profile whose black is
// black through lcms's matrix-shaper path, 1.14 fixed point. A curve that lifts black takes lcms's
// black point compensation instead, which icc applies too — without it the three "black lifted"
// rows are 63, 25 and 60 levels off, and without its clip of the black's lightness to L* 50 the
// "past L* 50" row is 105. Past L* 95 lcms takes the black to be 0, a negative profile, which
// without that rule is 3 levels off. The hostile rows are the cases the specification leaves
// undefined — a zero slope, a power of a negative base — where icc follows lcms's evaluator.
//
// "ending at 1.05" and "ending at 1.02" are the one disagreement kept on purpose: ICC.1 §10.18 clips
// a curve's output to [0,1] and icc does; lcms carries 1.05 into the matrix, where it pulls the
// other two channels, so RGB is 8 and 3 levels off. Gray, with no matrix, agrees.
func TestICCCurvesAgreeWithPdfium(t *testing.T) {
	adobe := [3][]byte{iccbuild.XYZ(0.6097, 0.3111, 0.0195), iccbuild.XYZ(0.2053, 0.6257, 0.0609), iccbuild.XYZ(0.1492, 0.0632, 0.7446)}
	lifted4 := iccbuild.Para(4, 2.4, math.Pow(0.98, 1/2.4)/1.055, math.Pow(0.98, 1/2.4)*0.055/1.055, 1/12.92, 0.04045, 0.02, 0.01)
	for _, c := range []struct {
		name            string
		trc             [3][]byte // gray reads trc[0]; RGB reads all three
		grayTol, rgbTol int
	}{
		{"curv, no entries", [3][]byte{iccbuild.Curve()}, 0, 1},
		{"curv, 2 entries", [3][]byte{iccbuild.Curve(0, 65535)}, 0, 1},
		{"curv, 3 entries", [3][]byte{iccbuild.Curve(0, 40000, 65535)}, 0, 1},
		{"curv, 3 entries, not monotonic", [3][]byte{iccbuild.Curve(0, 50000, 30000)}, 1, 1},
		{"curv, 256 entries", [3][]byte{sampledCurve(256, 1.8)}, 0, 1},
		{"curv, 1024 entries", [3][]byte{sampledCurve(1024, 2.2)}, 1, 1},
		{"curv, 4096 entries", [3][]byte{sampledCurve(4096, 2.4)}, 1, 1},
		{"curv, black lifted", [3][]byte{iccbuild.Curve(3000, 20000, 65535)}, 0, 0},
		{"para 0", [3][]byte{iccbuild.Para(0, 2.2)}, 0, 1},
		{"para 1", [3][]byte{iccbuild.Para(1, 2.2, 1.1, -0.1)}, 0, 1},
		{"para 2, black lifted", [3][]byte{liftedPara2(2.2, 0.05)}, 0, 0},
		{"para 2, black past L* 50", [3][]byte{liftedPara2(2.2, 0.30)}, 0, 1},
		{"para 2, black past L* 95", [3][]byte{liftedPara2(2.2, 0.90)}, 0, 1},
		{"para 2, ending at 1.05", [3][]byte{iccbuild.Para(2, 2.2, 1.1, -0.1, 0.05)}, 0, 8},
		{"para 3, sRGB", [3][]byte{iccbuild.Para(3, 2.4, 1/1.055, 0.055/1.055, 1/12.92, 0.04045)}, 0, 1},
		{"para 4, black lifted", [3][]byte{lifted4}, 0, 0},
		{"para 4, ending at 1.02", [3][]byte{iccbuild.Para(4, 2.4, 1/1.055, 0.055/1.055, 1/12.92, 0.04045, 0.02, 0.01)}, 0, 3},
		{"hostile para 0, gamma 0", [3][]byte{iccbuild.Para(0, 0)}, 0, 0},
		{"hostile para 0, gamma 30000", [3][]byte{iccbuild.Para(0, 30000)}, 0, 0},
		{"hostile para 1, a = 0", [3][]byte{iccbuild.Para(1, 1, 0, 1)}, 0, 0},
		{"hostile para 1, negative base", [3][]byte{iccbuild.Para(1, 2, -1, 0.5)}, 0, 0},
		{"hostile para 2, a = 0", [3][]byte{iccbuild.Para(2, 1, 0, 1, 0.3)}, 0, 0},
		{"hostile para 2, negative base", [3][]byte{iccbuild.Para(2, 2, -1, 0.5, 0.3)}, 0, 0},
		{"hostile para 3, negative base", [3][]byte{iccbuild.Para(3, 2, 1, -0.8, 1, 0.3)}, 0, 1},
		{"hostile para 4, negative base", [3][]byte{iccbuild.Para(4, 2, 1, -0.8, 1, 0.3, 0.2, 0)}, 0, 1},
		{"a curve of each form, black lifted in one", [3][]byte{sampledCurve(256, 1.8), lifted4, iccbuild.Gamma(1)}, -1, 1},
	} {
		for _, space := range []string{"GRAY", "RGB "} {
			n, tol, profile := 3, c.rgbTol, []byte(nil)
			if space == "GRAY" {
				if c.grayTol < 0 {
					continue
				}
				n, tol = 1, c.grayTol
				profile = iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: c.trc[0]})
			} else {
				trc := c.trc
				if trc[1] == nil {
					trc[1], trc[2] = trc[0], trc[0]
				}
				profile = iccbuild.Profile("RGB ",
					iccbuild.Tag{Sig: "rXYZ", Data: adobe[0]}, iccbuild.Tag{Sig: "gXYZ", Data: adobe[1]}, iccbuild.Tag{Sig: "bXYZ", Data: adobe[2]},
					iccbuild.Tag{Sig: "rTRC", Data: trc[0]}, iccbuild.Tag{Sig: "gTRC", Data: trc[1]}, iccbuild.Tag{Sig: "bTRC", Data: trc[2]})
			}
			t.Run(c.name+" "+strings.TrimSpace(space), func(t *testing.T) {
				var samples strings.Builder
				for i := 0; i < 256; i++ {
					for ch := 0; ch < n; ch++ {
						fmt.Fprintf(&samples, "%02X", (i+ch*85)%256)
					}
				}
				im := fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 256/Height 1/ColorSpace[/ICCBased 6 0 R]"+
					"/BitsPerComponent 8/Filter/ASCIIHexDecode/Length %d>>\nstream\n%s>\nendstream", samples.Len()+1, samples.String())
				page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 256 10]/Resources<</XObject<</Im0 5 0 R>>>>/Contents 4 0 R>>"
				path := buildPDF(t, append(pageObjs(page, "q 256 0 0 10 0 0 cm /Im0 Do Q\n"), im, iccStream(profile, n)), "icccurve.pdf")

				ref, err := pdfium.Open(path)
				if err != nil {
					t.Fatalf("pdfium: %v", err)
				}
				defer func() { _ = ref.Close() }()
				o := render.DefaultOptions
				o.DPI = 72
				want, err := ref.Page(1, o)
				if err != nil {
					t.Fatalf("pdfium Page: %v", err)
				}
				got := renderNative(t, path, 72)

				worst := 0
				for i := 0; i < 256; i++ {
					pr, pg, pb := rgbAt(want.Image, i, 5)
					nr, ng, nb := rgbAt(got.Image, i, 5)
					pv, nv := [3]int{pr, pg, pb}, [3]int{nr, ng, nb}
					for ch := range pv {
						if d := absInt(pv[ch] - nv[ch]); d > worst {
							worst = d
						}
						if d := absInt(pv[ch] - nv[ch]); d > tol {
							t.Errorf("sample %d channel %d: native %d, pdfium %d, want within %d", i, ch, nv[ch], pv[ch], tol)
						}
					}
				}
				t.Logf("worst %d, bound %d", worst, tol)
			})
		}
	}
}

// TestSRGBProfileDrawsAsDevice checks every consumer of an sRGB profile — a fill, a stroke and an
// image, gray and RGB — against the device space it is equivalent to, byte for byte. Each fill and
// stroke component sits on a half level, where a conversion off by the smallest amount rounds the
// other way, and the image carries every byte value in every channel.
func TestSRGBProfileDrawsAsDevice(t *testing.T) {
	curve := iccbuild.Para(3, 2.4, 1/1.055, 0.055/1.055, 1/12.92, 0.04045)
	rgbProfile := iccbuild.Profile("RGB ",
		iccbuild.Tag{Sig: "rXYZ", Data: iccbuild.XYZ(0.4361, 0.2225, 0.0139)},
		iccbuild.Tag{Sig: "gXYZ", Data: iccbuild.XYZ(0.3851, 0.7169, 0.0971)},
		iccbuild.Tag{Sig: "bXYZ", Data: iccbuild.XYZ(0.1431, 0.0606, 0.7141)},
		iccbuild.Tag{Sig: "rTRC", Data: curve}, iccbuild.Tag{Sig: "gTRC", Data: curve}, iccbuild.Tag{Sig: "bTRC", Data: curve})
	grayProfile := iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: curve})

	for _, c := range []struct {
		name         string
		profile      []byte
		n            int
		device       string
		fill, stroke string
	}{
		{"rgb", rgbProfile, 3, "/DeviceRGB", "rg", "RG"},
		{"gray", grayProfile, 1, "/DeviceGray", "g", "G"},
	} {
		t.Run(c.name, func(t *testing.T) {
			page := func(icc bool) string {
				var s strings.Builder
				for i := 0; i < 32; i++ {
					var comps strings.Builder
					for ch := 0; ch < c.n; ch++ {
						fmt.Fprintf(&comps, "%.6f ", (float64((i*8+ch*85)%255)+0.5)/255)
					}
					if icc {
						fmt.Fprintf(&s, "/CS0 cs %sscn %d 0 8 10 re f\n", comps.String(), i*8)
						fmt.Fprintf(&s, "/CS0 CS 4 w %sSCN %d 15 m %d 15 l S\n", comps.String(), i*8, i*8+8)
					} else {
						fmt.Fprintf(&s, "%s%s %d 0 8 10 re f\n", comps.String(), c.fill, i*8)
						fmt.Fprintf(&s, "4 w %s%s %d 15 m %d 15 l S\n", comps.String(), c.stroke, i*8, i*8+8)
					}
				}
				s.WriteString("q 256 0 0 10 0 20 cm /Im0 Do Q\n")
				var samples strings.Builder
				for i := 0; i < 256; i++ {
					for ch := 0; ch < c.n; ch++ {
						fmt.Fprintf(&samples, "%02X", (i+ch*85)%256)
					}
				}
				space := c.device
				if icc {
					space = "[/ICCBased 6 0 R]"
				}
				im := fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 256/Height 1/ColorSpace%s"+
					"/BitsPerComponent 8/Filter/ASCIIHexDecode/Length %d>>\nstream\n%s>\nendstream",
					space, samples.Len()+1, samples.String())
				pg := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 256 30]" +
					"/Resources<</ColorSpace<</CS0[/ICCBased 6 0 R]>>/XObject<</Im0 5 0 R>>>>/Contents 4 0 R>>"
				return buildPDF(t, append(pageObjs(pg, s.String()), im, iccStream(c.profile, c.n)), "srgb.pdf")
			}
			got, want := renderNative(t, page(true), 72).Image, renderNative(t, page(false), 72).Image
			b := want.Bounds()
			if got.Bounds() != b {
				t.Fatalf("bounds %v, device %v", got.Bounds(), b)
			}
			diff := 0
			for y := 0; y < b.Dy(); y++ {
				for x := 0; x < b.Dx(); x++ {
					gr, gg, gb := rgbAt(got, x, y)
					wr, wg, wb := rgbAt(want, x, y)
					if gr != wr || gg != wg || gb != wb {
						if diff < 5 {
							t.Errorf("(%d,%d): ICCBased (%d,%d,%d), %s (%d,%d,%d)", x, y, gr, gg, gb, c.device, wr, wg, wb)
						}
						diff++
					}
				}
			}
			if diff > 0 {
				t.Errorf("%d pixels differ from %s", diff, c.device)
			}
		})
	}
}

// TestStockSRGBProfileAgreesWithPdfium draws the stock 3144-byte sRGB IEC61966-2.1 profile, which
// pdfium recognises by its bytes and draws as DeviceRGB, against pdfium. It reads the copy Windows
// ships and skips where there is none, because the profile is not this repository's to
// redistribute; isSRGB accepts it on content, so the two agree exactly, fills and image alike.
func TestStockSRGBProfileAgreesWithPdfium(t *testing.T) {
	profile, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "spool", "drivers", "color", "sRGB Color Space Profile.icm"))
	if err != nil || len(profile) != 3144 {
		t.Skip("no stock sRGB IEC61966-2.1 profile on this machine")
	}
	var s strings.Builder
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&s, "/CS0 cs %.6f %.6f %.6f scn %d 0 8 10 re f\n",
			(float64(i*8%255)+0.5)/255, (float64((i*8+85)%255)+0.5)/255, (float64((i*8+170)%255)+0.5)/255, i*8)
	}
	s.WriteString("q 256 0 0 10 0 10 cm /Im0 Do Q\n")
	var samples strings.Builder
	for i := 0; i < 256; i++ {
		fmt.Fprintf(&samples, "%02X%02X%02X", i, (i+85)%256, (i+170)%256)
	}
	im := fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 256/Height 1/ColorSpace[/ICCBased 6 0 R]"+
		"/BitsPerComponent 8/Filter/ASCIIHexDecode/Length %d>>\nstream\n%s>\nendstream", samples.Len()+1, samples.String())
	page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 256 20]" +
		"/Resources<</ColorSpace<</CS0[/ICCBased 6 0 R]>>/XObject<</Im0 5 0 R>>>>/Contents 4 0 R>>"
	path := buildPDF(t, append(pageObjs(page, s.String()), im, iccStream(profile, 3)), "stock-srgb.pdf")

	ref, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium: %v", err)
	}
	defer func() { _ = ref.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	want, err := ref.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}
	got := renderNative(t, path, 72)
	diff := 0
	for y := 0; y < 20; y++ {
		for x := 0; x < 256; x++ {
			gr, gg, gb := rgbAt(got.Image, x, y)
			wr, wg, wb := rgbAt(want.Image, x, y)
			if gr != wr || gg != wg || gb != wb {
				if diff < 5 {
					t.Errorf("(%d,%d): native (%d,%d,%d), pdfium (%d,%d,%d)", x, y, gr, gg, gb, wr, wg, wb)
				}
				diff++
			}
		}
	}
	if diff > 0 {
		t.Errorf("%d pixels differ from pdfium", diff)
	}
}

// TestICCFallsBackAsPdfiumDoes is §8.6.5.5's fallback, checked against pdfium wherever pdfium has
// an opinion to check against and by reason where the profile cannot be used at all.
func TestICCFallsBackAsPdfiumDoes(t *testing.T) {
	garbage := []byte("not a profile at all")

	for _, c := range []struct {
		name    string
		obj5    string
		scn     string
		r, g, b int
	}{
		{"garbage /N 1 falls back to DeviceGray", iccStream(garbage, 1), "0.5 scn", 128, 128, 128},
		{"garbage /N 3 falls back to DeviceRGB", iccStream(garbage, 3), "1 0 0 scn", 255, 0, 0},
		{"a gray profile declared /N 3 falls back to DeviceRGB", iccStream(gray22, 3), "1 0 0 scn", 255, 0, 0},
		{"garbage with /Alternate /DeviceGray", iccStreamAlt(garbage, 1, "/DeviceGray"), "0.5 scn", 128, 128, 128},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]",
				"/CS0 cs "+c.scn+" 0 0 10 10 re f", c.obj5)

			ref, err := pdfium.Open(path)
			if err != nil {
				t.Fatalf("pdfium: %v", err)
			}
			defer func() { _ = ref.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			want, err := ref.Page(1, o)
			if err != nil {
				t.Fatalf("pdfium Page: %v", err)
			}
			pr, pg, pb := rgbAt(want.Image, 5, 5)
			if pr != c.r || pg != c.g || pb != c.b {
				t.Fatalf("pdfium = (%d,%d,%d), want (%d,%d,%d) — this fixture's claim about pdfium's"+
					" own fallback is wrong", pr, pg, pb, c.r, c.g, c.b)
			}

			got := renderNative(t, path, 72)
			nr, ng, nb := rgbAt(got.Image, 5, 5)
			if nr != c.r || ng != c.g || nb != c.b {
				t.Errorf("native = (%d,%d,%d), want (%d,%d,%d)", nr, ng, nb, c.r, c.g, c.b)
			}
		})
	}

	t.Run("garbage with an /Alternate ICCBased space goes through that profile", func(t *testing.T) {
		p, err := icc.Parse(gray10)
		if err != nil {
			t.Fatal(err)
		}
		sr, _, _ := p.SRGB([]float64{0.5})
		want := int(math.Round(sr * 255)) // 188 = round(255·enc(0.5)), enc being sRGB's encoding
		path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]",
			"/CS0 cs 0.5 scn 0 0 10 10 re f",
			fmt.Sprintf("<</N 1/Alternate[/ICCBased 6 0 R]/Length %d>>\nstream\n%s\nendstream", len(garbage), garbage),
			iccStream(gray10, 1))
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != want || g != want || b != want {
			t.Errorf("got (%d,%d,%d), want (%d,%d,%d)", r, g, b, want, want, want)
		}
	})

	sepAlt := "[/Separation/Spot/DeviceGray 6 0 R]"
	fn := "<</FunctionType 2/Domain[0 1]/C0[0]/C1[1]/N 1>>"
	for _, c := range []struct {
		name       string
		obj5, obj6 string
		want       string
	}{
		{"an /N outside 1, 3 and 4", iccStream(garbage, 2), "", "an ICCBased space with /N 2"},
		{"a garbage profile whose /Alternate is a Separation", iccStreamAlt(garbage, 1, sepAlt), fn,
			"an ICC profile this backend does not read, and its /Alternate: the colour space is /Separation"},
		{"garbage /N 3 with a 1-component /Alternate", iccStreamAlt(garbage, 3, "/DeviceGray"), "",
			"an ICCBased /Alternate of 1 components for /N 3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			extra := []string{c.obj5}
			if c.obj6 != "" {
				extra = append(extra, c.obj6)
			}
			path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]", "/CS0 cs 0.5 scn 0 0 10 10 re f", extra...)
			got := refusal(t, path)
			if !strings.Contains(got, c.want) {
				t.Errorf("refusal = %q, want it to contain %q", got, c.want)
			}
		})
	}

	t.Run("a garbage profile's /Alternate is a resource name, not looked up", func(t *testing.T) {
		// §8.6.3 makes a colour space an array or a family name, and /CS1 is neither: it names a
		// resource, which only cs and CS look up. So this refuses rather than chasing /CS1 to the
		// resources' /DeviceRGB, as for a resource entry that names another resource (see "a
		// resource entry that names another resource is not looked up again" below).
		//
		// Measured: pdfium draws (255,0,0) here, resolving the /Alternate name as a resource. This
		// backend refuses instead; the divergence is documented in ADR 0021, not copied.
		path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]/CS1/DeviceRGB",
			"/CS0 cs 1 0 0 scn 0 0 10 10 re f", iccStreamAlt(garbage, 3, "/CS1"))
		want := "an ICC profile this backend does not read, and its /Alternate: the colour space is /CS1"
		if got := refusal(t, path); !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want it to contain %q", got, want)
		}
	})
}

// iccAltChain is a chain of n garbage-profile /N 3 ICCBased streams, each one's /Alternate the
// next, and the last one's the bare name /DeviceRGB. That chain is the one nesting colourSpaceAt
// resolves rather than refuses, and so the path its depth limit has to bound. Returns the objects,
// numbered from 5, and the array to put in /CS0.
func iccAltChain(n int) (objs []string, cs0 string) {
	garbage := []byte("not a profile at all")
	for i := 0; i < n; i++ {
		alt := "/DeviceRGB"
		if i < n-1 {
			alt = fmt.Sprintf("[/ICCBased %d 0 R]", 6+i)
		}
		objs = append(objs, iccStreamAlt(garbage, 3, alt))
	}
	return objs, "[/ICCBased 5 0 R]"
}

// TestICCAlternateDepthIsLimited pins colourSpaceAt's "nested more than 4 deep" guard at the exact
// chain length it applies to: colourSpaceAt is called once per link, at depths 0..n on a chain of n
// streams ending in the bare name /DeviceRGB, so a chain of 4 streams reaches that name at depth
// 4 — not over the limit — and a chain of 5 reaches it at depth 5.
func TestICCAlternateDepthIsLimited(t *testing.T) {
	t.Run("a chain of 4 ICCBased streams still draws", func(t *testing.T) {
		objs, cs0 := iccAltChain(4)
		path := colourPDF(t, 10, 10, "/CS0"+cs0, "/CS0 cs 1 0 0 scn 0 0 10 10 re f", objs...)
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want (255,0,0) — 4 deep must still draw", r, g, b)
		}
	})
	t.Run("a chain of 5 ICCBased streams is refused", func(t *testing.T) {
		objs, cs0 := iccAltChain(5)
		path := colourPDF(t, 10, 10, "/CS0"+cs0, "/CS0 cs 1 0 0 scn 0 0 10 10 re f", objs...)
		want := "a colour space nested more than 4 deep"
		if got := refusal(t, path); !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want it to contain %q", got, want)
		}
	})
}

// TestUndrawableColourIsRefusedByReason is colour.go's refusals, at every operator that can carry
// one and only there — a colour space that nothing paints in is drawn, per marks' comment.
func TestUndrawableColourIsRefusedByReason(t *testing.T) {
	for _, c := range []struct {
		name, cs, want string
	}{
		{"Separation", "[/Separation/Spot/DeviceGray<</FunctionType 2/Domain[0 1]/C0[0]/C1[1]/N 1>>]", "fill: the colour space is /Separation"},
		{"DeviceN", "[/DeviceN[/A/B]/DeviceRGB<</FunctionType 2/Domain[0 1]/C0[0 0 0]/C1[1 1 1]/N 1>>]", "fill: the colour space is /DeviceN"},
		{"Indexed", "[/Indexed/DeviceRGB 1<000000FFFFFF>]", "fill: the colour space is /Indexed"},
		{"Lab", "[/Lab<</WhitePoint[0.9505 1 1.089]>>]", "fill: the colour space is /Lab"},
		{"CalGray", "[/CalGray<</WhitePoint[0.9505 1 1.089]>>]", "fill: the colour space is /CalGray"},
		{"CalRGB", "[/CalRGB<</WhitePoint[0.9505 1 1.089]>>]", "fill: the colour space is /CalRGB"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Every painting operator that fills is checked, not only f.
			for _, op := range []string{"f", "F", "f*", "B", "B*", "b", "b*"} {
				stream := fmt.Sprintf("/CS0 cs 0.5 scn 0 G 1 w 0 0 10 10 re %s", op)
				path := colourPDF(t, 10, 10, "/CS0"+c.cs, stream)
				got := refusal(t, path)
				if !strings.Contains(got, c.want) {
					t.Errorf("%s: refusal = %q, want it to contain %q", op, got, c.want)
				}
			}
			// The stroke side refuses the same family, by the same reason prefixed "stroke:".
			path := colourPDF(t, 10, 10, "/CS0"+c.cs, "/CS0 CS 0.5 SCN 1 w 0 5 m 10 5 l S")
			got := refusal(t, path)
			want := "stroke: " + strings.TrimPrefix(c.want, "fill: ")
			if !strings.Contains(got, want) {
				t.Errorf("stroke: refusal = %q, want it to contain %q", got, want)
			}
		})
	}

	t.Run("a Pattern fill", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "", "/Pattern cs /P0 scn 0 0 10 10 re f")
		if got := refusal(t, path); !strings.Contains(got, "fill: a Pattern colour") {
			t.Errorf("refusal = %q, want %q", got, "fill: a Pattern colour")
		}
	})
	t.Run("a Pattern stroke", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "", "/Pattern CS /P0 SCN 1 w 0 5 m 10 5 l S")
		if got := refusal(t, path); !strings.Contains(got, "stroke: a Pattern colour") {
			t.Errorf("refusal = %q, want %q", got, "stroke: a Pattern colour")
		}
	})
	t.Run("a colour space not in the resources", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "", "/CS9 cs 0 0 10 10 re f")
		want := "fill: a colour space not in the resources, /CS9"
		if got := refusal(t, path); !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want %q", got, want)
		}
	})
	t.Run("a colour space that did not resolve", func(t *testing.T) {
		// objects/pdfcpu's Resolve never errors on a missing object — it returns Null — and
		// errors only on a type Resolve cannot convert, so no PDF in this corpus reaches this
		// branch through it. errRefStore wraps a real Store and fails to resolve one chosen
		// reference instead, the same embedding boxStore uses in native_test.go.
		path := colourPDF(t, 10, 10, "/CS0 5 0 R", "/CS0 cs 1 0 0 scn 0 0 10 10 re f")
		s, err := pcstore.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = s.Close() }()
		o := render.DefaultOptions
		o.DPI = 72
		_, err = New(errRefStore{Store: s, bad: objects.Ref{Num: 5}}).Page(1, o)
		var u *Unsupported
		if !errors.As(err, &u) {
			t.Fatalf("got %v, %v; want an *Unsupported", u, err)
		}
		got := strings.Join(u.Ops, " | ")
		want := "fill: a colour space that did not resolve"
		if !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want it to contain %q", got, want)
		}
	})
	t.Run("a resource entry that names another resource is not looked up again", func(t *testing.T) {
		// /CS0's entry in the resources is the name /CS1, not a colour space object — a
		// resource's value may be a device name (Table 73) but §8.6.3 gives it no reading as
		// another resource name, so this refuses rather than chasing it to /CS1's own entry,
		// /DeviceRGB.
		//
		// Measured: pdfium leaves this white too, not red — it does not chase a resource's
		// name value to another resource either.
		path := colourPDF(t, 10, 10, "/CS0/CS1/CS1/DeviceRGB", "/CS0 cs 1 0 0 scn 0 0 10 10 re f")
		want := "fill: the colour space is /CS1"
		if got := refusal(t, path); !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want %q", got, want)
		}
	})
	t.Run("an empty colour space array is refused, not indexed", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "/CS0[]", "/CS0 cs 0 0 10 10 re f")
		want := "fill: a malformed colour space"
		if got := refusal(t, path); !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want %q", got, want)
		}
	})
	t.Run("a one-element device array draws the same as the bare name", func(t *testing.T) {
		// [/DeviceRGB] is the device space under another spelling (§8.6.3) — measured, pdfium
		// draws it the same as the bare name.
		path := colourPDF(t, 10, 10, "/CS0[/DeviceRGB]", "/CS0 cs 1 0 0 scn 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want (255,0,0)", r, g, b)
		}
	})
	t.Run("a Pattern family array is refused the same as the bare name", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "/CS0[/Pattern/DeviceRGB]", "/CS0 cs 0 0 10 10 re f")
		want := "fill: a Pattern colour"
		if got := refusal(t, path); !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want %q", got, want)
		}
	})
	t.Run("an ICCBased array with no profile at all is refused, not indexed", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "/CS0[/ICCBased]", "/CS0 cs 0 0 10 10 re f")
		want := "fill: an ICCBased space without a profile"
		if got := refusal(t, path); !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want %q", got, want)
		}
	})
	t.Run("an ICCBased profile that resolves but is not a stream", func(t *testing.T) {
		for _, c := range []struct {
			name, cs string
			extra    []string
		}{
			{"an indirect dictionary", "/CS0[/ICCBased 5 0 R]", []string{"<</Foo 1>>"}},
			{"a direct non-stream", "/CS0[/ICCBased 5]", nil},
		} {
			t.Run(c.name, func(t *testing.T) {
				path := colourPDF(t, 10, 10, c.cs, "/CS0 cs 0 0 10 10 re f", c.extra...)
				want := "fill: an ICCBased profile that is not a stream"
				if got := refusal(t, path); !strings.Contains(got, want) {
					t.Errorf("refusal = %q, want %q", got, want)
				}
			})
		}
	})

	sep := "/CS0[/Separation/Spot/DeviceGray<</FunctionType 2/Domain[0 1]/C0[0]/C1[1]/N 1>>]"
	t.Run("a visible show is refused for its fill space too", func(t *testing.T) {
		// Every showing operator fills its glyphs with the fill colour, and each one is checked
		// separately here: Tj and ' show a bare string, " takes aw and ac ahead of it, and TJ
		// takes an array.
		for _, c := range []struct {
			name, show string
		}{
			{"Tj", "(x) Tj"},
			{"'", "(x) '"},
			{"\"", "0 0 (x) \""},
			{"TJ", "[(x)] TJ"},
		} {
			t.Run(c.name, func(t *testing.T) {
				stream := "/CS0 cs 0.5 scn BT /F1 12 Tf 5 5 Td " + c.show + " ET"
				got := refusal(t, colourPDF(t, 10, 10, sep, stream))
				want := "fill: the colour space is /Separation"
				if !strings.Contains(got, want) {
					t.Errorf("refusal = %q, want it to contain %q", got, want)
				}
				// It also has a text: reason for having no font, which is not what this test is about.
			})
		}
	})
	t.Run("render mode 3 shows nothing and has no fill reason", func(t *testing.T) {
		stream := "/CS0 cs 0.5 scn BT /F1 12 Tf 3 Tr 5 5 Td (x) Tj ET"
		path := colourPDF(t, 10, 10, sep, stream)
		if _, err := renderAt72(t, path); err != nil {
			t.Errorf("Page: %v, want it drawn — render mode 3 shows nothing, so its fill space is"+
				" never checked", err)
		}
	})
	t.Run("a space selected and never painted in is drawn", func(t *testing.T) {
		path := colourPDF(t, 10, 10, sep, "/CS0 cs 0 g 0 0 10 10 re f")
		if _, err := renderAt72(t, path); err != nil {
			t.Errorf("Page: %v, want it drawn — 0 g reset the fill before anything painted in the"+
				" Separation space", err)
		}
	})
	t.Run("a space selected inside q is drawn once Q restores the outer one", func(t *testing.T) {
		path := colourPDF(t, 10, 10, sep, "q /CS0 cs Q 0 0 10 10 re f")
		if _, err := renderAt72(t, path); err != nil {
			t.Errorf("Page: %v, want it drawn — Q restored the page's initial DeviceGray before the"+
				" fill", err)
		}
	})

	t.Run("Default spaces", func(t *testing.T) {
		t.Run("DefaultRGB refuses an rg fill", func(t *testing.T) {
			path := colourPDF(t, 10, 10, "/DefaultRGB/DeviceRGB", "1 0 0 rg 0 0 10 10 re f")
			want := "fill: the resources define /DefaultRGB, which is not implemented"
			if got := refusal(t, path); !strings.Contains(got, want) {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
		t.Run("DefaultGray refuses the initial fill with no colour operator at all", func(t *testing.T) {
			path := colourPDF(t, 10, 10, "/DefaultGray/DeviceGray", "0 0 10 10 re f")
			want := "fill: the resources define /DefaultGray, which is not implemented"
			if got := refusal(t, path); !strings.Contains(got, want) {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
		t.Run("DefaultGray refuses a DeviceGray image", func(t *testing.T) {
			im := hexImage(1, 1, "DeviceGray", "00", "")
			page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]" +
				"/Resources<</XObject<</Im0 5 0 R>>/ColorSpace<</DefaultGray/DeviceGray>>>>/Contents 4 0 R>>"
			objs := append(pageObjs(page, "q 10 0 0 10 0 0 cm /Im0 Do Q\n"), im)
			path := buildPDF(t, objs, "defaultgray-img.pdf")
			want := "Do: an image that cannot be drawn, /Im0: the resources define /DefaultGray, which is not implemented"
			if got := refusal(t, path); !strings.Contains(got, want) {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
		t.Run("DefaultRGB in a form refuses a colour selected outside it (paint-time)", func(t *testing.T) {
			form := formXO("0 0 10 10", "/Resources<</ColorSpace<</DefaultRGB/DeviceRGB>>>>", "0 0 10 10 re f")
			page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]/Resources<</XObject<</Fm1 5 0 R>>>>/Contents 4 0 R>>"
			objs := append(pageObjs(page, "1 0 0 rg /Fm1 Do\n"), form)
			path := buildPDF(t, objs, "defaultrgb-form.pdf")
			want := "fill: the resources define /DefaultRGB, which is not implemented"
			if got := refusal(t, path); !strings.Contains(got, want) {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
		t.Run("DefaultRGB on the page refuses a colour even inside a form with none (selection-time)", func(t *testing.T) {
			// The form gets its own /Resources, empty, so at paint time — with w.res the form's
			// own resources — defaultRefusal finds no /ColorSpace at all and cannot refuse on its
			// own. Only the refusal cached when rg was selected on the page, under the page's
			// /DefaultRGB, still refuses it here; a form with no /Resources at all would inherit
			// the page's and let the paint-time check alone explain the pass.
			form := formXO("0 0 10 10", "/Resources<<>>", "0 0 10 10 re f")
			page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]" +
				"/Resources<</XObject<</Fm1 5 0 R>>/ColorSpace<</DefaultRGB/DeviceRGB>>>>/Contents 4 0 R>>"
			objs := append(pageObjs(page, "1 0 0 rg /Fm1 Do\n"), form)
			path := buildPDF(t, objs, "defaultrgb-page.pdf")
			want := "fill: the resources define /DefaultRGB, which is not implemented"
			if got := refusal(t, path); !strings.Contains(got, want) {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
		t.Run("DefaultGray refuses an ICCBased space that falls back to its DeviceGray /Alternate", func(t *testing.T) {
			path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]/DefaultGray/DeviceGray",
				"/CS0 cs 0.5 scn 0 0 10 10 re f", iccStreamAlt([]byte("not a profile at all"), 1, "/DeviceGray"))
			want := "fill: the resources define /DefaultGray, which is not implemented"
			if got := refusal(t, path); !strings.Contains(got, want) {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
		t.Run("DefaultGray does not refuse an ICCBased space drawn through its profile", func(t *testing.T) {
			path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]/DefaultGray/DeviceGray",
				"/CS0 cs 0.5 scn 0 0 10 10 re f", iccStream(gray22, 1))
			if _, err := renderAt72(t, path); err != nil {
				t.Errorf("Page: %v, want it drawn — a profile is not a device formula", err)
			}
		})
		t.Run("a space first resolved under a form's DefaultGray is drawn on the page without one", func(t *testing.T) {
			// iccSpaces caches the space by reference. The form resolves it first, under its own
			// /DefaultGray, and only selects it; the page then paints in it with no Default* in
			// force. A cache that kept the form's answer would refuse the page.
			alt := iccStreamAlt([]byte("not a profile at all"), 1, "/DeviceGray")
			form := formXO("0 0 10 10", "/Resources<</ColorSpace<</CS0[/ICCBased 6 0 R]/DefaultGray/DeviceGray>>>>", "/CS0 cs")
			page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]" +
				"/Resources<</XObject<</Fm1 5 0 R>>/ColorSpace<</CS0[/ICCBased 6 0 R]>>>>/Contents 4 0 R>>"
			objs := append(pageObjs(page, "/Fm1 Do /CS0 cs 0.5 scn 0 0 10 10 re f\n"), form, alt)
			ra, err := renderAt72(t, buildPDF(t, objs, "icc-cache.pdf"))
			if err != nil {
				t.Fatalf("Page: %v, want it drawn", err)
			}
			checkPixels(t, ra, []pixel{{5, 5, 128, 128, 128}}, 0)
		})
		t.Run("only DefaultCMYK does not refuse an rg fill", func(t *testing.T) {
			path := colourPDF(t, 10, 10, "/DefaultCMYK/DeviceCMYK", "1 0 0 rg 0 0 10 10 re f")
			if _, err := renderAt72(t, path); err != nil {
				t.Errorf("Page: %v, want it drawn — only /DefaultCMYK is defined, and this is an"+
					" RGB fill", err)
			}
		})
		t.Run("DefaultCMYK refuses a k fill", func(t *testing.T) {
			path := colourPDF(t, 10, 10, "/DefaultCMYK/DeviceCMYK", "0 0 0 1 k 0 0 10 10 re f")
			want := "fill: the resources define /DefaultCMYK, which is not implemented"
			if got := refusal(t, path); !strings.Contains(got, want) {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
	})

	t.Run("images", func(t *testing.T) {
		for _, c := range []struct {
			name, cs, want string
		}{
			{"Separation", "/Separation/Spot/DeviceGray<</FunctionType 2/Domain[0 1]/C0[0]/C1[1]/N 1>>",
				"the colour space is /Separation"},
			{"Lab", "/Lab<</WhitePoint[0.9505 1 1.089]>>", "the colour space is /Lab"},
			{"Indexed over Separation", "/Indexed[/Separation/Spot/DeviceGray<</FunctionType 2/Domain[0 1]/C0[0]/C1[1]/N 1>>]1<0000FF>",
				"the colour space is /Separation"},
		} {
			t.Run(c.name, func(t *testing.T) {
				im := fmt.Sprintf("<</Type/XObject/Subtype/Image/Width 1/Height 1/ColorSpace[%s]"+
					"/BitsPerComponent 8/Filter/ASCIIHexDecode/Length 3>>\nstream\n00>\nendstream", c.cs)
				page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]/Resources<</XObject<</Im0 5 0 R>>>>/Contents 4 0 R>>"
				objs := append(pageObjs(page, "q 10 0 0 10 0 0 cm /Im0 Do Q\n"), im)
				path := buildPDF(t, objs, "imgcs.pdf")
				want := "Do: an image that cannot be drawn, /Im0: " + c.want
				if got := refusal(t, path); !strings.Contains(got, want) {
					t.Errorf("refusal = %q, want it to contain %q", got, want)
				}
			})
		}
	})
}

// TestColourStateIsTheSpacesInitialColourAndIsRestoredByQ is cs/CS's initial colour (§8.6.5.2 and
// following, and §8.6.5.5 for ICCBased), q/Q's restore of the fill space and not only the fill
// colour, and the survey/paint separation colour shares with every other piece of state.
func TestColourStateIsTheSpacesInitialColourAndIsRestoredByQ(t *testing.T) {
	t.Run("cs resets to black even after rg", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "", "1 0 0 rg /DeviceRGB cs 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 0 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want black", r, g, b)
		}
	})
	t.Run("DeviceCMYK cs starts black", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "", "/DeviceCMYK cs 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 0 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want black", r, g, b)
		}
	})
	t.Run("an ICC space's initial colour is zero through its own profile", func(t *testing.T) {
		// §8.6.5.5 gives an ICCBased space an initial colour of all zeros. This profile's kTRC
		// maps 0 to 0.5, so the initial colour is not black: 167 = round(255·enc((0.5-t)/(1-t))),
		// enc being sRGB's encoding and t = (66/116)³ the black point compensation takes from a
		// black at L* 76, clipped to 50. pdfium draws the same 167 for an explicit "0 scn", but its
		// own initial colour is black, never converted — measured, and not copied here.
		profile := iccbuild.Profile("GRAY", iccbuild.Tag{Sig: "kTRC", Data: iccbuild.Curve(32768, 65535)})
		path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]", "/CS0 cs 0 0 10 10 re f", iccStream(profile, 1))
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 167 || g != 167 || b != 167 {
			t.Errorf("got (%d,%d,%d), want (167,167,167)", r, g, b)
		}
	})
	t.Run("a garbage /N 4 profile falls back to CMYK, whose all-zero initial colour is white", func(t *testing.T) {
		// §8.6.5.5's own reading, not compared against pdfium: pdfium was observed drawing black
		// for a garbage /N 4 profile, probably because it failed to load the space at all rather
		// than falling back to the device space — a documented divergence, not copied here.
		path := colourPDF(t, 10, 10, "/CS0[/ICCBased 5 0 R]", "/CS0 cs 0 0 10 10 re f",
			iccStream([]byte("not a profile at all"), 4))
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 255 || g != 255 || b != 255 {
			t.Errorf("got (%d,%d,%d), want white", r, g, b)
		}
	})
	t.Run("too few components for DeviceRGB keeps the colour", func(t *testing.T) {
		// cs itself resets the colour to the space's initial black (§8.6.5.2), so the red has to
		// come from a full scn inside the space — the malformed operator is the one after it.
		path := colourPDF(t, 10, 10, "", "/DeviceRGB cs 1 0 0 scn 0.5 scn 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want (255,0,0) — too few components changed nothing", r, g, b)
		}
	})
	t.Run("two fewer components than DeviceRGB also keeps the colour", func(t *testing.T) {
		// A second, still-malformed scn right after the first: sp.n is 3, len(v) is 1, two short
		// rather than one, which is the case an off-by-one guard would slice past — measured,
		// pdfium keeps this red too.
		path := colourPDF(t, 10, 10, "", "/DeviceRGB cs 1 0 0 scn 0.5 0.5 scn 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want (255,0,0) — too few components changed nothing", r, g, b)
		}
	})
	t.Run("more components than DeviceRGB takes the first three", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "", "/DeviceRGB cs 1 0 0 0.5 sc 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want (255,0,0)", r, g, b)
		}
	})
	t.Run("Q restores the fill space, not only the colour", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "/CS1[/ICCBased 5 0 R]/CS0[/ICCBased 6 0 R]",
			"/CS1 cs q /CS0 cs Q 0.5 sc 0 0 10 10 re f",
			iccStream(gray10, 1), iccStream(gray22, 1))
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 188 || g != 188 || b != 188 {
			t.Errorf("got (%d,%d,%d), want (188,188,188) — Q must restore CS1's space, not just its"+
				" colour", r, g, b)
		}
	})
	t.Run("the survey's state does not reach the paint pass", func(t *testing.T) {
		path := colourPDF(t, 10, 10, "/CS1[/ICCBased 5 0 R]",
			"0.5 sc 0 0 10 10 re f /CS1 cs", iccStream(gray10, 1))
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 128 || g != 128 || b != 128 {
			t.Errorf("got (%d,%d,%d), want (128,128,128) — the fill ran before /CS1 cs, in the"+
				" page's initial DeviceGray", r, g, b)
		}
	})
	t.Run("k clamps its components before converting", func(t *testing.T) {
		// §8.6.4.4's reading, not compared against pdfium: -0.5 0 0 0.7 k, clamped, reads as
		// 0 0 0 0.7 and gives r = 1 - min(1, 0+0.7) = 0.3 -> round(0.3·255) = 77. Unclamped, c
		// stays -0.5 and gives 1 - min(1, -0.5+0.7) = 0.8 -> 204.
		path := colourPDF(t, 10, 10, "", "-0.5 0 0 0.7 k 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 77 || g != 77 || b != 77 {
			t.Errorf("got (%d,%d,%d), want (77,77,77)", r, g, b)
		}
	})
	t.Run("a malformed rg does not switch the space away from DeviceGray", func(t *testing.T) {
		// 1 0 rg has 2 operands where rg needs 3, so it is dropped entirely: the space stays the
		// DeviceGray g set, and 0.25 sc reads in it — round(0.25·255) = 64. Measured, pdfium
		// drops it the same way and also draws 64.
		path := colourPDF(t, 10, 10, "", "0.5 g 1 0 rg 0.25 sc 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 64 || g != 64 || b != 64 {
			t.Errorf("got (%d,%d,%d), want (64,64,64) — the malformed rg must not have switched"+
				" the space to DeviceRGB", r, g, b)
		}
	})
	t.Run("cs with a non-name operand is ignored", func(t *testing.T) {
		// "1 cs" reads its operand as a number, not a name — op.NameAt(0) has no reading for it
		// and returns "" — so cs changes nothing and the red from rg is still there. Measured,
		// pdfium ignores it the same way.
		path := colourPDF(t, 10, 10, "", "1 0 0 rg 1 cs 0 0 10 10 re f")
		r, g, b := rgbAt(renderNative(t, path, 72).Image, 5, 5)
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("got (%d,%d,%d), want (255,0,0) — cs with no name must not have changed the space", r, g, b)
		}
	})
}

// TestIndexedImageOverAnICCBase pins the combination image.go's own fix and colour.go's imageSpace
// both had to get right: a palette entry is raw bytes in the base space, and the base space here is
// ICCBased, so the conversion has to run after the palette lookup expands the index, not instead of
// it.
func TestIndexedImageOverAnICCBase(t *testing.T) {
	p, err := icc.Parse(gray10)
	if err != nil {
		t.Fatal(err)
	}
	sr, _, _ := p.SRGB([]float64{128.0 / 255})
	want := int(math.Round(sr * 255)) // round(255·enc(128/255)) through gray10

	im := "<</Type/XObject/Subtype/Image/Width 2/Height 1/ColorSpace[/Indexed[/ICCBased 6 0 R]1<0080>]" +
		"/BitsPerComponent 8/Filter/ASCIIHexDecode/Length 5>>\nstream\n0001>\nendstream"
	page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 2 1]/Resources<</XObject<</Im0 5 0 R>>>>/Contents 4 0 R>>"
	objs := append(pageObjs(page, "q 2 0 0 1 0 0 cm /Im0 Do Q\n"), im, iccStream(gray10, 1))
	path := buildPDF(t, objs, "idxicc.pdf")

	ref, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium: %v", err)
	}
	defer func() { _ = ref.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	pdfWant, err := ref.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}
	got := renderNative(t, path, 72)

	for _, c := range []struct {
		x, y                int
		wantR, wantG, wantB int
	}{
		{0, 0, 0, 0, 0},
		{1, 0, want, want, want},
	} {
		pr, pg, pb := rgbAt(pdfWant.Image, c.x, c.y)
		if pr != c.wantR || pg != c.wantG || pb != c.wantB {
			t.Errorf("pdfium (%d,%d) = (%d,%d,%d), want (%d,%d,%d)", c.x, c.y, pr, pg, pb, c.wantR, c.wantG, c.wantB)
		}
		nr, ng, nb := rgbAt(got.Image, c.x, c.y)
		if nr != c.wantR || ng != c.wantG || nb != c.wantB {
			t.Errorf("native (%d,%d) = (%d,%d,%d), want (%d,%d,%d)", c.x, c.y, nr, ng, nb, c.wantR, c.wantG, c.wantB)
		}
	}
}

package native

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"strings"
	"testing"

	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
)

// xoPDF writes a 200-point page whose resources are the /XObject entries given and a /GS0 at half
// alpha, with extra objects numbered from 5.
//
// Built rather than committed, for the reason gsPDF gives: each case is a different arrangement of
// forms and images, and what is under test is which arrangement produces which pixels.
func xoPDF(t *testing.T, xobjects, stream string, extra ...string) string {
	t.Helper()
	page := "<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]/Resources<</XObject<<" + xobjects +
		">>/ExtGState<</GS0<</ca 0.5>>>>>>/Contents 4 0 R>>"
	return buildPDF(t, append(pageObjs(page, stream+"\n"), extra...), "xobject.pdf")
}

// hexImage is an 8-bit image XObject whose samples are written as hex.
func hexImage(w, h int, cs, samples, dict string) string {
	return fmt.Sprintf("<</Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace/%s"+
		"/BitsPerComponent 8/Filter/ASCIIHexDecode%s/Length %d>>\nstream\n%s>\nendstream",
		w, h, cs, dict, len(samples)+1, samples)
}

// zeroImage is a DeviceGray image of w×h black samples, Flate-compressed so a large one is a few
// kilobytes of fixture.
func zeroImage(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	z := zlib.NewWriter(&b)
	if _, err := z.Write(make([]byte, w*h)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("<</Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace/DeviceGray"+
		"/BitsPerComponent 8/Filter/FlateDecode/Length %d>>\nstream\n%s\nendstream", w, h, b.Len(), b.Bytes())
}

// formXO is a form XObject.
func formXO(bbox, dict, body string) string {
	return fmt.Sprintf("<</Type/XObject/Subtype/Form/BBox[%s]%s/Length %d>>\nstream\n%s\nendstream",
		bbox, dict, len(body), body)
}

func renderAt72(t *testing.T, path string) (*render.Raster, error) {
	t.Helper()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	return New(s).Page(1, o)
}

// pixel is one expected device pixel, top-left origin, at 72 dpi so a device pixel is a point.
type pixel struct {
	x, y    int
	r, g, b int
}

func checkPixels(t *testing.T, ra *render.Raster, want []pixel, tol int) {
	t.Helper()
	for _, p := range want {
		r, g, b := rgbAt(ra.Image, p.x, p.y)
		if absInt(r-p.r) > tol || absInt(g-p.g) > tol || absInt(b-p.b) > tol {
			t.Errorf("(%d,%d) = (%d,%d,%d), want (%d,%d,%d)", p.x, p.y, r, g, b, p.r, p.g, p.b)
		}
	}
}

// refusal renders a page that must be refused and returns its reasons joined.
func refusal(t *testing.T, path string) string {
	t.Helper()
	ra, err := renderAt72(t, path)
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("got %v, %v; want an *Unsupported", ra, err)
	}
	return strings.Join(u.Ops, " | ")
}

// TestImageIsPlacedAndOriented pins §8.9.4's mapping: the unit square through the CTM, with sample
// row 0 at the *top* of it.
//
// A 2×2 image of four distinct colours is the smallest fixture where every one of the eight ways
// to get the orientation wrong — either flip, either transpose, and their compositions — puts a
// different colour in some quadrant. A quadrant's centre pixel samples 0.005 of a sample from a
// sample centre, so bilinear filtering returns the pure colour to within 3 of 255 on the side where
// it does not clamp — and a wrong orientation is 255 away.
func TestImageIsPlacedAndOriented(t *testing.T) {
	const (
		red   = "FF0000"
		green = "00FF00"
		blue  = "0000FF"
		black = "000000"
	)
	im := hexImage(2, 2, "DeviceRGB", red+green+blue+black, "")
	var (
		R = [3]int{255, 0, 0}
		G = [3]int{0, 255, 0}
		B = [3]int{0, 0, 255}
		K = [3]int{0, 0, 0}
	)
	for _, c := range []struct {
		name           string
		cm             string
		tl, tr, bl, br [3]int
	}{
		{"upright", "200 0 0 200 0 0", R, G, B, K},
		{"flipped vertically", "200 0 0 -200 0 200", B, K, R, G},
		{"mirrored horizontally", "-200 0 0 200 200 0", G, R, K, B},
		{"rotated a quarter turn", "0 200 -200 0 200 0", G, K, R, B},
	} {
		t.Run(c.name, func(t *testing.T) {
			ra, err := renderAt72(t, xoPDF(t, "/Im1 5 0 R", "q "+c.cm+" cm /Im1 Do Q", im))
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			var want []pixel
			for _, q := range []struct {
				x, y int
				c    [3]int
			}{{50, 50, c.tl}, {150, 50, c.tr}, {50, 150, c.bl}, {150, 150, c.br}} {
				want = append(want, pixel{q.x, q.y, q.c[0], q.c[1], q.c[2]})
			}
			checkPixels(t, ra, want, 3)
		})
	}
}

// TestImagesAgreeWithPdfium is the image half of the acceptance test.
//
// A checkerboard of 10-sample squares, because it has an edge every ten samples: any error in
// placement, scale or orientation moves a hundred edges at once, where a smooth image would hide a
// half-sample shift. Drawn at its own resolution, at a quarter of it — the downscale the
// subsampling exists for — and rotated, where every edge pixel is a resampling decision.
//
// The tolerances are measured. Axis-aligned, the two backends agree to a mean of 0.000, so any
// shift at all fails. Rotated, the mean is 6.7 of 255 with the geometry the same: pdfium's edge
// filter is visibly softer than bilinear — it even rounds the image's corners — and every one of a
// checkerboard's edges is an edge pixel. A rotation the wrong way or a flip moves whole squares,
// which is tens, so 8 separates a filter from a mistake and no more finely than that.
func TestImagesAgreeWithPdfium(t *testing.T) {
	var s strings.Builder
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if (x/10+y/10)%2 == 0 {
				s.WriteString("20")
			} else {
				s.WriteString("E0")
			}
		}
	}
	im := hexImage(100, 100, "DeviceGray", s.String(), "")
	for _, c := range []struct {
		name string
		cm   string
		mean float64
	}{
		{"at its own resolution", "100 0 0 100 50 50", 0.05},
		{"at a quarter of it", "25 0 0 25 80 80", 0.05},
		{"rotated thirty degrees", "86.6 50 -50 86.6 100 20", 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			mean, worst, over := comparePath(t, xoPDF(t, "/Im1 5 0 R", "q "+c.cm+" cm /Im1 Do Q", im), 72)
			t.Logf("mean %.3f worst %.0f over-32 %d", mean, worst, over)
			if mean > c.mean {
				t.Errorf("mean %.3f, want at most %.2f — the image disagrees with pdfium", mean, c.mean)
			}
		})
	}
}

// TestDownscaleAveragesRatherThanAliases pins the subsampling. One dark column in four, drawn at a
// quarter of its width, is a quarter dark everywhere: 191 of 255. One sample per pixel lands on the
// same phase of the pattern in every pixel — here between two light columns — and draws it white.
//
// The checkerboard above cannot see this: its 10-sample squares make a single bilinear sample at a
// quarter scale land on a seam exactly when the pixel's area straddles one, so it agrees with the
// average by coincidence.
func TestDownscaleAveragesRatherThanAliases(t *testing.T) {
	var s strings.Builder
	for x := 0; x < 100; x++ {
		if x%4 == 0 {
			s.WriteString("00")
		} else {
			s.WriteString("FF")
		}
	}
	im := hexImage(100, 1, "DeviceGray", s.String(), "")
	ra, err := renderAt72(t, xoPDF(t, "/Im1 5 0 R", "q 25 0 0 25 0 0 cm /Im1 Do Q", im))
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	checkPixels(t, ra, []pixel{{10, 190, 191, 191, 191}}, 2)
}

// TestSoftMaskAlphaAndClipApplyToImages pins the three things that make an image less than
// opaque, each against the arithmetic rather than a direction: a mask of 0x80 over black is
// 127 of 255, half /ca is 128, and a clip is all or nothing.
func TestSoftMaskAlphaAndClipApplyToImages(t *testing.T) {
	black := hexImage(1, 1, "DeviceGray", "00", "")
	for _, c := range []struct {
		name, stream string
		extra        []string
		want         []pixel
	}{
		{"soft mask", "q 200 0 0 200 0 0 cm /Im1 Do Q",
			[]string{hexImage(1, 1, "DeviceGray", "00", "/SMask 6 0 R"), hexImage(1, 1, "DeviceGray", "80", "")},
			[]pixel{{100, 100, 127, 127, 127}}},
		{"constant alpha", "/GS0 gs q 200 0 0 200 0 0 cm /Im1 Do Q",
			[]string{black},
			[]pixel{{100, 100, 128, 128, 128}}},
		{"clip", "0 0 100 200 re W n q 200 0 0 200 0 0 cm /Im1 Do Q",
			[]string{black},
			[]pixel{{50, 100, 0, 0, 0}, {150, 100, 255, 255, 255}}},
		// Opaque blue beside all-but-transparent red. At the seam the pixel is about half covered
		// and nearly all of that cover is blue's, so it is a pale blue with no red to speak of:
		// (129, 128, 255). Interpolating colour without weighting by alpha makes it white.
		// Alpha 1/255 rather than 0, because a sample at 0 reaches here already black — the
		// decoder's premultiplied round trip discards its colour — and black cannot bleed.
		{"a transparent sample's colour does not bleed", "q 200 0 0 200 0 0 cm /Im1 Do Q",
			[]string{hexImage(2, 1, "DeviceRGB", "0000FFFF0000", "/SMask 6 0 R"), hexImage(2, 1, "DeviceGray", "FF01", "")},
			[]pixel{{100, 100, 129, 128, 255}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ra, err := renderAt72(t, xoPDF(t, "/Im1 5 0 R", c.stream, c.extra...))
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			checkPixels(t, ra, c.want, 1)
		})
	}
}

// TestFormIsPlacedAndClipped pins §8.10.1's placement: /Matrix applied *before* the CTM in force at
// the Do, and the result clipped to /BBox in form space.
//
// The composition case is the one that decides the order. A translate by 100 then a halving puts
// the square at 50..100; the halving first puts it at 100..150, and both are plausible pictures.
func TestFormIsPlacedAndClipped(t *testing.T) {
	const (
		ink   = 0
		paper = 255
	)
	for _, c := range []struct {
		name, stream, form string
		want               []pixel
	}{
		{"matrix", "/Fm1 Do",
			formXO("0 0 100 100", "/Matrix[1 0 0 1 100 100]", "0 g 0 0 100 100 re f"),
			[]pixel{{150, 50, ink, ink, ink}, {50, 150, paper, paper, paper}, {50, 50, paper, paper, paper}}},
		{"matrix before the CTM", "q 0.5 0 0 0.5 0 0 cm /Fm1 Do Q",
			formXO("0 0 100 100", "/Matrix[1 0 0 1 100 100]", "0 g 0 0 100 100 re f"),
			[]pixel{{75, 125, ink, ink, ink}, {125, 75, paper, paper, paper}}},
		{"bbox clips", "/Fm1 Do",
			formXO("0 0 50 50", "", "0 g 0 0 200 200 re f"),
			[]pixel{{25, 175, ink, ink, ink}, {100, 100, paper, paper, paper}}},
		{"bbox is in form space", "/Fm1 Do",
			formXO("0 0 50 50", "/Matrix[2 0 0 2 0 0]", "0 g 0 0 200 200 re f"),
			[]pixel{{75, 125, ink, ink, ink}, {125, 75, paper, paper, paper}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ra, err := renderAt72(t, xoPDF(t, "/Fm1 5 0 R", c.stream, c.form))
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			checkPixels(t, ra, c.want, 1)
		})
	}
}

// TestFormIsBracketedByQ pins both directions of §8.10.1's implicit q…Q: the form sees the state
// in force at the Do, and nothing it sets outlives it.
//
// The form sets a colour, an alpha and a clip, and then the page fills itself. Each one leaking
// would show differently — a red page, a grey one, a page filled only in its corner — so one
// fixture holds all three.
func TestFormIsBracketedByQ(t *testing.T) {
	t.Run("nothing outlives the form", func(t *testing.T) {
		form := formXO("0 0 200 200", "", "1 0 0 rg /GS0 gs 0 0 10 10 re W n 0 0 1 1 re f")
		ra, err := renderAt72(t, xoPDF(t, "/Fm1 5 0 R", "/Fm1 Do 0 0 200 200 re f", form))
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		checkPixels(t, ra, []pixel{{100, 100, 0, 0, 0}}, 0)
	})
	t.Run("the paint pass starts where the page does", func(t *testing.T) {
		// The survey runs gs too, and ends this page at half alpha. The fill before it is opaque.
		ra, err := renderAt72(t, xoPDF(t, "", "0 g 0 0 200 200 re f /GS0 gs"))
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		checkPixels(t, ra, []pixel{{100, 100, 0, 0, 0}}, 0)
	})
	t.Run("the form inherits the colour", func(t *testing.T) {
		form := formXO("0 0 200 200", "", "0 0 200 200 re f")
		ra, err := renderAt72(t, xoPDF(t, "/Fm1 5 0 R", "1 0 0 rg /Fm1 Do", form))
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		checkPixels(t, ra, []pixel{{100, 100, 255, 0, 0}}, 0)
	})
}

// TestFormResourcesAreItsOwn pins the resource half of §8.10.1: a form with /Resources looks names
// up there, one without looks them up in the stream that drew it, and the page's names are back in
// force after the Do.
//
// The two images share the name /Im1 and differ in colour, so a lookup in the wrong dictionary is a
// wrong colour in a known place rather than a missing picture.
func TestFormResourcesAreItsOwn(t *testing.T) {
	black := hexImage(1, 1, "DeviceGray", "00", "")
	red := hexImage(1, 1, "DeviceRGB", "FF0000", "")
	t.Run("own resources, then the page's again", func(t *testing.T) {
		form := formXO("0 0 200 200", "/Resources<</XObject<</Im1 7 0 R>>>>", "q 100 0 0 200 100 0 cm /Im1 Do Q")
		stream := "q 100 0 0 200 0 0 cm /Im1 Do Q /Fm1 Do q 100 0 0 100 100 0 cm /Im1 Do Q"
		ra, err := renderAt72(t, xoPDF(t, "/Im1 5 0 R /Fm1 6 0 R", stream, black, form, red))
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		checkPixels(t, ra, []pixel{{50, 100, 0, 0, 0}, {150, 50, 255, 0, 0}, {150, 150, 0, 0, 0}}, 0)
	})
	t.Run("inherited resources", func(t *testing.T) {
		form := formXO("0 0 200 200", "", "q 200 0 0 200 0 0 cm /Im1 Do Q")
		ra, err := renderAt72(t, xoPDF(t, "/Im1 5 0 R /Fm1 6 0 R", "/Fm1 Do", black, form))
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		checkPixels(t, ra, []pixel{{100, 100, 0, 0, 0}}, 0)
	})
}

// TestFormFontsAreLookedUpInTheFormsResources pins that the font name cache follows the resources.
//
// The cache is keyed by name, and a name means something only within one resource dictionary. So a
// form whose own /Font has no /F1, drawn after the page has used its /F1, must be refused — and one
// with no /Resources must find the page's /F1 and the size set before the Do, since the text state
// is inherited too.
func TestFormFontsAreLookedUpInTheFormsResources(t *testing.T) {
	t.Run("a form's own resources hide the page's font", func(t *testing.T) {
		form := formXO("0 0 200 200", "/Resources<</Font<<>>>>", "BT /F1 40 Tf 20 60 Td (A) Tj ET")
		path := textPDF(t, "BT /F1 40 Tf 20 60 Td (A) Tj ET /Fm1 Do", 200, withXObjects("/Fm1 8 0 R", form))
		if got := refusal(t, path); !strings.Contains(got, "/Font /F1 is not in the resources") {
			t.Errorf("Ops = %s, want the form's /F1 refused — it was answered from the page's cache", got)
		}
	})
	t.Run("a form with none inherits the page's font and size", func(t *testing.T) {
		form := formXO("0 0 200 200", "", "BT 20 60 Td (A) Tj ET")
		path := textPDF(t, "BT /F1 40 Tf ET /Fm1 Do", 200, withXObjects("/Fm1 8 0 R", form))
		ra, err := renderAt72(t, path)
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		px, _, _ := ink(ra.Image)
		sum := 0
		for _, v := range px {
			sum += int(v)
		}
		if sum == 0 {
			t.Error("the form drew nothing — it did not inherit the font or the size")
		}
	})
}

// TestUndrawableXObjectsAreRefusedByReason is every reason a Do refuses a page, each by the text a
// caller reads.
//
// The permitted counterparts are here too, because each refusal is a claim that the case cannot be
// drawn exactly, and a refusal widened to its neighbours — every transparency group rather than a
// knockout one — would pass a table of refusals alone.
func TestUndrawableXObjectsAreRefusedByReason(t *testing.T) {
	fill := "0 g 0 0 200 200 re f"
	for _, c := range []struct {
		name, xobjects, stream string
		extra                  []string
		want                   string // empty: drawn
	}{
		{"not in the resources", "", "/Nope Do", nil,
			"Do: an XObject not in the resources, /Nope"},
		{"not a stream", "/Fm1 5 0 R", "/Fm1 Do", []string{"<</Type/XObject/Subtype/Form>>"},
			"Do: an XObject that is not a stream, /Fm1"},
		{"PostScript", "/Ps1 5 0 R", "/Ps1 Do", []string{"<</Type/XObject/Subtype/PS/Length 0>>\nstream\n\nendstream"},
			"Do: an XObject of subtype /PS, /Ps1"},
		{"stencil", "/Im1 5 0 R", "/Im1 Do", []string{
			"<</Type/XObject/Subtype/Image/Width 1/Height 1/ImageMask true/Length 1>>\nstream\n\x00\nendstream"},
			"Do: an image that cannot be drawn, /Im1: "},
		{"knockout group", "/Fm1 5 0 R", "/Fm1 Do", []string{
			formXO("0 0 200 200", "/Group<</S/Transparency/K true>>", fill)},
			"Do: a knockout transparency group, /Fm1"},
		{"group at half alpha", "/Fm1 5 0 R", "/GS0 gs /Fm1 Do", []string{
			formXO("0 0 200 200", "/Group<</S/Transparency>>", fill)},
			"Do: a transparency group drawn at constant alpha below 1, /Fm1"},
		{"isolated group at full alpha", "/Fm1 5 0 R", "/Fm1 Do", []string{
			formXO("0 0 200 200", "/Group<</S/Transparency/I true>>", fill)}, ""},
		{"a form that is not a group, at half alpha", "/Fm1 5 0 R", "/GS0 gs /Fm1 Do", []string{
			formXO("0 0 200 200", "", fill)}, ""},
		{"undecodable content", "/Fm1 5 0 R", "/Fm1 Do", []string{
			formXO("0 0 200 200", "/Filter/FlateDecode", "this is not flate")},
			"Do: a form whose content did not decode, /Fm1"},
		{"a form that draws itself", "/Fm1 5 0 R", "/Fm1 Do", []string{
			formXO("0 0 200 200", "/Resources<</XObject<</Fm1 5 0 R>>>>", "/Fm1 Do")},
			fmt.Sprintf("Do: a form nested more than %d deep, /Fm1", maxFormDepth)},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := xoPDF(t, c.xobjects, c.stream, c.extra...)
			if c.want == "" {
				if _, err := renderAt72(t, path); err != nil {
					t.Fatalf("Page: %v, want it drawn", err)
				}
				return
			}
			if got := refusal(t, path); !strings.Contains(got, c.want) {
				t.Errorf("Ops = %s, want %q", got, c.want)
			}
		})
	}
}

// TestNestingDepthIsExact pins maxFormDepth as a number rather than as "some limit": forms nested
// exactly that deep draw, and one more is refused.
func TestNestingDepthIsExact(t *testing.T) {
	chain := func(n int) (string, []string) {
		var objs []string
		for i := 0; i < n; i++ {
			body, res := "0 g 0 0 1 1 re f", ""
			if i < n-1 {
				body, res = "/Fm1 Do", fmt.Sprintf("/Resources<</XObject<</Fm1 %d 0 R>>>>", 6+i)
			}
			objs = append(objs, formXO("0 0 200 200", res, body))
		}
		return "/Fm1 5 0 R", objs
	}
	xo, objs := chain(maxFormDepth)
	if _, err := renderAt72(t, xoPDF(t, xo, "/Fm1 Do", objs...)); err != nil {
		t.Errorf("%d nested forms: %v, want drawn", maxFormDepth, err)
	}
	xo, objs = chain(maxFormDepth + 1)
	if got := refusal(t, xoPDF(t, xo, "/Fm1 Do", objs...)); !strings.Contains(got, "nested more than") {
		t.Errorf("%d nested forms: Ops = %s, want refused for depth", maxFormDepth+1, got)
	}
}

// TestFormFanOutIsBounded pins maxOps: seven forms each drawing the next ten times is 10⁷ operators
// from under a kilobyte, inside the depth limit, and the budget is what refuses it.
func TestFormFanOutIsBounded(t *testing.T) {
	var objs []string
	for i := 0; i < 7; i++ {
		body, res := strings.Repeat("0 0 1 1 re f ", 10), ""
		if i < 6 {
			body = strings.Repeat("/Fm1 Do ", 10)
			res = fmt.Sprintf("/Resources<</XObject<</Fm1 %d 0 R>>>>", 6+i)
		}
		objs = append(objs, formXO("0 0 200 200", res, body))
	}
	got := refusal(t, xoPDF(t, "/Fm1 5 0 R", "/Fm1 Do", objs...))
	if !strings.Contains(got, fmt.Sprintf("run past %d operators", maxOps)) {
		t.Errorf("Ops = %s, want the operator budget", got)
	}
}

// TestImagePixelBudget pins maxPagePixels on each of its three terms: an image over it, a mask that
// takes a small image over it, and — the other direction — one image drawn several times, which is
// decoded once and must be charged once.
func TestImagePixelBudget(t *testing.T) {
	// maxPagePixels+1 as 8193×8192 would do, but nothing is decoded before the refusal and the
	// stream can be empty — the dimensions are the whole claim.
	huge := "<</Type/XObject/Subtype/Image/Width 8193/Height 8192/ColorSpace/DeviceGray/BitsPerComponent 8/Length 0>>\nstream\n\nendstream"
	t.Run("one image over the budget", func(t *testing.T) {
		got := refusal(t, xoPDF(t, "/Im1 5 0 R", "/Im1 Do", huge))
		if !strings.Contains(got, "exceed") {
			t.Errorf("Ops = %s, want the pixel budget", got)
		}
	})
	t.Run("two images that are over it together", func(t *testing.T) {
		// The first is a stencil, which Pixels refuses without decoding — so the fixture costs
		// nothing — but only after the survey has charged it, which is what the second one needs.
		stencil := "<</Type/XObject/Subtype/Image/Width 6000/Height 6000/ImageMask true/Length 0>>\nstream\n\nendstream"
		gray := "<</Type/XObject/Subtype/Image/Width 6000/Height 6000/ColorSpace/DeviceGray/BitsPerComponent 8/Length 0>>\nstream\n\nendstream"
		got := refusal(t, xoPDF(t, "/Im1 5 0 R /Im2 6 0 R", "/Im1 Do /Im2 Do", stencil, gray))
		if !strings.Contains(got, "/Im2: the page's images exceed") {
			t.Errorf("Ops = %s, want /Im2 over the budget — the page's total is what is bounded", got)
		}
	})
	t.Run("a mask that takes an image over it", func(t *testing.T) {
		base := hexImage(1, 1, "DeviceGray", "00", "/SMask 6 0 R")
		got := refusal(t, xoPDF(t, "/Im1 5 0 R", "/Im1 Do", base, huge))
		if !strings.Contains(got, "exceed") {
			t.Errorf("Ops = %s, want the pixel budget — the mask is decoded at its own size", got)
		}
	})
	t.Run("one image drawn five times is charged once", func(t *testing.T) {
		// 4096² is exactly a quarter of the budget, so five charges exceed it and one does not.
		stream := strings.Repeat("q 200 0 0 200 0 0 cm /Im1 Do Q ", 5)
		if _, err := renderAt72(t, xoPDF(t, "/Im1 5 0 R", stream, zeroImage(t, 4096, 4096))); err != nil {
			t.Errorf("Page: %v, want drawn", err)
		}
	})
}

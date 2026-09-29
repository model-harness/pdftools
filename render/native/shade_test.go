package native

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// shadePDF writes a page of w×h points with the resource dictionary entries given, and extra
// objects numbered from 5 — the shape colourPDF and xoPDF have, with every resource the caller's,
// because a shading case needs /Shading, a soft-mask case /ExtGState as well, and some both.
func shadePDF(t *testing.T, w, h int, resources, stream string, extra ...string) string {
	t.Helper()
	page := fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 %d %d]"+
		"/Resources<<%s>>/Contents 4 0 R>>", w, h, resources)
	return buildPDF(t, append(pageObjs(page, stream+"\n"), extra...), "shade.pdf")
}

// axialSh is an axial shading dictionary.
func axialSh(cs, coords, keys, fn string) string {
	return fmt.Sprintf("<</ShadingType 2/ColorSpace%s/Coords[%s]%s/Function%s>>", cs, coords, keys, fn)
}

// expFn is a type 2 function over [0 1].
func expFn(c0, c1, n string) string {
	return fmt.Sprintf("<</FunctionType 2/Domain[0 1]/C0[%s]/C1[%s]/N %s>>", c0, c1, n)
}

// compareRGB measures this backend against pdfium channel by channel, which ink cannot: a
// gradient from red to blue is one ink all the way across.
func compareRGB(t *testing.T, path string, dpi float64) (mean, worst float64) {
	t.Helper()
	ref, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium unavailable, so this comparison cannot run: %v", err)
	}
	defer func() { _ = ref.Close() }()
	o := render.DefaultOptions
	o.DPI = dpi
	want, err := ref.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}
	got := renderNative(t, path, dpi)
	wb, gb := want.Image.Bounds(), got.Image.Bounds()
	if wb.Dx() != gb.Dx() || wb.Dy() != gb.Dy() {
		t.Fatalf("size disagrees: pdfium %v, native %v", wb, gb)
	}
	n := 0
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			pr, pg, pb := rgbAt(want.Image, x, y)
			nr, ng, nb := rgbAt(got.Image, x, y)
			for _, d := range [3]int{pr - nr, pg - ng, pb - nb} {
				f := math.Abs(float64(d))
				mean += f
				worst = math.Max(worst, f)
				n++
			}
		}
	}
	return mean / float64(n), worst
}

// TestAxialShadingsAgreeWithPdfium is the acceptance test for sh.
//
// pdfium samples an axial shading's function at 256 values of t, i/256 of the way along the
// domain, and colours each device pixel with the step its corner falls in: the projection onto the
// axis, times 255, truncated. A spike over a 256-pixel gray ramp found that rule agreeing on every
// pixel, where sampling the pixel's centre disagreed on 127 and steps of i/255 on 126, so the
// cases here are for everything around it: each device space and an ICC one, each Extend, the
// Domain, the function shapes an axial shading may have, and CTMs that turn and stretch the axis.
//
// Every case agrees on every pixel but one. A curved clip is rasterized with coverage, and its
// edge pixels differ from pdfium's by up to 19 levels, as a plain fill under the same clip does;
// that is the clip's antialiasing, not the shading's, so it alone is held to a bound.
func TestAxialShadingsAgreeWithPdfium(t *testing.T) {
	const clip = "q 10 10 180 80 re W n /Sh0 sh Q"
	gray := expFn("0", "1", "1")
	for _, c := range []struct {
		name, sh, stream string
		extra            []string
	}{
		{"gray", axialSh("/DeviceGray", "20 0 180 0", "/Extend[true true]", gray), clip, nil},
		{"rgb", axialSh("/DeviceRGB", "20 0 180 0", "/Extend[true true]",
			expFn("1 0 0", "0 0.5 1", "1")), clip, nil},
		{"icc", axialSh(" 6 0 R", "20 0 180 0", "/Extend[true true]",
			expFn("0.0431373 0.145098 0.254902", "0.0980392 0.235294 0.647059", "1")), clip,
			[]string{"[/ICCBased 7 0 R]", iccStream(adobe22, 3)}},
		{"no extend", axialSh("/DeviceGray", "40 0 160 0", "", gray), clip, nil},
		{"start extend", axialSh("/DeviceGray", "40 0 160 0", "/Extend[true false]", gray), clip, nil},
		{"end extend", axialSh("/DeviceGray", "40 0 160 0", "/Extend[false true]", gray), clip, nil},
		{"domain", axialSh("/DeviceGray", "20 0 180 0", "/Domain[0.2 0.7]/Extend[true true]", gray), clip, nil},
		{"squared", axialSh("/DeviceGray", "20 0 180 0", "/Extend[true true]", expFn("0", "1", "2")), clip, nil},
		{"root", axialSh("/DeviceGray", "20 0 180 0", "/Extend[true true]", expFn("0", "1", "0.5")), clip, nil},
		{"defaults", axialSh("/DeviceGray", "20 0 180 0", "/Extend[true true]",
			"<</FunctionType 2/Domain[0 1]/N 1>>"), clip, nil},
		{"stitched", axialSh("/DeviceRGB", "20 0 180 0", "/Extend[true true]",
			"<</FunctionType 3/Domain[0 1]/Bounds[0.3]/Encode[0 1 1 0]/Functions["+
				expFn("1 0 0", "0 1 0", "1")+expFn("0 0 1", "1 1 0", "1")+"]>>"), clip, nil},
		{"per component", axialSh("/DeviceRGB", "20 0 180 0", "/Extend[true true]",
			"["+expFn("0", "1", "1")+expFn("1", "0", "1")+expFn("0.5", "0.5", "1")+"]"), clip, nil},
		{"diagonal", axialSh("/DeviceGray", "20 10 180 90", "/Extend[true true]", gray), clip, nil},
		{"reversed", axialSh("/DeviceGray", "180 0 20 0", "/Extend[true true]", gray), clip, nil},
		{"rotated", axialSh("/DeviceGray", "0 0 100 0", "/Extend[true true]", gray),
			"q 10 10 180 80 re W n 0.8 0.6 -0.6 0.8 60 20 cm /Sh0 sh Q", nil},
		{"stretched", axialSh("/DeviceGray", "0 0 1 0", "/Extend[true true]", gray),
			"q 10 10 180 80 re W n 150 0 0 0.5 25 50 cm /Sh0 sh Q", nil},
		{"alpha", axialSh("/DeviceRGB", "20 0 180 0", "/Extend[true true]",
			expFn("1 0 0", "0 0 1", "1")), "0 1 0 rg 0 0 200 50 re f q /GA gs 10 10 180 80 re W n /Sh0 sh Q", nil},
		// A CTM 10⁻³⁷ wide overflows the inverse's x term past the first column, and the axis is
		// vertical, so the projection is ∞×0: NaN, which pdfium's saturating cast makes step 0.
		{"nan", axialSh("/DeviceGray", "0 0 0 100", "", expFn("0.3", "1", "1")),
			strings.Repeat("0.0000001 0 0 1 0 0 cm ", 5) + "0.01 0 0 1 0 0 cm /Sh0 sh", nil},
		{"tiny axis", axialSh("/DeviceGray", "0 0 0.0000001 0", "/Extend[false true]", expFn("0.5", "0", "1")),
			"/Sh0 sh", nil},
		{"unclipped", axialSh("/DeviceGray", "20 0 180 0", "/Extend[true true]", gray), "/Sh0 sh", nil},
		{"curved clip", axialSh("/DeviceGray", "20 0 180 0", "/Extend[true true]", gray),
			"q 100 50 m 150 50 150 95 100 95 c 50 95 50 50 100 50 c W n /Sh0 sh Q", nil},
	} {
		for _, dpi := range []float64{72, 144, 200} {
			t.Run(fmt.Sprintf("%s/%g", c.name, dpi), func(t *testing.T) {
				path := shadePDF(t, 200, 100, "/Shading<</Sh0 5 0 R>>/ExtGState<</GA<</ca 0.5>>>>",
					c.stream, append([]string{c.sh}, c.extra...)...)
				mean, worst := compareRGB(t, path, dpi)
				wantMean, wantWorst := 0.0, 0.0
				if c.name == "curved clip" {
					wantMean, wantWorst = 0.07, 20
				}
				if mean > wantMean || worst > wantWorst {
					t.Errorf("mean %.4f, worst %.0f against pdfium; want at most %.2f and %.0f", mean, worst, wantMean, wantWorst)
				}
			})
		}
	}
}

// maskGroup is a luminosity soft mask's group: a form over bbox with a DeviceGray transparency
// group, the keys given, and content body.
func maskGroup(bbox, keys, body string) string {
	return formXO(bbox, "/Group<</S/Transparency/CS/DeviceGray>>"+keys, body)
}

// TestSoftMasksAgreeWithPdfium is the acceptance test for a luminosity soft mask.
//
// pdfium renders the group into a bitmap cleared to the backdrop colour, takes each pixel's
// luminosity as (30r + 59g + 11b)/100 in integers, and multiplies the object's alpha by it. The
// cases are the mask's own shapes — a gradient, a hard edge, a backdrop, colour — then each thing
// a mask can apply to, then the state rules: that q and Q stack it, /None ends it, and it stays in
// the space of the CTM in force at gs rather than of the object it masks.
//
// Every case agrees on every pixel except where a path's edge is sloped and antialiased: the hard
// edge by up to 6 levels at 200 dpi, the RGB backdrop's triangle by 3, the stroke by 1. That is
// the coverage of those edges, which this backend computes in float and AGG in its own cells, so
// those three alone are held to a bound. A hard edge that is a rectangle agrees on every pixel,
// because pdfium fills an axis-aligned rectangle in whole pixels inside the group as well.
func TestSoftMasksAgreeWithPdfium(t *testing.T) {
	ramp := axialSh("/DeviceGray", "0 0 200 0", "/Extend[true true]", expFn("0", "1", "1"))
	rampGroup := maskGroup("0 0 200 100", "/Resources<</Shading<</Sh0 5 0 R>>>>", "/Sh0 sh")
	const fill = "/GM gs 0 g 0 0 200 100 re f"
	for _, c := range []struct {
		name, smask, stream string
		extra               []string
	}{
		{"ramp", "/G 6 0 R", fill, []string{rampGroup}},
		{"hard edge", "/G 6 0 R", fill, []string{maskGroup("0 0 200 100", "", "1 g 40 20 m 140 20 l 90 70 l f")}},
		{"hard rectangle edge", "/G 6 0 R", fill, []string{maskGroup("0 0 200 100", "", "1 g 40.3 20.6 100.5 40.7 re f")}},
		{"group bbox", "/G 6 0 R", fill, []string{maskGroup("50 20 150 80",
			"/Resources<</Shading<</Sh0 5 0 R>>>>", "/Sh0 sh")}},
		{"backdrop", "/G 6 0 R/BC[0.6]", fill, []string{maskGroup("50 20 150 80", "", "0.2 g 0 0 200 100 re f")}},
		{"rgb backdrop", "/G 6 0 R/BC[1 0.5 0]", fill, []string{formXO("50 20 150 80",
			"/Group<</S/Transparency/CS/DeviceRGB>>", "0 0 1 rg 70 30 m 100 30 l 85 60 l f")}},
		{"half gray", "/G 6 0 R", fill, []string{maskGroup("0 0 200 100", "", "0.5 g 0 0 200 100 re f")}},
		{"colour", "/G 6 0 R", fill, []string{maskGroup("0 0 200 100", "",
			"1 0 0 rg 0 0 50 100 re f 0 1 0 rg 50 0 50 100 re f 0 0 1 rg 100 0 50 100 re f 1 1 0 rg 150 0 50 100 re f")}},
		{"red fill", "/G 6 0 R", "/GM gs 1 0 0 rg 0 0 200 100 re f", []string{rampGroup}},
		{"stroke", "/G 6 0 R", "/GM gs 0 0 1 RG 12 w 10 50 m 190 50 l S", []string{rampGroup}},
		{"shading", "/G 6 0 R", "/GM gs /Sh1 sh", []string{rampGroup,
			axialSh("/DeviceRGB", "0 0 0 100", "/Extend[true true]", expFn("1 0 0", "0 0 1", "1"))}},
		{"clipped", "/G 6 0 R", "/GM gs 30 10 140 80 re W n 0 g 0 0 200 100 re f", []string{rampGroup}},
		{"alpha", "/G 6 0 R", "/GM gs /GA gs 0 g 0 0 200 100 re f", []string{rampGroup}},
		{"restored by Q", "/G 6 0 R", "q /GM gs Q 0 g 0 0 200 100 re f", []string{rampGroup}},
		{"saved by q", "/G 6 0 R", "/GM gs q Q 0 g 0 0 200 100 re f", []string{rampGroup}},
		{"fill before gs", "/G 6 0 R", "1 0 0 rg 0 0 100 100 re f /GM gs 0 g 100 0 100 100 re f", []string{rampGroup}},
		{"ended by None", "/G 6 0 R", "/GM gs /GN gs 0 g 0 0 200 100 re f", []string{rampGroup}},
		{"CTM after gs", "/G 6 0 R", "/GM gs 0.5 0 0 1 0 0 cm 0 g 0 0 400 100 re f", []string{rampGroup}},
		{"CTM before gs", "/G 6 0 R", "0.5 0 0 1 50 0 cm /GM gs 0 g 0 0 200 100 re f", []string{rampGroup}},
		{"group matrix", "/G 6 0 R", fill, []string{maskGroup("0 0 200 100",
			"/Matrix[0.5 0 0 1 50 0]/Resources<</Shading<</Sh0 5 0 R>>>>", "/Sh0 sh")}},
		{"group state", "/G 6 0 R", "0.5 g 0.5 G /GA gs /GM gs 0 0 200 100 re f", []string{maskGroup("0 0 200 100", "",
			"0 0 100 100 re f 1 G 20 w 150 0 m 150 100 l S")}},
		{"group from the page's resources", "/G 6 0 R", "/Fm0 Do", []string{
			maskGroup("0 0 200 100", "", "/Sh0 sh"), "null",
			formXO("0 0 200 100", "/Resources<</ExtGState<</GM<</SMask<</S/Luminosity/G 6 0 R>>>>>>>>",
				"/GM gs 0 g 0 0 200 100 re f")}},
		{"path across gs", "/G 6 0 R", "0 g 0 0 40 100 re /GM gs 40 0 160 100 re f", []string{maskGroup("0 0 200 100", "",
			"0.5 g 0 0 200 100 re f")}},
	} {
		for _, dpi := range []float64{72, 144, 200} {
			t.Run(fmt.Sprintf("%s/%g", c.name, dpi), func(t *testing.T) {
				res := "/Shading<</Sh0 5 0 R/Sh1 7 0 R>>/ExtGState<</GM<</SMask<</S/Luminosity" + c.smask +
					">>>>/GA<</ca 0.5/CA 0.5>>/GN<</SMask/None>>>>/XObject<</Fm0 8 0 R>>"
				path := shadePDF(t, 200, 100, res, c.stream, append([]string{ramp}, c.extra...)...)
				mean, worst := compareRGB(t, path, dpi)
				wantMean, wantWorst := 0.0, 0.0
				switch c.name {
				case "hard edge", "rgb backdrop", "stroke":
					wantMean, wantWorst = 0.012, 6
				}
				if mean > wantMean || worst > wantWorst {
					t.Errorf("mean %.4f, worst %.0f against pdfium; want at most %.3f and %.0f", mean, worst, wantMean, wantWorst)
				}
			})
		}
	}
}

// TestASoftMaskedGlyphAgreesWithPdfium puts text under a soft mask, the one mark the shading and
// mask fixtures above cannot draw without a font.
//
// The bound is the glyphs', not the mask's. The same string unmasked differs from pdfium by up to
// 238 levels at 200 dpi, at the edges of the fixture font's outlines, and the mask only scales
// those edges; "A" alone under the mask agrees on every pixel at 72 dpi.
func TestASoftMaskedGlyphAgreesWithPdfium(t *testing.T) {
	withMask := func(o *textPDFOpts) {
		o.xobjects = "/ExtGState<</GM<</SMask<</S/Luminosity/G 8 0 R>>>>>>"
		o.extra = []string{maskGroup("0 0 200 200", "/Resources<</Shading<</Sh0 9 0 R>>>>", "/Sh0 sh"),
			axialSh("/DeviceGray", "0 0 200 0", "/Extend[true true]", expFn("0", "1", "1"))}
	}
	path := textPDF(t, "/GM gs BT /F1 60 Tf 10 80 Td (ABC) Tj ET", 200, withMask)
	for _, dpi := range []float64{72, 144, 200} {
		mean, worst := compareRGB(t, path, dpi)
		if mean > 0.04 || worst > 63 {
			t.Errorf("%g dpi: mean %.4f, worst %.0f against pdfium; want at most 0.04 and 63", dpi, mean, worst)
		}
	}
}

// TestUndrawableShadingsAreRefusedByReason is sh's refusal table. Each is a shading pdfium draws
// by a rule this backend does not implement, draws nothing for, or reads a malformed value in.
func TestUndrawableShadingsAreRefusedByReason(t *testing.T) {
	gray := expFn("0", "1", "1")
	for _, c := range []struct {
		name, sh, stream, want string
	}{
		{"not a resource", axialSh("/DeviceGray", "0 0 1 0", "", gray), "/Sh9 sh",
			"sh: /Sh9 is not in the page's /Shading resources"},
		{"not a dictionary", "42", "/Sh0 sh", "sh: /Sh0 is not a shading dictionary"},
		{"radial", "<</ShadingType 3/ColorSpace/DeviceGray/Coords[0 0 0 0 0 10]/Function" + gray + ">>", "/Sh0 sh",
			"sh: /Sh0 is shading type 3, and only axial shadings (type 2) are drawn"},
		{"no colour space", "<</ShadingType 2/Coords[0 0 1 0]/Function" + gray + ">>", "/Sh0 sh",
			"sh: /Sh0 has no /ColorSpace"},
		{"pattern space", axialSh("/Pattern", "0 0 1 0", "", gray), "/Sh0 sh", "sh: a Pattern colour"},
		{"cmyk", axialSh("/DeviceCMYK", "0 0 1 0", "", expFn("0 0.2 1 0", "1 0 0 0.3", "1")), "/Sh0 sh",
			"sh: /Sh0 is in DeviceCMYK, which pdfium converts by Adobe's table"},
		{"indexed space", axialSh("[/Indexed/DeviceGray 1<00ff>]", "0 0 1 0", "", gray), "/Sh0 sh",
			"sh: the colour space is /Indexed"},
		{"no coords", "<</ShadingType 2/ColorSpace/DeviceGray/Function" + gray + ">>", "/Sh0 sh",
			"sh: /Sh0 has no /Coords of four numbers"},
		{"short coords", axialSh("/DeviceGray", "0 0 1", "", gray), "/Sh0 sh",
			"sh: /Sh0 has no /Coords of four numbers"},
		{"zero axis", axialSh("/DeviceGray", "5 5 5 5", "", gray), "/Sh0 sh",
			"sh: /Sh0 has an axis of zero length"},
		{"huge axis", axialSh("/DeviceGray", "0 0 1e39 0", "", gray), "/Sh0 sh",
			"sh: /Sh0 has /Coords past the float range pdfium holds them in"},
		{"axis whose square is past float32", axialSh("/DeviceGray", "0 0 2e19 0", "", gray), "/Sh0 sh",
			"sh: /Sh0 has /Coords past the float range pdfium holds them in"},
		{"bad domain", axialSh("/DeviceGray", "0 0 1 0", "/Domain[0]", gray), "/Sh0 sh",
			"sh: /Sh0 has a /Domain that is not two numbers"},
		{"huge negative domain", axialSh("/DeviceGray", "0 0 1 0", "/Domain[-1e39 1]", gray), "/Sh0 sh",
			"sh: /Sh0 has a /Domain past the float range pdfium holds it in"},
		{"bad extend", axialSh("/DeviceGray", "0 0 1 0", "/Extend[true]", gray), "/Sh0 sh",
			"sh: /Sh0 has an /Extend that is not two booleans"},
		{"bbox", axialSh("/DeviceGray", "0 0 1 0", "/BBox[0 0 10 10]", gray), "/Sh0 sh",
			"sh: /Sh0 has a /BBox, which pdfium clips to in whole device pixels"},
		{"no function", "<</ShadingType 2/ColorSpace/DeviceGray/Coords[0 0 1 0]>>", "/Sh0 sh",
			"sh: /Sh0 has no /Function"},
		{"sampled function", axialSh("/DeviceGray", "0 0 1 0", "",
			"<</FunctionType 0/Domain[0 1]/Range[0 1]/Size[2]/BitsPerSample 8>>"),
			"/Sh0 sh", "sh: /Sh0's /Function: function: type 0 (sampled)"},
		{"too few outputs", axialSh("/DeviceRGB", "0 0 1 0", "", gray), "/Sh0 sh",
			"sh: /Sh0's /Function has 1 outputs for a colour space of 3 components"},
		{"wrong array", axialSh("/DeviceRGB", "0 0 1 0", "", "["+gray+gray+"]"), "/Sh0 sh",
			"sh: /Sh0's /Function is 2 functions for a colour space of 3 components"},
		{"array of multi-output", axialSh("/DeviceRGB", "0 0 1 0", "",
			"["+expFn("0 0", "1 1", "1")+gray+gray+"]"), "/Sh0 sh",
			"sh: /Sh0's /Function has a function of 2 outputs where each component needs its own"},
		{"degenerate CTM", axialSh("/DeviceGray", "0 0 1 0", "", gray), "0 0 0 0 0 0 cm /Sh0 sh",
			"sh: /Sh0 is drawn under a CTM that maps it to a line or a point"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := shadePDF(t, 100, 100, "/Shading<</Sh0 5 0 R>>", c.stream, c.sh)
			if got := refusal(t, path); !strings.Contains(got, c.want) {
				t.Errorf("refusal %q, want it to contain %q", got, c.want)
			}
		})
	}
}

// TestAPageOfMoreThanItsAreaPassesIsRefused holds maxPageAreas: each sh and each soft mask gs is
// a pass over the whole page, and the 65th is refused whichever operator makes it.
func TestAPageOfMoreThanItsAreaPassesIsRefused(t *testing.T) {
	sh := axialSh("/DeviceGray", "0 0 100 0", "/Extend[true true]", expFn("0", "1", "1"))
	group := maskGroup("0 0 100 100", "", "1 g 0 0 100 100 re f")
	for _, c := range []struct {
		name, stream, want string
	}{
		{"64 shadings", strings.Repeat("/Sh0 sh\n", 64), ""},
		{"65 shadings", strings.Repeat("/Sh0 sh\n", 65), "sh: the page makes more than 64 passes over its whole area"},
		{"64 masks", strings.Repeat("/GM gs\n", 64), ""},
		{"a mask after 64 shadings", strings.Repeat("/Sh0 sh\n", 64) + "/GM gs",
			"gs: the page makes more than 64 passes over its whole area"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := shadePDF(t, 100, 100, "/Shading<</Sh0 5 0 R>>/ExtGState<</GM<</SMask<</S/Luminosity/G 6 0 R>>>>>>",
				c.stream, sh, group)
			if c.want == "" {
				renderNative(t, path, 72)
				return
			}
			if got := refusal(t, path); !strings.Contains(got, c.want) {
				t.Errorf("refusal %q, want it to contain %q", got, c.want)
			}
		})
	}
}

// TestUndrawableSoftMasksAreRefusedByReason is the soft mask's refusal table.
func TestUndrawableSoftMasksAreRefusedByReason(t *testing.T) {
	group := maskGroup("0 0 100 100", "", "1 g 0 0 100 100 re f")
	img := hexImage(1, 1, "DeviceGray", "ff", "")
	for _, c := range []struct {
		name, smask, stream string
		extra               []string
		want                string
	}{
		{"alpha", "<</S/Alpha/G 5 0 R>>", "/GM gs", []string{group},
			"gs: /SMask /S is /Alpha, and only /Luminosity is implemented"},
		{"no subtype", "<</G 5 0 R>>", "/GM gs", []string{group},
			"gs: /SMask /S is missing, and only /Luminosity is implemented"},
		{"no group", "<</S/Luminosity>>", "/GM gs", nil, "gs: /SMask has no /G group"},
		{"group not a form", "<</S/Luminosity/G 5 0 R>>", "/GM gs", []string{"<</Type/Mask>>"},
			"gs: /SMask has no /G group"},
		{"group an image", "<</S/Luminosity/G 5 0 R>>", "/GM gs", []string{img}, "gs: /SMask has no /G group"},
		{"transfer", "<</S/Luminosity/G 5 0 R/TR 6 0 R>>", "/GM gs", []string{group, expFn("0", "1", "2")},
			"gs: /SMask /TR is a transfer function, which is not implemented"},
		{"unknown key", "<</S/Luminosity/G 5 0 R/Q 1>>", "/GM gs", []string{group},
			"gs: /SMask /Q is not a key this backend reads"},
		{"not a dictionary", "42", "/GM gs", nil, "gs: /SMask is neither /None nor a dictionary"},
		{"cmyk backdrop", "<</S/Luminosity/G 5 0 R/BC[0 0 0 1]>>", "/GM gs",
			[]string{formXO("0 0 100 100", "/Group<</S/Transparency/CS/DeviceCMYK>>", "")},
			"gs: /SMask /BC is in /DeviceCMYK, and only DeviceGray and DeviceRGB backdrops are implemented"},
		{"backdrop without a space", "<</S/Luminosity/G 5 0 R/BC[1]>>", "/GM gs",
			[]string{formXO("0 0 100 100", "", "")},
			"gs: /SMask /BC has no group colour space to be read in"},
		{"short backdrop", "<</S/Luminosity/G 5 0 R/BC[1 1]>>", "/GM gs",
			[]string{formXO("0 0 100 100", "/Group<</S/Transparency/CS/DeviceRGB>>", "")},
			"gs: /SMask /BC has 2 components for a space of 3"},
		{"transfer stream", "<</S/Luminosity/G 5 0 R/TR 6 0 R>>", "/GM gs",
			[]string{group, "<</FunctionType 4/Domain[0 1]/Range[0 1]/Length 5>>\nstream\n{ 1 }\nendstream"},
			"gs: /SMask /TR is a transfer function, which is not implemented"},
		{"long backdrop", "<</S/Luminosity/G 5 0 R/BC[1 1 1 1]>>", "/GM gs",
			[]string{formXO("0 0 100 100", "/Group<</S/Transparency/CS/DeviceRGB>>", "")},
			"gs: /SMask /BC has 4 components for a space of 3"},
		{"backdrop out of range", "<</S/Luminosity/G 5 0 R/BC[2]>>", "/GM gs", []string{group},
			"gs: /SMask /BC has a component outside 0..1"},
		{"negative backdrop", "<</S/Luminosity/G 5 0 R/BC[-0.5]>>", "/GM gs", []string{group},
			"gs: /SMask /BC has a component outside 0..1"},
		{"fill and stroke", "<</S/Luminosity/G 5 0 R>>", "/GM gs 0 0 100 100 re B", []string{group},
			"B: a path filled and stroked under a soft mask, which pdfium masks as one object"},
		{"image under a mask", "<</S/Luminosity/G 5 0 R>>", "/GM gs /Im0 Do", []string{group, img},
			"Do: an image drawn under a soft mask, /Im0"},
		{"form under a mask", "<</S/Luminosity/G 5 0 R>>", "/GM gs /Fm0 Do", []string{group, group},
			"Do: a form drawn under a soft mask, /Fm0"},
		{"image in the group", "<</S/Luminosity/G 5 0 R>>", "/GM gs",
			[]string{maskGroup("0 0 100 100", "/Resources<</XObject<</Im0 6 0 R>>>>", "/Im0 Do"), img},
			"Do: an image inside a soft mask's group, /Im0"},
		{"mask in the group", "<</S/Luminosity/G 5 0 R>>", "/GM gs",
			[]string{maskGroup("0 0 100 100", "/Resources<</ExtGState<</GM<</SMask<</S/Luminosity/G 5 0 R>>>>>>>>",
				"/GM gs")},
			"gs: a soft mask inside a soft mask's group"},
		{"group refused inside", "<</S/Luminosity/G 5 0 R>>", "/GM gs",
			[]string{maskGroup("0 0 100 100", "", "BI /W 1 /H 1 /CS /G /BPC 8 ID \xff EI")},
			"INLINE_IMAGE"},
		{"knockout group", "<</S/Luminosity/G 5 0 R>>", "/GM gs",
			[]string{formXO("0 0 100 100", "/Group<</S/Transparency/CS/DeviceGray/K true>>", "")},
			"gs: a knockout transparency group, /SMask /G"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := "/ExtGState<</GM<</SMask " + c.smask + ">>>>/XObject<</Im0 6 0 R/Fm0 6 0 R>>"
			path := shadePDF(t, 100, 100, res, c.stream, c.extra...)
			if got := refusal(t, path); !strings.Contains(got, c.want) {
				t.Errorf("refusal %q, want it to contain %q", got, c.want)
			}
		})
	}
}

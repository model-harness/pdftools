package native

import (
	"fmt"
	"testing"
)

// pdfiumCase is a stream drawn on a 200×100 page and the most it may differ from pdfium by.
type pdfiumCase struct {
	name, stream string
	mean, worst  float64 // 0 is every pixel equal
}

// againstPdfium draws each case at 72, 144 and 200 dpi, under the fill and stroke colours col sets,
// and holds it to its bounds.
func againstPdfium(t *testing.T, res, col string, cases []pdfiumCase) {
	t.Helper()
	for _, c := range cases {
		for _, dpi := range []float64{72, 144, 200} {
			t.Run(fmt.Sprintf("%s/%g", c.name, dpi), func(t *testing.T) {
				mean, worst := compareRGB(t, shadePDF(t, 200, 100, res, col+c.stream), dpi)
				if mean > c.mean || worst > c.worst {
					t.Errorf("mean %.4f, worst %.0f against pdfium; want at most %g and %g", mean, worst, c.mean, c.worst)
				}
			})
		}
	}
}

// TestAFillWithNoAreaIsAHairline pins CFX_RenderDevice::DrawPath's rules for a filled subpath with
// no area, which §8.5.3.3 paints nothing of and pdfium strokes a device pixel wide in the fill
// colour. A path of two points is a cosmetic line. Otherwise each subpath is asked GetZeroAreaPath's
// three questions — a line there and back, a palindrome, a line that folds back along itself — in
// user space, under a cm too, and each segment a subpath gives is stroked with the others as one
// path, where segments that cross count twice (ruleSum). A thin one is drawn at a quarter of the
// fill's alpha; a figure with area beside one is filled as well.
func TestAFillWithNoAreaIsAHairline(t *testing.T) {
	const res = "/ExtGState<</GA<</ca 0.5>>>>"
	againstPdfium(t, res, "0.2 0.4 0.6 rg 0.9 0.1 0.1 RG 3 w ", []pdfiumCase{
		{"m l, aslant", "20.3 15.2 m 170.6 80.4 l f", 0.02, 3},
		{"m l, across", "20.3 50.4 m 170.6 50.4 l f", 0.071, 7},
		{"m l under an alpha", "/GA gs 20.3 15.2 m 170.6 80.4 l f", 0.007, 2},
		{"m l going nowhere", "50.3 50.4 m 50.3 50.4 l f", 0.001, 6},
		{"m l h", "20.3 15.2 m 170.6 80.4 l h f", 0.006, 1},
		{"re with no width or height", "50.3 50.4 0 0 re f", 0.001, 2},
		{"two lines that cross", "20.3 15.2 m 170.6 80.4 l 20.3 80.2 m 170.6 15.4 l f", 0.01, 2},
		{"a palindrome", "20.3 15.2 m 100.6 80.4 l 170.2 30.3 l 100.6 80.4 l 20.3 15.2 l f", 0.007, 1},
		{"a fold down", "50.3 15.2 m 50.3 80.4 l 50.3 40.6 l f", 0.004, 6},
		{"a fold across", "20.3 50.4 m 170.6 50.4 l 90.2 50.4 l f", 0.038, 7},
		{"a fold aslant", "20 20 m 120 70 l 70 45 l f", 0.006, 3},
		{"a fold in a longer figure", "20.3 15.2 m 170.6 15.2 l 170.6 80.4 l 170.6 50.2 l f", 0.024, 5},
		{"beside a triangle", "20.3 15.2 m 170.6 30.4 l 90.2 85.7 l h 20.3 80.2 m 170.6 70.4 l f", 0.017, 3},
		{"under a cm", "1.5 0.3 -0.2 1.1 10.3 5.1 cm 20.3 15.2 m 100.6 60.4 l f", 0.014, 3},
		{"off the page", "-20.3 -15.2 m 170.6 80.4 l f", 0.021, 3},
		{"m m l", "20.3 15.2 m 60 60 m 170.6 80.4 l f", 0.011, 3},
		{"m l m", "20.3 15.2 m 170.6 80.4 l 60 60 m f", 0.02, 3},
		{"l l with no m", "20.3 15.2 l 170.6 80.4 l f", 0, 0},
		{"m l going nowhere beside a triangle", "20.3 15.2 m 170.6 30.4 l 90.2 85.7 l h 185.3 50.4 m 185.3 50.4 l f", 0.014, 3},
		{"a fold back to the middle", "20.3 15.2 m 100.6 80.4 l 170.2 30.3 l 100.6 80.4 l f", 0.003, 1},
		{"a fold at the end of a curve", "20.3 50.4 m 60 50.4 100 50.4 170.6 50.4 c 90.2 50.4 l f", 0, 0},
		{"a palindrome with a curve", "20.3 15.2 m 100.6 80.4 170.2 30.3 100.6 80.4 c 20.3 15.2 l f", 0.049, 34},
		{"m l h under an alpha", "/GA gs 20.3 15.2 m 170.6 80.4 l h f", 0.005, 1},
	})
}

// TestFillAndStrokeKnockOut pins DrawFillStrokePath, which pdfium takes for B, B*, b and b* when the
// stroke is translucent: the fill and then the stroke are drawn into a layer of their own, in bytes,
// where the stroke replaces the fill under it rather than being composited over it — §11.7.4.4's
// knockout group — and the layer is then composited through the clip. pdfium does not take
// §11.7.4.4's exception for overprint. A stroke with an alpha byte of 0 is not stroked at all, and a
// fill with one is not filled; nor does an S take the layer, whatever its alpha.
//
// The worst differences are at the corners, where this backend's joins differ from AGG's: by 41
// levels at 144 dpi on the triangle under an opaque stroke, which DrawFillStrokePath does not take,
// by 28 under one at half alpha, and by 45 at the star's points. A translucent fill differs by a
// level inside, because this backend composites it in float and pdfium in bytes.
func TestFillAndStrokeKnockOut(t *testing.T) {
	const res = "/ExtGState<</GA<</ca 0.5>>/GF<</ca 0.0039>>/GH<</CA 0.5>>/GS<</CA 0.0039>>>>"
	const tri = "20.3 15.2 m 170.6 30.4 l 90.2 85.7 l "
	againstPdfium(t, res, "0.2 0.4 0.6 rg 0.9 0.1 0.1 RG 3 w ", []pdfiumCase{
		{"B", "/GH gs " + tri + "h B", 0.014, 28},
		{"B*", "/GH gs " + tri + "h B*", 0.014, 28},
		{"b", "/GH gs " + tri + "b", 0.014, 28},
		{"b*", "/GH gs " + tri + "b*", 0.014, 28},
		{"an opaque stroke", tri + "h B", 0.025, 41},
		{"a translucent fill", "/GA gs /GH gs " + tri + "h B", 0.01, 20},
		{"a stroke with an alpha byte of 0", "/GS gs " + tri + "h B", 0.011, 3},
		{"on a line", "/GH gs 20.3 15.2 m 170.6 80.4 l B", 0.007, 2},
		{"b* on a line", "/GH gs 20.3 15.2 m 170.6 80.4 l b*", 0.007, 2},
		{"a rectangle clip", "20.3 20.3 100 50 re W n /GH gs " + tri + "h B", 0.007, 3},
		{"a sloped clip", "20 20 m 180 30 l 100 90 l h W n /GH gs 10.4 10.6 m 160 12 l 120 90 l h B", 0.01, 3},
		{"m b* under a stroke with an alpha byte of 0", "/GS gs 100.3 50.4 m b*", 0.001, 6},
		{"a fold under a stroke with an alpha byte of 0", "/GS gs 20.3 50.4 m 170.6 50.4 l 90.2 50.4 l B", 0, 0},
		{"S", "/GH gs " + tri + "h S", 0.018, 26},
		{"B after a smaller B", "/GH gs 175.3 80.4 m 195.2 80.4 l 185.1 95.6 l h B " + tri + "h B", 0.02, 28},
		{"B after a larger B", "/GH gs " + tri + "h B 175.3 80.4 m 195.2 80.4 l 185.1 95.6 l h B", 0.02, 28},
		{"B* on a star", "/GH gs 100 95 m 130 10 l 55 65 l 145 65 l 70 10 l h B*", 0.03, 45},
		{"B on a star", "/GH gs 100 95 m 130 10 l 55 65 l 145 65 l 70 10 l h B", 0.031, 45},
		{"a rectangle", "/GH gs 30.3 20.4 140 60 re B", 0.076, 22},
		{"a rectangle under an opaque stroke and a translucent fill", "/GA gs 30.3 20.4 140 60 re B", 0.25, 33},
		{"a rectangle under a fill with an alpha byte of 0", "/GF gs /GH gs 30.3 20.4 140 60 re B", 0.075, 22},
	})
}

// TestAStrokeThatGoesNowhereIsAPixelLong pins BuildAggPath's rule for a line from an open move to
// the point it started at, with nothing after it but the end or another move: it goes a device
// pixel to the right, so pdfium strokes it as a line a pixel long under every cap, where §8.5.3.2
// paints a dot under round caps and nothing under the others. A move that h closes is such a line
// under round caps (AddPathObject); a subpath with two such lines, or a curve that goes nowhere,
// AGG does not draw. Dashed, the line is dashed like any other. The pixel is along the CTM's first
// row, so a mirrored line goes a pixel to the left and a quarter-turned one a pixel up the page.
//
// The worst differences are round caps', which differ from AGG's by as much on any line.
func TestAStrokeThatGoesNowhereIsAPixelLong(t *testing.T) {
	const at = "100.3 50.4 m 100.3 50.4 l "
	againstPdfium(t, "", "0.9 0.1 0.1 RG 10 w ", []pdfiumCase{
		{"round", "1 J " + at + "S", 0.01, 25},
		{"butt", at + "S", 0.001, 6},
		{"square", "2 J " + at + "S", 0.006, 7},
		{"m h", "1 J 100.3 50.4 m h S", 0.01, 25},
		{"m h under butt caps", "100.3 50.4 m h S", 0, 0},
		{"m s", "1 J 100.3 50.4 m s", 0.01, 25},
		{"m l h", "1 J " + at + "h S", 0.01, 25},
		{"m l h under square caps", "2 J " + at + "h S", 0.006, 7},
		{"two l", "1 J " + at + "100.3 50.4 l S", 0, 0},
		{"a curve", "1 J 100.3 50.4 m 100.3 50.4 100.3 50.4 100.3 50.4 c S", 0, 0},
		{"a lone m", "1 J 100.3 50.4 m S", 0, 0},
		{"m b* under butt caps", "100.3 50.4 m b*", 0.001, 6},
		{"after another", "20 20.5 m 60 20.5 l " + at + "S", 0.006, 6},
		{"before another", at + "20 20.5 m 60 20.5 l S", 0.006, 6},
		{"between two l", "20 20.5 m 60 20.5 l " + at + "100.3 50.4 l S", 0.006, 6},
		{"under a cm", "2 0 0 2 0 0 cm 1 J 5 w 50.3 25.2 m 50.3 25.2 l S", 0.01, 25},
		{"mirrored", "-1 0 0 1 200 0 cm " + at + "S", 0.001, 6},
		{"quarter-turned", "0 1 -1 0 150 0 cm 50.3 50.4 m 50.3 50.4 l S", 0.002, 7},
		{"turned an eighth", "0.7071 0.7071 -0.7071 0.7071 100 0 cm 50.3 20.4 m 50.3 20.4 l S", 0.001, 2},
		{"m h mirrored", "-1 0 0 1 200 0 cm 1 J 100.3 50.4 m h S", 0.01, 25},
		{"dashed and mirrored", "-1 0 0 1 200 0 cm [3 3] 0 d " + at + "S", 0.001, 6},
		{"dashed", "[3 3] 0 d 1 J " + at + "S", 0.01, 25},
		{"dashed under butt caps", "[3 3] 0 d " + at + "S", 0.001, 6},
		{"dashed under square caps", "[3 3] 0 d 2 J " + at + "S", 0.006, 7},
		{"dashed from a gap", "[3 3] 4 d 1 J " + at + "S", 0, 0},
	})
}

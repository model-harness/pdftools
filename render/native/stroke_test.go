package native

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/model-harness/pdftools/geom"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
)

// TestStrokesAgreeWithPdfium is the stroke's acceptance test, on shapes chosen for what separates
// them: each cap and join style on its own, a miter over its limit, a subpath closed by h and by s,
// a curve, a pen made elliptical by the CTM, the fill-and-stroke operators, lines below a pixel, and
// outlines that overlap.
//
// At 72 and 144 dpi, where a 200pt page is a whole number of pixels, and at 200, where it is 555.56
// and both backends map it onto 556. That third one is TestThePageFillsTheImageAtEveryScale's case:
// it put hundreds of each shape's edge pixels over 32 until the page scale was pdfium's.
//
// Measured, with the bounds set from it: every mean is at most 0.031 of 255 and no pixel differs by
// more than 32, except a curve's flattening, which puts the mean near 0.1 and a few edge pixels past
// 32 on the inside of a tight bend (worst 37 at 144 dpi and 36 at 200, as for the filled curves in
// TestFillsAgreeWithPdfium); and the CMYK stroke, whose mean is 0.446 because pdfium does not
// convert CMYK by §8.6.4.4's naive formula. Elsewhere no pixel differs by more than 8. A wrong cap,
// join, width or pen shape moves the mean by whole units and puts hundreds of pixels over 32.
func TestStrokesAgreeWithPdfium(t *testing.T) {
	for _, c := range []struct {
		name   string
		stream string
		mean   float64
		over   int
	}{
		{"butt cap", "0 G 10 w 40 100 m 160 100 l S", 0.03, 0},
		{"round cap", "0 G 10 w 1 J 40 100 m 160 100 l S", 0.03, 0},
		{"square cap", "0 G 10 w 2 J 40 100 m 160 100 l S", 0.03, 0},
		{"miter join", "0 G 10 w 40 40 m 100 160 l 160 40 l S", 0.03, 0},
		{"round join", "0 G 10 w 1 j 40 40 m 100 160 l 160 40 l S", 0.03, 0},
		// Three joins whose arcs between them go all the way round, so one of them crosses the angle
		// where atan2 wraps from π to -π.
		{"round joins all the way round", "0 G 12 w 1 j 40 40 m 160 50 l 90 160 l h S", 0.03, 0},
		{"bevel join", "0 G 10 w 2 j 40 40 m 100 160 l 160 40 l S", 0.03, 0},
		// A turn of 15° under a pen 100 wide puts the miter inside it within innerMiter of the radius,
		// 6.6 back along each segment, which is past the end of one 2 long. pdfium draws that miter;
		// the two segments' ends there differed from it by 255.
		{"a short segment after a gentle turn", "0 G 100 w 20 100 m 100 100 l 101.9319 100.5176 l S", 0.03, 0},
		// A path that goes straight on has offset edges that never meet, and the join is the segments'
		// ends. Pen space's origin is the page's top left, so a join there tells the two apart from a
		// meeting point read where there is none.
		{"going straight on at the page's top left", "0 G 20 w 0 220 m 0 200 l 0 180 l S", 0.03, 0},
		// TestANearlyStraightRoundJoinHasNoArc's page.
		{"a round join on three points nearly in a line", "0 G 10 w 1 j 0.1 20.3 m 1.0 22.1 l 1.3 22.7 l S", 0.03, 0},
		{"miter over its limit", "0 G 10 w 2 M 40 40 m 100 160 l 160 40 l S", 0.03, 0},
		{"closed with h", "0 G 8 w 40 40 m 160 40 l 100 160 l h S", 0.03, 0},
		{"s closes", "0 G 8 w 40 40 m 160 40 l 100 160 l s", 0.03, 0},
		// After h the current point is the subpath's start, so what follows begins there.
		{"a segment after h", "0 G 4 w 40 40 m 160 40 l 100 160 l h 160 160 l S", 0.03, 0},
		// At 200 dpi, 4 inside the curve's tightest bend, as for "a curve".
		{"a curve after h", "0 G 4 w 40 40 m 160 40 l 100 160 l h 40 160 40 160 160 160 c S", 0.12, 4},
		{"a segment with no current point", "0 G 10 w 40 150 m 160 150 l S 100 100 l 150 150 l S", 0.03, 0},
		{"a curve", "0 G 6 w 30 30 m 60 190 140 190 170 30 c S", 0.12, 6},
		{"non-uniform ctm", "q 3 0 0 1 0 0 cm 0 G 4 w 1 J 20 40 m 50 160 l S Q", 0.03, 0},
		{"rotated non-uniform ctm", "q 2 1 -0.5 1 100 20 cm 0 G 4 w 0 0 m 0 60 l 40 60 l S Q", 0.03, 0},
		{"B fills then strokes", "0 0 1 rg 1 0 0 RG 8 w 50 50 100 100 re B", 0.03, 0},
		{"b* closes and fills even-odd",
			"0 0 1 rg 0 1 0 RG 3 w 150 150 m 168 105 l 123 133 l 177 133 l 132 105 l b*", 0.03, 0},
		{"cmyk stroke", "0 1 1 0 K 6 w 30 30 m 170 170 l S", 0.46, 0},
		{"lines below a pixel", "0 G 0.25 w 20 50 m 180 50 l S 0.5 w 20 100 m 180 100 l S 1 w 20 150 m 180 150 l S", 0.03, 0},
		{"a zero-width line", "0 G 0 w 20 50 m 180 150 l S", 0.03, 0},
		// Where two outlines overlap, AGG sums their coverage before it clamps it, so an edge pixel
		// both cover is the sum of the two. Covered once, two subpaths along one line differed by 32
		// at 200 dpi, and a closed path's two outlines where it crosses itself by 35 at 144.
		{"two subpaths along one line", "0 G 10 w 20 100 m 180 100 l 60 100 m 140 100 l S", 0.03, 0},
		// Its mean is 0.031 at 72 dpi from the edge pixels of two long diagonals, none more than 2 off.
		{"two lines that cross", "0 G 10 w 20 20 m 180 180 l 20 180 m 180 20 l S", 0.032, 0},
		{"a closed path that crosses itself", "0 G 6 w 100 180 m 150 20 l 30 120 l 170 120 l 50 20 l h S", 0.03, 0},
		// The side inside a sharp turn folds back over itself, and the fold counts twice.
		{"a sharp turn", "0 G 10 w 20 40 m 180 60 l 20 80 l S", 0.03, 0},
		{"a tight curve", "0 G 12 w 30 100 m 100 200 100 0 170 100 c S", 0.09, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, dpi := range []float64{72, 144, 200} {
				mean, worst, over := compare(t, c.stream, 200, dpi)
				t.Logf("%g dpi: mean %.3f worst %.0f over-32 %d", dpi, mean, worst, over)
				if mean > c.mean {
					t.Errorf("%g dpi: mean ink difference %.3f, want <= %.2f", dpi, mean, c.mean)
				}
				if over > c.over {
					t.Errorf("%g dpi: %d pixels differ by more than 32 (worst %.0f), want at most %d",
						dpi, over, worst, c.over)
				}
			}
		})
	}
}

// strokeAt72 renders a 200pt page at 72 dpi, so a device pixel is a point and device y is 200
// minus user y.
func strokeAt72(t *testing.T, stream string) *render.Raster {
	t.Helper()
	ra, err := renderAt72(t, onePagePDF(t, stream, 200, 200))
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	return ra
}

const (
	black0 = 0
	paper  = 255
)

func grey(x, y, v int) pixel { return pixel{x, y, v, v, v} }

// TestStrokeGeometryIsExact pins the constants the comparison can only bound: how far a stroke
// reaches from its path, how far each cap extends past an end, and where each join's outer edge
// lies. Each sample is a pixel wholly on one side of the edge it tests, so a correct stroke gives
// exactly ink or paper there and a wrong constant flips it.
func TestStrokeGeometryIsExact(t *testing.T) {
	// A 10pt line along device y 100: it covers rows 95 through 104 and nothing else.
	const line = "0 G 10 w %s 40 100 m 160 100 l S"
	// A join whose segments meet at 60°, apex at device (100.5, 100). The miter reaches half the
	// width over sin 30° = 10 above the apex, to y 90; a round join reaches 5, to 95; a bevel
	// reaches half the width times sin 30° = 2.5, to 97.5.
	const apex = "0 G 10 w %s 65.859 40 m 100.5 100 l 135.141 40 l S"

	for _, c := range []struct {
		name, stream string
		want         []pixel
	}{
		{"the width is the operand", fmt.Sprintf(line, ""), []pixel{
			grey(100, 94, paper), grey(100, 95, black0), grey(100, 104, black0), grey(100, 105, paper)}},
		{"a butt cap ends at the endpoint", fmt.Sprintf(line, "0 J"), []pixel{
			grey(39, 99, paper), grey(40, 99, black0), grey(159, 99, black0), grey(160, 99, paper)}},
		{"a square cap extends half the width", fmt.Sprintf(line, "2 J"), []pixel{
			grey(34, 99, paper), grey(35, 99, black0), grey(35, 95, black0), grey(164, 104, black0), grey(165, 99, paper)}},
		// The cap's corner is what tells round from square: (35, 95) is 5.7 from the endpoint at its
		// nearest.
		{"a round cap is a half disc", fmt.Sprintf(line, "1 J"), []pixel{
			grey(36, 99, black0), grey(35, 95, paper), grey(163, 99, black0), grey(165, 99, paper)}},
		{"a miter meets where the edges do", fmt.Sprintf(apex, "0 j"), []pixel{
			grey(100, 89, paper), grey(100, 91, black0)}},
		{"a miter within its limit is kept", fmt.Sprintf(apex, "0 j 2.001 M"), []pixel{
			grey(100, 91, black0)}},
		{"a miter over its limit is bevelled", fmt.Sprintf(apex, "0 j 1.999 M"), []pixel{
			grey(100, 96, paper), grey(100, 98, black0)}},
		{"a round join reaches the radius", fmt.Sprintf(apex, "1 j"), []pixel{
			grey(100, 94, paper), grey(100, 96, black0)}},
		{"a bevel cuts across", fmt.Sprintf(apex, "2 j"), []pixel{
			grey(100, 96, paper), grey(100, 98, black0)}},
		// A turn of 60°, under a right angle, still has an outside to fill: (109, 116) lies between
		// the bevel's chord, 17.3 from the vertex, and the arc at 20.
		{"a gentle turn is joined too", "0 G 40 w 1 j 40 100 m 100 100 l 130 151.96 l S", []pixel{
			grey(109, 116, black0)}},
		{"a gentle turn's bevel is short of it", "0 G 40 w 2 j 40 100 m 100 100 l 130 151.96 l S", []pixel{
			grey(109, 116, paper)}},
		// A 4pt line under a 3× horizontal scale is 12 device pixels wide and a 4pt line along
		// it is 4 high: the pen is an ellipse, not a circle of either radius.
		{"the pen is transformed", "q 3 0 0 1 0 0 cm 0 G 4 w 20 20 m 20 100 l 30 150 m 60 150 l S Q",
			[]pixel{grey(53, 140, paper), grey(54, 140, black0), grey(65, 140, black0), grey(66, 140, paper),
				grey(120, 47, paper), grey(120, 48, black0), grey(120, 51, black0), grey(120, 52, paper)}},
		// A quarter-point line is floored to one device pixel, centred on the path.
		{"no line is thinner than a pixel", "0 G 0.25 w 40 100.5 m 160 100.5 l S", []pixel{
			grey(100, 98, paper), grey(100, 99, black0), grey(100, 100, paper)}},
		{"a zero width is a pixel too", "0 G 0 w 40 100.5 m 160 100.5 l S", []pixel{
			grey(100, 99, black0), grey(100, 100, paper)}},
		// A subpath that goes nowhere is a line a device pixel long, to the right, as pdfium's
		// BuildAggPath makes it, where §8.5.3.2 would paint a dot under round caps and nothing under
		// the others: here from x 100.5 to 101.5, so a round cap reaches 106.5 and a butt-capped
		// line half covers the pixels at 100 and 101. A lone m is nothing.
		{"a zero-length segment is a pixel long under round caps", "0 G 10 w 1 J 100.5 100.5 m 100.5 100.5 l S",
			[]pixel{grey(100, 99, black0), grey(105, 99, black0), grey(107, 99, paper), grey(94, 99, paper)}},
		{"a closed single point is a pixel long", "0 G 10 w 1 J 100.5 100.5 m h S", []pixel{
			grey(100, 99, black0), grey(105, 99, black0), grey(107, 99, paper)}},
		{"a zero-length segment is a pixel long under butt caps", "0 G 10 w 0 J 100.5 100.5 m 100.5 100.5 l S",
			[]pixel{grey(99, 99, paper), grey(100, 99, 128), grey(101, 99, 128), grey(100, 95, 128), grey(102, 99, paper)}},
		{"a lone m is nothing", "0 G 10 w 1 J 100.5 100.5 m S", []pixel{grey(100, 99, paper)}},
		// A closed subpath has a join where it meets itself and no caps: the miter at the start
		// corner of a square fills the corner pixel.
		{"a closed subpath is joined at its start", "0 G 10 w 50 50 100 100 re S", []pixel{
			grey(45, 45, black0), grey(44, 44, paper)}},
		// After h the current point is the start (§8.5.2.1), and a segment from it is drawn there.
		{"a segment after h starts at the start", "0 G 4 w 40 40 m 160 40 l 100 160 l h 160 160 l S",
			[]pixel{grey(130, 69, black0)}},
		// A segment needs a current point, and a paint operator leaves none.
		{"a segment with no current point is nothing", "0 G 10 w 40 150 m 160 150 l S 100 100 l 150 150 l S",
			[]pixel{grey(30, 30, paper), grey(125, 75, paper)}},
		// Only the stream's h closes a subpath for the stroke: the first of two open ones is not
		// closed by the m that ends it, though a fill would close it.
		{"an m does not close the subpath it ends", "0 G 10 w 40 40 m 160 40 l 160 160 l 40 160 m 40 170 l S",
			[]pixel{grey(99, 100, paper)}},
		// A subpath that returns to its start before h has no segment from its start to itself.
		{"a return to the start before h", "0 G 10 w 50 50 m 150 50 l 150 150 l 50 150 l 50 50 l h S", []pixel{
			grey(45, 45, black0), grey(44, 44, paper), grey(100, 100, paper)}},
		// A reversal turns through 180°, and a round join is the half disc beyond the vertex.
		{"a reversal is joined", "0 G 10 w 1 j 40 100 m 160 100 l 100 100 l S", []pixel{
			grey(163, 99, black0), grey(165, 99, paper)}},
		{"an open subpath is capped at its start", "0 G 10 w 50 150 m 150 150 l 150 50 l 50 50 l 50 150 l S",
			[]pixel{grey(46, 46, paper), grey(46, 54, black0)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkPixels(t, strokeAt72(t, c.stream), c.want, 2)
		})
	}
}

// TestEveryStrokingOperatorDraws is the six painting operators that stroke, told apart by the
// three things that differ between them: whether the path is closed first, whether it is filled
// first, and by which rule. A 10pt stroke of an open right angle has its closing diagonal stroked
// only by s, b and b*, and its interior filled only by the B and b forms.
func TestEveryStrokingOperatorDraws(t *testing.T) {
	const tri = "0 0 1 rg 1 0 0 RG 10 w 40 40 m 160 40 l 160 160 l "
	red, blue, white := pixel{r: 255}, pixel{b: 255}, pixel{r: 255, g: 255, b: 255}
	at := func(x, y int, p pixel) pixel { p.x, p.y = x, y; return p }
	for _, c := range []struct {
		op               string
		closed, filled   bool
		evenOdd, strokes bool
	}{
		{op: "S"}, {op: "s", closed: true}, {op: "B", filled: true}, {op: "B*", filled: true, evenOdd: true},
		{op: "b", closed: true, filled: true}, {op: "b*", closed: true, filled: true, evenOdd: true},
	} {
		t.Run(c.op, func(t *testing.T) {
			diagonal, interior := white, white
			if c.closed {
				diagonal = red
			}
			if c.filled {
				interior = blue
			}
			checkPixels(t, strokeAt72(t, tri+c.op), []pixel{
				at(100, 159, red), at(95, 101, diagonal), at(140, 140, interior)}, 2)

			// A star drawn as one subpath fills its centre under nonzero and hollows it under
			// even-odd, which is the only way to see which rule the fill ran.
			if !c.filled {
				return
			}
			centre := blue
			if c.evenOdd {
				centre = white
			}
			star := "0 0 1 rg 1 0 0 RG 1 w 150 150 m 168 105 l 123 133 l 177 133 l 132 105 l " + c.op
			checkPixels(t, strokeAt72(t, star), []pixel{at(150, 76, centre)}, 2)
		})
	}
}

// TestStrokeStateIsRestoredByQ is one case per parameter, because each is its own field on save's
// struct and each could be forgotten alone: a q…Q that sets it must leave the stroke after the Q as
// though it had not.
func TestStrokeStateIsRestoredByQ(t *testing.T) {
	const line = " 40 100 m 160 100 l S"
	const apex = " 65.859 40 m 100.5 100 l 135.141 40 l S"
	for _, c := range []struct {
		name, stream string
		want         []pixel
	}{
		{"colour", "q 1 0 0 RG Q 10 w" + line, []pixel{grey(100, 99, black0)}},
		{"colour space", "q /CS0 CS Q 10 w" + line, []pixel{grey(100, 99, black0)}},
		{"width", "q 20 w Q" + line, []pixel{grey(100, 97, paper), grey(100, 102, paper)}},
		{"cap", "10 w q 2 J Q" + line, []pixel{grey(37, 99, paper)}},
		{"join", "10 w q 2 j Q" + apex, []pixel{grey(100, 91, black0)}},
		{"miter limit", "10 w q 1.5 M Q" + apex, []pixel{grey(100, 91, black0)}},
		{"dash", "q [2 2] 0 d Q 10 w" + line, []pixel{grey(100, 99, black0)}},
		// Not q and Q, but the same bug by another route: the survey runs the whole stream first,
		// and the paint pass must start from the initial state and not from where the survey ended.
		{"the survey's state does not reach the paint pass", "10 w" + line + " 1 0 0 RG 2 J", []pixel{
			grey(100, 99, black0), grey(37, 99, paper)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkPixels(t, strokeAt72(t, c.stream), c.want, 2)
		})
	}
}

// TestStrokeAlphaIsCAAndFillAlphaIsCa pins which constant alpha each half of a B uses. They are two
// parameters (§11.6.4.4), and a backend with one would get exactly one of these two pages right.
func TestStrokeAlphaIsCAAndFillAlphaIsCa(t *testing.T) {
	const b = "/GS0 gs 0 0 1 rg 1 0 0 RG 10 w 50 50 100 100 re B"
	for _, c := range []struct {
		extg string
		want []pixel
	}{
		// Inside the fill, and outside it on the stroke. On the fill the stroke knocks it out
		// (§11.7.4.4), so the fill does not show through: the stroke's half is the same either side.
		{"<</CA 0.5>>", []pixel{{100, 100, 0, 0, 255}, {46, 100, 255, 128, 128}, {52, 100, 255, 128, 128}}},
		{"<</ca 0.5>>", []pixel{{100, 100, 128, 128, 255}, {46, 100, 255, 0, 0}, {52, 100, 255, 0, 0}}},
	} {
		t.Run(c.extg, func(t *testing.T) {
			ra, err := renderAt72(t, gsPDF(t, c.extg, b))
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			checkPixels(t, ra, c.want, 2)
		})
	}
}

// TestExtGStateSetsTheStrokeGeometry is /LW, /LC, /LJ and /ML, each of which the ExtGState test
// accepts as a key and only a stroke can show was applied.
func TestExtGStateSetsTheStrokeGeometry(t *testing.T) {
	const line = " 40 100 m 160 100 l S"
	const apex = " 65.859 40 m 100.5 100 l 135.141 40 l S"
	for _, c := range []struct {
		name, extg, stream string
		want               []pixel
	}{
		{"LW", "<</LW 10>>", "/GS0 gs" + line, []pixel{grey(100, 95, black0), grey(100, 94, paper)}},
		// The width gs sets is the machine's, so Q restores it as it restores a w.
		{"LW is restored by Q", "<</LW 10>>", "q /GS0 gs Q" + line, []pixel{grey(100, 97, paper)}},
		{"CA is restored by Q", "<</CA 0.5>>", "q /GS0 gs Q 10 w" + line, []pixel{grey(100, 99, black0)}},
		{"CA does not reach the paint pass", "<</CA 0.5>>", "10 w" + line + " /GS0 gs", []pixel{grey(100, 99, black0)}},
		{"LC", "<</LC 2>>", "10 w /GS0 gs" + line, []pixel{grey(35, 99, black0)}},
		{"LJ", "<</LJ 2>>", "10 w /GS0 gs" + apex, []pixel{grey(100, 96, paper)}},
		{"ML", "<</ML 1.999>>", "10 w /GS0 gs" + apex, []pixel{grey(100, 96, paper)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ra, err := renderAt72(t, gsPDF(t, c.extg, c.stream))
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			checkPixels(t, ra, c.want, 2)
		})
	}
}

// TestStrokeColourIsEachDeviceSpace is the stroke colour operators, including the two that go
// through a colour space: CS resets the colour to the space's initial black, and SC and SCN read as
// many components as the space has.
func TestStrokeColourIsEachDeviceSpace(t *testing.T) {
	const line = " 10 w 40 100.5 m 160 100.5 l S"
	for _, c := range []struct {
		name, set string
		r, g, b   int
	}{
		{"G", "0.5 G", 128, 128, 128},
		{"RG", "1 0 0 RG", 255, 0, 0},
		{"K", "0 1 1 0 K", 255, 0, 0},
		{"DeviceGray SC", "/DeviceGray CS 0.5 SC", 128, 128, 128},
		{"DeviceRGB SCN", "/DeviceRGB CS 0 0 1 SCN", 0, 0, 255},
		{"DeviceCMYK SC", "/DeviceCMYK CS 1 0 1 0 SC", 0, 255, 0},
		{"CS starts at black", "1 0 0 RG /DeviceRGB CS", 0, 0, 0},
		{"CMYK starts at black too", "/DeviceCMYK CS", 0, 0, 0},
		{"too few components keep the colour", "1 0 0 RG /DeviceRGB CS 0 0 1 SC 0.5 SC", 0, 0, 255},
		{"G sets the space as well as the colour", "/DeviceRGB CS 0.5 G 0.25 SC", 64, 64, 64},
		{"the fill colour is not the stroke's", "1 0 0 rg", 0, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkPixels(t, strokeAt72(t, c.set+line), []pixel{{100, 99, c.r, c.g, c.b}}, 2)
		})
	}
}

// TestUndrawableStrokesAreRefusedByReason is the refusals, each checked at the stroke that would
// have drawn wrongly and not where its state was set: a dash pattern or a colour space no stroke
// uses is on a page that draws. TestUndrawableDashesAreRefusedByReason has each dash pattern's.
func TestUndrawableStrokesAreRefusedByReason(t *testing.T) {
	const line = " 10 w 40 100.5 m 160 100.5 l S"
	for _, c := range []struct {
		name, extg, stream, want string
	}{
		{"a dash is drawn", "", "[3 2] 0 d" + line, ""},
		{"a dash from the graphics state is drawn", "<</D[[3 2]0]>>", "/GS0 gs" + line, ""},
		{"a dash pattern", "", "[1 2 3] 0 d" + line, "stroke: a dash array of odd length 3"},
		{"an empty dash array is solid", "", "[] 0 d" + line, ""},
		{"a dash nothing strokes", "", "[3 2] 0 d 0 0 10 10 re f", ""},
		{"a named colour space", "", "/CS0 CS 1 SC" + line, "stroke: a colour space not in the resources, /CS0"},
		{"a pattern", "", "/Pattern CS /P0 SCN" + line, "stroke: a Pattern colour"},
		{"a colour space nothing strokes", "", "/CS0 CS 0 0 10 10 re f", ""},
		{"a device space after a named one", "", "/CS0 CS 0 G" + line, ""},
		{"a line cap outside 0..2", "", "3 J" + line, "stroke: line cap 3"},
		{"a line join outside 0..2", "", "3 j" + line, "stroke: line join 3"},
		{"a negative width", "", "-1 w 40 100.5 m 160 100.5 l S", "stroke: line width -1"},
		{"a stroke alpha outside 0..1", "<</CA 1.5>>", "/GS0 gs" + line, "gs: /CA is 1.5, outside 0..1"},
		// Each operator that strokes is checked, and not only S.
		{"a dash at s", "", "[1 2 3] 0 d 10 w 40 40 m 160 40 l 100 160 l s", "stroke: a dash array of odd length 3"},
		{"a dash at B", "", "[1 2 3] 0 d 10 w 40 40 m 160 40 l 100 160 l B", "stroke: a dash array of odd length 3"},
		{"a dash at B*", "", "[1 2 3] 0 d 10 w 40 40 m 160 40 l 100 160 l B*", "stroke: a dash array of odd length 3"},
		{"a dash at b", "", "[1 2 3] 0 d 10 w 40 40 m 160 40 l 100 160 l b", "stroke: a dash array of odd length 3"},
		{"a dash at b*", "", "[1 2 3] 0 d 10 w 40 40 m 160 40 l 100 160 l b*", "stroke: a dash array of odd length 3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			extg := c.extg
			if extg == "" {
				extg = "<<>>"
			}
			path := gsPDF(t, extg, c.stream)
			if c.want == "" {
				if _, err := renderAt72(t, path); err != nil {
					t.Fatalf("Page: %v, want the page drawn", err)
				}
				return
			}
			if got := refusal(t, path); !strings.Contains(got, c.want) {
				t.Errorf("refusal = %q, want it to contain %q", got, c.want)
			}
		})
	}
}

// TestTextRenderModesThatStrokeOrClipAreRefused is a defect stroking found: every mode but 3 was
// drawn as a fill, so outlined text came out solid and clipping text drew where it should have
// clipped. Mode 7 draws nothing and is refused anyway, because it clips.
func TestTextRenderModesThatStrokeOrClipAreRefused(t *testing.T) {
	for mode := 0; mode <= 7; mode++ {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			path := onePagePDF(t, fmt.Sprintf("BT %d Tr 10 10 Td (x) Tj ET", mode), 200, 200)
			want := fmt.Sprintf("render mode %d", mode)
			ra, err := renderAt72(t, path)
			refused := false
			if u, ok := err.(*Unsupported); ok {
				refused = strings.Contains(strings.Join(u.Ops, " | "), want)
			}
			switch mode {
			case 3:
				// Invisible, and so needing no font: the page draws.
				if err != nil || ra == nil {
					t.Errorf("mode 3: %v, want the page drawn", err)
				}
			case 0:
				if refused {
					t.Errorf("mode 0 refused for its render mode")
				}
			default:
				if !refused {
					t.Errorf("mode %d: err = %v, want a %q reason", mode, err, want)
				}
			}
		})
	}
}

// TestStrokeClipIsTheStrokedPath is W S: the clip is the path, not the stroke's outline, and it
// takes effect after the stroke is drawn.
func TestStrokeClipIsTheStrokedPath(t *testing.T) {
	ra := strokeAt72(t, "1 0 0 RG 10 w 50 50 100 100 re W S 0 g 0 0 200 200 re f")
	checkPixels(t, ra, []pixel{
		{100, 100, 0, 0, 0},     // inside the clip
		{46, 100, 255, 0, 0},    // the outer half of the stroke, drawn before the clip, outside it
		{20, 20, 255, 255, 255}, // outside everything
	}, 2)
}

// TestManyStrokedSegmentsIsNotQuadratic is TestManySegmentsIsNotQuadratic stroked with round joins,
// which add the most edges to a stroke's outline: every vertex of a zig-zag is a sharp turn.
func TestManyStrokedSegmentsIsNotQuadratic(t *testing.T) {
	var b strings.Builder
	b.WriteString("0 G 4 w 1 j 1 J 0 0 m ")
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&b, "%d %d l ", 10+(i%2)*580, 10+i*780/10000)
	}
	b.WriteString("S")
	s, err := pcstore.Open(onePagePDF(t, b.String(), 612, 792))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	start := time.Now()
	if _, err := New(s).Page(1, o); err != nil {
		t.Fatalf("Page: %v", err)
	}
	d := time.Since(start)
	t.Logf("10,000 stroked segments in %v", d)
	if d > time.Second {
		t.Errorf("10,000 stroked segments took %v, want under 1s", d)
	}
}

// TestAMiterAtExactlyItsLimitIsKept is §8.4.3.5's "exceeds": a miter is bevelled only when its
// ratio is over the limit, so one exactly at it is kept. No page can put a ratio exactly on a limit
// through a CTM and a flip, so this is the join alone, on a 60° apex whose ratio is exactly 2.
func TestAMiterAtExactlyItsLimitIsKept(t *testing.T) {
	a, b, c := point{-1, 0}, point{0, 0}, point{-1, math.Sqrt(3)}
	if hypot(b, c) != 2 {
		t.Fatalf("the apex is not exactly 60°: the second segment is %v long", hypot(b, c))
	}
	for _, c2 := range []struct {
		miter    float64
		vertices int
	}{{2, 1}, {math.Nextafter(2, 0), 2}} {
		s := stroker{hw: 1, pen: pen{miter: c2.miter}}
		s.join(a, b, c)
		if got := len(s.ring); got != c2.vertices {
			t.Errorf("limit %v: the join has %d vertices, want %d (1 is a miter, 2 a bevel)", c2.miter, got, c2.vertices)
		}
	}
}

// TestRoundCapsFlattenByThePensLongAxis is how finely an arc is cut, which no pixel of an ordinary
// stroke can see: it is set by the pen's radius under DrawPath's matrix1, the CTM's scale max(|a|,
// |b|), and under a CTM that stretches x a hundredfold a cap cut by the pen's short axis would be
// three chords, a sixth short of the ellipse. The whole stroke's ink is its area, and that is
// exact: an 80-pixel body one pixel tall and two half ellipses with semi-axes 50 and 0.5.
func TestRoundCapsFlattenByThePensLongAxis(t *testing.T) {
	ra := strokeAt72(t, "q 100 0 0 1 0 0 cm 0 G 1 w 1 J 0.6 100.5 m 1.4 100.5 l S Q")
	b := ra.Image.Bounds()
	ink := 0.0
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, _, _ := rgbAt(ra.Image, x, y)
			ink += float64(255-r) / 255
		}
	}
	want := 80 + math.Pi*50*0.5
	if math.Abs(ink-want) > want/100 {
		t.Errorf("the stroke's ink is %.2f pixels, want %.2f within 1%%", ink, want)
	}
}

// TestAnArcIsCutAtMatrix1sRadius is the radius an arc's step is set by: the pen's under DrawPath's
// matrix1, whose scale is max(|a|, |b|), and not the pen's largest on the device. Under a CTM that
// doubles and turns an eighth, a pen 4 wide has a radius of 2.83 in matrix1 and 4 on the device,
// and AGG cuts a round cap at the first into 7 vertices, where the second would give 8.
func TestAnArcIsCutAtMatrix1sRadius(t *testing.T) {
	out := &path{}
	k := math.Sqrt2
	s, _ := newStroker(out, 4, geom.Matrix{A: k, B: k, C: -k, D: k}, pen{cap: 1})
	s.polyline([]point{{0, 0}, {10, 0}}, false)
	if got, want := len(out.edges), 7+7; got != want {
		t.Errorf("the outline has %d edges, want %d", got, want)
	}
}

// TestAJoinsArcEndsAQuarterStepShort is stroke_calc_arc's two bounds on a join's arc: it turns the
// way the angle grows, across the angle where atan2 wraps from π to -π too, and it stops a quarter
// step short of its end. A million-pixel pen steps by π/64, and a corner turned an eighth of a step
// past a quarter turn is 31 steps and its two ends, where a whole 32nd step would fit without the
// quarter. Turned eight ways, the arc crosses the wrap in two. Each outline is two butt caps of two
// vertices, the arc outside the corner, and the two segments' ends inside it.
func TestAJoinsArcEndsAQuarterStepShort(t *testing.T) {
	turn := math.Pi/2 + math.Pi/maxArcSteps/8
	for k := range 8 {
		a := float64(k)*math.Pi/4 + 0.2
		v1 := point{1e7 * math.Cos(a), 1e7 * math.Sin(a)}
		v2 := add(v1, point{1e7 * math.Cos(a+turn), 1e7 * math.Sin(a+turn)})
		out := &path{}
		s, _ := newStroker(out, 1e6, geom.Identity, pen{join: 1})
		s.polyline([]point{{0, 0}, v1, v2}, false)
		if got, want := len(out.edges), 2+33+2+2; got != want {
			t.Errorf("first segment at %.2f radians: the outline has %d edges, want %d", a, got, want)
		}
	}
}

// TestANearlyStraightRoundJoinHasNoArc is three points in a line to within rounding, taken to pen
// space as a page at 72 dpi takes them. join's side test is 0 both ways, so the round join is
// outside the turn going out and coming back. Going out, the two offsets' angles are 1e-16 the other
// way, and stroke_calc_arc then turns that way and takes no step. Turning the way the angle grows
// took 13 steps, nearly a whole circle. Coming back they are 4e-16 apart the way the angle grows,
// which takes no step either. The outline is two butt caps and two arcs, each of its two ends.
func TestANearlyStraightRoundJoinHasNoArc(t *testing.T) {
	out := &path{}
	s, _ := newStroker(out, 10, geom.Matrix{A: 1, D: -1}, pen{join: 1})
	var pts []point
	for _, q := range []point{{0.1, 20.3}, {1.0, 22.1}, {1.3, 22.7}} {
		pts = append(pts, s.back(point{q.x, 200 - q.y}))
	}
	s.polyline(pts, false)
	if got, want := len(out.edges), 2+2+2+2; got != want {
		t.Errorf("the outline has %d edges, want %d", got, want)
	}
}

// TestARoundJoinAtAReversalTurnsAsAGGDoes is a path that goes back to its start, and one that goes
// back to a point an ulp short of it. At the reversal both joins at the tip are outside it, the two
// offsets' angles differ by π, and each arc turns the way the angle grows, round the tip: 9 vertices
// each past it. An ulp short, the join going out is outside by join's side test, and its angles
// differ by just under π. stroke_calc_arc turns that one the other way, back over the line, and the
// join coming back is inside, so the tip is flat. pdfium leaves the tip flat at some reversals too,
// from float32 (ADR 0028). The pen's half turn is 10.1 steps, so an arc a quarter step short of its
// end takes 9.
func TestARoundJoinAtAReversalTurnsAsAGGDoes(t *testing.T) {
	v0, v1 := point{115.1, 81.3}, point{40.3, 123.1}
	for _, c := range []struct {
		name         string
		v2           point
		edges, round int
	}{
		{"back to its start", v0, 2 + 11 + 2 + 11, 18},
		{"back to an ulp short of it", point{math.Nextafter(115.1, 0), 81.3}, 2 + 11 + 2 + 2, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := &path{}
			s, _ := newStroker(out, 20.5, geom.Identity, pen{join: 1})
			s.polyline([]point{v0, v1, c.v2}, false)
			dx, dy := v1.x-v0.x, v1.y-v0.y
			past := 0 // vertices more than a pixel past the tip
			for _, e := range out.edges {
				if ((e.x0-v1.x)*dx+(e.y0-v1.y)*dy)/math.Hypot(dx, dy) > 1 {
					past++
				}
			}
			if len(out.edges) != c.edges || past != c.round {
				t.Errorf("the outline has %d edges, %d of them past the tip; want %d and %d",
					len(out.edges), past, c.edges, c.round)
			}
		})
	}
}

// TestAnArcIsCutInAtMostMaxArcSteps is the bound on a round cap or join that AGG has only for a cap:
// a pen a million pixels wide, whose arcs AGG would cut into some two thousand chords each, is cut
// into maxArcSteps a half turn. The outline of a right-angled corner is two caps of 65 vertices
// each, a quarter-turn join of 33 outside it, and the two segments' ends inside it.
func TestAnArcIsCutInAtMostMaxArcSteps(t *testing.T) {
	out := &path{}
	s, _ := newStroker(out, 1e6, geom.Identity, pen{cap: 1, join: 1})
	s.polyline([]point{{0, 0}, {1e7, 0}, {1e7, 1e7}}, false)
	if got, want := len(out.edges), 65+33+65+2; got != want {
		t.Errorf("the outline has %d edges, want %d", got, want)
	}
}

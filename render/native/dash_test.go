package native

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// TestDashesAgreeWithPdfium is the dasher's acceptance test, on the patterns chosen for what
// separates them: each cap on a dash and on a dash of zero length, a phase inside the pattern,
// negative and past its period, a one-element array and a four-element one, the restart at each
// subpath, a corner inside a dash under two joins, a pen made elliptical and rotated by the CTM, a
// closed subpath that begins in a gap, a rectangle, and a dash that ends exactly on a corner.
//
// Measured at 72, 144 and 200 dpi, with the bounds set from it: every mean is at most 0.03 of 255
// and no pixel differs by more than 32, except the curve, whose flattening puts its mean at 0.071
// and 4 pixels past 32 at 144 dpi, as for the solid curve in TestStrokesAgreeWithPdfium.
func TestDashesAgreeWithPdfium(t *testing.T) {
	for _, c := range []struct {
		name   string
		stream string
		mean   float64
		over   int
	}{
		{"butt", "0 G 4 w [12 6] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"round", "0 G 4 w 1 J [12 6] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"square", "0 G 4 w 2 J [12 6] 0 d 20 100 m 180 100 l S", 0.03, 0},
		// A dash of zero length is pdfium's 0.1, which is a dot under round and square caps.
		{"dots", "0 G 4 w 1 J [0 8] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"dots with square caps", "0 G 4 w 2 J [0 8] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"dots with butt caps", "0 G 4 w 0 J [0 8] 0 d 20 100 m 180 100 l S", 0.03, 0},
		// A gap of zero length is 0.1 as well, and not a solid line.
		{"a zero gap", "0 G 4 w [4 0] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"a gap at pdfium's threshold", "0 G 4 w [4 0.000001] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"a gap past pdfium's threshold", "0 G 4 w [4 0.0000011] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"an open subpath inside one dash", "0 G 4 w [500 10] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"phase", "0 G 4 w [12 6] 7 d 20 100 m 180 100 l S", 0.03, 0},
		{"negative phase", "0 G 4 w [12 6] -7 d 20 100 m 180 100 l S", 0.03, 0},
		{"negative phase past the period", "0 G 4 w [12 6] -25 d 20 100 m 180 100 l S", 0.03, 0},
		{"phase past the period", "0 G 4 w [12 6] 43 d 20 100 m 180 100 l S", 0.03, 0},
		// A phase that ends a dash begins the gap, and draws no dash there. pdfium stays in the dash
		// with nothing left, and under round caps draws it as a dot at 96, 100, 120, 150, 200 and 300
		// dpi and not at 72, 144 or 250, the same for [12 6] 12, [10 6] 10 and [8 8] 8: what is left
		// of the dash is a rounding either side of zero in its device space, and not a rule to
		// follow. Butt caps.
		{"phase at the end of a dash", "0 G 4 w [12 6] 12 d 20 100 m 180 100 l S", 0.03, 0},
		// One ulp short of it: 2e-15 of the dash is left, too little to move the point it ends at,
		// so the dash is one point, with no direction for its caps.
		{"phase just short of the end of a dash", "0 G 4 w [12 6] 11.999999999999998 d 20 100 m 180 100 l S", 0.03, 0},
		{"one element", "0 G 4 w [9] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"four elements", "0 G 4 w [12 3 2 3] 0 d 20 100 m 180 100 l S", 0.03, 0},
		{"the pattern restarts at each subpath", "0 G 4 w [12 6] 0 d 20 60 m 180 60 l 23 140 m 180 140 l S", 0.03, 0},
		{"a corner inside a dash", "0 G 6 w [60 10] 0 d 40 40 m 100 160 l 160 40 l S", 0.03, 0},
		{"a round join inside a dash", "0 G 6 w 1 j [60 10] 0 d 40 40 m 100 160 l 160 40 l S", 0.03, 0},
		{"a curve", "0 G 3 w [10 5] 0 d 30 30 m 60 190 140 190 170 30 c S", 0.08, 4},
		{"non-uniform ctm", "q 3 0 0 1 0 0 cm 0 G 2 w 1 J [6 4] 0 d 10 40 m 50 160 l S Q", 0.03, 0},
		{"rotated ctm", "q 2 1 -0.5 1 100 20 cm 0 G 3 w [8 4] 0 d 0 0 m 0 60 l 40 60 l S Q", 0.03, 0},
		// Closed, and in a gap where it comes back to its start, so no dash wraps (TestADashThatWrapsIsJoined).
		{"closed", "0 G 6 w [50 10] 0 d 40 40 m 160 40 l 160 160 l 40 160 l h S", 0.03, 0},
		{"a rectangle", "0 G 4 w [20 10] 0 d 40 40 120 120 re S", 0.03, 0},
		{"a dash that ends on a corner", "0 G 6 w [60 10] 0 d 40 100 m 100 100 l 100 160 l S", 0.03, 0},
		// TestADashEndingOnACornerTakesNoJoin's page: the dash ends there only up to rounding.
		{"a dash that ends on a corner in pen space", "0 G 6 w [37 3] 0 d 1 150 m 38 150 l 28 180 l S", 0.03, 0},
		// And one that goes 0.8% of the segment past it, which is a join and not a rounding.
		{"a dash that crosses a corner", "0 G 2 w [100.8 3] 0 d 0 150 m 100 150 l 90 180 l S", 0.03, 0},
		{"thin, under a uniform ctm", "q 0.67188 0 0 0.67188 0 0 cm 0 G 0.5 w [1.85 0.8] 0 d 20 100 m 280 100 l S Q", 0.03, 0},
		// d with one operand, and d whose first operand is not an array, are ignored: a solid line.
		{"d with no phase", "0 G 4 w [6 6] d 20 100 m 180 100 l S", 0.03, 0},
		{"d with no array", "0 G 4 w 6 0 d 20 100 m 180 100 l S", 0.03, 0},
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

// TestADashFromTheGraphicsStateAgreesWithPdfium is /D, which sets the pattern and phase as d does.
func TestADashFromTheGraphicsStateAgreesWithPdfium(t *testing.T) {
	path := gsPDF(t, "<</D[[12 6]7]>>", "0 G 4 w /GS0 gs 20 100 m 180 100 l S")
	for _, dpi := range []float64{72, 144, 200} {
		mean, worst, over := comparePath(t, path, dpi)
		t.Logf("%g dpi: mean %.3f worst %.0f over-32 %d", dpi, mean, worst, over)
		if mean > 0.03 || over > 0 {
			t.Errorf("%g dpi: mean %.3f, %d over 32 (worst %.0f); want <= 0.03 and none", dpi, mean, over, worst)
		}
	}
}

// TestADashThatWrapsIsJoined pins the one place the dasher follows §8.4.3.3 and not pdfium: a
// closed square whose pattern is in a dash when it comes back to its start. In the first, the last
// dash runs up the left side into the corner at (40,40) and the first leaves along the bottom; in
// the second, one dash is the whole square. Either way §8.4.3.3 draws a miter at that corner, and
// pdfium ends one dash and begins the other, both butt-capped, leaving the corner's outer square
// empty.
func TestADashThatWrapsIsJoined(t *testing.T) {
	for _, c := range []struct{ name, pattern string }{
		{"a dash across the start", "[50 10] 20 d"},
		{"the whole subpath in one dash", "[500 10] 0 d"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := onePagePDF(t, "0 G 6 w "+c.pattern+" 40 40 m 160 40 l 160 160 l 40 160 l h S", 200, 200)
			const x, y = 38, 161 // user (38..39, 38..39): outside both sides, inside the miter
			got, w, _ := ink(renderNative(t, path, 72).Image)
			if v := got[y*w+x]; v < 250 {
				t.Errorf("ink at the wrapped corner = %d, want the miter's full ink", v)
			}

			ref, err := pdfium.Open(path)
			if err != nil {
				t.Fatalf("pdfium: %v", err)
			}
			defer func() { _ = ref.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			r, err := ref.Page(1, o)
			if err != nil {
				t.Fatalf("pdfium Page: %v", err)
			}
			want, pw, _ := ink(r.Image)
			if v := want[y*pw+x]; v > 5 {
				t.Errorf("pdfium's ink at the wrapped corner = %d, want paper: the difference this pins is gone", v)
			}
		})
	}
}

// TestADashEndingOnItsStartTakesNoJoin is a closed subpath whose pattern ends a dash exactly where
// it comes back to its start: the square has sides of 112.5, so the dash that begins 30 along the
// bottom ends at 450, the start. A dash that ends on a corner takes no join, and neither does this
// one; pdfium caps both ends there too. In the second, one dash is the whole square exactly.
//
// Measured with the join gone: every mean at most 0.020, and at most 4 pixels past 32 (worst 64),
// all at the inside of a corner, where the solid square has the same 4 at 200 dpi.
func TestADashEndingOnItsStartTakesNoJoin(t *testing.T) {
	for _, c := range []struct{ name, stream string }{
		{"the last of several dashes", "0 G 6 w [50 10] 20 d 40 40 m 152.5 40 l 152.5 152.5 l 40 152.5 l h S"},
		{"one dash the length of the subpath", "0 G 6 w [480 10] 0 d 40 40 m 160 40 l 160 160 l 40 160 l h S"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := onePagePDF(t, c.stream, 200, 200)
			const x, y = 38, 161 // as in TestADashThatWrapsIsJoined: inside the miter a join would draw
			got, w, _ := ink(renderNative(t, path, 72).Image)
			if v := got[y*w+x]; v > 5 {
				t.Errorf("ink at the start = %d, want paper: the dash was joined", v)
			}
			for _, dpi := range []float64{72, 144, 200} {
				mean, worst, over := comparePath(t, path, dpi)
				t.Logf("%g dpi: mean %.3f worst %.0f over-32 %d", dpi, mean, worst, over)
				if mean > 0.03 || over > 4 {
					t.Errorf("%g dpi: mean %.3f, %d over 32 (worst %.0f); want <= 0.03 and at most 4", dpi, mean, over, worst)
				}
			}
		})
	}
}

// TestAPhaseOnAnElementsEndBeginsTheNext is one pattern at one place written two ways: [10 5 20 5]
// with a phase of 15 is [20 5 10 5] with none, and both start the square on the 20. The square's
// sides of 122.5 bring it back to its start 10 into a dash, which §8.4.3.3 joins to the first. A
// phase that stayed in the gap it ends, as AGG's does, broke the pattern at the start with a gap
// of nothing, and capped both dashes there.
func TestAPhaseOnAnElementsEndBeginsTheNext(t *testing.T) {
	const square = " 40 40 m 162.5 40 l 162.5 162.5 l 40 162.5 l h S"
	for _, dpi := range []float64{72, 144} {
		want, w, _ := ink(renderNative(t, onePagePDF(t, "0 G 6 w [20 5 10 5] 0 d"+square, 200, 200), dpi).Image)
		for _, pattern := range []string{"[10 5 20 5] 15 d", "[10 5 20 5] -25 d"} {
			got, _, _ := ink(renderNative(t, onePagePDF(t, "0 G 6 w "+pattern+square, 200, 200), dpi).Image)
			n := 0
			for i := range got {
				if got[i] != want[i] {
					n++
				}
			}
			if n != 0 {
				t.Errorf("%g dpi: %s differs from [20 5 10 5] 0 d at %d pixels", dpi, pattern, n)
			}
		}
		x, y := 38, 161 // as in TestADashThatWrapsIsJoined, at 72 dpi
		if dpi == 144 {
			x, y = 77, 322
		}
		if v := want[y*w+x]; v < 250 {
			t.Errorf("%g dpi: ink at the start = %d, want the miter's full ink", dpi, v)
		}
	}
}

// TestADashEndingOnACornerTakesNoJoin is a defect the dasher had: a 37-unit dash along a 37-unit
// segment ended a rounding error short of the corner in pen space, so the dash went on round it
// and drew a miter there. pdfium draws none, and neither does the §8.4.3.6 reading.
func TestADashEndingOnACornerTakesNoJoin(t *testing.T) {
	ra := renderNative(t, onePagePDF(t, "0 G 6 w [37 3] 0 d 1 150 m 38 150 l 28 180 l S", 200, 200), 200)
	a, w, _ := ink(ra.Image)
	if v := a[144*w+112]; v != 0 {
		t.Errorf("ink past the corner = %d, want paper", v)
	}
}

// TestUndrawableDashesAreRefusedByReason is each dash pattern and each dashed subpath this backend
// does not draw as pdfium does, refused at the stroke; the patterns pdfium ignores are drawn.
func TestUndrawableDashesAreRefusedByReason(t *testing.T) {
	const line = " 10 w 40 100.5 m 160 100.5 l S"
	past := "34028236" + strings.Repeat("0", 31) + ".0" // 3.4028236e38, just past float32's range
	top := "34028234" + strings.Repeat("0", 31) + ".0"  // 3.4028234e38, just inside it
	for _, c := range []struct {
		name, extg, stream, want string
	}{
		{"odd", "", "[1 2 3] 0 d" + line, "stroke: a dash array of odd length 3"},
		{"33 elements", "", "[" + strings.Repeat("1 ", 33) + "] 0 d" + line, "stroke: a dash array of 33 elements"},
		{"32 elements", "", "[" + strings.Repeat("1 ", 32) + "] 0 d" + line, ""},
		{"a negative element", "", "[3 -2] 0 d" + line, "stroke: a negative dash element, -2"},
		{"zeros", "", "[0 0] 0 d" + line, "stroke: a dash array of zeros"},
		{"an element not a number", "", "[3 /x] 0 d" + line, "stroke: a dash element that is not a number"},
		{"a phase not a number", "", "[3 2] /x d" + line, "stroke: a dash phase that is not a number"},
		{"/D with no phase", "<</D[[3 2]]>>", "/GS0 gs" + line, "stroke: a dash phase that is not a number"},
		{"/D with no array", "<</D[5 0]>>", "/GS0 gs" + line, ""},
		{"an element past float32", "", "[3 " + past + "] 0 d" + line, "stroke: a dash element of 3.4028236e+38"},
		{"an element inside float32", "", "[3 " + top + "] 0 d" + line, ""},
		{"a phase past float32", "", "[3 2] -" + past + " d" + line, "stroke: a dash phase of -3.4028236e+38"},
		{"a phase inside float32", "", "[3 2] -" + top + " d" + line, ""},
		// Taken modulo the period, and not walked off one element at a time.
		{"a positive phase inside float32", "", "[3 2] " + top + " d" + line, ""},
		{"a lone m", "", "[3 3] 0 d 1 J 10 w 100 100 m S", ""},
		// Open, a subpath that goes nowhere is drawn a pixel long or not at all, as pdfium draws it.
		{"a subpath of zero length", "", "[3 3] 0 d 1 J 10 w 100 100 m 100 100 l S", ""},
		{"a subpath of zero length with butt caps", "", "[3 3] 0 d 10 w 100 100 m 100 100 l S", ""},
		{"a subpath of zero length beside another", "", "[3 3] 0 d 1 J 10 w 50 50 m 150 150 l 100 100 m 100 100 l S", ""},
		{"m h", "", "[3 3] 0 d 1 J 10 w 100 100 m h S", "stroke: a closed dashed subpath of zero length"},
		// The survey closes the path at s, as paint does, and a closed m is not a lone one.
		{"m s", "", "[3 3] 0 d 1 J 10 w 100 100 m s", "stroke: a closed dashed subpath of zero length"},
		{"m b", "", "[3 3] 0 d 1 J 10 w 100 100 m b", "stroke: a closed dashed subpath of zero length"},
		{"m b*", "", "[3 3] 0 d 1 J 10 w 100 100 m b*", "stroke: a closed dashed subpath of zero length"},
		{"m l h", "", "[3 3] 0 d 2 J 10 w 100 100 m 100 100 l h S", "stroke: a closed dashed subpath of zero length"},
		// Under butt caps pdfium's content parser drops a closed m: there is nothing to dash.
		{"m h under butt caps", "", "[3 3] 0 d 10 w 100 100 m h S", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			extg := c.extg
			if extg == "" {
				extg = "<<>>"
			}
			path := gsPDF(t, extg, "0 G "+c.stream)
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

// TestDashesAreBounded pins maxDashes, and that the survey charges each dashed stroke the path
// paint would stroke there: each painting operator and each form with a /BBox ends the path, s
// closes it first, and every stroke on the page adds to one count. Each refused page would draw
// over a million dashes, and none is drawn.
func TestDashesAreBounded(t *testing.T) {
	const dashed = "[0.5 0.5] 0 d "
	long := func(n int) string { return fmt.Sprintf("0 0 m %d 0 l ", n) }
	const short = "10 w 40 100 m 160 100 l S"
	e200 := "1" + strings.Repeat("0", 200) + ".0"
	em200 := "0." + strings.Repeat("0", 199) + "1"
	want := fmt.Sprintf("stroke: the page's dashes run past %d", maxDashes)
	form := func(bbox string) string {
		return fmt.Sprintf("<</Type/XObject/Subtype/Form%s/Length 0>>\nstream\n\nendstream", bbox)
	}
	// A dash for each subpath's partial periods, whatever its length: 16 dashes at each end of a
	// subpath under a pattern of 32 elements, so 33,000 subpaths of 0.001 are past the bound.
	var tiny strings.Builder
	tiny.WriteString("[" + strings.Repeat("1 ", 32) + "] 0 d ")
	for i := 0; i < 33_000; i++ {
		tiny.WriteString("0 0 m 0.001 0 l ")
	}
	tiny.WriteString("S")
	cases := []struct{ name, stream, form, want string }{
		{"one stroke", dashed + long(1_100_000) + "S", "", want},
		{"one element", "[0.5] 0 d " + long(1_100_000) + "S", "", want},
		{"four elements", "[0.5 0.5 0.5 0.5] 0 d " + long(1_100_000) + "S", "", want},
		{"two segments", dashed + "0 0 m 550000 0 l 0 0 l S", "", want},
		{"many short subpaths", tiny.String(), "", want},
		// Two points at x = +Inf in pen space are a segment of no known length, and the count is NaN.
		{"a length that is not a number", "q " + e200 + " 0 0 " + em200 + " 0 0 cm " + dashed +
			"0 0 m " + e200 + " 0 l 2" + e200 + " 0 l S Q", "", want},
		{"two strokes", dashed + long(600_000) + "S " + long(600_000) + "S", "", want},
		{"a closing segment", dashed + long(600_000) + "s", "", want},
		{"a form drawn four times", "/Fm0 Do /Fm0 Do /Fm0 Do /Fm0 Do",
			formXO("0 0 200 200", "", "0 G "+dashed+long(300_000)+"S"), want},
		{"a path kept across a form with no /BBox", dashed + long(1_100_000) + "/Fm0 Do " + short, form(""), want},
		{"a path ended by a form's /BBox", dashed + long(1_100_000) + "/Fm0 Do " + short, form("/BBox[0 0 200 200]"), ""},
	}
	for _, op := range []string{"f", "F", "f*", "n"} {
		cases = append(cases, struct{ name, stream, form, want string }{
			"a path ended by " + op, dashed + long(1_100_000) + op + " " + short, "", ""})
	}
	for _, op := range []string{"S", "s", "B", "B*", "b", "b*"} {
		cases = append(cases, struct{ name, stream, form, want string }{
			"a path ended by " + op, long(1_100_000) + op + " " + dashed + short, "", ""})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var path string
			if c.form != "" {
				path = xoPDF(t, "/Fm0 5 0 R", "0 G "+c.stream, c.form)
			} else {
				path = onePagePDF(t, "0 G "+c.stream, 200, 200)
			}
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

// TestPaintStartsWithNoPath is a defect building the path in the survey made possible: a page that
// ends with a subpath it never paints left that subpath in the walker, and paint began from it, so
// the first stroke drew it too.
func TestPaintStartsWithNoPath(t *testing.T) {
	ra := strokeAt72(t, "0 G 10 w 50 50 m 150 50 l S 20 180 m 180 180 l")
	checkPixels(t, ra, []pixel{grey(100, 150, black0), grey(100, 20, paper)}, 2)
}

// TestTheDashChargeIsInUserSpace is maxDashes at resolutions far from 72 dpi: the bound counts dashes
// along the path in user space, where they are measured, and not in device pixels, so a page is
// refused or drawn at every resolution alike.
func TestTheDashChargeIsInUserSpace(t *testing.T) {
	path := onePagePDF(t, "0 G [0.5 0.5] 0 d 0 0 m 1100000 0 l S", 200, 200)
	want := fmt.Sprintf("stroke: the page's dashes run past %d", maxDashes)
	for _, dpi := range []float64{20, 300} {
		if got := refusalAt(t, path, dpi); !strings.Contains(got, want) {
			t.Errorf("%g dpi: refusal = %q, want it to contain %q", dpi, got, want)
		}
	}
}

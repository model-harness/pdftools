package layout

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

func hrule(y, x0, x1 float64) doc.Rule { return doc.Rule{Pos: y, From: x0, To: x1} }

// frame is a grid of rules: column lines at xs, row lines at ys (descending), every line
// drawn its whole length, less the segments skip names. A skipped segment is "v c r", the
// column line c across row r, or "h r c", the row line r across column c.
func frame(xs, ys []float64, skip ...string) []doc.Rule {
	gone := map[string]bool{}
	for _, s := range skip {
		gone[s] = true
	}
	var out []doc.Rule
	for c, x := range xs {
		for r := 0; r+1 < len(ys); r++ {
			if !gone[strings.Join([]string{"v", itoa(c), itoa(r)}, " ")] {
				out = append(out, vrule(x, ys[r+1], ys[r]))
			}
		}
	}
	for r, y := range ys {
		for c := 0; c+1 < len(xs); c++ {
			if !gone[strings.Join([]string{"h", itoa(r), itoa(c)}, " ")] {
				out = append(out, hrule(y, xs[c], xs[c+1]))
			}
		}
	}
	return out
}

func itoa(i int) string { return string(rune('0' + i)) }

// put is a span of text set in the grid at x, its foot at y, 10pt tall.
func put(text string, x, y float64) doc.Span {
	return cellSpan(text, x, x+float64(len(text))*4, y, y+10)
}

func latticePage(rules []doc.Rule, blocks ...[]doc.Span) *doc.Document {
	p := doc.Page{Number: 1, Rules: rules}
	for _, sp := range blocks {
		p.Blocks = append(p.Blocks, doc.Block{Role: doc.RoleParagraph, Spans: sp})
	}
	return &doc.Document{Pages: []doc.Page{p}}
}

// cells renders a page's table cells as "r,c:text" in block order.
func cells(p doc.Page) string {
	var out []string
	for _, b := range p.Blocks {
		if b.Cell != nil {
			h := ""
			if b.Cell.Header {
				h = "*"
			}
			out = append(out, itoa(b.Cell.Row)+","+itoa(b.Cell.Col)+h+":"+strings.TrimSpace(b.Text()))
		}
	}
	return strings.Join(out, " ")
}

var (
	cols3 = []float64{100, 200, 300, 400}
	rows3 = []float64{700, 680, 660, 640}
)

// TestALatticeReadsAFramedGrid: a grid drawn whole reads cell for cell, its first row the
// header.
func TestALatticeReadsAFramedGrid(t *testing.T) {
	d := latticePage(frame(cols3, rows3),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("1", 105, 665), put("2", 205, 665), put("3", 305, 665)},
		[]doc.Span{put("4", 105, 645), put("5", 205, 645), put("6", 305, 645)})
	st := Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:1 1,1:2 1,2:3 2,0:4 2,1:5 2,2:6"; cells(d.Pages[0]) != want {
		t.Errorf("cells = %s, want %s", cells(d.Pages[0]), want)
	}
	if st.Tables != 1 || st.Rows != 3 || st.Cells != 9 {
		t.Errorf("stats = %+v", st)
	}
}

// TestALatticeMergesWhereNoRuleIsDrawn is table-merged.pdf's shape. A header spans the last two
// columns, and its text goes to the first. A label spans two rows centred, aligned with neither,
// and goes to the first. A label under an empty cell, with no rule between them, shares its own
// row's baseline and stays in its row.
func TestALatticeMergesWhereNoRuleIsDrawn(t *testing.T) {
	ys := []float64{720, 700, 680, 660, 640}
	rules := frame(cols3, ys, "v 2 0", "h 1 0", "h 3 0")
	d := latticePage(rules,
		[]doc.Span{put("Sales", 300, 705)},
		[]doc.Span{put("Product", 105, 685), put("2024", 205, 685), put("2025", 305, 685)},
		[]doc.Span{put("Kettles", 105, 652), put("120", 205, 665), put("135", 305, 665)},
		[]doc.Span{put("80", 205, 645), put("95", 305, 645)})
	Tables(d, DefaultOptions)
	want := "0,1*:Sales 1,0:Product 1,1:2024 1,2:2025 2,0:Kettles 2,1:120 2,2:135 3,1:80 3,2:95"
	if got := cells(d.Pages[0]); got != want {
		t.Errorf("cells = %s\nwant    %s", got, want)
	}
}

// TestALatticeCellWrapsAsOne: a cell's two lines are one cell, joined by a space, where its
// neighbours have one line.
func TestALatticeCellWrapsAsOne(t *testing.T) {
	d := latticePage(frame(cols3, []float64{700, 680, 640}),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("one", 105, 660), put("first", 205, 665), put("three", 305, 660)},
		[]doc.Span{put("line", 205, 652)})
	Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:one 1,1:first line 1,2:three"; cells(d.Pages[0]) != want {
		t.Errorf("cells = %s, want %s", cells(d.Pages[0]), want)
	}
	// The wrap is extract's rule's to join, so a hyphen holding a word gets no space.
	d = latticePage(frame(cols3, []float64{700, 680, 640}),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("one", 105, 660), put("exten-", 205, 665), put("three", 305, 660)},
		[]doc.Span{put("sive", 205, 652)})
	Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:one 1,1:exten-sive 1,2:three"; cells(d.Pages[0]) != want {
		t.Errorf("hyphenated = %s, want %s", cells(d.Pages[0]), want)
	}
}

// TestALatticeCountsOverlappingStrokesOnce: a line drawn in two strokes that overlap covers
// their union. Here 28 of the frame's 60 points, under half, so it is no column line, though the
// strokes' lengths sum past half.
func TestALatticeCountsOverlappingStrokesOnce(t *testing.T) {
	rules := append(frame(cols3, rows3), vrule(250, 640, 660), vrule(250, 650, 668))
	d := latticePage(rules,
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("1", 105, 665), put("x ", 205, 665), put("y", 255, 665), put("3", 305, 665)},
		[]doc.Span{put("4", 105, 645), put("5", 205, 645), put("6", 305, 645)})
	Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:1 1,1:x y 1,2:3 2,0:4 2,1:5 2,2:6"; cells(d.Pages[0]) != want {
		t.Errorf("cells = %s, want %s", cells(d.Pages[0]), want)
	}
}

// TestALatticeRowSplitsAtSharedBaselines: a framed table with no rules between its body rows has
// a line in every column on each of them, and each is a row. Two of three columns sharing a
// second line is a wrap, and stays one row.
func TestALatticeRowSplitsAtSharedBaselines(t *testing.T) {
	d := latticePage(frame(cols3, []float64{700, 680, 640}),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("1", 105, 665), put("2", 205, 665), put("3", 305, 665)},
		[]doc.Span{put("4", 105, 650), put("5", 205, 650), put("6", 305, 650)})
	Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:1 1,1:2 1,2:3 2,0:4 2,1:5 2,2:6"; cells(d.Pages[0]) != want {
		t.Errorf("all columns = %s, want %s", cells(d.Pages[0]), want)
	}
	d = latticePage(frame(cols3, []float64{700, 680, 640}),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("1", 105, 665), put("2", 205, 665), put("3", 305, 665)},
		[]doc.Span{put("4", 105, 650), put("5", 205, 650)})
	Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:1 4 1,1:2 5 1,2:3"; cells(d.Pages[0]) != want {
		t.Errorf("two columns = %s, want %s", cells(d.Pages[0]), want)
	}
}

// TestALatticeNeedsAFrame: a grid missing a stretch of its outer border longer than a rule's
// width is not framed, and is left to the band reader; within it, it is.
func TestALatticeNeedsAFrame(t *testing.T) {
	spans := func() [][]doc.Span {
		return [][]doc.Span{
			{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
			{put("1", 105, 665), put("2", 205, 665), put("3", 305, 665)},
		}
	}
	ys := []float64{700, 680, 660}
	for _, c := range []struct {
		name  string
		rules []doc.Rule
		table bool
	}{
		{"framed", frame(cols3, ys), true},
		{"a short gap in the right border", append(frame(cols3, ys, "v 3 0"), vrule(400, 681.6, 700)), true},
		{"a long gap in the right border", frame(cols3, ys, "v 3 0"), false},
		{"no right border, so a frame one column narrower", frame(cols3, ys, "v 3 0", "v 3 1"), true},
		{"no top border", frame(cols3, ys, "h 0 0", "h 0 1", "h 0 2"), false},
		{"a long gap in the top border", frame(cols3, ys, "h 0 1"), false},
		{"a long gap in the bottom border", frame(cols3, ys, "h 2 1"), false},
		{"a long gap in the left border", frame(cols3, ys, "v 0 0"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &doc.Page{Rules: c.rules}
			for _, sp := range spans() {
				p.Blocks = append(p.Blocks, doc.Block{Role: doc.RoleParagraph, Spans: sp})
			}
			var n int
			var st TableStats
			latticeTables(p, &n, &st)
			if (st.Tables == 1) != c.table {
				t.Errorf("tables = %d, want a table %v", st.Tables, c.table)
			}
		})
	}
}

// TestALatticeIgnoresWordBoxes: a box drawn around a word inside a cell, as v110-changes.pdf
// draws around inline code, is no column or row line: its rules cover a line's worth of a frame,
// not half of it.
func TestALatticeIgnoresWordBoxes(t *testing.T) {
	rules := append(frame(cols3, rows3), vrule(230, 660, 674), vrule(260, 660, 674), hrule(674, 230, 260))
	d := latticePage(rules,
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("1", 105, 665), put("a ", 205, 665), put("code", 232, 665), put("3", 305, 665)},
		[]doc.Span{put("4", 105, 645), put("5", 205, 645), put("6", 305, 645)})
	Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:1 1,1:a code 1,2:3 2,0:4 2,1:5 2,2:6"; cells(d.Pages[0]) != want {
		t.Errorf("cells = %s, want %s", cells(d.Pages[0]), want)
	}
}

// TestALatticeNeedsTwoRows: one row is a line with a box around it as often as a table.
func TestALatticeNeedsTwoRows(t *testing.T) {
	d := latticePage(frame(cols3, []float64{700, 680}),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)})
	if st := Tables(d, DefaultOptions); st.Tables != 0 {
		t.Errorf("tables = %d, want 0", st.Tables)
	}
	if latticePage(frame([]float64{100, 400}, rows3)).Pages[0].Rules == nil {
		t.Fatal("no rules")
	}
	if ls := lattices(frame([]float64{100, 400}, rows3)); len(ls) != 0 {
		t.Errorf("a frame of one column is %d lattices, want 0", len(ls))
	}
}

// TestALatticeKeepsTheTextAroundIt: a block holding text before and after its table spans keeps
// both, in order, either side of the cells, and every span lands in one block.
func TestALatticeKeepsTheTextAroundIt(t *testing.T) {
	d := latticePage(frame(cols3, []float64{700, 680, 660}),
		[]doc.Span{put("Before.", 105, 720), put("A", 105, 685), put("B", 205, 685), put("C", 305, 685),
			put("1", 105, 665), put(" ", 150, 665), put("2", 205, 665), put("3", 305, 665), put("After.", 105, 600)})
	Tables(d, DefaultOptions)
	var got []string
	for _, b := range d.Pages[0].Blocks {
		got = append(got, string(b.Role)+":"+b.Text())
	}
	want := "paragraph:Before. table_cell:A table_cell:B table_cell:C table_cell:1  table_cell:2 table_cell:3 paragraph:After."
	if strings.Join(got, " ") != want {
		t.Errorf("blocks = %q\nwant %q", strings.Join(got, " "), want)
	}
}

// TestLatticeLinesMergeWithinARulesWidth: two rules a rule's width apart are one line.
func TestLatticeLinesMergeWithinARulesWidth(t *testing.T) {
	if got := lines([]float64{100, 101.5, 200}, false); len(got) != 2 {
		t.Errorf("lines = %v, want two", got)
	}
	if got := lines([]float64{100, 101.6, 200}, false); len(got) != 3 {
		t.Errorf("lines = %v, want three", got)
	}
	if got := lines([]float64{100, 300, 200}, true); got[0] != 300 || got[2] != 100 {
		t.Errorf("descending = %v", got)
	}
}

// TestALatticeRowWithOneColumnIsOneRow: lines in one column alone, the others empty, are a cell
// that wraps, not rows.
func TestALatticeRowWithOneColumnIsOneRow(t *testing.T) {
	d := latticePage(frame(cols3, []float64{700, 680, 640}),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("one ", 105, 665), put("two", 105, 650)})
	Tables(d, DefaultOptions)
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:one two"; cells(d.Pages[0]) != want {
		t.Errorf("cells = %s, want %s", cells(d.Pages[0]), want)
	}
}

// TestALatticeRowSplitsInPageOrder: rows split at shared baselines are ordered top down, whatever
// order the page gives their spans in.
func TestALatticeRowSplitsInPageOrder(t *testing.T) {
	d := latticePage(frame(cols3, []float64{700, 680, 640}),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("4", 105, 650), put("5", 205, 650), put("6", 305, 650)},
		[]doc.Span{put("1", 105, 665), put("2", 205, 665), put("3", 305, 665)})
	Tables(d, DefaultOptions)
	if got := cells(d.Pages[0]); !strings.Contains(got, "1,0:1 ") || !strings.Contains(got, "2,0:4 ") {
		t.Errorf("cells = %s, want 1 2 3 the first body row", got)
	}
}

// TestALatticeDropsARowOfSpaces: a row of cells drawn with nothing in them but spaces is no row.
func TestALatticeDropsARowOfSpaces(t *testing.T) {
	d := latticePage(frame(cols3, rows3),
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put(" ", 105, 665), put(" ", 205, 665)},
		[]doc.Span{put("4", 105, 645), put("5", 205, 645), put("6", 305, 645)})
	if st := Tables(d, DefaultOptions); st.Rows != 2 {
		t.Errorf("rows = %d, want 2", st.Rows)
	}
}

// TestLatticesStackedOnOneLine: two tables one above the other, their borders on the same x,
// are two tables; a gap between them on one line is no rule.
func TestLatticesStackedOnOneLine(t *testing.T) {
	rules := append(frame(cols3, []float64{700, 680, 660}), frame(cols3, []float64{600, 580, 560})...)
	d := latticePage(rules,
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("1", 105, 665), put("2", 205, 665), put("3", 305, 665)},
		[]doc.Span{put("D", 105, 585), put("E", 205, 585), put("F", 305, 585)},
		[]doc.Span{put("4", 105, 565), put("5", 205, 565), put("6", 305, 565)})
	if st := Tables(d, DefaultOptions); st.Tables != 2 || st.Cells != 12 {
		t.Errorf("stats = %+v, want 2 tables of 6 cells", st)
	}
}

// TestNestedLatticesTakeASpanOnce: a framed table drawn inside another's cell, not touching it,
// is read once: every span reaches one block.
func TestNestedLatticesTakeASpanOnce(t *testing.T) {
	outer := frame([]float64{100, 300, 500}, []float64{800, 780, 600})
	inner := frame([]float64{320, 400, 480}, []float64{760, 740, 720})
	d := latticePage(append(outer, inner...),
		[]doc.Span{put("A", 105, 785), put("B", 305, 785)},
		[]doc.Span{put("x", 105, 700), put("p", 325, 745), put("q", 405, 745), put("r", 325, 725), put("s", 405, 725)})
	Tables(d, DefaultOptions)
	seen := map[string]int{}
	for _, b := range d.Pages[0].Blocks {
		for _, sp := range b.Spans {
			seen[sp.Text]++
		}
	}
	for _, s := range []string{"A", "B", "x", "p", "q", "r", "s"} {
		if seen[s] != 1 {
			t.Errorf("%q reaches %d blocks, want 1", s, seen[s])
		}
	}
}

// TestALatticeTakesOnlyParagraphText: furniture inside a frame, a watermark, stays furniture, and
// a span with no box, which no glyph drew, stays in its block even at the page's corner.
func TestALatticeTakesOnlyParagraphText(t *testing.T) {
	d := latticePage(frame([]float64{0, 100, 200}, []float64{50, 25, 0}),
		[]doc.Span{put("A", 5, 30), put("B", 105, 30)},
		[]doc.Span{put("1", 5, 5), put("2", 105, 5), {Text: "inferred", MCID: -1}})
	mark := doc.Block{Role: doc.RoleArtifact, Spans: []doc.Span{put("DRAFT", 60, 20)}}
	d.Pages[0].Blocks = append(d.Pages[0].Blocks, mark)
	Tables(d, DefaultOptions)
	var roles []string
	for _, b := range d.Pages[0].Blocks {
		if b.Cell == nil {
			roles = append(roles, string(b.Role)+":"+b.Text())
		}
	}
	if want := "paragraph:inferred artifact:DRAFT"; strings.Join(roles, " ") != want {
		t.Errorf("non-cells = %q, want %q", strings.Join(roles, " "), want)
	}
}

// TestALatticeIsNotAShape: a frame under four rules' widths across, a drawn shape, is no lattice.
func TestALatticeIsNotAShape(t *testing.T) {
	// 4pt wide, its top drawn 1.2pt: framed within its tolerance, under half covered.
	narrow := []doc.Rule{hrule(700, 100, 101.2), hrule(680, 100, 104),
		vrule(100, 680, 700), vrule(102, 680, 700), vrule(104, 680, 700)}
	if ls := lattices(narrow); len(ls) != 0 {
		t.Errorf("a frame 4pt wide is %d lattices, want 0", len(ls))
	}
}

// TestALatticeIsNotBridgedByAPoint: two tables 3pt apart are two, and a rule with no length
// between them, touching both, does not make them one.
func TestALatticeIsNotBridgedByAPoint(t *testing.T) {
	right := []float64{403, 503, 603, 703}
	ys := []float64{700, 680, 660}
	rules := append(append(frame(cols3, ys), frame(right, ys)...), vrule(401.5, 690, 690))
	if ls := lattices(rules); len(ls) != 2 {
		t.Errorf("lattices = %d, want 2", len(ls))
	}
}

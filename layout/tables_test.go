package layout

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// cellSpan builds one span occupying a box, which is all Tables reads: the grid comes
// from geometry, never from the text.
func cellSpan(text string, x0, x1, y0, y1 float64) doc.Span {
	return doc.Span{
		Text:  text,
		MCID:  -1,
		Box:   geom.Rect{X0: x0, Y0: y0, X1: x1, Y1: y1},
		Style: doc.Style{Size: 10},
	}
}

// vrule builds a vertical rule at x spanning y0..y1.
func vrule(x, y0, y1 float64) doc.Rule {
	return doc.Rule{Vertical: true, Pos: x, From: y0, To: y1}
}

// rowSpans lays out one row of cells 40 wide on a 50 pitch, starting at x=100.
func rowSpans(y float64, texts ...string) []doc.Span {
	var out []doc.Span
	for i, txt := range texts {
		x0 := 100 + float64(i)*50
		out = append(out, cellSpan(txt, x0, x0+40, y, y+10))
	}
	return out
}

// gridRules returns the verticals dividing a row laid out by rowSpans, one between each
// adjacent pair, spanning the row band.
func gridRules(y float64, n int) []doc.Rule {
	var out []doc.Rule
	for i := 1; i < n; i++ {
		out = append(out, vrule(100+float64(i)*50-5, y, y+10))
	}
	return out
}

// tableDoc builds a page whose single paragraph block holds rows at descending y, each
// divided by its own verticals — the shape reference/table.pdf has after extraction, where
// a LaTeX table draws each row's rules immediately before that row's text.
func tableDoc(rows ...[]string) *doc.Document {
	var spans []doc.Span
	var rules []doc.Rule
	y := 700.0
	for _, r := range rows {
		spans = append(spans, rowSpans(y, r...)...)
		rules = append(rules, gridRules(y, len(r))...)
		y -= 20
	}
	return &doc.Document{Pages: []doc.Page{{
		Number: 1,
		Blocks: []doc.Block{{Role: doc.RoleParagraph, Spans: spans}},
		Rules:  rules,
	}}}
}

// grid renders a page's cell blocks as "r,c:text" in row then column order, so an
// assertion reads as the table it describes and a transposition cannot pass.
func grid(p doc.Page) string {
	var cells []string
	for _, b := range p.Blocks {
		if b.Cell == nil {
			continue
		}
		h := ""
		if b.Cell.Header {
			h = "*"
		}
		cells = append(cells, fmt.Sprintf("t%d %d,%d%s:%s", b.Cell.Table, b.Cell.Row, b.Cell.Col, h,
			strings.TrimSpace(b.Text())))
	}
	return strings.Join(cells, " ")
}

// TestTablesReadsTheDrawnGrid is the central assertion: three rows of three become nine
// cells at the right coordinates, from the strokes alone.
//
// This is testdata/reference/table.pdf in miniature, and that fixture is the real
// assertion — cmd/pdfspec's TestReferenceExactMatch holds its output byte-identical to
// tagged-table.gold.md, the same table declared rather than drawn. This test is what says
// which part of the pipeline is responsible when that one goes red.
func TestTablesReadsTheDrawnGrid(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B", "Header C"},
		[]string{"Cell A1", "Cell B1", "Cell C1"},
		[]string{"Cell A2", "Cell B2", "Cell C2"},
	)
	st := Tables(d, DefaultOptions)
	if st.Tables != 1 || st.Rows != 3 || st.Cells != 9 {
		t.Fatalf("stats = %+v, want 1 table 3 rows 9 cells", st)
	}
	const want = "t1 0,0*:Header A t1 0,1*:Header B t1 0,2*:Header C " +
		"t1 1,0:Cell A1 t1 1,1:Cell B1 t1 1,2:Cell C1 " +
		"t1 2,0:Cell A2 t1 2,1:Cell B2 t1 2,2:Cell C2"
	if got := grid(d.Pages[0]); got != want {
		t.Errorf("grid =\n%s\nwant\n%s", got, want)
	}
}

// TestTablesNeedsARule is the negative half, and the reason this package reads strokes at
// all: the same nine cells at the same coordinates, with no rules on the page, stay one
// paragraph.
//
// Without this the gate could be a gap threshold and every test above would still pass.
// The gaps here are 10 units against a 10-point size, which is where a threshold would
// fire; the measurement in Tables' comment is that no threshold can, since the ratio of
// gap to space width is continuous over the 117499 inferred spaces on disk.
func TestTablesNeedsARule(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B", "Header C"},
		[]string{"Cell A1", "Cell B1", "Cell C1"},
	)
	d.Pages[0].Rules = nil
	if st := Tables(d, DefaultOptions); st.Tables != 0 {
		t.Fatalf("stats = %+v, want no table without rules", st)
	}
	if n := len(d.Pages[0].Blocks); n != 1 {
		t.Errorf("blocks = %d, want the original 1 left untouched", n)
	}
	if grid(d.Pages[0]) != "" {
		t.Errorf("cells = %s, want none", grid(d.Pages[0]))
	}
}

// TestTablesIgnoresARuleOutsideTheBand pins the second half of the crossing test, which
// is the one a gap-based reader has no equivalent of.
//
// The rule is at the right x and nowhere near the text: a page-wide vertical, a table's
// column rule seen from the paragraph above it, or the frame of a figure elsewhere on the
// page. Over ISO 32000-2, 12167 of 37631 inferred spaces have some vertical rule at their
// x, so without the extent test this would split prose on two pages in three.
func TestTablesIgnoresARuleOutsideTheBand(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	// Same positions, moved well below both rows.
	for i := range d.Pages[0].Rules {
		d.Pages[0].Rules[i].From, d.Pages[0].Rules[i].To = 100, 110
	}
	if st := Tables(d, DefaultOptions); st.Tables != 0 {
		t.Fatalf("stats = %+v, want no table from a rule outside the text band", st)
	}
}

// TestTablesIgnoresARuleAtACellEdge pins the strict-inside half: a rule flush with a
// glyph's edge is a border, which encloses text rather than dividing it.
//
// Both edges, because they are two separate comparisons — the search that finds the first
// candidate rule and the loop bound that stops past the gap — and each admits a border
// from a different side if it loosens. Left as its own test rather than folded into the
// band test above because the failures differ: a border admitted as a divider splits a
// cell's first character off, where a distant rule splits a paragraph.
func TestTablesIgnoresARuleAtACellEdge(t *testing.T) {
	// 140 is the left cell's right edge and 150 the right cell's left edge, so the gap is
	// the open interval between them.
	for _, pos := range []float64{140, 150} {
		d := tableDoc(
			[]string{"Header A", "Header B"},
			[]string{"Cell A1", "Cell B1"},
		)
		for i := range d.Pages[0].Rules {
			d.Pages[0].Rules[i].Pos = pos
		}
		if st := Tables(d, DefaultOptions); st.Tables != 0 {
			t.Errorf("rule at x=%g: stats = %+v, want no table from a rule at a cell edge",
				pos, st)
		}
	}
}

// TestTablesKeepsRaggedRowsTogether pins what replaced the divider key.
//
// The second row populates two of three columns, which is what ISO/TS 32004's key/type
// tables do throughout. Keying a row by the rules that split it read this as two tables;
// clustering cells by x-overlap reads one, and puts the second row's cells in columns 0
// and 2 rather than 0 and 1.
func TestTablesKeepsRaggedRowsTogether(t *testing.T) {
	d := tableDoc(
		[]string{"Key", "Type", "Value"},
		[]string{"Key", "Type", "Value"},
	)
	// Drop the middle cell of row 1, and the rule that would have divided it, leaving the
	// outer pair with one rule between them.
	p := &d.Pages[0]
	p.Blocks[0].Spans = append(p.Blocks[0].Spans[:4], p.Blocks[0].Spans[5])
	p.Rules = append(p.Rules[:2], p.Rules[3])

	st := Tables(d, DefaultOptions)
	if st.Tables != 1 || st.Rows != 2 || st.Cells != 5 {
		t.Fatalf("stats = %+v, want 1 table 2 rows 5 cells", st)
	}
	const want = "t1 0,0*:Key t1 0,1*:Type t1 0,2*:Value t1 1,0:Key t1 1,2:Value"
	if got := grid(*p); got != want {
		t.Errorf("grid =\n%s\nwant\n%s", got, want)
	}
}

// TestTablesIgnoresAZeroLengthRule pins that a rule with no extent divides nothing.
//
// A degenerate stroke marks a point, not an edge. It matters because the band test is an
// overlap and a zero-length rule's From equals its To, so it overlaps every band whose
// range contains that one coordinate — a single stray point would then be read as a column
// boundary for every row it happens to sit level with.
//
// Reachable here and not in extract, which is why the filter lives in both places and is
// tested in only one: extract builds its rules from a content stream and paintPath
// classifies a segment as vertical only when the y delta is not exactly zero, so no stream
// can produce one. This package's input is a doc.Document a caller assembled, so it can.
func TestTablesIgnoresAZeroLengthRule(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	// Same positions inside the gaps, collapsed to the row's top edge.
	for i := range d.Pages[0].Rules {
		y := d.Pages[0].Rules[i].To
		d.Pages[0].Rules[i].From, d.Pages[0].Rules[i].To = y, y
	}
	if st := Tables(d, DefaultOptions); st.Tables != 0 {
		t.Fatalf("stats = %+v, want no table from a zero-length rule", st)
	}
	if n := len(d.Pages[0].Blocks); n != 1 {
		t.Errorf("blocks = %d, want the original 1 left untouched", n)
	}
}

// TestTablesRejectsASingleRow pins the row minimum, which is the difference between the 9
// tables the untagged documents on disk really draw and the 21 runs the gate finds.
//
// One row emits a GFM header and a delimiter with no body, so a line of prose with a rule
// through it — a boxed note, a form field, a page frame — would read as a table of itself.
func TestTablesRejectsASingleRow(t *testing.T) {
	d := tableDoc([]string{"Header A", "Header B", "Header C"})
	if st := Tables(d, DefaultOptions); st.Tables != 0 {
		t.Fatalf("stats = %+v, want no table from one row", st)
	}
	if n := len(d.Pages[0].Blocks); n != 1 {
		t.Errorf("blocks = %d, want the original 1 left untouched", n)
	}
}

// TestTablesJoinsCellsWithNoRuleBetween pins that a rule is required *between* cells and
// not between spans.
//
// A cell holding an italic term is two spans by construction, since extract starts a new
// run at every style change. Requiring a rule between every adjacent pair would read such
// a cell as prose and reject the table — which is a silent failure, since the output would
// be a correct-looking paragraph.
func TestTablesJoinsCellsWithNoRuleBetween(t *testing.T) {
	d := tableDoc(
		[]string{"Key", "Value"},
		[]string{"Key", "Value"},
	)
	p := &d.Pages[0]
	// Split row 1's second cell into two spans at 150-170 and 172-190, no rule between.
	p.Blocks[0].Spans = append(p.Blocks[0].Spans[:3],
		cellSpan("shall ", 150, 170, 680, 690),
		cellSpan("not occur", 172, 190, 680, 690),
	)
	st := Tables(d, DefaultOptions)
	if st.Tables != 1 || st.Cells != 4 {
		t.Fatalf("stats = %+v, want 1 table 4 cells", st)
	}
	const want = "t1 0,0*:Key t1 0,1*:Value t1 1,0:Key t1 1,1:shall not occur"
	if got := grid(*p); got != want {
		t.Errorf("grid =\n%s\nwant\n%s", got, want)
	}
}

// TestTablesKeepsSurroundingText pins the conservation invariant through rebuild: a block
// holding a caption, a table, and a following line loses nothing and reorders nothing.
//
// This is the assertion that matters most for correctness at large, because the failure it
// catches is invisible in the table itself — a rebuild that drops the paragraph before the
// grid emits a perfect table and a document missing a sentence.
func TestTablesKeepsSurroundingText(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	p := &d.Pages[0]
	// A caption above the grid and a note below it, both single spans with no rule.
	p.Blocks[0].Spans = append([]doc.Span{cellSpan("Table 1 — Caption", 100, 300, 720, 730)},
		append(p.Blocks[0].Spans, cellSpan("A note after the table.", 100, 300, 640, 650))...)

	st := Tables(d, DefaultOptions)
	if st.Tables != 1 || st.Cells != 4 {
		t.Fatalf("stats = %+v, want 1 table 4 cells", st)
	}
	var got []string
	for _, b := range p.Blocks {
		got = append(got, strings.TrimSpace(b.Text()))
	}
	want := []string{"Table 1 — Caption", "Header A", "Header B", "Cell A1", "Cell B1",
		"A note after the table."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("blocks = %q, want %q", got, want)
	}
	if r := p.Blocks[0].Role; r != doc.RoleParagraph {
		t.Errorf("caption role = %v, want paragraph", r)
	}
	if p.Blocks[0].Cell != nil {
		t.Errorf("caption carries a cell: %+v", p.Blocks[0].Cell)
	}
}

// TestTablesKeepsSourceOrderAroundATable pins that surrounding prose comes back in the
// order extract emitted it, not in the order this package sorted it into.
//
// The failure is a silent text permutation, which is why it needs its own test: the
// conservation assertions all hold — every span reaches exactly one block — and the table
// itself is perfect. Only the prose either side is scrambled.
//
// The two trailing spans are emitted low-then-high, so their reading order disagrees with
// their y order. bandsOf sorts by descending y to find bands at all, so without the
// index-sorted flush in rebuild the block comes back reversed. Nothing on disk triggers it:
// over the 743 pages that draw rules, 0 blocks have their bands out of y order, so this is
// the only thing anywhere that can catch it — and a reader that trusts reading order for
// footnotes, sidebars, or any producer whose emission order is not top-to-bottom is one
// file away from needing it.
func TestTablesKeepsSourceOrderAroundATable(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	p := &d.Pages[0]
	p.Blocks[0].Spans = append(p.Blocks[0].Spans,
		cellSpan("emitted first, sits lower", 100, 300, 600, 610),
		cellSpan("emitted second, sits higher", 100, 300, 640, 650))

	if st := Tables(d, DefaultOptions); st.Tables != 1 || st.Cells != 4 {
		t.Fatalf("stats = %+v, want 1 table 4 cells", st)
	}
	last := p.Blocks[len(p.Blocks)-1]
	const want = "emitted first, sits loweremitted second, sits higher"
	if got := strings.TrimSpace(last.Text()); got != want {
		t.Errorf("trailing block = %q, want %q — the spans came back in y order", got, want)
	}
}

// TestTablesCapsARunsRows pins the cost guard, which is the only bound on work this pass
// does that the rule cap does not already provide.
//
// group re-clusters a candidate run's whole column set for every row it adds, because a
// column merge is retroactive, so the work is quadratic in the run length. maxRules caps
// rules and does not help: the single page-tall rule here splits every one of these rows,
// so a page of many short lines yields as many two-cell rows as it has lines. Measured
// before the cap, 4000 rows took 0.6s and 16000 took 12.3s from one page of a file the
// caller did not write.
//
// Asserted as two tables rather than a duration, because a timing assertion on a shared CI
// machine is a flake. 600 rows against a 512 cap gives one full table and one of 88 —
// which is also the assertion that reaching the cap *ends* the run rather than dropping
// rows: 600 rows in, 600 rows out.
func TestTablesCapsARunsRows(t *testing.T) {
	const n = 600
	var spans []doc.Span
	y := 700.0
	for i := 0; i < n; i++ {
		spans = append(spans, rowSpans(y, "a", "b")...)
		y -= 20
	}
	d := &doc.Document{Pages: []doc.Page{{
		Number: 1,
		Blocks: []doc.Block{{Role: doc.RoleParagraph, Spans: spans}},
		// One rule spanning every band, which is what makes the row count unbounded.
		Rules: []doc.Rule{vrule(145, y, 710)},
	}}}

	st := Tables(d, DefaultOptions)
	if st.Tables != 2 || st.Rows != n || st.Cells != 2*n {
		t.Errorf("stats = %+v, want 2 tables %d rows %d cells", st, n, 2*n)
	}
}

// TestTablesCapsARunsCells pins the second half of the cost guard, and it exists because
// capping rows alone was measured to be insufficient.
//
// The work is quadratic in the run's cells, not its rows, so either factor drives it on its
// own: 16384 two-cell rows and 512 four-hundred-cell rows are the same hostile page written
// two ways, and TestTablesCapsARunsRows only closes the first. With rows capped and cells
// not, a profile of 512 rows held fixed shows 100 columns at 1.3s, 200 at 4.8s and 400 at
// 14.0s, with agrees at 75% of samples.
//
// 40 rows of 200 cells against a 4096-cell cap gives two runs of 20, which is again the
// assertion that reaching the cap ends the run rather than truncating it: 8000 cells in,
// 8000 out.
func TestTablesCapsARunsCells(t *testing.T) {
	const rows, cols = 40, 200
	var spans []doc.Span
	y := 700.0
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			x0 := 100 + float64(c)*50
			spans = append(spans, cellSpan("x", x0, x0+40, y, y+10))
		}
		y -= 20
	}
	// One rule per column boundary, each spanning every band, so every row splits into
	// cols cells and no row ends the run.
	var rules []doc.Rule
	for c := 1; c < cols; c++ {
		rules = append(rules, vrule(100+float64(c)*50-5, y, 710))
	}
	d := &doc.Document{Pages: []doc.Page{{
		Number: 1,
		Blocks: []doc.Block{{Role: doc.RoleParagraph, Spans: spans}},
		Rules:  rules,
	}}}

	st := Tables(d, DefaultOptions)
	if st.Tables != 2 || st.Rows != rows || st.Cells != rows*cols {
		t.Errorf("stats = %+v, want 2 tables %d rows %d cells", st, rows, rows*cols)
	}
}

// TestTablesSplitsDisagreeingGrids pins the run boundary: two stacked tables of different
// shapes with a caption between them are two tables, numbered separately.
func TestTablesSplitsDisagreeingGrids(t *testing.T) {
	d := tableDoc(
		[]string{"A", "B"},
		[]string{"1", "2"},
	)
	p := &d.Pages[0]
	// A caption, then a two-row table whose columns sit between the first table's.
	p.Blocks[0].Spans = append(p.Blocks[0].Spans, cellSpan("Table 2", 100, 200, 620, 630))
	for _, y := range []float64{600.0, 580.0} {
		p.Blocks[0].Spans = append(p.Blocks[0].Spans,
			cellSpan("x", 125, 140, y, y+10), cellSpan("y", 175, 190, y, y+10))
		p.Rules = append(p.Rules, vrule(160, y, y+10))
	}

	st := Tables(d, DefaultOptions)
	if st.Tables != 2 || st.Rows != 4 || st.Cells != 8 {
		t.Fatalf("stats = %+v, want 2 tables 4 rows 8 cells", st)
	}
	const want = "t1 0,0*:A t1 0,1*:B t1 1,0:1 t1 1,1:2 " +
		"t2 0,0*:x t2 0,1*:y t2 1,0:x t2 1,1:y"
	if got := grid(*p); got != want {
		t.Errorf("grid =\n%s\nwant\n%s", got, want)
	}
}

// TestTablesKeepsRowsApartUnderATallSpan pins how a band's vertical extent is maintained:
// narrowed to the intersection of its members, never widened to their union.
//
// A cell can be taller than its row — an inline fraction, a large initial, a superscript
// reaching above the line — and under a union the band would grow to that span's full
// height and then admit the row below it. Two rows fused into one band is not a wrong
// column count but a missing row, and the table's own text is what disappears.
//
// The tall span here reaches from above row 0 down into row 1's band, which is the only
// arrangement that distinguishes the two rules: intersection keeps three bands where union
// keeps one.
func TestTablesKeepsRowsApartUnderATallSpan(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	p := &d.Pages[0]
	// Row 0 sits at y 700–710 and row 1 at 680–690. This span covers row 0 and reaches
	// into row 1. It joins row 0's second cell rather than becoming a third, because no
	// rule divides it from Header B — that is the rule TestTablesJoinsCellsWithNoRuleBetween
	// pins, and what this test asserts is that row 1 is still a row of its own.
	p.Blocks[0].Spans = append(p.Blocks[0].Spans, cellSpan("x", 260, 270, 688, 715))

	st := Tables(d, DefaultOptions)
	if st.Tables != 1 || st.Rows != 2 || st.Cells != 4 {
		t.Fatalf("stats = %+v, want 1 table 2 rows 4 cells", st)
	}
	const want = "t1 0,0*:Header A t1 0,1*:Header Bx t1 1,0:Cell A1 t1 1,1:Cell B1"
	if got := grid(*p); got != want {
		t.Errorf("grid =\n%s\nwant\n%s", got, want)
	}
}

// TestTablesEndsARunOnADisagreeingRow pins the column-agreement test itself, which is
// what stops a run when nothing separates the rows.
//
// The test above splits on the caption between the two tables — a band that is not a cell
// row ends a run before agreement is ever consulted — so without this case the agreement
// test could return true unconditionally and every other test here would still pass.
//
// The third row's cells straddle the first two rows' columns: its opening cell spans the
// gap the established divider sits in, so clustering all three rows by x-overlap collapses
// both columns into one and the first two rows would then hold two cells in one column.
// That is a different grid, so the run ends and the row stays a paragraph.
func TestTablesEndsARunOnADisagreeingRow(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	p := &d.Pages[0]
	p.Blocks[0].Spans = append(p.Blocks[0].Spans,
		cellSpan("straddles", 130, 160, 660, 670),
		cellSpan("elsewhere", 200, 240, 660, 670))
	p.Rules = append(p.Rules, vrule(180, 660, 670))

	st := Tables(d, DefaultOptions)
	if st.Tables != 1 || st.Rows != 2 || st.Cells != 4 {
		t.Fatalf("stats = %+v, want 1 table 2 rows 4 cells", st)
	}
	const want = "t1 0,0*:Header A t1 0,1*:Header B t1 1,0:Cell A1 t1 1,1:Cell B1"
	if got := grid(*p); got != want {
		t.Errorf("grid =\n%s\nwant\n%s", got, want)
	}
	last := p.Blocks[len(p.Blocks)-1]
	if last.Role != doc.RoleParagraph || last.Cell != nil {
		t.Errorf("trailing row: role %v cell %+v, want paragraph with no cell",
			last.Role, last.Cell)
	}
	if got := strings.TrimSpace(last.Text()); got != "straddleselsewhere" {
		t.Errorf("trailing row text = %q, want both spans kept", got)
	}
}

// TestTablesLeavesNonParagraphsAlone pins that a declared role is never overwritten.
//
// An artifact is a running header or a folio, and a page frame is a vertical rule at the
// right x for its columns. Tables runs before Headings and Lists on the untagged path, so
// a paragraph is everything not already classified — and everything already classified was
// classified on better evidence than a stroke.
func TestTablesLeavesNonParagraphsAlone(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	d.Pages[0].Blocks[0].Role = doc.RoleArtifact
	if st := Tables(d, DefaultOptions); st.Tables != 0 {
		t.Fatalf("stats = %+v, want no table from an artifact", st)
	}
	if n := len(d.Pages[0].Blocks); n != 1 {
		t.Errorf("blocks = %d, want the original 1 left untouched", n)
	}
}

// TestTablesFindsRowsAcrossBlocks pins that a table split across blocks is one table.
//
// Each row arriving as its own block is not hypothetical: pymupdf/dotted-gridlines.pdf
// sets its header cells as twelve separate single-span blocks, and a producer that emits
// one BT/ET per row gives the extractor no reason to join them. Grouping per block would
// find nothing at all there.
func TestTablesFindsRowsAcrossBlocks(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	p := &d.Pages[0]
	sp := p.Blocks[0].Spans
	p.Blocks = []doc.Block{
		{Role: doc.RoleParagraph, Spans: sp[:2]},
		{Role: doc.RoleParagraph, Spans: sp[2:]},
	}
	st := Tables(d, DefaultOptions)
	if st.Tables != 1 || st.Rows != 2 || st.Cells != 4 {
		t.Fatalf("stats = %+v, want 1 table 2 rows 4 cells", st)
	}
	const want = "t1 0,0*:Header A t1 0,1*:Header B t1 1,0:Cell A1 t1 1,1:Cell B1"
	if got := grid(*p); got != want {
		t.Errorf("grid =\n%s\nwant\n%s", got, want)
	}
}

// TestTablesNumbersTablesAcrossPages pins that the table number is document-scoped.
//
// doc.Cell.Table is what the markdown sink groups by, and it groups within a page — but a
// number reused on page 2 would collide for any consumer that walks the document, which
// the OKF sink does.
func TestTablesNumbersTablesAcrossPages(t *testing.T) {
	a := tableDoc([]string{"A", "B"}, []string{"1", "2"})
	b := tableDoc([]string{"C", "D"}, []string{"3", "4"})
	b.Pages[0].Number = 2
	d := &doc.Document{Pages: []doc.Page{a.Pages[0], b.Pages[0]}}

	if st := Tables(d, DefaultOptions); st.Tables != 2 {
		t.Fatalf("stats = %+v, want 2 tables", st)
	}
	for i, want := range []int{1, 2} {
		for _, blk := range d.Pages[i].Blocks {
			if blk.Cell != nil && blk.Cell.Table != want {
				t.Errorf("page %d cell %q table = %d, want %d",
					d.Pages[i].Number, blk.Text(), blk.Cell.Table, want)
			}
		}
	}
}

// TestTablesOnNilDocument pins that the nil guard holds, since md calls this before
// anything has validated the document.
func TestTablesOnNilDocument(t *testing.T) {
	if st := Tables(nil, DefaultOptions); st.Tables != 0 {
		t.Fatalf("stats = %+v, want zero", st)
	}
}

// A cell's MCIDs are the union of its own spans', not the row's.
//
// The block a cell is cut from is a struct copy of the row block, so it arrives holding the
// row's whole set — every cell would claim every identifier, and the field exists to answer
// which MCIDs went where. Nothing downstream reads it, so the wrong answer is a diagnostic
// that lies rather than wrong output, which is exactly why no other test here can catch it:
// every fixture above draws its spans at MCID -1, where a union and a row's set are both
// empty and the call is unobservable.
func TestCellMCIDsAreTheCellsOwn(t *testing.T) {
	d := tableDoc(
		[]string{"Header A", "Header B"},
		[]string{"Cell A1", "Cell B1"},
	)
	p := &d.Pages[0]
	// One identifier per cell, in reading order, and a repeat within the last cell so a
	// missing dedup is visible as well as a missing filter.
	for i := range p.Blocks[0].Spans {
		p.Blocks[0].Spans[i].MCID = i
	}
	p.Blocks[0].Spans = append(p.Blocks[0].Spans,
		cellSpan(" (again)", 150, 190, 680, 690))
	p.Blocks[0].Spans[len(p.Blocks[0].Spans)-1].MCID = 3
	p.Blocks[0].SetMCIDs()

	if st := Tables(d, DefaultOptions); st.Cells != 4 {
		t.Fatalf("stats = %+v, want 4 cells", st)
	}
	var got []string
	for _, b := range p.Blocks {
		if b.Cell == nil {
			continue
		}
		got = append(got, fmt.Sprintf("%d,%d:%v", b.Cell.Row, b.Cell.Col, b.MCIDs))
	}
	want := "0,0:[0] 0,1:[1] 1,0:[2] 1,1:[3]"
	if strings.Join(got, " ") != want {
		t.Errorf("cell MCIDs = %q, want %q", strings.Join(got, " "), want)
	}
}

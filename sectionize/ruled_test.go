package sectionize

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/tag"
)

// headerRule is Word's rule under a header row of three cells at y: each cell's bottom border
// and a quarter-point joint where two meet, as extract.RuledHeaders reads it.
func headerRule(y float64) []doc.Rule {
	h := func(x0, x1 float64) doc.Rule { return doc.Rule{Pos: y, From: x0, To: x1} }
	return []doc.Rule{h(66, 140), h(140, 140.25), h(140.25, 244), h(244, 244.25), h(244.25, 545)}
}

// on is at, on page n.
func on(n, mcid int, text string, x0, y0, x1 float64) doc.Span {
	s := at(mcid, text, x0, y0, x1)
	s.Page = n
	return s
}

// pages is a document of pages, each a block of spans drawn under a header rule at 600.
func pages(spans ...[]doc.Span) *doc.Document {
	d := &doc.Document{Meta: doc.Metadata{Tagged: true}}
	for i, ss := range spans {
		p := doc.Page{Number: i + 1, Box: geom.NewRect(0, 0, 612, 792), Rules: headerRule(600)}
		blk := doc.Block{Role: doc.RoleParagraph, Spans: ss}
		for _, s := range ss {
			blk.Box = blk.Box.Union(s.Box)
		}
		blk.SetMCIDs()
		p.Blocks = []doc.Block{blk}
		d.Pages = append(d.Pages, p)
	}
	return d
}

// cells writes blocks as "role:row,col,header:text", or "role:text" for a block that is no cell.
func cells(blocks []doc.Block) []string {
	var out []string
	for _, b := range blocks {
		if b.Cell == nil {
			out = append(out, fmt.Sprintf("%s:%s", b.Role, strings.TrimSpace(b.Text())))
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d,%d,%v:%s", b.Role, b.Cell.Row, b.Cell.Col, b.Cell.Header, strings.TrimSpace(b.Text())))
	}
	return out
}

// ps is a P per MCID, on page n.
func ps(n int, mcids ...int) []*tag.Elem {
	var out []*tag.Elem
	for _, m := range mcids {
		out = append(out, el(tag.RoleP, n, m))
	}
	return out
}

func sameCells(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got  %q\n want %q", name, got, want)
	}
}

// TestParagraphsUnderARuledHeaderAreATable: cells Word declared as bare P, one per cell in row
// order, under a header rule drawn cell by cell, are a table (ADR 0055): the row above the rule
// its header, a wrapped cell one cell, and a new row wherever a cell's column does not advance;
// a caption above the header and a paragraph after the table, each across the columns or clear
// of them, are no cells.
func TestParagraphsUnderARuledHeaderAreATable(t *testing.T) {
	d := pages([]doc.Span{
		on(1, 1, "Key fields:", 72, 640, 140),
		on(1, 2, "Field", 72, 604, 100), on(1, 3, "Required", 146, 604, 190), on(1, 4, "Description", 250, 604, 320),
		on(1, 5, "Confidence", 72, 580, 130), on(1, 6, "Recommended", 146, 580, 230),
		on(1, 7, "How sure the crew ", 250, 580, 400), on(1, 7, "is of the count", 250, 568, 380),
		on(1, 8, "Timestamp", 72, 540, 130), on(1, 9, "Yes", 146, 540, 170), on(1, 10, "When the sailing left", 250, 540, 420),
		on(1, 11, "The records are kept for a year, and the office reads them once a day.", 72, 500, 540),
	})
	tr := tree(append(ps(1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10), el(tag.RoleP, 1, 11))...)
	out, _ := Tagged(d, tr, DefaultOptions)
	sameCells(t, "a table", cells(out.Preamble), []string{
		"paragraph:Key fields:",
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Required", "table_cell:0,2,true:Description",
		"table_cell:1,0,false:Confidence", "table_cell:1,1,false:Recommended", "table_cell:1,2,false:How sure the crew is of the count",
		"table_cell:2,0,false:Timestamp", "table_cell:2,1,false:Yes", "table_cell:2,2,false:When the sailing left",
		"paragraph:The records are kept for a year, and the office reads them once a day.",
	})
}

// TestARuledTableRunsOnAcrossAPage: a table whose next page repeats its header and rule is one
// table, the repeated header dropped, and so is one whose first page holds only its header.
func TestARuledTableRunsOnAcrossAPage(t *testing.T) {
	head := func(n, m int) []doc.Span {
		return []doc.Span{on(n, m, "Field", 72, 604, 100), on(n, m+1, "Required", 146, 604, 190), on(n, m+2, "Description", 250, 604, 320)}
	}
	row := func(n, m int, a string) []doc.Span {
		return []doc.Span{on(n, m, a, 72, 580, 130), on(n, m+1, "Yes", 146, 580, 170), on(n, m+2, "Kept", 250, 580, 300)}
	}
	d := pages(append(head(1, 1), row(1, 4, "Confidence")...), append(head(2, 1), row(2, 4, "Timestamp")...))
	tr := tree(append(ps(1, 1, 2, 3, 4, 5, 6), ps(2, 1, 2, 3, 4, 5, 6)...)...)
	out, _ := Tagged(d, tr, DefaultOptions)
	sameCells(t, "a header repeated", cells(out.Preamble), []string{
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Required", "table_cell:0,2,true:Description",
		"table_cell:1,0,false:Confidence", "table_cell:1,1,false:Yes", "table_cell:1,2,false:Kept",
		"table_cell:2,0,false:Timestamp", "table_cell:2,1,false:Yes", "table_cell:2,2,false:Kept",
	})
	if first, last := out.Preamble[0].Cell, out.Preamble[len(out.Preamble)-1].Cell; first != nil && last != nil && first.Table != last.Table {
		t.Errorf("tables %d and %d, want one", first.Table, last.Table)
	}

	// The next page's rule may sit lower than the last page's rows: its first row is the
	// table's however far below them it is.
	d = pages(append(head(1, 1), row(1, 4, "Confidence")...), nil)
	d.Pages[1].Rules = headerRule(400)
	d.Pages[1].Blocks = pages(append(head(2, 1), row(2, 4, "Timestamp")...)).Pages[0].Blocks
	for i := range d.Pages[1].Blocks[0].Spans {
		d.Pages[1].Blocks[0].Spans[i].Box.Y0 -= 200
		d.Pages[1].Blocks[0].Spans[i].Box.Y1 -= 200
	}
	d.Pages[1].Blocks[0].Box = geom.Rect{}
	for _, s := range d.Pages[1].Blocks[0].Spans {
		d.Pages[1].Blocks[0].Box = d.Pages[1].Blocks[0].Box.Union(s.Box)
	}
	out, _ = Tagged(d, tr, DefaultOptions)
	sameCells(t, "a rule lower on the next page", cells(out.Preamble), []string{
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Required", "table_cell:0,2,true:Description",
		"table_cell:1,0,false:Confidence", "table_cell:1,1,false:Yes", "table_cell:1,2,false:Kept",
		"table_cell:2,0,false:Timestamp", "table_cell:2,1,false:Yes", "table_cell:2,2,false:Kept",
	})

	d = pages(head(1, 1), append(head(2, 1), row(2, 4, "Timestamp")...))
	tr = tree(append(ps(1, 1, 2, 3), ps(2, 1, 2, 3, 4, 5, 6)...)...)
	out, _ = Tagged(d, tr, DefaultOptions)
	sameCells(t, "a header alone at a page's foot", cells(out.Preamble), []string{
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Required", "table_cell:0,2,true:Description",
		"table_cell:1,0,false:Timestamp", "table_cell:1,1,false:Yes", "table_cell:1,2,false:Kept",
	})
}

// TestARuledHeaderMarksATaggedTablesHeader: a table Word tagged with no TH, its header row ruled
// cell by cell, has that row as its header; one ruled under every row, a grid, is left as declared.
func TestARuledHeaderMarksATaggedTablesHeader(t *testing.T) {
	spans := []doc.Span{
		on(1, 1, "Field", 72, 604, 100), on(1, 2, "Required", 146, 604, 190),
		on(1, 3, "Confidence", 72, 580, 130), on(1, 4, "Yes", 146, 580, 170),
	}
	table := func() *tag.Elem {
		return kids(el(tag.RoleTable, 1),
			kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 1), el(tag.RoleTD, 1, 2)),
			kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 3), el(tag.RoleTD, 1, 4)))
	}
	out, _ := Tagged(pages(spans), tree(table()), DefaultOptions)
	sameCells(t, "a ruled header", cells(out.Preamble), []string{
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Required",
		"table_cell:1,0,false:Confidence", "table_cell:1,1,false:Yes",
	})

	grid := pages(spans)
	grid.Pages[0].Rules = append(grid.Pages[0].Rules, headerRule(576)...)
	out, _ = Tagged(grid, tree(table()), DefaultOptions)
	sameCells(t, "a grid", cells(out.Preamble), []string{
		"table_cell:0,0,false:Field", "table_cell:0,1,false:Required",
		"table_cell:1,0,false:Confidence", "table_cell:1,1,false:Yes",
	})
}

// TestARuledTableEndsWhereItsRowsStop: the paragraph after a table that sits in its first column
// is no row of it: rows follow one another a cell's padding apart, as far as the first row sits
// under the header, and a paragraph after the table sits further off, here 1.75 times as far.
func TestARuledTableEndsWhereItsRowsStop(t *testing.T) {
	d := pages([]doc.Span{
		on(1, 1, "Field", 72, 604, 100), on(1, 2, "Required", 146, 604, 190),
		on(1, 3, "Confidence", 72, 588, 130), on(1, 4, "Yes", 146, 588, 170),
		on(1, 5, "Timestamp", 72, 572, 130), on(1, 6, "Yes", 146, 572, 170),
		on(1, 7, "Key observations:", 72, 551.5, 130),
	})
	out, _ := Tagged(d, tree(ps(1, 1, 2, 3, 4, 5, 6, 7)...), DefaultOptions)
	sameCells(t, "the paragraph after", cells(out.Preamble), []string{
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Required",
		"table_cell:1,0,false:Confidence", "table_cell:1,1,false:Yes",
		"table_cell:2,0,false:Timestamp", "table_cell:2,1,false:Yes",
		"paragraph:Key observations:",
	})
}

// TestAHeaderCarriesItsRepeat: Word's header row repeated at the top of the next page is more
// marked content of the same header P's, and the table's rows run on under it; the repeat is
// dropped, as layout drops a repeated header, and the rows are one table's.
func TestAHeaderCarriesItsRepeat(t *testing.T) {
	d := pages(
		[]doc.Span{on(1, 1, "Command ", 72, 604, 120), on(1, 2, "Function ", 146, 604, 196), on(1, 3, "stats", 72, 588, 110), on(1, 4, "Display them", 146, 588, 230)},
		[]doc.Span{on(2, 1, "Command ", 72, 604, 120), on(2, 2, "Function ", 146, 604, 196), on(2, 3, "blame", 72, 588, 110), on(2, 4, "Attribute them", 146, 588, 230),
			on(2, 5, "The records are kept for a year, and the office reads them once a day.", 72, 540, 540)},
	)
	head := func(m int) *tag.Elem {
		e := el(tag.RoleP, 1, m)
		e.Content = append(e.Content, tag.MCRef{MCID: m, Page: 2})
		return e
	}
	tr := tree(head(1), head(2), el(tag.RoleP, 1, 3), el(tag.RoleP, 1, 4), el(tag.RoleP, 2, 3), el(tag.RoleP, 2, 4), el(tag.RoleP, 2, 5))
	out, _ := Tagged(d, tr, DefaultOptions)
	sameCells(t, "a repeat in the header", cells(out.Preamble), []string{
		"table_cell:0,0,true:Command", "table_cell:0,1,true:Function",
		"table_cell:1,0,false:stats", "table_cell:1,1,false:Display them",
		"table_cell:2,0,false:blame", "table_cell:2,1,false:Attribute them",
		"paragraph:The records are kept for a year, and the office reads them once a day.",
	})
}

// TestARowRunsOnAcrossAPage: a row whose cells break across a page break, each one P with marked
// content on both pages, is one row, and the table runs on under the next page's repeated header.
func TestARowRunsOnAcrossAPage(t *testing.T) {
	d := pages(
		[]doc.Span{on(1, 1, "Component ", 72, 604, 130), on(1, 2, "Extension ", 146, 604, 200),
			on(1, 3, "Operational ", 72, 588, 130), on(1, 4, "Inference endpoints, access ", 146, 588, 240)},
		[]doc.Span{on(2, 1, "Component ", 72, 604, 130), on(2, 2, "Extension ", 146, 604, 200),
			on(2, 3, "metadata", 72, 588, 120), on(2, 4, "controls, monitoring hooks", 146, 588, 240),
			on(2, 5, "Ownership", 72, 572, 120), on(2, 6, "Human reviewers", 146, 572, 240)},
	)
	both := func(m int) *tag.Elem {
		e := el(tag.RoleP, 1, m)
		e.Content = append(e.Content, tag.MCRef{MCID: m, Page: 2})
		return e
	}
	tr := tree(both(1), both(2), both(3), both(4), el(tag.RoleP, 2, 5), el(tag.RoleP, 2, 6))
	out, _ := Tagged(d, tr, DefaultOptions)
	sameCells(t, "a row across a page", cells(out.Preamble), []string{
		"table_cell:0,0,true:Component", "table_cell:0,1,true:Extension",
		"table_cell:1,0,false:Operational metadata", "table_cell:1,1,false:Inference endpoints, access controls, monitoring hooks",
		"table_cell:2,0,false:Ownership", "table_cell:2,1,false:Human reviewers",
	})
}

// tableOf is the table number of each cell among blocks, in order.
func tableOf(blocks []doc.Block) []int {
	var out []int
	for _, b := range blocks {
		if b.Cell != nil {
			out = append(out, b.Cell.Table)
		}
	}
	return out
}

// TestNoHeaderNoTable: a rule with one paragraph over it, or none, heads no table: a header is two
// or more cells above the rule, left to right.
func TestNoHeaderNoTable(t *testing.T) {
	check := noCells(t)
	for name, spans := range map[string][]doc.Span{
		"one paragraph over the rule": {on(1, 1, "Field", 72, 604, 100), on(1, 2, "Confidence", 72, 580, 130), on(1, 3, "Timestamp", 72, 564, 130)},
		"none over it":                {on(1, 1, "Confidence", 72, 580, 130), on(1, 2, "Yes", 146, 580, 170)},
		"a caption well above it":     {on(1, 1, "Key fields", 72, 660, 130), on(1, 2, "as kept", 146, 660, 190), on(1, 3, "Confidence", 72, 580, 130), on(1, 4, "Yes", 146, 580, 170)},
	} {
		var mcids []int
		for _, s := range spans {
			mcids = append(mcids, s.MCID)
		}
		out, _ := Tagged(pages(spans), tree(ps(1, mcids...)...), DefaultOptions)
		check(name, out)
	}
	// A paragraph on the rule whose marked content runs on to another page as other text is no
	// header cell: only a repeat of the header says the same there.
	d := pages([]doc.Span{on(1, 1, "Field", 72, 604, 100), on(1, 2, "Required", 146, 604, 190), on(1, 3, "Confidence", 72, 580, 130), on(1, 4, "Yes", 146, 580, 170)},
		[]doc.Span{on(2, 1, "and the rest of it", 72, 700, 300)})
	e := el(tag.RoleP, 1, 1)
	e.Content = append(e.Content, tag.MCRef{MCID: 1, Page: 2})
	out, _ := Tagged(d, tree(e, el(tag.RoleP, 1, 2), el(tag.RoleP, 1, 3), el(tag.RoleP, 1, 4)), DefaultOptions)
	check("a paragraph that runs on as other text", out)
}

// noCells fails t for each cell among out's blocks.
func noCells(t *testing.T) func(string, *doc.Outline) {
	return func(name string, out *doc.Outline) {
		t.Helper()
		for _, b := range out.Preamble {
			if b.Cell != nil {
				t.Errorf("%s: %q is a cell, want no table", name, b.Text())
			}
		}
	}
}

// TestAHeaderTakesEachColumnOnce: a paragraph on the rule after the header's last cell, in a
// column the header has passed, is no header cell, and no row: the table is its header alone.
func TestAHeaderTakesEachColumnOnce(t *testing.T) {
	d := pages([]doc.Span{on(1, 1, "Field", 72, 604, 100), on(1, 2, "Required", 146, 604, 190), on(1, 3, "Notes", 72, 604, 110)})
	out, _ := Tagged(d, tree(ps(1, 1, 2, 3)...), DefaultOptions)
	sameCells(t, "a column taken twice", cells(out.Preamble), []string{
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Required", "paragraph:Notes",
	})
}

// TestARowMayLeaveACellEmpty: a row with no cell in a column is a row all the same; the next row
// starts where a cell's column does not advance, though it is the column the last cell was in.
func TestARowMayLeaveACellEmpty(t *testing.T) {
	d := pages([]doc.Span{
		on(1, 1, "Field", 72, 604, 100), on(1, 2, "Value", 146, 604, 190),
		on(1, 3, "Confidence", 72, 588, 130), on(1, 4, "Yes", 146, 588, 170),
		on(1, 5, "Timestamp", 72, 572, 130),
		on(1, 6, "Port", 72, 556, 130), on(1, 7, "Oban", 146, 556, 170),
	})
	out, _ := Tagged(d, tree(ps(1, 1, 2, 3, 4, 5, 6, 7)...), DefaultOptions)
	sameCells(t, "an empty cell", cells(out.Preamble), []string{
		"table_cell:0,0,true:Field", "table_cell:0,1,true:Value",
		"table_cell:1,0,false:Confidence", "table_cell:1,1,false:Yes",
		"table_cell:2,0,false:Timestamp",
		"table_cell:3,0,false:Port", "table_cell:3,1,false:Oban",
	})
}

// TestADifferentHeaderStartsATable: a next page that heads its columns with other names, under a
// rule with the same columns, starts a table of its own; so does one whose rule has other columns.
func TestADifferentHeaderStartsATable(t *testing.T) {
	page := func(n int, a, b string) []doc.Span {
		return []doc.Span{on(n, 1, a, 82, 604, 120), on(n, 2, b, 146, 604, 190), on(n, 3, "Oban", 82, 588, 120), on(n, 4, "Yes", 146, 588, 170)}
	}
	for _, c := range []struct {
		name  string
		names [2]string
		rules []doc.Rule
	}{
		{"other names", [2]string{"Port", "Crew"}, nil},
		{"the first column moved", [2]string{"Field", "Required"}, []doc.Rule{
			{Pos: 600, From: 80, To: 140}, {Pos: 600, From: 140, To: 140.25}, {Pos: 600, From: 140.25, To: 244},
			{Pos: 600, From: 244, To: 244.25}, {Pos: 600, From: 244.25, To: 545}}},
		{"a column fewer", [2]string{"Field", "Required"}, []doc.Rule{
			{Pos: 600, From: 66, To: 140}, {Pos: 600, From: 140, To: 140.25}, {Pos: 600, From: 140.25, To: 244}}},
	} {
		d := pages(page(1, "Field", "Required"), page(2, c.names[0], c.names[1]))
		if c.rules != nil {
			d.Pages[1].Rules = c.rules
		}
		out, _ := Tagged(d, tree(append(ps(1, 1, 2, 3, 4), ps(2, 1, 2, 3, 4)...)...), DefaultOptions)
		sameCells(t, c.name, cells(out.Preamble), []string{
			"table_cell:0,0,true:Field", "table_cell:0,1,true:Required", "table_cell:1,0,false:Oban", "table_cell:1,1,false:Yes",
			"table_cell:0,0,true:" + c.names[0], "table_cell:0,1,true:" + c.names[1], "table_cell:1,0,false:Oban", "table_cell:1,1,false:Yes",
		})
		if ts := tableOf(out.Preamble); len(ts) == 8 && ts[0] == ts[7] {
			t.Errorf("%s: one table %d, want two", c.name, ts[0])
		}
	}
}

// TestARunOnCellIsInItsTable: a cell whose marked content runs on past the next page, or onto the
// next page above its rule, is no row of a table that breaks there.
func TestARunOnCellIsInItsTable(t *testing.T) {
	head := []doc.Span{on(1, 1, "Field", 72, 604, 100), on(1, 2, "Required", 146, 604, 190)}
	for name, rest := range map[string]doc.Span{
		"onto a page after the next": on(3, 3, "metadata", 72, 588, 120),
		"above the next page's rule": on(2, 3, "metadata", 72, 620, 120),
		"in another column there":    on(2, 3, "metadata", 146, 588, 190),
	} {
		cell := []doc.Span{on(1, 3, "Operational ", 72, 588, 130), rest}
		d := pages(append(append([]doc.Span{}, head...), cell...), nil, nil)
		e := el(tag.RoleP, 1, 3)
		e.Content = append(e.Content, tag.MCRef{MCID: 3, Page: rest.Page})
		out, _ := Tagged(d, tree(el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 2), e), DefaultOptions)
		sameCells(t, name, cells(out.Preamble), []string{
			"table_cell:0,0,true:Field", "table_cell:0,1,true:Required", "paragraph:Operational metadata",
		})
	}
}

// TestRuledTablesAreNumberedAfterDeclaredOnes: a ruled table takes a number no declared table
// has.
func TestRuledTablesAreNumberedAfterDeclaredOnes(t *testing.T) {
	d := pages(
		[]doc.Span{on(1, 1, "Name", 72, 404, 100), on(1, 2, "Kind", 146, 404, 190), on(1, 3, "Oban", 72, 388, 120), on(1, 4, "Port", 146, 388, 170)},
		[]doc.Span{on(2, 1, "Field", 72, 604, 100), on(2, 2, "Required", 146, 604, 190), on(2, 3, "Oban", 72, 588, 120), on(2, 4, "Yes", 146, 588, 170)},
	)
	d.Pages[0].Rules = nil
	declared := kids(el(tag.RoleTable, 1),
		kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 1), el(tag.RoleTD, 1, 2)),
		kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 3), el(tag.RoleTD, 1, 4)))
	out, _ := Tagged(d, tree(append([]*tag.Elem{declared}, ps(2, 1, 2, 3, 4)...)...), DefaultOptions)
	if ts := tableOf(out.Preamble); len(ts) != 8 || ts[0] == ts[7] {
		t.Errorf("tables %v, want two numbers", ts)
	}
}

// TestARuledHeaderMarksOnlyARowOverIt: a declared table's first row is its header by the rule
// only where it is two or more cells, none declared a header already, sitting over the rule.
func TestARuledHeaderMarksOnlyARowOverIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		spans []doc.Span
		first []*tag.Elem
		want  []string
	}{
		{"a row of one cell", []doc.Span{on(1, 1, "Sailings", 72, 604, 130), on(1, 3, "Oban", 72, 580, 130), on(1, 4, "Yes", 146, 580, 170)},
			[]*tag.Elem{el(tag.RoleTD, 1, 1)}, []string{"table_cell:0,0,false:Sailings", "table_cell:1,0,false:Oban", "table_cell:1,1,false:Yes"}},
		{"a row with a header declared", []doc.Span{on(1, 1, "Field", 72, 604, 100), on(1, 2, "Required", 146, 604, 190), on(1, 3, "Oban", 72, 580, 130), on(1, 4, "Yes", 146, 580, 170)},
			[]*tag.Elem{el(tag.RoleTH, 1, 1), el(tag.RoleTD, 1, 2)}, []string{"table_cell:0,0,true:Field", "table_cell:0,1,false:Required", "table_cell:1,0,false:Oban", "table_cell:1,1,false:Yes"}},
		{"a row far from the rule", []doc.Span{on(1, 1, "Field", 72, 404, 100), on(1, 2, "Required", 146, 404, 190), on(1, 3, "Oban", 72, 380, 130), on(1, 4, "Yes", 146, 380, 170)},
			[]*tag.Elem{el(tag.RoleTD, 1, 1), el(tag.RoleTD, 1, 2)}, []string{"table_cell:0,0,false:Field", "table_cell:0,1,false:Required", "table_cell:1,0,false:Oban", "table_cell:1,1,false:Yes"}},
	} {
		table := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), c.first...),
			kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 3), el(tag.RoleTD, 1, 4)))
		out, _ := Tagged(pages(c.spans), tree(table), DefaultOptions)
		sameCells(t, c.name, cells(out.Preamble), c.want)
	}
}

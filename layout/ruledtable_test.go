package layout

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// wordRules is Word's rule under a header of three cells at y: each cell's bottom border and a
// half-point joint where two meet.
func wordRules(y float64) []doc.Rule {
	return []doc.Rule{hrule(y, 70, 120), hrule(y, 120, 120.5), hrule(y, 120.5, 210), hrule(y, 210, 210.5), hrule(y, 210.5, 400)}
}

// TestARuledHeaderReadsItsTable: an untagged table whose header the page rules cell by cell, its
// cells split apart by extract, is a table (ADR 0056): the spans over the rule its header, a
// wrapped cell one cell, a new row where cells' tops line up below the last, and the table ended
// where a row would start further below the last than the first starts below the header, or by a
// paragraph across its columns.
func TestARuledHeaderReadsItsTable(t *testing.T) {
	// The header's foot 12 points above its rule, as Word's cell padding sets it; rows 16 apart.
	d := latticePage(wordRules(588),
		[]doc.Span{put("Field", 75, 600), put("Required", 125, 600), put("Description", 215, 600)},
		[]doc.Span{put("Confidence", 75, 574)},
		[]doc.Span{put("Recommended", 125, 574)},
		[]doc.Span{put("How sure the crew ", 215, 574), put("is of the count", 215, 562)},
		[]doc.Span{put("Timestamp", 75, 536)},
		[]doc.Span{put("Yes", 125, 536), put("When it left", 215, 536)},
		[]doc.Span{put("Key observations:", 75, 500)},
		[]doc.Span{cellSpan("The records are kept for a year.", 72, 300, 480, 490)})
	st := Tables(d, DefaultOptions)
	want := "0,0*:Field 0,1*:Required 0,2*:Description 1,0:Confidence 1,1:Recommended " +
		"1,2:How sure the crew is of the count 2,0:Timestamp 2,1:Yes 2,2:When it left"
	if got := cells(d.Pages[0]); got != want {
		t.Errorf("cells = %s\nwant    %s", got, want)
	}
	if st.Tables != 1 || st.Rows != 3 || st.Cells != 9 {
		t.Errorf("stats = %+v", st)
	}
	var paras []string
	for _, b := range d.Pages[0].Blocks {
		if b.Role == doc.RoleParagraph {
			paras = append(paras, b.Text())
		}
	}
	if len(paras) != 2 || paras[0] != "Key observations:" || paras[1] != "The records are kept for a year." {
		t.Errorf("paragraphs = %q, want the two after the table", paras)
	}
}

// ruledRun reads blocks under wordRules(588), the header 12 points above as Word sets it, and
// returns the cells, the stats, and the blocks after.
func ruledRun(rules []doc.Rule, blocks ...[]doc.Span) (string, TableStats, []doc.Block) {
	d := latticePage(rules, blocks...)
	st := Tables(d, DefaultOptions)
	return cells(d.Pages[0]), st, d.Pages[0].Blocks
}

// head3 is the header row over wordRules(588).
func head3() []doc.Span {
	return []doc.Span{put("Field", 75, 600), put("Required", 125, 600), put("Description", 215, 600)}
}

// TestARuledTableKeepsItsBounds: the table's header is two or more cells on the nearest line above
// its rule; its rows run while each starts within half as far again below the last as the first
// starts below the header, and before any span across its columns; and it takes no span beside it,
// none another reader took, none that is blank or no paragraph's, and one row's cells though their
// tops differ by less than half a line.
func TestARuledTableKeepsItsBounds(t *testing.T) {
	rules := wordRules(588)
	header := "0,0*:Field 0,1*:Required 0,2*:Description"
	// A row's top 16 points below the foot of the row above, as the first row's is below the header's.
	r1 := []doc.Span{put("Port", 75, 574), put("Yes", 125, 574), put("Oban", 215, 574)}
	r2 := []doc.Span{put("Crew", 75, 548), put("No", 125, 548), put("Four", 215, 548)}
	row1, row2 := " 1,0:Port 1,1:Yes 1,2:Oban", " 2,0:Crew 2,1:No 2,2:Four"
	for _, c := range []struct {
		name   string
		blocks [][]doc.Span
		want   string
		rows   int
	}{
		{"a gap 1.75 times the pitch ends it", [][]doc.Span{head3(), r1, r2, {put("Key notes", 75, 510)}}, header + row1 + row2, 3},
		{"a gap 1.35 times the pitch is a row", [][]doc.Span{head3(), r1, r2, {put("Mull", 75, 516.4)}}, header + row1 + row2 + " 3,0:Mull", 4},
		{"a header of one cell", [][]doc.Span{{put("Field", 75, 600)}, r1, r2}, "", 0},
		{"a header alone", [][]doc.Span{head3()}, "", 0},
		{"a caption above, out of reach", [][]doc.Span{{put("Key", 75, 640)}, head3(), r1}, header + row1, 2},
		{"a caption over the header, within reach", [][]doc.Span{{put("Key", 75, 604)},
			{put("Field", 75, 590), put("Required", 125, 590), put("Description", 215, 590)}, r1}, header + row1, 2},
		{"a paragraph across the columns, a cell after it, and another", [][]doc.Span{head3(), r1,
			{cellSpan("The records are kept for a year.", 72, 300, 562, 568)}, {put("Mull", 75, 546)},
			{cellSpan("And read once a day by the office.", 72, 300, 500, 510)}}, header + row1, 2},
		{"a blank span between rows", [][]doc.Span{head3(), r1, {put("   ", 125, 560)}, r2}, header + row1 + row2, 3},
		{"a span beside the table", [][]doc.Span{head3(), r1, {put("Margin note", 420, 560)}, r2}, header + row1 + row2, 3},
		{"row tops two points apart", [][]doc.Span{head3(),
			{put("Port", 75, 574), put("Yes", 125, 572), put("Oban", 215, 574)}, r2}, header + row1 + row2, 3},
		{"a wrapped cell first in page order", [][]doc.Span{head3(),
			{put("How sure the crew ", 215, 574), put("is of it", 215, 562)}, {put("Port", 75, 574)}, {put("Yes", 125, 574)},
			{put("Crew", 75, 536)}}, header + " 1,0:Port 1,1:Yes 1,2:How sure the crew is of it 2,0:Crew", 3},
	} {
		got, st, _ := ruledRun(rules, c.blocks...)
		if got != c.want || st.Rows != c.rows {
			t.Errorf("%s: cells %s, %d rows\nwant         %s, %d rows", c.name, got, st.Rows, c.want, c.rows)
		}
	}

	// An artifact in a column is the page's, not the table's.
	d := latticePage(rules, head3(), r1, r2)
	d.Pages[0].Blocks = append(d.Pages[0].Blocks[:2], append([]doc.Block{{Role: doc.RoleArtifact, Spans: []doc.Span{put("Page 3", 75, 560)}}}, d.Pages[0].Blocks[2:]...)...)
	if st := Tables(d, DefaultOptions); cells(d.Pages[0]) != header+row1+row2 || st.Rows != 3 {
		t.Errorf("an artifact in a column: cells %s, %d rows", cells(d.Pages[0]), st.Rows)
	}

	// A grid the lattice reads, its header ruled cell by cell too, is the lattice's table alone.
	grid := append(frame(cols3, rows3), hrule(680, 100, 200), hrule(680, 200, 200.5), hrule(680, 200.5, 300),
		hrule(680, 300, 300.5), hrule(680, 300.5, 400))
	got, st, _ := ruledRun(grid,
		[]doc.Span{put("A", 105, 685), put("B", 205, 685), put("C", 305, 685)},
		[]doc.Span{put("1", 105, 665), put("2", 205, 665), put("3", 305, 665)},
		[]doc.Span{put("4", 105, 645), put("5", 205, 645), put("6", 305, 645)})
	if want := "0,0*:A 0,1*:B 0,2*:C 1,0:1 1,1:2 1,2:3 2,0:4 2,1:5 2,2:6"; got != want || st.Tables != 1 {
		t.Errorf("a grid: cells %s, %d tables, want %s and one", got, st.Tables, want)
	}

	// A header whose cells the page draws apart, a paragraph beside the table between them: the
	// table stands where its first span did.
	_, _, blocks := ruledRun(rules, []doc.Span{put("Required", 125, 600)}, []doc.Span{put("Margin note", 420, 600)},
		[]doc.Span{put("Field", 75, 600), put("Description", 215, 600)}, r1)
	if blocks[0].Cell == nil {
		t.Errorf("first block %q, want the table's", blocks[0].Text())
	}
}

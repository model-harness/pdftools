package extract

import (
	"fmt"
	"strings"
	"testing"
)

// grid draws rows of cells in 12pt Helvetica, cell c of each row at x[c], rows 14pt apart from
// y=700 down, each cell its own show operation as LaTeX's tabular sets them.
func grid(font string, x []float64, rows ...[]string) string {
	var sb strings.Builder
	sb.WriteString("BT /" + font + " 12 Tf ")
	for r, row := range rows {
		for c, text := range row {
			if text != "" {
				fmt.Fprintf(&sb, "1 0 0 1 %g %d Tm (%s) Tj ", x[c], 700-14*r, text)
			}
		}
	}
	sb.WriteString("ET")
	return sb.String()
}

var gridX = []float64{10, 90, 170}

// cellsOf renders a page's blocks as their spans' text, cells joined by " | " and blocks by
// " // ".
func cellsOf(t *testing.T, stream string) (string, int) {
	t.Helper()
	p := extractPage(t, stream)
	var blocks []string
	for _, b := range p.Blocks {
		var spans []string
		for _, sp := range b.Spans {
			spans = append(spans, strings.TrimSpace(sp.Text))
		}
		blocks = append(blocks, strings.Join(spans, " | "))
	}
	return strings.Join(blocks, " // "), len(p.Gutters)
}

// TestGuttersSplitAnUnruledTable is table-unruled.pdf in miniature: three columns held apart by
// white space alone, which runs down every row, split into cells with a gutter recorded between
// each pair of columns.
func TestGuttersSplitAnUnruledTable(t *testing.T) {
	text, n := cellsOf(t, grid("F1", gridX,
		[]string{"Station", "Region", "Rainfall"},
		[]string{"Ashford", "South", "712"},
		[]string{"Brampton", "North", "1048"},
		[]string{"Carlisle", "North", "893"}))
	if n != 2 {
		t.Errorf("gutters = %d, want 2", n)
	}
	if want := "Station | Region | Rainfall | Ashford | South | 712 | Brampton | North | 1048 | Carlisle | North | 893"; text != want {
		t.Errorf("spans = %q\nwant %q", text, want)
	}
	p := extractPage(t, grid("F1", gridX, []string{"A", "B", "C"}, []string{"1", "2", "3"}, []string{"4", "5", "6"}))
	for _, g := range p.Gutters {
		if !g.Vertical || g.Pos < 20 || g.Pos > 170 || g.From > 672 || g.To < 700 {
			t.Errorf("gutter = %+v, want a vertical between the columns running down the rows", g)
		}
	}
}

// TestGuttersNeedATable takes the evidence away a piece at a time.
func TestGuttersNeedATable(t *testing.T) {
	rows := func(r ...[]string) [][]string { return r }
	for _, c := range []struct {
		name string
		font string
		x    []float64
		rows [][]string
	}{
		{"two rows", "F1", gridX, rows([]string{"A", "B", "C"}, []string{"1", "2", "3"})},
		{"two columns", "F1", gridX[:2], rows([]string{"A", "B"}, []string{"1", "2"}, []string{"3", "4"})},
		{"a cell as wide as prose", "F1", []float64{10, 90, 400},
			rows([]string{"A", "B is a cell of many words that runs on", "C"}, []string{"1", "2", "3"}, []string{"4", "5", "6"})},
		{"a list", "F1", gridX, rows([]string{"1.", "B", "C"}, []string{"2.", "2", "3"}, []string{"3.", "5", "6"})},
		{"a row with one gap under two rows", "F1", gridX, rows([]string{"A", "B", "C"}, []string{"1", "2", "3"}, []string{"X", "", "Z"})},
		{"code", "F2", gridX, rows([]string{"A", "B", "C"}, []string{"1", "2", "3"}, []string{"4", "5", "6"})},
		{"dot leaders", "F1", gridX, rows([]string{"A . . . .", "B", "1"}, []string{"C . . . .", "D", "2"}, []string{"E . . . .", "F", "3"})},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, n := cellsOf(t, grid(c.font, c.x, c.rows...)); n != 0 {
				t.Errorf("gutters = %d, want 0", n)
			}
		})
	}
	// One gutter, wide enough that no cell is as wide as prose, and second gaps that overlap by
	// less than two spaces: one gutter is two columns, which is no table.
	var sb strings.Builder
	sb.WriteString("BT /F1 12 Tf ")
	for r, row := range []struct {
		mid   string
		third float64
	}{{"B", 418}, {"BBB", 431}, {"BBBBB", 447}} {
		y := 700 - 14*r
		fmt.Fprintf(&sb, "1 0 0 1 10 %d Tm (A) Tj 1 0 0 1 400 %d Tm (%s) Tj 1 0 0 1 %g %d Tm (C) Tj ", y, y, row.mid, row.third, y)
	}
	if _, n := cellsOf(t, sb.String()+"ET"); n != 0 {
		t.Errorf("one gutter: gutters = %d, want 0", n)
	}
	// A line of prose, its words set 4pt apart, under two rows: a gap under two spaces is a word's.
	prose := grid("F1", gridX, []string{"A", "B", "C"}, []string{"1", "2", "3"})
	prose = strings.TrimSuffix(prose, "ET") + "1 0 0 1 10 672 Tm (A) Tj 1 0 0 1 22.004 672 Tm (A) Tj 1 0 0 1 34.008 672 Tm (A) Tj ET"
	if _, n := cellsOf(t, prose); n != 0 {
		t.Errorf("prose under two rows: gutters = %d, want 0", n)
	}
}

// TestAGutterAllowsOneSpanningRow: a header cell set across two columns crosses their gutter,
// and one row may; two may not, since then the columns are not held apart. A header drawn twice,
// as a producer fakes bold, is still one row.
func TestAGutterAllowsOneSpanningRow(t *testing.T) {
	span := func(n int, twice bool) string {
		var sb strings.Builder
		sb.WriteString("BT /F1 12 Tf ")
		for r := 0; r < 4; r++ {
			y := 700 - 14*r
			if r < n {
				fmt.Fprintf(&sb, "1 0 0 1 10 %d Tm (Spanningthetwofirstcolumn) Tj 1 0 0 1 290 %d Tm (C) Tj 1 0 0 1 430 %d Tm (D) Tj ", y, y, y)
				if twice {
					fmt.Fprintf(&sb, "/F1 12.1 Tf 1 0 0 1 10.3 %d Tm (Spanningthetwofirstcolumn) Tj /F1 12 Tf ", y)
				}
				continue
			}
			fmt.Fprintf(&sb, "1 0 0 1 10 %d Tm (A) Tj 1 0 0 1 150 %d Tm (B) Tj 1 0 0 1 290 %d Tm (C) Tj 1 0 0 1 430 %d Tm (D) Tj ", y, y, y, y)
		}
		return sb.String() + "ET"
	}
	if _, n := cellsOf(t, span(1, false)); n != 3 {
		t.Errorf("one spanning row: gutters = %d, want 3", n)
	}
	if _, n := cellsOf(t, span(1, true)); n != 3 {
		t.Errorf("one spanning row drawn twice: gutters = %d, want 3", n)
	}
	if _, n := cellsOf(t, span(2, false)); n >= 3 {
		t.Errorf("two spanning rows: gutters = %d, want fewer than 3", n)
	}
}

// TestAGutterIsTwoSpacesWide: a column gap of two space widths is a gutter, and one just under
// it is a word space a little wide. Helvetica's space at 12pt is 3.336pt and "A" 8.004pt.
func TestAGutterIsTwoSpacesWide(t *testing.T) {
	for _, c := range []struct {
		gap  float64
		want int
	}{{6.7, 2}, {6.6, 0}} {
		x := []float64{10, 10 + 8.004 + c.gap, 10 + 2*(8.004+c.gap)}
		if _, n := cellsOf(t, grid("F1", x, []string{"A", "A", "A"}, []string{"A", "A", "A"}, []string{"A", "A", "A"})); n != c.want {
			t.Errorf("gap %v: gutters = %d, want %d", c.gap, n, c.want)
		}
	}
}

// TestGuttersRunDownThePage: a line that steps back up, as a column break does, ends the run.
func TestGuttersRunDownThePage(t *testing.T) {
	stream := grid("F1", gridX, []string{"A", "B", "C"}, []string{"1", "2", "3"})
	stream = strings.TrimSuffix(stream, "ET") + "1 0 0 1 10 750 Tm (4) Tj 1 0 0 1 90 750 Tm (5) Tj 1 0 0 1 170 750 Tm (6) Tj ET"
	if _, n := cellsOf(t, stream); n != 0 {
		t.Errorf("gutters = %d, want 0", n)
	}
}

// TestAGutterIsNotANarrowCrossing: rows whose gaps are each wide but overlap by less than two
// spaces share no gutter there. Each row's first cell is one "A" longer than the last's, and its
// second cell starts that much further right.
func TestAGutterIsNotANarrowCrossing(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("BT /F1 12 Tf ")
	for r := 0; r < 3; r++ {
		first := strings.Repeat("A", r+1)
		fmt.Fprintf(&sb, "1 0 0 1 10 %d Tm (%s) Tj 1 0 0 1 %g %d Tm (B) Tj 1 0 0 1 100 %d Tm (C) Tj ",
			700-14*r, first, 26.5+8.004*float64(r), 700-14*r, 700-14*r)
	}
	if _, n := cellsOf(t, sb.String()+"ET"); n != 0 {
		t.Errorf("gutters = %d, want 0", n)
	}
}

// TestGuttersSplitBetweenStyles: cells set in different sizes are fragments of their own, and
// the gap between two fragments is a gap like any other.
func TestGuttersSplitBetweenStyles(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("BT ")
	for r := 0; r < 3; r++ {
		y := 700 - 14*r
		fmt.Fprintf(&sb, "/F1 12 Tf 1 0 0 1 10 %d Tm (A) Tj /F1 10 Tf 1 0 0 1 90 %d Tm (B) Tj /F1 12 Tf 1 0 0 1 170 %d Tm (C) Tj ", y, y, y)
	}
	if _, n := cellsOf(t, sb.String()+"ET"); n != 2 {
		t.Errorf("gutters = %d, want 2", n)
	}
}

// TestAWideCellOfTwoStylesIsProse: a cell is the fragments between two gutters, so one of two
// styles as wide as prose is still too wide.
func TestAWideCellOfTwoStylesIsProse(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("BT ")
	for r := 0; r < 3; r++ {
		y := 700 - 14*r
		fmt.Fprintf(&sb, "/F1 12 Tf 1 0 0 1 10 %d Tm (A) Tj 1 0 0 1 90 %d Tm (B is a cell of many words ) Tj ", y, y)
		fmt.Fprintf(&sb, "/F1 11 Tf (that runs on and on) Tj /F1 12 Tf 1 0 0 1 400 %d Tm (C) Tj ", y)
	}
	if _, n := cellsOf(t, sb.String()+"ET"); n != 0 {
		t.Errorf("gutters = %d, want 0", n)
	}
}

// TestGuttersLeaveARuledTable: a line a drawn rule has split is the rules' table.
func TestGuttersLeaveARuledTable(t *testing.T) {
	stream := grid("F1", gridX, []string{"A", "B", "C"}, []string{"1", "2", "3"}, []string{"4", "5", "6"})
	stream += " 0.5 w 80 660 m 80 715 l S 160 660 m 160 715 l S"
	if p := extractPage(t, stream); len(p.Gutters) != 0 || len(p.Rules) == 0 {
		t.Errorf("gutters = %d, rules = %d; want only the rules", len(p.Gutters), len(p.Rules))
	}
}

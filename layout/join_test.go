package layout

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// tcell is table t's cell at (row, col) holding text.
func tcell(t, row, col int, text string) doc.Block {
	return doc.Block{Role: doc.RoleTableCell, Cell: &doc.Cell{Table: t, Row: row, Col: col},
		Spans: []doc.Span{{Text: text, MCID: -1, Style: doc.Style{Size: 10}}}}
}

// trows is table t's cells for a header and body rows, two columns each, starting at row 0.
func trows(t int, head [2]string, body ...[2]string) []doc.Block {
	out := []doc.Block{tcell(t, 0, 0, head[0]), tcell(t, 0, 1, head[1])}
	for i, r := range body {
		out = append(out, tcell(t, i+1, 0, r[0]), tcell(t, i+1, 1, r[1]))
	}
	return out
}

func cellsOf(blocks []doc.Block, t int) map[[2]int]string {
	out := map[[2]int]string{}
	for _, b := range blocks {
		if tableOf(b) == t {
			out[[2]int{b.Cell.Row, b.Cell.Col}] = b.Text()
		}
	}
	return out
}

var monthHead = [2]string{"Month", "Visitors"}

// TestATableContinuedOverAPageIsOneTable is joinAcrossPages' rule: the next page opens with
// a table repeating this page's last table's header at its width, and nothing but artifacts
// stands between them.
func TestATableContinuedOverAPageIsOneTable(t *testing.T) {
	folio := block("12", 9, false)
	folio.Role = doc.RoleArtifact
	for _, c := range []struct {
		name   string
		page2  []doc.Block
		joined bool
	}{
		{"the header repeated", trows(2, monthHead, [2]string{"June", "5410"}), true},
		{"past an artifact", append([]doc.Block{folio}, trows(2, monthHead, [2]string{"June", "5410"})...), true},
		{"a different header", trows(2, [2]string{"Month", "Sales"}, [2]string{"June", "5410"}), false},
		{"a different width", append(trows(2, monthHead, [2]string{"June", "5410"}), tcell(2, 1, 2, "note")), false},
		{"prose before it", append([]doc.Block{body(10, false)}, trows(2, monthHead, [2]string{"June", "5410"})...), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := &doc.Document{Pages: []doc.Page{
				{Number: 1, Blocks: trows(1, monthHead, [2]string{"May", "3920"})},
				{Number: 2, Blocks: c.page2},
			}}
			n := joinAcrossPages(d)
			if (n == 1) != c.joined {
				t.Fatalf("joined %d, want %v", n, c.joined)
			}
			if !c.joined {
				return
			}
			g := cellsOf(d.Pages[0].Blocks, 1)
			if g[[2]int{2, 0}] != "June" || g[[2]int{2, 1}] != "5410" || len(g) != 6 {
				t.Errorf("table 1 = %v, want the header once and June as row 2", g)
			}
			if _, left := firstTable(d.Pages[1].Blocks); left {
				t.Errorf("page 2 still opens with a table: %v", d.Pages[1].Blocks)
			}
		})
	}
}

// TestATableOverThreePagesIsOneTable is the case where the middle page holds nothing but
// the table, so the table is still open when the third page continues it.
func TestATableOverThreePagesIsOneTable(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: trows(1, monthHead, [2]string{"May", "3920"})},
		{Number: 2, Blocks: trows(2, monthHead, [2]string{"June", "5410"}, [2]string{"July", "6830"})},
		{Number: 3, Blocks: trows(3, monthHead, [2]string{"August", "6515"})},
	}}
	if n := joinAcrossPages(d); n != 2 {
		t.Fatalf("joined %d, want 2", n)
	}
	g := cellsOf(d.Pages[0].Blocks, 1)
	for row, month := range map[int]string{1: "May", 2: "June", 3: "July", 4: "August"} {
		if g[[2]int{row, 0}] != month {
			t.Errorf("row %d = %q, want %q (table %v)", row, g[[2]int{row, 0}], month, g)
		}
	}
}

// TestATableJoinsOnlyAcrossABreakItTouches: the table must be what one page ends with and
// the next begins with, so prose after a continuation, or a page of prose between two
// tables, keeps the third page's table its own.
func TestATableJoinsOnlyAcrossABreakItTouches(t *testing.T) {
	for _, c := range []struct {
		name  string
		page2 []doc.Block
	}{
		{"prose after the continuation", append(trows(2, monthHead, [2]string{"June", "5410"}), body(10, false))},
		{"a page of prose", []doc.Block{body(10, false)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := &doc.Document{Pages: []doc.Page{
				{Number: 1, Blocks: trows(1, monthHead, [2]string{"May", "3920"})},
				{Number: 2, Blocks: c.page2},
				{Number: 3, Blocks: trows(3, monthHead, [2]string{"August", "6515"})},
			}}
			joinAcrossPages(d)
			if _, still := firstTable(d.Pages[2].Blocks); !still {
				t.Errorf("page 3's table joined table 1 across %s", c.name)
			}
		})
	}
}

// TestADeclaredTableIsLeftAsItIs is a table sectionize built from a structure tree: one
// number across its pages, its row 0 only on the first. Its later pages have no header to
// compare, so sameHeader's empty-header case keeps it as it is: nothing joins and no row
// moves. The layout passes meet such tables where they run after sectionize, as
// TestBlockMCIDsAreTheUnionOfTheirSpans runs them on the tagged corpus.
func TestADeclaredTableIsLeftAsItIs(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: trows(7, monthHead, [2]string{"May", "3920"})},
		{Number: 2, Blocks: []doc.Block{tcell(7, 2, 0, "June"), tcell(7, 2, 1, "5410")}},
		{Number: 3, Blocks: []doc.Block{tcell(7, 3, 0, "July"), tcell(7, 3, 1, "6830")}},
	}}
	if n := joinAcrossPages(d); n != 0 {
		t.Errorf("joined %d, want 0", n)
	}
	for pi, want := range []int{4, 2, 2} {
		if got := len(d.Pages[pi].Blocks); got != want {
			t.Errorf("page %d has %d blocks, want %d: a declared table's cells stay where they are", pi+1, got, want)
		}
	}
	if r := d.Pages[2].Blocks[0].Cell.Row; r != 3 {
		t.Errorf("July's row = %d, want 3: its declared row is unchanged", r)
	}
}

// TestTwoHeaderlessPartsDoNotJoin: neither page has a row 0 to repeat, so their empty
// headers are no evidence at all, even under two numbers.
func TestTwoHeaderlessPartsDoNotJoin(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: []doc.Block{tcell(7, 1, 0, "May"), tcell(7, 1, 1, "3920")}},
		{Number: 2, Blocks: []doc.Block{tcell(8, 1, 0, "June"), tcell(8, 1, 1, "5410")}},
	}}
	if n := joinAcrossPages(d); n != 0 {
		t.Errorf("joined %d, want 0: an empty header repeats nothing", n)
	}
}

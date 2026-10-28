package extract

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// h is a horizontal rule at y from x0 to x1.
func h(y, x0, x1 float64) doc.Rule { return doc.Rule{Pos: y, From: x0, To: x1} }

// wordHeader is Word's rule under a header row of three cells: each cell's bottom border, and a
// quarter-point square where two meet, drawn twice, as a thin rectangle's two long edges.
func wordHeader(y float64) []doc.Rule {
	var out []doc.Rule
	for _, at := range []float64{y, y + 0.25} {
		out = append(out,
			h(at, 66.53, 139.55), h(at, 139.55, 139.80),
			h(at, 139.80, 243.60), h(at, 243.60, 243.85),
			h(at, 243.85, 545.73))
	}
	return out
}

// TestARuleDrawnCellByCellHeadsATable: a horizontal rule drawn as abutting borders, two or more
// cells long with a joint between each, names its table's columns; a rule drawn whole, two that
// touch with no joint, and segments that overlap, as a shaded box's lines do, name none.
func TestARuleDrawnCellByCellHeadsATable(t *testing.T) {
	got := RuledHeaders(wordHeader(620))
	if len(got) != 1 || len(got[0].Cols) != 3 {
		t.Fatalf("%+v, want one header of three columns", got)
	}
	want := [][2]float64{{66.53, 139.55}, {139.80, 243.60}, {243.85, 545.73}}
	for i, c := range got[0].Cols {
		if c != want[i] {
			t.Errorf("column %d %v, want %v", i, c, want[i])
		}
	}
	if got[0].Y != 620 {
		t.Errorf("y %v, want the rule's lower edge, 620", got[0].Y)
	}
	// The paper's page 19: each joint starts inside the border before it, by up to its own width,
	// and the next border starts from the joint.
	inside := []doc.Rule{h(74, 65.78, 244.86), h(74, 244.10, 244.35), h(74, 244.35, 389.68), h(74, 388.93, 389.18), h(74, 389.18, 545.73)}
	if got := RuledHeaders(inside); len(got) != 1 || len(got[0].Cols) != 3 {
		t.Errorf("joints inside their borders: %+v, want one header of three columns", got)
	}
	for name, rules := range map[string][]doc.Rule{
		"one rule":                               {h(620, 66.53, 545.73)},
		"two, no joint":                          {h(620, 66.53, 300), h(620, 300, 545.73)},
		"overlapping":                            {h(700, 70.53, 541.73), h(700, 72.03, 259.61)},
		"a vertical rule":                        {{Vertical: true, Pos: 139.6, From: 619, To: 620}, h(620, 66.53, 545.73)},
		"a vertical rule where a joint would be": {h(620, 66.53, 139.55), {Vertical: true, Pos: 620, From: 139.55, To: 139.80}, h(620, 139.80, 545.73)},
		"a mark inside a border":                 {h(620, 66.53, 300), h(620, 200, 200.25), h(620, 200.25, 400)},
		"a segment overlapping two cells":        {h(620, 66.53, 139.55), h(620, 139.55, 139.80), h(620, 139.80, 243.60), h(620, 200, 300)},
	} {
		if got := RuledHeaders(rules); len(got) != 0 {
			t.Errorf("%s: %+v, want no header", name, got)
		}
	}
}

// TestTablesSideBySideAreTwoHeaders: two cell-by-cell rules on one line, a gap between them, are
// two tables' headers.
func TestTablesSideBySideAreTwoHeaders(t *testing.T) {
	rules := []doc.Rule{
		h(620, 66, 140), h(620, 140, 140.25), h(620, 140.25, 244),
		h(620, 300, 400), h(620, 400, 400.25), h(620, 400.25, 500),
	}
	got := RuledHeaders(rules)
	if len(got) != 2 || len(got[0].Cols) != 2 || len(got[1].Cols) != 2 || got[1].Cols[0][0] != 300 {
		t.Errorf("%+v, want two headers of two columns, the second from 300", got)
	}
}

// TestRuledHeadersAreFoundTopDown: two tables on a page are two headers, in reading order.
func TestRuledHeadersAreFoundTopDown(t *testing.T) {
	got := RuledHeaders(append(wordHeader(200), wordHeader(600)...))
	if len(got) != 2 || got[0].Y != 600 || got[1].Y != 200 {
		t.Errorf("%+v, want the header at 600, then the one at 200", got)
	}
}

// wordTable is a page Word sets a three-column table on, its header ruled cell by cell under
// the header at 590: a caption above, the header, a row, a paragraph across the page after it,
// and a line after that whose words sit in the columns. Each cell is placed by its own Td, so
// the space between two cells on a line is one extract infers, as Word's is.
const wordTable = "70 590 50 0.5 re f 120 590 0.5 0.5 re f 120.5 590 89.5 0.5 re f 210 590 0.5 0.5 re f 210.5 590 189.5 0.5 re f\n" +
	"BT /F1 10 Tf 75 630 Td (Key) Tj 50 0 Td (fields) Tj ET\n" +
	"BT /F1 10 Tf 75 602 Td (Field) Tj 50 0 Td (Required) Tj 90 0 Td (Description) Tj ET\n" +
	"BT /F1 10 Tf 75 574 Td (Port) Tj 50 0 Td (Yes) Tj 90 0 Td (Where it left) Tj ET\n" +
	"BT /F1 10 Tf 72 548 Td (The records are kept for a year and read once a day by the office.) Tj ET\n" +
	"BT /F1 10 Tf 75 530 Td (Oban) Tj 50 0 Td (Mull) Tj ET"

// TestARuledHeaderSplitsItsTableIntoCells: the lines of a table whose header the page rules cell
// by cell are split at its columns' borders, the header's line and each row's, so each cell is a
// span of its own (ADR 0056). The header is the nearest line above the rule, here 12 points above
// it as Word's cell padding sets it. A line that its columns cannot hold, as a paragraph across the
// page, ends the table, and nothing after it or more than two lines above the rule is split.
func TestARuledHeaderSplitsItsTableIntoCells(t *testing.T) {
	got := spansOf(extractPage(t, wordTable))
	for _, want := range []string{"Field", "Required", "Description", "Port", "Yes", "Where it left",
		"The records are kept for a year and read once a day by the office.", "Key fields", "Oban Mull"} {
		found := false
		for _, s := range got {
			found = found || strings.TrimSpace(s) == want
		}
		if !found {
			t.Errorf("no span %q in %q", want, got)
		}
	}
}

// TestARuledHeaderNeedsItsHeader: a rule with no line within two lines above it heads no table, so
// nothing under it is split, though a caption sits 2.5 lines up; and the table's lines are only
// those over its columns, so a line set beside it, or a line set sideways across it, ends nothing.
func TestARuledHeaderNeedsItsHeader(t *testing.T) {
	rule := "70 590 50 0.5 re f 120 590 0.5 0.5 re f 120.5 590 89.5 0.5 re f 210 590 0.5 0.5 re f 210.5 590 189.5 0.5 re f\n"
	row := func(y string, a, b string) string {
		return "BT /F1 10 Tf 75 " + y + " Td (" + a + ") Tj 50 0 Td (" + b + ") Tj ET\n"
	}
	has := func(got []string, want string) bool {
		for _, s := range got {
			if strings.TrimSpace(s) == want {
				return true
			}
		}
		return false
	}
	got := spansOf(extractPage(t, rule+"BT /F1 10 Tf 75 615 Td (Key fields) Tj ET\n"+row("574", "Port", "Yes")))
	if !has(got, "Port Yes") {
		t.Errorf("no header: %q, want the row whole", got)
	}
	header := row("602", "Field", "Required")
	beside := "BT /F1 10 Tf 420 562 Td (A note in the margin beside the table) Tj ET\n"
	// Turned 15 degrees, so its baseline falls between the rows and its length across the table's
	// columns; a line turned further has its cross off the page's horizontal ones.
	sideways := "BT /F1 10 Tf 0.9659 0.2588 -0.2588 0.9659 200 635 Tm (Set turned across the whole of the table) Tj ET\n"
	// A space drawn by itself across the first column's border, as a producer can draw one.
	space := "BT /F1 10 Tf 119 562 Td ( ) Tj ET\n"
	for name, extra := range map[string]string{"a line beside it": beside, "a line set sideways": sideways, "a space on a border": space} {
		got := spansOf(extractPage(t, rule+header+row("574", "Port", "Yes")+extra+row("550", "Crew", "No")))
		if !has(got, "Crew") || !has(got, "No") {
			t.Errorf("%s: %q, want the row after it split", name, got)
		}
	}
}

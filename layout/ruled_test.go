package layout

import (
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
	got := RuledHeaders(&doc.Page{Rules: wordHeader(620)})
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
	if got := RuledHeaders(&doc.Page{Rules: inside}); len(got) != 1 || len(got[0].Cols) != 3 {
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
		if got := RuledHeaders(&doc.Page{Rules: rules}); len(got) != 0 {
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
	got := RuledHeaders(&doc.Page{Rules: rules})
	if len(got) != 2 || len(got[0].Cols) != 2 || len(got[1].Cols) != 2 || got[1].Cols[0][0] != 300 {
		t.Errorf("%+v, want two headers of two columns, the second from 300", got)
	}
}

// TestRuledHeadersAreFoundTopDown: two tables on a page are two headers, in reading order.
func TestRuledHeadersAreFoundTopDown(t *testing.T) {
	got := RuledHeaders(&doc.Page{Rules: append(wordHeader(200), wordHeader(600)...)})
	if len(got) != 2 || got[0].Y != 600 || got[1].Y != 200 {
		t.Errorf("%+v, want the header at 600, then the one at 200", got)
	}
}

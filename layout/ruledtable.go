package layout

import (
	"math"
	"sort"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
)

// ruledTables reads each table on page p whose header the page rules cell by cell, numbering
// tables from *n, and returns them with the spans they took; taken holds the spans another
// reader took, which this one leaves (ADR 0056).
//
// Word rules such a table only under its header, each cell's bottom border with a joint where
// two meet, and draws nothing between its rows or down its sides, so neither the lattice nor the
// band reader sees it. extract.RuledHeaders reads the rule as the columns, and extract splits the
// table's lines at their borders, so each span here lies in one column. Then:
//
//   - the header is the nearest line above the rule, within two lines of it, its spans in two or
//     more of the columns;
//   - a cell is a block's spans in one column, so a cell that wraps is one cell, as extract
//     joined its lines;
//   - a row starts at a cell whose top is below the row before's top by more than half a line,
//     since a row's cells start on one baseline;
//   - the table ends where a row would start more than half as far again below the row above it
//     as the first row starts below the header, the rule sectionize's tagged reader keeps, or at
//     the first span below the rule across its columns, a paragraph's.
func ruledTables(p *doc.Page, n *int, st *TableStats, taken map[spanRef]bool) []made {
	var out []made
	for _, h := range extract.RuledHeaders(p.Rules) {
		m, rows, ok := ruledTable(p, h, taken)
		if !ok {
			continue
		}
		*n++
		st.Tables++
		st.Rows += len(rows)
		for ri, row := range rows {
			for ci, cell := range row {
				if len(cell) > 0 {
					m.cells = append(m.cells, latticeCell(p, cell, *n, ri, ci))
					st.Cells++
				}
			}
		}
		out = append(out, m)
	}
	return out
}

// ruledSlack is how far a span's box may reach past its column's border or above its rule, in
// points: a glyph's box can reach a little past the line it is set against.
const ruledSlack = 1.0

// headerReach is how far above its rule a header's foot can be, in its lines: Word's cell padding
// sets it more than a line above, as extract's namesake measures.
const headerReach = 2.0

// ruledTable reads the table under h on page p, if there is one: its rows of cells, each a list
// of spans, and the made table with the first span it took; it marks the spans it took in taken.
func ruledTable(p *doc.Page, h extract.RuledHeader, taken map[spanRef]bool) (made, [][][]spanRef, bool) {
	lo, hi := h.Cols[0][0], h.Cols[len(h.Cols)-1][1]
	column := func(box doc.Span) int {
		for c, col := range h.Cols {
			if box.Box.X0 >= col[0]-ruledSlack && box.Box.X1 <= col[1]+ruledSlack {
				return c
			}
		}
		return -1
	}
	type cell struct {
		refs        []spanRef
		col         int
		top, bottom float64
	}
	type over struct {
		ref        spanRef
		col        int
		foot, size float64
	}
	var above []over
	body := map[[2]int]*cell{}
	var order [][2]int
	limit := math.Inf(-1) // the top of the first span across the columns below the rule
	for bi := range p.Blocks {
		b := &p.Blocks[bi]
		if b.Role != doc.RoleParagraph {
			continue
		}
		for si := range b.Spans {
			sp := b.Spans[si]
			ref := spanRef{bi, si}
			if taken[ref] || sp.Box.IsZero() || (doc.Block{Spans: b.Spans[si : si+1]}).IsEmpty() ||
				sp.Box.X1 <= lo || sp.Box.X0 >= hi {
				continue
			}
			c := column(sp)
			size := sp.Box.Y1 - sp.Box.Y0
			switch {
			case sp.Box.Y0 >= h.Y-ruledSlack && sp.Box.Y0 <= h.Y+headerReach*size && c >= 0:
				above = append(above, over{ref, c, sp.Box.Y0, size})
			case sp.Box.Y1 > h.Y+ruledSlack:
				// Above the header: a caption, or the page's text before the table.
			case c < 0:
				limit = math.Max(limit, sp.Box.Y1)
			default:
				k := [2]int{bi, c}
				if body[k] == nil {
					body[k] = &cell{col: c, top: math.Inf(-1), bottom: math.Inf(1)}
					order = append(order, k)
				}
				ce := body[k]
				ce.refs = append(ce.refs, ref)
				ce.top, ce.bottom = math.Max(ce.top, sp.Box.Y1), math.Min(ce.bottom, sp.Box.Y0)
			}
		}
	}
	// The header is the nearest line above the rule, as extract splits it: a caption sits above it.
	headFoot := math.Inf(1)
	for _, o := range above {
		headFoot = math.Min(headFoot, o.foot)
	}
	head := make([][]spanRef, len(h.Cols))
	headCols := 0
	for _, o := range above {
		if o.foot <= headFoot+o.size/2 {
			if len(head[o.col]) == 0 {
				headCols++
			}
			head[o.col] = append(head[o.col], o.ref)
		}
	}
	if headCols < 2 {
		return made{}, nil, false
	}
	var cells []*cell
	for _, k := range order {
		if ce := body[k]; ce.top > limit {
			cells = append(cells, ce)
		}
	}
	sort.SliceStable(cells, func(a, b int) bool { return cells[a].top > cells[b].top })
	rows := [][][]spanRef{head}
	pitch, bottom, rowTop := -1.0, headFoot, math.Inf(1)
	for _, ce := range cells {
		// rowTop starts above every cell, so the first starts the first row.
		if ce.top < rowTop-(ce.top-ce.bottom)/2 {
			gap := bottom - ce.top
			if pitch < 0 {
				pitch = gap
			} else if gap > 1.5*pitch {
				break
			}
			rows = append(rows, make([][]spanRef, len(h.Cols)))
			rowTop = ce.top
		}
		row := rows[len(rows)-1]
		row[ce.col] = append(row[ce.col], ce.refs...)
		bottom = math.Min(bottom, ce.bottom)
	}
	if len(rows) < 2 {
		return made{}, nil, false
	}
	var took []spanRef
	for _, row := range rows {
		for _, c := range row {
			took = append(took, c...)
		}
	}
	first := took[0]
	for _, r := range took {
		taken[r] = true
		if r.block < first.block || r.block == first.block && r.span < first.span {
			first = r
		}
	}
	return made{at: first}, rows, true
}

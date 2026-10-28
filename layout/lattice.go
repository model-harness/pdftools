package layout

import (
	"math"
	"sort"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
)

// A lattice is a table read from a framed grid of rules: the x of every column line and the y
// of every row line, and which neighbouring grid cells no rule divides (ADR 0037).
//
// The band reader in tables.go reads a table a baseline at a time, and two shapes defeat it.
// A cell that wraps is several baselines, so each line became a row of its own: v110-changes
// .pdf's table of link behaviours came out as three wrong lines. A merged cell is one
// baseline across columns that every other row divides, so it was not a row at all, and
// table-merged.pdf's header and its row label fell out of the table. The rules say both: a
// cell is the area its rules enclose, and a merge is a rule that is not drawn.
//
// So a page's rules are taken in connected groups. A group is a lattice when its outer rules
// run its full width and height, a frame. Its grid lines are the ones whose rules cover at least
// half the frame. That leaves out the boxes v110-changes.pdf draws around inline code, which touch
// each other and the table's rules and would otherwise add columns, while table-merged.pdf's
// column line, interrupted only by its spanning header, stays. A group that is not framed, such
// as a booktabs table with no verticals or a dotted grid, is left to the band reader.
type lattice struct {
	xs, ys []float64 // column lines ascending, row lines descending
	region []int     // per grid cell, row-major: the grid cell it merges into, top-left first
}

// latticeTol is how far apart two rules can be and still be one line, or touch: a rule's own
// width, which on disk is up to a point and a half for a frame drawn as a filled rectangle.
const latticeTol = 1.5

// lattices returns the page's framed grids.
func lattices(rules []doc.Rule) []lattice {
	var out []lattice
	for _, group := range touching(rules) {
		var vs, hs []doc.Rule
		var xs, ys []float64
		for _, r := range group {
			if r.Vertical {
				vs, xs = append(vs, r), append(xs, r.Pos)
			} else {
				hs, ys = append(hs, r), append(ys, r.Pos)
			}
		}
		xs, ys = lines(xs, false), lines(ys, true)
		if len(xs) < 2 || len(ys) < 2 || !framed(vs, hs, xs, ys) {
			continue
		}
		// A frame under four rules' widths across is framed by its tolerance alone, covered or not,
		// and can lose its own lines here: a shape drawn a few points wide, of which there are 76
		// on disk, 44 in ISO 32000-2's figures. Anything larger loses only inner lines.
		xs, ys = halfCovered(vs, xs, ys[len(ys)-1], ys[0]), halfCovered(hs, ys, xs[0], xs[len(xs)-1])
		if len(xs) < 3 || len(ys) < 2 {
			continue // one column is a box around text, not a table
		}
		out = append(out, lattice{xs: xs, ys: ys, region: regions(vs, hs, xs, ys)})
	}
	return out
}

// touching groups the rules that meet, end to side or overlapping on one line, transitively.
// Quadratic in the page's rules, which extract caps at 4096.
//
// A rule with no length is left out, as verticals leaves it out of the band reader's input. It
// covers nothing, but it has a position, so a point between two tables would make them one group
// and read them as one table. Extract emits none; a document another producer assembled can.
func touching(rules []doc.Rule) [][]doc.Rule {
	var live []doc.Rule
	for _, r := range rules {
		if r.Length() > 0 {
			live = append(live, r)
		}
	}
	parent := make([]int, len(live))
	for i := range parent {
		parent[i] = i
	}
	root := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i := range live {
		for j := i + 1; j < len(live); j++ {
			if meet(live[i], live[j]) {
				parent[root(i)] = root(j)
			}
		}
	}
	at := map[int]int{}
	var out [][]doc.Rule
	for i, r := range live {
		k, ok := at[root(i)]
		if !ok {
			k = len(out)
			at[root(i)] = k
			out = append(out, nil)
		}
		out[k] = append(out[k], r)
	}
	return out
}

// meet reports whether two rules touch.
func meet(a, b doc.Rule) bool {
	if a.Vertical == b.Vertical {
		return math.Abs(a.Pos-b.Pos) <= latticeTol && a.From <= b.To+latticeTol && b.From <= a.To+latticeTol
	}
	h, v := a, b
	if a.Vertical {
		h, v = b, a
	}
	return v.Pos >= h.From-latticeTol && v.Pos <= h.To+latticeTol &&
		h.Pos >= v.From-latticeTol && h.Pos <= v.To+latticeTol
}

// lines merges positions within latticeTol into one line each, ascending or descending.
func lines(pos []float64, desc bool) []float64 {
	sort.Float64s(pos)
	var out []float64
	for _, p := range pos {
		if len(out) == 0 || p-out[len(out)-1] > latticeTol {
			out = append(out, p)
		}
	}
	if desc {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// covered returns how much of [from, to] the rules at pos cover.
func covered(rules []doc.Rule, pos, from, to float64) float64 {
	var segs [][2]float64
	for _, r := range rules {
		if math.Abs(r.Pos-pos) <= latticeTol {
			segs = append(segs, [2]float64{math.Max(r.From, from), math.Min(r.To, to)})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i][0] < segs[j][0] })
	total, at := 0.0, from
	for _, s := range segs {
		if lo := math.Max(s[0], at); s[1] > lo {
			total += s[1] - lo
			at = s[1]
		}
	}
	return total
}

// framed reports whether the outer lines are drawn their whole length, within a rule's width.
func framed(vs, hs []doc.Rule, xs, ys []float64) bool {
	top, bot, left, right := ys[0], ys[len(ys)-1], xs[0], xs[len(xs)-1]
	whole := func(rs []doc.Rule, pos, from, to float64) bool {
		return covered(rs, pos, from-latticeTol, to+latticeTol) >= to-from-2*latticeTol
	}
	return whole(hs, top, left, right) && whole(hs, bot, left, right) &&
		whole(vs, left, bot, top) && whole(vs, right, bot, top)
}

// halfCovered keeps the lines whose rules cover at least half of [from, to].
func halfCovered(rules []doc.Rule, pos []float64, from, to float64) []float64 {
	var out []float64
	for _, p := range pos {
		if covered(rules, p, from, to) >= 0.5*(to-from) {
			out = append(out, p)
		}
	}
	return out
}

// regions merges each grid cell with its neighbour wherever no rule divides them, across the
// middle of the side they share, and returns each cell's region as its top-left cell.
func regions(vs, hs []doc.Rule, xs, ys []float64) []int {
	R, C := len(ys)-1, len(xs)-1
	region := make([]int, R*C)
	for i := range region {
		region[i] = i
	}
	root := func(i int) int {
		for region[i] != i {
			i = region[i]
		}
		return i
	}
	join := func(a, b int) {
		if ra, rb := root(a), root(b); ra < rb {
			region[rb] = ra
		} else if rb < ra {
			region[ra] = rb
		}
	}
	divides := func(rules []doc.Rule, pos, at float64) bool {
		for _, r := range rules {
			if math.Abs(r.Pos-pos) <= latticeTol && r.From-latticeTol <= at && at <= r.To+latticeTol {
				return true
			}
		}
		return false
	}
	for r := 0; r < R; r++ {
		for c := 0; c+1 < C; c++ {
			if !divides(vs, xs[c+1], (ys[r]+ys[r+1])/2) {
				join(r*C+c, r*C+c+1)
			}
		}
	}
	for c := 0; c < C; c++ {
		for r := 0; r+1 < R; r++ {
			if !divides(hs, ys[r+1], (xs[c]+xs[c+1])/2) {
				join(r*C+c, (r+1)*C+c)
			}
		}
	}
	for i := range region {
		region[i] = root(i)
	}
	return region
}

// spanRef is a span of the page by its block and its index there.
type spanRef struct{ block, span int }

// rows reads the lattice's spans into rows of cells, and returns them with the spans it
// took. A span is in the grid cell its centre is in, and its text goes to its region:
//
//   - across columns, to the region's first, as GFM has no span;
//   - down rows, to the row whose other cells share the span's baseline, or to the region's
//     first where no row does: table-merged.pdf's "Product" sits under an empty cell with no
//     rule between them and keeps its own row, where its centred "Kettles" goes to the first
//     of the two rows it spans.
//
// A grid row is then split at its baselines where every column with text in it has a line on
// each and on no other, as a table with no rules between its body rows has: table-across-pages
// .pdf's months. A cell that wraps where its neighbours do not is one cell.
func (l lattice) rows(p *doc.Page) ([][][]spanRef, []spanRef) {
	R, C := len(l.ys)-1, len(l.xs)-1
	type piece struct {
		ref  spanRef
		r, c int
		base float64
	}
	var pieces []piece
	for bi := range p.Blocks {
		b := &p.Blocks[bi]
		if b.Role != doc.RoleParagraph {
			continue
		}
		for si := range b.Spans {
			box := b.Spans[si].Box
			cx, cy := (box.X0+box.X1)/2, (box.Y0+box.Y1)/2
			if box.IsZero() || cx < l.xs[0] || cx > l.xs[C] || cy > l.ys[0] || cy < l.ys[R] {
				continue
			}
			c := sort.Search(C, func(i int) bool { return l.xs[i+1] >= cx })
			r := sort.Search(R, func(i int) bool { return l.ys[i+1] <= cy })
			pieces = append(pieces, piece{spanRef{bi, si}, r, c, box.Y0})
		}
	}
	cells := make([][]piece, R*C)
	var took []spanRef
	for _, pc := range pieces {
		reg := l.region[pc.r*C+pc.c]
		top, col := reg/C, reg%C
		row := top
		for r := top; r < R && l.region[r*C+col] == reg; r++ {
			for _, o := range pieces {
				if o.r == r && l.region[o.r*C+o.c] != reg && math.Abs(o.base-pc.base) <= latticeTol {
					row = r
				}
			}
		}
		cells[row*C+col] = append(cells[row*C+col], pc)
		took = append(took, pc.ref)
	}
	var out [][][]spanRef
	for r := 0; r < R; r++ {
		var bases []float64 // each a baseline, with the columns that have a line on it
		cols := map[int]map[int]bool{}
		filled := map[int]bool{}
		for c := 0; c < C; c++ {
			for _, pc := range cells[r*C+c] {
				k := -1
				for i, b := range bases {
					if math.Abs(b-pc.base) <= latticeTol {
						k = i
					}
				}
				if k < 0 {
					k = len(bases)
					bases = append(bases, pc.base)
					cols[k] = map[int]bool{}
				}
				cols[k][c] = true
				filled[c] = true
			}
		}
		split := len(bases) > 1 && len(filled) >= 2
		for k := range bases {
			split = split && len(cols[k]) == len(filled)
		}
		if !split {
			bases = nil
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(bases)))
		sub := make([][][]spanRef, max(1, len(bases)))
		for i := range sub {
			sub[i] = make([][]spanRef, C)
		}
		for c := 0; c < C; c++ {
			for _, pc := range cells[r*C+c] {
				i := 0
				for j, b := range bases {
					if pc.base <= b+latticeTol {
						i = j
					}
				}
				sub[i][c] = append(sub[i][c], pc.ref)
			}
		}
		for _, row := range sub {
			for _, cell := range row {
				if hasText(p, cell) {
					out = append(out, row)
					break
				}
			}
		}
	}
	return out, took
}

func hasText(p *doc.Page, refs []spanRef) bool {
	for _, r := range refs {
		if b := p.Blocks[r.block]; !(doc.Block{Spans: b.Spans[r.span : r.span+1]}).IsEmpty() {
			return true
		}
	}
	return false
}

// latticeCell builds one cell from its spans in the order the page gives them. Two spans that
// were neighbours in their block are joined as extract joined them; any others, a cell's lines
// that extract set apart because another cell's text came between, by extract's wrap rule.
func latticeCell(p *doc.Page, refs []spanRef, table, row, col int) doc.Block {
	var spans []doc.Span
	for i, r := range refs {
		sp := p.Blocks[r.block].Spans[r.span]
		if prev := refs[max(0, i-1)]; i > 0 && (prev.block != r.block || prev.span != r.span-1) &&
			extract.WrapSpace(spans, sp.Text) {
			spans[len(spans)-1].Text += " "
		}
		spans = append(spans, sp)
	}
	out := spanBlock(&p.Blocks[refs[0].block], spans)
	out.Role = doc.RoleTableCell
	out.Level, out.Marker = 0, ""
	out.Cell = &doc.Cell{Table: table, Row: row, Col: col, Header: row == 0}
	return out
}

// made is a table read from a page's spans: its cells, and the first span it took, where its
// cells stand.
type made struct {
	at    spanRef
	cells []doc.Block
}

// latticeTables reads each framed table on page p into its cells, numbering tables from *n, and
// returns them with the spans they took.
func latticeTables(p *doc.Page, n *int, st *TableStats) ([]made, map[spanRef]bool) {
	var tables []made
	taken := map[spanRef]bool{}
	for _, l := range lattices(p.Rules) {
		rows, took := l.rows(p)
		if len(rows) < 2 || anyTaken(taken, took) {
			continue
		}
		*n++
		st.Tables++
		st.Rows += len(rows)
		m := made{at: took[0]} // the first span taken, since spans are taken in page order
		for _, r := range took {
			taken[r] = true
		}
		for ri, row := range rows {
			for ci, cell := range row {
				if len(cell) > 0 {
					m.cells = append(m.cells, latticeCell(p, cell, *n, ri, ci))
					st.Cells++
				}
			}
		}
		tables = append(tables, m)
	}
	return tables, taken
}

// placeTables replaces the spans tables took on page p with their cells, and leaves every span
// they did not take where it was. A table's cells stand where its first span stood, and what a
// block held before and after its table spans stays before and after them.
func placeTables(p *doc.Page, tables []made, taken map[spanRef]bool) {
	if len(tables) == 0 {
		return
	}
	out := make([]doc.Block, 0, len(p.Blocks))
	for bi := range p.Blocks {
		src := &p.Blocks[bi]
		var rest []doc.Span
		touched := false
		flush := func() {
			if len(rest) > 0 {
				out = append(out, spanBlock(src, rest))
				rest = nil
			}
		}
		for si := range src.Spans {
			ref := spanRef{bi, si}
			for _, t := range tables {
				if t.at == ref {
					flush()
					out = append(out, t.cells...)
				}
			}
			if taken[ref] {
				touched = true
				continue
			}
			rest = append(rest, src.Spans[si])
		}
		if !touched {
			out = append(out, *src)
			continue
		}
		flush()
	}
	p.Blocks = out
}

func anyTaken(taken map[spanRef]bool, refs []spanRef) bool {
	for _, r := range refs {
		if taken[r] {
			return true
		}
	}
	return false
}

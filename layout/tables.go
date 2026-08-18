package layout

import (
	"sort"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// Tables promotes a page's spans to RoleTableCell where the page's own strokes say a
// table is drawn, and reports what it did.
//
// # Why the strokes and nothing else
//
// The gap between two cells and the gap between two words are the same measurement. Over
// all 117499 inferred spaces on disk the ratio of gap to nominal space width is continuous
// from the 0.40 the SpaceFrac threshold itself imposes out to 1303, with no quarter-width
// band empty below 5 and the largest jump anywhere below 200 — 4529 ratios below 0.50,
// 14530 between 1.75 and 2.00, then a long thin tail of 11 past 200 — so no threshold on
// the gap separates a column boundary from wide word spacing. Over the 12 documents in docs/
// alone the shape is the same: 46917 spaces, 2738 below 0.50, 14337 between 1.75 and 2.00.
// Twelve is the whole directory, not the eleven specifications the tolerance figures in
// geom are denominated in — docs/ also holds LightOnOCR's paper — and the denominator is
// named because a later reading taken over eleven would not reproduce these three numbers
// and would look like drift. That is the measurement that rules out the gap clustering
// pdfplumber and markitdown use, and it is why extract carries
// doc.Page.Rules at all: a stroke drawn between two glyphs is the producer's own statement
// that they are in different cells, where a gap is a statistic about them.
//
// extract has already acted on that statement — splitAtRules divided the row's text into
// one span per cell, because a block's spans carry one box each and text merged into a
// single span cannot be taken apart later without re-measuring glyphs. What is left here
// is the grid: which spans are a row, which rows are one table, and which column each cell
// sits in.
//
// # Everything here is a sort or a comparison, never an arithmetic threshold
//
// Columns come from x-overlap: two cells share a column when their boxes overlap on x.
// That deliberately has no tolerance in it, for the reason listTiers gives — the numbers
// compared are glyph extents the extractor measured, not quantities this package computed,
// so two cells either overlap or they do not.
//
// The alternative was to key a row by the positions of the rules that split it, and it is
// wrong in a way worth recording: adobe-samples/autotagPDFInput.pdf draws its header row's
// column rule at x=158.88 and its body rows' at x=158.94, so an exact key reads one table
// as two, and pymupdf/dotted-gridlines.pdf draws 2048 verticals of a dotted grid where
// which one is found first depends on the width of the text either side of it. Cell
// overlap is invariant to both.
//
// # What a row minimum would cost
//
// Measured over the 6 untagged documents on disk — the only ones this runs on, since all
// 11 corpus specifications are tagged — the gate finds 21 runs, 12 of them a single row.
// A one-row table is emitted by GFM as a header and a delimiter with no body, which is
// how "HB | pencil (very complex)" and "Generate a | launch" would read. They are dropped
// by requiring two rows, and that drop is not free: chinese-tables.pdf sets four genuine
// one-row tables of a rating history. Two rows is kept because a single row cannot
// distinguish a table from a line of text that happens to have a rule through it, which
// is what the other eight are.
func Tables(d *doc.Document, opt Options) TableStats {
	if d == nil {
		return TableStats{}
	}
	var st TableStats
	n := 0
	for pi := range d.Pages {
		p := &d.Pages[pi]
		verts := verticals(p.Rules)
		if len(verts) == 0 {
			continue
		}
		bands := pageBands(p, verts)
		tables := group(bands, opt.minRows())
		if len(tables) == 0 {
			continue
		}
		for _, t := range tables {
			n++
			st.Tables++
			st.Rows += len(t)
			for _, b := range t {
				st.Cells += len(b.cells)
			}
			assign(t, n)
		}
		p.Blocks = rebuild(p.Blocks, bands)
	}
	return st
}

// TableStats reports what Tables did. The failure modes are quiet in both directions — a
// run that finds nothing has not errored, and neither has one that reads a page of prose
// as a grid — so the counts are the only way to see it.
type TableStats struct {
	// Tables counts the grids found, Rows their rows and Cells their cells. Over the
	// untagged documents on disk: 9 tables, 26 rows, 88 cells.
	Tables, Rows, Cells int
}

// minRows is the number of rows a run must have to be a table. Not an Options field: it
// is 2 for the reason Tables' comment measures, and a caller who could set it to 1 would
// be turning on the eight false positives that measurement identified.
func (o Options) minRows() int { return 2 }

// maxRunRows and maxRunCells bound one table, and unlike minRows they are cost guards
// rather than statements about tables.
//
// group's inner loop re-clusters the whole candidate run for each row it adds, because a
// column merge is retroactive — a later row whose cell straddles two established columns
// collapses them, which can put an *earlier* row's pair of cells in one column, and that
// is what ends a run with nothing between the rows. Checking a new row against a frozen
// column set instead would be linear but would answer a different question.
//
// So the work is quadratic, and nothing else bounds it. maxRules caps rules at 4096 and
// does not help: one page-tall vertical splits every band on the page, so a stream of many
// short lines yields as many two-cell rows as it has lines.
//
// It is quadratic in the run's *cells*, though, not its rows, which is why there are two
// caps and not one. Rows alone were capped first and a profile of the capped code shows why
// that is not enough: at 512 rows held fixed, 100 columns take 1.3s, 200 take 4.8s and 400
// take 14.0s, with agrees at 75% of the samples and columnOf alone at 20%. Both caps are
// needed because either factor can be driven on its own — 16384 two-cell rows and 512
// four-hundred-cell rows are the same hostile page written two ways.
//
// Sized against measured ceilings, both taken over every PDF on disk. Rows: the longest run
// of multi-cell bands is 42, on page 888 of ISO 32000-2, with the next largest 23
// (dotted-gridlines.pdf) and 12 (chinese-tables.pdf). Cells: the largest table any document
// produces is 300 cells, and the widest single row is 15. So 512 rows and 4096 cells are
// each more than ten times what a real document reaches, and together they bound the work
// at about 1s for a page built solely to provoke it.
//
// Spans per cell is the axis these do not cap, and it does not need one: a cell holds
// arbitrarily many spans and columns loops all of them to widen the cell's extent, but that
// loop is linear, so the cost is linear in the page's spans rather than quadratic. Measured
// at 512 rows of two cells each, 1000 spans per cell — 1.024M spans — takes 1.088s, against
// 9ms for the same rows at one span per cell.
//
// Reaching either ends the run rather than dropping anything: the rows past the cap start a
// new table, so every span is still emitted and the visible effect on a hostile page is a
// table split into pieces. Truncating instead would lose text, which is the one outcome
// the conservation tests exist to prevent.
const (
	maxRunRows  = 512
	maxRunCells = 4096
)

// band is one baseline's worth of a block's spans, split into cells where a rule crosses.
type band struct {
	// block is the index in the page's block list this band came from, which is what
	// puts the page back together with the non-table bands still in the order they
	// were drawn.
	block int

	// cells are the band's spans grouped by the rules that cross them, in x order. A
	// band that is not a cell row has them all in one cell and is left alone.
	cells [][]doc.Span

	// order is each cell's spans' positions in the block's own span list, parallel to
	// cells. It exists so a band that is not part of a table can be put back in the
	// order extract emitted it: this package sorts spans by y to find bands at all, and
	// reading order is extract's to decide, not this pass's.
	order [][]int

	// table is the 1-based table this band's cells belong to, or 0 when the band is not
	// part of one.
	table int

	// row is the band's 0-based row within that table.
	row int

	// cols are the column indices of each cell, filled by assign.
	cols []int
}

// verticals returns the page's vertical rules with a length, sorted by position, which is
// the order crossed's search depends on.
//
// The length filter is live here and absent from extract's namesake, which is the
// difference between the two inputs rather than an inconsistency: extract builds its rules
// from a content stream and paintPath cannot emit a degenerate one, where this reads a
// doc.Document a caller assembled — an OCR pass, a future producer, a test — and a rule
// with no extent would be admitted by the band test against any band whose range happens
// to contain its single coordinate.
func verticals(rules []doc.Rule) []doc.Rule {
	var out []doc.Rule
	for _, ru := range rules {
		if ru.Vertical && ru.Length() > 0 {
			out = append(out, ru)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Pos < out[b].Pos })
	return out
}

// pageBands splits every paragraph block on the page into bands, in block order.
//
// Only paragraphs, because every other role is already a statement about what the block
// is: an artifact is page furniture and a figure has no spans to divide. On the untagged
// path Tables runs before Headings and Lists, so a paragraph is everything that is not an
// artifact.
func pageBands(p *doc.Page, verts []doc.Rule) []band {
	var out []band
	for bi := range p.Blocks {
		b := &p.Blocks[bi]
		if b.Role != doc.RoleParagraph {
			continue
		}
		for _, rows := range bandsOf(b.Spans) {
			cells, order := split(b.Spans, rows, verts)
			out = append(out, band{block: bi, cells: cells, order: order})
		}
	}
	return out
}

// bandsOf groups a block's spans into baselines, top to bottom, each band returned as
// indices into spans in x order.
//
// Indices rather than the spans themselves, because the spans' own order in the block is
// the reading order extract decided and this pass has to be able to restore it — see
// band.order. Returning copies would discard exactly the information rebuild needs.
//
// Grouped by whether the boxes overlap vertically rather than by a distance between
// baselines, for the same reason columns are grouped by x-overlap: a box is measured and
// an overlap is a comparison, where a leading threshold would be a quantity this package
// invented. A block's spans arrive in reading order and a table's rows are drawn in
// order, so this is a sweep rather than a clustering.
func bandsOf(spans []doc.Span) [][]int {
	idx := make([]int, len(spans))
	for i := range idx {
		idx[i] = i
	}
	// By descending top edge, which is page order for horizontal text. Stable so two
	// spans sharing a top edge keep the order the extractor put them in.
	sort.SliceStable(idx, func(a, b int) bool {
		return spans[idx[a]].Box.Y1 > spans[idx[b]].Box.Y1
	})

	var out [][]int
	var cur []int
	var lo, hi float64
	for _, i := range idx {
		sp := spans[i]
		if len(cur) > 0 && sp.Box.Y1 > lo && sp.Box.Y0 < hi {
			// The band's extent is narrowed to the intersection rather than widened to the
			// union, so a tall span cannot drag two genuine rows into one: every member
			// overlaps every other, not merely its predecessor.
			cur = append(cur, i)
			if sp.Box.Y0 > lo {
				lo = sp.Box.Y0
			}
			if sp.Box.Y1 < hi {
				hi = sp.Box.Y1
			}
			continue
		}
		if len(cur) > 0 {
			out = append(out, inX(spans, cur))
		}
		cur = []int{i}
		lo, hi = sp.Box.Y0, sp.Box.Y1
	}
	if len(cur) > 0 {
		out = append(out, inX(spans, cur))
	}
	return out
}

func inX(spans []doc.Span, idx []int) []int {
	sort.SliceStable(idx, func(a, b int) bool {
		return spans[idx[a]].Box.X0 < spans[idx[b]].Box.X0
	})
	return idx
}

// split divides one band into cells wherever a vertical rule runs between two of its
// spans, and returns one cell holding everything when none does. band is the band's span
// indices in x order; the returned slices are parallel, cells holding the spans and order
// their positions in spans.
//
// Two adjacent spans with no rule between them are one cell, not two. A cell holding an
// italic term is two spans by construction — extract starts a new run at every style
// change — so requiring a rule between every pair would read such a cell as a row of
// prose and reject the table.
func split(spans []doc.Span, band []int, verts []doc.Rule) ([][]doc.Span, [][]int) {
	cells := [][]doc.Span{{spans[band[0]]}}
	order := [][]int{{band[0]}}
	for i := 1; i < len(band); i++ {
		cur, prev := spans[band[i]], spans[band[i-1]]
		if crossed(verts, prev, cur) {
			cells = append(cells, []doc.Span{cur})
			order = append(order, []int{band[i]})
			continue
		}
		n := len(cells) - 1
		cells[n] = append(cells[n], cur)
		order[n] = append(order[n], band[i])
	}
	return cells, order
}

// crossed reports whether a vertical rule runs between two spans of one band.
//
// The two halves are the ones extract's own splitAtRules measured. The rule's x must lie
// strictly between the spans — a rule at a glyph's edge is the table's outer border, which
// encloses text rather than dividing it — and its vertical extent must overlap the spans'
// own band, because a page-wide rule shares its x with everything else set in that column.
// Over ISO 32000-2, 12167 of 37631 inferred spaces have some vertical rule at their x, so
// the extent test is what confines a split to the text the rule encloses.
func crossed(verts []doc.Rule, a, b doc.Span) bool {
	x0, x1 := a.Box.X1, b.Box.X0
	lo, hi := a.Box.Y0, a.Box.Y1
	if b.Box.Y0 < lo {
		lo = b.Box.Y0
	}
	if b.Box.Y1 > hi {
		hi = b.Box.Y1
	}
	i := sort.Search(len(verts), func(i int) bool { return verts[i].Pos > x0 })
	for ; i < len(verts) && verts[i].Pos < x1; i++ {
		if verts[i].To >= lo && verts[i].From <= hi {
			return true
		}
	}
	return false
}

// group gathers consecutive multi-cell bands into tables of at least min rows.
//
// A band joins the table being built when the two grids agree — that is, when clustering
// every cell of every row by x-overlap leaves no row holding two cells in one column. A
// row whose cell straddles two of the table's established columns is a different grid and
// starts a new table, which is what keeps two stacked tables of different shapes apart.
//
// Agreement rather than an identical column count, because a real table is ragged. ISO/TS
// 32004's key/type/value tables have rows filling two of three columns, and
// pymupdf/dotted-gridlines.pdf sets a nine-column total row under six-column data rows
// whose values sit in whichever columns apply to them. Requiring equal counts would cut
// both into one table per distinct row shape.
//
// A band that is not a cell row ends the run, which is what separates two tables with a
// caption or a paragraph between them. Two tables stacked directly with agreeing columns
// and nothing between them would merge; nothing untagged on disk does that, and the honest
// place for the limit is here rather than in a threshold that guesses at a vertical gap.
func group(bands []band, min int) [][]band {
	var out [][]band
	for i := 0; i < len(bands); {
		if len(bands[i].cells) < 2 {
			i++
			continue
		}
		run := bands[i : i+1]
		j := i + 1
		// cells is the run's cell count, tested before agrees is called rather than after,
		// because the call it would guard is the expensive one.
		cells := len(bands[i].cells)
		for j < len(bands) && j-i < maxRunRows &&
			len(bands[j].cells) >= 2 && cells+len(bands[j].cells) <= maxRunCells &&
			agrees(bands[i:j+1]) {
			cells += len(bands[j].cells)
			j++
			run = bands[i:j]
		}
		if len(run) >= min {
			out = append(out, run)
		}
		i = j
	}
	return out
}

// agrees reports whether every row of a candidate table holds at most one cell per column.
func agrees(rows []band) bool {
	cols := columns(rows)
	for _, r := range rows {
		seen := map[int]bool{}
		for _, c := range r.cells {
			k := columnOf(cols, c)
			if seen[k] {
				return false
			}
			seen[k] = true
		}
	}
	return true
}

// columns returns the table's column boundaries: the right edge of each column, in order.
//
// Built by sweeping every cell of every row in ascending left edge and starting a new
// column at the first cell that begins right of the current column's widest right edge.
// Two cells therefore share a column when their boxes overlap on x, transitively — which
// is what makes a number centred in its column and a long label left-aligned in the same
// one land together without a tolerance anywhere.
func columns(rows []band) []float64 {
	type ext struct{ x0, x1 float64 }
	var cs []ext
	for _, r := range rows {
		for _, c := range r.cells {
			lo, hi := c[0].Box.X0, c[0].Box.X1
			for _, sp := range c[1:] {
				if sp.Box.X0 < lo {
					lo = sp.Box.X0
				}
				if sp.Box.X1 > hi {
					hi = sp.Box.X1
				}
			}
			cs = append(cs, ext{lo, hi})
		}
	}
	sort.Slice(cs, func(a, b int) bool { return cs[a].x0 < cs[b].x0 })

	var out []float64
	for _, c := range cs {
		if n := len(out) - 1; n >= 0 && c.x0 < out[n] {
			if c.x1 > out[n] {
				out[n] = c.x1
			}
			continue
		}
		out = append(out, c.x1)
	}
	return out
}

// columnOf returns the index of the column a cell sits in.
//
// The first column whose right edge the cell's left edge does not reach past. A cell built
// into the sweep above always matches; the final clamp is for a caller that did not, and
// putting it in the last column rather than dropping it is the choice every part of this
// repo makes about text it cannot place.
func columnOf(cols []float64, cell []doc.Span) int {
	x0 := cell[0].Box.X0
	for i, right := range cols {
		if x0 < right {
			return i
		}
	}
	return len(cols) - 1
}

// assign records each cell's table, row, and column on the bands of one table.
func assign(rows []band, n int) {
	cols := columns(rows)
	for i := range rows {
		rows[i].table = n
		rows[i].row = i
		rows[i].cols = make([]int, len(rows[i].cells))
		for j, c := range rows[i].cells {
			rows[i].cols[j] = columnOf(cols, c)
		}
	}
}

// rebuild returns the page's blocks with each table's bands replaced by cell blocks.
//
// A block with no table band in it is passed through untouched, which is what keeps this
// change invisible to the 8 reference fixtures that draw no rules and to every page whose
// strokes are artwork. A block with one is taken apart: its non-table bands become blocks
// carrying everything the original declared, and each cell becomes a block of its own.
//
// Every span reaches exactly one output block. That is the invariant the whole repo's
// accounting rests on — a page's characters are the sum of its blocks' — and it is why the
// bands are rebuilt from the spans themselves rather than from their text.
func rebuild(blocks []doc.Block, bands []band) []doc.Block {
	touched := map[int]bool{}
	for _, b := range bands {
		if b.table > 0 {
			touched[b.block] = true
		}
	}
	if len(touched) == 0 {
		return blocks
	}

	byBlock := map[int][]band{}
	for _, b := range bands {
		if touched[b.block] {
			byBlock[b.block] = append(byBlock[b.block], b)
		}
	}

	out := make([]doc.Block, 0, len(blocks))
	for bi := range blocks {
		if !touched[bi] {
			out = append(out, blocks[bi])
			continue
		}
		src := &blocks[bi]
		// Non-table spans are collected as indices into src.Spans and sorted before being
		// emitted, so they come back in the order extract put them in. Without that they
		// would come back in the order bandsOf found them, which is by descending y —
		// this pass sorts by y to find bands at all, and a page whose reading order
		// disagrees with its y order would have its prose silently permuted. Nothing on
		// disk does: over the 743 pages with rules, 0 blocks have bands out of y order.
		// The sort is what makes that a property of the code rather than of the corpus.
		var rest []int
		flush := func() {
			if len(rest) == 0 {
				return
			}
			sort.Ints(rest)
			spans := make([]doc.Span, len(rest))
			for i, k := range rest {
				spans[i] = src.Spans[k]
			}
			out = append(out, spanBlock(src, spans))
			rest = nil
		}
		for _, b := range byBlock[bi] {
			if b.table == 0 {
				for _, o := range b.order {
					rest = append(rest, o...)
				}
				continue
			}
			flush()
			for j, c := range b.cells {
				out = append(out, cellBlock(src, c, b, j))
			}
		}
		flush()
	}
	return out
}

// spanBlock returns a copy of src holding only the given spans.
//
// Role, Level, Marker, Alt and Lang are carried across because they belong to the text
// rather than to the block's extent, and on the untagged path they are all zero anyway —
// carrying them is what keeps this correct if a future producer sets them before this runs.
func spanBlock(src *doc.Block, spans []doc.Span) doc.Block {
	b := *src
	b.Spans = append([]doc.Span(nil), spans...)
	b.Cell = nil
	b.Box = spansBox(spans)
	b.SetMCIDs()
	return b
}

// cellBlock returns one cell of a table as a block.
func cellBlock(src *doc.Block, spans []doc.Span, b band, j int) doc.Block {
	out := spanBlock(src, spans)
	out.Role = doc.RoleTableCell
	out.Level = 0
	out.Marker = ""
	out.Cell = &doc.Cell{
		Table: b.table,
		Row:   b.row,
		Col:   b.cols[j],
		// The first row is the header, which is a choice GFM forces rather than one the
		// page states. An untagged table declares no TH, and a pipe table has no syntax
		// for a body without a header — so the alternatives are to promote row 0 or to
		// emit a blank header above it. The tagged path faces the same choice with
		// evidence and declines to promote, because relabelling data as a column name
		// against a declaration would be inventing structure; here there is no
		// declaration either way, and a drawn table's first row is its heading by the
		// convention every fixture on disk follows.
		Header: b.row == 0,
	}
	return out
}

func spansBox(spans []doc.Span) (r geom.Rect) {
	for i := range spans {
		r = r.Union(spans[i].Box)
	}
	return r
}

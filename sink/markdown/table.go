package markdown

import (
	"sort"
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// blocks emits a run of blocks, gathering each table's cells into one grid.
//
// Every caller that walks a list of blocks goes through here rather than calling block
// directly, because a table is the one construct whose Markdown spans several blocks:
// a cell on its own is a row of one, and nine cells emitted one at a time are nine
// paragraphs. This is the only place that knows a group of blocks can be one output
// unit.
//
// Tables are keyed by number rather than by adjacency, and that is what makes nesting
// and page splits work. A nested table's cells arrive *inside* its container's run —
// 13 tables on disk are nested — so grouping consecutive cells would cut the outer
// table in two at the nesting point. Instead the first cell of a table triggers that
// whole table, gathered from the rest of the list, and its remaining cells are skipped
// where they appear. An inner table therefore follows the outer one rather than
// interrupting it, which is the only order Markdown can express: GFM has no nested
// table syntax.
func (w *writer) blocks(list []doc.Block, opt Options) {
	// done is the tables already emitted, so a cell reached after its table was
	// gathered writes nothing rather than a second copy.
	var done map[int]bool
	for i := range list {
		b := list[i]
		if b.Role == doc.RoleArtifact && !opt.Artifacts {
			continue
		}
		if b.IsEmpty() {
			continue
		}
		n := tableOf(b)
		if n == 0 {
			w.block(b)
			continue
		}
		if done[n] {
			continue
		}
		if done == nil {
			done = map[int]bool{}
		}
		done[n] = true
		w.table(gatherCells(list[i:], n), opt)
	}
}

// tableOf returns the number of the table a block is a cell of, or 0 when the block is
// not a positioned cell. A cell whose position is unknown — a TD outside any Table,
// which sectionize leaves with a nil Cell — is 0 and emits as a paragraph, since a
// grid of one unplaced cell says less than the text does.
func tableOf(b doc.Block) int {
	if b.Role != doc.RoleTableCell || b.Cell == nil {
		return 0
	}
	return b.Cell.Table
}

// gatherCells returns every cell of table n in list, which the caller has positioned
// at that table's first cell.
func gatherCells(list []doc.Block, n int) []doc.Block {
	var out []doc.Block
	for i := range list {
		if tableOf(list[i]) == n && !list[i].IsEmpty() {
			out = append(out, list[i])
		}
	}
	return out
}

// table emits one table as a GFM pipe table.
func (w *writer) table(cells []doc.Block, opt Options) {
	g := grid(cells)
	if len(g.rows) == 0 {
		return
	}
	w.gap(notList, 0)

	// GFM requires a header row and a delimiter row before any body row, so a table
	// whose first row is not a header still needs something in that position. Row 0 is
	// used when it holds any header cell, which covers 777 of the 788 tables on disk —
	// 773 have an all-TH first row and 4 have a partial one. The other 11 declare no
	// header there, and rather than promote a data row and state something the document
	// does not, those get an empty header: it renders as a table with a blank top row,
	// where promoting row 0 would silently relabel data as a column name.
	//
	// A header *below* row 0 has no Markdown syntax at all and is emitted as an
	// ordinary cell. That is 598 of the 788, because these producers mark a whole first
	// column as TH to give each row a row-header — a real distinction GFM cannot make,
	// and the reason Cell.Header exists on every cell rather than only being consulted
	// for row 0.
	body := g.rows
	if g.headerRow() {
		w.row(g.rows[0], g.width, opt)
		body = g.rows[1:]
	} else {
		w.row(nil, g.width, opt)
	}
	w.delimiter(g.width)
	for _, r := range body {
		w.row(r, g.width, opt)
	}

	w.blank = false
	w.lastList, w.lastLevel = notList, 0
}

// row writes one table row, padded to width.
//
// Padding rather than emitting a short row: 46 of 788 tables on disk are ragged, and a
// GFM row with fewer cells than the header is legal but renders with the missing cells
// simply absent, which silently shifts nothing but reads as a broken table. The 69
// ColSpan and 43 RowSpan cells on disk are the usual reason a row is short, and neither
// is expressible — a spanning cell's text lands in its first column and the columns it
// covered are blank, which is what GFM renders for a merged cell anyway.
func (w *writer) row(cells []doc.Block, width int, opt Options) {
	w.str("|")
	for i := 0; i < width; i++ {
		w.str(" ")
		if i < len(cells) {
			w.str(cellText(cells[i], opt))
		}
		w.str(" |")
	}
	w.nl()
}

// delimiter writes the row that separates the header from the body. No alignment
// markers: a cell's alignment is a /Layout attribute this model does not read, and
// defaulting every column to left when the document may say otherwise would be an
// invented claim rather than a missing one.
func (w *writer) delimiter(width int) {
	w.str("|")
	for i := 0; i < width; i++ {
		w.str(" --- |")
	}
	w.nl()
}

// cellText renders one cell's content onto a single line.
//
// A cell cannot contain a newline in GFM — the pipe row *is* the row — so a multi-line
// cell is joined with spaces. 752 cells on disk hold more than one paragraph, and those
// are already joined into the cell's spans by sectionize; this covers what remains, a
// line break inside one of them.
//
// Emphasis survives, since inline markup is legal inside a cell and a bold column
// heading is what these documents draw. The pipe is escaped by escapeInto along with
// every other Markdown delimiter, which is what keeps a cell containing "a|b" from
// becoming two cells.
func cellText(b doc.Block, opt Options) string {
	_ = opt
	return strings.TrimSpace(oneLine(inline(b.Spans, false)))
}

// table is a grid assembled from a table's cells.
type table struct {
	rows  [][]doc.Block
	width int
	// headers counts the header cells in the first row, and first its total cells.
	headers, first int
}

// headerRow reports whether the first row can serve as the Markdown header row.
func (g table) headerRow() bool { return g.headers > 0 }

// grid arranges cells by their declared row and column.
//
// Rows are indexed by their sorted distinct Row values rather than by Row directly, so
// a table whose cells reach this sink in two groups — one page of a table that spans a
// page break, under Write, which emits a page at a time — comes out as a table of the
// rows present instead of one padded with empty rows up to the first row number seen.
//
// A cell whose column collides with one already placed is appended to the row's end
// rather than overwriting. Nothing on disk does this: it would take a producer
// declaring two cells at one position, and the position is derived from ordinal
// position among the row's cells, so a collision is structurally impossible from
// sectionize. Kept because overwriting would drop a document's text, which is the one
// outcome this repo's tests exist to prevent.
func grid(cells []doc.Block) table {
	var order []int
	seen := map[int]bool{}
	for _, c := range cells {
		r := c.Cell.Row
		if !seen[r] {
			seen[r] = true
			order = append(order, r)
		}
	}
	sort.Ints(order)
	at := make(map[int]int, len(order))
	for i, r := range order {
		at[r] = i
	}

	g := table{rows: make([][]doc.Block, len(order))}
	for _, c := range cells {
		i := at[c.Cell.Row]
		row := g.rows[i]
		col := c.Cell.Col
		for len(row) <= col {
			row = append(row, doc.Block{})
		}
		if !row[col].IsEmpty() {
			row = append(row, c)
		} else {
			row[col] = c
		}
		g.rows[i] = row
		if len(row) > g.width {
			g.width = len(row)
		}
	}
	for _, c := range cells {
		if at[c.Cell.Row] != 0 {
			continue
		}
		g.first++
		if c.Cell.Header {
			g.headers++
		}
	}
	return g
}

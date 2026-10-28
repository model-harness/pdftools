package sectionize

import (
	"math"
	"strings"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/geom"
)

// ruledTables reads the tables a tree declared as bare paragraphs, where the page rules their
// header cell by cell, and marks the header row of a declared table its page rules so (ADR 0055).
//
// Word exports a table whose structure it has lost as one P per cell, in row order, and it rules
// the header as each cell's own bottom border: extract.RuledHeaders reads that as the columns. So a
// run of paragraphs that sits in those columns, the header above the rule and the rows below it,
// is the table the page draws. A new row starts wherever a cell's column does not advance past the
// one before it, since a row's cells come in order and a row's first cell is left of its last. A
// header the next page repeats, under the same rule, continues the table rather than starting one.
//
// A table Word did tag, with no TH, is a header row the sink cannot see; the rule under its first
// row is the producer's statement that it is one, unless the table is ruled under every row, a
// grid, where the rule says nothing about the first.
func ruledTables(out *doc.Outline, d *doc.Document) {
	heads := map[int][]extract.RuledHeader{}
	for pi := range d.Pages {
		if hs := extract.RuledHeaders(d.Pages[pi].Rules); len(hs) > 0 {
			heads[d.Pages[pi].Number] = hs
		}
	}
	if len(heads) == 0 {
		return
	}
	next := lastTable(out) + 1
	lists := []*[]doc.Block{&out.Preamble}
	out.Walk(func(s *doc.Section) bool {
		lists = append(lists, &s.Blocks)
		return true
	})
	for _, l := range lists {
		*l = tablesIn(*l, heads, &next)
		markHeaders(*l, heads)
	}
}

// lastTable is the highest table number the outline already uses.
func lastTable(out *doc.Outline) int {
	n := 0
	see := func(bs []doc.Block) {
		for i := range bs {
			if bs[i].Cell != nil && bs[i].Cell.Table > n {
				n = bs[i].Cell.Table
			}
		}
	}
	see(out.Preamble)
	out.Walk(func(s *doc.Section) bool {
		see(s.Blocks)
		return true
	})
	return n
}

// tablesIn reads the ruled tables among blocks and returns blocks with them made cells.
func tablesIn(blocks []doc.Block, heads map[int][]extract.RuledHeader, next *int) []doc.Block {
	drop := map[int]bool{}
	for i := 0; i < len(blocks); {
		end, ok := ruledTable(blocks, i, heads, *next, drop)
		if !ok {
			i++
			continue
		}
		*next++
		i = end
	}
	if len(drop) == 0 {
		return blocks
	}
	kept := blocks[:0:0]
	for j := range blocks {
		if !drop[j] {
			kept = append(kept, blocks[j])
		}
	}
	return kept
}

// ruledTable reads the ruled table whose header starts at blocks[i], if one does, numbers its
// cells table, and returns the index past its last cell. drop collects the blocks that repeat its
// header on a later page.
//
// The header is two or more paragraphs, each in its own column left to right, sitting on one of
// the page's ruled headers. The rows are the paragraphs under it in one of its columns, a new row
// wherever a cell's column does not advance past the one before. A row is the table's while it
// starts within half as far again below the row above it as the first row starts below the
// header: a cell's padding sets a table's row pitch, and a paragraph after a table sits further
// off. The table runs on past a page break where the next page's first blocks are cells under a
// ruled header with the same columns, which is Word's repeated header row.
func ruledTable(blocks []doc.Block, i int, heads map[int][]extract.RuledHeader, table int, drop map[int]bool) (int, bool) {
	page := blockPage(&blocks[i])
	for _, h := range heads[page] {
		j, prev := i, -1
		var names []string
		for j < len(blocks) && headerCell(&blocks[j], page, h) {
			col := columnOf(&blocks[j], h)
			if col <= prev {
				break
			}
			prev = col
			names = append(names, headText(&blocks[j], page))
			j++
		}
		if j-i < 2 {
			continue
		}
		bottom := lowest(blocks[i:j])
		for k := i; k < j; k++ {
			repeatDropped(&blocks[k], page)
			blocks[k].Role = doc.RoleTableCell
			blocks[k].Cell = &doc.Cell{Table: table, Row: 0, Col: columnOf(&blocks[k], h), Header: true}
		}
		row, pitch, k, fresh := 0, -1.0, j, true
		for {
			// A repeat of the header the page draws as blocks of its own is dropped.
			for n := 0; n < len(names) && k < len(blocks) && blockPage(&blocks[k]) == page && headerCell(&blocks[k], page, h) &&
				headText(&blocks[k], page) == names[n]; n++ {
				drop[k] = true
				k++
			}
			col := len(h.Cols)
			for k < len(blocks) && bodyCell(&blocks[k], page, h, heads) {
				c := columnOf(&blocks[k], h)
				if c <= col {
					gap := bottom - blocks[k].Box.Y1
					// The first row under a header, or on a page the table runs on to, is the
					// table's; the first sets its pitch.
					if pitch < 0 {
						pitch = gap
					} else if !fresh && gap > 1.5*pitch {
						break
					}
					fresh = false
					row++
					bottom = blocks[k].Box.Y0
				}
				bottom = math.Min(bottom, blocks[k].Box.Y0)
				col = c
				blocks[k].Role = doc.RoleTableCell
				blocks[k].Cell = &doc.Cell{Table: table, Row: row, Col: c}
				k++
			}
			if k >= len(blocks) {
				return k, true
			}
			nextPage := blockPage(&blocks[k])
			cont, ok := continued(h, heads[nextPage])
			// A next page whose first block is no cell under cont ends the table on the next pass,
			// which takes none of it and finds no later page.
			if nextPage <= page || !ok {
				return k, true
			}
			page, h, fresh = nextPage, cont, true
		}
	}
	return 0, false
}

// continued is the ruled header among hs with h's columns, a table's header repeated on its next
// page.
func continued(h extract.RuledHeader, hs []extract.RuledHeader) (extract.RuledHeader, bool) {
	for _, c := range hs {
		if len(c.Cols) != len(h.Cols) {
			continue
		}
		same := true
		for k := range c.Cols {
			if math.Abs(c.Cols[k][0]-h.Cols[k][0]) > 2*ruledSlack || math.Abs(c.Cols[k][1]-h.Cols[k][1]) > 2*ruledSlack {
				same = false
				break
			}
		}
		if same {
			return c, true
		}
	}
	return extract.RuledHeader{}, false
}

// lowest is the lowest bottom edge among blocks.
func lowest(blocks []doc.Block) float64 {
	y := math.Inf(1)
	for k := range blocks {
		y = math.Min(y, blocks[k].Box.Y0)
	}
	return y
}

// headerCell reports whether b is a header cell over h: a paragraph on h's page in one of its
// columns, its last line just above the rule, within a line of it. Its marked content may run on
// to later pages, where Word repeats a header row, as long as it says the same there.
func headerCell(b *doc.Block, page int, h extract.RuledHeader) bool {
	if b.Role != doc.RoleParagraph || b.Cell != nil || b.Box.IsZero() || columnOf(b, h) < 0 || blockPage(b) != page {
		return false
	}
	first := headText(b, page)
	for _, p := range pagesOf(b) {
		if p != page && headText(b, p) != first {
			return false
		}
	}
	size := blockSize(b)
	return b.Box.Y0 >= h.Y-ruledSlack && b.Box.Y0 <= h.Y+size
}

// bodyCell reports whether b is a cell of the table under h: a paragraph on h's page in one of
// its columns, below the rule. A row that breaks across a page break is a cell whose marked content
// runs on to the next page, which continues the table under a header with h's columns (continued),
// in the same column there.
func bodyCell(b *doc.Block, page int, h extract.RuledHeader, heads map[int][]extract.RuledHeader) bool {
	if b.Role != doc.RoleParagraph || b.Cell != nil || b.Box.IsZero() || blockPage(b) != page {
		return false
	}
	col := columnOf(b, h)
	if b.Box.Y1 > h.Y+ruledSlack || col < 0 {
		return false
	}
	for _, p := range pagesOf(b) {
		if p == page {
			continue
		}
		cont, ok := continued(h, heads[p])
		if p != page+1 || !ok {
			return false
		}
		var rest geom.Rect
		for i := range b.Spans {
			if b.Spans[i].Page == p {
				rest = rest.Union(b.Spans[i].Box)
			}
		}
		at := doc.Block{Box: rest}
		if rest.Y1 > cont.Y+ruledSlack || columnOf(&at, cont) != col {
			return false
		}
	}
	return true
}

// ruledSlack is how far a cell's box may cross its rule or its column's border, in points: a
// glyph's box can reach a little past the line it is set against.
const ruledSlack = 1.0

// columnOf is the column of h whose border b's box lies within, or -1.
func columnOf(b *doc.Block, h extract.RuledHeader) int {
	for c, col := range h.Cols {
		if b.Box.X0 >= col[0]-ruledSlack && b.Box.X1 <= col[1]+ruledSlack {
			return c
		}
	}
	return -1
}

// pagesOf is the pages b's positioned spans are on, in order of first appearance.
func pagesOf(b *doc.Block) []int {
	var out []int
	seen := map[int]bool{}
	for i := range b.Spans {
		if p := b.Spans[i].Page; p > 0 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// headText is the text of b's spans on page, trimmed.
func headText(b *doc.Block, page int) string {
	var sb strings.Builder
	for i := range b.Spans {
		if b.Spans[i].Page == page {
			sb.WriteString(b.Spans[i].Text)
		}
	}
	return strings.TrimSpace(sb.String())
}

// repeatDropped removes from a header cell the spans that repeat it on later pages.
func repeatDropped(b *doc.Block, page int) {
	kept := b.Spans[:0:0]
	for i := range b.Spans {
		if p := b.Spans[i].Page; p <= 0 || p == page {
			kept = append(kept, b.Spans[i])
		}
	}
	b.Spans = kept
	b.SetMCIDs()
}

// blockSize is the largest type size among b's spans.
func blockSize(b *doc.Block) float64 {
	size := 0.0
	for i := range b.Spans {
		if s := b.Spans[i].Style.Size; s > size {
			size = s
		}
	}
	return size
}

// markHeaders marks as its header the first row of each table among blocks that declares no
// header there, where its page rules that row cell by cell and no row below it.
func markHeaders(blocks []doc.Block, heads map[int][]extract.RuledHeader) {
	for i := 0; i < len(blocks); {
		c := blocks[i].Cell
		if c == nil {
			i++
			continue
		}
		j := i
		for j < len(blocks) && blocks[j].Cell != nil && blocks[j].Cell.Table == c.Table {
			j++
		}
		markHeader(blocks[i:j], heads)
		i = j
	}
}

// markHeader marks row 0 of one table's cells as its header, by markHeaders' rule.
func markHeader(cells []doc.Block, heads map[int][]extract.RuledHeader) {
	var first, rest []*doc.Block
	for k := range cells {
		if cells[k].Cell.Row == 0 {
			if cells[k].Cell.Header {
				return
			}
			first = append(first, &cells[k])
		} else {
			rest = append(rest, &cells[k])
		}
	}
	if len(first) < 2 {
		return
	}
	page := blockPage(first[0])
	var under *extract.RuledHeader
	for _, h := range heads[page] {
		over := true
		for _, b := range first {
			if blockPage(b) != page || b.Box.Y0 < h.Y-ruledSlack || b.Box.Y0 > h.Y+blockSize(b) {
				over = false
				break
			}
		}
		if over {
			under = &h
			break
		}
	}
	if under == nil {
		return
	}
	// A rule under a later row too is a grid's, which rules every row alike.
	for _, h := range heads[page] {
		if h.Y >= under.Y {
			continue
		}
		for _, b := range rest {
			if blockPage(b) == page && b.Box.Y0 >= h.Y-ruledSlack && b.Box.Y0 <= h.Y+blockSize(b) {
				return
			}
		}
	}
	for _, b := range first {
		b.Cell.Header = true
	}
}

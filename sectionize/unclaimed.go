package sectionize

import (
	"math"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/layout"
)

// placeUnclaimed puts among the outline's blocks each image the untagged path would keep and no
// structure element claimed, where layout.ImageSlot puts it among that page's blocks (#23).
//
// Chrome paints an SVG <image>, such as a chart's heatmap, outside marked content and in no
// element, so the join on (page, MCID) that places every tagged image could not reach it, and a
// picture Gemma had described was in no output. The untagged path places it by geometry, and this
// asks the same function the same question about the blocks the tree built for its page, in the
// order the tree reads them. A page the tree built nothing for gives its images to Unplaced, as it
// gives its unclaimed text.
//
// An image the tree left out that mostly overlaps one it claimed is the same picture painted
// twice, and is left out: ISO 32000-2's Figure 48 paints its photograph at 1444 by 964 samples,
// unmarked, under the 340 by 258 one its Figure claims, and placed it would be written twice.
func placeUnclaimed(out *doc.Outline, d *doc.Document, ix *index) {
	keep := layout.Keep(d)
	for pi := range d.Pages {
		p := &d.Pages[pi]
		var claimed []geom.Rect
		for ii := range p.Images {
			if ix.used[&p.Images[ii]] {
				claimed = append(claimed, p.Images[ii].Box)
			}
		}
		for ii := range p.Images {
			im := &p.Images[ii]
			// A claimed image is among claimed and overlaps itself, so this is also what
			// keeps one from being placed twice.
			if !keep[pi][ii] || overlapsAny(im.Box, claimed) {
				continue
			}
			ix.used[im] = true
			if !placeOnPage(out, p.Number, im) {
				unplacedImage(out, p, im)
			}
		}
	}
}

// slot is one of the outline's blocks: the list holding it and its index there.
type slot struct {
	list *[]doc.Block
	i    int
}

// placeOnPage inserts im's blocks among the outline blocks drawn on page n, and reports false when
// there are none to place it among.
func placeOnPage(out *doc.Outline, n int, im *doc.Image) bool {
	var lists []*[]doc.Block
	lists = append(lists, &out.Preamble)
	out.Walk(func(s *doc.Section) bool {
		lists = append(lists, &s.Blocks)
		return true
	})
	var on []doc.Block
	var at []slot
	for _, l := range lists {
		for i := range *l {
			if blockPage(&(*l)[i]) == n {
				on = append(on, (*l)[i])
				at = append(at, slot{l, i})
			}
		}
	}
	if len(on) == 0 {
		return false
	}
	k := layout.ImageSlot(on, im.Box)
	// Into a table's cells is no place for an image: before the table's first cell, or after
	// its last.
	var s slot
	if k < len(at) {
		s = at[k]
		for s.i > 0 && sameTable(&(*s.list)[s.i-1], &(*s.list)[s.i]) {
			s.i--
		}
	} else {
		s = at[len(at)-1]
		for s.i+1 < len(*s.list) && sameTable(&(*s.list)[s.i], &(*s.list)[s.i+1]) {
			s.i++
		}
		s.i++
	}
	blocks := layout.ImageBlocks(im)
	*s.list = append((*s.list)[:s.i], append(blocks, (*s.list)[s.i:]...)...)
	return true
}

// unplacedImage gives im to page p's Unplaced content, placed among its unclaimed text.
func unplacedImage(out *doc.Outline, p *doc.Page, im *doc.Image) {
	for i := range out.Unplaced {
		if out.Unplaced[i].Number == p.Number {
			layout.PlaceImage(&out.Unplaced[i], im)
			return
		}
	}
	left := doc.Page{Number: p.Number, Box: p.Box, Rotate: p.Rotate, Rasterized: p.Rasterized}
	layout.PlaceImage(&left, im)
	// In page order, as unplaced builds them.
	at := len(out.Unplaced)
	for i := range out.Unplaced {
		if out.Unplaced[i].Number > p.Number {
			at = i
			break
		}
	}
	out.Unplaced = append(out.Unplaced[:at], append([]doc.Page{left}, out.Unplaced[at:]...)...)
}

// blockPage is the page a block's text was found on: its first positioned span's.
func blockPage(b *doc.Block) int {
	for i := range b.Spans {
		if b.Spans[i].Page > 0 {
			return b.Spans[i].Page
		}
	}
	return 0
}

// sameTable reports whether a and b are cells of one table.
func sameTable(a, b *doc.Block) bool {
	return a.Cell != nil && b.Cell != nil && a.Cell.Table == b.Cell.Table
}

// overlapsAny reports whether r covers half or more of the smaller of itself and any of others.
func overlapsAny(r geom.Rect, others []geom.Rect) bool {
	for _, o := range others {
		if a := math.Min(r.Area(), o.Area()); a > 0 && r.Intersect(o).Area() >= a/2 {
			return true
		}
	}
	return false
}

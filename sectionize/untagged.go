package sectionize

import "github.com/model-harness/pdftools/doc"

// Untagged reconstructs the outline from blocks whose roles are already assigned.
//
// The package comment states that the hierarchy is a level stack over a linear sequence
// of headings rather than a subtree extraction, and that the same stack would serve a
// document with no structure tree. This is that second producer. Tagged reads (level,
// title, content) out of H1..H6 elements; this reads it out of doc.RoleHeading blocks
// and their Level, and both drive the identical builder.open and builder.place.
//
// # It reads declared roles and infers nothing
//
// No geometry, no typography, no font-size clustering — those belong to package layout,
// which has the measurements behind them and the negative results that bound them. A
// block arrives here already marked, and the marking may come from either of two
// producers: layout.Headings promotes a numbered heading in an untagged file, and
// package doctags assigns RoleHeading and RoleListItem from a recognition model's own
// output. Reading the role rather than re-deriving it is what lets one function serve
// both, and it is the same precedence the tagged path keeps — a declaration outranks a
// guess, wherever the declaration came from.
//
// The consequence worth naming is that this is only as good as its input. layout
// promotes a heading only where the document numbers it, so an unnumbered "Foreword"
// stays a paragraph and lands in the preamble rather than opening a clause. That is
// layout's measured limit (its package comment, and DESIGN.md §10), not one this adds:
// a levelled sequence with no levels in it correctly yields no sections.
//
// # No Unplaced, and that is not a gap
//
// Tagged joins page text to structure elements on (page, MCID) and reports what the join
// failed to claim, because on a tagged file unclaimed text means content silently lost.
// There is no join here. Every non-empty block in the document is either a heading or is
// placed under the open section, so Unplaced is always empty and the accounting is
// exact rather than merely reported.
//
// A document whose blocks hold no heading yields an outline with no sections and the
// whole document as preamble — the same shape Tagged returns for a nil tree, and for the
// same reason: it is the honest statement that there was no hierarchy to find, not an
// error. A caller that needs clauses checks Outline.Sections.
func Untagged(d *doc.Document, opt Options) (*doc.Outline, Stats) {
	if d == nil {
		return &doc.Outline{}, Stats{}
	}
	out := &doc.Outline{Meta: d.Meta}
	b := &builder{opt: opt}

	for pi := range d.Pages {
		p := &d.Pages[pi]
		// A block belongs to exactly one page on this path: extract nests blocks inside
		// doc.Page, so unlike the tagged join there is no block spanning a page break and
		// the range is a single number. Tagged needs a range because a paragraph joined
		// from marked content on two pages is one block there.
		pg := span{p.Number, p.Number}
		for bi := range p.Blocks {
			blk := p.Blocks[bi]
			if blk.IsEmpty() {
				continue
			}
			if blk.Role != doc.RoleHeading {
				b.place(detach(blk), pg)
				continue
			}
			// The heading becomes the section's title and is not also placed as content,
			// which is what the tagged path achieves by consuming the heading's spans
			// before the section opens. Here the block is simply not passed to place.
			//
			// clean and truncate are the tagged path's, and they matter for the same
			// reasons: a title arrives with the page's own spacing — a tab between the
			// clause number and the text, a trailing space almost always — and neither
			// survives being a filename or a YAML value.
			level := blk.Level
			if level < 1 {
				// A heading whose producer assigned no rank. Level 1 keeps the clause in
				// the outline, where dropping it would lose the clause and every block
				// under it. Unreachable from layout, which assigns a level or does not
				// promote at all, and reachable from a recognition model that names a
				// heading without ranking it.
				level = 1
			}
			b.open(b.truncate(clean(blk.Text())), level, pg)
		}
	}

	out.Sections = b.roots
	out.Preamble = b.preamble
	return out, b.stats(out)
}

// detach gives a block its own copy of the two slices it carries, so that the outline does
// not share storage with the document it was built from.
//
// The tagged path never needed this and so never says it: emitItem builds each block's spans
// from the elements' own, and unplaced copies each surviving span by value, so a Tagged
// outline is already independent of d. Untagged takes the extractor's blocks whole, which is
// the point — a block arrives with its roles, boxes and marker already right — and a struct
// copy shares the arrays behind Spans and MCIDs.
//
// It matters because both functions return the same *doc.Outline and a caller cannot tell
// which produced one. doc.Block.StripMarker and SetMarker both edit Span.Text in place, so a
// caller that ran layout.Lists after sectionizing rather than before — the order okf uses is
// before, and nothing enforces it — would silently rewrite the text inside the outline it was
// holding. Cheap, and it makes the independence a property of the type's contract rather than
// of one call site's ordering.
func detach(blk doc.Block) doc.Block {
	blk.Spans = append([]doc.Span(nil), blk.Spans...)
	blk.MCIDs = append([]int(nil), blk.MCIDs...)
	return blk
}

// stats reports what a reconstruction did. Shared so that the two paths cannot drift on
// what they count: the numbers are the acceptance measurement for both, and a per-path
// tally is a per-path definition of "section".
//
// The Unplaced fields are read off the outline rather than tracked, so a path that
// produces none reports none without having to say so.
func (b *builder) stats(out *doc.Outline) Stats {
	var st Stats
	st.Blocks = b.blocks
	for _, p := range out.Unplaced {
		st.UnplacedBlocks += len(p.Blocks)
		for _, blk := range p.Blocks {
			st.UnplacedChars += len(blk.Text())
		}
	}
	out.Walk(func(s *doc.Section) bool {
		st.Sections++
		if s.Title != "" {
			st.Titled++
		}
		if s.Number != "" {
			st.Numbered++
		}
		if s.Level > st.MaxLevel {
			st.MaxLevel = s.Level
		}
		return true
	})
	return st
}

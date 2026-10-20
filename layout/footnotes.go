package layout

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// Footnotes finds an untagged page's footnotes, in place, and returns how many it found.
//
// A footnote is two pieces of text far apart: a mark raised above the line it sits in and
// set smaller, and a note at the foot of the page that opens with the same mark, raised
// and smaller in its own turn. Either alone is weak evidence. A raised small digit is as
// often an exponent or a unit, and a small paragraph at the foot is as often a caption or
// an address. So a note is taken only where the page has both:
//
//   - a block below all the page's other text whose lines open with marks, cut into one note
//     at each such line;
//   - a mark in the page's text with the same label, the first one not already taken.
//
// A raised mark sits a quarter of its line's size or more above the line's foot, and is set
// at no more than 0.8 of the line's size. In reference/footnotes.pdf that is 4.2pt up in a
// 9.96pt line, at 6.97pt, and the same shape one size down in the notes (ADR 0036).
//
// A note becomes a RoleFootnote whose Note is its label and Marker its mark, the mark taken
// from its text; the mark in the
// text keeps its own characters and gains the label in Span.Note. A label is the mark's
// number, with its page added where an earlier page used the number already. A mark that
// is not a number becomes "fn" and a count, since a GFM label of "*" would be read as
// emphasis.
//
// Runs before PageBreaks, which passes over notes, so a paragraph continued past a page with
// footnotes is still joined.
func Footnotes(d *doc.Document) int {
	if d == nil {
		return 0
	}
	used := map[string]bool{}
	found, symbols := 0, 0
	for pi := range d.Pages {
		p := &d.Pages[pi]
		notes := footBlocks(p)
		// Cut each foot block into its notes, from its last line up, so the lines before a cut
		// keep their places, and rebuild the page's list with the notes in order.
		var out []doc.Block
		var pieces []int
		for bi := range p.Blocks {
			if !contains(notes, bi) {
				out = append(out, p.Blocks[bi])
				continue
			}
			b := p.Blocks[bi]
			var rest []doc.Block
			for k := len(b.Lines) - 1; k >= 1; k-- {
				if _, ok := markAt(&b, k); !ok {
					continue
				}
				if r, ok := cutAtLine(&b, k); ok {
					rest = append([]doc.Block{r}, rest...)
				}
			}
			for _, n := range append([]doc.Block{b}, rest...) {
				pieces = append(pieces, len(out))
				out = append(out, n)
			}
		}
		p.Blocks = out
		refs := marks(p, pieces)
		for _, bi := range pieces {
			b := &p.Blocks[bi]
			si, ok := markAt(b, 0)
			if !ok {
				continue
			}
			mark := strings.TrimSpace(b.Spans[si].Text)
			r := takeRef(refs, mark)
			if r == nil {
				continue
			}
			label := mark
			if !digits(mark) {
				symbols++
				label = "fn" + strconv.Itoa(symbols)
			} else if used[label] {
				label = mark + "-" + strconv.Itoa(p.Number)
			}
			used[label] = true
			p.Blocks[r.block].Spans[r.span].Note = label
			b.Spans[si].Text = ""
			b.SetMarker(mark)
			b.Note = label
			b.Role = doc.RoleFootnote
			found++
		}
	}
	return found
}

// label is what a footnote mark can say: a number of up to three digits, or up to three of
// the symbols a typesetter counts notes with. A mark it admits with a digit in it is a number,
// which is what digits asks.
var label = regexp.MustCompile(`^(?:[0-9]{1,3}|[*†‡§¶‖]{1,3})$`)

// Raised marks are a quarter of their line's size above its foot or more, and no more than
// 0.8 of its size; see Footnotes.
const (
	markRaise = 0.25
	markSize  = 0.8
)

// footBlocks returns the page's blocks whose first line opens with a mark and which lie below
// all of its other text.
func footBlocks(p *doc.Page) []int {
	var cands []int
	for bi := range p.Blocks {
		if b := &p.Blocks[bi]; b.Role == doc.RoleParagraph && len(b.Lines) > 0 {
			if _, ok := markAt(b, 0); ok {
				cands = append(cands, bi)
			}
		}
	}
	var out []int
	for _, c := range cands {
		top := p.Blocks[c].Box.Y1
		below := false
		for bi := range p.Blocks {
			o := &p.Blocks[bi]
			if o.Role == doc.RoleArtifact || o.IsEmpty() || contains(cands, bi) {
				continue
			}
			if o.Box.Y0 < top {
				below = true
				break
			}
		}
		if !below {
			out = append(out, c)
		}
	}
	return out
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// markAt returns the span that opens b's line k when it is a mark: a label, set raised and
// small against the line, and a span of its own.
func markAt(b *doc.Block, k int) (int, bool) {
	ln := b.Lines[k]
	si := ln.Span
	if ln.Offset == len(b.Spans[si].Text) && si+1 < len(b.Spans) {
		si++
	} else if ln.Offset != 0 {
		return 0, false
	}
	return si, raisedMark(b.Spans[si], ln)
}

// raisedMark reports whether sp is a label set raised and small against the line ln.
func raisedMark(sp doc.Span, ln doc.Line) bool {
	return label.MatchString(strings.TrimSpace(sp.Text)) && ln.Size > 0 &&
		sp.Style.Size <= markSize*ln.Size && sp.Box.Y0-ln.Box.Y0 >= markRaise*ln.Size
}

// ref is a mark in a page's text.
type ref struct {
	block, span int
	mark        string
	taken       bool
}

// marks returns the marks in page p's text, every block but the furniture and the notes, in
// reading order.
func marks(p *doc.Page, notes []int) []*ref {
	var out []*ref
	for bi := range p.Blocks {
		b := &p.Blocks[bi]
		if b.Role == doc.RoleArtifact || contains(notes, bi) || len(b.Lines) == 0 {
			continue
		}
		starts := lineStarts(b)
		at := 0
		for si := range b.Spans {
			k := 0
			for k+1 < len(starts) && starts[k+1] <= at {
				k++
			}
			if raisedMark(b.Spans[si], b.Lines[k]) {
				out = append(out, &ref{block: bi, span: si, mark: strings.TrimSpace(b.Spans[si].Text)})
			}
			at += len(b.Spans[si].Text)
		}
	}
	return out
}

// takeRef returns the first mark in refs saying mark that no note has taken yet, and takes it.
func takeRef(refs []*ref, mark string) *ref {
	for _, r := range refs {
		if !r.taken && r.mark == mark {
			r.taken = true
			return r
		}
	}
	return nil
}

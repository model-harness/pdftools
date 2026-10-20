package layout

import (
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/geom"
)

// PageBreaks joins a paragraph that a page break cut in two, in place, and returns how many
// it joined.
//
// It is extract's column rule (ADR 0030) at a page break, which extract cannot apply
// itself: a page is extracted alone, and an untagged page's running header stands between
// the halves until Furniture has marked it. So it runs after the other passes, and asks
// the same questions of the last paragraph before the break and the first after it,
// skipping artifacts:
//
//   - the first starts flush with its page's text, and is set at the same size;
//   - the last is at the foot of its page's text;
//   - and either the first starts lower case, or the last's last line runs to its page's
//     right edge and the first starts in the face the last ends in.
//
// The face is for a running header Furniture cannot see, because no other page repeats it:
// mupdf_explored.pdf's one page of chapter 27 sets its header flush, at the body's size, in
// italic, after a page that ends on a full line (ADR 0034).
//
// The second half's spans join the first's block, each keeping its own Page, as the model
// carries a block across a break, and the break is a wrap: extract.WrapSpace decides the
// space. A paragraph across three pages, the middle one holding nothing else, is one block.
// The second half ends where one of its lines repeats the indent the first half opened with;
// see splitAtIndent.
func PageBreaks(d *doc.Document) int {
	if d == nil {
		return 0
	}
	t := geom.DefaultTolerance
	joined := 0
	var tail *doc.Block // the paragraph the pages so far end with
	tailPage := -1
	own, opens := 0.0, false // the indent tail's first line was set with
	for pi := range d.Pages {
		p := &d.Pages[pi]
		first := firstBody(p.Blocks)
		if tail != nil && first >= 0 && continuesOver(d, tail, tailPage, p, first, t) {
			b := p.Blocks[first]
			rest, split := doc.Block{}, false
			if opens {
				rest, split = splitAtIndent(&b, own)
			}
			if extract.WrapSpace(tail.Spans, b.Spans[0].Text) {
				tail.Spans[len(tail.Spans)-1].Text += " "
			}
			base := len(tail.Spans)
			tail.Spans = append(tail.Spans, b.Spans...)
			for _, ln := range b.Lines {
				ln.Span += base
				tail.Lines = append(tail.Lines, ln)
			}
			tail.SetMCIDs()
			if split {
				p.Blocks[first] = rest
			} else {
				p.Blocks = append(p.Blocks[:first], p.Blocks[first+1:]...)
			}
			joined++
			if lastBody(p.Blocks) < 0 {
				// The page held nothing but the continuation, so the paragraph may go on.
				tailPage = pi
				continue
			}
		}
		tail, tailPage = nil, -1
		if i := lastBody(p.Blocks); i >= 0 && p.Blocks[i].Role == doc.RoleParagraph {
			tail, tailPage = &p.Blocks[i], pi
			own, opens = opening(linesOn(tail, p.Number), t)
		}
	}
	return joined
}

// opening returns the indent a paragraph's first line was set with, in its space widths, and
// whether it has one: its continuation lines agree on a margin, and its first line
// starts right of it by what extract counts as an indent.
func opening(lines []doc.Line, t geom.Tolerance) (float64, bool) {
	if len(lines) < 2 || lines[0].Space <= 0 {
		return 0, false
	}
	q := lines[0].Space
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, ln := range lines[1:] {
		lo, hi = math.Min(lo, ln.Box.X0), math.Max(hi, ln.Box.X0)
	}
	if (hi-lo)/q > 0.5 {
		return 0, false
	}
	own := (lines[0].Box.X0 - lo) / q
	return own, own >= t.IndentFrac && own <= t.IndentMax
}

// splitAtIndent cuts b before its first line that repeats the indent own, and returns the
// cut-off paragraph. It is extract's rule, that a line set with the indent the block's own
// first line was set with opens a paragraph, for the one block extract cannot apply it to: a
// continuation, whose own first line is flush. So it applies only while every line before
// the cut is flush with the first, and own comes from the paragraph the continuation joins.
// continuesOver has established that b has a first line, with a space width.
func splitAtIndent(b *doc.Block, own float64) (doc.Block, bool) {
	q := b.Lines[0].Space
	margin := b.Lines[0].Box.X0
	k := 1
	for ; k < len(b.Lines); k++ {
		in := (b.Lines[k].Box.X0 - margin) / q
		if math.Abs(in-own) <= 0.5 {
			break
		}
		if math.Abs(in) > 0.5 {
			return doc.Block{}, false
		}
	}
	if k == len(b.Lines) {
		return doc.Block{}, false
	}
	return cutAtLine(b, k)
}

// cutAtLine cuts b before its line k, leaving b the lines before it, and returns the rest as a
// block of its own: its spans from the cut on, its lines rebased onto them, and each half's box
// its own lines'. False, with b untouched, when the lines before the cut drew only spaces.
func cutAtLine(b *doc.Block, k int) (doc.Block, bool) {
	cut := b.Lines[k]
	rest := *b
	rest.Spans = slices.Clone(b.Spans[cut.Span:])
	rest.Spans[0].Text = rest.Spans[0].Text[cut.Offset:]
	rest.Lines = slices.Clone(b.Lines[k:])
	rest.Box = geom.Rect{}
	for i := range rest.Lines {
		if rest.Lines[i].Span == cut.Span {
			rest.Lines[i].Offset -= cut.Offset
		}
		rest.Lines[i].Span -= cut.Span
		rest.Box = rest.Box.Union(rest.Lines[i].Box)
	}
	rest.SetMCIDs()
	spans := slices.Clone(b.Spans[:cut.Span+1])
	spans[cut.Span].Text = spans[cut.Span].Text[:cut.Offset]
	if spans[cut.Span].Text == "" {
		spans = spans[:cut.Span]
	}
	if (doc.Block{Spans: spans}).IsEmpty() {
		// The lines before the cut drew only spaces, so there is no first half to keep.
		return doc.Block{}, false
	}
	// The wrap space at the cut trails whichever span ends the first half.
	spans[len(spans)-1].Text = strings.TrimRight(spans[len(spans)-1].Text, " ")
	b.Spans, b.Lines = spans, b.Lines[:k]
	b.Box = geom.Rect{}
	for i := range b.Lines {
		b.Box = b.Box.Union(b.Lines[i].Box)
	}
	b.SetMCIDs()
	return rest, true
}

// firstBody and lastBody pass over furniture and footnotes, which stand between a paragraph and
// its continuation on the page without being part of the text that runs on.
func firstBody(blocks []doc.Block) int {
	for i, b := range blocks {
		if !aside(b) {
			return i
		}
	}
	return -1
}

func lastBody(blocks []doc.Block) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if b := blocks[i]; !aside(b) {
			return i
		}
	}
	return -1
}

// continuesOver reports whether the first body block of page p carries on tail, the
// paragraph page tailPage ended with.
func continuesOver(d *doc.Document, tail *doc.Block, tailPage int, p *doc.Page, first int, t geom.Tolerance) bool {
	b := &p.Blocks[first]
	if b.Role != doc.RoleParagraph {
		return false
	}
	tp := d.Pages[tailPage].Number
	ends, starts := linesOn(tail, tp), linesOn(b, p.Number)
	if len(ends) == 0 || len(starts) == 0 {
		return false
	}
	end, start := ends[len(ends)-1], starts[0]
	if end.Size <= 0 || start.Size <= 0 || math.Max(end.Size, start.Size) > t.SizeFrac*math.Min(end.Size, start.Size) ||
		end.Space <= 0 || start.Space <= 0 {
		return false
	}
	// The bounds are extract's, in its unit: the line's space width.
	if (start.Box.X0-bodyEdge(p.Blocks, p.Number, false))/start.Space >= t.IndentFrac {
		return false
	}
	if end.Box.Y0-bodyFoot(d.Pages[tailPage].Blocks, tp) > end.Size {
		return false
	}
	if startsLowerText(b.Text()) {
		return true
	}
	// The tail's own lines as well, because a page that held only a continuation holds none
	// of its lines any more: they were moved into tail when it joined.
	right := math.Max(bodyEdge(d.Pages[tailPage].Blocks, tp, true), bodyEdge([]doc.Block{*tail}, tp, true))
	return len(ends) >= 2 && (right-end.Box.X1)/end.Space <= lineFull &&
		tail.Spans[len(tail.Spans)-1].Style.SameRun(b.Spans[0].Style, t)
}

// lineFull is extract's: how many space widths short of its edge a line can stop and still
// be full.
const lineFull = 1.0

// linesOn returns a block's lines on page n, which a block that already crossed a break has
// on two pages.
func linesOn(b *doc.Block, n int) []doc.Line {
	var out []doc.Line
	for _, ln := range b.Lines {
		if ln.Page == n {
			out = append(out, ln)
		}
	}
	return out
}

// bodyEdge is the left edge of the paragraphs' lines on page n, or with right, their right
// edge. Like bodyFoot it passes over a block that drew only spaces, as firstBody does.
func bodyEdge(blocks []doc.Block, n int, right bool) float64 {
	e := math.Inf(1)
	if right {
		e = math.Inf(-1)
	}
	for i := range blocks {
		if blocks[i].Role != doc.RoleParagraph || blocks[i].IsEmpty() {
			continue
		}
		for _, ln := range linesOn(&blocks[i], n) {
			if right {
				e = math.Max(e, ln.Box.X1)
			} else {
				e = math.Min(e, ln.Box.X0)
			}
		}
	}
	return e
}

// bodyFoot is the lowest bottom edge of page n's text, furniture and footnotes aside.
func bodyFoot(blocks []doc.Block, n int) float64 {
	f := math.Inf(1)
	for i := range blocks {
		if aside(blocks[i]) {
			continue
		}
		for _, ln := range linesOn(&blocks[i], n) {
			f = math.Min(f, ln.Box.Y0)
		}
	}
	return f
}

func startsLowerText(s string) bool {
	for _, r := range strings.TrimLeft(s, " ") {
		return unicode.IsLower(r)
	}
	return false
}

// aside reports a block that is not the page's running text: furniture, a footnote, or one that
// drew only spaces.
func aside(b doc.Block) bool {
	return b.Role == doc.RoleArtifact || b.Role == doc.RoleFootnote || b.IsEmpty()
}

package extract

import (
	"math"
	"sort"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/doc"
)

// maxRules bounds the rules kept from one page.
//
// A ruled table draws a few dozen; ISO 32000-2's heaviest page draws about 320
// rectangles, so four edges each puts the real ceiling near 1,300. A stream emitting
// millions of hairlines is corrupt or hostile, and without a cap the slice grows until
// the process dies. Dropping the excess loses grid detail on a page that has already
// left the range any table inference could use.
const maxRules = 4096

// path is the current path under construction, in page space.
//
// Points are transformed by the CTM as they arrive rather than at painting time,
// because the CTM can change mid-path only through a q/Q pair that a well-formed
// stream does not put inside one — and transforming on arrival means a segment carries
// the matrix that was actually in force when its operands were written.
type path struct {
	// cx, cy is the current point and sx, sy the current subpath's start, both needed
	// because h closes to the subpath start and not to the path's first point.
	cx, cy float64
	sx, sy float64
	have   bool

	// segs are the straight segments drawn so far in this path. Curves contribute
	// none: a curve's endpoints say nothing about where a table's edge runs, and a
	// Bézier that happens to be straight is not what a producer draws for a rule.
	segs []seg
}

// seg is one straight segment in page space.
type seg struct{ x0, y0, x1, y1 float64 }

// applyPath updates the path being built and returns whether op was a path operator.
//
// Separate from content.Machine.Apply because a path is not graphics state: it is
// discarded at every painting operator, it does not survive q/Q, and no text operator
// consults it. Keeping it here also keeps content free of a construct only this package
// reads.
func (r *run) applyPath(m *content.Machine, op content.Op) bool {
	ctm := m.GS.CTM
	switch op.Name {
	case "m":
		x, y := ctm.Apply(op.Num(0), op.Num(1))
		r.path.cx, r.path.cy = x, y
		r.path.sx, r.path.sy = x, y
		r.path.have = true
	case "l":
		x, y := ctm.Apply(op.Num(0), op.Num(1))
		if r.path.have {
			r.addSeg(seg{r.path.cx, r.path.cy, x, y})
		}
		r.path.cx, r.path.cy = x, y
		r.path.have = true
	case "c", "v", "y":
		// The current point moves to the last coordinate pair; the curve itself is not
		// a rule. c takes three points, v and y take two.
		n := len(op.Operands)
		if n < 2 {
			return true
		}
		x, y := ctm.Apply(op.Num(n-2), op.Num(n-1))
		r.path.cx, r.path.cy = x, y
		r.path.have = true
	case "h":
		if r.path.have {
			r.addSeg(seg{r.path.cx, r.path.cy, r.path.sx, r.path.sy})
			r.path.cx, r.path.cy = r.path.sx, r.path.sy
		}
	case "re":
		// re both begins and closes a subpath, so all four edges are known at once.
		x, y, w, h := op.Num(0), op.Num(1), op.Num(2), op.Num(3)
		x0, y0 := ctm.Apply(x, y)
		x1, y1 := ctm.Apply(x+w, y)
		x2, y2 := ctm.Apply(x+w, y+h)
		x3, y3 := ctm.Apply(x, y+h)
		r.addSeg(seg{x0, y0, x1, y1})
		r.addSeg(seg{x1, y1, x2, y2})
		r.addSeg(seg{x2, y2, x3, y3})
		r.addSeg(seg{x3, y3, x0, y0})
		r.path.cx, r.path.cy = x0, y0
		r.path.sx, r.path.sy = x0, y0
		r.path.have = true
	case "s", "b", "b*":
		// The close-and-paint variants close the subpath first, which contributes an
		// edge a table's border needs.
		if r.path.have {
			r.addSeg(seg{r.path.cx, r.path.cy, r.path.sx, r.path.sy})
		}
		r.paintPath()
	case "S", "f", "F", "f*", "B", "B*":
		r.paintPath()
	case "n":
		// No-paint. This is the clipping idiom — "W n" establishes a clip and draws
		// nothing — and a clip is not ink. Treating it as a rule would put a table edge
		// wherever a producer clipped an image.
		r.path = path{}
	default:
		return false
	}
	return true
}

func (r *run) addSeg(s seg) {
	if len(r.path.segs) >= maxRules {
		return
	}
	r.path.segs = append(r.path.segs, s)
}

// paintPath turns the finished path's axis-aligned segments into rules and clears it.
//
// Only axis-aligned segments survive. A table's edges are horizontal and vertical by
// construction, and admitting a near-miss would place a column boundary from the
// diagonal of a figure — the corpus draws 421 diagonals, all of them in artwork.
func (r *run) paintPath() {
	for _, s := range r.path.segs {
		if len(r.rules) >= maxRules {
			break
		}
		dx, dy := s.x1-s.x0, s.y1-s.y0
		switch {
		case exactly(dx) && !exactly(dy):
			r.rules = append(r.rules, doc.Rule{
				Vertical: true, Pos: s.x0,
				From: math.Min(s.y0, s.y1), To: math.Max(s.y0, s.y1),
			})
		case exactly(dy) && !exactly(dx):
			r.rules = append(r.rules, doc.Rule{
				Pos:  s.y0,
				From: math.Min(s.x0, s.x1), To: math.Max(s.x0, s.x1),
			})
		}
	}
	r.path = path{}
}

// splitAtRules divides every fragment whose wide gap has a vertical rule running
// through it.
//
// This is the whole reason rules are collected. A table's cells are one fragment
// because they share a style, a baseline and a marked-content region, and the gap
// between them is inferred as an ordinary space — reference/table.pdf yields "Header A
// Header B Header C" as a single fragment, and every downstream stage then sees one
// paragraph where the page draws nine cells. Splitting here rather than in layout is
// what makes the cells separately positionable at all: a block's spans carry one box
// each, so text already merged into one span cannot be taken apart by anything
// downstream without re-measuring glyphs.
//
// A rule "runs through" a gap when its x lies strictly inside the gap and its vertical
// extent overlaps the text's own line. Both halves are load-bearing. Without the x test
// a page's margin rules would split every line; without the extent test a table's
// column rule would split the prose paragraphs above and below it, since a page-wide
// vertical rule shares its x with whatever else the page sets in that column. Measured
// over the corpus, 12167 of ISO 32000-2's 37631 inferred spaces have some vertical rule
// at their x, and requiring the overlap is what confines the split to the text the rule
// actually encloses.
//
// Nothing is lost when no rule matches: a fragment with no matching cut is left exactly
// as it was, which is why the 8 reference fixtures that draw no rules at all are
// bit-identical across this change.
func (r *run) splitAtRules() {
	if len(r.rules) == 0 {
		return
	}
	verts := r.verticals()
	if len(verts) == 0 {
		return
	}
	for i := range r.lines {
		ln := &r.lines[i]
		// Vertical rules bound cells on the along axis, so only horizontal text can be
		// split by them. Rotated text would need the rules projected into its own frame,
		// and nothing on disk draws a ruled table sideways — 0 of the corpus's 421
		// non-axis-aligned segments are near rotated text — so the honest state is to
		// leave it alone rather than to place a boundary on an untested transform.
		if ln.orient != 0 {
			continue
		}
		var out []frag
		for j := range ln.frags {
			out = append(out, splitFrag(&ln.frags[j], verts, ln.cross)...)
		}
		ln.frags = out
	}
}

// verticals returns the page's vertical rules, sorted by position, which is the order
// crossedBy's search depends on.
//
// No length filter, because paintPath cannot emit a degenerate rule: it classifies a
// segment as vertical only when the y delta is not exactly zero, so every rule here
// already has an extent. A filter was written here first and mutation testing found it
// unreachable — no stream can reach it, which makes it a claim about paintPath stated in
// the wrong place. layout.verticals does filter, because its input is a caller-built
// doc.Document rather than a content stream.
func (r *run) verticals() []doc.Rule {
	var out []doc.Rule
	for _, ru := range r.rules {
		if ru.Vertical {
			out = append(out, ru)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Pos < out[b].Pos })
	return out
}

// splitFrag divides one fragment at each cut a rule runs through, returning the pieces
// in text order. The original is returned unchanged, as a single-element slice, when no
// cut matches.
func splitFrag(f *frag, verts []doc.Rule, cross float64) []frag {
	if len(f.cuts) == 0 {
		return []frag{*f}
	}
	var out []frag
	prev := 0
	prevX := f.along0
	for _, c := range f.cuts {
		if !crossedBy(verts, c, cross, f.height) {
			continue
		}
		if c.off <= prev || c.off > len(f.text) {
			// A cut at or before the previous split point cannot yield text, and one past
			// the end is impossible from place() — both would silently drop characters,
			// which is the one outcome the conservation tests exist to prevent.
			continue
		}
		out = append(out, f.piece(prev, c.off, prevX, c.x0))
		prev, prevX = c.off, c.x1
	}
	if len(out) == 0 {
		return []frag{*f}
	}
	return append(out, f.piece(prev, len(f.text), prevX, f.along1))
}

// piece returns the sub-fragment spanning text[lo:hi] and the along interval [x0, x1].
//
// The text is copied rather than shared. A subslice would alias the original's backing
// array, and appendText appends to a fragment's text — so two pieces of one split would
// then write through each other, which is the class of bug frag's own comment about
// strings.Builder was added for.
//
// Every piece is marked apart, including the first. That first mark is what keeps a
// row's opening cell from merging into the closing cell of the row above: appendLine
// walks a block's lines in order, and a table's rows share a style and carry no
// marked-content identifier, so without it reference/table.pdf's nine cells arrive as
// seven spans with "Header C Cell A1" fused across a row boundary.
func (f *frag) piece(lo, hi int, x0, x1 float64) frag {
	p := *f
	p.text = append([]byte(nil), f.text[lo:hi]...)
	p.along0, p.along1 = x0, x1
	p.cuts = nil
	p.apart = true
	return p
}

// crossedBy reports whether some vertical rule runs through a gap.
//
// The rule's x must lie strictly inside the gap: a rule exactly at a glyph's edge is a
// table's outer border, which encloses text rather than dividing it, and admitting it
// would split a cell's first character off from the rest.
//
// Its vertical extent must also overlap the line's own band, taken as the baseline plus
// the fragment's height. The band is deliberately generous at the top and tight at the
// bottom, matching how type sits on a baseline: a rule that stops just above the
// baseline still encloses the text on it, where one entirely below the baseline belongs
// to the row beneath.
func crossedBy(verts []doc.Rule, c cut, cross, height float64) bool {
	// The rules are sorted, so the search can start at the first one that could be
	// inside the gap and stop at the first one past it.
	i := sort.Search(len(verts), func(i int) bool { return verts[i].Pos > c.x0 })
	for ; i < len(verts) && verts[i].Pos < c.x1; i++ {
		if verts[i].To >= cross && verts[i].From <= cross+height {
			return true
		}
	}
	return false
}

// exactly reports whether a delta is zero to within matrix noise.
//
// The tolerance is absolute and tiny on purpose. A producer that draws a vertical rule
// writes the same x twice, and the only difference the two can acquire is the float
// error of passing through the CTM; anything larger is a line that genuinely slopes.
// geom.Tolerance.Epsilon is the same constant, and this is not routed through it
// because a rule is not "close enough" to anything — it either was written axis-aligned
// or it was not.
func exactly(d float64) bool { return math.Abs(d) <= 1e-6 }

package layout

import (
	"math"
	"sort"

	"github.com/model-harness/pdftools/doc"
)

// RuledHeader is the rule under a table's header row where the producer draws it cell by cell,
// as Word does: each header cell's bottom border, abutting, with a small square where two cells
// meet (ADR 0055).
type RuledHeader struct {
	// Y is the rule's lower edge: the header row is above it, and the table's rows below.
	Y float64
	// Cols are the columns' extents, left to right, each a cell's border.
	Cols [][2]float64
}

// Header geometry, in points. A rule is drawn as a thin rectangle, so its two long edges arrive
// as two rules a quarter-point apart; the joint where two cells' borders meet is a square of the
// rule's width, which Word draws at a quarter-point.
const (
	// ruleBand is how far apart two horizontal rules can be and still be one rule's edges.
	ruleBand = 1.0
	// jointMax is the widest a joint between two cells can be; a border is wider.
	jointMax = 1.0
	// abut is how far apart two segments' ends can be and still touch.
	abut = 0.5
)

// RuledHeaders returns the page's ruled headers, top down.
//
// Only a rule drawn as two or more cells' borders with a joint between each, the evidence a
// producer gives of a table's columns where it draws no vertical rules. A rule drawn whole is
// prose's, a page's or a caption's, and two segments that touch with no joint are a rule drawn
// in two strokes; segments that overlap, as a shaded box's lines do, are not borders at all.
func RuledHeaders(p *doc.Page) []RuledHeader {
	var hs []doc.Rule
	for _, r := range p.Rules {
		if !r.Vertical && r.To > r.From {
			hs = append(hs, r)
		}
	}
	sort.SliceStable(hs, func(a, b int) bool { return hs[a].Pos > hs[b].Pos })
	var out []RuledHeader
	for i := 0; i < len(hs); {
		j := i + 1
		low := hs[i].Pos
		for j < len(hs) && hs[i].Pos-hs[j].Pos <= ruleBand {
			low = math.Min(low, hs[j].Pos)
			j++
		}
		out = append(out, headersAt(hs[i:j], low)...)
		i = j
	}
	return out
}

// headersAt reads the ruled headers among one rule's segments, its lower edge at y.
func headersAt(rules []doc.Rule, y float64) []RuledHeader {
	segs := make([][2]float64, 0, len(rules))
	for _, r := range rules {
		segs = append(segs, [2]float64{r.From, r.To})
	}
	sort.Slice(segs, func(a, b int) bool {
		if segs[a][0] != segs[b][0] {
			return segs[a][0] < segs[b][0]
		}
		return segs[a][1] < segs[b][1]
	})
	// A rectangle's two long edges are one segment.
	var uniq [][2]float64
	for _, s := range segs {
		if n := len(uniq); n > 0 && math.Abs(s[0]-uniq[n-1][0]) <= abut && math.Abs(s[1]-uniq[n-1][1]) <= abut {
			continue
		}
		uniq = append(uniq, s)
	}
	var out []RuledHeader
	var cols [][2]float64
	joint := false
	flush := func() {
		if len(cols) >= 2 {
			out = append(out, RuledHeader{Y: y, Cols: cols})
		}
		cols, joint = nil, false
	}
	for k, s := range uniq {
		// Touching the one before, which a joint may start inside of by up to its own width, as
		// the next border then starts from the joint: Word draws the paper's page 19 so.
		if k > 0 {
			end := uniq[k-1][1]
			if s[0] > end+abut || s[0] < end-jointMax-abut {
				// A gap starts a new chain, and an overlap, as a shaded box's lines make, is
				// no border at all.
				if s[0] < end {
					cols = nil
				}
				flush()
			}
		}
		if s[1]-s[0] <= jointMax {
			joint = true
			continue
		}
		if len(cols) > 0 && !joint {
			// Two borders that touch with no joint between them are one rule in two strokes.
			cols = nil
		}
		cols = append(cols, s)
		joint = false
	}
	flush()
	return out
}

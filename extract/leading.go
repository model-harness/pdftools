package extract

import (
	"math"
	"sort"

	"github.com/model-harness/pdftools/geom"
)

// Line pitch, measured off the page (ADR 0057). continues breaks a paragraph wherever a line steps
// down more than ParaFrac of its type size, and a page that sets its type on a loose leading steps
// down that far at every line: 9-point type 14 points apart, 1.6 of its size, is the commonest
// leading on the 55 of opendataloader-bench's 116 text pages whose every line was a block of its
// own. Their own pitch says where a line is the next one: a step the page takes from line to line
// in one size, three times or more, continues a paragraph, and a larger one, the space a page sets
// between paragraphs, breaks it.
//
// Only in a size the page sets no tighter: where its lines also step down within ParaFrac pitchLines
// times, that is its leading, and a larger step it takes as regularly is the space between its
// paragraphs, as in mupdf_explored.pdf's list of libraries, whose entries are mostly one line each.
//
// Only after a full line, one that runs to its column's right edge as a wrapped line does, and is a
// prose line's width: a list's items and a table's rows are spaced as regularly as a paragraph's
// lines, and loosely, and each of them stops where its text does. A column of short lines alone
// makes its own edge, so the width says what the edge cannot: the benchmark's 9-point body lines
// are 31 of its size wide, and reference/lists.pdf's first item 17. A line with a gap in it gutterGap
// spaces wide is a table's row, which pymupdf/small-table.pdf spaces at a pitch, its header as wide
// as prose. And the next line starts at the margin, or left of a first line's indent, as a wrapped
// line does: chinese-tables.pdf sets a caption under a paragraph whose last line is full, at the
// paragraph's pitch, and further in.
const (
	// pitchLines is how many steps of one length make it a page's pitch rather than a coincidence.
	pitchLines = 3
	// pitchMax bounds the steps counted, in their type's size: a step further than that is a gap
	// between regions, not a leading.
	pitchMax = 3.0
	// pitchBin is how close two steps are to be one length, in points: a producer rounds positions.
	pitchBin = 0.5
	// pitchTol is how far a step can be from the pitch and still be it, in its type's size.
	pitchTol = 0.1
	// pitchFull is how far short of its column's right edge a line can stop and still be full, in
	// its type's size: about a word, which ragged-right type wraps before.
	pitchFull = 4.0
	// pitchLeft is how far apart two lines can start and still be in one column, in points.
	pitchLeft = 2.0
	// pitchWide is the narrowest a full line is, in its type's size.
	pitchWide = 20.0
)

// leading is a page's line pitch for each type size, and the right edge of each column its lines
// start at, by size and left edge.
type leading struct {
	pitch map[float64]float64
	edge  map[[2]float64]float64
}

// lineLeft and lineRight are a line's ink extent along its baseline.
func lineLeft(ln *line) float64  { return ln.frags[0].along0 }
func lineRight(ln *line) float64 { return ln.frags[len(ln.frags)-1].along1 }

// columnKey is a line's type size and left edge, bucketed to pitchLeft.
func columnKey(ln *line) [2]float64 {
	return [2]float64{sizeKey(ln.height), math.Round(lineLeft(ln)/pitchLeft) * pitchLeft}
}

// sizeKey buckets a line's type size, so that lines of one size share a pitch.
func sizeKey(h float64) float64 { return math.Round(h*4) / 4 }

// linePitches returns each type size's line pitch on the page, where it has one: the commonest
// step from one line to the next in that size, taken pitchLines times or more; and the right edge
// of each column, the furthest any line starting there reaches but a table's row.
func linePitches(lines []line, t geom.Tolerance) leading {
	edge := map[[2]float64]float64{}
	for i := range lines {
		// A table's row is no column's text, and wider than it.
		if ln := &lines[i]; ln.orient == 0 && len(ln.frags) > 0 && !gapped(ln) {
			k := columnKey(ln)
			edge[k] = math.Max(edge[k], lineRight(ln))
		}
	}
	type bin struct {
		n   int
		sum float64
	}
	bins := map[float64]map[float64]*bin{}
	tight := map[float64]int{}
	for i := 0; i+1 < len(lines); i++ {
		a, b := &lines[i], &lines[i+1]
		if a.orient != 0 || b.orient != 0 || a.height <= 0 || sizeKey(a.height) != sizeKey(b.height) {
			continue
		}
		step := a.cross - b.cross
		if step <= 0 || step > pitchMax*a.height {
			continue
		}
		k := sizeKey(a.height)
		// A tight step is binned too: where the pitch is used there are fewer than pitchLines
		// of them, so they are never its commonest.
		if step <= t.ParaFrac*a.height {
			tight[k]++
		}
		if bins[k] == nil {
			bins[k] = map[float64]*bin{}
		}
		at := math.Round(step/pitchBin) * pitchBin
		if bins[k][at] == nil {
			bins[k][at] = &bin{}
		}
		bins[k][at].n++
		bins[k][at].sum += step
	}
	out := map[float64]float64{}
	for k, steps := range bins {
		// Ascending, so of two steps taken as often the shorter, a line's, is the pitch.
		ats := make([]float64, 0, len(steps))
		for at := range steps {
			ats = append(ats, at)
		}
		sort.Float64s(ats)
		var best *bin
		for _, at := range ats {
			if b := steps[at]; best == nil || b.n > best.n {
				best = b
			}
		}
		if best.n >= pitchLines && tight[k] < pitchLines {
			out[k] = best.sum / float64(best.n)
		}
	}
	return leading{pitch: out, edge: edge}
}

// onPitch reports whether ln follows prev at the page's pitch for their size, prev a full line.
func (l leading) onPitch(prev, ln *line, step float64) bool {
	h := prev.height
	p, ok := l.pitch[sizeKey(h)]
	if !ok || math.Abs(step-p) > pitchTol*h || len(prev.frags) == 0 || len(ln.frags) == 0 {
		return false
	}
	right := lineRight(prev)
	return right-lineLeft(prev) >= pitchWide*h && right >= l.edge[columnKey(prev)]-pitchFull*h &&
		lineLeft(ln) <= lineLeft(prev)+pitchLeft && !gapped(prev) && !gapped(ln)
}

// gapped reports whether a line has a gap in it gutterGap of its spaces wide, as a table's row does
// between its cells.
func gapped(ln *line) bool {
	_, gaps, sp := inkOf(ln)
	for _, g := range gaps {
		if g[1]-g[0] >= gutterGap*sp {
			return true
		}
	}
	return false
}

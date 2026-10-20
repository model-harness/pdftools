package extract

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// splitAtGutters finds the tables a page sets with no rules, the white space between their
// columns running down every row, and splits each row's text into its cells there. It records
// each such gutter as a vertical in r.gutters, which layout's band reader takes as it takes a
// drawn rule (ADR 0038).
//
// A gap alone cannot say a column ends: a cell gap and a word space are the same measurement,
// as layout.Tables measures. A gap that recurs at one x in every line of a run is different
// evidence, since prose puts its word spaces wherever its words end. So a run is at least
// gutterRows lines in a row, each with two or more gaps of gutterGap space widths, and its
// gutters are the stretches gutterGap wide that no more than one of its lines inks: one, for a
// header that spans two columns. Then:
//
//   - a run needs two gutters, three columns, since two columns are as often a list of terms
//     and their values, or a two-column page drawn line by line;
//   - no cell may be wider than gutterCell of the run, as a column of prose is;
//   - a first column of list markers in every row is a list, which Lists reads;
//   - a line in a monospaced face is code, whose alignment is syntax, and a line with dot
//     leaders is a table of contents, whose dots line up from line to line.
//
// A line a drawn rule has already split is the rules' table, not this one's.
func (r *run) splitAtGutters() {
	for i := 0; i < len(r.lines); {
		j := i
		for j < len(r.lines) && rowLike(&r.lines[j]) && (j == i || r.lines[j].cross < r.lines[j-1].cross) {
			j++
		}
		if j-i >= gutterRows {
			r.gutterTable(r.lines[i:j])
		}
		i = max(j, i+1)
	}
}

const (
	// gutterRows is the fewest lines a gutter runs through: a header and two rows.
	gutterRows = 3
	// gutterGap is how wide a gap and a gutter are, in the widest space width of the line.
	gutterGap = 2.0
	// gutterCell is the widest a cell can be, as a share of its run's width.
	gutterCell = 0.4
)

// leaders matches a table of contents' dot leaders.
var leaders = regexp.MustCompile(`(\. ?){4,}`)

// rowLike reports whether a line could be a table row: horizontal, not code, no leaders, not
// already split by a rule, and two or more gaps gutterGap space widths wide.
func rowLike(ln *line) bool {
	if ln.orient != 0 {
		return false
	}
	var text strings.Builder
	n, mono := 0, 0
	for i := range ln.frags {
		f := &ln.frags[i]
		if f.apart {
			return false
		}
		text.Write(f.text)
		n += len(f.text)
		if f.style.Mono {
			mono += len(f.text)
		}
	}
	if mono*2 > n || leaders.MatchString(text.String()) {
		return false
	}
	_, gaps, sp := inkOf(ln)
	wide := 0
	for _, g := range gaps {
		if g[1]-g[0] >= gutterGap*sp {
			wide++
		}
	}
	return sp > 0 && wide >= 2
}

// inkOf returns a line's inked stretches along its baseline, its gaps, and its widest space.
// The gaps are a fragment's recorded cuts and the space between two fragments.
func inkOf(ln *line) (ink, gaps [][2]float64, sp float64) {
	for i := range ln.frags {
		f := &ln.frags[i]
		sp = math.Max(sp, f.space)
		at := f.along0
		for _, c := range f.cuts {
			ink = append(ink, [2]float64{at, c.x0})
			gaps = append(gaps, [2]float64{c.x0, c.x1})
			at = c.x1
		}
		ink = append(ink, [2]float64{at, f.along1})
		if i > 0 && f.along0 > ln.frags[i-1].along1 {
			gaps = append(gaps, [2]float64{ln.frags[i-1].along1, f.along0})
		}
	}
	return ink, gaps, sp
}

// gutterTable reads one run's gutters, and when it is a table splits its lines at them.
func (r *run) gutterTable(lines []line) {
	sp := 0.0
	lo, hi := math.Inf(1), math.Inf(-1)
	type edge struct {
		x float64
		d int
	}
	var edges []edge
	var box geom.Rect
	for k := range lines {
		ink, _, s := inkOf(&lines[k])
		sp = math.Max(sp, s)
		for _, v := range merged(ink) {
			edges = append(edges, edge{v[0], 1}, edge{v[1], -1})
			lo, hi = math.Min(lo, v[0]), math.Max(hi, v[1])
		}
		for f := range lines[k].frags {
			box = box.Union(fragBox(&lines[k].frags[f]))
		}
	}
	sort.Slice(edges, func(a, b int) bool { return edges[a].x < edges[b].x })
	var gutters []doc.Rule
	inked, from := 0, math.NaN()
	for _, e := range edges {
		inked += e.d
		switch {
		case inked <= 1 && math.IsNaN(from) && e.x > lo:
			from = e.x
		case inked > 1 && !math.IsNaN(from):
			// A gutter closes where a cell starts, which is always short of the run's last ink.
			if e.x-from >= gutterGap*sp {
				gutters = append(gutters, doc.Rule{Vertical: true, Pos: (from + e.x) / 2, From: box.Y0, To: box.Y1})
			}
			from = math.NaN()
		}
	}
	if len(gutters) < 2 {
		return
	}
	split := make([][]frag, len(lines))
	list := true
	for k := range lines {
		var cells []frag
		for f := range lines[k].frags {
			cells = append(cells, splitFrag(&lines[k].frags[f], gutters, lines[k].cross)...)
		}
		// A cell is the fragments between two gutters; one wider than gutterCell of the run is prose.
		cellLo, first := math.NaN(), ""
		for c := range cells {
			if c == 0 || crossesGutter(gutters, cells[c-1].along1, cells[c].along0) {
				cellLo = cells[c].along0
				if c == 0 {
					first = strings.TrimSpace(string(cells[c].text))
				}
			}
			if cells[c].along1-cellLo > gutterCell*(hi-lo) {
				return
			}
		}
		list = list && doc.IsMarker(first)
		split[k] = cells
	}
	if list {
		return
	}
	for k := range lines {
		lines[k].frags = split[k]
	}
	r.gutters = append(r.gutters, gutters...)
}

// crossesGutter reports whether a gutter runs between x0 and x1.
func crossesGutter(gutters []doc.Rule, x0, x1 float64) bool {
	for _, g := range gutters {
		if g.Pos > x0 && g.Pos < x1 {
			return true
		}
	}
	return false
}

// merged returns intervals merged where they overlap, in order, so a line counts once at any x.
func merged(iv [][2]float64) [][2]float64 {
	sort.Slice(iv, func(a, b int) bool { return iv[a][0] < iv[b][0] })
	var out [][2]float64
	for _, v := range iv {
		if n := len(out); n > 0 && v[0] <= out[n-1][1] {
			out[n-1][1] = math.Max(out[n-1][1], v[1])
		} else {
			out = append(out, v)
		}
	}
	return out
}

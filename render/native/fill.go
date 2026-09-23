// Package native rasterizes a page in Go, with no borrowed engine.
//
// It is the first half of DESIGN.md §8's Phase 6 and it is deliberately incomplete: what
// exists here is the rasterizer *core* — path construction, the two fill rules, clipping,
// anti-aliased coverage — and the thing every page in the corpus also needs, glyph outlines,
// does not. So this backend refuses a page it cannot draw in full rather than returning a
// partial image. That is the whole of its safety argument: a rasterizer that silently omits
// the text is indistinguishable from one that rendered a page with no text on it, and this
// repo has already paid once for a silent omission — see doc.Page.Failed and the cover page
// it was written for.
//
// # Why a scanline rasterizer and not an analytic one
//
// Coverage is sampled on 16 scanlines per pixel row and computed exactly along each of them.
// The vertical direction is sampled because a polygon's intersection with a pixel is a
// general polygon and the exact area of it is a clipping problem per pixel; the horizontal
// direction is exact because along one scanline it is just an interval, so there is nothing
// to approximate and no reason to.
//
// The split was measured rather than assumed, against render/pdfium on three shapes that
// exercise what a fill rule has to get right — a convex triangle, a five-pointed star drawn
// as one self-intersecting subpath (nonzero fills its middle, even-odd hollows it, so the
// shape says which rule ran), and a closed pair of cubic Béziers. At 16 samples the mean
// absolute difference from pdfium is **0.06 of 255**, the maximum is 28 on a handful of edge
// pixels, no pixel differs by more than 32, and the total ink agrees to 1.000. Quantizing per
// subsample instead of accumulating exact area and scaling once put the mean at 3.47 and the
// ink ratio at 0.941 — 255/16 is 15, so a fully covered pixel came out 240 — which is the
// kind of error that hides as "slightly light" forever.
package native

import (
	"math"
	"sort"
)

// coverSamples is how many scanlines each pixel row is sampled on.
//
// Sixteen because that is where the measurement above levels off and because the arithmetic
// stays in a float64 without care: a row's coverage is a sum of 16 interval lengths.
const coverSamples = 16

// point is a position in device space: y increases downward, as in the image this package
// writes. The conversion from PDF user space happens once, in the matrix the walker carries,
// so nothing below this line knows which way up a page is.
type point struct{ x, y float64 }

// edge is one flattened line segment of a path, kept in device space.
//
// A path arrives as lines and cubics and leaves as edges: the rasterizer never sees a curve,
// which is what keeps the fill rules to one implementation rather than one per segment kind.
type edge struct{ x0, y0, x1, y1 float64 }

// path accumulates one PDF path: subpaths of flattened edges, plus the pen state a content
// stream's operators move around.
type path struct {
	edges []edge

	cur   point // where the pen is
	start point // where the current subpath began, for h and for the implicit close
	open  bool  // a subpath is being built
}

func (p *path) moveTo(to point) {
	p.close()
	p.cur, p.start, p.open = to, to, true
}

func (p *path) lineTo(to point) {
	// A lineTo with no current subpath is not an error in a content stream — a producer may
	// emit one after a paint operator cleared the path — and the segment has no start, so
	// there is nothing to add. Silently ignoring it is what every reader does and what the
	// alternative would cost: refusing the page over a stray operator.
	if p.open {
		p.edges = append(p.edges, edge{p.cur.x, p.cur.y, to.x, to.y})
	}
	p.cur = to
}

// curveTo flattens a cubic Bézier into line segments.
//
// The segment count comes from the control polygon's length, which bounds the curve's own
// length from above, so a curve spanning three pixels gets a handful of segments and one
// spanning a page gets many. A fixed count would either round a small curve's corners or
// spend hundreds of segments on them.
//
// The 0.2-device-unit target is a fifth of a pixel: below the coverage this rasterizer can
// represent, so flattening stops being visible before it stops being cheap.
func (p *path) curveTo(c1, c2, to point) {
	if !p.open {
		p.moveTo(p.cur)
	}
	from := p.cur
	span := hypot(from, c1) + hypot(c1, c2) + hypot(c2, to)
	n := int(math.Min(256, math.Max(4, span/0.2)))
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
		p.lineTo(point{
			x: a*from.x + b*c1.x + c*c2.x + d*to.x,
			y: a*from.y + b*c1.y + c*c2.y + d*to.y,
		})
	}
}

// close ends the current subpath, adding the edge back to its start.
//
// Every subpath is closed for filling whether the stream said so or not, which is §8.5.3.3's
// rule and not a convenience: an unclosed subpath has no interior to fill, so a reader that
// honoured the omission would drop the shape rather than draw a different one.
func (p *path) close() {
	if p.open && p.cur != p.start {
		p.edges = append(p.edges, edge{p.cur.x, p.cur.y, p.start.x, p.start.y})
	}
	p.open = false
}

// rect adds a closed rectangle, which is the re operator.
func (p *path) rect(x, y, w, h float64, m matrix) {
	p.moveTo(m.apply(point{x, y}))
	p.lineTo(m.apply(point{x + w, y}))
	p.lineTo(m.apply(point{x + w, y + h}))
	p.lineTo(m.apply(point{x, y + h}))
	p.close()
}

func (p *path) empty() bool { return len(p.edges) == 0 }

func hypot(a, b point) float64 { return math.Hypot(b.x-a.x, b.y-a.y) }

// mask is an 8-bit coverage bitmap in device space, one byte per pixel.
//
// Its own type because it is used for two different things and the distinction matters: the
// coverage a fill produces, and the clip a W operator establishes. They compose by
// multiplication, which is what makes a clip inside a clip the intersection.
type mask struct {
	w, h int
	a    []uint8

	// y0 and y1 bound the rows that carry anything, so a clip does not cost the page's area
	// every time one is intersected into another. Half-open, and equal when the mask is empty.
	y0, y1 int
}

func newMask(w, h int) *mask { return &mask{w: w, h: h, a: make([]uint8, w*h)} }

// opaqueMask returns a mask that admits everything, which is the clip of an unclipped page.
func opaqueMask(w, h int) *mask {
	m := newMask(w, h)
	for i := range m.a {
		m.a[i] = 255
	}
	m.y0, m.y1 = 0, h
	return m
}

// intersect multiplies this mask by another, over the rows the other one touches.
//
// Multiplication and not a minimum, because a clip is a coverage and two partial coverages
// compose: a half-covered pixel inside a half-covered clip is a quarter covered. A minimum
// would agree on every hard-edged rectangle — which is what made the choice untestable until a
// fixture put two soft edges over each other — and disagree wherever anti-aliasing meets a
// clip, which is every curved clip's boundary.
//
// Bounded by the incoming mask's rows because a clip is established per q…Q group and a spec
// page has thousands of them: intersecting the whole page each time cost 18 ms per clip at 200
// dpi, so 200 clips took 3.6 s. Outside those rows the result is zero — anything the new clip
// does not admit is admitted by nothing — so the rows it never touched are cleared instead of
// multiplied.
func (m *mask) intersect(o *mask) {
	for y := 0; y < m.h; y++ {
		row := y * m.w
		if y < o.y0 || y >= o.y1 {
			clear(m.a[row : row+m.w])
			continue
		}
		for x := 0; x < m.w; x++ {
			// The +127 rounds to nearest rather than truncating, which matters only where both
			// coverages are partial: 200×200/255 is 156 truncated and 157 rounded. 255×255/255
			// is 255 either way, so this is not what keeps an opaque clip opaque.
			//
			// #nosec G115 -- both operands are uint8, so the product is at most 65025, the
			// rounded quotient at most 255, and the conversion cannot truncate.
			m.a[row+x] = uint8((int(m.a[row+x])*int(o.a[row+x]) + 127) / 255)
		}
	}
	m.y0, m.y1 = maxInt(m.y0, o.y0), minInt(m.y1, o.y1)
	if m.y0 >= m.y1 {
		m.y0, m.y1 = 0, 0
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// clone copies the mask, for the clip stack q and Q maintain.
func (m *mask) clone() *mask {
	c := &mask{w: m.w, h: m.h, y0: m.y0, y1: m.y1, a: make([]uint8, len(m.a))}
	copy(c.a, m.a)
	return c
}

// rasterize computes the coverage of a path as a full-page mask.
//
// Used for clips, which have to persist past the operator that set them, and not for fills:
// a fill composites row by row through scan, because a page of table rules is hundreds of
// small paths and a page-sized allocation for each one costs the page's area every time.
// ISO 32000-2 draws 284,866 rules, so the difference is not a micro-optimization.
func (p *path) rasterize(w, h int, evenOdd bool) *mask {
	out := newMask(w, h)
	out.y0, out.y1 = h, 0
	p.scan(w, h, evenOdd, func(py int, row []float64) {
		out.y0, out.y1 = minInt(out.y0, py), maxInt(out.y1, py+1)
		base := py * w
		for x, v := range row {
			if v <= 0 {
				continue
			}
			if v > 1 {
				v = 1
			}
			out.a[base+x] = uint8(math.Round(v * 255))
		}
	})
	if out.y0 >= out.y1 {
		out.y0, out.y1 = 0, 0
	}
	return out
}

// scan computes coverage one pixel row at a time and hands each non-empty row to emit.
//
// The row is reused between calls, so emit must not retain it. That is the whole reason this
// is a callback rather than a slice of rows: a caller either composites the row immediately or
// copies what it needs, and neither wants the page-sized allocation that returning them all
// would take.
//
// evenOdd selects §8.5.3.3.2's even-odd rule over §8.5.3.3.1's nonzero winding. The two differ
// only in how a crossing count becomes an inside test, which is why they are one function with
// a flag rather than two rasterizers: everything expensive — flattening, sorting, span
// accumulation — is shared, and a page that used both would otherwise pay for two
// implementations of it.
func (p *path) scan(w, h int, evenOdd bool, emit func(py int, row []float64)) {
	// The closing edge is computed here and appended to a local slice, so scanning a path leaves
	// it as the caller built it. Closing in place made scan a mutator of its receiver, which
	// combined with rasterizing a clip at the W rather than at the end of the path object to
	// drop every segment after a W: close had set open to false and lineTo takes nothing
	// without a subpath.
	//
	// With the clip taken at the end of the path object, no caller uses a path after scanning
	// it, so closing in place would pass every test in this package today. It stays local
	// because the footgun is what cost the page, not the mutation — and because the next caller
	// is a glyph cache, which will scan one outline many times.
	edges := p.edges
	if p.open && p.cur != p.start {
		edges = append(edges[:len(edges):len(edges)], edge{p.cur.x, p.cur.y, p.start.x, p.start.y})
	}
	if len(edges) == 0 {
		return
	}

	// The path's own bounds, clamped to the page *before* the conversion to int.
	//
	// Before the clamp this read int(math.Max(0, math.Floor(top))), which is the hazard
	// render.Fit documents for its own arithmetic: Go leaves an out-of-range float-to-int
	// conversion implementation-defined, and on amd64 1e19 becomes the most negative int64. A
	// content stream may carry any number the lexer can parse, so "0 -1e19 m" made the row loop
	// below run about 9.2×10¹⁸ times — not a slow page, a hung process, on input a producer
	// controls.
	top, bot := minMax(edges, false)
	left, right := minMax(edges, true)
	y0, y1 := clampRange(top, bot, h)
	x0, x1 := clampRange(left, right, w)
	if y0 >= y1 || x0 >= x1 {
		return
	}
	// One column past the right edge, because a span ending at x.5 marks the pixel at int(x).
	if x1 < w {
		x1++
	}

	// Edges bucketed by the first row they can cross, and an active list carried down the page.
	//
	// Without this every sample of every row rescans every edge, which is the page height times
	// the edge count: a zig-zag of 1,000 segments spanning a US Letter page took 11 seconds at
	// 72 dpi and 10,000 did not finish in ten minutes. A glyph is a few dozen edges over a few
	// rows and does not care, but a page-sized path is exactly what a producer draws for a
	// border or a chart, and refusing to be quadratic is cheaper than explaining why it was.
	buckets := make([][]int, y1-y0)
	for i := range edges {
		lo := math.Min(edges[i].y0, edges[i].y1)
		hi := math.Max(edges[i].y0, edges[i].y1)
		if !(hi > float64(y0)) || !(lo < float64(y1)) {
			continue // entirely above or below the rows this path touches
		}
		row := y0
		if lo > float64(y0) {
			row = int(lo)
		}
		if row < y0 {
			row = y0
		}
		if row >= y1 {
			continue
		}
		buckets[row-y0] = append(buckets[row-y0], i)
	}

	acc := make([]float64, w)
	type crossing struct {
		x   float64
		dir int
	}
	var xs []crossing
	var active []int

	for py := y0; py < y1; py++ {
		active = append(active, buckets[py-y0]...)
		if len(active) == 0 {
			continue
		}
		// Drop the edges this row has passed. Done once per row rather than per sample, which
		// is what keeps the inner loop proportional to the edges actually crossing here.
		keep := active[:0]
		for _, i := range active {
			if math.Max(edges[i].y0, edges[i].y1) > float64(py) {
				keep = append(keep, i)
			}
		}
		active = keep

		clear(acc[x0:x1])
		hit := false
		for s := 0; s < coverSamples; s++ {
			y := float64(py) + (float64(s)+0.5)/coverSamples
			xs = xs[:0]
			for _, i := range active {
				e := edges[i]
				ey0, ey1, ex0, ex1, dir := e.y0, e.y1, e.x0, e.x1, 1
				if ey0 > ey1 {
					ey0, ey1, ex0, ex1, dir = ey1, ey0, ex1, ex0, -1
				}
				// Half-open in y: a scanline exactly on a shared vertex counts the edge
				// below it and not the one above, so the two do not both contribute and a
				// horizontal join does not double-count.
				if y < ey0 || y >= ey1 {
					continue
				}
				t := (y - ey0) / (ey1 - ey0)
				x := ex0 + t*(ex1-ex0)
				// A crossing that is not a number cannot be ordered or indexed, and one
				// arises without any infinite literal: an edge whose y span and x span both
				// overflow to ±Inf gives t = 0 and 0×Inf = NaN. Dropped here, because every
				// comparison below would be false for it and int(NaN) is the most negative
				// int64.
				if math.IsNaN(x) {
					continue
				}
				xs = append(xs, crossing{x, dir})
			}
			if len(xs) < 2 {
				continue
			}
			// Insertion sort while the crossing count is small, which is the case for a glyph
			// or a table rule and the case insertion sort is best at; sort.Slice past that,
			// because insertion sort is quadratic and a self-intersecting chart is not rare.
			if len(xs) <= 32 {
				for i := 1; i < len(xs); i++ {
					for j := i; j > 0 && xs[j].x < xs[j-1].x; j-- {
						xs[j], xs[j-1] = xs[j-1], xs[j]
					}
				}
			} else {
				sort.Slice(xs, func(a, b int) bool { return xs[a].x < xs[b].x })
			}
			wind := 0
			for i := 0; i+1 < len(xs); i++ {
				wind += xs[i].dir
				in := wind != 0
				if evenOdd {
					in = (i+1)%2 == 1
				}
				if !in {
					continue
				}
				if addSpan(acc, xs[i].x, xs[i+1].x, 1.0/coverSamples) {
					hit = true
				}
			}
		}
		if hit {
			emit(py, acc)
		}
	}
}

// minMax returns an edge slice's extent on one axis, x when alongX is set.
func minMax(edges []edge, alongX bool) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, e := range edges {
		a, b := e.y0, e.y1
		if alongX {
			a, b = e.x0, e.x1
		}
		lo = math.Min(lo, math.Min(a, b))
		hi = math.Max(hi, math.Max(a, b))
	}
	return lo, hi
}

// clampRange turns a float extent into a pixel range inside [0, n], safely.
//
// The comparisons carry the whole of the safety, and they are written negated for it: !(hi > 0)
// is true when hi is NaN where hi <= 0 would be false, so a bound that is not a number takes the
// empty branch by the same test that rejects one off the page. An explicit IsNaN check was here
// too and is gone — it could be deleted with every test still passing, which is the definition
// of a check the code already makes somewhere else.
//
// What must not happen is reaching the conversion with an unbounded value: Go leaves an
// out-of-range float-to-int conversion implementation-defined, and on amd64 1e19 becomes the
// most negative int64, which as a loop bound is not a slow page but a hung process.
func clampRange(lo, hi float64, n int) (int, int) {
	if !(hi > 0) || !(lo < float64(n)) {
		return 0, 0
	}
	if lo < 0 {
		lo = 0
	}
	if hi > float64(n) {
		hi = float64(n)
	}
	return int(math.Floor(lo)), int(math.Ceil(hi))
}

// addSpan adds weight to the pixels a scanline interval covers, exactly: the two end pixels
// get the fraction of themselves the interval overlaps and the interior gets all of it.
//
// Reports whether anything landed inside the row, so a scanline entirely off the page does
// not make the caller write a row of zeros.
func addSpan(acc []float64, xa, xb, weight float64) bool {
	w := float64(len(acc))
	if xb <= 0 || xa >= w || xb <= xa {
		return false
	}
	if xa < 0 {
		xa = 0
	}
	if xb > w {
		xb = w
	}
	ia, ib := int(xa), int(xb)
	if ib >= len(acc) {
		ib = len(acc) - 1
	}
	if ia == ib {
		acc[ia] += (xb - xa) * weight
		return true
	}
	acc[ia] += (float64(ia+1) - xa) * weight
	for x := ia + 1; x < ib; x++ {
		acc[x] += weight
	}
	acc[ib] += (xb - float64(ib)) * weight
	return true
}

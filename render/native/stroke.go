package native

import (
	"fmt"
	"math"
	"slices"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// pen is the part of §8.4.3's graphics state that shapes a stroke and that content.Machine does
// not carry: the machine has the line width, and this has the rest. The stroke colour and colour
// space live on the walker instead — colour.go's setColour sets them for both g/rg/k's stroke
// counterparts and cs/sc/scn's — because a fill needs the identical state and one copy answers
// both.
type pen struct {
	cap, join int
	miter     float64

	// dash is the pattern as the dasher walks it, lengths in user space alternately on and off and
	// always an even count of them, or nil for a solid line; phase is how far into it a subpath
	// starts. dashWhy is why the pattern last set cannot be drawn, which checkStroke refuses at the
	// stroke. dashPattern sets all three, from d and from /D alike.
	dash    []float64
	phase   float64
	dashWhy string
}

var defaultPen = pen{miter: 10}

// setPen applies J, j, M or d, the pen geometry that is not a colour: content.Machine carries the
// line width, and colour.go's setColour carries every colour operator. Called from both passes,
// because the survey needs the dash to decide what checkStroke refuses and the paint pass needs
// the geometry itself.
func (w *walker) setPen(op content.Op) {
	switch op.Name {
	case "J":
		if len(op.Operands) >= 1 {
			w.pen.cap = op.Int(0)
		}
	case "j":
		if len(op.Operands) >= 1 {
			w.pen.join = op.Int(0)
		}
	case "M":
		if len(op.Operands) >= 1 {
			w.pen.miter = op.Num(0)
		}
	case "d":
		// A d without an array and a phase is ignored, as pdfium ignores it.
		if len(op.Operands) >= 2 {
			if a, ok := op.Operands[0].(objects.Array); ok {
				w.pen.dash, w.pen.phase, w.pen.dashWhy = dashPattern(w.s, a, op.Operands[1])
			}
		}
	}
}

// maxDashElements is how many elements of a dash array pdfium reads. AGG's vcgen_dash keeps 32
// and drops the rest.
const maxDashElements = 32

// dashPattern reads a dash array and phase, from d or from /D, as the dasher walks them, or says
// why they cannot be drawn.
//
// The pattern is §8.4.3.6's with one exception, which is pdfium's: an element of 0.000001 or less
// is drawn 0.1 unit long. §8.4.3.6 draws a dash of length zero as its caps alone, a dot under round
// caps, and keeps the period the array states. pdfium's lengthens every period by 0.1, so along a
// line its dots drift away from the specification's. The corpus decides between them: WTPDF page 3
// draws 45 dotted leaders as [0 3] with round caps, and drawn as §8.4.3.6 says, the page is further
// from pdfium than it is with no dots at all.
//
// The rule is for gaps too, which is measured and not read: RasterizeStroke, in pdfium's
// cfx_agg_devicedriver.cpp, lengthens only the dash, but the pdfium this repo compares against
// draws [4 0] and [4 0.0000009] exactly as [4 0.1], at every resolution, and [4 0.0000011] as a
// solid line. A gap of zero is a seam of 0.1 unit where §8.4.3.6 draws none.
//
// Everywhere else the two disagree, the pattern is refused rather than drawn either way:
//
//   - an odd count of three or more, which §8.4.3.6 repeats whole and pdfium pairs as (a b)(c c)
//   - more than 32 elements, where pdfium drops the rest
//   - a negative element, which §8.4.3.6 forbids and pdfium draws 0.1 long
//   - every element zero, which §8.4.3.6 forbids and pdfium draws as dashes and gaps of 0.1
//   - an element or a phase that is not a number, which pdfium reads as 0
//   - an element or a phase past float32's range, in which pdfium holds a dash pattern
//
// A negative phase is not among them. §8.4.3.6 adds twice the period until it is positive, and
// pdfium does the same.
func dashPattern(s objects.Store, a objects.Array, phase objects.Object) (dash []float64, ph float64, why string) {
	if len(a) == 0 {
		return nil, 0, ""
	}
	if len(a) > maxDashElements {
		return nil, 0, fmt.Sprintf("a dash array of %d elements, past the %d pdfium reads", len(a), maxDashElements)
	}
	if len(a)%2 == 1 && len(a) > 1 {
		return nil, 0, fmt.Sprintf("a dash array of odd length %d, which §8.4.3.6 repeats and pdfium pairs", len(a))
	}
	num := func(o objects.Object) (float64, bool) {
		r, _ := s.Resolve(o)
		return objects.AsNum(r)
	}
	ph, ok := num(phase)
	if !ok {
		return nil, 0, "a dash phase that is not a number"
	}
	if math.Abs(ph) > math.MaxFloat32 {
		return nil, 0, fmt.Sprintf("a dash phase of %g, past the float range pdfium holds it in", ph)
	}
	zero := true
	for _, o := range a {
		v, ok := num(o)
		if !ok {
			return nil, 0, "a dash element that is not a number"
		}
		if v < 0 {
			return nil, 0, fmt.Sprintf("a negative dash element, %g", v)
		}
		if v > math.MaxFloat32 {
			return nil, 0, fmt.Sprintf("a dash element of %g, past the float range pdfium holds it in", v)
		}
		zero = zero && v == 0
		dash = append(dash, v)
	}
	if zero {
		return nil, 0, "a dash array of zeros"
	}
	if len(dash) == 1 {
		dash = append(dash, dash[0]) // [a] is a on, a off, and the two readings agree
	}
	for i := range dash {
		if dash[i] <= 0.000001 { // pdfium's own threshold
			dash[i] = 0.1
		}
	}
	return dash, ph, ""
}

// period is the length of one repeat of a dash pattern.
func period(dash []float64) float64 {
	p := 0.0
	for _, v := range dash {
		p += v
	}
	return p
}

// maxDashes bounds the dashes one page may draw, counting a form each time it is drawn.
//
// A stroke's dashes are its length over the pattern's period, which nothing in the file bounds:
// one l a million units long dashed [0.1 0.1] is five million dashes, and one operator. A dash
// is a segment and two caps where a solid stroke's l is a segment and a join, so this bound is
// about two million of maxOps' operators, and tighter than it.
const maxDashes = 1 << 20

// checkDash charges the page for the dashes this stroke can draw, and refuses the dashed subpath
// the survey can see that pdfium and this dasher draw differently.
//
// That subpath is a closed one that goes nowhere. pdfium strokes the path traced gives it, so the
// subpath is a closed line a pixel long, which AGG's dasher walks there and back as one open dash:
// two caps at its start, which this dasher joins as §8.4.3.3 asks (dashed). An open one that goes
// nowhere is drawn alike by both, a pixel long or not at all.
//
// The charge is dashBound's, computed from the path the paint pass will stroke, which the survey
// builds for the purpose (buildPath): a bound taken from a second reading of the operators could
// disagree with the path it bounds.
func (w *walker) checkDash(m *content.Machine, refuse func(string, ...any)) {
	s, ok := newStroker(nil, m.GS.LineWidth, w.base.mul(m.GS.CTM).m, w.pen)
	if !ok {
		return // a stroke with no area, which paint does not dash
	}
	rec := w.path.rec.points(w.pen.cap == 1)
	for _, sub := range subpathsOf(rec) {
		if nowhere(sub) && slices.ContainsFunc(sub, func(q ppoint) bool { return q.close }) {
			refuse("a closed dashed subpath of zero length, which pdfium dashes open")
		}
	}
	p := traced(rec, w.base.mul(m.GS.CTM).m)
	for _, sp := range p.subpaths() {
		if pts := s.points(p.edges[sp.from:sp.to], sp); len(pts) > 1 {
			w.dashes += s.dashBound(pts, sp.closed)
		}
	}
	if !(w.dashes <= maxDashes) {
		refuse("the page's dashes run past %d", maxDashes)
	}
}

// checkStroke refuses a stroke this backend cannot draw, by reason.
//
// Checked at the stroke rather than where the state was set, for the reason stroke colours are
// allowed at all: setting a dash or a colour space that no stroke uses changes no pixel.
func (w *walker) checkStroke(m *content.Machine) {
	refuse := func(f string, a ...any) { w.unsup["stroke: "+fmt.Sprintf(f, a...)] = true }
	if r := w.refusal(w.strokeSpace); r != "" {
		refuse("%s", r)
	}
	if w.pen.dashWhy != "" {
		refuse("%s", w.pen.dashWhy)
	} else if w.pen.dash != nil {
		w.checkDash(m, refuse)
	}
	if w.pen.cap < 0 || w.pen.cap > 2 {
		refuse("line cap %d is not 0, 1 or 2", w.pen.cap)
	}
	if w.pen.join < 0 || w.pen.join > 2 {
		refuse("line join %d is not 0, 1 or 2", w.pen.join)
	}
	if lw := m.GS.LineWidth; !(lw >= 0) {
		refuse("line width %g", lw)
	}
}

// strokeTolerance is how far, in device pixels, a flattened round cap or join may fall inside
// the true arc. The same fifth of a pixel curveTo flattens to, halved, because an arc's error
// is on the outside of a mark where the eye finds it first.
const strokeTolerance = 0.1

// stroke returns the outline of p stroked with a pen of width w under the linear part of ctm,
// as a path whose nonzero fill is the stroke.
//
// # Why in pen space
//
// §8.4.3.2 makes the line width a distance in user space, so under a CTM that scales x and y
// differently the pen is an ellipse on the page, and its axes need not be the page's. The path
// is kept in device space, so each point is taken back through the inverse of the CTM's linear
// part, stroked there with a round pen, and brought forward again. That is exact — a linear map
// takes the circle to the ellipse and every offset with it — where offsetting in device space
// by a transformed width is exact only along the ellipse's axes.
//
// # Why pieces
//
// Every segment, join and cap is a convex polygon wound the same way, so their union is what
// nonzero fills and the overlaps where they meet count once: a winding number is one or more
// inside any of them and zero outside all of them. Stitching one outline around the whole
// stroke is what a renderer does to save edges, and it is where the inner side of a sharp join
// folds back over itself; pieces have no inner side.
func stroke(p *path, w float64, ctm geom.Matrix, pn pen) *path {
	out := &path{}
	s, ok := newStroker(out, w, ctm, pn)
	if !ok {
		return out
	}
	for _, sp := range p.subpaths() {
		s.subpath(p.edges[sp.from:sp.to], sp)
	}
	return out
}

// newStroker is the stroker that adds to out the pieces of a stroke of width w under ctm, or false
// when ctm collapses the plane.
func newStroker(out *path, w float64, ctm geom.Matrix, pn pen) (stroker, bool) {
	a, b, c, d := ctm.A, ctm.B, ctm.C, ctm.D
	det := a*d - b*c
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		// A CTM that collapses the plane collapses the pen with it, and the stroke has no area.
		return stroker{}, false
	}
	// No line is drawn thinner than one device pixel, which is §8.4.3.2's reading of a width of 0
	// and pdfium's of every width below a pixel. Measured before it was adopted: a 0.25pt line at
	// 72 dpi covered a quarter of its row here and all of one row in pdfium, and the corpus draws
	// 612 of its strokes at 0.5pt or less. The pixel is measured as pdfium measures it, by the
	// mean of the CTM's two axis scales, so the floor agrees under a non-uniform CTM too.
	if unit := 2 / (math.Hypot(a, b) + math.Hypot(c, d)); !(w >= unit) {
		w = unit
	}
	hw := w / 2
	return stroker{
		out: out, hw: hw, pen: pn,
		fwd:  func(q point) point { return point{a*q.x + c*q.y, b*q.x + d*q.y} },
		back: func(q point) point { return point{(d*q.x - c*q.y) / det, (a*q.y - b*q.x) / det} },
		rdev: hw * math.Max(math.Hypot(a, b), math.Hypot(c, d)),
	}, true
}

type stroker struct {
	out       *path
	hw        float64
	pen       pen
	fwd, back func(point) point
	rdev      float64 // the pen's largest radius on the device, which sets how finely arcs flatten
}

// subpath strokes one subpath's edges.
func (s *stroker) subpath(edges []edge, sp subpath) {
	pts := s.points(edges, sp)
	switch {
	case len(pts) > 1 && s.pen.dash != nil:
		s.dashed(pts, sp.closed)
	case len(pts) > 1:
		// AGG's vcgen_stroke strokes a closed figure of two points open, with its caps.
		s.polyline(pts, sp.closed && len(pts) > 2)
	}
	// A subpath that goes nowhere, which traced has not made a pixel long, AGG does not draw: §8.5.3.2
	// paints it under round caps as a dot.
}

// nowhere is whether every point of a recorded subpath is the one it starts at.
func nowhere(sub []ppoint) bool {
	return !slices.ContainsFunc(sub, func(q ppoint) bool { return q.at != sub[0].at })
}

// points is a subpath's points in pen space, each distinct from the one before it, without a
// closed subpath's last point when that is its first again.
func (s *stroker) points(edges []edge, sp subpath) []point {
	pts := []point{s.back(sp.at)}
	for _, e := range edges {
		if q := s.back(point{e.x1, e.y1}); q != pts[len(pts)-1] {
			pts = append(pts, q)
		}
	}
	if sp.closed && len(pts) > 1 && pts[len(pts)-1] == pts[0] {
		pts = pts[:len(pts)-1]
	}
	return pts
}

// polyline strokes two or more points, each distinct from the one before it: a segment between
// each pair, a join at each corner, and caps at the ends of an open one or a join where a closed
// one meets itself.
func (s *stroker) polyline(pts []point, closed bool) {
	n := len(pts)
	segs := n - 1
	if closed {
		segs = n
	}
	for i := 0; i < segs; i++ {
		s.segment(pts[i], pts[(i+1)%n])
	}
	for i := 1; i < segs; i++ {
		s.joinAt(pts[i-1], pts[i], pts[(i+1)%n])
	}
	if closed {
		s.joinAt(pts[n-1], pts[0], pts[1])
		return
	}
	s.capAt(pts[0], unit(pts[1], pts[0]))
	s.capAt(pts[n-1], unit(pts[n-2], pts[n-1]))
}

// dashed strokes a subpath's points in §8.4.3.6's dashes. Each dash is an open polyline, capped at
// both ends and joined at the corners inside it, and the pattern starts again at every subpath.
//
// The walk is in pen space, which is user space up to a translation, because that is where
// §8.4.3.6 measures a dash; pdfium dashes in user space too. A dash that ends exactly where a
// segment does ends there and takes no join, which is AGG's vcgen_dash's strict comparison.
//
// A closed subpath whose pattern is in a dash when it comes back to its start joins that dash to
// the one it began with, as §8.4.3.3 asks. A dash that ends exactly there is not in it, and takes no
// join, as at a corner. This is the one place the dasher follows the
// specification where pdfium does not: AGG ends one dash there and begins the other, both capped.
// The survey cannot see the difference coming without dashing the path, and no corpus page strokes
// a closed dashed subpath.
func (s *stroker) dashed(pts []point, closed bool) {
	if closed {
		pts = append(pts[:len(pts):len(pts)], pts[0])
	}
	d := s.pen.dash
	i, left := dashStart(d, s.pen.phase)
	on := i%2 == 0
	var first, cur []point // first is a closed subpath's opening dash, held for the last one
	broken := false        // whether any dash or gap has ended
	if on {
		cur = []point{pts[0]}
	}
	for k := 0; k+1 < len(pts); k++ {
		a, b := pts[k], pts[k+1]
		l := math.Hypot(b.x-a.x, b.y-a.y)
		t := 0.0
		for l-t > left {
			t += left
			q := point{a.x + (b.x-a.x)*t/l, a.y + (b.y-a.y)*t/l}
			if on {
				cur = append(cur, q)
				if closed && !broken {
					first = cur
				} else {
					s.dash(cur)
				}
				cur = nil
			} else {
				cur = []point{q}
			}
			broken = true
			i = (i + 1) % len(d)
			on, left = !on, d[i]
		}
		// A dash that ends where the segment does ends there, and takes no join to the next one.
		// The points came back from device space through the inverse CTM, so an end the file puts
		// exactly on a corner arrives a rounding either side of it; this is the side that would
		// carry the dash round the corner by that rounding, and draw the whole join.
		if left -= l - t; left < 1e-9*l {
			left = 0
		}
		if on {
			cur = append(cur, b)
		}
	}
	if !broken {
		// The whole subpath fell in one dash, which strokes it solid, or in one gap.
		if on && closed && left > 0 {
			s.polyline(pts[:len(pts)-1], true)
		} else if on {
			s.polyline(pts, false)
		}
		return
	}
	if on && first != nil && left > 0 {
		cur, first = append(cur, first[1:]...), nil
	}
	if on {
		s.dash(cur)
	}
	if first != nil {
		s.dash(first)
	}
}

// dashStart is where a subpath begins in a dash pattern: the element the phase falls in, and how
// much of that element is left.
//
// The phase is taken modulo the period, which is also §8.4.3.6's rule for a negative one: adding
// twice the period until it is positive leaves the same remainder. A phase that ends an element
// exactly begins the next, so a pattern and its rotation start alike; AGG's calc_dash_start stays
// in the element with nothing left, which differs only at a closed subpath's start.
func dashStart(d []float64, phase float64) (i int, left float64) {
	p := period(d)
	ph := math.Mod(phase, p)
	if ph < 0 {
		ph += p
	}
	for ph >= d[i] {
		ph -= d[i]
		i = (i + 1) % len(d)
	}
	return i, d[i] - ph
}

// dash strokes one dash, dropping any point that repeats the one before it. A dash that is one
// point after that has no direction for its caps, and draws nothing.
func (s *stroker) dash(c []point) {
	pts := c[:1]
	for _, q := range c[1:] {
		if q != pts[len(pts)-1] {
			pts = append(pts, q)
		}
	}
	if len(pts) > 1 {
		s.polyline(pts, false)
	}
}

// dashBound is the most dashes dashed can draw along pts: len(dash)/2 in each period of the
// subpath's length, and as many again for the partial period at each end.
func (s *stroker) dashBound(pts []point, closed bool) float64 {
	l := 0.0
	for i := 0; i+1 < len(pts); i++ {
		l += hypot(pts[i], pts[i+1])
	}
	if closed {
		l += hypot(pts[len(pts)-1], pts[0])
	}
	return float64(len(s.pen.dash)/2) * (l/period(s.pen.dash) + 2)
}

func unit(from, to point) point {
	l := math.Hypot(to.x-from.x, to.y-from.y)
	return point{(to.x - from.x) / l, (to.y - from.y) / l}
}

// left is the normal to a direction on its left, scaled to the pen's radius.
func (s *stroker) left(dir point) point { return point{-dir.y * s.hw, dir.x * s.hw} }

func add(a, b point) point { return point{a.x + b.x, a.y + b.y} }

func sub(a, b point) point { return point{a.x - b.x, a.y - b.y} }

func (s *stroker) segment(a, b point) {
	n := s.left(unit(a, b))
	s.poly(add(a, n), add(b, n), sub(b, n), sub(a, n))
}

// joinAt fills the wedge at b between the segment arriving from a and the one leaving for c, on
// the outside of the turn. The inside is already covered by the two segments.
func (s *stroker) joinAt(a, b, c point) {
	d1, d2 := unit(a, b), unit(b, c)
	cross := d1.x*d2.y - d1.y*d2.x
	dot := d1.x*d2.x + d1.y*d2.y
	if cross == 0 && dot > 0 {
		return // straight on: the segments already meet edge to edge
	}
	// The outside of a left turn is on the right, and the wedge sweeps from the arriving
	// segment's offset to the leaving one's through the turn angle.
	o1, o2, sweep := s.left(d1), s.left(d2), -math.Atan2(math.Abs(cross), dot)
	if cross > 0 {
		o1, o2, sweep = point{-o1.x, -o1.y}, point{-o2.x, -o2.y}, -sweep
	}
	switch s.pen.join {
	case 1:
		s.arc(b, o1, sweep)
	case 0:
		// The miter is where the two offset edges meet, |o1+o2|/(1+cos θ) from b, and the ratio
		// §8.4.3.5 limits is its length over the width: 1/sin(φ/2) for the angle φ between the
		// segments, which is 2/(1+cos θ) squared for the turn θ. Compared squared, so a right
		// angle's √2 is not a rounding away from its own limit.
		if dot > -1 && 2/(1+dot) <= s.pen.miter*s.pen.miter {
			k := 1 / (1 + dot)
			s.poly(b, add(b, o1), add(b, point{(o1.x + o2.x) * k, (o1.y + o2.y) * k}), add(b, o2))
			return
		}
		s.poly(b, add(b, o1), add(b, o2))
	default:
		s.poly(b, add(b, o1), add(b, o2))
	}
}

// capAt ends an open subpath at p, where dir points out of the stroke.
func (s *stroker) capAt(p, dir point) {
	n := s.left(dir)
	switch s.pen.cap {
	case 1:
		s.arc(p, n, -math.Pi)
	case 2:
		f := point{dir.x * s.hw, dir.y * s.hw}
		s.poly(add(p, n), add(add(p, n), f), add(sub(p, n), f), sub(p, n))
	}
}

// arc adds the fan at centre from offset o through sweep radians.
func (s *stroker) arc(centre, o point, sweep float64) {
	steps := 64.0
	if s.rdev <= strokeTolerance {
		steps = 1
	} else if s.rdev < 1e6 {
		steps = math.Ceil(math.Abs(sweep) / (2 * math.Acos(1-strokeTolerance/s.rdev)))
	}
	if !(steps >= 1) {
		steps = 1
	}
	steps = math.Min(steps, 64)
	pts := make([]point, 0, int(steps)+2)
	pts = append(pts, centre)
	for i := 0.0; i <= steps; i++ {
		sn, cs := math.Sincos(sweep * i / steps)
		pts = append(pts, add(centre, point{o.x*cs - o.y*sn, o.x*sn + o.y*cs}))
	}
	s.poly(pts...)
}

// poly adds one convex piece in pen space, taken to the device and wound positively there.
func (s *stroker) poly(pts ...point) {
	for i := range pts {
		pts[i] = s.fwd(pts[i])
	}
	area := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		area += pts[i].x*pts[j].y - pts[j].x*pts[i].y
	}
	if area == 0 {
		return
	}
	if area < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	s.out.moveTo(pts[0])
	for _, q := range pts[1:] {
		s.out.lineTo(q)
	}
	s.out.close()
}

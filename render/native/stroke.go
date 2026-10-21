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

// innerMiter is the miter limit of the side of a join inside its turn: vcgen_stroke's
// m_inner_miter_limit, which pdfium leaves at AGG's 1.01.
const innerMiter = 1.01

// maxArcSteps bounds the chords of one round cap or join. AGG steps an arc by an angle that falls
// as the pen grows, with no floor for a join, so a pen a million pixels wide would cut each join
// into thousands of chords. Past a radius of about 415 pixels in matrix1's space, where AGG's chords
// fall an eighth of a pixel inside the arc, this backend's fall 0.0003 of the radius inside it: a
// fifth of a pixel at a radius of 650, and three pixels at 10,000.
const maxArcSteps = 64

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
// pdfium strokes in a space of its own too, CFX_AggDeviceDriver::DrawPath's matrix1: the user
// space under a uniform scale of max(|a|, |b|) of the CTM, which is this one scaled. The stroke's
// shape is the same in both. Only how finely an arc is cut depends on the scale, so the stroker
// keeps it for that (newStroker).
//
// # Why AGG's outline
//
// The outline is the one AGG's vcgen_stroke makes, which is what pdfium strokes with: one polygon
// around each open polyline, which is a cap, the joins along one side, the other cap and the joins
// back along the other side; and for a closed one, two polygons, one for each side. On the side
// inside a turn, a join is the miter when that is within 1.01 of the pen's radius. Otherwise it is
// the two segments' ends, so at a sharp turn the outline folds back over itself.
//
// pdfium fills the outline with AGG's nonzero rule, which sums the coverage of each outline before
// it clamps it, and scan does the same. So a fold counts twice, and so do two subpaths or two
// dashes where they overlap. Under a translucent stroke this changes no pixel inside the stroke.
// At its edges, an edge pixel two outlines both cover counts as their sum, as in pdfium.
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

// newStroker is the stroker that adds to out the outline of a stroke of width w under ctm, or false
// when ctm collapses the plane.
func newStroker(out *path, w float64, ctm geom.Matrix, pn pen) (stroker, bool) {
	a, b, c, d := ctm.A, ctm.B, ctm.C, ctm.D
	det := float64(a*d) - float64(b*c)
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
	// An arc's step is AGG's for its pen radius in matrix1's space, where an eighth of a pixel is
	// as far as a chord falls from the radius it is cut at.
	r := hw * math.Max(math.Abs(a), math.Abs(b))
	return stroker{
		out: out, hw: hw, pen: pn,
		fwd: func(q point) point { return point{float64(a*q.x) + float64(c*q.y), float64(b*q.x) + float64(d*q.y)} },
		back: func(q point) point {
			return point{(float64(d*q.x) - float64(c*q.y)) / det, (float64(a*q.y) - float64(b*q.x)) / det}
		},
		da: math.Max(2*math.Acos(r/(r+1.0/8)), math.Pi/maxArcSteps),
	}, true
}

type stroker struct {
	out       *path
	hw        float64
	pen       pen
	fwd, back func(point) point
	da        float64 // the angle a round cap or join steps by
	ring      []point // the outline being built, in pen space
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

// polyline strokes two or more points, each distinct from the one before it, in the order
// vcgen_stroke's vertex takes them. An open one is one polygon: a cap, the joins out, the other cap
// and the joins back. A closed one is two polygons, the joins around each side.
func (s *stroker) polyline(pts []point, closed bool) {
	n := len(pts)
	if closed {
		for i := range n {
			s.join(pts[(i+n-1)%n], pts[i], pts[(i+1)%n])
		}
		s.end()
		for i := n - 1; i >= 0; i-- {
			s.join(pts[(i+1)%n], pts[i], pts[(i+n-1)%n])
		}
		s.end()
		return
	}
	s.capAt(pts[0], pts[1])
	for i := 1; i < n-1; i++ {
		s.join(pts[i-1], pts[i], pts[i+1])
	}
	s.capAt(pts[n-1], pts[n-2])
	for i := n - 2; i > 0; i-- {
		s.join(pts[i+1], pts[i], pts[i-1])
	}
	s.end()
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

// offsets are AGG's two offsets of the segment from a to b, and of the next one from b to c, as
// stroke_calc_join computes them: each the segment's direction turned a quarter, scaled to the
// pen's radius, and kept as AGG keeps it, as (dx, dy) for the offset (dx, -dy).
func (s *stroker) offset(a, b point) point {
	l := hypot(a, b)
	return point{s.hw * (b.y - a.y) / l, s.hw * (b.x - a.x) / l}
}

// join is stroke_calc_join: the vertices at v1 between the segment from v0 and the one to v2. The
// side inside the turn takes a miter within innerMiter of the radius, or else the two segments'
// ends. The side outside takes the pen's join, which for a miter over its limit is the bevel:
// RasterizeStroke gives AGG miter_join_revert for every join that is not round or bevel.
func (s *stroker) join(v0, v1, v2 point) {
	d1, d2 := s.offset(v0, v1), s.offset(v1, v2)
	// calc_point_location: which side of the segment from v0 v2 is on.
	// Each product is rounded before the difference, as AGG's are, because arm64 fuses p*q-r*s into
	// one multiply-subtract: at an exact reversal the two products are equal, and fused they leave a
	// residue that turns the join to the wrong side.
	if float64((v2.x-v1.x)*(v1.y-v0.y))-float64((v2.y-v1.y)*(v1.x-v0.x)) > 0 {
		s.miter(v0, v1, v2, d1, d2, innerMiter)
		return
	}
	switch s.pen.join {
	case 1:
		s.arc(v1, point{d1.x, -d1.y}, point{d2.x, -d2.y})
	case 2:
		s.vertex(point{v1.x + d1.x, v1.y - d1.y})
		s.vertex(point{v1.x + d2.x, v1.y - d2.y})
	default:
		s.miter(v0, v1, v2, d1, d2, s.pen.miter)
	}
}

// miter is stroke_calc_miter under miter_join_revert. The vertex is where the two offset edges
// meet, when that is within limit radii of v1. Otherwise the join is the two segments' ends.
//
// Where the edges are parallel and the path goes straight on, stroke_calc_miter adds the one end
// the segments share. The two ends are the same point to within rounding, so this adds both.
//
// limit is §8.4.3.5's ratio, the miter's length over the line's width, which is the meeting point's
// distance from v1 over the pen's radius. So one exactly at its limit is kept, as §8.4.3.5's
// "exceeds" asks.
func (s *stroker) miter(v0, v1, v2, d1, d2 point, limit float64) {
	p1, p2 := point{v1.x + d1.x, v1.y - d1.y}, point{v1.x + d2.x, v1.y - d2.y}
	if x, ok := intersection(point{v0.x + d1.x, v0.y - d1.y}, p1, p2, point{v2.x + d2.x, v2.y - d2.y}); ok && hypot(v1, x) <= s.hw*limit {
		s.vertex(x)
		return
	}
	s.vertex(p1)
	s.vertex(p2)
}

// intersection is calc_intersection: where the line through a and b meets the line through c and
// d, or false when they are parallel to within AGG's 1e-30.
func intersection(a, b, c, d point) (point, bool) {
	num := float64((a.y-c.y)*(d.x-c.x)) - float64((a.x-c.x)*(d.y-c.y))
	den := float64((b.x-a.x)*(d.y-c.y)) - float64((b.y-a.y)*(d.x-c.x))
	if math.Abs(den) < 1e-30 {
		return point{}, false
	}
	return point{a.x + (b.x-a.x)*num/den, a.y + (b.y-a.y)*num/den}, true
}

// capAt is stroke_calc_cap: the vertices that end a polyline at v0, whose segment goes to v1.
func (s *stroker) capAt(v0, v1 point) {
	d := s.offset(v0, v1)
	if s.pen.cap != 1 {
		var e point // how far a square cap reaches past v0
		if s.pen.cap == 2 {
			e = point{d.y, d.x}
		}
		s.vertex(point{v0.x - d.x - e.x, v0.y + d.y - e.y})
		s.vertex(point{v0.x + d.x - e.x, v0.y - d.y - e.y})
		return
	}
	a1 := math.Atan2(d.y, -d.x)
	s.vertex(point{v0.x - d.x, v0.y + d.y})
	for a, a2 := a1+s.da, a1+math.Pi-s.da/4; a < a2; a += s.da {
		s.vertex(s.around(v0, a))
	}
	s.vertex(point{v0.x + d.x, v0.y - d.y})
}

func add(a, b point) point { return point{a.x + b.x, a.y + b.y} }

// arc is stroke_calc_arc: the vertices of the arc about c from offset o1 to offset o2, a step of
// da apart and none within a quarter step of its end. It turns the way the angle grows, unless o1's
// angle is within a half turn above o2's.
//
// A join's offsets turn with the path, so a join outside a turn takes the other way only where
// join's side test and the two angles disagree. That happens where three points are in a line to
// within rounding, and the arc there is its two ends: turning the way the angle grows drew nearly a
// whole circle. It also happens where a path turns back to within rounding of a reversal.
//
// Where a path goes back to its own start, the angles differ by ±π in float64, and the arc turns
// the way the angle grows. AGG's float32 can put the difference just under π, and then the arc goes
// back over the line: pdfium leaves a round join's tip flat in some directions of a reversal, where
// this backend draws it round (ADR 0028).
func (s *stroker) arc(c, o1, o2 point) {
	a1, a2 := math.Atan2(o1.y, o1.x), math.Atan2(o2.y, o2.x)
	s.vertex(add(c, o1))
	if a1-a2 > 0 && a1-a2 < math.Pi {
		for a1, a2 = a1-s.da, a2+s.da/4; a1 > a2; a1 -= s.da {
			s.vertex(s.around(c, a1))
		}
	} else {
		if a1 > a2 {
			a2 += 2 * math.Pi
		}
		for a1, a2 = a1+s.da, a2-s.da/4; a1 < a2; a1 += s.da {
			s.vertex(s.around(c, a1))
		}
	}
	s.vertex(add(c, o2))
}

// around is the point at angle a on the pen's circle about c.
func (s *stroker) around(c point, a float64) point {
	sn, cs := math.Sincos(a)
	return point{c.x + s.hw*cs, c.y + s.hw*sn}
}

// vertex adds a vertex to the outline being built.
func (s *stroker) vertex(q point) { s.ring = append(s.ring, q) }

// end closes the outline being built, taken to the device, as a polygon of the stroke's path.
func (s *stroker) end() {
	for i, q := range s.ring {
		if i == 0 {
			s.out.moveTo(s.fwd(q))
		} else {
			s.out.lineTo(s.fwd(q))
		}
	}
	s.out.close()
	s.ring = s.ring[:0]
}

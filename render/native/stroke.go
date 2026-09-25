package native

import (
	"fmt"
	"math"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/geom"
)

// pen is the part of §8.4.3's graphics state that shapes a stroke and that content.Machine does
// not carry: the machine has the line width, and this has the rest.
type pen struct {
	cap, join int
	miter     float64

	// dashed is set by a non-empty dash array, which the survey refuses at the stroke: no page the
	// corpus draws sets one, so a dasher here would be code with nothing to measure it against.
	dashed bool

	// space is the stroke colour space's name when it is not one of the three device spaces, and
	// empty when it is; n is the device space's component count, which is what SC and SCN read.
	space string
	n     int
}

var defaultPen = pen{miter: 10, n: 1}

// setPen applies an operator that sets stroke state, in both passes: the survey needs the colour
// space and the dash to decide what it refuses, and the paint pass needs all of it.
func (w *walker) setPen(op content.Op) {
	nums := func() []float64 {
		v := make([]float64, 0, len(op.Operands))
		for i := range op.Operands {
			v = append(v, clamp01(op.Num(i)))
		}
		return v
	}
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
		if len(op.Operands) >= 1 {
			w.pen.dashed = len(op.Arr(0)) > 0
		}
	case "G", "RG", "K":
		// Each sets the space along with the colour (§8.6.8).
		w.pen.space, w.pen.n = "", map[string]int{"G": 1, "RG": 3, "K": 4}[op.Name]
		w.stroke = deviceColour(nums(), w.pen.n, w.stroke)
	case "CS":
		if len(op.Operands) < 1 {
			return
		}
		// A device space's initial colour is black in all three (§8.6.5.2 and following).
		name := string(op.NameAt(0))
		n := map[string]int{"DeviceGray": 1, "DeviceRGB": 3, "DeviceCMYK": 4}[name]
		w.pen.space, w.pen.n = "", n
		if n == 0 {
			w.pen.space = name
		}
		w.stroke = black
	case "SC", "SCN":
		if w.pen.space == "" {
			w.stroke = deviceColour(nums(), w.pen.n, w.stroke)
		}
	}
}

// deviceColour reads n components as a device colour, or keeps the colour it had when the
// operator carried too few, which is a malformed stream with no better reading.
func deviceColour(v []float64, n int, was paint) paint {
	if len(v) < n {
		return was
	}
	switch n {
	case 1:
		return gray(v[0])
	case 3:
		return rgb(v[0], v[1], v[2])
	case 4:
		return cmyk(v[0], v[1], v[2], v[3])
	}
	return was
}

// checkStroke refuses a stroke this backend cannot draw, by reason.
//
// Checked at the stroke rather than where the state was set, for the reason stroke colours are
// allowed at all: setting a dash or a colour space that no stroke uses changes no pixel.
func (w *walker) checkStroke(m *content.Machine) {
	refuse := func(f string, a ...any) { w.unsup["stroke: "+fmt.Sprintf(f, a...)] = true }
	if w.pen.space != "" {
		refuse("the colour space is /%s, and only the device spaces are implemented", w.pen.space)
	}
	if w.pen.dashed {
		// None of the pages the corpus draws sets a dash, so a dasher would have nothing to be
		// measured against.
		refuse("a dash pattern")
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
	a, b, c, d := ctm.A, ctm.B, ctm.C, ctm.D
	det := a*d - b*c
	out := &path{}
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		// A CTM that collapses the plane collapses the pen with it, and the stroke has no area.
		return out
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
	s := stroker{
		out: out, hw: hw, pen: pn,
		fwd:  func(q point) point { return point{a*q.x + c*q.y, b*q.x + d*q.y} },
		back: func(q point) point { return point{(d*q.x - c*q.y) / det, (a*q.y - b*q.x) / det} },
		rdev: hw * math.Max(math.Hypot(a, b), math.Hypot(c, d)),
	}
	subs := p.subs
	if p.open {
		subs = append(subs[:len(subs):len(subs)], subpath{from: p.from, to: len(p.edges), at: p.start})
	}
	for _, sp := range subs {
		s.subpath(p.edges[sp.from:sp.to], sp)
	}
	return out
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
	pts := []point{s.back(sp.at)}
	for _, e := range edges {
		if q := s.back(point{e.x1, e.y1}); q != pts[len(pts)-1] {
			pts = append(pts, q)
		}
	}
	if sp.closed && len(pts) > 1 && pts[len(pts)-1] == pts[0] {
		pts = pts[:len(pts)-1]
	}
	if len(pts) == 1 {
		// A subpath that goes nowhere is painted only with round caps, as a dot (§8.5.3.2). One
		// with no segment at all is a lone m, which marks nothing, unless the stream closed it.
		if s.pen.cap == 1 && (sp.closed || len(edges) > 0) {
			s.arc(pts[0], point{s.hw, 0}, 2*math.Pi)
		}
		return
	}

	n := len(pts)
	segs := n - 1
	if sp.closed {
		segs = n
	}
	for i := 0; i < segs; i++ {
		s.segment(pts[i], pts[(i+1)%n])
	}
	for i := 1; i < segs; i++ {
		s.joinAt(pts[i-1], pts[i], pts[(i+1)%n])
	}
	if sp.closed {
		s.joinAt(pts[n-1], pts[0], pts[1])
		return
	}
	s.capAt(pts[0], unit(pts[1], pts[0]))
	s.capAt(pts[n-1], unit(pts[n-2], pts[n-1]))
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

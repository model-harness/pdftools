package native

import (
	"math"

	"github.com/model-harness/pdftools/geom"
)

// pkind is a point's type in pdfium's CFX_Path.
type pkind uint8

const (
	pmove pkind = iota
	pline
	pbezier
)

// ppoint is a point of a path as pdfium's content parser records it: an operand pair in float32, in
// user space, with its type and whether it closes its figure. dev is the same operands under the CTM,
// in float64, which is where the edges put them and what the clip's rule was measured against.
type ppoint struct {
	at    pt32
	dev   point
	kind  pkind
	close bool
}

// operand is the point (x, y) under ctm, as both the recording and the edges take it.
func operand(x, y float64, ctm matrix) ppoint {
	return ppoint{at: pt32{float32(x), float32(y)}, dev: ctm.apply(point{x, y})}
}

// recording is a path as CPDF_StreamContentParser records it, which is what every question
// CFX_RenderDevice::DrawPath asks of a path is asked of: how many points it has, whether they make a
// rectangle, and which subpaths have no area. The answers turn on the parser's own bookkeeping, so it
// is kept here as the parser keeps it, beside the device-space edges this package draws:
//
//   - a move after an open move replaces it, and a trailing open move is dropped when the path is
//     painted;
//   - a line or curve with no point before it records nothing;
//   - h adds a line back to the start only when the pen is away from it, and marks the last point
//     closed otherwise; b* adds that line either way;
//   - re is a move, three lines and a closing line, its far corner summed in float32.
type recording struct {
	pts        []ppoint
	start, cur ppoint
}

// add is AddPathPoint, for a point of kind k.
func (r *recording) add(p ppoint, k pkind) {
	p.kind, p.close = k, false
	n := len(r.pts)
	r.cur = p
	if k == pmove {
		r.start = p
		if n > 0 && r.pts[n-1].kind == pmove && !r.pts[n-1].close {
			r.pts[n-1] = p
			return
		}
	} else if n == 0 {
		return
	}
	r.pts = append(r.pts, p)
}

// addClose is AddPathPointAndClose: a line back to the start that closes its figure, which is b*
// on its own and h when the pen is away from the start.
//
// On an empty recording it adds nothing, as AddPathPointAndClose adds nothing to an empty path, so a
// clip set on it is no clip (endPath).
func (r *recording) addClose() {
	p := r.start
	p.kind, p.close = pline, true
	r.cur = p
	if len(r.pts) > 0 {
		r.pts = append(r.pts, p)
	}
}

// closeFor is the close a painting operator makes before it paints: h for s and b, and for b* the
// line back to the start pdfium's parser adds wherever the pen is. Only the recording is closed:
// the edges are filled closed whatever the stream said (scan), and stroked from the recording.
func (r *recording) closeFor(op string) {
	switch op {
	case "s", "b":
		r.close()
	case "b*":
		r.addClose()
	}
}

// close is h, Handle_ClosePath.
func (r *recording) close() {
	n := len(r.pts)
	switch {
	case n == 0:
	case r.start.at != r.cur.at:
		r.addClose()
	default:
		r.pts[n-1].close = true
	}
}

// rect is re, AddPathRect: the far corner is summed in float32 for the recording, and in float64
// for the edges.
func (r *recording) rect(x, y, w, h float64, ctm matrix) {
	x1, y1 := float32(x)+float32(w), float32(y)+float32(h)
	r.add(operand(x, y, ctm), pmove)
	r.add(ppoint{at: pt32{x1, float32(y)}, dev: ctm.apply(point{x + w, y})}, pline)
	r.add(ppoint{at: pt32{x1, y1}, dev: ctm.apply(point{x + w, y + h})}, pline)
	r.add(ppoint{at: pt32{float32(x), y1}, dev: ctm.apply(point{x, y + h})}, pline)
	r.addClose()
}

// points is the path AddPathObject makes of the recording when an operator paints it. A lone point
// is no path, except a move that h closed under round caps, which pdfium draws as a line to itself.
func (r *recording) points(roundCap bool) []ppoint {
	pts := r.pts
	switch {
	case len(pts) == 0:
		return nil
	case len(pts) == 1:
		if pts[0].kind != pmove || !pts[0].close || !roundCap {
			return nil
		}
		mv, ln := pts[0], pts[0]
		mv.close, ln.kind = false, pline
		return []ppoint{mv, ln}
	}
	if last := pts[len(pts)-1]; last.kind == pmove && !last.close {
		pts = pts[:len(pts)-1]
	}
	return pts
}

// traced is the path pdfium strokes for pts under ctm, as BuildAggPath gives it to AGG: the points in
// device space, but a line that goes nowhere on its own — from an open move, to the end or another
// open move — goes a pixel further, so that pdfium strokes it as a line a pixel long where §8.5.3.2
// would paint a dot under round caps and nothing under the others.
//
// The pixel is one of the space CFX_AggDeviceDriver::DrawPath builds the stroke's path in: ctm's
// uniform scale by max(|a|, |b|), with the rest of ctm applied by the stroker after. So in device
// space it is a step of (a, b) / max(|a|, |b|): to the right on an upright page, to the left on a
// mirrored one, and down or up on a quarter-turned one. A ctm whose a and b are both 0 in float32
// collapses the plane: the step is NaN, the stroker draws nothing of the line, and a dash of it is
// refused.
func traced(pts []ppoint, ctm geom.Matrix) *path {
	a, b := float32(ctm.A), float32(ctm.B)
	s := max(abs32(a), abs32(b))
	step := point{float64(a / s), float64(b / s)}
	open := func(i int) bool { return pts[i].kind == pmove && !pts[i].close }
	p := &path{}
	for i := 0; i < len(pts); i++ {
		q := pts[i]
		switch q.kind {
		case pmove:
			p.moveTo(q.dev)
		case pline:
			to := q.dev
			if i > 0 && open(i-1) && (i+1 == len(pts) || open(i+1)) && q.at == pts[i-1].at {
				to = point{to.x + step.x, to.y + step.y}
			}
			p.lineTo(to)
		case pbezier:
			q = pts[i+2]
			p.curveTo(pts[i].dev, pts[i+1].dev, q.dev)
			i += 2
		}
		if q.close {
			p.close()
		}
	}
	return p
}

// rectOf is CFX_Path::GetRect under a matrix: the device rectangle pts make when they are a
// rectangle on the device's axes. Whether they are four lines around a box is asked of the points as
// recorded, in user space, and whether its sides lie on the device's axes is asked of them
// transformed. A path of more than five points is first rid of the lines that go nowhere, as
// GetNormalizedPoints does.
func rectOf(pts []ppoint, m mat32) (l, t, r, b float32, ok bool) {
	if len(pts) > 5 {
		pts = normalized(pts)
	}
	n := len(pts)
	if n != 4 && n != 5 || n == 5 && pts[0].at != pts[4].at || pts[0].at == pts[2].at || pts[1].at == pts[3].at {
		return 0, 0, 0, 0, false
	}
	for _, p := range pts[1:] {
		if p.kind != pline {
			return 0, 0, 0, 0, false
		}
	}
	d := make([]pt32, 0, 5)
	for _, p := range pts {
		d = append(d, m.apply(p.at.x, p.at.y))
	}
	askew := func(a, b pt32) bool { return a.x != b.x && a.y != b.y }
	for i := 1; i < len(d); i++ {
		if askew(d[i], d[i-1]) {
			return 0, 0, 0, 0, false
		}
	}
	if askew(d[0], d[3]) {
		return 0, 0, 0, 0, false
	}
	return min(d[0].x, d[2].x), min(d[0].y, d[2].y), max(d[0].x, d[2].x), max(d[0].y, d[2].y), true
}

// normalized is GetNormalizedPoints: five points, or none when the path does not end where it began
// or still has more than five once the lines that go nowhere are gone.
func normalized(pts []ppoint) []ppoint {
	if pts[0].at != pts[len(pts)-1].at {
		return nil
	}
	norm := make([]ppoint, 1, 6)
	norm[0] = pts[0]
	for i := 1; i < len(pts); i++ {
		if len(norm)+len(pts)-i == 5 {
			return append(norm, pts[i:]...)
		}
		p, last := pts[i], norm[len(norm)-1]
		if p.kind == pline && !p.close && !last.close && p.at == last.at {
			continue
		}
		if norm = append(norm, p); len(norm) > 5 {
			return nil
		}
	}
	return norm
}

// zeroArea is GetZeroAreaPath, for one subpath of a filled path: the segments pdfium strokes a pixel
// wide in the fill colour where the subpath has no area, in device space, and whether they are thin,
// which draws them at a quarter of the fill's alpha. Three shapes qualify:
//
//   - a line, or a line there and back: its two ends, snapped to the centres of the device pixels
//     they truncate to, and thin;
//   - a palindrome, A B C B A: each step from the middle outward, thin;
//   - anything else, for each line that folds back along itself: the shorter of its two legs, thin
//     only in a subpath of more than three points.
//
// Each test is asked in user space; the segments the last two make are transformed after.
func zeroArea(sub []ppoint, m mat32) (segs [][2]pt32, thin bool) {
	if n := len(sub); (n == 2 || n == 3) && sub[0].kind == pmove && sub[1].kind == pline &&
		(n == 2 || sub[2].kind == pline && sub[0].at == sub[2].at) {
		if sub[0].at == sub[1].at {
			return nil, false
		}
		snap := func(p pt32) pt32 {
			d := m.apply(p.x, p.y)
			return pt32{float32(sat32(d.x)) + 0.5, float32(sat32(d.y)) + 0.5}
		}
		return [][2]pt32{plusOne(snap(sub[0].at), snap(sub[1].at))}, true
	}
	var user [][2]pt32
	if n := len(sub); n > 3 && n%2 == 1 && palindrome(sub) {
		for i := 0; i < n/2; i++ {
			user = append(user, [2]pt32{sub[n/2-i].at, sub[n/2-i-1].at})
		}
		thin = true
	} else {
		for i := 1; i < n; i++ {
			if sub[i].kind == pbezier {
				i += 2
				continue
			}
			next := sub[(i+1)%n]
			if next.kind != pline {
				continue
			}
			a, b, c := sub[i-1].at, sub[i].at, next.at
			var usePrev bool
			switch {
			case foldsDown(a, b, c):
				usePrev = abs32(b.y-a.y) < abs32(b.y-c.y)
			case foldsAcross(a, b, c) || foldsAslant(a, b, c):
				usePrev = abs32(b.x-a.x) < abs32(b.x-c.x)
			default:
				continue
			}
			if usePrev {
				user = append(user, [2]pt32{a, b})
			} else {
				user = append(user, [2]pt32{b, c})
			}
		}
		thin = n > 3 && len(user) > 0
	}
	for _, s := range user {
		seg := [2]pt32{m.apply(s[0].x, s[0].y), m.apply(s[1].x, s[1].y)}
		if s[0] == s[1] {
			seg[1].x++
		}
		segs = append(segs, seg)
	}
	return segs, thin
}

// plusOne is the segment a to b as BuildAggPath gives it to AGG when it is a subpath of its own: a
// pixel long, to the right, when it goes nowhere. BuildAggPath compares the points it was given,
// before its matrix, which for a cosmetic line and a snapped zero-area line are already the device's.
func plusOne(a, b pt32) [2]pt32 {
	if a == b {
		b.x++
	}
	return [2]pt32{a, b}
}

// subpathsOf is pts split where each subpath begins, at a move, as DrawPath splits a fill for
// GetZeroAreaPath.
func subpathsOf(pts []ppoint) [][]ppoint {
	var subs [][]ppoint
	from := 0
	for i := 1; i < len(pts); i++ {
		if pts[i].kind == pmove {
			subs = append(subs, pts[from:i])
			from = i
		}
	}
	return append(subs, pts[from:])
}

// palindrome is CheckPalindromicPath's test: each point the same as its mirror about the middle one,
// and no curve among them.
func palindrome(sub []ppoint) bool {
	mid := len(sub) / 2
	for i := 0; i < mid; i++ {
		l, r := sub[mid-i-1], sub[mid+i+1]
		if l.at != r.at || l.kind == pbezier || r.kind == pbezier {
			return false
		}
	}
	return true
}

func foldsDown(a, b, c pt32) bool {
	return a.x == b.x && b.x == c.x && float32(float32(b.y-a.y)*float32(b.y-c.y)) > 0
}

func foldsAcross(a, b, c pt32) bool {
	return a.y == b.y && b.y == c.y && float32(float32(b.x-a.x)*float32(b.x-c.x)) > 0
}

func foldsAslant(a, b, c pt32) bool {
	return a.x != b.x && c.x != b.x && a.y != b.y && c.y != b.y &&
		float32(float32(a.y-b.y)*float32(c.x-b.x)) == float32(float32(c.y-b.y)*float32(a.x-b.x))
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

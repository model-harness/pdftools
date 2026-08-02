package geom

import (
	"math"
	"testing"
)

func TestNewRectNormalizes(t *testing.T) {
	// PDF permits a /MediaBox with either corner order, so construction must
	// normalize rather than trust the operand order.
	r := NewRect(612, 792, 0, 0)
	if r.X0 != 0 || r.Y0 != 0 || r.X1 != 612 || r.Y1 != 792 {
		t.Fatalf("not normalized: %+v", r)
	}
	if r.Width() != 612 || r.Height() != 792 {
		t.Fatalf("width/height wrong: %v x %v", r.Width(), r.Height())
	}
}

func TestRectIntersectDisjoint(t *testing.T) {
	a := NewRect(0, 0, 10, 10)
	b := NewRect(20, 20, 30, 30)
	if got := a.Intersect(b); got != (Rect{}) {
		t.Fatalf("disjoint should be zero, got %+v", got)
	}
	// Edge-touching counts as disjoint: a zero-area overlap is no overlap.
	c := NewRect(10, 0, 20, 10)
	if got := a.Intersect(c); got != (Rect{}) {
		t.Fatalf("edge-touching should be zero, got %+v", got)
	}
}

func TestRectUnionTreatsZeroAsAbsent(t *testing.T) {
	// Union over a growing set starts from the zero Rect, so the zero value must
	// not drag the result to the origin.
	a := NewRect(100, 100, 200, 200)
	if got := (Rect{}).Union(a); got != a {
		t.Fatalf("zero.Union(a) = %+v, want %+v", got, a)
	}
	if got := a.Union(Rect{}); got != a {
		t.Fatalf("a.Union(zero) = %+v, want %+v", got, a)
	}
}

func TestMatrixIdentity(t *testing.T) {
	x, y := Identity.Apply(3, 4)
	if x != 3 || y != 4 {
		t.Fatalf("identity moved the point: %v,%v", x, y)
	}
}

func TestMatrixMulAppliesLeftFirst(t *testing.T) {
	// Row-vector convention: A.Mul(B) applies A then B. Scale by 2 then shift by
	// 10 must give 2*x+10, not 2*(x+10).
	m := Scale(2, 2).Mul(Translate(10, 0))
	x, y := m.Apply(1, 1)
	if x != 12 || y != 2 {
		t.Fatalf("got %v,%v want 12,2 -- composition order is reversed", x, y)
	}

	// The opposite order proves the test is not symmetric.
	n := Translate(10, 0).Mul(Scale(2, 2))
	x2, _ := n.Apply(1, 1)
	if x2 != 22 {
		t.Fatalf("got %v want 22", x2)
	}
}

func TestApplyVecIgnoresTranslation(t *testing.T) {
	// Advance widths are directions, not positions. Applying translation to one
	// would offset every glyph by the text-matrix origin.
	m := Translate(100, 200).Mul(Scale(2, 2))
	x, y := m.ApplyVec(1, 0)
	if x != 2 || y != 0 {
		t.Fatalf("got %v,%v want 2,0", x, y)
	}
}

func TestScaleFactorsUnderRotation(t *testing.T) {
	// A 90-degree rotation composed with a 12-unit scale must still report 12:
	// this is how effective font size is recovered from a rotated text matrix.
	const s = 12
	rot := Matrix{A: 0, B: 1, C: -1, D: 0}
	m := Scale(s, s).Mul(rot)
	sx, sy := m.ScaleFactors()
	if math.Abs(sx-s) > 1e-9 || math.Abs(sy-s) > 1e-9 {
		t.Fatalf("got %v,%v want %v,%v", sx, sy, float64(s), float64(s))
	}
}

func TestToleranceNearlyEqual(t *testing.T) {
	tol := DefaultTolerance
	if !tol.NearlyEqual(1.0, 1.0+1e-9) {
		t.Fatal("should absorb float noise")
	}
	if tol.NearlyEqual(1.0, 1.1) {
		t.Fatal("should not absorb a real difference")
	}
}

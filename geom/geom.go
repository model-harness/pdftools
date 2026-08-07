// Package geom holds PDF user-space geometry: rectangles, transformation
// matrices, and the tolerance policy shared by every consumer.
//
// PDF user space has its origin at the lower-left corner with y increasing
// upward, which is the opposite of most raster conventions. Nothing in this
// package flips that; conversion is the rasterizer's job.
package geom

import "math"

// Rect is an axis-aligned rectangle in PDF user space, normalized so that
// X0 <= X1 and Y0 <= Y1. PDF permits either corner order in a /MediaBox array,
// so construct via NewRect rather than by literal.
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// NewRect returns a Rect with corners normalized.
func NewRect(x0, y0, x1, y1 float64) Rect {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return Rect{x0, y0, x1, y1}
}

func (r Rect) Width() float64  { return r.X1 - r.X0 }
func (r Rect) Height() float64 { return r.Y1 - r.Y0 }
func (r Rect) Area() float64   { return r.Width() * r.Height() }

// IsZero reports whether the rectangle has no area.
func (r Rect) IsZero() bool { return r.Width() == 0 || r.Height() == 0 }

// Intersect returns the overlap of r and s, or the zero Rect if they are
// disjoint.
func (r Rect) Intersect(s Rect) Rect {
	out := Rect{
		X0: math.Max(r.X0, s.X0),
		Y0: math.Max(r.Y0, s.Y0),
		X1: math.Min(r.X1, s.X1),
		Y1: math.Min(r.Y1, s.Y1),
	}
	if out.X0 >= out.X1 || out.Y0 >= out.Y1 {
		return Rect{}
	}
	return out
}

// Union returns the smallest rectangle containing both r and s. A zero-area
// operand is treated as absent, so Union over a growing set can start from the
// zero Rect.
func (r Rect) Union(s Rect) Rect {
	if r.IsZero() {
		return s
	}
	if s.IsZero() {
		return r
	}
	return Rect{
		X0: math.Min(r.X0, s.X0),
		Y0: math.Min(r.Y0, s.Y0),
		X1: math.Max(r.X1, s.X1),
		Y1: math.Max(r.Y1, s.Y1),
	}
}

// Matrix is a PDF transformation matrix. PDF writes it as the six operands
// [a b c d e f], standing for the affine matrix
//
//	| a b 0 |
//	| c d 0 |
//	| e f 1 |
//
// with row vectors, so a point transforms as [x y 1] * M. That row-vector
// convention is why Mul composes left-to-right: A.Mul(B) applies A first.
type Matrix struct {
	A, B, C, D, E, F float64
}

// Identity is the no-op transformation.
var Identity = Matrix{A: 1, D: 1}

// Translate returns a matrix shifting by (tx, ty).
func Translate(tx, ty float64) Matrix { return Matrix{A: 1, D: 1, E: tx, F: ty} }

// Scale returns a matrix scaling by (sx, sy).
func Scale(sx, sy float64) Matrix { return Matrix{A: sx, D: sy} }

// Mul returns m composed with n such that the result applies m first, then n.
// For a glyph this is the natural reading order: text matrix, then CTM.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.B*n.C,
		B: m.A*n.B + m.B*n.D,
		C: m.C*n.A + m.D*n.C,
		D: m.C*n.B + m.D*n.D,
		E: m.E*n.A + m.F*n.C + n.E,
		F: m.E*n.B + m.F*n.D + n.F,
	}
}

// Apply transforms the point (x, y).
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// ApplyVec transforms (x, y) as a direction, ignoring translation. Use this for
// displacements and advance widths, where the origin offset must not apply.
func (m Matrix) ApplyVec(x, y float64) (float64, float64) {
	return m.A*x + m.C*y, m.B*x + m.D*y
}

// ScaleFactors returns the magnitude of the transformed unit x and y vectors.
// For text this yields the on-page size of a nominal 1-unit glyph, which is how
// an effective font size is recovered from a matrix that may also rotate.
func (m Matrix) ScaleFactors() (sx, sy float64) {
	return math.Hypot(m.A, m.B), math.Hypot(m.C, m.D)
}

// Tolerance is the single place where "close enough" is decided.
//
// It exists because inferring a space from a coordinate gap is the one
// judgement that determines whether extracted text reads as prose or as one
// 4000-character word. Scattering epsilons through the extractor makes that
// judgement untunable and unmeasurable, so every threshold lives here and is
// benchmarked as a unit.
type Tolerance struct {
	// SpaceFrac is the fraction of a font's nominal space advance that a
	// horizontal gap must exceed before an inter-word space is inferred.
	// Below 1.0 because many producers emit slightly tightened spacing.
	SpaceFrac float64

	// WideSpaceFrac is the multiple of nominal space advance above which a gap
	// is treated as column or tab separation rather than a single space.
	WideSpaceFrac float64

	// LineFrac is the fraction of font size that a baseline must shift
	// vertically before a new line is started.
	LineFrac float64

	// ParaFrac is the multiple of line height above which a vertical gap is
	// treated as a paragraph break rather than a line break.
	ParaFrac float64

	// SizeFrac is the ratio between two consecutive lines' dominant type sizes
	// above which they are treated as separate blocks even when the vertical step
	// says otherwise. A heading set at ordinary leading is the case: the step alone
	// cannot see it, and without this the heading fuses into the paragraph below.
	//
	// It is a ratio of larger to smaller, so 1.0 would split on any difference at
	// all and is not a usable setting — OCR output reports the same line of type at
	// sizes differing by a few percent. Zero means the default.
	SizeFrac float64

	// IndentFrac is the number of space widths a line must be indented past its
	// block's own margin, while repeating the indent that block's first line was set
	// with, before it is read as starting a new paragraph.
	//
	// This is the paragraph break that has no vertical evidence: a document setting
	// no space between paragraphs steps down by exactly one line at a boundary, so
	// ParaFrac cannot see it and SizeFrac has nothing to compare. Expressed in space
	// widths rather than points so a footnote and a heading are judged alike. Zero
	// means off, which is a usable setting — the rule is the least certain of the
	// three and a caller who wants only vertical evidence can have it.
	IndentFrac float64

	// IndentMax is the number of space widths beyond which an indent is column or
	// cell placement rather than a paragraph's first line, and is ignored.
	IndentMax float64

	// Epsilon is the absolute tolerance for coordinate comparison in user-space
	// units, absorbing float noise from matrix composition.
	Epsilon float64
}

// DefaultTolerance is the starting policy. The values are deliberately plain
// and are expected to move once the golden corpus can measure them; they are
// not tuned constants inherited from anywhere.
var DefaultTolerance = Tolerance{
	SpaceFrac:     0.30,
	WideSpaceFrac: 2.50,
	LineFrac:      0.50,
	ParaFrac:      1.50,
	// 1.06 is measured rather than chosen. Over the 6,023 line pairs the corpus
	// joins on vertical step alone, 5,769 are at the same dominant size and the
	// remaining 254 split into two populations with a clear gap: jitter at or below
	// 1.057 — OCR output reporting one line of type at 27 and 28 points, an ISO
	// cover's 11.5pt address line against a 12pt URL — and real structure at or
	// above 1.067, where a 32pt title meets a 30pt subtitle and Adobe's 13.02pt
	// headings meet 12pt body. Nothing in the corpus falls between.
	SizeFrac: 1.06,
	// 1.0 and 6.0 bracket what a first-line indent is, in space widths, and the two
	// bounds are load-bearing to very different degrees. Swept over the corpus with
	// the shipping rule, the floor is flat: 0.75, 1.0, 1.25, 1.5, 2.0 and 3.0 all
	// yield the same 3 extra blocks, so it is a floor stating that an indent of under
	// one space width is not one, and not a threshold tuned to a trough. The ceiling
	// does real work — unbounded it admits 28 extra blocks, at 6 it admits 3, and it
	// plateaus across 4 to 10, so 6 sits inside a stable band rather than on an edge.
	// What it excludes is placement rather than indentation: reference/paragraphs.pdf
	// indents by 3.00 space widths (LaTeX's 15pt \parindent against a 4.981pt space),
	// while the offsets rejected above 6 run to 17, 63 and 94 and are table cells and
	// addresses.
	IndentFrac: 1.0,
	IndentMax:  6.0,
	Epsilon:    1e-6,
}

// NearlyEqual reports whether a and b are within the absolute epsilon.
func (t Tolerance) NearlyEqual(a, b float64) bool { return math.Abs(a-b) <= t.Epsilon }

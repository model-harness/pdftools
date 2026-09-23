package native

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// buildPDF writes a one-page PDF from a set of object bodies, computing the xref offsets.
//
// The third object is the page, so a caller can put whatever it needs there — a /Rotate, an
// /Annots — which is why this is separate from onePagePDF rather than a parameter on it: the
// tests that need a page dictionary of their own need *different* ones.
func buildPDF(t *testing.T, objs []string, name string) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	off := make([]int, len(objs))
	for i, o := range objs {
		off[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	start := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range off {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, start)

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pageObjs(pageDict, stream string) []string {
	return []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		pageDict,
		fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(stream), stream),
	}
}

// TestRotateMatchesTheBorrowedBackend pins that /Rotate turns the page and changes its extent.
//
// Two backends behind one interface must not disagree about the pixel dimensions of a page:
// every caller that maps a coordinate back to points would be right for one of them and wrong
// for the other. render/pdfium applies /Rotate and reports the rotated box; this backend
// ignored it and rendered 200×100 at every value, which the comparison here catches as a size
// disagreement before it reaches a pixel.
func TestRotateMatchesTheBorrowedBackend(t *testing.T) {
	for _, rot := range []int{0, 90, 180, 270} {
		t.Run(fmt.Sprint(rot), func(t *testing.T) {
			// A shape in one corner, so a rotation is observable rather than symmetric.
			const stream = "0 g 10 10 60 30 re f"
			path := buildPDF(t, pageObjs(
				fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]/Rotate %d/Resources<<>>/Contents 4 0 R>>", rot),
				stream), "rot.pdf")

			ref, err := pdfium.Open(path)
			if err != nil {
				t.Fatalf("pdfium: %v", err)
			}
			defer func() { _ = ref.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			want, err := ref.Page(1, o)
			if err != nil {
				t.Fatalf("pdfium Page: %v", err)
			}
			got := renderNative(t, path, 72)

			a, aw, ah := ink(want.Image)
			b, bw, bh := ink(got.Image)
			if aw != bw || ah != bh {
				t.Fatalf("size: pdfium %dx%d, native %dx%d", aw, ah, bw, bh)
			}
			var sum float64
			over := 0
			for i := range a {
				d := math.Abs(float64(a[i]) - float64(b[i]))
				sum += d
				if d > 32 {
					over++
				}
			}
			mean := sum / float64(len(a))
			t.Logf("%dx%d mean %.3f over-32 %d box %v", bw, bh, mean, over, got.Box)
			if mean > 1.0 || over > 0 {
				t.Errorf("mean %.3f, %d pixels over 32", mean, over)
			}
			// The box a caller is told about is the extent the image covers with the rotation
			// applied — cmd/pdfspec's ocr verb reads it to map a model's coordinates back to
			// the page, and a zero box makes every one of those wrong.
			wantW, wantH := 200.0, 100.0
			if rot == 90 || rot == 270 {
				wantW, wantH = 100, 200
			}
			if got.Box.Width() != wantW || got.Box.Height() != wantH {
				t.Errorf("Box = %v, want %gx%g for /Rotate %d", got.Box, wantW, wantH, rot)
			}
		})
	}
}

// TestCMYKIsThisPackagesConversionAndNotPdfiums records a divergence rather than hiding it.
//
// §8.6.4.4's subtractive formula makes pure K black: 1 − min(1, 0+1) = 0 on every channel.
// pdfium paints it at about RGB(35,35,35), because it uses a calibrated table rather than the
// formula. Measured over a pure-K rectangle the two differ by a **mean of 11.0 with 12,600
// pixels over 32**, three times this package's acceptance bound — so CMYK is outside the pdfium
// comparison by construction, and this test is what stands in for it.
//
// The formula and not the table, because a table is a colour-management decision: which profile
// and which rendering intent, on a page that names neither. Differing from one reader visibly is
// better than guessing at that quietly, and a caller who needs pdfium's numbers can use pdfium.
// What must not happen is the divergence going unrecorded.
func TestCMYKIsThisPackagesConversionAndNotPdfiums(t *testing.T) {
	for _, c := range []struct {
		name    string
		stream  string
		r, g, b int
	}{
		{"pure K is black", "0 0 0 1 k 30 30 140 90 re f", 0, 0, 0},
		{"pure C is cyan", "1 0 0 0 k 30 30 140 90 re f", 0, 255, 255},
		{"pure M is magenta", "0 1 0 0 k 30 30 140 90 re f", 255, 0, 255},
		{"pure Y is yellow", "0 0 1 0 k 30 30 140 90 re f", 255, 255, 0},
		{"no ink is white", "0 0 0 0 k 30 30 140 90 re f", 255, 255, 255},
		{"half K is mid grey", "0 0 0 0.5 k 30 30 140 90 re f", 128, 128, 128},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := renderNative(t, onePagePDF(t, c.stream, 200, 200), 72)
			// Inside the rectangle: user (100, 80) is device (100, 120).
			gr, gg, gb := rgbAt(r.Image, 100, 120)
			if absInt(gr-c.r) > 1 || absInt(gg-c.g) > 1 || absInt(gb-c.b) > 1 {
				t.Errorf("rgb = (%d,%d,%d), want (%d,%d,%d)", gr, gg, gb, c.r, c.g, c.b)
			}
		})
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestShowTextOperatorsAreAllRefused names each of the four.
//
// The blocker table ranks TJ first at 1,241 pages and no test named it — both refusal tests used
// Tj — so dropping "TJ" from the refusal list was a mutation the suite could not see.
func TestShowTextOperatorsAreAllRefused(t *testing.T) {
	for _, c := range []struct{ op, operand string }{
		{"Tj", "(t)"},
		{"TJ", "[(t)]"},
		{"'", "(t)"},
		{`"`, "1 1 (t)"},
	} {
		t.Run(c.op, func(t *testing.T) {
			stream := "BT /F1 12 Tf 30 30 Td " + c.operand + " " + c.op + " ET"
			s, err := pcstore.Open(onePagePDF(t, stream, 200, 200))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			_, err = New(s).Page(1, o)
			var u *Unsupported
			if !errors.As(err, &u) {
				t.Fatalf("err = %v, want an *Unsupported", err)
			}
			found := false
			for _, got := range u.Ops {
				if got == c.op {
					found = true
				}
			}
			if !found {
				t.Errorf("Ops = %v, want it to name %q", u.Ops, c.op)
			}
		})
	}
}

// TestAnnotationsAreRefusedWhenAskedFor pins the option-level refusal.
//
// render.Options documents Annotations as rendering appearance streams, and the borrowed backend
// honours it. A caller that asks for them and gets a page without them, with no error, is the
// same silent omission as an unimplemented operator — one level up, where a list of operators
// cannot see it.
func TestAnnotationsAreRefusedWhenAskedFor(t *testing.T) {
	objs := append(pageObjs(
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]/Annots[5 0 R]/Resources<<>>/Contents 4 0 R>>",
		"0 g 10 10 20 20 re f\n"),
		"<</Type/Annot/Subtype/Square/Rect[10 10 50 50]>>")
	path := buildPDF(t, objs, "annot.pdf")

	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	o := render.DefaultOptions
	o.DPI = 72
	o.Annotations = false
	if _, err := New(s).Page(1, o); err != nil {
		t.Errorf("Page without annotations = %v, want the page drawn", err)
	}
	o.Annotations = true
	if _, err := New(s).Page(1, o); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Page with annotations = %v, want ErrUnsupported", err)
	}
}

// TestGeometryCasesNoOtherFixtureReaches covers the inputs whose absence let a mutation live.
//
// Each row was a surviving mutant. A non-zero /MediaBox origin is the only thing that can tell
// the device matrix's translation from zero — every other fixture starts at 0 0, where the term
// vanishes. A shape crossing the page edge is the only thing that exercises the span clamps, and
// one of those clamps is not defensive: a span ending exactly at the page width indexes one past
// the row without it. A soft-edged clip over another soft-edged clip is the only input that can
// tell multiplying two coverages from taking their minimum, which is the choice the mask's own
// comment argues for at length. And a colour component out of range is what the clamp is for.
func TestGeometryCasesNoOtherFixtureReaches(t *testing.T) {
	for _, c := range []struct {
		name   string
		box    string
		stream string
		over   int
	}{
		{"a non-zero box origin", "[10 20 210 220]", "0 g 20 30 40 50 re f", 0},
		{"a shape off the left and top edges", "[0 0 200 200]", "0 g -50 150 120 120 re f", 0},
		{"a shape off the right and bottom edges", "[0 0 200 200]", "0 g 130 -40 120 120 re f", 0},
		{"a shape exactly on the right edge", "[0 0 200 200]", "0 g 100 50 100 60 re f", 0},
		{"two soft-edged clips over each other", "[0 0 200 200]",
			"20 100 m 100 190 180 100 100 20 c W n 40 40 m 100 180 160 40 100 100 c W n 0 g 0 0 200 200 re f", 12},
		{"a colour component out of range", "[0 0 200 200]", "1.5 g 30 30 60 60 re f 0 g 120 120 50 50 re f", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := buildPDF(t, pageObjs(
				"<</Type/Page/Parent 2 0 R/MediaBox"+c.box+"/Resources<<>>/Contents 4 0 R>>",
				c.stream), "geom.pdf")
			ref, err := pdfium.Open(path)
			if err != nil {
				t.Fatalf("pdfium: %v", err)
			}
			defer func() { _ = ref.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			want, err := ref.Page(1, o)
			if err != nil {
				t.Fatalf("pdfium Page: %v", err)
			}
			got := renderNative(t, path, 72)

			a, aw, ah := ink(want.Image)
			b, bw, bh := ink(got.Image)
			if aw != bw || ah != bh {
				t.Fatalf("size: pdfium %dx%d, native %dx%d", aw, ah, bw, bh)
			}
			var sum float64
			over := 0
			for i := range a {
				d := math.Abs(float64(a[i]) - float64(b[i]))
				sum += d
				if d > 32 {
					over++
				}
			}
			mean := sum / float64(len(a))
			t.Logf("mean %.3f over-32 %d", mean, over)
			if mean > 1.0 || over > c.over {
				t.Errorf("mean %.3f, %d pixels over 32 (allowed %d)", mean, over, c.over)
			}
		})
	}
}

// TestEveryStrokingOperatorIsRefused names all six, because only S was tested and each of the
// others would otherwise be silently part-drawn: b, b*, B and B* fill as well as stroke, so
// filling them and dropping the stroke is a different mark from the one the page makes.
func TestEveryStrokingOperatorIsRefused(t *testing.T) {
	for _, op := range []string{"S", "s", "B", "B*", "b", "b*"} {
		t.Run(op, func(t *testing.T) {
			s, err := pcstore.Open(onePagePDF(t, "0 g 20 20 m 180 20 l 180 180 l "+op, 200, 200))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			_, err = New(s).Page(1, o)
			var u *Unsupported
			if !errors.As(err, &u) {
				t.Fatalf("err = %v, want an *Unsupported for %q", err, op)
			}
			if len(u.Ops) != 1 || u.Ops[0] != op {
				t.Errorf("Ops = %v, want exactly [%q]", u.Ops, op)
			}
		})
	}
}

// TestIntersectRoundsRatherThanTruncates is a unit test because no page can show it.
//
// Two coverages of 200 multiply to 156 truncated and 157 rounded, one level apart — invisible in
// any image comparison, and the mask's own comment argues for the rounding, so something has to
// hold it. The 255 case is here for the claim the comment used to make and does not any more:
// 255×255/255 is 255 either way, so rounding is not what keeps an opaque clip opaque.
func TestIntersectRoundsRatherThanTruncates(t *testing.T) {
	for _, c := range []struct {
		a, b, want uint8
	}{
		{255, 255, 255},
		{200, 200, 157},
		{128, 128, 64},
		{0, 255, 0},
		{255, 0, 0},
		{1, 1, 0},
	} {
		m := newMask(1, 1)
		m.a[0], m.y0, m.y1 = c.a, 0, 1
		o := newMask(1, 1)
		o.a[0], o.y0, o.y1 = c.b, 0, 1
		m.intersect(o)
		if m.a[0] != c.want {
			t.Errorf("%d × %d = %d, want %d", c.a, c.b, m.a[0], c.want)
		}
	}
}

// TestHairlineNeedsVerticalSampling is what makes the sample count a measurement rather than a
// preference.
//
// Every acceptance shape is large and smooth, so four samples per pixel row agree with sixteen
// on all of them — the review that found this is the reason the claim "sixteen is where the
// measurement levels off" is no longer in this package. What four samples cannot represent is a
// rule thinner than a quarter of a pixel: a 0.1pt hairline covers a sixteenth of a row, which
// rounds to nothing at four samples and to a sixth of full ink at sixteen. Producers draw
// hairlines constantly — a table rule is one — so this is the case that prices the constant.
func TestHairlineNeedsVerticalSampling(t *testing.T) {
	r := renderNative(t, onePagePDF(t, "0 g 20 100 160 0.1 re f", 200, 200), 72)
	px, w, _ := ink(r.Image)
	// Device row for user y = 100 is 200 − 100 − 1 = 99 downward of the top edge.
	got := px[99*w+100]
	if got == 0 {
		t.Errorf("a 0.1pt hairline left no ink: vertical sampling is too coarse to represent it")
	}
	if got > 64 {
		t.Errorf("hairline ink = %d, want a small fraction of full coverage", got)
	}
	t.Logf("0.1pt hairline ink = %d of 255", got)
}

// TestNaNBoundsDoNotHang pins the other half of the clamp: a bounding box that is not a number.
//
// The hostile-coordinate test reaches infinite bounds, which the comparisons handle on their
// own. A NaN bound needs a transform: a CTM whose scale overflows to infinity applied to a
// coordinate of zero is 0 × ∞, and every comparison against the result is false — so a guard
// written as lo >= n rather than !(lo < n) lets it through, and int(NaN) is the most negative
// int64.
func TestNaNBoundsDoNotHang(t *testing.T) {
	// A CTM with one huge positive and one huge negative term. Each product overflows to an
	// infinity of opposite sign and the sum of the two is NaN, which is the only route to a
	// not-a-number bound: the lexer parses no exponent and salvages an out-of-range literal to
	// zero, so an infinity cannot be written down directly.
	huge := "1" + strings.Repeat("0", 308)
	stream := "q " + huge + " 0 -" + huge + " 1 0 0 cm 0 g 10 10 10 10 re f Q"
	s, err := pcstore.Open(onePagePDF(t, stream, 200, 200))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Page panicked: %v", r)
			}
		}()
		_, _ = New(s).Page(1, o)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Page did not return within 10s on a NaN bounding box")
	}
}

// TestPathSurvivesBeingUsedAsAClip pins that rasterizing a path does not consume it.
//
// §8.5.4 makes the clip the path as it stands when the path object *ends*, so a W in the middle
// of one is a marker and the segments after it are still part of the path. Closing the path
// inside the rasterizer made that impossible: the clip was taken at the W — a two-point path,
// which is a zero-area line that blanks the page — and every segment after it was dropped,
// because a closed subpath takes no more.
func TestPathSurvivesBeingUsedAsAClip(t *testing.T) {
	mean, worst, over := compare(t, "100 20 m 180 20 l W 180 100 l 100 100 l n 0 g 0 0 200 200 re f", 200, 72)
	t.Logf("mean %.3f worst %.0f over-32 %d", mean, worst, over)
	if mean > 1.0 || over > 0 {
		t.Errorf("mean %.3f, %d pixels over 32 — the clip took the path before it ended", mean, over)
	}
}

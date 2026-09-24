package native

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// onePagePDF writes a one-page PDF whose only content is stream.
//
// Written out here rather than committed as a fixture because each test needs a *different*
// content stream — the whole subject is which operators produce which pixels — and a fixture
// per operator sequence would be a directory of near-identical files nobody can diff. The
// bytes are the same shape as objects/pdfcpu's malformed-page fixture and for the same reason:
// no producer this repo can drive emits exactly the stream a test needs.
func onePagePDF(t *testing.T, stream string, w, h int) string {
	t.Helper()
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 %d %d]/Resources<<>>/Contents 4 0 R>>", w, h),
		fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(stream), stream),
	}
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

	path := filepath.Join(t.TempDir(), "page.pdf")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ink returns a page's coverage as one byte per pixel, 0 for white and 255 for black.
//
// The mean of the three channels, not the red one. Reading red alone made colour unobservable:
// a solid red rectangle and a blank page both came out 0, so dropping the green and blue writes
// from the compositor, or swapping magenta and yellow in the CMYK conversion, changed nothing
// any test could see.
//
// Coverage rather than RGBA, because the question a rasterizer test asks is where the ink is,
// and a channel-for-channel comparison would also be asserting the alpha convention of whichever
// backend happened to be written first.
func ink(img image.Image) (px []uint8, w, h int) {
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	px = make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			lum := (int(r>>8) + int(g>>8) + int(bl>>8)) / 3
			px[y*w+x] = uint8(255 - lum)
		}
	}
	return px, w, h
}

// rgbAt returns one pixel's three channels, for the assertions luminance cannot make: two
// colours of the same brightness are the same ink and different pixels.
func rgbAt(img image.Image, x, y int) (int, int, int) {
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
	return int(r >> 8), int(g >> 8), int(bl >> 8)
}

func renderNative(t *testing.T, path string, dpi float64) *render.Raster {
	t.Helper()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = dpi
	r, err := New(s).Page(1, o)
	if err != nil {
		t.Fatalf("native Page: %v", err)
	}
	return r
}

// compare measures this backend against pdfium on one content stream and reports the three
// numbers that say whether they agree: the mean absolute difference in ink, the worst pixel,
// and how many pixels are further apart than tol.
func compare(t *testing.T, stream string, size int, dpi float64) (mean, worst float64, over int) {
	t.Helper()
	return comparePath(t, onePagePDF(t, stream, size, size), dpi)
}

// comparePath is compare for a page a test built itself, because its content needs resources.
func comparePath(t *testing.T, path string, dpi float64) (mean, worst float64, over int) {
	t.Helper()

	// A hard failure, not a skip. Ten of this package's mutation kills come from this
	// comparison, so a skip would quietly retire them — and render/pdfium is a dependency of
	// this repo whose own tests run here, so its absence is a broken checkout rather than an
	// environment this backend has to tolerate.
	ref, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium unavailable, so this comparison cannot run: %v", err)
	}
	defer func() { _ = ref.Close() }()
	o := render.DefaultOptions
	o.DPI = dpi
	want, err := ref.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}

	got := renderNative(t, path, dpi)
	a, aw, ah := ink(want.Image)
	b, bw, bh := ink(got.Image)
	if aw != bw || ah != bh {
		t.Fatalf("size disagrees: pdfium %dx%d, native %dx%d — render.Fit is meant to be the one answer", aw, ah, bw, bh)
	}
	for i := range a {
		d := math.Abs(float64(a[i]) - float64(b[i]))
		mean += d
		if d > worst {
			worst = d
		}
		if d > 32 {
			over++
		}
	}
	mean /= float64(len(a))
	return mean, worst, over
}

// TestFillsAgreeWithPdfium is this backend's acceptance test, and pdfium is the yardstick
// because ADR 0005's whole plan is borrow-then-replace: the borrowed engine is what the native
// one has to reproduce, so it is the reference and not a second opinion.
//
// The three shapes are chosen for what separates them rather than for looking like a page. A
// convex triangle has one span per scanline and settles whether coverage is computed at all. A
// five-pointed star drawn as *one* self-intersecting subpath has three spans across its middle
// and fills that middle under the nonzero rule and hollows it under even-odd, so it is the only
// one of the three whose picture says which rule ran. A closed pair of cubics settles
// flattening, since a curve too coarsely flattened shows as a polygon and one too finely
// flattened costs time without changing a pixel.
//
// The tolerance is measured rather than chosen: at 16 samples per pixel row the mean absolute
// difference over these shapes is about 0.06 of 255 and no pixel differs by more than 32.
// Asserting 1.0 and 48 leaves room for a different AA convention on an edge pixel and none at
// all for a fill rule, a flip, or a flattening error, each of which moves the mean by tens.
func TestFillsAgreeWithPdfium(t *testing.T) {
	for _, c := range []struct {
		name   string
		stream string
		// over is how many pixels may differ by more than 32, and it is 0 everywhere except
		// where a measurement says otherwise. A tight curve is the exception: 10× finer
		// flattening and 4× more coverage samples leave the same 5 pixels at the same 40
		// levels, so what differs is where pdfium puts a curve's anti-aliasing at the extreme
		// of its curvature, not this rasterizer's precision. The mean over those shapes is
		// 0.036, better than the cubic pair that passes at 0, which is why the bound is a
		// count of edge pixels and not a looser mean.
		over int
	}{
		{"convex triangle, nonzero", "0 g 20 20 m 180 40 l 100 120 l f", 0},
		{"self-intersecting star, nonzero", "0 g 150 150 m 168 105 l 123 133 l 177 133 l 132 105 l f", 0},
		{"self-intersecting star, even-odd", "0 g 150 150 m 168 105 l 123 133 l 177 133 l 132 105 l f*", 0},
		{"closed pair of cubics", "0 g 40 140 m 60 195 100 195 120 140 c 90 170 70 170 40 140 c f", 0},
		{"rectangle from re", "0 g 30 30 140 90 re f", 0},
		{"grey and a second path", "0.5 g 20 20 60 60 re f 0 g 100 100 m 180 100 l 140 180 l f", 0},
		// Operators no other case reaches. Each was a surviving mutant before it was here: the
		// v and y curves take their control points from the current point and the endpoint, F
		// is f's deprecated spelling, h closes a subpath the stream did not, and a rotating cm
		// is the only thing that can tell the matrix composition order from its reverse.
		{"v takes the current point as its first control", "0 g 40 40 m 120 40 120 120 v f", 5},
		{"y takes the endpoint as its second control", "0 g 40 40 m 120 40 120 120 y f", 5},
		{"F is f", "0 g 30 30 140 90 re F", 0},
		{"h closes the subpath", "0 g 40 40 m 160 40 l 100 150 l h f", 0},
		{"a rotating cm inside q", "q 0.866 0.5 -0.5 0.866 100 100 cm 0 g 0 0 40 40 re f Q 0 g 0 0 30 30 re f", 0},
		{"a scaling cm", "q 2 0 0 3 20 20 cm 0 g 0 0 40 30 re f Q", 0},
		// Colour, which a red-channel-only comparison could not see at all.
		{"red", "1 0 0 rg 30 30 140 90 re f", 0},
		{"a blue and a green path", "0 0 1 rg 20 20 70 70 re f 0 1 0 rg 110 110 70 70 re f", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			mean, worst, over := compare(t, c.stream, 200, 72)
			t.Logf("mean %.3f worst %.0f over-32 %d", mean, worst, over)
			if mean > 1.0 {
				t.Errorf("mean ink difference %.3f, want <= 1.0", mean)
			}
			// Two bounds, not three: a worst pixel over 32 is already a pixel over 32, so the
			// operative pair is the mean and the maximum. An earlier third bound at 48 could
			// not fire without this one firing first, which made it read as headroom that was
			// not there.
			if over > c.over {
				t.Errorf("%d pixels differ by more than 32 (worst %.0f), want at most %d", over, worst, c.over)
			}
			if c.over > 0 && over == 0 {
				t.Errorf("no pixel differs by more than 32, but %d were expected: the bound is a"+
					" measurement and it has moved", c.over)
			}
		})
	}
}

// TestEvenOddHollowsWhatNonzeroFills is the assertion the comparison above cannot make on its
// own: both rules agree with pdfium, so a test that only compared to pdfium would pass with
// the two rules swapped if pdfium were swapped too.
//
// The star's middle is the difference. Sampled at its centre, nonzero must put ink there and
// even-odd must not, which is §8.5.3.3's distinction stated as a pixel.
func TestEvenOddHollowsWhatNonzeroFills(t *testing.T) {
	const star = "0 g 150 150 m 168 105 l 123 133 l 177 133 l 132 105 l "
	nz := renderNative(t, onePagePDF(t, star+"f", 200, 200), 72)
	eo := renderNative(t, onePagePDF(t, star+"f*", 200, 200), 72)

	// The centroid of the five points, in device space: y flips about the 200pt page.
	cx, cy := (150+168+123+177+132)/5, 200-(150+105+133+133+105)/5
	nzInk, w, _ := ink(nz.Image)
	eoInk, _, _ := ink(eo.Image)
	if got := nzInk[cy*w+cx]; got < 200 {
		t.Errorf("nonzero centre ink = %d, want the middle filled", got)
	}
	if got := eoInk[cy*w+cx]; got > 40 {
		t.Errorf("even-odd centre ink = %d, want the middle hollow", got)
	}
}

// TestClipRestrictsALaterFill pins that W applies after the painting operator that ends the
// path, not at the W itself (§8.5.4).
//
// Two paints and one clip: the rectangle is established as a clip by "W n", and the triangle
// drawn afterwards may only mark pixels inside it. The assertion is on a pixel the triangle
// covers and the rectangle does not, which is the only place the two orders differ.
func TestClipRestrictsALaterFill(t *testing.T) {
	// The triangle fills what lies right of the line x = y, so it and the clip rectangle
	// (user x 100..180, y 20..100) overlap over a wedge. The two sample points straddle the
	// clip's top edge inside that wedge, which is the only place the two orders of applying a
	// clip differ: at the W itself the rectangle would clip nothing, since the path it was
	// built from had not been painted yet.
	stream := "0 g 100 20 80 80 re W n 20 20 m 180 20 l 180 180 l f"
	r := renderNative(t, onePagePDF(t, stream, 200, 200), 72)
	px, w, _ := ink(r.Image)

	// User (170, 150): inside the triangle, above the clip. Must be blank.
	if got := px[(200-150)*w+170]; got != 0 {
		t.Errorf("ink %d at a point inside the triangle but outside the clip, want 0", got)
	}
	// User (150, 60): inside both. Must carry the fill.
	if got := px[(200-60)*w+150]; got < 200 {
		t.Errorf("ink %d at a point inside both, want the fill", got)
	}
}

// TestPageWithUndrawableTextIsRefusedNotHalfDrawn is the backend's safety property, and the
// reason it is a test rather than a comment: an incomplete rasterizer that returns an image is
// indistinguishable from a complete one that rendered a page with nothing on it.
//
// The page here is a fill *and* a line of text in a font the page never declares, which is the
// combination that matters: everything before the text draws, so a backend that returned what it
// managed would return a plausible page with a line missing. The error names why the text could
// not be drawn rather than which operator drew it, because the caller's next question is which
// feature to implement and "Tj" stopped answering it once Tj worked.
func TestPageWithUndrawableTextIsRefusedNotHalfDrawn(t *testing.T) {
	stream := "0 g 20 20 100 100 re f BT /F1 12 Tf 30 30 Td (text) Tj ET"
	path := onePagePDF(t, stream, 200, 200)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	o := render.DefaultOptions
	o.DPI = 72
	got, err := New(s).Page(1, o)
	if err == nil {
		t.Fatalf("Page returned an image for a page with text on it: %v", got)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("error %v does not unwrap to ErrUnsupported, so a caller cannot tell it from a broken page", err)
	}
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("error %v is not an *Unsupported, so nothing names the missing feature", err)
	}
	joined := strings.Join(u.Ops, " | ")
	if !strings.Contains(joined, "text:") || !strings.Contains(joined, "/Font") {
		t.Errorf("Ops = %v, want a text reason naming the missing /Font resource", u.Ops)
	}
	if u.Page != 1 {
		t.Errorf("Page = %d, want 1", u.Page)
	}
}

// TestPageBoxFallsBackInOrder pins §14.11.2's precedence and the last resort.
//
// A page with a /CropBox smaller than its /MediaBox renders the crop, because that is the
// visible region; a page with neither gets US Letter, because the other backend does and two
// backends disagreeing about a malformed page's size would be worse than either answer.
//
// Through a Store, not with a nil one: pageBox resolves the array it is given, because a
// producer may write the box as an indirect reference, and passing nil asserted that it does
// not — which it does, and the first version of this test found that out by panicking.
func TestPageBoxFallsBackInOrder(t *testing.T) {
	boxRef := objects.Ref{Num: 9}
	st := &boxStore{objs: map[objects.Ref]objects.Object{
		boxRef: objects.Array{objects.Int(10), objects.Int(20), objects.Int(110), objects.Int(220)},
	}}
	for _, c := range []struct {
		name           string
		page           objects.Dict
		x0, y0, x1, y1 float64
	}{
		{"crop box wins over media box", objects.Dict{
			"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
			"CropBox":  objects.Array{objects.Int(10), objects.Int(20), objects.Int(110), objects.Int(220)},
		}, 10, 20, 110, 220},
		{"an indirect crop box is resolved", objects.Dict{"CropBox": boxRef}, 10, 20, 110, 220},
		{"media box when there is no crop box", objects.Dict{
			"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(300), objects.Int(400)},
		}, 0, 0, 300, 400},
		{"letter when a page declares none", objects.Dict{}, 0, 0, 612, 792},
		{"letter when the box has no area", objects.Dict{
			"MediaBox": objects.Array{objects.Int(5), objects.Int(5), objects.Int(5), objects.Int(5)},
		}, 0, 0, 612, 792},
		{"media box when the crop box is unreadable", objects.Dict{
			"CropBox":  objects.Array{objects.Name("bad"), objects.Int(0), objects.Int(1), objects.Int(1)},
			"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(200), objects.Int(200)},
		}, 0, 0, 200, 200},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := pageBox(st, c.page)
			if got.X0 != c.x0 || got.Y0 != c.y0 || got.X1 != c.x1 || got.Y1 != c.y1 {
				t.Errorf("box = %v, want [%g %g %g %g]", got, c.x0, c.y0, c.x1, c.y1)
			}
		})
	}
}

// boxStore resolves references and nothing else, which is all pageBox asks of a Store.
type boxStore struct {
	objects.Store
	objs map[objects.Ref]objects.Object
}

func (b *boxStore) Resolve(o objects.Object) (objects.Object, error) {
	if ref, ok := o.(objects.Ref); ok {
		if v, ok := b.objs[ref]; ok {
			return v, nil
		}
		return objects.Null{}, nil
	}
	return o, nil
}

// TestRefusalListsEveryMissingFeature pins that the walk continues past the first thing it
// cannot draw.
//
// The error is a worklist and not a complaint: over a thousand pages the actionable question is
// which feature to implement next, and only the full list answers it. Stopping at the first
// unsupported operator would have reported "Tj" for every page in the corpus and hidden that
// gs blocks 1,184 of them and Do 173 — the ranking that decides what comes after text.
//
// The list mixes both kinds of entry on purpose: an operator this backend cannot draw at all, and
// a reason a drawable operator could not be drawn on this page. One list, because the caller's
// question is the same for both.
//
// Painting does stop, which is the other half and the reason walking on is affordable: lexing a
// stream is cheap, rasterizing into an image nobody will receive is not.
func TestRefusalListsEveryMissingFeature(t *testing.T) {
	stream := "0 g 20 20 100 100 re f BT /F1 12 Tf 30 30 Td (text) Tj ET " +
		"/GS0 gs 40 40 m 80 80 l S"
	path := onePagePDF(t, stream, 200, 200)
	s, err := pcstore.Open(path)
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
	got := map[string]bool{}
	for _, op := range u.Ops {
		got[op] = true
	}
	if !got["S"] {
		t.Errorf("Ops = %v, missing %q — the walk stopped at the first blocker", u.Ops, "S")
	}
	// The other two are keyed by reason rather than by operator, so they are matched by prefix:
	// one operator name stands for one missing feature, where one *reason* is the thing a caller
	// can act on. Both must be present, which is the property this test exists for.
	joined := strings.Join(u.Ops, " | ")
	for _, want := range []string{"gs:", "text:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Ops = %v, missing a %q reason — the walk stopped at the first blocker",
				u.Ops, want)
		}
	}
	// Sorted, so the message reads the same way every run and a diff of two logs is about the
	// features rather than about map order.
	for i := 1; i < len(u.Ops); i++ {
		if u.Ops[i-1] > u.Ops[i] {
			t.Errorf("Ops = %v, not sorted", u.Ops)
			break
		}
	}
}

// TestInlineImageIsRefused is the defect an enumeration of refusals produced.
//
// content.Scanner does not report an inline image as "BI" — it consumes BI, ID and EI and
// reports one op named "INLINE_IMAGE" — so a refusal list naming "BI" refused nothing, and a
// page whose only content was an inline image rendered blank, with no error and no indication.
// That is exactly the silent omission this backend exists to avoid, produced by the list rather
// than by the rasterizer, which is why the list is now an allow-list of operators that do not
// mark and everything else is refused by name.
func TestInlineImageIsRefused(t *testing.T) {
	stream := "q 200 0 0 200 0 0 cm BI /W 2 /H 2 /CS /G /BPC 8 /F /AHx ID 00ff ff00 > EI Q"
	path := onePagePDF(t, stream, 200, 200)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	if _, err := New(s).Page(1, o); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported: an inline image draws pixels", err)
	}
}

// TestUnknownOperatorIsRefused is the allow-list's own property, and the reason it is an
// allow-list: an operator this package has never heard of has to be refused, because the only
// alternative is to ignore it and ignoring one is how the inline image above went missing.
//
// PS is the real instance — a deprecated PostScript XObject, which draws — but the assertion is
// about the shape rather than that operator: a made-up name is refused too.
func TestUnknownOperatorIsRefused(t *testing.T) {
	for _, op := range []string{"PS", "zzz"} {
		t.Run(op, func(t *testing.T) {
			path := onePagePDF(t, "0 g 20 20 60 60 re f "+op, 200, 200)
			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()
			o := render.DefaultOptions
			o.DPI = 72
			if _, err := New(s).Page(1, o); !errors.Is(err, ErrUnsupported) {
				t.Errorf("err = %v, want ErrUnsupported", err)
			}
		})
	}
}

// TestClipIsRestoredByQ pins that a clip is part of the graphics state q saves.
//
// §8.4.2 lists the current clipping path among the parameters q saves and Q restores, and this
// backend keeps the clip as a mask on the canvas rather than inside content.Machine, so it has
// to stack it itself. It did not: a clip set inside a q…Q confined everything drawn after the Q
// as well, and a whole-page fill after one came out at a sixth of its ink.
//
// Compared against pdfium rather than asserted by pixel, because the page after the Q is
// unclipped and the whole of it is the assertion.
func TestClipIsRestoredByQ(t *testing.T) {
	for _, c := range []struct{ name, stream string }{
		{"clip inside q does not outlive Q", "q 0 0 20 20 re W n Q 0 g 0 0 200 200 re f"},
		{"clip inside q applies inside it", "q 100 20 80 80 re W n 0 g 0 0 200 200 re f Q"},
		{"nested q each restore their own", "q 0 0 100 200 re W n q 0 0 200 40 re W n Q 0 g 0 0 200 200 re f Q"},
	} {
		t.Run(c.name, func(t *testing.T) {
			mean, worst, over := compare(t, c.stream, 200, 72)
			t.Logf("mean %.3f worst %.0f over-32 %d", mean, worst, over)
			if mean > 1.0 || over > 0 {
				t.Errorf("mean %.3f, %d pixels over 32 — the clip stack disagrees with pdfium", mean, over)
			}
		})
	}
}

// TestQRestoresTheFillColourAndTheFont pins the two parameters Q restored nowhere until form
// XObjects needed a real q.
//
// §8.4.2 lists the current colour and the text font among what q saves, and content.Machine
// restored its own copy of the text state while this walker kept the colour and the resolved font
// outside it. So "q 1 0 0 rg Q … f" filled red and a Tf inside q…Q stayed in force after the Q.
// Every other test set its colour after its last Q, which is why nothing saw either.
func TestQRestoresTheFillColourAndTheFont(t *testing.T) {
	t.Run("fill colour", func(t *testing.T) {
		mean, worst, over := compare(t, "q 1 0 0 rg Q 20 20 160 160 re f", 200, 72)
		t.Logf("mean %.3f worst %.0f over-32 %d", mean, worst, over)
		if mean > 1.0 || over > 0 {
			t.Errorf("mean %.3f, %d pixels over 32 — the colour set inside q outlived its Q", mean, over)
		}
	})
	t.Run("font", func(t *testing.T) {
		path := textPDF(t, "q BT /F1 40 Tf ET Q BT 20 60 Td (A) Tj ET", 200)
		s, err := pcstore.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		_, err = New(s).Page(1, render.DefaultOptions)
		var u *Unsupported
		if !errors.As(err, &u) || !strings.Contains(strings.Join(u.Ops, "|"), "before naming a font") {
			t.Fatalf("got %v, want the page refused for showing a string with no font — the Tf was inside q…Q", err)
		}
	})
}

// TestHostileCoordinatesDoNotHangOrPanic pins the two ways a content stream stopped this
// package rather than being refused by it.
//
// Both inputs are numbers a producer can write and the lexer accepts. A coordinate of 1e19
// converted to int is the most negative int64 on amd64, because Go leaves an out-of-range
// float-to-int conversion implementation-defined — so the row loop ran about 9.2×10¹⁸ times and
// the process never returned. And an edge whose y span and x span both overflow to ±Inf makes
// the crossing arithmetic 0×Inf, which is NaN: every comparison against it is false, so it
// passed the span guard and indexed the coverage row at the most negative int64.
//
// The assertion is that Page returns, not what it draws: a page built from numbers like these
// has no correct rendering, and the property under test is that a hostile stream costs a result
// rather than the process.
func TestHostileCoordinatesDoNotHangOrPanic(t *testing.T) {
	big := "10000000000000000000.0"
	huge := "1" + strings.Repeat("0", 308) + ".0"
	for _, c := range []struct{ name, stream string }{
		{"a coordinate past what an int64 holds", "0 g 0 -" + big + " m 10 -" + big + " l 20 -" + big + " l f"},
		{"an edge whose spans overflow to infinity", "0 g 5 5 m " + huge + " " + huge + " l -" + huge + " -" + huge + " l f"},
		{"a rectangle of infinite extent", "0 g 0 0 " + huge + " " + huge + " re f"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := onePagePDF(t, c.stream, 200, 200)
			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()
			o := render.DefaultOptions
			o.DPI = 72

			done := make(chan struct{})
			go func() {
				defer close(done)
				// A panic here fails the test rather than the process, which is the point: the
				// first version of this rasterizer panicked with a negative index.
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
				t.Fatal("Page did not return within 10s: a coordinate a producer controls must not hang the rasterizer")
			}
		})
	}
}

// TestManySegmentsIsNotQuadratic pins that the rasterizer's cost follows the path rather than
// the page times the path.
//
// Every sample of every row used to rescan every edge, so a zig-zag spanning a page took 11
// seconds at 1,000 segments and did not finish in ten minutes at 10,000. Edges are bucketed by
// the first row they can cross and carried in an active list now. The bound is generous — this
// is a timing assertion and a loaded machine must not fail it — because the defect it guards
// was three orders of magnitude, not a factor of two.
func TestManySegmentsIsNotQuadratic(t *testing.T) {
	// Ten thousand segments spanning the full page height, which is what makes the two
	// behaviours differ by three orders of magnitude rather than by a factor: rescanning every
	// edge on every sample of every row is 10,000 × 16 × 792 edge tests, where bucketing them
	// by their first row is proportional to the path.
	var b strings.Builder
	b.WriteString("0 g 0 0 m ")
	for i := 0; i < 10000; i++ {
		x := 10 + (i%2)*580
		y := 10 + i*780/10000
		fmt.Fprintf(&b, "%d %d l ", x, y)
	}
	b.WriteString("f")

	path := onePagePDF(t, b.String(), 612, 792)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72

	start := time.Now()
	if _, err := New(s).Page(1, o); err != nil {
		t.Fatalf("Page: %v", err)
	}
	// 100 ms, from both measurements rather than from taste: as shipped this is 5.5 ms and with
	// the edges rescanned per sample it is 154 ms, so the bound sits 18× above the one and
	// 1.5× below the other. A loaded machine has room; a rescan does not.
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("10,000 segments took %v, want under 100ms — 5.5ms is the measured figure and"+
			" 154ms is what rescanning every edge per sample costs", d)
	} else {
		t.Logf("10,000 segments in %v", d)
	}
}

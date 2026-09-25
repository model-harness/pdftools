package native

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
)

// gsPDF writes a one-page PDF whose /ExtGState resource is the dictionary given.
//
// A fixture rather than a committed file, for the reason ADR 0015 gives about the fourteen path
// streams: the subject is which ExtGState parameter produces which outcome, and a file per case
// would be a directory of near-identical PDFs. No producer this repo can drive emits an ExtGState
// with a deliberately unknown key in it either.
func gsPDF(t *testing.T, extg, stream string) string {
	t.Helper()
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]" +
			"/Resources<</ExtGState<</GS0 " + extg + ">>>>/Contents 4 0 R>>",
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(stream), stream),
	}
	return buildPDF(t, objs, "gs.pdf")
}

// renderGS renders a page that applies /GS0 before filling the whole page.
func renderGS(t *testing.T, extg string) (*render.Raster, error) {
	t.Helper()
	path := gsPDF(t, extg, "0 g /GS0 gs 0 0 200 200 re f")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	return New(s).Page(1, o)
}

// TestExtGStateAcceptsWhatCannotMarkAndRefusesWhatCan is the ExtGState classification.
//
// # Why every accepted key needs its own case
//
// Because accepting one is a claim — "this parameter cannot change a pixel this backend draws" —
// and the claims are not interchangeable. `/SA` is inert because stroke adjustment is a hint a
// device may decline; `/OP` because this backend has no separations; `/TK` because it only matters
// under a blend mode that is itself refused. (`/LW` and the rest of the stroke geometry are not
// inert at all but applied, which TestExtGStateSetsTheStrokeGeometry shows.) A test that accepted
// them as a group would pass if the group were replaced by "accept everything", which is the exact
// mutation this table exists to kill.
//
// The refusals matter more. An ExtGState this backend does not understand and accepts anyway draws
// a page whose graphics state it guessed at — the silent-omission failure the whole backend is
// built to avoid, arriving through the one operator that looks like it changes nothing.
func TestExtGStateAcceptsWhatCannotMarkAndRefusesWhatCan(t *testing.T) {
	for _, c := range []struct {
		name string
		extg string
		want string // a substring of the refusal, or "" to require the page to draw
	}{
		// Accepted, each for its own reason.
		{"just a type", "<</Type/ExtGState>>", ""},
		{"opaque alpha", "<</ca 1/CA 1>>", ""},
		{"normal blend", "<</BM/Normal>>", ""},
		{"compatible blend, which is Normal under its 1.3 name", "<</BM/Compatible>>", ""},
		{"no soft mask", "<</SMask/None>>", ""},
		// Applied rather than inert, and accepted on a page that strokes nothing: a dash is refused only
		// at a stroke, as TestUndrawableStrokesAreRefusedByReason shows.
		{"stroke geometry, with nothing stroked", "<</LW 4/LC 1/LJ 2/ML 8/D[[2 2]0]/SA true>>", ""},
		{"overprint, which needs separations this backend has not got", "<</OP true/op true/OPM 1>>", ""},
		{"black generation and undercolour removal, for a CMYK device", "<</BG2/Default/UCR2/Default>>", ""},
		{"screening and tolerance hints", "<</HT/Default/FL 1/SM 0.02>>", ""},
		{"a rendering intent, while only device spaces are supported", "<</RI/RelativeColorimetric>>", ""},
		{"text knockout and alpha-is-shape, both inert without a mask", "<</TK true/AIS false>>", ""},
		// A partial alpha is honoured rather than refused, so it draws.
		{"partial alpha", "<</ca 0.5>>", ""},

		// Refused, each naming what it is.
		{"a real blend mode", "<</BM/Multiply>>", "/BM is /Multiply"},
		{"a blend mode array", "<</BM[/Darken/Normal]>>", "/BM is /Darken"},
		{"a soft mask group", "<</SMask<</S/Luminosity/G 5 0 R>>>>", "soft mask"},
		{"a font from the graphics state", "<</Font[5 0 R 12]>>", "/Font sets the text font"},
		{"an alpha outside 0..1", "<</ca 1.5>>", "outside 0..1"},
		{"an alpha that is not a number", "<</ca/Half>>", "not a number"},
		// The default case, which is the whole reason this is an allow-list: a key nobody
		// enumerated must refuse rather than be ignored.
		{"a key this backend has never heard of", "<</Frobnicate 3>>", "not a parameter this backend reads"},
		{"a transfer function", "<</TR/Identity>>", "/TR is not a parameter"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ra, err := renderGS(t, c.extg)
			if c.want == "" {
				if err != nil {
					t.Fatalf("Page: %v — this ExtGState cannot change a pixel and must be accepted", err)
				}
				if ra == nil {
					t.Fatal("no raster and no error")
				}
				return
			}
			var u *Unsupported
			if !errors.As(err, &u) {
				t.Fatalf("err = %v, want an *Unsupported — accepting this draws a page whose"+
					" graphics state was guessed at", err)
			}
			joined := strings.Join(u.Ops, " | ")
			if !strings.Contains(joined, c.want) {
				t.Errorf("refusal = %q, want it to mention %q", joined, c.want)
			}
			if !strings.Contains(joined, "gs:") {
				t.Errorf("refusal = %q, want it keyed as a gs reason so a corpus log ranks it", joined)
			}
		})
	}
}

// TestUnresolvableExtGStateIsRefused pins the name that resolves to nothing.
//
// A `/GS0 gs` naming an ExtGState the resources do not hold is a page whose graphics state is
// unknowable rather than empty: the producer set something and this reader cannot see what. Taking
// it as "no change" would draw the page as though the operator were absent.
func TestUnresolvableExtGStateIsRefused(t *testing.T) {
	path := gsPDF(t, "<</Type/ExtGState>>", "0 g /Missing gs 0 0 200 200 re f")
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
	if joined := strings.Join(u.Ops, " | "); !strings.Contains(joined, "not in the page's /ExtGState") {
		t.Errorf("refusal = %q, want it to name the unresolvable resource", joined)
	}
}

// TestConstantAlphaScalesCoverage pins /ca as arithmetic rather than as a flag.
//
// Half alpha over a white page is mid grey, and the value is checked rather than the direction:
// an implementation that treated /ca as "some transparency" could produce any lighter shade, and
// one that ignored it entirely produces black. 128 of 255 is what coverage times 0.5 gives over
// white, and the tolerance is one level for the rounding.
func TestConstantAlphaScalesCoverage(t *testing.T) {
	for _, c := range []struct {
		name  string
		extg  string
		wantR uint8
	}{
		{"opaque", "<</ca 1>>", 0},
		{"half", "<</ca 0.5>>", 128},
		{"a quarter", "<</ca 0.25>>", 191},
		{"fully transparent", "<</ca 0>>", 255},
	} {
		t.Run(c.name, func(t *testing.T) {
			ra, err := renderGS(t, c.extg)
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			r, g, b := rgbAt(ra.Image, 100, 100)
			if absInt(int(r)-int(c.wantR)) > 1 || r != g || g != b {
				t.Errorf("centre = (%d,%d,%d), want a neutral %d — /ca multiplies coverage",
					r, g, b, c.wantR)
			}
		})
	}
}

// TestAlphaIsRestoredByQ pins that /ca is graphics state and not a global.
//
// §8.4.2 lists the constant alphas among the parameters q saves, and a mask on the canvas cannot
// be stacked by content.Machine — the same reason the clip is stacked in this walker. Without it a
// half-transparent fill inside a q…Q would leave everything drawn after the Q half transparent
// too, which looks like a page rendered slightly pale rather than like a defect.
func TestAlphaIsRestoredByQ(t *testing.T) {
	// A transparent fill on the left inside q…Q, then an opaque fill on the right after it.
	stream := "0 g q /GS0 gs 0 0 100 200 re f Q 100 0 100 200 re f"
	path := gsPDF(t, "<</ca 0.25>>", stream)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	ra, err := New(s).Page(1, o)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	lr, _, _ := rgbAt(ra.Image, 50, 100)
	rr, _, _ := rgbAt(ra.Image, 150, 100)
	if absInt(int(lr)-191) > 1 {
		t.Errorf("left half = %d, want 191 — the transparent fill inside q…Q", lr)
	}
	if rr != 0 {
		t.Errorf("right half = %d, want 0 — Q must restore the alpha the q saved", rr)
	}
}

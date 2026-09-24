package native

import (
	"errors"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/font"
	"github.com/model-harness/pdftools/internal/liberation"
	"github.com/model-harness/pdftools/internal/ttfbuild"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// fixedSource hands out one program for every request, and records what it was asked for.
//
// The hand-built font rather than a real face, because these tests are about the substitution
// *path* — does a font with no program reach a face, through which lookup, at which size — and a
// real face would make every assertion about someone's Arial. The recorded requests are how the
// inference is checked: what this package concluded from a name and a descriptor is exactly what
// arrives here.
type fixedSource struct {
	tt       *font.TrueType
	seen     []font.FaceRequest
	declineF font.Family // a family this source refuses, to exercise the decline path
	decline  bool
}

func (s *fixedSource) Face(req font.FaceRequest) (*font.TrueType, bool) {
	s.seen = append(s.seen, req)
	if s.decline && req.Style.Family == s.declineF {
		return nil, false
	}
	return s.tt, true
}

func newFixedSource(t *testing.T) *fixedSource {
	t.Helper()
	tt, err := font.ParseTrueType(ttfbuild.Builder{UnitsPerEm: 1000}.Build())
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	return &fixedSource{tt: tt}
}

// newRealSource records requests like fixedSource and answers them with an actual Liberation face.
//
// Needed wherever the substitute's *glyph count* is part of the assertion. The hand-built font has
// six glyphs, so a code above six cannot reach the code-as-index route at all — which made a test
// of "that route must not fire on a substitute" pass for the wrong reason, since the route was
// unreachable rather than refused. A real face has thousands, so code 65 resolves to a glyph and
// the difference between allowing the route and forbidding it becomes visible.
func newRealSource(t *testing.T) *fixedSource {
	t.Helper()
	tt, ok := liberation.New().Face(font.FaceRequest{Style: font.Style{Family: font.FamilySans}})
	if !ok {
		t.Fatal("liberation has no sans face")
	}
	if tt.NumGlyphs() < 200 {
		t.Fatalf("the sans face has %d glyphs, too few for this test's premise", tt.NumGlyphs())
	}
	return &fixedSource{tt: tt}
}

// TestSubstitutedPageMatchesTheSamePageEmbedded is this increment's primary acceptance bar.
//
// # Why not pdfium
//
// Because pdfium cannot be the oracle for a substituted face, and that is a fact about the
// problem rather than a shortcut. pdfium substitutes Foxit's own standard-14 clones; this backend
// substitutes whatever its FaceSource supplies. The two therefore draw *different typefaces* by
// construction, and no per-pixel tolerance can separate "a different face" from "a defect" — a
// bound loose enough to admit the first admits the second. It is the third place this phase has
// had to name pdfium as not the yardstick, after CMYK and glyph grid-fitting.
//
// # What is asserted instead
//
// Exactly the claim substitution makes: *this page looks as if the face had been embedded.* The
// same stream is rendered twice — once against a font dictionary with no program, so the face
// arrives through the substitution path, and once against the same dictionary with the very same
// program embedded — and the two must agree to the pixel. Same rasterizer, same outlines, same
// advances; the only difference is where the bytes came from. So the tolerance is zero, which is
// tighter than anything an external oracle could give.
//
// What this cannot see is whether the face chosen was the *right* one: if the style inference
// picked bold for a regular font, both renderings would pick bold and agree. That is what
// TestSubstitutedStyleIsInferredFromTheFont and the geometry check against pdfium cover, and the
// three together are the bar ADR 0017 states.
func TestSubstitutedPageMatchesTheSamePageEmbedded(t *testing.T) {
	for _, c := range []struct{ name, stream string }{
		{"one glyph", "BT /F1 48 Tf 20 100 Td (A) Tj ET"},
		{"a line", "BT /F1 24 Tf 20 100 Td (ABCABC) Tj ET"},
		{"two lines and a kern", "BT /F1 24 Tf 40 TL 20 150 Td [(AB) -200 (C)] TJ T* (BC) Tj ET"},
		// A curve and a composite through the substituted program, because the outline path is
		// the same code and a substitute that arrived truncated would show here first.
		{"curves and composites", "BT /F1 48 Tf 20 100 Td (BCD) Tj ET"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := newFixedSource(t)
			// The substituted page: a descriptor that declares a face and embeds no program.
			got := renderWith(t, textPDF(t, c.stream, 200, withNoFontFile()), src)
			// The embedded page: the identical program, in the dictionary.
			want := renderWith(t, textPDF(t, c.stream, 200), nil)

			if len(src.seen) == 0 {
				t.Fatal("the face source was never asked, so this compares two embedded renders")
			}
			diff, ink := 0, 0.0
			for i := range want {
				if got[i] != want[i] {
					diff++
				}
				ink += float64(want[i])
			}
			if ink == 0 {
				t.Fatal("the embedded render drew nothing, so this comparison asserts nothing")
			}
			if diff != 0 {
				t.Errorf("%d pixels differ from the same page with the face embedded, want 0 —"+
					" the substitution path changed the glyphs, their size or their advance", diff)
			}
		})
	}
}

// TestSubstitutedStyleIsInferredFromTheFont pins what the FaceSource is asked for.
//
// This is the half the pixel comparison above is blind to. A request carries the canonical style
// this package inferred, and the inference reads a name and a descriptor that routinely disagree:
// a producer writes "Arial-BoldMT" and sets no weight, or sets /ItalicAngle and no italic flag.
// Getting it wrong substitutes a real face at the wrong weight, which renders a page that is
// entirely legible and set in the wrong type — the failure this whole backend refuses pages to
// avoid, one level up from a missing glyph.
func TestSubstitutedStyleIsInferredFromTheFont(t *testing.T) {
	// Flags bit 1 is serif, bit 2 (value 4) is symbolic, bit 7 (value 64) is italic.
	const serifFlag, italicFlag, monoFlag = 2, 64, 1

	for _, c := range []struct {
		name  string
		base  string
		flags int
		want  font.Style
	}{
		{"a plain sans face", "Helvetica", 0, font.Style{Family: font.FamilySans}},
		{"serif by flag", "Whatever", serifFlag, font.Style{Family: font.FamilySerif}},
		// The name overrides an unset serif flag, because unset means "not stated" far more often
		// than it means "no serifs".
		{"serif by name", "TimesNewRomanPSMT", 0, font.Style{Family: font.FamilySerif}},
		{"sans by name against a serif flag", "ArialMT", serifFlag, font.Style{Family: font.FamilySans}},
		{"bold by name with no weight stated", "Arial-BoldMT", 0,
			font.Style{Family: font.FamilySans, Bold: true}},
		{"italic by flag", "Whatever", italicFlag, font.Style{Family: font.FamilySans, Italic: true}},
		{"bold italic by name", "Arial-BoldItalicMT", 0,
			font.Style{Family: font.FamilySans, Bold: true, Italic: true}},
		{"mono by flag", "Whatever", monoFlag, font.Style{Family: font.FamilyMono}},
		{"mono by name", "CourierNewPSMT", 0, font.Style{Family: font.FamilyMono}},
		// Mono *and* serif, which is the only input that can tell the two branches' order apart:
		// Courier has serifs and is fixed-pitch, and a fixed-pitch page set in a proportional
		// serif face reflows every column it has. Fixed pitch is the property that matters, so it
		// is checked first. Without this case the two branches could be swapped unnoticed.
		{"a serif monospaced face resolves as mono", "CourierNewPS-BoldMT", serifFlag,
			font.Style{Family: font.FamilyMono, Bold: true}},
		// A symbol face is its own family and carries no weight: no text face stands in for it,
		// and asking for "symbol bold" would ask for a variant that does not exist.
		{"a symbol face", "ZapfDingbats", 0, font.Style{Family: font.FamilySymbol}},
		{"symbol by name, ignoring bold", "Wingdings-Bold", 0, font.Style{Family: font.FamilySymbol}},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := newFixedSource(t)
			renderWith(t, textPDF(t, "BT /F1 24 Tf 20 100 Td (A) Tj ET", 200,
				withNoFontFile(), withBaseFont(c.base), withFlags(c.flags)), src)
			if len(src.seen) != 1 {
				t.Fatalf("the source was asked %d times, want once", len(src.seen))
			}
			req := src.seen[0]
			if req.Style != c.want {
				t.Errorf("style = %v, want %v", req.Style, c.want)
			}
			if req.Name != c.base {
				t.Errorf("name = %q, want %q — a caller may match on it", req.Name, c.base)
			}
			if req.Why == "" {
				t.Error("Why is empty, so a caller cannot tell a standard-14 font from a"+
					" descriptor with no program", req.Why)
			}
		})
	}
}

// TestADeclinedFaceLeavesThePageRefused pins that a source may say no.
//
// Declining has to be a normal answer rather than an error, because the one case that matters is a
// source with no symbol face: substituting Latin letters for mathematics draws a page that looks
// like text and says something else, which is worse than not drawing it. The refusal has to name
// the style so the log says which face is missing.
func TestADeclinedFaceLeavesThePageRefused(t *testing.T) {
	src := newFixedSource(t)
	src.decline, src.declineF = true, font.FamilySymbol

	s, err := pcstore.Open(textPDF(t, "BT /F1 24 Tf 20 100 Td (A) Tj ET", 200,
		withNoFontFile(), withBaseFont("ZapfDingbats")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	_, err = New(s, WithFaces(src)).Page(1, o)

	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want an *Unsupported — a declined face must not draw a text face", err)
	}
	joined := strings.Join(u.Ops, " | ")
	if !strings.Contains(joined, "symbol") {
		t.Errorf("refusal = %q, want it to name the symbol style the source has no face for", joined)
	}
}

// TestASubstitutedFaceIsReachedOnlyThroughTheCharacter pins the sharpest consequence of
// substituting at all.
//
// Every other code-to-glyph route asks the *document* which glyph it means: /CIDToGIDMap indexes
// the program the document embedded, a symbolic cmap is the one its subsetter wrote, and the last
// resort treats the code itself as an index into it. None of those indices mean anything in a face
// the document has never seen — following them picks whatever glyph sits at that position and draws
// it with complete confidence.
//
// So the fixture is a font whose text cannot be decoded at all: symbolic, no /Encoding and no
// /ToUnicode, which §9.6.5.4 leaves genuinely unreadable. With a face supplied and the character
// route the only one allowed, the page must still be refused. With the document's routes left
// available it would resolve code 65 to *glyph 65* of the substitute and draw an arbitrary letter,
// and the page would come back looking like text.
//
// This is the case the pixel-exact comparison above cannot see, because there the substitute and the
// embedded program are the same bytes, so every route agrees.
func TestASubstitutedFaceIsReachedOnlyThroughTheCharacter(t *testing.T) {
	// A real face, because the assertion needs the forbidden route to be *reachable*: the
	// hand-built font's six glyphs put code 65 out of range, so the page would be refused whether
	// the route were forbidden or not.
	src := newRealSource(t)
	// Flags 4 is symbolic, and the fixture writes neither /Encoding nor /ToUnicode — so there is
	// no base table and §9.6.5.4 leaves the codes genuinely unreadable, which is what makes this
	// the one input where a substituted face has nothing to look a code up by.
	path := textPDF(t, "BT /F1 24 Tf 20 100 Td (A) Tj ET", 200,
		withNoFontFile(), withFlags(4))

	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	_, err = New(s, WithFaces(src)).Page(1, o)

	if len(src.seen) == 0 {
		t.Fatal("the face source was never asked, so this asserts nothing about substitution")
	}
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want an *Unsupported — a code with no character cannot reach a"+
			" substituted glyph, and drawing one anyway puts an arbitrary letter on the page", err)
	}
	if joined := strings.Join(u.Ops, " | "); !strings.Contains(joined, "no glyph for code 65") {
		t.Errorf("refusal = %q, want it to name the code that could not be resolved", joined)
	}
}

// TestSubstitutedGeometryAgreesWithPdfium is the secondary bar, and the one that can see a face
// chosen at the wrong weight.
//
// Per-pixel is impossible here — pdfium draws Foxit's faces — so what is asserted is what does not
// depend on which face drew it: where the ink is, and how much of it there is. A glyph run at the
// wrong size, in the wrong place, with the wrong advance, or absent moves the bounding box or the
// total; a different typeface of the same style at the same size moves neither much.
//
// The tolerances are measured, not chosen, and they are wide because they have to admit two
// different typefaces. That is the honest weakness of this assertion and the reason it is secondary
// to the pixel-exact comparison above rather than a substitute for it.
func TestSubstitutedGeometryAgreesWithPdfium(t *testing.T) {
	// A page of Helvetica with no program at all, which is a standard-14 font and the corpus's
	// most common substitution by a wide margin — 1,138 of 1,251 pages.
	path := textPDF(t, "BT /F1 36 Tf 20 100 Td (ABC) Tj ET", 200,
		withNoDescriptor(), withBaseFont("Helvetica"))

	o := render.DefaultOptions
	o.DPI = 72
	ref, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium: %v", err)
	}
	defer func() { _ = ref.Close() }()
	want, err := ref.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}

	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	// The real Liberation face rather than the hand-built one, because this is the assertion where
	// the actual substitute matters: Liberation is metric-compatible with Arial and Times by
	// construction, so comparing it against pdfium's Foxit Helvetica is a comparison of two faces
	// that are *supposed* to occupy the same space. The hand-built font's square-and-diamond
	// glyphs would pass this on a coincidence of extents and prove nothing about the real one.
	got, err := New(s, WithFaces(liberation.New())).Page(1, o)
	if err != nil {
		t.Fatalf("native Page: %v", err)
	}

	wb, ww, wh := ink(want.Image)
	gb, _, _ := ink(got.Image)
	wx0, wy0, wx1, wy1, wink := inkBounds(wb, ww, wh)
	gx0, gy0, gx1, gy1, gink := inkBounds(gb, ww, wh)
	t.Logf("pdfium bbox (%d,%d)-(%d,%d) ink %.0f", wx0, wy0, wx1, wy1, wink)
	t.Logf("native bbox (%d,%d)-(%d,%d) ink %.0f", gx0, gy0, gx1, gy1, gink)

	if wink == 0 {
		t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
	}
	// Two device pixels, which is what the measurement supports rather than what felt safe: with
	// Liberation against pdfium's Foxit Helvetica the left, right and bottom edges are *identical*
	// and the top differs by one, because both faces are built to Arial's metrics. An earlier
	// draft allowed six, which was headroom for the hand-built fixture font this test used before
	// and is loose enough to admit a genuinely misplaced run.
	const edge = 2
	for _, e := range []struct {
		what     string
		got, ref int
	}{
		{"left", gx0, wx0}, {"top", gy0, wy0}, {"right", gx1, wx1}, {"bottom", gy1, wy1},
	} {
		if d := e.got - e.ref; d < -edge || d > edge {
			t.Errorf("%s edge of the ink is %d against pdfium's %d, %+d away, want within %d",
				e.what, e.got, e.ref, d, edge)
		}
	}
	// Total ink within 20%: measured at 0.909, because Foxit's Helvetica has slightly heavier
	// stems than Liberation and this backend does not hint. A weight change roughly doubles it, so
	// 20% separates the two faces from a bold substituted for a regular.
	if r := gink / wink; r < 0.80 || r > 1.20 {
		t.Errorf("ink ratio %.3f, want within 20%% — the face is at the wrong weight or size", r)
	}
}

// inkBounds returns the bounding box of any ink and the total, for a face-independent comparison.
func inkBounds(px []byte, w, h int) (x0, y0, x1, y1 int, total float64) {
	x0, y0, x1, y1 = w, h, -1, -1
	for y := range h {
		for x := range w {
			v := px[y*w+x]
			if v < 32 {
				continue
			}
			total += float64(v)
			x0, y0 = minInt(x0, x), minInt(y0, y)
			x1, y1 = maxInt(x1, x), maxInt(y1, y)
		}
	}
	return x0, y0, x1, y1, total
}

// renderWith renders page 1 of path, optionally through a face source.
func renderWith(t *testing.T, path string, src font.FaceSource) []byte {
	t.Helper()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	var opts []Option
	if src != nil {
		opts = append(opts, WithFaces(src))
	}
	ra, err := New(s, opts...).Page(1, o)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	px, _, _ := ink(ra.Image)
	return px
}

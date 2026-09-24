package native

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/ttfbuild"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// textPDF writes a one-page PDF that embeds a TrueType font and shows text in it.
//
// The font comes from internal/ttfbuild rather than from the system, so this test asserts what a
// page set in a *known* font looks like: the glyphs' outlines were written by hand, which is the
// only way the comparison below is about this package's rasterizer rather than about whose copy of
// Arial the machine has.
func textPDF(t *testing.T, stream string, size int, opts ...func(*textPDFOpts)) string {
	t.Helper()
	o := textPDFOpts{prog: ttfbuild.Builder{UnitsPerEm: 1000}.Build(), fontFile: "FontFile2",
		baseFont: "Test"}
	for _, f := range opts {
		f(&o)
	}

	// /ItalicAngle is 0 and /StemV 80 so the descriptor states neither italic nor bold: the style
	// inference reads both, and a fixture that quietly declared one would make every case below
	// about the fixture rather than about the evidence under test.
	desc := fmt.Sprintf("<</Type/FontDescriptor/FontName/%s/Flags %d/ItalicAngle 0"+
		"/Ascent 800/Descent -200/CapHeight 700/StemV 80/FontBBox[0 -200 1000 800]/%s 6 0 R>>",
		o.baseFont, o.flags, o.fontFile)

	// /Widths from the space to 'C', in 1/1000 em: 250 for the space, 500 for the square and 600
	// for the other two, with the unused codes between them zero. Stated in the dictionary rather
	// than left to the font's hmtx because §9.2.4 says a simple font's advance comes from /Widths
	// and a reader that fell back to the program would be a second answer — and because a wrong
	// advance shows as overlapping or gappy glyphs rather than as nothing.
	widths := make([]string, 0, 'D'-' '+1)
	for c := ' '; c <= 'D'; c++ {
		switch c {
		case ' ':
			widths = append(widths, "250")
		case 'A':
			widths = append(widths, "500")
		case 'B', 'C', 'D':
			widths = append(widths, "600")
		default:
			widths = append(widths, "0")
		}
	}
	// A standard-14 font has no /FontDescriptor at all, which is the shape 7 of the corpus's fonts
	// and 1,138 of its pages have — and the case where /BaseFont is the *only* evidence of which
	// face the page wants.
	descRef := "/FontDescriptor 5 0 R"
	if o.noDescriptor {
		descRef = ""
	}
	fontDict := fmt.Sprintf("<</Type/Font/Subtype/TrueType/BaseFont/%s/FirstChar 32/LastChar 68"+
		"/Widths [%s]%s%s>>", o.baseFont, strings.Join(widths, " "), descRef, o.encoding)
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 %d %d]"+
			"/Resources<</Font<</F1 4 0 R>>%s>>/Contents 7 0 R>>", size, size, o.xobjects),
		fontDict,
		desc,
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(o.prog), o.prog),
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(stream), stream),
	}
	if o.noFontFile {
		// A descriptor that declares a face and embeds no program: the other substitution shape,
		// and the one that still has /Flags and the metrics to infer a style from.
		objs[4] = fmt.Sprintf("<</Type/FontDescriptor/FontName/%s/Flags %d/ItalicAngle 0"+
			"/Ascent 800/Descent -200/CapHeight 700/StemV 80/FontBBox[0 -200 1000 800]>>",
			o.baseFont, o.flags)
	}
	if o.composite {
		// A Type0 font over a CIDFontType2 descendant, Identity-H encoded: two-byte codes that
		// are CIDs, mapped to glyphs by /CIDToGIDMap, which defaults to Identity. This is what
		// all 67 of the corpus's CIDFontType2 fonts look like, and the descriptor moves to the
		// descendant because a Type0 dictionary has none of its own (§9.7.4.1).
		objs[3] = "<</Type/Font/Subtype/Type0/BaseFont/Test/Encoding/Identity-H" +
			"/DescendantFonts[8 0 R]>>"
		// /CIDToGIDMap as a stream that maps CID 1 to the *diamond*, not to glyph 1. The
		// identity would send it to glyph 1, and so would the code-as-glyph-index last resort,
		// so a non-identity map is the only fixture that can tell this route from either.
		objs = append(objs, "<</Type/Font/Subtype/CIDFontType2/BaseFont/Test"+
			"/CIDSystemInfo<</Registry(Adobe)/Ordering(Identity)/Supplement 0>>"+
			"/FontDescriptor 5 0 R/DW 600/CIDToGIDMap 9 0 R>>")
		// Long enough to reach CID 32, which is mapped to the square. A two-byte code of 32 is
		// what §9.3.3's rule turns on: word spacing applies to a *single-byte* code 32 and to
		// nothing else, so a composite font whose CIDs happen to include 32 is the only fixture
		// that can catch the guard being dropped. Real CJK text hits it constantly.
		m := make([]byte, 2*33)
		m[2*1+1] = ttfbuild.GIDDiamond
		m[2*32+1] = ttfbuild.GIDSquare
		objs = append(objs, fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(m), m))
	}
	return buildPDF(t, append(objs, o.extra...), "text.pdf")
}

type textPDFOpts struct {
	prog         []byte
	flags        int
	encoding     string
	fontFile     string
	baseFont     string
	noDescriptor bool
	noFontFile   bool
	composite    bool
	xobjects     string
	extra        []string
}

// withXObjects adds an /XObject resource dictionary to the page, and objects numbered from 8 for
// it to refer to — which is where they land only without withComposite, and no test needs both.
func withXObjects(dict string, objs ...string) func(*textPDFOpts) {
	return func(o *textPDFOpts) { o.xobjects, o.extra = "/XObject<<"+dict+">>", objs }
}

func withProgram(p []byte) func(*textPDFOpts) { return func(o *textPDFOpts) { o.prog = p } }
func withComposite() func(*textPDFOpts)       { return func(o *textPDFOpts) { o.composite = true } }

func withFlags(f int) func(*textPDFOpts) { return func(o *textPDFOpts) { o.flags = f } }

// withBaseFont names the face the dictionary claims, which is half of what the style inference
// reads: /BaseFont is the only evidence a standard-14 font offers, since it has no descriptor.
func withBaseFont(n string) func(*textPDFOpts) {
	return func(o *textPDFOpts) { o.baseFont = n }
}

// withNoDescriptor is the standard-14 shape: a font dictionary with no /FontDescriptor at all,
// which is how 7 of the corpus's fonts and 1,138 of its pages ask for Helvetica.
func withNoDescriptor() func(*textPDFOpts) {
	return func(o *textPDFOpts) { o.noDescriptor = true }
}
func withNoFontFile() func(*textPDFOpts) { return func(o *textPDFOpts) { o.noFontFile = true } }
func withFontFileKey(k string) func(*textPDFOpts) {
	return func(o *textPDFOpts) { o.fontFile = k }
}

// TestTextAgreesWithPdfium is this increment's acceptance test: a page of glyphs drawn from an
// embedded TrueType program, against the engine this one is replacing.
//
// A glyph is a path, so nothing in the rasterizer is new — what is new is everything between a
// show operator and a path: the code-to-glyph lookup, the outline, the quadratic curves, the text
// and render matrices, and the advance. Each of those has its own way of being subtly wrong that a
// unit test cannot see: a glyph drawn at the wrong size, upside down, at the wrong place on the
// line, or at the right place with the wrong advance to the next one. Comparing a whole line
// against another implementation is what catches those, and it is why this test compares a *string*
// rather than one glyph.
func TestTextAgreesWithPdfium(t *testing.T) {
	for _, c := range []struct {
		name   string
		stream string
		over   int
	}{
		{"one glyph", "BT /F1 48 Tf 20 100 Td (A) Tj ET", 0},
		{"a line of glyphs", "BT /F1 36 Tf 20 100 Td (AAA) Tj ET", 0},
		// The diamond is four off-curve points: a reader that misses TrueType's implied
		// midpoints draws a polygon of the control points, which differs from a curve by far
		// more than an anti-aliasing convention.
		{"a curved glyph", "BT /F1 48 Tf 20 100 Td (B) Tj ET", 0},
		// The same curve at 160pt. TrueType's quadratic is raised to a cubic by placing each
		// control two thirds of the way from its endpoint toward the quadratic's single control,
		// and at 48pt a weight of one third instead leaves the mean under 1.5 with no pixel over
		// 32 — the deviation is a fraction of a pixel on a glyph that small. Size prices it.
		{"a large curved glyph", "BT /F1 160 Tf 20 40 Td (B) Tj ET", 0},
		{"a composite glyph", "BT /F1 48 Tf 20 100 Td (C) Tj ET", 260},
		// The ring's two contours are wound the same way, so nonzero fills its middle and
		// even-odd hollows it. pdfium fills it, because TrueType's rule is nonzero.
		{"a glyph with nested contours", "BT /F1 48 Tf 20 100 Td (D) Tj ET", 0},
		{"mixed glyphs at one size", "BT /F1 24 Tf 20 100 Td (ABCABC) Tj ET", 160},
		// TJ's adjustments move the pen backwards for a positive value, so a sign error here
		// reverses every kern and the glyphs pile up.
		{"TJ with adjustments", "BT /F1 36 Tf 20 100 Td [(A) -200 (B) 300 (C)] TJ ET", 150},
		// Tz scales horizontally, Tc and Tw add to the advance, and Ts raises the baseline:
		// each multiplies into the advance or the render matrix at a different point.
		{"horizontal scaling", "BT /F1 36 Tf 150 Tz 20 100 Td (AB) Tj ET", 70},
		{"character spacing", "BT /F1 36 Tf 8 Tc 20 100 Td (AB) Tj ET", 0},
		{"word spacing", "BT /F1 36 Tf 20 Tw 20 100 Td (A A) Tj ET", 0},
		{"a raised baseline", "BT /F1 36 Tf 10 Ts 20 100 Td (A) Tj ET", 0},
		// A text matrix that rotates and scales, which is the one thing that can tell the
		// render matrix's composition order from its reverse.
		{"a rotated text matrix", "BT /F1 1 Tf 30 10 -10 30 40 60 Tm (AB) Tj ET", 0},
		{"two lines with Td", "BT /F1 24 Tf 20 150 Td (AB) Tj 0 -40 Td (BC) Tj ET", 80},
		{"a line break with T*", "BT /F1 24 Tf 40 TL 20 150 Td (AB) Tj T* (BC) Tj ET", 80},
		{"a quote shows on the next line", "BT /F1 24 Tf 40 TL 20 150 Td (AB) Tj (BC) ' ET", 80},
		// The double quote carries its own word and character spacing, and both persist after
		// the string (§9.4.3). Nothing else in this table sets either through it — the operator
		// appeared only with two zeroes — so dropping both operands changed no pixel anywhere.
		{"a double quote sets spacing and shows", `BT /F1 24 Tf 40 TL 20 150 Td (A) Tj 20 8 (A A) " ET`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := textPDF(t, c.stream, 200)

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

			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()
			got, err := New(s).Page(1, o)
			if err != nil {
				t.Fatalf("native Page: %v", err)
			}

			a, aw, ah := ink(want.Image)
			b, bw, bh := ink(got.Image)
			if aw != bw || ah != bh {
				t.Fatalf("size: pdfium %dx%d, native %dx%d", aw, ah, bw, bh)
			}
			var sum, inkA, inkB float64
			over := 0
			for i := range a {
				d := math.Abs(float64(a[i]) - float64(b[i]))
				sum += d
				if d > 32 {
					over++
				}
				inkA += float64(a[i])
				inkB += float64(b[i])
			}
			mean := sum / float64(len(a))
			ratio := 0.0
			if inkA > 0 {
				ratio = inkB / inkA
			}
			t.Logf("mean %.3f over-32 %d ink %.3f", mean, over, ratio)

			// Ink first, because it is the assertion that cannot pass vacuously: a rasterizer
			// that drew nothing would have a mean of whatever pdfium drew, and a test that only
			// bounded the mean would call a blank page a small disagreement.
			if inkA == 0 {
				t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
			}
			if ratio < 0.9 || ratio > 1.1 {
				t.Errorf("ink ratio %.3f, want within 10%% — the glyphs are the wrong size, place or shape", ratio)
			}
			// Glyph edges are almost all curve or diagonal, so the per-pixel bound is looser
			// than the one for large paths: a 36pt glyph has a few hundred edge pixels and two
			// implementations will disagree on some of them.
			if mean > 1.5 {
				t.Errorf("mean ink difference %.3f, want <= 1.5", mean)
			}
			// And the count of far-apart pixels per case, which is the assertion that does the
			// work here. It was declared in the table above and never checked, and a mean bound
			// alone cannot see a glyph one pixel out of place: reversing TJ's adjustment sign
			// moves the mean to 3.8 and this count to 671, so a single loose mean passed a
			// mutation that reverses every kern on the page.
			//
			// The allowances are measured, not chosen, and they are not this package's error.
			// pdfium grid-fits a glyph's origin to the pixel and this backend does not: the same
			// glyph at an integer pen position agrees to a mean of 0.003 with no pixel over 32,
			// and at a half-pixel position to 0.064 with 27 — so every non-zero allowance below
			// belongs to a case whose pen lands between pixels, and the cases at zero are the
			// ones where it does not. Grid-fitting is a hinting decision, and ADR 0015 already
			// declines to hint.
			if over > c.over {
				t.Errorf("%d pixels differ by more than 32, want at most %d", over, c.over)
			}
		})
	}
}

// TestCodeToGlyphTakesEachRoute pins each way a PDF says which glyph it means.
//
// §9.6.5.4 and §9.7.4.2 give three routes and the fixture font reached one: a composite font maps
// its CID through /CIDToGIDMap, and a simple font with no usable cmap at all falls back to the
// code as a glyph index — which is not a guess but what a producer means by a subset with no
// character map to read it with. Neither had an input, so either could have been deleted or
// reordered with every test still passing.
//
// Each case is compared against the *same glyph drawn by the route that already worked*, at the
// same size and pen position, so the assertion is the whole image rather than a sample: a route
// that resolved to a different glyph, or to nothing, differs everywhere the glyph is. That is
// also why the composite case maps its CID to the diamond — the identity map and the
// code-as-index fallback would both answer glyph 1, so only a map that disagrees with both can
// tell which one ran.
func TestCodeToGlyphTakesEachRoute(t *testing.T) {
	draw := func(t *testing.T, stream string, opts ...func(*textPDFOpts)) []byte {
		t.Helper()
		s, err := pcstore.Open(textPDF(t, stream, 200, opts...))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = s.Close() }()
		o := render.DefaultOptions
		o.DPI = 72
		got, err := New(s).Page(1, o)
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		px, _, _ := ink(got.Image)
		return px
	}

	for _, c := range []struct {
		name   string
		stream string
		opts   []func(*textPDFOpts)
		want   string // the same glyph, reached through the font's own cmap
	}{
		{
			name:   "a code as the glyph index, in a font with no cmap",
			stream: `BT /F1 48 Tf 20 100 Td (\001) Tj ET`,
			opts:   []func(*textPDFOpts){withProgram(ttfbuild.Builder{UnitsPerEm: 1000, NoCmap: true}.Build())},
			want:   "BT /F1 48 Tf 20 100 Td (A) Tj ET",
		},
		{
			name:   "a CID through a CIDToGIDMap stream",
			stream: `BT /F1 48 Tf 20 100 Td (\000\001) Tj ET`,
			// A symbolic (3,0) subtable that maps code 1 to the square, so the route that reads
			// it and the route that reads /CIDToGIDMap give different glyphs. Without it both
			// answered glyph 1 and the order between them could be reversed unnoticed — a CID is
			// not a character code, and reading one as the other is how a composite font comes
			// out set in entirely the wrong glyphs.
			opts: []func(*textPDFOpts){withComposite(),
				withProgram(ttfbuild.Builder{UnitsPerEm: 1000, SymbolicCmap: true}.Build())},
			want: "BT /F1 48 Tf 20 100 Td (B) Tj ET",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := draw(t, c.stream, c.opts...)
			want := draw(t, c.want)
			var total, diff float64
			for i := range want {
				total += float64(want[i])
				if got[i] != want[i] {
					diff++
				}
			}
			if total == 0 {
				t.Fatal("the reference render drew nothing, so this comparison asserts nothing")
			}
			if diff > 0 {
				t.Errorf("%.0f pixels differ from the same glyph drawn through the cmap —"+
					" this route resolved to a different glyph or to none", diff)
			}
		})
	}
}

// TestGlyphsFillNonzeroNotEvenOdd samples the one pixel that tells the two rules apart.
//
// §9.3.6's render modes are about fill against stroke against clip and never about the winding
// rule: a glyph fills nonzero, always. The ring glyph is two squares wound the same direction, so
// the winding number in the middle is two — filled under nonzero, hollow under even-odd — and it
// is the only glyph in the fixture font where the rules disagree. Every other one is a single
// convex contour, where flipping the rule changes nothing at all.
//
// Sampled directly and not only compared against pdfium, for the reason ADR 0015 gives about the
// star: an oracle that had the same rule flipped would still agree.
func TestGlyphsFillNonzeroNotEvenOdd(t *testing.T) {
	s, err := pcstore.Open(textPDF(t, "BT /F1 48 Tf 20 100 Td (D) Tj ET", 200))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	got, err := New(s).Page(1, o)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	px, w, h := ink(got.Image)
	at := func(x, y int) byte { return px[(h-1-y)*w+x] }
	// The inner square runs from 0.2 to 0.3 em, so at 48pt from a pen at (20,100) its middle is
	// at (32,112). The outer square's own body, between 0.1 and 0.2 em, is at (26,106).
	if v := at(32, 112); v < 200 {
		t.Errorf("ink %d in the middle of the ring, want it filled: two contours wound the same"+
			" way have a winding number of two, which is nonzero and not even", v)
	}
	if v := at(26, 106); v < 200 {
		t.Errorf("ink %d in the ring's body, want it filled", v)
	}
}

// TestWordSpacingSkipsMultiByteCodes pins §9.3.3's one-byte condition.
//
// Word spacing is added to the advance of "a single-byte code 32" and to nothing else — not to a
// two-byte code whose value happens to be 32, which in a composite font is an ordinary CID. The
// rule reads like a detail and is not: CJK text is two-byte throughout and runs over CID 32
// routinely, so applying word spacing there spreads a line apart at arbitrary characters whenever
// the page happens to set Tw.
//
// The fixture is a composite font showing CID 1, CID 32 and CID 1, with a Tw of 20 that must have
// no effect at all. Against pdfium, because the assertion is about where the *third* glyph lands
// and 20 points of it is the whole difference.
func TestWordSpacingSkipsMultiByteCodes(t *testing.T) {
	path := textPDF(t, `BT /F1 36 Tf 20 Tw 20 100 Td (\000\001\000\040\000\001) Tj ET`, 200,
		withComposite())

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
	got, err := New(s).Page(1, o)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	a, _, _ := ink(want.Image)
	b, _, _ := ink(got.Image)
	var inkA, inkB float64
	over := 0
	for i := range a {
		if math.Abs(float64(a[i])-float64(b[i])) > 32 {
			over++
		}
		inkA += float64(a[i])
		inkB += float64(b[i])
	}
	if inkA == 0 {
		t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
	}
	if ratio := inkB / inkA; ratio < 0.9 || ratio > 1.1 {
		t.Errorf("ink ratio %.3f, want within 10%%", ratio)
	}
	if over > 80 {
		t.Errorf("%d pixels differ by more than 32, want at most 80 — word spacing was applied"+
			" to a two-byte code", over)
	}
}

// TestFontsWithoutGlyfAreRefusedByReason pins that a page is refused for the *reason* its font
// cannot be drawn, not for using a text operator.
//
// The distinction is the whole value of the error: over a corpus, "this page draws text" is the
// same message 1,241 times, where "the font program is FontFile3 (CFF)" against "no descriptor" is
// a census of what to implement next. Each case here is a real shape a producer writes.
//
// Crossed with all four show operators, because the refusal is decided in the survey and the
// survey dispatches on the operator: a show operator missing from that switch does not produce a
// wrong error, it produces a page that comes back *drawn* with the text silently absent. Only Tj
// had an input, and TJ is the operator the corpus uses most — on 1,241 of 1,251 pages.
func TestFontsWithoutGlyfAreRefusedByReason(t *testing.T) {
	for _, c := range []struct {
		name string
		opt  func(*textPDFOpts)
		want string
	}{
		{"a CFF program", withFontFileKey("FontFile3"), "CFF"},
		{"a Type 1 program", withFontFileKey("FontFile"), "Type 1"},
		{"no embedded program", withNoFontFile(), "no face source was supplied"},
	} {
		for _, show := range []struct{ op, operands string }{
			{"Tj", "(A)"},
			{"TJ", "[(A)]"},
			{"'", "(A)"},
			{`"`, "0 0 (A)"},
		} {
			t.Run(c.name+", shown with "+show.op, func(t *testing.T) {
				stream := "BT /F1 24 Tf 40 TL 20 100 Td " + show.operands + " " + show.op + " ET"
				path := textPDF(t, stream, 200, c.opt)
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
					t.Fatalf("err = %v, want an *Unsupported — a page drawn without its text"+
						" is the failure this backend exists to prevent", err)
				}
				joined := strings.Join(u.Ops, " | ")
				if !strings.Contains(joined, c.want) {
					t.Errorf("refusal = %q, want it to mention %q", joined, c.want)
				}
				if !strings.Contains(joined, "text:") {
					t.Errorf("refusal = %q, want it keyed as a text reason", joined)
				}
			})
		}
	}
}

// TestInvisibleTextNeedsNoGlyph pins that render mode 3 does not require what it will not draw.
//
// The survey decides refusals before anything is painted, and it has to ask for a glyph only
// where one would be drawn — so it runs the graphics state machine for §9.3.6's render mode. A
// survey that skipped that check would refuse every scanned page whose invisible OCR layer names
// a character its font cannot map, which is a page that renders perfectly: the OCR text is there
// to be searched, not seen. 'Z' is outside this font's cmap, so it is exactly that character.
func TestInvisibleTextNeedsNoGlyph(t *testing.T) {
	path := textPDF(t, "BT /F1 36 Tf 3 Tr 20 100 Td (Z) Tj 0 Tr (A) Tj ET", 200)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	got, err := New(s).Page(1, o)
	if err != nil {
		t.Fatalf("Page: %v — an unmappable code in an invisible run refused the page", err)
	}
	px, _, _ := ink(got.Image)
	var total float64
	for _, v := range px {
		total += float64(v)
	}
	// The visible 'A' is a 0.3 em square at 36pt, about 117 fully covered pixels.
	if want := 60.0 * 255; total < want {
		t.Errorf("ink %.0f, want at least %.0f — the visible glyph was not drawn", total, want)
	}
}

// TestAGlyphWithNoMappingIsRefused pins that a code the font cannot resolve stops the page.
//
// A code with no glyph is a character the page draws and this backend cannot, which is the same
// class as an operator it cannot draw and gets the same answer. Dropping it would put a page on
// screen with one character quietly missing — the failure mode this whole backend refuses pages to
// avoid, at the granularity where it is hardest to notice.
func TestAGlyphWithNoMappingIsRefused(t *testing.T) {
	// 'Z' is outside the font's cmap segment and past its glyph count.
	path := textPDF(t, "BT /F1 24 Tf 20 100 Td (Z) Tj ET", 200)
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
		t.Fatalf("err = %v, want an *Unsupported naming the code", err)
	}
	if joined := strings.Join(u.Ops, " | "); !strings.Contains(joined, "no glyph for code 90") {
		t.Errorf("refusal = %q, want it to name the code with no glyph", joined)
	}
}

// TestInvisibleTextDrawsNothingAndStillAdvances pins render mode 3, which is how a scanned page's
// OCR layer is written: the text is there to be searched and must not be drawn.
//
// Compared against pdfium, because the assertion is that the page is *blank* — and a blank page is
// also what a rasterizer that could not draw the font produces, so the comparison has to include
// a visible glyph to prove the difference.
func TestInvisibleTextDrawsNothingAndStillAdvances(t *testing.T) {
	path := textPDF(t, "BT /F1 36 Tf 3 Tr 20 100 Td (AAA) Tj 0 Tr (B) Tj ET", 200)

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

	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	got, err := New(s).Page(1, o)
	if err != nil {
		t.Fatalf("native Page: %v", err)
	}

	a, w, _ := ink(want.Image)
	b, _, _ := ink(got.Image)
	var inkA, inkB, sum float64
	for i := range a {
		inkA += float64(a[i])
		inkB += float64(b[i])
		sum += math.Abs(float64(a[i]) - float64(b[i]))
	}
	t.Logf("ink pdfium %.0f native %.0f mean %.3f", inkA, inkB, sum/float64(len(a)))
	if inkA == 0 {
		t.Fatal("pdfium drew nothing: the visible glyph should be there")
	}
	if ratio := inkB / inkA; ratio < 0.85 || ratio > 1.15 {
		t.Errorf("ink ratio %.3f — the invisible run was drawn, or the visible one was not", ratio)
	}
	// The visible glyph sits after three invisible ones, so where the page's *leftmost* ink is
	// proves both halves at once: at 36pt the square is 0.5 em wide, so three of them advance
	// 54pt from x=20, and nothing may be drawn left of x=74. An advance that did not happen
	// draws the visible glyph back at x=20, and an invisible glyph that was drawn anyway puts
	// ink at 20 as well.
	//
	// Stated as the leftmost column rather than as a probe at one expected column, which is
	// what it was: the probe sat four points into the visible glyph's bounding box, and the
	// visible glyph is a curve whose leftmost point is a quarter of the way in from its
	// control — so the probe missed by geometry while the page was right, and the fix is an
	// assertion that does not need to know the glyph's shape.
	pen := 20 + 3*0.5*36
	first := w
	for y := 0; y < len(b)/w; y++ {
		for x := 0; x < first; x++ {
			if b[y*w+x] > 128 {
				first = x
				break
			}
		}
	}
	if first >= w {
		t.Fatal("native drew nothing at all")
	}
	if float64(first) < pen {
		t.Errorf("leftmost ink at x=%d, want at or past the pen at x=%.0f: either an invisible"+
			" glyph was drawn or the pen did not advance", first, pen)
	}
}

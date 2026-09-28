package native

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/t1build"
)

// type1Font embeds b as FontFile under a simple /Type1 font — every Type 1 program in the corpus's
// shape. The rest of textPDF's defaults stand: nonsymbolic, over /WinAnsiEncoding.
func type1Font(b t1build.Builder, opts ...func(*textPDFOpts)) []func(*textPDFOpts) {
	prog, length1 := b.Build()
	o := []func(*textPDFOpts){withProgram(prog), withFontFileKey("FontFile"), withSubtype("Type1"),
		withProgramDict(fmt.Sprintf("/Length1 %d", length1))}
	return append(o, opts...)
}

// t1Glyph is a name-keyed program whose one glyph is cs.
func t1Glyph(name string, cs []byte) t1build.Builder {
	return t1build.Builder{Glyphs: []t1build.Glyph{{Name: name, CS: cs}}}
}

// t1SquareCS is this package's Square shape, (100,100)-(600,600) advance 700, under whatever name
// the caller gives it — the same outline t1build.Square() draws for "A".
func t1SquareCS() []byte {
	cs, op := t1build.CS, t1build.RMoveTo
	return cs(100, 700, t1build.HSBW, 100, 100, op, 500, t1build.HLineTo,
		500, t1build.VLineTo, -500, t1build.HLineTo, t1build.ClosePath, t1build.EndChar)
}

// t1DiamondCS is a second shape, unlike the square in both silhouette and position, so a route
// that lands one glyph over draws something a pixel comparison can tell from the square: a diamond
// of corners (350,600), (600,350), (350,100), (100,350).
func t1DiamondCS() []byte {
	cs, op := t1build.CS, t1build.RMoveTo
	return cs(0, 700, t1build.HSBW, 350, 600, op,
		250, -250, t1build.RLineTo, -250, -250, t1build.RLineTo, -250, 250, t1build.RLineTo,
		t1build.ClosePath, t1build.EndChar)
}

// t1Letters is a name-keyed font over StandardEncoding: A the square, B the diamond.
func t1Letters() t1build.Builder {
	return t1build.Builder{Glyphs: []t1build.Glyph{
		{Name: "A", CS: t1SquareCS()},
		{Name: "B", CS: t1DiamondCS()},
	}}
}

// t1Swapped is t1Letters with A and B swapped in the built-in encoding, so a route by code draws
// the diamond for code 65 where a route by name draws the square.
func t1Swapped() t1build.Builder {
	b := t1Letters()
	b.Encoding = map[byte]string{'A': "B", 'B': "A"}
	return b
}

// assertType1AgreesWithPdfium runs TestCFFAgreesWithPdfium's tolerance scheme, unchanged: an ink
// ratio within 10%, a mean ink difference of at most 1.5, and a per-case count of pixels more than
// 32 apart — pdfium's LCD-filtered small sizes and its curve anti-aliasing, the same two
// conventions cff_test.go's comment above TestCFFAgreesWithPdfium explains.
func assertType1AgreesWithPdfium(t *testing.T, native, ref []byte, over int) {
	t.Helper()
	if len(native) != len(ref) {
		t.Fatalf("size: pdfium %d pixels, native %d", len(ref), len(native))
	}
	var sum, inkRef, inkNative float64
	got := 0
	for i := range ref {
		d := math.Abs(float64(ref[i]) - float64(native[i]))
		sum += d
		if d > 32 {
			got++
		}
		inkRef += float64(ref[i])
		inkNative += float64(native[i])
	}
	mean := sum / float64(len(ref))
	t.Logf("mean %.3f over-32 %d ink %.3f", mean, got, inkNative/math.Max(inkRef, 1))
	if inkRef == 0 {
		t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
	}
	if r := inkNative / inkRef; r < 0.9 || r > 1.1 {
		t.Errorf("ink ratio %.3f, want within 10%% — the glyphs are the wrong size, place or shape", r)
	}
	if mean > 1.5 {
		t.Errorf("mean ink difference %.3f, want <= 1.5", mean)
	}
	if got > over {
		t.Errorf("%d pixels differ by more than 32, want at most %d", got, over)
	}
}

// TestType1AgreesWithPdfium is TestCFFAgreesWithPdfium's counterpart for a Type 1 program: glyphs
// drawn from a hand-built t1build font, against pdfium, one case per thing the charstring
// interpreter and the code-to-glyph routing read.
func TestType1AgreesWithPdfium(t *testing.T) {
	const one = "BT /F1 48 Tf 20 100 Td (A) Tj ET"
	cs, op := t1build.CS, t1build.RMoveTo

	// A curve glyph, closed by a diagonal line back to its start rather than by more path
	// operators, so all three curve forms are exercised once each: rrcurveto, then vhcurveto
	// (vertical tangent in, horizontal out), then hvcurveto (horizontal in, vertical out).
	curveCS := cs(0, 700, t1build.HSBW, 100, 100, op,
		0, 200, 200, 200, 200, 0, t1build.RRCurveTo,
		100, 100, 100, 50, t1build.VHCurveTo,
		100, 100, 100, 50, t1build.HVCurveTo,
		-750, -750, t1build.RLineTo, t1build.ClosePath, t1build.EndChar)

	// closepath, then a second, oppositely-wound contour: an outer square (100,100)-(700,700)
	// with a (300,300)-(500,500) hole, wound the other way, so a nonzero fill leaves it open.
	ringCS := cs(0, 900, t1build.HSBW, 100, 100, op,
		600, t1build.HLineTo, 600, t1build.VLineTo, -600, t1build.HLineTo, t1build.ClosePath,
		200, -400, op, 200, t1build.VLineTo, 200, t1build.HLineTo, -200, t1build.VLineTo,
		t1build.ClosePath, t1build.EndChar)

	// A flex, written the way a producer writes one: subr 1 starts it, subr 2 is called after
	// each of the seven points (a reference point the drawing ignores, then the two curves' six
	// points), and subr 0 ends it, checking that the x, y it is passed — 750, 300 — is the pen's
	// absolute position, sbx included, once all seven moves have run.
	flexSubrs := [][]byte{
		cs(3, 0, t1build.CallOtherSubr, t1build.Pop, t1build.Pop, t1build.SetCurrentPoint, t1build.Return),
		cs(0, 1, t1build.CallOtherSubr, t1build.Return),
		cs(0, 2, t1build.CallOtherSubr, t1build.Return),
	}
	flexCS := cs(50, 700, t1build.HSBW, 100, 300, op,
		1, t1build.CallSubr,
		0, 0, op, 2, t1build.CallSubr,
		100, 150, op, 2, t1build.CallSubr,
		100, 50, op, 2, t1build.CallSubr,
		100, 0, op, 2, t1build.CallSubr,
		100, 0, op, 2, t1build.CallSubr,
		100, -50, op, 2, t1build.CallSubr,
		100, -150, op, 2, t1build.CallSubr,
		50, 750, 300, 0, t1build.CallSubr,
		0, -200, t1build.RLineTo, t1build.ClosePath, t1build.EndChar)

	// Hint replacement: "subr# 1 3 callothersubr pop callsubr", where the subroutine callsubr
	// ends up calling is the one hint replacement names — subr 0, defined here to do nothing but
	// return, since what is under test is that the mechanism reaches it at all.
	hintSubrs := [][]byte{cs(t1build.Return)}
	hintCS := cs(0, 700, t1build.HSBW,
		0, 1, 3, t1build.CallOtherSubr, t1build.Pop, t1build.CallSubr,
		100, 100, op, 500, t1build.HLineTo, 500, t1build.VLineTo, -500, t1build.HLineTo,
		t1build.ClosePath, t1build.EndChar)

	// sbw, both side bearings and both widths, in place of hsbw.
	sbwCS := cs(50, 20, 700, 0, t1build.SBW, 100, 100, op,
		400, t1build.HLineTo, 400, t1build.VLineTo, -400, t1build.HLineTo, t1build.ClosePath, t1build.EndChar)

	// div computing a coordinate: 600 3 div is 200, the dx of the rlineto that follows it.
	divCS := cs(0, 700, t1build.HSBW, 100, 100, op,
		600, 3, t1build.Div, 300, t1build.RLineTo,
		-200, t1build.HLineTo, -300, t1build.VLineTo, t1build.ClosePath, t1build.EndChar)

	// seac: Aacute is A with acute over it, found by their StandardEncoding codes, 65 and 194.
	seacFont := t1build.Builder{Glyphs: []t1build.Glyph{
		{Name: "A", CS: cs(0, 700, t1build.HSBW, 100, 100, op,
			400, t1build.HLineTo, 400, t1build.VLineTo, -400, t1build.HLineTo, t1build.ClosePath, t1build.EndChar)},
		{Name: "acute", CS: cs(0, 300, t1build.HSBW, 50, 50, op,
			100, 0, t1build.RLineTo, -50, 150, t1build.RLineTo, t1build.ClosePath, t1build.EndChar)},
		{Name: "Aacute", CS: cs(0, 700, t1build.HSBW, 0, 300, 500, 65, 194, t1build.Seac)},
	}}

	for _, c := range []struct {
		name   string
		opts   []func(*textPDFOpts)
		stream string
		over   int
	}{
		{"a square", type1Font(t1Glyph("A", t1SquareCS())), one, 0},
		{"a curved glyph (rrcurveto, vhcurveto, hvcurveto)",
			type1Font(t1Glyph("A", curveCS)), "BT /F1 40 Tf 20 20 Td (A) Tj ET", 0},
		// Measured (t.Logf'd during development, then removed): all 10 pixels over 32 apart are
		// one column, x=43, y=156-165 — the hole's right edge, which is vertical. pdfium
		// anti-aliases that edge to level 38 there; this backend leaves the column fully
		// unpainted. The same one-vertical-edge, ~38-level leak TestCFFAgreesWithPdfium's comment
		// attributes to pdfium's LCD-filtered rendering below 50 pixels per em — this glyph is
		// drawn at 48.
		{"closepath then a second contour, a ring with a hole",
			type1Font(t1Glyph("A", ringCS)), "BT /F1 48 Tf 20 20 Td (A) Tj ET", 10},
		// The 8 pixels over 32 apart are the flex curve's own anti-aliasing, measured the same way.
		{"a flex through othersubrs 0, 1 and 2 in the standard Subrs",
			type1Font(t1build.Builder{Glyphs: []t1build.Glyph{{Name: "A", CS: flexCS}}, Subrs: flexSubrs}),
			"BT /F1 40 Tf 20 20 Td (A) Tj ET", 8},
		{"hint replacement", type1Font(t1build.Builder{
			Glyphs: []t1build.Glyph{{Name: "A", CS: hintCS}}, Subrs: hintSubrs}), one, 0},
		// The 19 pixels over 32 apart are the accent's diagonal edges' own anti-aliasing — the same
		// convention as TestCFFAgreesWithPdfium's "seac with a width", whose diamond accent
		// differs at 14 for the same reason.
		{"seac", type1Font(seacFont,
			withEncoding("/Encoding<</BaseEncoding/WinAnsiEncoding/Differences[66/Aacute]>>")),
			"BT /F1 48 Tf 20 20 Td (B) Tj ET", 19},
		{"sbw", type1Font(t1Glyph("A", sbwCS)), one, 0},
		{"div in a coordinate", type1Font(t1Glyph("A", divCS)), one, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			native, ref := drawBoth(t, c.stream, c.opts...)
			assertType1AgreesWithPdfium(t, native, ref, c.over)
		})
	}
}

// TestType1CodeToGlyphTakesEachRoute is TestCFFCodeToGlyphTakesEachRoute's counterpart for a
// Type 1 program: type1GID is the same function for both, so the fixtures are the same shape —
// a font where the route under test and some other route reach different glyphs, so a font whose
// routes agree cannot be mistaken for the route having been read at all. Each is compared, exactly,
// against the glyph the route should reach drawn through t1Letters, in this backend and separately
// in pdfium.
func TestType1CodeToGlyphTakesEachRoute(t *testing.T) {
	symbolic := []func(*textPDFOpts){withFlags(4), withEncoding("")}
	noBase := []func(*textPDFOpts){withEncoding("")}

	for _, c := range []struct {
		name   string
		b      t1build.Builder
		opts   []func(*textPDFOpts)
		stream string
		want   string // the glyph the route should reach, through t1Letters
	}{
		{"a WinAnsi name, not the built-in encoding", t1Swapped(), nil, "(A)", "(A)"},
		{"a /Differences name over WinAnsi", t1Letters(), []func(*textPDFOpts){
			withEncoding("/Encoding<</BaseEncoding/WinAnsiEncoding/Differences[65/B]>>")},
			"(A)", "(B)"},
		{"a /Differences name with no base", t1Swapped(), []func(*textPDFOpts){
			withEncoding("/Encoding<</Differences[65/A]>>")}, "(A)", "(A)"},
		{"a symbolic font's /Differences name, not its built-in encoding", t1Swapped(),
			[]func(*textPDFOpts){withFlags(4), withEncoding("/Encoding<</Differences[65/A]>>")},
			"(A)", "(A)"},
		{"a symbolic font's code through the built-in encoding", t1Swapped(), symbolic, "(A)", "(B)"},
		{"a symbolic font with StandardEncoding as its built-in", t1Letters(), symbolic, "(A)", "(A)"},
		{"a nonsymbolic font with no base, where both readings agree",
			t1build.Builder{Glyphs: []t1build.Glyph{{Name: "A", CS: t1SquareCS()},
				{Name: "B", CS: t1DiamondCS()}}, Encoding: map[byte]string{'A': "A"}},
			noBase, "(A)", "(A)"},
		{"an MMType1 font", t1Swapped(), []func(*textPDFOpts){withSubtype("MMType1")}, "(A)", "(A)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, gotRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td "+c.stream+" Tj ET",
				type1Font(c.b, c.opts...)...)
			want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td "+c.want+" Tj ET",
				type1Font(t1Letters())...)
			assertSameInk(t, got, gotRef, want, wantRef)
		})
	}
}

// TestType1Length1 pins the two /Length1 shapes TestType1FontsOutsideTheAgreedRoutesAreRefused
// does not refuse: absent, where only FreeType's own reading of eexec is taken, and equal to the
// program's actual clear-text length, which is what every corpus font that has one carries.
func TestType1Length1(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []func(*textPDFOpts)
	}{
		{"absent", type1Font(t1Glyph("A", t1SquareCS()), withProgramDict(""))},
		{"equal", type1Font(t1Glyph("A", t1SquareCS()))},
	} {
		t.Run(c.name, func(t *testing.T) {
			native, ref := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET", c.opts...)
			assertType1AgreesWithPdfium(t, native, ref, 0)
		})
	}
}

// TestType1FontsOutsideTheAgreedRoutesAreRefused pins every Type 1 font and code this backend
// refuses, by the reason it gives — parseType1's own checks, and type1Route's, shared with CFF.
func TestType1FontsOutsideTheAgreedRoutesAreRefused(t *testing.T) {
	square := t1Glyph("A", t1SquareCS())
	_, length1 := square.Build()

	for _, c := range []struct {
		name   string
		opts   []func(*textPDFOpts)
		stream string
		want   string
	}{
		{"a FontFile program under a /TrueType font", type1Font(square, withSubtype("TrueType")),
			"(A)", "a FontFile program under a /TrueType font"},
		{"a FontFile program under a composite font", type1Font(square, withComposite()),
			"(A)", "a FontFile program under a composite font"},
		{"a Type 1 font over /MacRomanEncoding",
			type1Font(square, withEncoding("/Encoding/MacRomanEncoding")), "(A)",
			"a Type 1 font over /MacRomanEncoding"},
		{"a font named Symbol", type1Font(square, withBaseFont("Symbol")), "(A)",
			"a Type 1 font named"},
		{"a font named ZapfDingbats", type1Font(square, withBaseFont("ZapfDingbats")), "(A)",
			"a Type 1 font named"},
		{"the FontFile program did not parse",
			type1Font(square, withProgram([]byte{1, 0, 4, 4, 0})), "(A)",
			"the FontFile program did not parse"},
		{"the FontFile stream did not decode",
			type1Font(square, withProgramDict("/Filter/FlateDecode"), withProgram([]byte("this is not flate"))),
			"(A)", "the FontFile stream did not decode"},
		{"a /Length1 that differs from the clear-text length",
			type1Font(square, withProgramDict(fmt.Sprintf("/Length1 %d", length1+1))), "(A)",
			"/Length1"},
		{"a code that reaches no glyph at all", type1Font(square), "(Z)",
			"the program maps it to none"},
		{"a code that reaches glyph 0, by name", type1Font(square,
			withEncoding("/Encoding<</BaseEncoding/WinAnsiEncoding/Differences[65/.notdef]>>")),
			"(A)", "the program maps it to none"},
		{"a nonsymbolic font with no base, where the readings part",
			type1Font(t1Swapped(), withEncoding("")), "(A)",
			"its StandardEncoding name selects glyph"},
		{"a code WinAnsi leaves unnamed", type1Font(square), `(\201)`,
			"/WinAnsiEncoding names no glyph for it"},
		{"a charstring the interpreter refuses",
			type1Font(t1Glyph("A", t1build.CS(0, 700, t1build.HSBW, t1build.Pop, t1build.EndChar))),
			"(A)", "without the othersubr whose result it takes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := renderErr(t, textPDF(t, "BT /F1 24 Tf 20 100 Td "+c.stream+" Tj ET", 200, c.opts...))
			var u *Unsupported
			if !errors.As(err, &u) {
				t.Fatalf("err = %v, want an *Unsupported naming the shape", err)
			}
			if joined := strings.Join(u.Ops, " | "); !strings.Contains(joined, c.want) {
				t.Errorf("refusal = %q, want it to mention %q", joined, c.want)
			}
		})
	}
}

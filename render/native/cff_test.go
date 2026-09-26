package native

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/font"
	"github.com/model-harness/pdftools/internal/cffbuild"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
)

// Standard string IDs (TN #5176 Appendix A) of the glyph names the fixtures below use.
const (
	sidSpace = 1
	sidA     = 34
	sidB     = 35
	sidC     = 36
	sidD     = 37
)

// cffTriangle is the third shape, beside cffbuild's square and diamond, so a route that lands one
// glyph over draws something else again.
var cffTriangle = cffbuild.CS(100, 100, cffbuild.RMoveTo, 600, 0, -300, 500, cffbuild.RLineTo,
	cffbuild.EndChar)

// cffLetters is a name-keyed font over the predefined Standard encoding: the space, A the square,
// B the diamond and C the triangle.
func cffLetters() cffbuild.Builder {
	return cffbuild.Builder{Glyphs: []cffbuild.Glyph{
		{SID: sidSpace, CharString: cffbuild.CS(cffbuild.EndChar)},
		{SID: sidA, CharString: cffbuild.Square()},
		{SID: sidB, CharString: cffbuild.Diamond()},
		{SID: sidC, CharString: cffTriangle},
	}}
}

// cffGlyph is a name-keyed font whose one glyph, A, is cs.
func cffGlyph(cs []byte) cffbuild.Builder {
	return cffbuild.Builder{Glyphs: []cffbuild.Glyph{{SID: sidA, CharString: cs}}}
}

// cffFont embeds b as FontFile3: a Type1C program under a simple Type 1 font, or a CIDFontType0C
// one under a CIDFontType0 descendant with no /CIDToGIDMap, which is every CFF font in the corpus.
// The rest of textPDF's defaults stand: nonsymbolic, over /WinAnsiEncoding.
func cffFont(b cffbuild.Builder, opts ...func(*textPDFOpts)) []func(*textPDFOpts) {
	o := []func(*textPDFOpts){withProgram(b.Build()), withFontFileKey("FontFile3")}
	if b.CID {
		o = append(o, withProgramDict("/Subtype/CIDFontType0C"), withComposite(),
			withDescendant("CIDFontType0"), withNoCIDToGIDMap())
	} else {
		o = append(o, withProgramDict("/Subtype/Type1C"), withSubtype("Type1"))
	}
	return append(o, opts...)
}

// TestCFFAgreesWithPdfium is ADR 0022's acceptance test: glyphs drawn from an embedded CFF program,
// against the engine this one is replacing, one case per thing the interpreter reads.
//
// The operators are where a Type 2 reader goes wrong quietly. Most take a variable count of
// operands whose meaning depends on the count — vvcurveto's optional first delta, the alternating
// tangents of vhcurveto, the flex shorthands' implied coordinates — so a misreading draws a glyph
// of plausible shape in the wrong place rather than failing. The unit tests in package font pin
// hand-derived coordinates; this pins that FreeType, which pdfium draws with, derives the same.
//
// Every glyph starts on a whole pixel, so pdfium's grid-fitting of the origin moves nothing. The
// allowances of pixels more than 32 apart are measured, and each is one of two pdfium conventions
// this backend declines. Below 50 pixels per em pdfium draws a glyph through FreeType's LCD mode —
// at three times the width, filtered across, then averaged back (cfx_face.cpp, and
// DrawNormalTextHelper in cfx_renderdevice.cpp) — so a vertical edge on a pixel boundary leaks
// about 38 levels into the column outside it: the square at 40pt differs at one pixel per edge per
// row, 24 of them. Above 50 pdfium fills the glyph as a path and places a curve's anti-aliasing as
// ADR 0015 describes: the diamond at 100pt differs at the same 14 pixels drawn alone or by seac. The
// cases at zero are exact to within anti-aliasing.
func TestCFFAgreesWithPdfium(t *testing.T) {
	const one = "BT /F1 100 Tf 20 40 Td (A) Tj ET"
	cs := cffbuild.CS
	type raw = cffbuild.Raw
	// A glyph that closes a contour with a line down and back, so the curve under test bounds an
	// area rather than a sliver.
	down := func(items ...any) []byte {
		return cs(append(items, 0, -200, -600, 0, cffbuild.RLineTo, cffbuild.EndChar)...)
	}

	for _, c := range []struct {
		name   string
		b      cffbuild.Builder
		opts   []func(*textPDFOpts)
		stream string
		over   int
	}{
		{"one glyph", cffLetters(), nil, "BT /F1 48 Tf 20 100 Td (A) Tj ET", 0},
		{"a curved glyph", cffLetters(), nil, "BT /F1 48 Tf 20 100 Td (B) Tj ET", 0},
		{"a large curved glyph", cffLetters(), nil, "BT /F1 160 Tf 20 20 Td (B) Tj ET", 0},
		{"a line of glyphs", cffLetters(), nil, "BT /F1 40 Tf 20 100 Td (ABC ABC) Tj ET", 58},
		{"TJ with adjustments", cffLetters(), nil,
			"BT /F1 40 Tf 20 100 Td [(A) -200 (B) 300 (C)] TJ ET", 28},
		{"word spacing over the space glyph", cffLetters(), nil,
			"BT /F1 36 Tf 20 Tw 20 100 Td (A A) Tj ET", 0},
		{"a rotated text matrix", cffLetters(), nil, "BT /F1 1 Tf 30 10 -10 30 40 60 Tm (AB) Tj ET", 0},

		// Each path operator, drawn once.
		{"rlineto", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			600, 0, -200, 500, -300, -100, cffbuild.RLineTo, cffbuild.EndChar)), nil, one, 0},
		{"hlineto, alternating from horizontal", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			500, 300, -200, 200, -300, cffbuild.HLineTo, cffbuild.EndChar)), nil, one, 0},
		{"vlineto, alternating from vertical", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			400, 500, -200, -300, cffbuild.VLineTo, cffbuild.EndChar)), nil, one, 0},
		{"rcurveline", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			0, 300, 200, 200, 300, 0, 100, -600, cffbuild.RCurveLine, cffbuild.EndChar)), nil, one, 0},
		{"rlinecurve", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			500, 0, 0, 200, 0, 200, -200, 200, -300, 0, cffbuild.RLineCurve, cffbuild.EndChar)), nil, one, 0},
		{"vvcurveto with a first dx", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			200, 300, 300, 200, 100, cffbuild.VVCurveTo, 0, -600, cffbuild.RLineTo, cffbuild.EndChar)),
			nil, one, 2},
		{"vvcurveto, two curves", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			300, 200, 200, 100, 100, 200, -100, 200, cffbuild.VVCurveTo, 300, -800, cffbuild.RLineTo,
			cffbuild.EndChar)), nil, one, 1},
		{"hhcurveto with a first dy", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			200, 300, 200, 300, 200, cffbuild.HHCurveTo, 0, -500, cffbuild.RLineTo, cffbuild.EndChar)),
			nil, one, 0},
		{"hhcurveto, two curves", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			300, 200, 300, 100, 100, 100, -200, 200, cffbuild.HHCurveTo, 0, -100, cffbuild.RLineTo,
			cffbuild.EndChar)), nil, one, 1},
		{"vhcurveto, alternating, with a last delta", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			300, 200, 200, 300, 100, 200, -300, -400, 50, cffbuild.VHCurveTo, cffbuild.EndChar)),
			nil, one, 0},
		{"hvcurveto with a last delta", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			300, 200, 300, 400, 100, cffbuild.HVCurveTo, cffbuild.EndChar)), nil, one, 0},
		{"flex", cffGlyph(down(100, 300, cffbuild.RMoveTo,
			100, 150, 100, 50, 100, 0, 100, 0, 100, -50, 100, -150, 50, cffbuild.Flex)), nil, one, 0},
		{"hflex", cffGlyph(down(100, 300, cffbuild.RMoveTo,
			100, 100, 200, 100, 100, 100, 100, cffbuild.HFlex)), nil, one, 2},
		{"hflex1", cffGlyph(down(100, 300, cffbuild.RMoveTo,
			100, 50, 100, 150, 100, 100, 100, -100, 100, cffbuild.HFlex1)), nil, one, 0},
		// flex1's last point runs along whichever axis the first five moved further on, so each
		// branch needs its own glyph.
		{"flex1, wider than tall", cffGlyph(down(100, 300, cffbuild.RMoveTo,
			100, 100, 100, 100, 100, 0, 100, 0, 100, -100, 100, cffbuild.Flex1)), nil, one, 0},
		{"flex1, taller than wide", cffGlyph(cs(300, 100, cffbuild.RMoveTo,
			100, 100, 100, 100, 0, 100, 0, 100, -100, 100, 100, cffbuild.Flex1,
			-200, 0, 0, -600, cffbuild.RLineTo, cffbuild.EndChar)), nil, one, 0},

		// The moveto forms, and the width a first stack-clearing operator may carry: read as a
		// coordinate, it moves the whole glyph.
		{"a width before rmoveto", cffGlyph(cs(250, 100, 100, cffbuild.RMoveTo,
			300, 300, -300, cffbuild.HLineTo, cffbuild.EndChar)), nil, one, 0},
		{"hmoveto then vmoveto, with a width", cffGlyph(cs(250, 100, cffbuild.HMoveTo,
			100, cffbuild.VMoveTo, 300, 300, -300, cffbuild.HLineTo, cffbuild.EndChar)), nil, one, 0},
		{"a moveto closes the contour before it", cffGlyph(cs(100, 100, cffbuild.RMoveTo,
			300, 300, -300, cffbuild.HLineTo, 400, -300, cffbuild.RMoveTo,
			200, 200, -200, cffbuild.HLineTo, cffbuild.EndChar)), nil, one, 0},

		// Hints change nothing drawn, but a hint mask is followed by a bit per stem, and reading
		// the wrong number of mask bytes turns the rest of the glyph into other operators. Nine
		// stems, the last implied by the mask's operands, is the count that needs a second byte.
		{"stems and a two-byte hint mask", cffGlyph(cs(250, 0, 50, 400, 50, cffbuild.HStemHM,
			100, 50, 100, 50, 100, 50, 100, 50, 100, 50, 100, 50, cffbuild.VStemHM,
			100, 50, cffbuild.HintMask, raw{0xFF, 0x80},
			100, 100, cffbuild.RMoveTo, 300, 300, -300, cffbuild.HLineTo,
			cffbuild.HintMask, raw{0x55, 0x00}, 400, -300, cffbuild.RMoveTo,
			200, 200, -200, cffbuild.HLineTo, cffbuild.EndChar)), nil, one, 0},
		{"hstem, vstem and a counter mask", cffGlyph(cs(0, 50, cffbuild.HStem, 100, 50, cffbuild.VStem,
			cffbuild.CntrMask, raw{0xC0}, 100, 100, cffbuild.RMoveTo, 300, 300, -300, cffbuild.HLineTo,
			cffbuild.EndChar)), nil, one, 0},

		// Subroutines, through their bias of 107: a local and a global one each drawing part of a
		// glyph, and a glyph that ends inside a subroutine it called through another.
		{"local and global subroutines", cffbuild.Builder{
			Glyphs: []cffbuild.Glyph{
				{SID: sidA, CharString: cs(-107, cffbuild.CallSubr, -107, cffbuild.CallGSubr,
					cffbuild.EndChar)},
				{SID: sidB, CharString: cs(-106, cffbuild.CallGSubr)},
			},
			Subrs: [][]byte{
				cs(100, 100, cffbuild.RMoveTo, cffbuild.Return),
				cs(100, 100, cffbuild.RMoveTo, 600, 0, -300, 500, cffbuild.RLineTo, cffbuild.Return),
			},
			GSubrs: [][]byte{
				cs(300, 300, -300, cffbuild.HLineTo, cffbuild.Return),
				cs(-106, cffbuild.CallSubr, cffbuild.EndChar),
			},
		}, nil, "BT /F1 100 Tf 20 40 Td (AB) Tj ET", 0},

		// seac: A is B with C over it, found by their StandardEncoding codes, 66 and 67. The
		// second case carries a width, the fifth operand endchar may take.
		{"seac", cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			{SID: sidA, CharString: cs(200, 450, 66, 67, cffbuild.EndChar)},
			{SID: sidB, CharString: cffbuild.Square()},
			{SID: sidC, CharString: cs(100, 100, cffbuild.RMoveTo, 200, 100, -200, cffbuild.HLineTo,
				cffbuild.EndChar)},
		}}, nil, one, 0},
		{"seac with a width", cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			{SID: sidA, CharString: cs(250, -100, 450, 66, 67, cffbuild.EndChar)},
			{SID: sidB, CharString: cffbuild.Diamond()},
			{SID: sidC, CharString: cs(100, 100, cffbuild.RMoveTo, 200, 100, -200, cffbuild.HLineTo,
				cffbuild.EndChar)},
		}}, nil, one, 14},

		// FontMatrix: a 2,000-unit em, which is the corpus's shape at 2,816, and a shear.
		{"a FontMatrix of a 2,000-unit em", cffbuild.Builder{
			FontMatrix: []float64{0.0005, 0, 0, 0.0005, 0, 0},
			Glyphs: []cffbuild.Glyph{{SID: sidA, CharString: cs(200, 200, cffbuild.RMoveTo,
				600, 600, -600, cffbuild.HLineTo, cffbuild.EndChar)}},
		}, nil, one, 0},
		{"a sheared FontMatrix", cffbuild.Builder{
			FontMatrix: []float64{0.001, 0, 0.0003, 0.001, 0, 0},
			Glyphs:     []cffbuild.Glyph{{SID: sidA, CharString: cffbuild.Diamond()}},
		}, nil, one, 2},

		// A CID-keyed font: two-byte codes through the charset, and each glyph drawn in its own
		// Font DICT's matrix and local subroutines, which FDSelect chooses.
		{"a CID-keyed font", cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{
			{CID: 1, CharString: cffbuild.Square()},
			{CID: 2, CharString: cffbuild.Diamond()},
		}}, nil, `BT /F1 50 Tf 20 100 Td <000100020001> Tj ET`, 51},
		{"a Font DICT's FontMatrix composed with the Top DICT's", cffbuild.Builder{CID: true,
			FontMatrix: []float64{0.5, 0, 0, 0.25, 0, 0},
			FDs:        []cffbuild.FontDict{{FontMatrix: []float64{0.002, 0, 0.001, 0.002, 0, 0}}},
			Glyphs:     []cffbuild.Glyph{{CID: 1, CharString: cffbuild.Diamond()}},
		}, nil, `BT /F1 100 Tf 20 40 Td <0001> Tj ET`, 2},
		{"FDSelect format 0", cffFDs(0), nil, `BT /F1 50 Tf 20 100 Td <00010002> Tj ET`, 21},
		{"FDSelect format 3", cffFDs(3), nil, `BT /F1 50 Tf 20 100 Td <00010002> Tj ET`, 21},
	} {
		t.Run(c.name, func(t *testing.T) {
			native, ref := drawBoth(t, c.stream, cffFont(c.b, c.opts...)...)
			if len(native) != len(ref) {
				t.Fatalf("size: pdfium %d pixels, native %d", len(ref), len(native))
			}
			var sum, inkRef, inkNative float64
			over := 0
			for i := range ref {
				d := math.Abs(float64(ref[i]) - float64(native[i]))
				sum += d
				if d > 32 {
					over++
				}
				inkRef += float64(ref[i])
				inkNative += float64(native[i])
			}
			mean := sum / float64(len(ref))
			t.Logf("mean %.3f over-32 %d ink %.3f", mean, over, inkNative/math.Max(inkRef, 1))
			if inkRef == 0 {
				t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
			}
			if r := inkNative / inkRef; r < 0.9 || r > 1.1 {
				t.Errorf("ink ratio %.3f, want within 10%% — the glyphs are the wrong size, place or shape", r)
			}
			if mean > 1.5 {
				t.Errorf("mean ink difference %.3f, want <= 1.5", mean)
			}
			if over > c.over {
				t.Errorf("%d pixels differ by more than 32, want at most %d", over, c.over)
			}
		})
	}
}

// cffFDs is a CID-keyed font of two glyphs in two Font DICTs, where reading the wrong one for
// glyph 2 draws it at twice the size or not at all: glyph 2 is the square at twice its size,
// drawn through the second dictionary's 2,000-unit em, and it calls local subroutine 0, which
// only that dictionary has.
func cffFDs(format int) cffbuild.Builder {
	cs := cffbuild.CS
	return cffbuild.Builder{CID: true, FDSelectFormat: format,
		FDs: []cffbuild.FontDict{{}, {
			FontMatrix: []float64{0.0005, 0, 0, 0.0005, 0, 0},
			Subrs:      [][]byte{cs(600, 600, -600, cffbuild.HLineTo, cffbuild.Return)},
		}},
		Glyphs: []cffbuild.Glyph{
			{CID: 1, FD: 0, CharString: cffbuild.Diamond()},
			{CID: 2, FD: 1, CharString: cs(200, 200, cffbuild.RMoveTo, -107, cffbuild.CallSubr,
				cffbuild.EndChar)},
		}}
}

// TestCFFCodeToGlyphTakesEachRoute pins which glyph each of ADR 0022's routes selects, in both
// engines.
//
// Each case is a font where the route under test and some other route reach different glyphs —
// a built-in encoding that disagrees with the glyph names, a charset whose CIDs are not the glyph
// indices — because a font whose routes agree cannot tell which one was read. Each is compared,
// exactly, against the glyph the route should reach drawn through cffLetters, in this backend and
// separately in pdfium: this backend's comparison says the route reached the glyph, and pdfium's
// that it is the glyph pdfium reaches too.
func TestCFFCodeToGlyphTakesEachRoute(t *testing.T) {
	// A and B swapped in the built-in encoding, so a route by code draws the diamond for A where
	// a route by name draws the square.
	swapped := func() cffbuild.Builder {
		b := cffLetters()
		b.Glyphs = b.Glyphs[1:3]
		b.Codes = []byte{'B', 'A'}
		return b
	}
	symbolic := []func(*textPDFOpts){withFlags(4), withEncoding("")}
	noBase := []func(*textPDFOpts){withEncoding("")}
	// Code 5 to CID 1, in a font that has a CID 5 too: a route that took the code for the CID
	// draws the square for the diamond.
	const fiveToOne = `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CMapName /Test def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidrange
<0005> <0005> 1
endcidrange
endcmap
CMapName currentdict /CMap defineresource pop
end
end`

	for _, c := range []struct {
		name   string
		b      cffbuild.Builder
		opts   []func(*textPDFOpts)
		stream string
		want   string // the glyph the route should reach, through cffLetters
	}{
		{"a WinAnsi name through the charset, not the built-in encoding", swapped(), nil,
			"(A)", "(A)"},
		{"a /Differences name over WinAnsi", cffLetters(), []func(*textPDFOpts){
			withEncoding("/Encoding<</BaseEncoding/WinAnsiEncoding/Differences[65/B]>>")},
			"(A)", "(B)"},
		{"a /Differences name with no base", swapped(), []func(*textPDFOpts){
			withEncoding("/Encoding<</Differences[65/A]>>")}, "(A)", "(A)"},
		{"a symbolic font's /Differences name, not its built-in encoding", swapped(),
			[]func(*textPDFOpts){withFlags(4), withEncoding("/Encoding<</Differences[65/A]>>")},
			"(A)", "(A)"},
		{"a symbolic font's code through the built-in encoding", swapped(), symbolic, "(A)", "(B)"},
		{"a symbolic font's code through a built-in encoding in ranges", func() cffbuild.Builder {
			b := cffLetters()
			b.Glyphs = b.Glyphs[1:]
			b.Codes, b.EncodingFormat = []byte{'B', 'C', 'A'}, 1
			return b
		}(), symbolic, "(A)", "(C)"},
		{"a symbolic font's code through an encoding supplement", func() cffbuild.Builder {
			b := swapped()
			b.Supplements = []cffbuild.Supplement{{Code: 'C', SID: sidA}}
			return b
		}(), symbolic, "(C)", "(A)"},
		{"a symbolic font's code through the predefined Standard encoding", func() cffbuild.Builder {
			b := cffLetters()
			b.Glyphs = []cffbuild.Glyph{b.Glyphs[2], b.Glyphs[1]}
			return b
		}(), symbolic, "(A)", "(A)"},
		{"a nonsymbolic font with no base, where both readings agree", func() cffbuild.Builder {
			b := cffLetters()
			b.Glyphs = []cffbuild.Glyph{b.Glyphs[2], b.Glyphs[1]}
			b.Codes = []byte{'B', 'A'}
			return b
		}(), noBase, "(A)", "(A)"},
		{"an MMType1 font", swapped(), []func(*textPDFOpts){withSubtype("MMType1")}, "(A)", "(A)"},

		// The charset, which is where a name becomes a glyph index.
		{"charset format 1", charsetFormat(1), nil, "(D)", "(C)"},
		{"charset format 2", charsetFormat(2), nil, "(D)", "(C)"},
		{"the predefined ISOAdobe charset", func() cffbuild.Builder {
			// GID n is SID n, so the fixture carries every glyph up to B.
			b := cffbuild.Builder{ISOAdobe: true}
			for sid := 1; sid <= sidB; sid++ {
				g := cffbuild.Glyph{CharString: cffbuild.CS(cffbuild.EndChar)}
				switch sid {
				case sidA:
					g.CharString = cffbuild.Square()
				case sidB:
					g.CharString = cffbuild.Diamond()
				}
				b.Glyphs = append(b.Glyphs, g)
			}
			return b
		}(), nil, "(B)", "(B)"},

		// A composite font's CID, which is a glyph index only in a program not CID-keyed.
		{"a CID through the charset", cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{
			{CID: 5, CharString: cffbuild.Square()},
			{CID: 1, CharString: cffbuild.Diamond()},
		}}, nil, "<0001>", "(B)"},
		{"a CID through a charset in ranges", cffbuild.Builder{CID: true, CharsetFormat: 2,
			Glyphs: []cffbuild.Glyph{
				{CID: 5, CharString: cffbuild.Square()},
				{CID: 6, CharString: cffbuild.Diamond()},
			}}, nil, "<0006>", "(B)"},
		{"a code through an embedded CMap to its CID", cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{
			{CID: 5, CharString: cffbuild.Square()},
			{CID: 1, CharString: cffbuild.Diamond()},
		}}, []func(*textPDFOpts){withCMap(fiveToOne)}, "<0005>", "(B)"},
		{"a CID as the glyph index, in a program not CID-keyed", cffLetters(), []func(*textPDFOpts){
			withComposite(), withDescendant("CIDFontType0"), withNoCIDToGIDMap()}, "<0003>", "(B)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, gotRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td "+c.stream+" Tj ET",
				cffFont(c.b, c.opts...)...)
			want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td "+c.want+" Tj ET",
				cffFont(cffLetters())...)
			assertSameInk(t, got, gotRef, want, wantRef)
		})
	}
}

// charsetFormat is a font whose charset is two ranges, A to B and then D, which is the triangle: a
// first range read one entry short names D the diamond, and one entry long pushes D past the last
// glyph.
func charsetFormat(format int) cffbuild.Builder {
	return cffbuild.Builder{CharsetFormat: format, Glyphs: []cffbuild.Glyph{
		{SID: sidA, CharString: cffbuild.Square()},
		{SID: sidB, CharString: cffbuild.Diamond()},
		{SID: sidD, CharString: cffTriangle},
	}}
}

// TestCFFFontsOutsideTheAgreedRoutesAreRefused pins every CFF font and code ADR 0022 refuses, by
// the reason it gives.
//
// Each font is one the interpreter could draw, on a page where which glyph to draw is the open
// question or the program is one this package does not read; each code is one that reaches no
// glyph. None occurs in the corpus.
func TestCFFFontsOutsideTheAgreedRoutesAreRefused(t *testing.T) {
	letters := cffLetters()
	cidFont := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{{CID: 1, CharString: cffbuild.Square()}}}
	top := func(operand int32, op ...byte) cffbuild.Builder {
		b := cffLetters()
		b.TopExtra = append(cffbuild.DictInt(operand), op...)
		return b
	}
	symbolic := []func(*textPDFOpts){withFlags(4), withEncoding("")}
	noBase := []func(*textPDFOpts){withEncoding("")}

	for _, c := range []struct {
		name   string
		prog   []byte // the program, when it is not b's
		b      cffbuild.Builder
		opts   []func(*textPDFOpts)
		stream string
		want   string
	}{
		// The program.
		{"an OpenType wrapper", append([]byte("OTTO"), letters.Build()...), letters, nil, "(A)",
			"an OpenType-wrapped CFF program"},
		{"CFF2", nil, cffbuild.Builder{Major: 2, Glyphs: letters.Glyphs}, nil, "(A)",
			"CFF major version 2"},
		{"a program that does not parse", []byte{1, 0, 4, 4, 0}, letters, nil, "(A)",
			"the FontFile3 program did not parse"},
		{"a SyntheticBase", nil, top(0, 12, 20), nil, "(A)", "SyntheticBase"},
		{"CharstringType 1", nil, top(1, 12, 6), nil, "(A)", "CharstringType"},
		{"the predefined Expert charset", nil, top(1, 15), nil, "(A)", "predefined Expert charset"},
		{"the predefined Expert encoding", nil, top(1, 16), nil, "(A)", "predefined Expert encoding"},
		{"a FontMatrix of an em per unit", nil, cffbuild.Builder{FontMatrix: []float64{1, 0, 0, 1, 0, 0},
			Glyphs: letters.Glyphs}, nil, "(A)", "FontMatrix"},
		// FreeType truncates a translation to whole units, rounds an em to one, and reads a shear
		// this steep as the identity, so pdfium draws each unlike its matrix.
		{"a translated FontMatrix", nil, cffbuild.Builder{FontMatrix: []float64{0.001, 0, 0, 0.001, 0.05, 0.02},
			Glyphs: letters.Glyphs}, nil, "(A)", "translates, which is refused"},
		{"a FontMatrix whose em FreeType rounds", nil, cffbuild.Builder{FontMatrix: []float64{0.5, 0, 0, 0.35, 0, 0},
			Glyphs: letters.Glyphs}, nil, "(A)", "makes an em of 2.85714 units, which FreeType rounds"},
		{"a CID Top DICT whose em FreeType rounds", nil, cffbuild.Builder{CID: true,
			FontMatrix: []float64{0.5, 0, 0, 0.35, 0, 0}, Glyphs: cidFont.Glyphs}, nil, "<0001>",
			"makes an em of 2.85714 units, which FreeType rounds"},
		{"a shear FreeType calls degenerate", nil, cffbuild.Builder{FontMatrix: []float64{0.001, 0, 0.0056, 0.001, 0, 0},
			Glyphs: letters.Glyphs}, nil, "(A)", "FontMatrix [0.001 0 0.0056 0.001 0 0], which is refused"},

		// The descriptor. Both keys name the one stream, which is enough: what is counted is the
		// keys.
		{"a descriptor with a FontFile beside the FontFile3", nil, letters, []func(*textPDFOpts){
			withFontFileKey("FontFile 6 0 R/FontFile3")}, "(A)",
			"the descriptor embeds more than one font program"},

		// The font the program is under.
		{"a TrueType font", nil, letters, []func(*textPDFOpts){withSubtype("TrueType")}, "(A)",
			"a FontFile3 program under a /TrueType font"},
		{"a CIDFontType2 descendant", nil, cidFont, []func(*textPDFOpts){withDescendant("CIDFontType2")},
			"<0001>", "a FontFile3 program under a /CIDFontType2 descendant"},
		{"a CIDToGIDMap stream on a CIDFontType0 font", nil, cidFont, []func(*textPDFOpts){
			func(o *textPDFOpts) { o.noCIDToGIDMap = false }},
			"<0001>", "a /CIDToGIDMap stream on a CIDFontType0 font"},
		{"a CID-keyed program under a simple font", nil, cidFont, []func(*textPDFOpts){
			func(o *textPDFOpts) { o.composite = false }, withSubtype("Type1")},
			"(A)", "a CID-keyed CFF program under a simple font"},
		{"a base encoding other than WinAnsi", nil, letters, []func(*textPDFOpts){
			withEncoding("/Encoding/MacRomanEncoding")}, "(A)", "a CFF font over /MacRomanEncoding"},
		{"a dictionary's base other than WinAnsi", nil, letters, []func(*textPDFOpts){
			withEncoding("/Encoding<</BaseEncoding/MacExpertEncoding>>")}, "(A)",
			"a CFF font over /MacExpertEncoding"},
		{"a font named Symbol", nil, letters, []func(*textPDFOpts){withBaseFont("Symbol")}, "(A)",
			"a CFF font named Symbol"},
		// The subset prefix is not part of the name pdfium matches.
		{"a subset named SymbolMT", nil, letters, []func(*textPDFOpts){withBaseFont("ABCDEF+SymbolMT")},
			"(A)", "a CFF font named SymbolMT"},
		{"a font named ZapfDingbats", nil, letters, []func(*textPDFOpts){withBaseFont("ZapfDingbats")},
			"(A)", "a CFF font named ZapfDingbats"},

		// And one code rather than the font.
		{"a nonsymbolic font with no base, where the readings part", nil, func() cffbuild.Builder {
			b := cffLetters()
			b.Glyphs = b.Glyphs[1:3]
			b.Codes = []byte{'B', 'A'}
			return b
		}(), noBase, "(A)", "no glyph for code 65 in /Test: its StandardEncoding name selects glyph 1" +
			" and the program's built-in encoding glyph 2"},
		{"a nonsymbolic font with no base, whose charset lacks the StandardEncoding name", nil,
			func() cffbuild.Builder {
				b := cffLetters()
				b.Glyphs = b.Glyphs[1:3]
				b.Codes = []byte{'A', 'C'}
				return b
			}(), noBase, "(C)", "no glyph for code 67 in /Test: the program maps it to none"},
		{"a nonsymbolic font with no base, whose built-in encoding lacks the code", nil,
			func() cffbuild.Builder {
				b := cffLetters()
				b.Glyphs = b.Glyphs[1:3]
				b.Codes = []byte{'A'}
				return b
			}(), noBase, "(B)", "no glyph for code 66 in /Test: the program maps it to none"},
		{"a symbolic font's code the built-in encoding leaves unassigned", nil, func() cffbuild.Builder {
			b := cffLetters()
			b.Glyphs = b.Glyphs[1:3]
			b.Codes = []byte{'A'}
			return b
		}(), symbolic, "(B)", "no glyph for code 66 in /Test: the program maps it to none"},
		{"a code WinAnsi leaves unnamed", nil, letters, nil, `(\201)`,
			"no glyph for code 129 in /Test: /WinAnsiEncoding names no glyph for it"},
		{"a name the charset does not carry", nil, letters, nil, "(Z)",
			"no glyph for code 90 in /Test: the program maps it to none"},
		{".notdef by name", nil, letters, []func(*textPDFOpts){
			withEncoding("/Encoding<</BaseEncoding/WinAnsiEncoding/Differences[65/.notdef]>>")},
			"(A)", "no glyph for code 65 in /Test: the program maps it to none"},
		{"a CID the charset does not carry", nil, cidFont, nil, "<0009>",
			"no glyph for code 9 in /Test: the program maps it to none"},
		{"CID 0", nil, cidFont, nil, "<0000>", "no glyph for code 0 in /Test: the program maps it to none"},
		{"a CID past the glyphs of a program not CID-keyed", nil, letters, []func(*textPDFOpts){
			withComposite(), withDescendant("CIDFontType0"), withNoCIDToGIDMap()}, "<0009>",
			"no glyph for code 9 in /Test: the program maps it to none"},
		{"a glyph whose charstring is refused", nil, cffGlyph(cffbuild.CS(100, 100, cffbuild.RMoveTo,
			cffbuild.Random, 0, cffbuild.RLineTo, cffbuild.EndChar)), nil, "(A)",
			"code 65 in /Test does not draw: font: CFF glyph 1: the random operator, which is refused"},
	} {
		t.Run(c.name, func(t *testing.T) {
			opts := cffFont(c.b, c.opts...)
			if c.prog != nil {
				opts = append(opts, withProgram(c.prog))
			}
			err := renderErr(t, textPDF(t, "BT /F1 24 Tf 20 100 Td "+c.stream+" Tj ET", 200, opts...))
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

// TestCFFGlyphWorkIsBoundedPerPage pins the budget of ADR 0022: glyph outlines are charged to the
// page, once each.
//
// A charstring cannot loop, but subroutines nest ten deep and each may call the next many times,
// so the fixture is a chain of them whose work is a fixed fraction of the budget. Two different
// glyphs of that weight exceed it together and not alone; the same glyph shown three times is
// built once and charged once, because a page of text shows the same glyphs over and over.
func TestCFFGlyphWorkIsBoundedPerPage(t *testing.T) {
	subrs, heavy, depth, fan := heavyCFFFixture()
	b := cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{
		{SID: sidA, CharString: heavy},
		{SID: sidB, CharString: heavy},
	}}

	// The premise, measured rather than assumed: one glyph fits the budget, and two do not.
	c, err := font.ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	_, ops, err := c.Outline(1, math.MaxInt)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if ops > maxGlyphWork || 2*ops <= maxGlyphWork {
		t.Fatalf("one glyph takes %d operations; the fixture needs one under %d and two over it",
			ops, maxGlyphWork)
	}

	t.Run("one glyph shown three times is built once and painted", func(t *testing.T) {
		native, ref := drawBoth(t, "BT /F1 24 Tf 20 100 Td (AAA) Tj ET", cffFont(b)...)
		var nativeSum, refSum float64
		for i := range ref {
			nativeSum += float64(native[i])
			refSum += float64(ref[i])
		}
		if refSum == 0 {
			t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
		}
		if r := nativeSum / refSum; r < 0.9 || r > 1.1 {
			t.Errorf("ink ratio %.3f against pdfium, want within 10%%: the glyph was not painted", r)
		}
	})
	t.Run("two glyphs are charged together", func(t *testing.T) {
		err := renderErr(t, textPDF(t, "BT /F1 24 Tf 20 100 Td (AB) Tj ET", 200, cffFont(b)...))
		var u *Unsupported
		if !errors.As(err, &u) {
			t.Fatalf("err = %v, want an *Unsupported naming the budget", err)
		}
		if joined, want := strings.Join(u.Ops, " | "),
			"the page's glyphs take more than 4194304 operations to build and draw"; !strings.Contains(joined, want) {
			t.Errorf("refusal = %q, want it to mention %q", joined, want)
		}
	})
	t.Run("work of exactly the budget is within it, pinned for both formats", func(t *testing.T) {
		// The TrueType square behind the page's own /F1 costs 12 however it is reached: one visit,
		// its one contour and its four points to build, and the move, four lines and close it
		// draws — fixed by the fixture, unlike the CFF side, which exactCFFOps hits to the operation.
		const ttWork = 12

		exact := cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{
			{SID: sidA, CharString: exactCFFOps(subrs, depth, fan, maxGlyphWork-ttWork)},
		}}.Build()
		if pc, err := font.ParseCFF(exact); err != nil {
			t.Fatalf("ParseCFF: %v", err)
		} else if _, ops, err := pc.Outline(1, math.MaxInt); err != nil || ops != maxGlyphWork-ttWork {
			t.Fatalf("the glyph takes %d operations (err %v), want exactly %d", ops, err, maxGlyphWork-ttWork)
		}
		over := cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{
			{SID: sidA, CharString: exactCFFOps(subrs, depth, fan, maxGlyphWork-ttWork+1)},
		}}.Build()

		// ref is the square alone: the CFF glyph above never calls a path operator, so it never
		// draws anything, and a page that also Do's it should paint pixel for pixel like this one.
		ref := renderNative(t, textPDF(t, "BT /F1 24 Tf 20 60 Td (A) Tj ET", 200), 72)
		refInk, _, _ := ink(ref.Image)

		for _, v := range []struct {
			name    string
			ttFirst bool
		}{
			{"last unit is CFF work", true},
			{"last unit is TrueType work", false},
		} {
			t.Run(v.name, func(t *testing.T) {
				ra := renderNative(t, heavyFormPage(t, exact, v.ttFirst, "A"), 72)
				got, _, _ := ink(ra.Image)
				if len(got) != len(refInk) {
					t.Fatalf("size: %d pixels, want %d", len(got), len(refInk))
				}
				for i := range got {
					if got[i] != refInk[i] {
						t.Fatalf("pixel %d = %d, want %d: the square was not painted, or the CFF"+
							" glyph drew something it should not have", i, got[i], refInk[i])
					}
				}
			})
			t.Run(v.name+", one operation over the budget is refused", func(t *testing.T) {
				err := renderErr(t, heavyFormPage(t, over, v.ttFirst, "A"))
				var u *Unsupported
				if !errors.As(err, &u) {
					t.Fatalf("err = %v, want an *Unsupported naming the budget", err)
				}
				if joined, want := strings.Join(u.Ops, " | "),
					"the page's glyphs take more than 4194304 operations to build and draw"; !strings.Contains(joined, want) {
					t.Errorf("refusal = %q, want it to mention %q", joined, want)
				}
			})
		}

		// At exactly the budget a build is still given what is left, which is nothing, and a glyph
		// that fails spending none of it — an empty charstring, which never reaches endchar — is
		// refused for its own fault. Only a glyph that finds the page already over is the budget's.
		t.Run("a glyph that fails at exactly the budget is refused for its own fault", func(t *testing.T) {
			empty := cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{
				{SID: sidA, CharString: exactCFFOps(subrs, depth, fan, maxGlyphWork-ttWork)},
				{SID: sidB, CharString: []byte{}},
			}}.Build()
			err := renderErr(t, heavyFormPage(t, empty, true, "AB"))
			var u *Unsupported
			if !errors.As(err, &u) {
				t.Fatalf("err = %v, want an *Unsupported naming glyph B", err)
			}
			joined := strings.Join(u.Ops, " | ")
			if want := "code 66 in /Heavy does not draw"; !strings.Contains(joined, want) {
				t.Errorf("refusal = %q, want it to mention %q", joined, want)
			}
			if budget := "operations to build and draw"; strings.Contains(joined, budget) {
				t.Errorf("refusal = %q, want no budget refusal: the page is at the budget, not over it", joined)
			}
		})
	})
}

// heavyCFFFixture returns depth levels of fan-wide fan-out subroutines, and a charstring that
// calls the top of that chain three times before drawing a small triangle: a glyph whose build
// cost is a known fraction of maxGlyphWork — over half and under it — and whose ink is real,
// which the budget tests and the form-caching test both need.
//
// Subroutine i calls subroutine i+1 fan times, and the last draws nothing and returns: about a
// million operations, which the glyph spends three times.
func heavyCFFFixture() (subrs [][]byte, heavy []byte, depth, fan int) {
	depth, fan = 7, 6
	for i := 0; i < depth; i++ {
		var items []any
		for range fan {
			items = append(items, i+1-107, cffbuild.CallSubr)
		}
		subrs = append(subrs, cffbuild.CS(append(items, cffbuild.Return)...))
	}
	subrs = append(subrs, cffbuild.CS(cffbuild.Return))
	heavy = cffbuild.CS(-107, cffbuild.CallSubr, -107, cffbuild.CallSubr, -107, cffbuild.CallSubr,
		100, 100, cffbuild.RMoveTo, 300, 300, -300, cffbuild.HLineTo, cffbuild.EndChar)
	return subrs, heavy, depth, fan
}

// exactCFFOps returns a charstring over subrs (built for depth levels of fan-wide fan-out, as
// above) whose Outline costs exactly ops operations and draws nothing: the padding below endchar
// is subroutine calls and dotsection, neither of which is a path operator.
//
// cost[i] is what one call of subroutine i takes: its number, the callsubr, and the subroutine's
// own work. Calls are taken greedily from the dearest, padded with dotsection, which does nothing,
// to spend the target to the operation.
func exactCFFOps(subrs [][]byte, depth, fan, ops int) []byte {
	cost := make([]int, depth+1)
	cost[depth] = 3
	for i := depth - 1; i >= 0; i-- {
		cost[i] = 2 + fan*cost[i+1] + 1
	}
	var items []any
	left := ops - 1 // the endchar
	for i, c := range cost {
		for ; left >= c; left -= c {
			items = append(items, i-107, cffbuild.CallSubr)
		}
	}
	for ; left > 0; left-- {
		items = append(items, cffbuild.DotSection)
	}
	return cffbuild.CS(append(items, cffbuild.EndChar)...)
}

// heavyFormPage is a page whose /F1 is the default embedded TrueType font — showing the square, in
// 'A' — and whose /Fm1 form has a font of its own, /F2, over prog, showing show: the two in the
// order ttFirst asks for, so the same two glyphs can pin the budget's boundary with either
// format's charge the one that lands on it.
func heavyFormPage(t *testing.T, prog []byte, ttFirst bool, show string) string {
	t.Helper()
	form := formXO("0 0 200 200", "/Resources<</Font<</F2 9 0 R>>>>", "BT /F2 24 Tf 20 100 Td ("+show+") Tj ET")
	heavy := "<</Type/Font/Subtype/Type1/BaseFont/Heavy/FirstChar 65/LastChar 66/Widths[500 500]" +
		"/Encoding/WinAnsiEncoding/FontDescriptor 10 0 R>>"
	desc := "<</Type/FontDescriptor/FontName/Heavy/Flags 32/ItalicAngle 0/Ascent 800/Descent -200" +
		"/CapHeight 700/StemV 80/FontBBox[0 -200 1000 800]/FontFile3 11 0 R>>"
	stream := fmt.Sprintf("<</Subtype/Type1C/Length %d>>\nstream\n%s\nendstream", len(prog), prog)
	tt, formDo := "BT /F1 24 Tf 20 60 Td (A) Tj ET", "/Fm1 Do"
	body := formDo + " " + tt
	if ttFirst {
		body = tt + " " + formDo
	}
	return textPDF(t, body, 200, withXObjects("/Fm1 8 0 R", form, heavy, desc, stream))
}

// renderErr renders page 1 of path at 72 dpi and returns only the error.
func renderErr(t *testing.T, path string) error {
	t.Helper()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	_, err = New(s).Page(1, o)
	return err
}

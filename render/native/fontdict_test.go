package native

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// simpleCFFFontPDF builds a one-page PDF with a simple Type1 CFF font (cffLetters: the space, A
// the square, B the diamond, C the triangle, over the program's own predefined Standard encoding)
// shown as "(A)", writing /BaseFont and the descriptor's /Flags exactly as given.
//
// Neither entry can be written through textPDF's Go-typed options: baseFont there is always a
// name, and flags is an int, which cannot even hold C20's out-of-range value on a 32-bit build.
// Writing the two dictionaries by hand here is what lets a fixture put a string where /BaseFont
// names one, or a value outside int32 or not a number at all into /Flags.
func simpleCFFFontPDF(t *testing.T, baseFont, flags string) string {
	t.Helper()
	prog := cffLetters().Build()
	fontDict := "<</Type/Font/Subtype/Type1/BaseFont " + baseFont + "/FirstChar 65/LastChar 67" +
		"/Widths[600 600 600]/Encoding/WinAnsiEncoding/FontDescriptor 5 0 R>>"
	desc := "<</Type/FontDescriptor/FontName/Test/Flags " + flags + "/ItalicAngle 0/Ascent 800" +
		"/Descent -200/CapHeight 700/StemV 80/FontBBox[0 -200 1000 800]/FontFile3 6 0 R>>"
	stream := "BT /F1 24 Tf 20 100 Td (A) Tj ET"
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]/Resources<</Font<</F1 4 0 R>>>>/Contents 7 0 R>>",
		fontDict,
		desc,
		fmt.Sprintf("<</Subtype/Type1C/Length %d>>\nstream\n%s\nendstream", len(prog), prog),
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(stream), stream),
	}
	return buildPDF(t, objs, "simplecff.pdf")
}

// wantRefusedText renders path and fails unless it comes back an *Unsupported keyed as a text
// reason — the shape every refusal in checkText takes — returning the joined reason so a caller
// that also wants to pin which one fired can check that itself.
func wantRefusedText(t *testing.T, path string) string {
	t.Helper()
	err := renderErr(t, path)
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want an *Unsupported", err)
	}
	joined := strings.Join(u.Ops, " | ")
	if !strings.Contains(joined, "text:") {
		t.Errorf("refusal = %q, want it keyed as a text reason", joined)
	}
	return joined
}

// wantRefused renders path and fails unless it comes back an *Unsupported whose reason mentions
// want, keyed as a text reason.
func wantRefused(t *testing.T, path, want string) {
	t.Helper()
	joined := wantRefusedText(t, path)
	if !strings.Contains(joined, want) {
		t.Errorf("refusal = %q, want it to mention %q", joined, want)
	}
}

// TestIrregularSimpleFontDictionariesAreRefused pins C15, C16, C17, C20, R#0, and R#2: a simple font
// dictionary with an entry pdfium reads under a looser type or a wider range than font.Font does,
// which parseFont now refuses on before it ever chooses a program.
func TestIrregularSimpleFontDictionariesAreRefused(t *testing.T) {
	t.Run("C15: /BaseFont as a string", func(t *testing.T) {
		// pdfium's cpdf_font.cpp GetByteStringFor reads a string /BaseFont too, and would go on to
		// treat (Symbol) or (ZapfDingbats) as the matching base-14 face; this fixture only has to
		// show that any string there is refused, not reproduce the Symbol substitution itself.
		wantRefused(t, simpleCFFFontPDF(t, "(Test)", "32"), "/BaseFont present and not a name")
	})
	t.Run("C20: /Flags outside a 32-bit value's range", func(t *testing.T) {
		// 2^32+4: font.flags() keeps the full int64 and would read bit 3 (symbolic) as set. What
		// pdfium's own number parser and GetIntegerFor (cpdf_font.cpp) do with a value outside
		// int32 range is not on disk to read (R#3: neither core/fxcrt's number parsing nor
		// CPDF_Number is vendored here), and this fixture's /Encoding names /WinAnsiEncoding, so
		// type1GID resolves by name regardless of which bits flags() reports — no route through
		// /Flags is exercised here to measure. The test shows only that this package refuses the
		// value outright, rather than trusting a bit its own int64 reading and a 32-bit reader's
		// GetIntegerFor may disagree on.
		//
		// It asserts only that this is refused, not which of checkFlags' reasons fires: pdfcpu's
		// own integer parser overflows this value to Integer(0) on GOARCH=386 before font.go ever
		// sees it (model/parse.go's parseNumericOrIndRef, the isRangeError branch — #407), so on
		// that platform the range check never runs against it — but 0 has neither the symbolic
		// nor the nonsymbolic bit set, and R#0's both-clear rule refuses that shape regardless.
		// The two platforms take different reasons to the same refusal, which is the point.
		wantRefusedText(t, simpleCFFFontPDF(t, "/Test", "4294967300"))
	})
	t.Run("C21: /Flags present and not a number", func(t *testing.T) {
		// pdfium's GetIntegerFor reads a present, non-number value as 0, not as the default this
		// package's flags() falls back to for an absent key — the same divergence C21 finds in
		// ttRoute, caught here before ttRoute is even reached.
		wantRefused(t, simpleCFFFontPDF(t, "/Test", "/X"), "/Flags present and not an integer")
	})
	// A literal /Flags null has no render-level pin: pdfcpu's own parser drops a null-valued
	// dictionary entry at parse time (ISO 32000-2 §7.3.9's rule, taken literally — see
	// parseDict, pkg/pdfcpu/model/parse.go), so by the time Load ever sees the descriptor the
	// key is gone, indistinguishable from an absent /Flags. checkFlags's raw-map check is still
	// correct for a Store that keeps the entry — font_test.go's TestIrregularFlags and
	// TestSimpleFontReportsWhatRoutesItsGlyphs pin it against the fake store this package uses
	// for exactly that reason — but this adapter never hands it one. Reported under concerns.
	t.Run("R#2: /Flags a reference to nothing", func(t *testing.T) {
		// Object 99 is never defined, so this reference dangles. ISO 32000-2 §7.3.10 makes it
		// the same object as null, and pdfium's GetIntegerFor reads it the same way: 0, not its
		// absent-key default.
		wantRefused(t, simpleCFFFontPDF(t, "/Test", "99 0 R"), "/Flags present and null, or a reference to nothing")
	})
	t.Run("R#0: /Flags with both the symbolic and nonsymbolic bits set", func(t *testing.T) {
		// Table 121: "This flag and the Nonsymbolic flag shall not both be set or both be
		// clear." ttRoute used to refuse this shape itself, as a TrueType-specific route
		// conflict; now checkFlags reports it through Irregular before ttRoute (or parseCFF)
		// ever runs, so the refusal — and the test that pins it — moved here from
		// text_test.go's TestSimpleTrueTypeFontsOutsideTheAgreedRoutesAreRefused.
		path := textPDF(t, "BT /F1 24 Tf 20 100 Td (A) Tj ET", 200, withFlags(36))
		wantRefused(t, path, "both the symbolic and nonsymbolic bits set")
	})
	t.Run("C16: /BaseEncoding as a string", func(t *testing.T) {
		// pdfium's cpdf_simplefont.cpp reads /BaseEncoding with GetByteStringFor as well, so a
		// string here still names a base encoding to pdfium while font.baseEncoding leaves this
		// package's f.baseName empty.
		path := textPDF(t, "BT /F1 24 Tf 20 100 Td (A) Tj ET", 200,
			cffFont(cffLetters(), withEncoding("/Encoding<</BaseEncoding(WinAnsiEncoding)>>"))...)
		wantRefused(t, path, "/BaseEncoding present and not a name")
	})
	t.Run("C17: a /Differences entry that is neither an integer 0..255 nor a name", func(t *testing.T) {
		// pdfium's LoadDifferences (cpdf_simplefont.cpp) reads the string (x) through GetInteger,
		// as 0, and keeps counting from there; applyDifferences requires a literal integer and has
		// nothing to do with a string but skip it and move on to the next entry unmodified.
		path := textPDF(t, "BT /F1 24 Tf 20 100 Td (A) Tj ET", 200, cffFont(cffLetters(),
			withEncoding("/Encoding<</BaseEncoding/WinAnsiEncoding/Differences[65/A(x)/C]>>"))...)
		wantRefused(t, path, "/Differences containing an entry that is neither an integer 0..255 nor a name")
	})
}

// TestFlaglessOrIneffectiveDifferencesAreRefused pins C21's TrueType-specific half: an
// /Encoding dictionary's /Differences array, present but assigning nothing, still moves pdfium off
// the by-name route ttRoute would otherwise take.
func TestFlaglessOrIneffectiveDifferencesAreRefused(t *testing.T) {
	path := textPDF(t, "BT /F1 24 Tf 20 100 Td (A) Tj ET", 200,
		withEncoding("/Encoding<</BaseEncoding/WinAnsiEncoding/Differences[]>>"))
	wantRefused(t, path, "a TrueType font with /Differences")
}

// TestRegularSimpleFontDictionaryAgreesWithPdfium is the control: a dictionary with none of the
// irregularities above, over the same CFF program, drawn and compared against pdfium — proving
// the refusals above are about the irregular entry and not about this shape of font in general.
func TestRegularSimpleFontDictionaryAgreesWithPdfium(t *testing.T) {
	native, ref := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET", cffFont(cffLetters())...)
	if len(native) != len(ref) {
		t.Fatalf("size: pdfium %d pixels, native %d", len(ref), len(native))
	}
	var sum, inkRef, inkNative float64
	for i := range ref {
		sum += math.Abs(float64(ref[i]) - float64(native[i]))
		inkRef += float64(ref[i])
		inkNative += float64(native[i])
	}
	if inkRef == 0 {
		t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
	}
	if r := inkNative / inkRef; r < 0.9 || r > 1.1 {
		t.Errorf("ink ratio %.3f, want within 10%%", r)
	}
	if mean := sum / float64(len(ref)); mean > 1.5 {
		t.Errorf("mean ink difference %.3f, want <= 1.5", mean)
	}
}

// TestDifferencesAtCodeBoundariesAgreeWithPdfium pins R#5: /Differences starting at code 0 or at
// code 255 is itself a valid §9.6.5.1 code, not the out-of-range shape checkDifferences refuses,
// so a page using either boundary draws — the diamond, named B in cffLetters, assigned there by
// /Differences rather than by the font's own predefined Standard encoding — and agrees with
// pdfium, giving the mutants that narrow checkDifferences' range (`e < 1`, `e > 254`) something to
// fail against: either would refuse the page this test renders.
func TestDifferencesAtCodeBoundariesAgreeWithPdfium(t *testing.T) {
	for _, c := range []struct {
		name, stream, diff string
	}{
		{"code 0", `BT /F1 48 Tf 20 100 Td (\000) Tj ET`, "[0/B]"},
		{"code 255", `BT /F1 48 Tf 20 100 Td (\377) Tj ET`, "[255/B]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			native, ref := drawBoth(t, c.stream, cffFont(cffLetters(), withEncoding(
				"/Encoding<</BaseEncoding/WinAnsiEncoding/Differences"+c.diff+">>"))...)
			if len(native) != len(ref) {
				t.Fatalf("size: pdfium %d pixels, native %d", len(ref), len(native))
			}
			var inkRef, inkNative float64
			for i := range ref {
				inkRef += float64(ref[i])
				inkNative += float64(native[i])
			}
			if inkRef == 0 {
				t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
			}
			if r := inkNative / inkRef; r < 0.9 || r > 1.1 {
				t.Errorf("ink ratio %.3f, want within 10%%", r)
			}
		})
	}
}

// TestDifferencesCodeCloseToWrapDisagreesWithPdfium pins R#1: checkDifferences' doc's account of
// how LoadDifferences' uint32_t cur_code (cpdf_simplefont.cpp) wraps back into range and assigns a
// name applyDifferences leaves dropped — more than 2^32-c names after a starting code c wrap it,
// so three names are enough at 4294967294 and four at 4294967293, the two cases this test
// exercises (checkDifferences' doc gives 4294967295, two names, as the fewest case overall; this
// test does not need the fewest case to reach the same disagreement). checkDifferences' 0..255
// check refuses both codes outright, so this pins the refusal against pdfium actually drawing the
// wrapped name, rather than only asserting the refusal in a comment.
//
// 64-bit only: on GOARCH=386, pdfcpu's own parser reads a code this far outside int32 as 0 before
// font.go ever sees it — the deferred renderer-wide follow-up checkDifferences' doc names, not a
// gap this group's checks close — so the array reads as [0/A/B/C], a perfectly regular one
// checkDifferences has no reason to refuse.
func TestDifferencesCodeCloseToWrapDisagreesWithPdfium(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("pdfcpu reads these codes as 0 on GOARCH=386; see the doc above")
	}
	o := render.DefaultOptions
	o.DPI = 72
	measure := func(stream string, opts ...func(*textPDFOpts)) []uint8 {
		p, err := pdfium.Open(textPDF(t, stream, 200, opts...))
		if err != nil {
			t.Fatalf("pdfium.Open: %v", err)
		}
		defer func() { _ = p.Close() }()
		got, err := p.Page(1, o)
		if err != nil {
			t.Fatalf("pdfium Page: %v", err)
		}
		px, _, _ := ink(got.Image)
		return px
	}

	for _, c := range []struct {
		name, diff, want string
	}{
		{"4294967294, wraps after three names", "[4294967294/A/B/C]", "(C)"},
		{"4294967293, wraps after four names", "[4294967293/A/A/A/B]", "(B)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			opts := cffFont(cffLetters(), withEncoding(
				"/Encoding<</BaseEncoding/WinAnsiEncoding/Differences"+c.diff+">>"))
			path := textPDF(t, `BT /F1 48 Tf 20 100 Td (\000) Tj ET`, 200, opts...)
			wantRefused(t, path, "a code outside 0..255")

			got := measure(`BT /F1 48 Tf 20 100 Td (\000) Tj ET`, opts...)
			want := measure("BT /F1 48 Tf 20 100 Td "+c.want+" Tj ET", cffFont(cffLetters())...)
			var diff int
			for i := range got {
				if got[i] != want[i] {
					diff++
				}
			}
			if diff != 0 {
				t.Errorf("pdfium: %d pixels differ from the letter the wrap assigns to code 0, "+
					"want 0", diff)
			}
		})
	}
}

// swappedFlagsCFFFontPDF builds a one-page PDF, with no /Encoding key at all, whose CFF program
// swaps its by-name and built-in encodings: code 'A' names the square by the predefined Standard
// encoding cffLetters() uses, but the program's own built-in encoding (Codes) gives code 'A' the
// diamond's GID instead. With no /Encoding key, which glyph draws for code 'A' depends only on
// whether /Flags reads as symbolic or nonsymbolic. Writing /Flags by hand, as simpleCFFFontPDF
// does above, is what lets this carry a value outside int32; withFlags' int parameter cannot even
// hold one on a 32-bit build.
func swappedFlagsCFFFontPDF(t *testing.T, flags string) string {
	t.Helper()
	b := cffLetters()
	b.Glyphs = b.Glyphs[1:3]
	b.Codes = []byte{'B', 'A'}
	prog := b.Build()
	fontDict := "<</Type/Font/Subtype/Type1/BaseFont/Test/FirstChar 65/LastChar 66" +
		"/Widths[600 600]/FontDescriptor 5 0 R>>"
	desc := "<</Type/FontDescriptor/FontName/Test/Flags " + flags + "/ItalicAngle 0/Ascent 800" +
		"/Descent -200/CapHeight 700/StemV 80/FontBBox[0 -200 1000 800]/FontFile3 6 0 R>>"
	stream := "BT /F1 24 Tf 20 100 Td (A) Tj ET"
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]/Resources<</Font<</F1 4 0 R>>>>/Contents 7 0 R>>",
		fontDict,
		desc,
		fmt.Sprintf("<</Subtype/Type1C/Length %d>>\nstream\n%s\nendstream", len(prog), prog),
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(stream), stream),
	}
	return buildPDF(t, objs, "swappedflags.pdf")
}

// TestFlagsOutsideTheRangeWithOneBitIsRefusedNotRouted pins font_test.go's TestIrregularFlags
// doc: checkFlags' range check refuses a value outside [-2^31, 2^31) even when the value's low 32
// bits set exactly one of Table 121's two bits, a shape R#0's both/neither bit rule alone would
// call regular. 4294967300 (2^32+4) is that shape — its low 32 bits are 4, the symbolic bit alone
// — so with the range check deleted (checked in a scratch copy, never in this tree) checkFlags
// finds it regular, type1GID reads the font as symbolic, and this backend draws the diamond
// swappedFlagsCFFFontPDF's built-in encoding gives code 'A' under a symbolic reading.
//
// This measures pdfium's side rather than only asserting it in a comment: pdfium draws the same
// square for flags 4294967300 as it does for flags 32 (nonsymbolic), not the diamond flags 4
// (symbolic) would draw — so with the range check deleted, this package's diamond and pdfium's
// square would be the disagreement the range check is here to prevent.
//
// The refusal itself asserts only that this is refused, not which of checkFlags' reasons fires,
// for the same reason as C20 above: on GOARCH=386, pdfcpu's own integer parser overflows this
// value to Integer(0) before font.go ever sees it, and 0 is refused by the both-clear bit rule
// instead — still refused, just not by the range check this test is otherwise pinning.
func TestFlagsOutsideTheRangeWithOneBitIsRefusedNotRouted(t *testing.T) {
	path := swappedFlagsCFFFontPDF(t, "4294967300")
	wantRefusedText(t, path)

	o := render.DefaultOptions
	o.DPI = 72
	p, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium.Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	got, err := p.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}
	gotInk, _, _ := ink(got.Image)

	squarePath := swappedFlagsCFFFontPDF(t, "32")
	sp, err := pdfium.Open(squarePath)
	if err != nil {
		t.Fatalf("pdfium.Open: %v", err)
	}
	defer func() { _ = sp.Close() }()
	square, err := sp.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}
	squareInk, _, _ := ink(square.Image)

	var diff int
	for i := range gotInk {
		if gotInk[i] != squareInk[i] {
			diff++
		}
	}
	if diff != 0 {
		t.Errorf("pdfium: %d pixels differ from flags 32's square, want flags 4294967300 to draw "+
			"the same glyph", diff)
	}
}

// TestFlagsSignedLiteralAboveIntMaxIsRefusedEitherWay pins R#0: objects.Store cannot tell a
// '+'-signed /Flags literal from an unsigned one once pdfcpu has parsed it — both come back as the
// same int64 — but pdfium reads them apart. Measured against swappedFlagsCFFFontPDF: "+2147483652"
// draws as flags 0 in pdfium, the same square as flags 32, while the unsigned "2147483652" draws
// at its full uint32 value, 0x80000004, the same diamond as flags 4. checkFlags' range refuses
// both, since nothing here can tell which literal produced the value once pdfcpu has parsed it;
// the unsigned case is refused more strictly than pdfium needs (documented in checkFlags' doc),
// but the '+'-signed case would otherwise draw the wrong glyph.
//
// The refusal itself asserts only that this is refused, not which of checkFlags' reasons fires,
// for the same reason as C20 above: on GOARCH=386, pdfcpu's own strconv.Atoi returns ErrRange for
// both literals — "+2147483652" and "2147483652" both exceed a 32-bit int's range there — so its
// #407 branch substitutes Integer(0) for both before font.go ever sees either value, and 0 is
// refused by the both-clear bit rule instead of the range check this test pins on a 64-bit build.
func TestFlagsSignedLiteralAboveIntMaxIsRefusedEitherWay(t *testing.T) {
	o := render.DefaultOptions
	o.DPI = 72
	measure := func(flags string) []uint8 {
		p, err := pdfium.Open(swappedFlagsCFFFontPDF(t, flags))
		if err != nil {
			t.Fatalf("pdfium.Open: %v", err)
		}
		defer func() { _ = p.Close() }()
		got, err := p.Page(1, o)
		if err != nil {
			t.Fatalf("pdfium Page: %v", err)
		}
		px, _, _ := ink(got.Image)
		return px
	}
	square, diamond := measure("32"), measure("4")

	for _, c := range []struct {
		name  string
		flags string
		want  []uint8
	}{
		{"a '+'-signed literal, which pdfium reads as flags 0", "+2147483652", square},
		{"the same bits unsigned, which pdfium reads at their full uint32 value", "2147483652", diamond},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantRefusedText(t, swappedFlagsCFFFontPDF(t, c.flags))

			got := measure(c.flags)
			var diff int
			for i := range got {
				if got[i] != c.want[i] {
					diff++
				}
			}
			if diff != 0 {
				t.Errorf("pdfium: %d pixels differ from the glyph this literal draws, want 0", diff)
			}
		})
	}
}

// TestFlagsNegativeLiteralWithSymbolicBitAgreesWithPdfium carries font_test.go's TestIrregularFlags
// row "-2^31+4 as int32, inside the range near its lower edge, symbolic bit only" through to a
// page. -2147483644 sets Table 121's symbolic bit (1<<2) and clears the nonsymbolic bit (1<<5) in
// its low 32 bits, the same as flags 4, but also sets the sign bit those two low bits alone do
// not: as a negative int64, flags&(1<<2) and flags&(1<<5) (font.go's flags(), :495) still read the
// low bits correctly, because Go's bitwise AND on a negative int64 works the same as on a
// positive one — but a mutant that also gated either bool on the sign, `flags&(1<<2) != 0 &&
// flags >= 0` for symbolic and `flags&(1<<5) != 0 || flags < 0` for nonsymbolic, would flip this
// exact value's read to nonsymbolic without font_test.go's row noticing, since that row checks
// Irregular()'s string, not which glyph flags() routes to. This renders the page instead: native
// must not refuse -2147483644 (checkFlags' range and bit rules both pass it, same as flags 4),
// and must draw the diamond swappedFlagsCFFFontPDF's built-in encoding gives a symbolic reading —
// the same glyph pdfium draws for it too (measured: flags -2147483644 native=diamond,
// pdfium=diamond).
func TestFlagsNegativeLiteralWithSymbolicBitAgreesWithPdfium(t *testing.T) {
	o := render.DefaultOptions
	o.DPI = 72

	measureNative := func(flags string) []uint8 {
		path := swappedFlagsCFFFontPDF(t, flags)
		s, err := pcstore.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = s.Close() }()
		got, err := New(s).Page(1, o)
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		px, _, _ := ink(got.Image)
		return px
	}
	measurePdfium := func(flags string) []uint8 {
		p, err := pdfium.Open(swappedFlagsCFFFontPDF(t, flags))
		if err != nil {
			t.Fatalf("pdfium.Open: %v", err)
		}
		defer func() { _ = p.Close() }()
		got, err := p.Page(1, o)
		if err != nil {
			t.Fatalf("pdfium Page: %v", err)
		}
		px, _, _ := ink(got.Image)
		return px
	}

	diamondNative, diamondPdfium := measureNative("4"), measurePdfium("4")
	gotNative, gotPdfium := measureNative("-2147483644"), measurePdfium("-2147483644")

	var diff int
	for i := range gotNative {
		if gotNative[i] != diamondNative[i] {
			diff++
		}
	}
	if diff != 0 {
		t.Errorf("this backend: %d pixels differ from flags 4's diamond, want flags -2147483644 "+
			"to draw the same glyph", diff)
	}

	diff = 0
	for i := range gotPdfium {
		if gotPdfium[i] != diamondPdfium[i] {
			diff++
		}
	}
	if diff != 0 {
		t.Errorf("pdfium: %d pixels differ from flags 4's diamond, want flags -2147483644 to draw "+
			"the same glyph", diff)
	}
}

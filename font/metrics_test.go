package font

import (
	"testing"

	"github.com/model-harness/pdftools/font/encoding"
	pdfcpufont "github.com/pdfcpu/pdfcpu/pkg/font"
)

// winAnsiGlyphs maps every code WinAnsiEncoding assigns to its glyph name, which
// is the set the standard-14 tables have to cover: a simple font omitting /Widths
// is almost always WinAnsi- or Standard-encoded, and WinAnsi is the larger.
//
// 218 codes carry 216 distinct names, because Annex D maps 0xA0 to "space" and
// 0xAD to "hyphen" — the codes Windows-1252 gives to NBSP and the soft hyphen.
// Those two are the whole reason the tables are keyed by glyph name rather than by
// code, and the reason the pdfcpu comparison below needs an exemption.
func winAnsiGlyphs(t *testing.T) map[byte]string {
	t.Helper()
	e, ok := encoding.Base("WinAnsiEncoding")
	if !ok {
		t.Fatal("WinAnsiEncoding missing from font/encoding")
	}
	out := map[byte]string{}
	names := map[string][]byte{}
	for c := 0; c < 256; c++ {
		if g := e.Glyph(byte(c)); g != "" {
			out[byte(c)] = g
			names[g] = append(names[g], byte(c))
		}
	}
	if len(out) != 218 || len(names) != 216 {
		t.Fatalf("WinAnsiEncoding assigns %d codes over %d distinct glyph names, want "+
			"218 and 216; if the encoding table changed, the width tables below cover a "+
			"different set than they claim", len(out), len(names))
	}
	// The two shared names must be exactly the Annex D departures. Any other
	// duplicate would mean the encoding table gained an aliased code, and the
	// exemption below would then be silently skipping a real comparison.
	for name, codes := range names {
		if len(codes) == 1 {
			continue
		}
		switch {
		case name == "space" && len(codes) == 2 && codes[0] == 0x20 && codes[1] == 0xA0:
		case name == "hyphen" && len(codes) == 2 && codes[0] == 0x2D && codes[1] == 0xAD:
		default:
			t.Fatalf("glyph %q is reached by codes %#x; only the Annex D space and "+
				"hyphen are expected to share a name", name, codes)
		}
	}
	return out
}

func TestCourierIsUniformlySixHundred(t *testing.T) {
	// This is the known-answer control the tables' provenance rests on. Every
	// Courier glyph is 600 by construction — it is monospaced — so any other
	// number coming back from pdfcpu is a lookup miss, not data.
	//
	// The control is what makes the Helvetica and Times numbers trustworthy:
	// pdfcpu returns 1000 for a glyph it cannot find, which is indistinguishable
	// from a real width of 1000, and several real entries in these tables are
	// exactly 1000 (Helvetica "emdash", "ellipsis", "AE"). Without a face whose
	// correct answer is known in advance, a silent miss would have been copied in
	// as if it were measured.
	glyphs := winAnsiGlyphs(t)
	for _, face := range []string{"Courier", "Courier-Bold", "Courier-Oblique", "Courier-BoldOblique"} {
		for code, glyph := range glyphs {
			w, ok := StandardWidth(face, glyph)
			if !ok {
				t.Errorf("%s: no width for code %#x (%q)", face, code, glyph)
				continue
			}
			if w != courierWidth {
				t.Errorf("%s: width for %q = %d, want %d", face, glyph, w, courierWidth)
			}
		}
	}
}

func TestStandardWidthsAgreeWithPdfcpu(t *testing.T) {
	// The tables were derived from pdfcpu's AFM data; this re-derives the
	// comparison at test time so a dependency bump that changes the metrics fails
	// here instead of silently disagreeing with the file it was generated from.
	//
	// pdfcpu is consulted by WinAnsi code, not by rune. Its lookup table is
	// code-keyed, so passing runes silently sends the whole 0x80-0x9F block — Euro,
	// the curly quotes, the dashes, OE, Scaron — to the unknown-glyph default.
	// Passing codes reaches it. That mismatch is why this comparison is expressed
	// in codes even though the tables are keyed by glyph name.
	glyphs := winAnsiGlyphs(t)
	faces := []string{
		"Helvetica", "Helvetica-Bold", "Helvetica-Oblique", "Helvetica-BoldOblique",
		"Times-Roman", "Times-Bold", "Times-Italic", "Times-BoldItalic",
		"Courier", "Courier-Bold", "Courier-Oblique", "Courier-BoldOblique",
	}
	for _, face := range faces {
		for code, glyph := range glyphs {
			mine, ok := StandardWidth(face, glyph)
			if !ok {
				t.Errorf("%s: no width for code %#x (%q)", face, code, glyph)
				continue
			}
			theirs := pdfcpufont.CharWidth(face, rune(code))

			// 0xA0 and 0xAD are exactly where PDF's Annex D departs from
			// Windows-1252: Annex D maps them to "space" and "hyphen", where
			// Windows-1252 has NBSP and a soft hyphen. pdfcpu's WinAnsi table
			// omits both, so it answers with its 1000 default. This repo's own
			// encoding tables carry them, which is why the derivation routed
			// through those rather than through pdfcpu's mapping — and the
			// Courier control above is what proves 1000 here is a miss rather
			// than a real width, since no Courier glyph is anything but 600.
			if code == 0xA0 || code == 0xAD {
				if theirs != 1000 {
					t.Errorf("%s code %#x: pdfcpu now returns %d rather than its "+
						"unknown-glyph default; the exemption below is stale", face, code, theirs)
				}
				continue
			}

			if mine != theirs {
				t.Errorf("%s code %#x (%q): table says %d, pdfcpu says %d",
					face, code, glyph, mine, theirs)
			}
		}
	}
}

func TestAnnexDDeparturesHaveRealWidths(t *testing.T) {
	// The two codes exempted above still need correct widths, since a soft hyphen
	// at a line break is a real character in body text. They come from the glyphs
	// Annex D names, which are ordinary entries in the same tables.
	for _, tc := range []struct {
		face  string
		glyph string
		want  int
	}{
		{"Helvetica", "space", 278},
		{"Helvetica", "hyphen", 333},
		{"Times-Roman", "space", 250},
		{"Times-Roman", "hyphen", 333},
	} {
		if got, ok := StandardWidth(tc.face, tc.glyph); !ok || got != tc.want {
			t.Errorf("StandardWidth(%q, %q) = %d %v, want %d true", tc.face, tc.glyph, got, ok, tc.want)
		}
	}
}

func TestStandardFaceCoverage(t *testing.T) {
	// All 14 must be recognized, or a font relying on built-in metrics loses its
	// advances. Symbol and ZapfDingbats are deliberately absent: their glyph sets
	// are not WinAnsi's, so the tables here cannot answer for them, and returning
	// a Helvetica width for a Symbol glyph would be worse than admitting no answer.
	recognized := []string{
		"Helvetica", "Helvetica-Bold", "Helvetica-Oblique", "Helvetica-BoldOblique",
		"Times-Roman", "Times-Bold", "Times-Italic", "Times-BoldItalic",
		"Courier", "Courier-Bold", "Courier-Oblique", "Courier-BoldOblique",
	}
	for _, face := range recognized {
		if !IsStandard(face) {
			t.Errorf("IsStandard(%q) = false", face)
		}
	}
	for _, face := range []string{"Symbol", "ZapfDingbats"} {
		if IsStandard(face) {
			t.Errorf("IsStandard(%q) = true, but this package has no metrics for its glyph set", face)
		}
	}
	for _, face := range []string{"", "SomeEmbeddedFont", "Helvetica-Condensed", "Garamond"} {
		if IsStandard(face) {
			t.Errorf("IsStandard(%q) = true, want false", face)
		}
	}
	// "Times" is not one of the 14 — the face is Times-Roman — but producers write
	// it, and an alias is the only thing standing between that font and no metrics
	// at all.
	if !IsStandard("Times") {
		t.Error(`IsStandard("Times") = false, but the alias table maps it to Times-Roman`)
	}
}

func TestStandardWidthResolvesAliasesAndSubsets(t *testing.T) {
	// Producers write the substituted name. Arial and Helvetica are metrically
	// compatible by design, which is what makes the substitution safe; a reader
	// that only matches canonical names loses the metrics of every Windows-produced
	// standard font.
	for _, tc := range []struct {
		name string
		want int // width of "A"
	}{
		{"Helvetica", 667},
		{"Arial", 667},
		{"ArialMT", 667},
		{"Arial,Bold", 722},
		{"Arial-BoldMT", 722},
		{"ABCDEF+Arial", 667},
		{"ABCDEF+Helvetica-Bold", 722},
		{"TimesNewRomanPSMT", 722},
		{"CourierNew", 600},
	} {
		got, ok := StandardWidth(tc.name, "A")
		if !ok || got != tc.want {
			t.Errorf("StandardWidth(%q, \"A\") = %d %v, want %d true", tc.name, got, ok, tc.want)
		}
	}
}

func TestStandardWidthReportsUnknownAsUnknown(t *testing.T) {
	// A missing answer must be distinguishable from a width, or the caller
	// substitutes a plausible number for an embedded font it knows nothing about
	// and every glyph after it is misplaced.
	for _, tc := range []struct{ face, glyph string }{
		{"NotAFont", "A"},
		{"", "A"},
		{"Helvetica", "notarealglyphname"},
		{"Helvetica", ""},
		{"Symbol", "alpha"},
	} {
		if w, ok := StandardWidth(tc.face, tc.glyph); ok {
			t.Errorf("StandardWidth(%q, %q) = %d true, want unknown", tc.face, tc.glyph, w)
		}
	}
}

func TestEveryFaceCoversTheSameGlyphSet(t *testing.T) {
	// The eight generated tables must agree on their key set. A face missing a
	// glyph the others have would fall through to the default width for that one
	// character, which is the kind of gap that shows up as a single misplaced
	// glyph in one font on one page.
	want := map[string]bool{}
	for _, glyph := range winAnsiGlyphs(t) {
		want[glyph] = true
	}
	for face, table := range standardWidths {
		if len(table) != len(want) {
			t.Errorf("%s has %d entries, want %d", face, len(table), len(want))
		}
		for glyph := range want {
			if _, ok := table[glyph]; !ok {
				t.Errorf("%s is missing %q", face, glyph)
			}
		}
	}
	if len(standardWidths) != 8 {
		t.Errorf("%d generated tables, want 8 (Courier is a constant, Symbol and "+
			"ZapfDingbats are out of scope)", len(standardWidths))
	}
}

func TestNoWidthIsNegativeOrAbsurd(t *testing.T) {
	// A width outside this range would be a transcription error rather than a
	// metric. The ceiling is above one em on purpose: Helvetica's "at" is genuinely
	// 1015 in Adobe's AFM, so a 1000 bound would reject real data. A negative
	// advance would walk the text backwards, which no standard-14 glyph does.
	for face, table := range standardWidths {
		for glyph, w := range table {
			if w <= 0 || w > 1100 {
				t.Errorf("%s %q = %d, outside 1..1100", face, glyph, w)
			}
		}
	}
}

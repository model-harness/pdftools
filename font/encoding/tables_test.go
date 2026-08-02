package encoding

import (
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// The encodings here are data, and hand-written data is where silent errors
// live: one wrong glyph name yields one wrong character in every document using
// that encoding, with no error and nothing in the output to point at the cause.
//
// These tests check the tables against golang.org/x/text/encoding/charmap,
// which derives Windows-1252 and Mac OS Roman from the Unicode Consortium's own
// mapping files. That is a genuinely independent source: it does not read this
// package, and agreement between the two means either both are right or both
// are wrong in exactly the same way, which for a mechanically generated table
// and a hand-written one is not a plausible coincidence.
//
// The comparison runs code → glyph name → rune, so it exercises the glyph list
// as much as the encoding table. A wrong rune for "endash" fails here even
// though the table itself only names the glyph.

// pdfDepartures records every code where an Annex D table intentionally
// disagrees with the platform encoding it derives from.
//
// Listing them explicitly is the point. Without this, the test could only be
// "mostly equal", which tolerates an accidental fourth departure — exactly the
// silent error the test exists to catch. Each entry states what PDF says and
// why, and an unexpected difference anywhere else fails.
var pdfDepartures = map[string]map[byte]struct {
	want   rune
	reason string
}{
	"WinAnsiEncoding": {
		0xA0: {0x0020, "Annex D specifies SPACE where Windows-1252 has NO-BREAK SPACE"},
		0xAD: {0x002D, "Annex D specifies HYPHEN where Windows-1252 has SOFT HYPHEN"},
	},
	"MacRomanEncoding": {
		0xCA: {0x0020, "Annex D specifies SPACE where Mac OS Roman has NO-BREAK SPACE"},
		0xDB: {0x00A4, "Annex D specifies CURRENCY; Mac OS Roman later put EURO here"},
	},
}

func TestBaseEncodingsAgainstCharmap(t *testing.T) {
	cases := []struct {
		encoding string
		cm       *charmap.Charmap
	}{
		{"WinAnsiEncoding", charmap.Windows1252},
		{"MacRomanEncoding", charmap.Macintosh},
	}

	for _, c := range cases {
		t.Run(c.encoding, func(t *testing.T) {
			enc, ok := Base(c.encoding)
			if !ok {
				t.Fatalf("%s is not a known base encoding", c.encoding)
			}
			departures := pdfDepartures[c.encoding]
			seen := map[byte]bool{}

			for i := 0; i < 256; i++ {
				code := byte(i)
				want := c.cm.DecodeByte(code)
				got := enc.Rune(code)

				if d, isDeparture := departures[code]; isDeparture {
					seen[code] = true
					if got != d.want {
						t.Errorf("code 0x%02X = U+%04X, want U+%04X: %s",
							code, got, d.want, d.reason)
					}
					continue
				}

				// charmap reports U+FFFD for a byte the encoding does not define.
				// Annex D leaves those codes unmapped, which this package reports
				// as rune 0.
				if want == 0xFFFD {
					if got != 0 {
						t.Errorf("code 0x%02X = U+%04X, but %s leaves it undefined",
							code, got, c.encoding)
					}
					continue
				}

				// Control characters. charmap maps them to themselves because it
				// describes a byte encoding; Annex D names no glyph for them because
				// it describes a glyph encoding, and a control character has no
				// glyph. 0x7F is DEL, which belongs to this group despite sitting
				// above the printable range.
				if code < 0x20 || code == 0x7F {
					continue
				}

				if got != want {
					t.Errorf("code 0x%02X = U+%04X (glyph %q), want U+%04X",
						code, got, enc.Glyph(code), want)
				}
			}

			// A departure that no longer differs means either the table changed or
			// charmap did, and in both cases the comment above it is now false.
			for code, d := range departures {
				if !seen[code] {
					t.Errorf("declared departure at 0x%02X was never reached: %s", code, d.reason)
				}
				if want := c.cm.DecodeByte(code); want == d.want {
					t.Errorf("code 0x%02X no longer departs from %s (both U+%04X): remove the exception",
						code, c.encoding, want)
				}
			}
		})
	}
}

func TestEveryBaseEncodingGlyphResolves(t *testing.T) {
	// A glyph name in a base table that the glyph list does not know is a hole:
	// the code maps to a name and then to nothing, so the character silently
	// disappears. Every name these tables use must resolve.
	for name := range baseTables {
		enc, ok := Base(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		for i := 0; i < 256; i++ {
			glyph := enc.Glyph(byte(i))
			if glyph == "" {
				continue
			}
			if _, ok := GlyphText(glyph); !ok {
				t.Errorf("%s code 0x%02X names glyph %q, which does not resolve to any text",
					name, i, glyph)
			}
		}
	}
}

func TestStandardEncodingQuoteCodes(t *testing.T) {
	// StandardEncoding predates the ASCII convention at these two codes, and
	// treating them as ASCII is a common bug: text comes out with vertical
	// typewriter marks where the document has typographic quotes, or the reverse.
	enc := Standard()
	cases := []struct {
		code  byte
		glyph string
		want  rune
	}{
		{0x27, "quoteright", 0x2019},
		{0x60, "quoteleft", 0x2018},
	}
	for _, c := range cases {
		if g := enc.Glyph(c.code); g != c.glyph {
			t.Errorf("code 0x%02X names %q, want %q", c.code, g, c.glyph)
		}
		if r := enc.Rune(c.code); r != c.want {
			t.Errorf("code 0x%02X = U+%04X, want U+%04X", c.code, r, c.want)
		}
	}

	// WinAnsi and MacRoman use the ASCII forms at the same codes, so the
	// difference is real rather than a table-wide convention.
	for _, name := range []string{"WinAnsiEncoding", "MacRomanEncoding"} {
		other, _ := Base(name)
		if r := other.Rune(0x27); r != '\'' {
			t.Errorf("%s code 0x27 = U+%04X, want U+0027", name, r)
		}
		if r := other.Rune(0x60); r != '`' {
			t.Errorf("%s code 0x60 = U+%04X, want U+0060", name, r)
		}
	}
}

func TestSharedASCIIRange(t *testing.T) {
	// The printable ASCII range must be identical across the Latin encodings
	// apart from the two StandardEncoding quote codes. A divergence would mean a
	// typo in one of the tables, and ordinary letters are where such a typo does
	// the most damage.
	win, _ := Base("WinAnsiEncoding")
	mac, _ := Base("MacRomanEncoding")
	std := Standard()
	pdfDoc, _ := Base("PDFDocEncoding")

	for i := 0x20; i <= 0x7E; i++ {
		code := byte(i)
		want := rune(i)
		for _, e := range []struct {
			name string
			enc  *Encoding
		}{
			{"WinAnsiEncoding", win},
			{"MacRomanEncoding", mac},
			{"PDFDocEncoding", pdfDoc},
		} {
			if got := e.enc.Rune(code); got != want {
				t.Errorf("%s code 0x%02X = U+%04X, want U+%04X", e.name, code, got, want)
			}
		}
		if code == 0x27 || code == 0x60 {
			continue
		}
		if got := std.Rune(code); got != want {
			t.Errorf("StandardEncoding code 0x%02X = U+%04X, want U+%04X", code, got, want)
		}
	}
}

func TestMacExpertEncodingIsReportedAbsent(t *testing.T) {
	// Not implemented, and the caller must be able to tell. Returning a
	// half-populated table would produce wrong characters for exactly the expert
	// glyphs the encoding exists to address.
	if _, ok := Base("MacExpertEncoding"); ok {
		t.Error("MacExpertEncoding reports as known but has no table")
	}
	if _, ok := Base("NotAnEncoding"); ok {
		t.Error("an invented encoding name reported as known")
	}
}

func TestBaseReturnsIndependentCopies(t *testing.T) {
	// Differences are applied by mutating an Encoding, so two callers asking for
	// the same base encoding must not share state. If they did, one font's
	// /Differences would silently rewrite every other font using that encoding —
	// a corruption that would appear only in documents with several fonts.
	a, _ := Base("WinAnsiEncoding")
	b, _ := Base("WinAnsiEncoding")

	a.Set('A', "bullet")
	if got := b.Glyph('A'); got != "A" {
		t.Fatalf("mutating one Encoding changed another: code 'A' is now %q", got)
	}
	if got := a.Rune('A'); got != 0x2022 {
		t.Errorf("Set did not update the rune: got U+%04X, want U+2022", got)
	}

	c := b.Clone()
	c.Set('B', "dagger")
	if got := b.Glyph('B'); got != "B" {
		t.Fatalf("mutating a clone changed its source: code 'B' is now %q", got)
	}
}

package font

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/cffbuild"
)

// noop is a filler charstring for fixtures that only care about charset, encoding, or FDSelect
// lookups and never call Outline.
var noop = cffbuild.CS(cffbuild.EndChar)

// dict builds a cffDict literal for a test that wants specific operands without going through
// parseDict, since cffDict now also tracks which operators had a real-form operand.
func dict(m map[int][]float64) cffDict { return cffDict{v: m} }

// namedGlyphs returns n glyphs named "g0".."g(n-1)", each Build assigns a fresh, consecutive SID
// (391 + its position) to, per cffbuild.Glyph's own doc — which is what lets a single long charset
// run be built without spelling out 300 explicit SIDs by hand.
func namedGlyphs(n int) []cffbuild.Glyph {
	g := make([]cffbuild.Glyph, n)
	for i := range g {
		g[i] = cffbuild.Glyph{Name: fmt.Sprintf("g%d", i), CharString: noop}
	}
	return g
}

// TestCharsetFormatsGiveTheRightGID pins that each charset format (TN #5176 §13) gives the right
// GID for a name (name-keyed) and for a CID (CID-keyed).
func TestCharsetFormatsGiveTheRightGID(t *testing.T) {
	for _, format := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("name-keyed, format %d", format), func(t *testing.T) {
			b := cffbuild.Builder{CharsetFormat: format, Glyphs: []cffbuild.Glyph{
				{SID: 34, CharString: noop}, // "A", TN #5176 Appendix A
				{SID: 35, CharString: noop}, // "B"
				{SID: 36, CharString: noop}, // "C"
			}}
			cff, err := ParseCFF(b.Build())
			if err != nil {
				t.Fatalf("ParseCFF: %v", err)
			}
			for _, c := range []struct {
				name string
				gid  uint16
			}{{"A", 1}, {"B", 2}, {"C", 3}} {
				if gid, ok := cff.GIDForName(c.name); !ok || gid != c.gid {
					t.Errorf("GIDForName(%q) = %d, %v; want %d, true", c.name, gid, ok, c.gid)
				}
			}
		})
		t.Run(fmt.Sprintf("CID-keyed, format %d", format), func(t *testing.T) {
			b := cffbuild.Builder{CID: true, CharsetFormat: format, Glyphs: []cffbuild.Glyph{
				{CID: 100, CharString: noop},
				{CID: 200, CharString: noop},
				{CID: 300, CharString: noop},
			}}
			cff, err := ParseCFF(b.Build())
			if err != nil {
				t.Fatalf("ParseCFF: %v", err)
			}
			for _, c := range []struct {
				cid uint32
				gid uint16
			}{{100, 1}, {200, 2}, {300, 3}} {
				if gid, ok := cff.GIDForCID(c.cid); !ok || gid != c.gid {
					t.Errorf("GIDForCID(%d) = %d, %v; want %d, true", c.cid, gid, ok, c.gid)
				}
			}
		})
	}
}

// TestCharsetFormat2TwoByteNLeft is the regression TN #5176 §13's Range2 exists to prevent: format
// 2's nLeft field is a Card16, not the Card8 format 1 uses, so a run of 300 consecutive glyphs
// fits in a single range with nLeft 299 — which a one-byte reader would truncate to 299 mod 256
// (43) and misplace every glyph from GID 301 on. Named glyphs mint consecutive SIDs (391 up), so
// this needs no explicit SID table.
func TestCharsetFormat2TwoByteNLeft(t *testing.T) {
	const n = 300
	b := cffbuild.Builder{CharsetFormat: 2, Glyphs: namedGlyphs(n)}
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	for _, i := range []int{0, 150, n - 1} {
		want := uint16(i + 1) // #nosec G115 -- i < 300
		if gid, ok := cff.GIDForName(fmt.Sprintf("g%d", i)); !ok || gid != want {
			t.Errorf("GIDForName(%q) = %d, %v; want %d, true", fmt.Sprintf("g%d", i), gid, ok, want)
		}
	}
}

// TestCharsetFormat1SplitAt255 pins that format 1's one-byte nLeft (TN #5176 §13's Range1) makes a
// 300-glyph consecutive run into two ranges, split after 256 glyphs (nLeft 255), and that both the
// boundary and the range after it read back correctly.
func TestCharsetFormat1SplitAt255(t *testing.T) {
	const n = 300
	b := cffbuild.Builder{CharsetFormat: 1, Glyphs: namedGlyphs(n)}
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	for _, i := range []int{0, 255, 256, n - 1} {
		want := uint16(i + 1) // #nosec G115 -- i < 300
		if gid, ok := cff.GIDForName(fmt.Sprintf("g%d", i)); !ok || gid != want {
			t.Errorf("GIDForName(%q) = %d, %v; want %d, true", fmt.Sprintf("g%d", i), gid, ok, want)
		}
	}
}

// TestCharsetSIDOverflowIsTrimmed pins the FreeType reading cff.go's loadCharset comment
// documents: a range whose last SID would pass 65535 is trimmed to end there. first=65530,
// nLeft=10 asks for SIDs 65530..65540; trimmed to 65530..65535 (6 ids), which exactly covers 6
// glyphs (GIDs 1-6) with numGlyphs 7, so the range — and the font — is well formed once trimmed.
func TestCharsetSIDOverflowIsTrimmed(t *testing.T) {
	// Offset 0 (and 1, 2) name a predefined charset regardless of what bytes sit there, so the
	// format-1 table under test starts at offset 3, behind three padding bytes.
	c := &CFF{data: []byte{0, 0, 0, 1, 0xFF, 0xFA, 10}, numGlyphs: 7}
	if err := c.loadCharset(3); err != nil {
		t.Fatalf("loadCharset: %v", err)
	}
	for gid := 1; gid <= 6; gid++ {
		want := uint16(65530 + gid - 1) // #nosec G115 -- 65530+5 = 65535, in range
		if got := c.charset[gid]; got != want {
			t.Errorf("charset[%d] = %d, want %d", gid, got, want)
		}
	}
}

// TestCharsetDuplicateResolvesToLowestGID pins that a name (via a duplicated standard SID) or a
// CID claimed by two glyphs resolves to the lower GID (FreeType's reading, per cff.go's comment).
func TestCharsetDuplicateResolvesToLowestGID(t *testing.T) {
	t.Run("name", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			{SID: 34, CharString: noop}, // "A" at GID 1
			{SID: 34, CharString: noop}, // "A" again at GID 2
		}}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		if gid, ok := cff.GIDForName("A"); !ok || gid != 1 {
			t.Errorf("GIDForName(A) = %d, %v; want 1, true", gid, ok)
		}
	})
	t.Run("CID", func(t *testing.T) {
		b := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{
			{CID: 5, CharString: noop},
			{CID: 5, CharString: noop},
		}}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		if gid, ok := cff.GIDForCID(5); !ok || gid != 1 {
			t.Errorf("GIDForCID(5) = %d, %v; want 1, true", gid, ok)
		}
	})
	t.Run("name, including GID 0", func(t *testing.T) {
		// GID 0's charset entry is always SID 0, .notdef, so the downward walk must reach GID 0
		// or a later glyph that also names .notdef wins.
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			{SID: 0, CharString: noop}, // .notdef again, at GID 1
		}}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		if gid, ok := cff.GIDForName(".notdef"); !ok || gid != 0 {
			t.Errorf(`GIDForName(".notdef") = %d, %v; want 0, true (GID 0 is the lower one)`, gid, ok)
		}
	})
	t.Run("CID, including GID 0", func(t *testing.T) {
		// The same for a CID-keyed font, where GID 0's charset entry is always CID 0.
		b := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{
			{CID: 0, CharString: noop}, // CID 0 again, at GID 1
		}}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		if gid, ok := cff.GIDForCID(0); !ok || gid != 0 {
			t.Errorf("GIDForCID(0) = %d, %v; want 0, true (GID 0 is the lower one)", gid, ok)
		}
	})
}

// TestCheckCharsetNames pins C8: a custom charset SID (391 or more) whose String INDEX entry is
// empty, or contains a NUL, is refused — FreeType's ft_strcmp-based name comparison (cffload.c,
// cff_get_name_index) would not read it the way sidString does — while a SID past the String
// INDEX (no name in either reading) and an ordinary custom name are both accepted.
func TestCheckCharsetNames(t *testing.T) {
	t.Run("an empty custom string is refused", func(t *testing.T) {
		// String INDEX: count 1, offSize 1, offsets [1, 1] (an empty object). charset names SID
		// 391 (the first custom string) for GID 1.
		c := &CFF{data: []byte{0, 1, 1, 1, 1}, numGlyphs: 2, charset: []uint16{0, 391}}
		var err error
		if c.strs, err = c.index(0); err != nil {
			t.Fatalf("index: %v", err)
		}
		if err := c.checkCharsetNames(); err == nil {
			t.Error("checkCharsetNames succeeded with an empty custom string, want a refusal")
		}
	})
	t.Run("a custom string containing a NUL is refused", func(t *testing.T) {
		// String INDEX: count 1, offSize 1, offsets [1, 4] (a 3-byte object, "A\x00x"). charset
		// names SID 391 for GID 1.
		c := &CFF{data: []byte{0, 1, 1, 1, 4, 'A', 0, 'x'}, numGlyphs: 2, charset: []uint16{0, 391}}
		var err error
		if c.strs, err = c.index(0); err != nil {
			t.Fatalf("index: %v", err)
		}
		if err := c.checkCharsetNames(); err == nil {
			t.Error("checkCharsetNames succeeded with a NUL in a custom string, want a refusal")
		}
	})
	t.Run("a SID past the String INDEX has no name and is accepted", func(t *testing.T) {
		c := &CFF{numGlyphs: 2, charset: []uint16{0, 500}} // c.strs is the zero value: count 0
		if err := c.checkCharsetNames(); err != nil {
			t.Errorf("checkCharsetNames = %v, want success (no name past the String INDEX)", err)
		}
	})
	t.Run("an ordinary custom name is accepted", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{Name: "Q", CharString: noop}}}
		if _, err := ParseCFF(b.Build()); err != nil {
			t.Errorf("ParseCFF: %v, want success", err)
		}
	})
	t.Run("a standard SID below 391 is never checked against the String INDEX", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{SID: 34, CharString: noop}}} // "A"
		if _, err := ParseCFF(b.Build()); err != nil {
			t.Errorf("ParseCFF: %v, want success", err)
		}
	})
}

// TestC19EmptyCustomNameEndToEnd pins C19's own scenario at the font level: a nonsymbolic font
// whose charset gives a glyph an empty custom name is refused at parse, which is what keeps
// render/native's GIDForName("") — built from that same empty string — from ever running for such
// a font. TestCheckCharsetNames above pins the rule directly; this pins that ParseCFF applies it.
func TestC19EmptyCustomNameEndToEnd(t *testing.T) {
	b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{EmptyName: true, CharString: noop}}}
	if _, err := ParseCFF(b.Build()); err == nil {
		t.Error("ParseCFF succeeded with an empty custom glyph name, want a refusal")
	}
}

// TestISOAdobeCharset pins the predefined ISOAdobe charset (offset 0): GID i's SID is i (TN #5176
// Appendix C names GID 1 "space" (SID 1) and GID 2 "exclam" (SID 2), TN #5176 Appendix A), and a
// font of more than 229 glyphs naming it is refused because ISOAdobe has only 229 (.notdef plus
// SIDs 1-228).
func TestISOAdobeCharset(t *testing.T) {
	t.Run("GID i is SID i", func(t *testing.T) {
		b := cffbuild.Builder{ISOAdobe: true, Glyphs: []cffbuild.Glyph{
			{CharString: noop}, // GID 1 -> SID 1 "space"
			{CharString: noop}, // GID 2 -> SID 2 "exclam"
		}}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		if gid, ok := cff.GIDForName("space"); !ok || gid != 1 {
			t.Errorf(`GIDForName("space") = %d, %v; want 1, true`, gid, ok)
		}
		if gid, ok := cff.GIDForName("exclam"); !ok || gid != 2 {
			t.Errorf(`GIDForName("exclam") = %d, %v; want 2, true`, gid, ok)
		}
	})
	t.Run("more than 229 glyphs is refused", func(t *testing.T) {
		glyphs := make([]cffbuild.Glyph, 230)
		for i := range glyphs {
			glyphs[i] = cffbuild.Glyph{CharString: noop}
		}
		b := cffbuild.Builder{ISOAdobe: true, Glyphs: glyphs}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with 231 glyphs naming ISOAdobe, want a refusal")
		}
	})
	t.Run("exactly 229 glyphs (GID 0 plus 228) is accepted", func(t *testing.T) {
		glyphs := make([]cffbuild.Glyph, 228)
		for i := range glyphs {
			glyphs[i] = cffbuild.Glyph{CharString: noop}
		}
		b := cffbuild.Builder{ISOAdobe: true, Glyphs: glyphs}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v, want success with exactly 229 glyphs", err)
		}
		if got := cff.NumGlyphs(); got != 229 {
			t.Fatalf("NumGlyphs = %d, want 229", got)
		}
	})
	t.Run("230 glyphs is refused", func(t *testing.T) {
		glyphs := make([]cffbuild.Glyph, 229)
		for i := range glyphs {
			glyphs[i] = cffbuild.Glyph{CharString: noop}
		}
		b := cffbuild.Builder{ISOAdobe: true, Glyphs: glyphs}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with 230 glyphs naming ISOAdobe, want a refusal")
		}
	})
}

// TestExpertCharsetIsRefused pins that the predefined Expert and ExpertSubset charsets (offsets 1
// and 2, TN #5176 Table 22) are refused. cffbuild.Builder has no field for them — a predefined
// offset is not a table this package can build — so TopExtra overrides Build's own charset
// operator with the literal offset (parseDict keeps the last operator of a repeated key).
// TestEncodingStandardMapsThroughCharset pins that the predefined Standard encoding (offset 0)
// maps a code through its StandardEncoding name to whichever glyph the charset gives that name's
// SID (TN #5176 Appendix B), and answers false for a code whose name this subset's charset does
// not carry.
func TestEncodingStandardMapsThroughCharset(t *testing.T) {
	b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{
		{SID: 34, CharString: noop}, // "A", code 65
		{SID: 8, CharString: noop},  // "quoteright", code 39 (0x27)
	}}
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	if gid, ok := cff.GIDForCode(65); !ok || gid != 1 {
		t.Errorf("GIDForCode(65) = %d, %v; want 1, true", gid, ok)
	}
	if gid, ok := cff.GIDForCode(0x27); !ok || gid != 2 {
		t.Errorf("GIDForCode(0x27) = %d, %v; want 2, true", gid, ok)
	}
	// Code 66 is "B" (SID 35, TN #5176 Appendix A) in StandardEncoding, and this font has no
	// glyph with that SID.
	if gid, ok := cff.GIDForCode(66); ok {
		t.Errorf("GIDForCode(66) = %d, true; want false: no glyph in this subset is named \"B\"", gid)
	}
}

// TestEncodingFormatsKeepTheLastGID pins that a code assigned twice keeps the last GID the
// encoding data wrote, in both custom formats.
func TestEncodingFormatsKeepTheLastGID(t *testing.T) {
	for _, format := range []int{0, 1} {
		t.Run(fmt.Sprintf("format %d", format), func(t *testing.T) {
			b := cffbuild.Builder{
				Codes:          []byte{65, 65}, // GID 1 and GID 2 both claim code 65
				EncodingFormat: format,
				Glyphs:         []cffbuild.Glyph{{CharString: noop}, {CharString: noop}},
			}
			cff, err := ParseCFF(b.Build())
			if err != nil {
				t.Fatalf("ParseCFF: %v", err)
			}
			if gid, ok := cff.GIDForCode(65); !ok || gid != 2 {
				t.Errorf("GIDForCode(65) = %d, %v; want 2, true (the last GID written)", gid, ok)
			}
		})
	}
}

// TestEncodingFormat0ExtraCodeIsUnassigned pins that a format 0 encoding naming one more code
// than there are real glyphs leaves the extra code unassigned, rather than pointing it one GID
// past the last real glyph.
func TestEncodingFormat0ExtraCodeIsUnassigned(t *testing.T) {
	b := cffbuild.Builder{
		Codes:  []byte{65, 66}, // one more code (66) than the single real glyph
		Glyphs: []cffbuild.Glyph{{CharString: noop}},
	}
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	if gid, ok := cff.GIDForCode(65); !ok || gid != 1 {
		t.Errorf("GIDForCode(65) = %d, %v; want 1, true", gid, ok)
	}
	if gid, ok := cff.GIDForCode(66); ok {
		t.Errorf("GIDForCode(66) = %d, true; want false: the extra code names GID %d, past the last real glyph", gid, gid)
	}
}

// TestEncodingSupplements pins three things about a supplement (TN #5176 Table 15): it resolves
// its SID through the charset, one naming a SID no glyph has leaves the code as the main table
// set it (rather than clearing it), and one naming SID 0 (.notdef, always present at GID 0)
// overwrites the code to "no glyph" — a different outcome from "untouched" that a supplement
// resolving successfully to GID 0 has to produce.
func TestEncodingSupplements(t *testing.T) {
	b := cffbuild.Builder{
		Codes: []byte{65, 70}, // GID 1 -> code 65, GID 2 -> code 70
		Supplements: []cffbuild.Supplement{
			{Code: 70, SID: 9999}, // no glyph has SID 9999: code 70 keeps GID 2
			{Code: 72, SID: 0},    // .notdef: code 72 becomes "no glyph"
		},
		Glyphs: []cffbuild.Glyph{{CharString: noop}, {CharString: noop}},
	}
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	if gid, ok := cff.GIDForCode(70); !ok || gid != 2 {
		t.Errorf(`GIDForCode(70) = %d, %v; want 2, true (the main table's assignment survives)`, gid, ok)
	}
	if gid, ok := cff.GIDForCode(72); ok {
		t.Errorf("GIDForCode(72) = %d, true; want false (a SID-0 supplement names .notdef)", gid)
	}
}

// TestEncodingFormat1ZeroRangesBuildsNoCmap pins C3/C18: a format-1 built-in encoding with zero
// ranges leaves FreeType's encoding->count at 0 (cffload.c, cff_encoding_load), so cff_face_init
// builds no Adobe custom cmap for it (cffobjs.c, "if (encoding->count > 0)") — a symbolic font's
// code lookup falls through to a route this package does not model, so the code must come back
// unassigned rather than through a supplement, even though the supplement itself still has to be
// read (a truncated one is still refused). Format 0 with zero codes is unaffected: its count is
// always at least 1 (n+1), so its supplements apply normally, the control below.
func TestEncodingFormat1ZeroRangesBuildsNoCmap(t *testing.T) {
	t.Run("format 1, zero ranges: a supplement assigns nothing", func(t *testing.T) {
		b := cffbuild.Builder{
			EncodingFormat: 1,
			Supplements:    []cffbuild.Supplement{{Code: 'A', SID: sidB}},
			Glyphs:         []cffbuild.Glyph{{SID: sidB, CharString: noop}},
		}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		if gid, ok := cff.GIDForCode('A'); ok {
			t.Errorf("GIDForCode('A') = %d, true; want false: format 1 with zero ranges builds no cmap", gid)
		}
	})
	t.Run("format 0, zero codes: a supplement still assigns", func(t *testing.T) {
		b := cffbuild.Builder{
			EncodingFormat: 0,
			Supplements:    []cffbuild.Supplement{{Code: 'A', SID: sidB}},
			Glyphs:         []cffbuild.Glyph{{SID: sidB, CharString: noop}},
		}
		cff, err := ParseCFF(b.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		if gid, ok := cff.GIDForCode('A'); !ok || gid != 1 {
			t.Errorf("GIDForCode('A') = %d, %v; want 1, true: format 0's count is always at least 1", gid, ok)
		}
	})
	t.Run("format 1, zero ranges: a truncated supplement array is still refused", func(t *testing.T) {
		// Two bytes of padding (0 and 1 are the predefined encodings to loadEncoding's own off),
		// then format 0x81 (format 1, supplements present) at off 2, 0 ranges, 1 supplement — but
		// only 1 of its 3 bytes: still read (and so still refused) even though noCmap means it
		// will assign nothing.
		c := &CFF{data: []byte{0, 0, 0x81, 0, 1, 0}, numGlyphs: 2}
		if err := c.loadEncoding(2); err == nil {
			t.Error("loadEncoding succeeded with a truncated supplement, want a refusal")
		}
	})
}

// sidB is the standard string ID of "B" (TN #5176 Appendix A), for encoding fixtures above that
// need a real glyph a supplement can name.
const sidB = 35

// TestExpertEncodingIsRefused pins that the predefined Expert encoding (offset 1) is refused.
// cffbuild.Builder has no field for it, so TopExtra overrides Build's own encoding operator.
func TestExpertEncodingIsRefused(t *testing.T) {
	b := cffbuild.Builder{
		Glyphs:   []cffbuild.Glyph{{SID: 34, CharString: noop}},
		TopExtra: append(cffbuild.DictInt(1), 16), // Encoding (op 16) = offset 1
	}
	if _, err := ParseCFF(b.Build()); err == nil {
		t.Error("ParseCFF succeeded with the predefined Expert encoding, want a refusal")
	}
}

// TestGIDForCodeIsFalseForGID0 pins that GIDForCode answers false rather than 0 for a code the
// encoding leaves unassigned. Code 0 is .notdef in StandardEncoding (TN #5176 Appendix B), so no
// font using it ever assigns code 0 to anything.
func TestGIDForCodeIsFalseForGID0(t *testing.T) {
	b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{SID: 34, CharString: noop}}}
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	if gid, ok := cff.GIDForCode(0); ok {
		t.Errorf("GIDForCode(0) = %d, true; want false", gid)
	}
}

// TestGIDForCIDOnANonCIDFont pins that GIDForCID on a font that is not CID-keyed is the identity
// below NumGlyphs, and false at or beyond it.
func TestGIDForCIDOnANonCIDFont(t *testing.T) {
	b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}, {CharString: noop}}}
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	if got := cff.NumGlyphs(); got != 3 {
		t.Fatalf("NumGlyphs = %d, want 3", got)
	}
	for _, cid := range []uint32{0, 1, 2} {
		if gid, ok := cff.GIDForCID(cid); !ok || uint32(gid) != cid {
			t.Errorf("GIDForCID(%d) = %d, %v; want %d, true", cid, gid, ok, cid)
		}
	}
	if gid, ok := cff.GIDForCID(3); ok {
		t.Errorf("GIDForCID(3) = %d, true; want false: 3 is not less than NumGlyphs (3)", gid)
	}
}

// defaultFontMatrix is the Top DICT's default (TN #5176 Table 9), for calling loadCIDFonts and
// fontDict directly.
var defaultFontMatrix = [6]float64{0.001, 0, 0, 0.001, 0, 0}

// TestFDSelectRoutesGlyphsToTheirFD pins that FDSelect formats 0 and 3 (TN #5176 §19) route each
// glyph to its own Font DICT, observed through each FD's own local Subrs: both glyphs run the
// identical charstring "0 0 rmoveto <subr 0> callsubr", and only which FD's Subrs INDEX subr 0
// comes from tells them apart.
func TestFDSelectRoutesGlyphsToTheirFD(t *testing.T) {
	glyphCS := cffbuild.CS(0, 0, cffbuild.RMoveTo, -107, cffbuild.CallSubr, cffbuild.EndChar)
	for _, format := range []int{0, 3} {
		t.Run(fmt.Sprintf("format %d", format), func(t *testing.T) {
			b := cffbuild.Builder{
				CID: true,
				FDs: []cffbuild.FontDict{
					{Subrs: [][]byte{cffbuild.CS(1, 0, cffbuild.RLineTo, cffbuild.Return)}},
					{Subrs: [][]byte{cffbuild.CS(2, 0, cffbuild.RLineTo, cffbuild.Return)}},
				},
				FDSelectFormat: format,
				Glyphs: []cffbuild.Glyph{
					{CID: 1, FD: 0, CharString: glyphCS},
					{CID: 2, FD: 1, CharString: glyphCS},
				},
			}
			cff, err := ParseCFF(b.Build())
			if err != nil {
				t.Fatalf("ParseCFF: %v", err)
			}
			out1, _, err := cff.Outline(1, 1000)
			if err != nil {
				t.Fatalf("Outline(1): %v", err)
			}
			want1 := Outline{{Op: SegMove, P: [3]Point{{0, 0}}}, {Op: SegLine, P: [3]Point{{1, 0}}}, {Op: SegClose}}
			if !sameOutline(out1, want1) {
				t.Errorf("GID 1 outline = %v\nwant             %v (FD 0's subr)", out1, want1)
			}
			out2, _, err := cff.Outline(2, 1000)
			if err != nil {
				t.Fatalf("Outline(2): %v", err)
			}
			want2 := Outline{{Op: SegMove, P: [3]Point{{0, 0}}}, {Op: SegLine, P: [3]Point{{2, 0}}}, {Op: SegClose}}
			if !sameOutline(out2, want2) {
				t.Errorf("GID 2 outline = %v\nwant             %v (FD 1's subr)", out2, want2)
			}
		})
	}
}

// TestFDSelectRefusals pins loadFDSelect's refusals directly: format 3 ranges must start at glyph
// 0 and cover every glyph with no gap, and every FD index named must exist in the FDArray.
func TestFDSelectRefusals(t *testing.T) {
	t.Run("format 3 ranges not contiguous from glyph 0", func(t *testing.T) {
		// format 3, 1 range: first=1 (not 0), fd=0, sentinel=2. Offset 0 (and 1, 2) would name a
		// predefined charset if this were loadCharset, but loadFDSelect has no such cases, so the
		// table can start right at offset 0.
		data := []byte{3, 0, 1, 0, 1, 0, 0, 2}
		c := &CFF{data: data, numGlyphs: 2, fds: make([]cffFD, 1)}
		if err := c.loadFDSelect(0); err == nil {
			t.Error("loadFDSelect succeeded, want a refusal: the first range must start at 0")
		}
	})
	t.Run("a gap leaves glyphs uncovered", func(t *testing.T) {
		// format 3, 1 range: first=0, fd=0, sentinel=2 -- but numGlyphs is 3, so glyph 2 is never
		// covered.
		data := []byte{3, 0, 1, 0, 0, 0, 0, 2}
		c := &CFF{data: data, numGlyphs: 3, fds: make([]cffFD, 1)}
		if err := c.loadFDSelect(0); err == nil {
			t.Error("loadFDSelect succeeded, want a refusal: glyph 2 is not covered by any range")
		}
	})
	t.Run("an FD index past the FDArray is refused", func(t *testing.T) {
		// format 0: one FD byte, naming FD 5, with only 2 Font DICTs in fds.
		data := []byte{0, 5}
		c := &CFF{data: data, numGlyphs: 1, fds: make([]cffFD, 2)}
		if err := c.loadFDSelect(0); err == nil {
			t.Error("loadFDSelect succeeded, want a refusal: FD 5 is past the 2-entry FDArray")
		}
	})
	t.Run("a zero-width range is refused", func(t *testing.T) {
		// format 3, 1 range: first=0, fd=0, sentinel=0 (next == first): a range covering no
		// glyphs at all, rather than one glyph short of it.
		data := []byte{3, 0, 1, 0, 0, 0, 0, 0}
		c := &CFF{data: data, numGlyphs: 1, fds: make([]cffFD, 1)}
		err := c.loadFDSelect(0)
		if err == nil || !strings.Contains(err.Error(), "not contiguous") {
			t.Errorf("loadFDSelect = %v, want a refusal naming \"not contiguous\"", err)
		}
	})
}

// TestCIDFontMissingFDArrayOrFDSelect pins loadCIDFonts' refusals for a CID-keyed font missing
// either table, and for an FDArray of zero Font DICTs, calling it directly rather than assembling
// a whole font around the malformed piece.
func TestCIDFontMissingFDArrayOrFDSelect(t *testing.T) {
	t.Run("no FDArray operator", func(t *testing.T) {
		c := &CFF{}
		if err := c.loadCIDFonts(dict(nil), defaultFontMatrix, false); err == nil {
			t.Error("loadCIDFonts succeeded, want a refusal for a missing FDArray")
		}
	})
	t.Run("FDArray of zero Font DICTs", func(t *testing.T) {
		c := &CFF{data: []byte{0, 0}} // an INDEX with count 0
		if err := c.loadCIDFonts(dict(map[int][]float64{opFDArray: {0}}), defaultFontMatrix, false); err == nil {
			t.Error("loadCIDFonts succeeded, want a refusal for an empty FDArray")
		}
	})
	t.Run("no FDSelect operator", func(t *testing.T) {
		// An FDArray INDEX with one empty Font DICT (count 1, offSize 1, offsets [1,1]: an
		// empty object), so loadCIDFonts gets past the FDArray and reaches the FDSelect check.
		c := &CFF{data: []byte{0, 1, 1, 1, 1}}
		err := c.loadCIDFonts(dict(map[int][]float64{opFDArray: {0}}), defaultFontMatrix, false)
		if err == nil {
			t.Error("loadCIDFonts succeeded, want a refusal for a missing FDSelect")
		}
	})
}

// TestROSRegistry65535IsNameKeyed pins C2: ROS marks a font CID-keyed only when its registry
// (operand 0) is not 65535 (FreeType tests cid_registry != 0xFFFF, not merely ROS's presence).
// TopExtra overrides the Builder's own ROS operator (parseDict keeps the last of a repeated key),
// so the font parses as CID-keyed data — a charset of raw CIDs, an FDArray and FDSelect — but with
// a registry of 65535, the reading throughout.
func TestROSRegistry65535IsNameKeyed(t *testing.T) {
	b := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{
		{CID: 5, CharString: noop},
		{CID: 1, CharString: noop},
	}}
	var extra []byte
	extra = append(extra, cffbuild.DictInt(65535)...) // registry
	extra = append(extra, cffbuild.DictInt(0)...)     // ordering
	extra = append(extra, cffbuild.DictInt(0)...)     // supplement
	b.TopExtra = append(extra, 12, 30)                // ROS: escape 12, byte 30 (1230)
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	if cff.CIDKeyed() {
		t.Error("CIDKeyed() = true, want false: a registry of 65535 is name-keyed")
	}
	// Not CID-keyed, so GIDForCID is the identity below NumGlyphs: CID 1 is glyph 1, not the glyph
	// whose charset entry happens to be CID 1.
	if gid, ok := cff.GIDForCID(1); !ok || gid != 1 {
		t.Errorf("GIDForCID(1) = %d, %v; want 1, true", gid, ok)
	}
}

// TestROSOperandCount pins that checkTopOccurrence's ROS case refuses an operand count other than
// 3, as FreeType's cff_parse_cid_ros (cffparse.c:874) fails the font on anything else. rosCID
// itself no longer checks this: it is reached only through parseDict(dictTop), which runs
// checkTopOccurrence at every occurrence of ROS before ever storing one in d.v (see parseDict).
func TestROSOperandCount(t *testing.T) {
	for _, n := range []int{2, 4} {
		t.Run(fmt.Sprintf("%d operands", n), func(t *testing.T) {
			b := append(dictInts(make([]int32, n)...), opBytes(opROS)...)
			if _, err := parseDict(b, dictTop); err == nil {
				t.Errorf("parseDict succeeded with an ROS operator with %d operands, want a refusal", n)
			}
		})
	}
}

// TestFontDictROSSkipsPrivate pins C7's Font-DICT case: a Font DICT that itself carries ROS with
// a registry other than 65535 is CID-keyed by rosCID's own rule, and FreeType skips loading its
// Private DICT in that case (cffload.c, cff_subfont_load: "if top->cid_registry != 0xFFFF goto
// Exit" before the Private DICT). The Private operand here (100, 5) would fail as outside a
// 10-byte program if fontDict tried to read it at all.
func TestFontDictROSSkipsPrivate(t *testing.T) {
	c := &CFF{data: make([]byte, 10)}
	d := dict(map[int][]float64{
		opROS:     {0, 0, 0}, // registry 0 (not 65535): CID-keyed
		opPrivate: {100, 5},
	})
	fd, err := c.fontDict(d, defaultFontMatrix, false)
	if err != nil {
		t.Fatalf("fontDict: %v, want success: a Font DICT's own ROS skips its Private entirely", err)
	}
	if fd.subrs.count != 0 {
		t.Errorf("fd.subrs.count = %d, want 0: no Private was read", fd.subrs.count)
	}
}

// TestPrivateAndSubrsZeroMeansAbsent pins C7: a Private operator whose size or offset is 0 means
// no Private DICT (cffload.c, cff_load_private_dict: "!private_offset || !private_size"), and a
// Subrs offset of 0 means no local subrs (cff_subfont_load: "if (priv->local_subrs_offset)")
// rather than an INDEX read at the Private DICT's own bytes.
func TestPrivateAndSubrsZeroMeansAbsent(t *testing.T) {
	t.Run("Private size 0 means no Private DICT", func(t *testing.T) {
		// The offset is far outside the 10-byte program: cff_load_private_dict looks at
		// private_size before ever touching private_offset, so a nonsensical offset beside a
		// size of 0 must still be accepted, not read as "outside the program".
		c := &CFF{data: make([]byte, 10)}
		fd, err := c.fontDict(dict(map[int][]float64{opPrivate: {0, 1000000}}), defaultFontMatrix, false)
		if err != nil {
			t.Fatalf("fontDict: %v, want success (size 0 means no Private DICT, regardless of offset)", err)
		}
		if fd.subrs.count != 0 {
			t.Errorf("fd.subrs.count = %d, want 0", fd.subrs.count)
		}
	})
	t.Run("Private offset 0 means no Private DICT", func(t *testing.T) {
		// If offset 0 were read as a Private DICT anyway, these two bytes decode as Subrs
		// (op 19) with value 1, sending fontDict looking for local Subrs at position 1 — where
		// there plainly are none — rather than leaving fd.subrs empty.
		data := []byte{140, 19, 0, 0, 0, 0, 0, 0, 0, 0}
		c := &CFF{data: data}
		fd, err := c.fontDict(dict(map[int][]float64{opPrivate: {6, 0}}), defaultFontMatrix, false)
		if err != nil {
			t.Fatalf("fontDict: %v, want success (offset 0 means no Private DICT)", err)
		}
		if fd.subrs.count != 0 {
			t.Errorf("fd.subrs.count = %d, want 0", fd.subrs.count)
		}
	})
	t.Run("Subrs offset 0 means no local subrs", func(t *testing.T) {
		// A Private DICT of defaultWidthX 0, nominalWidthX 0, Subrs 0. If Subrs 0 were read as an
		// INDEX at the Private DICT's own start, these same bytes (count 0x8B14 ...) would panic or
		// misread; rel > 0 never attempts it.
		pd := []byte{139, 20, 139, 21, 139, 19}
		data := make([]byte, 20)
		copy(data[10:], pd)
		c := &CFF{data: data}
		fd, err := c.fontDict(dict(map[int][]float64{opPrivate: {float64(len(pd)), 10}}), defaultFontMatrix, false)
		if err != nil {
			t.Fatalf("fontDict: %v, want success (Subrs 0 means no local subrs)", err)
		}
		if fd.subrs.count != 0 {
			t.Errorf("fd.subrs.count = %d, want 0: Subrs 0 must not be read as an INDEX at the "+
				"Private DICT itself", fd.subrs.count)
		}
	})
}

// TestFontMatrixScalesOutlines pins that the Top DICT's FontMatrix scales outlines: doubling it
// to [0.002 0 0 0.002 0 0] doubles every coordinate a charstring draws in its own units.
func TestFontMatrixScalesOutlines(t *testing.T) {
	b := cffbuild.Builder{
		FontMatrix: []float64{0.002, 0, 0, 0.002, 0, 0},
		Glyphs:     []cffbuild.Glyph{{CharString: cffbuild.CS(10, 0, cffbuild.RMoveTo, 0, 0, cffbuild.RLineTo, cffbuild.EndChar)}},
	}
	out, _, err := outlineOfBuilder(t, b, 1, 1000)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if len(out) == 0 || !close2(out[0].P[0], Point{20, 0}) {
		t.Errorf("outline = %v, want a move to (20,0): a 0.002 FontMatrix doubles the 10-unit delta", out)
	}
}

// TestFontMatrixCompositionForCIDFonts pins the three cases of composing a CID Font DICT's own
// FontMatrix with the Top DICT's (cff.go's fontDict comment, following FreeType's cffobjs.c): the
// Font DICT's own matrix applied first and the Top's after when both are present, and whichever
// one is present used alone when only one is.
func TestFontMatrixCompositionForCIDFonts(t *testing.T) {
	// The glyph visits (50,0) and then (0,50), so its two points are the composed matrix's two
	// rows, scaled.
	glyphCS := cffbuild.CS(50, 0, cffbuild.RMoveTo, -50, 50, cffbuild.RLineTo, cffbuild.EndChar)
	for _, c := range []struct {
		name      string
		fdMatrix  []float64
		topMatrix []float64
		want      [2]Point
	}{
		// Composed scale is 0.01 * 0.5 * 1000 = 5; the glyph's 50-unit deltas become 250.
		{"both present, composed", []float64{0.01, 0, 0, 0.01, 0, 0}, []float64{0.5, 0, 0, 0.5, 0, 0},
			[2]Point{{250, 0}, {0, 250}}},
		// Every element of both matrices nonzero and distinct, so a product that took any term
		// from the wrong element lands elsewhere. The FD's [0.003 0.0005 0.002 0.0032] then the
		// Top's [0.5 0.1 0.2 0.25] is [0.0016 0.000425 0.00164 0.001], an em of 1,000.
		{"both present, every element", []float64{0.003, 0.0005, 0.002, 0.0032, 0, 0},
			[]float64{0.5, 0.1, 0.2, 0.25, 0, 0}, [2]Point{{80, 21.25}, {82, 50}}},
		// FD alone: 0.002 * 1000 = 2; the 50-unit deltas become 100.
		{"only the FD's matrix", []float64{0.002, 0, 0, 0.002, 0, 0}, nil, [2]Point{{100, 0}, {0, 100}}},
		// Top alone: 0.002 * 1000 = 2; the 50-unit deltas become 100.
		{"only the Top DICT's matrix", nil, []float64{0.002, 0, 0, 0.002, 0, 0},
			[2]Point{{100, 0}, {0, 100}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := cffbuild.Builder{
				CID:        true,
				FontMatrix: c.topMatrix,
				FDs:        []cffbuild.FontDict{{FontMatrix: c.fdMatrix}},
				Glyphs:     []cffbuild.Glyph{{CID: 1, CharString: glyphCS}},
			}
			out, _, err := outlineOfBuilder(t, b, 1, 1000)
			if err != nil {
				t.Fatalf("Outline: %v", err)
			}
			if len(out) < 2 || !close2(out[0].P[0], c.want[0]) || !close2(out[1].P[0], c.want[1]) {
				t.Errorf("outline = %v, want a move to %v and a line to %v", out, c.want[0], c.want[1])
			}
		})
	}
}

// TestFontMatrixBounds pins matrix()'s and exactEm's refusal rules at both sides of each numeric
// bound their comments document, since the comments are the only place the rules are stated in
// one piece. Each refusal names the rule that made it, so a case refused by the wrong rule fails.
func TestFontMatrixBounds(t *testing.T) {
	for _, c := range []struct {
		name   string
		matrix []float64
		why    string // "" when the matrix is accepted
	}{
		{"5 elements is refused (6 required)", []float64{0.001, 0, 0, 0.001, 0}, "has 5 elements"},
		{"a linear element of 1 is refused", []float64{1, 0, 0, 1, 0, 0}, "which is refused"},
		{"a largest element just below 1 is accepted", []float64{0.99, 0, 0, 0.5, 0, 0}, ""},
		{"a yy of 1 is refused", []float64{0.5, 0, 0, 1, 0, 0}, "which is refused"},
		{"a largest element below 1e-5 is refused", []float64{9e-6, 0, 0, 9e-6, 0, 0}, "which is refused"},
		{"a degenerate (zero-determinant) matrix is refused", []float64{0.002, 0.002, 0.001, 0.001, 0, 0}, "which is refused"},
		{"a nearly singular matrix is refused", []float64{0.002, 0.002, 0.001, 0.00100001, 0, 0}, "which is refused"},
		{"a non-degenerate matrix of the same magnitude is accepted", []float64{0.002, 0, 0, 0.001, 0, 0}, ""},
		// FreeType's bound on this shear is 0.00548, and a tenth's margin puts ours at 0.00520.
		{"a shear inside FreeType's degeneracy bound is accepted", []float64{0.001, 0, 0.005, 0.001, 0, 0}, ""},
		{"a shear within a tenth of the bound is refused", []float64{0.001, 0, 0.0053, 0.001, 0, 0}, "which is refused"},
		{"a shear past the bound is refused", []float64{0.001, 0, 0.0056, 0.001, 0, 0}, "which is refused"},
		{"an element far tinier than the largest is refused", []float64{0.002, 1e-8, 0, 0.002, 0, 0}, "which is refused"},
		{"an element merely small is accepted", []float64{0.002, 3e-8, 0, 0.002, 0, 0}, ""},
		{"a translation is refused", []float64{0.001, 0, 0, 0.001, 0.05, 0.02}, "translates"},
		{"a vertical translation alone is refused", []float64{0.001, 0, 0, 0.001, 0, 0.02}, "translates"},
		{"an em FreeType rounds is refused", []float64{0.5, 0, 0, 0.35, 0, 0}, "which FreeType rounds"},
		{"an em a fifth of a unit off whole is refused", []float64{0.001, 0, 0, 0.00099980004, 0, 0}, "which FreeType rounds"},
		{"an em a twentieth of a unit off whole is accepted", []float64{0.001, 0, 0, 0.00099995, 0, 0}, ""},
		// The corpus's three non-default Top DICTs: an em of 2816.03, which FreeType reads as 2816.
		{"the corpus's 0.00035511001 is accepted", []float64{0.00035511001, 0, 0, 0.00035511001, 0, 0}, ""},
		{"an em of 65535 is accepted", []float64{1.0 / 65535, 0, 0, 1.0 / 65535, 0, 0}, ""},
		{"an em of 65536 is refused", []float64{1.0 / 65536, 0, 0, 1.0 / 65536, 0, 0}, "more than 65535"},
		{"a divisor too small to normalize by is refused", []float64{0.001, 0, 0, 0.0001, 0, 0}, "too small to normalize by"},
		{"a divisor just large enough is accepted", []float64{0.001, 0, 0, 0.0002, 0, 0}, ""},
		{"a zero yy divides by yx", []float64{0, 0.001, -0.001, 0, 0, 0}, ""},
		{"a zero yy and a yx FreeType rounds is refused", []float64{0, 0.35, -0.5, 0, 0, 0}, "which FreeType rounds"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := cffbuild.Builder{
				FontMatrix: c.matrix,
				Glyphs:     []cffbuild.Glyph{{CharString: noop}},
			}
			_, err := ParseCFF(b.Build())
			switch {
			case c.why == "" && err != nil:
				t.Errorf("ParseCFF: %v, want success", err)
			case c.why != "" && err == nil:
				t.Errorf("ParseCFF succeeded, want a refusal naming %q", c.why)
			case c.why != "" && !strings.Contains(err.Error(), c.why):
				t.Errorf("ParseCFF: %v, want a refusal naming %q", err, c.why)
			}
		})
	}
}

// TestCIDFontMatrixEms pins which ems a CID-keyed font's matrices must make whole, and how fine
// its composed divisor must be. FreeType normalizes the Top DICT's matrix before composing it with
// a Font DICT's and the composition after, so both ems must be whole, but the Font DICT's own em
// need not be: a Top em of 4 and an FD em of 714.25 compose to 2857. The composition is carried
// at the larger of the Top's em and the FD's own factor, at least 100 for an FD of [0.01 0 0 0.01].
func TestCIDFontMatrixEms(t *testing.T) {
	fd01 := []float64{0.01, 0, 0, 0.01, 0, 0}
	for _, c := range []struct {
		name    string
		top, fd []float64
		why     string  // "" when the font is accepted
		wantX   float64 // where the outline puts the charstring's point (1000, 0)
	}{
		{"a Top em FreeType rounds is refused", []float64{0.5, 0, 0, 0.35, 0, 0}, nil, "which FreeType rounds", 0},
		{"a Top em of 4 alone is accepted", []float64{0.5, 0, 0, 0.25, 0, 0}, nil, "", 500000},
		{"a composed em FreeType rounds is refused", []float64{0.5, 0, 0, 0.25, 0, 0}, []float64{0.002, 0, 0, 0.003, 0, 0}, "which FreeType rounds", 0},
		{"an FD em of 714.25 composing to 2857 is accepted", []float64{0.5, 0, 0, 0.25, 0, 0}, []float64{0.002, 0, 0, 1 / 714.25, 0, 0}, "", 1000},
		{"an FD em FreeType rounds is refused when alone", nil, []float64{0.002, 0, 0, 0.0014, 0, 0}, "which FreeType rounds", 0},
		{"an FD translation is refused", nil, []float64{0.001, 0, 0, 0.001, 0.05, 0}, "translates", 0},
		// The composed divisor is 0.002 and 0.00125 of the factor 100: 7.6e-5 and 1.2e-4 coarse.
		{"a composed divisor fine enough is accepted", []float64{0.2, 0, 0, 0.2, 0, 0}, fd01, "", 2000},
		{"a composed divisor too coarse is refused", []float64{0.125, 0, 0, 0.125, 0, 0}, fd01, "too small to normalize by", 0},
		// FreeType composes these to a divisor of 1311/65536 and an em of 4999, not 5000.
		{"a composition FreeType reads as 4999 is refused", []float64{0.02, 0, 0, 0.02, 0, 0}, fd01, "too small to normalize by", 0},
		// FreeType reads 0.5 as 5 at a factor of 10, so the Top's em of 1000 is what carries this.
		{"a Top em above the FD's factor carries the composition", []float64{0.001, 0, 0, 0.001, 0, 0}, []float64{0.5, 0, 0, 0.5, 0, 0}, "", 500},
		// matrix() takes a largest element of exactly 1e-5. Alone, that FD's em of 100000 is
		// past 65535, and a Top with every element below 1 can at most double yy: -1e-5*-0.99995
		// plus 1e-5*0.99995 is 1.9999e-5, an em of 50002.5, whole within emTolerance. 0.99995
		// keeps the Top's own em, 1.00005, whole within it too.
		{"an FD largest element of exactly 1e-5 composes to a whole-ish em", []float64{0.5, -0.99995, 0, 0.99995, 0, 0}, []float64{1e-5, 1e-5, -1e-5, 1e-5, 0, 0}, "", 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := cffbuild.Builder{
				CID:        true,
				FontMatrix: c.top,
				FDs:        []cffbuild.FontDict{{FontMatrix: c.fd}},
				Glyphs:     []cffbuild.Glyph{{CID: 1, CharString: cffbuild.CS(1000, 0, cffbuild.RMoveTo, 0, 0, cffbuild.RLineTo, cffbuild.EndChar)}},
			}
			cff, err := ParseCFF(b.Build())
			switch {
			case c.why == "" && err != nil:
				t.Fatalf("ParseCFF: %v, want success", err)
			case c.why != "" && err == nil:
				t.Fatalf("ParseCFF succeeded, want a refusal naming %q", c.why)
			case c.why != "" && !strings.Contains(err.Error(), c.why):
				t.Fatalf("ParseCFF: %v, want a refusal naming %q", err, c.why)
			case c.why != "":
				return
			}
			out, _, err := cff.Outline(1, 1000)
			if err != nil {
				t.Fatalf("Outline: %v", err)
			}
			if len(out) == 0 || !close2(out[0].P[0], Point{c.wantX, 0}) {
				t.Errorf("outline = %v, want a move to (%g,0)", out, c.wantX)
			}
		})
	}
}

// TestTopDictStructuralRefusals pins the Top DICT-level refusals cff.go documents: a font naming
// SyntheticBase or a CharstringType other than 2, an OpenType wrapper, a CFF2 major version, a
// missing or empty CharStrings, and an empty Name INDEX.
func TestTopDictStructuralRefusals(t *testing.T) {
	t.Run("SyntheticBase (12 20)", func(t *testing.T) {
		b := cffbuild.Builder{
			Glyphs:   []cffbuild.Glyph{{CharString: noop}},
			TopExtra: append(cffbuild.DictInt(0), 12, 20),
		}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with SyntheticBase, want a refusal")
		}
	})
	t.Run("CharstringType 1 (12 6)", func(t *testing.T) {
		b := cffbuild.Builder{
			Glyphs:   []cffbuild.Glyph{{CharString: noop}},
			TopExtra: append(cffbuild.DictInt(1), 12, 6),
		}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with CharstringType 1, want a refusal")
		}
	})
	t.Run("an OpenType-wrapped (OTTO) program", func(t *testing.T) {
		data := append([]byte("OTTO"), make([]byte, 20)...)
		if _, err := ParseCFF(data); err == nil {
			t.Error("ParseCFF succeeded with an OTTO tag, want a refusal")
		}
	})
	t.Run("major version 0", func(t *testing.T) {
		// cffbuild.Builder's Major field treats 0 as "default to 1" (it has no way to write a
		// literal 0), so the header is hand-written: major 0 is enough, since ParseCFF checks it
		// before reading anything else.
		if _, err := ParseCFF([]byte{0, 0, 0, 0}); err == nil || !strings.Contains(err.Error(), "CFF major version 0") {
			t.Errorf("ParseCFF = %v, want a refusal naming \"CFF major version 0\"", err)
		}
	})
	t.Run("a 4-byte program has no room for a Name INDEX", func(t *testing.T) {
		// Exactly a header (major 1, minor 0, hdrSize 4, offSize 4) and nothing after: long
		// enough to clear the length check, but the Name INDEX at hdrSize (4) starts past the
		// data, so the refusal comes from the INDEX read rather than the length itself.
		if _, err := ParseCFF([]byte{1, 0, 4, 4}); err == nil || !strings.Contains(err.Error(), "lies outside the program") {
			t.Errorf("ParseCFF = %v, want a refusal naming \"lies outside the program\"", err)
		}
	})
	t.Run("major version 2 (CFF2)", func(t *testing.T) {
		b := cffbuild.Builder{Major: 2, Glyphs: []cffbuild.Glyph{{CharString: noop}}}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with major version 2, want a refusal")
		}
	})
	t.Run("no CharStrings operator", func(t *testing.T) {
		c := &CFF{}
		if err := c.load(dict(nil)); err == nil {
			t.Error("load succeeded with no CharStrings operator, want a refusal")
		}
	})
	t.Run("CharStrings INDEX of zero glyphs", func(t *testing.T) {
		// An INDEX with count 0, at offset 2 -- not 0, so this exercises the zero-glyph refusal
		// rather than the "CharStrings offset of 0" one below.
		c := &CFF{data: []byte{0xAB, 0xCD, 0, 0}}
		if err := c.load(dict(map[int][]float64{opCharStrings: {2}})); err == nil {
			t.Error("load succeeded with zero glyphs, want a refusal")
		}
	})
	t.Run("CharStrings offset of 0", func(t *testing.T) {
		// A valid, nonempty INDEX sits at offset 0, but FreeType's charstrings_offset 0 is "none"
		// (cffload.c, cff_font_load), so this is refused before it is ever read.
		c := &CFF{data: []byte{0, 1, 1, 1, 2, 0xAA}}
		if err := c.load(dict(map[int][]float64{opCharStrings: {0}})); err == nil {
			t.Error("load succeeded with CharStrings offset 0, want a refusal")
		}
	})
	t.Run("an empty Name INDEX", func(t *testing.T) {
		// Header (major 1, minor 0, hdrSize 4, offSize 4) followed directly by a count-0 INDEX:
		// ParseCFF fails on the Name INDEX before it ever needs a Top DICT.
		data := []byte{1, 0, 4, 4, 0, 0}
		if _, err := ParseCFF(data); err == nil {
			t.Error("ParseCFF succeeded with an empty Name INDEX, want a refusal")
		}
	})
	t.Run("header hdrSize below 4", func(t *testing.T) {
		if _, err := ParseCFF([]byte{1, 0, 3, 4}); err == nil {
			t.Error("ParseCFF succeeded with hdrSize 3, want a refusal")
		}
	})
	t.Run("header offSize above 4", func(t *testing.T) {
		if _, err := ParseCFF([]byte{1, 0, 4, 5}); err == nil {
			t.Error("ParseCFF succeeded with a header offSize of 5, want a refusal")
		}
	})
	t.Run("header offSize of 0 is accepted", func(t *testing.T) {
		// FreeType checks its header offSize only against an upper bound of 4 (cffload.c,
		// cff_font_load), so 0 stays accepted; a real font (below) still parses with it.
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}}
		data := b.Build()
		data[3] = 0
		if _, err := ParseCFF(data); err != nil {
			t.Errorf("ParseCFF: %v, want success with a header offSize of 0", err)
		}
	})
	// The three cases above patch a bare 4-byte program, which the Name INDEX read refuses on its
	// own (there is no room for one past a 4-byte header) whether or not the header guards fire.
	// These use cffbuild.Builder.HeaderSize instead: Build computes every later offset from the
	// buffer's own length as it writes, so a font built with hdrSize 3 is otherwise entirely
	// self-consistent, and is refused only because hdrSize itself is below 4.
	t.Run("header hdrSize below 4 is refused, even for an otherwise valid font", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, HeaderSize: 3}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with hdrSize 3 on an otherwise valid font, want a refusal")
		}
	})
	t.Run("header hdrSize of 4 is accepted", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, HeaderSize: 4}
		if _, err := ParseCFF(b.Build()); err != nil {
			t.Errorf("ParseCFF: %v, want success with hdrSize 4", err)
		}
	})
	t.Run("a header with a spare byte and hdrSize 5 is accepted", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, HeaderSize: 5}
		if _, err := ParseCFF(b.Build()); err != nil {
			t.Errorf("ParseCFF: %v, want success with hdrSize 5 and the header padded to match", err)
		}
	})
	t.Run("header offSize above 4 is refused, even for an otherwise valid font", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}}
		data := b.Build()
		data[3] = 5
		if _, err := ParseCFF(data); err == nil {
			t.Error("ParseCFF succeeded with a header offSize of 5 on an otherwise valid font, want a refusal")
		}
	})
	t.Run("more Name INDEX fonts than Top DICTs", func(t *testing.T) {
		data := []byte{
			1, 0, 4, 4, // header
			0, 2, 1, 1, 2, 3, 'A', 'B', // Name INDEX: count 2, offSize 1, offsets [1,2,3], "A","B"
			0, 1, 1, 1, 2, 0, // Top DICT INDEX: count 1, offSize 1, offsets [1,2], one data byte
		}
		if _, err := ParseCFF(data); err == nil || !strings.Contains(err.Error(), "more than") {
			t.Errorf("ParseCFF = %v, want a refusal naming \"more than\"", err)
		}
	})
}

// index1 builds a minimal offSize-1 INDEX (TN #5176 §5) from small objects, for the Name and Top
// DICT INDEX surgery TestNameIndexDataSizeGuard below does.
func index1(objs ...[]byte) []byte {
	b := []byte{byte(len(objs) >> 8), byte(len(objs))} // #nosec G115 -- test fixtures, small counts
	if len(objs) == 0 {
		return b
	}
	b = append(b, 1)
	off := 1
	for i := 0; i <= len(objs); i++ {
		b = append(b, byte(off)) // #nosec G115 -- test fixtures, small offsets
		if i < len(objs) {
			off += len(objs[i])
		}
	}
	for _, o := range objs {
		b = append(b, o...)
	}
	return b
}

// rewriteNames replaces a real Build() font's Name INDEX with names, one Top DICT entry per name —
// data's own (so every absolute offset it carries still points at the unmoved bytes after it) for
// the first, and empty for the rest — and pads the header so those unmoved bytes land at exactly
// the position Build() left them.
func rewriteNames(t *testing.T, data []byte, names ...string) []byte {
	t.Helper()
	c := &CFF{data: data}
	origNames, err := c.index(int(data[2]))
	if err != nil {
		t.Fatalf("index(names): %v", err)
	}
	origTops, err := c.index(origNames.end)
	if err != nil {
		t.Fatalf("index(tops): %v", err)
	}
	topBytes, err := origTops.get(0)
	if err != nil {
		t.Fatalf("get(top 0): %v", err)
	}
	nameObjs := make([][]byte, len(names))
	for i, n := range names {
		nameObjs[i] = []byte(n)
	}
	topObjs := make([][]byte, len(names))
	topObjs[0] = topBytes
	newNames, newTops := index1(nameObjs...), index1(topObjs...)
	hdrSize := origTops.end - len(newNames) - len(newTops)
	if hdrSize < 4 {
		t.Fatalf("computed hdrSize %d, want at least 4 (shrink the fixture)", hdrSize)
	}
	hdr := make([]byte, hdrSize)
	hdr[0], hdr[2], hdr[3] = 1, byte(hdrSize), 4 // #nosec G115 -- bounded by the fixture's own size
	out := append(hdr, newNames...)
	out = append(out, newTops...)
	return append(out, data[origTops.end:]...)
}

// TestNameIndexDataSizeGuard pins R#0 (review finding #0): cff_font_load refuses a Name INDEX with
// more than one name whose combined data is smaller than the count — at least one name would then
// have to be empty, and "if we have an empty font name, it must be the only font in the CFF"
// (cffload.c, cff_font_load, just below the Name INDEX read). A lone empty name is exactly that
// one allowed case, so the rule applies only once there are 2 or more names.
func TestNameIndexDataSizeGuard(t *testing.T) {
	base := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{SID: 34, CharString: noop}}}.Build() // "A"
	t.Run("count 2, data size 1 (one name empty): refused", func(t *testing.T) {
		data := rewriteNames(t, base, "A", "") // offsets [1,2,2]: 1 byte of data for 2 names
		_, err := ParseCFF(data)
		if err == nil {
			t.Fatal("ParseCFF succeeded with 2 names in 1 byte, want a refusal")
		}
		if !strings.Contains(err.Error(), "holds 2 names in 1 bytes") {
			t.Errorf("ParseCFF = %v, want a refusal naming the Name INDEX's own data size", err)
		}
	})
	t.Run("count 2, data size 2: accepted by this guard", func(t *testing.T) {
		data := rewriteNames(t, base, "A", "B") // offsets [1,2,3]: 2 bytes of data for 2 names
		cff, err := ParseCFF(data)
		if err != nil {
			t.Fatalf("ParseCFF: %v, want success: 2 bytes for 2 names is not below the count", err)
		}
		if got := cff.NumGlyphs(); got != 2 {
			t.Errorf("NumGlyphs = %d, want 2", got)
		}
	})
	t.Run("count 1, data size 0 (the sole allowed empty name): accepted", func(t *testing.T) {
		data := rewriteNames(t, base, "") // offsets [1,1]: 0 bytes of data for 1 name
		cff, err := ParseCFF(data)
		if err != nil {
			t.Fatalf("ParseCFF: %v, want success: a single empty name is the allowed case", err)
		}
		if got := cff.NumGlyphs(); got != 2 {
			t.Errorf("NumGlyphs = %d, want 2", got)
		}
	})
}

// TestIndexRefusals pins cffIndex's own refusals directly: a truncated header, an offSize of 0
// or 5 (only 1-4 are defined, TN #5176 Table 7), a first offset other than 1, and an offset that
// runs backwards from the one before it — whether or not it is the INDEX's last entry.
func TestIndexRefusals(t *testing.T) {
	t.Run("truncated before the offSize byte", func(t *testing.T) {
		c := &CFF{data: []byte{0, 1}} // count 1, but no offSize byte follows
		if _, err := c.index(0); err == nil {
			t.Error("index succeeded, want a refusal for the missing offSize byte")
		}
	})
	for _, offSize := range []byte{0, 5} {
		t.Run(fmt.Sprintf("offSize %d", offSize), func(t *testing.T) {
			c := &CFF{data: []byte{0, 1, offSize, 0, 0}}
			if _, err := c.index(0); err == nil {
				t.Errorf("index succeeded with offSize %d, want a refusal", offSize)
			}
		})
	}
	t.Run("an entry's offsets run backwards", func(t *testing.T) {
		// count 2, offSize 1, offsets [1, 3, 2] (entry 1's end, offset 2, runs behind entry 0's
		// end of 3): refused in index(), which validates every offset once rather than leaving
		// get() to catch a backward run on the last offset alone.
		c := &CFF{data: []byte{0, 2, 1, 1, 3, 2, 0xAA, 0xBB}}
		if _, err := c.index(0); err == nil {
			t.Error("index succeeded with an offset running backwards, want a refusal")
		}
	})
	t.Run("the last offset one past the program's end", func(t *testing.T) {
		// count 1, offSize 1, offsets [1, 3]: base+last is one more than len(data), the exact
		// boundary past the one TestIndexBoundaryValuesAreAccepted pins as accepted.
		c := &CFF{data: []byte{0, 1, 1, 1, 3, 0xAA}}
		if _, err := c.index(0); err == nil {
			t.Error("index succeeded with the last offset one past the program, want a refusal")
		}
	})
	t.Run("a position outside the program", func(t *testing.T) {
		c := &CFF{data: []byte{0, 0}}
		for _, p := range []int{-1, 2, 100} {
			if _, err := c.index(p); err == nil || !strings.Contains(err.Error(), "INDEX lies outside the program") {
				t.Errorf("index(%d) = %v, want a refusal naming \"INDEX lies outside the program\"", p, err)
			}
		}
	})

	// C0/C1: FreeType repairs each of these instead of refusing them (cffload.c,
	// cff_index_get_pointers forces the first offset, raises one running backwards, and clamps one
	// past the end), so this package refuses them at index() time, before get() ever reads them.
	t.Run("first offset is not 1", func(t *testing.T) {
		// count 1, offSize 1, offsets [11, 12]: as if the Subrs INDEX behind a callsubr had its
		// first offset patched from 1 to 11, silently dropping the first 10 bytes of subr 0.
		data := append([]byte{0, 1, 1, 11, 12}, make([]byte, 12)...)
		if _, err := (&CFF{data: data}).index(0); err == nil {
			t.Error("index succeeded with a first offset of 11, want a refusal")
		}
	})
	t.Run("an offset runs backwards, not just the last", func(t *testing.T) {
		// count 3, offSize 1, offsets [1, 5, 3, 7]: entry 2's start, offset 3, runs behind entry
		// 1's end, offset 5 — the exact pattern that would give FreeType's cff_index_get_pointers
		// (which raises a backward offset to the one before it) different bytes than a get() that
		// only checked the last offset would have read.
		data := append([]byte{0, 3, 1, 1, 5, 3, 7}, make([]byte, 6)...)
		if _, err := (&CFF{data: data}).index(0); err == nil {
			t.Error("index succeeded with offsets [1,5,3,7], want a refusal")
		}
	})
	t.Run("a 4-byte offset near 0x80000000 does not panic on GOARCH=386", func(t *testing.T) {
		// The 30-byte program from the review's own failure scenario: header, a one-glyph Name
		// INDEX, a Top DICT INDEX of count 2 with offsets [1, 0x7FFFFFFF, 2] and one data byte, an
		// empty String INDEX, and an empty Global Subr INDEX. On a 32-bit int, x.base+0x7FFFFFFF
		// wraps negative; comparing offsets against len(data)-x.base instead, rather than adding
		// base to an unvalidated offset, must refuse this cleanly rather than panic.
		data := []byte{
			1, 0, 4, 1, // header
			0, 1, 1, 1, 2, 'A', // Name INDEX: count 1, offSize 1, offsets [1,2], object "A"
			0, 2, 4, 0, 0, 0, 1, 0x7F, 0xFF, 0xFF, 0xFF, 0, 0, 0, 2, 0xAA, // Top DICT INDEX
			0, 0, // String INDEX: count 0
			0, 0, // Global Subr INDEX: count 0
		}
		if len(data) != 30 {
			t.Fatalf("fixture is %d bytes, want 30 (the review's own failure scenario)", len(data))
		}
		if _, err := ParseCFF(data); err == nil {
			t.Error("ParseCFF succeeded with an offset of 0x7FFFFFFF, want a refusal")
		}
	})
}

// TestIndexBoundaryValuesAreAccepted pins index()'s own boundaries at the accepting side: an
// empty INDEX whose two count bytes are the last bytes of the program, and the largest offSize
// (4) TN #5176 Table 7 defines, both read rather than refused as running past the program.
func TestIndexBoundaryValuesAreAccepted(t *testing.T) {
	t.Run("an empty INDEX ending exactly at the program's end", func(t *testing.T) {
		c := &CFF{data: []byte{0, 0}} // count 0, and this is the entire program
		idx, err := c.index(0)
		if err != nil {
			t.Fatalf("index: %v", err)
		}
		if idx.count != 0 || idx.end != len(c.data) {
			t.Errorf("index = %+v, want count 0, end %d", idx, len(c.data))
		}
	})
	t.Run("offSize 4 reads its object", func(t *testing.T) {
		// count 1, offSize 4, offsets [1, 2] (one byte of object data), object byte 0xAA.
		data := []byte{0, 1, 4, 0, 0, 0, 1, 0, 0, 0, 2, 0xAA}
		c := &CFF{data: data}
		idx, err := c.index(0)
		if err != nil {
			t.Fatalf("index: %v", err)
		}
		got, err := idx.get(0)
		if err != nil {
			t.Fatalf("get(0): %v", err)
		}
		if string(got) != "\xAA" {
			t.Errorf("get(0) = % x, want AA", got)
		}
	})
	t.Run("the last offset ending exactly at the program's end", func(t *testing.T) {
		// count 1, offSize 1, offsets [1, 2] (one object byte): base+last == len(data), the exact
		// boundary index()'s "last > len(c.data)-x.base" check draws.
		c := &CFF{data: []byte{0, 1, 1, 1, 2, 0xAA}}
		idx, err := c.index(0)
		if err != nil {
			t.Fatalf("index: %v", err)
		}
		if idx.end != len(c.data) {
			t.Errorf("end = %d, want %d", idx.end, len(c.data))
		}
	})
}

// TestPrivateBoundsAtExactBoundary pins R#0 (round-4 review): fontDict's own bound, "off >
// len(c.data) || size > len(c.data)-off", at its exact edge, through ParseCFF rather than a direct
// fontDict call, since a boundary in the built file's total length is what the off-by-one mutant
// (size > len(c.data)-off+1) needs to read past. FreeType's cff_load_private_dict does the same
// two things separately -- FT_STREAM_SEEK to private_offset, then FT_FRAME_ENTER(private_size)
// (cffload.c:1925-1927) -- so a Private DICT that ends exactly at the program's last byte is
// accepted (FT_FRAME_ENTER reads exactly to the stream's limit), and one ending one byte past it
// is refused with this check's own error, not a slice that quietly reads one byte of whatever
// follows the program in Go's backing array. It pins the name-keyed Top DICT only: an FDArray
// Font DICT's Private goes through the same fontDict, from loadCIDFonts.
func TestPrivateBoundsAtExactBoundary(t *testing.T) {
	const size = 4
	empty := []byte{139, 20, 139, 21} // defaultWidthX 0, nominalWidthX 0: an ordinary empty Private DICT

	// off is written with DictInt's fixed 5-byte form, so embedding the real value in the second
	// build below does not change the program's length from the first build's, which is where the
	// value comes from.
	build := func(off int32) []byte {
		priv := append(cffbuild.DictInt(size), cffbuild.DictInt(off)...)
		priv = append(priv, opBytes(opPrivate)...)
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: priv}
		return b.Build()
	}
	off := len(build(0))
	data := build(int32(off)) // #nosec G115 -- off is a small test-built length

	t.Run("ends exactly at the program's end: accepted", func(t *testing.T) {
		program := append(append([]byte{}, data...), empty...)
		if _, err := ParseCFF(program); err != nil {
			t.Errorf("ParseCFF = %v, want success: the Private DICT ends exactly at len(program)", err)
		}
	})
	t.Run("ends one byte past the program: refused by the bound's own error", func(t *testing.T) {
		program := append(append([]byte{}, data...), empty[:size-1]...)
		if _, err := ParseCFF(program); err == nil || !strings.Contains(err.Error(), "Private DICT lies outside the program") {
			t.Errorf("ParseCFF = %v, want \"Private DICT lies outside the program\"", err)
		}
	})
}

// TestPrivateAndSubrsOutsideProgram pins that a Private DICT, or its Subrs INDEX, lying outside
// the program's own bytes is refused rather than read past the end of data.
func TestPrivateAndSubrsOutsideProgram(t *testing.T) {
	t.Run("Private DICT outside the program", func(t *testing.T) {
		c := &CFF{data: make([]byte, 10)}
		// size 100, offset 5: 5+100 runs far past the 10-byte program.
		_, err := c.fontDict(dict(map[int][]float64{opPrivate: {100, 5}}), defaultFontMatrix, false)
		if err == nil {
			t.Error("fontDict succeeded, want a refusal for a Private DICT outside the program")
		}
	})
	t.Run("Subrs outside the program", func(t *testing.T) {
		// A one-entry Private DICT naming Subrs at a relative offset (9999) that cannot fit
		// beside it in a 100-byte program.
		pd := append(cffbuild.DictInt(9999), 19) // Subrs (op 19) = 9999
		data := make([]byte, 100)
		copy(data[10:], pd)
		c := &CFF{data: data}
		_, err := c.fontDict(dict(map[int][]float64{opPrivate: {float64(len(pd)), 10}}), defaultFontMatrix, false)
		if err == nil {
			t.Error("fontDict succeeded, want a refusal for Subrs outside the program")
		}
	})
}

// TestPrivateOperandCount pins that checkTopOccurrence's Private case must have exactly two
// operands (a size and an offset). FreeType (cffparse.c, cff_parse_private_dict) silently takes
// the first two of a longer list and ignores the rest; this package refuses the grammar error
// instead of repairing it. fontDict's own copy of this check is gone (see cff.go's fontDict);
// parseDict, which runs checkTopOccurrence at every occurrence of Private, is the only place it
// runs now.
func TestPrivateOperandCount(t *testing.T) {
	for _, n := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d operands", n), func(t *testing.T) {
			b := append(dictInts(make([]int32, n)...), opBytes(opPrivate)...)
			_, err := parseDict(b, dictTop)
			if err == nil || !strings.Contains(err.Error(), "size and an offset") {
				t.Errorf("parseDict = %v, want a refusal naming \"size and an offset\"", err)
			}
		})
	}
}

// TestPrivateNegativeSizeOrOffsetIsRefused pins the other half of R#1's Private check:
// cff_parse_private_dict itself fails the font when either operand is negative (cffparse.c,
// cff_parse_private_dict:780-818: "Invalid dictionary size"/"...offset"). fontDict's own copy of
// this check is gone (see cff.go's fontDict), so this goes through ParseCFF instead — for each of
// the three DICTs FreeType reads a Private operator from: a name-keyed Top DICT, a CID-keyed Top
// DICT (where checkTopOccurrence is the only Private guard at all: load never calls fontDict on a
// CID Top DICT, see cff.go's load), and an FDArray Font DICT.
func TestPrivateNegativeSizeOrOffsetIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		priv []int32 // size, offset
	}{
		{"negative size", []int32{-1, 5}},
		{"negative offset", []int32{1, -1}},
	} {
		bad := append(dictInts(c.priv...), opBytes(opPrivate)...)
		t.Run(c.name+": name-keyed Top DICT", func(t *testing.T) {
			b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: bad}
			if _, err := ParseCFF(b.Build()); err == nil || !strings.Contains(err.Error(), "negative size or offset") {
				t.Errorf(`ParseCFF = %v with Private %v, want "negative size or offset"`, err, c.priv)
			}
		})
		t.Run(c.name+": CID-keyed Top DICT", func(t *testing.T) {
			b := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{{CID: 1, CharString: noop}}, TopExtra: bad}
			if _, err := ParseCFF(b.Build()); err == nil || !strings.Contains(err.Error(), "negative size or offset") {
				t.Errorf(`ParseCFF = %v with a CID Top DICT's Private %v, want "negative size or offset"`, err, c.priv)
			}
		})
		t.Run(c.name+": FDArray Font DICT", func(t *testing.T) {
			b := cffbuild.Builder{
				CID:    true,
				Glyphs: []cffbuild.Glyph{{CID: 1, CharString: noop}},
				FDs:    []cffbuild.FontDict{{Extra: bad}},
			}
			if _, err := ParseCFF(b.Build()); err == nil || !strings.Contains(err.Error(), "negative size or offset") {
				t.Errorf(`ParseCFF = %v with an FDArray Font DICT's Private %v, want "negative size or offset"`, err, c.priv)
			}
		})
	}
}

// TestPrivateOperandOverflowIsRefused pins the !ok conjunct of checkTopOccurrence's Private case
// on its own: intPair(ops[0], ops[1]) itself fails when an operand's magnitude is over
// math.MaxInt32, and int32's own minimum, -2147483648, is one such operand — a value the DICT's
// 32-bit integer form can encode exactly (cff.go's parseDict, the v==29 case), so it reaches here
// as an ordinary, non-real, negative operand rather than one the real or arity checks catch first.
// !ok alone would not be caught by size<0 or off<0 either: on overflow, intPair returns (0, 0,
// false) rather than the operand's own value, so a mutant that drops the !ok half (leaving only
// size<0||off<0) sees size and off as 0, not -2147483648, and finds nothing negative to refuse.
func TestPrivateOperandOverflowIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		priv []int32
	}{
		{"offset overflows intPair", []int32{0, math.MinInt32}},
		{"size overflows intPair", []int32{math.MinInt32, 0}},
	} {
		bad := append(dictInts(c.priv...), opBytes(opPrivate)...)
		t.Run(c.name+": name-keyed Top DICT", func(t *testing.T) {
			b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: bad}
			if _, err := ParseCFF(b.Build()); err == nil || !strings.Contains(err.Error(), "negative size or offset") {
				t.Errorf(`ParseCFF = %v with Private %v, want "negative size or offset"`, err, c.priv)
			}
		})
		t.Run(c.name+": CID-keyed Top DICT", func(t *testing.T) {
			b := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{{CID: 1, CharString: noop}}, TopExtra: bad}
			if _, err := ParseCFF(b.Build()); err == nil || !strings.Contains(err.Error(), "negative size or offset") {
				t.Errorf(`ParseCFF = %v with a CID Top DICT's Private %v, want "negative size or offset"`, err, c.priv)
			}
		})
		t.Run(c.name+": FDArray Font DICT", func(t *testing.T) {
			b := cffbuild.Builder{
				CID:    true,
				Glyphs: []cffbuild.Glyph{{CID: 1, CharString: noop}},
				FDs:    []cffbuild.FontDict{{Extra: bad}},
			}
			if _, err := ParseCFF(b.Build()); err == nil || !strings.Contains(err.Error(), "negative size or offset") {
				t.Errorf(`ParseCFF = %v with an FDArray Font DICT's Private %v, want "negative size or offset"`, err, c.priv)
			}
		})
	}
}

// TestPrivateRealOffsetGuardWithAFittingProgram pins R#2 (round-2 review finding #2): a program
// large enough that the Private DICT's own real-valued offset (32768 or more, uncapped since this
// package refuses rather than reads it, see C6's own comment on offset()) lies inside it, so a
// bounds check alone cannot explain a refusal — only a real-operand guard can. The Private DICT
// the offset names is otherwise perfectly ordinary (defaultWidthX 0, nominalWidthX 0), so removing
// every such guard would read it without error. The same offset written as an integer is read
// as-is and accepted, the same as every other offset operator (TestRealOperandRefusedOnOffset
// Operators). checkTopOccurrence, reached through parseDict at every occurrence of Private, is the
// only place this guard runs now: fontDict's own copy is gone (see cff.go's fontDict).
func TestPrivateRealOffsetGuardWithAFittingProgram(t *testing.T) {
	const privOff = 40000
	data := make([]byte, privOff+4)
	copy(data[privOff:], []byte{139, 20, 139, 21}) // defaultWidthX 0, nominalWidthX 0
	real40000 := []byte{30, 0x40, 0x00, 0x0F}      // packed decimal "40000"

	t.Run("real-form offset: refused although it fits the program", func(t *testing.T) {
		priv := append(cffbuild.DictInt(4), real40000...)
		if _, err := parseDict(append(priv, opPrivate), dictTop); err == nil {
			t.Error("parseDict succeeded with a real-valued Private offset that fits the program, want a refusal")
		}
	})
	t.Run("int-form offset of the same value: accepted", func(t *testing.T) {
		c := &CFF{data: data}
		priv := append(cffbuild.DictInt(4), cffbuild.DictInt(privOff)...)
		d, err := parseDict(append(priv, opPrivate), dictTop)
		if err != nil {
			t.Fatalf("parseDict: %v", err)
		}
		if _, err := c.fontDict(d, defaultFontMatrix, false); err != nil {
			t.Errorf("fontDict: %v, want success: an integer offset of %d is read as-is", err, privOff)
		}
	})
}

// TestParseDictOperandForms pins each operand form of TN #5176 §4's Table 3 (and Table 5 for
// reals), plus its refusals: a reserved lead byte, too many operands before one operator, an
// unterminated real, and a real using the reserved nibble.
func TestParseDictOperandForms(t *testing.T) {
	for _, c := range []struct {
		name    string
		b       []byte
		want    float64 // ignored when wantErr is set
		wantErr bool
	}{
		{"32-246: one-byte integer", []byte{200, 0}, 61, false},               // 200-139
		{"247-250: two-byte positive integer", []byte{247, 5, 0}, 113, false}, // (247-247)*256+5+108
		{"251-254: two-byte negative integer", []byte{251, 5, 0}, -113, false},
		// The one-byte range's own boundaries: 32 (-107) and 246 (107).
		{"32: one-byte, lower bound", []byte{32, 0}, -107, false},
		{"246: one-byte, upper bound", []byte{246, 0}, 107, false},
		// The two-byte positive range's boundaries: 247 (108) and 250 (1131).
		{"247: two-byte positive, lower bound", []byte{247, 0, 0}, 108, false},
		{"250: two-byte positive, upper bound", []byte{250, 255, 0}, 1131, false},
		// The two-byte negative range's boundaries: 251 (-108) and 254 (-1131).
		{"251: two-byte negative, lower bound", []byte{251, 0, 0}, -108, false},
		{"254: two-byte negative, upper bound", []byte{254, 255, 0}, -1131, false},
		// 31 is reserved, just below the one-byte range's lower bound of 32.
		{"reserved lead byte (31)", []byte{31, 0}, 0, true},
		{"28: three-byte (int16) integer", []byte{28, 0xFC, 0x18, 0}, -1000, false},
		{"29: five-byte (int32) integer", []byte{29, 0x00, 0x01, 0x86, 0xA0, 0}, 100000, false},
		// TN #5176 §4's own worked example: -2.25, nibbles minus/2/./2/5/end.
		{"real: minus, digit, decimal point, end", []byte{30, 0xE2, 0xA2, 0x5F, 0}, -2.25, false},
		// The spec's other worked example: 0.140541E-3, exercising the 'E-' nibble.
		{"real: the E- nibble", []byte{30, 0x0A, 0x14, 0x05, 0x41, 0xC3, 0xFF, 0}, 0.140541e-3, false},
		// 2.5E3 = 2500, exercising the plain 'E' nibble the other example does not.
		{"real: the E nibble", []byte{30, 0x2A, 0x5B, 0x3F, 0}, 2500, false},
		{"reserved lead byte (23)", []byte{23}, 0, true},
		{"real is unterminated", []byte{30, 0x12, 0}, 0, true},
		{"real uses the reserved nibble (0xD)", []byte{30, 0xD5, 0}, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := parseDict(c.b, dictTop)
			if c.wantErr {
				if err == nil {
					t.Error("parseDict succeeded, want a refusal")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDict: %v", err)
			}
			got := d.v[0]
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("d.v[0] = %v, want [%v]", got, c.want)
			}
		})
	}

	// Per-kind operand limits (cff.go's dictKind comment): CFF_MAX_STACK_DEPTH is 96 for a Top or
	// Font DICT and 97 for a Private DICT (cff_load_private_dict's one extra slot), checked twice by
	// cff_parser_run at that same threshold but at different moments (cffparse.c ~1183, a push, and
	// ~1330, an operator) — so an operator may be preceded by one operand fewer than can sit on the
	// stack with no operator following at all.
	for _, c := range []struct {
		name  string
		kind  dictKind
		op    byte
		hasOp bool // false: the operands are the whole DICT, with no operator after them
		n     int
		want  bool // true if parseDict should succeed
	}{
		{"Top: an operator with 95 operands is accepted", dictTop, 0, true, 95, true},
		{"Top: an operator with 96 operands is refused", dictTop, 0, true, 96, false},
		{"Top: 96 trailing operands with no operator is accepted", dictTop, 0, false, 96, true},
		{"Top: 97 trailing operands with no operator is refused", dictTop, 0, false, 97, false},
		{"Private: an operator with 96 operands is accepted", dictPrivate, 19, true, 96, true},
		{"Private: an operator with 97 operands is refused", dictPrivate, 19, true, 97, false},
		{"Private: 97 trailing operands with no operator is accepted", dictPrivate, 19, false, 97, true},
		{"Private: 98 trailing operands with no operator is refused", dictPrivate, 19, false, 98, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b []byte
			for i := 0; i < c.n; i++ {
				b = append(b, 139) // value 0, one byte each
			}
			if c.hasOp {
				b = append(b, c.op)
			}
			d, err := parseDict(b, c.kind)
			if !c.want {
				if err == nil {
					t.Errorf("parseDict succeeded with %d operands, want a refusal", c.n)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDict: %v, want success with %d operands", err, c.n)
			}
			if c.hasOp {
				if got := len(d.v[int(c.op)]); got != c.n {
					t.Errorf("len(d.v[%d]) = %d, want %d", c.op, got, c.n)
				}
			}
		})
	}
}

// TestKnownOperatorNeedsAnOperand pins C4b: a known operator (one FreeType's field table for this
// kind has) with zero operands is refused, except the delta kinds, which cff_parser_run exempts.
// An operator neither table knows just clears the stack, whether or not it has operands.
func TestKnownOperatorNeedsAnOperand(t *testing.T) {
	t.Run("Top: a known operator with no operands is refused", func(t *testing.T) {
		if _, err := parseDict([]byte{0}, dictTop); err == nil { // op 0, version, no operand
			t.Error("parseDict succeeded with a known Top operator and no operands, want a refusal")
		}
	})
	t.Run("Top: an unknown operator with no operands clears the stack", func(t *testing.T) {
		// op 6 (hstem in a charstring, but not a Top DICT field at all) with no operands.
		if _, err := parseDict([]byte{6}, dictTop); err != nil {
			t.Errorf("parseDict: %v, want success: op 6 is not a Top DICT field", err)
		}
	})
	t.Run("Private: a known non-delta operator with no operands is refused", func(t *testing.T) {
		if _, err := parseDict([]byte{19}, dictPrivate); err == nil { // op 19, Subrs, no operand
			t.Error("parseDict succeeded with a known Private operator and no operands, want a refusal")
		}
	})
	t.Run("Private: a delta operator with no operands is accepted", func(t *testing.T) {
		d, err := parseDict([]byte{6}, dictPrivate) // op 6, BlueValues, may be empty
		if err != nil {
			t.Fatalf("parseDict: %v, want success: BlueValues may be empty", err)
		}
		if _, ok := d.v[6]; !ok {
			t.Error("d.v[6] missing, want an empty (but present) operand list")
		}
	})
	t.Run("Private: a real (escaped) delta operator with no operands is accepted", func(t *testing.T) {
		if _, err := parseDict([]byte{12, 13}, dictPrivate); err != nil { // 1213, StemSnapV
			t.Errorf("parseDict: %v, want success: StemSnapV may be empty", err)
		}
	})
}

// opBytes encodes a bare operator (no operands): one byte, or escape 12 plus the second for one at
// or above 1200.
func opBytes(op int) []byte {
	if op >= 1200 {
		return []byte{12, byte(op - 1200)} // #nosec G115 -- op is one of this file's own literals
	}
	return []byte{byte(op)} // #nosec G115 -- op is one of this file's own literals
}

// TestFieldTableOperatorsRequireAnOperand is R#8's table-driven pin: every operator topFields and
// privateFields name (other than the delta kinds cff_parser_run exempts, TestKnownOperatorNeedsAn
// Operand above) fails with no operands. Built by ranging over the two maps themselves, so a wrong
// value survives a deleted or added key the same way; the length checks below catch that, since
// ranging over a map that lost a key would otherwise just stop testing it rather than fail.
func TestFieldTableOperatorsRequireAnOperand(t *testing.T) {
	const wantTopFields, wantPrivateFields = 32, 14
	if got := len(topFields); got != wantTopFields {
		t.Fatalf("len(topFields) = %d, want %d: an entry was added or removed", got, wantTopFields)
	}
	if got := len(privateFields); got != wantPrivateFields {
		t.Fatalf("len(privateFields) = %d, want %d: an entry was added or removed", got, wantPrivateFields)
	}
	for op := range topFields {
		t.Run(fmt.Sprintf("Top: operator %d with no operands is refused", op), func(t *testing.T) {
			if _, err := parseDict(opBytes(op), dictTop); err == nil {
				t.Errorf("parseDict succeeded with Top operator %d and no operands, want a refusal", op)
			}
		})
	}
	for op := range privateFields {
		t.Run(fmt.Sprintf("Private: operator %d with no operands is refused", op), func(t *testing.T) {
			if _, err := parseDict(opBytes(op), dictPrivate); err == nil {
				t.Errorf("parseDict succeeded with Private operator %d and no operands, want a refusal", op)
			}
		})
	}
	// The delta kinds privateFields' own comment names: known to FreeType's table but exempted from
	// needing any operand at all (cff_parser_run: "except for delta encoded arrays").
	for _, op := range []int{6, 7, 8, 9, 1212, 1213} {
		t.Run(fmt.Sprintf("Private: delta operator %d with no operands is accepted", op), func(t *testing.T) {
			if _, err := parseDict(opBytes(op), dictPrivate); err != nil {
				t.Errorf("parseDict: %v, want success: operator %d is a delta kind", err, op)
			}
		})
	}
}

// TestFontBBoxNeedsFourOperands pins R#1 (review finding #1): FontBBox needs 4 operands, and fails
// the whole program with Stack_Underflow below that (cffparse.c, cff_parse_font_bbox: "parser->top
// >= parser->stack + 4"). requiresOperand already refuses zero operands as any other known field
// does (TestKnownOperatorNeedsAnOperand); this is the gap between one operand and four.
func TestFontBBoxNeedsFourOperands(t *testing.T) {
	for _, c := range []struct {
		n    int
		want bool // true if parseDict should succeed
	}{{1, false}, {3, false}, {4, true}, {5, true}} {
		t.Run(fmt.Sprintf("%d operands", c.n), func(t *testing.T) {
			var b []byte
			for i := 0; i < c.n; i++ {
				b = append(b, 139) // value 0, one byte each
			}
			b = append(b, opFontBBox)
			_, err := parseDict(b, dictTop)
			if c.want && err != nil {
				t.Errorf("parseDict: %v, want success with %d operands", err, c.n)
			}
			if !c.want && err == nil {
				t.Errorf("parseDict succeeded with %d operands, want a refusal", c.n)
			}
		})
	}
}

// TestFontMatrixOperandCount pins R#0 (round-3 review finding #0): checkTopOccurrence's
// FontMatrix case needs exactly 6 operands, on both sides of that boundary. FreeType's
// cff_parse_font_matrix fails the whole program with Stack_Underflow below 6 (cffparse.c,
// cff_parse_font_matrix: "parser->top >= parser->stack + 6") and reads only the first 6 of a
// longer list, ignoring the rest; this package refuses the longer list outright instead of
// repairing it, the same as FontBBox's own lower bound (TestFontBBoxNeedsFourOperands) but exact
// on both sides rather than just a floor. The round-3 review found the only duplicate-occurrence
// fixture (TestDuplicatedOperatorOccurrenceIsRefused) used 1 operand, not the boundary itself, so
// a mutant loosening the check to `< 6` (accepting 7) or `> 6` (accepting 5) survived the suite.
func TestFontMatrixOperandCount(t *testing.T) {
	for _, c := range []struct {
		n    int
		want bool // true if parseDict should succeed
	}{{5, false}, {6, true}, {7, false}} {
		t.Run(fmt.Sprintf("%d operands", c.n), func(t *testing.T) {
			b := append(dictInts(make([]int32, c.n)...), opBytes(opFontMatrix)...)
			_, err := parseDict(b, dictTop)
			if c.want && err != nil {
				t.Errorf("parseDict: %v, want success with %d operands", err, c.n)
			}
			if !c.want && err == nil {
				t.Errorf("parseDict succeeded with %d operands, want a refusal", c.n)
			}
		})
	}
}

// TestFontMatrixDuplicatedOccurrenceRefusedEndToEnd pins R#0 end to end, at the arity boundary
// itself rather than TestDuplicatedOperatorOccurrenceIsRefused's own 1-then-6 FontMatrix case: a
// Top DICT with FontMatrix [0 0 0 0 0] (5 operands, one short of the boundary) followed by a valid
// FontMatrix is refused by ParseCFF, the same way cff_parser_run fails the whole Top DICT at the
// first occurrence's Stack_Underflow, before it ever reaches the second, valid one (cffparse.c,
// cff_parser_run: "error = field->reader(parser); if (error) goto Exit"). The second occurrence is
// defaultFontMatrix, which the control subtest below shows matrix() accepts on its own (R#1,
// round-4 review): without that control, [1 0 0 1 0 0] read as the "good" matrix would be refused
// by matrix() regardless of whether checkTopOccurrence ever ran, and this test would not notice.
func TestFontMatrixDuplicatedOccurrenceRefusedEndToEnd(t *testing.T) {
	good := cffbuild.DictReal(defaultFontMatrix[0])
	for _, e := range defaultFontMatrix[1:] {
		good = append(good, cffbuild.DictReal(e)...)
	}
	extra := append(dictInts(0, 0, 0, 0, 0), opBytes(opFontMatrix)...)
	extra = append(extra, good...)
	extra = append(extra, opBytes(opFontMatrix)...)

	t.Run("control: the good matrix alone parses through ParseCFF", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: append(good, opBytes(opFontMatrix)...)}
		if _, err := ParseCFF(b.Build()); err != nil {
			t.Errorf("ParseCFF = %v, want success: matrix() accepts %v on its own", err, defaultFontMatrix)
		}
	})
	t.Run("a 5-operand occurrence before the good one is refused end-to-end", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: extra}
		if _, err := ParseCFF(b.Build()); err == nil || !strings.Contains(err.Error(), "has 5 elements") {
			t.Errorf(`ParseCFF = %v, want checkTopOccurrence's FontMatrix arity error ("has 5 elements")`, err)
		}
	})
}

// TestMultipleMasterIsAlwaysRefused pins R#1: FreeType itself only partly supports a CFF1
// multiple-master font (an FT_TRACE1 message in cff_parse_multiple_master reads "handling first
// master design only"), and the blending a real one needs beyond that is a route this package has
// no model for at all — so the operator is refused outright, unlike FreeType's own
// Stack_Underflow-below-5 and num_designs-outside-2..16 checks, which would still accept some
// multiple-master programs this backend cannot draw correctly.
func TestMultipleMasterIsAlwaysRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		ops  []float64 // operands preceding the MultipleMaster operator
	}{
		{"1 operand (FreeType itself refuses this one)", []float64{2}},
		{"5 operands, num_designs 1 (FreeType itself refuses this one)", []float64{1, 0, 0, 0, 0}},
		{"5 operands, num_designs 2 (FreeType itself would accept this one)", []float64{2, 0, 0, 0, 0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b []byte
			for _, v := range c.ops {
				b = append(b, byte(v+139)) // #nosec G115 -- this file's own small literals
			}
			b = append(b, 12, 24) // escape 12, byte 24: MultipleMaster (1224)
			if _, err := parseDict(b, dictTop); err == nil {
				t.Error("parseDict succeeded with a MultipleMaster operator, want a refusal")
			}
		})
	}
}

// TestDuplicatedOperatorOccurrenceIsRefused pins R#0 (round-2 review finding #0): FreeType's
// cff_parser_run runs a field's reader at every occurrence of its operator and fails the whole
// DICT the moment one occurrence's reader errors (cffparse.c: "error = field->reader(parser); if
// (error) goto Exit"), but parseDict's own d.v[op] = ops overwrites on each occurrence, keeping
// only the last. Before checkTopOccurrence, a bad first occurrence of FontBBox, ROS, FontMatrix or
// Private followed by a valid one parsed successfully, because the arity and sign checks ran once,
// after the whole DICT, on the value the last occurrence left behind — the same class of bug
// TestFontBBoxNeedsFourOperands and its siblings pin for a single occurrence, but not for two.
// render/native's TestFontBBoxDuplicatedOccurrenceRefusedEndToEnd proves the FontBBox case against
// pdfium.
func TestDuplicatedOperatorOccurrenceIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		bad  []byte // the first, invalid occurrence's operands
		good []byte // the second, valid occurrence's operands
		op   int
	}{
		{"FontBBox: [0], then [0 0 0 0]", dictInts(0), dictInts(0, 0, 0, 0), opFontBBox},
		{"ROS: [0 0], then [65535 0 0]", dictInts(0, 0), dictInts(65535, 0, 0), opROS},
		{"FontMatrix: [0], then [0 0 0 0 0 0]", dictInts(0), dictInts(0, 0, 0, 0, 0, 0), opFontMatrix},
		{"Private: [0], then [4 10]", dictInts(0), dictInts(4, 10), opPrivate},
		{"Private: [-1 5], then [4 10]", dictInts(-1, 5), dictInts(4, 10), opPrivate},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := append(append([]byte{}, c.bad...), opBytes(c.op)...)
			b = append(b, c.good...)
			b = append(b, opBytes(c.op)...)
			if _, err := parseDict(b, dictTop); err == nil {
				t.Errorf("parseDict succeeded with a bad occurrence of operator %d before a good one, want a refusal", c.op)
			}
		})
	}
}

// dictInts concatenates cffbuild.DictInt(v) for each v, for a test that wants several integer
// operands at once.
func dictInts(vs ...int32) []byte {
	var b []byte
	for _, v := range vs {
		b = append(b, cffbuild.DictInt(v)...)
	}
	return b
}

// TestPrivateDictIgnoresTopOnlyOperators pins R#3 (round-2 review finding #3): FontBBox (op 5) and
// MultipleMaster (op 1224) are Top DICT operators FreeType's Private field table does not know
// (privateFields above has neither), so an occurrence of either inside a Private DICT is an
// unknown operator cff_parser_run ignores after clearing the stack (cffparse.c: "this is an
// unknown operator, or it is unsupported; we will ignore it for now"), not a field
// checkTopOccurrence's kind == dictTop guard may ever run against. Both must parse as an ordinary
// Private DICT regardless of their operand count.
func TestPrivateDictIgnoresTopOnlyOperators(t *testing.T) {
	t.Run("FontBBox with 1 operand", func(t *testing.T) {
		b := append(dictInts(0), opBytes(opFontBBox)...)
		if _, err := parseDict(b, dictPrivate); err != nil {
			t.Errorf("parseDict: %v, want success: FontBBox is unknown in a Private DICT", err)
		}
	})
	t.Run("MultipleMaster with no operands", func(t *testing.T) {
		if _, err := parseDict(opBytes(opMultipleMaster), dictPrivate); err != nil {
			t.Errorf("parseDict: %v, want success: MultipleMaster is unknown in a Private DICT", err)
		}
	})
}

// fittingRealPrivate builds mk's font with a Private DICT (size 4, a real-valued offset) that
// ends exactly at the built program's own end — the boundary TestPrivateBoundsAtExactBoundary
// pins as accepted — so fontDict's own bound can never be the reason ParseCFF refuses it; only
// checkTopOccurrence's real-operand check (R#2, round-4 review) can (or, once the mutant under
// TestPrivateRealOffsetGuardWithAFittingProgram is applied, cannot). mk's own extra bytes come
// back from Build unchanged in length, but the real offset's own encoding grows as its value
// does: the first pass writes off 0 (1e0f), the second the first build's length, which is a byte
// longer, and the third confirms the second. So it takes three builds, and the cap of six is the
// guard against an encoding that never settles.
func fittingRealPrivate(t *testing.T, mk func(extra []byte) cffbuild.Builder) []byte {
	t.Helper()
	const size = 4
	empty := []byte{139, 20, 139, 21} // defaultWidthX 0, nominalWidthX 0: an ordinary empty Private DICT
	off, data := 0, []byte(nil)
	for i := 0; i < 6; i++ {
		priv := append(cffbuild.DictInt(size), cffbuild.DictReal(float64(off))...)
		priv = append(priv, opBytes(opPrivate)...)
		data = mk(priv).Build()
		if off == len(data) {
			return append(data, empty...)
		}
		off = len(data)
	}
	t.Fatalf("fittingRealPrivate did not converge: off=%d len(data)=%d", off, len(data))
	return nil
}

// TestRealOperandRefusedOnOffsetOperators pins C6: a real-valued (30-form) operand is refused on
// every operator this package reads as an offset or count, since FreeType reads it through
// cff_parse_num, which reads a real past 32767 as 32767 or 0 depending on its digits
// (cffparse.c:395-396, 417-418, 426-427) — a different value than int(v[0]) would read — while
// the same value written as an integer is read as-is, even past 32767.
func TestRealOperandRefusedOnOffsetOperators(t *testing.T) {
	real40000 := []byte{30, 0x40, 0x00, 0x0F} // packed decimal "40000"
	int40000 := cffbuild.DictInt(40000)
	t.Run("charset: a real operand is refused", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: append(real40000, 15)}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with a real-valued charset offset, want a refusal")
		}
	})
	t.Run("charset: an integer operand of 40000 is accepted (as a lies-outside refusal, not a form one)", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: append(int40000, 15)}
		_, err := ParseCFF(b.Build())
		if err == nil || strings.Contains(err.Error(), "real-valued") {
			t.Errorf("ParseCFF = %v, want a refusal unrelated to the operand's form (40000 is outside the program)", err)
		}
	})
	t.Run("ROS: a real operand is refused", func(t *testing.T) {
		var extra []byte
		extra = append(extra, real40000...)
		extra = append(extra, cffbuild.DictInt(0)...)
		extra = append(extra, cffbuild.DictInt(0)...)
		extra = append(extra, 12, 30)
		b := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{{CID: 1, CharString: noop}}, TopExtra: extra}
		if _, err := ParseCFF(b.Build()); err == nil {
			t.Error("ParseCFF succeeded with a real-valued ROS registry, want a refusal")
		}
	})
	t.Run("Private: a real operand is refused", func(t *testing.T) {
		var priv []byte
		priv = append(priv, cffbuild.DictInt(6)...)
		priv = append(priv, real40000...)
		priv = append(priv, 18)
		// checkTopOccurrence, reached through parseDict at every occurrence of Private, is the
		// only place this check runs now (fontDict's own copy is gone, see cff.go's fontDict); it
		// runs at the same point in the same function for a name-keyed or CID-keyed Top DICT and
		// for an FDArray Font DICT. The three ParseCFF subtests below each use an offset that
		// fittingRealPrivate makes fit inside the built program (R#2, round-4 review), so
		// fontDict's own bound (TestPrivateBoundsAtExactBoundary) cannot also explain a refusal —
		// only this check can, and so this end-to-end case does cover all three.
		t.Run("through parseDict directly", func(t *testing.T) {
			if _, err := parseDict(priv, dictTop); err == nil || !strings.Contains(err.Error(), "real-valued operand") {
				t.Errorf(`parseDict = %v, want "real-valued operand"`, err)
			}
		})
		t.Run("name-keyed Top DICT, through ParseCFF", func(t *testing.T) {
			program := fittingRealPrivate(t, func(extra []byte) cffbuild.Builder {
				return cffbuild.Builder{Glyphs: []cffbuild.Glyph{{CharString: noop}}, TopExtra: extra}
			})
			if _, err := ParseCFF(program); err == nil || !strings.Contains(err.Error(), "real-valued operand") {
				t.Errorf(`ParseCFF = %v, want "real-valued operand"`, err)
			}
		})
		t.Run("CID-keyed Top DICT, through ParseCFF", func(t *testing.T) {
			program := fittingRealPrivate(t, func(extra []byte) cffbuild.Builder {
				return cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{{CID: 1, CharString: noop}}, TopExtra: extra}
			})
			if _, err := ParseCFF(program); err == nil || !strings.Contains(err.Error(), "real-valued operand") {
				t.Errorf(`ParseCFF = %v, want "real-valued operand"`, err)
			}
		})
		t.Run("FDArray Font DICT, through ParseCFF", func(t *testing.T) {
			program := fittingRealPrivate(t, func(extra []byte) cffbuild.Builder {
				return cffbuild.Builder{
					CID:    true,
					Glyphs: []cffbuild.Glyph{{CID: 1, CharString: noop}},
					FDs:    []cffbuild.FontDict{{Extra: extra}},
				}
			})
			if _, err := ParseCFF(program); err == nil || !strings.Contains(err.Error(), "real-valued operand") {
				t.Errorf(`ParseCFF = %v, want "real-valued operand"`, err)
			}
		})
	})
	t.Run("Subrs: a real operand is refused", func(t *testing.T) {
		d, err := parseDict(append(real40000, 19), dictPrivate)
		if err != nil {
			t.Fatalf("parseDict: %v", err)
		}
		if _, err := d.offset(opSubrs, -1); err == nil {
			t.Error("offset succeeded with a real-valued Subrs offset, want a refusal")
		}
	})
}

func TestExpertCharsetIsRefused(t *testing.T) {
	for _, offset := range []int32{1, 2} {
		t.Run(fmt.Sprintf("offset %d", offset), func(t *testing.T) {
			b := cffbuild.Builder{
				Glyphs:   []cffbuild.Glyph{{SID: 34, CharString: noop}},
				TopExtra: append(cffbuild.DictInt(offset), 15), // charset (op 15) = offset
			}
			if _, err := ParseCFF(b.Build()); err == nil {
				t.Errorf("ParseCFF succeeded with charset offset %d, want a refusal", offset)
			}
		})
	}
}

// TestCFFBuildProducesAParseableFont is Part 1's own verification: every shape cffbuild.Builder
// claims to write — each charset and encoding format, supplements, and a CID-keyed font with each
// FDSelect format and two Font DICTs — has to parse before it is trusted as a test fixture for
// anything else in this file.
func TestCFFBuildProducesAParseableFont(t *testing.T) {
	square, diamond := cffbuild.Square(), cffbuild.Diamond()
	for _, c := range []struct {
		name string
		b    cffbuild.Builder
	}{
		{"default name-keyed, two glyphs", cffbuild.Builder{
			Glyphs: []cffbuild.Glyph{
				{Name: "A", CharString: square},
				{Name: "B", CharString: diamond},
			},
		}},
		{"charset format 0", cffbuild.Builder{
			CharsetFormat: 0,
			Glyphs: []cffbuild.Glyph{
				{Name: "A", CharString: square},
				{Name: "B", CharString: diamond},
			},
		}},
		{"charset format 1", cffbuild.Builder{
			CharsetFormat: 1,
			Glyphs: []cffbuild.Glyph{
				{Name: "A", CharString: square},
				{Name: "B", CharString: diamond},
			},
		}},
		{"charset format 2", cffbuild.Builder{
			CharsetFormat: 2,
			Glyphs: []cffbuild.Glyph{
				{Name: "A", CharString: square},
				{Name: "B", CharString: diamond},
			},
		}},
		{"encoding format 0", cffbuild.Builder{
			Codes: []byte{'A', 'B'},
			Glyphs: []cffbuild.Glyph{
				{Name: "A", CharString: square},
				{Name: "B", CharString: diamond},
			},
		}},
		{"encoding format 1", cffbuild.Builder{
			Codes:          []byte{'A', 'B'},
			EncodingFormat: 1,
			Glyphs: []cffbuild.Glyph{
				{Name: "A", CharString: square},
				{Name: "B", CharString: diamond},
			},
		}},
		{"encoding supplements", cffbuild.Builder{
			Codes:       []byte{'A', 'B'},
			Supplements: []cffbuild.Supplement{{Code: 'C', SID: 34}}, // 34 "A", TN #5176 Appendix A
			Glyphs: []cffbuild.Glyph{
				{Name: "A", CharString: square},
				{Name: "B", CharString: diamond},
			},
		}},
		{"CID-keyed, FDSelect format 0, two FDs", cffbuild.Builder{
			CID: true,
			Glyphs: []cffbuild.Glyph{
				{CID: 1, FD: 0, CharString: square},
				{CID: 2, FD: 1, CharString: diamond},
			},
			FDs:            []cffbuild.FontDict{{}, {}},
			FDSelectFormat: 0,
		}},
		{"CID-keyed, FDSelect format 3, two FDs", cffbuild.Builder{
			CID: true,
			Glyphs: []cffbuild.Glyph{
				{CID: 1, FD: 0, CharString: square},
				{CID: 2, FD: 1, CharString: diamond},
			},
			FDs:            []cffbuild.FontDict{{}, {}},
			FDSelectFormat: 3,
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := c.b.Build()
			cff, err := ParseCFF(data)
			if err != nil {
				t.Fatalf("ParseCFF: %v\ndata: % x", err, data)
			}
			if got := cff.NumGlyphs(); got != len(c.b.Glyphs)+1 {
				t.Errorf("NumGlyphs = %d, want %d", got, len(c.b.Glyphs)+1)
			}
			for gid := uint16(1); int(gid) < cff.NumGlyphs(); gid++ {
				if _, _, err := cff.Outline(gid, 1<<16); err != nil {
					t.Errorf("Outline(%d): %v", gid, err)
				}
			}
		})
	}
}

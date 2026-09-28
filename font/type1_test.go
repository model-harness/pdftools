package font

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/t1build"
)

// t1LenIV is a convenience for t1build.Builder.LenIV, which takes a pointer so a caller can
// distinguish "not set" (the default of 4) from an explicit 0.
func t1LenIV(v int) *int { return &v }

// squareGlyphs is t1build.Square()'s glyph list, spelled out so tests that need Build's other
// return value (length1) or a non-default LenIV can still draw the same square.
func squareGlyphs() []t1build.Glyph {
	return []t1build.Glyph{
		{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
		{Name: "A", CS: t1build.CS(100, 700, t1build.HSBW, 0, 100, t1build.RMoveTo, 500, t1build.HLineTo,
			500, t1build.VLineTo, -500, t1build.HLineTo, t1build.ClosePath, t1build.EndChar)},
	}
}

// encryptT1 is the forward direction of the Type 1 cipher of §7 of the format -- the inverse of
// decrypt -- so a test can choose the plaintext of an eexec cipher precisely.
func encryptT1(plain []byte, r uint16) []byte {
	out := make([]byte, len(plain))
	for i, p := range plain {
		c := p ^ byte(r>>8)
		out[i] = c
		r = (uint16(c)+r)*52845 + 22719
	}
	return out
}

var squareOutlineWant = Outline{
	{Op: SegMove, P: [3]Point{{100, 100}}},
	{Op: SegLine, P: [3]Point{{600, 100}}},
	{Op: SegLine, P: [3]Point{{600, 600}}},
	{Op: SegLine, P: [3]Point{{100, 600}}},
	{Op: SegClose},
}

// TestParseType1HappyPath pins the ordinary case: a font with .notdef already at GID 0, a custom
// built-in encoding, and NumGlyphs/GIDForName/GIDForCode/ClearTextLength all agreeing.
func TestParseType1HappyPath(t *testing.T) {
	b := t1build.Builder{Glyphs: squareGlyphs(), Encoding: map[byte]string{65: "A"}}
	data, length1 := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	if got := tf.NumGlyphs(); got != 2 {
		t.Errorf("NumGlyphs = %d, want 2", got)
	}
	if gid, ok := tf.GIDForName(".notdef"); !ok || gid != 0 {
		t.Errorf("GIDForName(.notdef) = %d, %v; want 0, true", gid, ok)
	}
	if gid, ok := tf.GIDForName("A"); !ok || gid != 1 {
		t.Errorf("GIDForName(A) = %d, %v; want 1, true", gid, ok)
	}
	if gid, ok := tf.GIDForCode(65); !ok || gid != 1 {
		t.Errorf("GIDForCode(65) = %d, %v; want 1, true (the custom built-in encoding)", gid, ok)
	}
	if got := tf.ClearTextLength(); got != length1 {
		t.Errorf("ClearTextLength = %d, want %d (Build's own length1)", got, length1)
	}
	out, _, err := tf.Outline(1, 1000)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if !sameOutline(out, squareOutlineWant) {
		t.Errorf("outline = %v\nwant     %v", out, squareOutlineWant)
	}
}

// TestGIDForCodeStandardEncoding pins that a font declaring the predefined StandardEncoding (no
// Encoding array of its own) still resolves a code through it: "A" is code 65 (TN #5176 Appendix
// B, the same table CFF's StandardEncoding tests use).
func TestGIDForCodeStandardEncoding(t *testing.T) {
	b := t1build.Builder{Glyphs: squareGlyphs()} // Encoding left nil: Build writes StandardEncoding.
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	if gid, ok := tf.GIDForCode(65); !ok || gid != 1 {
		t.Errorf("GIDForCode(65) = %d, %v; want 1, true (StandardEncoding's code 65 is A)", gid, ok)
	}
}

// TestLenIVVariants pins that lenIV 0 (no bytes stripped, but still charstring-encrypted) and -1
// (not encrypted at all) both draw the same outline lenIV 4 (the default) does.
func TestLenIVVariants(t *testing.T) {
	for _, iv := range []int{0, -1} {
		t.Run(fmt.Sprintf("lenIV %d", iv), func(t *testing.T) {
			b := t1build.Builder{LenIV: t1LenIV(iv), Glyphs: squareGlyphs(), Encoding: map[byte]string{65: "A"}}
			data, _ := b.Build()
			tf, err := ParseType1(data)
			if err != nil {
				t.Fatalf("ParseType1: %v", err)
			}
			gid, ok := tf.GIDForName("A")
			if !ok {
				t.Fatalf("GIDForName(A) = false")
			}
			out, _, err := tf.Outline(gid, 1000)
			if err != nil {
				t.Fatalf("Outline: %v", err)
			}
			if !sameOutline(out, squareOutlineWant) {
				t.Errorf("outline = %v\nwant     %v", out, squareOutlineWant)
			}
		})
	}
}

// TestNotdefFoundAtNonzero pins that a .notdef declared at any glyph index other than 0 is swapped
// into GID 0, and the glyph that had been at GID 0 takes .notdef's old slot -- every other glyph's
// GID is untouched.
func TestNotdefFoundAtNonzero(t *testing.T) {
	b := t1build.Builder{Glyphs: []t1build.Glyph{
		{Name: "A", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
		{Name: ".notdef", CS: t1build.CS(0, 333, t1build.HSBW, t1build.EndChar)},
		{Name: "B", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
	}}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	if gid, ok := tf.GIDForName(".notdef"); !ok || gid != 0 {
		t.Errorf("GIDForName(.notdef) = %d, %v; want 0, true", gid, ok)
	}
	if gid, ok := tf.GIDForName("A"); !ok || gid != 1 {
		t.Errorf("GIDForName(A) = %d, %v; want 1, true (swapped into .notdef's old slot)", gid, ok)
	}
	if gid, ok := tf.GIDForName("B"); !ok || gid != 2 {
		t.Errorf("GIDForName(B) = %d, %v; want 2, true (untouched)", gid, ok)
	}
}

// TestNotdefAbsentIsSynthesized pins that a font naming no .notdef gets one synthesized at GID 0 --
// "0 333 hsbw endchar", an empty outline with no error -- and the glyph that had been at GID 0 is
// moved to the end, still reachable by its own name.
func TestNotdefAbsentIsSynthesized(t *testing.T) {
	b := t1build.Builder{Glyphs: []t1build.Glyph{
		{Name: "A", CS: t1build.CS(100, 700, t1build.HSBW, 0, 100, t1build.RMoveTo, 500, t1build.HLineTo,
			500, t1build.VLineTo, -500, t1build.HLineTo, t1build.ClosePath, t1build.EndChar)},
	}, Encoding: map[byte]string{65: "A"}}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	if got := tf.NumGlyphs(); got != 2 {
		t.Errorf("NumGlyphs = %d, want 2", got)
	}
	if gid, ok := tf.GIDForName(".notdef"); !ok || gid != 0 {
		t.Errorf("GIDForName(.notdef) = %d, %v; want 0, true", gid, ok)
	}
	out, _, err := tf.Outline(0, 100)
	if err != nil {
		t.Fatalf("Outline(0): %v", err)
	}
	if out != nil {
		t.Errorf("Outline(0) = %v, want nil: the synthesized .notdef draws nothing", out)
	}
	if gid, ok := tf.GIDForName("A"); !ok || gid != 1 {
		t.Errorf("GIDForName(A) = %d, %v; want 1, true (moved to the end)", gid, ok)
	}
	outA, _, err := tf.Outline(1, 1000)
	if err != nil {
		t.Fatalf("Outline(1): %v", err)
	}
	if !sameOutline(outA, squareOutlineWant) {
		t.Errorf("A's outline = %v\nwant          %v", outA, squareOutlineWant)
	}
}

// TestEncodingLastOccurrenceWins pins that a second /Encoding overrides the first, code for code,
// as FreeType's own reading does (the array is rebuilt from scratch at each occurrence, and only the
// last occurrence's assignments survive).
func TestEncodingLastOccurrenceWins(t *testing.T) {
	b := t1build.Builder{
		Glyphs: []t1build.Glyph{
			{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
			{Name: "X", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
			{Name: "Y", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
		},
		Encoding:  map[byte]string{65: "X"},
		FontExtra: "/Encoding 256 array\ndup 65 /Y put\nreadonly def\n",
	}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	gidY, ok := tf.GIDForName("Y")
	if !ok {
		t.Fatalf("GIDForName(Y) = false")
	}
	if gid, ok := tf.GIDForCode(65); !ok || gid != gidY {
		t.Errorf("GIDForCode(65) = %d, %v; want %d, true (the second /Encoding wins)", gid, ok, gidY)
	}
}

// TestEncodingCodeWithNoGlyph pins that a code the encoding maps to a name no glyph carries answers
// false, not GID 0.
func TestEncodingCodeWithNoGlyph(t *testing.T) {
	b := t1build.Builder{
		Glyphs:   []t1build.Glyph{{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)}},
		Encoding: map[byte]string{65: "Z"}, // no glyph named Z
	}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	if gid, ok := tf.GIDForCode(65); ok {
		t.Errorf("GIDForCode(65) = %d, true; want false: no glyph is named Z", gid)
	}
}

// TestEncodingUnsetCodeSkipsAnEmptyName pins that a code the built-in encoding leaves unset
// answers false even when a glyph is named "" — the name an unset code holds before lookup.
func TestEncodingUnsetCodeSkipsAnEmptyName(t *testing.T) {
	b := t1build.Builder{
		Glyphs:   []t1build.Glyph{{Name: "", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)}},
		Encoding: map[byte]string{65: "A"},
	}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	if gid, ok := tf.GIDForName(""); !ok || gid == 0 {
		t.Fatalf(`GIDForName("") = %d, %v; want a real glyph, or this asserts nothing`, gid, ok)
	}
	if gid, ok := tf.GIDForCode(66); ok {
		t.Errorf("GIDForCode(66) = %d, true; want false: code 66 is unset", gid)
	}
}

// TestEncodingCodeMappedToNotdef pins that a code the encoding maps to .notdef itself answers
// false, the same as an unassigned code -- .notdef is never reached through GIDForCode.
func TestEncodingCodeMappedToNotdef(t *testing.T) {
	b := t1build.Builder{
		Glyphs:   []t1build.Glyph{{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)}},
		Encoding: map[byte]string{65: ".notdef"},
	}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	if gid, ok := tf.GIDForCode(65); ok {
		t.Errorf("GIDForCode(65) = %d, true; want false: code 65 names .notdef", gid)
	}
}

// TestParseType1TopLevelRefusals pins the refusals that only show up walking a whole program:
// the PFB and header checks, every shape of a malformed eexec, and the two post-parse checks for a
// missing /CharStrings or /Encoding.
func TestParseType1TopLevelRefusals(t *testing.T) {
	minimalGlyphs := []t1build.Glyph{
		{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
		{Name: "A", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
	}
	program := func(b t1build.Builder) []byte {
		data, _ := b.Build()
		return data
	}
	hexEexec := func() []byte {
		data, length1 := t1build.Builder{Glyphs: minimalGlyphs}.Build()
		return append(data[:length1], []byte("41424344\n")...)
	}

	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"the PFB segment form", []byte{0x80, 1, 2, 3}, "the PFB segment form"},
		{"no header", []byte("this is not a font\n"), "no %!PS-AdobeFont"},
		{"no eexec at all", []byte("%!PS-AdobeFont-1.0\ncurrentdict end\n"), "no eexec"},
		{
			"eexec not followed by a cipher",
			[]byte("%!PS-AdobeFont-1.0\ncurrentfile eexec" + strings.Repeat(" ", 20)),
			"eexec is not followed by a cipher",
		},
		{
			"the eexec part is shorter than its four random bytes",
			[]byte("%!PS-AdobeFont-1.0\ncurrentfile eexec" + strings.Repeat("\n", 6) + "AB"),
			"shorter than its four random bytes",
		},
		{"hexadecimal eexec", hexEexec(), "hexadecimal eexec"},
		{"no /CharStrings", program(t1build.Builder{Glyphs: minimalGlyphs, NoCharStrings: true}), "no /CharStrings"},
		{"no built-in /Encoding", program(t1build.Builder{Glyphs: minimalGlyphs, NoEncoding: true}), "no built-in /Encoding"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseType1(c.data)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("ParseType1 = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// TestPrivateDictBoundaryGuards pins privateDict's own off-by-one guards directly: the eexec
// token needs its full ten trailing bytes (five letters, one whitespace, four cipher bytes) before
// it is recognized at all, the hexadecimal-eexec guard needs only those same four cipher bytes
// before testing them (not a fifth byte beyond), isHex accepts the digit 9, and a private section
// of exactly its four random bytes -- the minimum -- is accepted, not refused.
func TestPrivateDictBoundaryGuards(t *testing.T) {
	t.Run("the eexec token needs its full ten trailing bytes", func(t *testing.T) {
		// "eexec" (5) + one whitespace (1) + three cipher bytes (3) = 9, one short of the ten
		// bytes the guard requires; this occurrence is skipped like any other token, and since
		// none other follows, privateDict reports no eexec at all.
		l := &t1Loader{t: &Type1{}}
		_, err := l.privateDict([]byte("currentfile eexec ABC"))
		if err == nil || !strings.Contains(err.Error(), "no eexec") {
			t.Errorf(`privateDict = %v, want "no eexec": only nine bytes follow the leading e`, err)
		}
	})

	t.Run("the hexadecimal-eexec guard does not need a fifth trailing byte", func(t *testing.T) {
		// Exactly four hex digits sit at the very end of the buffer, with nothing past them.
		l := &t1Loader{t: &Type1{}}
		_, err := l.privateDict([]byte("currentfile eexec 4142"))
		if err == nil || !strings.Contains(err.Error(), "hexadecimal eexec") {
			t.Errorf(`privateDict = %v, want "hexadecimal eexec, which is refused"`, err)
		}
	})

	t.Run("isHex accepts the digit 9", func(t *testing.T) {
		if !isHex('9') {
			t.Errorf("isHex('9') = false, want true")
		}
	})

	t.Run("a private section of exactly its four random bytes is accepted", func(t *testing.T) {
		l := &t1Loader{t: &Type1{}}
		private, err := l.privateDict([]byte("currentfile eexec WXYZ"))
		if err != nil {
			t.Fatalf("privateDict: %v, want a private section of exactly 4 bytes accepted", err)
		}
		if got := string(private); got != "    " {
			t.Errorf("private = %q, want four spaces (all four random bytes blanked)", got)
		}
	})

	t.Run("the four random bytes are blanked to spaces, not left as their decrypted values", func(t *testing.T) {
		cipher := encryptT1([]byte("TEST"), 55665)
		data := append([]byte("currentfile eexec\n"), cipher...)
		l := &t1Loader{t: &Type1{}}
		private, err := l.privateDict(data)
		if err != nil {
			t.Fatalf("privateDict: %v", err)
		}
		if got := string(private[:4]); got != "    " {
			t.Errorf("private[:4] = %q, want four spaces (the random bytes blanked)", got)
		}
	})
}

// TestParseDictBinaryMarkerRecognition pins parseDict's own recognition of a "size RD ... ND" (or
// "-| ... |-") binary blob attached to any key, not just Subrs or CharStrings: it needs six bytes
// of lookahead past the marker before trusting it, and once recognized, skips exactly the declared
// size, byte for byte, even past a delimiter like ')' inside the raw data that would otherwise
// break the ordinary token scanner.
func TestParseDictBinaryMarkerRecognition(t *testing.T) {
	t.Run("the marker needs its full six trailing bytes", func(t *testing.T) {
		l := &t1Loader{t: &Type1{}}
		if err := l.parseDict([]byte("1 RDabcd")); err != nil {
			t.Errorf("parseDict = %v, want the undersized marker ignored, not misread as binary", err)
		}
	})

	t.Run("an unrecognized key's RD-delimited value is skipped whole", func(t *testing.T) {
		l := &t1Loader{t: &Type1{}}
		if err := l.parseDict([]byte("/Zzz 5 RD ab)cd ND\n")); err != nil {
			t.Errorf("parseDict = %v, want the binary blob skipped by its declared size", err)
		}
	})
}

// TestParseType1TooManyGlyphsIsRefused pins the 0xFFFF glyph-count ceiling directly against
// parseCharStrings, without paying for an eexec-encrypted program around 65,536 glyph entries.
func TestParseType1TooManyGlyphsIsRefused(t *testing.T) {
	const n = 65535 // plus the synthesized .notdef, since none of these is named ".notdef".
	var buf strings.Builder
	fmt.Fprintf(&buf, "%d dict dup begin\n", n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&buf, "/g%d 1 RD Q ND\n", i)
	}
	buf.WriteString("end\n")
	l := &t1Loader{t: &Type1{}, lenIV: -1}
	err := l.parseCharStrings(&psParser{b: []byte(buf.String())})
	if err == nil || !strings.Contains(err.Error(), "65536 glyphs") {
		t.Errorf("parseCharStrings = %v, want a refusal naming 65536 glyphs", err)
	}
}

// TestParseType1AtGlyphCeilingSucceeds pins that a font landing at exactly 0xFFFF glyphs -- the
// ceiling itself, fully representable -- is accepted, not refused a glyph too early.
func TestParseType1AtGlyphCeilingSucceeds(t *testing.T) {
	const n = 65534 // plus the synthesized .notdef makes exactly 0xFFFF, the ceiling itself.
	var buf strings.Builder
	fmt.Fprintf(&buf, "%d dict dup begin\n", n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&buf, "/g%d 1 RD Q ND\n", i)
	}
	buf.WriteString("end\n")
	l := &t1Loader{t: &Type1{}, lenIV: -1}
	err := l.parseCharStrings(&psParser{b: []byte(buf.String())})
	if err != nil {
		t.Fatalf("parseCharStrings = %v, want success at exactly the 0xFFFF ceiling", err)
	}
	if got := len(l.t.glyphs); got != 0xFFFF {
		t.Errorf("len(glyphs) = %d, want %d", got, 0xFFFF)
	}
}

// TestParseDictRefusals pins the keyword-level refusals of the main and Private dictionaries: a
// synthetic font's /FontDirectory after /Private, PaintType, the four multiple-master keys, lenIV,
// and BuildCharArray -- each called directly against parseDict, at the point in the grammar the
// refusal actually fires, rather than through a whole assembled program.
func TestParseDictRefusals(t *testing.T) {
	for _, c := range []struct {
		name    string
		private bool
		data    []byte
		want    string
	}{
		{"FontDirectory after /Private", true, []byte("FontDirectory\n"), "FontDirectory after /Private"},
		{"PaintType non-zero", false, []byte("/PaintType 1 x\n"), "PaintType 1, which is refused"},
		{"PaintType malformed value", false, []byte("/PaintType foo\n"), "/PaintType:"},
		{"PaintType negative", false, []byte("/PaintType -1 x\n"), "PaintType -1, which is refused"},
		{"BlendDesignPositions", false, []byte("/BlendDesignPositions 0\n"), "BlendDesignPositions, a multiple master"},
		{"BlendDesignMap", false, []byte("/BlendDesignMap 0\n"), "BlendDesignMap, a multiple master"},
		{"BlendAxisTypes", false, []byte("/BlendAxisTypes 0\n"), "BlendAxisTypes, a multiple master"},
		{"WeightVector", false, []byte("/WeightVector 0\n"), "WeightVector, a multiple master"},
		{"lenIV below -1", true, []byte("/lenIV -2\n"), "lenIV -2"},
		{"lenIV malformed value", true, []byte("/lenIV foo\n"), "/lenIV:"},
		{"BuildCharArray", true, []byte("/BuildCharArray 0\n"), "BuildCharArray, a multiple master"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &t1Loader{t: &Type1{}, private: c.private}
			err := l.parseDict(c.data)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("parseDict = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// TestFontMatrixRefusals pins fontMatrix's own refusals directly: not an array at all, an entry
// that is not the value the format requires (the same branch a malformed token like an unterminated
// real would reach, since both fail the same v != want[i] || err != nil check), and a count other
// than six.
func TestFontMatrixRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"not an array", []byte("0.001 0 0 0.001 0 0"), "is not an array"},
		{"a wrong value", []byte("[0.002 0 0 0.001 0 0]"), `entry 0 is "0.002"`},
		{"a count other than six (7 elements)", []byte("[0.001 0 0 0.001 0 0 0]"), "does not hold six numbers"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &psParser{b: c.data}
			err := p.fontMatrix()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("fontMatrix = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// TestParseEncodingRefusals pins parseEncoding's own refusals directly, one for every distinct
// return statement it makes other than success.
func TestParseEncodingRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"no value at all", []byte(""), "has no value"},
		{"a literal array", []byte("[ /a /b ]"), "written as a literal array"},
		{"a count other than 256", []byte("255 array\n"), "an /Encoding array of 255 entries"},
		{"a malformed count", []byte("99999999999999999999 array\n"), "/Encoding: an integer out of range"},
		{"other than an array or StandardEncoding", []byte("ExpertEncoding"), "other than an array or StandardEncoding"},
		{"a code out of range", []byte("256 array\ndup 300 /A put\ndef\n"), "/Encoding code 300"},
		{"a count greater than 256", []byte("300 array\n"), "an /Encoding array of 300 entries"},
		{
			"StandardEncoding exactly at the length guard's boundary",
			[]byte("StandardEncodingX"),
			"other than an array or StandardEncoding",
		},
		{
			"content other than StandardEncoding, but long enough to satisfy the length guard",
			[]byte("ExpertEncodingXXXX"),
			"other than an array or StandardEncoding",
		},
		{
			"more codes than the array holds",
			[]byte("256 array\n" + strings.Repeat("dup 0 /A put\n", 257) + "def\n"),
			"more codes than its array holds",
		},
		{"the encoding ends inside a glyph name", []byte("256 array\ndup 5 /AB"), "ends inside a glyph name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &t1Loader{}
			err := l.parseEncoding(&psParser{b: c.data})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("parseEncoding = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// TestParseEncodingCode255Accepted pins that /Encoding code 255, the maximum byte value, is
// accepted -- only a code outside 0..255 is refused.
func TestParseEncodingCode255Accepted(t *testing.T) {
	l := &t1Loader{}
	err := l.parseEncoding(&psParser{b: []byte("256 array\ndup 255 /A put\ndef\n")})
	if err != nil {
		t.Fatalf("parseEncoding: %v, want code 255 accepted", err)
	}
	if l.names[255] != "A" {
		t.Errorf("names[255] = %q, want %q", l.names[255], "A")
	}
}

// TestParseSubrsRefusals pins parseSubrs's own refusals directly: the literal-array shorthand not
// empty, a malformed or negative count, a malformed or out-of-range index, a duplicate index, and a
// subroutine too short to hold lenIV's own leading bytes.
func TestParseSubrsRefusals(t *testing.T) {
	for _, c := range []struct {
		name  string
		lenIV int
		data  []byte
		want  string
	}{
		{"the [ shorthand is not an empty array", 4, []byte("[ 1 2 3 ]"), "is not an empty array"},
		{"a negative count", 4, []byte("-1 array\n"), "count -1"},
		{"a malformed count", 4, []byte("foo array\n"), "/Subrs:"},
		{"a malformed index", 4, []byte("1 array\ndup foo 3 RD abc NP\n"), "/Subrs index:"},
		{"an index out of range", 4, []byte("1 array\ndup 5 5 RD abcde NP\n"), "of a /Subrs array of 1"},
		{"an index equal to count", 4, []byte("1 array\ndup 1 5 RD abcde NP\n"), "of a /Subrs array of 1"},
		{
			"a duplicate index",
			4,
			[]byte("2 array\ndup 0 5 RD abcde NP\ndup 0 5 RD fghij NP\n"),
			"subroutine 0 defined twice",
		},
		{"a subroutine shorter than lenIV", 4, []byte("1 array\ndup 0 3 RD abc NP\n"), "too short for lenIV 4"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &t1Loader{t: &Type1{}, lenIV: c.lenIV}
			err := l.parseSubrs(&psParser{b: c.data})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("parseSubrs = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
	t.Run("Subrs defined twice", func(t *testing.T) {
		l := &t1Loader{t: &Type1{}, lenIV: 4}
		if err := l.parseSubrs(&psParser{b: []byte("0 array\nND\n")}); err != nil {
			t.Fatalf("first parseSubrs: %v", err)
		}
		err := l.parseSubrs(&psParser{b: []byte("0 array\nND\n")})
		if err == nil || !strings.Contains(err.Error(), "defined twice") {
			t.Errorf("second parseSubrs = %v, want a refusal for a duplicate /Subrs", err)
		}
	})
	// The boundary parseCharStrings draws the other way: exactly lenIV bytes is an empty subroutine.
	t.Run("a subroutine of exactly lenIV bytes", func(t *testing.T) {
		l := &t1Loader{t: &Type1{}, lenIV: 4}
		if err := l.parseSubrs(&psParser{b: []byte("1 array\ndup 0 4 RD abcd NP\n")}); err != nil {
			t.Fatalf("parseSubrs = %v, want an empty subroutine 0", err)
		}
		if cs, ok := l.t.subrs[0]; !ok || len(cs) != 0 {
			t.Errorf("subroutine 0 = %q (present %v), want present and empty", cs, ok)
		}
	})
}

// TestParseSubrsTruncatedDupIsIgnored pins that the lookahead before matching a trailing "dup"
// needs a sixth byte past it: a "dup" sitting exactly at the end, with no room left for an index,
// is left for the caller rather than misread as the start of an entry.
func TestParseSubrsTruncatedDupIsIgnored(t *testing.T) {
	l := &t1Loader{t: &Type1{}, lenIV: 4}
	if err := l.parseSubrs(&psParser{b: []byte("0 array\ndup ")}); err != nil {
		t.Errorf("parseSubrs = %v, want the truncated trailing dup ignored", err)
	}
}

// TestParseCharStringsRefusals pins parseCharStrings's own refusals directly, including the two
// distinct branches that share the message "/CharStrings holds no glyphs": a declared count of 0,
// caught before the loop ever runs, and a count of 1 with no glyph entries before "end", caught
// after the loop finds nothing to show for it.
func TestParseCharStringsRefusals(t *testing.T) {
	for _, c := range []struct {
		name  string
		lenIV int
		data  []byte
		want  string
	}{
		{"a malformed count", 4, []byte("foo dict dup begin\nend\n"), "/CharStrings:"},
		{"a negative count", 4, []byte("-1 dict dup begin\nend\n"), "count -1"},
		{"a declared count of 0", 4, []byte("0 dict dup begin\nend\n"), "holds no glyphs"},
		{"a count of 1 but no glyphs before end", 4, []byte("1 dict dup begin\nend\n"), "holds no glyphs"},
		{
			"the room heuristic truncates the count to zero, but leftover content is still examined",
			4,
			[]byte("9/ab"),
			"holds no glyphs",
		},
		{
			"a declared count of 0 with a glyph entry present",
			-1,
			[]byte("0 dict dup begin\n/a 1 RD Q ND\nend\n"),
			"holds no glyphs",
		},
		{"CharStrings runs to the end of the program", 4, []byte("1 dict dup begin\n/a"), "runs to the end of the program"},
		{"CharStrings ends inside a glyph name", 4, []byte("1 dict dup begin\n/)"), "ends inside a glyph name"},
		{
			"more glyphs than declared",
			-1,
			[]byte("1 dict dup begin\n/a 1 RD Q ND\n/b 1 RD Q ND\nend\n"),
			"more than the 1 glyphs it declares",
		},
		{
			"a duplicate glyph name",
			-1,
			[]byte("2 dict dup begin\n/a 1 RD Q ND\n/a 1 RD Q ND\nend\n"),
			"glyph /a defined twice",
		},
		{
			"glyph data at or below lenIV bytes",
			4,
			[]byte("1 dict dup begin\n/a 3 RD abc ND\nend\n"),
			"too short for lenIV 4",
		},
		{
			"glyph data of exactly lenIV bytes",
			4,
			[]byte("1 dict dup begin\n/a 4 RD abcd ND\nend\n"),
			"too short for lenIV 4",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &t1Loader{t: &Type1{}, lenIV: c.lenIV}
			err := l.parseCharStrings(&psParser{b: c.data})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("parseCharStrings = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
	t.Run("CharStrings defined twice", func(t *testing.T) {
		l := &t1Loader{t: &Type1{}, lenIV: -1}
		if err := l.parseCharStrings(&psParser{b: []byte("1 dict dup begin\n/a 1 RD Q ND\nend\n")}); err != nil {
			t.Fatalf("first parseCharStrings: %v", err)
		}
		err := l.parseCharStrings(&psParser{b: []byte("1 dict dup begin\n/b 1 RD Q ND\nend\n")})
		if err == nil || !strings.Contains(err.Error(), "defined twice") {
			t.Errorf("second parseCharStrings = %v, want a refusal for a duplicate /CharStrings", err)
		}
	})
}

// TestPSParserTokenRefusals pins the tokenizer-level refusals: toInt's radix, fractional, and
// overflow forms, binary's missing-size, malformed-size, and past-the-program cases, a stray '>', a
// self-delimiting character where a token belongs, and the unterminated forms of a literal string,
// a procedure, and a hex string.
func TestPSParserTokenRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func() error
		want string
	}{
		{"integer: radix form", func() error { _, err := (&psParser{b: []byte("8#17 ")}).toInt(); return err }, "not an integer"},
		{"integer: fractional form", func() error { _, err := (&psParser{b: []byte("1.5 ")}).toInt(); return err }, "not an integer"},
		{"integer: overflows int32", func() error {
			_, err := (&psParser{b: []byte("99999999999999999999 ")}).toInt()
			return err
		}, "out of range"},
		{"integer: fits int64 but overflows int32", func() error {
			_, err := (&psParser{b: []byte("5000000000 ")}).toInt()
			return err
		}, "out of range"},
		{"binary: no size at all", func() error { _, err := (&psParser{b: []byte("RD")}).binary(); return err }, "without a size"},
		{"binary: a malformed size", func() error {
			_, err := (&psParser{b: []byte("99999999999999999999 RD")}).binary()
			return err
		}, "binary data size:"},
		{"binary: runs past the program", func() error {
			_, err := (&psParser{b: []byte("100 RD X")}).binary()
			return err
		}, "runs past the program"},
		{"binary: size exactly equals the remaining room", func() error {
			_, err := (&psParser{b: []byte("3 RD abc")}).binary()
			return err
		}, "runs past the program"},
		{"a stray >", func() error { p := &psParser{b: []byte(">x")}; p.skipToken(); return p.err }, "an unexpected >"},
		{"a self-delimiting character", func() error { p := &psParser{b: []byte(")")}; p.skipToken(); return p.err }, "self-delimiting"},
		{"an unterminated literal string", func() error { _, err := skipLiteralString([]byte("(abc"), 0); return err }, "an unterminated string"},
		{"a literal string ending in a backslash", func() error {
			_, err := skipLiteralString([]byte("(abc\\"), 0)
			return err
		}, "ends in a backslash"},
		{"an unterminated procedure", func() error { _, err := skipProcedure([]byte("{abc"), 0); return err }, "an unterminated procedure"},
		{"a hex string closed by neither > nor EOF", func() error {
			_, err := skipHexString([]byte("<12)"), 0)
			return err
		}, "without its closing >"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// TestTokenizerPositiveCases pins the FreeType tokenizer behavior that the refusal tables above
// never exercise: a skipped value can be a procedure holding a string and a comment, a literal
// string can nest with escapes, "<<"/">>" are their own two-byte tokens, a comment between two
// tokens is invisible to the parser, and the eexec whitespace rule treats a \r\n line ending as one
// unit of whitespace rather than \r ending the cipher search early.
func TestTokenizerPositiveCases(t *testing.T) {
	t.Run("a skipped procedure containing a string and a comment", func(t *testing.T) {
		data := []byte("{ (a str with a } inside) % a } comment too\n stuff }")
		cur, err := skipProcedure(data, 0)
		if err != nil {
			t.Fatalf("skipProcedure: %v", err)
		}
		if cur != len(data) {
			t.Errorf("skipProcedure stopped at %d, want %d: the real closing brace, not one inside the string or comment", cur, len(data))
		}
	})

	t.Run("nested literal strings with escapes", func(t *testing.T) {
		data := []byte(`(outer (inner \) still inside) \\ end) rest`)
		want := len(data) - len(" rest")
		cur, err := skipLiteralString(data, 0)
		if err != nil {
			t.Fatalf("skipLiteralString: %v", err)
		}
		if cur != want {
			t.Errorf("skipLiteralString stopped at %d, want %d: just after the real closing paren", cur, want)
		}
	})

	t.Run("<< and >> are their own tokens", func(t *testing.T) {
		p := &psParser{b: []byte("<< /a 1 >> rest")}
		p.skipToken()
		if p.err != nil || p.pos != 2 {
			t.Fatalf("skipToken(<<) = pos %d, err %v; want pos 2, no error", p.pos, p.err)
		}
	})

	t.Run("a comment between two tokens is invisible", func(t *testing.T) {
		l := &t1Loader{t: &Type1{}}
		err := l.parseDict([]byte("/PaintType % a comment\n 0\n"))
		if err != nil {
			t.Errorf("parseDict: %v, want the comment skipped and PaintType 0 accepted", err)
		}
	})

	t.Run("the eexec whitespace rule skips a CRLF as one line ending", func(t *testing.T) {
		data := []byte("%!PS-AdobeFont-1.0\ncurrentfile eexec\r\nABX")
		l := &t1Loader{t: &Type1{}}
		_, err := l.privateDict(data)
		if err == nil || !strings.Contains(err.Error(), "shorter than its four random bytes") {
			t.Errorf("privateDict = %v, want the \\r\\n skipped as whitespace, leaving only \"ABX\" as cipher", err)
		}
	})
}

package cmap

import (
	"fmt"
	"strings"
	"testing"
)

func TestIdentityCMaps(t *testing.T) {
	// Identity-H is what every composite font in this repo's corpus uses, so it
	// is the one path that must be exactly right. It is synthesized rather than
	// parsed, which means nothing else in this file exercises it.
	for _, name := range []string{"Identity-H", "Identity-V"} {
		c, ok := Identity(name)
		if !ok {
			t.Fatalf("Identity(%q) not recognized", name)
		}
		if c.Name != name {
			t.Errorf("Name = %q, want %q", c.Name, name)
		}
		for _, code := range []uint32{0, 1, 0x41, 0x1234, 0xFFFF} {
			cid, ok := c.CID(code)
			if !ok || cid != code {
				t.Errorf("%s: CID(0x%04X) = %d ok=%v, want %d", name, code, cid, ok, code)
			}
		}
		// Two-byte codes, always: a reader that split these as single bytes would
		// report twice as many glyphs as the string contains.
		got := c.Codes([]byte{0x00, 0x41, 0x12, 0x34})
		want := []Code{{0x0041, 2}, {0x1234, 2}}
		if len(got) != len(want) {
			t.Fatalf("%s: Codes gave %d codes, want %d: %v", name, len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: code %d = %+v, want %+v", name, i, got[i], want[i])
			}
		}
	}

	if _, ok := Identity("Identity"); ok {
		t.Error(`"Identity" without a writing-mode suffix is not a CMap name`)
	}
	if _, ok := Identity("UniJIS-UCS2-H"); ok {
		t.Error("a predefined CJK CMap must not be reported as an identity mapping; its CIDs are not its codes")
	}
}

func TestParseToUnicodeBfchar(t *testing.T) {
	// The shape 262 entries across this corpus take: a single two-byte codespace
	// range and a run of bfchar pairs. The wrapping PostScript is included
	// verbatim because a parser that trips on findresource or begincmap fails on
	// every real file while passing a hand-trimmed fixture.
	src := `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
3 beginbfchar
<0003> <0020>
<0024> <0041>
<0057> <0074>
endbfchar
endcmap
CMapName currentdict /CMap defineresource pop
end
end`

	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name != "Adobe-Identity-UCS" {
		t.Errorf("Name = %q, want Adobe-Identity-UCS", c.Name)
	}
	for code, want := range map[uint32]string{0x03: " ", 0x24: "A", 0x57: "t"} {
		got, ok := c.Text(code)
		if !ok || got != want {
			t.Errorf("Text(0x%04X) = %q ok=%v, want %q", code, got, ok, want)
		}
	}
	if _, ok := c.Text(0x99); ok {
		t.Error("an unmapped code reported a mapping; the caller cannot fall back if absence looks like an answer")
	}
	if _, texts := c.Entries(); texts != 3 {
		t.Errorf("stored %d text mappings, want 3", texts)
	}
}

func TestParseBfrangeForms(t *testing.T) {
	// All three destination forms of §9.10.3, which is what makes bfrange the
	// operator most often read wrong. 112 of them appear across this corpus.
	src := `2 beginbfrange
<0020> <0022> <0041>
<0030> <0032> [<0058> <0059> <005A>]
endbfrange
1 beginbfrange
<0040> <0042> <00660066>
endbfrange`

	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cases := map[uint32]string{
		// A string destination increments: the range's first code takes the
		// destination and each subsequent code advances it.
		0x20: "A", 0x21: "B", 0x22: "C",
		// An array destination gives one value per code, in order.
		0x30: "X", 0x31: "Y", 0x32: "Z",
		// A multi-character destination advances only its last character, so a
		// ligature range maps to "ff", "fg", "fh" rather than incrementing both.
		0x40: "ff", 0x41: "fg", 0x42: "fh",
	}
	for code, want := range cases {
		got, ok := c.Text(code)
		if !ok || got != want {
			t.Errorf("Text(0x%04X) = %q ok=%v, want %q", code, got, ok, want)
		}
	}
	// Outside every declared range.
	if _, ok := c.Text(0x23); ok {
		t.Error("code past the end of a bfrange resolved")
	}
	if _, ok := c.Text(0x33); ok {
		t.Error("code past the end of an array bfrange resolved")
	}
}

func TestParseSurrogatePairsInDestinations(t *testing.T) {
	// /ToUnicode destinations are UTF-16BE, so a character outside the BMP
	// arrives as a surrogate pair. Decoding the units independently would yield
	// two unpaired surrogates and corrupt the output string, which is the kind of
	// damage that shows up as replacement characters far from its cause.
	src := `1 beginbfchar
<0001> <D83DDE00>
endbfchar`
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, ok := c.Text(1)
	if !ok {
		t.Fatal("surrogate pair destination did not resolve")
	}
	if got != "\U0001F600" {
		t.Errorf("Text(1) = %q (% x), want U+1F600", got, got)
	}
}

func TestParseCIDRangesAndChars(t *testing.T) {
	// The encoding-CMap side: codes map to CIDs, not to text. An embedded CMap
	// like this is how a composite font addresses glyphs when it does not use
	// Identity-H.
	src := `1 begincodespacerange
<0000> <FFFF>
endcodespacerange
2 begincidrange
<0020> <0022> 1
<0100> <0101> 100
endcidrange
1 begincidchar
<0500> 999
endcidchar`

	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cases := map[uint32]uint32{
		0x20: 1, 0x21: 2, 0x22: 3,
		0x100: 100, 0x101: 101,
		0x500: 999,
	}
	for code, want := range cases {
		got, ok := c.CID(code)
		if !ok || got != want {
			t.Errorf("CID(0x%04X) = %d ok=%v, want %d", code, got, ok, want)
		}
	}
	// CID 0 is the notdef glyph, so "absent" and "zero" are different answers and
	// the API has to distinguish them.
	if cid, ok := c.CID(0x23); ok {
		t.Errorf("unmapped code returned CID %d; absence must be reported as absence", cid)
	}
}

func TestCodespaceRangesDecideByteWidth(t *testing.T) {
	// The mixed-width case, which is where a reader that assumes two-byte codes
	// goes wrong. Bytes 0x00-0x80 are single codes; 0x81-0x9F begins a two-byte
	// code. This is the shape of the Japanese predefined CMaps, and the string
	// below is deliberately ambiguous under a fixed-width reading.
	src := `2 begincodespacerange
<00> <80>
<8140> <9FFC>
endcodespacerange`
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := c.Codes([]byte{0x41, 0x81, 0x40, 0x42, 0x9F, 0xFC})
	want := []Code{
		{0x41, 1},   // single byte, in the first range
		{0x8140, 2}, // two bytes, in the second
		{0x42, 1},
		{0x9FFC, 2},
	}
	if len(got) != len(want) {
		t.Fatalf("Codes gave %d codes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("code %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// A byte in no range still has to consume something, or the loop cannot
	// advance. Consuming the shortest declared width keeps every later code
	// aligned, which matters more than the unmapped code itself.
	got = c.Codes([]byte{0xFF, 0x41})
	if len(got) != 2 {
		t.Fatalf("an out-of-range byte gave %d codes, want 2: %+v", len(got), got)
	}
	if got[0].Bytes != 1 || got[0].Value != 0xFF {
		t.Errorf("out-of-range byte = %+v, want {0xFF 1}", got[0])
	}
	if got[1].Value != 0x41 {
		t.Errorf("the code after an out-of-range byte = %+v, want value 0x41: "+
			"a lost byte boundary corrupts everything after it", got[1])
	}
}

func TestCodesTotalBytesAlwaysMatchInput(t *testing.T) {
	// Whatever the CMap says and whatever the string holds, the widths must sum
	// to the input length. If they do not, either a byte was dropped or one was
	// read twice, and both desynchronize the text without any error.
	cmaps := map[string]*CMap{}
	id, _ := Identity("Identity-H")
	cmaps["Identity-H"] = id
	cmaps["two-byte fallback"] = TwoByte("UniJIS-UCS2-H")

	mixed, err := Parse([]byte("2 begincodespacerange\n<00> <80>\n<8140> <9FFC>\nendcodespacerange"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cmaps["mixed width"] = mixed

	empty, err := Parse([]byte("begincmap\nendcmap"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cmaps["no codespace declared"] = empty

	inputs := [][]byte{
		{},
		{0x00},
		{0xFF},
		{0x41, 0x42},
		{0x81, 0x40},
		{0x81},                         // truncated two-byte code
		{0x00, 0x41, 0x00, 0x42, 0x00}, // odd length under two-byte codes
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // nothing in range, odd length
	}

	for name, c := range cmaps {
		for _, in := range inputs {
			total := 0
			for _, code := range c.Codes(in) {
				if code.Bytes < 1 {
					t.Fatalf("%s: zero-width code on % x would loop forever", name, in)
				}
				total += code.Bytes
			}
			if total != len(in) {
				t.Errorf("%s: codes of % x consumed %d bytes, want %d", name, in, total, len(in))
			}
		}
	}
}

func TestParseToleratesMalformedInput(t *testing.T) {
	// A CMap stream is untrusted document data, and the useful behavior is to
	// keep whatever mappings are recoverable. Each of these must return without
	// hanging, and the ones with a valid mapping must keep it.
	cases := []struct {
		name string
		src  string
		code uint32
		want string
	}{
		{
			name: "unterminated section",
			src:  "1 beginbfchar\n<0041> <0061>",
			code: 0x41, want: "a",
		},
		{
			name: "section count disagrees with contents",
			src:  "99 beginbfchar\n<0041> <0061>\nendbfchar",
			code: 0x41, want: "a",
		},
		{
			name: "junk between entries",
			src:  "2 beginbfchar\n<0041> <0061>\n/Nonsense 42 true\n<0042> <0062>\nendbfchar",
			code: 0x42, want: "b",
		},
		{
			name: "odd byte in destination",
			src:  "1 beginbfchar\n<0041> <00610>\nendbfchar",
			code: 0x41, want: "a",
		},
		{
			name: "bfrange with reversed bounds",
			src:  "2 beginbfrange\n<0050> <0020> <0041>\nendbfrange\n1 beginbfchar\n<0041> <0061>\nendbfchar",
			code: 0x41, want: "a",
		},
		{
			name: "array longer than its range",
			src:  "1 beginbfrange\n<0041> <0041> [<0061> <0062> <0063>]\nendbfrange",
			code: 0x41, want: "a",
		},
		{
			name: "nested begin without end",
			src:  "1 beginbfchar\n<0041> <0061>\nbeginbfchar\nendbfchar",
			code: 0x41, want: "a",
		},
		{
			name: "empty",
			src:  "",
		},
		{
			name: "only whitespace and comments",
			src:  "  \n% a comment\n\t",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cm, err := Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("Parse returned an error on recoverable input: %v", err)
			}
			if c.want == "" {
				return
			}
			got, ok := cm.Text(c.code)
			if !ok || got != c.want {
				t.Errorf("Text(0x%04X) = %q ok=%v, want %q", c.code, got, ok, c.want)
			}
		})
	}
}

func TestParseRejectsResourceExhaustion(t *testing.T) {
	// Bounded because the input is untrusted and each of these constructs is a
	// few bytes that ask for a large table.
	t.Run("wide bfrange span", func(t *testing.T) {
		// 0x0000 to 0xFFFFFFFF is 4 billion entries from 22 bytes of input. The
		// range is skipped rather than erroring the file, so a later valid
		// mapping survives.
		src := "1 beginbfrange\n<00000000> <FFFFFFFF> <0041>\nendbfrange\n" +
			"1 beginbfchar\n<0042> <0062>\nendbfchar"
		c, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if _, texts := c.Entries(); texts != 1 {
			t.Errorf("stored %d mappings, want 1: the wide range must be skipped, not materialized", texts)
		}
		if got, ok := c.Text(0x42); !ok || got != "b" {
			t.Errorf("the valid mapping after a rejected range was lost: %q ok=%v", got, ok)
		}
	})

	t.Run("many entries", func(t *testing.T) {
		// Enough bfchar entries to exceed maxEntries, which must error rather
		// than grow the map without limit.
		var b strings.Builder
		b.WriteString("1 beginbfchar\n")
		for i := 0; i <= maxEntries; i++ {
			fmt.Fprintf(&b, "<%08X> <0041>\n", i)
		}
		b.WriteString("endbfchar")
		if _, err := Parse([]byte(b.String())); err == nil {
			t.Errorf("Parse accepted more than %d mappings", maxEntries)
		}
	})

	t.Run("many codespace ranges", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("1 begincodespacerange\n")
		for i := 0; i <= maxCodespaceRanges; i++ {
			b.WriteString("<0000> <FFFF>\n")
		}
		b.WriteString("endcodespacerange")
		if _, err := Parse([]byte(b.String())); err == nil {
			t.Errorf("Parse accepted more than %d codespace ranges", maxCodespaceRanges)
		}
	})

	t.Run("token flood", func(t *testing.T) {
		// A file of nothing but operands must not accumulate them without bound.
		src := strings.Repeat("1 ", maxTokens+1)
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("Parse accepted more than %d tokens", maxTokens)
		}
	})
}

func TestUseCMapInheritsIdentity(t *testing.T) {
	// No CMap in this repo's corpus uses the operator, but a file that does would
	// otherwise lose its codespace ranges and get its codes split as single
	// bytes.
	src := `/Identity-H usecmap
1 beginbfchar
<0041> <0061>
endbfchar`
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := c.Codes([]byte{0x00, 0x41})
	if len(got) != 1 || got[0] != (Code{0x0041, 2}) {
		t.Errorf("Codes = %+v, want one two-byte code 0x0041: the inherited codespace was not applied", got)
	}
	// An explicit bfchar entry still wins over the identity CID mapping, because
	// they answer different questions.
	if text, ok := c.Text(0x41); !ok || text != "a" {
		t.Errorf("Text(0x41) = %q ok=%v, want \"a\"", text, ok)
	}
	if cid, ok := c.CID(0x1234); !ok || cid != 0x1234 {
		t.Errorf("CID(0x1234) = %d ok=%v, want 0x1234", cid, ok)
	}
}

func TestUseCMapIgnoresUnknownNames(t *testing.T) {
	// Inheriting from an embedded CMap would need a Store, which this package
	// deliberately does not have. Silently treating an unknown parent as identity
	// would claim CIDs the file never defined.
	c, err := Parse([]byte("/SomeEmbeddedCMap usecmap\nbegincmap\nendcmap"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cid, ok := c.CID(0x1234); ok {
		t.Errorf("CID(0x1234) = %d after inheriting an unknown CMap; codes must not be claimed as CIDs", cid)
	}
}

func TestDestinationForms(t *testing.T) {
	// The forms a bfchar or bfrange destination may take, and the ones it may
	// not. A name destination is legal and resolves through the Adobe Glyph List;
	// dropping it would lose the character with no error, which is exactly the
	// failure this toolkit exists to avoid.
	cases := []struct {
		name string
		src  string
		code uint32
		want string
		ok   bool
		why  string
	}{
		{
			name: "hex string", src: "1 beginbfchar\n<0041> <0061>\nendbfchar",
			code: 0x41, want: "a", ok: true,
			why: "the ordinary form: UTF-16BE",
		},
		{
			name: "literal string", src: "1 beginbfchar\n<0041> (\x00a)\nendbfchar",
			code: 0x41, want: "a", ok: true,
			why: "a literal string is the same bytes written differently, so it decodes the same way",
		},
		{
			name: "multi-character string", src: "1 beginbfchar\n<0041> <00660069>\nendbfchar",
			code: 0x41, want: "fi", ok: true,
			why: "one code may stand for several characters, which is how a ligature glyph maps",
		},
		{
			name: "glyph name", src: "1 beginbfchar\n<0041> /eacute\nendbfchar",
			code: 0x41, want: "é", ok: true,
			why: "a name destination resolves through the Adobe Glyph List",
		},
		{
			name: "ligature glyph name", src: "1 beginbfchar\n<0041> /f_i\nendbfchar",
			code: 0x41, ok: true, want: "fi",
			why: "the AGL's underscore form, which needs several characters to represent",
		},
		{
			name: "unresolvable glyph name", src: "1 beginbfchar\n<0041> /g42\nendbfchar",
			code: 0x41, ok: false,
			why: "a glyph index carries no character meaning, so there is nothing to map",
		},
		{
			name: "integer", src: "1 beginbfchar\n<0041> 97\nendbfchar",
			code: 0x41, ok: false,
			why: "not a form the spec defines for bfchar; accepting it would guess at what a producer meant",
		},
		{
			name: "empty string", src: "1 beginbfchar\n<0041> <>\nendbfchar",
			code: 0x41, want: "", ok: true,
			why: "an empty destination is a legal mapping to nothing, distinct from an absent mapping",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cm, err := Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, ok := cm.Text(c.code)
			if ok != c.ok {
				t.Fatalf("Text(0x%02X) = %q ok=%v, want ok=%v: %s", c.code, got, ok, c.ok, c.why)
			}
			if ok && got != c.want {
				t.Errorf("Text(0x%02X) = %q, want %q: %s", c.code, got, c.want, c.why)
			}
		})
	}
}

func TestSectionsEndedByEOFOrJunkKeepWhatTheyRead(t *testing.T) {
	// Every section reader has to handle three endings: its own end operator, the
	// end of the stream, and a token that belongs to neither. Truncated CMap
	// streams are common enough — a producer that miscounted a /Length, a file cut
	// short — that losing the whole mapping over one is the wrong trade.
	cases := []struct {
		name string
		src  string
		// check runs against the parsed CMap.
		check func(*testing.T, *CMap)
	}{
		{
			name: "codespacerange cut off mid-pair",
			src:  "2 begincodespacerange\n<0000> <FFFF>\n<0000>",
			check: func(t *testing.T, c *CMap) {
				// The complete range must survive, so codes still split correctly.
				got := c.Codes([]byte{0x00, 0x41})
				if len(got) != 1 || got[0].Bytes != 2 {
					t.Errorf("Codes = %+v, want one two-byte code", got)
				}
			},
		},
		{
			name: "codespacerange with mismatched widths",
			src:  "2 begincodespacerange\n<00> <FFFF>\n<0000> <FFFF>\nendcodespacerange",
			check: func(t *testing.T, c *CMap) {
				// A range whose bounds differ in width has no byte length, so it is
				// skipped while the valid one is kept.
				got := c.Codes([]byte{0x00, 0x41})
				if len(got) != 1 || got[0].Bytes != 2 {
					t.Errorf("Codes = %+v, want one two-byte code from the valid range", got)
				}
			},
		},
		{
			name: "cidrange cut off after the low code",
			src:  "1 begincidrange\n<0020> <0022> 1\nendcidrange\n1 begincidrange\n<0100>",
			check: func(t *testing.T, c *CMap) {
				if cid, ok := c.CID(0x20); !ok || cid != 1 {
					t.Errorf("CID(0x20) = %d ok=%v, want 1: the complete range was lost", cid, ok)
				}
			},
		},
		{
			name: "cidrange with a non-integer destination",
			src:  "2 begincidrange\n<0020> <0022> /NotANumber\n<0030> <0031> 5\nendcidrange",
			check: func(t *testing.T, c *CMap) {
				if cid, ok := c.CID(0x30); !ok || cid != 5 {
					t.Errorf("CID(0x30) = %d ok=%v, want 5", cid, ok)
				}
				if _, ok := c.CID(0x20); ok {
					t.Error("a range with an unusable destination was stored anyway")
				}
			},
		},
		{
			name: "cidchar cut off after the code",
			src:  "2 begincidchar\n<0500> 999\n<0501>",
			check: func(t *testing.T, c *CMap) {
				if cid, ok := c.CID(0x500); !ok || cid != 999 {
					t.Errorf("CID(0x500) = %d ok=%v, want 999", cid, ok)
				}
			},
		},
		{
			name: "cidchar with a negative CID",
			src:  "2 begincidchar\n<0500> -1\n<0501> 7\nendcidchar",
			check: func(t *testing.T, c *CMap) {
				if _, ok := c.CID(0x500); ok {
					t.Error("a negative CID was stored; CIDs are unsigned glyph selectors")
				}
				if cid, ok := c.CID(0x501); !ok || cid != 7 {
					t.Errorf("CID(0x501) = %d ok=%v, want 7", cid, ok)
				}
			},
		},
		{
			name: "bfchar cut off after the code",
			src:  "2 beginbfchar\n<0041> <0061>\n<0042>",
			check: func(t *testing.T, c *CMap) {
				if got, ok := c.Text(0x41); !ok || got != "a" {
					t.Errorf("Text(0x41) = %q ok=%v, want \"a\"", got, ok)
				}
			},
		},
		{
			name: "bfrange array cut off",
			src:  "1 beginbfrange\n<0041> <0043> [<0061>",
			check: func(t *testing.T, c *CMap) {
				if got, ok := c.Text(0x41); !ok || got != "a" {
					t.Errorf("Text(0x41) = %q ok=%v, want \"a\"", got, ok)
				}
			},
		},
		{
			name: "bfrange cut off before its destination",
			src:  "1 beginbfchar\n<0041> <0061>\nendbfchar\n1 beginbfrange\n<0050> <0052>",
			check: func(t *testing.T, c *CMap) {
				if got, ok := c.Text(0x41); !ok || got != "a" {
					t.Errorf("Text(0x41) = %q ok=%v, want \"a\": an earlier section was lost", got, ok)
				}
			},
		},
		{
			name: "cid sections mixed with junk operators",
			src:  "2 begincidrange\nfoo\n<0020> <0021> 3\nbar\nendcidrange",
			check: func(t *testing.T, c *CMap) {
				if cid, ok := c.CID(0x21); !ok || cid != 4 {
					t.Errorf("CID(0x21) = %d ok=%v, want 4", cid, ok)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cm, err := Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("Parse returned an error on recoverable input: %v", err)
			}
			c.check(t, cm)
		})
	}
}

func TestWideCIDRangeIsSkipped(t *testing.T) {
	// The cidrange counterpart of the bfrange span guard: a few bytes must not
	// buy a four-billion-entry map.
	src := "1 begincidrange\n<00000000> <FFFFFFFF> 1\nendcidrange\n" +
		"1 begincidchar\n<0042> 7\nendcidchar"
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cids, _ := c.Entries()
	if cids != 1 {
		t.Errorf("stored %d CID mappings, want 1: the wide range must be skipped", cids)
	}
	if cid, ok := c.CID(0x42); !ok || cid != 7 {
		t.Errorf("CID(0x42) = %d ok=%v, want 7: the valid mapping after a rejected range was lost", cid, ok)
	}
}

func TestCMapNameOnlyFromCMapNameDef(t *testing.T) {
	// def is reached for every "/Key value def" in the file, and only /CMapName
	// may set the name. A parser that took the last name it saw would report
	// /Adobe or /UCS from the /CIDSystemInfo dictionary.
	src := `/Registry (Adobe) def
/Ordering /UCS def
/CMapName /Real-Name def
/WMode 0 def`
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name != "Real-Name" {
		t.Errorf("Name = %q, want Real-Name", c.Name)
	}

	// A def with too few operands, or a non-name value, must leave the name alone
	// rather than panic on the missing operand.
	for _, src := range []string{"def", "/CMapName def", "/CMapName 42 def"} {
		c, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		if c.Name != "" {
			t.Errorf("Parse(%q) set Name = %q, want empty", src, c.Name)
		}
	}
}

func TestIncrementLastAdvancesOnlyTheFinalCharacter(t *testing.T) {
	cases := []struct {
		base string
		n    uint32
		want string
		why  string
	}{
		{"A", 0, "A", "the first code of a range takes the destination unchanged"},
		{"A", 25, "Z", "within the ASCII letters"},
		{"ff", 1, "fg", "only the last character advances, per the 'last byte' rule"},
		{"", 5, "", "an empty destination has nothing to advance"},
		{"\U0010FFFF", 1, "\U0010FFFF", "advancing past the Unicode maximum returns the base rather than an invalid rune"},
	}
	for _, c := range cases {
		if got := incrementLast(c.base, c.n); got != c.want {
			t.Errorf("incrementLast(%q, %d) = %q, want %q: %s", c.base, c.n, got, c.want, c.why)
		}
	}
}

func TestTwoByteFallbackSplitsButClaimsNothing(t *testing.T) {
	// A composite font naming a predefined CMap this package does not carry still
	// needs its string split correctly. Getting the widths right while reporting
	// no CIDs lets the caller fall back to /ToUnicode; guessing one-byte codes
	// would double the glyph count.
	c := TwoByte("UniGB-UCS2-H")
	if c.Name != "UniGB-UCS2-H" {
		t.Errorf("Name = %q, want UniGB-UCS2-H", c.Name)
	}
	got := c.Codes([]byte{0x4E, 0x2D, 0x65, 0x87})
	want := []Code{{0x4E2D, 2}, {0x6587, 2}}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("Codes = %+v, want %+v", got, want)
		}
	}
	if cid, ok := c.CID(0x4E2D); ok {
		t.Errorf("CID(0x4E2D) = %d; the fallback must not invent CIDs it cannot know", cid)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange"))
	f.Add([]byte("1 beginbfchar\n<0041> <0061>\nendbfchar"))
	f.Add([]byte("1 beginbfrange\n<0020> <0030> <0041>\nendbfrange"))
	f.Add([]byte("1 beginbfrange\n<0020> <0022> [<0041> <0042> <0043>]\nendbfrange"))
	f.Add([]byte("1 begincidrange\n<0000> <00FF> 1\nendcidrange"))
	f.Add([]byte("/Identity-H usecmap"))
	f.Add([]byte("/CMapName /Test def"))

	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Parse(data)
		if err != nil {
			return
		}
		// The invariant that matters on arbitrary input: splitting a string must
		// consume it exactly. A zero-width code would loop forever and an
		// over-wide one would read past the end.
		for _, in := range [][]byte{{}, {0x41}, {0x00, 0x41}, {0xFF, 0xFF, 0xFF}} {
			total := 0
			for _, code := range c.Codes(in) {
				if code.Bytes < 1 || code.Bytes > 4 {
					t.Fatalf("code width %d out of range", code.Bytes)
				}
				total += code.Bytes
			}
			if total != len(in) {
				t.Fatalf("codes of % x consumed %d bytes, want %d", in, total, len(in))
			}
		}
	})
}

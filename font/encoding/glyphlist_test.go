package encoding

import (
	"strings"
	"testing"
)

// GlyphText's arithmetic rules are what keep this package useful beyond the
// names it happens to list. Subsetting tools and PDF generators synthesize glyph
// names freely, so a resolver that only consults a table loses every glyph whose
// name its author did not anticipate — and the table can never be complete.
//
// The rules also have to fail closed. A name that carries no character meaning
// must resolve to nothing, because emitting a plausible wrong character is worse
// than emitting none: the caller can fall back to /ToUnicode for a hole, but it
// cannot detect a confident lie.

func TestGlyphTextResolutionRules(t *testing.T) {
	cases := []struct {
		name  string
		glyph string
		want  string
		ok    bool
		why   string
	}{
		{
			name: "table entry", glyph: "eacute", want: "é", ok: true,
			why: "a listed name maps to its listed value",
		},
		{
			name: "generated latin letter", glyph: "Q", want: "Q", ok: true,
			why: "the letters are filled in by init, not written out",
		},
		{
			name: "uni form", glyph: "uni0041", want: "A", ok: true,
			why: "the standard four-digit form",
		},
		{
			name: "uni form, several groups", glyph: "uni004100420043", want: "ABC", ok: true,
			why: "the uni form takes one code point per four digits, which is how it names a ligature",
		},
		{
			name: "uni form with a partial group", glyph: "uni004100", ok: false,
			why: "seven digits cannot be split into four-digit groups, so the name is not this form",
		},
		{
			name: "u form, four digits", glyph: "u2014", want: "—", ok: true,
			why: "uXXXX is the four-to-six-digit form",
		},
		{
			name: "u form, five digits", glyph: "u1D400", want: "\U0001D400", ok: true,
			why: "a mathematical alphanumeric, which needs five digits",
		},
		{
			name: "u form, six digits", glyph: "u10FFFF", want: "\U0010FFFF", ok: true,
			why: "the highest code point Unicode defines",
		},
		{
			name: "u form, seven digits", glyph: "u0010FFFF", ok: false,
			why: "beyond the form's defined length, so the name means something else",
		},
		{
			name: "u form, three digits", glyph: "u041", ok: false,
			why: "below the form's minimum length; 'u041' is a name, not a code point",
		},
		{
			name: "u form above the Unicode maximum", glyph: "u110000", ok: false,
			why: "parses as hex but is not a character",
		},
		{
			name: "u form naming a surrogate half", glyph: "uD800", ok: false,
			why: "a lone surrogate is not a character and would corrupt the output string",
		},
		{
			name: "uni form naming a surrogate half", glyph: "uniDC00", ok: false,
			why: "same rule through the uni path",
		},
		{
			name: "not hex at all", glyph: "unicorn", ok: false,
			why: "has the uni prefix and four trailing characters, but they are not digits",
		},
		{
			name: "small caps variant", glyph: "a.sc", want: "a", ok: true,
			why: "a period suffix is a stylistic variant of the base glyph",
		},
		{
			name: "oldstyle figure", glyph: "one.oldstyle", want: "1", ok: true,
			why: "the most common synthesized suffix in real documents",
		},
		{
			name: "suffix on a uni name", glyph: "uni2014.alt", want: "—", ok: true,
			why: "stripping the suffix leaves a name the arithmetic rules resolve",
		},
		{
			name: "stacked suffixes", glyph: "f.alt.sc", want: "f", ok: true,
			why: "the first period wins, so nesting collapses to the base name",
		},
		{
			name: "leading period", glyph: ".notdef", ok: false,
			why: "the conventional name for the empty glyph; stripping at index 0 would leave nothing, and it must not resolve to whatever follows",
		},
		{
			name: "bare period", glyph: ".", ok: false,
			why: "no base name to fall back to",
		},
		{
			name: "glyph index by number", glyph: "g42", ok: false,
			why: "identifies a glyph by position in the font program, which carries no character meaning",
		},
		{
			name: "CID reference", glyph: "cid1234", ok: false,
			why: "same: a CID is an index into a font, resolvable only through that font's CMap",
		},
		{
			name: "index with a suffix", glyph: "g42.alt", ok: false,
			why: "suffix stripping must not turn an unresolvable name into a resolvable one",
		},
		{
			name: "empty", glyph: "", ok: false,
			why: "an unmapped code has no name, and the empty string must not resolve",
		},
		{
			name: "unknown name", glyph: "someArbitraryName", ok: false,
			why: "no rule applies, so the answer is 'unknown' rather than a guess",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := GlyphText(c.glyph)
			if ok != c.ok {
				t.Fatalf("GlyphText(%q) = %q resolved=%v, want %v: %s", c.glyph, got, ok, c.ok, c.why)
			}
			if ok && got != c.want {
				t.Errorf("GlyphText(%q) = %q, want %q: %s", c.glyph, got, c.want, c.why)
			}
			if !ok && got != "" {
				t.Errorf("GlyphText(%q) failed but returned %q, want empty", c.glyph, got)
			}
		})
	}
}

func TestLigatureComponentNames(t *testing.T) {
	// An underscore in a glyph name joins the names of the glyphs a ligature
	// draws. This rule is not decoration: "f_f" and "f_t" both appear in this
	// repo's corpus, and "f_t" has no precomposed code point at all, so a
	// resolver returning one rune cannot represent it and drops the characters
	// with no error. That is the "efficient" → "ecient" failure exactly.
	cases := []struct {
		glyph string
		want  string
		ok    bool
		why   string
	}{
		{"f_f", "ff", true, "two components, and U+FB00 exists but the name says components"},
		{"f_t", "ft", true, "no precomposed code point exists, so components are the only answer"},
		{"f_f_i", "ffi", true, "three components"},
		{"f_i.alt", "fi", true, "the suffix is stripped before the split, so styled ligatures resolve"},
		{"uni0066_uni0069", "fi", true, "components may themselves use the arithmetic forms"},
		{"f_g42", "", false, "one unresolvable component makes the whole name unresolvable, because a partial answer would drop a character silently"},
		{"f_", "", false, "an empty component cannot resolve"},
		{"_f", "", false, "same at the front"},
		{"_", "", false, "no components at all"},
	}
	for _, c := range cases {
		got, ok := GlyphText(c.glyph)
		if ok != c.ok {
			t.Errorf("GlyphText(%q) = %q resolved=%v, want %v: %s", c.glyph, got, ok, c.ok, c.why)
			continue
		}
		if got != c.want {
			t.Errorf("GlyphText(%q) = %q, want %q: %s", c.glyph, got, c.want, c.why)
		}
	}

	// GlyphRune is the narrow form and must decline rather than truncate: an "ff"
	// that came back as 'f' would be the silent character loss this whole design
	// avoids.
	if r, ok := GlyphRune("f_f"); ok {
		t.Errorf("GlyphRune(\"f_f\") returned U+%04X; a two-character glyph has no single rune", r)
	}
	if r, ok := GlyphRune("fi"); !ok || r != 0xFB01 {
		t.Errorf("GlyphRune(\"fi\") = U+%04X ok=%v, want U+FB01: the precomposed ligature is one rune", r, ok)
	}
}

func TestGlyphRuneAgreesWithLatinArithmetic(t *testing.T) {
	// The 52 Latin letters are generated, so the check has to come from outside
	// the generator: every letter must resolve to its own code point, and the
	// uniXXXX form of that code point must resolve to the same rune. These are
	// the most-used entries in the table and a wrong one would be everywhere.
	for r := rune('A'); r <= 'Z'; r++ {
		for _, name := range []string{string(r), string(r + 32)} {
			want := []rune(name)[0]
			got, ok := GlyphRune(name)
			if !ok || got != want {
				t.Errorf("GlyphRune(%q) = U+%04X ok=%v, want U+%04X", name, got, ok, want)
			}
		}
	}
}

func TestRecursionOnHostileNamesTerminates(t *testing.T) {
	// Both the suffix and ligature rules recurse, and the input is a glyph name
	// from an untrusted document. A long run of separators must terminate rather
	// than recurse once per separator until the stack is gone.
	dots := strings.Repeat(".", 100000)
	if _, ok := GlyphText(dots); ok {
		t.Error("a name of only periods resolved")
	}
	if _, ok := GlyphText("a" + dots); !ok {
		t.Error("a valid base name followed by many periods failed to resolve")
	}

	// The ligature rule splits rather than recursing per character, but each
	// component recurses once, so a long run of them is the case to check.
	unders := strings.Repeat("_", 100000)
	if _, ok := GlyphText(unders); ok {
		t.Error("a name of only underscores resolved")
	}
	got, ok := GlyphText(strings.TrimSuffix(strings.Repeat("f_", 50000), "_"))
	if !ok {
		t.Fatal("a long run of valid components failed to resolve")
	}
	if len(got) != 50000 {
		t.Errorf("resolved to %d characters, want 50000", len(got))
	}
}

func TestKnownAmbiguousNames(t *testing.T) {
	// The AGL's two documented traps, asserted directly rather than only through
	// the encoding tables. Getting these backwards is easy — the suffixed name
	// looks like the special case when it is in fact the plain Greek letter — and
	// tables_test.go only reaches them at the codes MacRomanEncoding happens to
	// use.
	cases := []struct {
		glyph string
		want  rune
		why   string
	}{
		{"Delta", 0x2206, "unsuffixed Delta is INCREMENT, the technical symbol"},
		{"Deltagreek", 0x0394, "the Greek letter carries the suffix"},
		{"Omega", 0x2126, "unsuffixed Omega is OHM SIGN"},
		{"Omegagreek", 0x03A9, "the Greek letter carries the suffix"},
		{"mu", 0x00B5, "MICRO SIGN, the Latin-1 character"},
		{"mugreek", 0x03BC, "GREEK SMALL LETTER MU"},
	}
	for _, c := range cases {
		got, ok := GlyphRune(c.glyph)
		if !ok {
			t.Errorf("GlyphRune(%q) did not resolve: %s", c.glyph, c.why)
			continue
		}
		if got != c.want {
			t.Errorf("GlyphRune(%q) = U+%04X, want U+%04X: %s", c.glyph, got, c.want, c.why)
		}
	}
}

func TestGlyphListValuesAreValidRunes(t *testing.T) {
	// A typo that produced a surrogate or an out-of-range value in the table
	// would pass every mapping test that only checks the codes it looks at, then
	// emit a replacement character or corrupt UTF-8 for one glyph somewhere.
	for name, r := range glyphList {
		if r < 0 {
			t.Errorf("glyph %q maps to a negative value", name)
			continue
		}
		if _, ok := hexRune(uint64(r)); !ok {
			t.Errorf("glyph %q maps to U+%04X, which is not a usable character", name, r)
		}
		if r == 0 {
			t.Errorf("glyph %q maps to U+0000, which Rune uses to mean unmapped", name)
		}
	}
}

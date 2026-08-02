// Package encoding maps simple-font character codes to glyph names and Unicode.
//
// A simple font addresses glyphs with single bytes, and the meaning of a byte
// comes from a base encoding (ISO 32000-2 Annex D) optionally modified by a
// /Differences array (§9.6.5.1). Resolving a code therefore takes two steps:
// code to glyph name, then glyph name to Unicode via the Adobe Glyph List.
//
// Both steps are needed because a large share of real fonts ship no /ToUnicode
// CMap: 55 of the 134 distinct simple fonts across this repo's corpus, every one
// of them WinAnsiEncoding. An extractor that relies on /ToUnicode alone returns
// nothing for those, which is one of the ways existing tools lose text. The
// count is measured rather than estimated, by TestCorpusFontsWithoutToUnicode-
// HaveAnEncoding in cmd/pdfspec, which also asserts every one of them is
// recoverable through this package.
//
// This package holds no PDF object knowledge: it takes glyph names and byte
// codes, not dictionaries. Reading /Encoding and /Differences out of a font
// dictionary belongs to the font package, which keeps the tables here testable
// against the Unicode data they claim to implement.
package encoding

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Encoding is a code-to-glyph mapping: the 256 glyph names of a base encoding,
// with any /Differences already applied.
//
// The zero Encoding maps nothing, which is the correct starting point for a
// symbolic font that supplies its own /Differences for every code it uses.
type Encoding struct {
	names [256]string
	text  [256]string
}

// Base returns the named base encoding, or false if the name is not one of the
// four defined by Annex D.
//
// The returned Encoding is a copy, so a caller may apply /Differences to it
// without disturbing the shared table.
func Base(name string) (*Encoding, bool) {
	names, ok := baseTables[name]
	if !ok {
		return nil, false
	}
	e := &Encoding{names: *names}
	e.resolve()
	return e, true
}

// Standard returns StandardEncoding, the fallback when a font names no encoding
// and is not symbolic (§9.6.5.1).
func Standard() *Encoding {
	e, _ := Base("StandardEncoding")
	return e
}

// Empty returns an Encoding that maps no code, for a symbolic font whose codes
// mean only what its /Differences say.
func Empty() *Encoding { return &Encoding{} }

// Clone returns a copy, so /Differences can be applied without mutating a
// shared table.
func (e *Encoding) Clone() *Encoding {
	c := *e
	return &c
}

// Set assigns a glyph name to a code and resolves its text. This is how one
// entry of a /Differences array is applied.
func (e *Encoding) Set(code byte, glyph string) {
	e.names[code] = glyph
	e.text[code], _ = GlyphText(glyph)
}

// Glyph returns the glyph name for a code, or "" when the code is unmapped.
func (e *Encoding) Glyph(code byte) string { return e.names[code] }

// Text returns the characters a code stands for, or "" when the code is
// unmapped or its glyph name has no known Unicode value.
//
// A string rather than a rune because a glyph is not always one character: an
// "ffi" ligature is one glyph and three characters, and the AGL names such
// glyphs by joining their components with underscores. Returning a rune would
// force this package to drop them, which is how "efficient" becomes "ecient".
//
// The empty string rather than an error, and rather than U+FFFD: an unmapped
// code is routine in a symbolic font, and the caller decides whether to drop
// the glyph or fall back to a /ToUnicode entry.
func (e *Encoding) Text(code byte) string { return e.text[code] }

// Rune returns the single character a code stands for, or 0 when the code is
// unmapped or stands for more than one character.
//
// Convenience for the common case and for tests. Anything extracting text
// should use Text, or it silently loses every ligature.
func (e *Encoding) Rune(code byte) rune {
	s := e.text[code]
	if s == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(s)
	if size != len(s) {
		return 0
	}
	return r
}

// resolve fills the text table from the name table.
func (e *Encoding) resolve() {
	for i, n := range e.names {
		if n == "" {
			continue
		}
		e.text[i], _ = GlyphText(n)
	}
}

// GlyphText returns the characters a glyph name stands for.
//
// Four rules apply in order, per the Adobe Glyph List specification:
//
//  1. A name in the glyph list maps to its listed value.
//  2. A uniXXXX or uXXXX[XX] name maps to those code points directly. Producers
//     emit these for glyphs with no conventional name, and they are the reason
//     a table alone is not enough. The uni form may carry several code points,
//     one per four digits.
//  3. A name with a suffix after a period — "a.sc", "one.oldstyle" — maps as
//     its base name does. Subsetting tools add these freely, and dropping the
//     suffix recovers text that would otherwise be lost entirely.
//  4. A name joined by underscores — "f_f", "f_t" — is a ligature, and maps to
//     its components' characters in order. Both of those appear in this repo's
//     corpus, and "f_t" has no precomposed code point at all, so a resolver
//     that cannot return multiple characters cannot represent it.
//
// A name of the form "gXX" or "cidXX" deliberately does not resolve: those
// identify a glyph by index in the font program, carry no character meaning,
// and guessing would emit plausible wrong text rather than nothing.
func GlyphText(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if r, ok := glyphList[name]; ok {
		return string(r), true
	}

	// uniXXXX: groups of exactly four hex digits, one per code point. A single
	// group is by far the common case; several denote a ligature.
	if rest, ok := strings.CutPrefix(name, "uni"); ok && len(rest) >= 4 && len(rest)%4 == 0 {
		if s, ok := parseHexRuns(rest, 4); ok {
			return s, true
		}
	}

	// uXXXX through uXXXXXX: four to six hex digits, one code point. Unlike the
	// uni form this is never a ligature, because its digits are variable-length
	// and could not be split unambiguously.
	if rest, ok := strings.CutPrefix(name, "u"); ok && len(rest) >= 4 && len(rest) <= 6 {
		if v, err := strconv.ParseUint(rest, 16, 32); err == nil {
			if r, ok := hexRune(v); ok {
				return string(r), true
			}
		}
	}

	// A period suffix is a stylistic variant of the base glyph. Stripped before
	// the underscore split so "f_f.alt" reaches the ligature rule.
	if i := strings.IndexByte(name, '.'); i > 0 {
		return GlyphText(name[:i])
	}

	// An underscore joins the names of a ligature's component glyphs. Every
	// component must resolve: a partial answer would drop characters silently,
	// which is worse than reporting the name as unknown and letting the caller
	// fall back to /ToUnicode.
	if strings.Contains(name, "_") {
		var b strings.Builder
		for _, part := range strings.Split(name, "_") {
			s, ok := GlyphText(part)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		}
		return b.String(), true
	}
	return "", false
}

// GlyphRune returns the single Unicode value of a glyph name, or false when the
// name is unknown or stands for more than one character.
//
// GlyphText is the resolver; this is the narrow form for callers that genuinely
// need one rune.
func GlyphRune(name string) (rune, bool) {
	s, ok := GlyphText(name)
	if !ok {
		return 0, false
	}
	r, size := utf8.DecodeRuneInString(s)
	if size != len(s) {
		return 0, false
	}
	return r, true
}

// parseHexRuns decodes a string of fixed-width hex groups into characters.
func parseHexRuns(s string, width int) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i += width {
		v, err := strconv.ParseUint(s[i:i+width], 16, 32)
		if err != nil {
			return "", false
		}
		r, ok := hexRune(v)
		if !ok {
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

// hexRune converts a parsed hex value to a character, rejecting values that are
// not usable ones. Surrogate halves cannot stand alone, and anything above the
// Unicode maximum is a misinterpreted name rather than a code point.
//
// The bound is checked before the conversion rather than after. Checking after
// would work here — every caller parses at most six hex digits — but it would rest
// on a bound in the caller rather than in this function, and a rune is signed, so
// a value above 2^31 converted first would come out negative and pass a
// greater-than test.
func hexRune(v uint64) (rune, bool) {
	if v > 0x10FFFF {
		return 0, false
	}
	if v >= 0xD800 && v <= 0xDFFF {
		return 0, false
	}
	return rune(v), true
}

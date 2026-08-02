// Package cmap reads PDF CMaps: the code-to-CID mappings of composite fonts and
// the code-to-Unicode mappings of /ToUnicode streams (ISO 32000-2 §9.7.5,
// §9.10.3).
//
// A CMap answers two questions, and conflating them is a common source of wrong
// output. First, how many bytes is the next character code — one, two, or a
// mixture, decided by the codespace ranges. Second, what does that code mean,
// which is either a CID to look a glyph up by, or the text the glyph stands for.
// Both mappings share one syntax, so one parser reads both.
//
// The byte-splitting question is the one that matters most in practice. A reader
// that assumes two-byte codes because Identity-H is common will mis-split every
// mixed-width CMap, and because the result is still a sequence of plausible
// codes it produces confident wrong text rather than an error.
//
// CMap syntax is PostScript, but only a fixed skeleton of it is legal here, so
// this package reads tokens rather than interpreting a language: the operators it
// recognizes are listed in the switch in parse, and everything else is skipped.
// The tokenizer is content's, because the token syntax is the same one.
package cmap

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/3rg0n/pdf-spec/content"
	"github.com/3rg0n/pdf-spec/font/encoding"
	"github.com/3rg0n/pdf-spec/objects"
)

// Bounds on what a CMap may declare. A CMap arrives as untrusted document data,
// and every one of these limits is generous next to real files: the corpus
// maximum is 262 bfchar entries in a single stream and one codespace range.
const (
	maxCodespaceRanges = 256
	maxEntries         = 1 << 20
	maxRangeSpan       = 1 << 16
	maxTokens          = 1 << 22
)

// Code is one character code lifted out of a string, with the byte width it was
// written in.
//
// The width is carried because the caller needs it: a /W array indexes by CID,
// but positioning and error reporting work in bytes, and a two-byte code that
// consumed one byte on a malformed stream must not silently shift everything
// after it.
type Code struct {
	Value uint32
	Bytes int
}

// CMap maps character codes to CIDs, or to text, or both.
//
// Both because a /ToUnicode CMap and a CID CMap are the same object with
// different contents, and a font may have either or both. Absent entries are
// reported as absent rather than as zero: CID 0 is the notdef glyph and the empty
// string is a legitimate mapping, so the caller must be able to tell "no answer"
// from "this answer".
type CMap struct {
	// Name is the /CMapName, when the CMap declares one.
	Name string

	// codespace holds the ranges in declaration order, and is what decides how
	// many bytes each code occupies.
	codespace []codespaceRange

	cid  map[uint32]uint32
	text map[uint32]string

	// identity marks a CMap whose CID equals its code, so no table is needed.
	// Identity-H and Identity-V are by far the most common encoding CMaps —
	// every composite font in this repo's corpus uses Identity-H — and holding
	// 65,536 entries to express f(x) = x would be pure waste.
	identity bool
}

// codespaceRange is one begincodespacerange entry: a low and high code of equal
// byte width.
type codespaceRange struct {
	low, high uint32
	bytes     int
}

// Identity returns the Identity-H or Identity-V CMap, or false for any other
// name.
//
// These two are predefined rather than embedded in the file: two-byte codes
// mapping to the same CID. They are handled here rather than by loading a
// predefined CMap file because they need no file, and because every composite
// font in this repo's corpus uses Identity-H.
func Identity(name string) (*CMap, bool) {
	if name != "Identity-H" && name != "Identity-V" {
		return nil, false
	}
	return &CMap{
		Name:      name,
		identity:  true,
		codespace: []codespaceRange{{low: 0, high: 0xFFFF, bytes: 2}},
	}, true
}

// TwoByte returns a CMap that reads two-byte codes and maps nothing.
//
// This is the fallback for a composite font naming a predefined CMap this
// package does not carry. Every predefined CMap in ISO 32000-2 Table 118 except
// the deprecated ones uses two-byte codes, so splitting the string correctly is
// still possible even when the CIDs are not; the caller then has code widths
// right and can fall back to /ToUnicode for meaning. Guessing one-byte codes
// instead would double the glyph count and garble the text.
func TwoByte(name string) *CMap {
	return &CMap{
		Name:      name,
		codespace: []codespaceRange{{low: 0, high: 0xFFFF, bytes: 2}},
	}
}

// CID returns the CID a code maps to.
func (c *CMap) CID(code uint32) (uint32, bool) {
	if c.identity {
		return code, true
	}
	v, ok := c.cid[code]
	return v, ok
}

// Text returns the characters a code stands for, from a /ToUnicode CMap.
func (c *CMap) Text(code uint32) (string, bool) {
	s, ok := c.text[code]
	return s, ok
}

// Entries reports how many code-to-CID and code-to-text mappings the CMap holds,
// for callers deciding whether it is worth consulting.
func (c *CMap) Entries() (cids, texts int) { return len(c.cid), len(c.text) }

// CodeForText returns a code that maps to the given text.
//
// This exists for one question the font package has to ask: which code is the
// space, so that inter-word gaps can be measured against its width. A composite
// font gives no other way to find it, since a CID carries no character meaning.
//
// The lowest matching code is returned, so the answer does not depend on map
// iteration order. Several codes mapping to the same text is normal — a subset
// font often has more than one space-like glyph — and any of them would have the
// right width, but a stable answer is what makes the caller's behavior
// reproducible.
func (c *CMap) CodeForText(text string) (uint32, bool) {
	found, ok := uint32(0), false
	for code, s := range c.text {
		if s != text {
			continue
		}
		if !ok || code < found {
			found, ok = code, true
		}
	}
	return found, ok
}

// Codes splits a PDF string into character codes.
//
// The splitting follows §9.7.6.2: the codespace ranges are matched by byte
// width, shortest first, and a code whose leading bytes fall in no range is
// still consumed at the width of the shortest range that its first byte could
// begin. That last rule is what keeps a malformed string from desynchronizing
// everything after it — the alternative is to stop, which loses the rest of the
// text for one bad byte.
//
// A CMap with no codespace ranges reads two-byte codes, because a CMap stream
// that omits them is malformed and every composite font this applies to uses
// two-byte codes.
func (c *CMap) Codes(s []byte) []Code {
	if len(s) == 0 {
		return nil
	}
	ranges := c.codespace
	if len(ranges) == 0 {
		ranges = []codespaceRange{{low: 0, high: 0xFFFF, bytes: 2}}
	}

	// Sized for the common case of uniform-width codes; growth beyond it is
	// bounded by len(s).
	out := make([]Code, 0, len(s)/2+1)
	for i := 0; i < len(s); {
		width, value := matchCode(s[i:], ranges)
		out = append(out, Code{Value: value, Bytes: width})
		i += width
	}
	return out
}

// matchCode finds the byte width of the code at the head of s.
func matchCode(s []byte, ranges []codespaceRange) (width int, value uint32) {
	// Try widths in increasing order so a one-byte range wins over a two-byte
	// range that happens to contain the same leading byte, which is the order
	// §9.7.6.2 specifies.
	for n := 1; n <= 4 && n <= len(s); n++ {
		v := beUint(s[:n])
		for _, r := range ranges {
			if r.bytes == n && v >= r.low && v <= r.high {
				return n, v
			}
		}
	}

	// No range matched. Consume the width of the shortest declared range so the
	// stream stays aligned, and report the bytes as they are: an unmapped code is
	// something the caller can fall back on, whereas a lost byte boundary
	// corrupts every code after it.
	shortest := 4
	for _, r := range ranges {
		if r.bytes < shortest {
			shortest = r.bytes
		}
	}
	if shortest > len(s) {
		shortest = len(s)
	}
	return shortest, beUint(s[:shortest])
}

// beUint reads up to four bytes big-endian, which is the byte order every PDF
// character code uses.
func beUint(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return v
}

// Parse reads a CMap from a stream's decoded bytes.
//
// Unrecognized constructs are skipped rather than rejected. A /ToUnicode stream
// carrying a stray PostScript procedure is common, and a parser that fails on it
// throws away every mapping in the file over a construct it did not need to
// understand.
func Parse(data []byte) (*CMap, error) {
	p := &parser{
		lex: content.NewLexer(data),
		cm:  &CMap{cid: map[uint32]uint32{}, text: map[uint32]string{}},
	}
	if err := p.run(); err != nil {
		return nil, err
	}
	if len(p.cm.cid) == 0 {
		p.cm.cid = nil
	}
	if len(p.cm.text) == 0 {
		p.cm.text = nil
	}
	return p.cm, nil
}

type parser struct {
	lex *content.Lexer
	cm  *CMap

	// operands holds the tokens since the last operator. CMap sections are
	// introduced by a count — "3 beginbfchar" — so the count is an operand
	// behind the operator, exactly as in a content stream.
	operands []objects.Object
	tokens   int
}

func (p *parser) run() error {
	for {
		tok := p.lex.Next()
		if tok.Kind == content.KindEOF {
			return nil
		}
		p.tokens++
		if p.tokens > maxTokens {
			return fmt.Errorf("cmap: more than %d tokens, which no real CMap approaches", maxTokens)
		}

		switch tok.Kind {
		case content.KindObject:
			// Operands accumulate, but only a handful are ever needed and a
			// malformed file can hold millions. Keeping the last few is enough
			// because every construct here reads at most one.
			if len(p.operands) >= 8 {
				p.operands = p.operands[1:]
			}
			p.operands = append(p.operands, tok.Val)
			continue

		case content.KindOperator:
			var err error
			switch tok.Op {
			case "begincodespacerange":
				err = p.codespaceRanges()
			case "beginbfchar":
				err = p.bfchars()
			case "beginbfrange":
				err = p.bfranges()
			case "begincidchar":
				err = p.cidchars()
			case "begincidrange":
				err = p.cidranges()
			case "usecmap":
				err = p.useCMap()
			case "def":
				p.def()
			}
			if err != nil {
				return err
			}
		}
		p.operands = p.operands[:0]
	}
}

// def records the values this package cares about from "/Key value def" pairs.
// Everything else — /CIDSystemInfo, /WMode, /CMapType — is read by the font
// package from the font dictionary, or not needed at all.
func (p *parser) def() {
	if len(p.operands) < 2 {
		return
	}
	key, ok := p.operands[len(p.operands)-2].(objects.Name)
	if !ok || key != "CMapName" {
		return
	}
	if name, ok := p.operands[len(p.operands)-1].(objects.Name); ok {
		p.cm.Name = string(name)
	}
}

// useCMap inherits from another CMap: "/Identity-H usecmap".
//
// Only the Identity CMaps are honored, because inheriting from an embedded CMap
// would mean resolving a stream reference, and this package holds no Store. No
// CMap in this repo's corpus uses the operator at all; handling the Identity
// case is what keeps a file that does from losing its codespace ranges.
func (p *parser) useCMap() error {
	if len(p.operands) == 0 {
		return nil
	}
	name, ok := p.operands[len(p.operands)-1].(objects.Name)
	if !ok {
		return nil
	}
	base, ok := Identity(string(name))
	if !ok {
		return nil
	}
	if len(p.cm.codespace) == 0 {
		p.cm.codespace = base.codespace
	}
	p.cm.identity = true
	return nil
}

// codespaceRanges reads begincodespacerange ... endcodespacerange.
func (p *parser) codespaceRanges() error {
	for {
		lo, hi, done, err := p.stringPair("endcodespacerange")
		if err != nil || done {
			return err
		}
		if len(lo) != len(hi) || len(lo) == 0 || len(lo) > 4 {
			// A range whose bounds differ in width has no defined byte length, so
			// there is nothing to record. Skipping it keeps the rest.
			continue
		}
		if len(p.cm.codespace) >= maxCodespaceRanges {
			return fmt.Errorf("cmap: more than %d codespace ranges", maxCodespaceRanges)
		}
		p.cm.codespace = append(p.cm.codespace, codespaceRange{
			low:   beUint(lo),
			high:  beUint(hi),
			bytes: len(lo),
		})
	}
}

// bfchars reads beginbfchar ... endbfchar: single code to destination.
func (p *parser) bfchars() error {
	for {
		tok := p.lex.Next()
		if tok.Kind == content.KindEOF {
			return nil
		}
		if tok.Kind == content.KindOperator {
			if tok.Op == "endbfchar" {
				return nil
			}
			continue
		}
		src, ok := tok.Val.(objects.String)
		if !ok {
			continue
		}
		dst := p.lex.Next()
		if dst.Kind == content.KindEOF {
			return nil
		}
		if err := p.limit(); err != nil {
			return err
		}
		if s, ok := destText(dst.Val); ok {
			p.cm.text[beUint(src)] = s
		}
	}
}

// bfranges reads beginbfrange ... endbfrange.
//
// Three destination forms are legal, and all three appear in real files: a
// string, which increments across the range; an array, one destination per code;
// and a name, which increments as a string would.
func (p *parser) bfranges() error {
	for {
		tok := p.lex.Next()
		if tok.Kind == content.KindEOF {
			return nil
		}
		if tok.Kind == content.KindOperator {
			if tok.Op == "endbfrange" {
				return nil
			}
			continue
		}
		lo, ok := tok.Val.(objects.String)
		if !ok {
			continue
		}
		hiTok := p.lex.Next()
		hi, ok := hiTok.Val.(objects.String)
		if !ok {
			if hiTok.Kind == content.KindEOF {
				return nil
			}
			continue
		}
		low, high := beUint(lo), beUint(hi)

		dst := p.lex.Next()
		switch dst.Kind {
		case content.KindEOF:
			return nil

		case content.KindArrayOpen:
			// One destination per code. The array is read to its close even if it
			// is longer than the range, so a mismatched count cannot leave the
			// lexer inside an array.
			code := low
			for {
				item := p.lex.Next()
				if item.Kind == content.KindArrayClose || item.Kind == content.KindEOF {
					break
				}
				if item.Kind == content.KindOperator {
					continue
				}
				if code > high {
					continue
				}
				if err := p.limit(); err != nil {
					return err
				}
				if s, ok := destText(item.Val); ok {
					p.cm.text[code] = s
				}
				code++
			}

		default:
			if high < low || high-low >= maxRangeSpan {
				// A span this wide is either malformed or an attempt to make a
				// few bytes of input allocate a large table. Either way the range
				// is not usable, and the rest of the CMap still is.
				continue
			}
			base, ok := destText(dst.Val)
			if !ok {
				continue
			}
			if err := p.limitN(int(high-low) + 1); err != nil {
				return err
			}
			for code := low; code <= high; code++ {
				p.cm.text[code] = incrementLast(base, code-low)
				if code == 0xFFFFFFFF {
					break // guard the wrap that would make this loop infinite
				}
			}
		}
	}
}

// cidchars reads begincidchar ... endcidchar: code string, then a CID integer.
func (p *parser) cidchars() error {
	for {
		tok := p.lex.Next()
		if tok.Kind == content.KindEOF {
			return nil
		}
		if tok.Kind == content.KindOperator {
			if tok.Op == "endcidchar" {
				return nil
			}
			continue
		}
		src, ok := tok.Val.(objects.String)
		if !ok {
			continue
		}
		dst := p.lex.Next()
		if dst.Kind == content.KindEOF {
			return nil
		}
		cid, ok := asUint(dst.Val)
		if !ok {
			continue
		}
		if err := p.limit(); err != nil {
			return err
		}
		p.cm.cid[beUint(src)] = cid
	}
}

// cidranges reads begincidrange ... endcidrange: low, high, then the CID the low
// code maps to, with the rest of the range following consecutively.
func (p *parser) cidranges() error {
	for {
		tok := p.lex.Next()
		if tok.Kind == content.KindEOF {
			return nil
		}
		if tok.Kind == content.KindOperator {
			if tok.Op == "endcidrange" {
				return nil
			}
			continue
		}
		lo, ok := tok.Val.(objects.String)
		if !ok {
			continue
		}
		hiTok := p.lex.Next()
		if hiTok.Kind == content.KindEOF {
			return nil
		}
		hi, ok := hiTok.Val.(objects.String)
		if !ok {
			continue
		}
		dstTok := p.lex.Next()
		if dstTok.Kind == content.KindEOF {
			return nil
		}
		base, ok := asUint(dstTok.Val)
		if !ok {
			continue
		}
		low, high := beUint(lo), beUint(hi)
		if high < low || high-low >= maxRangeSpan {
			continue
		}
		if err := p.limitN(int(high-low) + 1); err != nil {
			return err
		}
		for code := low; code <= high; code++ {
			p.cm.cid[code] = base + (code - low)
			if code == 0xFFFFFFFF {
				break
			}
		}
	}
}

// stringPair reads two hex strings, or reports that the section ended.
func (p *parser) stringPair(end string) (lo, hi []byte, done bool, err error) {
	first := p.lex.Next()
	switch first.Kind {
	case content.KindEOF:
		return nil, nil, true, nil
	case content.KindOperator:
		if first.Op == end {
			return nil, nil, true, nil
		}
		return nil, nil, false, nil
	}
	a, ok := first.Val.(objects.String)
	if !ok {
		return nil, nil, false, nil
	}
	second := p.lex.Next()
	if second.Kind == content.KindEOF {
		return nil, nil, true, nil
	}
	b, ok := second.Val.(objects.String)
	if !ok {
		return nil, nil, false, nil
	}
	return a, b, false, nil
}

func (p *parser) limit() error { return p.limitN(1) }

// limitN bounds the total number of mappings. The section counts a CMap declares
// are advisory — real files get them wrong — so the bound is on what was
// actually stored.
func (p *parser) limitN(n int) error {
	if len(p.cm.cid)+len(p.cm.text)+n > maxEntries {
		return fmt.Errorf("cmap: more than %d mappings", maxEntries)
	}
	return nil
}

// destText decodes a bfchar or bfrange destination into the text it names.
//
// A string destination is UTF-16BE, which is what §9.10.3 requires of
// /ToUnicode, and may hold a surrogate pair for a character outside the BMP.
//
// A name destination is a glyph name, resolved through font/encoding. Doing so
// costs this package a dependency, which is worth it: dropping the mapping
// instead would lose the character silently, and font/encoding is itself a leaf
// with no dependencies, so nothing is coupled by reaching it.
//
// An integer destination is not a form §9.10.3 defines and is not accepted. The
// operators that do take integers — cidchar and cidrange — read them directly.
func destText(o objects.Object) (string, bool) {
	switch v := o.(type) {
	case objects.String:
		return decodeUTF16BE(v), true
	case objects.Name:
		return encoding.GlyphText(string(v))
	}
	return "", false
}

// decodeUTF16BE decodes big-endian UTF-16, pairing surrogates.
//
// An odd trailing byte is dropped rather than treated as a character: half a
// code unit carries no meaning, and the alternative is to invent one.
func decodeUTF16BE(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return string(utf16.Decode(units))
}

// incrementLast advances a bfrange destination by n.
//
// A range destination gives the text of its first code, and each subsequent code
// takes that text with its last character advanced. Only the last character
// moves: "the last byte" in §9.10.3 means the last code unit of the destination,
// so a multi-character destination like "ff" maps the range to "ff", "fg", "fh".
func incrementLast(base string, n uint32) string {
	if n == 0 || base == "" {
		return base
	}
	rs := []rune(base)
	// Summed as uint64 and bounded before narrowing. A rune is signed and n comes
	// from a code range in the document, so adding first could overflow to a
	// negative value — which utf8.ValidRune would then reject for the wrong reason,
	// or accept if the wrap happened to land in range.
	sum := uint64(rs[len(rs)-1]) + uint64(n)
	if sum > 0x10FFFF {
		return base
	}
	last := rune(sum)
	if !utf8.ValidRune(last) {
		// Incrementing off the end of Unicode means the range and its destination
		// disagree. Returning the base unchanged keeps a plausible character
		// rather than emitting U+FFFD.
		return base
	}
	rs[len(rs)-1] = last
	return string(rs)
}

// asUint reads a non-negative integer operand.
func asUint(o objects.Object) (uint32, bool) {
	n, ok := o.(objects.Int)
	if !ok || n < 0 || n > 0xFFFFFFFF {
		return 0, false
	}
	return uint32(n), true
}

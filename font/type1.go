package font

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/model-harness/pdftools/font/encoding"
)

// A Type 1 font program — what /FontFile holds — parsed far enough to hand out glyph outlines.
//
// # What this parses
//
// The Adobe Type 1 Font Format's PFA form: the clear-text part up to eexec, then the eexec-
// encrypted Private part, from which it takes the Subrs, the CharStrings, lenIV, and the built-in
// encoding the clear-text part declares. The charstrings themselves are Type 1 and are interpreted
// in type1charstring.go.
//
// # Whose reading
//
// pdfium hands the stream to FreeType, and FreeType does not run PostScript: it walks the tokens
// looking for the keys it knows (type1/t1load.c, parse_dict), and a key that is not one of them is
// skipped, whatever it would have done in a PostScript interpreter. The walk here is FreeType's,
// token for token — its skipper (psaux/psobjs.c, ps_parser_skip_PS_token), its eexec search and
// whitespace rule (type1/t1parse.c, T1_Get_Private_Dict), its readings of the Subrs and CharStrings
// entries — since a program read any other way draws other glyphs than the yardstick does.
//
// # What it refuses
//
// It refuses where FreeType and the format part ways, and where drawing would need machinery that
// is not here: the PFB segment form and hex eexec, which no corpus program uses; a FontMatrix other
// than [0.001 0 0 0.001 0 0], which FreeType applies and a scaled em would need carried through;
// PaintType other than 0, which asks for a stroked outline FreeType fills; a multiple master or
// synthetic font; a built-in encoding other than an array of 256 or StandardEncoding; a Subrs or
// CharStrings entry the declared count has no room for, or a subroutine or glyph name defined
// twice, where FreeType keeps one of the two by table mechanics PostScript does not share; and a
// number where FreeType would read the radix or fractional form of one as an integer.
type Type1 struct {
	subrs  map[int][]byte
	glyphs [][]byte // decrypted, lenIV stripped; glyph 0 is .notdef
	names  []string
	byName map[string]uint16
	codes  [256]uint16 // the built-in encoding, code to glyph, 0 meaning none

	clearText int // the offset of the first cipher byte after eexec
}

// NumGlyphs is the glyph count, a .notdef FreeType synthesizes included.
func (t *Type1) NumGlyphs() int { return len(t.glyphs) }

// GIDForName maps a glyph name to its glyph. Names are unique, since a program defining one twice
// is refused.
func (t *Type1) GIDForName(name string) (uint16, bool) {
	gid, ok := t.byName[name]
	return gid, ok
}

// GIDForCode maps a code through the built-in encoding. A code the encoding leaves unassigned, or
// assigns a name no glyph has, answers false, as FreeType's charmap answers glyph 0.
func (t *Type1) GIDForCode(code byte) (uint16, bool) {
	gid := t.codes[code]
	return gid, gid != 0
}

// ClearTextLength is where the encrypted part starts: the offset /Length1 states in the stream
// dictionary.
func (t *Type1) ClearTextLength() int { return t.clearText }

var stdEncoding = encoding.Standard()

// ParseType1 parses a PFA Type 1 program.
func ParseType1(data []byte) (*Type1, error) {
	t, err := parseType1(data)
	if err != nil {
		return nil, fmt.Errorf("font: Type 1: %w", err)
	}
	return t, nil
}

// t1Loader is the parse state FreeType's T1_LoaderRec keeps: what the walk has met so far.
type t1Loader struct {
	t *Type1

	private  bool // /Private has been seen, so the Private dict's keys apply and the font dict's do not
	lenIV    int
	encoding int // 0 none, 1 an array, 2 StandardEncoding
	names    [256]string
	haveCS   bool
	haveSubr bool
}

const (
	t1NoEncoding = iota
	t1ArrayEncoding
	t1StandardEncoding
)

func parseType1(data []byte) (*Type1, error) {
	if len(data) > 0 && data[0] == 0x80 {
		return nil, errors.New("the PFB segment form, which is refused")
	}
	if !bytes.HasPrefix(data, []byte("%!PS-AdobeFont")) && !bytes.HasPrefix(data, []byte("%!FontType")) {
		return nil, errors.New("no %!PS-AdobeFont or %!FontType header")
	}
	l := &t1Loader{t: &Type1{}, lenIV: 4}
	if err := l.parseDict(data); err != nil {
		return nil, err
	}
	private, err := l.privateDict(data)
	if err != nil {
		return nil, err
	}
	if err := l.parseDict(private); err != nil {
		return nil, err
	}
	if !l.haveCS {
		return nil, errors.New("no /CharStrings")
	}
	if l.encoding == t1NoEncoding {
		return nil, errors.New("no built-in /Encoding")
	}
	t := l.t
	t.byName = make(map[string]uint16, len(t.names))
	for gid, n := range t.names {
		t.byName[n] = uint16(gid) // #nosec G115 -- bounded by the data length over 8, checked at 65536
	}
	for code := range t.codes {
		name := l.names[code]
		if l.encoding == t1StandardEncoding {
			name = stdEncoding.Glyph(byte(code))
		}
		if name == "" { // unset, which FreeType reads as .notdef — and "/ 1 RD" names a glyph ""
			continue
		}
		t.codes[code] = t.byName[name]
	}
	return t, nil
}

// privateDict finds eexec and decrypts what follows it, as T1_Get_Private_Dict does for a PFA
// program held in memory.
func (l *t1Loader) privateDict(data []byte) ([]byte, error) {
	p := &psParser{b: data}
	for p.pos < len(data) {
		c := p.pos
		// 9 is five letters of eexec, whitespace, and four cipher bytes.
		if data[c] == 'e' && c+9 < len(data) && bytes.HasPrefix(data[c+1:], []byte("exec")) {
			p.skipToken()
			cur := p.pos
			// FreeType skips all whitespace after eexec, though the format says a cipher byte may
			// be one, and stops at \r only where \r is not the line end.
			lf := bytes.IndexByte(data[cur:], '\n')
			cr := bytes.IndexByte(data[cur:], '\r')
			testCR := lf < 0 || (cr >= 0 && lf > cr)
			for cur < len(data) && (data[cur] == ' ' || data[cur] == '\t' || data[cur] == '\n' ||
				(testCR && data[cur] == '\r')) {
				cur++
			}
			if cur >= len(data) {
				return nil, errors.New("eexec is not followed by a cipher")
			}
			if cur+3 < len(data) && isHex(data[cur]) && isHex(data[cur+1]) && isHex(data[cur+2]) &&
				isHex(data[cur+3]) {
				return nil, errors.New("hexadecimal eexec, which is refused")
			}
			l.t.clearText = cur
			private := make([]byte, len(data)-cur)
			copy(private, data[cur:])
			decrypt(private, 55665)
			if len(private) < 4 {
				return nil, errors.New("the eexec part is shorter than its four random bytes")
			}
			copy(private, "    ")
			return private, nil
		}
		p.skipToken()
		if p.err != nil {
			break
		}
		p.skipSpaces()
	}
	return nil, errors.New("no eexec")
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// decrypt runs the Type 1 cipher of §7 of the format in place.
func decrypt(b []byte, r uint16) {
	for i, c := range b {
		b[i] = c ^ byte(r>>8)
		r = (uint16(c)+r)*52845 + 22719
	}
}

// parseDict is FreeType's parse_dict: walk the tokens, run a keyword's reader where its name is
// met in the dictionary it belongs to, skip everything else, and stop at eexec or closefile.
func (l *t1Loader) parseDict(b []byte) error {
	p := &psParser{b: b}
	startBinary, haveInteger := 0, false
	p.skipSpaces()
	for p.pos < len(b) {
		c := p.pos
		switch {
		case p.isToken("eexec"), p.isToken("closefile"):
			return nil
		case p.isToken("FontDirectory"):
			if l.private {
				return errors.New("FontDirectory after /Private, a synthetic font, which is refused")
			}
			p.pos += len("FontDirectory")
		case isDigit(b[c]):
			startBinary = c
			p.skipToken()
			if p.err != nil {
				return p.err
			}
			haveInteger = true
		case haveInteger && c+6 < len(b) && (b[c] == 'R' && b[c+1] == 'D' || b[c] == '-' && b[c+1] == '|'):
			p.pos = startBinary
			if _, err := p.binary(); err != nil {
				return err
			}
			haveInteger = false
		case b[c] == '/' && c+2 < len(b):
			p.pos = c + 1
			p.skipToken()
			if p.err != nil {
				return p.err
			}
			name := string(b[c+1 : p.pos])
			if len(name) > 0 && len(name) < 22 && p.pos < len(b) {
				if err := l.keyword(p, name); err != nil {
					return err
				}
			}
			haveInteger = false
		default:
			p.skipToken()
			if p.err != nil {
				return p.err
			}
			haveInteger = false
		}
		p.skipSpaces()
	}
	return nil
}

// keyword runs the reader for one of the keys FreeType's t1_keywords table names, when it is met
// in the dictionary the table assigns it to; met in the other, FreeType ignores it, and so does
// this. Keys FreeType reads into fields this package never uses are left to the walk, which skips
// their values token by token as FreeType's field loader would consume them whole.
func (l *t1Loader) keyword(p *psParser, name string) error {
	if !l.private {
		switch name {
		case "FontMatrix":
			return p.fontMatrix()
		case "Encoding":
			return l.parseEncoding(p)
		case "PaintType":
			v, err := p.toInt()
			if err != nil {
				return fmt.Errorf("/PaintType: %w", err)
			}
			if v != 0 {
				return fmt.Errorf("PaintType %d, which is refused", v)
			}
		case "Private":
			l.private = true
		case "BlendDesignPositions", "BlendDesignMap", "BlendAxisTypes", "WeightVector":
			return fmt.Errorf("/%s, a multiple master font, which is refused", name)
		}
		return nil
	}
	switch name {
	case "lenIV":
		v, err := p.toInt()
		if err != nil {
			return fmt.Errorf("/lenIV: %w", err)
		}
		if v < -1 {
			return fmt.Errorf("lenIV %d", v)
		}
		l.lenIV = v
	case "Subrs":
		return l.parseSubrs(p)
	case "CharStrings":
		return l.parseCharStrings(p)
	case "BuildCharArray":
		return errors.New("/BuildCharArray, a multiple master font, which is refused")
	}
	return nil
}

// parseEncoding is FreeType's parse_encoding, for the forms this package takes: an array built
// by "code /name" pairs, which is how every producer writes one, or StandardEncoding.
func (l *t1Loader) parseEncoding(p *psParser) error {
	b := p.b
	p.skipSpaces()
	if p.pos >= len(b) {
		return errors.New("/Encoding has no value")
	}
	cur := p.pos
	switch {
	case b[cur] == '[':
		return errors.New("an /Encoding written as a literal array, which is refused")
	case isDigit(b[cur]):
	case bytes.HasPrefix(b[cur:], []byte("StandardEncoding")) && cur+17 < len(b):
		l.encoding = t1StandardEncoding
		return nil
	default:
		return errors.New("a built-in /Encoding other than an array or StandardEncoding, which is refused")
	}
	count, err := p.toInt()
	if err != nil {
		return fmt.Errorf("/Encoding: %w", err)
	}
	if count != 256 {
		return fmt.Errorf("an /Encoding array of %d entries", count)
	}
	l.names = [256]string{}
	n := 0
	p.skipSpaces()
	for p.pos < len(b) {
		cur = p.pos
		if b[cur] == 'd' && cur+3 < len(b) && b[cur+1] == 'e' && b[cur+2] == 'f' && isDelim(b[cur+3]) {
			p.pos = cur + 3
			break
		}
		if b[cur] == ']' {
			p.pos = cur + 1
			break
		}
		if isDigit(b[cur]) {
			code, err := p.toInt()
			if err != nil {
				return fmt.Errorf("/Encoding: %w", err)
			}
			p.skipSpaces()
			cur = p.pos
			if cur+2 < len(b) && b[cur] == '/' {
				if n == count {
					return errors.New("/Encoding assigns more codes than its array holds")
				}
				p.pos = cur + 1
				p.skipToken()
				if p.pos >= len(b) || p.err != nil {
					return errors.New("/Encoding ends inside a glyph name")
				}
				if code < 0 || code > 255 {
					return fmt.Errorf("/Encoding code %d", code)
				}
				l.names[code] = string(b[cur+1 : p.pos])
				n++
			}
		} else {
			p.skipToken()
			if p.err != nil {
				return p.err
			}
		}
		p.skipSpaces()
	}
	l.encoding = t1ArrayEncoding
	return nil
}

// parseSubrs is FreeType's parse_subrs: "dup index <binary> NP" entries, for as long as the next
// token is dup.
func (l *t1Loader) parseSubrs(p *psParser) error {
	if l.haveSubr {
		return errors.New("/Subrs defined twice")
	}
	l.haveSubr = true
	b := p.b
	l.t.subrs = map[int][]byte{}
	p.skipSpaces()
	if p.pos < len(b) && b[p.pos] == '[' {
		p.skipToken()
		p.skipSpaces()
		if p.pos >= len(b) || b[p.pos] != ']' {
			return errors.New("/Subrs [ is not an empty array")
		}
		return nil
	}
	count, err := p.toInt()
	if err != nil {
		return fmt.Errorf("/Subrs: %w", err)
	}
	if count < 0 {
		return fmt.Errorf("/Subrs count %d", count)
	}
	p.skipToken() // array
	if p.err != nil {
		return p.err
	}
	p.skipSpaces()
	for p.pos+4 < len(b) && bytes.HasPrefix(b[p.pos:], []byte("dup")) {
		p.skipToken()
		idx, err := p.toInt()
		if err != nil {
			return fmt.Errorf("/Subrs index: %w", err)
		}
		data, err := p.binary()
		if err != nil {
			return err
		}
		p.skipToken() // NP, or noaccess
		if p.err != nil {
			return p.err
		}
		p.skipSpaces()
		if p.pos+4 < len(b) && bytes.HasPrefix(b[p.pos:], []byte("put")) {
			p.skipToken()
			p.skipSpaces()
		}
		if idx < 0 || idx >= count {
			return fmt.Errorf("subroutine %d of a /Subrs array of %d", idx, count)
		}
		if _, dup := l.t.subrs[idx]; dup {
			return fmt.Errorf("subroutine %d defined twice", idx)
		}
		// A subroutine of exactly lenIV bytes is an empty one, which FreeType's parse_subrs takes
		// (size < lenIV); parse_charstrings refuses the same length for a glyph (size <= lenIV).
		cs, err := l.charstring(data, len(data) < l.lenIV)
		if err != nil {
			return fmt.Errorf("subroutine %d: %w", idx, err)
		}
		l.t.subrs[idx] = cs
	}
	return nil
}

// charstring decrypts one charstring with the charstring key and strips its lenIV leading bytes;
// short says the entry is too short to hold them, which FreeType fails the font for. A lenIV of
// -1 means the charstrings are not encrypted.
func (l *t1Loader) charstring(data []byte, short bool) ([]byte, error) {
	if l.lenIV < 0 {
		return data, nil
	}
	if short {
		return nil, fmt.Errorf("%d bytes, too short for lenIV %d", len(data), l.lenIV)
	}
	cs := make([]byte, len(data))
	copy(cs, data)
	decrypt(cs, 4330)
	return cs[l.lenIV:], nil
}

// parseCharStrings is FreeType's parse_charstrings: "/name <binary> ND" entries until end, or
// until def once a glyph has been read, and .notdef made glyph 0.
func (l *t1Loader) parseCharStrings(p *psParser) error {
	if l.haveCS {
		return errors.New("/CharStrings defined twice")
	}
	l.haveCS = true
	b := p.b
	start := p.pos
	count, err := p.toInt()
	if err != nil {
		return fmt.Errorf("/CharStrings: %w", err)
	}
	if count < 0 {
		return fmt.Errorf("/CharStrings count %d", count)
	}
	// FreeType trims a count past what the bytes could hold, at 8 bytes a glyph.
	if room := (len(b) - start) >> 3; count > room {
		count = room
	}
	if count == 0 {
		return errors.New("/CharStrings holds no glyphs")
	}
	t := l.t
	seen := map[string]bool{}
	notdef := -1
	for {
		p.skipSpaces()
		cur := p.pos
		if cur >= len(b) {
			break
		}
		if cur+3 < len(b) && isDelim(b[cur+3]) {
			w := string(b[cur : cur+3])
			if w == "def" && len(t.glyphs) > 0 || w == "end" {
				break
			}
		}
		p.skipToken()
		if p.pos >= len(b) {
			return errors.New("/CharStrings runs to the end of the program")
		}
		if p.err != nil {
			return p.err
		}
		if b[cur] != '/' {
			continue
		}
		if cur+2 >= len(b) {
			return errors.New("/CharStrings ends inside a glyph name")
		}
		name := string(b[cur+1 : p.pos])
		data, err := p.binary()
		if err != nil {
			return err
		}
		if len(t.glyphs) == count {
			return fmt.Errorf("/CharStrings holds more than the %d glyphs it declares", count)
		}
		if seen[name] {
			return fmt.Errorf("glyph /%s defined twice", name)
		}
		seen[name] = true
		if name == ".notdef" {
			notdef = len(t.glyphs)
		}
		cs, err := l.charstring(data, len(data) <= l.lenIV) // <=, not parseSubrs' <, as FreeType has it
		if err != nil {
			return fmt.Errorf("glyph /%s: %w", name, err)
		}
		t.glyphs = append(t.glyphs, cs)
		t.names = append(t.names, name)
	}
	if len(t.glyphs) == 0 {
		return errors.New("/CharStrings holds no glyphs")
	}
	switch {
	case notdef > 0:
		t.glyphs[0], t.glyphs[notdef] = t.glyphs[notdef], t.glyphs[0]
		t.names[0], t.names[notdef] = t.names[notdef], t.names[0]
	case notdef < 0:
		// FreeType moves glyph 0 to the end and puts "0 333 hsbw endchar" in its place.
		t.glyphs = append(t.glyphs, t.glyphs[0])
		t.names = append(t.names, t.names[0])
		t.glyphs[0], t.names[0] = []byte{0x8B, 0xF7, 0xE1, 0x0D, 0x0E}, ".notdef"
	}
	if len(t.glyphs) > 0xFFFF {
		return fmt.Errorf("%d glyphs", len(t.glyphs))
	}
	return nil
}

// psParser is FreeType's PS_Parser: a cursor over PostScript bytes that can skip one token.
type psParser struct {
	b   []byte
	pos int
	err error
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isSpace(c byte) bool {
	return c == ' ' || c == '\r' || c == '\n' || c == '\t' || c == '\f' || c == 0
}

func isDelim(c byte) bool {
	switch c {
	case '/', '(', ')', '<', '>', '[', ']', '{', '}', '%':
		return true
	}
	return isSpace(c)
}

// isToken is FreeType's IS_PS_TOKEN: the keyword at the cursor, followed by a delimiter or the
// end.
func (p *psParser) isToken(tok string) bool {
	end := p.pos + len(tok)
	if end > len(p.b) || !bytes.HasPrefix(p.b[p.pos:], []byte(tok)) {
		return false
	}
	return end == len(p.b) || isDelim(p.b[end])
}

// skipComment stops at the line end, leaving it for the caller.
func skipComment(b []byte, cur int) int {
	for cur < len(b) && b[cur] != '\r' && b[cur] != '\n' {
		cur++
	}
	return cur
}

// skipSpaces skips whitespace and comments. A comment consumes the line end after it, as
// FreeType's skip_spaces does by stepping past whatever ended the comment.
func skipSpaces(b []byte, cur int) int {
	for cur < len(b) {
		if !isSpace(b[cur]) {
			if b[cur] != '%' {
				break
			}
			cur = skipComment(b, cur)
		}
		cur++
	}
	return min(cur, len(b))
}

func (p *psParser) skipSpaces() { p.pos = skipSpaces(p.b, p.pos) }

// skipLiteralString skips a ( string ), escapes and nesting included, leaving the cursor after
// its closing parenthesis.
func skipLiteralString(b []byte, cur int) (int, error) {
	embed := 0
	for cur < len(b) {
		c := b[cur]
		cur++
		switch c {
		case '\\':
			if cur == len(b) {
				return cur, errors.New("a string ends in a backslash")
			}
			switch b[cur] {
			case 'n', 'r', 't', 'b', 'f', '\\', '(', ')':
				cur++
			default:
				for i := 0; i < 3 && cur < len(b) && b[cur] >= '0' && b[cur] <= '7'; i++ {
					cur++
				}
			}
		case '(':
			embed++
		case ')':
			embed--
			if embed == 0 {
				return cur, nil
			}
		}
	}
	return cur, errors.New("an unterminated string")
}

// skipHexString skips a < hex string >, leaving the cursor after its closing bracket. A string
// that runs off the end is taken, as FreeType's skip_string takes it; the caller clamps the cursor.
func skipHexString(b []byte, cur int) (int, error) {
	for cur++; cur < len(b); cur++ {
		cur = skipSpaces(b, cur)
		if cur >= len(b) || !isHex(b[cur]) {
			break
		}
	}
	if cur < len(b) && b[cur] != '>' {
		return cur, errors.New("a hex string without its closing >")
	}
	return cur + 1, nil
}

// skipProcedure skips a { procedure }. It is FreeType's skip_procedure to the byte, including the
// step it takes past the byte after a string or comment inside the procedure, since its loop
// advances once more after each skip.
func skipProcedure(b []byte, cur int) (int, error) {
	embed := 0
	var err error
	for ; cur < len(b) && err == nil; cur++ {
		switch b[cur] {
		case '{':
			embed++
		case '}':
			embed--
			if embed == 0 {
				return cur + 1, nil
			}
		case '(':
			cur, err = skipLiteralString(b, cur)
		case '<':
			cur, err = skipHexString(b, cur)
		case '%':
			cur = skipComment(b, cur)
		}
	}
	if err == nil {
		err = errors.New("an unterminated procedure")
	}
	return cur, err
}

// skipToken is FreeType's ps_parser_skip_PS_token.
func (p *psParser) skipToken() {
	b := p.b
	start := p.pos
	cur := skipSpaces(b, p.pos)
	var err error
	switch {
	case cur >= len(b):
	case b[cur] == '[' || b[cur] == ']':
		cur++
	case b[cur] == '{':
		cur, err = skipProcedure(b, cur)
	case b[cur] == '(':
		cur, err = skipLiteralString(b, cur)
	case b[cur] == '<':
		if cur+1 < len(b) && b[cur+1] == '<' {
			cur += 2
		} else {
			cur, err = skipHexString(b, cur)
		}
	case b[cur] == '>':
		cur++
		if cur >= len(b) || b[cur] != '>' {
			err = errors.New("an unexpected >")
		} else {
			cur++
		}
	default:
		if b[cur] == '/' {
			cur++
		}
		for cur < len(b) && !isDelim(b[cur]) {
			cur++
		}
	}
	if err == nil && cur < len(b) && cur == start {
		err = fmt.Errorf("the self-delimiting %q where a token belongs", b[cur])
	}
	p.pos = min(cur, len(b))
	p.err = err
}

// toInt reads a decimal integer. FreeType's reader also takes a radix form and stops quietly at a
// fraction, answering 0 when there is no number at all; this refuses all three, since each is a
// program no producer should write and whose FreeType reading is an accident of the reader.
func (p *psParser) toInt() (int, error) {
	p.skipSpaces()
	b := p.b
	cur := p.pos
	if cur < len(b) && (b[cur] == '-' || b[cur] == '+') {
		cur++
	}
	digits := cur
	for cur < len(b) && isDigit(b[cur]) {
		cur++
	}
	if cur == digits || cur < len(b) && !isDelim(b[cur]) {
		return 0, errors.New("not an integer")
	}
	v, err := strconv.ParseInt(string(b[p.pos:cur]), 10, 32)
	if err != nil {
		return 0, errors.New("an integer out of range")
	}
	p.pos = cur
	return int(v), nil
}

// binary is FreeType's read_binary_data: "size RD " and size bytes, the RD standing for any
// token and the space for exactly one byte.
func (p *psParser) binary() ([]byte, error) {
	p.skipSpaces()
	if p.pos >= len(p.b) || !isDigit(p.b[p.pos]) {
		return nil, errors.New("binary data without a size")
	}
	size, err := p.toInt()
	if err != nil {
		return nil, fmt.Errorf("binary data size: %w", err)
	}
	p.skipToken()
	if p.err != nil {
		return nil, p.err
	}
	base := p.pos + 1
	if size >= len(p.b)-base { // the isDigit guard above leaves toInt no sign to read
		return nil, fmt.Errorf("binary data of %d bytes runs past the program", size)
	}
	p.pos = base + size
	return p.b[base:p.pos], nil
}

// fontMatrix requires the matrix every Type 1 program in practice states, [0.001 0 0 0.001 0 0],
// in brackets or braces.
func (p *psParser) fontMatrix() error {
	p.skipSpaces()
	b := p.b
	if p.pos >= len(b) || b[p.pos] != '[' && b[p.pos] != '{' {
		return errors.New("/FontMatrix is not an array")
	}
	closer := byte(']')
	if b[p.pos] == '{' {
		closer = '}'
	}
	p.pos++
	want := [6]float64{0.001, 0, 0, 0.001, 0, 0}
	for i := range want {
		p.skipSpaces()
		cur := p.pos
		for p.pos < len(b) && !isDelim(b[p.pos]) {
			p.pos++
		}
		v, err := strconv.ParseFloat(string(b[cur:p.pos]), 64)
		if err != nil || v != want[i] {
			return fmt.Errorf("/FontMatrix entry %d is %q, not %g, which is refused", i, b[cur:p.pos], want[i])
		}
	}
	p.skipSpaces()
	if p.pos >= len(b) || b[p.pos] != closer {
		return errors.New("/FontMatrix does not hold six numbers")
	}
	p.pos++
	return nil
}

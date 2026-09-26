// Package cffbuild assembles a bare CFF font program from nothing, for tests.
//
// It exists for the reason internal/ttfbuild does: a test that needs a font either redistributes
// one it may not, skips in a clone, or builds what it needs, and font and render/native both need
// the same bytes — font, to check its parser reads what was written here, and render/native, to
// check a page set in this font rasterizes like the borrowed engine. A font builder duplicated in
// two test files is the same defect as arithmetic duplicated in two packages: a fix to one is
// invisible to the other.
//
// Not in a _test.go file, because an internal package cannot export from one. Internal so it
// cannot become part of the module's surface by accident.
//
// # Why every offset is written in the widest form
//
// A Top or Font DICT operand that names an offset — charset, Encoding, CharStrings, Private,
// Subrs, FDArray, FDSelect — is written here in the fixed 5-byte form TN #5176 §4 calls the "29"
// integer, even where the value would fit in one byte. That form's size does not depend on its
// value, so every section's byte length is known before the offsets into later sections are, and
// Build lays the font out in one pass: write each section in order, and the moment a later
// section's start is known, go back and fill in the placeholder that was reserved for it. A
// shortest-form encoder would make a DICT's own length depend on the very offsets it is trying to
// compute the position of.
//
// # On the overflow annotations below
//
// Every function here writes a chosen value into a big-endian binary field, and CFF's fields are
// written by reinterpreting the value's bits, which is what the format specifies: an offset, an
// SID, a code, or a count, cast down to the byte or word width its field declares. A scanner reads
// each of those as a possible overflow, dozens of times in one file. The annotations are at the
// function level with this one explanation, rather than at each site, because a suppression
// repeated dozens of times stops being read — and because every input here is a literal or a small
// computed value from this file or a caller's test data, never a value from outside the program.
package cffbuild

import (
	"fmt"
	"math"
	"strconv"
)

// Op is a Type 2 charstring operator. A two-byte (escaped) operator is 1200 plus its second byte.
type Op int

const (
	HStem      Op = 1
	VStem      Op = 3
	VMoveTo    Op = 4
	RLineTo    Op = 5
	HLineTo    Op = 6
	VLineTo    Op = 7
	RRCurveTo  Op = 8
	CallSubr   Op = 10
	Return     Op = 11
	EndChar    Op = 14
	HStemHM    Op = 18
	HintMask   Op = 19
	CntrMask   Op = 20
	RMoveTo    Op = 21
	HMoveTo    Op = 22
	VStemHM    Op = 23
	RCurveLine Op = 24
	RLineCurve Op = 25
	VVCurveTo  Op = 26
	HHCurveTo  Op = 27
	CallGSubr  Op = 29
	VHCurveTo  Op = 30
	HVCurveTo  Op = 31

	DotSection Op = 1200
	And        Op = 1203
	Add        Op = 1210
	Random     Op = 1223
	HFlex      Op = 1234
	Flex       Op = 1235
	HFlex1     Op = 1236
	Flex1      Op = 1237
)

// Raw is bytes written into a charstring verbatim: a hintmask's mask bytes, or a malformed tail.
type Raw []byte

// CS encodes a charstring from operands (int or float64), operators (Op), and Raw bytes, in order.
// Integers use the shortest form of TN #5177 Table 1 (one byte for -107..107, two for ±108..1131,
// 28+int16 otherwise); a non-integer float64 uses the 255 16.16 fixed form. Panics on any other
// type.
func CS(items ...any) []byte {
	var b []byte
	for _, it := range items {
		switch v := it.(type) {
		case int:
			b = appendCSInt(b, v)
		case float64:
			b = appendCSFixed(b, v)
		case Op:
			b = appendCSOp(b, v)
		case Raw:
			b = append(b, v...)
		default:
			panic(fmt.Sprintf("cffbuild: CS: %T is not int, float64, cffbuild.Op, or cffbuild.Raw", it))
		}
	}
	return b
}

// #nosec G115 -- see the package comment
func appendCSInt(b []byte, v int) []byte {
	switch {
	case v >= -107 && v <= 107:
		return append(b, byte(v+139))
	case v >= 108 && v <= 1131:
		w := v - 108
		return append(b, byte(247+w/256), byte(w%256))
	case v >= -1131 && v <= -108:
		w := -v - 108
		return append(b, byte(251+w/256), byte(w%256))
	case v >= -32768 && v <= 32767:
		n := int16(v)
		return append(b, 28, byte(n>>8), byte(n))
	default:
		panic(fmt.Sprintf("cffbuild: CS: integer %d does not fit a Type 2 charstring operand", v))
	}
}

// #nosec G115 -- see the package comment
func appendCSFixed(b []byte, v float64) []byte {
	n := int32(math.Round(v * 65536))
	return append(b, 255, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// #nosec G115 -- see the package comment
func appendCSOp(b []byte, op Op) []byte {
	v := int(op)
	if v >= 1200 {
		return append(b, 12, byte(v-1200))
	}
	return append(b, byte(v))
}

// DictInt encodes one DICT integer operand in the 5-byte 29 form, for building TopExtra.
// #nosec G115 -- see the package comment
func DictInt(v int32) []byte {
	return []byte{29, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

// dictInt encodes a DICT integer operand in its shortest form (TN #5176 Table 3), for a value that
// is not an offset and so is not held to the fixed form DictInt writes.
// #nosec G115 -- see the package comment
func dictInt(v int32) []byte {
	switch {
	case v >= -107 && v <= 107:
		return []byte{byte(v + 139)}
	case v >= 108 && v <= 1131:
		w := v - 108
		return []byte{byte(247 + w/256), byte(w % 256)}
	case v >= -1131 && v <= -108:
		w := -v - 108
		return []byte{byte(251 + w/256), byte(w % 256)}
	case v >= -32768 && v <= 32767:
		n := int16(v)
		return []byte{28, byte(n >> 8), byte(n)}
	default:
		return DictInt(v)
	}
}

// dictReal encodes a non-integer DICT operand as packed decimal nibbles (TN #5176 Table 5), always
// in plain decimal — never scientific notation — so the nibbles 'E' and 'E-' never have to be
// written.
func dictReal(v float64) []byte {
	// 'f' (never scientific notation) and precision -1 (the shortest decimal that reads back as
	// exactly v) -- fmt's "%f" rounds to 6 decimal places instead, which is indistinguishable from
	// 0 for a value like 1e-12 that a FontMatrix refusal fixture needs written exactly.
	s := strconv.FormatFloat(v, 'f', -1, 64)
	nibs := make([]byte, 0, len(s)+2)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '.':
			nibs = append(nibs, 0xA)
		case '-':
			nibs = append(nibs, 0xE)
		default:
			nibs = append(nibs, c-'0')
		}
	}
	nibs = append(nibs, 0xF)
	if len(nibs)%2 == 1 {
		nibs = append(nibs, 0xF)
	}
	b := make([]byte, 1+len(nibs)/2)
	b[0] = 30
	for i := 0; i < len(nibs); i += 2 {
		b[1+i/2] = nibs[i]<<4 | nibs[i+1]
	}
	return b
}

// DictReal encodes one DICT operand in the real (30-form) packed-decimal form, forcing that form
// even for a value dictNum would otherwise write as an integer, for building a TopExtra or FDArray
// Extra fixture that needs checkTopOccurrence's own real-operand checks (Private, ROS) to see
// hadReal set.
func DictReal(v float64) []byte { return dictReal(v) }

// dictNum encodes one DICT number operand, as an integer when v has no fractional part and a real
// otherwise.
func dictNum(v float64) []byte {
	if v == math.Trunc(v) && math.Abs(v) <= math.MaxInt32 {
		return dictInt(int32(v))
	}
	return dictReal(v)
}

// Glyph is one CharStrings entry. GID 0 is always .notdef, written by Build as `endchar`; Glyphs
// are GIDs 1 up.
type Glyph struct {
	SID        uint16 // charset entry for a name-keyed font when Name is "": a standard string ID
	Name       string // charset entry for a name-keyed font: appended to the String INDEX (SID 391 up)
	CID        uint16 // charset entry for a CID-keyed font
	FD         uint8  // the Font DICT a CID-keyed glyph uses
	CharString []byte
	// EmptyName mints a fresh SID naming an empty String INDEX entry, overriding Name and SID.
	// Name alone cannot write this: an empty Name falls back to SID (see charsetIDsAndStrings),
	// and a font's own custom names are otherwise never empty.
	EmptyName bool
}

// FontDict is one FDArray entry of a CID-keyed font.
type FontDict struct {
	FontMatrix []float64 // written when non-nil
	Subrs      [][]byte  // local subroutines in its Private DICT
	// Extra is raw DICT bytes appended after everything fontDictBytes writes, the FDArray entry's
	// own analogue of Builder.TopExtra: an operator here overrides fontDictBytes's own (parseDict
	// keeps the last), for a refusal fixture that needs a bad occurrence inside one Font DICT.
	Extra []byte
}

// Supplement is an encoding supplement: a code, and the SID of the glyph it names.
type Supplement struct {
	Code byte
	SID  uint16
}

type Builder struct {
	Glyphs []Glyph
	// Codes is the built-in encoding: Codes[i] is the code of GID i+1. With Codes nil and no
	// Supplements, the Top DICT names the predefined Standard encoding (offset 0) — the default.
	Codes          []byte
	EncodingFormat int // 0 or 1 (ranges of consecutive codes for consecutive GIDs)
	Supplements    []Supplement
	CharsetFormat  int       // 0, 1 or 2 (format 2 has a two-byte nLeft; ranges group consecutive IDs)
	ISOAdobe       bool      // name the predefined ISOAdobe charset (offset 0) and write none
	FontMatrix     []float64 // Top DICT FontMatrix, written when non-nil
	Subrs          [][]byte  // local subroutines of a name-keyed font's Private DICT
	GSubrs         [][]byte
	CID            bool       // CID-keyed: ROS (Adobe/Identity/0, both strings in the String INDEX), FDArray, FDSelect
	FDs            []FontDict // CID-keyed only; one empty FontDict when nil
	FDSelectFormat int        // 0 or 3
	// TopExtra is raw DICT bytes appended to the Top DICT after everything Build writes, so an
	// operator here overrides Build's (parseDict keeps the last); for refusal fixtures.
	TopExtra []byte
	Major    byte // header major version; 0 means 1
	// HeaderSize overrides the header's own length (hdrSize, data[2]): 0 means the default 4. A
	// shorter value trims the header's own trailing fixed field (offSize) before writing hdrSize; a
	// longer one pads with zero bytes after it. Build computes every later offset from the
	// resulting buffer length, so this is the only field a header-guard fixture needs to set; for
	// pinning ParseCFF's hdrSize bound at both a real font's own header size and one byte more.
	HeaderSize int
}

// Top DICT, Font DICT and Private DICT operators Build writes. A two-byte operator is escape 12
// followed by its second byte, held here as 1200 plus it, per TN #5176 Tables 9, 10 and 23.
const (
	opCharset       = 15
	opEncoding      = 16
	opCharStrings   = 17
	opPrivate       = 18
	opSubrs         = 19
	opDefaultWidthX = 20
	opNominalWidthX = 21
	opFontMatrix    = 1207
	opROS           = 1230
	opFDArray       = 1236
	opFDSelect      = 1237
)

// nStdStrings is the standard string count of TN #5176 Appendix A: a name-keyed glyph's own name
// is appended to the String INDEX starting at this SID.
const nStdStrings = 391

// Build assembles the font. See the package comment for the layout and why every offset is
// written in the fixed 5-byte form.
func (b Builder) Build() []byte {
	major := b.Major
	if major == 0 {
		major = 1
	}
	hdr := []byte{major, 0, 4, 4}
	if n := b.HeaderSize; n != 0 {
		switch {
		case n < len(hdr):
			hdr = hdr[:n]
		case n > len(hdr):
			hdr = append(hdr, make([]byte, n-len(hdr))...)
		}
		hdr[2] = byte(n) // #nosec G115 -- a test fixture's own literal
	}
	buf := hdr

	buf, _ = appendIndex(buf, [][]byte{[]byte("Test")})

	ids, strs := b.charsetIDsAndStrings()
	registrySID, orderingSID := 0, 0
	if b.CID {
		registrySID = nStdStrings + len(strs)
		strs = append(strs, []byte("Adobe"))
		orderingSID = nStdStrings + len(strs)
		strs = append(strs, []byte("Identity"))
	}
	customEncoding := !b.CID && (b.Codes != nil || len(b.Supplements) > 0)

	top, topRel := b.topDictBytes(registrySID, orderingSID, customEncoding)
	var topStarts []int
	buf, topStarts = appendIndex(buf, [][]byte{top})
	patch := make(map[string]int, len(topRel))
	for role, rel := range topRel {
		patch[role] = topStarts[0] + rel
	}

	buf, _ = appendIndex(buf, strs)
	buf, _ = appendIndex(buf, b.GSubrs)

	if !b.ISOAdobe {
		pos := len(buf)
		buf = appendCharset(buf, b.CharsetFormat, ids)
		patchOffset(buf, patch["charset"], pos)
	}
	if customEncoding {
		pos := len(buf)
		buf = appendEncoding(buf, b)
		patchOffset(buf, patch["encoding"], pos)
	}

	csPos := len(buf)
	cs := make([][]byte, 0, len(b.Glyphs)+1)
	cs = append(cs, CS(EndChar))
	for _, g := range b.Glyphs {
		cs = append(cs, g.CharString)
	}
	buf, _ = appendIndex(buf, cs)
	patchOffset(buf, patch["charstrings"], csPos)

	if !b.CID {
		var pos, size int
		buf, pos, size = appendPrivateDict(buf, b.Subrs)
		patchOffset(buf, patch["privateSize"], size)
		patchOffset(buf, patch["privateOffset"], pos)
		return buf
	}

	fds := b.FDs
	if fds == nil {
		fds = []FontDict{{}}
	}
	privPos := make([]int, len(fds))
	privSize := make([]int, len(fds))
	for i, fd := range fds {
		buf, privPos[i], privSize[i] = appendPrivateDict(buf, fd.Subrs)
	}
	fdArrPos := len(buf)
	fdObjs := make([][]byte, len(fds))
	for i, fd := range fds {
		fdObjs[i] = fontDictBytes(fd, privSize[i], privPos[i])
	}
	buf, _ = appendIndex(buf, fdObjs)
	patchOffset(buf, patch["fdarray"], fdArrPos)

	fdSelPos := len(buf)
	buf = appendFDSelect(buf, b)
	patchOffset(buf, patch["fdselect"], fdSelPos)
	return buf
}

// charsetIDsAndStrings decides each glyph's charset entry (a CID, or an SID either standard or
// newly minted for a name) and the strings a name-keyed font's names add to the String INDEX.
// #nosec G115 -- see the package comment
func (b Builder) charsetIDsAndStrings() (ids []uint16, strs [][]byte) {
	ids = make([]uint16, len(b.Glyphs))
	if b.CID {
		for i, g := range b.Glyphs {
			ids[i] = g.CID
		}
		return ids, nil
	}
	for i, g := range b.Glyphs {
		switch {
		case g.EmptyName:
			ids[i] = uint16(nStdStrings + len(strs))
			strs = append(strs, []byte{})
		case g.Name != "":
			ids[i] = uint16(nStdStrings + len(strs))
			strs = append(strs, []byte(g.Name))
		default:
			ids[i] = g.SID
		}
	}
	return ids, strs
}

// topDictBytes builds the Top DICT, with a 5-byte placeholder for each offset operand whose value
// is not known yet. It returns the bytes and each placeholder's role and position (of its value's
// first byte, relative to the start of the returned slice).
func (b Builder) topDictBytes(registrySID, orderingSID int, customEncoding bool) ([]byte, map[string]int) {
	d := newDictBuf()
	if b.CID {
		d.num(float64(registrySID)).num(float64(orderingSID)).num(0).op(opROS)
	}
	if b.FontMatrix != nil {
		d.nums(b.FontMatrix).op(opFontMatrix)
	}
	if !b.ISOAdobe {
		d.placeholder("charset").op(opCharset)
	}
	if customEncoding {
		d.placeholder("encoding").op(opEncoding)
	}
	d.placeholder("charstrings").op(opCharStrings)
	if !b.CID {
		d.placeholder("privateSize").placeholder("privateOffset").op(opPrivate)
	} else {
		d.placeholder("fdarray").op(opFDArray)
		d.placeholder("fdselect").op(opFDSelect)
	}
	d.raw(b.TopExtra)
	return d.b, d.patches
}

// fontDictBytes builds one FDArray entry: its own FontMatrix when it has one, and a Private
// operator whose size and offset are already known, so — unlike the Top DICT's — they need no
// placeholder, though they are still written in the fixed 5-byte form every offset operand uses.
func fontDictBytes(fd FontDict, privSize, privPos int) []byte {
	d := newDictBuf()
	if fd.FontMatrix != nil {
		d.nums(fd.FontMatrix).op(opFontMatrix)
	}
	d.fixed(privSize).fixed(privPos).op(opPrivate)
	d.raw(fd.Extra)
	return d.b
}

// appendPrivateDict appends one Private DICT — always defaultWidthX 0 and nominalWidthX 0, so it
// is never empty — and, when subrs is non-empty, its Subrs INDEX, relative to the Private DICT's
// own start as TN #5176 §15 requires. It returns the new buf and the Private DICT's position and
// size.
func appendPrivateDict(buf []byte, subrs [][]byte) ([]byte, int, int) {
	d := newDictBuf()
	d.num(0).op(opDefaultWidthX)
	d.num(0).op(opNominalWidthX)
	if len(subrs) > 0 {
		d.placeholder("subrs").op(opSubrs)
	}
	pos := len(buf)
	buf = append(buf, d.b...)
	size := len(d.b)
	if len(subrs) > 0 {
		subrsValuePos := pos + d.patches["subrs"]
		subrsStart := len(buf)
		buf, _ = appendIndex(buf, subrs)
		patchOffset(buf, subrsValuePos, subrsStart-pos)
	}
	return buf, pos, size
}

// dictBuf assembles one DICT's bytes, remembering where a value the layout has not decided yet
// will need to be patched in once it has.
type dictBuf struct {
	b       []byte
	patches map[string]int // role -> position of the value's first byte, relative to d.b[0]
}

func newDictBuf() *dictBuf { return &dictBuf{patches: map[string]int{}} }

func (d *dictBuf) num(v float64) *dictBuf {
	d.b = append(d.b, dictNum(v)...)
	return d
}

func (d *dictBuf) nums(vs []float64) *dictBuf {
	for _, v := range vs {
		d.num(v)
	}
	return d
}

// fixed writes a value that is already known, in the fixed 5-byte form every offset operand uses
// (see the package comment) even though, being known already, it could have used the shortest one.
func (d *dictBuf) fixed(v int) *dictBuf {
	d.b = append(d.b, DictInt(int32(v))...) // #nosec G115 -- see the package comment
	return d
}

// placeholder reserves a 5-byte slot for a value the layout has not decided yet, and remembers
// where Build must come back to patch it.
func (d *dictBuf) placeholder(role string) *dictBuf {
	d.patches[role] = len(d.b) + 1 // +1 to skip the leading 29
	d.b = append(d.b, 29, 0, 0, 0, 0)
	return d
}

// #nosec G115 -- see the package comment
func (d *dictBuf) op(op int) *dictBuf {
	if op >= 1200 {
		d.b = append(d.b, 12, byte(op-1200))
	} else {
		d.b = append(d.b, byte(op))
	}
	return d
}

func (d *dictBuf) raw(b []byte) *dictBuf {
	d.b = append(d.b, b...)
	return d
}

// indexOffSize is the smallest INDEX offset size (TN #5176 §5) that can hold maxOffset.
func indexOffSize(maxOffset int) int {
	switch {
	case maxOffset < 1<<8:
		return 1
	case maxOffset < 1<<16:
		return 2
	case maxOffset < 1<<24:
		return 3
	default:
		return 4
	}
}

// appendIndex appends an INDEX of objs to buf (TN #5176 §5), returning the new buf and each
// object's absolute start position within it. objs may be empty, or nil, for an empty INDEX.
// #nosec G115 -- see the package comment
func appendIndex(buf []byte, objs [][]byte) ([]byte, []int) {
	count := len(objs)
	buf = append(buf, byte(count>>8), byte(count))
	if count == 0 {
		return buf, nil
	}
	total := 0
	for _, o := range objs {
		total += len(o)
	}
	offSize := indexOffSize(total + 1)
	buf = append(buf, byte(offSize))
	off := 1
	for i := 0; i <= count; i++ {
		buf = putOffset(buf, offSize, off)
		if i < count {
			off += len(objs[i])
		}
	}
	starts := make([]int, count)
	pos := len(buf)
	for i, o := range objs {
		starts[i] = pos
		buf = append(buf, o...)
		pos += len(o)
	}
	return buf, starts
}

// #nosec G115 -- see the package comment
func putOffset(buf []byte, size, v int) []byte {
	for i := size - 1; i >= 0; i-- {
		buf = append(buf, byte(v>>(8*i)))
	}
	return buf
}

// patchOffset fills in a 4-byte value TN #5176's 29 form reserved, once the layout has decided it.
// #nosec G115 -- see the package comment
func patchOffset(buf []byte, pos, v int) {
	buf[pos] = byte(v >> 24)
	buf[pos+1] = byte(v >> 16)
	buf[pos+2] = byte(v >> 8)
	buf[pos+3] = byte(v)
}

// appendCharset appends a charset (TN #5176 §13) naming ids[i] for GID i+1. Format 1 splits a
// consecutive run at 255, the most an 8-bit nLeft can hold; format 2's 16-bit nLeft does not have
// to.
// #nosec G115 -- see the package comment
func appendCharset(buf []byte, format int, ids []uint16) []byte {
	buf = append(buf, byte(format))
	switch format {
	case 0:
		for _, id := range ids {
			buf = append(buf, byte(id>>8), byte(id))
		}
	case 1:
		for i := 0; i < len(ids); {
			j := i
			for j+1 < len(ids) && ids[j+1] == ids[j]+1 && j-i < 255 {
				j++
			}
			buf = append(buf, byte(ids[i]>>8), byte(ids[i]), byte(j-i))
			i = j + 1
		}
	case 2:
		for i := 0; i < len(ids); {
			j := i
			for j+1 < len(ids) && ids[j+1] == ids[j]+1 {
				j++
			}
			nLeft := j - i
			buf = append(buf, byte(ids[i]>>8), byte(ids[i]), byte(nLeft>>8), byte(nLeft))
			i = j + 1
		}
	}
	return buf
}

// appendEncoding appends a custom built-in encoding (TN #5176 §12): a format 0 or 1 main table,
// then a supplement array when b.Supplements is non-empty, with the format byte's high bit set.
// #nosec G115 -- see the package comment
func appendEncoding(buf []byte, b Builder) []byte {
	format := byte(b.EncodingFormat)
	if len(b.Supplements) > 0 {
		format |= 0x80
	}
	buf = append(buf, format)
	switch b.EncodingFormat {
	case 0:
		buf = append(buf, byte(len(b.Codes)))
		buf = append(buf, b.Codes...)
	case 1:
		type rng struct{ first, left byte }
		var ranges []rng
		for i := 0; i < len(b.Codes); {
			j := i
			for j+1 < len(b.Codes) && b.Codes[j+1] == b.Codes[j]+1 {
				j++
			}
			ranges = append(ranges, rng{b.Codes[i], byte(j - i)})
			i = j + 1
		}
		buf = append(buf, byte(len(ranges)))
		for _, r := range ranges {
			buf = append(buf, r.first, r.left)
		}
	}
	if len(b.Supplements) > 0 {
		buf = append(buf, byte(len(b.Supplements)))
		for _, s := range b.Supplements {
			buf = append(buf, s.Code, byte(s.SID>>8), byte(s.SID))
		}
	}
	return buf
}

// appendFDSelect appends an FDSelect (TN #5176 §19) naming glyph i's Font DICT as b.Glyphs[i-1].FD
// (GID 0 always names FD 0). Format 3 groups consecutive GIDs sharing an FD into one range, ended
// by the sentinel GID.
// #nosec G115 -- see the package comment
func appendFDSelect(buf []byte, b Builder) []byte {
	fds := make([]byte, len(b.Glyphs)+1)
	for i, g := range b.Glyphs {
		fds[i+1] = g.FD
	}
	switch b.FDSelectFormat {
	case 0:
		buf = append(buf, 0)
		buf = append(buf, fds...)
	case 3:
		buf = append(buf, 3)
		type rng struct {
			first uint16
			fd    byte
		}
		var ranges []rng
		for i := 0; i < len(fds); {
			j := i
			for j+1 < len(fds) && fds[j+1] == fds[i] {
				j++
			}
			ranges = append(ranges, rng{uint16(i), fds[i]})
			i = j + 1
		}
		buf = append(buf, byte(len(ranges)>>8), byte(len(ranges)))
		for _, r := range ranges {
			buf = append(buf, byte(r.first>>8), byte(r.first), r.fd)
		}
		n := len(fds)
		buf = append(buf, byte(n>>8), byte(n))
	}
	return buf
}

// Square is a box from (100,100) to (400,400), drawn with rmoveto and one hlineto alternating
// horizontal and vertical, mirroring ttfbuild's square. Operands precede the operator they belong
// to, as every Type 2 charstring stack-based operator requires (TN #5177 §3).
func Square() []byte {
	return CS(100, 100, RMoveTo, 300, 300, -300, -300, HLineTo, EndChar)
}

// Diamond is a closed shape of four rrcurveto cubics through (500,100), (900,500), (500,900) and
// (100,500) — roughly inscribed in the box (100,100)-(900,900) — so a render test has a glyph that
// differs from Square everywhere a glyph can.
func Diamond() []byte {
	return CS(500, 100, RMoveTo,
		200, 0, 200, 200, 0, 200,
		0, 200, -200, 200, -200, 0,
		-200, 0, -200, -200, 0, -200,
		0, -200, 200, -200, 200, 0,
		RRCurveTo, EndChar)
}

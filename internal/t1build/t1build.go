// Package t1build assembles a Type 1 font program in the PFA form from nothing, for tests.
//
// It exists for the reason internal/cffbuild does: a test that needs a font either redistributes
// one it may not, skips in a clone, or builds what it needs, and font and render/native both need
// the same bytes.
//
// The program is laid out the way the producers in the corpus write one: a clear-text font
// dictionary ending in "currentfile eexec", then the binary eexec-encrypted Private dictionary,
// Subrs, and CharStrings, with no trailer, since a PDF's FontFile carries none.
//
// Not in a _test.go file, because an internal package cannot export from one. Internal so it
// cannot become part of the module's surface by accident.
//
// # On the overflow annotations below
//
// A charstring's number forms and the cipher are defined on the bits of a value cast down to a
// byte, and a scanner reads each cast as a possible overflow. The annotations carry this one
// explanation; every input is a literal or a small value from this file or a caller's test data.
package t1build

import (
	"fmt"
	"strconv"
	"strings"
)

// Op is a Type 1 charstring operator. An escaped operator is 1200 plus its second byte.
type Op int

const (
	HStem     Op = 1
	VStem     Op = 3
	VMoveTo   Op = 4
	RLineTo   Op = 5
	HLineTo   Op = 6
	VLineTo   Op = 7
	RRCurveTo Op = 8
	ClosePath Op = 9
	CallSubr  Op = 10
	Return    Op = 11
	HSBW      Op = 13
	EndChar   Op = 14
	RMoveTo   Op = 21
	HMoveTo   Op = 22
	VHCurveTo Op = 30
	HVCurveTo Op = 31

	DotSection      Op = 1200
	VStem3          Op = 1201
	HStem3          Op = 1202
	Seac            Op = 1206
	SBW             Op = 1207
	Div             Op = 1212
	CallOtherSubr   Op = 1216
	Pop             Op = 1217
	SetCurrentPoint Op = 1233
)

// Raw is bytes written into a charstring verbatim, for a malformed one.
type Raw []byte

// CS encodes a charstring from integer operands, operators, and Raw bytes, in order. An integer
// takes the shortest form of §6.2 of the format: one byte for -107..107, two for ±108..1131, and
// the 255 form with a 32-bit integer otherwise. Panics on any other type.
func CS(items ...any) []byte {
	var b []byte
	for _, it := range items {
		switch v := it.(type) {
		case int:
			b = appendInt(b, v)
		case Op:
			b = appendOp(b, v)
		case Raw:
			b = append(b, v...)
		default:
			panic(fmt.Sprintf("t1build: CS: %T is not int, t1build.Op, or t1build.Raw", it))
		}
	}
	return b
}

// #nosec G115 -- see the package comment
func appendInt(b []byte, v int) []byte {
	switch {
	case v >= -107 && v <= 107:
		return append(b, byte(v+139))
	case v >= 108 && v <= 1131:
		w := v - 108
		return append(b, byte(247+w/256), byte(w%256))
	case v >= -1131 && v <= -108:
		w := -v - 108
		return append(b, byte(251+w/256), byte(w%256))
	}
	n := int32(v)
	return append(b, 255, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// #nosec G115 -- see the package comment
func appendOp(b []byte, op Op) []byte {
	if op >= 1200 {
		return append(b, 12, byte(op-1200))
	}
	return append(b, byte(op))
}

// Glyph is one CharStrings entry.
type Glyph struct {
	Name string
	CS   []byte // plain; Build encrypts it
}

// Builder describes a program. The zero value of each field is the ordinary choice.
type Builder struct {
	Glyphs []Glyph
	Subrs  [][]byte // plain; a nil entry leaves that index undefined

	// Encoding is the built-in encoding, code to glyph name; nil writes StandardEncoding.
	Encoding map[byte]string

	// NoEncoding omits the /Encoding key entirely, for a font that declares none. Encoding is
	// ignored when this is set.
	NoEncoding bool

	// NoCharStrings omits the /CharStrings key entirely, for a font that declares none. Glyphs
	// and CharStringsExtra are ignored when this is set.
	NoCharStrings bool

	// LenIV, when set, is written as /lenIV and used for the Subrs and CharStrings; otherwise
	// the default of 4 is used and no key is written.
	LenIV *int

	FontMatrix string // the /FontMatrix value; empty writes [0.001 0 0 0.001 0 0]

	// FontExtra and PrivateExtra are PostScript written verbatim at the end of the clear-text
	// font dictionary and at the start of the Private dictionary.
	FontExtra, PrivateExtra string

	// CharStringsExtra is written verbatim after the last CharStrings entry, before its end.
	CharStringsExtra string
}

// Square is a program whose A (code 65) is the square (100,100)-(600,600), advance 700.
func Square() []byte {
	b, _ := Builder{
		Glyphs: []Glyph{
			{".notdef", CS(0, 500, HSBW, EndChar)},
			{"A", CS(100, 700, HSBW, 0, 100, RMoveTo, 500, HLineTo, 500, VLineTo, -500, HLineTo,
				ClosePath, EndChar)},
		},
		Encoding: map[byte]string{65: "A"},
	}.Build()
	return b
}

// Build returns the program and its clear-text length, the stream's /Length1.
func (b Builder) Build() (program []byte, length1 int) {
	lenIV := 4
	var priv strings.Builder
	priv.WriteString("dup /Private 8 dict dup begin\n" +
		"/RD {string currentfile exch readstring pop} executeonly def\n" +
		"/ND {noaccess def} executeonly def\n" +
		"/NP {noaccess put} executeonly def\n")
	if b.LenIV != nil {
		lenIV = *b.LenIV
		fmt.Fprintf(&priv, "/lenIV %d def\n", lenIV)
	}
	priv.WriteString("/MinFeature {16 16} def\n/password 5839 def\n")
	priv.WriteString(b.PrivateExtra)
	if b.Subrs != nil {
		fmt.Fprintf(&priv, "/Subrs %d array\n", len(b.Subrs))
		for i, s := range b.Subrs {
			if s == nil {
				continue
			}
			cs := charstring(s, lenIV)
			fmt.Fprintf(&priv, "dup %d %d RD %s NP\n", i, len(cs), cs)
		}
		priv.WriteString("ND\n")
	}
	if !b.NoCharStrings {
		fmt.Fprintf(&priv, "2 index /CharStrings %d dict dup begin\n", len(b.Glyphs))
		for _, g := range b.Glyphs {
			cs := charstring(g.CS, lenIV)
			fmt.Fprintf(&priv, "/%s %d RD %s ND\n", g.Name, len(cs), cs)
		}
		priv.WriteString(b.CharStringsExtra)
		priv.WriteString("end\n")
	}
	priv.WriteString("end\nreadonly put\nnoaccess put\n" +
		"dup /FontName get exch definefont pop\nmark currentfile closefile\n")

	matrix := b.FontMatrix
	if matrix == "" {
		matrix = "[0.001 0 0 0.001 0 0]"
	}
	var clear strings.Builder
	clear.WriteString("%!PS-AdobeFont-1.0: Test 001.000\n11 dict begin\n/FontName /Test def\n" +
		"/PaintType 0 def\n/FontType 1 def\n")
	fmt.Fprintf(&clear, "/FontMatrix %s readonly def\n/FontBBox {0 0 1000 1000} readonly def\n", matrix)
	switch {
	case b.NoEncoding:
	case b.Encoding == nil:
		clear.WriteString("/Encoding StandardEncoding def\n")
	default:
		clear.WriteString("/Encoding 256 array\n0 1 255 {1 index exch /.notdef put} for\n")
		for code := 0; code < 256; code++ {
			if name, ok := b.Encoding[byte(code)]; ok {
				clear.WriteString("dup " + strconv.Itoa(code) + " /" + name + " put\n")
			}
		}
		clear.WriteString("readonly def\n")
	}
	clear.WriteString(b.FontExtra)
	clear.WriteString("currentdict end\ncurrentfile eexec\n")

	cipher := encrypt(append([]byte{0x10, 0x20, 0x30, 0x40}, priv.String()...), 55665)
	return append([]byte(clear.String()), cipher...), clear.Len()
}

// charstring prefixes lenIV bytes and encrypts, or leaves the charstring plain for a lenIV of -1.
func charstring(cs []byte, lenIV int) []byte {
	if lenIV < 0 {
		return cs
	}
	return encrypt(append(make([]byte, lenIV), cs...), 4330)
}

// encrypt is the cipher of §7 of the format.
// #nosec G115 -- see the package comment
func encrypt(plain []byte, r uint16) []byte {
	out := make([]byte, len(plain))
	for i, p := range plain {
		c := p ^ byte(r>>8)
		out[i] = c
		r = (uint16(c)+r)*52845 + 22719
	}
	return out
}

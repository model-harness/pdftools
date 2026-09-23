package font

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/model-harness/pdftools/internal/ttfbuild"
)

// The font these tests read is assembled by internal/ttfbuild, which exists because two packages
// need the same bytes: this one, to check the parser reads the outlines that were written there,
// and render/native, to check a page set in that font rasterizes like the borrowed engine. Its
// three glyphs are chosen for what separates them — a square of straight lines, a diamond of four
// *off-curve* points whose implied midpoints a naive reader misses, and a composite that places
// the square twice.
type ttBuilder = ttfbuild.Builder

const (
	gidSquare    = ttfbuild.GIDSquare
	gidDiamond   = ttfbuild.GIDDiamond
	gidComposite = ttfbuild.GIDComposite
	ttNumGlyphs  = ttfbuild.NumGlyphs
)

func assembleTables(t map[string][]byte) []byte { return ttfbuild.AssembleTables(t) }

func TestParseTrueTypeRejectsWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"too short", []byte{0, 1, 0, 0}},
		{"a collection names no face", append([]byte("ttcf"), make([]byte, 20)...)},
		{"not a font at all", append([]byte("%PDF"), make([]byte, 20)...)},
		{"no glyf or loca", assembleTables(map[string][]byte{
			"head": make([]byte, 54), "maxp": make([]byte, 6),
			"cmap": {}, "glyf": nil, "loca": nil,
		})},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseTrueType(c.data); err == nil {
				t.Error("parsed, want an error")
			}
		})
	}
}

// TestOutlineOfStraightGlyph pins the point decoding against an outline written by hand.
//
// An exact comparison, which is only possible because the font is built here: every real-font
// test can assert that something was drawn and not that the right thing was.
func TestOutlineOfStraightGlyph(t *testing.T) {
	for _, tb := range []ttBuilder{
		{UnitsPerEm: 1000},
		{UnitsPerEm: 1000, LongLoca: true},
		// 2048 is the other unit grid in wide use, and the one that makes a reader that
		// forgets to scale wrong by a factor of two.
		{UnitsPerEm: 2048},
	} {
		t.Run(label(tb), func(t *testing.T) {
			tt, err := ParseTrueType(tb.Build())
			if err != nil {
				t.Fatalf("ParseTrueType: %v", err)
			}
			if got := tt.NumGlyphs(); got != ttNumGlyphs {
				t.Errorf("NumGlyphs = %d, want %d", got, ttNumGlyphs)
			}
			out, ok := tt.Outline(gidSquare)
			if !ok {
				t.Fatal("no outline for the square")
			}
			scale := 1000 / float64(tb.UnitsPerEm)
			want := Outline{
				{Op: SegMove, P: [3]Point{{100 * scale, 100 * scale}}},
				{Op: SegLine, P: [3]Point{{400 * scale, 100 * scale}}},
				{Op: SegLine, P: [3]Point{{400 * scale, 400 * scale}}},
				{Op: SegLine, P: [3]Point{{100 * scale, 400 * scale}}},
				{Op: SegLine, P: [3]Point{{100 * scale, 100 * scale}}},
				{Op: SegClose},
			}
			if !sameOutline(out, want) {
				t.Errorf("outline = %v\nwant     %v", out, want)
			}
		})
	}
}

// TestOutlineOfAllOffCurveGlyph is the case a reader gets wrong by treating the point list as a
// polygon.
//
// Every point of this contour is off-curve, so each pair implies an on-curve point midway
// between them and the contour is four quadratics through those midpoints — a circle, near
// enough. A reader that misses the implied midpoint draws the diamond of control points instead,
// which on a round glyph is visibly a polygon and on a straight one is invisible, so this is the
// glyph that has to be here.
func TestOutlineOfAllOffCurveGlyph(t *testing.T) {
	tt, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000}.Build())
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	out, ok := tt.Outline(gidDiamond)
	if !ok {
		t.Fatal("no outline for the diamond")
	}
	// Exact, like the square's, and for a reason counting quadratics could not cover: a reader
	// that takes each off-curve point as the *end* of the previous curve rather than as implying
	// a midpoint still emits four quadratics starting at the right place, and draws a shape that
	// passes through the control points instead of between them. Counting segments called that
	// correct, and so did a whole-image comparison at 48pt.
	//
	// The control points are (500,100), (900,500), (500,900) and (100,500), and the curve runs
	// through the midpoint of each consecutive pair: (700,300), (700,700), (300,700) and the
	// start at (300,300).
	want := Outline{
		{Op: SegMove, P: [3]Point{{300, 300}}},
		{Op: SegQuad, P: [3]Point{{500, 100}, {700, 300}}},
		{Op: SegQuad, P: [3]Point{{900, 500}, {700, 700}}},
		{Op: SegQuad, P: [3]Point{{500, 900}, {300, 700}}},
		{Op: SegQuad, P: [3]Point{{100, 500}, {300, 300}}},
		{Op: SegClose},
	}
	if !sameOutline(out, want) {
		t.Errorf("outline = %v\nwant     %v", out, want)
	}
}

// TestOutlineOfNestedContours pins the end-point array that separates one contour from the next.
//
// The ring is two squares in one flat point list, and endPtsOfContours is what says where the
// first ends: a reader that treats the list as one contour draws a single figure joining them,
// and a reader off by one on the boundary drops a segment from each. Both come out as a shape
// with ink in roughly the right place.
func TestOutlineOfNestedContours(t *testing.T) {
	tt, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000}.Build())
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	out, ok := tt.Outline(ttfbuild.GIDRing)
	if !ok {
		t.Fatal("no outline for the ring")
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{100, 100}}},
		{Op: SegLine, P: [3]Point{{400, 100}}},
		{Op: SegLine, P: [3]Point{{400, 400}}},
		{Op: SegLine, P: [3]Point{{100, 400}}},
		{Op: SegLine, P: [3]Point{{100, 100}}},
		{Op: SegClose},
		{Op: SegMove, P: [3]Point{{200, 200}}},
		{Op: SegLine, P: [3]Point{{300, 200}}},
		{Op: SegLine, P: [3]Point{{300, 300}}},
		{Op: SegLine, P: [3]Point{{200, 300}}},
		{Op: SegLine, P: [3]Point{{200, 200}}},
		{Op: SegClose},
	}
	if !sameOutline(out, want) {
		t.Errorf("outline = %v\nwant     %v", out, want)
	}
}

// TestCompositeGlyphPlacesItsComponents pins the component transform.
//
// Two copies of the square: one at the origin and one offset by (400,200) at half scale. An
// accented letter is exactly this shape, and getting the transform wrong puts the accent
// somewhere plausible — which is why the scaled copy is here and not just the offset one.
func TestCompositeGlyphPlacesItsComponents(t *testing.T) {
	// Over both unit grids, because a component's offset is in font units and the assembled part
	// is already in 1/1000 em, so the offset needs converting on its own. At 1000 units that
	// conversion is a multiplication by one, and the fixture had no other grid — so not
	// converting it at all placed the second copy correctly in every test there was.
	for _, tb := range []ttBuilder{{UnitsPerEm: 1000}, {UnitsPerEm: 2048}} {
		t.Run(label(tb), func(t *testing.T) {
			tt, err := ParseTrueType(tb.Build())
			if err != nil {
				t.Fatalf("ParseTrueType: %v", err)
			}
			out, ok := tt.Outline(gidComposite)
			if !ok {
				t.Fatal("no outline for the composite")
			}
			moves := 0
			for _, s := range out {
				if s.Op == SegMove {
					moves++
				}
			}
			if moves != 2 {
				t.Fatalf("%d contours, want 2 — one per component: %v", moves, out)
			}
			// The component offsets in ttfbuild are written in font units, so they scale with the
			// grid exactly as the outlines do.
			scale := 1000 / float64(tb.UnitsPerEm)
			// First component: the square unmoved.
			if !close2(out[0].P[0], Point{100 * scale, 100 * scale}) {
				t.Errorf("first component starts at %v, want (100,100) scaled", out[0].P[0])
			}
			// Second: half scale about the origin, then offset by (400,200).
			var second Point
			for i, s := range out {
				if s.Op == SegMove && i > 0 {
					second = s.P[0]
				}
			}
			want := Point{(100*0.5 + 400) * scale, (100*0.5 + 200) * scale}
			if !close2(second, want) {
				t.Errorf("second component starts at %v, want %v — half scale then offset", second, want)
			}
		})
	}
}

// TestGIDForRuneReadsTheFontsOwnCmap pins the lookup a simple TrueType font needs.
func TestGIDForRuneReadsTheFontsOwnCmap(t *testing.T) {
	tt, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000}.Build())
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	for _, c := range []struct {
		r    rune
		gid  uint16
		want bool
	}{
		{'A', gidSquare, true},
		{'B', gidDiamond, true},
		{'C', gidComposite, true},
		{'D', ttfbuild.GIDRing, true},
		{'Z', 0, false},
		{'é', 0, false},
	} {
		got, ok := tt.GIDForRune(c.r)
		if ok != c.want || (ok && got != c.gid) {
			t.Errorf("GIDForRune(%q) = %d, %v; want %d, %v", c.r, got, ok, c.gid, c.want)
		}
	}
}

// TestEmptyGlyphIsNotAnOutline pins that a space is not an error.
//
// Glyph 0 here has a zero-length loca entry, which is how every font stores a glyph with no ink.
// Returning an error for it would make a page of text fail on its first space; returning glyph
// 0's fallback box would put a box there.
func TestEmptyGlyphIsNotAnOutline(t *testing.T) {
	tt, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000}.Build())
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	if _, ok := tt.Outline(0); ok {
		t.Error("glyph 0 has an outline, want none: its loca entry is empty")
	}
	if _, ok := tt.Outline(ttNumGlyphs + 5); ok {
		t.Error("a glyph past maxp has an outline, want none")
	}
}

// TestGIDForCIDReadsTheMap pins both forms of /CIDToGIDMap, and that a CID past the end has no
// glyph rather than glyph 0 — which is a real glyph, the one a font draws for "not found", so
// returning it would put a box on the page where the font said nothing.
func TestGIDForCIDReadsTheMap(t *testing.T) {
	identity := &Font{}
	for _, cid := range []uint32{0, 1, 65535} {
		got, ok := identity.GIDForCID(cid)
		if !ok || uint32(got) != cid {
			t.Errorf("identity GIDForCID(%d) = %d, %v", cid, got, ok)
		}
	}
	if _, ok := identity.GIDForCID(0x10000); ok {
		t.Error("a CID past 16 bits mapped through Identity, want no glyph")
	}

	mapped := &Font{cidToGID: []byte{0, 0, 0, 7, 0, 9}}
	for _, c := range []struct {
		cid  uint32
		gid  uint16
		want bool
	}{
		{0, 0, true},
		{1, 7, true},
		{2, 9, true},
		{3, 0, false},
	} {
		got, ok := mapped.GIDForCID(c.cid)
		if ok != c.want || (ok && got != c.gid) {
			t.Errorf("GIDForCID(%d) = %d, %v; want %d, %v", c.cid, got, ok, c.gid, c.want)
		}
	}
}

// TestCmapSubtableFormats reads each subtable form directly, because the fixture font writes only
// one of them.
//
// The font internal/ttfbuild builds carries a format 4 subtable with idDelta segments, which is
// what a modern producer writes — and so formats 0, 6 and 12 and format 4's *other* branch had no
// input at all: about sixty lines of offset arithmetic that every test in this package walked
// past. A subtable is a byte layout and a lookup, so it is tested as one here rather than through
// a font, which is also the only way to write the shapes a builder would have to be extended to
// emit.
//
// The absence rule is the other subject. A cmap entry resolving to glyph 0 means "this font has
// no glyph for that character" — glyph 0 is notdef, a real glyph that draws a box — so every
// format has to read a zero as absence and not as an answer. Each case below includes a code that
// resolves to zero, in the branch where it is reachable.
func TestCmapSubtableFormats(t *testing.T) {
	put := func(b []byte, vs ...uint16) []byte {
		for _, v := range vs {
			b = binary.BigEndian.AppendUint16(b, v)
		}
		return b
	}

	// Format 0: a 256-byte byte-indexed array, which only reaches glyph 255.
	f0 := put(nil, 0, 262, 0)
	f0 = append(f0, make([]byte, 256)...)
	f0[6+'A'] = 11

	// Format 6: a single contiguous range of 16-bit indices.
	f6 := put(nil, 6, 0, 0, 'A', 3, 11, 0, 13)

	// Format 12: 32-bit groups, which is the only form that reaches beyond the BMP. Written with
	// two groups so the scan past a non-matching one is exercised, and the second covers a
	// supplementary-plane range.
	f12 := put(nil, 12, 0)
	f12 = binary.BigEndian.AppendUint32(f12, 16+2*12) // length
	f12 = binary.BigEndian.AppendUint32(f12, 0)       // language
	f12 = binary.BigEndian.AppendUint32(f12, 2)       // numGroups
	for _, g := range [][3]uint32{{'A', 'C', 11}, {0x1F600, 0x1F601, 40}} {
		for _, v := range g {
			f12 = binary.BigEndian.AppendUint32(f12, v)
		}
	}

	// Format 4's idRangeOffset branch: the glyph comes from an array whose address is computed
	// *from the offset's own slot*, which is the one piece of the format that cannot be read
	// without the specification open. One segment for 'A'–'B' plus the required terminator, an
	// idDelta of 5 that must be added to what the array holds, and a zero in the array for 'B'
	// to which it must not be.
	//
	// Three codes and not two, because the index into the array is 2*(c-startCode) and the first
	// code of a segment makes that zero: a reader that dropped the term entirely would answer
	// correctly for 'A' and wrongly for everything after it.
	f4 := put(nil, 4, 16+8*2+6, 0, 4, 4, 1, 0)
	f4 = put(f4, 'C', 0xFFFF) // endCode
	f4 = put(f4, 0)           // reservedPad
	f4 = put(f4, 'A', 0xFFFF) // startCode
	f4 = put(f4, 5, 1)        // idDelta
	f4 = put(f4, 4, 0)        // idRangeOffset: 4 from its own slot lands on the array below
	f4 = put(f4, 11, 0, 13)   // glyphIdArray: 'A' -> 11+5, 'B' -> absent, 'C' -> 13+5

	for _, c := range []struct {
		name string
		sub  []byte
		code uint32
		gid  uint16
		want bool
	}{
		{"format 0", f0, 'A', 11, true},
		{"format 0, unmapped code reads as absent", f0, 'B', 0, false},
		{"format 0, past a byte", f0, 0x100, 0, false},
		{"format 6", f6, 'A', 11, true},
		{"format 6, a zero inside the range", f6, 'B', 0, false},
		{"format 6, past the range", f6, 'D', 0, false},
		{"format 12", f12, 'B', 12, true},
		{"format 12, a later group", f12, 0x1F601, 41, true},
		{"format 12, between the groups", f12, 0x100, 0, false},
		{"format 4 by idRangeOffset", f4, 'A', 16, true},
		{"format 4, further into the glyph array", f4, 'C', 18, true},
		{"format 4, a zero in the glyph array", f4, 'B', 0, false},
		{"format 4, below the first segment", f4, '0', 0, false},
		{"a truncated subtable", f4[:3], 'A', 0, false},
		{"an unknown format", put(nil, 99, 0, 0, 0), 'A', 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := &TrueType{}
			got, ok := tt.lookup(c.sub, c.code)
			if ok != c.want || (ok && got != c.gid) {
				t.Errorf("lookup(%#x) = %d, %v; want %d, %v", c.code, got, ok, c.gid, c.want)
			}
		})
	}
}

// TestGIDForCodeReadsTheSymbolicSubtable pins §9.6.5.4's route for a symbolic font.
//
// A symbolic font's codes mean nothing outside it, so the lookup goes straight to the (3,0)
// subtable with the *code* rather than through a character — and because such a font's cmap is
// conventionally keyed in the Private Use Area, the code is tried both as written and offset into
// 0xF000. A dingbat font is the common case. None of this had an input before: the fixture font is
// non-symbolic and carries a (3,1) subtable, so both the subtable choice and the 0xF000 offset
// could be deleted without a test noticing.
func TestGIDForCodeReadsTheSymbolicSubtable(t *testing.T) {
	sub := func(lo uint32, gid uint16) []byte {
		b := binary.BigEndian.AppendUint16(nil, 6)
		b = binary.BigEndian.AppendUint16(b, 0)
		b = binary.BigEndian.AppendUint16(b, 0)
		b = binary.BigEndian.AppendUint16(b, uint16(lo))
		b = binary.BigEndian.AppendUint16(b, 1)
		return binary.BigEndian.AppendUint16(b, gid)
	}

	direct := &TrueType{cmapSubtabs: map[uint32][]byte{3<<16 | 0: sub(0x41, 11)}}
	if got, ok := direct.GIDForCode(0x41); !ok || got != 11 {
		t.Errorf("a code keyed directly = %d, %v; want 11, true", got, ok)
	}
	// The same code, in a font that keyed its cmap in the Private Use Area instead.
	pua := &TrueType{cmapSubtabs: map[uint32][]byte{3<<16 | 0: sub(0xF041, 11)}}
	if got, ok := pua.GIDForCode(0x41); !ok || got != 11 {
		t.Errorf("a code keyed at 0xF000 = %d, %v; want 11, true", got, ok)
	}
	// A (3,1) subtable is not consulted by this route: reading it would map the code as though it
	// were a character, which for a symbolic font is how a dingbat comes out as a letter.
	uni := &TrueType{cmapSubtabs: map[uint32][]byte{3<<16 | 1: sub(0x41, 11)}}
	if _, ok := uni.GIDForCode(0x41); ok {
		t.Error("the symbolic route read the Unicode subtable")
	}
	// An old producer's symbolic font keys (1,0) by code, which is the documented fallback.
	mac := &TrueType{cmapSubtabs: map[uint32][]byte{1<<16 | 0: sub(0x41, 11)}}
	if got, ok := mac.GIDForCode(0x41); !ok || got != 11 {
		t.Errorf("a (1,0) subtable = %d, %v; want 11, true", got, ok)
	}
}

// TestCompositeGlyphRefusesPointMatching pins the component form this reader declines.
//
// A component can be placed by naming two point indices that must coincide instead of by an
// offset, and doing that needs the assembled outline of the part already placed. Refusing is the
// decision ADR 0015's refusal contract implies — placing it wrongly moves an accent somewhere
// plausible — and the refusal had no input, so returning the component unplaced at the origin
// would have passed every test here.
func TestCompositeGlyphRefusesPointMatching(t *testing.T) {
	// ARG_1_AND_2_ARE_WORDS without ARGS_ARE_XY_VALUES: the two arguments are point indices.
	g := binary.BigEndian.AppendUint16(nil, 0xFFFF)
	for range 4 {
		g = binary.BigEndian.AppendUint16(g, 0)
	}
	g = binary.BigEndian.AppendUint16(g, 0x0001) // flags: words, no XY values
	g = binary.BigEndian.AppendUint16(g, gidSquare)
	g = binary.BigEndian.AppendUint16(g, 3) // point index in the parent
	g = binary.BigEndian.AppendUint16(g, 1) // point index in the component

	tt, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000}.Build())
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	if out, ok := tt.composite(g[10:], 0); ok {
		t.Errorf("point-matched component placed at %v, want a refusal", out)
	}
}

func label(tb ttBuilder) string {
	s := "unitsPerEm " + itoa(int(tb.UnitsPerEm))
	if tb.LongLoca {
		s += ", long loca"
	}
	return s
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func close2(a, b Point) bool {
	return math.Abs(a.X-b.X) < 0.01 && math.Abs(a.Y-b.Y) < 0.01
}

func sameOutline(a, b Outline) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Op != b[i].Op {
			return false
		}
		for j := range a[i].P {
			if !close2(a[i].P[j], b[i].P[j]) {
				return false
			}
		}
	}
	return true
}

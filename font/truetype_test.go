package font

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
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
		want string // a substring of the refusal, so this pins the reason and not just that there is one
	}{
		{"empty", nil, "is 0 bytes"},
		{"too short", []byte{0, 1, 0, 0}, "is 4 bytes"},
		{"a collection names no face", append([]byte("ttcf"), make([]byte, 20)...), "names no face to use"},
		{"not a font at all", append([]byte("%PDF"), make([]byte, 20)...), "not a truetype program"},
		{"no glyf or loca", assembleTables(map[string][]byte{
			// head declares a valid unitsPerEm, and hhea/hmtx are present and valid, or an
			// earlier check would refuse the face for that reason and never reach the glyf/loca
			// check this case is about.
			"head": head54(1000), "hhea": make([]byte, 36), "hmtx": make([]byte, 4),
			"maxp": make([]byte, 6), "cmap": {}, "glyf": nil, "loca": nil,
		}), "no glyf/loca outlines"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseTrueType(c.data)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("ParseTrueType err = %v, want it to contain %q", err, c.want)
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
			out, _, err := tt.Outline(gidSquare, math.MaxInt)
			if err != nil || out == nil {
				t.Fatalf("no outline for the square: %v", err)
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
	out, _, err := tt.Outline(gidDiamond, math.MaxInt)
	if err != nil || out == nil {
		t.Fatalf("no outline for the diamond: %v", err)
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
	out, _, err := tt.Outline(ttfbuild.GIDRing, math.MaxInt)
	if err != nil || out == nil {
		t.Fatalf("no outline for the ring: %v", err)
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
			out, _, err := tt.Outline(gidComposite, math.MaxInt)
			if err != nil || out == nil {
				t.Fatalf("no outline for the composite: %v", err)
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
	if out, _, err := tt.Outline(0, math.MaxInt); out != nil || err != nil {
		t.Errorf("glyph 0 = %v, %v; want nil, nil: its loca entry is empty", out, err)
	}
	// Past maxp is not an empty glyph but a code the font cannot answer for, and drawing nothing
	// there would be a silent gap.
	if _, _, err := tt.Outline(ttNumGlyphs+5, math.MaxInt); err == nil {
		t.Error("a glyph past maxp has no error")
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
			// numGlyphs must cover every gid a fixture above resolves to, or notFound's own
			// bound (R#0: no exemption for numGlyphs == 0) zeroes the answer before the format
			// under test is ever exercised.
			tt := &TrueType{numGlyphs: 1000}
			got, ok := tt.lookup(c.sub, c.code)
			if ok != c.want || (ok && got != c.gid) {
				t.Errorf("lookup(%#x) = %d, %v; want %d, %v", c.code, got, ok, c.gid, c.want)
			}
		})
	}
}

// TestSubtablesAreReadByPlatformAndEncoding pins that a lookup reads the one subtable it names.
//
// §9.6.5.4's routes are defined by which subtable they consult — (3,1) by character, (3,0) and
// (1,0) by code — and a program's subtables routinely disagree: a subsetter writes a (1,0) that
// maps a code to whatever its own ordering put there. So each lookup here is into a program whose
// other subtable maps the same key to a different glyph, and a lookup that fell through to it
// would answer rather than miss.
//
// Two records naming the same subtable are resolved as pdfium's charmap selection resolves them:
// the first one wins. A program that states two answers is one where the order of the records is
// the only evidence of which it means.
func TestSubtablesAreReadByPlatformAndEncoding(t *testing.T) {
	parse := func(c ...ttfbuild.Cmap) *TrueType {
		t.Helper()
		tt, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000, Cmaps: c}.Build())
		if err != nil {
			t.Fatalf("ParseTrueType: %v", err)
		}
		return tt
	}
	tt := parse(
		ttfbuild.Cmap{Platform: 1, Encoding: 0, Map: map[uint16]uint16{'A': gidDiamond}},
		ttfbuild.Cmap{Platform: 3, Encoding: 1, Map: map[uint16]uint16{'A': gidSquare}},
		ttfbuild.Cmap{Platform: 3, Encoding: 1, Map: map[uint16]uint16{'A': gidComposite}},
	)
	for _, c := range []struct {
		platform, encoding uint16
		has                bool
		gid                uint16
	}{
		{3, 1, true, gidSquare},
		{1, 0, true, gidDiamond},
		{3, 0, false, 0},
		{0, 3, false, 0},
	} {
		if got := tt.HasSubtable(c.platform, c.encoding); got != c.has {
			t.Errorf("HasSubtable(%d, %d) = %v, want %v", c.platform, c.encoding, got, c.has)
		}
		got, ok := tt.GIDInSubtable(c.platform, c.encoding, 'A')
		if ok != c.has || got != c.gid {
			t.Errorf("GIDInSubtable(%d, %d, 'A') = %d, %v; want %d, %v", c.platform, c.encoding,
				got, ok, c.gid, c.has)
		}
	}
	if _, ok := tt.GIDInSubtable(3, 1, 'B'); ok {
		t.Error("GIDInSubtable(3, 1, 'B') found a glyph for a character the subtable does not map")
	}
	if !tt.HasCmap() {
		t.Error("HasCmap() = false for a program with three subtables")
	}
	if one := parse(ttfbuild.Cmap{Platform: 1, Encoding: 0, Map: map[uint16]uint16{'A': gidDiamond}}); !one.HasCmap() {
		t.Error("HasCmap() = false for a program with one subtable")
	}
	bare, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000, NoCmap: true}.Build())
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	if bare.HasCmap() || bare.HasSubtable(3, 1) {
		t.Error("a program with no cmap reports a subtable")
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
	if out, _, err := tt.composite(g[10:], 0, &ttRun{budget: math.MaxInt}, 0); !errors.Is(err, errPointMatching) {
		t.Errorf("point-matched component = %v, %v; want errPointMatching", out, err)
	}
}

// TestCompositeDepthLimit pins the nesting bound at maxComposite: a chain nested exactly that
// deep draws, and one level deeper is refused.
func TestCompositeDepthLimit(t *testing.T) {
	// chain builds a font with n composite glyphs (gid 0..n-1), each a single component naming
	// the next, ending in a trivial empty leaf at gid n. Outline(0) then recurses to depth n
	// before the leaf returns.
	chain := func(n int) *TrueType {
		var glyf []byte
		put16 := func(v uint16) { glyf = binary.BigEndian.AppendUint16(glyf, v) }
		offs := []uint32{0}
		for i := 0; i < n; i++ {
			put16(0xFFFF) // composite
			for j := 0; j < 4; j++ {
				put16(0) // bbox
			}
			put16(0x0001 | 0x0002) // ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES, no MORE_COMPONENTS
			put16(uint16(i + 1))   // the one component names the next glyph
			put16(0)
			put16(0) // dx, dy
			offs = append(offs, uint32(len(glyf)))
		}
		put16(0) // leaf: numberOfContours 0
		for j := 0; j < 4; j++ {
			put16(0) // bbox
		}
		offs = append(offs, uint32(len(glyf)))

		var loca []byte
		for _, o := range offs {
			loca = binary.BigEndian.AppendUint32(loca, o)
		}
		return &TrueType{longLoca: true, loca: loca, glyf: glyf, numGlyphs: n + 1, unitsPerEm: 1000}
	}

	if out, _, err := chain(maxComposite).Outline(0, math.MaxInt); err != nil {
		t.Errorf("nested %d deep = %v, %v; want it to draw", maxComposite, out, err)
	}
	if _, _, err := chain(maxComposite+1).Outline(0, math.MaxInt); err == nil ||
		!strings.Contains(err.Error(), "nest more than") {
		t.Errorf("nested %d deep did not refuse the extra level", maxComposite+1)
	}
}

// TestCompositeVisitLimit pins the total-visits bound at maxComponents: a flat composite
// visiting exactly that many glyphs draws.
//
// maxComponents counts the top glyph's own visit too, so maxComponents-1 component records
// naming a trivial leaf bring the total to exactly maxComponents.
func TestCompositeVisitLimit(t *testing.T) {
	leaf := make([]byte, 10) // numberOfContours 0, zero bbox: an empty leaf that always draws

	var root []byte
	put16 := func(v uint16) { root = binary.BigEndian.AppendUint16(root, v) }
	put16(0xFFFF) // composite
	for i := 0; i < 4; i++ {
		put16(0) // bbox
	}
	const components = maxComponents - 1
	for i := 0; i < components; i++ {
		flags := uint16(0x0001 | 0x0002) // ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES
		if i < components-1 {
			flags |= 0x0020 // MORE_COMPONENTS
		}
		put16(flags)
		put16(0) // sub: the leaf, gid 0
		put16(0)
		put16(0) // dx, dy
	}

	glyf := append(append([]byte{}, leaf...), root...)
	var loca []byte
	for _, o := range []uint32{0, uint32(len(leaf)), uint32(len(glyf))} {
		loca = binary.BigEndian.AppendUint32(loca, o)
	}
	tt := &TrueType{longLoca: true, loca: loca, glyf: glyf, numGlyphs: 2, unitsPerEm: 1000}

	if out, _, err := tt.Outline(1, math.MaxInt); err != nil {
		t.Errorf("composite visiting exactly %d glyphs = %v, %v; want it to draw", maxComponents, out, err)
	}
}

// TestGlyphDataRefusesIndexAtNumGlyphs pins the maxp bound in glyphData: an index equal to the
// declared glyph count is refused as past the end, not read from loca.
//
// loca here is sized well past what index 3 would need to read cleanly, so a refusal can only be
// the maxp guard — if that guard let the index through, loca's own length check would not catch
// it either, and the reader would return a glyph instead of an error.
func TestGlyphDataRefusesIndexAtNumGlyphs(t *testing.T) {
	tt := &TrueType{numGlyphs: 3, loca: make([]byte, 12)}
	out, err := tt.glyphData(3)
	if err == nil || !strings.Contains(err.Error(), "maxp declares") {
		t.Errorf("glyphData(3) = %v, %v; want the maxp-past-the-end error", out, err)
	}
}

// TestGlyphDataReadsTheLastLongLocaEntry pins the long-loca bound: a glyph index whose loca
// entry sits exactly at the table's end reads cleanly rather than being refused as past it.
func TestGlyphDataReadsTheLastLongLocaEntry(t *testing.T) {
	glyf := make([]byte, 8)
	loca := make([]byte, 12) // 3 entries: (i+2)*4 == len(loca) for i == 1
	binary.BigEndian.PutUint32(loca[4:], 4)
	binary.BigEndian.PutUint32(loca[8:], 8)
	tt := &TrueType{longLoca: true, loca: loca, glyf: glyf, numGlyphs: 2}

	out, err := tt.glyphData(1)
	if err != nil {
		t.Fatalf("glyphData(1) = %v, %v; want the last glyph to read cleanly", out, err)
	}
	if len(out) != 4 {
		t.Errorf("glyphData(1) len = %d, want 4", len(out))
	}
}

// TestSimpleGlyphRefusesARepeatedContourEnd pins the strictly-increasing rule on
// endPtsOfContours: a second contour whose end point repeats the first's is a zero-length
// contour, refused rather than accepted.
func TestSimpleGlyphRefusesARepeatedContourEnd(t *testing.T) {
	g := make([]byte, 6)
	binary.BigEndian.PutUint16(g[0:], 3)
	binary.BigEndian.PutUint16(g[2:], 3) // the second contour's end repeats the first's
	tt := &TrueType{unitsPerEm: 1000}
	if out, err := tt.simple(g, 2, &ttRun{budget: math.MaxInt}); err == nil || !strings.Contains(err.Error(), "do not increase") {
		t.Errorf("simple() = %v, %v; want the non-increasing contour-end error", out, err)
	}
}

// TestSimpleGlyphPointLimit pins the point-count bound at maxGlyphPoints: a glyph with exactly
// that many points is accepted, and one more is refused.
func TestSimpleGlyphPointLimit(t *testing.T) {
	// build writes npts points sharing one contour, each on-curve and flagged X_IS_SAME /
	// Y_IS_SAME so no coordinate bytes are needed — the point count is all this glyph tests.
	build := func(npts int) []byte {
		g := binary.BigEndian.AppendUint16(nil, uint16(npts-1)) // endPtsOfContours[0]
		g = binary.BigEndian.AppendUint16(g, 0)                 // instructionLength
		for i := 0; i < npts; i++ {
			g = append(g, 0x31) // on-curve, x-same, y-same
		}
		return g
	}
	tt := &TrueType{unitsPerEm: 1000}
	if out, err := tt.simple(build(maxGlyphPoints), 1, &ttRun{budget: math.MaxInt}); err != nil {
		t.Errorf("%d points = %v, %v; want it accepted", maxGlyphPoints, out, err)
	}
	if _, err := tt.simple(build(maxGlyphPoints+1), 1, &ttRun{budget: math.MaxInt}); err == nil ||
		!strings.Contains(err.Error(), "past the limit") {
		t.Errorf("%d points did not refuse", maxGlyphPoints+1)
	}
}

// TestCompositeSegmentLimit pins the total-segments bound at maxGlyphSegments: a composite whose
// components together draw exactly that many segments is accepted.
//
// Each of 256 components draws a single 254-point on-curve contour — a move, 254 lines and a
// close, 256 segments — for 256*256 = 65536 = maxGlyphSegments total. Its 257 visits and 254
// points a glyph are well inside maxComponents and maxGlyphPoints, so neither guard fires first.
func TestCompositeSegmentLimit(t *testing.T) {
	const pointsPerLeaf = 254
	const components = 256

	leaf := binary.BigEndian.AppendUint16(nil, 1) // numberOfContours
	leaf = append(leaf, make([]byte, 8)...)       // bbox
	leaf = binary.BigEndian.AppendUint16(leaf, pointsPerLeaf-1)
	leaf = binary.BigEndian.AppendUint16(leaf, 0) // instructionLength
	for i := 0; i < pointsPerLeaf; i++ {
		leaf = append(leaf, 0x31) // on-curve, x-same, y-same
	}

	root := binary.BigEndian.AppendUint16(nil, 0xFFFF) // composite
	root = append(root, make([]byte, 8)...)            // bbox
	for i := 0; i < components; i++ {
		flags := uint16(0x0001 | 0x0002)
		if i < components-1 {
			flags |= 0x0020 // MORE_COMPONENTS
		}
		root = binary.BigEndian.AppendUint16(root, flags)
		root = binary.BigEndian.AppendUint16(root, 0) // sub: the leaf, gid 0
		root = binary.BigEndian.AppendUint16(root, 0) // dx
		root = binary.BigEndian.AppendUint16(root, 0) // dy
	}

	glyf := append(append([]byte{}, leaf...), root...)
	var loca []byte
	for _, o := range []uint32{0, uint32(len(leaf)), uint32(len(glyf))} {
		loca = binary.BigEndian.AppendUint32(loca, o)
	}
	tt := &TrueType{longLoca: true, loca: loca, glyf: glyf, numGlyphs: 2, unitsPerEm: 1000}

	out, _, err := tt.Outline(1, math.MaxInt)
	if err != nil {
		t.Fatalf("composite drawing exactly %d segments = %v; want it accepted", maxGlyphSegments, err)
	}
	if len(out) != maxGlyphSegments {
		t.Errorf("drew %d segments, want %d", len(out), maxGlyphSegments)
	}
}

// TestCompositeChargesVisitsAndPoints pins C23's fix, and R#4's on top of it: a TrueType glyph's
// work is 1 per glyph visited, plus a simple glyph's own contour count, plus its points, charged
// whether or not those points draw a segment.
//
// The leaf is leafPoints one-point contours — each too short to draw anything, by appendContour's
// `n < 2` rule — so a reader that charged output segments instead of points would see every visit
// to it cost nothing, and a page could ask for as many as maxComponents allows for free. Charged
// by contours and points instead, the composite's exact cost is knowable ahead of building it: one
// visit per glyph, plus leafPoints contours and leafPoints points per reference to the leaf — the
// leaf here has one point per contour, so the two charges happen to be equal.
func TestCompositeChargesVisitsAndPoints(t *testing.T) {
	const leafPoints = 9999 // just under maxGlyphPoints, the shape C23's fan-out attack used
	const components = 5

	// leaf: leafPoints one-point contours, all on-curve so no coordinate bytes are needed.
	leaf := binary.BigEndian.AppendUint16(nil, uint16(leafPoints)) // numberOfContours
	leaf = append(leaf, make([]byte, 8)...)                        // bbox
	for i := 0; i < leafPoints; i++ {
		leaf = binary.BigEndian.AppendUint16(leaf, uint16(i)) // endPtsOfContours[i] == i
	}
	leaf = binary.BigEndian.AppendUint16(leaf, 0) // instructionLength
	for i := 0; i < leafPoints; i++ {
		leaf = append(leaf, 0x31) // on-curve, x-same, y-same
	}

	root := binary.BigEndian.AppendUint16(nil, 0xFFFF) // composite
	root = append(root, make([]byte, 8)...)            // bbox
	for i := 0; i < components; i++ {
		flags := uint16(0x0001 | 0x0002) // ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES
		if i < components-1 {
			flags |= 0x0020 // MORE_COMPONENTS
		}
		root = binary.BigEndian.AppendUint16(root, flags)
		root = binary.BigEndian.AppendUint16(root, 0) // sub: the leaf, gid 0
		root = binary.BigEndian.AppendUint16(root, 0)
		root = binary.BigEndian.AppendUint16(root, 0)
	}

	glyf := append(append([]byte{}, leaf...), root...)
	var loca []byte
	for _, o := range []uint32{0, uint32(len(leaf)), uint32(len(glyf))} {
		loca = binary.BigEndian.AppendUint32(loca, o)
	}
	tt := &TrueType{longLoca: true, loca: loca, glyf: glyf, numGlyphs: 2, unitsPerEm: 1000}

	// One visit per glyph — the composite plus each of its five references to the leaf — plus one
	// unit per contour and one per point, whether or not the point draws a segment.
	want := (1 + components) + 2*components*leafPoints
	out, work, err := tt.Outline(1, want)
	if err != nil {
		t.Fatalf("Outline at exactly %d = %v, want it accepted", want, err)
	}
	if work != want {
		t.Errorf("work = %d, want %d (visits plus contours plus points, not segments)", work, want)
	}
	if len(out) != 0 {
		t.Errorf("segments = %d, want 0: each leaf contour is one point and draws nothing", len(out))
	}
	if _, _, err := tt.Outline(1, want-1); !errors.Is(err, ErrGlyphBudget) {
		t.Errorf("Outline at %d = %v, want ErrGlyphBudget", want-1, err)
	}
}

// TestSimpleGlyphContourCountIsChargedBeforeTheEndsAreRead pins R#4: simple() charges a glyph's
// contour count before it allocates or reads a single contour end, so a corrupt count too big for
// the remaining budget is refused instead of sized into an allocation.
//
// g is far shorter than contours*2+2 needs — every one of its ends would be truncated — so a
// reader that checked the length first would return errTruncatedGlyph regardless of budget. The
// budget check has to come first to be seen at all: sufficient budget lets the truncation error
// through unchanged, and insufficient budget preempts it with ErrGlyphBudget.
func TestSimpleGlyphContourCountIsChargedBeforeTheEndsAreRead(t *testing.T) {
	const contours = 5000
	g := make([]byte, 4) // nowhere near contours*2+2 bytes
	tt := &TrueType{unitsPerEm: 1000}

	if _, err := tt.simple(g, contours, &ttRun{budget: contours - 1}); !errors.Is(err, ErrGlyphBudget) {
		t.Errorf("budget %d = %v, want ErrGlyphBudget before the truncation check fires", contours-1, err)
	}
	if _, err := tt.simple(g, contours, &ttRun{budget: math.MaxInt}); !errors.Is(err, errTruncatedGlyph) {
		t.Errorf("budget MaxInt = %v, want errTruncatedGlyph: the fixture is truncated once the "+
			"charge itself does not refuse it", err)
	}
}

// simpleGlyphOf builds a one-glyph font whose glyph 0 declares npts points in one contour, with
// only flagBytes of the flags-and-coordinates section present — flagBytes < npts truncates it.
func simpleGlyphOf(npts, flagBytes int) *TrueType {
	g := binary.BigEndian.AppendUint16(nil, 1) // numberOfContours
	g = append(g, make([]byte, 8)...)          // bbox
	g = binary.BigEndian.AppendUint16(g, uint16(npts-1))
	g = binary.BigEndian.AppendUint16(g, 0) // instructionLength
	for i := 0; i < flagBytes; i++ {
		g = append(g, 0x31) // on-curve, x-same, y-same: no coordinate bytes follow any of these
	}
	var loca []byte
	for _, o := range []uint32{0, uint32(len(g))} {
		loca = binary.BigEndian.AppendUint32(loca, o)
	}
	return &TrueType{longLoca: true, loca: loca, glyf: g, numGlyphs: 1, unitsPerEm: 1000}
}

// TestTrueTypeChargingOrderAndErrorAccounting pins R#5: work is charged visit, then contours, then
// points, in that order, and an error path reports what was charged before it failed rather than
// zero — the three shapes mutants A, B and C each break.
func TestTrueTypeChargingOrderAndErrorAccounting(t *testing.T) {
	t.Run("an empty glyph is one visit and nothing else", func(t *testing.T) {
		tt, err := ParseTrueType(ttBuilder{UnitsPerEm: 1000}.Build())
		if err != nil {
			t.Fatalf("ParseTrueType: %v", err)
		}
		// Glyph 0 is ttfbuild's unused first glyph, with an empty loca entry.
		out, work, err := tt.Outline(0, math.MaxInt)
		if out != nil || err != nil || work != 1 {
			t.Errorf("empty glyph = %v, work %d, err %v; want nil, 1, nil", out, work, err)
		}
	})
	t.Run("a simple glyph truncated in its coordinates still reports what it charged", func(t *testing.T) {
		const npts = 10
		tt := simpleGlyphOf(npts, 2) // only 2 of 10 flag bytes present
		out, work, err := tt.Outline(0, math.MaxInt)
		want := 1 + 1 + npts // visit, one contour, npts points
		if out != nil || work != want || !errors.Is(err, errTruncatedGlyph) {
			t.Errorf("truncated glyph = %v, work %d, err %v; want nil, %d, errTruncatedGlyph",
				out, work, want, err)
		}
	})
	t.Run("points past the remaining budget refuse before a truncated coordinate is read", func(t *testing.T) {
		const npts = 10
		tt := simpleGlyphOf(npts, 2) // truncated the same way, but the budget is what should fire
		// visit(1) + contour(1) leaves 3 of a 5-unit budget, short of the 10 points about to be
		// charged: ErrGlyphBudget has to win the race against errTruncatedGlyph.
		out, work, err := tt.Outline(0, 5)
		if out != nil || work != 1+1+npts || !errors.Is(err, ErrGlyphBudget) {
			t.Errorf("over-budget truncated glyph = %v, work %d, err %v; want nil, %d, ErrGlyphBudget",
				out, work, 1+1+npts, err)
		}
	})
}

// --- round 5: R#0, the left side bearing shift ---

// hmtxPairs writes an hmtx table of len(vals)/2 advance/lsb pairs — the shape a table with
// numberOfHMetrics entries and no trailing array has.
func hmtxPairs(vals ...int) []byte {
	b := make([]byte, 0, len(vals)*2)
	for _, v := range vals {
		b = binary.BigEndian.AppendUint16(b, uint16(int16(v)))
	}
	return b
}

// TestLeftSideBearingBranches pins lsb (tt_face_get_metrics, ttmtx.c:227-306) at each of its
// branches' boundaries directly, bypassing ParseTrueType: lsb reads only t.hmtx and
// t.numberOfHMetrics, so a literal *TrueType exercises exactly the same code a parsed one would,
// without a directory's own rules (round 5's other tests) standing between the test and the
// boundary. A table kept within the file keeps its declared length, which need not be a multiple
// of 4, so two rows end the table partway through the bearing they ask for.
func TestLeftSideBearingBranches(t *testing.T) {
	for _, c := range []struct {
		name string
		k    int
		hmtx []byte
		gid  uint16
		want int
	}{
		{"numberOfHMetrics 0 is NoData regardless of hmtx", 0, hmtxPairs(500, 42), 0, 0},
		{"gid below k reads its own pair", 2, hmtxPairs(500, 42, 600, -7), 1, -7},
		{"gid below k whose own pair runs past the table's end is NoData", 2, hmtxPairs(500, 42), 1, 0},
		{"gid below k whose pair ends at its advance is NoData", 2, hmtxPairs(500, 42, 600), 1, 0},
		{"gid at k, aadvance unreadable, is NoData (the last pair itself is short)",
			3, hmtxPairs(500, 42, 600, -7), 5, 0},
		{"gid at or past k reads the trailing lsb array", 1, append(hmtxPairs(500, 42), hmtxPairs(-16)...), 1, -16},
		{"gid at or past k with no trailing entry is NoData", 1, hmtxPairs(500, 42), 1, 0},
		{"gid at or past k whose trailing entry is one byte short is NoData",
			1, append(hmtxPairs(500, 42), 0xff), 1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := &TrueType{numberOfHMetrics: c.k, hmtx: c.hmtx}
			if got := tt.lsb(c.gid); got != c.want {
				t.Errorf("lsb(%d) = %d, want %d", c.gid, got, c.want)
			}
		})
	}
}

// twoPointGlyph writes a simple glyph with the given header xMin and one degenerate contour of
// two on-curve, zero-delta points — real point data is irrelevant to every test below, which asks
// only what pp1 does with the header's own xMin field, so the contour exists only to give
// outline() something to return that a test can read an X coordinate off of.
func twoPointGlyph(xMin int16) []byte {
	g := binary.BigEndian.AppendUint16(nil, 1) // numberOfContours
	g = binary.BigEndian.AppendUint16(g, uint16(xMin))
	g = append(g, make([]byte, 6)...)       // yMin, xMax, yMax
	g = binary.BigEndian.AppendUint16(g, 1) // endPtsOfContours[0]: 2 points
	g = binary.BigEndian.AppendUint16(g, 0) // instructionLength
	g = append(g, 0x31, 0x31)               // two on-curve, x-same, y-same flags: both at (0,0)
	return g
}

// zeroContourGlyph writes a simple glyph header declaring zero contours and the given xMin, the
// shape ttgload.c:1543-1549 zeroes the bbox for — "a space glyph" — so a test can tell whether
// outline() also zeroes xMin for it, the way that rule requires, rather than trusting the header.
func zeroContourGlyph(xMin int16) []byte {
	g := binary.BigEndian.AppendUint16(nil, 0) // numberOfContours
	g = binary.BigEndian.AppendUint16(g, uint16(xMin))
	return append(g, make([]byte, 6)...)
}

// oneComponentComposite writes a composite with one component naming sub, offset by dx, dy, with
// USE_MY_METRICS set when useMyMetrics is true.
func oneComponentComposite(xMin int16, sub uint16, dx, dy int16, useMyMetrics bool) []byte {
	g := binary.BigEndian.AppendUint16(nil, 0xFFFF) // composite marker
	g = binary.BigEndian.AppendUint16(g, uint16(xMin))
	g = append(g, make([]byte, 6)...)
	flags := uint16(0x0001 | 0x0002) // ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES
	if useMyMetrics {
		flags |= 0x0200
	}
	g = binary.BigEndian.AppendUint16(g, flags)
	g = binary.BigEndian.AppendUint16(g, sub)
	g = binary.BigEndian.AppendUint16(g, uint16(dx))
	g = binary.BigEndian.AppendUint16(g, uint16(dy))
	return g
}

// buildTT assembles a *TrueType directly from glyf data for each gid in order, bypassing
// ParseTrueType's directory rules — round 5's other tests already cover those — so a pp1 test
// controls each glyph's header and hmtx on its own.
func buildTT(k int, hmtx []byte, glyphs ...[]byte) *TrueType {
	var glyf []byte
	offs := []uint32{0}
	for _, g := range glyphs {
		glyf = append(glyf, g...)
		offs = append(offs, uint32(len(glyf)))
	}
	var loca []byte
	for _, o := range offs {
		loca = binary.BigEndian.AppendUint32(loca, o)
	}
	return &TrueType{
		longLoca: true, loca: loca, glyf: glyf, numGlyphs: len(glyphs), unitsPerEm: 1000,
		numberOfHMetrics: k, hmtx: hmtx,
	}
}

// TestPhantomPointShiftsTheOutline pins R#0 for a simple glyph: Outline translates the result by
// xMin-lsb (tt_loader_set_pp, ttgload.c:1346-1350, then the top-level translate at
// ttgload.c:2665-2670), so a glyph whose lsb disagrees with its own header xMin is drawn away from
// where its raw coordinates say, by exactly lsb-xMin at unitsPerEm 1000 (scale 1).
func TestPhantomPointShiftsTheOutline(t *testing.T) {
	for _, c := range []struct {
		name   string
		xMin   int
		lsb    int
		wantDX float64
	}{
		{"lsb equals xMin: no shift", 100, 100, 0},
		{"lsb 0: shifts left of the naive coordinate", 100, 0, -100},
		{"lsb 200: shifts right of the naive coordinate", 100, 200, 100},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := buildTT(1, hmtxPairs(500, c.lsb), twoPointGlyph(int16(c.xMin)))
			out, _, err := tt.Outline(0, math.MaxInt)
			if err != nil {
				t.Fatalf("Outline: %v", err)
			}
			if got := out[0].P[0].X; got != c.wantDX {
				t.Errorf("first point X = %v, want %v", got, c.wantDX)
			}
		})
	}
}

// TestPhantomPointForAnEmptyGlyph pins the exemption ttgload.c:1543-1549 draws: bbox.xMin is
// zeroed for a glyph with no bytes at all or that declares zero contours, but lsb is read exactly
// as any other glyph's is, so pp1 is -lsb, not 0, for either shape.
func TestPhantomPointForAnEmptyGlyph(t *testing.T) {
	t.Run("no glyph data at all (a zero-length loca entry)", func(t *testing.T) {
		tt := buildTT(1, hmtxPairs(500, 30), []byte{}, twoPointGlyph(999))
		out, pp1, err := tt.outline(0, 0, &ttRun{budget: math.MaxInt})
		if out != nil || err != nil {
			t.Fatalf("outline(empty) = %v, %v, want nil, nil", out, err)
		}
		if want := -30.0; pp1 != want {
			t.Errorf("pp1 = %v, want %v — xMin 0 (empty), lsb 30", pp1, want)
		}
	})
	t.Run("a simple glyph declaring zero contours ignores its own header xMin", func(t *testing.T) {
		tt := buildTT(1, hmtxPairs(500, 30), zeroContourGlyph(999))
		out, pp1, err := tt.outline(0, 0, &ttRun{budget: math.MaxInt})
		if out != nil || err != nil {
			t.Fatalf("outline(zero contours) = %v, %v, want nil, nil", out, err)
		}
		if want := -30.0; pp1 != want {
			t.Errorf("pp1 = %v, want %v — xMin forced to 0 despite the header's 999, lsb 30", pp1, want)
		}
	})
}

// TestCompositePhantomPoint pins R#0 for a composite: its own pp1, from its own header and its
// own lsb, unless a component carrying USE_MY_METRICS (0x0200) replaces it with that component's
// own pp1 — in the component's own coordinates, not moved by the offset or transform that places
// its outline (ttgload.c ~1840-1880).
func TestCompositePhantomPoint(t *testing.T) {
	// gid 0: the component. Its own xMin is 100 and its own lsb is 50, so its own pp1 is 50 — a
	// mismatch by construction, since USE_MY_METRICS is what tests whether that mismatch reaches
	// the composite at all.
	leaf := twoPointGlyph(100)
	// gid 1: the composite. Its own xMin is 100 and its own lsb (ownLsb) is 100 in most cases, so
	// its own pp1 is 0, and 0 in one, so its own pp1 is 100 — the value each case should see
	// unless USE_MY_METRICS overrides it.
	for _, c := range []struct {
		name         string
		ownLsb       int
		dx, dy       int16
		useMyMetrics bool
		wantDX       float64
	}{
		{"no USE_MY_METRICS: the composite's own pp1 (0) applies", 100, 400, 200, false, 400},
		{"no USE_MY_METRICS: the composite's own non-zero pp1 (100) applies", 0, 400, 200, false, 300},
		{"USE_MY_METRICS: the component's own pp1 (50) replaces it", 100, 400, 200, true, 350},
		{"USE_MY_METRICS at a different offset: pp1 is still 50, not offset-adjusted", 100, 0, 0, true, -50},
	} {
		t.Run(c.name, func(t *testing.T) {
			comp := oneComponentComposite(100, 0, c.dx, c.dy, c.useMyMetrics)
			tt := buildTT(2, hmtxPairs(500, 50, 500, c.ownLsb), leaf, comp)
			out, _, err := tt.Outline(1, math.MaxInt)
			if err != nil {
				t.Fatalf("Outline: %v", err)
			}
			if got := out[0].P[0].X; got != c.wantDX {
				t.Errorf("first point X = %v, want %v", got, c.wantDX)
			}
		})
	}
}

// TestCompositeLastUseMyMetricsWins pins which of two components carrying USE_MY_METRICS sets
// the composite's pp1: FreeType saves the phantom points before each component and restores them
// only after one without the flag (ttgload.c ~1840-1880), so every flagged component overwrites
// the one before and the last is left standing. gid 0 (xMin 100, lsb 0) has pp1 100 and gid 1
// (xMin 100, lsb 200) has pp1 -100; both placed at no offset, the composite is moved right by 100
// when the second wins and would be moved left by 100 if the first did.
func TestCompositeLastUseMyMetricsWins(t *testing.T) {
	comp := binary.BigEndian.AppendUint16(nil, 0xFFFF) // composite marker
	comp = binary.BigEndian.AppendUint16(comp, 100)    // its own xMin; its own lsb, below, is 100 too
	comp = append(comp, make([]byte, 6)...)
	for i, more := range []uint16{0x0020, 0} { // MORE_COMPONENTS on the first only
		comp = binary.BigEndian.AppendUint16(comp, 0x0001|0x0002|0x0200|more)
		comp = binary.BigEndian.AppendUint16(comp, uint16(i))
		comp = binary.BigEndian.AppendUint32(comp, 0) // dx, dy: 0
	}
	tt := buildTT(3, hmtxPairs(500, 0, 500, 200, 500, 100), twoPointGlyph(100), twoPointGlyph(100), comp)
	out, _, err := tt.Outline(2, math.MaxInt)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	n := 0
	for i := range out {
		for _, p := range out[i].P {
			if n++; p.X != 100 {
				t.Errorf("segment %d point X = %v, want 100 — the second component's pp1 (-100)", i, p.X)
			}
		}
	}
	if n == 0 {
		t.Fatal("Outline has no points")
	}
}

// TestPhantomPointScalesWithUnitsPerEm pins pp1's conversion from font units to 1/1000 em, which
// every other pp1 test here leaves at the identity (buildTT's unitsPerEm 1000). At 2048 a pp1 of
// 100 font units is 48.828125 thousandths, and an empty glyph's -lsb converts the same way; both
// values are exact in float64, so the comparison is too.
func TestPhantomPointScalesWithUnitsPerEm(t *testing.T) {
	t.Run("a simple glyph, xMin 100 against lsb 0", func(t *testing.T) {
		tt := buildTT(1, hmtxPairs(500, 0), twoPointGlyph(100))
		tt.unitsPerEm = 2048
		out, _, err := tt.Outline(0, math.MaxInt)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		if got, want := out[0].P[0].X, -48.828125; got != want {
			t.Errorf("first point X = %v, want %v", got, want)
		}
	})
	t.Run("an empty glyph, lsb 30", func(t *testing.T) {
		tt := buildTT(1, hmtxPairs(500, 30), []byte{}, twoPointGlyph(999))
		tt.unitsPerEm = 2048
		_, pp1, err := tt.outline(0, 0, &ttRun{budget: math.MaxInt})
		if err != nil {
			t.Fatalf("outline: %v", err)
		}
		if want := -14.6484375; pp1 != want {
			t.Errorf("pp1 = %v, want %v", pp1, want)
		}
	})
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

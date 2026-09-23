// Package ttfbuild assembles a TrueType font from nothing, for tests.
//
// It exists because every real font is someone's copyright and the corpus's are inside gitignored
// ISO documents, so a test that needs a font either redistributes one it may not, skips in a
// clone, or builds what it needs. Two packages need the same bytes — font, to check that its
// parser reads the outlines that were written here, and render/native, to check that a page set
// in this font rasterizes like the borrowed engine says — and a font builder duplicated in two
// test files is the same defect as arithmetic duplicated in two packages: a fix to one is
// invisible to the other.
//
// Not in a _test.go file, because an internal package cannot export from one. Internal so it
// cannot become part of the module's surface by accident.
//
// # On the overflow annotations below
//
// Every function here writes chosen constants into big-endian font fields, and a font's signed
// fields — a coordinate, an ascender, a side bearing — are written by reinterpreting the value's
// two's-complement bits, which is what the format specifies. A scanner reads each of those as a
// possible overflow, thirty-two times in one file. The annotations are at the function level with
// this one explanation, rather than at each site, because a suppression repeated thirty-two times
// stops being read — and because every input here is a literal in this file.
package ttfbuild

import (
	"encoding/binary"
	"math"
	"sort"
)

type Builder struct {
	UnitsPerEm uint16
	LongLoca   bool
	// NoCmap omits the cmap table, which is what a subsetted symbolic font looks like: there is
	// no character map to read it with, so a consumer's only route to a glyph is the code itself.
	NoCmap bool
	// SymbolicCmap adds a (3,0) subtable keyed by code, which is what a symbolic font carries
	// alongside or instead of the (3,1) one keyed by character.
	SymbolicCmap bool
}

// cmapSubtable is one platform-and-encoding record with its subtable bytes.
type cmapSubtable struct {
	plat, enc uint16
	data      []byte
}

// assembleCmap writes the cmap header, one record per subtable, then the subtables.
// #nosec G115 -- font fields are written by reinterpretation; see the package comment
func assembleCmap(subs []cmapSubtable) []byte {
	head := binary.BigEndian.AppendUint16(nil, 0) // version
	head = binary.BigEndian.AppendUint16(head, uint16(len(subs)))
	off := uint32(4 + 8*len(subs))
	var body []byte
	for _, s := range subs {
		head = binary.BigEndian.AppendUint16(head, s.plat)
		head = binary.BigEndian.AppendUint16(head, s.enc)
		head = binary.BigEndian.AppendUint32(head, off+uint32(len(body)))
		body = append(body, s.data...)
	}
	return append(head, body...)
}

const (
	GIDSquare    = 1
	GIDDiamond   = 2
	GIDComposite = 3
	// GIDRing is two squares, one inside the other and wound the *same* direction, which is the
	// only shape that tells the two fill rules apart. Nonzero fills the middle, because both
	// contours wind the same way and the winding number there is two; even-odd hollows it,
	// because two crossings is even. A real font winds a counter the opposite way to its outer
	// contour, where both rules agree and neither can be caught being the other — so a glyph
	// wound this way is the fixture, and same-direction nested contours do occur, in overlapping
	// composite components and in fonts built by hand.
	GIDRing = 4
	// GIDSpace is a glyph with no ink, mapped from U+0020 the way every real font maps it.
	//
	// It is here because the first version of this font stopped at 'C', and a page reading
	// "A A" was then refused for having no glyph for code 32 — correctly, by the contract that
	// a code the page draws and this backend cannot resolve stops the page. The defect was the
	// font: a real one carries a space glyph with an empty outline, so a fixture without one
	// tests a font shape no producer emits. Not glyph 0, which is notdef: a cmap entry
	// resolving to 0 means "this font has no glyph for that character", which is the one value
	// a lookup has to read as absence rather than as an answer.
	GIDSpace  = 5
	NumGlyphs = 6
)

// pt is one point of a contour, in font units.
type pt struct {
	x, y int16
	on   bool
}

// simpleGlyph writes a glyph from one or more contours.
//
// More than one because a glyph with a counter is the only shape that can tell nonzero from
// even-odd, and because the end-point array it writes — one entry per contour, each an index into
// a single flat point list — is a layout a reader gets wrong in a way one contour cannot show.
// #nosec G115 -- font fields are written by reinterpretation; see the package comment
func simpleGlyph(contours ...[]pt) []byte {
	var b []byte
	put16 := func(v uint16) { b = binary.BigEndian.AppendUint16(b, v) }

	var pts []pt
	for _, c := range contours {
		pts = append(pts, c...)
	}
	minX, minY := int16(math.MaxInt16), int16(math.MaxInt16)
	maxX, maxY := int16(math.MinInt16), int16(math.MinInt16)
	for _, p := range pts {
		minX, minY = min16(minX, p.x), min16(minY, p.y)
		maxX, maxY = max16(maxX, p.x), max16(maxY, p.y)
	}
	put16(uint16(len(contours)))
	put16(uint16(minX))
	put16(uint16(minY))
	put16(uint16(maxX))
	put16(uint16(maxY))
	// endPtsOfContours: the *last point index* of each contour, cumulative over the flat list.
	end := -1
	for _, c := range contours {
		end += len(c)
		put16(uint16(end))
	}
	put16(0) // no instructions

	// Flags: bit 0 is on-curve. No repeats and no short forms, so the deltas below are all
	// 16-bit — longer bytes, shorter test.
	for _, p := range pts {
		var f byte
		if p.on {
			f |= 0x01
		}
		b = append(b, f)
	}
	prev := int16(0)
	for _, p := range pts {
		put16(uint16(p.x - prev))
		prev = p.x
	}
	prev = 0
	for _, p := range pts {
		put16(uint16(p.y - prev))
		prev = p.y
	}
	return b
}

func min16(a, b int16) int16 {
	if a < b {
		return a
	}
	return b
}

func max16(a, b int16) int16 {
	if a > b {
		return a
	}
	return b
}

// compositeGlyph places two copies of gid, one offset and one scaled by half.
// #nosec G115 -- font fields are written by reinterpretation; see the package comment
func compositeGlyph(gid uint16) []byte {
	var b []byte
	put16 := func(v uint16) { b = binary.BigEndian.AppendUint16(b, v) }
	put16(0xFFFF) // negative contour count marks a composite
	put16(0)
	put16(0)
	put16(1000)
	put16(1000)

	// ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES | MORE_COMPONENTS
	put16(0x0001 | 0x0002 | 0x0020)
	put16(gid)
	put16(uint16(int16(0)))
	put16(uint16(int16(0)))

	// ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES | WE_HAVE_A_SCALE
	put16(0x0001 | 0x0002 | 0x0008)
	put16(gid)
	put16(uint16(int16(400))) // dx
	put16(uint16(int16(200))) // dy
	put16(0x2000)             // 0.5 in F2Dot14
	return b
}

// build assembles the font: head, maxp, loca, glyf, cmap.
// #nosec G115 -- font fields are written by reinterpretation; see the package comment
func (tb Builder) Build() []byte {
	square := simpleGlyph([]pt{
		{100, 100, true}, {400, 100, true}, {400, 400, true}, {100, 400, true},
	})
	// Four off-curve points: every segment is a quadratic through an implied midpoint.
	diamond := simpleGlyph([]pt{
		{500, 100, false}, {900, 500, false}, {500, 900, false}, {100, 500, false},
	})
	// Two squares wound the same way, for the reason GIDRing gives.
	ring := simpleGlyph(
		[]pt{{100, 100, true}, {400, 100, true}, {400, 400, true}, {100, 400, true}},
		[]pt{{200, 200, true}, {300, 200, true}, {300, 300, true}, {200, 300, true}},
	)
	comp := compositeGlyph(GIDSquare)

	glyphs := [][]byte{{}, square, diamond, comp, ring, {}}
	var glyf []byte
	offs := make([]uint32, 0, len(glyphs)+1)
	for _, g := range glyphs {
		offs = append(offs, uint32(len(glyf)))
		glyf = append(glyf, g...)
		for len(glyf)%4 != 0 { // glyph data is long-aligned
			glyf = append(glyf, 0)
		}
	}
	offs = append(offs, uint32(len(glyf)))

	var loca []byte
	for _, o := range offs {
		if tb.LongLoca {
			loca = binary.BigEndian.AppendUint32(loca, o)
		} else {
			loca = binary.BigEndian.AppendUint16(loca, uint16(o/2))
		}
	}

	head := make([]byte, 54)
	binary.BigEndian.PutUint32(head[0:], 0x00010000)  // version
	binary.BigEndian.PutUint32(head[12:], 0x5F0F3CF5) // magicNumber, which FreeType checks
	binary.BigEndian.PutUint16(head[16:], 3)          // flags
	binary.BigEndian.PutUint16(head[18:], tb.UnitsPerEm)
	binary.BigEndian.PutUint16(head[44:], 2) // fontDirectionHint
	binary.BigEndian.PutUint16(head[52:], 0) // glyphDataFormat
	if tb.LongLoca {
		binary.BigEndian.PutUint16(head[50:], 1)
	}

	// maxp version 1.0, which is what a glyf-based font must declare: the half-sized 0.5
	// version is for CFF outlines and FreeType rejects the pair.
	maxp := make([]byte, 32)
	binary.BigEndian.PutUint32(maxp[0:], 0x00010000)
	binary.BigEndian.PutUint16(maxp[4:], NumGlyphs)
	binary.BigEndian.PutUint16(maxp[6:], 20) // maxPoints
	binary.BigEndian.PutUint16(maxp[8:], 4)  // maxContours
	binary.BigEndian.PutUint16(maxp[14:], 2) // maxComponentElements
	binary.BigEndian.PutUint16(maxp[16:], 2) // maxComponentDepth

	// hhea and hmtx, which this package's own reader does not need and FreeType will not open a
	// font without. That asymmetry is the reason they are here: pdfium reads this font through
	// FreeType, and a font FreeType rejects is silently *substituted* — so without these tables
	// the comparison against pdfium is a comparison against whatever face the machine happens to
	// have, which looks like a disagreement about rasterization and is a disagreement about which
	// font is being drawn.
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint32(hhea[0:], 0x00010000)
	binary.BigEndian.PutUint16(hhea[4:], uint16(int16(int(tb.UnitsPerEm)*4/5))) // ascender
	binary.BigEndian.PutUint16(hhea[6:], uint16(int16(-int(tb.UnitsPerEm)/5)))  // descender
	binary.BigEndian.PutUint16(hhea[10:], uint16(int16(tb.UnitsPerEm)))         // advanceWidthMax
	binary.BigEndian.PutUint16(hhea[34:], NumGlyphs)                            // numberOfHMetrics

	// The left side bearing must equal each glyph's own xMin. FreeType shifts an outline to
	// agree with the declared bearing when it hints, so a font that declares zero for a glyph
	// starting at x=100 renders 100 units to the left of where its own outline says — which
	// looked exactly like a rasterizer placing glyphs wrongly, in the one comparison built to
	// find that.
	lsb := [NumGlyphs]int{0, 100, 100, 100, 100, 0}
	var hmtx []byte
	for i := 0; i < NumGlyphs; i++ {
		adv := uint16(int(tb.UnitsPerEm) / 2)
		if i >= 2 && i != GIDSpace {
			adv = uint16(int(tb.UnitsPerEm) * 3 / 5)
		}
		hmtx = binary.BigEndian.AppendUint16(hmtx, adv)
		hmtx = binary.BigEndian.AppendUint16(hmtx, uint16(int16(lsb[i]*int(tb.UnitsPerEm)/1000)))
	}

	// A minimal name table and a post table. FreeType tolerates their absence for glyph access
	// but a PDF consumer may look for a PostScript name, and a font with neither is the kind of
	// thing a reader declines for reasons that have nothing to do with its outlines.
	post := make([]byte, 32)
	binary.BigEndian.PutUint32(post[0:], 0x00030000) // version 3.0: no glyph names

	os2 := make([]byte, 96)
	binary.BigEndian.PutUint16(os2[0:], 4)                                     // version
	binary.BigEndian.PutUint16(os2[2:], uint16(int(tb.UnitsPerEm)/2))          // xAvgCharWidth
	binary.BigEndian.PutUint16(os2[4:], 400)                                   // usWeightClass
	binary.BigEndian.PutUint16(os2[6:], 5)                                     // usWidthClass
	binary.BigEndian.PutUint16(os2[64:], uint16(int(tb.UnitsPerEm)*4/5))       // sTypoAscender
	binary.BigEndian.PutUint16(os2[66:], uint16(int16(-int(tb.UnitsPerEm)/5))) // sTypoDescender

	// A format 4 cmap mapping the space to its empty glyph and 'A' through 'D' to the square,
	// diamond, composite and ring, plus the required terminating segment at 0xFFFF. Segments
	// ascend by end code, which is what makes format 4 binary-searchable.
	subs := []cmapSubtable{{plat: 3, enc: 1, data: format4([]struct {
		lo, hi, gid uint16
	}{
		{' ', ' ', GIDSpace},
		{'A', 'D', GIDSquare},
		{0xFFFF, 0xFFFF, 0},
	})}}
	if tb.SymbolicCmap {
		// A (3,0) subtable keyed by code rather than by character, which is what a symbolic font
		// carries. It maps code 1 to the *square*, so a consumer that reads this table where it
		// should have gone through /CIDToGIDMap draws a glyph the map does not name.
		sym := binary.BigEndian.AppendUint16(nil, 6) // format 6, a contiguous range
		sym = binary.BigEndian.AppendUint16(sym, 12) // length
		sym = binary.BigEndian.AppendUint16(sym, 0)  // language
		sym = binary.BigEndian.AppendUint16(sym, 1)  // firstCode
		sym = binary.BigEndian.AppendUint16(sym, 1)  // entryCount
		sym = binary.BigEndian.AppendUint16(sym, GIDSquare)
		subs = append(subs, cmapSubtable{plat: 3, enc: 0, data: sym})
	}
	cmap := assembleCmap(subs)

	tables := map[string][]byte{
		"head": head, "hhea": hhea, "hmtx": hmtx, "maxp": maxp,
		"loca": loca, "glyf": glyf, "cmap": cmap, "post": post, "OS/2": os2,
	}
	if tb.NoCmap {
		delete(tables, "cmap")
	}
	return AssembleTables(tables)
}

// format4 writes a cmap subtable with idDelta segments, which is the common real-world shape.
// #nosec G115 -- font fields are written by reinterpretation; see the package comment
func format4(segs []struct{ lo, hi, gid uint16 }) []byte {
	n := len(segs)
	var b []byte
	put := func(v uint16) { b = binary.BigEndian.AppendUint16(b, v) }
	put(4)
	put(uint16(16 + 8*n)) // length
	put(0)                // language
	put(uint16(2 * n))    // segCountX2
	put(2)                // searchRange, unused by this reader
	put(0)
	put(0)
	for _, s := range segs {
		put(s.hi)
	}
	put(0) // reservedPad
	for _, s := range segs {
		put(s.lo)
	}
	for _, s := range segs {
		// idDelta, applied modulo 65536: gid = code + delta.
		put(uint16(int32(s.gid) - int32(s.lo)))
	}
	for range segs {
		put(0) // idRangeOffset zero means use idDelta
	}
	return b
}

// assembleTables writes a table directory and the tables, four-byte aligned.
// #nosec G115 -- font fields are written by reinterpretation; see the package comment
func AssembleTables(tables map[string][]byte) []byte {
	// The directory must be sorted by tag, and 'OS/2' sorts before 'cmap' because 'O' is 0x4F
	// and 'c' is 0x63 — a detail worth writing down because a reader that binary-searches the
	// directory finds nothing in a font that gets it wrong.
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	n := len(names)
	head := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(head[0:], 0x00010000)
	binary.BigEndian.PutUint16(head[4:], uint16(n))

	body := []byte{}
	off := len(head)
	for i, name := range names {
		t := tables[name]
		rec := 12 + i*16
		copy(head[rec:], name)
		binary.BigEndian.PutUint32(head[rec+8:], uint32(off+len(body)))
		binary.BigEndian.PutUint32(head[rec+12:], uint32(len(t)))
		body = append(body, t...)
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
	}
	return append(head, body...)
}

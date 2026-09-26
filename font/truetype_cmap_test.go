package font

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/ttfbuild"
)

// This file pins parseCmap and lookup against tt_face_build_cmaps and its four validators
// (ttcmap.c, FreeType 2.13.3, vendored at C:/tmp/cfffix/ttcmap-2.13.3.c), the finding sources for
// C22 and C24. Every fixture below builds a whole TrueType program: parseCmap runs from
// ParseTrueType, not from a bare subtable, so a record's offset and a duplicate key are exactly
// as reachable here as they are from a real font.
//
// cmapProgram carries the minimum ParseTrueType needs to accept a program at all — a `head` with
// a unitsPerEm, an hhea and hmtx (round 4: a face FreeType would fail to open is refused before
// its cmap is ever read), a `maxp` with a glyph count, and a non-empty `glyf`/`loca` pair — so
// every test here is about the cmap table alone. numGlyphs is a parameter because the C24 fix
// reads it: a format 4 lookup that resolves past what `maxp` declares is refused the same way a
// wrapped one is.
func cmapProgram(numGlyphs uint16, cmap []byte) []byte {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000) // unitsPerEm
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:], numGlyphs)
	return assembleTables(map[string][]byte{
		"head": head, "hhea": make([]byte, 36), "hmtx": make([]byte, 4), "maxp": maxp,
		"loca": make([]byte, 4), "glyf": []byte{0, 0, 0, 0},
		"cmap": cmap,
	})
}

func parseCmapProgram(t *testing.T, numGlyphs uint16, cmap []byte) *TrueType {
	t.Helper()
	tt, err := ParseTrueType(cmapProgram(numGlyphs, cmap))
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	return tt
}

// cmRecord is one 'cmap' table record. off is the offset ttcmap.c:3821 reads directly, so it can
// be set to something other than where sub actually lives — which is the only way to build F2, a
// record naming offset 0. -1 places sub right after the record area, in declaration order.
type cmRecord struct {
	plat, enc uint16
	sub       []byte
	off       int
}

// buildCmap writes a 'cmap' table's version, record count, records and subtable bodies, in that
// order — the layout tt_face_build_cmaps reads a record's offset against.
func buildCmap(version uint16, recs ...cmRecord) []byte {
	head := binary.BigEndian.AppendUint16(nil, version)
	head = binary.BigEndian.AppendUint16(head, uint16(len(recs)))
	base := 4 + 8*len(recs)
	var body []byte
	for _, r := range recs {
		off := base + len(body)
		if r.off >= 0 {
			off = r.off
		} else {
			body = append(body, r.sub...)
		}
		head = binary.BigEndian.AppendUint16(head, r.plat)
		head = binary.BigEndian.AppendUint16(head, r.enc)
		head = binary.BigEndian.AppendUint32(head, uint32(off))
	}
	return append(head, body...)
}

// f0 writes a format 0 subtable with an explicit length field, which the accepted-boundary case
// needs set independently of the 256-byte array that always follows it.
func f0(length uint16, glyphs map[byte]uint16) []byte {
	b := binary.BigEndian.AppendUint16(nil, 0)
	b = binary.BigEndian.AppendUint16(b, length)
	b = binary.BigEndian.AppendUint16(b, 0) // language
	b = append(b, make([]byte, 256)...)
	for code, gid := range glyphs {
		b[6+int(code)] = byte(gid)
	}
	return b
}

// seg is one format 4 segment, written exactly as given: f4 does not sort or terminate for the
// caller, because the malformed cases here are the point.
type seg struct{ start, end, delta, ro uint16 }

// dl is the idDelta that sends code to gid, computed at runtime so the wraparound a code above
// its gid needs is modular arithmetic rather than a constant overflow the compiler rejects.
func dl(code, gid uint16) uint16 { return gid - code }

func f4(segs []seg, glyphIDs []uint16) []byte {
	n := len(segs)
	var b []byte
	put := func(v uint16) { b = binary.BigEndian.AppendUint16(b, v) }
	put(4)
	put(uint16(16 + 8*n + 2*len(glyphIDs))) // length: informational only at FT_VALIDATE_DEFAULT
	put(0)                                  // language
	put(uint16(2 * n))                      // segCountX2
	put(0)                                  // searchRange
	put(0)                                  // entrySelector
	put(0)                                  // rangeShift
	for _, s := range segs {
		put(s.end)
	}
	put(0) // reservedPad
	for _, s := range segs {
		put(s.start)
	}
	for _, s := range segs {
		put(s.delta)
	}
	for _, s := range segs {
		put(s.ro)
	}
	for _, g := range glyphIDs {
		put(g)
	}
	return b
}

// f4Map writes a well-formed format 4 subtable, sorted and terminated, mapping each code to gid
// by idDelta alone — the shape every real producer writes and every "good" fixture below needs.
func f4Map(m map[uint16]uint16) []byte {
	codes := make([]uint16, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	segs := make([]seg, 0, len(codes)+1)
	for _, c := range codes {
		segs = append(segs, seg{c, c, m[c] - c, 0})
	}
	segs = append(segs, seg{0xFFFF, 0xFFFF, 1, 0})
	return f4(segs, nil)
}

func f6(first uint16, glyphs []uint16) []byte {
	b := binary.BigEndian.AppendUint16(nil, 6)
	b = binary.BigEndian.AppendUint16(b, uint16(10+2*len(glyphs))) // length
	b = binary.BigEndian.AppendUint16(b, 0)                        // language
	b = binary.BigEndian.AppendUint16(b, first)
	b = binary.BigEndian.AppendUint16(b, uint16(len(glyphs)))
	for _, g := range glyphs {
		b = binary.BigEndian.AppendUint16(b, g)
	}
	return b
}

func f12(groups [][3]uint32) []byte {
	b := binary.BigEndian.AppendUint16(nil, 12)
	b = binary.BigEndian.AppendUint16(b, 0) // reserved
	b = binary.BigEndian.AppendUint32(b, uint32(16+12*len(groups)))
	b = binary.BigEndian.AppendUint32(b, 0) // language
	b = binary.BigEndian.AppendUint32(b, uint32(len(groups)))
	for _, g := range groups {
		b = binary.BigEndian.AppendUint32(b, g[0])
		b = binary.BigEndian.AppendUint32(b, g[1])
		b = binary.BigEndian.AppendUint32(b, g[2])
	}
	return b
}

// TestParseCmapDropsWhatFreeTypeDrops pins C22's per-record rules of tt_face_build_cmaps: a
// record FreeType never reads at all (a zero offset, or a format with no class), and a subtable
// FreeType reads and then discards for failing its validator at FT_VALIDATE_DEFAULT. Each case
// pairs the broken record with a good one, the way the finding's fixtures do, so the visible
// effect is which glyph a lookup reaches rather than a count of surviving subtables.
func TestParseCmapDropsWhatFreeTypeDrops(t *testing.T) {
	good31 := f4Map(map[uint16]uint16{'A': 21})
	good10 := f4Map(map[uint16]uint16{'A': 22})

	for _, c := range []struct {
		name string
		cmap []byte
		want uint16 // GIDForRune('A'); 0 means "no glyph"
	}{
		{
			// F2: the first (3,1) record names offset 0, which ttcmap.c:3821 skips before ever
			// reading a format there — the cmap header itself would be read as data otherwise.
			"F2 offset zero is skipped, not read as format 0",
			buildCmap(0,
				cmRecord{plat: 3, enc: 1, sub: f0(262, map[byte]uint16{'A': 9}), off: 0},
				cmRecord{plat: 3, enc: 1, sub: good31, off: -1},
			),
			21,
		},
		{
			// F3a: a segment whose start is after its end is invalid at every validation level
			// (ttcmap.c:1012-1013), so the whole (3,1) subtable is dropped and (1,0) is read
			// instead, exactly as GIDForRune's own fallback order already provides for.
			// The 'A' segment after the bad one is what makes this a regression test rather
			// than a coincidence: unfixed, lookup's own scan skips the bad segment (its end is
			// below 'A') and matches the good one anyway, drawing square. Fixed, the bad
			// segment drops the whole subtable before that segment is ever reached.
			"F3a start after end drops the subtable, not just the segment",
			buildCmap(0,
				cmRecord{plat: 3, enc: 1, sub: f4([]seg{
					{0x30, 0x20, 0, 0},
					{'A', 'A', dl('A', 9), 0},
					{0xFFFF, 0xFFFF, 1, 0},
				}, nil), off: -1},
				cmRecord{plat: 1, enc: 0, sub: good10, off: -1},
			),
			22,
		},
		{
			// F3b: the first (3,1) is format 0 with a length field of 0, which fails length>=262
			// regardless of how many bytes physically follow it; the second (3,1) is good, and
			// wins because the first was never kept to begin with.
			"F3b a length field of 0 drops the subtable",
			buildCmap(0,
				cmRecord{plat: 3, enc: 1, sub: f0(0, map[byte]uint16{'A': 9}), off: -1},
				cmRecord{plat: 3, enc: 1, sub: good31, off: -1},
			),
			21,
		},
		{
			// F3d: idRangeOffset 1 from its own slot misaligns into the offset array itself,
			// short of glyphIdArray, which ttcmap.c:1059-1061 rejects at FT_VALIDATE_DEFAULT for
			// every segment but the final {0xFFFF, 0xFFFF} sentinel this one is not. Unfixed,
			// lookup's own bound check passes (the misaligned read still lands inside the
			// subtable) and returns a glyph read out of the terminator's own idRangeOffset
			// bytes; fixed, the subtable is dropped before that read happens.
			"F3d idRangeOffset below glyphIdArray drops the subtable",
			buildCmap(0,
				cmRecord{plat: 3, enc: 1, sub: f4([]seg{{'A', 'A', 0, 1}, {0xFFFF, 0xFFFF, 1, 0}}, nil), off: -1},
				cmRecord{plat: 1, enc: 0, sub: good10, off: -1},
			),
			22,
		},
		{
			// A record naming a format with no entry in tt_cmap_classes (ttcmap.c:3767) is
			// skipped the same way an invalid one is (ttcmap.c:3875-3879), leaving the fallback.
			"an unknown format is skipped like an invalid one",
			buildCmap(0,
				cmRecord{plat: 3, enc: 1, sub: binary.BigEndian.AppendUint16(nil, 99), off: -1},
				cmRecord{plat: 1, enc: 0, sub: good10, off: -1},
			),
			22,
		},
		{
			// The 'cmap' table's own version is read past without being checked
			// (ttcmap.c:3794-3803): a nonstandard version does not stop a good subtable from
			// being read.
			"a nonzero table version does not stop a good subtable being read",
			buildCmap(7, cmRecord{plat: 3, enc: 1, sub: good31, off: -1}),
			21,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := parseCmapProgram(t, 1000, c.cmap)
			got, ok := tt.GIDForRune('A')
			if want := c.want; (want != 0) != ok || got != want {
				t.Errorf("GIDForRune('A') = %d, %v; want %d, %v", got, ok, want, want != 0)
			}
		})
	}
}

// TestParseCmapDropsAloneLeavesNoCmap pins F3a' and F3c's shape at this layer: a font whose only
// subtable fails validation has none that survive at all. For a font whose `post` table names no
// glyphs, that is what sends pdfium to a route with no charmap (MacRoman, or gid = code for a
// symbolic font) rather than to a wrong glyph; when `post` does name glyphs, FreeType synthesizes
// a Unicode charmap from those names instead (sfobjs.c:1206-1230) and pdfium reads the code
// through it, which render/native's ttRoute refuses rather than models — see PostNamesGlyphs and
// the pins in render/native/cmap_test.go.
func TestParseCmapDropsAloneLeavesNoCmap(t *testing.T) {
	bad := f4([]seg{{0x30, 0x20, 0, 0}, {0xFFFF, 0xFFFF, 1, 0}}, nil)
	tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{plat: 3, enc: 1, sub: bad, off: -1}))
	if tt.HasCmap() {
		t.Error("HasCmap() = true for a program whose only subtable failed validation")
	}
	if tt.HasSubtable(3, 1) {
		t.Error("HasSubtable(3, 1) = true for a dropped subtable")
	}
	if _, ok := tt.GIDForRune('0'); ok {
		t.Error("GIDForRune found a glyph through a subtable that should not have survived")
	}
}

// withU16 and withU32 patch one field of a subtable's bytes without changing its physical size —
// the only way to set a declared length or count independently of what the bytes around it hold,
// which every case below needs that isn't just "shorter than its own minimum".
func withU16(sub []byte, at int, v uint16) []byte {
	out := append([]byte(nil), sub...)
	binary.BigEndian.PutUint16(out[at:], v)
	return out
}

func withU32(sub []byte, at int, v uint32) []byte {
	out := append([]byte(nil), sub...)
	binary.BigEndian.PutUint32(out[at:], v)
	return out
}

// TestParseCmapAcceptsAtTheValidatedBoundary pins each of formats 0, 6 and 12's own length or
// count bounds, at FT_VALIDATE_DEFAULT. Format 0's is the finding's own example: 262 kept, 261
// dropped, and — its other bound, on the same field — 263 against a table that only has 262
// dropped too, as format 12's 29 against 28 is. Format 6 and 12 each have two independent bounds of their own: a declared length
// no shorter than their count or numGroups implies (with the table physically large enough to
// have satisfied it), and a declared length no longer than the table actually is (with the count
// or numGroups left alone and the table itself cut short instead). Format 12 additionally must
// run in strictly increasing, non-overlapping order at every level, which is pinned separately
// below with both a passing and a backward-ordered pair of groups.
func TestParseCmapAcceptsAtTheValidatedBoundary(t *testing.T) {
	for _, c := range []struct {
		name string
		cmap []byte
		kept bool
	}{
		{"format 0, length 262 is kept", buildCmap(0, cmRecord{3, 1, f0(262, map[byte]uint16{'A': 9}), -1}), true},
		{"format 0, length 261 is dropped", buildCmap(0, cmRecord{3, 1, f0(261, map[byte]uint16{'A': 9}), -1}), false},
		{"format 0, length 263 against a 262-byte table is dropped", buildCmap(0, cmRecord{3, 1, f0(263, map[byte]uint16{'A': 9}), -1}), false},
		{"format 6, length matching its count is kept", buildCmap(0, cmRecord{3, 1, f6('A', []uint16{9, 10}), -1}), true},
		{"format 6, a truncated length is dropped", buildCmap(0, cmRecord{3, 1, f6('A', []uint16{9, 10})[:11], -1}), false},
		// The table has room for both entries; only the declared length undershoots what its own
		// count needs, which is the length >= 10+count*2 bound the truncated-table case above
		// does not reach.
		{"format 6, a declared length shorter than its own count is dropped",
			buildCmap(0, cmRecord{3, 1, withU16(f6('A', []uint16{9, 10}), 2, 13), -1}), false},
		{"format 12, one well-formed group is kept", buildCmap(0, cmRecord{3, 1, f12([][3]uint32{{'A', 'C', 9}}), -1}), true},
		{"format 12, a truncated length is dropped", buildCmap(0, cmRecord{3, 1, f12([][3]uint32{{'A', 'C', 9}})[:16], -1}), false},
		{"format 12, length 29 against a 28-byte table is dropped",
			buildCmap(0, cmRecord{3, 1, withU32(f12([][3]uint32{{'A', 'C', 9}}), 4, 29), -1}), false},
		// Both groups are physically present and properly ordered; only the declared length is
		// patched down to claim room for one, which (length-16)/12 < numGroups catches on its
		// own — nothing else here bounds a read against the table's *declared* length rather
		// than its physical size or the groups' own ordering.
		{"format 12, a declared length shorter than numGroups needs is dropped",
			buildCmap(0, cmRecord{3, 1,
				withU32(f12([][3]uint32{{'A', 'C', 9}, {'D', 'F', 11}}), 4, 28), -1}), false},
		{"format 12, two groups in strictly increasing order are kept",
			buildCmap(0, cmRecord{3, 1, f12([][3]uint32{{'A', 'B', 9}, {'C', 'D', 11}}), -1}), true},
		{"format 12, a group starting at or before the previous one's end is dropped",
			buildCmap(0, cmRecord{3, 1, f12([][3]uint32{{'A', 'C', 9}, {'B', 'D', 11}}), -1}), false},
		{"format 12, a group whose start is after its own end is dropped",
			buildCmap(0, cmRecord{3, 1, f12([][3]uint32{{'C', 'A', 9}}), -1}), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := parseCmapProgram(t, 1000, c.cmap)
			if got := tt.HasSubtable(3, 1); got != c.kept {
				t.Errorf("HasSubtable(3, 1) = %v, want %v", got, c.kept)
			}
		})
	}
}

// TestParseCmapKeepsUnreadableFormatsUnvalidated pins the bullet's third case: a format lookup
// never reads (2, 8, 10, 13, 14) is kept the way FreeType keeps it, garbage contents and all,
// rather than validated and dropped — because lookup answers every one of them "no glyph for
// code" regardless, a page routed through one is refused the same way whether or not it
// survived.
func TestParseCmapKeepsUnreadableFormatsUnvalidated(t *testing.T) {
	for _, format := range []uint16{2, 8, 10, 13, 14} {
		sub := binary.BigEndian.AppendUint16(nil, format)
		sub = append(sub, 0xFF, 0xFF) // garbage that would fail any real validator
		tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{3, 1, sub, -1}))
		if !tt.HasSubtable(3, 1) {
			t.Errorf("format %d: HasSubtable(3, 1) = false, want true (FreeType keeps this class)", format)
		}
		if _, ok := tt.GIDInSubtable(3, 1, 'A'); ok {
			t.Errorf("format %d: GIDInSubtable found a glyph lookup cannot read this format to find", format)
		}
	}
}

// TestFormat4UnsortedSubtableResolvesLikeFreeType pins R#1: tt_cmap4_validate flags a format 4
// subtable TT_CMAP_FLAG_UNSORTED when a segment's start or end goes backward from the one before
// it (ttcmap.c:1019-1033), but FreeType still keeps the charmap. tt_cmap4_char_map_linear
// (ttcmap.c:1131-1221) then walks its segments in exactly the order they are declared — stop once
// a code is below a segment's start, match once it is at or below its end — which is the same
// walk lookup's forward scan already does, once a segment's own start <= end is validated. So a
// backward subtable is read the way FreeType reads it, including a code a later segment names but
// an earlier segment's start has already ruled out: that is FreeType's own answer, not a case
// this package used to have to refuse for lack of a way to tell its scan from FreeType's.
func TestFormat4UnsortedSubtableResolvesLikeFreeType(t *testing.T) {
	// Segment order 'A'-'A' then '0'-'0': the second segment's start goes backward from the
	// first, which is exactly the shape tt_cmap4_validate flags rather than rejects.
	backward := f4([]seg{
		{'A', 'A', dl('A', 9), 0},
		{'0', '0', dl('0', 11), 0},
		{0xFFFF, 0xFFFF, 1, 0},
	}, nil)
	tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{3, 1, backward, -1}))
	if !tt.HasSubtable(3, 1) {
		t.Fatal("HasSubtable(3, 1) = false; an unsorted subtable is flagged, not dropped")
	}
	if got, ok := tt.GIDInSubtable(3, 1, 'A'); !ok || got != 9 {
		t.Errorf("GIDInSubtable('A') = %d, %v; want 9, true (the first segment's own range)", got, ok)
	}
	// '0' sits inside the second segment, but the first segment's start ('A') is already past it,
	// and FreeType's own linear scan stops there without ever reaching the second segment.
	if _, ok := tt.GIDInSubtable(3, 1, '0'); ok {
		t.Error("GIDInSubtable('0') found a glyph FreeType's declared-order scan never reaches")
	}
}

// TestFormat4RangeOffsetPastMaxGlyphsRefuses pins C24 and the divergence next to it: on the
// idRangeOffset path, tt_cmap4_char_index zeroes the glyph it just computed when idDelta wraps
// the sum back to 0, or when the sum is not a glyph `maxp` declares at all (ttcmap.c:1181-1186).
// Both are FreeType's own "not found", read here as a refusal rather than glyph 0 or a glyph the
// font never sized outlines for.
func TestFormat4RangeOffsetPastMaxGlyphsRefuses(t *testing.T) {
	// One segment, 'A' only, with idRangeOffset routed through glyphIdArray to entry 1: gid=1,
	// wrap := gid+idDelta. The wrap and bound cases share the fixture's shape and differ only in
	// idDelta and numGlyphs, so both are table-driven off the same segment layout.
	build := func(idDelta uint16, numGlyphs uint16) []byte {
		sub := f4([]seg{{'A', 'A', idDelta, 4}, {0xFFFF, 0xFFFF, 1, 0}}, []uint16{1})
		return buildCmap(0, cmRecord{3, 1, sub, -1})
	}
	for _, c := range []struct {
		name      string
		idDelta   uint16
		numGlyphs uint16
		wantGID   uint16
		wantOK    bool
	}{
		// idDelta 0xFFFF: gid = (1 + 0xFFFF) & 0xFFFF = 0, FreeType's own "not found".
		{"idDelta wraps the sum to 0", 0xFFFF, 100, 0, false},
		// idDelta 0: gid = 1, comfortably inside 100 glyphs, and the control that the fixture
		// itself is read correctly rather than always refusing.
		{"no wrap, well inside maxp, is found", 0, 100, 1, true},
		// idDelta 0: gid = 1, but maxp declares only 1 glyph (glyph 0), so glyph 1 does not
		// exist — FreeType zeroes it exactly as it does the wrap.
		{"no wrap, but past what maxp declares, refuses", 0, 1, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := parseCmapProgram(t, c.numGlyphs, build(c.idDelta, c.numGlyphs))
			got, ok := tt.GIDInSubtable(3, 1, 'A')
			if ok != c.wantOK || (ok && got != c.wantGID) {
				t.Errorf("GIDInSubtable('A') = %d, %v; want %d, %v", got, ok, c.wantGID, c.wantOK)
			}
		})
	}
}

// f4Raw writes a format 4 subtable exactly as given, including a segCountX2 the caller may leave
// odd: f4 above always writes 2*len(segs), which cannot express the shape R#0 needs.
func f4Raw(segCountX2 uint16, segs []seg, glyphIDs []uint16) []byte {
	n := len(segs)
	var b []byte
	put := func(v uint16) { b = binary.BigEndian.AppendUint16(b, v) }
	put(4)
	put(uint16(16 + 8*n + 2*len(glyphIDs))) // length
	put(0)                                  // language
	put(segCountX2)
	put(0) // searchRange
	put(0) // entrySelector
	put(0) // rangeShift
	for _, s := range segs {
		put(s.end)
	}
	put(0) // reservedPad
	for _, s := range segs {
		put(s.start)
	}
	for _, s := range segs {
		put(s.delta)
	}
	for _, s := range segs {
		put(s.ro)
	}
	for _, g := range glyphIDs {
		put(g)
	}
	return b
}

// TestFormat4OddSegCountX2IsHalvedLikeFreeType pins R#0: tt_cmap4_validate halves segCountX2
// before laying out the ends/starts/deltas/offsets arrays and bounding the table against them
// (ttcmap.c line 940, "num_segs /= 2"), and tt_cmap4_char_map_linear/binary do the same at lookup
// time ("TT_PEEK_USHORT( p ) >> 1"). Reading the raw, undivided field instead is four bytes
// stricter at validation, dropping a subtable FreeType keeps, and misreads the array layout at
// lookup, finding a glyph FreeType never does.
func TestFormat4OddSegCountX2IsHalvedLikeFreeType(t *testing.T) {
	t.Run("a: the odd record is read halved, kept, and wins as the first surviving (3,1)", func(t *testing.T) {
		// segCountX2 3 halves to one segment, [0, 0xFFFF], whose idDelta sends every code,
		// including A, to glyph 9. Undivided, the 24-byte subtable is four bytes short of the
		// wrong stricter bound, so it used to be dropped and the second, well-formed record
		// naming A to 21 would be read instead. The first surviving record for a pair wins, so
		// keeping the odd one correctly is what makes this a regression test rather than a
		// coincidence.
		odd := f4Raw(3, []seg{{0, 0xFFFF, dl('A', 9), 0}}, nil)
		good := f4Map(map[uint16]uint16{'A': 21})
		tt := parseCmapProgram(t, 1000, buildCmap(0,
			cmRecord{plat: 3, enc: 1, sub: odd, off: -1},
			cmRecord{plat: 3, enc: 1, sub: good, off: -1},
		))
		if got, ok := tt.GIDForRune('A'); !ok || got != 9 {
			t.Errorf("GIDForRune('A') = %d, %v; want 9, true", got, ok)
		}
	})
	t.Run("b: the odd subtable alone is kept, not dropped", func(t *testing.T) {
		odd := f4Raw(3, []seg{{0, 0xFFFF, dl('A', 9), 0}}, nil)
		tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{plat: 3, enc: 1, sub: odd, off: -1}))
		if !tt.HasSubtable(3, 1) {
			t.Fatal("HasSubtable(3, 1) = false; segCountX2 3 halves to one valid segment, which FreeType keeps")
		}
		if got, ok := tt.GIDForRune('A'); !ok || got != 9 {
			t.Errorf("GIDForRune('A') = %d, %v; want 9, true", got, ok)
		}
	})
	t.Run("c: segCountX2 1 halves to zero segments, kept and empty", func(t *testing.T) {
		// FreeType's own num_segs is 1 >> 1 = 0: no segment ever matches, for any code. Reading
		// the raw field as one segment instead misreads the 16 bytes right after the header,
		// which hold nothing meaningful here, as that segment's own fields.
		zero := f4Raw(1, nil, nil)
		tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{plat: 3, enc: 0, sub: zero, off: -1}))
		if !tt.HasSubtable(3, 0) {
			t.Fatal("HasSubtable(3, 0) = false; segCountX2 1 halves to zero segments, which FreeType still keeps")
		}
		if _, ok := tt.GIDInSubtable(3, 0, 2); ok {
			t.Error("GIDInSubtable found a glyph through a subtable with zero segments")
		}
	})
}

// TestNotFoundPastMaxpIsRefusedInEveryFormat pins R#3: FT_Get_Char_Index zeroes any result at or
// past what maxp declares (ftobjs.c lines 3947-3949, "if ( result >= (FT_UInt)face->num_glyphs )
// result = 0"), for every format's own char_index function, not only format 4's idRangeOffset
// path, which used to be the only one this package's own bound reached.
func TestNotFoundPastMaxpIsRefusedInEveryFormat(t *testing.T) {
	for _, c := range []struct {
		name string
		cmap []byte
	}{
		{"format 0", buildCmap(0, cmRecord{3, 1, f0(262, map[byte]uint16{'A': 50}), -1})},
		{"format 4, idDelta path", buildCmap(0, cmRecord{3, 1, f4Map(map[uint16]uint16{'A': 50}), -1})},
		{"format 6", buildCmap(0, cmRecord{3, 1, f6('A', []uint16{50}), -1})},
		{"format 12", buildCmap(0, cmRecord{3, 1, f12([][3]uint32{{'A', 'A', 50}}), -1})},
	} {
		t.Run(c.name, func(t *testing.T) {
			// maxp declares 6 glyphs, 0 through 5; every fixture above maps A to glyph 50.
			tt := parseCmapProgram(t, 6, c.cmap)
			if got, ok := tt.GIDInSubtable(3, 1, 'A'); ok {
				t.Errorf("GIDInSubtable('A') = %d, true; want a refusal, glyph 50 is past maxp's 6", got)
			}
		})
	}
}

// TestFormat4NewGuardsMatterAgainstFreeType pins R#4: three of validateCmap4's own guards that no
// other test here reaches, each one modelled from ttcmap.c and each one where getting it wrong,
// either direction, is a page pdfium draws that this backend would refuse, or the reverse.
func TestFormat4NewGuardsMatterAgainstFreeType(t *testing.T) {
	t.Run("a malformed idRangeOffset is kept only on the final sentinel segment", func(t *testing.T) {
		// ttcmap.c lines 1046-1062 omit the bound check for exactly this shape ("we thus omit
		// the test here"), because a sloppy subsetter routinely leaves the sentinel malformed.
		// ro 1000 here points nowhere near glyphIdArray or the end of the table, and would fail
		// the bound on any segment but this one.
		sub := f4([]seg{{'A', 'A', dl('A', 9), 0}, {0xFFFF, 0xFFFF, 0, 1000}}, nil)
		tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{plat: 3, enc: 1, sub: sub, off: -1}))
		if !tt.HasSubtable(3, 1) {
			t.Fatal("HasSubtable(3, 1) = false; ttcmap.c omits this bound on the final segment")
		}
		if got, ok := tt.GIDForRune('A'); !ok || got != 9 {
			t.Errorf("GIDForRune('A') = %d, %v; want 9, true", got, ok)
		}
	})
	t.Run("idRangeOffset 0xFFFF drops the subtable off the final segment", func(t *testing.T) {
		sub := f4([]seg{{'A', 'A', 0, 0xFFFF}, {0xFFFF, 0xFFFF, 1, 0}}, nil)
		tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{plat: 3, enc: 1, sub: sub, off: -1}))
		if tt.HasSubtable(3, 1) {
			t.Error("HasSubtable(3, 1) = true; idRangeOffset 0xFFFF off the final segment is invalid at every level")
		}
	})
	t.Run("idRangeOffset 0xFFFF is kept on the final sentinel segment", func(t *testing.T) {
		sub := f4([]seg{{'A', 'A', dl('A', 9), 0}, {0xFFFF, 0xFFFF, 0, 0xFFFF}}, nil)
		tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{plat: 3, enc: 1, sub: sub, off: -1}))
		if !tt.HasSubtable(3, 1) {
			t.Error("HasSubtable(3, 1) = false; idRangeOffset 0xFFFF is only rejected off the final segment")
		}
	})
	t.Run("idRangeOffset ending exactly at the table is kept, one entry short is dropped", func(t *testing.T) {
		// Two segments, so the first is not final and the general bound applies. Its ro (4)
		// points at glyphIdArray's first slot; whether that slot exists is the only difference
		// between the two subtables below.
		seg0 := seg{'A', 'A', 0, 4}
		final := seg{0xFFFF, 0xFFFF, 1, 0}
		for _, c := range []struct {
			name string
			sub  []byte
			kept bool
		}{
			{"glyphIdArray reaches exactly to the table's end", f4([]seg{seg0, final}, []uint16{7}), true},
			{"glyphIdArray one entry short of what idRangeOffset needs", f4([]seg{seg0, final}, nil), false},
		} {
			t.Run(c.name, func(t *testing.T) {
				tt := parseCmapProgram(t, 1000, buildCmap(0, cmRecord{plat: 3, enc: 1, sub: c.sub, off: -1}))
				if got := tt.HasSubtable(3, 1); got != c.kept {
					t.Errorf("HasSubtable(3, 1) = %v, want %v", got, c.kept)
				}
			})
		}
	})
}

// findTableRecord returns the offset of tag's own 16-byte record in an SFNT table directory, the
// way ParseTrueType's own table-directory loop finds it.
func findTableRecord(data []byte, tag string) int {
	n := int(binary.BigEndian.Uint16(data[4:6]))
	for i := 0; i < n; i++ {
		rec := 12 + i*16
		if string(data[rec:rec+4]) == tag {
			return rec
		}
	}
	return -1
}

// TestParseTrueTypeTableDirectoryDoesNotOverflowOn386 pins R#5: off and length are widened to
// int64 before any comparison, so a field at or past 1<<31 is bounded the same way on a 32-bit
// build (GOARCH=386, where plain `int` is 32 bits and int(uint32) of such a field comes back
// negative) as it is on amd64, rather than wrapping and slipping the bound that would otherwise
// cut the table down to size or drop it outright. The point pinned here is that ParseTrueType
// returns rather than panics on the negative slice bound that wrapping used to let through;
// whether it then errors or clamps the table to what the file actually holds is the ordinary "too
// long is fine" reading, which is not this test's question — recover() below only turns a panic
// into a reported failure, on either architecture.
//
// Every row runs the same on amd64, where int is already 64 bits and none of these fields was
// ever the problem; only GOARCH=386 exercises the cast this pins (`GOARCH=386 go test -short
// -count=1 ./font/`, which is the merge gate's own command for this package).
func TestParseTrueTypeTableDirectoryDoesNotOverflowOn386(t *testing.T) {
	build := func() []byte {
		return assembleTables(map[string][]byte{
			"head": make([]byte, 54), "maxp": make([]byte, 6),
			"loca": make([]byte, 4), "glyf": []byte{0, 0, 0, 0},
			"hmtx": make([]byte, 4),
		})
	}
	for _, c := range []struct {
		name  string
		tag   string
		field int // the record's offset (+8) or length (+12) field
		v     uint32
	}{
		{"glyf length just under 1<<31", "glyf", 12, 0x7FFFFFF0},
		{"glyf length at 1<<31", "glyf", 12, 0x80000000},
		{"glyf length 0xFFFFFFF0", "glyf", 12, 0xFFFFFFF0},
		{"glyf offset at 1<<31", "glyf", 8, 0x80000000},
		{"glyf offset 0xFFFFFFF0", "glyf", 8, 0xFFFFFFF0},
		// hmtx is the one tag whose length is clamped down rather than the entry dropped when it
		// runs past the end of the file, so a mutated hmtx offset reaches the slice below rather
		// than being filtered out by the length check the way every glyf row above is.
		{"hmtx offset at 1<<31", "hmtx", 8, 0x80000000},
		{"hmtx offset 0xFFFFFFF0", "hmtx", 8, 0xFFFFFFF0},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParseTrueType panicked: %v", r)
				}
			}()
			data := build()
			rec := findTableRecord(data, c.tag)
			if rec < 0 {
				t.Fatal(c.tag + " record not found")
			}
			binary.BigEndian.PutUint32(data[rec+c.field:], c.v)
			_, _ = ParseTrueType(data)
		})
	}
}

// --- round 3 ---

// head54 writes a minimal `head` table declaring unitsPerEm, long enough (54 bytes, 0x36) to pass
// check_table_dir's own length rule.
func head54(unitsPerEm uint16) []byte {
	b := make([]byte, 54)
	binary.BigEndian.PutUint16(b[18:], unitsPerEm)
	return b
}

// maxp6 writes a minimal `maxp` table declaring numGlyphs, the only field this package reads.
func maxp6(numGlyphs uint16) []byte {
	b := make([]byte, 6)
	binary.BigEndian.PutUint16(b[4:], numGlyphs)
	return b
}

// minimalEntries is a face ParseTrueType's own rules accept on their own: a head, hhea, hmtx,
// maxp, loca and glyf, in that order. Each round-3 directory test below starts here and breaks
// exactly one rule; round 4 added hhea and hmtx to the set FreeType itself would fail the face
// over, so they are part of the minimum now too.
func minimalEntries() []ttfbuild.Entry {
	return []ttfbuild.Entry{
		{Tag: "head", Data: head54(1000)},
		{Tag: "hhea", Data: make([]byte, 36)},
		{Tag: "hmtx", Data: make([]byte, 4)},
		{Tag: "maxp", Data: maxp6(6)},
		{Tag: "loca", Data: make([]byte, 4)},
		{Tag: "glyf", Data: []byte{0, 0, 0, 0}},
	}
}

// recordOffset, setRecordOffset and setRecordLength read or patch one table-directory record's
// offset or length field in place — the only way to give a record a value
// ttfbuild.AssembleTablesOrdered's own bookkeeping would never write, since it always agrees the
// record it emits with the bytes actually there.
func recordOffset(data []byte, i int) int {
	return int(binary.BigEndian.Uint32(data[12+i*16+8:]))
}

func setRecordOffset(data []byte, i, off int) {
	binary.BigEndian.PutUint32(data[12+i*16+8:], uint32(off))
}

func setRecordLength(data []byte, i, length int) {
	binary.BigEndian.PutUint32(data[12+i*16+12:], uint32(length))
}

// TestNotFoundZeroNumGlyphsRefusesEveryFormat pins R#0: notFound used to exempt numGlyphs == 0
// from its own maxp bound, so a font with no maxp table, or one that declares 0 glyphs, still
// drew through a (3,1) subtable. FT_Get_Char_Index zeroes every result once face->num_glyphs is 0
// (ftobjs.c:3947-3949), and that is exactly what a missing or unreadable maxp leaves it at:
// LOAD_( maxp )'s own error is never checked (sfobjs.c:915-917, "often not present in embedded
// TrueType fonts within PDF documents").
//
// Glyph 1 is below the six tables the directory declares, so a read of numGlyphs through an absent
// maxp's zero offset — which lands on the directory's own numTables — would find it.
func TestNotFoundZeroNumGlyphsRefusesEveryFormat(t *testing.T) {
	sub := f4Map(map[uint16]uint16{'A': 1})
	cmap := buildCmap(0, cmRecord{plat: 3, enc: 1, sub: sub, off: -1})
	for _, c := range []struct {
		name string
		maxp []byte // nil omits the table entirely
	}{
		{"no maxp table at all", nil},
		{"maxp present, numGlyphs 0", maxp6(0)},
	} {
		t.Run(c.name, func(t *testing.T) {
			entries := []ttfbuild.Entry{
				{Tag: "head", Data: head54(1000)},
				{Tag: "hhea", Data: make([]byte, 36)},
				{Tag: "hmtx", Data: make([]byte, 4)},
				{Tag: "loca", Data: make([]byte, 4)},
				{Tag: "glyf", Data: []byte{0, 0, 0, 0}},
				{Tag: "cmap", Data: cmap},
			}
			if c.maxp != nil {
				entries = append(entries, ttfbuild.Entry{Tag: "maxp", Data: c.maxp})
			}
			tt, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
			if err != nil {
				t.Fatalf("ParseTrueType: %v", err)
			}
			if got, ok := tt.GIDForRune('A'); ok {
				t.Errorf("GIDForRune('A') = %d, true; want a refusal, num_glyphs is 0", got)
			}
		})
	}
}

// TestNotFoundNumGlyphsBoundary pins the exact boundary notFound checks against maxp's own
// numGlyphs: the last glyph maxp declares is found, and the one past it — which is exactly what
// FT_Get_Char_Index's `>=` zeroes — is refused.
func TestNotFoundNumGlyphsBoundary(t *testing.T) {
	for _, c := range []struct {
		name      string
		numGlyphs uint16
		gid       uint16
		want      bool
	}{
		{"gid == numGlyphs-1 is found", 6, 5, true},
		{"gid == numGlyphs is refused", 6, 6, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmap := buildCmap(0, cmRecord{3, 1, f4Map(map[uint16]uint16{'A': c.gid}), -1})
			tt := parseCmapProgram(t, c.numGlyphs, cmap)
			got, ok := tt.GIDForRune('A')
			if ok != c.want || (ok && got != c.gid) {
				t.Errorf("GIDForRune('A') = %d, %v; want %d, %v", got, ok, c.gid, c.want)
			}
		})
	}
}

// TestDirectoryOffsetPastEOFIsDropped pins R#1/R#2's first per-record directory rule: an entry
// whose offset is past the end of the file is dropped outright (ttload.c:437-438, "if (
// entry.Offset > stream->size ) continue"), so a `post` table that would otherwise name glyphs is
// read as absent once its own offset is pushed past the end of the file.
func TestDirectoryOffsetPastEOFIsDropped(t *testing.T) {
	post20 := make([]byte, 32)
	binary.BigEndian.PutUint32(post20, 0x00020000) // format 2.0: names glyphs, if it survives
	entries := append(minimalEntries(), ttfbuild.Entry{Tag: "post", Data: post20})
	data := ttfbuild.AssembleTablesOrdered(entries)
	setRecordOffset(data, len(entries)-1, len(data)+10)
	tt, err := ParseTrueType(data)
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	if tt.PostNamesGlyphs() {
		t.Error("PostNamesGlyphs() = true; an offset past the end of the file drops the whole entry")
	}
}

// TestDirectoryLengthPastEOFIsDroppedExceptMetrics pins the second per-record rule: an entry
// whose length runs past the end of the file is dropped too, unlike a length runs long — except
// hmtx and vmtx, whose clamp ParseTrueType now observes directly, elsewhere in this package: it
// refuses a program whose hmtx is clamped to zero length (TestHmtxClampToAMultipleOfFour below)
// and, in render/native, a drawn glyph's placement moves by exactly what the clamped table's lsb
// says. Post's own naming is this test's actual subject; the clamp is exercised by name, not
// here.
func TestDirectoryLengthPastEOFIsDroppedExceptMetrics(t *testing.T) {
	post20 := make([]byte, 32)
	binary.BigEndian.PutUint32(post20, 0x00020000)
	entries := append(minimalEntries(), ttfbuild.Entry{Tag: "post", Data: post20})
	data := ttfbuild.AssembleTablesOrdered(entries)
	i := len(entries) - 1
	setRecordLength(data, i, len(data)-recordOffset(data, i)+1) // one byte past the end
	tt, err := ParseTrueType(data)
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	if tt.PostNamesGlyphs() {
		t.Error("PostNamesGlyphs() = true; a declared length past the end of the file drops the whole entry")
	}
}

// TestDirectoryFirstDuplicateWinsEvenZeroLength pins the third and fourth per-record rules
// together: FreeType keeps the first entry for a tag, even a zero-length one, and a zero-length
// entry reads exactly as absent (tt_face_lookup_table, ttload.c:79-84, "zero-length tables the
// same as missing tables"). So the *order* the two `post` entries are declared in, not which one
// actually names glyphs, decides whether the font's post names glyphs at all.
func TestDirectoryFirstDuplicateWinsEvenZeroLength(t *testing.T) {
	post20 := make([]byte, 32)
	binary.BigEndian.PutUint32(post20, 0x00020000) // format 2.0: names glyphs

	for _, c := range []struct {
		name    string
		entries []ttfbuild.Entry
		names   bool
	}{
		{
			"a zero-length post first shadows a real one that follows",
			append(minimalEntries(),
				ttfbuild.Entry{Tag: "post", Data: nil},
				ttfbuild.Entry{Tag: "post", Data: post20}),
			false,
		},
		{
			"a real post first is the one kept, even with a zero-length one behind it",
			append(minimalEntries(),
				ttfbuild.Entry{Tag: "post", Data: post20},
				ttfbuild.Entry{Tag: "post", Data: nil}),
			true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(c.entries))
			if err != nil {
				t.Fatalf("ParseTrueType: %v", err)
			}
			if got := tt.PostNamesGlyphs(); got != c.names {
				t.Errorf("PostNamesGlyphs() = %v, want %v", got, c.names)
			}
		})
	}
}

// TestDirectoryDuplicateCmapTableFirstWins pins the same first-wins rule one level up: two whole
// `cmap` *tables* in the sfnt directory, not two records inside one, resolve to the first.
func TestDirectoryDuplicateCmapTableFirstWins(t *testing.T) {
	// gid 3 and 4, not 9 and 99: minimalEntries' own maxp declares 6 glyphs, and a gid past that
	// would be refused by R#0's own fix regardless of which `cmap` table won.
	first := buildCmap(0, cmRecord{plat: 3, enc: 1, sub: f4Map(map[uint16]uint16{'A': 3}), off: -1})
	second := buildCmap(0, cmRecord{plat: 3, enc: 1, sub: f4Map(map[uint16]uint16{'A': 4}), off: -1})
	entries := append(minimalEntries(),
		ttfbuild.Entry{Tag: "cmap", Data: first},
		ttfbuild.Entry{Tag: "cmap", Data: second})
	tt, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	if got, ok := tt.GIDForRune('A'); !ok || got != 3 {
		t.Errorf("GIDForRune('A') = %d, %v; want 3, true (the first `cmap` table wins)", got, ok)
	}
}

// TestParseTrueTypeFailsWhenFreeTypeWouldNotOpenTheFace pins check_table_dir's own face-level
// failures (ttload.c:251-264, "no valid tables found" and a too-short head/bhed) and sfobjs.c's
// (missing head, and unitsPerEm outside 16..16384): pdfium substitutes a different face for any
// of these, so ParseTrueType must refuse the program rather than parse it leniently.
func TestParseTrueTypeFailsWhenFreeTypeWouldNotOpenTheFace(t *testing.T) {
	wantErr := func(t *testing.T, err error, substr string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), substr) {
			t.Errorf("ParseTrueType err = %v, want it to contain %q", err, substr)
		}
	}
	t.Run("no valid entries at all fails the face", func(t *testing.T) {
		entries := minimalEntries()
		data := ttfbuild.AssembleTablesOrdered(entries)
		for i := range entries {
			setRecordOffset(data, i, len(data)+1)
		}
		_, err := ParseTrueType(data)
		wantErr(t, err, "no valid entries")
	})
	t.Run("head one byte short of 0x36 fails the face, not just the table", func(t *testing.T) {
		entries := minimalEntries()
		entries[0].Data = make([]byte, 0x35)
		_, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
		wantErr(t, err, "shorter than 0x36 bytes")
	})
	t.Run("head exactly 0x36 is the accepted boundary", func(t *testing.T) {
		entries := minimalEntries()
		entries[0].Data = make([]byte, 0x36)
		binary.BigEndian.PutUint16(entries[0].Data[18:], 1000)
		if _, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries)); err != nil {
			t.Errorf("ParseTrueType failed at the accepted head-length boundary: %v", err)
		}
	})
	t.Run("a missing head fails the face", func(t *testing.T) {
		entries := minimalEntries()[1:] // no head entry at all
		_, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
		wantErr(t, err, "has no head table")
	})
	for _, c := range []struct {
		name string
		u    uint16
		ok   bool
	}{
		{"unitsPerEm 15 is refused", 15, false},
		{"unitsPerEm 16 is the accepted boundary", 16, true},
		{"unitsPerEm 16384 is the accepted boundary", 16384, true},
		{"unitsPerEm 16385 is refused", 16385, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			entries := minimalEntries()
			entries[0].Data = head54(c.u)
			_, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
			if c.ok {
				if err != nil {
					t.Errorf("ParseTrueType failed at the accepted unitsPerEm boundary: %v", err)
				}
				return
			}
			wantErr(t, err, "outside FreeType's 16..16384")
		})
	}
}

// TestPostReadsThirtyTwoBytesFromTheFile pins R#3: tt_face_load_post reads a fixed 32-byte header
// from the *stream* at the table's own offset (ttload.c:1330-1334), not bounded by what its
// directory entry declared, so a post table declared only 20 bytes long is read exactly as if it
// had declared all 32 — as long as the file actually has 32 bytes there.
//
// The format 2.0 case documents an over-refusal, not a clean confirmation: load_post_names
// (ttpost.c:340) needs a *declared* length of at least 34 to load any names at all, so a real
// FreeType would set FT_FACE_FLAG_GLYPH_NAMES from this same 32-byte header yet load zero names
// behind it, synthesize no Unicode charmap, and read a symbolic font's code as a glyph index —
// where this model, reading the header alone, still calls it naming glyphs and refuses. See
// render/native's TestPostDeclaredUnder34NamesGlyphsButPdfiumDrawsGidEqualsCode for the pdfium
// side of that divergence.
func TestPostReadsThirtyTwoBytesFromTheFile(t *testing.T) {
	for _, c := range []struct {
		name    string
		version uint32
		names   bool
	}{
		{"format 2.0, declared 20 (over-refuses: FreeType needs a declared length of 34 to load any names)", 0x00020000, true},
		{"format 3.0 does not name glyphs", 0x00030000, false},
		{"format 4.0 names glyphs (stricter than the 2024 source, on purpose)", 0x00040000, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			post := make([]byte, 32) // 32 bytes physically present in the file ...
			binary.BigEndian.PutUint32(post, c.version)
			entries := append(minimalEntries(), ttfbuild.Entry{Tag: "post", Data: post})
			data := ttfbuild.AssembleTablesOrdered(entries)
			setRecordLength(data, len(entries)-1, 20) // ... but the directory declares only 20.
			tt, err := ParseTrueType(data)
			if err != nil {
				t.Fatalf("ParseTrueType: %v", err)
			}
			if got := tt.PostNamesGlyphs(); got != c.names {
				t.Errorf("PostNamesGlyphs() = %v, want %v", got, c.names)
			}
		})
	}
}

// TestPostFewerThanThirtyTwoBytesNamesGlyphsConservatively pins the other half of R#3: when fewer
// than 32 bytes are actually left in the file from post's offset, its FormatType cannot be
// confirmed at all — including a confirmation that it is 3.0 — so it is read as naming glyphs
// regardless of what the few bytes that are present spell out.
func TestPostFewerThanThirtyTwoBytesNamesGlyphsConservatively(t *testing.T) {
	post := make([]byte, 20)                     // fewer than 32 bytes, and it is the last table in the file
	binary.BigEndian.PutUint32(post, 0x00030000) // format 3.0 — would not name glyphs if fully readable
	entries := append(minimalEntries(), ttfbuild.Entry{Tag: "post", Data: post})
	tt, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
	if err != nil {
		t.Fatalf("ParseTrueType: %v", err)
	}
	if !tt.PostNamesGlyphs() {
		t.Error("PostNamesGlyphs() = false; fewer than 32 bytes were readable, so format 3.0 could not be confirmed")
	}
}

// TestFormat12GroupOrderingBoundary pins M8: a group starting *at* the previous one's own end,
// not just before it, is what tt_cmap12_validate's `start <= last` rejects and a loosened
// `start < last` would keep (ttcmap.c's own bound; the accepted C > B case already lives in
// TestParseCmapAcceptsAtTheValidatedBoundary, next to the wholly overlapping B < C case dropped
// there).
func TestFormat12GroupOrderingBoundary(t *testing.T) {
	cmap := buildCmap(0, cmRecord{3, 1, f12([][3]uint32{{'A', 'C', 9}, {'C', 'D', 11}}), -1})
	tt := parseCmapProgram(t, 1000, cmap)
	if tt.HasSubtable(3, 1) {
		t.Error("HasSubtable(3, 1) = true; a group starting exactly at the previous one's end is invalid")
	}
}

// TestFormat12DeclaredLengthUnder16IsDropped pins M10: tt_cmap12_validate's `length < 16` bound
// is its own check, not implied by the others — a declared length under 16 is invalid even when
// the table itself is 16 physical bytes and numGroups is 0, which the other two bounds
// (length > len(sub), and (length-16)/12 < numGroups) do not catch on their own.
func TestFormat12DeclaredLengthUnder16IsDropped(t *testing.T) {
	sub := withU32(f12(nil), 4, 12) // f12(nil) is the 16-byte header alone; its length field is
	// patched from 16 down to 12, independent of the table's own physical size.
	cmap := buildCmap(0, cmRecord{3, 1, sub, -1})
	tt := parseCmapProgram(t, 1000, cmap)
	if tt.HasSubtable(3, 1) {
		t.Error("HasSubtable(3, 1) = true; a declared length under 16 is invalid regardless of numGroups")
	}
}

// TestFormat4IdRangeOffsetOneByteBeforeGlyphIDsIsDropped pins M14a and M14b together: an
// idRangeOffset that lands one byte before glyphIdArray is invalid on a non-final segment
// (ttcmap.c:1059-1061, "p < glyph_ids"), and it is one byte short of the already-pinned accepted
// case (TestFormat4NewGuardsMatterAgainstFreeType's "ending exactly at the table") on the same
// segment shape.
func TestFormat4IdRangeOffsetOneByteBeforeGlyphIDsIsDropped(t *testing.T) {
	// Two segments (not final), idRangeOffset 3 from its own slot: with segCountX2 4, that lands
	// the computed pointer at ranges+3, exactly one byte before glyphIdArray (ranges+4).
	sub := f4([]seg{{'A', 'A', 0, 3}, {0xFFFF, 0xFFFF, 1, 0}}, []uint16{0, 0})
	cmap := buildCmap(0, cmRecord{3, 1, sub, -1})
	tt := parseCmapProgram(t, 1000, cmap)
	if tt.HasSubtable(3, 1) {
		t.Error("HasSubtable(3, 1) = true; idRangeOffset landing 1 byte before glyphIdArray is invalid")
	}
}

// TestFormat4FinalSentinelExemptionNeedsBothConjuncts pins M16 and M17: the exemption from
// idRangeOffset's bound check applies only to the segment that is both last (i == segX2-2) and
// shaped {0xFFFF, 0xFFFF} — not to either alone.
func TestFormat4FinalSentinelExemptionNeedsBothConjuncts(t *testing.T) {
	t.Run("shaped like the sentinel but not last is not exempt (kills M17)", func(t *testing.T) {
		// {0xFFFF, 0xFFFF} first, then a real segment: the first is not the final segment, so its
		// wildly invalid idRangeOffset (1000) must still be rejected.
		sub := f4([]seg{{0xFFFF, 0xFFFF, 0, 1000}, {0x30, 0x40, dl(0x30, 9), 0}}, nil)
		cmap := buildCmap(0, cmRecord{3, 1, sub, -1})
		tt := parseCmapProgram(t, 1000, cmap)
		if tt.HasSubtable(3, 1) {
			t.Error("HasSubtable(3, 1) = true; {0xFFFF, 0xFFFF} off the final segment is not exempt")
		}
	})
	t.Run("last but not shaped like the sentinel is not exempt (kills M16)", func(t *testing.T) {
		// Last segment, end 0xFFFF like the sentinel, but start 0xFFFE, not 0xFFFF.
		sub := f4([]seg{{'A', 'A', dl('A', 9), 0}, {0xFFFE, 0xFFFF, 0, 1000}}, nil)
		cmap := buildCmap(0, cmRecord{3, 1, sub, -1})
		tt := parseCmapProgram(t, 1000, cmap)
		if tt.HasSubtable(3, 1) {
			t.Error("HasSubtable(3, 1) = true; {0xFFFE, 0xFFFF} in the final slot is not exempt")
		}
	})
}

// TestFormat4OneByteShortOfItsFourArraysIsDropped pins M29: validateCmap4's `len(sub) <
// 16+segX2*4` bound, one byte tighter than the mutant's `-2` form. f4Map's own output (idDelta
// only, no glyphIdArray) is exactly 16+segX2*4 bytes, the accepted boundary every other test that
// calls it already exercises; slicing one byte off is the refused side.
func TestFormat4OneByteShortOfItsFourArraysIsDropped(t *testing.T) {
	full := f4Map(map[uint16]uint16{'A': 9})
	short := full[:len(full)-1]
	cmap := buildCmap(0, cmRecord{3, 1, short, -1})
	tt := parseCmapProgram(t, 1000, cmap)
	if tt.HasSubtable(3, 1) {
		t.Error("HasSubtable(3, 1) = true; a subtable one byte short of its declared segments is invalid")
	}
}

// --- round 5 ---

// wantErrSubstring fails unless err is non-nil and its message contains substr — the shape every
// neighbouring face-level test in this file already asserts by hand; named once here because
// round 5 adds several more of them.
func wantErrSubstring(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Errorf("ParseTrueType err = %v, want it to contain %q", err, substr)
	}
}

// tableAtPhysicalBoundary assembles base plus one more table, named name, declared and physically
// present for exactly physical bytes past its own offset, as the last table in the file — the
// shape R#2 and R#3 need: tt_face_load_hhea's FT_FRAME_START(36) (ttmtx.c:129-194) reads 36 bytes
// from the *stream* at the table's own offset, not bounded by its declared Length, so a table the
// directory rule above would keep (declared Length within what the file has) can still be too
// short for that fixed read. Declaring it any longer than physical would instead make the
// directory rule itself drop the entry — "missing", a different error this is not testing.
func tableAtPhysicalBoundary(base []ttfbuild.Entry, name string, physical int) []byte {
	entries := append(append([]ttfbuild.Entry{}, base...), ttfbuild.Entry{Tag: name, Data: make([]byte, physical)})
	data := ttfbuild.AssembleTablesOrdered(entries)
	i := len(entries) - 1
	off := recordOffset(data, i)
	return data[:off+physical]
}

// noHheaEntries is minimalEntries' five other tables, without its own valid hhea, so a test can
// append the one hhea entry whose physical length it wants to control without producing a
// duplicate tag — ParseTrueType's "first duplicate wins" rule would otherwise read the original,
// always-valid one instead.
func noHheaEntries() []ttfbuild.Entry {
	m := minimalEntries()
	return append([]ttfbuild.Entry{m[0]}, m[2:]...) // head, then hmtx onward
}

// TestHheaThirtySixPhysicalByteBoundary pins R#2: tt_face_load_hhea's FT_FRAME_START(36) reads 36
// bytes from the stream at hhea's own offset, not bounded by its declared Length, so a file that
// ends one byte short of that read fails the face and a file that ends exactly at 36 bytes does
// not. Round 4's own test (TestHheaFewerThan36BytesFailsAgainstPdfium) only tried 10 bytes, which
// a `< 36` bound and the mutant that weakens it to `< 35` both pass.
func TestHheaThirtySixPhysicalByteBoundary(t *testing.T) {
	t.Run("35 bytes physically present is refused (kills hhea<35)", func(t *testing.T) {
		_, err := ParseTrueType(tableAtPhysicalBoundary(noHheaEntries(), "hhea", 35))
		wantErrSubstring(t, err, "hhea table has fewer than 36 bytes from its offset")
	})
	t.Run("36 bytes physically present is the accepted boundary (kills hhea<37)", func(t *testing.T) {
		if _, err := ParseTrueType(tableAtPhysicalBoundary(noHheaEntries(), "hhea", 36)); err != nil {
			t.Errorf("ParseTrueType failed at the accepted hhea boundary: %v", err)
		}
	})
}

// TestVheaThirtySixPhysicalByteBoundary pins R#3, the same read for vhea's own tolerant path
// (LOADM_(hhea,1), sfobjs.c:988-996): present-but-short fails the face; present-and-complete does
// not, even though vhea itself is read for nothing this package uses.
func TestVheaThirtySixPhysicalByteBoundary(t *testing.T) {
	t.Run("35 bytes physically present is refused (kills vhea<35)", func(t *testing.T) {
		_, err := ParseTrueType(tableAtPhysicalBoundary(minimalEntries(), "vhea", 35))
		wantErrSubstring(t, err, "vhea table has fewer than 36 bytes from its offset")
	})
	t.Run("36 bytes physically present is the accepted boundary (kills vhea<37)", func(t *testing.T) {
		if _, err := ParseTrueType(tableAtPhysicalBoundary(minimalEntries(), "vhea", 36)); err != nil {
			t.Errorf("ParseTrueType failed at the accepted vhea boundary: %v", err)
		}
	})
}

// TestVheaDeclaredShortButPhysicallyComplete pins the control finding #6 names (mutant C): a vhea
// whose directory entry declares only 20 bytes, with the full 36 physically present past its
// offset, is accepted — the 36-byte read is against the stream, not against
// entries["vhea"].data's own declared-length slice, so a vhea a reader clamped to its declared
// length first would refuse here for no FreeType reason.
func TestVheaDeclaredShortButPhysicallyComplete(t *testing.T) {
	entries := append(minimalEntries(), ttfbuild.Entry{Tag: "vhea", Data: make([]byte, 20)})
	data := ttfbuild.AssembleTablesOrdered(entries)
	i := len(entries) - 1
	setRecordLength(data, i, 20) // declared 20, though 36 bytes are about to be physically present
	// AssembleTablesOrdered already padded vhea's own 20-byte body to a 4-byte boundary (no-op,
	// 20 is one already); pad the file itself out to 36 physical bytes past vhea's offset, the way
	// a subsetter that trims a directory entry without touching the byte it once covered would
	// leave it.
	data = append(data, make([]byte, 16)...)
	if _, err := ParseTrueType(data); err != nil {
		t.Errorf("ParseTrueType failed on a vhea declared short but physically complete: %v", err)
	}
}

// maxpBase is a minimal face without its own maxp, so a test can append the one maxp entry whose
// physical length it wants to control, as tableAtPhysicalBoundary does for hhea and vhea.
func maxpBase() []ttfbuild.Entry {
	return []ttfbuild.Entry{
		{Tag: "head", Data: head54(1000)},
		{Tag: "hhea", Data: make([]byte, 36)},
		{Tag: "hmtx", Data: make([]byte, 4)},
		{Tag: "loca", Data: make([]byte, 4)},
		{Tag: "glyf", Data: []byte{0, 0, 0, 0}},
	}
}

// TestMaxpNumGlyphsSixPhysicalByteBoundary pins R#6's maxp half (mutants F and F2):
// tt_face_load_maxp reads a fixed 6 bytes (FT_FRAME_START(6): version, then numGlyphs) from the
// stream at maxp's own offset, so a file ending exactly 6 bytes past that offset reads numGlyphs,
// and one byte short leaves it at the zero notFound already turns into "no glyph for any code" —
// not a refusal, since sfobjs.c:915-917 never checks LOAD_(maxp)'s own error. A maxp declared
// longer than what is physically present would instead be dropped by the directory rule itself
// (see tableAtPhysicalBoundary), which leaves numGlyphs at the same zero for a different reason,
// so this builds its maxp entry directly rather than through that helper: numGlyphs' own field
// sits at maxp+4, one field short of the version numGlyphs itself never checks, and a 5-byte maxp
// stops one byte before it either way. The 6-byte side is what kills a guard moved to 7; the
// 5-byte side is a control no threshold mutant can fail, because a guard lowered to 5 is
// equivalent — be16 reads 0 when fewer than 2 bytes remain, so numGlyphs is 0 either way.
func maxpAtPhysicalBoundary(physical int) []byte {
	m := maxp6(6)[:min(physical, 6)]
	entries := append(maxpBase(), ttfbuild.Entry{Tag: "maxp", Data: m})
	data := ttfbuild.AssembleTablesOrdered(entries)
	i := len(entries) - 1
	off := recordOffset(data, i)
	return data[:off+physical]
}

func TestMaxpNumGlyphsSixPhysicalByteBoundary(t *testing.T) {
	t.Run("5 bytes physically present leaves numGlyphs at 0", func(t *testing.T) {
		tt, err := ParseTrueType(maxpAtPhysicalBoundary(5))
		if err != nil {
			t.Fatalf("ParseTrueType: %v", err)
		}
		if got := tt.NumGlyphs(); got != 0 {
			t.Errorf("NumGlyphs() = %d, want 0 — a maxp one byte short of its numGlyphs field is unread", got)
		}
	})
	t.Run("6 bytes physically present reads numGlyphs (the accepted boundary; kills maxp>=7)", func(t *testing.T) {
		tt, err := ParseTrueType(maxpAtPhysicalBoundary(6))
		if err != nil {
			t.Fatalf("ParseTrueType: %v", err)
		}
		if got := tt.NumGlyphs(); got != 6 {
			t.Errorf("NumGlyphs() = %d, want 6", got)
		}
	})
}

// hmtxLastTable builds a minimal face whose hmtx is the last table, declared 24 bytes (six
// glyphs' worth), and returns it cut down to leave exactly physical bytes of hmtx present.
func hmtxLastTable(physical int) []byte {
	entries := []ttfbuild.Entry{
		{Tag: "head", Data: head54(1000)},
		{Tag: "hhea", Data: make([]byte, 36)},
		{Tag: "maxp", Data: maxp6(6)},
		{Tag: "loca", Data: make([]byte, 4)},
		{Tag: "glyf", Data: []byte{0, 0, 0, 0}},
		{Tag: "hmtx", Data: make([]byte, 24)}, // last table, declared 24 bytes
	}
	full := ttfbuild.AssembleTablesOrdered(entries)
	return full[:len(full)-(24-physical)]
}

// TestHmtxOverrunIsClampedNotDropped pins finding #6's mutant B (dropping hmtx's exemption from
// the "runs past EOF, so discard the record" rule entirely): an hmtx that overruns the end of the
// file is clamped to a multiple of 4 and kept, not discarded the way every other overrunning
// table is, as long as 4 or more bytes survive the clamp. TestHmtxClampToAMultipleOfFour below
// pins the 1-3 byte side, where the clamp drives the table to zero length and it is refused.
func TestHmtxOverrunIsClampedNotDropped(t *testing.T) {
	for _, p := range []int{4, 5} {
		t.Run(fmt.Sprintf("hmtx physical %d of 24 declared", p), func(t *testing.T) {
			if _, err := ParseTrueType(hmtxLastTable(p)); err != nil {
				t.Errorf("ParseTrueType refused an hmtx FreeType clamps and keeps: %v", err)
			}
		})
	}
}

// TestHmtxClampToAMultipleOfFour pins R#1 and the mutants it names (A: no `&^ 3`; Y: `&^ 1`): an
// hmtx declared 24 bytes, the last table in the file, with the file ending 1, 2 or 3 bytes into
// it clamps to zero length under `&^ 3` — FreeType's own tt_face_load_font_dir rule — and a
// program with no hmtx table is refused regardless of tag. See TestHmtxOverrunIsClampedNotDropped
// for the 4- and 5-byte side, which shares this fixture.
func TestHmtxClampToAMultipleOfFour(t *testing.T) {
	for _, p := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("hmtx physical %d of 24 declared", p), func(t *testing.T) {
			_, err := ParseTrueType(hmtxLastTable(p))
			wantErrSubstring(t, err, "no hmtx table")
		})
	}
}

// TestFaceRulesPinnedAtFontLevel pins R#7: each round-4 face rule ParseTrueType applies, asserted
// directly against ParseTrueType's own error rather than only through a render/native pdfium
// comparison — so `go test ./font/` alone still catches a regression in any one of them, even in
// a merge where render/native's own test build is broken by something outside this package.
func TestFaceRulesPinnedAtFontLevel(t *testing.T) {
	t.Run("OTTO tag is refused (kills I)", func(t *testing.T) {
		data := ttfbuild.AssembleTablesOrdered(minimalEntries())
		binary.BigEndian.PutUint32(data[0:], 0x4F54544F) // 'OTTO'
		_, err := ParseTrueType(data)
		wantErrSubstring(t, err, "not a truetype program")
	})
	t.Run("missing hhea is refused (kills K)", func(t *testing.T) {
		m := minimalEntries()
		entries := append([]ttfbuild.Entry{m[0]}, m[2:]...) // head, then hmtx onward: no hhea
		_, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
		wantErrSubstring(t, err, "no hhea table")
	})
	t.Run("hhea with 35 physical bytes is refused (kills L)", func(t *testing.T) {
		_, err := ParseTrueType(tableAtPhysicalBoundary(noHheaEntries(), "hhea", 35))
		wantErrSubstring(t, err, "hhea table has fewer than 36 bytes")
	})
	t.Run("missing hmtx is refused (kills J)", func(t *testing.T) {
		m := minimalEntries()
		entries := append(append([]ttfbuild.Entry{}, m[:2]...), m[3:]...) // drop hmtx (index 2)
		_, err := ParseTrueType(ttfbuild.AssembleTablesOrdered(entries))
		wantErrSubstring(t, err, "no hmtx table")
	})
	t.Run("hmtx clamped to zero length is refused", func(t *testing.T) {
		_, err := ParseTrueType(hmtxLastTable(1))
		wantErrSubstring(t, err, "no hmtx table")
	})
	t.Run("vhea with 35 physical bytes is refused (kills M)", func(t *testing.T) {
		_, err := ParseTrueType(tableAtPhysicalBoundary(minimalEntries(), "vhea", 35))
		wantErrSubstring(t, err, "vhea table has fewer than 36 bytes")
	})
}

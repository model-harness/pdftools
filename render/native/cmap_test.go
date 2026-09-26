package native

import (
	"encoding/binary"
	"fmt"
	"sort"
	"testing"

	"github.com/model-harness/pdftools/internal/ttfbuild"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// This file compares ttRoute, ttCodeGID and the font package's cmap reader against pdfium (real
// FreeType, run in-process) for round 2's fixes: R#0 (segCountX2 halved), R#1 (a backward-ordered
// format 4 subtable resolved like FreeType, not refused), R#2 (a post table that names glyphs
// with no surviving cmap subtable), and R#3 (a glyph past maxp refused in every format).

// fixtureTables returns the default fixture font's own tables, keyed by tag, as a map a test can
// edit before reassembling — the same technique the fixture's /Widths and /Encoding already get,
// applied to a table ttfbuild.Builder has no field for. names, if non-nil, builds a format 2.0
// `post` table naming each glyph (see ttfbuild.Builder.PostNames); nil leaves the default
// format 3.0 one, which names none.
func fixtureTables(names []string) map[string][]byte {
	prog := ttfbuild.Builder{UnitsPerEm: 1000, PostNames: names}.Build()
	n := int(binary.BigEndian.Uint16(prog[4:6]))
	tables := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		rec := 12 + i*16
		tag := string(prog[rec : rec+4])
		off, ln := binary.BigEndian.Uint32(prog[rec+8:]), binary.BigEndian.Uint32(prog[rec+12:])
		tables[tag] = append([]byte(nil), prog[off:off+ln]...)
	}
	return tables
}

// withCmap edits the fixture's own `cmap` table (or removes it, for cm nil) and reassembles.
func withCmap(cm []byte, names []string) func(*textPDFOpts) {
	tables := fixtureTables(names)
	if cm == nil {
		delete(tables, "cmap")
	} else {
		tables["cmap"] = cm
	}
	return withProgram(ttfbuild.AssembleTables(tables))
}

// cmSub is one 'cmap' table record: the platform and encoding it names, and its subtable bytes.
type cmSub struct {
	plat, enc uint16
	data      []byte
}

// cmapTable writes a 'cmap' table's version, record count, records and subtable bodies, in that
// order — the layout tt_face_build_cmaps reads a record's offset against.
func cmapTable(subs ...cmSub) []byte {
	head := binary.BigEndian.AppendUint16(nil, 0)
	head = binary.BigEndian.AppendUint16(head, uint16(len(subs)))
	base := 4 + 8*len(subs)
	var body []byte
	for _, s := range subs {
		head = binary.BigEndian.AppendUint16(head, s.plat)
		head = binary.BigEndian.AppendUint16(head, s.enc)
		head = binary.BigEndian.AppendUint32(head, uint32(base+len(body)))
		body = append(body, s.data...)
	}
	return append(head, body...)
}

// cmSeg is one format 4 segment.
type cmSeg struct{ start, end, delta, ro uint16 }

// dl is the idDelta that sends code to gid, computed mod 65536.
func dl(code, gid uint16) uint16 { return gid - code }

// format4 writes a well-formed format 4 subtable with segCountX2 = 2*len(segs), exactly as given —
// sorted or not, terminated or not, whichever the scenario needs.
func format4(segs []cmSeg, glyphIDs []uint16) []byte {
	return format4Raw(uint16(2*len(segs)), segs, glyphIDs)
}

// format4Raw writes a format 4 subtable with the declared segCountX2 the caller chooses, which is
// what R#0's odd-count fixtures need and format4 above cannot express.
func format4Raw(segCountX2 uint16, segs []cmSeg, glyphIDs []uint16) []byte {
	b := binary.BigEndian.AppendUint16(nil, 4)
	b = binary.BigEndian.AppendUint16(b, uint16(16+8*len(segs)+2*len(glyphIDs))) // length
	b = binary.BigEndian.AppendUint16(b, 0)                                      // language
	b = binary.BigEndian.AppendUint16(b, segCountX2)
	b = binary.BigEndian.AppendUint16(b, 0) // searchRange
	b = binary.BigEndian.AppendUint16(b, 0) // entrySelector
	b = binary.BigEndian.AppendUint16(b, 0) // rangeShift
	for _, s := range segs {
		b = binary.BigEndian.AppendUint16(b, s.end)
	}
	b = binary.BigEndian.AppendUint16(b, 0) // reservedPad
	for _, s := range segs {
		b = binary.BigEndian.AppendUint16(b, s.start)
	}
	for _, s := range segs {
		b = binary.BigEndian.AppendUint16(b, s.delta)
	}
	for _, s := range segs {
		b = binary.BigEndian.AppendUint16(b, s.ro)
	}
	for _, g := range glyphIDs {
		b = binary.BigEndian.AppendUint16(b, g)
	}
	return b
}

// format4Map writes a well-formed, sorted format 4 subtable mapping each code by idDelta, plus
// the required {0xFFFF, 0xFFFF} terminating segment.
func format4Map(m map[uint16]uint16) []byte {
	codes := make([]uint16, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	segs := make([]cmSeg, 0, len(codes)+1)
	for _, c := range codes {
		segs = append(segs, cmSeg{c, c, dl(c, m[c]), 0})
	}
	segs = append(segs, cmSeg{0xFFFF, 0xFFFF, 1, 0})
	return format4(segs, nil)
}

// blank reports whether every pixel is white — the ink a page with nothing drawn on it has.
func blank(px []byte) bool {
	for _, v := range px {
		if v != 0 {
			return false
		}
	}
	return true
}

// sameInk reports whether two ink slices are pixel-identical.
func sameInk(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pdfiumInk renders path in pdfium alone, for a scenario this backend refuses outright.
func pdfiumInk(t *testing.T, path string) []byte {
	t.Helper()
	p, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium: %v", err)
	}
	defer func() { _ = p.Close() }()
	o := render.DefaultOptions
	o.DPI = 72
	got, err := p.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}
	px, _, _ := ink(got.Image)
	return px
}

// TestFormat4OddSegCountX2AgreesWithPdfium pins R#0's cases (a) and (b), against pdfium: a
// segCountX2 FreeType halves is read the way FreeType reads it, not four bytes stricter.
func TestFormat4OddSegCountX2AgreesWithPdfium(t *testing.T) {
	t.Run("a: a good (3,1) record after an odd, kept one is never reached", func(t *testing.T) {
		// segCountX2 3 halves to one segment, [0, 0xFFFF], sending every code — including A — to
		// the diamond. The second, well-formed record maps A to the square, and would win were
		// the first one still (wrongly) dropped for being four bytes short of the raw bound.
		odd := format4Raw(3, []cmSeg{{0, 0xFFFF, dl('A', ttfbuild.GIDDiamond), 0}}, nil)
		good := format4Map(map[uint16]uint16{'A': ttfbuild.GIDSquare})
		cm := cmapTable(cmSub{3, 1, odd}, cmSub{3, 1, good})
		got, gotRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET", withCmap(cm, nil))
		want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")
		assertSameInk(t, got, gotRef, want, wantRef)
	})
	t.Run("b: the odd subtable alone is kept, not dropped", func(t *testing.T) {
		odd := format4Raw(3, []cmSeg{{0, 0xFFFF, dl('A', ttfbuild.GIDDiamond), 0}}, nil)
		cm := cmapTable(cmSub{3, 1, odd})
		got, gotRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET", withCmap(cm, nil))
		want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")
		assertSameInk(t, got, gotRef, want, wantRef)
	})
}

// TestFormat4OddSegCountX2SymbolicFallsThroughToCode pins R#0's case (c): a symbolic (3,0) whose
// segCountX2 is 1 halves to zero segments, which FreeType keeps but finds nothing in for any
// code. pdfium's GetGlyphIndexForMSSymbol then finds nothing through the (3,0) charmap either,
// and falls all the way through LoadGlyphMap to gid = code = 2, the diamond — a glyph this
// backend has no route to once it agrees the (3,0) subtable is empty rather than misaligned, so
// it refuses instead of drawing the wrong one HEAD used to.
func TestFormat4OddSegCountX2SymbolicFallsThroughToCode(t *testing.T) {
	// The exact bytes tt_cmap4_validate's halving matters for: segCountX2 1 (odd), and — read
	// raw instead — a misaligned single segment [0, 2] with idRangeOffset 3, sending code 2 to
	// glyphIdArray's one entry (the square) instead of finding nothing.
	sub := []byte{0, 4, 0, 28, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0x00, 0x02, 0, 0, 0, 0, 0, 3, 0, 0, 0, 0, 0, 1}
	cm := cmapTable(cmSub{3, 0, sub})
	path := textPDF(t, `BT /F1 48 Tf 20 100 Td (\002) Tj ET`, 200,
		withFlags(4), withEncoding(""), withCmap(cm, nil))
	wantRefused(t, path, "no glyph for code 2 in /Test")

	_, diamondRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")
	ref := pdfiumInk(t, path)
	if !sameInk(ref, diamondRef) {
		t.Error("pdfium did not draw the diamond; the divergence this refusal avoids is not the one tested")
	}
}

// TestFormat4BackwardOrderedSubtableAgreesWithPdfium pins R#1: a format 4 subtable whose segments
// go backward is flagged TT_CMAP_FLAG_UNSORTED and kept, not dropped, and lookup's forward scan
// reads it exactly the way FreeType's own linear search does — including a code a later segment
// names but an earlier segment's start has already ruled out.
func TestFormat4BackwardOrderedSubtableAgreesWithPdfium(t *testing.T) {
	back := format4([]cmSeg{
		{'B', 'B', dl('B', ttfbuild.GIDComposite), 0},
		{'A', 'A', dl('A', ttfbuild.GIDDiamond), 0},
	}, nil)
	cm := cmapTable(cmSub{3, 1, back})
	t.Run("B is inside the first segment", func(t *testing.T) {
		got, gotRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET", withCmap(cm, nil))
		want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (C) Tj ET")
		assertSameInk(t, got, gotRef, want, wantRef)
	})
	t.Run("A is inside the second segment, but the first segment's start already excludes it", func(t *testing.T) {
		path := textPDF(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET", 200, withCmap(cm, nil))
		wantRefused(t, path, "no glyph for code 65 in /Test")
		if ref := pdfiumInk(t, path); !blank(ref) {
			t.Error("pdfium drew something for A; this backend's own miss no longer agrees with it")
		}
	})
}

// TestSymbolicByCodeAndPostNames pins R#2's four scenarios against pdfium: gid = code is right
// only when FreeType has nothing else to read a code through, and a `post` table that names
// glyphs is something else once no cmap subtable survives.
func TestSymbolicByCodeAndPostNames(t *testing.T) {
	postNames := []string{"", "sq", "uni0001", "cx", "rg", "sp"} // glyph 2 (diamond) is uni0001
	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`

	t.Run("post 3.0, no cmap: drawn and agreeing", func(t *testing.T) {
		got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withCmap(nil, nil))
		want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET")
		assertSameInk(t, got, gotRef, want, wantRef)
	})
	t.Run("no cmap table and no post table: drawn and agreeing", func(t *testing.T) {
		tables := fixtureTables(nil)
		delete(tables, "cmap")
		delete(tables, "post")
		got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""),
			withProgram(ttfbuild.AssembleTables(tables)))
		want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET")
		assertSameInk(t, got, gotRef, want, wantRef)
	})
	t.Run("post 2.0 names, a broken (3,0) dropped, no other subtable: refused", func(t *testing.T) {
		// start > end drops the whole subtable at FT_VALIDATE_DEFAULT, leaving no cmap subtable
		// at all — the same shape F3c's fix already refuses, now with post 2.0 behind it too.
		bad30 := format4([]cmSeg{{0x30, 0x20, 0, 0}, {0xF001, 0xF001, dl(0xF001, ttfbuild.GIDComposite), 0}}, nil)
		path := textPDF(t, one, 200, withFlags(4), withEncoding(""),
			withCmap(cmapTable(cmSub{3, 0, bad30}), postNames))
		wantRefused(t, path, "a post table that names glyphs")

		_, diamondRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")
		if ref := pdfiumInk(t, path); !sameInk(ref, diamondRef) {
			t.Error("pdfium did not draw the diamond through the synthesized charmap")
		}
	})
	t.Run("no cmap table, post 2.0 names: refused", func(t *testing.T) {
		tables := fixtureTables(postNames)
		delete(tables, "cmap")
		path := textPDF(t, one, 200, withFlags(4), withEncoding(""),
			withProgram(ttfbuild.AssembleTables(tables)))
		wantRefused(t, path, "a post table that names glyphs")

		_, diamondRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")
		if ref := pdfiumInk(t, path); !sameInk(ref, diamondRef) {
			t.Error("pdfium did not draw the diamond through the synthesized charmap")
		}
	})
}

// TestFormat4PastMaxpAgreesWithPdfium pins R#3's reviewer-named cases: FT_Get_Char_Index zeroes a
// result at or past what maxp declares, on every format 4 path this backend reads, and pdfium
// agrees exactly because GetCharIndex for the same code goes through the same FreeType function.
func TestFormat4PastMaxpAgreesWithPdfium(t *testing.T) {
	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	diamond, diamondRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")

	t.Run("idDelta: 0x0001 -> 50 past maxp's 6, 0xF001 -> diamond", func(t *testing.T) {
		sym := format4([]cmSeg{{1, 1, dl(1, 50), 0}, {0xF001, 0xF001, dl(0xF001, ttfbuild.GIDDiamond), 0}}, nil)
		got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withCmap(cmapTable(cmSub{3, 0, sym}), nil))
		if !sameInk(got, diamond) || !sameInk(gotRef, diamondRef) {
			t.Errorf("got %v pdfium %v, want both to draw the diamond through 0xF001",
				sameInk(got, diamond), sameInk(gotRef, diamondRef))
		}
	})
	t.Run("idRangeOffset: 0x0001 -> 50 through glyphIdArray, 0xF001 -> diamond", func(t *testing.T) {
		sym := format4([]cmSeg{{1, 1, 0, 6}, {0xF001, 0xF001, dl(0xF001, ttfbuild.GIDDiamond), 0},
			{0xFFFF, 0xFFFF, 1, 0}}, []uint16{50})
		got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withCmap(cmapTable(cmSub{3, 0, sym}), nil))
		if !sameInk(got, diamond) || !sameInk(gotRef, diamondRef) {
			t.Errorf("got %v pdfium %v, want both to draw the diamond through 0xF001",
				sameInk(got, diamond), sameInk(gotRef, diamondRef))
		}
	})
	t.Run("format 6 at (1,0): code 1 -> 50 past maxp's 6, no other range to fall back to", func(t *testing.T) {
		f6 := []byte{0, 6, 0, 12, 0, 0, 0, 1, 0, 1, 0, 50}
		path := textPDF(t, one, 200, withFlags(4), withEncoding(""), withCmap(cmapTable(cmSub{1, 0, f6}), nil))
		wantRefused(t, path, "no glyph for code 1 in /Test")
		if ref := pdfiumInk(t, path); !blank(ref) {
			t.Error("pdfium drew something for code 1; this backend's own miss no longer agrees with it")
		}
	})
}

// --- round 3 ---

// tablesOf indexes prog's own table directory by tag, the general form of fixtureTables above for
// a program the default fixture's PostNames-only knob cannot build (SymbolicCmap, here).
func tablesOf(prog []byte) map[string][]byte {
	n := int(binary.BigEndian.Uint16(prog[4:6]))
	tables := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		rec := 12 + i*16
		tag := string(prog[rec : rec+4])
		off, ln := binary.BigEndian.Uint32(prog[rec+8:]), binary.BigEndian.Uint32(prog[rec+12:])
		tables[tag] = append([]byte(nil), prog[off:off+ln]...)
	}
	return tables
}

// orderedEntries picks tags out of tables in the given order, for ttfbuild.AssembleTablesOrdered
// callers below that need a directory a plain map (which AssembleTables always sorts and
// deduplicates) cannot express.
func orderedEntries(tables map[string][]byte, tags ...string) []ttfbuild.Entry {
	entries := make([]ttfbuild.Entry, 0, len(tags))
	for _, tag := range tags {
		entries = append(entries, ttfbuild.Entry{Tag: tag, Data: tables[tag]})
	}
	return entries
}

// setRecordLength patches one table-directory record's length field in place.
func setRecordLength(data []byte, i, length int) {
	binary.BigEndian.PutUint32(data[12+i*16+12:], uint32(length))
}

// setRecordOffset patches one table-directory record's offset field in place.
func setRecordOffset(data []byte, i, off int) {
	binary.BigEndian.PutUint32(data[12+i*16+8:], uint32(off))
}

// TestNoMaxpAgreesWithPdfiumRefusal pins R#0 against pdfium: FT_Get_Char_Index zeroes every result
// once face->num_glyphs is 0 (ftobjs.c:3947-3949), which a missing or unreadable maxp leaves it at
// (sfobjs.c:915-917). A (3,0) subtable that would otherwise resolve code 1 to the square is
// refused instead, agreeing with pdfium's blank page rather than drawing what native used to.
func TestNoMaxpAgreesWithPdfiumRefusal(t *testing.T) {
	base := tablesOf(ttfbuild.Builder{UnitsPerEm: 1000, SymbolicCmap: true}.Build())
	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	for _, c := range []struct {
		name string
		edit func(map[string][]byte)
	}{
		{"no maxp table at all", func(tb map[string][]byte) { delete(tb, "maxp") }},
		{"maxp present, numGlyphs 0", func(tb map[string][]byte) { tb["maxp"] = make([]byte, 6) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			tb := make(map[string][]byte, len(base))
			for k, v := range base {
				tb[k] = v
			}
			c.edit(tb)
			path := textPDF(t, one, 200, withFlags(4), withEncoding(""), withProgram(ttfbuild.AssembleTables(tb)))
			wantRefused(t, path, "no glyph for code 1 in /Test")
			if ref := pdfiumInk(t, path); !blank(ref) {
				t.Error("pdfium drew something for code 1; num_glyphs 0 should zero every result")
			}
		})
	}
}

// TestDuplicateCmapTableFirstWinsAgainstPdfium pins R#1/R#2's directory-level first-wins rule
// against pdfium: two whole `cmap` tables in the sfnt directory, not two records inside one,
// resolve to the first.
func TestDuplicateCmapTableFirstWinsAgainstPdfium(t *testing.T) {
	tables := fixtureTables(nil)
	first := cmapTable(cmSub{3, 0, format4([]cmSeg{{1, 1, dl(1, ttfbuild.GIDSquare), 0}, {0xFFFF, 0xFFFF, 1, 0}}, nil)})
	second := cmapTable(cmSub{3, 0, format4([]cmSeg{{1, 1, dl(1, ttfbuild.GIDDiamond), 0}, {0xFFFF, 0xFFFF, 1, 0}}, nil)})
	entries := orderedEntries(tables, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "post", "OS/2")
	entries = append(entries, ttfbuild.Entry{Tag: "cmap", Data: first}, ttfbuild.Entry{Tag: "cmap", Data: second})
	data := ttfbuild.AssembleTablesOrdered(entries)

	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withProgram(data))
	want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET")
	assertSameInk(t, got, gotRef, want, wantRef)
}

// TestZeroLengthFirstPostShadowsLaterPostAgainstPdfium pins R#2's post-directory finding against
// pdfium: a zero-length `post` entry ahead of a real, glyph-naming one wins and reads as absent
// (tt_face_lookup_table, ttload.c:79-84), so a by-code font with no cmap at all is drawn as
// gid = code rather than refused for a post table that, in the end, names nothing FreeType reads.
func TestZeroLengthFirstPostShadowsLaterPostAgainstPdfium(t *testing.T) {
	names := []string{"", "sq", "uni0001", "cx", "rg", "sp"} // glyph 2 (diamond) is uni0001
	tables := fixtureTables(names)
	entries := orderedEntries(tables, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "OS/2")
	entries = append(entries,
		ttfbuild.Entry{Tag: "post", Data: nil},            // zero-length, first: wins, reads as absent
		ttfbuild.Entry{Tag: "post", Data: tables["post"]}) // names glyphs, but shadowed
	data := ttfbuild.AssembleTablesOrdered(entries)

	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withProgram(data))
	want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET")
	assertSameInk(t, got, gotRef, want, wantRef)
}

// TestCmapTableOneBytePastEOFIsDroppedAgainstPdfium pins R#1/R#2's per-record offset/length rule
// against pdfium at the table this backend actually reads: a `cmap` table whose declared length
// runs one byte past the end of the file is dropped entirely (ttload.c:437-464), not clamped, so a
// by-code font falls all the way through to gid = code, the same place pdfium's own dropped-table
// reading lands.
func TestCmapTableOneBytePastEOFIsDroppedAgainstPdfium(t *testing.T) {
	tables := fixtureTables(nil) // default post 3.0, which does not name glyphs
	entries := orderedEntries(tables, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "post", "OS/2")
	entries = append(entries, ttfbuild.Entry{Tag: "cmap", Data: tables["cmap"]}) // last in the file
	data := ttfbuild.AssembleTablesOrdered(entries)
	setRecordLength(data, len(entries)-1, len(tables["cmap"])+1) // one byte past the end

	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withProgram(data))
	want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET")
	assertSameInk(t, got, gotRef, want, wantRef)
}

// TestByCodeWithSubtableAndPostNamesIsDrawnAgainstPdfium pins R#5: ttRoute's post-names refusal
// requires !tt.HasCmap() as well as PostNamesGlyphs() — a symbolic font with a surviving (3,0)
// subtable is drawn through it regardless of what `post` says, because FreeType only synthesizes
// a Unicode charmap from post's names when nothing else survived (sfobjs.c:1206-1230), and a
// surviving MS_SYMBOL subtable is "something else".
func TestByCodeWithSubtableAndPostNamesIsDrawnAgainstPdfium(t *testing.T) {
	names := []string{"", "sq", "uni0001", "cx", "rg", "sp"}
	data := ttfbuild.Builder{UnitsPerEm: 1000, SymbolicCmap: true, PostNames: names}.Build()

	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withProgram(data))
	want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET")
	assertSameInk(t, got, gotRef, want, wantRef)
}

// --- round 4 ---

// TestFaceLevelFailuresAgainstPdfium pins R#0: a face FreeType's TrueType driver itself would
// fail to open — a missing hhea or hmtx, an 'OTTO' tag over a glyf program, or the Mac 'true' tag
// with a missing hhea — is refused here too, agreeing with pdfium's own substitution rather than
// drawing this program's embedded glyphs the way the round-3 tree still did.
//
// The 'true' case is the one worth spelling out: FreeType's own sfnt_load_face *succeeds* for a
// 'true'-tagged face with no hhea, setting has_outline = 0 instead of returning
// Horiz_Header_Missing (sfobjs.c:952-961). That is a return code, not a drawable face — a face
// with no outline flag has nothing for pdfium's font code to read glyf through, so it substitutes
// there exactly as it does for Horiz_Header_Missing on every other tag. Refusing it here, with no
// 'true' exemption, is stricter than FreeType's own return value and truer to what pdfium draws.
func TestFaceLevelFailuresAgainstPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	_, squareRef := drawBoth(t, A) // the embedded program's own square, when both readers draw it

	edit := func(fn func(map[string][]byte)) []byte {
		tb := fixtureTables(nil)
		fn(tb)
		return ttfbuild.AssembleTables(tb)
	}
	for _, c := range []struct {
		name string
		prog []byte
		want string
	}{
		{"no hhea", edit(func(tb map[string][]byte) { delete(tb, "hhea") }), "no hhea table"},
		{"no hmtx", edit(func(tb map[string][]byte) { delete(tb, "hmtx") }), "no hmtx table"},
		{"OTTO tag with glyf", func() []byte {
			p := edit(func(map[string][]byte) {})
			binary.BigEndian.PutUint32(p, 0x4F54544F)
			return p
		}(), "not a truetype program"},
		{"'true' tag, no hhea", func() []byte {
			p := edit(func(tb map[string][]byte) { delete(tb, "hhea") })
			binary.BigEndian.PutUint32(p, 0x74727565)
			return p
		}(), "no hhea table"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := textPDF(t, A, 200, withProgram(c.prog))
			wantRefused(t, path, c.want)
			ref := pdfiumInk(t, path)
			if blank(ref) {
				t.Error("pdfium drew nothing for 'A'; want a substituted face's own glyph")
			}
			if sameInk(ref, squareRef) {
				t.Error("pdfium drew the fixture's own square; want a substituted face's different one")
			}
		})
	}
}

// TestFaceLevelControlsAgreeWithPdfium pins the two cases the rules above must not over-refuse: a
// declared hhea length shorter than the 36 bytes tt_face_load_hhea actually reads, with the real
// 36 (and more) physically present in the file past it, and the Mac 'true' tag with a complete
// hhea. Both draw the embedded program's own square in every reader.
func TestFaceLevelControlsAgreeWithPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	t.Run("hhea declared 20 bytes, 36 physically present", func(t *testing.T) {
		tb := fixtureTables(nil)
		tb["hhea"] = tb["hhea"][:20]
		got, gotRef := drawBoth(t, A, withProgram(ttfbuild.AssembleTables(tb)))
		want, wantRef := drawBoth(t, A)
		assertSameInk(t, got, gotRef, want, wantRef)
	})
	t.Run("'true' tag, hhea present", func(t *testing.T) {
		p := ttfbuild.AssembleTables(fixtureTables(nil))
		binary.BigEndian.PutUint32(p, 0x74727565)
		got, gotRef := drawBoth(t, A, withProgram(p))
		want, wantRef := drawBoth(t, A)
		assertSameInk(t, got, gotRef, want, wantRef)
	})
}

// TestDuplicatePostFirstWinsBothOffsetAndDataAgainstPdfium pins R#2's D4 finding: the entry kept
// for a duplicated tag must have its offset and its bytes come from the same directory record.
// Two `post` tables, 2.0 (which names glyphs) first and 3.0 (which does not) second: FreeType
// keeps the first (ttload.c:478-499, "the first one wins") and, with no cmap subtable surviving
// at all, synthesizes a Unicode charmap from its names (sfobjs.c:1206-1230) — so pdfium draws
// code 1 through it (the diamond, glyph 2, named uni0001), while this backend refuses once it
// agrees the kept post table names glyphs.
func TestDuplicatePostFirstWinsBothOffsetAndDataAgainstPdfium(t *testing.T) {
	names := []string{"", "sq", "uni0001", "cx", "rg", "sp"} // glyph 2 (diamond) is uni0001
	t20, t30 := fixtureTables(names), fixtureTables(nil)
	entries := orderedEntries(t30, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "OS/2")
	entries = append(entries,
		ttfbuild.Entry{Tag: "post", Data: t20["post"]}, // 2.0, names glyphs: kept
		ttfbuild.Entry{Tag: "post", Data: t30["post"]}) // 3.0, shadowed
	data := ttfbuild.AssembleTablesOrdered(entries)

	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	path := textPDF(t, one, 200, withFlags(4), withEncoding(""), withProgram(data))
	wantRefused(t, path, "a post table that names glyphs")

	_, diamondRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")
	if ref := pdfiumInk(t, path); !sameInk(ref, diamondRef) {
		t.Error("pdfium did not draw the diamond through the synthesized charmap")
	}
}

// TestSecondShortHeadFailsAgainstPdfium and TestShortBhedNextToValidHeadFailsAgainstPdfium pin
// R#3's D5 and D6: check_table_dir's own head/bhed length rule (ttload.c:251-264) runs inside the
// per-record loop over every entry carrying that tag, not only the first one kept and not only
// 'head' to the exclusion of 'bhed' — so a second, short head behind a valid one, or a short bhed
// beside a valid head, fails the whole face exactly as a lone short head does.
func TestSecondShortHeadFailsAgainstPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	tb := fixtureTables(nil)
	entries := orderedEntries(tb, "head")
	entries = append(entries, ttfbuild.Entry{Tag: "head", Data: tb["head"][:20]}) // short, second
	entries = append(entries, orderedEntries(tb, "hhea", "hmtx", "maxp", "loca", "glyf", "post", "OS/2", "cmap")...)
	path := textPDF(t, A, 200, withProgram(ttfbuild.AssembleTablesOrdered(entries)))
	wantRefused(t, path, "shorter than 0x36 bytes")

	_, squareRef := drawBoth(t, A)
	ref := pdfiumInk(t, path)
	if blank(ref) {
		t.Error("pdfium drew nothing for 'A'; want a substituted face's own glyph")
	}
	if sameInk(ref, squareRef) {
		t.Error("pdfium drew the fixture's own square; want a substituted face's different one")
	}
}

func TestShortBhedNextToValidHeadFailsAgainstPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	tb := fixtureTables(nil)
	tb["bhed"] = tb["head"][:20] // short, alongside a valid head
	path := textPDF(t, A, 200, withProgram(ttfbuild.AssembleTables(tb)))
	wantRefused(t, path, "shorter than 0x36 bytes")

	_, squareRef := drawBoth(t, A)
	ref := pdfiumInk(t, path)
	if blank(ref) {
		t.Error("pdfium drew nothing for 'A'; want a substituted face's own glyph")
	}
	if sameInk(ref, squareRef) {
		t.Error("pdfium drew the fixture's own square; want a substituted face's different one")
	}
}

// TestZeroLengthCmapAtEOFShadowsLaterCmapAgainstPdfium pins R#4: a zero-length cmap entry whose
// offset is exactly len(data) passes check_table_dir's own bound (Offset > size is false at
// Offset == size, ttload.c:217) and is kept, not dropped — so a later, real cmap table of the
// same tag is discarded as a duplicate (ttload.c:478-499, "the first one wins") and reads as
// absent (tt_face_lookup_table, ttload.c:79-84), leaving this font with no surviving cmap
// subtable at all. Both readers then fall through to gid = code for a symbolic font with no
// /Encoding, so code 1 draws the square (glyph 1) rather than the diamond the shadowed cmap named.
func TestZeroLengthCmapAtEOFShadowsLaterCmapAgainstPdfium(t *testing.T) {
	tb := fixtureTables(nil) // post 3.0, which does not name glyphs
	real := cmapTable(cmSub{3, 0, format4Map(map[uint16]uint16{0x0001: ttfbuild.GIDDiamond, 0xF001: ttfbuild.GIDDiamond})})
	entries := orderedEntries(tb, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "post", "OS/2")
	entries = append(entries,
		ttfbuild.Entry{Tag: "cmap", Data: nil}, // zero-length, first
		ttfbuild.Entry{Tag: "cmap", Data: real})
	data := ttfbuild.AssembleTablesOrdered(entries)
	setRecordOffset(data, len(entries)-2, len(data)) // the zero-length entry's offset is exactly EOF

	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	got, gotRef := drawBoth(t, one, withFlags(4), withEncoding(""), withProgram(data))
	want, wantRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET") // gid = code = 1, the square
	assertSameInk(t, got, gotRef, want, wantRef)
}

// TestMaxpDeclaredShortNumGlyphsReadFromFileAgainstPdfium pins R#6: maxp's own numGlyphs field is
// read as 2 bytes at a fixed offset in the *stream*, not bounded by the table's declared length
// (ttload.c:728-770, tt_face_load_maxp: goto_table seeks, then FT_STREAM_READ_FIELDS with
// FT_FRAME_START(6)) — so a maxp table declared only 4 bytes long, with the full table still
// physically present in the file, is read exactly as if it had declared the whole thing.
func TestMaxpDeclaredShortNumGlyphsReadFromFileAgainstPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	tb := fixtureTables(nil)
	entries := orderedEntries(tb, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "post", "OS/2", "cmap")
	data := ttfbuild.AssembleTablesOrdered(entries)
	setRecordLength(data, 3, 4) // maxp, declared 4 bytes; the full table remains in the file

	got, gotRef := drawBoth(t, A, withProgram(data))
	want, wantRef := drawBoth(t, A)
	assertSameInk(t, got, gotRef, want, wantRef)
}

// TestPostDeclaredUnder34NamesGlyphsButPdfiumDrawsGidEqualsCode pins R#7's over-refusal
// deliberately: load_post_names (ttpost.c:340, "post_len < 34") only extracts a post 2.0 or 2.5
// table's names when its own *declared* length is at least 34 bytes, even though the fixed
// 32-byte header tt_face_load_post reads to set FT_FACE_FLAG_GLYPH_NAMES is unaffected by that
// same declared length. A post 2.0 table declared shorter than 34, with the full table
// physically present in the file, therefore sets the flag but loads no names — so
// sfnt_load_face's Unicode-charmap synthesis has nothing to synthesize from
// (No_Unicode_Glyph_Name, tolerated, sfobjs.c:1226-1229) — and a symbolic font with no cmap at
// all falls through to gid = code, the square. This backend's PostNamesGlyphs reads only the
// physical 32-byte header, not the declared-length distinction load_post_names draws, so it
// refuses instead: the safe direction, and the reason this is a documented over-refusal rather
// than a bug.
func TestPostDeclaredUnder34NamesGlyphsButPdfiumDrawsGidEqualsCode(t *testing.T) {
	names := []string{"", "sq", "uni0001", "cx", "rg", "sp"} // glyph 2 (diamond) is uni0001
	tb := fixtureTables(names)                               // post 2.0, naming glyphs
	one := `BT /F1 48 Tf 20 100 Td (\001) Tj ET`
	for _, n := range []int{3, 10, 19, 20, 33} { // all under 34; 34 itself is the control below
		t.Run(fmt.Sprintf("declared %d bytes", n), func(t *testing.T) {
			entries := orderedEntries(tb, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "OS/2", "post")
			data := ttfbuild.AssembleTablesOrdered(entries)
			setRecordLength(data, len(entries)-1, n)
			path := textPDF(t, one, 200, withFlags(4), withEncoding(""), withProgram(data))
			wantRefused(t, path, "a post table that names glyphs")

			_, squareRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (A) Tj ET")
			if ref := pdfiumInk(t, path); !sameInk(ref, squareRef) {
				t.Error("pdfium did not draw the square through gid = code")
			}
		})
	}
	t.Run("control: post declared its own full length, names actually load", func(t *testing.T) {
		// load_format_20 (ttpost.c) needs post_len-34 bytes beyond the 32-byte header to read the
		// glyph name index array and the Pascal strings themselves — declaring exactly 34 leaves
		// zero of those, so only the *whole* table's own declared length lets names load at all.
		entries := orderedEntries(tb, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "OS/2", "post")
		data := ttfbuild.AssembleTablesOrdered(entries)
		path := textPDF(t, one, 200, withFlags(4), withEncoding(""), withProgram(data))
		wantRefused(t, path, "a post table that names glyphs")

		_, diamondRef := drawBoth(t, "BT /F1 48 Tf 20 100 Td (B) Tj ET")
		if ref := pdfiumInk(t, path); !sameInk(ref, diamondRef) {
			t.Error("pdfium did not draw the diamond through the synthesized charmap")
		}
	})
}

// TestHheaFewerThan36BytesFailsAgainstPdfium pins the read-failure half of R#0's hhea rule,
// distinct from a missing table: tt_face_load_hhea reads a fixed 36 bytes from the stream at
// hhea's own offset (ttmtx.c:129-194), so a *present* hhea entry, kept by the directory rules
// because its own declared length is honest about what is left, still fails the face when the
// file itself runs out before those 36 bytes do.
func TestHheaFewerThan36BytesFailsAgainstPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	tb := fixtureTables(nil)
	entries := orderedEntries(tb, "head", "maxp", "hmtx", "loca", "glyf", "post", "OS/2", "cmap", "hhea")
	data := ttfbuild.AssembleTablesOrdered(entries)
	data = data[:len(data)-26]                // hhea, the last table, now has only 10 bytes physically present
	setRecordLength(data, len(entries)-1, 10) // its own directory entry agrees, so it is kept, not dropped

	path := textPDF(t, A, 200, withProgram(data))
	wantRefused(t, path, "fewer than 36 bytes from its offset")

	_, squareRef := drawBoth(t, A)
	ref := pdfiumInk(t, path)
	if blank(ref) {
		t.Error("pdfium drew nothing for 'A'; want a substituted face's own glyph")
	}
	if sameInk(ref, squareRef) {
		t.Error("pdfium drew the fixture's own square; want a substituted face's different one")
	}
}

// TestVheaFewerThan36BytesFailsAgainstPdfium pins the other named vhea rule: unlike a missing
// vhea (tolerated, sfobjs.c:996's Table_Missing exemption; every other test in this file omits
// vhea entirely and still draws), a *present* vhea entry that cannot be read — fewer than 36
// bytes physically left in the file from its offset — is an error other than Table_Missing and
// fails the whole face.
func TestVheaFewerThan36BytesFailsAgainstPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	tb := fixtureTables(nil)
	entries := orderedEntries(tb, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "post", "OS/2", "cmap")
	entries = append(entries, ttfbuild.Entry{Tag: "vhea", Data: make([]byte, 36)}) // last table
	data := ttfbuild.AssembleTablesOrdered(entries)
	data = data[:len(data)-26]                // vhea now has only 10 bytes physically present
	setRecordLength(data, len(entries)-1, 10) // its own directory entry agrees, so it is kept, not dropped

	path := textPDF(t, A, 200, withProgram(data))
	wantRefused(t, path, "vhea table has fewer than 36 bytes from its offset")

	_, squareRef := drawBoth(t, A)
	ref := pdfiumInk(t, path)
	if blank(ref) {
		t.Error("pdfium drew nothing for 'A'; want a substituted face's own glyph")
	}
	if sameInk(ref, squareRef) {
		t.Error("pdfium drew the fixture's own square; want a substituted face's different one")
	}
}

// TestHheaAndVheaAt35And36PhysicalBytesAgainstPdfium pins the two tests above at their own
// boundary rather than 26 bytes short of it: with hhea, or a present vhea, as the last table and
// the file ending 35 bytes into it, the face fails in both engines — this backend refuses and
// pdfium substitutes — and with all 36 present, both draw the fixture's own square.
func TestHheaAndVheaAt35And36PhysicalBytesAgainstPdfium(t *testing.T) {
	A := `BT /F1 48 Tf 20 100 Td (A) Tj ET`
	nativeSquare, squareRef := drawBoth(t, A)
	for _, which := range []string{"hhea", "vhea"} {
		for _, physical := range []int{35, 36} {
			t.Run(fmt.Sprintf("%s with %d physical bytes", which, physical), func(t *testing.T) {
				tb := fixtureTables(nil)
				var entries []ttfbuild.Entry
				if which == "hhea" {
					entries = orderedEntries(tb, "head", "maxp", "hmtx", "loca", "glyf", "post", "OS/2", "cmap", "hhea")
				} else {
					entries = orderedEntries(tb, "head", "hhea", "hmtx", "maxp", "loca", "glyf", "post", "OS/2", "cmap")
					entries = append(entries, ttfbuild.Entry{Tag: "vhea", Data: make([]byte, 36)})
				}
				data := ttfbuild.AssembleTablesOrdered(entries)
				data = data[:len(data)-(36-physical)]
				setRecordLength(data, len(entries)-1, physical) // kept, not dropped, by the directory rules
				path := textPDF(t, A, 200, withProgram(data))
				ref := pdfiumInk(t, path)
				if physical < 36 {
					wantRefused(t, path, which+" table has fewer than 36 bytes from its offset")
					if blank(ref) || sameInk(ref, squareRef) {
						t.Error("pdfium drew nothing or the fixture's own square; want a substituted face's glyph")
					}
					return
				}
				if px, _, _ := ink(renderNative(t, path, 72).Image); !sameInk(px, nativeSquare) {
					t.Error("this backend did not draw the fixture's own square")
				}
				if !sameInk(ref, squareRef) {
					t.Error("pdfium did not draw the fixture's own square")
				}
			})
		}
	}
}

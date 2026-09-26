package native

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/model-harness/pdftools/internal/ttfbuild"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// This file pins R#0 against pdfium (real FreeType, and unhinted at this size: 48pt at 288 dpi
// is 192 device pixels per em, past cfx_renderdevice.cpp:1243's 50, so pdfium draws through
// LoadGlyphPath with FT_LOAD_NO_HINTING — see TrueType.Outline's own comment): a glyph whose
// hmtx lsb disagrees with its glyf xMin is drawn shifted by TT_Load_Glyph's own translate
// (ttgload.c:2665-2670), and this
// backend has to model that shift, not just its own unshifted glyf coordinates. Every test draws
// 'A' at 48pt, 288 dpi — the size and resolution the round-4 reviewer's own probe (cmap-rev4/
// render/native/zzlsb288_test.go) found the corpus defect at — and compares the leftmost ink
// column, which is what a horizontal shift moves and a vertical one does not.

// leftInkColumn returns the smallest column any pixel in px (w wide) has ink in, or -1 for a
// blank page.
func leftInkColumn(px []byte, w int) int {
	col := -1
	for i, v := range px {
		if v <= 40 {
			continue
		}
		if x := i % w; col < 0 || x < col {
			col = x
		}
	}
	return col
}

// assertLeftInkWithinOnePixel renders path in this backend and in pdfium at dpi and fails unless
// their leftmost ink columns agree within 1 px — the tolerance the round-4 probe's own FIX288
// rows used, for the same reason: this package's scan converter and FreeType's rasterizer round a
// covered pixel's edge independently, so an exact match is not the claim, agreement on where the
// shift landed is.
func assertLeftInkWithinOnePixel(t *testing.T, path string, dpi float64) {
	t.Helper()
	nat := renderNative(t, path, dpi)
	npx, w, _ := ink(nat.Image)

	ref, err := pdfium.Open(path)
	if err != nil {
		t.Fatalf("pdfium unavailable, so this comparison cannot run: %v", err)
	}
	defer func() { _ = ref.Close() }()
	o := render.DefaultOptions
	o.DPI = dpi
	want, err := ref.Page(1, o)
	if err != nil {
		t.Fatalf("pdfium Page: %v", err)
	}
	ppx, _, _ := ink(want.Image)

	nCol, pCol := leftInkColumn(npx, w), leftInkColumn(ppx, w)
	if nCol < 0 || pCol < 0 {
		t.Fatalf("no ink drawn: native left column %d, pdfium %d", nCol, pCol)
	}
	if d := nCol - pCol; d < -1 || d > 1 {
		t.Errorf("left ink column: this backend %d, pdfium %d (differ by %d, want at most 1)",
			nCol, pCol, d)
	}
}

const drawA = `BT /F1 48 Tf 20 100 Td (A) Tj ET`

// TestLeftSideBearingShiftsTheOutlineAgainstPdfium pins R#0's core claim directly: the square's
// hmtx lsb (fixtureTables' gidSquare entry) set to 100, 0 and 200 against its own xMin of 100 —
// the fixture's default is the 100 case, "no shift" — moves the leftmost ink column by the same
// amount pdfium moves it, in either direction.
func TestLeftSideBearingShiftsTheOutlineAgainstPdfium(t *testing.T) {
	for _, lsb := range []int{100, 0, 200} {
		t.Run(fmt.Sprintf("lsb %d (xMin 100)", lsb), func(t *testing.T) {
			tb := fixtureTables(nil)
			hm := append([]byte(nil), tb["hmtx"]...)
			binary.BigEndian.PutUint16(hm[4*ttfbuild.GIDSquare+2:], uint16(int16(lsb)))
			tb["hmtx"] = hm
			path := textPDF(t, drawA, 200, withProgram(ttfbuild.AssembleTables(tb)))
			assertLeftInkWithinOnePixel(t, path, 288)
		})
	}
}

// TestNumberOfHMetricsZeroAgainstPdfium pins the k==0 branch of lsb: hhea's numberOfHMetrics
// zeroed leaves every glyph's lsb at 0 (tt_face_get_metrics' own NoData path), so the square —
// xMin 100 — is drawn 100 units left of its glyf coordinate, in both engines alike.
func TestNumberOfHMetricsZeroAgainstPdfium(t *testing.T) {
	tb := fixtureTables(nil)
	hh := append([]byte(nil), tb["hhea"]...)
	binary.BigEndian.PutUint16(hh[34:], 0) // numberOfHMetrics
	tb["hhea"] = hh
	path := textPDF(t, drawA, 200, withProgram(ttfbuild.AssembleTables(tb)))
	assertLeftInkWithinOnePixel(t, path, 288)
}

// TestLeftSideBearingBelowNumberOfHMetricsPastTableEndAgainstPdfium pins the i<k branch's own
// NoData path: numberOfHMetrics still declares 6 (every glyph, including the square), but hmtx
// itself is clamped to 4 bytes — room for gid 0's pair alone — so the square's own metric would
// start at byte 4 and run past the table's end. FreeType answers lsb 0 rather than reading past
// what is there (ttmtx.c:266-267, "goto NoData"), and pdfium draws through that same answer.
func TestLeftSideBearingBelowNumberOfHMetricsPastTableEndAgainstPdfium(t *testing.T) {
	tb := fixtureTables(nil)
	tb["hmtx"] = tb["hmtx"][:4] // only gid 0's advance/lsb pair remains; numberOfHMetrics stays 6
	path := textPDF(t, drawA, 200, withProgram(ttfbuild.AssembleTables(tb)))
	assertLeftInkWithinOnePixel(t, path, 288)
}

// hmtxLastOrdered reassembles tb's own tables (from fixtureTables) with hmtx as the last table in
// the file, declared 24 bytes (six glyphs' worth, this fixture's own hmtx length), and cut to
// leave exactly physical bytes past hmtx's own offset — the shape R#1 needs: a real embedded
// FontFile2 program whose hmtx runs past the end of the file, so tt_face_load_font_dir's own
// clamp (ttload.c:441-450) is what FreeType and this package both apply, not a table this test
// simply wrote short.
func hmtxLastOrdered(tb map[string][]byte, physical int) []byte {
	var entries []ttfbuild.Entry
	for _, tag := range []string{"OS/2", "cmap", "glyf", "head", "hhea", "loca", "maxp", "post"} {
		entries = append(entries, ttfbuild.Entry{Tag: tag, Data: tb[tag]})
	}
	entries = append(entries, ttfbuild.Entry{Tag: "hmtx", Data: tb["hmtx"]}) // 24 bytes, six pairs
	full := ttfbuild.AssembleTablesOrdered(entries)
	i := len(entries) - 1
	off := recordOffset(full, i)
	return full[:off+physical]
}

// recordOffset reads one table-directory record's offset field, the same way font's own tests do
// for the same reason: AssembleTablesOrdered's bookkeeping always agrees the record it emits with
// the bytes actually there, so cutting the file to an exact physical length needs the offset read
// back rather than recomputed by hand.
func recordOffset(data []byte, i int) int {
	return int(binary.BigEndian.Uint32(data[12+i*16+8:]))
}

// TestHmtxClampAgainstPdfium pins R#1 against pdfium directly: an hmtx declared 24 bytes, the
// last table in a real embedded FontFile2 program, with the file ending 1, 2 or 3 bytes into it
// is refused — FreeType's own clamp drives it to zero length and Hmtx_Table_Missing fails the
// face, so pdfium substitutes — and with 4 or 5 bytes left it is accepted and drawn, agreeing
// with pdfium on where the square lands (the clamped table's lsb for gid 0, the only pair the
// clamp leaves, not gid 1's own).
func TestHmtxClampAgainstPdfium(t *testing.T) {
	for _, physical := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("hmtx physical %d of 24 declared is refused", physical), func(t *testing.T) {
			tb := fixtureTables(nil)
			path := textPDF(t, drawA, 200, withProgram(hmtxLastOrdered(tb, physical)))
			wantRefused(t, path, "no hmtx table")
		})
	}
	for _, physical := range []int{4, 5} {
		t.Run(fmt.Sprintf("hmtx physical %d of 24 declared is drawn", physical), func(t *testing.T) {
			tb := fixtureTables(nil)
			path := textPDF(t, drawA, 200, withProgram(hmtxLastOrdered(tb, physical)))
			assertLeftInkWithinOnePixel(t, path, 288)
		})
	}
}

// hmtxWithK rewrites tb's hmtx and hhea to declare numberOfHMetrics k, keeping the first k
// advance/lsb pairs from the fixture's own hmtx and appending one lsb-only halfword per remaining
// glyph — the shape the trailing array has once a font's numberOfHMetrics is fewer than its
// glyphs, which every glyph gid >= k is read through (tt_face_get_metrics' own else branch).
// trailing, keyed by gid, overrides that glyph's own trailing entry; a gid with no override gets 0.
func hmtxWithK(tb map[string][]byte, k int, trailing map[int]int) {
	orig := tb["hmtx"]
	hm := append([]byte(nil), orig[:4*k]...)
	for gid := k; gid < ttfbuild.NumGlyphs; gid++ {
		hm = binary.BigEndian.AppendUint16(hm, uint16(int16(trailing[gid])))
	}
	tb["hmtx"] = hm
	hh := append([]byte(nil), tb["hhea"]...)
	binary.BigEndian.PutUint16(hh[34:], uint16(k))
	tb["hhea"] = hh
}

// TestLeftSideBearingTrailingArrayAgainstPdfium pins the i>=k branch reading the trailing lsb
// array: numberOfHMetrics 1, so the square (gid 1) reads its lsb from the first trailing
// halfword, set here to -16, rather than from a pair of its own.
func TestLeftSideBearingTrailingArrayAgainstPdfium(t *testing.T) {
	tb := fixtureTables(nil)
	hmtxWithK(tb, 1, map[int]int{ttfbuild.GIDSquare: -16})
	path := textPDF(t, drawA, 200, withProgram(ttfbuild.AssembleTables(tb)))
	assertLeftInkWithinOnePixel(t, path, 288)
}

// TestLeftSideBearingTrailingArrayTooShortAgainstPdfium pins the trailing array's own NoData path:
// numberOfHMetrics 1 with no trailing halfwords at all, so the square's lsb — which the previous
// test read from the first one — is 0, the same answer a font that never declared one gives.
func TestLeftSideBearingTrailingArrayTooShortAgainstPdfium(t *testing.T) {
	tb := fixtureTables(nil)
	hmtxWithK(tb, 1, nil)
	tb["hmtx"] = tb["hmtx"][:4] // the k pairs alone; no trailing entries follow
	path := textPDF(t, drawA, 200, withProgram(ttfbuild.AssembleTables(tb)))
	assertLeftInkWithinOnePixel(t, path, 288)
}

// compositeComponentFlags is the byte offset, within the fixture's own glyf table, of the flags
// field for the composite glyph's first (offset  0,0) or second (offset 400,200, half scale)
// component — found from loca rather than hardcoded, since ttfbuild pads every glyph's own bytes
// to a 4-byte boundary and a change to any earlier glyph would move it.
func compositeComponentFlags(tb map[string][]byte, second bool) int {
	loca := tb["loca"]
	start := int(binary.BigEndian.Uint32(loca[4*ttfbuild.GIDComposite:]))
	if second {
		return start + 10 + 8 // header (10) + component 1 (flags, gid, dx, dy: 8)
	}
	return start + 10 // header (10)
}

// fixtureTablesComposite is fixtureTables' own extraction loop, over a fixture built at upem
// whose cmap maps 'A' to the square and 'C' to the composite glyph — the default subtable
// (fixtureTables' own) sends 'A' through 'D' all to the square, which cannot draw a composite at
// all, and is built at unitsPerEm 1000 only, where pp1's conversion to 1/1000 em is the identity.
func fixtureTablesComposite(upem uint16) map[string][]byte {
	prog := ttfbuild.Builder{UnitsPerEm: upem, LongLoca: true, Cmaps: []ttfbuild.Cmap{{Platform: 3, Encoding: 1,
		Map: map[uint16]uint16{'A': ttfbuild.GIDSquare, 'C': ttfbuild.GIDComposite}}}}.Build()
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

// setLsb rewrites gid's own lsb in tb's hmtx, a copy so the fixture's other tables are untouched.
func setLsb(tb map[string][]byte, gid uint16, lsb int) {
	hm := append([]byte(nil), tb["hmtx"]...)
	binary.BigEndian.PutUint16(hm[4*int(gid)+2:], uint16(int16(lsb)))
	tb["hmtx"] = hm
}

// setComponent ORs flags into the composite's first or second component (see
// compositeComponentFlags) and, when gid is not 0, points that component at gid instead of the
// square.
func setComponent(tb map[string][]byte, second bool, flags uint16, gid uint16) {
	glyf := append([]byte(nil), tb["glyf"]...)
	at := compositeComponentFlags(tb, second)
	binary.BigEndian.PutUint16(glyf[at:], binary.BigEndian.Uint16(glyf[at:])|flags)
	if gid != 0 {
		binary.BigEndian.PutUint16(glyf[at+2:], gid)
	}
	tb["glyf"] = glyf
}

const drawC = `BT /F1 48 Tf 20 100 Td (C) Tj ET`

// TestCompositeWithoutUseMyMetricsAgainstPdfium pins the default shape: neither of the fixture's
// composite's two components carries USE_MY_METRICS, so the composite draws through its own pp1 —
// its own header xMin (100) against its own lsb — regardless of what the square it places twice,
// whose lsb this test detunes to 0, would resolve to on its own. With the composite's lsb at 100
// its own pp1 is 0 and nothing moves; with it at 0 its own pp1 is 100, and the whole composite
// moves left by that, 19 px at this size, which is what a composite that ignored its own bearing
// would miss.
func TestCompositeWithoutUseMyMetricsAgainstPdfium(t *testing.T) {
	for _, lsb := range []int{100, 0} {
		t.Run(fmt.Sprintf("the composite's own lsb %d (xMin 100)", lsb), func(t *testing.T) {
			tb := fixtureTablesComposite(1000)
			setLsb(tb, ttfbuild.GIDSquare, 0) // the component's own lsb, 0 against its xMin 100
			setLsb(tb, ttfbuild.GIDComposite, lsb)
			path := textPDF(t, drawC, 200, withProgram(ttfbuild.AssembleTables(tb)))
			assertLeftInkWithinOnePixel(t, path, 288)
		})
	}
}

// TestCompositeUseMyMetricsAgainstPdfium pins USE_MY_METRICS (0x0200): setting it on one of the
// composite's own two components — first at no offset, then at the fixture's own (400,200),
// half-scale placement — replaces the composite's own pp1 with that component's, resolved in the
// component's own coordinates and not adjusted by the offset or transform that places its ink
// (ttgload.c ~1840-1880). Both components are the same square (xMin 100), detuned here to lsb 0,
// so either flagged component shifts the composite by the same amount pdfium shifts it by, and
// the offset used has no bearing on how much. The last case flags both, with the second pointed at
// the diamond (lsb 200) instead: FreeType restores the saved phantom points only after a component
// without the flag, so the last flagged component's pp1 is the one left standing — the diamond's,
// 38 px right of the square's at this size.
func TestCompositeUseMyMetricsAgainstPdfium(t *testing.T) {
	for _, c := range []struct {
		name          string
		first, second bool // which component carries USE_MY_METRICS
	}{
		{"USE_MY_METRICS on the component at no offset", true, false},
		{"USE_MY_METRICS on the component at a non-zero offset", false, true},
		{"USE_MY_METRICS on both: the last one wins", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tb := fixtureTablesComposite(1000)
			setLsb(tb, ttfbuild.GIDSquare, 0) // component's own lsb, was 100
			if c.first {
				setComponent(tb, false, 0x0200, 0)
			}
			if c.second {
				gid := uint16(0)
				if c.first {
					gid = ttfbuild.GIDDiamond
					setLsb(tb, ttfbuild.GIDDiamond, 200)
				}
				setComponent(tb, true, 0x0200, gid)
			}
			path := textPDF(t, drawC, 200, withProgram(ttfbuild.AssembleTables(tb)))
			assertLeftInkWithinOnePixel(t, path, 288)
		})
	}
}

// TestLeftSideBearingAtAnotherUnitsPerEmAgainstPdfium pins pp1's conversion from font units to
// this package's 1/1000 em, which every test above leaves at the identity (unitsPerEm 1000). At
// 2048, the square's xMin 100 against lsb 0 is a pp1 of 100 font units, 48.8 thousandths of an
// em; drawn as 100 thousandths it would land 10 px left of pdfium at this size. The empty-glyph
// case converts the same way: the composite's second component pointed at the space — no outline,
// so its pp1 is -lsb alone (ttgload.c:1543-1549) — with USE_MY_METRICS and lsb -200, a pp1 of
// 200 that moves the composite's square left by 200 font units, not 200 thousandths.
func TestLeftSideBearingAtAnotherUnitsPerEmAgainstPdfium(t *testing.T) {
	t.Run("a simple glyph, lsb 0 against xMin 100", func(t *testing.T) {
		tb := fixtureTablesComposite(2048)
		setLsb(tb, ttfbuild.GIDSquare, 0)
		path := textPDF(t, drawA, 200, withProgram(ttfbuild.AssembleTables(tb)))
		assertLeftInkWithinOnePixel(t, path, 288)
	})
	t.Run("an empty USE_MY_METRICS component, lsb -200", func(t *testing.T) {
		tb := fixtureTablesComposite(2048)
		setLsb(tb, ttfbuild.GIDSpace, -200)
		setComponent(tb, true, 0x0200, ttfbuild.GIDSpace)
		path := textPDF(t, drawC, 200, withProgram(ttfbuild.AssembleTables(tb)))
		assertLeftInkWithinOnePixel(t, path, 288)
	})
}

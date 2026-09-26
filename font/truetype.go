package font

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// A TrueType font program, parsed far enough to hand out glyph outlines.
//
// # Why this is here and not in the rasterizer
//
// A glyph program is font data, and `font` is the package that reads font data. The rasterizer
// asks for an outline the way `extract` asks for a width, and neither knows what a `loca` table
// is. The alternative — a rasterizer that parses fonts — would put the same tables in two
// packages the first time anything else needed a glyph.
//
// # Why the bytes are a parameter rather than something Font holds
//
// Font is loaded once per font dictionary and cached for a document's life, by every consumer:
// `extract` holds one per reference for a thousand pages. A font *program* is tens of kilobytes
// subsetted and megabytes when it is not, and extraction never looks at one. Holding it on Font
// would make every text run pay for every outline nobody reads, and holding a Store on Font to
// load it later would tie a cached font's lifetime to a file handle. So the caller reads
// /FontFile2 when it wants outlines and parses it here, and caches the result where it knows how
// long it needs it.
//
// # What this parses, and what it refuses
//
// `head` for unitsPerEm and the `loca` format, `maxp` for the glyph count, `loca` and `glyf` for
// the outlines, `cmap` for the character lookups a simple font needs. Not `CFF ` — a
// FontFile3 program has cubic charstrings and its own interpreter, which is cff.go and
// charstring.go rather than a variation on this one. Not hinting: an outline is scaled and filled, which
// is what a 200-dpi page wants and what every renderer does above about 12 pixels per em.
type TrueType struct {
	data []byte

	unitsPerEm  float64
	numGlyphs   int
	longLoca    bool
	loca, glyf  []byte
	cmapSubtabs map[uint32][]byte // (platformID<<16 | encodingID) -> subtable
	postNames   bool              // a `post` table is present and names glyphs; see PostNamesGlyphs

	// hmtx and numberOfHMetrics feed lsb (tt_face_get_metrics, ttmtx.c:227-306): hmtx is the kept
	// table's own bytes — its declared length, or, only when it runs past the end of the file,
	// that length clamped to a multiple of 4 by the directory rule above; either way exactly the
	// directory length tt_face_load_hmtx stores as horz_metrics_size (ttmtx.c:95-99) — and
	// numberOfHMetrics is hhea's own field,
	// read once here because ParseTrueType has already proven 36 bytes are physically present at
	// hhea's offset.
	hmtx             []byte
	numberOfHMetrics int
}

// SegOp is what a segment of an outline does.
type SegOp uint8

const (
	// SegMove starts a contour at P[0].
	SegMove SegOp = iota
	// SegLine draws to P[0].
	SegLine
	// SegQuad draws a quadratic Bézier with control P[0] to P[1]. TrueType's native curve.
	SegQuad
	// SegCubic draws a cubic Bézier with controls P[0], P[1] to P[2]. Type 1 and CFF's native
	// curve, declared here so one outline type serves both and a rasterizer implements each
	// curve once.
	SegCubic
	// SegClose closes the current contour.
	SegClose
)

// Point is a position in glyph space.
type Point struct{ X, Y float64 }

// Seg is one segment of an outline.
type Seg struct {
	Op SegOp
	P  [3]Point
}

// Outline is a glyph's contours, in 1/1000 em with y up.
//
// The same units as Glyph.Width, and for the same reason its comment gives: a caller must not
// have to ask what kind of font it holds. TrueType's own units are whatever `head` says —
// usually 1000 or 2048 — and converting here rather than exporting unitsPerEm means a consumer
// cannot forget to, which is a silent scale error of a factor of two on half the fonts in the
// world.
type Outline []Seg

// ParseTrueType reads a TrueType or OpenType program far enough to yield outlines.
func ParseTrueType(data []byte) (*TrueType, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("font: truetype program is %d bytes", len(data))
	}
	t := &TrueType{data: data, unitsPerEm: 1000, cmapSubtabs: map[uint32][]byte{}}

	// tt_face_init accepts exactly five format tags (ft-truetype/ttobjs.c:693-701): 0x00010000,
	// the undocumented 0x00020000 some Arphic CJK fonts carry, Apple's 'true', and the two legacy
	// Mac dfont tags 0xA5kbd and 0xA5lst. Only 0x00010000 and 'true' are accepted below — the
	// other three are refused anyway, in the safe direction, since nothing in this repo's corpus
	// or the PDF spec calls for them. 'ttcf' is refused rather than guessed at: picking a face out
	// of a collection is a choice the font dictionary does not record. 'OTTO' is not in FreeType's
	// list at all: an OpenType CFF program belongs in FontFile3 and is read by cff.go, and a
	// FontFile2 program stamped 'OTTO' fails FreeType's TrueType driver exactly like any other
	// unrecognized tag — pdfium then substitutes a face rather than drawing this program's glyf,
	// however complete that table is.
	tag := be32(data, 0)
	switch tag {
	case 0x00010000, 0x74727565 /* 'true' */ :
	case 0x74746366 /* 'ttcf' */ :
		return nil, fmt.Errorf("font: truetype collection, which names no face to use")
	default:
		return nil, fmt.Errorf("font: not a truetype program (tag %#08x)", tag)
	}

	numTables := int(be16(data, 4))
	// One record per tag, holding both the kept entry's offset and its slice together, so the two
	// can never come from different entries: a mutant that moved one write outside the other's
	// `if _, dup := entries[name]; !dup` guard, updating the offset from every duplicate while the
	// slice stayed at the first, survived the whole suite the two-map version had (R#2's D4) —
	// there is no longer a second write to move.
	type sfntTable struct {
		off  int
		data []byte
	}
	entries := map[string]sfntTable{}
	validEntries := 0
	shortHead := false
	for i := 0; i < numTables; i++ {
		rec := 12 + i*16
		if rec+16 > len(data) {
			break
		}
		name := string(data[rec : rec+4])
		// Widened to int64 before any comparison, rather than the int this package reads
		// everything else as: be32 returns a uint32, and its widening to int64 is exact and
		// always non-negative on every GOARCH, where its widening to plain `int` is only exact on
		// a 64-bit build. On a 32-bit build (GOARCH=386) int is 32 bits, so int(be32(...)) for a
		// field at or past 1<<31 came back negative, and comparing or subtracting those wrapped
		// int values — off+length is the sum this package no longer computes, for exactly that
		// reason — could skip the bound below and hand a later slice a negative bound. int64 has
		// no such width to be a function of: off64 is bounded by len(data) two lines down, and
		// length64 by len(data)-off64 (never negative once off64 is, both ordinary int64
		// arithmetic, on 386 as much as on amd64), or the entry is dropped either way.
		off64, length64 := int64(be32(data, rec+8)), int64(be32(data, rec+12))
		if off64 > int64(len(data)) {
			continue
		}
		if length64 > int64(len(data))-off64 {
			// FreeType drops a table that runs past the end of the file — it is not refusing the
			// font, it is discarding one record from its directory — except hmtx and vmtx, which
			// check_table_dir (ttload.c:224-231) counts as valid anyway: "Some tables have such a
			// simple structure that clipping its contents is harmless", which is not true of head,
			// maxp, post, cmap, loca or glyf, every table this package actually reads. The clamp
			// itself — down to a multiple of 4 — is a separate step later, in tt_face_load_font_dir
			// (ttload.c:441-450: "entry.Length = ( stream->size - entry.Offset ) & ~3U").
			if name != "hmtx" && name != "vmtx" {
				continue
			}
			length64 = (int64(len(data)) - off64) &^ 3
		}
		// Both are now bounded by len(data), which itself has to fit in an int on this build (Go
		// bounds a slice's own length that way), so converting back to int here is exact.
		off, length := int(off64), int(length64)
		if (name == "head" || name == "bhed") && length < 0x36 {
			// check_table_dir (ttload.c:251-264) fails to load the face at all over this, rather
			// than merely dropping the table: "The table length should be 0x36, but certain font
			// tools make it 0x38, so we will just check that it is greater."
			shortHead = true
		}
		validEntries++
		// ttload.c:478-499, "ignore duplicate tables – the first one wins": the entry kept for a
		// tag is the first one that reaches here, even when its length is 0, so a later, real
		// table of the same name never displaces it.
		if _, dup := entries[name]; !dup {
			entries[name] = sfntTable{off, data[off : off+length]}
		}
	}
	if validEntries == 0 {
		// check_table_dir: "no valid tables found" fails the whole face.
		return nil, fmt.Errorf("font: truetype table directory has no valid entries")
	}
	if shortHead {
		return nil, fmt.Errorf("font: truetype head table is shorter than 0x36 bytes")
	}

	head := entries["head"].data
	if len(head) < 54 {
		// sfobjs.c:895-898, LOAD_( head ); if ( error ) goto Exit: a face with no head table
		// fails to load at all, and pdfium then substitutes a different face rather than drawing
		// this program's glyphs. (check_table_dir's own length<0x36 rule above already refuses a
		// too-short one before this is reached; this is the "tag never in the directory" half.)
		return nil, fmt.Errorf("font: truetype program has no head table")
	}
	// sfobjs.c:904-913: unitsPerEm outside this range fails the face, "OpenType 1.8.2 introduced
	// limits to this value; however, they make sense for older SFNT fonts also."
	u := be16(head, 18)
	if u < 16 || u > 16384 {
		return nil, fmt.Errorf("font: truetype unitsPerEm %d is outside FreeType's 16..16384", u)
	}
	t.unitsPerEm = float64(u)
	t.longLoca = be16(head, 50) == 1

	// hhea and hmtx (sfobjs.c:928-985, LOADM_(hhea,0) then LOADM_(hmtx,0)). tt_face_load_hhea
	// (ttmtx.c:129-194) reads a fixed 36 bytes from the *stream* at hhea's own offset, not
	// bounded by the table's declared length, so a present hhea is refused below only when the
	// file itself runs out before those 36 bytes do — the same distinction the post header check
	// below draws for its own 32 bytes.
	//
	// A missing hhea fails the *load* for every tag but the Mac 'true' one, which FreeType
	// tolerates by setting has_outline = 0 instead ("This is an SFNT Mac font.", sfobjs.c:960) —
	// and never even attempts to load hmtx for, since LOADM_(hmtx,0) sits inside the branch a
	// missing hhea skips entirely. That is FreeType's own return value, not what pdfium draws: a
	// face with has_outline == 0 has nothing for pdfium's font code to read glyf through either,
	// so it substitutes there exactly as it does for every other tag's Horiz_Header_Missing
	// (measured against real pdfium; see TestFaceLevelFailuresAgainstPdfium). So a missing hhea is
	// refused here for every tag, with no 'true' exemption — stricter than the FreeType return
	// value alone, in the safe direction, and truer to what pdfium actually draws.
	//
	// A missing hmtx fails the face regardless of tag (Hmtx_Table_Missing, sfobjs.c:940),
	// including one the directory rules' own hmtx clamp above has already driven to zero length.
	switch hheaLen, hheaOff := len(entries["hhea"].data), entries["hhea"].off; {
	case hheaLen == 0:
		return nil, fmt.Errorf("font: truetype program has no hhea table")
	case len(data)-hheaOff < 36:
		return nil, fmt.Errorf("font: truetype hhea table has fewer than 36 bytes from its offset")
	case len(entries["hmtx"].data) == 0:
		return nil, fmt.Errorf("font: truetype program has no hmtx table")
	}
	// The two facts lsb (below) needs, read now because the switch above has already proven hhea
	// has 36 bytes physically present at its offset and hmtx is non-empty: numberOfHMetrics is the
	// uint16 at hhea's offset+34 (tt_face_load_hhea, ttmtx.c:129-194, the same fixed stream read as
	// the 36-byte check above), and hmtx is the kept table's own bytes at their directory length
	// (clamped to a multiple of 4 only if the table ran past the end of the file).
	t.numberOfHMetrics = int(be16(data, entries["hhea"].off+34))
	t.hmtx = entries["hmtx"].data
	// vhea and vmtx (sfobjs.c:988-996, LOADM_(hhea,1) then LOADM_(hmtx,1)): the same 36-byte
	// stream read as hhea, but a missing vhea or a missing vmtx is tolerated either way —
	// "if ( error && FT_ERR_NEQ( error, Table_Missing ) ) goto Exit" only fails the face for an
	// error that is *not* Table_Missing, which a present-but-unreadable vhea is and an absent one
	// is not. vmtx's own presence is never checked at all: LOADM_(hmtx,1) only sets
	// vertical_info, which nothing here reads.
	if vheaLen, vheaOff := len(entries["vhea"].data), entries["vhea"].off; vheaLen > 0 && len(data)-vheaOff < 36 {
		return nil, fmt.Errorf("font: truetype vhea table has fewer than 36 bytes from its offset")
	}

	// maxp's numGlyphs, like hhea above, is read as 2 bytes at a fixed position in the *stream*
	// from the table's own offset (ttload.c:728-770, tt_face_load_maxp: goto_table seeks, then
	// FT_STREAM_READ_FIELDS with FT_FRAME_START(6) over a 4-byte version and a 2-byte numGlyphs),
	// not bounded by the declared length entries["maxp"].data is clamped to. A maxp table this repo's
	// directory rules would call present is not itself required — sfobjs.c:915-917 does not check
	// LOAD_(maxp)'s error — so a missing or too-short maxp simply leaves numGlyphs at its zero
	// value, which notFound already turns into "no glyph for any code." be16 answers 0 when fewer
	// than the frame's 6 bytes are left in the file, which is that failure.
	if len(entries["maxp"].data) > 0 {
		t.numGlyphs = int(be16(data, entries["maxp"].off+4))
	}
	// A `post` table other than format 3.0 is what sets FT_FACE_FLAG_GLYPH_NAMES
	// (sfobjs.c:1116-1118): tt_face_load_post succeeded and its version is not the one Apple
	// defines as "no PostScript name information here". tt_face_load_post reads a fixed 32-byte
	// header from the *stream* at the table's own offset (ttload.c:1330-1334, goto_table then
	// FT_STREAM_READ_FIELDS with FT_FRAME_START(32)), not bounded by the table's declared length —
	// so what is checked below is whether 32 bytes are physically present in the file from that
	// offset, not whether entries["post"].data (bounded by the directory rules above, which never
	// lengthens a table past what it declared) happens to be that long.
	//
	// Which grammar a given FreeType revision accepts beyond that header is deliberately not
	// modelled fully: ttpost.c is not vendored anywhere under C:/tmp/pdfium-src or
	// C:/tmp/pdfium-verify, so which formats pdfium's own FreeType revision loads names for cannot
	// be checked against the copy that actually runs pdfium — only against the 2024 FreeType sfnt
	// sources at cmap-rev2-ft/ttpost.c, which this package's own model is deliberately stricter
	// than in three named ways, each in the safe direction (the one place this is read,
	// render/native's ttRoute, uses it only to refuse a page — over-refusing never draws a wrong
	// glyph, it only enlarges a refusal FreeType would in fact draw through):
	//
	//   - Present (per the directory rules above) and not confirmed to be a 3.0 header is enough
	//     to count as naming glyphs, so FormatType 4.0, or 1.0 without exactly 258 glyphs — both
	//     of which ttload.c:1337-1341 (not vendored where pdfium runs from either) rejects outright
	//     — are read here as naming glyphs too.
	//   - load_post_names (ttpost.c:323-358) only extracts names for FormatType 2.0 or 2.5, and
	//     only when goto_table's own *declared* length is at least 34 bytes (`post_len < 34`,
	//     ttpost.c:340); a 2.0 or 2.5 table declared shorter than that loads no names at all, so
	//     FreeType's own synthesized charmap step below has nothing to synthesize from. This
	//     model does not draw that distinction — it is read from the 32-byte *physical* header
	//     alone — so a 2.0 or 2.5 table declared under 34 bytes, full header or not, still counts
	//     as naming glyphs here.
	//   - Even a fully loaded, correctly declared 2.0/2.5 table can name not one glyph FreeType's
	//     psnames module maps to a Unicode value; FT_CMap_New then fails with
	//     No_Unicode_Glyph_Name, which sfnt_load_face tolerates (sfobjs.c:1226-1229) by simply not
	//     synthesizing a charmap, rather than refusing the face — so pdfium falls through the rest
	//     of LoadGlyphMap to gid = code. This model has no way to check what a name maps to
	//     (that is the psnames module's own table, not read here) and reads any post naming any
	//     glyph as naming glyphs regardless.
	if post := entries["post"].data; len(post) > 0 {
		off := entries["post"].off
		t.postNames = off+32 > len(data) || be32(data, off) != 0x00030000
	}
	t.loca, t.glyf = entries["loca"].data, entries["glyf"].data
	// Emptiness rather than absence: a directory entry with a zero length is not nil, and a
	// program whose glyf table is zero bytes is exactly as unusable as one with no entry. The
	// first version tested for nil and accepted the empty form, which is what an OpenType
	// wrapper around CFF charstrings looks like from here.
	if len(t.glyf) == 0 || len(t.loca) < 4 {
		return nil, fmt.Errorf("font: truetype program has no glyf/loca outlines to read"+
			" (glyf %d bytes, loca %d): CFF outlines are read only from a bare FontFile3 program",
			len(t.glyf), len(t.loca))
	}
	t.parseCmap(entries["cmap"].data)
	return t, nil
}

// UnitsPerEm is the program's own grid, exposed for a caller that has to reconcile it with a
// /FontMatrix. Outlines are already converted, so a renderer does not need it.
func (t *TrueType) UnitsPerEm() float64 { return t.unitsPerEm }

// NumGlyphs is the glyph count `maxp` declares.
func (t *TrueType) NumGlyphs() int { return t.numGlyphs }

// parseCmap indexes the character-to-glyph subtables by platform and encoding.
//
// This models the set tt_face_build_cmaps (ttcmap.c:3780-3884) builds, not every record the
// table lists: FreeType skips a record whose offset is 0 or leaves less than a format+length
// header inside the table (ttcmap.c:3821), skips a record whose format it has no class for
// (ttcmap.c:3875-3879), and — for the four formats lookup reads — drops a subtable that fails
// its class's validator at FT_VALIDATE_DEFAULT (ttcmap.c:3838-3870), the level the sfnt driver
// always builds at. A dropped subtable is not repaired, it is simply gone, so a font with a
// broken (3,1) and a good one afterward is read through the good one, and a font with only the
// broken one has no (3,1) at all.
//
// The 'cmap' table's own version field is deliberately not checked: FreeType reads past it
// without looking (ttcmap.c:3794-3803, "this essentially means that a version format test is
// useless") because the OpenType spec leaves it at 0 even for fonts using the newer subtable
// formats, so there is nothing here to gate on either.
//
// The first *surviving* record for a pair wins, as it does in pdfium, which selects a subtable
// by taking the first record that names it and that FreeType actually kept.
func (t *TrueType) parseCmap(cm []byte) {
	if len(cm) < 4 {
		return
	}
	n := int(be16(cm, 2))
	for i := 0; i < n; i++ {
		rec := 4 + i*8
		if rec+8 > len(cm) {
			return
		}
		plat, enc := be16(cm, rec), be16(cm, rec+2)
		off := int(be32(cm, rec+4))
		// offset<=0 catches both the zero offset ttcmap.c:3821 skips and a value that reinterprets
		// as negative on a 32-bit build; off > len(cm)-2 is that same line's "offset <=
		// cmap_size - 2", the room a format+length header needs.
		if off <= 0 || off > len(cm)-2 {
			continue
		}
		sub := cm[off:]
		if !cmapSubtableSurvives(sub) {
			continue
		}
		key := uint32(plat)<<16 | uint32(enc)
		if _, dup := t.cmapSubtabs[key]; !dup {
			t.cmapSubtabs[key] = sub
		}
	}
}

// cmapSubtableSurvives reports whether tt_face_build_cmaps would keep a record's subtable: it
// needs a FreeType class for the format (tt_cmap_classes, ttcmap.c:3767) and, for the formats
// lookup can read, a pass of that class's validator at FT_VALIDATE_DEFAULT.
//
// Formats 0, 4, 6 and 12 are the ones lookup actually reads, so their validators are modelled
// below, each skipping the branches FT_VALIDATE_TIGHT and FT_VALIDATE_PARANOID gate (this
// package never asks for either). Formats 2, 8, 10, 13 and 14 have a FreeType class too — a real
// font's charmap is built and kept for them — but lookup answers every one of them "no glyph for
// code" regardless of their contents, so they are kept here unvalidated: a page routed through
// one is refused exactly as it would be if FreeType's own validator had dropped it, and exactly
// as it is when FreeType's validator keeps it.
func cmapSubtableSurvives(sub []byte) bool {
	if len(sub) < 2 {
		return false
	}
	switch be16(sub, 0) {
	case 0:
		return validateCmap0(sub)
	case 4:
		return validateCmap4(sub)
	case 6:
		return validateCmap6(sub)
	case 12:
		return validateCmap12(sub)
	case 2, 8, 10, 13, 14:
		return true
	default:
		return false
	}
}

// validateCmap0 models tt_cmap0_validate (ttcmap.c:99-131) at FT_VALIDATE_DEFAULT: the declared
// length must reach the 256-entry byte array and must not claim more than the table has left.
func validateCmap0(sub []byte) bool {
	if len(sub) < 4 {
		return false
	}
	length := int(be16(sub, 2))
	return length >= 262 && length <= len(sub)
}

// validateCmap4 models tt_cmap4_validate (ttcmap.c:900-1100) at FT_VALIDATE_DEFAULT.
//
// The declared length field is not read for this. At FT_VALIDATE_DEFAULT the validator corrects
// it to exactly what remains of the table either way it disagrees (ttcmap.c:917-935: too large is
// truncated down, "we try to correct this here"; too small is extended up, "this is easy to
// correct") — so every bound below is against len(sub) directly, which is what the corrected
// length always equals.
//
// segCountX2 is halved before anything else reads it (ttcmap.c:950, "num_segs /= 2"): FreeType
// lays out the ends/starts/deltas/offsets arrays, and bounds the table against them, using that
// halved count, not the raw field. Masking the low bit off is that same halving, done so the
// arithmetic below — which still doubles it back out to size each of the four arrays — never has
// to carry a fractional segment. An odd segCountX2 is not itself rejected at FT_VALIDATE_DEFAULT
// (only FT_VALIDATE_PARANOID checks it, ttcmap.c:943-948), so reading it raw is not merely a
// stricter bound: it lays out every array two bytes off from where FreeType puts it.
//
// A segment's start after its end is rejected outright, at every validation level (ttcmap.c:1012-
// 1013). Overlapping or backward segments are not: FreeType keeps them and flags the charmap
// instead — lookup's plain forward scan agrees with FreeType's own reading of a flagged charmap
// either way, so nothing here has to act on the flag (see the comment in lookup). An idRangeOffset
// of 0xFFFF, and one that lands before glyphIdArray or runs past the table, are both rejected too,
// except on the final segment when it is the {0xFFFF, 0xFFFF} sentinel a sloppy subsetter
// routinely leaves malformed (ttcmap.c:1046-1062, "we thus omit the test here").
func validateCmap4(sub []byte) bool {
	if len(sub) < 16 {
		return false
	}
	segX2 := int(be16(sub, 6)) &^ 1
	if len(sub) < 16+segX2*4 {
		return false
	}
	ends, starts := 14, 16+segX2
	ranges := starts + 2*segX2
	glyphIDs := ranges + segX2
	for i := 0; i < segX2; i += 2 {
		start, end := be16(sub, starts+i), be16(sub, ends+i)
		if start > end {
			return false
		}
		final := i == segX2-2 && start == 0xFFFF && end == 0xFFFF
		switch ro := int(be16(sub, ranges+i)); {
		case ro != 0 && ro != 0xFFFF:
			if !final {
				p := ranges + i + ro
				if p < glyphIDs || p+(int(end)-int(start)+1)*2 > len(sub) {
					return false
				}
			}
		case ro == 0xFFFF && !final:
			return false
		}
	}
	return true
}

// validateCmap6 models tt_cmap6_validate (ttcmap.c:1600-1635) at FT_VALIDATE_DEFAULT: the same
// shape as format 0's check, against a run of `count` entries rather than a fixed 256.
func validateCmap6(sub []byte) bool {
	if len(sub) < 10 {
		return false
	}
	length, count := int(be16(sub, 2)), int(be16(sub, 8))
	return length <= len(sub) && length >= 10+count*2
}

// validateCmap12 models tt_cmap12_validate (ttcmap.c:2279-2336) at FT_VALIDATE_DEFAULT: the
// groups must fit the declared length, and — at every level, not only FT_VALIDATE_TIGHT — must
// run in strictly increasing, non-overlapping order.
func validateCmap12(sub []byte) bool {
	if len(sub) < 16 {
		return false
	}
	length, numGroups := be32(sub, 4), be32(sub, 12)
	if int64(length) > int64(len(sub)) || length < 16 || (length-16)/12 < numGroups {
		return false
	}
	var last uint32
	for i := uint32(0); i < numGroups; i++ {
		g := 16 + int(i)*12
		start, end := be32(sub, g), be32(sub, g+4)
		if start > end {
			return false
		}
		if i > 0 && start <= last {
			return false
		}
		last = end
	}
	return true
}

// HasCmap reports whether the program carries any cmap subtable.
func (t *TrueType) HasCmap() bool { return len(t.cmapSubtabs) > 0 }

// PostNamesGlyphs reports whether the program's `post` table should be treated as naming its
// glyphs: present, per the directory rules ParseTrueType applies, and then either its 32-byte
// header cannot be confirmed at all — fewer than 32 bytes are physically readable from the
// table's own offset, so whether it declares FormatType 3.0 ("no PostScript name information
// here") is unknown — or the header that is readable declares some other FormatType. Only a
// present table with a confirmed 3.0 header answers false.
//
// A caller with no surviving cmap subtable needs this: FreeType synthesizes a Unicode charmap
// from those names when nothing else survived tt_face_build_cmaps (sfobjs.c:1206-1230), and reads
// a symbolic font's code through it before ever falling back to using the code as a glyph index.
func (t *TrueType) PostNamesGlyphs() bool { return t.postNames }

// HasSubtable reports whether the program carries a cmap subtable for platform and encoding.
func (t *TrueType) HasSubtable(platform, encoding uint16) bool {
	_, ok := t.cmapSubtabs[uint32(platform)<<16|uint32(encoding)]
	return ok
}

// GIDInSubtable looks code up in the cmap subtable for platform and encoding, and reports false
// for a program without one, or a code it maps to no glyph.
func (t *TrueType) GIDInSubtable(platform, encoding uint16, code uint32) (uint16, bool) {
	return t.lookup(t.cmapSubtabs[uint32(platform)<<16|uint32(encoding)], code)
}

// GIDForRune maps a character to a glyph through the font's own cmap.
//
// The Windows Unicode subtable first and the Macintosh Roman one second, which is the order
// §9.6.5.4 gives for a non-symbolic TrueType font: (3,1) is what every modern producer writes,
// and (1,0) is what an old one wrote instead.
func (t *TrueType) GIDForRune(r rune) (uint16, bool) {
	// A negative rune is not a character. Rejected here rather than converted, because as a
	// uint32 it becomes a value near four billion that matches no subtable and so would answer
	// "this font has no glyph for it" — the right answer by accident, from a comparison that
	// means nothing.
	if r < 0 {
		return 0, false
	}
	for _, key := range []uint32{3<<16 | 1, 3<<16 | 10, 0<<16 | 3, 0<<16 | 4} {
		if gid, ok := t.lookup(t.cmapSubtabs[key], uint32(r)); ok {
			return gid, true
		}
	}
	if r < 256 {
		if gid, ok := t.lookup(t.cmapSubtabs[1<<16|0], uint32(r)); ok {
			return gid, true
		}
	}
	return 0, false
}

// notFound folds FT_Get_Char_Index's own zeroing into every format's own "glyph 0 means no
// mapping" rule: a result at or past what `maxp` declares comes back zeroed too
// (ftobjs.c:3947-3949, "if ( result >= (FT_UInt)face->num_glyphs ) result = 0"), which is a
// glyph the font never sized outlines for and, to a caller, exactly as absent as glyph 0 is.
// One place for both, so every format below reads it the same way rather than four copies that
// can drift the way the idRangeOffset path's did, which only ever got the maxp half of this.
//
// No exemption for numGlyphs == 0: maxp is "often not present in embedded TrueType fonts within
// PDF documents" (sfobjs.c:915-917) and LOAD_( maxp )'s own error is never checked, so a missing
// or unreadable maxp leaves face->num_glyphs at 0, and ftobjs.c's `>=` zeroes every result
// against that — FT_Get_Char_Index finds nothing for any code, and pdfium draws nothing. gid, the
// parameter, is always > 0 by the check above once glyph 0 is excluded, so `>= 0` is exactly
// "always zeroed", the same answer FreeType gives.
func (t *TrueType) notFound(gid uint16) (uint16, bool) {
	if gid == 0 || int(gid) >= t.numGlyphs {
		return 0, false
	}
	return gid, true
}

// lookup reads one cmap subtable. Formats 0, 4, 6 and 12 cover every subtable in this repo's
// corpus and every one a PDF producer writes; an unknown format returns no glyph rather than a
// wrong one.
func (t *TrueType) lookup(sub []byte, c uint32) (uint16, bool) {
	if len(sub) < 4 {
		return 0, false
	}
	switch be16(sub, 0) {
	case 0:
		if len(sub) < 262 || c > 255 {
			return 0, false
		}
		return t.notFound(uint16(sub[6+c]))

	case 4:
		if c > 0xFFFF || len(sub) < 14 {
			return 0, false
		}
		// Halved the same way validateCmap4 halves it, and for the same reason: FreeType lays
		// out every array below from num_segs, not from the raw field (ttcmap.c:950, "num_segs
		// /= 2"; char_index reads it as "TT_PEEK_USHORT( p ) >> 1").
		segX2 := int(be16(sub, 6)) &^ 1
		ends, starts := 14, 14+segX2+2
		deltas, ranges := starts+segX2, starts+2*segX2
		if ranges+segX2 > len(sub) {
			return 0, false
		}
		// tt_cmap4_validate flags a charmap TT_CMAP_FLAG_UNSORTED when a segment's start or end
		// goes backward from the one before it (ttcmap.c:1019-1033), and FreeType keeps it
		// rather than dropping it. The flag only picks which of FreeType's own two search
		// algorithms runs; tt_cmap4_char_map_linear (ttcmap.c:1131-1221) walks segments in
		// declared order — stop once c is below a segment's start, match once c is at or below
		// its end — and the scan below is that same walk, so it agrees with FreeType on a
		// backward subtable exactly because it does not try to be cleverer than declared order.
		// (The OVERLAPPING flag, for a subtable that only overlaps without ever going backward,
		// picks tt_cmap4_char_map_binary's own search for the earliest such segment instead, but
		// that always lands on the same segment declared order would.)
		for i := 0; i < segX2; i += 2 {
			if uint32(be16(sub, ends+i)) < c {
				continue
			}
			start := uint32(be16(sub, starts+i))
			if c < start {
				return 0, false
			}
			ro := int(be16(sub, ranges+i))
			if ro == 0 {
				return t.notFound(uint16(c) + be16(sub, deltas+i))
			}
			// The offset is from the range's own slot, which is the one piece of format 4
			// that cannot be read without the specification in front of you.
			at := ranges + i + ro + 2*int(c-start)
			if at+2 > len(sub) {
				return 0, false
			}
			gid := be16(sub, at)
			if gid == 0 {
				return 0, false
			}
			return t.notFound(gid + be16(sub, deltas+i))
		}
		return 0, false

	case 6:
		if len(sub) < 10 {
			return 0, false
		}
		first, count := uint32(be16(sub, 6)), uint32(be16(sub, 8))
		if c < first || c >= first+count {
			return 0, false
		}
		at := 10 + 2*int(c-first)
		if at+2 > len(sub) {
			return 0, false
		}
		return t.notFound(be16(sub, at))

	case 12:
		if len(sub) < 16 {
			return 0, false
		}
		n := int(be32(sub, 12))
		for i := 0; i < n; i++ {
			g := 16 + i*12
			if g+12 > len(sub) {
				return 0, false
			}
			lo, hi := be32(sub, g), be32(sub, g+4)
			if c < lo {
				return 0, false
			}
			if c > hi {
				continue
			}
			// Format 12 is the only one whose arithmetic is 32-bit while a glyph index is 16, so
			// it is the only one where the sum can exceed what a glyph index holds. Rejected
			// rather than truncated: truncation aliases onto a *valid* index and draws the wrong
			// glyph, where refusing draws none and refuses the page.
			g32 := be32(sub, g+8) + (c - lo)
			if g32 > 0xFFFF {
				return 0, false
			}
			return t.notFound(uint16(g32))
		}
	}
	return 0, false
}

// maxComposite bounds recursion into composite glyphs.
//
// Five, because a composite of composites is legal and a cycle is not detectable from a glyph
// index alone: a subsetter that rewrites indices can produce one that points at itself. Real
// composites are one level — an accented letter over a base — and two at the most.
const maxComposite = 5

// maxComponents and maxGlyphSegments bound what one composite costs across its whole tree.
//
// Depth alone does not: a component record is six bytes, so one glyph can name thousands of
// components, each of which can name thousands more, and five levels of that is work no page
// finishes. Real composites visit a handful of glyphs and draw a few hundred segments, so both
// bounds are far from anything a font means.
const (
	maxComponents    = 1024
	maxGlyphSegments = 1 << 16
)

// lsb is a glyph's left side bearing, read from hmtx exactly as tt_face_get_metrics reads it
// (ttmtx.c:227-306, vertical false): the last entry's advance/bearing pair is reused for every
// glyph index at or past numberOfHMetrics, and any read that would run past hmtx's own end — the
// NoData label at ttmtx.c:296-300 — answers 0 rather than failing the glyph. t.hmtx is already
// bounded to the kept table's length, and s16 answers 0 for a read past its end, which is each of
// FreeType's NoData bounds: a pair's four bytes end where its bearing's two do, and the last
// pair's advance, which FreeType reads before a trailing bearing, ends before that bearing starts.
func (t *TrueType) lsb(gid uint16) int {
	k := t.numberOfHMetrics
	if k == 0 {
		return 0
	}
	i := int(gid)
	if i < k {
		return s16(t.hmtx, 4*i+2)
	}
	return s16(t.hmtx, 4*k+2*(i-k))
}

// ttRun is what one Outline call spends: every glyph outline() enters, composite and simple
// alike, and work charged against the caller's budget, both shared across every component a
// composite's tree visits — the way charRun shares a CFF glyph's operations across its seac
// components.
type ttRun struct {
	visits, work, budget int
}

// charge adds n to the work this run has spent, and reports ErrGlyphBudget once that passes the
// budget its caller gave.
func (r *ttRun) charge(n int) error {
	r.work += n
	if r.work > r.budget {
		return ErrGlyphBudget
	}
	return nil
}

// Outline returns a glyph's contours in 1/1000 em, and how much work it took.
//
// budget bounds that work the way CFF.Outline's bounds a charstring's: 1 for every glyph visited,
// composite components included, plus a simple glyph's own contour count and its point count,
// charged whether or not those points end up drawing a segment — the contour count first, and
// before a byte of the contour ends is read, so a corrupt count cannot size an allocation for
// free. Past the budget the answer is ErrGlyphBudget, wrapped so errors.Is works.
//
// An empty glyph and an unreadable one are different answers. A space has an entry in `loca`
// with zero length, which is not ink and not an error, so it is nil with no error. A glyph whose
// data is malformed, or that places a component by point matching, is an error: drawing nothing
// there would be a silent gap where the font meant a letter, so the caller refuses instead.
//
// The outline comes back translated by -pp1.x, the way TT_Load_Glyph (ttgload.c:2665-2670)
// translates the assembled outline once, at the top, so "(0,0) is the glyph's origin" regardless
// of the gap between where glyf places it and where hmtx says its left edge is. pp1 is resolved
// by outline() in the same traversal that builds the outline — see its comment — so this is the
// only place the shift is applied; a component is never shifted inside the recursion. The
// translate does not depend on hinting (TT_Load_Glyph applies it either way), but its amount
// does: this is the unhinted design-space shift, which is what pdfium draws above 50 device
// pixels per em — cfx_renderdevice.cpp:1243 sends that text to DrawTextPath, whose LoadGlyphPath
// loads every non-tricky font with FT_LOAD_NO_HINTING (cfx_face.cpp:1268-1271). At 50 or fewer,
// pdfium rasterizes through RenderGlyph instead, which loads a TrueType font hinted
// (cfx_face.cpp:1145-1147), and hinting rounds pp1 to the pixel grid before the translate; nothing
// in this package rounds anything, so a small glyph can land up to a pixel from where pdfium
// puts it.
func (t *TrueType) Outline(gid uint16, budget int) (Outline, int, error) {
	r := &ttRun{budget: budget}
	out, pp1x, err := t.outline(gid, 0, r)
	if err != nil {
		return nil, r.work, fmt.Errorf("font: truetype glyph %d: %w", gid, err)
	}
	if pp1x != 0 {
		for i := range out {
			for j := range out[i].P {
				out[i].P[j].X -= pp1x
			}
		}
	}
	return out, r.work, nil
}

var (
	errTruncatedGlyph = errors.New("glyph data is truncated")
	errPointMatching  = errors.New("a component is placed by point matching, which is declined")
)

// maxGlyphPoints bounds one simple glyph, far above any real one, so a corrupt end point cannot
// size the allocations.
const maxGlyphPoints = 10000

// outline resolves one glyph's outline and its own pp1.x (tt_loader_set_pp, ttgload.c:1346-1350),
// in the same traversal so the depth, visit and work budgets above already govern it — a second
// walk to find pp1 would spend a glyph's budget twice.
//
// pp1.x is xMin - lsb(gid), xMin being the glyph header's own int16 field, except for an empty
// glyph — no bytes at all (a zero-length loca entry) or a simple glyph declaring zero contours —
// whose bbox ttgload.c:1543-1549 zeroes before tt_get_metrics ever runs, so its xMin is 0 while
// its lsb is read exactly as any other glyph's is. A composite starts from its own header's xMin
// and its own lsb the same way; composite (below) may replace that with a component's own pp1.
// The returned Outline is never shifted by the pp1 this call resolves — only the caller, either
// composite assembling a parent or Outline() at the top, applies a shift, and only once.
func (t *TrueType) outline(gid uint16, depth int, r *ttRun) (Outline, float64, error) {
	if depth > maxComposite {
		return nil, 0, fmt.Errorf("composite glyphs nest more than %d deep", maxComposite)
	}
	if r.visits++; r.visits > maxComponents {
		return nil, 0, fmt.Errorf("the composite visits more than %d glyphs", maxComponents)
	}
	if err := r.charge(1); err != nil {
		return nil, 0, err
	}
	scale := 1000 / t.unitsPerEm
	g, err := t.glyphData(gid)
	if err != nil {
		return nil, 0, err
	}
	if g == nil {
		return nil, -float64(t.lsb(gid)) * scale, nil
	}
	if len(g) < 10 {
		return nil, 0, errTruncatedGlyph
	}
	n := s16(g, 0)
	xMin := float64(s16(g, 2))
	if n == 0 {
		xMin = 0
	}
	pp1 := (xMin - float64(t.lsb(gid))) * scale
	if n < 0 {
		return t.composite(g[10:], depth, r, pp1)
	}
	out, err := t.simple(g[10:], n, r)
	return out, pp1, err
}

// glyphData slices one glyph out of `glyf` using `loca`: nil for an empty entry, and an error for
// one that lies outside the tables.
func (t *TrueType) glyphData(gid uint16) ([]byte, error) {
	i := int(gid)
	if t.numGlyphs > 0 && i >= t.numGlyphs {
		return nil, fmt.Errorf("glyph %d is past the %d glyphs maxp declares", i, t.numGlyphs)
	}
	var from, to int
	if t.longLoca {
		if (i+2)*4 > len(t.loca) {
			return nil, fmt.Errorf("glyph %d is past the end of loca", i)
		}
		from, to = int(be32(t.loca, i*4)), int(be32(t.loca, (i+1)*4))
	} else {
		if (i+2)*2 > len(t.loca) {
			return nil, fmt.Errorf("glyph %d is past the end of loca", i)
		}
		// The short form stores offsets halved, which is the only reason `head` has to say
		// which form is in use.
		from, to = int(be16(t.loca, i*2))*2, int(be16(t.loca, (i+1)*2))*2
	}
	if from == to {
		return nil, nil // an empty entry is a glyph with no ink, such as a space
	}
	if from > to || to > len(t.glyf) {
		return nil, fmt.Errorf("glyph %d has loca offsets %d..%d in a glyf of %d bytes",
			i, from, to, len(t.glyf))
	}
	return t.glyf[from:to], nil
}

// simple reads a glyph's own contours: the on-and-off-curve point list §5 of the TrueType
// specification describes, turned into segments.
//
// The quadratic rule is the part worth stating. Two consecutive off-curve points imply an
// on-curve point midway between them, so a contour of all off-curve points — which is how a
// circle is drawn — has twice as many segments as points. Missing that implied midpoint draws
// every round glyph as a polygon of its control points, which is visibly wrong and easy not to
// notice on a straight-sided one.
func (t *TrueType) simple(g []byte, contours int, r *ttRun) (Outline, error) {
	if contours == 0 {
		return nil, nil
	}
	// Charged before the length check below, and so before ends is allocated or a byte of it is
	// read: a corrupt contour count large enough to be refused by neither the length check nor
	// maxGlyphPoints still sizes an allocation and a two-byte read per contour, and the budget has
	// to see that cost even when the bytes describing the contours turn out not to be there.
	if err := r.charge(contours); err != nil {
		return nil, err
	}
	if len(g) < contours*2+2 {
		return nil, errTruncatedGlyph
	}
	ends := make([]int, contours)
	for i := range ends {
		ends[i] = int(be16(g, i*2))
		if i > 0 && ends[i] <= ends[i-1] {
			return nil, fmt.Errorf("contour end points %d then %d do not increase", ends[i-1], ends[i])
		}
	}
	npts := ends[contours-1] + 1
	if npts > maxGlyphPoints {
		return nil, fmt.Errorf("%d points, past the limit of %d", npts, maxGlyphPoints)
	}
	// Charged here, right after the point count is known and before a byte of flags or
	// coordinates is read: a contour of all one-point rings decodes this many points and draws
	// zero segments, and the page budget has to see the decode, not the empty result.
	if err := r.charge(npts); err != nil {
		return nil, err
	}
	p := contours * 2
	insLen := int(be16(g, p))
	p += 2 + insLen
	if p > len(g) {
		return nil, errTruncatedGlyph
	}

	// Flags, run-length encoded by the repeat bit.
	flags := make([]byte, 0, npts)
	for len(flags) < npts {
		if p >= len(g) {
			return nil, errTruncatedGlyph
		}
		f := g[p]
		p++
		flags = append(flags, f)
		if f&0x08 != 0 { // repeat
			if p >= len(g) {
				return nil, errTruncatedGlyph
			}
			r := int(g[p])
			p++
			for i := 0; i < r && len(flags) < npts; i++ {
				flags = append(flags, f)
			}
		}
	}

	xs := make([]float64, npts)
	v := 0
	for i, f := range flags {
		switch {
		case f&0x02 != 0: // short
			if p >= len(g) {
				return nil, errTruncatedGlyph
			}
			d := int(g[p])
			p++
			if f&0x10 == 0 {
				d = -d
			}
			v += d
		case f&0x10 == 0: // long
			if p+2 > len(g) {
				return nil, errTruncatedGlyph
			}
			v += s16(g, p)
			p += 2
		}
		xs[i] = float64(v)
	}
	ys := make([]float64, npts)
	v = 0
	for i, f := range flags {
		switch {
		case f&0x04 != 0:
			if p >= len(g) {
				return nil, errTruncatedGlyph
			}
			d := int(g[p])
			p++
			if f&0x20 == 0 {
				d = -d
			}
			v += d
		case f&0x20 == 0:
			if p+2 > len(g) {
				return nil, errTruncatedGlyph
			}
			v += s16(g, p)
			p += 2
		}
		ys[i] = float64(v)
	}

	scale := 1000 / t.unitsPerEm
	at := func(i int) (Point, bool) {
		return Point{xs[i] * scale, ys[i] * scale}, flags[i]&0x01 != 0
	}

	var out Outline
	start := 0
	for _, end := range ends {
		out = appendContour(out, start, end, at)
		start = end + 1
	}
	return out, nil
}

// appendContour turns one contour's points into segments.
//
// A contour is a ring of points, each on or off the curve, and the walk has to start at an
// on-curve one — so the ring is rotated to begin at the first on-curve point, and when there is
// none the start is the midpoint implied between the last point and the first. Every point is
// then consumed exactly once.
//
// Rotating rather than special-casing the start is what fixes a contour of *all* off-curve
// points, which is how a round glyph is drawn. Beginning the walk one point in — the obvious
// reading, since the start was computed from the last and first points — skips the curve whose
// control is the first point and adds a degenerate one at the end. The shape that produces is
// right for three quarters of its circumference and cut off across the fourth, which is exactly
// what it looked like.
func appendContour(out Outline, start, end int, at func(int) (Point, bool)) Outline {
	n := end - start + 1
	if n < 2 {
		return out
	}
	pt := func(i int) (Point, bool) { return at(start + ((i%n)+n)%n) }

	// Rotate to the first on-curve point, or to the implied midpoint before point 0.
	from := 0
	first, onCurve := pt(0)
	for i := 0; i < n; i++ {
		if p, on := pt(i); on {
			first, from = p, i+1
			onCurve = true
			break
		}
	}
	if !onCurve {
		a, _ := pt(n - 1)
		b, _ := pt(0)
		first, from = mid(a, b), 0
	}
	out = append(out, Seg{Op: SegMove, P: [3]Point{first}})

	var ctrl Point
	haveCtrl := false
	for k := 0; k < n; k++ {
		p, on := pt(from + k)
		switch {
		case on && !haveCtrl:
			out = append(out, Seg{Op: SegLine, P: [3]Point{p}})
		case on && haveCtrl:
			out = append(out, Seg{Op: SegQuad, P: [3]Point{ctrl, p}})
			haveCtrl = false
		case !on && !haveCtrl:
			ctrl, haveCtrl = p, true
		default:
			// Two off-curve points in a row imply an on-curve point midway between them,
			// which is the rule that makes a circle four quadratics rather than a diamond of
			// its control points.
			out = append(out, Seg{Op: SegQuad, P: [3]Point{ctrl, mid(ctrl, p)}})
			ctrl = p
		}
	}
	if haveCtrl {
		out = append(out, Seg{Op: SegQuad, P: [3]Point{ctrl, first}})
	}
	return append(out, Seg{Op: SegClose})
}

func mid(a, b Point) Point { return Point{(a.X + b.X) / 2, (a.Y + b.Y) / 2} }

// composite assembles a glyph out of others, each placed by its own transform.
//
// Only the offset and 2×2 forms, which is what a font builder emits for an accented letter. The
// point-matching form — where a component is positioned by making two point indices coincide —
// is declined, because placing it wrongly moves an accent somewhere plausible and wrong. A
// component that cannot be read fails the whole glyph: an accented letter drawn without its
// accent is a different letter.
//
// pp1 is the composite's own phantom point, resolved by outline() before this is called from the
// composite's own header and its own lsb; USE_MY_METRICS (0x0200) replaces it with a component's
// own pp1 once that component has been loaded, so the *last* component carrying the flag wins
// (ttgload.c ~1870-1877, "restore phantom points ... if !(subglyph->flags & USE_MY_METRICS)" —
// read the other way around, a flagged component's pp1 survives past the restore). The component
// is placed by dx, dy and the 2×2 transform below either way; its pp1 is not, because pp1 is a
// glyph-space X coordinate, not a point on the outline, and USE_MY_METRICS names it in the
// component's own coordinates (ttgload.c ~1840-1880).
func (t *TrueType) composite(g []byte, depth int, r *ttRun, pp1 float64) (Outline, float64, error) {
	var out Outline
	p := 0
	for {
		if p+4 > len(g) {
			return nil, 0, errTruncatedGlyph
		}
		flags := be16(g, p)
		sub := be16(g, p+2)
		p += 4

		var dx, dy float64
		if flags&0x0001 != 0 { // ARG_1_AND_2_ARE_WORDS
			if p+4 > len(g) {
				return nil, 0, errTruncatedGlyph
			}
			if flags&0x0002 == 0 { // ARGS_ARE_XY_VALUES unset
				return nil, 0, errPointMatching
			}
			dx, dy = float64(s16(g, p)), float64(s16(g, p+2))
			p += 4
		} else {
			if p+2 > len(g) {
				return nil, 0, errTruncatedGlyph
			}
			if flags&0x0002 == 0 {
				return nil, 0, errPointMatching
			}
			dx, dy = float64(s8(g, p)), float64(s8(g, p+1))
			p += 2
		}

		a, b, c, d := 1.0, 0.0, 0.0, 1.0
		switch {
		case flags&0x0008 != 0: // WE_HAVE_A_SCALE
			if p+2 > len(g) {
				return nil, 0, errTruncatedGlyph
			}
			a = f2dot14(be16(g, p))
			d = a
			p += 2
		case flags&0x0040 != 0: // X_AND_Y_SCALE
			if p+4 > len(g) {
				return nil, 0, errTruncatedGlyph
			}
			a, d = f2dot14(be16(g, p)), f2dot14(be16(g, p+2))
			p += 4
		case flags&0x0080 != 0: // TWO_BY_TWO
			if p+8 > len(g) {
				return nil, 0, errTruncatedGlyph
			}
			a, b, c, d = f2dot14(be16(g, p)), f2dot14(be16(g, p+2)),
				f2dot14(be16(g, p+4)), f2dot14(be16(g, p+6))
			p += 8
		}

		part, partPP1, err := t.outline(sub, depth+1, r)
		if err != nil {
			return nil, 0, fmt.Errorf("component glyph %d: %w", sub, err)
		}
		if flags&0x0200 != 0 { // USE_MY_METRICS
			pp1 = partPP1
		}
		// The offset is in font units and the part is already in 1/1000 em, so the offset is
		// converted here rather than the part being converted twice.
		scale := 1000 / t.unitsPerEm
		ox, oy := dx*scale, dy*scale
		for _, seg := range part {
			for i := range seg.P {
				x, y := seg.P[i].X, seg.P[i].Y
				seg.P[i] = Point{a*x + c*y + ox, b*x + d*y + oy}
			}
			out = append(out, seg)
		}
		if len(out) > maxGlyphSegments {
			return nil, 0, fmt.Errorf("the composite draws more than %d segments", maxGlyphSegments)
		}
		if flags&0x0020 == 0 { // MORE_COMPONENTS
			break
		}
	}
	return out, pp1, nil
}

// f2dot14 reads TrueType's fixed-point scale: one sign bit, one integer bit, fourteen fraction.
func f2dot14(v uint16) float64 {
	// A signed 2.14 fixed-point scale: one sign bit, one integer bit, fourteen fraction.
	return float64(int16(v)) / 16384 // #nosec G115 -- the field is defined signed
}

// s16 and s8 read TrueType's signed fields.
//
// Several are defined signed and the two's-complement reinterpretation *is* the format's rule: a
// glyph's coordinate deltas, a composite component's offsets, and `numberOfContours`, whose
// negative value is what marks a glyph composite in the first place. Named here rather than
// written out at each site so the intent is stated once and a scanner's overflow warning is
// answered once, instead of six times in the middle of the parsing.
func s16(b []byte, i int) int {
	return int(int16(be16(b, i))) // #nosec G115 -- the field is defined signed; see above
}

func s8(b []byte, i int) int {
	if i < 0 || i >= len(b) {
		return 0
	}
	return int(int8(b[i])) // #nosec G115 -- the field is defined signed; see above
}

func be16(b []byte, i int) uint16 {
	if i+2 > len(b) || i < 0 {
		return 0
	}
	return binary.BigEndian.Uint16(b[i:])
}

func be32(b []byte, i int) uint32 {
	if i+4 > len(b) || i < 0 {
		return 0
	}
	return binary.BigEndian.Uint32(b[i:])
}

// GIDForCID maps a composite font's CID to a glyph index.
//
// /CIDToGIDMap is a name or a stream (§9.7.4.2). Identity — the only defined name, and what all
// 67 CIDFontType2 fonts in this repo's corpus use — makes the two equal. A stream is a big-endian
// array of glyph indices, and a CID past its end has no glyph rather than glyph 0: glyph 0 is a
// real glyph, the one a font draws for "not found", so returning it would silently put a box on
// the page where the font said nothing.
func (f *Font) GIDForCID(cid uint32) (uint16, bool) {
	if f.cidToGID == nil {
		if cid > 0xFFFF {
			return 0, false
		}
		return uint16(cid), true
	}
	at := int(cid) * 2
	if at+2 > len(f.cidToGID) {
		return 0, false
	}
	return be16(f.cidToGID, at), true
}

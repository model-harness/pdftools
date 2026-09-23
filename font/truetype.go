package font

import (
	"encoding/binary"
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
// FontFile3/OpenType program has cubic charstrings and its own interpreter, which is the next
// increment and not a variation on this one. Not hinting: an outline is scaled and filled, which
// is what a 200-dpi page wants and what every renderer does above about 12 pixels per em.
type TrueType struct {
	data []byte

	unitsPerEm  float64
	numGlyphs   int
	longLoca    bool
	loca, glyf  []byte
	cmapSubtabs map[uint32][]byte // (platformID<<16 | encodingID) -> subtable
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

	// A TrueType collection or an OpenType wrapper both start with a tag this handles by
	// reading the table directory that follows it. 'ttcf' is refused rather than guessed at:
	// picking a face out of a collection is a choice the font dictionary does not record.
	switch tag := be32(data, 0); tag {
	case 0x00010000, 0x74727565 /* 'true' */, 0x4F54544F /* 'OTTO' */ :
	case 0x74746366 /* 'ttcf' */ :
		return nil, fmt.Errorf("font: truetype collection, which names no face to use")
	default:
		return nil, fmt.Errorf("font: not a truetype program (tag %#08x)", tag)
	}

	numTables := int(be16(data, 4))
	tables := map[string][]byte{}
	for i := 0; i < numTables; i++ {
		rec := 12 + i*16
		if rec+16 > len(data) {
			break
		}
		name := string(data[rec : rec+4])
		off, length := int(be32(data, rec+8)), int(be32(data, rec+12))
		if off < 0 || length < 0 || off > len(data) {
			continue
		}
		// A length running past the end is clamped rather than rejected. A subsetter that
		// truncates the last table is common enough that refusing the font would lose a page
		// over bytes no glyph in it needs.
		if off+length > len(data) {
			length = len(data) - off
		}
		tables[name] = data[off : off+length]
	}

	head := tables["head"]
	if len(head) >= 54 {
		if u := be16(head, 18); u != 0 {
			t.unitsPerEm = float64(u)
		}
		t.longLoca = be16(head, 50) == 1
	}
	if maxp := tables["maxp"]; len(maxp) >= 6 {
		t.numGlyphs = int(be16(maxp, 4))
	}
	t.loca, t.glyf = tables["loca"], tables["glyf"]
	// Emptiness rather than absence: a directory entry with a zero length is not nil, and a
	// program whose glyf table is zero bytes is exactly as unusable as one with no entry. The
	// first version tested for nil and accepted the empty form, which is what an OpenType
	// wrapper around CFF charstrings looks like from here.
	if len(t.glyf) == 0 || len(t.loca) < 4 {
		return nil, fmt.Errorf("font: truetype program has no glyf/loca outlines to read"+
			" (glyf %d bytes, loca %d): a CFF program needs a charstring interpreter",
			len(t.glyf), len(t.loca))
	}
	t.parseCmap(tables["cmap"])
	return t, nil
}

// UnitsPerEm is the program's own grid, exposed for a caller that has to reconcile it with a
// /FontMatrix. Outlines are already converted, so a renderer does not need it.
func (t *TrueType) UnitsPerEm() float64 { return t.unitsPerEm }

// NumGlyphs is the glyph count `maxp` declares.
func (t *TrueType) NumGlyphs() int { return t.numGlyphs }

// parseCmap indexes the character-to-glyph subtables by platform and encoding.
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
		if off >= len(cm) {
			continue
		}
		t.cmapSubtabs[uint32(plat)<<16|uint32(enc)] = cm[off:]
	}
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

// GIDForCode maps a character *code* to a glyph through the symbolic subtable.
//
// §9.6.5.4's rule for a symbolic font: look the code up in the (3,0) subtable, and because such
// a font's cmap is conventionally keyed in the Private Use Area, try the code both as written
// and offset into 0xF000. A dingbat font is the common case and its codes mean nothing outside
// the font, which is why this path exists at all rather than going through a character.
func (t *TrueType) GIDForCode(code uint32) (uint16, bool) {
	sym := t.cmapSubtabs[3<<16|0]
	for _, key := range []uint32{code, 0xF000 | (code & 0xFF)} {
		if gid, ok := t.lookup(sym, key); ok {
			return gid, true
		}
	}
	// A symbolic font with a (1,0) subtable keys it by code directly.
	if gid, ok := t.lookup(t.cmapSubtabs[1<<16|0], code); ok {
		return gid, true
	}
	return 0, false
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
		gid := uint16(sub[6+c])
		return gid, gid != 0

	case 4:
		if c > 0xFFFF || len(sub) < 14 {
			return 0, false
		}
		segX2 := int(be16(sub, 6))
		ends, starts := 14, 14+segX2+2
		deltas, ranges := starts+segX2, starts+2*segX2
		if ranges+segX2 > len(sub) {
			return 0, false
		}
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
				gid := uint16(c) + be16(sub, deltas+i)
				return gid, gid != 0
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
			return gid + be16(sub, deltas+i), true
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
		gid := be16(sub, at)
		return gid, gid != 0

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
			gid := uint16(g32)
			return gid, gid != 0
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

// Outline returns a glyph's contours in 1/1000 em, or false when the glyph has none.
//
// An empty outline and a missing glyph are different answers and both are "false, nothing to
// draw": a space has an entry in `loca` with zero length, which is not an error and not ink.
func (t *TrueType) Outline(gid uint16) (Outline, bool) {
	return t.outline(gid, 0)
}

func (t *TrueType) outline(gid uint16, depth int) (Outline, bool) {
	if depth > maxComposite {
		return nil, false
	}
	g, ok := t.glyphData(gid)
	if !ok || len(g) < 10 {
		return nil, false
	}
	n := s16(g, 0)
	if n < 0 {
		return t.composite(g[10:], depth)
	}
	return t.simple(g[10:], n)
}

// glyphData slices one glyph out of `glyf` using `loca`.
func (t *TrueType) glyphData(gid uint16) ([]byte, bool) {
	i := int(gid)
	if t.numGlyphs > 0 && i >= t.numGlyphs {
		return nil, false
	}
	var from, to int
	if t.longLoca {
		if (i+2)*4 > len(t.loca) {
			return nil, false
		}
		from, to = int(be32(t.loca, i*4)), int(be32(t.loca, (i+1)*4))
	} else {
		if (i+2)*2 > len(t.loca) {
			return nil, false
		}
		// The short form stores offsets halved, which is the only reason `head` has to say
		// which form is in use.
		from, to = int(be16(t.loca, i*2))*2, int(be16(t.loca, (i+1)*2))*2
	}
	if from >= to || to > len(t.glyf) {
		return nil, false // an empty entry is a glyph with no ink, such as a space
	}
	return t.glyf[from:to], true
}

// simple reads a glyph's own contours: the on-and-off-curve point list §5 of the TrueType
// specification describes, turned into segments.
//
// The quadratic rule is the part worth stating. Two consecutive off-curve points imply an
// on-curve point midway between them, so a contour of all off-curve points — which is how a
// circle is drawn — has twice as many segments as points. Missing that implied midpoint draws
// every round glyph as a polygon of its control points, which is visibly wrong and easy not to
// notice on a straight-sided one.
func (t *TrueType) simple(g []byte, contours int) (Outline, bool) {
	if contours == 0 || len(g) < contours*2+2 {
		return nil, false
	}
	ends := make([]int, contours)
	for i := range ends {
		ends[i] = int(be16(g, i*2))
	}
	npts := ends[contours-1] + 1
	if npts <= 0 || npts > 10000 {
		return nil, false
	}
	p := contours * 2
	insLen := int(be16(g, p))
	p += 2 + insLen
	if p > len(g) {
		return nil, false
	}

	// Flags, run-length encoded by the repeat bit.
	flags := make([]byte, 0, npts)
	for len(flags) < npts {
		if p >= len(g) {
			return nil, false
		}
		f := g[p]
		p++
		flags = append(flags, f)
		if f&0x08 != 0 { // repeat
			if p >= len(g) {
				return nil, false
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
				return nil, false
			}
			d := int(g[p])
			p++
			if f&0x10 == 0 {
				d = -d
			}
			v += d
		case f&0x10 == 0: // long
			if p+2 > len(g) {
				return nil, false
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
				return nil, false
			}
			d := int(g[p])
			p++
			if f&0x20 == 0 {
				d = -d
			}
			v += d
		case f&0x20 == 0:
			if p+2 > len(g) {
				return nil, false
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
		if end < start || end >= npts {
			break
		}
		out = appendContour(out, start, end, at)
		start = end + 1
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
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
// is read far enough to skip and then declined, because placing it wrongly moves an accent
// somewhere plausible and wrong.
func (t *TrueType) composite(g []byte, depth int) (Outline, bool) {
	var out Outline
	p := 0
	for {
		if p+4 > len(g) {
			break
		}
		flags := be16(g, p)
		sub := be16(g, p+2)
		p += 4

		var dx, dy float64
		if flags&0x0001 != 0 { // ARG_1_AND_2_ARE_WORDS
			if p+4 > len(g) {
				return nil, false
			}
			if flags&0x0002 == 0 { // ARGS_ARE_XY_VALUES unset: point matching
				return nil, false
			}
			dx, dy = float64(s16(g, p)), float64(s16(g, p+2))
			p += 4
		} else {
			if p+2 > len(g) {
				return nil, false
			}
			if flags&0x0002 == 0 {
				return nil, false
			}
			dx, dy = float64(s8(g, p)), float64(s8(g, p+1))
			p += 2
		}

		a, b, c, d := 1.0, 0.0, 0.0, 1.0
		switch {
		case flags&0x0008 != 0: // WE_HAVE_A_SCALE
			if p+2 > len(g) {
				return nil, false
			}
			a = f2dot14(be16(g, p))
			d = a
			p += 2
		case flags&0x0040 != 0: // X_AND_Y_SCALE
			if p+4 > len(g) {
				return nil, false
			}
			a, d = f2dot14(be16(g, p)), f2dot14(be16(g, p+2))
			p += 4
		case flags&0x0080 != 0: // TWO_BY_TWO
			if p+8 > len(g) {
				return nil, false
			}
			a, b, c, d = f2dot14(be16(g, p)), f2dot14(be16(g, p+2)),
				f2dot14(be16(g, p+4)), f2dot14(be16(g, p+6))
			p += 8
		}

		part, ok := t.outline(sub, depth+1)
		if ok {
			// The offset is in font units and the part is already in 1/1000 em, so the offset
			// is converted here rather than the part being converted twice.
			scale := 1000 / t.unitsPerEm
			ox, oy := dx*scale, dy*scale
			for _, seg := range part {
				for i := range seg.P {
					x, y := seg.P[i].X, seg.P[i].Y
					seg.P[i] = Point{a*x + c*y + ox, b*x + d*y + oy}
				}
				out = append(out, seg)
			}
		}
		if flags&0x0020 == 0 { // MORE_COMPONENTS
			break
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
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

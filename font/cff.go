package font

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/model-harness/pdftools/font/encoding"
)

// A CFF font program — what /FontFile3 holds for /Type1C and /CIDFontType0C — parsed far enough
// to hand out glyph outlines.
//
// # What this parses
//
// The Compact Font Format of Adobe Technical Note #5176: the header, the first font of the Name
// INDEX and its Top DICT, the String and Global Subr INDEXes, the charset, the built-in encoding,
// the Private DICT's local subroutines, and — for a CID-keyed font — the FDArray and FDSelect that
// give each glyph its own Private DICT and FontMatrix. The charstrings themselves are Type 2
// (Technical Note #5177) and are interpreted in charstring.go.
//
// # What it refuses, and why at parse time
//
// Every refusal is a place where drawing would need a guess. A font that names SyntheticBase
// borrows its glyphs from another font the file does not carry; CharstringType 1 is a different
// interpreter; CFF2 and an OpenType wrapper are different containers; and the predefined Expert
// charset and encoding are tables no font in the corpus uses and this package does not hold.
// Refusing them here, in one place, means a reader of this file sees the whole unsupported set
// at once and a caller gets the reason before it has drawn anything.
//
// # Which choices follow FreeType
//
// pdfium draws CFF through FreeType, so where the specification leaves a malformed table's
// meaning open, the reading here is FreeType's (cffload.c): a charset range that would run past
// SID 65535 is trimmed rather than rejected, an encoding code assigned twice keeps its last GID,
// and a name or CID that more than one glyph claims resolves to the lowest GID. A reader that
// chose differently would draw a different glyph than the yardstick for the same bytes, which is
// a comparison failure that looks like a rasterizer bug.
type CFF struct {
	data []byte

	numGlyphs   int
	charStrings cffIndex
	gsubrs      cffIndex
	strs        cffIndex

	// cid reports a CID-keyed font: its Top DICT has ROS, its charset holds CIDs rather than
	// string IDs, and each glyph's Private DICT comes from the FDArray.
	cid bool

	// charset is the per-glyph string ID, or CID for a CID-keyed font. Entry 0 is always 0,
	// .notdef, which the format does not store.
	charset []uint16

	// codes is the built-in encoding, code to GID with 0 meaning none. Only a font that is not
	// CID-keyed has one.
	codes [256]uint16

	// fds holds each Font DICT's local subroutines and glyph transform. A font that is not
	// CID-keyed has exactly one, built from its Top and Private DICTs.
	fds      []cffFD
	fdSelect []uint8 // per GID, CID-keyed only

	names map[string]uint16 // glyph name to lowest GID, built on first use
	cids  map[uint16]uint16 // CID to lowest GID, built on first use
}

// cffFD is what one Font DICT contributes to drawing a glyph.
type cffFD struct {
	subrs cffIndex

	// matrix maps charstring units to 1/1000 em: the FontMatrix, composed with the Top DICT's
	// for a CID-keyed font, then scaled by 1000 so an Outline is in the units every other
	// outline in this package uses.
	matrix [6]float64
}

// ErrGlyphBudget reports that a charstring or TrueType glyph ran past the work budget its caller
// gave.
//
// Exported because the caller owns the budget and so owns the refusal: a renderer bounds a page's
// total glyph-building work, across both formats, and needs to tell that apart from a glyph that
// is malformed. The message names neither format, because the caller's budget is one number
// shared between them.
var ErrGlyphBudget = errors.New("font: glyph outline budget exhausted")

// ParseCFF reads a bare CFF program far enough to yield outlines.
func ParseCFF(data []byte) (*CFF, error) {
	if len(data) >= 4 && string(data[:4]) == "OTTO" {
		return nil, errors.New("font: an OpenType-wrapped CFF program, which is refused")
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("font: CFF program is %d bytes", len(data))
	}
	if major := data[0]; major != 1 {
		return nil, fmt.Errorf("font: CFF major version %d, which is refused (CFF2 is version 2)", major)
	}
	// hdrSize (data[2]) is where the Name INDEX starts, so FreeType refuses one too small to hold
	// the fixed header; its own offSize (data[3]) is an unrelated legacy field FreeType checks only
	// against an upper bound, so 0 stays accepted (cffload.c, cff_font_load).
	if hdrSize := data[2]; hdrSize < 4 {
		return nil, fmt.Errorf("font: CFF header is %d bytes, less than 4", hdrSize)
	}
	if offSize := data[3]; offSize > 4 {
		return nil, fmt.Errorf("font: CFF header's offSize is %d, more than 4", offSize)
	}
	c := &CFF{data: data}

	names, err := c.index(int(data[2]))
	if err != nil {
		return nil, fmt.Errorf("font: CFF Name INDEX: %w", err)
	}
	if names.count == 0 {
		return nil, errors.New("font: CFF Name INDEX holds no font")
	}
	if dataSize := names.end - names.base - 1; names.count > 1 && dataSize < names.count {
		// "if we have an empty font name, it must be the only font in the CFF" (cffload.c,
		// cff_font_load, just below the Name INDEX read): with more than one name, there are too
		// few bytes for every one of them to hold at least one, so at least one would have to be
		// empty — the single-font case this rule otherwise leaves alone.
		return nil, fmt.Errorf("font: CFF Name INDEX holds %d names in %d bytes", names.count, dataSize)
	}
	tops, err := c.index(names.end)
	if err != nil {
		return nil, fmt.Errorf("font: CFF Top DICT INDEX: %w", err)
	}
	if tops.count == 0 {
		return nil, errors.New("font: CFF Top DICT INDEX is empty")
	}
	if names.count > tops.count {
		// cff_font_load refuses more names than Top DICTs to choose among (cffload.c, just above
		// line 2330).
		return nil, fmt.Errorf("font: CFF Name INDEX holds %d fonts, more than the %d Top DICTs",
			names.count, tops.count)
	}
	if c.strs, err = c.index(tops.end); err != nil {
		return nil, fmt.Errorf("font: CFF String INDEX: %w", err)
	}
	if c.gsubrs, err = c.index(c.strs.end); err != nil {
		return nil, fmt.Errorf("font: CFF Global Subr INDEX: %w", err)
	}

	// The first font only. A PDF font program holds one (§9.9 requires it), and a set with more
	// does not say which one the font dictionary means.
	topData, err := tops.get(0)
	if err != nil {
		return nil, fmt.Errorf("font: CFF Top DICT: %w", err)
	}
	top, err := parseDict(topData, dictTop)
	if err != nil {
		return nil, fmt.Errorf("font: CFF Top DICT: %w", err)
	}
	if err := c.load(top); err != nil {
		return nil, err
	}
	return c, nil
}

// Top DICT, Font DICT, and Private DICT operators this reads, per Tables 9, 10 and 23 of TN #5176.
// A two-byte operator is escape 12 followed by the second byte, held here as 1200 plus it.
const (
	opFontBBox       = 5
	opCharset        = 15
	opEncoding       = 16
	opCharStrings    = 17
	opPrivate        = 18
	opSubrs          = 19
	opCharstringType = 1206
	opFontMatrix     = 1207
	opSyntheticBase  = 1220
	opMultipleMaster = 1224
	opROS            = 1230
	opFDArray        = 1236
	opFDSelect       = 1237
)

func (c *CFF) load(top cffDict) error {
	if _, ok := top.v[opSyntheticBase]; ok {
		return errors.New("font: CFF font names a SyntheticBase, whose glyphs are in another font")
	}
	if v, ok := top.v[opCharstringType]; ok && (len(v) != 1 || v[0] != 2) {
		return fmt.Errorf("font: CFF CharstringType %v, which is refused (only Type 2 is read)", v)
	}
	c.cid = rosCID(top)

	csOff, err := top.offset(opCharStrings, -1)
	if err != nil || csOff <= 0 {
		// FreeType's charstrings_offset 0 is "none", the same as the operator's absence
		// (cffload.c, cff_font_load).
		return errors.New("font: CFF Top DICT has no CharStrings")
	}
	if c.charStrings, err = c.index(csOff); err != nil {
		return fmt.Errorf("font: CFF CharStrings INDEX: %w", err)
	}
	c.numGlyphs = c.charStrings.count
	if c.numGlyphs == 0 {
		return errors.New("font: CFF font has no glyphs")
	}

	csetOff, err := top.offset(opCharset, 0)
	if err != nil {
		return err
	}
	if err := c.loadCharset(csetOff); err != nil {
		return err
	}

	topMatrix, topHas, err := top.matrix()
	if err != nil {
		return err
	}
	if !c.cid {
		encOff, err := top.offset(opEncoding, 0)
		if err != nil {
			return err
		}
		if err := c.loadEncoding(encOff); err != nil {
			return err
		}
		fd, err := c.fontDict(top, topMatrix, false)
		if err != nil {
			return err
		}
		c.fds = []cffFD{fd}
		return nil
	}
	return c.loadCIDFonts(top, topMatrix, topHas)
}

// loadCIDFonts reads the FDArray and FDSelect of a CID-keyed font.
//
// The glyph transform is FreeType's composition (cffobjs.c, cff_face_init): a Font DICT's own
// FontMatrix applied first and the Top DICT's after it when both are present, whichever one is
// present when only one is, and the 1/1000 default when neither is. FreeType normalizes the Top
// DICT's matrix before composing it and the composition after, so each must have an exact em.
func (c *CFF) loadCIDFonts(top cffDict, topMatrix [6]float64, topHas bool) error {
	if topHas {
		if err := exactEm(topMatrix, 1/largest(topMatrix)); err != nil {
			return err
		}
	}
	arrOff, err := top.offset(opFDArray, -1)
	if err != nil || arrOff < 0 {
		return errors.New("font: CID-keyed CFF font has no FDArray")
	}
	arr, err := c.index(arrOff)
	if err != nil {
		return fmt.Errorf("font: CFF FDArray: %w", err)
	}
	if arr.count == 0 || arr.count > 256 {
		return fmt.Errorf("font: CFF FDArray holds %d Font DICTs", arr.count)
	}
	for i := 0; i < arr.count; i++ {
		b, err := arr.get(i)
		if err != nil {
			return fmt.Errorf("font: CFF Font DICT %d: %w", i, err)
		}
		d, err := parseDict(b, dictTop) // a Font DICT reads the Top DICT's own field table
		if err != nil {
			return fmt.Errorf("font: CFF Font DICT %d: %w", i, err)
		}
		fd, err := c.fontDict(d, topMatrix, topHas)
		if err != nil {
			return err
		}
		c.fds = append(c.fds, fd)
	}

	selOff, err := top.offset(opFDSelect, -1)
	if err != nil || selOff < 0 {
		return errors.New("font: CID-keyed CFF font has no FDSelect")
	}
	return c.loadFDSelect(selOff)
}

// fontDict builds one Font DICT's drawing state: its Private DICT's local subroutines and its
// glyph transform. For a CID-keyed font, outer is the Top DICT's matrix, composed after this
// dictionary's own when both are written and used alone when only it is. For a font that is not
// CID-keyed, d is the Top DICT itself and outer is its own matrix or the default.
func (c *CFF) fontDict(d cffDict, outer [6]float64, outerHas bool) (cffFD, error) {
	fd := cffFD{}
	m, has, err := d.matrix()
	if err != nil {
		return fd, err
	}
	switch {
	case has && outerHas:
		scale := math.Max(math.Round(1/divisor(outer)), 1/largest(m))
		m = composeMatrix(m, outer)
		err = exactEm(m, scale)
	case has:
		err = exactEm(m, 1/largest(m))
	default:
		m = outer
	}
	if err != nil {
		return fd, err
	}
	for i := range m {
		m[i] *= 1000
	}
	fd.matrix = m

	// A Font DICT that itself carries ROS is CID-keyed by the same rule as the Top DICT (rosCID),
	// and FreeType skips its Private in that case (cffload.c, cff_subfont_load: "if
	// top->cid_registry != 0xFFFF goto Exit" before loading the Private DICT). For the Top DICT
	// itself, called here as the Font DICT of a font that is not CID-keyed, this is never true.
	if rosCID(d) {
		return fd, nil
	}

	priv, ok := d.v[opPrivate]
	if !ok {
		return fd, nil
	}
	// priv's realness, arity and sign are already checkTopOccurrence's Private case: d comes from
	// parseDict(dictTop) (the Top DICT itself, or an FDArray Font DICT), which runs that check at
	// every occurrence of the operator before ever storing one in d.v (see parseDict). intPair
	// cannot fail here.
	size, off, _ := intPair(priv[0], priv[1])
	if size == 0 || off == 0 {
		// FreeType loads no Private DICT, and so no local subrs, when either is 0 (cffload.c,
		// cff_load_private_dict: "!top->private_offset || !top->private_size").
		return fd, nil
	}
	// off and size are both nonzero here, and both non-negative (checkTopOccurrence's Private sign
	// case, already run above). FreeType does this as two separate stream operations rather than
	// one bound: FT_STREAM_SEEK(private_offset), then FT_FRAME_ENTER(private_size) (cffload.c,
	// cff_load_private_dict:1925-1927), so this models each with its own conjunct. off > len(c.data)
	// can never be this check's sole reason to refuse, since size >= 1 already makes size >
	// len(c.data)-off true whenever off does — TestPrivateBoundsAtExactBoundary pins the size
	// conjunct's own boundary, which is the one a slice can read past. A CID-keyed font's FDArray
	// Font DICTs reach this same check, through loadCIDFonts calling fontDict.
	if off > len(c.data) || size > len(c.data)-off {
		return fd, errors.New("font: CFF Private DICT lies outside the program")
	}
	pd, err := parseDict(c.data[off:off+size], dictPrivate)
	if err != nil {
		return fd, fmt.Errorf("font: CFF Private DICT: %w", err)
	}
	// Subrs is the one Private DICT offset relative to the Private DICT rather than the file.
	rel, err := pd.offset(opSubrs, -1)
	if err != nil {
		return fd, err
	}
	if rel > 0 {
		// FreeType loads local subrs only when the offset is nonzero (cffload.c,
		// cff_subfont_load: "if (priv->local_subrs_offset)").
		if rel > len(c.data)-off {
			return fd, errors.New("font: CFF local Subrs lie outside the program")
		}
		if fd.subrs, err = c.index(off + rel); err != nil {
			return fd, fmt.Errorf("font: CFF local Subrs: %w", err)
		}
	}
	return fd, nil
}

// rosCID reports whether d's ROS operator marks it CID-keyed: FreeType tests cid_registry !=
// 0xFFFF everywhere it decides (cffload.c, cff_font_load's CID branch and charset invert;
// cff_subfont_load's Private skip; cffgload.c:233's CID-to-GID through the charset), so a
// registry (operand 0) of exactly 65535 is name-keyed even with ROS present. Used for the Top
// DICT and for each Font DICT, the two dictionaries FreeType reads a subfont's cid_registry
// field from.
//
// v's realness and arity are already checkTopOccurrence's ROS case, reached through
// parseDict(dictTop) at every occurrence of the operator before ever storing one in d.v; see that
// case for the FreeType behaviour a real-valued (30-form) supplement or a count other than 3
// would need to model. v always holds exactly 3 non-real elements here.
func rosCID(d cffDict) bool {
	v, ok := d.v[opROS]
	if !ok {
		return false
	}
	return v[0] != 65535
}

// loadCharset reads the per-glyph string IDs (or CIDs), per §13 of TN #5176, then — for a font
// that is not CID-keyed — refuses a name FreeType's own comparison would not read the way this
// package does; see checkCharsetNames.
func (c *CFF) loadCharset(off int) error {
	if err := c.loadCharsetData(off); err != nil {
		return err
	}
	if c.cid {
		return nil
	}
	return c.checkCharsetNames()
}

// loadCharsetData is loadCharset's own read of the table, per §13 of TN #5176.
func (c *CFF) loadCharsetData(off int) error {
	c.charset = make([]uint16, c.numGlyphs)
	switch off {
	case 0:
		// ISOAdobe is the identity over its 229 names, so a font with more glyphs than that
		// cannot use it. FreeType rejects the font rather than leaving the rest unnamed.
		if c.numGlyphs > 229 {
			return fmt.Errorf("font: CFF font of %d glyphs names the 229-glyph ISOAdobe charset", c.numGlyphs)
		}
		for i := range c.charset {
			c.charset[i] = uint16(i) // #nosec G115 -- bounded by 229 above
		}
		return nil
	case 1, 2:
		return errors.New("font: CFF font uses a predefined Expert charset, which is refused")
	}
	if off >= len(c.data) {
		return errors.New("font: CFF charset lies outside the program")
	}
	p := off + 1
	switch format := c.data[off]; format {
	case 0:
		for j := 1; j < c.numGlyphs; j++ {
			if p+2 > len(c.data) {
				return errors.New("font: CFF charset is truncated")
			}
			c.charset[j] = be16(c.data, p)
			p += 2
		}
	case 1, 2:
		for j := 1; j < c.numGlyphs; {
			width := int(format) // the nLeft field: one byte for format 1, two for format 2
			if p+2+width > len(c.data) {
				return errors.New("font: CFF charset is truncated")
			}
			sid := int(be16(c.data, p))
			left := int(c.data[p+2])
			if format == 2 {
				left = int(be16(c.data, p+2))
			}
			p += 2 + width
			// A range whose last ID would pass 65535 is trimmed to end there, as FreeType does,
			// rather than rejected or wrapped round to low IDs that name other glyphs.
			if sid+left > 0xFFFF {
				left = 0xFFFF - sid
			}
			for k := 0; k <= left && j < c.numGlyphs; k++ {
				c.charset[j] = uint16(sid + k) // #nosec G115 -- sid+left is trimmed to 65535 above
				j++
			}
		}
	default:
		return fmt.Errorf("font: CFF charset format %d", format)
	}
	return nil
}

// checkCharsetNames refuses a charset SID of 391 or more (a custom, not standard, string) whose
// String INDEX entry FreeType's own comparison would not read the way sidString does: an empty
// string, which aliases the following pool string because FreeType only NUL-terminates a
// nonempty one (cffload.c, cff_index_get_pointers), or a string containing a NUL, which
// cff_get_name_index's ft_strcmp reads as ending there. A SID past the String INDEX is left with
// no name in both readings (FreeType returns NULL for it) and so is not refused.
func (c *CFF) checkCharsetNames() error {
	for gid, sid := range c.charset {
		if int(sid) < len(cffStdStrings) {
			continue
		}
		b, err := c.strs.get(int(sid) - len(cffStdStrings))
		if err != nil {
			continue
		}
		if len(b) == 0 || bytes.IndexByte(b, 0) >= 0 {
			return fmt.Errorf("font: CFF glyph %d names string %d, which is empty or contains a NUL", gid, sid)
		}
	}
	return nil
}

// loadEncoding reads the built-in encoding of a font that is not CID-keyed, per §12 of TN #5176.
func (c *CFF) loadEncoding(off int) error {
	switch off {
	case 0:
		// The predefined Standard encoding, mapped through this font's charset: a code whose
		// Standard name the subset does not carry has no glyph, rather than whatever glyph
		// happens to sit at that position.
		for code := 0; code < 256; code++ {
			if sid := stdSID(byte(code)); sid != 0 {
				c.codes[code] = c.gidForSID(sid)
			}
		}
		return nil
	case 1:
		return errors.New("font: CFF font uses the predefined Expert encoding, which is refused")
	}
	if off >= len(c.data) {
		return errors.New("font: CFF encoding lies outside the program")
	}
	format := c.data[off]
	p := off + 1
	// noCmap models FreeType's encoding->count staying 0 (cffload.c, cff_encoding_load): true only
	// for format 1 with no ranges, the one shape whose count the format's own maximum-nLeft
	// bookkeeping never lifts above 0 (format 0's is n+1, at least 1). cff_face_init then builds no
	// Adobe custom cmap at all (cffobjs.c ~1058, "if (encoding->count > 0)"), so a symbolic font's
	// code lookup falls through to the synthesized Unicode cmap instead — a route this package does
	// not model, so the codes below are left unassigned rather than guessed at.
	noCmap := false
	switch format & 0x7F {
	case 0:
		if p >= len(c.data) {
			return errors.New("font: CFF encoding is truncated")
		}
		n := int(c.data[p])
		p++
		if p+n > len(c.data) {
			return errors.New("font: CFF encoding is truncated")
		}
		for j := 1; j <= n; j++ {
			if j < c.numGlyphs {
				c.codes[c.data[p+j-1]] = uint16(j) // #nosec G115 -- j < numGlyphs, an INDEX count
			}
		}
		p += n
	case 1:
		if p >= len(c.data) {
			return errors.New("font: CFF encoding is truncated")
		}
		ranges := int(c.data[p])
		p++
		noCmap = ranges == 0
		gid := 1
		for r := 0; r < ranges; r++ {
			if p+2 > len(c.data) {
				return errors.New("font: CFF encoding is truncated")
			}
			code, left := int(c.data[p]), int(c.data[p+1])
			p += 2
			for k := 0; k <= left; k++ {
				if gid+k < c.numGlyphs && code+k < 256 {
					c.codes[code+k] = uint16(gid + k) // #nosec G115 -- below numGlyphs
				}
			}
			gid += left + 1
		}
	default:
		return fmt.Errorf("font: CFF encoding format %d", format&0x7F)
	}
	if format&0x80 != 0 {
		// Supplements name a glyph by string ID rather than by position, so each resolves
		// through the charset — and one naming a glyph the subset dropped leaves the code as the
		// main table set it. Still read in full when noCmap, so a truncated supplement array is
		// still refused; just never applied to c.codes.
		if p >= len(c.data) {
			return errors.New("font: CFF encoding supplement is truncated")
		}
		n := int(c.data[p])
		p++
		for i := 0; i < n; i++ {
			if p+3 > len(c.data) {
				return errors.New("font: CFF encoding supplement is truncated")
			}
			code, sid := c.data[p], be16(c.data, p+1)
			p += 3
			if noCmap {
				continue
			}
			if gid := c.gidForSID(sid); gid != 0 || sid == 0 {
				c.codes[code] = gid
			}
		}
	}
	return nil
}

// loadFDSelect reads which Font DICT each glyph of a CID-keyed font uses, per §19 of TN #5176.
func (c *CFF) loadFDSelect(off int) error {
	if off >= len(c.data) {
		return errors.New("font: CFF FDSelect lies outside the program")
	}
	c.fdSelect = make([]uint8, c.numGlyphs)
	p := off + 1
	switch format := c.data[off]; format {
	case 0:
		if p+c.numGlyphs > len(c.data) {
			return errors.New("font: CFF FDSelect is truncated")
		}
		copy(c.fdSelect, c.data[p:p+c.numGlyphs])
	case 3:
		if p+2 > len(c.data) {
			return errors.New("font: CFF FDSelect is truncated")
		}
		n := int(be16(c.data, p))
		p += 2
		if n == 0 || p+n*3+2 > len(c.data) {
			return errors.New("font: CFF FDSelect is truncated")
		}
		// Every glyph has to fall in some range. FreeType gives an uncovered glyph Font DICT 0;
		// a gap is malformed, and drawing it with whichever subroutines happen to be first is a
		// guess, so the font is refused instead.
		covered := 0
		for r := 0; r < n; r++ {
			first, fd, next := int(be16(c.data, p)), c.data[p+2], int(be16(c.data, p+3))
			p += 3
			if first != covered || next <= first {
				return errors.New("font: CFF FDSelect ranges are not contiguous from glyph 0")
			}
			for g := first; g < next && g < c.numGlyphs; g++ {
				c.fdSelect[g] = fd
			}
			covered = next
		}
		if covered < c.numGlyphs {
			return errors.New("font: CFF FDSelect leaves glyphs without a Font DICT")
		}
	default:
		return fmt.Errorf("font: CFF FDSelect format %d", format)
	}
	for _, fd := range c.fdSelect {
		if int(fd) >= len(c.fds) {
			return fmt.Errorf("font: CFF FDSelect names Font DICT %d of %d", fd, len(c.fds))
		}
	}
	return nil
}

// NumGlyphs is the glyph count of the CharStrings INDEX.
func (c *CFF) NumGlyphs() int { return c.numGlyphs }

// CIDKeyed reports whether the program's charset holds CIDs rather than glyph names.
func (c *CFF) CIDKeyed() bool { return c.cid }

// GIDForName maps a glyph name to the lowest GID the charset gives it, as FreeType's
// cff_get_name_index does. A CID-keyed font has no names and answers false.
func (c *CFF) GIDForName(name string) (uint16, bool) {
	if c.cid {
		return 0, false
	}
	if c.names == nil {
		c.names = make(map[string]uint16, c.numGlyphs)
		for gid := c.numGlyphs - 1; gid >= 0; gid-- {
			if n, ok := c.sidString(c.charset[gid]); ok {
				c.names[n] = uint16(gid) // #nosec G115 -- an INDEX count is 16-bit
			}
		}
	}
	gid, ok := c.names[name]
	return gid, ok
}

// GIDForCode maps a code through the built-in encoding. A code the encoding leaves unassigned
// answers false rather than GID 0, which is .notdef and not an answer.
func (c *CFF) GIDForCode(code byte) (uint16, bool) {
	gid := c.codes[code]
	return gid, gid != 0
}

// GIDForCID maps a CID to a glyph: through the inverted charset for a CID-keyed font, lowest GID
// winning, and as the glyph index itself below the glyph count for one that is not — which is
// FreeType's reading of a CIDFontType0 font whose program was not built CID-keyed.
func (c *CFF) GIDForCID(cid uint32) (uint16, bool) {
	if !c.cid {
		if cid >= uint32(c.numGlyphs) { // #nosec G115 -- an INDEX count is 16-bit
			return 0, false
		}
		return uint16(cid), true // #nosec G115 -- bounded by numGlyphs above
	}
	if cid > 0xFFFF {
		return 0, false
	}
	if c.cids == nil {
		c.cids = make(map[uint16]uint16, c.numGlyphs)
		for gid := c.numGlyphs - 1; gid >= 0; gid-- {
			c.cids[c.charset[gid]] = uint16(gid) // #nosec G115 -- an INDEX count is 16-bit
		}
	}
	gid, ok := c.cids[uint16(cid)]
	return gid, ok
}

// gidForSID is the lowest GID whose charset entry is sid, or 0 when none is.
func (c *CFF) gidForSID(sid uint16) uint16 {
	for gid, s := range c.charset {
		if s == sid {
			return uint16(gid) // #nosec G115 -- an INDEX count is 16-bit
		}
	}
	return 0
}

// sidString resolves a string ID: the standard strings below 391, the font's String INDEX above.
func (c *CFF) sidString(sid uint16) (string, bool) {
	if int(sid) < len(cffStdStrings) {
		return cffStdStrings[sid], true
	}
	b, err := c.strs.get(int(sid) - len(cffStdStrings))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// stdSIDs maps each StandardEncoding code to its string ID, the reading seac and the predefined
// encoding need. Built from the Annex D table this package already carries, which agrees with
// TN #5176 Appendix B at all 256 codes, rather than from a second copy of the same table.
var stdSIDs = func() (t [256]uint16) {
	sid := make(map[string]uint16, len(cffStdStrings))
	for i, s := range cffStdStrings {
		sid[s] = uint16(i) // #nosec G115 -- 391 entries
	}
	std := encoding.Standard()
	for code := 0; code < 256; code++ {
		t[code] = sid[std.Glyph(byte(code))]
	}
	return t
}()

func stdSID(code byte) uint16 { return stdSIDs[code] }

// fdFor is the Font DICT a glyph draws with.
func (c *CFF) fdFor(gid uint16) *cffFD {
	if c.fdSelect != nil {
		return &c.fds[c.fdSelect[gid]]
	}
	return &c.fds[0]
}

// composeMatrix returns the transform that applies a and then b, in PDF's row-vector convention.
func composeMatrix(a, b [6]float64) [6]float64 {
	return [6]float64{
		a[0]*b[0] + a[1]*b[2],
		a[0]*b[1] + a[1]*b[3],
		a[2]*b[0] + a[3]*b[2],
		a[2]*b[1] + a[3]*b[3],
		a[4]*b[0] + a[5]*b[2] + b[4],
		a[4]*b[1] + a[5]*b[3] + b[5],
	}
}

// cffIndex is one INDEX structure: a count, an offset size, and count+1 offsets into the data
// that follows, each relative to the byte before it (§5 of TN #5176).
type cffIndex struct {
	data    []byte
	count   int
	offSize int
	offs    int // where the offset array starts
	base    int // the byte before the object data, which every offset counts from
	end     int // the first byte after the INDEX
}

// index reads the INDEX at p, validating every offset once: the first must be 1, each later one
// no less than the one before it, and the last within the object data that follows — the bound
// get() then trusts without ever adding to base itself. FreeType repairs each of these states
// instead of refusing them (cffload.c, cff_index_get_pointers: forces the first offset to 1,
// raises one that runs backwards to the one before it, and clamps one past the end), which reads
// different bytes than a plain range check would, so every INDEX this package reads — the Top
// DICT, CharStrings, the FDArray, Strings, and global and local Subrs — is refused instead.
//
// The validation never adds an unvalidated offset to base: base+offset(x.count) can overflow a
// 32-bit int (GOARCH=386) when offset() returns a value near 0x7FFFFFFF, wrapping negative and
// passing a naive bound check before panicking the slice beneath it. Comparing offsets only
// against each other and against len(c.data)-x.base — a small, real value bounded by the data's
// own length — never overflows on either width.
func (c *CFF) index(p int) (cffIndex, error) {
	x := cffIndex{data: c.data}
	if p < 0 || p+2 > len(c.data) {
		return x, errors.New("INDEX lies outside the program")
	}
	x.count = int(be16(c.data, p))
	if x.count == 0 {
		x.end = p + 2
		return x, nil
	}
	if p+3 > len(c.data) {
		return x, errors.New("INDEX is truncated")
	}
	x.offSize = int(c.data[p+2])
	if x.offSize < 1 || x.offSize > 4 {
		return x, fmt.Errorf("INDEX offset size %d", x.offSize)
	}
	x.offs = p + 3
	x.base = x.offs + (x.count+1)*x.offSize - 1
	if x.base+1 > len(c.data) {
		return x, errors.New("INDEX offset array is truncated")
	}
	if first := x.offset(0); first != 1 {
		return x, fmt.Errorf("INDEX entry 0 has offset %d, not 1", first)
	}
	prev := 1
	for i := 1; i <= x.count; i++ {
		o := x.offset(i)
		if o < prev {
			return x, fmt.Errorf("INDEX entry %d has offset %d, before entry %d's %d", i, o, i-1, prev)
		}
		prev = o
	}
	last := prev // offset(x.count), the loop's final value
	if last > len(c.data)-x.base {
		return x, fmt.Errorf("INDEX entry %d has offset %d, past the program", x.count, last)
	}
	x.end = x.base + last
	return x, nil
}

// offset reads the i'th entry of the offset array.
func (x cffIndex) offset(i int) int {
	v := 0
	for k := 0; k < x.offSize; k++ {
		v = v<<8 | int(x.data[x.offs+i*x.offSize+k])
	}
	return v
}

// get returns object i. index validated every offset once, so this only range-checks i itself.
func (x cffIndex) get(i int) ([]byte, error) {
	if i < 0 || i >= x.count {
		return nil, fmt.Errorf("INDEX entry %d of %d", i, x.count)
	}
	return x.data[x.base+x.offset(i) : x.base+x.offset(i+1)], nil
}

// cffDict is a parsed DICT: each operator's operands, plus which operators had at least one
// operand written in the packed-decimal real form (TN #5176 Table 5) rather than an integer —
// tracked so offset() and its callers can refuse one, since FreeType reads every such operand
// through cff_parse_num, which takes a real through cff_parse_real's own fixed-point cap, a
// different reading than a plain truncation to int (cffparse.c:340,426,460-463).
type cffDict struct {
	v    map[int][]float64
	real map[int]bool
}

// dictKind is which of FreeType's per-DICT field tables governs a parse: which operators it
// knows — an operator not in the table just clears the stack — and this kind's own stack depth,
// CFF_MAX_STACK_DEPTH (cffload.c ~2004-2005): 96 for a Top or Font DICT, and one more, 97, for a
// Private DICT, which cff_load_private_dict parses with one extra slot (cffload.c ~1913-1914). A
// Font DICT reads the same field table as the Top DICT (cfftoken.h: both are CFF_CODE_TOPDICT), so
// dictTop serves both.
//
// cff_parser_run checks operand depth against that one number twice (cffparse.c ~1183, a push; and
// ~1330, an operator), both times refusing once depth is already at the limit — so a push refuses
// once stackSize operands are already on the stack, allowing stackSize in total, while an operator
// refuses once stackSize or more precede it, so at most stackSize-1 ever actually reach one.
// Operands with no operator after them at all — the tail of a truncated DICT — are bounded only by
// the push check, one more than an operator ever sees.
type dictKind int

const (
	dictTop dictKind = iota
	dictPrivate
)

// stackSize is this kind's CFF_MAX_STACK_DEPTH (see the dictKind comment): 96 for a Top or Font
// DICT, 97 for a Private DICT.
func (k dictKind) stackSize() int {
	if k == dictPrivate {
		return 97
	}
	return 96
}

// requiresOperand reports whether op is a known field of this kind's table other than one of the
// delta kinds, which cff_parser_run's "except for delta encoded arrays, which can be empty"
// exempts from needing any operand at all — the same as an operator the table does not have.
func (k dictKind) requiresOperand(op int) bool {
	if k == dictPrivate {
		return privateFields[op]
	}
	return topFields[op]
}

// topFields are the Top DICT operators FreeType's field table knows, read from cfftoken.h's
// CFF_CODE_TOPDICT (0x100+b there is 1200+b here); a Font DICT reads the same table. None of them
// is a delta kind. The entries cfftoken.h guards with #if 0 (XUID, BaseFontName, BaseFontBlend,
// BlendAxisTypes, Chameleon) are compiled out of FreeType and so are unknown here too.
var topFields = map[int]bool{
	0: true, 1: true, 2: true, 3: true, 4: true, 5: true, 13: true,
	15: true, 16: true, 17: true, 18: true,
	1200: true, 1201: true, 1202: true, 1203: true, 1204: true, 1205: true, 1206: true, 1207: true, 1208: true,
	1220: true, 1221: true, 1224: true,
	1230: true, 1231: true, 1232: true, 1233: true, 1234: true, 1235: true, 1236: true, 1237: true, 1238: true,
}

// privateFields are the Private DICT operators FreeType's field table knows and requires an
// operand for. It omits the delta kinds — BlueValues, OtherBlues, FamilyBlues, FamilyOtherBlues,
// StemSnapH and StemSnapV (ops 6-9, 1212, 1213) — which are known but exempt (see requiresOperand).
var privateFields = map[int]bool{
	10: true, 11: true, 19: true, 20: true, 21: true,
	1209: true, 1210: true, 1211: true, 1214: true, 1215: true, 1216: true, 1217: true, 1218: true, 1219: true,
}

// parseDict reads a DICT, per §4 of TN #5176. The integer operand forms are the charstring ones,
// plus a 32-bit form; reals are packed decimal nibbles.
func parseDict(b []byte, kind dictKind) (cffDict, error) {
	d := cffDict{v: map[int][]float64{}, real: map[int]bool{}}
	var ops []float64
	var hadReal bool
	for p := 0; p < len(b); {
		v := b[p]
		switch {
		case v <= 21:
			op := int(v)
			p++
			if v == 12 {
				if p >= len(b) {
					return d, errors.New("DICT ends inside an escaped operator")
				}
				op = 1200 + int(b[p])
				p++
			}
			if len(ops) >= kind.stackSize() {
				// cff_parser_run's operator-time check (cffparse.c ~1330): one operand fewer than
				// the push check below allows, since this fires before the operator ever consumes
				// what is already on the stack.
				return d, errors.New("DICT operator has too many operands")
			}
			if len(ops) == 0 && kind.requiresOperand(op) {
				return d, fmt.Errorf("DICT operator %d has no operands", op)
			}
			if kind == dictTop {
				// cff_parser_run calls a field's reader at every occurrence of its operator, and
				// fails the whole DICT the moment one occurrence's reader errors (cffparse.c,
				// cff_parser_run: "error = field->reader(parser); if (error) goto Exit"). d.v[op]
				// below only overwrites on each occurrence, so without this, a bad first
				// occurrence of one of these fields followed by a valid one would draw the valid
				// one while FreeType has already failed the font on the first.
				if err := checkTopOccurrence(op, ops, hadReal); err != nil {
					return d, err
				}
			}
			d.v[op] = ops
			d.real[op] = hadReal
			ops = nil
			hadReal = false
			continue
		case v == 28:
			if p+3 > len(b) {
				return d, errors.New("DICT operand is truncated")
			}
			ops = append(ops, float64(int16(be16(b, p+1)))) // #nosec G115 -- the form is signed
			p += 3
		case v == 29:
			if p+5 > len(b) {
				return d, errors.New("DICT operand is truncated")
			}
			ops = append(ops, float64(int32(be32(b, p+1)))) // #nosec G115 -- the form is signed
			p += 5
		case v == 30:
			r, n, err := dictReal(b[p+1:])
			if err != nil {
				return d, err
			}
			ops = append(ops, r)
			hadReal = true
			p += 1 + n
		case v >= 32 && v <= 246:
			ops = append(ops, float64(int(v)-139))
			p++
		case v >= 247 && v <= 254:
			if p+2 > len(b) {
				return d, errors.New("DICT operand is truncated")
			}
			w := float64((int(v)-247)*256 + int(b[p+1]) + 108)
			if v >= 251 {
				w = float64(-(int(v)-251)*256 - int(b[p+1]) - 108)
			}
			ops = append(ops, w)
			p += 2
		default:
			return d, fmt.Errorf("DICT byte %d is reserved", v)
		}
		if len(ops) > kind.stackSize() {
			// cff_parser_run's push-time check (cffparse.c ~1183): refuses the push that would
			// make stackSize+1 operands sit on the stack at once, whether or not an operator ever
			// follows them.
			return d, errors.New("DICT operand stack is full")
		}
	}
	return d, nil
}

// checkTopOccurrence runs, at every occurrence of a Top or Font DICT operator (see dictKind's
// comment: a Font DICT reads the Top DICT's own field table), the arity and sign check that
// operator's own field ultimately applies. It is the only place any of those checks run: fontDict
// and rosCID (above) and matrix (below) trust that the value parseDict's last occurrence kept in
// d.v already has the right shape, and just read it. Private DICT fields never reach here:
// FontBBox, ROS, FontMatrix, Private and MultipleMaster are Top DICT operators FreeType's Private
// field table does not know (privateFields above), so an occurrence of one of them inside a
// Private DICT is an unknown operator FreeType ignores after clearing the stack (cffparse.c,
// cff_parser_run: "this is an unknown operator, or it is unsupported; we will ignore it for
// now"), not a field whose reader can ever run.
func checkTopOccurrence(op int, ops []float64, real bool) error {
	switch op {
	case opFontBBox:
		if len(ops) < 4 {
			// cff_parse_font_bbox needs 4 operands and fails the whole program with
			// Stack_Underflow below that (cffparse.c, cff_parse_font_bbox: "parser->top >=
			// parser->stack + 4"), reading only the first 4 of a longer list.
			return fmt.Errorf("DICT FontBBox has %d operands, fewer than 4", len(ops))
		}
	case opROS:
		if real {
			// FreeType reads a real-valued (30-form) supplement rather than refusing it, unlike
			// this package: cff_parse_num returns cff_parse_real(...) >> 16 for a real operand
			// (cffparse.c, cff_parse_num:457-465). cff_parse_real first rounds the real to the
			// nearest 1/65536 in 16.16 fixed point through FT_DivFix (ftcalc.c, FT_DivFix:
			// "( ( a << 16 ) + ( b >> 1 ) ) / b") — cff_parse_cid_ros's own FT_TRACE1 calls
			// this "real supplement is rounded" (cffparse.c:888) — and the later >> 16 floors
			// that already-rounded fixed value to an integer, so 2.999999 becomes 3, not 2. Out
			// of range, cff_parse_real saturates to 0x7FFFFFFF, which the >> 16 makes 32767, when
			// the integer part has more than five digits (cffparse.c:395-396), or has no fraction
			// and exceeds 0x7FFF (426-427); but it returns 0 when a fraction is present and the
			// integer part exceeds 0x7FFF (417-418). So 40000.25 reads as 0, 100000.5 as 32767,
			// and -40000 as -32768. This package refuses every real here regardless of value, so
			// none of that repair needs modelling to match it.
			// cff_parse_cid_ros itself needs only 3 operands (cffparse.c:874-899) and reads just
			// the first three of a longer list, ignoring the rest. This package refuses a
			// real-valued operand here, and any operand count other than 3, instead of modelling
			// either repair: stricter than FreeType, the safe direction for a grammar error
			// FreeType repairs silently rather than reads as written.
			return errors.New("DICT ROS has a real-valued operand, which is refused")
		}
		if len(ops) != 3 {
			return fmt.Errorf("DICT ROS has %d operands, not 3", len(ops))
		}
	case opFontMatrix:
		if len(ops) != 6 {
			return fmt.Errorf("DICT FontMatrix has %d elements", len(ops))
		}
	case opPrivate:
		if real {
			return errors.New("DICT Private operator has a real-valued operand, which is refused")
		}
		if len(ops) != 2 {
			// cff_parse_private_dict needs only "parser->top >= parser->stack + 2" (cffparse.c,
			// cff_parse_private_dict) and reads just the first two of a longer list, ignoring the
			// rest. This package refuses that grammar error instead of repairing it, the safe
			// direction for something FreeType reads silently rather than as written.
			return errors.New("DICT Private operator takes a size and an offset")
		}
		if size, off, ok := intPair(ops[0], ops[1]); !ok || size < 0 || off < 0 {
			// cff_parse_private_dict fails with Invalid_File_Format the moment either cff_parse_num
			// comes back negative (cffparse.c, cff_parse_private_dict:780-818: "Invalid dictionary
			// size" / "...offset").
			return errors.New("DICT Private has a negative size or offset")
		}
	case opMultipleMaster:
		// An FT_TRACE1 message in cff_parse_multiple_master reads "handling first master design
		// only": FreeType itself only partly supports a CFF1 multiple-master font, and the
		// blending a real one needs is on top of that. This backend has no model for either, so
		// the operator's mere presence is refused, regardless of its operand count: modelling
		// cff_parse_multiple_master's own Stack_Underflow-below-5 and num_designs-outside-2..16
		// checks would still leave a font this package cannot draw correctly.
		return errors.New("DICT names a MultipleMaster, which this backend has no model for")
	}
	return nil
}

// dictReal reads a packed-decimal real, returning it and the bytes it took.
func dictReal(b []byte) (float64, int, error) {
	var s strings.Builder
	for i := 0; i < len(b) && i < 64; i++ {
		for _, nib := range [2]byte{b[i] >> 4, b[i] & 0x0F} {
			switch {
			case nib <= 9:
				s.WriteByte('0' + nib)
			case nib == 0xA:
				s.WriteByte('.')
			case nib == 0xB:
				s.WriteByte('E')
			case nib == 0xC:
				s.WriteString("E-")
			case nib == 0xE:
				s.WriteByte('-')
			case nib == 0xF:
				v, err := strconv.ParseFloat(s.String(), 64)
				if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
					return 0, 0, fmt.Errorf("DICT real %q", s.String())
				}
				return v, i + 1, nil
			default:
				return 0, 0, errors.New("DICT real uses the reserved nibble")
			}
		}
	}
	return 0, 0, errors.New("DICT real is unterminated")
}

// offset reads an operator's single integer operand, or def when the operator is absent. A
// real-form operand is refused rather than read: every operator this is called for — charset,
// Encoding, CharStrings, FDArray, FDSelect, Subrs — is one FreeType reads through cff_parse_num
// (cffparse.c:458-464), which rounds a real to 16.16 and truncates it, and reads one past 32767
// as 32767 or 0 depending on its digits (checkTopOccurrence's ROS case gives the cases), where this
// package's int(v[0]) would read it as written.
func (d cffDict) offset(op, def int) (int, error) {
	v, ok := d.v[op]
	if !ok {
		return def, nil
	}
	if d.real[op] {
		return 0, fmt.Errorf("font: CFF DICT operator %d has a real-valued operand, which is refused", op)
	}
	if len(v) != 1 || v[0] != math.Trunc(v[0]) || v[0] < 0 || v[0] > math.MaxInt32 {
		return 0, fmt.Errorf("font: CFF DICT operator %d has operands %v, not an offset", op, v)
	}
	return int(v[0]), nil
}

// matrix reads a FontMatrix, reporting whether one is present.
//
// FreeType reads each element as a decimal mantissa of up to five digits and the power of ten
// of its last digit (cffparse.c, cff_parse_font_matrix). It fails the font when there are fewer
// than six elements, but reads only the first six of a longer list and ignores the rest;
// checkTopOccurrence's exactly-six case, run at every occurrence before parseDict ever stores one
// in d.v, is stricter than that, the safe direction for a grammar error FreeType repairs silently
// rather than reads as written. It falls back to the identity when those powers
// are implausible: above zero, which is an element of 32768 or more; all below -9; or two nonzero
// elements more than nine apart. Neither fallback is modelled. A power lies within four of the
// element's leading digit, so a matrix is taken only where no digit string can reach a fallback:
// a largest element of at least 1e-5 and below one unit per em, and no nonzero element below
// 1e-5 of it. FreeType
// also falls back when the determinant is at most a thirty-second of the sum of the squared
// elements (ftcalc.c, FT_Matrix_Check), which it tests on the elements shifted down to 13
// significant bits (FT_MSB(val) - 12, as in 2.13.2 through 2.14.1; later sources keep more bits,
// which only shrinks the error). The determinant must clear that bound by a tenth: with L the
// largest shifted element, at least 4096, truncation moves 32|det| minus the sum of squares by
// under 136L + 68, and a tenth of that sum is at least 0.1L², over 409L.
//
// The translation must be zero. FreeType divides it by the em it derives (see exactEm) in fixed
// point and then truncates it to whole units, so [0.001 0 0 0.001 0.05 0.02] moves a glyph 49
// and 19 units and not 50 and 20.
func (d cffDict) matrix() ([6]float64, bool, error) {
	def := [6]float64{0.001, 0, 0, 0.001, 0, 0}
	v, ok := d.v[opFontMatrix]
	if !ok {
		return def, false, nil
	}
	// v's arity is already checkTopOccurrence's FontMatrix case, reached through parseDict
	// (dictTop) at every occurrence of the operator before ever storing one in d.v; v always
	// holds exactly 6 elements here.
	var m [6]float64
	copy(m[:], v)
	big := largest(m)
	det := m[0]*m[3] - m[1]*m[2]
	squares := m[0]*m[0] + m[1]*m[1] + m[2]*m[2] + m[3]*m[3]
	ordinary := big >= 1e-5 && big < 1 && math.Abs(det) > squares/32*1.1
	for _, e := range m[:4] {
		if e != 0 && math.Abs(e) < big*1e-5 {
			ordinary = false
		}
	}
	if !ordinary {
		return def, false, fmt.Errorf("font: CFF FontMatrix %v, which is refused", v)
	}
	if m[4] != 0 || m[5] != 0 {
		return def, false, fmt.Errorf("font: CFF FontMatrix %v translates, which is refused", v)
	}
	return m, true, nil
}

// emTolerance is how far, relative to the scale it gives, FreeType's reading of a FontMatrix
// may stray from the matrix: a tenth of a pixel on a glyph a thousand pixels tall.
const emTolerance = 1e-4

// exactEm refuses a matrix FreeType cannot normalize without changing its scale.
//
// FreeType divides the matrix by its divisor and keeps the quotient of that division as the em:
// the font's units per em, which is an integer (cffobjs.c, cff_face_init). An em that is not a
// whole number is rounded, and every glyph is drawn at the rounded scale — [0.5 0 0 0.35 0 0] is
// an em of 3 and not 2.857, 5% small. The face stores the em in 16 bits, so one over 65535 is
// refused too.
//
// And the divisor is a 16.16 number, the element times a factor FreeType chose, so its error is
// up to one part in 65536 of the factor: a divisor too small against the factor is too coarse to
// give the em it names. scale is the least that factor can be. A matrix FreeType parsed alone
// carries one over its largest element or more; a Font DICT's matrix composed with the Top
// DICT's carries the larger of the Top's em and the Font DICT's own factor.
func exactEm(m [6]float64, scale float64) error {
	div := divisor(m)
	em := 1 / div
	switch {
	case 1/(65536*div*scale) > emTolerance:
		return fmt.Errorf("font: CFF FontMatrix %v divides by an element too small to normalize by", m)
	case math.Round(em) > 65535:
		return fmt.Errorf("font: CFF FontMatrix %v makes an em of %.6g units, more than 65535", m, em)
	case math.Abs(em-math.Round(em)) > emTolerance*em:
		return fmt.Errorf("font: CFF FontMatrix %v makes an em of %.6g units, which FreeType rounds", m, em)
	}
	return nil
}

// divisor is the element FreeType normalizes a matrix by: yy, or yx when yy is zero.
func divisor(m [6]float64) float64 {
	if m[3] != 0 {
		return math.Abs(m[3])
	}
	return math.Abs(m[1])
}

// largest is the magnitude of a matrix's largest linear element.
func largest(m [6]float64) float64 {
	big := 0.0
	for _, e := range m[:4] {
		big = math.Max(big, math.Abs(e))
	}
	return big
}

// intPair reads two operands that must be integers.
func intPair(a, b float64) (int, int, bool) {
	if a != math.Trunc(a) || b != math.Trunc(b) || math.Abs(a) > math.MaxInt32 || math.Abs(b) > math.MaxInt32 {
		return 0, 0, false
	}
	return int(a), int(b), true
}

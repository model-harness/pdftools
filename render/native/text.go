package native

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/font"
	"github.com/model-harness/pdftools/font/encoding"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// textFont is a font resolved for drawing: what the dictionary says, plus the program that has
// the outlines, plus the reason there is no program when there is none.
//
// The reason is kept because it is the error a page is refused with. "This page draws text" is
// not actionable; "this font is a CFF program and this backend reads glyf" names the next
// increment, and over a corpus the distribution of those reasons is the worklist.
type textFont struct {
	f    *font.Font
	tt   *font.TrueType
	cff  *font.CFF
	t1   *font.Type1
	why  string // empty when tt, cff, or t1 is usable
	dict objects.Dict

	// substituted marks a program that is a stand-in rather than the document's own.
	//
	// It changes how a code reaches a glyph. The document's own program was subsetted and
	// encoded together with the font dictionary, so a code may index it directly; a substitute
	// knows nothing of that dictionary and can only be reached through a *character*. Without
	// this flag the code-as-glyph-index last resort would fire on a substitute and draw whatever
	// glyph happened to sit at that index — arbitrary letters, confidently.
	substituted bool

	// byCode marks a simple TrueType font whose codes select glyphs directly, through the program's
	// (3,0) or (1,0) subtable or as the glyph index, rather than through the glyph names its
	// encoding states. ttRoute decides it once per font, because it is the font's shape that
	// decides it and not the code.
	byCode bool

	// outlines holds each glyph's outline, or why it has none, for the page: the survey builds it
	// to learn whether the glyph draws, and paint draws what the survey built.
	outlines map[uint16]glyphOutline
}

type glyphOutline struct {
	out font.Outline
	err error
}

// loadFont resolves a /Font resource name once per resource dictionary, and a font program once
// per page.
func (w *walker) loadFont(name string) *textFont {
	if tf, ok := w.fonts[name]; ok {
		return tf
	}
	fonts, ok := objects.GetDict(w.s, w.res, "Font")
	if !ok {
		tf := &textFont{why: "the resources declare no /Font"}
		w.fonts[name] = tf
		return tf
	}
	ref, isRef := fonts[objects.Name(name)].(objects.Ref)
	if isRef {
		if tf, ok := w.fontRefs[ref]; ok {
			w.fonts[name] = tf
			return tf
		}
	}
	tf := w.parseFont(fonts, name)
	w.fonts[name] = tf
	if isRef {
		w.fontRefs[ref] = tf
	}
	return tf
}

// parseFont resolves one font dictionary and the program behind it.
func (w *walker) parseFont(fonts objects.Dict, name string) *textFont {
	tf := &textFont{}
	dict, ok := objects.GetDict(w.s, fonts, objects.Name(name))
	if !ok {
		tf.why = fmt.Sprintf("/Font /%s is not in the resources", name)
		return tf
	}
	tf.dict = dict
	f := font.Load(w.s, dict)
	if f == nil {
		tf.why = fmt.Sprintf("/Font /%s did not load", name)
		return tf
	}
	tf.f = f

	// A simple font whose dictionary has an entry pdfium reads under a looser type or a wider
	// range (font.Font.Irregular) is refused before either program route below is even chosen:
	// C15, C16, C17, C20, and C21 are each a page where the two readers would select a different
	// glyph, and the divergence is in what the dictionary states rather than in which of
	// FontFile2, FontFile3, or a substitute carries the glyphs. A composite font has none of
	// these entries — Irregular is a Kind == Simple concept — so it never refuses here.
	if f.Kind != font.Composite {
		if why := f.Irregular(); why != "" {
			tf.why = "an irregular " + why
			return tf
		}
	}

	// The program lives on the descriptor, and for a composite font on the descendant's
	// descriptor — the Type0 dictionary has none of its own (§9.7.4.1).
	desc := dict
	if f.Kind == font.Composite {
		if kids, ok := objects.GetArray(w.s, dict, "DescendantFonts"); ok && len(kids) > 0 {
			if d, ok := objects.GetDict(w.s, objects.Dict{"D": kids[0]}, "D"); ok {
				desc = d
			}
		}
	}
	fd, ok := objects.GetDict(w.s, desc, "FontDescriptor")
	if !ok {
		// A standard-14 font: no descriptor at all, so the name is the only statement of which
		// face the page wants. Substituted rather than refused, now that a caller can supply one.
		tt, why := w.substitute(f, fmt.Sprintf(
			"/Font /%s has no descriptor, so it is a standard-14 face", name))
		tf.tt, tf.why, tf.substituted = tt, why, tt != nil
		return tf
	}
	programs := 0
	for _, key := range []objects.Name{"FontFile", "FontFile2", "FontFile3"} {
		if fd[key] != nil {
			programs++
		}
	}
	switch {
	case programs > 1:
		// §9.8 allows one, and pdfium takes the first of FontFile, FontFile2, FontFile3 — so which
		// program a page means is a question the file answers wrongly and the two readers answer
		// differently.
		tf.why = "the descriptor embeds more than one font program"
		return tf
	case fd["FontFile"] != nil:
		st, _ := objects.GetStream(w.s, fd, "FontFile")
		data, ok := objects.GetStreamData(w.s, fd, "FontFile")
		if !ok {
			tf.why = "the FontFile stream did not decode"
			return tf
		}
		tf.t1, tf.why = w.parseType1(f, st.Dict, data)
		return tf
	case fd["FontFile3"] != nil:
		data, ok := objects.GetStreamData(w.s, fd, "FontFile3")
		if !ok {
			tf.why = "the FontFile3 stream did not decode"
			return tf
		}
		tf.cff, tf.why = w.parseCFF(f, desc, data)
		return tf
	}
	data, ok := objects.GetStreamData(w.s, fd, "FontFile2")
	if !ok {
		tt, why := w.substitute(f, "the font declares a descriptor and embeds no program")
		tf.tt, tf.why, tf.substituted = tt, why, tt != nil
		return tf
	}
	tt, err := font.ParseTrueType(data)
	if err != nil {
		tf.why = fmt.Sprintf("the FontFile2 program did not parse: %v", err)
		return tf
	}
	if f.Kind != font.Composite {
		if tf.byCode, tf.why = ttRoute(f, tt); tf.why != "" {
			return tf
		}
	}
	tf.tt = tt
	return tf
}

// ttRoute decides how a simple TrueType font's codes reach its glyphs, and refuses the fonts whose
// glyphs this backend and pdfium would select differently.
//
// §9.6.5.4 gives two routes: by the glyph name a code's encoding states, looked up through the
// (3,1) subtable, for a font with a named WinAnsi or MacRoman encoding or the nonsymbolic flag; and
// by the code itself, through (3,0) or (1,0), for a font with no /Encoding or the symbolic flag.
// pdfium takes the first for a nonsymbolic font and for any font over WinAnsi, MacRoman, or
// MacExpert — which it reads as WinAnsi — and the second otherwise. A font both of §9.6.5.4's
// routes claim, or that it and pdfium route apart, is one where which glyph a code means is the
// open question, and each is refused by a reason that names its shape.
//
// Of the fonts both route by name, this backend draws those over WinAnsi: it is the one base both
// readers take from the same table, and the only one the corpus has. A font with /Differences is
// refused too, but not because its names are resolved two ways: CPDF_SimpleFont::LoadDifferences
// (cpdf_simplefont.cpp) resizes char_names_ to 256 for any /Differences array, even one that
// assigns nothing, and cpdf_truetypefont.cpp's by-name test reads char_names_.empty() as half of
// its condition — so for a font not also flagged nonsymbolic, the array's mere presence moves
// pdfium onto the by-code route regardless of what it assigns. Refusing whenever the array is
// there, rather than only when it changes pdfium's route, is the simpler rule and the safe one:
// it costs the narrow case where the font is nonsymbolic too and both readers would have agreed.
func ttRoute(f *font.Font, tt *font.TrueType) (byCode bool, why string) {
	if f.Subtype != "TrueType" {
		// pdfium selects the glyphs of a TrueType program under a Type 1 font by its Type 1
		// rules, which are not §9.6.5.4's.
		return false, fmt.Sprintf("a FontFile2 program under a /%s font", f.Subtype)
	}
	base, sym := f.BaseEncoding(), f.Symbolic()
	byName := f.Nonsymbolic() || base == "WinAnsiEncoding" || base == "MacRomanEncoding" ||
		base == "MacExpertEncoding"
	// A font flagged both symbolic and nonsymbolic, or neither, never reaches here (R#0):
	// checkFlags now reports that shape through Irregular, and parseFont refuses on it before
	// either program route is chosen — so sym and f.Nonsymbolic() are never both true here, and
	// never both false either.
	switch {
	case f.HasDifferences():
		return false, "a TrueType font with /Differences"
	case !byName:
		switch {
		case tt.HasCmap() && !tt.HasSubtable(3, 0) && !tt.HasSubtable(1, 0):
			// pdfium reads the code as a character in whatever subtable FreeType takes for
			// Unicode, which §9.6.5.4 leaves to the reader.
			return false, "a TrueType font read by code, with neither a (3,0) nor a (1,0) subtable"
		case !tt.HasCmap() && tt.PostNamesGlyphs():
			// With no cmap subtable surviving at all, this backend's only other route is the
			// code itself as a glyph index. FreeType instead synthesizes a Unicode charmap from
			// `post`'s glyph names whenever none of the ones that did survive is Unicode or
			// MS_SYMBOL (sfobjs.c:1206-1230), and pdfium's symbolic route reads the code through
			// that charmap before it ever falls back to the code as an index
			// (cpdf_truetypefont.cpp, CPDF_TrueTypeFont::LoadGlyphMap,
			// SelectCharMap(kUnicode)).
			return false, "a TrueType font read by code, with no surviving cmap subtable and a " +
				"post table that names glyphs, which FreeType reads through instead"
		}
		return true, ""
	case sym:
		return false, fmt.Sprintf("a symbolic TrueType font over /%s, which §9.6.5.4 ignores and "+
			"pdfium reads glyph names from", base)
	case !f.HasEncoding():
		return false, "a nonsymbolic TrueType font with no /Encoding, which §9.6.5.4 routes both " +
			"by glyph name and by code"
	case base == "":
		return false, "a TrueType font whose /Encoding names no base encoding"
	case base != "WinAnsiEncoding":
		return false, fmt.Sprintf("a TrueType font whose glyph names come from /%s", base)
	case !tt.HasSubtable(3, 1):
		return false, "a nonsymbolic TrueType font with no (3,1) subtable"
	}
	return false, ""
}

// parseCFF reads a FontFile3 program and refuses the fonts whose glyphs this backend and pdfium
// would select differently.
//
// Every refusal here is a font the interpreter could draw, on a page where which glyph to draw is
// the open question. pdfium reads a named base encoding other than WinAnsi through tables of its
// own, special-cases a font named Symbol or ZapfDingbats by name, and honours a /CIDToGIDMap
// stream that §9.7.4.2 gives only to a TrueType descendant; a CID-keyed program under a simple
// font, or a CFF one under a CIDFontType2, is a file that contradicts itself. Each would be a page
// drawn in confident wrong glyphs wherever the two readings part.
func (w *walker) parseCFF(f *font.Font, desc objects.Dict, data []byte) (*font.CFF, string) {
	if len(data) >= 4 {
		switch binary.BigEndian.Uint32(data) {
		case 0x00010000, 0x74727565: // 'true'
			return nil, "the FontFile3 program is TrueType, which belongs in FontFile2"
		}
	}
	if f.Kind == font.Composite {
		if sub, _ := objects.GetName(w.s, desc, "Subtype"); sub != "CIDFontType0" {
			return nil, fmt.Sprintf("a FontFile3 program under a /%s descendant", sub)
		}
		if _, ok := objects.GetStream(w.s, desc, "CIDToGIDMap"); ok {
			return nil, "a /CIDToGIDMap stream on a CIDFontType0 font"
		}
	} else if why := type1Route(f, "FontFile3", "CFF"); why != "" {
		return nil, why
	}
	c, err := font.ParseCFF(data)
	if err != nil {
		return nil, fmt.Sprintf("the FontFile3 program did not parse: %v", err)
	}
	if c.CIDKeyed() && f.Kind != font.Composite {
		return nil, "a CID-keyed CFF program under a simple font"
	}
	return c, ""
}

// type1Route refuses the simple fonts whose codes pdfium sends to a Type 1 or CFF program's glyphs
// by tables of its own, rather than type1GID's: a named base encoding other than WinAnsi, and a
// font named Symbol or ZapfDingbats. pdfium reads both program kinds as one CPDF_Type1Font, so
// the refusals are the same for each. program and kind name the font in the reason.
func type1Route(f *font.Font, program, kind string) string {
	if f.Subtype != "Type1" && f.Subtype != "MMType1" {
		return fmt.Sprintf("a %s program under a /%s font", program, f.Subtype)
	}
	if base := f.BaseEncoding(); base != "" && base != "WinAnsiEncoding" {
		return fmt.Sprintf("a %s font over /%s", kind, base)
	}
	switch strings.ToLower(f.Name()) {
	case "symbol", "symbolmt", "zapfdingbats":
		return fmt.Sprintf("a %s font named %s", kind, f.Name())
	}
	return ""
}

// parseType1 reads a FontFile program, refusing what parseCFF refuses of a simple font, and a
// program under a composite font, which §9.7.4 gives no Type 1 route. /Length1 is where §9.9 says
// the clear text ends and FreeType finds it for itself, by searching for eexec; where the two
// disagree, the file and the yardstick have different ideas of where the encrypted part begins,
// and the font is refused. An absent /Length1 leaves only FreeType's answer, and is taken.
func (w *walker) parseType1(f *font.Font, stream objects.Dict, data []byte) (*font.Type1, string) {
	if f.Kind == font.Composite {
		return nil, "a FontFile program under a composite font"
	}
	if why := type1Route(f, "FontFile", "Type 1"); why != "" {
		return nil, why
	}
	t, err := font.ParseType1(data)
	if err != nil {
		return nil, fmt.Sprintf("the FontFile program did not parse: %v", err)
	}
	if n, ok := objects.GetInt(w.s, stream, "Length1"); ok && n != int64(t.ClearTextLength()) {
		return nil, fmt.Sprintf("/Length1 %d, where the program's clear text is %d bytes", n,
			t.ClearTextLength())
	}
	return t, ""
}

// drawable reports whether the font has a program to draw with.
func (tf *textFont) drawable() bool {
	return tf.tt != nil || tf.cff != nil || tf.t1 != nil
}

var errNoGlyph = errors.New("the program maps it to none")

// gid resolves one decoded glyph to an index in the program.
func (tf *textFont) gid(g font.Glyph) (uint16, error) {
	if tf.cff != nil || tf.t1 != nil {
		return tf.type1GID(g)
	}
	return tf.ttGID(g)
}

// type1GID selects a glyph in a CFF or Type 1 program the way pdfium does.
//
// A composite font's CID goes through the program's charset, or is the glyph index itself when the
// program is not CID-keyed (§9.7.4.2). A simple font's code goes by the glyph name its encoding
// states, when it states one; a symbolic font without one goes by the program's built-in
// encoding. A non-symbolic font that names no base is where the readings part: §9.6.5.1 gives it
// the program's built-in encoding and pdfium looks up the StandardEncoding name, so its glyph is
// drawn only where both land on the same one.
//
// Glyph 0 is .notdef, which draws a box or nothing depending on the font, so a code that reaches
// it is refused like one that reaches no glyph at all.
func (tf *textFont) type1GID(g font.Glyph) (uint16, error) {
	var c interface {
		GIDForName(string) (uint16, bool)
		GIDForCode(byte) (uint16, bool)
	} = tf.t1
	if tf.cff != nil {
		c = tf.cff
	}
	var gid uint16
	var ok bool
	if tf.f.Kind == font.Composite {
		gid, ok = tf.cff.GIDForCID(g.CID) // parseType1 refuses a Type 1 program here
	} else {
		code := byte(g.Code) // #nosec G115 -- a simple font's code is one byte
		name, base := tf.f.StatedGlyphName(code), tf.f.BaseEncoding()
		switch {
		case name != "":
			gid, ok = c.GIDForName(name)
		case base != "":
			return 0, fmt.Errorf("/%s names no glyph for it", base)
		case tf.f.Symbolic():
			gid, ok = c.GIDForCode(code)
		default:
			byName, okName := c.GIDForName(encoding.Standard().Glyph(code))
			byCode, okCode := c.GIDForCode(code)
			if okName && okCode && byName != byCode {
				return 0, fmt.Errorf("its StandardEncoding name selects glyph %d and the "+
					"program's built-in encoding glyph %d", byName, byCode)
			}
			gid, ok = byName, okName && okCode
		}
	}
	if !ok || gid == 0 {
		return 0, errNoGlyph
	}
	return gid, nil
}

// ttGID resolves one decoded glyph to an index in a TrueType program.
//
// Three routes, because a PDF has three ways of saying which glyph it means (§9.6.5.4, §9.7.4.2):
// a composite font maps its CID through /CIDToGIDMap; a simple font ttRoute reads by code looks
// the code itself up, because its codes mean nothing outside the font; and every other simple font
// goes through the glyph name its encoding states, to the character the glyph list gives that
// name, to the glyph the program's (3,1) subtable gives the character. The name is the document's
// and not the text's: a /ToUnicode map says what a code means to a reader, and a subsetter's
// (1,0) subtable may say something else again, but neither is what selects the glyph.
func (tf *textFont) ttGID(g font.Glyph) (uint16, error) {
	// A substituted face is reachable only through the character, and this is the whole reason
	// textFont records that it is one. Every other route below asks the *document* which glyph it
	// means — /CIDToGIDMap indexes the program the document embedded, a symbolic cmap is the one
	// the subsetter wrote, and the last resort treats the code itself as an index into it. None of
	// those indices mean anything in a face the document has never seen: following them would pick
	// whatever glyph happened to sit at that position and draw it with complete confidence.
	//
	// So a code whose text could not be decoded has no substitute glyph, and the page is refused.
	// That is the honest outcome — it is precisely the case where nothing knows what character the
	// page draws — and it is why a substituted symbolic font with no /ToUnicode stays refused
	// rather than becoming a line of arbitrary letters.
	if tf.substituted {
		for _, r := range g.Text {
			if gid, ok := tf.tt.GIDForRune(r); ok {
				return gid, nil
			}
		}
		return 0, errNoGlyph
	}
	if tf.f.Kind == font.Composite {
		if gid, ok := tf.f.GIDForCID(g.CID); ok {
			return gid, nil
		}
		return 0, errNoGlyph
	}
	code := byte(g.Code) // #nosec G115 -- a simple font's code is one byte
	if tf.byCode {
		return tf.ttCodeGID(code)
	}
	name := tf.f.StatedGlyphName(code)
	if name == "" {
		// Codes below 32, and the five above that WinAnsi leaves unused and Annex D's note 3
		// sends to the bullet, which this package's table does not.
		return 0, fmt.Errorf("/%s names no glyph for it", tf.f.BaseEncoding())
	}
	if r, ok := encoding.GlyphRune(name); ok {
		// #nosec G115 -- a decoded rune is a code point or U+FFFD, never negative
		if gid, ok := tf.tt.GIDInSubtable(3, 1, uint32(r)); ok {
			return gid, nil
		}
	}
	return 0, errNoGlyph
}

// ttCodeGID selects a glyph by its code, through the (3,0) subtable at whichever of §9.6.5.4's four
// ranges maps it, else through (1,0).
//
// A subset with no cmap at all takes the code as the glyph index: that is what a producer means by
// a subset with no character map to read it with, and where pdfium lands. Glyph 0 is .notdef, so a
// code of 0 is refused like one past the program's glyphs.
//
// A (3,0) subtable that maps a code differently at two ranges states two glyphs for it, and pdfium
// draws the first; §9.6.5.4 says a font's codes lie in one range, so the file has contradicted
// itself and the code is refused.
func (tf *textFont) ttCodeGID(code byte) (uint16, error) {
	tt := tf.tt
	switch {
	case tt.HasSubtable(3, 0):
		var gid uint16
		var at uint32
		for _, hi := range []uint32{0x0000, 0xF000, 0xF100, 0xF200} {
			g, ok := tt.GIDInSubtable(3, 0, hi|uint32(code))
			if !ok {
				continue
			}
			if gid != 0 && g != gid {
				return 0, fmt.Errorf("the (3,0) subtable maps it to glyph %d at 0x%04X and glyph %d "+
					"at 0x%04X", gid, at, g, hi|uint32(code))
			}
			gid, at = g, hi|uint32(code)
		}
		if gid != 0 {
			return gid, nil
		}
	case tt.HasSubtable(1, 0):
		if gid, ok := tt.GIDInSubtable(1, 0, uint32(code)); ok {
			return gid, nil
		}
	case !tt.HasCmap() && code != 0 && int(code) < tt.NumGlyphs():
		return uint16(code), nil
	}
	return 0, errNoGlyph
}

// showText draws one string's glyphs and advances the text matrix.
//
// The advance arithmetic is extract's, deliberately: it is the same question — where does the
// next glyph start — and two answers to it would be two readings of §9.4.4. What differs is what
// happens with the glyph once it is placed, which is the whole of this function.
func (w *walker) showText(m *content.Machine, str []byte) {
	// The survey has already refused the page if there is no font or no program in it, so this is
	// a precondition rather than a check that can fire. Guarded anyway because the alternative to
	// a two-line return is a nil dereference, which is a worse answer than nothing for a caller
	// who reaches this some other way.
	tf := w.font
	if tf == nil || !tf.drawable() {
		return
	}
	ts := &m.GS.Text
	th := ts.Scale / 100
	for _, g := range tf.f.Decode(str) {
		trm := m.RenderMatrix()

		if m.Visible() {
			// Every code resolves, because the survey refused the page otherwise. An outline-less
			// glyph is a space — a real glyph with no ink, which every font carries — and it draws
			// nothing and still advances.
			if gid, err := tf.gid(g); err == nil {
				if out, err := w.outline(tf, gid); err == nil && out != nil {
					w.fillGlyph(out, trm)
				}
			}
		}

		tw := 0.0
		if g.Bytes == 1 && g.Code == 32 {
			tw = ts.WordSpace
		}
		tx := (g.Width/1000*ts.Size + ts.CharSpace + tw) * th
		if tf.f.Vertical {
			m.AdvanceVertical(-tx)
			continue
		}
		m.Advance(tx)
	}
}

// maxGlyphWork bounds what a page's glyph outlines may cost, building and drawing together:
// charstring operations for a CFF or Type 1 program, or visits plus contours plus points for a
// TrueType one, to build — once per distinct glyph per textFont, the way this cache makes it — and
// segments to draw, once per show and not deduplicated the way a build is. Every glyph's segments
// are themselves bounded: a TrueType composite and a charstring both by font's
// maxGlyphSegments (65,536 — charstring.go's appendSeg caps a charstring's the way the composite
// check in truetype.go caps a composite's), and a simple TrueType glyph further by maxGlyphPoints
// (10,000 points). So one show of one glyph costs at most maxGlyphSegments to draw; this is what
// bounds a page of many glyphs, or one glyph shown many times, from adding up to the same stall.
//
// A charstring cannot loop, but nested subroutines that each call others many times make one
// glyph's build exponential in its size, and a TrueType composite fans out the same way.
// The heaviest corpus page's total, build and draw together, is 133,489 (ISO 32000-2 page 161).
const maxGlyphWork = 4 << 20

// outline builds a glyph's outline once per page, charging its build cost to the page's glyph
// work. Charging what a show of it draws is checkText's job, not this function's: outline is
// also how paint fetches the outline the survey already built, and a cache hit here must cost
// paint nothing.
func (w *walker) outline(tf *textFont, gid uint16) (font.Outline, error) {
	if o, ok := tf.outlines[gid]; ok {
		return o.out, o.err
	}
	var o glyphOutline
	switch {
	case w.glyphWork > maxGlyphWork:
		o.err = font.ErrGlyphBudget
	case tf.cff != nil:
		var ops int
		o.out, ops, o.err = tf.cff.Outline(gid, maxGlyphWork-w.glyphWork)
		w.chargeGlyphWork(ops)
	case tf.t1 != nil:
		var ops int
		o.out, ops, o.err = tf.t1.Outline(gid, maxGlyphWork-w.glyphWork)
		w.chargeGlyphWork(ops)
	default:
		var work int
		o.out, work, o.err = tf.tt.Outline(gid, maxGlyphWork-w.glyphWork)
		w.chargeGlyphWork(work)
	}
	if tf.outlines == nil {
		tf.outlines = map[uint16]glyphOutline{}
	}
	tf.outlines[gid] = o
	return o.out, o.err
}

// refuseText records a font problem under a key that names the reason.
//
// Keyed by the reason rather than by the operator, so the refusal a page comes back with says
// "the FontFile3 program did not parse" instead of "Tj" — which over a corpus turns the error
// list into a census of what to implement next.
func (w *walker) refuseText(why string) {
	w.unsup["text: "+why] = true
}

// fillGlyph turns one glyph's outline into a filled path.
//
// The outline is in 1/1000 em with y up, the render matrix takes text space to user space, and
// the walker's base takes user space to device — so the glyph's own thousandth-of-an-em scale is
// the only conversion this function owns. Glyphs fill nonzero: §9.3.6's rendering modes are about
// fill versus stroke versus clip, never about the winding rule, and a TrueType outline's outer and
// inner contours are wound in opposite directions precisely so that nonzero leaves the counters
// open.
func (w *walker) fillGlyph(out font.Outline, trm geom.Matrix) {
	ctm := w.base.mul(geom.Matrix{A: 0.001, D: 0.001}.Mul(trm))
	var p path
	for _, seg := range out {
		switch seg.Op {
		case font.SegMove:
			p.moveTo(ctm.apply(point{seg.P[0].X, seg.P[0].Y}))
		case font.SegLine:
			p.lineTo(ctm.apply(point{seg.P[0].X, seg.P[0].Y}))
		case font.SegQuad:
			// A quadratic raised to a cubic: the two cubic controls sit two thirds of the way
			// from each endpoint toward the quadratic's single control. Exact, not an
			// approximation — every quadratic is a cubic — so the rasterizer needs one curve
			// primitive rather than two.
			c := point{seg.P[0].X, seg.P[0].Y}
			to := point{seg.P[1].X, seg.P[1].Y}
			from := p.cur
			cd := ctm.apply(c)
			td := ctm.apply(to)
			p.curveTo(
				point{from.x + 2.0/3*(cd.x-from.x), from.y + 2.0/3*(cd.y-from.y)},
				point{td.x + 2.0/3*(cd.x-td.x), td.y + 2.0/3*(cd.y-td.y)},
				td)
		case font.SegCubic:
			p.curveTo(
				ctm.apply(point{seg.P[0].X, seg.P[0].Y}),
				ctm.apply(point{seg.P[1].X, seg.P[1].Y}),
				ctm.apply(point{seg.P[2].X, seg.P[2].Y}))
		case font.SegClose:
			p.close()
		}
	}
	if p.empty() {
		return
	}
	w.canvasFor().fill(&p, w.fill, false, w.alpha, w.soft)
}

// showArray handles TJ, whose array mixes strings to show with kerning adjustments.
//
// The adjustment arithmetic is extract's, for the reason showText's comment gives, including the
// sign: a positive adjustment moves the pen *backwards*, because §9.4.3 subtracts it from the
// displacement. Getting that wrong reverses every kerning correction, which per glyph looks like
// sloppy spacing rather than a defect — and on a rendered page looks like a font problem.
//
// The font is resolved even for an array that shows nothing, because which axis an adjustment
// moves is the font's writing mode. A TJ of pure adjustments is rare and legal.
func (w *walker) showArray(m *content.Machine, arr objects.Array) {
	vertical := w.font != nil && w.font.f != nil && w.font.f.Vertical
	ts := &m.GS.Text
	for _, item := range arr {
		switch v := item.(type) {
		case objects.String:
			w.showText(m, v)
		case objects.Int, objects.Real:
			adj, _ := objects.AsNum(v)
			tx := -adj / 1000 * ts.Size * ts.Scale / 100
			if vertical {
				m.AdvanceVertical(tx)
				continue
			}
			m.Advance(tx)
		}
	}
}

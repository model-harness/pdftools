package native

import (
	"fmt"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/font"
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
	why  string // empty when tt is usable
	dict objects.Dict
}

// loadFont resolves a /Font resource name once per page.
func (w *walker) loadFont(name string) *textFont {
	if tf, ok := w.fonts[name]; ok {
		return tf
	}
	tf := &textFont{}
	w.fonts[name] = tf

	fonts, ok := objects.GetDict(w.s, w.res, "Font")
	if !ok {
		tf.why = "the page declares no /Font resources"
		return tf
	}
	dict, ok := objects.GetDict(w.s, fonts, objects.Name(name))
	if !ok {
		tf.why = fmt.Sprintf("/Font /%s is not in the page's resources", name)
		return tf
	}
	tf.dict = dict
	f := font.Load(w.s, dict)
	if f == nil {
		tf.why = fmt.Sprintf("/Font /%s did not load", name)
		return tf
	}
	tf.f = f

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
		// A standard-14 font names a face this package does not carry. Substituting one is a
		// decision about which face, and a wrong substitution is a page that looks right and
		// is set in the wrong type — so it is a refusal until there is a face to substitute.
		tf.why = fmt.Sprintf("/Font /%s has no descriptor: a standard-14 face would have to be substituted", name)
		return tf
	}
	data, ok := objects.GetStreamData(w.s, fd, "FontFile2")
	if !ok {
		switch {
		case fd["FontFile3"] != nil:
			tf.why = "the font program is FontFile3 (CFF), which needs a charstring interpreter"
		case fd["FontFile"] != nil:
			tf.why = "the font program is FontFile (Type 1), which needs a charstring interpreter"
		default:
			tf.why = "the font embeds no program and would have to be substituted"
		}
		return tf
	}
	tt, err := font.ParseTrueType(data)
	if err != nil {
		tf.why = fmt.Sprintf("the FontFile2 program did not parse: %v", err)
		return tf
	}
	tf.tt = tt
	return tf
}

// gid resolves one decoded glyph to an index in the program.
//
// Three routes, because a PDF has three ways of saying which glyph it means (§9.6.5.4, §9.7.4.2):
// a composite font maps its CID through /CIDToGIDMap; a symbolic simple font looks its *code* up
// in the program's (3,0) subtable, because its codes mean nothing outside the font; and a
// non-symbolic simple font goes through the character the encoding gives to the program's Unicode
// subtable. The last is the one that needs the text, which is why a glyph whose text could not be
// decoded is a refusal rather than a blank: a reader that skipped it would drop a character with
// nothing to say so.
func (tf *textFont) gid(g font.Glyph) (uint16, bool) {
	if tf.f.Kind == font.Composite {
		return tf.f.GIDForCID(g.CID)
	}
	if gid, ok := tf.tt.GIDForCode(g.Code); ok {
		return gid, true
	}
	for _, r := range g.Text {
		if gid, ok := tf.tt.GIDForRune(r); ok {
			return gid, true
		}
	}
	// A subset font may carry no usable cmap at all, and then the code *is* the glyph index:
	// that is what a producer means by a subset with no character map to read it with. Taken
	// only when the index is one the program has, so this is a last resort and not a guess that
	// silently draws the wrong glyph.
	//
	// Bounded at 0xFFFF as well as by the glyph count, which is the same bound twice over — `maxp`
	// declares the count in sixteen bits — but stated here so the conversion below is in range by
	// a comparison a reader can see rather than by a fact two files away.
	if g.Code <= 0xFFFF && int(g.Code) < tf.tt.NumGlyphs() {
		return uint16(g.Code), true
	}
	return 0, false
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
	if tf == nil || tf.tt == nil {
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
			if gid, ok := tf.gid(g); ok {
				if out, ok := tf.tt.Outline(gid); ok {
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

// refuseText records a font problem under a key that names the reason.
//
// Keyed by the reason rather than by the operator, so the refusal a page comes back with says
// "the font program is FontFile3 (CFF)" instead of "Tj" — which over a corpus turns the error
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
	w.canvasFor().fill(&p, w.fill, false)
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

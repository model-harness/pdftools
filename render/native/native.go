package native

import (
	"errors"
	"fmt"
	"image"
	"sort"
	"strings"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/font"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
	"github.com/model-harness/pdftools/render"
)

// ErrUnsupported reports that a page draws something this backend cannot draw.
//
// Its own error rather than a generic one, because the caller's reaction is specific: fall back
// to another Rasterizer for that page. Nothing in this repo does that yet — cmd/pdfspec still
// constructs render/pdfium unconditionally — so the sentinel is what a fallback will be built
// on rather than something one already uses, and saying so is the difference between a plan and
// a claim.
var ErrUnsupported = errors.New("render/native: page needs an operator this backend does not implement")

// Unsupported names what stopped a page, so a report can say which feature is missing rather
// than that something was.
//
// The operator names are what the page actually used, deduplicated and sorted, because the
// actionable question when this arrives in a log over a thousand pages is which *feature* to
// implement next — and a list of operators answers it where a count does not.
type Unsupported struct {
	Page int
	Ops  []string
}

func (u *Unsupported) Error() string {
	return fmt.Sprintf("render/native: page %d draws %s, which this backend does not implement yet",
		u.Page, strings.Join(u.Ops, ", "))
}

func (u *Unsupported) Unwrap() error { return ErrUnsupported }

// Rasterizer draws pages with this repo's own code.
//
// It reads through objects.Store rather than bringing a parser, which is the one thing it has
// over the borrowed backend besides the binary size: render/pdfium cannot be handed a Store
// and so parses the file a second time (ADR 0005). Here a caller that has already opened a
// document pays for one parse.
type rasterizer struct {
	s objects.Store

	// faces supplies programs for fonts the document did not embed. nil by default, and a page
	// needing one is then refused with the reason — see FaceSource for why the choice of face is
	// a caller's and not this package's.
	faces font.FaceSource
}

var _ render.Rasterizer = (*rasterizer)(nil)

// New returns a Rasterizer over s.
//
// The interface rather than the concrete type, which is what render/pdfium's Open returns and
// what keeps the two adapters substitutable: a caller that named the type would be choosing a
// backend at compile time, where the whole point of the interface is choosing one at run time.
//
// The Store is borrowed: Close does not close it, because the caller opened it and is still
// using it for text. One consequence worth stating, since render.Rasterizer's own comment says
// implementations are not safe for concurrent use: page-level parallelism needs one Store per
// worker, which spends back some of the single-parse saving this backend has over the borrowed
// one.
func New(s objects.Store, opts ...Option) render.Rasterizer {
	r := &rasterizer{s: s}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *rasterizer) PageCount() int { return r.s.PageCount() }

// Close releases nothing, and says so rather than being absent.
//
// The interface requires it because the WASM backend holds a compiled module and a worker
// pool. This backend holds a borrowed Store and no runtime at all, so there is nothing to
// release — and a nil-returning Close is the honest implementation of that, not a stub.
func (r *rasterizer) Close() error { return nil }

// Page renders page n, or returns an *Unsupported naming what it could not draw.
//
// Refusing rather than part-drawing is the decision this backend rests on. A rasterizer that
// skipped the operators it does not implement would return a page with its text missing and
// no indication — the same failure as a dropped page, which this repo has already paid for
// once, and worse here because a plausible-looking image invites no second look.
func (r *rasterizer) Page(n int, o render.Options) (*render.Raster, error) {
	if n < 1 || n > r.s.PageCount() {
		return nil, fmt.Errorf("render/native: page %d out of range 1..%d", n, r.s.PageCount())
	}
	page, err := r.s.Page(n)
	if err != nil {
		return nil, fmt.Errorf("render/native: page %d: %w", n, err)
	}
	// Annotations are a refusal rather than an omission. render.Options documents the flag as
	// rendering annotation appearance streams, and the borrowed backend honours it; a page with
	// an /Annots array and a caller that asked for them would otherwise come back without them
	// and with nothing to say so, which is the operator-level silent omission one level up.
	if o.Annotations {
		if a, ok := objects.GetArray(r.s, page, "Annots"); ok && len(a) > 0 {
			return nil, &Unsupported{Page: n, Ops: []string{"/Annots"}}
		}
	}

	box, rotate := pageBox(r.s, page), pageRotate(r.s, page)
	// The box a caller is told about is the one the image covers, so a quarter turn swaps it.
	// render/pdfium reports it that way and renders those pixel dimensions, and two backends
	// behind one interface disagreeing about the size of a page would be the worst kind of
	// difference: every caller that maps a coordinate back would be wrong for one of them.
	fitBox := box
	if rotate == 90 || rotate == 270 {
		fitBox = geom.NewRect(box.X0, box.Y0, box.X0+box.Height(), box.Y0+box.Width())
	}
	dpi, pw, ph, err := render.Fit(fitBox, o)
	if err != nil {
		return nil, fmt.Errorf("render/native: page %d: %w", n, err)
	}

	data, err := r.s.PageContent(n)
	if err != nil {
		return nil, fmt.Errorf("render/native: page %d content: %w", n, err)
	}

	res, _ := objects.GetDict(r.s, page, "Resources")
	w := &walker{
		s:        r.s,
		res:      res,
		w:        pw,
		h:        ph,
		base:     pageMatrix(box, dpi/72, rotate),
		unsup:    map[string]bool{},
		fonts:    map[string]*textFont{},
		fontRefs: map[objects.Ref]*textFont{},
		images:   map[objects.Ref]*image.NRGBA{},
		forms:    map[objects.Ref][]byte{},
		faces:    r.faces,
	}
	w.run(data)

	if len(w.unsup) > 0 {
		ops := make([]string, 0, len(w.unsup))
		for op := range w.unsup {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		return nil, &Unsupported{Page: n, Ops: ops}
	}
	return &render.Raster{Number: n, Image: w.canvasFor().img, DPI: dpi, Box: fitBox}, nil
}

// pageBox is the crop box, or the media box, or US Letter.
//
// The same fallback order render/pdfium uses and for the same reason: §14.11.2 makes /CropBox
// the visible region, a page may declare neither, and a rasterizer with no box has nothing to
// scale. Letter rather than A4 because that is what the other backend picks, and two backends
// disagreeing about the size of a malformed page would be worse than either choice.
func pageBox(s objects.Store, page objects.Dict) geom.Rect {
	for _, key := range []objects.Name{"CropBox", "MediaBox"} {
		a, ok := objects.GetArray(s, page, key)
		if !ok || len(a) != 4 {
			continue
		}
		var v [4]float64
		bad := false
		for i, o := range a {
			f, ok := objects.AsNum(o)
			if !ok {
				bad = true
				break
			}
			v[i] = f
		}
		if bad {
			continue
		}
		r := geom.NewRect(v[0], v[1], v[2], v[3])
		if r.X1 > r.X0 && r.Y1 > r.Y0 {
			return r
		}
	}
	return geom.NewRect(0, 0, 612, 792)
}

// pageRotate is /Rotate normalized to 0, 90, 180 or 270.
//
// §7.7.3.3 requires a multiple of 90 and permits a negative one, so -90 is 270 and 450 is 90. A
// value that is not a multiple of 90 is a producer error with no defensible reading, and it is
// taken as 0 rather than refused: the page's content is still there, and the alternative is
// refusing a page over a number nothing in the content stream depends on.
func pageRotate(s objects.Store, page objects.Dict) int {
	v, ok := objects.GetInt(s, page, "Rotate")
	if !ok {
		return 0
	}
	r := int(v) % 360
	if r < 0 {
		r += 360
	}
	if r%90 != 0 {
		return 0
	}
	return r
}

// walker interprets a content stream into fills on a canvas.
//
// It carries content.Machine for the graphics state — the CTM, the clip depth, the line width
// — because that machine is already the repo's reading of §8.4 and a second one here would be
// a second set of answers to the same questions. What it adds is the part a machine cannot
// have: the path being built and the colour to fill it with.
type walker struct {
	// canvas is created on the first mark rather than up front, so a page that will be refused
	// does not pay for it. A page-sized RGBA plus an opaque clip mask is about 10 MB at 200 dpi
	// — 7.8 million byte writes — and every one of the corpus's 1,251 pages is refused today, so
	// allocating eagerly was most of what the refusal pass cost: 31.5 s at the default DPI
	// against 0.45 s to parse and read the streams.
	canvas *canvas
	w, h   int

	base matrix

	// s and res are the page's store and resource dictionary, which text needs and paths do
	// not: a glyph is looked up in a font the stream names, and the name only means something
	// against /Resources /Font.
	s   objects.Store
	res objects.Dict

	// fonts caches what a /Font name resolved to, per page. A page names the same font at
	// every Tf — a spec page has hundreds — and parsing a font program per Tf would parse the
	// same tens of kilobytes hundreds of times.
	fonts map[string]*textFont
	font  *textFont

	// faces is the rasterizer's face source, carried down so loadFont can ask for a substitute.
	faces font.FaceSource

	// fill is the only colour kept. A stroke colour is accepted and discarded: setting one
	// marks nothing, so refusing G/RG/K would refuse a page over an operator that changed no
	// pixel, and storing it would be state no code reads while stroking itself is refused.
	// It comes back with the stroke.
	fill paint
	path path

	// alpha is the constant fill alpha /ca sets.
	alpha float64

	// pending is a clip the *next* painting operator must apply, and pendingEO the rule it was
	// marked with. Held rather than applied at W, because §8.5.4 makes the clip the path as it
	// stands when the path object ends: "W f" fills the path and then clips with it.
	pending   bool
	pendingEO bool

	// stack is what q saved and Q restores: the part of §8.4.2's graphics state this walker
	// holds itself. content.Machine stacks the CTM and the text state; the fill colour, the
	// resolved font, the alpha and the clip live here, because the machine has no mask, reads no
	// ExtGState and resolves no font — each of those needs the resource dictionary, which content
	// deliberately does not take.
	stack []saved

	// fontRefs caches a parsed font by its indirect reference, across every resource dictionary
	// the page reaches. fonts is keyed by resource *name*, and a name only means something within
	// one dictionary — a form's /F1 need not be the page's — so each form gets a fresh name map,
	// and this is what keeps a form drawn a hundred times from parsing its font a hundred times.
	fontRefs map[objects.Ref]*textFont

	// images caches decoded pixels by indirect reference, for the same reason, and pixels
	// counts what has been decoded so far against maxPagePixels.
	images map[objects.Ref]*image.NRGBA
	pixels int

	// forms caches a form's decoded content by indirect reference; see formContent for why it is
	// not simply read off the stream.
	forms map[objects.Ref][]byte

	// ops counts operators surveyed across the page and every form it reaches, against maxOps.
	ops int

	unsup map[string]bool
}

// saved is one q's worth of the state this walker holds.
//
// One struct rather than a stack per parameter, which was the shape until form XObjects needed a
// real q: separate stacks for the clip and the alpha, and none for the colour or the font, so
// "q 1 0 0 rg Q … f" filled red and a Tf inside q…Q stayed in force after it. A parameter added
// to the graphics state is now one field here, not one more stack to remember to pop.
type saved struct {
	// clip is nil when no clip was in force, so an unclipped q costs no page-area copy. Measured
	// before it was stacked at all: a clip set inside q…Q confined everything after the Q too, and
	// "q 0 0 20 20 re W n Q 0 0 200 200 re f" came out at 1% of its ink against pdfium.
	clip  *mask
	fill  paint
	font  *textFont
	alpha float64
}

// canvasFor returns the canvas, creating it on first use.
func (w *walker) canvasFor() *canvas {
	if w.canvas == nil {
		w.canvas = newCanvas(w.w, w.h)
	}
	return w.canvas
}

// run surveys the stream for what it cannot draw, and paints only if the answer is nothing.
//
// Two passes over the same bytes, and the first is why a refused page is cheap. Painting until
// the first unsupported operator turns up meant a page of paths and text — which is 893 of the
// corpus's 1,251 — paid for every path before its first Tj: 32.6 s over the corpus at the default
// DPI, against 5.5 s when nothing is painted. Lexing the stream twice costs a tenth of that for
// the pages that *can* be drawn, and nothing for the pages that cannot.
//
// It also removes a guard that could not be tested. Checking len(unsup) inside each painting
// operator was the previous shape, and no assertion could see it: the image is discarded either
// way, so the only observable was wall time.
func (w *walker) run(data []byte) {
	w.alpha = 1
	w.survey(content.NewMachine(geom.Identity), data, 0)
	if len(w.unsup) > 0 {
		return
	}
	// The survey walked the same q/Q and Tf sequence and left its state behind; the paint pass
	// starts from the page's initial state, not from wherever the survey ended.
	w.fill, w.alpha, w.font, w.stack = black, 1, nil, nil
	w.paint(content.NewMachine(geom.Identity), data, 0)
}

// paint interprets one content stream — the page's, or a form's at depth > 0 — onto the canvas.
func (w *walker) paint(m *content.Machine, data []byte, depth int) {
	sc := content.NewScanner(data)
	for {
		op, ok := sc.Next()
		if !ok {
			return
		}
		// The machine first, then this walker, and both unconditionally. Apply reports whether
		// it handled an operator, and for two of them the answer is "yes" while this walker
		// still needs to see it: W and W* are graphics state to the machine — a clip depth —
		// and a *path* here. Skipping on Apply's word dropped every clip on the page, which the
		// clip test caught.
		m.Apply(op)
		w.op(m, op, depth)
	}
}

// survey records every reason this backend cannot draw the page, without drawing anything.
//
// Every reason rather than the first, because the error is a worklist: over a thousand pages
// the actionable question is which feature to implement next, and only the full list answers it.
// Stopping early would have reported Tj for every page in the corpus and hidden that gs blocks
// 1,184 of them.
//
// # Why this resolves fonts and glyphs rather than only lexing
//
// A show operator is not refused for being a show operator — it is refused when the font behind it
// has no program this backend reads, and that is a *reason* the caller can act on. Deciding it
// needs the resource dictionary and the font program, so the first version left it to the paint
// pass, where showText already had to know. That made the list silently incomplete in exactly the
// case it exists for: the paint pass never runs when anything else blocked the page, so a page
// with an ExtGState and a CFF font reported `gs` alone. Over the corpus, where gs blocks 1,184 of
// 1,251 pages, the font census — the thing that ranks the increments after this one — would have
// been taken from the 67 pages that happened to have nothing else wrong with them.
//
// The cost is the same font parse the paint pass would have done, cached per resource name, and
// the machine runs here too so that a glyph is only required where it would actually be drawn:
// §9.3.6's render mode 3 is how a scanned page's invisible OCR layer is written, and demanding a
// glyph for text nobody draws would refuse a page over characters that mark nothing.
func (w *walker) survey(m *content.Machine, data []byte, depth int) {
	sc := content.NewScanner(data)
	for {
		op, ok := sc.Next()
		if !ok {
			return
		}
		w.ops++
		if w.ops > maxOps {
			w.unsup[fmt.Sprintf("Do: the page and its forms run past %d operators", maxOps)] = true
			return
		}
		m.Apply(op)
		if marks(op.Name) {
			w.unsup[op.Name] = true
			continue
		}
		switch op.Name {
		case "q":
			w.save()
		case "Q":
			w.restore()
		case "Do":
			if len(op.Operands) >= 1 {
				w.surveyXObject(m, op.NameAt(0), depth)
			}
		case "gs":
			// Resolved and checked here rather than in the paint pass, for the reason the font
			// reasons are: the paint pass never runs when anything else blocked the page, so a
			// reason decided there is missing from the worklist exactly when the worklist matters.
			if len(op.Operands) >= 1 {
				name := objects.Name(op.NameAt(0))
				if g, ok := w.extGState(name); ok {
					w.checkExtGState(g)
					w.setAlpha(g)
				} else {
					w.unsup[fmt.Sprintf("gs: /%s is not in the page's /ExtGState resources", name)] = true
				}
			}
		case "Tf":
			if len(op.Operands) >= 1 {
				w.font = w.loadFont(string(op.NameAt(0)))
			}
		case "Tj", "'":
			if len(op.Operands) >= 1 && m.Visible() {
				w.checkText(op.Str(0))
			}
		case "\"":
			if len(op.Operands) >= 3 && m.Visible() {
				w.checkText(op.Str(2))
			}
		case "TJ":
			if len(op.Operands) >= 1 && m.Visible() {
				for _, item := range op.Arr(0) {
					if s, ok := item.(objects.String); ok {
						w.checkText(s)
					}
				}
			}
		}
	}
}

// checkText records why a string cannot be drawn, without drawing it.
//
// The same three questions showText would ask — is there a font, does it carry a program this
// backend reads, and does every code in the string resolve to a glyph — asked where the answer can
// still reach the caller. showText does not ask them again: painting happens only when this
// returned nothing, so by then each is settled.
func (w *walker) checkText(str []byte) {
	tf := w.font
	switch {
	case tf == nil:
		w.refuseText("the page shows a string before naming a font with Tf")
		return
	case tf.tt == nil:
		w.refuseText(tf.why)
		return
	}
	for _, g := range tf.f.Decode(str) {
		if _, ok := tf.gid(g); !ok {
			// A code with no glyph is a character the page draws and this backend cannot, which
			// is the same class as an operator it cannot draw and gets the same answer. Dropping
			// it would put a page on screen with one character quietly missing — the failure
			// mode this whole backend refuses pages to avoid, at the granularity where it is
			// hardest to notice.
			w.refuseText(fmt.Sprintf("no glyph for code %d in /%s", g.Code, tf.f.BaseFont))
		}
	}
}

// setAlpha applies an ExtGState's /ca, the one parameter of it this backend reads.
//
// Every other parameter either cannot change a pixel this backend draws or has already refused the
// page in the survey, so this is the whole of applying an ExtGState — and checkExtGState's comment
// is where that claim is argued.
func (w *walker) setAlpha(g objects.Dict) {
	if v, ok := objects.GetNum(w.s, g, "ca"); ok {
		w.alpha = v
	}
}

// extGState resolves a named ExtGState from the current resources.
func (w *walker) extGState(name objects.Name) (objects.Dict, bool) {
	egs, ok := objects.GetDict(w.s, w.res, "ExtGState")
	if !ok {
		return nil, false
	}
	return objects.GetDict(w.s, egs, name)
}

// checkExtGState records every parameter of an ExtGState this backend cannot honour.
//
// # Why an allow-list of keys rather than a list of refusals
//
// The same reason ADR 0015 inverted the operator list after an inline image was drawn as nothing:
// an enumeration of what to refuse is wrong the first time a key appears that nobody enumerated,
// and it is wrong *silently*, by drawing a page whose graphics state it did not understand. So a
// key this function has not been taught is a refusal, and every acceptance below carries the
// reason it is safe.
//
// # What is honoured, and what marks nothing
//
// /ca is honoured: it is the constant alpha a fill composites with (§11.6.4.4), and multiplying it
// into coverage is exact over an opaque backdrop under the normal blend mode, which is what this
// canvas has.
//
// The rest are accepted because they cannot change a pixel this backend draws, and each is a
// distinct argument rather than one waved at the group:
//
//   - /CA, /LW, /LC, /LJ, /ML, /D, /SA set stroke alpha and pen geometry, and every stroking
//     operator is refused. This is ADR 0015's reasoning for accepting G, RG and K — refusing a
//     page over state no drawn pixel reads would refuse it over nothing.
//   - /OP, /op, /OPM are overprint, which controls how colorants combine on a device that has
//     separations (§11.7.4.5). This backend composites RGB and has none.
//   - /BG, /BG2, /UCR, /UCR2 are black generation and undercolour removal, which apply when a
//     device converts to CMYK. This backend emits RGB.
//   - /HT, /FL, /SM are a halftone screen and flatness and smoothness tolerances: hints for a
//     device that screens or flattens, with no required effect on a continuous-tone raster.
//   - /RI is a rendering intent, which selects a gamut mapping. Only device colour spaces are
//     supported here, and ADR 0015 already declines to colour-manage.
//   - /TK is text knockout, which changes how glyphs composite *against each other* inside a
//     transparency group. With alpha 1 and the normal blend mode there is nothing to knock out,
//     and a non-normal blend mode is refused below, so the case where it matters cannot arrive.
//   - /AIS makes alpha come from a soft mask's shape instead of its alpha, and a soft mask is
//     refused below, so it likewise cannot matter here.
//   - /Type is /ExtGState.
func (w *walker) checkExtGState(g objects.Dict) {
	refuse := func(f string, a ...any) { w.unsup["gs: "+fmt.Sprintf(f, a...)] = true }

	for k := range g {
		switch k {
		case "Type", "CA", "LW", "LC", "LJ", "ML", "D", "SA",
			"OP", "op", "OPM", "BG", "BG2", "UCR", "UCR2",
			"HT", "FL", "SM", "RI", "TK", "AIS":
			// Accepted; see the comment above for the argument per key.

		case "ca":
			// Any value in 0..1 is honoured. Outside that range is a producer error with no
			// reading — §11.6.4.4 defines it as a number in that interval — and clamping it
			// silently would make a malformed page indistinguishable from a valid one.
			v, ok := objects.GetNum(w.s, g, "ca")
			if !ok {
				refuse("/ca is not a number")
			} else if v < 0 || v > 1 {
				refuse("/ca is %g, outside 0..1", v)
			}

		case "BM":
			// A blend mode is a per-pixel function of backdrop and source (§11.3.5). Normal is
			// the identity this canvas already implements; Compatible is Normal under another
			// name, kept for PDF 1.3 files. An array picks the first supported entry, which for
			// this backend is the first that is one of those two.
			for _, nm := range blendNames(w.s, g) {
				if nm != "Normal" && nm != "Compatible" {
					refuse("/BM is /%s, and only /Normal is implemented", nm)
				}
			}

		case "SMask":
			// /None is the absence of a soft mask. A dictionary is a luminosity or alpha group
			// that has to be rendered and then used as a per-pixel mask — a page of its own —
			// and compositing one wrongly produces an image that looks plausible.
			if nm, ok := objects.GetName(w.s, g, "SMask"); !ok || nm != "None" {
				refuse("/SMask is a soft mask group")
			}

		case "Font":
			// An ExtGState can set the font and size, which every show operator after it then
			// uses (§8.4.5). Nothing reads it here, so honouring the page would need the same
			// resolution Tf does; refused rather than ignored, because ignoring it draws the
			// page in whatever font Tf last named.
			refuse("/Font sets the text font from the graphics state")

		default:
			refuse("/%s is not a parameter this backend reads", k)
		}
	}
}

// blendNames reads /BM, which is a name or an array of names in preference order (§11.6.3).
func blendNames(s objects.Store, g objects.Dict) []objects.Name {
	if nm, ok := objects.GetName(s, g, "BM"); ok {
		return []objects.Name{nm}
	}
	arr, ok := objects.GetArray(s, g, "BM")
	if !ok {
		return []objects.Name{"(not a name)"}
	}
	var out []objects.Name
	for _, o := range arr {
		if nm, ok := o.(objects.Name); ok {
			out = append(out, nm)
		}
	}
	if len(out) == 0 {
		return []objects.Name{"(an array of no names)"}
	}
	return out
}

func (w *walker) op(m *content.Machine, op content.Op, depth int) {
	ctm := w.base.mul(m.GS.CTM)
	switch op.Name {
	// Path construction.
	case "m":
		if len(op.Operands) >= 2 {
			w.path.moveTo(ctm.apply(point{op.Num(0), op.Num(1)}))
		}
	case "l":
		if len(op.Operands) >= 2 {
			w.path.lineTo(ctm.apply(point{op.Num(0), op.Num(1)}))
		}
	case "c":
		if len(op.Operands) >= 6 {
			w.path.curveTo(
				ctm.apply(point{op.Num(0), op.Num(1)}),
				ctm.apply(point{op.Num(2), op.Num(3)}),
				ctm.apply(point{op.Num(4), op.Num(5)}))
		}
	case "v":
		// The first control point is the current point (§8.5.2.2), so the curve leaves the
		// pen in the direction it was already going.
		if len(op.Operands) >= 4 {
			c2 := ctm.apply(point{op.Num(0), op.Num(1)})
			to := ctm.apply(point{op.Num(2), op.Num(3)})
			w.path.curveTo(w.path.cur, c2, to)
		}
	case "y":
		// The second control point is the endpoint, so the curve arrives straight.
		if len(op.Operands) >= 4 {
			c1 := ctm.apply(point{op.Num(0), op.Num(1)})
			to := ctm.apply(point{op.Num(2), op.Num(3)})
			w.path.curveTo(c1, to, to)
		}
	// Text. The show operators draw; Tf chooses the font the page names.
	case "Tf":
		if len(op.Operands) >= 1 {
			w.font = w.loadFont(string(op.NameAt(0)))
		}
	case "Tj":
		if len(op.Operands) >= 1 {
			w.showText(m, op.Str(0))
		}
	case "'":
		// A quote shows a string on the next line, and content.Machine has already moved the
		// text matrix there by the time this runs.
		if len(op.Operands) >= 1 {
			w.showText(m, op.Str(0))
		}
	case "\"":
		// Word and character spacing come first, and the machine applies them.
		if len(op.Operands) >= 3 {
			w.showText(m, op.Str(2))
		}
	case "TJ":
		if len(op.Operands) >= 1 {
			w.showArray(m, op.Arr(0))
		}

	case "h":
		w.path.close()
	case "re":
		if len(op.Operands) >= 4 {
			w.path.rect(op.Num(0), op.Num(1), op.Num(2), op.Num(3), ctm)
		}

	// Colour. Only the device spaces; see paint.go for why the rest are refused.
	case "g":
		if len(op.Operands) >= 1 {
			w.fill = gray(clamp01(op.Num(0)))
		}
	case "rg":
		if len(op.Operands) >= 3 {
			w.fill = rgb(clamp01(op.Num(0)), clamp01(op.Num(1)), clamp01(op.Num(2)))
		}
	case "k":
		if len(op.Operands) >= 4 {
			w.fill = cmyk(op.Num(0), op.Num(1), op.Num(2), op.Num(3))
		}

	// Clipping. W and W* mark the path; the paint operator that follows establishes it.
	case "W", "W*":
		w.pending, w.pendingEO = true, op.Name == "W*"

	// Painting.
	case "f", "F":
		w.paintPath(false)
	case "f*":
		w.paintPath(true)
	case "n":
		w.endPath()
	case "b", "b*", "B", "B*", "s", "S":
		// Stroking is a pen geometry problem — join style, cap style, dash phase, and a
		// width that is a distance in user space — and none of it is this rasterizer's yet.
		// A filled-and-stroked operator is refused rather than filled, because the fill
		// alone is a different mark from the one the page makes.
		w.unsup[op.Name] = true
		w.endPath()

	case "gs":
		if len(op.Operands) >= 1 {
			if g, ok := w.extGState(objects.Name(op.NameAt(0))); ok {
				w.setAlpha(g)
			}
		}

	case "q":
		w.save()
	case "Q":
		w.restore()
	case "Do":
		if len(op.Operands) >= 1 {
			w.drawXObject(m, op.NameAt(0), depth)
		}
	}
}

// save pushes the state q saves.
//
// Called from the survey as well as the paint pass, because the font and the alpha decide what the
// survey refuses: a string shown after the Q that ended its Tf has no font, and a transparency group
// drawn after the Q that ended its /ca is drawn opaque. The clip is only ever non-nil when painting,
// since the survey creates no canvas.
func (w *walker) save() {
	s := saved{fill: w.fill, font: w.font, alpha: w.alpha}
	if w.canvas != nil && w.canvas.clipped {
		s.clip = w.canvas.clip.clone()
	}
	w.stack = append(w.stack, s)
}

// restore pops what save pushed. An unbalanced Q is a no-op, as it is in content.Machine.
func (w *walker) restore() {
	n := len(w.stack)
	if n == 0 {
		return
	}
	s := w.stack[n-1]
	w.stack = w.stack[:n-1]
	w.fill, w.font, w.alpha = s.fill, s.font, s.alpha
	// Only when something actually has to change. A q…Q pair that set no clip is the
	// overwhelming majority — a spec page has hundreds — and rebuilding a page-sized opaque mask
	// for each of them cost more than everything else this walker does put together: 43 s over
	// the corpus against 6 s for doing nothing.
	switch {
	case s.clip != nil:
		c := w.canvasFor()
		c.clip, c.clipped = s.clip, true
	case w.canvas != nil && w.canvas.clipped:
		w.canvas.clip = opaqueMask(w.canvas.clip.w, w.canvas.clip.h)
		w.canvas.clipped = false
	}
}

// marks reports whether an operator can put ink on the page, which is the same question as
// whether this backend must refuse a page that uses one it does not implement.
//
// An allow-list of the operators that mark *nothing*, and everything else marks. The inverse —
// a list of what to refuse — is exactly how an inline image came to be drawn as nothing:
// content.Scanner reports BI as "INLINE_IMAGE", so a list naming "BI" refused nothing, a page
// whose only content was an inline image rendered blank, and no test could see it because the
// entry had never been reachable. A list of what is safe is the only shape that survives an
// operator this package has not heard of.
//
// The stroke colour operators are safe: a stroke colour marks nothing while every operator that
// would use it is refused, so refusing a page for setting one would refuse it over a change no
// pixel can see. The *fill* colour space operators are not — a Pattern fill colour changes what
// f paints — so cs and scn are refusals.
func marks(op string) bool {
	switch op {
	case // path construction and the painting operators this backend implements
		"m", "l", "c", "v", "y", "h", "re", "f", "F", "f*", "n", "W", "W*",
		// text, whose refusal is a property of the *font* rather than of the operator: a
		// glyf program is drawn and a CFF one is refused, and only the paint pass can tell
		// which a page holds. The survey lets these through and showText refuses by reason.
		"Tj", "TJ", "'", "\"",
		// fill colour in the device spaces
		"g", "rg", "k",
		// graphics state. gs is here for the same reason the show operators are: it marks
		// nothing by itself, and whether what it *sets* can be drawn is a property of the
		// ExtGState's keys rather than of the operator. checkExtGState decides it by reason.
		"q", "Q", "cm", "w", "J", "j", "M", "d", "ri", "i", "gs",
		// XObjects, for the same reason again: Do marks what the XObject it names marks, so the
		// survey resolves it and refuses by what it found — see surveyXObject.
		"Do",
		// text state, which positions nothing until a show operator
		"BT", "ET", "Tf", "Tc", "Tw", "Tz", "TL", "Ts", "Tr", "Td", "TD", "Tm", "T*",
		// marked content, compatibility, and stroke colour
		"BMC", "BDC", "EMC", "MP", "DP", "BX", "EX",
		"G", "RG", "K", "CS", "SCN", "SC":
		return false
	}
	return true
}

// paintPath fills the current path and applies any clip the stream marked.
//
// Nothing is painted once the page is known to be refused. The walk still runs to the end —
// lexing a stream is cheap and the list of *everything* missing is what makes the error a
// worklist rather than a complaint — but rasterizing for an image that will not be returned is
// the expensive half, and skipping it took the corpus-wide refusal pass from minutes to
// seconds.
func (w *walker) paintPath(evenOdd bool) {
	if !w.path.empty() {
		w.canvasFor().fill(&w.path, w.fill, evenOdd, w.alpha)
	}
	w.endPath()
}

// endPath clears the path and installs a pending clip.
//
// The clip takes effect *after* the painting operator that ends the path (§8.5.4), which is
// why it is applied here and not at W: a W n sequence clips everything after it, and a W f
// fills the path first and clips with it afterwards.
func (w *walker) endPath() {
	if w.pending {
		c := w.canvasFor()
		c.clip.intersect(w.path.rasterize(c.clip.w, c.clip.h, w.pendingEO))
		c.clipped = true
	}
	w.pending = false
	w.path = path{}
}

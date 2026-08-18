package extract

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/font"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// run is one page's extraction in progress.
//
// Text arrives as glyphs and leaves as fragments: a fragment is a maximal run of
// glyphs sharing a style, a baseline, and a marked-content region, with inferred
// spaces already in its text. Accumulating at that granularity rather than per
// glyph is what keeps a 1,023-page document from allocating a struct per character
// — and it is also where the space decision belongs, since the evidence for it is
// the gap between two consecutive glyphs and nothing wider.
type run struct {
	ex  *Extractor
	tol geom.Tolerance

	// page is the 1-based page being walked, stamped onto every span this run emits so
	// that a consumer regrouping spans across pages can tell two page coordinate spaces
	// apart. Held here rather than passed down because the position it qualifies is
	// already held here: everything in this struct is measured in one page's user space,
	// and a run that did not know which page that was could still emit a Box.
	page int

	// lines are closed lines in the order they were first drawn.
	lines []line

	// open is the line currently being drawn into, if any.
	open *line

	// haveFrag reports whether open has a fragment being appended to. The fragment
	// itself is always open.frags[len-1] and is reached through cur(), never held as
	// a pointer: appending the next fragment can move the backing array, and a
	// retained pointer would then address the abandoned copy, silently discarding
	// every glyph written through it.
	haveFrag bool

	// pen is where the next glyph is expected to start, in along-axis units, and
	// is the baseline against which a gap becomes a space. It is the position after
	// the previous glyph's own advance and its Tc/Tw, so a gap here is displacement
	// the text state did not account for — which is precisely what a producer emits
	// instead of a space glyph.
	pen float64

	// havePen reports whether pen is meaningful yet.
	havePen bool

	// path is the path currently being constructed, and rules the axis-aligned
	// segments every painted path has contributed. See rules.go for why a path is
	// tracked here rather than in content.Machine.
	path  path
	rules []doc.Rule
}

// frag is a run of glyphs sharing a style and a baseline.
//
// text is a byte slice rather than a strings.Builder because fragments are held in
// slices that get appended to and sorted, and both copy their elements. A Builder
// panics when written after a copy, by design, so it cannot live in a value that
// moves.
type frag struct {
	text []byte

	// along0 and along1 bound the fragment on its baseline axis; cross is its
	// signed distance from the origin perpendicular to that axis. For unrotated
	// text these are x0, x1, and y.
	along0, along1 float64
	cross          float64

	// orient buckets the baseline direction so rotated text is not grouped with
	// horizontal text on a numerically similar cross value.
	orient int

	// height is the largest on-page glyph height in the fragment, used as the
	// line-height estimate for paragraph breaks.
	height float64

	// space is the nominal advance of the font's space glyph in page units at the
	// size the fragment is set in. This is the denominator of the gap test, and
	// carrying it per fragment rather than per page is why a footnote in 7-point
	// type gets the same treatment as 11-point body text.
	space float64

	style doc.Style
	mcid  int

	// artifact reports that the fragment was drawn inside an Artifact
	// marked-content region.
	artifact bool

	// cuts are the wide gaps inside this fragment, in text order: places a rule may
	// later turn into a cell boundary.
	//
	// Recorded during the walk and resolved after it, because a page's rules are not
	// known while its text is being drawn. LaTeX draws a row's vertical rules just
	// before that row's text, and a producer emitting rectangles draws them wherever
	// it likes — reference/table.pdf interleaves the two, so a decision made at the
	// gap would be reading a partially built grid.
	cuts []cut

	// apart marks a fragment that split off from the one before it at a rule, and
	// must not be merged back into it by appendLine. Without it the split would be
	// undone immediately: two cells of one row share a style and a marked-content
	// identifier, which is exactly appendLine's test for one span.
	apart bool
}

// cut is a wide gap inside a fragment: the byte offset in its text where the gap
// falls, and the interval on the along axis that the gap spans.
//
// The offset points at the byte after the space the gap produced, so splitting there
// leaves the space on the text before it — which is the trailing-placement rule
// appendLine already applies to a wrapped line, for the same reason.
type cut struct {
	off    int
	x0, x1 float64
}

// line is a set of fragments sharing a baseline.
type line struct {
	frags  []frag
	cross  float64
	orient int
}

// walk interprets a content stream, appending everything it draws to r.
//
// res may be nil: a page or form with no resource dictionary can still draw text,
// and every font lookup then fails, which loses the glyphs but not the rest of the
// stream.
func (r *run) walk(data []byte, res objects.Dict, ctm geom.Matrix, depth int) {
	if depth > maxFormDepth {
		return
	}

	m := content.NewMachine(ctm)
	r.walkWith(m, data, res, depth)
}

// walkWith runs a stream on an existing machine, so a form inherits the text state
// of the stream that invoked it.
func (r *run) walkWith(m *content.Machine, data []byte, res objects.Dict, depth int) {
	sc := content.NewScanner(data)
	// fonts caches this stream's resolved font resources. The same Tf name recurs
	// thousands of times per page, and each lookup would otherwise walk the
	// resource dictionary again.
	fonts := map[content.Name]*font.Font{}

	for {
		op, ok := sc.Next()
		if !ok {
			return
		}

		if m.Apply(op) {
			// A BDC whose property list is a name refers to the page's /Properties
			// resource, which the machine cannot resolve on its own. Left unresolved
			// the region has no MCID, and every fragment inside it is unattributable
			// to the structure tree — which is the whole join the tagged path runs on.
			if op.Name == "BDC" {
				if n := op.NameAt(1); n != "" {
					if id, ok := r.propertiesMCID(res, n); ok {
						m.SetMCID(id)
					}
				}
			}
			continue
		}

		// Paths are read after the state operators and before the text ones, because
		// "W n" is a clip rather than ink and the machine has to see the W first. The
		// path operators overlap no text operator, so the order between these two is
		// otherwise free.
		if r.applyPath(m, op) {
			continue
		}

		switch op.Name {
		case "Tj":
			r.show(m, res, fonts, op.Str(0))
		case "TJ":
			r.showArray(m, res, fonts, op.Arr(0))
		case "'":
			m.NextLine()
			r.show(m, res, fonts, op.Str(0))
		case `"`:
			// The operands set word and character spacing before showing, and they
			// persist afterwards (§9.4.3).
			m.GS.Text.WordSpace = op.Num(0)
			m.GS.Text.CharSpace = op.Num(1)
			m.NextLine()
			r.show(m, res, fonts, op.Str(2))
		case "Do":
			r.doXObject(m, res, op.NameAt(0), depth)
		}
	}
}

// propertiesMCID resolves a named BDC property list to its /MCID.
func (r *run) propertiesMCID(res objects.Dict, name objects.Name) (int, bool) {
	if res == nil {
		return 0, false
	}
	props, ok := objects.GetDict(r.ex.s, res, "Properties")
	if !ok {
		return 0, false
	}
	d, ok := objects.GetDict(r.ex.s, props, name)
	if !ok {
		return 0, false
	}
	id, ok := objects.GetInt(r.ex.s, d, "MCID")
	if !ok {
		return 0, false
	}
	return int(id), true
}

// doXObject executes a Form XObject's content stream.
//
// Descending is not optional. A form carries its own /Resources and the text drawn
// inside it names fonts from there; a reader that stops at the page dictionary
// returns nothing for every form, and the corpus tests for the font package
// measured 8 fonts that appear nowhere but inside one. Image XObjects are ignored
// here — they belong to the image path.
func (r *run) doXObject(m *content.Machine, res objects.Dict, name objects.Name, depth int) {
	if depth >= maxFormDepth || res == nil || name == "" {
		return
	}
	xobjs, ok := objects.GetDict(r.ex.s, res, "XObject")
	if !ok {
		return
	}
	v, ok := objects.Get(r.ex.s, xobjs, name)
	if !ok {
		return
	}
	st, isStream := v.(*objects.Stream)
	if !isStream {
		return
	}
	if sub, _ := objects.GetName(r.ex.s, st.Dict, "Subtype"); sub != "Form" {
		return
	}
	if st.Decoded == nil {
		if err := r.ex.s.Decode(st); err != nil || st.Decoded == nil {
			return
		}
	}

	// The form's /Matrix maps form space to the space of the stream that invoked
	// it, so it applies before the current CTM.
	ctm := m.GS.CTM
	if arr, ok := objects.GetArray(r.ex.s, st.Dict, "Matrix"); ok && len(arr) == 6 {
		var v [6]float64
		bad := false
		for i := range v {
			o, err := r.ex.s.Resolve(arr[i])
			if err != nil {
				bad = true
				break
			}
			f, isNum := objects.AsNum(o)
			if !isNum {
				bad = true
				break
			}
			v[i] = f
		}
		if !bad {
			fm := geom.Matrix{A: v[0], B: v[1], C: v[2], D: v[3], E: v[4], F: v[5]}
			ctm = fm.Mul(ctm)
		}
	}

	// A form with no /Resources inherits the invoking stream's (§8.10.1). Falling
	// back rather than giving up is what recovers the text of every form that
	// relies on that inheritance.
	inner := res
	if d, ok := objects.GetDict(r.ex.s, st.Dict, "Resources"); ok {
		inner = d
	}

	// The form executes as though inside q/Q with its own CTM, inheriting the
	// current text state: a Tf before the Do still applies inside.
	sub := content.NewMachine(ctm)
	sub.GS.Text = m.GS.Text
	sub.GS.ClipDepth = m.GS.ClipDepth
	r.walkWith(sub, st.Decoded, inner, depth+1)
}

// showArray handles TJ, whose array mixes strings to show with adjustments.
func (r *run) showArray(m *content.Machine, res objects.Dict, fonts map[content.Name]*font.Font, arr objects.Array) {
	ts := &m.GS.Text
	// Which axis an adjustment moves is the font's writing mode, so the font has to
	// be resolved even for an array that shows no string — a TJ of pure adjustments
	// is rare but legal, and moving the wrong axis would displace everything after
	// it.
	vertical := false
	if f := r.font(m, res, fonts); f != nil {
		vertical = f.Vertical
	}

	for _, item := range arr {
		switch v := item.(type) {
		case objects.String:
			r.show(m, res, fonts, v)
		case objects.Int, objects.Real:
			adj, _ := objects.AsNum(v)
			// A positive adjustment moves the pen *backwards*, because the value is
			// subtracted from the displacement (§9.4.3). Getting the sign wrong
			// reverses every kerning correction, which is small enough per glyph to
			// look like sloppy spacing rather than a bug.
			tx := -adj / 1000 * ts.Size * ts.Scale / 100
			if vertical {
				m.AdvanceVertical(tx)
			} else {
				m.Advance(tx)
			}
			// The pen is deliberately not moved with it. A TJ adjustment is the most
			// common way a producer emits a space — a wide negative number between two
			// strings instead of a space glyph — so it has to reach the gap test as an
			// unexplained displacement. Tracking it here would explain it away and
			// suppress exactly the spaces this package exists to recover.
			//
			// Nothing is emitted here either: whether the gap is a space is decided by
			// the next glyph against the same threshold used everywhere else, so an
			// ordinary kern of a few thousandths falls below it and a wide one does
			// not. Deciding it here as well is how a reader emits two spaces for one
			// wide kern.
		}
	}
}

// font resolves the current Tf resource, caching per stream.
func (r *run) font(m *content.Machine, res objects.Dict, fonts map[content.Name]*font.Font) *font.Font {
	name := m.GS.Text.Font
	f, cached := fonts[name]
	if !cached {
		f = r.ex.loadFont(res, name)
		fonts[name] = f
	}
	return f
}

// show draws one string.
func (r *run) show(m *content.Machine, res objects.Dict, fonts map[content.Name]*font.Font, s objects.String) {
	if len(s) == 0 {
		return
	}
	f := r.font(m, res, fonts)
	if f == nil {
		// No font resource: the string cannot be split into codes, let alone
		// decoded, and guessing one byte per code would emit plausible garbage. The
		// glyphs are lost; the rest of the page is not.
		return
	}

	ts := &m.GS.Text
	th := ts.Scale / 100
	visible := m.Visible()
	if !visible && !r.ex.opt.KeepHidden {
		// Still has to advance, or everything after it lands in the wrong place.
		r.advanceOnly(m, f, s)
		return
	}

	artifact := m.InArtifact()
	mcid := m.MCID()

	for _, g := range f.Decode(s) {
		trm := m.RenderMatrix()
		sx, sy := trm.ScaleFactors()
		ox, oy := trm.Apply(0, 0)

		// Word spacing applies to a single-byte code 32 only (§9.3.3). A composite
		// font whose two-byte code happens to be 32 must not receive it, and the
		// rule is easy to lose because the common case makes no difference.
		tw := 0.0
		if g.Bytes == 1 && g.Code == 32 {
			tw = ts.WordSpace
		}

		w0 := g.Width / 1000
		tx := (w0*ts.Size + ts.CharSpace + tw) * th

		// Captured before the advance, though a translation cannot change it: the
		// pen arithmetic below is in page units and tx is in unscaled text space.
		as := alongScale(m)

		if g.Text != "" {
			r.place(m, g, trm, ox, oy, sx, sy, w0, f, artifact, mcid)
		}

		if f.Vertical {
			// Vertical writing advances the cross axis, which the pen does not track.
			// place has already put the pen at this glyph's own along position, so the
			// next glyph measures a zero gap rather than a spurious one.
			m.AdvanceVertical(-tx)
			continue
		}
		m.Advance(tx)
		// place left the pen at this glyph's start, so adding the full displacement —
		// width plus Tc and Tw — leaves it where the next glyph is expected, and a gap
		// there is displacement the text state did not explain. A glyph whose text
		// could not be decoded still advances it, so a dropped glyph reads as a gap in
		// the middle of a word rather than as a space.
		r.pen += tx * as
	}
}

// advanceOnly moves the text position for a string without recording its glyphs.
//
// The pen is deliberately left behind. The skipped text occupied space on the
// page, so the next glyph measures that whole span as a gap and one space is
// inferred, which is what separates the words either side of an excluded run.
// Advancing the pen with it would butt them together.
func (r *run) advanceOnly(m *content.Machine, f *font.Font, s objects.String) {
	ts := &m.GS.Text
	th := ts.Scale / 100
	for _, g := range f.Decode(s) {
		tw := 0.0
		if g.Bytes == 1 && g.Code == 32 {
			tw = ts.WordSpace
		}
		tx := (g.Width/1000*ts.Size + ts.CharSpace + tw) * th
		if f.Vertical {
			m.AdvanceVertical(-tx)
		} else {
			m.Advance(tx)
		}
	}
}

// place records one glyph's text, opening a new fragment or line when the glyph
// does not continue the current one.
func (r *run) place(m *content.Machine, g font.Glyph, trm geom.Matrix, ox, oy, sx, sy, w0 float64, f *font.Font, artifact bool, mcid int) {
	orient, along, cross := project(trm, ox, oy)
	style := r.styleOf(m, f, sy)
	// The nominal space advance at this size. A font with no space glyph reports 0,
	// and the fallback is half an em: a threshold of zero would infer a space
	// between every pair of glyphs, which is the opposite failure from no spaces at
	// all and just as unreadable.
	space := f.SpaceWidth() / 1000 * sx
	if space <= 0 {
		space = 0.5 * sx
	}

	end := along + w0*sx

	// The gap is measured against where the previous glyph left the pen, so it has
	// to be read before anything moves it.
	gap := along - r.pen
	hadPen := r.havePen

	prev := r.cur()
	sameLine := r.open != nil && prev != nil && r.open.orient == orient &&
		math.Abs(cross-r.open.cross) <= r.tol.LineFrac*maxf(sy, prev.height)
	if !sameLine {
		r.closeLine()
		r.open = &line{cross: cross, orient: orient}
		r.startFrag(along, end, cross, orient, sy, space, style, mcid, artifact)
		r.appendText(g.Text, along, end, sy)
		// Set after closeLine, which clears havePen: assigning before it would leave
		// the second glyph of every line with no pen to measure against.
		r.setPen(along)
		return
	}

	needSpace := hadPen && gap > r.tol.SpaceFrac*space
	// A gap wide enough to be a column or a tab still becomes one space here.
	// Distinguishing them needs to know whether the page has columns, which is
	// layout's question, and emitting several spaces would corrupt the character
	// counts the benchmark compares against.
	//
	// Every inferred space is remembered rather than acted on, so that splitAtRules can
	// divide the fragment at one if a rule turns out to run through the gap.
	//
	// Every space and not only a wide one, which was measured rather than assumed. A
	// WideSpaceFrac filter of 2.50 was tried first and it dropped a real cell boundary:
	// reference/table.pdf's header row sets wider cells than its body, so its column
	// gaps are 2.400 space widths against the body's 4.128, and the filter admitted the
	// body rows while silently discarding the header. That is the failure mode of every
	// threshold on this quantity — the gap distribution over all 117499 inferred spaces
	// on disk is continuous from the 0.40 the threshold itself imposes out to 1303 space
	// widths, with no quarter-width band empty below 5 and the largest jump anywhere below
	// 200 — and the rule is the evidence, so there is nothing for a width to add. The cost
	// is bookkeeping on a slice that is discarded with the page.
	//
	// writeSpace is the narrower question of whether that one space is a character the
	// text does not already have. A gap is inferred from geometry alone, and geometry
	// does not know that the page has already drawn a space into it: justified text sets
	// a space glyph and then stretches the word gap around it, so the pen ends up further
	// from the next glyph than the nominal space width, and the rule fires a second time
	// on a boundary that is already spaced. Measured on the corpus, 24579 of 46917
	// inferred spaces follow text that already ends in whitespace and 12835 of those also
	// precede a space glyph, where the inserted character would be the third — 10922
	// interior runs of two or more spaces reach the Markdown output because of it, 9719 of
	// exactly two and 1203 of three or more, counting a run as interior when a
	// non-whitespace character stands on each side of it.
	//
	// writeSpace and not needSpace is also the population SpaceFrac has to be swept against,
	// and sweeping the wrong one is what kept the threshold at 0.30 for a phase. The gap
	// between the two counts is what makes them disagree: over the eleven specification
	// documents needSpace fires 41164 times and writeSpace 3627, an eleven-fold difference,
	// so a step in the threshold reads as costing hundreds of correct spaces against the
	// gaps and as free against the insertions. geom.SpaceFrac carries that measurement.
	//
	// Only the character is suppressed, never the cut. A cell boundary is a position in
	// the text and stays one whether or not a space is written there: a header cell whose
	// label ends in a space still has to divide from the next cell, and splitAtRules can
	// only find that division at a recorded cut. Dropping the cut with the space put
	// reference/table.pdf's cells back into one fragment.
	writeSpace := needSpace && !endsInSpace(prev) && !startsWithSpace(g.Text)
	c := r.cur()
	sameFrag := c != nil && c.mcid == mcid && c.artifact == artifact &&
		c.style.SameRun(style, r.tol)
	if !sameFrag {
		r.startFrag(along, end, cross, orient, sy, space, style, mcid, artifact)
		// A space between two fragments is carried by the one that follows it, so
		// that trimming a fragment's leading space is a decision the sink can still
		// make.
		if writeSpace {
			c = r.cur()
			c.text = append(c.text, ' ')
		}
		r.appendText(g.Text, along, end, sy)
		r.setPen(along)
		return
	}

	if writeSpace {
		c.text = append(c.text, ' ')
	}
	if needSpace {
		// After the space, so the offset splits with the space on the left-hand side.
		c.cuts = append(c.cuts, cut{off: len(c.text), x0: r.pen, x1: along})
	}
	r.appendText(g.Text, along, end, sy)
	r.setPen(along)
}

// endsInSpace reports whether a fragment's accumulated text ends in whitespace, so that a
// gap after it does not add a second space.
//
// The byte-slice twin of endsWithSpace, and it exists for cost rather than for semantics:
// place runs once per glyph, 2.76M times over this corpus, and string(f.text) would copy
// a whole fragment on each call to read its last rune.
//
// The last rune rather than the last byte, because the whitespace to detect is not only
// U+0020 — Well-Tagged-PDF draws U+2002 EN SPACE as its clause-number separator, and a
// byte test would read that rune's trailing 0x82 as non-space and double it.
//
// No guard for empty text: DecodeLastRune returns U+FFFD for it, which is not a space, so
// the answer is already false and a guard would change no byte. Empty is reachable — a
// glyph whose /ToUnicode maps it to nothing appends none.
//
// Nil is not guarded either, and that is a precondition rather than a handled case: this
// dereferences f, so a nil fragment panics. place's sameLine test returns early unless
// cur() is non-nil, so the one call site cannot pass one. Stated rather than defended
// because a nil guard here would answer false, and false is "write the space" — turning a
// caller's bug into a doubled space somewhere unrelated instead of a stack trace at it.
func endsInSpace(f *frag) bool {
	r, _ := utf8.DecodeLastRune(f.text)
	return unicode.IsSpace(r)
}

// setPen leaves the pen at a glyph's own start position.
//
// show then adds that glyph's full displacement, which carries the pen to where
// the next glyph is expected. Setting it to the glyph's end here as well would
// count the width twice, and every gap after it would measure negative — a space
// inferred nowhere on the page.
func (r *run) setPen(along float64) {
	r.pen, r.havePen = along, true
}

// cur returns the fragment currently being appended to, or nil when there is
// none.
//
// The result is for immediate use only. It addresses an element of r.open.frags,
// and the next startFrag may reallocate that slice, so nothing may hold it across
// a call that appends.
func (r *run) cur() *frag {
	if !r.haveFrag {
		return nil
	}
	return &r.open.frags[len(r.open.frags)-1]
}

func (r *run) appendText(s string, along, end, height float64) {
	c := r.cur()
	c.text = append(c.text, s...)
	if along < c.along0 {
		c.along0 = along
	}
	if end > c.along1 {
		c.along1 = end
	}
	if height > c.height {
		c.height = height
	}
}

func (r *run) startFrag(along, end, cross float64, orient int, height, space float64, style doc.Style, mcid int, artifact bool) {
	r.open.frags = append(r.open.frags, frag{
		along0:   along,
		along1:   end,
		cross:    cross,
		orient:   orient,
		height:   height,
		space:    space,
		style:    style,
		mcid:     mcid,
		artifact: artifact,
	})
	r.haveFrag = true
}

func (r *run) closeLine() {
	if r.open == nil {
		return
	}
	if len(r.open.frags) > 0 {
		r.lines = append(r.lines, *r.open)
	}
	r.open, r.haveFrag = nil, false
	r.havePen = false
}

// styleOf reads the on-page style of the text currently being drawn.
func (r *run) styleOf(m *content.Machine, f *font.Font, sy float64) doc.Style {
	st := doc.Style{
		Font: f.Name(),
		// The effective size, from the composed matrix rather than from the Tf
		// operand. A Tf of 1 with a matrix scaling by 12 is how many producers set
		// 12-point type, and reporting 1 makes every size-based heuristic useless.
		Size:   sy,
		Bold:   f.Bold(),
		Italic: f.Italic(),
		Mono:   f.Monospaced(),
		Hidden: !m.Visible(),
	}
	return st
}

// project returns a baseline-relative coordinate system for a glyph.
//
// Text is not always horizontal — a rotated page, a sideways table header, a
// vertical script — and a reader that compares raw y values groups a rotated
// caption into whatever horizontal line shares its y. Projecting onto the
// baseline direction makes "along the line" and "across lines" mean the same thing
// at every angle, and for unrotated text it reduces to x and y exactly.
func project(trm geom.Matrix, ox, oy float64) (orient int, along, cross float64) {
	dx, dy := trm.A, trm.B
	n := math.Hypot(dx, dy)
	if n == 0 {
		// A degenerate matrix — zero font size, or Tz 0. The glyph paints nothing;
		// treat it as horizontal so it still groups somewhere rather than forming a
		// line of its own per glyph.
		return 0, ox, oy
	}
	dx, dy = dx/n, dy/n
	// Bucketed to 15 degrees. Exact comparison would split a line whose glyphs
	// differ by float noise in the last bits; anything coarser would merge genuinely
	// different orientations.
	orient = int(math.Round(math.Atan2(dy, dx) / (math.Pi / 12)))
	return orient, ox*dx + oy*dy, -ox*dy + oy*dx
}

// alongScale returns the length, in page units, of one unit of text-space
// displacement along the current baseline. It converts the tx a show operation
// computes into the coordinate system the gap test measures in.
//
// The text matrix has to be in it, not just the CTM. A displacement passes through
// Tm and then the CTM, and a producer that sets 12-point type as a Tf of 1 with a
// Tm scaling by 12 — which many do — puts the whole font size in Tm. Scaling by
// the CTM alone then undercounts every advance by that factor, the pen falls a
// glyph-width behind on every glyph, and the accumulated shortfall reads as an
// unexplained gap: a space between every pair of glyphs. That is the failure this
// package exists to prevent, arrived at from the other side.
//
// The font size is deliberately excluded, because tx already carries it. Only the
// Tm and CTM composition is left, which is exactly the transform between the space
// tx is expressed in and the page.
func alongScale(m *content.Machine) float64 {
	sx, _ := m.Tm.Mul(m.GS.CTM).ScaleFactors()
	return sx
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// blocks assembles the collected lines into paragraph blocks.
//
// Lines keep the order they were drawn in rather than being sorted top to bottom.
// That is a deliberate limit, not an oversight: stream order is the producer's
// reading order and is right for the large majority of documents, while sorting by
// position interleaves the columns of every two-column page. Recovering reading
// order from geometry is what layout owns, and doing a half version of it here
// would make the good case worse to improve the bad one.
func (r *run) blocks(opt Options) []doc.Block {
	r.closeLine()
	if len(r.lines) == 0 {
		return nil
	}
	r.splitAtRules()

	var out []doc.Block
	var cur *doc.Block
	var prev *line
	var shape indent

	for i := range r.lines {
		ln := &r.lines[i]
		// Fragments within a line are ordered by position, not by when they were
		// drawn. Producers emit a line out of order routinely — a superscript after
		// the clause it annotates, a table cell revisited to add a leader — and
		// within a single baseline, position *is* reading order, so sorting here is
		// safe in a way sorting whole lines would not be. Stable, so two fragments
		// starting at the same x keep their drawn order.
		sort.SliceStable(ln.frags, func(a, b int) bool {
			return ln.frags[a].along0 < ln.frags[b].along0
		})

		art := lineIsArtifact(ln)
		if art && !opt.KeepArtifacts {
			// Dropped rather than emitted so that a running header does not appear a
			// thousand times in the middle of prose. The line still ended the current
			// paragraph, because on the page it did.
			cur, prev = nil, nil
			shape = indent{}
			continue
		}

		if cur == nil || !continues(prev, ln, r.tol) || shape.startsParagraph(ln, r.tol) {
			out = append(out, doc.Block{Role: roleOf(art)})
			cur = &out[len(out)-1]
			shape = newIndent(ln)
		} else {
			shape.observe(ln)
		}
		appendLine(cur, ln, r.tol, r.page)
		prev = ln
	}

	// A block that ended up with nothing to show is a positioned rectangle the
	// producer left behind.
	kept := out[:0]
	for i := range out {
		if !out[i].IsEmpty() {
			kept = append(kept, out[i])
		}
	}
	return kept
}

func roleOf(artifact bool) doc.Role {
	if artifact {
		return doc.RoleArtifact
	}
	// Every non-artifact block is a paragraph at this stage. Headings, lists, and
	// captions are decided from the structure tree by sectionize or from font-size
	// clusters by layout; inferring them here would mean two packages guessing at
	// the same thing from less evidence.
	return doc.RoleParagraph
}

func lineIsArtifact(ln *line) bool {
	for i := range ln.frags {
		if !ln.frags[i].artifact {
			return false
		}
	}
	return len(ln.frags) > 0
}

// continues reports whether ln belongs to the same paragraph as the line before
// it.
func continues(prev, ln *line, t geom.Tolerance) bool {
	if prev == nil || prev.orient != ln.orient {
		return false
	}
	h := lineHeight(prev)
	if h <= 0 {
		return false
	}
	// Only a downward step continues a paragraph: text returning up the page is a
	// new column or a new region, never the next line of the current one.
	step := prev.cross - ln.cross
	if step < 0 {
		return false
	}
	if step > t.ParaFrac*h {
		return false
	}
	if sameElement(prev, ln) {
		// Both lines are inside one marked-content element, so the producer has stated
		// they are one thing and the size test has nothing to add. ISO/TS 32003's cover
		// is the case: a 36pt document number and a 17.5pt title, both /MCID 3, which
		// sizeBreak would otherwise split. Splitting it loses the space between them:
		// the wrap space is inferred only *within* a block, so a boundary here means no
		// space is written at all, and sectionize then rejoins the two spans on their
		// shared MCID with no separator, yielding "32003:2023Document management".
		// Trailing placement above does not cover this — there is nothing to place.
		//
		// Deferring to the declaration is the same rule ADR 0008 applies to roles: where
		// a producer declared the structure, a heuristic that guesses over it replaces
		// evidence with inference. Untagged pages carry no MCID at all, so this never
		// reaches the documents the size test was added for.
		return true
	}
	return !sizeBreak(prev, ln, t)
}

// sameElement reports whether two lines were drawn entirely inside one marked-content
// element.
//
// Every fragment of both must carry the same identifier. A line spanning two MCIDs is
// not a statement that the lines belong together, and MCID 0 is valid while -1 means
// "outside any marked content", so the absent case has to be excluded explicitly.
func sameElement(prev, ln *line) bool {
	if len(prev.frags) == 0 || len(ln.frags) == 0 {
		return false
	}
	id := prev.frags[0].mcid
	if id < 0 {
		return false
	}
	for _, fs := range [][]frag{prev.frags, ln.frags} {
		for i := range fs {
			if fs[i].mcid != id {
				return false
			}
		}
	}
	return true
}

// sizeBreak reports whether two lines are set in different enough type to be
// different blocks regardless of how little vertical space separates them.
//
// The vertical step cannot see this case. A heading set at the same leading as the
// prose under it steps down by exactly one line, so the step test joins them and the
// heading is resolved as the first words of the following paragraph — which is why
// adobe-samples/autotagPDFInput.pdf and pymupdf/v110-changes.pdf produced no
// promotable heading at all before this, and why the fix belongs here rather than in
// layout: there was no separate block to promote.
//
// Compared on the dominant size rather than on style, because weight is not evidence
// of a break. reference/text-styles.pdf sets four consecutive same-size paragraphs
// that differ only in which word each emphasizes, so their longest fragments differ in
// weight while the paragraphs themselves are unremarkable; splitting on that would
// break blocks at whichever word happened to be bold.
func sizeBreak(prev, ln *line, t geom.Tolerance) bool {
	if t.SizeFrac <= 1 {
		// Disabled. A ratio of 1 would split on any difference at all, which no
		// document survives, so it is read as "off" rather than applied literally —
		// this is what a caller filling some Tolerance fields and not others gets.
		return false
	}
	a, b := domSize(prev), domSize(ln)
	if a <= 0 || b <= 0 {
		return false
	}
	if a < b {
		a, b = b, a
	}
	return a > b*t.SizeFrac
}

// indent tracks the horizontal shape of the block being built, so that a paragraph
// break with no vertical evidence at all can still be seen.
//
// Neither of the other two tests can see this case, and reference/paragraphs.pdf is
// the measurement that says so. Three paragraphs, each wrapped over three or four
// lines, all set in one size at one leading: every consecutive line pair steps down
// 11.955pt against a 9.963pt line height, a ratio of 1.200 to three decimals, whether
// the pair is an ordinary wrap or a paragraph boundary. So no ParaFrac separates them
// at any value, and SizeFrac has nothing to compare. The only signal left is
// horizontal — the first line of each paragraph starts three space widths right of
// the margin its own continuations sit at.
//
// text-styles.pdf was documented as this case and is not: measured, its four
// paragraphs are one line each, so every pair in it is a boundary, there is no wrap
// to contrast against, and a rule that split unconditionally would score perfectly.
// That is why paragraphs.pdf exists.
type indent struct {
	// first is the left edge of the block's opening line, and edge the leftmost edge
	// its continuation lines have shown. NaN for edge means the block has no
	// continuation line yet and there is nothing to measure an indent against.
	first, edge float64
	space       float64

	// spread is how far the widest continuation line's left edge sits right of edge.
	// A left-aligned block's continuations share an edge, so this stays at float
	// noise; a centred one's wander, and that is what disqualifies it below.
	spread float64
}

func newIndent(ln *line) indent {
	lo, _ := lineExtent(ln)
	return indent{first: lo, edge: math.NaN(), space: lineSpace(ln)}
}

// observe records a line that stayed in the block, which is what establishes the
// margin later lines are judged against.
//
// The minimum rather than the mean: a paragraph's continuation lines all start at the
// measure's left edge, and taking the smallest is what makes one stray positioned
// fragment unable to drag the margin rightward. spread then records how much they
// disagreed, which is the only thing separating an indent from centred type.
//
// spread is maintained as (widest edge seen - narrowest), and the += below is what keeps
// it that when the margin moves left: the running value was measured against the old
// edge, so adding the shift rebases it onto the new one rather than accumulating noise.
// Following edges of 12, 20, 10 and 8 gives 8, 10 and then 12, which is 20 - 8 at every
// step. A max there would silently under-report every block whose margin moved.
//
// No document on disk proves that: measured over the corpus, += and max disagree on 111
// of 30328 indent decisions and change the guard's verdict on none of them. The case that
// divides them is synthetic and lives in TestIndentMatchesTheBlocksOwnFirstLine — a margin
// walking left in two sub-tolerance steps that sum past the guard.
func (in *indent) observe(ln *line) {
	lo, _ := lineExtent(ln)
	if math.IsNaN(in.edge) {
		in.edge = lo
		return
	}
	if lo < in.edge {
		in.spread += in.edge - lo
		in.edge = lo
		return
	}
	if d := lo - in.edge; d > in.spread {
		in.spread = d
	}
}

// startsParagraph reports whether ln repeats the indent this block's own first line
// was set with, which is a new paragraph in a document that separates them by nothing
// else.
//
// Matching the block's *own* first line is what makes the rule safe, and it is the
// whole design. An indent alone is far too common to act on: with each variant run as
// the live rule over the corpus, "indented past the block's margin" fires 441 times
// across 19 files, and reading them shows they are mostly C source listings in
// mupdf_explored.pdf, where the indent is syntax, and hanging-indented bullets in
// ISO 32000-2, where the continuation is indented and the marker line is not.
// Requiring the incoming indent to equal the one the block opened with rejects both —
// a bullet's own first line sits left of its continuations, not right of them — and
// takes 441 firings down to 11. The spread guard below removes 8 of those 11, leaving
// 3 across 2 files: the two real boundaries in paragraphs.pdf and one in
// mupdf_explored.pdf.
func (in *indent) startsParagraph(ln *line, t geom.Tolerance) bool {
	if t.IndentFrac <= 0 || math.IsNaN(in.edge) || in.space <= 0 {
		// Disabled, or the block has no continuation line to establish a margin. The
		// second case is the common one and it is why a two-line paragraph followed by
		// an indented third line is left alone: with one continuation line the margin
		// is a guess.
		return false
	}
	if in.spread > 0.5*in.space {
		// The block's continuation lines do not agree on a left edge, so it has no
		// margin and an "indent" past it is meaningless. Centred type is the case, and
		// pymupdf/dotted-gridlines.pdf is why the guard exists: its centred table
		// headers set "COMUNI / SUPERIORI / 15.000 / abitanti / (SUP)" at left edges of
		// 285.53, 282.53, 286.73, 285.65 and 287.45, wandering about two points around
		// a centre. Against that document's 1.335pt space advance, two points is 1.35
		// space widths, which cleared the threshold below and split the cell mid-phrase
		// into "COMUNI SUPERIORI 15.000" and "abitanti (SUP)". A left-aligned block's
		// continuations agree to within float noise, so half a space width separates
		// the two without needing to recognize centring as such. Measured: that file
		// fires twice with the check above alone and not at all with this one.
		return false
	}
	lo, _ := lineExtent(ln)
	in2 := (lo - in.edge) / in.space
	own := (in.first - in.edge) / in.space
	if in2 < t.IndentFrac || in2 > t.IndentMax {
		return false
	}
	// Half a space width of agreement. Tighter than it looks: the two indents come
	// from the same \parindent through the same text matrix, so they agree to within
	// float noise when they agree at all — 3.00 against 3.00 in paragraphs.pdf.
	return math.Abs(in2-own) <= 0.5
}

// lineExtent returns a line's leftmost and rightmost edges along its baseline.
func lineExtent(ln *line) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for i := range ln.frags {
		lo = math.Min(lo, ln.frags[i].along0)
		hi = math.Max(hi, ln.frags[i].along1)
	}
	return lo, hi
}

// lineSpace returns the widest nominal space advance among a line's fragments, the
// unit every horizontal threshold here is expressed in.
//
// In space widths rather than points for the same reason sizeBreak compares a ratio:
// a 7pt footnote and a 28pt heading are indented by different numbers of points and
// by the same number of spaces.
func lineSpace(ln *line) float64 {
	sp := 0.0
	for i := range ln.frags {
		if ln.frags[i].space > sp {
			sp = ln.frags[i].space
		}
	}
	return sp
}

// domSize returns the size most of a line's characters are set in.
//
// The dominant size, not the largest: a footnote marker or an inline superscript is a
// legitimately different size within one line of prose, and taking the maximum would
// make every annotated line look like a heading meeting body text.
func domSize(ln *line) float64 {
	by := map[float64]int{}
	for i := range ln.frags {
		// Runes, not bytes, so that "characters" means the same thing here as it does in
		// layout.bodyCluster. Weighting by byte length would count a CJK or mathematical
		// character three or four times over a Latin one, and a line of body-size CJK
		// carrying one Latin word would tally as though the CJK were the emphasis.
		n := utf8.RuneCount(ln.frags[i].text)
		// Rounded to hundredths of a point, for the same reason layout.quantize
		// exists: Style.Size is computed through the text matrix and the CTM, so one
		// run of type differs in the far decimals and would tally as several sizes,
		// none of them dominant.
		sz := math.Round(ln.frags[i].style.Size*100) / 100
		if n == 0 || sz <= 0 {
			continue
		}
		by[sz] += n
	}
	// Ties break toward the smaller size so the result does not depend on map order.
	// bestN starts below zero so the first size seen always wins outright, leaving
	// best's zero value unread.
	best, bestN := 0.0, -1
	for sz, n := range by {
		if n > bestN || (n == bestN && sz < best) {
			best, bestN = sz, n
		}
	}
	return best
}

func lineHeight(ln *line) float64 {
	h := 0.0
	for i := range ln.frags {
		if ln.frags[i].height > h {
			h = ln.frags[i].height
		}
	}
	return h
}

// appendLine adds a line's fragments to a block as spans, joining to the previous
// line with a space.
func appendLine(b *doc.Block, ln *line, t geom.Tolerance, page int) {
	for i := range ln.frags {
		fr := &ln.frags[i]
		txt := string(fr.text)
		if txt == "" {
			continue
		}
		if i == 0 && len(b.Spans) > 0 {
			// A line break inside a paragraph is a word boundary, and the space goes on the
			// *trailing* end of the span already there rather than the leading end of the
			// one arriving.
			//
			// Which end is not cosmetic, because consumers regroup spans. sectionize joins
			// them in the order a structure element lists its content, not in page order,
			// and joins with no separator — so a space on a span's leading edge travels away
			// from the neighbour it was inferred for and reappears inside a word somewhere
			// else. Measured over the 11 tagged documents: leading placement emitted
			// "revision" as "re" + "-" + " vision", and "surrounding", "structure",
			// "digest", "requirements" and 12 more the same way, while running a clause
			// number into the sentence before it ("…an ISO 32000-2 document.-5.5.2.3").
			// Trailing placement fixed all 29 and broke none. TestWrapSpaceTrailsThe-
			// PreviousSpan pins it, since text joined from the spans reads identically
			// either way and no assertion on the joined string can tell them apart.
			n := len(b.Spans) - 1
			prev := b.Spans[n].Text
			if !endsWithSpace(prev) && !startsWithSpace(txt) && wrapNeedsSpace(prev, txt) &&
				!dashHoldsTheWord(b.Spans, n) {
				b.Spans[n].Text += " "
			}
		}

		box := fragBox(fr)
		// The MCID has to match as well as the style, or a span would straddle a
		// marked-content boundary and stop being a usable join key. A heading and the
		// paragraph after it are frequently set in the same face at the same size — the
		// style says one run, the structure tree says two elements — and merging them
		// resolves the heading's title to the heading plus the paragraph.
		if n := len(b.Spans); n > 0 && !fr.apart && b.Spans[n-1].MCID == fr.mcid &&
			b.Spans[n-1].Style.SameRun(fr.style, t) {
			b.Spans[n-1].Text += txt
			b.Spans[n-1].Box = b.Spans[n-1].Box.Union(box)
		} else {
			b.Spans = append(b.Spans, doc.Span{
				Text:  txt,
				Style: fr.style,
				Box:   box,
				Page:  page,
				MCID:  fr.mcid,
			})
		}
		b.Box = b.Box.Union(box)
	}
	// The union of the spans' identifiers, read from the spans rather than accumulated here
	// from the fragments.
	//
	// The same set either way, and that is not an accident to be relied on quietly: an empty
	// fragment is skipped above so it contributes no span and had no text to attribute, and the
	// merge branch folds a fragment only into a span whose MCID already equals its own. So this
	// site was one of the two that already honoured the invariant, and the corpus agrees — 0
	// duplicate and 0 negative entries on page blocks before the change, against 34141 duplicates
	// on the blocks sectionize rebuilds from them. It reads from the spans anyway, because the
	// field describes the spans and an accumulator that happens to agree is still a second
	// implementation of a contract stated on the field. See doc.Block.SetMCIDs.
	b.SetMCIDs()
}

// endsWithSpace and startsWithSpace report whether a word boundary is already written at
// the join, in which case appendLine must not infer a second one.
//
// The test is on the *rune*, not the byte, because a producer writes a word boundary with
// whatever space character its typography calls for. Well-Tagged-PDF-WTPDF-1.0.pdf sets
// every inter-word gap as U+2002 EN SPACE, so an ASCII-only test saw none of them and
// doubled 231 of them — "the understanding  of", "e.g.,  WCAG" — and 5 more in
// the other direction, where the arriving line *began* with U+2002. In Markdown two
// trailing spaces are a hard line break, so a doubled space at a wrap is not only a wrong
// answer about the page but changes the rendering of the line.
//
// unicode.IsSpace covers the Unicode space separators along with the ASCII whitespace the
// byte test already had. Only these two predicates widen: the space appendLine writes stays
// an ASCII space, since it is this code's own inference and not a glyph the page drew.
//
// U+00A0 NO-BREAK SPACE counts, which is the one that looks arguable. It is a word boundary
// the producer drew as a glyph — the "no-break" is a line-breaking instruction to a
// *formatter*, and this code is reading a page that is already formatted. A second space
// beside it would be as wrong as beside any other.
//
// The empty string needs no guard: it decodes to U+FFFD, which is not a space, so both
// return false. Invalid UTF-8 decodes to U+FFFD as well and therefore still gets a space,
// which is the answer the byte test gave and the one wrapNeedsSpace documents for the same
// input — a decode failure should not also lose a word boundary.
func endsWithSpace(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(r)
}

func startsWithSpace(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
}

// wrapNeedsSpace reports whether joining two wrapped lines needs a space between them.
//
// It does for Latin, where a line break falls between words and the break is the only
// thing marking the boundary. It does not for Chinese or Japanese, which are set without
// inter-word spaces: a line simply fills and wraps mid-word, so inserting a space there
// splits a word that was never divided. Measured on chinese-tables.pdf, a Chinese bond
// prospectus — the company name 中诚信国际信用评级有限责任公司 wraps across three lines
// and came out as three fragments with spaces in it, which is not a rendering choice but
// a wrong answer about what the page says.
//
// The test is on the characters actually adjacent to the join rather than on a script
// detected for the document, because a document is routinely mixed — this one sets its
// numbers and its rating codes in Latin — and the only thing that decides whether this
// particular break was a word boundary is what sits on either side of it. A space is
// added unless both sides are scripts written without them, which keeps a CJK line
// wrapping into a Latin one (and the reverse) joined as the two words they are.
//
// One rune on each side is enough because that is where the break falls. prev is the
// previous span's text rather than the previous line's, and the two differ when a line
// ends in its own style run — but the last rune is the same either way, since a span
// boundary inside a line does not move the line's end.
//
// Invalid UTF-8 decodes to U+FFFD, which is not spaceless, so a space is added: the same
// answer this function gave everywhere before, which is the right way for a decode failure
// to fail.
func wrapNeedsSpace(prev, next string) bool {
	last, _ := utf8.DecodeLastRuneInString(prev)
	first, _ := utf8.DecodeRuneInString(next)
	return !(spaceless(last) && spaceless(first))
}

// dashHoldsTheWord reports whether the line ending at spans[n] ends in a dash that is
// holding a word together across the break, in which case the wrap contributes no space.
//
// A line ending in a dash is two different things. In "marked-|content" and "struc-|ture"
// the dash is inside a word, and a space at the break splits that word: 483 of the 489
// dash-final wraps across the 17 PDFs on disk are this, and every one of them came out
// wrong — "cross- reference", "human- readable", "ISO 32000- 2:2020". In "text is sent -|as
// each glyph" the dash is punctuation standing alone between two words, and the space is
// the word boundary it needs. The corpus has 5 of those, one of them an em dash, plus the one
// case below where punctuation rather than a space precedes the dash: 489 wraps in all.
//
// What separates them is the rune *before* the dash, not the dash and not what follows.
// Attached to a letter or digit, the dash is part of a word; preceded by a space, it is a
// word of its own. The rune after the break decides nothing: 26 of the 483 continue into a
// digit and 17 into a capital ("41-|44", "GREATER-|THAN", "UTF-|8"), all of them words that
// a space would break just as badly as a lowercase one.
//
// Letter-or-digit rather than the weaker "not a space", because punctuation before the dash
// is a third case and it wants the space kept: the corpus's one instance is "resources/-"
// wrapping into "Courier'", where the dash separates two quoted names in a list.
//
// The walk back through spans is what makes the rule reach its own population. A dash is
// frequently a span of its own, because a producer sets it in a different run or the tagger
// gives it its own MCID, and then prev is the bare "-" with the word it belongs to one span
// earlier. That is 16 of the 483 — the "surrounding", "structure", "constituent" and
// "algorithm" breaks in the TS documents, which are exactly the cases the first attempt at
// this rule missed. Only spans of pure dashes are skipped over, so the walk cannot run past
// the word it is looking for.
//
// Whether to also drop the dash is a separate question this does not answer. Looking each
// joined word up in its own document, with this rule off so the search is not finding the
// join it just made, splits the 483: dropping would be right for the 218 the document spells
// out elsewhere without a dash ("applica-|tion" against "application"), wrong for the 170 it
// spells out with one ("cross-|reference" against "cross-reference"), either way for 14
// spelled both ways, and unanswerable for the 81 that appear nowhere else at all. So the dash
// stays: an unsplit word with a hyphen in it is a reading a consumer can still repair, while
// a deleted hyphen is not recoverable from the output.
//
// The full dash alphabet, because the corpus proves the rule is not about U+002D alone:
// U+2013 EN DASH holds "a–|f" together in a hexadecimal range, and U+2011 NON-BREAKING
// HYPHEN holds "doc‑|bibliography". The one em dash at a wrap is detached and keeps its
// space through the same test, which is the alphabet widening without the exception moving.
func dashHoldsTheWord(spans []doc.Span, n int) bool {
	last, _ := utf8.DecodeLastRuneInString(spans[n].Text)
	if !isDash(last) {
		return false
	}
	for i := n; i >= 0; i-- {
		s := strings.TrimRightFunc(spans[i].Text, isDash)
		if s == "" {
			continue // a span of nothing but dashes: the word is earlier
		}
		r, _ := utf8.DecodeLastRuneInString(s)
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	// The dash opens the block, so there is no word before it to hold together.
	return false
}

// isDash covers the dash punctuation a producer can set at a line break: the Unicode Pd
// category, plus the two dashes outside it that a PDF can hold a word together with.
//
// Pd rather than a list, because the corpus already proves a list of one is wrong — three
// dashes end a line here, and picking them by hand is how the next one gets missed. The
// members with corpus evidence are U+002D (483 breaks), U+2013 EN DASH ("0–9 and A–F or
// a–|f", a hexadecimal range) and U+2011 NON-BREAKING HYPHEN ("doc‑|bibliography"), and
// U+2014 EM DASH ends a line once where it is detached and therefore keeps its space —
// which is the alphabet widening without the exception moving.
//
// The two additions are Pd's near misses, both So or Sm rather than Pd. U+00AD SOFT HYPHEN
// is what a formatter's own hyphenation is when the producer marks it as such, and 16
// /ActualText values in the corpus are exactly that; U+2212 MINUS SIGN because a line
// broken inside "−1" is no more a word boundary than one broken inside "-1". Neither ends a
// line on disk, so both are the rule stated over its whole alphabet rather than a measured
// case — the cost of being wrong is one missing space, against a split word for omitting
// them.
func isDash(r rune) bool {
	return r == 0x00AD || r == 0x2212 || unicode.Is(unicode.Pd, r)
}

// spaceless reports whether r belongs to a script written without spaces between words.
//
// Chinese and Japanese: Han, the two syllabaries, and the CJK punctuation block, since a
// line ending in "，" or "。" is as much a mid-sentence wrap as one ending in a character.
//
// Hangul is deliberately **not** here even though it is CJK by every other classification.
// Modern Korean *is* written with spaces between words, so a Korean line wrap is an
// ordinary word boundary and suppressing the space would run two words together — the
// exact defect this function exists to prevent, in the other direction. The criterion is
// the script's use of spaces, not its residence in the CJK blocks.
//
// Thai, Lao, Khmer, and Burmese do qualify and are absent for a different reason: no
// fixture exercises them, and a rule this file cannot measure is a guess. A Thai document
// arriving in the corpus is the notice to extend this, and the Han ranges are what say
// what such an extension has to look like.
func spaceless(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303F: // CJK symbols and punctuation
		return true
	case r >= 0x3040 && r <= 0x30FF: // hiragana, katakana
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK extension A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK unified ideographs
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK compatibility ideographs
		return true
	// Fullwidth forms through halfwidth katakana. The upper bound is U+FF9F rather than
	// the U+FF60 that "fullwidth forms" alone suggests, because U+FF61–FF9F is halfwidth
	// katakana and its voicing marks — kana either way, and a script does not acquire word
	// spaces by being set at half width.
	case r >= 0xFF00 && r <= 0xFF9F:
		return true
	}
	return false
}

// fragBox turns a fragment's baseline-relative extent back into a user-space
// rectangle.
//
// The vertical extent is estimated from the font size — 0.8 above the baseline and
// 0.2 below — because the real glyph bounding box is inside the font program and
// reading it would mean parsing every embedded font to answer a question no
// consumer of this box asks. It is used for coverage and for block bounds, both of
// which need an approximation of where text sits, not its exact outline.
func fragBox(fr *frag) geom.Rect {
	const ascent, descent = 0.8, 0.2
	switch fr.orient {
	case 0:
		return geom.NewRect(fr.along0, fr.cross-descent*fr.height, fr.along1, fr.cross+ascent*fr.height)
	default:
		// Rotated: un-project the two ends and grow by the height in both axes. The
		// result is an axis-aligned box containing the text, which is what a Rect can
		// express.
		a := float64(fr.orient) * (math.Pi / 12)
		dx, dy := math.Cos(a), math.Sin(a)
		x0 := fr.along0*dx - fr.cross*dy
		y0 := fr.along0*dy + fr.cross*dx
		x1 := fr.along1*dx - fr.cross*dy
		y1 := fr.along1*dy + fr.cross*dx
		r := geom.NewRect(x0, y0, x1, y1)
		return geom.NewRect(r.X0-fr.height, r.Y0-fr.height, r.X1+fr.height, r.Y1+fr.height)
	}
}

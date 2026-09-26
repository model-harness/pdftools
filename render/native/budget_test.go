package native

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/font"
	"github.com/model-harness/pdftools/internal/cffbuild"
	"github.com/model-harness/pdftools/internal/ttfbuild"
	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
)

// TestDirectFontDictInFormIsCachedAcrossInvocations pins C25/C27's fix: a form's own font
// dictionary, named directly rather than by an indirect reference, is parsed once for the page and
// not once per invocation, and its glyphs are built once rather than re-charged into the page's
// glyph work on every Do.
//
// The glyph is heavyCFFFixture's — over half maxGlyphWork to build — so a re-parse that recharges
// it on the form's second invocation crosses the budget during the survey itself, which walks the
// same Do twice with nothing resetting w.glyphWork between them: the page is refused outright,
// naming the budget, not silently drawn short a glyph. (A form reached as a direct stream, rather
// than cached and re-parsed by reference, is what turns a recharge into a drop instead of a
// refusal — TestDirectStreamFormFontsPaintTheirOwnGlyph below is that case.) Drawn through the
// form twice, at two places, so a mutant that let the recharge through would leave a native render
// disagreeing with pdfium — refusing a page pdfium draws either way.
func TestDirectFontDictInFormIsCachedAcrossInvocations(t *testing.T) {
	subrs, heavy, _, _ := heavyCFFFixture()
	prog := cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{{SID: sidA, CharString: heavy}}}.Build()

	desc := "<</Type/FontDescriptor/FontName/Heavy/Flags 32/ItalicAngle 0/Ascent 800/Descent -200" +
		"/CapHeight 700/StemV 80/FontBBox[0 -200 1000 800]/FontFile3 7 0 R>>"
	stream := fmt.Sprintf("<</Subtype/Type1C/Length %d>>\nstream\n%s\nendstream", len(prog), prog)
	// The /Font entry is the dictionary itself, not a reference to one — the shape that has no
	// indirect reference for fontRefs to cache by, and relies on formFonts instead.
	fontDict := "<</Type/Font/Subtype/Type1/BaseFont/Heavy/FirstChar 65/LastChar 65/Widths[500]" +
		"/Encoding/WinAnsiEncoding/FontDescriptor 6 0 R>>"
	form := formXO("0 0 200 200", "/Resources<</Font<</F2 "+fontDict+">>>>",
		"BT /F2 24 Tf 20 100 Td (A) Tj ET")

	path := xoPDF(t, "/Fm1 5 0 R",
		"q 1 0 0 1 0 0 cm /Fm1 Do Q q 1 0 0 1 0 -60 cm /Fm1 Do Q", form, desc, stream)

	mean, worst, over := comparePath(t, path, 72)
	t.Logf("mean %.3f worst %.0f over-32 %d", mean, worst, over)
	if mean > 1.5 || over > 0 {
		t.Errorf("mean %.3f, %d pixels over 32 — a direct font dict in a form drawn twice should"+
			" paint the glyph both times, not drop one to a recharge of the budget", mean, over)
	}
}

// TestDrawWorkAloneExceedsTheBudget pins C9's draw side: a glyph cheap to build and rich in
// segments, shown enough times, is refused on its draw cost alone. The build side of the budget
// never sees this, since the outline is built once and cached from the first show on.
func TestDrawWorkAloneExceedsTheBudget(t *testing.T) {
	// One moveto and forty linetos: a few dozen operations to build, and a segment for every
	// lineto plus the move and the close endchar draws.
	items := []any{100, 100, cffbuild.RMoveTo}
	for i := 0; i < 40; i++ {
		items = append(items, 10)
	}
	items = append(items, cffbuild.HLineTo, cffbuild.EndChar)
	cs := cffbuild.CS(items...)

	// The premise, measured rather than assumed: cheap to build, rich in segments, so a page that
	// shows it enough times is a test of draw work and not of build work.
	c, err := font.ParseCFF(cffGlyph(cs).Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	out, ops, err := c.Outline(1, math.MaxInt)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	segments := len(out)
	if ops > 100 || segments < 40 {
		t.Fatalf("glyph = %d ops, %d segments; want cheap to build and rich in segments", ops, segments)
	}

	shows := maxGlyphWork/segments + 2
	stream := fmt.Sprintf("BT /F1 8 Tf 0 0 Td (%s) Tj ET", strings.Repeat("A", shows))
	err = renderErr(t, textPDF(t, stream, 200, cffFont(cffGlyph(cs))...))
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want an *Unsupported naming the budget", err)
	}
	if joined, want := strings.Join(u.Ops, " | "),
		"the page's glyphs take more than 4194304 operations to build and draw"; !strings.Contains(joined, want) {
		t.Errorf("refusal = %q, want it to mention %q", joined, want)
	}
}

// TestGlyphWorkStaysBoundedAfterThePageIsOver pins R#1: once glyphWork has passed maxGlyphWork,
// chargeGlyphWork stops adding to it, so a page that keeps showing glyphs after the page is
// already refused cannot make the counter climb without bound. Unguarded, that accumulation is
// what wraps a 32-bit int negative on GOARCH=386 (finding #1): the next distinct glyph would then
// be handed a remaining budget of maxGlyphWork minus a negative number, over a billion operations.
//
// Reusing TestDrawWorkAloneExceedsTheBudget's glyph — cheap to build, rich in segments — and
// showing it 100,000 times past the point that already crosses the budget: every one of those
// extra shows still reaches checkText and calls chargeGlyphWork, and none of them may add to it.
// 100,000 rather than a handful, so that an unguarded add — one that only checked afterward,
// rather than gating the add itself — could not stay inside this test's tolerance by accident:
// 100,000 further shows of a ~42-segment glyph would add about 4.2 million more, far past
// wantSlack, where a guarded page adds nothing at all once it is over.
func TestGlyphWorkStaysBoundedAfterThePageIsOver(t *testing.T) {
	// wantSlack is a generous ceiling on how far past maxGlyphWork the one charge that actually
	// crosses it can push glyphWork — this glyph draws only tens of segments, nowhere near
	// font's unexported maxGlyphSegments, so wantSlack is not a tight bound, only a large enough
	// one that an unguarded add (see the mutant this test kills) cannot pass it by accident.
	const wantSlack = 1 << 16

	items := []any{100, 100, cffbuild.RMoveTo}
	for i := 0; i < 40; i++ {
		items = append(items, 10)
	}
	items = append(items, cffbuild.HLineTo, cffbuild.EndChar)
	cs := cffbuild.CS(items...)

	c, err := font.ParseCFF(cffGlyph(cs).Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	out, _, err := c.Outline(1, math.MaxInt)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	segments := len(out)

	shows := maxGlyphWork/segments + 2 + 100_000
	stream := fmt.Sprintf("BT /F1 8 Tf 0 0 Td (%s) Tj ET", strings.Repeat("A", shows))
	path := textPDF(t, stream, 200, cffFont(cffGlyph(cs))...)

	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	page, err := s.Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	data, err := s.PageContent(1)
	if err != nil {
		t.Fatalf("PageContent: %v", err)
	}
	res, _ := objects.GetDict(s, page, "Resources")
	w := &walker{
		s: s, res: res, unsup: map[string]bool{},
		fonts:     map[string]*textFont{},
		fontRefs:  map[objects.Ref]*textFont{},
		formFonts: map[objects.Ref]map[string]*textFont{},
		images:    map[objects.Ref]*image.NRGBA{},
		forms:     map[objects.Ref][]byte{},
		iccSpaces: map[objects.Ref]space{},
	}
	w.run(data) // paint never starts: the survey alone crosses the budget

	if len(w.unsup) == 0 {
		t.Fatal("want the page refused")
	}
	if w.glyphWork <= maxGlyphWork || w.glyphWork > maxGlyphWork+wantSlack {
		t.Errorf("glyphWork = %d after %d shows past the crossing, want in (%d, %d]",
			w.glyphWork, shows, maxGlyphWork, maxGlyphWork+wantSlack)
	}
}

// TestChargeGlyphWorkExactBoundary pins chargeGlyphWork's own guard, which stops glyphWork
// climbing without bound once a page is already refused (see its comment above). Weakening its
// "<=" to "<" is what this test kills: once glyphWork lands exactly on maxGlyphWork, that mutant
// drops every further charge, and a page with unlimited further shows is accepted.
//
// The draw side is what actually depends on the guard: a show of a glyph already in outline()'s
// cache returns straight from the cache hit at the top of outline(), text.go, without going
// through outline()'s own "w.glyphWork > maxGlyphWork" check at all, so chargeGlyphWork's guard is
// the only thing standing between an exactly spent budget and a free run of further shows. A new
// build is refused either way, by outline()'s pre-check or by font.Outline's own budget of zero,
// but is pinned here too, so both halves of the boundary the comment above claims are checked.
func TestChargeGlyphWorkExactBoundary(t *testing.T) {
	// A glyph already cached, the shape outline() hands back without spending any budget at all —
	// real fonts are not needed to pin chargeGlyphWork's own arithmetic.
	tf := &textFont{outlines: map[uint16]glyphOutline{1: {out: font.Outline{{}}}}}

	t.Run("one under the budget, a further charge of one lands on it and is accepted", func(t *testing.T) {
		w := &walker{glyphWork: maxGlyphWork - 1}
		out, err := w.outline(tf, 1)
		if err != nil {
			t.Fatalf("outline: %v", err)
		}
		w.chargeGlyphWork(len(out))
		if w.glyphWork != maxGlyphWork {
			t.Fatalf("glyphWork = %d, want exactly %d", w.glyphWork, maxGlyphWork)
		}
	})

	t.Run("exactly at the budget, a further draw charge of one is still counted and crosses it", func(t *testing.T) {
		w := &walker{glyphWork: maxGlyphWork}
		out, err := w.outline(tf, 1)
		if err != nil {
			t.Fatalf("outline: %v", err)
		}
		w.chargeGlyphWork(len(out))
		if w.glyphWork != maxGlyphWork+1 {
			t.Errorf("glyphWork = %d, want %d: a charge landing exactly on the budget must still be"+
				" counted, or every further show of an already-built glyph is free", w.glyphWork, maxGlyphWork+1)
		}
	})

	t.Run("exactly at the budget, a further build is refused too", func(t *testing.T) {
		cs := cffbuild.CS(100, 100, cffbuild.RMoveTo, 100, cffbuild.HLineTo, cffbuild.EndChar)
		c, err := font.ParseCFF(cffGlyph(cs).Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		w := &walker{glyphWork: maxGlyphWork}
		if _, err := w.outline(&textFont{cff: c}, 1); !errors.Is(err, font.ErrGlyphBudget) {
			t.Errorf("outline() = %v, want ErrGlyphBudget: no budget remains once glyphWork is"+
				" already at maxGlyphWork", err)
		}
	})
}

// TestOutlineIsGivenTheBudgetRemainingNotTheWhole pins R#3: outline() (text.go) passes
// font.Outline the budget still remaining on the page — maxGlyphWork-w.glyphWork — and not a
// constant or the full per-glyph allowance, so a glyph reached with almost none of the budget
// left cannot spend up to a whole budget's worth before the page is refused.
//
// Each glyph is a "runaway": built to keep spending long after any reasonable remaining budget
// would have refused it, so a caller that passed the wrong argument — 1<<30, say — would let it
// run far past the small budget these subtests actually leave.
func TestOutlineIsGivenTheBudgetRemainingNotTheWhole(t *testing.T) {
	const remaining = 10

	t.Run("CFF", func(t *testing.T) {
		subrs, _, depth, fan := heavyCFFFixture()
		// A subr fan-out well over 100k operations that draws nothing: exactCFFOps pads with
		// subroutine calls and dotsection, neither a path operator.
		runaway := exactCFFOps(subrs, depth, fan, 200_000)
		c, err := font.ParseCFF(cffbuild.Builder{Subrs: subrs,
			Glyphs: []cffbuild.Glyph{{SID: sidA, CharString: runaway}}}.Build())
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		w := &walker{glyphWork: maxGlyphWork - remaining}
		if _, err := w.outline(&textFont{cff: c}, 1); !errors.Is(err, font.ErrGlyphBudget) {
			t.Errorf("outline() = %v, want ErrGlyphBudget: %d ops against a %d-op remaining budget",
				err, 200_000, remaining)
		}
		// A CFF build cannot overshoot the budget it was given by more than one operation: the
		// interpreter checks after every single op (charstring.go's exec).
		if want := maxGlyphWork + 1; w.glyphWork > want {
			t.Errorf("glyphWork = %d, want at most %d", w.glyphWork, want)
		}
	})

	t.Run("TrueType", func(t *testing.T) {
		// A composite with one component, over a simple glyph of leafPoints points in one
		// contour — just under maxGlyphPoints, so it is not refused for its size alone, and far
		// more than the remaining budget.
		const leafPoints = 9999
		leaf := binary.BigEndian.AppendUint16(nil, 1) // numberOfContours
		leaf = append(leaf, make([]byte, 8)...)       // bbox
		leaf = binary.BigEndian.AppendUint16(leaf, uint16(leafPoints-1))
		leaf = binary.BigEndian.AppendUint16(leaf, 0) // instructionLength
		for i := 0; i < leafPoints; i++ {
			leaf = append(leaf, 0x31) // on-curve, x-same, y-same: no coordinate bytes needed
		}
		root := binary.BigEndian.AppendUint16(nil, 0xFFFF)        // composite
		root = append(root, make([]byte, 8)...)                   // bbox
		root = binary.BigEndian.AppendUint16(root, 0x0001|0x0002) // ARG_1_AND_2_ARE_WORDS|ARGS_ARE_XY_VALUES
		root = binary.BigEndian.AppendUint16(root, 0)             // sub: the leaf, gid 0
		root = binary.BigEndian.AppendUint16(root, 0)             // dx
		root = binary.BigEndian.AppendUint16(root, 0)             // dy

		glyf := append(append([]byte{}, leaf...), root...)
		var loca []byte
		for _, o := range []uint32{0, uint32(len(leaf)), uint32(len(glyf))} {
			loca = binary.BigEndian.AppendUint32(loca, o)
		}
		head := make([]byte, 54)
		binary.BigEndian.PutUint16(head[18:], 1000) // unitsPerEm
		binary.BigEndian.PutUint16(head[50:], 1)    // indexToLocFormat: long
		maxp := make([]byte, 6)
		binary.BigEndian.PutUint16(maxp[4:], 2) // numGlyphs
		// FreeType will not open a face without hhea and hmtx, so ParseTrueType refuses one too.
		hhea := make([]byte, 36)
		binary.BigEndian.PutUint16(hhea[34:], 2) // numberOfHMetrics
		hmtx := make([]byte, 8)

		data := ttfbuild.AssembleTables(map[string][]byte{
			"head": head, "hhea": hhea, "hmtx": hmtx, "maxp": maxp, "glyf": glyf, "loca": loca,
		})
		tt, err := font.ParseTrueType(data)
		if err != nil {
			t.Fatalf("ParseTrueType: %v", err)
		}
		w := &walker{glyphWork: maxGlyphWork - remaining}
		if _, err := w.outline(&textFont{tt: tt}, 1); !errors.Is(err, font.ErrGlyphBudget) {
			t.Errorf("outline() = %v, want ErrGlyphBudget: %d points against a %d-op remaining budget",
				err, leafPoints, remaining)
		}
		// A TrueType build can overshoot the remaining budget by as much as one charge — up to
		// leafPoints here — but never past maxGlyphSegments (1<<16): a contour count is an int16,
		// at most 32767, and a point count is capped at maxGlyphPoints, 10000, both comfortably
		// under it.
		if want := maxGlyphWork + 1<<16; w.glyphWork > want {
			t.Errorf("glyphWork = %d, want at most %d", w.glyphWork, want)
		}
	})
}

// TestDirectStreamFormFontsPaintTheirOwnGlyph pins R#2 (finding #2): a form reached as a direct
// stream — which only an in-memory store hands back, since a file's streams are indirect objects
// (ISO 32000-2 §7.3.8) — re-parses its own direct /Font dictionary on every invocation, in paint
// as much as in the survey. Without resetting w.glyphWork to zero before paint starts, that
// re-parse recharges the survey's own total on top of itself, crosses the budget on a page the
// survey accepted, and drops the glyph — silently, with err == nil for the page.
//
// Two forms rather than one, each with its own direct /Font /F1 over its own heavy glyph, both
// invoked once: the survey sees each font parsed and charged exactly once and accepts the page,
// so only paint's un-reset recharge — not a form invoked twice, TestDirectFontDictInFormIsCached
// AcrossInvocations's case — can be what drops a glyph here.
func TestDirectStreamFormFontsPaintTheirOwnGlyph(t *testing.T) {
	subrs, _, _, _ := heavyCFFFixture()
	// Two calls to the top of the fan-out chain, not three: a known fraction under half
	// maxGlyphWork apiece, so the two forms' fonts fit the budget together but a further
	// recharge of either does not.
	glyph := func(dx, dy float64) []byte {
		return cffbuild.CS(-107, cffbuild.CallSubr, -107, cffbuild.CallSubr,
			dx, dy, cffbuild.RMoveTo, 100, 100, -100, cffbuild.HLineTo, cffbuild.EndChar)
	}
	prog1 := cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{{SID: sidA, CharString: glyph(20, 20)}}}.Build()
	prog2 := cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{{SID: sidA, CharString: glyph(60, 60)}}}.Build()

	// The premise, measured rather than assumed: each glyph alone is a substantial fraction of
	// the budget, and the two together still fit under it, so the survey accepts.
	measure := func(prog []byte) int {
		c, err := font.ParseCFF(prog)
		if err != nil {
			t.Fatalf("ParseCFF: %v", err)
		}
		_, ops, err := c.Outline(1, math.MaxInt)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		return ops
	}
	ops1, ops2 := measure(prog1), measure(prog2)
	if ops1+ops2 > maxGlyphWork {
		t.Fatalf("the two glyphs together cost %d, want under %d so the survey accepts", ops1+ops2, maxGlyphWork)
	}
	if ops1 < maxGlyphWork/3 || ops2 < maxGlyphWork/3 {
		t.Fatalf("glyph costs %d and %d, want each a substantial fraction of %d", ops1, ops2, maxGlyphWork)
	}

	fontDict := func(name string, descRef int) string {
		return fmt.Sprintf("<</Type/Font/Subtype/Type1/BaseFont/%s/FirstChar 65/LastChar 65"+
			"/Widths[500]/Encoding/WinAnsiEncoding/FontDescriptor %d 0 R>>", name, descRef)
	}
	desc := func(name string, fileRef int) string {
		return fmt.Sprintf("<</Type/FontDescriptor/FontName/%s/Flags 32/ItalicAngle 0/Ascent 800"+
			"/Descent -200/CapHeight 700/StemV 80/FontBBox[0 -200 1000 800]/FontFile3 %d 0 R>>", name, fileRef)
	}
	stream := func(prog []byte) string {
		return fmt.Sprintf("<</Subtype/Type1C/Length %d>>\nstream\n%s\nendstream", len(prog), prog)
	}

	form1 := formXO("0 0 200 200", "/Resources<</Font<</F1 "+fontDict("Heavy1", 6)+">>>>",
		"BT /F1 24 Tf 20 100 Td (A) Tj ET")
	form2 := formXO("0 0 200 200", "/Resources<</Font<</F1 "+fontDict("Heavy2", 9)+">>>>",
		"BT /F1 24 Tf 20 100 Td (A) Tj ET")

	path := xoPDF(t, "/Fm1 5 0 R/Fm2 8 0 R",
		"q 1 0 0 1 0 0 cm /Fm1 Do Q q 1 0 0 1 0 -60 cm /Fm2 Do Q",
		form1, desc("Heavy1", 7), stream(prog1), form2, desc("Heavy2", 10), stream(prog2))

	// The forms are indirect references here, 5 0 R and 8 0 R, which a file-backed store always
	// hands back — this render is the baseline the direct-stream case below has to match.
	refRaster := renderNative(t, path, 72)
	refInk, w, h := ink(refRaster.Image)
	if sum := 0.0; true {
		for _, v := range refInk {
			sum += float64(v)
		}
		if sum == 0 {
			t.Fatal("the reference page drew nothing, so this comparison asserts nothing")
		}
	}

	// A store that hands the resolved *objects.Stream back in place of the /XObject entries'
	// references — the only way to make a form's stream direct, which an in-memory store would
	// do itself and this fakes on top of a file-backed one instead of committing one.
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	page, err := s.Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	res, _ := objects.GetDict(s, page, "Resources")
	xo, _ := objects.GetDict(s, res, "XObject")
	directXO := objects.Dict{}
	for name, ref := range xo {
		v, err := s.Resolve(ref)
		if err != nil {
			t.Fatalf("Resolve %s: %v", name, err)
		}
		directXO[name] = v
	}
	nres := objects.Dict{}
	for k, v := range res {
		nres[k] = v
	}
	nres["XObject"] = directXO
	npage := objects.Dict{}
	for k, v := range page {
		npage[k] = v
	}
	npage["Resources"] = nres

	o := render.DefaultOptions
	o.DPI = 72
	got, err := New(&directPageStore{Store: s, page: npage}).Page(1, o)
	if err != nil {
		t.Fatalf("Page with direct-stream forms: %v", err)
	}
	gotInk, gw, gh := ink(got.Image)
	if gw != w || gh != h {
		t.Fatalf("size %dx%d, want %dx%d", gw, gh, w, h)
	}
	for i := range gotInk {
		if gotInk[i] != refInk[i] {
			t.Fatalf("pixel %d = %d, want %d: a direct-stream form's own glyph was dropped in "+
				"paint by a recharge against the survey's total", i, gotInk[i], refInk[i])
		}
	}
}

// directPageStore hands back a fixed page dictionary — direct streams and all — the way an
// in-memory store's Page always returns whatever object it is holding, rather than the reference
// a file-backed store's /XObject entries hold instead.
type directPageStore struct {
	objects.Store
	page objects.Dict
}

func (d *directPageStore) Page(int) (objects.Dict, error) { return d.page, nil }

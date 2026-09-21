package extract

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// fracDraw is one synthetic stacked fraction: two baselines with a bar between them.
//
// It exists because the corpus cannot test this. Every fraction on disk is in a gitignored
// ISO document, so a clone without those PDFs runs no fraction test at all — and the two
// producers draw the bar incompatibly, one filling a rectangle and the other stroking a
// line, so a fixture from either covers half the rule. Both are reachable from one field
// here.
//
// newFrac's defaults are a 12pt "12 over 116", the pair ISO 32000-2 page 205 sets, with
// every measured quantity inside its band: overhang 0.049, gap 1.167, balance 0.307,
// thickness 0.6pt. Each test changes only the quantity it is about, so a failure names the
// threshold that moved.
type fracDraw struct {
	num, den   string
	numX, denX float64
	numY, denY float64

	// numTail is drawn on the numerator's baseline to its right, and denHead on the
	// denominator's to its left. Each is what makes the side test reachable: a level with
	// text outside the bar's extent on the solidus's side has nowhere to put it.
	//
	// Both are set in Courier rather than Helvetica, and that is load-bearing. place()
	// continues a fragment across any distance on one baseline as long as the style, the
	// MCID and the artifact flag match, so same-style text beside a level joins the level's
	// own fragment and is then excluded with it — which rejects the candidate for having no
	// level to gather rather than for having text on the wrong side. A style change is what
	// makes it a separate fragment and the side test the thing under test.
	numTail, denHead string
	tailX, headX     float64

	// from and to are the bar's extent, y its lower edge.
	from, to, y float64

	// thick is a filled bar's rectangle height; stroke is a stroked bar's line width.
	// Exactly one is set, and which one is the whole difference between the producers.
	thick, stroke float64
}

func newFrac() fracDraw {
	return fracDraw{
		num: "12", den: "116",
		numX: 100, denX: 100,
		numY: 700, denY: 686,
		from: 99.7, to: 120.3,
		y:     695.4,
		thick: 0.6,
	}
}

func (f fracDraw) stream() string {
	var b strings.Builder
	show := func(font string, x, y float64, s string) {
		fmt.Fprintf(&b, "BT /%s 12 Tf 1 0 0 1 %g %g Tm (%s) Tj ET\n", font, x, y, s)
	}
	// Drawn in baseline order, because a text object at a different cross closes the open
	// line: an extra fragment emitted after the denominator would be a third line rather
	// than part of the level it is meant to sit beside.
	show("F1", f.numX, f.numY, f.num)
	if f.numTail != "" {
		show("F2", f.tailX, f.numY, f.numTail)
	}
	show("F1", f.denX, f.denY, f.den)
	if f.denHead != "" {
		show("F2", f.headX, f.denY, f.denHead)
	}
	if f.stroke > 0 {
		fmt.Fprintf(&b, "%g w %g %g m %g %g l S\n", f.stroke, f.from, f.y, f.to, f.y)
	} else {
		fmt.Fprintf(&b, "%g %g %g %g re f\n", f.from, f.y, f.to-f.from, f.thick)
	}
	return b.String()
}

func (f fracDraw) text(t *testing.T) string {
	t.Helper()
	return extractText(t, f.stream())
}

// want asserts the whole page text, so a rejected candidate is pinned as the two lines it
// stays rather than merely as "no solidus". A wrong join and no join are different
// failures and an assertion on the separator alone cannot tell them apart.
func (f fracDraw) want(t *testing.T, want string) {
	t.Helper()
	if got := f.text(t); got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

// unjoined is the default pair's text when the bar is not taken as one: two lines in one
// paragraph, wrapped with an inferred space.
const unjoined = "12 116"

// TestFractionFromFilledBar is the base case, and the one every ISO document on disk
// draws: the bar is a filled rectangle, so it arrives as both of its long edges.
func TestFractionFromFilledBar(t *testing.T) {
	f := newFrac()
	f.want(t, "12/116")
	if n := strings.Count(f.text(t), "/"); n != 1 {
		t.Errorf("%d solidi, want 1: a filled pair must be taken once, not once per edge", n)
	}
}

// TestFractionFromStrokedBar is the same fraction drawn the other way, and is the reason
// doc.Rule carries a width at all.
//
// pdfTeX strokes a fraction bar — "0.398 w 0 0 m 15.781 0 l S" on page 12 of the arXiv
// paper in docs/ — which emits one rule with no second edge anywhere on the page. A reader
// that recovers thickness only from a pair of edges finds every fraction in a filled
// document and none in a stroked one.
func TestFractionFromStrokedBar(t *testing.T) {
	f := newFrac()
	f.thick, f.stroke = 0, 0.398
	f.want(t, "12/116")
}

// TestFractionDropsABlankFragmentPastTheNumerator is the trim's other route.
//
// levelOf skips a whitespace-only fragment before it tests the extent, so such a fragment is
// neither gathered into the level nor counted as text after it, and TrimRightFunc on the run's
// last fragment cannot reach it — the space is in a fragment of its own. It emitted "12/ 116",
// which is the defect joinPrev exists to prevent arriving where joinPrev cannot see it.
//
// The Courier is load-bearing for the same reason fracDraw's numTail is: a same-style space on
// one baseline joins the level's own fragment and is trimmed with it.
func TestFractionDropsABlankFragmentPastTheNumerator(t *testing.T) {
	got := extractText(t, "BT /F1 12 Tf 1 0 0 1 100 700 Tm (12) Tj ET\n"+
		"BT /F2 12 Tf 1 0 0 1 114 700 Tm ( ) Tj ET\n"+
		"BT /F1 12 Tf 1 0 0 1 100 686 Tm (116) Tj ET\n"+
		"99.7 695.4 20.6 0.6 re f")
	if got != "12/116" {
		t.Errorf("text = %q, want %q", got, "12/116")
	}
}

// TestFractionDoesNotChainTwoBars pins that a line is one level of one fraction.
//
// Three baselines with a bar between each pair is a fraction whose numerator is a fraction, and
// taking both pairs gives "12/34/56" — a form ISO 80000-2 does not allow without parentheses,
// so it asserts something the standard this rewrite cites refuses to read. The second bar is
// left undivided: the output says less than the page does rather than something else.
func TestFractionDoesNotChainTwoBars(t *testing.T) {
	got := extractText(t, "BT /F1 12 Tf 1 0 0 1 100 700 Tm (12) Tj ET\n"+
		"BT /F1 12 Tf 1 0 0 1 100 686 Tm (34) Tj ET\n"+
		"BT /F1 12 Tf 1 0 0 1 100 672 Tm (56) Tj ET\n"+
		"99.7 695.4 14.0 0.6 re f\n"+
		"99.7 681.4 14.0 0.6 re f")
	if strings.Count(got, "/") != 1 {
		t.Errorf("text = %q, want one solidus — a repeated one is not a form ISO 80000-2 allows", got)
	}
	if got != "12/34 56" {
		t.Errorf("text = %q, want %q", got, "12/34 56")
	}
}

// TestFractionNotJoinedAcrossTheArtifactBoundary pins that both levels are the same kind of
// content.
//
// The assembly loop drops an artifact line, so joining across the boundary writes a solidus
// into a line that survives and takes away the text that completes it: a numerator marked as
// page furniture with a visible denominator emitted "12/", a division with no divisor.
func TestFractionNotJoinedAcrossTheArtifactBoundary(t *testing.T) {
	got := extractText(t, "BT /F1 12 Tf 1 0 0 1 100 700 Tm (12) Tj ET\n"+
		"/Artifact <</Type /Pagination>> BDC BT /F1 12 Tf 1 0 0 1 100 686 Tm (116) Tj ET EMC\n"+
		"99.7 695.4 20.6 0.6 re f")
	if got != "12" {
		t.Errorf("text = %q, want %q — the denominator is an artifact and the numerator is not", got, "12")
	}
}

// TestBarMarkCentresAFilledPair pins that a pair collapses to one mark at its midpoint.
//
// The midpoint rather than either edge, because the balance ratio is what decides a bar and
// the two edges give different ratios. Which edge a producer emitted first must not change
// the answer.
func TestBarMarkCentresAFilledPair(t *testing.T) {
	hs := []doc.Rule{
		{Pos: 100, From: 10, To: 50},
		{Pos: 100.6, From: 10, To: 50},
	}
	var got []float64
	for _, b := range hs {
		if c, ok := barMark(hs, b); ok {
			got = append(got, c.Pos)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d marks from one pair, want 1: %v", len(got), got)
	}
	if got[0] != 100.3 {
		t.Errorf("mark at %v, want 100.3 — the midpoint of the two edges", got[0])
	}
}

// TestBarMarkRejectsAGridLine is the false positive that forced the measurement to be one
// quantity with three outcomes rather than two separate tests.
//
// A table draws its rules at one extent and several positions, so three or more rules at one
// extent are a grid whatever their thickness. Both paint kinds have to be rejected: ISO
// 32000-2 fills its table rules and LaTeX's booktabs strokes them, and the stroked one on
// page 11 of the arXiv paper passes overhang at 0.1707 and balance at 0.3887, with a 0.797pt
// width well inside barThickMax. Nothing else sees it.
func TestBarMarkRejectsAGridLine(t *testing.T) {
	for _, w := range []float64{0, 0.4} {
		hs := []doc.Rule{
			{Pos: 700, From: 10, To: 50, Width: w},
			{Pos: 714, From: 10, To: 50, Width: w},
			{Pos: 728, From: 10, To: 50, Width: w},
		}
		for _, b := range hs {
			if _, ok := barMark(hs, b); ok {
				t.Errorf("width %v: rule at %v accepted, want rejected — three at one extent, 14pt apart, which is row spacing", w, b.Pos)
			}
		}
	}
}

// TestBarMarkAcceptsTwoBarsAtOneExtent is the defect testdata/reference/fractions.pdf was
// built to catch.
//
// A page that sets the same formula twice draws two bars of the same width at the same
// indent, and each is then the other's far same-extent partner. Condemning a rule for having
// any such partner made the two cancel each other out: both of that fixture's compound
// equations lost their bar, in a document whose bars are strokes carrying their own
// thickness. The extent here is the fixture's own, 294.554 to 316.693, which "1 + 2" and
// "3 + 4" share because they set to the same width in Computer Modern.
func TestBarMarkAcceptsTwoBarsAtOneExtent(t *testing.T) {
	hs := []doc.Rule{
		{Pos: 500, From: 294.554, To: 316.693, Width: 0.398},
		{Pos: 440, From: 294.554, To: 316.693, Width: 0.398},
	}
	for _, b := range hs {
		if _, ok := barMark(hs, b); !ok {
			t.Errorf("bar at %v rejected, want accepted — its only partner is 60pt away and it carries its own 0.398pt width", b.Pos)
		}
	}
}

// TestBarMarkGridCountBound straddles gridRules, in counts rather than in the constant.
//
// The last two cases are what keeps extentCount's tolerance honest: it measures both ends, so
// a third rule sharing only one of them is a different rule and does not make a grid. Without
// both comparisons a table's cell shading beside a bar would be counted as part of it.
func TestBarMarkGridCountBound(t *testing.T) {
	for _, c := range []struct {
		name string
		hs   []doc.Rule
		want bool
	}{
		{"two at one extent", []doc.Rule{
			{Pos: 700, From: 10, To: 50, Width: 0.4},
			{Pos: 730, From: 10, To: 50, Width: 0.4},
		}, true},
		{"three at one extent", []doc.Rule{
			{Pos: 700, From: 10, To: 50, Width: 0.4},
			{Pos: 730, From: 10, To: 50, Width: 0.4},
			{Pos: 760, From: 10, To: 50, Width: 0.4},
		}, false},
		{"a third ending elsewhere", []doc.Rule{
			{Pos: 700, From: 10, To: 50, Width: 0.4},
			{Pos: 730, From: 10, To: 50, Width: 0.4},
			{Pos: 760, From: 10, To: 51, Width: 0.4},
		}, true},
		{"a third beginning elsewhere", []doc.Rule{
			{Pos: 700, From: 10, To: 50, Width: 0.4},
			{Pos: 730, From: 10, To: 50, Width: 0.4},
			{Pos: 760, From: 11, To: 50, Width: 0.4},
		}, true},
		// The count is tolerant, not exact. A producer rounds a table's rules to different
		// numbers of decimals, so three rules whose ends differ by a fifth of a point are
		// still three rules at one extent — measuring equality would read that table as
		// three unrelated marks and hand all of them to the thresholds below.
		{"three at nearly one extent", []doc.Rule{
			{Pos: 700, From: 10, To: 50, Width: 0.4},
			{Pos: 730, From: 10.2, To: 50.2, Width: 0.4},
			{Pos: 760, From: 9.8, To: 49.8, Width: 0.4},
		}, false},
	} {
		if _, ok := barMark(c.hs, c.hs[0]); ok != c.want {
			t.Errorf("%s: accepted = %v, want %v", c.name, ok, c.want)
		}
	}
}

// TestBarMarkRejectsALoneFillEdge pins the ambiguity at zero width.
//
// A fill reports no width, so an edge with no same-extent neighbour has no thickness this
// package can recover: it might be one side of a region clipped away, and reading it as a
// hairline would admit every stray fill edge on the page.
func TestBarMarkRejectsALoneFillEdge(t *testing.T) {
	hs := []doc.Rule{{Pos: 700, From: 10, To: 50}}
	if _, ok := barMark(hs, hs[0]); ok {
		t.Error("a lone edge of zero width was accepted, want rejected")
	}
}

// TestBarMarkAcceptsALoneStroke pins that a stroked line is reported where it was drawn.
//
// Unmoved, unlike a pair: a stroke has no area, so its position is already the mark's
// centre and shifting it by half a thickness would put the bar off the math axis.
func TestBarMarkAcceptsALoneStroke(t *testing.T) {
	hs := []doc.Rule{{Pos: 700, From: 10, To: 50, Width: 0.398}}
	c, ok := barMark(hs, hs[0])
	if !ok {
		t.Fatal("a lone 0.398pt stroke was rejected, want accepted")
	}
	if c.Pos != 700 {
		t.Errorf("mark at %v, want 700 — a stroke is not centred, it has no second edge", c.Pos)
	}
}

// TestBarMarkStrokeThicknessBound straddles barThickMax, which is the only thing bounding a
// lone stroke: a 3pt border under a heading has no partner to disqualify it either.
//
// Both widths are written as figures rather than as barThickMax ± something, so that moving
// the constant fails the test. A fixture expressed in terms of the threshold it is checking
// passes at every value of it.
func TestBarMarkStrokeThicknessBound(t *testing.T) {
	for _, c := range []struct {
		w    float64
		want bool
	}{{1.4, true}, {1.6, false}} {
		hs := []doc.Rule{{Pos: 700, From: 10, To: 50, Width: c.w}}
		if _, ok := barMark(hs, hs[0]); ok != c.want {
			t.Errorf("a %vpt stroke: accepted = %v, want %v", c.w, ok, c.want)
		}
	}
}

// TestBarMarkPairSeparationBound straddles barThickMin.
//
// Two edges closer together than the floor are one line drawn twice, not a rectangle with
// area, and pairing them reports a mark of no thickness at all. Figures rather than the
// constant, for the reason above.
func TestBarMarkPairSeparationBound(t *testing.T) {
	for _, c := range []struct {
		lo, hi float64
		want   bool
	}{
		{700, 700.05, false},
		{700, 700.15, true},
		// barThickMin itself, and the one case here written as the constant's own value
		// rather than clear of it: the bound is inclusive and only an exact separation says
		// so. The positions are 0 and 0.1 because the difference has to *be* barThickMin —
		// 700.1 − 700 is 0.10000000000002274 in float64, above the floor rather than on it,
		// so a page whose edges are a nominal tenth of a point apart can land on either
		// side. That is a fact about the coordinates a producer writes, not a tolerance
		// this package can fix, and it is why the floor sits well under a real bar's
		// 0.60pt.
		{0, 0.1, false},
	} {
		hs := []doc.Rule{
			{Pos: c.lo, From: 10, To: 50},
			{Pos: c.hi, From: 10, To: 50},
		}
		got := 0
		for _, b := range hs {
			if _, ok := barMark(hs, b); ok {
				got++
			}
		}
		if (got == 1) != c.want {
			t.Errorf("a pair %v apart yielded %d marks, want %v", c.hi-c.lo, got, map[bool]string{true: "1", false: "0"}[c.want])
		}
	}
}

// TestBarMarkPairSeparationUpperBound straddles barThickMax on the pair, which is a
// different branch from the lone stroke TestBarMarkStrokeThicknessBound bounds.
//
// Past the ceiling the two edges are no longer read as one bar's sides: they become two rules
// that happen to share an extent, and a filled edge on its own has no thickness, so the
// count goes to zero rather than to two. Figures rather than the constant, as above.
func TestBarMarkPairSeparationUpperBound(t *testing.T) {
	for _, c := range []struct {
		d    float64
		want int
		// 1.5 is barThickMax itself, for the reason the floor's own case gives: the
		// ceiling is inclusive, and nothing but the exact value can pin that.
	}{{1.4, 1}, {1.5, 1}, {1.6, 0}} {
		hs := []doc.Rule{
			{Pos: 700, From: 10, To: 50},
			{Pos: 700 + c.d, From: 10, To: 50},
		}
		got := 0
		for _, b := range hs {
			if _, ok := barMark(hs, b); ok {
				got++
			}
		}
		if got != c.want {
			t.Errorf("a pair %vpt apart yielded %d marks, want %d", c.d, got, c.want)
		}
	}
}

// TestBarMarkExtentSlack straddles barSlack, the tolerance on "the same extent".
//
// It decides whether a nearby parallel rule is this bar's other edge or a different rule
// that happens to run alongside it. Too tight and a filled bar whose edges a producer
// rounded differently arrives as two lone zero-width edges; too loose and a table's rule
// pairs with the cell shading beside it.
func TestBarMarkExtentSlack(t *testing.T) {
	for _, c := range []struct {
		off  float64
		want bool
	}{{0.4, true}, {0.6, false}} {
		hs := []doc.Rule{
			{Pos: 700, From: 10, To: 50},
			{Pos: 700.6, From: 10 + c.off, To: 50},
		}
		if _, ok := barMark(hs, hs[0]); ok != c.want {
			t.Errorf("ends differing by %v: accepted = %v, want %v", c.off, ok, c.want)
		}
	}
}

// TestExtentRulesIgnoresOtherExtents pins that the neighbour must share both ends.
//
// A page-wide rule a fraction of a point from a bar is not that bar's other edge, and
// treating any nearby parallel rule as a twin would report the bar's thickness as the
// distance to whatever the page happened to draw beside it.
func TestExtentRulesIgnoresOtherExtents(t *testing.T) {
	b := doc.Rule{Pos: 700, From: 10, To: 50}
	hs := []doc.Rule{
		b,
		{Pos: 700.6, From: 10, To: 400},
		{Pos: 700.6, From: 11, To: 50},
	}
	o, ok, at := extentRules(hs, b)
	if ok {
		t.Errorf("found a twin at %v spanning %v-%v, want none", o.Pos, o.From, o.To)
	}
	if at != 1 {
		t.Errorf("%d positions at this extent, want 1 — b's own, and neither other rule shares it", at)
	}
	if _, ok := barMark(hs, b); ok {
		t.Error("the bar was accepted, want rejected — with no twin it is a lone zero-width edge")
	}
}

// TestExtentRulesCountsPositionsNotRules pins the half of one scan that a second scan got
// wrong: a bar a producer emitted twice is one position, not two.
//
// Two bars at one extent, each drawn twice, is four rules and two positions. Counting rules
// made that a grid — three or more — so a page that set the same formula twice *and* whose
// producer doubled its paths lost both fractions, which is this fixture's own geometry with
// the rules duplicated.
func TestExtentRulesCountsPositionsNotRules(t *testing.T) {
	one := doc.Rule{Pos: 500, From: 294.554, To: 316.693, Width: 0.398}
	two := doc.Rule{Pos: 440, From: 294.554, To: 316.693, Width: 0.398}
	hs := []doc.Rule{one, one, two, two}
	if _, _, at := extentRules(hs, one); at != 2 {
		t.Errorf("%d positions at this extent, want 2 — four rules, two of them duplicates", at)
	}
	for _, b := range []doc.Rule{one, two} {
		if _, ok := barMark(hs, b); !ok {
			t.Errorf("the bar at %v was rejected, want accepted — a doubled path is not a third rule", b.Pos)
		}
	}
	if got := len((&run{rules: hs}).bars()); got != 2 {
		t.Errorf("%d marks from two doubled bars, want 2 — one per mark is bars' contract", got)
	}
	// Interleaved, which is what the sort's second key is for: these four marks share a From,
	// so ordering on From alone leaves them in whatever order the sort happened to produce and
	// a duplicate need not land beside its twin. Dedupe only ever compares neighbours.
	inter := []doc.Rule{one, two, one, two}
	if got := len((&run{rules: inter}).bars()); got != 2 {
		t.Errorf("%d marks from the same four rules drawn in two passes, want 2", got)
	}
}

// TestLevelNotGatheredWhenItsRunIsSplit pins the contiguity precondition, which declines
// nothing over the corpus and so has no case but this one.
//
// The solidus is written into the numerator's last gathered fragment, so a level in two pieces
// has no single place for it. The three fragments here are inside the bar, straddling its right
// edge, and inside again — overlapping on the page, which a content stream may do and a
// typesetter would not — so sorting by position cannot make the run contiguous. That is the
// only shape that reaches this guard: anything positionally between two fragments the bar spans
// is itself spanned, the extent being an interval.
func TestLevelNotGatheredWhenItsRunIsSplit(t *testing.T) {
	ln := &line{cross: 700, frags: []frag{
		{text: []byte("12"), along0: 100, along1: 114, height: 12},
		{text: []byte("out"), along0: 115, along1: 130, height: 12},
		{text: []byte("34"), along0: 116, along1: 120, height: 12},
	}}
	if _, ok := levelOf(ln, doc.Rule{Pos: 695, From: 99.7, To: 120.3}); ok {
		t.Error("a level in two pieces was gathered, want rejected — there is no one place for the solidus")
	}
}

// TestFractionRejectsAThickFilledBar and its stroked counterpart pin the same bound on
// each paint kind, since the two measure thickness by different routes.
func TestFractionRejectsAThickFilledBar(t *testing.T) {
	f := newFrac()
	f.thick = 2.0
	f.want(t, unjoined)
}

func TestFractionRejectsAThickStrokedBar(t *testing.T) {
	f := newFrac()
	f.thick, f.stroke = 0, 2.0
	f.want(t, unjoined)
}

// TestFractionOverhangAtTheThreshold straddles barOverhangMax from both sides, 0.1pt
// apart, so a threshold moved in either direction fails.
//
// The default denominator is 20.016pt wide and the type 12pt, so the bar may be 23.016pt
// before it overhangs by more than a quarter of the type size.
func TestFractionOverhangAtTheThreshold(t *testing.T) {
	just := newFrac()
	just.to = just.from + 23.0
	just.want(t, "12/116")

	over := newFrac()
	over.to = over.from + 23.1
	over.want(t, unjoined)
}

// TestFractionBalanceAtTheThresholds straddles both ends of the balance band.
//
// A bar sits on the math axis, above the middle of the gap. Below the band it is an
// underline hanging under its own baseline; above it, it is nearer the lower line than the
// upper and divides nothing.
func TestFractionBalanceAtTheThresholds(t *testing.T) {
	// The mark is the pair's midpoint, 0.3pt above the rectangle's lower edge, and the
	// gap is 14pt, so balance is (700 - y - 0.3) / 14.
	cases := []struct {
		name string
		y    float64
		want string
	}{
		{"just inside the low end at 0.257", 696.1, "12/116"},
		{"just under the low end at 0.243", 696.3, unjoined},
		{"just inside the high end at 0.393", 694.2, "12/116"},
		{"just over the high end at 0.407", 694.0, unjoined},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFrac()
			f.y = c.y
			f.want(t, c.want)
		})
	}
}

// TestFractionGapAtTheThreshold straddles levelGapMax with balance held at 0.32, so the
// gap is the only quantity moving.
func TestFractionGapAtTheThreshold(t *testing.T) {
	just := newFrac()
	just.denY, just.y = 700-21.4, 692.852
	just.want(t, "12/116")

	over := newFrac()
	over.denY, over.y = 700-21.8, 692.724
	over.want(t, "12\n116")
}

// TestFractionLevelsStayInOneBlock is the defect the gap test above found.
//
// Two levels more than ParaFrac apart are two paragraphs by every test in blocks(), so the
// solidus ended one block and the denominator opened the next — "12/" then "116", which
// reads as two quantities again and is worse than the unrewritten pair. A tagged document
// is rescued by sameElement, because both levels share an MCID; an untagged one has no MCID
// to share, and pdfTeX's are untagged. 21.4pt over 12pt type is 1.783, inside levelGapMax
// and past ParaFrac, which is the whole band where the two stages disagreed.
func TestFractionLevelsStayInOneBlock(t *testing.T) {
	f := newFrac()
	f.denY, f.y = 700-21.4, 692.852
	p := extractPage(t, f.stream())
	if len(p.Blocks) != 1 {
		t.Fatalf("%d blocks, want 1: a joined pair is one expression and cannot span a paragraph boundary", len(p.Blocks))
	}
	if strings.Contains(p.Text(), "\n") {
		t.Errorf("text = %q, want no newline", p.Text())
	}
}

// TestFractionParenthesizesACompoundNumerator pins the ISO 80000-2 requirement: a solidus
// binds tighter than the sum above it, so the numerator needs brackets or the rewrite
// changes the value.
func TestFractionParenthesizesACompoundNumerator(t *testing.T) {
	f := newFrac()
	f.num, f.to = "L + 16", 134.0
	f.want(t, "(L + 16)/116")
}

func TestFractionParenthesizesACompoundDenominator(t *testing.T) {
	f := newFrac()
	f.den, f.to = "1 + 2", 127.6
	f.want(t, "12/(1 + 2)")
}

// TestFractionTrimsTheSpaceBeforeTheSolidus pins both halves of one decision.
//
// A space at the numerator's right edge is the horizontal gap to the bar, which the solidus
// now occupies, so it is dropped — page 383 draws one and keeping it emits "(x )/2" where
// the identical formula four lines down emits "(x)/2". And because the space is at the edge
// rather than inside, it does not make the level compound: no parentheses.
func TestFractionTrimsTheSpaceBeforeTheSolidus(t *testing.T) {
	f := newFrac()
	f.num = "12 "
	f.want(t, "12/116")
}

// TestFractionNotJoinedWhenTextFollowsTheNumerator pins the side test on the numerator.
//
// The solidus is written into the numerator's last gathered fragment, so anything the bar
// does not span sitting to its right would end up on the wrong side of it.
func TestFractionNotJoinedWhenTextFollowsTheNumerator(t *testing.T) {
	f := newFrac()
	f.numTail, f.tailX = "tail", 125
	f.want(t, "12 tail 116")
}

func TestFractionNotJoinedWhenTextPrecedesTheDenominator(t *testing.T) {
	f := newFrac()
	f.numX, f.denX = 130, 130
	f.from, f.to = 129.7, 150.3
	f.denHead, f.headX = "pre", 100
	// No space before "116": the space between two fragments is carried by the one that
	// follows it, and "pre" was drawn after "116" so nothing was measured across that gap.
	// It is the missing solidus this pins, not the spacing.
	f.want(t, "12 pre116")
}

// TestFractionNotJoinedWhenTheBarIsAboveTheNumerator pins that a bar above both baselines
// divides nothing, or a heading's underline would divide the heading by the line under it.
//
// Balance is what holds it, not the position check joinFraction opens with: a mark above the
// numerator is a negative share of the gap, which is outside the band. Deleting that check
// leaves this test passing — it is a fast path, and this test does not cover it.
func TestFractionNotJoinedWhenTheBarIsAboveTheNumerator(t *testing.T) {
	f := newFrac()
	f.y = 701
	f.want(t, unjoined)
}

// TestFractionDeletesOnlyTheSpaceAtTheSolidus asserts the whole rewritten string, which is the
// only assertion that can say what the rewrite removed.
//
// It was named for character conservation, and that name was a claim the rewrite does not
// keep: writeSolidus deletes the space a producer drew at the numerator's right edge, which
// TestFractionTrimsTheSpaceBeforeTheSolidus pins as deliberate. Nothing else is deleted, and
// nothing in cmd/pdfspec can see the difference — its conservation tests count alphanumerics,
// so a dropped space is invisible and so are the inserted parentheses and solidus — so the
// whole-string form here is where "only that one space" is stated.
func TestFractionDeletesOnlyTheSpaceAtTheSolidus(t *testing.T) {
	f := newFrac()
	f.num, f.to = "L + 16", 134.0
	if got, want := f.text(t), "(L + 16)/116"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

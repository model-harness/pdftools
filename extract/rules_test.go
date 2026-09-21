package extract

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// rulesOf renders a page's rules as "V x from-to" / "H y from-to", sorted the way the
// content stream emitted them, so an assertion reads as the strokes it describes.
func rulesOf(p doc.Page) string {
	var out []string
	for _, r := range p.Rules {
		k := "H"
		if r.Vertical {
			k = "V"
		}
		out = append(out, fmt.Sprintf("%s %.0f %.0f-%.0f", k, r.Pos, r.From, r.To))
	}
	return strings.Join(out, " ")
}

// spansOf renders a page's spans as their text, one entry per span, which is what the
// cell split has to be asserted against — the text alone would be identical either way.
func spansOf(p doc.Page) []string {
	var out []string
	for _, b := range p.Blocks {
		for _, sp := range b.Spans {
			out = append(out, sp.Text)
		}
	}
	return out
}

// TestRuleFromLineTo is the base case: an m/l pair painted by S is one rule.
func TestRuleFromLineTo(t *testing.T) {
	p := extractPage(t, "100 700 m 100 600 l S 100 600 m 300 600 l S")
	if got, want := rulesOf(p), "V 100 600-700 H 600 100-300"; got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestRuleFromRectangle pins that re contributes all four edges.
//
// A producer that draws a table's frame as one rectangle states four boundaries in one
// operator, and a reader that only handles m/l finds no frame at all. This is the common
// case, not the exotic one — it is how every corpus document draws a box.
func TestRuleFromRectangle(t *testing.T) {
	p := extractPage(t, "100 600 200 100 re S")
	// Bottom, right, top, left, in the order re's corners are walked.
	const want = "H 600 100-300 V 300 600-700 H 700 100-300 V 100 600-700"
	if got := rulesOf(p); got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestRuleFromFilledRectangle pins that a filled rectangle counts.
//
// A hairline rule is usually drawn as a thin filled rectangle rather than a stroked line,
// because a fill has no line width to interact with the device resolution. Handling only
// S would miss most real rules — and would have missed every one in
// testdata/reference/table.pdf, which LaTeX draws as fills.
func TestRuleFromFilledRectangle(t *testing.T) {
	p := extractPage(t, "100 600 200 0.4 re f")
	const want = "H 600 100-300 V 300 600-600 H 600 100-300 V 100 600-600"
	if got := rulesOf(p); got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestClipPathIsNotARule pins that "W n" contributes nothing.
//
// A clip is not ink. Every page that clips an image to its frame establishes the clip with
// a rectangle and discards it with n, so treating the no-paint operator as a paint would
// put a table boundary around every figure on disk.
func TestClipPathIsNotARule(t *testing.T) {
	p := extractPage(t, "100 600 200 100 re W n")
	if got := rulesOf(p); got != "" {
		t.Errorf("rules = %q, want none from a clip path", got)
	}
}

// TestDiagonalIsNotARule pins that only axis-aligned segments survive.
//
// The corpus draws 421 non-axis-aligned segments and all of them are artwork. A table's
// edges are horizontal or vertical by construction, so a sloped line is evidence of a
// figure and admitting it would place a column boundary inside one.
func TestDiagonalIsNotARule(t *testing.T) {
	p := extractPage(t, "100 600 m 300 700 l S")
	if got := rulesOf(p); got != "" {
		t.Errorf("rules = %q, want none from a diagonal", got)
	}
}

// TestZeroLengthSegmentIsNotARule pins that a degenerate stroke marks a point, not an edge.
//
// Both deltas are zero, so it is neither horizontal nor vertical — which is what the
// exclusive test in paintPath makes true. Classifying it either way would give a rule whose
// extent overlaps nothing and whose position divides everything at that coordinate.
func TestZeroLengthSegmentIsNotARule(t *testing.T) {
	p := extractPage(t, "100 600 m 100 600 l S")
	if got := rulesOf(p); got != "" {
		t.Errorf("rules = %q, want none from a zero-length segment", got)
	}
}

// TestCurveIsNotARule pins that a Bézier contributes no segment but still moves the
// current point.
//
// The second line starts where the curve ended, so if c did not move the current point the
// rule would run from the curve's start instead — a rule at the right x with the wrong
// extent, which is worse than none because the extent test is what confines a split.
func TestCurveIsNotARule(t *testing.T) {
	p := extractPage(t, "100 700 m 150 750 200 750 100 600 c 100 500 l S")
	if got, want := rulesOf(p), "V 100 500-600"; got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestClosePathClosesToSubpathStart pins that h closes to the current subpath's start
// rather than to the path's first point.
//
// The path has two subpaths, and the second's h must return to 200 600 — where m last
// began — not to 100 600, where the path started. The second subpath is a three-sided box
// whose closing edge is the vertical at x=200; closing to the path's first point instead
// gives a diagonal, which paintPath drops, so the failure is a silently missing edge.
func TestClosePathClosesToSubpathStart(t *testing.T) {
	p := extractPage(t, "100 600 m 100 700 l 200 600 m 300 600 l 300 700 l 200 700 l h S")
	const want = "V 100 600-700 H 600 200-300 V 300 600-700 H 700 200-300 V 200 600-700"
	if got := rulesOf(p); got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestCloseAndStrokeClosesFirst pins that s closes the subpath before painting.
//
// s is "h S" in one operator, so the closing edge is part of what it paints. A reader that
// treated it as a bare S would lose the fourth side of every triangle-and-close frame.
func TestCloseAndStrokeClosesFirst(t *testing.T) {
	p := extractPage(t, "100 600 m 100 700 l 300 700 l 300 600 l s")
	const want = "V 100 600-700 H 700 100-300 V 300 600-700 H 600 100-300"
	if got := rulesOf(p); got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestRuleTransformedByCTM pins that points pass through the CTM.
//
// The rule is drawn at x=50 under a matrix that translates by 100, so it is at 150 in page
// space — which is the only space the text boxes it will be compared against are in. An
// untransformed rule lands wherever the producer's local frame happened to be, and every
// crossing test against it is then meaningless.
func TestRuleTransformedByCTM(t *testing.T) {
	p := extractPage(t, "q 1 0 0 1 100 0 cm 50 600 m 50 700 l S Q")
	if got, want := rulesOf(p), "V 150 600-700"; got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestPathClearedAtPaint pins that a painted path does not contribute again.
//
// Without the clear, the second S would re-emit the first line and every subsequent paint
// would repeat everything before it — quadratic growth on a page of artwork, and a
// duplicate rule at every position.
func TestPathClearedAtPaint(t *testing.T) {
	p := extractPage(t, "100 600 m 100 700 l S 200 600 m 200 700 l S")
	if got, want := rulesOf(p), "V 100 600-700 V 200 600-700"; got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// widthsOf renders each rule's recorded thickness, which rulesOf deliberately omits: a
// rule's position and its thickness are separate claims and an assertion on both at once
// cannot say which one moved.
func widthsOf(p doc.Page) string {
	var out []string
	for _, r := range p.Rules {
		out = append(out, fmt.Sprintf("%.3f", r.Width))
	}
	return strings.Join(out, " ")
}

// TestStrokeRecordsItsWidth pins that a stroked rule carries the width it was stroked with.
//
// This is the measurement a fill cannot supply. A stroke is reported once and has no area,
// so nothing in its own coordinates says how thick it is — and pdfTeX draws a fraction bar
// this way, which is why extract/fraction.go reads the field at all.
func TestStrokeRecordsItsWidth(t *testing.T) {
	p := extractPage(t, "0.398 w 100 600 m 300 600 l S")
	if got, want := widthsOf(p), "0.398"; got != want {
		t.Errorf("widths = %q, want %q", got, want)
	}
}

// TestStrokeWidthDefaultsToOne pins the initial width rather than zero.
//
// A producer that strokes without setting w gets a 1-unit line (§8.4.3.2). Reporting zero
// would make it indistinguishable from a fill edge, which is the one distinction the field
// exists to carry.
func TestStrokeWidthDefaultsToOne(t *testing.T) {
	p := extractPage(t, "100 600 m 300 600 l S")
	if got, want := widthsOf(p), "1.000"; got != want {
		t.Errorf("widths = %q, want %q", got, want)
	}
}

// TestFillRecordsNoWidth pins that a fill reports zero however wide the line width is.
//
// A fill does not consult w, so carrying it here would report a fraction bar's thickness as
// whatever the last stroke happened to set. The bar's real thickness is the distance between
// the two edges the fill emits, which is the consumer's measurement to make.
func TestFillRecordsNoWidth(t *testing.T) {
	p := extractPage(t, "3 w 100 600 200 0.6 re f")
	if got, want := widthsOf(p), "0.000 0.000 0.000 0.000"; got != want {
		t.Errorf("widths = %q, want %q", got, want)
	}
}

// TestStrokeWidthScaledByCTM pins that the width is in page units, like every coordinate
// beside it.
//
// w is in unscaled user space, so a producer drawing inside a scaled frame states a number
// that is not the thickness on the page. A consumer comparing the width against a threshold
// in points needs the scaled one, and mixing the two is how a hairline inside a 10x frame
// reads as a border.
func TestStrokeWidthScaledByCTM(t *testing.T) {
	p := extractPage(t, "q 2 0 0 3 0 0 cm 0.5 w 50 200 m 150 200 l S Q")
	// Horizontal rule, so its thickness is across the y axis and takes the y factor.
	if got, want := widthsOf(p), "1.500"; got != want {
		t.Errorf("widths = %q, want %q", got, want)
	}
	// And the position still passed through the same matrix, so the two cannot have been
	// read from different states.
	if got, want := rulesOf(p), "H 600 100-300"; got != want {
		t.Errorf("rules = %q, want %q", got, want)
	}
}

// TestVerticalStrokeWidthTakesTheOtherFactor pins that thickness is perpendicular to the
// rule's own axis, which is the whole reason strokeWidth returns two numbers.
func TestVerticalStrokeWidthTakesTheOtherFactor(t *testing.T) {
	p := extractPage(t, "q 2 0 0 3 0 0 cm 0.5 w 50 100 m 50 200 l S Q")
	if got, want := widthsOf(p), "1.000"; got != want {
		t.Errorf("widths = %q, want %q — a vertical rule's thickness is scaled by x, not y", got, want)
	}
}

// TestStrokeWidthUnderAQuarterTurn is what the two tests above cannot see: both use a diagonal
// matrix, where the user axes and the page axes coincide and either indexing of the two scale
// factors gives the same answer.
//
// Here they do not. "0 2 3 0 0 0 cm" sends a user-horizontal line down the page — the rule
// arrives vertical at x=300 — and its thickness is across user y, scaled by 3 rather than by
// the 2 the user-x column carries. Reading the factors by user axis when paintPath classifies
// by page axis reported 1.000 for a stroke that is 1.500 wide on the page.
func TestStrokeWidthUnderAQuarterTurn(t *testing.T) {
	p := extractPage(t, "q 0 2 3 0 0 0 cm 0.5 w 50 100 m 150 100 l S Q")
	if got, want := rulesOf(p), "V 300 100-300"; got != want {
		t.Fatalf("rules = %q, want %q — the turn has to reach the geometry too", got, want)
	}
	if got, want := widthsOf(p), "1.500"; got != want {
		t.Errorf("widths = %q, want %q", got, want)
	}
}

// cellStream draws a two-cell row with a vertical rule in the gap, and shows the two cells
// as one Tj at a single size — which is how a producer emits a table row, and why the cells
// arrive as one fragment with an inferred space between them.
//
// Td is relative to the current line's origin, so the second cell is placed with a 100pt
// offset: "Left" at 12pt Helvetica advances 20.016pt from x=100, leaving an unexplained gap
// of about 80pt that place() infers a space in. The rule at 190 sits inside it.
func cellStream(ruleX string) string {
	return "1 0 0 RG " + ruleX + " 600 m " + ruleX + " 620 l S\n" +
		"BT /F1 12 Tf 100 605 Td (Left) Tj 100 0 Td (Right) Tj ET"
}

// TestSplitAtRuleDividesTheFragment is the point of the whole subsystem: two cells shown as
// one run become two spans, because a rule runs through the gap.
//
// The text is identical either way — this is why the assertion is on the span list. A
// reader that gets the characters right and the boundaries wrong has lost the table without
// losing a word, and no character-conservation check can see it.
func TestSplitAtRuleDividesTheFragment(t *testing.T) {
	p := extractPage(t, cellStream("190"))
	got := spansOf(p)
	want := []string{"Left ", "Right"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("spans = %q, want %q", got, want)
	}
}

// TestSplitPiecesSortIntoPositionOrder pins that the pieces of a split fragment are ordered
// against the rest of the line and not against their parent's position.
//
// splitFrag leaves a fragment's pieces in that fragment's own slot, so sorting the line
// before the split leaves the pieces wherever the parent sat: "Left" at 100 and "Right" at
// 200 are one show operation, and a Courier "mid" at 150 drawn after them stays behind both
// pieces, emitting "Left Rightmid". The sort has to run on the pieces, which means after the
// split — the whole reason a line is sorted at all is that a producer may draw it out of
// order, and this is that case arriving through the split rather than through the stream.
func TestSplitPiecesSortIntoPositionOrder(t *testing.T) {
	p := extractPage(t, "1 0 0 RG 190 600 m 190 620 l S\n"+
		"BT /F1 12 Tf 100 605 Td (Left) Tj 100 0 Td (Right) Tj ET\n"+
		"BT /F2 12 Tf 1 0 0 1 150 605 Tm (mid) Tj ET")
	got := spansOf(p)
	want := []string{"Left ", "mid", "Right"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("spans = %q, want %q", got, want)
	}
}

// TestNoSplitWithoutARule is the negative half: the same gap with no rule in it stays one
// span.
//
// The gap here is about 72pt against a 12pt font — six space widths, which is where any
// threshold would fire. That it does not split is the measurement this package rests on:
// the ratio of gap to space width is continuous over all 117499 inferred spaces on disk, so
// only the stroke separates a cell boundary from wide spacing.
func TestNoSplitWithoutARule(t *testing.T) {
	p := extractPage(t, "BT /F1 12 Tf 100 605 Td (Left) Tj 100 0 Td (Right) Tj ET")
	got := spansOf(p)
	want := []string{"Left Right"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("spans = %q, want %q", got, want)
	}
}

// TestNoSplitFromARuleOutsideTheGap pins the x half of the crossing test.
//
// The rule is at 400, past both cells, so it divides nothing. A rule in the margin is the
// common case of this — a page frame, a change bar — and without the test every line on the
// page would split at its widest gap.
func TestNoSplitFromARuleOutsideTheGap(t *testing.T) {
	p := extractPage(t, cellStream("400"))
	if got := spansOf(p); strings.Join(got, "|") != "Left Right" {
		t.Errorf("spans = %q, want the fragment intact", got)
	}
}

// TestNoSplitFromARuleAtAGlyphEdge pins that the x test is strictly inside the gap.
//
// A rule flush with a glyph's edge is the table's outer border, which encloses text rather
// than dividing it, so admitting it would split a cell's first or last character off from
// the rest — the row would keep every character and gain a phantom column.
//
// Both edges, because the two comparisons fail independently: one bounds the search that
// finds the first candidate rule, the other stops the loop past the gap.
//
// Courier rather than the Helvetica the other tests use, because the boundary has to be
// named exactly and Courier's advances are uniform: every glyph is 600/1000 of the size, so
// "Left" at 10pt from x=100 ends at exactly 124 and the gap is the open interval (124, 200).
// Helvetica's proportional widths put the pen at 120.016, which is not representable, so a
// literal at the boundary lands on whichever side the float error falls.
func TestNoSplitFromARuleAtAGlyphEdge(t *testing.T) {
	for _, x := range []string{"124", "200"} {
		p := extractPage(t, "1 0 0 RG "+x+" 600 m "+x+" 620 l S\n"+
			"BT /F2 10 Tf 100 605 Td (Left) Tj 100 0 Td (Right) Tj ET")
		if got := spansOf(p); strings.Join(got, "|") != "Left Right" {
			t.Errorf("rule at x=%s: spans = %q, want the fragment intact", x, got)
		}
	}
}

// TestNoSplitFromARuleBelowTheLine pins the extent half.
//
// Same x, moved to y 400–420, which is nowhere near the text. Over ISO 32000-2, 12167 of
// 37631 inferred spaces have some vertical rule at their x — the table's own column rules,
// seen from the prose above and below — so the extent test is what confines a split to the
// text the rule encloses. Without it two pages in three would split their paragraphs.
func TestNoSplitFromARuleBelowTheLine(t *testing.T) {
	p := extractPage(t, "1 0 0 RG 190 400 m 190 420 l S\n"+
		"BT /F1 12 Tf 100 605 Td (Left) Tj 100 0 Td (Right) Tj ET")
	if got := spansOf(p); strings.Join(got, "|") != "Left Right" {
		t.Errorf("spans = %q, want the fragment intact", got)
	}
}

// TestNoSplitOnRotatedText pins that only horizontal text is divided, which is the one
// place this subsystem declines to act on evidence it has.
//
// A gap is measured along the baseline and a rule's position is measured across the page,
// and for horizontal text those are the same axis — which is the whole reason the two can
// be compared at all. Rotated text breaks that coincidence: the comparison still
// type-checks and still yields a boolean, so without the guard a rule is matched against
// a coordinate in a different frame and the cut lands wherever the arithmetic happens to
// agree. Projecting the rules into the text's own frame is the real fix, and nothing on
// disk needs it — 0 of the corpus's 421 non-axis-aligned segments are anywhere near
// rotated text.
//
// The rule's coordinates are what makes this test rather than a mutation the only way to
// see the defect. The text is set upward by a 90-degree Tm from (100, 100), so its along
// axis is page y — the two cells sit at along 100 and 200 with the gap between 120.016 and
// 200 — and its cross axis is negated page x, putting the line's band at -100. A vertical
// rule at x=150 spanning y -200 to 0 therefore satisfies both halves of crossedBy in the
// projected frame while being nowhere near the text on the page. That is exactly the
// mismatch the guard exists for, and it is why the rule is drawn off the page: on-page
// coordinates cannot express it.
func TestNoSplitOnRotatedText(t *testing.T) {
	p := extractPage(t, "1 0 0 RG 150 -200 m 150 0 l S\n"+
		"BT /F1 12 Tf 0 1 -1 0 100 100 Tm (Left) Tj 100 0 Td (Right) Tj ET")
	if got := spansOf(p); strings.Join(got, "|") != "Left Right" {
		t.Errorf("spans = %q, want the fragment intact", got)
	}
}

// TestSplitKeepsEveryCharacter pins conservation across the split, which is the invariant
// the whole repo's accounting rests on.
//
// Asserted by concatenation rather than by count, because a split that duplicates a
// character and drops another keeps the count exactly right.
func TestSplitKeepsEveryCharacter(t *testing.T) {
	p := extractPage(t, cellStream("190"))
	if got, want := strings.Join(spansOf(p), ""), "Left Right"; got != want {
		t.Errorf("concatenated spans = %q, want %q", got, want)
	}
}

// TestSplitPiecesDoNotShareStorage pins that a piece's text is copied rather than
// subsliced.
//
// A subslice aliases the original's backing array, and appendText appends to a fragment's
// text — so two pieces of one split would write through each other. The row here has three
// cells, which is what makes the aliasing observable: the middle piece's append would
// overwrite the last one's first byte.
func TestSplitPiecesDoNotShareStorage(t *testing.T) {
	p := extractPage(t, "1 0 0 RG 190 600 m 190 620 l S 290 600 m 290 620 l S\n"+
		"BT /F1 12 Tf 100 605 Td (One) Tj 100 0 Td (Two) Tj 100 0 Td (Six) Tj ET")
	got := spansOf(p)
	want := []string{"One ", "Two ", "Six"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("spans = %q, want %q", got, want)
	}
}

// TestSplitMarksEveryPieceApart pins the fix for the row-boundary defect, which is the
// subtlest thing in this file.
//
// Two rows in one text object at one style with no marked content, each divided into two
// cells. appendLine merges a fragment into the previous span when the style and MCID match,
// and a table's rows match on both — so unless every piece is marked apart, including the
// first, row 1's opening cell fuses into row 0's closing cell. Before the fix
// testdata/reference/table.pdf's nine cells arrived as seven spans with "Header C Cell A1"
// run together, which reads as a plausible sentence and loses the grid.
//
// The 14pt leading is load-bearing: at 20pt the paragraph rule splits the rows into
// separate blocks and appendLine never considers merging them, so the test would pass
// without the fix. That pitch is also what a real table sets — reference/table.pdf's rows
// are 12.35pt apart.
func TestSplitMarksEveryPieceApart(t *testing.T) {
	p := extractPage(t, "1 0 0 RG 190 598 m 190 616 l S 190 584 m 190 602 l S\n"+
		"BT /F1 12 Tf 100 605 Td (One) Tj 100 0 Td (Two) Tj -100 -14 Td (Six) Tj 100 0 Td (Ten) Tj ET")
	got := spansOf(p)
	// "Two " keeps the trailing space the line wrap contributes; the assertion is that
	// there are four spans and not three.
	want := []string{"One ", "Two ", "Six ", "Ten"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("spans = %q, want %q — a fused pair means a row boundary was lost", got, want)
	}
}

// Two cuts can now share a byte offset, and splitFrag has to absorb it.
//
// Suppressing the inferred space where the page already drew one made the state reachable:
// a cut records off = len(c.text), so two consecutive gaps that write no character record
// the same offset. The guard that handles it — `c.off <= prev` — was written for a
// different reason and its comment names dropped characters as the outcome it prevents, so
// the arithmetic is pinned here rather than left as a reading of that comment.
//
// Constructed rather than extracted, because no file on disk reaches it: over all 10102
// cuts recorded across the 51 PDFs on disk, 0 are duplicated, 0 sit at offset 0, and 0 are
// past the end of their fragment. A guard whose population is zero is exactly the kind that
// rots, and the empty piece it would otherwise emit is a fragment with no text that
// appendLine would carry into the span list.
//
// These two tests are the guard's only coverage, which is measured rather than assumed:
// relaxing <= to < leaves the whole extract package passing when they are skipped, and both
// of them fail — one on each half of the comparison.
func TestSplitAbsorbsADuplicateCutOffset(t *testing.T) {
	f := frag{
		text: []byte("AB"), along0: 0, along1: 100, height: 10,
		cuts: []cut{{off: 1, x0: 10, x1: 20}, {off: 1, x0: 30, x1: 40}},
	}
	out := splitFrag(&f, []doc.Rule{
		{Vertical: true, Pos: 15, From: 0, To: 20},
		{Vertical: true, Pos: 35, From: 0, To: 20},
	}, 5)
	var got []string
	for i := range out {
		got = append(got, string(out[i].text))
	}
	if want := []string{"A", "B"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("pieces = %q, want %q: the second cut adds no boundary and no empty piece", got, want)
	}
}

// A cut at offset 0 divides nothing, and the fragment has to survive whole.
//
// Same guard, other half: `c.off <= prev` with prev starting at 0 rejects it, so out stays
// empty and splitFrag returns the original as one piece. Emitting the split would produce a
// leading fragment with no text at all.
func TestSplitAbsorbsACutAtOffsetZero(t *testing.T) {
	f := frag{text: []byte("AB"), along0: 0, along1: 100, height: 10,
		cuts: []cut{{off: 0, x0: 10, x1: 20}}}
	out := splitFrag(&f, []doc.Rule{{Vertical: true, Pos: 15, From: 0, To: 20}}, 5)
	if len(out) != 1 || string(out[0].text) != "AB" {
		t.Errorf("pieces = %d, first = %q, want one piece %q", len(out), string(out[0].text), "AB")
	}
}

// TestRulesCappedPerPage pins the bound, which is a denial-of-service guard rather than a
// correctness rule.
//
// ISO 32000-2's heaviest page draws about 320 rectangles, so four edges each puts the real
// ceiling near 1300. A stream emitting hairlines without limit is corrupt or hostile, and
// without the cap the slice grows until the process dies. Dropping the excess loses grid
// detail on a page already past anything table inference could use.
func TestRulesCappedPerPage(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxRules+100; i++ {
		fmt.Fprintf(&sb, "%d 600 m %d 700 l S ", i, i)
	}
	p := extractPage(t, sb.String())
	if len(p.Rules) != maxRules {
		t.Errorf("rules = %d, want the cap %d", len(p.Rules), maxRules)
	}
}

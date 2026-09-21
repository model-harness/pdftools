package extract

import (
	"bytes"
	"math"
	"sort"
	"unicode"

	"github.com/model-harness/pdftools/doc"
)

// A stacked fraction is two baselines with a rule between them, and on the page it means
// one quantity. Read as text it is two, separated by whatever the wrap inferred: ISO
// 32000-2 page 205 draws 𝐿∗ + 16 over 116 and this package emitted "𝐿∗ + 16 116", which is
// not a formula that is hard to read — it is a different formula, and a consumer has no way
// to tell it from a page that really said those two things in sequence.
//
// joinFractions rewrites the pair into the solidus form: "(𝐿∗ + 16)/116". ISO 80000-2
// defines that form and requires the parentheses when a level is a sum or a difference, so
// the convention is the standard's rather than this package's.
//
// The markup stops there. No \frac, no $…$: extract recovers text and geometry, and which
// notation a document should be rendered in belongs to a sink. A LaTeX sink can be layered
// on this later; a LaTeX extractor could not be unlayered.
//
// # What a fraction bar is, and what else looks like one
//
// A short horizontal mark with text above and below it is also a table's row rule, a
// heading's underline, a link's underline, and the top edge of a shaded cell. Over the 12
// documents on disk, 281405 (line pair, mark) combinations put a mark between two
// baselines — every horizontal rule the page paints, counted once against each pair of
// adjacent upright baselines it falls between. 73 of them are fractions — 71 in ISO
// 32000-2, on 27 of its 1023 pages, and 2 on page 12 of one arXiv paper — and nothing that
// is not a fraction is accepted anywhere in the corpus.
//
// Four measurements do the separating. What each one is worth was taken by turning it off
// with the other three left on, so the number is what that test alone rejects:
//
//	balance    the mark sits on the math axis, nearer the numerator than the
//	           denominator, in a narrow band                                     (+27)
//	thickness  the mark is one thin mark and not one line of a grid, which is what
//	           barMark decides                                                    (+8)
//	overhang   the mark reaches past the wider level by less than a quarter of the
//	           level's own type size                                              (+2)
//	side       nothing follows the numerator and nothing precedes the denominator  (+1)
//
// In order, and counting one mark once:
//
//	between baselines      281405
//	thin mark               47032
//	both levels gathered     7314
//	side                      281
//	overhang                  111
//	gap                       100
//	balance                    73
//
// What each test rejects, by name, because a threshold with no named counterexample is a
// guess:
//
//   - balance rejects 27, all of them underlines, which sit just under their text's
//     baseline rather than on the math axis: 6 "https://pdfa.org/sponsored-standards" links
//     on page 1 of the sponsored ISO documents at 0.0500–0.0689, 2 Creative Commons licence
//     links at 0.0625, and 19 Well-Tagged-PDF section-heading rules at 0.0807–0.0811. Every
//     fraction in the corpus falls in 0.2795–0.3561, so the two populations are three and a
//     half times apart at their nearest, 0.0811 against 0.2795.
//   - thickness rejects 8 table rules, each with a parallel rule of its own extent a row
//     away rather than a bar's thickness away, by two different mechanisms. Five are
//     declined for being one of three or more rules at one extent: 3 on ISO 32000-2 page
//     1010, where 32 rules share that extent, 1 on page 1023 where 24 do, and the arXiv
//     paper's booktabs bottom rule on page 11, one of 3. That last is the corpus's hardest
//     case — a table's final row over its caption, its partner 75.09pt away — and nothing
//     else sees it: it passes overhang at 0.1707 and balance at 0.3887, and its 0.797pt
//     stroke is well inside barThickMax. The other three are two on ISO 32000-2 page 1014
//     and one on page 1015, each the only other rule at its extent, 12.96pt away; those are
//     declined for having no thickness of their own, because a filled rule reports no stroke
//     width and a partner a row off is not the far edge of anything.
//   - overhang rejects 2, both prose paragraphs with a full-width rule between them: ISO
//     32000-2 page 620 at 3.1701 type sizes and Well-Tagged-PDF page 40 at 5.3725. Every
//     fraction falls in −0.0137–0.0763, so the 0.25 threshold stands at 3.3 times the widest
//     genuine overhang, and the nearest thing this test has to reject sits at 0.3694 — 4.8
//     times that same figure — which puts the threshold inside an empty band rather than on
//     either population's edge. Two things that are not fractions do land inside the band —
//     the booktabs rule at 0.1707 and an ISO/TS 32005 page 1 underline at 0.1999 — and each is
//     rejected by one of the other three tests, which is why the margin here is not the margin
//     for the whole rule.
//   - side rejects 1: ISO/TS 32003 page 10, where a stroked rule spans "(GCM)." above and a
//     line beginning with text the rule does not cover below. One and not more because place
//     continues a fragment across any distance on one baseline as long as the style, the MCID
//     and the artifact flag all match — so same-style text beside a level joins the level's
//     own fragment and is excluded along with it, which declines the candidate for having no
//     level rather than for having text on the wrong side. This test only ever sees a
//     neighbour across a style or marked-content change.
//
// Two conditions reject nothing over this corpus and are kept anyway, which is worth saying
// plainly rather than presenting them as filters:
//
//   - gap declines 11 candidates at the sixth row above, and balance declines all 11 too,
//     so turning it off changes no output. It bounds a quantity balance cannot see — balance
//     is a ratio and says nothing about absolute distance — and the candidates it declines
//     are two prose paragraphs with a rule between them, which is the most common false
//     positive shape the corpus has.
//   - contiguity declines nothing, because each level of a stacked fraction is already its
//     own whole line. Page 203 sets 𝑥R, 𝑦R, 1−𝑥R and its 𝑦R as four separate line records
//     holding nothing else: the numerator, the bar and the denominator sit at three
//     different vertical positions and run closes a line at each. It is a precondition of
//     the rewrite and not a filter — the solidus goes into the numerator's last gathered
//     fragment, which only reads correctly if the run is in one piece — and a line cut
//     differently, by another producer or by a change to closeLine, would need it.
//
// # What was measured and refuted
//
// Three hypotheses that sound better than the tests above and are false:
//
//   - The bar's width as a *ratio* of the wider level's does not separate, and was the test
//     here first, at 1.05. Page 203 sets Y_A, Y_B and Y_C as three copies of one formula,
//     and the ratio put 𝑦𝑅/𝑅 at 1.065 while 𝑦G/G and 𝑦B/B came in at 1.044 and 1.046 — a
//     threshold inside one population, splitting three identical lines two ways. The cause
//     is that a bar's overhang is an absolute amount, about a third of a point, so dividing
//     it by a level four points wide inflates the ratio and dividing it by one two hundred
//     points wide hides it. Denominating the same overhang in the level's type size is what
//     the overhang test does, and it separates with the margin quoted above.
//   - The marked-content identifier does not separate, though it looks like it should. The
//     test was whether the bar was painted inside one of the marked-content sequences
//     holding the levels it divides, and over all 12 documents it rejected nothing the four
//     tests above accept. It was kept while the overhang test was still a ratio, because it
//     rejected page 1023's table rule — painted under /MCID 20 between cells marked 15 and
//     21 — and it is the thickness test that rejects that rule now. Carrying the identifier
//     from the painting operator to here cost a wrapper around doc.Rule on every rule of
//     every page, so it is gone.
//   - The bar's *thickness* denominated in the type size does not separate, which is the
//     one worth stating because it is so nearly right. Genuine bars run 0.0399–0.0896 of
//     their level's glyph height: pages 205 and 206 set inline fractions in 8.04pt type with
//     a 0.72pt bar, and the arXiv paper sets 9.96pt type with a 0.398pt one. The booktabs
//     bottom rule named above measures 0.0800 — inside that range, near the middle of it.
//     Thickness is used only as an absolute bound, where its job is to tell one mark from a
//     grid rather than a small bar from a large one.
//
// # Recall comes from grouping, not from loosening
//
// Gathering every fragment the bar spans on each baseline, rather than the nearest one,
// raises the count from 31 to 73 with no threshold moved. A compound level is many
// fragments — "𝐿∗ + 16" is four, and the CIE chromaticity numerators on pages 203 and 204
// are eighteen — and a rule that picks one fragment per level finds only the simple cases
// it was written against.
const (
	// barSlack is how far outside the bar's extent a level's text may reach, and how far
	// two rules' ends may differ while still counting as the same extent. Half a point:
	// a bar is drawn a little wider than both levels, never narrower.
	barSlack = 0.5

	// gridRules is how many rules at one extent make a grid rather than a coincidence.
	//
	// Three, because that is the fewest a ruled table draws at one extent: a booktabs
	// table's top, mid and bottom rules span the same columns, and the corpus's hardest
	// false positive is one of them. Two at one extent is what two fractions of the same
	// width at the same indent look like, and it has to stay admissible.
	gridRules = 3

	// barThickMin and barThickMax bound a bar's thickness. The lower bound applies only to
	// a filled rectangle, where the thickness is the distance between two edges and a rule
	// must not pair with itself; a stroked line reports its own width and needs no floor.
	barThickMin = 0.1
	barThickMax = 1.5

	// barOverhangMax bounds how far the bar reaches past the wider level, as a multiple of
	// the taller level's glyph height. A bar is drawn for the expression it divides, so it
	// exceeds it by a typesetter's margin — 0.0763 type sizes at the most here, and −0.0137
	// at the least, since a level may reach a hair past its own bar; a rule drawn for a
	// column or a paragraph is not bounded by either level and overhangs by 3.17 and 5.37.
	//
	// Only the upper bound is needed. A bar much narrower than its level cannot reach this
	// test, because levelOf gathers only fragments that lie inside the bar's extent.
	barOverhangMax = 0.25

	// levelGapMax bounds the two baselines' separation as a multiple of the taller level's
	// glyph height. Two levels of one fraction are set a little more than one type size
	// apart — 1.0964–1.6304 here — and two lines of a paragraph with a rule between them
	// are further.
	levelGapMax = 1.8

	// balanceMin and balanceMax bound the bar's position between the two baselines, as the
	// numerator's share of the gap. A bar sits on the math axis, which is above the middle:
	// the corpus's fractions fall in 0.2795–0.3561, and the 27 things that reach this test
	// without being one are all underlines, at 0.0500–0.0811, sitting just under their own
	// baseline rather than between two.
	balanceMin = 0.25
	balanceMax = 0.40
)

// joinFractions rewrites every stacked fraction on the page into its solidus form.
//
// Only adjacent lines are considered, and at most one fraction is taken from a pair. Both
// follow from each level being its own line: a fraction's denominator is the very next line
// every time, and a line pair that is a fraction's two levels holds nothing else to be a
// second one.
//
// A line is one level of one fraction, which is why a join advances past the denominator.
// Three baselines with a bar between each pair — a fraction whose own numerator is a fraction
// — would otherwise chain into "12/34/56", and ISO 80000-2 does not allow a repeated solidus
// on one line without parentheses, so that form asserts something the standard this rewrite
// cites refuses to read. The second bar is left undivided instead: "12/34 56" says less than
// the page does, where the chain would say something else. Nothing on disk sets one — page
// 177's nested case is inline, where the levels are not adjacent lines at all.
//
// A pair that straddles the artifact boundary is skipped. The two levels of one expression are
// one piece of content, so a page that marked only one of them as an artifact is not describing
// a fraction this rewrite can read: joining them writes a solidus into a line that the
// assembly loop then drops, and "12/" is a division with no divisor.
func (r *run) joinFractions() {
	bars := r.bars()
	if len(bars) == 0 {
		return
	}
	for i := 0; i+1 < len(r.lines); i++ {
		num, den := &r.lines[i], &r.lines[i+1]
		if num.orient != 0 || den.orient != 0 || lineIsArtifact(num) != lineIsArtifact(den) {
			continue
		}
		for _, b := range bars {
			if joinFraction(num, den, b) {
				i++
				break
			}
		}
	}
}

// bars returns the page's fraction-bar candidates: one per thin horizontal mark, at the
// mark's centre, ordered along the page.
//
// Quadratic in the page's rules, and bounded only by maxRules: every horizontal rule asks
// extentRules about every rule at its extent. That is the same argument layout.Tables makes
// for its own quadratic — the cap is what makes it safe, not the shape of the input — and at
// the 4096-rule cap the worst page on disk measures well under a second.
//
// One per mark and not one per rule, because a filled rectangle arrives as both of its
// long edges. Testing each edge separately accepts the same fraction twice — the two edges'
// balance ratios differ by the thickness over the level gap, which the measured bands put
// between 0.02 and 0.09, inside the 0.15-wide band either edge has to fall in — and the
// second rewrite would write a second solidus into text the first had already rewritten.
//
// The centre rather than either edge, so the balance ratio does not depend on which edge a
// producer happened to emit first.
func (r *run) bars() []doc.Rule {
	var hs []doc.Rule
	for _, ru := range r.rules {
		if !ru.Vertical && ru.To > ru.From {
			hs = append(hs, ru)
		}
	}
	var out []doc.Rule
	for _, b := range hs {
		if c, ok := barMark(hs, b); ok {
			out = append(out, c)
		}
	}
	// Sorted on both coordinates so the order is total, and then deduplicated, because a
	// producer that emits one path twice reaches barMark twice and gets the same mark back
	// both times. One per mark is this function's contract and the thing that keeps a second
	// solidus out of text the first rewrite already changed; joinFractions' break happens to
	// stop it today, which is a guarantee in the wrong function.
	sort.Slice(out, func(a, b int) bool {
		if out[a].From != out[b].From {
			return out[a].From < out[b].From
		}
		return out[a].Pos < out[b].Pos
	})
	return dedupeMarks(out)
}

// dedupeMarks drops marks that repeat the one before them, which a sorted slice puts
// adjacent. Coordinates compared exactly: these are copies of one rule, not two rules that
// nearly agree, and the tolerant question is the one barSlack already answered.
func dedupeMarks(in []doc.Rule) []doc.Rule {
	out := in[:0]
	for _, m := range in {
		// Against the last mark kept, not against in[i-1]: out aliases in, so a dropped
		// mark shifts the ones after it and in[i-1] is not reliably the previous element
		// by the time it is read.
		if n := len(out); n > 0 && m.Pos == out[n-1].Pos && m.From == out[n-1].From && m.To == out[n-1].To {
			continue
		}
		out = append(out, m)
	}
	return out
}

// barMark reports whether a rule is one thin horizontal mark, returning it centred on
// that mark's thickness.
//
// Two measurements answer it — how far away the nearest rule of the same extent is, and
// how many rules share that extent — because the same mark is painted two ways and a
// table's rules are a third thing:
//
//	within barThickMax   the other long edge of a filled rectangle. The pair is one
//	                     mark, its separation is the thickness, and it is reported once.
//	further off, 3+      the next line of a grid. A table draws its rules at one extent
//	                     and several positions, so a rule that is one of three or more at
//	                     one extent is a table's, however it was painted.
//	further off, 2       two unrelated marks that happen to line up. Each is judged on
//	                     its own stroke width, as if the other were not there.
//	none at all          a lone stroked line, which has no second edge to find. Its
//	                     thickness is the width it was stroked with, and nothing else.
//
// The last case is the whole reason Width exists. ISO 32000-2 fills its fraction bars and
// pdfTeX strokes them, so reading only the pair would find every fraction in one document
// and none in the other.
//
// All four read the *nearest* rule at the extent, which assumes at most two rules lie within
// barThickMax of each other there — one bar, or one bar's two edges. A third inside that
// window is judged wrong in both directions, and neither has an instance on disk: a hairline
// drawn over one edge of a filled bar puts every edge's nearest neighbour inside barThickMin,
// so all of them are rejected and a real bar is lost; a thin rule a third of a point above the
// upper edge pairs with it and reports a mark between the two edges of nothing. Pairing the
// positions at an extent by walking them in order would answer it, and it is not written
// because the corpus has no case to measure it against — recorded here so the next producer
// that shadows its rules is diagnosed rather than re-derived.
//
// The count is what separates the third case from the second, and it was wrong before it
// was measured: with any far partner enough to condemn a rule, two fraction bars of the
// same width at the same indent cancelled each other. That is not exotic — a page setting
// the same formula twice draws it — and testdata/reference/fractions.pdf is that page.
// Three is the floor because a table's rules come in threes at their least: a booktabs
// table's top, mid and bottom rules share an extent, which is what still rejects the
// hardest false positive the corpus has. Admitting the two-rule case adds 452 marks over
// the 12 documents on disk and not one fraction, because overhang and balance decline
// every one of them.
func barMark(hs []doc.Rule, b doc.Rule) (doc.Rule, bool) {
	twin, ok, atExtent := extentRules(hs, b)
	if !ok {
		return b, b.Width > 0 && b.Width <= barThickMax
	}
	d := twin.Pos - b.Pos
	if math.Abs(d) <= barThickMin {
		// One line drawn twice, or a rectangle with no area. Pairing these reports a mark
		// of no thickness, and taking each on its own would report the same mark twice.
		return doc.Rule{}, false
	}
	if math.Abs(d) > barThickMax {
		if atExtent >= gridRules {
			return doc.Rule{}, false
		}
		return b, b.Width > 0 && b.Width <= barThickMax
	}
	if d < 0 {
		// A pair is reported by its lower edge, so the one whose twin is below it has
		// already been counted. Either edge would do; picking one is what makes the mark
		// arrive once.
		return doc.Rule{}, false
	}
	b.Pos += 0.5 * d
	return b, true
}

// extentRules reads both of barMark's measurements in one pass over the page's rules: the
// rule nearest b that begins and ends where b does, and how many *positions* at that extent
// carry a rule.
//
// One pass because they are one scan of one predicate, and asking twice is how the two
// answers came to disagree. The nearest-rule half skips a rule at exactly b's position — an
// exact duplicate is not b's other edge — so a count of rules would count a bar a producer
// emitted twice as two, and two duplicated bars at one extent as four: a grid, by a count
// that has seen two bars. That is testdata/reference/fractions.pdf's own geometry with the
// rules doubled, and it would lose both fractions.
//
// Distance is unbounded on purpose: how far the nearest rule is, is the answer barMark reads,
// so bounding it here would collapse "too far" and "not there" into one result. The count
// stops at gridRules, since no caller asks how much more than a grid a grid is.
func extentRules(hs []doc.Rule, b doc.Rule) (twin doc.Rule, found bool, positions int) {
	var at [gridRules]float64
	at[0], positions = b.Pos, 1
	near := math.Inf(1)
	for _, o := range hs {
		if math.Abs(o.From-b.From) > barSlack || math.Abs(o.To-b.To) > barSlack {
			continue
		}
		if o.Pos != b.Pos {
			if d := math.Abs(o.Pos - b.Pos); d < near {
				near, twin, found = d, o, true
			}
		}
		if positions < gridRules && !hasPos(at[:positions], o.Pos) {
			at[positions] = o.Pos
			positions++
		}
	}
	return twin, found, positions
}

// hasPos reports whether p is already among the positions counted.
func hasPos(at []float64, p float64) bool {
	for _, q := range at {
		if q == p {
			return true
		}
	}
	return false
}

// level is one side of a candidate fraction: the run of fragments on one baseline that
// the bar spans.
type level struct {
	// first and last index the run's fragments in the line. Both ends are needed because
	// the parentheses go on the outside of the run and the solidus after it.
	first, last int

	// from and to bound the run on the along axis, and height is its tallest glyph.
	from, to, height float64

	// before and after report a non-blank fragment on the same line outside the run. The
	// numerator must have nothing after it and the denominator nothing before it, or the
	// solidus lands in the middle of text that is not part of the fraction.
	before, after bool

	// spaced reports that the run's text holds an interior space, which is where ISO
	// 80000-2 asks for parentheses around the level.
	spaced bool
}

// joinFraction rewrites one stacked fraction in place, reporting whether it did.
func joinFraction(num, den *line, b doc.Rule) bool {
	// Above the denominator and below the numerator. Checked before anything is gathered
	// because it is the cheapest of the tests and rejects most of the page's rules.
	//
	// Not a filter, and nothing should be credited to it: balance declines the same
	// candidates. A mark above the numerator gives a negative share of the gap and one below
	// the denominator a share above 1, so both fall outside the band, and deleting this line
	// changes no output over the corpus or the tests. What it does do is keep gap strictly
	// positive — two levels on one baseline are rejected here, whichever side the mark is on,
	// so the division below cannot be by zero.
	if b.Pos >= num.cross || b.Pos <= den.cross {
		return false
	}
	n, ok := levelOf(num, b)
	if !ok || n.after {
		return false
	}
	d, ok := levelOf(den, b)
	if !ok || d.before {
		return false
	}

	wmax := maxf(n.to-n.from, d.to-d.from)
	hmax := maxf(n.height, d.height)
	// The two bounds are a check on the producer's numbers, not on this package's: a glyph with
	// no advance and a font with no size are both legal in a content stream, and two levels of
	// no width or no height have no shape for the overhang below to be a fraction of. Neither
	// guards a division — balance divides by gap, which the position check above keeps positive
	// — and neither has an instance on disk, which is why they are stated as a boundary and not
	// credited with rejecting anything.
	if wmax <= 0 || hmax <= 0 {
		return false
	}
	if (b.To-b.From)-wmax > barOverhangMax*hmax {
		return false
	}
	gap := num.cross - den.cross
	if gap > levelGapMax*hmax {
		return false
	}
	if bal := (num.cross - b.Pos) / gap; bal < balanceMin || bal > balanceMax {
		return false
	}

	writeSolidus(num, den, n, d)
	return true
}

// levelOf gathers the fragments on ln that the bar spans.
//
// Returns false when the run is empty or when a fragment the bar does not span falls
// between two it does. A run in two pieces has no single place for the solidus.
func levelOf(ln *line, b doc.Rule) (level, bool) {
	lv := level{first: -1, last: -1}
	var text []byte
	for i := range ln.frags {
		f := &ln.frags[i]
		if blank(f.text) {
			continue
		}
		if f.along0 < b.From-barSlack || f.along1 > b.To+barSlack {
			if lv.last < 0 {
				lv.before = true
			} else {
				lv.after = true
			}
			continue
		}
		if lv.after {
			return level{}, false
		}
		if lv.first < 0 {
			lv.first, lv.from = i, f.along0
		}
		lv.last, lv.to = i, f.along1
		lv.height = maxf(lv.height, f.height)
		text = append(text, f.text...)
	}
	if lv.first < 0 {
		return level{}, false
	}
	// Trimmed first: a space at either end of the run is the gap to whatever the page set
	// beside it, and only an interior one says the level is a compound expression.
	lv.spaced = bytes.ContainsFunc(bytes.TrimSpace(text), unicode.IsSpace)
	return lv, true
}

// writeSolidus puts the bar into the text, as a solidus after the numerator, and marks the
// denominator's line as continuing without a word boundary.
//
// A space the producer drew at the numerator's right edge is dropped, because that space is
// the horizontal gap to the bar and the solidus now occupies it. Page 383 draws one, in
// "𝑠𝑖𝑛(360 × 𝑥) " over 2, and keeping it emits "(𝑠𝑖𝑛(360 × 𝑥) )/2" where the same formula
// four lines down emits "(𝑠𝑖𝑛(360 × 𝑦))/2". It is the only one in the corpus, and no
// denominator there opens with one, so nothing is trimmed from that side.
func writeSolidus(num, den *line, n, d level) {
	tail := &num.frags[n.last]
	tail.text = bytes.TrimRightFunc(tail.text, unicode.IsSpace)
	// A whitespace-only fragment past the run is the same space by another route, and the
	// trim above cannot reach it: levelOf skips a blank fragment before it tests the extent,
	// so such a fragment is neither gathered into the level nor counted as text after it, and
	// it would emit "12/ 116" — the defect joinPrev exists to prevent, arriving where joinPrev
	// cannot see it. Nothing non-blank can be here, because that sets after and declines the
	// candidate. Emptied rather than removed, so no index a caller holds moves.
	for i := n.last + 1; i < len(num.frags); i++ {
		num.frags[i].text = nil
	}
	if n.spaced {
		tail.text = append(tail.text, ')')
	}
	tail.text = append(tail.text, '/')
	if n.spaced {
		openLevel(&num.frags[n.first])
	}
	if d.spaced {
		den.frags[d.last].text = append(den.frags[d.last].text, ')')
		openLevel(&den.frags[d.first])
	}
	// The wrap between these two lines is the bar, not a word boundary, so appendLine must
	// not infer a space at it.
	den.joinPrev = true
}

// openLevel puts the opening parenthesis in front of a level's first fragment.
//
// A level holding an interior space is what asks for the parentheses, rather than an
// operator table, because deciding which expressions a solidus would reassociate is
// mathematics and this package reads geometry. It wraps 32 of the corpus's 146 levels, of
// which 22 need it; the other 10 are already parenthesized, a function or a constructor
// applied to a compound argument — "𝑠𝑖𝑛(360 × 𝑥)/2" becomes "(𝑠𝑖𝑛(360 × 𝑥))/2", and
// "Union(𝑓𝑏 × 𝑞𝑏, 𝑓𝑠 × 𝑞𝑠)" the same way — where the extra pair is redundant and still says
// exactly what the page says. The alternative is a bracket-depth scan whose only effect is
// cosmetic and which no measurement here could confirm.
func openLevel(f *frag) {
	f.text = append([]byte{'('}, f.text...)
}

// blank reports whether a fragment's text is whitespace only, in which case it is a gap
// the page drew and not a term of the expression.
func blank(text []byte) bool {
	return len(bytes.TrimSpace(text)) == 0
}

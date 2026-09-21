# 14. Join a stacked fraction into the solidus form

Date: 2026-09-21

## Status

Accepted

## Context

A stacked fraction is two baselines with a rule between them, and on the page it means one
quantity. Read as text it is two. ISO 32000-2 page 205 draws 𝐿∗ + 16 over 116 and this
package emitted `𝐿∗ + 16 116`, which is not a formula that is hard to read — it is a
*different* formula, and nothing in the output distinguishes it from a page that really said
those two things in sequence. Every other inference in this repo recovers structure a
producer declared or drew; this one recovers an operator that was never written down as
text at all, so the question is not how to render it but whether the geometry says it is
there.

**The population is adversarial.** A short horizontal mark with text above and below it is
also a table's row rule, a heading's underline, a link's underline and the top edge of a
shaded cell. Over the 12 documents on disk, **281405** (line pair, mark) combinations put a
mark between two adjacent upright baselines. **73** of them are fractions — 71 in ISO
32000-2, on 27 of its 1023 pages, and 2 on page 12 of one arXiv paper. So the rule has to
reject 281332 candidates without rejecting those 73, and a threshold with no named
counterexample would be a guess dressed as a measurement.

**The two producers draw the bar incompatibly, and each hides half the problem.** ISO
32000-2 *fills* a rectangle — `re f` — so both long edges arrive as rules and the bar's
thickness is their separation, 0.60–0.72pt. pdfTeX *strokes* a line — `0.398 w 0 0 m 15.781
0 l S` — and emits **one** rule with no second edge anywhere on the page, whose thickness
exists only in the graphics state. A reader that recovers thickness from a pair of edges
finds every fraction in the ISO documents and none in a LaTeX one; a reader that trusts a
lone rule's width finds none of ISO's, because a fill reports no width. Both halves are
needed, and the corpus can only exercise one of them: every fraction on disk is in a
gitignored ISO document, so a clone without those PDFs ran no fraction test at all.

**Recall came from grouping, not from loosening.** Gathering every fragment the bar spans
on each baseline, rather than the nearest one, raises the count from 31 to 73 with no
threshold moved. A compound level is many fragments — `𝐿∗ + 16` is four, and the CIE
chromaticity numerators on pages 203 and 204 are eighteen — so a rule that takes one
fragment per level finds only the simple cases it was written against.

**Three measurements that sound better than the ones chosen are false**, each implemented
and scored before it was dropped:

- The bar's width as a *ratio* of the wider level's does not separate, and was the test here
  first, at 1.05. Page 203 sets Y_A, Y_B and Y_C as three copies of one formula, and the
  ratio put 𝑦𝑅/𝑅 at 1.065 while 𝑦G/G and 𝑦B/B came in at 1.044 and 1.046 — a threshold
  inside one population, splitting three identical lines two ways. A bar's overhang is an
  absolute amount, about a third of a point, so dividing it by a level four points wide
  inflates the ratio and dividing it by one two hundred points wide hides it.
- The marked-content identifier does not separate, though it looks like it should. The test
  was whether the bar was painted inside one of the sequences holding the levels it divides,
  and over all 12 documents it rejected nothing the accepted tests admit. Carrying the
  identifier from the painting operator to the rule cost a wrapper around `doc.Rule` on
  every rule of every page.
- The bar's *thickness denominated in the type size* does not separate, which is the one
  worth stating because it is so nearly right. Genuine bars run 0.0399–0.0896 of their
  level's glyph height, and the hardest false positive on disk — a booktabs table's bottom
  rule over its caption — measures 0.0800, near the middle of that range.

## Decision

`extract/fraction.go` rewrites a stacked fraction into ISO 80000-2's solidus form:
`(𝐿∗ + 16)/116`, with the parentheses the standard requires when a level is a sum or a
difference. `run.joinFractions` runs once per page, from `run.blocks`, before blocks are
assembled and after lines are closed.

**The markup stops at the solidus.** No `\frac`, no `$…$`: `extract` recovers text and
geometry, and which notation a document should be rendered in belongs to a sink. A LaTeX
sink can be layered on this later; a LaTeX extractor could not be unlayered.

**Four measurements do the separating**, each worth a count taken by turning it off with the
other three left on, so the number is what that test alone rejects:

| test | what it says | alone rejects |
|---|---|---|
| balance | the mark sits on the math axis, in 0.25–0.40 of the gap | 27 |
| thickness | the mark is one thin mark and not one line of a grid | 8 |
| overhang | the mark reaches past the wider level by < 0.25 of its type size | 2 |
| side | nothing follows the numerator and nothing precedes the denominator | 1 |

Every rejection is named in the source by document and page. The accepted 73 fall in
overhang −0.0137..0.0763, balance 0.2795..0.3561 and gap 1.0964..1.6304; the nearest thing
overhang has to reject sits at 0.3694, and balance's 27 are all underlines at 0.0500–0.0811,
four times away from the genuine band.

**Two further conditions reject nothing over this corpus and are kept anyway**, which the
source states plainly rather than presenting them as filters. `levelGapMax` bounds a
quantity balance cannot see — balance is a ratio and says nothing about absolute distance —
and contiguity is a precondition of the rewrite rather than a test, since the solidus is
written into the numerator's last gathered fragment.

**A grid line is three or more rules at one extent, not any same-extent partner.** This is
the one threshold here that was wrong before it was measured. Two bars of the same width at
the same indent are each other's far partner, so a page setting the same formula twice lost
both: `\frac{1 + 2}{3}` and `\frac{12}{3 + 4}` share the extent 294.554..316.693 because
`1 + 2` and `3 + 4` set to the same width in Computer Modern. Three is the floor because a
ruled table's rules come in threes at their least — a booktabs table's top, mid and bottom
rules share an extent, which is what still rejects the hardest false positive on disk.
Admitting the two-rule case costs **452 extra marks over the 12 documents and zero extra
fractions**, every one declined by overhang or balance, with four documents' Markdown
byte-identical.

**Two public fields are added, and the alternative was leaving half the rule unreachable.**
`doc.Rule.Width` is a rule's thickness perpendicular to itself, and
`content.GraphicsState.LineWidth` is the `w` operand that produces it, seeded to 1.0 because
§8.4.3.2 makes that the initial value — a stream that strokes without setting `w` gets a
1-unit line, and defaulting to zero would report it as a hairline. A fill sets no width and
reports none; zero therefore means *not recoverable from this rule alone*, not *hairline*,
which is why a lone zero-width edge is rejected.

Three limits on that pair of fields are stated on the fields rather than guarded, because each
would need something this package deliberately does not have. `LineWidth` is set by `w` and by
nothing else: an ExtGState may carry `/LW`, so `/GS0 gs` changes the width with no `w` anywhere
and the field then reports the stale value — 1 if nothing set one — which reads a 3pt border as
a hairline in one direction and a hairline as 3pt in the other. Applying it needs the page's
resource dictionary, and `content` interprets a stream and resolves no references; `Apply`
reports false for `gs` so a caller holding the resources can see the operator go by. A path that
is filled *and* stroked reports the stroke's width on all four edges, so "zero for a filled
edge" is not exhaustive. And a skewed matrix has no single perpendicular extent for a thickness
to be, so `Width` would be wrong there rather than absent.

## Consequences

**Seventy-three joins on disk, and a fixture for the half the corpus cannot reach.**
`testdata/reference/fractions.pdf` is built with `pdflatex`, so its bars are strokes, and it
is the repo's only committable fraction fixture. It is also the same-extent collision: two
of its three equations share a bar extent, and it fails against the pre-fix binary on
exactly those two. It joins `exactFixtures`, so the whole page's text is pinned, including
that the full-width `\rule` between two paragraphs — the commonest false-positive shape the
corpus has — contributes no solidus and no text.

**Twenty-three mutations on the geometry, twenty-one killed from `./extract/` alone, and both
survivors are recorded in the source rather than fixed.** Both directions of `gridRules` and
its `>=`; either half of `extentRules`' tolerance, the tolerance itself, counting rules instead
of positions, and pairing a rule with its own duplicate; both bounds on a pair's separation;
the width floor on a far partner; the lower-edge rule that makes a pair arrive once; the
midpoint shift; the mark dedupe and the sort key it depends on; the chain guard, the artifact
guard, the blank-fragment clear and the split-run guard; and both directions of `strokeWidth`'s
axis swap — each named killer is in `extract/fraction_test.go` or `extract/rules_test.go`.

Two passes were needed and the first pass is the lesson: run over `./extract/` **and**
`./cmd/pdfspec/` it reported twelve of twelve killed, and three of those kills came only from
the fixture and corpus tests. A guard held up by a fixture is not covered by the package that
owns it. The two inclusive bounds also needed exact coordinates — `700.1 − 700` is
0.10000000000002274 in float64, *above* `barThickMin` rather than on it, so a pair a nominal
tenth of a point apart lands on either side of the floor depending on where the producer drew
it, which is a property of the coordinates and not something this package can tolerance away.
Two more mutants survived a pass that had every other one killed and both were unobservable by
luck rather than by equivalence: the sort's second key only matters when duplicates of two
marks interleave, and the split-run guard only when a level's fragments overlap on the page.
Both now have a fixture built for exactly that state.

The survivors are the position check `joinFraction` opens with, and the zero-width/zero-height
bound above the overhang test. Deleting the first changes no output over the corpus or the
tests, because balance declines the same candidates: a mark above the numerator is a negative
share of the gap and one below the denominator a share above 1. It is kept for the one thing
balance cannot do — it keeps the gap strictly positive, so two levels on one baseline cannot
reach a division by zero. The second is a check on the producer's numbers rather than on this
package's: a glyph with no advance and a font with no size are both legal in a content stream,
and two levels with no shape have nothing for the overhang to be a fraction of. Neither is
credited with rejecting anything, on the rule that a test named for a threshold should not be
believed to cover a line that never rejects anything.

**A fresh reviewer found a live regression this change had introduced, and the fix is one
line.** Hoisting the fragment sort out of the assembly loop — needed, because `joinFractions`
reads where a level sits in its line — put it ahead of `splitAtRules`, and `splitFrag` leaves a
fragment's pieces in that fragment's own slot. So sorting the parents does not sort the pieces:
a table row emitted as one show operation with a differently-styled run inside one of its cells
emitted `"AA ZZmid"`, which is the exact disorder the sort exists to prevent. It now runs
between the split and the rewrite, `TestSplitPiecesSortIntoPositionOrder` fails if it moves
back, and the comment claims the invariant that is true — `splitAtRules` does not care about
order — rather than that sorting commutes with splitting.

Seven further defects came out of the same review and all seven are in this change's own code:
`extentRules` replaces two separate scans that had come to disagree about duplicates;
`dedupeMarks` enforces the one-mark-per-bar contract `bars` states, which a producer emitting
one path twice broke; `barMark`'s doc comment now states the assumption its four outcomes rest
on, that at most two rules lie within `barThickMax` at one extent; a whitespace-only fragment
past the numerator's run is cleared, since `levelOf` skips it before the extent test and it
emitted `"12/ 116"`; a line is now one level of one fraction, so three baselines with two bars
cannot chain into `12/34/56`; a pair straddling the artifact boundary is skipped, since joining
one meant emitting `"12/"` after the other was dropped; and `strokeWidth` reads its two scale
factors by page axis rather than by user axis. All twelve corpus documents are byte-identical
through every one of those fixes, which is the measurement that says they are latent shapes
rather than live defects — the sort hoist excepted, since it was live.

**An inline fraction is not joined, and that is recorded rather than pinned.** Inline math
sets the levels in scriptstyle around the *text* baseline, so the prose lies between them
and they are not adjacent line records at all. Measured: `An inline fraction
$\frac{12}{116}$ sits in a sentence` extracts as `An inline fraction 1`, `2`, `116 sits in a
sentence` — the numerator splitting across two line records at cross 554.986 and 558.908 is
a second, line-assignment defect independent of fractions. Every fraction on disk is set
with each level alone on its own line, so nothing here is exercised by the corpus, and a
fixture asserting today's output would be pinning the wrong answer.

**A nested fraction flattens, and the two shapes it comes in are answered differently.** Set
inline, as ISO 32000-2 page 177 does, `1/(sin 𝑗/2)` emits `1sin𝑗/2` — wrong at HEAD too and in
the same way, one instance on disk, mechanism unmeasured, so the limitation recorded is the
symptom and nothing about the cause. Set as a stack, three baselines with a bar between each
pair, the two joins would chain into `12/34/56`: a repeated solidus with no parentheses, which
is a form ISO 80000-2 does not permit, so it would assert something the standard this rewrite
cites refuses to read. That one is guarded rather than recorded — a join advances past the
denominator, so a line is one level of one fraction — and the second bar is left undivided.
`12/34 56` says less than the page does where the chain would say something else.

**`doc.Page.Rules` is documented by measurement now, because this rule is its first real
consumer** and the field's comment was wrong in both directions. Of ISO 32000-2's 72002
painting operators, 71194 sit inside an `/Artifact` with no MCID and 15 outside any marked
content, but **793 sit inside a `Span`, a `P` or a `Figure` carrying a real identifier**
(531/254/8) — which is how page 205 paints the bars this rule reads, so "drawn outside any
marked content" was false for the load-bearing minority. And of the 647 pages that both
paint and show text, **exactly one** paints before its first text operator, so "frequently
before the text it encloses" was inverted. 658 of 1023 pages draw at least one rule, 284866
in all, and 9 of the 11 reference fixtures draw none.

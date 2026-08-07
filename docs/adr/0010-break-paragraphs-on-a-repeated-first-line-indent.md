# 10. Break paragraphs on a repeated first-line indent

Date: 2026-08-07

## Status

Accepted. **Corrected on 2026-08-07** on one point of fact: the Consequences claim that
"all ten mutations of the rule are now caught" was premature when written. Re-running the
driver showed the tenth — replacing `observe`'s rebasing `+=` with a plain `max` —
surviving. It is now caught, by a fifth case in
`TestIndentMatchesTheBlocksOwnFirstLine`, and the correction is recorded inline below
rather than edited away because *why* it survived is the finding: **no document on disk
can distinguish the two forms.** Measured over the corpus they disagree on 111 of 30328
indent decisions and the spread guard's verdict differs on none of them, so the case had
to be constructed — a margin walking left in two sub-tolerance steps that sum past the
guard. A corpus, however large, is not a substitute for a case built to divide two
implementations.

## Context

ADR 0009 closed the heading half of DESIGN.md §10's paragraph-break gap and left the
other half open with a stated reason:

> **The same-size case remains open.** `text-styles`' four paragraphs are all 9.96pt, so
> no size ratio separates them and the only remaining evidence is the leading itself.

**Both halves of that sentence are wrong, and measuring them is what produced this
ADR.** They are corrected in ADR 0009's Status section rather than edited away.

**The leading is not evidence at all.** A document that sets no extra space between
paragraphs — `article`'s default `\parskip` of zero, which is most books and most
specifications — steps down by exactly one line at a paragraph boundary, because that
is what "no extra space" means. Over `testdata/reference/paragraphs.pdf` every
consecutive line pair steps down 11.955pt against a 9.963pt line height, a ratio of
**1.200 to three decimals**, whether the pair is an ordinary wrap or a paragraph
boundary. No value of `ParaFrac` separates the two populations because there are not
two populations. Tuning it would have produced a plausible-looking constant that
separated nothing.

**`text-styles.pdf` is not the fixture for this case.** Measured, its four paragraphs
are *one line each*, so every consecutive pair in it is a paragraph boundary and there
is no wrap to contrast against. A rule that split unconditionally would score perfectly
on it. The gap it reports — four paragraphs arriving as one line — is real, but it
cannot discriminate the rule that fixes it, and a threshold tuned against it would be
tuned against a document that cannot exercise the case.

So the first work was a fixture that can: `reference/paragraphs.tex` sets three
paragraphs wrapped over three or four lines each, one size, one leading, no `\parskip`.
Hyphenation is suppressed in it, because with hyphenation on LaTeX broke "contains"
across a line and the extracted text read "con- tains" — a real defect and a *different*
one, and a fixture failing for either reason distinguishes neither.

With the vertical signal eliminated, what remains is horizontal. In that fixture the
first line of each paragraph starts at x = 148.712 and its continuations at
x = 133.768: a delta of 14.944pt, which is three space widths (4.981 × 3 = 14.943) and
is LaTeX's 15pt `\parindent`.

**An indent by itself is far too noisy to act on**, which the corpus says plainly. Run
as the live rule over every PDF on disk, "this line is indented past its block's
margin" fires **441 times across 19 files**. Reading them rather than counting them is
what mattered: they are mostly C source listings in `pymupdf/mupdf_explored.pdf`, where
the indent is program syntax, and hanging-indented bullets in ISO 32000-2, where the
*continuation* lines are indented and the marker line is not.

## Decision

**`extract` starts a new block where a line repeats the indent its current block's own
first line was set with.** Three conditions, each rejecting a measured failure mode:

- **The indent must fall in a window**, `Tolerance.IndentFrac` to `IndentMax`, default
  1.0 to 6.0 space widths.
- **The indent must match the block's own first line**, within half a space width.
- **The block's continuation lines must agree on a left edge**, within half a space
  width of spread.

Expressed as multiples of the font's own space advance rather than in points, for the
reason `SizeFrac` is a ratio: a 7pt footnote and a 28pt heading are then judged alike.

- **Matching the block's *own* first line is the whole design, and it is structural
  rather than tuned.** It takes the 441 naive firings down to **11**. Both noise
  populations are rejected by construction: a hanging-indented bullet's own first line
  sits *left* of its continuations, not right of them, so no threshold choice is
  involved in declining it.
- **The spread guard rejects centred text, and exists because of a real regression.**
  `pymupdf/dotted-gridlines.pdf` sets centred table headers whose lines start at
  285.53, 282.53, 286.73, 285.65 and 287.45 — wandering about two points around a
  centre. Against that document's 1.335pt space advance two points is 1.35 space
  widths, which cleared the window and split `COMUNI SUPERIORI 15.000 abitanti (SUP)`
  mid-phrase. A left-aligned block's continuations agree to within float noise, so half
  a space width separates the two cases without needing to recognize centring as such.
  Measured: that file fires twice with the own-first-line check alone and not at all
  with this guard. Across the corpus the guard removes 8 of the 11.
- **The rule declines where the block has no continuation line yet.** With a single
  continuation the margin is a guess, so a two-line paragraph followed by an indented
  third line is left alone. This is a deliberate loss of recall for precision.
- **`IndentFrac <= 0` reads as off**, and unlike `SizeFrac` that is a usable setting: of
  the three block-breaking rules this is the least certain, and a caller who wants only
  vertical evidence can have it.
- **The bounds are load-bearing to very different degrees, and the code says which.**
  Swept with the shipping rule, the floor is *flat* — 0.75, 1.0, 1.25, 1.5, 2.0 and 3.0
  all yield the same 3 extra blocks — so it is a floor asserting that an indent under
  one space width is not one, not a threshold at a measured trough. The ceiling does
  real work: unbounded it admits 28 extra blocks, at 6 it admits 3, and it plateaus
  across 4 to 10, so 6 sits inside a stable band rather than on an edge. What it
  excludes is placement rather than indentation — the offsets above 6 run to 17, 63 and
  94 space widths and are table cells and addresses.
- **Thresholds live in `geom.Tolerance`** with every other one, for the reason that
  package documents.

## Consequences

**DESIGN.md §10's paragraph-break item is closed.** `reference/paragraphs.pdf` converts
byte-for-byte to its hand-written gold file, and `paragraphs` is now enforced in
`TestReferenceExactMatch`'s `exactFixtures` rather than logged.

**The corpus cost is 3 firings across 2 files.** Two are the real boundaries in
`paragraphs.pdf`; the third is in `mupdf_explored.pdf`, where the change is to C
listings and TOC entries and is cosmetic. ISO 32000-2 and `autotagPDFInput.pdf` do not
change at all. That is the precision this rule was built for: a segmentation rule
firing 441 times on a corpus this size would be a liability whatever its recall.

**Text is conserved.** As with the size rule, a segmentation change can drop or
duplicate a line while satisfying every threshold assertion, so
`TestIndentBreakConservesText` compares page text with the rule on against the same
text with it off across every PDF present, ignoring whitespace because a boundary
change is exactly what alters the join-with-a-space behavior. 47 fixtures, 2 with
boundaries moved, none losing or gaining a character. The test also requires that at
least one fixture's boundaries *move*, so it cannot pass by the rule silently never
firing — the failure mode a conservation test is otherwise blind to.

**The half-space tolerances are pinned by test, because review found they were not.**
Mutation testing the finished rule showed that widening the own-first-line agreement from
half a space width to a vacuous 99 left every test passing — the discriminator this ADR
calls the whole design had its *direction* constrained and its *magnitude* not, and made
vacuous it fires 226 times over the corpus instead of 3. Tightening either tolerance to
exact equality also survived. `TestIndentMatchesTheBlocksOwnFirstLine` closes all three:
a hanging-indented block where only the own-line comparison declines, and two near-misses
at the scale of producer rounding that must still be treated as agreement. All ten
mutations of the rule are now caught.

> **Corrected.** That last sentence was not true when written — the tenth mutation,
> `observe`'s rebasing `+=` replaced by a plain `max`, still survived. It is caught now by
> a fifth case in that test, and the reason it lasted is worth more than the fix: the two
> forms diverge on 111 of 30328 corpus indent decisions and change the guard's verdict on
> **none** of them, so no fixture could have killed it. The case is synthetic by necessity
> — a margin walking left in two 0.36 space-width steps whose 0.72 total clears the guard
> — and it is the standing example that corpus breadth does not substitute for a case
> built to divide two implementations. `max` under-reports every block whose margin moves;
> see `observe`'s comment for the hand-trace.

**The fixture pins the measurement, not just the output.** Enforcing `paragraphs`
exactly is what keeps the 1.200-ratio finding from being re-lost: anything that makes
that file pass by reading the vertical step is reading noise, and the fixture is also
the guard on this rule's own failure mode, since a rule loose enough to fire on centring
jitter would break its three paragraphs into more.

**What this does not close.** A paragraph boundary in a document that sets *neither*
extra space *nor* a first-line indent has no evidence left in the geometry at all, and
this rule correctly declines it rather than guessing. Nor does it help a two-line
paragraph, per the margin condition above. DESIGN.md §10's remaining items — list role
and table grid — are untouched.

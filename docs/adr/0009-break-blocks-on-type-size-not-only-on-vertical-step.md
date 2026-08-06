# 9. Break blocks on type size, not only on vertical step

Date: 2026-08-06

## Status

Accepted

## Context

`extract.continues` decided paragraph membership from one signal: the vertical step
between consecutive baselines, against `Tolerance.ParaFrac` times the line height. That
is the right test for prose and it is why a wrapped paragraph stays one block.

It cannot see a heading. A heading set at ordinary leading steps down by exactly one
line, so the step test joins it to the prose beneath and the heading is resolved as the
first words of the following paragraph. ADR 0008 hit this from the other side: `layout`
had nothing to promote on `adobe-samples/autotagPDFInput.pdf` or
`pymupdf/v110-changes.pdf`, not because its gate rejected their headings but because
their headings were not separate blocks. No classification rule can recover a boundary
that segmentation never drew.

The obvious fix — break wherever the style changes — was measured and rejected.
`testdata/reference/text-styles.pdf` sets four consecutive same-size paragraphs that
differ only in which word each emphasizes, so their longest fragments differ in weight
while the paragraphs themselves are unremarkable prose. A weight test breaks blocks at
whichever word happens to be bold.

That leaves size, which needed a threshold rather than a guess. Measured over the 6,023
line pairs the corpus joins on vertical step alone:

| dominant-size ratio | joins | what they are |
|---|---|---|
| 1.00 | 5,769 | ordinary wrapped prose |
| ≤ 1.057 | ~90 | jitter — OCR reporting one line of type at 27 and 28pt, an ISO cover's 11.5pt address line against a 12pt URL |
| ≥ 1.067 | ~164 | structure — a 32pt title meeting a 30pt subtitle, Adobe's 13.02pt headings meeting 12pt body |

Nothing in the corpus falls between 1.057 and 1.067.

## Decision

**`continues` also breaks a block where two consecutive lines' *dominant* type sizes
differ by more than `Tolerance.SizeFrac`, default 1.06.**

- **A ratio, not a difference in points**, so a 7pt footnote and a 28pt title are judged
  on the same scale. Larger over smaller, so the test is direction-free.
- **`SizeFrac` lives in `geom.Tolerance`** with every other threshold, for the reason
  that package documents: a judgement scattered as an inline epsilon is untunable and
  unmeasurable.
- **1.06 is measured, sitting in the empty band between the two populations above.** It
  is not a round number chosen for looking like one.
- **The comparison is on the dominant size — the size most of the line's characters are
  set in — not the largest.** An inline superscript or footnote marker is a legitimately
  different size within one line of prose, and a largest-size rule makes every annotated
  line look like a heading meeting body text. Characters means *runes*, as it does in
  `layout.bodyCluster`: a byte tally counts a CJK or mathematical character three or four
  times over a Latin one, and four CJK characters would outweigh nine ASCII ones.
- **`SizeFrac <= 1` reads as "off" rather than being applied literally.** A ratio of 1
  splits on any difference at all, which no real document survives. This is what a caller
  filling some `Tolerance` fields and not others gets, and silently shattering every
  block would be a worse answer than doing nothing.
- **Weight is not part of the test**, per the `text-styles` measurement above.
- **The test does not apply where both lines are inside one marked-content element**, per
  the Consequences below: a producer that declared them one thing has said more than any
  size ratio can, and splitting the declaration loses the space that joined them.

## Consequences

The heading half of DESIGN.md §10's paragraph-break gap is closed.
`autotagPDFInput.pdf` went from 0 to 12 heading candidates and `v110-changes.pdf` from 0
to 6, and all 18 are real headings on the page.

**Neither promotes a heading yet, and that is ADR 0008's unnumbered limit rather than a
new defect.** Every one of the 18 is unnumbered — "Lists", "Simple Table", "Pixmap",
"API Change: Text Page" — so the numbering rule that assigns levels has nothing to read.
The limit is now reached honestly instead of being masked by a segmentation defect one
layer down, which is the point: two independent gaps were presenting as one.

**Text is conserved.** A segmentation change can drop a line or duplicate one into both
blocks while satisfying every threshold assertion, so `TestSizeBreakConservesText`
compares the page text with the rule on against the same text with it off across every
committed fixture, ignoring whitespace because the join-with-a-space behavior is exactly
what a boundary change alters. 34 fixtures, 8 with boundaries moved, none losing or
gaining a character. The gate also requires that at least one fixture's boundaries
*move*, so the test cannot pass by the rule silently never firing.

**The same-size case remains open.** `text-styles`' four paragraphs are all 9.96pt, so no
size ratio separates them and the only remaining evidence is the leading itself — a
paragraph break in a document that sets no extra space between paragraphs. That is the
rest of DESIGN.md §10's paragraph-break item and this ADR does not address it.

**The size test does not apply inside a single marked-content element.** Where a producer
declared two lines to be one thing, a layout heuristic has nothing to add — the same rule
ADR 0008 applies to roles. Untagged pages carry no MCID, so this never reaches the
documents the size test was added for; a line spanning two MCIDs is not a declaration that
the lines belong together, so the test still applies there.

**That guard also covers one route into a pre-existing defect, and does not close it.**
`appendLine` writes the space that joins two lines onto the *second* line's leading text,
so a line that starts a block loses it, and `sectionize.title` then rejoins spans sharing
a `(page, MCID)` across the block boundary with no separator. ISO/TS 32003:2023's cover is
the case — a 36pt document number over a 17.5pt title, both `/MCID 3` — which read
"ISO/TS 32003:2023Document management" before the guard. But the size test is not the only
way a block boundary lands between two same-MCID spans: the vertical-step test does it
too, and always could. Measured as the count of cross-block same-MCID adjacencies where
the join would need a space, over all 47 PDFs: **47 before this change, 74 after** — so
the guard removes the route it introduced on tagged covers and leaves the 47 that the step
test already produced. One of them now manifests where it did not before: ISO 32000-2's
`𝐷min 2𝑛` becomes `𝐷min2𝑛`, a fraction whose bar was being rendered as a space.

The root fix is to put the wrap space on the *trailing* end of the previous line instead,
where any regrouping keeps it. That was not done here: it changes what `doc.Span.Text`
contains for every wrapped line in every document, which is a public observable needing
its own measurement, and `appendLine`'s comment records the current placement as
deliberate. DESIGN.md carries it as a known defect with this measurement.

**Block counts move for any document with mixed type sizes**, which is a public
observable for anything reading `doc.Page.Blocks`. Markdown output was diffed over all 47
convertible PDFs on disk, including the 11 tagged ISO documents: 17 files changed, and
with whitespace squeezed out 46 of the 47 are character-for-character identical — no PDF
text is gained or lost, only whitespace moves. Most of what moves is a spurious leading
space a block boundary used to inherit (` A use of ISO 32000` → `A use of ISO 32000`). The
word-level changes are all in mathematics, where subscript levels join or separate as one
variable — ISO 32000-2's `*T*f s` → `*T*fs` — which is where a size-based rule is at its
least certain and where the corpus offers no gold answer. One of them, `𝐷min 2𝑛` →
`𝐷min2𝑛`, is the lost-space defect above rather than a subscript joining correctly. The
47th file, the LightOnOCR
paper, gains two `*` characters and no PDF text: a boundary moved so that an italic run
carries its own Markdown delimiters instead of sharing a neighbour's. The enforced
reference fixtures are unchanged.

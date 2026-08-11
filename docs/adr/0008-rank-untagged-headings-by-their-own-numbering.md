# 8. Rank untagged headings by their own numbering, not by font size

Date: 2026-08-06

## Status

Accepted

## Context

Most PDFs are untagged, so for most PDFs no structure tree declares a heading. ADR 0002
built the clause hierarchy from the *tagged* heading sequence and left the untagged path
to "layout heuristics" — recognizing a heading "by size and weight". This ADR is what
happened when that was measured, because size and weight turn out not to be sufficient,
and the reason is worth recording where the next person to touch it will find it.

Measured over the untagged corpus, the obvious rule fails four different ways:

| document | body | what defeats "bold and larger" |
|---|---|---|
| `pymupdf/v110-changes.pdf` | 9.96pt plain at 49.4% of chars, narrowly over 8.04pt **bold** at 48.8% | bold is not emphasis here at all; a weight rule marks half the document |
| `pymupdf/2201.00069.pdf` (arXiv) | 8.97pt plain | headings are 11.96pt **plain**; bold is inline emphasis, 0.3% of chars |
| `adobe-samples/autotagPDFInput.pdf` | 12pt plain | headings are 28/16pt **plain**; bold is emphasis, 0.5% of chars |
| `testdata/reference/headings.pdf` | 9.96pt plain | its level-3 heading is at *body size*, bold only |
| `pymupdf/dotted-gridlines.pdf` | 7.20pt plain | a 41-char table row at body size in bold — the exact shape of a heading |

The last two are the bind. `headings.pdf`'s deepest level is body-size bold and must be
promoted; `dotted-gridlines.pdf`'s table row is body-size bold and must not be. No
typographic property separates them. Neither does the space above: the table row sits
1.68 body-sizes below its predecessor, inside the 1.60–1.96 range the reference
headings occupy.

The other candidate rule was ranking by position in the size ladder — sort the
above-body sizes descending, call the largest level 1. `pymupdf/mupdf_explored.pdf`
falsifies it: five distinct above-body sizes (24.79 chapter titles, 20.66 "Chapter 1"
labels, 17.22 the book title, 14.35 sections, 11.96 the author line and part entries),
of which only some are heading levels at all. Ladder position disagreed with the
document's own numbering on **296 of 296** numbered headings there, because it counts
rungs that are not levels. It also has no rung for a body-size heading, so it cannot
express `headings.pdf` at all.

## Decision

A new `layout` package infers roles where nothing declares them, and inverts the usual
arrangement: **typography is the gate that admits a candidate; the document's own
section number assigns the level.**

- **The body cluster is the size most of the document's *characters* are set in.**
  Characters, not blocks — a page of one-line table rows would otherwise outvote the
  prose. Keyed on size alone, with the weight of the winning size reported alongside,
  because the two questions differ: "what size is the body" decides which blocks are
  larger, while "is the body bold" only says whether weight carries any signal in this
  document. Ties break toward the smaller size, because an arbitrary body size makes
  every comparison against it arbitrary.
- **The gate is "larger than the body, or bold where the body is not."** Both halves are
  required: requiring a larger size loses `headings.pdf`'s level 3, and requiring bold
  loses the arXiv and Adobe headings. Where the body is already bold, weight carries no
  signal and the rule degrades to size alone.
- **The level is the depth of a leading dotted-decimal number.** "4.2.1 Nested
  subclause" is level three because the document says so. The separator must be
  whitespace, so "3.14 is pi" is not level two and a table of measurements is not an
  outline.
- **An unnumbered candidate stays a paragraph.** This is the deliberate cost, and it is
  the `dotted-gridlines.pdf` row that buys it: promoting "Preface" means promoting that
  row, because nothing distinguishes them.
- **`sink/markdown` drops emphasis that covers a whole heading.** On this path the
  emphasis is *why* the block was recognized, and `#` already says heading, so
  `# **1 First Section**` restates it in a way no author writes. Emphasis *within* a
  heading is a different claim and survives.

`layout.Headings` mutates `Role` and `Level` in place on blocks `extract` left as
paragraphs. It is called on the untagged branch only — where a tree exists, sectionize
has already assigned every role from what the producer declared, and guessing over a
declaration replaces evidence with a heuristic.

## Consequences

`testdata/reference/headings.pdf` matches its gold file byte-for-byte and is enforced
rather than logged. That closes the first of the four untagged gaps DESIGN.md §10
records.

Measured over the fixture population, from `layout.Stats`:

| document | body | candidates | headings | left as prose |
|---|---|---|---|---|
| `reference/headings.pdf` | 9.96 | 4 | **4** | 0 |
| `pymupdf/mupdf_explored.pdf` | 9.96 | 410 | **296** | 114 |
| `pymupdf/2201.00069.pdf` | 8.97 | 3 | **2** | 1 |
| `pymupdf/dotted-gridlines.pdf` | 7.20 | 1 | **0** | 1 (the table row) |
| `pymupdf/v110-changes.pdf` | 9.96 | 0 | **0** | 0 |
| `adobe-samples/autotagPDFInput.pdf` | 12.00 | 0 | **0** | 0 |

Every other untagged fixture promotes nothing, including the five reference fixtures that
have no headings. On the untagged `LightOnOCR-2601.14251v1.pdf` paper all 21 numbered
headings promote at the level its own numbering states, and no numbered-heading-shaped
line is left behind. The 11 ISO documents are byte-identical, because they are tagged and
sectionize builds title blocks carrying no style.

**Unnumbered headings are not recognized, and this is the known limit.** "Preface",
"Contents", and `mupdf_explored.pdf`'s 114 unnumbered candidates stay paragraphs.
Lifting it needs a document-level pass that sees the *sequence* — an A.1 after an A is
evidence for an annex scheme where a lone "A" is not, and a size that heads a section on
forty pages is evidence where one occurrence is not. That is a different algorithm, not a
tuned threshold, and tuning a threshold to admit "Preface" on one document is how a
heuristic becomes fitted to a fixture.

> **Partly closed, and this paragraph's reasoning is partly falsified — 2026-08-11, ADR
> 0013.** The sequence pass is not what was needed and would not have worked. Measured
> against the tagged corpus's own declarations, rank and repetition are independent: 9 of
> the 151 unnumbered above-body candidate styles occur exactly once and include genuine
> titles, while the most-repeated include `mupdf_explored.pdf`'s "Robin Watts" at 9
> occurrences over 7 pages. No size ratio has a gap either — precision peaks at 73.2%
> around 1.17× body and reaches 6% by 1.63×.
>
> What the measurement found instead is that a third of the missing population is not
> unnumbered at all: it is numbered with a *letter*. Dotted lettered numbering ("A.1",
> "B.2.3") is declared a heading 112 times out of 112 with no false positives, so ADR 0013
> reads it in a new `annexLevel` and recovers 10 appendix headings in 2 documents. The
> *bare* letter stays deferred exactly as this paragraph says, and for exactly this
> paragraph's reason — "A" is also a word — with its counter-example now named:
> `PDF-Declarations.pdf`'s "A use of ISO 32000", which no producer declares a heading. The
> decision above is unchanged; a lettered number is a number, which is what this ADR
> already says assigns the level.

**An OKF bundle still needs a structure tree.** `layout` produces levels, not a tree.
Turning a levelled sequence into one means running sectionize's level stack over
`layout`'s output instead of over a structure tree — which ADR 0002 anticipated and is
the next step on this path. `pdfspec okf` continues to refuse untagged input, with an
error that now says which half landed.

> **Closed, 2026-08-09.** `sectionize.Untagged` is that step. It reads `(level, title,
> content)` out of `doc.RoleHeading` blocks and drives the identical `builder.open` and
> `builder.place` the tagged path drives over `H1`..`H6`, so a levelled sequence needs no
> tree of its own — the stack *is* the tree, exactly as ADR 0002 measured it (7 `Sect`
> elements against 981 headings). `pdfspec okf` accepts untagged input; 4 of the untagged
> documents on disk yield a bundle, `mupdf_explored.pdf` at 296 clauses three levels deep.
> The refusal survives only where inference finds no heading at all, which is this ADR's
> *first* consequence — unnumbered headings — reached honestly rather than hidden behind a
> missing tree.

**Block segmentation was the blocking gap underneath this, and it is why two of the six
reported zero candidates.** `extract.continues` tested only vertical step, so a heading
whose next line fell at ordinary leading fused into the paragraph after it, and
`autotagPDFInput.pdf` and `v110-changes.pdf` yielded *no* style-uniform blocks above their
body size at all — their headings were not separate blocks to promote, so no gate or
ranking rule could reach them. That has since been fixed in `extract` rather than worked
around in `layout`: `continues` now also breaks where two consecutive lines' dominant type
sizes differ by more than `Tolerance.SizeFrac`. Their candidate counts went to 12 and 6.

Neither promotes a heading even so, and the reason is the unnumbered limit above rather
than segmentation. Every one of the 18 recovered candidates is unnumbered: Adobe's are
"Accessible PDF Demo Document", "Lists", "Simple List", "Simple Table", "Embedded
Hyperlink" and the like; pymupdf's are "Pixmap", "PyMuPDF Design Decision", "API Change:
Display List", "API Change: Text Page", "API Change: Links", "Configuration Changes". The
limit is now reached honestly instead of being masked by a defect one layer down.

The bold-body case remains argued from measurement rather than from a passing fixture —
`v110-changes.pdf`'s body cluster is 9.96pt plain, so no corpus document actually exercises
a bold body — which is what `TestHeadingsBodyBoldCarriesNoSignal` stands in for.

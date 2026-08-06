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

**An OKF bundle still needs a structure tree.** `layout` produces levels, not a tree.
Turning a levelled sequence into one means running sectionize's level stack over
`layout`'s output instead of over a structure tree — which ADR 0002 anticipated and is
the next step on this path. `pdfspec okf` continues to refuse untagged input, with an
error that now says which half landed.

**Block segmentation is the blocking gap underneath this, and it is why two of the six
report zero candidates.** `extract.continues` tests only vertical step, so a heading whose
next line falls at ordinary leading fuses into the paragraph after it. `autotagPDFInput.pdf`
and `v110-changes.pdf` yield *no* style-uniform blocks above their body size at all — their
headings are not separate blocks to promote, so no gate or ranking rule could reach them.
Fixing it belongs to `extract`, not `layout`, and it is the same defect as DESIGN.md §10's
paragraph-break gap. It also means the bold-body case above is currently argued from
measurement rather than from a passing fixture, which is what
`TestHeadingsBodyBoldCarriesNoSignal` stands in for until segmentation lands.

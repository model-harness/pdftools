# 57. A page's own line pitch continues a paragraph

Date: 2026-10-28

## Status

Accepted.

## Context

pdfspec was scored on opendataloader-bench: 200 single PDF pages, with ground-truth Markdown and the benchmark's own evaluator. That evaluator reproduces docling's published score exactly here, 0.8817. pdfspec scored 0.755 overall, against docling's 0.882, and 0.522 on headings against 0.824. It missed every heading on 33 of the 107 pages that have one, where docling missed 2.

Some of those misses were not headings' at all. Document 173's title was missed because every line of the page was a block of its own, so the title was followed by a one-line fragment, not by the body a heading heads. The page was not unusual. Of the benchmark's 116 pages with six or more text blocks, 55 had more than 60% of them one line long, and 32 had every one.

The cause is extract's paragraph rule, `continues`: a line that steps down from the one above by more than `ParaFrac`, 1.5 times its type size, starts a new paragraph. These pages set their type on a loose leading. On 34 of the 55, the commonest step between lines was 1.6 times the type size, 9-point type 14 points apart, and on most of the rest between 1.5 and 2. Every line stepped down further than the rule allows a line to.

## Decision

**A step the page itself takes from line to line continues a paragraph, however loose** (`linePitches`, `onPitch`).

- **The page's pitch for a type size** is the commonest step from one line to the next in that size, taken three times or more. Steps further than three lines are left out, as gaps between regions.
- **A step within a tenth of a line of that pitch** continues the paragraph past `ParaFrac`. A larger step is the space between paragraphs, and still breaks it, and the first-line indent rule still applies.

Each of the following conditions was added after a measured regression, in the corpus or a reference fixture, that the condition before it let through.

- **Only after a full line,** one that runs to its column's right edge, within about a word, four of its type's size. A list's items and a table's rows are spaced as regularly as prose and stop where their text does: with no condition at all, `reference/lists.pdf`'s items merged into one.
- **And one a prose line's width, 20 of its size or more.** A column of short lines alone makes its own edge, so each of its lines reaches it, and `lists.pdf` still merged with the full-line condition alone. The benchmark's 9-point body lines are 31 of its size wide, and `lists.pdf`'s first item 17.
- **Not across a table's row,** a line with a gap two spaces wide or more in it. `pymupdf/small-table.pdf` spaces its rows at a pitch, and its header row is as wide as prose; without this its header and first row merged.
- **Only in a size the page sets no tighter.** Where the page's lines also step down within `ParaFrac` three times or more, that is its leading, and a larger step it takes as regularly is the space between its paragraphs. `mupdf_explored.pdf`'s list of libraries, its entries mostly one line each, merged without this, and so did `2201.00069`'s separate footnotes.
- **And the next line starts at the margin, or left of a first line's indent,** as a wrapped line does. `chinese-tables.pdf` sets a table's caption under a paragraph whose last line is full, at the paragraph's pitch, and further in.

## Acceptance

- **opendataloader-bench:** 29 of the 55 fragmented pages read as paragraphs again; 26 still do not. With ADR 0058, headings rise from 0.522 to 0.599.
- **Old against new** over the 76 PDFs here: no document's Markdown changes. Their leading is under `ParaFrac`, so the pitch never stands in for it.
- **The new tests** set 9-point type 14 points apart: two paragraphs, broken where the step grows; a list, a table, a caption set in and a tightly set page, which stay as they were.

## Mutation

16 mutants cover the pitch: its counts, its bins and their order, the steps it takes and leaves out, the tolerance, each of the five conditions, the column edge, and the wiring. All 16 are killed, each by a named test in extract, so none relies on a fixture another package holds or on a corpus document a clone lacks.

- **One guard could not change the output, and was deleted:** a tight step was left out of the bins, but where the pitch is used there are fewer than three, so they are never the commonest.
- **Two fixes came of the mutants:**
  - a table's row was setting its column's right edge, so no prose line beside it reached that edge; the edge now leaves gapped lines out;
  - two steps taken as often were told apart by map order, which is random; they are taken in ascending order now, so the shorter is the pitch by construction.
- **The first fixtures tested the sign:** the first paragraph ended on a short line, so the break was the full-line test's and not the step's, and a row only ever met another row. Each condition now has a case that it alone decides.
- One mutant did not compile; the tie-break it mutated is the ascending order now, and that order is mutated in its place.

## Consequences

- A page set on a loose leading keeps its paragraphs, so its headings head them and its reading order is a paragraph's, not a line's.
- A page whose short lines are spaced loosely and regularly, a list or a table, is read as before.
- The 26 pages still fragmented are set in ways these conditions decline, and are left to further work.

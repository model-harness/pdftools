# 36. Pair a footnote mark with its note

Date: 2026-10-20

## Status

Accepted. Closes issue #6. Tagged `Note` elements are a separate population, filed under #1.

## Context

An untagged page's footnote came out as two pieces of text in the wrong places. Its mark was a digit glued to the word before it (`island1`), and its note was a paragraph after the body. Several notes on one page merged into one paragraph: `1Counted … 2The eastern …`. `footnotes.pdf` scored CER 0.019 and blocks 0.571. Real files show the same defect:

- LightOnOCR's two URL notes sat inside the paragraph a page break had cut. That also stopped `PageBreaks` from joining it.
- `mupdf_explored.pdf` has a note on a marking operation, and one on bidirectional text.

## Decision

**The model.** Two additions:

- `doc.RoleFootnote` is a note's text. Its `Marker` is the label.
- `doc.Span.Note` is set on the span that marks a note, and names its label. The span keeps its own text, so a sink that cannot link a note still says what the page says.

**`layout.Footnotes`.** It runs after the list passes and before `PageBreaks`. A mark is a span of its own that reads as a label:

- a number of up to three digits, or up to three of `* † ‡ § ¶ ‖`;
- set at no more than 0.8 of its line's size;
- raised a quarter of the line's size or more above the line's foot.

The fixture's marks are 6.97pt in a 9.96pt line, raised 4.2pt, and its notes repeat that shape one size down. A note is taken only where the page has both halves:

- **the note:** a block below all of the page's other text, furniture aside, whose line opens with a mark. It is cut into one note at each such line.
- **the mark:** the first mark in the page's text that says the same label and no note has taken.

Either half alone is weak evidence. A raised small digit is as often an exponent or a unit, and a small block at the foot is as often a caption or an address.

**Labels.**

- A number is its own label once per document. A later page's `1` becomes `1-75`, after its page.
- A mark that is not a number becomes `fn` and a count, because GFM would read a label of `*` as emphasis.

**Cutting.** The cut is `cutAtLine`, factored out of `PageBreaks`' indent split rather than written a second time. Each half's box and MCIDs are now its own.

**`PageBreaks` passes over footnotes,** as it does furniture. A page's notes lie below the paragraph it ends with, so before this they stood between the paragraph and its continuation.

**The sink.** `sink/markdown` writes a mark as `[^label]`. Footnote blocks are held back and written as definitions at the end of whatever is being written:

- the document;
- one page under `-split`;
- a clause in a bundle;
- an outline.

GFM renders a definition at the end wherever it stands. A model reading the source gets the text unbroken and the notes after it, which is where the gold files put them. In text already inside markers, a mark is the page's digit, since a reference there is not one.

## Acceptance

**`footnotes.pdf`** went from CER 0.019 and blocks 0.571 to 0.000 and 1.000, and its output equals its gold. The bench mean for blocks rose from 0.714 to 0.729, and for CER from 0.168 to 0.167.

**The corpus.** Three documents changed. Every note was read, and every one is a note:

- **the fixture's 2;**
- **LightOnOCR's 2:** its model URLs. Moving them out of the text lets `PageBreaks` join the paragraph they had split.
- **`mupdf_explored.pdf`'s 2:** labelled `1` and `1-75`.

`pandoc -f gfm` links every reference to its definition in all three.

**The tests.** `footnotes_test.go` covers:

- the fixture's shape, including each note's MCIDs and box;
- each half alone, a mark and a note that differ, and text below the note;
- a raised word, and a heading at the foot;
- each of the mark's two bounds, at it and just past it;
- labels across pages, and symbols;
- one mark taken once, and two notes with one mark;
- three notes in one block;
- a mark that opens a body line;
- marks in furniture, and a folio below the notes;
- a line that starts inside a span;
- blocks with no lines;
- `PageBreaks` across a page with notes.

The sink's tests cover references and definitions, every writer, and plain text. `TestBlockLinesDescribeTheirSpans` covers the cuts.

## Mutation

43 mutants across the pass, the cut and the sink, all killed by named tests. The first run left 15 alive.

- **Three guards could never fire, and were deleted:**
  - skipping a page with no notes, where rebuilding its list changes nothing;
  - clearing the held notes, since each writer flushes exactly once;
  - letting plain text run on through a mark, since a separate run writes the same bytes.
- **The other twelve were missing cases, now tested:**
  - three notes in one block, `‖` as a label, and a raised word;
  - a heading at the foot, a line opening inside a label span, and a mark in a running header;
  - a mark opening a body line, two notes with one mark, and a mark opening plain text;
  - the cut head's MCIDs and box.

## Consequences

- Untagged footnotes are GFM footnotes, linked from their marks.
- A note continued onto the next page, or a mark set at the body's size and not raised, is not found. Neither occurs on disk.
- Tagged documents keep their `Note` elements inline: 25 across five standards. That is the tagged path's reconstruction, so it is a separate issue.

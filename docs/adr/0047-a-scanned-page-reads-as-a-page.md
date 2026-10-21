# 47. A scanned page reads as a page

Date: 2026-10-21

## Status

Accepted. Closes issue #12.

## Context

#12 asks for a PDF of page images to convert with the same structure as a born-digital one: Tesseract's word boxes feeding the same layout code as glyphs, so that a scanned page gets headings, paragraphs and tables.

ADR 0043 read a scan's words, and ADR 0044 had Gemma read again those Tesseract doubted. The gold set's four scans then read their words. Three of them missed their structure, with Tesseract and without Gemma:

| fixture | CER | blocks | headings | tables | why |
|---|---|---|---|---|---|
| scan-table | 0.088 | 0.000 | | 0.000 | the grid read as a line of text, its rules as `\|` |
| scan-two-column | 0.000 | 0.667 | | | a paragraph cut by the column break, as two |
| scan-unnumbered-headings | 0.000 | | 0.667 | | two of four headings read as paragraphs |

A word's box was the only measure of its type, so a line's size was its box's height. That reads a descender as a larger type and a line of x-height letters as a smaller one. On the heading scan, body lines of one size read from 7.9 to 13.7 points. Tesseract reads no weight at all.

## Decision

**A page is levelled before it is read** (`ocr.Deskew`). A flatbed scan is turned, and the gold set's are turned by 0.6 and 0.8 degrees, as `degrade.go` makes them. Across a 1,000-pixel rule that is 10 pixels, so no row of pixels holds the rule and no grid is found.

- The angle is the one whose projection of the page's ink onto rows is most peaked, searched from -3 to 3 degrees in tenths.
- The ink is sampled every 4th column and every row: sampled every 4th row as well, a 3-pixel rule fell between samples, and 0.6 degrees was found as -0.1.
- The page is turned back by interpolating between the four nearest pixels. Turned to the nearest pixel, scan-table's "B1" and "C1" came back "BL" and "Ci" from a page Tesseract had read right when it was still turned.
- A page under 0.15 degrees is left as it is.

Tesseract reads the levelled page.

**A line's size is read from its letters** (`lineSize`). Each word's height is divided by the share of the em its letters span: 0.43 for x-height alone, 0.69 with an ascender or a capital, 0.62 with a descender, and 0.88 with both. Those are Latin Modern's; another face differs by a few hundredths, and only a page's sizes against each other are read. The line's size is the median of its words', to the half point. The heading scan's four sizes then read 15.6, 12.7, 11.0 and 10.4, for type set at 17.3, 14.4, 12 and 10.

Sizes within 6% of each other are made one size (`snapSizes`), chained each to the next and capped at 12% of the first. Without this, the two top-level headings read 15.0 and 14.5 and made two levels, and lines of 10, 10.5 and 11 points in one paragraph read as three sizes.

**A line's weight is its stroke** (`Word.Stroke`, `boldStroke`). The adapter measures each word's median horizontal run of ink. Weight is that stroke over the word's size, and a line weighing 1.35 times the page's median word is bold. On the gold set's scans every body line weighs the median, with strokes of 2 pixels, and the bold lines 1.5 to 2.5 times it. A page under 20 words has no body to weigh against, and nothing on it is bold.

Ink was measured first, the share of a word's box that is dark, and the two were too close to part: body lines reached 1.11 times the median and the lightest bold line 1.13.

**A ruled table is read from its rules** (`ocr.Tables`):

1. Horizontal runs of ink half an inch long or more are rules.
2. Three of the same extent stacked are a candidate.
3. `Grid` (ADR 0046) confirms the candidate's region on its crop.
4. The words whose centres fall inside go into its cells, in two rows and two columns or it is no table.

The table's cells stand where it stood among the page's blocks, and its words are not written again as text.

**A paragraph cut by a column break is joined** (`joinColumns`). This is the extractor's rule for a page of glyphs, read over Tesseract's blocks. A block that opens a column, flush and at the same size as the block before it, carries that block on when two things hold:

- the block before ends at the foot of the page's text;
- the new block starts lower case, or the line before ran to its column's edge.

## Acceptance

**The gold set's scans with Tesseract,** without Gemma:

| fixture | CER | recall | precision | blocks | headings | tables | style |
|---|---|---|---|---|---|---|---|
| scan-paragraphs | 0.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| scan-table | 0.018 | 0.944 | 0.944 | 1.000 | 1.000 | 0.889 | 1.000 |
| scan-two-column | 0.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| scan-unnumbered-headings | 0.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |

Over the four, every v1 target is met: CER 0.005, recall and precision 0.986, blocks and headings 1.000, tables 0.889, style 1.000. scan-table's one word wrong is Tesseract's "Al" for "A1". Where inferd runs, Gemma reads it as "A1" and the word is `[err]`.

**The corpus.** Converted with Tesseract against the commit before, only four documents change: the three scans above, and ocrInput.pdf, whose bold headings ("Executive Summary" and the others) now read bold, as its render shows them.

**Tests.**

- `TestDeskewFindsTheAngle` and `TestRotateInterpolates` cover the levelling.
- `TestLineSizeReadsTheLetters` and `TestSnapSizesMakesOneSizeOfNearOnes` cover the sizes.
- `TestStrokeIsTheMedianRun` and `TestPageMarksAHeavyLineBold` cover the weight.
- `TestTablesFindTheRuledTable` covers the tables.
- `TestColumnsJoinACutParagraph` and `TestPageMakesNearSizesOneAndJoinsColumns` cover the columns.
- `TestOCRPageIsLevelledAndItsTableRead` runs a turned page with a ruled table through the command's OCR path.
- `TestBenchmarkWithTesseract`, opt-in, enforces every v1 target over the scans.

## Mutation

The 40 mutants were all killed, each by a named test. They cover the levelling, the size factors, the clustering, the bold rule, the table rules, the column join, and the command's use of each.

- **12 survived the first batch**, and 8 did not build. Each survivor was given a test that holds it:
  - a page with one dash, whose angle every near-level turn ties;
  - a chain of sizes stopped only by the cap;
  - a bold line at 1.3 times the median, and a heading twice the size with twice the stroke;
  - a footer below the first column;
  - an indented first line;
  - lower case after a short line;
  - a table's words not written again;
  - a doubtful cell written `[err]`.
- **One guard was a copy and was deleted:** `Tables` checked Grid's `ok`. Grid fails only with fewer than three lines one way, and Filled rejects the one row or column that leaves.
- **The adapter's stroke** is held only by `TestReadsAPage`, which needs Tesseract.

## Consequences

- A scanned page converts to headings, paragraphs and tables with no model.
- The measured constants are the gold set's, at 200 dpi, in Latin Modern: the share of the em, the bold weight and the size tolerance. A scan in another face, or of poorer quality, can need them measured again.
- A table drawn without rules, aligned by white space alone, is read as text on a scan. A born-digital page finds those by their gutters (ADR 0038), and Tesseract's boxes could feed the same reading.

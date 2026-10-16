# 30. Carry a paragraph over a column break

Date: 2026-10-16

## Status

Accepted. Issue #3, under the project index, #1.

## Context

The benchmark (ADR 0029) found two defects on the column fixtures, both at a column break:

- **A paragraph the break cuts through came out as two.** `two-column.pdf` broke at "where the | river narrows", `three-column.pdf` at "The second paragraph | follows". Blocks scored 0.500 and 0.571.
- **The paragraph after the break's run-on ran into it.** In `two-column.pdf` the third paragraph joined the second's continuation.

Both have one cause, in `extract`'s `blocks`:

- `continues` takes any line that steps back up the page as a new block, so a column break always ends the paragraph.
- `startsParagraph` finds a paragraph by an indent equal to the one its block opened with. A block that opens with the rest of a paragraph at a column top is flush, so its own indent is 0, and no later indent matches it.

Stream order is already the reading order on every fixture: order scored 1 throughout. What was missing is the paragraph across the break, not the order of the columns.

## Decision

`columns` divides a page's lines into columns. A column is a run of lines, each at or below the one before, so a line that steps back up the page opens the next column. Each column has the left and right edges its lines set. The lowest baseline of the page's text is kept for each orientation. A dropped artifact belongs to no column: a running header drawn after the text would otherwise open a column of its own, and a folio would set the page's foot.

**A column's first line carries on the paragraph** the previous column's last line was in when:

- it is **flush**, less than `IndentFrac` space widths from its column's left edge, in a column of two lines or more;
- it is the **same size**, by `sizeBreak`;
- the previous line is at the **foot of the page's text**, within one line height of the lowest baseline. A column breaks because it is full. A table cell ends at its row with the rest of the table below it.
- and one of these says the paragraph had not ended:
  - the line **starts lower case**, as a sentence carried over a break usually does;
  - or the previous line is **full**, within `lineFull`, one space width, of its column's right edge, as a justified line that wraps is. The previous column must have two lines or more for its edge to be any line's but its own.

**The paragraph indent carries over the break.** A line that opens a column flush takes `atColumnTop`'s indent state: its own left edge as the margin, and as the indent to look for, the one the block before was measuring. That applies whether the line carries on a paragraph or opens a new one.

**Page breaks are left to #6.** A paragraph across a page break needs the same decision with the previous page's last line. On an untagged page there is almost always a running header or folio between the two halves, and until #6 removes it there is nothing to join across.

## Acceptance

| fixture | blocks before | blocks after |
|---|---|---|
| `two-column` | 0.500 | 1.000 |
| `three-column` | 0.571 | 1.000 |
| `two-column-ragged`, new | 0.571 | 1.000 |

`two-column-ragged` is ragged right with spaced, unindented paragraphs, as word processors set them. Every line stops short there, so only the lower-case word at the right column's top says the sentence carries on.

Its floors are raised, and **order's v1 target is enforced** from here on (`benchEnforced`).

**The corpus.** Every document in `docs/` and `testdata/` was converted before and after: 2 of 70 changed, the two fixtures above. Without the foot-of-text condition, the rule made 18 joins elsewhere and none was a column. They were 12 matrices, formulas and table cells in ISO 32000-2, 4 cells in `chinese-tables.pdf`, and 2 matrix rows in `mupdf_explored.pdf`.

**The tests.** `TestAColumnBreakCanFallInsideAParagraph` covers each condition at its bound:

- **flush:** 0.9 space widths in is flush, and 1.2 is indented;
- **full:** half a space width short of the edge is full, and one and a half is not;
- **foot of the text:** one line height above the lowest text is at the foot, and 13pt against a 12pt line is not;
- a change of size;
- a lower-case line after a short one;
- a column of one line on either side.

`TestTheParagraphIndentCarriesOverAColumnBreak` finds the next paragraph in three places: in the joined paragraph, on the line just after the break, and in a column that opens a new paragraph. `TestADroppedArtifactIsNoPartOfTheColumns` draws a folio at the foot of the right column.

## Mutation

21 mutants were run against the final code. 18 are killed, each by a named test. The first run left one more alive: the margin left unknown at the break, which mattered only for a paragraph on the very next line. A test now covers it.

Three are equivalent. For each, converting all 70 documents gave output identical to production. As a positive control, a mutant that marks no column top changes exactly the two fixtures.

- **A line level with the one before opens a column (`>=` for `>`).** Such a line continues its block by `continues`' own test either way, so only the indent state differs, and nothing separates the two.
- **An orientation change no longer opens a column.** `continues` already fails on the orientation, so the block breaks either way.
- **A fraction's joined line can open a column.** `joinFractions` joins a line below its partner, never above.

## Findings, documented and not followed

- **`blocks`' doc comment had come apart from its function.** It sat above `sortLines`. It is back above `blocks`, and now says that a column break is where stream order steps back up the page.
- **Reading order is still stream order.** A producer that draws a page's columns in the wrong order is not corrected here. No fixture or corpus file does it.

## Consequences

- A paragraph runs on across a column break, and the paragraph after it is still found.
- `blocks` stays page-scoped. Joining across a page waits for #6's furniture removal.

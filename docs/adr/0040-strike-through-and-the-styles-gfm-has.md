# 40. Strike through, and the styles GFM has

Date: 2026-10-20

## Status

Accepted. Part of issue #7, which stays open for its output rules: links, wraps and decorative characters.

## Context

The gold set's style F1 stood at 0.648 against v1's 0.85. Three fixtures held it there, each for a different reason:

- **`strikethrough.pdf`** read 0.000. Nothing read a line drawn through text, so `~~Tuesday afternoon~~` came out plain.
- **`chrome-article.pdf`** read 0.571. Its italic `*north quay*` came out bold italic, and its table's header came out `| **Vessel** | **Calls** |`.
- **`xelatex-article.pdf`** read 0.667. Its `Changes` heading came out as a bold paragraph.

## Decision

**A strike** is a rule drawn through the letters' lower body. `extract.strikeThrough` runs after `splitAtRules`. It reads a horizontal rule as a strike when all of these hold:

- it lies between 0.15 and 0.6 of the type's size above the baseline (ulem's `\sout` runs at 0.26);
- it is no thicker than 0.15 of the size;
- it starts and ends within the line's text, give or take a space.

The last condition carries the most weight. Well-Tagged PDF draws a line across its whole measure through a list item, and ISO 32000-2 draws a figure's connector through the word "arrowhead". Each line runs past its text, and neither is a strike.

**The unit is a word:** a stretch between two of the fragment's recorded gaps, struck when the rules cover half of it. The fragment is split where a struck word meets one that is not. Each piece keeps its gaps, which the gutter pass reads next.

The model gains `Style.Strike`, which `SameRun` compares. The sink writes `~~` outside a run's other delimiters, with whitespace outside both.

**Bold from `/StemV`** now applies only to a name that states no style. A suffix such as `-Italic`, `-Regular` or `-BoldMT` is a statement, and a stem is an estimate. Chrome writes Georgia-Italic's `/StemV` as 129.9 and Georgia-Bold's as 99.1, so the estimate called the italic bold and the bold not. `/FontWeight` still stands, as a statement.

**A heading over an indented list.** `layout.headsBody` lets a list start up to four of the heading's sizes right of it, since a list is indented under its heading as often as not. A table may not. On the tagged corpus the same allowance for tables promoted three of ISO 32000-2's captions: `Table 136 — …`, set flush above a table set in.

**A header row in a header's style** is written plain, as a heading is: two or more cells, each wholly in one emphasis and all in the same one. GFM's header row is a header already. The test is on the whole row, because row 0 of a table read from its layout is a header by convention only, and its emphasis can be the data's. ISO 32000-2's operator tables set an operator bold and its operands italic in their first row, and mupdf_explored's matrices set their variables italic. Those rows differ from cell to cell and keep their emphasis.

This replaces the earlier `TestCellKeepsEmphasis` rule, "a bold column heading is what these documents draw, so emphasis survives". The gold set, written from intent, says otherwise, and the model-first goal says not to spend tokens restating structure.

## Acceptance

**The gold set.**

| fixture | before | after |
|---|---|---|
| style F1, `strikethrough` | 0.000 | 1.000 |
| style F1, `chrome-article` | 0.571 | 1.000 |
| style F1, `xelatex-article` | 0.667 | 0.800 |
| style F1, mean | 0.648 | 0.960 |
| headings F1, `xelatex-article` | 0.500 | 0.800 |
| headings F1, mean | 0.688 | 0.725 |

`Traffic` above xelatex-article's table is still a bold paragraph, since that table is indented.

**The corpus.** 785 lines changed across 18 documents. Every one was classified:

- **765** are bold dropped from header rows set in a header's style.
- **7** are strikes, every one in ISO 32000-2's errata pages (1005 to 1020). Two were checked against the rendered page: a caption the errata move, and an `s` they delete from "Attributes". The two false strikes seen during the work are gone: the line across WTPDF's measure and the connector through "arrowhead".
- **7** are emphasis changes in tables, all header rows set in one style.
- **2** are headings, xelatex-article's and another above an indented list.
- **1** is italic no longer read as bold italic.
- **3** combine a strike with a header row.

**The heading oracle,** rebuilt for this, scores promoted headings against the tagged corpus's declared ones. It stands at 1,212 promotions with 16 not declared, before and after the change.

**The tests:**
- **`strike_test.go`:** each band bound, thickness, half coverage, an underline, a line past the text either way, the cuts kept, a struck cell in an unruled table, and a rotated line.
- **`TestAStyleInTheNameOutranksTheStem`:** the bold rule.
- **`headed_test.go`:** the list allowance at four sizes, past four sizes, left of the heading, and a table, which may not be indented.
- **Sink:** strike with bold and with code, and header rows in one style, in two styles, with plain text in a cell, with an empty cell, and with a code span.

## Mutation

34 mutants, all killed by named tests. The first run left 6 alive:

- **one equivalent, deleted:** an early return when nothing is struck, which the rebuild makes anyway;
- **five missing cases, now tested:**
  - a struck cell in an unruled table, which needs the cuts both rebased and copied;
  - a rotated line;
  - `SameRun` in `doc`'s own tests, since its only killer was in another package;
  - a family name holding a style word, `RomanFont-PS`.

## Consequences

- Struck text is `~~struck~~`. Underline, colour and size stay dropped.
- A style suffix in a font's name decides its weight over its stem.
- The style target, 0.85, is met. It is enforced when #7 closes, with its output rules.

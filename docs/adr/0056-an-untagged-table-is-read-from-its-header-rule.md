# 56. An untagged table is read from its header rule

Date: 2026-10-28

## Status

Accepted.

## Context

ADR 0055 reads a Word table whose header is ruled cell by cell, but only through the structure tree: it finds the cells as Word's bare `P`s. The same document exported without tags, which Word does when asked, reached the layout path, and the table came out as loose paragraphs:

- "Field Required Description", the header, as one paragraph;
- each body cell a paragraph of its own;
- a row's two short cells, "Yes When the sailing left the pier", run together.

Neither of the layout path's readers sees such a table. The lattice reader (ADR 0037) needs a frame, and the band reader needs vertical rules or gutters between the columns. Word draws neither: only the rule under the header.

And the cells were not separable where the layout path looks. Word places each cell of a line by position, so extract infers the space between two cells as it does between two words, and joins them into one span. A table reader working on spans cannot split a span.

## Decision

**extract splits a ruled table's lines at its columns' borders** (`splitAtHeaders`), as `splitAtRules` splits a line at a drawn vertical. `RuledHeaders` moves from layout to extract, which both layout and sectionize already import, so the rule is read once.

- **The header is the nearest line above the rule, within two of its lines.** Word's cell padding sets it more than a line above: its foot is 11.8 points over the rule in `word-report`. A caption sits further up, and is not split.
- **The table's lines** are the header's and each line below it that the columns hold, every piece it splits into lying within one column's border.
- **The first line the columns cannot hold ends the table,** as a paragraph across the page does, and nothing below it is split. So is a line beside the table, or set sideways across it, left alone.

**layout reads the table from those spans** (`ruledTables`), after the lattice reader and before the band reader:

- **The header** is the spans on that nearest line, in two or more columns.
- **A cell** is a block's spans in one column. A cell that wraps is one cell, as extract joined its lines.
- **A row starts** at a cell whose top is below the row's top by more than half a line, since a row's cells start on one baseline.
- **The table ends** where a row would start more than half as far again below the row above it as the first row starts below the header, the rule ADR 0055 keeps on the tagged path, or at the first span below the rule across its columns.
- **It takes no span** another reader took, none that is blank, none of a block that is no paragraph, and none beside its columns.

The lattice reader's placement step, which puts a table's cells where its first span stood, is shared by both readers.

## Acceptance

- **A fixture, `word-report-untagged`,** is `word-report` exported with no tags, by `word-report.ps1 -Untagged`. It converts to the same gold as the tagged one, and scores 1.000 on every dimension. v1.2.1 scored 0.000 on tables and 0.667 on blocks for it.
- **Old against new** over the 76 PDFs here: only the new fixture changes. No document in the corpus sets a table this way, so the fixture and the unit tests are what hold the rule.

## Mutation

33 mutants cover the extractor's split and the layout reader: the header's reach, the line filter, the fit of a piece in a column, the reader's header, cells, rows, gap rule and limit, its exclusions, and the wiring of each. All 33 are killed, each by a named test.

- **Three guards could not change the output, and were deleted rather than tested:**
  - a skip of lines above the rule after the header, which can be none, since the header is the last line in reach;
  - a first-row test, since a row's top starts above every cell;
  - a sort of each cell's spans into page order, which they are added in.
- **The first test reached too little,** and 21 mutants survived it. Its paragraph after the table was too wide for the first column, so the table ended there as at a span across its columns, and the gap rule never ran. Cases were added for:
  - a gap 1.75 times the pitch, which ends the table, and one 1.35 times, which is a row;
  - a header of one cell, a header alone, a caption out of reach above it and one within reach over the header;
  - a paragraph across the columns, a cell after it, and a second such paragraph;
  - a blank span, a span beside the table, an artifact in a column;
  - row tops two points apart, a wrapped cell first in page order;
  - a grid the lattice reads, its header ruled cell by cell too;
  - a header drawn in two blocks with a paragraph between;
  - in extract, a rule with no header in reach, a line beside the table, a line turned 15 degrees across it, and a space drawn on a border.
- Four mutants did not compile: three were rewritten so that they did, and the fourth mutated one of the guards since deleted.

## Consequences

- A Word table ruled only under its header converts as a table whether or not the PDF is tagged.
- A ruled header with no line in reach above it, or with one column, reads nothing, and the layout path's other readers see the page as before.

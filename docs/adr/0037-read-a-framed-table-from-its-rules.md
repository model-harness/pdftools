# 37. Read a framed table from its rules

Date: 2026-10-20

## Status

Accepted. Part of issue #5, which stays open for unruled tables.

## Context

The band reader in `layout/tables.go` reads a table a baseline at a time. It takes rows as the bands the vertical rules split, and columns from where the cells overlap. Two common shapes defeat it:

- **A cell that wraps** is several baselines, so each line became a row. `v110-changes.pdf`'s table of link behaviours came out as three wrong lines, with the rest as loose paragraphs. chinese-tables.pdf's rating table did the same.
- **A merged cell** is one band that no other row matches. `table-merged.pdf`'s spanning header and its row label fell out of the table, at cell F1 0. That was the spike recorded on #5, which also garbled v110-changes.

The rules say both things. A cell is the area its rules enclose, and a merge is a rule that is not drawn.

## Decision

**`layout/lattice.go`** reads a framed grid from its rules, before the band reader runs on whatever it leaves.

1. **Groups.** The page's rules are grouped where they touch, within a rule's width (1.5pt). A rule with no length is left out, because a point between two tables would make them one group.
2. **The frame.** A group is a lattice when its outer rules run its whole width and height. A booktabs table with no verticals, or a dotted grid, is not framed and is left to the band reader as before.
3. **Grid lines.** These are the lines whose rules cover at least half the frame. That rule does two jobs:
   - it leaves out the boxes v110-changes draws around inline code, which touch each other and the table and would otherwise add columns;
   - it keeps table-merged's column line, which its spanning header interrupts.
   A frame under four rules' widths across can lose its own lines here and is dropped. Those are small drawn shapes: 76 on disk, 44 of them in ISO 32000-2's figures.
4. **Merges.** Neighbouring grid cells merge where no rule divides them across the middle of their shared side.
5. **Spans.** A span belongs to the grid cell its centre is in, and its text goes to its region:
   - **across columns:** to the first column, since GFM has no spans;
   - **down rows:** to the row whose other cells share its baseline, otherwise to the first row. table-merged's `Product` sits under an empty cell with no rule between them, and keeps its own row. Its centred `Kettles` aligns with neither of its two rows, and goes to the first.
6. **Rows.** A grid row splits at its baselines only where every column with text has a line on each of them and on no others. That is a framed table with no rules between its body rows, such as table-across-pages' months. A cell that wraps where its neighbours do not stays one cell. So do two wrapped cells whose lines happen to align, and raised ordinals like `1st`.
7. **Cells.** A cell's spans keep the page's order. Spans that were neighbours in their block stay joined as extract joined them. Any others, a cell's lines that another cell's text came between, are joined by `extract.WrapSpace`.
8. **Minimum.** A lattice needs two rows with text.

The cells go where the table's first span was. What a block held before and after its table spans stays on either side of them. A span already taken by one lattice is not taken by a nested one.

## Acceptance

**The gold set.** `table-merged` went from cells 0.000 and blocks 0.000 to 1.000 and 1.000, and its output equals its gold. `table`, `table-across-pages` and the two articles still equal theirs; table-across-pages' join across a page still holds. The bench mean for cells rose from 0.625 to 0.750, and for blocks from 0.729 to 0.762.

**The corpus.** Four documents changed, and every change was read:

- **chinese-tables.pdf:** its rating table of four rows is now one table, each cell whole. It had been one table row plus 25 loose paragraphs.
- **v110-changes.pdf:** its two-page table of link behaviours is one four-column table, joined across the page, its multi-line cells whole. It had been 3 table lines and 30 loose ones.
- **autotagPDFInput.pdf:** its 14-column sales table gains its header row with the spanning quarters. Its `Complex Table` heading is no longer promoted, because the block after it is now the caption `Sales Results for 2015` rather than the header text the old reading merged into a paragraph. ADR 0031's rule does not look past a caption.
- **table-merged.pdf.**

Over the 51 untagged PDFs on disk, the pass finds 14 tables, 86 rows and 399 cells. The band reader alone finds 13, 76 and 352. `TestBlockLinesDescribeTheirSpans` holds every cell's lines to its spans.

**The tests.** `lattice_test.go` covers:

- a framed grid;
- each direction of merge, and a row label that shares its row's baseline;
- a wrapped cell, including a hyphen held across the wrap;
- rows split at shared baselines; wraps in two of three columns, and in one column, that are not split; rows given bottom-up;
- each border with a long gap, a short gap within tolerance, and a frame narrowed by a missing border;
- word boxes, strokes that overlap, a drawn shape, and a point between two tables;
- one row, and a row of spaces;
- stacked tables on one line, and a table nested in another's cell;
- the text around a table;
- furniture and boxless spans inside a frame;
- the tolerance that merges lines.

## Mutation

40 mutants, all killed by named tests. The first run left 18 alive.

- **Three were code that could not change any result, and were deleted:**
  - a column-count check a later one repeats;
  - a search for the first span taken, which is already the first;
  - the length filter. It was deleted, then restored when its deletion was measured.
- **The other fifteen were missing cases, now tested.**

The length filter is the lesson. Deleting it, together with a row-count check judged redundant, panicked on ISO 32000-2's drawn shapes. The panic came from the row check. The length filter had only been credited with it, and is pinned now by the point between two tables.

## Consequences

- A framed table is read as its rules draw it: merged cells, multi-line cells, and rule-less body rows.
- A merged cell's text is in its first cell, and the cells it covers are empty. GFM cannot say more, and the gold files follow the same rule.
- Unruled tables are still not read; they are #5's last part. So are framed tables whose row lines cover less than half the width, which merge into the rows around them.

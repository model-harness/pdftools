# 38. Find an unruled table by its gutters

Date: 2026-10-20

## Status

Accepted. Closes issue #5.

## Context

A table with no rules holds its columns apart by white space alone. The band reader reads a table from the rules that split its rows, so it saw nothing in `table-unruled.pdf`: the table came out as one paragraph, at cell F1 0. Real files set tables this way too: LightOnOCR's results tables, `small-table.pdf`'s boiling points, and the transform matrices in `mupdf_explored.pdf`.

`layout.Tables` records why a gap threshold cannot find these. A cell gap and a word space are the same measurement, continuous from 0.4 space widths to over a thousand. A gap that recurs at one x in every line of a run is different evidence. Prose puts its word spaces wherever its words end, so they line up only by chance.

## Decision

**`extract.splitAtGutters`** runs after `splitAtRules`, while each fragment's gaps are still recorded with their positions.

1. **A run** is at least three lines going down the page, each with two or more gaps two space widths wide.
2. **Its gutters** are the stretches two space widths wide that no more than one of its lines inks. The one allowed line is a header spanning two columns.
3. **A run is a table** when it has two or more gutters, and no cell is wider than 40% of the run.
4. **Its lines are split** at the gutters by `splitFrag`, as at a rule, and the gutters are recorded as verticals in the new `doc.Page.Gutters`.

**What it leaves alone:**
- **Two columns:** a list of terms and their values is as often two columns as a table is, and so is a two-column page drawn line by line.
- **A column of prose:** that is the 40% cell limit.
- **A first column of list markers:** that is a list, which Lists reads. The new `doc.IsMarker` asks whether text is a marker standing alone. `ListMarker` needs text after the marker, so the first draft's list test could never fire.
- **Monospaced lines:** in code, alignment is syntax.
- **Dot leaders:** a table of contents' leaders line up from line to line. mupdf_explored's contents pages were 36 such runs in the first spike.
- **Lines a drawn rule has already split:** those are the rules' table.

**`doc.Page.Gutters`** is kept apart from `Rules`, because a rule is the producer's own statement and a gutter is a reading of the page. The band reader takes both. The lattice (ADR 0037), which needs a drawn frame, takes only `Rules`.

## Acceptance

**The gold set.** `table-unruled` went from cells 0.000 and blocks 0.000 to 1.000 and 1.000, and its output equals its gold. The bench mean for cells rose from 0.750 to 0.875, past v1's 0.85, so that target is now enforced. Blocks rose from 0.762 to 0.795.

**The corpus.** Five documents changed. Every change was read:

| document | what changed |
|---|---|
| `small-table.pdf` | its boiling points are a table, header and four rows; they had been five bold-led paragraphs |
| `mupdf_explored.pdf` | its two transform matrices are 3×3 tables |
| `dotted-gridlines.pdf` | two broken tables, where the dotted rules did not split every row, are one table of twelve columns, totals included |
| LightOnOCR | two results tables are tables |
| `table-unruled.pdf` | the fixture |

LightOnOCR keeps two faults:

- `151k (Base)` crosses a gutter, so the band reader merges columns at that row, and the table starts a row later. Its first row stays a paragraph above it.
- Where one row crosses a gutter, two of its values share a cell.

Over the 51 untagged PDFs on disk, tables rose from 14 to 19, rows from 86 to 113, and cells from 399 to 573, from 77 gutters.

**False positives.** The spike's runs over the tagged standards contain no prose: they are operator lists, matrices, colour-space tables and annotation tables. md does not reach them, since those files have an outline.

**The tests.** `gutters_test.go` covers:

- the fixture's shape, and the gutters' positions;
- each condition taken away: two rows, two columns, a cell as wide as prose, a list, code, dot leaders, a row with one gap, a prose line with word-width gaps, and one gutter with second gaps that do not align;
- one spanning row against two, and a spanning header overprinted;
- the two-space bound on each side;
- a gutter narrower than two spaces where wide gaps overlap;
- cells in different styles, and a wide cell of two styles;
- a line stepping back up the page;
- a ruled table.

`TestTablesReadsGutters` holds the band reader to gutters, and checks that it does not write into the page's rules. `TestIsMarker` covers the new predicate.

## Mutation

33 mutants, all killed by named tests. The first run left 13 alive.

- **Two were code that could not change any result, and were deleted:**
  - a test that a gutter closes before the run's last ink. A gutter closes where a cell starts, which is always before it.
  - a tie-break for edges at one x, which decides nothing but a zero-width gutter.
- **One was a real fault:** the list exclusion could never fire, now fixed with `doc.IsMarker`.
- **The rest were missing cases, now tested.**

## Consequences

- An unruled table of three or more columns, with short cells and a full row of cells on every line, is a table.
- A row with an empty cell has fewer gaps and ends a run, so a sparse table splits at it. Two-column tables are not found.
- A spanning cell in one row is tolerated, but the band reader merges columns there, so the table can lose that row.
- #5's v1 target is met and enforced.

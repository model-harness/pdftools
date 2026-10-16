# 32. Join a table a page break split

Date: 2026-10-16

## Status

Accepted. Part of issue #5, which stays open for merged cells and unruled tables.

## Context

`layout.Tables` finds a table one page at a time, from the page's strokes. A table that runs onto the next page and repeats its header row there therefore came out as two tables, with the header twice. On `table-across-pages.pdf`, table cell F1 was 0.632.

## Decision

After every page's tables are found, `joinAcrossPages` joins a page's opening table to the previous page's closing table when all of these hold:

- nothing but artifacts stands between them;
- the second's header row repeats the first's, cell for cell;
- both have the same width.

**What happens to the continuation.** Its repeated header row is dropped. Its other rows are renumbered to follow the first table's. Its cells move to the page the table began on, so the sink, which gathers a table page by page, emits one grid. Each span keeps its own `Page`, which is how the model already carries a block across a break. A table that runs over three pages stays open through a page that holds nothing but the table.

**A table that runs on with no repeated header stays two tables.** Two adjacent tables of one width are common, and a repeated header is the only statement that they are one.

Only whole-document output runs this pass. `md -split` is page-scoped and never infers roles.

## Acceptance

`table-across-pages`: cells 0.632 → 1.000, blocks 0.000 → 1.000, CER 0.149 → 0.000. Its floor is raised.

**The corpus.** No other document of the 70 on disk changes, because no untagged one has a ruled table repeated across a break. Tagged files take the outline path.

**The tests.** `join_test.go` covers each condition:

- a repeated header joins, and so does one past an artifact;
- a different header does not join, nor a different width, nor prose before the table;
- a table over three pages becomes one;
- the join needs a break the table touches, so prose after a continuation, or a page of prose between, keeps the next table its own.

## Mutation

11 mutants. All 11 are killed, each by a named test.

The first run left the width check alive, because the test's wider table also had a different header. The test now repeats the header over a third column.

**The empty-header check was deleted, then restored.** It was first taken for code that changed nothing, on the argument that `assign` numbers every table it builds from row 0.

That argument was incomplete. `TestBlockMCIDsAreTheUnionOfTheirSpans` runs the layout passes after sectionize on the tagged corpus, and its population floor fell. Looking into why showed that sectionize builds cells too. A declared table that spans three pages is numbered once and has no row 0 after its first page. So its second and third pages' empty headers read as equal, and the table joins itself, with its rows renumbered.

The check is back, with that reason in its comment. `TestADeclaredTableIsLeftAsItIs` and `TestTwoHeaderlessPartsDoNotJoin` now hold it. A same-number guard added alongside it was equivalent and is removed.

The floor fell for a legitimate reason: the join merges 88 header cells of tables that are tagged separately on each page with their headers repeated. That walk counts 71,971 blocks, against 72,059 without the join, and its floor is restated.

## Consequences

- A long table in an untagged file is one GFM table, its header once.
- Running headers that are not tagged as artifacts still stand between a table's halves. Removing those is #6's.

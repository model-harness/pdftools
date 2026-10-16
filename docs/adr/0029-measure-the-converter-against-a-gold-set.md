# 29. Measure the converter against a gold set, with floors and v1 targets

Date: 2026-10-16

## Status

Accepted. Issue #2, the first goal under the project index, #1.

## Context

The project's goal is a converter whose Markdown says what the PDF says, and v1 is when it
does that well enough. Nothing measured "well enough".

`testdata/reference` held twelve LaTeX fixtures, each with gold Markdown written from the
source's intent and never from a reader's output. Two tests read them:

- `TestReferenceFidelity` checks that the words survive in order;
- `TestReferenceExactMatch` checks which fixtures match byte for byte.

Both are pass or fail. Neither says how far a fixture is from right, or on what. The
twelve all came from pdfTeX, apart from the tagged ones from LuaTeX. None had columns,
unnumbered headings, an unruled or merged table, a footnote, a running header, a
hyphenated word, strikethrough, an unmapped font or a scan, which are the cases v1 has to
handle.

## Decision

### A scorer, `bench`

`bench.Score(ref, got)` parses gold and output with goldmark, GFM and footnotes, and scores
each dimension apart from the others:

| dimension | what it measures |
|---|---|
| CER | character edits per reference character, once the output's blocks are put back in the reference's order |
| recall, precision | the overlap of word bags, case-sensitive |
| order | whether matched blocks keep the reference's order |
| blocks | F1 of blocks matched with a similarity of 0.9 or more |
| headings | F1 on level and text |
| tables | F1 on each cell's row, column and text, inside the table it matched |
| style | F1 on bold, italic, code and strikethrough spans |

It also counts the `[err]` and `[image]` marks.

**Block matching.** Each reference block takes the unused output block most similar to
it, over characters, case folded. A table takes only a table, and anything else only a
non-table. A match needs a similarity of 0.5. To keep the cost down, the comparison runs
only on the three candidates with the closest word bags.

**Why CER is taken over re-sequenced blocks.** A merge, a split or a reordering is a
structure defect, and Blocks and Order score it. Scoring each matched pair alone had
charged a merged paragraph twice: four one-line paragraphs merged into one scored a CER
of 2.0, with every word right.

**Cost.** The edit distance runs over the whole text, so its cost is the product of the
two lengths. A fixture is a page or two.

`pdfspec bench <dir>` scores each `X.pdf` against its `X.gold.md`. It converts through
`writeMarkdown`, the same function `pdfspec md` uses, so the two cannot drift apart.

**goldmark** (v1.8.6, MIT) parses Markdown and nothing else. It is a single-purpose
library, so under the borrowing rule it stays borrowed.

### Seventeen fixtures

Each covers one concern. Each gold file is written from the source and checked word for
word against it.

| kind | fixtures |
|---|---|
| reading order | `two-column`, `three-column` |
| headings | `unnumbered-headings` |
| tables | `table-unruled`, `table-merged`, `table-across-pages` |
| furniture and flow | `footnotes`, `running-headers`, `hyphenation` |
| style | `strikethrough` |
| glyphs | `type3-unmapped` |
| producers | `xelatex-article` (XeTeX, Identity-H CID fonts), `chrome-article` (Chrome, Skia, tagged) |
| scans | `scan-paragraphs`, `scan-table`, `scan-two-column`, `scan-unnumbered-headings` |

**How the scans are made.** Each is its source rendered at 200 dpi, then passed through
`degrade.go`: grey, a 0.6° skew, a paper tone, seeded noise, and JPEG at quality 60. The
result is embedded as `/DCTDecode`, which is what scanners write. A rebuild writes the same
bytes. A scan's gold file is its source's.

**Images and charts are not here.** A description has no single right string, so they need
their own measure, which belongs to #11.

The PDFs and JPEGs are pinned in `manifest.json`. `.tex`, `.html` and `degrade.go` join
`.gold.md` as exempt sources.

### Floors, and targets

**Floors.** `TestBenchmarkFloors` holds every fixture to its measured score on each
dimension. Floors only go up: a change that improves a fixture raises its floor in the same
commit.

**Targets.** `TestBenchmarkTargets` compares the means with v1's targets. A dimension's
target fails the build from the commit that closes its goal. Until then, it reports the
gap.

| dimension | baseline mean | v1 target | goal |
|---|---|---|---|
| CER | 0.184 | ≤ 0.03 | #8, #9, #12 |
| recall | 0.822 | ≥ 0.97 | #8, #9, #12 |
| precision | 0.811 | ≥ 0.97 | #6 |
| order | 1.000 | ≥ 0.98 | #3 |
| blocks | 0.615 | ≥ 0.90 | #3, #6 |
| headings | 0.375 | ≥ 0.90 | #4 |
| tables | 0.579 | ≥ 0.85 | #5 |
| style | 0.629 | ≥ 0.85 | #7 |

**The targets are not 1.** v1 ships with `[err]` marks where text can't be read, rather
than chasing every glyph, so each target leaves room for a fixture or two to stay
imperfect. The CER target is the clearest case:
- `type3-unmapped` alone, left all `[err]`, would add 0.034 to the mean;
- four scans read at Tesseract's measured median of 3.2% would add 0.004.

The 0.03 target therefore holds only once the text that *is* readable comes out right.

## Acceptance

**The spike** ran on the original twelve fixtures:
- gold against itself scored 1 everywhere;
- the converter scored 1 on eleven;
- `text-styles` scored 0 on blocks alone, from its known merged paragraphs (DESIGN.md §10).

A planted defect of each kind lowered only its own metric.

**The baseline**, every failure on an open goal:

| fixtures | what fails | goal |
|---|---|---|
| the four scans, `type3-unmapped` | no text: CER 1.0 | #8, #9, #12 |
| `two-column`, `three-column` | the spanning title is not a heading; paragraphs split at column breaks | #3, #4 |
| `unnumbered-headings`, `xelatex-article` | headings come out as bold paragraphs | #4 |
| `table-unruled`, `table-merged`, `table-across-pages` | cell F1 of 0 to 0.632 | #5 |
| `running-headers`, `footnotes`, `hyphenation` | furniture kept, footnotes inline, hyphens kept | #6 |
| `strikethrough`, `chrome-article` | style F1 of 0 and 0.571 | #7 |

**The tests.** `bench_test.go` pins each metric with a constructed case:
- a swap, a demotion, a changed cell, dropped emphasis, a typo, a merge and a split;
- the similarity bounds at exactly 0.5 and 0.9;
- the three-candidate shortlist;
- case, a soft line break, repeated paragraphs, and style in list items and table cells;
- a table emitted as paragraphs, and an unmatched table.

`TestBenchMeansLeaveOutVacuousFixtures` pins the mean row. A fixture with no headings
scores a vacuous 1 on headings, and is left out of that mean.

## Mutation

30 mutants were run against the final code. 27 are killed, each by a named test. Three are
equivalent:

- `order`'s `>` against `>=`: no output block is matched twice, so the indices never tie.
- Bold and italic's labels swapped: both sides are parsed by the same code, so the keys swap
  together.
- Jaccard's denominator without the intersection: `i/(a+b)` and `i/(a+b−i)` rank candidates
  alike, since one is a rising function of the other.

**The first run left twelve mutants alive that were real gaps.** A thirteenth came up once
its first version built: an unmatched table borrowing credit for the reference's header
cells. Each now has a test.

**It also found three pieces of code that changed nothing, and they are deleted:**
- a list item's `item` kind, which no score read;
- a check that a reference table's match is a table, which no lookup could see;
- `benchRows` skipping gold names with a second dot, which skipping a missing PDF already
  does.

**Review of the subagent's code found three defects:**
- the double-charged merge above;
- the walk skipping list items' and table cells' children, so their bold was never scored;
- a table emitted as paragraphs counting as an intact block.

The first was fixed before the floors were taken. The other two came after, and moved two
floors down and nothing else: `chrome-article`'s style from 0.800 to 0.571, and
`table-unruled`'s blocks from 1.000 to 0.000.

Three mutants first failed to build, each from a variable left unused. Each was respecified
and run again.

## Findings, documented and not followed

- **The order score is uninformative so far.** The converter keeps every fixture's blocks
  in order. A scan has fewer than two matched blocks, so its order is a vacuous 1. Order
  starts measuring when a fixture moves a block.
- **`fetch.ps1` reports five mismatches on this checkout.** They are `ocr-ed.txt` and four
  `.dt` files. `core.autocrlf=true` writes them with CRLF, while the committed blobs match
  the manifest. A `.gitattributes` entry marking fixture text `-text` would stop it. This
  predates the change.
- **XeTeX's "ff" ligature comes out as U+FB00**, the single ligature character, where the
  text is two letters. That belongs to #8.
- **Chrome's `<em>` comes out bold-italic, and its header cells gain `**`.** That belongs
  to #7.

## Consequences

- A change to extraction, layout or the sink now moves a number. A change that makes any
  fixture worse fails the build.
- Each goal under #1 is closed by moving its dimension to its target and enforcing it.
- The gold set grows inside each goal: a goal that finds a case adds the fixture that pins
  it.

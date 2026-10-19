# 34. Join a paragraph that a page break split

Date: 2026-10-19

## Status

Accepted. Part of issue #6, which stays open for hyphenation and footnotes.

## Context

Once ADR 0033 removed the running headers, `running-headers.pdf` still scored blocks 0.500. The page break cut its third paragraph in two, and each page's half came out as a paragraph of its own. Extract cannot join them. It extracts a page alone, and on an untagged page the running header stands between the halves until `layout.Furniture` has marked it. Real files show the same split: LightOnOCR's paper, and nearly every page of `mupdf_explored.pdf`.

## Decision

**`layout.PageBreaks`** runs last among the untagged passes. It asks ADR 0030's column-break questions at a page break, of the last body paragraph before the break and the first after it, skipping artifacts and blocks that drew only spaces. The two halves join when all of these hold:

- the first block of the new page starts flush with its page's text, at the same size;
- the previous page's paragraph ends at the foot of its page's text;
- and either the new block starts lower case, or the previous paragraph's last line runs to its page's right edge and the new block starts in the face that line ends in.

The face condition is for a running header that `Furniture` cannot see, because no other page repeats it. `mupdf_explored.pdf` has one page of chapter 27. Its header is set flush, at the body's size, in italic, after a page that ends on a full line.

The second half's spans join the first's block, each keeping its own `Page`. A page that holds nothing but the continuation passes the paragraph on to the next page, so a paragraph across three pages is one block.

**The break is a wrap.** `extract.WrapSpace`, the rule `appendLine` applies inside a page, is exported and decides the space at the join. The space trails the first half, and a hyphen holding a word gets none. `re-quest` joins as `re-quest`, as `re-quired` does within a page. Before this, the join wrote `re- quest`.

**A continuation ends at its paragraph's indent.** A continuation opens flush, so extract's own-indent rule never fires in it, and the next paragraph on that page ran on into it. `splitAtIndent` cuts the continuation before the first line that repeats the indent the joined paragraph opened with on the page before. It applies only while every earlier line is flush, within half a space, and only when that opening indent is one extract counts: its continuation lines agree on a margin, and the indent is between `IndentFrac` and `IndentMax` space widths.

**Lines.** A span runs on across lines whenever its style does, so the spans cannot say where a line ended. `doc.Block` therefore gains `Lines`, which extract fills: each line's box, page, dominant size, space width and the span and byte offset where its text starts. Indents and line ends are measured in the line's space width, the unit extract's tolerances are in. A first draft used a quarter of the type size, which put LaTeX's 11pt `\parindent` past `IndentMax`.

**Furniture reaches the outermost block.** `mupdf_explored.pdf` is set on Letter paper. Its running headers sit 98 to 108pt below the top, 12.4% to 13.6% of the page, so ADR 0033's 12% band missed them. `PageBreaks` then joined each header into the paragraph before it. A block past the band is now furniture when all of these hold:

- it is its page's outermost text on that side;
- it lies within 15% of the page;
- it carries a folio, meaning its text recurs with numbers that change from page to page.

| variant | true | false | missed | untagged marked |
|---|---|---|---|---|
| band 12% (ADR 0033) | 3538 | 11 | 34 | 31 |
| band 15% | 3538 | 410 | 34 | 309 |
| **12%, outermost within 15%** | **3538** | **11** | **34** | **309** |

The measurement is ADR 0033's: the tagged corpus extracted with `KeepArtifacts`, where the producer's furniture is the ground truth. All 278 newly marked untagged blocks are `mupdf_explored.pdf`'s running headers, `CONTENTS` headers and chapter-opening folios.

The folio condition came from a second measurement. With a tagged producer's furniture already dropped by extract, the outermost text on a page is body text. Without the condition, the reach marked the header row that ISO 32000-2 and ISO/TS 32005 repeat at the top of each page a table continues on: 294 blocks. With it, the reach marks none.

## Acceptance

**`running-headers.pdf`** went from blocks 0.500 to 1.000, and its output now equals its gold. The bench mean for blocks rose from 0.698 to 0.714, and no other fixture moved.

**The corpus.** Three documents changed in `md`'s output:

- the fixture;
- LightOnOCR, with two joins, both correct;
- `mupdf_explored.pdf`: its 278 headers and folios are gone, and 45 paragraphs and code listings are joined across a page.

Every one of the 48 joins was read. Two of them merge a command name into its usage text, in a book whose listings already come out as paragraphs within a page.

Three defects were found in reading and fixed before acceptance:

- **A caption joined across a page.** The full-line test measured the right edge from the paragraph's own lines, and `autotagPDFInput.pdf`'s caption opens with a line that is a lone space. That made its last line the widest, so it counted as full, and `Figure 2` joined `Figure 1`. The edge is now the page's.
- **A hyphen got a space.** The join wrote `re- quest`, where extract's wrap rule keeps `re-quest`.
- **A blank block blocked joins.** A block that drew only a space glyph below the text counted as the page's foot, so no paragraph on that page could carry over.

**The tagged corpus as an oracle.** The layout passes run over tagged pages in `cmd/pdfspec`'s tests, and `md` runs them on a tagged file that has no outline. On those pages `PageBreaks` makes 146 joins. The structure tree puts both halves in one element for only 4 of them, but the tree is the wrong oracle here. ISO 32000-2 opens a new P or Span at every page break, mid-sentence ("…to the" | "document and…"), so the text was read instead. 3 joins are wrong: a row of a parameter table, keyed by a lower-case name, carried into the next page's first row through the full-line branch.

A guard was tried against those 3: a lower-case start does not count after a full stop. It changed none of the 146, since the rows pass by the full-line branch, so it was not kept. The tree shows 2 continuations the pass misses, a Code element and an LBody. The block walk in `TestBlockMCIDsAreTheUnionOfTheirSpans` fell by 142, from 71971 to 71829. That is the net of the joins and the cuts, and the reach removes none of it.

**The tests.** `breaks_test.go` covers:

- the join itself, with spans, lines, pages and MCIDs;
- each question, at its bound and just past it: the size, the indent, the foot, the line's end, the face and the line count;
- artifacts and blank blocks on both sides;
- three pages, and a page with no body between two;
- the hyphen;
- the cut at an indent, each of its bounds, and an indent that opens a span.

`furniture_test.go` covers the reach at both edges, text beyond the block or level with it, a blank block, the same text on every page, and the same text in capitals. `TestLinesRecordWhereEachLineStarts` pins what extract records.

## Mutation

76 mutants across `breaks.go`, `furniture.go` and extract's line recording and `WrapSpace`, all killed by named tests in `layout` and `extract`, which need no corpus. The first run left 9 alive, and each was a missing case:

- a blank block after the paragraph;
- blank text and a list item left of the margin;
- a continuation opening with a space;
- repeated text at the foot within the reach;
- a margin that wanders within tolerance near the indent bound;
- a cut-off paragraph whose MCIDs differ from the whole;
- a line built from two fragments in one style, which only an artifact beside body text produces.

The ninth was a guard that could not fire. It checked for an empty first half, which a continuation cannot have. It now checks for a first half that drew only spaces, which one can.

Some guards could not be killed, because the code around them already made them hold, so they were removed rather than tested:
- the requirement that the two pages be consecutive. Every page either resets the paragraph or carries it on.
- `splitAtIndent`'s line-count and space checks. `continuesOver` establishes both.

## Consequences

- Untagged paragraphs survive a page break, with their spans' pages intact, so a consumer can still say which page each part was drawn on.
- `doc.Block.Lines` is new model surface. OCR and hand-built blocks have none, and `PageBreaks` leaves them alone.
- `extract.WrapSpace` is the one statement of the wrap rule, for both a line break and a page break.
- A table whose rows are set as paragraphs can be joined across a page when its last column runs to the margin. On disk, that happens only on tagged pages.

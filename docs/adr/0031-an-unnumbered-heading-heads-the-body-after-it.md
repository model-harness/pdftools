# 31. An unnumbered heading heads the body after it

Date: 2026-10-16

## Status

Accepted. Issue #4, under the project index, #1. Closes the limit ADR 0008 recorded and ADR 0013 kept open.

## Context

On an untagged file, `layout.Headings` promoted a block to a heading only where the document numbered it. "Getting Started", "Preface" and "Acknowledgments" stayed bold paragraphs. The benchmark (ADR 0029) measured that as headings F1 of 0 on `unnumbered-headings`, `xelatex-article` and `two-column`.

The limit was kept deliberately: nothing in a candidate separates "Preface" from a bold table row. Two signals were measured and rejected:

- **Recurrence:** real titles occur once, and author lines repeat.
- **A size-ratio threshold:** precision peaks at 73.2%, and the distribution has no gap to put a threshold in.

Both look at the candidate itself.

## Decision

An unnumbered candidate is a heading when **the block after it is the body it heads**:

- a paragraph of at least `headedRunes` (40) characters;
- set lighter than the candidate: smaller by more than `headedSize` (0.25pt), or the same size with the candidate bold and it not;
- starting within `headedAlign` (3pt) of the candidate's left edge;
- on the same page.

**The block may also be a list item or a table cell starting at the same edge.** The walk to it may pass over at most `headedSkip` (4) numbered headings, since "7.3.4 String objects" is followed at once by "7.3.4.1 General". An unnumbered candidate on the way ends the walk: a run of them is a table header or a title page.

**The candidate must read as a title.** It needs more letters than digits, and no list marker or ordered label at its start, since `Lists` and `OrderedLists` claim those after this pass.

**Its level comes from the numbered headings set in its style**, the commonest of their levels and the lower on a tie, so a "Foreword" set like "1 Scope" is level 1. A style no numbered heading uses is ranked among the rest: larger first, and bold before regular at one size.

**Numbered headings are unchanged.** They are promoted at the level their number states.

A table header row is followed by another row, a masthead by an author line, a "Chapter 1" label by the chapter's title, and a figure title by its figure. None of those is a long paragraph starting at the candidate's own edge.

## The spike

The spike measured against the producers' declarations in the corpus's 14 tagged files. It ran the untagged path's gate on each file and counted the candidates whose text the producer declares a heading.

| rule, on unnumbered candidates | true | false | precision |
|---|---|---|---|
| promote every candidate | 96 | 732 | 0.116 |
| the body after it, at its edge | 64 | 6 | 0.914 |
| the same, also centred | 73 | 64 | 0.533 |
| a list or table up to 40pt right of it | 65 | 12 | 0.844 |

**Centred headings were rejected.** A centred figure caption is centred over text too, and admitting centred headings admitted every centred caption in ISO 32000-2.

**Lists and tables indented under a heading were rejected.** They added one heading and six table captions.

Run after `layout.Tables`, as the pipeline runs, the table header rows the rule first admitted are table cells, and drop out. Of the six that remain, four are arguably headings that their producer did not tag: "Table of Contents", two "Algorithm:" titles, and an errata entry whose siblings are declared. The letters-over-digits and list-label conditions cost no declared heading.

## Acceptance

| fixture | headings before | headings after | style before | style after |
|---|---|---|---|---|
| `unnumbered-headings` | 0 | 1.000 | 0 | 1.000 |
| `two-column` | 0 | 1.000 | 0 | 1.000 |
| `xelatex-article` | 0 | 0.500 | 0.571 | 0.667 |

Style rises because a heading's bold is its role and is no longer emitted as `**`. The mean headings F1 went from 0.375 to 0.688. The floors are raised.

**The corpus.** Only untagged documents changed: three fixtures and five real files, with 71 new headings in the five.

- **The arXiv paper:** "Acknowledgments" and appendices A to E.
- **Adobe's accessibility demo:** its whole three-level outline.
- **`mupdf_explored.pdf`:** 45 chapter titles.
- **Five false:** table-of-contents part entries in `mupdf_explored.pdf`, such as "I The MuPDF C API 5", each ending in a page number.

Its OKF bundle grew from 302 concept documents to 352. Tagged files are untouched, since they take the outline path.

**The tests.** `headed_test.go` pins each condition at its bound:

- 39 and 40 characters;
- 3pt and 3.5pt of alignment;
- smaller by 0.3pt and by 0.2pt;
- the same size, bold above regular and bold above bold;
- four numbered headings passed over and five;
- a list, a misaligned list, and a table;
- the next page;
- an artifact passed over;
- digits against letters, including a tie;
- a list label;
- the follower's dominant size on a tie, and half bold.

On levels, it pins a numbered style's level where the rank would say otherwise, a tie between two levels, and bold against regular at one size. `TestHeadingsRejectsBareAnnexLetter` now holds that a bare letter is still no number, while "A Vocabulary Pruning" is a heading by what follows it.

## Mutation

26 mutants were run against the final code, and all 26 are killed, each by a named test. The first run left six alive, and each was a real gap: the skip bound read from the constant itself, a letters-to-digits tie, bold against regular at one size, a style shared with numbered headings at a different level, a tie between two levels, a follower half bold, a follower split between two sizes, and an artifact passed over. Two mutants that left a variable unused were respecified so they build.

## Findings, documented and not followed

- **Table-of-contents entries.** A part entry above a block of contents lines is promoted, as five are in `mupdf_explored.pdf`. A trailing page number would mark them, but headings can end in numbers too, and the case is small.
- **A heading over an indented list or table** stays a paragraph, as `xelatex-article`'s "Changes" and "Traffic" do. Allowing for the indent cost precision from 0.914 to 0.844.
- **The headings target needs the scans read.** The four scan fixtures have no text until #9 and #12, so the mean cannot reach 0.90 before then. The target is checked at v1, #13.

## Consequences

- Untagged files get unnumbered headings, and so do their OKF bundles.
- The package comment in `layout` and DESIGN.md §10 keep the rejected signals, and now record the one that worked.

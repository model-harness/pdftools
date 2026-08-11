# 13. Rank untagged annex headings by their lettered number

Date: 2026-08-11

## Status

Accepted

## Context

ADR 0008 decided that on the untagged path typography gates a heading candidate and the
document's own section number assigns its level, and it recorded one limit as measured
debt: an *unnumbered* candidate stays a paragraph. It also said how it expected the limit
to be lifted:

> Lifting it needs a document-level pass that sees the *sequence* — an A.1 after an A is
> evidence for an annex scheme where a lone "A" is not, and a size that heads a section on
> forty pages is evidence where one occurrence is not.

Half of that is wrong, and the other half turned out not to be needed. Both halves were
measurable, so they were measured before anything was designed.

**The tagged corpus is ground truth for an untagged rule.** A structure tree declares which
text is a heading and at what rank; joining those declarations to the typographic gate's
own candidate set on `{page, MCID}` answers what no untagged fixture can — which of the
gate's clauses predicts heading-ness, and what level a producer assigns a given number
shape. Over the 11 tagged ISO documents plus `Well-Tagged-PDF-WTPDF-1.0.pdf` and
`PDF-Declarations.pdf`:

| candidate population | declared a heading | declared something else |
|---|---|---|
| unnumbered, strictly above body size | **188** | 110 |
| unnumbered, bold at body size | **6** | 647 |

The second row is why the rule below only considers above-body candidates in the first
place: 552 of those 647 are ISO 32000-2 table header rows, which is `dotted-gridlines.pdf`'s
bind at corpus scale.

**The recurrence hypothesis is false in both directions.** Of the 151 unnumbered
above-body candidate *styles* on the untagged path, 9 occur exactly once and those include
genuine titles — `chinese-tables.pdf`'s "第七章 企业资信状况" and "MARKET SUMMARY & PLAN" —
while the most-repeated styles include `mupdf_explored.pdf`'s "Robin Watts" and
"September 5, 2022" at 9 occurrences across 7 pages. Rank and repetition are independent;
a document-level pass over the sequence buys nothing here. Bookmarks were checked as a
second, independent producer declaration and are too sparse to build on: most untagged
fixtures have none, and `mupdf_explored.pdf`'s own outline omits "Contents", "Part I" and
its chapter labels.

**No size ratio has a gap to put a threshold in.** Over the 287 tagged unnumbered
above-body candidates, precision against the declaration peaks at 73.2% around 1.17× body
and falls to 6% by 1.63×: a 3.4× masthead is not a heading and a 1.08× subclause is.

What the measurement did surface is that the missing population is not unnumbered at all.
It is **numbered with a letter** — appendix and annex numbering, which ADR 0008's
dotted-decimal `numberedLevel` cannot parse because its first component is not a digit:

| shape | declared a heading | declared something else |
|---|---|---|
| dotted lettered ("A.1", "B.2.3") | **112** | 0 |
| bare letter ("A Vocabulary Pruning") | 0 | 1 |

## Decision

**A dotted lettered number is a section number, and its letter is its first component.**
`annexLevel` reads it, `Headings` falls back to it when `numberedLevel` declines, and
"B.2.3" is level three for the same reason "4.2.3" is — the document says so.

The bare letter stays deferred, and the split is the point. "A" is also a word, so
admitting a single letter and a space admits every line that starts with one; "A.1" cannot
be a sentence, so it needs none of the sequence evidence ADR 0008 wanted for the bare form.
The one bare-letter candidate on disk is `PDF-Declarations.pdf`'s cover line "A use of ISO
32000", which the producer does not declare a heading — so promoting the arXiv paper's real
"A Vocabulary Pruning" means promoting that, and nothing in either block separates them.

Two guards are decided by construction rather than by measurement, and both are recorded
that way in the code because the corpus cannot separate them:

- **The dot must precede the digits.** Requiring it admits exactly the same 112 as not
  requiring it, so no fixture chooses. It stays because it excludes "A4 paper size" by
  shape rather than by luck.
- **The letter is single, and a trailing dot on the number is a style rather than a
  level.** "A.1. Licensing" is level two, the same call ADR 0008 makes for "4.2.1. Title",
  because the dots have already closed every component the digits opened. "AA.1" is not
  matched at all: the second letter is neither a dot nor a digit, so the scan stops on it and
  the separator check refuses what is left.
- **The letter must be upper case.** A lower-case leading letter is a list label —
  `doc.OrderedLabel` admits "a." and refuses "A." for the converse reason, that an
  upper-case letter and a dot is how a sentence begins. `annexLevel` refuses "A." on the
  same evidence: a letter with no digits after it carries no more than the bare letter does.

**The level assignment is validated against the producer, not chosen.** Reading "A.1" as
level 2 rather than 1 agrees with the declared `H1`..`H6` rank **107 times against 5**, with
**0** blocks promoted that the producer does not call a heading. That is measurably better
behaved than the decimal rule ADR 0008 already ships, which scores 931 agreements against
88 and promotes 10 blocks no producer declares a heading — which is the strongest available
argument for this rule, stronger than any absolute threshold would be.

## Consequences

Ten headings are recovered, in two documents, and nothing else on disk changes. Rendering
all 49 readable PDFs to markdown before and after, the only differing files are:

| document | headings before | after | recovered |
|---|---|---|---|
| `pymupdf/mupdf_explored.pdf` | 296 | **301** | `A.1 Licensing`, `A.1.1 GNU AGPL`, `A.1.2 Artifex Commercial License`, `A.2 Copyright Assignment`, `A.3 Coding Style` |
| `LightOnOCR-2601.14251v1.pdf` | 21 | **26** | `C.1 OlmOCR Headers/Footers`, `C.2 OmniDocBench Results`, `C.3 Extended Efficiency Comparison`, `C.4 RLVR Effect on Repetition Loops`, `D.1 Task-Arithmetic Model Merging` |

Every one is a genuine appendix title that was previously emitted as a bold or plain
paragraph. The 11 tagged ISO documents are byte-identical, because inference never runs
where a producer declared a role. The 7 bare-letter candidates beside them — "A Vocabulary
Pruning", "I The MuPDF C API 5" and the part entries — stay paragraphs, which is the
remaining half of ADR 0008's limit and is still recorded as debt in DESIGN.md §10.

Review pushed back on the breadth of the separator test — `unicode.IsSpace` accepts a tab
and a newline, not only the multi-byte spaces the comment named — and the measurement
inverted the concern. A tab is not a symptom of a block `extract` failed to split: **18
clause headings on disk separate the number from the title with one**, all in the ISO TS
documents ("3\t Terms and Definitions" in 32001 through 32005), so a rule insisting on
U+0020 or U+00A0 would lose every one. A narrowing mutation is caught by a new fixture.
Newlines are the genuinely unobserved shape — 0 spans of 50 documents contain one — and are
admitted by the same call rather than on their own evidence, which the comment now says.

The Markdown level cap is the caller's, and it now has a fixture on this path too.
`annexLevel` counts components with no bound of its own, so "A.1.2.3.4.5.6.7" is depth 8;
`Headings` clamps it to `opt.maxLevel()` on the one line that clamps the decimal rule, which
is the reason the fallback assigns into the same `level` rather than promoting separately.
A mutation that exempted the annex path from the clamp emitted level 8 and is now caught.

`numberedLevel` gave up an unreachable branch to this change. Its `i >= len(s)` test for "a
bare number with no title" survived a mutation that deleted it, because
`utf8.DecodeRuneInString` of an empty tail returns `RuneError`, which the separator check
below already rejects — so the folio and table-cell case was always being caught one line
later. Removing it from both functions is the whole reason to mutation-test a guard that a
green suite says nothing about; the same pass found `depth == 0` unpinned by any test (a
leading-space string returned level 0 with `ok` true) and it now has one.

Roman ("IV.") and parenthesised ("(a)") schemes remain unmatched by either function, and
the bare annex letter remains ADR 0008's open limit with its counter-example named.

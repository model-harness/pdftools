# 35. Take a typesetter's hyphen by the document's own words

Date: 2026-10-20

## Status

Accepted. Part of issue #6, which stays open for footnotes.

## Context

Extract keeps every hyphen that ends a line after a letter, and joins the next line to it with no space. `dashHoldsTheWord` does that because, at a single line, `exten-|sive` cannot be told from `cross-|reference`, and a hyphen deleted in error cannot be recovered from the output. So `hyphenation.pdf` came out as `exten-sive`, `configura-tion` and `implemen-tation`, at CER 0.012, and so did most of `mupdf_explored.pdf`. Issue #6 asks for a hyphen to be joined only where the evidence says the word continues, and for an ambiguous case to keep its hyphen, with no dictionary.

## Decision

**`layout.Hyphens`** reads the document as its own dictionary. It counts every word, keeping hyphens inside a word, so `well-known` is one word. A word that crosses a line start is not counted, so a hyphen being judged is never its own evidence. A hyphen U+002D or U+2010 that ends a line, after a letter and before a lower-case letter, is dropped when either of these holds:

- the document spells the joined word elsewhere, and never the hyphenated one;
- neither half is a word anywhere in the document.

Otherwise it stays. A soft hyphen at a line end is a typesetter's by definition, and always goes.

**It runs on every path.** It runs once before the outline is built, because sectionize copies the spans. `inferRoles` runs it again after `PageBreaks`, since PageBreaks makes line starts of its own. The second run judges what the first left exactly as the first did, because the counted words skip anything that crosses a line start. `md` and `okf` call it, and `ocr` reaches it through `inferRoles`.

**The spike.** The corpus holds 437 hyphens ending a line after a letter and before a lower-case one. The document settles 342 of them:

- 216 have the joined word elsewhere and never the hyphenated one. All 216 were read, and every one is a typesetter's: `struc-ture`, `with-out`, `soft-ware`, `check-point`.
- 126 have the hyphenated word elsewhere: `cross-reference`, `marked-content`, `human-readable`.

For the 95 the document could not settle, a first draft dropped the hyphen unless both halves were words. That joined 9 compounds wrongly, among them `inline-progression`, `document-level` and `multi-column`. Dropping only when neither half is a word anywhere keeps those, and joins 2: `machine-readable` and `sub-clauses`. It also leaves a few typesetter's hyphens whose halves are words, such as `over-|lap` and `insert-|ing`. They are the ambiguous case the issue says to keep.

**`Lines` describe the spans as they are.** The first run panicked on every table fixture. A cell cut from its row's spans kept its row's lines, which index spans the cell does not hold. ADR 0034 had added the field without saying who keeps it true. Every writer now does one of two things:

- **Keeps the starts in step:** the marker and label strips move them back by what each took from a span's front, and `Hyphens` by the hyphen it takes.
- **Empties them:** a table cell does this, because a cell does not know where its own text wrapped.

`sectionize`'s `detach` copies the lines with the spans, so an outline shares neither. `TestBlockLinesDescribeTheirSpans` walks every committed fixture, and the corpus where present, through extraction, the pass and the layout passes. It checks each line against its spans, and it fails at once with the cell's empty-lines fix reverted.

## Acceptance

**`hyphenation.pdf`** went from CER 0.012, recall 0.921 and precision 0.854 to 0.000, 1.000 and 1.000, and its output equals its gold. The bench mean for recall rose from 0.828 to 0.831, and for precision from 0.825 to 0.830.

**The corpus.** The pass takes 249 hyphens from 11 files. Seven documents' `md` output changed, and all 203 changed words were read. Two are wrong, the two the spike named. Every other one is a typesetter's hyphen taken:

| file | removed |
|---|---|
| `mupdf_explored.pdf` | 198 |
| `2201.00069.pdf` | 14 |
| ISO/TS 32005 | 10 |
| LightOnOCR | 8 |
| `v110-changes.pdf` | 7 |
| ISO 32000-2 | 3 |
| `hyphenation.pdf` | 3 |
| ISO/TS 32004 and 32002 | 2 each |
| ISO/TS 32003, PDF-Declarations | 1 each |

**An outside oracle.** Four of the tagged standards mark their typesetter's hyphens themselves: each sits in an element whose `/ActualText` replaces it. Sectionize already applies that, which is why those files' `md` did not move. The pass takes all 15 of them on its own evidence.

**The tests.** `hyphens_test.go` covers each half of the rule and its counter-cases, among them:

- the word spelled whole, hyphenated, both ways, in capitals, and with U+2010;
- a capital after the hyphen, and a digit, punctuation or an en dash before it;
- U+2010 and soft hyphens;
- breaks found only at line ends, and evidence that opens a line;
- two breaks in one block, and a hyphen in a span of its own;
- lines in a later span;
- running twice, and a block with no lines.

`TestStripsKeepLinesInStep`, `TestATableCellHasNoLines` and the extended `TestUntaggedOutlineDoesNotAliasTheDocument` pin the writers.

## Mutation

36 mutants. 33 were killed by named tests in `layout`, `doc` and `sectionize`. The first run left 7 alive. Four were missing cases, now added:

- a capitalized first half;
- a capital inside the second half;
- evidence that opens a line;
- lines in a later span.

A fifth needed a word spelled with U+2010 elsewhere, since nothing did.

The other 3 survive because nothing on disk can tell them apart from the real code:

- **Starting the loop at line 0:** line 0 always starts at offset 0, where there is no character before it.
- **Counting a word that ends exactly at a line start:** that cannot happen, because a wrap always puts a space or a hyphen there.
- **Moving a line that starts exactly at the hyphen:** that cannot happen either, because the next line always starts after the hyphen.

## Consequences

- Hyphenated prose reads as words. A compound whose halves appear nowhere else in its document, such as `machine-readable` in a paper that never says "machine", loses its hyphen.
- The rule is per document, so the same break can be judged differently in two files.
- A typesetter's hyphen whose halves are both words stays, so `over-lap` reaches the output. A dictionary would settle it, and the issue rules one out.

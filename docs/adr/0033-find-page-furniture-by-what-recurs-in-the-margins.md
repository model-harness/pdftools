# 33. Find page furniture by what recurs in the margins

Date: 2026-10-16

## Status

Accepted. Part of issue #6, which stays open for paragraphs across a page break, hyphenation and footnotes.

## Context

A tagged producer wraps its running headers, running footers and folios in Artifact marked content, and `extract` drops them. An untagged page says nothing, so the furniture came out as text. In `running-headers.pdf` the header line and the folio sat in the middle of the prose on every page: CER 0.136, precision 0.882. Real files showed the same, for example the arXiv paper's page numbers and `v110-changes.pdf`'s running title. Until furniture is removed, no paragraph can be joined across a page break, because the header stands between the two halves.

## Decision

`layout.Furniture` runs first among the untagged passes. It marks a block as an artifact when both hold:

- **it lies wholly in a margin band:** the top or bottom 12% of its page;
- **its signature is in the same band on another page.** The signature is the block's text in lower case, with every word that is a number, in digits or roman numerals, replaced by one symbol.

A digit inside a word stays, so "rfc5652" and "rfc6211" stay two texts. One page is no evidence, so a one-page document keeps everything. A block that is already an artifact is neither counted nor marked again.

**The measurement.** The rule was spiked against the tagged corpus, extracted with `KeepArtifacts`, where each producer's furniture is the ground truth. It found 3,538 of 3,572 artifact blocks and marked 11 that are not. Four of those are PDF Association footers such as "© 2019 PDF Association 6", which their producer left untagged. Six are `EXAMPLE n` labels that fall at a page's foot, and one is a table header repeated at the top of two pages.

| variant | true | false | missed | precision | recall |
|---|---|---|---|---|---|
| band 10% | 3506 | 3 | 66 | 0.999 | 0.982 |
| **band 12%** | **3538** | **11** | **34** | **0.997** | **0.990** |
| band 15% | 3538 | 410 | 34 | 0.896 | 0.990 |
| 12%, 3 pages | 3534 | 9 | 38 | 0.997 | 0.989 |
| every digit one symbol | 3502 | 15 | 70 | 0.996 | 0.980 |

Making every digit one symbol, rather than every number, joined footnotes whose URLs differ only in their digits, and failed to match folios set in roman numerals.

## Acceptance

**`running-headers.pdf`** went from CER 0.136 to 0.000, and from precision 0.882 to 1.000. Blocks rose from 0.333 to 0.500, held down by the page-break split, which is the next part of #6. The fixture was rebuilt at a page's proportions, 5 by 6 inches with ordinary margins. At 2.1 inches tall, its header had sat 17% down the page and its folio stuck out of the bottom band, where no real page puts them. Its text and gold are unchanged.

**The corpus.** Four documents changed, and every block removed is furniture: `LightOnOCR`'s page numbers, `exportPDFInput.pdf`'s repeated address footer, `v110-changes.pdf`'s running title, and the fixture.

**The tests.** `furniture_test.go` covers:

- a header, a folio, a roman folio, and a footer with its folio;
- each band's edge, and just past it;
- different text, the same text in the other band, and numbers inside words;
- case;
- one page, and margin text on one page of several;
- blocks that are already artifacts;
- the numeral rule, including words such as "mild" and "did".

## Mutation

15 mutants, all killed by named tests.

## Consequences

- Untagged files lose their running headers, footers and folios, which `-artifacts` keeps.
- A paragraph that a page break splits can now be joined, since nothing stands between its halves.

# 58. A centred title is a heading

Date: 2026-10-28

## Status

Accepted.

## Context

On opendataloader-bench (ADR 0057), pdfspec scored 0.522 on headings against docling's 0.824, and missed every heading on 33 of the 107 pages that have one. ADR 0057 joined the paragraphs a loose leading had split line by line, which a heading has to have under it. The commonest miss left that the layout path can be held to is a title set centred over its page. Document 19's "Author's Note to the 2021 Edition" is 24-point bold over 11-point body, and came out as a bold paragraph. ADR 0031's rule makes an unnumbered candidate a heading only when the block after it starts where it does, within 3 points, and a centred title starts far right of the margin its body starts at.

That alignment test was deliberate. Admitting centred headings admitted every centred figure caption in ISO 32000-2. But those captions are set at about the body's size, and these titles are not: they are 1.62 to 2.4 times it.

## Decision

**A candidate set centred on its page, half as large again as the body or more, is a heading, whatever follows it** (`centredTitle`).

- **Centred:** its middle within 2.5% of the page's width of the page's middle. A book page's text is set off the page's centre by its gutter, 6 points in 434 on document 19.
- **Half as large again:** 1.5 times the body's size.
- **Whatever follows it:** the body that starts at the margin, as a chapter's text does, or a figure, as a textbook's section title over its first figure does. `headsBody` asks for a following paragraph only of a heading set flush.

The candidate still has to pass Headings' gate: uniform in style, distinct from the body, no longer than a heading, and reading as words. A centred line at the body's size, a caption's, is still left to ADR 0031's rule, which keeps it out.

## Acceptance

- **opendataloader-bench:** headings rise from 0.522 to 0.599 with this and ADR 0057 together, and pages with no heading found from 33 to 23. Overall moves from 0.755 to 0.776; reading order and tables are unchanged.
- **The corpus:** two documents change, both by headings that were plain text:
  - `exportPDFInput`'s cover title "MARKET SUMMARY & PLAN" becomes the document's first heading, and its next heading moves a level down under it;
  - `mupdf_explored`'s title page and its five part pages, "Part I" and its title through "Part V", become headings: 11 in all.

  Nothing else in the 76 PDFs changes, ISO 32000-2's centred captions included.

## Mutation

5 mutants cover the title: the ratio either side of 1.5, the centring, the size test, and the wiring. All 5 are killed by `TestACentredTitleHeadsItsBody`, which sets the ratio at exactly 1.5 and just under it, a line off the page's centre, a caption at the body's size, and a title over a figure.

- **One guard could not change the output, and was deleted:** a check for an empty page box or no body size. Headings asks only where the document has a body, and an empty box has no middle a block's can be near.
- One mutant did not compile, and was rewritten so that it did.

## Consequences

- A title set large and centred, a book's, a report's, a chapter's, converts as a heading.
- Large centred text that is no title, a pull quote set in the page's middle, would too. None is on disk.

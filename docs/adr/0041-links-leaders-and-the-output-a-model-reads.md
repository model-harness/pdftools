# 41. Links, leaders, and the output a model reads

Date: 2026-10-20

## Status

Accepted. Closes issue #7.

## Context

#7's last part is its output rules, written for a model reader:

- links kept as `[text](url)`;
- no hard wraps inside a paragraph;
- one blank line between blocks;
- no decorative characters.

Nothing read a page's link annotations, so a link survived only where its text happened to be its URL, which GFM links where it stands. The tables of contents were the decorative characters. ISO 32000-2's contents entries each ran about 150 leader dots: a line drawn to a page number, spelled out in tokens.

## Decision

**Links.** `extract.links` reads a page's `/Link` annotations whose action opens a URI. Its areas are its `/QuadPoints`, one rectangle per quadrilateral, since a link that wraps covers two lines and its `/Rect` the space between them. Without them the `/Rect` serves.

`linkWords` gives each word that lies half inside a link's area that URI, judging height at a third of the type's size above the baseline. A fragment is split where a word in one link meets a word in another or in none. This is `splitWords`, shared now with ADR 0040's strike, which was its first user. Spans with different links do not merge, and `doc.Span.Link` carries the URI.

Only a URI a reader follows is kept: http, https, mailto and ftp. A `javascript:` or `file:` URI is a producer's instruction to a viewer, and written into Markdown it is a link a model or a renderer would act on. A link to a place in the document keeps its text, since a heading's anchor is the sink's to name.

**The sink** writes a run sharing a link as `[text](url)`, with its whitespace outside the brackets. A link whose text is its own URI says it once:

- **bare,** for the lower-case http, https and ftp that GFM autolinks where they stand;
- **`<uri>`,** for the rest. pandoc leaves `HTTPS://example.org` and `mailto:` as text.

The destination percent-encodes the characters it cannot hold: space, brackets, parentheses, and `|`, which ends a table cell before the link is read.

**Leaders.** A run of four dots or more, spaced or not, or two ellipses or more, is written as one space, along with the space around it. Three dots are an ellipsis in prose, and code is never touched.

**The output rules are pinned.** `TestOutputIsShapedForAModel` holds a paragraph drawn over two lines to one line, and blocks to one blank line apart. `TestALinkIsWrittenAsGFMHasIt` and `TestALeaderIsOneSpace` hold the rest.

**The accounting.** `TestMDOutlineConservesText` strips link destinations from both renderings before counting, since they are markup and the two cut a link's run in different places. Its figure is unchanged: 9,526 gained, 9,524 of them substituted and 2 in footnote ids.

## Acceptance

**Links.** 77 more links are written across 14 documents, counted from `pandoc -f gfm`'s output. Every added link was read. The annotations do more than add links:

- **They repair destinations the text breaks.** LightOnOCR's `https: //github.com/NVIDIA/nvtexlive` now links to the right address.
- **They repair the producer's own errors.** autotag's `htp://www.adobe.com/accessibility` links to `http://`.
- **They give the address where the text names something else.** `pdfa.org`, `Apryse PDF Viewer` and the ISO standards' `[(Issue #327](…)` now link where they point.

In each case the text stays as the page drew it.

**Leaders.** Every table of contents in the corpus loses them. The output shrinks: 16 KB from mupdf_explored, 15 KB from ISO 32000-2, and 2 to 5 KB from each standard.

**The gold set.** No fixture moved. The style target, 0.85, is met at 0.960 and is now enforced.

**The tests.** `links_test.go` covers:

- a link by `/Rect` and by `/QuadPoints`, and half coverage on each side;
- a link area above or below the line;
- each scheme kept and refused, a scheme in capitals, a bare scheme;
- an internal action, a `Launch` action, and a widget;
- two links in one run, and a rotated line.

The sink's tests cover text links, emphasis inside a link, autolinks, `mailto`, destination encoding, two links side by side, plain contexts, and leaders.

## Mutation

34 mutants across the reader, the splitter and the sink, all killed by named tests. The first run left 7 alive:

- **Two were checks that could not change any result, and were deleted:** an empty-areas guard, and a no-links early return.
- **One showed a defect:** `autolinked` lower-cased the URI, so an `HTTPS://` link whose text was its URI would have been written bare, and GFM would not have linked it. The scheme check is now case-sensitive, as GFM's is.
- **The rest were missing cases, now tested.**

## Consequences

- A link's destination is in the output even where its text misspells or spaces it.
- No URI that runs anything reaches the output.
- Tables of contents are read without their leaders.

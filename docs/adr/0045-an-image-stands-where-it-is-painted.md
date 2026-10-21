# 45. An image stands where it is painted

Date: 2026-10-21

## Status

Accepted. The first half of issue #11: images reach the output. Classifying what each image holds, so that text, a table, a chart or a description can stand in for it, is the second half and builds on this.

## Context

#11 asks for each embedded image to become the Markdown that carries its information, and `[image]` when nothing can be read. The output did not mention images at all. The extractor skipped image XObjects ("they belong to the image path") and inline images, so a page holding a photo and its caption converted to the caption alone. A model reading the Markdown had no way to know a picture had been there.

## Decision

**The extractor records every image it paints** as a `doc.Image` on `Page.Images`, in paint order. That covers image XObjects, inline images, and images inside forms. Each records:

- its box, the unit square through the CTM, bounded;
- its size in samples;
- whether it is a stencil mask;
- the marked-content id and the artifact flag it was painted under.

An inline image's keys are read under their abbreviations too (§8.9.7). A page keeps at most 4,096 images. What an image holds will be read off the rendered page, not its samples, so masks, transforms and overlays come for free.

**On the untagged path, `layout.Images`** places each image as a `RoleImage` block in reading order, before the other layout passes. Two rules fix where it goes:

- **Columns.** It stands before the first block that shares its columns and starts below its top, and after the blocks above it.
- **Joins.** An image is an aside to the passes that join text, as furniture and footnotes are, since a typesetter floats figures to a page's top or foot.

**On the tagged path**, sectionize indexes images by page and MCID beside the spans. A block element takes the images its own marked content and its non-block kids name, and emits a `RoleImage` block ahead of its text.

A Figure's `/Alt` is the author's description of its image. It becomes the image's line, `[image: alt]`, and is not written again as an italic caption. A Figure drawn in vectors paints no image and keeps its caption as before.

**Decoration is left out,** on both paths:

- **Artifacts:** an image painted inside an `/Artifact`, the producer's statement that it is decoration. The ISO standards' logos are.
- **Small images:** under 16 points on the shorter side. These are icons set in text or a table cell, and logos 10 to 13 points tall; ISO 32000-2's annex tables hold 12 by 11 point icons. The smallest content image in the corpus, a blend-mode sample, is 36 points on its short side.
- **Fills:** 16 samples or fewer. ISO 32000-2 paints colour swatches as 2 by 2 images.
- **Furniture:** the same size at the same place on 3 pages or more, and on half the document's pages, where it covers under a quarter of the page. exportPDFInput's logo on all four pages is. A scan is excluded by the size condition, since every page of a scan is one image in the same place.
- **Images already read:** any invisible text over an image marks an OCR'd PDF, whose text layer is what the scan says. pymupdf's two OCR'd fixtures lay 5 and 6 lines over a full-page scan.

**The sink writes** `[image]` for an image nothing was read off, and `[tag: description]` for one that was. It is bracketed so a reader can tell a description from the page's own text, and kept on one line. A bracket inside the description becomes a parenthesis, so the line closes where it should and reads as no link.

**The bench** counts an image's line as an image and keeps it out of every text score.

## Acceptance

**The corpus.** 208 image lines appear across 21 documents; every changed line was read.

- **The ISO standards.** 174 lines are the standards' tagged figures. Each carries the author's `/Alt` and stands where its caption did: `[image: Figure outlining the components of a PDF file.]`.
- **Untagged documents.**
  - LightOnOCR's six figures stand above their captions.
  - autotagPDFInput's eight images, the "goofy grin" dog among them, stand where they are painted.
  - exportPDFInput keeps 5 of 14 images. The repeated logo and the icons are left out.
- **Pages that are images.** The image-only fixtures and the four reference scans read `[image]`, where they read nothing. With Tesseract, a scan is read whole (ADR 0043), so it carries no image line.
- **OCR'd PDFs.** ocr-ed.pdf and PDF_XChange-OCRed.pdf do not change: their scans are under a text layer.
- **sampleInvoice's** logo and watercolours repeat on all three pages and are left out as furniture.

**The gold set.** `image.gold.md` now opens with `[image]`, since the image is what the fixture is about. No mean moved.

**Tests.**
- `TestImagesArePlacedByTheCTM`: an XObject, rotated, a mask, inside a form, inline abbreviated and in full, in marked content, an artifact. Also `TestImagesAreBounded`.
- `TestImagesStandInReadingOrder`: between paragraphs, above, below, in the right column, beside nothing.
- `TestImagesLeaveOutDecoration`: each rule, the scan that is not furniture, a logo on too few pages, an OCR'd scan. Also `TestImageFillBound`.
- `TestATaggedFigureNamesItsImage`, `TestATaggedFigureWithNoImageKeepsItsAlt` and `TestATaggedImageInsideASpanIsTaken`.
- `TestAnImageIsOneLine` and `TestScoreImageLinesAreNotText`.
- `TestFixtureNoTextIsNotAnError` and `TestFixtureScanWithoutTextLayerIsEmpty` now hold `[image]` as the honest answer for a page that is one image.

## Mutation

36 mutants were run across the extractor, the placement, the filters, the tagged join, the sink and the bench. 35 now die to a named test in their own package. The first run left 12 alive:

- **Seven were boundaries or cases no test reached, now tested:**
  - a logo on exactly half the pages;
  - the same logo twice on one page;
  - a block whose top is the image's;
  - blocks out of page order;
  - visible text over an image;
  - an image floated above a paragraph's continuation;
  - images in the header band, which share the empty text a running header is matched by.
- **Three were tests that could not see their mutant:**
  - the tagged span case let a second Figure take the image anyway;
  - `IsEmpty`'s rule for images was held only from other packages;
  - no test gave a paragraph's `/Alt` to an image.
- **One showed the rule was wrong.** A paragraph that named an image had its own `/Alt` suppressed too. Only a Figure's `/Alt` now goes to its image.
- **One was a rule with no case, and was deleted:** keeping images out of the footnote test. Footnotes sit below floats, so an image below a candidate note is evidence like any other block. That is the 36th mutant, whose code is gone.

Two mutants that did not build were rewritten as changes in behaviour.

## Consequences

- No image the file paints as content is silently dropped, and a reader knows where each one was.
- Every image block points at its placement, so the next step can render its box, read it, and replace `[image]` with what it holds.
- A tagged image with no Figure around it, or one the tree does not reach, is not yet in the output.

# 46. An image is read as what it holds

Date: 2026-10-21

## Status

Accepted. Closes issue #11, on ADR 0045's placement.

## Context

ADR 0045 put every image in the output as `[image]`, or as `[image: alt]` where a tagged Figure describes it. #11 asks for each image to become the Markdown that carries its information:

- text gives its words, in place;
- a table gives a GFM table, from Tesseract's word boxes and the ruling lines, with no model;
- a chart gives a table of its values, from inferd;
- a picture gives `{tag}: {description}`, from inferd;
- without inferd, a chart or a picture is `[image]`.

## The measurements

**The gold set had one image,** image.pdf's colour bands, a picture. `image-kinds` adds one of each kind on a page under their captions, each a PNG built from a source of known content:

- a paragraph set as type and rendered at 200 dpi;
- a ruled table of three columns and four rows;
- a pgfplots bar chart of four labelled values;
- the colour bands again.

Its gold is what each holds: the paragraph's words, the table, the chart's values as a table, and `[image]` for the picture, whose description is a model's and so no gold.

**What Tesseract reads off each placed image.** Every image ADR 0045 places in the untagged corpus, and the four:

| image | words | mean confidence | words cover |
|---|---|---|---|
| the text image | 33 | 96.3 | 39% |
| the table image | 15 | 93.7 | 24%, with rules read as `\|` |
| the chart | 13 | 96.5 | 2.8% |
| the colour bands, photos | 0 to 5 | 0 to 70 | |
| LightOnOCR's screenshots of documents | 11 to 634 | 61 to 91 | 0.2% to 20% |
| exportPDFInput's icons | 1 to 2 | 77 to 87 | up to 78% |

LightOnOCR's figures are pictures of pages in a paper about reading pages. Their words are the figure's, not this page's prose, and they cover at most 20% of their image.

## Decision

**Images are read off the rendered page,** after the pages OCR reads (ADR 0043), by the same Tesseract, at 200 dpi. Each kept image is cropped from its page, the crop copied to an image of its own at the origin, since an engine reports positions from 0. Its kind is decided deterministically where it can be:

- **A table** is a ruled grid with words in it. `ocr.Grid` finds rules as runs of ink crossing 60% of the crop. Ink is a luma under 200: a rule falling between two pixel columns is drawn grey in each, and at 128 the table image's frame broke into runs of half its height. A grid needs three lines each way, a frame with a line inside it, which a chart's frame alone is not, and words in two rows and two columns of its cells. `ocr.Cells` places each word by its centre, dropping a word of nothing but `|`, a rule read as text. The first row is the header. The cells are numbered from 2^20 apart from every other table, so no two share a number.
- **Text** is words read as text (`ocr.IsText`, mean confidence 80), five of them or more, covering 30% of the crop or more (`ocr.IsTextImage`). The text image covers 39%, and no figure covered over 20%. The words become the image's paragraphs through `ocr.Page`, as a scanned page's do, read again by Gemma where inferd runs (ADR 0044).
- **Otherwise inferd's vision model** describes it, `tag: description`. A chart, by its tag or by a description that names a chart, graph, plot or histogram, is then read as a table of its values. Gemma tagged the gold chart "diagram: A bar chart displays monthly sales figures from January to April". Without inferd, the image stays `[image]`.

**What is read stands in for the image** (`layout.ImageBlocks`), on both paths:

- text and a table are their blocks;
- a picture is its line, `[photo: …]`;
- a chart is its line and then its table.

On a tagged file, an image its Figure describes with `/Alt` is not read. The author's description is the file's own, and the tree's pages are resolved to find which images those are. Only what layout places is read (`layout.Keep`): decoration, furniture and OCR'd scans are not.

**The targets.** A fixture whose content is only in pixels cannot meet a target where nothing reads pixels. Those are the four scans, `image-kinds` and `type3-unmapped`. They leave the means `TestBenchmarkTargets` enforces, as in a clone, and are measured by `TestBenchmarkWithTesseract`, where the enforced means hold over every fixture.

## Acceptance

**image-kinds.**

| | CER | tables | kinds read |
|---|---|---|---|
| without OCR | 0.606 | 0.000 | four `[image]` |
| with Tesseract | 0.081 | 0.706 | text, table, two `[image]` |
| with Tesseract and Gemma | 0.000 | 1.000 | text, table, chart, picture |

The picture was described as "flag: A horizontal tricolor flag featuring red, green, and blue stripes."

**The gold means** without OCR, over the fixtures with text in the file, now meet every v1 target:

| CER | recall | precision | order | blocks | headings | tables | style |
|---|---|---|---|---|---|---|---|
| 0.000 | 0.999 | 0.999 | 1.000 | 0.954 | 0.967 | 1.000 | 0.960 |

With Tesseract and Gemma, over all of them:

| CER | recall | precision | order | blocks | headings | tables | style |
|---|---|---|---|---|---|---|---|
| 0.004 | 0.998 | 0.999 | 1.000 | 0.920 | 0.933 | 0.889 | 0.960 |

**The corpus,** run three ways:

- **Without OCR,** byte-identical to ADR 0045 on all 71 documents.
- **With Tesseract,** identical too. No corpus image is read as text or as a table, and only image-kinds changes. That includes LightOnOCR's six screenshots of pages, autotagPDFInput's photographs, exportPDFInput's pie charts and test-rewrite-images' photo with a caption in it. Its grid of edges once read as a two-cell table, until a table needed words in two rows and two columns.
- **With Gemma,** every untagged image is described. Each line was read against the image:
  - autotagPDFInput: "photo: A dark-furred dog with white markings is shown in a portrait-style photograph." and five sunsets;
  - exportPDFInput: four pie charts as charts, each with a table of its percentages;
  - LightOnOCR: "screenshot: A research paper titled …", its architecture as a diagram, and its "complex financial chart", whose table failed its guard and is left out.

  ISO 32000-2 reads 5 of its 166 placed images. The other 161 are described by their Figure's `/Alt`. That holds once the structure tree's pages are resolved; without it every reference is on page 0, and all 166 were read.

**Tests.**
- `TestGridFindsTheRuledTable`, on the fixture's three images; `TestCellsPlaceWordsByTheirCentres`; `TestIsTextImageNeedsWordsCoveringIt`.
- `TestImageIsReadByKind`: a ruled table, text, a chart, a picture, nothing to ask, a describer that fails.
- `TestChartWordsFindAChart`, `TestCropIsAtTheOrigin`, `TestCellBlocksSkipEmptyCells`, `TestFigureAltsNamesWhatTheTreeDescribes`.
- `TestImageBlocksStandInForWhatWasRead` and `TestATaggedImageReadStandsAsWhatItHolds`.
- `TestWarnIfEmptySpeaksOnlyWhenNothingCameOut`: a page that is one image read is no longer warned about.
- With Tesseract: `TestImageKindsAreRead`, #11's done-when, and with inferd too; `TestBenchmarkWithTesseract`, now over every fixture.
- On the sponsored ISO/TS 32001, skipped in a clone: `TestTaggedFiguresAreNotRead`, the one hold on resolving the tree's pages.

## Mutation

40 mutants were run across the grid, the cells, the text rule, the reading step, the tree's descriptions and the substitution. They ran with no engine, as a clone runs, and 39 die to a named test in their own package. The first run left 12 alive:

- **Eleven were cases no test reached, now tested:**
  - lines reaching half way across;
  - a rule's centre;
  - an image under 8 pixels;
  - a word box overhanging its crop;
  - a grid filled in one row, and one filled in one column;
  - a cell off the middle row, where reading y from the top and from the bottom agree only at the middle;
  - a crop whose pixels on the page do not start at 0;
  - the reading step as a whole, through a fake rasterizer, keeping what layout keeps and skipping what the file describes;
  - a text image read again;
  - images read on a page whose text keeps it from OCR.
- **One was a check that repeated the map's own answer, and was deleted:** a content reference never names MCID -1.

The resolved pages are held only by `TestTaggedFiguresAreNotRead`, on a sponsored document a clone lacks. No committed fixture has a tagged Figure whose image its `/Alt` describes.

## Consequences

- An image of text or of a ruled table reads as text or a table with no model, wherever Tesseract is installed.
- A chart's values and a picture's description are a model's, and say so: the description is bracketed and tagged, and the chart's table follows its line.
- An image in a tagged file that no Figure describes, and that the tree does not reach, is still not in the output.

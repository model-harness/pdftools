# 43. Read a page with Tesseract

Date: 2026-10-20

## Status

Accepted. Closes issue #9. Supersedes ADR 0006's model path.

## Context

#9 asks for text in images to be read deterministically, by Tesseract, with each word's confidence deciding whether it is written or tagged `[err]`.

The OCR path it replaces read a page with granite-docling over llama-server and parsed the DocTags it returned. It stated every word with the same assurance, right or wrong. It also needed a model download and a GPU host to run at all.

The user asked for three things before this was built:
- re-run the Gemma comparison with Google's documented OCR prompt, which #9's spike had not used;
- check for a newer Tesseract;
- use Gemma as a second opinion on `[err]`, to tell a ghost from a real glyph.

## The measurements

**Pages.** The spike's 8 pages were re-run:
- each page clean at 300 dpi, and scan-like at 150 dpi (grey, skewed by 0.8°, with noise);
- scored against `md -artifacts`, so that a watermark the converter strips is not counted as an OCR error.

| engine | median CER, clean | median CER, scan-like | median s/page |
|---|---|---|---|
| Tesseract 5.5.3, tessdata_fast | 2.3% | 3.3% | 1.6 |
| Tesseract 5.5.3, tessdata_best | 2.3% | 3.3% | 3.5 |
| Gemma 4 E4B, strict prompt, 4 strips (#9's B) | 5.9% | 5.2% | 8.7 |
| Gemma, Google's prompt, whole page, 512 tokens | 75.8% | 74.6% | 4.2 |
| Gemma, Google's prompt, 4 strips, 512 tokens | 32.3% | 36.7% | 9.9 |
| Gemma, Google's prompt, 4 strips, 3000 tokens | 34.4% | 41.4% | 9.8 |

The Gemma rows above ran at inferd's default image budget, which measured 254 to 268 tokens an image. Google recommends 1120 for fine detail. With the user's approval, the daemon was set to `mmproj_image_max_tokens: 1120`, and the arms were re-run. A page now encodes to 1,094 tokens, and a strip to 938.

| engine at 1120 image tokens | median CER, clean | median CER, scan-like | strips or pages cut by the token bound |
|---|---|---|---|
| Gemma, strict prompt, 4 strips | 17.0% | 3.6% | 3 of 64, in loops |
| Gemma, strict prompt, whole page | 13.5% | 10.4% | 1 of 16 |
| Gemma, Google's prompt, whole page | 72.8% | 70.3% | 10 of 16 |
| Gemma, Google's prompt, 4 strips | 66.6% | 26.9% | 7 of 64 |

The budget helps Gemma read degraded type, 5.2% to 3.6% on the scan-like strips, close to Tesseract's 3.3%. It also makes loops likelier: three clean strips repeated until the bound, and the clean median rose to 17.0%. Tesseract stays first.

The word crops below encode to 47 tokens at either budget, so the snippet results hold at both.

**Google's prompt.** Google's example asks a question of an image, "What does the sign say?", with the image first and 512 new tokens. Asked "What does the text in this image say?" of a page, Gemma does what was asked: it answers. It summarises the page, opens with "The text in the image says:", and sets the text in Markdown and LaTeX. Seven of the 16 whole pages hit the 512-token bound.

inferd fixes the image token budget per daemon (`mmproj_image_max_tokens`, ADR 0013 there), not per request, and its documentation tells consumers to pre-segment. That is what the strips do.

**The newer Tesseract.** 5.5.3, installed here, is the latest release. Its `main` branch has 86 commits since then. None of them touches recognition. The nearest are:
- an ALTO confidence format fix (this reads TSV);
- a quadratic paragraph scan;
- bounds checks on loading `.traineddata`.

The installed model is tessdata_fast; its 4,113,088 bytes match upstream. tessdata_best, verified by its git blob hash, reads the same at twice the time. fast stays.

**Confidence.** Over the 7,481 words read, the share that is wrong falls sharply above 90:

| confidence | words | wrong |
|---|---|---|
| under 30 | 59 | 46% |
| 30–80 | 315 | 29% |
| 80–90 | 338 | 9.8% |
| 90 and up | 6,769 | 0.6% |

**Gemma as a second reader.** Each of the 712 words below 90 was cropped and given to Gemma.
- With Google's question, Gemma answered with a preamble every time. 2 of the 712 matched.
- With a strict transcription prompt, Gemma read 616 right, where Tesseract read 566. The two agreed on 494 words, and 488 of those were right: 98.8%.

So the chain the user asked for is measured, with these steps:
1. accept Tesseract at 90 and up;
2. below that, accept where Gemma agrees;
3. otherwise write `[err]`.

It writes 257 words as `[err]` (3.4%) and lets 44 wrong words through. Tesseract alone lets 188 through. It lands with inferd in #10.

**Ghosts.** Every `[err]` run from ADR 0042 was rendered at 300 dpi and its box measured for ink: 543 runs, 3,288 glyphs.
- One run in type3font.pdf has no ink, and is a ghost.
- Two in mupdf_explored have less than 2%. By eye they are thin parentheses, so real.
- The rest are glyphs, read by eye on a contact sheet: CMEX brackets, Word's "ti", an invoice's address, primes and bullets.

`[err]` is therefore chasing real glyphs. Gemma's answer to "is any character visible?" was no for 69 runs. Only one of those is the ghost; 42 are bullets. It is not a ghost detector, and ink is.

**Segmentation.** Tesseract ran in automatic mode (psm 3) and as a single block (psm 6).
- Automatic failed once: iso209 scan-like, a hex dump split into columns of digits, at 60.3% CER against single block's 4.5%. Single block was 1.5 more confident.
- On the reference fixtures with two and three columns, and on a two-column paper, automatic was the more confident, by 0.5 to 5.3.

**Resolution.** Tesseract read the seven born-digital pages within 0.3 points of CER at 200 dpi and at 300. 300 costs 2.25 times the pixels.

## Decision

**`ocr.Engine` returns words.** Each `ocr.Word` has its text, a box in the image's pixels, a confidence from 0 to 100, and the block, paragraph and line it belongs to. `Name` gives the engine and its model, `tesseract 5.5.3 eng`.

**`ocr/tesseract`** runs the `tesseract` CLI as a subprocess, with no CGO. It is found by `-tesseract`, then `PDFSPEC_TESSERACT`, then `PATH`. The PNG goes in on stdin and TSV comes out on stdout, at the raster's dpi.
- **Segmentation.** It reads each page in both modes and keeps automatic unless single block is more confident by more than 1 point.
- **Confidence.** Weighted by word length, so a grid's one-digit fragments do not decide it.

**`ocr.Page`** builds the page as the extractor would have:
- a paragraph block per Tesseract block and paragraph;
- a span and a `doc.Line` per line, in points with y up;
- lines joined by `extract.WrapSpace`, so a hyphenated word reaches `layout.Hyphens` as the extractor's does;
- a word below `-min-conf` written as `doc.Unmapped`, one per word, since Tesseract does not say how many characters it could not read.

Layout's passes then run over the page like any other.

**`-min-conf` is 30.** Below it, a word is as likely wrong as right (46%). Above it, marking would replace more right words than wrong ones until 90. The Gemma step moves the line to 90 once #10 exists.

**md reads routed pages.** The order is the one the converter works in: the file's text first, then Tesseract on the pages `ocr.Route` picks. `-ocr=false` turns it off. bench scores what md writes, so it reads them too.

**OCR only adds.** A reading replaces a page only when it holds at least 10 times the words with a character read that the extractor found on the page *with its artifacts* (`ocr.Adds`). `-force` overrides this.

The rule came from the bench and the corpus. Route sends a page by coverage, and the reference fixtures set a few lines on a full page, under 5%. Read again, they gave back their own words, now inferred from pixels. With every such page replaced, these bench means fell, all of them enforced:

| dimension | before | replaced |
|---|---|---|
| tables | 0.875 | 0.375 |
| style | 0.960 | 0.560 |
| order | 1.000 | 0.977 |

The corpus then showed two more kinds of page:

- **Pages blank but for their furniture.** The ISO standards' blank pages route at 0 words, since the extractor strips artifacts. OCR read the licence watermark back in, naming the copy's buyer. Compared with the page with its artifacts, they read back 21 words of 21 and are kept.
- **Figure pages.** ISO 32000-2's raster figures read at 3.0 to 5.7 times the extracted words, the labels taken for new text, and replacing one dropped its caption from the outline.

Over the 209 routed pages, every other born-digital page read back at most 1.3 times its words. A scan with a stamped header reads at 50 times or more. 10 sits between.

**A page's reading must be text.** A reading with a mean confidence under 80 is taken as no text (`ocr.IsText`). The photos read at 32 to 74, "eee eee eee eee" among them. Pages of text read at 84.0 to 95.7. A blank page, or a photo, then adds nothing and keeps the extracted page. Neither counts as a failure.

**The reports.**
- `md` names the pages kept, as ranges: "1-151" for a blank scan.
- It names the words read below `-min-conf`, each written `[err]`, by page.
- The file-unmapped warning of ADR 0042 skips pages read by OCR.

**Without Tesseract** the run succeeds. The pages keep their extracted text, and stderr names them. Tests set `PDFSPEC_TESSERACT=off` in `TestMain`, so what is installed on a machine cannot change what they measure. `PDFSPEC_TEST_TESSERACT` names an engine for the tests that use one.

**Retired:**
- `ocr/docd`, `ocr/doctags` and `ocr/ipc`, with granite-docling and llama-server;
- `testdata/docling`, the seven DocTags fixtures, and their manifest entries;
- the ocr verb's `-addr`, `-exe`, `-model`, `-ngl`, `-ctx` and `-max-tokens`.

`ocr/ipc` was not named in #9. It implemented the DocTags contract, and the only route to it was `-addr` to a DocTags host. #10 replaces the wire with thlibo's codec for inferd's v2.

## Acceptance

**The gold set with Tesseract.** Only the four scans and type3-unmapped change. Every other fixture scores what it scored without OCR, and `TestBenchmarkWithTesseract` holds that.

| fixture | CER, without | CER, with |
|---|---|---|
| scan-paragraphs | 1.000 | 0.000 |
| scan-two-column | 1.000 | 0.000 |
| scan-unnumbered-headings | 1.000 | 0.000 |
| scan-table | 1.000 | 0.088 |
| type3-unmapped | 1.000, 15 `[err]` | 0.000, 0 `[err]` |

- The scans' mean CER is 0.022, under the v1 target of 0.03.
- scan-table's 0.088 is its rules read as `|` and "A1" read as "Al". Its grid is #12's, which feeds Tesseract's boxes to the table code.
- The gold means with Tesseract:

| | CER | recall | precision | blocks | headings | tables | style |
|---|---|---|---|---|---|---|---|
| with Tesseract | 0.003 | 0.997 | 0.997 | 0.917 | 0.892 | 0.875 | 0.960 |
| v1 target | 0.03 | 0.97 | 0.97 | 0.90 | 0.90 | 0.85 | 0.85 |

Headings is 0.008 short.

**The corpus.** Run three ways on 71 documents, `docs/*.pdf` and `testdata/*/*.pdf`:
- HEAD;
- this build with `PDFSPEC_TESSERACT=off`, byte-identical to HEAD on all 71;
- this build with Tesseract.

With Tesseract, 7 change, and every change was read. They are the four reference scans, type3-unmapped, `scanned.pdf` (its seven lines, where it had none), and Adobe's `ocrInput.pdf`. That one went from nothing to four pages of a marketing plan, with 19 words `[err]`, among them the logo's three. Everything else is unchanged.

**Tests.**
- **The adapter:**
  - `TestParseTSVKeepsWordRows` and `TestParseTSVRejects`;
  - `TestVersionReadsTheFirstLine`;
  - `TestPickNeedsTheMargin`;
  - `TestNewWithoutTesseract`, which checks `git`, which runs and is not Tesseract.
- **The page:**
  - `TestPageBuildsTheExtractorsShape`, `TestPageJoinsAHyphenAsTheExtractorDoes`, `TestPageThresholdIsInclusive`, `TestPageSpaceIsTheMedianGap` and `TestPageOfAnEmptyImage`;
  - `TestMeanConfWeighsByLength`, `TestIsTextAtEighty` and `TestAddsNeedsTenTimesTheWords`.
- **The commands:**
  - `TestMDWithoutTesseractKeepsTheScan`, `TestOCRVerbWithoutTesseractSucceeds`, `TestBenchReadsWithOCR` and `TestMDRejectsABadMinConf`;
  - `TestOCRReadsAgainstTheFullPage`, `TestWithArtifactsKeepsThem`, `TestOCRGateIsAtEighty`, `TestOCRReportsItsUnsureWords`, `TestPageListWritesRanges` and `TestOCRDefaultsAreTheMeasuredOnes`;
  - the verb tests, now on words.
- **With `PDFSPEC_TEST_TESSERACT`:** `TestReadsAPage`, on the reference scan, and `TestBenchmarkWithTesseract`.

## Mutation

42 mutants were run across the page builder, the router's rules, the adapter and the commands. They ran without Tesseract, as a clone runs, and each died to a named test in its own package.

The first run left 9 alive and 6 that did not build. Those 6 were rewritten as changes in behaviour. Of the 9 survivors:

- **Two were guards that repeated another, and were deleted:**
  - TSV's level check, since only word rows carry text;
  - an empty-page check ahead of `Adds`, which already refuses a reading of no words.
- **Four were tests that could not see their mutant:**
  - the noise case compared against a page that already had text, so `Adds` refused it either way;
  - the gate's confidence was derived from the constant under test;
  - `New` was tried against `git` after `PATH` was emptied, so it failed for the wrong reason;
  - the gate was tested only from `cmd`, while its constant lives in `ocr`, so it is now `ocr.IsText`.
- **Three were missing cases, now tested:**
  - bench's call to OCR, which only the opt-in test had reached;
  - the median of a line's gaps;
  - the verb's success without Tesseract.

The figures behind `-min-conf` and the OCR dpi are pinned to what this ADR measured.

## Consequences

- A scanned page converts with no model, no GPU and no download, wherever Tesseract is installed.
- An unsure word is `[err]`, not a fluent guess.
- The page-level `[err]` of ADR 0042 is recovered whole when a page has no readable text. A page of mixed text keeps its `[err]` until glyph-level recovery, which needs each glyph's exact box: a proportional cut of a span gave Gemma "rea" for Word's "ti".
- #10 adds Gemma's agreement on the words below 90.

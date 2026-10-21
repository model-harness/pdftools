# 44. Gemma reads again what Tesseract doubts

Date: 2026-10-21

## Status

Accepted. Issue #10. Its first done-when box, pictures and charts in the output, closes with #11, which classifies images. The extractor does not yet record where images are placed.

## Context

#10 asks for inferd to answer what only a model can, and for the converter to run without it.

ADR 0043 measured where that is. Gemma is not the page reader. On whole pages and strips it is behind Tesseract at every image budget, and it loops. On a word Tesseract reads under confidence 90, it is a good second reader: where the two agree, 98.8% of words are right. The user's order of work puts it there: the file first, then Tesseract, then the model for what Tesseract cannot settle.

## Decision

**The wire.** `internal/inferd` is thlibo's codec, borrowed at commit 0860bc8 as #10 scoped (MIT, Copyright (c) 2026 3rg0n, the same author). It speaks inferd's v2 wire directly: length-prefixed frames, with images as raw RGB in BLOB frames. Changed here:

- The embedding client is left out.
- A message can carry its images before its text. Google's examples order them that way, the spike measured that order, and the daemon's Gemma 4 template renders blocks in the order sent.
- `ReadStatus` reads the admin socket's opening frames: one capability frame per backend, then the state. A ready daemon with a v2 backend that has `vision` is the only one asked.
  - The read is bounded at 3 s.
  - The dial is bounded at 250 ms, so a run with no daemon learns so at once. A Windows pipe can be briefly absent between instances, which is why the dialer retries a missing one at all.
- `Result.StopReason` says when the token bound cut a reply off. A guard refuses that reply.
- The timeout variable is `PDFSPEC_INFERD_TIMEOUT`, and its default is 60 s.

**The adapter.** `ocr/gemma` wraps it. `Open` returns `ErrUnavailable` when there is no daemon, it is loading, or no backend sees images; the caller goes on as if it had not asked. Every request is image-first at temperature 0, and every answer passes a guard. A refusal is `ocr.ErrRefused`.

| method | prompt | guard |
|---|---|---|
| `Read` | the spike's strict transcription | refuses an empty reply, one longer than its bound, more than one line, Markdown or LaTeX, a loop, or a reply cut off |
| `Describe` | `tag: description`, one line | refuses anything not of that form, and a loop |
| `Chart` | a Markdown table of the values | refuses anything but a GFM table with a separator and a data row, a row of the wrong width, a loop |

Every reply is cleaned of leaked template tokens, the `<turn|>` the user saw at the end of Google's captioning template among them.

A loop is either of these: a line said three times or more where there are four lines or more, or one word eight times in a row. #9's spike saw three lines repeated fifty times.

**The second reading.** `ocr.Confirm` asks the reader about each word Tesseract reads under `ocr.SureConf`, 90. The crop is the word's box padded by a third of its height, as the spike cut it, from the image Tesseract read, and the answer is bounded at twice the word's length plus 8 characters.

- **Agreement** is equality of the letters and digits in NFKC, the comparison the spike scored by. The word is marked `Agreed`, and `ocr.Page` writes it whatever its confidence.
- **A disagreement, or a refusal,** leaves the word under 90 and unconfirmed. With a second reader the threshold is 90, so it is `[err]`. Neither reading is taken, since where the two disagreed in the spike, each was right about as often as neither was.
- **A reader that fails outright,** the daemon gone mid-page, is dropped for the rest of the run. The page is read with `-min-conf` alone, as it would be without inferd.
- **A page whose reading would not replace it** (`ocr.Adds`, ADR 0043) is not asked about. That took the gold set from 9 sets of questions to 2.

**Wiring.** `md`, `ocr` and bench find inferd by `-inferd`, then `PDFSPEC_INFERD`, then the platform's endpoint, and `off` asks nothing. `TestMain` sets it off with Tesseract. stderr says how many words were asked, by which model, how many it agreed with and refused, and how many are `[err]`, by page.

## Acceptance

**Live, on the spike's seven born-digital pages.** Each page was read through OCR at 200 dpi and scored on its own page against `md -artifacts`:

| | wrong words accepted | `[err]` | median CER |
|---|---|---|---|
| Tesseract, `-min-conf` 30 | 65 | 33 | 3.3% |
| Tesseract with Gemma's agreement | 24 | 103 | 4.2% |

- Gemma was asked about 306 words and agreed with 203, 66%; the spike's rate was 69%.
- It refused 23.
- Wrong words accepted fell by 63%. CER rose, because an `[err]` costs its word's characters: that is the trade "mark, don't guess" makes.

**The gold set with both.** Every mean is as with Tesseract alone, but precision, which rose from 0.997 to 0.999:

| | CER | recall | precision | blocks | headings | tables | style |
|---|---|---|---|---|---|---|---|
| with Tesseract and Gemma | 0.004 | 0.997 | 0.999 | 0.917 | 0.933 | 0.875 | 0.960 |
| v1 target | 0.03 | 0.97 | 0.97 | 0.90 | 0.90 | 0.85 | 0.85 |

scan-table's misread "Al", for "A1", is now `[err]`, since Gemma read "A1" and the two disagreed. Its precision went from 0.944 to 1.000.

**A described picture.** img-regular.pdf's page is a grid of yellow heptagons, four rows by three. Gemma described it as `drawing: A grid of yellow octagons arranged in four rows and three columns.`: the layout is right and the side count is not. A model counts poorly, as Google's own page warns, and a description is the model's, which #11 will mark as such.

**Tests.** The guards are pinned on synthetic replies, as #10's second box asks:
- `TestCleanStripsControlTokens`, `TestLoopsFindsRepetition`, `TestGuardRead`, `TestParseDescription` (Google's captioning answer among the refused), `TestParseTable`;
- `TestReadStatusFindsTheVisionBackend`, `TestReadStatusWithoutVision`, `TestReadStatusWhileLoading`, `TestReadStatusRejects`, `TestReadStatusWithNoDaemon`, `TestImagesFirstOrdersTheBlocks`, `TestOpenWithoutADaemon`;
- `TestConfirmAsksOnlyUnderNinety`, `TestConfirmStopsWhenTheReaderFails`, `TestCropStaysInTheImage`, `TestSameReadsLettersAndDigits`;
- `TestOCRSecondReaderDecides` (agree, disagree, refuse, fail), `TestOCRAsksOnlyWhereTheReadingWillTake`, `TestOpenSecondHonoursOff`;
- with `PDFSPEC_TEST_INFERD`: `TestLiveVision`, over the native wire, and `TestLiveDescribe`.

## Mutation

43 mutants were run across the wire, the admin reader, the guards, the agreement and the wiring. They ran with no daemon, as a clone runs, and each now dies to a named test in its own package.

The first run left 5 alive:

- **Three were guards that repeated another, and were deleted:**
  - the admin reader's check for a stream that ends without a status, which the next read's end of file already makes;
  - the separator's check for no cells, which `strings.Split` never gives;
  - the wiring's check for no words before asking, which `Adds` already makes.
- **Two were tests that could not see their mutant:**
  - the no-daemon timing check took its bound from the constant under test;
  - the client's stop reason was tested only on a hand-built result, so the parse is now tested too.

Two mutants that did not build were rewritten as changes in behaviour.

To make a clone able to test them, two decisions were moved out of code reachable only with a daemon. `choose`, which backend `Open` takes, and `checkStop`, which refuses a reply cut off, are pure functions.

## Consequences

- With inferd running, a word Tesseract doubts is either confirmed by a second reader or marked `[err]`. Without it, the run is as ADR 0043 left it.
- The picture and chart prompts and their guards exist and are tested. #11 sends them images.
- Recovering ADR 0042's `[err]` glyphs on born-digital pages needs each glyph's exact box, which a span's proportional cut does not give.

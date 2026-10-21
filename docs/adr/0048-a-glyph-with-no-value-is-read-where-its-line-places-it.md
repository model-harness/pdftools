# 48. A glyph with no value is read where its line places it

Date: 2026-10-21

## Status

Accepted. Closes issue #8.

## Context

#8 asks that no glyph be guessed. A glyph whose value the file does not give is written `[err]` (ADR 0042), and a recovery chain then reads it off the page: Tesseract above its confidence threshold, then inferd, and otherwise the `[err]` stays. Every recovered glyph is to be recorded with the engine that read it and its confidence.

A page whose glyphs all have no value was already read whole: the router sends it to OCR, and the reading replaces it (ADRs 0043 and 0044). The gold set's type3-unmapped goes from 15 `[err]` to none that way.

That leaves a page with a few such glyphs among text the file does map. OCR's reading of that page adds nothing (`ocr.Adds`), so the page keeps its text, and its `[err]` with it.

Over the corpus, eight documents hold 3,288 glyphs with no value. type3-unmapped's 63 are read whole, which leaves seven documents and 3,225 glyphs:

| document | glyphs | what they are |
|---|---|---|
| sampleInvoice.pdf | 2,781 | whole lines: the items, their prices, the addresses |
| 2201.00069.pdf | 284 | offsets and coordinates, `∼`, `″`, decimal points, monospace tool names, URLs |
| autotagPDFInput.pdf | 58 | the "ti" and "ft" ligatures, inside words |
| mupdf_explored.pdf | 54 | list bullets, and a shell's `$` |
| v110-changes.pdf | 36 | letters of a subset font, inside words |
| type3font.pdf | 8 | a Type 3 font's glyphs |
| LightOnOCR-2601.14251v1.pdf | 4 | symbols set alone in a formula |

The spike read each `[err]` span's clip of the page with Tesseract. A glyph read on its own loses what makes it legible: alone, "ti" is two strokes, and inside "creating" it is two letters. Every ligature in autotagPDFInput came back in its word at 95 to 97 ("creating", "Population", "software"). The mapped letters around a run single out the word that holds it, even when the clip holds words from the lines above and below.

## Decision

**The page is read once, at 300 dpi, and each line takes the words standing on it** (`glyphs`, `lineWords`).

- A word stands on a line when its centre is inside the line's box, with a line's height of slack to each side for a glyph that overhangs it.
- The line is the unit, not the span. A span runs on across lines whenever its style does, and a span that is one glyph alone, the `$` before "mutool usage", has its context in the next span.
- One reading a page rather than one a line. A page set wholly in such a font, and not routed, would cost forty processes a page. Read a line at a time, the seven documents gave 1,702 glyphs to the page's 1,600. sampleInvoice took 24.5 seconds against 8.8.
- 300 dpi and not OCR's 200. At 200, 2201's offsets and coordinates read 53 glyphs to 300's 106, and sampleInvoice's body 1,441 to 1,403.

**A run is read only where the line's own text places it** (`ocr.Fills`):

1. The line's mapped words are matched to the reading's, in order, by their longest common run. What lies between two matches on one side is read against what lies between them on the other.
2. Where the two sides have as many words, they are paired. A word with a run must read as its mapped letters around the run: "crea" and "ng" around "ti". A run of n glyphs reads as n characters, or as up to 3n letters, since only a ligature reads as more characters than it has glyphs. 2201's "5″", two primes, came back "55”": two characters for one glyph and not letters, so it was refused.
3. A word of nothing but a run of n glyphs reads as a word of n characters.
4. A gap of nothing but such words, a line set wholly in such a font, takes the reading's words split into groups in order. Each group's characters, spaces counted or not, must number its word's glyphs, and there must be exactly one such split.
5. A group's words join with spaces, as Tesseract read them apart. The count cannot tell a space the page draws as a gap ("Springfield CA") from a break Tesseract reads inside a word ("https: //"), and Tesseract's reading is the only evidence there is.
6. A line of one glyph and nothing else is not read. Nothing places it, and every reading of one character has its count. LightOnOCR's formula glyph read as `\` at 92.

**A reading is taken where Tesseract is sure of it, or where Gemma agrees** (`confirmFills`):

- A fill is taken at `ocr.SureConf` (90) or over. Below that, it is taken only where the second reader's reading of the word agrees with it (`ocr.Confirm`). Without a second reader it stays `[err]`. There is no fall-back to `-min-conf`, because a glyph that stays `[err]` is still honest, and a guess at 30 would not be.
- **A word that is one glyph alone needs the second reader however sure Tesseract is.** It is read by its shape alone: Tesseract read mupdf_explored's 31 bullets as "e", and its 12 `$` as "$", all at sure confidence.
- That agreement is character for character (`ocr.SameGlyph`). `ocr.Same` compares letters and digits, and a glyph alone is as often a symbol. Gemma reads the bullets as nothing, so they stay `[err]`, and it reads the `$` as `$`.
- Gemma's read guard refused any `$` as LaTeX. It now refuses two or more, since LaTeX's math is between two dollars, and one dollar is a price or a prompt.

**Each run read is recorded** in `Page.Recovered`: the line's box, the glyphs, the text, the engine and Tesseract's confidence. Stderr reports the count, and per page the glyphs read by each engine with the range of confidences: "p1 468 by tesseract 5.5.3 eng at 92-97". Writing every glyph to stderr would bury the report on a page like sampleInvoice's.

**A page read whole by OCR is not read again:** its `[err]` are words below the threshold, reported with that reading.

`md` now runs OCR for a document with a glyph with no value, as it does for a routed page or an image (`wantsOCR`).

## Acceptance

**New fixture `type3-words`:** lmodern text with a mapping, and words and parts of words in the EC bitmap fonts type3-unmapped uses ("recov[er]y", "glyph[s]", "1234", a lone `$`). Its 42 glyphs with no value are 11 `[err]`. With Tesseract, 41 are read and CER goes from 0.057 to 0.001. The `$` reads as "§" at 71, Gemma does not agree with that reading, and it stays `[err]`. `TestBenchmarkWithTesseract` pins both counts.

**The corpus,** glyphs read of 3,225:

| document | glyphs | Tesseract | with Gemma |
|---|---|---|---|
| sampleInvoice.pdf | 2,781 | 1,403 | 1,439 |
| 2201.00069.pdf | 284 | 106 | 220 |
| autotagPDFInput.pdf | 58 | 57 | 57 |
| mupdf_explored.pdf | 54 | 0 | 12 |
| v110-changes.pdf | 36 | 34 | 34 |
| type3font.pdf | 8 | 0 | 0 |
| LightOnOCR-2601.14251v1.pdf | 4 | 0 | 0 |
| **all** | **3,225** | **1,600 (49.6%)** | **1,762 (54.6%)** |

Every fill was read against the page's render:

- **Right:** the ligatures, v110's "Constructor now requires the page’s mediabox." and "“burnt in” into them", sampleInvoice's rows and addresses, 2201's decimal points, `∼`, "uvotdetect" and "uvotsource", and the `$` prompts.
- **Wrong:** two link texts in 2201, agreed by Gemma, read "https: //…" with Tesseract's break kept (rule 5).
- **Character, not shape:** 2201's primes read as `’`. They look the same and are different characters.

**Tests.**

- `TestFillsPlaceARunByItsLine` covers each rule and its refusal.
- `TestFillsAtIsTheRun` covers the byte range a fill names.
- `TestGlyphsReadEachLine` covers one reading a page, the replacement, the next line's offset, the record and the report.
- `TestGlyphsNeedSureOrAgreed` covers the confidence, the second reader, a lone glyph, and a page read whole.
- `TestGlyphsFindTheirSpan` and `TestTextLinesFollowTheSpans` cover a line across spans.
- `TestGlyphsKeepAReaderThatRefuses` covers a refusal and a failure.
- `TestLineWordsStandOnTheLine` covers the slack and the order.
- `TestSameGlyphIsTheCharacter`, `TestGuardRead` and `TestWantsOCRForAGlyphWithNoValue` cover the rest.

## Mutation

The 44 mutants were all killed, each by a named test:

- 23 cover the alignment, the glyph comparison and Gemma's guard.
- 19 cover the lines, the acceptance, the record and the replacement.
- 2 cover the wiring into `md`. Only `TestBenchmarkWithTesseract` holds those, and it needs Tesseract.

**4 survived the first batch,** and 7 did not build and were rewritten so they did.

- **A word with a run checked as no anchor.** This was a copy and was deleted: doc.Unmapped is no character a reader writes, so such a word never equals a reading.
- **The guard that keeps a mapped word out of a split.** It is now held by a mapped word Tesseract read as two, beside a word of glyphs.
- **The common-run table's tie.** It is now held by a line that repeats a word, the first one misread. A corrupt table anchors the misread one and loses the gap that pairs the run. A search over every short pair of sequences found the case, after one over binary sequences found none.
- **Splitting the line on a no-break space.** That now has a case.

**One check was deleted before the batch.** The walk's test that a match lies on a longest path is always true where the table counts a match.

## Consequences

- A born-digital page with a few glyphs its fonts do not name reads whole, with no model, wherever the line around each glyph places it. That is about half of the corpus's such glyphs.
- What is left is mostly whole lines whose reading does not split one way into their words, symbols set alone, and bullets. In sampleInvoice, that is the rug rows and most of the "54” Buffet" names.
- A glyph set alone waits for a second reader, and with one it is read character for character.
- The page is read again at 300 dpi for its glyphs, after any reading OCR's route made at 200. A page with both is read twice.
- 2201's link texts keep Tesseract's break inside a URL. Telling that break from a drawn space would take the gap between the two words, measured against the line's spaces, and that is not built.

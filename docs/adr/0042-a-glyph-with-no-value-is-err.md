# 42. A glyph with no value is [err]

Date: 2026-10-20

## Status

Accepted. The first half of issue #8. Recovery, the second half, waits on #9 and #10.

## Context

#8 asks that no glyph be guessed. A glyph whose Unicode value the file does not determine is written `[err]`, and a recovery chain then tries to read it.

The file can determine a glyph's value in several ways: `/ToUnicode`, the encoding and its differences, glyph names, and the font program's own cmap. `font.Decode` already tried each of them, and returned empty text when none answered. The extractor then placed nothing for that glyph, and its advance carried the pen on, so the text closed over it. That loss was silent. Two examples:

- **sampleInvoice.pdf** draws its whole body in a font with no `/BaseFont` and no mapping. It converted to its running header and three folios.
- **type3font.pdf** converted to nothing, and a test pinned that empty output as the honest answer.

## Decision

**The extractor writes `doc.Unmapped`, U+FFFD, for each glyph with no value.** It is placed like any other character, with the glyph's position, size and style. A word drawn in an unmapped font keeps its shape on the page, along with its lines, blocks, gaps and columns.

There is one exception: **a single-byte code 32.** Its code says it is the word space (§9.3.3 applies `Tw` to it alone), so it is not marked. It now leaves the pen behind, as a skipped string does, so its width is the gap that infers the space. Before this change it advanced the pen like every other dropped glyph. The invoice's 27 such spaces would then have joined its words into one run.

A two-byte code 32 is a glyph like any other.

**The Markdown sink writes each run of U+FFFD as one `[err]`.** One tag per run rather than one per glyph: a word of 12 unread glyphs is one thing a reader cannot read, and twelve tags would spend 60 bytes saying so. Both of the sink's final writers do this: `escapeInto`, and `sanitize` for code spans and code blocks. Every context is therefore covered, including headings, emphasis, table cells, link text and okf's bundles.

The tag is escaped as `\[err]` where the next character would turn it into a link's text or a reference label, which is before `(`, `[` and `:`. This matters in particular because a line `[err]: x` would define `[err]` as a link, and every other tag in the document would then point to it. pandoc renders every form in the tests as the text it means.

**A U+FFFD the file supplies is `[err]` too.** autotagPDFInput.pdf's `/ToUnicode` maps Word's "ti" ligature to U+FFFD, the file's own statement that it does not know. The C0 replacement, for a code the file maps to a control byte, stays U+FFFD: that is a value, if a meaningless one.

**The counts are reported.**
- `md` warns on stderr with the total and the count on each page, `by page: p1 29, p2 7`.
- `probe` reports `unmapped` and `unmapped_pages`, found by extracting the text. A font's mapping is decided glyph by glyph, so the fonts alone cannot answer.
- `md`'s empty-output hint no longer blames fonts, which now give `[err]` rather than nothing.

**OCR routing reads only what was read.** `ocr.Coverage` measures the blocks that hold a character other than U+FFFD and whitespace. A page drawn wholly in an unmapped font routes to OCR as it did when it extracted to nothing. Counting its `[err]` blocks would have kept it away from the one engine that can read it.

**The bench scores an `[err]` as one unread character.** That is one edit in CER, and no word "err" in the bag. Means are unchanged: `type3-unmapped`'s CER stays 1.000, now from 15 `[err]` against its 63-character gold rather than from nothing. This is the figure ADR 0029 planned for.

## Acceptance

**The corpus.** 3,288 glyphs across 8 documents and 32 pages are now written as 543 `[err]` tags. Every changed line was read.

| file | glyphs | `[err]` | what they are |
|---|---|---|---|
| sampleInvoice.pdf | 2,781 | 307 | the whole invoice, in a font with no mapping |
| 2201.00069.pdf | 284 | 69 | NewTX math (primes, minus signs, degrees) and t1xtt URLs |
| type3-unmapped.pdf | 63 | 15 | the gold fixture, one per word |
| autotagPDFInput.pdf | 58 | 58 | the file's own U+FFFD for "ti" |
| mupdf_explored.pdf | 54 | 54 | list bullets in an unnamed font, and CMEX10 brackets |
| v110-changes.pdf | 36 | 32 | Calibri letters with no mapping |
| type3font.pdf | 8 | 4 | bitmap glyphs named `/0` and `/1` |
| LightOnOCR | 4 | 4 | CMEX10 delimiters |

**What else moved:**
- **2201.00069.pdf.** The footnote URLs come back as `[[err]](https://…)` links. Their annotations give the address, but the text drawn over them has no value.
- **v110-changes.pdf.** Two lines become headings: "API Change: Display List" and "API Change: Text Page". Each sits above a bold paragraph that `headedRunes` had seen as too short. Its unmapped letters had been dropped, and once they count, the paragraph is the body it is.
- **The CMEX10 delimiters.** These are large brackets set below the baseline, and they now stand on lines of their own. LightOnOCR's IoU formula and mupdf_explored's matrix each break into several blocks around them. This is left for now, as display maths, not prose.
- **Nothing else changed.** The gold set's means are as they were.

**Tests.**
- `TestAGlyphWithNoValueIsKept`: a simple font, a space between, a composite font, a composite code 32.
- `TestAnUnmappedRunIsOneErr`: 12 contexts, including all three escapes and the control byte.
- `TestScoreErrIsOneUnreadCharacter`.
- `TestRoute`: an all-`[err]` page, and one with a character read.
- `TestAnUnmappedFontIsWrittenErr`: the fixture's md, its stderr from `runMD`, probe's count, and type3font.pdf.
- `TestUnmappedGlyphsAreReportedByPage`.

`TestFixtureNoTextIsNotAnError` no longer lists type3font.pdf.

## Mutation

26 mutants were run across the extractor, the sink's two writers, the router, the bench and the reports. All of them are killed by named tests.

The first run left one alive: `ocr -dry-run` printing `doc.Page.Coverage` rather than `ocr.Coverage`. With that mutant, the plan would show 1.5% for a page it routes as 0%. `TestOCRDryRunMeasuresWhatWasRead` now holds it.

`TestAGlyphWithNoValueIsKept/a_space_between` kills the pen mutant. This is the one where an unmapped space carries the pen on again.

## Consequences

- No glyph the file draws is lost without a mark, and a reader can count what was not read.
- The `[err]` tags are where recovery starts. Each is a glyph with a position and a size, so its clip can be rendered for Tesseract (#9) and then inferd (#10).
- A page all `[err]` is still routed to OCR whole.

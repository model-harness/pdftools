# Test documents

Where this project's PDF test inputs come from, what each one is for, and what the
upstream projects publish that is worth reading.

There are two populations and the distinction matters more than it looks.

**`docs/` — the corpus.** Eleven documents: the ISO 32000 specifications, the PDF
Association's technical notes, one arXiv paper. It is where every threshold in the test
suite was measured, and it is **gitignored** — the ISO PDFs are paid documents and not
redistributable. Corpus tests skip when the files are absent, so a fresh clone passes.

**`testdata/` — the reference fixtures.** 30 files from PyMuPDF and Adobe, committed,
each one chosen upstream *as a test input*. See §Provenance.

The corpus is a good regression baseline and a bad witness. Ten of its eleven files are
tagged standards prose from one family of producers, so it exercises the tagged path
almost exclusively, and a threshold tuned on it cannot be shown to generalize by running
it again. It also contains no CJK, no ligature test, no deliberately broken font, no
zero-byte file, no CCITT, and no `/SMask` without `/Matte`. Everything in `testdata/` is
there because it covers something the corpus does not — that is the selection rule, and
§Fixtures says which gap each file fills.

The reason to want an outside population at all: a baseline this project measured from
its own output catches a regression but cannot catch a mistake that was already there
when the baseline was taken. `ocr-ed.txt` is the only file in the repository that states
what the right answer is without this project having decided it.

Two real defects surfaced the first time these ran, and neither was reachable from the
corpus: every scanned document was being wrapped in backticks (§Ground truth), and every
CJK line wrap was splitting a word (§The CJK line wrap).

---

## Sources

### PyMuPDF — `github.com/pymupdf/pymupdf`

The Python binding for MuPDF, and the most complete open PDF text-extraction stack
outside the commercial products. **AGPL-3.0** (commercial licence available from Artifex),
which is exactly the licensing wall described in `DESIGN.md` §1 and the reason this
project exists.

Worth reading — methodology, not code:

- [`docs/`](https://github.com/pymupdf/PyMuPDF/tree/main/docs) — the reference manual. The
  `TextPage` and `Page.get_text()` pages are the useful ones: the `dict`/`rawdict` output
  formats are a well-thought-out answer to "what shape should extracted text be", and
  `doc.Document` is a deliberate narrowing of the same idea.
- [`tests/`](https://github.com/pymupdf/PyMuPDF/tree/main/tests) — where the fixtures live,
  each named for the behaviour or the issue it pins. Reading the test names is the fastest
  way to learn which PDF constructs actually break extractors in the field.
- [Recipes: text](https://pymupdf.readthedocs.io/en/latest/recipes-text.html) — the
  practical write-up of extraction order, clipping, and the `sort` flag.

### PyMuPDF-Utilities — `github.com/pymupdf/PyMuPDF-Utilities`

Worked examples rather than library code. **AGPL-3.0**.

- [`OCR/`](https://github.com/pymupdf/PyMuPDF-Utilities/tree/master/OCR) — the same page in
  three states: scanned, OCRed by Tesseract, OCRed by PDF-XChange, **plus the expected
  text as a `.txt`**. That last file is the ground truth. The directory's README on
  invisible text layers (rendering mode 3) is the clearest short description of what an
  OCR layer is in PDF terms.
- [`text-documents/`](https://github.com/pymupdf/PyMuPDF-Utilities/tree/master/text-documents)
  — text-extraction examples, including the layout-preserving renderer whose output format
  `ocr-ed.txt` is.

### Adobe PDF Services — `github.com/adobe/pdfservices-python-sdk-samples`

**MIT**, which is why these fixtures are the least encumbered thing here. The value is
that the sample inputs are chosen by the team that wrote Acrobat, so they represent what
Adobe considers a representative or a problematic document.

- [`src/resources/`](https://github.com/adobe/pdfservices-python-sdk-samples/tree/main/src/resources)
  — the sample inputs, named for the operation they demonstrate.
- `src/resources/invalidinputs/` — deliberately bad inputs. `zeroLength.pdf` is 0 bytes
  and `disqualifiedScannedPages.pdf` is 151 pages Adobe's own OCR declines.

### Adobe PDF Services docs — `github.com/AdobeDocs/pdfservices-api-documentation`

**Apache-2.0** for the documentation. No fixtures taken from here; it is a reference for
output design.

- [Extract PDF](https://developer.adobe.com/document-services/docs/overview/pdf-extract-api/)
  — the JSON schema for extracted content. Its element vocabulary is the PDF structure-tree
  vocabulary, which is the same thing `tag` reads.
- [PDF to Markdown](https://developer.adobe.com/document-services/docs/overview/pdf-to-markdown-api/)
  — see §Markdown coverage below.

> This repository's `docs/` also holds the ISO 32000-1 and 32000-2 PDFs. Both Adobe repos
> redistribute copies. **Not committed here** — ISO's copyright does not change because a
> vendor mirrors the file, and they are gitignored for the same reason the sponsored
> copies are.

---

## Provenance

`testdata/manifest.json` records, per file: the upstream repo, a **pinned full commit
SHA**, the path within that repo, the SHA-256 of the bytes, the size, and one line on why
the file is here. `testdata/fetch.ps1` re-downloads from those pinned commits and verifies
every hash:

```powershell
pwsh testdata/fetch.ps1            # verify what is in the tree
pwsh testdata/fetch.ps1 -Download  # fetch anything missing, then verify
```

`TestFixtureManifestMatchesTree` fails if a file is in the tree without a manifest entry
or in the manifest without a file, so the record cannot silently drift from the bytes.

**Selection rule — reference-intent only.** A file is committed only if upstream authored
it *as a reference fixture*. Two categories are excluded:

- **Bug-report attachments.** PyMuPDF's `test_NNNN.pdf` files are named for the issue that
  produced them, which means they were uploaded to a tracker by a third party. Upstream
  cannot grant rights to those, so their presence in an AGPL repo says nothing about their
  licence.
- **Copies of the ISO specifications.** ISO's copyright, whoever redistributes them.

**On the AGPL.** 22 of the 30 fixtures are AGPL-3.0 upstream. They are data in a marked
directory with attribution, not linked code — mere aggregation, which does not relicense
this project's MIT Go source. No PyMuPDF or MuPDF code is used, read for copying, or
translated; where their approach informed a design decision it is cited in `DESIGN.md` as
methodology.

---

## Ground truth

`pymupdf-utilities/ocr-ed.txt` is the expected text for `ocr-ed.pdf`, written by upstream:

```
       PyMuPDF— the Python
          bindings for MuPDF

PyMuPDF Documentation
                            Release 1.18.19

                               Jorj X. McKie

                                     Sep 17, 2021
```

`TestFixtureOCRMatchesGroundTruth` compares our Markdown against it on word sequence
rather than byte-for-byte: upstream's file is PyMuPDF's layout-preserving rendering, which
pads with spaces to place text on the page, and Markdown's job is not to reproduce that.
The words and their order are the claim.

It found a defect on its first run. The text was word-for-word correct and every line of
it came out wrapped in backticks:

```
`PyMuPDF— the Python bindings for MuPDF`

`PyMuPDF Documentation Release 1.18.19`
```

Tesseract's invisible text layer uses `GlyphLessFont`, whose descriptor sets the
`FixedPitch` flag — a true statement about a font nobody ever sees, and not a typographic
claim about the text. The Markdown sink read it as "code". No metric in the suite could
have caught this: character counts, space ratios, and word-length distributions are
identical either way. The fix is in `sink/markdown/inline.go`: `Style.Hidden` suppresses
the code mark, and the measurement behind it is that `mono && !hidden` is zero across
every fixture, including a 285-page C API manual full of real code listings.

`PDF_XChange-OCRed.pdf` is the second engine, and it is here so the fix is not a fix for
Tesseract: its layer is invisible but *not* fixed-pitch, which exercises the other branch.

---

## Fixtures

Each row says what the file covers that the corpus does not. `path` is probe's routing
decision. All of it is asserted in `cmd/pdfspec/testdata_test.go`.

### PyMuPDF (`testdata/pymupdf/`)

| File | Pages | Path | Covers |
|---|---|---|---|
| `2201.00069.pdf` | 1 | layout | LaTeX Type1, the ordinary born-digital page |
| `chinese-tables.pdf` | 1 | layout | **CJK.** A line wrap inside a Chinese word is not a word boundary. The corpus has no CJK at all, and this file found a real defect — see below. |
| `circular-toc.pdf` | 2 | layout | A **cyclic outline tree.** Without cycle handling the test hangs rather than fails. |
| `dotted-gridlines.pdf` | 1 | layout | Table rules drawn as dot patterns — vector noise that must not become text |
| `has-bad-fonts.pdf` | 1 | layout | **Deliberately broken font dictionaries.** A defective font may cost its own glyphs; it may not cost the page. This is `DESIGN.md` §1's whole argument as a test. |
| `img-regular.pdf` | 1 | ocr | One image, no text: the minimal OCR-path file |
| `img-transparent.pdf` | 1 | ocr | **`/SMask` with no `/Matte`** — plain alpha, nothing premultiplied. 136 of the corpus's 143 soft masks carry `/Matte [0 0 0]`, so ADR 0004's premultiplication statement rests on one producer's habit. |
| `mupdf_explored.pdf` | 285 | layout | The throughput case, and the **negative control** for the OCR fix: a C API manual full of code listings, where a false monospace positive would show up if anywhere. 374k non-space chars. |
| `small-table.pdf` | 1 | layout | Bold/regular span split from the font alone — the layout path has nothing else to take emphasis from |
| `symbol-list.pdf` | 1 | layout | Symbols drawn as vector paths, not glyphs. Only the labels are text; 136 chars is correct. |
| `test_delete_image.pdf` | 1 | ocr | **An image two Form XObjects deep.** This is the file that exposed probe's shallow resource scan. |
| `test-linebreaks.pdf` | 1 | layout | Hyphenation and line-break joining, `/Lang de-DE` |
| `test-rewrite-images.pdf` | 1 | ocr | A re-encoded image stream |
| `test-styled-table.pdf` | 1 | tagged | A **tagged table small enough to verify by hand** — 5 TR, 8 TD, 7 TH. The corpus's tables sit inside a 78,469-element tree. |
| `text-find-ligatures.pdf` | 1 | layout | **The "ecient" case.** The same word twice, with and without a ligature, so the two are directly comparable. Its real font is two Form XObjects deep — the second witness for the probe fix. |
| `type3font.pdf` | 1 | layout | **Type3 glyphs with no recoverable text.** `/CharProcs` drawing streams, `/Differences [0 /0 /1]`. Empty output is the honest answer, and it must not be an error. |
| `v110-changes.pdf` | 4 | layout | Mixed TrueType + Type0 + Type1 in one document |

### PyMuPDF-Utilities (`testdata/pymupdf-utilities/`)

| File | Pages | Path | Covers |
|---|---|---|---|
| `scanned.pdf` | 1 | ocr | The page **before** OCR: one DCT image, no fonts. Empty output is correct. |
| `ocr-ed.pdf` | 1 | layout | The same page **after Tesseract.** Ground truth (§above). |
| `ocr-ed.txt` | — | — | **The expected text.** The only outside statement of a right answer in this repo. |
| `PDF_XChange-OCRed.pdf` | 1 | layout | A **second OCR engine**, invisible layer but not fixed-pitch |
| `test.pdf` | 1 | layout | Minimal Type1 smoke case |

The scanned/OCRed pair is also the clearest statement of what Phase 4 is for: the text is
in the image and this pipeline cannot read it. Both now render, with the same ink percentage,
which is the point — the invisible text layer is invisible.

### Adobe (`testdata/adobe-samples/`)

| File | Pages | Path | Covers |
|---|---|---|---|
| `autotagPDFInput.pdf` | 4 | layout | Adobe's auto-tag input: untagged, 8 images, `/Lang EN-US` |
| `disqualifiedScannedPages.pdf` | 151 | ocr | **No fonts and no images**, 151 pages. Adobe's own disqualified input — nothing to extract and nothing to rasterize. |
| `exportPDFInput.pdf` | 4 | layout | PDF 1.3, TrueType + Type0, 8 images with transparency |
| `extractPdfInput.pdf` | 3 | tagged | **Adobe's Extract reference input.** The file their element-taxonomy docs are written against: 259 elements, 105 P, 5 H, 4 L, 38 LI, 1 Table, 151 MCIDs. |
| `ocrInput.pdf` | 4 | ocr | Adobe's OCR sample: 4 scanned pages, no text layer |
| `sampleInvoice.pdf` | 3 | layout | **The producer-stub witness.** `StructTreeRoot` present, `/MarkInfo /Marked true`, and two `Document` elements holding nothing — 0 headings, 0 paragraphs, 0 MCIDs. Also 5 `/SMask`s with no `/Matte`. |
| `watermark.pdf` | 1 | tagged | Tagged, and the text really is only spaces: every showing operator is `[( )] TJ` inside a `/Span`. 0 characters is correct. |
| `zeroLength.pdf` | 0 | *error* | **0 bytes.** Every corpus file opens; this one must not, and must fail with a reason rather than a panic or an empty success. |

### The CJK line wrap

The second defect these fixtures found, and the clearest argument for having them.

`extract` joined every wrapped line with a space. For Latin that is right and necessary —
a line break is the only thing marking the word boundary. For Chinese it is wrong, because
a CJK line has no boundaries to break at: it fills and wraps wherever it runs out of
measure. So the company name 中诚信国际信用评级有限责任公司, set across three lines in
`chinese-tables.pdf`, came out as:

```
中诚信国际信 用评级有限责 任公司
```

Three words where the page says one. `appendLine` now looks at the characters on either
side of the join and adds a space unless both are Han, kana, Hangul, or CJK punctuation.
Han-to-Han spaces on this page fell from 22 to 7, and the 7 that remain are correct: a
clause number before its title (`第七章 企业资信状况`) and table header cells
(`序号 金融机构名称 授信总额`). Removing those would be the opposite defect, which is why
the test names the four words the wraps fall inside rather than asserting a ratio — a
ratio cannot tell a broken word from a table column, and that distinction is the whole
question.

The decision is per join, not per document, because documents are mixed: this one sets its
prose in Chinese and its rating codes and amounts in Latin, and a line wrapping from one
into the other is a real boundary. That case cannot be expressed with a fixture, so the
predicate has its own unit test in `extract`.

Eleven English-language documents cannot find this, however carefully they are measured.
No Latin measurement in the suite moved when it was fixed.

### The producer-stub heuristic

`sampleInvoice.pdf` earns a paragraph because it justifies a line of code that would
otherwise look wrong. `probe.go` does not route on whether a structure tree exists:

```go
r.Tagged = st.Headings > 0 || st.Paras > 0
```

A `StructTreeRoot` and `/MarkInfo /Marked true` are what a conforming tagged PDF has, so
trusting them looks correct. This file has both and no content under them — a producer
wrote the scaffolding and never filled it in. Trusting the flag takes the tagged path,
finds no MCIDs to join page text to, and emits nothing from a document that has three
pages of visible text. The check was written before this file was in the tree; the file is
an independent witness for it, and it is Adobe's own sample.

---

## Markdown coverage

Adobe's [PDF to Markdown](https://developer.adobe.com/document-services/docs/overview/pdf-to-markdown-api/)
API documents its element mapping, and since it reads the same structure-tree vocabulary
`tag` does, that list is a usable coverage checklist. Where we stand:

| Element | Markdown | Status |
|---|---|---|
| `Title`, `H`, `H1`–`H6` | `#`–`######` | done — level 7+ clamps to 6, since ISO 32000-2 nests clauses deeper than Markdown goes |
| `P`, `ParagraphSpan` | paragraph | done |
| `L`, `LI`, `Lbl`, `LBody` | `- ` | done, nested by level |
| `StyleSpan` | `**`, `*`, `` ` `` | done — from font flags on the layout path too |
| `Figure` | `*alt*` | `/Alt` and `/ActualText` preferred over glyphs |
| `Table`, `TR`, `TH`, `TD` | table | **partial** — cells emit as paragraphs; grid emission is open (`DESIGN.md` §10). `test-styled-table.pdf` and `extractPdfInput.pdf` are the fixtures it will be written against. |
| `Aside`, `Footnote`, `Reference` | — | not mapped |
| `Sect` | — | structural only; `sectionize` uses it, the sink does not mark it |
| `Link` | `[text](uri)` | **not done** — needs `/Annots` threaded through `extract` |
| Images as base64 | — | deliberately not done. `images` writes files; a base64 blob in prose is unreadable either way. |

Adobe also documents what their API refuses, and it is a useful list of what a
Markdown-shaped output cannot represent: hidden objects, JavaScript, optional content
groups, XFA and fillable forms, complex annotations, CAD drawings and vector art,
password-protected content. Our positions differ on two. Hidden text we **do** extract —
an OCR layer is invisible and is the whole content of a scanned page. Encrypted files
probe reports as `encrypted` and routes nowhere.

---

## What the fixtures pin for rendering

`render/pdfium` and the `render` verb test against these files rather than the corpus, for
the usual reason: the fixture tests never skip. Six of them carry a distinct load.

| File | What only this file establishes |
|---|---|
| `adobe-samples/ocrInput.pdf` | 4 visibly different scanned pages, which is what makes the **aliasing** test possible: render page 1, render 2–4, and require page 1 to be unchanged. go-pdfium's WASM adapter hands back a view into linear memory that `Cleanup` frees, so without a copy this fails — verified by reverting the copy and watching it fail. Also the concurrency case and both ends of the 1-based↔0-based conversion. |
| `adobe-samples/disqualifiedScannedPages.pdf` | The **blank negative control.** No fonts and no images, so a correct render is empty, and it bounds every ink assertion from below. It also renders 151 pages of nothing, which is the honest answer probe's routing already implies. |
| `pymupdf/type3font.pdf` | The **smallest page in the population**, 72 × 72 pt, so it exercises the one-pixel floor. It is also the file whose text is unrecoverable and whose *pixels* are not, which is the entire argument for the OCR path existing. |
| `pymupdf/2201.00069.pdf` | A4's fractional point size, so it pins the rounding direction, and the page the pixel cap is measured against. |
| `pymupdf/mupdf_explored.pdf` | 285 pages: the throughput measurement and the **zero-padding width**, which on a document this size is the difference between a usable directory listing and page 100 sorting before page 2. |
| `adobe-samples/zeroLength.pdf` | 0 bytes must fail at `Open` with a reason. pdfium is a second, independent parser, so this is not covered by the extraction path's version of the same test. |

Three gaps worth stating rather than implying. **No page anywhere in the fixtures or the corpus
is rotated** — zero of 1,729 — so `/Rotate` handling rests on four PDFs built by hand during
the spike, not on a file a producer emitted. **No file has an annotation with a visible
appearance stream** either: three carry `/Annots`, and rendering them with annotations on and
off is pixel-identical. That gap had teeth — `-annots` was a no-op for every subtype except
form fields and no fixture could have shown it — so `render/pdfium` builds its own one-page PDF
with a Square annotation in `TestAnnotationsFlag`. It is built rather than committed because
the selection rule above admits only files upstream authored as test inputs. And nothing here
has a `/MediaBox` large enough
to reach the pixel cap; the largest page in the whole population is 630 × 1008 pt, which at
200 DPI is 4.9 Mpx against a 64 Mpx cap. The cap is tested with a synthetic 200 × 200-inch
box in `render`, because that is what a hostile producer writes for free and no honest
document contains.

---

## Running against these

```bash
pdfspec probe testdata/pymupdf/*.pdf              # routing decision per file
pdfspec probe -json -pages testdata/adobe-samples/extractPdfInput.pdf
pdfspec md testdata/pymupdf-utilities/ocr-ed.pdf  # to stdout
pdfspec md -o out.md testdata/pymupdf/small-table.pdf
pdfspec images -o ./img testdata/adobe-samples/sampleInvoice.pdf
pdfspec okf -o ./bundle testdata/adobe-samples/extractPdfInput.pdf
pdfspec render -o ./png testdata/adobe-samples/ocrInput.pdf -pages 1,3
```

```bash
go test ./cmd/pdfspec/ -run TestFixture -v   # the fixture suite; never skips
go test ./... -count=1                       # everything; corpus tests skip without docs/
```

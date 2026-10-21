# pdftools

PDF extraction for Go — text, structure, images, raster, OCR — under MIT, as a library and
a CLI.

```
go install github.com/model-harness/pdftools/cmd/pdfspec@v1.0.0
pdfspec md paper.pdf > paper.md
```

**Status: v1.0.0.** The converter is built: `pdfspec md` reads the file's text and structure,
reads with Tesseract and a vision model what is only in pixels where they are installed, and
writes `[err]` and `[image]` for what nothing could read. The gold set's targets are enforced in
CI. Everything documented below works today.

## Why

The permissive PDF options in Go each solve a slice and stop. `pdfcpu` has an excellent
object layer and no rasterizer. `ledongthuc/pdf` extracts text with 0.01% spaces and a
longest "word" of 4,069 characters. Outside Go, the quality is in Python — which is slow —
or behind AGPL and commercial licences. Measured against the alternatives on the same
17-page paper:

| | non-space chars | words | words >25 ch | longest |
|---|---|---|---|---|
| **pdftools** | 39,049 | 6,343 | 19 (0.30%) | 71 |
| pdftotext (Poppler) | 39,035 | — | — | — |
| pdfplumber 0.11.9 | 39,089 | 2,392 | 377 (15.8%) | 110 |

The three agree on the characters to within 0.14%, which rules out missing text. Where they
disagree is how those characters divide into words: pdfplumber finds 2,392 where this finds
6,343 from the same glyphs, and 15.8% of its words run past 25 characters. That is the
signature of dropped inter-word spaces — a PDF stores glyph positions, not spaces, so the
spaces have to be recovered from font metrics. `TestExtractionQualityOnArXiv` pins those
figures.

**Deterministic first.** Tokens are the most expensive way to read a PDF that already
contains its own text, so the file's text comes first. Tesseract reads what is only in pixels:
a scanned page, an image of text or of a table, a glyph the file gives no value to. A vision
model is asked only what neither can answer: a word Tesseract doubts, a chart, a picture. A
glyph nothing reads is written `[err]`, never guessed.

## CLI

```
pdfspec md      convert a PDF to Markdown
pdfspec okf     convert to an Open Knowledge Format bundle, one file per clause
pdfspec images  extract embedded images, original codec preserved where possible
pdfspec render  rasterize pages to PNG or JPEG
pdfspec ocr     convert to Markdown, recognizing pages that carry no text
pdfspec probe   report what a PDF contains and which extraction path it will take
```

Start with `probe` — it tells you which path a file will take before you spend anything:

```
$ pdfspec probe spec.pdf
spec.pdf
  size        18.31 MB
  version     PDF 1.7
  pages       1023
  encrypted   false
  structure   StructTreeRoot + Marked
  tags        78469 elements, depth 13, 44955 MCIDs
              981 headings, 29400 paras, 745 tables, 195 figures, 531 lists
  roles       P=29400 Span=14911 TD=11036 Link=6047 TH=5432 TR=4324
  lang        en
  streams     ObjStm, XRefStm
  fonts       TrueType, Type0, Type1
  filters     DCTDecode, FlateDecode
  images      224
  path        tagged
  probed in   1294 ms
```

`path` is the answer to look at. `tagged` means the document declares its own heading
hierarchy and reading order, so no heuristics are needed. `layout` means geometry analysis.
`ocr` means there is no text to extract, and reading the pixels is the only option. `probe`
takes several files at once and has `-json` for scripting; run `pdfspec <command> -h` for flags.

`engines` and `needs` say what OCR will do before it runs, for a file whose pixels hold some of
its content:

```
$ pdfspec probe testdata/adobe-samples/autotagPDFInput.pdf
  ...
  path        layout
  unmapped    58 glyph(s) with no Unicode value, written [err]: p1 30, p2 24, p3 1, p4 3
  engines     tesseract 5.5.3 eng; gemma-4-e4b via inferd
  needs       images: p2 2, p3 4, p4 2; glyphs with no value: p1 30, p2 24, p3 1, p4 3
```

```sh
pdfspec md -split -o pages/ report.pdf      # one .md per page
pdfspec okf -o bundle/ spec.pdf             # clause-per-file, stitched across pages
pdfspec images -list scan.pdf               # what is in there, writing nothing
pdfspec render -o png/ -dpi 200 form.pdf    # rasterize
pdfspec ocr scan.pdf                        # text where there is text, Tesseract where there is not
```

## Library

Every verb is a thin shell over a package you can call directly. Interfaces are declared by
the package that consumes them; the borrowed parser is confined to one adapter.

```go
import (
    "os"

    "github.com/model-harness/pdftools/extract"
    pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
    "github.com/model-harness/pdftools/sink/markdown"
)

s, err := pcstore.Open("spec.pdf")
if err != nil {
    return err
}
defer s.Close()

d, err := extract.New(s, extract.DefaultOptions).Document()
if err != nil {
    return err
}
err = markdown.Write(os.Stdout, d, markdown.DefaultOptions)
```

That is the page-ordered path. For a tagged document, `tag.Read` plus
`sectionize.Tagged` reconstructs the clause hierarchy first — `markdown.WriteOutline`
then emits it with real heading levels. `cmd/pdfspec/md.go` shows both paths, flag parsing
included, and `cmd/pdfspec/ocr.go` the engines after them.

| package | what it owns |
|---|---|
| `objects` | the PDF object model and the `Store` interface every layer reads through |
| `objects/pdfcpu` | the borrowed-parser adapter — all pdfcpu types stop here |
| `filter` | Flate, LZW, ASCIIHex, ASCII85, RunLength, PNG/TIFF predictors |
| `content` | content-stream lexer, scanner, and the graphics/text state machine |
| `font`, `font/cmap`, `font/encoding` | metrics, CMaps, Annex D encodings, glyph lists |
| `tag` | structure-tree reader — the tagged path |
| `extract`, `sectionize`, `doc` | text runs, clause reconstruction, the document model |
| `layout` | roles read from geometry: columns, headings, tables, lists, notes, images |
| `image`, `bits` | image XObjects, sub-byte sample unpacking |
| `render`, `render/pdfium`, `render/native` | rasterization: pdfium over WASM by default, and the native rasterizer |
| `ocr`, `ocr/tesseract`, `ocr/gemma` | routing, words to pages, tables and glyphs read off pixels, Tesseract, and inferd's vision model |
| `bench` | the gold set's scorer |
| `internal/inferd` | inferd's v2 wire, borrowed from thlibo |
| `sink/markdown`, `sink/okf` | output formats |

## OCR

Text comes from the file first. Where it holds none, or no value for a glyph, the pixels are
read with [Tesseract](https://github.com/tesseract-ocr/tesseract):

- **A page** whose readable text covers under 5% of it is read whole. It is levelled first, its
  type sizes and weights are read from its letters, and its ruled tables become tables
  (ADRs 0043, 0047). The reading replaces the page only where it adds to what the file gave.
- **An image** is read as what it holds: text as paragraphs, a ruled table as a table. With a
  vision model, a chart becomes a description and a table of its values, and a picture
  `[photo: …]` (ADR 0046). An image nothing reads is written `[image]`.
- **A glyph with no value** is read off the page where its line's own text places it, as "ti"
  in "crea[err]ng" (ADR 0048).

A word Tesseract is less than 30% confident of is written `[err]`. When
[inferd](https://github.com/3rg0n/inferd) is running with a vision model, a word Tesseract is
less than 90% confident of is read again by it and kept only where the two agree; the rest are
`[err]` (ADR 0044). stderr says, per page, what was read and what is written `[err]` or
`[image]`.

`md` does this by default (`-ocr=false` turns it off, `-inferd off` the second reading). `pdfspec ocr` adds the router's knobs:

```sh
pdfspec ocr -dry-run scan.pdf     # which pages need OCR, and why
pdfspec ocr -force scan.pdf       # read every page, whatever its text
```

### Installing the engines

Both are optional. Without them, `md` writes what the file holds, `[err]` for a glyph it gives
no value to, and `[image]` for an image. `pdfspec probe` says which engines it finds.

- **Tesseract 5** with its `eng` model. The figures in `docs/adr/` were measured with 5.5.3 and
  `tessdata_fast`. Install it with `apt install tesseract-ocr` on Debian and Ubuntu,
  `brew install tesseract` on macOS, or the UB Mannheim installer on Windows. It is found on
  `PATH`, or named with `-tesseract` or `PDFSPEC_TESSERACT`, and its models with `-tessdata` or
  `TESSDATA_PREFIX`. `PDFSPEC_TESSERACT=off` runs none.
- **inferd** with a vision model; the figures were measured with `gemma-4-e4b` and its
  projector. It is found at the platform's default address, or named with `-inferd` or
  `PDFSPEC_INFERD`, and `off` asks none. The figures were measured with
  `"mmproj_image_max_tokens": 1120` in inferd's config, the largest of the image budgets
  Google's vision guide lists.

## Building and testing

```sh
go build ./...
go test ./...      # passes with no PDFs on disk at all
```

The test suite draws on three populations, and the split is deliberate:

- **`testdata/reference/`** — the gold set: single-concern PDFs built from the `.tex` and
  `.html` sources committed beside them, each with a hand-written `.gold.md` of what it should
  convert to. `pdfspec bench testdata/reference` scores them, and the v1 targets are means over
  them that fail the build: CER at most 0.03, word recall and precision 0.97, reading order
  0.98, blocks and headings 0.90, tables and style 0.85 (ADR 0029). A fixture whose content is
  only in pixels leaves those means where no Tesseract is installed.
- **`testdata/`** — 30 fixtures from PyMuPDF and Adobe, committed with a
  pinned upstream commit SHA and a SHA-256 per file (`testdata/manifest.json`). A test never
  skips for these. Each one covers something the corpus cannot.
- **`docs/`** — eleven ISO 32000 and PDF Association specifications, where the aggregate
  baselines were measured. **Not in this repository:** they are sponsored copies of paid ISO
  documents and not redistributable. Every test that uses them skips when they are absent,
  which is why a fresh clone passes with no PDFs at all. Drop your own copies in `docs/` to
  run them.

That last population is a good regression baseline and a bad witness — all eleven files
are tagged standards prose from one family of producers, so a threshold tuned on them cannot
be shown to generalize by running them again. `docs/test.docs.md` records which gap each
committed fixture fills.

The engines' tests are opt-in: `PDFSPEC_TEST_TESSERACT=<tesseract>` runs those that need
Tesseract, among them the gold set with OCR, and `PDFSPEC_TEST_INFERD=1` those that need inferd.

## Licence

MIT — see [`LICENSE`](LICENSE).

22 of the 70 files in `testdata/` are AGPL-3.0 upstream, PyMuPDF's; the other 48 are MIT,
Adobe's samples and the gold set generated here. The AGPL files are data in a marked directory
with attribution, not linked code, which does not relicense this project's source. No PyMuPDF or MuPDF code is used, read for copying, or translated;
where their approach informed a decision it is cited in `docs/DESIGN.md` as methodology.

The ISO specifications are not redistributed here in any form, and never were — see
`docs/test.docs.md`.

## Design

- [`docs/DESIGN.md`](docs/DESIGN.md) — why the repo exists, the architecture, the
  borrow-then-replace dependency policy, and the phase roadmap.
- [`docs/adr/`](docs/adr/) — the decisions that were costly to reverse, with the
  measurements that drove them. Accepted ADRs are annotated, never edited.
- [`docs/test.docs.md`](docs/test.docs.md) — the two test populations and what each fixture
  is for.
- [`CHANGELOG.md`](CHANGELOG.md).

Dependencies are borrowed to start and replaced over time, each behind an interface this
project owns so the swap is local. `pdfcpu` supplies the object layer today; pdfium (WASM,
via wazero — no cgo) supplies rasterization. See ADR 0001.

# pdftools

Most PDFs already contain their own text. The trouble is getting it out in a form you can
use. A PDF stores where each glyph is drawn, not words or paragraphs or tables, so all of that
has to be rebuilt from the page's geometry. The tools that do this well are mostly in Python,
which is slow, or under AGPL or commercial licences. The permissive ones in Go each handle a
slice of the job and stop. The fashionable alternative is to hand the page to a vision model.
That costs far more, takes far longer, and when the model can't read something it guesses.

`pdfspec` takes the other route. It turns a PDF into Markdown that a person, or a model you
hand it to later, can read. To do that it reads the text the file already contains, and it
rebuilds the structure from the page: headings, paragraphs, columns, tables, lists, footnotes
and links. It only looks at pixels when the file gives it nothing else to go on. When it can't
read something, it says so with `[err]` or `[image]` instead of making something up.

```sh
go install github.com/model-harness/pdftools/cmd/pdfspec@v1.0.0
pdfspec md paper.pdf > paper.md
```

It's written in Go, released under MIT, and comes as a CLI and as a library.

## Why another PDF tool

On a 17-page paper, here is how `pdfspec` compares with two of the usual choices:

| | non-space chars | words | words >25 ch | longest |
|---|---|---|---|---|
| **pdftools** | 39,049 | 6,343 | 19 (0.30%) | 71 |
| pdftotext (Poppler) | 39,035 | — | — | — |
| pdfplumber 0.11.9 | 39,089 | 2,392 | 377 (15.8%) | 110 |

All three find the same characters, to within 0.14%, so none of them is missing text. They
differ in how they divide those characters into words. pdfplumber finds 2,392 words where
`pdfspec` finds 6,343, and 15.8% of its "words" run past 25 characters. That is what dropped
spaces look like. A PDF stores no spaces between words, so a reader has to work them out from
the fonts' metrics, and most get it wrong. `TestExtractionQualityOnArXiv` keeps those figures
honest.

In Go the picture is worse. `pdfcpu` has an excellent object layer but can't draw a page, and
`ledongthuc/pdf` finds spaces in 0.01% of its text, with a longest "word" of 4,069
characters.

## How it reads a PDF

`pdfspec` trusts its sources in a fixed order, because each one is cheaper and more reliable
than the next.

1. **The file itself comes first.** Most of the time the text and its structure are all there.
   A tagged PDF declares its own headings and reading order. An untagged one gets them rebuilt
   from the page's geometry.
2. **Tesseract reads what exists only as pixels**, when it's installed. That covers a scanned
   page, an image of text or of a table, and a glyph whose font never says which character it
   is.
3. **A vision model answers only what Tesseract can't.** It is asked to re-read a word Tesseract
   wasn't sure of, to describe a chart or a picture, and to read the values off a chart.

Anything left unread is marked rather than guessed. A glyph nobody could read becomes `[err]`,
and an image nobody could read becomes `[image]`. Every run reports, page by page, how many of
each it wrote, so you know where to look.

## Using the CLI

Start with `probe`. It tells you what a file holds and how it will be read, before you spend
anything converting it:

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

The line to read is `path`:

- `tagged` means the document declares its own headings and reading order, so nothing has to
  be inferred.
- `layout` means the structure will be rebuilt from geometry.
- `ocr` means there is no text to extract, and the pixels are the only option.

For a file that keeps some of its content in pixels, `probe` also says which engines it found,
and what each page will need from them:

```
$ pdfspec probe testdata/adobe-samples/autotagPDFInput.pdf
  ...
  path        layout
  unmapped    58 glyph(s) with no Unicode value, written [err]: p1 30, p2 24, p3 1, p4 3
  engines     tesseract 5.5.3 eng; gemma-4-e4b via inferd
  needs       images: p2 2, p3 4, p4 2; glyphs with no value: p1 30, p2 24, p3 1, p4 3
```

`probe` accepts several files at once and has `-json` for scripts.

`md` is the conversion itself. The other verbs cover the jobs around it:

```sh
pdfspec md paper.pdf > paper.md             # the whole pipeline
pdfspec md -split -o pages/ report.pdf      # one .md per page
pdfspec okf -o bundle/ spec.pdf             # one file per clause, stitched across pages
pdfspec images -list scan.pdf               # what images are in there, writing nothing
pdfspec render -o png/ -dpi 200 form.pdf    # rasterize pages to PNG or JPEG
pdfspec ocr scan.pdf                        # md, with the OCR router's knobs exposed
```

Run `pdfspec <command> -h` for each verb's flags.

## When the pixels matter

Born-digital documents rarely need more than the file, but scans, screenshots and odd fonts do.
That is where Tesseract comes in, and `md` uses it by default whenever it's installed:

- **A page with almost no text**, under 5% of its area, is read whole. It is levelled first if
  the scan was placed crooked. Its type sizes and weights are read from the letters, and its
  ruled tables come out as tables (ADRs 0043, 0047). The reading replaces the page only if it
  adds something the file didn't already give.
- **An image** is read as what it holds. Text becomes paragraphs and a ruled table becomes a
  table. With a vision model, a chart gets a description and a table of its values, and a photo
  becomes `[photo: …]` (ADR 0046).
- **A glyph the file never names**, such as the "ti" ligature in "crea[err]ng", is read off the
  page. It is filled in only where the words around it on that line pin down what it must be
  (ADR 0048).

Tesseract reports how confident it is about each word, and that decides what is kept. A word
it rates under 30% is written `[err]`. If [inferd](https://github.com/3rg0n/inferd) is running
with a vision model, the bar rises: a word under 90% is read a second time by the model and kept
only when the two readings agree (ADR 0044).

To turn this off, pass `-ocr=false`; `-inferd off` keeps Tesseract but skips the second reading.
The `ocr` verb exposes the router itself:

```sh
pdfspec ocr -dry-run scan.pdf     # which pages need OCR, and why
pdfspec ocr -force scan.pdf       # read every page, whatever text it has
```

### Installing the engines

Both engines are optional. Without them, `md` still writes everything the file holds, with
`[err]` and `[image]` marking what it couldn't read. Run `pdfspec probe` to see which ones it
finds.

**Tesseract 5**, with the `eng` model. The figures in `docs/adr/` were measured with 5.5.3 and
`tessdata_fast`. To install it:

- Debian and Ubuntu: `apt install tesseract-ocr`
- macOS: `brew install tesseract`
- Windows: the UB Mannheim installer

`pdfspec` looks for it on `PATH`. To point it elsewhere, use `-tesseract` or
`PDFSPEC_TESSERACT`, and for the models, `-tessdata` or `TESSDATA_PREFIX`.
`PDFSPEC_TESSERACT=off` disables it.

**inferd**, with a vision model. The figures were measured with `gemma-4-e4b` and its projector,
with `"mmproj_image_max_tokens": 1120` in inferd's config. That is the largest of the image
budgets Google's vision guide lists. `pdfspec` finds inferd at the platform's default address.
To point it elsewhere, use `-inferd` or `PDFSPEC_INFERD`, and set it to `off` to disable it.

## Using it as a library

Every verb is a thin shell over packages you can call yourself. This is the shortest path from
a file to Markdown:

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

That writes the page-ordered text. For a tagged document, `tag.Read` and `sectionize.Tagged`
rebuild the clause hierarchy first, and `markdown.WriteOutline` writes it with real heading
levels. `cmd/pdfspec/md.go` shows both paths, flag parsing included, and `cmd/pdfspec/ocr.go`
shows how the engines run after them.

Each package declares the interfaces it consumes, and the borrowed parser is confined to a
single adapter:

| package | what it owns |
|---|---|
| `objects` | the PDF object model, and the `Store` interface every layer reads through |
| `objects/pdfcpu` | the adapter for the borrowed parser; no pdfcpu type leaves it |
| `filter` | Flate, LZW, ASCIIHex, ASCII85, RunLength, PNG/TIFF predictors |
| `content` | the content-stream lexer and scanner, and the graphics and text state machine |
| `font`, `font/cmap`, `font/encoding` | metrics, CMaps, Annex D encodings, glyph lists |
| `tag` | the structure-tree reader, which is the tagged path |
| `extract`, `sectionize`, `doc` | text runs, clause reconstruction, the document model |
| `layout` | roles read from geometry: columns, headings, tables, lists, notes, images |
| `image`, `bits` | image XObjects, and unpacking samples narrower than a byte |
| `render`, `render/pdfium`, `render/native` | rasterization, by pdfium over WASM by default, or by the native rasterizer |
| `ocr`, `ocr/tesseract`, `ocr/gemma` | routing pages to the engines, and turning what they read into pages, tables and glyphs |
| `bench` | the scorer for the gold set |
| `internal/inferd` | inferd's v2 wire protocol, borrowed from thlibo |
| `sink/markdown`, `sink/okf` | the output formats |

## How we know it works

```sh
go build ./...
go test ./...      # passes with no PDFs on disk at all
```

The tests measure the converter against three sets of documents. Each set covers what the
others can't.

**The gold set**, in `testdata/reference/`, sets the bar. Each of its PDFs tests one thing, is
built from a `.tex` or `.html` source committed beside it, and comes with a hand-written
`.gold.md` of what it should convert to. `pdfspec bench testdata/reference` scores the
converter against them. The v1 targets are averages over the set, and missing any of them fails
the build (ADR 0029):

- character error rate at most 0.03
- word recall and word precision at least 0.97
- reading order at least 0.98
- blocks and headings at least 0.90
- tables and style at least 0.85

CI checks all of them on Linux and Windows. A few fixtures hold their content only in pixels.
Those count toward the averages only where Tesseract is installed.

**Thirty fixtures from PyMuPDF and Adobe**, in `testdata/`, cover cases the other sets don't.
Each is pinned to the upstream commit it came from and to a SHA-256 in
`testdata/manifest.json`, and no test ever skips them.

**Eleven ISO 32000 and PDF Association specifications**, in `docs/`, are where the larger
baselines were measured. They are sponsored copies of paid ISO documents, so they can't be
redistributed and aren't in this repository. The tests that use them skip when the files are
absent, which is why a fresh clone passes with no PDFs at all. To run those tests, drop your own
copies in `docs/`.

The specifications make a good regression baseline but a poor witness. All eleven are tagged
standards prose from one family of producers, so a threshold tuned on them proves nothing about
other documents. That is why the other two sets exist. `docs/test.docs.md` records which gap
each committed fixture fills.

The tests that need an engine are opt-in. Set `PDFSPEC_TEST_TESSERACT=<path to tesseract>` to
run the Tesseract tests, which include the gold set with OCR, and set `PDFSPEC_TEST_INFERD=1` to
run the inferd tests.

## Licence

MIT; see [`LICENSE`](LICENSE).

Of the 70 files in `testdata/`, 48 are MIT: Adobe's samples, and the gold set generated here.
The other 22 come from PyMuPDF and are AGPL-3.0 upstream. They are kept as data, in a marked
directory with attribution, and are not linked as code, so they don't change this project's
licence. No PyMuPDF or MuPDF code is used, read in order to copy it, or translated. Where their
approach informed a decision, `docs/DESIGN.md` cites it as methodology.

The ISO specifications are not redistributed here in any form, and never have been; see
`docs/test.docs.md`.

## Design and history

- [`docs/DESIGN.md`](docs/DESIGN.md) explains why the project exists, its architecture, how it
  manages dependencies, and the roadmap.
- [`docs/adr/`](docs/adr/) records the decisions that were costly to reverse, with the
  measurements behind them. Accepted ADRs are annotated, never edited.
- [`docs/test.docs.md`](docs/test.docs.md) describes the test populations and what each fixture
  is for.
- [`CHANGELOG.md`](CHANGELOG.md) lists what changed in each release.

Dependencies are borrowed at first and replaced over time, each behind an interface this
project owns, so swapping one out stays a local change. Today `pdfcpu` supplies the object
layer, and pdfium supplies rasterization, compiled to WASM and run on wazero, so no cgo is
needed. See ADR 0001.

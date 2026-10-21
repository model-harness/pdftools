# pdftools

`pdfspec` converts a PDF into Markdown. It reads the text the file already holds, and it marks
what it cannot read with `[err]` or `[image]` instead of guessing.

```sh
go install github.com/model-harness/pdftools/cmd/pdfspec@v1.0.0
pdfspec md paper.pdf > paper.md
```

The project is written in Go and released under the MIT licence. You can use it as a command
or as a library.

## Why it exists

Most PDFs already hold their own text. A PDF records only where each letter sits on the page,
though. It has no words, paragraphs or tables, so a converter has to rebuild them from the
layout.

Existing tools today are mostly built in Python, which is slow, or they use licences that
restrict reuse, such as AGPL or a commercial licence. The permissive Go libraries each handle
part of the job. Many people now send each page to a vision model instead. `docs/DESIGN.md`
puts reading the file's own text at two orders of magnitude cheaper than a call to a language
model, and a model guesses when it cannot read something.

`pdfspec` reads the file first and rebuilds the structure: headings, paragraphs, columns,
tables, lists, footnotes and links. It reads pixels only where the file holds no text.

We ran `pdfspec` and two common tools on the same 17-page paper:

| | non-space chars | words | words >25 ch | longest |
|---|---|---|---|---|
| pdftools | 39,049 | 6,343 | 19 (0.30%) | 71 |
| pdftotext (Poppler) | 39,035 | — | — | — |
| pdfplumber 0.11.9 | 39,089 | 2,392 | 377 (15.8%) | 110 |

The three tools find the same characters, within 0.14% of each other, so none of them loses
text. They split those characters into words differently. pdfplumber finds 2,392 words where
`pdfspec` finds 6,343, and 15.8% of pdfplumber's words run past 25 characters. Missing spaces
cause this. A PDF stores no spaces between words, so a converter has to infer them from the
font measurements. `TestExtractionQualityOnArXiv` checks these figures on every test run.

Two Go libraries show the gap. `pdfcpu` reads a PDF's objects well but cannot draw a page.
`ledongthuc/pdf` finds spaces in 0.01% of its text, and its longest "word" runs to 4,069
characters.

## How it reads a PDF

`pdfspec` uses three sources in a fixed order. Each source is cheaper and more reliable than
the next one.

1. It reads the file first. A tagged PDF declares its own headings and reading order. For an
   untagged PDF, `pdfspec` infers them from the layout.
2. Tesseract, an open-source text recognizer, reads what exists only as pixels, if it is
   installed. That covers a scanned page, a picture of text or of a table, and a letter whose
   font never says which character it is.
3. A vision model answers what Tesseract cannot. It checks a word Tesseract doubted, describes
   a chart or a photo, and reads the values off a chart.

`pdfspec` marks whatever is left unread. A letter it could not read becomes `[err]`, and an
image it could not read becomes `[image]`. Each run reports both counts for every page, so you
know where to look.

## Using the command

Run `probe` first. It reports what a file holds and how `pdfspec` will read it, without
converting anything:

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

The `path` line gives the answer:

- `tagged` means the document declares its own headings and reading order.
- `layout` means `pdfspec` will rebuild the structure from the layout.
- `ocr` means the file holds no text, so only its pixels can be read.

For a file that keeps some content in pixels, `probe` also names the engines it found. It lists
what each page will ask of them:

```
$ pdfspec probe testdata/adobe-samples/autotagPDFInput.pdf
  ...
  path        layout
  unmapped    58 glyph(s) with no Unicode value, written [err]: p1 30, p2 24, p3 1, p4 3
  engines     tesseract 5.5.3 eng; gemma-4-e4b via inferd
  needs       images: p2 2, p3 4, p4 2; glyphs with no value: p1 30, p2 24, p3 1, p4 3
```

`probe` accepts several files at once, and `-json` gives output for scripts.

`md` runs the conversion. The other commands cover related jobs:

```sh
pdfspec md paper.pdf > paper.md             # the whole pipeline
pdfspec md -split -o pages/ report.pdf      # one .md per page
pdfspec okf -o bundle/ spec.pdf             # one file per clause, stitched across pages
pdfspec images -list scan.pdf               # what images are in there, writing nothing
pdfspec render -o png/ -dpi 200 form.pdf    # rasterize pages to PNG or JPEG
pdfspec ocr scan.pdf                        # md, with the page router's settings exposed
```

`okf` writes the Open Knowledge Format, one file per clause. `pdfspec <command> -h` lists each
command's flags.

## Reading pixels

`md` runs Tesseract by default whenever it is installed. Tesseract reads three kinds of
content:

- A page whose text covers under 5% of its area is read whole. `pdfspec` straightens a crooked
  scan first. It reads type sizes and weights from the letters, and it turns ruled tables into
  tables (ADRs 0043 and 0047). The new reading replaces the page only where it adds text the
  file lacked.
- An image is read as what it holds. Text becomes paragraphs, and a ruled table becomes a
  table. With a vision model, a chart gets a description and a table of its values, and a
  photo becomes `[photo: …]` (ADR 0046).
- A letter the file never names is read off the page, such as the "ti" ligature in
  "crea[err]ng". `pdfspec` fills it in only where the words around it on that line fix what it
  must be (ADR 0048).

Tesseract rates its confidence in each word, and the rating decides what `pdfspec` keeps. A
word rated under 30% becomes `[err]`. When [inferd](https://github.com/3rg0n/inferd) runs a
vision model, the bar rises to 90%. The model re-reads each word under 90%, and `pdfspec` keeps
the word only when both readings agree (ADR 0044).

`-ocr=false` turns this off. `-inferd off` keeps Tesseract and skips the second reading. The
`ocr` command exposes the page router:

```sh
pdfspec ocr -dry-run scan.pdf     # which pages need OCR, and why
pdfspec ocr -force scan.pdf       # read every page, whatever text it has
```

### Installing the engines

Both engines are optional. Without them, `md` still writes everything the file holds, and it
marks what it could not read with `[err]` and `[image]`. `pdfspec probe` shows which engines it
finds.

Tesseract 5 needs its `eng` model. The figures in `docs/adr/` came from version 5.5.3 with
`tessdata_fast`. Install it with one of these:

- Debian and Ubuntu: `apt install tesseract-ocr`
- macOS: `brew install tesseract`
- Windows: the UB Mannheim installer

`pdfspec` looks for Tesseract on `PATH`. `-tesseract` or `PDFSPEC_TESSERACT` names another
location, and `-tessdata` or `TESSDATA_PREFIX` names its models. `PDFSPEC_TESSERACT=off`
disables it.

inferd needs a vision model. The figures came from `gemma-4-e4b` and its projector, with
`"mmproj_image_max_tokens": 1120` in inferd's config. Google's vision guide lists 1120 as the
largest image budget. `pdfspec` looks for inferd at the platform's default address. `-inferd`
or `PDFSPEC_INFERD` names another address, and `off` disables it.

## Using it as a library

Each command calls packages you can call yourself. This code converts a file to Markdown:

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

That code writes the text in page order. For a tagged document, `tag.Read` and
`sectionize.Tagged` rebuild the clause hierarchy, and `markdown.WriteOutline` writes it with
real heading levels. `cmd/pdfspec/md.go` shows both paths with their flags.
`cmd/pdfspec/ocr.go` shows how the engines run after them.

Each package declares the interfaces it consumes. One adapter holds all the code for the
borrowed parser:

| package | what it owns |
|---|---|
| `objects` | the PDF object model, and the `Store` interface every layer reads through |
| `objects/pdfcpu` | the adapter for the borrowed parser; no pdfcpu type leaves it |
| `filter` | Flate, LZW, ASCIIHex, ASCII85, RunLength, PNG/TIFF predictors |
| `content` | the content-stream lexer and scanner, and the graphics and text state machine |
| `font`, `font/cmap`, `font/encoding` | metrics, CMaps, Annex D encodings, glyph lists |
| `tag` | the structure-tree reader, which is the tagged path |
| `extract`, `sectionize`, `doc` | text runs, clause reconstruction, the document model |
| `layout` | roles read from the layout: columns, headings, tables, lists, notes, images |
| `image`, `bits` | image XObjects, and unpacking samples narrower than a byte |
| `render`, `render/pdfium`, `render/native` | drawing pages: pdfium over WebAssembly by default, or the native rasterizer |
| `ocr`, `ocr/tesseract`, `ocr/gemma` | routing pages to the engines, and turning what they read into pages, tables and letters |
| `bench` | the scorer for the gold set |
| `internal/inferd` | inferd's v2 wire protocol, borrowed from thlibo |
| `sink/markdown`, `sink/okf` | the output formats |

## How we test it

```sh
go build ./...
go test ./...      # passes with no PDFs on disk at all
```

The tests use three sets of documents, and each set covers cases the others miss.

The gold set in `testdata/reference/` sets the bar. Each PDF there tests one thing. We built
each one from a `.tex` or `.html` source committed beside it, and wrote a `.gold.md` by hand
with the expected output. `pdfspec bench testdata/reference` scores the converter against
them. A build fails if the averages over the set miss any of these targets (ADR 0029):

- character error rate (CER) at most 0.03
- word recall and word precision at least 0.97
- reading order at least 0.98
- blocks and headings at least 0.90
- tables and style at least 0.85

Continuous integration (CI) checks every target on Linux and Windows. A few fixtures hold their
content only in pixels, and they count toward the averages only where Tesseract is installed.

Thirty fixtures in `testdata/` come from PyMuPDF and Adobe. Each one is pinned to its upstream
commit and to a SHA-256 hash in `testdata/manifest.json`. No test skips them.

Eleven ISO 32000 and PDF Association specifications in `docs/` hold the larger baselines.
Because they are sponsored copies of paid ISO documents that we cannot redistribute, this
repository leaves them out. The tests that use them skip when the files are absent, which is why
a fresh clone passes with no PDFs at all. To run those tests, copy your own files into `docs/`.

The specifications make a good regression baseline but a weak test of anything new. All eleven
are tagged standards documents from one family of producers. A threshold tuned on them proves
nothing about other documents, so the other two sets exist. `docs/test.docs.md` records which
gap each committed fixture fills.

The tests that need an engine are opt-in. `PDFSPEC_TEST_TESSERACT=<path to tesseract>` runs the
Tesseract tests, including the gold set with text recognition. `PDFSPEC_TEST_INFERD=1` runs the
inferd tests.

## Licence

The code is MIT-licensed; see [`LICENSE`](LICENSE).

Of the 70 files in `testdata/`, 48 are MIT: Adobe's samples, and the gold set generated here.
The other 22 come from PyMuPDF and are AGPL-3.0 upstream. They sit in a marked directory with
attribution, as data rather than linked code, so they leave this project's licence unchanged.
No PyMuPDF or MuPDF code is used here, whether copied, read in order to copy, or translated.
Where their approach informed a decision, `docs/DESIGN.md` cites it as methodology.

This repository has never redistributed the ISO specifications in any form; see
`docs/test.docs.md`.

## Design and history

- [`docs/DESIGN.md`](docs/DESIGN.md) explains why the project exists, its architecture, how it
  manages dependencies, and the roadmap.
- [`docs/adr/`](docs/adr/) records each decision that was costly to reverse, with the
  measurements behind it. We annotate accepted records and never edit them.
- [`docs/test.docs.md`](docs/test.docs.md) describes the test sets and what each fixture is
  for.
- [`CHANGELOG.md`](CHANGELOG.md) lists the changes in each release.

The project borrows each dependency at first and replaces it later. Each one sits behind an
interface this project owns, so a swap stays a local change. Today `pdfcpu` supplies the object
layer. pdfium supplies page drawing, compiled to WebAssembly and run on wazero, so the build
needs no C toolchain. See ADR 0001.

# pdftools

PDF extraction for Go — text, structure, images, raster, OCR — under MIT, as a library and
a CLI.

```
go install github.com/model-harness/pdftools/cmd/pdfspec@v0.2.0
pdfspec md paper.pdf > paper.md
```

**Status: v0.2.0, pre-1.0.** Phases 1–4 of `docs/DESIGN.md` are implemented and tested.
Everything documented below works today; the API is not stable yet, so pin the tag —
`@v0.2.0` rather than `@latest` — if you depend on it.

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
contains its own text. A model is invoked only for raster-only pages with no text layer,
decided per page rather than per document.

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
`ocr` means there is no text to extract and a model is the only option. `probe` takes
several files at once and has `-json` for scripting; run `pdfspec <command> -h` for flags.

```sh
pdfspec md -split -o pages/ report.pdf      # one .md per page
pdfspec okf -o bundle/ spec.pdf             # clause-per-file, stitched across pages
pdfspec images -list scan.pdf               # what is in there, writing nothing
pdfspec render -o png/ -dpi 200 form.pdf    # rasterize
pdfspec ocr scan.pdf                        # text where there is text, model where there is not
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
then emits it with real heading levels. `cmd/pdfspec/md.go` shows both paths in under 200
lines, flag parsing included.

| package | what it owns |
|---|---|
| `objects` | the PDF object model and the `Store` interface every layer reads through |
| `objects/pdfcpu` | the borrowed-parser adapter — all pdfcpu types stop here |
| `filter` | Flate, LZW, ASCIIHex, ASCII85, RunLength, PNG/TIFF predictors |
| `content` | content-stream lexer, scanner, and the graphics/text state machine |
| `font`, `font/cmap`, `font/encoding` | metrics, CMaps, Annex D encodings, glyph lists |
| `tag` | structure-tree reader — the tagged path |
| `extract`, `sectionize`, `doc` | text runs, clause reconstruction, the document model |
| `image`, `bits` | image XObjects, sub-byte sample unpacking |
| `render`, `render/pdfium` | rasterization over a pdfium WASM adapter |
| `ocr`, `ocr/doctags`, `ocr/ipc`, `ocr/docd` | per-page routing, DocTags parsing, the model host |
| `sink/markdown`, `sink/okf` | output formats |

## OCR

`pdfspec ocr` extracts text deterministically and sends only pages whose text coverage
falls below `-threshold` to a vision model — granite-docling-258M (Apache-2.0) over an
NDJSON IPC wire, wire-compatible with `inferd`. `-dry-run` reports which pages would be
sent without loading anything:

```sh
pdfspec ocr -dry-run scan.pdf     # which pages need a model, and why
pdfspec ocr -addr /tmp/model.sock scan.pdf   # use an already-running host
```

Without `-addr`, a host is started in-process, which downloads a GGUF on first use.

## Building and testing

```sh
go build ./...
go test ./...      # passes with no PDFs on disk at all
```

The test suite draws on two populations, and the split is deliberate:

- **`testdata/`** — 37 fixtures from PyMuPDF, Adobe, and docling-core, committed with a
  pinned upstream commit SHA and a SHA-256 per file (`testdata/manifest.json`). A test never
  skips for these. Each one covers something the corpus cannot.
- **`docs/`** — eleven ISO 32000 and PDF Association specifications, where the aggregate
  baselines were measured. **Not in this repository:** they are sponsored copies of paid ISO
  documents and not redistributable. Every test that uses them skips when they are absent,
  which is why a fresh clone passes with no PDFs at all. Drop your own copies in `docs/` to
  run them.

That second population is a good regression baseline and a bad witness — all eleven files
are tagged standards prose from one family of producers, so a threshold tuned on them cannot
be shown to generalize by running them again. `docs/test.docs.md` records which gap each
committed fixture fills.

## Licence

MIT — see [`LICENSE`](LICENSE).

22 of the 37 test fixtures in `testdata/` are AGPL-3.0 upstream; the other 15 are MIT. They
are data in a marked directory with attribution, not linked code, which does not relicense
this project's source. No PyMuPDF or MuPDF code is used, read for copying, or translated;
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

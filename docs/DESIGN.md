# pdf-spec — Design

Status: draft · 2026-08-02 · License: MIT

High-quality, efficient PDF tooling for Go and Rust — libraries and CLIs you can consume
without banging your head on the wall.

---

## 1. Why this repo exists

The PDF library space is bimodal. Python has breadth but poor quality and poor speed —
pdfplumber emits 6.39% of words longer than 25 characters, which is the signature of
missing inter-word spaces. Everything decent outside Python is Apache-with-strings,
AGPL, or a commercial EULA. The genuinely permissive Go options each solve a slice and
stop: `pdfcpu` has an excellent object layer and no rasterizer; `ledongthuc/pdf` extracts
text at 0.01% spaces with a longest "word" of 4,069 characters; `gopdf` is fast but does
text only, and not all of it.

Baseline to beat, measured on a 6.89 MB arXiv paper in a sibling project:

| | Python (pdfplumber) | gopdf v0.9.5 | ledongthuc |
|---|---|---|---|
| time | 2.29 s | 14.6 ms (156×) | 1.09 s |
| chars | 87,623 | 95,801 | 76,278 |
| spaces | — | 19.15% | 0.01% |
| words >25 ch | 500 (6.39%) | 13 (0.11%) | — |
| longest word | 89 | 47 | 4,069 |
| binary cost | — | +0.95 MB | +1.6 MB |

The 14.6 ms figure proves the ceiling: deterministic extraction is three orders of
magnitude cheaper than the Python path and two orders cheaper than an LLM call. The
quality gap is not a speed/quality tradeoff — it is unfinished work in font and layout
handling.

### Non-AI-first

Tokens are the most expensive way to read a PDF that already contains its own text.
Deterministic extraction is the default path and handles the overwhelming majority of
real documents. LLM/VLM inference is reserved for the one case where determinism
genuinely fails: raster-only pages with no text layer. That is a routing decision made
per page, not a whole-document mode.

---

## 2. First deliverable

A processor that converts a PDF to Markdown:

- Default: one `.md` for the document.
- `--split`: one `.md` per page.
- `--frontmatter`: YAML frontmatter, off by default.
- `okf` verb: section-aware output as an Open Knowledge Format bundle, where sections are
  the unit and are stitched across page boundaries.

The forcing function is converting the PDF 2.0 spec corpus already sitting in `docs/`
into OKF, so a model can query the spec to build the native libraries that come later.
That is deliberate: **the toolkit's first job is to read the specification it will then
be built from.**

---

## 3. The finding that shaped the architecture

Every ISO spec PDF in `docs/` is **tagged**. Measured with `pdfspec probe`:

```
                                    MB  Pages  Struct  Marked  Elements  Head  Paras  Table  MCIDs  Depth  Path
ISO_32000-2_sponsored_EC3.pdf    18.31   1023       Y       Y     78469   981  29400    745  44955     13  tagged
ISO-TS-32005-2023-sponsored.pdf   1.92     49       Y       Y      6266    27    698      5  10102     11  tagged
Well-Tagged-PDF-WTPDF-1.0.pdf     0.59     57       Y       Y      2061   183    789     10   2035      8  tagged
ISO-TS-32004-2024_sponsored.pdf   1.24     25       Y       n      1149    55    368      9   1614     11  tagged
ISO_TS_32002-2022_sponsored.pdf   0.34     14       Y       Y       645    14    171      4    608     11  tagged
ISO_TS_32003-2023_sponsored.pdf   0.91     13       Y       n       394    11    175      4    454     11  tagged
PDF20_AN003-ObjectMetadata.pdf    0.66     10       Y       Y       333    22     98      5    416      9  tagged
ISO_TS_32001-2022_sponsored.pdf   0.35     14       Y       Y       322    14    112      1    329     11  tagged
PDF-Declarations.pdf              0.17     10       Y       Y       311    22     91      3    232      9  tagged
PDF20_AN002-AF.pdf                0.15     14       Y       Y       259    32    101      0    242      4  tagged
PDF20_AN001-BPC.pdf               0.17      5       Y       Y        83     8     35      0     84      4  tagged
LightOnOCR-2601.14251v1.pdf      12.15     17       n       n         -     -      -      -      -      -  layout
```

All are PDF 1.7 and unencrypted. Every tagged file uses object streams and xref
streams; the untagged preprint uses a plain xref table, which is a fair proxy for its age
and producer.

Two corrections to an earlier byte-level estimate of this corpus, both found by running the
real parser:

- **`/Marked` is not universal.** ISO-TS-32004 and ISO-TS-32003 have a `/StructTreeRoot`
  but no `/MarkInfo << /Marked true >>`. Per ISO 32000-2 §14.7.1 that combination is
  technically non-conformant, yet the structure trees are substantive (1,149 and 394
  elements). So **the tagged path must key off `/StructTreeRoot` and tree contents, never
  off `/Marked`** — gating on the conformance flag would push two spec documents onto the
  wrong path.
- **ISO 32000-2 is 1,023 pages and ISO-TS-32005 is 49, not 98.** Counting `/Type /Page`
  occurrences in raw bytes double-counts, because object streams hold page objects that
  the xref table supersedes. Only a real page-tree walk gives the true count.

A `/StructTreeRoot` is a document-level tree of logical elements — `H1`…`H6`, `P`,
`Table`, `L`/`LI`, `Figure`, `Code` — in reading order. Three consequences, all of which
remove work rather than add it:

1. **Heading hierarchy is declared, not inferred.** No font-size clustering heuristics.
2. **Reading order is declared.** No column-detection heuristics, no x/y sorting.
3. **Sections are not page-scoped.** The tree spans the whole document, so page boundaries
   simply do not appear in it. Stitching a clause across pages 412–414 is not a step in the
   pipeline; there is nothing to stitch.

So the primary path for the target corpus is: **parse the structure tree, not the page
geometry.** Geometry-based layout analysis is the *fallback* for untagged input (like the
LightOnOCR paper above), and VLM/OCR is the fallback for that. This inverts the usual
build order, and it is why the first deliverable is achievable without a rasterizer.

### Sections come from the heading sequence, not from containers

An earlier draft of this document said a clause is "one contiguous subtree," which would
have made `sectionize` a tree walk collecting `Sect` containers. Measuring ISO 32000-2
disproves it:

```
Sect   total=7      spanning=0     widest=0 pages
Part   total=10     spanning=6     widest=985 pages
Table  total=745    spanning=2     widest=4 pages
L      total=531    spanning=10    widest=7 pages
P      total=29400  spanning=0     widest=0 pages
anchored 76976 of 78469 elements across 1023 pages

heading parents: map[Document:17 Part:964]
headings with element children: 15, without: 966
widest element: Part with 13442 direct kids
  first kids: H1 P P P P P P P P P
```

Seven `Sect` elements against **981 headings**, and one `Part` holding 13,442 direct
children in a flat `H1 P P P P …` stream. The document declares hierarchy through *heading
levels*, not through nesting: 966 of 981 headings have no element children at all, so a
clause's body is its heading's **following siblings**, not its descendants.

Two consequences for `sectionize`:

- **Boundaries are derived from the heading sequence in logical order.** Each heading opens
  a section that runs until the next heading of equal-or-higher level. Container elements
  (`Part`, `Div`, `Sect`) are traversed for reading order and used as the `H`-depth basis,
  never as section delimiters. A container-driven implementation would emit 7 sections from
  a 1,023-page standard.
- **Heading text comes from content, not from `/T`.** Every heading in ISO 32000-2 has an
  empty `/T`, so titles must be resolved by joining the heading's MCIDs to page text. The
  `Title` attribute is an optimization when present, not the source of truth.

This does not weaken the tagged path — reading order and heading level are still declared,
which is the expensive part. It changes only where the boundary comes from, and it is
better measured now than after `sink/okf` is written against the wrong shape.

The design must not over-fit to this. All three paths ship; tagged is just first and
cheapest.

### Corpus availability

The 11 spec PDFs above are **sponsored copies of paid ISO documents and are gitignored** —
they are not redistributable, so they are not in this repository. Obtain your own copies
and place them in `docs/` to run the golden corpus. The ISO 32000-2 sponsored copies are
published for download by the PDF Association; the TS documents and PDF 2.0 application
notes come from the same source.

`docs/LightOnOCR-2601.14251v1.pdf` **is** tracked, deliberately: it is a public arXiv
preprint, and its untagged 17 pages are the fixture for the untagged and OCR paths. Every
other row in the table above is tagged, so without it the fallback paths would have no test
input at all.

---

## 4. Architecture

Clean architecture with Go-idiomatic dependency inversion: **interfaces are declared by
the package that consumes them**, adapters live in subpackages beneath. No `ports/`
directory, no interface-per-struct ceremony. Dependencies point inward only; the domain
model imports nothing outside the standard library.

```
                    ┌──────────────────────────────────────┐
   cmd/pdfspec ───► │  usecase: convert · sectionize       │
                    └──────────────┬───────────────────────┘
                                   │ depends on interfaces only
      ┌────────────────┬───────────┼────────────┬──────────────────┐
      ▼                ▼           ▼            ▼                  ▼
  objects.Store   render.Raster  ocr.Engine  sink.Writer      doc (domain)
      │                │           │            │             zero deps
  ┌───┴────┐      ┌────┴────┐  ┌───┴────┐  ┌────┴─────┐
  │ pdfcpu │      │ pdfium  │  │llamacpp│  │ markdown │
  │        │      │ (wasm)  │  │        │  │   okf    │
  └────────┘      └─────────┘  └────────┘  └──────────┘
   borrowed        borrowed     borrowed       native
```

### Package layout

```
github.com/3rg0n/pdf-spec

doc/                  Domain model. Zero dependencies outside stdlib.
                        Document, Page, Block, Span, Style, Rect, Matrix,
                        Section, Role, ReadingOrder
objects/              Interface: Store — resolve indirect refs, walk page tree,
                        read content streams and the structure tree
objects/pdfcpu/         Adapter over pdfcpu's XRefTable (Apache-2.0, pure Go)
content/              Content-stream tokenizer and operator interpreter.
                        Owns the graphics + text state machine
font/                 Font dictionaries, /Widths, embedded programs, glyph advances
font/cmap/              CMap and /ToUnicode parsing — CID → Unicode
font/encoding/          Simple-font encodings, /Differences, Adobe Glyph List
filter/               Stream filter chain: Flate, LZW, ASCIIHex, ASCII85,
                        RunLength, Crypt. Stops at an image codec by design,
                        leaving it for image/
bits/                 MSB/LSB bit reader. Unpacks sub-byte image samples;
                        foundation for CCITT and JBIG2
image/                Image XObjects: codec classification, colour spaces,
                        soft masks, sample unpacking. Original codec preserved
                        where it has a container of its own
geom/                 Matrix math, CTM composition, one tolerance policy
tag/                  Structure tree: StructElem, role map, standard-role
                        normalization, logical-order traversal
extract/              Text extraction use case. Consumes objects + content +
                        font + geom. Produces doc.Page
layout/               Geometry-based fallback: column detection, line grouping,
                        heading inference from font-size clusters
sectionize/           Section reconstruction by heading sequence: each heading
                        runs to the next of equal-or-higher level. Levels come
                        from tag/ when tagged, layout/ when not. Emits
                        doc.Section
render/               Interface: Rasterizer — page → image.Image at DPI
render/pdfium/          Adapter: go-pdfium on wazero WASM. No CGO
ocr/                  Interface: Engine — image → markdown or DocTags
ocr/llamacpp/           Adapter: llama.cpp multimodal, model download + cache
ocr/doctags/            DocTags → doc.Section parser (granite-docling output)
sink/markdown/        doc.Document → one .md or per-page .md
sink/okf/             doc.Section tree → OKF bundle
cmd/pdfspec/          CLI
```

Each package is one sub-problem from the specification, which is why the list is long.
The alternative — a `pdf` package that does everything — is how the existing libraries
got to the state described in §1. The split is not speculative abstraction; every
directory above corresponds to a distinct chapter of ISO 32000-2 that independently
breaks text quality when handled badly.

Two boundaries deserve emphasis:

- **`font/cmap` is separate from `font`, and both are separate from `extract`.** The
  0.01%-spaces and 4,069-character-word failures are CMap and glyph-advance bugs
  surfacing as extraction bugs. Keeping decode separate from layout is what makes them
  independently testable.
- **`bits` sits below `filter`.** CCITT and JBIG2 are bit-stream codecs, not byte-stream.
  Building the bit reader as a shared primitive first is a precondition, not a
  refactoring opportunity. It turned out to have a nearer consumer than either: image
  samples below 8 bits per component are packed several to a byte with each row
  re-padded to a byte boundary (§8.9.5.1), so `image` needs the same reader, and a
  1-bit image read with byte arithmetic decodes its first row correctly and skews
  every one after it.

### Pipeline

```
                    ┌─ tagged?  ─── yes ──► tag/ walk ──────────┐
PDF ─► objects ─┬──►│                                            ├─► sectionize
                │   └─ no ───────────────► layout/ heuristics ──┘        │
                │                                                        ▼
                └─► content ─► font ─► extract ─► doc.Page ──────►  doc.Section
                                                      │                  │
                          text coverage too low?      │                  ▼
                                    ▼                 │            sink/{markdown,okf}
                          render ─► ocr ──────────────┘
```

The router is per page and its rule is measurable: if a page's extracted text covers less
than a threshold of the page area, or yields no text at all, that page goes to
`render → ocr`. A mixed document — a born-digital spec with three scanned appendix pages
— produces a mixed pipeline in one pass. This is why `ocr` returns `doc.Section` and not
a string: OCR output rejoins the same domain model and the same sinks.

### Solving the spaces problem properly

The 19.15%-vs-0.01% divergence has one root cause. PDF content streams frequently emit no
space glyph at all; inter-word space is implied by the displacement between glyph
positions. Correct handling requires, per glyph:

1. The advance width from `/Widths` or the embedded font program.
2. The active `Tc` (char spacing), `Tw` (word spacing), `Tz` (horizontal scale), and
   `TL`/`Td`/`TD`/`T*` displacements.
3. The composed text rendering matrix (`Tm` × CTM × font size).

A space is inferred when actual displacement exceeds expected advance by more than a
fraction of the font's space width. That threshold is **one policy in `geom`**, not
fifteen inline epsilons — because it is the single knob that trades "no spaces" against
"too many spaces", and it must be tunable and benchmarked, not scattered.

Line breaks follow the same logic on the vertical axis. Together these are the entire
difference between a 4,069-character word and readable Markdown.

---

## 5. Borrow now, replace later

Native implementation is a **goal of the repo, not a precondition for shipping**. Every
borrowed dependency sits behind an interface this repo owns, so replacement is a new
adapter and a one-line wiring change — never a rewrite of callers. Nothing borrowed is
copyleft; nothing borrowed is EULA-encumbered.

| Sub-problem | Start with | License | Replacement outlook |
|---|---|---|---|
| Object graph, xref, object streams | `pdfcpu/pkg/pdfcpu` | Apache-2.0 | Low priority — it is already good and pure Go |
| Flate, LZW | Go stdlib | BSD-3 | Never; PDF's LZW `EarlyChange` variant is a thin wrapper |
| DCT (JPEG) | `image/jpeg` | BSD-3 | Medium — stdlib rejects some CMYK/Adobe-transform and progressive streams found in the wild |
| CCITT G3/G4 | `golang.org/x/image/ccitt` | BSD-3 | Low — decode and encode both present, `Group3`/`Group4`, MSB/LSB, byte-align option |
| Rasterization | `klippa-app/go-pdfium` on wazero WASM | MIT over Apache-2.0/BSD-3 | **High — the flagship native target.** No CGO, cross-compiles, so it is a clean starting point |
| Glyph rasterization | via the raster adapter | — | Follows the rasterizer |
| JBIG2 decode | none initially | — | **High.** Port from Apache PDFBox's decoder (Apache-2.0, ITU T.88 / ISO 14492) |
| JPX (JPEG 2000) | none initially | — | Lowest priority, highest cost. Rare in practice |
| VLM inference | `llama.cpp` multimodal | MIT | Never native |
| Structure tree, extraction, sectionize, sinks | **native from day one** | MIT | n/a — this is the repo's own contribution |

The last row is the point. The borrowed layers are the commodity ones. The layers that
actually determine output quality — structure-tree semantics, glyph advance and space
inference, section reconstruction, OKF emission — are native from the first commit,
because that is where every existing library falls down.

### Prior art worth studying

- **pdf.js** (Apache-2.0) — the reference implementation for text extraction and
  bidirectional text. Readable, and hardened against a decade of real-world web PDFs.
  UniDoc credits it for exactly this.
- **Apache PDFBox JBIG2** (Apache-2.0) — the canonical JBIG2 decoder to port.
- **Adobe Glyph List** (BSD-3) and **core-14 AFM metrics** — required data, not code.
- **UniPDF** — read for *package decomposition only*. It is EULA-licensed and ships
  bundled single-file releases, so its implementation is neither readable nor safe to
  study. Its value was confirming that a pure-Go rasterizer is achievable and that the
  sub-problem boundaries above match what a mature implementation converges on.

---

## 6. CLI

```
pdfspec md <file.pdf> [-o out]        one .md (default)
pdfspec md --split <file.pdf> -o dir/  one .md per page
pdfspec md --frontmatter …             emit YAML frontmatter
pdfspec okf <file.pdf> -o bundle/      section-aware OKF bundle
pdfspec probe <file.pdf>               tagged? encrypted? filters? fonts? per-page text coverage
pdfspec images <file.pdf> -o dir/      extract embedded images, original codec preserved
pdfspec images --list <file.pdf>       report codecs, sizes, and masks; write nothing
pdfspec render <file.pdf> -o dir/      pages → PNG at --dpi
pdfspec ocr <file.pdf>                 force the VLM path
```

Flags that matter: `--dpi` (default 200), `--ocr auto|never|always` (default `auto` — the
per-page router), `--model` (granite-docling | lightonocr), `--jobs` (page-level
parallelism).

`probe` exists because the first question about any PDF is "which path will this take, and
why", and the answer must be inspectable without reading source. It is also the harness
for the golden corpus in §9.

---

## 7. OKF output

Per the OKF v0.2 specification: markdown files with YAML frontmatter, in a directory
tree. Only `type` is required. `index.md` and `log.md` are reserved filenames; every other
`.md` is a concept document. Consumers must not reject a bundle for missing optional
fields, so emit conservatively and add fields as they become trustworthy.

One clause of the spec becomes one concept document. Mapping:

| OKF field | Source |
|---|---|
| `type` | `PDF Spec Clause` (required) |
| `title` | Clause heading text from the structure tree |
| `description` | First sentence of the clause body |
| `resource` | Stable clause URI, e.g. `iso32000-2:2020#7.5.8` |
| `tags` | Clause ancestry |
| `sources[]` | `{resource: <source path>, title: <document title>, last_modified: <spec date>}` |
| `generated` | `{by: pdfspec/<version>, at: <run time>}` |
| `status` | `draft` until a verification pass runs |

Two fields deviate from an earlier draft of this table, both because of OKF §7's actor
convention — `<producer>/<version>` for agents, `human:<id>` for people, `process:<id>` for
processes, which is what lets a consumer classify trust by detecting the `human:` prefix:

- `generated.by` is `pdfspec/<version>`, not `pdfspec vX.Y.Z`. A space-and-`v` form does
  not parse as an actor and so is not classifiable.
- `sources[].author` is an actor field, and the author of ISO 32000-2 is an organization
  with no actor form — a bare `ISO` is neither `human:` nor `<producer>/<version>`. It is
  omitted rather than filled with something a consumer would misclassify. The document's
  own title carries the same information in a field that admits prose.

Cross-references between clauses become ordinary markdown links, which is how OKF
represents graph structure rather than a pure tree. Directory hierarchy follows clause
numbering.

The links are currently resolved **textually** — a cue word (`clause`, `annex`, `see`, `§ `)
followed by a dotted clause number the document actually contains. Resolving them from the
PDF's `/Annots` and `/Dests` instead is strictly better and remains the target: it would
catch references the prose does not cue and would never produce a wrong edge. It is not
available yet because `doc.Outline` carries text and structure, and annotations are neither;
it lands when a field for them is threaded through `extract` and `sectionize`. Measured on
ISO 32000-2 the textual pass resolves 1,328 references. On WTPDF it resolves none, which is
correct: that file's reading order draws the clause number after a closing parenthesis
(`see ).8.2.6`), so there is no number adjacent to the cue to match.

---

## 8. Roadmap

Each phase is independently useful. Nothing later is required for anything earlier.

**Phase 1 — deterministic Markdown.** `doc`, `objects` + pdfcpu adapter, `filter` (Flate,
LZW, ASCIIHex, ASCII85, RunLength), `content`, `font` + `cmap` + `encoding`, `geom`,
`extract`, `sink/markdown`, `cmd/pdfspec` with `md`, `probe`. Success: beats every column
of the §1 table on the same arXiv paper.

**Phase 2 — structure and sections.** `tag` (done), `sectionize` tagged path, `sink/okf`.
Success: ISO 32000-2's 1,023 pages emit a clause-per-file OKF bundle with correct hierarchy
and resolved cross-references. The measurement above sets the acceptance bar: a
heading-driven implementation should yield on the order of 981 sections, so any run
producing single digits has reverted to container-driven segmentation.

**Phase 3 — images.** `bits`, `image`, DCT and CCITT wiring, `images` verb. Embedded
images extracted with their original codec preserved where possible. Later: native
JBIG2.

Scoping this phase against the corpus moved two of its premises. **There is no CCITT,
no JBIG2, and no JPX anywhere in the 12 files** — 185 of 245 images are Flate, 56 are
DCT, and 4 carry no filter at all — so the CCITT half of this phase cannot be validated
against a real file here. It is wired anyway, with fixtures derived by hand from the ITU
T.4/T.6 code tables, and the test that covers it says in its own comment that this is a
weaker guarantee than the rest of the package has. JBIG2 and JPX are recognized and
named so a report can state what a file holds, and refused on write, because handing
back a `.jbig2` this build cannot produce would be worse than declining.

The premise that moved the design, though, is **`/SMask`: 143 of 245 images carry one,
and 136 of those carry `/Matte [0 0 0]`.** Soft masks are the common case, not an edge
case, and `/Matte` means the base image's samples are premultiplied against black —
they are not the colours they appear to be. An extractor cannot resolve that: the honest
output is both layers plus the statement that one is premultiplied, which is what
`Image.Premultiplied` reports and what the verb writes as a separate `-mask` file.
Compositing stays in Phase 4, where a rasterizer owns it.

Two smaller measurements shaped the code: 7 images sit inside a Form XObject, so the
form recursion is load-bearing rather than defensive — the same defect cost the font
subsystem 21 of its 247 fonts — and deduplication by indirect reference is what makes
the count 245 instead of many thousands, because ISO 32000-2 draws shared images across
1,023 pages.

**Phase 4 — raster and OCR.** `render` + pdfium WASM adapter, `ocr` + llama.cpp adapter,
`ocr/doctags`, the per-page router, `render`/`ocr` verbs. Model handling: download to a
cache directory, verify checksum, load via llama.cpp. Both candidate models are
Apache-2.0 — granite-docling-258M (Idefics3; siglip2-base-patch16-512 + Granite 165M;
emits DocTags; official GGUF published by ibm-granite, plus a ggml-org build) and
LightOnOCR-2-1B (1B, emits markdown directly, wants 200 DPI at 1540 px longest edge,
community GGUFs). Ship granite-docling first: an eighth the size, official GGUF, and
DocTags is structured output rather than prose that must be re-parsed.

**Phase 5 — untagged layout.** `layout` heuristics so untagged born-digital PDFs get
sections without paying for inference.

**Phase 6 — native replacement.** Rasterizer first, then JBIG2. Driven by the interfaces
already in place.

**Phase 7 — Rust.** Same architecture, same CLI surface, shared golden corpus. Deferred
until the Go boundaries have been proven by use, so the Rust port inherits a validated
design instead of re-litigating one.

Generation (creating PDFs) and filters come after extraction is solid, informed by the
OKF-ified spec.

---

## 9. Quality gates

- **Golden corpus with committed expectations.** The `docs/` PDFs plus the arXiv paper.
  Every extraction change reports the §1 metrics — char count, space ratio, words over 25
  characters, longest word, wall time, binary delta. A regression in any column fails.
- **Metrics are the test, not a benchmark afterthought.** "Extraction improved" is not a
  claim without those numbers, because every library in §1 would have claimed it.
- **Fuzz the parsers.** `objects`, `content`, and `filter` take hostile input by
  definition. Malformed PDFs are the norm, not the exception — 30 years of bad producer
  engines is the actual problem this repo exists to absorb.
- **No panics on malformed input, ever.** A PDF tool that crashes on a broken file is
  useless for the corpora that need it most.
- **Pure Go, no CGO, single binary, cross-compiles.** The WASM rasterizer is chosen
  specifically to preserve this.
- Full lint, test, and security scan across the codebase before any commit.

---

## 10. Open questions

- **Table extraction.** Tagged PDFs declare `Table`/`TR`/`TD`, so the tagged path can emit
  real Markdown tables. Untagged table detection is a research problem and is explicitly
  out of scope for Phase 1–5; the VLM path covers it in the interim.
- **Clause URI scheme.** `iso32000-2:2020#7.5.8` is a placeholder. Worth checking whether
  a registered ISO identifier scheme exists before baking it into `resource` values.
- **Whether the golden corpus should move out of `docs/`.** The spec PDFs sit in `docs/`
  alongside this document and are gitignored (below). Relocating them to `testdata/spec/`
  or `corpus/` would separate input from documentation and make the golden corpus
  explicit. Deferred until Phase 1 needs a fixture path, since moving them is cheap and
  the layout should follow the test harness rather than precede it.

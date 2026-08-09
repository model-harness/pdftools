# pdftools — Design

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

`docs/LightOnOCR-2601.14251v1.pdf` is gitignored too, and for a different reason: it is a
public arXiv preprint, but republishing a third party's paper inside this repository is not
this project's call to make, so it was purged from history before publication. Its untagged
17 pages are still where the §1 extraction figures were measured — every other row in the
table above is tagged — so the tests that use it skip when it is absent and `paperFile`
keeps it distinct from the corpus. `testdata/` now covers the untagged and OCR paths with
committed fixtures, which is what makes that a skip rather than a hole.

Both populations skip rather than fail when absent, which is the invariant that lets a
fresh clone pass the suite with no PDFs at all.

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
github.com/model-harness/pdftools

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
layout/               Geometry-based fallback for documents that declare no roles.
                        Heading inference lands first: the body cluster by
                        character count gates candidates, their own section
                        numbering ranks them (ADR 0008). Column detection and
                        line grouping are still ahead of it
sectionize/           Section reconstruction by heading sequence: each heading
                        runs to the next of equal-or-higher level. Levels come
                        from tag/ when tagged, layout/ when not. Emits
                        doc.Section
render/               Interface: Rasterizer — page → image.Image at DPI
render/pdfium/          Adapter: go-pdfium on wazero WASM. No CGO
ocr/                  Interface: Engine — image → DocTags. Also Route, the
                        text-coverage rule that decides which pages cost a model
ocr/doctags/            DocTags → doc.Page parser (granite-docling output)
ocr/ipc/                The wire, byte-identical to inferd's generation protocol.
                        Client, server, and an in-process bridge, all one Engine
ocr/docd/               Host: llama-server as a subprocess over loopback HTTP.
                        The interim backend until inferd carries docling
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

One arm of that diagram is still half-built. `layout` today writes roles and levels back
onto `doc.Page` — which is enough for `sink/markdown`, since a heading is a block with a
level — but it does not yet hand `sectionize` a sequence to build a tree from, so `okf`
still requires a tagged file. ADR 0008 records why, and ADR 0002 records why the builder
needs nothing more than that sequence when it arrives.

The router is per page and its rule is measurable: if a page's extracted text covers less
than a threshold of the page area — 5% by default — or yields no text at all, that page goes
to `render → ocr`. A mixed document, a born-digital spec with three scanned appendix pages,
produces a mixed pipeline in one pass. This is why the OCR path ends at **`doc.Page` and not
at a string**: recognized pages rejoin the same domain model and the same sinks, so nothing
downstream needs to know which pages a model read. `doc.Page.Rasterized` is the only trace
it leaves, and it is there because a knowledge bundle a model will later treat as fact must
record which of its text was inferred.

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
| VLM inference | `llama.cpp` multimodal, as a **subprocess** | MIT | Never native. Not linked either — see §9: the rule is about linkage, and a model host belongs in its own process |
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

#### On list segmentation: what the field does, and what it declines to do

Surveyed while investigating the block-fusion defect §10 recorded. The finding is
uniform and worth stating, because it is a negative result that saved a rule:

| Library | How lines become blocks | List handling |
|---|---|---|
| **pdfminer.six** | `LAParams.line_margin`, a fraction "relative to the height of a line" — the same shape as our `ParaFrac` | none. No lexical or bullet signal anywhere |
| **pdfplumber** | wraps pdfminer; `x_tolerance` / `y_tolerance` / `use_text_flow` | none, explicitly left to the caller |
| **MuPDF** `fz_stext` | geometric lines and blocks; `paragraph-break` breaks blocks at paragraph boundaries, `segment` attempts page segmentation | no bullet or list option. `structured` collects structure markup — the tagged path, separately |
| **Docling** | ML layout model | `ListItem` stores **`marker`** separately from **`text`**, plus **`enumerated`** for ordered lists, and `GroupLabel.LIST` / `ORDERED_LIST` |
| **oar-ocr** (Apache-2.0, Rust) | ONNX layout models, needs downloaded weights | region-level layout analysis, not marker semantics |

Two conclusions, both acted on:

**No mature extractor uses a bullet glyph to decide a block boundary.** They all
segment on geometry alone and leave markers in the text. So `layout.Lists` reading the
glyph is not a missing feature we lag on — it is a step past what pdfminer, pdfplumber
and MuPDF do, and the reason it is defensible here is that it was scored against the
corpus (1442 promotions, 5 false) rather than assumed.

**Docling's data model is the one to copy, and it is a model rather than a heuristic.**
Keeping the marker as its own field instead of a prefix of the text is what makes
ordered lists representable at all — ADR 0011 records that a numbered item is
indistinguishable from a numbered heading *given only the glyphs*, and a `marker` +
`enumerated` pair is where that stops being true, because a tagged PDF declares both.
That is the shape §10's remaining list work should take.

`oar-ocr` is worth revisiting for the OCR path rather than this one: Apache-2.0 and
native Rust matches §9's linkage rule, but it is ONNX-plus-weights, which puts it in
the same subprocess category as `llama.cpp` and not in the deterministic core. Reading
a marker out of a structure tree we already parse costs nothing and needs no model.

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
pdfspec ocr <file.pdf> [-o out]        one .md, recognizing only the pages that need it
pdfspec ocr -dry-run <file.pdf>        which pages would go to a model; loads nothing
```

Flags that matter: `-dpi` (default 200 for `render`), `-jobs` (page-level parallelism, for
`render` only — see below), and on `ocr`, `-threshold` (the router's coverage rule; 0 is the
off switch, 1 forces every page), `-addr` (talk to a host someone else is running), and
`-model`.

The router is a threshold rather than a mode, which is why there is no `--ocr
auto|never|always`: three names for two numbers is a worse interface than the numbers, and
`-threshold 0` and `-threshold 1` already say "never" and "always" without a second flag that
can disagree with the first. `ocr` has no `-jobs` at all — one model, one slot, one in-flight
request per connection, so page-level fan-out would add queueing without throughput.

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
no JBIG2, and no JPX anywhere in the 11 files** — 184 of 239 images are Flate, 51 are
DCT, and 4 carry no filter at all — so the CCITT half of this phase cannot be validated
against a real file here. It is wired anyway, with fixtures derived by hand from the ITU
T.4/T.6 code tables, and the test that covers it says in its own comment that this is a
weaker guarantee than the rest of the package has. JBIG2 and JPX are recognized and
named so a report can state what a file holds, and refused on write, because handing
back a `.jbig2` this build cannot produce would be worse than declining.

The premise that moved the design, though, is **`/SMask`: 142 of 239 images carry one,
and 136 of those carry `/Matte [0 0 0]`.** Soft masks are the common case, not an edge
case, and `/Matte` means the base image's samples are premultiplied against black —
they are not the colours they appear to be. The verb writes both layers, the mask as its
own `-mask` file, because compositing needs a backdrop and belongs in Phase 4's
rasterizer. **Un-premultiplying, though, turned out to belong here** and was deferred a
phase too long: §11.6.5.3 requires the inversion to precede colour conversion, so it can
only run on pre-conversion samples inside the decoder, and until it did, 131 pre-blended
images were being written into a PNG format that declares its samples *not* premultiplied.
`Image.Recoverable` names the 5 where it cannot be done — all with a DCT base, which is
never decoded. See ADR 0007.

Two smaller measurements shaped the code: 7 images sit inside a Form XObject, so the
form recursion is load-bearing rather than defensive — the same defect cost the font
subsystem 21 of its 247 fonts — and deduplication by indirect reference is what makes
the count 239 instead of many thousands, because ISO 32000-2 draws shared images across
1,023 pages.

The image figures above are the eleven-file corpus. ADR 0004's table was measured when
`docs/` also held a non-spec paper that `corpusFiles` swept up with the rest, so its
counts run six images high — 245/143 there against 239/142 here, and its "largest
6049×4090 DCT" was that paper's, not the corpus's (the real maximum is 1169×1394, raw
DeviceRGB). The ADR stands as written because an accepted ADR is a record of what was
decided and on what evidence; the `/Matte` figures it rests on are unaffected.

**Phase 4 — raster and OCR.** Done, in two halves. `render` + pdfium WASM adapter, `ocr` +
`ocr/doctags` + `ocr/ipc` + `ocr/docd`, the per-page router, and the `render` and `ocr` verbs.
Both candidate models were Apache-2.0 — granite-docling-258M (Idefics3;
siglip2-base-patch16-512 + Granite 165M; emits DocTags; official GGUF published by
ibm-granite) and LightOnOCR-2-1B (1B, emits Markdown directly, wants 200 DPI at 1540 px
longest edge, community GGUFs). granite-docling shipped: an eighth the size, official GGUF,
and DocTags is structured output rather than prose that must be re-parsed. Apache-2.0 was a
gate rather than a preference — an MIT repo cannot make a copyleft model its default.

Model handling is **not** what this section originally planned. Weights go through llama.cpp's
own `-hf` cache with its own integrity checks rather than through a downloader here, because a
second downloader means a second cache, a second checksum policy, and two places for a
partially written GGUF to hide. The *executable* is a different question and gets the opposite
answer: `ocr/docd` locates `llama-server` on `PATH` and prints the official install command
when it is absent, never fetching it, because fetching and running a binary on a user's behalf
is a supply-chain step of a different kind than fetching data.

The raster half is done: `render` declares `Rasterizer`, `render/pdfium` supplies it on
wazero, and the `render` verb writes PNG or JPEG. ADR 0005 records what the borrow costs,
all of it measured — **+10.0 MB of binary** (10,416,128 → 20,890,624 bytes), ~1.4 s of
one-time module compile, 3–8 ms per page at 200 DPI, and **a second parse of the file**,
because pdfium brings its own parser and cannot be handed an `objects.Store`. That last one
is why `Rasterizer` declares its own `PageCount`. Two findings from the spike changed the
code rather than merely describing it: the WASM adapter's image `Pix` is a *view into linear
memory* that `Cleanup` frees, so every bitmap must be copied out or one page's pixels appear
in another's; and page dimensions are asked for in pixels rather than DPI, because the
DPI API takes an `int` and the pixel cap produces a fractional one. Parallelism is several
single-threaded Rasterizers over the same file — measured 7.6 ms/page at 1 worker, 3.4 at 4,
and no better at 8, so `-jobs` defaults to `min(4, NumCPU)`.

The OCR half is a **three-part decoupled chain** — parser ↔ host ↔ model — and the middle link
is the one with a contract. `ocr.Engine` is three methods this repo declares; `ocr/ipc`
implements a wire **byte-identical to inferd's generation protocol v2**, so inferd becomes a
drop-in replacement once it carries docling and `ocr/docd` is the lightweight interim host.
Byte compatibility rather than a matching JSON idiom is what makes that substitution real. Two
paths sit behind the one interface and a test asserts they are equivalent: in-process for the
common case of one CLI invocation over one document, and over a socket for a warm host or a
machine with the GPU. Neither side can tell which it is.

The router is the reason this is cheap. `ocr.Route` sends a page to a model only when its text
coverage — the union of block rectangles over the crop box — falls below 5%, so a specification
with scanned annexes pays for the annexes and nothing else, and a born-digital file loads no
model at all. Coverage rather than a character count, because a scanned page carrying a Bates
number has characters and no content. Both paths converge on `doc.Page`, so no sink knows which
pages were recognized; `doc.Page.Rasterized` is the only trace the model leaves. ADR 0006
records the rest, including the two things about DocTags that are not guessable from a model's
output — the 500-unit normalized `<loc_>` grid, and the top-down Y that the parser must flip.

**Phase 5 — untagged layout.** `layout` heuristics so untagged born-digital PDFs get
sections without paying for inference. Heading *rank* has landed (ADR 0008); what remains
before an untagged file yields sections is block segmentation in `extract` — a heading
still fuses into the paragraph below it when the leading is ordinary — and running
sectionize's level stack over `layout`'s levels to get a tree rather than a flat sequence.

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
- **Pure Go, no CGO, single binary, cross-compiles — for the library and the CLI.** This is a
  strong preference, not a prohibition, and the distinction is where it applies. Every
  package a caller imports is pure Go, so `go build` works for any target with no toolchain
  on the machine and the binary is one file; the WASM rasterizer is chosen specifically to
  preserve that. What the binary may *talk to* is a separate question, and Rust or C++ on the
  far side of a process boundary is acceptable where it is the right tool — `ocr/docd` runs
  llama.cpp as a subprocess for exactly that reason, and the OCR path degrades to "no model
  available" rather than failing to compile. The line is linkage, not language: a CGO
  dependency changes what `go build` requires of every user, and a subprocess does not.
- Full lint, test, and security scan across the codebase before any commit.

---

## 10. Open questions

- **~~Table extraction~~ — the tagged half is closed, and re-measuring the debt is what
  moved it.** This bullet used to say the tagged path "can emit real Markdown tables",
  which was a statement about the format rather than about this code: it was emitting
  **none**. A census over the whole corpus found **788 tagged tables, 4650 `TR`, 11626 `TD`
  and 5856 `TH`** — 745 of the tables in ISO 32000-2 alone — every one of them flattened
  into scattered paragraphs. Untagged tables exist in exactly five files. So the work
  described here as blocked on a research problem was 788 real tables blocked on nothing,
  and the fixture that named the blocker is the only thing that needed it.

  **The defect was the `LI → LBody → P` case one element name over.** Of 17482 `TD`/`TH`
  elements on disk, **0 hold marked content of their own**: all 17370 non-empty ones wrap
  their text in a `P`, and `gather` detached that `P` exactly as it once detached a list
  item's body. The cell emitted no spans, `IsEmpty` dropped it, and the text reappeared as a
  free paragraph. Fixed by generalizing the transparency already there — `wrapsText` covers
  `RoleListItem` and `RoleTableCell` — which also joins the 752 multi-`P` cells into one
  cell each. A cell holding a list (42) or a nested table (13) still detaches, because those
  kids have block roles of their own.

  **Position is a field on the cell block, not a nested table block**, and that is the
  load-bearing decision. A `doc.Page` is a flat list in reading order and every stage after
  extraction walks it — the space accounting, the two character-conservation tests, the OKF
  sink, the unplaced report — so nesting would make a block's text reachable by two paths
  and break the invariant those tests rest on. `doc.Cell` carries `Table`, `Row`, `Col`,
  `Header`; the sink regroups. Grouping is keyed by table *number* rather than adjacency,
  which is what makes the 13 nested tables work: their cells arrive inside the container's
  run, so consecutive-cell grouping would cut the outer table in two, and the inner table
  instead follows the outer one — the only order GFM can express.

  **The corpus is unusually GFM-friendly and one shape still is not expressible.**
  Rectangular in 742/788, all-`TH` first row in 773/788, no table without `TR`, column
  counts clustering at 3 (628). But **598 tables carry a `TH` below row 0** — these
  producers mark a whole first *column* as `TH` so each row has a row-header, a real
  distinction with no Markdown syntax, emitted as ordinary cells. Spans are the other:
  69 `ColSpan` and 43 `RowSpan` over 280 cells with an `/A`, about 1%, concentrated in
  ISO/TS 32005; `tag.Elem` does not read `/A` at all and GFM has no merged-cell syntax, so
  a ragged row is padded. The 11 tables whose first row declares no header get an *empty*
  header row rather than a promoted data row, because promoting one relabels data as a
  column name — GFM requires something in that position and inventing a claim is worse than
  a blank cell.

  Cost, fully accounted: ISO 32000-2's block count falls 29218 → 27517, which is the 1721
  extra `P`s folded into their cells (the 20-block difference is extra paragraphs that were
  empty and dropped by `IsEmpty` either way). Both conservation tests hold across the
  change, which is what proves it is a merge and not a deletion — a block count cannot tell
  those apart.

  `reference/tagged-table.pdf` is the yardstick, and it exists because the corpus cannot
  be one: 788 tables are asserted by a section total and a block floor, and a count cannot
  tell a correct grid from a transposed one nor a declared header from a promoted data row.
  It matches its gold file byte-for-byte and is enforced. Its gold file is byte-identical
  to `table.gold.md` on purpose — the same table, one declared and one drawn — so the day
  stroke-path extraction lands, the untagged path has an exact target already known to be
  reachable. Building it needed a third requirement beyond the two `clauses.tex` records:
  a plain `tabular` under `tagging=on` declares *every* cell a `TD`, so the first build was
  the headerless shape (11 of 788) rather than the one being pinned (773 of 788), and
  `tagging-setup={table/header-rows={1}}` with `latex-lab-testphase-table` is what makes
  row 1 `TH`.

  **Untagged table detection remains a research problem** and is still out of scope for
  Phase 1–5; the VLM path covers it in the interim. Two shapes are on disk for whenever it
  is taken up: LaTeX draws each rule as a translated stroke (`q / cm / w / m 0 0 /
  l 159.789 0 / S / Q`) and pymupdf draws `re` rectangles with `B`.
- **The untagged layout path's one remaining measured gap.** `TestReferenceExactMatch` in
  `cmd/pdfspec` reports these against the fixtures in `testdata/reference/`, so they are a
  measured worklist rather than a remembered one. Every item in this sub-list is the layout
  path lacking a role the structure tree would otherwise declare. `clauses` matches exactly
  and is enforced, but that is not the same as the tagged path being gapless — it has no
  list fixture, and the item after this one is a tagged-path defect that measurement found:
  - *Heading rank* — **closed.** `layout.Headings` levels an untagged file's headings from its
    own section numbering, gated on typographic distinction from the body cluster; `headings`
    matches exactly and is enforced. ADR 0008 records why numbering rather than size-ladder
    position decides the level, and the two limits that remain: an *unnumbered* heading stays a
    paragraph, because nothing separates "Preface" from a body-size bold table row, and an OKF
    bundle still needs a tree rather than a levelled sequence.
  - *Paragraph breaks* — **closed**, in two halves, and the second one corrected the first's
    account of it. The heading half is ADR 0009: `extract.continues` breaks a block where two
    consecutive lines' dominant type sizes differ by more than `Tolerance.SizeFrac`, which is
    what a heading set at ordinary leading needs, and `autotagPDFInput.pdf` went from 0 to 12
    heading candidates and `v110-changes.pdf` from 0 to 6. Neither promotes anything yet,
    because both set their headings *unnumbered* — ADR 0008's recorded limit, reached now
    rather than hidden behind a segmentation defect.

    The same-size half is ADR 0010, and getting there meant discarding what ADR 0009 said
    about it. That the "only remaining evidence is the leading itself" is false: measured
    over the purpose-built `reference/paragraphs.pdf`, a same-size paragraph boundary steps
    down 1.200 line heights and so does an ordinary wrap, so no `ParaFrac` separates them at
    any value. `text-styles` is also not the fixture for the case — its paragraphs are one
    line each, so every pair in it is a boundary and a rule that split unconditionally would
    score perfectly. The evidence is horizontal: `extract` starts a block where a line
    repeats the indent its block's own first line was set with, three space widths in that
    fixture. Requiring the match against the block's *own* first line rather than any indent
    is what makes it safe — 441 naive firings over the corpus down to 11 — and a spread guard
    declining blocks whose continuations disagree on a left edge takes it to 3, after centred
    table headers in `pymupdf/dotted-gridlines.pdf` were split mid-phrase. Splitting on style
    was measured and rejected earlier for the same class of reason: `text-styles`' paragraphs
    differ only in which word each emphasizes, so a weight test breaks blocks at whichever
    word happened to be bold.

    What is still not inferable: a document setting *neither* extra space *nor* a first-line
    indent leaves no geometric evidence of a boundary, and the rule declines rather than
    guessing. **`text-styles` is that document, and measuring the one remaining candidate
    closed it as unreachable rather than pending.** Its four paragraphs are one line each set
    at the same x and one line height apart, so `\parindent` cancels — every line *is* a first
    line — and the only signal left is that three of the four end short of the measure, by
    69.6, 28.6 and 33.6 against a 343.1 measure. Scored over all 37 committed testdata PDFs
    plus the 11 corpus documents, "the line before ends short" fires on **16072 of 28231 line
    pairs (57%)** at a tenth of the measure, and 0.02 through 0.30 differ by only six points;
    even at four fifths — a line filling barely a fifth of the column — it still fires 4735
    times. No threshold sits in an empty band because there is no band: **nothing on disk is
    justified.** Counting lines that end at the page's widest extent, the best real document
    is `mupdf_explored` at 44.1% and the specifications are 5.4% (ISO 32000-2), 7.3% (WTPDF)
    and 13.1% (PDF-Declarations), where justified type would sit near 90%. In ragged-right
    setting every line ends short, so a short line carries no information about what follows
    it. `text-styles` stays logged, and it stays logged for a reason that no threshold, guard
    or fixture can change — closing it needs evidence the file does not contain.
  - *List role* — **closed.** `layout.Lists` promotes a block whose text opens with a marker
    glyph followed by whitespace, removes the marker, and takes the depth from the left edge;
    `lists` matches exactly and is enforced. ADR 0011 records the measurements, of which two
    matter here. The marker-plus-separator gate is what makes an allowlist of glyphs safe —
    "opens with punctuation" is hopeless, since 20125 untagged paragraph blocks open with 190
    distinct non-alphanumeric runes and the common ones are `/`, `(` and a quote — and the
    1442 promotions it produces across the corpus contain 5 non-items, all of them rows of
    Annex A and D's glyph tables where a dash *is* the row's subject. Both guards that looked
    obvious were measured and rejected: requiring a run of two items drops 136 genuine
    promotions, and rejecting a block whose marker recurs inside it costs 33 to catch 3.

    Two limits remain. An *ordered* list is not recognized — a numbered item is a paragraph
    opening with a number, which is also what a numbered heading and a table row are, and no
    fixture separates them. And nesting rests on a single case: `lists.pdf`'s 2.403 type-size
    indent is the only genuine left-edge gap inside a marker run anywhere on disk, the other
    seven being 0.011 float noise and one 0.241 glyph-width difference, so `ListStep: 1.0`
    sits in the middle of an empty band rather than at a fitted point.

    **The "extract fused several items into one block" defect this recorded does not reach
    the output, and chasing it would have been wasted work.** Measured at the line level
    inside `extract`, 98 line pairs across 6 files join a bullet-opening line onto the line
    before it. None of them survives to the emitted Markdown: every affected file is
    *tagged*, and `sectionize` splits those items from the structure tree before any sink
    sees them — `Well-Tagged-PDF-WTPDF-1.0.pdf` emits 92 separate list items, not one fused
    block, and `PDF20_AN003` emits 8. Only one fused line survives anywhere on disk, in
    `LightOnOCR-2601.14251v1.pdf`.

    Two candidate fixes were scored and both are dead, which is why the run-minimum guard
    ADR 0011 rejected must stay rejected rather than being revisited alongside a fix:
    - *Geometry cannot see it.* The step before a bullet line spans 1.220–1.486 line
      heights; an ordinary wrap spans 1.100–1.500 across 41849 pairs. Complete overlap, so
      no `ParaFrac` separates them at any value. Nor does the left edge: at the 25th
      through 90th percentile the bullet line's outdent from its block's margin is exactly
      **0.000** space widths, because these producers set the marker flush with the
      continuation text rather than hanging it.
    - *"Different marked-content element breaks the block" costs 6911 splits to buy 8.* Of
      the 41947 currently-joined line pairs, 6911 join lines carrying different MCIDs, and
      only 8 of those are the bullet case — the other 77 bullet joins have one line spanning
      several MCIDs, so an equality test does not even classify them.
  - *Table grid.* `table` emits nine cells as one run of words, and it is now the only place
    that does: the tagged path emits real grids for all 788 tagged tables on disk. Row and
    column membership on *this* file is the untagged-table research problem named above —
    the fixture draws no `Table` element, so there is nothing to read and the strokes that
    would say where the cells are go unconsumed. `reference/tagged-table.pdf` is the same
    table declared and matches exactly, so the target is known reachable and the gap belongs
    to this path alone; the fixture exists so the day it is solved is measurable.
- **~~The tagged path emits its list markers literally~~ — fixed, and the fix found a second
  defect.** *Closed. Kept because the two figures either side of it are the measurement, and
  because the second defect is the argument for the fixture that now guards both.*

  The original: 1363 list items across 6 files rendered as `- ■ text` — the sink's own `- `
  followed by the marker glyph still sitting in the item's text, 1242 of them in ISO 32000-2
  alone. It was a gap in `sectionize`, not in `layout`. PDF declares the marker as its own
  element (`LI → {Lbl, LBody}`, ISO 32000-2 §14.8.4.5.3), `blockRole` mapped neither, so both
  fell into `gather`'s transparent default and the label's spans were appended to the item
  indistinguishably from its content. The tagged path was described here as having no gaps;
  `clauses` matching exactly did not catch it because that fixture has no lists.

  **The declaration is read where there is one, the glyph where there is not.** Of 1407
  declared list items whose text opens with a marker glyph, 121 also declare a `Lbl` — and
  the label's first rune is that glyph in **121 of 121, with 0 disagreeing**, which is the
  strongest available check on the `layout` allowlist. 1286 declare no label at all, so the
  glyph rule has to run on this path too. That is not `inferRoles` over a declaration: the
  block is *already* declared `RoleListItem` and the only question is which of its runes is
  the label it was declared to have. What the declaration adds that no glyph could is 13
  ordered labels — `a.`, `b.`, `[1]`–`[7]` — exactly the case ADR 0011 records as unreachable
  from glyphs alone. It also reaches 11 items in PDF-Declarations whose bold Wingdings square
  is *glued* to its text with no separator, which `ListMarker` rejects outright.

  Docling's model, followed: `Marker` as a field beside the text, `Enumerated()` derived from
  it rather than stored, so the two cannot disagree. `doc/marker.go` rather than `layout`,
  because both producers need the vocabulary and neither may depend on the other.

  Result: **doubled markers 1363 → 0** with prose intact — alphanumeric word counts and line
  counts identical on every corpus file, and all 1391 changed lines classified (1386 markers
  removed, 5 ordered labels losing a doubled space, 0 unexplained).

  **The second defect, which only the new fixture could find.** `testdata/reference/tagged-lists.pdf`
  is tagged by LaTeX, which writes `LI → LBody → Part → P` — legal under Table 364, and a
  shape no corpus document uses. `gather` detached that `P`, so the item had no spans, was
  dropped by `IsEmpty`, and its body was emitted as a bare paragraph: six list items became
  six paragraphs and the marker each had just been given went with the discarded block. Worse
  than the doubled glyph beside it — a doubled glyph is ugly, a lost role is a list that is no
  longer a list. Fixed by making a paragraph transparent *inside a list item only*; a `Figure`
  in an `LBody` (1 on disk) still detaches, and a nested list still detaches because its `LI`
  has a block role of its own. Zero change across all 19 corpus files, as the shape census
  predicted.

  **Moving the marker out of the text moved it out of the accounting too**, which the two
  character-conservation tests caught on exactly the three tagged list-bearing corpus files.
  The deficit was the markers to the character — 124 on WTPDF, 3 on ISO/TS 32001, 1242 on ISO
  32000-2, every lost rune a `•` or `■`, each total equal to the recorded markers — so nothing
  was lost and the tests were reading one of the two places a character can now live. Both now
  count `Marker` alongside `Text()`, which makes them stricter: `TestOutlineConservesCharacters`
  is an exact sum, so an item whose glyph stayed in its text *and* was recorded as its marker
  comes out over the document's own total. Verified both ways by mutation — recorded-and-left
  gives +92/+1239, stripped-but-unrecorded gives −92/−1239 — so the invariant that started this
  section is now enforced by the accounting rather than only by a fixture.

  **The ordered-list syntax this left open is closed, and measuring it is what decided how
  far.** `sink/markdown` emits `1. text` for a label Markdown can express, keeping the
  document's own number — a list starting at 3 is continuing one something interrupted, and
  CommonMark reads only the first item's number anyway, so preserving each item's costs
  nothing. `tagged-lists` now matches its gold file byte-for-byte and is enforced rather than
  logged, leaving `table` and `text-styles` as the only two fixtures still logged — and both
  now for named reasons rather than as unexamined debt. `table` waits on stroke-path
  extraction that does not exist: `content/lexer.go` tokenizes `m`, `l` and `re` and nothing
  consumes them, so there is no grid to emit. That is a statement about *this fixture*, which
  is untagged — the tagged-table bullet above records the 788 tables that needed no strokes
  and were being flattened while this note stood. `text-styles` is not a styling gap at all —
  every emphasis marker in it is already byte-correct, and the whole difference is its four
  one-line paragraphs arriving as one block, which the paragraph-break bullet above records
  as measured and unreachable.

  What the measurement changed is the scope: **none of the corpus's own ordered labels can
  use it.** All 13 are `[1]`–`[7]` and `a.`/`b.`, and Markdown's ordered marker is digits then
  `.` or `)` — so an alphabetic or bracketed label written as one would be renumbered to 1 by
  any parser and lose what the page says. Those keep the bullet and are written into the line
  as text, which is what this repo did for every label before. The fixture holds the only
  arabic markers on disk, so without it neither branch would be pinned by anything: the
  untagged path contributes none, since ADR 0011 records an ordered item as unrecognizable
  from glyphs alone.

  The delimiter is normalized to `.` where the number is preserved, and the asymmetry is the
  point — `1)` and `1.` are the same marker to a parser, so the delimiter is syntax here and
  carries nothing a reader can act on, where the number is the only part of the label that
  carries information. A bullet list followed by an ordered one now gets the blank line
  between them that says what CommonMark already does: a change of marker type ends a list,
  so writing them adjacent only hid the boundary from a reader of the Markdown.

  **The blank line is emitted between top-level items only, and the nesting indent became a
  running stack, both because the review of the change found the ordered marker had made a
  latent bug reachable.** Indenting a nested item two spaces per level is right under `- ` and
  short under `1. `, which is three columns wide: an item indented two there lands inside its
  parent's marker rather than its content, and CommonMark parses it as a *sibling*, so the
  document's nesting is flattened with nothing reporting it. Each level now records the width
  of the marker actually written at it, so a child of `10. ` indents four. The blank line is
  held to the top level for the symmetric reason — between two items inside an enclosing one
  it makes that enclosing list loose, and CommonMark then wraps every one of its items in a
  paragraph, which is a visible change to the whole list in exchange for stating a boundary
  the marker change already establishes. A paragraph clears the stack, since it ends every
  open list and the recorded columns then name parents that no longer exist.

  All four mutations of the marker rule are caught, and so are three of the nesting rule.
  The one that needed a case no document supplies is the delimiter set: widening it to `]`
  converts `1]` and silently drops a bracket the page drew, which nothing on disk is shaped
  to catch, so `TestArabicMarkerRecognition` carries it as a stated boundary rather than a
  measured one. The nesting rules are the same kind of debt made explicit: no corpus label is
  Markdown-expressible, so every parent marker on disk is `- ` and all 98 nested items indent
  two whichever rule runs. What pins the wider marker is
  `TestNestedItemIndentsToParentContent`, from a shape no fixture has. The Markdown is
  byte-identical across every document in `docs/` before and after, which is that reasoning
  confirmed rather than assumed.

  The figures above are a direct measurement over the corpus rather than a carried-forward
  note: 2022 list items, 13 enumerated across 9 distinct labels (`[1]`–`[7]` once each, `a.`
  and `b.` three times each), 0 of them expressible, 623 items whose marker is neither
  declared nor drawn, and 98 nested. Worth recording because the rendered output *looks* like
  it contradicts them — 186 lines open with an `a)`-shaped token after the bullet — and those
  are item text rather than markers, which is what the 623 accounts for. A label the producer
  never declared is not a label this sink can move.
- **A block boundary inside one marked-content element still loses the space that joined
  its two lines.** The wrap space is inferred only *within* a block, so a boundary there
  writes no space at all, and `sectionize.title` then rejoins spans sharing a `(page, MCID)`
  across it with no separator. ADR 0009's marked-content guard closes the route the size
  test would have opened; the vertical-step route remains and has always been there. It
  shows on ISO 32000-2 as `𝐷min2𝑛` where the page sets a fraction.

  Moving the wrap space to the previous span's *trailing* end was the fix tried for this
  and it is not one — with no space written, there is nothing to place. It landed anyway
  because the measurement found a different, larger defect: span *regrouping*. `sectionize`
  joins spans in the order a structure element lists its content rather than in page order,
  so a space on a span's leading edge travels away from the neighbour it was inferred for.
  Over the 11 tagged documents that emitted "revision" as "re" + "-" + " vision" and 15
  more the same way, and ran clause numbers into the sentence before them; trailing
  placement fixed all 29 and broke none. What is left for this item is the block-boundary
  case, which needs a space *inferred* at the boundary rather than moved — and inferring one
  wherever two blocks share an MCID is wrong. Enumerated over the corpus, 52 of those 56
  adjacencies are mathematics: sub- and superscripts, summation limits, and displayed
  equations, where a space would be a new defect. Only 4 are genuinely a lost space. The
  signal has to be the page geometry, not the shared identifier.
- **Clause URI scheme.** `iso32000-2:2020#7.5.8` is a placeholder. Worth checking whether
  a registered ISO identifier scheme exists before baking it into `resource` values.
- **Whether the golden corpus should move out of `docs/`.** The spec PDFs sit in `docs/`
  alongside this document and are gitignored (below). Relocating them to `testdata/spec/`
  or `corpus/` would separate input from documentation and make the golden corpus
  explicit. Deferred until Phase 1 needs a fixture path, since moving them is cheap and
  the layout should follow the test harness rather than precede it.

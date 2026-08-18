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

One node of that tree is ours rather than the file's. `/StructTreeRoot` has `/K` but no
`/S`, so it is not a structure element and has no role; `tag.Read` synthesizes a root to
have something for the walk to start from. It carried `RoleDocument`, which is also what a
tagged document's own top element almost always is, so **every count of that role was one
too high** — 17 of the 18 tagged files on disk reported two `Document` elements where the
file has one, and the figure is not internal, since `probe` publishes `Stats.Roles` as
`tags.top_roles`. `docs/test.docs.md` carried the wrong number for `sampleInvoice.pdf` on
the strength of it while the changelog entry that measured the objects directly had it
right. The root now uses `RoleStructTreeRoot`, a name outside §14.8.4. That makes the
collision rare rather than impossible — nothing rejects an `/S` or `/RoleMap` target naming
it, and a file that did would put the count back — but 0 elements across those 18 trees
carry the name against a `Document` in almost every one, so it trades a collision that
happens for one that has never been observed. Behaviour is otherwise unchanged: `Depth`
excluded the root by name before, and now `isGrouping` excludes it one layer earlier, while
the name check that keeps a real `Document` from counting as a heading level stays.

"Reading order is declared" is a claim about `/K`, and it is only true of a reader that keeps
`/K`'s order. `tag.Elem` splits the array into `Content` (marked-content references) and `Kids`
(child elements) and recorded nothing about how the two interleaved, so the walk read all of one
and then all of the other. Correct for the **89813 elements on disk that hold only one** of them,
wrong for the **767 that hold both**, which moved every rune a child drew to the end of its
parent's own text: **32022 runes across 13 documents, and the whole test suite passing with all
of it displaced.**

The visible damage scales *inversely* with the child's size, which is why it survived so long. A
child holding a paragraph relocates a paragraph, and a reader notices. A `Span` holding one soft
hyphen puts that hyphen at the end of the enclosing paragraph, and what reaches the output is
`constituent elements.--` — ISO/TS 32005's Table 1, with the hyphens of `exposi-tion` and
`constitu-ent` trailing the sentence they were drawn inside. That is unattributable to its cause
by inspection; it was found by tracing one soft hyphen through the pipeline while looking for
something else.

The fix is in the model rather than the walker: `tag.MCRef.Order` and `tag.Elem.KidAt` record
each item's index in the same `/K` array, and `sectionize.inOrder` merges the two slices on those
indices. Two slices and one index rather than one ordered slice of a sum type, because every
consumer but one wants exactly `Content` or exactly `Kids` and a sum type would make all of them
switch. `readKids` assigns `Kids` and `KidAt` itself, since they are one sequence indexed twice
and a caller holding only one cannot reconstruct what the other encodes.

**Four output defects were this one defect.** Glued words (`ISO/TS32005`, `First
edition2023-07`) where the gap between a parent's text and a child's was never measured; a
spurious space inside `http:// creativecommons.org`; a TOC entry emphasized twice
(`**Preface ...**  **2**`); and a link swallowing the punctuation that followed it
(`).www.iso.org/directives` rather than `www.iso.org/directives).`, the very shape this document
described as correct behaviour under §7's link resolution). Two of them had been logged as
separate items. None needed a rule of its own.

Two facts about `/K` that the reader now depends on, both measured rather than assumed over
90721 elements: **no kid and no reference share a position** (each `/K` item takes exactly one
branch, and each branch grows exactly one of the two slices), and **no positions are recorded out
of ascending order**. The first makes the merge's tie-break unobservable — `<` versus `<=` in
`kidBefore` is an equivalent mutation no fixture can kill, so it is recorded as one instead of
being tested for. What *is* observable is what counts as a run: everything up to the next kid,
not one call per position. Per-position grouping makes a transparent element emit a paragraph per
reference — **286 extra paragraphs across 205 elements in 9 documents**, 118 in
`Well-Tagged-PDF-WTPDF-1.0.pdf` alone.

Reconciled in both directions on ISO/TS 32005 rather than spot-checked, since a reordering fix
cannot be verified by diffing sequences: 4472 spaces appear where gluing had hidden a gap, and
**not one non-space rune is lost**. The only other change is two backslashes, and they were the
gluing's own artifact — a glued `[a][a][a]` looks like a link reference and is escaped, and
`[a] [a] [a]` does not.

The merge shipped a panic that review caught, and the shape of the mistake is worth keeping: the
loop bounded its kid branch by `len(Kids)` while `kidBefore` bounded the *same decision* by
`len(KidAt)`. Two guards for one question, disagreeing — a `KidAt` longer than `Kids` names a kid
that does not exist, `kidBefore` said it came first, and the loop indexed past the end. `tag.Read`
cannot produce it, which is exactly why no corpus run and no existing test could: `tag.Elem` is
exported with both fields settable, so the state is reachable only from outside the reader. The
guard now asks both bounds, and the invariant is documented as a fact about `tag.Read` rather
than something enforced, because a library reading untrusted files should not panic over two
comparisons. `TestInOrderSurvivesEveryKidAtSkew` covers all ten skews and asserts termination
plus exactly-once delivery, not just absence of a panic.

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
indistinguishable from a numbered heading *given only one block's glyphs*, and a `marker` +
`enumerated` pair is where that stops being true, because a tagged PDF declares both and an
untagged one betrays a run of them. Both producers fill the same two fields.

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

**The frontmatter is written by hand and validated by a real loader.** Hand-written because a
YAML library reorders keys and a stable order is what makes two runs over the same document
diff cleanly; validated by a loader because that choice puts the file's correctness in its own
string literals, and every assertion over it used to be `strings.Contains`, which cannot tell
a nested mapping from a flat one. `TestOKFFrontmatterLoads` loads all 1409 frontmatter blocks
and 19781 scalars across the corpus and asserts the nesting OKF §5.1 and §7 define, plus that
every scalar loads back as a string. It kills four mutations the previous suite missed — a
two-space `indented()`, a `sources` entry without its `- `, an unindented `generated.by`
(which parses as `generated: null` beside a top-level `by`, moving provenance out of its
field), and removing `yamlReserved` (which coerces 2195 scalars, including 29 booleans and two
clause titles that are bare numbers).

`plainYAML`'s leading/trailing-space rejection was recorded as a survivor of that walk, and
following it up found the record was the wrong shape twice over. The rule is not uncovered —
`TestYAMLQuoting`'s `" a"` case kills its outright removal, and the corpus walk cannot see it
because **0 of 125 non-empty metadata values across all 50 PDFs on disk carry edge
whitespace**, so it was never a corpus survivor to begin with. What *was* uncovered is
narrower and was invisible from a single point: a rule checking leading space alone, or
trailing alone, survives every test in the repository, because one example of a four-position
property pins one position. Both are now in the table, along with the tabs.

The tab cases belong to a different rule — a tab is 0x09, so the control-byte rejection two
rules later catches it — and following *that* out is what found a defect in shipped code.
The record claimed narrowing `TrimSpace` to `Trim(s, " ")` was an equivalent mutant, on the
reasoning that everything the narrowed rule stops rejecting is rejected later anyway. True
for a tab, false in general: `TrimSpace` trims every rune `unicode.IsSpace` accepts, and the
ones above 0x7f are multi-byte, so **no byte-wise rule in `plainYAML` could see them** — the
`c < 0x20` scan reads a lead byte of 0xc2 or 0xe2 and passes it through.

Three of those runes are line breaks. YAML 1.2 §5.4 counts NEL (U+0085), LS (U+2028) and PS
(U+2029) alongside LF and CR, and `gopkg.in/yaml.v2` implements it. `plainYAML` returned true
for a value containing one, so it was written unquoted, and unquoted it **ends the line**: the
loader reads the rest of the value as a new line of the block, fails there, and every key
below it is gone. A `/Title` of `x<LS>---<LS>y` loaded back as `x` with `pages`, `tagged` and
`encrypted` silently absent. Quoting alone was not enough either — a raw NEL inside a quoted
scalar loads back as a plain space — so `yamlString` now emits YAML's own `\N`, `\L` and `\P`,
`\xNN` being defined only for 8-bit values. Both halves are pinned independently: removing
either the plain-scalar rejection or the escape fails `TestYAMLQuoting`,
`TestQuotingRoundTrips` and `TestFrontmatterCannotBeEscaped`.

It is reachable from an untrusted file and it was latent: **0 of 23816 frontmatter lines**
the corpus emits carry one of the three, which is the same shape as the 0-of-125 figure above
and the reason no corpus test could have found it. It is not an escalation — no second key can
be injected, because a colon needs a following space and the document fails to parse before
one is reached — but a consumer reading the frontmatter of an attacker-supplied PDF got a
truncated mapping rather than an error. `TestFrontmatterCannotBeEscaped` had asserted the
right property and measured it with the wrong alphabet: `strings.ContainsAny(s, "\n\r")`
cannot see a line break the loader honours. Its round-trip half is what catches these.

The blast radius is YAML and only YAML, which was worth checking rather than assuming, since
the same strings leave through two other doors. Every value-carrying write in
`sink/okf/frontmatter.go` goes through `markdown.YAMLString`, so that sink is fixed by the
same change. The other door is the Markdown body: `oneLine` passes all three through — its
`isSpaceByte` guard tests `\n\r\t` and space, so it returns early before `strings.Fields`,
which would have split on them — and a raw break therefore reaches a link label and inline
text. Rendered, it is harmless: `pandoc -f gfm` returns one intact `<a>` for each of the
three, the break collapsing to a space. So `oneLine`'s narrow alphabet is a latent
inconsistency rather than a second defect, and it is left alone. Markdown has no construct
these characters terminate, which is exactly what makes YAML the special case.

The generalization is `TestQuotingRoundTrips`, which asserts the property the table's points
are examples of: whatever `yamlString` emits, a loader hands back the string it was given, as a
string. That is the assertion the whitespace class actually needs, because a loader *strips*
edge whitespace rather than rejecting it — `title: a ` loads as `a` with no error — so the
failure is a corrupted value inside a perfectly valid document, which no check on parseability
can see. The same holds for the coercions: `1.7` arriving as a `float64` is valid YAML and is
no longer the version string of the document.

Reading metadata made an untrusted string reach output that never carried one before, so two
properties are now asserted rather than inferred. No emitted scalar can contain a line break —
`YAMLString` writes a newline as the two characters `\n`, so a title of `x\n---\ntype: other`
cannot close the frontmatter fence early, and the three Unicode line breaks above are escaped
for the same reason — and it still loads back byte-identical, because escaping that corrupts a
value is a different defect from escaping that breaks a document. The
title also names the bundle's root directory now, where before it was always the filename;
`kebab`'s `[a-z0-9]`-and-dashes allowlist makes traversal structurally impossible rather than
filtered, so `../../etc/passwd` becomes `etc-passwd`.

Finding those required fixing the test harness first, and the harness was hiding a real
defect. `bundleOf` did not set `Meta.Path`, which every CLI verb sets, so `builder.source()`
returned nothing and the bundle under test had no `sources:` block at all — 0 entries where a
real run has 1398. With it set, `sources` was still only a bare `resource`, because the
`pdfcpu` store reconstructed the trailer without its `Info` entry and every document's
metadata was empty: 0 of 11 corpus documents and 0 of 37 fixtures carried a single `Info`
field. Two layers of test scaffolding were each masking the next, and the thing at the bottom
was a defect in the object layer that no output made visible.

The test that should have caught it is instructive about why a walk is not a count.
`TestMDFrontmatterOffByDefault` asserted that every line the frontmatter emitted was a
well-formed `key: value` pair, and it was — all 6 of them, where 12 belong, because `scalar()`
omits a key whose value is empty and the loop only ever saw what was written. Nothing in the
gold-fixture harness covered `-frontmatter` either, so the byte comparison that catches
everything else in this repo had never looked at this output at all.

That second half is now closed. `testdata/reference/metadata.pdf` is the tenth reference
fixture and the only one that sets `Title`, `Author`, `Subject` or `Keywords` — LaTeX does not
write `\title` into `/Info` without `hyperref`, so the absence was a property of the sources
rather than of the engine. It carries a second gold file holding the whole expected output,
with only the caller's own path substituted; the dates are asserted verbatim, because the PDF
is pinned by SHA-256 and its `/CreationDate` is as fixed as its title.

What that gold file adds is coverage of the *reader*, and the distinction was measured rather
than argued. Mutating the writer — field order, quoting every value, dropping the blank line
after the fence, emitting empty keys — is caught four times out of four by `sink/markdown`'s
own unit tests, so against the writer the fixture asserts nothing new. Mutating
`extract.metadata()` inverts it: reading `Subject` from `/Keywords`, `Creator` from
`/Producer`, or `Modified` from `/CreationDate` each survives every test in the repository and
dies only here. Each emits a complete, well-formed block of non-empty, plausible values, which
is precisely what a presence check, a key count and a loader-validity check all pass. That is
the general shape of what a gold file is for and the three cheaper checks are not: they
establish that a value *is there* and *parses*, and only an independently authored expectation
establishes that it is the *right* value in the *right* field.

The fixture's two dates differ for the same reason. Built with `/ModDate` equal to
`/CreationDate`, the third mutation above emitted a matching value and survived this test too
— the assertion held by coincidence rather than by construction, which is the failure mode
this whole section is about.

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
sections without paying for inference. **Landed.** Heading *rank* came first (ADR 0008);
block segmentation in `extract` followed, so a heading no longer fuses into the paragraph
below it at ordinary leading (ADR 0009); and `sectionize.Untagged` now runs the same level
stack over `layout`'s levelled blocks that the tagged path runs over `H1`..`H6`, so an
untagged file yields a tree and not a flat sequence. `pdfspec okf` accepts untagged input
as a result — 4 of the untagged documents on disk produce a bundle, `mupdf_explored.pdf` at
296 clauses three levels deep. What bounds it now is `layout`'s own limit and not the
absence of a tree: a heading is promoted where the document *numbers* it, so a file whose
headings are all unnumbered still yields no clauses and the verb says to run `md` instead.

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

  **Untagged table detection is closed too, and it was not the research problem this said
  it was.** What made it look like one was measuring the wrong quantity. The question was
  taken to be "how wide must a gap be to be a column boundary", which has no answer: over
  all **117499 inferred spaces on disk** the ratio of gap to nominal space width is
  continuous from the **0.40 the `SpaceFrac` threshold itself imposes out to 1303, with no
  quarter-width band empty below 5** and the largest jump anywhere below 200 — 4529 ratios
  below 0.50, 14530 between 1.75 and 2.00, then a thin tail of 11 past 200 — so no threshold
  separates a cell boundary from wide word spacing at any value. That is the measurement that
  rules out the percentile gap clustering pdfplumber and markitdown use, and it is the reason
  this was deferred rather than attempted with one.

  Three things about that sentence were wrong at one time or another, and all three are the
  ordinary ways a measured figure rots. It said **0.25**, which the threshold makes
  unreachable: `needSpace` requires `gap > SpaceFrac*space`, so nothing below the threshold is
  ever counted and `[0.25,0.50)` and `[0.30,0.50)` held the same 4351 — a bucket label read
  back as a minimum. It said **48757 on disk** while its sub-counts were the twelve
  specification documents alone, which at `SpaceFrac` 0.30 gave **48530**; the 227 went to two
  intervening extraction fixes, chiefly `1d0a536`'s rune word-boundary rule, which stopped
  inferring spaces that the page had already drawn. And every figure in it is denominated in
  the threshold, so raising `SpaceFrac` to 0.40 moved all of them: the 12-document count is
  now 46917 with 2738 below 0.50, and the `[1.75,2.00)` bucket is unmoved at 14337 because the
  change is entirely at the bottom of the distribution. Fewer inferred spaces after a fix that
  removes doubled ones is the right direction, which is the check that distinguishes drift from
  a regression.

  The disk-wide count is the one figure that did not reconcile, and it is recorded rather than
  replaced. Re-measured at 0.30 the twelve documents reproduce **exactly** — 48530, 4351 below
  0.50, 14337 in `[1.75,2.00)` — which is what validates the harness, but the disk-wide total
  comes back **119264** against the 119246 recorded here, and the 18 is unattributed: no PDF
  on disk has been added or changed since the measurement, nothing in the gap path changed
  across the two intervening commits, and counting `gap/space > 0.30` instead of
  `gap > 0.30*space` gives the same 119264, so it is not a rounding artifact either. An
  18-in-119246 discrepancy with a validated harness beside it is worth stating as unexplained;
  quietly overwriting the number would have hidden that the two populations disagree by
  something.

  The answer is that the producer already states it. A stroke drawn between two glyphs is
  the page's own claim that they are in different cells, where a gap is a statistic about
  them, so `extract` collects the page's axis-aligned segments into `doc.Page.Rules` and
  `layout.Tables` reads the grid from those. Both shapes previously named as future work are
  handled by the same collector: LaTeX's translated stroke (`q / cm / w / m 0 0 / l 159.789
  0 / S / Q`) and pymupdf's `re` rectangles with `B`. `re` and the fill operators are not
  optional — `reference/table.pdf`'s sixteen rules are *all* filled rectangles, because a
  hairline is drawn as a thin fill rather than a stroked line to avoid interacting with the
  device resolution, so an `m`/`l`-only reader finds no grid there at all.

  **Splitting happens in `extract`, not in `layout`, and that placement is forced.** A
  block's spans carry one box each, so a row whose cells have already merged into a single
  span cannot be taken apart downstream without re-measuring glyphs. `place` therefore
  records every inferred space as a candidate cut and `splitAtRules` divides the fragment at
  the ones a rule runs through. Every inferred space and not only a wide one, which was
  measured rather than assumed: a `WideSpaceFrac` filter of 2.50 was tried first and
  silently dropped a real boundary, because `reference/table.pdf`'s header row sets wider
  cells than its body — 2.400 space widths against 4.128 — so the filter admitted the body
  rows and discarded the header.

  **A gap is two questions, and conflating them doubled 23598 spaces.** Whether the gap is a
  candidate cut is geometric and every one qualifies, per the paragraph above. Whether a
  *space character* belongs in it is not, because geometry cannot see that the producer
  already drew one there: justified text sets its space glyph and then stretches the word gap
  around it, so the pen ends up more than a nominal space width from the next glyph and the
  gap test fires on a boundary that is already spaced. That is the common case in this corpus,
  not an edge — **25892 of the 48530 inferred spaces follow text already ending in
  whitespace, and 12836 of those also precede a space glyph**, where the inserted character is
  the third. **10922 interior runs of two or more spaces reached the Markdown** because of it,
  9719 of exactly two and 1203 of three or more, in all 12 documents. So `place` now computes
  `writeSpace` separately from `needSpace`: the cut is recorded on every gap and the character
  is written only when neither side already has one.

  Three things about it are worth recording, because each was a wrong answer first:

  - **Dropping the cut with the space undoes the table split.** The cut is a *position* and
    stays one whether or not a character is written there. A header cell whose label ends in a
    space still has to divide from the cell after it, and mutating the cut onto `writeSpace`
    put `reference/table.pdf`'s cells back into one fragment — invisible to any
    character-conservation check, which is why that test asserts on the span list.
  - **The predicate is over the last rune, not the last byte.** `Well-Tagged-PDF-WTPDF-1.0.pdf`
    draws U+2002 EN SPACE as its clause-number separator, and a byte compared against `' '`
    reads that rune's trailing `0x82` as an ordinary character and doubles it — the same defect
    class as the wrap-join rule's, which had already been through exactly this correction.
  - **The inter-fragment write site is a separate branch, and only mutation testing found
    it.** A style change starts a new fragment, and the space between two fragments is carried
    by the one that follows rather than appended to the one before, so the guard has to be
    applied twice. Eight of nine mutations died on the first pass; this one survived with the
    whole suite and 12 corpus documents passing. It is not an obscure path — a normative
    reference sets its title in italic, so `ISO/TC 171, *Document management*` changes font at
    a comma the page has already spaced — and it is **reached 15902 times, with 872 Markdown
    lines across 9 files changing when it is mutated.** A branch the corpus exercises
    constantly can still be one no test can see.

  **Reconciled in both directions.** Against a pre-fix baseline the only runes that changed in
  any of the 12 files are `U+0020` at `-23598` and `U+005C` at `+4`, all 36460 line counts are
  unchanged, and collapsing every whitespace run in both makes all 12 byte-identical — so the
  change shortens whitespace runs and does nothing else. The 4 backslashes are a consequence:
  three table cells held `" -"` and one `" #"`, so the block-start escape did not fire on byte
  0, and with the leading space gone it does. `pandoc 3.9 -f gfm` renders `| \- | x |` as
  `<td>-</td>`, identical to the unescaped cell. Of the 2874 interior runs that remain, every
  one is whitespace the page draws — code-listing alignment inside code spans, `© ISO 2020`,
  `Note 1 to entry:` — so what is left is content rather than inference.

  Both run figures are counted the same way, and the definition is part of the figure: a run is
  interior when a non-whitespace character stands on each side of it. Bounding it with
  `[^ \n]` instead — which admits the runs a tab sits beside — gives 10928 before and 2879
  after, self-consistent and five higher in each direction. Recorded because the two
  definitions differ by a rounding error's worth of runs while looking like the same
  measurement, and a re-measurement that picks the other one reads as drift.

  **The threshold itself was wrong in 12 places, and the fix was two constants — one raised on
  its own measurement, and one that had to be split off from it.** This entry previously
  concluded that no instrument separated the twelve and logged the defect. The instrument was
  the population. Raising `SpaceFrac` then moved a second rule that shared it: see the
  leading-indent item in §11, where 8 spaces beside a bullet glyph crossed from positioning to
  indentation and `geom.IndentAttachFrac` was separated out. A threshold read by two rules is
  a threshold two rules have to agree about, and these two did not.

  The defect: ISO/TS 32004 splits `PdfMacIntegrityInfo` into `Pdf MacIntegrityInfo` twelve
  times against four joined occurrences, at gap ratios of 0.3023 and 0.3105 — just over
  `SpaceFrac`'s 0.30. The reason it looked unfixable was a figure counted on the wrong side of
  the rule. `needSpace` is where the threshold fires; `writeSpace` is where a character is
  actually appended, and it is narrower, because a producer setting justified text draws a
  space glyph and then stretches the gap around it, so the rule fires a second time on a
  boundary that is already spaced and the extra space is suppressed. Over the eleven
  specification documents those are **41164 gaps against 3627 insertions, an eleven-fold
  difference** — and the two populations disagree about this threshold. The "1613 inferred
  spaces that are all correct" in the band was a `needSpace` count. Keyed on `writeSpace`, the
  band from 0.30 to 0.40 holds **243**.

  **241 of the 243 are defects, and every one is nameable.** `STANDAR|D`, `Ver|s`, `ht t|p`,
  `SH A|5`, `Ta b|s`, `(GCM|)`, `(se|e`, `sec ur it y ha|n`, `Ty p|e`, `h t t p s :|/`, and
  `Pdf|M` twelve times. The remaining two are a table of contents' dot leader meeting its page
  number (`"............"|"1"` at 0.3295 and `"............"|"i"` at 0.3615, ISO/TS 32004 p4),
  where a space is defensible either way.

  **The mechanism is in the content stream, and reading it is what ended the guessing.** Two
  aggregate discriminators were measured and killed first — italic overhang finds only 14 of
  the 243 (`below0.40=14 above0.40=19` italic against `229` and `3608` upright), and
  "a drawn space appears elsewhere on the line" splits them `148/95`. Neither separates.
  Dumping the raw ops did, in one step: ISO/TS 32004 emits `["I" -28.4 "S" -25.8 "O" 50.2 …]`
  and draws its word spaces as glyph code `\x00\x03` *inside* the `TJ` strings. So these
  producers letter-space with `TJ` adjustments, which `showArray` deliberately does not add to
  the pen — that is how a wide adjustment reaches the gap test as the space it stands in for —
  and the consequence is that ordinary tracking arrives there as unexplained displacement too.
  At 0.30 of a 0.2200 em Cambria space the threshold is **0.066 em**, below ordinary letter
  tracking; the twelve splits sit at 0.0665 and 0.0683 em. The space advances themselves are
  all sane — Cambria and CambriaMath 0.2200 em, SourceSansPro 0.2000, TimesNewRomanPSMT 0.2500,
  ArialMT 0.2780, Consolas 0.5500 — so the denominator was never the fault; the fraction was
  too small in absolute terms.

  **0.40 rather than 0.35 or 0.45 because the ceiling is where real spaces start.** The first
  insertion in the corpus that can be defended is at **0.479** (the dot leaders at 0.4843, `■`
  bullets at 0.4924, 0.7525, 0.8600), so 0.40 leaves a margin rather than sitting against an
  edge. What the raise costs is **1605 recorded cuts**, which are cell-boundary *candidates*
  rather than characters — `splitAtRules` divides a fragment only at a cut a rule runs through,
  so a cut dropped here is one no rule was crossing.

  **Reconciled on the rendered output.** `Pdf MacIntegrityInfo` 12 → **0**, with
  `PdfMacIntegrityInfo` on 13 lines. Four of the eleven documents are byte-identical; the other
  seven change 130 · 58 · 24 · 22 · 16 · 10 · 4 lines. **No line in any file grew and no file's
  line count moved** — every diff is characters removed, which is the shape a join has and a
  regression does not — and the **2872** interior whitespace runs over these eleven documents are
  unchanged (2000 of exactly two, 872 of three or more; 2874 is the same count with LightOnOCR's
  file, which is why the population goes with the figure), so the raise touched word joins and
  nothing else. Every changed line read in the largest file is a repair:
  `Ta b s`→`Tabs`, `Ver sion`→`Version`, `r e ader s`→`readers`, `STANDAR D`→`STANDARD`,
  `𝑥 ′`→`𝑥′`, `𝑐𝑠 )`→`𝑐𝑠)`, `𝑉𝑖+1,𝑗 ,`→`𝑉𝑖+1,𝑗,`. The separately logged math-layout limit that
  renders 6/29 as `6 29` is correctly untouched.

  **Nothing in `extract` could see the constant's value before this, only its sign.** Mutating
  0.40 to 0.34 or 0.38 left every test in the package passing while changing the output of five
  of eleven corpus documents, because every fixture there sits orders of magnitude from the
  threshold. `TestTheSpaceThresholdIsExactlyFourTenthsOfASpaceAdvance` brackets it two-sidedly
  instead: Helvetica's space is 278/1000 em, so at 12pt the threshold is exactly 1.3344pt, and a
  `TJ` adjustment of -111.2 thousandths must not space while -111.3 must — 0.09% wide, and it
  also pins that the comparison is strict. Seven mutants of the constant, including ±0.01, die
  to that one test with the corpus absent.

  The discriminator that was measured instead of this is kept because the figure is still true
  and still a useful negative: requiring both sides of the gap to read as identifiers finds
  **176 such gaps corpus-wide and all 176 are real spaces** (`LBody TH` ×18, `FENote LBody`
  ×17, `ToUnicode CMap` ×8), and it does not match this case anyway, because `"Pdf"` has no
  digit, hyphen, or internal capital. A discriminator that is perfectly precise on a population
  that excludes the defect is not evidence about the defect.

  **Columns come from cell x-overlap with no tolerance, and the alternative is worth
  recording because it shipped first and was wrong twice.** Keying a row by the positions of
  the rules that split it fails on two files on disk: `autotagPDFInput.pdf` draws its header
  row's column rule at x=158.88 and its body rows' at 158.94, so an exact key read one table
  as two, and `dotted-gridlines.pdf` draws 2048 verticals of a dotted grid where which one
  is found first depends on the width of the text either side. Cell overlap is invariant to
  both, and it has the property `listTiers` has: the numbers compared are glyph extents the
  extractor measured, not quantities `layout` invented. Rows are grouped the same way, by
  vertical box overlap — narrowed to the intersection of a band's members and never widened
  to their union, because a tall span (an inline fraction, a large initial, a superscript)
  would otherwise drag the row below into the same band, which is a *missing row* rather
  than a wrong column count.

  Three gates, each paid for: a rule is required between *cells* and not between spans,
  since a cell holding an italic term is two spans by construction and requiring one per
  adjacent pair would read it as prose and reject the table silently; rows need only *agree*
  on columns rather than match counts, because ISO/TS 32004's key/type/value tables fill two
  of three and `dotted-gridlines.pdf` sets a 9-column total row under 6-column data rows;
  and a run needs **two rows**, which over the 6 untagged documents drops 8 of 21 candidates
  that are a line of prose with a rule through it, at the cost of 4 genuine one-row rating
  tables in `chinese-tables.pdf`. A one-row table is a GFM header and delimiter with no
  body, which is why the trade goes that way.

  **Two further caps, `maxRunRows = 512` and `maxRunCells = 4096`, are cost guards and not
  claims about tables.** `group` re-clusters a candidate run's whole column set for every row
  it adds, because a column merge is retroactive — a later row whose cell straddles two
  established columns collapses them, which can put an *earlier* row's pair of cells in one
  column, and that is what ends a run. So the work is quadratic, and `maxRules` does not bound
  it: one page-tall vertical splits every band on the page, so a stream of many short lines
  yields as many two-cell rows as it has lines. Measured on a synthetic page, 4000 rows take
  0.6s, 8000 take 3.2s and 16000 take 12.3s.

  It is quadratic in the run's *cells* rather than its rows, which is why there are two caps
  and why capping rows alone — which is what shipped first — was measured to be insufficient.
  With the row cap in place and 512 rows held fixed, 100 columns take 1.3s, 200 take 4.8s and
  400 take 14.0s, with `agrees` at 75% of profile samples and `columnOf` alone at 20%. Either
  factor drives the cost on its own: 16384 two-cell rows and 512 four-hundred-cell rows are
  the same hostile page written two ways, which is why both are capped and each has its own
  test. Both are sized against measured ceilings taken over every PDF on disk — the longest
  run of multi-cell bands is **42 rows** (page 888 of ISO 32000-2, next largest 23 and 12) and
  the largest table any document produces is **300 cells**, the widest row 15 — so each cap is
  more than ten times what a real document reaches, and together they take a 204800-span
  hostile page from 14.0s to 0.27s, linear in span count thereafter. Reaching either ends the
  run rather than truncating it, so the rows past the cap start a new table and every span is
  still emitted.

  **One defect in this pass was invisible to every conservation assertion.** `rebuild` flushed
  a block's non-table spans in the order `bandsOf` found them, which is by descending y — this
  pass sorts by y to find bands at all, and reading order is `extract`'s to decide. A block
  whose reading order disagrees with its y order, a footnote emitted after the body but drawn
  above it, therefore had its prose silently permuted while the character count, the span
  count and the table itself all stayed correct. Non-table spans now carry their positions in
  the source block and are sorted before being emitted. Nothing on disk reaches it: over the
  743 pages that draw rules, 0 blocks have their bands out of y order, so the case had to be
  constructed, and the test that does so is the only thing anywhere that can catch it.

  `reference/table.pdf` now matches `table.gold.md` byte-for-byte and is enforced. That file
  is byte-identical to `tagged-table.gold.md`, which is what the pair was built to prove:
  the same table, one declared and one drawn, reaching the same output.
- **The untagged layout path, and the one gap left in it.** `TestReferenceExactMatch` in
  `cmd/pdfspec` reports these against the fixtures in `testdata/reference/`, so they are a
  measured worklist rather than a remembered one. Every item in this sub-list is the layout
  path lacking a role the structure tree would otherwise declare, and every one of them is
  now closed and enforced except `text-styles`, which is recorded below as *unreachable*
  rather than pending: the document contains no geometric evidence of its paragraph
  boundaries, and the only candidate signal fires on 57% of all line pairs on disk. That
  makes it the single fixture `TestReferenceExactMatch` still logs rather than asserts.
  `clauses` matching exactly is not the same as the tagged path being gapless — it has no
  list fixture, and the item after this one is a tagged-path defect that measurement found:
  - *Heading rank* — **closed.** `layout.Headings` levels an untagged file's headings from its
    own section numbering, gated on typographic distinction from the body cluster; `headings`
    matches exactly and is enforced. ADR 0008 records why numbering rather than size-ladder
    position decides the level, and the two limits that remain: an *unnumbered* heading stays a
    paragraph, because nothing separates "Preface" from a body-size bold table row — though a
    *lettered* number turned out not to be one of them. ADR 0013 reads a dotted annex number
    ("A.1", "B.2.3") as a section number whose first component is a letter, on the tagged
    corpus's own declarations: 112 of 112 such blocks are declared headings, the level agrees
    with the declared `H1`..`H6` rank 107 times against 5, and it promotes 0 blocks no producer
    calls a heading — better behaved than the decimal rule already shipping, which scores 931
    against 88 with 10. It recovers 10 appendix headings in 2 documents (`mupdf_explored.pdf`
    296→301, `LightOnOCR-2601.14251v1.pdf` 21→26) and changes nothing else across all 49
    readable PDFs. What stays open is the *bare* letter ("A Vocabulary Pruning"), because "A"
    is also a word and the one bare-letter candidate on disk is a cover line no producer
    declares a heading; ADR 0013 also records that ADR 0008's proposed fix for the unnumbered
    case — a document-level pass over the sequence — is measurably the wrong instrument, since
    rank and repetition are independent and no size ratio has a gap. The second
    limit ADR 0008 recorded — that an OKF bundle needs a tree rather than a levelled sequence —
    is **closed**: `sectionize.Untagged` drives the same `builder.open`/`builder.place` the
    tagged path drives, reading `(level, title, content)` out of `doc.RoleHeading` blocks
    instead of out of `H1`..`H6` elements, so a levelled sequence *is* a tree. `pdfspec okf`
    accepts untagged input; 4 documents on disk yield one (`mupdf_explored.pdf` 296 clauses to
    3 levels, `LightOnOCR-2601.14251v1.pdf` 21, `2201.00069.pdf` 2, `reference/headings.pdf`
    4) and 25 yield none, which the verb reports rather than writing a bundle of one document
    claiming to be a knowledge base. The 18 bundles the tagged path already produced are
    unchanged, and `md` output is byte-identical across all 50 PDFs — the markdown sink already
    rendered a levelled heading, so the debt was OKF's alone.
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

    **Ordered lists are recognized too, and the run is what makes them separable.** ADR 0011
    recorded them as unreachable because a numbered item is a paragraph opening with a number,
    which is also what a numbered heading and a table row are. True of one block — so
    `layout.OrderedLists` never promotes one. It promotes a *consecutive incrementing run at
    one left edge*: `1.` `2.` `3.`, same label form, same margin, which is a claim about a
    sequence that no heading and no table row makes accidentally. The delimiter is required,
    which excludes `7.4 Filters` and `1 Scope`, and a dotted clause number has no single value
    to increment and so cannot form a run at all — ADR 0008's collision closed by construction
    rather than by threshold. Corpus-wide: 70 runs of 260 items, all read; the 4
    false positives are tables of contents and all 4 sit in *tagged* files, where `inferRoles`
    never runs. Live effect on the untagged path: 5 runs of 25 items in `mupdf_explored.pdf`,
    all genuine, with 3 lone numbered paragraphs correctly left as prose. Against producers'
    own `/Lbl` declarations, `doc.OrderedLabel` reads the same form in 16 of 16.

    Note that the run minimum ADR 0011 *rejected* for bullets at 136-to-3 is *required* here,
    and the asymmetry is not a contradiction: for a bullet the run is a guard on top of the
    glyph's own evidence, and for a number it is the only evidence there is.

    One limit remains. Nesting rests on a single case: `lists.pdf`'s 2.403 type-size indent is
    the only genuine left-edge gap inside a marker run anywhere on disk, the other seven being
    0.011 float noise and one 0.241 glyph-width difference, so `ListStep: 1.0` sits in the
    middle of an empty band rather than at a fitted point. Ordered items are all level 1 for
    the same reason inverted — no indented ordered sub-list exists on disk, so there is nothing
    to fit a tier rule to.

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
  - *Table grid* — **closed.** `table` emitted nine cells as one run of words until
    `extract` collected the page's strokes and `layout.Tables` read the grid from them; it
    now matches its gold file byte-for-byte and is enforced. Everything the pipe table says
    about that file was read from sixteen rules — four horizontal, twelve vertical — since
    the fixture declares no `Table` element and no `TH`. The measurements are above, under
    the untagged-table bullet, and the one worth repeating here is why the fixture pair
    exists at all: its gold file is byte-identical to `tagged-table.gold.md`, so the drawn
    path and the declared path are asserted to reach the same output rather than each being
    checked against itself. Nothing else on disk can catch them diverging, because the whole
    11-document corpus is tagged and a corpus count would stay green while this path emitted
    a transposed grid.
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

  **The declaration is read where there is one, the glyph where there is not.** Of 1415
  declared list items whose text opens with a marker glyph, 124 also declare a `Lbl` — and
  the label's first rune is that glyph in **124 of 124, with 0 disagreeing**, which is the
  strongest available check on the `layout` allowlist. 1291 declare no label at all, so the
  glyph rule has to run on this path too. That is not `inferRoles` over a declaration: the
  block is *already* declared `RoleListItem` and the only question is which of its runes is
  the label it was declared to have. What the declaration adds that no glyph could is 16
  ordered labels — `a.`, `b.`, `[1]`–`[7]` and `1.`–`3.` — exactly the case ADR 0011 records
  as unreachable from glyphs alone. It also reaches 11 items in PDF-Declarations whose bold
  Wingdings square is *glued* to its text with no separator, which `ListMarker` rejects
  outright.

  That 124 is a label read that descends into the `Lbl`, which is now the read `label()` does
  too. It was not: a read of the element's own marked content alone sees 24 of the 124, the
  other 100 holding their marker in a `Span` kid. Both numbers came from the same pass, which
  is how the shallow-read defect closed below was measurable from this side — and why the
  agreement figure is the one to re-run, since it is the only place a disagreement between
  what a producer declared and what the glyph rule reads could surface.

  Three of those figures moved after the fact and it is worth saying why, since none of the
  three is a re-measurement of the same population: `testdata/reference/tagged-lists.pdf` was
  committed later and contributes 3 marker-opening items but 6 declared labels — it holds 6
  `LI`, all 6 declaring a label, only 3 of which open with an allowlist glyph — including the 3
  ordered `1.`–`3.`, while admitting `U+F0A7` to the allowlist contributes the other 2 items. The
  two populations therefore take different amounts from one fixture, which is why 1412 moved by
  5 and 153 by 6. Then `declaredMarkers` took it to **1415**, by admitting the hyphen on this
  path alone — 3 items, all in ISO 32000-2, none declaring a `Lbl`, so the agreement half is
  untouched at 124 of 124 rather than merely still passing. A figure
  defined by the allowlist moves when the allowlist does, which is the thing to check when a
  glyph joins it — the agreement count is the one that would expose a bad admission.

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
  untagged path contributed none at the time, since ADR 0011 then recorded an ordered item as
  unrecognizable from glyphs alone. `layout.OrderedLists` has since closed that — it reads a
  run of them and now supplies 25 arabic and lettered labels from `mupdf_explored.pdf` — which
  makes the fixture the pin for the sink's rendering rather than for the labels' existence.

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
- **~~A producer that sets a hyphen as its bullet and declares no `/Lbl`~~ emitted a doubled
  marker: 3 items, all in ISO 32000-2. Closed — fixed by a second vocabulary, not a wider one.**

  The 3 read `- *-  Markup3D (PDF 1.7)* for a 3D comment.` — the sink's own `- ` followed by
  the producer's hyphen still in the text, which is the 1363-item defect above in the one
  shape its fix could not reach. They are declared `RoleListItem`, so `markItem` runs, but they
  declare no label, so it fell to `StripMarker`, and `doc.listMarkers` excludes `-`
  deliberately: on the *inferring* path a leading hyphen is overwhelmingly not a bullet.

  **The exclusion is right for the path it was measured on and wrong for this one, and the
  A/B says so per glyph rather than in aggregate.** Admitting all the excluded glyphs
  changes 2 of 50 documents, which reads like a wash until the glyphs are separated:
  admitting `-` alone changes 1 file and **fixes 3, breaking 0**; admitting `*` alone changes
  1 file and **breaks 3, fixing 0** — three C comment continuation lines in
  `mupdf_explored.pdf` (`* No operations are allowed to change the top level gstate.`)
  becoming list items. Admitting `>`, U+00B7 or U+02D9 changes **0 files either way**, since
  none occurs block-initially on the untagged path at all. So widening the shared vocabulary
  trades 3 for 3, and the trade is unnecessary: the two paths are not asking the same question.
  `markItem`'s own comment already draws the line — *"nothing is being guessed about what the
  block is; the question is only which of its runes is the label it was declared to have"* —
  and a rule that fires on a declared item can afford a vocabulary a rule that fires on
  inferred geometry cannot.

  **The two populations behind these figures are not the same and the earlier phrasing
  conflated them**, which is worth recording because it is the kind of error that survives by
  looking consistent. Block-initial occurrences, separated/glued, over every extracted block
  on disk: `*` 5/8, `-` 1/12, U+00B7 3/0, U+02D9 2/0, `>` 3/6. Over the untagged
  `RoleParagraph` blocks `layout.Lists` actually considers: `*` 3/8, `-` **0/11**, both dots
  0/0, `>` 0/0. ADR 0011's "`-` is glued in 12 of its 13" is the all-blocks figure while its
  surrounding prose is about untagged paragraphs, where the count is 11 and *none* is
  separated. Same for the bullet: 1305 of 1305 over all blocks, 7 over untagged paragraphs.
  The numbers were right and the attribution was not.

  **The dot is two codepoints, and writing it as one glyph hid a gap the mutation pass was
  supposed to close.** `·` U+00B7 MIDDLE DOT and `˙` U+02D9 DOT ABOVE both occur
  block-initially and separated in ISO 32000-2's Annex D, and the D.3 row whose text
  *describes* U+02D9 opens with U+00B7. A test case read off that row therefore pinned U+00B7
  while appearing to cover "the dot", and a mutation admitting U+02D9 survived it. Both are
  now asserted from rows whose leading glyph is their own subject, and each kills its own
  mutation. The general lesson is the one this section already carries about populations: a
  figure or a case named by rendered appearance rather than by codepoint is not checkable, and
  agreement across sites is not evidence when every site inherited the same name.

  **The fix is `doc.declaredMarkers`: `listMarkers` plus the hyphen, reached only through
  `Block.StripDeclaredMarker`, which `markItem` calls.** A superset built from the shared map
  at init rather than a second literal, so a glyph admitted for both paths cannot fall out of
  this one, and a method rather than a parameter on `StripMarker` — the choice of vocabulary
  follows from whether a declaration exists, which only `sectionize` knows, and a boolean
  argument would let `layout` pass the wrong one with the compiler indifferent.

  **The hyphen is the only addition, because it is the only one the declared path can see.**
  Measured over all 1825 declared list items in the corpus, exactly 3 open with a glyph
  `listMarkers` excludes and all 3 are that hyphen: `*`, `>`, U+00B7 and U+02D9 occur **0
  times each** on this path, so admitting them would be speculation with no occurrence to
  check it against. The 3 are separated from their text, `Cambria-Italic` at 10pt, and declare
  no `Lbl` element at all — so none enters the 124-of-124 agreement population, which is
  unmoved rather than merely still passing.

  A hyphen is also the least costly possible mistake in this direction: if one of the 3 is not
  a bullet, what is lost is a single `-` from an item the producer already called an item,
  where the previous behaviour lost nothing and doubled a marker. That asymmetry does not hold
  for `*`, where the same reading over untagged prose broke 3 lines of C.

  **`Enumerated` had to move too, and only the A/B found it.** It asks whether a marker in
  hand is a bullet, and it asked `listMarkers` — so the hyphen `StripDeclaredMarker` had just
  recovered came back as an ordered label and the sink wrote it into the line as
  `- \- Markup3D`: the same doubling, one escape further along. It reads `declaredMarkers`
  now, which has no false positive to weigh, since `layout` cannot set a marker this map
  admits and the shared one does not.

  Result: **3 lines fixed, 0 changed elsewhere** across all 50 documents. Pinned in three
  places, each killing a mutation the others survive — `TestStripDeclaredMarkerReadsTheHyphen`
  and `TestStripMarkerStillDeclinesTheHyphen` for the split, `TestDeclaredMarkersAddOnlyTheHyphen`
  for its size, and `sectionize.TestDeclaredItemsHyphenIsItsMarker` for the wiring. That last
  one is the load-bearing test and was the last written: every test in `sectionize` passed with
  `markItem` still calling `StripMarker`, so the vocabulary could have been written, unit-tested,
  and never reached — which is exactly what the first mutation run showed.

  **What the assertion does buy is `>` and the two dots, and it is the only thing that could.**
  Their A/B is 0 files either way, so no corpus golden and no character-conservation total can
  tell whether they are in the allowlist or out of it — the exclusion was documented, unpinned,
  and unobservable at the same time. That is the shape of debt a golden-file suite is structurally
  unable to carry, which is the general point worth taking from this item.

  **Both halves are fixed and they went to different places, because the two glyphs are not
  alike.** The same census that measured the hyphen found a second glyph the
  declared path could not read: `U+F0A7`, Wingdings' square bullet, 2 items in
  `PDF20_AN001-BPC.pdf` emitting `- ****  How those brand colours…` — the sink's own `- `
  followed by a bold PUA glyph that Markdown renders as empty emphasis. It went into the
  shared allowlist rather than a declared-path set because it has no ambiguity to weigh in
  either direction: every occurrence in the corpus is a bullet (both `Wingdings-Regular`,
  both alone in their span at 12pt, both block-initial and separated, both on a declared
  `RoleListItem`, and there is no third), where a hyphen's whole problem is that it is
  something else 12 times out of 13. A PUA codepoint has no Unicode meaning to appeal to, so
  corpus use is the only evidence there can be — which is thin, and stated as thin in
  `doc/marker.go` rather than dressed up. Its A/B changes 2 lines in 1 document of 50 and
  nothing else.
- **~~`label()` reads only the `Lbl` element's own marked content, so 100 of the 153 declared
  labels on disk read as empty~~. Closed — the read descends, bounded by `blockRole`, the same
  predicate `gather` detaches on.**

  The producer's marker usually sits one level below the `Lbl`, in a `Span` inside it — 92
  cases as a single `Span` and 8 split across two — and `label()` walked `e.Kids` only far
  enough to find the `Lbl`, then read `k.Content` and stopped. So it returned `""` for 100 of
  the 102 empties it produced, and `markItem` fell through to `StripMarker`, which recovered
  the same `■` from the item's text. The output was right; the reason it was right was the glyph
  rule, not the declaration the producer supplied.

  **What makes it worth recording rather than fixing quietly is that the code's own comment
  had the cause wrong**, attributing all 14 empties it then knew about to producers who "drew
  the marker outside the element" — a property of the file. Measured against production
  `label()` by compiling counters into it, the split is 2 that own no marked content at all
  (the producer's version) and 100 that own it one level down (ours). A number that looks
  measured but was never attributed is the same failure this section records for the two dots
  and for the populations above.

  **The safety margin recorded here was the argument for closing it, not against.** It read:
  all 16 ordered labels, the only case with no glyph fallback, are owned by their `Lbl`
  directly, so the 100 are exactly the cases where the fallback works. Both halves are ordinary
  on disk — 100 of 108 `Lbl` kids are a `Span`, and 16 labels are ordered — and only their
  *intersection* is missing. So "no document does both" is a fact about these 50 files rather
  than about producers, and an ordered label in a `Span` kid would emit `- [1] text` with the
  label doubled into the line and nothing in the corpus to say so. That is the case
  `TestOrderedLabelInASpanKidIsRead` builds, and it is the one that fails without the descent.

  The bound is `gather`'s, both halves: descend through a kid with no block role, stop at one
  that has one, and stop at a heading. Reusing `blockRole` is what keeps the label's idea of
  where it ends from drifting from the walk's. A nested list is the boundary the original note
  named — descending through it would put a sub-item's marker into its parent's label — and a
  heading kid is worse, because `gather` hands it to `visit`, which opens a section from it, so
  consuming its spans would leave that section with no title. Neither shape occurs inside a
  `Lbl` on disk: the first text below one is at depth 1 in all 100 cases and `Span` is the only
  kid role that ever appears. `TestLabelStopsAtANestedList` and `TestLabelStopsAtAHeading` hold
  the two stops, and the second was written only because a mutation deleting the `IsHeading`
  term passed everything else, including the nested-list test — a heading has no block role.

  The A/B is byte-identical across all 40 documents that render text, which is the predicted
  result and not a null one: the descent now *reads* the marker the glyph rule was recovering,
  and both routes reach the same output. Measured after the change, the 100 are unmoved and
  `label()`'s empties are 2 — the producer's version, which no descent can fix — where they
  were 102.
- **A block boundary inside one marked-content element loses no space, and this item is
  closed as *unreachable* — the census that was meant to size it found no case to fix and a
  different defect instead.** The premise was sound: the wrap space is inferred only
  *within* a block, so a boundary writes none, and `sectionize` rejoins spans sharing a
  `(page, MCID)` with no separator. In `extract.continues` the vertical-step test runs
  *before* ADR 0009's marked-content guard, so a shared-MCID pair whose step exceeds
  `ParaFrac × h` still splits, and that route is real.

  What the corpus says is that nothing travels it. Enumerated over all 50 PDFs there are
  **398** such boundaries, not the 56 recorded before — 183 already spaced, and of the 215
  not: 116 upward, 19 at the same baseline, 80 downward. The 19 all have `dx ≈ 0` and fall
  mid-token (`[` + `5]`, `r` + `d`), where a space would be a new defect. Of the 80 downward
  candidates **79 are ISO 32000-2 mathematics** — fractions, summation limits, matrix
  brackets, subscripts. `𝐷min2𝑛`, named here as the symptom, is among them: it is `min` + `2`
  at a step of 0.728 × height, a *subscript*, and the space this item wanted to insert would
  have been wrong. Exactly **1** looked like an ordinary wrap, and it was not a lost space
  either — its first span already ended in U+2002 EN SPACE.

  That last case was the actual defect, in the opposite direction. `endsWithSpace` tested
  bytes for ASCII space, `\n`, `\t`, so it could not see the space character
  Well-Tagged-PDF-WTPDF-1.0.pdf uses for *every* inter-word gap, and inferred a second one on
  top of it: 231 doubled spaces in that document — "the understanding  of", "e.g.,  WCAG" —
  plus 5 where the arriving line *began* with U+2002 and the leading `HasPrefix` missed it.
  In Markdown two trailing spaces are a hard line break, so the doubling changed how those
  lines rendered. Both halves of the guard now test the rune with `unicode.IsSpace`; the
  space this code writes stays an ASCII one, since that is its own inference rather than a
  glyph the page drew. 224 characters left that document's Markdown and the other 49 are
  byte-identical.

  Also recorded, because it is the reason the earlier count was wrong: the geometry signal
  this item asked for cannot be built from a corpus of one. Trailing placement — the fix
  tried first — was never a fix for the boundary case, since with no space written there is
  nothing to place; it landed on its own merits, because `sectionize` joins spans in
  structure order rather than page order, so a leading-edge space travels away from the
  neighbour it was inferred for. That emitted "revision" as "re" + "-" + " vision" and 15
  more the same way, and ran clause numbers into the sentence before them. Trailing
  placement fixed all 29 and broke none.
- **Composite-font glyph decoding is covered, and the coverage is a *negative* result:
  0 of 2900493 drawn glyphs decode to nothing.** The gap this closes was not a missing
  branch — `Decode` is at 100% of statements and the `font` package at 87.8% — it was that
  `TestCorpusSimpleFontsDecodeToText` had no composite counterpart, and
  `extract`'s `show` drops an undecodable glyph in silence, advancing the pen without
  placing it. A character can vanish from the output with no error anywhere.

  **The two tests have to be built the opposite way round, and that asymmetry is the whole
  reason one was missing.** A simple font's encoding *names* codes, so the names are an
  enumerable claim and resolving each one is the test. A composite font makes no such
  claim: a CID indexes a glyph set and implies no character, so the only population is
  `/ToUnicode`'s own domain — and asking a map whether it contains its own keys asserts
  nothing. What exists instead is the set of codes the document *draws*, which means the
  test interprets content streams rather than reading dictionaries.
  `TestCorpusDrawnCodesDecodeToText` walks page streams and descends into Form XObjects
  the way `extract.doXObject` does, resolving each string's font from the graphics state.

  Both of those are load-bearing and both were found by getting them wrong first. A probe
  tracking the last `Tf` operand rather than `m.GS.Text.Font` reported **46 undecodable
  glyphs** in `BCDIEE+SymbolMT`; dumping the bytes spelled "Adobe Acrobat Reader", ordinary
  prose that extracts correctly — `q` pushes the whole graphics state including `Text`, so
  a linear scan attributes a string to whichever font was set last anywhere in the stream.
  And a page-only walk misses **1977 composite glyphs drawn inside forms**, which is a clean
  census of an incomplete population.

  **A third mistake was caught by review, and it is the one that changed a published
  figure.** Counting fonts and codes by `/BaseFont` collapses them: a name is not an
  identity, and 11 composite names on disk are drawn by more than one font dictionary —
  `ArialMT` and `SymbolMT` by four each, and a subset prefix does not always separate them,
  since `BCDIEE+SymbolMT` is two distinct objects. The real figures are **96 fonts and 2617
  drawn codes**, not the 79 and 2556 first measured. The key has to include the file too: an
  object number is unique only within its own document, and keying on the reference alone
  under-counts by a further six codes across the eleven.

  Seven mutations were run against the finished test and five are killed, including all
  three of those mistakes. The two survivors are recorded rather than patched, because both
  are fidelity to `extract`'s own walk that this corpus cannot exercise: no form here
  inherits its font from the invoking stream, and no form's content stream decodes to nil
  without reporting an error. The inheritance one is safe because of the assertion on
  unresolvable font names — an inheriting form under the mutation resolves the name `""`,
  which matches nothing in a real `/Font` dictionary, so the glyph is reported rather than
  skipped in silence.

  What the zero rests on is a fact about these producers, not about the format: **all 96
  composite fonts reached carry a `/ToUnicode` stream**, so `compositeText`'s empty return
  is never taken. That is pinned separately, because the day a font without one appears the
  zero stops being a statement about this code and becomes one about the file. Such a font
  is genuinely unrecoverable — a CID carries no character meaning and OCR is the only
  remaining route — which is also why the Symbol and ZapfDingbats built-in glyph sets stay
  recorded debt: the corpus draws no code that needs them.

  **The test proves presence, not correctness, and the boundary is worth stating because it
  is where the next defect would hide.** A code that decodes to the *wrong* character passes
  — a byte-order or offset bug in `cmap` that keeps producing output survives this test
  completely. Correctness is the gold fixtures' claim, and pointing this same walk at
  `testdata/reference` measures how far it reaches: **416 composite glyphs over 5 fonts and
  95 distinct codes**, compared word for word against committed Markdown. The corpus
  contributes the other 2522 codes as presence only. Closing that gap would need a per-code
  expectation no producer publishes, so the split is recorded rather than resolved.
- **The leading space on cells after the first is closed as *unreachable*, and the recorded
  figure was wrong in both directions.** It was logged as "1 row in 50 PDFs, cosmetic". At the
  `doc.Block` level it is **11084 of 16401 cells**, which is the common case and not an edge
  one: `extract` carries an inferred space on the fragment that *follows* it, deliberately, so
  that trimming stays the sink's decision — and `splitAtRules` cuts a ruled row into cells at
  exactly those gaps, so every cell after the first opens with the space that separated it from
  its neighbour. In rendered Markdown it is **0 of 16724**, because `cellText` trims.

  The reason it looked like one row is that `row()` writes its own padding space, so a leak
  reads as `|  Table 29 |` — a second space GFM collapses, visible only in the bytes. What was
  actually missing was a test at the trim: deleting it leaves all of `sink/markdown` green and
  fails only `TestReferenceExactMatch/table` two packages away, as a whole-document byte diff
  naming no cause. `TestCellLeadingSpaceIsTrimmed` now pins all three paths — plain, code span,
  and the substituted path, which returns before the spans are read and therefore trims
  separately — and kills four mutations, including weakening either `TrimSpace` to
  `TrimRight`. That third one is reachable from no document: `sectionize` sets `Alt` on 218
  blocks across the 50 and none is a cell. `Alt` comes from the structure tree rather than the
  page — `extract` sets it on 0 blocks — so the tagged path is the only one that could reach
  that branch at all. (The fixture now sets `Replacement`, since the split below made `Alt`
  alone insufficient to reach a substitution over a cell that draws text.)
- **`/Alt` and `/ActualText` were one field, and merging two opposite spec operations
  deleted page text.** §14.9.3 makes `/Alt` a *description* of content; §14.9.4 makes
  `/ActualText` a *replacement* for it. `doc.Block.Alt` held both, so a sink had to choose one
  behaviour for both and chose substitution — writing a figure's description over three
  captions drawn as real text inside that same figure on `PDF20_AN001-BPC.pdf`, 129
  characters. Fixed in the model: `Replacement` is a field of its own and one `substitute()`
  rule serves both markdown walkers and the OKF sink. The measurement is what settled the
  shape — **217 of the 218 blocks carrying either are `/Alt` with no text**, where
  substitution is correct and is the only text there is, so narrowing `/Alt` to a description
  everywhere would have broken the majority case while fixing the one.
- **The `Replacement` branch is unreachable from any file on disk, and that is a wiring gap
  worth its own line.** Not because `/ActualText` is rare: there are **4803 of them across the
  51 PDFs**, and **all 4803 sit on inline `Span` elements**, which `sectionize` never lifts
  into a block. So the field is a correct model of §14.9.4 that nothing currently fills, and
  its rule is held by unit tests rather than by the corpus — which is not a footnote. Review
  found that reinstating the original defect in `sink/okf` alone passed **every test in the
  repository**, and that swapping `substitute`'s branch order survived all of `sink/markdown`,
  because precedence is observable only when both fields are set *and* the spans are blank.
  Both are pinned now. A rule with a zero corpus population gets no protection from a green
  suite, and the mutation has to be applied to find that out. Reading those spans is separate
  work, and it is not cosmetic: **0 of the 4803 agree with the glyphs beneath them.** The
  three distinct values are a line break (4695×, glyphs read `" "`), `" • "` (92×, glyphs read
  `"■ "`) and a soft hyphen (16×, glyphs read `"- "`). The bullet case is benign today because
  neither glyph survives into output, but the soft hyphen is a visible defect —
  `ISO-TS-32004-2024_sponsored.pdf` emits `id-ct- pdfMacIntegrityInfo` where the page says
  `id-ct-pdfMacIntegrityInfo`. **All three are read now**, by `sectionize.substituted`; see the
  entry below. That example is half fixed and the figure has moved rather than gone: line 320
  of the current output reads `id-ct-pdfMacIntegrityInfo` correctly, and line 330 now reads
  `id-ct-pdf MacIntegrityInfo` — the hyphen is attached and a *different* space, inside one
  extracted span between `pdf` and `Mac`, is what still splits it. That one is the intra-line
  gap rule and not this key: the same file shows `Pdf MacIntegrityInfo` 12 more times where no
  `/ActualText` is involved at all.

  **Chasing one of those 16 is what found the `/K`-order defect** described in §3, and the
  triage above is why: the item was logged as "read `/ActualText` from a `Span`", and measuring
  it showed 4803 values holding 3 strings that want 3 different treatments, only 16 of which are
  a substitution at all. A declared line break is one a Markdown sink reflows away, so ignoring
  4695 of them is correct rather than deferred. The remaining question is narrow — whether to
  emit `U+00AD` for the 16, or the `-` the page draws — and it is now reachable, which it was
  not when this bullet was written: those `Span`s' runes land in their `/K` position instead of
  at the end of the enclosing paragraph. Answered in the entry below: neither. A soft hyphen is
  discretionary, so it is dropped and the word joins.
- **All 4803 `/ActualText` values are read, and the majority case is the one a blind §14.9.4
  rule would have broken.** `sectionize.substituted` applies a declaring element's value to the
  spans it covers, on the inline path — which is the only path that can reach them, since every
  one of the 4803 is on a `Span` and a `Span` is transparent, so none ever becomes a block with
  a `Replacement` field. Wired into both inline walkers, `gather` and `labelText`, because
  **all 92 of the `" • "` declarations are `LI>Lbl>Span`** and reach only the second: a rule in
  one walker would miss the corpus's largest declared shape entirely, which is what
  `TestDeclaredLabelIsSubstituted` exists to pin.
  - **Measuring first is what stopped it being a regression.** The logged item was the 16 soft
    hyphens, and the general rule is right — but substituting all three values verbatim would
    have put a line break into 4695 spans of inline text. It did, in the first draft:
    `**Technical Specification**` came out as two lines and lost its bold, because a CommonMark
    emphasis run cannot span a line break. So `inlineText` adapts the value to what a
    `doc.Span` holds — a break becomes a space, a `U+00AD` is dropped — because a dictionary
    string can say things a run of glyphs cannot and every sink downstream assumes it does not.
  - **One of the two defects my own draft introduced looked like an improvement.** The declared
    break restored the line structure of the ASN.1 listings in ISO/TS 32004 and 32003, which
    currently collapse to one line. It is not an improvement: those are code *spans*, and
    `pandoc -f gfm` renders a blank line inside one as a paragraph break with the backticks
    left literal. Settled with the renderer rather than by reading the output. Line structure
    there is worth having and is separate work — it needs the block *fenced*, which is
    `doc.Block.Role`'s business and not a side effect of one dictionary key. Half-answered
    below, and the half this bullet named turned out to be the wrong half: the tagged path is
    fixed, and 32004 and 32003 declare no `Code` at all.
  - **The net corpus effect is exactly `{U+002D: -16}`, in both directions.** 32004 −3, 32005
    −10, 32002 −2, 32003 −1, and nothing else changed either way across all 51 files. Each
    joined word now matches its own document's majority spelling: `MACLocation` 16 against
    `MAC-Location` 0, `digest` 31, `structure` 64, `algorithm` 21, `revision` 4, and 0 `U+00AD`
    left anywhere in the output. The 92 bullets move nothing, because both the declared `•` and
    the drawn `U+25A0` are list markers and the sink strips whichever it gets — the value is
    honoured rather than the square being read as a label the producer disclaimed.
  - **The conservation invariant was tightened, not relaxed.** A substitution is a deliberate
    loss, so `TestSectionizeLosesNoText` now names the lost multiset per rune and exactly —
    `{U+25A0: 92}` for WTPDF, `{U+002D: 10}` for 32005 — rather than raising a percentage bound
    that 102 characters of slack would fit a real loss inside. Exact in both directions, so a
    substitution *stopping* fails it too. Its sum-based twin,
    `TestOutlineConservesCharacters`, states the expected delta per file for the same reason,
    and its sign carries information the multiset check cannot see: 0 for WTPDF, where one
    bullet replaces one square, −10 for 32005, where the hyphens drop.
  - **A substitution copies its spans.** `index` hands out `*doc.Span` pointers into the
    caller's `doc.Document` and the `Unplaced` recovery pass reads the same ones, so editing in
    place would rewrite the page text of a document the caller also asked to extract.
  - **Three call sites read the raw value, and only one of them has a corpus population.**
    `emitItem` copies `/ActualText` into `doc.Block.Replacement`, which every sink's
    `substitute()` prefers over the block's spans, and `title` reads it for a heading whose
    marked content resolved to no spans — where `clean` is not enough on its own, since it
    folds the declared break, which is whitespace, and leaves `U+00AD`, which is not. Both go
    through `inlineText` now. Review found this, not the corpus diff and not the mutation run:
    all 4803 declarations are on a `Span`, so no file on disk can reach either path, and the
    defect there is the same invisible hyphen one layer further on. Same shape as the
    zero-population `Replacement` branch above — a rule nothing measures needs the mutation
    applied to know it is held.
  - Thirteen mutations applied, thirteen killed — but two of them only after the tests that
    kill them were written in response to review, and the eleventh only after it survived
    everything. Dropping the box union passed the entire suite *and* all 51 files, because
    every fixture in `sectionize_test.go` leaves its spans' boxes zero and `Union` of two zero
    rects is zero either way; `TestSubstitutionKeepsTheWholeRunsBox` calls `substituted`
    directly with real geometry. The `labelText` wiring is the other one a narrower test set
    would have missed.
- **A `Figure` that draws text now drops its `/Alt` entirely, and that is a deliberate
  trade rather than a solved problem.** On `PDF20_AN001-BPC.pdf` the fix recovers 129
  characters of real caption text and loses the 217-rune description that used to stand in
  its place — verified by grepping the output: the paraphrase appears 0 times, the captions
  once. Substituting it back is exactly the defect, and appending it would invent a line the
  page does not have, so neither sink emits it. The right home is an image reference whose
  alt attribute carries it, which is what `/Alt` is for and which waits on figures emitting
  images at all. Recorded here because a description silently going nowhere is worth knowing
  about even when dropping it is the correct call.
- **A hyphen holding a word across a line wrap no longer gets a space after it — 483 breaks
  repaired, and the rule is about the rune *before* the dash.** `marked- content`,
  `cross- reference`, `human- readable`, `ISO 32000- 2:2020`. Measured at the decision point
  in `appendLine`: **489 dash-final wraps across the 17 PDFs on disk, of which 483 are a dash
  attached to a letter or digit, 5 have a space before the dash, and 1 has punctuation before
  it.** The 483 were all wrong and the other 6 were all right, so the discriminator is
  attachment to a letter or digit — not the dash,
  and not what follows the break, since 26 of the 483 continue into a digit and 17 into a
  capital (`41- 44`, `GREATER- THAN`, `UTF- 8`). Removing 193 characters from ISO 32000-2's
  Markdown, 208 breaks in `mupdf_explored.pdf`, 15 in WTPDF, and 12 remain corpus-wide — 17
  when this was written, since three were reached later by the intra-line gap rule and the
  count is downstream of it. All 12 are drawn spaces rather than wrap decisions, and none is a
  defect; see the sub-entry below.
  - **16 of the 483 need a walk back through spans**, because the dash is frequently a span
    of its own — a different style run, or its own MCID — leaving `prev` as a bare `-` with
    the word one span earlier. Those are the `surrounding`, `structure`, `constituent` and
    `algorithm` breaks in the TS documents, and a rule reading only `prev` misses them.
  - **The alphabet is not `U+002D`.** `U+2013` EN DASH holds `a– f` together in a hexadecimal
    range and `U+2011` NON-BREAKING HYPHEN holds `doc‑ bibliography`; the one `U+2014` EM DASH
    at a wrap is detached and keeps its space through the same test. So `isDash` is the Pd
    category plus `U+00AD` and `U+2212`, which are Pd's near misses.
  - **The hyphen itself stays, and the corpus says it must.** Looking each joined word up in
    its own document — against text with the rule *off*, because with it on the fix has already
    made the join being searched for — accounts for all 483: the document spells **218**
    elsewhere *without* a hyphen (`applica- tion` against `application`), **170** *with* one
    (`cross- reference` against `cross-reference`), **14** both ways, and **81** appear nowhere
    else at all. No rule separates them, and an unsplit word with a hyphen in it is a reading a
    consumer can repair while a deleted hyphen is not recoverable from the output.
  - **The whole suite passed with all 483 defects in it**, because no assertion anywhere
    looked at the character before a wrap — and it passes with them fixed, since the corpus
    baselines assert counts rather than text. Ten mutations of the rule were applied and all
    ten killed; the four tests are in `extract/extract_test.go`. The tenth came from review
    after nine of my own were all killed: relaxing the word test to `!unicode.IsSpace(r)`
    survived the entire suite, because no test placed *punctuation* before the dash. The corpus
    has exactly one (`resources/-` wrapping into `Courier'`), which is now a row. Same lesson as the
    `/ActualText` split above, from the other direction: there the rule had no corpus
    population, here it had 483 and still nothing measured it.
  - Distinct from the soft-hyphen `/ActualText` case above, which is 16 structure elements
    rather than a wrap decision, and still open.
  - **~~The dash-space pairs that remain~~ are 12, and 11 of them are what the page draws.**
    Closed as a non-defect after measurement, having been logged as remaining work on the
    assumption that a `<digit>- <letter>` reading like `48- byte` had to be a lost join. It is
    not. Every one of the 12 is a space the producer *drew*, not one this package inferred —
    established by marking `place`'s three insertion sites with distinct sentinel bytes and
    regenerating, which attributed all 12 to the glyph stream and none to `writeSpace` or the
    wrap rule. Six are correct suspended hyphens (`four- or five-element`, `human- or
    machine-readable`, `both forward- and backward-compatible`, `both 2- and 4-byte`, `Mixed
    one- and two-dimensional`, `at both the document- and object-level`), one is `%PDF-`
    naming the header prefix, and three more are the math-layout limit recorded below.
    - **What settles the remaining four — `48- byte`, `32- byte`, `2- byte`, `2- unit` — is
      the pen, not the text.** Each space glyph is drawn with a *negative* gap against the
      dash before it, as little as −1.85pt, so it overlaps the dash and the appearance is
      ambiguous from the coordinates alone. But it carries a full advance (2428/1000 em, the
      font's own `SpaceWidth`), and the glyph after it lands where that advance puts the pen
      rather than back at the dash's end: `12700` against `12476` for `The 48-`, and the same
      224-unit displacement in all four. The gap is on the page. `48- byte` is what ISO
      32000-2 renders, so joining it would be the extractor overruling the document.
    - **The instrument is the transferable part.** A drawn space and an inferred one are
      indistinguishable in the output, and no assertion on text can separate them — sentinels
      at each insertion site can, in one regeneration, and that is what turned a five-item
      backlog entry into a closed question. The count had also moved on its own, from 17 to
      12, because the intra-line gap fix reached three of them; a backlog figure measured
      before an unrelated fix is a figure about the old pipeline.
- **No line ends in whitespace any more, and 12947 characters of it came off.** A span's text
  ends in a space whenever the producer drew one there, and the last span of a line carried
  that space to the line's end, where Markdown gives it a meaning the page never had: **516
  lines across 8 documents ended in two or more**, which CommonMark §6.7 makes a hard line
  break, so the output rendered a `<br>` no document asked for. **11826 more ended in exactly
  one** — renders identically, makes every conversion diff-hostile, and trips
  `MD009 no-trailing-spaces` on any linted consumer. Distinct from the wrap-space rules above:
  those decide whether to *infer* a space at a break, this is a space the file actually
  contains landing where Markdown reads something into it.
  - **The figures logged when this was found — 539 with 2+, 11970 with one — were wrong by the
    time it was fixed**, because the `/K`-order and `/ActualText` fixes both change where a
    line ends. Re-measuring first is what made the reconciliation below exact; taking the
    logged numbers would have left a 605-character discrepancy with nothing to attribute it to.
    They also differ *by layer*: running the sink directly on `extract`'s output reaches 13478
    such lines where the CLI's own path reaches 12342, so the stage a figure was measured at is
    part of the figure.
  - **Held back rather than trimmed at each line's close, because there are eighteen places
    that end a line and one that writes bytes.** `writer.str` buffers trailing space and tab in
    `pend` and flushes it only when a non-whitespace byte follows on the same line; a newline
    discards it. A space between two words survives, a space before a newline is never written,
    and the rule does not care *which* of the eighteen callers closes the line — including the
    four that write their own `"
"` inside a longer string, which a per-close trim would miss
    entirely. `write` is now the sole path to the underlying writer, which is what makes "one
    place" true rather than aspirational.
  - **The alphabet is space and tab**, matching MD009 and CommonMark §2.1. A no-break space is
    not whitespace for this purpose: it is content a producer chose, and 0 lines on disk end in
    one, so keeping it costs nothing and dropping it would delete a character.
  - **Whether any of it was content was measured rather than reasoned about.** Trailing
    whitespace inside a fenced code block is preserved verbatim by a renderer, so trimming it
    changes the block's bytes. Classifying every affected line put 6 inside an unclosed code
    span and 1 after a table pipe against 13471 plain; neither of the two renders the whitespace
    it holds. The claim recorded here originally — 0 fences in the corpus output at all — was
    wrong when written: the corpus already emitted 14 fence markers over 7 lines, from the 7
    `Code` elements that carry their own content. It is now 36 markers over 106 lines, and the
    number that matters is that **0 of those 106 lines end in whitespace**, so the conclusion
    survives on a population that is not zero.
  - **Reconciled in both directions against a pre-fix baseline: `U+0020` is the only rune that
    changed in any of the 12 files, `-12947`**, matching the measured total exactly, with 0
    trailing whitespace remaining and all 36472 line counts unchanged.
  - **Twelve mutations applied, and the first pass killed seven.** Five survivors, all from
    branches the corpus reaches and no test did — the value of applying a mutation rather than
    reading the code and judging it covered.
    - **Four of the five are one path: the interior lines of a chunk that carries its own
      newlines.** `str` trims those where it finds the newline rather than through `pend`, and
      a **code block is the only caller that reaches it** — the fence, body and closing fence
      are separate writes, but the body arrives whole, so its lines 1..n-1 never touch the
      buffered path. The four other embedded newlines in the package are frontmatter's `---`
      rules and `nl()` itself, none of which has content before the newline. Dropping the trim
      there, and either half of its alphabet, survived everything.
      `TestTrailingWhitespaceOnAnInteriorLine` is a three-line body whose two interior lines
      end in a space and a tab respectively, so each alphabet mutation fails on one of them.
    - **One is byte-identical on the whole corpus.** Flushing held-back whitespace from an
      all-whitespace chunk produces the same bytes for all 12 documents, because reaching the
      state needs a whitespace-only write *after* a non-whitespace one on the same line and
      every whitespace-only span on disk arrives first or between two words. A whitespace-only
      `Replacement` reaches it, since `doc.Block.IsEmpty` inspects the spans;
      `TestAnAllWhitespaceWriteDoesNotFlush` is that state, and without it the guard is held
      by nothing.
    - **Review found a thirteenth defect, in a branch this change made worse rather than
      created.** `inline`'s whitespace-only case (`sink/markdown/inline.go:24`) writes a span's
      text without calling `escapeInto`, and `unicode.IsSpace` counts VT and FF where
      `isControl` does not — so a span holding one is whitespace-only there and reached the
      output unescaped, contradicting the invariant `sanitize`'s own comment states. Mid-line
      that was merely wrong; with this rule it also *ends a line*, because `TrimRight(" 	")`
      leaves it: `**word** 
`. **0 of the corpus's 11597 whitespace-only spans hold a
      control byte and 0 spans hold one at all**, so no measurement could have found it and
      nothing but `TestAControlByteCannotEndALine` holds the fix. Reverting the `sanitize` call
      is killed by that test and by nothing else in the repo.
    - **Two of the reviewer's three findings were wrong, and running the code settled both.** A
      whitespace-only `Replacement` was reported as emitting an invalid `-` list marker and an
      invalid `>` quote. Rendered, they are `"-
"` and `">
"`, both of which CommonMark reads
      as an empty list item and an empty block quote; before the change they were `"- "` and
      `"> "`, equally empty. The table case the reviewer did not raise is the one that mattered
      structurally, and it is safe: `| a |  |` keeps its pipes, because a pipe is not
      whitespace, so the column count and the delimiter row still agree.
    - **The twelfth survivor was deleted rather than tested.** `write` had an `if s == ""`
      guard, and `str` calls it with an empty string routinely — but `bufio.Writer.WriteString("")`
      returns `(0, nil)` unless it flushes, which it cannot do with nothing to write. The
      mutation changed no byte and failed no test because the guard is unobservable, which is
      the definition of code that should not be there.
  - **One test's claim was rewritten rather than its bound relaxed.**
    `TestWhitespaceMovesOutsideDelimiters` asserts CommonMark §6.2 — that a caption's trailing
    space moves outside the emphasis delimiter — and both its rows previously ended at that
    space, which the trim now removes. A fixture ending at a line's end can no longer observe
    where the delimiter went, so a plain span follows the emphasized one in every row and the
    §6.2 claim stays visible.
- **Eleven of the corpus's eighteen code listings were dropped whole, and the logged note named
  the wrong files for the wrong reason.** A `Code` element whose listing is one `P` per line
  holds no marked content of its own, so `gather` detached all 99 of those paragraphs as blocks
  in their own right, the `Code` block emitted no spans, and `doc.Block.IsEmpty` discarded it:
  `declared=11 kidP=99 -> codeblocks=0 empty=0 chars=0`. Every listing came out as ordinary
  prose with its `/` and `<<` escaped. Fixed on the tagged path.
  - **The measurement is what found it, because the fence count looked healthy.** 18 `Code`
    elements on disk in 3 files. The 7 in `PDF-Declarations.pdf` and
    `PDF20_AN003-ObjectMetadataLocations.pdf` are `kids=0 content=N` and always fenced
    correctly. All 11 in `Well-Tagged-PDF-WTPDF-1.0.pdf` are `kids=N content=0`. A count of
    fenced blocks is therefore non-zero while two thirds of the corpus's listings are missing —
    the same shape as *presence is not correctness* elsewhere in this document. The note said
    fencing needed `doc.Block.Role` and blamed ISO/TS 32004 and 32003: the role path was already
    complete end to end, and those two files declare no `Code` at all.
  - **`linesText` is a second predicate rather than a wider `wrapsText`, and that is the whole
    design of the fix.** `doc.Block.writeText` joins spans with no separator, so absorbing the
    paragraphs without restoring the breaks yields one block holding every line run together —
    the collapse the fence exists to prevent, produced by the fix for it. The distinction is
    measured: 752 cells hold more than one `P` — over all 18 tagged files, of which the 11
    sponsored ISO PDFs in `docs/` account for 751 — and their text is one run of prose the
    producer broke across lines, where joining with nothing is correct; a listing's `P` *is* a
    line, and the break between two of them is content with no glyph anywhere on the page.
  - **Three conditions on one `append`, each reachable.** The break goes only after a block role
    — 10 of the 109 descendants of the corpus's `Code` elements are `Span`, and breaking on any
    kid splits a styled line in two — only before recursing, so it lands between two lines
    rather than after the last, and only once there is text, so no listing opens with a blank
    line. That last guard fires on a `Code` with no content of its own, which is all 11 of them;
    the shape review proposed instead — a first `P` that resolves to no spans, where the guard
    would have to hold a second time — has **0 of 99** on disk, and it needs no break there
    anyway, since there is no first line to separate from.
  - **The inserted span carries `MCID: -1`, which is what keeps it out of the join.** `newIndex`
    indexes only spans with `MCID >= 0`, so a fabricated one can never be handed to a second
    element or double-claimed; and because it is created after extraction it is not in any
    `doc.Page` for `unplaced` to recover. Its zero `Box` is the same story — nothing unions a
    span's box except `substituted`, which only touches spans it was given.
  - **Both sinks were checked, not just the one the fence lives in.** `sink/okf` routes clause
    bodies through `markdown.WriteBlocks`, so the fence and its breaks arrive there too: 22 fence
    markers across 7 concept files. The two places okf could have leaked a raw newline into a
    YAML scalar both fold it — `oneLine` on titles and `collapse` in `describe` — and 0 of the
    generated files have a malformed frontmatter line.
  - **Reconciled in both directions: not one character of text changed.** One of 12 files moved.
    `U+000A -66`, `U+0060 +66`, `U+005C -103` and nothing else — the newlines and backticks are
    the 11 fences' own three lines each, and the escapes went because fence content is verbatim.
    49 lines lost a backslash and **0** gained one, taking the lines holding one from 76 to 9.
    Lines 1934 → 1868; with all whitespace, backslashes and backticks removed — the three runes
    that moved and nothing else — the two outputs are byte-identical at 104249 characters each.
    The exclusion of backticks matters and is not slack: 66 of them were added, so a
    normalization that kept them would differ by exactly that and prove nothing.
  - **The block count drops 88 and the arithmetic closes.** WTPDF goes 938 → 850, because 99
    paragraphs that each stood beside an empty `Code` block are now the contents of 11 of them.
    `TestSectionizeCorpus`'s floor for that file moved 900 → 840 with the reconciliation written
    beside it, the same treatment the 29218 → 27517 cell merge got: a block count cannot
    distinguish a merge from a deletion, so the floor documents the change while
    `TestSectionizeLosesNoText` and `TestOutlineConservesCharacters` — both unchanged and both
    exact — are what rule the deletion out.
  - **Eight mutations, eight killed — and one was killed only by the corpus test.** Dropping the
    `blockRole` guard failed `TestSectionizeCorpus` alone, which skips when the sponsored PDFs
    are absent, so on a clean clone that guard had no coverage at all.
    `TestCodeSpansDoNotSplitALine` reproduces the `P > Span` shape those 10 descendants are in
    and kills it from the `sectionize` package by itself. Same lesson as the inter-fragment
    write site above: a branch the corpus exercises is not a branch a clone tests.
  - Logged here as "the untagged half of the same defect" — ISO/TS 32004's and 32003's ASN.1
    listings, with `Style.Mono` as the signal and a rule in a different package. **That framing
    was wrong and the entry below is what came of measuring it.** There is no untagged half:
    every mono-bearing file in the corpus is tagged, so the rule would have had no population.
- **A listing's lines can exist only as geometry, and one of the corpus's does.** The entry above
  restored the breaks of listings whose producer declares one `P` per line. `PDF-Declarations.pdf`
  declares the other shape: a single `Code` holding 25 lines as 25 MCIDs under **no `P` at all**.
  `gather` has no paragraph to absorb, so the sink fenced a 25-line XML sample as one
  892-character line, with every character-conservation check passing and all 24 breaks lost.
  Nothing in the text marks them — the page draws its own space at each line end, so `extract`'s
  wrap rule finds a boundary already written and infers nothing. `sectionize.breakAtBaselines` now
  writes them from the one place they survive, the distinct `Box.Y0` values.
  - **The logged note was wrong in all three of its claims, and each was disproved by a specific
    probe rather than by argument.** It said the remaining half was *untagged*, that `Style.Mono`
    was the signal, and that the loss happened in `extract`'s `appendLine`. A tagged/untagged
    census over mono-bearing files: **11 of 12 are tagged, and the twelfth has no mono at all**,
    so `inferRoles` — and with it the whole of `layout` — never executes on this population. A
    prior measurement pass of 360 mono blocks had already been run against a code path that does
    not run. Mono and `Code` are disjoint signals on this corpus: WTPDF's 11 listings are declared
    rather than monospaced, and six other files have no mono whatsoever.
  - **A newline in an inline code span is not invisible, it is destructive — settled with a real
    renderer.** pandoc 3.9 `-f gfm` turns `` `a\nb` `` into `<code>a b</code>`, so an
    `appendLine` break would have been unobservable in 3886 of 4246 mono-mono wraps. In the other
    **360 the next line opens with `>>`**, and there the newline destroys the span: nested
    `<blockquote>` elements and literal backticks in the output. So the break can only be written
    where the block is fenced, which is what moved the rule out of `extract` and into a role the
    producer declared. The originally-planned fix site was the wrong one and this is what showed
    it — the same lesson as *escaping needs a real renderer* elsewhere in this document.
  - **Geometric in a package that is otherwise declaration-driven, which is why it is confined.**
    The gate is `linesText(role)`, so the rule never asks what a block *is* — `RoleCode` is the
    producer's own statement — only where its lines end. A listing is the one role for which that
    answer must survive to the sink at all: every other role either folds a newline to a space or,
    for a table cell, cannot hold one. `TestParagraphLinesDoNotBreakOnBaseline` is what keeps it
    off the corpus's wrapped prose, and mutating the gate to `true` fails it.
  - **Idempotent with `gather`'s rule rather than layered on it.** A break is written only where
    there is not one already. The two rules see the same block for 5 of the corpus's 6 multi-line
    `Code` listings, and this one is strictly wider: it supplies PDF-Declarations' 24 missing
    breaks plus one WTPDF break `gather` cannot write, where `"…report67890"` wraps into
    `"</ pdfd:claimReport>"` with both sides being `Span`s inside a single `P`. Census before and
    after: `code=18 oneBaseline=11 fullyBroken=5 underBroken=2 lostBreaks=25` →
    `fullyBroken=7 underBroken=0 lostBreaks=0`.
  - **A fabricated span's zero box is a trap, and it bit twice.** `gather`'s break spans carry
    `MCID: -1` and no geometry, so a zero `Box.Y0` reads as a several-hundred-point jump from
    every real line. In my own census probe it inflated every already-broken WTPDF block by
    exactly one, giving a wrong `underBroken=7 lostBreaks=31` before the correction to `2` and
    `25`. In the rule itself it is a design constraint: `newLine` must skip `MCID < 0` on **both**
    sides or it writes a blank line between every pair of listing lines, and
    `TestCodeDeclaredAndDrawnLinesBreakOnce` kills all three ways of dropping those guards.
  - **The threshold is the extractor's own line test, not a second opinion about the same
    question.** `LineFrac` of the *larger* of the two type sizes, matching `run.go`'s
    `maxf(sy, prev.height)`. The larger size is also the conservative direction: a superscript
    half its line's height clears half of *its own* size long before it clears half of the line's,
    so measuring against the small span breaks a line at every raised digit. **49 of the 179
    adjacent pairs inside a corpus `Code` block change type size, and 0 of them land in the window
    where the four readings of "the type size" differ** — so the corpus cannot hold this choice
    and a fixture does. The comparison is on the step's magnitude rather than its sign for the
    same kind of reason: PDF-Declarations' listing crosses a page and **rises 681pt**, which a
    signed comparison reads as one long line.
  - **Eighteen mutations, all eighteen killed from `./sectionize/` alone**, with no corpus-only
    kills — the clone-gap trap named in the entry above is avoided by construction here. Four of
    the eighteen required fixtures the corpus cannot supply: the upward page-crossing step,
    and the three wrong readings of the type size, which one subscript fixture settles at once
    because a 5pt run dropped 3pt on a 10pt line breaks in a *different place* under each. Two
    results from the first pass had to be thrown out as **invalid mutants** — dropping `math.Abs`
    or the tolerance lookup leaves an import unused, so the harness was counting a compile error
    as a test failure; both now keep the import alive with a blank reference. Two further patterns
    reported NOT-FOUND because the harness's own escape pass turned the Go literal `\n` into a
    real newline before searching for it.
  - **Two output files change and both diffs are pure newline additions.** PDF-Declarations' one
    892-character line becomes 25; one WTPDF line becomes two. Nothing else moves in any of the 12.
  - **The rule compares two coordinate spaces at a page boundary, and the corpus does not punish
    it.** `emitItem` takes a page *range* precisely because content crosses page breaks, and
    `doc.Span` carries no page number — its own doc comment says the join key is the MCID
    *combined with* the page. So `newLine` compares a `Box.Y0` from page *n* against one from page
    *n+1* as though they were one space, and a listing whose next page resumes at nearly the same
    height as the previous page's last line would be a **missed** break: the exact defect this
    rule exists to fix, wearing the rule's own clothes. Measured: **1 of the 18 `Code` elements
    crosses a page, and its step is 681.22pt against a threshold of 6.00pt** — a 114× margin, so
    there is no instance on disk. Left as it is rather than threading a page through the span,
    because the honest fix is a page number on `doc.Span` and that is a wider change than this
    defect justifies; recorded here so the next listing that crosses a page is diagnosed rather
    than re-derived. **Fixed as its own unit of work below, and two of the three figures above
    were wrong** — the step is 681.22pt against 6.00pt, not 680.64 against 4.56, and 149× was a
    ratio of two different files' numbers; the widest margin on the corpus is ISO/TS 32004's
    155.1×, and the narrowest — which is the one that matters — is 107.7×.
  - ~~Still open: PDF-Declarations encodes its listing indentation as **x-position only**, so the
    restored lines are flush-left. In a fence the leading spaces are content, so this is a wrong
    answer about the listing — but it is a pre-existing one, equally lost when the whole block was
    a single line, so the fix is an improvement with a stated limit rather than a regression.~~
    **Fixed, and the premise was wrong: the indentation is drawn as real space glyphs, not as
    x-position.** They are drawn *outside marked content*, which is why they vanished — `newIndex`
    skips `MCID < 0`, the same mechanism as the dropped leader below, so 23 runs corpus-wide were
    never claimed by any element. Nor did they reach `Unplaced`: a rebuilt block holding only
    whitespace is dropped by the `len(keep.Spans) == 0` guard, so the comment promising the
    recovery pass keeps them was false for exactly this shape. `leadingIndent` adopts a run under
    the key of the span it is attached to. Both halves of this item were one defect — the "one
    WTPDF listing line missing its leading indent" logged separately is the single WTPDF run of the
    23; the other 22 are PDF-Declarations' whole sample, which is why one file lost one line's
    indent and the other lost all of them.
    - **Contiguity is the discriminator, and the band is empty for 6.8×.** The corpus holds 66
      untagged whitespace-only spans: 23 indents and 43 that are not — a dotted leader's trailing
      space, the space beside a bullet glyph, a TOC entry's padding. Three conditions separate them
      with no overlap: whitespace by rune, first on its baseline, and *attached* to the following
      span. Measured in space advances, every indent meets its text within **0.0532** of one and
      every other run with a same-line successor stands at least **0.364** clear, so the threshold
      sits in an empty gap. The band is what the corpus test asserts rather than the threshold.
    - **A band in the wrong unit did not guard the threshold, and that is how the shared constant
      broke.** The band above was first measured in points — 0.243pt to 2.000pt, an
      emptier-looking 8.2× — and attachment was expressed as the negation of `gapSpace`'s own space
      test, sharing `SpaceFrac` so that no gap could be a space and an indent at once. The
      composition was sound and the constant was a coincidence. When `SpaceFrac` was raised from
      0.30 to 0.40 on its own measurement, **8 spaces beside a bullet glyph** in PDF-Declarations —
      6 on p5, 2 on p9, all at 2.184pt against 12pt text, which is 0.364 of a space advance —
      crossed from positioning to indentation, taking the split to 31/35. Points cannot bound a
      threshold that scales with the type size: those runs are set in 12pt and the 23 real indents
      in 9.12pt, so the threshold walked across one population while **both edges of the
      point-denominated band stood still** and the assertion still read (0.243, 2.000). Two figures
      moved and the figure guarding them did not.

      The defect was fully latent — all 11 documents render byte-identically either way, because the
      list writer emits its own `- ` and discards a line's leading whitespace. A misclassification
      masked by a downstream sink is worth fixing anyway: the mask is the sink's current behaviour,
      not a property of the rule. Attachment now has its own constant, `geom.IndentAttachFrac` =
      **0.15**, centred on a log scale in the 6.8× band, and the corpus test asserts the band in
      the fraction the rule compares against — substituting `SpaceFrac` back now collapses it to
      1.1× and fires the guard alongside the counts. 8 mutants die where 3 previously survived:
      the four other `Tolerance` fields, the em for the space advance, the indent's own size for
      the text's, an unsigned comparison, and `>=` for `>`.

      Splitting the constant also removed the reason the two rules could not contradict each other,
      so what actually holds them apart had to be found rather than assumed. It is not the MCID
      guard, which is what `gapSpace`'s own comment credits: the adopted indent is indexed as a
      *copy carrying the following span's MCID*, so it arrives at `gapSpace` non-negative and its
      geometry is read. It is the whitespace-rune test one line further down, which declines because
      `leadingIndent` adopts nothing but whitespace — verified load-bearing by disabling it, which
      fails three fixtures. At today's values the two thresholds leave a band from 0.15 to 0.40
      where a gap is neither attached nor a space, and nothing structural keeps them in that order;
      the whitespace test is what makes any two values safe. Named in both comments, because a
      guard that holds by construction and is credited to the wrong line is a guard the next
      change will move.
    - **The indexed span is a copy carrying the key's MCID, and that is not cosmetic.** `newLine`
      skips `MCID < 0`, so an indent left at −1 answers false on *both* sides of itself: the first
      version of this fix restored every indent and collapsed the 25-line listing back into one
      892-character line — the very defect `breakAtBaselines` exists to undo, reintroduced by its
      own repair. The original is marked consumed where the copy is indexed, or the same spaces
      appear in a section and in `Unplaced` both.
    - **20 of 22 lines land on the right column; 5 runs are drawn with stretched spaces.** The
      producer draws 18 of the 23 runs at a uniform 4.97pt per space and 5 at **10.16 and
      20.51pt** — 4 glyphs across 82pt where the column wants 16. The character count is exact
      wherever the advance is nominal and under-reports by 2.2× and 4.5× where it is not, so those
      5 lines come out short. Left verbatim: correcting them needs a per-font space advance, and
      `doc.Style` carries `Size` and no advance by deliberate design (`block.go:267`) — the same
      trade `spaceAdvance` documents. Reporting what the page drew is the honest answer, and the
      remaining error is 5 lines under-indented against 25 formerly flush-left.
    - **The character-conservation test could not have caught this**, which is the transferable
      part. `TestOutlineConservesCharacters` counts *non-space* characters, so 23 dropped
      whitespace runs pass it by construction. `TestListingIndentsReachTheirListing` pins the
      population (23 and 43) and the band from outside the package that decides it — the two counts
      move for different reasons, so a rule that started claiming a leader's space would move the
      second without the first.
    - **Mutation testing found the same-line rule written three times, and duplicated arithmetic
      cannot be tested — only tested somewhere.** Six mutants of `leadingIndent`'s own copy of the
      baseline comparison survived (drop the guard, `math.Min` for `math.Max`, either size alone,
      `SpaceFrac` for `LineFrac`, `<` for `<=`), because every fixture that would have killed one
      was already killing its twin in `gapSpace`. The answer was to delete two of the three copies
      rather than to write six more fixtures: `sameLine` is now the one reading of "the same line"
      this package has, `newLine` is its negation, and `gapSpace` and `leadingIndent` both call it.
    - **A fixture that clears a threshold by an order of magnitude tests the comparison's sign, not
      its constant.** The first detached-run fixture stood a full 14pt off its text where the
      threshold is 1.5pt, and three mutants of the threshold itself survived it — `LineFrac` for
      `SpaceFrac`, `WideSpaceFrac` for `SpaceFrac`, and the em in place of the space advance — while
      each adopted **8 further corpus runs** at a gap of 2.184pt. The corpus test could not see
      them either: `indentGap` is a deliberate copy of the rule, so it measures the population and
      not the code. Retightened to 1.6pt against 1.5, which is 1.07×.
    - **The corpus cannot reach five of the rule's conditions**, so they are pinned by fixtures and
      the counts say why: 0 of the 66 untagged runs are non-whitespace *and* line-first *and*
      attached, 0 are non-ASCII whitespace, 0 are empty, 1 of 66 is followed by another untagged
      run, and 0 adopted pairs differ in type size. Of the 23 adopted runs, 18 stop short of their
      text by up to 0.028pt, 4 overlap it by up to 0.243, and exactly 1 meets it — which is why
      attachment is `|X0 − X1|` against a real tolerance and not a signed test or an exact one. The
      empty case is the one whose guard looks redundant and is not: `TrimFunc` of the empty string
      *is* the empty string, so "no non-space characters remain" is true of a span with no
      characters at all, and the mutant that drops the `== ""` half adopts a span that draws
      nothing. It survived a full 41-mutant run on an equivalence argument that a five-line
      program disproved.
    - **`Block.MCIDs` is documented as a union and three of its four writers do not build one, which
      this change makes 23 entries worse against a pre-existing 34341.** `doc/block.go:247` calls
      the field a union; `extract` (`run.go:1113`, `hasMCID` + `mcid >= 0`) and `layout/tables.go`'s
      `mcidsOf` honour that, `sectionize.go:995` appends one entry per span with neither the dedup
      nor the `>= 0` filter, and `:1445` filters but does not dedup. A two-line adopted listing
      emits `MCIDs=[1 1 2 2]` where the union is `[1 2]`, and the second copy of each is the
      adopted indent carrying its successor's key — which is the whole point of the copy, so the
      duplicate is a consequence of the fix and not a mistake in it. Measured by switching adoption
      off: 34341 duplicate entries become 34364, exactly the 23 adopted runs, with no block changing
      category. **Not fixed here and not a wrong answer today, because nothing reads the field for
      logic** — a repo-wide grep finds writers, a probe counter, and no consumer, which is also why
      the corpus render is byte-identical. Logged rather than bundled: the fix is one call shared by
      all four writers, it touches `layout` and `sectionize` together, and 34341 of the 34364
      entries have nothing to do with listings. **Fixed as its own unit of work below.**
- **A dropped artifact welded two words together, in 3 of PDF-Declarations' 13 contents entries.**
  The entry reads `2 Scope1` where the page draws `2 Scope` … `1`. Nothing geometric is wrong:
  `extract` puts the dotted leader in a run of its own whose text *ends in a space*, and infers one
  across the gap besides. The loss is in the tagged rebuild. `take` reads the MCID index and
  `newIndex` skips `MCID < 0`, so a span drawn outside marked content cannot be claimed by any
  element — it reaches `Unplaced` instead — and **decoration is exactly what producers leave
  unmarked**. The two spans either side of the leader were then concatenated across 395.57pt.
  - **The class is 6 of 14538, and the band the threshold sits in is empty for 4.4×.** Over every
    same-line adjacent pair in a sectionized block that joins with no space on either side —
    excluding the spans `sectionize` fabricates, since a count taken after the rule runs measures a
    different population — the gap as a multiple of the test runs **p50 0.007, p90 0.073,
    p99 0.355**, then clusters densely from **0.404 to 0.435** (69 pairs, every one an ISO 32000-2
    mathematical variable meeting punctuation — a subscript touches its bracket), and then stops:
    **nothing until 1.918**. Above that lie exactly six — 1.918, 2.515, 2.596 in ISO 32000-2's
    L\*a\*b\* definition and 99.873, 152.694, 219.760 in PDF-Declarations' contents list. So the
    order of magnitude carries the rule, not the constant; but the band is 4.4× and not 53×, and
    getting that wrong is what let the error below through.
  - **Two thresholds, because `extract` uses two — and the first draft used one.** The same-line
    test takes the larger of the pair's sizes, matching `run.go:462`'s `maxf(sy, prev.height)`;
    the space test takes the **following** span's advance, matching `run.go:448`, where it is read
    per glyph and never maximised. The first version applied one `math.Max` to both, which is
    the same mismatch class as the entry above — a comment claiming to match an existing rule that
    had not been diffed against it. Correcting it changed the output, and *for the better*: two
    lines in ISO 32000-2's L\*a\*b\* definition where an operator was glued to its operand,
    `𝑥 ≥6 29` → `𝑥 ≥ 6 29` and `=108 841` → `= 108 841`. The `cur` reading is the tighter one, so
    it catches boundaries `max` misses.
  - **The advance was proxied by the em, and that was a 4× error that left this very defect in the
    output.** `extract` measures `SpaceFrac` against the font's nominal space advance; `doc.Style`
    carries `Size` and no advance, and widening the type every sink reads to serve one rule is the
    wrong trade — so an estimate is right. Measuring against the em *directly* is not an estimate,
    it is a different quantity, roughly 4× too wide, and the corpus had a case inside the difference:
    ISO 32000-2 draws `× (𝑥 −` at 11.04pt then `4 29` at 8.04pt with **2.313pt** between them, which
    is 1.918 space widths and only 0.959 ems. The rule scored it a near miss and left `(𝑥 −4 29)`
    joined — the same defect class as the three contents entries, **on the same output line as one
    of the two joins it did fix**. Half an em closes it, and it is not a new constant: it is the
    fallback `extract` uses for a font that reports no space glyph (`run.go:449`), so the one
    quantity this rule cannot read already had an answer in the codebase.
  - **What made the error invisible was that every fixture sat far from the boundary.** Fourteen
    tests, all of them either far above both readings or far below, so the 4× cancelled out of every
    one — and the corpus figures were stated in the wrong units, which made the band look 53× wide
    and the near-miss look like part of the empty gap. Both edges are now pinned to the real
    geometry: `TestNarrowGapAtARealFormulaIsASpace` at 1.918 above and
    `TestWidestJoinedSubscriptGapStaysJoined` at 0.435 below, both stated in points from the file
    rather than round numbers, because those two pairs are the only geometry on disk that separates
    the readings. `TestGapExactlyAtTheThresholdIsNotASpace` moved from a 10pt span to a 20pt one for
    the same reason: written in ems, its exact-boundary assertion was exact about the wrong unit.
  - **A rune test, not a byte scan.** A span ending in U+00A0 or U+2002 has its boundary already,
    and `strings.HasSuffix(s, " ")` cannot see one — the same reason `extract`'s `endsWithSpace`
    decodes a rune. `TestGapAfterANonBreakingSpaceAddsNothing` is the fixture, and the corpus has
    no instance of the shape, which is exactly why it needs one.
  - **A zero type size decides both questions the wrong way at once, and the review found it.**
    At `Size == 0` both thresholds are 0, so every positive gap is a space and every unequal
    baseline is another line. `doc.Style.Size` is `sy` from the composed text matrix with nothing
    clamping it (`extract/run.go:635`), so a `Tf` of 0 or a degenerate matrix produces one.
    Measured: **0 of the 96569 spans `extract` emits**, so this is a guard rather than a case — and
    the same guard `extract` makes on the same quantity one line from where it reads it
    (`run.go:449`). The layer matters and naming it is what resolved a review finding: a *finished
    outline* holds **119** zero-size spans, and every one is fabricated by this package — 113
    newlines from `breakAtBaselines` and `gather`, 6 spaces from this rule, one per firing pair.
    Same field, two populations, and the post-pipeline count is the one a naive measurement reaches
    first. It is also a count the fix itself moved — 118 while the em proxy left the sixth join
    unmade — so it had to be re-measured after the threshold changed rather than carried forward,
    which is the same mistake in miniature as the one the threshold error was.
    Adding it also exposed an entanglement worth recording: `TestGapSpaceIgnoresAFabricatedSpan`
    began failing because its fabricated span had no size, so the new guard was declining before
    the identifier was ever read. Left alone it would have started passing for the wrong reason.
    The fixture is now sized deliberately, which is the only thing keeping the two guards
    distinguishable.
  - **38 mutations, 37 killed, every kill named to a test.** The one survivor is the call order
    against `breakAtBaselines` and is a genuine **equivalent mutant**: the two predicates are
    exclusive per pair — one requires the pair on the same line, the other requires it not — so an
    insertion between one pair cannot change another's answer. Recorded as equivalent rather than
    papered over with a test that would be asserting something untrue.
  - **A second survivor looked equivalent, was not, and is now killed.** Dropping the `MCID < 0`
    guard initially survived every test *and* all 51 files, because every span this package
    fabricates holds whitespace, which the space test rejects on its own. But the guard is decisive
    in itself — the same pair answers `false` with it and `true` without, since a zero box against
    a span at X0 400 reads as a 400pt gap. What made it unreachable was a coincidence about today's
    insertions, not a property of the rule, so `TestGapSpaceIgnoresAFabricatedSpan` calls `gapSpace`
    directly and now kills it. Leaving a geometric rule to be saved by a text rule is what would
    make the next fabricated span — a marker, a separator — a silent defect.
  - **That kill was itself hiding behind an ambiguous anchor, and the size guard exposed it.** The
    mutant had been anchored on the bare `if prev.MCID < 0 || cur.MCID < 0 {` line, which `newLine`
    also holds verbatim, so `replace(…, 1)` was resolving by file order rather than by intent — the
    same harness bug as `mut7.py` below, in the harness written to avoid it. Re-anchoring through
    the following empty-text check is what made the result trustworthy. A find-string that matches
    twice is worse than one that matches never: the miss announces itself, the alias reports
    confidently on the wrong function.
  - **Five of the 38 mutants exist because the 4× error existed.** Four drive `spaceAdvance` itself
    — the em, a quarter em, zero, and a fixed constant — and `advance-is-the-em` is the shipped
    defect reinstated, killed now by `TestNarrowGapAtARealFormulaIsASpace`. The fifth,
    `line-guard-uses-advance`, pushes the estimate into the *line* test, halving a threshold that
    should not scale by it; every existing line fixture had enough headroom to survive that
    unnoticed, so `TestLineTestMeasuresTheSizeNotTheSpaceAdvance` states the 10pt case where 5pt is
    one line and 2.5pt is not. Two thresholds reading one field is the kind of coupling only a
    boundary fixture per threshold can hold apart.
  - **Two harness bugs, both of which had been scoring false kills.** Three mutants that drop a
    `unicode.IsSpace` call leave `last`/`first` unused; that is a compile error, which an
    exit-code harness reads as a kill no test produced, and all three die once made valid. Worse,
    `mut7.py`'s guard anchor became **ambiguous** when `gapSpace` introduced a byte-identical line
    earlier in the same file, so `replace(…, 1)` silently mutated the wrong function — which
    presented as a coverage regression in the *previous* rule and was not one. Both harnesses now
    report `NO-OP` when a replacement leaves the file unchanged, since that state otherwise reads
    as `SURVIVED`.
  - **Five lines change across the 12 outputs, all five are corrections, and they carry six
    joins** — confirmed in both sinks (`okf`'s `table-of-contents.md` reads `- 2 Scope 1`). Measured
    against a baseline generated with the call site switched off, not against an earlier run of the
    rule, which is the difference that made the sixth join visible: comparing two versions of a rule
    shows what changed between them, and only the off-state shows what the rule is worth. The three
    contents entries are independently confirmed by the `/Alt` on each `Link`, which holds the
    correct text — see the entry below. The formula lines still show `6 29` for 6/29 and `4 29` for
    4/29, which is the pre-existing math-layout limit and untouched by this change.
- **`/Alt` on a `Link` never reaches a block — measured, and closed as a non-defect.** Raw counts
  over the 51: 413 elements carry `/Alt` — `Figure` 218, `Link` 194, `Table` 1 — and only the 218
  arrive, because `RoleLink` has no `doc.Role` and the element goes with its description. The
  question is whether the 194 cost any text, and they do not. `Alt` is a stand-in for content
  that draws nothing: both consumers read it only when the block's own text is empty
  (`sink/markdown/markdown.go:568`, `sink/okf/okf.go:567`). Of the 194, **0 describe a block that
  draws nothing** and **192 restate the visible text exactly**, so carrying them would add
  nothing to any output. The remaining debt is the annotation/cross-reference mapping — a link's
  *target* — which is deferred separately and is not about `/Alt`.
  - The 2 outliers were worth the measurement: they are contents entries whose `/Alt` disagreed
    with the drawn text, which is how the leading-space defect above was found. Where the two
    disagree the `/Alt` is a gold answer already on disk, and it confirmed all three entries
    independently of the geometry.
- **A field's contract was stated once and implemented four times, and three of the four built
  something else.** `doc.Block.MCIDs` is documented on the field as the identifiers a block was
  assembled from, and at `doc/block.go:247` explicitly as a **union**. Each writer then implemented
  that separately: `extract` deduplicated and skipped −1, `layout`'s `mcidsOf` did both,
  `sectionize`'s `emitItem` did neither, and its `unplaced` filtered −1 without deduplicating. A
  four-span element across two sequences recorded `[1 1 2 2]`. `doc.Block.SetMCIDs` is now the only
  implementation and both helpers are deleted.
  - **Layer the measurement, because the aggregate hid where the defect was.** Over every block the
    tagged pipeline produces for the corpus, 34364 entries on 7406 of 46722 blocks were duplicates
    and 88 were −1 — and the split is total: page blocks 0 and 0, section blocks 34141 and all 88,
    preamble 211, unplaced 12. The contract held exactly where someone had implemented it, so a
    single corpus-wide number would have read as a pipeline-wide problem when it was a per-writer
    one.
  - **Nothing reads the field, which is why the violation was invisible and why it still mattered.**
    A repo-wide grep finds four writers, a `probe` counter of an unrelated quantity, and no logic
    consumer; nothing serializes it. The 12 rendered documents are byte-identical across the fix. So
    the cost was not wrong output — it was a diagnostic that lies, and the field exists to answer
    "which MCIDs went where" during exactly the investigations a wrong answer would misdirect.
  - **A fresh slice, not a truncated reuse, because a struct copy of a `Block` shares the array
    behind `MCIDs`.** That is the whole reason `sectionize.detach` exists, and `layout`'s
    `spanBlock` starts from `b := *src` before rebuilding — so writing into `b.MCIDs[:0]` would edit
    a sibling's set through shared storage while the sibling kept its own length and read values
    computed for another block. No render can show it, so it is pinned by a fixture. Linear scan
    rather than a map: the largest block on disk holds 106 spans (ISO/TS 32005), so a map's
    allocation costs more than the scan it saves.
  - **The helper was fully covered while three of the four sites it exists for were pinned by
    nothing.** 6 of 6 mutants of `SetMCIDs` die to a named test; deleting the call in `layout`'s
    `spanBlock`, `sectionize`'s `emitItem` and its `unplaced` each left the whole suite green. The
    arithmetic was tested and the wiring was not, which is the same failure class as a vocabulary
    that is written, unit-tested and never reached. `layout` could not observe it at all: every
    fixture in that package builds spans at `MCID: -1`, where a union and a whole row's set are
    both empty. Each site now has a test that fails when the call is removed.
  - **The corpus assertion is an identity, not a defect count.** `TestBlockMCIDsAreTheUnionOfTheirSpans`
    recomputes the union for every block and requires equality, with block floors as the vacuity
    guard — 0 disagreements means nothing if the walk found no blocks. A count of duplicates would
    need re-deriving whenever the corpus or the extractor moved; the identity is what the field
    means and does not drift. **A walk is only as wide as the pipeline it runs**: the first version
    reached 46722 blocks and three of the four write sites, because `outlineOf` stops at
    `sectionize` and never runs the layout passes — so the one site that had neither a corpus
    population nor a fixture stayed uncovered by the test written to cover it. Calling `inferRoles`
    and re-walking the pages brings it to 72218.
  - **Two figures carried from the log entry were wrong, and only re-measuring caught it.** The log
    said 34127 duplicates on 7411 blocks, which came from a narrower walk; over the whole pipeline
    it is 34364 on 7406. A remembered 3308 blocks carrying −1 was out by more than an order of
    magnitude — the real population is 88 entries on 6 blocks. The delta did reproduce exactly:
    adoption off gives 34341, 23 fewer, which is the 23 adopted indents each carrying its
    successor's key by design. A figure inherited from a log entry is a figure about whatever walk
    produced it.
- **A geometric rule was comparing two coordinate spaces, and the corpus could not tell.** A
  `Box.Y0` is a position within *its own page's* user space, and `sectionize` joins spans in the
  order a structure element lists its content — so a paragraph continuing past a page break is one
  element naming two pages, and `sameLine` was measuring page *n+1*'s baseline against page *n*'s.
  `doc.Span` now carries `Page`, stamped by the extractor, and `sameLine` refuses the comparison
  outright. Both rules that ask — `newLine` and `gapSpace` — inherit it, because `sameLine` is the
  one reading of "the same line" this package has.
  - **The logged position was too pessimistic by an order of magnitude of effort.** The entry above
    declined the fix because "the honest fix is a page number on `doc.Span` and that is a wider
    change than this defect justifies". It is not wide at all: of the 12 non-test `doc.Span{…}`
    construction sites in the repo, exactly **one** produces geometry (`extract/run.go:1105`) — every
    other fabricates a span to carry text with no position — and the page number is already in
    scope at that site's caller, since `Extractor.Page(n)` builds `doc.Page{Number: n}` before
    calling `run.blocks`. Three lines of threading, one field, no consumer to migrate. What made it
    look expensive was counting construction sites instead of *geometry-producing* construction
    sites.
  - **The two rules fail in opposite directions from the same cause, so both are pinned.**
    `newLine` loses a break it should write (a listing welds into one line); `gapSpace` writes a
    space it should not (page 1 ending at x=100 and page 2 starting at x=400 read as one line with
    a 300pt hole, so "continuation" arrives as "continua tion"). Which one a file gets depends only
    on where its text happens to sit.
  - **Nothing on disk changes, and the measurement says why.** 7 adjacent cross-page pairs across
    the 11 tagged documents, and the smallest baseline step is **107.7× its own threshold** —
    because a paragraph that breaks does so at the bottom of one page and resumes at the top of the
    next, so the two baselines are most of a page apart. All 7 already carry a space at the join
    from `extract`'s wrapped-line rule. The 12 rendered documents are byte-identical. That margin is
    a fact about where those particular paragraphs break, not a property of the comparison: a short
    page, a two-line footnote, a table continued in a header band steps by nothing at all, and no
    threshold can separate that from a real line.
  - **The fix invalidated the rationale for a neighbouring choice, which is the part a diff review
    would miss.** `sameLine` is unsigned, and its stated witness was the cross-page rise — the one
    pair that no longer reaches the arithmetic. Measured over the corpus: **0 upward steps in 3357
    adjacent same-page pairs**, so a signed comparison now passes every corpus assertion in the
    repo, and `TestCodeBreaksOnAnUpwardStep` is the only thing holding it. Recorded on the rule, so
    the next reader does not take a fixture's claim for a measured one.
  - **Review found a second cross-page comparison the guard does not reach.** `substituted`
    replaces an element's whole run with one span holding the declared `/ActualText`, and unioned
    every span's box into the survivor — a union across a page break is a rectangle in no
    coordinate space, and the survivor names one page, so `doc.Span.Page` would be the thing
    asserting it. The union now stops at the first page: the text is the declared value either
    way, so what is given up is a box that was never meaningful. Reachable but not on disk — 92
    of 4803 declaring elements list more than one reference, 0 of them cross a page — which is
    why the search was for the *shape* of the defect rather than an instance of it. `sameLine`
    being the single reading of "the same line" is what made the first fix one line; it is not
    what makes every page-mixing comparison route through it.
  - **A third one, in the block builder, found by the review after that — the same shape a third
    time.** `emitItem` unioned every span's box into `doc.Block.Box`, whose comment said "in the
    page's coordinate space", singular. It is false for 7 of the 30301 blocks the tagged path
    rebuilds for the corpus. A block has a box and no page range, where a `doc.Section` carries
    `FirstPage` and `LastPage` precisely because it spans breaks — so the type cannot express what
    the union produced. It now bounds the first positioned span's page, and nothing on disk moves
    because nothing reads the field: `doc.Page.TextBounds` and `Coverage` are its only consumers
    and both walk the extractor's own per-page blocks. Three separate sites, three separate
    reviews, one question — "can these two boxes be on different pages" is not answerable by
    grepping for the rule that was wrong, and each pass found the next one only because the
    previous fix named the shape.
  - **"MCID −1 means no geometry" is false, and the corpus test is what caught it.** The first
    version of the invariant asserted that an unmarked span carries no page; PDF-Declarations draws
    its contents leaders *outside* marked content, and those 5 spans are ordinary rectangles on a
    real page. An empty `Box` is what says there is no position. The invariant is now keyed on the
    box, and asserts both halves — 188140 spans, 188021 carrying a page, 119 fabricated at page 0 —
    plus that 7 cross-page pairs exist at all, so the guard cannot become dead code held up only by
    its fixtures.
- **Clause URI scheme.** `iso32000-2:2020#7.5.8` is a placeholder. Worth checking whether
  a registered ISO identifier scheme exists before baking it into `resource` values.
- **Whether the golden corpus should move out of `docs/`.** The spec PDFs sit in `docs/`
  alongside this document and are gitignored (below). Relocating them to `testdata/spec/`
  or `corpus/` would separate input from documentation and make the golden corpus
  explicit. Deferred until Phase 1 needs a fixture path, since moving them is cheap and
  the layout should follow the test harness rather than precede it.

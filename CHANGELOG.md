# Changelog

All notable changes to this project are documented here, following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added — 2026-08-02

- `docs/DESIGN.md` — the toolkit concept: why the repo exists, the benchmark baseline to
  beat, the non-AI-first stance, clean-architecture package layout, the borrow-then-replace
  dependency policy, OKF output mapping, and a seven-phase roadmap.
- `docs/adr/0001-borrow-pdfcpu-behind-an-owned-object-model.md` — the dependency decision
  that shapes every package boundary.
- MIT `LICENSE`.
- `geom` — `Rect`, `Matrix` (row-vector convention), and `Tolerance`, the single place
  spacing thresholds are defined rather than scattered as inline epsilons.
- `objects` — the owned PDF object model and the `Store` interface every higher layer reads
  through, plus absent-tolerant getters and `DecodeTextString` for PDF text strings
  (UTF-16BE with BOM, otherwise PDFDocEncoded).
- `objects/pdfcpu` — the borrowed-parser adapter, with all pdfcpu types confined to this
  package. Relaxed validation on purpose: the files that most need extraction are the ones
  producers got wrong.
- `tag` — structure-tree reader for the tagged path. Handles every `/K` encoding real
  producers emit, normalizes `/RoleMap`, derives bare-`H` levels from nesting depth per ISO
  32000-2 §14.8.4.4, resolves page anchors on demand, and terminates on cyclic trees.
- `cmd/pdfspec` with the `probe` verb — reports which pipeline path a file will take and
  why, which is what makes the corpus measurable.
- Corpus tests that skip when the gitignored spec PDFs are absent, so a clone without them
  still passes the suite.
- `content` — content-stream reader: a lexer over the §7.8.2 token syntax, a scanner that
  assembles operands into operators with an explicit stack rather than recursion, and the
  graphics and text state machine of §8 and §9. Deliberately tolerant, because content
  streams are the part of a PDF most often malformed and a reader that stops at the first
  oddity loses the rest of the page. Inline images (`BI`/`ID`/`EI`) are scanned specially:
  the bytes between `ID` and `EI` are raw data that routinely lex as operators.
- `filter` — stream decoders: Flate, LZW, ASCIIHex, ASCII85, RunLength, and the PNG and
  TIFF predictors of §7.4.4.4. Image filters (DCT, CCITT, JPX) are passed through as encoded
  data rather than half-decoded, so the image path can own them later.
- Test coverage for `content` (97.7%) and `filter` (93.8%), including corpus tests over 11
  real documents, five fuzz targets, and benchmarks. The corpus pass found zero unrecognized
  operators across 2.76M operators of ISO 32000-2, and MCID coverage splits exactly as the
  design predicts: 90–98% on tagged files against 0 on the untagged paper.
- `font/encoding` — the base encodings of Annex D as code-to-glyph-name tables, plus a
  reduced Adobe Glyph List. Validated against `golang.org/x/text/encoding/charmap`, which
  derives its tables from the Unicode Consortium's files rather than from these: two
  independent sources agreeing is evidence, one source restating itself is not. That
  validation is also what identified the three places PDF deliberately departs from the
  platform encodings, each now asserted individually so a fourth cannot appear quietly.
  Resolution returns a *string* rather than a rune, because a glyph is not always one
  character — `f_t` has no precomposed code point, and returning one rune per glyph is how
  "efficient" becomes "ecient".
- `font/cmap` — CMap reader for both roles: the code-to-CID mappings of composite fonts and
  the code-to-Unicode mappings of `/ToUnicode`. One parser, because they share a syntax.
  Reuses `content`'s lexer rather than carrying a second tokenizer for the same tokens.
  Byte-splitting per §9.7.6.2 is the part that matters most: a reader that assumes two-byte
  codes because Identity-H is common mis-splits every mixed-width CMap and, because the
  result is still a sequence of plausible codes, emits confident wrong text rather than an
  error.
- `font` — the loader that answers the two questions extraction asks of a font: what a
  character code means, and how far it advances. Both from one `Font`, because separating
  them is how extractors end up with correct characters in the wrong order, or with the
  4,069-character "word" a single dropped advance produces. Includes glyph-name-keyed
  standard-14 metrics for the Helvetica and Times faces, derived from pdfcpu's AFM data and
  cross-checked at test time. The check is a known-answer control rather than an inspection:
  pdfcpu returns 1000 for a glyph it cannot find, which is indistinguishable from a real
  width of 1000 — and several real entries *are* 1000 — so all four Courier faces are
  asserted to return exactly 600 for all 216 WinAnsi glyph names, which no lookup miss could
  satisfy.
- `objects.Store.Decode` and the `GetStreamData` helper. Decoding belongs on the interface
  because most streams worth reading are not page content — a `/ToUnicode` CMap, an embedded
  CMap, a font program — and `Resolve` returns those with `Decoded` nil. Reading one without
  decoding yields nothing and looks like an empty stream rather than an un-decoded one, which
  is exactly how every `/ToUnicode` CMap in the corpus first read as empty.
- `doc` — the extracted-document model, and the convergence point of the whole design. The
  extractor, the layout heuristics, the OCR engine, and the structure-tree walker all
  *produce* a `doc.Page`; the Markdown and OKF sinks all *consume* one. Neither side knows
  the other exists, which is what lets the OCR path arrive later without touching a sink.
- `extract` — text extraction: the package where the spaces problem is actually solved. PDF
  records glyphs and positions, not words, so reconstructing a word needs the advance width
  (`font`), the composed text rendering matrix (`content`), and one threshold policy
  (`geom.Tolerance`) at the same moment. Positions project onto the baseline so rotated and
  skewed text reads correctly, orientation is bucketed to 15° so a fitted-curve label does
  not fragment per glyph, fragments split on style change and marked-content boundary, and
  `/Artifact` content — the running header repeated on 1,023 pages — is dropped by default.
  Stream order is preserved as reading order: sorting lines would interleave the two columns
  of a spec page.
- `font.Font.Name`, `Bold`, `Italic`, and `Monospaced` — the typographic identity a Markdown
  sink needs to emit emphasis or recognize a code span. Derived from the descriptor and the
  `/BaseFont` name together, because producers routinely omit `/FontWeight` and set no italic
  flag on a font whose own name says "BoldItalic", while name-only detection fails on every
  subset font named by its foundry. `Name` strips the subset prefix, since the same typeface
  subset twice gets two prefixes and a consumer grouping by name would break a paragraph at
  every subset boundary.
- `extract` unit tests over hand-written content streams, one operator sequence per test.
  A fixture PDF cannot be edited to isolate "does a TJ adjustment of -400 produce a space,"
  which is the only question several of these tests ask. The standard-14 fonts are used with
  no `/Widths`, so every advance comes from `font/metrics.go` rather than from numbers
  written into the test.
- `cmd/pdfspec` extraction metrics tests — the `docs/DESIGN.md` §1 table, measured rather
  than asserted, over the whole corpus. ISO 32000-2: 1,023 pages, 1.96M non-space characters,
  383,956 words, 18.46% spaces, 0.06% of words past 25 characters, longest genuine word 73,
  in 1.6s. The arXiv paper's bars come from running two other extractors over the same file:
  we find 39,257 non-space characters to pdftotext's 39,035 and pdfplumber's 39,089 — the
  three agree on the characters to within 0.6% — but 6,343 words against pdfplumber's 2,392
  from those same glyphs, with 0.30% of words past 25 characters against its 15.8%. That is
  the §1 spaces problem measured on a document rather than quoted from a table.
- Corpus tests for the three font packages, every count measured before it was pinned. 262
  font dictionaries across the 11 documents: 166 simple, 96 composite, all 96 carrying
  `/ToUnicode`, and 29,952 of 29,952 encoded codes resolving to text. Reaching all 262
  required descending into Form XObject resources and the AcroForm `/DR` — 36 fonts live
  only there, and a traversal stopping at the page dictionary omits them silently, which in
  an extractor means the text inside every form comes out undecoded.

### Added — 2026-08-03

- `sink/markdown` — the Markdown writer, and the completion of Phase 1's pipeline. Consumes
  `doc` and nothing else: no PDFs, no fonts, no glyph positions, so a page recovered by OCR
  and a page recovered from a content stream render identically. Writes to an `io.Writer`
  and never touches the filesystem, because where files go and what they are called is a
  decision only the command can make. No page markers and no rules between pages — a
  paragraph continuing across a page break is one paragraph, and recovering that is
  `sectionize`'s job, not something to make harder by asserting a boundary here.
- `sink/markdown` escaping — the package's real risk surface, and deliberately narrow.
  Extracted prose contains most of Markdown's syntax as ordinary text: a PDF specification
  is full of `<</Type /Page>>`, `snake_case` identifiers, `[1]` citations, hyphenated
  compounds, and clause numbers like `7.5.8`. Each metacharacter is escaped only where
  CommonMark makes it live — `_` and `~` at word edges only, `-`/`#`/`>`/`+`/`=` at line
  start only, `[` only when a `]` followed by `(`, `[`, or `:` follows it, `<` only when a
  tag or autolink could begin, `&` only for a well-formed entity. Measured result: 0.34
  backslashes per 1,000 characters on the arXiv paper and 0.16 on ISO 32000-2, so the
  output reads as prose rather than as backslashes, and `<pdfd:conformsTo>` is still
  escaped where raw HTML would otherwise be swallowed and vanish.
- `sink/markdown` frontmatter — optional YAML, off by default, with a fixed field order so
  two conversions of the same document diff cleanly. Hand-written rather than marshalled:
  the output is a flat map of strings, integers, and booleans, so all a YAML library would
  contribute is the quoting rule, and `gopkg.in/yaml.v3` reorders keys. Dates are emitted
  as the strings the file contained — a PDF date is `D:20240131120000Z` and frequently
  malformed, so converting to a YAML timestamp would mean dropping the ones that fail to
  parse or inventing a value. `tagged` and `encrypted` are always emitted including when
  false, because they report which extraction path ran and an absent key cannot be told
  from a key the writer did not know about.
- `cmd/pdfspec md` — the verb from `docs/DESIGN.md` §2. One long `.md` by default,
  `-split` for one file per page, `-frontmatter` and `-artifacts` as settings. Split names
  are zero-padded to the width of the highest page number: without it page 10 sorts before
  page 2, and on a 1,023-page specification that makes the output unusable in any file
  browser. Every page gets a file including a blank one, since a gap in the numbering reads
  as a conversion that failed there.
- `sink/markdown` tests built from `doc` values rather than fixture PDFs — this package's
  input is `doc.Document` and nothing else, so a fixture would test `extract` as well and
  could not express the cases that matter: a span boundary landing on the space after a
  bold word, an `/Alt` containing a newline, a title with a colon in it. The escaping tests
  assert against real corpus strings.
- `cmd/pdfspec` end-to-end `md` tests, which re-measure the §1 metrics at the *end* of the
  pipeline. The extraction metrics tests assert on `Document.Text()`; nothing there would
  notice a sink that dropped a block or joined two paragraphs. Through `md` on the arXiv
  paper: 39,781 non-space characters, 6,343 words, 14.04% spaces, 0.33% of words past 25
  characters, longest 71, 18ms. On ISO 32000-2: 2.03M non-space, 383,956 words, 0.06% past
  25 characters, 1.9s. Split output is checked to match whole-document output
  character-for-character on non-space text, since a difference means one of the two paths
  is wrong.
- `TestMDEmitsNoHeadingsYet` records a Phase 1 limitation as a test rather than leaving it
  to be discovered. `extract.roleOf` assigns `RoleParagraph` to every non-artifact block on
  purpose — heading level is *declared* by the structure tree and reading it is
  `sectionize`'s job — so ISO 32000-2's Markdown is currently flat prose despite `tag.Read`
  finding 981 headings in the same file. The sink renders headings correctly when given
  them; nothing gives it any yet. The test inverts when `sectionize` lands.
- `docs/adr/0002-derive-sections-from-the-heading-sequence.md` — why sections come from the
  heading sequence rather than from container nesting, with the measurements that forced it and
  the `tag.MCRef` API change that followed. Recorded as an ADR because it is a long-lived
  convention both the tagged and the untagged path are built on, and because it changed a
  public type.
- `doc.Section` and `doc.Outline` — the clause hierarchy, and the second thing every sink
  consumes. A `Section` carries its title, clause number, level, page range, own blocks, and
  children; an `Outline` carries the roots plus a `Preamble` for content before the first
  heading and `Unplaced` for content no section claimed. `Unplaced` exists because dropping
  text is never the right default for an extractor and a wrong attribution is worse than an
  absent one — ISO 32000-2 draws the whole of clause 1 outside any marked-content sequence,
  so no structure element names it, and filing it under the preceding clause would put the
  standard's Scope inside "0.4 Changes introduced in ISO 32000-2:2020".
- `sectionize` — the package that turns a structure tree into a clause hierarchy, and the
  one whose algorithm `docs/DESIGN.md` §3 had to be corrected for. Sections come from the
  **heading sequence**, not from container nesting: ISO 32000-2 has 7 `Sect` elements against
  981 headings, 966 of those 981 headings have no element children at all, and one `Part`
  holds 13,442 direct children as a flat `H1 P P P …` stream. A clause's body is therefore
  its heading's *following siblings*, and the hierarchy is a level stack over a linear
  sequence — which is also why the same builder will serve the untagged path, where headings
  are recognized rather than declared. Measured, tagged path:

  | file | sections | titled | numbered | max level | blocks | roots | unplaced |
  |---|---|---|---|---|---|---|---|
  | WTPDF 1.0 | 183 | 183 | 173 | 6 | 943 | 13 | 6 chars (0.004%) |
  | ISO/TS 32001 | 14 | 14 | 10 | 3 | 115 | 9 | 0 |
  | ISO/TS 32005 | 27 | 27 | 23 | 4 | 692 | 11 | 0 |
  | ISO 32000-2 | 981 | 981 | 851 | 5 | 29,218 | 48 | 5,734 chars (0.231%) |

  Zero characters lost on all four, and placed plus unplaced equals the document's own total
  exactly — the accounting is an equality, not a bound, so a block partly claimed by a
  section is rebuilt from its unclaimed spans rather than repeated whole.
- `sectionize` title resolution from **content**, not from `/T`. Not one of ISO 32000-2's 981
  headings carries a non-empty `/T`, nor one of WTPDF's 183, so a reader trusting the
  attribute produces an outline of untitled sections. Titles are joined to the heading's own
  marked content on `(page, MCID)`, with `/ActualText` last rather than first: it is a
  substitution for what the glyphs spell, and where both exist the glyphs are what a reader
  checking the conversion against the page will see.
- `sink/markdown.WriteOutline` — the outline renderer. The same sink as `Write` with the
  headings added, and the page boundaries gone: a paragraph continuing across a page break is
  one paragraph, which is the whole reason the tagged path exists. Unplaced text is emitted
  last, each page behind an HTML comment naming it, rather than interleaved by page — a
  comment renders as nothing and greps as something, where a heading would put a clause in the
  outline that no heading in the document corresponds to.
- `cmd/pdfspec md -flat` — page-ordered prose even for a tagged file. The outline is now the
  default when a structure tree resolves, and `-flat` is both the escape hatch for a reader
  who wants the document as it was laid out and the reference output the conservation tests
  compare against.
- `sectionize` unit tests built from hand-written `tag.Tree` and `doc.Document` values, one
  behaviour per test: the level stack, transparent containers, inline elements not splitting a
  paragraph, the span-level join, cross-page content, list depth, table cells, figure `/Alt`,
  unknown roles staying transparent. Fixtures put one block per page rather than one per run,
  deliberately — that is the shape the extractor actually produces, and a fixture with one
  block per run would pass under a block-level join too and prove nothing.
- `cmd/pdfspec` corpus acceptance tests for the outline: exact section counts, the
  heading-sized-title bound that guards the span-level join from the inside, hierarchy
  well-formedness, page ordering, character conservation, and an untagged file yielding no
  sections. The exact counts are load-bearing in one direction — a run that returns single
  digits from a 1,023-page standard has reverted to container-driven segmentation, which is a
  silent failure that produces a plausible-looking outline.
- `sink/okf` — the Open Knowledge Format v0.2 bundle writer, and the completion of Phase 2.
  One clause becomes one concept document; a clause with subclauses becomes a directory
  holding an `index.md`, a concept document named for itself, and its children. `Bundle`
  returns the files as values and `Write` is a thin wrapper over it, so the whole corpus
  acceptance suite runs without touching the filesystem. Measured on ISO 32000-2: **996
  concept documents, 196 indexes, 1,328 resolved cross-references, 1,193 files**, longest path
  145 characters. On WTPDF: 186 concepts, 35 indexes, 222 files, longest path 122. Zero
  letters or digits lost against the flat conversion on either.
- `sink/okf` emits only fields it can support. `sources[].author` is omitted, not set to
  `ISO`: OKF §7 makes an actor `<producer>/<version>`, `human:<id>`, or `process:<id>`, and
  consumers classify trust by detecting the `human:` prefix, so an organization name there is
  unclassifiable. `generated.by` is `pdfspec/<version>` for the same reason. Dates are
  range-checked and dropped when unusable, and `log.md` is skipped entirely rather than
  emitting a non-conformant `unknown` heading. `status: draft` is written on every document
  *because* §11 defaults an absent `status` to `stable` — nothing has verified that a clause's
  text matches its page, so silence would assert the opposite of the truth. See
  `docs/adr/0003-one-clause-per-file-okf-bundle.md`.
- `sink/okf` cross-reference resolution, textual: a cue word (`clause`, `annex`, `see`, `§ `)
  followed by a dotted clause number the document actually contains. Deliberately narrow —
  `in` and `of` precede more version numbers than clause numbers, and a missed link costs a
  consumer one search where a wrong one sends it to the wrong clause and looks authoritative
  doing it. Code spans and fenced blocks are skipped whole, since a specification quoting
  `see 7.4` means the literal. Resolving from `/Annots` and `/Dests` is strictly better and
  remains the target. WTPDF resolves zero, which is correct: its reading order draws the
  clause number after a closing parenthesis, so its own references extract as `see ).8.2.6`.
- `sink/okf` path budgeting, enforced rather than assumed. Windows caps an absolute path at
  260 characters, so `MaxPath = 150` leaves room for a destination directory, and `fit` picks
  the first of three candidate names that fits: the full `7-4-1-general`, then `1-general` with
  the parent's number dropped, then the bare clause number. The failure this prevents is not a
  long filename, it is a write that dies partway through a 1,193-file bundle with an error
  naming the path and not the cause.
- `sink/okf` unattributed content as concept documents that say so — `/front-matter.md` and
  `/unplaced/page-NNNN.md`, both carrying `pdf_unattributed: true`. Both on by default: a
  bundle that drops content by default is one whose omissions nothing reports.
- `sink/markdown` exports `WriteBlocks`, `YAMLString`, `InlineText`, and `LinkLabel` for
  `sink/okf`, which composes markdown that is neither a whole document nor a page. Exported
  rather than reimplemented because two escaping policies diverge, and the first clause
  containing a PDF dictionary would then come out one way in the flat conversion and another
  in the bundle from the same extraction. `LinkLabel` escapes both brackets unconditionally
  where the prose policy escapes `[` only when it could open a link — correct for prose, wrong
  inside a label, and ISO 32000-2 has clause titles containing brackets.
- `cmd/pdfspec okf` — the bundle verb. `-o` is required; `-id` overrides the document
  identifier in resource URIs, which matters because the sponsored ISO PDFs carry no `/Title`
  and the filename fallback yields `iso-32000-2-sponsored-ec3#7.4.8` rather than
  `iso32000-2:2020#7.4.8`. Untagged files are refused with the reason rather than emitting a
  bundle of one clause. The timestamp is injected by the command, not read inside the sink, so
  rendering is deterministic and testable.
- `docs/adr/0003-one-clause-per-file-okf-bundle.md` — the bundle layout, the resource URI
  form, and the emit-only-what-is-true policy. A public contract: a consumer citing
  `iso32000-2:2020#7.5.8` or linking a bundle path breaks if either changes.
- Tests for `sink/okf` (unit, over hand-built outlines), `sink/markdown` exports — including
  one asserting `WriteBlocks` and `Write` produce byte-identical output for the same block —
  and `cmd/pdfspec` corpus acceptance: every file is something OKF describes, every `/`-rooted
  link resolves to a file that exists, no path contains a character Windows rejects, and the
  bundle conserves the flat conversion's text.
- `bits` — MSB/LSB bit reader over a byte slice, stdlib-only and with no knowledge of PDF.
  `Align` is the call that matters: sample data below 8 bits per component packs several
  samples to a byte and re-pads at every row boundary (§8.9.5.1), so a reader that treats the
  stream as one continuous bit sequence decodes row 1 correctly and skews every row after it.
  That diagonal skew presents as a decoder bug rather than an alignment one, which is why it
  has a test of its own. The aligned-byte fast path is deliberately MSB-only — see Fixed.
- `image` — image XObjects as a domain type plus an encoder. Codec classification (Raw, JPEG,
  CCITT, JBIG2, JPX) is read off the filter chain `filter` stopped at, so a Flate-then-DCT
  stream arrives already decompressed and needs no further decode to be written as a `.jpg`.
  Colour spaces: DeviceRGB, DeviceGray, DeviceCMYK, ICCBased (for `/N` only), Indexed with
  palette expansion and `/HiVal` clamping, CalRGB, CalGray, and Lab. Soft masks become an
  alpha channel and remain available as their own image; `/Matte` is reported through
  `Premultiplied` rather than silently applied. Stencil masks render black-on-transparent
  because the colour they paint is graphics state the extractor never sees. `/Decode` is
  applied, and ignored when its length disagrees with the component count — a producer error
  that, applied partially, recolours some channels and not others.
- `cmd/pdfspec images` — extracts every image XObject, deduplicated by indirect reference,
  including the 7 that sit inside a Form XObject. `-list` reports without writing, `-masks`
  (default on) writes soft masks as separate `-mask` files, `-min` filters by pixel count. A
  write that fails removes its partial file: a truncated image looks like a successful
  extraction and fails only when something tries to open it.
- `docs/adr/0004-extract-images-without-compositing.md` — the phase's cross-cutting decision
  and the corpus measurement that forced it. 143 of 245 images carry an `/SMask` and 136 of
  those carry `/Matte [0 0 0]`, so the base samples are premultiplied against black and are
  not the colours they appear to be. An extractor cannot resolve that; it can only emit both
  layers and say which is premultiplied.
- Tests for `bits` (8 unit tests plus `FuzzReader`, 1.39M executions clean) and `image` (29
  tests). The image tests assert against the bytes `Encode` writes by reading the PNG back,
  not against an intermediate. CCITT fixtures are derived by hand from the ITU T.6 code
  tables, since no corpus file is CCITT — the test comment states that this checks the
  parameters reach the decoder rather than that the decoder is right, which is a weaker
  guarantee than the rest of the package has and the reason CCITT stays borrowed.
- `cmd/pdfspec` corpus baselines for images: 224 / 49 JPEG / 175 raw on ISO 32000-2, 143 soft
  masks with 136 premultiplied across the corpus, all 245 images encoding to a file that
  decodes, and zero JBIG2 or JPX. The last is an alarm, not a fact: a file that introduces one
  fails that test by name, which is the notice that the JBIG2 port has moved from Phase 6 to
  now.
- `testdata/` — 30 reference PDFs from PyMuPDF, PyMuPDF-Utilities, and Adobe's PDF Services
  samples, committed with `manifest.json` recording each file's upstream repo, pinned commit
  SHA, path, SHA-256, and the reason it is here, and `fetch.ps1` to re-fetch and verify.
  Selection rule is reference-intent: a file is included only if upstream authored it *as* a
  test fixture. PyMuPDF's `test_NNNN.pdf` bug-report attachments are excluded, because a file
  a third party uploaded to a tracker is not upstream's to license, and the ISO specification
  copies both Adobe repos redistribute are excluded for the reason the sponsored ones in
  `docs/` are gitignored. 22 of the 30 are AGPL-3.0 upstream: data in a marked directory with
  attribution is mere aggregation and does not reach the MIT Go source, and no PyMuPDF or
  MuPDF code is used or read for copying.
- `testdata/pymupdf-utilities/ocr-ed.txt` — the only statement in this repository of what a
  right answer is that this project did not write. Upstream ships it beside `ocr-ed.pdf` as
  the text that page's OCR layer holds. Every other assertion in the suite is a baseline
  measured from our own output, which catches a regression but cannot catch a mistake that
  was already there when the baseline was taken; this one can, and did on its first run — see
  the `sink/markdown` OCR entry under Fixed.
- `cmd/pdfspec/testdata_test.go` — 20 tests over the reference fixtures, which never skip.
  They cover what the corpus structurally cannot: the corpus is eleven documents from one
  family of producers, ten of them tagged standards prose, so it exercises the tagged path
  almost exclusively and has no CJK, no ligature case, no deliberately broken font, no
  zero-byte file, and only one producer's `/SMask` habit. New cases pinned here — space
  inference staying silent between Han characters, a broken font costing its glyphs and not
  its page, `/SMask` with no `/Matte` reporting not-premultiplied, a cyclic outline
  terminating, a Type3 font with no recoverable text producing empty output rather than an
  error, a zero-byte file rejected with a reason by every verb, and probe's routing decision
  on 17 files across all four paths.
- `docs/test.docs.md` — where the fixtures came from, what each covers that the corpus does
  not, the ground-truth comparison and the defect it found, the producer-stub heuristic and
  its independent witness, and Adobe's PDF-to-Markdown element taxonomy as a coverage
  checklist for the sink. Four elements are unmapped and two positions differ deliberately
  from Adobe's: hidden text is extracted, since an OCR layer is invisible and is the entire
  content of a scanned page.

### Added — 2026-08-04

- `render` — the rasterization interface: `Rasterizer` (`PageCount`, `Page`, `Close`),
  `Options` (DPI, `MaxPixels`, `Annotations`), `Raster` (image, the DPI actually used, and
  the page box **with `/Rotate` applied**, which is the documented difference from
  `doc.Page.Box`), and `Fit`, which is the whole of the resolution policy. `Fit` lives in
  this package rather than in an adapter because every backend must apply it identically —
  a native rasterizer that capped differently would make two backends disagree about the
  same document — and because it is the one part of rendering testable without a rasterizer
  at all. Writing that test found a real defect in it: a page reduced to land exactly on
  `MaxPixels` and then rounded *up* on both axes lands past the bound (US Letter reduced to
  fit 2,097,152 pixels is 1272.999 × 1647.411, whose product is the cap exactly, and
  1273 × 1648 is 2,097,904 — 752 pixels over). Capped pages now floor; uncapped pages still
  ceil, because rounding down otherwise
  drops the last fractional row of every A4 page. The code was fixed rather than the bar
  lowered, and the comment that had claimed this could not happen now records that a test
  disagreed.
- `render/pdfium` — the adapter over `klippa-app/go-pdfium` (MIT) on the WebAssembly
  backend. The WASM build rather than the CGO one is a requirement, not a preference:
  DESIGN.md §9 commits to pure Go, no CGO, single binary, cross-compiles, and rasterization
  is the one borrowed layer that would otherwise break it. No go-pdfium type appears in any
  exported signature, so the Phase 6 native rasterizer is a sibling package and a one-line
  wiring change. ADR 0005 records the cost, all measured: **+10.0 MB of binary**
  (`cmd/pdfspec` 10,416,128 → 20,890,624 bytes on windows/amd64, of which the embedded
  `pdfium.wasm` is 5,225,611), ~1.4 s of one-time module compile, 3–8 ms per page at 200
  DPI, and **a second parse of the file** — pdfium brings its own parser and cannot be
  handed an `objects.Store`, which is why `Rasterizer` declares its own `PageCount`.
- `cmd/pdfspec render` — pages to PNG or JPEG. `-o`, `-dpi` (default 200), `-pages` with the
  usual `1,4-9,20-` syntax, `-format`, `-quality`, `-jobs`, `-annots`, `-maxpixels`. Files
  are zero-padded to the *document's* page count so a listing sorts in page order on a
  285-page manual. One page that fails does not fail the run — a 151-page scan with one
  broken page should still yield 150 images — but the count is reported, up to five failures
  are named, and the exit status reflects it, because a silent partial render is
  indistinguishable from a complete one. A partially written image is deleted rather than
  left, since it looks like a success and fails only when something opens it. `-dpi` is
  validated before the WASM module compiles or a directory is created.
- `docs/adr/0005-rasterize-through-pdfium-on-wasm.md` — the borrow decision, its measured
  costs, and the three spike findings that changed the code rather than merely describing
  it.
- Tests. `render`: `Fit` across eight cases plus nine rejections, each also asserting the
  cap holds *after* rounding, and a hostile 200 × 200-inch `/MediaBox` that must be reduced
  into the cap while staying a usable image and keeping its aspect ratio. `render/pdfium`:
  pdfium's page counts cross-checked against pdfcpu's on six fixtures (1/1/1/285/4/151, so a
  future divergence between the two parsers fails by name), dimensions computed independently
  from known page sizes so a box-reading bug cannot make the test agree with itself, the
  1-based↔0-based conversion pinned at both ends, idempotent `Close` and use-after-close as
  an error rather than a crash inside WASM, ink-percentage bands with
  `disqualifiedScannedPages.pdf` as the blank negative control, and a Type3 page that
  extraction correctly yields nothing for but rendering must not. The load-bearing one is
  `TestPagesDoNotAliasEachOther`: go-pdfium's WASM adapter assigns `img.Pix` from wazero's
  `Memory().Read`, which returns a view into linear memory rather than a copy, and `Cleanup`
  frees the pdfium bitmap that view points at — so a returned image must own its pixels or
  one page's content appears in another's, which is far harder to recognize than an error.
  Confirmed load-bearing by reverting the copy and watching the test fail. `cmd/pdfspec`: 13 range
  cases and 11 rejections, byte-identical output between `-jobs 1` and `-jobs 4`, and a
  cross-check that every fixture `probe` routes to the OCR path does in fact rasterize.
  `TestAnnotationsFlag` builds its own one-page PDF with a Square annotation, because no
  file in `testdata/` or the corpus has an annotation with a visible appearance stream —
  which is why that flag was silently untested and silently wrong. See Fixed.

- `ocr` — the recognition interface and the routing rule, which is the whole of the package's
  policy. `Engine` is `Recognize(ctx, *image.RGBA, Options) (string, error)` plus `Close`;
  `Route(page, threshold)` decides a page by **text coverage** — the union of its block
  rectangles over its crop box — rather than by a character count, because a scanned page
  carrying a Bates number or a stamped folio has characters and no content. `DefaultThreshold`
  is 0.05. Two orders of magnitude separate the populations it divides (measured: 0.000 for a
  bare scan against 0.729 and 0.806 for pages of prose), so the exact default is not
  load-bearing; 0 is the off switch and 1 forces every page, which is how a caller expresses
  both extremes without a second flag meaning the same thing. A page already marked
  `Rasterized` is never routed, since asking a model to re-read its own output is not a
  stronger reading of the page.
- `ocr/doctags` — the parser for DocTags, the tag stream `granite-docling-258M` emits, into
  the same `doc.Page` the extractor produces. `Parse` for a whole document, `ParsePage` for
  one page, and a page break inside single-page input is an error rather than a silent second
  page. Both sinks and every downstream consumer therefore work on a document that is part
  scanned and part digital without knowing which pages were which — `doc.Page.Rasterized` is
  the only trace the model leaves. The vocabulary is pinned from `docling_core/types/doc/
  tokens.py` at commit `23fa247e`, and two of its properties are not guessable from the
  output: `<loc_>` is a **500-unit normalized grid** (`round(500*val)` clamped to `[0, 499]`,
  four tokens in x0,y0,x1,y1 order), and **DocTags Y runs top-down while PDF user space runs
  bottom-up**, so the parser owns the flip. A document parsed without it reads perfectly and
  has every rectangle mirrored, which no text comparison catches.
- `ocr/ipc` — the wire, **byte-identical to inferd's generation protocol v2** rather than
  merely similar: `[uvarint payload_len][1 byte frame_type][payload]` with no delimiter,
  `0x01` JSON and `0x02` BLOB, `Version uint32 = 1` in band, a 64 MiB cap checked *before*
  allocation, inferd's socket-resolution chain (`$XDG_RUNTIME_DIR/inferd/inferd.sock` →
  `$HOME/.inferd/run` → `/tmp/inferd`, `${TMPDIR}` on macOS, `\\.\pipe\inferd` on Windows),
  and its enumerated error codes. Byte compatibility is what "inferd is a drop-in
  replacement" actually means, so matching a JSON idiom would not have been enough. Framed
  binary rather than NDJSON because a 200 DPI page is ~12 MB of arbitrary octets and base64
  costs a third more plus a copy each way; images ride as interleaved RGB with no alpha,
  matching inferd's ADR 0016, because the daemon links no image codec. Reimplemented rather
  than imported — inferd's Go client has no server side, this repo needs one, and a shared
  module would couple two release cadences. Both a `Local` bridge (in-process, no socket) and
  an `Engine` (over the socket) satisfy `ocr.Engine`, and a test asserts the same handler
  yields identical text either way.
- `ocr/docd` — minimal scaffolding to run a model: `llama-server` as a child process, talked
  to over loopback HTTP. `/v1/chat/completions` with a PNG data URI rather than `/completion`
  with a media marker, because `get_media_marker()` in llama.cpp's `tools/server/
  server-common.cpp` **randomizes the marker per process** while `mtmd_default_marker()` still
  returns the documented `<__media__>` — a client using the documented one fails against a
  real server, and fails as a model-quality problem rather than a protocol error. PNG rather
  than JPEG because the payload is a page of text and JPEG's ringing lands on exactly the
  glyph edges the model reads. The executable is **located on `PATH` and never downloaded**;
  when it is absent the error carries the official per-platform install command. Model
  *weights* are data and go through llama.cpp's own `-hf` cache with its own integrity checks,
  deliberately not reimplemented — a second downloader means a second cache, a second checksum
  policy, and two places for a partially written GGUF to hide. `granite-docling-258M-GGUF` is
  the default: Apache-2.0 base weights, because an MIT repo cannot make a copyleft model its
  default. Binds 127.0.0.1 with no option to change it.
- `cmd/pdfspec ocr` — Markdown, recognizing only the pages that need it. `-o`, `-pages`,
  `-threshold`, `-force`, `-dry-run`, `-dpi`, `-max-tokens`, `-addr`, `-exe`, `-model`, `-ngl`,
  `-ctx`, `-frontmatter`, `-artifacts`. It is `md` plus a fallback, not a second pipeline: the
  extractor runs over the whole document, the router picks the pages whose content stream
  carries nothing, and only those cost a model — so a born-digital file produces byte-identical
  output to `md` and **loads nothing at all**, which is asserted. `-dry-run` reports the routing
  decision and exits, because the model is the expensive part and the decision is free ("which
  pages of this 1,000-page file need OCR" should not cost a 500 MB download). Generation is
  bounded at 8192 tokens per page by default, since the failure mode of a vision model on a
  dense page is not silence but a repetition loop emitting the same table row until something
  stops it; the bound turns it into a truncated page the parser still reads. Recognition is
  serial and deliberately has **no `-jobs` flag** — the host runs one slot and the wire allows
  one in-flight request per connection, so page-level fan-out would add queueing without
  throughput, and this is the one place the verb's structure diverges from `render`'s because
  the resource does. A page the model fails on keeps its extracted text and is not marked
  `Rasterized`, while a generation that errored *after* emitting DocTags is kept and parsed.
- `docs/adr/0006-recognize-scanned-pages-over-an-inferd-compatible-wire.md` — the four
  cross-cutting decisions (which pages, what the model returns, who runs it, how this repo
  talks to it), the measurements that shaped them, and the security posture stated rather than
  assumed.
- 7 MIT fixtures in `testdata/docling/` — docling-core's own DocTags documents and the
  Markdown docling renders them to, pinned by SHA-256 at commit `23fa247e`. The only
  non-PDF fixtures in the repo, and the reason the parser was finishable before a model was
  ever loaded: `ocr/doctags`'s tests run with no model, no GPU, and no daemon.
- Tests. `ocr`: `Route` across the routing table, the threshold's two extremes, and a check
  that the default separates the two measured populations. `ocr/doctags`: 9 pages and 567
  blocks across every `doc.Role` from upstream's widest fixture, the smallest document
  asserted block by block, upstream's no-coordinates degenerate case, one page as a model
  emits it, the Y-flip arithmetic, and a 25-case malformed-input matrix. `ocr/ipc`: framing,
  the wire version, the request JSON shape against inferd's field names, round-trips, image
  padding, the frame cap, malformed frames, blob/descriptor mismatch, cancellation, and
  `TestLocalMatchesIPC`, which is the claim the two paths are one interface. `cmd/pdfspec`: the
  whole verb pipeline against a fake `Engine` — which is what the interface is for, and why
  none of this needs a llama-server on CI — including that a scanned page comes out holding the
  model's text and marked `Rasterized`, that a failed page keeps what the file said, that a
  truncated generation is kept, byte-identical output to `md` on a born-digital document, and
  two assertions that catch coordinate defects no text comparison would: a recognized block
  above the page midpoint (a removed Y flip fails it) and an image width ≥ 500 at 72 DPI
  (passing the page box instead of the raster fails it).

### Fixed — 2026-08-04

- `-annots` drew form fields only, not annotations. go-pdfium's `RenderForm` calls
  `FPDF_FFLDraw`, which draws form *fields*; the appearance stream of every other subtype —
  a Square, Highlight, Stamp, or FreeText — is drawn under the separate `FPDF_ANNOT` render
  flag, which was never set. So `-annots` was a no-op for the common case: measured 640
  non-white pixels with the flag on and 640 with it off, on a page whose annotation covers
  10,000. Both switches are now set together. Found by writing the test the flag never had,
  and confirmed load-bearing by removing the fix and watching it fail.
- `render.Fit` returned nonsense instead of an error when the pixel cap was disabled and the
  DPI was absurd. Converting an out-of-range `float64` to `int` is implementation-defined —
  on amd64 it yields the minimum int, which the one-pixel floor then reported as 1 — so
  `-maxpixels 0 -dpi 1e18` on US Letter produced `8500000000000000000 × 1`. Dimensions are
  now bounded at 1e9 per axis *before* the conversion, which is a hundred times any real page
  at any real DPI; `MaxPixels` still bounds every sane case.
- The `Raster.Box` doc comment claimed the box carries "its origin, not zero". The pdfium
  adapter reports 0,0, because `GetPageSize` returns a size rather than a box. Only the
  extent is meaningful and the comment now says so, since a caller reading position out of
  `Min` would silently get the wrong answer.
- Three restatements of the same arithmetic said US Letter reduced to a 2,097,152-pixel cap
  is 1272.98 × 1647.40. It is 1272.999 × 1647.411, whose product is the cap exactly — which
  is the actual reason rounding up breaches it. Corrected in `render.go`, its test, ADR 0005,
  and above.

### Changed — 2026-08-04

- **`DESIGN.md` §9's "pure Go, no CGO" is a preference scoped to linkage, not a prohibition on
  languages.** The rule it was written to protect is that `go build` works for any target with
  no toolchain on the machine and the binary is one file, and a CGO dependency changes that for
  every user of the library. A *subprocess* does not, so what the binary talks to is a separate
  question and Rust or C++ on the far side of a process boundary is acceptable where it is the
  right tool — `ocr/docd` runs llama.cpp that way, and the OCR path degrades to "no model
  available" rather than failing to compile. ADR 0005 was amended to match: a linked C++
  rasterizer is still ruled out there, because the alternative is pure Go and costs only speed,
  while a subprocess elsewhere in the repo is not a contradiction of it.
- `docs/test.docs.md` records the docling fixtures, the DocTags vocabulary pin, and the
  coverage measurement behind the OCR router's default. The fixture count is now 37 across
  four upstreams, 15 of them MIT.
- A test helper in `cmd/pdfspec` named `context` became `around`. A function of that name
  shadows the standard library package for every file in the package, which the `ocr` verb's
  `"context"` import turned from latent into a build failure.

### Fixed — 2026-08-04

- **`ocr/ipc` did not compile for any 32-bit target.** `math.MaxUint32` is an untyped
  constant that does not fit an `int` on a 32-bit build, so the two guards written to
  *prevent* an overflow (`opt.MaxTokens < math.MaxUint32` in the client, `out <
  math.MaxUint32` in the server) were themselves compile errors there — `GOARCH=386 go
  vet ./...` failed on three sites. Both comparisons now widen to `uint64`. Found by
  running the 32-bit vet rather than by reading, and `GOARCH=386` is now part of the gate:
  every other package in the repo already built clean for it, so this was a regression the
  64-bit gate could not see.
- `ocr/ipc`'s server clamps a peer's `max_tokens` instead of converting it. `uint32` to
  `int` is lossy where `int` is 32 bits, and a peer's bound above 2³¹ arrived *negative* —
  which every handler here reads as "no bound", the exact opposite of a peer asking for a
  large one. It saturates now, so a ceiling stays a ceiling. This is the mirror of the
  client-side clamp added the same day, and `TestMaxTokensClamp` walks a bound through both
  conversions; its last case writes the frame directly rather than through `Recognize`,
  because the client clamps first and would otherwise hide the server's bug on precisely
  the platform where it bites.

### Added — 2026-08-04 (tests)

- `ocr/docd` has unit tests. The subprocess half still does not — that needs a real
  llama-server, a model download, and a GPU decision, which is an integration concern — but
  the SSE reader and the PNG encoder are pure, and they are where a page silently loses
  content. Seven cases on `stream`: delta concatenation, a stream that ends without
  `[DONE]` (an error, *with* the partial text), an in-band server error, non-`data:` frame
  lines, a malformed chunk skipped rather than fatal, a 200 KiB delta that would truncate
  under `bufio.Scanner`'s 64 KiB default, and a mid-stream read failure. Plus a
  `dataURI` round-trip through `png.Decode` — compared as resolved components, since PNG
  decoding an opaque image yields `NRGBA` where the source is `RGBA` and the `color.Color`
  interface values differ by dynamic type while every channel matches.

### Fixed — 2026-08-04

- **131 extracted images carried pre-blended samples in a format that declares samples
  non-premultiplied.** `/Matte` means the base image's colours were blended against the
  matte using the mask as the weight (§11.6.5.3), and `decodeSamples` was writing those
  samples straight into `color.NRGBA` — the *non*-premultiplied type — so every consumer
  that composited them applied alpha a second time. `Encode` now inverts the pre-blending
  with the spec's own `c = m + (c′ − m) / α`. Measured across the corpus: 2,283,562 of the
  2,297,495 partial-alpha samples change value, by up to 127 of 255. This is a fix rather
  than a refinement.
- The inversion runs *before* the conversion to RGB, because §11.6.5.3 requires it —
  "inversion of the pre-blending shall precede the colour conversion". Doing it afterwards
  would invert a blend in a space the blend was never performed in, which for a CMYK or Lab
  parent is different arithmetic with a different answer. That ordering is what places this
  in `image`'s decoder rather than in `render` as ADR 0005 assumed; ADR 0007 records the
  amendment, and 0004 and 0005 are annotated rather than edited.
- It also runs *after* the `/Decode` remap, which the same clause requires: the computation
  "shall use actual colour component values, with the effects of the **Filter** and
  **Decode** transformations already performed", and Table 144 puts `/Matte`'s numbers in
  that same post-`/Decode` domain. Every matted image in the corpus uses the default
  `/Decode`, where the remap is the identity and either order passes, so this needed a test
  built on an inverting `/Decode` to be pinned at all.
- At α = 0 the inverse divides by zero. The spec permits any in-range value there and notes
  the choice cannot affect output; the matte colour is chosen, being in range by
  construction and already what the sample holds. Not an edge case: **85.00% of the corpus's
  28,446,018 matted pixels have α = 0**, which is why the arbitrary value got a defensible
  choice rather than a convenient one. Components are clamped to 0..1 in the sample domain
  before conversion, since 1/α amplifies by up to ×255 at this corpus's smallest non-zero
  alpha and `clamp8` would flatten an over-range channel to full intensity, losing the
  ratios.

### Added — 2026-08-04

- `Image.Recoverable` reports whether the pre-blending can be inverted, and is true for any
  image that was never blended. False for a DCT base (never decoded, so there is nothing to
  invert — 5 of the corpus's 136), a DCT mask (no per-pixel alpha without importing the JPEG
  decode the base path avoids), a matte whose length disagrees with the component count, an
  `/Indexed` parent (the pre-blending applies to the palette, not to the index a sample
  carries), `Lab` (the matte is in real Lab units while the sample is normalized 0..1), and a
  matted mask whose dimensions differ from its parent's — which Table 143 forbids, and where
  the α that would divide is a resampled guess rather than the α that multiplied. All 136 of
  the corpus's matted masks match their parent, and the 4 differently-sized masks are all
  unmatted, so that last exclusion fires only on a malformed file. `images -list` names the
  count that stays blended with its reason, so the two populations in an output directory are
  distinguishable.
- Eight tests in `image`: the inversion at four alphas, the post-`/Decode` ordering, the
  α = 0 case, a non-black matte
  (`[0 0 0]` degenerates to a plain division and would hide a dropped `m` term — and every
  matte in the corpus is `[0 0 0]`), the six `Recoverable` exclusions, an unrecoverable
  image passing through unchanged, and an unmatted mask at partial alpha passing through
  unchanged. Input is built by running the *forward* formula, so the tests are about the
  inversion rather than about hand-computed constants that would encode the same mistake
  twice. `TestCorpusSoftMasks` now pins 131 recoverable / 5 blocked, keyed by codec pair.

### Added — 2026-08-05

- `README.md`. Every claim in it was run rather than transcribed, which caught two of its
  own: the library example called `doc.Extract` and `markdown.Render`, neither of which
  exists — `doc` is a pure model package with no exported functions — and the `probe` sample
  was a table copied from DESIGN.md rather than the per-file report the binary actually
  prints. Both are now output this build produces.

### Changed — 2026-08-05

- **Module path is `github.com/model-harness/pdftools`**, was `github.com/3rg0n/pdf-spec`.
  That path never resolved — the repository does not exist under that owner — so the module
  was not installable by anyone, `go get` included. Rewritten across 79 Go files, `go.mod`,
  and `docs/DESIGN.md`; `go.sum` is unchanged, since no dependency moved. The binary stays
  `pdfspec`.

### Fixed — 2026-08-05

- **`metrics.chars` counted bytes while `metrics.nonSpace` counted runes**, so the §1
  comparison figure published since Phase 1 was a byte total minus a rune count — a
  subtraction across two units, which no measurement produces. The arXiv paper's real
  non-space count is **39,049, not 39,257**; the 206-character gap is its multi-byte runes.
  `spaceRatio` had the same mismatch in its denominator and moves 13.94% → 14.01% on that
  file. Corrected wherever the figures were quoted: the README comparison table, the
  `extract_test.go` table and `nonSpace` doc, the corpus band (14.11%–17.81%), and the `md`
  baseline's space ratio. Nothing regressed and no assertion changed — every bar is a floor
  or a ceiling well clear of both figures, which is exactly why a wrong number survived
  this long. The agreement with pdftotext and pdfplumber tightens from 0.6% to 0.14%.
  Earlier CHANGELOG entries keep the figures they were written with.
- `escapeRate` divided by `len(s)` — the same defect one function down, found by looking for
  the class rather than the instance. Its denominator is runes now, which moves the arXiv
  paper's rate 0.34 → 0.35 and leaves ISO 32000-2 at 0.16. Two figures in
  `TestFixtureLargeDocumentStaysLinear`'s comment were stale independently of this and are
  now measured: 374,166 non-space characters (was 375,696) and 2,759 backslashes (was
  2,751), of which 2,609 still precede a C pointer asterisk.

### Fixed — 2026-08-04

- **Every aggregate corpus figure in the repo was measured over twelve files instead of
  eleven.** `corpusFiles` globbed `docs/*.pdf`, and `docs/` is a working directory as well as
  the corpus, so an arXiv paper dropped there to be read joined the baselines silently from
  Phase 3 onward. The image counts were the visible casualty — **239 images and 142 soft
  masks, not 245 and 143** — and the "largest is 6049×4090 DCT" claim quoted in three places
  was that paper's image, not the corpus's, whose largest is 1169×1394 raw DeviceRGB. Also
  corrected: 51 DCT (not 56), 184 Flate (not 185), 235 at 8 bpc (not 241). The `/Matte`
  figures are unaffected — 136 matted, 131 invertible, 5 blocked — so ADR 0007's analysis and
  the fix above stand as measured.
- `corpusFiles` now draws from an explicit eleven-name list rather than a directory glob, and
  `TestCorpusStructure` fails if its own table and that list disagree. A glob cannot tell a
  spec document from a stray download; the list can, and the drift above is what it costs
  when nothing does.
- ADR 0004's population table is annotated with the corrected counts rather than edited, and
  its decision is unchanged: `/SMask` is still the dominant case at 59% rather than 58%.

### Changed — 2026-08-04

- `docs/LightOnOCR-2601.14251v1.pdf` is gitignored and purged from history. It is a public
  arXiv preprint, but republishing a third party's paper is not this project's call to make,
  and the repository is now public. Tests that measure against it use `paperFile` and skip
  when it is absent, exactly as the corpus tests do — `testdata/` covers the untagged and OCR
  paths with committed fixtures, which is what makes that a skip rather than a hole. History
  rewrite: 44 MB packed to 5 MB.

### Changed — 2026-08-03

- `TestMDEmitsNoHeadingsYet` became `TestMDEmitsOutlineHeadings`, which is the inversion its
  own comment promised. ISO 32000-2 now converts to 981 ATX headings across five levels
  (`map[1:48 2:145 3:351 4:330 5:107]`).
- `tag.Elem.MCIDs []int` became `Content []MCRef`, where an `MCRef` carries its own page.
  This is a public-contract change to a core type and it is not cosmetic: an MCR's own `/Pg`
  is *authoritative over its element's*, and that is precisely the mechanism by which one
  paragraph continues across a page break. Flattening the references to a bare ID list
  discarded it. Measured: 5 of WTPDF's 2,035 references are MCRs and all 5 name a page other
  than their element's, likewise 5 of 329 in ISO/TS 32001 — and those 5-and-5 were exactly
  the unplaced text, which fell from 0.352% to 0.004% and from 1.222% to 0 when the join
  started reading them.
- `docs/DESIGN.md` §7: two corrections found by reading the OKF spec closely against the
  implementation. `generated.by` is `pdfspec/<version>`, not `pdfspec vX.Y.Z` — the latter does
  not parse as an actor under §7's convention and so cannot be classified. `sources[].author`
  is an actor field, and ISO has no actor form, so the field is dropped from the mapping rather
  than filled with a value a consumer would misclassify. The section also now records that
  cross-reference resolution is textual for now, with the annotation-based form as the target,
  and why WTPDF resolves none.
- `doc.Span` carries an `MCID`, and `extract` requires it to match before merging two spans
  into one. The `(page, MCID)` join has to be span-level because the paragraph heuristic
  merges a heading line with the body line after it when they share style and spacing, so a
  block-level join over-captures: it turned 12.0% of WTPDF's headings into
  heading-plus-definition, 3.7% of ISO/TS 32005's, and produced a 518-character "title" on the
  specification. Span-level took the title length p90 from 101 to 45 characters, and the
  longest title on ISO 32000-2 is now a real clause name.
- `filter`'s package doc states the passthrough as a contract rather than a design note: the
  `image` package reads the codec off the chain this package stopped at, and the stopping point
  is what makes a Flate-then-DCT stream arrive as a decompressed JPEG.
- `docs/DESIGN.md` §4 and §8: `bits` is documented with the consumer it actually has. It was
  described as the "foundation for CCITT and JBIG2," and neither codec appears anywhere in the
  12-file corpus — but sub-byte sample unpacking does, in 4 images at 1 bit per component, and
  that is a nearer consumer than either. §8's Phase 3 entry now records the three measurements
  that moved the design: no CCITT/JBIG2/JPX at all, `/SMask` at 143 of 245 with `/Matte` at
  136, and 7 images reachable only through a Form XObject.
- `golang.org/x/image` and `golang.org/x/text` are direct dependencies now rather than indirect
  — `image/ccitt` for the CCITT decoder, per the DESIGN.md §5 borrow table.

### Fixed — 2026-08-03

- `tag`: `ResolvePages` discarded the `/Pg` of a marked-content reference, keeping only its
  element's. See the `MCRef` entry above — the cross-page continuation this loses is the one
  case the tagged path exists to handle, and it read as unattributable text rather than as an
  error.
- `sectionize`: `truncate` could emit invalid UTF-8. The backoff after a byte-offset cut
  stripped continuation bytes but not a dangling lead byte, so a cut landing immediately after
  one left a partial rune — which both a YAML value and a filename reject. Found by a unit
  test rather than by a corpus run: no title in the corpus is long enough to truncate.
- `sink/markdown` wrote control bytes into its output. A C0 byte or DEL in extracted text
  passed through `escapeInto` unchanged, and there are three in this corpus — all in
  `PDF20_AN001-BPC.pdf`, whose `/ToUnicode` maps a code to U+0000, drawn between two
  sentences. Replaced with U+FFFD, which is what CommonMark §2.3 requires of a parser reading
  U+0000, in the verbatim contexts too: a code span cannot escape one, since `\x00` inside
  backticks renders as those four characters, so replacement is the only substitution that
  keeps the output parseable and adds no backslash. YAML values were already safe —
  `yamlString` escapes them as `\xNN` — and filenames were, since `kebab` drops them. The
  consequence was not cosmetic: a NUL terminates a path in every C API downstream, and a
  consumer reading a bundle byte-for-byte got a value it could not round-trip. Pinned by
  `TestMDEmitsNoControlBytes` over every file in the corpus, which is where it had to go — a
  test built from hand-written spans would never contain the byte.
- `bits`: the aligned-byte fast path returned the raw byte in both bit orders, but LSB order
  consumes a byte's bits in the opposite sequence, so composing them most-significant-first
  reverses it — `0x7F` read as `0xFE`. The fast path is now MSB-only. Caught by a test that
  pins the fast and slow paths against each other rather than assuming they agree, which is the
  only way this surfaces: the wrong answer is a valid byte value and every read succeeds.
- `image`: CCITT polarity was wired inverted, on the assumption that `x/image/ccitt` and PDF
  used opposite conventions. They agree — `Invert=false` means black is the 0 byte, and
  `/BlackIs1=false` (§7.4.6) means 0 bits are black — so the negation decoded every fax image
  as a photographic negative. A test that only checked the decode succeeded would have passed.
- `image`: a truncated CCITT stream lost its `/BlackIs1` polarity. `x/image/ccitt` applies
  `Invert` only after its row loop completes, so the rows this package deliberately keeps from
  a damaged stream came back uninverted — a photographic negative reached through the error
  path, which the polarity test could not see because it decodes only clean fixtures. Found by
  reading the decoder's source to verify the polarity mapping rather than by trusting its
  documentation, and pinned by `TestCCITTTruncatedStreamKeepsPolarity`, which was confirmed to
  fail without the fix.
- `image`: the ICCBased branch indexed `cs[1]` before checking the array's length, so a
  truncated `[/ICCBased]` panicked. Self-caught on review, pinned by a test.
- `image`: `decodeCCITT`'s comment said "/Width wins" while the code decodes at `/Columns` and
  crops back to `/Width`. The code was right — a CCITT stream is coded at `/Columns` and
  decoding at any other width misreads every row's codes — but the comment invited a future
  change that would break it.
- `image`: an aligned-loop exit assigned a loop variable whose value was never read
  (`staticcheck` SA4006), now a labeled `break`.
- `sink/markdown` wrapped every scanned document in backticks. A span was emitted as code on
  `Style.Mono` alone, and an OCR text layer is fixed-pitch by declaration — Tesseract's
  `GlyphLessFont` sets the descriptor's `FixedPitch` flag — so a page whose only text is an
  invisible OCR layer converted to one long code span. The flag is a true statement about a
  font nobody ever sees rather than a typographic claim about the text, so `Style.Hidden` now
  suppresses the code mark. `Mono` remains sufficient on its own for visible text.
  Deliberately not scoped to a font name: `PDF_XChange-OCRed.pdf` shows the same layer from a
  different engine, and the discriminator that generalizes is the rendering mode, not the
  font. Measured before changing it — `mono && !hidden` is 0 across every fixture, and a
  285-page C API manual full of real code listings has no monospaced span at all, so the
  suppression costs no genuine code span anywhere available to test. No metric in the suite
  could have caught this: character counts, space ratios, and word-length distributions are
  identical either way, which is why it took an outside statement of the right answer. Pinned
  by `TestFixtureOCRMatchesGroundTruth` against upstream's `ocr-ed.txt`, confirmed to fail
  without the fix.
- `extract` inserted a space at every wrapped-line join, which broke words in every script
  written without inter-word spaces. The rule is right for Latin — a line break is the only
  thing marking the boundary there — and wrong for CJK, where a line fills and wraps
  wherever it runs out of measure, so the break carries no information and a space at the
  join is a claim the page does not make. `appendLine` now consults the characters actually
  adjacent to the join: a space is added unless both sides are Han, kana (full or half
  width), or CJK punctuation. Hangul is deliberately excluded despite being CJK by every
  other classification — modern Korean *is* written with spaces between words, so a Korean
  line wrap is an ordinary boundary and suppressing the space there would run two words
  together, which is this same defect in the other direction. The criterion is the script's
  use of spaces, not its residence in the CJK blocks; Thai and Khmer qualify and are absent
  only because no fixture exercises them.
  Decided per join rather than per document on purpose, because a document is
  routinely mixed — the fixture sets its prose in Chinese and its rating codes and amounts
  in Latin — and a line wrapping from one script into the other is a real boundary.
  Measured on `chinese-tables.pdf`, a Chinese bond prospectus: the company name
  中诚信国际信用评级有限责任公司 wraps across three lines and was emitted with two spaces in
  it, and Han-to-Han spaces fell from 22 to 7. The 7 that remain are correct — a clause
  number before its title, and table header cells. Found because the fixture corpus has CJK
  and the `docs/` corpus does not: eleven English-language documents cannot exercise this,
  and no Latin measurement in the suite moved when it was fixed. Pinned at both levels, by
  `TestWrapNeedsSpace` on the predicate (including the mixed-script joins the fixture cannot
  express) and by `TestFixtureCJKNeedsNoSpaceInference` naming the four words the wraps fall
  inside — asserted as words rather than as a ratio, since a ratio cannot tell a broken word
  from a table column, which is the entire question.
- `cmd/pdfspec probe` scanned only page-level `/Resources`, so it undercounted fonts and
  images and — because probe's entire output is a routing decision — answered the question
  wrongly for the files it missed. A Form XObject carries its own resources, and one with no
  `/Resources` inherits the invoking dictionary's (§8.10.1); `extract` and `image` both
  recurse, and probe did not. `text-find-ligatures.pdf` reported `Type1` while the font
  actually setting its ligature sits two forms deep, and `test_delete_image.pdf` reported 0
  images with its only image in the same position, which put probe and the `images` verb into
  disagreement about one file. The scan now recurses to the same `maxFormDepth = 8` as the
  other two packages, with a `seen` set on indirect references that both stops cycles and
  deduplicates — one XObject drawn on 1,023 pages is one image, which is the rule
  `image.Reader` applies and is what makes probe's ISO 32000-2 count land on the same 224 the
  images tests assert. Per-page columns still report each page's own dictionary, since
  summing nested forms into them would make the column mean something different per row.
  probe had no test coverage at all before this; `TestFixtureRouting` now asserts path, page
  count, and image count on 17 files and cross-checks every image count against
  `image.Reader`, and both halves were confirmed to fail without the recursion.

### Changed — 2026-08-02

- `docs/DESIGN.md` §3: corrected the claim that a clause is "one contiguous subtree."
  Measuring ISO 32000-2 found 7 `Sect` elements against 981 headings, with one `Part`
  holding 13,442 flat children, so `sectionize` derives boundaries from the heading
  sequence rather than from container nesting. Recorded the measurement in the document and
  pinned it with `TestSectionShapeOnTarget`.
- `docs/DESIGN.md` §3: corrected two claims from an earlier byte-level estimate — `/Marked`
  is not universal (two spec documents omit it while carrying substantive structure trees,
  so the tagged path must never gate on it), and page counts from raw-byte `/Type /Page`
  matching are inflated because object streams retain superseded page objects.
- Bumped `golang.org/x/text` to v0.39.0 and `golang.org/x/image` to v0.43.0, clearing two
  advisories reachable through PDF parsing. GO-2026-5970 is an infinite loop on invalid
  input, and invalid input is this toolkit's normal case.

- `content`: operator names are interned against ISO 32000-2 Annex A, so a recognized
  operator no longer allocates a string for its name. Cut 2,200 allocations per benchmark
  iteration from both the lexer and the scanner and took lexer throughput from 214 MB/s to
  236 MB/s. What remains is boxing operand scalars into `objects.Object` plus one buffer per
  literal string and name, which is structural; `Op` and `Scanner.operands` now say so
  instead of claiming steady-state scanning does not allocate.

### Fixed — 2026-08-02

- `filter`: LZW decoded to silent garbage past 511 codes. The code-width thresholds
  conflated two offsets that stack. A decoder cannot define a table entry until it has read
  one code past the one that produced it, so it always trails the encoder by exactly one
  entry — that is an unconditional `+1`. PDF's default `EarlyChange=1` then widens one code
  earlier still, which is a *second* `+1`, not a substitute for the first. Reading one code
  at the wrong width desynchronizes everything after it, and because the stream stays
  syntactically valid the symptom was corrupt text with no error. Found by writing an
  independent PDF-flavored encoder for the tests: `compress/lzw` implements only the
  `EarlyChange=0` behavior, so round-tripping through this package's own decoder would have
  passed with both sides wrong the same way.
- `filter`: the predictor's geometry guard bounded each factor but not their product, so
  `/Colors 32`, `/BitsPerComponent 32`, and `/Columns` at the permitted 2^24 all passed
  individually while multiplying out to a 2 GiB row — four bytes of input bought three
  multi-gigabyte allocations and a memory-exhaustion abort. Now bounds the product against
  `maxDecoded`, computed in int64 so the check cannot itself overflow on a 32-bit build,
  where `int` is 32 bits and that product wraps to zero. Found in review.
- `filter`: ASCII85 wrapped groups above 2^32-1 instead of rejecting them. Five base-85
  digits can name a value the encoding cannot represent — `uuuuu` is 4,437,053,124, which
  wrapped to 142,085,828 and emitted four plausible bytes with no error. Accumulated in
  uint64 and rejected; the largest legal group, `s8W-!`, still decodes. Found in review.
- `objects/pdfcpu`: indirect references never resolved. pdfcpu's `Dereference` type-asserts
  `types.IndirectRef` by value while `NewIndirectRef` returns a pointer, so every lookup
  fell through its type switch and returned the reference unchanged. Everything reachable as
  a direct object worked, which made the failure look like an untagged corpus rather than a
  bug. Dereferencing the pointer took ISO 32000-2 from `path ocr` at 55 ms to `path tagged`
  at 5 ms.
- `cmd/pdfspec probe`: UTF-16BE `/Lang` values printed as mojibake. Consolidated text-string
  decoding into `objects.DecodeTextString` and removed the duplicate decoder in `tag`.
- `tag`: structure-tree construction was unbounded in depth. The visited-reference check
  stops a tree looping back on itself but not a long chain of distinct references, so a
  crafted deeply-nested document could exhaust the stack on untrusted input. Bounded at 512
  (corpus maximum is 13), which bounds `Walk` and `Stats` transitively. Found in review.
- `objects/pdfcpu`: `Decode` panicked with a nil dereference on an unfiltered stream. pdfcpu
  distinguishes a nil filter pipeline from an empty non-nil one — nil returns the raw bytes,
  while an empty slice reaches the decode loop, whose body never runs, and pdfcpu then reads
  from a nil reader. The adapter built an empty slice for a stream with no `/Filter`, which is
  exactly what a form XObject written without compression is. Page 269 of ISO 32000-2 has one,
  so this aborted extraction of the target document. Found by running the corpus, not by a
  unit test: no hand-written stream in `extract`'s tests is uncompressed.
- `extract`: a space was inferred between every pair of glyphs on five corpus documents —
  56% whitespace, longest "word" of two characters, the exact inverse of the §1 baselines'
  failure. The pen advance scaled displacements by the CTM alone, but a displacement passes
  through the text matrix *and* the CTM, and a producer setting 12-point type as a `/F1 1 Tf`
  with a `Tm` scaling by 12 — which many do — puts the whole font size in `Tm`. Every advance
  was then undercounted by that factor, the shortfall accumulated, and it read as an
  unexplained gap. Fixed by composing `Tm` with the CTM. Non-space character counts were
  unchanged afterwards, which is what confirms it removed only the spurious spaces.

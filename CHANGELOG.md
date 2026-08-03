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

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
- `doc.Span` carries an `MCID`, and `extract` requires it to match before merging two spans
  into one. The `(page, MCID)` join has to be span-level because the paragraph heuristic
  merges a heading line with the body line after it when they share style and spacing, so a
  block-level join over-captures: it turned 12.0% of WTPDF's headings into
  heading-plus-definition, 3.7% of ISO/TS 32005's, and produced a 518-character "title" on the
  specification. Span-level took the title length p90 from 101 to 45 characters, and the
  longest title on ISO 32000-2 is now a real clause name.

### Fixed — 2026-08-03

- `tag`: `ResolvePages` discarded the `/Pg` of a marked-content reference, keeping only its
  element's. See the `MCRef` entry above — the cross-page continuation this loses is the one
  case the tagged path exists to handle, and it read as unattributable text rather than as an
  error.
- `sectionize`: `truncate` could emit invalid UTF-8. The backoff after a byte-offset cut
  stripped continuation bytes but not a dangling lead byte, so a cut landing immediately after
  one left a partial rune — which both a YAML value and a filename reject. Found by a unit
  test rather than by a corpus run: no title in the corpus is long enough to truncate.

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

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

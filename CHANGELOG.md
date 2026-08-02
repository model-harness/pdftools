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

### Fixed — 2026-08-02

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

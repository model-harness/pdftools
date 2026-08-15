# Changelog

All notable changes to this project are documented here, following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed — 2026-08-15

- **Three contents entries read `2 Scope1`, because the span holding the space was one the
  structure tree could not name.** The extractor gets this right: a dotted leader is a run of its
  own whose text ends in a space, and `needSpace` infers one across the gap besides. What lost it
  was the tagged rebuild — `newIndex` indexes only spans with a non-negative MCID, so an artifact
  cannot be claimed by any element, and decoration is exactly what producers leave unmarked. The
  entry's title and its page number were then concatenated across a 395.57pt gap.
  - **The population is 6 of 14538, and the band the threshold sits in is empty for 4.4×.**
    Measured over every same-line adjacent pair in a sectionized block that joins with no space on
    either side, excluding the spans `sectionize` itself fabricates: the gap as a multiple of the
    test runs p50 0.007, p90 0.073, p99 0.355, then a dense cluster from 0.404 to 0.435 — 69 pairs,
    every one an ISO 32000-2 mathematical variable meeting punctuation — and nothing until 1.918.
    Above that lie exactly six: 1.918, 2.515 and 2.596 in ISO 32000-2's L\*a\*b\* definition, and
    99.873, 152.694 and 219.760 in `PDF-Declarations.pdf`'s contents list. A subscript touches its
    bracket; a dropped span leaves most of a line.
  - **Two thresholds, because `extract` uses two.** The same-line test takes the larger of the
    pair's type sizes, matching `run.go:462`'s `maxf(sy, prev.height)`; the space test takes the
    *following* span's advance, matching `run.go:448`, where it is read per glyph and never
    maximised. Collapsing both onto one reading would be a second opinion about a question the
    extractor has already answered twice.
  - **The advance is estimated at half an em, and the first version's failure to estimate it at
    all was a 4× error that left a defect of this very class in the output.** `doc.Style` carries
    `Size` and no advance, so the first version measured against the em directly — which is roughly
    four times too wide. It left ISO 32000-2's `× (𝑥 −4 29)` joined at a 2.313pt gap on an 8.04pt
    span, scoring it 0.959 of the threshold: the same defect as the three contents entries, on the
    same output line as one of the two joins the rule did fix. Half an em is not a guess — it is
    the fallback `extract` itself uses when a font reports no space glyph (`run.go:449`), so the
    one quantity this rule cannot read already had a documented answer. `TestNarrowGapAtARealFormulaIsASpace`
    and `TestWidestJoinedSubscriptGapStaysJoined` now pin both edges of the empty band, because no
    other geometry on disk separates the two readings.
  - **A rune test, not a byte scan.** A span ending in U+00A0 or U+2002 already has its boundary,
    and `strings.HasSuffix(s, " ")` cannot see one. This is the test `extract`'s own
    `endsWithSpace` makes, for the same reason.
  - **A zero type size decides both questions the wrong way at once**, since at `Size == 0` both
    thresholds are 0 — every positive gap becomes a space and every unequal baseline another line.
    `doc.Style.Size` is the composed matrix's `sy` with nothing clamping it (`extract/run.go:635`),
    so a `Tf` of 0 produces one; the corpus has 0 of the 96569 spans `extract` emits, so this is a
    guard and not a case, and it is the guard `extract` already makes on the same quantity at
    `run.go:449`. Review found it. Adding it also entangled two fixtures:
    `TestGapSpaceIgnoresAFabricatedSpan` had an unsized span, so the new guard declined before the
    identifier was read, and the test would have gone back to passing for the wrong reason.
  - **A finished outline holds 119 zero-size spans and every one is `sectionize`'s own**, which is
    what a naive count of that quantity reaches first — 113 are the newlines `breakAtBaselines` and
    `gather` insert and 6 are this rule's spaces, one per firing pair. The two numbers answer
    different questions and the layer has to be named for either to mean anything, which is how the
    review's `118 vs 0` was resolved and what led to the 4× finding above. The count moved with the
    fix: it was 118 while the em proxy left the sixth join unmade, so a figure written before a
    threshold changes is a figure about the old threshold.
  - **38 mutations applied, 37 killed, every kill named to a test.** The survivor is the call
    order against `breakAtBaselines`, which is an equivalent mutant: the two predicates are
    exclusive per pair, so neither can change the other's answer. Dropping the fabricated-span
    guard first looked equivalent too — it survives from `Tagged`, because every span this package
    fabricates holds whitespace — and is not: the same pair answers false with it and true without,
    since a zero box against a span at X0 400 reads as a 400pt gap. That is a fact about today's
    insertions and not about the rule, so `TestGapSpaceIgnoresAFabricatedSpan` calls `gapSpace`
    directly and kills it.
  - **Two harness bugs found, both of which had been scoring false kills.** Three mutants that
    drop a `unicode.IsSpace` call leave `last`/`first` unused, which is a compile error and
    scored as a kill no test produced; all three die once made valid. And `mut7.py`'s guard
    anchor became ambiguous when `gapSpace` introduced a byte-identical line earlier in the file,
    so `replace(…, 1)` had been mutating the wrong function — `drop-cur-mcid-guard` looked like a
    coverage regression and was an aliased anchor. The new harness had the same bug on the same
    line, which is how the count reached 33: an anchor that matches twice resolves by file order
    rather than intent, and unlike a miss it never announces itself. Both harnesses now report
    `NO-OP` when a replacement leaves the file unchanged, which is the failure that reads as
    `SURVIVED`.
  - **Five lines change across the 12 outputs, all five are corrections, and they carry six
    joins.** Verified against a baseline generated with the rule switched off rather than against
    an earlier run of it, which is what made the sixth join visible: three contents entries, and
    two lines of ISO 32000-2's L\*a\*b\* definition where an operator had been welded to its
    operand — `𝑥 ≥6 29` → `𝑥 ≥ 6 29`, and `=108 841 × (𝑥 −4 29)` → `= 108 841 × (𝑥 − 4 29)`,
    which is two joins on one line. Both sinks carry the fix (`okf`'s `table-of-contents.md` reads
    `- 2 Scope 1`), and the `/Alt` on each contents `Link` independently confirms the three
    against the page.

### Investigated — 2026-08-15

- **The 12 remaining dash-space pairs are what the page draws, and the item is closed as a
  non-defect.** `48- byte`, `32- byte`, `2- byte` and `2- unit` in ISO 32000-2 read like joins the
  wrap rule missed, and they are not: every one of the 12 is a space the producer set as a glyph,
  so `dashHoldsTheWord` — which fires only at a line break, on an *inferred* space — never sees
  them. Established by marking each of `place`'s three insertion sites with a distinct sentinel
  byte and regenerating, which attributed all 12 to the glyph stream and none to `writeSpace` or
  the wrap rule; instrumenting the wrap path directly found exactly one dash wrap whose space is
  drawn (`XA/11– recommended`), and it is correct.
  - **The pen settles the four ambiguous ones.** Each of those space glyphs is drawn with a
    *negative* gap against the dash — as much as −1.85pt of overlap — so the coordinates alone do
    not say whether the page shows a gap. The advance does: the glyph after the space lands where
    the space's own advance puts the pen (`12700` against the dash's `12476`, the same 224-unit
    displacement in all four), so the space is on the page and joining it would be the extractor
    overruling the document.
  - **Six are correct suspended hyphens** (`four- or five-element`, `human- or machine-readable`,
    `both forward- and backward-compatible`, `both 2- and 4-byte`, `Mixed one- and
    two-dimensional`, `at both the document- and object-level`), one is `%PDF-` naming the header
    prefix, and three are the known math-layout limit.
  - **The count had moved on its own, 17 → 12**, because the intra-line gap rule reached three of
    them — a backlog figure measured before an unrelated fix describes the old pipeline. A drawn
    space and an inferred one are identical in the output and no assertion on text separates them;
    sentinels at the insertion sites separate them in one regeneration.

### Fixed — 2026-08-14

- **Eleven code listings were dropped entirely, and their 99 lines escaped as prose.** A `Code`
  element that holds no marked content of its own — its listing is one `P` per line — emitted no
  spans, so `doc.Block.IsEmpty` discarded it and `gather` had already detached every one of those
  paragraphs as a block in its own right: `declared=11 kidP=99 -> codeblocks=0 empty=0 chars=0`.
  The listings came out as ordinary paragraphs with their `/` and `<<` backslash-escaped, and
  nothing in the output said a fence had been lost.
  - **The census is what named the defect, and it contradicted the logged note.** There are 18
    `Code` elements on disk in 3 files. The 7 in `PDF-Declarations.pdf` and
    `PDF20_AN003-ObjectMetadataLocations.pdf` are `kids=0 content=N` — they carry their own
    content and already fenced correctly, which is why a fence count alone looked healthy. All
    11 in `Well-Tagged-PDF-WTPDF-1.0.pdf` are `kids=N content=0`, holding 99 `P` between them.
    The note blamed ISO/TS 32004 and 32003 and said fencing needed `doc.Block.Role`; both halves
    were wrong. The role plumbing was already complete end to end — `doc.RoleCode`,
    `sectionize.go`'s `tag.RoleCode` mapping, and `sink/markdown`'s fence writer — and those two
    files declare **no** `Code` at all. Their collapsing ASN.1 listings are the untagged-path
    half of the same defect and are still open.
  - **The one-line fix is the defect wearing the fix's clothes.** Adding `RoleCode` to
    `wrapsText` gets the fence, and because `doc.Block.writeText` concatenates spans with no
    separator it runs all 99 lines together — the collapse a fence exists to prevent. So
    `linesText` is a *second* predicate rather than a wider `wrapsText`: a cell holding several
    `P` is one run of prose the producer broke across lines and joining it with nothing is
    right, which 752 cells on disk do; a listing's `P` *is* a line and the break between two of
    them is content with no glyph anywhere on the page.
  - **The break is written per absorbed block, before recursing, and only after there is text.**
    Three separate conditions, each with its own reachable failure: 10 of the 109 descendants of
    the corpus's `Code` elements are `Span`, so breaking on any kid splits a styled line in two;
    writing it after the recursion puts the break after the last line instead of between two;
    and dropping the `len(*spans) > 0` guard opens every listing with a blank line.
  - **Reconciled in both directions, and not one character of text changed.** One of 12 files
    moved. `U+000A -66`, `U+0060 +66`, `U+005C -103`, nothing else: the 66 newlines are the 11
    fences' own lines and the 66 backticks are their markers (11 × 3 × 2), and the 103 escapes
    vanished because fence content is verbatim. 49 previously-escaped lines lost their
    backslashes and **zero** lines gained one; 76 lines held a backslash before and 9 do now.
    Line count 1934 → 1868, and with all whitespace, backslashes and backticks removed the two
    outputs are byte-identical at 104249 characters each — the three excluded classes being
    exactly the three runes that moved.
  - **The break span is `MCID: -1`, and both sinks were checked.** `newIndex` indexes only spans
    with a non-negative MCID, so a fabricated one cannot be claimed twice or reach the `unplaced`
    recovery pass. `sink/okf` sends clause bodies through `markdown.WriteBlocks`, so it gains the
    fences too — 22 markers across 7 concept files — and the two paths where a raw newline could
    have entered a YAML scalar, `oneLine` on titles and `collapse` in `describe`, both fold it:
    0 malformed frontmatter lines in the generated tree.
  - **The corpus block count moved −88 and reconciles exactly.** WTPDF goes from 938 blocks to
    850: 99 paragraphs that each used to stand beside a discarded empty `Code` block are now the
    contents of 11, and 99 − 11 = 88. `TestSectionizeCorpus`'s floor moved to 840 for that file,
    with the arithmetic recorded beside it — a floor is not evidence of a merge, and
    `TestSectionizeLosesNoText` and `TestOutlineConservesCharacters` both hold unchanged, which
    is.
  - **Eight mutations applied, eight killed, and one of them was killed only by the corpus.**
    Dropping the `blockRole` guard on the break failed `TestSectionizeCorpus` and nothing else —
    a guard that would be covered by nothing on a clone without the sponsored PDFs.
    `TestCodeSpansDoNotSplitALine` is the `P > Span` shape those 10 descendants are in, and it
    kills the mutation from the `sectionize` package alone.
  - Corrects a measurement in the 2026-08-13 entry below: "0 fences in the corpus output at
    all" was already wrong when written — the 7 self-contained `Code` elements produced 14 fence
    markers over 7 lines. There are now 36 markers over 106 lines, and 0 of those lines end in
    whitespace, so the conclusion that trailing-whitespace-inside-a-fence is unobservable here
    still holds on a population that is no longer zero.

- **23598 spaces were inferred into gaps the page had already spaced.** `place` infers a space
  wherever a glyph starts further along than the previous one's advance accounts for, and that
  test is geometric: it cannot see that the producer already drew a space into the same gap.
  Justified text is where it happens — the line sets its space glyph and then stretches the word
  gap around it, so the pen ends up more than a nominal space width from the next glyph and the
  rule fires a second time on a boundary that is already spaced. 25892 of the corpus's 48530
  inferred spaces followed text already ending in whitespace, and 12836 of those also *preceded*
  a space glyph, where the inserted character was the third. 10922 interior runs of two or more
  spaces reached the Markdown output because of it — 9719 of exactly two and 1203 of three or
  more, across all 12 documents. A run counts as interior when a non-whitespace character stands
  on both sides of it, which is the definition the 2874 below is measured against too; bounding
  the run with `[^ \n]` instead admits the runs a tab sits next to and gives 10928 and 2879.
  - **Only the character is suppressed, never the cut.** A gap is two things at once: a place a
    space may belong, and a position a table's vertical rule may run through. `splitAtRules` can
    divide a fragment only at a recorded cut, so a header cell whose label ends in a space still
    has to divide from the cell after it. Suppressing the cut alongside the space put
    `reference/table.pdf`'s cells back into one fragment — pinned by a test that asserts on the
    span list, since `Text()` cannot see it.
  - **The predicate is a rune test, not a byte test.** `Well-Tagged-PDF-WTPDF-1.0.pdf` draws
    U+2002 EN SPACE as its clause-number separator, and a byte compared against `' '` reads that
    rune's trailing `0x82` as an ordinary character and doubles it. `endsInSpace` is the
    byte-slice twin of the existing `endsWithSpace` and exists for cost: `place` runs once per
    glyph, 2.76M times over this corpus, and `string(f.text)` would copy a whole fragment per
    call to read its last rune.
  - **Reconciled in both directions against a pre-fix baseline.** The only runes that changed in
    any of the 12 files are `U+0020` at `-23598` and `U+005C` at `+4`; all 36460 line counts are
    unchanged; and collapsing every whitespace run in both makes all 12 files byte-identical, so
    the change shortens whitespace runs and does nothing else. The 4 backslashes are a
    consequence rather than a defect: three cells held `" -"` and one `" #"`, so the block-start
    escape did not fire on byte 0, and with the leading space gone it does. Verified against
    pandoc 3.9 `-f gfm` that `| \- | x |` renders `<td>-</td>`, identical to the unescaped cell.
  - **2874 interior runs remain and every one is a space the page draws** — code-listing
    alignment inside `` ` `` spans, `© ISO 2020`, `Note 1 to entry:` — so what is left is content
    rather than inference.
  - **Nine mutations applied, and the first pass killed eight.** The survivor was the
    inter-fragment write site, where a style change starts a new fragment and the space is
    carried by the one that follows. That site is not obscure: a normative reference sets its
    title in italic, so `ISO/TC 171, *Document management*` changes font at a comma the page has
    already spaced. It is reached 15902 times on the corpus and 872 Markdown lines across 9 files
    change when it is mutated — a branch the corpus exercises constantly that no test could see.
    Covered now, and all nine are killed.
  - Not fixed, and still logged: the 12 `Pdf MacIntegrityInfo` splits in ISO/TS 32004. Their gap
    ratios are 0.3023 and 0.3105 against a `SpaceFrac` of 0.30, below the distribution's 1st
    percentile of 0.327, inside a band holding 1613 inferred spaces that are correct. An
    identifier-shaped discriminator finds 176 such gaps corpus-wide and all 176 are real spaces —
    and it does not match this case anyway, since `"Pdf"` has no digit, hyphen, or internal
    capital. Neither a threshold nor that discriminator separates the twelve.

- **A 25-line XML sample came out as one 892-character fenced line.** `PDF-Declarations.pdf`
  declares a `Code` element holding its 25 listing lines as 25 MCIDs under no `P` at all, so the
  breaks survive nowhere but the fact that consecutive spans were drawn at descending baselines.
  `sectionize` now restores them geometrically for a producer-declared lines role, recovering all
  25 lost breaks across the corpus (`underBroken=2 -> 0`).
  - **The logged note's premise was false, and measuring is what showed it.** The item was
    recorded as the *untagged* half of the collapse, with `Style.Mono` as the signal and
    `extract`'s `appendLine` as the site. All three were wrong for the population that reaches
    output: every mono-bearing file in the corpus is tagged (11 of 12; the one untagged file has
    no mono at all), so `inferRoles` — and therefore all of `layout` — never runs on any of them.
    Mono and `Code` turn out to be disjoint signals here: WTPDF's 11 listings are declared, not
    monospaced.
  - **The extract-side fix was abandoned because a real renderer refuted it.** Verified with
    pandoc 3.9 `-f gfm` that a newline inside an inline `` ` `` span renders as a space, so the
    break would have been unobservable in 3886 of 4246 mono-mono wraps — and *destructive* in the
    other 360, where the next line opens with `>>` and the span becomes nested `<blockquote>`s
    with literal backticks. The newline can only be written where the block is fenced, which puts
    the rule in `sectionize` and confines it to `RoleCode`.
  - **Geometric in a declaration-driven package, and deliberately narrow.** The rule is not a
    heuristic about what a block is — `RoleCode` is the producer's own statement — only about
    where its lines end, and a listing is the one role for which that answer must survive to the
    sink. It is idempotent with `gather`'s paragraph-absorption break rather than layered on it:
    the two agree on 5 of the corpus's 6 multi-line `Code` blocks, and this one is strictly wider,
    supplying PDF-Declarations' 24 breaks plus one WTPDF break `gather` cannot write because both
    sides of it are spans inside a single `P`.
  - **The threshold is the extractor's own line test, not a second opinion about it.** `LineFrac`
    of the *larger* of the two type sizes, matching `run.go`'s `maxf(sy, prev.height)`, and the
    magnitude of the step rather than its sign — the corpus has one listing that crosses a page
    and rises 681pt, which a signed comparison would read as one long line. 49 of 179 adjacent
    pairs inside a `Code` block change size and none land in the window where the four readings of
    "the type size" differ, so fixtures rather than the corpus hold that choice.
  - **Eighteen mutations applied and all eighteen killed from the package alone**, with no
    corpus-only kills. Four needed fixtures the corpus cannot supply: the upward page-crossing
    step, and the three wrong readings of the type size, which one subscript fixture settles
    because each breaks the same line in a different place. Two earlier "kills" were invalid
    mutants — dropping `math.Abs` or the tolerance leaves an import unused, so the compile error
    was being counted as a test failure.
  - **Two output files change and both diffs are pure newline additions**, nothing else, against a
    pre-fix baseline of all 12 files.
  - Not fixed, and now logged: PDF-Declarations encodes its listing indentation as x-position
    only, so the restored lines are flush-left. That indentation was equally lost before this
    change, when the whole listing was one line.

### Fixed — 2026-08-13

- **12342 lines ended in whitespace, and Markdown gives a trailing space a meaning the page
  never had.** A span's text ends in a space wherever the producer drew one, and the last span
  of a line carried that space to the line's end: 516 lines across 8 documents ended in two or
  more, which CommonMark §6.7 makes a hard line break, so the output rendered a `<br>` no
  document asked for. The other 11826 ended in exactly one, which renders identically but makes
  every conversion diff-hostile and trips `MD009 no-trailing-spaces` on any linted consumer.
  12947 whitespace characters in all, over 11 of the 12 corpus documents.
  - **Held back, not trimmed at each line close, because there are eighteen places that end a
    line and one that writes bytes.** `writer.str` buffers trailing space and tab in `pend` and
    flushes it only when a non-whitespace byte follows on the same line; a newline discards it.
    So a space between two words survives, a space before a newline is never written, and the
    rule is indifferent to *which* caller closes the line — including the four that write their
    own `"\n"` inside a longer string. `write` is now the sole path to the underlying writer.
  - **The alphabet is space and tab**, matching MD009 and CommonMark §2.1. A no-break space is
    content a producer chose and must survive; 0 lines on disk end in one.
  - **Whether any of it was content was measured, not assumed.** A trailing space inside a
    fenced code block is preserved verbatim by a renderer, so trimming it changes the block's
    bytes — and there are 0 fences in the corpus output at all, so the case is unobservable
    here. Classifying every such line put 6 inside an unclosed code span and 1 after a table
    pipe against 13471 plain; neither of the two renders the whitespace. That classification ran
    the sink directly on `extract`'s output, which is a different layer from the CLI and reaches
    13478 lines rather than 12342 — the same defect counts differently per stage, so both were
    measured.
  - **Reconciled in both directions against a pre-fix baseline: the only rune that changed in
    any of the 12 files is `U+0020`, `-12947`**, matching the measured total exactly, with 0
    trailing whitespace remaining and all 36472 line counts unchanged.
  - **Twelve mutations applied, and the first pass killed seven.** Four survivors were one
    path — the interior lines of a chunk carrying its own newlines, which only a code block's
    body produces — now covered by a three-line fixture whose two interior lines end in a space
    and a tab. A fifth is byte-identical corpus-wide and needed a whitespace-only `Replacement`
    to reach. The last was deleted rather than tested: `write`'s `if s == ""` guard is
    unobservable, since `bufio.Writer.WriteString("")` cannot fail with nothing to write.
  - **A control byte could end a line, which review found and this rule made visible.**
    `inline`'s whitespace-only branch writes a span's text without `escapeInto`, and
    `unicode.IsSpace` counts VT and FF where `isControl` does not — so `**word** \v\n` was
    emitted, since `TrimRight(" \t")` does not remove it. Now sanitized. 0 of the corpus's 11597
    whitespace-only spans hold a control byte, so the fix is held by a test alone.
  - The figures previously logged for this defect (539 lines with 2+, 11970 with one) are
    superseded: the `/K`-order and `/ActualText` fixes changed where lines end, so it was
    re-measured before implementing.

### Fixed — 2026-08-12

- **A producer's `/ActualText` was never read, and 16 words came out with a stray hyphen in
  the middle.** ISO 32000-2 §14.9.4 makes the key a *replacement* for what the glyphs spell.
  `sectionize.substituted` now applies a declaring element's value to the spans it covers, so
  `di-gest` reads `digest` — a declared `U+00AD` SOFT HYPHEN is discretionary, drawn only where
  a line breaks, and nothing downstream has a line width to break at.
  - **All 4803 values on disk are three strings and every one is on a `Span`.** Measured before
    implementing, which is what stopped a general rule from being a regression: 4695 declare a
    line break over a drawn space, 92 declare `" • "` over a drawn `U+25A0` BLACK SQUARE, and
    16 declare the soft hyphen. Substituting all three verbatim broke the majority case — it
    put a line break into inline text, and `**Technical Specification**` came out as two lines
    and lost its bold, since a CommonMark emphasis run cannot span one.
  - **`inlineText` adapts the value to what a `doc.Span` holds.** A break becomes a space; a
    soft hyphen is dropped. A dictionary string can say things a run of drawn glyphs cannot,
    and every sink downstream is entitled to assume it does not.
  - **Wired into both inline walkers.** All 92 `" • "` declarations are `LI>Lbl>Span` and reach
    `labelText`, not `gather`; a rule in one walker only would never reach the corpus's largest
    declared shape.
  - **All three consumers of the raw value adapt it, which review found and the corpus cannot
    show.** Besides `substituted`, `emitItem` copies `/ActualText` into `doc.Block.Replacement`
    — which every sink's `substitute()` prefers over the block's spans — and `title` reads it
    for a heading that drew no glyphs, where `clean` folds the declared break but leaves the
    soft hyphen, since it is not whitespace. Either one left raw reinstates exactly this defect
    one layer further on. No file on disk reaches either, because all 4803 declarations are on
    a `Span`, so both are held by a test alone.
  - **Net corpus effect is exactly `{U+002D: -16}`**, reconciled in both directions: 32004 −3,
    32005 −10, 32002 −2, 32003 −1, nothing else changed anywhere. Every joined word matches its
    own document's majority spelling — `MACLocation` 16 against `MAC-Location` 0, `digest` 31,
    `structure` 64, `algorithm` 21 — and 0 `U+00AD` remain in the output. The 92 bullets change
    nothing visible, because both glyphs are list markers and the sink strips whichever it gets.
  - **Both conservation invariants were tightened rather than relaxed.**
    `TestSectionizeLosesNoText` names the lost multiset per rune and exactly, and
    `TestOutlineConservesCharacters` states the expected delta per file — so a substitution that
    *stopped happening*, or started adding characters, fails as loudly as one that loses too
    much. Eleven mutations applied and eleven killed, the last only after dropping the box union
    survived the whole suite and all 51 files: every fixture leaves its spans' boxes zero.
  - Line structure inside the ASN.1 listings of ISO/TS 32004 and 32003 is **not** part of this.
    The declared break appeared to restore it and `pandoc -f gfm` says otherwise — a blank line
    inside a code *span* is a paragraph break with the backticks left literal. Fencing those
    blocks is separate work in `doc.Block.Role`.

- **An element's children were read after all of its own text instead of in `/K` order: 32022
  runes displaced across 13 documents.** `tag.Elem` splits `/K` into `Content` (marked-content
  references) and `Kids` (child elements) and kept no record of how the array interleaved them,
  so the walk read all of one and then all of the other. That is the right answer for the 89813
  elements on disk holding only one of the two, and wrong for the **767 holding both**: every
  rune a child drew moved to the end of its parent's text.
  - **The damage is worst where the child is smallest.** A `Span` wrapping a single soft hyphen
    is torn out of the middle of a word and left at the end of the paragraph, which is what put
    `constituent elements.--` in ISO/TS 32005's Table 1 — the hyphens of `exposi-tion` and
    `constitu-ent` trailing behind the sentence they belonged inside.
  - **Fixed in the model, not the walker.** `tag.MCRef.Order` and `tag.Elem.KidAt` record each
    item's position in the same `/K` array, and `sectionize.inOrder` merges the two slices on
    those positions. `readKids` writes `Kids` and `KidAt` itself so the two cannot be assigned
    by separate statements and fall out of step — they are one sequence indexed twice.
  - **Four separate output defects were the same defect.** Glued words (`ISO/TS32005`,
    `First edition2023-07`) where the gap between a parent's text and a child's was never
    measured; a spurious space inside `http:// creativecommons.org`; doubled TOC emphasis
    (`**Preface ...**  **2**` for one entry); and links swallowing the punctuation after them
    (`).www.iso.org/directives` for `www.iso.org/directives).`). None needed a rule of its own.
  - **A run is every reference up to the next kid, not one per `/K` position.** Splitting per
    position emits a paragraph per marked-content reference: 286 extra paragraphs across 205
    transparent elements in 9 documents, 118 of them in `Well-Tagged-PDF-WTPDF-1.0.pdf`.
  - Reconciled in both directions on ISO/TS 32005: 4472 spaces appear where gluing had hidden a
    gap, and **not one non-space rune is lost** — the only other change is 2 backslashes that
    the gluing itself had required, escaping a `[a][a]` that looked like a link reference and no
    longer does once the brackets are separated.
  - **Review found a panic in the merge.** The loop bounded its kid branch by `len(Kids)` while
    `kidBefore` bounded the same decision by `len(KidAt)`, so a `KidAt` longer than `Kids` — a
    position naming a kid that does not exist — answered "this kid comes first" and then indexed
    past the end. `tag.Read` cannot build that state, but `tag.Elem` is exported with both
    fields settable, so a caller can. `kidBefore` now requires both bounds, and
    `TestInOrderSurvivesEveryKidAtSkew` covers all ten ways the three slices can disagree
    (either length skew, absent slices, negative, duplicate and descending positions),
    asserting termination and that every item is handed over exactly once.

- **A line wrap put a space after a hyphen that was holding a word together: 483 words split
  across the corpus.** `marked- content`, `cross- reference`, `human- readable`, `ISO 32000-
  2:2020`. `appendLine` infers a space at every line break inside a paragraph, which is right
  for Latin prose and wrong when the break falls inside a word. Measured at the decision point
  rather than by grepping output: **489 dash-final wraps across the 17 PDFs on disk — 483 with
  the dash attached to a letter or digit, 5 with a space before it, and 1 with punctuation
  before it.** All 483 were wrong and all 6 of the rest were right, so `dashHoldsTheWord` keys
  on the rune *before* the dash and requires a letter or digit specifically. 193
  characters removed from ISO 32000-2's Markdown, 208 breaks in `mupdf_explored.pdf`, 15 in
  WTPDF; 17 hyphen-space pairs remain corpus-wide, 12 of them correct suspended hyphens
  (`one- and two-dimensional`).
  - Neither the dash nor the following character can decide it. **26 of the 483 continue into a
    digit and 17 into a capital** — `41- 44`, `GREATER- THAN`, `UTF- 8` — words a space breaks
    exactly as badly as a lowercase one.
  - **The rule walks back through spans, which is 16 of the 483.** A producer often sets the
    dash in its own style run, or the tagger gives it its own MCID, so `prev` is the bare `-`
    and the word is one span earlier: the `surrounding`, `structure`, `constituent`, `digest`
    and `algorithm` breaks in the TS documents. Only dash-only spans are skipped, so the walk
    cannot run past the word it is looking for.
  - **The alphabet is the Pd category, not `U+002D`.** `U+2013` holds `a– f` together in a
    hexadecimal range and `U+2011` holds `doc‑ bibliography`; the one `U+2014` at a wrap is
    detached and keeps its space through the same attachment test. `U+00AD` and `U+2212` are
    added as Pd's near misses — neither ends a line on disk, and the cost of including them is
    one missing space against a split word for leaving them out.
  - **The hyphen is kept, and that is the corpus's answer rather than caution.** Looking each
    joined word up in its own document — in text with the rule *off*, since with it on every
    candidate finds the join the fix just made — all 483 resolve: **218 are spelled elsewhere
    without a hyphen** (`applica- tion` / `application`), **170 with one** (`cross- reference` /
    `cross-reference`), **14 both ways and 81 nowhere else at all.** Nothing separates
    hyphenation from compound punctuation, and a deleted hyphen is not recoverable from the
    output.
  - **The entire suite passed with all 483 defects present**, because no assertion looked at the
    character before a wrap — and it passes with them fixed, since the corpus tests assert
    counts and conservation rather than text. Ten mutations were applied and all ten killed:
    removing the rule fails `TestWrapSpaceSuppressedAfterAWordHyphen` and
    `…WhereTheHyphenIsItsOwnSpan`; suppressing on any dash fails
    `TestWrapSpaceKeptAfterADetachedDash`; dropping the span walk, narrowing the alphabet to
    `'-'`, dropping digits from the word test, not trimming dashes during the walk, `break`
    instead of `continue`, a bare dash opening a block, and dropping `U+00AD`/`U+2212` each fail
    `TestDashHoldsTheWord`. The tenth is the one nine rounds of mutation missed and a review
    found: relaxing the word test to `!unicode.IsSpace(r)` survived everything, because no test
    put *punctuation* before the dash. The corpus has exactly one such wrap — `resources/-`
    breaking into `Courier'` in `mupdf_explored.pdf`, a list separator that needs its space — so
    that case and a quote-mark variant are now rows in `TestDashHoldsTheWord`, and the mutation
    dies on both.
- **A figure's `/Alt` was substituted over real page text, deleting three captions.**
  `doc.Block.Alt` carried both `/Alt` and `/ActualText`, which are opposite operations:
  §14.9.4 makes `/ActualText` a *replacement* for content, §14.9.3 makes `/Alt` a
  *description* of it. One field cannot say which it holds, so every sink had to pick one
  behaviour for both and picked substitution. On `PDF20_AN001-BPC.pdf` the illustration is a
  `Figure` whose `/Alt` paraphrases the whole picture, and inside that same `Figure` three
  captions are drawn as real text (MCID 271) — all three were written over. **129 characters
  on the one block in the corpus where the two disagree.** Fixed in the model rather than in
  the sink, because the sink had nothing to decide on: `Replacement` is now a field of its
  own, `sectionize` stops collapsing the two (the `alt()` helper that preferred
  `/ActualText` and discarded `/Alt` is gone), and one `substitute()` rule serves both
  markdown walkers and the OKF sink — `/ActualText` always stands in, `/Alt` only where the
  block draws nothing. Picking "description" for both instead would have broken the case
  `/ActualText` exists for.
- **The corpus decided the shape of that fix, and one of its zeros is a wiring gap rather
  than an absence.** Of the 218 blocks carrying either field, **217 are `/Alt` on a block
  with no text** — where substitution is right and is the only text there is — and 1 is the
  AN001 figure. **0 carry a `/ActualText`**, and that is not because the construct is rare:
  there are **4803 of them across the 51 PDFs on disk**, and every one is on an inline `Span`
  element, which `sectionize` never lifts into a block. So the substitution branch never
  fired correctly on disk, only wrongly, and the `Replacement` arm is a correct model of
  §14.9.4 that no corpus file currently reaches — its rule rests on unit tests, which is
  recorded at the branch rather than left to be inferred from a passing suite.
- **The existing conservation test could have caught this and was not pointed at the file.**
  `TestMDOutlineConservesText` reports `missing=2` on AN001 under the old model — only 2
  because the paraphrase re-uses the captions' own words, which is how a paraphrasing `/Alt`
  hides its own damage from a letter count. AN001 is now first in its file list. The
  assertion also had to change: it compares the outline's gain against the **substituted**
  total, not the total `/Alt`, because on this file those differ — **212 letters of `/Alt`
  against 33 substituted**, and asserting against the total would demand the 179-letter loss
  back. After the fix `missing=0` on all four files and `gained` equals the substituted total
  exactly (33 / 33 / 44 / 9524).
- **A caption wrapped in emphasis without trimming emitted a bullet instead.** CommonMark
  §6.2 forbids an opening emphasis delimiter followed by whitespace, so `*   text *` renders
  as `<ul><li>text *</li></ul>` — confirmed with `pandoc -f gfm`. The caption/figure branch
  wrapped unconditionally, and the recovered captions arrive behind ~100 spaces of
  two-column padding. **Pre-existing, not introduced by the change above**:
  `Well-Tagged-PDF-WTPDF-1.0.pdf` emitted `* cube root of x *` at line 856 on the unmodified
  tree, proven by stashing the fix and re-running. The `/Alt` fix made a second instance
  reachable and the trim takes both to 0 — **2 → 0 broken-emphasis lines corpus-wide**. A
  caption's leading space is a producer's positioning and carries no meaning, where inside a
  paragraph it separates words and must survive, so the trim is at the caption branch and
  not in `oneLine`.
- **Both new rules were mutation-tested.** Restoring the defect — `/Alt` substitutes
  unconditionally — is killed by `TestAltDoesNotReplaceSpans`,
  `TestCellAltDoesNotReplaceSpans` and `TestMDOutlineConservesText/PDF20_AN001-BPC.pdf`, so
  it fails at both the unit and the corpus level. Dropping the caption trim is killed by
  `TestCaptionTrimsWhitespaceBeforeEmphasis`. Five existing tests were pinning the merged
  contract by setting `.Alt` where they meant `/ActualText` and were moved to the split
  model; `TestAltPreferredOverSpans` was pinning the defect itself and became a
  `/ActualText` test plus a new `/Alt`-does-not-replace test.
- **Review found two of the new rules unpinned, and one of them let the original defect
  back in.** Mutation testing before review covered the rules it occurred to me to break.
  Two it missed: swapping the order of `substitute`'s branches so `/Alt` is tried first
  **survived the whole `sink/markdown` package**, because precedence is only observable when
  both fields are set *and* the spans are blank — with text present the `/Alt` branch excludes
  itself, so the existing both-fields test passes either way. And in `sink/okf`, reinstating
  the original defect outright — `/Alt` substituting unconditionally — **passed every test in
  the repository**, since `describe` had no test touching either field and the corpus reaches
  neither branch. Fixed with `TestReplacementWinsOverAltWhenSpansAreBlank` and a four-case
  `TestDescribeSubstitutesReplacementNotAlt`; both mutations now fail, re-verified by applying
  each one again. The lesson is the general one: a rule whose corpus population is 0 is held
  by unit tests alone, so "the suite is green" says nothing about it.
- **One review claim was wrong and is worth recording, because it points the other way.**
  The reviewer reported that AN001's figure `/Alt` "is NOT dropped, it's emitted correctly",
  generalizing from a synthetic blank-span fixture. Grepping the actual output for the
  paraphrase returns **0**. It is dropped, deliberately — see `DESIGN.md`, which records that
  trade rather than leaving it implied.
- **The U+FFFD in that figure's alt text is correct and was checked rather than assumed.**
  The file genuinely stores a trailing `0x00` on object 147's `/Alt`, `DecodeTextString`
  decodes it faithfully, and CommonMark §2.3 requires U+0000 be replaced — a policy already
  pinned at `sink/markdown/inline_test.go`. Also confirmed while comparing against the
  Acrobat page renderings: the `- ****` doubled-marker defect recorded in this file, in
  `DESIGN.md` and in ADR 0011 is **gone** from current output, and the running
  header/footer artifacts are correctly suppressed.

- **The synthetic structure-tree root claimed `Document`, inflating that role's count for
  almost every tagged file.** `/StructTreeRoot` has `/K` but no `/S`, so it is not a
  structure element; `tag.Read` synthesizes a root `Elem` to start the walk from and gave it
  `RoleDocument`. A tagged document's own top element is a `Document` too, so **17 of the 18
  tagged files on disk reported two where the file has one**. Not an internal figure: `probe`
  publishes `Stats.Roles` as `tags.top_roles`, and `docs/test.docs.md` plus
  `testdata/manifest.json` both recorded "two `Document` elements" for
  `adobe-samples/sampleInvoice.pdf` — contradicting the entry below, which counted the
  objects directly and said its single live `/S /Document` has no `/K`. The store-wide sweep
  and a walk from `StructTreeRoot/K` both return 1, and so does Acrobat; the tree returned 2.
  The root is now `RoleStructTreeRoot`, a name outside §14.8.4 — which makes the collision
  rare rather than impossible, and the distinction is in the doc comment: nothing rejects an
  `/S` or `/RoleMap` target naming it, but 0 elements across those 18 trees do, against a
  `Document` in almost every one. Behaviour is otherwise identical; `Depth` excluded the root
  by name before and `isGrouping` excludes it one layer earlier now, with the name check that
  keeps a real `Document` from counting as a heading level left in place. Pinned by
  `TestSyntheticRootDoesNotInflateARoleCount`: the count arm fails if the root reverts, and
  the depth arm is nested `Document > Sect > H` so that it fails for either of two mutations
  — admitting the new role to `isGrouping`, or dropping `Depth`'s `RoleDocument` exclusion.
  An `H` directly under the `Document` would have been absorbed by `Depth`'s floor of 1 and
  asserted nothing, which is what the first draft of the test did.
- **`Elements` and the heading-parent figures were checked and are unaffected.**
  `extractPdfInput.pdf` reports 259 elements before and after, since that count always
  included the root. `DESIGN.md`'s `heading parents: map[Document:17 Part:964]` for ISO
  32000-2 was the figure most likely to have hidden the same defect, and re-measuring returns
  the same 17/964 over 981 headings: those 17 are the file's own `Document`, not the root,
  which is never a heading's parent because a heading's parent is where the tree put it.
- **A Unicode line break in PDF metadata truncated the frontmatter block, dropping every
  key below it.** YAML 1.2 §5.4 counts NEL (U+0085), LS (U+2028) and PS (U+2029) as line
  breaks alongside LF and CR, and `gopkg.in/yaml.v2` implements it. `plainYAML` returned
  true for a value containing one, because every rule in it scans **bytes** and these are
  multi-byte in UTF-8 — the `c < 0x20` check reads a lead byte of 0xc2 or 0xe2 and passes it
  through. Emitted raw, one of them ends the line: the loader reads the rest of the value as
  a new line of the block, fails there, and everything below is gone. A `/Title` of
  `x<LS>---<LS>y` loaded back as `x`, with `pages`, `tagged` and `encrypted` silently absent.
  Quoting alone was not enough either — a raw NEL inside a quoted scalar loads back as a
  plain space — so `yamlString` now emits YAML's own `\N`, `\L` and `\P`, `\xNN` being
  defined only for 8-bit values. Both sinks were affected, since `sink/okf` writes through
  `markdown.YAMLString`.
- **Reachable from an untrusted file, and latent.** These strings come from `/Title`,
  `/Author`, `/Subject`, `/Keywords`, `/Creator` and `/Producer`; LS is what a producer
  writes for a line break inside a UTF-16BE text string. It is not an escalation — no second
  key can be injected, because a colon needs a following space and the document stops
  parsing before one is reached — but a consumer got a truncated mapping instead of an
  error. **0 of 23816 frontmatter lines** the corpus emits carry one of the three, the same
  shape as the 0-of-125 figure above and the reason no corpus test could have caught it.
- **The blast radius is YAML and only YAML, checked rather than assumed.** Every
  value-carrying write in `sink/okf/frontmatter.go` goes through `markdown.YAMLString`, so
  that sink is covered by the same change. The other door these strings leave by is the
  Markdown body: `oneLine` passes all three through, because its `isSpaceByte` guard tests
  only `\n\r\t` and space and so returns early before `strings.Fields` — which would have
  split on them — and a raw break reaches a link label and inline text. Rendered it is
  harmless: `pandoc -f gfm` returns one intact `<a>` per case, the break collapsing to a
  space. Left alone as a latent inconsistency rather than treated as a second defect;
  Markdown has no construct these characters terminate, which is what makes YAML the
  special case.
- **`TestFrontmatterCannotBeEscaped` asserted the right property with the wrong alphabet.**
  Its `strings.ContainsAny(s, "\n\r")` check cannot see a line break the loader honours; the
  round-trip half is what catches these, and all three are now in its hostile list. Each
  half of the fix is pinned independently: removing either the plain-scalar rejection or the
  escape fails `TestYAMLQuoting`, `TestQuotingRoundTrips` and
  `TestFrontmatterCannotBeEscaped`.

### Added — 2026-08-12

- **A gold fixture for `md -frontmatter`, which had none.** `testdata/reference/metadata.pdf`
  is the tenth reference fixture and the only one that sets `Title`, `Author`, `Subject` or
  `Keywords` — LaTeX does not write `\title` into `/Info` on its own, that is `hyperref`'s job,
  so their absence was a property of the nine `.tex` sources rather than of the engine. It
  carries a second gold file, `metadata.frontmatter.gold.md`, holding the whole expected output
  fences and prose included, with `{{source}}` substituted for the path the caller typed since
  that is the one field belonging to the invocation rather than the document. Everything else is
  asserted verbatim, dates included: the PDF is pinned by SHA-256, so its `/CreationDate` is as
  fixed as its title. This closes the second of the two reasons the `Info` defect below
  survived — the first, a walk that never counted, was fixed the day before.
- **What the fixture asserts is the reader, and that was measured rather than assumed.**
  Mutating the writer — transposing the field order, quoting every value, dropping the blank
  line after the closing fence, emitting empty keys instead of omitting them — is caught **four
  times out of four** by `sink/markdown`'s own unit tests, so against the writer a gold file
  adds nothing. Mutating `extract.metadata()` is the opposite: reading `Subject` from
  `/Keywords` and `Keywords` from `/Subject`, `Creator` from `/Producer` and `Producer` from
  `/Creator`, or `Modified` from `/CreationDate` — **each survives every test in the repository** (`go test ./...`, 23 of the 24 packages having tests)
  and dies only on this fixture. Every one emits a full block of plausible, well-formed,
  non-empty values, which is exactly what a presence check, a key count and a loader-validity
  check all pass. Only an independently authored statement of *which* value belongs in *which*
  field separates them. Reverting the `Info` fix also fails here, with all four identity fields
  gone.
- **A round-trip property for the YAML quoting rule, and three whitespace positions the
  table was missing.** Chasing the recorded survivor above found the record wrong twice over.
  The rule is not uncovered — `TestYAMLQuoting`'s `" a"` case kills its outright removal — and
  the corpus walk cannot reach it because **0 of 125 non-empty metadata values across all 50
  PDFs on disk carry edge whitespace**, so it was never a corpus survivor in the first place.
  What *was* uncovered is narrower and invisible from a single example: a rule checking
  **leading space alone, or trailing alone, survives every test in the repository**, because
  one point pins one position of a four-position property. All four are now in the table.
  The two tab cases belong to the control-byte rule rather than the space rule (a tab is
  0x09), and taking that one step further found a real defect rather than an equivalent
  mutant — see **Fixed**, below.
- **`TestQuotingRoundTrips` is the generalization.** Whatever `yamlString` emits, a loader
  must hand back the string it was given, as a `string` — asserted over all 30 cases in the
  table plus non-ASCII ones from the corpus. That is what the whitespace class needs, because
  a loader *strips* edge whitespace rather than rejecting it (`title: a ` loads as `a`, no
  error), so the failure is a corrupted value inside a perfectly valid document and no check
  on parseability can see it. Same for the coercions: `1.7` as a `float64` is valid YAML and
  is no longer the version string of the document. It kills three of the four mutations above
  independently of the table.
- **The fixture's two dates differ on purpose.** `\pdfinfo{/ModDate (D:20240131120000Z)}` sets
  a date no clock here produces, which pdfTeX honours while still writing its own
  `/CreationDate`. Built with the two equal, the `Modified`-from-`/CreationDate` mutation
  emitted a matching value and survived this test as well — the assertion was true by
  coincidence. The title also carries a `": "` and the keywords a `","`: the first must be
  quoted (`": "` ends a YAML key anywhere in a line) and the second must not (a comma is only
  special inside a flow collection), so the pair separates the quoting rule from a rule that
  quotes defensively. The values arrive UTF-16BE with a BOM, as `hyperref` writes them, making
  this the only committed fixture that pins that decode path.

### Fixed — 2026-08-11

- **The document information dictionary was never read, for every file the tool has ever
  opened.** pdfcpu does not keep the raw trailer — it folds the entries into its xref table —
  so `objects/pdfcpu` reconstructs the dictionary its callers use. It reconstructed `Root`,
  `Encrypt` and `Size` and dropped `Info`, which made the whole `Info` branch of
  `extract.metadata()` unreachable: `Title`, `Author`, `Subject`, `Keywords`, `Creator`,
  `Producer`, `CreationDate` and `ModDate` were empty everywhere.
  Nothing failed, because an absent title is indistinguishable from a document that has none.
  `md -frontmatter` emitted a block with nine fields missing and looked correct; the OKF bundle
  titled its root index from the filename slug rather than the document, and its `sources[]`
  entry carried a bare `resource` with no `title` or `last_modified`. The measurement that
  found it: **0 of 11 corpus documents and 0 of 37 reference fixtures** carried any `Info`
  field, which is not a plausible property of real PDFs. After the fix **25 of 37** fixtures
  do, and all 11 corpus documents recover a title — `ISO 32000-2:2020 (PDF 2.0) including
  Errata Collection 3` among them. `TestDocumentInfoIsRead` asserts it on committed fixtures,
  so it holds on a clean clone, and fails on `Creator`/`Producer` without the fix.

### Added — 2026-08-11

- **The emitted OKF frontmatter is now parsed by a real YAML loader.** `sink/okf`'s
  frontmatter is written by hand and says why — a library reorders keys, and a stable order is
  what makes two runs diff cleanly — but every assertion over it was `strings.Contains`, which
  cannot tell a nested mapping from a flat one. `TestOKFFrontmatterLoads` loads all **1409
  frontmatter blocks** and **19781 scalars** across the corpus with `gopkg.in/yaml.v2` (already
  in the module graph via pdfcpu, so no new dependency), asserting that every block parses,
  that `sources` and `generated` are the nested shapes OKF §5.1 and §7 define, and that every
  scalar loads back as a `string` rather than a coerced `int`/`float64`/`bool`.
  It kills four mutations the previous suite missed: two-space `indented()`, a `sources` entry
  without its `- `, an unindented `generated.by` — which parses as `generated: null` beside a
  top-level `by`, moving provenance out of the field OKF defines it in — and removing
  `yamlReserved`, which coerces **2195** scalars including 29 booleans and two clause titles
  that are bare numbers. One survivor is recorded rather than fixed: `plainYAML`'s
  leading/trailing-space rejection, which no corpus value exercises. (Followed up the next
  day — see 2026-08-12 below. The rule was already covered; what the record got wrong was
  reading "survives a corpus walk" as "untested".)
- **`TestMDFrontmatterOffByDefault` now counts the keys it walks.** It checked that every line
  the frontmatter emitted was a well-formed `key: value` — but `scalar()` omits a key whose
  value is empty, so the loop passed over a block missing most of its fields. That is why the
  `Info` defect survived a test written to cover exactly this output: 6 keys were emitted where
  12 belong, and every one of the 6 was well-formed. The floor turns "every line present is
  well-formed" into "the lines are present", and fails at 6 without the store fix. No
  `.gold.md` fixture covered `-frontmatter` at all, which is the other half of why nothing
  caught it — closed the next day by `metadata.pdf`, above.
- **Two tests for what became attacker-reachable when metadata started being read.** `Title`,
  `Author`, `Subject`, `Keywords`, `Creator` and `Producer` are strings from an untrusted file
  and they now reach output where they were previously always empty.
  `TestFrontmatterCannotBeEscaped` pins that no emitted scalar can contain a line break at all
  — `YAMLString` writes a newline as the two characters `\n`, so a title of
  `x\n---\ntype: other` cannot close the frontmatter fence early — and that the value still
  loads back byte-identical, since escaping that corrupts the value is a different defect from
  escaping that breaks the document. Restoring the newline case to a raw byte fails both
  halves. `TestDocIDIsASafeSegment` pins the other reachable surface: the title now names the
  bundle's root directory, and `kebab`'s `[a-z0-9]`-and-dashes allowlist makes traversal
  structurally impossible rather than filtered — `../../etc/passwd` becomes `etc-passwd`.
- **`bundleOf` now sets `Meta.Path`, as every CLI verb does.** Without it `builder.source()`
  returns nothing, so the bundle under test had **no `sources:` block at all** where a real run
  has 1398 — meaning every OKF test measured a shape the CLI never emits, and
  `okf/frontmatter.go`'s `indented()` was unreachable from the tests. Fixing the harness is
  what exposed the `Info` defect above.

- **A test for `cellText`'s trim, which nothing in `sink/markdown` asserted.** The backlog
  carried "a leading space on cells after the first, 1 row in 50 PDFs, cosmetic". Measuring it
  found the opposite of both halves: at the `doc.Block` level **11084 of 16401 table cells**
  begin with a space, because `extract` carries an inferred space on the fragment that
  *follows* it — deliberately, so trimming stays the sink's decision — and `splitAtRules` cuts
  a ruled row at those same gaps, so every cell after the first opens with one. In rendered
  Markdown the count is **0 of 16724**: `cellText` trims, and removing that trim puts the
  space back on **11310** cells. So the defect is unreachable in output and the recorded
  figure was measuring neither the cause nor the symptom.
  What was real is that the guard was untested where it lives. `sink/markdown`'s whole suite
  passes with the trim deleted; the only thing that caught it was
  `TestReferenceExactMatch/table`, two packages away, as a whole-document byte diff naming no
  cause. `TestCellLeadingSpaceIsTrimmed` asserts all three paths — plain, code span, and
  `Alt`, which returns before the spans are read and so trims separately — and kills four
  mutations, including replacing either `TrimSpace` with `TrimRight`. The `Alt` branch is
  reachable from no document on disk: `sectionize` sets `Alt` on 218 blocks across the 50 and
  none is a cell, and `extract` sets it on none at all.

- **Untagged appendix headings are recognized, closing half of the limit ADR 0008 recorded as
  measured debt** (ADR 0013). `layout.annexLevel` reads a dotted annex number as a section
  number whose first component is a letter — "B.2.3 CMS MAC validation" is level three for the
  same reason "4.2.3" is — and `Headings` falls back to it where the dotted-decimal rule
  declines. Validated against the tagged corpus's own declarations rather than against
  judgement, by joining the typographic gate to the structure tree on `{page, MCID}`: the shape
  is declared a heading **112 times out of 112**, the level agrees with the declared `H1`..`H6`
  rank **107 against 5**, and it promotes **0** blocks no producer calls a heading — better
  behaved on both axes than the decimal rule already shipping, which scores 931 against 88 with
  10 false positives. Effect on disk is 10 headings in 2 documents and nothing else across all
  49 readable PDFs: `mupdf_explored.pdf` 296→301 (`A.1 Licensing` through `A.3 Coding Style`)
  and `LightOnOCR-2601.14251v1.pdf` 21→26 (`C.1`–`C.4`, `D.1`), every one a genuine appendix
  title previously emitted as a bold or plain paragraph. The 11 tagged ISO documents are
  byte-identical, since inference never runs where a producer declared a role.
- A separator the corpus needs and no test named: `unicode.IsSpace` accepts a **tab** between
  a clause number and its title, and 18 clause headings on disk depend on it — all in ISO TS
  32001 through 32005, "3\t Terms and Definitions". Review flagged the breadth as a possible
  extractor-split symptom; measuring it inverted the concern, since narrowing the check to
  U+0020 or U+00A0 loses all 18. There is a fixture for it now and a mutation proving it
  bites. Newlines are the genuinely unobserved shape (0 spans of 50 documents) and the comment
  says so rather than implying they were measured.
- Thirteen mutations, all killed. Three of them found missing fixtures rather than confirming
  existing ones: nothing pinned the trailing-dot style ("A.1. Licensing" is level two, not
  three, matching the decimal rule's documented behavior), and nothing pinned that the
  Markdown level cap applies on the annex path — a mutation exempting it emitted level 8 for
  "A.1.2.3.4.5.6.7". Those two and the tab separator above have cases now, which is the point
  of mutating a guard a green suite says nothing about.
- The *bare* annex letter stays deferred with its counter-example named, and ADR 0008's
  proposed instrument for lifting the unnumbered limit is recorded as falsified rather than
  untried: rank and repetition are independent (9 of 151 candidate styles occur once and
  include genuine titles; the most-repeated include "Robin Watts" at 9 occurrences over 7
  pages), and no size ratio has a gap to hold a threshold (precision peaks at 73.2% around
  1.17× body, 6% by 1.63×). Both measurements are in the package comment so neither is
  proposed again as an idea nobody tried.
- **A corpus test that every drawn glyph decodes to text, which the composite half of the font
  package never had.** `TestCorpusDrawnCodesDecodeToText` is the counterpart to
  `TestCorpusSimpleFontsDecodeToText`, and it has to be built the other way round: a simple
  font's encoding *names* codes, so the names are the claim to resolve, while a composite font
  makes no claim at all — a CID indexes a glyph set and implies no character — so the only
  population is the codes the document *draws*. That means interpreting content streams rather
  than reading dictionaries. Result is a negative one, recorded as such in DESIGN.md §10:
  **0 of 2900493 drawn glyphs decode to nothing**, 2689358 simple and 211135 composite. It is
  not a tautology — `compositeText` returns empty whenever `/ToUnicode` is missing or does not
  cover the code — and the assertion that all 96 composite fonts reached carry a `/ToUnicode`
  stream is pinned separately, because the zero depends on it. The test proves presence and not
  correctness: a code decoding to the *wrong* character passes it, so a byte-order defect in
  `cmap` that keeps producing output would survive. Correctness is the gold fixtures' claim, and
  pointing this walk at `testdata/reference` measures its reach — 416 composite glyphs over 5
  fonts and 95 distinct codes, checked word for word — leaving the corpus's other 2522 codes as
  presence only.
- Three mistakes in building that census are now the mutations that guard it. Tracking the last
  `Tf` operand instead of `m.GS.Text.Font` reported 46 undecodable glyphs whose bytes spell
  "Adobe Acrobat Reader" — `q` pushes the whole graphics state including the text state, so a
  linear scan attributes a string to whichever font was set last anywhere in the stream. A
  page-only walk misses the **1977 composite glyphs drawn inside Form XObjects**. And counting
  by `/BaseFont` collapses distinct fonts, which review caught and which moved two figures:
  11 composite names on disk are drawn by more than one font dictionary (`ArialMT` and
  `SymbolMT` by four each; `BCDIEE+SymbolMT` is two objects, so a subset prefix is no
  guarantee), making the real totals **96 fonts and 2617 drawn codes** rather than 79 and 2556.
  Five of seven mutations killed; the two survivors are recorded rather than patched, since
  no form in this corpus inherits its font from the invoking stream and none decodes to nil
  without reporting an error.

### Fixed — 2026-08-11

- `numberedLevel` dropped an unreachable guard that mutation testing found: its `i >= len(s)`
  test for a bare number with no title survived deletion, because `utf8.DecodeRuneInString` of
  an empty tail returns `RuneError` and the whitespace-separator check below already rejected
  it. The folio and table-cell case was being caught one line later all along, in both that
  function and the new `annexLevel` that copied its shape. The same pass found `depth == 0`
  unpinned — a leading-space string returned level 0 with `ok` true — and it has a case now.

### Added — 2026-08-10

- **Untagged ordered lists are recognized, closing the consequence ADR 0011 recorded as
  unreachable.** The ADR's objection was exact and still holds: a numbered item is a paragraph
  opening with a number, which is also what a numbered heading is and what a table row is, and
  nothing on disk separates them. Nothing separates *one block*. `layout.OrderedLists` promotes
  a **consecutive incrementing run at one left edge** — `1.` `2.` `3.`, same label form, same
  margin — which is a claim about a sequence that no heading and no table row makes by
  accident, and it promotes nothing at all on a single numbered paragraph. Measured over all 50
  documents: 70 runs of 260 items, forms `a)` 174, `n.` 43, `[n]` 21, `n)` 17, `a.` 5, lengths
  2–11 with 25 runs of exactly 2. Every run was read; the 4 false positives are tables of
  contents, and all 4 sit in *tagged* files where `inferRoles` never runs, so the live effect is
  **5 runs of 25 items in `mupdf_explored.pdf`, all genuine**, with 3 lone numbered paragraphs
  correctly left as prose. Character accounting on the one changed file is exact: 45 fewer
  bytes, being 25 removed `\.` escapes and 20 inter-item blank lines, with the
  whitespace-normalized text identical.
- `doc.OrderedLabel` and `doc.Block.StripOrderedLabel`, beside `ListMarker` and `StripMarker`
  and in `doc` for the same reason: the marker vocabulary belongs where both producers can
  reach it, since `sectionize` reads what a tree declares and `layout` infers from what a page
  draws, and two copies of the policy is how they come to disagree. Five label forms — `1.`
  `1)` `[1]` `a.` `a)` — which is exactly what the corpus contains and no more. `(1)` and `(a)`
  look like obvious siblings of `[1]`, were written, measured at **zero occurrences**, and
  removed; their removal changed no byte of output on any of the 50 documents, which is the
  proof they were unreachable. Admitting a form on the strength of it seeming plausible is the
  speculative code this repo does not keep — no fixture could reach it, so nothing would catch
  it going wrong. The delimiter is required and is what does the separating: `7.4 Filters` and
  `1 Scope` are clause numbers with no delimiter, and a dotted number has no single value to
  increment, so `N.N` cannot form a run and ADR 0008's rule cannot collide with this one.
  Digits cap at three (a longer run is a year or a byte count) and letters are single and
  lowercase (an uppercase `A. ` opens far too much prose). Validated the way ADR 0011 validated
  its glyph allowlist — against producers' own declarations: of the tagged items declaring a
  `/Lbl` holding an ordered label, `OrderedLabel` reads the same form off the item's text in
  **16 of 16**, disagreeing in 0.
- `StripOrderedLabel` is separate from `StripMarker` rather than a branch inside it, because a
  bullet is one rune and a label is up to five and can be split across spans by a style change
  on the delimiter — a producer setting the number bold and the bracket roman writes `1` then
  `) text`. It consumes a rune count across as many spans as it takes, where `StripMarker`
  decodes a single rune.
- `layout.OrderedLists` runs last in `inferRoles`, and there the order is a precedence claim
  rather than the "nothing depends on it" the Headings/Lists pair measured. A numbered heading
  is precisely the block that could be misread as an item, so `Headings` running first answers
  ADR 0011's objection structurally: a promoted heading is no longer `RoleParagraph`, so it is
  not a candidate. Same for a table cell, which `Tables` produces.
- `sameEdge`'s tolerance is half a point, and it is measured rather than asserted, which is the
  only defensible reason to state a number. Censused over the corpus: of the 192 adjacent block
  pairs that agree on label form and increment by one, 180 have left-edge gaps of exactly 0, 10
  are below 0.1pt, and then there is nothing at all until 2.18pt. Every value in [0.1, 2.1)
  separates the two populations identically, so 0.5 is the middle of an empty band — the same
  shape as ADR 0011's `ListStep`.
- A run that crosses a page break splits into two, and that is a measured limitation rather than
  a preference: the loop is per page, so `a)…d)` ending one page and `e)…g)` opening the next
  promote separately. 5 such continuations exist in the corpus and **all 5 are in tagged
  documents, so 0 reach this pass**. Joining them means carrying run state between pages, which
  no other inference pass keeps; a sink that renumbered from the first item would need it closed
  first, and the markdown sink re-emits each item's own label.
- 6 tests in `doc/marker_test.go` and 11 in `layout/ordered_test.go`. Every check in the new
  code was mutation-verified — the run minimum, each of the three run conditions, both halves
  of the form comparison, the paragraph role gate, the strip invocation, the label separator
  and content requirements, the digit cap, the lowercase-only rule, the leading-space rune
  count, and the post-label gap close. Two mutations survived their first pass and both were
  real test gaps. `sameForm`'s delimiter comparison passed every test when it compared lengths
  instead of strings, because `1.`/`2)` and `[1]`/`2]` have same-length delimiters — both pairs
  are now asserted. And the accepted run's advance passed when it skipped one block past the
  run, because every test had a non-candidate after its list; two adjacent runs is now a test.
  The *rejected* run's advance survives as an equivalent mutation, which is correct rather than
  a gap: a rejected candidate is always exactly one block long.
- Review found the digit cap pinned in one direction only. `2026.` proved the cap rejects four
  digits, so loosening 3 to 4 died — but tightening 3 to 2 passed the entire suite, because
  nothing asserted that three digits are *admitted*. `100.` is now a case, and both mutations
  die. `OrderedLists` accepting an `Options` it never reads was the review's other finding, and
  it is answered in a comment rather than a knob: `MaxHeading` and `MaxLevel` bound a heading,
  `ListStep` is a nesting step for tiers this pass does not produce, and `sameEdge`'s tolerance
  is a different question from `ListStep` — how close two edges must be to mean one margin,
  not how far apart to mean two depths. The parameter stays for the signature the four passes
  share; adding a setting for a value that sits in an empty band is configurability no caller
  asked for.
- Not shipped, and recorded rather than written: a table-of-contents guard reading a
  page-number tail. It catches 3 of the 4 false positives at zero cost to a genuine item — a
  clean separation, unlike the run minimum ADR 0011 rejected at 136-to-3 — but it cannot fire
  on any file this pass runs on, and code no fixture can reach is worse than a measurement in
  a comment. Nesting is not attempted either: every promoted item is level 1, because no
  indented ordered sub-list exists on disk and a tier rule fitted to no positive case is
  fitted to noise, which is ADR 0011's own reason for stating `ListStep` rather than tuning it.

### Fixed — 2026-08-10

- **A hyphen is a bullet on the declared path and not on the inferring one, so it has its own
  vocabulary.** 3 items in ISO 32000-2 emitted `- *-  Markup3D (PDF 1.7)* for a 3D comment.` —
  the sink's own `- ` followed by the producer's hyphen, still inside the italic run the item
  opens with. All 3 are declared `RoleListItem` and declare no `/Lbl`, so `markItem` fell
  through to the glyph, and `doc.listMarkers` excludes `-` on purpose: over the untagged
  paragraphs `layout.Lists` considers, a block-initial hyphen occurs 11 times and is separated
  from its text in **0** of them, and admitting it to the shared map turned 3 C comment
  continuations in `mupdf_explored.pdf` into list items. Both exclusions are right. They are
  answers to different questions.

  `doc.declaredMarkers` is `listMarkers` plus the hyphen, reached only through
  `Block.StripDeclaredMarker`, which `sectionize.markItem` calls. Built from the shared map at
  init rather than written out again, so a glyph admitted for both paths cannot fall out of
  this one — and a separate method rather than a `vocab` parameter on `StripMarker`, because
  which vocabulary applies follows from whether a declaration exists, which only `sectionize`
  knows. A boolean argument would let `layout` pass the wrong one and the compiler would not
  care.

  **The hyphen is the only addition, because it is the only one this path can see.** Measured
  over all 1825 declared list items: exactly 3 open with a glyph `listMarkers` excludes and all
  3 are that hyphen — `*`, `>`, U+00B7 and U+02D9 occur **0 times each** here, so admitting any
  of them would be speculation with nothing to check it against. The 3 are separated from their
  text, `Cambria-Italic` at 10pt, and declare no `Lbl` element at all, so none can enter the
  agreement population and its **124 of 124** is unmoved rather than merely still passing. It is
  also the cheapest possible mistake in this direction: if one of the 3 is not a bullet, what is
  lost is one `-` from an item the producer already called an item, where the old behaviour lost
  nothing and doubled a marker. `*` has no such asymmetry, which is why it stays out of both.

  **`Enumerated` had to move too, and only the corpus A/B found it.** It asks whether a marker
  already in hand is a bullet, and it asked `listMarkers` — so the hyphen `StripDeclaredMarker`
  had just recovered came back as an ordered label and the sink wrote it into the line as
  `- \- *Markup3D…*`: the same doubling one escape further along, and every unit test still
  green. It reads `declaredMarkers` now, where there is no false positive to weigh, since
  `layout` cannot produce a marker this map admits and the shared one does not.

  Result: **3 lines fixed, 0 changed elsewhere** in 50 documents. Four pins, each killing
  mutations the others survive: `TestStripDeclaredMarkerReadsTheHyphen` and
  `TestStripMarkerStillDeclinesTheHyphen` for the split, `TestDeclaredMarkersAddOnlyTheHyphen`
  for its size and its superset property, and `sectionize.TestDeclaredItemsHyphenIsItsMarker`
  for the wiring. That last one is the load-bearing one and was written last, because the first
  mutation run showed why: pointing `markItem` back at `StripMarker` — the whole change,
  reverted at the call site — passed every test in `sectionize`. A vocabulary can be written,
  unit-tested, documented, and never reached.
- **`U+F0A7`, Wingdings' square bullet, is a list marker.** 2 items in
  `PDF20_AN001-BPC.pdf` emitted `- ****  How those brand colours are specified…` — the sink's
  own `- ` followed by a bold PUA glyph that Markdown renders as empty emphasis. Both are
  declared `RoleListItem`, so `markItem` ran and `StripMarker` declined, because
  `doc.listMarkers` did not contain the glyph. It does now, and the A/B over all 50 documents
  changes those 2 lines and nothing else.

  **Found by a census of the declared path, which is the part worth recording.** The survey
  that built the allowlist read block-initial runes over *untagged* paragraphs, and `U+F0A7`
  never occurs there — so no amount of re-reading that tally could have found it. Asking the
  other question instead, "which items did a producer declare `RoleListItem` while
  `StripMarker` declined", found 5 across 2 files: these 2, and 3 hyphens in ISO 32000-2 that
  are the entry above — this one's "stay open deliberately" describes the state between the two
  changes, not the state now. **A survey scoped to one path cannot complete an allowlist both
  paths share.**

  Admitted to the shared map rather than a declared-path-only set because it has no ambiguity
  in either direction: both occurrences are `Wingdings-Regular`, alone in their span at 12pt,
  the block's leading rune followed by whitespace, on a declared `RoleListItem`, and there is
  no third occurrence anywhere in the corpus. That is the strongest evidence a Private Use
  Area codepoint can have — a PUA glyph has no Unicode meaning to appeal to — and
  `doc/marker.go` says so in those terms rather than implying more.
  `TestStripMarkerReadsTheWingdingsSquareBullet` pins it, and has to: the 2 lines live in a
  gitignored file, so no golden covers them, and a mutation deleting the map entry fails only
  that test and the `Enumerated` case beside it. Its span split turned out to be a shape no
  other case reached — three spans with the separator alone in the middle one.

- **A declared label held in a `Span` kid is read, closing the shallow-read defect DESIGN.md
  recorded as open and benign.** `sectionize.label()` walked `e.Kids` far enough to find the
  `Lbl`, then read that element's own marked content and stopped — so it returned `""` for
  **100 of the 153 `Lbl` on disk**, which hold their marker one level down in a `Span` (92 as a
  single span, 8 split across two). `markItem` fell through to the glyph rule and recovered the
  same `■` from the item's text, which is why no output was wrong and why the defect kept.

  **The benign-ness was the corpus, not the code.** Both halves of the bad case are the common
  shape here: 100 of 108 `Lbl` kids are a `Span`, and 16 declared labels are ordered. Only their
  intersection is absent, so "every ordered label is owned by its `Lbl` directly" is a fact
  about these 50 files. An ordered label in a `Span` kid has no fallback — ADR 0011 records a
  leading number as unreachable from the glyph side, being what a heading and a table row also
  open with — so such an item would emit `- [1] text` with the label doubled into the line and
  nothing in the corpus to say so. `TestOrderedLabelInASpanKidIsRead` is that case, and it is
  the one that fails without the descent: reverting `labelText` to the shallow read leaves the
  marker empty and `[1]` in the text.

  The bound is `gather`'s rather than a second rule — descend through a kid with no block role,
  stop at one that has one, and stop at a heading — so the label's idea of where it ends cannot
  drift from the walk's. The nested-list stop is the one the original note named: descending
  through an `L` inside a `Lbl` puts a sub-item's marker into its parent's label, measured as
  `"a.■ inner item"`. The heading stop is worse and was found only by mutation: `gather` hands a
  heading kid to `visit`, which opens a section from it, so consuming its spans first leaves
  that section titled `""` — a lost clause name rather than a mislabelled item. Deleting the
  `IsHeading` term passed every other test in the package, including the nested-list one, since
  a heading has no block role.

  Neither stop occurs on disk: the first text below a `Lbl` is at depth 1 in all 100 cases and
  `Span` is the only kid role that ever appears, 108 times. They are there for shapes
  ISO 32000-2 permits, and `TestLabelStopsAtANestedList` and `TestLabelStopsAtAHeading` are what
  keep them, since no fixture would.

  The A/B is **byte-identical across all 40 documents that render text**, which is the predicted
  result rather than a null one — the descent reads the marker the glyph rule was recovering, so
  both routes agree. Re-measured after: the 100 are unmoved, and `label()`'s empties are **2**
  where they were 102, those 2 being the producer's own version — a `Lbl` declaring no marked
  content anywhere below it, which no descent can fix and
  `TestEmptyLabelFallsBackToTheGlyph` still covers. Three mutations, all fatal: no descent,
  unbounded descent, and the missing heading term.

### Changed — 2026-08-10

- **The `/Lbl` figures, re-measured again — the 08-08 entry's `1407 / 121 of 121 / 1286 / 13`
  and its `147` labels are all superseded.** Current: **1412** declared list items open with an
  allowlist glyph, **1288** declare no label, **124** declare one and the label's first rune is
  that glyph in **124 of 124 with 0 disagreeing**, **16** declared labels are ordered (`a.`,
  `b.`, `[1]`–`[7]` in WTPDF, `1.`–`3.` in `tagged-lists.pdf`), **153** `Lbl` exist and all 153
  are direct kids of their `LI`, **1672** items declare none, and removing the label leaves the
  item opening with whitespace in **139 of 153** — the figure `SetMarker` exists for, quoted as
  `133 of 147` above.

  Two of these are now scoped rather than superseded, by the hyphen entry above: `1412` and
  `1288` are the counts under `listMarkers`, which is still exactly what they were measured as,
  and the declared path reads **1415** and **1291** through `declaredMarkers`. The other figures
  are unmoved — the 3 hyphens declare no `Lbl`, so `153`, `124 of 124`, `16` and `139 of 153`
  cannot see them. That the *agreement* figure is the one that stayed still is the point of the
  last paragraph below, and this is the first admission to test it.

  `139 of 153` and `0 ordered items emptied` were re-derived independently rather than carried
  over, by reproducing the declared path outside the package: join `Lbl` and `LBody` marked
  content to page spans on `{page, mcid}`, then run the same `doc.Block.SetMarker`. Both come
  back exact, along with 153 labels, 102 empty and 16 ordered. Note that `133 + 6 = 139` is a
  coincidence rather than the derivation — the 6 new labels do all open with whitespace, but the
  old 133 was measured over a 147-item population containing none of them.

  Unlike 08-08's correction this is not the same population re-counted: there are two causes and
  only one is ours. `testdata/reference/tagged-lists.pdf` was committed *after* the figures were
  taken and contributes **3 items but 6 labels** — it holds 6 `LI`, every one declaring a
  `Lbl`, and only 3 of them open with an allowlist glyph, so it lands in the two populations at
  different sizes. Admitting `U+F0A7` contributes the other 2 items, both declaring no label.
  `1412 − 2 − 3 = 1407` and `153 − 6 = 147` reconcile exactly, which is what makes the
  attribution a measurement rather
  than a story. **A figure defined by the allowlist moves when the allowlist does** — so the
  agreement count is the one to re-run when a glyph joins the map, since a bad admission is a
  glyph whose declared label disagrees, and that is the only figure where it would surface.
- **The list-item total is 1825, and the old `147 / 1915` pair — implying 2062 — is retired as
  unreproducible rather than corrected.** Adding a fixture cannot *remove* 237 items, so that
  pair was not the same population counted differently; it was worth chasing rather than
  quietly replacing, because `label()`'s "taking the last instead is a change no input
  distinguishes" rests on the no-label count. Three independent counts agree on
  **1825 = 153 + 1672**, with **0** items declaring two labels: a structural walk of
  `tag.Tree`; a pointer-identity check confirming no element is reached twice through a shared
  kid (1825 visits, 1825 distinct); and a store-wide count of every indirect `/S /LI`
  dictionary, which is the one that does *not* depend on the walk.

  Only the label half of the old pair reconciles: `153 − 6 = 147` for `tagged-lists.pdf`'s
  contribution, so the `153` is the same measurement the old `147` was. The other half does
  not, and no counting method I could construct reaches 2062 — not raw `/S` before `/RoleMap`
  normalization (1819), not `LBody` (1819), not `L` (579), nor any sum of those. So what `1915`
  counted is unknown, not diagnosed; what is measured is that 1672 is right and 1915 is not
  reachable from this corpus.

  That last count also turned up a corpus property worth knowing before anyone reaches for it
  as a cross-check: `testdata/adobe-samples/sampleInvoice.pdf` holds **17** `/S /LI` objects
  that the walk correctly reaches **none** of. Its single live `/S /Document` has no `/K` at
  all, and the only inbound references to those 17 come from the `StructTreeRoot`'s IDTree
  under `/Names` — a lookup index, not the hierarchy. The file's structure hierarchy is empty
  in the revision we read, so **a store-wide object count over-reports by 17 and the walk is
  right**. Conversely `tagged-lists.pdf` contributes 6 items the object count misses entirely,
  because `lualatex` writes its structure elements direct rather than indirect. Neither is a
  parser defect; both would corrupt a census that used object counts as ground truth.
- **`label()`'s "14 of the 147 declare empty text" was wrong about the cause, and the real
  figure is 102 of 153.** The comment attributed every empty label to a producer that "drew
  the marker outside the element it declared for it" — a property of the file. Measured by
  compiling counters into production `label()` and running the corpus through it, the split is
  **2** that own no marked content at all (the producer's version, and the only shape any test
  covers) and **100** that own their marker in a `Span` one level down, which `label()` does
  not read. Recorded open in DESIGN.md rather than fixed here: the output is unaffected because
  `markItem` falls through to `StripMarker` and recovers the same `■`, and all 16 ordered
  labels — the case with no glyph fallback — are owned by their `Lbl` directly. But it means
  the glyph rule, not the declaration, supplies the marker for two thirds of the items that
  declared one, and *"a label exists"* was never *"a label was read"*.

  Fixed by the descent in the entry above, which also names what this deferral got wrong: the
  two facts holding the output up — a `Span` kid, and an ordered label — are each ordinary on
  disk and only their intersection is missing, so "unaffected" was a property of these 50 files.
  The `102` and the `2 / 100` split stand as measured; only "recorded open" is superseded.

### Documentation — 2026-08-10

- **ADR 0012, "Decode a text string through Annex D.2."** The 2026-08-09 PDFDocEncoding change
  shipped with a changelog line and no ADR, and it deserved one: what it reversed was not a bug
  in the ordinary sense but an assumption written into a doc comment as a fact — *"text strings
  in practice do not use 0x80-0x9F"* — and then relied on for four phases. Nothing had measured
  it. A decision recorded only as a fixed defect leaves the next reader with no way to know that
  the reading being replaced was a defensible trade (a BOM-less UTF-8 producer, which `string(b)`
  gets right by accident) that the corpus happens to settle at zero cost.
- **Re-measuring for that ADR invalidated every figure the change was recorded with, and the
  method is the finding.** The original counts came from a recursive object-graph walk that
  marked a `Ref` seen and skipped it, so each string was attributed to whichever *path* reached
  it first — and Go randomizes map iteration order, so identical code reported 21002, 15318,
  12877, 14195 and 13854 strings across runs. A depth bound made it worse by truncating
  differently per traversal. Replaced with two passes: an unbounded worklist collecting reachable
  `Ref`s (dedupe by `Ref` terminates it, the reference graph being finite), then one visit per
  resolved object reading only its own direct entries. Three consecutive runs then agreed
  exactly. **The tell that a corpus figure came from an order-dependent traversal is that it
  moves when nothing does.**
- Corrected in `objects.DecodeTextString` and `pdfDocText`, all in the same direction because the
  old walk could only miss objects: **137 → 144** differing strings, **103 → 142** BOM-less
  strings holding a byte above `0x7F`, **192 → 202** carriage returns preserved by the
  undefined-position fallback. The 2026-08-09 entry's "4 of them to invalid UTF-8" is superseded
  by **142** and is left in place unedited — a changelog records what was believed at the time.
  That one was not an undercount but an impossibility: a lone byte above `0x7F` is never
  well-formed UTF-8, so under any population the invalid count sits just under the differing
  count rather than at 3% of it, which is arithmetic and is what showed the figure was wrong
  before any probe was written.
- Two facts the re-measurement added rather than corrected. All **202** carriage returns are
  annotation `/Contents` in one file and **0** are under a metadata key, so the fallback branch
  is load-bearing for markup text and invisible to `Doc.Meta` — a reviewer hunting for its effect
  in a `/Title` will find nothing and should not conclude it is dead. And the 155 affected byte
  occurrences resolve to 11 distinct values, of which `0x80` (the bullet, 92 occurrences, all
  `/ActualText` in `Well-Tagged-PDF-WTPDF-1.0.pdf`) and `0x84` (the em dash, 25) are almost the
  whole population.
- **`doc.listMarkers`' deliberate exclusion of `*`, `-`, `>` and the two dots is asserted for the
  first time, and pinning it uncovered a defect on the other producer.** The exclusion was
  documented with its measurement and held by nothing: admitting the glyphs passed every test in
  the repo, the corpus goldens included. `TestListMarkerExcludesTheAmbiguousGlyphs` now covers it,
  and each of the five cases is mutation-verified to fail on its own glyph alone — which the
  first draft of the test was not. Its `-` case was `-o - output file name`, the flag the code
  comment quotes, and that text is *glued*: `ListMarker`'s separator gate rejects it whatever
  the allowlist holds, so the case killed no mutation and tested nothing. Re-measured over every
  block on disk, `-` is block-initial and separated exactly **once** (and glued 12 times,
  confirming the comment's 12-of-13), and the separated occurrences are Annex D rows whose first
  column *is* the glyph. Those rows are the test's cases, verified byte-exact against
  `Block.Text()`.
- **The exclusion set is five glyphs, not four: `·` was one name for two codepoints, and the
  mutation pass caught it.** U+00B7 MIDDLE DOT and U+02D9 DOT ABOVE both occur block-initially
  and separated in Annex D (3 and 2 times over all blocks), and the D.3 row whose text
  *describes* `U+02D9  DOT ABOVE` opens with U+00B7. The test case taken from that row therefore
  asserted U+00B7 while reading as coverage of "the dot", so a mutation admitting U+02D9 survived
  a pass that reported all glyphs killed. Both are now asserted from rows whose leading glyph is
  their own subject, both kill their own mutation, and the code comments name them by codepoint.
  A case or figure named by rendered appearance rather than codepoint is not checkable, and
  agreement across sites is not evidence when every site inherited the same name — the same
  failure shape as the population conflation below.
- **Three of the five glyphs are unobservable in the output, which is why a unit assertion was
  the only thing that could hold them.** Per-glyph A/B over all 50 documents: admitting `>`,
  U+00B7 or U+02D9 changes **0 files**, because none occurs block-initially on the untagged path
  at all — their block-initial occurrences are all in a tagged file, where `sectionize` declares
  them table cells and `inferRoles` never runs. No corpus golden and no conservation total can
  tell whether those three are in the allowlist or out of it, so the exclusion was documented,
  unpinned and unobservable at once. That combination is the kind of debt a golden-file suite
  is structurally unable to carry.
- **The A/B that made the exclusion look like a wash was measuring the wrong granularity.**
  Admitting all of them changes 2 of 50 documents, 3 lines each way. Per glyph the two effects
  separate completely: `-` alone **fixes 3 and breaks 0**, `*` alone **breaks 3 and fixes 0**.
  The 3 that `-` fixes are real — declared `RoleListItem` blocks in ISO 32000-2 with a hyphen
  bullet and no `/Lbl`, emitted as `- *-  Markup3D (PDF 1.7)* for a 3D comment.` — and they are
  the 1363-item doubled-marker defect in the one shape its fix could not reach. Recorded open in
  DESIGN.md §10 rather than fixed here: the fix is a second entry point into the shared
  vocabulary for the *declared* path, not a wider allowlist, since a rule firing on inferred
  geometry has to weigh `mupdf_explored.pdf`'s C comment continuation lines and one firing on a
  producer's own declaration does not.
- **Corrected a population conflation in the marker figures, in `doc/marker.go`, `doc/marker_test.go`
  and DESIGN.md.** ADR 0011's "`-` is glued in 12 of its 13 block-initial occurrences" is a count
  over *every extracted block on disk*, while the prose around it is about the untagged
  paragraph blocks `layout.Lists` considers — where the count is **11, none of them separated**.
  The bullet figure has the same split: 1305 of 1305 over all blocks, 7 over untagged
  paragraphs. Both numbers were right and the attribution was not, which is a failure mode that
  survives review by looking internally consistent: four sites carried "12 of 13" in agreement
  and none of them said which population it counted. The per-glyph split for every excluded
  glyph is now recorded against both populations explicitly. ADR 0011's text is left unedited,
  an accepted ADR being immutable, but it carries an appended note naming the population and
  giving the untagged-path counts — the arrangement ADR 0005 already uses for a paragraph ADR
  0007 withdrew, since silence would leave the next reader to re-derive the same mismatch. The
  conclusion is unaffected and in fact strengthened: on the path that rule governs a separated
  `-` does not occur at all, which argues the exclusion harder than 12 of 13 does.

### Added — 2026-08-09

- **`pdfspec okf` converts an untagged PDF.** It used to refuse one outright, because a bundle
  is one document per clause and the clause hierarchy came from a structure tree. `sectionize.Untagged`
  removes that dependency: the hierarchy was never a subtree extraction — ADR 0002 measured ISO
  32000-2 at 7 `Sect` elements against 981 headings, one `Part` holding 13,442 flat children —
  it is a level stack over a linear sequence of headings, and a sequence is exactly what
  `layout.Headings` already produced. So `Untagged` reads `(level, title, content)` out of
  `doc.RoleHeading` blocks and their `Level` and drives the *identical* `builder.open` and
  `builder.place` the tagged path drives over `H1`..`H6`. Measured over the corpus: 4 of the
  untagged documents on disk yield a bundle — `mupdf_explored.pdf` 296 clauses to 3 levels,
  `LightOnOCR-2601.14251v1.pdf` 21, `2201.00069.pdf` 2, `testdata/reference/headings.pdf` 4 —
  and those 4 are the only exit-code changes across all 50 PDFs. Closes the second consequence
  ADR 0008 recorded and Phase 5 of the roadmap.
- `sectionize.Untagged` **reads declared roles and infers nothing.** No geometry and no
  typography, because those belong to `layout`, which has the measurements behind them and the
  negative results that bound them. That is what lets one function serve two producers:
  `layout.Headings` promotes a numbered heading in an untagged file, and package `doctags`
  assigns roles from a recognition model's output. It is the same precedence the tagged path
  keeps — a declaration outranks a guess, wherever the declaration came from.
- `builder.open` and `builder.place`, extracted from `builder.heading` and `builder.emitItem`.
  These are the two operations in the builder that never touched a `tag.Elem`: the stack push
  reads a `(level, title, pages)` triple and the placement reads a `doc.Block` and a page
  range. Extracting them is what made a second producer possible without a second hierarchy
  implementation. `builder.stats` is shared for the same reason — a per-path tally would be a
  per-path definition of "section", and these numbers are the acceptance measurement for both.
- `Untagged` has **no `Unplaced`, and that is exactness rather than a gap.** `Tagged` joins page
  text to structure elements on (page, MCID) and must report what the join missed, because on a
  tagged file unclaimed text is content silently lost. There is no join here: every non-empty
  block is either a title or placed content. The page range is likewise a single number, since
  `extract` nests blocks inside `doc.Page` and no block spans a page break — `Tagged` needs a
  range because a paragraph joined from marked content on two pages is one block there.
- A guard on the untagged branch: a file inference finds no heading in is reported rather than
  written as a bundle of one preamble document, which would look like a successful conversion of
  a specification into a knowledge base and be the opposite. The error names `pdfspec md`, which
  is the conversion that still works. **Checked on the untagged branch only** — 5 tagged files on
  disk reach `Bundle` with an empty outline (invoices and single-table fixtures) and have always
  written the preamble-only bundle that produces; their tree is a declaration that this path has
  no better answer, and broadening the guard would newly reject them.
- `detach` copies a placed block's `Spans` and `MCIDs`, so an `Untagged` outline shares no
  storage with the document it was built from. Review found the asymmetry: the tagged path gets
  this for free and therefore never states it — `emitItem` builds each block's spans from the
  elements' own and `unplaced` copies each survivor by value — while `Untagged` takes the
  extractor's blocks whole, which is what carries their roles and boxes across, and a struct
  copy shares the array behind a slice. It matters because both functions return the same
  `*doc.Outline` and a caller cannot tell which produced one: `doc.Block.StripMarker` edits
  `Span.Text` in place, so a caller running `layout.Lists` *after* sectionizing would rewrite
  text inside an outline it was holding. `okf` runs inference first and nothing hit this, which
  is why it is now a property of the type rather than of one call site's ordering. Bundle output
  is byte-identical across all 4 untagged documents.
- 13 tests in `sectionize/untagged_test.go` and 2 in `cmd/pdfspec/okf_test.go`, each pinning one
  mutation. The CLI pair exists because 2 of the 3 mutations against `okf.go` — dropping
  `inferRoles`, dropping the guard — initially survived: nothing tested that branch at all.
- `md` output is **byte-identical across all 50 PDFs**, and the 18 bundles the tagged path
  already produced are unchanged modulo their generation timestamp. `markdown.Write` already
  rendered `doc.RoleHeading` with its level, so an untagged file's headings were always levelled
  in `md`; the debt was OKF's alone, and routing `md` through the outline was measured, found to
  change 4 documents' bytes for no correctness gain, and dropped.

- **An untagged table is read from the strokes the page draws.** `extract` collects the page's
  axis-aligned segments into `doc.Page.Rules` and `layout.Tables` infers the grid from them, so
  `testdata/reference/table.pdf` now emits a GFM pipe table and matches `table.gold.md`
  byte-for-byte — which is byte-identical to `tagged-table.gold.md`, the pair those two fixtures
  were built to prove. Everything the table says about that file comes from sixteen rules; it
  declares no `Table` element and no `TH`. Over the 6 untagged documents on disk this finds 9
  tables, 26 rows and 88 cells with no prose misread as a grid.
- `extract/rules.go`: a path accumulator over `m`/`l`/`c`/`h`/`re` and the paint operators.
  Filled rectangles count and are not an edge case — `table.pdf`'s sixteen rules are *all*
  fills, because a hairline is drawn as a thin filled rectangle rather than a stroked line to
  avoid interacting with the device resolution, so an `m`/`l`-only reader finds no grid there
  at all. `W n` contributes nothing, since a clip is not ink and treating one as a rule would
  put a table edge around every clipped image on disk.
- `splitAtRules` divides a fragment at each inferred space a vertical rule runs through. This
  is in `extract` and not in `layout` because a block's spans carry one box each, so a row
  whose cells have merged into one span cannot be taken apart downstream without re-measuring
  glyphs.
- `layout/tables.go`: rows from vertical box overlap, columns from cell x-overlap, both with no
  tolerance anywhere — the numbers compared are glyph extents the extractor measured rather
  than quantities this package computed. A band's extent is narrowed to the intersection of its
  members and never widened to their union, so a tall span cannot drag the row below it into
  the same band.
- 21 tests in `extract/rules_test.go` and 19 in `layout/tables_test.go`, each pinning one
  mutation of the two files. Two mutations that survived the first pass are recorded in the
  Fixed section below, because what they found was a defect in the code rather than a hole in
  the tests.

- **`testdata/reference/tagged-table.pdf`, the yardstick for the tagged table path**, matching
  its gold file byte-for-byte and enforced by `TestReferenceExactMatch`. The corpus cannot be
  one: its 788 tables are asserted by a section total and a block floor, and a count cannot
  tell a correct grid from a transposed one, nor a declared header row from a promoted data
  row. Its gold file is byte-identical to `table.gold.md` deliberately — the same table, one
  declared and one drawn — so when stroke-path extraction lands the untagged path has an exact
  target already known to be reachable.
- Building it needed a requirement beyond the engine note `clauses.tex` records: a plain
  `tabular` under `tagging=on` declares *every* cell a `TD`, so the first build was the
  headerless shape (11 of 788 tables) rather than the one being pinned (773 of 788).
  `tagging-setup={table/header-rows={1}}` with `latex-lab-testphase-table` makes row 1 `TH`;
  `pdfspec probe` reports `TH=3 TD=6` for the committed build against `TD=9` without the key.
  Recorded in `testdata/reference/README.md`, because a fixture nobody can rebuild is an
  assertion nobody can check.

- **A tagged table emits a real Markdown table.** `sink/markdown` renders a GFM pipe table
  from a table's cells, grouped by `doc.Cell.Table`; `sectionize` reads each cell's row,
  column and header from the structure tree. This covers **788 tagged tables, 4650 `TR`,
  11626 `TD` and 5856 `TH`** across the corpus — 745 of the tables in ISO 32000-2 alone —
  every one of which was previously flattened into scattered paragraphs.
- `doc.Cell` (`Table`, `Row`, `Col`, `Header`) as a field on the cell block rather than a
  nested table block. A `doc.Page` is a flat list of blocks and every stage after extraction
  walks it, so nesting would make a block's text reachable by two paths and break the
  invariant the character-conservation tests rest on. A sink regroups instead.
- `sink/markdown/table.go` and 17 tests over it. Grouping is keyed by table number rather
  than adjacency, which is what makes the 13 nested tables on disk work: their cells arrive
  inside the container's run, so consecutive-cell grouping would cut the outer table in two.
  An inner table follows the outer one, the only order GFM can express.

### Fixed — 2026-08-09

- **A wrap space was doubled wherever the producer wrote its word boundaries as anything but
  an ASCII space.** `appendLine` infers a space at a line break unless one is already there,
  and that test read bytes: `' '`, `'\n'`, `'\t'`. Well-Tagged-PDF-WTPDF-1.0.pdf sets *every*
  inter-word gap as U+2002 EN SPACE, so the guard saw none of them and added a second space on
  top — 231 of them, "the understanding  of" and "e.g.,  WCAG" among them — plus 5 more in the
  other direction, where the arriving line began with U+2002 and the leading `strings.HasPrefix`
  missed it. Not only a wrong answer about the page: two trailing spaces in Markdown are a hard
  line break, so the doubling changed how those lines rendered. Both halves of the guard now
  decode the rune and test `unicode.IsSpace`. The space this code *writes* stays an ASCII one,
  since that is its own inference rather than a glyph the page drew. 224 characters left that
  document's Markdown, no line changed in anything but whitespace, no line grew, and the other
  49 PDFs are byte-identical.

  Found while sizing the block-boundary lost-space item in `docs/DESIGN.md`, which is now
  closed as *unreachable* by the same census: across all 50 PDFs there are 398 shared-MCID
  block boundaries, and of the 80 downward-unspaced candidates 79 are ISO 32000-2 mathematics
  where a space would be a new defect — including `𝐷min2𝑛`, which that item named as its
  symptom and which is a subscript. The 1 remaining case was this defect, in the opposite
  direction.
- **An OKF description was cut mid-word in any document whose word boundaries are not ASCII
  spaces.** `firstSentence` looked for a `.` followed by `' '` and then, failing that,
  truncated at the last `' '` — both byte searches. In Well-Tagged-PDF-WTPDF-1.0.pdf, which
  sets every gap as U+2002, the first search found no sentence end anywhere in the document, so
  every description fell through to truncation; the second found no boundary either and cut
  wherever the 300-byte bound landed. 4 descriptions on disk, including "font specifications
  referenced by thes…" with the boundary two characters away. Both are now rune searches
  (`strings.LastIndexFunc`, `unicode.IsSpace`), and those two read `referenced by…` and
  `specified in ISO…`.
- `sectionize.truncate` had the same rule and is fixed with it, though nothing on disk reaches
  it: no title in the corpus is 200 bytes long. Recorded rather than left, because the two
  functions are the same decision written twice and a fix to one of them is a trap in the other.
- **A PDF text string was never PDFDocEncoded, only reinterpreted as UTF-8.** `decodeBytes`
  handled the UTF-16BE case and then did `string(b)`, which is correct for PDFDocEncoding's
  ASCII subset and wrong everywhere else — and the doc comment said so, asserting that "text
  strings in practice do not use" 0x80-0x9F. The corpus disagrees 102 times. Annex D.2 puts
  Latin-1 above 0xA0, so `0xE9` is `eacute` and became a lone invalid byte instead of `é`
  ("Cubic B\xe9zier curves"); it reassigns 0x80-0xA0 to punctuation, so `0x84` is an em dash
  and read as a raw byte ("Table 15 \x84 Entries"); and `0x80` is a bullet, which is what 92
  `/ActualText` entries in Well-Tagged-PDF-WTPDF-1.0.pdf use to say what their list marker
  means. 137 strings across the 50 PDFs on disk decoded wrongly, 4 of them to invalid UTF-8;
  after the fix, 0 and 0.
- The table it needed already existed. `font/encoding`'s `PDFDocEncoding` entry carried the
  comment "Text-string decoding lives in objects.DecodeTextString" while `DecodeTextString`
  never used it — two halves written to meet and never connected. `objects` now imports it,
  which introduces no cycle since `font/encoding` depends only on the standard library.
  Undefined positions fall back to the code point of their own byte, which is what preserves
  tab, newline and the corpus's 192 carriage returns: those have no glyph name, so a bare
  table lookup would delete them.
- This changes no output today. Markdown is byte-identical across all 50 documents, and OKF is
  too once the generation timestamp is excluded — the corrected strings live in `Meta` and in
  `Block.Alt`, and no `Alt` on disk holds an affected byte. It also settles the truncation
  defect below at its root rather than at one consumer: `DecodeTextString` can no longer return
  invalid UTF-8 for any input, which the new 256-byte property test pins.
- **The same truncation could emit invalid UTF-8**, which is the defect underneath the mid-word
  cut and outlives the fix for it. The bound is on bytes, so where no word boundary is close
  enough to move the cut it stays wherever 297 bytes landed, which for multi-byte text is
  inside a rune: 400 bytes of `é` with no space in it returned a description ending in a bare
  `\xc3`, and a YAML value is where that goes. `firstSentence` now backs the cut off to a rune
  boundary the way `sectionize.truncate` already did. The backoff stops on decoded width and
  not on `RuneError` alone, so a U+FFFD the document itself holds is kept — it is a character
  the page draws, not an artifact of the cut, and the two are indistinguishable from the rune
  alone. Found by a review of the fix above and confirmed against the corpus, where it is
  unreachable: all 222 files of the WTPDF bundle are valid UTF-8 with no U+FFFD in them, so
  the two new assertions are the only thing that can catch it.
- **A table cell ignored its own `/ActualText`.** `content` prefers `Alt` over a block's spans,
  because `/ActualText` and `/Alt` are the producer's statement of what the content says where
  the glyphs do not spell it — a ligature drawn as artwork, an image of a word. `sectionize`
  sets it on a `RoleTableCell` like any other block, but `cellText` read only the spans, so a
  TD with `/ActualText` emitted the glyphs the producer went out of its way to correct while
  the same text in a paragraph emitted the correction. Now honoured, with `atStart` false
  rather than true: a cell is inline context, so a leading `-` or `#` in one is a dash or a
  hash and not a list marker or a heading. Unreachable from the corpus — 218 blocks on disk
  carry `Alt` and none of them is a cell — so the two new tests are the only thing that can
  catch it.
- **A pipe inside a monospace table cell broke the row it sat in.** `escapeInto` escapes the
  pipe along with every other Markdown delimiter, but a monospace span routes through
  `writeCode`, which escapes nothing by design — a code span is literal. GFM splits a row
  into cells *before* it parses inline content, though, so a raw pipe in a code span still
  ends the cell: a cell holding `a|b` emitted `` | `a|b` | `second` | ``, four pipes for a
  two-column row, which reads as three cells against a two-column delimiter and **drops
  `second` outright**. Now escaped in `writeCode` under a `cell` flag threaded from
  `cellText`, one backslash per pipe unconditionally, because the row split consumes exactly
  one and the span is literal after that. The escape has to live at this layer rather than in
  a pass over the rendered cell: by then an escape and a backslash the document itself draws
  are the same byte, so a parity check on the rendered text turned a literal `` `a\|b` ``
  into `` `a|b` `` and lost the backslash. Verified against pandoc 3.9 `-f gfm` on all four
  cases — plain and monospace, escaped and literal-backslash — each rendering exactly the
  document's text in two cells. Unreachable from the corpus, so the four pipe tests are the only
  thing that can catch it: 13 table rows on disk hold a code span and none of them holds a
  pipe, while the 25 escaped pipes elsewhere are all outside code. Markdown output is
  byte-identical across all 50 PDFs on disk, and both table fixtures still match their golds.
- **A table's first cell no longer fuses into the last cell of the row above.** `appendLine`
  merges a fragment into the previous span when the style and MCID match, and a table's rows
  match on both, so `table.pdf`'s nine cells arrived as seven spans with "Header C Cell A1" run
  together — a plausible-looking sentence that has silently lost the grid. Every piece of a
  split is now marked apart, including the first.
- **A gap-width filter on the cut candidates was dropping real cell boundaries and is gone.**
  A `WideSpaceFrac` of 2.50 was tried first; `table.pdf`'s header row sets wider cells than its
  body, so its column gaps are 2.400 space widths against the body's 4.128, and the filter
  admitted the body rows while discarding the header. Every inferred space is now a candidate
  and the rule is the only evidence consulted — the gap distribution over all 48757 inferred
  spaces on disk is continuous from 0.25 to 1303 space widths with no empty band, so no
  threshold on that quantity is available at any value.
- **Columns are keyed by cell overlap rather than by which rule split the row**, which was the
  first design and was wrong on two files. `autotagPDFInput.pdf` draws its header row's column
  rule at x=158.88 and its body rows' at 158.94, so an exact key read one table as two, and
  `dotted-gridlines.pdf` draws 2048 verticals of a dotted grid where which one is found first
  depends on the width of the text either side of it.
- **`layout.Tables` was quadratic in a table's row count with nothing bounding it**, found by
  the code review of this change and measured before being fixed. `group` re-clusters a
  candidate run's whole column set for every row it adds, because a column merge is
  retroactive; `maxRules` caps rules at 4096 and does not help, since one page-tall vertical
  splits every band on the page and a stream of many short lines then yields as many two-cell
  rows as it has lines. On a synthetic page of agreeing rows: 4000 rows 0.6s, 8000 rows 3.2s,
  16000 rows 12.3s — unbounded work from one page of an untrusted file. The work is quadratic
  in the run's **cells**, not its rows, so both factors are capped: `maxRunRows = 512` and
  `maxRunCells = 4096`. Capping rows alone was measured to be insufficient — with the row cap
  in place and 512 rows held fixed, 100 columns took 1.3s, 200 took 4.8s and 400 took 14.0s,
  with `agrees` at 75% of profile samples and `columnOf` alone at 20%. Both caps are sized
  against measured ceilings taken over every PDF on disk: the longest run of multi-cell bands
  is **42 rows** (page 888 of ISO 32000-2; next largest 23 and 12), and the largest table any
  document produces is **300 cells**, with the widest single row at 15. Together they take a
  204800-span hostile page from 14.0s to 0.27s and an 819200-span one to 3.3s, linear in span
  count thereafter. Reaching either cap ends the run so the rows past it start a new table —
  every span is still emitted, because truncating would lose text.
- **Prose around a table came back in geometric order rather than the order it was emitted
  in.** `rebuild` flushed a block's non-table spans in the order `bandsOf` found them, which is
  by descending y, so a block whose reading order disagreed with its y order — a footnote
  emitted after the body but drawn above it — had its text silently permuted. Every
  conservation assertion held and the table itself was correct, which is why nothing caught it.
  Non-table spans are now collected as indices into the source block and sorted before being
  emitted. Unreachable from the corpus: over the 743 pages that draw rules, 0 blocks have their
  bands out of y order, so the new test is the only thing anywhere that can catch it.
- **A zero-length-rule filter in `extract.verticals` was unreachable and has been removed**,
  found by mutation testing rather than by review: `paintPath` classifies a segment as vertical
  only when the y delta is not exactly zero, so no content stream can produce one. It was a
  claim about `paintPath` stated in the wrong file. The namesake filter in `layout` stays and is
  now tested, because that package's input is a caller-assembled `doc.Document`.
- `splitAtRules` declines rotated text, and that decision is now pinned by a test rather than
  only by a comment. A gap is measured along the baseline and a rule's position across the
  page; for horizontal text those are the same axis, which is the only reason the two can be
  compared, and for rotated text the comparison still yields a boolean from coordinates in
  different frames.

- **A paragraph inside a table cell no longer detaches the cell's text.** Of 17482 `TD`/`TH`
  elements on disk, **0 hold marked content of their own** — all 17370 non-empty ones wrap
  their text in a `P`, and `gather` detached that `P`, leaving the cell with no spans to be
  dropped by `IsEmpty` while its text reappeared as a free paragraph. This is the
  `LI → LBody → P` defect v0.2.0 fixed, one element name over, and the fix generalizes the
  same transparency (`wrapsText`) rather than adding a second mechanism. It also joins the
  752 multi-`P` cells into one cell each; a cell holding a list (42) or a nested table (13)
  still detaches, because those kids carry block roles of their own.
- `WriteBlocks`, which is `sink/okf`'s entry point, goes through the same grouping. Left as
  it was, an OKF bundle would have emitted cells as paragraphs while the Markdown sink
  emitted a table from the same extraction.
- `TestTableCellsBecomeBlocks` asserted the defect as expected behaviour — its fourth block
  wanted `RoleParagraph` where its own comment said a nested `P` must not duplicate the cell.
  Expectation corrected and position assertions added.
- **A cell that is not one of its row's own kids is declined rather than mispositioned**,
  found by the review of this change. `cellAt` derives the column as an ordinal scan over
  `tr.Kids`; a cell nested deeper was never matched, so the scan ran to the end and left the
  column at the row's full cell count — placing the cell past the end of its own row. All
  17482 cells on disk are direct children and ISO 32000-2 does not require it, so the guard
  is unreachable from the corpus and pinned by a constructed fixture instead. A declined
  cell emits its text as a paragraph, which loses the grid and nothing else.
- ISO 32000-2's block-count floor moves 29000 → 27500 against a measured 29218 → 27517. The
  drop is the 1721 extra `P`s folded into their cells, with the 20-block difference being
  extra paragraphs that were empty and dropped by `IsEmpty` either way. Both
  `TestSectionizeLosesNoText` and `TestOutlineConservesCharacters` hold across the change,
  which is what proves a merge rather than a deletion — a block count cannot tell those apart.

### Documentation — 2026-08-09

- DESIGN.md §10's "Table extraction" bullet said the tagged path "can emit real Markdown
  tables", which was a statement about the format and not about this code: it emitted none.
  Rewritten with the census, the defect, and the two shapes that remain unexpressible — 598
  tables mark a whole first *column* as `TH` to give each row a row-header, and 69 `ColSpan` /
  43 `RowSpan` cells (about 1% of 17482, concentrated in ISO/TS 32005) have no GFM syntax at
  all. The note naming stroke-path extraction as `table`'s blocker is now scoped to that
  fixture, which is untagged, rather than reading as the state of tagged tables.

### Fixed — 2026-08-08

- **An ordered list emits Markdown's ordered syntax, which closes the gap v0.2.0 shipped
  logged.** `sink/markdown` wrote `- 1\. First numbered item.` where the syntax is
  `1. First numbered item.`; it now writes the latter for any label Markdown can express,
  keeping the document's own number. A list starting at 3 is continuing one something
  interrupted, and CommonMark reads only the first item's number anyway, so preserving each
  item's costs nothing and keeps what the page says. `reference/tagged-lists.pdf` matches its
  gold file byte-for-byte and is enforced rather than logged, leaving `table` and
  `text-styles` as the only two fixtures still logged.
- **The measurement is what bounded the fix: none of the corpus's own ordered labels can use
  the new syntax.** All 13 on disk are `[1]`–`[7]` and `a.`/`b.`, against 2022 list items —
  and Markdown's ordered marker is digits then `.` or `)`, so an alphabetic or bracketed label
  written as one would be renumbered to 1 by any parser and lose the label the page drew.
  Those keep the bullet and are written into the line as text, exactly as every label was
  before. The untagged path contributes 0 enumerated markers, per ADR 0011's recorded limit,
  so `tagged-lists` holds the only arabic markers anywhere and without it neither branch would
  be pinned by anything on disk.
- **A bullet list followed by an ordered one is separated by a blank line**, which says what
  CommonMark already does rather than changing it: a change of marker type ends a list, so
  writing the two adjacent only hid the boundary from a reader of the Markdown. `lastList`
  became a kind rather than a bool for this.
- The delimiter is normalized to `.` while the number is preserved, and the asymmetry is
  deliberate: `1)` and `1.` are the same marker to a parser, so the delimiter is syntax and
  carries nothing a reader can act on, where the number is the only part of an ordered label
  that carries information.
- **A nested item indents to the column where its parent's content begins**, rather than two
  spaces per level. Two is right under `- ` and short under `1. `, which is three columns: an
  item indented two there lands inside its parent's marker instead of its text, and CommonMark
  parses it as a *sibling*, so the document's nesting was flattened with nothing reporting it.
  Latent for bullets, where two is correct, and made reachable by the wider ordered marker —
  found by the review of that change. A child of `10. ` now indents four, and a paragraph
  clears the stack because it ends every open list.
- **The blank line at a marker-type change is held to top-level items.** Between two items
  inside an enclosing one it makes that enclosing list loose, and CommonMark then wraps every
  one of its items in a paragraph — a visible change to the whole list in exchange for stating
  a boundary the marker change already establishes.
- **All seven mutations are caught, and two of the rules are pinned only by shapes no document
  supplies.** Never converting, renumbering from 1, and collapsing the two list kinds are each
  caught by a unit test *and* by the fixture. The other four are caught by a test alone, which
  is the debt stated rather than hidden: widening the delimiter set to `]` would convert `1]`
  and silently drop the bracket a page drew, and nothing on disk is shaped that way; and since
  no corpus label is Markdown-expressible, every parent marker on disk is `- `, so all 98
  nested items indent two whichever indent rule runs. The Markdown is byte-identical across
  every document in `docs/` before and after, which is that reasoning confirmed rather than
  assumed.
- Measured directly rather than carried from a previous session's notes: 2022 list items,
  13 enumerated across 9 distinct labels (`[1]`–`[7]` once each, `a.` and `b.` three times
  each), 0 of them expressible in Markdown's ordered syntax, 623 items whose marker is neither
  declared nor drawn, and 98 nested. The `a)`–`f)` labels visible in the output are item text
  rather than markers, which is what that 623 accounts for.

### Documentation — 2026-08-08

- **`reference/text-styles.pdf` is recorded as unreachable rather than pending, which is the
  result of investigating it.** Its emphasis markers are already byte-correct; the whole
  difference against its gold file is four one-line paragraphs arriving as one block. With
  every line set at the same x and one line height apart, `\parindent` cancels — every line
  *is* a first line — so the only signal left is that three of the four end short of the
  measure, by 69.6, 28.6 and 33.6 against 343.1. Scored over all 37 committed testdata PDFs
  plus the 11 corpus documents, "the line before ends short" fires on **16072 of 28231 line
  pairs (57%)** at a tenth of the measure; 0.02 through 0.30 differ by six points, and even at
  four fifths it fires 4735 times. The reason no threshold works is that **nothing on disk is
  justified**: counting lines ending at the page's widest extent, the best real document is
  `mupdf_explored` at 44.1% and the specifications are 5.4%, 7.3% and 13.1%, where justified
  type would sit near 90%. In ragged-right setting a short line is what every line looks like.
  `DESIGN.md` §10 and the `exactFixtures` comment now name this, and name `table`'s blocker as
  the stroke-path extraction `content/lexer.go` tokenizes and nothing consumes — so neither
  logged fixture is unexamined debt.

## [0.2.0] — 2026-08-08

The untagged layout path, and the tagged path's list markers. `docs/DESIGN.md` §10 opened
with four gaps a reference fixture measured rather than remembered — heading rank, paragraph
breaks, list role, table grid — and three of them are closed here; the fourth is the
untagged-table research problem and stays open. Closing the list item is what exposed the
larger defect beside it: the tagged path was emitting `- ■ text` on 1363 items across 6
files, and the fixture written to guard the fix found a second defect that dropped six list
items entirely.

Minor rather than patch because the surface changed: a new `layout` package, `doc.Block.Marker`
with `StripMarker`/`SetMarker`/`Enumerated`, `doc.ListMarker`, and different Markdown bytes out
of `md` for any file with a list. Pin the tag rather than `@latest` — the API is not stable
before 1.0.

### Fixed — 2026-08-08

- **The tagged path's list markers, which closes DESIGN.md §10's largest open item: 1363
  doubled markers → 0.** `sectionize` now reads a list item's declared `/Lbl` (ISO 32000-2
  §14.8.4.5.3) out of the item and into `doc.Block.Marker`, and falls back to the glyph the
  item's text opens with where no label is declared. Per file: ISO 32000-2 1242→0, WTPDF 92→0,
  AN002 15→0, AN003 8→0, PDF-Declarations 23→0, ISO/TS 32001 and 32002 3→0 each.

  Measured against a binary built from the previous commit rather than asserted. Prose is
  intact: alphanumeric word counts identical on every file (ISO 32000-2 396598/396598, WTPDF
  19904/19904), line counts identical, and every one of the 1391 changed lines classified —
  1386 markers removed, 5 ordered labels losing a doubled space, **0 unexplained**.
- **A list item whose body is a wrapped paragraph, found by the new fixture on its first
  run.** LaTeX's tagging writes `LI → LBody → Part → P`, legal under Table 364 and a shape no
  corpus document uses. `gather` detached that `P`, so the item had no spans and `IsEmpty`
  dropped it while the body was emitted as a bare paragraph — six items became six paragraphs
  and the marker each had just been given went with the discarded block. A paragraph is now
  transparent *inside a list item only*: a `Figure` in an `LBody` (the 1 such case on disk)
  still detaches, and a nested list still detaches because its `LI` has a block role of its
  own. Zero change across all 19 corpus files, as the shape census predicted.

### Added — 2026-08-08

- **`doc.Block.Marker`, and `doc/marker.go` to fill it.** A list item's label as a field
  beside its text, which is Docling's arrangement and the reason an ordered label is
  representable at all: a marker left inside the text has to be re-found by every sink using
  an allowlist each would re-derive, and on the one sink that exists it doubles. `Enumerated()`
  is derived from `Marker` rather than stored, so the two cannot disagree.

  The vocabulary lives in `doc` rather than `layout` because both producers need it and
  neither may depend on the other — `sectionize` importing `layout` would invert declared
  structure onto inferred geometry. `layout.stripMarker`/`listMarkers`/`listMarker` (114
  lines) moved there as `Block.StripMarker` and `ListMarker`; `SetMarker` is the declared
  path's separate operation, because taking the label's spans leaves whitespace where the
  marker was in **133 of 147** items and a sink writing its own `- ` renders that as two
  spaces.
- **`testdata/reference/tagged-lists.pdf`, the first fixture covering a tagged list.** Its
  absence is why the defect above shipped: `clauses` is the only other tagged fixture and has
  no lists, `lists` is untagged and exercises the glyph path, so nothing compared a declared
  marker to an expectation. Bulleted *and* numbered, because the two failures differ — a
  dropped bullet reads correctly anyway since Markdown writes one, where a dropped `1.` has
  lost text the document says. Built with `lualatex` for the reason `clauses.tex` records.
  It found a second, independent defect on its first run (above).
- **13 tests in `sectionize/marker_test.go` and 7 in `doc/marker_test.go`**, each verified by
  mutation rather than by passing: every mutation of the new code is caught, except three
  recorded in the code as unmeasurable — no `LI` on disk declares two labels (147 declare
  exactly one, 1915 none), and `ListMarker` requires content after the separator so a strip
  cannot empty a block.

### Changed — 2026-08-08

- **The `/Lbl` figures, re-measured.** The 08-07 entry's `132 / 2 / 1256` came from a probe
  that read spans through `index.take`, which marks them claimed — so the labels it tallied
  had already been consumed. Corrected: of **1407** declared items whose text opens with a
  marker glyph, **121** also declare a `/Lbl` and the label's first rune is that glyph in
  **121 of 121, 0 disagreeing**; **1286** declare no label; **13** declare an ordered one
  (`a.`, `b.`, `[1]`–`[7]`, all in WTPDF); and **14 of 147** `/Lbl` elements declare empty
  text, so "a label exists" is not "a marker is declared". The 121/121 agreement is the
  strongest available check on ADR 0011's glyph allowlist and is recorded there.
- **A declaration outranks a glyph, always.** The declared label is taken whenever it exists
  and the glyph is never consulted then — the same precedence `md.go` states for not running
  `inferRoles` over a structure tree. The glyph rule reaching the tagged path is not that
  case: the block is *already* declared `RoleListItem`, and the only question is which of its
  runes is the label it was declared to have.
- **`sink/markdown` re-emits an ordered label after its bullet**, escaped like any other
  text, and never re-emits a bullet glyph — the `- ` already is one. Markdown has no syntax
  that restates `[1]` or a nested `a.`, so the alternative is dropping a reference the prose
  points at. `TestReferenceExactMatch` logs the remaining gap: an ordered item emits
  `- 1\. text` where Markdown's own ordered syntax is `1. text`.
- **The two character-conservation tests count `Block.Marker`,** which made them stricter
  rather than looser. They failed on the three tagged list-bearing corpus files, and the
  deficit was exactly the markers — WTPDF 124 of 100519, ISO/TS 32001 3, ISO 32000-2 1242,
  each equal to the recorded marker total to the character, with every lost rune a `•` or
  `■`. Nothing was lost; the accounting was reading one of two places a character can now
  live. Counting both makes `TestOutlineConservesCharacters` an exact-sum guard against the
  doubling this whole field exists to stop, and both directions are verified by mutation: a
  marker recorded *and* left in the text comes out +92/+1239 over the document's own total,
  and one stripped but never recorded comes out −92/−1239.

### Added — 2026-08-07

- **The list role on the untagged path, which closes DESIGN.md §10's list item.**
  `layout.Lists` promotes a paragraph whose text opens with a marker glyph followed by
  whitespace, removes the marker — it is structure, not content — and takes the nesting
  depth from the block's left edge, ranked within its run of consecutive marker blocks.
  `reference/lists.pdf` now converts byte-for-byte to its gold file, `- ` at two spaces per
  level, where it emitted a literal `•` and a bold `**–**` before. ADR 0011 carries the
  measurements. Only table grid remains.
- **The population was measured before the rule was designed, and "opens with punctuation"
  is hopeless.** 20125 untagged paragraph blocks across the corpus open with a
  non-alphanumeric rune, and with **190 distinct** ones; the frequent openers are `/` (437,
  from PDF names quoted in prose), `(` (256) and a quote (134). What carries the
  discrimination is the *separator*: `•` opens 1302 blocks and is followed by whitespace in
  **1302 of 1302**, glued in none, while the excluded `-` is glued in **12 of its 13**
  block-initial occurrences because those are command-line flags. With the twelve-glyph
  allowlist and that gate the rule promotes 1442 blocks, of which **5** are not list items
  — all rows of Annex A and D's glyph tables, where a dash is the row's subject. 288:1 in
  favour, the inverse of the ratio that made the lost-space defect not worth a rule.
- **Both obvious guards were implemented, scored against the blocks they rejected, and
  dropped.** A minimum run of two consecutive items costs **136** promotions and reading
  them shows they are overwhelmingly genuine — single-item lists, plus multi-item lists that
  `extract` fused into one block. Rejecting a block whose marker recurs inside it trades
  **33** genuine items for 3 of the 5 table rows. Counting the rejections would have made
  both look cheap; reading them is what settled it.
- **A defect found while measuring, recorded rather than papered over.** The run minimum's
  136 victims include `■ machine-readable text presented in a declared language; ■
  appropriate…` — several list items arriving as one block on `Well-Tagged-PDF-WTPDF-1.0.pdf`
  and `PDF20_AN003`. That is segmentation, not classification, and a role rule that
  declined to promote its victims would hide it. Investigated since — see below.
- **`ListStep` is a statement, not a fitted threshold.** A marker run contains only
  **eight** distinct left-edge gaps corpus-wide, as multiples of type size: six at 0.011
  (float noise, and Annex A rows opening with an em dash and an en dash of different
  widths), one at 0.241 (the same effect, larger), and one at **2.403** — `lists.pdf`'s
  `itemize` nesting. Anything from 0.3 to 2.4 gives identical results everywhere, so the
  default of 1.0 says "nesting indents by about a character" and sits in the middle of an
  empty band. That one positive case is also why `lists` is enforced exactly: a change that
  broke nesting would show up nowhere else on disk.
- **The rule is mutation-tested at 18 of 19 caught, and the survivor is stated rather than
  papered over.** It is `listTiers` taking the run's smallest type size instead of its
  largest — equivalent on everything measured, since the 52 mixed-size runs get identical
  tier counts either way, so no case on disk divides them and the choice is recorded in the
  function's comment as a conservative-end preference rather than a measured one.
- **Three mutations survived earlier passes and all three were real defects.** `listMarker`
  carried an "and content follows" condition that no test could reach — on trimmed text a
  marker followed by whitespace must have something after it, so the clause was
  unreachable; it is gone, and the trim moved inside the function where the invariant it
  rests on is visible. `stripMarker`'s branch for a leading whitespace-only span had no case
  at all: such a block is admitted on a marker further along, and a strip that stopped at
  the first non-empty span left the marker in the text. `tierEpsilon` guarded a comparison
  that needs no tolerance, and is deleted.
- **Review returned zero findings on 291 lines, and sending it back deleted a constant.**
  Pressed for traces on named code paths rather than verdicts, it raised three quantities,
  each of which was then measured: the tier denominator is the run's largest type size, and
  ranking the 52 mixed-size runs by their smallest instead changes the tier count on **0**
  of them; `inferRoles`' Headings-then-Lists order could in principle shift `bodyCluster`,
  which counts runes of span text that `stripMarker` shortens, and run both ways over all
  48 PDFs the two orders agree on every role, level, heading count and body size — **0**
  files differ. The third was a real defect: `tierEpsilon` guarded nothing. A tier value is
  a copy of some block's own `Box.X0` with no arithmetic applied, so the comment claiming
  the tolerance had to survive that arithmetic described arithmetic that does not happen;
  of 1447 tier comparisons, 1404 are exact equality and **0** land within 0.01pt below a
  tier. The constant is gone and the comparison is exact.
- **Paragraph breaks in documents that mark them with nothing but a first-line indent.**
  A book or a specification setting `\parskip` to zero steps down by exactly one line at a
  paragraph boundary, so the vertical test that segments prose cannot see the boundary at
  all and every paragraph on the page arrives fused into one block. `extract` now also
  starts a block where a line *repeats the indent its current block's own first line was
  set with*. `reference/paragraphs.pdf` converts byte-for-byte to its gold file and is
  enforced rather than logged, which closes DESIGN.md §10's paragraph-break item. ADR 0010
  carries the measurements.
- **The two premises this was supposed to rest on were both false, and measuring them is
  what produced the rule.** ADR 0009 recorded that in the same-size case "the only
  remaining evidence is the leading itself" and named `text-styles` as the fixture for it.
  Measured, the leading is *no* evidence: every consecutive line pair in
  `reference/paragraphs.pdf` steps down 11.955pt against a 9.963pt line height — a ratio
  of 1.200 to three decimals — whether the pair is an ordinary wrap or a paragraph
  boundary, so no `ParaFrac` separates them at any value. And `text-styles.pdf` cannot
  discriminate any rule here, because its four paragraphs are one line each: every pair in
  it is a boundary, there is no wrap to contrast against, and a rule that split
  unconditionally would score perfectly on it. ADR 0009's Status now records the
  correction; the decision it made is unaffected.
- **A new reference fixture, because the case was not measurable without one.**
  `reference/paragraphs.tex` sets three paragraphs wrapped over three or four lines at one
  size and one leading, with hyphenation suppressed — left on, LaTeX broke "contains"
  across a line and the extracted text read "con- tains", a real defect but a *different*
  one, and a fixture failing for either reason distinguishes neither.
- **The safety of the rule is structural, not tuned.** An indent by itself fires **441
  times across 19 files** — C source listings in `mupdf_explored.pdf` where the indent is
  syntax, hanging-indented bullets in ISO 32000-2 where the *continuation* is indented and
  the marker line is not. Requiring the incoming indent to match the block's *own* first
  line rejects both by construction, since a bullet's first line sits left of its
  continuations rather than right of them, and takes 441 down to **11**. A spread guard
  declining blocks whose continuation lines disagree on a left edge takes it to **3 across
  2 files**, and exists because of a real regression: `pymupdf/dotted-gridlines.pdf` sets
  centred table headers at 285.53, 282.53, 286.73, 285.65 and 287.45, and against that
  file's 1.335pt space advance the two-point wander cleared the window and split `COMUNI
  SUPERIORI 15.000 abitanti (SUP)` mid-phrase.
- **Both new thresholds were swept against the shipping rule, and they are load-bearing to
  very different degrees** — recorded in `geom.Tolerance` so neither reads as a tuned
  constant. `IndentFrac`'s floor is flat: 0.75 through 3.0 all yield the same 3 extra
  blocks, so it states that an indent under one space width is not one. `IndentMax`'s
  ceiling does real work: unbounded it admits 28 extra blocks and at 6 it admits 3, with a
  plateau across 4 to 10, so 6 sits inside a stable band. What it excludes is placement
  rather than indentation — the rejected offsets run to 17, 63 and 94 space widths and are
  table cells and addresses.
- **Review caught that both half-space tolerances were unpinned, and mutation testing is
  what showed it.** Widening the own-first-line agreement to a vacuous 99 space widths left
  every test passing — the check ADR 0010 calls the whole design had its direction
  constrained and its magnitude not, and made vacuous the rule fires 226 times over the
  corpus instead of 3. Tightening either tolerance to exact equality survived too.
  `TestIndentMatchesTheBlocksOwnFirstLine` closes all three with a hanging-indented block
  where only the own-line comparison declines, plus two near-misses at the scale of
  producer rounding.
- **The last surviving mutation needed a case no corpus could supply.** Replacing
  `observe`'s rebasing `+=` with a plain `max` survived every test and every reference
  fixture, and re-running the driver is what showed it — an earlier version of this entry
  claimed all ten were caught before that was true. The reason it lasted: measured over the
  corpus the two forms disagree on 111 of 30328 indent decisions and the spread guard's
  verdict differs on **none** of them, so no document on disk could have killed it. A fifth
  synthetic case does — a margin walking left in two 0.36 space-width steps whose 0.72
  total clears the guard, where `max` reports one step and admits a block that has no
  margin. All ten mutations of the rule are now caught.
- **Text is conserved, and the test cannot pass by the rule never firing.**
  `TestIndentBreakConservesText` compares page text with the rule on against the same text
  with it off over every PDF present, ignoring whitespace because a boundary change is
  exactly what alters the join-with-a-space behavior: 47 fixtures, 2 with boundaries moved,
  none losing or gaining a character. It also requires that at least one fixture's
  boundaries *move* — the blind spot a conservation test otherwise has. Extending the same
  corpus glob to `TestSizeBreakConservesText` raised its coverage from 8 moved files to 20.

### Added — 2026-08-06

- **`layout`, which levels the headings of a document that declares none.** Untagged
  files got their text and its order right but emitted `**1 First Section**` where the
  document meant `# 1 First Section` — the missing step was "bold, larger" to a level, and
  the reference fixture written to measure it had been logging that gap since it landed.
  `layout.Headings` closes it: the body cluster is the size most of the document's
  *characters* are set in, typographic distinction from it admits a candidate, and the
  candidate's own dotted-decimal section number assigns the level. `headings` now matches
  its gold file byte-for-byte and is enforced rather than logged. Called from `md` and
  `ocr` on the untagged branch only — where a structure tree exists, `sectionize` has
  already read every role from what the producer declared.
- **The rule is derived from the corpus, and the obvious version of it is wrong four
  ways.** `v110-changes.pdf` sets 8.04pt *bold* as 48.8% of its characters — a hair under
  the 49.4% at 9.96pt that wins the body on size — so a weight-implies-heading rule marks
  half of it; arXiv's `2201.00069.pdf` and Adobe's
  `autotagPDFInput.pdf` set headings *plain* and use bold only as inline emphasis (0.3%
  and 0.5% of characters), so a rule requiring bold finds nothing in either;
  `headings.pdf`'s own third level is at *body size*, so a rule requiring a larger size
  loses the deepest level of the fixture written to test depth. Ranking by position in the
  size ladder was measured and rejected separately — `mupdf_explored.pdf` has five
  distinct above-body sizes of which only some are levels, and ladder position disagreed
  with that document's own numbering on 296 of 296 numbered headings. ADR 0008 carries the
  measurements and the two limits that remain.
- **An unnumbered heading stays a paragraph, deliberately.** `dotted-gridlines.pdf` has a
  41-character table row at body size in bold that no typographic signal separates from a
  real heading — not even the space above it, which at 1.68 body-sizes sits inside the
  1.60–1.96 range the reference headings occupy. Promoting "Preface" means promoting that
  row. Lifting this needs a pass that sees the *sequence* rather than a tuned threshold.
- Measured across the fixtures: 296 headings on `mupdf_explored.pdf`, 2 on
  `2201.00069.pdf`, 21 on the untagged `LightOnOCR-2601.14251v1.pdf` paper each at the
  level its own numbering states, and **zero** on every other untagged fixture. The 11 ISO
  documents are byte-identical, being tagged.

### Documented — 2026-08-07

- **Prior art on list segmentation, surveyed while chasing the fusion defect, and the
  survey is a negative result.** No mature extractor uses a marker glyph to decide a block
  boundary: pdfminer.six segments on `LAParams.line_margin` — a fraction of line height,
  the same shape as our `ParaFrac` — with no lexical signal anywhere; pdfplumber wraps it
  and leaves list detection to the caller explicitly; MuPDF's `fz_stext` offers
  `paragraph-break` and `segment` but no bullet option. So `layout.Lists` reading the glyph
  is a step past the field rather than catching up to it, which is only defensible because
  it was scored against the corpus. What *is* worth copying is Docling's data model, which
  keeps `marker` and `enumerated` as fields beside `text` rather than as a prefix of it —
  that is what makes an ordered list representable at all. `oar-ocr` (Apache-2.0, Rust) was
  evaluated and belongs to the OCR path, not this one: ONNX plus downloaded weights puts it
  in the same subprocess category as `llama.cpp`. Recorded in DESIGN.md §5.
- **The fusion defect was measured and does not reach the output; both fixes for it are
  dead.** 98 line pairs across 6 files join a bullet-opening line onto the line before it
  inside `extract`, but exactly one survives to the emitted Markdown — every other affected
  file is tagged, and `sectionize` splits those items from the structure tree first
  (`Well-Tagged-PDF-WTPDF-1.0.pdf` emits 92 separate items, not one block). Geometry cannot
  separate the case: the step before a bullet line spans 1.220–1.486 line heights against
  ordinary wraps' 1.100–1.500 over 41849 pairs, complete overlap, and the bullet's outdent
  from its block margin is **0.000** space widths at the 25th through 90th percentile
  because these producers set the marker flush. Breaking where the marked-content
  identifier changes would cost 6911 splits to buy 8. ADR 0011's rejected run minimum
  therefore stands on its own 136-to-3 arithmetic rather than pending a segmentation fix.
- **A larger, better-evidenced defect on the *tagged* path, which that investigation
  found.** 1363 list items across 6 files render as `- ■ text` — the sink's `- ` followed by
  the marker still sitting in the item's text, 1242 of them in ISO 32000-2 alone. PDF
  declares the marker as its own element (`LI → {Lbl, LBody}`) and `sectionize`'s `blockRole`
  maps neither, so `gather`'s transparent default appends the label's spans to the item
  indistinguishably from its content. Fixed 2026-08-08, below, with the figures re-measured:
  the counts quoted here on 08-07 came from a probe whose span-claiming `take` consumed the
  spans it was reading, so the label texts it tallied were partly empty. DESIGN.md §10 carries
  it, and the previous claim there that the tagged path has no gaps is corrected: `clauses`
  matches exactly, but that fixture has no lists.

### Changed — 2026-08-07

- **`inferHeadings` is now `inferRoles`**, since the untagged branch runs two inferences.
  Headings goes first and the order is load-bearing: both passes consider only
  `RoleParagraph` blocks, so whichever runs second cannot reclassify what the first
  promoted, and a section number the document states outright is stronger evidence than a
  marker glyph. Shared by `md` and `ocr` as before, which
  `TestOCRVerbWithoutModelOnDigitalDocument` holds them to.

### Changed — 2026-08-06

- **A heading no longer carries emphasis that covers all of it.** `# **1 First Section**`
  states the same thing twice — and on the untagged path that emphasis is *why* the block
  was recognized as a heading, since a body-size block is admitted only when it is bold.
  Emphasis *within* a heading is a different claim and survives, so "## The value of
  *Length*" is unaffected; monospace is never treated as emphasis here, because nothing
  promotes a block for being monospaced. Visible only on the untagged path in practice: the
  11 tagged corpus documents are byte-identical, because `sectionize` builds title blocks
  carrying no style at all.
- `pdfspec okf` still refuses untagged input, but its error now says which half landed —
  a bundle needs a tree, and `layout` produces levels rather than one.
- **A block now also breaks where the type size changes, not only where the vertical step
  is large.** A heading set at ordinary leading steps down by exactly one line, so the
  step test joined it to the prose beneath and the heading was resolved as the first words
  of the following paragraph — which is why `autotagPDFInput.pdf` and `v110-changes.pdf`
  had *no* heading candidate for `layout` to promote: no classification rule can recover a
  boundary segmentation never drew. `Tolerance.SizeFrac` is the threshold, at 1.06 because
  that is where the corpus is empty: over the 6,023 line pairs joined on step alone, size
  jitter tops out at a ratio of 1.057 (OCR reporting one line of type at 27 and 28pt) and
  real structure starts at 1.067 (a 32pt title meeting a 30pt subtitle). Compared on each
  line's *dominant* size rather than its largest, so an inline superscript does not make
  every annotated line look like a heading, and never on weight — `text-styles.pdf` sets
  four same-size paragraphs differing only in which word each emphasizes, so a weight test
  breaks blocks at whichever word happens to be bold. Candidate counts went 0→12 on
  `autotagPDFInput.pdf` and 0→6 on `v110-changes.pdf`; neither promotes a heading, because
  all 18 are unnumbered, which is ADR 0008's recorded limit now reached honestly rather
  than masked by a defect a layer down. The test does not apply inside a single
  marked-content element, where the producer has already declared the lines to be one
  thing. Measured over all 47 convertible PDFs: 17 files changed, and with whitespace
  squeezed out 46 of the 47 are character-for-character identical — the 47th gains two
  Markdown emphasis markers and no PDF text. ADR 0009 carries the measurements.
- **Known defect this surfaced and does not fix:** a block boundary falling *inside* one
  marked-content element loses the space that joined its lines, because the wrap space is
  inferred only within a block, and `sectionize` then rejoins same-MCID spans with no
  separator. It predates this change and the marked-content guard above keeps the size test
  from adding to it; ISO 32000-2's `𝐷min 2𝑛` reads `𝐷min2𝑛`. DESIGN.md carries it, along
  with why the obvious fix is wrong: of the 56 cross-block same-MCID adjacencies in the
  corpus, 52 are mathematics — sub- and superscripts, summation limits, displayed equations
  — where inserting a space would be a new defect, and only 4 are genuinely a lost space.
- **A wrapped line's space now sits at the end of the line before it, not the start of the
  line after.** Both ends read the same once the spans are joined, so this looks cosmetic
  and is not: `sectionize` regroups spans in the order a structure element lists its
  content rather than in page order, so a space on a span's *leading* edge travels away
  from the neighbour it was inferred for and lands inside a word somewhere else. Measured
  over the 11 tagged documents, which are the only ones that regroup: "revision" was
  emitted as "re" + "-" + " vision", and "surrounding", "structure", "digest",
  "requirements" and 12 more the same way, while clause numbers ran into the sentence
  before them ("…an ISO 32000-2 document.-5.5.2.3"). 29 such defects fixed, none
  introduced; the 35 untagged files and all 6 enforced reference fixtures are
  byte-identical. Found while measuring the defect above, which it does not fix — where a
  block boundary falls, no space is written at all, so there is none to move.

### Fixed — 2026-08-05

- **Type 3 glyph advances were read as 1/1000 em, which is the one font kind where that is
  not the unit.** §9.6.4 gives a Type 3 font its own glyph space, mapped to text space by
  `/FontMatrix`, where every other kind has that mapping fixed at 1/1000 — and
  `/FontMatrix` was never read. Measured on a pdfTeX document that states the answer in its
  own content stream: at `/FontMatrix [0.00836 …]` the five glyphs of "First" sum to 275.64,
  which the file means as 33.06 text-space units and the stream's own `Td` confirms by
  moving 38.32 to clear the word and the space after it; read as 1/1000 the same run
  advances 3.95, an 8.36× error. Because `extract` infers inter-word spaces by comparing
  measured gaps against these advances, the pen fell a word-width behind on every word and
  every Type 3 run became its own text block. Widths and `/MissingWidth` are now normalized
  into 1/1000 units at load, so `Width` means one thing to every caller. No corpus document
  uses Type 3 for body text — all twelve produce byte-identical Markdown — so this is a
  latent defect fixed on evidence rather than a regression.
- **A Type 3 font could borrow the standard-14 metrics for a code its `/Widths` did not
  cover.** That fallback exists because a simple font may legally omit `/Widths` when it is
  one of the fourteen, but it is wrong twice over here: Helvetica's advance says nothing
  about a glyph that is a content stream in the font's own `/CharProcs`, and it is returned
  in 1/1000 directly while that font's own widths have been scaled out of its `/FontMatrix`
  glyph space — so one uncovered code in a font with a 0.00836 matrix advanced 8.36× its
  neighbours. Reachable, not theoretical: a Type 3 dictionary naming `/BaseFont /Helvetica`
  returned Helvetica's 667. `/MissingWidth` is the entry the specification provides for this
  case, and it scales.
- **A malformed `/FontMatrix` was scaled by anyway.** A shorter-than-six array is not an
  affine transform with a readable first element, but the guard accepted four or five and
  read the scale from it — silently, and confidently. Now exactly six or the 1/1000 default,
  which is what §9.6.4 documents for a missing entry.
- **A document that extracted to nothing said so with an empty file and exit 0.** `md` now
  warns on stderr when every page yielded no text, and names `probe` and `ocr` as the next
  step. Same class as the `okf.Write` guard below and found the same way: a plausible
  artifact, no signal, and in a shell loop over a corpus nothing to distinguish it from a
  clean conversion. Deliberately a warning and not an error — a page holding one image has
  no text, and that is the honest answer.

### Added — 2026-08-05

- **A yardstick: six reference PDFs with hand-written gold Markdown beside them.** Every
  fidelity check before this compared the pipeline to *itself* — counts reconciled, bundles
  round-tripped, escape rates held steady — which detects drift and not being wrong from the
  start. Asked directly whether the Markdown matched what the PDF said, the honest answer was
  that nothing had ever compared the two. `testdata/reference/` now holds one single-concern
  document per feature (headings, text styles, lists, a table, an image-only page, a tagged
  clause hierarchy), each generated from a committed `.tex` and each beside the Markdown it
  should produce. The gold files are written from the source's *intent*, never from any
  reader's output including ours: an Acrobat export would only prove we agree with Acrobat, and
  an export of our own would turn a defect into an assertion. All six are MIT and committed, so
  unlike the sponsored ISO corpus every clone can run these.
- `TestReferenceFidelity` is the tier that must pass: every word the document says, in order,
  and none invented. `TestReferenceExactMatch` reports the remaining distance without failing
  the suite, because part of that distance is a debt we chose to defer (grid table emission,
  DESIGN.md §10) and gating commits on it would mean either paying it now or deleting the test
  that remembers it. A fixture is promoted into the enforced set the first time it matches;
  `image` and `clauses` are there today. What the other four measure is a worklist, and the
  point is that it is measured rather than remembered.
- The manifest now distinguishes a *generated* source from a vendored one. A vendored file
  pins an upstream commit because somebody else set its licence; a file authored here has its
  provenance in this repo's history and has no commit to pin, and a field filled in to satisfy
  a test stops meaning anything. `.tex`, `.gold.md` and `README.md` under `reference/` are
  exempt from hash pinning for a related reason: a gold file is *meant* to change as fidelity
  improves, so a hash would turn every improvement into a manifest edit and teach everyone to
  update it on autopilot. The `.pdf` and `.png` stay pinned — those are opaque, and a byte
  change in one is invisible in review.

### Fixed — 2026-08-05

- **The tagged path was verified against a real document for the first time, and it is
  exact.** `clauses.pdf` extracts byte-for-byte identical to its independently authored gold
  file: five numbered clauses at three depths, each with its body, nothing unplaced. That is
  the path all eleven corpus documents take, and it had only ever been measured against its own
  prior output. No code changed — the point is that the claim is now checked.
- **A note for anyone regenerating the tagged fixture: build it with `lualatex`.** pdfTeX's
  `latex-lab` tagging backend writes the wrong `/MCID` into every `/MCR` — `1` in all ten, for
  a content stream that draws 0 through 9 — so nine of ten structure elements join to nothing.
  The clause titles survive because they come from `/T` and every body paragraph falls out of
  the tree into `Unplaced`, which extracted as five headings with no bodies plus a duplicate
  dump of the page. Diagnosed by comparing the tree's `/MCID` references against the `BDC`
  operators actually in the stream, and ruled out as ours by confirming a single revision with
  no duplicate objects. Kept out of the fixtures deliberately: no reader can tell that file
  from one whose paragraphs are genuinely untagged, so it would pin our behaviour against a
  broken producer.

### Changed — 2026-08-05

- `docs/` is now gitignored by allowlist — everything ignored, Markdown at any depth
  re-admitted — rather than by a list of extensions. The directory holds sponsored ISO
  documents that cannot be redistributed, and an export of one is a derivative work of the
  same content: Acrobat writes XML, HTML, text, RTF, Word, Excel, PowerPoint, CSV, JSON and
  several image formats, so an enumeration reads as complete while leaving the next format to
  be committed by a well-meaning `git add docs/`. Failing closed costs one line when a new
  kind of doc is added; failing open costs a republished ISO document.
- **An OKF bundle could silently drop concept documents.** A parent clause's own document is
  written *inside* its children's directory (`index.md` is reserved and cannot hold prose),
  but that name was chosen against the parent's sibling set and never reserved in the
  children's, whose deduplication is per-directory. A child slugging to the same stem
  overwrote it. On ISO/TS 32004 that lost two real clauses — B.2.2.2 *PDF MAC coverage* and
  B.2.3.3 *External digests* — because a clause with no number and no usable title falls back
  to its position among its siblings, and `section-2` was both the parent's position among its
  own siblings and the child's among its. `Stats` still counted both, so the CLI reported 56
  documents and wrote 54. Found by reconciling the reported counts against files on disk
  across the whole corpus; the other ten bundles were exact.
- `okf.Write` now returns an error when two documents resolve to one path instead of writing
  one over the other and reporting a count the bundle does not contain. The reserved stems
  (`index` everywhere, `log` and `front-matter` at the root) are seeded into the layout's
  name set for the same reason — a clause titled "Index" slugs to `index`, so it collided
  with the navigation file rather than being renamed. No corpus document contains one; it is
  the same defect one step away.

## [0.1.0] — 2026-08-05

First tagged release. Phases 1–4 of `docs/DESIGN.md`: extraction, clause reconstruction,
Markdown and OKF output, image extraction, rasterization, and per-page OCR routing. Pin the
tag rather than `@latest` — the API is not stable before 1.0.

### Fixed — 2026-08-05

- **`version` was a variable nothing set.** It was written to be filled by an `-ldflags -X`
  that no release script exists to pass, so every `go install` reported `0.0.0-dev` — and
  since `version` also stamps each OKF bundle's `generated.by`, a tagged build would have
  written that string into its own output. Both consumers now call `buildVersion()`, which
  reads `debug.ReadBuildInfo`: the tag when installed at one, a pseudo-version otherwise,
  `0.0.0-dev` only from `go run`. The `v` is stripped, since `generated.by` is
  `pdfspec/0.1.0` and the OKF actor form is a name and a version, not a module selector.
  Found while tagging, which is the first thing that would have exposed it.
- The first version of that fix broke the `-X` override it claimed to preserve. Writing
  `var version = buildVersion()` means the linker sets the var's initial value and package
  init then overwrites it, so `-X main.version=9.9.9` built clean and printed the
  pseudo-version. `version` is now an empty var that nothing in Go assigns — the only shape
  the hook works in — and `buildVersion` returns it when set. Measured in all four modes:
  `-X` gives 9.9.9, `go install @v0.1.0` the tag, `go build` a VCS pseudo-version with
  `+dirty`, `go run` the fallback.

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

[Unreleased]: https://github.com/model-harness/pdftools/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/model-harness/pdftools/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/model-harness/pdftools/releases/tag/v0.1.0

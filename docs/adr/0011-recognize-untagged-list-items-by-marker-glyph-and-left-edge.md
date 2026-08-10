# 11. Recognize untagged list items by marker glyph and left edge

Date: 2026-08-07

## Status

Accepted

## Context

A tagged PDF declares its lists: `sectionize` maps `LI` and `TOCI` to
`doc.RoleListItem` and reads the depth off the structure tree's own nesting. Most PDFs
are untagged, and there the list is only drawn. `testdata/reference/lists.pdf` is LaTeX's
`itemize` — three items with two nested under the second — and before this change it came
out as five paragraphs whose text began with the glyph the page draws:

```
• First item.
• Second item.
**–** Nested item under the second.
```

against a gold file that says `- ` with two spaces per level. Both defects are visible
there: the role is not assigned, so the marker survives as text, and the nested items'
indent — plainly present in the geometry — is not read as depth.

The signal is weaker than the one ADR 0008's heading rule runs on. A numbered heading
states its own level; a bullet states only that a producer drew something. So the question
was whether *any* rule here is safe enough to run by default, and the way to answer it was
to measure the population before designing the rule.

**"Opens with punctuation" is hopeless.** Across every PDF on disk, 20125 untagged
paragraph blocks open with a non-alphanumeric rune, and they open with **190 distinct**
ones. The frequent ones are not markers: 437 blocks open with `/` (PDF names quoted in
prose — `/Type`, `/Filter`), 256 with `(`, 134 with a quote. Any character-class rule
promotes thousands of paragraphs.

**A marker glyph plus a separator is a strong signal.** `•` U+2022 opens 1302 blocks and
is followed by whitespace in **1302 of 1302** — glued to its text in none. The separator
requirement is what carries the discrimination, and the excluded `-` shows why: it is
glued in **12 of its 13** block-initial occurrences, because those are command-line flags
(`-o - output file name`) rather than markers.

> **The two figures in the paragraph above count every extracted block on disk, not the
> untagged paragraphs this section is otherwise about, and the ADR is left unedited only
> because an accepted ADR is immutable.** Over the blocks `layout.Lists` actually considers
> the counts are `•` **7**, not 1302, and `-` **11 with none separated**, not 12 of 13. Both
> numbers were correct measurements of the wrong population for the sentence they sit in, and
> the conclusion they support is unaffected — on the untagged path a separated `-` does not
> occur at all, which argues the exclusion more strongly than 12 of 13 does. `doc/marker.go`
> and `TestListMarkerExcludesTheAmbiguousGlyphs` carry the per-glyph split against both
> populations; DESIGN.md's open questions record why the distinction matters, since the
> hyphen's real cost is on the *declared* path where this rule never runs.

**The residue is 5 in 1442, and all 5 are the same thing.** With the allowlist below and
the separator gate, the rule promotes 1442 blocks over the corpus. Reading every ambiguous
one leaves five that are not list items, all rows of ISO 32000-2's Annex A and D glyph
tables, where an em or en dash *is* the row's subject:

```
— 132 0x84 0204 U+2014 EM DASH
```

That is 288:1 in favour of promoting — the inverse of the ratio that made DESIGN.md's
lost-space defect not worth a rule (52 of 56 candidates were mathematics, where the fix
would be a new defect).

**Both obvious guards cost more than they save.** Each was implemented and scored against
the blocks it rejected rather than against a count:

| candidate guard | drops | of which genuine |
|---|---|---|
| require a run of ≥2 consecutive marker blocks | 136 | nearly all — single-item lists, and multi-item lists `extract` fused into one block |
| reject a block whose marker recurs inside it | ~36 | ~33; catches 3 of the 5 table rows |

The run minimum is the more instructive failure. Its 136 rejections include blocks like
`■ machine-readable text presented in a declared language; ■ appropriate…` — several items
that arrived as one block. That is a *segmentation* defect in `extract`, and a role rule
that quietly declines to promote its victims papers over it. The repeat guard trades 33
genuine items for 3 false ones, 11:1 the wrong way.

**The nesting threshold sits in an empty band, not a trough.** Corpus-wide, a marker run
contains only **eight** distinct left-edge gaps, expressed as multiples of the run's type
size: six at **0.011** (float noise, plus Annex A's table where adjacent rows open with an
em dash and an en dash of different widths), one at **0.241** (the same glyph-width
effect, larger), and one at **2.403** — `lists.pdf`'s `itemize` nesting. Any threshold from
roughly 0.3 to 2.4 gives identical results everywhere.

## Decision

`layout.Lists` promotes a paragraph to `doc.RoleListItem` when its text, trimmed, opens
with a rune in `listMarkers` followed by whitespace. It removes the marker and that
whitespace from the spans, and sets `Level` from the block's left edge ranked within its
run.

**The allowlist is twelve glyphs**, each surviving a read of its own corpus occurrences:
`• ‣ ⁃ ■ ▪ ○ ● ◦ – —` plus U+F0B7 and U+F06E. The last two are Private Use Area
codepoints and are not a bug — Symbol and Wingdings have no Unicode mapping for their
bullet, so a producer setting one emits a PUA codepoint and the extractor reports it
faithfully. That is the same glyph-set debt DESIGN.md records for ZapfDingbats.
Deliberately excluded: `*`, `-`, `·`, `>`, all of which occur block-initially and were
something else every time — C code (`*/ fz_stream *…`), flags, and Annex D rows
(`*  asterisk  052  052`).

> **The `·` above is two codepoints, so the excluded set is five glyphs and not four; the ADR
> is left unedited only because an accepted ADR is immutable.** `·` U+00B7 MIDDLE DOT and `˙`
> U+02D9 DOT ABOVE both occur block-initially and separated in Annex D — 3 and 2 times over
> every block on disk — and writing the exclusion as one rendered glyph is what let U+02D9 go
> unasserted: the D.3 row whose text *describes* `U+02D9  DOT ABOVE` opens with U+00B7, so a
> test case read off it pins the wrong character while appearing to cover "the dot". A mutation
> admitting U+02D9 survived a pass that reported every glyph killed.
> `TestListMarkerExcludesTheAmbiguousGlyphs` now asserts both, from rows whose leading glyph is
> its own subject, and `doc/marker.go` names them by codepoint. Neither dot changes any output
> either way, so nothing but that test holds them out. The decision this section records is
> unaffected — both were excluded all along, one of them accidentally.

**The marker leaves the text, here and not in the sink.** Promoting a block is the
statement that its marker is structure rather than content, so it goes with the role.
Leaving it would make every sink re-derive this allowlist to know which leading rune to
drop — the same split `doc.Block` warns against for space inference — and would double it
on the one sink that exists, since `sink/markdown` writes its own `- `.

**Depth is the left edge, ranked within the maximal run of consecutive marker blocks.**
Ranking within the run rather than the page is what stops two unrelated lists in different
columns being read as one nested list. Tiers are built by walking the run's sorted left
edges and opening a new tier at a gap of `ListStep` times the run's largest type size;
`ListStep` defaults to 1.0, which is a statement — nesting indents by about a character —
rather than a fitted value, per the eight-gap measurement above. A negative `ListStep`
disables nesting entirely, for a caller that wants markers removed without trusting the
geometry.

**No run minimum, and no repeat-marker guard**, per the table above. Five glyph-table rows
in 1442 promotions is the accepted cost.

## Consequences

**`lists` matches its gold file exactly and is enforced.** It joins `image`, `clauses`,
`headings` and `paragraphs` in `exactFixtures`, leaving table grid as DESIGN.md §10's only
remaining untagged gap.

**The rule is pinned by mutation: 18 of 19 caught, and the survivor is stated rather than
fixed.** Dropping the separator gate, the length gate or the trim; making the tier
threshold absolute instead of relative to type size; letting a run with no size nest;
ranking tiers page-wide; adding the rejected run minimum; skipping the level clamp, the
marker strip or the paragraph-only gate; taking the first matching tier instead of the
last; comparing an edge with strict `>`; stopping the strip at the marker's own span;
narrowing it to a byte cutset; removing its whitespace-span branch; and either sign of
`ListStep` meaning the wrong thing — each is caught by `layout/lists_test.go` or
`TestReferenceExactMatch`.

The one survivor is `listTiers` taking the run's **smallest** size instead of its largest,
and it survives because the two are equivalent on everything measured: the 52 mixed-size
runs get identical tier counts either way, so no case on disk divides them and none was
built, unlike ADR 0010's synthetic margin walk. The choice is recorded in the function's
comment as a conservative-end preference rather than a measured one, which is the honest
description of it.

Three mutations survived earlier passes and all three were real, which is the same lesson
ADR 0010 closed with. `listMarker` carried an "and content follows" condition that no test
could reach: on trimmed text a marker followed by whitespace *must* have something after
it, so the clause was unreachable and is gone, with the trim moved inside the function
where the invariant it depends on is visible. `stripMarker`'s branch for a leading
whitespace-only span had no case at all — such a block is admitted on a marker that lives
further along, and a strip that gave up at the first non-empty span would leave the marker
in the text. And `tierEpsilon` guarded a comparison that needs no tolerance; it is deleted,
per the review notes below.

**An ordered list is not recognized.** A numbered item is a paragraph opening with a
number, which is also what a numbered heading is, and what a table row is. Nothing on
disk separates them, and inventing a rule here would collide with ADR 0008's, which reads
exactly that leading number as a heading's level.

> **Closed, 2026-08-10.** The objection holds for a *single* block and is what
> `layout.OrderedLists` is built around: it promotes nothing on one numbered paragraph. What
> separates an item from a heading is a **consecutive incrementing run at one left edge** —
> `1.` then `2.` then `3.`, same label form, same margin — which is evidence no single block
> carries and which neither a heading nor a table row makes accidentally. The delimiter is
> required, so `7.4 Filters` and `1 Scope` are not candidates at all; a dotted clause number
> has no single value to increment and therefore cannot form a run, which is the collision
> with ADR 0008 resolved structurally rather than by tolerance. Precedence does the rest:
> `Tables` and `Headings` run first in `inferRoles`, so anything they promoted is no longer
> `RoleParagraph` and cannot be reconsidered.
>
> Measured over all 50 documents: 70 runs of 260 items, forms `a)` 174, `n.` 43,
> `[n]` 21, `n)` 17, `a.` 5, run lengths 2–11 with 25 runs of exactly 2. The label vocabulary
> is those five forms and no more: `(1)` and `(a)` look like obvious siblings of `[1]`, were
> written, measured at **zero occurrences**, and removed — a removal proven output-neutral on
> all 50 documents, which is the proof they were unreachable. Reading every run, 4 are tables of contents —
> the only false positives on disk, and all 4 sit in *tagged* files where `inferRoles` never
> runs. On the untagged path the pass actually serves, the effect is **5 runs of 25 items,
> all in `mupdf_explored.pdf`, all genuine**, plus 3 lone numbered paragraphs correctly left
> as prose. Validated against producers' own declarations, this ADR's own standard: of the
> tagged list items declaring a `/Lbl` that holds an ordered label, `doc.OrderedLabel` reads
> the same form off the item's text in **16 of 16**, disagreeing in 0.
>
> A table-of-contents guard (a page-number tail) was measured and would catch 3 of the 4 at
> zero cost to a genuine item. It is not shipped: it cannot fire on any file this pass runs
> on, and untestable code is worse than a recorded measurement.
>
> The one limitation worth recording is that a run crossing a page break splits into two,
> because the loop is per page. Measured, not assumed: 5 such continuations exist in the
> corpus and all 5 are in tagged documents, so 0 reach this pass. Joining them means carrying
> run state between pages, which no inference pass keeps; each item carries its own label, so
> the split costs a sink nothing until one renumbers from the first item.
>
> `sameEdge`'s half-point tolerance is measured on the same standard as `ListStep` below. Of
> the 192 adjacent block pairs that agree on label form and increment by one, 180 have
> left-edge gaps of exactly 0 and 10 are below 0.1pt, then nothing until 2.18pt — so every
> value in [0.1, 2.1) separates the two populations identically and 0.5 is the middle of an
> empty band.
>
> Note the asymmetry with the run minimum **rejected** above for bullets. There, requiring
> two consecutive items cost 136 genuine promotions to catch about 3 strays. Here the run is
> not a guard on top of the evidence — it *is* the evidence, so there is no promotion without
> it. The two rules differ because a bullet glyph means something by itself and a number does
> not.

**Nesting rests on one document.** `lists.pdf`'s 2.403 is the only genuine left-edge gap
inside a marker run anywhere on disk, which is why that fixture is enforced exactly: a
change that broke nesting would show up nowhere else, and the corpus tests would stay
green. It is also why `ListStep` is stated rather than tuned — with one positive case,
fitting a threshold to it would be fitting to noise.

**Review's three surviving questions were all about quantities that turned out not to
matter, and answering them by measurement deleted one of them.** A first review pass
returned zero findings, which for a change this size is a result to send back rather than
accept; pressed for traces on named code paths, it raised three:

- *The tier denominator is the run's **largest** type size, which a producer setting nested
  items smaller could defeat* — a 14pt-derived threshold is wider than a 9pt one. Real
  shape: 52 of the corpus's 524 marker runs mix sizes, by up to 2.5×. Ranking those 52 by
  their smallest size instead changes the tier count on **0** of them. Largest is kept for
  being the conservative end — it under-reports nesting rather than inventing a level.
- *`tierEpsilon` was absorbing nothing.* Correct, and it is now deleted. A tier value is a
  copy of some block's own `Box.X0` with no arithmetic applied, so the comment claiming the
  tolerance had to "survive the arithmetic that produced both" was describing arithmetic
  that does not happen. Of 1447 tier comparisons over the corpus, 1404 are exact equality
  and **0** fall within 0.01pt below a tier. The comparison is exact now.
- *Running `Lists` before `Headings` could shift `bodyCluster`*, which ranks sizes by rune
  count over span text that `stripMarker` shortens. Run both ways over all 48 PDFs, the two
  orders agree on every block's role and level, every heading count and every measured body
  size — **0** files differ. So `inferRoles`' ordering is a statement about which evidence
  outranks which, not a dependency, and `md.go` says so.

**The strip is the only place in `layout` that edits a block's text** rather than its
role, which is why it is confined to the rune `listMarker` matched and the whitespace
after it, and why it empties a span rather than removing it — span indices a caller holds
stay valid, and `Span.MCID` survives for diagnosis.

**It found a defect it must not paper over — and the defect turned out to be somewhere
else.** Measuring the run minimum surfaced blocks where `extract` fused several `■`-marked
items into one, on `Well-Tagged-PDF-WTPDF-1.0.pdf` and `PDF20_AN003`, and that is recorded
in `Lists`' own comment so the next person to reach for a run minimum sees what it would be
hiding. Investigated afterwards, the fusion is real inside `extract` — 98 line pairs across
6 files — but reaches the emitted output in exactly one place on disk, because every
affected file is tagged and `sectionize` splits those items from the structure tree first.
Neither candidate fix survives measurement: the step before a bullet line (1.220–1.486 line
heights) overlaps ordinary wraps (1.100–1.500) completely, the bullet's left edge is flush
with the block margin at the 25th through 90th percentile, and breaking on a marked-content
change would cost 6911 splits to buy 8. So the run minimum stays rejected on its own merits
rather than pending a segmentation fix.

What the investigation did find is a larger defect on the *tagged* path: 1363 items across
6 files emitted their marker glyph literally, because `sectionize` treated `Lbl` as
transparent and appended the label's spans to the item's text. Since fixed, and the fix
supplies the strongest check this ADR's allowlist has: of the 1407 declared list items whose
text opens with one of these glyphs, 121 also declare a `Lbl` saying what their label is, and
the label's first rune is that glyph in **121 of 121, with 0 disagreeing**. A producer's own
declaration agrees with the allowlist everywhere the two can be compared.

It also reaches what this rule cannot. 13 of the declared labels are ordered — `a.`, `b.`,
`[1]`–`[7]` — which is the case ruled out above, and 11 more are a bold Wingdings square
*glued* to its text, which the separator requirement rejects. Neither is a reason to loosen
this rule: those items are reachable because a producer declared them, not because a glyph
was guessed at. The declaration is taken whenever it exists and the glyph is never consulted
then. DESIGN.md §10 records the result, and the marker vocabulary now lives in `doc` so both
paths read one copy of it.

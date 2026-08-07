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

**It found a defect it must not paper over.** Measuring the run minimum surfaced blocks
where `extract` fused several `■`-marked items into one, on `Well-Tagged-PDF-WTPDF-1.0.pdf`
and `PDF20_AN003`. That is block segmentation, not classification, and it is recorded in
`Lists`' own comment so the next person to reach for a run minimum sees what it would be
hiding.

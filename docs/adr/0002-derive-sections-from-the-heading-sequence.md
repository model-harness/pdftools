# 2. Derive sections from the heading sequence, not from container nesting

Date: 2026-08-03

## Status

Accepted

## Context

`docs/DESIGN.md` originally described a tagged clause as "one contiguous subtree": find the
`Sect` (or `Div`, or `Art`) elements in the structure tree, and each one is a section whose
body is its descendants. That is what ISO 32000-2 §14.8.4 describes, it is what the standard's
own examples show, and it is what a reader of the specification would implement.

Measuring the corpus says otherwise. In ISO 32000-2 itself:

- 7 `Sect` elements, against **981 headings**.
- One `Part` element with **13,442 direct children**, a flat `H1 P P P P H2 P P …` stream.
- **966 of the 981 headings have no element children at all.**

So a container-driven implementation emits 7 sections from a 1,023-page standard, and the
failure is silent: the output is a well-formed outline with plausible titles that happens to
be missing 974 clauses. WTPDF and the two ISO technical specifications show the same shape at
smaller scale. The specification's own document, produced by the organization that wrote the
specification, does not nest its clauses in containers.

A second measurement constrains how a heading's *text* is found. Not one of ISO 32000-2's 981
headings carries a non-empty `/T`, and neither does one of WTPDF's 183. The attribute exists
for exactly this purpose and no real producer populates it, so a reader trusting it produces
an outline of untitled sections — and a clause-per-file OKF bundle cannot name a file for a
section with no title.

## Decision

**A section boundary is a heading, and a clause's body is its heading's following siblings.**
The hierarchy is a level stack over the linear heading sequence: a heading at level *n* closes
every open section at level ≥ *n* and opens a new one, and content between two headings belongs
to the earlier of them. Containers (`Document`, `Part`, `Sect`, `Div`, `L`, `Table`, `TOC`) are
walked for their children and hold no text of their own; inline elements (`Span`, `Link`,
`Note`) do not split a paragraph; unrecognized roles are transparent.

**Titles come from content, joined on `(page, MCID)`, and the join is span-level.**
`doc.Span` carries an `MCID` and `extract` will not merge two spans that disagree on it. This
is not an optimization — the extractor's paragraph heuristic merges a heading line with the
body line after it when they share style and spacing, so a block-level join over-captures:
12.0% of WTPDF's headings became heading-plus-definition, and the specification's worst case
was a 518-character "title". `/T` is consulted first and is always empty; `/ActualText` is
consulted last, because it substitutes for what the glyphs spell and the glyphs are what a
reader checking the conversion against the page will see.

**Text no section claims is kept, unattributed.** `doc.Outline.Unplaced` holds it and the
Markdown sink emits it last behind an HTML comment. ISO 32000-2 draws the whole of clause 1
outside any marked-content sequence, so nothing in the structure tree names it — and a
standard's Scope clause is not optional. The two alternatives are both worse: dropping it loses
a normative clause, and attaching it to the nearest preceding section files the Scope under
"0.4 Changes introduced in ISO 32000-2:2020". A wrong attribution in a bundle a model later
reads as fact is worse than an absent one.

## Consequences

The measured result on the tagged path, which is the acceptance bar `TestSectionizeCorpus`
pins exactly:

| file | sections | titled | numbered | max level | blocks | unplaced |
|---|---|---|---|---|---|---|
| WTPDF 1.0 | 183 | 183 | 173 | 6 | 943 | 6 chars (0.004%) |
| ISO/TS 32001 | 14 | 14 | 10 | 3 | 115 | 0 |
| ISO/TS 32005 | 27 | 27 | 23 | 4 | 692 | 0 |
| ISO 32000-2 | 981 | 981 | 851 | 5 | 29,218 | 5,734 chars (0.231%) |

Zero characters lost on all four, and placed plus unplaced equals the document's own character
count *exactly* — so a block a section claims only part of is rebuilt from its unclaimed spans
rather than repeated whole.

**The same builder serves the untagged path.** This is the consequence that matters most for
the roadmap. A level stack over a linear heading sequence needs only a sequence of
`(level, text, body)` triples, and where those come from is the tagged path's business or the
layout heuristics' business, not the builder's. Had sections been derived from containers, the
untagged path would have needed a second, unrelated algorithm — because layout analysis can
recognize a heading by size and weight but has no containers to find.

**Wrong heading levels produce wrong nesting, with no recovery.** The stack trusts the
declared rank. A producer that jumps `H1` to `H4` yields a level-4 child of a level-1 parent,
which is what the document says and is asserted as well-formed rather than repaired: inferring
the "intended" level would mean overriding a producer who may have meant it.

**`tag.Elem.Content []MCRef` is required, and replaced `MCIDs []int`.** An MCR's own `/Pg` is
authoritative over its element's, and that is precisely how one paragraph continues across a
page break. Flattening references to a bare ID list discards it; measured, 5 of WTPDF's 2,035
references are MCRs and all 5 name a page other than their element's, and those 5 were exactly
its unplaced text. The API change is the cost of reading them.

**A clause number is a separate field, not something recovered from the title.**
`doc.Section.Number` is populated only from a leading all-digit token, so "Annex A" and
"A.1 General" have none — an annex is lettered and its subsections are not in the same
sequence as the numbered clauses, so admitting "A.1" would sort it among them. 851 of 981
sections are numbered; the rest are annexes, the foreword, and the bibliography.

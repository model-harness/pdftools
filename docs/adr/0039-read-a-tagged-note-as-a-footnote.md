# 39. Read a tagged note as a footnote

Date: 2026-10-20

## Status

Accepted. Closes issue #20.

## Context

ADR 0036 made an untagged page's footnotes GFM footnotes. A tagged document declares its notes in the structure tree instead: 25 `Note` elements across five standards on disk. Sectionize treated `Note` as inline content, and the inner paragraph made it a block of its own. So each note came out right after the paragraph citing it, with its label kept as text, and its mark was glued to the word before:

```
IETF RFC 5480:2009, *Elliptic Curve Cryptography Subject Public Key Information*1)

1\)  https:// datatracker .ietf .org/ doc/ html/ rfc5480
```

The spaces inside that URL are the source's own: the producer drew them as separate space spans. They are left as the page has them.

## Decision

**`sectionize.readNotes`** reads the tree before it is built. The mark is gathered into the citing paragraph before `visit` reaches its `Note`, so it has to be known first. Each `Note` qualifies when two things hold:

- **A label.** Its text opens with one and has text after it: up to three digits, with `)`, `.` or nothing after them, or up to three of `* † ‡ § ¶ ‖`.
- **A mark.** The label must appear beside the note, in one of two ways:
  - **an element before it in its parent whose text is the label**, the nearest one. Four of the standards set this shape: `P > Reference > Span "1)"`, then `Note > P "1)  https://…"`.
  - **failing that, a span saying the label in a smaller size than the span before it**, in the nearest earlier sibling. ISO 32000-2 sets its four notes this way. Each is a sibling of the paragraphs at the `Part` level, and its mark is a raised digit inside a sentence, sharing that sentence's MCID. So a mark is held by its span, not by its marked content. The size bound is layout's 0.8.

A `Note` that qualifies is emitted after the text citing it, as a `RoleFootnote` with its label taken off its text. Its mark's spans get `Span.Note`. The sink writes both as ADR 0036 set out.

**`doc.Block.Note`** is new, and ADR 0036 is corrected by it. ADR 0036 put a footnote's GFM label in `Marker`, but `Marker` is page text, and the conservation tests count it as such. A label is an identifier, so the two split:

- **`Note`** is `1` for a mark set `1)`, `fn1` for `*`, and `1-2` for a document's second note numbered 1. Mark spans and the sink use this.
- **`Marker`** is what the page drew: `1)`, `*`, `1`.

The untagged pass now fills both. The sink writes definitions from `Note`.

**The accounting.** `TestMDOutlineConservesText` compares the outline's md with the flat md. A footnote's id is markup written twice, at the reference and at the definition, where the page drew its mark. It adds what the id has over the mark at each: nothing for `1)` written as `1`, and two digits for ISO 32000-2's `1-2`. The test states that, and the figure is exact: 9,526 letters and digits gained, which is 9,524 from substitution plus 2 in ids.

## Acceptance

All 25 notes are definitions, each linked from its mark, as counted by `pandoc -f gfm`:

| document | notes |
|---|---|
| ISO/TS 32002 | 10 |
| ISO/TS 32004 | 8 |
| ISO 32000-2 | 4 |
| ISO/TS 32001 | 2 |
| ISO/TS 32003 | 1 |

ISO 32000-2's marks fall where they should: after `PostScript®`, `Adobe Systems Incorporated`, a NIST reference and the ETSI title. Its producer tagged note 4 inside note 3's element, so the two share one definition. `footnotes.pdf` still equals its gold, and the untagged corpus did not move.

**The tests.** `notes_test.go` covers:

- both shapes;
- the size bound, at it and just past it;
- a note with no label, a word for a label, only a label, or no mark;
- numbered, repeated and symbol labels, and the label pattern;
- the nearest of two marks;
- `stripNote` across spans.

The layout tests check each footnote's `Note` and `Marker` separately.

## Mutation

25 mutants, all killed by named tests. The first run left 4 alive.

- **Two were guards that could not fire, and were deleted:**
  - a check for a `Note` with no parent. `tag.Read`'s root is the structure-tree root, so every `Note` has one.
  - a rule skipping an earlier `Note` as a mark. It could matter only for a note whose whole text is its label, which no longer qualifies.
- **One was a missing case:** a note that opens with an ordinary word, beside a sibling saying the same word.
- **One showed a defect, now fixed:** a note that is only its label became an empty footnote, which the outline drops. Its mark would then have pointed at nothing, which GFM writes as text. A label now needs text after it.

## Consequences

- Tagged notes are GFM footnotes, linked from their marks.
- A note's label as the page drew it is kept in `Marker`, so nothing the page says is lost to an identifier.
- A note whose mark is neither a sibling element nor a raised span is left where it was.

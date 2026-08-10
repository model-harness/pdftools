# 12. Decode a text string through Annex D.2

Date: 2026-08-10

## Status

Accepted. The decision it reverses was not a bug in the ordinary sense — it was an
assumption written down as a fact in a doc comment and then relied on for four phases,
which is why it gets an ADR rather than a changelog line.

## Context

A PDF *text string* (ISO 32000-2 §7.9.2.1) is one of exactly two things, and which one is
signalled by its first two bytes: UTF-16BE if a `FEFF` byte-order mark opens it,
**PDFDocEncoded** otherwise. PDFDocEncoding is defined in Annex D.2. It is not Latin-1,
it is not UTF-8, and it is not ASCII beyond `0x7F`.

`objects.DecodeTextString` handled the BOM case correctly from the first phase, and then
did this with the other one:

```go
return string(b)
```

That reinterprets the bytes as UTF-8. It is right for the ASCII subset and wrong for every
byte above it. The function's own doc comment stated the justification:

> text strings in practice do not use 0x80-0x9F

**That sentence was an assumption presented as a measurement, and it is false on this
corpus.** Nothing had measured it. It survived four phases because the strings it governs
— `/Title`, `/Lang`, `/ActualText`, `/Alt`, `/Lbl`, an annotation's `/Contents` — are
mostly ASCII, so the failure is invisible until a document uses the range the comment
claimed was unused.

### What the corpus says

Measured over the 49 readable PDFs on disk (a 50th, `zeroLength.pdf`, is empty by
design), across the 22341 text strings reachable from the catalog, trailer and page
dictionaries:

**144 BOM-less strings decode differently through Annex D.2 than as raw bytes**, and
**142 of them were invalid UTF-8** under the old reading. That near-identity is not a
coincidence and is worth stating, because it is what makes the defect a correctness
problem rather than a cosmetic one: a lone byte in `0x80`–`0xFF` is never a well-formed
UTF-8 sequence, so almost every affected string was not merely *wrong* — it was not a Go
string that could be safely written anywhere. Exactly 2 differ while staying valid UTF-8,
and they are the two holding `0x18` and `0x19`: those are C0 control codes, valid UTF-8 on
their own, which Annex D reassigns to the accents `˘` and `ˇ`. Through the table, **0 of
the 144 are invalid UTF-8**.

The 11 distinct byte values responsible, with occurrences — 155 in total across the 144
strings, since a string may hold more than one:

| Byte | Annex D.2 says | Occurrences |
|---|---|---|
| `0x80` | `•` bullet | 92 |
| `0x84` | `—` em dash | 25 |
| `0x90` | `’` right single quote | 13 |
| `0x83` | `…` ellipsis | 8 |
| `0xE9` | `é` e-acute | 8 |
| `0x85` | `–` en dash | 2 |
| `0x8D` | `“` left double quote | 2 |
| `0x8E` | `”` right double quote | 2 |
| `0x8A` | `−` minus | 1 |
| `0x18` | `˘` breve | 1 |
| `0x19` | `ˇ` caron | 1 |

Three things in that table are the whole argument:

- **`0x80` is a bullet, and 92 of the 144 are `/ActualText`.** All 92 are in
  `Well-Tagged-PDF-WTPDF-1.0.pdf`, where a producer went out of its way to declare what
  each list marker *means*. Under the old reading that declaration decoded to a C1 control
  code. The producer's statement about its own content was discarded and replaced with a
  byte no consumer can use.
- **Annex D reassigns `0x80`–`0xA0` away from both ASCII and Latin-1**, to punctuation.
  This is the range the deleted comment claimed was unused; 145 of the 155 affected byte
  occurrences fall in it.
- **Above `0xA0` the encoding is Latin-1**, so `0xE9` is `é`. `Cubic Bézier curves` came
  out with a lone `0xE9` in it.

By file: `Well-Tagged-PDF-WTPDF-1.0.pdf` 93, `ISO_32000-2_sponsored_EC3.pdf` 50,
`PDF20_AN002-AF.pdf` 1. By key: `/ActualText` 92, an annotation's `/Contents` 36, `/RC`
14, `/Title` 2.

### The risk on the other side

The reading being replaced was not arbitrary. A producer that writes **UTF-8 with no BOM**,
in defiance of Annex D, is a real thing in the wild, and `string(b)` gets such a producer
right by accident while the table mangles it. So the question is not "which reading is
correct" — the specification settles that — but "which reading loses less on real files."

Measured: of the **142 BOM-less strings holding any byte above `0x7F`**, **0 are
well-formed multi-byte UTF-8**. A UTF-8-writing producer leaves valid multi-byte
sequences behind; not one string on disk looks like that. The trade is therefore paid at
zero cost on this corpus, and the specification wins where the corpus is silent.

## Decision

**A BOM-less text string is decoded one byte at a time through Annex D.2's table.**

- **The table is `font/encoding`'s existing `PDFDocEncoding` entry, not a new one.** That
  entry already existed and carried the comment "Text-string decoding lives in
  `objects.DecodeTextString`" — while `DecodeTextString` never used it. Two halves written
  to meet and never connected. `objects` now imports `font/encoding`, which introduces no
  cycle: that package depends only on the standard library.
- **A position the encoding leaves undefined decodes as the code point of its own byte.**
  This is what the table already says everywhere it is defined outside the two reassigned
  ranges, and it is load-bearing rather than a fallback for tidiness: tab, newline and
  carriage return have no *glyph name*, so a bare table lookup treats them as empty and
  **deletes** them. 202 carriage returns in the corpus decode through this path, and where
  they are is worth knowing: all 202 sit in annotation `/Contents` in
  `ISO_32000-2_sponsored_EC3.pdf`, and **0 are under a metadata key**. So the fallback is
  load-bearing for markup text and invisible to `Doc.Meta` — a reviewer looking for its
  effect in a `/Title` will find nothing and should not conclude the branch is dead.
- **Annex D is followed where it surprises.** `0xA0` is the Euro sign and not NO-BREAK
  SPACE; `0xAD` is a hyphen and not SOFT HYPHEN. Both are the specification's, both look
  like errors, and **neither occurs in the corpus** — measured at 0 and 0. They are
  followed anyway, because an exception list for cases no file exercises is this code
  overruling ISO on evidence it does not have.
- **The BOM check keeps precedence.** UTF-16BE is selected first, so `0x80` inside a
  UTF-16BE string is a code unit and never a table index.

## Consequences

**A decoded text string is now always valid UTF-8, for any input.** That is a property,
not an observation, and it is what `TestDecodeTextStringIsAlwaysValidUTF8` pins by
decoding all 256 byte values at once: each byte stands for at least one rune, so nothing
is dropped and nothing is malformed. This matters beyond correctness of the text — these
strings end up in YAML values in an OKF bundle, where a raw byte is a parse error, and it
settles that class of defect at the root rather than at each consumer.

**No output changes today, and that is the honest report rather than a disappointing one.**
Markdown is byte-identical across all 50 documents; OKF is identical once the generation
timestamp is excluded. The corrected strings live in `Document.Meta` and `Block.Alt`, and
no `Alt` on disk holds an affected byte. The fix is to what the library *returns*, and the
94 differing strings under metadata keys are visible to any consumer that reads `Meta`
even though the two shipped sinks do not surface them.

**The 92 `/ActualText` bullets are now readable, which unblocks a rule this repo does not
yet have.** A producer declaring `•` as its marker is stating what ADR 0011's glyph
allowlist has to infer from geometry. Nothing consumes that yet, and this ADR does not
add a consumer — but the declaration is no longer destroyed on the way in.

**Every figure recorded when the change first landed is superseded here, and all of them
in the same direction.** The 2026-08-09 changelog entry says "137 strings … 4 of them to
invalid UTF-8"; the code's own doc comments said 137, 103 and 192. Re-measured
deterministically, those are **144 differing, 142 holding a high byte, 202 carriage
returns**. Three of the four are undercounts by construction — the original walk marked a
`Ref` seen and skipped it, so a string was attributed to whichever path reached it first
and Go's map order decided which paths ran at all. A walk like that misses objects, and it
misses different ones each run.

The "4 invalid" is not an undercount but an impossibility, and saying so is the point: a
lone byte above `0x7F` is never a well-formed UTF-8 sequence, so under *any* population
the invalid count has to sit just under the differing count, never at 3% of it. That is
arithmetic, not measurement, and it is what proved the figure wrong before a probe was
written. The doc comments are corrected in place and point here; the changelog line is
left alone, because a changelog records what was believed at the time.

**Measurement method, recorded because it bit twice.** The first two probes were
non-deterministic: a recursive walk that marks a `Ref` seen and skips it thereafter
attributes each string to whichever *path* reached it first, and Go randomizes map
iteration order, so identical code reported 21002, then 15318, then 12877 strings. A depth
bound made it worse by truncating differently per run. The shipping probe is two passes —
an unbounded worklist collecting reachable `Ref`s (dedupe by `Ref` terminates it; the
reference graph is finite), then one visit per resolved object reading only its own direct
entries. Three consecutive runs agree exactly. **A corpus figure from an order-dependent
traversal is not a measurement**, and the tell is that it moves when nothing does.

**What this does not settle.** A `/ToUnicode`-less symbolic font still decodes through the
glyph-name path, which is a different mechanism with its own gap (DESIGN.md records the
ZapfDingbats and Symbol PUA debt). And this ADR governs *text strings* only: a byte string
— `/ID`, a signature's `/Contents` — must not be decoded at all, and the type-directed
call sites are what keep the two apart.

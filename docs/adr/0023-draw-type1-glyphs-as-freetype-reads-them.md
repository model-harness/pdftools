# 23. Draw Type 1 glyphs as FreeType reads them

Date: 2026-09-28

## Status

Accepted. Amends [ADR 0017](0017-substitute-a-face-behind-a-caller-supplied-source.md), which
refused every FontFile program for want of an interpreter, and
[ADR 0022](0022-draw-cff-glyphs-and-route-a-code-as-pdfium-does.md), whose glyph selection for
CFF now serves Type 1 as well.

## Context

ADR 0022 left Type 1 as the largest blocker: 17 pages, 16 of them blocked by nothing else. All
are in one document, and a census of its FontFile programs set the scope:

| what | programs |
|---|---|
| a PFA program with a binary eexec part and no trailer | 20 |
| `/Length1` equal to the offset of the first cipher byte | 20 |
| a built-in encoding as an array | 20 |
| `/lenIV 0`, or the default of 4 | 4, 16 |
| a FontMatrix other than `[0.001 0 0 0.001 0 0]` | 0 |
| flex (othersubrs 0, 1 and 2) | 7 |
| hint replacement (othersubr 3) | 9 |
| `div` | 6, among them `0 1000 3 div hsbw` |
| an integer past ±32,000, taken only by `div` | 2 |
| seac, `sbw` | 0 |

That is 396 glyphs. Every font is simple, flagged symbolic and names no base encoding: 13 have no
`/Encoding`, and 7 have `/Differences` over the built-in one.

pdfium hands a FontFile stream to FreeType, as it hands a FontFile3 one, and reads both through
`CPDF_Type1Font`. FreeType does not run the program's PostScript. It walks the tokens for the
keys it knows and skips the rest, so a program read by a PostScript interpreter and one read by
FreeType can draw different glyphs.

## Decision

### `font.Type1`, parsed token for token as FreeType parses it

`font.ParseType1` reads the PFA form: the clear text up to `eexec`, then the encrypted Private
part, from which it takes lenIV, the Subrs and the CharStrings. The walk is FreeType's:

- `parse_dict` and its token skipper, `ps_parser_skip_PS_token`, including where that skipper
  stops inside a procedure
- `T1_Get_Private_Dict`'s search for `eexec` and the whitespace it skips after it
- `read_binary_data` for each `RD` entry, `parse_encoding`, `parse_subrs` and
  `parse_charstrings`
- the swap that makes `.notdef` glyph 0, and the `.notdef` FreeType synthesizes when a program has
  none

**Refused at parse time, each by name:**

- the PFB segment form, and a hex eexec part, which no corpus program uses
- a FontMatrix other than the default, which a scaled em would need carried through
- a PaintType other than 0, whose stroked outline FreeType fills
- a multiple master or synthetic font
- a built-in encoding that is neither an array of 256 nor StandardEncoding
- a Subrs or CharStrings entry past the declared count, and a subroutine or glyph name defined
  twice, where FreeType keeps one of the two by its table mechanics
- a radix or fractional number where FreeType reads an integer

### A Type 1 charstring interpreter

`Type1.Outline` interprets chapter 6 of the Adobe Type 1 Font Format as FreeType's Adobe engine
does in its Type 1 mode (`psaux/psintrp.c`). It draws the path operators, `hsbw` and `sbw`,
`closepath`, subroutines, `div` and seac. Hints are counted for FreeType's limit and not applied,
since pdfium loads glyphs unhinted.

**Othersubrs are taken in the form the format prints.** An othersubr is a PostScript procedure,
and FreeType runs none. It knows what 0 to 3 do and ignores the rest. So flex is taken as othersubr
1, seven othersubr 2 calls and othersubr 0 followed by exactly `pop pop setcurrentpoint`. Hint
replacement is taken as othersubr 3 followed by `pop`. A `pop` or `setcurrentpoint` anywhere else
is refused, since there is no result for it to take.

**`div` may come before `hsbw`**, because a width of 1000/3 em is written `0 1000 3 div hsbw`.
Every other operator before the first `hsbw` or `sbw` is refused. At first `div` was refused too, and
105 of the 396 corpus glyphs failed.

**Refused, per glyph:**

- an operand count other than the one the operator takes, where FreeType reads whatever is on the
  stack
- an integer past ±32,000 that is not an operand of `div`, which FreeType carries unscaled
- a path drawn before the first moveto, or after `closepath` from a point no moveto set, where
  FreeType's current point and the format's part ways
- an operator during a flex that is not a move, where FreeType would draw through the reference
  point
- a seac after the glyph has drawn, whose earlier path FreeType drops
- subroutines nested more than 8 deep, where FreeType fails the call
- the operators Type 1 does not define, and othersubrs other than 0 to 3

Work is charged to the page's budget of 4,194,304 operations, shared with CFF and TrueType.

### Which Type 1 glyph a code selects

pdfium reads a Type 1 font through the same `CPDF_Type1Font` as a CFF one, so the two share one
route and one set of refusals. ADR 0022's CFF route, `cffGID`, is now `type1GID`, and serves
both:

- a stated glyph name, from `/Differences` or the base encoding, is looked up by name
- a symbolic font with no base encoding uses the program's built-in encoding
- a nonsymbolic font with no base encoding is drawn only where the built-in encoding and
  StandardEncoding select the same glyph

The refusals shared with CFF are a base encoding other than WinAnsi, a font named Symbol, SymbolMT
or ZapfDingbats, and a program under a font that is neither Type 1 nor MMType1. A FontFile program
under a composite font is refused as well, since §9.7.4 gives Type 1 no CID route.

**`/Length1` must agree with FreeType.** §9.9 says `/Length1` is where the clear text ends. FreeType
never reads it and finds `eexec` for itself. Where the two differ, the file and the yardstick
disagree about where the cipher starts, and the font is refused. An absent `/Length1` leaves only
FreeType's answer, which is taken.

## Acceptance

**fontTools**, as a one-off second implementation. It agrees with `Type1.Outline` on all 396
corpus glyphs in 20 programs, to 1e-4 of a thousandth of an em. Both implementations assign the
same glyph to every one of the 256 codes of every built-in encoding.

**Corpus:** 1,232 pages drew before, and 1,248 draw now. None was lost, since a FontFile font was
refused before and the CFF route's refusals kept their messages.

- **The 16 pages gained** were each rendered at 72 dpi with their Type 1 glyphs drawn and skipped.
  Every one is closer to pdfium with them drawn, over the whole page and over the pixels they
  touch. On those pixels the mean difference is at most 38.4 levels, and at least 86.8 without
  them.
- **The heaviest page's glyph work** is still ISO 32000-2 page 161's 133,489. No LightOnOCR page
  is among the ten heaviest.

**Tests:** in `render/native`, 8 glyphs are drawn against pdfium, 8 code-to-glyph routes are
checked, 2 `/Length1` shapes are accepted and 12 fonts are refused by the reason they give. In
`font`, 57 functions pin the parser's and the interpreter's outlines and refusals.
`FuzzParseType1` ran 13,785,100 inputs in 60 seconds, and none panicked.

**Mutation:** 199 mutants were run on the parser, the interpreter and the routing, in copies of
the repo without the corpus. 10 did not build and were rewritten until they did. 194 are killed,
each by a named test, and 44 of those needed a test written for them. The other 5 are
equivalent:

- **The keyword length bound**, two mutants. `keyword` matches only fixed names of up to 20
  bytes, so no name at 0, 21 or 22 bytes reaches anything. The bound is FreeType's, and stays.
- **The CharStrings room cap at equality.** It assigns the count the value it already holds.
- **A two-digit octal escape.** The third digit is consumed one step later, at the same cursor.
- **The default route's glyph.** Both routes are taken only when they agree, so returning either
  one is the same.

Two findings changed code. The subroutine and glyph lenIV bounds differ, `<` and `<=`, as
FreeType's `parse_subrs` and `parse_charstrings` have them, and both are now commented and
pinned. Half of the unset-code guard was dead and is deleted. The live half is now pinned,
because `/ 1 RD` names a glyph `""`.

## Consequences

- **`render/native` draws 1,248 of 1,251 corpus pages (99.8%).** The corpus test's floor is 1,248.
- **All 17 pages Type 1 blocked now draw**, except LightOnOCR page 12, which is refused for a dash.
- **The remaining worklist** is 3 pages:
  - a dash: LightOnOCR page 12 and WTPDF page 3
  - a soft-mask group and a shading: ISO TS 32005 page 1
- **Not built:** the PFB form, hex eexec, a scaled FontMatrix, PaintType 2, multiple masters, and
  othersubrs past 3. Each refuses by name, and none has a corpus page.

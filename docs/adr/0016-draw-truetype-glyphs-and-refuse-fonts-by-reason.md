# 16. Draw TrueType glyphs, and refuse a font by the reason it cannot be drawn

Date: 2026-09-23

## Status

Accepted

## Context

ADR 0015 built `render/native`'s rasterizer core and named what stood between it and a page. Its
census was unambiguous about the first increment: **every one of the corpus's 1,251 pages draws
text**, 357 of them draw nothing else, and `font` had no glyph outlines at all — it parses metrics,
encodings and CMaps and stops before `glyf`. Of the 216 embedded font programs, **140 are
`FontFile2`**, which is TrueType `glyf`, against 56 CFF and 20 Type 1.

So this increment is glyph outlines for `glyf`, and the text machinery between a show operator and
a filled path: the code-to-glyph lookup, the outline, the quadratic curves, the text and render
matrices, and the advance.

## Decision

**`font.ParseTrueType` reads a font program and hands out outlines in 1/1000 em; `render/native`
fills them as paths.** A glyph is a path, so the rasterizer gains no new primitive — quadratics are
raised to cubics exactly, since every quadratic is a cubic, and the 2/3 control weight is
arithmetic rather than an approximation.

**Outlines are converted to 1/1000 em by the parser, not by its caller.** The same units as
`Glyph.Width`, for the same reason: a consumer must not have to ask what kind of font it holds.
TrueType's own grid is whatever `head` says, usually 1000 or 2048, and exporting `unitsPerEm` for
the caller to apply is a silent factor-of-two error on half the fonts in the world.

**The font program is a parameter, not something `Font` holds.** `Font` is cached for a document's
life by every consumer — `extract` holds one per reference for a thousand pages — and a font
program is tens of kilobytes subsetted and megabytes when it is not. Extraction never looks at one.
Holding it on `Font` would make every text run pay for every outline nobody reads, and holding a
`Store` to load it later would tie a cached font's lifetime to a file handle.

### A page is refused for the reason its font cannot be drawn, not for using a text operator

This is the decision with the most consequence. ADR 0015 refused a page by naming the *operators*
it met, and over a corpus "this page draws text" is the same message 1,241 times. Keyed by reason
instead, the same errors are a census:

| blocked by | pages | sole blocker on |
|---|---|---|
| `gs` — ExtGState | 1,184 | 0 |
| a substituted face — no descriptor, or a descriptor with no program | **1,139** | 10 |
| `Do` — XObject | 173 | 0 |
| stroking | 156 | 0 |
| CFF charstrings (`FontFile3`) | 104 | 0 |
| `cs`/`scn` — non-device fill colour | 97 | 0 |
| Type 1 charstrings (`FontFile`) | 17 | 6 |
| `sh` — shading | 1 | 0 |

`Tj`, `TJ`, `'` and `"` are absent from that table for the first time: text draws.

**That table inverts the order ADR 0015 predicted, and the reason is the layer it counted.** ADR
0015 ranked the font work by *font dictionary* — 56 CFF programs against 13 fonts needing a
substituted face — and concluded CFF was next by more than two to one. Counted by *page* it is the
reverse by a factor of eleven: the 13 fonts needing a face are the running heads and page numbers
of a specification, so they appear on 1,139 pages, and the 56 CFF programs appear on 104. A count
of definitions is not a count of uses, and the increment is chosen by uses.

**The "sole blocker" column is why the table alone still is not the plan.** `gs` blocks the most
pages and is the only thing blocking *none* of them: implementing it in isolation would draw zero
new pages. Pages unlock in combinations, so the order is measured greedily, each feature added on
top of the last:

| add | pages that draw | of 1,251 |
|---|---|---|
| a substituted face | 10 | 1% |
| **+ `gs`** | **903** | **72%** |
| + `Do` | 1,029 | 82% |
| + stroking | 1,113 | 89% |
| + CFF | 1,137 | 91% |
| + `cs`/`scn` | 1,233 | 99% |
| + Type 1 | 1,250 | 100% |
| + `sh` | 1,251 | 100% |

**So the next increment is a substituted face and `gs` together, for 72% of the corpus**, and CFF —
which ADR 0015 named second — is fifth, worth 24 pages when it arrives.

**The survey resolves fonts and glyphs, not just operator names.** ADR 0015's survey lexed the
stream and listed operators; the font reason was decided later, in the paint pass. That made the
worklist silently incomplete in exactly the case it exists for, because the paint pass never runs
when anything else blocked the page: a page with an ExtGState and a CFF font reported `gs` alone.
With `gs` on 1,184 of 1,251 pages, the font census above would have been taken from the 67 pages
that happened to have nothing else wrong with them. The survey now runs the graphics state machine
too, so that a glyph is required only where one would be drawn — §9.3.6's render mode 3 is how a
scanned page's invisible OCR layer is written, and demanding a glyph for text nobody draws would
refuse a page over characters that mark nothing.

**A code with no glyph refuses the page.** It is a character the page draws and this backend
cannot, which is the same class as an operator it cannot draw. Dropping it would put a page on
screen with one character quietly missing — at the granularity where it is hardest to notice.

**A cmap entry resolving to glyph 0 is read as absence, in every subtable format.** Glyph 0 is
notdef, a real glyph that draws a box, so returning it would put a box on the page where the font
said nothing. PDFsharp's glyph-mapping note states the same rule from the producer's side — "glyph
0 is always the unknown glyph" — and it is the one value a lookup must not treat as an answer.

**Three routes from a code to a glyph, in this order** (§9.6.5.4, §9.7.4.2): a composite font maps
its CID through `/CIDToGIDMap`; a symbolic simple font looks its *code* up in the program's (3,0)
subtable, trying the Private Use Area form as well, because such a font's codes mean nothing
outside it; and a non-symbolic font goes through the character to the Unicode subtable. The last
resort is the code as a glyph index, which is not a guess but what a producer means by a subset
with no character map to read it with — gated on the program actually having that many glyphs.
Order matters and is not interchangeable: a CID is not a character code, and reading one as the
other sets a composite font in entirely the wrong glyphs.

**Point-matched composite components are refused.** A component can be placed by naming two point
indices that must coincide rather than by an offset, and placing it wrongly moves an accent
somewhere plausible. Refusal is what ADR 0015's contract implies.

**No hinting.** An outline is scaled and filled, which is what every renderer does above about
twelve pixels per em. The measured consequence is that **pdfium grid-fits a glyph's origin to the
pixel and this backend does not**: the same glyph at an integer pen position agrees with pdfium to
a mean of 0.003 of 255 with no pixel further than 32, and at a half-pixel position to 0.064 with
27 pixels over. Every non-zero allowance in the acceptance test belongs to a case whose pen lands
between pixels. Grid-fitting is a hinting decision and this ADR declines to make it.

## Consequences

**The font the tests read is built from nothing, in `internal/ttfbuild`.** Every real font is
someone's copyright and the corpus's are inside gitignored ISO documents, so a test needing a font
either redistributes one it may not, skips in a clone, or builds what it needs. Two packages need
the same bytes, and a builder duplicated in two test files is the same defect as arithmetic
duplicated in two packages.

Its glyphs are chosen for what separates them, and each was added because something survived
without it: a square of straight lines; a diamond of four *off-curve* points, whose implied
midpoints a reader misses by treating the point list as a polygon; a composite that places the
square twice, once scaled, because a wrong transform puts an accent somewhere plausible; a **ring
of two contours wound the same direction**, which is the only shape where nonzero and even-odd
disagree, since a real font winds a counter the opposite way and there both rules agree; and a
space, a real glyph with no ink, which every font carries and the first version of this one did not.

**Two tables exist only because FreeType will not open a font without them, and that asymmetry is
the point.** `hhea` and `hmtx` are not read by this package's own parser. pdfium reads the fixture
through FreeType, and **a font FreeType rejects is silently substituted** — so without them the
acceptance test compared this rasterizer against whatever face the machine happened to have, which
looks like a disagreement about rasterization and is a disagreement about which font is being
drawn. The `head` magic number is checked the same way. And `hmtx`'s left side bearing must equal
each glyph's own `xMin`, because FreeType shifts an outline to agree with the declared bearing: a
font declaring zero for a glyph starting at x=100 renders 100 units left of where its own outline
says, which looked exactly like a rasterizer placing glyphs wrongly.

**§9.4.3's quote operators moved into `content.Machine`, and that fixed a live defect.** `'` is
`T*` then `Tj`, and `"` sets word and character spacing and then behaves as `'`. The machine's
`Apply` documented that it does not handle the show operators, because they need font metrics — but
the line move and the spacing need no metrics, and leaving the whole operator to the caller meant
the state half was spelled out in `extract`'s walker and nowhere else. `render/native`, written
later against that comment, **drew every `'` on the line above**: pdfium renders `'` and `T*`
identically and this backend did not, by a mean of 2.8 of 255 over 491 pixels.

`Apply` now applies the state and still reports the operator unhandled, because the caller has a
string left to draw. A caller treating `false` as "nothing happened" was already wrong about `Tj`.

**The acceptance test's own bound was the last thing found, and it had been written and never
used.** ADR 0015's tolerance is a mean *and* a count of pixels further than 32 apart, and the text
comparison declared the per-case count in its table and asserted only the mean, at 6.0. Three
mutations lived there: reversing every one of `TJ`'s kerning adjustments moves the mean to 3.8 —
under the bound — and the over-32 count to 671. The mean is now 1.5 and the count is asserted per
case at its measured value.

**Thirty-eight mutations, thirty-eight killed.** Both fill rules on a glyph; the contour rotation
and its off-by-one, the implied midpoint, the closing quadratic, and the end-point array that
separates one contour from the next; all four cmap subtable formats, format 4's `idRangeOffset`
branch and its `idDelta` addition, and a zero glyph read as absence in each; the symbolic route's
subtable choice and its 0xF000 form; `f2dot14`; the short `loca`'s halved offsets; the unit-grid
conversion, twice, once for outlines and once for a composite's offset; each of the three
code-to-glyph routes and the order between them; the glyph-space scale; the quadratic's control
weight; word spacing's code and its single-byte condition; `TJ`'s adjustment sign; the survey's
four show operators, its `Tf`, its render-mode gate and its glyph check; the quote's line move, the
double quote's spacing, and `Apply`'s report.

**Eight of those survived the first run**, and roughly half of the set needed a fixture or an
assertion that did not exist. Most of those were rules **nothing in this repo reached at all**:
three of the four cmap formats, format 4's `idRangeOffset` branch and the `idDelta` addition inside
it, a zero glyph read as absence, the symbolic route and its 0xF000 form, point matching, the
code-as-index last resort, the composite route, same-direction nested contours, word spacing on a
multi-byte code, and the double quote's spacing operands. A rule with no input is not tested by a
passing suite; it is absent from it.

The rest were fixtures too loose to discriminate, and each one taught the same lesson at a different
scale. Counting a diamond's four quadratics cannot tell a curve through the implied midpoints from
one through the control points — both are four — so that outline is compared exactly now, which is
only possible because the font is built by hand. A 48pt curve cannot tell the 2/3 control weight
from 1/3, because the deviation is a fraction of a pixel at that size; a 160pt one can. And a
composite's offset conversion is a multiplication by one at 1000 units per em, so not doing it at
all was correct in every test there was until a 2048-unit grid was added.

**Vertical writing advances by the wrong metric, and this ADR does not fix it — it names it.**
`showText` takes its advance arithmetic from `extract`, deliberately, because where the next glyph
starts is one question and two answers to it would be two readings of §9.4.4. That inherited a
reading which is wrong for vertical text in two ways: the displacement comes from `Glyph.Width`,
where §9.7.4.3 says a vertical glyph's `w1` comes from `/W2` or `/DW2` — defaulting to one em down,
not to the horizontal width — and the horizontal scaling `Th` is multiplied in, where §9.4.4 applies
it to the horizontal coordinate only. Neither `/W2` nor `/DW2` is parsed anywhere in `font`.

It is left because **the corpus has no vertical text at all**: all 67 CIDFontType2 fonts are
Identity-H, so there is nothing to measure a fix against and no page it would change. Fixing it
blind would be a third reading of §9.4.4 with no way to tell whether it was right. Recorded here
and in DESIGN §10 so that it is a known gap rather than a latent surprise, and so the first
vertical document that arrives finds the answer already written down.

**Still 0 of 1,251 corpus pages, and saying so plainly is part of the decision.** Text renders and
is measured against the engine it replaces on seventeen streams; what stands in front of a real page
is the table above, in the order the table gives. `render/pdfium` stays, the binary does not shrink,
and nothing falls back yet.

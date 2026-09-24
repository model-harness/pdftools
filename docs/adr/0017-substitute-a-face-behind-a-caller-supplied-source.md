# 17. Substitute a face behind a caller-supplied source, and honour an ExtGState by parameter

Date: 2026-09-24

## Status

Accepted

## Context

ADR 0016 replaced a guess with a measurement and the measurement named this increment. Counted by
page rather than by font dictionary, and greedily rather than by frequency, the corpus's blockers
ranked: **a substituted face plus `gs` together draw 903 of 1,251 pages (72%)**, and *neither alone
draws anything worth having* — `gs` blocks 1,184 pages and is the sole blocker on **zero** of them,
while a substituted face alone unlocks 10.

So the two are one increment, and that is the whole reason they are in one ADR.

## Decision

### `gs` is honoured parameter by parameter, and the corpus's ExtGStates change nothing

A spike over all 1,251 pages found **2,680 `gs` operators on 1,184 pages**, and what they set is
almost entirely inert:

| key | pages | values |
|---|---|---|
| `/ca`, `/CA` | 1,184 | **1** on 2,678 and 2,636 of 2,680; one each at 0.996078 |
| `/BM` | 1,182 | `/Normal` on 2,668 |
| `/SMask` | 177 | `/None` on 1,625; **one** dictionary |
| `/op`, `/OP`, `/OPM`, `/SA`, `/AIS` | 177 | overprint and stroke adjustment |

There is **no `/LW` anywhere**, and no `/Font`, `/D`, `/TR` or `/HT`. That last fact retires a
consequence ADR 0015 recorded: `content.GraphicsState.LineWidth`'s comment says an ExtGState's
`/LW` is unread because applying one needs the page's resource dictionary, and rendering was named
as the consumer that would make the gap expensive. It does not, because nothing writes one — and
even if something did, `/LW` sets a pen width while every stroking operator is refused.

**The classification is an allow-list of keys, for the reason ADR 0015 inverted the operator
list.** An enumeration of what to refuse is wrong the first time a key arrives that nobody
enumerated, and wrong *silently* — by drawing a page whose graphics state it did not understand.
A key `checkExtGState` has not been taught refuses the page.

`/ca` is **implemented** rather than conditionally accepted: constant alpha multiplied into
coverage, which is exact over an opaque backdrop under the normal blend mode, and this canvas has
both. That takes the one 0.996078 page with it and needs no special case.

Everything else is accepted with its own argument, not one waved at the group: `/CA`, `/LW`,
`/LC`, `/LJ`, `/ML`, `/D` and `/SA` set stroke alpha and pen geometry while every stroking operator
is refused — ADR 0015's reasoning for accepting `G`, `RG` and `K`; `/OP`, `/op` and `/OPM` control
how colorants combine on a device with separations, and this backend composites RGB; `/BG`, `/UCR`
and their `2` forms apply when converting to CMYK; `/HT`, `/FL` and `/SM` are screening and
tolerance hints with no required effect on a continuous-tone raster; `/RI` selects a gamut mapping
and ADR 0015 already declines to colour-manage; `/TK` and `/AIS` only matter under a non-normal
blend mode or a soft mask, both refused below.

Refused: a `/BM` other than `/Normal` or `/Compatible`, a `/SMask` dictionary, and `/Font` —
which sets the text font from the graphics state, so ignoring it would draw the page in whatever
`Tf` last named.

**`gs` went from blocking 1,184 pages to blocking 1**: the single soft-mask group.

### A substitute face comes from the caller, through an interface in `font`

**`font.FaceSource` is an interface and `render/native` refuses without one.** Which face
substitutes for Helvetica is policy, not parsing. A proofing tool wants metric compatibility with
the original; an archival pipeline may be required to use a face it has licensed; a caller
rendering for search does not care. A parser choosing for everyone would make a typographic
decision on evidence it does not have — and would put several faces' bytes into every binary that
imports it, including the ones that never render a page.

The cost is stated rather than discovered, and every claim in this ADR carries it: **the native
backend draws 903 pages only when a `FaceSource` is supplied, and 0 without one.**

It lives in `font` rather than in the renderer for two reasons, one of which was found by
building it the other way. A substitute font is font data. And with the interface in
`render/native`, the one implementation imported the renderer — so no test in `render/native`
could import the implementation back, and the corpus-coverage test below was an import cycle. The
cycle was the symptom; the misplaced boundary was the defect.

### Typeface inference is deterministic, and has no outlines to look at

This is the half of ADR 0018's architecture that is built, and the distinction that makes it
buildable is the input. **A font needing substitution has no outlines at all** — the program is
absent, which is the entire reason a substitute is needed — so there is nothing to measure the
shape of. The evidence is the `/BaseFont` name, the descriptor's `/Flags`, `/StemV`, `/FontWeight`
and `/ItalicAngle`, and the `/Widths` array. `font.Load` already reduced those to bold, italic and
monospaced traits, weighing name against descriptor where they disagree; this ADR adds serif, from
`/Flags` bit 2 and the name, because nothing before needed it.

`Font.SubstituteStyle` reduces that to a `Style`: one of four families — sans, serif, monospaced,
symbol — plus bold and italic. **Four values and not more, because four is what the evidence
supports**: "sans or serif" is answerable from a name and a flag, and "Garamond rather than Caslon"
is not.

**There is no confidence score, and its absence is a decision.** Every branch is a rule over
stated evidence, so the answer is determined rather than estimated. A probability attached to a
determined answer reads as humility and acts as noise — a caller can do nothing with 0.9 that it
cannot do with 1.0, because there is no second candidate to weigh it against. Confidence belongs
to an inference that *ranks* candidates, which is character inference from an outline: a different
problem, on a population this corpus does not have, and the subject of ADR 0018.

**Sans is the default rather than serif**, because `/Flags` bit 2 unset means "not stated" far more
often than it means "no serifs", and the corpus settles which way to lean: Helvetica on 1,138 pages
and Arial on 886 against Times on 148.

**`FamilySymbol` exists so a request can be declined rather than answered.** Symbol and
ZapfDingbats are also serif-less and would otherwise resolve to sans — and a page of mathematics
set in Liberation Sans draws Latin letters where the page shows operators, which is a page that
looks like text and says something else. Declining is a normal answer from a `FaceSource`, not an
error.

Recognised by *name*, not by `/Flags` bit 3. The symbolic flag is set on a great many ordinary text
subsets — a symbolic TrueType is simply one whose cmap is keyed by code, which is how most
subsetters write Latin text — so reading it here would send half the corpus's text fonts to a
dingbat substitute.

### A substituted face is reachable only through the character

This is the sharpest consequence and the easiest to get wrong. Every other code-to-glyph route in
ADR 0016 asks the *document* which glyph it means: `/CIDToGIDMap` indexes the program the document
embedded, a symbolic cmap is the one the subsetter wrote, and the last resort treats the code itself
as an index into it. **None of those indices mean anything in a face the document has never seen.**
Following them would pick whatever glyph sits at that position and draw it with complete confidence.

So a substituted font resolves a code only through its decoded text, and a code whose text could
not be decoded refuses the page. That is the honest outcome — it is exactly the case where nothing
knows what character the page draws — and it is why a substituted symbolic font with no
`/ToUnicode` stays refused rather than becoming a line of arbitrary letters.

**CFF and Type 1 fonts are refused rather than substituted**, and the difference from a
non-embedded font is worth stating: the document *did* embed a face, so its glyphs are on hand and
only the charstring interpreter is missing. Standing a different face in for one the file carries
would replace a typeface the producer chose and shipped — a larger misrepresentation than declining
the page.

### `cmd/pdfspec` supplies Liberation, and the numbers were corrected once

`internal/liberation` embeds **eight faces of Liberation 2.1.5** — Sans and Serif in regular, bold,
italic and bold italic — gzipped to **1.69 MB** from 3.17 MB raw, inflated on first use. The CLI
binary goes from 25.24 MB to 27.12 MB.

**An earlier figure in this work was wrong and was corrected before anything was built on it.** The
573 KB quoted for four faces came from `pdfjs-dist`'s trimmed copies; the official faces carry full
Unicode coverage — Cyrillic, Greek, Hebrew, extended Latin — and all twelve weigh 4.36 MB raw and
2.38 MB gzipped. Mono was then dropped: Courier is requested on **0** of the corpus's 1,251 pages
and the three faces cost 686 KB, so a page asking for one is refused with a reason naming the
missing family.

Gzipped rather than subsetted, because the OFL reserves the name: a modified Liberation may not be
called Liberation, so subsetting would mean renaming the faces and shipping something no longer
traceable upstream. Compression leaves the bytes identical. The licence and AUTHORS travel with
them in `faces/`.

`internal/` so that **no external importer pays for these bytes**, which is the same decision as
the interface, one layer down.

### pdfium cannot be the oracle here, so the bar is a pair of assertions

**The third place this phase has had to name pdfium as not the yardstick**, after CMYK's
calibrated table (ADR 0015) and glyph grid-fitting (ADR 0016). pdfium substitutes Foxit's own
standard-14 clones; this backend substitutes whatever its source supplies. The two draw *different
typefaces* by construction, and no per-pixel tolerance can separate "a different face" from "a
defect" — a bound loose enough to admit the first admits the second.

**Primary: the same page, substituted against embedded, to the pixel.** One stream rendered twice —
once against a dictionary with no program so the face arrives through substitution, once with that
very program embedded — must agree exactly. Same rasterizer, same outlines, same advances; the only
difference is where the bytes came from. The tolerance is **zero**, which is tighter than any
external oracle could give, and it asserts precisely what substitution claims: *this page looks as
if the face had been embedded.*

**Secondary: geometry against pdfium, for the half the first is blind to.** If the style inference
picked bold for a regular font, both renderings above would pick bold and agree. So the ink's
bounding box and total are compared against pdfium, which are the quantities that do not depend on
which face drew them. With Liberation against pdfium's Foxit Helvetica the left, right and bottom
edges are **identical** and the top differs by **one** pixel, because both faces are built to
Arial's metrics; total ink is **0.909**. The bounds are 2 pixels and 20%, which is what the
measurement supports — an earlier draft allowed 6 pixels and 35%, headroom for the hand-built
fixture font the test used before it used the real one.

A third assertion covers what neither can: the style a `FaceSource` is *asked* for, over eleven
name-and-descriptor combinations, because a producer writes "Arial-BoldMT" and states no weight.

## Consequences

**`cmd/pdfspec` gains `-backend pdfium|native|auto`, and `auto` is the fallback ADR 0015 declined
to build.** That ADR said `ErrUnsupported` was "what a fallback will be built on, not something one
already uses"; this is it being used. Per page rather than per document, which is the whole value:
the native backend refuses a page it cannot draw *in full*, so a mixed document gets native
renderings for the pages it can draw and correct ones for the rest. Only `ErrUnsupported` falls
through — a malformed page is an error from either backend, and retrying it would replace a specific
complaint with a vaguer one.

**pdfium stays the default**, because the two backends do not produce identical pixels: they differ
on CMYK by design, on grid-fitting because this one does not hint, and on a substituted typeface.
Making native the default would silently change every rendered page for existing callers.

**One constructor for both callers.** `render` and `ocr` each had `renderpdfium.Open` written out,
so adding a backend to one would have left the other behind with nothing to say so.

**The corpus coverage figure is now a test with a floor, not a sentence in an ADR.** `903 of 1,251`
is asserted at `>= 900`, because every ADR in this phase states a page count and a figure in prose
drifts — a refusal added elsewhere for a good reason can take a hundred pages with it and no
fixture-based test would notice. It runs at **20 dpi**, since whether a page is refused is decided
by the survey and is resolution-independent while the painting is not: at the default 200 dpi the
run exceeded `go test`'s ten-minute timeout, and at 20 dpi it is 92 seconds and measures the same
set. It skips without the gitignored corpus, and under `-short` so that a mutation run stays
affordable — a cost noted where it is taken, because a mutation only this test kills then shows as
a survivor and has to be re-checked.

**The remaining worklist, re-measured:** `Do` 173 pages, stroking 156, CFF 104, `cs`/`scn` 97,
Type 1 17, one soft mask, one shading. `Do` is next at 82% cumulative, then stroking at 89% and CFF
at 91%.

**Vertical writing is still wrong and still unfixable here**, as ADR 0016 recorded: the advance
comes from the horizontal width with `Th` applied, where §9.7.4.3 wants `/W2`. All 67 CIDFontType2
fonts in the corpus are Identity-H, so there is no page to measure a fix against.

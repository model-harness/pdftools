# 15. Rasterize paths natively behind a backend that refuses what it cannot draw

Date: 2026-09-23

## Status

Accepted

## Context

ADR 0005 borrowed pdfium on wazero and named the terms of the loan: **+10.0 MB of binary**, a
one-time ~1.4 s module compile, and a **second parse of every file**, because go-pdfium brings
its own parser and cannot be handed an `objects.Store`. DESIGN.md §8's Phase 6 is the
repayment — "native replacement, rasterizer first" — and `render.Rasterizer` was written to be
three methods so that repaying it is a sibling package and a wiring change rather than a
migration.

The question this ADR answers is not whether to write one. It is **where to start**, because a
page rasterizer is, as `render`'s own doc comment says, a decade of glyph hinting, blend modes
and shading types. Starting in the wrong place produces a package that renders nothing useful
for months.

**What the corpus draws decides it, and the census is unambiguous.** Over the 12 documents'
1,251 pages: **every single page draws text**. 357 pages (28.5%) draw nothing else, 894 draw
text with paths or images, and exactly 1 draws a shading. There are **no** image-only pages and
**no** path-only pages. So:

- A rasterizer that starts with images renders 0 pages — and the consumer that would want it,
  `ocr`, only routes pages whose text coverage is under 5%, of which this corpus has none.
- A rasterizer that starts with text needs glyph outlines, and `font` has none: it parses
  metrics, encodings and CMaps, and stops before `glyf` and before a CFF charstring.

**The font census then fixes the order after this one.** The corpus's pages reach 229 font
dictionaries, of which **216 carry an embedded program: 140 `FontFile2`** — TrueType `glyf` —
against 56 `FontFile3` (CFF) and 20 `FontFile` (Type 1). The other 13 carry no program to parse:
7 are standard-14 fonts with no descriptor at all and 6 declare a descriptor with no font file,
which is a non-embedded TrueType the renderer would have to substitute for. So `glyf` is the
first outline format and CFF the second, by a margin of more than two to one — and 13 fonts will
need a fallback face whatever order the rest are done in.

That leaves the rasterizer *core* — path construction, the fill rules, clipping, anti-aliased
coverage — as the only piece that is both foundational to all of the above and finishable
without any of it. Glyphs are paths; images are a resampling into a mask; strokes are paths.

## Decision

**`render/native` implements `render.Rasterizer` with a scanline rasterizer, and refuses any
page it cannot draw in full.**

**Coverage is sampled 16× vertically and computed exactly horizontally.** A polygon's
intersection with a pixel is a general polygon, so the exact area is a per-pixel clipping
problem; along one scanline it is an interval, where there is nothing to approximate. The split
was measured against pdfium on fourteen streams: a convex triangle; a five-pointed star drawn as
one self-intersecting subpath under each fill rule; a closed pair of cubics; `re`; two greys; the
`v`, `y`, `F` and `h` operators; a rotating and a scaling `cm`; and three colours. The count grew
during review, and each addition was a mutation that had survived for want of an input.

Sixteen samples is a measurement now and was not when this was written. On these shapes four
samples agree with sixteen to the same bounds — the review that found that is why the claim
changed — and what four cannot represent is a rule thinner than a quarter of a pixel: a 0.1pt
hairline covers a sixteenth of a row, which is 32 of 255 at sixteen samples and nothing at four.
A table rule is a hairline, so that is the case that prices the constant. The 0.2-device-unit
flattening target is *not* measured: 1.0, 2.0 and 5.0 all pass these shapes and only 20.0 fails,
so it is headroom for glyph outlines rather than a fitted value, and saying otherwise would be
claiming a measurement that has not been made.

| | mean ink difference | worst pixel | pixels over 32 | total ink |
|---|---|---|---|---|
| 16 samples, exact spans | **0.000–0.050 of 255** | 0–28 | **0** | 1.000 in aggregate, 0.998–1.002 per shape |
| quantized per subsample | 0.22–4.73, mean of means 2.01 | 37 | 8 over 14 shapes | 0.940 |

The second row is the version that gives each subsample an integer weight of `floor(255/16)`,
which is 15: a fully covered pixel comes out 240 of 255 and the whole page is 6% light. It is the
error class this design note exists to name in advance, because it never looks wrong — it looks
very slightly pale, on every page, forever. An earlier draft of this ADR quoted it as a single
mean of 3.47 with a worst pixel of 31 and no pixel over 32; the figures here are re-measured over
all fourteen acceptance shapes, and only the ink ratio reproduced.

**pdfium is the yardstick, not a second opinion.** Borrow-then-replace means the borrowed engine
defines what the native one has to reproduce, so the acceptance test compares against it
directly and the tolerance is the measurement above with headroom: **mean ≤ 1.0 and no pixel
further than 32**, per shape, with a per-case allowance where a measurement says otherwise. Two
bounds and not three: an earlier draft added "worst ≤ 48", which could not fire without the
over-32 count firing first and so read as headroom that was not there. A fill rule inverted, a
y-flip, or a flattening error each move the mean by tens, so the bound is loose enough for an edge
pixel's anti-aliasing convention and tight enough for nothing else.

The allowance is used twice, for the `v` and `y` curves, at five pixels each. Ten times finer
flattening and four times more coverage samples leave the same five pixels at the same 40 levels,
so what differs is where pdfium places a curve's anti-aliasing at the extreme of its curvature —
and the mean over those two shapes is 0.036, better than the cubic pair that passes at zero. A
bound stated as a count of edge pixels is the honest shape for that; a looser mean would have
hidden it. One assertion cannot come from the oracle — that even-odd *hollows*
what nonzero fills — because swapping both implementations would keep them agreeing; the star's
centre pixel is sampled directly for that.

**A page this backend cannot draw in full is an error, never a partial image.** `Page` returns
an `*Unsupported` naming the operators it met, unwrapping to `ErrUnsupported` so a caller can
fall back to the borrowed backend for that page. This is the decision with the most consequence
and the least code: a rasterizer that skipped the operators it does not implement would return a
page with its text missing and nothing to say so, which is the same failure as a dropped page —
a failure this repo has already paid for once, in a cover page that was absent from every
conversion for the project's life while every check reconciled. A plausible-looking image invites
even less scrutiny than an empty one.

**A page is surveyed before it is painted, and the survey lists every blocker rather than the
first.** Two passes over the same bytes: the first asks only whether every operator is one this
backend can draw, and painting happens only if the answer is yes. Painting until the first
unsupported operator turned up was the earlier shape, and it meant a page of paths and text — 894
of the 1,251 — paid for every path before its first `Tj`: **32.6 s** over the corpus at the
default 200 DPI against **1.1 s** when nothing is painted, and that second figure is
resolution-independent because nothing is allocated per pixel. Lexing twice costs a tenth of a
second across the corpus.

Listing everything is what produced the ranking; stopping at the first blocker would have reported
`Tj` for every page and hidden that `gs` blocks 1,184 of them. It also replaced a guard no test
could see — checking the blocker list inside each painting operator, where the image is discarded
either way and the only observable was wall time.

| blocked by | pages |
|---|---|
| `TJ` / `Tj` — show text | 1,241 / 1,234 |
| `gs` — ExtGState | **1,184** |
| `Do` — XObject | 173 |
| `S` — stroke | 156 |
| `cs` / `scn` — non-device *fill* colour | 97 / 97 |
| `sh` — shading | 1 |

`CS`/`SCN` are not in that table and were in an earlier draft of it. They set a *stroke* colour
space, which marks nothing while every stroking operator is refused, so refusing a page for one
would refuse it over a change no pixel can see — the same reasoning that accepts `G`, `RG` and
`K`. The fill-side `cs`/`scn` stay refused, because a Pattern fill colour changes what `f`
paints.

**Today that is 0 of 1,251 corpus pages and 0 of 12 reference fixtures**, and saying so plainly
is part of the decision: this increment buys a proven core and a measured worklist, not a
working backend. The alternative — shipping something that draws most of a page — is the one
outcome the refusal contract exists to prevent.

**Device colour spaces only.** `g`/`rg`/`k` and their stroke forms, with CMYK converted by
§8.6.4.4's subtractive formula. An ICCBased or Separation colour needs a profile and a rendering
intent, which is a decision a rasterizer cannot make for a caller; guessing sRGB is how a
proofing tool comes to disagree with a printer. `cs`/`scn` are refused rather than approximated.

**CMYK is the one place this backend and its own yardstick disagree, and it is deliberate.** Pure
K by the formula is RGB(0,0,0); pdfium paints it at about RGB(35,35,35) from a calibrated table.
Measured over a pure-K rectangle the two differ by a **mean of 11.0 with 12,600 pixels over 32**,
three times the acceptance bound — so CMYK is outside the pdfium comparison by construction, and
a test of the conversion's own six cases stands in for it. Matching the table would mean adopting
a colour-management decision the page does not state; differing visibly from one reader is better
than guessing quietly, and a caller who needs pdfium's numbers can use pdfium.

**The clip is a full-page mask; a fill is composited row by row.** A clip outlives the operator
that set it, so it has to be stored. A fill does not, and a spec page draws hundreds of small
paths — the eleven-document corpus draws 284,866 rules across 1,023 of its pages, ADR 0014's
figure — so a page-sized coverage allocation per path costs the page's area every time.
`path.scan` hands one row of coverage to a callback and the canvas composites it immediately, over
the path's own rows and columns.

Three costs of the same shape were found by measuring rather than by reading, and all three were
mine: a coverage mask per fill; a page-area intersect per clip, where bounding the clip mask by
its own rows took 200 clips at 200 DPI from 3.6 s to negligible; and a page-area *opaque* mask
rebuilt on every `Q`, which is the cheapest-looking line in the walker and cost more than
everything else in it put together — 43 s over the corpus against 5.5 s once an unclipped `q…Q`
pair became free. A rasterizer's costs are all page-area multiplied by something, and the only way
to find which something is to measure the whole corpus.

**Edges are bucketed by the first row they can cross, and carried in an active list.** Without it
every sample of every row rescans every edge: 10,000 segments spanning a page took **154 ms**
against 5.5 ms, and on a taller page with the old column bound it was seconds. `path.scan` sorts
crossings with insertion sort while there are at most 32 of them — the case for a glyph or a rule,
and the case insertion sort is best at — and `sort.Slice` past that, because insertion sort is
quadratic and a self-intersecting chart is not rare.

**`content.Machine` carries the graphics state, and both it and this walker see every
operator.** The machine is already this repo's reading of §8.4 and a second one here would be a
second set of answers. But `W` and `W*` are state to the machine — a clip depth — and a *path*
here, so skipping an operator because the machine reported handling it dropped every clip on the
page. Both run, unconditionally.

## Consequences

**`render/pdfium` stays, and the binary does not shrink yet.** Nothing is removed by this ADR:
the +10 MB and the second parse are still what a rendered run costs, because the native backend
refuses every real page. What changes is that the replacement now exists, is tested against the
engine it replaces, and has a ranked list of what stands between it and a page.

**The `gs` figure connects Phase 6 to the last documented hole in `content`.** `gs` blocks 1,184
pages here, and `content.GraphicsState.LineWidth`'s comment already records that an ExtGState's
`/LW` is not read because applying one needs the page's resource dictionary, which `content`
deliberately does not take. Rendering is the consumer that makes that gap expensive rather than
theoretical, so the hook for it belongs in this phase rather than after it.

**The next three increments are named by the table, in order:** `glyf` outlines in `font` (140 of
the 216 embedded programs, and the only thing standing between this backend and the 28.5% of the
corpus that draws text and nothing else), then `gs`, then `Do` with the existing `image` decoders.
CFF follows `glyf` at 56 programs, and 13 fonts will need a substituted face. Stroking is fourth by page count and is a pen-geometry problem —
join, cap, dash, and a width that is a distance in user space — so it gets its own increment
rather than an approximation inside this one.

**A native `Rasterizer` reads through `objects.Store`, which is the borrow's other cost
repaid.** A caller that has already opened a document for text extraction pays for one parse
instead of two. That is invisible today, since no page renders, and it is the reason the
constructor takes a `Store` rather than a path: the shape of the saving has to be in the interface
from the start or the backend that finally draws a page will have the same second parse the
borrowed one does. Two consequences follow and are worth stating rather than discovering.
`render.Rasterizer`'s own comment says implementations are not safe for concurrent use, so
page-level parallelism needs one `Store` per worker, which spends part of that saving back. And
the two adapters' constructors differ — `pdfium.Open` takes a path, `native.New` takes a Store —
so the fallback caller this ADR describes has to hold both.

**Nothing falls back yet.** `cmd/pdfspec` constructs `render/pdfium` unconditionally; there is no
`-backend` flag and no `errors.Is(err, ErrUnsupported)` anywhere outside this package's tests. The
sentinel is what a fallback will be built on, not something one already uses, and an earlier draft
of this ADR and of the package comment both said otherwise.

**Twenty-eight mutations, twenty-seven killed, and the survivor is stated.** Both fill rules and
both directions of the even-odd parity; the y flip, the box origin, and each rotation; `addSpan`'s
end-pixel arithmetic and both of its clamps, one of which is not defensive — a span ending exactly
at the page width indexes one past the row without it; the NaN crossing guard; `intersect`'s
rounding, which needed a unit test because one level of 255 is invisible in any image; the sample
count, which needed a 0.1pt hairline; the clip stack, the clip's timing, and the path surviving its
own rasterization; `v`, `y`, `F`, the CMYK channels, the colour clamp, the white background,
`Box`, the annotation refusal, and the allow-list's default. The survivor is `scan` closing the
path in place rather than locally: with the clip taken at the end of the path object no caller
reads a path after scanning it, so the two are equivalent today. It stays local because the next
caller is a glyph cache that will scan one outline many times.

**Fourteen fixture streams, written in the test rather than committed.** The subject is which
operator sequence produces which pixels, so a fixture per case would be a directory of
near-identical files; the PDFs are assembled byte by byte in `native_test.go` the same way
`objects/pdfcpu`'s malformed-page fixture is, and for the same reason — no producer this repo can
drive emits exactly the stream a test needs. The pdfium comparison fails rather than skips when
the borrowed backend cannot open a file: ten of the mutation kills depend on it, and a comparison
that can skip itself retires them silently.

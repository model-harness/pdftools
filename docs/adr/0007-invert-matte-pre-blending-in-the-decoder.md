# 7. Invert `/Matte` pre-blending in the decoder, not in `render`

Date: 2026-08-04

## Status

Accepted

Amends ADR 0004 ("Do not composite the soft mask into the base image") and ADR 0005
("`render` is where a backdrop exists, so it is where `/Matte` un-premultiplication
belongs"). Neither is superseded: everything else both decided still holds, including
that `image` does not composite. What changes is where one specific inversion runs, and
the claim that it had no implementation.

## Context

ADR 0004 measured 136 of the corpus's 143 soft masks as carrying `/Matte`, correctly read
§11.6.5.3 as meaning their base samples are pre-blended against the matte colour, and then
deferred the inversion — writing that the honest output is "both layers plus the statement
that one is premultiplied". ADR 0005 assigned that deferred work to `render`, reasoning
that render is where a backdrop exists.

That placement is wrong, and the defect it left is not cosmetic. `decodeSamples` wrote
pre-blended samples into `color.NRGBA` — a type that is *non*-premultiplied by definition.
So 131 corpus images were not merely un-inverted, they were mislabelled: any consumer that
composites them applies alpha a second time to samples that already carry it. Handing over
both layers is only honest if the base layer says what it is.

Reading the clause settles the placement, because it contains an ordering requirement
neither prior ADR quoted:

> If a colour conversion is required, inversion of the pre-blending shall precede the
> colour conversion.

The inversion therefore has to run on samples still in the parent image's own colour
space. `decodeSamples` converts CMYK, Lab, Indexed and Separation to RGB on the way to a
PNG, and that RGB is the only thing any caller above this package ever sees. Inverting a
CMYK blend after the subtractive conversion inverts a blend that was never performed in
CMYK; the arithmetic runs, and the answer is different. Pre-conversion samples exist for
the duration of one loop iteration inside the decoder and nowhere else, so the decoder is
the only place this can be correct.

Two clauses beyond the ordering shape the units. Table 144 says the matte's numbers "shall
be valid colour components in that colour space", and the forward computation "shall use
actual colour component values, with the effects of the **Filter** and **Decode**
transformations already performed." Together those put the matte in the *post*-`/Decode`
domain, which is what fixes the inversion's position as after `applyDecode` and before
`toRGB` rather than merely somewhere between them. Every corpus image with a `/Matte` uses
the default `/Decode`, so the order is unobservable there and needed a test built on an
inverting one.

Table 143 also requires a mask carrying `/Matte` to have the same `Width` and `Height` as
its parent — "otherwise independent of it." All 136 of the corpus's matted masks comply, and
the 4 masks that differ in size from their parent are all unmatted, so nearest-neighbour
resampling never lands on this path.

Two further clauses shape the exclusions. Table 144's NOTE addresses α = 0 explicitly:
the inverse divides by zero, so "an arbitrary value for *c* can be chosen within the range
of colour component values", and "because α is 0.0, the arbitrary value of *c* does not
affect output." And on Indexed parents: "the colour values in the colour table (not the
index values themselves) shall be pre-blended" — a different operation on a different
array from the one a sample carries.

The corpus was measured before any of this was designed, with throwaway in-package probes
(the functions are unexported), because two of the numbers decide the implementation:

| | measured |
|---|---|
| images with `/Matte` | 136, all `[0 0 0]` |
| invertible | **131** |
| blocked, all DCT base with a raw mask | 5 |
| matted pixels | 28,446,018 |
| **α = 0** | **85.00%** |
| α = 255 | 6.92% |
| partial alpha | 8.08% (2,297,495 samples) |
| smallest non-zero α | 1/255 — a ×255 amplification |
| samples whose value changes | 2,283,562 (99.4% of partial-alpha) |
| largest change | 127 of 255 |

α = 0 being 85% of the population is what makes the spec's divide-by-zero NOTE the
dominant case rather than an edge case. And the last two rows are why this is a fix rather
than a refinement: 99.4% of partial-alpha samples move, by up to half the range.

## Decision

**Invert in `decodeSamples`, between `applyDecode` and `toRGB`.** The forward computation
is `c′ = m + α × (c − m)`, so `unblend` applies `c = m + (c′ − m) / α` per component, in
place, on the normalized pre-conversion sample. Alpha is now resolved before the
conversion rather than after it, because it is the divisor.

**At α = 0, choose the matte colour.** The spec permits any in-range value. The matte is
in range by construction — it is a colour in this very space — and it is what `c′` already
holds at α = 0, so a fully transparent pixel keeps the bytes the file gave it instead of
acquiring a fabricated colour. This governs 85% of matted pixels, so "arbitrary" needed a
defensible choice rather than a convenient one.

**Clamp in the sample domain, not through `clamp8`.** The spec requires the result to lie
within the colour space's range, and the division amplifies by 1/α — a factor of 255 at
this corpus's smallest non-zero alpha, where 8-bit rounding in `c′` alone can push a
recovered component past 1.0. `clamp8` would fold an over-range component to full
intensity and flatten the channel ratios; clamping each component to 0..1 first keeps the
ratios that survive.

**Name the exclusions in a public predicate.** `Image.Recoverable` reports whether the
inversion can be performed, and is true for every image that was never blended. It is
false for a DCT base (never decoded, so there is nothing to invert — this is the 5), a DCT
mask (no per-pixel alpha without importing the JPEG decode this package avoids on the base
path), a matte whose length disagrees with the component count, an Indexed parent (the
pre-blending is on the palette), Lab (the matte is in real Lab units, L 0..100, while the
sample is normalized 0..1, because this package applies Lab's scaling in `labToRGB` rather
than through `/Decode` — mixing scales silently), and a matted mask whose dimensions differ
from its parent's, which Table 143 forbids: the mask can still be resampled to supply
alpha, but dividing a colour by a resampled weight invents the weight. `matteFor` defers to
this predicate rather than restating its conditions, so the answer a caller queries and the
one the encoder acts on cannot drift apart.

**Leave `render` alone.** pdfium composites soft masks itself, so the rendered path never
needed this. ADR 0005's "`render`-adjacent utility" is not built and should not be: there
is no second implementation to keep in step.

## Consequences

**The five blocked images still come out pre-blended, and now say so.** `-list` names the
count with its reason, and `Recoverable` is queryable, so the two populations in an output
directory are distinguishable. A DCT base cannot be fixed without decoding it, which is
the codec-preservation promise ADR 0004 made and this ADR does not reopen.

**ADR 0004's "both layers" framing narrows.** The mask is still written as its own file —
that remains useful, and it is the only route to the mask's own samples — but it is no
longer the *only* honest output for the 131. The base PNG's colours are now the colours.

**The corpus split is an assertion.** `TestCorpusSoftMasks` pins 131 recoverable and 5
blocked, and reports the blocked ones keyed by codec pair. A widened exclusion shows up as
a drop in the recoverable count with the reason attached, rather than as images that
quietly stop being corrected.

**The tests build their input with the forward formula.** `TestMatteIsUnblended` blends
known colours per §11.6.5.3 and asserts the decoder recovers them, with a tolerance of
1/α — so the test is about the inversion rather than about hand-computed constants that
would encode the same mistake twice. The non-black-matte case is separate on purpose:
`[0 0 0]` degenerates to a plain division and would hide a dropped `m` term, and every
matte in this corpus is `[0 0 0]`. The `/Decode` ordering needed its own test for the same
class of reason — with the default `/Decode` the remap is the identity, so only an inverting
one can tell the two orders apart, and the corpus contains none.

**α = 0 is unreadable through a premultiplied accessor.** `image/color`'s `RGBA()`
premultiplies, so a fully transparent pixel reads as `0,0,0,0` whatever colour is stored
in it. Its test asserts on `NRGBA` directly. That is not a workaround — it is the spec's
point that the arbitrary value does not affect output, showing up as a property of the
type system.

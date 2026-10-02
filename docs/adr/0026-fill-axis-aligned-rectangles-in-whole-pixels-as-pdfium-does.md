# 26. Fill axis-aligned rectangles in whole pixels, as pdfium does

Date: 2026-09-29

## Status

Accepted. Tests ADR 0025's open hypothesis, and confirms it.

## Context

ADR 0025 made a rectangle clip on the device's axes admit whole pixels, as pdfium's clip does.
The rule was exact in isolation, yet it moved 456 of the 1,103 corpus pages that have such a clip
farther from pdfium. ADR 0025 left one hypothesis untested: pdfium also *fills* such a rectangle
in whole pixels, and this backend antialiased it. If so, those pages were closer before only
because two errors cancelled.

ADR 0025 also mislabelled its figure. "The corpus mean went from 5.06554 to 5.06454" is the mean
over the 1,103 pages with a rectangle clip, not over the corpus. Re-measured with this ADR's rule
off, each of those pages reproduces ADR 0025's figure exactly, and the whole corpus's mean is
4.87658 over 1,251 pages.

## Decision

### The rule: CFX_RenderDevice::DrawPath

When no stroke is visible, `DrawPath` asks `CFX_Path::GetRect` whether the path is a rectangle on
the device's axes. "No stroke visible" means the path is not stroked, or the stroke's alpha byte
is 0. If `GetRect` says yes, `DrawPath` fills whole pixels with `FillRect` and returns.
`path.fillRect` is that computation:

- **The outer bounds**, floor and ceiling, then `FX_RECT::Valid`. A rectangle whose width or
  height does not fit in int32 draws nothing.
- **At least one pixel.** A rectangle less than a pixel wide is one column wide, and one of no
  width at all gets a column added. The same holds for height.
- **One pixel given back.** When the outer bounds are a pixel wider than `ceil(r-l)`, the side the
  rectangle covers less loses its pixel. 10.4 to 20.2 fills columns 11 to 20.
- **int32, as the WebAssembly build does it.** A float that does not fit saturates (`sat32`).
  Every overflow `CheckedNumeric` catches draws nothing. The pixel given back is the case that
  matters: +1 past the widest int32 wraps to the narrowest, which would fill every column where
  pdfium fills none. The column added to a rectangle of no width can wrap too, but only on a
  rectangle 2³¹ pixels off the canvas, so it fills nothing either way.
- **The fill** (`canvas.fillRect`, as `CFX_AggDeviceDriver::FillRect` does it): each pixel is
  merged at the fill's alpha byte, times the clip's coverage and the mask's, through `merge`.
- **No stroke after it.** `DrawPath` returns once `FillRect` has drawn. So `B` or `b` under a
  stroke whose alpha byte is 0 (`CA 0.0039`) draws the fill and nothing else. This backend drew
  that stroke at one level. `fillPath` now reports when the rule drew, and `B` and `b` skip the
  stroke if it did.

The rule is the same one ADR 0025's clip rule uses, and asks the same question (`deviceRect`), so
the two agree on which paths qualify.

### The points, in float32 as pdfium computes them

`GetRect` looks at the points pdfium recorded, transformed by its matrix, and pdfium computes both
in float32. On a fill that difference is enough to move `ceil(r-l)` past an integer. To pdfium,
`150.3 70.6 80 60 re` is 60.000008 tall: `re` adds its width and height in float32. So at 72 dpi
it fills a row more than the same rectangle measured in float64.

So a path now also records each point as pdfium does:

- `re` adds x+w and y+h in float32, as `AddPathRect` does.
- `m` and `l` record their operands in float32.
- Each point goes through pdfium's matrix: the CTM times the display matrix, in float32. That is
  `CFX_Matrix::operator*` and `Transform`, with each product rounded on its own so that no
  platform fuses it.
- The display matrix (`display32`) is `CPDF_Page`'s page matrix for the box and `/Rotate`, then
  `GetDisplayMatrixForFloatRect`'s scale onto the bitmap, which flips y.

`fillRect` asks `GetRect` of those points.

The clip rule still asks of the float64 points rounded to float32, which is what ADR 0025
measured it against. pdfium's clip chain transforms by the CTM and then by the display matrix, not
by their product, so the fill's chain would be wrong there as well.

The CTM is the walker's own, rounded to float32. pdfium concatenates each `cm`, and a form's
`/Matrix`, in float32 as it goes, so the two agree after at most one `cm`. Past that they can
differ in the last bit. This is recorded below, not followed.

## Acceptance

**`TestARectangleFillIsWholePixels`** runs at 72, 144 and 200 dpi against pdfium, measured per
channel. It has 34 cases and four rotations:

- the rule's own shapes: fractional and integer edges, the pixel given back on each side and on a
  tie, `m l l l h` with and without an `h` before it, even-odd, flipped, and scaled, mirrored and
  quarter-turned by a `cm`;
- "a sum in float32", `20.2 10.7 60 53.6 re f`, whose float32 top fills rows 36 to 90 at 72 dpi
  where the float64 one would fill 35 to 89;
- a rectangle off the page, one of no width, one of no height, and ones thinner than a pixel;
- what it fills under: an alpha, a soft mask, a rectangle clip and a sloped clip;
- `B` and `b` under a stroke alpha byte of 0, `f` under one of 1, and `B` under an opaque stroke;
- huge rectangles, ones too wide or too tall for int32 alone, and ones whose pixels are past
  int32 on each side;
- each `/Rotate` of a page whose box is off the origin, with a quarter-turned `cm` on it, at 72,
  150 and 200 dpi. At 150 dpi the rotated page's two scales differ, 2.09 and 2.085. At the other
  resolutions they are equal, so no case could tell the matrix's b term from its c term.

Every case agrees with pdfium on every pixel, with two exceptions:

- **The sloped clip.** Its antialiased edge is held to a mean of 0.02 and a worst of 5.
- **An opaque stroke on `B`.** The rule is off here, and the case pins that: the stroke agrees
  only as far as this backend's stroke does, a worst of 45 at 144 dpi, held to 0.1 and 64.
  Skipping the stroke would be off by the stroke's whole colour.

**`TestSoftMasksAgreeWithPdfium`** gains "hard rectangle edge", a mask group whose shape is a
rectangle. It agrees on every pixel, because pdfium applies the rule inside the group too.

**`TestHairlineNeedsVerticalSampling`** drew its 0.1pt hairline as a `re`. The rule now fills that
as one whole row, as pdfium does, so the test no longer priced vertical sampling. Its hairline
now leans by 0.2pt over its length.

**The corpus**, at 72 dpi, with an off-switch in a scratch copy:

| | pages | mean with the rule off | mean with it on |
|---|---|---|---|
| the corpus | 1,251 | 4.87658 | 3.98128 |
| pages the rule fills on | 682 | 5.62427 | 3.98203 |

The rule fills 72,805 rectangles on 682 pages. 681 pages move closer, 1 does not move, and none
moves farther. No page it does not fill on moves. The largest gain is EC3 page 890: 10.81952 with
the rule off, 2.65841 with it on.

On its own, the float32 chain changes 9 rectangles on 4 pages, and all 4 move closer.

**ADR 0025's hypothesis is confirmed.** Each of the 456 pages the clip rule moved away is now at or
below its figure from before the clip rule.

**Findings, documented and not followed.** Each is read from `DrawPath` or measured on a fixture.
No corpus page has been shown to need one. ADR 0027 follows the first four.

- **A path of two points with no visible stroke** goes to `DrawCosmeticLine` before `GetRect` is
  asked, and is drawn as a one-pixel line in the fill colour. This backend fills nothing.
- **A zero-area subpath of a fill** goes to `DrawZeroAreaPath`, which draws it as a line.
  `x y 0 0 re f` is one pixel at 192 in pdfium, and nothing here.
- **A stroke alpha byte from 1 to 254 on a filled path** goes to `DrawFillStrokePath`. That draws
  the fill and the stroke into one layer, as a knockout group, and composites the layer. This
  backend composites each on its own, so under the stroke the fill shows through.
- **Alpha as a byte.** A fill's or stroke's alpha is a float in this backend and a byte in
  pdfium. Under `CA 0.0039`, pdfium's stroke draws nothing, and this backend's draws one level.
- **An integer literal past int32** reads as 0 in pdfium. The past-int32 fixtures therefore
  write `3000000000.0`.
- **The CTM past one `cm`** is this backend's float64 product rounded to float32, not pdfium's
  running float32 product. A form's `/Matrix` is the same case.
- **The clip's chain** is left as ADR 0025 measured it.

**Mutation:** 65 mutants cover these areas:
- `fillRect` and `sat32`;
- the float32 points of `re`, `m`, `l` and `h`, and of the move a segment after `h` makes;
- `mat32` and `display32`;
- the walker's routing of `f`, `B` and `b`;
- `canvas.fillRect`.

Each ran under a 4 GB commit watchdog, and none was killed by memory: the peak is 0.50 GB. 62 are
killed, each by a named test. Three live, and each is equivalent:

- **`sat32`'s lower bound.** Without it, a float below int32 converts as Go's implementation
  does, which gives the smallest int32 on amd64, 386 and arm64. That is what the case returns. It
  stays, because the language does not promise it.
- **`sat32`'s NaN case.** NaN reaches `sat32` as a width or a height, from ∞ − ∞, where 0 and the
  smallest int32 are both below 1, so either becomes one pixel. It reaches it as an edge only from
  0 × ∞, when a coordinate is past float32. The matrix's other term for that coordinate makes the
  other axis infinite, and a rectangle there fills nothing either way.
- **`canvas.fillRect`'s clip test.** `merge` at a coverage of 0 writes the pixel back unchanged.
  The test only skips the work, as `fill`'s masked path does.

A first pass of 69 had 17 survivors and one mutant that did not compile. The survivors became
these tests:
- "covered alike on each side", the tie in the pixel given back;
- "a sum in float32";
- "huge across" and "huge down the page", each too big for int32 on one axis only;
- `m h l l l h`;
- the rotated pages at 150 dpi, with a quarter-turned `cm`.

The first rectangle added for the matrix's b and c terms moved by a column under each mutant and
killed neither. It lay inside the page's first rectangle, in the same colour. The one in the test
is where nothing else is drawn.

Two survivors were code that could not matter, and it is gone rather than tested:
- `fillRect`'s swap of reversed bounds, which `deviceRect` has already ordered;
- the pen's float32 copy, which was read only where it equals the subpath's start.

## Consequences

- **A rectangle is drawn by two routes.** One on the device's axes with no visible stroke is filled
  in whole pixels through `merge`. Every other path is rasterized with coverage, as before. The
  choice follows pdfium's `DrawPath`, which is the only reason both routes exist.
- **Paths carry pdfium's points beside their own.** Each edge records its end as pdfium computes
  it, which is 8 bytes more per edge. Only `fillRect` reads them, so the ones on the clip that
  `rect` builds for a soft mask's or a form's `/BBox` are never read.
- **The corpus mean fell by 0.9**, from 4.87658 to 3.98128.

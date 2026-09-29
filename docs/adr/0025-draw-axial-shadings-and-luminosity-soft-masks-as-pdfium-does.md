# 25. Draw axial shadings and luminosity soft masks as pdfium does

Date: 2026-09-29

## Status

Accepted. Draws two things every earlier ADR refused: `sh`, and an ExtGState `/SMask` other
than `/None`.

## Context

ADR 0024 left one page, ISO TS 32005 page 1. Its TargetStream logo is an axial shading drawn
through a luminosity soft mask whose group is itself an axial shading. The page has nothing else
the backend refuses. Nothing else in the corpus uses `sh` or a soft mask.

§8.7.4.5.3 defines an axial shading as a function of the projection onto its axis. §11.6.5.2
defines a luminosity mask as a group rendered over its backdrop and converted to luminosity.
Neither says how to sample the function, where a pixel is measured, or how to round, and pdfium
answers each. The rules below are pdfium's, and each was checked against the go-pdfium build on
fixtures made for the rule, since one page can separate few of them.

## Decision

### `sh`: pdfium's DrawAxialShading

- **256 steps.** Step i samples the functions at i/256 of the way along `/Domain`, in float32 as
  `GetShadingSteps` does. Each component is clamped to 0..1 and each channel is roundf of 255
  times the float32.
- **A pixel's corner, truncated.** Each device pixel's corner, not its centre, is mapped back into
  shading space and projected onto the axis. The projection times 255, truncated, is the step. A
  spike over a 256-pixel gray ramp found this rule agreeing with pdfium on every pixel. The centre
  disagreed on 127 pixels and steps of i/255 on 126.
- **The ends.** A projection before the start or past the end takes the end's step if that end
  is extended, and leaves the pixel alone if it is not. pdfium truncates with an int32 cast, which
  the WebAssembly build saturates: a projection past 2³¹ is `INT_MAX` and takes the end's rule,
  and NaN is 0 and takes the first step whatever `/Extend` says. This was measured on an axis
  10⁻⁷ long, where every pixel past the first overflows and pdfium draws the extended end. An x86
  build gives `INT_MIN` there, and would draw nothing.
- **The matrix, in float32.** The matrix is the CTM times the page's matrix. Each product and sum
  is rounded to float32 as `CFX_Matrix::operator*` rounds it, then translated to the bitmap
  `CPDF_DeviceBuffer` draws into. That bitmap's first pixel is the clip's box, the first column
  and row the clip admits (`mask.box`). `inverse32` is `CFX_Matrix::GetInverse`, each product
  converted on its own line so that no target fuses it.
- **The composite, in integers.** The steps are composited as pdfium composites the ARGB bitmap it
  drew them into (`CompositeRow_Argb2Rgb`). The clip's coverage scales the alpha, then
  `FXDIB_ALPHA_MERGE` blends, each step truncating. The constant alpha is `FXSYS_roundf(255*ca)`.
  The float blend put half the pixels of a half-alpha ramp one level off.
- **The colour.** A device space converts by its formula, and an ICC space through the new
  `icc.Profile.Translate`, which is `IccTransform::Translate`: each component truncated to a byte,
  then converted, so each channel is a whole 255th. An sRGB profile keeps the clamped colour
  unquantized, as `CPDF_ICCBasedCS::GetRGB` does.
- **The functions.** They come from the new `function` package: types 2 and 3 in float32, parsed
  and called as `cpdf_expintfunc.cpp` and `cpdf_stitchfunc.cpp` do. The package bounds nesting at
  8 levels and a tree at 256 functions, and refuses a power or output past float32 at a
  `/Domain` end. An axial shading takes one function of n outputs, an array of one, or an array of
  n functions of one output each, as `ValidateFunctions` does.

### A rectangle clip: whole pixels

`sh` exposed a rule every earlier fixture had been too soft to see. `CFX_AggDeviceDriver::
SetClip_PathFill` does not rasterize a path that `CFX_Path::GetRect` calls a rectangle on the
device's axes. It clips to the path's outer bounds, so a clip edge at 27.8 device pixels admits
all of row 27. At 200 dpi a shading under `10 10 180 80 re W n` differed from pdfium by up to 245
levels along the clip's edges before the rule.

`path.pixelRect` asks `GetRect`'s question of the points pdfium's parser would have recorded:
a move, then the end of each line, in float32. `re` makes five points, and so does `m l l l h`.
More than five are first rid of the lines that go nowhere, as `GetNormalizedPoints` does, keeping
a closing line. Only one subpath with no curve can qualify. `rectMask` admits the result.
`TestARectangleClipAdmitsWholePixels` has 17 shapes. Nine are ones `GetRect` accepts, and each
agrees with pdfium on every pixel: among them a rectangle of no width, which admits one column,
and one past the page's edge. Eight are refused, one per rule of `GetRect`'s: turned, a curve, an
edge askew, five points not closed, the second and fourth points equal, a closing line kept by its
close flag, and no edges. Those are rasterized, and agree to 5 levels where whole pixels would be
some 180 off. More than five points that do not return to the start need no test of their own:
what normalization leaves is five points whose fifth is the last, so the five-point test refuses
them, and the separate guard was deleted.

The rule applies to every clip, so it moves pages that have no shading. At 72 dpi, on the 1,250
pages drawn before this ADR, it was measured with an off-switch. 1,103 pages have a rectangle
clip. Of those, 131 moved closer to pdfium, 456 moved farther and 516 did not move. The corpus
mean went from 5.06554 with the rule off to 5.06454 with it on. The largest move away was 0.015,
on EC3 page 269 (8.05126 to 8.06596). No page without a rectangle clip moved. The rule is kept
because it is exact in isolation.

The regressions are unexplained. One hypothesis is untested: pdfium also *fills* an axis-aligned
rectangle in whole pixels, which this backend does not do yet (below). If so, a page whose
rectangle fills sit inside rectangle clips was closer before only because two errors cancelled.

### A luminosity soft mask: pdfium's LoadSMask

- **The reading.** `/SMask` is read as `LoadSMask` reads it, and refused wherever pdfium would draw
  something this backend does not. `/TR` is used only when it is a dictionary or a stream;
  anything else, `/Identity` included, is the identity. `/BC` is read in the group's `/CS`, each
  channel the float32 times 255 truncated, as `GetBackgroundColor` does. A group with no `/BC`
  is drawn over black.
- **The group.** It is drawn onto a page-sized canvas cleared to the backdrop, unclipped but for
  its `/BBox`. The CTM is the one in force at the `gs`, times the group's `/Matrix`. The graphics
  state is the initial one: black, DeviceGray, alpha 1, the default pen, no font, no mask. The
  resources are the group's own `/Resources`, or else the page's, as `LoadSMask` gives it the
  page's wherever the `gs` is.
- **The mask.** Each pixel is `(30r + 59g + 11b)/100` in integers. It is computed once, at the
  `gs`, and not per object as pdfium computes it. pdfium draws only the part of the group each
  object's box covers, and those parts agree. `q` saves the mask and `Q` restores it, as
  §11.6.5.2 makes it graphics state. `/None` ends it.
- **The composite.** pdfium draws a masked object into an unclipped layer, multiplies the layer's
  alpha by the mask (`MultiplyAlphaMask`), and composites it through the clip. `canvas.merge` does
  the same per pixel, in that order, in integers, each step truncating. The mask comes first, then
  the clip's coverage, then `FXDIB_ALPHA_MERGE`. Fills, strokes, glyphs and shadings all go
  through it.
- **Two alphas.** A fill's or stroke's ARGB alpha is `static_cast<int32_t>(alpha*255)`, truncated
  and "not rounded" (`cpdf_renderstatus.cpp`). A shading's is `FXSYS_roundf(255*alpha)`. Under a
  half alpha the two differ by one level, and the fixtures see it.
- **The bound.** `maxPageAreas`: each `sh` and each soft-mask `gs` is a pass over the whole page,
  which nothing else bounds. A page may make 64 passes. At 200 dpi a Letter page is 3.7 million
  pixels, so that is at most 240 MB of masks, or 240 million pixels shaded. The corpus's one page
  with either makes two.

### Refused by reason

| shading or mask | refusal |
|---|---|
| `sh` of a name not in `/Shading` | `sh: /%s is not in the page's /Shading resources` |
| a shading that is not a dictionary or stream | `sh: /%s is not a shading dictionary` |
| shading types other than 2 | `sh: /%s is shading type %d, and only axial shadings (type 2) are drawn` |
| no `/ColorSpace` | `sh: /%s has no /ColorSpace` |
| a space a fill refuses (Pattern, Indexed, …) | the fill's refusal |
| DeviceCMYK | `sh: /%s is in DeviceCMYK, which pdfium converts by Adobe's table rather than §8.6.4.4's formula` |
| `/Coords` not four numbers | `sh: /%s has no /Coords of four numbers` |
| `/Coords`, or the axis's squared length, past float32 | `sh: /%s has /Coords past the float range pdfium holds them in` |
| an axis of zero length | `sh: /%s has an axis of zero length` |
| `/Domain` or `/Extend` malformed | `sh: /%s has a /Domain that is not two numbers`, `… a /Domain past the float range pdfium holds it in`, `… an /Extend that is not two booleans` |
| `/BBox` | `sh: /%s has a /BBox, which pdfium clips to in whole device pixels` |
| `/Function` missing, of the wrong shape, or refused by `function` | `sh: /%s has no /Function`, `…'s /Function …` |
| a CTM that maps the shading to a line or point | `sh: /%s is drawn under a CTM that maps it to a line or a point` |
| more than 64 page-area passes | `sh:` or `gs: the page makes more than 64 passes over its whole area` |
| `/SMask` neither `/None` nor a dictionary | `gs: /SMask is neither /None nor a dictionary` |
| a key other than Type, S, G, BC, TR | `gs: /SMask /%s is not a key this backend reads` |
| `/S` missing or not `/Luminosity` | `gs: /SMask /S is missing, …`, `gs: /SMask /S is /%s, and only /Luminosity is implemented` |
| `/G` not a form | `gs: /SMask has no /G group` |
| `/TR` a function | `gs: /SMask /TR is a transfer function, which is not implemented` |
| `/BC` without a group `/CS`, in a space other than DeviceGray or DeviceRGB, of the wrong count, or outside 0..1 | `gs: /SMask /BC …` |
| a knockout or otherwise refused group | `gs: ` and the form's refusal, with `/SMask /G` |
| a soft mask inside a group | `gs: a soft mask inside a soft mask's group` |
| an image inside a group | `Do: an image inside a soft mask's group, /%s` |
| an image or form under a mask | `Do: an image drawn under a soft mask, /%s`, `Do: a form drawn under a soft mask, /%s` |
| `B`, `B*`, `b`, `b*` under a mask | `B: a path filled and stroked under a soft mask, which pdfium masks as one object` |

A form under a mask is refused because pdfium masks the form's layer as one object. An image is
refused because the backend's image path does not composite through `merge`. A path filled and
stroked is refused for the same reason as the form: pdfium draws the fill and the stroke into one
layer and masks it, so where the stroke covers the fill, the fill does not show through. The
corpus page needs none of these.

## Acceptance

**Fixtures against pdfium**, at 72, 144 and 200 dpi, measured per channel (`compareRGB`), since
a red-to-blue ramp is one ink throughout:
- **`TestAxialShadingsAgreeWithPdfium`**, 21 cases. They cover:
  - each device space and an ICC one;
  - each `/Extend`, and `/Domain`;
  - a squared, a root and a default function, a stitched one, and one function per component;
  - an axis that is diagonal, reversed, rotated or stretched;
  - a constant alpha, an axis 10⁻⁷ long, a CTM so small the projection is NaN, and no clip;
  - a curved clip.

  Every case agrees with pdfium on every pixel, except the curved clip. That clip is rasterized
  with coverage, and its edge pixels differ by up to 19 levels, as a plain fill under the same
  clip does. It is held to mean 0.07 and worst 20.
- **`TestSoftMasksAgreeWithPdfium`**, 22 cases. They cover:
  - a ramp, a hard edge, the group's `/BBox`, a gray and an RGB backdrop, half gray, colour;
  - a red fill, a stroke, a shading, a clip and an alpha under the mask;
  - `q` saving the mask, `Q` and `/None` ending it, and a fill before the `gs`;
  - a CTM before and after the `gs`, the group's `/Matrix`, a group drawn from the initial
    state, one drawn from the page's resources for a `gs` inside a form, and a path begun before
    the `gs` and filled after it.

  Every case agrees on every pixel except three whose sloped edges are antialiased: the hard edge
  (0.0117 and 6 at 200 dpi), the RGB backdrop's triangle (3) and the stroke (1). That is the
  coverage of those edges, which this backend computes in float and AGG in its own cells. The
  three are held to 0.012 and 6.
- **`TestASoftMaskedGlyphAgreesWithPdfium`**, at 0.04 and 63. The same string unmasked differs by
  up to 238 at 200 dpi, at the fixture font's outline edges. "A" alone under the mask is exact
  at 72 dpi.
- **`TestAPageOfMoreThanItsAreaPassesIsRefused`** holds 64 passes drawn and the 65th refused, by
  `sh` or by `gs`.

**The page.** ISO TS 32005 page 1, against pdfium:

| region | dpi | mean | worst | pixels past 8 |
|---|---|---|---|---|
| whole page | 72 | 1.7068 | 208 | 21,900 of 501,832 |
| whole page | 200 | 0.7349 | 253 | 68,447 |
| TargetStream logo box | 72 | 0.776 | 59 | |
| TargetStream logo box | 200 | 0.268 | 37 | |

The whole page's differences are glyph and logo-outline antialiasing, categories earlier ADRs
already record. The logo box holds the masked shading and the text over it.

**Findings, documented and not followed:**
- **pdfium fills an axis-aligned rectangle in whole pixels.** `CFX_RenderDevice::
  DrawPathWithBlend` takes `GetRect`, then `GetOuterRect`, drops the less-covered edge, and fills
  with `FillRectWithBlend`. This backend antialiases it. `40 20 100 50 re f` at 200 dpi has pixel
  x=111 at 0 in pdfium and 51 here, and row 83 at 0 and 96, with no mask. The soft-mask fixtures
  therefore take their hard edge from a triangle. This is the likeliest next rule, and the test
  of the hypothesis above.
- **The masked shading's origin.** Under a mask, pdfium draws the shading into the layer, whose
  origin is the layer's rectangle, not the clip's box. This backend uses the clip's box in both
  cases. The fixture's masked shading agrees on every pixel, so the difference has not been seen.
- **`pixelRect` asks its question of device-space points.** pdfium asks `GetRect` of the path's
  user-space points and the matrix. Where the two can disagree has not been measured.
- **CMYK.** A shading's DeviceCMYK is refused because pdfium converts it by Adobe's table. That
  table is 16 levels from §8.6.4.4's formula on average over a ramp. A fill's CMYK is the formula
  here and is as far off. A fill is one colour, though, and every corpus page with one draws.
- **ICC fills** still convert through `SRGB`, exactly. Shadings go through `Translate`, quantized as
  pdfium quantizes them. Whether fills should too has not been measured.

**Mutation:** 113 mutants cover these areas:
- the soft mask's reading, its group and its state;
- the shading's reading, its refusals and its steps;
- the rectangle clip;
- the paint pass under a mask;
- `function`'s type 2 reads;
- `icc.Translate`.

Each ran under a 4 GB commit watchdog, and none was killed by memory: the peak is 0.50 GB. 111
are killed, each by a named test. Two live:

- **Coverage truncated, not rounded, under a mask.** The masked paint takes coverage as
  `int(math.Round(v*255))`. Truncating moves a partly covered pixel by at most one level, and the
  fill's own edges are further from pdfium than that: coverage is sampled 16× per pixel row (see
  DESIGN), and a fractional horizontal edge is 50 in pdfium and 48 here with no mask at all. The
  only trace in the fixtures is "stroke" at 200 dpi, a mean of 0.00107 against 0.00121. That is
  too close to pin, and no fixture can separate the two until edge coverage agrees.
- **`float32(a*d) - float32(b*c)` in the inverse.** The conversions stop the compiler fusing
  the products into an FMA. amd64 at `GOAMD64=v1` and 386 do not fuse, so the mutant is
  equivalent on every target the gate runs. On arm64 it is not.

A first pass of 115 had 34 survivors and 5 mutants that did not compile. The survivors became
these tests:
- the soft-mask cases "saved by q", "fill before gs", "group from the page's resources" and
  "path across gs", and the "group an image" refusal;
- seven more refused shapes and three more accepted ones in the rectangle-clip test;
- the "CTM so small the projection is NaN" shading case;
- the float-range refusals for `/Coords` and `/Domain`, and the `/Encode` length and power
  endpoints in `function`;
- `TestTranslateQuantizesAsPdfium`.

Three survivors were equivalent, and their code is gone rather than tested:
- a survey path reset that the paint pass repeats;
- `pixelRect`'s check that a path ends where it starts, which the five-point test already makes;
- `Translate`'s gray branch, since a gray profile reads only the first channel.

The power check in `function` tested both endpoints under one message, and no test reached the
high one, so dropping it lived. The error now names the endpoint that overflowed, and a test pins
each.

## Consequences

- **`render/native` draws all 1,251 of the corpus's 1,251 pages.** The corpus test's floor is
  1,251, and the worklist ADR 0015 began is empty for this corpus.
- **`function` is a package of its own.** It is pdfium's function reading, for types 2 and 3. Types
  0 and 4 are refused, and nothing in the corpus uses them.
- **A mark's composite is now in two places.** The unmasked float path is unchanged, and the
  masked integer one is `merge`. A mark that learns pdfium's integer composite unmasked would
  move to `merge`, and that is what the rectangle-fill rule above would need.

# 28. Stroke with AGG's outline, and sum coverage where outlines overlap, as pdfium does

Date: 2026-10-15

## Status

Accepted. Follows two of ADR 0027's findings: coverage summed across a path's overlapping outlines,
and the joins, which differed from AGG's by up to 45 levels at a corner.

## Context

This backend stroked a path as pieces: a convex polygon for every segment, join and cap, all wound
one way. Nonzero filled their union, so where pieces overlapped they counted once.

pdfium strokes with AGG's `vcgen_stroke`, and fills the result with AGG's `fill_non_zero`. Both
differ from the pieces:

- **The outline.** `vcgen_stroke` makes one polygon around each open polyline: a cap, the joins
  along one side, the other cap, and the joins back along the other side. A closed polyline is two
  polygons, one for each side. On the side inside a turn, a join is the miter when that lies within
  1.01 of the pen's radius (`inner_miter`). Otherwise it is the two segments' ends, so at a sharp
  turn the outline folds back over itself.
- **The sum.** `fill_non_zero` sums every edge's signed area in a pixel, takes its size, and clamps
  it to a whole pixel. Two outlines that overlap and wind alike add where they meet, and so does a
  fold. Nonzero winding covers their union once.
- **The arc step.** `CFX_AggDeviceDriver::DrawPath` strokes under `matrix1`, the CTM's uniform scale
  by max(|a|, |b|), and AGG cuts an arc at the step for that radius:
  `2·acos(r / (r + 1/8))`.

ADR 0027 already summed one set of hairlines (`ruleSum`), and nothing else.

## Decision

### The outline is `vcgen_stroke`'s

`polyline` takes the points in the order `vcgen_stroke`'s `vertex` takes them (`stroke.go`):

- **open:** `capAt` at the start, `join` at each corner going out, `capAt` at the end, and `join`
  at each corner coming back, then `end`;
- **closed:** `join` at every point going round, `end`, then `join` at every point going back the
  other way, `end`.

A closed polyline of fewer than three points is stroked open, as before.

The vertices are AGG's own computations:

- `offset` is `stroke_calc_join`'s offset of a segment;
- `join` is `stroke_calc_join`;
- `miter` is `stroke_calc_miter` under `miter_join_revert`;
- `intersection` is `calc_intersection`, with AGG's 1e-30;
- `capAt` is `stroke_calc_cap`;
- `arc` is `stroke_calc_arc`.

`RasterizeStroke` gives AGG a round join for round, a bevel for bevel, and `miter_join_revert` for
every other join. So a miter over its limit is a bevel, as before. The inner side takes a miter
within `innerMiter`, 1.01, of the radius, or else the two segments' ends.

The outline is built in pen space and taken to the device by `end`, as the pieces were.

### An arc's step is matrix1's

The step is AGG's for the pen's radius in `matrix1`'s space, `hw · max(|a|, |b|)`. It was the
pen's largest radius on the device, `hw · max(‖(a, b)‖, ‖(c, d)‖)`, which cut a round cap more
finely than pdfium under a turned or stretched CTM.

The step is floored at π/64, `maxArcSteps` to a half turn. AGG floors a cap's step and not a join's,
so a pen a million pixels wide would cut each join into thousands of chords. Past a radius of about
415 pixels, this backend's chords fall 0.0003 of the radius inside the arc where AGG's fall an
eighth of a pixel: a fifth of a pixel at a radius of 650, and three pixels at 10,000. The floor
leaves pdfium's step unchanged below 415.

### Nonzero sums

`scan` weights each nonzero span by the size of its winding number, and the callers clamp a pixel's
sum to 1 as before. Even-odd is unchanged. `fillRule`, `ruleOf`, `ruleSum` and `paintScan` are gone.
`scan` takes `evenOdd` again, and hairlines are an ordinary nonzero fill.

This applies to every nonzero fill, clip and stroke:

- a fold inside a sharp turn counts twice;
- so do two subpaths, or two dashes, where they overlap;
- so do two edges of one fill in one pixel;
- the knockout's stroke sums too, which was ADR 0027's live mutant.

Under a translucent stroke this changes no pixel inside the stroke, because the sum clamps there.
It changes the edge pixels two outlines both cover.

**It is not AGG's sum in every pixel.** AGG sums *signed* areas, so in a pixel that holds windings
of both signs they cancel, where this backend adds each span's |winding|. Two rectangles wound
opposite ways differ by 61 at the two corners where both rectangles' edges cross. Following it would
mean replacing the scanline's spans with AGG's per-cell cover and area. No corpus page's mean moved
the wrong way (below), so that is left as a finding.

## Acceptance

Each case runs at 72, 144 and 200 dpi against pdfium.

| case | worst before | worst after |
|---|---|---|
| a segment after `h` | 59 | 4 |
| the knockout under an opaque stroke | 41 | 4 |
| `B`, `B*`, `b`, `b*` | 28 | 3 |
| a round cap on a line that goes nowhere | 25 | 5 |
| a closed path that crosses itself | 35 | 4 |
| round dashes and dots | 22 | 3 |
| dashes that go back over each other | 683 pixels over 32 | none over 32 |
| two rectangles whose edges share pixels, 72 dpi | 398 pixels over 32 | none over 32 |

Bounds tightened where the residual went:

- **`TestStrokesAgreeWithPdfium`**: every mean is at most 0.031. Elsewhere no pixel differs by more
  than 8, except curves, whose flattening differs by up to 37. "A segment after h" and "a rotated
  non-uniform CTM" are now held to 0.03 with none over 32. New cases cover two subpaths along one
  line, two lines that cross, a closed path that crosses itself, a sharp turn, and a tight curve.
  Three more came from the mutation pass. One is a short segment after a 15° turn under a pen 100
  wide: the inner miter reaches past the short segment's end, and the two ends instead differed
  from pdfium by 255. Another is a triangle with round joins, whose three arcs go all the way
  round, so one crosses atan2's wrap. A third goes straight on at the page's top left, which is pen
  space's origin. A fourth came from review: a round join on three points in a line to within
  rounding.
- **`TestDashesAgreeWithPdfium`**: dashes that go back over each other.
- **`TestFillsAgreeWithPdfium`** gains four cases:
  - two rectangles whose edges share pixels;
  - two rectangles wound opposite ways, with at most 2 pixels over 32: the two corners that differ
    by 61;
  - a clip of two rectangles whose edges share pixels;
  - three nested rectangles under even-odd, the innermost at a winding of 3.
- **`TestFillAndStrokeKnockOut`**: each worst is now 0 to 8, except the stars, below.
- **`TestAStrokeThatGoesNowhereIsAPixelLong`**: each worst is now at most 7.
- **`TestAnArcIsCutInAtMostMaxArcSteps`** pins the floor. A million-pixel pen's right-angled corner
  is two caps of 65 vertices each, a quarter-turn join of 33, and two segment ends.
- **`TestAnArcIsCutAtMatrix1sRadius`** pins the arc step's radius. Under a CTM that doubles and
  turns an eighth, the step for matrix1's radius cuts a cap into 7 vertices; the device's radius
  would cut it into 8.
- **`TestAJoinsArcEndsAQuarterStepShort`** pins the join arc's two bounds:
  - it stops a quarter step short of its end;
  - it turns across atan2's wrap.

  The test turns a corner an eighth of a step past a quarter turn, in eight directions.
- **`TestANearlyStraightRoundJoinHasNoArc`** pins an arc that turns the other way and takes no
  step. Its three points are in a line to within rounding, and the outline has 8 edges. Turning the
  way the angle grows drew nearly a whole circle, 21 edges.
- **`TestARoundJoinAtAReversalTurnsAsAGGDoes`** pins the other way's loop. Going back to its
  start, a path's two arcs both go round the tip, 18 vertices past it. Going back to an ulp short of
  it, the join going out turns back over the line and the tip is flat, as `stroke_calc_arc` makes it
  from those angles.

**The corpus**, at 72 dpi, against HEAD, with each new rule counted in a scratch copy. The mean went
from 3.98125 to 3.98114. 32 pages moved closer, none farther, and 1,219 did not move. No page moved
without a counter firing on it.

| counter | pages | moved |
|---|---|---|
| a polyline stroked | 180 | 31 |
| a fold, the inner side's two ends | 30 | 30 |
| a span of winding 2 or more | 31 | 31 |

- **Well-Tagged PDF page 3** accounts for −0.094 of the −0.134. It strokes 5,098 polylines with no
  fold and no overlap, so what moved it is the outline's own shape.
- **The other 31** are 23 EC3 pages and the covers and logos of eight more documents. Each moved
  less than 0.007. All 31 have summed spans and 30 have folds. EC3 page 504 has no fold, and its
  summed spans are a fill's.
- **149 pages** stroke and did not move.

## Mutation

37 mutants were run against `stroke.go`, `fill.go` and `paint.go`, each in a scratch copy under
a 4 GB cap. 29 were killed by a named test, and 8 survived.

**Six survivors were real gaps, and each now has a killer:**

| mutant | killer |
|---|---|
| `innerMiter` 1 | "a short segment after a gentle turn" |
| the arc step at the device's radius | `TestAnArcIsCutAtMatrix1sRadius` |
| no 2π wrap in a join's arc | `TestAJoinsArcEndsAQuarterStepShort`, "round joins all the way round", `TestARoundJoinAtAReversalTurnsAsAGGDoes` |
| no quarter step short in a join's arc | `TestAJoinsArcEndsAQuarterStepShort`, `TestARoundJoinAtAReversalTurnsAsAGGDoes` |
| even-odd weighed by the winding | "three rectangles nested, even-odd" |
| no clamp in a clip's coverage | "a clip of two rectangles whose edges share pixels" |

**A seventh survivor dropped the half-turn bound from `stroke_calc_arc`'s other way.** `arc`
turns the other way when o1's angle is within a half turn above o2's. A join's offsets turn with
the path, and an arc is only the join outside a turn, so the whole branch looked unreachable, and it
was deleted. Review found that it is reached wherever join's side test and the two angles disagree:

- on three points in a line to within rounding, where the side test is 0 and the angles are 1e-16
  the other way. Without the branch the arc there went nearly all the way round, where pdfium draws
  no circle.
- going back to an ulp short of a path's start, where the angles differ by just under π. The arc
  then goes back over the line, and the tip is flat.

The branch is restored. The survivor is now killed by `TestARoundJoinAtAReversalTurnsAsAGGDoes`,
`TestAJoinsArcEndsAQuarterStepShort` and "round joins all the way round". Four more mutants were
taken for the branch, and all four are killed:

| mutant | killer |
|---|---|
| the whole branch gone | `TestANearlyStraightRoundJoinHasNoArc`, "a round join on three points nearly in a line", `TestARoundJoinAtAReversalTurnsAsAGGDoes` |
| no quarter step short | `TestARoundJoinAtAReversalTurnsAsAGGDoes` |
| no first step | `TestARoundJoinAtAReversalTurnsAsAGGDoes` |
| a half step | `TestARoundJoinAtAReversalTurnsAsAGGDoes` |

**The eighth was code that changed nothing, and it is deleted.** It is `stroke_calc_miter`'s
straight-on case. Where the offset edges are parallel and the path goes on, AGG adds the one end the
segments share. The fallback adds both ends, which are the same point to within rounding. A
difference needs two segments at an angle whose lengths multiply to under 1e-30, which AGG's
`calc_intersection` takes as parallel.

This backend applies that 1e-30 in pen space, where AGG applies it in `matrix1`'s. AGG also drops a
point within 1e-14 of the one before it, and pdfium's points are float32, so points that close on
the device are one point before AGG sees them. This backend drops only a point equal to the one
before it. So segments that short can reach the fallback here and not in AGG. They are about 1e-15
long, a pixel only under a CTM that scales by 1e15.

Two mutants were then added for the miter that was left, and both are killed:

- a miter whose fallback adds one end, by `TestAMiterAtExactlyItsLimitIsKept` and ten more;
- a meeting point read when the edges are parallel. "Going straight on at the page's top left" kills
  it, because pen space's origin is there, and the zero point an absent meeting returns is within
  the miter limit.

The one miter mutant that was re-anchored, a miter at its limit taken as over it, is killed again.

**All 41 mutants were then run again against the final code, and every one is killed by a named
test.** That is the 37, less the two on the deleted straight-on case, plus the two miter mutants
and the four on the arc's branch.

## Findings, documented and not followed

- **pdfium clips a knockout to its stroke's box, which cuts off a miter at a figure's start.**
  `DrawFillStrokePath`'s layer is `GetBoundingBoxForStrokePath`'s box (`cfx_path.cpp`). That box
  gives a closed figure's first point a cap and never its join, with a half width of the line's
  width. So a miter at that point can reach past the layer, and pdfium cuts its tip off. At the
  star's top point, the box ends at y 98.83 and the tip is at 99.5. This backend draws the tip, and
  the star cases differ there by 20 at 144 dpi and 45 at 200. Following it means porting that box as
  the layer's clip. It is a candidate ADR.
- **Curve flattening** is the remaining residual on curves: up to 37 at 144 dpi and 36 at 200 on a
  stroked curve, and the same on filled ones. pdfium flattens with AGG's `curve4`. It is a
  candidate ADR.
- **A pixel that holds windings of both signs**, as above: AGG cancels them, and this backend adds.
- **A round join at a reversal.** pdfium's AGG computes the stroke in float32. Where a path turns
  straight back, the two offsets' angles then differ by just under π in some directions.
  `stroke_calc_arc` takes that as the other way round, so the arc goes back over the line and the
  tip is left flat. Take a line 60 long from (100, 100) and back, under a pen 20.5 wide with round
  joins, at 72 dpi and 3° steps: pdfium leaves 33 of 120 tips flat, and this backend none. Going
  only half way back, it is 6 and none.
  - **Back to its own start**, the difference in float64 is ±π exactly, so this backend draws every
    such tip round, as §8.4.3.4 does. No random reversal of 20,000 took the other way.
  - **Back to another point on the line**, or to an ulp short of the start, float64 can make the
    choice float32 makes, and this backend's tip is flat too. 28 of 20,000 random reversals that go
    half way back took the other way.

  Following pdfium would mean stroking in float32, in matrix1's space.
- **The 200 dpi edge row.** Any line's edge differs from pdfium by about 8 levels at 200 dpi, and
  by twice that where two outlines cover it. This predates the change.

## Consequences

- **A stroke is one outline per open subpath and two per closed one**, where it was a polygon per
  segment, join and cap. A long polyline adds fewer edges than before.
- **Overlapping coverage adds everywhere nonzero is used**, not only in hairlines. A clip whose
  subpaths overlap is darker at their shared edge pixels, as pdfium's AGG clip is.
- **`maxArcSteps` departs from AGG** for a round join of radius past about 415 pixels in matrix1's
  space. This bounds the cost of a huge pen.

# 27. Draw paths as pdfium's DrawPath does: the knockout, the one-pixel lines, and lines that go nowhere

Date: 2026-10-02

## Status

Accepted. Follows four of ADR 0026's findings: the cosmetic line, the zero-area path, the knockout,
and alpha as a byte. It also follows two rules found while pinning them: BuildAggPath's line a
pixel long, and AGG's open stroke of a closed subpath of two points.

## Context

ADR 0026 routed one of `CFX_RenderDevice::DrawPath`'s rules, the whole-pixel rectangle, and read
the rest without following them. Each of the rest decides what a path draws from questions this
backend did not ask:

- how many points pdfium's content parser recorded;
- whether a subpath has no area;
- whether the fill's and the stroke's alpha *bytes* are 0 or 255.

The answers turn on the parser's bookkeeping: a move after an open move replaces it, `h` adds a
line only when the pen is away from the start, and so on. The edges this backend fills do not keep
that bookkeeping.

## Decision

### The recording

A path now keeps pdfium's points beside its edges (`recording`, `points.go`). They are the operands
in float32, in user space, each with its type and whether it closes its figure, kept as
`CPDF_StreamContentParser` keeps them:

- A move after an open move replaces it. A trailing open move is dropped when the path is painted.
- A line or curve with no point before it records nothing.
- `h` adds a line back to the start only when the pen is away from it, and marks the last point
  closed otherwise. `b*` adds that line either way.
- `re` is a move, three lines and a closing line, with its far corner summed in float32.
- `AddPathObject`: a lone point is no path, except a move that `h` closed under round caps. pdfium
  draws that one as a line to itself.

Every question below is asked of the recording. The edges are still what is filled.

The recording replaces ADR 0026's float32 fields on each edge. `closeFor`, the close a painting
operator makes, now closes only the recording. `scan` already fills every subpath closed, and the
stroke is drawn from the recording, so closing the edges as well changed nothing. Mutation showed
that, and the edge close is gone.

### DrawPath, in its order

`drawPath` takes the first rule that applies:

1. **A cosmetic line.** A path of two points with no visible stroke is drawn one device pixel wide,
   butt capped, in the fill colour, whatever it was filled with.
2. **A rectangle on the device's axes** with no visible stroke fills whole pixels. This is ADR
   0026's rule, now asked of the recording.
3. **Zero-area hairlines.** A fill with no stroke at all first takes, from each subpath, the parts
   `GetZeroAreaPath` says have no area, and strokes them a pixel wide in the fill colour. Then it
   fills. `GetZeroAreaPath` asks three questions, each in user space:
   - **A line, or a line there and back.** Its two ends are snapped to the centres of the device
     pixels they truncate to, and it is thin.
   - **A palindrome, `A B C B A`.** Each step from the middle outward is a segment, and it is
     thin.
   - **Anything else.** For each line that folds back along itself, down, across or aslant, the
     shorter of its two legs is a segment. It is thin only in a subpath of more than three points.
     Curves are skipped.

   A thin segment is drawn at a quarter of the fill's alpha byte, `(fa>>2)/255`.
4. **The knockout.** A fill and a stroke whose alpha byte is below 255 are drawn by
   `DrawFillStrokePath` (`canvas.fillStroke`):
   - Both go into a transparent layer of bytes. The layer is transparent because go-pdfium's
     bitmap has an alpha channel, and `DrawFillStrokePath` copies the device in only when it has
     none.
   - The fill is composited as `CompositeSpanARGB` does.
   - The stroke is composited as `CompositeSpan`'s BGRA alpha merge does. It replaces the fill
     where the layer's alpha is 0 or the stroke covers the pixel wholly, and mixes with it by
     coverage elsewhere.
   - The layer then goes onto the page through the clip.

   This is §11.7.4.4's knockout group for `B`, `B*`, `b` and `b*`. pdfium does not take
   §11.7.4.4's exception for overprint, and neither does this backend. The survey still refuses a
   fill and stroke under a soft mask.
5. **Otherwise** the fill, then the stroke.

**Alpha bytes.** A fill's and a stroke's alpha are the bytes `GetFillArgb` and `GetStrokeArgb`
make: `int(float32(a)*255)`. Every rule above asks of the bytes. A stroke whose byte is 0 is not
stroked, and a fill whose byte is 0 is not filled. Before this, `ca 0.0039` filled at one level.

Each set of hairlines is stroked as one path and summed where its outlines overlap (`ruleSum`), as
AGG's rasterizer sums them. Two hairlines that cross are darker where they cross.

### A line that goes nowhere is a pixel long

`BuildAggPath` moves one point. It moves a line's end that equals the point before it, compared
before the matrix, when that point is an open move and the line is followed by the end or another
open move. The end moves a pixel (`traced`, `plusOne`). So pdfium strokes `x y m x y l S` as a
line a pixel long under every cap, where §8.5.3.2 paints a dot under round caps and nothing under
the others. This backend did the latter and now does the former.

The pixel belongs to the space the point is moved in:
- **A stroke.** `CFX_AggDeviceDriver::DrawPath` splits the CTM. The path is built under its uniform
  scale by max(|a|, |b|), and the stroker applies the rest. In device space the pixel is a step of
  (a, b) / max(|a|, |b|): right on an upright page, left on a mirrored one, down or up on a
  quarter-turned one, and √2 long at an eighth of a turn.
- **A cosmetic line and a snapped zero-area line.** Their points are already the device's, so the
  pixel is one to the right.

Two cases nearby draw nothing in AGG, and nothing here:
- a subpath of two lines that go nowhere;
- a curve that goes nowhere.

The curve used to draw a dot. Flattening it by Bernstein evaluation drifted from the start point.
A curve whose control polygon has no length is now its line (`curveTo`).

**A curve with no current point adds nothing**, as a line with none already did. The recording
dropped such a curve, but the edges drew it from (0, 0). **A clip pdfium recorded no point of is no
clip**, because `AddPathObject` returns before it clips. So `W n` and `W b*` with no path, and a
`c` or `l` with no current point under `W n`, leave the clip as it was, where they used to clip
everything away. `b*`'s close adds nothing to an empty recording, as `AddPathPointAndClose` adds
nothing to an empty path. A lone `m` still clips everything away, in pdfium and here.

AGG's `vcgen_stroke` strokes a closed subpath of fewer than three vertices open, with caps. The
`m l h` of a line that goes nowhere is one of these, and this backend now strokes it the same way.

**Dashed**, a line that goes nowhere is dashed as any other. A *closed* one is refused by reason:
"a closed dashed subpath of zero length". AGG's `vcgen_dash` walks such a subpath there and back
as one open dash, with two caps at its start. This dasher joins them there, as §8.4.3.3 asks and
ADR 0024 decided.

## Acceptance

Three new tests run each case at 72, 144 and 200 dpi against pdfium, per channel, on a 200×100
page (`againstPdfium`):

- **`TestAFillWithNoAreaIsAHairline`**, 23 cases. It covers each of `GetZeroAreaPath`'s shapes
  across, down and aslant. It also covers a fold in a longer figure, a fold at a curve's end and a
  palindrome with a curve in it. And it covers a line going nowhere, alone and beside a triangle,
  lines that cross, `l l` with no `m`, `m m l`, `m l m`, a `cm`, an alpha and a path off the page.
  Each agrees within a mean of 0.071 and a worst of 7, except the palindrome with a curve, whose
  curved edge differs by up to 34.
- **`TestFillAndStrokeKnockOut`**, 21 cases:
  - `B`, `B*`, `b` and `b*`, and `B` and `B*` on a star that winds twice;
  - `S`, which never takes the layer;
  - opaque and translucent strokes and fills, and alpha bytes of 0 on each;
  - a rectangle, two clips, and a fold and an `m b*` under a stroke with an alpha byte of 0;
  - two knockouts on one page, the smaller first and then the larger, so the layer is kept and
    grown.

  The worst differences are at the corners, where this backend's joins differ from AGG's: 41 on
  the triangle under an opaque stroke, 28 under one at half alpha, and 45 at the star's points.
- **`TestAStrokeThatGoesNowhereIsAPixelLong`**, 25 cases:
  - each cap;
  - `m h`, `m s`, `m b*` and `m l h`;
  - two `l`, a curve and a lone `m`;
  - a line going nowhere before, after and between others;
  - a scaling `cm`, and one mirrored, one quarter-turned and one turned an eighth;
  - dashes, including one that starts in a gap, and one mirrored.

  Round caps differ by 25, as on any line.

Older tests changed where pdfium draws otherwise than §8.5.3.2:
- **`TestStrokeGeometryIsExact`**: its zero-length cases are now a pixel long.
- **`TestStrokeAlphaIsCAAndFillAlphaIsCa`**: the stroke now knocks out the fill under it.
- **`TestUndrawableDashesAreRefusedByReason`**: it draws open zero-length dashes and refuses closed
  ones.
- **`TestFillsAgreeWithPdfium`** has seven new cases, each exact:
  - a `c` with no current point, under a fill and under a clip;
  - an `l` with none, under a clip;
  - `W n`, `W b*` and `W h n` with no path;
  - a lone `m` as a clip.

**A review** by a fresh reader of the diff found three defects in a first round and one in a
second, each now fixed and pinned:
- **The pixel's direction.** It always pointed right, and so missed pdfium by up to 228 levels
  under a mirrored or turned CTM, or a `/Rotate`. It is now 7 at worst, apart from round caps.
- **A curve with no current point.** It filled and clipped from (0, 0): a mean of 13 on a fill, and
  all of a clipped page.
- **`W b*` with no path**, found in the second round. `b*`'s close left a lone line in an empty
  recording, so the clip's test for an empty recording passed it, and it clipped all of the page.
- **The knockout layer's size.** It was 16 bytes a pixel on amd64, allocated afresh for each path.
  Ten `B` at 600 dpi allocated 5 GB. It is now 4 bytes a pixel and reused, and the same page
  allocates 286 MB.

**`TestManySegmentsIsNotQuadratic`** now takes 15 to 20 ms. Its comment gave 5.5 ms, and the fill
alone is still 8 ms on the machine that measured both. The rest is the hairlines pdfium strokes
where the zig-zag folds back along itself, on rows where its integer y repeats. Its bound still sits between that and the 154 ms of a fill that rescans its
edges.

**The corpus**, at 72 dpi, with each rule counted in a scratch copy and measured against HEAD. The
mean went from 3.98128 to 3.98125. 6 pages moved closer, none farther, and 1,245 did not move. No
page moved without a counter firing on it.

| rule | pages | moved |
|---|---|---|
| a line that goes nowhere, +1 | 6 | 6 closer, −0.04587 in all |
| zero-area hairlines | 6 | 0 |
| a curve that goes nowhere | 2 | 0 |
| cosmetic line, a stroke or fill with an alpha byte of 0, knockout, closed subpath of two points, a curve or clip with no current point | 0 | — |

- **The +1 accounts for every move.** The largest is Well-Tagged PDF page 3, from 2.2984 to
  2.2551, where it fires 90 times. The other five are EC3 pages 927, 931, 932, 1004 and 1020, each
  about 0.0005 closer.
- **The zero-area pages** are six PDF Association covers, page 1 of ISO TS 32005, PDF
  Declarations, the three PDF 2.0 application notes and Well-Tagged PDF. Each logo has two fold
  hairlines 0.002 pixels long, which are thin and so drawn at 63/255. Each lies inside a dark
  outline, and none changes a byte.
- **The curves that go nowhere** are on EC3 pages 178, 70 times, and 820, 3 times. They changed
  no byte.
- **No page has a line that goes nowhere under a mirrored or turned CTM.** Measured again after
  the review's fixes, every page's mean is as it was. The step leaves (1, 0) on EC3 pages 931, 932,
  1004 and 1020 only by under 3 × 10⁻⁷ in y, from a `b` that float32 does not make 0.

## Findings, documented and not followed

- **Coverage summed across a path's outlines.** AGG sums the coverage of every outline in a path
  before it clamps. So two subpaths, or two dashes, that overlap are darker where they meet. This
  backend sums only within one set of hairlines. A zero-length line beside another in one dashed
  path is 74 levels from pdfium, and the two caps of a dashed `m h` are 119.
  It is planned as ADR 0028.
- **Joins.** This backend's joins differ from AGG's by up to 45 levels at a corner. That is the
  residual in every case above with a corner.
- **A closed dashed figure** still joins at its start where AGG's dasher caps it (ADR 0024). A
  round-capped closed dashed figure differs by 229 at that point.
- **Float compositing.** An unmasked fill or stroke is composited in float, at `v × alpha`, where
  pdfium composites in bytes. A translucent fill differs by a level on a channel across its whole
  interior: a mean of 0.14 to 0.16 on a quadrilateral at `ca 0.5`, against a worst of 4. The
  knockout layer and a masked fill already composite in bytes.
- **The overprint exception** to §11.7.4.4's knockout is not taken, as pdfium does not take it.
- **A `cm` inside a path object.** §8.5.1 allows none there. When one is found, routing asks the
  recorded points under the CTM at the painting operator, while the edges were placed under the
  CTM of each operator that added them. The two can disagree on which rule a path takes. This
  predates this ADR, and only malformed content reaches it.
- **The survey charges every stroke's dashes.** It refuses a dash it cannot draw even on a stroke
  paint then skips: one with an alpha byte of 0, a cosmetic line, or a rectangle. So
  `/GS gs [3 3] 0 d 1 J 100 100 m h S` is refused, although nothing would be dashed. This costs
  coverage, not correctness.

## Mutation

97 mutants cover:
- the recording and `closeFor`;
- `traced` and its +1;
- `rectOf`;
- `zeroArea` and its three questions;
- `curveTo`'s line;
- `drawPath`'s routing and its alpha bytes;
- `canvas.fillStroke` and the layer it keeps;
- the review's fixes: the step's direction, a curve with no current point, and a clip with no point.

Each ran in a scratch copy under a 4 GB commit watchdog, and none was killed by memory: the peak
is 0.51 GB. 89 are killed, each by a named test. The three tests this ADR adds kill 62, and older
tests kill 27:

| test | kills |
|---|---|
| `TestUndrawableDashesAreRefusedByReason` | 10 |
| `TestDashesAgreeWithPdfium` | 3 |
| `TestFillsAgreeWithPdfium` | 6 |
| `TestARectangleClipAdmitsWholePixels` | 3 |
| `TestARectangleFillIsWholePixels` | 2 |
| `TestADashThatWrapsIsJoined` | 1 |
| `TestCFFAgreesWithPdfium` | 1 |
| `TestICCColourAgreesWithPdfium` | 1 |

A first pass of 81 had 20 survivors and 4 mutants that did not compile. 12 survivors became cases:
- in `TestAFillWithNoAreaIsAHairline`: `l l` with no `m`, a line going nowhere beside a triangle, a
  fold back to the middle, a fold at a curve's end, a palindrome with a curve, and `m l h` under an
  alpha;
- in `TestFillAndStrokeKnockOut`: `m b*` and a fold under a stroke with an alpha byte of 0, `S`,
  `B` and `B*` on a star, and three rectangles;
- in `TestAStrokeThatGoesNowhereIsAPixelLong`: `m b*` under butt caps.

A second pass of 19 re-ran those and the 4. It also ran `rectOf`'s test of the point type and the
fill now skipped at an alpha byte of 0. 17 were killed. The knockout stroke's rule lived again, as
below, and so did the type test. "A curve around a box" in `TestARectangleFillIsWholePixels` now
kills it at a mean of 13. A third pass of 2 ran on `rectOf`'s test of each side, rewritten so
gosec can see its bound, and killed both.

A fourth pass of 11 ran on the first round's fixes. 7 were killed, 1 did not compile, and 3 lived. By
the end of the passes that followed, 10 were killed:
- `TestAStrokeThatGoesNowhereIsAPixelLong` kills each of five changes to the step;
- `TestFillsAgreeWithPdfium` kills the curve's guard and the clip's;
- `TestFillAndStrokeKnockOut` kills a layer never allocated, never grown, and never cleared.

The layer's `clear` and its growth lived at first, as no fixture painted two knockouts on a page.
A smaller path then a larger one killed the growth and not the `clear`: the larger is given a
fresh layer, so no stale byte reaches it. The larger then the smaller killed the `clear`, at a
worst of 115 against 28. The curve's `p.cur = to` lived, and is gone. A last pass of 2 ran on
`addClose`'s test for an empty recording, and `TestFillsAgreeWithPdfium` killed both. The first
pass had found that test dead and deleted it, because no rule read an empty recording's lone line.
The empty clip's test reads it, and the second round restored it.

Five survivors were code that could not matter, and it is gone rather than tested:
- the edge close of a painting operator, since `scan` fills every subpath closed;
- `add`'s early return for a move to the pen's point, which the replacing move covers;
- `zeroArea`'s test for a subpath under two points, of which its loop already makes nothing;
- `fillStroke`'s test for a stroke that covers a pixel wholly, which the mix already gives;
- `curveTo`'s current point for a curve with none, which no operator reads before a move sets it.

Three live:
- **`fillStroke`'s `x1++`** is equivalent on every fixture. It guards against an edge whose
  interpolated x passes its end by an ulp, where `ex0 + t(ex1 − ex0)` exceeds `ex1`.
- **`fillStroke`'s test for a transparent layer pixel** only skips work. `merge` at alpha 0 writes
  the pixel back unchanged, as in ADR 0026.
- **The knockout stroke's rule** is nonzero, as the direct stroke's is. Summing it would be
  ADR 0028's change, made for both at once.

## Consequences

- **Every path keeps pdfium's points beside its edges.** Each operator records its points in
  float32 next to the device-space edges it adds. Every routing question is asked of the recorded
  points, and the edges are only filled.
- **`B`, `B*`, `b` and `b*` with a translucent stroke use a layer** the size of the path's bounds,
  at 4 bytes a pixel. The canvas keeps it, so a page holds one at a time: ten page-sized `B` at
  600 dpi allocate 286 MB in all, as one does.
- **A stroke departs from §8.5.3.2 where pdfium does.** A subpath that goes nowhere draws a line a
  pixel long under every cap, and a filled path with no area draws a hairline where the
  specification paints nothing.

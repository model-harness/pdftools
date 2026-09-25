# 20. Stroke in pen space, and refuse the dash

Date: 2026-09-25

## Status

Accepted

## Context

ADR 0019 left stroking as the largest blocker: `S` on 180 pages, `B` on 3 and `B*` on 2. The greedy
re-measurement projected that answering it would take `render/native` from 1,013 pages to 1,113.

A census of the strokes on those pages set the scope:

| what | corpus count |
|---|---|
| paint operators | `S` 1,500, `B*` 15, `B` 11 |
| line widths | 0.25pt to 2pt; 612 strokes at 0.5pt or less |
| caps and joins set | `J` 0 and 1, `j` 0 and 1, `M` 4 and 10 |
| stroke colour | `G` 116, `RG` 73, `K` 72, and named spaces |
| a CTM that scales x and y differently, in force at a stroke | 289 |
| `/CA` in an ExtGState | 1 on all 1,867 |
| a dash pattern | only on pages with other blockers |

Two numbers in that table decide most of the design. The 289 strokes under a non-uniform CTM mean the
pen can't be a circle on the device. The 612 thin strokes mean the sub-pixel case is common, not an
edge case.

## Decision

### The stroke is built in pen space, from convex pieces

§8.4.3.2 measures the line width in user space. Under a CTM that scales x and y differently, the pen
is an ellipse on the page, and its axes need not be the page's.

The path is kept in device space, so the stroker works like this:

1. It takes each point back through the inverse of the CTM's linear part.
2. It strokes the path there with a round pen.
3. It maps every piece forward again.

A linear map takes the circle to the ellipse and every offset with it, so this is exact. Offsetting
in device space by a transformed width is exact only along the ellipse's axes.

Every segment, join and cap is a convex polygon, wound positively on the device. The stroke is their
union, which the existing nonzero fill computes: the winding number is at least one inside any piece
and zero outside all of them.

The usual alternative is one outline stitched around the whole stroke. It saves edges, but at a sharp
join its inner side folds back over itself and has to be repaired. Pieces have no inner side, so
there is nothing to repair. A zig-zag of 10,000 segments with round joins and caps strokes in 0.16 s.

- **Joins:**
  - **Miter:** the wedge on the outside of the turn is closed at the point where the two offset
    edges meet. §8.4.3.5's limit is compared squared, as `2/(1+cos θ) ≤ ML²`, so a right angle's √2
    is never rounded away from its own limit. A ratio exactly at the limit is kept, because
    "exceeds" is the spec's word.
  - **Round:** an arc.
  - **Bevel:** a triangle.
  - A reversal is joined like any other turn. Its round join is the half disc beyond the vertex.
- **Caps:** butt, a half-disc arc, or a rectangle extended by half the width. A subpath that goes
  nowhere is a dot under round caps only, and a lone `m` is nothing (§8.5.3.2).
- **Arcs** are flattened to within a tenth of a device pixel. The step count is measured against the
  pen's *largest* device radius, so under a CTM stretched a hundredfold a cap is not three chords.
- **No line is thinner than one device pixel.** §8.4.3.2 says a width of 0 draws the thinnest
  visible line, and pdfium applies the same floor to every width below a pixel. It measures the
  pixel by the mean of the CTM's two axis scales, and so does this backend. Before the floor, a
  0.25pt line at 72 dpi covered a quarter of its row here and all of one row in pdfium: 640 pixels
  disagreed by more than 32.
- **A CTM that collapses the plane** (determinant 0, NaN or infinite) strokes nothing.

### The pen is the walker's; the width is the machine's

`content.Machine` already stacks the line width with q/Q, so `w` and an ExtGState's `/LW` both write
it, and there is one width, not two. Everything else is the walker's `pen`:

- cap, join and miter limit
- whether a dash is set
- the stroke colour space

It is saved and restored with the stroke colour and `/CA`. The paint pass starts from the initial
pen, not from wherever the survey left it. A stream that sets `1 0 0 RG` after its last stroke would
otherwise paint every stroke red.

### Refused, by reason, at the stroke

A refusal is checked at the painting operator, not where its state was set. Setting a dash or a
colour space that no stroke uses changes no pixel, and refusing it there would give up a page for
nothing. Each of the six stroking operators is checked.

| reason | why |
|---|---|
| `stroke: the colour space is /…` | only the device spaces are implemented, as for fills |
| `stroke: a dash pattern` | set by `d` or by `/D`; no page that would draw sets one, so a dasher would have nothing to measure it against |
| `stroke: line cap n` / `line join n` | outside 0..2 |
| `stroke: line width w` | negative or NaN |
| `gs: /CA is …` | validated as `/ca` is |

### Two defects stroking made visible

- **Text render modes.** Every mode but 3 was drawn as a fill:
  - Outline text (modes 1 and 2) came out solid.
  - Clipping text (modes 4 to 7) drew where it should have clipped.

  Every mode other than 0 and 3 is now refused as `text: render mode n strokes or clips with the
  glyphs`. The check runs at every show operator, visible or not, because mode 7 draws nothing and
  still clips.
- **The current point after `h`.** §8.5.2.1 makes the closed subpath's start the current point.
  `close()` left it at the last point, and `lineTo` with no open subpath dropped the segment.

  pdfium draws `… h 160 160 l S` from the start. This backend drew nothing: 791 pixels disagreed by
  more than 32, then 3. A segment with no current point at all is still ignored. That happens after
  a paint operator, and pdfium agrees.

## Acceptance

Against pdfium, at 72 and 144 dpi, on 19 shapes. They cover:

- each cap and each join, and a miter over its limit
- closing by `h` and by `s`
- a segment and a curve after `h`
- a segment with no current point
- a curve
- a non-uniform CTM, and a rotated one
- `B`, `b*` and a CMYK stroke
- lines of 0, 0.25, 0.5 and 1 point

Results:

- **Most shapes:** the mean ink difference is at most 0.024 of 255, and no pixel differs by more
  than 32.
- **Curves:** flattening puts the mean near 0.1. At 144 dpi, a few edge pixels are just past 32,
  worst 42, as for filled curves.
- **CMYK:** the stroke's mean is 0.44, because pdfium does not convert by §8.6.4.4's naive formula.

A wrong cap, join, width or pen shape moves the mean by whole units and puts hundreds of pixels over
32.

The comparison runs at 72 and 144 dpi because a 200pt page is a whole number of pixels there. At
200 dpi it is 555.56 pixels. pdfium maps the page onto 556, and every edge then disagrees by a
fraction of a pixel, a plain triangle fill included. That is a difference in page scale, not in
stroking, and it is fixed separately.

Exact-pixel fixtures pin what the comparison can only bound:

- the width
- each cap's extent
- the miter tip at half the width over sin 30° on a 60° apex
- the limit on either side of 2
- the elliptical pen's two widths
- the pixel floor
- the closing segment of each of the six operators
- which fill rule `B*` and `b*` use

On the corpus, the 100 pages stroking unlocked were each compared with pdfium at 72 dpi twice, with
their strokes painted and with them suppressed. **Painting them brought every one of the 100 closer
to pdfium**, and none further: the mean ink difference fell from 3.58 to 2.96 of 255, and by up to
2.5 on a page of figures. What remains is text. These pages draw with substituted faces, which is
also why the 1,013 pages drawn before stroking differ from pdfium by 5.62 on average.

Mutation testing: 69 mutants across `stroke.go`, `fill.go` and the walker. The first round killed
58, each by a named test. None of the other 11 was a defect in the code:

- Six were invalid: they left a variable unused, so they failed the build rather than a test. They
  were rewritten as `_ = v`, and died.
- One was equivalent. The end of a subpath kept the closing edge for a closed subpath, but the
  stroker draws that segment from its own points. The branch was removed, and a fixture now covers
  the case the dedupe still needs: a subpath that returns to its start before `h`.
- Four were fixtures that could not see their rule:
  - A reversal, which no shape had.
  - Arc flattening by the pen's largest radius, which only a stroke's total ink under a CTM
    stretched a hundredfold can see.
  - The first of two open subpaths.
  - A segment with no current point.

A second round of 13 retargeted the eleven and added two more. It killed all 13, each by a named
test.

## Consequences

- **`render/native` draws 1,113 of 1,251 corpus pages (89%)**, as projected, and stroking blocks
  none of them except by the reasons it refuses:
  - a named stroke colour space: 68 pages
  - a dash: 2 pages

  The corpus test's floor is 1,113. Without a face source the figure is still 0, with 1,140 pages
  refused for want of a face.
- **The remaining worklist:**
  - CFF: 104
  - `cs`/`scn`: 98
  - a named stroke colour space: 68, which is the same increment as `cs`/`scn`
  - Type 1: 17
  - a dash: 2
  - one soft-mask group
  - one shading
- **Not copied from pdfium:** it snaps an `re` rectangle's edges to the pixel grid, so
  `40.3 40.3 120 120 re` disagrees by up to 159 at 144 dpi. That is a rendering choice, not the
  spec's, and adopting it would move every rectangle this backend already draws.
- **`/SA` stays inert.** Stroke adjustment is a hint a device may decline, and this backend
  declines it.

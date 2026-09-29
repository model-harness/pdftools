# 24. Dash as pdfium does

Date: 2026-09-29

## Status

Accepted. Amends [ADR 0020](0020-stroke-in-pen-space-and-refuse-the-dash.md), which refused every
dash pattern because no page that would draw had one to measure against.

## Context

ADR 0023 left three pages. Two are refused for a dash and nothing else: WTPDF page 3, whose
strokes need at most 5,075 dashes, and LightOnOCR page 12, which needs at most 2,340. No
corpus page strokes a closed dashed subpath.

§8.4.3.6 defines the pattern as lengths along the path, alternately on and off, starting the
phase into it at each subpath. pdfium hands the pattern to AGG's `vcgen_dash`, after rules of its
own, and holds the array and the phase as `float`s (`CFX_GraphStateData`). Where the two differ,
the corpus cannot say which a reader expects. The dasher was therefore checked against pdfium on
fixtures built for each case.

## Decision

### The walk: §8.4.3.6, in pen space

The dasher walks each subpath in pen space, where ADR 0020's stroker already works. There a dash
has the length the file gives it and the stroker's pieces apply unchanged. Each dash is an open
polyline, capped at both ends and joined at the corners inside it. The pattern starts again at
every subpath.

- **The phase** is taken modulo the period, which is also §8.4.3.6's rule for a negative phase. A
  phase that ends an element exactly begins the next one, so a pattern and its rotation start
  alike: `[10 5 20 5] 15 d` is `[20 5 10 5] 0 d`. AGG's `calc_dash_start` stays in the element
  with nothing left. On an open subpath the two draw the same, since a dash of no length draws
  nothing and a gap of none separates nothing. At a closed subpath's start they differ, below.
- **A dash that ends exactly where a segment does** ends there and takes no join, which is
  `vcgen_dash`'s strict comparison. The points arrive in pen space through the inverse CTM, so an
  end the file puts exactly on a corner arrives a rounding either side of it. A remainder below
  1e-9 of the segment's length is taken as zero, the side that would otherwise carry the dash
  round the corner.
- **pdfium's rule for short elements:** any element, dash or gap, at most 0.000001 becomes 0.1.
  The go-pdfium build this repo measures against draws `[4 0]` and `[4 0.0000009]` as `[4 0.1]`,
  and `[4 0.0000011]` solid. A pattern of one element is repeated, as §8.4.3.6 says.

### A closed subpath: joined across its start

When a closed subpath's pattern is still in a dash as it comes back to its start, the last dash
is joined to the first, as §8.4.3.3 asks. The same holds when the whole subpath lies in one dash.
Here the dasher follows the specification and not pdfium. AGG ends one dash and begins the other,
both capped, so pdfium leaves the outer square of a mitred corner empty.
`TestADashThatWrapsIsJoined` pins both halves. A dash that ends exactly at the start is not in
it, and takes no join, as at a corner (`TestADashEndingOnItsStartTakesNoJoin`); pdfium caps it
too. A dash that begins exactly at the start is in it. Staying in the gap the phase ends, as AGG
does, would break the pattern there with a gap of nothing and cap both dashes, so `[10 5 20 5]
15 d` would leave the corner that `[20 5 10 5] 0 d` joins (`TestAPhaseOnAnElementsEndBeginsTheNext`).
Each element holds its start and not its end.

No corpus page reaches this, and the survey cannot see it coming without dashing the path.

### Refused by reason

| pattern or path | refusal |
|---|---|
| an odd array of 3 or more | `a dash array of odd length %d, which §8.4.3.6 repeats and pdfium pairs` |
| more than 32 elements | `a dash array of %d elements, past the 32 pdfium reads` |
| a negative element | `a negative dash element, %g` |
| every element zero | `a dash array of zeros` |
| an element that is not a number | `a dash element that is not a number` |
| a phase that is not a number, including `/D` with no phase | `a dash phase that is not a number` |
| an element past float32's range | `a dash element of %g, past the float range pdfium holds it in` |
| a phase past float32's range | `a dash phase of %g, past the float range pdfium holds it in` |
| a dashed subpath of zero length, other than a lone `m` | `a dashed subpath of zero length, which pdfium draws by rules of its own` |
| more than 2^20 dashes on the page | `the page's dashes run past 1048576` |

The zero-length subpath is refused because pdfium's answer depends on its neighbours. It draws a
path of nothing but `m` and `l` to one point as a line one unit long under every cap, and dashes
that line. The same subpath beside another it does not draw, and closed by `h` it draws part of
a dot. §8.5.3.2 paints it only under round caps. A lone `m` draws nothing in either, and is drawn.

pdfium ignores `d` with fewer than two operands or a first operand that is not an array, and
`/D` whose first element is not an array. So does this backend, and those strokes are drawn
solid.

A number too long for float64 cannot reach the dasher as infinity: the content lexer salvages it
as 0. The float32 refusals are what remain of "not finite".

### The bound: `maxDashes`, charged by the survey

A stroke's dash count is its length over the period, which nothing in the file bounds. One `l` a
million units long dashed `[0.1 0.1]` is five million dashes. The survey charges each dashed
stroke `dashBound`: for each subpath, half the element count times the sum of two and the length
over the period. The two cover the partial periods at either end. The charge is summed over the page, including every form each time it is drawn. It is
refused past 2^20, and the comparison is written `!(w.dashes <= maxDashes)` so that a NaN count, a
segment of infinite length in pen space, is refused too.

The charge is in user space, where a dash is measured. A page is refused or drawn at every
resolution alike.

To charge the path paint will stroke, the survey now builds it with the same `buildPath` paint
uses. It ends the path at each painting operator and at each form with a `/BBox`, and closes it
first at `s`, `b` and `b*`. `run` resets the path between the passes, so paint starts with none.
That reset is a defect the shared path made possible, and `TestPaintStartsWithNoPath` pins it.

## Acceptance

**Fixtures against pdfium**, at 72, 144 and 200 dpi, in `TestDashesAgreeWithPdfium`. The cases:
- each cap, and dots of zero length under each;
- a zero gap, and gaps either side of pdfium's threshold;
- a phase inside the pattern, negative, past the period, negative past it, and at the end of a
  dash;
- arrays of one and four elements, and the restart at each subpath;
- a corner inside a dash under two joins, a dash ending on a corner, one ending there only in pen
  space, and one crossing it;
- a curve, a non-uniform CTM, a rotated CTM, and a thin stroke under a uniform CTM;
- closed and `re` subpaths;
- the two `d` forms pdfium ignores.

Every mean is at most 0.03 of 255, and no pixel differs by more than 32. The exception is the
curve, whose flattening puts it at 0.071 and 4 pixels at 144 dpi, as for the solid curve.

**The corpus:** the two pages move closer to pdfium with their dashes drawn.

| page | dpi | mean, dashed | mean, drawn solid |
|---|---|---|---|
| WTPDF p3 | 72 | 2.298 | 8.255 |
| WTPDF p3 | 144 | 1.477 | 7.295 |
| WTPDF p3 | 200 | 1.147 | 6.899 |
| LightOnOCR p12 | 72 | 2.276 | 2.357 |
| LightOnOCR p12 | 144 | 1.614 | 1.667 |
| LightOnOCR p12 | 200 | 1.284 | 1.329 |

**Findings, documented and not followed:**
- **A short segment after a sharp corner.** At 6 w, 0.3 past the corner, pdfium leaves a hole
  that AGG's inner join makes. A solid path `1 150 m 38 150 l 37.905132 150.284605 l` reproduces
  it, so it is the stroker's difference, not the dasher's.
- **A zero-length dash under round caps.** pdfium stays in a dash that the phase ends, with
  nothing in it. It draws that as a dot at 96, 100, 120, 150, 200 and 300 dpi, and not at 72, 144
  or 250. The same holds for `[12 6] 12`, `[10 6] 10` and `[8 8] 8`. What is left of the dash is
  a rounding either side of zero in pdfium's device space, not a rule. This backend begins the gap
  and draws no dot.

**Mutation:** 71 mutants cover these areas:
- the pattern's reading and its refusals;
- the phase, the walk and the wrap;
- the bound and its charge;
- the survey's path;
- the survey's gate in `run`.

Each ran under a 4 GB commit watchdog. 70 are killed, each by a named test, and none by memory: the peak is 0.50 GB. The one that lives is equivalent:

- **`for l-t >= left` in the walk.** A dash or gap that ends exactly where a segment does is then
  ended in the loop at the segment's end. Without the mutant, it ends at the next segment's start,
  the same point, which `dash` drops as a repeat. `subpath` has already dropped repeated points,
  so no segment has length zero, and the `0/0` that `>=` would meet there cannot arise.

The batch changed the code twice:

- **The phase.** Mutating `for ph > d[i]` in `dashStart` to `>=` was first taken to be
  equivalent. On a closed subpath it is not: it moved a phase that ends a gap into the dash that
  follows, where the code had stayed in the gap and capped the start. The mutant was the
  consistent rule, and is now the code. `TestAPhaseOnAnElementsEndBeginsTheNext` kills the old
  one.
- **A dash ending on the start.** Tracing the walk's survivor found that a dash ending exactly on
  a closed subpath's start was joined there. The wrap now needs `left > 0`, and
  `TestADashEndingOnItsStartTakesNoJoin` pins it.

The guard in `dash` against a dash of one point lost its killer when the phase changed, since no
phase leaves a dash of nothing any more. A phase one ulp short of a dash's end makes one again.
`polyline` reads a second point, so without the guard that dash panics.

## Consequences

- **`render/native` draws 1,250 of 1,251 corpus pages.** The corpus test's floor is 1,250.
- **The remaining worklist** is ISO TS 32005 page 1, which needs a soft-mask group and a
  shading.
- **Refusal tests survey and do not paint.** Mutant k-03, which turns the NaN-safe comparison into
  `>`, let paint dash a segment of infinite length, and its test binary took the machine's commit
  limit twice. A test that renders a page it expects refused becomes, when the survey regresses, a
  test that paints what the refusal guards against: a million dashes, or a form that draws itself.
  `Page` is split so that `open` builds the walker and `surveyPage` runs the survey alone. The
  `refusal` helper every refusal test uses goes through them. `TestARefusedPageIsNotPainted` pins
  the contract it relies on: `Page` refuses what the survey refuses, and paints nothing. k-03 now
  fails in seconds at 0.39 GB. Mutation runs are still made under a 4 GB commit watchdog.

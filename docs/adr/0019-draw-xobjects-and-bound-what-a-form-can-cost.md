# 19. Draw image and form XObjects, and bound what a form can cost

Date: 2026-09-24

## Status

Accepted

## Context

ADR 0017 left `Do` as the largest blocker: 173 pages, and the greedy re-measurement in DESIGN §8
projected that answering it would take `render/native` from 903 pages to 1,029. `Do` draws what the
XObject it names draws, and that is one of two things: an image — samples mapped onto the unit
square — or a form, a content stream with its own matrix, clip and resources.

## Decision

### An image is decoded by `image`, and composited here

`image.Pixels` returns non-premultiplied colour with the soft mask as alpha. That puts the samples
and the `/Matte` inversion in the package that already had to get them right for extraction, where a
renderer would otherwise have written a second copy of both. Compositing stays in the renderer, so
ADR 0004's line still holds. The matte check is split into `invertible()`, which `Recoverable()`
and `Pixels` share, so the rule for which blend can be undone exists once.

`Pixels` is **stricter than `Encode`, on purpose**. `Encode` degrades: a user holding the file can
still look at it. A rasterizer that degrades draws a wrong picture with nothing to say so. So each
case `Encode` tolerates is an error here:

- a stencil mask, which has no colour until a fill colour is supplied;
- a premultiplied image whose blend cannot be inverted;
- a DCT image in CMYK, whose Adobe inversion convention the stdlib decoder does not follow;
- a DCT image with a non-identity `/Decode`, which the decoder would ignore;
- a JPEG header whose size disagrees with the dictionary. The one-image sample cap was checked
  against the dictionary's size, and the decoder allocates the header's, so a disagreement is both
  an allocation the cap never saw and a file with two answers to how big its image is.

The renderer inverts the CTM for each device pixel, samples bilinearly, and takes coverage from the
fraction of up to 4×4 subsamples that land inside the unit square. That fraction is what gives a
rotated image an anti-aliased edge. When an image is drawn smaller than its resolution, the
subsamples average it rather than alias it. Colour is weighted by alpha before it is interpolated,
so the colour of a transparent sample, which a file may leave as anything, does not bleed into its
neighbour. `/ca` and the clip apply as they do to a fill.

### A form is §8.10.1, and only two transparency groups are refused

A form runs bracketed by q/Q. Its `/Matrix` is applied before the CTM in force at the `Do`, it
inherits the text state, it uses its own `/Resources` if it has them and the invoking stream's if it
does not, and it is clipped to `/BBox` in form space. The font name cache is swapped along with the
resources, because a form's `/F1` is its own and need not be the page's.

A transparency group is drawn as though it were not one, and that is exact only under the normal
blend mode, which is the only mode `gs` admits. Under it an isolated group composites to the same
result as a non-isolated one, so `/I` changes nothing. Two cases do change the result, and both are
refused:

- a **knockout** group, where each element replaces the elements before it;
- a group drawn at **constant alpha below 1**, which §11.6.6 applies to the group's *result*. Two
  overlapping half-transparent squares inside such a group are one 50% shape; drawn one by one,
  their overlap is 75%.

Also refused, each by its own reason: an XObject that is not in the resources, one that is not a
stream, a PostScript XObject (§8.8.2 lets a reader ignore it, and ignoring one still leaves out
something the producer drew), and a form whose content does not decode.

### Every axis of a form's cost is bounded

Every axis gets its own bound, because depth alone does not bound the work: a form that draws
another form ten times, eight deep, is 10⁸ operators from a file of a few hundred bytes.

| bound | value | corpus maximum |
|---|---|---|
| form depth | 8, the bound `extract` and `image` already use | — |
| operators per page, forms included, counted each time drawn | 5,000,000 | 13,733 |
| decoded image pixels per page, soft masks included | 64M (256 MB as RGBA) | 24,740,410 |

The mask is charged because `Pixels` decodes it at its own size before scaling it to the base. A
1×1 image carrying a 16384×16384 mask is a gigabyte, not a pixel, and the first version charged it
as a pixel.

Decoded images, form content and parsed fonts are cached by indirect reference for the page. That is
what keeps an image drawn five times from being charged five times.

### The defect the fixtures found: every form painted nothing

`objects/pdfcpu` builds a new `*objects.Stream` on every `Resolve`. The survey decoded a form's
content onto its copy, and the paint pass read `Decoded` off its own copy, which was nil. So every
form painted as nothing, with no error. **The corpus count could not see it**: refusing a page is
the survey's job, and the survey had the bytes, so 1,013 pages "drew". The defect was found by a
fixture that asserts a pixel inside a form. Form content is now cached on the walker by reference,
and nothing is carried between the two passes on a resolved object.

The same work found that Q restored neither the fill colour nor the font. content.Machine restores
its own copy of the text state, and the walker kept the colour and the resolved font outside it.
Every test until now had set its colour after its last Q.

## Acceptance

Against pdfium, on a 100×100 checkerboard:

| case | mean ink difference (of 255) |
|---|---|
| drawn at its own resolution | 0.000 |
| drawn at a quarter of its resolution | 0.000 |
| rotated 30° | 6.7 |

The rotated figure has the same geometry in both. pdfium's edge filter is visibly softer than
bilinear, and even rounds the image's corners, and every edge of a checkerboard is an edge pixel. A
flip or a wrong rotation moves whole squares, which costs tens, so the test asserts 8 there and
0.05 axis-aligned.

On a real figure, the image XObject of Figure 12 on EC3 page 168, the mean RGB difference over the
figure's crop is **1.14 at 72 dpi and 1.82 at 200 dpi**.

Mutation testing: 32 mutants across `xobject.go`, the walker and `image/pixels.go`, each killed by a
named test. Five survived the first round, and none of them was a gap in the code:

- One was invalid: it left a variable unused, so it failed the build rather than a test.
- One was equivalent. The forms cache only saves cost, so removing it changes nothing. It was
  retargeted at the decode in the paint pass, and died.
- Three were fixtures that could not see their rule:
  - A checkerboard whose 10-sample squares make one bilinear sample equal the area average at a
    quarter scale.
  - A transparent sample whose colour the premultiplied decode had already turned black.
  - A bleed fixture whose transparent sample had no red, against a mutant that changed only red.

## Consequences

- **`render/native` draws 1,013 of 1,251 corpus pages (81%)**, and no page is blocked by `Do`. The
  projection said 1,029. The 16-page gap is pages whose forms hold other blockers that were
  invisible while the survey refused the form unopened. Walking into forms raised the stroking count from
  156 to 180 and cs/scn from 97 to 98. The corpus test's floor is 1,013. Without a face source the
  figure is still 0, and 1,140 pages are refused for want of a face (was 1,139; one more font is now
  reached inside a form).
- **The remaining worklist:** S 180, CFF 104, `cs`/`scn` 98, Type 1 17, B 3, B* 2, one soft-mask
  group, one shading. **Stroking is next.**
- A stencil mask image is refused. It needs the fill colour at the `Do`, which is a small change,
  but the corpus has none left to measure it against.
- The `counted` distinction in `imagePixels` (a direct image stream decoded again when painted, and
  not charged twice) is unreachable from a valid file, because a stream is always an indirect object.
  No test reaches it.

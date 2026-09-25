# 21. Convert ICC colour as pdfium does, and refuse the rest by name

Date: 2026-09-25

## Status

Accepted

## Context

ADR 0020 left non-device colour as the largest blocker after CFF: `cs`/`scn` fills on 98 pages,
and a named stroke colour space on 68. Both come from the same increment, since a stroke colour
space is selected the same way a fill's is.

A census of the colour spaces on those pages set the scope:

| what | pages |
|---|---|
| a fill in an ICCBased RGB space, profile "sRGB IEC61966-2.1" | 97 |
| a fill in an ICCBased gray space, profile "Gray Gamma 2.2" | 23 |
| a fill after `/DeviceRGB cs` | 1 |
| a stroke in the ICCBased sRGB space | 68 |
| an ICCBased image, sRGB | 2 |
| images in DeviceRGB, DeviceGray | 140, 10 |
| Separation, DeviceN, Indexed, Lab, CalGray, CalRGB, Pattern | 0 |
| a `/DefaultGray`, `/DefaultRGB` or `/DefaultCMYK` resource | 0 |

Every profile is a v2 matrix/TRC profile. sRGB has `rTRC` as a `curv`, the colorants and `wtpt`,
and gray has `kTRC` and `wtpt`. None has a LUT.

Colour alone blocks 16 of the 98 pages. On the other 82 it is joined by:

- CFF (80)
- a dash (2)
- a soft mask (1)
- a shading (1)
- Type 1 (1)

So this increment is worth 16 pages by itself, and it matters mostly for what it unblocks once CFF
is done.

## Decision

### An `icc` package for the profiles PDFs carry

`icc.Parse` reads a matrix/TRC profile, gray or RGB, with an XYZ connection space, and refuses
everything else by reason:

- **A LUT profile is refused, not approximated.** A CMM that finds an A2B tag uses it in preference
  to the matrix and curves, so drawing through the matrix would disagree with lcms, which is the CMM
  pdfium runs.
- **The header's size field is not trusted.** Every bound is checked against the bytes actually
  read. `FuzzParse` ran 31.4 million inputs without a crash.

The curves are read as ICC.1 defines them:

- `curv` with no entries is the identity, with one entry a gamma, and with more a sampled table,
  linearly interpolated.
- `para` types 0 to 4, with lcms's own guards (cmsgamma.c) where the specification leaves the
  result undefined. In types 1 and 2 a slope under 1e-4 gives 0, and in every type a power of a
  base that is not positive gives 0. A curve from a hostile profile therefore agrees with pdfium
  instead of producing a NaN.
- Every output is clipped to [0,1], as ICC.1 §10.18 says. lcms does not clip, which is one of the
  divergences below.

The conversion into sRGB is folded into one matrix, `inv(S)·K·M`, plus an offset, so one colour
costs one multiply and an add. `Image` samples each curve into an 8-bit table and shares one
65,536-entry table for sRGB's encoding.

### Black point compensation, always, because pdfium does it always

pdfium converts every ICC colour at the perceptual intent, hard-coded, into lcms's own v4 sRGB
profile. It ignores whatever the PDF asks for: `/Perceptual`, `/RelativeColorimetric` and
`/AbsoluteColorimetric` were measured drawing identically. lcms compensates the black point of
every such conversion whose two blacks differ, so `icc` compensates too.

The black is taken as lcms takes it (cmssamp.c, `BlackPointAsDarkerColorant`): the darkest colour
through CIELAB, with two rules applied to its lightness.

- Above L* 95 the lightness becomes 0, because lcms reads such a profile as a negative.
- Above L* 50 it is clipped to 50.

A profile whose curves start at 0 has a black of 0, and compensation leaves it alone.

`/RI` in an ExtGState and the `ri` operator are accepted and not honoured, for the same reason.

### sRGB is recognised by what it does, not by its bytes

pdfium draws the stock 3,144-byte "sRGB IEC61966-2.1" profile as DeviceRGB. It recognises that
profile by its size and description. Two spikes measured that:

- The same profile with its red colorant altered still drew as DeviceRGB.
- The unaltered profile with four bytes appended went through lcms instead, a level off on 333 of
  468 channels.

`isSRGB` decides on content instead. A profile counts as sRGB if converting through it moves no
colour by a whole 8-bit level, at every point of a grid 1/16 apart in each component. An sRGB
profile then returns its input unchanged. On that test:

- the stock profile is 0.51 levels from the identity (0.53 on a grid 1/255 apart)
- the nearest non-sRGB profile Windows ships, PAL/SECAM, is 48

So the rule lands where pdfium's does on the profile PDFs actually carry, and has a wide margin on
either side.

Before the rule, converting the stock profile exactly moved 944 samples by one level across the 16
pages colour unlocked, 382 closer to pdfium and 562 further. After it, none moved.

### Resolving a colour space

`cs` and `CS` resolve:

- a device name
- a named resource
- a family array, including a one-element `[/DeviceRGB]`, which pdfium was measured drawing as the
  device space

An `[/ICCBased stream]` space is resolved in this order:

1. its profile
2. failing that, its `/Alternate`
3. failing that, the device space for `/N`

pdfium was measured doing the last step: garbage profile bytes with `/N 1` draw as DeviceGray and
with `/N 3` as DeviceRGB. An `/Alternate` may itself be ICCBased, so the recursion is bounded at a
depth of 4. A space is cached by its profile stream's reference, because a page selects the same
space at every `cs` and a resolved stream is a copy that does not keep its decode.

The rest of the state:

- **The initial colour.** `cs` sets §8.6.5.5's initial colour: all zeros, taken through the space.
  Through the CMYK fallback that is white, not black.
- **Components are clamped** to 0..1 before conversion. This unifies `k`, which used to read its
  operands raw, with `K`, which already clamped.
- **Too few components.** An `sc` or `scn` with fewer components than the space has changes
  nothing. pdfium was measured doing the same: `/DeviceRGB cs 1 0 0 sc 0.5 sc` stays red.

### Images

An image's colour space is resolved before the image cache is consulted, so a refusal answers the
same way on the first call and every later one. A decoded image in an ICCBased space is converted
in place, through the same profile as a fill.

An `/Indexed` image converts through its base space after the palette lookup. Stacking an Indexed
space on an ICCBased base exposed a defect in `image`: the palette's stride was guessed from the
base's family name. `ICCBased` names no component count, so the guess fell back to 3. The stride is
now `BaseComponents`, counted from the base's `/N`. No corpus image is Indexed, so this fix changes
no corpus page, and `image.Encode` shares it.

### Refused, by reason

A fill or stroke is checked where it paints, as ADR 0020 checks a stroke. An image is checked in
`Do`.

| reason | why |
|---|---|
| `the colour space is /…` | Separation, DeviceN, Indexed, Lab, CalGray and CalRGB need colour-management or palette decisions this backend does not make, and none occurs in the corpus |
| `a Pattern colour` | patterns and shadings are not implemented |
| `a colour space not in the resources, /…` | nothing to resolve |
| `an ICCBased space with /N n` | /N outside 1, 3, 4 |
| `an ICC profile this backend does not read, and its /Alternate: …` | the fallback was tried and refused |
| `an ICCBased /Alternate of n components for /N m` | the alternate disagrees with the space |
| `a colour space nested more than 4 deep` | bounds the /Alternate recursion |
| `the resources define /Default…, which is not implemented` | see below |

**Default\* is refused, not resolved.** §8.6.5.6 has `/DefaultGray` replace DeviceGray "when a
device colour space is selected", which by its own reading includes `g`. pdfium honours it for
`/DeviceGray cs` fills and for a DeviceGray image, but not for `g` or `G`. No corpus page defines
one, so nothing could settle which reading to follow. A Default\* resource therefore refuses every
colour a device formula would convert, however it was reached. One rule for every route also keeps
the answer out of the space cache.

### Where this backend and pdfium differ, on purpose

- **`scn` precision.** pdfium truncates every component to a byte before its CMM sees it, and this
  backend does not. The two agree to a level for a mid-gamma profile, and to six levels near black
  for a linear one.
- **An ICCBased space's initial colour.** pdfium's is black and never converted. §8.6.5.5's is all
  zeros through the space, which is what this backend uses. Through a profile whose black is lifted
  it differs from pdfium's: 167 against 0.
- **A garbage `/N 4` profile** is white by the CMYK fallback. pdfium draws black, probably because
  it failed to load the space at all.
- **A curve that ends above 1** is clipped, as ICC.1 §10.18 says. lcms carries 1.05 into the
  matrix, where it pulls the other two channels, so an RGB profile disagrees by 8 levels and by 3 at
  1.02. Gray, with no matrix, agrees.
- **DeviceCMYK** still converts by §8.6.4.4's formula. pdfium converts it through a CMYK-to-sRGB
  table of its own. This predates ICC, but the `/N 4` fallback now reaches it as well.
- **An image `/ColorSpace` that names a resource** is refused, because §8.9.7 grants that lookup
  to inline images alone. pdfium resolves the name. No corpus image does it.
- **An ICC `/Alternate` that names a resource** is refused for the same reason: §8.6.3 makes a
  colour space an array or a family name, and only `cs` and `CS` look a name up. pdfium resolves
  it, and draws a garbage `/N 3` profile with `/Alternate /CS1` in `/CS1`'s colour. A chain of
  resource names reached through `cs` (`/CS0` → `/CS1` → `/DeviceRGB`) is refused here, and pdfium
  paints nothing for it (white), so neither resolves the second name.
- **`/IM` on an image XObject** is read as `/ImageMask`, as `image.Read` reads it, although §8.9.7
  says the inline-image abbreviations "shall not be used in image XObjects". pdfium does not treat
  it as a stencil. This backend refuses a stencil mask either way, so the divergence costs a
  refusal, not a wrong pixel.

## Acceptance

Against pdfium, each bound the worst measured.

**Fills** (`TestICCColourAgreesWithPdfium`) cover every gray level and the 6×6×6 RGB grid, through
six profiles:

| profile | bound, in levels |
|---|---|
| a 2.2 gray | 1 |
| a linear gray | 6, near black |
| sRGB's colorants with a 1.8 gamma | 0 |
| an Adobe RGB | 1 |
| a gray and an RGB whose black is lifted | 1, 0 |

pdfium's pixel equals this package's profile fed pdfium's own byte truncation exactly, on every
patch but one. The exception is a rounding tie at Adobe RGB (1, 0.6, 0.2), a level off in blue. So
truncation is the only difference between them.

**Images** (`TestICCImagesAgreeWithPdfium`) are 256×1, one sample per device pixel. They agree
exactly in gray, and Adobe RGB is a level off on 16 of 768 channels. Skipping the profile would put
samples 73 levels off.

**Curves** (`TestICCCurvesAgreeWithPdfium`) draw 26 curves through a 256×1 image, each as a gray
profile's one curve and as all three of an Adobe RGB profile's:

- `curv` with 0 to 4,096 entries
- `para` types 0 to 4
- a black lifted, a black past L* 50, a black past L* 95
- the hostile forms: a zero slope and negative bases
- one of each form in a single RGB profile

Gray agrees to within 1. RGB agrees to lcms's own 1, except the two curves that end above 1, where
it is 8 and 3 off. Without black point compensation, the three lifted rows are 63, 25 and 60 levels
off. Without the L* 50 clip, the row past L* 50 is 105 off, and without the L* 95 rule the row past
L* 95 is 3 off.

**The stock sRGB profile**, read from the copy Windows ships, agrees with pdfium exactly, fills and
image alike. The test skips where there is no such copy, because the profile is not this
repository's to redistribute. Every other profile is built in Go by `internal/iccbuild`.

**The fallbacks** are checked against pdfium: a garbage profile with `/N 1` and with `/N 3`, a gray
profile declared `/N 3`, and a garbage profile with an `/Alternate`.

**Corpus:** 1,113 pages drew before, and 1,129 draw now. None was lost. Every page that drew before
renders byte-identically: none of them selects an ICCBased space or draws an ICCBased image. The 16
pages gained were each rendered with the ICC conversion on and off, and the two renders are
identical. The one profile on those pages, sRGB IEC61966-2.1, passes `isSRGB`, so it converts as the
identity.

**180 mutants across `icc`, `colour.go`, the walker, `stroke.go`, `xobject.go` and `image`: 177
are killed, each by a named test, and the other three are stated.**

- One is equivalent. Reading a type 1 curve as flat at `|a| <= 1e-4` rather than `< 1e-4` differs
  only where `|a|` is exactly 1e-4, and no s15Fixed16 value is: all 2³² were checked.
- Two mutated guards that did nothing, and were deleted rather than tested. One checked that a
  `/ColorSpace` resource dictionary exists before a name is looked up in it, but a missing
  dictionary reads as a nil map, where the name is just as missing. The other kept a refused
  space's initial colour black, but a refused space has no components, so its colour is black
  anyway.

Eight `icc` mutants die only in `render/native`'s comparisons with pdfium: both black point clips,
the `isSRGB` grid, the gray clamp, three in the image path's tables, and the linear segment of the
sRGB encoding, which fills and images share. They hold in any clone, because pdfium is a module
dependency and a failure to open it is fatal, not a skip.

The first round killed 134 of the 180. Of the rest, none was a gap in the code:

- Eight were invalid. Each left a variable unused, so it failed the build rather than a test.
  Rewritten to keep the variable, all eight die.
- One survived because `image.Read` counted a named colour space in two places, so deleting either
  copy changed nothing. The second copy is gone.
- The two guards above, and the one equivalent.
- The other 34 were fixtures that could not see their rule. Among them:
  - The selection-time Default* check needed a colour chosen under a page's `/DefaultRGB` and
    painted inside a form that defines none.
  - The `/Alternate` depth limit needed chains of exactly 4 and 5.
  - The black point's lightness clips needed blacks at L* −5, 45, 55 and 93, each between a
    boundary and its mutant.
  - The parametric breaks needed curves evaluated at exactly `d`, and a negative slope whose break
    rounds below `-b/a`.
  - The image abbreviations `/IM`, `/CS` and `/I` needed an image XObject that spells them.

## Consequences

- **`render/native` draws 1,129 of 1,251 corpus pages (90%).** The corpus test's floor is 1,129.
- **The remaining worklist:**
  - CFF: 104 pages, 80 of which also select an ICC colour space that now draws
  - Type 1: 17
  - a dash: 2
  - one soft-mask group
  - one shading
- **Two accuracy items, not coverage items:**
  - pdfium's CMYK-to-sRGB table, which moves every CMYK colour this backend already draws
  - resolving an image's named colour space, which no corpus image uses
- **Not built:** Separation, DeviceN, Lab and the Cal spaces, a LUT profile, and Default\*. Each
  refuses by name. None has a corpus page to measure it against.

# 22. Draw CFF glyphs, and route a TrueType code as pdfium does

Date: 2026-09-26

## Status

Accepted. Amends [ADR 0016](0016-draw-truetype-glyphs-and-refuse-fonts-by-reason.md): a simple
TrueType font's codes now reach its glyphs by one of two routes chosen per font, and not by
trying every route in turn.

## Context

ADR 0021 left CFF as the largest blocker: 104 pages, 80 of which also select an ICC colour space
that now draws. A census of the corpus's FontFile3 programs set the scope:

| what | programs |
|---|---|
| `/Subtype /Type1C`, under a simple Type 1 font | 35 |
| `/Subtype /CIDFontType0C`, under a CIDFontType0 descendant | 25 |
| `/Subtype /OpenType` | 0 |
| a charset in format 0 | 35: the 25 CID-keyed, and 10 simple |
| a charset in format 1 | 25, all simple |
| a built-in encoding in format 1, format 0, or the predefined Standard | 21, 4, 10 |
| a FontMatrix other than `[0.001 0 0 0.001 0 0]` | 3, a 2,816-unit em |
| an FDSelect in format 0, format 3 | 24, 1 |
| local or global subroutines, flex, seac | 0 |

That is 1,378 glyphs. Every simple font is over WinAnsi, 4 of them with `/Differences`, and every
composite is `Identity-H` with no `/CIDToGIDMap`. Of the simple fonts, 3 are flagged symbolic and
32 nonsymbolic. Since every one names WinAnsi as its base, none reaches the program's built-in
encoding.

Drawing them turned up a defect in the TrueType path, not the CFF one. On a SourceSansPro subset,
`This` drew as `Thrs`. ADR 0016's routes were meant as alternatives, one per kind of font, but
they were tried in order for every font. A code went first to the (3,0) subtable, then to (1,0),
then through its decoded text to the Unicode subtable. This subset's (1,0) subtable is its
subsetter's, keyed by its own codes and not by WinAnsi's, so a nonsymbolic font drew some codes
as other glyphs, `i` as `r`. The same route sent a dash to an empty glyph.

## Decision

### `font.CFF`, for the programs FontFile3 carries

`font.ParseCFF` reads a bare CFF program by Technical Note #5176:

- the header and the first font of the Name INDEX
- its Top DICT
- the String and Global Subr INDEXes
- the charset, in all three formats, or the predefined ISOAdobe charset
- the built-in encoding, in both formats with its supplements, or the predefined Standard encoding
- the Private DICT's local subroutines
- for a CID-keyed font, the FDArray and FDSelect (formats 0 and 3) that give each glyph its own
  Private DICT and FontMatrix

Where the note leaves a malformed table's meaning open, the reading is FreeType's (`cffload.c`),
since that is what pdfium draws with:

- A charset range that would run past SID 65535 is trimmed.
- A code the encoding assigns twice keeps its last glyph.
- A name or CID that two glyphs claim resolves to the lower one.
- A CID-keyed font's FontMatrix is the Font DICT's composed with the Top DICT's.

**Refused at parse time, each by name:**

- an OpenType wrapper, and CFF2
- `SyntheticBase`, whose glyphs are in a font the file does not carry
- `CharstringType 1`
- the predefined Expert charset and encoding, which no corpus font uses
- a MultipleMaster font, which FreeType itself reads only in part
- a FontMatrix without six elements, or one FreeType would not draw as written
- the grammar errors FreeType repairs silently or reads past, none of which occurs in the corpus:
  - a header under 4 bytes, or an offSize over 4
  - a Name INDEX whose names overrun its bytes, or that names more fonts than there are Top DICTs
  - an INDEX whose first offset is not 1, whose offsets go backwards, or that runs past the
    program
  - a DICT that ends inside an operand or an escape, uses a reserved byte, or gives an operator
    too many operands or none
  - a FontBBox of fewer than four operands, an ROS of other than three, and a Private entry that
    is not one size and one offset, or is negative
  - a real-valued operand where FreeType reads an integer: an offset, a Private entry or an ROS
  - a glyph name that is empty or holds a NUL

### A FontMatrix is taken only where FreeType reads it exactly

FreeType does not keep a FontMatrix as written. It divides the matrix by yy, or by yx where yy is
zero, and keeps the quotient as a whole number of units per em (`cffobjs.c`). It truncates a
translation to whole units, and it falls back to the identity at one unit per em in two cases:
when the elements' decimal powers look implausible (`cffparse.c`), and when `FT_Matrix_Check`
calls the linear part degenerate (`ftcalc.c`). pdfium draws each of those unlike the matrix:

| FontMatrix | FreeType reads | pdfium draws |
|---|---|---|
| `[0.5 0 0 0.35 0 0]` | an em of 3, not 2.857 | 5% small |
| `[0.001 0 0 0.001 0.05 0.02]` | an offset of 49 and 19 units, not 50 and 20 | a unit off |
| `[0.001 0 0.0056 0.001 0 0]` | the identity | a thousand times larger |
| `[0.01 0 0 0.01 0 0]` under a Top DICT's `[0.02 0 0 0.02 0 0]` | an em of 4,999, not 5,000 | 0.02% small |

The first three are pdfium's glyph paths. The fourth is FreeType's arithmetic, since no raster can
see it. Each is refused, by rules that hold for every digit string a matrix can be written in:

- The largest linear element is at least 1e-5 and under 1, and no nonzero element is under 1e-5
  of it, so the parser's powers stay plausible.
- The determinant clears `FT_Matrix_Check`'s bound by a tenth. The bound is a thirty-second of the
  summed squares, and FreeType tests it on elements shifted down to as few as 15 bits, which is
  what a 32-bit long leaves, as in pdfium's WASM build.
- There is no translation.
- The em is whole to within 1e-4 of itself and at most 65,535. The divisor is also fine enough at
  the least factor FreeType can carry it at, since that divisor is a 16.16 number.

In a CID-keyed font the Top DICT's matrix and each composition with a Font DICT's must pass, but a
Font DICT's own em need not. A Top em of 4 and a Font DICT em of 714.25 compose to exactly 2,857.

The rules also refuse matrices FreeType reads exactly. None is in the corpus, whose only matrix
other than the default is three programs' em of 2,816.03, which FreeType reads as 2,816:

- a Top DICT of `[1 0 0 1 0 0]` over Font DICTs that carry the scale
- a shear within a tenth of FreeType's degeneracy bound
- a composition carried at a larger factor than the least the rule assumes, such as `[0.01 0 0
  0.01 0 0]` under a Top DICT's `[0.125 0 0 0.125 0 0]`

### A Type 2 charstring interpreter

`CFF.Outline` interprets Technical Note #5177 as FreeType's Adobe engine (`psaux/psintrp.c`)
does. It draws every path operator, including flex and its three shorthands, and follows local and
global subroutines through their bias and the seac form of `endchar`.

**Hints are counted but not applied.** pdfium loads CFF glyphs with `FT_LOAD_NO_HINTING`, so the
outline it fills is unhinted. Stems still have to be counted, because `hintmask` and `cntrmask` are
followed by one bit per stem.

**Refused, per glyph:**

- the arithmetic, storage and conditional operators, and `random`, which is not deterministic
- the grammar errors FreeType repairs silently:
  - an operand count that is not one the operator takes
  - a stem declared after a hint mask or the first moveto
  - more than 96 stems, once a mask or the first moveto counts them
  - a subroutine number, or a seac component, written as a 16.16 fixed-point operand
  - a path drawn before the first moveto
  - a charstring that runs off its end
- a glyph of more than 65,536 segments, the cap TrueType composites have

None occurs in the corpus. Matching FreeType's repairs one by one would be guessing about bytes no
producer should write.

**Work is bounded per page.** A charstring cannot loop, but subroutines nest ten deep, so the work
one glyph asks for is exponential in its size, and a TrueType composite fans out the same way. A
page has a budget of 4,194,304 operations, shared by both formats. Each distinct glyph is charged
once for its build, since outlines are cached per page. Each show is charged for the segments it
draws. The heaviest corpus page, ISO 32000-2 page 161, uses 133,489.

### Which CFF glyph a code selects

pdfium is followed where ISO 32000-2 is silent, and a font is refused where the two disagree:

- **A composite font** maps its CID through the charset. A CIDFontType0 program that is not
  CID-keyed takes the CID as the glyph index, as FreeType does.
- **A simple font with a stated glyph name** looks the name up in the charset. The name is the
  `/Differences` entry, else the base encoding's.
- **A symbolic simple font with no base encoding** uses the program's built-in encoding.
- **A nonsymbolic simple font with no base encoding** is where §9.6.5.1 and pdfium part: the
  specification gives it the built-in encoding, and pdfium looks up the StandardEncoding name. Its
  glyph is drawn only where both select the same one.

A code that reaches no glyph, or glyph 0, is refused, per glyph.

**Refused per font:**

- a base encoding other than WinAnsi, which pdfium reads through tables of its own
- a font named Symbol, SymbolMT or ZapfDingbats, which pdfium special-cases by name
- a `/CIDToGIDMap` stream on a CIDFontType0 font, which §9.7.4.2 gives only to CIDFontType2 and
  pdfium honours anyway
- a CID-keyed program under a simple font, a FontFile3 program under a font that is neither Type 1
  nor CIDFontType0, and a TrueType program in FontFile3

### Which TrueType glyph a code selects

§9.6.5.4 gives two routes, and pdfium chooses between them per font:

- **By name.** The code's glyph name, from `/Differences` or the base encoding, goes through the
  Adobe Glyph List to a character, and the (3,1) subtable gives that character's glyph. pdfium
  takes this route for a nonsymbolic font, and for any font over WinAnsi, MacRoman or MacExpert.
- **By code.** Otherwise the code itself goes to the (3,0) subtable at the four ranges §9.6.5.4
  names — 0x0000, 0xF000, 0xF100, 0xF200 — else to (1,0), else, in a subset with no cmap at all,
  it is taken as the glyph index.

**The decoded text no longer selects a glyph.** A `/ToUnicode` map says what a code means to a
reader, and a subsetter's (1,0) subtable may say something else again, but neither is what
selects the glyph. A substituted face is the exception, and is still reached through the
character, because its glyph indices mean nothing to the document.

**Refused per font, where pdfium and this backend would choose apart:**

- a FontFile2 program under a Type 1 font, which pdfium reads by its Type 1 rules
- `/Differences`, because pdfium resolves glyph names by FreeType's rules and this backend by the
  glyph list
- a symbolic font over a named base encoding, which §9.6.5.4 ignores and pdfium reads names from
- a nonsymbolic font with no `/Encoding`, which §9.6.5.4 routes both ways
- a base encoding other than WinAnsi, and an `/Encoding` that names no base
- a font read by name with no (3,1) subtable, or by code with neither (3,0) nor (1,0)

**Refused per code:** a (3,0) subtable that maps a code to two glyphs at two ranges. §9.6.5.4
puts a font's codes in one range, so the file contradicts itself. pdfium draws the first.

Every embedded simple TrueType font in the corpus is nonsymbolic over WinAnsi, so every one goes
by name, and no corpus page is refused by any of these reasons.

### A TrueType program opens where FreeType opens it, and its glyphs land where FreeType puts them

**Refused per program**, each where FreeType fails the face and pdfium draws a substitute:

- a table directory with no valid entries
- no head, hhea or hmtx table
- a head shorter than 54 bytes, or an hhea or vhea with fewer than 36 bytes from its offset
- a unitsPerEm outside FreeType's 16 to 16,384

An hmtx that runs past the end of the file is clamped to a multiple of 4 bytes, as FreeType's
directory rule clamps it, and is missing if nothing is left.

**A glyph is shifted by its left side bearing.** FreeType moves every glyph it loads by its first
phantom point, the glyph's xMin less its hmtx bearing, so that the bearing and not the glyf
coordinates sets where the ink starts (`TT_Load_Glyph`, `ttgload.c`). ADR 0016's fixtures kept
the two equal and never saw it. A corpus SymbolMT subset's bullet has an xMin of 0 and a bearing
of 106, and every one drew 2 pixels from pdfium's at 288 dpi. They now draw within a pixel of it.
The shift is FreeType's:

- the bearing is `tt_face_get_metrics`' reading, and 0 wherever hmtx has no entry for the glyph
- an empty glyph's xMin is 0
- a composite takes its own phantom point, unless a component carries `USE_MY_METRICS`, in which
  case the last such component's replaces it
- the shift is converted from font units, so it holds at any units per em

**Refused per glyph:** a composite that nests more than 5 deep, visits more than 1,024 glyphs, or
draws more than 65,536 segments.

### A simple font's dictionary is read as pdfium reads it, or refused

pdfium reads some font dictionary entries under a looser type or a wider range than §9.6 allows.
Where the two readings would select different glyphs, the font is refused as irregular, whatever
program draws it:

- a `/BaseFont` that is not a name, which pdfium still matches against Symbol and ZapfDingbats
- an `/Encoding` that is neither a name nor a dictionary, or whose `/BaseEncoding` is not a name
- a `/Differences` that is not an array, starts with a name, holds a code outside 0 to 255, or
  holds an entry that is neither an integer nor a name
- a font descriptor's `/Flags` that is absent, which Table 120 forbids, or null, dangling, not an
  integer, or outside the signed 32-bit range
- a `/Flags` with both or neither of the symbolic and nonsymbolic bits, which Table 121 forbids

The range stops at 2^31 − 1 because pdfcpu drops a `+` sign before this backend sees the value,
and above that pdfium reads `+2147483652` as 0 and `2147483652` as its bits.

A TrueType font read by code is also refused when no cmap subtable survives and its post table
names glyphs, since FreeType then builds a Unicode map from those names and pdfium reads through
it.

### Where this backend and pdfium differ, on purpose

- **pdfium grid-fits a glyph's origin to the device pixel**, and this backend places it where the
  text matrix puts it. At a low resolution a thin stroke half a pixel off can score worse than no
  stroke at all.
- **pdfium draws a glyph under 50 pixels per em through FreeType's LCD mode**, even into a
  greyscale bitmap. `CFX_RenderDevice::DrawNormalText` chooses it for any display device with more
  than one bit per pixel. The glyph is rasterized at three times its width, filtered across by
  FreeType's default five taps, 8, 77, 86, 77 and 8 (`cfx_face.cpp`), and averaged back to one
  channel with a gamma adjustment. A vertical edge on a pixel boundary leaks about 38 levels into
  the column outside it. This backend fills the outline at its own coverage, as it fills any path.
- **Above 50 pixels per em pdfium fills the glyph as a path**, and places a curve's
  anti-aliasing as ADR 0015 describes.
- **pdfium hints a TrueType glyph at 50 pixels per em or fewer.** Its path route loads every font
  but a tricky one unhinted, and its raster route loads TrueType hinted (`cfx_face.cpp`). Hinting
  rounds the phantom point to the pixel grid before the shift. This backend rounds nothing, so a
  small glyph can land up to a pixel from pdfium's.
- **FreeType floors each charstring point to a 64th of a pixel** at the size it loads the glyph at
  (`psobjs.c`). On the path route that size is 64 pixels per em, so a point moves by up to 1/4,096
  of an em. This backend keeps the charstring's coordinates as written.
- **WinAnsi's unused codes.** Annex D's note 3 sends every unused code above octal 40 to the
  bullet, "subject to future reassignment", and pdfium draws the bullet. This backend's table
  leaves 0x7F, 0x81, 0x8D, 0x8F, 0x90 and 0x9D unassigned, and refuses them per glyph.

## Acceptance

Against pdfium, each bound the worst measured.

**Drawing** (`TestCFFAgreesWithPdfium`) has 37 cases:

- text as one glyph, a line, a `TJ` with adjustments, word spacing and a rotated text matrix
- each path operator
- flex and its three shorthands, with both of flex1's branches
- the moveto forms, with and without a width
- nine stems and a two-byte hint mask
- local and global subroutines through their bias
- seac, with and without a width
- a 2,000-unit em, and a shear
- a CID-keyed font, with a composed FontMatrix and both FDSelect formats

The worst mean difference is 0.205 levels, against ADR 0016's bound of 1.5. Ink agrees to within
1.2%, against a bound of 10%. 25 cases have no pixel more than 32 levels off. Each of the other 12
is allowed exactly the count measured, from 1 to 58, and each is one of the two conventions above:
FreeType's LCD mode below 50 pixels per em, or ADR 0015's anti-aliasing above it.

**Routes** (`TestCFFCodeToGlyphTakesEachRoute`, `TestCodeToGlyphTakesEachRoute`) have 17 CFF cases
and 11 TrueType ones. Each is a font where the route under test and some other route reach
different glyphs. It is compared pixel for pixel with the glyph the route should reach, drawn
through a reference font, in this backend and separately in pdfium. Every case agrees exactly in
both. The CFF cases cover:

- each built-in encoding format, a supplement, and the predefined Standard encoding
- each charset format, and ISOAdobe
- a CID through the charset and through an embedded CMap
- a CID taken as the glyph index

The TrueType cases cover each of §9.6.5.4's four (3,0) ranges, and (3,0) ahead of (1,0).

**Refusals** (`TestCFFFontsOutsideTheAgreedRoutesAreRefused`,
`TestSimpleTrueTypeFontsOutsideTheAgreedRoutesAreRefused`) have 33 CFF fonts and 20 TrueType ones.
They cover each reason above that is refused at parse time, per font or per code, and one refused
charstring. Each page is refused as `*Unsupported`, naming its reason. The charstring reasons are
pinned in `font`'s own tests:

- `add` and `random`
- each operator's wrong operand counts
- a stem after a hint mask
- a path before the first moveto
- a charstring that runs off its end

**Work** (`TestCFFGlyphWorkIsBoundedPerPage`, and six tests in `budget_test.go`):

- a glyph shown three times is built once and charged for each show
- two glyphs are charged together
- work of exactly the budget is within it, and one operation more is refused, whichever format
  supplies the last unit
- drawing alone can exceed the budget
- the charge stays bounded once a page is over it
- a glyph's build is given only what the page has left

**Review.** Five rounds of adversarial review against FreeType's and pdfium's sources added the
parse-time, per-program and dictionary refusals above, and the left side bearing shift. Each
finding is pinned by a test. `render/native` has 50 such tests of 97 cases, in six files, and
`font`'s `truetype_cmap_test.go` has 31.

**fontTools**, as a one-off second implementation, since it is not a dependency. It agrees with
`CFF.Outline` point for point on all 1,378 corpus glyphs in 60 programs. The worst difference is
1.1e-13 of a thousandth of an em.

**Fuzzing:** `FuzzParseCFF` ran 41.1 million inputs in a minute, through `ParseCFF` and every
glyph's outline. None panicked, and none drew a non-finite coordinate.

**Corpus:** 1,129 pages drew before, and 1,232 draw now. None was lost.

- **The 103 pages gained** were each rendered at 72 dpi with their 46,118 CFF glyphs drawn and
  skipped. Every one is closer to pdfium with them drawn, over the whole page and over the pixels
  they touch. On those pixels the mean difference is at most 50 levels, and at least 57 without
  them.
- **Of the 1,129 that drew before**, 983 score the same against pdfium at 36 dpi. 103 are closer
  and 43 further, by at most 0.0065 in mean. Of the 43:
  - 31 draw a dash that was missing before, half a pixel from where pdfium's grid-fitted origin
    puts it. At 150 dpi every changed region is closer.
  - 12 moved with the left side bearing shift. At 36 dpi a glyph under 100 points is 50 pixels
    per em or fewer, so pdfium hints it and rounds the shift to the pixel. At 600 dpi, where it
    does not, all 31 pages the shift moves are closer. On the pixels it changes, the mean
    difference falls from 165–192 levels to 1.1–1.5.

**348 mutants across `font`'s CFF, charstring and TrueType readers and `render/native`'s text
path: 346 are killed, each by a named test, and the other two are equivalent.**

- A charset range is trimmed where `sid+left > 0xFFFF`. Trimming at equality instead sets `left`
  to what it already is.
- An FDSelect range writes its glyphs up to `next`. Writing `next` too is overwritten by the
  following range, which must start there, or is past the last glyph.

Three `font` mutants died at first only in `render/native`: a term of the composed FontMatrix, a
named base encoding's glyph names, and a program with one cmap subtable. Each now dies in `font`'s
own tests. The composition's other seven terms were then mutated as well, and all eight die there.
No kill needs the corpus. The `render/native` comparisons hold in any clone, because pdfium is a
module dependency and a failure to open it is fatal, not a skip.

The first round killed 143 of 190. Of the rest, none was a gap in the code:

- Six were invalid, and failed the build rather than a test. Rewritten as ten, all die.
- The two equivalents.
- One stem-limit test declared 97 stems on one operator. The 48-operand stack refused it before
  the stem limit was reached, so the limit went untested. The test now declares them 24 to an
  operator.
- The other 38 were fixtures that could not see their rule. Among them, values at a boundary:
  - 33,900 subroutines for the bias
  - 48 operands, and a call ten deep
  - 96 stems
  - DICT lead bytes 31 and 250, and an INDEX offset size of 4
  - composites exactly 5 deep, of 1,024 components and of 65,536 segments
  - a glyph of 10,000 points
  - a page's work of exactly the budget

The last round ran all 349 mutants of the five review rounds against the merged tree, and killed
338. Of the rest:

- Three were invalid. Rewritten, all die.
- The two equivalents.
- Two were fixtures that could not see their rule: a glyph that fails having spent nothing at
  exactly the budget, and a `/Differences` code of 256.
- Four sat on TrueType bounds the byte readers already hold, since they answer 0 past the end of
  a table, or on one a later bound subsumes. No test can tell such a bound from its absence, so
  the bounds were deleted, and two more mutants on them with them.

Five mutants were then added, three on what replaced those bounds and two on format 12's length
bound. Two needed a fixture: a format 12 length one past its table, and a glyph below the six
tables the directory declares, which a read through an absent `maxp`'s zero offset finds.

## Consequences

- **`render/native` draws 1,232 of 1,251 corpus pages (98%)** with substitute faces, and 95
  without. The corpus test's floor is 1,232.
- **103 of the 104 pages CFF blocked now draw.** The other one, WTPDF page 3, is refused for a
  dash.
- **The remaining worklist**, 19 pages, with some overlap:
  - Type 1: 17
  - a dash: 2
  - one soft-mask group
  - one shading
- **Not built:** CFF2 and the OpenType wrapper, CharstringType 1, the Expert tables, and the
  charstring arithmetic operators. Each refuses by name, and none has a corpus page.
- **Deferred, because each needs a change below the font readers:**
  - The budget counts segments, not what filling them costs. The review drew a crafted page
    within the budget that took tens of seconds.
  - pdfcpu reads an integer outside the platform's `int` as 0. It also strips a leading `0`
    before a sign, so `0+4` reaches this backend as 4 and pdfium reads 0. No check on the parsed
    value can see either, and both reach every integer the renderer reads, `/Widths` among them.
    The fix belongs in the pdfcpu adapter.
  - `objects.Store` follows a reference to a reference to its end, and pdfium follows one level.

# 4. Extract images without compositing, and preserve the original codec

Date: 2026-08-03

## Status

Accepted. Amended by [ADR 0007](0007-invert-matte-pre-blending-in-the-decoder.md), which
moves `/Matte` un-premultiplication into this package: the "hand over both layers" position
below holds for the mask file and for the 5 images with a DCT base, but the other 131 base
images now carry their true colours.

> **Counts corrected, decision unchanged.** The population table below was measured over
> twelve files, because `corpusFiles` globs `docs/*.pdf` and that directory then also held
> a non-spec paper. The spec corpus is eleven files: **239 images, 142 with `/SMask`, 184
> Flate, 51 DCT, 227 DeviceRGB, 235 at 8 bpc**, and the largest is **1169×1394 raw
> DeviceRGB** — the 6049×4090 DCT below was the extra paper's. The table is left as it was
> written, since it records the evidence this decision was actually taken on. Nothing the
> decision rests on moves: `/SMask` is still the dominant case (59% rather than 58%), and
> the `/Matte` figures — 136 masks, all `[0 0 0]`, 131 invertible — were never affected,
> which is why ADR 0007's analysis stands unamended.

## Context

`docs/DESIGN.md` §8 scoped Phase 3 as "`bits`, DCT and CCITT wiring, `images` verb.
Embedded images extracted with their original codec preserved where possible." Measuring
the corpus before writing any of it moved two premises, one of them enough to change what
the verb outputs.

The population, deduplicated by indirect reference across all 12 files:

| | count |
|---|---|
| images | 245 |
| FlateDecode | 185 |
| DCTDecode | 56 |
| no filter | 4 |
| CCITT · JBIG2 · JPX | **0 · 0 · 0** |
| DeviceRGB · DeviceGray · ICCBased | 233 · 10 · 2 |
| 8 bits per component · 1 bit | 241 · 4 |
| `/SMask` present | **143** |
| `/SMask` with `/Matte` | **136**, all `[0 0 0]` |
| `/Mask` · `/Decode` · `/ImageMask` · `/Indexed` | 0 · 0 · 0 · 0 |
| inside a Form XObject | 7 |
| largest | 6049×4090 DCT DeviceRGB |

Deduplication is what makes 245 the number. ISO 32000-2 draws shared images across 1,023
pages; counting placements rather than objects reports thousands of copies of the same
bytes.

Three of these change decisions rather than merely describing the corpus.

**`/SMask` is the common case.** 143 of 245, against a roadmap line that did not mention
soft masks at all. Any design that treated transparency as an edge case to handle later
would be wrong for 58% of the corpus.

**136 of those masks carry `/Matte`.** Per §11.6.5.3 that means the base image's colour
samples are *premultiplied* against the matte colour. The samples are not the colours they
appear to be, and un-premultiplying requires the alpha channel. An extractor that wrote
only the base image would emit 136 images that look approximately right, are quantitatively
wrong, and carry no indication of it.

**Nothing in the corpus exercises CCITT, JBIG2, or JPX.** So the phase's "CCITT wiring"
cannot be validated against a real file here, and `bits` — described in DESIGN.md as the
"foundation for CCITT and JBIG2" — has no bit-stream codec to be the foundation of yet.

## Decision

**Extract per object, not per placement.** An image XObject is identified by its indirect
reference and reported once, attributed to the first page that draws it under the name used
there. Positioning is not recovered: the same XObject may be drawn many times at different
sizes by different CTMs, so there is no single placement to report. `render` owns placement.

**Preserve the codec wherever it has a container of its own.** A DCTDecode stream is
written as the `.jpg` it already is, byte for byte, with no parse and no re-encode. This is
not only a fidelity argument — though a re-encode of the 6049×4090 image costs a
generational quality loss for nothing — it is a robustness one: a JPEG this build cannot
decode is still a JPEG a viewer opens, and refusing to write it because the standard library
disliked a marker would lose data to protect nothing. Only the codecs with no standalone
container — packed samples behind Flate/LZW/no filter, and CCITT — are decoded and
re-encoded, as lossless PNG.

**Do not composite the soft mask into the base image.** The mask is written as its own file
alongside it. Flattening is a rendering decision that needs a backdrop, and for the 136
premultiplied images it is not recoverable from the base image alone. `Image.Premultiplied`
reports the condition so a consumer can act on it rather than discovering it as a colour
shift. The base image's PNG does carry the mask as an alpha channel — that is lossless and
costs nothing — but the mask's own samples remain available as a separate file, which is
what a consumer that needs to un-premultiply requires.

**Do not invent a colour for a stencil mask.** An `/ImageMask` paints the fill colour in
force at its `Do` operator, which is graphics state the extractor does not have, and the
same mask may be painted in several colours in one document. Black-on-transparent shows the
shape the mask describes without asserting a colour it never carried. None appear in this
corpus; the path exists because the format allows them and a file that has one must not
produce a blank image.

**Wire CCITT anyway, and say what that guarantee is worth.** `golang.org/x/image/ccitt`
(BSD-3) is wired per the DESIGN.md §5 borrow table, with the `/DecodeParms` handling —
`/K`, `/Columns`, `/BlackIs1`, `/EncodedByteAlign` — that a CCITT stream requires because,
unlike a JPEG, it carries no header stating its own width or variant. The tests are
fixtures derived by hand from the ITU T.4/T.6 code tables, and they check that the
parameters reach the decoder correctly rather than that the decoder is correct. That is
weaker than every other test in the package and is stated as such in the test's own comment.
It is also the reason CCITT stays borrowed rather than becoming owned: owning a codec no
corpus file exercises would be writing unvalidated code with no way to tell.

**Recognize JBIG2 and JPX; refuse to write them.** Both are named in `Codec` so `-list` can
state what a file contains, and both fail `Ext` and `Encode`. Producing a file whose
extension promises contents this build cannot generate is worse than declining, and a run
that skips images reports the count and the codecs, because a silent partial extraction is
indistinguishable from a complete one.

**Declare no stride rather than guess one.** A colour space whose component count cannot be
determined yields `Components == 0`, which blocks re-encoding. A wrong component count does
not produce a slightly wrong image, it produces a diagonal smear, and emitting that is worse
than declining to emit anything.

## Consequences

`bits` is justified by a nearer consumer than the one DESIGN.md named. Sub-byte samples are
packed several to a byte with each row re-padded to a byte boundary (§8.9.5.1), so a 1-bit
image read with byte arithmetic decodes its first row correctly and skews every row after
it — a failure that presents as a decoder bug. `Align` is the per-row call that prevents it.
Writing the reader also surfaced a real defect immediately: the aligned-byte fast path is
valid only in MSB order, because composing an LSB-order byte most-significant-first reverses
it (`0x7F` reads as `0xFE`). The fast and slow paths are now pinned against each other by a
test rather than assumed to agree.

The corpus counts are now assertions. `TestISOImageInventory` pins 224 images / 49 JPEG /
175 raw for ISO 32000-2, `TestCorpusSoftMasks` pins 143 masks and 136 `/Matte`, and
`TestCorpusImagesEncodeToValidFiles` requires all 245 to encode to a file that decodes —
with zero unsupported, since the corpus has no JBIG2 or JPX. A future file that introduces
one will fail that test by name, which is the notification that the JBIG2 port has moved
from Phase 6 to now.

Two soft masks are DCT-encoded, and their alpha is therefore not applied to the base image:
turning a JPEG mask into alpha means running it through the JPEG decoder, which would import
the decode this package exists to avoid on the base-image path. Those two base images are
written fully opaque, and the masks are written as their own `.jpg` files, so nothing is
lost — but the alpha is not pre-applied for them as it is for the other 141.

Four masks differ in size from their base image, so mask coordinates are scaled by nearest
neighbour rather than assumed to match. Assuming a match reads outside a smaller mask's
buffer and silently crops a larger one.

`/Width` and `/Height` are attacker-controlled integers whose product sizes the buffer the
encoder allocates, so they are bounded at read time — before allocation — at 256 million
samples, roughly ten times the corpus's largest image. A dictionary claiming 2^40 by 2^40
costs a producer nothing to write and would otherwise ask for an exabyte.

16-bit samples are read but written as 8-bit, because the PNG path is 8-bit. That is a real
loss of precision and it applies to no image in this corpus. ICC profiles are read only for
their component count (`/N`) and are not applied: honouring one means colour management,
which is `render`'s concern. CMYK and Lab conversions are the naive ones — correct for the
device spaces, an approximation for the CIE-based ones.

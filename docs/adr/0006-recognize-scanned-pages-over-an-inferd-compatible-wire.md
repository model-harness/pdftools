# 6. Recognize scanned pages through a DocTags model over an inferd-compatible wire

Date: 2026-08-04

## Status

Accepted

## Context

`docs/DESIGN.md` §8 scoped Phase 4 as "render and OCR". The render half landed as ADR 0005.
This half has to answer four questions that are each cross-cutting and each expensive to
reverse: which pages go to a model, what the model returns, who runs the model, and how this
repo talks to it.

The population that motivates it is measurable in the fixtures. `pymupdf-utilities/scanned.pdf`
has no text layer at all — zero coverage. `adobe-samples/ocrInput.pdf` is four such pages.
`adobe-samples/disqualifiedScannedPages.pdf` is 151 pages with zero fonts and zero images,
which is Adobe's own disqualified-input fixture. Against those, `pymupdf/2201.00069.pdf`
covers 72.9% of its page with text and `adobe-samples/extractPdfInput.pdf` 80.6% across three
pages. The two populations are not adjacent, which is what makes a router possible at all.

Two further measurements shaped the answers rather than merely describing the input.

**A page with one line is indistinguishable from a scan, and correctly so.**
`pymupdf-utilities/test.pdf` is 711 bytes of a single line on a full page: 0.3% coverage. Any
rule that classified it as "has text, skip it" would also skip a scanned page carrying a
Bates number or a stamped folio, which is the exact case that loses a whole page of content.
So the router's unit is area, not characters, and a page holding one line genuinely is a page
with essentially no text.

**llama.cpp's documented media marker is not the one a running server uses.**
`get_media_marker()` in `tools/server/server-common.cpp` returns `"<__media_" + random +
"__>"` unless `LLAMA_MEDIA_MARKER` is set, while `mtmd_default_marker()` still returns the
documented `"<__media__>"`. A client that placed the documented marker in a `/completion`
prompt would fail against a real server, and fail as a model-quality problem rather than as a
protocol error.

## Decision

**Route by text coverage, at a 5% default.** `ocr.Route` sends a page to a model when
`doc.Page.Coverage()` — the union of block rectangles over the crop box — falls below a
threshold. Coverage rather than a character count, for the stamped-header case above. A union
rather than a sum of areas, because overlapping blocks would double-count and push a scan
above the threshold. 5% rather than 0, because 0 catches only a pure scan and leaves the mixed
page silently yielding one line; the default sits an order of magnitude below what a page of
prose reaches, so its exact value is not load-bearing. A threshold of 0 is the off switch and
1 forces every page, which is how a caller expresses both extremes without a second flag
meaning the same thing.

**A model that emits DocTags, not Markdown.** granite-docling-258M returns a structured tag
stream — `<text>`, `<otsl>`, `<picture>`, `<section_header_level_N>`, each with `<loc_>`
coordinates — which `ocr/doctags` parses into the same `doc.Page` the extractor produces. A
model that wrote Markdown would have already discarded what it knew: which line was a
heading, where the table's cells were. Recovering that means running layout heuristics over
generated prose, which is this repo's original problem with a strictly worse input. Both
paths converging on `doc.Page` is what lets a document be part scanned and part digital with
no sink knowing which pages were which; `doc.Page.Rasterized` is the only trace the model
leaves.

The model choice is also a licensing gate rather than a preference: granite-docling's base
weights are Apache-2.0, and a repo licensed MIT cannot make a copyleft model its default.

**A subprocess, not CGO.** `ocr/docd` runs `llama-server` as a child process and talks HTTP
to it on loopback. Linking llama.cpp means CGO, which changes what `go build` requires of
every user of this library — DESIGN.md §9 was amended to state that the rule is about linkage
rather than language, precisely because of this decision. A subprocess keeps the CLI pure Go
and one file, puts the GPU-linked code in a process that may be absent entirely, and confines
a model that segfaults on a malformed page to a child. The cost is a process boundary and an
HTTP hop, which against tens of seconds of generation does not register.

**`/v1/chat/completions` with a data-URI image, not `/completion`.** The chat endpoint
substitutes the live media marker itself, which removes the randomized-marker defect above
instead of working around it. PNG rather than JPEG in the data URI: the payload is a page of
text destined for an OCR model, and JPEG's ringing artifacts land on exactly the
high-contrast glyph edges the model reads.

**Find the executable; never download it.** `docd` locates `llama-server` on `PATH` and prints
the official per-platform install command when it is absent. Fetching and executing a binary
on a user's behalf is a supply-chain step of a different kind than fetching data, and it is
not one a PDF tool takes quietly. Model *weights* are data and go through llama.cpp's own
`-hf` cache with its own integrity checks — deliberately not reimplemented, because a second
downloader means a second cache, a second checksum policy, and two places for a partially
written GGUF to hide.

**The IPC wire is byte-identical to inferd's generation protocol v2.** `ocr/ipc` implements
`[uvarint payload_len][1 byte frame_type][payload]` with `WireVersion 1` in band, a 64 MiB
cap checked before allocation, `0x01` JSON / `0x02` BLOB frames, inferd's socket-resolution
chain, and its enumerated error codes. The requirement is that inferd be a drop-in
replacement once it carries docling, and byte compatibility is what that actually means —
matching a JSON idiom would not be enough. Framed binary rather than NDJSON because a 200 DPI
page is roughly 12 MB of arbitrary octets and base64 would cost a third more plus a copy each
way; images ride as interleaved RGB with no alpha, matching inferd's ADR 0016, because the
daemon links no image codec.

Reimplemented rather than imported. inferd's Go client has no server side, this repo needs
one, pdf-spec stays dependency-free, and a shared module would couple two release cadences.
The cost is drift, and it is mitigated by tests that fail when upstream's constants or field
names change rather than by hoping.

**Two paths behind one interface, proven equivalent.** `ipc.Local` reaches a `Handler`
in-process and `ipc.Engine` reaches one over the socket; both are `ocr.Engine`, and a test
asserts the same handler yields identical text either way. The verb defaults to local, because
for one CLI invocation over one document the model is loaded and discarded by the same run and
a socket between two halves of one process buys nothing. `-addr` selects the other, which is
the case that earns the protocol: a warm host serving many invocations, or a host on the
machine with the GPU. Neither side can tell which one it is, which is the whole point.

**A failed page keeps its extracted text.** Recognition replaces a page's blocks only when it
produced blocks. A page the model failed on, or read as empty, keeps whatever the content
stream said — sparse, but what the file actually states — because replacing it with nothing
would make a failed call indistinguishable from a blank page. A generation that errored *after*
emitting DocTags is kept and parsed, since a token bound cutting a dense table mid-grid is the
dominant failure and `ocr/doctags` reads truncated input by design.

## Consequences

**Recognition is serial, with no `-jobs` flag.** The bottleneck is one model: the host runs
one slot, the wire allows one in-flight request per connection, and `ipc.Local` serializes for
the same reason. Page-level fan-out would add queueing and interleaved progress output without
adding throughput. Rasterization is milliseconds against tens of seconds of generation and is
not worth parallelizing alone. This is the one place the OCR verb diverges from the render
verb's structure, and it diverges because the resource does.

**DocTags Y is top-down and PDF user space is bottom-up, so the parser owns the flip.** A
document parsed without it reads correctly and has every block's rectangle mirrored, which no
text comparison would catch. `loc_` values are a 500-unit normalized grid — `round(500*val)`
clamped to `[0, 499]`, four tokens in x0,y0,x1,y1 order — pinned from
`docling_core/types/doc/tokens.py` at commit `23fa247e`.

**Two tokens are in both the element and the picture-classification sets.** `<table>` and
`<chart>` appear in `DocumentToken` and in `_PictureClassificationToken`, and a
classification is shaped exactly like an element name. Resolved by nesting context: inside a
figure, one of those names is the classification and lands in `Block.Alt`. Getting this
backwards is not a subtle failure — the first implementation read every element name as a
classification and produced 561 paragraphs and no headings from a 9-page paper.

**A rotated page's recognized blocks are in the rotated frame.** The model saw the rotated
pixels, so `render.Raster.Box` supplies the extent while `doc.Page.Box` supplies the origin.
The recognized page carries no `/Rotate`, which says so exactly; a sink that applied the
original rotation to these boxes would place them twice.

**Windows cannot serve the socket.** A named-pipe *server* needs `CreateNamedPipe` with an
explicit security descriptor, which the standard library does not expose, and a default pipe
ACL grants more than a loopback inference endpoint should. The client side works, and the
in-process path makes serving unnecessary there — the platform where a shared warm model
matters is a Linux box with a GPU, which does have one.

**Security posture, stated rather than assumed.** `llama-server` binds 127.0.0.1 with no
option to change it, because an inference endpoint reachable off-box is an unauthenticated
GPU for anyone who finds it. The Unix socket directory is 0700 and the socket 0600; unlinking
a stale socket before binding is safe *because* of the 0700 directory, so the two are one
decision. Image dimensions are bounded before the `w*h*3` multiply so the product cannot wrap.
`EACCES` is not retried on dial — a socket owned by another user will not become accessible
by waiting.

**Generation is bounded by default at 8192 tokens per page.** The failure mode of a vision
model on a dense page is not silence but a repetition loop emitting the same table row until
something stops it; on a 200-page document that is the difference between an hour and a night.
The bound turns it into a truncated page the parser still reads.

**The parser is tested; the model is not.** `ocr/doctags` runs against upstream's own
MIT-licensed `.dt`/`.md` ground-truth pairs with no model, no GPU, and no daemon — 9 pages and
567 blocks across every role, plus a 25-case malformed-input matrix. The verb's pipeline is
tested with a fake `Engine`, which is what the interface is for. What no test here covers is
whether granite-docling reads a given page correctly, and no test in this repository can:
that is a property of the weights.

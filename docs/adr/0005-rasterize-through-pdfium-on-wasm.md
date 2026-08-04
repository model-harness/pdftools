# 5. Rasterize through pdfium compiled to WebAssembly

Date: 2026-08-04

## Status

Accepted. Amended by [ADR 0007](0007-invert-matte-pre-blending-in-the-decoder.md): the
`/Matte` ownership claim under Consequences is withdrawn. §11.6.5.3 requires the inversion
to precede colour conversion, which places it in `image`'s decoder, not here.

## Context

`docs/DESIGN.md` §5 names rasterization as the flagship native target and §9 commits to
"pure Go, no CGO, single binary, cross-compiles. The WASM rasterizer is chosen specifically
to preserve this." Phase 4 is where that commitment gets paid for, so the tradeoffs need to
be measured rather than asserted.

Rasterization is the one sub-problem in this repo that is genuinely a commodity. Everything
from `objects` to `sectionize` is native because that is exactly where the libraries in §1
fall down: structure-tree semantics, glyph advance and space inference, section
reconstruction. A page rasterizer is not that. It is a decade of glyph hinting, blend modes,
shading types 1 through 7, and knockout groups, and nobody gets it right twice. Writing one
now would delay the OCR path that depends on it by months and produce something worse than
what already exists under a license this repo can use.

Two consumers need pixels and nothing more: the `render` verb, which writes PNGs, and `ocr`,
which feeds a vision model. Neither needs a display list, a clip stack, or a device
abstraction.

The candidate was measured in a throwaway module before `go.mod` was touched, because three
of its behaviours are not documented in a way that could be assumed:

| | measured |
|---|---|
| binary delta, `cmd/pdfspec` on windows/amd64 | 10,416,128 → **20,890,624 bytes (+10.0 MB)** |
| embedded `pdfium.wasm` | 5,225,611 bytes |
| one-time module compile | **~1.4 s** |
| per page at 200 DPI | 3–8 ms |
| throughput at 1 / 2 / 4 / 8 workers | 7.6 / 4.3 / 3.4 / 3.7 ms per page |
| largest page in corpus + fixtures | 630 × 1008 pt → 4.9 Mpx at 200 DPI |
| rotated pages in the whole population | **0** |

## Decision

**Borrow `klippa-app/go-pdfium` (MIT) on the WebAssembly backend, behind an interface this
repo declares.** `render.Rasterizer` is three methods — `PageCount`, `Page`, `Close` — and no
go-pdfium type appears in any signature `render` or `render/pdfium` exports. The native
rasterizer of Phase 6 is a sibling package and a one-line wiring change in `cmd`.

**The WASM backend, not the CGO one.** go-pdfium ships both. The CGO build is faster and
would break `go build` for any target without a pdfium toolchain on the machine, which is
the property §9 protects. wazero is a pure-Go runtime, so cross-compilation still produces
one static binary.

The rule this follows is about *linkage*, not about language. A CGO dependency changes what
`go build` requires of every user of the library; a separate process does not, and §9 was
softened to say so once `ocr/docd` began running llama.cpp as a subprocess. So a linked C++
rasterizer is still ruled out here — the alternative is pure Go and costs only speed —
while a subprocess elsewhere in the repo is not a contradiction of it.

**The resolution policy lives in `render.Fit`, not in the adapter.** DPI, the pixel cap, and
the rounding direction are one function every adapter calls, because a native rasterizer that
capped differently would make two backends disagree about the same document. It is also the
only part of rendering testable without a rasterizer at all, and writing that test found a
real defect: a page reduced to land exactly on `MaxPixels` and then rounded *up* on both axes
lands past it — US Letter reduced to fit 2,097,152 pixels is 1272.999 × 1647.411, whose
product is the cap exactly, and 1273 × 1648 is 2,097,904. Capped pages floor; uncapped pages
ceil, because rounding down
otherwise drops the last fractional row of every A4 page.

**Cap at 64 Mpx and reduce rather than refuse.** `/MediaBox` is a producer-controlled pair of
numbers; a page declaring 200 × 200 inches costs nothing to write and asks for 1.6 gigapixels,
which at 4 bytes per RGBA pixel is 6.4 GB. The cap bounds the largest allocation at 256 MiB
and sits about thirteen times above the 4.9 Mpx largest page measured, so no real document at
a sane DPI reaches it. Refusing would make the cap a denial-of-service surface of its own,
where one oversized page fails a thousand-page run, and the pages that overflow it — a plotted
poster, an engineering drawing — are legitimate. `Raster.DPI` reports what was actually used
and the verb says so on stderr, because a silent reduction resurfaces later as "OCR got worse
on the big pages" with nothing in the output to explain why.

**Copy the bitmap out of every response.** This is not a precaution. go-pdfium's WASM
implementation assigns `img.Pix` directly from wazero's `Memory().Read`, which returns a view
into linear memory rather than a copy, and `Cleanup` frees the pdfium-side bitmap that view
points at. Returning the image uncopied hands back pixels the next page overwrites, and the
symptom is one page's content appearing in another's — much harder to recognize than an
allocation. The copy is row by row rather than one `copy` of `Pix`, because a bitmap whose
stride exceeds 4×width has inter-row padding that a flat copy would skew.

**Ask in pixels, not DPI.** `RenderPageInDPI` takes an `int`, and `Fit`'s reduction produces a
fractional DPI, so pixel mode is what makes the cap exact instead of rounded to a whole DPI.
It also keeps one code path for the reduced and unreduced cases.

**Trust `GetPageSize` for the page box.** It returns the crop box where present, otherwise the
media box, with `/Rotate` already applied — the *rendered* extent, verified against hand-built
`/Rotate` 0/90/180/270 pages (612 × 792 at `/Rotate 90` reports 792 × 612) and against a
fixture whose crop box is inset from its media box. That is exactly what `Fit` needs, so no box
arithmetic happens in the adapter. `Raster.Box` therefore differs from `doc.Page.Box`, which
carries the unrotated box with `/Rotate` as a separate field; the difference is documented on
both.

**One process-wide pool, created on first use, never torn down.** Compiling `pdfium.wasm`
costs ~1.4 s and the result is immutable, so a per-document pool would pay that per file —
which for a directory of PDFs is the dominant cost. There is nothing to tear down for: the
runtime holds a compiled module and no file handles, process exit reclaims it, and a `Close`
racing a live Rasterizer would crash rather than clean up. A long-running server wanting the
memory back should keep its own pool; this package targets a CLI.

**A Rasterizer is single-threaded by contract; parallelism is several of them.** Each holds one
WASM instance with its own linear memory, and sharing that across goroutines is a data race in
the adapter that no interface can paper over. The `render` verb opens one per worker over the
same file. `-jobs` defaults to `min(4, NumCPU)` because throughput stopped improving past 4 on
a 32-core machine — page rendering is memory-bandwidth bound, not CPU bound — and the pool is
capped at 16 instances, which is a memory bound rather than a CPU one.

**Annotations are off by default.** An annotation is a layer over the page, not part of it, so
a rendered page with comments burned in does not show what the document says. For the OCR path
that difference becomes text a reader takes as the document's own. `-annots` turns it on for a
caller reproducing what a viewer displays, and it sets *both* pdfium switches: `RenderForm`
draws form fields via `FPDF_FFLDraw`, while every other subtype's appearance stream needs the
separate `FPDF_ANNOT` render flag. Setting one without the other is a no-op for the common
case, which is how it shipped in the first draft and what the flag's first test caught.

## Consequences

**A rendered run parses the file twice.** pdfium brings its own parser and cannot be handed an
`objects.Store`. There is no honest way to share one, so a run that both extracts and renders
reads the bytes twice, and the two parsers may disagree about a damaged file. That is the
borrow showing through, and it is why `render.Rasterizer` declares its own `PageCount`: a
caller looping over pages must use the number belonging to the thing it calls.
`TestOpenPageCounts` pins pdfium's counts against the ones pdfcpu reports for six fixtures —
1 / 1 / 1 / 285 / 4 / 151 — so a future divergence fails by name.

**The binary roughly doubles.** +10.0 MB is the largest single cost in the repo and it is
unavoidable for anything that imports this package, which is why nothing else does: the two
verbs reach it through the interface, and a build excluding them drops the module. This is also
the strongest argument for the Phase 6 native rasterizer, above and beyond independence.

**`/Rotate` is untested against real data.** Zero of the 1,729 pages across the fixtures and
the corpus are rotated, so the rotation behaviour rests on four PDFs built by hand for the
spike rather than on a document a producer emitted. Stated rather than implied, because it is
the one verified-by-construction claim in the adapter.

**ADR 0004's deferred compositing now has an owner but not yet an implementation.** `render`
is where a backdrop exists, so it is where `/Matte` un-premultiplication belongs. pdfium
composites soft masks itself when rendering a page, which covers the rendered path; what
remains unbuilt is the extraction-side path that turns 136 premultiplied base images back
into their true colours. That is a `render`-adjacent utility, not part of `Rasterizer`, and it
is deliberately not in this interface.

> **Withdrawn by [ADR 0007](0007-invert-matte-pre-blending-in-the-decoder.md).** The
> paragraph above is wrong about placement, and this ADR is left unedited only because an
> accepted ADR is immutable. §11.6.5.3 requires the inversion to precede colour conversion,
> so it must run on samples in the parent image's own colour space — a state that exists
> only inside `image`'s decoder, where it now lives. The rendered path is unaffected:
> pdfium does composite soft masks itself, as stated. No `render`-adjacent utility was
> built, and none should be.

**The `render` verb tolerates per-page failure.** One page that fails does not fail the run —
a 151-page scan with one broken page should still yield 150 images — but the count is reported,
up to five failures are named individually, and the exit status reflects it, because a silent
partial render is indistinguishable from a complete one. A partially written image is deleted
rather than left, since it looks like a success and fails only when something opens it.

**Page numbers stay 1-based at this boundary.** pdfium is 0-based. The conversion is in one
place and pinned at both ends by a test, because page 0 would silently render page 1 and
`PageCount+1` would silently render nothing.

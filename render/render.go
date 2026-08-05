// Package render turns a page into pixels.
//
// It exists as its own package for one reason: rasterization is the only
// sub-problem in this repo that is genuinely a commodity. Everything from
// `objects` to `sectionize` is native because that is where the libraries in
// DESIGN.md §1 fall down, but a page rasterizer is a decade of glyph hinting,
// blend modes, and shading types that nobody gets right twice. So this package
// declares what the rest of the repo needs from one and nothing more, and
// render/pdfium supplies it. DESIGN.md §5 lists the native rasterizer as the
// flagship replacement target; when it arrives it is a second adapter behind this
// interface, not a change to any caller.
//
// The interface is narrow on purpose. A rasterizer is asked for one page at one
// resolution and hands back an image — no display lists, no clip stacks, no
// device abstraction. Two consumers need exactly that: the `render` verb, which
// writes PNGs, and `ocr`, which feeds a vision model. Anything wider would be
// designed for a caller that does not exist.
//
// Nothing here decodes a PDF. A Rasterizer is opened from a file by its adapter,
// independently of objects.Store, because a borrowed rasterizer brings its own
// parser and there is no honest way to share one. That means a rendered run reads
// the file twice, which is the cost of the borrow and is stated rather than hidden.
package render

import (
	"fmt"
	stdimage "image"
	"io"
	"math"

	"github.com/model-harness/pdftools/geom"
)

// Rasterizer renders pages of one document.
//
// Implementations are **not** safe for concurrent use. A rasterizer holds a
// parsed document and, for the WASM adapter, one WebAssembly instance with its
// own linear memory; sharing either across goroutines is a data race in the
// adapter, not something this interface can paper over. Page-level parallelism is
// several Rasterizers over the same file, which is why Close exists and why
// opening is the adapter's job rather than this package's.
type Rasterizer interface {
	// PageCount reports how many pages the document has.
	//
	// Present because the count a rasterizer's own parser reports may differ from
	// the one objects.Store reports on a damaged file, and a caller looping over
	// pages must use the number belonging to the thing it is calling.
	PageCount() int

	// Page renders the 1-based page n.
	//
	// A page that cannot be rendered is an error for that page, not for the
	// document: a 151-page scan with one broken page should still yield 150
	// images. Callers decide whether to stop.
	Page(n int, o Options) (*Raster, error)

	// Close releases the document and whatever runtime the adapter holds. Not
	// optional — the WASM adapter holds a compiled module and a worker pool that
	// outlive garbage collection.
	io.Closer
}

// Options is what a caller asks for. The zero value is not useful; use
// DefaultOptions and change fields from there.
type Options struct {
	// DPI is the requested resolution. 72 is 1:1 with PDF user space, since a
	// user-space unit is 1/72 inch by default (ISO 32000-2 §8.3.2.3).
	DPI float64

	// MaxPixels caps the output. When the requested DPI would exceed it, the DPI
	// is reduced to fit and Raster.DPI reports what was actually used.
	//
	// Reduce rather than refuse, because the pages that overflow a cap are real:
	// an engineering drawing or a plotted poster is a legitimate document, and a
	// coarse image of it is more useful than an error. Refusing would also make
	// the cap a denial-of-service surface of its own, where one oversized page in
	// a thousand-page file fails the run.
	MaxPixels int

	// Annotations renders annotation appearance streams and form-field content.
	//
	// Off by default, and the default is the load-bearing choice. An annotation is
	// a layer over the page rather than part of it, so a rendered page with
	// comments burned in does not show what the document says — and for the OCR
	// path that difference becomes text a reader will take as the document's own.
	// A caller reproducing what a viewer displays turns it on.
	Annotations bool
}

// DefaultOptions is 200 DPI capped at 64 megapixels.
//
// 200 DPI is DESIGN.md §6's default and is what the OCR models want:
// LightOnOCR-2 asks for 200 DPI at 1540 px on the longest edge, and granite-docling
// is trained at a comparable scale. It is also enough for the thing rasterization
// exists to serve here — a scanned page's text — where 150 DPI starts costing
// small type and 300 DPI costs four times the pixels for no measured gain.
//
// The cap is 64 Mpx because the image is RGBA: 4 bytes a pixel puts the largest
// allocation at 256 MiB. The largest page in this project's corpus and fixtures is
// 630 × 1008 pt (disqualifiedScannedPages.pdf), which at 200 DPI is 4.9 Mpx, so
// the cap sits about thirteen times above anything measured and will not be reached
// by a real document at a sane DPI. It is there for /MediaBox, which is an
// attacker-controlled pair of numbers: a page declaring 200 × 200 inches costs a
// producer nothing to write and asks for 1.6 gigapixels at this DPI.
var DefaultOptions = Options{DPI: 200, MaxPixels: 64 << 20}

// Raster is one rendered page.
type Raster struct {
	// Number is the 1-based page number, matching doc.Page.Number.
	Number int

	// Image is the rendered page. Opaque unless the page has transparency and the
	// adapter preserved it.
	Image stdimage.Image

	// DPI is the resolution actually used, which is Options.DPI unless MaxPixels
	// reduced it.
	//
	// Reported rather than assumed because a consumer that resizes to a model's
	// expected input needs to know the scale it is starting from, and because a
	// silent reduction is exactly the kind of quiet degradation that shows up later
	// as "OCR got worse on big pages" with no way to see why.
	DPI float64

	// Box is the extent this image covers, in points: the crop box where present,
	// otherwise the media box, **with /Rotate applied**.
	//
	// The rotation is the difference from doc.Page.Box, which carries the unrotated
	// box and keeps /Rotate as a separate field because a text sink does not need
	// it. Here it is already applied, because the image is: a 612 × 792 page with
	// /Rotate 90 renders 792 × 612, and a Box reporting the unrotated size would
	// not describe the pixels it sits beside. Callers mapping between the two must
	// account for the rotation themselves.
	//
	// Only the extent is meaningful. The origin is whatever the adapter had — the
	// pdfium one reports 0,0 because pdfium gives a size rather than a box — so a
	// caller must use Width and Height and not read position into Min. Fit ignores
	// the origin for the same reason.
	Box geom.Rect
}

// Reduced reports whether MaxPixels lowered the resolution below what was asked
// for.
func (r *Raster) Reduced(o Options) bool { return r.DPI < o.DPI }

// Fit resolves a request against a page box, returning the DPI to render at and
// the pixel dimensions that follow from it.
//
// This lives here rather than in an adapter because it is the whole of the
// resolution policy and every adapter must apply it identically — a native
// rasterizer that capped differently would make two backends disagree about the
// same document. It is also the one part of rendering that can be tested without
// a rasterizer at all.
//
// Dimensions round up and floor at one pixel. Rounding down loses the last
// fractional row of a page, and a zero-dimension image is not an image: a 4 × 4 pt
// stamp at 8 DPI is 0.44 px each way, and the honest answer is one pixel rather
// than a decode error from the PNG encoder.
func Fit(box geom.Rect, o Options) (dpi float64, w, h int, err error) {
	wp, hp := box.Width(), box.Height()
	if wp <= 0 || hp <= 0 || math.IsNaN(wp) || math.IsNaN(hp) || math.IsInf(wp, 0) || math.IsInf(hp, 0) {
		return 0, 0, 0, fmt.Errorf("render: page box %v has no area", box)
	}
	if o.DPI <= 0 || math.IsNaN(o.DPI) || math.IsInf(o.DPI, 0) {
		return 0, 0, 0, fmt.Errorf("render: dpi %v is not a positive number", o.DPI)
	}

	dpi = o.DPI
	capped := false
	if o.MaxPixels > 0 {
		// Scale both axes by the square root of the overshoot, which keeps the aspect
		// ratio while landing exactly on the cap.
		if px := (wp / 72 * dpi) * (hp / 72 * dpi); px > float64(o.MaxPixels) {
			dpi *= math.Sqrt(float64(o.MaxPixels) / px)
			capped = true
		}
	}

	// Rounding direction depends on whether the cap bound this page, and it has to.
	//
	// Normally dimensions round up, because rounding down drops the last fractional
	// row and column of the page. But a reduced DPI lands the exact dimensions
	// *on* the cap, so rounding either one up carries the product back over it: US
	// Letter reduced to fit 2,097,152 pixels is 1272.999 × 1647.411, whose product
	// is the cap exactly, and 1273 × 1648 is 2,097,904 — 752 pixels past the bound
	// the caller set. A test caught that after this comment first claimed it could
	// not happen.
	//
	// Rounding down is safe because floor(x) ≤ x on both axes and the exact product
	// is already ≤ MaxPixels. What it costs is a sub-pixel row of a page that is
	// being downscaled anyway, which is not a tradeoff worth a second parameter.
	round := math.Ceil
	if capped {
		round = math.Floor
	}
	fw, fh := round(wp/72*dpi), round(hp/72*dpi)

	// Bounded before the conversion to int, not after, because converting an
	// out-of-range float64 to int is implementation-defined: on amd64 it yields the
	// minimum int, which the one-pixel floor below then turns into 1. So
	// `-maxpixels 0 -dpi 1e18` on US Letter produced 8500000000000000000 × 1 — a
	// silently nonsensical answer where an error is the only honest one. maxDim is
	// generous rather than tuned: 1e9 px on an axis is already a hundred times any
	// real page at any real DPI, and MaxPixels is what bounds the sane cases.
	const maxDim = 1e9
	if fw > maxDim || fh > maxDim {
		return 0, 0, 0, fmt.Errorf("render: %v at %g dpi is %.0f x %.0f pixels, past any usable size", box, dpi, fw, fh)
	}
	w, h = int(fw), int(fh)

	// A zero-dimension image is not an image — the PNG encoder rejects it — so one
	// pixel is the floor. It cannot breach the cap: MaxPixels reaching here is at
	// least 1.
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return dpi, w, h, nil
}

// Package pdfium adapts github.com/klippa-app/go-pdfium (MIT) over pdfium
// compiled to WebAssembly to the render.Rasterizer interface.
//
// The WASM build rather than the CGO one is a requirement, not a preference:
// DESIGN.md §9 commits to pure Go, no CGO, single binary, cross-compiles, and
// rasterization is the one borrowed layer that would otherwise break that. The
// module runs on wazero, a pure-Go WebAssembly runtime, so `go build` for any
// target still produces one static binary with no toolchain on the machine.
//
// What that costs, stated plainly:
//
//   - **+10.0 MB of binary**, measured: cmd/pdfspec went from 10,416,128 bytes to
//     20,890,624 on windows/amd64. The embedded pdfium.wasm is 5,225,611 of that
//     and wazero's compiler and the bindings are the rest, so the cost is roughly
//     double what the module alone suggests. Nothing that imports this package can
//     avoid it, which is why nothing else in the repo imports it — the `render` and
//     `ocr` verbs reach it through render.Rasterizer, and a build that excluded
//     them would drop the module.
//   - **~1.4 s of one-time startup**, measured, to compile the module. Per page
//     after that is 3–8 ms at 200 DPI. So a Rasterizer is worth reusing across
//     pages and not worth creating per page.
//   - **A second parse of the file.** pdfium brings its own parser and cannot be
//     handed an objects.Store. A run that both extracts and renders reads the
//     bytes twice, and the two parsers may disagree about a damaged file. That is
//     the borrow showing through, and it is why render.Rasterizer reports its own
//     PageCount.
//
// Everything pdfium-specific stays in this file: no go-pdfium type appears in any
// signature this package exports. When the native rasterizer DESIGN.md §5 names as
// the flagship replacement target arrives, it is a sibling package and a one-line
// change in cmd, not a change to any caller.
package pdfium

import (
	"errors"
	"fmt"
	stdimage "image"
	"os"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"

	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/render"
)

// pool is the process-wide wazero runtime and compiled module.
//
// One pool for the process, created on first use, never torn down. Compiling
// pdfium.wasm costs about 1.4 seconds and the result is immutable, so a per-document
// pool would pay that on every file — which for the `render` verb over a directory
// is the dominant cost. Instances are cheap by comparison and are what a Rasterizer
// holds.
//
// Not torn down because there is nothing to tear down for: the runtime holds a
// compiled module and no file handles, the process exit reclaims it, and a Close
// that raced a live Rasterizer would crash rather than clean up. A long-running
// server that wants the memory back should keep its own pool; this package is aimed
// at a CLI.
var (
	poolOnce sync.Once
	poolInst pdfium.Pool
	poolErr  error
)

// maxInstances bounds how many WASM workers the pool will hand out at once.
//
// Each instance is a separate linear memory holding a parsed document and a page
// bitmap, so the number is a memory bound rather than a CPU one. 16 is above the
// point where rendering stops getting faster — measured 7.6 ms/page at 1 worker,
// 3.4 ms at 4, and no further gain at 8 on a 32-core machine, because the work is
// memory-bandwidth bound — and low enough that sixteen A4 pages at 200 DPI is a
// few hundred megabytes rather than an unbounded amount.
const maxInstances = 16

// instanceTimeout is how long Open waits for a free worker.
//
// A timeout rather than an indefinite wait: exhausting the pool means a caller
// leaked Rasterizers, and blocking forever turns that bug into a hang with no
// diagnosis. Long enough that a genuinely busy pool rendering large pages is not
// mistaken for a leak.
const instanceTimeout = 60 * time.Second

func getPool() (pdfium.Pool, error) {
	poolOnce.Do(func() {
		poolInst, poolErr = webassembly.Init(webassembly.Config{
			MinIdle:  0,
			MaxIdle:  maxInstances,
			MaxTotal: maxInstances,
			// Instances are reused rather than recreated. Creating one is cheap
			// relative to compiling the module but not free, and a CLI run opens
			// one document per Rasterizer.
			ReuseWorkers: true,
		})
		if poolErr != nil {
			poolErr = fmt.Errorf("pdfium: wasm runtime: %w", poolErr)
		}
	})
	return poolInst, poolErr
}

// rasterizer implements render.Rasterizer over one pdfium document.
type rasterizer struct {
	inst  pdfium.Pdfium
	doc   references.FPDF_DOCUMENT
	pages int

	// data must outlive the document. pdfium reads the buffer lazily, so releasing
	// it while the document is open reads freed memory.
	data []byte

	closed bool
}

var _ render.Rasterizer = (*rasterizer)(nil)

// Open reads the PDF at path.
//
// The whole file is read into memory rather than streamed. pdfium's reader
// interface would let it seek, but crossing the WASM boundary per seek is slower
// than one read for any file that fits, and the files that do not fit in memory do
// not fit in a rasterizer's page cache either.
func Open(path string) (render.Rasterizer, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- opening a caller-named file is the API
	if err != nil {
		return nil, err
	}
	return New(b)
}

// New reads a PDF from b. The slice must not be modified while the Rasterizer is
// open; it is not copied.
func New(b []byte) (render.Rasterizer, error) {
	pool, err := getPool()
	if err != nil {
		return nil, err
	}
	inst, err := pool.GetInstance(instanceTimeout)
	if err != nil {
		return nil, fmt.Errorf("pdfium: no worker available: %w", err)
	}

	d, err := inst.OpenDocument(&requests.OpenDocument{File: &b})
	if err != nil {
		// The instance goes back to the pool. Closing it here rather than leaking it
		// is what keeps a directory of broken files from exhausting the pool.
		_ = inst.Close()
		return nil, fmt.Errorf("pdfium: open: %w", err)
	}

	r := &rasterizer{inst: inst, doc: d.Document, data: b}
	pc, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: d.Document})
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("pdfium: page count: %w", err)
	}
	r.pages = pc.PageCount
	return r, nil
}

func (r *rasterizer) PageCount() int { return r.pages }

func (r *rasterizer) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	// The document first, then the instance. Closing the instance releases the
	// linear memory the document lives in, so the other order closes a handle into
	// freed memory.
	if r.doc != "" {
		_, _ = r.inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: r.doc})
		r.doc = ""
	}
	err := r.inst.Close()
	// Held until here so pdfium cannot read the buffer after it is dropped.
	r.data = nil
	return err
}

func (r *rasterizer) Page(n int, o render.Options) (*render.Raster, error) {
	if r.closed {
		return nil, errors.New("pdfium: rasterizer is closed")
	}
	if n < 1 || n > r.pages {
		return nil, fmt.Errorf("pdfium: page %d out of range (1-%d)", n, r.pages)
	}

	// pdfium is 0-based; every other page number in this repo is 1-based.
	pg := requests.Page{ByIndex: &requests.PageByIndex{Document: r.doc, Index: n - 1}}

	sz, err := r.inst.GetPageSize(&requests.GetPageSize{Page: pg})
	if err != nil {
		return nil, fmt.Errorf("pdfium: page %d size: %w", n, err)
	}
	// GetPageSize reports the *rendered* extent: the crop box where present,
	// otherwise the media box, with /Rotate already applied. Verified against
	// hand-built /Rotate 0/90/180/270 pages — a 612 × 792 page at /Rotate 90
	// reports 792 × 612 — and against a fixture whose crop box is inset from its
	// media box, where it reports the crop box. That is exactly what render.Fit
	// needs, so no box arithmetic happens here.
	box := geom.NewRect(0, 0, sz.Width, sz.Height)

	dpi, w, h, err := render.Fit(box, o)
	if err != nil {
		return nil, fmt.Errorf("pdfium: page %d: %w", n, err)
	}

	// Pixels rather than DPI, though go-pdfium offers both. RenderPageInDPI takes an
	// int, and Fit's reduction under MaxPixels produces a fractional DPI — asking in
	// pixels is what makes the cap exact instead of rounded to the nearest whole DPI.
	// It also keeps one code path for the reduced and unreduced cases.
	//
	// Width and Height are a *maximum* in this API, not an exact size: pdfium fits
	// the page inside the box preserving aspect ratio. Since w and h come from the
	// page's own aspect ratio, the fit is the box, give or take the rounding Fit
	// already did.
	// Annotations need both switches. RenderForm calls FPDF_FFLDraw, which draws form
	// *fields* and nothing else; the appearance streams of every other annotation
	// subtype — a highlight, a stamp, a FreeText note — are drawn only under the
	// FPDF_ANNOT render flag. Setting one without the other honours half of what
	// Options.Annotations promises, and the half it drops is the common one.
	var flags enums.FPDF_RENDER_FLAG
	if o.Annotations {
		flags |= enums.FPDF_RENDER_FLAG_ANNOT
	}
	res, err := r.inst.RenderPageInPixels(&requests.RenderPageInPixels{
		Page:        pg,
		Width:       w,
		Height:      h,
		RenderFlags: flags,
		RenderForm:  o.Annotations,
	})
	if err != nil {
		return nil, fmt.Errorf("pdfium: page %d render: %w", n, err)
	}
	if res.Result.Image == nil {
		return nil, fmt.Errorf("pdfium: page %d rendered no image", n)
	}

	// The bitmap is copied out of the response, and this is not a precaution. The
	// adapter assigns Pix directly from wazero's Memory().Read, which returns a view
	// into WASM linear memory rather than a copy, and Cleanup frees the pdfium-side
	// bitmap that view points at. Returning the image without copying would hand back
	// pixels that the next page is about to overwrite — a defect that presents as one
	// page's content appearing in another's, which is much harder to recognize than
	// the cost of an allocation.
	img := copyRGBA(res.Result.Image)
	res.Cleanup()

	return &render.Raster{
		Number: n,
		Image:  img,
		DPI:    dpi,
		Box:    box,
	}, nil
}

// copyRGBA returns an independent copy of src.
func copyRGBA(src *stdimage.RGBA) *stdimage.RGBA {
	dst := stdimage.NewRGBA(src.Rect)
	// Row by row rather than one copy of Pix: a bitmap whose Stride exceeds its
	// width has padding between rows, and NewRGBA's stride is exactly 4×width, so a
	// flat copy would skew every row after the first.
	w := src.Rect.Dx() * 4
	for y := 0; y < src.Rect.Dy(); y++ {
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+w], src.Pix[y*src.Stride:y*src.Stride+w])
	}
	return dst
}

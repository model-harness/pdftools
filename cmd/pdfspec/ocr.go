package main

import (
	"context"
	"flag"
	"fmt"
	stdimage "image"
	"image/draw"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/geom"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/ocr/docd"
	"github.com/model-harness/pdftools/ocr/doctags"
	"github.com/model-harness/pdftools/ocr/ipc"
	"github.com/model-harness/pdftools/render"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
	"github.com/model-harness/pdftools/sink/markdown"
)

// runOCR converts a PDF to Markdown, sending only the pages that need it through a
// vision model.
//
// The verb is `md` plus a fallback, not a separate pipeline, and that is the design:
// the extractor runs over the whole document first, the router picks the pages whose
// content stream carries nothing, and only those cost a model. A document that is
// entirely born-digital produces exactly what `md` produces and never loads a model at
// all; a document that is entirely scanned goes through the model page by page. The
// common real case — a specification with scanned annexes — is handled without the user
// having to know which pages were which.
func runOCR(args []string) error {
	fs := flag.NewFlagSet("ocr", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: stdout)")
	pages := fs.String("pages", "", "page ranges to consider, e.g. 1,4-9,20- (default: all)")
	threshold := fs.Float64("threshold", ocr.DefaultThreshold, "text coverage below which a page is sent to the model; 0 disables OCR")
	force := fs.Bool("force", false, "send every selected page to the model, ignoring coverage")
	dryRun := fs.Bool("dry-run", false, "report which pages would be sent and exit without loading a model")
	dpi := fs.Float64("dpi", render.DefaultOptions.DPI, "resolution pages are rasterized at before recognition")
	maxTokens := fs.Int("max-tokens", defaultMaxTokens, "token bound per page; 0 means the backend's default")
	addr := fs.String("addr", "", "IPC address of a running model host (default: run one in-process)")
	exe := fs.String("exe", "", "llama-server executable (default: look on PATH)")
	model := fs.String("model", docd.Model, "HuggingFace GGUF repo the host loads")
	ngl := fs.Int("ngl", 0, "model layers to offload to the GPU; 0 is CPU only")
	nctx := fs.Int("ctx", 0, "context window in tokens; 0 means the backend's default")
	frontmatter := fs.Bool("frontmatter", false, "emit YAML frontmatter")
	artifacts := fs.Bool("artifacts", false, "keep running headers, folios, and watermarks")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec ocr [-o out] [-pages 1,4-9] [-threshold f] [-force] [-dry-run] [-addr sock] <file.pdf>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("ocr takes exactly one input file")
	}
	if *threshold < 0 || *threshold > 1 {
		return fmt.Errorf("-threshold %g is outside 0-1: it is a fraction of the page area", *threshold)
	}
	if *maxTokens < 0 {
		return fmt.Errorf("-max-tokens %d is negative", *maxTokens)
	}

	ropt := render.Options{DPI: *dpi, MaxPixels: render.DefaultOptions.MaxPixels}
	// Validated before anything is opened or downloaded. A typo in -dpi that surfaced
	// after a ten-minute first-run model download would be the worst possible time to
	// learn about it.
	if _, _, _, err := render.Fit(letterBox, ropt); err != nil {
		return err
	}

	// Ctrl-C reaches the child. docd starts llama-server with a cancellable context, so
	// cancelling here is what stops a model that is mid-page instead of leaving an
	// orphan holding the GPU and the port.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	in := fs.Arg(0)
	s, err := pcstore.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	eopt := extract.DefaultOptions
	eopt.KeepArtifacts = *artifacts
	d, err := extract.New(s, eopt).Document()
	if err != nil {
		return err
	}
	d.Meta.Path = in

	want, err := parseRanges(*pages, len(d.Pages))
	if err != nil {
		return err
	}
	routed := route(d, want, *threshold, *force)

	mopt := markdown.Options{Frontmatter: *frontmatter, Artifacts: *artifacts}
	if *dryRun {
		return plan(os.Stdout, d, want, routed, *threshold)
	}
	if len(routed) == 0 {
		// Nothing to recognize, so nothing is loaded. Said out loud, because a run that
		// silently produced the same output as `md` would otherwise look like the model
		// was consulted and agreed.
		fmt.Fprintf(os.Stderr, "no pages need recognition: all %d selected page(s) carry text above %.0f%% coverage\n",
			len(want), *threshold*100)
		if !d.Meta.Tagged {
			inferRoles(d)
		}
		return writeWhole(d, *out, mopt)
	}

	eng, closeEngine, err := openEngine(ctx, engineOptions{
		addr: *addr, exe: *exe, model: *model, ngl: *ngl, ctx: *nctx,
	})
	if err != nil {
		return err
	}
	defer closeEngine()

	r, err := renderpdfium.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	if err := recognize(ctx, d, routed, eng, r, ropt, *maxTokens); err != nil {
		return err
	}
	if !d.Meta.Tagged {
		// A partly-scanned document keeps its born-digital pages' extracted text, and
		// those pages get the same inference `md` would give them. The recognized pages
		// are already levelled — doctags reads a heading's rank out of the model's
		// output — and carry no font size for a cluster to be measured from, so they
		// are not reconsidered here.
		inferRoles(d)
	}
	return writeWhole(d, *out, mopt)
}

// defaultMaxTokens bounds one page's generation.
//
// Bounded by default rather than left to the backend, because the failure mode of a
// vision model on a dense page is not silence but a repetition loop that emits the same
// table row until something stops it — on a 200-page document that is the difference
// between an hour and a night. 8192 is comfortably more than a full page of DocTags
// (the densest fixture in testdata/docling is under 3000 tokens) and still short enough
// that a looping page gives up in seconds rather than minutes.
const defaultMaxTokens = 8192

// route returns the selected pages that need recognition, in page order.
func route(d *doc.Document, want []int, threshold float64, force bool) []int {
	var routed []int
	for _, n := range want {
		p := d.Pages[n-1]
		// -force still respects Rasterized, which ocr.Route checks: a page that already
		// came from a model is not re-read, because asking the model to convert its own
		// output is not a stronger reading of the page.
		if force && !p.Rasterized {
			routed = append(routed, n)
			continue
		}
		if ocr.Route(p, threshold) {
			routed = append(routed, n)
		}
	}
	return routed
}

// plan reports the routing decision without loading a model.
//
// This exists because the model is the expensive part and the decision is free. "Which
// pages of this 1,000-page file need OCR" is the first question anyone asks, and
// answering it should not cost a 500 MB download.
func plan(w io.Writer, d *doc.Document, want, routed []int, threshold float64) error {
	set := make(map[int]bool, len(routed))
	for _, n := range routed {
		set[n] = true
	}
	for _, n := range want {
		p := d.Pages[n-1]
		mark := "keep"
		if set[n] {
			mark = "ocr "
		}
		if _, err := fmt.Fprintf(w, "%s page %d\tcoverage %5.1f%%\t%d block(s)\n",
			mark, n, p.Coverage()*100, len(p.Blocks)); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "%d of %d selected page(s) below %.0f%% coverage\n", len(routed), len(want), threshold*100)
	return nil
}

// recognize sends each routed page through the engine and replaces its blocks.
//
// Serial, and deliberately without a -jobs flag. The bottleneck is a single model: the
// host runs one slot, the wire allows one in-flight request per connection, and
// ipc.Local serializes for the same reason — so page-level fan-out would add queueing
// and interleaved progress output without adding throughput. Rasterization, which is
// milliseconds against tens of seconds of generation, is not worth parallelizing on its
// own.
//
// A page that fails keeps whatever the extractor found for it, rather than being
// emptied. That is the conservative direction: the extracted text may be sparse, but it
// is what the file actually says, and discarding it in favour of nothing would make a
// failed model call look like a blank page.
func recognize(ctx context.Context, d *doc.Document, routed []int, eng ocr.Engine, r render.Rasterizer, ropt render.Options, maxTokens int) error {
	start := time.Now()
	var failed []int

	for i, n := range routed {
		if err := ctx.Err(); err != nil {
			return err
		}
		began := time.Now()
		p, err := recognizePage(ctx, n, eng, r, ropt, maxTokens, d.Pages[n-1])
		if err != nil {
			// Reported per page as it happens rather than collected for the end: a run
			// where every page fails should say so on the first one, not after an hour.
			fmt.Fprintf(os.Stderr, "  page %d: %v\n", n, err)
			failed = append(failed, n)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		d.Pages[n-1] = p
		fmt.Fprintf(os.Stderr, "page %d (%d of %d): %d block(s) in %v\n",
			n, i+1, len(routed), len(p.Blocks), time.Since(began).Round(time.Millisecond))
	}

	fmt.Fprintf(os.Stderr, "recognized %d of %d page(s) in %v\n",
		len(routed)-len(failed), len(routed), time.Since(start).Round(time.Second))
	if len(failed) > 0 {
		// A partial document is still written — the caller has the output and the count
		// of what is missing from it — but the exit status reflects the failure, because
		// a silently incomplete conversion is the one outcome worth failing over.
		return fmt.Errorf("%d of %d page(s) failed recognition and kept their extracted text", len(failed), len(routed))
	}
	return nil
}

// recognizePage rasterizes one page, recognizes it, and parses the result.
func recognizePage(ctx context.Context, n int, eng ocr.Engine, r render.Rasterizer, ropt render.Options, maxTokens int, orig doc.Page) (doc.Page, error) {
	ra, err := r.Page(n, ropt)
	if err != nil {
		return doc.Page{}, err
	}

	src, err := eng.Recognize(ctx, rgba(ra.Image), ocr.Options{MaxTokens: maxTokens})
	// Parsed even when generation failed. A page truncated by its token bound is the
	// dominant failure on a dense table, and ocr/doctags reads a truncated document by
	// design — so the error is reported only when there is nothing to salvage.
	if src == "" {
		if err != nil {
			return doc.Page{}, err
		}
		return doc.Page{}, fmt.Errorf("the model returned no output")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "  page %d: %v (keeping what was generated)\n", n, err)
	}

	p, perr := doctags.ParsePage(src, n, ocrBox(orig, ra))
	if perr != nil {
		return doc.Page{}, perr
	}
	if len(p.Blocks) == 0 {
		// A page the model read as empty is not obviously wrong — a blank scan is a real
		// thing — but replacing extracted text with nothing is, so it is a failure for
		// this page and the extractor's version survives.
		return doc.Page{}, fmt.Errorf("the model produced no blocks from %d characters of output", len(src))
	}
	return p, nil
}

// ocrBox is the rectangle DocTags coordinates are resolved against.
//
// Origin from the extractor's page box, extent from the raster. The two differ on
// purpose: doc.Page.Box carries the unrotated crop box while render.Raster.Box has
// /Rotate applied, and the model saw the rotated pixels. Taking the extent from the
// raster is therefore what makes a rotated page's boxes describe the page as read
// rather than as stored; taking the origin from the crop box is what keeps them in the
// same coordinate space as the blocks on every neighbouring page.
//
// The consequence, for a rotated page only, is that the recognized blocks are in the
// rotated frame. The recognized page's Rotate is zero — doctags.ParsePage does not set
// it, and recognize replaces the whole page rather than merging into it — which says so
// exactly: a sink that applied the original /Rotate to these boxes would place them
// twice.
func ocrBox(orig doc.Page, ra *render.Raster) geom.Rect {
	return geom.NewRect(orig.Box.X0, orig.Box.Y0,
		orig.Box.X0+ra.Box.Width(), orig.Box.Y0+ra.Box.Height())
}

// rgba returns img as *image.RGBA, converting only when it is not one already.
//
// render/pdfium always returns RGBA, so the conversion is the cost of the interface
// rather than of this path: render.Rasterizer promises image.Image, and a future
// adapter — a native rasterizer, or one that renders grayscale for a scan — would
// otherwise fail here at runtime rather than convert.
func rgba(img stdimage.Image) *stdimage.RGBA {
	if r, ok := img.(*stdimage.RGBA); ok {
		return r
	}
	out := stdimage.NewRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// engineOptions is the wiring choice: an address, or a host to start.
type engineOptions struct {
	addr  string
	exe   string
	model string
	ngl   int
	ctx   int
}

// openEngine returns an ocr.Engine and the function that releases it.
//
// Two paths behind one interface. With -addr the verb talks to a host someone else is
// running — inferd once it carries docling, or a docd on the machine with the GPU — and
// starts nothing. Without it, it starts llama-server itself and reaches it in-process,
// which is the right default for the common case of one CLI invocation over one
// document: the model is loaded and discarded by the same run either way, so a socket
// between two halves of one process would only add framing.
//
// Neither side can tell which one it is. That is the whole point of the wire in ocr/ipc
// being byte-compatible with inferd's, and it is why this function is the only place in
// the verb that knows a backend exists.
func openEngine(ctx context.Context, o engineOptions) (ocr.Engine, func(), error) {
	if o.addr != "" {
		eng, err := ipc.Dial(ctx, o.addr)
		if err != nil {
			return nil, nil, err
		}
		return eng, func() { _ = eng.Close() }, nil
	}

	host, err := docd.Start(ctx, docd.Options{
		Model:     o.model,
		Exe:       o.exe,
		GPULayers: o.ngl,
		Ctx:       o.ctx,
	})
	if err != nil {
		return nil, nil, err
	}
	eng := ipc.NewLocal(host)
	// The host's Close, not the engine's: ipc.Local deliberately does not stop the model
	// it was handed, because the caller owns that lifetime. Here the caller is this
	// function, and forgetting it would leave llama-server running after the CLI exits.
	return eng, func() { _ = host.Close() }, nil
}

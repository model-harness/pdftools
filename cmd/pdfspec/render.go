package main

import (
	"flag"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/render"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
)

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	out := fs.String("o", "", "output directory (required)")
	dpi := fs.Float64("dpi", render.DefaultOptions.DPI, "resolution in dots per inch")
	pages := fs.String("pages", "", "page ranges, e.g. 1,4-9,20- (default: all)")
	format := fs.String("format", "png", "output format: png or jpeg")
	quality := fs.Int("quality", 90, "JPEG quality, 1-100")
	jobs := fs.Int("jobs", 0, "pages to render in parallel (default: min(4, NumCPU))")
	annots := fs.Bool("annots", false, "render annotations and form fields")
	maxPx := fs.Int("maxpixels", render.DefaultOptions.MaxPixels, "reduce DPI to keep a page under this many pixels")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec render -o dir [-dpi n] [-pages 1,4-9] [-format png|jpeg] [-jobs n] <file.pdf>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("render takes exactly one input file")
	}
	if *out == "" {
		return fmt.Errorf("-o <dir> is required")
	}
	enc, ext, err := encoder(*format, *quality)
	if err != nil {
		return err
	}

	opt := render.Options{DPI: *dpi, MaxPixels: *maxPx, Annotations: *annots}
	// Validated here rather than at the first page, so a typo in -dpi fails before
	// the WASM module is compiled and a directory is created.
	if _, _, _, err := render.Fit(letterBox, opt); err != nil {
		return err
	}

	in := fs.Arg(0)
	r, err := renderpdfium.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	want, err := parseRanges(*pages, r.PageCount())
	if err != nil {
		return err
	}
	if len(want) == 0 {
		return fmt.Errorf("no pages selected from a %d-page document", r.PageCount())
	}

	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	return renderPages(in, r, want, opt, *out, ext, enc, *jobs)
}

// letterBox is US Letter in points, used only to validate flags before opening
// anything. Any non-degenerate box answers the question "is this DPI usable".
var letterBox = geom.NewRect(0, 0, 612, 792)

// encodeFunc writes one rendered page.
type encodeFunc func(io.Writer, *render.Raster) error

func encoder(format string, quality int) (encodeFunc, string, error) {
	switch strings.ToLower(format) {
	case "png":
		// PNG is the default because a rendered page is either going to a reader or
		// to a vision model, and both are better served by exact pixels than by a
		// smaller file. It is also lossless, which matters for the OCR path: JPEG
		// ringing around small type is precisely the artifact that costs characters.
		return func(w io.Writer, r *render.Raster) error {
			return png.Encode(w, r.Image)
		}, "png", nil
	case "jpeg", "jpg":
		if quality < 1 || quality > 100 {
			return nil, "", fmt.Errorf("-quality %d is outside 1-100", quality)
		}
		return func(w io.Writer, r *render.Raster) error {
			return jpeg.Encode(w, r.Image, &jpeg.Options{Quality: quality})
		}, "jpg", nil
	}
	return nil, "", fmt.Errorf("-format %q is not png or jpeg", format)
}

// renderPages renders the selected pages into dir.
//
// One page that fails does not fail the run, for the same reason it does not in the
// images verb: a 151-page scan with one broken page should still yield 150 images.
// The count of what failed is reported and the exit status reflects it, because a
// silent partial render is indistinguishable from a complete one.
func renderPages(in string, r render.Rasterizer, want []int, opt render.Options, dir, ext string, enc encodeFunc, jobs int) error {
	start := time.Now()
	width := len(fmt.Sprint(r.PageCount()))

	// A Rasterizer is single-threaded by contract, so parallelism is more of them
	// over the same file. Each costs a WASM instance and a parse, so the default is
	// deliberately low: measured throughput on a 285-page manual was 7.6 ms/page at
	// 1 worker, 4.3 at 2, 3.4 at 4, and no better at 8, because page rendering is
	// memory-bandwidth bound rather than CPU bound.
	if jobs <= 0 {
		jobs = min(4, runtime.NumCPU())
	}
	if jobs > len(want) {
		jobs = len(want)
	}

	type result struct {
		page    int
		err     error
		reduced bool
	}
	results := make([]result, len(want))

	var wg sync.WaitGroup
	for j := range jobs {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			// Worker 0 uses the Rasterizer already open, so a single-job run does not
			// pay for a second parse of the file.
			ras := r
			if j > 0 {
				var err error
				ras, err = renderpdfium.Open(in)
				if err != nil {
					// One worker failing to start is not fatal: the others cover its
					// pages more slowly. Recorded against its first page so the run
					// does not report a silent gap.
					for i := j; i < len(want); i += jobs {
						results[i] = result{page: want[i], err: err}
					}
					return
				}
				defer func() { _ = ras.Close() }()
			}
			for i := j; i < len(want); i += jobs {
				n := want[i]
				ra, err := ras.Page(n, opt)
				if err != nil {
					results[i] = result{page: n, err: err}
					continue
				}
				path := filepath.Join(dir, fmt.Sprintf("page-%0*d.%s", width, n, ext))
				err = writeFile(path, func(w io.Writer) error { return enc(w, ra) })
				if err != nil {
					// A partial image looks like a successful render and fails only
					// when something opens it.
					_ = os.Remove(path)
				}
				results[i] = result{page: n, err: err, reduced: ra.Reduced(opt)}
			}
		}(j)
	}
	wg.Wait()

	wrote, reduced := 0, 0
	var failed []result
	for _, res := range results {
		switch {
		case res.err != nil:
			failed = append(failed, res)
		default:
			wrote++
			if res.reduced {
				reduced++
			}
		}
	}

	fmt.Fprintf(os.Stderr, "wrote %d of %d pages to %s at %g dpi in %v\n",
		wrote, len(want), dir, opt.DPI, time.Since(start).Round(time.Millisecond))
	if reduced > 0 {
		// Said out loud. A page silently rendered coarser than asked is the kind of
		// degradation that surfaces later as "OCR is worse on the big pages".
		fmt.Fprintf(os.Stderr, "%d page(s) exceeded -maxpixels %d and were rendered at a lower dpi\n",
			reduced, opt.MaxPixels)
	}
	if len(failed) > 0 {
		// Every failure named, up to a limit. On a document where most pages fail
		// the list is the diagnosis, and truncating it entirely would hide it.
		const show = 5
		for i, res := range failed {
			if i == show {
				fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(failed)-show)
				break
			}
			fmt.Fprintf(os.Stderr, "  page %d: %v\n", res.page, res.err)
		}
		return fmt.Errorf("%d of %d page(s) failed", len(failed), len(want))
	}
	return nil
}

// parseRanges turns "1,4-9,20-" into a sorted, deduplicated page list.
//
// Written here rather than taken from a flag library because the syntax is the one
// every PDF tool uses and users expect it: a bare number, a closed range, and an
// open range meaning "to the end". An empty spec means every page.
//
// Out-of-range numbers are an error rather than silently clamped. "-pages 500" on a
// 3-page file is a mistake, and rendering nothing while reporting success is how a
// user concludes the tool is broken.
func parseRanges(spec string, count int) ([]int, error) {
	if count <= 0 {
		return nil, fmt.Errorf("document has no pages")
	}
	if strings.TrimSpace(spec) == "" {
		all := make([]int, count)
		for i := range all {
			all[i] = i + 1
		}
		return all, nil
	}

	seen := make(map[int]bool, count)
	var out []int
	add := func(n int) error {
		if n < 1 || n > count {
			return fmt.Errorf("page %d is outside 1-%d", n, count)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
		return nil
	}

	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			n, err := atoiPage(part)
			if err != nil {
				return nil, err
			}
			if err := add(n); err != nil {
				return nil, err
			}
			continue
		}
		from, err := atoiPage(strings.TrimSpace(lo))
		if err != nil {
			return nil, err
		}
		to := count
		if h := strings.TrimSpace(hi); h != "" {
			if to, err = atoiPage(h); err != nil {
				return nil, err
			}
		}
		if to < from {
			return nil, fmt.Errorf("range %q counts backwards", part)
		}
		for n := from; n <= to; n++ {
			if err := add(n); err != nil {
				return nil, err
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no pages in %q", spec)
	}
	// Sorted so output order matches page order regardless of how the spec was
	// written, which is what makes "-pages 9,1" produce the same files as "1,9".
	sortInts(out)
	return out, nil
}

func atoiPage(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty page number")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is not a page number", s)
		}
		n = n*10 + int(c-'0')
		// Bounded rather than allowed to overflow: "-pages 99999999999999999999"
		// is a typo, not a request, and an overflowed int is a negative page.
		if n > 1<<30 {
			return 0, fmt.Errorf("page number %q is too large", s)
		}
	}
	return n, nil
}

func sortInts(a []int) {
	// Insertion sort. A page list is short and already nearly sorted in every real
	// spec, and pulling in a generic sort for it would cost more to read than this.
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/geom"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/ocr/tesseract"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/sink/markdown"
)

// runOCR converts a PDF to Markdown, reading the pages that need it with Tesseract.
//
// The verb is md with the router's knobs: md already reads the pages Route picks when
// Tesseract is installed (ADR 0043), and this verb adds -force, -threshold and -dry-run.
// A document that is entirely born-digital produces exactly what md produces and never runs
// an engine at all.
func runOCR(args []string) error {
	fs := flag.NewFlagSet("ocr", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: stdout)")
	pages := fs.String("pages", "", "page ranges to consider, e.g. 1,4-9,20- (default: all)")
	threshold := fs.Float64("threshold", ocr.DefaultThreshold, "text coverage below which a page is read by OCR; 0 disables OCR")
	force := fs.Bool("force", false, "read every selected page by OCR, ignoring coverage")
	dryRun := fs.Bool("dry-run", false, "report which pages would be read and exit without running an engine")
	backend := fs.String("backend", "pdfium", "rasterizer: "+backendNames)
	frontmatter := fs.Bool("frontmatter", false, "emit YAML frontmatter")
	artifacts := fs.Bool("artifacts", false, "keep running headers, folios, and watermarks")
	var eo engineFlags
	eo.register(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec ocr [-o out] [-pages 1,4-9] [-threshold f] [-force] [-dry-run] [-tesseract exe] <file.pdf>\n\n")
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
	if err := eo.validate(); err != nil {
		return err
	}

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
	ex := extract.New(s, eopt)
	d, err := ex.Document()
	if err != nil {
		return err
	}
	d.Meta.Path = in
	warnIfPagesFailed(ex.Failed(), len(d.Pages))

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
		fmt.Fprintf(os.Stderr, "no pages need recognition: all %d selected page(s) carry text above %.0f%% coverage\n",
			len(want), *threshold*100)
	} else if err := readPages(ctx, d, in, routed, eo, *backend, *force); err != nil {
		return err
	}
	if !d.Meta.Tagged {
		inferRoles(d)
	}
	return writeWhole(d, *out, mopt)
}

// engineFlags are the Tesseract settings md and ocr share.
type engineFlags struct {
	exe, tessdata, lang string
	dpi, minConf        float64
}

func (e *engineFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&e.exe, "tesseract", "", "tesseract executable (default: look on PATH)")
	fs.StringVar(&e.tessdata, "tessdata", "", "directory of Tesseract language models (default: TESSDATA_PREFIX or the install's)")
	fs.StringVar(&e.lang, "lang", "eng", "Tesseract language model")
	fs.Float64Var(&e.dpi, "dpi", ocrDPI, "resolution pages are rasterized at before OCR")
	fs.Float64Var(&e.minConf, "min-conf", defaultMinConf, "Tesseract confidence (0-100) below which a word is written [err]")
}

func (e *engineFlags) validate() error {
	if e.minConf < 0 || e.minConf > 100 {
		return fmt.Errorf("-min-conf %g is outside 0-100", e.minConf)
	}
	// Validated before anything is opened, so a typo in -dpi surfaces at once.
	_, _, _, err := render.Fit(letterBox, render.Options{DPI: e.dpi, MaxPixels: render.DefaultOptions.MaxPixels})
	return err
}

// ocrDPI is the resolution pages are read at, the rasterizer's default. Tesseract read the
// spike's seven born-digital pages within 0.3 points of CER at 200 dpi and at 300, and 300 is
// 2.25 times the pixels (ADR 0043).
const ocrDPI = 200

// defaultMinConf is the confidence below which a word is [err] (ADR 0043).
const defaultMinConf = 30

// route returns the selected pages that need recognition, in page order.
func route(d *doc.Document, want []int, threshold float64, force bool) []int {
	var routed []int
	for _, n := range want {
		p := d.Pages[n-1]
		// -force still respects Rasterized, which ocr.Route checks: a page that already came
		// from OCR is not re-read.
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

// plan reports the routing decision without running an engine.
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
			mark, n, ocr.Coverage(p)*100, len(p.Blocks)); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "%d of %d selected page(s) below %.0f%% coverage\n", len(routed), len(want), threshold*100)
	return nil
}

// autoOCR is md's step after extraction: the pages Route picks are read with Tesseract when it
// is installed, which is the order the converter works in, the file's own text first (ADR 0043).
// A page OCR fails on keeps its extracted text, and md goes on with a warning: the conversion
// is the file's, and OCR only adds to it.
func autoOCR(d *doc.Document, path string, eo engineFlags, backend string) {
	routed := route(d, allPages(len(d.Pages)), ocr.DefaultThreshold, false)
	if len(routed) == 0 {
		return
	}
	if err := readPages(context.Background(), d, path, routed, eo, backend, false); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
}

// allPages is 1..n.
func allPages(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// tesseractEnv names the tesseract executable when no flag does, or "off" to run none. Tests
// set it to "off", so what is installed on a machine cannot change what they measure.
const tesseractEnv = "PDFSPEC_TESSERACT"

// readPages reads the routed pages with Tesseract and replaces their blocks.
//
// Without Tesseract the run still succeeds: the pages keep what the extractor found, and
// stderr says which ones they are, since a scan written as nothing would otherwise read as a
// blank page.
//
// A page that fails keeps its extracted text too, rather than being emptied: that text may
// be sparse, but it is what the file says.
func readPages(ctx context.Context, d *doc.Document, path string, routed []int, eo engineFlags, backend string, force bool) error {
	exe := eo.exe
	if exe == "" {
		exe = os.Getenv(tesseractEnv)
	}
	if exe == "off" {
		return nil
	}
	eng, err := tesseract.New(ctx, tesseract.Options{Exe: exe, TessData: eo.tessdata, Lang: eo.lang, DPI: eo.dpi})
	if errors.Is(err, tesseract.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "warning: %d page(s) need OCR and %v; they keep their extracted text: %s\n",
			len(routed), err, pageList(routed))
		return nil
	}
	if err != nil {
		return err
	}
	r, err := openRasterizer(backend, path)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	return readWith(ctx, d, routed, eng, r, render.Options{DPI: eo.dpi, MaxPixels: render.DefaultOptions.MaxPixels}, eo.minConf, force,
		withArtifacts(path))
}

// withArtifacts returns the function that extracts page n with its artifacts kept, which is
// what OCR's reading is compared against: OCR reads a running header and a watermark as text,
// and compared with a page they were stripped from, a page blank but for its furniture reads as
// all new. ISO/TS 32001's blank pages gave back the licence watermark naming its buyer.
func withArtifacts(path string) func(n int) doc.Page {
	return func(n int) doc.Page {
		s, err := pcstore.Open(path)
		if err != nil {
			return doc.Page{}
		}
		defer func() { _ = s.Close() }()
		opt := extract.DefaultOptions
		opt.KeepArtifacts = true
		p, err := extract.New(s, opt).Page(n)
		if err != nil {
			return doc.Page{}
		}
		return p
	}
}

// readWith reads the routed pages with eng, rasterized by r. A reading replaces a page only
// where ocr.Adds says it adds to what the extractor found, or under -force.
func readWith(ctx context.Context, d *doc.Document, routed []int, eng ocr.Engine, r render.Rasterizer, ropt render.Options, minConf float64, force bool, full func(int) doc.Page) error {
	start := time.Now()
	var failed, kept []int
	var unsure []pageErrs
	for _, n := range routed {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, err := readPage(ctx, n, eng, r, ropt, minConf, d.Pages[n-1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "  page %d: %v\n", n, err)
			failed = append(failed, n)
			continue
		}
		if !force && !ocr.Adds(full(n), p) {
			kept = append(kept, n)
			continue
		}
		d.Pages[n-1] = p
		if k := strings.Count(p.Text(), doc.Unmapped); k > 0 {
			unsure = append(unsure, pageErrs{Page: n, Glyphs: k})
		}
	}
	fmt.Fprintf(os.Stderr, "read %d of %d page(s) with %s in %v\n",
		len(routed)-len(failed), len(routed), eng.Name(), time.Since(start).Round(time.Millisecond))
	if len(kept) > 0 {
		fmt.Fprintf(os.Stderr, "  kept the extracted text of %d page(s), which OCR did not add to: %s\n", len(kept), pageList(kept))
	}
	if len(unsure) > 0 {
		fmt.Fprintf(os.Stderr, "  %d word(s) read below confidence %g are written [err]: %s\n", errTotal(unsure), minConf, errPages(unsure))
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d page(s) failed OCR and kept their extracted text", len(failed), len(routed))
	}
	return nil
}

// readPage rasterizes one page and builds it from the words Tesseract reads.
func readPage(ctx context.Context, n int, eng ocr.Engine, r render.Rasterizer, ropt render.Options, minConf float64, orig doc.Page) (doc.Page, error) {
	ra, err := r.Page(n, ropt)
	if err != nil {
		return doc.Page{}, err
	}
	words, err := eng.Read(ctx, ra.Image)
	if err != nil {
		return doc.Page{}, err
	}
	if !ocr.IsText(words) {
		// A blank page, or a picture read as noise: nothing read is text, and the page keeps
		// what the extractor found.
		words = nil
	}
	return ocr.Page(words, n, ocrBox(orig, ra), ra.Image.Bounds(), minConf), nil
}

// ocrBox is the rectangle the image's pixels are resolved against.
//
// Origin from the extractor's page box, extent from the raster. The two differ on purpose:
// doc.Page.Box carries the unrotated crop box while render.Raster.Box has /Rotate applied,
// and the engine saw the rotated pixels. Taking the extent from the raster is what makes a
// rotated page's boxes describe the page as read; taking the origin from the crop box keeps
// them in the same space as every neighbouring page's. The page built is in the rotated
// frame, and its Rotate is zero, which says so.
func ocrBox(orig doc.Page, ra *render.Raster) geom.Rect {
	return geom.NewRect(orig.Box.X0, orig.Box.Y0,
		orig.Box.X0+ra.Box.Width(), orig.Box.Y0+ra.Box.Height())
}

// pageList writes ascending page numbers as "3, 7-12, 20": a blank scan of 151 pages is one
// range, not 151 numbers.
func pageList(ns []int) string {
	var parts []string
	for i := 0; i < len(ns); {
		j := i
		for j+1 < len(ns) && ns[j+1] == ns[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, strconv.Itoa(ns[i])+"-"+strconv.Itoa(ns[j]))
		} else {
			parts = append(parts, strconv.Itoa(ns[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

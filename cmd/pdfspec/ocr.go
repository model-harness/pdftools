package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/pdfspec"
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
	backend := fs.String("backend", "pdfium", "rasterizer: "+pdfspec.BackendNames)
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

	opt := eo.options()
	opt.KeepArtifacts = *artifacts
	opt.Frontmatter = *frontmatter
	opt.Threshold = *threshold
	opt.Force = *force
	opt.Strict = true
	opt.Backend = *backend
	d, err := pdfspec.Open(fs.Arg(0), opt)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	want, err := parseRanges(*pages, len(d.Doc.Pages))
	if err != nil {
		return err
	}
	routed := pdfspec.Route(d.Doc, want, *threshold, *force)

	if *dryRun {
		return plan(os.Stdout, d.Doc, want, routed, *threshold)
	}
	if len(routed) == 0 {
		fmt.Fprintf(os.Stderr, "no pages need recognition: all %d selected page(s) carry text above %.0f%% coverage\n",
			len(want), *threshold*100)
	}
	d.Options.Pages = want
	if err := d.Read(ctx); err != nil {
		return err
	}
	if !d.Doc.Meta.Tagged {
		pdfspec.InferRoles(d.Doc)
	}
	return writeWhole(d.Doc, *out, markdown.Options{Frontmatter: *frontmatter, Artifacts: *artifacts})
}

// engineFlags are the Tesseract settings md and ocr share.
type engineFlags struct {
	exe, tessdata, lang, inferd string
	dpi, minConf                float64
}

func (e *engineFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&e.exe, "tesseract", "", "tesseract executable, or off (default: PDFSPEC_TESSERACT, else the one pdfspec ships, else PATH)")
	fs.StringVar(&e.tessdata, "tessdata", "", "directory of Tesseract language models (default: the shipped Tesseract's own, else TESSDATA_PREFIX or the install's)")
	fs.StringVar(&e.lang, "lang", "eng", "Tesseract language model")
	fs.Float64Var(&e.dpi, "dpi", pdfspec.DefaultOptions.DPI, "resolution pages are rasterized at before OCR")
	fs.Float64Var(&e.minConf, "min-conf", pdfspec.DefaultOptions.MinConf, "without a second reader, the Tesseract confidence (0-100) below which a word is written [err]")
	fs.StringVar(&e.inferd, "inferd", "", "inferd's generation endpoint for the second reader, or off (default: PDFSPEC_INFERD, else the platform's)")
}

func (e *engineFlags) validate() error {
	if e.minConf < 0 || e.minConf > 100 {
		return fmt.Errorf("-min-conf %g is outside 0-100", e.minConf)
	}
	// Validated before anything is opened, so a typo in -dpi surfaces at once.
	_, _, _, err := render.Fit(letterBox, render.Options{DPI: e.dpi, MaxPixels: render.DefaultOptions.MaxPixels})
	return err
}

// defaultEngines are the flags' defaults, for a conversion that takes no flags.
func defaultEngines() engineFlags {
	o := pdfspec.DefaultOptions
	return engineFlags{lang: o.Lang, dpi: o.DPI, minConf: o.MinConf}
}

// options are the library's options for these flags, the environment standing in for a flag
// not given: PDFSPEC_TESSERACT for -tesseract and PDFSPEC_INFERD for -inferd, where "auto" is
// the platform's address. Messages go to stderr.
func (e engineFlags) options() pdfspec.Options {
	o := pdfspec.DefaultOptions
	o.Tesseract, o.Inferd = envEngines(e.exe, e.inferd)
	o.TessData, o.Lang, o.DPI, o.MinConf = e.tessdata, e.lang, e.dpi, e.minConf
	o.InferdTimeout = inferdTimeout(os.Getenv(inferdTimeoutEnv))
	// With neither a flag nor the environment naming one, the Tesseract a release ships comes
	// before any on PATH: it is the build the gold set is measured with. Its model is its own,
	// whatever TESSDATA_PREFIX says, since that names the model of some other install.
	if o.Tesseract == "" {
		if exe, data := bundledTesseract(selfDir()); exe != "" {
			o.Tesseract = exe
			if o.TessData == "" {
				o.TessData = data
			}
		}
	}
	o.Log = os.Stderr
	return o
}

// bundledTesseract is the Tesseract a release ships with the command in dir, and its model
// directory, or "" for both when there is none. The archive unpacks it to dir/tesseract, and
// install.sh puts it in dir/../share/pdfspec/tesseract, beside ~/.local/bin rather than in it.
func bundledTesseract(dir string) (exe, tessdata string) {
	if dir == "" {
		return "", ""
	}
	name := "tesseract"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, root := range []string{
		filepath.Join(dir, "tesseract"),
		filepath.Join(dir, "..", "share", "pdfspec", "tesseract"),
	} {
		root = filepath.Clean(root)
		if fi, err := os.Stat(filepath.Join(root, name)); err == nil && fi.Mode().IsRegular() {
			return filepath.Join(root, name), filepath.Join(root, "tessdata")
		}
	}
	return "", ""
}

// selfDir is the directory the running command is in, through any symlink to it, or "".
func selfDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

// envEngines are the tesseract executable and inferd endpoint the flags exe and addr name, or
// the environment when they are empty.
func envEngines(exe, addr string) (string, string) {
	if exe == "" {
		exe = os.Getenv(tesseractEnv)
	}
	if addr == "" {
		addr = os.Getenv(inferdEnv)
	}
	if addr == "auto" {
		addr = ""
	}
	return exe, addr
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

// tesseractEnv names the tesseract executable when no flag does, or "off" to run none. Tests
// set it to "off", so what is installed on a machine cannot change what they measure.
const tesseractEnv = "PDFSPEC_TESSERACT"

// inferdEnv is "off" to ask no model, which tests set, or names inferd's generation endpoint.
const inferdEnv = "PDFSPEC_INFERD"

// inferdTimeoutEnv bounds each request to the model: a Go duration ("90s", "5m") or bare
// seconds, and "0" for none, for an operator debugging a slow model.
const inferdTimeoutEnv = "PDFSPEC_INFERD_TIMEOUT"

// inferdTimeout is Options.InferdTimeout for the value of inferdTimeoutEnv: zero, the library's
// default, when it is empty or unreadable, and negative, no bound, for "0".
func inferdTimeout(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	d, err := time.ParseDuration(raw)
	if err != nil {
		n, nerr := strconv.Atoi(raw)
		if nerr != nil {
			return 0
		}
		d = time.Duration(n) * time.Second
	}
	switch {
	case d < 0:
		return 0
	case d == 0:
		return -1
	}
	return d
}

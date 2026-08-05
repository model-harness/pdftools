package main

import (
	"context"
	"errors"
	stdimage "image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/geom"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/render"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
)

// These tests run the ocr verb's whole pipeline — extract, route, rasterize, recognize,
// parse, merge, write — with a fake Engine in place of the model. That substitution is
// the reason the pipeline is testable at all: everything except the generation is
// deterministic, and the generation is behind an interface precisely so that a test can
// state what the model said and assert on what the document became. There is no
// llama-server on CI and there should not need to be one.

// fakeEngine returns fixed DocTags for every page, and records the images it was given.
type fakeEngine struct {
	dt     string
	err    error
	calls  int
	widths []int
}

func (f *fakeEngine) Recognize(_ context.Context, img *stdimage.RGBA, opt ocr.Options) (string, error) {
	f.calls++
	f.widths = append(f.widths, img.Bounds().Dx())
	// The verb must always pass a bound, since an unbounded generation on a dense page
	// is the repetition-loop failure defaultMaxTokens exists for.
	if opt.MaxTokens <= 0 {
		return "", errNoBound
	}
	return f.dt, f.err
}

func (f *fakeEngine) Close() error { return nil }

var errNoBound = errors.New("the verb passed no token bound")

// The substitution only works if the fake is what the verb accepts.
var _ ocr.Engine = (*fakeEngine)(nil)

// oneParagraph is the smallest DocTags a model could return for a page: one text block
// with a location, in the top-left eighth of the page.
const oneParagraph = `<doctag><text><loc_10><loc_20><loc_240><loc_60>Recognized body text.</text></doctag>`

// extractOnly runs the extractor the way the verb does, so a test can compare the
// document before recognition with the one after.
func extractOnly(t *testing.T, path string) *doc.Document {
	t.Helper()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	d, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return d
}

func allPages(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// digitalFixture is a page of real body text, and the choice took a measurement.
//
// pymupdf-utilities/test.pdf is the obvious candidate and is wrong: it is 711 bytes of
// one line on a full page, which measures 0.3% coverage and routes. That is not a defect
// — it is precisely the "a scan with a stamped header" case the coverage rule exists to
// catch, and a page holding one line genuinely is a page with essentially no text. The
// router cannot distinguish it from a scan, because nothing in the file does.
//
// So the born-digital witness has to be a document that actually looks like one. Measured
// across the fixtures: this arXiv paper covers 72.9%, adobe-samples/extractPdfInput.pdf
// 80.6% over 3 pages, and pymupdf/mupdf_explored.pdf has 5 of its 285 pages below the
// threshold — chapter dividers, correctly.
const digitalFixture = "pymupdf/2201.00069.pdf"

// TestOCRRoutesScannedNotDigital is the decision the verb exists to make, on two real
// files that differ in exactly the way that matters: one scanned page with no text layer
// and one page of ordinary prose. A verb that routed both would burn a GPU-second to
// produce a worse answer than the extractor already had; one that routed neither would
// never do anything at all.
func TestOCRRoutesScannedNotDigital(t *testing.T) {
	scanned := extractOnly(t, fixture(t, "pymupdf-utilities/scanned.pdf"))
	digital := extractOnly(t, fixture(t, digitalFixture))

	if got := route(scanned, allPages(len(scanned.Pages)), ocr.DefaultThreshold, false); len(got) != len(scanned.Pages) {
		t.Errorf("routed %v of %d scanned page(s); a page with no text layer is the whole point",
			got, len(scanned.Pages))
	}
	if got := route(digital, allPages(len(digital.Pages)), ocr.DefaultThreshold, false); len(got) != 0 {
		t.Errorf("routed %v of a born-digital document: coverage is %.3f",
			got, digital.Pages[0].Coverage())
	}
}

// TestOCRForceRoutesEverything covers the escape hatch. -force exists for a document
// whose text layer is present but wrong — a bad OCR pass someone else ran — where
// coverage says "already done" and the user knows better.
func TestOCRForceRoutesEverything(t *testing.T) {
	d := extractOnly(t, fixture(t, digitalFixture))
	want := allPages(len(d.Pages))

	if got := route(d, want, ocr.DefaultThreshold, true); len(got) != len(want) {
		t.Errorf("-force routed %d of %d pages", len(got), len(want))
	}
	// Except a page that already came from a model. Re-reading generated output is not a
	// second opinion, and ocr.Route's Rasterized check is what -force must not override.
	d.Pages[0].Rasterized = true
	if got := route(d, want, ocr.DefaultThreshold, true); len(got) != 0 {
		t.Errorf("-force routed an already-rasterized page: %v", got)
	}
}

// TestOCRThresholdZeroDisables pins the off switch. A threshold of 0 must route nothing
// at all, including a page with no text whatsoever, so that a user can run the verb over
// a corpus and be certain no model was consulted.
func TestOCRThresholdZeroDisables(t *testing.T) {
	d := extractOnly(t, fixture(t, "pymupdf-utilities/scanned.pdf"))
	if got := route(d, allPages(len(d.Pages)), 0, false); len(got) != 0 {
		t.Errorf("-threshold 0 routed %v; it is the off switch", got)
	}
}

// TestOCRVerbReplacesScannedPage is the end-to-end claim: a scanned page comes out
// holding the model's text, marked as inferred, while the rest of the document is
// untouched.
func TestOCRVerbReplacesScannedPage(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	d := extractOnly(t, in)
	before := d.Pages[0].Text()
	if strings.Contains(before, "Recognized") {
		t.Fatal("test setup: the fixture already contains the fake model's text")
	}

	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	eng := &fakeEngine{dt: oneParagraph}
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	if err := recognize(context.Background(), d, []int{1}, eng, r, ropt, defaultMaxTokens); err != nil {
		t.Fatalf("recognize: %v", err)
	}

	if eng.calls != 1 {
		t.Errorf("engine called %d times for 1 routed page", eng.calls)
	}
	// The model receives the page at the requested resolution, not the page box. A verb
	// that passed the unrendered size would hand the model a 612-pixel image of a page it
	// is trained to read at 1540 on the long edge, and the symptom would be poor
	// recognition rather than an error.
	if len(eng.widths) != 1 || eng.widths[0] < 500 {
		t.Errorf("engine saw image widths %v at 72 dpi over a US Letter page", eng.widths)
	}
	p := d.Pages[0]
	if !strings.Contains(p.Text(), "Recognized body text.") {
		t.Errorf("page 1 text is %q, want the model's output", p.Text())
	}
	// Rasterized is the only record that this page's text was inferred rather than read,
	// and a knowledge bundle a model will later treat as fact depends on it being set.
	if !p.Rasterized {
		t.Error("the recognized page is not marked Rasterized")
	}
	// The recognized boxes must land inside the page, which is the Y-flip working: DocTags
	// counts down from the top and PDF user space counts up from the bottom, so a missing
	// flip mirrors every rectangle and no text comparison would notice.
	if len(p.Blocks) == 0 {
		t.Fatal("no blocks")
	}
	box := p.Blocks[0].Box
	if box.Y1 > p.Box.Y1 || box.Y0 < p.Box.Y0 || box.X1 > p.Box.X1 {
		t.Errorf("block box %+v is outside the page box %+v", box, p.Box)
	}
	// loc_20..loc_60 out of 500 is the top eighth of the page, so in PDF coordinates the
	// block sits in the upper part — above the midpoint. Reversed, it would be below.
	if mid := (p.Box.Y0 + p.Box.Y1) / 2; box.Y0 < mid {
		t.Errorf("block at y %g-%g is below the page midpoint %g: the Y flip is inverted",
			box.Y0, box.Y1, mid)
	}
}

// TestOCRVerbKeepsExtractedTextOnFailure is the conservative direction, and the one worth
// pinning: a page the model failed on keeps whatever the file actually said. Replacing it
// with nothing would make a failed call indistinguishable from a blank page.
func TestOCRVerbKeepsExtractedTextOnFailure(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/ocr-ed.pdf")
	d := extractOnly(t, in)
	before := d.Pages[0].Text()
	if strings.TrimSpace(before) == "" {
		t.Fatal("test setup: the fixture has no text layer to preserve")
	}

	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	// Empty output with an error: the model produced nothing salvageable.
	eng := &fakeEngine{dt: "", err: errors.New("backend unavailable")}
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	err = recognize(context.Background(), d, []int{1}, eng, r, ropt, defaultMaxTokens)
	if err == nil {
		t.Error("recognize reported success after every page failed")
	}
	if got := d.Pages[0].Text(); got != before {
		t.Errorf("a failed page lost its extracted text:\n got  %q\n want %q", got, before)
	}
	if d.Pages[0].Rasterized {
		t.Error("a failed page was marked Rasterized")
	}
}

// TestOCRVerbKeepsPartialGeneration is the other half of that policy. A generation that
// errored *after* emitting DocTags is the dominant failure on a dense table — the token
// bound cuts it mid-grid — and ocr/doctags parses truncated input by design, so the page
// is kept rather than discarded.
func TestOCRVerbKeepsPartialGeneration(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	d := extractOnly(t, in)

	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	// Truncated mid-element, with the error a real Engine returns for a token bound.
	const truncated = `<doctag><text><loc_10><loc_20><loc_240><loc_60>Half a page of `
	eng := &fakeEngine{dt: truncated, err: errors.New("generation hit the token limit")}
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	if err := recognize(context.Background(), d, []int{1}, eng, r, ropt, defaultMaxTokens); err != nil {
		t.Fatalf("a truncated page must still count as recognized: %v", err)
	}
	if !strings.Contains(d.Pages[0].Text(), "Half a page of") {
		t.Errorf("partial generation was discarded: %q", d.Pages[0].Text())
	}
}

// TestOCRVerbRejects pins the flag errors. Each must fail before a model is loaded,
// because the alternative is learning about a typo after a 500 MB download.
func TestOCRVerbRejects(t *testing.T) {
	good := fixture(t, "pymupdf-utilities/test.pdf")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no input", nil},
		{"two inputs", []string{good, good}},
		{"negative threshold", []string{"-threshold", "-1", good}},
		{"threshold above 1", []string{"-threshold", "2", good}},
		{"negative max-tokens", []string{"-max-tokens", "-1", good}},
		{"zero dpi", []string{"-dpi", "0", good}},
		{"page out of range", []string{"-pages", "99", good}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runOCR(tc.args); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestOCRVerbWithoutModelOnDigitalDocument is the case that must never touch a backend:
// a document whose every page carries text. The verb writes the same Markdown `md` would
// and returns without loading anything — which is also why this test can run on a machine
// with no llama-server, and would fail if the ordering were reversed.
func TestOCRVerbWithoutModelOnDigitalDocument(t *testing.T) {
	in := fixture(t, digitalFixture)
	out := filepath.Join(t.TempDir(), "out.md")

	if err := runOCR([]string{"-o", out, in}); err != nil {
		t.Fatalf("runOCR: %v", err)
	}
	got, err := os.ReadFile(out) // #nosec G304 -- a path this test just wrote
	if err != nil {
		t.Fatal(err)
	}
	want := mdOut(t, in)
	if string(got) != want {
		t.Errorf("ocr on a born-digital document differs from md:\n got  %q\n want %q", got, want)
	}
}

// TestOCRDryRunLoadsNoModel checks the free answer to the first question anyone asks —
// which pages of this file need OCR — including on a scanned document, where the
// non-dry-run path would have started a model.
func TestOCRDryRunLoadsNoModel(t *testing.T) {
	var sb strings.Builder
	d := extractOnly(t, fixture(t, "pymupdf-utilities/scanned.pdf"))
	want := allPages(len(d.Pages))
	routed := route(d, want, ocr.DefaultThreshold, false)

	if err := plan(&sb, d, want, routed, ocr.DefaultThreshold); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "ocr  page 1") {
		t.Errorf("plan did not mark the scanned page for OCR:\n%s", sb.String())
	}

	// And through the verb, which must not reach a backend.
	if err := runOCR([]string{"-dry-run", fixture(t, "pymupdf-utilities/scanned.pdf")}); err != nil {
		t.Errorf("runOCR -dry-run: %v", err)
	}
}

// TestOCRBoxUsesRasterExtent pins the coordinate space the parser resolves against.
//
// The two boxes differ on purpose — doc.Page.Box is the unrotated crop box, while
// render.Raster.Box has /Rotate applied — and the model saw the rotated pixels. Taking
// the extent from the raster is what makes a rotated page's blocks describe the page as
// read; taking the origin from the crop box is what keeps them in the same space as
// every neighbouring page's.
func TestOCRBoxUsesRasterExtent(t *testing.T) {
	orig := doc.Page{Number: 1, Box: geom.NewRect(10, 20, 622, 812), Rotate: 90}
	ra := &render.Raster{Box: geom.NewRect(0, 0, 792, 612)} // the rotated extent

	got := ocrBox(orig, ra)
	want := geom.NewRect(10, 20, 802, 632)
	tol := geom.Tolerance{Epsilon: 1e-9}
	if !tol.NearlyEqual(got.X0, want.X0) || !tol.NearlyEqual(got.Y0, want.Y0) ||
		!tol.NearlyEqual(got.X1, want.X1) || !tol.NearlyEqual(got.Y1, want.Y1) {
		t.Errorf("ocrBox = %+v, want %+v", got, want)
	}
}

// TestOCRRGBAConversion covers the interface's cost. render/pdfium always returns RGBA,
// so the conversion is for a future adapter — a native rasterizer, or one rendering
// grayscale for a scan — and without it that adapter would fail at runtime instead.
func TestOCRRGBAConversion(t *testing.T) {
	rgb := stdimage.NewRGBA(stdimage.Rect(0, 0, 4, 3))
	if got := rgba(rgb); got != rgb {
		t.Error("an *image.RGBA was copied instead of passed through")
	}

	gray := stdimage.NewGray(stdimage.Rect(0, 0, 4, 3))
	gray.SetGray(1, 1, color.Gray{Y: 200})
	out := rgba(gray)
	if out.Bounds() != gray.Bounds() {
		t.Fatalf("converted bounds %v, want %v", out.Bounds(), gray.Bounds())
	}
	// The pixel survives the conversion. A draw.Src into the wrong bounds origin would
	// produce a correctly sized image of nothing, which is the failure a bounds check
	// alone would pass.
	if r, _, _, _ := out.At(1, 1).RGBA(); r>>8 != 200 {
		t.Errorf("pixel (1,1) is %d, want 200", r>>8)
	}
}

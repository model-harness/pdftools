package main

import (
	"context"
	"errors"
	stdimage "image"
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

// These tests run the ocr verb's pipeline — extract, route, rasterize, read, build, write —
// with a fake Engine in place of Tesseract, which CI does not have: everything but the reading
// is deterministic, and the reading is behind an interface so a test can state what the engine
// read and assert on what the document became.

// fakeEngine returns fixed words for every page, and records the images it was given.
type fakeEngine struct {
	words  []ocr.Word
	err    error
	calls  int
	widths []int
}

func (f *fakeEngine) Read(_ context.Context, img stdimage.Image) ([]ocr.Word, error) {
	f.calls++
	f.widths = append(f.widths, img.Bounds().Dx())
	return f.words, f.err
}

func (f *fakeEngine) Name() string { return "fake" }

// The substitution only works if the fake is what the verb accepts.
var _ ocr.Engine = (*fakeEngine)(nil)

// fakeLine is one line of three words in the top left of a 612-pixel-wide image, the second
// read at a confidence of 40.
func fakeLine() []ocr.Word {
	return []ocr.Word{
		{Text: "Recognized", Box: stdimage.Rect(60, 40, 200, 60), Conf: 96, Block: 1, Par: 1, Line: 1},
		{Text: "body", Box: stdimage.Rect(210, 40, 260, 60), Conf: 40, Block: 1, Par: 1, Line: 1},
		{Text: "text.", Box: stdimage.Rect(270, 40, 320, 60), Conf: 95, Block: 1, Par: 1, Line: 1},
	}
}

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

// TestOCRVerbReplacesScannedPage is the end-to-end claim: a scanned page comes out holding the
// words the engine read, a word below the confidence threshold as [err], and marked as read
// from pixels.
func TestOCRVerbReplacesScannedPage(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	d := extractOnly(t, in)
	before := d.Pages[0].Text()
	if strings.Contains(before, "Recognized") {
		t.Fatal("test setup: the fixture already contains the fake engine's text")
	}

	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	eng := &fakeEngine{words: fakeLine()}
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	if err := (&ocrRun{eng: eng, r: r, ropt: ropt, minConf: 50, force: false, full: func(int) doc.Page { return d.Pages[0] }}).read(context.Background(), d, []int{1}); err != nil {
		t.Fatalf("read: %v", err)
	}

	if eng.calls != 1 {
		t.Errorf("engine called %d times for 1 routed page", eng.calls)
	}
	// The engine receives the page at the requested resolution.
	if len(eng.widths) != 1 || eng.widths[0] < 500 {
		t.Errorf("engine saw image widths %v at 72 dpi over a US Letter page", eng.widths)
	}
	p := d.Pages[0]
	if want := "Recognized " + doc.Unmapped + " text."; p.Text() != want {
		t.Errorf("page 1 text is %q, want %q", p.Text(), want)
	}
	// Rasterized is the only record that this page's text was read from pixels.
	if !p.Rasterized {
		t.Error("the recognized page is not marked Rasterized")
	}
	// The boxes must land inside the page, which is the Y flip working: pixels count down
	// from the top and PDF user space up from the bottom, so a missing flip mirrors every
	// rectangle and no text comparison would notice.
	if len(p.Blocks) == 0 {
		t.Fatal("no blocks")
	}
	box := p.Blocks[0].Box
	if box.Y1 > p.Box.Y1 || box.Y0 < p.Box.Y0 || box.X1 > p.Box.X1 {
		t.Errorf("block box %+v is outside the page box %+v", box, p.Box)
	}
	// Pixel rows 40 to 60 are the top of the page, so the block sits above the midpoint.
	if mid := (p.Box.Y0 + p.Box.Y1) / 2; box.Y0 < mid {
		t.Errorf("block at y %g-%g is below the page midpoint %g: the Y flip is inverted",
			box.Y0, box.Y1, mid)
	}
}

// TestOCRVerbKeepsExtractedTextOnFailure is the conservative direction: a page the engine
// failed on keeps whatever the file said, and so does one it read no text on.
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

	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	full := func(int) doc.Page { return d.Pages[0] }
	noisy := []ocr.Word{{Text: "eee", Box: stdimage.Rect(0, 0, 10, 10), Conf: 79, Block: 1, Par: 1, Line: 1}}
	for _, c := range []struct {
		name string
		eng  *fakeEngine
		fail bool
	}{
		{"an engine error", &fakeEngine{err: errors.New("tesseract exited 1")}, true},
		// A blank page and a picture read as noise add nothing; neither is a failure.
		{"no words", &fakeEngine{}, false},
		{"noise", &fakeEngine{words: noisy}, false},
	} {
		// Against a page of nothing, so that only the gate keeps the noise out.
		if c.name == "noise" {
			full = func(int) doc.Page { return doc.Page{} }
		}
		if err := (&ocrRun{eng: c.eng, r: r, ropt: ropt, minConf: 50, force: false, full: full}).read(context.Background(), d, []int{1}); (err != nil) != c.fail {
			t.Errorf("%s: read error %v, want failure %v", c.name, err, c.fail)
		}
		if got := d.Pages[0].Text(); got != before {
			t.Errorf("a failed page lost its extracted text:\n got  %q\n want %q", got, before)
		}
		if d.Pages[0].Rasterized {
			t.Error("a failed page was marked Rasterized")
		}
	}
}

// TestOCRVerbRejects pins the flag errors, each raised before anything runs.
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
		{"negative min-conf", []string{"-min-conf", "-1", good}},
		{"min-conf above 100", []string{"-min-conf", "101", good}},
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

// TestOCRVerbOnDigitalDocument is the case that must never run an engine: a document whose
// every page carries text gets the Markdown md writes.
func TestOCRVerbOnDigitalDocument(t *testing.T) {
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

// TestOCRDryRunRunsNoEngine checks the free answer to the first question anyone asks, which
// pages of this file need OCR, on a scanned document.
func TestOCRDryRunRunsNoEngine(t *testing.T) {
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

	// And through the verb.
	if err := runOCR([]string{"-dry-run", fixture(t, "pymupdf-utilities/scanned.pdf")}); err != nil {
		t.Errorf("runOCR -dry-run: %v", err)
	}
}

// TestOCRDryRunMeasuresWhatWasRead: the plan's coverage is the one Route decides by, so a page
// drawn wholly in a font with no mapping reads 0% beside its one block of [err] (ADR 0042).
func TestOCRDryRunMeasuresWhatWasRead(t *testing.T) {
	var sb strings.Builder
	d := extractOnly(t, fixture(t, "reference/type3-unmapped.pdf"))
	want := allPages(len(d.Pages))
	if err := plan(&sb, d, want, route(d, want, ocr.DefaultThreshold, false), ocr.DefaultThreshold); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "ocr  page 1\tcoverage   0.0%\t1 block(s)") {
		t.Errorf("plan = %q, want page 1 at 0.0%% over its one block", sb.String())
	}
}

// TestOCRBoxUsesRasterExtent pins the coordinate space the words are resolved against.
//
// The two boxes differ on purpose — doc.Page.Box is the unrotated crop box, while
// render.Raster.Box has /Rotate applied — and the engine saw the rotated pixels. Taking
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

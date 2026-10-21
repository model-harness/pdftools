package main

import (
	"context"
	"errors"
	"fmt"
	stdimage "image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/bench"
	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/ocr/gemma"
	"github.com/model-harness/pdftools/render"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
)

// TestMain turns OCR and its second reader off for every test in the package, so the Tesseract
// and the inferd a machine has cannot change what the tests measure: md and bench use them when
// they are found (ADRs 0043 and 0044). The tests that want them name them.
func TestMain(m *testing.M) {
	for _, v := range []string{tesseractEnv, inferdEnv} {
		if err := os.Setenv(v, "off"); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

// testTesseract is the tesseract the opt-in tests run, or a skip.
func testTesseract(t *testing.T) {
	t.Helper()
	exe := os.Getenv("PDFSPEC_TEST_TESSERACT")
	if exe == "" {
		t.Skip("PDFSPEC_TEST_TESSERACT names no tesseract")
	}
	t.Setenv(tesseractEnv, exe)
}

// TestMDWithoutTesseractKeepsTheScan: a scanned page with no engine to read it is written as
// the extractor found it, the run succeeds, and stderr names the page.
func TestMDWithoutTesseractKeepsTheScan(t *testing.T) {
	in := fixture(t, "reference/scan-paragraphs.pdf")
	t.Setenv(tesseractEnv, "")
	t.Setenv("PATH", t.TempDir())
	out := filepath.Join(t.TempDir(), "out.md")
	warned := captureStderr(t, func() {
		if err := runMD([]string{"-o", out, in}); err != nil {
			t.Errorf("runMD: %v", err)
		}
	})
	if !strings.Contains(warned, "1 page(s) need OCR and tesseract not found") || !strings.Contains(warned, "extracted text: 1") {
		t.Errorf("stderr = %q, want the page that needs OCR named", warned)
	}

	// The variable names the engine when no flag does.
	t.Setenv(tesseractEnv, filepath.Join(t.TempDir(), "no-tesseract.exe"))
	if warned := captureStderr(t, func() { _ = runMD([]string{"-o", out, in}) }); !strings.Contains(warned, "no-tesseract.exe does not run") {
		t.Errorf("with %s naming a missing file, stderr = %q", tesseractEnv, warned)
	}

	// Off is off: nothing is said, since nothing was asked.
	t.Setenv(tesseractEnv, "off")
	if warned := captureStderr(t, func() { _ = runMD([]string{"-o", out, in}) }); strings.Contains(warned, "OCR") ||
		strings.Contains(warned, "tesseract") {
		t.Errorf("with OCR off, stderr = %q", warned)
	}
}

// TestMDRejectsABadMinConf: the shared engine flags are checked before anything runs.
func TestMDRejectsABadMinConf(t *testing.T) {
	in := fixture(t, "reference/scan-paragraphs.pdf")
	for _, v := range []string{"-1", "100.5"} {
		if err := runMD([]string{"-min-conf", v, in}); err == nil {
			t.Errorf("-min-conf %s accepted", v)
		}
	}
}

// TestBenchmarkWithTesseract is #9's done-when: with Tesseract present, the gold set's scanned
// pages meet the v1 character target, and no born-digital fixture moves, since OCR only adds
// to what the extractor found (ocr.Adds). Skipped without PDFSPEC_TEST_TESSERACT.
func TestBenchmarkWithTesseract(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "reference")
	off, err := benchRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	testTesseract(t)
	on, err := benchRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	cer, n := 0.0, 0
	for i, r := range on {
		switch {
		case strings.HasPrefix(r.name, "scan-"):
			cer += r.s.CER
			n++
		case r.name == "type3-unmapped":
			// The page of glyphs with no value is read whole, its 15 [err] with it.
			if r.s.CER > 0.01 || r.s.Errs != 0 {
				t.Errorf("type3-unmapped: CER %.3f with %d [err], want it read", r.s.CER, r.s.Errs)
			}
		case r.name == "image-kinds":
			// The text and the table are read off their images; the chart is a model's to read,
			// and without one stays [image] (TestImageKindsAreRead).
			if r.s.CER > 0.1 || r.s.Tables < 0.7 {
				t.Errorf("image-kinds: CER %.3f, tables %.3f, want its text and table read", r.s.CER, r.s.Tables)
			}
		case r.s != off[i].s:
			t.Errorf("%s moved with Tesseract present: %+v, was %+v", r.name, r.s, off[i].s)
		}
	}
	if n != 4 {
		t.Fatalf("%d scanned fixtures, want 4", n)
	}
	if mean := cer / float64(n); mean > benchTargets.cer {
		t.Errorf("scanned pages' mean CER %.4f, above the v1 target %.2f", mean, benchTargets.cer)
	}
	// With an engine for pixels, every fixture counts, and the enforced targets hold over all.
	m := benchMeans(on)
	for name, got := range map[string]float64{"order": m.order, "tables": m.tables, "style": m.style} {
		if want := map[string]float64{"order": benchTargets.order, "tables": benchTargets.tables, "style": benchTargets.style}[name]; got < want {
			t.Errorf("%s: %.3f over every fixture with Tesseract, under its target %.2f", name, got, want)
		}
	}
}

// TestImageKindsAreRead is #11's done-when on the gold set's page of images (ADR 0046): with
// Tesseract the text image is text and the table image a table, each at its character and cell
// target; with inferd's vision model too, the chart is a chart, its values a table, and the
// picture a picture. Skipped without the engines.
func TestImageKindsAreRead(t *testing.T) {
	testTesseract(t)
	path := fixture(t, "reference/image-kinds.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	d, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatal(err)
	}
	withModel := os.Getenv("PDFSPEC_TEST_INFERD") != ""
	if withModel {
		t.Setenv(inferdEnv, "auto")
	}
	_ = captureStderr(t, func() { autoOCR(d, s, path, engineFlags{lang: "eng", dpi: ocrDPI, minConf: defaultMinConf}, "pdfium") })
	var kinds []string
	for _, im := range d.Pages[0].Images {
		k := "unread"
		if im.Read != nil {
			k = im.Read.Kind
		}
		kinds = append(kinds, k)
	}
	want := "text table unread unread"
	if withModel {
		want = "text table chart picture"
	}
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("kinds = %s, want %s", got, want)
	}
	gold, err := os.ReadFile(fixture(t, "reference/image-kinds.gold.md")) // #nosec G304 -- a committed fixture
	if err != nil {
		t.Fatal(err)
	}
	md, err := convertDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	sc := bench.Score(gold, md)
	if withModel && (sc.CER > benchTargets.cer || sc.Tables < benchTargets.tables) {
		t.Errorf("with the model: CER %.3f tables %.3f, want the v1 targets", sc.CER, sc.Tables)
	}
}

func TestPageListWritesRanges(t *testing.T) {
	for _, c := range []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{3}, "3"},
		{[]int{3, 4}, "3-4"},
		{[]int{1, 2, 3, 5, 7, 8, 20}, "1-3, 5, 7-8, 20"},
	} {
		if got := pageList(c.in); got != c.want {
			t.Errorf("pageList(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOCRReadsAgainstTheFullPage: a page blank but for a watermark the extractor strips is
// compared with OCR's reading as it is with the watermark, so reading the watermark back adds
// nothing and the page keeps its text.
func TestOCRReadsAgainstTheFullPage(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	d := extractOnly(t, in)
	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	watermark := doc.Page{Blocks: []doc.Block{{Spans: []doc.Span{{Text: "Recognized body text."}}}}}
	if err := (&ocrRun{eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, minConf: 50, force: false, full: func(int) doc.Page { return watermark }}).read(context.Background(), d, []int{1}); err != nil {
		t.Fatal(err)
	}
	if d.Pages[0].Rasterized {
		t.Error("a reading of the stripped watermark replaced the page")
	}
	// -force replaces it all the same.
	if err := (&ocrRun{eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, minConf: 50, force: true, full: func(int) doc.Page { return watermark }}).read(context.Background(), d, []int{1}); err != nil || !d.Pages[0].Rasterized {
		t.Errorf("-force did not replace the page: err %v", err)
	}
}

// TestWithArtifactsKeepsThem: the page OCR's reading is compared with is the extractor's with
// its artifacts: extractPdfInput.pdf's second page marks 8 words as one, 417 without them and
// 425 with.
func TestWithArtifactsKeepsThem(t *testing.T) {
	in := fixture(t, "adobe-samples/extractPdfInput.pdf")
	plain := extractOnly(t, in)
	full := withArtifacts(in)(2)
	if a, b := len(strings.Fields(plain.Pages[1].Text())), len(strings.Fields(full.Text())); a != 417 || b != 425 {
		t.Errorf("page 2 has %d words without its artifacts and %d with, want 417 and 425", a, b)
	}
	if p := withArtifacts(filepath.Join(t.TempDir(), "missing.pdf"))(1); len(p.Blocks) != 0 {
		t.Error("a file that does not open gave a page")
	}
}

// TestOCRReportsItsUnsureWords: the words read below -min-conf are counted by page on stderr.
func TestOCRReportsItsUnsureWords(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	d := extractOnly(t, in)
	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	warned := captureStderr(t, func() {
		if err := (&ocrRun{eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, minConf: 50, force: false, full: func(int) doc.Page { return doc.Page{} }}).read(context.Background(), d, []int{1}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(warned, "1 word(s) are written [err]: p1 1") {
		t.Errorf("stderr = %q", warned)
	}
}

// TestOCRGateIsAtEighty: a page read at a mean confidence of 80 is text, and one at 79.9 is not.
func TestOCRGateIsAtEighty(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	for _, c := range []struct {
		conf float64
		read bool
	}{{80, true}, {79.9, false}} {
		d := extractOnly(t, in)
		eng := &fakeEngine{words: []ocr.Word{{Text: "word", Box: stdimage.Rect(0, 0, 10, 10), Conf: c.conf, Block: 1, Par: 1, Line: 1}}}
		if err := (&ocrRun{eng: eng, r: r, ropt: ropt, minConf: 30, force: false, full: func(int) doc.Page { return doc.Page{} }}).read(context.Background(), d, []int{1}); err != nil {
			t.Fatal(err)
		}
		if d.Pages[0].Rasterized != c.read {
			t.Errorf("a page read at %g: replaced %v, want %v", c.conf, d.Pages[0].Rasterized, c.read)
		}
	}
}

// TestOCRVerbWithoutTesseractSucceeds: the verb, like md, keeps a scan's extracted text when no
// engine is installed, and exits 0.
func TestOCRVerbWithoutTesseractSucceeds(t *testing.T) {
	t.Setenv(tesseractEnv, "")
	t.Setenv("PATH", t.TempDir())
	out := filepath.Join(t.TempDir(), "out.md")
	if err := runOCR([]string{"-o", out, fixture(t, "reference/scan-paragraphs.pdf")}); err != nil {
		t.Errorf("runOCR without tesseract: %v", err)
	}
}

// TestBenchReadsWithOCR: bench scores what md writes, so it asks for OCR as md does, here with
// an engine that is not there.
func TestBenchReadsWithOCR(t *testing.T) {
	t.Setenv(tesseractEnv, filepath.Join(t.TempDir(), "no-tesseract.exe"))
	warned := captureStderr(t, func() {
		if _, err := convertDefault(fixture(t, "reference/scan-paragraphs.pdf")); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(warned, "no-tesseract.exe does not run") {
		t.Errorf("stderr = %q, want bench to have asked for OCR", warned)
	}
}

// TestOCRDefaultsAreTheMeasuredOnes pins the figures ADR 0043 measured: a word under 30 is as
// likely wrong as right, and Tesseract reads at the rasterizer's 200 dpi as well as at 300.
func TestOCRDefaultsAreTheMeasuredOnes(t *testing.T) {
	if defaultMinConf != 30 {
		t.Errorf("defaultMinConf = %g, want 30", float64(defaultMinConf))
	}
	if ocrDPI != render.DefaultOptions.DPI {
		t.Errorf("ocrDPI = %g, want the rasterizer's %g", float64(ocrDPI), render.DefaultOptions.DPI)
	}
}

// secondReader answers every crop with one reading, or fails.
type secondReader struct {
	answer string
	err    error
	calls  int
}

func (s *secondReader) Read(context.Context, stdimage.Image, int) (string, error) {
	s.calls++
	return s.answer, s.err
}

func (s *secondReader) Name() string { return "second" }

// TestOCRSecondReaderDecides: with a second reader, fakeLine's word at 40, under 90, is kept
// where the reader agrees and [err] where it does not, and the run says how many it asked. A
// reader that fails is dropped, and the word falls back to -min-conf.
func TestOCRSecondReaderDecides(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	for _, c := range []struct {
		name, want, stderr string
		second             *secondReader
	}{
		{"agrees", "Recognized body text.", "1 word(s) under confidence 90 read again by second: 1 agreed, 0 refused", &secondReader{answer: "body"}},
		{"disagrees", "Recognized " + doc.Unmapped + " text.", "0 agreed, 0 refused", &secondReader{answer: "bodv"}},
		{"refuses", "Recognized " + doc.Unmapped + " text.", "0 agreed, 1 refused", &secondReader{err: fmt.Errorf("x: %w", ocr.ErrRefused)}},
		// Gone: the word at 40 is over -min-conf 30, so it is read as Tesseract read it.
		{"fails", "Recognized body text.", "second failed, and OCR goes on without it", &secondReader{err: errors.New("inferd: backend not ready")}},
	} {
		d := extractOnly(t, in)
		run := &ocrRun{eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, second: c.second, minConf: 30,
			full: func(int) doc.Page { return doc.Page{} }}
		routed := []int{1}
		if c.name == "fails" {
			// Read twice: a reader that failed on the first page is not asked on the second.
			routed = []int{1, 1}
		}
		warned := captureStderr(t, func() {
			if err := run.read(context.Background(), d, routed); err != nil {
				t.Error(err)
			}
		})
		if got := d.Pages[0].Text(); got != c.want {
			t.Errorf("%s: page = %q, want %q", c.name, got, c.want)
		}
		if !strings.Contains(warned, c.stderr) {
			t.Errorf("%s: stderr = %q, want %q", c.name, warned, c.stderr)
		}
		if c.second.calls != 1 {
			t.Errorf("%s: the reader was asked %d times, want once for the one word under 90", c.name, c.second.calls)
		}
	}
}

// TestOpenSecondHonoursOff: off asks nothing, and an endpoint with no daemon is no reader.
func TestOpenSecondHonoursOff(t *testing.T) {
	for _, c := range []struct {
		name, env string
		eo        engineFlags
	}{
		{"-inferd off", "", engineFlags{inferd: "off"}},
		{inferdEnv + "=off", "off", engineFlags{}},
	} {
		t.Setenv(inferdEnv, c.env)
		var got *gemma.Model
		warned := captureStderr(t, func() { got = openSecond(context.Background(), c.eo) })
		if got != nil || warned != "" {
			t.Errorf("%s: reader %v, stderr %q; off asks nothing and says nothing", c.name, got, warned)
		}
	}
}

// TestOCRAsksOnlyWhereTheReadingWillTake: a page whose reading adds nothing keeps its text, so
// the second reader is not asked about its words.
func TestOCRAsksOnlyWhereTheReadingWillTake(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	d := extractOnly(t, in)
	second := &secondReader{answer: "body"}
	same := doc.Page{Blocks: []doc.Block{{Spans: []doc.Span{{Text: "Recognized body text."}}}}}
	run := &ocrRun{eng: &fakeEngine{words: fakeLine()}, r: r, ropt: render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels},
		second: second, minConf: 30, full: func(int) doc.Page { return same }}
	_ = captureStderr(t, func() {
		if err := run.read(context.Background(), d, []int{1}); err != nil {
			t.Error(err)
		}
	})
	if second.calls != 0 || d.Pages[0].Rasterized {
		t.Errorf("asked %d times, replaced %v: want neither for a reading that adds nothing", second.calls, d.Pages[0].Rasterized)
	}
}

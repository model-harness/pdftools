package main

import (
	"context"
	stdimage "image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/render"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
)

// TestMain turns OCR off for every test in the package, so the Tesseract a machine has
// installed cannot change what the tests measure: md and bench read a routed page with it when
// it is found (ADR 0043). The tests that want it name it with PDFSPEC_TEST_TESSERACT.
func TestMain(m *testing.M) {
	if err := os.Setenv(tesseractEnv, "off"); err != nil {
		panic(err)
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
	if err := readWith(context.Background(), d, []int{1}, &fakeEngine{words: fakeLine()}, r, ropt, 50, false,
		func(int) doc.Page { return watermark }); err != nil {
		t.Fatal(err)
	}
	if d.Pages[0].Rasterized {
		t.Error("a reading of the stripped watermark replaced the page")
	}
	// -force replaces it all the same.
	if err := readWith(context.Background(), d, []int{1}, &fakeEngine{words: fakeLine()}, r, ropt, 50, true,
		func(int) doc.Page { return watermark }); err != nil || !d.Pages[0].Rasterized {
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
		if err := readWith(context.Background(), d, []int{1}, &fakeEngine{words: fakeLine()}, r, ropt, 50, false,
			func(int) doc.Page { return doc.Page{} }); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(warned, "1 word(s) read below confidence 50 are written [err]: p1 1") {
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
		if err := readWith(context.Background(), d, []int{1}, eng, r, ropt, 30, false, func(int) doc.Page { return doc.Page{} }); err != nil {
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

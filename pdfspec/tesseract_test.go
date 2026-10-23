package pdfspec

import (
	"context"
	"errors"
	"fmt"
	stdimage "image"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/render"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
)

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
	if err := (&ocrRun{log: io.Discard, eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, minConf: 50, force: false, full: func(int) doc.Page { return watermark }}).read(context.Background(), d, []int{1}); err != nil {
		t.Fatal(err)
	}
	if d.Pages[0].Rasterized {
		t.Error("a reading of the stripped watermark replaced the page")
	}
	// -force replaces it all the same.
	if err := (&ocrRun{log: io.Discard, eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, minConf: 50, force: true, full: func(int) doc.Page { return watermark }}).read(context.Background(), d, []int{1}); err != nil || !d.Pages[0].Rasterized {
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
		if err := (&ocrRun{log: io.Discard, eng: eng, r: r, ropt: ropt, minConf: 30, force: false, full: func(int) doc.Page { return doc.Page{} }}).read(context.Background(), d, []int{1}); err != nil {
			t.Fatal(err)
		}
		if d.Pages[0].Rasterized != c.read {
			t.Errorf("a page read at %g: replaced %v, want %v", c.conf, d.Pages[0].Rasterized, c.read)
		}
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

// TestOCRReportsItsUnsureWords: the words read below MinConf are counted by page in the log.
func TestOCRReportsItsUnsureWords(t *testing.T) {
	in := fixture(t, "pymupdf-utilities/scanned.pdf")
	d := extractOnly(t, in)
	r, err := renderpdfium.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	ropt := render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}
	var log strings.Builder
	if err := (&ocrRun{log: &log, eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, minConf: 50, force: false, full: func(int) doc.Page { return doc.Page{} }}).read(context.Background(), d, []int{1}); err != nil {
		t.Error(err)
	}
	if !strings.Contains(log.String(), "1 word(s) are written [err]: p1 1") {
		t.Errorf("log = %q", log.String())
	}
}

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
		var log strings.Builder
		run := &ocrRun{log: &log, eng: &fakeEngine{words: fakeLine()}, r: r, ropt: ropt, second: c.second, minConf: 30,
			full: func(int) doc.Page { return doc.Page{} }}
		routed := []int{1}
		if c.name == "fails" {
			// Read twice: a reader that failed on the first page is not asked on the second.
			routed = []int{1, 1}
		}
		if err := run.read(context.Background(), d, routed); err != nil {
			t.Error(err)
		}
		warned := log.String()
		if got := d.Pages[0].Text(); got != c.want {
			t.Errorf("%s: page = %q, want %q", c.name, got, c.want)
		}
		if !strings.Contains(warned, c.stderr) {
			t.Errorf("%s: log = %q, want %q", c.name, warned, c.stderr)
		}
		if c.second.calls != 1 {
			t.Errorf("%s: the reader was asked %d times, want once for the one word under 90", c.name, c.second.calls)
		}
	}
}

// TestOpenSecondHonoursOff: off asks nothing and says nothing.
func TestOpenSecondHonoursOff(t *testing.T) {
	var log strings.Builder
	if got := openSecond(context.Background(), "off", 0, 30, &log); got != nil || log.Len() != 0 {
		t.Errorf("reader %v, log %q; off asks nothing and says nothing", got, log.String())
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
	run := &ocrRun{log: io.Discard, eng: &fakeEngine{words: fakeLine()}, r: r, ropt: render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels},
		second: second, minConf: 30, full: func(int) doc.Page { return same }}
	if err := run.read(context.Background(), d, []int{1}); err != nil {
		t.Error(err)
	}
	if second.calls != 0 || d.Pages[0].Rasterized {
		t.Errorf("asked %d times, replaced %v: want neither for a reading that adds nothing", second.calls, d.Pages[0].Rasterized)
	}
}

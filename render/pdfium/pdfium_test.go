package pdfium

import (
	"errors"
	"fmt"
	stdimage "image"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/3rg0n/pdf-spec/render"
)

// fixtureDir is the committed reference corpus. See docs/test.docs.md: these files
// were chosen upstream as test inputs, and unlike the gitignored spec corpus they
// are always present, so these tests never skip.
const fixtureDir = "../../testdata"

func fixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(fixtureDir, filepath.FromSlash(name))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reference fixture missing: %s — run `pwsh testdata/fetch.ps1 -Download`", name)
	}
	return path
}

func open(t *testing.T, name string) render.Rasterizer {
	t.Helper()
	r, err := Open(fixture(t, name))
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// TestOpenPageCounts checks the adapter's own parser against the one the rest of the
// repo uses.
//
// Worth its own test because the two parsers are genuinely independent — pdfium
// brings its own and cannot be handed an objects.Store — so a disagreement about
// page count is a real possibility on a damaged file, and a caller looping over
// pages must use the number belonging to the thing it calls. These counts are the
// same ones cmd/pdfspec/testdata_test.go asserts against pdfcpu.
func TestOpenPageCounts(t *testing.T) {
	for _, tc := range []struct {
		file  string
		pages int
	}{
		{"pymupdf-utilities/scanned.pdf", 1},
		{"pymupdf/type3font.pdf", 1},
		{"pymupdf/2201.00069.pdf", 1},
		{"pymupdf/mupdf_explored.pdf", 285},
		{"adobe-samples/ocrInput.pdf", 4},
		{"adobe-samples/disqualifiedScannedPages.pdf", 151},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if got := open(t, tc.file).PageCount(); got != tc.pages {
				t.Errorf("PageCount = %d, want %d (pdfcpu agrees on %d)", got, tc.pages, tc.pages)
			}
		})
	}
}

// TestOpenRejectsZeroLength pins that a file which is not a PDF fails at Open with a
// reason.
//
// zeroLength.pdf is 0 bytes and is Adobe's own deliberately-invalid input. The
// requirement is the same one the extraction path has: fail with something a user can
// read, not a panic and not an empty success that looks like a blank document.
func TestOpenRejectsZeroLength(t *testing.T) {
	r, err := Open(fixture(t, "adobe-samples/zeroLength.pdf"))
	if err == nil {
		_ = r.Close()
		t.Fatal("Open succeeded on a 0-byte file")
	}
	if r != nil {
		t.Error("Open returned a non-nil Rasterizer with an error")
	}
}

// TestPageDimensionsMatchFit is the adapter's contract with render.Fit: the image it
// produces is the size the policy asked for.
//
// The dimensions are computed independently here from the fixture's known page size
// rather than from the adapter's own box, so a bug in reading the box cannot make the
// test agree with itself.
func TestPageDimensionsMatchFit(t *testing.T) {
	for _, tc := range []struct {
		file       string
		dpi        float64
		wantW      int
		wantH      int
		wantPointW float64
	}{
		// 72x72 pt, the smallest page in the fixtures: 1:1 at 72 DPI.
		{"pymupdf/type3font.pdf", 72, 72, 72, 72},
		{"pymupdf/type3font.pdf", 200, 200, 200, 72},
		// A4 at 595.28 x 841.89 pt, rounding up on both axes.
		{"pymupdf/2201.00069.pdf", 200, 1654, 2339, 595.28},
		// US Letter, the round case.
		{"pymupdf-utilities/scanned.pdf", 200, 1700, 2200, 612},
	} {
		t.Run(tc.file, func(t *testing.T) {
			opt := render.Options{DPI: tc.dpi, MaxPixels: render.DefaultOptions.MaxPixels}
			ra, err := open(t, tc.file).Page(1, opt)
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			b := ra.Image.Bounds()
			if b.Dx() != tc.wantW || b.Dy() != tc.wantH {
				t.Errorf("image %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.wantW, tc.wantH)
			}
			if ra.DPI != tc.dpi {
				t.Errorf("DPI = %v, want %v (nothing here should hit the cap)", ra.DPI, tc.dpi)
			}
			if ra.Number != 1 {
				t.Errorf("Number = %d, want 1: page numbers are 1-based everywhere in this repo", ra.Number)
			}
			// The box is in points and must be the page's own size, not the pixels'.
			if got := ra.Box.Width(); got < tc.wantPointW-1 || got > tc.wantPointW+1 {
				t.Errorf("Box width %v pt, want about %v", got, tc.wantPointW)
			}
		})
	}
}

// TestPageRejectsOutOfRange pins the 1-based conversion at both ends.
//
// pdfium is 0-based and everything else in this repo is 1-based, so page 0 is the
// off-by-one that would silently render page 1, and the count itself is the one that
// would silently render the last page's successor. Both must be errors.
func TestPageRejectsOutOfRange(t *testing.T) {
	r := open(t, "adobe-samples/ocrInput.pdf") // 4 pages
	for _, n := range []int{0, -1, 5, 1 << 30} {
		if _, err := r.Page(n, render.DefaultOptions); err == nil {
			t.Errorf("Page(%d) succeeded on a 4-page document", n)
		}
	}
	// The boundaries themselves must work.
	for _, n := range []int{1, 4} {
		if _, err := r.Page(n, render.DefaultOptions); err != nil {
			t.Errorf("Page(%d) failed on a 4-page document: %v", n, err)
		}
	}
}

// TestClosedRasterizerErrors requires a use-after-close to be an error rather than a
// crash inside WASM. The instance's linear memory is gone by then, so this is the
// difference between a message and a segfault-equivalent.
func TestClosedRasterizerErrors(t *testing.T) {
	r, err := Open(fixture(t, "pymupdf-utilities/test.pdf"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close is idempotent: a deferred Close plus an explicit one is normal, and the
	// second must not double-free the WASM instance.
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := r.Page(1, render.DefaultOptions); err == nil {
		t.Error("Page succeeded on a closed rasterizer")
	}
}

// TestPagesDoNotAliasEachOther is the test for the defect that would be hardest to
// recognize in output.
//
// The adapter's image Pix is assigned from wazero's Memory().Read, which returns a
// view into WASM linear memory rather than a copy, and the pdfium bitmap it points at
// is freed and reused by the next render. Without copying, holding two Rasters means
// holding one buffer twice: the second page's pixels appear in the first, and the
// symptom is content from the wrong page rather than an error.
//
// Two visibly different fixture pages, rendered in sequence and compared afterwards.
func TestPagesDoNotAliasEachOther(t *testing.T) {
	r := open(t, "adobe-samples/ocrInput.pdf")
	opt := render.Options{DPI: 100, MaxPixels: render.DefaultOptions.MaxPixels}

	first, err := r.Page(1, opt)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	before := ink(t, first.Image)

	// Rendering more pages reuses the WASM-side bitmap that an uncopied first page
	// would still be pointing at.
	for n := 2; n <= 4; n++ {
		if _, err := r.Page(n, opt); err != nil {
			t.Fatalf("page %d: %v", n, err)
		}
	}

	if after := ink(t, first.Image); after != before {
		t.Errorf("page 1 changed after rendering pages 2-4: %d non-white pixels became %d "+
			"— the image aliases WASM memory instead of owning a copy", before, after)
	}
	// A blank result would pass the comparison above trivially, so the page must
	// have had content in the first place. Page 1 of Adobe's OCR sample is a scan.
	if before == 0 {
		t.Fatal("page 1 rendered blank: the aliasing check needs a page with content")
	}
}

// TestConcurrentRasterizers pins the parallelism model the render verb relies on.
//
// A Rasterizer is single-threaded by contract, so the verb runs several over the same
// file. This checks that the shared pool hands out independent instances and that
// pages rendered concurrently match pages rendered alone — the failure it guards
// against is two workers sharing one WASM linear memory, which would corrupt both.
func TestConcurrentRasterizers(t *testing.T) {
	const name = "adobe-samples/ocrInput.pdf"
	opt := render.Options{DPI: 100, MaxPixels: render.DefaultOptions.MaxPixels}

	want := make([]int, 5)
	seq := open(t, name)
	for n := 1; n <= 4; n++ {
		ra, err := seq.Page(n, opt)
		if err != nil {
			t.Fatalf("sequential page %d: %v", n, err)
		}
		want[n] = ink(t, ra.Image)
	}

	got := make([]int, 5)
	var wg sync.WaitGroup
	for n := 1; n <= 4; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r, err := Open(fixture(t, name))
			if err != nil {
				t.Errorf("page %d: Open: %v", n, err)
				return
			}
			defer func() { _ = r.Close() }()
			ra, err := r.Page(n, opt)
			if err != nil {
				t.Errorf("page %d: %v", n, err)
				return
			}
			got[n] = ink(t, ra.Image)
		}(n)
	}
	wg.Wait()

	for n := 1; n <= 4; n++ {
		if got[n] != want[n] {
			t.Errorf("page %d: %d non-white pixels concurrently, %d sequentially",
				n, got[n], want[n])
		}
	}
}

// TestPageAppliesCap requires the adapter to route through render.Fit rather than
// asking pdfium for a DPI directly. A 200-inch page is what /MediaBox lets a producer
// declare for free.
func TestPageAppliesCap(t *testing.T) {
	opt := render.Options{DPI: 400, MaxPixels: 1 << 20}
	ra, err := open(t, "pymupdf/2201.00069.pdf").Page(1, opt)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	b := ra.Image.Bounds()
	if px := b.Dx() * b.Dy(); px > opt.MaxPixels {
		t.Errorf("%dx%d = %d pixels exceeds MaxPixels %d", b.Dx(), b.Dy(), px, opt.MaxPixels)
	}
	if !ra.Reduced(opt) {
		t.Errorf("DPI %v not reported as reduced from %v", ra.DPI, opt.DPI)
	}
}

// TestRenderedPageHasContent checks that rendering actually produces the page rather
// than a blank bitmap of the right size, which every dimension assertion above would
// accept.
//
// The counts are lower bounds from a measured run, not exact values: pdfium's
// antialiasing may shift a pixel or two between builds, and a test that pinned an
// exact count would fail on an upgrade that changed nothing that matters. What is
// asserted is the distinction the OCR router will act on — a scan has ink, an empty
// page does not.
func TestRenderedPageHasContent(t *testing.T) {
	opt := render.Options{DPI: 100, MaxPixels: render.DefaultOptions.MaxPixels}
	for _, tc := range []struct {
		file    string
		page    int
		minInk  float64
		maxInk  float64
		comment string
	}{
		{"pymupdf-utilities/scanned.pdf", 1, 5, 30, "a scanned page: ink from the image"},
		{"pymupdf-utilities/ocr-ed.pdf", 1, 5, 30, "the same scan with an invisible text layer: the same ink"},
		{"pymupdf/2201.00069.pdf", 1, 1, 20, "a born-digital LaTeX page"},
		// The disqualified input has no fonts and no images. A blank render is the
		// correct answer, and it is the negative control for every case above.
		{"adobe-samples/disqualifiedScannedPages.pdf", 1, 0, 0.01, "no fonts, no images: blank is correct"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			ra, err := open(t, tc.file).Page(tc.page, opt)
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			b := ra.Image.Bounds()
			pct := 100 * float64(ink(t, ra.Image)) / float64(b.Dx()*b.Dy())
			if pct < tc.minInk || pct > tc.maxInk {
				t.Errorf("%.2f%% non-white pixels, want %v-%v%% (%s)",
					pct, tc.minInk, tc.maxInk, tc.comment)
			}
		})
	}
}

// TestType3PageRenders is the counterpart to the extraction result for this file.
//
// type3font.pdf has Type3 glyphs with no recoverable Unicode, so extraction correctly
// yields nothing. Rendering it yields the glyphs as drawn — which is the entire
// argument for the OCR path existing, so it must not be an error or a blank page.
func TestType3PageRenders(t *testing.T) {
	ra, err := open(t, "pymupdf/type3font.pdf").Page(1, render.DefaultOptions)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	b := ra.Image.Bounds()
	if n := ink(t, ra.Image); n == 0 {
		t.Errorf("blank: a page whose text cannot be extracted still has marks to rasterize")
	} else {
		t.Logf("%dx%d, %d non-white pixels", b.Dx(), b.Dy(), n)
	}
}

// ink counts non-white pixels, which is a crude but stable measure of whether
// anything was drawn. Crude on purpose: an exact hash would fail on any pdfium
// upgrade that changed antialiasing, and the question these tests ask is whether the
// page rendered at all, not whether it rendered identically to a stored image.
func ink(t *testing.T, img stdimage.Image) int {
	t.Helper()
	rgba, ok := img.(*stdimage.RGBA)
	if !ok {
		t.Fatalf("image is %T, want *image.RGBA", img)
	}
	n := 0
	for y := rgba.Rect.Min.Y; y < rgba.Rect.Max.Y; y++ {
		row := rgba.Pix[(y-rgba.Rect.Min.Y)*rgba.Stride:]
		for x := 0; x < rgba.Rect.Dx(); x++ {
			p := row[x*4 : x*4+4]
			if p[0] < 250 || p[1] < 250 || p[2] < 250 {
				n++
			}
		}
	}
	return n
}

// TestAnnotationsFlag pins that Options.Annotations reaches pdfium and that turning it
// on changes the page.
//
// It needs a file built here rather than a fixture, and that is the finding: no PDF in
// testdata/ or the corpus has an annotation with a visible appearance stream, so this
// flag was silently untested. It was also wrong. go-pdfium's RenderForm calls
// FPDF_FFLDraw, which draws form *fields* only; a Square, Highlight, or FreeText
// appearance stream is drawn under the separate FPDF_ANNOT render flag. Setting one
// without the other made -annots a no-op for every annotation subtype except form
// fields, and this test measured 640 non-white pixels with the flag on — the page's own
// square, and nothing of the annotation's.
//
// Built rather than committed because docs/test.docs.md commits only files upstream
// authored as test inputs, and this is neither.
func TestAnnotationsFlag(t *testing.T) {
	// A 200x200 page drawing a small red square, plus a Square annotation whose
	// appearance stream fills 100x100 in blue. The annotation covers 10,000 px at 72
	// DPI, so the two renders cannot be confused for each other.
	b := buildAnnotatedPDF()

	off, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = off.Close() }()
	plain, err := off.Page(1, render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels})
	if err != nil {
		t.Fatalf("without annotations: %v", err)
	}

	on, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = on.Close() }()
	marked, err := on.Page(1, render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels, Annotations: true})
	if err != nil {
		t.Fatalf("with annotations: %v", err)
	}

	bare, annotated := ink(t, plain.Image), ink(t, marked.Image)
	if bare == 0 {
		t.Fatal("the page itself rendered blank: the comparison needs page content to be present in both")
	}
	// The appearance stream is 100x100 at 72 DPI. Anything less than most of that means
	// the flag reached pdfium but the annotation was not drawn.
	if annotated-bare < 9000 {
		t.Errorf("annotations added %d non-white pixels (%d to %d), want about 10000: "+
			"the appearance stream was not drawn", annotated-bare, bare, annotated)
	}
	// And off must mean off, or the default silently burns comments into every page.
	if bare >= annotated {
		t.Errorf("Annotations:false rendered %d non-white pixels against %d with it on: "+
			"the flag is ignored", bare, annotated)
	}
}

// buildAnnotatedPDF returns a one-page PDF with a Square annotation.
//
// Written out by hand with a real xref table rather than assembled by a library,
// because a library that produced it would be a second dependency to test the first
// one with. Offsets are computed from the bytes actually written, so the table cannot
// drift from the objects.
func buildAnnotatedPDF() []byte {
	page := []byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] " +
		"/Contents 4 0 R /Annots [5 0 R] >>")
	content := []byte("q 1 0 0 RG 4 w 10 10 40 40 re S Q")
	// /F 4 is the Print flag. Without it a viewer may hide the annotation, and pdfium
	// honours the flag rather than drawing everything it finds.
	annot := []byte("<< /Type /Annot /Subtype /Square /Rect [80 80 180 180] /F 4 " +
		"/IC [0 0 1] /C [0 0 1] /CA 1 /AP << /N 6 0 R >> >>")
	appearance := []byte("q 0 0 1 rg 0 0 100 100 re f Q")

	objs := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		page,
		stream(nil, content),
		annot,
		stream([]byte("/Type /XObject /Subtype /Form /BBox [0 0 100 100] "), appearance),
	}

	var out []byte
	out = append(out, "%PDF-1.7\n"...)
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = len(out)
		out = append(out, fmt.Sprintf("%d 0 obj\n", i+1)...)
		out = append(out, o...)
		out = append(out, "\nendobj\n"...)
	}
	start := len(out)
	out = append(out, fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)...)
	for _, off := range offsets {
		out = append(out, fmt.Sprintf("%010d 00000 n \n", off)...)
	}
	out = append(out, fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, start)...)
	return out
}

func stream(extra, body []byte) []byte {
	return []byte(fmt.Sprintf("<< %s/Length %d >>\nstream\n%s\nendstream", extra, len(body), body))
}

// TestOpenMissingFile pins that a path error surfaces as a path error rather than
// being reported as a malformed PDF.
func TestOpenMissingFile(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "does-not-exist.pdf"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want an os.ErrNotExist", err)
	}
}

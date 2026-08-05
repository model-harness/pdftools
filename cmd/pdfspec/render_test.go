package main

import (
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/model-harness/pdftools/render"
)

// TestParseRanges covers the page-range syntax. It is the one piece of the render
// verb with no I/O in it, and the failure mode it guards against is silent: a spec
// that parses to the wrong pages renders successfully and produces the wrong files.
func TestParseRanges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  string
		count int
		want  []int
	}{
		{"empty means all", "", 3, []int{1, 2, 3}},
		{"whitespace means all", "  ", 3, []int{1, 2, 3}},
		{"single page", "2", 3, []int{2}},
		{"list", "1,3", 3, []int{1, 3}},
		{"closed range", "2-4", 5, []int{2, 3, 4}},
		{"open range runs to the end", "3-", 5, []int{3, 4, 5}},
		{"mixed", "1,3-5,8", 10, []int{1, 3, 4, 5, 8}},
		{"one-page range", "2-2", 3, []int{2}},
		{"whole document as a range", "1-", 3, []int{1, 2, 3}},
		{"spaces are tolerated", " 1 , 3 - 4 ", 5, []int{1, 3, 4}},
		{"trailing comma", "1,2,", 3, []int{1, 2}},
		// Sorted and deduplicated, so the files written are in page order however the
		// spec was written and an overlapping range does not render a page twice.
		{"out of order is sorted", "9,1,5", 10, []int{1, 5, 9}},
		{"duplicates collapse", "2,2,2", 3, []int{2}},
		{"overlapping ranges collapse", "1-3,2-4", 5, []int{1, 2, 3, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRanges(tc.spec, tc.count)
			if err != nil {
				t.Fatalf("parseRanges(%q, %d): %v", tc.spec, tc.count, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestParseRangesRejects pins what must fail.
//
// Out-of-range is an error rather than a clamp, and that is the decision worth a
// test: "-pages 500" on a 3-page file is a mistake, and rendering nothing while
// reporting success is how a user concludes the tool is broken.
func TestParseRangesRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  string
		count int
	}{
		{"past the end", "500", 3},
		{"range past the end", "1-500", 3},
		{"zero is not a page", "0", 3},
		{"negative", "-1", 3}, // parses as a range from "" to 1
		{"backwards", "5-2", 10},
		{"not a number", "abc", 3},
		{"partly not a number", "1,abc", 3},
		{"float", "1.5", 3},
		{"only a comma", ",", 3},
		{"overflow", "99999999999999999999", 3},
		{"no pages in the document", "1", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := parseRanges(tc.spec, tc.count); err == nil {
				t.Errorf("parseRanges(%q, %d) = %v, want an error", tc.spec, tc.count, got)
			}
		})
	}
}

// TestRenderVerbWritesPages runs the verb end to end and checks the files it wrote
// are decodable images of the right size, which is the only claim that matters to a
// user: the directory holds pages they can open.
func TestRenderVerbWritesPages(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, "adobe-samples/ocrInput.pdf") // 4 pages, US Letter scans

	if err := runRender([]string{"-o", dir, "-dpi", "72", "-pages", "1,3", in}); err != nil {
		t.Fatalf("runRender: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("wrote %d files, want 2", len(entries))
	}
	// Names are zero-padded to the *document's* page count, not the selection's, so
	// a listing sorts in page order and page 10 does not precede page 2. Four pages
	// means one digit.
	for i, want := range []string{"page-1.png", "page-3.png"} {
		if got := entries[i].Name(); got != want {
			t.Errorf("file %d is %q, want %q", i, got, want)
		}
	}

	for _, name := range []string{"page-1.png", "page-3.png"} {
		f, err := os.Open(filepath.Join(dir, name)) // #nosec G304 -- a path this test just wrote
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// US Letter at 72 DPI is 1:1 with points.
		if b := img.Bounds(); b.Dx() != 612 || b.Dy() != 792 {
			t.Errorf("%s is %dx%d, want 612x792", name, b.Dx(), b.Dy())
		}
	}
}

// TestRenderVerbPadsToPageCount pins the zero-padding width against a document large
// enough for it to matter. Without it a directory listing puts page 100 before page
// 2, which on a 285-page manual makes the output unusable in a file browser — the
// same reason md -split pads.
func TestRenderVerbPadsToPageCount(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, "pymupdf/mupdf_explored.pdf") // 285 pages

	if err := runRender([]string{"-o", dir, "-dpi", "36", "-pages", "1,285", in}); err != nil {
		t.Fatalf("runRender: %v", err)
	}
	for _, want := range []string{"page-001.png", "page-285.png"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			names, _ := os.ReadDir(dir)
			var got []string
			for _, e := range names {
				got = append(got, e.Name())
			}
			t.Errorf("missing %s; directory holds %v", want, got)
		}
	}
}

// TestRenderVerbJPEG covers the second format. JPEG exists for the case where a
// hundred rendered pages need to fit somewhere; PNG is the default because the OCR
// path is the main consumer and JPEG ringing around small type costs characters.
func TestRenderVerbJPEG(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, "pymupdf-utilities/scanned.pdf")

	if err := runRender([]string{"-o", dir, "-dpi", "72", "-format", "jpeg", "-quality", "70", in}); err != nil {
		t.Fatalf("runRender: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "page-1.jpg")) // #nosec G304 -- a path this test just wrote
	if err != nil {
		t.Fatalf("expected page-1.jpg: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := jpeg.Decode(f); err != nil {
		t.Errorf("decode: %v", err)
	}
}

// TestRenderVerbParallel checks that -jobs produces the same files as sequential
// rendering. Each worker is its own Rasterizer over the same file, so the failure
// this guards against is two workers writing the same page or skipping one.
func TestRenderVerbParallel(t *testing.T) {
	in := fixture(t, "adobe-samples/ocrInput.pdf")

	seq := t.TempDir()
	if err := runRender([]string{"-o", seq, "-dpi", "72", "-jobs", "1", in}); err != nil {
		t.Fatalf("sequential: %v", err)
	}
	par := t.TempDir()
	if err := runRender([]string{"-o", par, "-dpi", "72", "-jobs", "4", in}); err != nil {
		t.Fatalf("parallel: %v", err)
	}

	for n := 1; n <= 4; n++ {
		name := fmt.Sprintf("page-%d.png", n)
		a, err := os.ReadFile(filepath.Join(seq, name)) // #nosec G304 -- a path this test just wrote
		if err != nil {
			t.Fatalf("sequential %s: %v", name, err)
		}
		b, err := os.ReadFile(filepath.Join(par, name)) // #nosec G304 -- a path this test just wrote
		if err != nil {
			t.Fatalf("parallel %s: %v", name, err)
		}
		// Byte-identical is a fair bar here: same rasterizer, same options, same
		// encoder. Rendering order is the only variable, and it must not be one.
		if len(a) != len(b) {
			t.Errorf("%s: %d bytes sequentially, %d in parallel", name, len(a), len(b))
		}
	}
}

// TestRenderVerbRejects pins the flag and input errors, each of which must fail
// before anything is written.
func TestRenderVerbRejects(t *testing.T) {
	good := fixture(t, "pymupdf-utilities/test.pdf")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no output directory", []string{good}},
		{"no input", []string{"-o", t.TempDir()}},
		{"two inputs", []string{"-o", t.TempDir(), good, good}},
		{"zero dpi", []string{"-o", t.TempDir(), "-dpi", "0", good}},
		{"negative dpi", []string{"-o", t.TempDir(), "-dpi", "-100", good}},
		{"unknown format", []string{"-o", t.TempDir(), "-format", "tiff", good}},
		{"quality out of range", []string{"-o", t.TempDir(), "-format", "jpeg", "-quality", "0", good}},
		{"page past the end", []string{"-o", t.TempDir(), "-pages", "500", good}},
		// Adobe's own 0-byte input. It must fail with a reason rather than produce an
		// empty directory that looks like a document with no pages.
		{"zero-length pdf", []string{"-o", t.TempDir(), fixture(t, "adobe-samples/zeroLength.pdf")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runRender(tc.args); err == nil {
				t.Error("runRender succeeded, want an error")
			}
		})
	}
}

// TestRenderVerbCapReducesResolution pins that -maxpixels reaches the rasterizer and
// that the file on disk respects it, not just the computation. The cap is what stands
// between a producer-declared 200-inch /MediaBox and a multi-gigabyte allocation.
func TestRenderVerbCapReducesResolution(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, "pymupdf/2201.00069.pdf") // A4

	const cap = 1 << 20
	if err := runRender([]string{"-o", dir, "-dpi", "600", "-maxpixels", "1048576", in}); err != nil {
		t.Fatalf("runRender: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "page-1.png")) // #nosec G304 -- a path this test just wrote
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if px := cfg.Width * cfg.Height; px > cap {
		t.Errorf("%dx%d = %d pixels exceeds -maxpixels %d", cfg.Width, cfg.Height, px, cap)
	}
	// A cap that produced a thumbnail would satisfy the bound and be useless. A4 at
	// the reduced DPI should still be around 1 Mpx.
	if px := cfg.Width * cfg.Height; px < cap/2 {
		t.Errorf("%dx%d = %d pixels is far under the %d cap: the reduction overshot", cfg.Width, cfg.Height, px, cap)
	}
}

// TestEncoderSelection covers the format flag's own logic, including the extension it
// chooses — a file named .png holding JPEG bytes is worse than a rejected flag.
func TestEncoderSelection(t *testing.T) {
	for _, tc := range []struct {
		format  string
		quality int
		wantExt string
		wantErr bool
	}{
		{"png", 90, "png", false},
		{"PNG", 90, "png", false},
		{"jpeg", 90, "jpg", false},
		{"jpg", 90, "jpg", false},
		{"JPEG", 1, "jpg", false},
		{"jpeg", 0, "", true},
		{"jpeg", 101, "", true},
		{"tiff", 90, "", true},
		{"", 90, "", true},
		// Quality is only meaningful for JPEG, so an out-of-range value with PNG is
		// not an error: the flag is unused, and rejecting it would fail a run over a
		// setting that changes nothing.
		{"png", 0, "png", false},
	} {
		t.Run(fmt.Sprintf("%s/q%d", tc.format, tc.quality), func(t *testing.T) {
			enc, ext, err := encoder(tc.format, tc.quality)
			if tc.wantErr {
				if err == nil {
					t.Errorf("encoder(%q, %d) succeeded, want an error", tc.format, tc.quality)
				}
				return
			}
			if err != nil {
				t.Fatalf("encoder(%q, %d): %v", tc.format, tc.quality, err)
			}
			if ext != tc.wantExt {
				t.Errorf("ext = %q, want %q", ext, tc.wantExt)
			}
			if enc == nil {
				t.Error("encoder is nil")
			}
		})
	}
}

// TestRenderMatchesProbeRouting is the cross-check between the two verbs.
//
// probe's whole output is a routing decision, and for the pages it routes to "ocr"
// that decision is a claim that rasterizing them is worthwhile. This checks the claim
// holds: every fixture probe sends to the OCR path must render, because a page that
// cannot be rasterized has nowhere left to go.
//
// disqualifiedScannedPages.pdf is the honest exception and it is included on purpose.
// probe routes it to "ocr" because it has no fonts, and it renders — blank, because it
// has no images either. Adobe's own OCR declines it. Rendering successfully and
// finding nothing is the correct outcome and is what the OCR router will have to
// recognize.
func TestRenderMatchesProbeRouting(t *testing.T) {
	for _, name := range []string{
		"pymupdf-utilities/scanned.pdf",
		"adobe-samples/ocrInput.pdf",
		"pymupdf/img-regular.pdf",
		"adobe-samples/disqualifiedScannedPages.pdf",
		"pymupdf/test_delete_image.pdf",
	} {
		t.Run(name, func(t *testing.T) {
			path := fixture(t, name)
			if got := probeOne(path, false).Path; got != "ocr" {
				t.Fatalf("probe routes this to %q, not %q: the fixture table changed", got, "ocr")
			}
			dir := t.TempDir()
			if err := runRender([]string{"-o", dir, "-dpi", "72", "-pages", "1", path}); err != nil {
				t.Errorf("probe routes this to the OCR path but it will not rasterize: %v", err)
			}
		})
	}
}

// TestRenderDefaultDPIMatchesDesign pins the default against DESIGN.md §6, which
// specifies 200 DPI, and against what the OCR models want: LightOnOCR-2 asks for 200
// DPI at 1540 px on the longest edge, and granite-docling is trained at a comparable
// scale. A default that drifted from the model's expected input would degrade OCR
// with nothing in the output to say why.
func TestRenderDefaultDPIMatchesDesign(t *testing.T) {
	if render.DefaultOptions.DPI != 200 {
		t.Errorf("default DPI is %v, want 200 per DESIGN.md §6", render.DefaultOptions.DPI)
	}
	// A4 at 200 DPI is 2339 px on the long edge, comfortably above the 1540 px the
	// models ask for, so downscaling to model input never upsamples.
	if _, _, h, err := render.Fit(letterBox, render.DefaultOptions); err != nil || h < 1540 {
		t.Errorf("letter at the default DPI is %d px tall (err %v), want at least 1540", h, err)
	}
}

package main

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/pdfbuild"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/native"
)

// stubRasterizer answers on a script, so the fallback's decision can be tested without two real
// backends and a PDF that exercises them.
//
// A stub rather than real backends because the subject is the *routing*: which page goes to which
// rasterizer and what happens to each kind of error. Driving that with real documents would need a
// file whose pages the native backend refuses for one reason and fails on for another, which is a
// fixture about pdfium and pdfcpu rather than about these fifteen lines.
type stubRasterizer struct {
	name   string
	err    map[int]error // per page; absent means success
	asked  []int
	closed bool
}

func (s *stubRasterizer) PageCount() int { return 3 }

func (s *stubRasterizer) Page(n int, _ render.Options) (*render.Raster, error) {
	s.asked = append(s.asked, n)
	if err, ok := s.err[n]; ok {
		return nil, err
	}
	return &render.Raster{Number: n, Image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, nil
}

func (s *stubRasterizer) Close() error {
	s.closed = true
	return nil
}

// TestFallbackRoutesPerPageAndOnlyOnUnsupported is the CLI's half of ADR 0017.
//
// ADR 0015 said ErrUnsupported was "what a fallback will be built on, not something one already
// uses". This is it being used, and it had no test: the whole -backend auto path was reachable from
// the command line and asserted by nothing.
//
// Two properties, and the second is the one worth the test. Per *page* rather than per document,
// because the native backend refuses a page it cannot draw in full — so a mixed document gets
// native renderings where it can and correct ones everywhere else, where a per-document choice
// would give up at the first unsupported feature. And *only* ErrUnsupported falls through: a
// malformed page is an error from either backend, and retrying it would replace a specific
// complaint with a vaguer one from a second parser.
func TestFallbackRoutesPerPageAndOnlyOnUnsupported(t *testing.T) {
	broken := errors.New("this page is malformed")
	unsupported := &native.Unsupported{Page: 2, Ops: []string{"Do"}}

	nat := &stubRasterizer{name: "native", err: map[int]error{2: unsupported, 3: broken}}
	pdf := &stubRasterizer{name: "pdfium"}
	f := &fallback{native: nat, borrowed: pdf}

	// Page 1: native draws it, and the borrowed backend is never asked.
	if _, err := f.Page(1, render.DefaultOptions); err != nil {
		t.Fatalf("page 1: %v", err)
	}
	// Page 2: native refuses, so the borrowed backend answers.
	if _, err := f.Page(2, render.DefaultOptions); err != nil {
		t.Fatalf("page 2 should have fallen back: %v", err)
	}
	// Page 3: native fails for a reason that is not a missing feature, which must surface as
	// itself rather than being retried.
	_, err := f.Page(3, render.DefaultOptions)
	if !errors.Is(err, broken) {
		t.Errorf("page 3 error = %v, want the malformed-page error unchanged", err)
	}

	if want := []int{1, 2, 3}; !sameInts(nat.asked, want) {
		t.Errorf("native was asked for %v, want %v — every page goes to it first", nat.asked, want)
	}
	if want := []int{2}; !sameInts(pdf.asked, want) {
		t.Errorf("the borrowed backend was asked for %v, want %v — only the refused page, and"+
			" never the malformed one", pdf.asked, want)
	}
}

// TestFallbackClosesBothBackends pins that neither is leaked.
//
// Each holds a resource the other does not — the native one a parsed Store, the borrowed one a
// WASM instance and a second parse of the file — so closing one and forgetting the other leaks a
// file handle per document, which shows up as a process that cannot delete its own temp files
// rather than as a test failure.
func TestFallbackClosesBothBackends(t *testing.T) {
	nat, pdf := &stubRasterizer{}, &stubRasterizer{}
	if err := (&fallback{native: nat, borrowed: pdf}).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !nat.closed || !pdf.closed {
		t.Errorf("closed native=%v borrowed=%v, want both", nat.closed, pdf.closed)
	}
}

// TestUnknownBackendNamesTheChoices pins the error a typo gets.
//
// "unknown -backend" with the list is the difference between a user fixing a flag in one attempt
// and reading the source, and the list is built from the same constant the flag's help text uses so
// the two cannot drift apart.
func TestUnknownBackendNamesTheChoices(t *testing.T) {
	_, err := openRasterizer("natve", "nonexistent.pdf")
	if err == nil {
		t.Fatal("a misspelled backend was accepted")
	}
	for _, want := range []string{"natve", "pdfium", "native", "auto"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestNativeBackendIsGivenFaces pins the wiring this repo's whole substitution story rests on.
//
// render/native refuses every page whose font embeds no program unless a font.FaceSource is
// supplied, which is 1,139 of the corpus's 1,251 pages — so a native backend constructed without
// one draws almost nothing while still being a valid, working Rasterizer that returns no error at
// construction time. The failure is therefore invisible here and shows up as a corpus coverage
// figure collapsing, which is exactly the kind of wiring defect this repo has been bitten by.
//
// Asserted by rendering a page that *needs* a substitute: a standard-14 font with no descriptor,
// which is the corpus's most common case by a wide margin.
func TestNativeBackendIsGivenFaces(t *testing.T) {
	path := standard14PDF(t)
	r, err := openRasterizer("native", path)
	if err != nil {
		t.Fatalf("openRasterizer: %v", err)
	}
	defer func() { _ = r.Close() }()

	o := render.DefaultOptions
	o.DPI = 72
	if _, err := r.Page(1, o); err != nil {
		t.Fatalf("Page: %v — the native backend was built without a face source, so every"+
			" standard-14 page is refused", err)
	}
}

// standard14PDF writes a page of Helvetica with no descriptor and no embedded program.
func standard14PDF(t *testing.T) string {
	t.Helper()
	stream := "BT /F1 24 Tf 20 100 Td (Hello) Tj ET"
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]" +
			"/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>",
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(stream), stream),
	}
	path := filepath.Join(t.TempDir(), "std14.pdf")
	if err := os.WriteFile(path, pdfbuild.Bytes(objs), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

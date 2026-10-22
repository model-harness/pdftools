package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/pdfspec"
)

// These tests run the ocr verb: its flags, its plan, and its output against md's. The pipeline
// under it, with a fake Engine in place of Tesseract, is tested in package pdfspec.

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

// digitalFixture is a page of real body text, chosen as pdfspec's test of the router says.
const digitalFixture = "pymupdf/2201.00069.pdf"

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
	want, err := parseRanges("", len(d.Pages))
	if err != nil {
		t.Fatal(err)
	}
	routed := pdfspec.Route(d, want, ocr.DefaultThreshold, false)

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
	want, err := parseRanges("", len(d.Pages))
	if err != nil {
		t.Fatal(err)
	}
	if err := plan(&sb, d, want, pdfspec.Route(d, want, ocr.DefaultThreshold, false), ocr.DefaultThreshold); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "ocr  page 1\tcoverage   0.0%\t1 block(s)") {
		t.Errorf("plan = %q, want page 1 at 0.0%% over its one block", sb.String())
	}
}

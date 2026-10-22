package pdfspec

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
)

// fixtureDir is the repository's testdata, which cmd/pdfspec's tests read too.
const fixtureDir = "../testdata"

// fixture is the path of a committed test file, or a failed test when it is missing.
func fixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(fixtureDir, name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("reference fixture missing: %s — run `pwsh testdata/fetch.ps1 -Download`", name)
	}
	return p
}

// offline is DefaultOptions with no engine: the package reads no environment variable, so a
// test that runs none says so, and what is installed on a machine cannot change what it measures.
func offline() Options {
	o := DefaultOptions
	o.Tesseract, o.Inferd = "off", "off"
	return o
}

// extractOnly runs the extractor as Open does, so a test can compare the document before
// recognition with the one after.
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

// logged is what fn writes to the log it is handed.
func logged(fn func(io.Writer)) string {
	var b strings.Builder
	fn(&b)
	return b.String()
}

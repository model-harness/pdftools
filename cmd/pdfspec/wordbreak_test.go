package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/extract"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
)

// TestNoWordMarkLeavesTheExtractor: the mark extract writes where a wrap may break a word, U+FDD0,
// is decided before a document leaves the package, in every committed fixture and every corpus
// document present: no span carries one.
func TestNoWordMarkLeavesTheExtractor(t *testing.T) {
	// Every committed fixture but an empty one, zeroLength.pdf, is a PDF this opens, so a test
	// that opened fewer read less than it says, on a clone without the corpus too.
	var paths []string
	want := 0
	for _, name := range fixturePDFs(t) {
		path := filepath.Join(fixtureDir, name)
		paths = append(paths, path)
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			want++
		}
	}
	fixtures := len(paths)
	for _, name := range corpusFiles() {
		paths = append(paths, filepath.Join(corpusDir, name))
	}
	opened := 0
	for i, path := range paths {
		s, err := pcstore.Open(path)
		if err != nil {
			continue
		}
		if i < fixtures {
			opened++
		}
		d, err := extract.New(s, extract.DefaultOptions).Document()
		_ = s.Close()
		if err != nil {
			continue
		}
		for _, p := range d.Pages {
			for _, b := range p.Blocks {
				for _, sp := range b.Spans {
					if strings.ContainsRune(sp.Text, '\uFDD0') {
						t.Errorf("%s page %d: a word mark survives in %q", path, p.Number, sp.Text)
					}
				}
			}
		}
	}
	if opened != want {
		t.Fatalf("opened %d of the %d committed fixtures with content", opened, want)
	}
}

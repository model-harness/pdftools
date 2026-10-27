package main

import (
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
	var paths []string
	for _, name := range fixturePDFs(t) {
		paths = append(paths, filepath.Join(fixtureDir, name))
	}
	for _, name := range corpusFiles() {
		paths = append(paths, filepath.Join(corpusDir, name))
	}
	opened := 0
	for _, path := range paths {
		s, err := pcstore.Open(path)
		if err != nil {
			continue
		}
		opened++
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
	// Every committed fixture is a PDF this opens, so opening none means the test read nothing.
	if opened < len(fixturePDFs(t)) {
		t.Fatalf("opened %d of %d documents", opened, len(paths))
	}
}

package main

import (
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/layout"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
)

// TestBlockLinesDescribeTheirSpans holds doc.Block.Lines to the spans they index, at every
// stage md takes a page through: as extracted, after layout.Hyphens, and after the layout
// passes. Every pass that edits a block's spans has to keep its lines in step or empty them,
// and the one that did neither, the table pass cutting a cell from its row, left lines that
// indexed past the cell's spans: layout.Hyphens panicked on every table fixture.
//
// Over the committed fixtures rather than the corpus, so that it runs on a clone, plus the
// corpus where it is present.
func TestBlockLinesDescribeTheirSpans(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "*", "*.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range corpusFiles() {
		files = append(files, filepath.Join(corpusDir, name))
	}
	var blocks, lines int
	check := func(path, where string, d *doc.Document) {
		for pi := range d.Pages {
			for bi := range d.Pages[pi].Blocks {
				b := &d.Pages[pi].Blocks[bi]
				blocks++
				prev := -1
				for k, ln := range b.Lines {
					lines++
					if ln.Span < 0 || ln.Span >= len(b.Spans) || ln.Offset < 0 || ln.Offset > len(b.Spans[ln.Span].Text) {
						t.Fatalf("%s %s page %d block %d line %d = {Span %d Offset %d}, outside its %d spans",
							filepath.Base(path), where, pi+1, bi, k, ln.Span, ln.Offset, len(b.Spans))
					}
					if txt := b.Spans[ln.Span].Text; ln.Offset < len(txt) && !utf8.RuneStart(txt[ln.Offset]) {
						t.Fatalf("%s %s page %d block %d line %d = {Span %d Offset %d}, inside a character",
							filepath.Base(path), where, pi+1, bi, k, ln.Span, ln.Offset)
					}
					at := 0
					for i := 0; i < ln.Span; i++ {
						at += len(b.Spans[i].Text)
					}
					if at += ln.Offset; at < prev {
						t.Fatalf("%s %s page %d block %d line %d starts before the line above it",
							filepath.Base(path), where, pi+1, bi, k)
					}
					prev = at
				}
			}
		}
	}
	for _, path := range files {
		s, err := pcstore.Open(path)
		if err != nil {
			continue
		}
		d, err := extract.New(s, extract.DefaultOptions).Document()
		_ = s.Close()
		if err != nil {
			continue
		}
		check(path, "extracted", d)
		layout.Hyphens(d)
		check(path, "dehyphenated", d)
		inferRoles(d)
		check(path, "laid out", d)
	}
	if blocks < 1000 || lines < 3000 {
		t.Errorf("walked %d blocks and %d lines; want a real population", blocks, lines)
	}
	t.Logf("%d blocks, %d lines", blocks, lines)
}

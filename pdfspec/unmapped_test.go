package pdfspec

import (
	"bytes"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// TestUnmappedGlyphsAreReportedByPage: the warning names each page with its count, and says
// nothing when every glyph has a value.
func TestUnmappedGlyphsAreReportedByPage(t *testing.T) {
	u := doc.Unmapped
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: []doc.Block{{Spans: []doc.Span{{Text: "a" + u + u}}}}},
		{Number: 2, Blocks: []doc.Block{{Spans: []doc.Span{{Text: "clean"}}}}},
		{Number: 3, Blocks: []doc.Block{{Spans: []doc.Span{{Text: u}}}, {Spans: []doc.Span{{Text: u + " " + u}}}}},
		// A page read by OCR: its [err] are words read below the threshold, reported with OCR.
		{Number: 4, Rasterized: true, Blocks: []doc.Block{{Spans: []doc.Span{{Text: u}}}}},
	}}
	var w bytes.Buffer
	warnIfUnmapped(&w, Unmapped(d))
	want := "warning: 5 glyph(s) on 2 page(s) have no Unicode value in the file and are written [err]\n" +
		"  by page: p1 2, p3 3\n"
	if w.String() != want {
		t.Errorf("warning = %q, want %q", w.String(), want)
	}
	w.Reset()
	warnIfUnmapped(&w, Unmapped(&doc.Document{Pages: d.Pages[1:2]}))
	if w.Len() != 0 {
		t.Errorf("a clean document warned %q", w.String())
	}
}

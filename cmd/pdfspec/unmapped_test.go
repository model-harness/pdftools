package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/pdfspec"
)

// TestAnUnmappedFontIsWrittenErr: the gold set's file whose bitmap font maps no glyph writes an
// [err] for each of its 15 words, and probe counts its 63 glyphs on the page they are on, where
// md used to write nothing at all (ADR 0042).
func TestAnUnmappedFontIsWrittenErr(t *testing.T) {
	path := fixture(t, "reference/type3-unmapped.pdf")
	md, err := convertDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(md)); got != strings.TrimSpace(strings.Repeat("[err] ", 15)) {
		t.Errorf("md = %q, want 15 [err]", got)
	}
	r := probeOne(path, false)
	if r.Unmapped != 63 || len(r.UnmappedPages) != 1 || r.UnmappedPages[0] != (pdfspec.PageErrs{Page: 1, Glyphs: 63}) {
		t.Errorf("probe unmapped = %d on %v, want 63 on page 1", r.Unmapped, r.UnmappedPages)
	}

	// md says so where a reader running it sees it.
	out := filepath.Join(t.TempDir(), "out.md")
	warned := captureStderr(t, func() {
		if err := runMD([]string{"-o", out, path}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(warned, "63 glyph(s) on 1 page(s)") || !strings.Contains(warned, "by page: p1 63") {
		t.Errorf("md warned %q, want the 63 glyphs on page 1", warned)
	}

	// type3font.pdf's bitmap glyphs are named "/0" and "/1", which carry no text: [err] and
	// nothing else, so nothing is invented out of the drawing operators.
	if got := strings.Join(strings.Fields(mdOut(t, fixture(t, "pymupdf/type3font.pdf"))), " "); got != "[err] [err] [err] [err]" {
		t.Errorf("type3font.pdf = %q, want four [err]", got)
	}
}

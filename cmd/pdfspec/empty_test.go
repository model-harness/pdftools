package main

import (
	"os"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// TestWarnIfEmptySpeaksOnlyWhenNothingCameOut covers the signal that a conversion
// produced nothing.
//
// The failure it guards is not a crash, it is a success: an empty .md and exit 0, which
// in a loop over a corpus is indistinguishable from a document that converted cleanly.
// That happened — a pdfTeX file whose fonts carry no /ToUnicode and glyph names outside
// the Adobe glyph list extracted to zero bytes with nothing reporting it.
//
// The two negative cases are the point of the test rather than padding. A warning that
// fires on documents that did convert is noise a user learns to ignore, at which point
// it no longer reports the case it exists for.
func TestWarnIfEmptySpeaksOnlyWhenNothingCameOut(t *testing.T) {
	page := func(spans ...string) doc.Page {
		p := doc.Page{Number: 1}
		for _, s := range spans {
			p.Blocks = append(p.Blocks, doc.Block{Spans: []doc.Span{{Text: s}}})
		}
		return p
	}

	for _, tc := range []struct {
		name string
		d    doc.Document
		warn bool
	}{
		{"text on the page", doc.Document{Pages: []doc.Page{page("Hello world.")}}, false},
		{"no blocks at all", doc.Document{Pages: []doc.Page{page()}}, true},
		// Whitespace is not text. A page of positioned spaces is what a font with no
		// decodable glyphs leaves behind once the undecodable ones are dropped, so
		// comparing against "" rather than trimming would miss the real case.
		{"only whitespace", doc.Document{Pages: []doc.Page{page("   ", "\n\t")}}, true},
		// One page of many carrying text is a document that converted. Partial loss is
		// a real problem and not this one: reporting it needs a per-page threshold,
		// which is what the OCR router already does with coverage.
		{"one page empty of two", doc.Document{Pages: []doc.Page{page(), page("Body.")}}, false},
		// No pages is a different failure — an unreadable or zero-page file — and the
		// store reports that. Warning here would attach a font-encoding hint to it.
		{"no pages", doc.Document{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := captureStderr(t, func() { warnIfEmpty(&tc.d) })
			if warned := strings.Contains(got, "warning:"); warned != tc.warn {
				t.Errorf("warned = %v, want %v; stderr was %q", warned, tc.warn, got)
			}
			if !tc.warn {
				return
			}
			// The warning has to say what to do next, or it reports a dead end. Both
			// verbs named here recover something a bare "no text" does not.
			for _, want := range []string{"probe", "ocr"} {
				if !strings.Contains(got, want) {
					t.Errorf("the warning does not mention %q: %q", want, got)
				}
			}
		})
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was written.
//
// A pipe rather than swapping in a bytes.Buffer, because the code under test writes to
// os.Stderr directly — which is the behavior worth testing, since a warning that goes
// to stdout would land inside the Markdown it is warning about.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	// Read concurrently: a pipe's buffer is finite, and a writer that fills it before
	// anyone reads would deadlock rather than fail.
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 512)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()

	fn()

	os.Stderr = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := <-done
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

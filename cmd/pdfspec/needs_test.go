package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/pdfspec"
)

// stdout runs fn and returns what it wrote to os.Stdout.
func stdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	return <-done
}

// TestProbeSaysWhatEachPageNeeds: probe names the engines md would use and what each page would
// ask of them, in its text and in its JSON.
func TestProbeSaysWhatEachPageNeeds(t *testing.T) {
	path := fixture(t, "reference/type3-words.pdf")
	if r := probeOne(path, false); len(r.Needs) != 1 || r.Needs[0] != (pdfspec.PageNeed{Page: 1, Unmapped: 42}) {
		t.Errorf("needs %+v, want page 1's 42 glyphs", r.Needs)
	}
	text := stdout(t, func() {
		if err := probe([]string{path}, false, false); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"  engines     tesseract off; inferd off\n", "  needs       glyphs with no value: p1 42\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("probe wrote %q, want %q", text, want)
		}
	}
	js := stdout(t, func() {
		if err := probe([]string{path}, true, false); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(js, `"tesseract": "off"`) || !strings.Contains(js, `"unmapped": 42`) || !strings.Contains(js, `"needs": [`) {
		t.Errorf("probe -json wrote %s", js)
	}
}

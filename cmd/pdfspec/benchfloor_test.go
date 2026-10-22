package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// benchSlack is half the last printed digit. The floors are the scores pdfspec bench
// printed, to three places, so a score that rounds to its floor meets it.
const benchSlack = 0.0005

// benchFloors is every reference fixture's score on each dimension, as measured when the
// fixture went in or when a change last raised it (ADR 0029). TestBenchmarkFloors fails
// on any score below its floor, and CER above its ceiling, so no change can make a
// fixture worse without saying so here.
//
// A floor only goes up. When a goal's change improves a fixture, the test logs the new
// score, and the change raises the floor to it in the same commit, so the improvement is
// held from then on. Lowering one needs a reason in the ADR that the change is
// documented in.
var benchFloors = map[string]benchFigures{
	"chrome-article":           {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"chrome-report":            {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"clauses":                  {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"footnotes":                {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"fractions":                {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"headings":                 {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"hyphenation":              {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"image-kinds":              {cer: 0.606, recall: 0.372, precision: 1.000, order: 1.000, blocks: 0.769, headings: 1.000, tables: 0.000, style: 1.000},
	"image":                    {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"lists":                    {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"metadata":                 {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"paragraphs":               {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"running-headers":          {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"scan-paragraphs":          {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 1.000, style: 1.000},
	"scan-table":               {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 0.000, style: 1.000},
	"scan-two-column":          {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 0.000, tables: 1.000, style: 1.000},
	"scan-unnumbered-headings": {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 0.000, tables: 1.000, style: 1.000},
	"strikethrough":            {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"superscripts":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"table-across-pages":       {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"table-merged":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"table-unruled":            {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"table":                    {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"tagged-lists":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"tagged-table":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"text-styles":              {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 1.000, style: 1.000},
	"three-column":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"two-column-ragged":        {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"two-column":               {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"type3-unmapped":           {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 1.000, style: 1.000},
	"type3-words":              {cer: 0.057, recall: 0.949, precision: 0.984, order: 1.000, blocks: 0.667, headings: 1.000, tables: 1.000, style: 1.000},
	"unnumbered-headings":      {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"xelatex-article":          {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 0.800, tables: 1.000, style: 0.800},
}

// benchTargets is what v1 needs, as means over the reference fixtures (ADR 0029). Each was
// enforced from the commit that closed its goal; since v1.0.0 (#13), every one fails the build.
var benchTargets = benchFigures{
	cer: 0.03, recall: 0.97, precision: 0.97, order: 0.98,
	blocks: 0.90, headings: 0.90, tables: 0.85, style: 0.85,
}

// benchNeedsOCR names the fixtures whose content is only in pixels: the scans, the page of
// images, and the pages of bitmap glyphs with no Unicode value.
var benchNeedsOCR = map[string]bool{
	"scan-paragraphs": true, "scan-table": true, "scan-two-column": true, "scan-unnumbered-headings": true,
	"image-kinds": true, "type3-unmapped": true, "type3-words": true,
}

func TestBenchmarkFloors(t *testing.T) {
	rows, err := benchRows(filepath.Join("..", "..", "testdata", "reference"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.name] = true
		f, ok := benchFloors[r.name]
		if !ok {
			t.Errorf("%s has no floor: add its measured scores to benchFloors", r.name)
			continue
		}
		s := r.s
		if s.CER > f.cer+benchSlack {
			t.Errorf("%s: CER %.4f, above its ceiling %.3f", r.name, s.CER, f.cer)
		} else if s.CER < f.cer-benchSlack {
			t.Logf("%s: CER %.4f, below its ceiling %.3f: lower the ceiling", r.name, s.CER, f.cer)
		}
		for _, d := range []struct {
			name       string
			got, floor float64
		}{
			{"recall", s.Recall, f.recall},
			{"precision", s.Precision, f.precision},
			{"order", s.Order, f.order},
			{"blocks", s.Blocks, f.blocks},
			{"headings", s.Headings, f.headings},
			{"tables", s.Tables, f.tables},
			{"style", s.Style, f.style},
		} {
			if d.got < d.floor-benchSlack {
				t.Errorf("%s: %s %.4f, below its floor %.3f", r.name, d.name, d.got, d.floor)
			} else if d.got > d.floor+benchSlack {
				t.Logf("%s: %s %.4f, above its floor %.3f: raise the floor", r.name, d.name, d.got, d.floor)
			}
		}
	}
	// A floor with no fixture means a PDF or its gold file went missing, which benchRows
	// skips without complaint.
	for name := range benchFloors {
		if !seen[name] {
			t.Errorf("%s has a floor and no fixture", name)
		}
	}
}

func TestBenchmarkTargets(t *testing.T) {
	rows, err := benchRows(filepath.Join("..", "..", "testdata", "reference"))
	if err != nil {
		t.Fatal(err)
	}
	// A fixture whose content is only in pixels is measured where an engine reads pixels: by
	// TestBenchmarkWithTesseract. Here, as in a clone, none does, and a scan or an image of a
	// table scores 0 on what nothing here can read (ADR 0046).
	var read []benchRow
	for _, r := range rows {
		if !benchNeedsOCR[r.name] {
			read = append(read, r)
		}
	}
	m := benchMeans(read)
	t.Logf("means: %+v", m)
	for _, miss := range benchMisses(m, benchTargets) {
		t.Error(miss)
	}
}

// benchMisses names each dimension of m that misses its target: CER above it, the rest below.
func benchMisses(m, target benchFigures) []string {
	var out []string
	for _, d := range []struct {
		name           string
		got, target    float64
		higherIsBetter bool
	}{
		{"cer", m.cer, target.cer, false},
		{"recall", m.recall, target.recall, true},
		{"precision", m.precision, target.precision, true},
		{"order", m.order, target.order, true},
		{"blocks", m.blocks, target.blocks, true},
		{"headings", m.headings, target.headings, true},
		{"tables", m.tables, target.tables, true},
		{"style", m.style, target.style, true},
	} {
		met := d.got <= d.target
		if d.higherIsBetter {
			met = d.got >= d.target
		}
		if !met {
			out = append(out, fmt.Sprintf("%s: %.3f misses its v1 target %.2f", d.name, d.got, d.target))
		}
	}
	return out
}

// TestBenchMissesNamesEachDimension: every dimension is compared with its own target, in its
// own direction, and one exactly at its target meets it.
func TestBenchMissesNamesEachDimension(t *testing.T) {
	at := benchFigures{cer: 0.03, recall: 0.97, precision: 0.97, order: 0.98, blocks: 0.9, headings: 0.9, tables: 0.85, style: 0.85}
	if got := benchMisses(at, at); len(got) != 0 {
		t.Errorf("at the targets: %q", got)
	}
	worse := benchFigures{cer: 0.04, recall: 0.96, precision: 0.96, order: 0.97, blocks: 0.8, headings: 0.8, tables: 0.8, style: 0.8}
	got := benchMisses(worse, at)
	if len(got) != 8 {
		t.Fatalf("%d misses, want all 8: %q", len(got), got)
	}
	for i, name := range []string{"cer", "recall", "precision", "order", "blocks", "headings", "tables", "style"} {
		if !strings.HasPrefix(got[i], name+":") {
			t.Errorf("miss %d is %q, want %s", i, got[i], name)
		}
	}
}

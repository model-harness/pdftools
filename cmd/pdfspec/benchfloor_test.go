package main

import (
	"path/filepath"
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
	"chrome-article":           {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 0.571},
	"clauses":                  {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"footnotes":                {cer: 0.019, recall: 0.936, precision: 0.917, order: 1.000, blocks: 0.571, headings: 1.000, tables: 1.000, style: 1.000},
	"fractions":                {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"headings":                 {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"hyphenation":              {cer: 0.012, recall: 0.921, precision: 0.854, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"image":                    {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"lists":                    {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"metadata":                 {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"paragraphs":               {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"running-headers":          {cer: 0.136, recall: 1.000, precision: 0.882, order: 1.000, blocks: 0.333, headings: 1.000, tables: 1.000, style: 1.000},
	"scan-paragraphs":          {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 1.000, style: 1.000},
	"scan-table":               {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 0.000, style: 1.000},
	"scan-two-column":          {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 0.000, tables: 1.000, style: 1.000},
	"scan-unnumbered-headings": {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 0.000, tables: 1.000, style: 1.000},
	"strikethrough":            {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 0.000},
	"superscripts":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"table-across-pages":       {cer: 0.149, recall: 1.000, precision: 0.900, order: 1.000, blocks: 0.000, headings: 1.000, tables: 0.632, style: 1.000},
	"table-merged":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 0.000, style: 1.000},
	"table-unruled":            {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 0.000, style: 1.000},
	"table":                    {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"tagged-lists":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"tagged-table":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"text-styles":              {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 1.000, style: 1.000},
	"three-column":             {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"two-column-ragged":        {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"two-column":               {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"type3-unmapped":           {cer: 1.000, recall: 0.000, precision: 0.000, order: 1.000, blocks: 0.000, headings: 1.000, tables: 1.000, style: 1.000},
	"unnumbered-headings":      {cer: 0.000, recall: 1.000, precision: 1.000, order: 1.000, blocks: 1.000, headings: 1.000, tables: 1.000, style: 1.000},
	"xelatex-article":          {cer: 0.008, recall: 0.979, precision: 0.979, order: 1.000, blocks: 0.857, headings: 0.500, tables: 1.000, style: 0.667},
}

// benchTargets is what v1 needs, as means over the reference fixtures (ADR 0029). A
// dimension's target is enforced from the commit that closes the goal it belongs to, by
// adding it to benchEnforced. Until then TestBenchmarkTargets reports the gap.
var benchTargets = benchFigures{
	cer: 0.03, recall: 0.97, precision: 0.97, order: 0.98,
	blocks: 0.90, headings: 0.90, tables: 0.85, style: 0.85,
}

// benchEnforced names the dimensions whose v1 target now fails the build, each from the
// goal that closed it.
var benchEnforced = map[string]bool{
	"order": true, // #3, ADR 0030
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
	m := benchMeans(rows)
	for _, d := range []struct {
		name           string
		got, target    float64
		higherIsBetter bool
	}{
		{"cer", m.cer, benchTargets.cer, false},
		{"recall", m.recall, benchTargets.recall, true},
		{"precision", m.precision, benchTargets.precision, true},
		{"order", m.order, benchTargets.order, true},
		{"blocks", m.blocks, benchTargets.blocks, true},
		{"headings", m.headings, benchTargets.headings, true},
		{"tables", m.tables, benchTargets.tables, true},
		{"style", m.style, benchTargets.style, true},
	} {
		met := d.got <= d.target
		if d.higherIsBetter {
			met = d.got >= d.target
		}
		switch {
		case met:
			t.Logf("%s: %.3f meets its v1 target %.2f", d.name, d.got, d.target)
		case benchEnforced[d.name]:
			t.Errorf("%s: %.3f misses its v1 target %.2f", d.name, d.got, d.target)
		default:
			t.Logf("%s: %.3f, v1 target %.2f, not yet enforced", d.name, d.got, d.target)
		}
	}
}

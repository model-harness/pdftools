package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/bench"
)

// TestBenchScoresReferenceFixtures runs the bench verb's own path — benchRows,
// convertDefault, bench.Score — against the reference corpus and checks two things:
// that every fixture produces a row, and that headings.pdf, whose Markdown is an exact
// match for its gold file (TestReferenceExactMatch pins that down for the md verb
// itself), scores a perfect CER and Blocks here too. A regression in either path —
// bench's own scoring, or convertDefault drifting from what md actually emits — would
// show up as this no longer being 0 and 1.
func TestBenchScoresReferenceFixtures(t *testing.T) {
	rows, err := benchRows("../../testdata/reference")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows: no X.pdf in testdata/reference matched an X.gold.md")
	}

	var found bool
	for _, r := range rows {
		if r.name != "headings" {
			continue
		}
		found = true
		if r.s.CER != 0 {
			t.Errorf("headings CER = %v, want 0", r.s.CER)
		}
		if r.s.Blocks != 1 {
			t.Errorf("headings Blocks = %v, want 1", r.s.Blocks)
		}
	}
	if !found {
		t.Error("no row for the headings fixture")
	}

	// metadata.frontmatter.gold.md belongs to -frontmatter, not to a PDF of its own,
	// and must not turn into a row.
	for _, r := range rows {
		if strings.Contains(r.name, ".") {
			t.Errorf("row %q should have been skipped as a non-default gold file", r.name)
		}
	}

	var buf bytes.Buffer
	printBench(&buf, rows)
	out := buf.String()
	if !strings.Contains(out, "headings") {
		t.Error("printBench output does not mention the headings fixture")
	}
	if !strings.Contains(out, "mean") {
		t.Error("printBench output has no mean row")
	}
}

// The mean of headings, tables and style leaves out a fixture with none of them, whose
// F1 is a vacuous 1. One measured 0.5 beside one vacuous 1 is a mean of 0.5, not 0.75.
// With every fixture vacuous there is nothing to average, and the mean is 0.
func TestBenchMeansLeaveOutVacuousFixtures(t *testing.T) {
	vacuous := bench.Scores{Headings: 1, Tables: 1, Style: 1}
	measured := bench.Scores{Headings: 0.5, Tables: 0.5, Style: 0.5, RefHeadings: 2, RefCells: 4, RefSpans: 3}
	m := benchMeans([]benchRow{{name: "v", s: vacuous}, {name: "m", s: measured}})
	if m.headings != 0.5 || m.tables != 0.5 || m.style != 0.5 {
		t.Errorf("means = %v %v %v, want 0.5 each", m.headings, m.tables, m.style)
	}
	m = benchMeans([]benchRow{{name: "v", s: vacuous}})
	if m.headings != 0 || m.tables != 0 || m.style != 0 {
		t.Errorf("all-vacuous means = %v %v %v, want 0 each", m.headings, m.tables, m.style)
	}
}

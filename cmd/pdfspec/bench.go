package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/model-harness/pdftools/bench"
)

func runBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec bench <dir>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("bench takes exactly one fixture directory")
	}

	rows, err := benchRows(fs.Arg(0))
	if err != nil {
		return err
	}
	printBench(os.Stdout, rows)
	return nil
}

// benchRow is one fixture's score, named for the table printBench writes it into.
type benchRow struct {
	name string
	s    bench.Scores
}

// benchRows scores every X.pdf in dir against its X.gold.md, converting each PDF with
// convertDefault — the same path "pdfspec md" takes with no flags — so this measures
// the CLI's actual default output.
func benchRows(dir string) ([]benchRow, error) {
	golds, err := filepath.Glob(filepath.Join(dir, "*.gold.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(golds)

	var rows []benchRow
	for _, g := range golds {
		base := strings.TrimSuffix(filepath.Base(g), ".gold.md")
		// A gold file with no PDF of its name is skipped. That is how
		// metadata.frontmatter.gold.md, the gold for "md -frontmatter", stays out: this
		// command scores the default output only.
		pdf := filepath.Join(dir, base+".pdf")
		if _, err := os.Stat(pdf); err != nil {
			continue
		}
		ref, err := os.ReadFile(g) // #nosec G304 -- dir is the caller's own argument
		if err != nil {
			return nil, err
		}
		got, err := convertDefault(pdf)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", base, err)
		}
		rows = append(rows, benchRow{name: base, s: bench.Score(ref, got)})
	}
	return rows, nil
}

// printBench writes one row per fixture and the mean row beneath it.
func printBench(w io.Writer, rows []benchRow) {
	fmt.Fprintf(w, "%-16s %6s %6s %6s %6s %6s %6s %6s %6s %6s %6s\n",
		"fixture", "cer", "recall", "prec", "order", "blocks", "head", "table", "style", "errs", "images")
	for _, r := range rows {
		s := r.s
		fmt.Fprintf(w, "%-16s %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f %6d %6d\n",
			r.name, s.CER, s.Recall, s.Precision, s.Order, s.Blocks, s.Headings, s.Tables, s.Style, s.Errs, s.Images)
	}
	if len(rows) == 0 {
		return
	}
	m := benchMeans(rows)
	fmt.Fprintf(w, "%-16s %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f %6.3f\n",
		"mean", m.cer, m.recall, m.precision, m.order, m.blocks, m.headings, m.tables, m.style, m.errs, m.images)
}

// benchFigures is one line of the bench table as numbers: a fixture's floors, the means,
// or the v1 targets.
type benchFigures struct {
	cer, recall, precision, order, blocks, headings, tables, style, errs, images float64
}

// benchMeans is the mean row. A dimension whose reference count is zero for a fixture,
// no table in it, say, is left out of that dimension's mean rather than counted as the
// 1.0 its vacuous F1 reports, which would reward fixtures for not exercising it at all.
func benchMeans(rows []benchRow) benchFigures {
	var m benchFigures
	var headN, tableN, styleN int
	for _, r := range rows {
		s := r.s
		m.cer += s.CER
		m.recall += s.Recall
		m.precision += s.Precision
		m.order += s.Order
		m.blocks += s.Blocks
		m.errs += float64(s.Errs)
		m.images += float64(s.Images)
		if s.RefHeadings > 0 {
			m.headings += s.Headings
			headN++
		}
		if s.RefCells > 0 {
			m.tables += s.Tables
			tableN++
		}
		if s.RefSpans > 0 {
			m.style += s.Style
			styleN++
		}
	}
	n := len(rows)
	m.cer, m.recall, m.precision = meanOver(m.cer, n), meanOver(m.recall, n), meanOver(m.precision, n)
	m.order, m.blocks = meanOver(m.order, n), meanOver(m.blocks, n)
	m.errs, m.images = meanOver(m.errs, n), meanOver(m.images, n)
	m.headings, m.tables, m.style = meanOver(m.headings, headN), meanOver(m.tables, tableN), meanOver(m.style, styleN)
	return m
}

// meanOver is sum/n, or 0 when n is 0 — every fixture in the run was vacuous for that
// dimension, so there is nothing to average and no reference count to divide by.
func meanOver(sum float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

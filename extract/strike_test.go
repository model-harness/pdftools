package extract

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// struckWords draws "keep gone keep" in 12pt Helvetica on a baseline at y=700, the middle word
// from x=40 to 66.688, and a line from x0 to x1 at y, w wide; and returns which spans are struck.
func struckWords(t *testing.T, x0, x1, y, w float64) string {
	t.Helper()
	stream := "BT /F1 12 Tf 10 700 Td (keep) Tj 30 0 Td (gone) Tj 40 0 Td (keep) Tj ET " +
		fmt.Sprintf("%g w %g %g m %g %g l S", w, x0, y, x1, y)
	p := extractPage(t, stream)
	var out []string
	for _, b := range p.Blocks {
		for _, sp := range b.Spans {
			if sp.Style.Strike {
				out = append(out, strings.TrimSpace(sp.Text))
			}
		}
	}
	return strings.Join(out, ",")
}

// TestAStrikeRunsThroughTheWordsItStrikes: a line through the middle of a word strikes it and no
// other, within the band a strike runs in and no thicker than one, and drawn for the text, not
// across it. Each bound is tried a hundredth of a point either side, since 0.15 of 12pt has no
// exact binary value to sit on.
func TestAStrikeRunsThroughTheWordsItStrikes(t *testing.T) {
	for _, c := range []struct {
		name         string
		x0, x1, y, w float64
		want         string
	}{
		{"through the word", 40, 66.688, 703.6, 0.5, "gone"},
		{"just inside the band's foot", 40, 66.688, 701.81, 0.5, "gone"},
		{"just below it", 40, 66.688, 701.79, 0.5, ""},
		{"just inside its top", 40, 66.688, 707.19, 0.5, "gone"},
		{"just above it", 40, 66.688, 707.21, 0.5, ""},
		{"an underline", 40, 66.688, 698.5, 0.5, ""},
		{"just thin enough", 40, 66.688, 703.6, 1.79, "gone"},
		{"just too thick", 40, 66.688, 703.6, 1.81, ""},
		{"over half the word", 40, 54, 703.6, 0.5, "gone"},
		{"under half of it", 40, 53, 703.6, 0.5, ""},
		{"across the measure", 0, 400, 703.6, 0.5, ""},
		{"from before the text", 2, 66.688, 703.6, 0.5, ""},
		{"to past its end", 40, 120, 703.6, 0.5, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := struckWords(t, c.x0, c.x1, c.y, c.w); got != c.want {
				t.Errorf("struck %q, want %q", got, c.want)
			}
		})
	}
}

// TestAStrikeKeepsTheCutsAroundIt: the words either side of a strike keep the gaps between them,
// which the gutter pass reads after this one.
func TestAStrikeKeepsTheCutsAroundIt(t *testing.T) {
	stream := "BT /F1 12 Tf 10 700 Td (a) Tj 30 0 Td (b) Tj 30 0 Td (gone) Tj 40 0 Td (c) Tj 30 0 Td (d) Tj ET " +
		"0.5 w 70 703.6 m 96.688 703.6 l S"
	p := extractPage(t, stream)
	var got []string
	for _, b := range p.Blocks {
		for _, sp := range b.Spans {
			got = append(got, fmt.Sprintf("%q/%v", sp.Text, sp.Style.Strike))
		}
	}
	if want := `"a b "/false "gone "/true "c d"/false`; strings.Join(got, " ") != want {
		t.Errorf("spans = %s, want %s", strings.Join(got, " "), want)
	}
}

// TestAStruckCellKeepsItsTable: an unruled table with a cell struck out is still a table, since
// the strike's pieces keep the gaps the gutter pass reads after it, each at its own offset.
func TestAStruckCellKeepsItsTable(t *testing.T) {
	stream := grid("F1", gridX, []string{"Station", "Region", "Rainfall"}, []string{"Ashford", "South", "712"},
		[]string{"Brampton", "North", "1048"}) + " 0.5 w 10 689.6 m 52 689.6 l S"
	text, n := cellsOf(t, stream)
	if n != 2 {
		t.Errorf("gutters = %d, want 2", n)
	}
	if want := "Station | Region | Rainfall | Ashford | South | 712 | Brampton | North | 1048"; text != want {
		t.Errorf("spans = %q\nwant %q", text, want)
	}
}

// TestAStrikeReadsHorizontalLinesOnly: a rotated line's coordinates are along its own axis, so
// a horizontal rule's height above it means nothing, and nothing in it is struck.
func TestAStrikeReadsHorizontalLinesOnly(t *testing.T) {
	r := &run{
		lines: []line{{orient: 6, frags: []frag{{text: []byte("gone"), along0: 40, along1: 66, cross: 700, height: 12, space: 3}}}},
		rules: []doc.Rule{{Pos: 703.6, From: 40, To: 66, Width: 0.5}},
	}
	r.strikeThrough()
	if r.lines[0].frags[0].style.Strike {
		t.Error("a rotated line was struck")
	}
}

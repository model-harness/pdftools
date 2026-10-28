package extract

import (
	"strconv"
	"testing"
)

// at9 is a line of 9-point text at x, its baseline at y.
func at9(x, y int, text string) string {
	return "BT /F1 9 Tf " + strconv.Itoa(x) + " " + strconv.Itoa(y) + " Td (" + text + ") Tj ET\n"
}

// Lines of 9-point text: one that runs to the column's edge, as a wrapped line does; one a prose
// line's width that stops short of it; and a table's row, its two cells apart.
const (
	full  = "the records are kept for a year and read once a day by the office"
	short = "the records are kept for a year and read once a day by"
)

func row9(y int) string {
	return at9(72, y, "Name") + at9(300, y, "the value in the second column of the row")
}

// TestLooseLeadingKeepsAParagraph: a page that sets its type on a loose leading, 9-point type 14
// points apart as half of opendataloader-bench's pages do, keeps a paragraph's lines one block,
// since the step between them is the page's own line pitch, taken three times or more, and breaks
// it where a step is larger by half a line, though the line before is full (ADR 0057).
func TestLooseLeadingKeepsAParagraph(t *testing.T) {
	for _, c := range []struct {
		name   string
		stream string
		want   int
	}{
		{"two paragraphs", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full) + at9(72, 650, full) + at9(72, 636, full), 2},
		{"a step half a line longer", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full) + at9(72, 653, full) + at9(72, 639, full), 2},
		{"three lines are two steps, no pitch", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full), 3},
	} {
		if got := len(extractPage(t, c.stream).Blocks); got != c.want {
			t.Errorf("%s: %d blocks, want %d", c.name, got, c.want)
		}
	}
	// Two steps taken as often: the shorter is the pitch, a line's, and the longer a paragraph's.
	p := extractPage(t, at9(72, 700, full)+at9(72, 686, full)+at9(72, 672, full)+at9(72, 658, full)+
		at9(72, 638, full)+at9(72, 618, full)+at9(72, 598, full))
	if len(p.Blocks) == 0 || len(p.Blocks[0].Lines) != 4 {
		t.Errorf("a tie: first block %d lines, want the four 14 points apart", len(p.Blocks[0].Lines))
	}
}

// TestLooseLeadingIsNotEveryRegularStep: the page's pitch continues a paragraph only after a full
// prose line, at the margin, in a size the page sets no tighter: not after a list's short items, a
// line short of the column's edge, or a table's row, nor before a row or a caption set further in;
// and only a step in that size, horizontal, within three lines, counts toward the pitch.
func TestLooseLeadingIsNotEveryRegularStep(t *testing.T) {
	for _, c := range []struct {
		name   string
		stream string
		want   int
	}{
		{"a list's items", at9(72, 700, "First item.") + at9(72, 686, "Second item.") + at9(72, 672, "Third item.") + at9(72, 658, "Fourth item."), 4},
		{"a line short of the edge", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, short) + at9(72, 658, full) + at9(72, 644, short), 2},
		{"a table's rows", row9(700) + row9(686) + row9(672) + row9(658), 4},
		{"a row after prose", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full) + row9(658), 2},
		{"prose after a row", row9(700) + at9(72, 686, full) + at9(72, 672, full) + at9(72, 658, full), 2},
		{"a caption set in under a full line", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full) + at9(72, 658, full) +
			at9(120, 644, "Table 2: the records"), 2},
		{"a page set tight", at9(72, 700, full) + at9(72, 690, full) + at9(72, 680, full) + at9(72, 670, full) +
			at9(72, 656, full) + at9(72, 642, full) + at9(72, 628, full) + at9(72, 614, full), 5},
		{"steps beyond three lines", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full) + at9(72, 642, full) +
			at9(72, 612, full) + at9(72, 582, full), 6},
		{"a step into another size", at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full) +
			"BT /F1 12 Tf 72 658 Td (A heading at twelve points) Tj ET\n", 4},
		// Turned 90 degrees a line's cross is -x, so these step down by 14 from one to the next.
		{"steps of a column set sideways", "BT /F1 9 Tf 0 1 -1 0 458 100 Tm (up) Tj ET BT /F1 9 Tf 0 1 -1 0 472 100 Tm (up) Tj ET " +
			"BT /F1 9 Tf 0 1 -1 0 486 100 Tm (up) Tj ET BT /F1 9 Tf 0 1 -1 0 500 100 Tm (up) Tj ET\n" +
			at9(72, 700, full) + at9(72, 686, full) + at9(72, 672, full), 7},
	} {
		if got := len(extractPage(t, c.stream).Blocks); got != c.want {
			t.Errorf("%s: %d blocks, want %d", c.name, got, c.want)
		}
	}
}

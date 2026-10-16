package extract

import (
	"fmt"
	"strings"
	"testing"
)

// at is one line of Helvetica: its text, its position, and its size in points (12 when
// zero). A space is 3.336pt at 12pt, and lines 12pt apart are one line height apart.
type at struct {
	text string
	x, y float64
	size float64
}

func page(lines ...at) string {
	var b strings.Builder
	b.WriteString("BT ")
	for _, l := range lines {
		size := l.size
		if size == 0 {
			size = 12
		}
		fmt.Fprintf(&b, "/F1 %g Tf 1 0 0 1 %g %g Tm (%s) Tj ", size, l.x, l.y, l.text)
	}
	b.WriteString("ET")
	return b.String()
}

func texts(t *testing.T, stream string) []string {
	t.Helper()
	var out []string
	for _, b := range extractPage(t, stream).Blocks {
		out = append(out, b.Text())
	}
	return out
}

func wantBlocks(t *testing.T, stream string, n int, why string) {
	t.Helper()
	if got := texts(t, stream); len(got) != n {
		t.Errorf("%d blocks, want %d: %s\n%q", len(got), n, why, got)
	}
}

// A left column of three lines at x=10, the last at the foot of the page's text, every
// line the same words so the last runs to the column's right edge as a wrapped justified
// line does.
var fullColumn = []at{
	{"the same words on each line", 10, 700, 0},
	{"the same words on each line", 10, 688, 0},
	{"the same words on each line", 10, 676, 0},
}

// The same column whose last line stops well short of the edge.
var shortColumn = []at{
	{"the same words on each line", 10, 700, 0},
	{"the same words on each line", 10, 688, 0},
	{"short", 10, 676, 0},
}

func with(col []at, more ...at) string { return page(append(append([]at{}, col...), more...)...) }

// TestAColumnBreakCanFallInsideAParagraph is ADR 0030's rule, condition by condition. The
// page's text goes down the left column and steps back up to the top of a right column at
// x=300, and whether the line there carries on the left column's last paragraph is what
// each case varies.
func TestAColumnBreakCanFallInsideAParagraph(t *testing.T) {
	t.Run("a full line and a flush one continue", func(t *testing.T) {
		wantBlocks(t, with(fullColumn, at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0}), 1,
			"the left column's last line is full and the right column's first is flush")
	})
	t.Run("an indent opens a paragraph", func(t *testing.T) {
		wantBlocks(t, with(fullColumn, at{"Next column starts here", 304, 700, 0}, at{"and goes on", 300, 688, 0}), 2,
			"the right column's first line is 1.2 space widths right of its margin")
	})
	t.Run("under the smallest indent is flush", func(t *testing.T) {
		wantBlocks(t, with(fullColumn, at{"Next column starts here", 303, 700, 0}, at{"and goes on", 300, 688, 0}), 1,
			"0.9 space widths is less than IndentFrac")
	})
	t.Run("a short line ends the paragraph", func(t *testing.T) {
		wantBlocks(t, with(shortColumn, at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0}), 2,
			"the left column's last line stops short and the right column's starts with a capital")
	})
	// The last line's words, moved left so it ends short of the column's right edge by
	// half a space width, which is full, and by one and a half, which is not.
	t.Run("half a space short is full", func(t *testing.T) {
		col := append(append([]at{}, fullColumn[:2]...), at{"the same words on each line", 8.332, 676, 0})
		wantBlocks(t, with(col, at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0}), 1,
			"0.5 space widths is within lineFull")
	})
	t.Run("a space and a half short is not", func(t *testing.T) {
		col := append(append([]at{}, fullColumn[:2]...), at{"the same words on each line", 4.996, 676, 0})
		wantBlocks(t, with(col, at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0}), 2,
			"1.5 space widths is past lineFull")
	})
	t.Run("lower case carries a paragraph past a short line", func(t *testing.T) {
		wantBlocks(t, with(shortColumn, at{"next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0}), 1,
			"a line that starts lower case carries on a sentence")
	})
	t.Run("at the foot of the text, to within a line", func(t *testing.T) {
		wantBlocks(t, with(fullColumn, at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0},
			at{"and on", 300, 676, 0}, at{"and on", 300, 664, 0}), 1,
			"the right column ends one line height below the left one's last line")
	})
	t.Run("above the foot of the text is a cell, not a column", func(t *testing.T) {
		wantBlocks(t, with(fullColumn, at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0},
			at{"and on", 300, 676, 0}, at{"and on", 300, 663, 0}), 2,
			"the right column ends 13pt below the left one's last line, more than its 12pt line")
	})
	t.Run("a change of size is a break", func(t *testing.T) {
		wantBlocks(t, with(fullColumn, at{"Next column starts here", 300, 700, 14}, at{"and goes on", 300, 686, 14}), 2,
			"14pt against 12pt is past SizeFrac")
	})
	t.Run("a column of one line has no margin to be flush with", func(t *testing.T) {
		wantBlocks(t, with(fullColumn, at{"next column starts here", 300, 700, 0}), 2,
			"one line is its own left edge, which says nothing")
	})
	t.Run("a column of one line has no right edge to be full against", func(t *testing.T) {
		wantBlocks(t, page(at{"the same words on each line", 10, 676, 0},
			at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0}), 2,
			"a one-line left column is its own right edge")
	})
}

// TestTheParagraphIndentCarriesOverAColumnBreak is the other half of the rule: a column
// that opens flush has an indent of its own of 0, so the next paragraph's indent must be
// matched against the one the page has been using. The left column's paragraph opens
// 10pt in, three space widths, and so does the right column's second paragraph.
func TestTheParagraphIndentCarriesOverAColumnBreak(t *testing.T) {
	indented := []at{
		{"an indented first line", 20, 700, 0},
		{"the same words on each line", 10, 688, 0},
		{"short", 10, 676, 0},
	}
	t.Run("into the paragraph the break continued", func(t *testing.T) {
		wantBlocks(t, with(indented, at{"carried on here", 300, 700, 0}, at{"more of it", 300, 688, 0},
			at{"Next paragraph", 310, 676, 0}, at{"and its second line", 300, 664, 0}), 2,
			"the right column's 10pt indent is the left column's")
	})
	// The margin is known from the break's own line, so a paragraph that starts on the
	// very next line is found too.
	t.Run("on the line after the break", func(t *testing.T) {
		wantBlocks(t, with(indented, at{"carried on here", 300, 700, 0},
			at{"Next paragraph", 310, 688, 0}, at{"and its second line", 300, 676, 0}), 2,
			"the second line of the right column is indented as the page's paragraphs are")
	})
	t.Run("into a paragraph the column opened", func(t *testing.T) {
		wantBlocks(t, with(indented, at{"Something new", 300, 700, 0}, at{"more of it", 300, 688, 0},
			at{"Next paragraph", 310, 676, 0}, at{"and its second line", 300, 664, 0}), 3,
			"the column opens a new paragraph flush, and its indent is still the page's")
	})
}

// TestADroppedArtifactIsNoPartOfTheColumns is a folio drawn last, at the foot of the right
// column. Counted, it would move that column's left edge out to 280, so the line at 300
// would read as indented, and it would be the page's lowest text, so the left column
// would not reach the foot.
func TestADroppedArtifactIsNoPartOfTheColumns(t *testing.T) {
	stream := with(fullColumn, at{"Next column starts here", 300, 700, 0}, at{"and goes on", 300, 688, 0}) +
		" /Artifact BMC BT /F1 12 Tf 1 0 0 1 280 640 Tm (12) Tj ET EMC"
	wantBlocks(t, stream, 1, "the folio is dropped, and the columns are measured without it")
}

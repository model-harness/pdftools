package extract

import (
	"strings"
	"testing"
)

// TestMonospacedLinesKeepTheirBreaks: two lines set wholly in a monospaced face are joined by a
// line break, not by the space a wrap gets, since a listing's line ends are content; the space a
// producer draws at a line's end goes, and a line's leading spaces stay. A line in a
// proportional face on either side is a wrap as before.
func TestMonospacedLinesKeepTheirBreaks(t *testing.T) {
	for _, c := range []struct {
		name, stream, want string
	}{
		{"two listing lines", `BT /F2 10 Tf 10 700 Td (# one ) Tj 0 -12 Td (# two) Tj ET`, "# one\n# two"},
		{"indentation", `BT /F2 10 Tf 10 700 Td (def f\(\):) Tj 0 -12 Td (    return x) Tj ET`, "def f():\n    return x"},
		{"a blank line", `BT /F2 10 Tf 10 700 Td (# one) Tj 0 -12 Td ( ) Tj 0 -12 Td (def f\(\):) Tj ET`, "# one\n\ndef f():"},
		// Word draws a code line's end-space in its body face.
		{"a line-end space in another face", `BT /F2 10 Tf 10 700 Td (# one) Tj /F1 10 Tf ( ) Tj /F2 10 Tf 0 -12 Td (# two) Tj ET`, "# one\n# two"},
		{"the joining line's end-space in another face", `BT /F2 10 Tf 10 700 Td (# one) Tj 0 -12 Td (# two) Tj /F1 10 Tf ( ) Tj /F2 10 Tf 0 -12 Td (# three) Tj ET`, "# one\n# two\n# three"},
		{"after a line of prose and code", `BT /F1 10 Tf 10 700 Td (see ) Tj /F2 10 Tf (main.go) Tj 0 -12 Td (for it) Tj ET`, "see main.go for it"},
		{"into prose", `BT /F2 10 Tf 10 700 Td (main.go) Tj /F1 10 Tf 0 -12 Td (is the file) Tj ET`, "main.go is the file"},
		{"out of prose", `BT /F1 10 Tf 10 700 Td (the file is) Tj /F2 10 Tf 0 -12 Td (main.go) Tj ET`, "the file is main.go"},
	} {
		p := extractPage(t, c.stream)
		if len(p.Blocks) != 1 {
			t.Fatalf("%s: %d blocks, want 1", c.name, len(p.Blocks))
		}
		if got := p.Blocks[0].Text(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestAMonospacedFractionIsNotTwoLines: a stacked fraction set in a monospaced face is still one
// fraction; its denominator continues the numerator's line (joinPrev), so no listing break is
// written between them.
func TestAMonospacedFractionIsNotTwoLines(t *testing.T) {
	f := newFrac()
	// Courier is wider than Helvetica, so the bar runs under the wider denominator.
	f.to = 122
	f.want(t, "12/116")
	mono := strings.ReplaceAll(f.stream(), "/F1 ", "/F2 ")
	if got := extractText(t, mono); got != "12/116" {
		t.Errorf("in a monospaced face: %q, want %q", got, "12/116")
	}
}

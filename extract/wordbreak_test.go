package extract

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// Lines of prose, as a page draws them: one that ends in the space Word draws where it wraps
// between words, one with none, one broken at a hyphen, and one starting in a capital, after
// which no line wraps a sentence.
const (
	drawnLine   = "the records are kept "
	undrawnLine = "the records are kept"
	dashedLine  = "the records are re-"
	capitalLine = "The records are kept"
)

// prose is a paragraph of lines, each in turn, then a narrow cell's word broken across two lines
// with no space drawn, "Confid" and "ence", which counts among the page's wraps.
func prose(lines ...string) string {
	var b strings.Builder
	b.WriteString("BT /F1 10 Tf 10 700 Td ")
	for _, l := range lines {
		b.WriteString("(" + l + ") Tj 0 -12 Td ")
	}
	b.WriteString("(and read.) Tj ET BT /F1 10 Tf 300 400 Td (Confid) Tj 0 -12 Td (ence) Tj ET")
	return b.String()
}

// times is n of line.
func times(n int, line string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = line
	}
	return out
}

// TestAWordBrokenWithoutASpaceIsOneWord: on a page that draws the space at each line it wraps
// between words, a wrap with no space drawn after a line of one word breaks the word, as Word
// breaks one too long for its cell, and is joined with none; on a page that draws no wrap spaces,
// a wrap is a space as before. Drawing them is a habit from five on, and while more of the page's
// wraps between words draw it than do not; a wrap at a hyphen, or before a capital, is none.
func TestAWordBrokenWithoutASpaceIsOneWord(t *testing.T) {
	for _, c := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"a page that draws its wrap spaces", times(5, drawnLine), "Confidence"},
		{"a page that draws four", times(4, drawnLine), "Confid ence"},
		{"a page that draws none", times(12, undrawnLine), "Confid ence"},
		{"as many drawn as not", append(times(6, drawnLine), times(5, undrawnLine)...), "Confid ence"},
		{"one more drawn than not", append(times(7, drawnLine), times(5, undrawnLine)...), "Confidence"},
		{"wraps at a hyphen", append(times(6, drawnLine), times(6, dashedLine)...), "Confidence"},
		{"wraps before a capital", append(times(6, capitalLine), times(6, drawnLine)...), "Confidence"},
	} {
		got := extractText(t, prose(c.lines...))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want it to hold %q", c.name, got, c.want)
		}
	}
	// A document decides the same, by its own words.
	d, err := New(onePage(prose(times(5, drawnLine)...)), DefaultOptions).Document()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Pages[0].Text(); !strings.Contains(got, "Confidence") {
		t.Errorf("a document: %q, want the word whole", got)
	}
}

// TestABreakBetweenWordsKeepsItsSpace: a wrap with no space drawn after a line of several words,
// as Word left in Adobe's sample's "Booker" and "speaking", is a space all the same, though the
// page uses "speaking" nowhere else: Word breaks a word only where it is too wide for the line
// by itself. A narrow cell's one word to a line, ISO 32000-2's "Object" and "number", is two
// words where the page uses the second.
func TestABreakBetweenWordsKeepsItsSpace(t *testing.T) {
	for _, c := range []struct{ name, cell, want string }{
		{"a line of several words", "(Thomas Booker) Tj 0 -12 Td (speaking now.) Tj", "Thomas Booker speaking"},
		{"a word the page uses", "(Object) Tj 0 -12 Td (number) Tj 0 -12 Td (The number one.) Tj", "Object number"},
		{"a line before a capital", "(Field) Tj 0 -12 Td (Confid) Tj 0 -12 Td (ence) Tj", "Field Confidence"},
		{"a line before a figure", "(Total) Tj 0 -12 Td (42 crossings) Tj", "Total 42"},
		{"a figure before a word", "(42) Tj 0 -12 Td (crossings a day) Tj", "42 crossings"},
		{"a word set off by a space", "( Confid) Tj 0 -12 Td (ence) Tj", "Confidence"},
	} {
		stream := strings.Replace(prose(times(5, drawnLine)...), "(Confid) Tj 0 -12 Td (ence) Tj", c.cell, 1)
		if got := extractText(t, stream); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want it to hold %q", c.name, got, c.want)
		}
	}
}

// TestBreaksResolveByTheDocumentsWords: a candidate joins where the joined word is one the
// document uses on any page, though the second part is a word too, or where the second part is
// no word it uses; otherwise it is a space. The line starts after each mark, in its span, move
// with the bytes the mark gives up.
func TestBreaksResolveByTheDocumentsWords(t *testing.T) {
	pages := []doc.Page{
		{Number: 1, Blocks: []doc.Block{
			{Spans: []doc.Span{{Text: "High Confidenc" + wordMark + "e is"}, {Text: "high and low, as the crew says"}},
				Lines: []doc.Line{{Span: 0, Offset: 0}, {Span: 0, Offset: len("High Confidenc" + wordMark)}, {Span: 1, Offset: len("high and low, as ")}}},
			{Spans: []doc.Span{{Text: "Timest" + wordMark + "amp"}}},
			{Spans: []doc.Span{{Text: "Object" + wordMark + "nUMBER"}}},
		}},
		{Number: 2, Blocks: []doc.Block{
			{Spans: []doc.Span{{Text: "\"confidence\": \"high\", e.g. the second number"}}},
			{Spans: []doc.Span{{Text: "and the" + wordMark + "second one"}},
				Lines: []doc.Line{{Span: 0, Offset: 0}, {Span: 0, Offset: len("and the" + wordMark)}}},
			{Spans: []doc.Span{{Text: "and the" + wordMark}, {Text: "second one"}}},
		}},
	}
	resolveBreaks(pages)
	for _, c := range []struct {
		name string
		b    doc.Block
		want string
	}{
		{"a word the document uses", pages[0].Blocks[0], "High Confidence ishigh and low, as the crew says"},
		{"parts it uses for no word", pages[0].Blocks[1], "Timestamp"},
		{"a second part it uses", pages[0].Blocks[2], "Object nUMBER"},
		{"two words it uses", pages[1].Blocks[1], "and the second one"},
		{"a second part in the next span", pages[1].Blocks[2], "and the second one"},
	} {
		var sb strings.Builder
		for _, s := range c.b.Spans {
			sb.WriteString(s.Text)
		}
		if got := sb.String(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		name string
		l    doc.Line
		want doc.Line
	}{
		{"after a join", pages[0].Blocks[0].Lines[1], doc.Line{Span: 0, Offset: len("High Confidenc")}},
		{"in the next span", pages[0].Blocks[0].Lines[2], doc.Line{Span: 1, Offset: len("high and low, as ")}},
		{"after a space", pages[1].Blocks[1].Lines[1], doc.Line{Span: 0, Offset: len("and the ")}},
	} {
		if c.l.Span != c.want.Span || c.l.Offset != c.want.Offset {
			t.Errorf("%s: line at %d:%d, want %d:%d", c.name, c.l.Span, c.l.Offset, c.want.Span, c.want.Offset)
		}
	}
}

// lineOf is a line of fragments in orientation orient.
func lineOf(orient int, texts ...string) line {
	l := line{orient: orient}
	for _, t := range texts {
		l.frags = append(l.frags, frag{text: []byte(t), orient: orient})
	}
	return l
}

// TestTheHabitReadsEachWrap: drawsWrapSpaces reads a wrap by the last fragment with text before
// it and the first after it, past the white space it starts with, and counts no wrap to or from
// a line set in another direction.
func TestTheHabitReadsEachWrap(t *testing.T) {
	end := lineOf(0, "and read.")
	for _, c := range []struct {
		name  string
		lines []line
	}{
		{"a line ending in an empty fragment", []line{
			lineOf(0, drawnLine, ""), lineOf(0, drawnLine, ""), lineOf(0, drawnLine, ""),
			lineOf(0, drawnLine, ""), lineOf(0, drawnLine, ""), end}},
		{"a line starting in a space", []line{
			lineOf(0, " "+drawnLine), lineOf(0, " "+drawnLine), lineOf(0, " "+drawnLine),
			lineOf(0, " "+drawnLine), lineOf(0, " "+drawnLine), lineOf(0, " and read.")}},
		{"lines set sideways", []line{
			lineOf(1, undrawnLine), lineOf(1, undrawnLine), lineOf(1, undrawnLine),
			lineOf(1, undrawnLine), lineOf(1, undrawnLine), lineOf(1, undrawnLine),
			lineOf(0, drawnLine), lineOf(0, drawnLine), lineOf(0, drawnLine),
			lineOf(0, drawnLine), lineOf(0, drawnLine), lineOf(0, drawnLine), end}},
	} {
		if !drawsWrapSpaces(c.lines) {
			t.Errorf("%s: want the page's habit read", c.name)
		}
	}
}

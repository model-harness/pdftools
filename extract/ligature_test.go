package extract

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// TestLigaturesAreSpelledInsideWords: a ligature a letter touches is its letters (#24), one that
// stands alone is the character a glyph table is about, and the start of every later line in the
// span moves with the bytes the spelling changed.
func TestLigaturesAreSpelledInsideWords(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"de\uFB01nitions", "definitions"},
		{"tra\uFB03c", "traffic"},
		{"\uFB01rst", "first"},
		{"o\uFB00", "off"},
		{"\uFB00", "\uFB00"},
		{"| \uFB00 | ff |", "| \uFB00 | ff |"},
		{"plain", "plain"},
		{"st\uFB06", "stst"},
	} {
		if got := spellLigatures(c.in); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
	b := doc.Block{Spans: []doc.Span{{Text: "de\uFB01ned tra\uFB03c"}}, Lines: []doc.Line{{Span: 0, Offset: 0}, {Span: 0, Offset: len("de\uFB01ned ")}}}
	spellBlock(&b)
	if b.Spans[0].Text != "defined traffic" || b.Spans[0].Text[b.Lines[1].Offset:] != "traffic" {
		t.Errorf("%q, second line at %d", b.Spans[0].Text, b.Lines[1].Offset)
	}
}

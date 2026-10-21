package ocr

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// sure is a reading of words, each at a confidence of 96.
func sure(texts ...string) []Word {
	out := make([]Word, len(texts))
	for i, t := range texts {
		out[i] = Word{Text: t, Conf: 96}
	}
	return out
}

// u is n glyphs with no value.
func u(n int) string { return strings.Repeat(doc.Unmapped, n) }

// TestFillsPlaceARunByItsLine: a run is read where the line's own text pins it, by its mapped
// words and its word's letters, and not elsewhere.
func TestFillsPlaceARunByItsLine(t *testing.T) {
	type fill struct {
		text  string
		words []int
		alone bool
	}
	for _, c := range []struct {
		name  string
		line  string
		words []Word
		want  []fill
	}{
		{"a ligature in a word", "When crea" + u(1) + "ng lists,", sure("When", "creating", "lists,"),
			[]fill{{"ti", []int{1}, false}}},
		{"among the lines above and below", "When crea" + u(1) + "ng lists,", sure("AUVUNE", "When", "creating", "lists,", "Adobe"),
			[]fill{{"ti", []int{2}, false}}},
		{"two runs in a word", "Co" + u(1) + "st" + u(1) + "u" + u(1) + "to" + u(1) + " now", sure("Constructor", "now"),
			[]fill{{"n", []int{0}, false}, {"r", []int{0}, false}, {"c", []int{0}, false}, {"r", []int{0}, false}}},
		{"letters that are not the word's", "crea" + u(1) + "ng lists", sure("cleaning", "lists"), nil},
		{"more characters than glyphs, not letters", "within 5" + u(1) + ". Using", sure("within", "55”.", "Using"), nil},
		{"more letters than glyphs, a ligature", "a so" + u(1) + "ware family", sure("a", "software", "family"),
			[]fill{{"ft", []int{1}, false}}},
		{"more than three letters a glyph", "a so" + u(1) + "ware family", sure("a", "soabcdware", "family"), nil},
		{"a glyph read as nothing", "a so" + u(1) + "ware family", sure("a", "soware", "family"), nil},
		{"two glyphs read as one character", "the pi" + u(2) + "ap’s", sure("the", "pixap’s"), nil},
		{"a word of one glyph in a line", u(1) + " You are free", sure("e", "You", "are", "free"),
			[]fill{{"e", []int{0}, true}}},
		{"a word of glyphs is read as as many characters", u(3) + " PRICE", sure("QTY", "PRICE"),
			[]fill{{"QTY", []int{0}, false}}},
		{"a word of glyphs is not read as more", u(3) + " PRICE", sure("QUANTITY", "PRICE"), nil},
		{"a line of one glyph", u(1), sure(`\`), nil},
		{"a line of glyphs, spaces glyphs too", u(12) + " " + u(1) + " " + u(7), sure("Dining", "Chair", "1", "$215.00"),
			[]fill{{"Dining Chair", []int{0, 1}, false}, {"1", []int{2}, true}, {"$215.00", []int{3}, false}}},
		{"a line of glyphs, its spaces not glyphs", u(11), sure("Dining", "Chair"),
			[]fill{{"Dining Chair", []int{0, 1}, false}}},
		{"a link read in two words", u(16), sure("https:", "//ned.ipac"),
			[]fill{{"https: //ned.ipac", []int{0, 1}, false}}},
		{"a line of glyphs read as too many", u(4), sure("Dining", "Chair"), nil},
		{"a line of glyphs split two ways", u(3) + " " + u(3), sure("a", "b", "c", "d", "e"), nil},
		{"words read as blank are not words", "crea" + u(1) + "ng", []Word{{Text: " ", Conf: 96}, {Text: "creating", Conf: 96}},
			[]fill{{"ti", []int{1}, false}}},
		{"no run", "plain text", sure("plain", "text"), nil},
		{"words apart by a no-break space", "a\u00a0so" + u(1) + "ware family", sure("a", "software", "family"),
			[]fill{{"ft", []int{1}, false}}},
		// The second "the" is the one the reading read; matched to the first, misread, the run
		// would fall in no gap that pairs it.
		{"a word the line repeats", "the crea" + u(1) + "ng the", sure("tbe", "creating", "the"),
			[]fill{{"ti", []int{1}, false}}},
		// A mapped word Tesseract split, beside a word of glyphs: the gap is not glyphs alone.
		{"a split word beside a run", "When " + u(3) + " abcd", sure("When", "QTY", "ab", "cd"), nil},
	} {
		got := Fills(c.line, c.words)
		if len(got) != len(c.want) {
			t.Errorf("%s: %d fills %+v, want %d", c.name, len(got), got, len(c.want))
			continue
		}
		for k, f := range got {
			w := c.want[k]
			if f.Text != w.text || f.Alone != w.alone || len(f.Words) != len(w.words) || c.line[f.At[0]:f.At[1]] != strings.Repeat(doc.Unmapped, (f.At[1]-f.At[0])/len(doc.Unmapped)) {
				t.Errorf("%s: fill %d = %+v, want %+v", c.name, k, f, w)
				continue
			}
			for j := range w.words {
				if f.Words[j] != w.words[j] {
					t.Errorf("%s: fill %d words %v, want %v", c.name, k, f.Words, w.words)
				}
			}
		}
	}
}

// TestFillsAtIsTheRun: a fill names its run's bytes, so that the caller replaces exactly the
// glyphs read.
func TestFillsAtIsTheRun(t *testing.T) {
	line := "the pi" + u(2) + "ap’s IRe" + u(1) + "t"
	got := Fills(line, sure("the", "pixmap’s", "IRect"))
	if len(got) != 2 {
		t.Fatalf("%d fills, want 2", len(got))
	}
	if start := strings.Index(line, doc.Unmapped); got[0].At != [2]int{start, start + 2*len(doc.Unmapped)} || got[0].Text != "xm" {
		t.Errorf("first fill %+v, want bytes %d to %d read xm", got[0], start, start+2*len(doc.Unmapped))
	}
	if start := strings.LastIndex(line, doc.Unmapped); got[1].At != [2]int{start, start + len(doc.Unmapped)} || got[1].Text != "c" {
		t.Errorf("second fill %+v", got[1])
	}
}

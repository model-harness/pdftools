package layout

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
)

// Hyphens removes the hyphens a typesetter put at line ends, in place, and returns how many
// it removed.
//
// Extract keeps every hyphen that ends a line after a letter, and joins the next line to it
// with no space (dashHoldsTheWord), because it cannot tell "exten-|sive" from "cross-|reference"
// and a hyphen deleted in error is not recoverable from the output. The document can tell them
// apart, because it is the only dictionary there is. A hyphen before a lower-case letter is
// dropped when either of these holds:
//
//   - the document spells the joined word elsewhere, and never the hyphenated one: "configura-|
//     tion" against "configuration";
//   - neither half is a word anywhere in the document: "exten-|sive". A compound's halves are
//     words, "machine-|readable" aside.
//
// Otherwise it stays, as the ambiguous case does (ADR 0035). Over the corpus the pass takes 249
// hyphens from 11 files. Of the words that changed in md's output, 2 are compounds it wrongly
// joined, "machine-readable" and "sub-clauses", whose halves appear nowhere else in their
// documents. Four of the tagged standards mark their typesetter's hyphens with /ActualText, which
// sectionize already applies; the pass takes all 15 of those on its own. The case it leaves alone is
// a break with a word for either half, such as "over-|lap" or "insert-|ing", which no reading of the
// document can tell from a compound.
//
// A soft hyphen at a line end is a typesetter's by definition, and always goes.
//
// The words counted leave out any that crosses a line start, so a hyphen being judged is not
// its own evidence, and running the pass again, after PageBreaks has made line starts of its
// own, judges the rest the same way.
func Hyphens(d *doc.Document) int {
	if d == nil {
		return 0
	}
	words := map[string]int{}
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			b := &d.Pages[pi].Blocks[bi]
			text, starts := b.Text(), lineStarts(b)
			for _, at := range wordRE.FindAllStringIndex(text, -1) {
				if !crosses(starts, at[0], at[1]) {
					words[strings.ReplaceAll(strings.ToLower(text[at[0]:at[1]]), "\u2010", "-")]++
				}
			}
		}
	}
	removed := 0
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			b := &d.Pages[pi].Blocks[bi]
			text, starts := b.Text(), lineStarts(b)
			// Last line first, so the offsets of the lines before stay put.
			for k := len(starts) - 1; k >= 1; k-- {
				if n := typesetHyphen(text, starts[k], words); n > 0 {
					removeBytes(b, starts[k]-n, n)
					removed++
				}
			}
		}
	}
	return removed
}

// wordRE is a word as the dictionary counts one: letters, with hyphens inside it, so that
// "well-known" is a word and "well" is another. U+2010 HYPHEN is a hyphen too, counted as
// U+002D, or a word broken at one would read as its two halves, both of them words.
var wordRE = regexp.MustCompile(`\pL[\pL\x{2010}-]*\pL|\pL`)

// typesetHyphen returns the length of the hyphen ending text[:at] when it is a typesetter's,
// and 0 when there is none or it is the word's.
func typesetHyphen(text string, at int, words map[string]int) int {
	h, n := utf8.DecodeLastRuneInString(text[:at])
	before, _ := utf8.DecodeLastRuneInString(text[:at-n])
	if !unicode.IsLetter(before) {
		return 0
	}
	if h == '\u00ad' {
		return n
	}
	after, _ := utf8.DecodeRuneInString(text[at:])
	if (h != '-' && h != '\u2010') || !unicode.IsLower(after) {
		return 0
	}
	left := strings.ToLower(text[strings.LastIndexFunc(text[:at-n], notLetter)+1 : at-n])
	right := text[at:]
	if i := strings.IndexFunc(right, notLetter); i >= 0 {
		right = right[:i]
	}
	right = strings.ToLower(right)
	joined, hyphenated := words[left+right], words[left+"-"+right]
	if (joined > 0 && hyphenated == 0) || (hyphenated == 0 && words[left] == 0 && words[right] == 0) {
		return n
	}
	return 0
}

func notLetter(r rune) bool { return !unicode.IsLetter(r) }

// lineStarts returns where each of b's lines starts, as a byte offset into b.Text().
func lineStarts(b *doc.Block) []int {
	at := make([]int, len(b.Spans)+1)
	for i := range b.Spans {
		at[i+1] = at[i] + len(b.Spans[i].Text)
	}
	out := make([]int, len(b.Lines))
	for i, ln := range b.Lines {
		out[i] = at[ln.Span] + ln.Offset
	}
	return out
}

// crosses reports whether a line starts strictly inside text[from:to].
func crosses(starts []int, from, to int) bool {
	for _, s := range starts {
		if from < s && s < to {
			return true
		}
	}
	return false
}

// removeBytes deletes n bytes at offset at of b.Text(), which lie in one span since they are
// one rune, and moves the starts of the lines after them back.
func removeBytes(b *doc.Block, at, n int) {
	for i := range b.Spans {
		if l := len(b.Spans[i].Text); at >= l {
			at -= l
			continue
		}
		b.Spans[i].Text = b.Spans[i].Text[:at] + b.Spans[i].Text[at+n:]
		for k := range b.Lines {
			if b.Lines[k].Span == i && b.Lines[k].Offset > at {
				b.Lines[k].Offset -= n
			}
		}
		return
	}
}

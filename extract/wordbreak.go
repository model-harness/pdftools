package extract

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
)

// wordMark stands where appendLine could not yet say whether a wrap is a space or a broken word:
// a page that draws the space at its wraps drew none after a line of one word, between a letter
// and a lowercase letter. U+FDD0 is a noncharacter, which no text holds, so the mark cannot be
// confused with anything a page drew; resolveBreaks replaces every one before a page or a document
// leaves this package.
const wordMark = "\uFDD0"

// resolveBreaks decides each wordMark among pages by the words they use: the two parts are one
// word where that word is one the pages use elsewhere, or the second part is no word they use, as
// "Confidenc" and "e" are; otherwise the wrap is the space it would have been, as a narrow
// header cell's "Object" and "number" are in ISO 32000-2. The same evidence layout.Hyphens takes
// a hyphen by: the document's own words, and no dictionary.
//
// A word just after a mark is not counted as used, since it is the part in question; the part
// before one is never asked about.
func resolveBreaks(pages []doc.Page) {
	marked := false
	for pi := range pages {
		for bi := range pages[pi].Blocks {
			for si := range pages[pi].Blocks[bi].Spans {
				if strings.Contains(pages[pi].Blocks[bi].Spans[si].Text, wordMark) {
					marked = true
				}
			}
		}
	}
	if !marked {
		return
	}
	used := map[string]bool{}
	for pi := range pages {
		for bi := range pages[pi].Blocks {
			for _, sp := range pages[pi].Blocks[bi].Spans {
				for _, w := range wordsAfterNoMark(sp.Text) {
					used[w] = true
				}
			}
		}
	}
	for pi := range pages {
		for bi := range pages[pi].Blocks {
			resolveBlock(&pages[pi].Blocks[bi], used)
		}
	}
}

// wordsAfterNoMark returns text's words, lowercased, but for those that start just after a
// wordMark.
func wordsAfterNoMark(text string) []string {
	var out []string
	start, touched := -1, false
	for i, r := range text + " " {
		switch {
		case unicode.IsLetter(r):
			if start < 0 {
				start = i
				touched = i >= len(wordMark) && text[i-len(wordMark):i] == wordMark
			}
		default:
			if start >= 0 {
				if !touched {
					out = append(out, strings.ToLower(text[start:i]))
				}
				start = -1
			}
		}
	}
	return out
}

// resolveBlock replaces the marks in b by used, moving the line starts after each with its bytes.
func resolveBlock(b *doc.Block, used map[string]bool) {
	for si := range b.Spans {
		for {
			at := strings.Index(b.Spans[si].Text, wordMark)
			if at < 0 {
				break
			}
			text := b.Spans[si].Text
			before := lastLetters(text[:at])
			after := firstLetters(b.Spans, si, at+len(wordMark))
			with := " "
			if w := strings.ToLower(before + after); used[w] || !used[strings.ToLower(after)] {
				with = ""
			}
			b.Spans[si].Text = text[:at] + with + text[at+len(wordMark):]
			moved := len(wordMark) - len(with)
			for li := range b.Lines {
				if l := &b.Lines[li]; l.Span == si && l.Offset > at {
					l.Offset -= moved
				}
			}
		}
	}
}

// lastLetters is the run of letters text ends in.
func lastLetters(text string) string {
	i := len(text)
	for i > 0 {
		r, n := utf8.DecodeLastRuneInString(text[:i])
		if !unicode.IsLetter(r) {
			break
		}
		i -= n
	}
	return text[i:]
}

// firstLetters is the run of letters that starts at byte at of spans[si], running on into the
// spans after it.
func firstLetters(spans []doc.Span, si, at int) string {
	var sb strings.Builder
	for ; si < len(spans); si, at = si+1, 0 {
		text := spans[si].Text[at:]
		for i, r := range text {
			if !unicode.IsLetter(r) {
				sb.WriteString(text[:i])
				return sb.String()
			}
		}
		sb.WriteString(text)
	}
	return sb.String()
}

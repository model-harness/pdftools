package ocr

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
	"golang.org/x/text/unicode/norm"
)

// A Fill is what a reading gives one run of glyphs with no value in a line's text (ADR 0048).
type Fill struct {
	// At is the run of doc.Unmapped, a byte range of the text.
	At [2]int

	// Text is what was read in its place.
	Text string

	// Words are the indices of the words it was read from.
	Words []int

	// Alone is a run that is a word of one glyph, read by its shape alone. Tesseract reads a
	// bullet as "e" as surely as a "$" as "$", so its confidence does not say which it is.
	Alone bool
}

// maxFillTokens bounds the tokens a side that Fills aligns, so that its table stays small: the
// text is a line, and a longer one is left as it is.
const maxFillTokens = 512

// Fills reads, from the words read off a line's image, the text of each run of glyphs in it with
// no value. No glyph is guessed: a run is filled only where the line's own text pins its place.
//
//   - The line's words with no run in them are matched to the reading's, in order, and what lies
//     between two matches on one side is read against what lies between them on the other.
//   - Between them, word for word where the two have as many words, a word with a run must read
//     as its mapped letters around the run, "crea" and "ng" around "ti", and a run of n glyphs as
//     n characters, or as up to 3n letters, a glyph being one character or a ligature.
//   - A word of nothing but a run of n glyphs, with no letters of its own to place it, must read
//     as a word of n characters.
//   - A gap that is all one such word, a line set wholly in a font with no values, takes the
//     words between whose characters, spaces included or not, number n.
//   - A line of one glyph and nothing else is not filled: nothing places it, and every reading of
//     one character has its count. A symbol set alone, in a formula, is what one is.
//
// The words' confidence is the caller's to judge.
func Fills(text string, words []Word) []Fill {
	type tok struct {
		text string
		runs [][2]int // its runs of doc.Unmapped, as byte ranges of text
	}
	var s []tok
	for _, f := range fields.FindAllStringIndex(text, -1) {
		t := tok{text: text[f[0]:f[1]]}
		for _, r := range unmappedRuns.FindAllStringIndex(t.text, -1) {
			t.runs = append(t.runs, [2]int{f[0] + r[0], f[0] + r[1]})
		}
		s = append(s, t)
	}
	if !strings.Contains(text, doc.Unmapped) || len(s) == 1 && s[0].text == doc.Unmapped {
		return nil
	}
	var t []int
	for i, w := range words {
		if readable(w.Text) {
			t = append(t, i)
		}
	}
	if len(s) > maxFillTokens || len(t) > maxFillTokens {
		return nil
	}
	// The longest run of the line's mapped words the reading reads in order.
	lcs := make([][]int32, len(s)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(t)+1)
	}
	// A word with a run is no reading's: doc.Unmapped is no character a reader writes.
	same := func(i, j int) bool {
		return norm.NFKC.String(s[i].text) == norm.NFKC.String(words[t[j]].Text)
	}
	for i := len(s) - 1; i >= 0; i-- {
		for j := len(t) - 1; j >= 0; j-- {
			switch {
			case same(i, j):
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []Fill
	gap := func(si, sj, ti, tj int) {
		g, r := s[si:sj], t[ti:tj]
		if len(g) == len(r) {
			for k, tk := range g {
				if len(tk.runs) == 0 {
					continue
				}
				caps, ok := match(tk.text, words[r[k]].Text)
				if !ok {
					continue
				}
				for c, txt := range caps {
					out = append(out, Fill{At: tk.runs[c], Text: txt, Words: []int{r[k]}, Alone: tk.text == doc.Unmapped})
				}
			}
			return
		}
		sizes := make([]int, len(g))
		for k, tk := range g {
			if !onlyUnmapped(tk.text) {
				return
			}
			sizes[k] = utf8.RuneCountInString(tk.text)
		}
		parts := make([]string, len(r))
		for k, wi := range r {
			parts[k] = norm.NFKC.String(words[wi].Text)
		}
		cuts, texts, ok := partition(sizes, parts)
		if !ok {
			return
		}
		for k, tk := range g {
			out = append(out, Fill{At: tk.runs[0], Text: texts[k],
				Words: append([]int(nil), r[cuts[k]:cuts[k+1]]...), Alone: tk.text == doc.Unmapped})
		}
	}
	i, j, pi, pj := 0, 0, 0, 0
	for i < len(s) && j < len(t) {
		switch {
		// The table counts a match wherever there is one, so a match is on a longest path.
		case same(i, j):
			gap(pi, i, pj, j)
			i, j = i+1, j+1
			pi, pj = i, j
		case lcs[i+1][j] >= lcs[i][j+1]:
			i++
		default:
			j++
		}
	}
	gap(pi, len(s), pj, len(t))
	return out
}

// unmappedRuns finds the runs of glyphs with no value in a word, and fields the words of a line.
var (
	unmappedRuns = regexp.MustCompile(doc.Unmapped + "+")
	fields       = regexp.MustCompile(`[^\s\p{Z}]+`)
)

// onlyUnmapped reports a word of nothing but glyphs with no value.
func onlyUnmapped(w string) bool {
	return strings.Trim(w, doc.Unmapped) == ""
}

// match reads word, a word of the span's with runs of glyphs with no value in it, against got,
// the word read in its place: got must be the word's mapped letters with something between, and
// each run of n glyphs is read as n characters, or, where the word is not the run alone, as up
// to 3n letters. It returns what each run reads as.
func match(word, got string) ([]string, bool) {
	word, got = norm.NFKC.String(word), norm.NFKC.String(got)
	lone := onlyUnmapped(word)
	var pat strings.Builder
	var sizes []int
	pat.WriteString("^")
	last := 0
	for _, r := range unmappedRuns.FindAllStringIndex(word, -1) {
		pat.WriteString(regexp.QuoteMeta(word[last:r[0]]))
		pat.WriteString("(.+?)")
		sizes = append(sizes, utf8.RuneCountInString(word[r[0]:r[1]]))
		last = r[1]
	}
	pat.WriteString(regexp.QuoteMeta(word[last:]) + "$")
	m := regexp.MustCompile(pat.String()).FindStringSubmatch(got)
	if m == nil {
		return nil, false
	}
	for k, n := range sizes {
		c := utf8.RuneCountInString(m[k+1])
		if c == n {
			continue
		}
		// More characters than glyphs is a ligature's reading, and a ligature is letters.
		if lone || c < n || c > 3*n || strings.IndexFunc(m[k+1], func(r rune) bool { return !unicode.IsLetter(r) }) >= 0 {
			return nil, false
		}
	}
	return m[1:], true
}

// partition splits parts, in order, into one group for each of sizes, each group's characters
// numbering its size, spaces between its parts counted or not. It returns where each group
// starts, and the end, and each group's text, its parts joined by spaces. A space the page draws
// as a gap is no glyph, and neither is a break Tesseract reads inside a word, "https: //", so the
// count cannot tell them apart and Tesseract's words are taken as it read them. It reports false
// unless there is exactly one such split.
func partition(sizes []int, parts []string) ([]int, []string, bool) {
	n := make([]int, len(parts))
	for i, p := range parts {
		n[i] = utf8.RuneCountInString(p)
	}
	// ways[k][j] counts, up to 2, the splits of parts[j:] into sizes[k:].
	ways := make([][]int, len(sizes)+1)
	for k := range ways {
		ways[k] = make([]int, len(parts)+1)
	}
	ways[len(sizes)][len(parts)] = 1
	group := func(k, j, e int) (string, bool) {
		chars := 0
		for _, c := range n[j:e] {
			chars += c
		}
		if sizes[k] != chars && sizes[k] != chars+e-j-1 {
			return "", false
		}
		return strings.Join(parts[j:e], " "), true
	}
	for k := len(sizes) - 1; k >= 0; k-- {
		for j := len(parts) - 1; j >= 0; j-- {
			for e := j + 1; e <= len(parts); e++ {
				if _, ok := group(k, j, e); ok && ways[k+1][e] > 0 {
					ways[k][j] = min(2, ways[k][j]+ways[k+1][e])
				}
			}
		}
	}
	if ways[0][0] != 1 {
		return nil, nil, false
	}
	cuts, texts := []int{0}, make([]string, 0, len(sizes))
	for k, j := 0, 0; k < len(sizes); k++ {
		for e := j + 1; e <= len(parts); e++ {
			if text, ok := group(k, j, e); ok && ways[k+1][e] > 0 {
				cuts, texts = append(cuts, e), append(texts, text)
				j = e
				break
			}
		}
	}
	return cuts, texts, true
}

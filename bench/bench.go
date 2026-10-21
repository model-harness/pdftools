// Package bench scores a converter's Markdown against a gold Markdown file, so a change
// to extraction, layout, or the sink can be judged by a number instead of a diff.
//
// Both files are parsed with goldmark into the same shape: headings, paragraphs, list
// items, tables, and code blocks, plus the inline spans — bold, italic, code, strike —
// that live inside them. Scoring then matches the reference's blocks to whichever output
// blocks resemble them most, in reference order, and reports on that matching: how many
// characters survived once the blocks are back in order, how many of the reference's
// words came through anywhere, whether blocks landed in the right order, how many blocks
// came through intact, and how well headings, tables, and inline style reproduced.
//
// Matching by content rather than by position is what lets this package score a reordered
// or merged document instead of just a line-for-line diff: a converter that emits the
// same two paragraphs in the wrong order should lose on Order and nothing else, and one
// that fuses four paragraphs into one should lose on Blocks because three of the four
// reference paragraphs no longer have an output block to themselves.
package bench

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Scores is what Score reports. Every field is a fraction except the two counters at the
// end; 1 is perfect for all of the fractions except CER, where 0 is perfect.
type Scores struct {
	// CER is the character error rate: edits per reference character, once the output's
	// blocks are back in the reference's order.
	CER float64

	// Recall and Precision are word-bag overlap: how much of the reference's
	// vocabulary came through anywhere in the output, and how much of the output's
	// vocabulary was actually in the reference.
	Recall, Precision float64

	// Order is the fraction of consecutive matched reference blocks that also come out
	// in increasing order in the output. A converter that only reorders blocks loses
	// here and nowhere else.
	Order float64

	// Blocks is the F1 of blocks that matched the reference closely enough to call
	// intact, over the reference and output block counts.
	Blocks float64

	Headings, Tables, Style float64

	// RefHeadings, RefCells, and RefSpans are the reference counts behind Headings,
	// Tables, and Style, so a caller can tell a vacuous 1.0 — neither side had any —
	// from one actually measured against content.
	RefHeadings, RefCells, RefSpans int

	// Errs and Images count the literal "[err]" and "[image]" markers in got: the text
	// and the pictures the converter marked rather than read.
	Errs, Images int
}

// parser is shared across calls: GFM for tables and strikethrough, Footnote because
// gold fixtures use footnote definitions as ordinary paragraphs with nothing else to
// hold them.
var parser = goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote))

// block is one scoring unit: a heading, a paragraph or list item, a table, or a code
// block. cells is set only for tables; every other kind carries its content in text.
type block struct {
	kind  string
	level int
	text  string
	cells [][]string
}

// span is one inline style occurrence: bold, italic, code, or strike, with its own text
// so two spans of the same kind over different words don't collide in the style F1.
type span struct{ kind, text string }

type document struct {
	blocks       []block
	spans        []span
	errs, images int
}

// Score compares got, the converter's Markdown, with ref, the gold Markdown.
func Score(ref, got []byte) Scores {
	return compare(parse(ref), parse(got))
}

// parse walks goldmark's AST into the flat block/span shape Score works on. Walking
// rather than reading the tree node types directly is what lets a table's cells and a
// paragraph's text use the same text-collecting pass, textOf.
func parse(src []byte) document {
	var d document
	s := string(src)
	d.errs, d.images = strings.Count(s, "[err]"), strings.Count(s, "[image]")
	// An [err] is one character the converter could not read, and is scored as one: as text it
	// is five characters of CER and a word "err" in the bag, none of them on the page (ADR 0042).
	src = []byte(strings.ReplaceAll(s, "[err]", "�"))
	root := parser.Parser().Parse(text.NewReader(src))
	_ = ast.Walk(root, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Heading:
			d.blocks = append(d.blocks, block{kind: "heading", level: t.Level, text: textOf(t, src)})
		case *ast.Paragraph:
			text := textOf(t, src)
			if imageMark.MatchString(text) {
				// An image is not text: kept out of every text score and counted, "[image]"
				// already above with the inline ones (ADR 0045).
				if text != "[image]" {
					d.images++
				}
				return ast.WalkSkipChildren, nil
			}
			d.blocks = append(d.blocks, block{kind: "para", text: text})
		case *ast.TextBlock:
			// A tight list's item text arrives as a TextBlock rather than a Paragraph. The
			// walk goes on into it, as it does into a paragraph, so the item's bold and
			// italic are scored.
			d.blocks = append(d.blocks, block{kind: "para", text: textOf(t, src)})
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			var b strings.Builder
			for i := 0; i < n.Lines().Len(); i++ {
				seg := n.Lines().At(i)
				b.Write(seg.Value(src))
			}
			d.blocks = append(d.blocks, block{kind: "code", text: strings.Join(strings.Fields(b.String()), " ")})
		case *east.Table:
			var rows [][]string
			for r := t.FirstChild(); r != nil; r = r.NextSibling() {
				var row []string
				for c := r.FirstChild(); c != nil; c = c.NextSibling() {
					row = append(row, textOf(c, src))
				}
				rows = append(rows, row)
			}
			// The walk goes on into the cells too, so a bold header cell is a bold span.
			d.blocks = append(d.blocks, block{kind: "table", cells: rows, text: strings.Join(flatten(rows), " ")})
		case *ast.Emphasis:
			kind := "italic"
			if t.Level == 2 {
				kind = "bold"
			}
			// A *** span is CommonMark's nested emphasis — level 1 wrapping level 2 —
			// so the walk visits both nodes over the same text and this appends one
			// span of each kind without any special case for it.
			d.spans = append(d.spans, span{kind, norm(textOf(t, src))})
		case *ast.CodeSpan:
			d.spans = append(d.spans, span{"code", norm(textOf(t, src))})
		case *east.Strikethrough:
			d.spans = append(d.spans, span{"strike", norm(textOf(t, src))})
		}
		return ast.WalkContinue, nil
	})
	return d
}

func flatten(rows [][]string) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

// textOf collects a node's text, the same way regardless of whether it holds a
// paragraph, a heading, or a table cell. SoftLineBreak and HardLineBreak become a space
// rather than nothing, so a line wrapped only by the source's line width doesn't fuse
// two words; strings.Fields then collapses every run of whitespace to one space.
func textOf(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.CodeSpan:
			// A code span's own backtick-delimited text, not the Markdown around it.
			for k := t.FirstChild(); k != nil; k = k.NextSibling() {
				if s, ok := k.(*ast.Text); ok {
					b.Write(s.Segment.Value(src))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// imageMark is a paragraph that is an image's line: "[image]", or "[tag: description]".
var imageMark = regexp.MustCompile(`^\[(image|[a-z][a-z -]{0,23}: .+)\]$`)

func words(s string) []string { return wordRe.FindAllString(s, -1) }

// norm is the key normalization shared by headings, table cells, and style spans:
// lowercase, words only, joined by single spaces. Punctuation and spacing differences
// that don't change the words shouldn't cost a match.
func norm(s string) string { return strings.Join(words(strings.ToLower(s)), " ") }

// fold is sim's normalization: lowercase and whitespace-collapsed, but — unlike norm —
// keeping punctuation, since sim is what decides whether two blocks are the same block
// at all and a dropped comma shouldn't be the difference between matched and not.
func fold(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// stripSpace is CER's normalization: every rune but whitespace, case and punctuation
// both counted. CER is meant to be strict in the way the word-bag Recall/Precision
// above is not.
func stripSpace(s string) []rune {
	rs := make([]rune, 0, len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			rs = append(rs, r)
		}
	}
	return rs
}

// lev is Levenshtein edit distance, over whatever comparable sequence the caller hands
// it — runes, here, for both sim and CER. Two rows of the usual dynamic-programming
// table, since a document's blocks are short enough that the full matrix is wasted
// memory for no benefit.
func lev[T comparable](a, b []T) int {
	prev, cur := make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := prev[j-1]
			if a[i-1] != b[j-1] {
				c++
			}
			cur[j] = min(c, prev[j]+1, cur[j-1]+1)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	inter := 0
	for _, x := range b {
		if m[x] > 0 {
			m[x]--
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// pairing is one reference block's match: idx is the output block it landed on, or -1
// when nothing cleared the similarity floor. sim is only meaningful when idx >= 0.
type pairing struct {
	idx int
	sim float64
}

// matchBlocks pairs each reference block, in order, with the best unused output block —
// "best" meaning highest sim, over runes, after lowercasing and collapsing whitespace.
// A pair needs sim >= 0.5 to count at all, and the match is "intact" at sim >= 0.9.
//
// A table matches only a table, and anything else only something that is not one. A
// table that came out as paragraphs holds the same words, but it is not an intact table,
// and matching it to them would let it count as one.
//
// Levenshtein over every reference/output pair is O(blocks²) in block count and O(len²)
// inside each call, which is fine for a page but not for a long document scored
// repeatedly in a benchmark loop. shortlist narrows each reference block to its three
// best word-bag Jaccard candidates first — cheap, since it's just set overlap — so the
// expensive comparison only ever runs 3 times per reference block instead of once per
// remaining output block.
func matchBlocks(ref, got []block) []pairing {
	used := make([]bool, len(got))
	gotWords := make([][]string, len(got))
	for j, b := range got {
		gotWords[j] = words(strings.ToLower(b.text))
	}

	out := make([]pairing, len(ref))
	for i, rb := range ref {
		out[i] = pairing{idx: -1}
		rw := words(strings.ToLower(rb.text))
		table := rb.kind == "table"
		free := func(j int) bool { return !used[j] && (got[j].kind == "table") == table }
		best, bj := 0.0, -1
		for _, j := range shortlist(rw, gotWords, free) {
			if s := similarity(rb.text, got[j].text); s > best {
				best, bj = s, j
			}
		}
		if bj >= 0 && best >= 0.5 {
			out[i] = pairing{idx: bj, sim: best}
			used[bj] = true
		}
	}
	return out
}

// shortlist returns the (up to 3) free output blocks whose word bag is closest to rw.
func shortlist(rw []string, gotWords [][]string, free func(int) bool) []int {
	type cand struct {
		j   int
		jac float64
	}
	var cands []cand
	for j, gw := range gotWords {
		if free(j) {
			cands = append(cands, cand{j, jaccard(rw, gw)})
		}
	}
	// Stable, so equal candidates stay in output order and the earlier block wins a tie.
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].jac > cands[b].jac })
	const n = 3
	if len(cands) > n {
		cands = cands[:n]
	}
	out := make([]int, len(cands))
	for i, c := range cands {
		out[i] = c.j
	}
	return out
}

func similarity(a, b string) float64 {
	ar, br := []rune(fold(a)), []rune(fold(b))
	if len(ar) == 0 && len(br) == 0 {
		return 1
	}
	return 1 - float64(lev(ar, br))/float64(max(len(ar), len(br)))
}

func compare(ref, got document) Scores {
	var s Scores
	pairs := matchBlocks(ref.blocks, got.blocks)
	intact := 0
	for _, p := range pairs {
		if p.idx >= 0 && p.sim >= 0.9 {
			intact++
		}
	}

	s.CER = cer(ref.blocks, got.blocks, pairs)
	s.Recall, s.Precision = bagScores(allWords(ref), allWords(got))
	s.Order = order(pairs)
	s.Blocks = f1Count(intact, len(ref.blocks), len(got.blocks))
	s.Headings, s.RefHeadings = headingsF1(ref.blocks, got.blocks)
	s.Tables, s.RefCells = tablesF1(ref.blocks, got.blocks, pairs)
	s.Style, s.RefSpans = stylesF1(ref.spans, got.spans)
	s.Errs, s.Images = got.errs, got.images
	return s
}

// cer is the character error rate of the output's text against the reference's, once
// the output's blocks are back in the reference's order. A matched output block takes
// its reference block's place, and an unmatched one follows the output block before it.
// So a merge, a split or a reordering costs no characters, since those are Blocks's and
// Order's to score. Scoring each matched pair alone charged a merged paragraph twice,
// once as every reference block it swallowed and once as the output block nothing
// matched: four whole paragraphs scored a CER of 2.
//
// The edit distance runs over the whole text, so its cost is the product of the two
// lengths. That suits a fixture of a page or two, and not a book.
func cer(ref, got []block, pairs []pairing) float64 {
	pos := make([]int, len(got))
	for j := range pos {
		pos[j] = -1
	}
	for i, p := range pairs {
		if p.idx >= 0 {
			pos[p.idx] = i
		}
	}
	last := -1
	for j := range pos {
		if pos[j] < 0 {
			pos[j] = last
		} else {
			last = pos[j]
		}
	}
	seq := make([]int, len(got))
	for j := range seq {
		seq[j] = j
	}
	sort.SliceStable(seq, func(a, b int) bool { return pos[seq[a]] < pos[seq[b]] })

	var r, g []rune
	for _, b := range ref {
		r = append(r, stripSpace(b.text)...)
	}
	for _, j := range seq {
		g = append(g, stripSpace(got[j].text)...)
	}
	return float64(lev(r, g)) / float64(max(1, len(r)))
}

// order is the fraction of consecutive matched reference blocks whose output positions
// also increase. It is the one dimension a pure reordering moves, since matching itself
// is by content and ignores where either block sits.
func order(pairs []pairing) float64 {
	seen, kept, last := 0, 0, -1
	for _, p := range pairs {
		if p.idx < 0 {
			continue
		}
		if last >= 0 {
			seen++
			if p.idx > last {
				kept++
			}
		}
		last = p.idx
	}
	if seen == 0 {
		return 1
	}
	return float64(kept) / float64(seen)
}

func allWords(d document) []string {
	var w []string
	for _, b := range d.blocks {
		w = append(w, words(b.text)...)
	}
	return w
}

// bagScores is Recall and Precision: multiset overlap of word tokens, case-sensitive,
// so a word whose case changed counts as missing. Block matching folds case and this
// does not: the block is the same block, and the word is not the same word.
func bagScores(ref, got []string) (recall, precision float64) {
	m := map[string]int{}
	for _, x := range ref {
		m[x]++
	}
	tp := 0
	for _, x := range got {
		if m[x] > 0 {
			m[x]--
			tp++
		}
	}
	return float64(tp) / float64(max(1, len(ref))), float64(tp) / float64(max(1, len(got)))
}

func headingsF1(ref, got []block) (float64, int) {
	rh, gh := headingKeys(ref), headingKeys(got)
	return f1(rh, gh), len(rh)
}

func headingKeys(blocks []block) []string {
	var out []string
	for _, b := range blocks {
		if b.kind == "heading" {
			out = append(out, fmt.Sprintf("%d|%s", b.level, norm(b.text)))
		}
	}
	return out
}

// tablesF1 keys a cell by which reference table its own table matched, not by position
// in either document, so a table that moved doesn't cost its cells. An output table
// that never matched a reference table has nothing to key against; its cells are kept
// anyway, each under an index no reference table can ever hold, so they still count as
// false positives rather than silently vanishing from the comparison.
func tablesF1(ref, got []block, pairs []pairing) (float64, int) {
	refTableIdx := map[int]int{}
	gotTableIdx := map[int]int{}
	n := 0
	for i, b := range ref {
		if b.kind != "table" {
			continue
		}
		refTableIdx[i] = n
		if p := pairs[i]; p.idx >= 0 {
			gotTableIdx[p.idx] = n
		}
		n++
	}

	var rt []string
	refCells := 0
	for i, b := range ref {
		if b.kind != "table" {
			continue
		}
		for r, row := range b.cells {
			for c, cell := range row {
				rt = append(rt, fmt.Sprintf("%d,%d,%d|%s", refTableIdx[i], r, c, norm(cell)))
				refCells++
			}
		}
	}

	var gt []string
	unmatched := -1
	for j, b := range got {
		if b.kind != "table" {
			continue
		}
		idx, ok := gotTableIdx[j]
		if !ok {
			idx, unmatched = unmatched, unmatched-1
		}
		for r, row := range b.cells {
			for c, cell := range row {
				gt = append(gt, fmt.Sprintf("%d,%d,%d|%s", idx, r, c, norm(cell)))
			}
		}
	}
	return f1(rt, gt), refCells
}

func stylesF1(ref, got []span) (float64, int) {
	return f1(spanKeys(ref), spanKeys(got)), len(ref)
}

func spanKeys(spans []span) []string {
	out := make([]string, len(spans))
	for i, x := range spans {
		out[i] = x.kind + "|" + x.text
	}
	return out
}

// f1 is the harmonic mean of precision and recall over two multisets of keys. It backs
// every dimension where "the right occurrence, under the right label" is what matters
// and position isn't part of the question — headings, table cells, and inline style.
func f1(ref, got []string) float64 {
	if len(ref) == 0 && len(got) == 0 {
		return 1
	}
	m := map[string]int{}
	for _, x := range ref {
		m[x]++
	}
	tp := 0
	for _, x := range got {
		if m[x] > 0 {
			m[x]--
			tp++
		}
	}
	if tp == 0 {
		return 0
	}
	p, r := float64(tp)/float64(len(got)), float64(tp)/float64(len(ref))
	return 2 * p * r / (p + r)
}

// f1Count is f1 for counts already reduced to true positives, used for Blocks: tp is
// the number of intact matches, nref and ngot the two block counts.
func f1Count(tp, nref, ngot int) float64 {
	if nref == 0 && ngot == 0 {
		return 1
	}
	if tp == 0 {
		return 0
	}
	p, r := float64(tp)/float64(ngot), float64(tp)/float64(nref)
	return 2 * p * r / (p + r)
}

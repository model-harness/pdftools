package sectionize

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/tag"
)

// notes reads a tagged document's footnotes before the tree is built: each Note element whose
// text opens with a label, and the element beside it that says the same label, which is its
// mark (ADR 0039).
//
// The five tagged standards on disk that declare notes, 25 of them, all set one shape:
//
//	P "IETF RFC 5480:2009, Elliptic Curve Cryptography …"
//	  Reference > Span "1)"
//	  Note > P "1)  https://datatracker…"
//
// Without this the Note's paragraph is a block of its own after the one that cites it, its
// label kept as text and its mark glued to the word before. So the Note becomes a
// RoleFootnote, its label taken from its text, and the mark's spans carry the label in
// Span.Note, which the Markdown sink writes as a GFM reference and its definition.
//
// ISO 32000-2 sets its four the other way: each Note a sibling of the paragraphs, at the
// Part's level, and its mark not an element but a raised digit inside an earlier
// paragraph's text, a span in a smaller size than the one before it. markOf reads both.
//
// Read before visit rather than during it, because the mark is gathered into the citing
// paragraph before visit reaches the Note. A mark is a span rather than its marked content,
// since a raised digit shares its MCID with the sentence it sits in.
type notes struct {
	label map[*tag.Elem]string // each Note's GFM label
	raw   map[*tag.Elem]string // and the label as its text spells it, "1)"
	marks map[*doc.Span]string // a mark's spans, by the label of its note
}

// noteLabel is what a note's text can open with: a number of up to three digits, with a
// closing parenthesis or a full stop or neither, or up to three of the symbols a typesetter
// counts notes with.
var noteLabel = regexp.MustCompile(`^(?:[0-9]{1,3}[).]?|[*†‡§¶‖]{1,3})$`)

func readNotes(tr *tag.Tree, ix *index) notes {
	n := notes{label: map[*tag.Elem]string{}, raw: map[*tag.Elem]string{}, marks: map[*doc.Span]string{}}
	used := map[string]bool{}
	symbols := 0
	tr.Walk(func(e *tag.Elem, _ int) bool {
		if e.Role != tag.RoleNote {
			return true
		}
		raw := leadingLabel(ix.text(e))
		if !noteLabel.MatchString(raw) {
			return true
		}
		mark := markOf(e, raw, ix)
		if len(mark) == 0 {
			return true
		}
		label := strings.TrimRight(raw, ").")
		if !unicode.IsDigit([]rune(label)[0]) {
			symbols++
			label = "fn" + strconv.Itoa(symbols)
		}
		for base, i := label, 2; used[label]; i++ {
			label = base + "-" + strconv.Itoa(i)
		}
		used[label] = true
		n.label[e], n.raw[e] = label, raw
		for _, sp := range mark {
			n.marks[sp] = label
		}
		return true
	})
	return n
}

// markOf returns the spans of a note's mark: the element before it in its parent whose text
// is the note's label, the nearest, since a sentence citing two notes cites each nearer its own;
// or failing one, the last span saying the label in a smaller size than the span before it, in
// the nearest earlier sibling that has one.
func markOf(note *tag.Elem, raw string, ix *index) []*doc.Span {
	var before []*tag.Elem
	for _, k := range note.Parent.Kids {
		if k == note {
			break
		}
		before = append(before, k)
	}
	for i := len(before) - 1; i >= 0; i-- {
		if k := before[i]; strings.TrimSpace(ix.text(k)) == raw {
			return ix.spansOf(k)
		}
	}
	for i := len(before) - 1; i >= 0; i-- {
		spans := ix.spansOf(before[i])
		for j := len(spans) - 1; j > 0; j-- {
			if strings.TrimSpace(spans[j].Text) == raw && spans[j].Style.Size <= raisedSize*spans[j-1].Style.Size {
				return spans[j : j+1]
			}
		}
	}
	return nil
}

// raisedSize is the most a mark set inside text can be of the size before it: layout's
// markSize, for the same marks.
const raisedSize = 0.8

// leadingLabel returns the word a note's text opens with, when there is text after it: a note
// that is only its label would be an empty footnote, which the outline drops, and its mark a
// reference to nothing, which GFM writes as text.
func leadingLabel(s string) string {
	if f := strings.Fields(s); len(f) > 1 {
		return f[0]
	}
	return ""
}

// note emits a Note as a footnote: its text, the label it opens with taken off, as one block
// whose Marker is its label.
func (b *builder) note(e *tag.Elem) {
	var spans []*doc.Span
	var nested []*tag.Elem
	var pg span
	b.gather(e, &spans, &nested, &pg, true, false)
	b.emitItem(e, doc.RoleFootnote, spans, pg, b.notes.label[e])
	for _, n := range nested {
		b.visit(n)
	}
}

// stripNote takes a note's label, as its text spells it, and the space after it off the front
// of its block.
func stripNote(blk *doc.Block, raw string) {
	for i := range blk.Spans {
		s := &blk.Spans[i]
		t := strings.TrimLeftFunc(s.Text, unicode.IsSpace)
		if t == "" {
			s.Text = ""
			continue
		}
		if raw != "" {
			n := min(len(raw), len(t))
			if !strings.HasPrefix(raw, t[:n]) {
				return
			}
			t, raw = t[n:], raw[n:]
		}
		s.Text = strings.TrimLeftFunc(t, unicode.IsSpace)
		if raw == "" && s.Text != "" {
			return
		}
	}
}

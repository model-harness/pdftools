package sectionize

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/tag"
)

// outlineBlocks is every block of an outline, preamble first then sections in order.
func outlineBlocks(o *doc.Outline) []doc.Block {
	out := append([]doc.Block(nil), o.Preamble...)
	o.Walk(func(s *doc.Section) bool {
		out = append(out, s.Blocks...)
		return true
	})
	return out
}

// noted renders an outline's blocks as role:text, a mark as text^label, and a footnote's
// label as [label].
func noted(o *doc.Outline) string {
	var out []string
	for _, b := range outlineBlocks(o) {
		var sb strings.Builder
		sb.WriteString(string(b.Role) + ":")
		if b.Role == doc.RoleFootnote {
			sb.WriteString("[" + b.Note + "|" + b.Marker + "]")
		}
		for _, s := range b.Spans {
			sb.WriteString(s.Text)
			if s.Note != "" {
				sb.WriteString("^" + s.Note)
			}
		}
		out = append(out, sb.String())
	}
	return strings.Join(out, " | ")
}

// reference is ISO/TS 32002's shape: a paragraph citing a note by a Reference beside it, the
// Note inside the paragraph, its text opening with the label.
func reference(label, note string) (*doc.Document, *tag.Tree) {
	d := docWith(sp{1, 1, "IETF RFC 5480, "}, sp{1, 2, label}, sp{1, 3, note})
	p := el(tag.RoleP, 1, 1)
	kids(p, kids(el("Reference", 1), el(tag.RoleSpan, 1, 2)), kids(el(tag.RoleNote, 1), el(tag.RoleP, 1, 3)))
	return d, tree(p)
}

// TestANoteIsAFootnoteItsReferenceMarks is the shape four of the tagged standards use.
func TestANoteIsAFootnoteItsReferenceMarks(t *testing.T) {
	d, tr := reference("1)", "1)  https://datatracker.ietf.org")
	out, _ := Tagged(d, tr, DefaultOptions)
	if want := "paragraph:IETF RFC 5480, 1)^1 | footnote:[1|1)]https://datatracker.ietf.org"; noted(out) != want {
		t.Errorf("got  %s\nwant %s", noted(out), want)
	}
}

// TestANoteMarkedByARaisedDigit is ISO 32000-2's shape: the Note a sibling of the paragraph that
// cites it, its mark a digit in that paragraph's text, set smaller than the text before it.
func TestANoteMarkedByARaisedDigit(t *testing.T) {
	for _, c := range []struct {
		name string
		size float64
		want string
	}{
		{"set at 0.8 of the text", 8, "paragraph:derived from PostScript1^1 page description | footnote:[1|1]PostScript is a trademark."},
		{"set larger", 8.01, "paragraph:derived from PostScript1 page description | paragraph:1 PostScript is a trademark."},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := docWith(sp{1, 1, "derived from PostScript"}, sp{1, 2, "1"}, sp{1, 3, " page description"}, sp{1, 4, "1 PostScript is a trademark."})
			for i := range d.Pages[0].Blocks[0].Spans {
				d.Pages[0].Blocks[0].Spans[i].Style.Size = 10
			}
			d.Pages[0].Blocks[0].Spans[1].Style.Size = c.size
			part := kids(el(tag.RolePart, 1), el(tag.RoleP, 1, 1, 2, 3), el(tag.RoleNote, 1, 4))
			out, _ := Tagged(d, tree(part), DefaultOptions)
			if noted(out) != c.want {
				t.Errorf("got  %s\nwant %s", noted(out), c.want)
			}
		})
	}
}

// TestANoteNeedsALabelAndAMark: a Note whose text opens with no label, or whose label nothing
// beside it says, is left where it was.
func TestANoteNeedsALabelAndAMark(t *testing.T) {
	d, tr := reference("1)", "See the annex.")
	out, _ := Tagged(d, tr, DefaultOptions)
	if strings.Contains(noted(out), "footnote") || strings.Contains(noted(out), "^") {
		t.Errorf("no label: %s", noted(out))
	}
	d, tr = reference("1)", "1)")
	out, _ = Tagged(d, tr, DefaultOptions)
	if strings.Contains(noted(out), "footnote") || strings.Contains(noted(out), "^") {
		t.Errorf("only a label: %s", noted(out))
	}
	d, tr = reference("See", "See the annex.")
	out, _ = Tagged(d, tr, DefaultOptions)
	if strings.Contains(noted(out), "footnote") || strings.Contains(noted(out), "^") {
		t.Errorf("a word for a label: %s", noted(out))
	}
	d, tr = reference("2)", "1)  https://datatracker.ietf.org")
	out, _ = Tagged(d, tr, DefaultOptions)
	if strings.Contains(noted(out), "footnote") || strings.Contains(noted(out), "^") {
		t.Errorf("no mark: %s", noted(out))
	}
}

// TestNoteLabels: a number is its own label, a second note with it gets a count, and a symbol
// becomes "fn" and a count, which GFM cannot read as emphasis.
func TestNoteLabels(t *testing.T) {
	d := docWith(sp{1, 1, "A"}, sp{1, 2, "1)"}, sp{1, 3, "1) one"}, sp{1, 4, "B"}, sp{1, 5, "1)"}, sp{1, 6, "1) two"},
		sp{1, 7, "C"}, sp{1, 8, "*"}, sp{1, 9, "* three"})
	cite := func(text, mark, note int) *tag.Elem {
		return kids(el(tag.RoleP, 1, text), el(tag.RoleSpan, 1, mark), kids(el(tag.RoleNote, 1), el(tag.RoleP, 1, note)))
	}
	out, _ := Tagged(d, tree(cite(1, 2, 3), cite(4, 5, 6), cite(7, 8, 9)), DefaultOptions)
	want := "paragraph:A1)^1 | footnote:[1|1)]one | paragraph:B1)^1-2 | footnote:[1-2|1)]two | paragraph:C*^fn1 | footnote:[fn1|*]three"
	if noted(out) != want {
		t.Errorf("got  %s\nwant %s", noted(out), want)
	}
	for raw, ok := range map[string]bool{"1": true, "12)": true, "3.": true, "†": true, "1234": false, "a)": false, "1.5": false} {
		if noteLabel.MatchString(raw) != ok {
			t.Errorf("noteLabel(%q) = %v, want %v", raw, !ok, ok)
		}
	}
}

// TestANoteTakesTheNearestMark: two elements before a note say its label, and the one nearer
// it is its mark.
func TestANoteTakesTheNearestMark(t *testing.T) {
	d := docWith(sp{1, 1, "A"}, sp{1, 2, "1)"}, sp{1, 3, "B"}, sp{1, 4, "1)"}, sp{1, 5, "1) note"})
	p := kids(el(tag.RoleP, 1, 1), el(tag.RoleSpan, 1, 2), el(tag.RoleSpan, 1, 4), kids(el(tag.RoleNote, 1), el(tag.RoleP, 1, 5)))
	p.Content = append(p.Content, tag.MCRef{MCID: 3, Page: 1})
	out, _ := Tagged(d, tree(p), DefaultOptions)
	if got := noted(out); !strings.Contains(got, "1)^1") || strings.Count(got, "^1") != 1 || !strings.HasSuffix(strings.Split(got, " | ")[0], "1)^1") {
		t.Errorf("got %s, want only the second 1) marked", got)
	}
}

// TestStripNote takes a label off the front of a note whatever spans it is split across.
func TestStripNote(t *testing.T) {
	for _, c := range []struct {
		spans []string
		raw   string
		want  string
	}{
		{[]string{"1)  https://x"}, "1)", "https://x"},
		{[]string{" ", "1", ")", " text"}, "1)", "text"},
		{[]string{"* starred"}, "*", "starred"},
		{[]string{"2) other"}, "1)", "2) other"},
	} {
		var b doc.Block
		for _, s := range c.spans {
			b.Spans = append(b.Spans, doc.Span{Text: s})
		}
		stripNote(&b, c.raw)
		if got := b.Text(); got != c.want {
			t.Errorf("stripNote(%q, %q) = %q, want %q", c.spans, c.raw, got, c.want)
		}
	}
}

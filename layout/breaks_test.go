package layout

import (
	"slices"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// tline is one line of a paragraph for PageBreaks: its text, its left and right edges, the
// bottom of its box, and whether it opens a span of its own rather than running on in the
// one before.
type tline struct {
	text   string
	x0, x1 float64
	y      float64
	span   bool
}

var bodyFace = doc.Style{Font: "Body", Size: 10}

// para builds a paragraph as extract does: lines joined by a trailing space, a span running on
// across lines, and each line's start recorded. A space is a quarter of the size, 2.5pt here,
// and the measure runs from 72 to 540.
func para(page int, st doc.Style, mcid int, lines ...tline) doc.Block {
	b := doc.Block{Role: doc.RoleParagraph}
	for i, l := range lines {
		box := geom.Rect{X0: l.x0, Y0: l.y, X1: l.x1, Y1: l.y + st.Size}
		if i > 0 {
			b.Spans[len(b.Spans)-1].Text += " "
		}
		start := doc.Line{Box: box, Page: page, Size: st.Size, Space: st.Size / 4}
		if i == 0 || l.span {
			start.Span = len(b.Spans)
			b.Spans = append(b.Spans, doc.Span{Text: l.text, Style: st, Box: box, Page: page, MCID: mcid})
		} else {
			n := len(b.Spans) - 1
			start.Span, start.Offset = n, len(b.Spans[n].Text)
			b.Spans[n].Text += l.text
			b.Spans[n].Box = b.Spans[n].Box.Union(box)
		}
		b.Lines = append(b.Lines, start)
		b.Box = b.Box.Union(box)
	}
	b.SetMCIDs()
	return b
}

// third is a paragraph at the foot of page 1, opened with a three-space indent and ending on
// a short line.
func third() doc.Block {
	return para(1, bodyFace, 1,
		tline{text: "The third lake lies in a hollow between two", x0: 79.5, x1: 540, y: 124},
		tline{text: "ridges, and the path to it climbs for an hour", x0: 72, x1: 540, y: 112},
		tline{text: "through the", x0: 72, x1: 300, y: 100})
}

// fourth is an ordinary paragraph below a continuation on page 2.
func fourth() doc.Block {
	return para(2, bodyFace, 9,
		tline{text: "The fourth lake is the smallest, and it", x0: 79.5, x1: 540, y: 600},
		tline{text: "freezes first.", x0: 72, x1: 200, y: 588})
}

func texts(blocks []doc.Block) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, b.Text())
	}
	return out
}

// TestAParagraphCutByAPageBreakIsOneBlock is the join: the halves are one block on the first
// page, the wrap space trails the first half as extract places it, and the second half's
// spans and lines keep their own page, rebased onto the joined block.
func TestAParagraphCutByAPageBreakIsOneBlock(t *testing.T) {
	cont := para(2, bodyFace, 2, tline{text: "pines to the water.", x0: 72, x1: 200, y: 900})
	d := pages([]doc.Block{third()}, []doc.Block{cont, fourth()})
	if n := PageBreaks(d); n != 1 {
		t.Fatalf("joined %d, want 1", n)
	}
	got := d.Pages[0].Blocks
	if len(got) != 1 || got[0].Text() != "The third lake lies in a hollow between two ridges, and the path to it climbs for an hour through the pines to the water." {
		t.Fatalf("page 1 = %q", texts(got))
	}
	if rest := texts(d.Pages[1].Blocks); !slices.Equal(rest, []string{"The fourth lake is the smallest, and it freezes first."}) {
		t.Errorf("page 2 = %q, want only the fourth paragraph", rest)
	}
	b := got[0]
	if len(b.Spans) != 2 || !strings.HasSuffix(b.Spans[0].Text, "the ") || b.Spans[1].Text != "pines to the water." || b.Spans[1].Page != 2 {
		t.Errorf("spans = %+v: the space trails the first half and the second keeps its page", b.Spans)
	}
	if len(b.Lines) != 4 || b.Lines[3].Span != 1 || b.Lines[3].Offset != 0 || b.Lines[3].Page != 2 {
		t.Errorf("lines = %+v: the second half's line starts its own span, on page 2", b.Lines)
	}
	if !slices.Equal(b.MCIDs, []int{1, 2}) {
		t.Errorf("MCIDs = %v, want [1 2]", b.MCIDs)
	}
}

// TestPageBreaksAsksEachQuestion takes the questions one at a time: a paragraph carries on
// over the break only if it starts flush at the same size after a paragraph at the foot of its
// page, and either starts lower case or follows a full line in the face that line ends in.
func TestPageBreaksAsksEachQuestion(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(tail, cont *doc.Block, page1 *[]doc.Block)
		join bool
	}{
		{"lower case after a short line", nil, true},
		{"upper case after a short line", func(_, c *doc.Block, _ *[]doc.Block) { capital(c) }, false},
		{"upper case after a full line", func(tl, c *doc.Block, _ *[]doc.Block) { capital(c); endAt(tl, 540) }, true},
		{"a space short of the edge", func(tl, c *doc.Block, _ *[]doc.Block) { capital(c); endAt(tl, 537.5) }, true},
		{"more than a space short", func(tl, c *doc.Block, _ *[]doc.Block) { capital(c); endAt(tl, 537.4) }, false},
		{"upper case after a full line in another face", func(tl, c *doc.Block, _ *[]doc.Block) {
			capital(c)
			endAt(tl, 540)
			c.Spans[0].Style.Italic = true
		}, false},
		{"upper case after a paragraph of one full line", func(tl, c *doc.Block, _ *[]doc.Block) {
			capital(c)
			*tl = para(1, bodyFace, 1, tline{text: "One line through the", x0: 72, x1: 540, y: 100})
		}, false},
		{"under a space from the margin", func(_, c *doc.Block, _ *[]doc.Block) { shift(c, 74.4) }, true},
		{"indented by a space", func(_, c *doc.Block, _ *[]doc.Block) { shift(c, 74.5) }, false},
		// 8 and 8.48, because 1.06 times 8 is 8.48 to the last bit and times 10 is not 10.6.
		{"in a size at the bound", func(tl, c *doc.Block, _ *[]doc.Block) { resize(tl, 8); resize(c, 8.48) }, true},
		{"in a size past it", func(tl, c *doc.Block, _ *[]doc.Block) { resize(tl, 8); resize(c, 8.49) }, false},
		{"text more than a line below the first half", func(_, _ *doc.Block, p1 *[]doc.Block) {
			low := para(1, bodyFace, 7, tline{text: "a note", x0: 72, x1: 120, y: 89.9})
			*p1 = append([]doc.Block{low}, *p1...)
		}, false},
		{"text a line below the first half", func(_, _ *doc.Block, p1 *[]doc.Block) {
			low := para(1, bodyFace, 7, tline{text: "a note", x0: 72, x1: 120, y: 90})
			*p1 = append([]doc.Block{low}, *p1...)
		}, true},
		{"upper case after the widest line of a narrow paragraph", func(tl, c *doc.Block, p1 *[]doc.Block) {
			capital(c)
			wide := para(1, bodyFace, 7, tline{text: "A full line above.", x0: 72, x1: 540, y: 700})
			*p1 = append([]doc.Block{wide}, *p1...)
			*tl = para(1, bodyFace, 1,
				tline{text: "Figure 1:", x0: 72, x1: 120, y: 112},
				tline{text: "a dog with his goofy grin", x0: 72, x1: 300, y: 100})
		}, false},
		{"lower case after a leading space", func(_, c *doc.Block, _ *[]doc.Block) { c.Spans[0].Text = " " + c.Spans[0].Text }, true},
		{"no lines either side", func(tl, c *doc.Block, _ *[]doc.Block) { tl.Lines, c.Lines = nil, nil }, false},
		{"a heading after the break", func(_, c *doc.Block, _ *[]doc.Block) { c.Role = doc.RoleHeading }, false},
		{"a list item before the break", func(tl, _ *doc.Block, _ *[]doc.Block) { tl.Role = doc.RoleListItem }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tail := third()
			cont := para(2, bodyFace, 2, tline{text: "pines to the water.", x0: 72, x1: 200, y: 900})
			var page1 []doc.Block
			if c.edit != nil {
				c.edit(&tail, &cont, &page1)
			}
			page1 = append(page1, tail)
			d := pages(page1, []doc.Block{cont, fourth()})
			if n := PageBreaks(d); (n == 1) != c.join {
				t.Errorf("joined %d, want join = %v; page 1 %q", n, c.join, texts(d.Pages[0].Blocks))
			}
		})
	}
}

func capital(b *doc.Block) { b.Spans[0].Text = "P" + b.Spans[0].Text[1:] }

// endAt moves the right edge of a paragraph's last line.
func endAt(b *doc.Block, x1 float64) { b.Lines[len(b.Lines)-1].Box.X1 = x1 }

// shift moves a paragraph's first line's left edge.
func shift(b *doc.Block, x0 float64) { b.Lines[0].Box.X0 = x0 }

func resize(b *doc.Block, size float64) {
	for i := range b.Lines {
		b.Lines[i].Size, b.Lines[i].Space = size, size/4
	}
	for i := range b.Spans {
		b.Spans[i].Style.Size = size
	}
}

// TestPageBreaksSkipArtifacts: a folio below the first half and a running header above the
// second stand between them on the page, and are not text the question is asked of; nor is a
// block that drew only a space.
func TestPageBreaksSkipArtifacts(t *testing.T) {
	folio := para(1, bodyFace, -1, tline{text: "1", x0: 300, x1: 305, y: 40})
	folio.Role = doc.RoleArtifact
	header := para(2, bodyFace, -1, tline{text: "Field Notes", x0: 250, x1: 350, y: 960})
	header.Role = doc.RoleArtifact
	cont := para(2, bodyFace, 2, tline{text: "pines to the water.", x0: 72, x1: 200, y: 900})
	blank := para(1, bodyFace, -1, tline{text: " ", x0: 60, x1: 62, y: 20})
	blank2 := para(2, bodyFace, -1, tline{text: " ", x0: 60, x1: 62, y: 950})
	d := pages([]doc.Block{third(), folio, blank}, []doc.Block{header, blank2, cont, fourth()})
	if n := PageBreaks(d); n != 1 {
		t.Fatalf("joined %d, want 1", n)
	}
	if got := texts(d.Pages[1].Blocks); len(got) != 3 || got[0] != "Field Notes" {
		t.Errorf("page 2 = %q, want the header kept and the continuation gone", got)
	}
	if !strings.HasSuffix(d.Pages[0].Blocks[0].Text(), "through the pines to the water.") {
		t.Errorf("page 1 = %q, want the continuation joined to the paragraph", texts(d.Pages[0].Blocks))
	}
	if len(d.Pages[0].Blocks) != 3 || d.Pages[0].Blocks[1].Role != doc.RoleArtifact {
		t.Errorf("page 1 = %q, want the folio kept", texts(d.Pages[0].Blocks))
	}
}

// TestAParagraphOverThreePagesIsOneBlock: a page holding nothing but the middle of a paragraph
// passes it on to the next, and the middle page's lines, now in the joined block, are what the
// next break is judged by.
func TestAParagraphOverThreePagesIsOneBlock(t *testing.T) {
	for _, c := range []struct {
		name    string
		middle  float64 // the right edge of the middle page's last line
		oneLine bool    // the middle page holds one line
		next    string
		joined  int
	}{
		{"lower case", 300, false, "and on to the shore.", 2},
		{"upper case after a full line", 540, false, "And on to the shore.", 2},
		{"upper case after a short line", 300, false, "And on to the shore.", 1},
		{"upper case after one full line", 540, true, "And on to the shore.", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			lines := []tline{{text: "pines, then down a slope of scree", x0: 72, x1: 540, y: 900},
				{text: "to the water", x0: 72, x1: c.middle, y: 100}}
			if c.oneLine {
				lines = lines[1:]
			}
			mid := para(2, bodyFace, 2, lines...)
			last := para(3, bodyFace, 3, tline{text: c.next, x0: 72, x1: 200, y: 900})
			d := pages([]doc.Block{third()}, []doc.Block{mid}, []doc.Block{last})
			if n := PageBreaks(d); n != c.joined {
				t.Fatalf("joined %d, want %d", n, c.joined)
			}
			b := d.Pages[0].Blocks[0]
			var pages []int
			for _, ln := range b.Lines {
				pages = append(pages, ln.Page)
			}
			want := []int{1, 1, 1, 2, 2}
			if c.oneLine {
				want = want[:4]
			}
			if c.joined == 2 {
				want = append(want, 3)
			}
			if !slices.Equal(pages, want) {
				t.Errorf("lines on pages %v, want %v", pages, want)
			}
			if len(d.Pages[1].Blocks) != 0 {
				t.Errorf("page 2 = %q, want nothing left", texts(d.Pages[1].Blocks))
			}
		})
	}
}

// TestPageBreaksDoNotSkipAPage: a break joins only the pages either side of it, so a page with
// no body text between them ends the paragraph.
func TestPageBreaksDoNotSkipAPage(t *testing.T) {
	cont := para(3, bodyFace, 2, tline{text: "pines to the water.", x0: 72, x1: 200, y: 900})
	d := pages([]doc.Block{third()}, nil, []doc.Block{cont})
	if n := PageBreaks(d); n != 0 {
		t.Errorf("joined %d across an empty page, want 0", n)
	}
}

// TestPageBreaksKeepTheWordAHyphenHolds: the break is a wrap, so extract's rule decides the
// space, and a hyphen holding a word together across it gets none.
func TestPageBreaksKeepTheWordAHyphenHolds(t *testing.T) {
	tail := para(1, bodyFace, 1,
		tline{text: "The third lake lies in a hollow between two", x0: 79.5, x1: 540, y: 112},
		tline{text: "ridges, and the path to it climbs for an hour through pine for-", x0: 72, x1: 540, y: 100})
	cont := para(2, bodyFace, 2, tline{text: "est to the water.", x0: 72, x1: 200, y: 900})
	d := pages([]doc.Block{tail}, []doc.Block{cont})
	if n := PageBreaks(d); n != 1 {
		t.Fatalf("joined %d, want 1", n)
	}
	if got := d.Pages[0].Blocks[0].Text(); !strings.HasSuffix(got, "pine for-est to the water.") {
		t.Errorf("text = %q, want the hyphenated word unbroken", got)
	}
}

// TestAContinuationEndsAtTheIndentItsParagraphOpenedWith is extract's indent rule for the block
// extract cannot apply it to: the first line of a continuation is flush, so the indent a later
// line repeats is the one the paragraph opened with on the page before.
func TestAContinuationEndsAtTheIndentItsParagraphOpenedWith(t *testing.T) {
	cont := func(indent float64, newSpan bool) doc.Block {
		return contAfter(0, indent, newSpan)
	}
	opened := func(first float64) doc.Block {
		return para(1, bodyFace, 1,
			tline{text: "The third lake lies in a hollow between two", x0: first, x1: 540, y: 112},
			tline{text: "ridges, and the path", x0: 72, x1: 300, y: 100})
	}
	for _, c := range []struct {
		name  string
		tail  doc.Block
		cont  doc.Block
		split bool
	}{
		{"the paragraph's own indent", third(), cont(7.5, false), true},
		{"its indent opening a span", third(), withMCID(cont(7.5, true), 1, 3), true},
		{"half a space more", third(), cont(8.75, false), true},
		{"over half a space more", third(), cont(8.76, false), false},
		{"an indent of its own", third(), cont(12.5, false), false},
		{"an indent of one space", opened(74.5), cont(2.5, false), true},
		{"an indent of six spaces", opened(87), cont(15, false), true},
		{"an indent of one space over a wandering margin", para(1, bodyFace, 1,
			tline{text: "The third lake lies in a hollow between two", x0: 74.5, x1: 540, y: 124},
			tline{text: "ridges, and the path to it climbs for an", x0: 73, x1: 540, y: 112},
			tline{text: "hour through the", x0: 72, x1: 300, y: 100}), cont(2.5, false), true},
		{"after a paragraph indented further", opened(87.1), cont(15.1, false), false},
		{"after a blank first line", third(), para(2, bodyFace, 2,
			tline{text: " ", x0: 72, x1: 74, y: 900},
			tline{text: "the fourth lake is the smallest, and", x0: 79.5, x1: 540, y: 888, span: true},
			tline{text: "it freezes first.", x0: 72, x1: 200, y: 876}), false},
		{"after a line half a space in", third(), contAfter(1.25, 7.5, false), true},
		{"after a line further in", third(), contAfter(1.26, 7.5, false), false},
		{"after a margin within half a space", para(1, bodyFace, 1,
			tline{text: "The third lake lies in a hollow between two", x0: 79.5, x1: 540, y: 124},
			tline{text: "ridges, and the path to it climbs for an", x0: 72, x1: 540, y: 112},
			tline{text: "hour through the", x0: 73.25, x1: 300, y: 100}), cont(7.5, false), true},
		{"after a paragraph opened flush", para(1, bodyFace, 1,
			tline{text: "The third lake lies in a hollow between two", x0: 72, x1: 540, y: 112},
			tline{text: "ridges, and the path", x0: 72, x1: 300, y: 100}), cont(7.5, false), false},
		{"after a paragraph with no margin", para(1, bodyFace, 1,
			tline{text: "The third lake lies in a hollow between two", x0: 79.5, x1: 540, y: 124},
			tline{text: "ridges, and the path to it climbs for an", x0: 72, x1: 540, y: 112},
			tline{text: "hour through the", x0: 73.3, x1: 300, y: 100}), cont(7.5, false), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := pages([]doc.Block{c.tail}, []doc.Block{c.cont})
			if n := PageBreaks(d); n != 1 {
				t.Fatalf("joined %d, want 1", n)
			}
			joined := d.Pages[0].Blocks[0].Text()
			if !c.split {
				if len(d.Pages[1].Blocks) != 0 || !strings.HasSuffix(joined, "it freezes first.") {
					t.Errorf("page 2 = %q, want the whole continuation joined", texts(d.Pages[1].Blocks))
				}
				return
			}
			if !strings.HasSuffix(joined, "scree to the water.") {
				t.Errorf("joined = %q, want it to end where the fourth paragraph starts", joined)
			}
			if len(d.Pages[1].Blocks) != 1 {
				t.Fatalf("page 2 = %q, want the fourth paragraph", texts(d.Pages[1].Blocks))
			}
			rest := d.Pages[1].Blocks[0]
			if rest.Text() != "The fourth lake is the smallest, and it freezes first." {
				t.Errorf("rest = %q", rest.Text())
			}
			if len(rest.Lines) != 2 || rest.Lines[0].Span != 0 || rest.Lines[0].Offset != 0 ||
				rest.Lines[1].Span != 0 || rest.Lines[1].Offset != len("The fourth lake is the smallest, and ") {
				t.Errorf("rest's lines = %+v, want them rebased onto its own spans", rest.Lines)
			}
			if want := (geom.Rect{X0: 72, Y0: 864, X1: 540, Y1: 886}); rest.Box != want {
				t.Errorf("rest's box = %v, want %v", rest.Box, want)
			}
			if want := []int{rest.Spans[0].MCID}; !slices.Equal(rest.MCIDs, want) {
				t.Errorf("rest's MCIDs = %v", rest.MCIDs)
			}
			first := d.Pages[0].Blocks[0]
			if want := len(c.tail.Lines) + 2; len(first.Spans) != 2 || len(first.Lines) != want {
				t.Errorf("joined block has %d spans and %d lines, want 2 and %d", len(first.Spans), len(first.Lines), want)
			}
		})
	}
}

// contAfter is a continuation whose second line starts second right of the margin and whose
// third starts indent right of it: the fourth paragraph's first line.
func contAfter(second, indent float64, newSpan bool) doc.Block {
	return para(2, bodyFace, 2,
		tline{text: "pines, then down a slope of scree", x0: 72, x1: 540, y: 900},
		tline{text: "to the water.", x0: 72 + second, x1: 200, y: 888},
		tline{text: "The fourth lake is the smallest, and", x0: 72 + indent, x1: 540, y: 876, span: newSpan},
		tline{text: "it freezes first.", x0: 72, x1: 200, y: 864})
}

// TestPageBreaksMeasureTheMarginByParagraphs: a list item's marker hangs left of the text, so
// the margin a continuation starts flush with is the paragraphs'.
func TestPageBreaksMeasureTheMarginByParagraphs(t *testing.T) {
	cont := para(2, bodyFace, 2, tline{text: "pines to the water.", x0: 72, x1: 200, y: 900})
	item := para(2, bodyFace, 3, tline{text: "• a hanging item", x0: 60, x1: 200, y: 600})
	item.Role = doc.RoleListItem
	d := pages([]doc.Block{third()}, []doc.Block{cont, item})
	if n := PageBreaks(d); n != 1 {
		t.Errorf("joined %d, want 1", n)
	}
}

// withMCID gives span i of b its own marked-content identifier.
func withMCID(b doc.Block, i, id int) doc.Block {
	b.Spans[i].MCID = id
	b.SetMCIDs()
	return b
}

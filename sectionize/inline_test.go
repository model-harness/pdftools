package sectionize

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/tag"
)

// at is a positioned run: its MCID, text and box, at size 10 unless it says otherwise.
func at(mcid int, text string, x0, y0, x1 float64) doc.Span {
	return doc.Span{Text: text, MCID: mcid, Page: 1, Box: geom.NewRect(x0, y0, x1, y0+10), Style: doc.Style{Size: 10}}
}

// page is a document of one page whose extractor blocks are blocks, each a list of spans.
func page(blocks ...[]doc.Span) *doc.Document {
	p := doc.Page{Number: 1, Box: geom.NewRect(0, 0, 612, 792)}
	for _, b := range blocks {
		blk := doc.Block{Role: doc.RoleParagraph, Spans: b}
		for _, s := range b {
			blk.Box = blk.Box.Union(s.Box)
		}
		blk.SetMCIDs()
		p.Blocks = append(p.Blocks, blk)
	}
	return &doc.Document{Meta: doc.Metadata{Tagged: true}, Pages: []doc.Page{p}}
}

// TestCodeInsideTextIsInline: a Code sharing its parent with text is part of that text, whether
// the parent owns the text (#21's sentence) or holds it in other kids (its table cell); a Code that
// is its parent's whole content is a listing, a block of its own.
func TestCodeInsideTextIsInline(t *testing.T) {
	d := page([]doc.Span{at(1, "by its ", 72, 700, 110), at(2, "sailing_id", 110, 700, 160), at(3, ", and", 160, 700, 190)})
	for _, c := range []struct {
		name string
		tr   *tag.Tree
		want []string
	}{
		{"owns its text", tree(interleaved(tag.RoleP, 1, 1, el(tag.RoleCode, 1, 2), 3)), []string{"paragraph:by its sailing_id, and"}},
		{"holds it in kids", tree(kids(el(tag.RoleTD, 1), el(tag.RoleNonStruct, 1, 1), el(tag.RoleCode, 1, 2), el(tag.RoleNonStruct, 1, 3))),
			[]string{"table_cell:by its sailing_id, and"}},
		{"a listing", tree(el(tag.RoleP, 1, 1), kids(el(tag.RoleP, 1), el(tag.RoleCode, 1, 2)), el(tag.RoleP, 1, 3)),
			[]string{"paragraph:by its ", "code:sailing_id", "paragraph:, and"}},
	} {
		out, _ := Tagged(clone(d), c.tr, DefaultOptions)
		var got []string
		for _, b := range out.Preamble {
			got = append(got, string(b.Role)+":"+b.Text())
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %q, want %q", c.name, got, c.want)
				break
			}
		}
	}
}

// clone is a copy of d deep enough for Tagged to rebuild from: its own blocks and spans.
func clone(d *doc.Document) *doc.Document {
	c := *d
	c.Pages = append([]doc.Page(nil), d.Pages...)
	for pi := range c.Pages {
		c.Pages[pi].Blocks = append([]doc.Block(nil), d.Pages[pi].Blocks...)
		for bi := range c.Pages[pi].Blocks {
			c.Pages[pi].Blocks[bi].Spans = append([]doc.Span(nil), d.Pages[pi].Blocks[bi].Spans...)
		}
	}
	return &c
}

// TestAWrapTheExtractorDidNotJoinIsASpace: an element whose text runs on to a line the extractor
// read as another block gets the wrap's space (#22); a join inside one of its blocks keeps the
// extractor's answer, a denominator under its slash and a line that does not return to the start
// get none, and neither does a run only partly lower.
func TestAWrapTheExtractorDidNotJoinIsASpace(t *testing.T) {
	label := func(second doc.Span) *doc.Document {
		return page([]doc.Span{at(5, "Monthly engaged installs", 47, 388, 140)}, []doc.Span{second})
	}
	for _, c := range []struct {
		name string
		d    *doc.Document
		want string
	}{
		{"two blocks", label(at(5, "(MAU)", 47, 375, 70)), "Monthly engaged installs (MAU)"},
		{"one block", page([]doc.Span{at(5, "Monthly engaged installs", 47, 388, 140), at(5, "(MAU)", 47, 375, 70)}), "Monthly engaged installs(MAU)"},
		{"not back at the line start", label(at(5, "(MAU)", 60, 375, 80)), "Monthly engaged installs(MAU)"},
		{"not below", label(at(5, "(MAU)", 47, 386, 70)), "Monthly engaged installs(MAU)"},
		{"after a slash", page([]doc.Span{at(5, "(x + y)/", 47, 388, 90)}, []doc.Span{at(5, "2", 47, 375, 52)}), "(x + y)/2"},
		{"on no line", page([]doc.Span{{Text: "Monthly", MCID: 5, Page: 1, Style: doc.Style{Size: 10}}}, []doc.Span{{Text: "(MAU)", MCID: 5, Page: 1, Style: doc.Style{Size: 10}}}), "Monthly(MAU)"},
		{"across a page", acrossPage(), "continuation"},
	} {
		p := el(tag.RoleP, 1, 5)
		if len(c.d.Pages) > 1 {
			p.Content = append(p.Content, tag.MCRef{MCID: 5, Page: 2})
		}
		out, _ := Tagged(c.d, tree(p), DefaultOptions)
		if len(out.Preamble) != 1 || out.Preamble[0].Text() != c.want {
			var got []string
			for _, b := range out.Preamble {
				got = append(got, b.Text())
			}
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestASubstitutedSpanJoinsAsItsText: a hyphen the tree replaces with an empty /ActualText is
// still the extractor's text between its neighbours, so the word it held stays one (ISO/TS 32005's
// "exposi-tion").
func TestASubstitutedSpanJoinsAsItsText(t *testing.T) {
	d := page([]doc.Span{at(1, "exposi", 72, 700, 110), at(2, "-", 110, 700, 114), at(3, "tion", 72, 688, 100)})
	soft := el(tag.RoleSpan, 1, 2)
	soft.ActualText = "\u00ad"
	out, _ := Tagged(d, tree(interleaved(tag.RoleP, 1, 1, soft, 3)), DefaultOptions)
	if len(out.Preamble) != 1 || out.Preamble[0].Text() != "exposition" {
		t.Errorf("%+v, want one paragraph, exposition", out.Preamble)
	}
}

// TestAnUnclaimedImageStandsWhereItIsPainted: an image no element claims is placed among the
// tree's blocks on its page as the untagged path places it (#23), before a table rather than
// inside it, not again where it overlaps the image its Figure claims, and in Unplaced on a page
// the tree built nothing for.
func TestAnUnclaimedImageStandsWhereItIsPainted(t *testing.T) {
	picture := func(mcid int, y0, y1 float64) doc.Image {
		return doc.Image{Box: geom.NewRect(72, y0, 300, y1), Width: 200, Height: 100, MCID: mcid}
	}
	roles := func(blocks []doc.Block) string {
		var s string
		for _, b := range blocks {
			s += string(b.Role)[:1]
		}
		return s
	}
	d := page([]doc.Span{at(1, "above", 72, 700, 120)}, []doc.Span{at(2, "below", 72, 400, 120)})
	// The second image is a 5pt dot, which layout.Keep calls decoration.
	d.Pages[0].Images = []doc.Image{picture(-1, 500, 600), {Box: geom.NewRect(72, 450, 77, 455), Width: 50, Height: 50, MCID: -1}}
	out, _ := Tagged(d, tree(el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 2)), DefaultOptions)
	if got := roles(out.Preamble); got != "pip" {
		t.Errorf("blocks %q, want paragraph, image, paragraph", got)
	}

	d = page([]doc.Span{at(1, "above", 72, 700, 120)}, []doc.Span{at(2, "one", 72, 600, 100)}, []doc.Span{at(3, "two", 72, 500, 100)})
	d.Pages[0].Images = []doc.Image{picture(-1, 540, 560)}
	tbl := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 2)), kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 3)))
	out, _ = Tagged(d, tree(el(tag.RoleP, 1, 1), tbl), DefaultOptions)
	// The image's top is between the two rows, so the first block below it is the second cell;
	// it goes before the table instead.
	if got := roles(out.Preamble); got != "pitt" {
		t.Errorf("blocks %q, want paragraph, image, then the table's two cells", got)
	}

	// A table that runs on to the next page: an image below its first page's rows goes after
	// the whole table, not between its pages' cells.
	d = page([]doc.Span{at(1, "above", 72, 700, 120)}, []doc.Span{at(2, "one", 72, 600, 100)})
	d.Pages[0].Images = []doc.Image{picture(-1, 300, 400)}
	two := at(3, "two", 72, 700, 100)
	two.Page = 2
	d.Pages = append(d.Pages, doc.Page{Number: 2, Box: geom.NewRect(0, 0, 612, 792), Blocks: []doc.Block{{Role: doc.RoleParagraph, Spans: []doc.Span{two}, Box: two.Box}}})
	run := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 2)), kids(el(tag.RoleTR, 2), el(tag.RoleTD, 2, 3)))
	out, _ = Tagged(d, tree(el(tag.RoleP, 1, 1), run), DefaultOptions)
	if got := roles(out.Preamble); got != "ptti" {
		t.Errorf("blocks %q, want the image after the table's last cell", got)
	}

	d = page([]doc.Span{at(1, "caption", 72, 300, 120)})
	d.Pages[0].Images = []doc.Image{picture(7, 500, 600), picture(-1, 495, 605)}
	fig := el(tag.RoleFigure, 1, 7)
	out, _ = Tagged(d, tree(fig, el(tag.RoleP, 1, 1)), DefaultOptions)
	images := 0
	for _, b := range out.Preamble {
		if b.Role == doc.RoleImage {
			images++
		}
	}
	if images != 1 {
		t.Errorf("%d image lines, want the Figure's alone", images)
	}

	d = page([]doc.Span{at(1, "text", 72, 700, 120)})
	d.Pages = append(d.Pages, doc.Page{Number: 2, Box: geom.NewRect(0, 0, 612, 792), Images: []doc.Image{picture(-1, 500, 600)}})
	out, _ = Tagged(d, tree(el(tag.RoleP, 1, 1)), DefaultOptions)
	if len(out.Unplaced) != 1 || out.Unplaced[0].Number != 2 || roles(out.Unplaced[0].Blocks) != "i" {
		t.Errorf("unplaced %+v, want page 2's image", out.Unplaced)
	}
}

// acrossPage is "continua" low on page 1's line and "tion" lower still at the line start on page
// 2: as a wrap on one page it would get a space, and across a page it must not.
func acrossPage() *doc.Document {
	d := page([]doc.Span{at(5, "continua", 47, 700, 100)})
	tion := at(5, "tion", 47, 600, 70)
	tion.Page = 2
	d.Pages = append(d.Pages, doc.Page{Number: 2, Box: geom.NewRect(0, 0, 612, 792), Blocks: []doc.Block{{Role: doc.RoleParagraph, Spans: []doc.Span{tion}, Box: tion.Box}}})
	return d
}

// TestAListingAcrossBlocksKeepsItsLines: a code listing whose lines the extractor read as separate
// blocks is broken at each line, not joined by a wrap's space that would hide the break.
func TestAListingAcrossBlocksKeepsItsLines(t *testing.T) {
	d := page([]doc.Span{at(5, "endobj", 47, 700, 90)}, []doc.Span{at(5, "11 0 obj", 47, 688, 100)})
	out, _ := Tagged(d, tree(el(tag.RoleCode, 1, 5)), DefaultOptions)
	if len(out.Preamble) != 1 || out.Preamble[0].Text() != "endobj\n11 0 obj" {
		t.Errorf("%+v, want one listing of two lines", out.Preamble)
	}
}

// TestUnclaimedImagesBetweenTablesAndOverlaps: an image between two tables stands between them,
// not before the first; and one covering most of a claimed image is that picture again.
func TestUnclaimedImagesBetweenTablesAndOverlaps(t *testing.T) {
	roles := func(blocks []doc.Block) string {
		var s string
		for _, b := range blocks {
			s += string(b.Role)[:1]
		}
		return s
	}
	d := page([]doc.Span{at(1, "above", 72, 700, 120)}, []doc.Span{at(2, "a", 72, 600, 100)}, []doc.Span{at(3, "b", 72, 450, 100)})
	d.Pages[0].Images = []doc.Image{{Box: geom.NewRect(72, 500, 300, 540), Width: 200, Height: 100, MCID: -1}}
	one := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 2)))
	two := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 3)))
	out, _ := Tagged(d, tree(el(tag.RoleP, 1, 1), one, two), DefaultOptions)
	if got := roles(out.Preamble); got != "ptit" {
		t.Errorf("blocks %q, want the image between the two tables", got)
	}

	// Two thirds of the claimed picture lie under the unclaimed one: still the same picture.
	d = page([]doc.Span{at(1, "caption", 72, 300, 120)})
	d.Pages[0].Images = []doc.Image{
		{Box: geom.NewRect(72, 500, 300, 600), Width: 200, Height: 100, MCID: 7},
		{Box: geom.NewRect(72, 466, 300, 566), Width: 200, Height: 100, MCID: -1},
	}
	out, _ = Tagged(d, tree(el(tag.RoleFigure, 1, 7), el(tag.RoleP, 1, 1)), DefaultOptions)
	images := 0
	for _, b := range out.Preamble {
		if b.Role == doc.RoleImage {
			images++
		}
	}
	if images != 1 {
		t.Errorf("%d image lines, want the Figure's alone", images)
	}
}

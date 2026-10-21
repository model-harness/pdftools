package ocr

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// pageWithTable is a 1000 by 1400 white page image holding a ruled table of 3 columns and 2 rows
// from x 200 to 800, y 400 to 600, and a short rule below it that is no table.
func pageWithTable() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1400))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	black := image.NewUniform(color.Black)
	for _, y := range []int{400, 500, 600} {
		draw.Draw(img, image.Rect(200, y, 802, y+2), black, image.Point{}, draw.Src)
	}
	for _, x := range []int{200, 400, 600, 800} {
		draw.Draw(img, image.Rect(x, 400, x+2, 602), black, image.Point{}, draw.Src)
	}
	draw.Draw(img, image.Rect(200, 900, 900, 902), black, image.Point{}, draw.Src)
	return img
}

func cellWord(text string, x, y, line int) Word {
	return Word{Text: text, Box: image.Rect(x, y, x+60, y+20), Conf: 95, Block: 1, Par: 1, Line: line}
}

// TestTablesFindTheRuledTable: the page's table is found with its grid, its words placed in its
// cells and named; the words outside it, and a lone rule, are not taken.
func TestTablesFindTheRuledTable(t *testing.T) {
	words := []Word{
		cellWord("above", 200, 300, 1),
		cellWord("Name", 220, 440, 2), cellWord("|", 398, 440, 2), cellWord("Qty", 420, 440, 2),
		cellWord("Apples", 220, 540, 3), cellWord("3", 420, 540, 3), cellWord("1.20", 620, 540, 3),
		cellWord("below", 200, 700, 4),
	}
	tables := Tables(pageWithTable(), words, 200)
	if len(tables) != 1 {
		t.Fatalf("%d tables, want 1", len(tables))
	}
	tb := tables[0]
	if len(tb.XS) != 4 || len(tb.YS) != 3 || tb.XS[0] < 199 || tb.XS[0] > 202 || tb.YS[0] < 399 || tb.YS[0] > 402 {
		t.Errorf("grid = %v %v, want the page's lines", tb.XS, tb.YS)
	}
	if tb.Cells[0][0] != "Name" || tb.Cells[0][1] != "Qty" || tb.Cells[1][2] != "1.20" {
		t.Errorf("cells = %q", tb.Cells)
	}
	if len(tb.Words) != 6 || tb.Words[0] != 1 || tb.Words[5] != 6 {
		t.Errorf("words taken = %v, want 1 to 6", tb.Words)
	}
	// Two rules of one extent are no table, nor three that do not frame a grid.
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1400))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	for _, y := range []int{400, 500} {
		draw.Draw(img, image.Rect(200, y, 802, y+2), image.NewUniform(color.Black), image.Point{}, draw.Src)
	}
	if got := Tables(img, words, 200); len(got) != 0 {
		t.Errorf("two rules made %d tables", len(got))
	}
	// A grid nobody wrote in is not a table either.
	if got := Tables(pageWithTable(), nil, 200); len(got) != 0 {
		t.Errorf("an empty grid made %d tables", len(got))
	}
	// Nor two rules of one extent with a shorter one between, though Grid reads three lines.
	short := pageWithTable()
	draw.Draw(short, image.Rect(200, 500, 802, 502), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(short, image.Rect(200, 500, 700, 502), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if got := Tables(short, words, 200); len(got) != 0 {
		t.Errorf("two rules of one extent made %d tables", len(got))
	}
}

// TestStrokeIsTheMedianRun: a word's stroke is its median horizontal run of ink, not its thinnest.
func TestStrokeIsTheMedianRun(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	black := image.NewUniform(color.Black)
	draw.Draw(img, image.Rect(10, 10, 11, 11), black, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(10, 11, 13, 13), black, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(30, 10, 33, 11), black, image.Point{}, draw.Src)
	if got := Stroke(img, image.Rect(0, 0, 100, 100)); got != 3 {
		t.Errorf("stroke of runs 1, 3, 3, 3 = %g, want 3", got)
	}
	if got := Stroke(img, image.Rect(50, 50, 100, 100)); got != 0 {
		t.Errorf("stroke of no ink = %g", got)
	}
}

// TestLineSizeReadsTheLetters: a word's height over the share of the em its letters span, the
// line's median, to the half point; a line of no letters has no size.
func TestLineSizeReadsTheLetters(t *testing.T) {
	h := func(text string, px int) Word { return Word{Text: text, Box: image.Rect(0, 0, 10, px)} }
	for _, c := range []struct {
		words []Word
		want  float64
	}{
		{[]Word{h("ace", 43)}, 100},                         // x-height only
		{[]Word{h("the", 69)}, 100},                         // an ascender
		{[]Word{h("Abc", 69)}, 100},                         // a capital
		{[]Word{h("pay", 62)}, 100},                         // a descender
		{[]Word{h("Typed", 88)}, 100},                       // both
		{[]Word{h("--", 50)}, 0},                            // no letters
		{[]Word{h("ace", 4), h("the", 7), h("Abc", 7)}, 10}, // the median of 9.3, 10.1, 10.1
	} {
		if got := lineSize(c.words, 1); got != c.want {
			t.Errorf("%q: %g, want %g", c.words[0].Text, got, c.want)
		}
	}
}

// TestPageMarksAHeavyLineBold: a line drawn half again as heavy as the page's words reads bold,
// and a third again does not; a line twice the size with twice the stroke weighs the same; and
// with too few words on the page to have a body, nothing is bold.
func TestPageMarksAHeavyLineBold(t *testing.T) {
	var ws []Word
	for i := 0; i < boldMinWords; i++ {
		ws = append(ws, Word{Text: "the", Box: image.Rect(i*40, 100, i*40+30, 130), Conf: 95, Block: 1, Par: 1, Line: 2, Stroke: 2})
	}
	title := func(height int, stroke float64) []Word {
		return []Word{{Text: "Title", Box: image.Rect(0, 80-height, 60, 80), Conf: 95, Block: 1, Par: 1, Line: 1, Stroke: stroke}}
	}
	for _, c := range []struct {
		name   string
		height int
		stroke float64
		bold   bool
	}{
		{"half again as heavy", 30, 3, true},
		{"a third again as heavy", 30, 2.6, false},
		{"twice the size and stroke", 60, 4, false},
	} {
		page := Page(append(title(c.height, c.stroke), ws...), 1, geom.NewRect(0, 0, 1000, 1000), image.Rect(0, 0, 1000, 1000), 30)
		if got := page.Blocks[0].Spans[0].Style.Bold; got != c.bold || page.Blocks[0].Spans[1].Style.Bold {
			t.Errorf("%s: bold = %v, %v; want %v, false", c.name, got, page.Blocks[0].Spans[1].Style.Bold, c.bold)
		}
	}
	few := Page(append(title(30, 3), ws[:3]...), 1, geom.NewRect(0, 0, 1000, 1000), image.Rect(0, 0, 1000, 1000), 30)
	if few.Blocks[0].Spans[0].Style.Bold {
		t.Error("a page of four words had a body to be bold against")
	}
}

// TestPageMakesNearSizesOneAndJoinsColumns: Page snaps a paragraph's lines of near sizes to one,
// and joins the paragraph a column break cut.
func TestPageMakesNearSizesOneAndJoinsColumns(t *testing.T) {
	w := func(text string, x, y, h, block, line int) Word {
		return Word{Text: text, Box: image.Rect(x, y, x+80, y+h), Conf: 95, Block: block, Par: 1, Line: line}
	}
	words := []Word{
		w("held", 100, 100, 30, 1, 1), w("the", 200, 100, 30, 1, 1), w("hill", 300, 100, 30, 1, 1),
		w("held", 100, 140, 31, 1, 2), w("the", 200, 140, 31, 1, 2), w("hill", 300, 140, 31, 1, 2),
		w("and", 500, 100, 30, 2, 1), w("then", 600, 100, 30, 2, 1),
		w("held", 500, 140, 30, 2, 2),
	}
	page := Page(words, 1, geom.NewRect(0, 0, 1000, 1000), image.Rect(0, 0, 1000, 1000), 30)
	if len(page.Blocks) != 1 {
		t.Fatalf("%d blocks, want the cut paragraph as one", len(page.Blocks))
	}
	for _, l := range page.Blocks[0].Lines {
		if l.Size != page.Blocks[0].Lines[0].Size {
			t.Errorf("sizes %v and %v, want one", l.Size, page.Blocks[0].Lines[0].Size)
		}
	}
}

// TestColumnsJoinACutParagraph: a block opening the next column, flush and at the same size, after
// a block at the foot of the page, continues it when it starts lower case; capitalised, and not
// from the foot, it does not.
func TestColumnsJoinACutParagraph(t *testing.T) {
	line := func(text string, x0, y0 float64) (doc.Line, doc.Span) {
		box := geom.NewRect(x0, y0, x0+170, y0+10)
		return doc.Line{Box: box, Size: 10, Space: 3}, doc.Span{Text: text, Box: box, MCID: -1}
	}
	block := func(texts []string, x0, top float64) doc.Block {
		var b doc.Block
		for i, s := range texts {
			l, sp := line(s, x0, top-12*float64(i))
			l.Span = len(b.Spans)
			b.Lines = append(b.Lines, l)
			b.Spans = append(b.Spans, sp)
			b.Box = b.Box.Union(l.Box)
		}
		b.Role = doc.RoleParagraph
		return b
	}
	for _, c := range []struct {
		name   string
		second []string
		top    float64
		blocks int
	}{
		{"lower case at the top", []string{"river narrows,", "and on"}, 200, 1},
		{"a capital", []string{"The third", "paragraph"}, 200, 2},
		// One line, ending at the foot of the first column, so that only its top is below.
		{"below the first", []string{"river narrows, and on"}, 40, 2},
		{"a capital after a full line", []string{"The third", "paragraph"}, 200, 1},
		{"lower case after a short line", []string{"river narrows,", "and on"}, 200, 1},
		{"an indented first line", []string{"river narrows,", "and on"}, 200, 2},
		{"a footer below the first", []string{"river narrows,", "and on"}, 200, 3},
	} {
		first := block([]string{"The second", "then by a mill"}, 36, 60)
		if c.name == "a capital" || c.name == "lower case after a short line" {
			// Its last line ends short, as a paragraph's last does; a full one carries on.
			first.Lines[1].Box.X1 -= 80
		}
		second := block(c.second, 220, c.top)
		if c.name == "an indented first line" {
			second.Lines[0].Box.X0 += 10
		}
		p := doc.Page{Blocks: []doc.Block{first, second}}
		if c.name == "a footer below the first" {
			// The first column does not end at the foot of the page's text.
			p.Blocks = append(p.Blocks, block([]string{"page 2"}, 220, 10))
		}
		joinColumns(&p)
		if len(p.Blocks) != c.blocks {
			t.Errorf("%s: %d blocks, want %d", c.name, len(p.Blocks), c.blocks)
		}
		if c.blocks == 1 && c.name == "lower case at the top" && (p.Blocks[0].Text() != "The secondthen by a mill river narrows,and on" || len(p.Blocks[0].Lines) != 4 || p.Blocks[0].Lines[2].Span != 2) {
			t.Errorf("%s: joined %q, lines %+v", c.name, p.Blocks[0].Text(), p.Blocks[0].Lines)
		}
	}
}

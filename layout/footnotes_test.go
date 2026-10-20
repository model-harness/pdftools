package layout

import (
	"slices"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// run is one span of a line for the footnote tests: its text, its size, and how far its box's
// foot sits above the line's.
type run struct {
	text  string
	size  float64
	raise float64
}

// noteLines builds a block from lines of runs, each line at a given foot y and the size of its
// first run that is not raised, with a span per run and each line's start recorded.
func noteLines(lines ...[]run) func(ys ...float64) doc.Block {
	return func(ys ...float64) doc.Block {
		b := doc.Block{Role: doc.RoleParagraph}
		for li, l := range lines {
			y := ys[li]
			size := l[0].size
			for _, r := range l {
				if r.raise == 0 {
					size = r.size
					break
				}
			}
			ln := doc.Line{Span: len(b.Spans), Page: 1, Size: size, Space: size / 4}
			x := 72.0
			for _, r := range l {
				box := geom.Rect{X0: x, Y0: y + r.raise, X1: x + float64(len(r.text))*r.size/2, Y1: y + r.raise + r.size}
				x = box.X1
				b.Spans = append(b.Spans, doc.Span{Text: r.text, Style: doc.Style{Size: r.size}, Box: box, Page: 1, MCID: -1})
				ln.Box = ln.Box.Union(box)
			}
			b.Lines = append(b.Lines, ln)
			b.Box = b.Box.Union(ln.Box)
		}
		return b
	}
}

// survey is reference/footnotes.pdf's first paragraph: a 10pt line with a 7pt mark raised 4.5pt.
func survey(mark string) doc.Block {
	return noteLines([]run{{"The survey counted forty species on the island", 10, 0}, {mark, 7, 4.5}, {" and found twelve.", 10, 0}})(232)
}

// notes is a foot block of two 8pt notes, each opened by a 6pt mark raised 3pt.
func notes(a, b string) doc.Block {
	return noteLines(
		[]run{{a, 6, 3}, {"Counted over three summers. ", 8, 0}},
		[]run{{b, 6, 3}, {"The eastern shore is open to grazing.", 8, 0}},
	)(50, 40)
}

// TestFootnotesPairAMarkWithItsNote is the fixture's shape: two marks in the text, a block of two
// notes at the foot. The block is cut into its notes, each a RoleFootnote labelled by its mark
// and without it, and each mark names its note.
func TestFootnotesPairAMarkWithItsNote(t *testing.T) {
	second := noteLines([]run{{"Most nests were on the cliffs.", 10, 0}, {"2", 7, 4.5}})(200)
	foot := notes("1", "2")
	for i := range foot.Spans {
		foot.Spans[i].MCID = 10 + i/2
	}
	foot.SetMCIDs()
	d := pages([]doc.Block{survey("1"), second, foot})
	if n := Footnotes(d); n != 2 {
		t.Fatalf("found %d, want 2", n)
	}
	bs := d.Pages[0].Blocks
	if len(bs) != 4 {
		t.Fatalf("blocks = %q, want the note block cut in two", texts(bs))
	}
	for i, want := range []struct{ marker, text string }{{"1", "Counted over three summers."}, {"2", "The eastern shore is open to grazing."}} {
		b := bs[2+i]
		if b.Role != doc.RoleFootnote || b.Marker != want.marker || b.Text() != want.text {
			t.Errorf("note %d = %s %q %q, want a footnote %q %q", i, b.Role, b.Marker, b.Text(), want.marker, want.text)
		}
	}
	if !slices.Equal(bs[2].MCIDs, []int{10}) || !slices.Equal(bs[3].MCIDs, []int{11}) {
		t.Errorf("notes' MCIDs = %v and %v, want each its own", bs[2].MCIDs, bs[3].MCIDs)
	}
	if bs[2].Box != bs[2].Lines[0].Box || bs[3].Box != bs[3].Lines[0].Box {
		t.Errorf("notes' boxes = %v and %v, want each its own line's", bs[2].Box, bs[3].Box)
	}
	if bs[0].Spans[1].Note != "1" || bs[0].Spans[1].Text != "1" || bs[1].Spans[1].Note != "2" {
		t.Errorf("marks = %+v and %+v, want them naming notes 1 and 2 and keeping their text", bs[0].Spans[1], bs[1].Spans[1])
	}
	if bs[0].Spans[0].Note != "" || bs[0].Spans[2].Note != "" {
		t.Error("a span that is not a mark names a note")
	}
}

// TestFootnotesNeedBothHalves: a raised digit with no note is an exponent or a unit as often as a
// mark, and a small block at the foot with no mark is a caption or an address.
func TestFootnotesNeedBothHalves(t *testing.T) {
	for _, c := range []struct {
		name   string
		blocks []doc.Block
	}{
		{"a note with no mark", []doc.Block{noteLines([]run{{"Plain text.", 10, 0}})(232), notes("1", "2")}},
		{"a mark with no note", []doc.Block{survey("1"), noteLines([]run{{"An address at the foot.", 8, 0}})(50)}},
		{"a mark and a note that differ", []doc.Block{survey("3"), notes("1", "2")}},
		{"a note with text below it", []doc.Block{survey("1"), notes("1", "2"), noteLines([]run{{"More text.", 10, 0}})(30)}},
		{"a raised word for a mark", []doc.Block{survey("st"), notes("st", "nd")}},
		{"a heading at the foot", []doc.Block{survey("1"), heading(notes("1", "2"))}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := pages(c.blocks)
			if n := Footnotes(d); n != 0 {
				t.Errorf("found %d, want 0", n)
			}
			for _, b := range d.Pages[0].Blocks {
				for _, sp := range b.Spans {
					if b.Role == doc.RoleFootnote || sp.Note != "" {
						t.Errorf("block %q was made part of a footnote", b.Text())
					}
				}
			}
		})
	}
}

// TestAFootnoteMarkIsRaisedAndSmall takes the mark's two bounds, at each and just past it: a
// quarter of its line's size above the line's foot, and 0.8 of its size. 7 against 10 and 4.5
// up are the fixture's; 8 is the size where 0.8 of it is 6.4 to the last bit.
func TestAFootnoteMarkIsRaisedAndSmall(t *testing.T) {
	for _, c := range []struct {
		name        string
		size, raise float64
		mark        bool
	}{
		{"the fixture's", 7, 4.5, true},
		{"raised a quarter of the size", 7, 2.5, true},
		{"raised less", 7, 2.49, false},
		{"set at 0.8 of the size", 8, 4.5, true},
		{"set larger", 8.01, 4.5, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			text := noteLines([]run{{"The survey counted forty species on the island", 10, 0}, {"1", c.size, c.raise}, {" and found twelve.", 10, 0}})(232)
			d := pages([]doc.Block{text, notes("1", "2")})
			if got := Footnotes(d) == 1; got != c.mark {
				t.Errorf("mark = %v, want %v", got, c.mark)
			}
		})
	}
}

// TestFootnoteLabels: a number is its own label, once per document; a later page's repeat of it
// gets the page; a mark that is not a number gets a count, since "*" is emphasis to GFM; and
// anything else is not a mark at all.
func TestFootnoteLabels(t *testing.T) {
	d := pages(
		[]doc.Block{survey("1"), notes("1", "9")},
		[]doc.Block{survey("1"), notes("1", "9")},
		[]doc.Block{survey("*"), notes("*", "9")},
		[]doc.Block{survey("†"), notes("†", "9")},
	)
	if n := Footnotes(d); n != 4 {
		t.Fatalf("found %d, want 4", n)
	}
	var got []string
	for _, p := range d.Pages {
		got = append(got, p.Blocks[0].Spans[1].Note)
	}
	if want := []string{"1", "1-2", "fn1", "fn2"}; !slices.Equal(got, want) {
		t.Errorf("labels = %q, want %q", got, want)
	}
	for _, mark := range []string{"1234", "12a", "x", "**††"} {
		if label.MatchString(mark) {
			t.Errorf("%q is a label", mark)
		}
	}
	for _, mark := range []string{"1", "123", "*", "***", "†", "‡", "§§", "¶", "‖"} {
		if !label.MatchString(mark) {
			t.Errorf("%q is not a label", mark)
		}
	}
}

// TestAFootnoteMarkIsTakenOnce: the same mark twice in the text and one note takes the first.
func TestAFootnoteMarkIsTakenOnce(t *testing.T) {
	d := pages([]doc.Block{survey("1"), survey("1"), notes("1", "2")})
	if n := Footnotes(d); n != 1 {
		t.Fatalf("found %d, want 1", n)
	}
	if bs := d.Pages[0].Blocks; bs[0].Spans[1].Note != "1" || bs[1].Spans[1].Note != "" {
		t.Errorf("marks = %q and %q, want only the first taken", bs[0].Spans[1].Note, bs[1].Spans[1].Note)
	}
}

// TestFootnotesBeAboveTheFolio: furniture below the notes does not move them off the foot.
func TestFootnotesBeAboveTheFolio(t *testing.T) {
	folio := noteLines([]run{{"7", 10, 0}})(10)
	folio.Role = doc.RoleArtifact
	d := pages([]doc.Block{survey("1"), notes("1", "2"), folio})
	if n := Footnotes(d); n != 1 {
		t.Errorf("found %d, want 1", n)
	}
}

// TestAFootnoteMarkOpensItsLine: a note's mark is a span of its own at the start of its line,
// where a line may also start at the end of the span before it; a mark in the middle of a span
// is not one.
func TestAFootnoteMarkOpensItsLine(t *testing.T) {
	end := notes("1", "2")
	end.Lines[1] = doc.Line{Span: 1, Offset: len(end.Spans[1].Text), Page: 1, Size: 8, Space: 2, Box: end.Lines[1].Box}
	mid := notes("1", "2")
	mid.Spans[1].Text += "2"
	mid.Lines[1] = doc.Line{Span: 1, Offset: len(mid.Spans[1].Text) - 1, Page: 1, Size: 8, Space: 2, Box: mid.Lines[1].Box}
	for _, c := range []struct {
		name string
		foot doc.Block
		want int
	}{
		{"a line starting where a span ends", end, 2},
		{"a line starting inside a span", mid, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			second := noteLines([]run{{"Most nests were on the cliffs.", 10, 0}, {"2", 7, 4.5}})(200)
			d := pages([]doc.Block{survey("1"), second, c.foot})
			if n := Footnotes(d); n != c.want {
				t.Errorf("found %d, want %d", n, c.want)
			}
		})
	}
}

// TestFootnotesNeedLines: a block with no lines, a table cell's, is not a note.
func TestFootnotesNeedLines(t *testing.T) {
	foot := notes("1", "2")
	foot.Lines = nil
	d := pages([]doc.Block{survey("1"), foot})
	if n := Footnotes(d); n != 0 {
		t.Errorf("found %d, want 0", n)
	}
	if Footnotes(nil) != 0 {
		t.Error("a nil document had footnotes")
	}
}

// TestPageBreaksPassOverFootnotes: a page's notes lie below the paragraph it ends with, and are
// not text the paragraph's continuation is asked about.
func TestPageBreaksPassOverFootnotes(t *testing.T) {
	note := para(1, doc.Style{Font: "Body", Size: 8}, 5, tline{text: "A note.", x0: 72, x1: 120, y: 40})
	note.Role = doc.RoleFootnote
	cont := para(2, bodyFace, 2, tline{text: "pines to the water.", x0: 72, x1: 200, y: 900})
	d := pages([]doc.Block{third(), note}, []doc.Block{note, cont})
	if n := PageBreaks(d); n != 1 {
		t.Errorf("joined %d, want 1", n)
	}
}

func heading(b doc.Block) doc.Block {
	b.Role = doc.RoleHeading
	return b
}

// TestFootnotesCutEveryNote: a foot block of three notes is three, in order.
func TestFootnotesCutEveryNote(t *testing.T) {
	foot := noteLines(
		[]run{{"1", 6, 3}, {"One. ", 8, 0}},
		[]run{{"2", 6, 3}, {"Two. ", 8, 0}},
		[]run{{"3", 6, 3}, {"Three.", 8, 0}},
	)(60, 50, 40)
	text := noteLines([]run{{"A", 10, 0}, {"1", 7, 4.5}, {" b", 10, 0}, {"2", 7, 4.5}, {" c", 10, 0}, {"3", 7, 4.5}})(232)
	d := pages([]doc.Block{text, foot})
	if n := Footnotes(d); n != 3 {
		t.Fatalf("found %d, want 3", n)
	}
	if got := texts(d.Pages[0].Blocks[1:]); !slices.Equal(got, []string{"One.", "Two.", "Three."}) {
		t.Errorf("notes = %q", got)
	}
}

// TestAFootnoteMarkOpeningALine is judged against the line it opens, not the one above.
func TestAFootnoteMarkOpeningALine(t *testing.T) {
	text := noteLines(
		[]run{{"The survey counted forty species on the island ", 10, 0}},
		[]run{{"1", 7, 4.5}, {" and found twelve.", 10, 0}},
		[]run{{"Then a third line", 10, 0}, {"2", 7, 4.5}},
	)(250, 238, 226)
	d := pages([]doc.Block{text, notes("1", "2")})
	if n := Footnotes(d); n != 2 {
		t.Errorf("found %d, want 2", n)
	}
}

// TestFootnoteMarksInFurnitureAreNotTaken: a raised digit in a running header is not the mark a
// note answers.
func TestFootnoteMarksInFurnitureAreNotTaken(t *testing.T) {
	header := noteLines([]run{{"Chapter", 10, 0}, {"1", 7, 4.5}})(270)
	header.Role = doc.RoleArtifact
	d := pages([]doc.Block{header, survey("1"), notes("1", "2")})
	if n := Footnotes(d); n != 1 || d.Pages[0].Blocks[1].Spans[1].Note != "1" || d.Pages[0].Blocks[0].Spans[1].Note != "" {
		t.Errorf("found %d; the header's mark = %q, the text's = %q", n, d.Pages[0].Blocks[0].Spans[1].Note, d.Pages[0].Blocks[1].Spans[1].Note)
	}
}

// TestTwoFootnotesWithOneMark: two notes saying "1" take the two marks saying it, in order.
func TestTwoFootnotesWithOneMark(t *testing.T) {
	d := pages([]doc.Block{survey("1"), survey("1"), notes("1", "1")})
	if n := Footnotes(d); n != 2 {
		t.Fatalf("found %d, want 2", n)
	}
	if bs := d.Pages[0].Blocks; bs[0].Spans[1].Note != "1" || bs[1].Spans[1].Note != "1-1" {
		t.Errorf("marks = %q and %q, want 1 and 1-1", bs[0].Spans[1].Note, bs[1].Spans[1].Note)
	}
}

// TestAFootnoteMarkIsAWholeSpan: a line that starts inside a span starts no note, even where
// the span from there on would read as a mark.
func TestAFootnoteMarkIsAWholeSpan(t *testing.T) {
	foot := noteLines(
		[]run{{"1", 6, 3}, {"One. ", 8, 0}},
		[]run{{"22", 6, 3}, {"Two.", 8, 0}},
	)(50, 40)
	foot.Lines[1].Offset = 1
	text := noteLines([]run{{"A", 10, 0}, {"1", 7, 4.5}, {" b", 10, 0}, {"2", 7, 4.5}})(232)
	d := pages([]doc.Block{text, foot})
	if n := Footnotes(d); n != 1 {
		t.Errorf("found %d, want 1: the second line opens inside \"22\"", n)
	}
}

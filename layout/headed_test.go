package layout

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// at is block with its left edge at x.
func at(b doc.Block, x float64) doc.Block {
	b.Box = geom.Rect{X0: x, X1: x + 200}
	return b
}

// runes is a body paragraph of n characters at 9.96pt.
func runes(n int) doc.Block {
	return block(strings.Repeat("a", n), 9.96, false)
}

func roleOf(t *testing.T, d *doc.Document, page, i int) (doc.Role, int) {
	t.Helper()
	b := d.Pages[page].Blocks[i]
	return b.Role, b.Level
}

// TestAnUnnumberedHeadingHeadsTheBodyAfterIt is ADR 0031's rule, condition by condition: a
// 12pt bold "Overview" above body text, and what is varied is the block after it.
func TestAnUnnumberedHeadingHeadsTheBodyAfterIt(t *testing.T) {
	heading := block("Overview", 12, true)
	for _, c := range []struct {
		name   string
		after  []doc.Block
		header bool
		why    string
	}{
		{"a long paragraph at its edge", []doc.Block{at(runes(40), 0)}, true, "40 characters of lighter type start where it does"},
		{"a short one", []doc.Block{at(runes(39), 0)}, false, "39 characters is a table row or a caption's line, not the body it heads"},
		{"within the alignment", []doc.Block{at(runes(80), 3)}, true, "3pt is where a heading's own edge can sit"},
		{"past the alignment", []doc.Block{at(runes(80), 3.5)}, false, "a block 3.5pt in starts somewhere else"},
		{"nothing after it", nil, false, "a candidate that heads nothing is a line, not a heading"},
		{"through a numbered heading", []doc.Block{at(block("1.1 First part", 11, true), 0), at(runes(80), 0)}, true,
			"\"7.3.4 String objects\" is followed at once by \"7.3.4.1 General\""},
		{"not through an unnumbered one", []doc.Block{at(block("Also a candidate", 11, true), 0), at(runes(80), 0)}, false,
			"a run of unnumbered candidates is a table header or a title page"},
		{"a list after it", []doc.Block{at(block("• A second crane", 9.96, false), 0)}, true,
			"\"Changes\" above its bullets: a list is a heading's body as a paragraph is"},
		{"an indented list", []doc.Block{at(block("• A second crane", 9.96, false), 20)}, true,
			"a list is indented under its heading as often as not, up to four of its sizes"},
		{"a list four sizes in", []doc.Block{at(block("• A second crane", 9.96, false), 48)}, true,
			"48pt is four of a 12pt heading's sizes"},
		{"a list further in", []doc.Block{at(block("• A second crane", 9.96, false), 48.1)}, false,
			"past four sizes the list starts somewhere else"},
		{"a list left of it", []doc.Block{at(block("• A second crane", 9.96, false), -3.5)}, false,
			"a list hangs no further left of its heading than the alignment allows"},
		{"an indented table", []doc.Block{at(doc.Block{Role: doc.RoleTableCell, Spans: []doc.Span{{Text: "Vessel", MCID: -1, Style: doc.Style{Size: 9.96}}}}, 20), at(body(9.96, false), 20)}, false,
			"a caption sits flush above a table set in, so a table must start at the heading's edge"},
		{"a table after it", []doc.Block{at(doc.Block{Role: doc.RoleTableCell, Spans: []doc.Span{{Text: "Vessel", MCID: -1, Style: doc.Style{Size: 9.96}}}}, 0), at(body(9.96, false), 0)}, true,
			"\"Traffic\" above its table"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := pageDoc(append([]doc.Block{at(heading, 0)}, c.after...)...)
			Headings(d, DefaultOptions)
			if r, _ := roleOf(t, d, 0, 0); (r == doc.RoleHeading) != c.header {
				t.Errorf("Overview = %v, want heading %v: %s", r, c.header, c.why)
			}
		})
	}
}

// TestTheBodyAHeadingHeadsIsLighter is the type of the block after the candidate. It must be
// smaller by more than headedSize, or the same size and the candidate bold and it not. Each
// follower is 100 characters, past MaxHeading, so it is no candidate itself: a block larger
// than the body that is short enough to be one is a heading's peer, not its body.
func TestTheBodyAHeadingHeadsIsLighter(t *testing.T) {
	for _, c := range []struct {
		name         string
		head, follow doc.Block
		header       bool
	}{
		{"smaller", block("Overview", 12, false), block(strings.Repeat("a", 100), 11.7, false), true},
		{"no smaller than headedSize", block("Overview", 12, false), block(strings.Repeat("a", 100), 11.8, false), false},
		{"as large and lighter", block("Overview", 10, true), block(strings.Repeat("a", 100), 10, false), true},
		{"as large and as bold", block("Overview", 10, true), block(strings.Repeat("a", 100), 10, true), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The body at 9.96pt is what makes the 10pt and 12pt candidates distinct.
			d := pageDoc(at(c.head, 0), at(c.follow, 0), at(body(9.96, false), 0), at(body(9.96, false), 0))
			Headings(d, DefaultOptions)
			if r, _ := roleOf(t, d, 0, 0); (r == doc.RoleHeading) != c.header {
				t.Errorf("Overview = %v, want heading %v", r, c.header)
			}
		})
	}
}

// TestAHeadingHeadsTheBodyOnItsOwnPage: the block after the candidate in reading order is on
// the next page, so its left edge is in another page's coordinates and says nothing.
func TestAHeadingHeadsTheBodyOnItsOwnPage(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: []doc.Block{at(body(9.96, false), 0), at(block("Overview", 12, true), 0)}},
		{Number: 2, Blocks: []doc.Block{at(runes(80), 0)}},
	}}
	Headings(d, DefaultOptions)
	if r, _ := roleOf(t, d, 0, 1); r == doc.RoleHeading {
		t.Errorf("Overview = %v, want a paragraph: what follows it is on another page", r)
	}
}

// TestAHeadingThroughNumberedOnesStopsAtHeadedSkip: four numbered headings can stand between
// an unnumbered one and its body, and a fifth cannot.
func TestAHeadingThroughNumberedOnesStopsAtHeadedSkip(t *testing.T) {
	for _, n := range []int{4, 5} {
		blocks := []doc.Block{at(block("Overview", 12, true), 0)}
		for i := 0; i < n; i++ {
			blocks = append(blocks, at(block("1.1 First part", 11, true), 0))
		}
		blocks = append(blocks, at(runes(80), 0))
		d := pageDoc(blocks...)
		Headings(d, DefaultOptions)
		r, _ := roleOf(t, d, 0, 0)
		if want := n <= 4; (r == doc.RoleHeading) != want {
			t.Errorf("through %d numbered headings: Overview = %v, want heading %v", n, r, want)
		}
	}
}

// TestAnUnnumberedHeadingIsWords: a candidate of more digits than letters, or one a list
// marker or ordered label opens, is never a heading by what follows it.
func TestAnUnnumberedHeadingIsWords(t *testing.T) {
	for _, text := range []string{"Q1 2024 1234", "AB12", "• A bullet point", "b) Apply the transform"} {
		d := pageDoc(at(block(text, 12, true), 0), at(runes(80), 0))
		Headings(d, DefaultOptions)
		if r, _ := roleOf(t, d, 0, 0); r == doc.RoleHeading {
			t.Errorf("%q = %v, want a paragraph", text, r)
		}
	}
	d := pageDoc(at(block("Q1 2024 Results", 12, true), 0), at(runes(80), 0))
	Headings(d, DefaultOptions)
	if r, _ := roleOf(t, d, 0, 0); r != doc.RoleHeading {
		t.Errorf("Q1 2024 Results = %v, want a heading: nine letters to five digits", r)
	}
}

// TestAnUnnumberedHeadingTakesItsLevel: from the numbered headings in its style when there
// are any, and from its style's rank among the rest when there are not.
func TestAnUnnumberedHeadingTakesItsLevel(t *testing.T) {
	t.Run("a numbered heading's style", func(t *testing.T) {
		d := pageDoc(
			at(block("Foreword", 13, true), 0), at(runes(80), 0),
			at(block("1 Scope", 13, true), 0), at(runes(80), 0),
			at(block("1.1 General", 11, true), 0), at(runes(80), 0),
			at(block("1.2 Terms", 11, true), 0), at(runes(80), 0),
			at(block("Notes", 11, true), 0), at(runes(80), 0),
		)
		Headings(d, DefaultOptions)
		if _, l := roleOf(t, d, 0, 0); l != 1 {
			t.Errorf("Foreword level %d, want 1: it is set like 1 Scope", l)
		}
		if _, l := roleOf(t, d, 0, 8); l != 2 {
			t.Errorf("Notes level %d, want 2: it is set like 1.1 General", l)
		}
	})
	t.Run("rank by size, then weight", func(t *testing.T) {
		d := pageDoc(
			at(block("Getting Started", 14.35, true), 0), at(runes(80), 0),
			at(block("Configuration", 11.96, true), 0), at(runes(80), 0),
			at(block("Environment", 9.96, true), 0), at(runes(80), 0),
			at(block("Troubleshooting", 14.35, true), 0), at(runes(80), 0),
		)
		Headings(d, DefaultOptions)
		for i, want := range map[int]int{0: 1, 2: 2, 4: 3, 6: 1} {
			if r, l := roleOf(t, d, 0, i); r != doc.RoleHeading || l != want {
				t.Errorf("block %d = %v level %d, want heading level %d", i, r, l, want)
			}
		}
	})
}

// TestTheBodyAHeadingHeadsIsWeighedByItsCharacters is how the block after a candidate is
// read: its size is the one most of its characters are set in, the smaller on a tie, and it
// is bold only when more than half of them are.
func TestTheBodyAHeadingHeadsIsWeighedByItsCharacters(t *testing.T) {
	half := func(a, b doc.Style) doc.Block {
		return doc.Block{Role: doc.RoleParagraph, Spans: []doc.Span{
			{Text: strings.Repeat("a", 50), MCID: -1, Style: a},
			{Text: strings.Repeat("b", 50), MCID: -1, Style: b},
		}}
	}
	for _, c := range []struct {
		name         string
		head, follow doc.Block
	}{
		{"half bold is not bold", block("Overview", 10, true), half(doc.Style{Size: 10, Bold: true}, doc.Style{Size: 10})},
		{"a tie in size is the smaller", block("Overview", 12, false), half(doc.Style{Size: 11.5}, doc.Style{Size: 12})},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := pageDoc(at(c.head, 0), at(c.follow, 0), at(body(9.96, false), 0), at(body(9.96, false), 0))
			Headings(d, DefaultOptions)
			if r, _ := roleOf(t, d, 0, 0); r != doc.RoleHeading {
				t.Errorf("Overview = %v, want a heading: the block after it is lighter", r)
			}
		})
	}
}

// TestAnArtifactIsNotWhatAHeadingHeads: a running header drawn between a heading and its
// body is not the block a reader reaches next.
func TestAnArtifactIsNotWhatAHeadingHeads(t *testing.T) {
	art := block("Running header", 9.96, false)
	art.Role = doc.RoleArtifact
	d := pageDoc(at(block("Overview", 12, true), 0), at(art, 0), at(runes(80), 0))
	Headings(d, DefaultOptions)
	if r, _ := roleOf(t, d, 0, 0); r != doc.RoleHeading {
		t.Errorf("Overview = %v, want a heading: the artifact is passed over", r)
	}
}

// TestAnUnnumberedHeadingsLevelComesFromItsStyle is the two ways ADR 0031 levels one, at the
// points where they disagree.
func TestAnUnnumberedHeadingsLevelComesFromItsStyle(t *testing.T) {
	t.Run("the numbered style's level, not the rank", func(t *testing.T) {
		// "Notes" is the only unnumbered style, so its rank would be 1. It is set like
		// "1.1.1 Detail", which is level 3.
		d := pageDoc(
			at(block("1 Scope", 13, true), 0), at(runes(80), 0),
			at(block("1.1.1 Detail", 11, true), 0), at(runes(80), 0),
			at(block("Notes", 11, true), 0), at(runes(80), 0),
		)
		Headings(d, DefaultOptions)
		if _, l := roleOf(t, d, 0, 4); l != 3 {
			t.Errorf("Notes level %d, want 3, the level of 1.1.1 Detail set the same way", l)
		}
	})
	t.Run("a tie between levels is the lower", func(t *testing.T) {
		d := pageDoc(
			at(block("1 Scope", 13, true), 0), at(runes(80), 0),
			at(block("1.1 Part", 13, true), 0), at(runes(80), 0),
			at(block("Foreword", 13, true), 0), at(runes(80), 0),
		)
		Headings(d, DefaultOptions)
		if _, l := roleOf(t, d, 0, 4); l != 1 {
			t.Errorf("Foreword level %d, want 1: its style is level 1 once and level 2 once", l)
		}
	})
	t.Run("bold ranks above regular at one size", func(t *testing.T) {
		d := pageDoc(
			at(block("Section", 12, false), 0), at(runes(80), 0),
			at(block("Chapter", 12, true), 0), at(runes(80), 0),
		)
		Headings(d, DefaultOptions)
		if _, l := roleOf(t, d, 0, 2); l != 1 {
			t.Errorf("Chapter level %d, want 1", l)
		}
		if _, l := roleOf(t, d, 0, 0); l != 2 {
			t.Errorf("Section level %d, want 2", l)
		}
	})
}

// TestACentredTitleHeadsItsBody: a title centred on its page, set half as large again as the body
// or more, is a heading though the paragraph after it starts at the margin, as a book's chapter
// title and a report's title are, or a figure follows it, as a textbook's section does (ADR 0058).
// A centred line at the body's size is a caption, which ADR 0031 keeps out, and so is one off the
// page's centre or not half as large again.
func TestACentredTitleHeadsItsBody(t *testing.T) {
	title := func(size, x0, x1 float64, bold bool) doc.Block {
		b := block("Author's Note to the 2021 Edition", size, bold)
		b.Box = geom.Rect{X0: x0, X1: x1}
		return b
	}
	para := block(strings.Repeat("This book is a reprinted version of a book, as the title says. ", 6), 11, false)
	para.Box = geom.Rect{X0: 57, X1: 377}
	for _, c := range []struct {
		name string
		head doc.Block
		want doc.Role
	}{
		{"a centred title", title(24, 112, 316, true), doc.RoleHeading},
		{"half as large again", title(16.5, 112, 316, false), doc.RoleHeading},
		{"a centred caption at the body's size", title(11, 112, 316, true), doc.RoleParagraph},
		{"a large line off the centre", title(24, 152, 356, true), doc.RoleParagraph},
		{"not half as large again", title(16.25, 112, 316, false), doc.RoleParagraph},
		{"over a figure", title(24, 112, 316, true), doc.RoleHeading},
	} {
		next := para
		if c.name == "over a figure" {
			next = doc.Block{Role: doc.RoleImage, Box: geom.Rect{X0: 57, Y0: 300, X1: 377, Y1: 500}}
		}
		d := &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 432, 648), Blocks: []doc.Block{c.head, next, para, para}}}}
		Headings(d, DefaultOptions)
		if got := d.Pages[0].Blocks[0].Role; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

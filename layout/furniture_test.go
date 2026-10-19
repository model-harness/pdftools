package layout

import (
	"strconv"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// spanAt is a block of text whose box runs from y0 to y1 on a page 1000pt tall, where the
// top band starts at 880 and the bottom band ends at 120.
func spanAt(text string, y0, y1 float64) doc.Block {
	b := block(text, 10, false)
	b.Box = geom.Rect{X0: 72, Y0: y0, X1: 400, Y1: y1}
	return b
}

func pages(blocks ...[]doc.Block) *doc.Document {
	d := &doc.Document{}
	for i, bs := range blocks {
		d.Pages = append(d.Pages, doc.Page{Number: i + 1, Box: geom.Rect{X1: 600, Y1: 1000}, Blocks: bs})
	}
	return d
}

func artifact(d *doc.Document, page, i int) bool {
	return d.Pages[page].Blocks[i].Role == doc.RoleArtifact
}

// TestFurnitureRecursInAMarginBand is ADR 0033's rule: a block wholly in the top or bottom
// eighth of its page whose text, numbers aside, is in the same band on another page.
func TestFurnitureRecursInAMarginBand(t *testing.T) {
	for _, c := range []struct {
		name      string
		a, b      doc.Block
		furniture bool
	}{
		{"a running header", spanAt("Field Notes", 900, 920), spanAt("Field Notes", 900, 920), true},
		{"a folio", spanAt("1", 40, 50), spanAt("2", 40, 50), true},
		{"a roman folio", spanAt("ii", 40, 50), spanAt("iii", 40, 50), true},
		{"a footer with its folio", spanAt("© 2020 iv", 40, 50), spanAt("© 2020 v", 40, 50), true},
		{"at the band's edge", spanAt("Field Notes", 880, 900), spanAt("Field Notes", 880, 900), true},
		{"past the band's edge", spanAt("Field Notes", 879, 900), spanAt("Field Notes", 879, 900), false},
		{"at the foot band's edge", spanAt("Field Notes", 100, 120), spanAt("Field Notes", 100, 120), true},
		{"past the foot band's edge", spanAt("Field Notes", 100, 121), spanAt("Field Notes", 100, 121), false},
		{"different text", spanAt("Field Notes", 900, 920), spanAt("Other Notes", 900, 920), false},
		{"the same text in the other band", spanAt("Field Notes", 900, 920), spanAt("Field Notes", 40, 50), false},
		{"numbers inside words", spanAt("rfc5652", 40, 50), spanAt("rfc6211", 40, 50), false},
		{"in either case", spanAt("Field Notes", 900, 920), spanAt("FIELD NOTES", 900, 920), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Text beyond the block at both edges, which no other page repeats, so that it is
			// never its page's outermost and only the band can mark it.
			d := pages([]doc.Block{c.a, spanAt("Body text on the first page.", 500, 520),
				spanAt("first edge", 990, 995), spanAt("first foot", 2, 4)},
				[]doc.Block{c.b, spanAt("Body text on the second page.", 500, 520),
					spanAt("second edge", 990, 995), spanAt("second foot", 2, 4)})
			Furniture(d)
			if got := artifact(d, 0, 0) && artifact(d, 1, 0); got != c.furniture {
				t.Errorf("furniture = %v, want %v", got, c.furniture)
			}
			if artifact(d, 0, 1) || artifact(d, 1, 1) {
				t.Errorf("a body block was marked")
			}
		})
	}
}

// TestFurnitureReachesTheOutermostBlock: past the band, a page's outermost block on a side is
// still in that side's margin up to 15% of the page, which on a page 1000pt tall is above 850
// and below 150, if it carries a folio. A block with text beyond it is not, and nor is one
// whose text is the same on every page, as a table's repeated header row is.
func TestFurnitureReachesTheOutermostBlock(t *testing.T) {
	for _, c := range []struct {
		name      string
		a         doc.Block // given each page's number, unless same
		beyond    doc.Block // on each page, beyond a; empty for none
		furniture bool
		same      bool // the same text on each page, or with upper, the second page's in capitals
		upper     bool
	}{
		{"a header below the band", spanAt("Field Notes", 860, 870), doc.Block{}, true, false, false},
		{"at the reach's edge", spanAt("Field Notes", 850, 870), doc.Block{}, true, false, false},
		{"past the reach's edge", spanAt("Field Notes", 849, 870), doc.Block{}, false, false, false},
		{"a folio above the foot band", spanAt("7", 130, 140), doc.Block{}, true, false, false},
		{"at the foot reach's edge", spanAt("7", 130, 150), doc.Block{}, true, false, false},
		{"past the foot reach's edge", spanAt("7", 130, 151), doc.Block{}, false, false, false},
		{"text above it", spanAt("Field Notes", 860, 870), spanAt("edge", 871, 872), false, false, false},
		{"text below it", spanAt("7", 130, 140), spanAt("edge", 128, 129), false, false, false},
		{"text level with its top", spanAt("Field Notes", 860, 870), spanAt("edge", 500, 870), true, false, false},
		{"text level with its foot", spanAt("7", 130, 140), spanAt("edge", 130, 500), true, false, false},
		{"the same text on each page", spanAt("Key Type Value", 860, 870), doc.Block{}, false, true, false},
		{"the same text in the band", spanAt("Key Type Value", 900, 910), doc.Block{}, true, true, false},
		{"the same text at the foot", spanAt("Key Type Value", 130, 140), doc.Block{}, false, true, false},
		{"the same text in capitals", spanAt("Key Type Value", 860, 870), doc.Block{}, false, true, true},
		{"a blank block above it", spanAt("Field Notes", 860, 870), spanAt(" ", 871, 872), true, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var p1, p2 []doc.Block
			for i, body := range []string{"Body text on the first page.", "Body text on the second page."} {
				a := c.a
				switch {
				case !c.same:
					a.Spans = []doc.Span{{Text: a.Text() + " " + strconv.Itoa(i+1), MCID: -1}}
				case c.upper && i == 1:
					a.Spans = []doc.Span{{Text: strings.ToUpper(a.Text()), MCID: -1}}
				}
				bs := []doc.Block{a, spanAt(body, 400, 420)}
				if e := c.beyond; !e.Box.IsZero() {
					if !e.IsEmpty() {
						e.Spans = []doc.Span{{Text: body[:len(body)-1] + " edge", MCID: -1}}
					}
					bs = append(bs, e)
				}
				if i == 0 {
					p1 = bs
				} else {
					p2 = bs
				}
			}
			d := pages(p1, p2)
			Furniture(d)
			if got := artifact(d, 0, 0) && artifact(d, 1, 0); got != c.furniture {
				t.Errorf("furniture = %v, want %v", got, c.furniture)
			}
		})
	}
}

// TestFurnitureNeedsTwoPages: one page is no evidence, whether the document has one page or
// the margin text is on only one of several.
func TestFurnitureNeedsTwoPages(t *testing.T) {
	d := pages([]doc.Block{spanAt("Field Notes", 900, 920)})
	if n := Furniture(d); n != 0 || artifact(d, 0, 0) {
		t.Errorf("a one-page document had %d blocks marked", n)
	}
	d = pages([]doc.Block{spanAt("A footnote", 40, 50)}, []doc.Block{spanAt("Another footnote", 40, 50)})
	if n := Furniture(d); n != 0 {
		t.Errorf("%d blocks marked, want 0: neither margin text recurs", n)
	}
}

// TestANumeralIsANumber is the signature's one normalization: a word that is a number, in
// digits or roman numerals, stands for any number.
func TestANumeralIsANumber(t *testing.T) {
	for tok, want := range map[string]bool{
		"12": true, "10": true, "iv": true, "xiii": true, "mcmxcix": true,
		"": false, "rfc5652": false, "mild": false, "did": false, "ivy": false, "4)": false,
	} {
		if got := numeral(tok); got != want {
			t.Errorf("numeral(%q) = %v, want %v", tok, got, want)
		}
	}
}

// TestFurnitureLeavesArtifactsAlone: md -artifacts keeps a tagged producer's furniture as
// artifacts, and those are not found again or counted as found.
func TestFurnitureLeavesArtifactsAlone(t *testing.T) {
	h := spanAt("Field Notes", 900, 920)
	h.Role = doc.RoleArtifact
	d := pages([]doc.Block{h}, []doc.Block{h})
	if n := Furniture(d); n != 0 {
		t.Errorf("marked %d, want 0: both headers were artifacts already", n)
	}
}

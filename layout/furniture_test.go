package layout

import (
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
			d := pages([]doc.Block{c.a, spanAt("Body text on the first page.", 500, 520)},
				[]doc.Block{c.b, spanAt("Body text on the second page.", 500, 520)})
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

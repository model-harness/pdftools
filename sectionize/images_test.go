package sectionize

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/tag"
)

// withImages gives page 1 of d the images, a 200 by 100 point picture unless said otherwise.
func withImages(d *doc.Document, ims ...doc.Image) *doc.Document {
	d.Pages[0].Images = append(d.Pages[0].Images, ims...)
	return d
}

func figureImage(mcid int) doc.Image {
	return doc.Image{Box: geom.NewRect(100, 400, 300, 500), Width: 64, Height: 48, MCID: mcid}
}

func roles(blocks []doc.Block) string {
	s := ""
	for _, b := range blocks {
		s += string(b.Role) + ":" + b.Alt + "|"
	}
	return s
}

// TestATaggedFigureNamesItsImage: a Figure whose marked content painted an image is an image
// block, its /Alt the image's description, written once. Without /Alt it is "[image]".
func TestATaggedFigureNamesItsImage(t *testing.T) {
	for _, c := range []struct {
		name, alt, want string
	}{
		{"with /Alt", "A diagram of the file", "image:image: A diagram of the file|paragraph:|"},
		{"without", "", "image:|paragraph:|"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := withImages(docWith(sp{1, 0, "body text"}), figureImage(7))
			fig := el(tag.RoleFigure, 1, 7)
			fig.Alt = c.alt
			out, _ := Tagged(d, tree(fig, el(tag.RoleP, 1, 0)), DefaultOptions)
			if got := roles(out.Preamble); got != c.want {
				t.Errorf("blocks = %s, want %s", got, c.want)
			}
			if im := out.Preamble[0].Image; im == nil || im.MCID != 7 {
				t.Errorf("the block does not point at its image: %+v", im)
			}
		})
	}
}

// TestATaggedFigureWithNoImageKeepsItsAlt: a Figure drawn in vectors paints no image, and its
// /Alt is its caption as before.
func TestATaggedFigureWithNoImageKeepsItsAlt(t *testing.T) {
	d := docWith(sp{1, 0, "body text"})
	fig := el(tag.RoleFigure, 1)
	fig.Alt = "a vector diagram"
	out, _ := Tagged(d, tree(fig, el(tag.RoleP, 1, 0)), DefaultOptions)
	if got, want := roles(out.Preamble), "figure:a vector diagram|paragraph:|"; got != want {
		t.Errorf("blocks = %s, want %s", got, want)
	}
}

// TestATaggedImageInsideASpanIsTaken: an image named by a non-block kid, a Span inside the
// Figure, is the Figure's, and a decorative one is left out.
func TestATaggedImageInsideASpanIsTaken(t *testing.T) {
	swatch := figureImage(9)
	swatch.Width, swatch.Height = 2, 2
	d := withImages(docWith(sp{1, 0, "body text"}), figureImage(8), swatch)
	fig := kids(el(tag.RoleFigure, 1), el(tag.RoleSpan, 1, 8), el(tag.RoleSpan, 1, 9))
	fig.Alt = "a chart"
	out, _ := Tagged(d, tree(fig, el(tag.RoleP, 1, 0)), DefaultOptions)
	if got, want := roles(out.Preamble), "image:image: a chart|paragraph:|"; got != want {
		t.Errorf("blocks = %s, want %s", got, want)
	}
}

// TestATaggedImageIsTakenOnce: two elements naming one image give one image line.
func TestATaggedImageIsTakenOnce(t *testing.T) {
	d := withImages(docWith(sp{1, 0, "body text"}), figureImage(8))
	out, _ := Tagged(d, tree(el(tag.RoleFigure, 1, 8), el(tag.RoleFigure, 1, 8), el(tag.RoleP, 1, 0)), DefaultOptions)
	if got, want := roles(out.Preamble), "image:|paragraph:|"; got != want {
		t.Errorf("blocks = %s, want %s", got, want)
	}
}

// TestOnlyAFiguresAltDescribesItsImage: a paragraph that names an image does not lend the image
// its /Alt, which describes the paragraph.
func TestOnlyAFiguresAltDescribesItsImage(t *testing.T) {
	d := withImages(docWith(sp{1, 0, "body text"}), figureImage(8))
	p := el(tag.RoleP, 1, 8, 0)
	p.Alt = "a paragraph"
	out, _ := Tagged(d, tree(p), DefaultOptions)
	if got, want := roles(out.Preamble), "image:|paragraph:a paragraph|"; got != want {
		t.Errorf("blocks = %s, want %s: the image undescribed, the paragraph's /Alt its own", got, want)
	}
}

// TestATaggedImageReadStandsAsWhatItHolds: an image read as text, with no /Alt to describe it,
// is its paragraphs where the Figure stands.
func TestATaggedImageReadStandsAsWhatItHolds(t *testing.T) {
	im := figureImage(8)
	im.Read = &doc.ImageRead{Kind: "text", Blocks: []doc.Block{{Role: doc.RoleParagraph,
		Spans: []doc.Span{{Text: "read off the pixels", MCID: -1}}}}}
	d := withImages(docWith(sp{1, 0, "body text"}), im)
	out, _ := Tagged(d, tree(el(tag.RoleFigure, 1, 8), el(tag.RoleP, 1, 0)), DefaultOptions)
	if len(out.Preamble) != 2 || out.Preamble[0].Text() != "read off the pixels" {
		t.Errorf("blocks = %s", roles(out.Preamble))
	}
}

package layout

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

func textBlock(x0, y0, x1, y1 float64, text string) doc.Block {
	return doc.Block{Role: doc.RoleParagraph, Box: geom.NewRect(x0, y0, x1, y1),
		Spans: []doc.Span{{Text: text, Box: geom.NewRect(x0, y0, x1, y1), MCID: -1}}}
}

func picture(x0, y0, x1, y1 float64) doc.Image {
	return doc.Image{Box: geom.NewRect(x0, y0, x1, y1), Width: 64, Height: 48, MCID: -1}
}

func order(p doc.Page) string {
	s := ""
	for _, b := range p.Blocks {
		if b.Role == doc.RoleImage {
			s += "[image]"
		} else {
			s += b.Text()
		}
		s += "|"
	}
	return s
}

// TestImagesStandInReadingOrder: an image goes before the first block below its top that shares
// its columns, after the blocks above it, and last when nothing shares its columns.
func TestImagesStandInReadingOrder(t *testing.T) {
	for _, c := range []struct {
		name string
		im   doc.Image
		want string
	}{
		{"between paragraphs", picture(100, 400, 300, 600), "above|[image]|below|right|"},
		{"above everything", picture(100, 720, 300, 760), "[image]|above|below|right|"},
		{"below everything", picture(100, 10, 300, 100), "above|below|[image]|right|"},
		// In the right column, it reads with the right column's block, not the left's.
		{"in the right column", picture(350, 600, 500, 680), "above|below|[image]|right|"},
		{"beside nothing", picture(560, 10, 600, 100), "above|below|right|[image]|"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792),
				Blocks: []doc.Block{
					textBlock(72, 650, 300, 700, "above"),
					textBlock(72, 200, 300, 350, "below"),
					textBlock(320, 300, 540, 500, "right"),
				},
				Images: []doc.Image{c.im}}}}
			if st := Images(d); st.Placed != 1 {
				t.Fatalf("stats = %+v", st)
			}
			if got := order(d.Pages[0]); got != c.want {
				t.Errorf("order = %s, want %s", got, c.want)
			}
			b := d.Pages[0].Blocks
			for i := range b {
				if b[i].Role == doc.RoleImage && (b[i].Image == nil || b[i].Image.Box != c.im.Box) {
					t.Errorf("the block does not point at its image: %+v", b[i].Image)
				}
			}
		})
	}
}

// TestImagesLeaveOutDecoration: artifacts, icons, fills, repeated logos and OCR'd scans are
// not placed, and each is counted.
func TestImagesLeaveOutDecoration(t *testing.T) {
	logo := picture(72, 740, 120, 780)
	mark := picture(0, 0, 612, 792)
	pages := func(extra ...doc.Image) []doc.Page {
		var ps []doc.Page
		for i := 1; i <= 4; i++ {
			ims := []doc.Image{logo}
			if i == 1 {
				ims = append(ims, extra...)
			}
			ps = append(ps, doc.Page{Number: i, Box: geom.NewRect(0, 0, 612, 792), Images: ims})
		}
		return ps
	}
	art := picture(100, 100, 300, 300)
	art.Artifact = true
	icon := picture(100, 100, 112, 111)
	fill := picture(100, 100, 300, 300)
	fill.Width, fill.Height = 4, 4
	edge := picture(100, 100, 300, 100+imageMinSide)
	d := &doc.Document{Pages: pages(art, icon, fill, edge)}
	st := Images(d)
	want := ImageStats{Placed: 1, Artifacts: 1, Small: 1, Fills: 1, Furniture: 4}
	if st != want {
		t.Errorf("stats = %+v, want %+v", st, want)
	}

	// A full-page scan on every page is the pages, not furniture.
	d = &doc.Document{}
	for i := 1; i <= 4; i++ {
		d.Pages = append(d.Pages, doc.Page{Number: i, Box: geom.NewRect(0, 0, 612, 792), Images: []doc.Image{mark}})
	}
	if st := Images(d); st.Placed != 4 || st.Furniture != 0 {
		t.Errorf("scans: %+v", st)
	}

	// A logo on 2 of 4 pages is under the 3 pages furniture needs.
	d = &doc.Document{Pages: pages()}
	d.Pages[2].Images, d.Pages[3].Images = nil, nil
	if st := Images(d); st.Placed != 2 {
		t.Errorf("a logo on two pages: %+v", st)
	}

	// Under invisible text, a scan is already read.
	ocred := doc.Page{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Images: []doc.Image{mark},
		Blocks: []doc.Block{{Spans: []doc.Span{{Text: "words", Box: geom.NewRect(72, 700, 200, 712), Style: doc.Style{Hidden: true}}}}}}
	d = &doc.Document{Pages: []doc.Page{ocred}}
	if st := Images(d); st.Read != 1 || st.Placed != 0 {
		t.Errorf("an OCR'd scan: %+v", st)
	}
}

func TestImageFillBound(t *testing.T) {
	for samples, placed := range map[[2]int]int{{4, 4}: 0, {4, 5}: 1, {2, 2}: 0} {
		im := picture(100, 100, 300, 300)
		im.Width, im.Height = samples[0], samples[1]
		d := &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Images: []doc.Image{im}}}}
		if st := Images(d); st.Placed != placed {
			t.Errorf("%dx%d samples: placed %d, want %d", samples[0], samples[1], st.Placed, placed)
		}
	}
}

// TestImagesFurnitureBoundaries: a logo on exactly half the pages, and 3 of them, is furniture;
// one painted twice on one page counts that page once.
func TestImagesFurnitureBoundaries(t *testing.T) {
	logo := picture(72, 740, 120, 780)
	doc6 := func(on ...int) *doc.Document {
		d := &doc.Document{}
		for i := 1; i <= 6; i++ {
			d.Pages = append(d.Pages, doc.Page{Number: i, Box: geom.NewRect(0, 0, 612, 792)})
		}
		for _, n := range on {
			d.Pages[n-1].Images = append(d.Pages[n-1].Images, logo)
		}
		return d
	}
	if st := Images(doc6(1, 2, 3)); st.Furniture != 3 {
		t.Errorf("on 3 of 6 pages: %+v, want furniture", st)
	}
	// Twice on page 1 and on page 2: two pages, under three.
	if st := Images(doc6(1, 1, 2)); st.Furniture != 0 || st.Placed != 3 {
		t.Errorf("twice on one page and once on another: %+v", st)
	}
}

// TestImagesPlacementBoundaries: a block whose top is the image's top is below it, blocks out of
// page order still put the image after the ones above, and visible text over an image does not
// hide it.
func TestImagesPlacementBoundaries(t *testing.T) {
	one := func(blocks []doc.Block, im doc.Image) string {
		d := &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Blocks: blocks, Images: []doc.Image{im}}}}
		Images(d)
		return order(d.Pages[0])
	}
	if got := one([]doc.Block{textBlock(72, 650, 300, 700, "level")}, picture(100, 500, 300, 700)); got != "[image]|level|" {
		t.Errorf("a block at the image's top: %s", got)
	}
	if got := one([]doc.Block{textBlock(72, 200, 300, 350, "below"), textBlock(72, 650, 300, 700, "above")},
		picture(100, 400, 300, 600)); got != "below|above|[image]|" {
		t.Errorf("blocks out of order: %s", got)
	}
	labelled := textBlock(150, 450, 250, 470, "label")
	if got := one([]doc.Block{labelled}, picture(100, 400, 300, 600)); got != "[image]|label|" {
		t.Errorf("a visible label over the image: %s", got)
	}
}

// TestFurnitureLeavesImagesAlone: different images in the top band of each page share no text,
// and are not taken for a running header.
func TestFurnitureLeavesImagesAlone(t *testing.T) {
	im := func(y float64) doc.Block {
		return doc.Block{Role: doc.RoleImage, Box: geom.Rect{X0: 72, Y0: y, X1: 300, Y1: y + 20}}
	}
	d := pages([]doc.Block{im(900), spanAt("Body text on the first page.", 500, 520)},
		[]doc.Block{im(905), spanAt("Body text on the second page.", 500, 520)})
	Furniture(d)
	if artifact(d, 0, 0) || artifact(d, 1, 0) {
		t.Error("an image in the top band was taken for furniture")
	}
}

// TestPageBreaksJoinPastAnImage: a figure a typesetter floated to the next page's top does not
// break the paragraph that continues under it.
func TestPageBreaksJoinPastAnImage(t *testing.T) {
	fig := doc.Block{Role: doc.RoleImage, Box: geom.Rect{X0: 72, Y0: 920, X1: 300, Y1: 990}}
	cont := para(2, bodyFace, 2, tline{text: "pines to the water.", x0: 72, x1: 200, y: 900})
	d := pages([]doc.Block{third()}, []doc.Block{fig, cont, fourth()})
	if n := PageBreaks(d); n != 1 {
		t.Fatalf("joined %d, want 1 past the image", n)
	}
	if d.Pages[1].Blocks[0].Role != doc.RoleImage {
		t.Errorf("page 2 = %q, want the image kept", texts(d.Pages[1].Blocks))
	}
}

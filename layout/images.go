package layout

import (
	"math"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// ImageStats counts what Images did.
type ImageStats struct {
	Placed, Artifacts, Small, Fills, Furniture, Read int
}

// imageMinSide is the shorter side, in points, below which an image is not placed: an icon set
// in a line of text or a table cell, a swatch, a bullet drawn as a bitmap. Over the corpus's
// 330 images, the ones under it are ISO 32000-2's 12 by 11 point table icons and logos 10 to 13
// points tall, and the smallest content image, a blend-mode sample, is 36 points on its short
// side (ADR 0045).
const imageMinSide = 16

// imageMinSamples is the sample count at or under which an image is a fill: ISO 32000-2 paints
// colour swatches as 2 by 2 images.
const imageMinSamples = 16

// Images places each image a page paints as a RoleImage block among the page's text blocks,
// before the first block whose top lies below the image's top and that shares its columns, and
// after every block above it. An image is left out when it is decoration:
//
//   - painted inside an /Artifact, the producer's statement that it is;
//   - under imageMinSide on its shorter side, or of imageMinSamples or fewer samples;
//   - furniture: the same size at the same place on 3 pages or more and half of them, a logo
//     in a running header, where it covers under a quarter of the page, since every page of
//     a scan is one image in the same place;
//   - already read: a scan under an invisible text layer, an OCR'd PDF, whose text is what
//     the image says. Any invisible text over the image is the sign, since that is what an
//     invisible text layer is for: pymupdf's two OCR'd fixtures lay 5 and 6 lines over a
//     full-page scan, a few percent of it.
func Images(d *doc.Document) ImageStats {
	keep, st := keepImages(d)
	for pi := range d.Pages {
		p := &d.Pages[pi]
		for i := range p.Images {
			if keep[pi][i] {
				placeImage(p, p.Images[i].Box, ImageBlocks(&p.Images[i]))
			}
		}
	}
	return st
}

// Keep reports, for each page and each of its images, whether Images places it: what is worth
// reading off the page, since what is decoration is not (ADR 0046).
func Keep(d *doc.Document) [][]bool {
	keep, _ := keepImages(d)
	return keep
}

func keepImages(d *doc.Document) ([][]bool, ImageStats) {
	var st ImageStats
	repeats := imageRepeats(d)
	keep := make([][]bool, len(d.Pages))
	for pi := range d.Pages {
		p := &d.Pages[pi]
		keep[pi] = make([]bool, len(p.Images))
		for i, im := range p.Images {
			switch Decoration(im) {
			case "artifact":
				st.Artifacts++
				continue
			case "small":
				st.Small++
				continue
			case "fill":
				st.Fills++
				continue
			}
			switch {
			case repeats[imageKeyOf(im)] >= 3 && 2*repeats[imageKeyOf(im)] >= len(d.Pages) &&
				im.Box.Area() < p.Box.Area()/4:
				st.Furniture++
				continue
			case hiddenOver(p, im.Box) > 0:
				st.Read++
				continue
			}
			keep[pi][i] = true
			st.Placed++
		}
	}
	return keep, st
}

// imageKey is an image's size and place, to a point, which is how a repeated logo is told.
type imageKey struct{ x, y, w, h, sw, sh int }

func imageKeyOf(im doc.Image) imageKey {
	r := func(v float64) int { return int(math.Round(v)) }
	return imageKey{r(im.Box.X0), r(im.Box.Y0), r(im.Box.Width()), r(im.Box.Height()), im.Width, im.Height}
}

// imageRepeats counts, for each size and place, the pages that paint an image there.
func imageRepeats(d *doc.Document) map[imageKey]int {
	n := map[imageKey]int{}
	for _, p := range d.Pages {
		seen := map[imageKey]bool{}
		for _, im := range p.Images {
			k := imageKeyOf(im)
			if !seen[k] {
				seen[k] = true
				n[k]++
			}
		}
	}
	return n
}

// ImageBlocks is what stands in for im (ADR 0046): the paragraphs or the cells read off it, for
// text and a table; its line, "[tag: description]", for a picture; its line and then its table
// of values for a chart; and "[image]" when nothing was read. The tagged path asks it too.
func ImageBlocks(im *doc.Image) []doc.Block {
	// No MCIDs on the image's line: a block's are the union of its spans', and the line has
	// none. The image's own is on Block.Image.
	line := doc.Block{Role: doc.RoleImage, Box: im.Box, Image: im}
	if im.Read == nil {
		return []doc.Block{line}
	}
	switch im.Read.Kind {
	case "text", "table":
		if len(im.Read.Blocks) > 0 {
			return append([]doc.Block(nil), im.Read.Blocks...)
		}
	case "chart":
		line.Alt = im.Read.Alt
		return append([]doc.Block{line}, im.Read.Blocks...)
	}
	line.Alt = im.Read.Alt
	return []doc.Block{line}
}

// placeImage inserts blocks, which stand for one image at box, among p's blocks: before the first
// block below the image's top that shares its columns, or after the last block above it that
// does, or last.
func placeImage(p *doc.Page, box geom.Rect, blocks []doc.Block) {
	b := doc.Block{Box: box}
	at, after := -1, -1
	for i, o := range p.Blocks {
		if o.Box.X1 <= b.Box.X0 || o.Box.X0 >= b.Box.X1 {
			continue
		}
		if o.Box.Y1 <= b.Box.Y1 {
			if at < 0 {
				at = i
			}
		} else {
			after = i
		}
	}
	switch {
	case at >= 0 && at > after:
	case after >= 0:
		at = after + 1
	default:
		at = len(p.Blocks)
	}
	p.Blocks = append(p.Blocks[:at], append(append([]doc.Block(nil), blocks...), p.Blocks[at:]...)...)
}

// hiddenOver is the area of p's invisible text inside box, the text layer an OCR'd PDF lays
// over its scan.
func hiddenOver(p *doc.Page, box geom.Rect) float64 {
	a := 0.0
	for _, b := range p.Blocks {
		for _, sp := range b.Spans {
			if sp.Style.Hidden {
				a += sp.Box.Intersect(box).Area()
			}
		}
	}
	return a
}

// Decoration says why im is decoration on its own, whatever page it is on: "artifact",
// "small" or "fill", or "" when it is not. The tagged path asks it too, so a structure tree
// that tags a swatch as a Figure does not put it in the output.
func Decoration(im doc.Image) string {
	switch {
	case im.Artifact:
		return "artifact"
	case math.Min(im.Box.Width(), im.Box.Height()) < imageMinSide:
		return "small"
	case im.Width*im.Height <= imageMinSamples:
		return "fill"
	}
	return ""
}

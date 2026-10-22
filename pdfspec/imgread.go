package pdfspec

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"regexp"
	"strings"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/layout"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/ocr/gemma"
)

// imageTables numbers the tables read off images from here, apart from the tables layout and
// the structure tree number from 1, so that no two share a number (ADR 0046).
const imageTables = 1 << 20

// describer is what reads a picture and a chart: inferd's vision model (ocr/gemma).
type describer interface {
	Describe(ctx context.Context, img image.Image) (gemma.Description, error)
	Chart(ctx context.Context, img image.Image) ([][]string, error)
	Name() string
}

// chartTags are the tags a description gives a chart by, its values then read as a table.
var chartTags = map[string]bool{"chart": true, "bar chart": true, "graph": true, "plot": true,
	"line chart": true, "pie chart": true, "line graph": true, "bar graph": true, "histogram": true}

// chartWords finds a chart in a description whose tag says something else: Gemma described the
// gold set's bar chart as "diagram: A bar chart displays monthly sales figures".
var chartWords = regexp.MustCompile(`(?i)\b(chart|graph|plot|histogram)s?\b`)

// imageCounts says what images reads found.
type imageCounts struct{ text, table, chart, picture, unread int }

// images reads what each kept image holds, off the rendered page (ADR 0046). Tesseract decides
// what can be decided without a model: a ruled grid with words in it is a table, words covering
// enough of it make it text. What is left is a chart or a picture, which describe, the vision
// model, tells apart and reads; without it the image stays "[image]".
//
// skip reports images the file already describes, a tagged Figure's /Alt, which are left as
// they are.
func (o *ocrRun) images(ctx context.Context, d *doc.Document, describe describer, skip func(page int, im doc.Image) bool) imageCounts {
	var n imageCounts
	if o.tables < imageTables {
		o.tables = imageTables
	}
	keep := layout.Keep(d)
	for pi := range d.Pages {
		p := &d.Pages[pi]
		var ra *rasterPage
		for ii := range p.Images {
			im := &p.Images[ii]
			if im.Read != nil || !keep[pi][ii] || (skip != nil && skip(p.Number, *im)) {
				continue
			}
			if ra == nil {
				r, err := o.r.Page(p.Number, o.ropt)
				if err != nil {
					fmt.Fprintf(o.log, "  page %d: %v\n", p.Number, err)
					break
				}
				ra = &rasterPage{img: r.Image, page: p.Box}
			}
			crop, box, ok := ra.crop(im.Box)
			if !ok {
				continue
			}
			words, err := o.eng.Read(ctx, crop)
			if err != nil {
				fmt.Fprintf(o.log, "  page %d: an image: %v\n", p.Number, err)
				continue
			}
			read := o.image(ctx, p.Number, im.Box, crop, box, words, describe, &o.tables)
			if read == nil {
				n.unread++
				continue
			}
			im.Read = read
			switch read.Kind {
			case "text":
				n.text++
			case "table":
				n.table++
			case "chart":
				n.chart++
			default:
				n.picture++
			}
		}
	}
	return n
}

// image reads one image's crop: a table, text, or with describe a chart or a picture; nil when
// it is none of those it can tell.
func (o *ocrRun) image(ctx context.Context, page int, at geom.Rect, crop image.Image, box image.Rectangle,
	words []ocr.Word, describe describer, table *int) *doc.ImageRead {
	by := o.eng.Name()
	if xs, ys, ok := ocr.Grid(crop); ok && ocr.IsText(words) {
		if cells := ocr.Cells(words, xs, ys); ocr.Filled(cells) {
			*table++
			return &doc.ImageRead{Kind: "table", By: by, Blocks: cellBlocks(cells, *table, page, at, box, xs, ys)}
		}
	}
	if ocr.IsTextImage(words, crop.Bounds()) {
		minConf := o.minConf
		if o.second != nil {
			if confirmed, _, err := ocr.Confirm(ctx, crop, words, o.second); err == nil {
				words, minConf, by = confirmed, ocr.SureConf, by+", "+o.second.Name()
			}
		}
		pg := ocr.Page(words, page, at, crop.Bounds(), minConf)
		return &doc.ImageRead{Kind: "text", By: by, Blocks: pg.Blocks}
	}
	if describe == nil {
		return nil
	}
	d, err := describe.Describe(ctx, crop)
	if err != nil {
		return nil
	}
	read := &doc.ImageRead{Kind: "picture", Alt: d.String(), By: describe.Name()}
	if chartTags[d.Tag] || chartWords.MatchString(d.Text) {
		read.Kind = "chart"
		if rows, err := describe.Chart(ctx, crop); err == nil {
			*table++
			read.Blocks = cellBlocks(rows, *table, page, at, box, nil, nil)
		}
	}
	return read
}

// cellBlocks makes a table's cell blocks, the first row its header, each at its place on the
// page: from the grid's lines where there are some, and the image's box where there are not.
func cellBlocks(rows [][]string, table, page int, at geom.Rect, box image.Rectangle, xs, ys []int) []doc.Block {
	var out []doc.Block
	for r, row := range rows {
		for c, text := range row {
			text = strings.Join(strings.Fields(text), " ")
			if text == "" {
				continue
			}
			cell := at
			if r+1 < len(ys) && c+1 < len(xs) {
				cell = pixelRect(at, box, image.Rect(xs[c], ys[r], xs[c+1], ys[r+1]))
			}
			out = append(out, doc.Block{Role: doc.RoleTableCell, Box: cell,
				Cell:  &doc.Cell{Table: table, Row: r, Col: c, Header: r == 0},
				Spans: []doc.Span{{Text: text, Box: cell, Page: page, MCID: -1}}})
		}
	}
	return out
}

// pixelRect maps px, in the crop's pixels from 0, to page coordinates within at, the crop's
// place on the page, box its pixels on the page image.
func pixelRect(at geom.Rect, box image.Rectangle, px image.Rectangle) geom.Rect {
	sx, sy := at.Width()/float64(box.Dx()), at.Height()/float64(box.Dy())
	return geom.NewRect(at.X0+float64(px.Min.X)*sx, at.Y1-float64(px.Max.Y)*sy,
		at.X0+float64(px.Max.X)*sx, at.Y1-float64(px.Min.Y)*sy)
}

// rasterPage is a rendered page and the page box it covers.
type rasterPage struct {
	img  image.Image
	page geom.Rect
}

// crop returns the part of the page image under r, a rectangle in page coordinates, with its
// pixel bounds, or false when nothing of it is on the page.
func (rp *rasterPage) crop(r geom.Rect) (image.Image, image.Rectangle, bool) {
	b := rp.img.Bounds()
	sx, sy := float64(b.Dx())/rp.page.Width(), float64(b.Dy())/rp.page.Height()
	px := image.Rect(b.Min.X+int((r.X0-rp.page.X0)*sx), b.Min.Y+int((rp.page.Y1-r.Y1)*sy),
		b.Min.X+int((r.X1-rp.page.X0)*sx+0.5), b.Min.Y+int((rp.page.Y1-r.Y0)*sy+0.5)).Intersect(b)
	if px.Dx() < 8 || px.Dy() < 8 {
		return nil, image.Rectangle{}, false
	}
	// Copied to its own image at the origin: the engines report positions in the image they
	// are given, from 0, and a sub-image keeps the page's offset, so every box read off one
	// would sit a crop's distance from its place.
	out := image.NewRGBA(image.Rect(0, 0, px.Dx(), px.Dy()))
	draw.Draw(out, out.Bounds(), rp.img, px.Min, draw.Src)
	return out, px, true
}

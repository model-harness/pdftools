package pdfspec

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/geom"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/ocr/gemma"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/tag"
)

// gridImage is a white image with a ruled grid of 3 by 2 cells, or blank.
func gridImage(ruled bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if ruled {
		black := image.NewUniform(color.Black)
		for _, x := range []int{10, 100, 190, 290} {
			draw.Draw(img, image.Rect(x, 10, x+2, 190), black, image.Point{}, draw.Src)
		}
		for _, y := range []int{10, 100, 188} {
			draw.Draw(img, image.Rect(10, y, 292, y+2), black, image.Point{}, draw.Src)
		}
	}
	return img
}

// word is a word Tesseract is sure of, in a line of its own.
func word(text string, x0, y0, x1, y1, line int) ocr.Word {
	return ocr.Word{Text: text, Box: image.Rect(x0, y0, x1, y1), Conf: 95, Block: 1, Par: 1, Line: line}
}

// fakeDescriber answers Describe and Chart with fixed replies.
type fakeDescriber struct {
	desc  gemma.Description
	rows  [][]string
	err   error
	asked int
}

func (f *fakeDescriber) Describe(context.Context, image.Image) (gemma.Description, error) {
	f.asked++
	return f.desc, f.err
}
func (f *fakeDescriber) Chart(context.Context, image.Image) ([][]string, error) { return f.rows, nil }
func (f *fakeDescriber) Name() string                                           { return "describer" }

// TestImageIsReadByKind: a ruled grid with words is a table, words covering the image make it
// text, and the rest goes to the describer, which a chart's description sends on for its values.
func TestImageIsReadByKind(t *testing.T) {
	at := geom.NewRect(100, 400, 400, 600)
	// The crop's pixels on the page image, which do not start at 0: an engine's boxes are the
	// crop's, from 0, and box is only where the crop came from.
	box := image.Rect(500, 300, 800, 500)
	cellWords := []ocr.Word{word("Fruit", 20, 40, 80, 60, 1), word("|", 98, 40, 102, 60, 1), word("Count", 110, 40, 170, 60, 1),
		word("Apples", 20, 130, 90, 150, 2), word("3", 110, 130, 120, 150, 2)}
	var many []ocr.Word
	for i := 0; i < 6; i++ {
		many = append(many, word("words", 10, 10+30*i, 290, 40+30*i, i+1))
	}
	chart := &fakeDescriber{desc: gemma.Description{Tag: "diagram", Text: "A bar chart of monthly sales"},
		rows: [][]string{{"Month", "Sales"}, {"Jan", "12"}}}
	photo := &fakeDescriber{desc: gemma.Description{Tag: "photo", Text: "a geyser erupting"}}
	for _, c := range []struct {
		name     string
		img      image.Image
		words    []ocr.Word
		describe describer
		kind     string
		blocks   int
		alt      string
	}{
		{"a ruled table", gridImage(true), cellWords, nil, "table", 4, ""},
		{"text", gridImage(false), many, nil, "text", 1, ""},
		{"a chart", gridImage(false), nil, chart, "chart", 4, "diagram: A bar chart of monthly sales"},
		{"a picture", gridImage(false), nil, photo, "picture", 0, "photo: a geyser erupting"},
		{"nothing to ask", gridImage(false), nil, nil, "", 0, ""},
		{"a describer that fails", gridImage(false), nil, &fakeDescriber{err: errors.New("refused")}, "", 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := &ocrRun{log: io.Discard, eng: &fakeEngine{}, minConf: 30}
			table := imageTables
			read := o.image(context.Background(), 1, at, c.img, box, c.words, c.describe, &table)
			if c.kind == "" {
				if read != nil {
					t.Errorf("read %+v, want nothing", read)
				}
				return
			}
			if read == nil || read.Kind != c.kind || len(read.Blocks) != c.blocks || read.Alt != c.alt {
				t.Fatalf("read %+v, want %s with %d blocks and %q", read, c.kind, c.blocks, c.alt)
			}
			if c.kind == "table" {
				cell := read.Blocks[0]
				if cell.Cell == nil || cell.Cell.Table != imageTables+1 || !cell.Cell.Header || cell.Text() != "Fruit" {
					t.Errorf("first cell = %+v %q", cell.Cell, cell.Text())
				}
				// The cell's place on the page: columns from pixel 11 to 101 of 300 across 300
				// points, rows from 11 to 101 of 200 down 200 points from the top at 600.
				if b := read.Blocks[0].Box; b.X0 < 110 || b.X0 > 112 || b.X1 < 200 || b.X1 > 202 || b.Y1 < 588 || b.Y1 > 590 || b.Y0 < 498 || b.Y0 > 500 {
					t.Errorf("first cell box = %+v", b)
				}
				// A second-row cell, off the middle row, where a y read from the bottom and one
				// read from the top part: rows 100 to 188 of 200, so 412 to 500.
				if b := read.Blocks[2].Box; read.Blocks[2].Text() != "Apples" || b.Y0 < 411 || b.Y0 > 413 || b.Y1 < 499 || b.Y1 > 501 {
					t.Errorf("Apples' cell = %q %+v", read.Blocks[2].Text(), b)
				}
			}
		})
	}
}

func TestChartWordsFindAChart(t *testing.T) {
	for text, want := range map[string]bool{
		"A bar chart of monthly sales":   true,
		"two line graphs over a year":    true,
		"a scatter plot":                 true,
		"a photograph of a mountain":     false,
		"a paragraph of chartered terms": false,
	} {
		if got := chartWords.MatchString(text); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}

// TestCropIsAtTheOrigin: a crop is its own image from 0, so the boxes an engine reads off it
// are the crop's; a rectangle mostly off the page, or too small, is no crop.
func TestCropIsAtTheOrigin(t *testing.T) {
	page := image.NewRGBA(image.Rect(0, 0, 612, 792))
	page.Set(200, 292, color.RGBA{R: 255, A: 255})
	rp := &rasterPage{img: page, page: geom.NewRect(0, 0, 612, 792)}
	crop, px, ok := rp.crop(geom.NewRect(200, 400, 300, 500))
	if !ok || crop.Bounds() != image.Rect(0, 0, 100, 100) || px != image.Rect(200, 292, 300, 392) {
		t.Fatalf("crop %v at %v, ok %v", crop.Bounds(), px, ok)
	}
	if r, _, _, _ := crop.At(0, 0).RGBA(); r>>8 != 255 {
		t.Error("the crop's first pixel is not the page's at its corner")
	}
	if _, _, ok := rp.crop(geom.NewRect(608, 0, 700, 10)); ok {
		t.Error("a sliver at the page's edge was cropped")
	}
}

// TestFigureAltsNamesWhatTheTreeDescribes: an image named by a Figure with an /Alt, or by a
// kid of one, is described; one under a Figure with none, or on another page, is not.
func TestFigureAltsNamesWhatTheTreeDescribes(t *testing.T) {
	kid := &tag.Elem{Role: tag.RoleSpan, Content: []tag.MCRef{{MCID: 4, Page: 1}}}
	fig := &tag.Elem{Role: tag.RoleFigure, Alt: "a diagram", Content: []tag.MCRef{{MCID: 3, Page: 1}}, Kids: []*tag.Elem{kid}}
	bare := &tag.Elem{Role: tag.RoleFigure, Alt: "  ", Content: []tag.MCRef{{MCID: 5, Page: 1}}}
	root := &tag.Elem{Kids: []*tag.Elem{{Role: tag.RoleSect, Kids: []*tag.Elem{fig, bare}}}}
	described := figureAlts(&tag.Tree{Root: root})
	for _, c := range []struct {
		page, mcid int
		want       bool
	}{{1, 3, true}, {1, 4, true}, {1, 5, false}, {2, 3, false}, {1, -1, false}} {
		if got := described(c.page, doc.Image{MCID: c.mcid}); got != c.want {
			t.Errorf("page %d MCID %d: %v, want %v", c.page, c.mcid, got, c.want)
		}
	}
}

// TestCellBlocksSkipEmptyCells: an empty cell has no block, and the rest keep their places.
func TestCellBlocksSkipEmptyCells(t *testing.T) {
	blocks := cellBlocks([][]string{{"a", ""}, {" ", "b  c"}}, 7, 2, geom.NewRect(0, 0, 100, 100), image.Rect(0, 0, 10, 10), nil, nil)
	if len(blocks) != 2 || blocks[1].Text() != "b c" || blocks[1].Cell.Row != 1 || blocks[1].Cell.Col != 1 || blocks[1].Cell.Header {
		var got []string
		for _, b := range blocks {
			got = append(got, b.Text())
		}
		t.Errorf("blocks = %s", strings.Join(got, "|"))
	}
}

// TestTaggedFiguresAreNotRead: ISO/TS 32001's four images are each its Figure's, described by
// /Alt, and none is read off the pixels. The tree's pages must be resolved to know it, and
// without them every image was read. A sponsored copy, not in the repository: skipped without it.
func TestTaggedFiguresAreNotRead(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "ISO_TS_32001-2022_sponsored_EC3.pdf")
	if _, err := os.Stat(path); err != nil {
		t.Skip("the sponsored ISO/TS 32001 is not on disk")
	}
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	d, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatal(err)
	}
	described, n := describedImages(s, d), 0
	for _, p := range d.Pages {
		for _, im := range p.Images {
			if im.MCID >= 0 && !described(p.Number, im) {
				t.Errorf("page %d: image %d is not described", p.Number, im.MCID)
			}
			n++
		}
	}
	if n < 4 {
		t.Errorf("%d images, want ISO/TS 32001's 4 at least", n)
	}
}

func TestFilledNeedsTwoRowsAndTwoColumns(t *testing.T) {
	for _, c := range []struct {
		cells [][]string
		want  bool
	}{
		{[][]string{{"a", ""}, {"", "b"}}, true},
		{[][]string{{"a", "b"}, {"", ""}}, false},
		{[][]string{{"a", ""}, {"b", ""}}, false},
		{nil, false},
	} {
		if got := ocr.Filled(c.cells); got != c.want {
			t.Errorf("Filled(%q) = %v, want %v", c.cells, got, c.want)
		}
	}
}

// TestImageTextIsReadAgain: a text image's words under 90 go to the second reader, as a scanned
// page's do, and the reading names it.
func TestImageTextIsReadAgain(t *testing.T) {
	var many []ocr.Word
	for i := 0; i < 6; i++ {
		many = append(many, word("words", 10, 10+30*i, 290, 40+30*i, i+1))
	}
	many[0].Conf = 50
	second := &secondReader{answer: "other"}
	o := &ocrRun{log: io.Discard, eng: &fakeEngine{}, minConf: 30, second: second}
	table := imageTables
	read := o.image(context.Background(), 1, geom.NewRect(0, 0, 300, 200), gridImage(false), image.Rect(0, 0, 300, 200), many, nil, &table)
	if read == nil || read.Kind != "text" || !strings.Contains(read.By, "second") || second.calls != 1 {
		t.Fatalf("read %+v, asked %d", read, second.calls)
	}
	if !strings.HasPrefix(read.Blocks[0].Text(), doc.Unmapped) {
		t.Errorf("a word the second reader disagreed with: %q, want [err]", read.Blocks[0].Text())
	}
}

// fakeRasterizer renders every page as a white page of 612 by 792 pixels.
type fakeRasterizer struct{ pages int }

func (f *fakeRasterizer) PageCount() int { return f.pages }
func (f *fakeRasterizer) Page(n int, _ render.Options) (*render.Raster, error) {
	img := image.NewRGBA(image.Rect(0, 0, 612, 792))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	return &render.Raster{Number: n, Image: img, DPI: 72, Box: geom.NewRect(0, 0, 612, 792)}, nil
}
func (f *fakeRasterizer) Close() error { return nil }

// TestImagesReadsWhatIsKept: an image layout keeps is read, a decorative one and one the file
// describes are not, and one already read is left as it is.
func TestImagesReadsWhatIsKept(t *testing.T) {
	pic := func(x float64) doc.Image {
		return doc.Image{Box: geom.NewRect(x, 400, x+100, 500), Width: 64, Height: 48, MCID: int(x)}
	}
	icon := pic(400)
	icon.Box = geom.NewRect(400, 400, 410, 410)
	done := pic(500)
	done.Read = &doc.ImageRead{Kind: "picture", Alt: "photo: kept"}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792),
		Images: []doc.Image{pic(100), pic(250), icon, done}}}}
	eng := &fakeEngine{}
	o := &ocrRun{log: io.Discard, eng: eng, r: &fakeRasterizer{pages: 1}, ropt: render.Options{DPI: 72, MaxPixels: render.DefaultOptions.MaxPixels}}
	describe := &fakeDescriber{desc: gemma.Description{Tag: "photo", Text: "a geyser erupting"}}
	n := o.images(context.Background(), d, describe, func(page int, im doc.Image) bool { return im.MCID == 250 })
	if n != (imageCounts{picture: 1}) || eng.calls != 1 || describe.asked != 1 {
		t.Errorf("counts %+v, engine read %d, described %d: want the one kept image read", n, eng.calls, describe.asked)
	}
	ims := d.Pages[0].Images
	if ims[0].Read == nil || ims[1].Read != nil || ims[2].Read != nil || ims[3].Read.Alt != "photo: kept" {
		t.Errorf("reads = %+v %+v %+v %+v", ims[0].Read, ims[1].Read, ims[2].Read, ims[3].Read)
	}
}

// TestReadReadsImagesOnPagesWithText: a page whose text keeps it from OCR still has its images
// read, here by an engine that is not there.
func TestReadReadsImagesOnPagesWithText(t *testing.T) {
	opt := offline()
	opt.Tesseract = filepath.Join(t.TempDir(), "no-tesseract.exe")
	var log strings.Builder
	opt.Log = &log
	d, err := Open(fixture(t, "adobe-samples/exportPDFInput.pdf"), opt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if len(Route(d.Doc, allPages(len(d.Doc.Pages)), ocr.DefaultThreshold, false)) != 0 {
		t.Fatal("test setup: a page of exportPDFInput routes")
	}
	if err := d.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "no-tesseract.exe does not run") {
		t.Errorf("log = %q, want the images to have asked for an engine", log.String())
	}
}

// turnedTable is a 1000 by 1400 page image with a ruled table of 3 columns and 2 rows, from x 200
// to 800 and y 400 to 600, turned by deg degrees about the page's centre.
func turnedTable(deg float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1400))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	sin, cos := math.Sincos(deg * math.Pi / 180)
	put := func(x, y float64) {
		dx, dy := x-500, y-700
		img.Set(int(cos*dx-sin*dy+500), int(sin*dx+cos*dy+700), color.Black)
	}
	for _, y := range []float64{400, 500, 600} {
		for x := 200.0; x <= 802; x += 0.5 {
			for th := 0.0; th < 3; th += 0.5 {
				put(x, y+th)
			}
		}
	}
	for _, x := range []float64{200, 400, 600, 800} {
		for y := 400.0; y <= 602; y += 0.5 {
			for th := 0.0; th < 3; th += 0.5 {
				put(x+th, y)
			}
		}
	}
	return img
}

// pageRasterizer renders every page as one image.
type pageRasterizer struct{ img image.Image }

func (r *pageRasterizer) PageCount() int { return 1 }
func (r *pageRasterizer) Page(n int, _ render.Options) (*render.Raster, error) {
	return &render.Raster{Number: n, Image: r.img, DPI: 200, Box: geom.NewRect(0, 0, 360, 504)}, nil
}
func (r *pageRasterizer) Close() error { return nil }

// TestOCRPageIsLevelledAndItsTableRead: a scan turned by a degree is levelled before it is read,
// so its ruled table is found and its words, read off the level page, are its cells and not text
// again; a cell's word read below the floor is [err].
func TestOCRPageIsLevelledAndItsTableRead(t *testing.T) {
	var words []ocr.Word
	for i, w := range []struct {
		text string
		x, y int
	}{{"Name", 220, 440}, {"Qty", 420, 440}, {"Apples", 220, 540}, {"3", 420, 540}} {
		words = append(words, ocr.Word{Text: w.text, Box: image.Rect(w.x, w.y, w.x+60, w.y+20), Conf: 95, Block: 1, Par: 1, Line: i/2 + 1})
	}
	words[3].Conf = 10
	for i := 0; i < 8; i++ {
		words = append(words, ocr.Word{Text: "prose", Box: image.Rect(200, 800+i*30, 300, 820+i*30), Conf: 95, Block: 2, Par: 1, Line: i + 1})
	}
	o := &ocrRun{log: io.Discard, eng: &fakeEngine{words: words}, r: &pageRasterizer{img: turnedTable(1)}, minConf: 30, force: true,
		full: func(int) doc.Page { return doc.Page{} }}
	p, _, err := o.page(context.Background(), 1, doc.Page{Number: 1, Box: geom.NewRect(0, 0, 360, 504)})
	if err != nil {
		t.Fatal(err)
	}
	var cells, doubted int
	for _, b := range p.Blocks {
		if b.Role == doc.RoleTableCell {
			cells++
			if b.Text() == doc.Unmapped {
				doubted++
			}
		} else if strings.Contains(b.Text(), "Apples") {
			t.Errorf("a cell's word written again as text: %q", b.Text())
		}
	}
	if doubted != 1 {
		t.Errorf("%d cells [err], want the one read at 10", doubted)
	}
	if cells != 4 || p.Blocks[0].Role != doc.RoleTableCell || p.Blocks[0].Text() != "Name" {
		t.Errorf("%d cells, first block %s %q: want the table read, first", cells, p.Blocks[0].Role, p.Blocks[0].Text())
	}
}

package pdfspec

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// pageOfText is a page whose one paragraph covers most of it, so that the router leaves it.
func pageOfText(n int, text string) doc.Page {
	box := geom.NewRect(0, 0, 612, 792)
	return doc.Page{Number: n, Box: box, Blocks: []doc.Block{{Role: doc.RoleParagraph, Box: geom.NewRect(36, 36, 576, 756),
		Spans: []doc.Span{{Text: text, Box: geom.NewRect(36, 36, 576, 756), Page: n, MCID: -1}}}}}
}

// picture is an image worth reading: large, with samples, no artifact.
func picture(mcid int) doc.Image {
	return doc.Image{Box: geom.NewRect(100, 100, 300, 300), Width: 200, Height: 200, MCID: mcid}
}

// TestBareImagesCountWhatIsWrittenImage: an image line with nothing on it is counted on its
// image's page, on the pages' own blocks and on an outline's; one with a description is not.
func TestBareImagesCountWhatIsWrittenImage(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{pageOfText(1, "one"), pageOfText(2, "two")}}
	d.Pages[0].Images = []doc.Image{picture(-1), picture(-1)}
	d.Pages[1].Images = []doc.Image{picture(-1)}
	line := func(im *doc.Image, alt string) doc.Block { return doc.Block{Role: doc.RoleImage, Image: im, Alt: alt} }
	d.Pages[0].Blocks = append(d.Pages[0].Blocks, line(&d.Pages[0].Images[0], ""), line(&d.Pages[0].Images[1], "photo: a dog"))
	d.Pages[1].Blocks = append(d.Pages[1].Blocks, line(&d.Pages[1].Images[0], " "))
	got := bareImages(d, imageLines(d, nil))
	if len(got) != 2 || got[0] != (pageCount{1, 1}) || got[1] != (pageCount{2, 1}) {
		t.Errorf("pages %v, want p1 1, p2 1", got)
	}
	o := &doc.Outline{
		Preamble: []doc.Block{line(&d.Pages[1].Images[0], "")},
		Sections: []*doc.Section{{Blocks: []doc.Block{line(&d.Pages[0].Images[0], ""), line(&d.Pages[0].Images[1], "")}}},
		Unplaced: []doc.Page{{Number: 2, Blocks: []doc.Block{line(&d.Pages[1].Images[0], "")}}},
	}
	if got := bareImages(d, imageLines(d, o)); len(got) != 2 || got[0] != (pageCount{1, 2}) || got[1] != (pageCount{2, 2}) {
		t.Errorf("outline pages %v, want p1 2, p2 2", got)
	}
	var w bytes.Buffer
	warnIfBareImages(&w, bareImages(d, imageLines(d, o)))
	if want := "note: 4 image(s) on 2 page(s) are written [image], with nothing read off them\n  by page: p1 2, p2 2\n"; w.String() != want {
		t.Errorf("%q", w.String())
	}
	w.Reset()
	if warnIfBareImages(&w, nil); w.Len() != 0 {
		t.Errorf("no images: %q", w.String())
	}
}

// TestPageNeedsAreWhatMDReads: a page with no text is read whole, with its image; an image worth
// reading, and not one the file describes or a decoration, is one to read; a glyph with no value
// is one to read.
func TestPageNeedsAreWhatMDReads(t *testing.T) {
	scan := doc.Page{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Images: []doc.Image{picture(-1)}}
	text := pageOfText(2, "a page "+doc.Unmapped+doc.Unmapped+" of text")
	small := picture(-1)
	small.Box = geom.NewRect(0, 0, 8, 8)
	text.Images = []doc.Image{picture(4), picture(5), small}
	d := &doc.Document{Pages: []doc.Page{scan, text, pageOfText(3, "plain")}}
	described := func(page int, im doc.Image) bool { return page == 2 && im.MCID == 5 }
	got := pageNeeds(d, described)
	want := []PageNeed{{Page: 1, Whole: true}, {Page: 2, Images: 1, Unmapped: 2}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("needs %+v, want %+v", got, want)
	}
	if line := NeedsLine(got); line != "read whole: 1; images: p2 1; glyphs with no value: p2 2" {
		t.Errorf("line %q", line)
	}
	if line := NeedsLine(nil); line != "nothing for an engine to read" {
		t.Errorf("line %q", line)
	}
	if got := pageNeeds(d, nil); got[1].Images != 2 {
		t.Errorf("with no descriptions, %d images to read, want 2", got[1].Images)
	}
}

// TestFindEnginesAsMDDoes: an engine set off is off, and one named that is not there says why.
func TestFindEnginesAsMDDoes(t *testing.T) {
	if e := FindEngines(context.Background(), "off", "off"); e != (Engines{Tesseract: "off", Inferd: "off"}) {
		t.Errorf("engines %+v, want both off", e)
	}
	if e := FindEngines(context.Background(), filepath.Join(t.TempDir(), "no-tesseract.exe"), "off"); !strings.Contains(e.Tesseract, "does not run") {
		t.Errorf("a tesseract that is not there: %q", e.Tesseract)
	}
	for _, c := range []struct {
		e    Engines
		want string
	}{
		{Engines{"tesseract 5.5.3 eng", "gemma-4-e4b via inferd"}, "tesseract 5.5.3 eng; gemma-4-e4b via inferd"},
		{Engines{"not found", "unavailable: no daemon"}, "tesseract not found; inferd unavailable: no daemon"},
		{Engines{"tesseract at x does not run", "off"}, "tesseract at x does not run; inferd off"},
	} {
		if got := c.e.String(); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
}

// TestMDReportsItsBareImages: md says how many images it wrote as [image], on the page path and
// on the outline's.
func TestMDReportsItsBareImages(t *testing.T) {
	for _, c := range []struct {
		path    string
		outline bool
		want    string
	}{
		{"reference/image-kinds.pdf", false, "note: 4 image(s) on 1 page(s) are written [image]"},
		{"adobe-samples/watermark.pdf", true, "note: 1 image(s) on 1 page(s) are written [image]"},
	} {
		path := fixture(t, c.path)
		// The outline is read from a document of its own, since reading one rebuilds the text it
		// is built from.
		if o, err := outlineOf(t, path); err != nil || (o != nil) != c.outline {
			t.Fatalf("%s: outline %v, %v; want %v", c.path, o != nil, err, c.outline)
		}
		var log strings.Builder
		opt := offline()
		opt.Log = &log
		d, err := Open(path, opt)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Markdown(io.Discard); err != nil {
			t.Fatal(err)
		}
		_ = d.Close()
		if !strings.Contains(log.String(), c.want) {
			t.Errorf("%s: log %q, want %q", c.path, log.String(), c.want)
		}
	}
}

// outlineOf is the outline the file at path declares, or nil.
func outlineOf(t *testing.T, path string) (*doc.Outline, error) {
	t.Helper()
	d, err := Open(path, offline())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	return d.Outline()
}

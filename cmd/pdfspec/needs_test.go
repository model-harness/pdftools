package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/sink/markdown"
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
	want := []pageNeed{{Page: 1, Whole: true}, {Page: 2, Images: 1, Unmapped: 2}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("needs %+v, want %+v", got, want)
	}
	if line := needsLine(got); line != "read whole: 1; images: p2 1; glyphs with no value: p2 2" {
		t.Errorf("line %q", line)
	}
	if line := needsLine(nil); line != "nothing for an engine to read" {
		t.Errorf("line %q", line)
	}
	if got := pageNeeds(d, nil); got[1].Images != 2 {
		t.Errorf("with no descriptions, %d images to read, want 2", got[1].Images)
	}
}

// TestFindEnginesAsMDDoes: an engine set off is off, and one named that is not there says why.
func TestFindEnginesAsMDDoes(t *testing.T) {
	t.Setenv(tesseractEnv, "off")
	t.Setenv(inferdEnv, "off")
	if e := findEngines(context.Background()); e != (engines{Tesseract: "off", Inferd: "off"}) {
		t.Errorf("engines %+v, want both off", e)
	}
	t.Setenv(tesseractEnv, filepath.Join(t.TempDir(), "no-tesseract.exe"))
	if e := findEngines(context.Background()); !strings.Contains(e.Tesseract, "does not run") {
		t.Errorf("a tesseract that is not there: %q", e.Tesseract)
	}
	for _, c := range []struct {
		e    engines
		want string
	}{
		{engines{"tesseract 5.5.3 eng", "gemma-4-e4b via inferd"}, "tesseract 5.5.3 eng; gemma-4-e4b via inferd"},
		{engines{"not found", "unavailable: no daemon"}, "tesseract not found; inferd unavailable: no daemon"},
		{engines{"tesseract at x does not run", "off"}, "tesseract at x does not run; inferd off"},
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
		s, err := pcstore.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		d := extractOnly(t, path)
		if o, err := readOutline(s, extractOnly(t, path)); err != nil || (o != nil) != c.outline {
			t.Fatalf("%s: outline %v, %v; want %v", c.path, o != nil, err, c.outline)
		}
		got := captureStderr(t, func() {
			if err := writeMarkdown(io.Discard, s, d, false, markdown.Options{}); err != nil {
				t.Fatal(err)
			}
		})
		_ = s.Close()
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: stderr %q, want %q", c.path, got, c.want)
		}
	}
}

// stdout runs fn and returns what it wrote to os.Stdout.
func stdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	return <-done
}

// TestProbeSaysWhatEachPageNeeds: probe names the engines md would use and what each page would
// ask of them, in its text and in its JSON.
func TestProbeSaysWhatEachPageNeeds(t *testing.T) {
	path := fixture(t, "reference/type3-words.pdf")
	if r := probeOne(path, false); len(r.Needs) != 1 || r.Needs[0] != (pageNeed{Page: 1, Unmapped: 42}) {
		t.Errorf("needs %+v, want page 1's 42 glyphs", r.Needs)
	}
	text := stdout(t, func() {
		if err := probe([]string{path}, false, false); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"  engines     tesseract off; inferd off\n", "  needs       glyphs with no value: p1 42\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("probe wrote %q, want %q", text, want)
		}
	}
	js := stdout(t, func() {
		if err := probe([]string{path}, true, false); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(js, `"tesseract": "off"`) || !strings.Contains(js, `"unmapped": 42`) || !strings.Contains(js, `"needs": [`) {
		t.Errorf("probe -json wrote %s", js)
	}
}

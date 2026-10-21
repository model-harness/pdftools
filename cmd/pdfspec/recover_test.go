package main

import (
	"context"
	"errors"
	stdimage "image"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/ocr"
)

// onLine is a reading of texts standing on the line whose top is at pixel y of fakeRasterizer's
// page, where a point is a pixel, each at conf.
func onLine(y int, conf float64, texts ...string) []ocr.Word {
	out := make([]ocr.Word, len(texts))
	for i, t := range texts {
		out[i] = ocr.Word{Text: t, Box: stdimage.Rect(80+i*60, y+1, 80+i*60+50, y+11), Conf: conf}
	}
	return out
}

// twoLines is a page of one block whose one span runs on across two lines, a glyph with no
// value in each, and the place of the second line in the span's text.
func twoLines() (*doc.Document, int) {
	text := "When crea" + doc.Unmapped + "ng lists, and mo" + doc.Unmapped + "on"
	second := strings.Index(text, "and")
	b := doc.Block{Role: doc.RoleParagraph, Box: geom.NewRect(72, 680, 300, 720),
		Spans: []doc.Span{{Text: text, Box: geom.NewRect(72, 680, 300, 720), Page: 1, MCID: -1}},
		Lines: []doc.Line{
			{Box: geom.NewRect(72, 708, 300, 720), Page: 1, Size: 10, Span: 0, Offset: 0},
			{Box: geom.NewRect(72, 680, 200, 692), Page: 1, Size: 10, Span: 0, Offset: second},
		}}
	return &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Blocks: []doc.Block{b}}}}, second
}

// TestGlyphsReadEachLine: the page is read once, and each line's glyph with no value is read
// from the words standing on that line, written into the span, and recorded with its engine and
// confidence; the second line's start moves by what the first reading changed the span's length
// by.
func TestGlyphsReadEachLine(t *testing.T) {
	d, _ := twoLines()
	eng := &fakeEngine{words: append(onLine(72, 96, "When", "creating", "lists,"), onLine(100, 95, "and", "motion")...)}
	o := &ocrRun{eng: eng, r: &fakeRasterizer{pages: 1}}
	total, read, err := o.glyphs(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	b := d.Pages[0].Blocks[0]
	if total != 2 || read != 2 || b.Spans[0].Text != "When creating lists, and motion" {
		t.Fatalf("read %d of %d: %q", read, total, b.Spans[0].Text)
	}
	if got := b.Spans[0].Text[b.Lines[1].Offset:]; got != "and motion" {
		t.Errorf("the second line starts at %q", got)
	}
	rec := d.Pages[0].Recovered
	if len(rec) != 2 || rec[0].Text != "ti" || rec[0].Engine != "fake" || rec[0].Conf != 96 || rec[0].Glyphs != 1 ||
		rec[1].Text != "ti" || rec[1].Conf != 95 || rec[1].Box != b.Lines[1].Box {
		t.Errorf("recovered %+v", rec)
	}
	if lines := recoveredPages(d); len(lines) != 1 || lines[0] != "p1 2 by fake at 95-96" {
		t.Errorf("report %q", lines)
	}
	if eng.calls != 1 {
		t.Errorf("%d readings of one page, want one", eng.calls)
	}
}

// TestGlyphsNeedSureOrAgreed: a reading under SureConf, or a glyph read alone however sure, is
// taken only where the second reader agrees with it; and a page read by OCR is not read again.
func TestGlyphsNeedSureOrAgreed(t *testing.T) {
	lone := func() *doc.Document {
		b := doc.Block{Role: doc.RoleParagraph, Spans: []doc.Span{{Text: doc.Unmapped + " mutool usage", Box: geom.NewRect(72, 708, 300, 720), Page: 1, MCID: -1}}}
		return &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Blocks: []doc.Block{b}}}}
	}
	for _, c := range []struct {
		name   string
		d      *doc.Document
		read   []ocr.Word
		second *secondReader
		want   string
		engine string
	}{
		{"unsure, alone", nil, onLine(72, 80, "When", "creating", "lists,"), nil, "When crea" + doc.Unmapped + "ng", ""},
		{"unsure, agreed", nil, onLine(72, 80, "When", "creating", "lists,"), &secondReader{answer: "creating"}, "When creating", "second"},
		{"unsure, refused", nil, onLine(72, 80, "When", "creating", "lists,"), &secondReader{answer: "crea"}, "When crea" + doc.Unmapped + "ng", ""},
		{"a lone glyph, sure", lone(), onLine(72, 99, "$", "mutool", "usage"), nil, doc.Unmapped + " mutool", ""},
		{"a lone glyph, agreed", lone(), onLine(72, 99, "$", "mutool", "usage"), &secondReader{answer: "$"}, "$ mutool", "second"},
		{"a lone glyph, refused", lone(), onLine(72, 99, "e", "mutool", "usage"), &secondReader{answer: "•"}, doc.Unmapped + " mutool", ""},
	} {
		d := c.d
		if d == nil {
			d, _ = twoLines()
		}
		o := &ocrRun{eng: &fakeEngine{words: c.read}, r: &fakeRasterizer{pages: 1}}
		if c.second != nil {
			o.second = c.second
		}
		if _, _, err := o.glyphs(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		if got := d.Pages[0].Blocks[0].Spans[0].Text; !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		rec := d.Pages[0].Recovered
		if c.engine == "" && len(rec) != 0 || c.engine != "" && (len(rec) != 1 || rec[0].Engine != c.engine) {
			t.Errorf("%s: recovered %+v, want by %q", c.name, rec, c.engine)
		}
		if c.second != nil && c.second.calls != 1 {
			t.Errorf("%s: the second reader was asked %d times, want once", c.name, c.second.calls)
		}
	}
	d, _ := twoLines()
	d.Pages[0].Rasterized = true
	eng := &fakeEngine{}
	if total, _, _ := (&ocrRun{eng: eng, r: &fakeRasterizer{pages: 1}}).glyphs(context.Background(), d); total != 0 || eng.calls != 0 {
		t.Errorf("a page read by OCR: %d glyphs, %d reads", total, eng.calls)
	}
}

// TestLineWordsStandOnTheLine: a line takes the words whose centre is on it, a line's height of
// slack to each side, left to right, and none from the lines above and below.
func TestLineWordsStandOnTheLine(t *testing.T) {
	w := func(text string, x, y int) ocr.Word {
		return ocr.Word{Text: text, Box: stdimage.Rect(x, y, x+20, y+10)}
	}
	page := []ocr.Word{w("right", 200, 100), w("left", 100, 100), w("above", 100, 80), w("below", 100, 115),
		w("near", 82, 100), w("far", 60, 100)}
	got := lineWords(page, stdimage.Rect(100, 100, 220, 110))
	var texts []string
	for _, g := range got {
		texts = append(texts, g.Text)
	}
	if strings.Join(texts, " ") != "near left right" {
		t.Errorf("line words %q, want near left right", texts)
	}
}

// TestTextLinesFollowTheSpans: a line's text is its pieces of the spans it runs across, a block
// with no lines is its spans, and an offset past its span leaves out both lines it bounds.
func TestTextLinesFollowTheSpans(t *testing.T) {
	b := doc.Block{
		Spans: []doc.Span{{Text: "one two ", Page: 1}, {Text: "three four", Page: 1}},
		Lines: []doc.Line{{Box: geom.NewRect(0, 20, 100, 30), Span: 0, Offset: 0}, {Box: geom.NewRect(0, 0, 100, 10), Span: 1, Offset: 6}},
	}
	ls := textLines(&b)
	if len(ls) != 2 || ls[0].text(&b) != "one two three " || ls[1].text(&b) != "four" {
		t.Errorf("lines %+v", ls)
	}
	b.Lines[1].Offset = 99
	if ls := textLines(&b); len(ls) != 0 {
		t.Errorf("an offset past its span: %d lines", len(ls))
	}
	bare := doc.Block{Spans: []doc.Span{{Text: "a", Page: 1, Box: geom.NewRect(0, 0, 5, 5)}, {Text: "b"}}}
	if ls := textLines(&bare); len(ls) != 1 || ls[0].text(&bare) != "a" {
		t.Errorf("a block with no lines: %+v", ls)
	}
}

// TestGlyphsFindTheirSpan: a line runs across spans, and a run read on it is written into the
// span it is in.
func TestGlyphsFindTheirSpan(t *testing.T) {
	box := geom.NewRect(72, 708, 300, 720)
	b := doc.Block{Role: doc.RoleParagraph, Box: box,
		Spans: []doc.Span{{Text: "When ", Box: box, Page: 1, MCID: -1}, {Text: "crea" + doc.Unmapped + "ng lists,", Box: box, Page: 1, MCID: -1}},
		Lines: []doc.Line{{Box: box, Page: 1, Size: 10, Span: 0, Offset: 0}}}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Blocks: []doc.Block{b}}}}
	o := &ocrRun{eng: &fakeEngine{words: onLine(72, 96, "When", "creating", "lists,")}, r: &fakeRasterizer{pages: 1}}
	if _, _, err := o.glyphs(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if got := d.Pages[0].Blocks[0].Spans; got[0].Text != "When " || got[1].Text != "creating lists," {
		t.Errorf("spans %q, %q", got[0].Text, got[1].Text)
	}
}

// TestGlyphsKeepAReaderThatRefuses: a second reader that refuses a glyph alone leaves it [err]
// and is asked again; one that fails is dropped.
func TestGlyphsKeepAReaderThatRefuses(t *testing.T) {
	lone := doc.Block{Role: doc.RoleParagraph, Spans: []doc.Span{{Text: doc.Unmapped + " mutool usage", Box: geom.NewRect(72, 708, 300, 720), Page: 1, MCID: -1}}}
	for _, c := range []struct {
		name string
		err  error
		kept bool
	}{
		{"refused", ocr.ErrRefused, true},
		{"failed", errors.New("connection reset"), false},
	} {
		d := &doc.Document{Pages: []doc.Page{{Number: 1, Box: geom.NewRect(0, 0, 612, 792), Blocks: []doc.Block{lone}}}}
		o := &ocrRun{eng: &fakeEngine{words: onLine(72, 99, "$", "mutool", "usage")}, r: &fakeRasterizer{pages: 1}, second: &secondReader{err: c.err}}
		_ = captureStderr(t, func() {
			if _, _, err := o.glyphs(context.Background(), d); err != nil {
				t.Fatal(err)
			}
		})
		if got := d.Pages[0].Blocks[0].Spans[0].Text; !strings.HasPrefix(got, doc.Unmapped) || (o.second != nil) != c.kept {
			t.Errorf("%s: %q, reader kept %v", c.name, got, o.second != nil)
		}
	}
}

// TestWantsOCRForAGlyphWithNoValue: a document with no page to route and no image still has its
// glyphs with no value to read.
func TestWantsOCRForAGlyphWithNoValue(t *testing.T) {
	d, _ := twoLines()
	if !wantsOCR(d, nil) {
		t.Error("a page with glyphs with no value wants no OCR")
	}
	d.Pages[0].Blocks[0].Spans[0].Text = "plain"
	if wantsOCR(d, nil) {
		t.Error("a page of plain text wants OCR")
	}
	if !wantsOCR(d, []int{1}) {
		t.Error("a routed page wants no OCR")
	}
}

package ocr

import (
	"image"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// A 1000x2000-pixel image of a 500x1000-point page: half a point per pixel, and y flipped.
var (
	pageBox = geom.NewRect(0, 0, 500, 1000)
	imgBox  = image.Rect(0, 0, 1000, 2000)
)

func w(text string, x0, y0, x1, y1 int, conf float64, block, par, line int) Word {
	return Word{Text: text, Box: image.Rect(x0, y0, x1, y1), Conf: conf, Block: block, Par: par, Line: line}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestPageBuildsTheExtractorsShape: a paragraph per block and paragraph, a span and a line per
// line joined by the wrap space, in points with y up, and a word below the threshold unread.
func TestPageBuildsTheExtractorsShape(t *testing.T) {
	ws := []Word{
		w("One", 100, 100, 160, 140, 96, 1, 1, 1),
		w("two", 180, 100, 240, 140, 20, 1, 1, 1),
		w("three", 100, 160, 200, 200, 95, 1, 1, 2),
		w("Next", 100, 400, 180, 440, 93, 1, 2, 1),
		w("Other", 100, 800, 200, 840, 92, 2, 1, 1),
	}
	p := Page(ws, 3, pageBox, imgBox, 30)
	if p.Number != 3 || !p.Rasterized || p.Box != pageBox {
		t.Errorf("page = number %d rasterized %v box %+v", p.Number, p.Rasterized, p.Box)
	}
	if len(p.Blocks) != 3 {
		t.Fatalf("%d blocks, want 3: one per block and paragraph", len(p.Blocks))
	}
	b := p.Blocks[0]
	if got, want := b.Text(), "One "+doc.Unmapped+" three"; got != want {
		t.Errorf("block 0 = %q, want %q", got, want)
	}
	if b.Role != doc.RoleParagraph || len(b.Spans) != 2 || len(b.Lines) != 2 {
		t.Fatalf("block 0: role %s, %d spans, %d lines", b.Role, len(b.Spans), len(b.Lines))
	}
	// Pixel rows 100-140 are 50-70 points from the top, so 930-950 from the bottom.
	want := geom.NewRect(50, 930, 120, 950)
	if got := b.Lines[0].Box; !near(got.X0, want.X0) || !near(got.Y0, want.Y0) || !near(got.X1, want.X1) || !near(got.Y1, want.Y1) {
		t.Errorf("line 0 box = %+v, want %+v", got, want)
	}
	l1 := b.Lines[1]
	if l1.Span != 1 || l1.Offset != 0 || l1.Page != 3 || !near(l1.Size, 20) {
		t.Errorf("line 1 = %+v", l1)
	}
	// The gap between "One" and "two" is 20 pixels, 10 points; a line of one word has a
	// quarter of its size.
	if !near(b.Lines[0].Space, 10) || !near(l1.Space, 5) {
		t.Errorf("spaces = %g and %g, want 10 and 5", b.Lines[0].Space, l1.Space)
	}
	if sp := b.Spans[1]; sp.Page != 3 || sp.MCID != -1 || !near(sp.Style.Size, 20) {
		t.Errorf("span 1 = %+v", sp)
	}
	if !near(b.Box.Y0, 900) || !near(b.Box.Y1, 950) {
		t.Errorf("block 0 box = %+v, want y 900-950", b.Box)
	}
	if p.Blocks[1].Text() != "Next" || p.Blocks[2].Text() != "Other" {
		t.Errorf("blocks 1 and 2 = %q, %q", p.Blocks[1].Text(), p.Blocks[2].Text())
	}
}

// TestPageJoinsAHyphenAsTheExtractorDoes: a line ending in a hyphen gets no wrap space, so
// layout.Hyphens sees the word broken as it sees the extractor's.
func TestPageJoinsAHyphenAsTheExtractorDoes(t *testing.T) {
	p := Page([]Word{w("hyph-", 0, 0, 50, 20, 95, 1, 1, 1), w("enated", 0, 30, 60, 50, 95, 1, 1, 2)},
		1, pageBox, imgBox, 30)
	if got := p.Blocks[0].Text(); got != "hyph-enated" {
		t.Errorf("text = %q, want the extractor's join", got)
	}
}

// TestPageThresholdIsInclusive: a word at exactly the threshold is read.
func TestPageThresholdIsInclusive(t *testing.T) {
	p := Page([]Word{w("at", 0, 0, 10, 10, 30, 1, 1, 1), w("under", 20, 0, 30, 10, 29.99, 1, 1, 1)},
		1, pageBox, imgBox, 30)
	if got, want := p.Blocks[0].Text(), "at "+doc.Unmapped; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestPageOfAnEmptyImage(t *testing.T) {
	p := Page([]Word{w("x", 0, 0, 1, 1, 99, 1, 1, 1)}, 1, pageBox, image.Rectangle{}, 30)
	if len(p.Blocks) != 0 || !p.Rasterized {
		t.Errorf("a zero-size image gave %d blocks", len(p.Blocks))
	}
}

func page(texts ...string) doc.Page {
	var p doc.Page
	for _, s := range texts {
		p.Blocks = append(p.Blocks, doc.Block{Spans: []doc.Span{{Text: s}}})
	}
	return p
}

// TestAddsNeedsTenTimesTheWords: an OCR reading replaces a page only with addsFactor times the
// words the extractor read, so a sparse born-digital page, or a figure whose labels OCR reads,
// keeps its own text.
func TestAddsNeedsTenTimesTheWords(t *testing.T) {
	u := doc.Unmapped
	n := func(k int) string { return strings.TrimSpace(strings.Repeat("w ", k)) }
	for _, c := range []struct {
		name       string
		orig, read doc.Page
		want       bool
	}{
		{"a scan", page(), page("words read"), true},
		{"a stamped header", page("Bates 001"), page(n(2 * addsFactor)), true},
		{"ten times exactly", page("one two"), page(n(2 * addsFactor)), true},
		{"just under", page("one two"), page(n(2*addsFactor - 1)), false},
		{"a figure's labels", page(n(36)), page(n(204)), false},
		{"the same words", page("a sparse digital page"), page("a sparse digital page"), false},
		{"unmapped glyphs", page(u + u + " " + u), page("read"), true},
		{"nothing read", page(), page(u + " " + u), false},
		{"an empty reading", page(), page(), false},
	} {
		if got := Adds(c.orig, c.read); got != c.want {
			t.Errorf("%s: Adds = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMeanConfWeighsByLength: a page is judged by its words, not by its one-character
// fragments, which a dense grid splits into by the hundred.
func TestMeanConfWeighsByLength(t *testing.T) {
	ws := []Word{{Text: "Association", Conf: 90}, {Text: "x", Conf: 0}}
	if got, want := MeanConf(ws), 90.0*11/12; !near(got, want) {
		t.Errorf("MeanConf = %g, want %g", got, want)
	}
	if got := MeanConf(nil); got != 0 {
		t.Errorf("MeanConf(nil) = %g", got)
	}
}

// TestPageSpaceIsTheMedianGap: a line's space is the middle of its gaps, not the first.
func TestPageSpaceIsTheMedianGap(t *testing.T) {
	p := Page([]Word{
		w("a", 0, 0, 10, 20, 95, 1, 1, 1), w("b", 40, 0, 50, 20, 95, 1, 1, 1),
		w("c", 60, 0, 70, 20, 95, 1, 1, 1), w("d", 90, 0, 100, 20, 95, 1, 1, 1),
	}, 1, pageBox, imgBox, 30)
	// Gaps of 30, 10 and 20 pixels: the median 20 is 10 points.
	if got := p.Blocks[0].Lines[0].Space; !near(got, 10) {
		t.Errorf("space = %g, want 10", got)
	}
}

// TestIsTextAtEighty: a page read at a mean of 80 is text, and one at 79.9 is noise, as the
// spike's photos were at 32 to 74.
func TestIsTextAtEighty(t *testing.T) {
	for conf, want := range map[float64]bool{80: true, 79.9: false, 95: true, 0: false} {
		if got := IsText([]Word{{Text: "word", Conf: conf}}); got != want {
			t.Errorf("IsText at %g = %v, want %v", conf, got, want)
		}
	}
	if IsText(nil) {
		t.Error("no words read as text")
	}
}

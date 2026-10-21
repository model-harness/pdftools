package ocr

import (
	"image"
	"sort"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/geom"
)

// Page builds page n from the words an engine read in its image, as the extractor would have
// built it from glyphs: a block per paragraph, a span and a doc.Line per line, in page
// coordinates.
//
// box is the page's rectangle in points and img the image's bounds, which span it. A word read
// below minConf, and not Agreed by a second reader, is written doc.Unmapped, which the sink tags
// [err] (ADR 0042): one for the word, since how many characters an engine could not read is not
// something it says.
//
// Lines are joined by extract.WrapSpace, the extractor's own rule for a wrapped line, so a word
// broken at a hyphen reaches layout.Hyphens as the extractor's words do.
func Page(words []Word, n int, box geom.Rect, img image.Rectangle, minConf float64) doc.Page {
	p := doc.Page{Number: n, Box: box, Rasterized: true}
	if img.Dx() <= 0 || img.Dy() <= 0 {
		return p
	}
	sx, sy := box.Width()/float64(img.Dx()), box.Height()/float64(img.Dy())
	rect := func(r image.Rectangle) geom.Rect {
		return geom.NewRect(box.X0+float64(r.Min.X-img.Min.X)*sx, box.Y1-float64(r.Max.Y-img.Min.Y)*sy,
			box.X0+float64(r.Max.X-img.Min.X)*sx, box.Y1-float64(r.Min.Y-img.Min.Y)*sy)
	}

	type key struct{ block, par int }
	var b *doc.Block
	var at key
	for i := 0; i < len(words); {
		j := i + 1
		for j < len(words) && words[j].Block == words[i].Block && words[j].Par == words[i].Par &&
			words[j].Line == words[i].Line {
			j++
		}
		line := words[i:j]
		if k := (key{line[0].Block, line[0].Par}); b == nil || k != at {
			p.Blocks = append(p.Blocks, doc.Block{Role: doc.RoleParagraph})
			b, at = &p.Blocks[len(p.Blocks)-1], k
		}
		var lb geom.Rect
		text := ""
		for k, w := range line {
			lb = lb.Union(rect(w.Box))
			if k > 0 {
				text += " "
			}
			if w.Conf < minConf && !w.Agreed {
				text += doc.Unmapped
			} else {
				text += w.Text
			}
		}
		if len(b.Spans) > 0 && extract.WrapSpace(b.Spans, text) {
			b.Spans[len(b.Spans)-1].Text += " "
		}
		size := lb.Height()
		b.Lines = append(b.Lines, doc.Line{Box: lb, Page: n, Size: size, Space: space(line, sx, size), Span: len(b.Spans)})
		b.Spans = append(b.Spans, doc.Span{Text: text, Style: doc.Style{Size: size}, Box: lb, Page: n, MCID: -1})
		b.Box = b.Box.Union(lb)
		i = j
	}
	return p
}

// space is the line's median gap between words in points, the unit layout measures indents in,
// or a quarter of its size for a line of one word.
func space(line []Word, sx, size float64) float64 {
	var gaps []float64
	for k := 1; k < len(line); k++ {
		if g := float64(line[k].Box.Min.X-line[k-1].Box.Max.X) * sx; g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return size / 4
	}
	sort.Float64s(gaps)
	return gaps[len(gaps)/2]
}

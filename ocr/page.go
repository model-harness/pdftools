package ocr

import (
	"image"
	"math"
	"sort"
	"strings"
	"unicode"

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

	weight := 0.0
	if len(words) >= boldMinWords {
		weight = medianWeight(words, sy)
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
		size := lineSize(line, sy)
		if size <= 0 {
			size = lb.Height()
		}
		style := doc.Style{Size: size, Bold: weight > 0 && medianWeight(line, sy) >= boldStroke*weight}
		b.Lines = append(b.Lines, doc.Line{Box: lb, Page: n, Size: size, Space: space(line, sx, size), Span: len(b.Spans)})
		b.Spans = append(b.Spans, doc.Span{Text: text, Style: style, Box: lb, Page: n, MCID: -1})
		b.Box = b.Box.Union(lb)
		i = j
	}
	snapSizes(&p)
	joinColumns(&p)
	return p
}

// joinColumns joins a paragraph a column break cut: a block that opens a column, flush and at the
// size of the block before it, where that block ends at the foot of the page's text, continues it
// if it starts lower case or the line before ran to its column's edge. The extractor's rule for a
// page of glyphs (extract's columns.continues), read over Tesseract's blocks (ADR 0047).
func joinColumns(p *doc.Page) {
	foot := math.Inf(1)
	for _, b := range p.Blocks {
		for _, l := range b.Lines {
			foot = math.Min(foot, l.Box.Y0)
		}
	}
	for i := 1; i < len(p.Blocks); {
		a, b := &p.Blocks[i-1], &p.Blocks[i]
		if !continues(a, b, foot) {
			i++
			continue
		}
		off := len(a.Spans)
		if extract.WrapSpace(a.Spans, b.Spans[0].Text) {
			a.Spans[len(a.Spans)-1].Text += " "
		}
		a.Spans = append(a.Spans, b.Spans...)
		for _, l := range b.Lines {
			l.Span += off
			a.Lines = append(a.Lines, l)
		}
		a.Box = a.Box.Union(b.Box)
		p.Blocks = append(p.Blocks[:i], p.Blocks[i+1:]...)
	}
}

// continues reports whether b carries on a over a column break.
func continues(a, b *doc.Block, foot float64) bool {
	if len(a.Lines) == 0 || len(b.Lines) == 0 || len(b.Spans) == 0 {
		return false
	}
	last, first := a.Lines[len(a.Lines)-1], b.Lines[0]
	if first.Box.Y1 <= last.Box.Y1 || last.Size != first.Size || last.Box.Y0-foot > last.Box.Height() {
		return false
	}
	left := math.Inf(1)
	for _, l := range b.Lines {
		left = math.Min(left, l.Box.X0)
	}
	if first.Space <= 0 || first.Box.X0-left >= first.Space {
		return false
	}
	for _, r := range b.Spans[0].Text {
		if unicode.IsLower(r) {
			return true
		}
		break
	}
	return a.Box.X1-last.Box.X1 <= last.Space
}

// sizeCluster is how far apart, as a share, two lines' sizes may be and be read as one size.
// A size read off words' heights is good to about half a point: straightened, the gold set's two
// top-level headings read 15.0 and 14.5, and as two sizes they made two levels. Its nearest
// real step is its second-level heading at 12.5 against a body of 10.5, 19% (ADR 0047).
const sizeCluster = 0.06

// snapSizes gives the lines of a page whose sizes are within sizeCluster of each other the
// median of them, so that type set at one size reads as one.
func snapSizes(p *doc.Page) {
	var sizes []float64
	for _, b := range p.Blocks {
		for _, l := range b.Lines {
			sizes = append(sizes, l.Size)
		}
	}
	sort.Float64s(sizes)
	snap := map[float64]float64{}
	for i := 0; i < len(sizes); {
		j := i
		// Chained, each size to the one before it, so that 10, 10.5 and 11 are one size, as on
		// the gold set's two-column scan; and capped at twice the step from the first, so that
		// a page of sizes a step apart each does not become one.
		for j+1 < len(sizes) && sizes[j+1] <= sizes[j]*(1+sizeCluster) && sizes[j+1] <= sizes[i]*(1+2*sizeCluster) {
			j++
		}
		for k := i; k <= j; k++ {
			snap[sizes[k]] = sizes[(i+j)/2]
		}
		i = j + 1
	}
	for bi := range p.Blocks {
		b := &p.Blocks[bi]
		for li := range b.Lines {
			b.Lines[li].Size = snap[b.Lines[li].Size]
		}
		for si := range b.Spans {
			if s, ok := snap[b.Spans[si].Style.Size]; ok {
				b.Spans[si].Style.Size = s
			}
		}
	}
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

// The share of the em a word's box spans, by the letters it holds: x-height alone, an ascender
// or a capital, a descender, or both. Latin Modern's, which the gold set is set in; another face
// differs by a few hundredths, and only a page's sizes against each other are read.
const (
	emX    = 0.43
	emUp   = 0.69
	emDown = 0.62
	emBoth = 0.88
)

const ascenders, descenders = "bdfhklt0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", "gjpqy"

// lineSize is the line's type size in points: the median of its words' heights over the share of
// the em their letters span. A line's box is no measure: on the gold set's scanned headings it
// ran from 7.9 to 13.7 points over body type of one size, as lines happened to hold a descender
// or not. Read this way, its four sizes come out 15.6, 12.7, 11.0 and 10.4, for type set at 17.3,
// 14.4, 12 and 10 (ADR 0047), and to the half point 15.5, 12.5, 11 and 10.5. sy is points per
// pixel; 0 for a line of no letters.
func lineSize(line []Word, sy float64) float64 {
	var ems []float64
	for _, w := range line {
		up, down := strings.ContainsAny(w.Text, ascenders), strings.ContainsAny(w.Text, descenders)
		f := emX
		switch {
		case up && down:
			f = emBoth
		case up:
			f = emUp
		case down:
			f = emDown
		}
		if !strings.ContainsFunc(w.Text, unicode.IsLetter) && !strings.ContainsFunc(w.Text, unicode.IsDigit) {
			continue
		}
		ems = append(ems, float64(w.Box.Dy())*sy/f)
	}
	if len(ems) == 0 {
		return 0
	}
	sort.Float64s(ems)
	// To the half point, which is as close as a word's height measures it: the gold set's two
	// top-level headings read 15.60 and 15.55, and taken as they are made two levels.
	return math.Round(ems[len(ems)/2]*2) / 2
}

// boldStroke is how much heavier than the page's median word a line's median word is drawn to
// read as bold, weight being a word's stroke over its type size. On the gold set's scans every
// body line weighs the median, and the bold lines 1.5 to 2.5 times it: the lightest is the
// third-level heading set in body size, whose strokes are 3 pixels to the body's 2 (ADR 0047).
// Measured on ink instead, the share of a word's box that is dark, the body reached 1.11 and the
// bold fell to 1.13, too near to part.
const boldStroke = 1.35

// boldMinWords is the fewest words a page needs for its median to be a body's: a page of a
// heading and a line has no body to measure against.
const boldMinWords = 20

// medianWeight is the median of the words' weights, stroke over type size, 0 for none measured.
func medianWeight(ws []Word, sy float64) float64 {
	var v []float64
	for _, w := range ws {
		if size := lineSize([]Word{w}, sy); w.Stroke > 0 && size > 0 {
			v = append(v, w.Stroke/size)
		}
	}
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	return v[len(v)/2]
}

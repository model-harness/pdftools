package pdfspec

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/render"
)

// glyphDPI is the resolution a page is rendered at to read its glyphs with no value. A glyph with
// no value is as often a superscript or a symbol as a letter of body text: at 200 dpi,
// 2201.00069.pdf's offsets and coordinates read 53 glyphs to 300's 106, for sampleInvoice.pdf's
// body 1441 to 1403 (ADR 0048).
const glyphDPI = 300

// glyphs reads again, off the rendered page, the glyphs the file gives no value to on the pages
// that kept the extractor's text, a line at a time from one reading of the page, and writes what
// it reads where Tesseract is sure of it, or where the second reader agrees with it (ADR 0048).
// It returns how many glyphs there were and how many it read.
func (o *ocrRun) glyphs(ctx context.Context, d *doc.Document) (total, read int, err error) {
	for pi := range d.Pages {
		p := &d.Pages[pi]
		if p.Rasterized || !strings.Contains(p.Text(), doc.Unmapped) {
			continue
		}
		total += strings.Count(p.Text(), doc.Unmapped)
		ra, err := o.r.Page(p.Number, render.Options{DPI: glyphDPI, MaxPixels: o.ropt.MaxPixels})
		if err != nil {
			fmt.Fprintf(o.log, "  page %d: %v; its glyphs with no value stay [err]\n", p.Number, err)
			continue
		}
		// One reading of the page, which each line takes its words from: a reading a line would
		// cost a process a line, and a page set wholly in such a font is forty of them.
		words, err := o.eng.Read(ctx, ra.Image)
		if err != nil {
			return total, read, err
		}
		for bi := range p.Blocks {
			b := &p.Blocks[bi]
			var reps []replacement
			for _, ln := range textLines(b) {
				got, r, err := o.line(ctx, ra, words, b, ln)
				if err != nil {
					return total, read, err
				}
				for _, g := range got {
					read += g.Glyphs
				}
				p.Recovered = append(p.Recovered, got...)
				reps = append(reps, r...)
			}
			replace(b, reps)
		}
	}
	return total, read, nil
}

// textLine is one line of a block's text: its box, and the pieces of the spans it is made of, a
// span running on across lines whenever its style does.
type textLine struct {
	box    geom.Rect
	pieces []piece
}

// piece is a byte range of one span's text.
type piece struct{ span, from, to int }

// text is the line's text, its pieces' run together.
func (ln textLine) text(b *doc.Block) string {
	var sb strings.Builder
	for _, pc := range ln.pieces {
		sb.WriteString(b.Spans[pc.span].Text[pc.from:pc.to])
	}
	return sb.String()
}

// textLines splits a block's text into its lines, or, for a block with none, into its spans.
// A line whose place in the text does not hold is left out.
func textLines(b *doc.Block) []textLine {
	var out []textLine
	if len(b.Lines) == 0 {
		for i, s := range b.Spans {
			if s.Page != 0 && !s.Box.IsZero() {
				out = append(out, textLine{box: s.Box, pieces: []piece{{i, 0, len(s.Text)}}})
			}
		}
		return out
	}
	for k, l := range b.Lines {
		if l.Box.IsZero() || l.Span < 0 || l.Span >= len(b.Spans) {
			continue
		}
		end, endAt := len(b.Spans)-1, len(b.Spans[len(b.Spans)-1].Text)
		if k+1 < len(b.Lines) {
			end, endAt = b.Lines[k+1].Span, b.Lines[k+1].Offset
		}
		ln := textLine{box: l.Box}
		ok := true
		for s := l.Span; s <= end && s < len(b.Spans); s++ {
			from, to := 0, len(b.Spans[s].Text)
			if s == l.Span {
				from = l.Offset
			}
			if s == end {
				to = endAt
			}
			if from < 0 || to > len(b.Spans[s].Text) || from > to {
				ok = false
				break
			}
			if to > from {
				ln.pieces = append(ln.pieces, piece{s, from, to})
			}
		}
		if ok {
			out = append(out, ln)
		}
	}
	return out
}

// replacement is a reading of a run of glyphs with no value, at a byte range of a span's text.
type replacement struct {
	span, from, to int
	text           string
}

// line reads one line's glyphs with no value from the words of the page's reading that stand on
// it, and returns the readings taken, as recoveries and as replacements in its spans' text.
func (o *ocrRun) line(ctx context.Context, ra *render.Raster, page []ocr.Word, b *doc.Block, ln textLine) ([]doc.Recovery, []replacement, error) {
	text := ln.text(b)
	if !strings.Contains(text, doc.Unmapped) {
		return nil, nil, nil
	}
	words := lineWords(page, pixels(ra, ln.box))
	fills := ocr.Fills(text, words)
	if len(fills) == 0 {
		return nil, nil, nil
	}
	o.confirmFills(ctx, ra.Image, words, fills)
	var got []doc.Recovery
	var reps []replacement
	for _, f := range fills {
		conf, agreed := 100.0, true
		for _, wi := range f.Words {
			conf = math.Min(conf, words[wi].Conf)
			agreed = agreed && words[wi].Agreed
		}
		engine := o.eng.Name()
		switch {
		case conf >= ocr.SureConf && !f.Alone:
		case agreed && o.second != nil:
			engine = o.second.Name()
		default:
			continue
		}
		// The run lies in one piece: a run of doc.Unmapped is one span's, since a span ends
		// where its font does.
		at := 0
		for _, pc := range ln.pieces {
			n := pc.to - pc.from
			if f.At[0] >= at && f.At[1] <= at+n {
				reps = append(reps, replacement{span: pc.span, from: pc.from + f.At[0] - at, to: pc.from + f.At[1] - at, text: f.Text})
				got = append(got, doc.Recovery{Box: ln.box, Glyphs: utf8.RuneCountInString(text[f.At[0]:f.At[1]]),
					Text: f.Text, Engine: engine, Conf: conf})
				break
			}
			at += n
		}
	}
	return got, reps, nil
}

// confirmFills asks the second reader, if there is one, about the words fills were read from
// that Tesseract is unsure of, and about every glyph read alone, and marks those it agrees with:
// a word by its letters (ocr.Confirm), a glyph alone character for character (ocr.SameGlyph).
func (o *ocrRun) confirmFills(ctx context.Context, img image.Image, words []ocr.Word, fills []ocr.Fill) {
	if o.second == nil {
		return
	}
	fail := func(err error) {
		fmt.Fprintf(o.log, "  %s failed, and glyphs are read without it: %v\n", o.second.Name(), err)
		o.second = nil
	}
	var doubt []ocr.Word
	var at []int
	for _, f := range fills {
		if f.Alone {
			// One word: a glyph is at least a character, so no two words read as one.
			wi := f.Words[0]
			got, err := ocr.ReadAgain(ctx, img, words[wi], o.second)
			if errors.Is(err, ocr.ErrRefused) {
				continue
			}
			if err != nil {
				fail(err)
				return
			}
			words[wi].Agreed = ocr.SameGlyph(got, words[wi].Text)
			continue
		}
		for _, wi := range f.Words {
			if words[wi].Conf < ocr.SureConf {
				doubt = append(doubt, words[wi])
				at = append(at, wi)
			}
		}
	}
	if len(doubt) == 0 {
		return
	}
	confirmed, _, err := ocr.Confirm(ctx, img, doubt, o.second)
	if err != nil {
		fail(err)
		return
	}
	for k, w := range confirmed {
		words[at[k]].Agreed = w.Agreed
	}
}

// replace writes the readings into the block's spans, last first, so that each byte range is
// still where it was read, and moves the start of every later line in a span by what the
// reading changed its length by.
func replace(b *doc.Block, reps []replacement) {
	sort.Slice(reps, func(i, j int) bool {
		if reps[i].span != reps[j].span {
			return reps[i].span > reps[j].span
		}
		return reps[i].from > reps[j].from
	})
	for _, r := range reps {
		s := &b.Spans[r.span]
		s.Text = s.Text[:r.from] + r.text + s.Text[r.to:]
		for li := range b.Lines {
			if b.Lines[li].Span == r.span && b.Lines[li].Offset >= r.to {
				b.Lines[li].Offset += len(r.text) - (r.to - r.from)
			}
		}
	}
}

// pixels is r, a rectangle in page coordinates, in the pixels of the page image.
func pixels(ra *render.Raster, r geom.Rect) image.Rectangle {
	b := ra.Image.Bounds()
	sx, sy := float64(b.Dx())/ra.Box.Width(), float64(b.Dy())/ra.Box.Height()
	return image.Rect(int((r.X0-ra.Box.X0)*sx), int((ra.Box.Y1-r.Y1)*sy),
		int(math.Ceil((r.X1-ra.Box.X0)*sx)), int(math.Ceil((ra.Box.Y1-r.Y0)*sy))).Add(b.Min)
}

// lineWords are the words of the page's reading that stand on the line at px, left to right:
// those whose centre is inside it, its height of slack to each side, for a glyph that overhangs
// the line's box.
func lineWords(page []ocr.Word, px image.Rectangle) []ocr.Word {
	h := px.Dy()
	in := image.Rect(px.Min.X-h, px.Min.Y, px.Max.X+h, px.Max.Y)
	var out []ocr.Word
	for _, w := range page {
		if c := image.Pt((w.Box.Min.X+w.Box.Max.X)/2, (w.Box.Min.Y+w.Box.Max.Y)/2); c.In(in) {
			out = append(out, w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Box.Min.X < out[j].Box.Min.X })
	return out
}

// recoveredPages lists, page by page, the glyphs read and by what: "p1 40 by tesseract 5.5.3
// eng at 91-97".
func recoveredPages(d *doc.Document) []string {
	var out []string
	for _, p := range d.Pages {
		by := map[string][]float64{}
		n := map[string]int{}
		for _, r := range p.Recovered {
			by[r.Engine] = append(by[r.Engine], r.Conf)
			n[r.Engine] += r.Glyphs
		}
		Engines := make([]string, 0, len(by))
		for e := range by {
			Engines = append(Engines, e)
		}
		sort.Strings(Engines)
		for _, e := range Engines {
			cs := by[e]
			sort.Float64s(cs)
			out = append(out, fmt.Sprintf("p%d %d by %s at %.0f-%.0f", p.Number, n[e], e, cs[0], cs[len(cs)-1]))
		}
	}
	return out
}

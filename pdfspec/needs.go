package pdfspec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/layout"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/ocr/gemma"
	"github.com/model-harness/pdftools/ocr/tesseract"
)

// pageCount is one page's count of something.
type pageCount struct{ Page, N int }

// perPage lists counts by page as "p3 12, p7 4".
func perPage(pcs []pageCount) string {
	parts := make([]string, len(pcs))
	for i, pc := range pcs {
		parts[i] = "p" + strconv.Itoa(pc.Page) + " " + strconv.Itoa(pc.N)
	}
	return strings.Join(parts, ", ")
}

// imageLines are the image lines the sink is given: the outline's, where the file is written
// as one, and otherwise the pages' own.
func imageLines(d *doc.Document, o *doc.Outline) []doc.Block {
	var out []doc.Block
	add := func(bs []doc.Block) {
		for _, b := range bs {
			// An image line is the block that carries its image: layout.ImageBlocks and
			// sectionize make no other, and no pass gives one another role.
			if b.Image != nil {
				out = append(out, b)
			}
		}
	}
	pages := d.Pages
	if o != nil {
		add(o.Preamble)
		o.Walk(func(s *doc.Section) bool {
			add(s.Blocks)
			return true
		})
		pages = o.Unplaced
	}
	for _, p := range pages {
		add(p.Blocks)
	}
	return out
}

// bareImages counts, page by page, the image lines written "[image]": nothing was read off the
// image, and the file describes it nowhere (ADRs 0045, 0046). A line's image is one of the
// document's own, which says its page.
func bareImages(d *doc.Document, lines []doc.Block) []pageCount {
	page := map[*doc.Image]int{}
	for pi := range d.Pages {
		for ii := range d.Pages[pi].Images {
			page[&d.Pages[pi].Images[ii]] = d.Pages[pi].Number
		}
	}
	n := map[int]int{}
	for _, b := range lines {
		if len(strings.Fields(b.Alt)) == 0 {
			n[page[b.Image]]++
		}
	}
	var out []pageCount
	for _, p := range d.Pages {
		if n[p.Number] > 0 {
			out = append(out, pageCount{p.Number, n[p.Number]})
		}
	}
	return out
}

// warnIfBareImages reports the images written [image] and the pages they are on, so that a
// reader knows what is missing and where: an image nothing read, which Tesseract or inferd may
// read where they are installed.
func warnIfBareImages(w io.Writer, pages []pageCount) {
	if len(pages) == 0 {
		return
	}
	total := 0
	for _, p := range pages {
		total += p.N
	}
	fmt.Fprintf(w, "note: %d image(s) on %d page(s) are written [image], with nothing read off them\n", total, len(pages))
	fmt.Fprintf(w, "  by page: %s\n", perPage(pages))
}

// PageNeed is what an engine would read on one page: the page whole, which the router sends to
// OCR, its images worth reading, and its glyphs with no value.
type PageNeed struct {
	Page     int  `json:"page"`
	Whole    bool `json:"read_whole,omitempty"`
	Images   int  `json:"images,omitempty"`
	Unmapped int  `json:"unmapped,omitempty"`
}

// pageNeeds is what md would ask an engine to read in d, page by page, by the rules it reads by:
// ocr.Route for a page, layout.Keep and the file's own descriptions for an image (ADR 0046).
func pageNeeds(d *doc.Document, described func(page int, im doc.Image) bool) []PageNeed {
	keep := layout.Keep(d)
	var out []PageNeed
	for pi, p := range d.Pages {
		need := PageNeed{Page: p.Number, Whole: ocr.Route(p, ocr.DefaultThreshold), Unmapped: strings.Count(p.Text(), doc.Unmapped)}
		for ii, im := range p.Images {
			// A page read whole is read with its images: the reading's text lies over them, and
			// what lies under text is not read again (layout.Keep).
			if keep[pi][ii] && !need.Whole && (described == nil || !described(p.Number, im)) {
				need.Images++
			}
		}
		if need.Whole || need.Images > 0 || need.Unmapped > 0 {
			out = append(out, need)
		}
	}
	return out
}

// NeedsLine is probe's line for what the pages need: "read whole: 1-3; images: p5 2; glyphs
// with no value: p7 12".
func NeedsLine(needs []PageNeed) string {
	var whole []int
	var images, glyphs []pageCount
	for _, n := range needs {
		if n.Whole {
			whole = append(whole, n.Page)
		}
		if n.Images > 0 {
			images = append(images, pageCount{n.Page, n.Images})
		}
		if n.Unmapped > 0 {
			glyphs = append(glyphs, pageCount{n.Page, n.Unmapped})
		}
	}
	var parts []string
	if len(whole) > 0 {
		parts = append(parts, "read whole: "+pageList(whole))
	}
	if len(images) > 0 {
		parts = append(parts, "images: "+perPage(images))
	}
	if len(glyphs) > 0 {
		parts = append(parts, "glyphs with no value: "+perPage(glyphs))
	}
	if len(parts) == 0 {
		return "nothing for an engine to read"
	}
	return strings.Join(parts, "; ")
}

// Engines names the readers md would use, or why there is none.
type Engines struct {
	Tesseract string `json:"tesseract"`
	Inferd    string `json:"inferd"`
}

// String is probe's line: each engine by its name, or what it is with what stops it, "tesseract
// 5.5.3 eng; inferd off".
func (e Engines) String() string {
	t, i := e.Tesseract, e.Inferd
	if !strings.HasPrefix(t, "tesseract ") {
		t = "tesseract " + t
	}
	if !strings.HasSuffix(i, " via inferd") {
		i = "inferd " + i
	}
	return t + "; " + i
}

// FindEngines looks for the engines as Read does, by Options' Tesseract and Inferd: exe is the
// tesseract executable, "" for the one on PATH, or "off"; addr is inferd's generation endpoint,
// "" for the platform's, or "off".
func FindEngines(ctx context.Context, exe, addr string) Engines {
	var e Engines
	switch {
	case exe == "off":
		e.Tesseract = "off"
	default:
		t, err := tesseract.New(ctx, tesseract.Options{Exe: exe, DPI: ocrDPI})
		switch {
		case errors.Is(err, tesseract.ErrNotFound):
			e.Tesseract = "not found"
		case err != nil:
			e.Tesseract = err.Error()
		default:
			e.Tesseract = t.Name()
		}
	}
	switch addr {
	case "off":
		e.Inferd = "off"
	default:
		m, err := gemma.Open(ctx, addr, "", 0)
		if err != nil {
			e.Inferd = "unavailable: " + err.Error()
		} else {
			e.Inferd = m.Name()
		}
	}
	return e
}

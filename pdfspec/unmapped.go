package pdfspec

import (
	"fmt"
	"io"
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// PageErrs is one page's count of the glyphs the file gives no Unicode value to.
type PageErrs struct {
	Page   int `json:"page"`
	Glyphs int `json:"glyphs"`
}

// Unmapped counts, page by page, the glyphs the file gives no Unicode value to, each of which the
// Markdown writes as part of an [err] (ADR 0042). A page with none is left out, and so is a page
// read by OCR, whose [err] are words read below the confidence threshold and reported with it.
func Unmapped(d *doc.Document) []PageErrs {
	var out []PageErrs
	for i := range d.Pages {
		if d.Pages[i].Rasterized {
			continue
		}
		if n := strings.Count(d.Pages[i].Text(), doc.Unmapped); n > 0 {
			out = append(out, PageErrs{Page: d.Pages[i].Number, Glyphs: n})
		}
	}
	return out
}

// ErrTotal is the number of glyphs across pages.
func ErrTotal(pages []PageErrs) int {
	n := 0
	for _, p := range pages {
		n += p.Glyphs
	}
	return n
}

// ErrPages lists the pages as "p3 12, p7 4".
func ErrPages(pages []PageErrs) string {
	pcs := make([]pageCount, len(pages))
	for i, p := range pages {
		pcs[i] = pageCount{p.Page, p.Glyphs}
	}
	return perPage(pcs)
}

// warnIfUnmapped reports the glyphs written [err] and the pages they are on. The output reads as
// a conversion with a few unknown characters; this says how many and where, so a reader knows
// which pages to look at, or to read with "pdfspec ocr".
func warnIfUnmapped(w io.Writer, pages []PageErrs) {
	if len(pages) == 0 {
		return
	}
	fmt.Fprintf(w, "warning: %d glyph(s) on %d page(s) have no Unicode value in the file and are written [err]\n",
		ErrTotal(pages), len(pages))
	fmt.Fprintf(w, "  by page: %s\n", ErrPages(pages))
}

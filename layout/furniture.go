package layout

import (
	"regexp"
	"strings"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// Furniture marks an untagged document's running headers, running footers and folios as
// artifacts in place, and returns how many blocks it marked.
//
// A tagged producer wraps its furniture in an Artifact, and extract drops it. An untagged
// page says nothing about it, but the document does: furniture is text in the top or bottom
// margin that recurs from page to page, changing only its numbers. So a block lying wholly
// in a margin band is furniture when its text, with each number made one symbol, is found in
// the same band on another page.
//
// Measured against the tagged corpus, whose producers mark their furniture as artifacts
// (ADR 0033): 3538 of 3572 artifact blocks found, and 11 blocks marked that are not. A band of
// 15% of the page admitted 399 more false ones, and making every digit one symbol, rather
// than every number, joined footnotes whose URLs differ only in digits and split folios set
// in roman numerals.
//
// One page is no evidence, so a one-page document keeps everything. A block already an
// artifact, as md -artifacts keeps a tagged producer's, is neither counted nor marked again.
// Runs before the other passes, so a header is never a heading and a folio never a cell.
func Furniture(d *doc.Document) int {
	if d == nil {
		return 0
	}
	type key struct {
		band int
		sig  string
	}
	pages := map[key]map[int]bool{}
	at := func(pi, bi int) (key, bool) {
		p := &d.Pages[pi]
		b := &p.Blocks[bi]
		if b.Role == doc.RoleArtifact || b.IsEmpty() {
			return key{}, false
		}
		band := marginBand(p.Box, b.Box)
		if band == 0 {
			return key{}, false
		}
		return key{band, signature(b.Text())}, true
	}
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			if k, ok := at(pi, bi); ok {
				if pages[k] == nil {
					pages[k] = map[int]bool{}
				}
				pages[k][pi] = true
			}
		}
	}
	marked := 0
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			if k, ok := at(pi, bi); ok && len(pages[k]) >= furniturePages {
				d.Pages[pi].Blocks[bi].Role = doc.RoleArtifact
				marked++
			}
		}
	}
	return marked
}

const (
	// furnitureBand is the share of the page's height that is its top margin band, and its
	// bottom one. Measured with the rule: 10% missed 32 more artifacts, 15% marked 399 more
	// blocks that are not.
	furnitureBand = 0.12
	// furniturePages is how many pages a margin text has to be on.
	furniturePages = 2
)

// marginBand returns 1 for a block wholly in the page's top margin band, -1 for one wholly in
// its bottom band, and 0 otherwise.
func marginBand(page, b geom.Rect) int {
	h := page.Y1 - page.Y0
	switch {
	case h <= 0:
		return 0
	case b.Y0 >= page.Y1-furnitureBand*h:
		return 1
	case b.Y1 <= page.Y0+furnitureBand*h:
		return -1
	}
	return 0
}

// roman is a roman numeral, which front matter is numbered in.
var roman = regexp.MustCompile(`^m{0,4}(cm|cd|d?c{0,3})(xc|xl|l?x{0,3})(ix|iv|v?i{0,3})$`)

// signature is a margin text as furniture recurs: lower case, words split on spaces, and
// every word that is a number, in digits or roman numerals, one symbol. A digit inside a
// word stays, so "rfc5652" and "rfc6211" stay two texts.
func signature(s string) string {
	var out []string
	for _, tok := range strings.Fields(strings.ToLower(s)) {
		if numeral(tok) {
			tok = "#"
		}
		out = append(out, tok)
	}
	return strings.Join(out, " ")
}

func numeral(tok string) bool {
	if tok == "" {
		return false
	}
	digits := true
	for _, r := range tok {
		if r < '0' || r > '9' {
			digits = false
			break
		}
	}
	return digits || roman.MatchString(tok)
}

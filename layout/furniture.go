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
	folios := map[key]map[string]bool{} // each text a key was found as, numbers and all
	extents := make([]geom.Rect, len(d.Pages))
	for pi := range d.Pages {
		extents[pi] = extent(d.Pages[pi].Blocks)
	}
	at := func(pi, bi int) (k key, reached, ok bool) {
		p := &d.Pages[pi]
		b := &p.Blocks[bi]
		if b.Role == doc.RoleArtifact || b.Role == doc.RoleImage || b.IsEmpty() {
			return key{}, false, false
		}
		band, reached := marginBand(p.Box, b.Box, extents[pi])
		if band == 0 {
			return key{}, false, false
		}
		return key{band, signature(b.Text())}, reached, true
	}
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			if k, _, ok := at(pi, bi); ok {
				if pages[k] == nil {
					pages[k], folios[k] = map[int]bool{}, map[string]bool{}
				}
				pages[k][pi] = true
				folios[k][strings.Join(strings.Fields(strings.ToLower(d.Pages[pi].Blocks[bi].Text())), " ")] = true
			}
		}
	}
	marked := 0
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			k, reached, ok := at(pi, bi)
			// Past the band, only a text whose numbers change from page to page: a folio.
			if ok && len(pages[k]) >= furniturePages && (!reached || len(folios[k]) >= 2) {
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
	// furnitureReach is how far into the page a band reaches for the page's outermost block.
	furnitureReach = 0.15
	// furniturePages is how many pages a margin text has to be on.
	furniturePages = 2
)

// marginBand returns 1 for a block in the page's top margin band, -1 for one in its bottom
// band, and 0 otherwise, and whether it is there only by the band's reach. A block is in a band
// when it lies wholly inside it, or when it is the page's outermost block on that side, no
// text beyond it, and lies wholly inside the band's reach. text is the extent of the page's
// text.
//
// The reach is for a page whose margins are wider than the band. mupdf_explored.pdf, set on
// Letter paper, puts its running headers 98 to 108pt below the top, 12.4% to 13.6% of the
// page, and the folios of its chapters' opening pages as far above the bottom, so the band
// alone missed all 278 (ADR 0034). Widening the band instead marks text that recurs near the
// page's edge without being furniture, 399 more blocks in the tagged corpus at 15%.
//
// The reach marks none of those 399, because each has its page's furniture beyond it, but a
// page with no furniture has nothing beyond its first line. Extract drops a tagged producer's,
// and the reach then found the header row that ISO 32000-2 and ISO/TS 32005 repeat at the top
// of each page a table continues on, 294 times. So Furniture asks more of a reached block: its
// numbers change from page to page, as a folio's do and a table header's do not.
func marginBand(page, b, text geom.Rect) (band int, reached bool) {
	h := page.Y1 - page.Y0
	switch {
	case h <= 0:
		return 0, false
	case b.Y0 >= page.Y1-furnitureBand*h:
		return 1, false
	case b.Y1 <= page.Y0+furnitureBand*h:
		return -1, false
	case b.Y1 >= text.Y1 && b.Y0 >= page.Y1-furnitureReach*h:
		return 1, true
	case b.Y0 <= text.Y0 && b.Y1 <= page.Y0+furnitureReach*h:
		return -1, true
	}
	return 0, false
}

// extent is the box of a page's text: every block that has any, furniture included.
func extent(blocks []doc.Block) geom.Rect {
	var r geom.Rect
	for i := range blocks {
		if !blocks[i].IsEmpty() {
			r = r.Union(blocks[i].Box)
		}
	}
	return r
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

// Package ocr recovers text from pages whose content stream does not carry it.
//
// It is the fallback path, and naming it that is the whole design. A scanned page
// has an image and no text; a born-digital page has text and the extractor already
// read it. Reading the second kind from pixels produces a worse answer than the one
// already in hand — the content stream states its glyphs, while OCR infers them — so
// the decision of *which* pages to send is worth more than any engine choice. That
// decision is Route.
//
// # Words, with a confidence
//
// An Engine returns the words it read, each with its box and its confidence. Page builds
// the same doc.Page the extractor produces from them, so every sink downstream is
// unchanged, and a document can be part scanned and part digital without the caller
// knowing which pages were which. doc.Page.Rasterized is the record of that.
//
// The confidence is what the issue (#9) asked for and what DocTags could not give: a
// model's transcription states every word with the same assurance, right or wrong. A word
// below the threshold is written [err] (ADR 0042) instead of a guess.
//
// The interface is this repo's, and no backend's type appears in it: the consuming
// package owns the interface and adapters live in subpackages, as with objects/pdfcpu
// and render/pdfium. ocr/tesseract is the one adapter (ADR 0043).
package ocr

import (
	"context"
	"image"
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// Word is one word an Engine read.
type Word struct {
	// Text is the word as read.
	Text string

	// Box is the word's extent in the image's pixels.
	Box image.Rectangle

	// Conf is the engine's confidence in Text, from 0 to 100.
	Conf float64

	// Block, Par and Line number the word's block, paragraph and line within the image, as
	// the engine segmented it. Words sharing all three are one line, in reading order.
	Block, Par, Line int

	// Agreed reports that a second reader read the word as the engine did (Confirm), which
	// Page takes as the engine's reading confirmed, whatever its confidence.
	Agreed bool
}

// Engine reads the words in one page image.
//
// One page per call: a page is the unit the router decides about, and the unit a failure
// is confined to, so a fifty-page scan with one unreadable page yields forty-nine.
// Implementations need not be safe for concurrent use.
type Engine interface {
	// Read returns the words in img, in reading order.
	Read(ctx context.Context, img image.Image) ([]Word, error)

	// Name identifies the engine and its model, for the page report: "tesseract 5.5.3 eng".
	Name() string
}

// Route reports whether a page should be sent to an Engine.
//
// The rule is coverage: the fraction of the page's crop box covered by the text
// blocks the extractor found. A scanned page has an image spanning the page and no
// text blocks at all, so its coverage is zero; a born-digital page has text spread
// across most of it. The threshold separates them, and the reason it is a coverage
// ratio rather than a character count is the case that motivates the whole router —
// a scanned page carrying a single line of digital text in a header. Counting
// characters says "this page has text, skip it" and loses the entire scan. Coverage
// says a page whose text occupies 2% of it has essentially none.
//
// doc.Page.Coverage was written for this and documents itself as "the numerator of
// the OCR router's coverage rule", including the two defensive cases that matter
// here: a block outside the crop box clamps rather than pushing coverage above 1,
// and a zero-area page box yields 0, so a defective page routes to OCR rather
// than being silently accepted as complete.
//
// A page already marked Rasterized is never routed again — it came from OCR, so
// sending it back would ask the engine to read its own output.
func Route(p doc.Page, threshold float64) bool {
	if p.Rasterized {
		return false
	}
	return Coverage(p) < threshold
}

// Coverage is the page's coverage by the blocks that hold a character the extractor read. A
// block of glyphs the file gives no value to, each written [err] (ADR 0042), covers the page as
// a scan's image does: with text nobody has read. Counted, it would keep the page from the one
// engine that could read it.
func Coverage(p doc.Page) float64 {
	read := make([]doc.Block, 0, len(p.Blocks))
	for _, b := range p.Blocks {
		if readable(b.Text()) {
			read = append(read, b)
		}
	}
	p.Blocks = read
	return p.Coverage()
}

// MeanConf is the words' confidence weighted by their length, so a page is not judged by its
// stray one-character fragments; 0 for no words.
func MeanConf(ws []Word) float64 {
	sum, n := 0.0, 0
	for _, w := range ws {
		k := len([]rune(w.Text))
		sum += w.Conf * float64(k)
		n += k
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// PageMinConf is the mean confidence below which a page's reading is not taken as text.
//
// A photo is read as a few words of noise: the spike's three image-only fixtures gave 1 to 4
// words at a mean of 32 to 74, "eee eee eee eee" among them. Pages of text read at 84.0 to
// 95.7, the lowest a scan-like page of a dense paper (ADR 0043). 80 sits between.
const PageMinConf = 80

// IsText reports whether a page's words read as text: at a mean confidence of PageMinConf or
// more.
func IsText(ws []Word) bool { return MeanConf(ws) >= PageMinConf }

// Adds reports whether read, a page built from OCR, adds to orig, the page the extractor found
// with its artifacts: whether it holds at least addsFactor times orig's words with a character
// read.
//
// The file's own text comes first, and OCR only adds to it. Route sends a page by its
// coverage, and a born-digital page can cover very little: the reference fixtures set a few
// lines on a full page, under the 5% threshold. Read again, such a page gives back the words
// it already had, now inferred from pixels, and replacing it cost every enforced bench
// dimension: tables fell from 0.875 to 0.375 and style from 0.960 to 0.560 (ADR 0043).
func Adds(orig, read doc.Page) bool {
	return words(read) > 0 && words(read) >= addsFactor*words(orig)
}

// addsFactor is how many times the extracted words a reading must hold to replace a page.
//
// Over the corpus's 209 routed pages, a born-digital page read back at most 1.3 times its
// words, except where its figure is a raster with labels in it: ISO 32000-2's figure pages
// read at 3.0 to 5.7 times, the labels taken for new text, and a tagged figure page replaced
// lost its caption from the outline. A scan with a stamped header reads at 50 times or more.
// 10 sits between.
const addsFactor = 10

// words counts the page's words that hold a character read.
func words(p doc.Page) int {
	n := 0
	for _, w := range strings.Fields(p.Text()) {
		if readable(w) {
			n++
		}
	}
	return n
}

// readable reports whether s holds a character other than whitespace and doc.Unmapped.
func readable(s string) bool {
	return strings.TrimSpace(strings.ReplaceAll(s, doc.Unmapped, "")) != ""
}

// DefaultThreshold is the coverage below which a page is sent to OCR.
//
// 5% rather than 0. A pure scan is exactly zero and any positive threshold catches
// it, so the number is really about the mixed page: a scan whose only digital text
// is a stamped page number, a header, or a Bates number occupies a percent or two,
// and at a threshold of zero every one of those pages silently yields one line
// instead of the page. Set well below what a page of real prose reaches — a
// single-column page of body text covers a third of its box or more — so the two
// populations are not close to each other and the exact value is not load-bearing.
const DefaultThreshold = 0.05

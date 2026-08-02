// Package doc is the domain model every other package converges on: what a
// document looks like after it has been read and before it has been written out.
//
// Nothing here parses, positions, classifies, or renders. That is deliberate and
// it is the reason the package exists. The extractor, the layout heuristics, the
// OCR engine, and the structure-tree walker all produce this model, and the
// Markdown and OKF sinks all consume it — so a page recovered from glyph
// positions and a page recovered from a vision model are interchangeable by the
// time a sink sees them. Without a shared model in the middle, each producer and
// each sink would need to know about the others, which is how the libraries in
// §1 of docs/DESIGN.md ended up as one package that does everything.
//
// The only dependency is geom, which is itself stdlib-only. geom owns Rect,
// Matrix, and the tolerance policy; re-declaring them here would give the repo
// two rectangles that must be kept in agreement, so the layout in DESIGN.md §4
// listing them under doc is satisfied by using geom's.
//
// Section and the OKF-facing types are not here yet. Their shape depends on what
// sectionize actually needs to stitch a clause across pages, and inventing it
// before that measurement is how a model acquires fields nothing sets.
package doc

import (
	"strings"

	"github.com/3rg0n/pdf-spec/geom"
)

// Document is one PDF, read.
type Document struct {
	// Meta is what the file says about itself, for frontmatter and for OKF
	// provenance.
	Meta Metadata

	// Pages are in document order, and every page the file declares is present
	// even when nothing could be extracted from it. A missing page would shift
	// every page number after it, and page numbers are what a reader checks a
	// conversion against.
	Pages []Page
}

// Metadata is the document-level information a sink can put in frontmatter.
//
// Every field is optional, because every one of them is optional in the file.
// The zero value is a document that said nothing about itself, which is common
// and is not an error.
type Metadata struct {
	// Path is the source file as given on the command line. Kept because a
	// converted document with no record of where it came from cannot be
	// regenerated or checked.
	Path string

	// Title, Author, Subject, Keywords, Creator, and Producer come from the
	// document information dictionary (ISO 32000-2 §14.3.3) or from XMP.
	Title    string
	Author   string
	Subject  string
	Keywords string
	Creator  string
	Producer string

	// Created and Modified are as written in the file, not parsed into a time.
	// PDF date strings are frequently malformed, and a sink that emits the string
	// it found is more useful than one that drops a date it could not parse.
	Created  string
	Modified string

	// Lang is the document's /Lang, which OKF and Markdown frontmatter both want
	// and which is also the only reliable hint for hyphenation and casing rules.
	Lang string

	// Version is the PDF version, as reported by objects.Store.
	Version string

	// Tagged reports whether the file has a structure tree. It decides which
	// extraction path ran, so it belongs in the output: a conversion that silently
	// fell back to layout heuristics should be legible as such.
	Tagged bool

	// Encrypted reports an /Encrypt dictionary. A file may be readable and still
	// report true, since empty-password encryption is common.
	Encrypted bool
}

// Page is one page's content in reading order.
type Page struct {
	// Number is the 1-based page number.
	Number int

	// Box is the page's visible area in user space — the crop box where one is
	// present, otherwise the media box. Blocks are positioned in the same space.
	Box geom.Rect

	// Rotate is /Rotate in degrees, a multiple of 90. Carried rather than applied:
	// a sink emitting text does not need it, and a rasterizer does, so folding it
	// into coordinates here would force the sink to undo it.
	Rotate int

	// Blocks are the page's content in reading order. Order is the producer's
	// responsibility — the structure tree's logical order when tagged, geometry
	// when not — because it is the one thing a sink cannot recover.
	Blocks []Block

	// Rasterized reports that this page's content came from OCR rather than from
	// the content stream. It travels with the page so a sink can mark inferred
	// text as inferred, which matters for a knowledge bundle a model will later
	// read as fact.
	Rasterized bool
}

// Text returns the page's text with one newline between blocks.
//
// This is for measurement, not for output: the §9 benchmark counts characters,
// space ratio, and word lengths, and those numbers must come from one agreed
// rendering or they are not comparable across runs. Markdown formatting —
// heading markers, list bullets, paragraph spacing — is the sink's job, and a
// sink that called this would be reformatting a string instead of walking the
// model.
func (p Page) Text() string {
	var b strings.Builder
	for i := range p.Blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		p.Blocks[i].writeText(&b)
	}
	return b.String()
}

// TextBounds returns the union of every block's rectangle, or the zero Rect when
// the page has no text.
//
// This is the numerator of the OCR router's coverage rule: a page whose text
// covers too little of its box is a scan with no text layer, or one with a text
// layer so sparse that rasterizing it will do better. Expressed as a bounding
// union rather than a sum of areas because overlapping blocks would double-count
// and push a scanned page's coverage above the threshold.
func (p Page) TextBounds() geom.Rect {
	var out geom.Rect
	for i := range p.Blocks {
		out = out.Union(p.Blocks[i].Box)
	}
	return out
}

// Coverage returns the fraction of the page box its text occupies, from 0 to 1.
//
// Zero for a page with no text or no box. A zero-area box is a defective page
// dictionary rather than a full page, so reporting no coverage routes it to the
// rasterizer, which is the outcome that recovers something.
func (p Page) Coverage() float64 {
	area := p.Box.Area()
	if area <= 0 {
		return 0
	}
	c := p.TextBounds().Area() / area
	if c > 1 {
		// A block positioned outside the crop box — a producer bug, and common
		// enough in generated documents to be worth clamping rather than emitting a
		// coverage above 1 that every threshold comparison would then pass.
		return 1
	}
	return c
}

// Text returns the whole document's text, pages joined by a blank line.
//
// The same measurement rendering as Page.Text, for the same reason.
func (d *Document) Text() string {
	var b strings.Builder
	for i := range d.Pages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(d.Pages[i].Text())
	}
	return b.String()
}

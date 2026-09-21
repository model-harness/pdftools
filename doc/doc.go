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
// Section is in section.go, and its shape came from measuring ISO 32000-2 rather
// than from anticipating it — see that file for what the measurement changed.
package doc

import (
	"strings"

	"github.com/model-harness/pdftools/geom"
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

	// Rules are the page's axis-aligned straight strokes and fills, in the order
	// drawn. They are the only evidence an untagged ruled table leaves behind, which
	// is why they are carried at all: measured over every inferred space on disk, the
	// gap that separates two cells and the gap that separates two words occupy one
	// continuous distribution from 0.25 to 1300 space widths with no empty band
	// anywhere, so no threshold on the gap alone can tell a column boundary from wide
	// word spacing. A rule between two glyphs can, and it is the producer's own
	// statement rather than a statistic about it.
	//
	// On the page rather than on a block because a rule belongs to no block: one rule
	// bounds the cells on both sides of it, so attributing it to either is a choice
	// the producer never made. Marked content cannot decide it either. Of ISO
	// 32000-2's 72,002 painting operators, 71,194 sit inside an /Artifact with no MCID
	// and 15 outside any marked content at all — a producer usually declares a rule
	// not to be content — but 793 sit inside a Span, a P or a Figure carrying a real
	// identifier, which is how page 205 paints the fraction bars extract reads. Nor
	// are they painted ahead of the text they enclose: of the 647 pages that both
	// paint and show text, exactly one paints first.
	//
	// Nothing downstream of layout reads these, and no accounting test counts them:
	// they carry no characters, so they cannot affect the conservation invariant that
	// every other field here is subject to.
	//
	// Empty for a page that draws none, which is the minority — 658 of ISO 32000-2's
	// 1,023 draw at least one, 284,866 rules in all, and 9 of the 11 reference
	// fixtures draw none.
	//
	// This comment said 580 pages before, and that is a corrected mismeasurement
	// rather than a quantity that moved: nothing in the rule-collection path has
	// changed the segments it records, only the width it records beside them. 658
	// and 284,866 are what a walk of every page reports.
	Rules []Rule
}

// Rule is one axis-aligned straight stroke or fill edge, in page coordinates.
//
// Kept as a segment rather than a full line because extent is what distinguishes a
// table's edge from a page decoration: a horizontal rule spanning the text measure is a
// header underline, and one spanning a single column is a cell edge. A consumer needs
// both endpoints to tell them apart.
//
// Curves are deliberately absent. A Bézier says nothing about where a table's edge
// runs, and the corpus draws 421 diagonal segments, all of them inside artwork.
type Rule struct {
	// Vertical reports the axis. A rule is one or the other by construction — a
	// segment that is neither is discarded rather than snapped to the nearer axis.
	Vertical bool

	// Pos is the coordinate on the axis the rule is perpendicular to: x for a
	// vertical rule, y for a horizontal one.
	Pos float64

	// From and To bound the rule along its own axis, From <= To.
	From, To float64

	// Width is the rule's thickness perpendicular to itself, in page units, when
	// it came from a stroked line; zero when it is one edge of a filled region.
	//
	// The two cases are not interchangeable and a consumer needs to tell them
	// apart. A filled rectangle has area, so its thickness is the distance between
	// the two edges reported here — both of them are. A stroked line has none: it
	// is reported once and its thickness is only this number. That is a difference
	// in the producer, not in the mark: ISO 32000-2 draws a fraction bar as a
	// filled rectangle and pdfTeX strokes the same bar, so a reader that measures
	// thickness one way sees it in one document and not the other.
	//
	// Zero is therefore ambiguous at the bottom end, deliberately: a hairline
	// "0 w" stroke and a filled edge both report zero. Both are the thinnest mark
	// the page can make, and distinguishing them would need a flag carrying no
	// information about the geometry.
	//
	// A path that is filled *and* stroked reports the stroke's width on all four
	// edges of its rectangle, so the two cases above are not exhaustive: "re B"
	// makes every edge both a filled region's boundary and a stroke of its own.
	//
	// Two producer habits are not read at all, and a consumer that measures a mark
	// should know it. A line width set through an ExtGState — "/GS0 gs" with /LW —
	// is not seen, so this value may be whatever the last "w" set or the initial 1;
	// and a skewed transformation has no single perpendicular extent for the
	// thickness to be, so the number would be wrong rather than absent.
	Width float64
}

// Length returns the rule's extent along its own axis.
func (r Rule) Length() float64 { return r.To - r.From }

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

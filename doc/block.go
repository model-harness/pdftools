package doc

import (
	"strings"

	"github.com/3rg0n/pdf-spec/geom"
)

// Role is what a block is, in the vocabulary a Markdown or OKF sink can act on.
//
// It is a deliberately smaller set than tag.Role, and the reduction is the point.
// ISO 32000-2 §14.8.4 defines around fifty structure types; Markdown can express
// perhaps eight of them. Mapping down happens once, where the structure tree is
// read, rather than in each sink — otherwise every sink grows its own opinion
// about whether a TOCI is a list item, and two sinks disagree about the same
// document.
//
// The set is also what layout/ and ocr/ have to produce. A vision model emits
// prose and headings, not TBody, so a vocabulary any narrower than this could not
// carry a tagged document's structure, and any wider could not be filled from an
// untagged one.
type Role string

const (
	// RoleParagraph is body text, the default and the overwhelming majority.
	RoleParagraph Role = "paragraph"

	// RoleHeading is a heading; its depth is in Block.Level.
	RoleHeading Role = "heading"

	// RoleListItem is one item of a list. Nesting is in Block.Level, so a sink can
	// indent without reconstructing a tree.
	RoleListItem Role = "list_item"

	// RoleTableCell is one cell. Table geometry is not modeled: reconstructing a
	// grid from a tagged table is sectionize's problem, and no sink needs it to
	// emit the cell's text.
	RoleTableCell Role = "table_cell"

	// RoleCode is preformatted text, where the extractor must not collapse
	// whitespace. The spec corpus is full of it.
	RoleCode Role = "code"

	// RoleQuote is a block quotation.
	RoleQuote Role = "quote"

	// RoleCaption is a figure or table caption. Separate from RoleParagraph
	// because a caption belongs with its figure when sections are stitched, and a
	// sink that cannot tell them apart puts it in the following paragraph.
	RoleCaption Role = "caption"

	// RoleFigure is a figure with no text of its own. Its Alt carries the
	// description, which for an accessible document is the only text there is.
	RoleFigure Role = "figure"

	// RoleArtifact is page furniture — a running header, a folio, a rule. Kept
	// rather than dropped at extraction, because "is this a header" is a judgement
	// and the evidence for it is here; a sink omits them, and probe can count them.
	RoleArtifact Role = "artifact"
)

// Block is a run of content that a sink emits as one unit.
//
// A block holds Spans rather than a string because style changes inside a
// paragraph carry meaning that Markdown can express — an italic term, a code
// identifier in prose — and flattening to a string at extraction time discards it
// irreversibly. The spans are also where the space-inference decisions land, so
// keeping them lets a defect be traced to the glyph run that caused it.
type Block struct {
	// Role is what this block is.
	Role Role

	// Level is the heading depth for RoleHeading (1-6) or the nesting depth for
	// RoleListItem (1-based). Zero for everything else.
	Level int

	// Spans are the block's styled runs in reading order.
	Spans []Span

	// Box is the block's bounding rectangle in the page's coordinate space.
	Box geom.Rect

	// Lang is a language override from the structure tree, when the block declares
	// one different from the document's.
	Lang string

	// Alt is /Alt or /ActualText: what the content means when the glyphs do not
	// spell it. For a RoleFigure it is the only text available, and for a block of
	// artwork-as-text it is more correct than the glyphs, so a sink prefers it —
	// which is why it is on the block rather than dropped once the spans are read.
	Alt string

	// MCIDs are the marked-content identifiers this block was assembled from, on
	// the block's page. Carried for diagnosis: when tagged text comes out in the
	// wrong order, the question is always which MCIDs went where, and answering it
	// without this means re-running the extraction.
	//
	// Not the join key for a structure element. A block is a *layout* unit and an
	// element is a *logical* one, and the two disagree often enough to matter: on
	// Well-Tagged-PDF-WTPDF-1.0.pdf, 12% of headings share a block with the body text
	// that follows them, because the extractor's paragraph heuristic saw one paragraph
	// where the tree declares a heading and a definition. Joining on this set resolves
	// such a heading's title to the heading plus the definition. Span.MCID is the key
	// that does not have that failure.
	MCIDs []int
}

// Text returns the block's text, spans concatenated with no separator.
//
// No separator because a span boundary is a style change, not a word boundary:
// the space between "an" and "italic" belongs to one span or the other, and
// inserting one here would double it. Space inference happens once, in the
// extractor, against font metrics — doing any of it in this package would put
// half the policy in the model and half in the producer.
func (b Block) Text() string {
	var sb strings.Builder
	b.writeText(&sb)
	return sb.String()
}

func (b Block) writeText(sb *strings.Builder) {
	for i := range b.Spans {
		sb.WriteString(b.Spans[i].Text)
	}
}

// IsEmpty reports whether the block would emit nothing. A block with no text and
// no Alt is a positioned rectangle a producer left behind, and every stage from
// sectionize onward has to skip it.
func (b Block) IsEmpty() bool {
	if b.Alt != "" {
		return false
	}
	for i := range b.Spans {
		if strings.TrimSpace(b.Spans[i].Text) != "" {
			return false
		}
	}
	return true
}

// Span is a run of text sharing one style.
type Span struct {
	// Text is the run's characters. Several characters may come from one glyph — a
	// ligature — and one character may come from several glyphs.
	Text string

	// Style is how the run was drawn.
	Style Style

	// Box is the run's bounding rectangle in page coordinates.
	Box geom.Rect

	// MCID is the marked-content identifier this run was drawn inside, or -1 when it
	// was drawn outside any marked-content sequence. Combined with the page number it
	// is the join key between page text and a structure element.
	//
	// It is on the span rather than only on the block because a span never crosses a
	// marked-content boundary while a block routinely does — the extractor starts a
	// new run at every MCID change, so this is exact where Block.MCIDs is a union. On
	// Well-Tagged-PDF-WTPDF-1.0.pdf that distinction is 12% of the headings.
	//
	// Zero is a valid MCID, so the absent value is -1 and cannot be the zero value of
	// the field. Constructing a Span by hand therefore leaves it claiming MCID 0; the
	// extractor always sets it, and a consumer joining on it should treat a page with
	// no marked content as having no join at all rather than trusting a 0.
	MCID int
}

// Style is the typographic identity of a span.
//
// Font stays alongside the Bold and Italic flags rather than being replaced by
// them, because the two disagree and each has a consumer. The flags are what the
// descriptor asserts, and a font named "Arial-BoldMT" whose descriptor forgot the
// flag is ordinary — so the extractor sets the flags from both sources. A
// consumer grouping runs into blocks still needs the exact font name, which no
// pair of booleans can stand in for.
type Style struct {
	// Font is the /BaseFont name with any subset prefix stripped.
	Font string

	// Size is the effective on-page size in user-space units, after the text
	// matrix and CTM. Not the Tf operand: a Tf of 1 with a matrix scaling by 12 is
	// how many producers set 12-point type, and reporting 1 makes every size-based
	// heading heuristic useless.
	Size float64

	// Bold and Italic are as declared by the font descriptor's flags and weight.
	Bold   bool
	Italic bool

	// Mono reports a fixed-pitch font, from the descriptor's flag. It is the signal
	// that distinguishes a code block from a paragraph in an untagged document,
	// where nothing else does.
	Mono bool

	// Hidden reports rendering mode 3 or 7 — invisible, or clip-only. This is the
	// text layer under a scanned page, which is exactly the text an extractor
	// wants, so it is reported rather than filtered. A sink emits it; a coverage
	// calculation counts it; only a renderer skips it.
	Hidden bool
}

// SameRun reports whether two styles are close enough that their spans can be
// merged into one.
//
// Size is compared with a tolerance because it is computed from a matrix, so two
// glyphs set in the same type can differ in the last bits. Without the tolerance
// a paragraph becomes one span per glyph, which is correct and useless.
func (s Style) SameRun(o Style, t geom.Tolerance) bool {
	if s.Font != o.Font || s.Bold != o.Bold || s.Italic != o.Italic ||
		s.Mono != o.Mono || s.Hidden != o.Hidden {
		return false
	}
	return t.NearlyEqual(s.Size, o.Size)
}

package doc

import (
	"strings"

	"github.com/model-harness/pdftools/geom"
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

	// RoleTableCell is one cell; Block.Cell says where in which table it sits.
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

	// Marker is a list item's label with the item's own text — the bullet glyph a
	// page draws, or the number or letter an ordered list counts. Empty for
	// everything that is not a list item, and for an item whose label is neither
	// declared nor drawn.
	//
	// It is a field rather than a prefix on the text for the reason Docling's
	// ListItem separates the two: a marker kept inside the text has to be re-found
	// by every sink, using a glyph allowlist each one would have to re-derive, and
	// on the one sink that exists it doubles — markdown writes its own "- ". Kept
	// rather than dropped because a bullet is losable and a label is not: "[1]" and
	// "a." are text the page says, and Markdown has no syntax that restates them, so
	// a sink that wants them needs to know they existed. Enumerated is that
	// distinction, derived from this.
	Marker string

	// Cell places a RoleTableCell in its table, and is nil for every other role.
	// See Cell for why the position is on the block rather than the blocks being
	// nested inside a table block.
	Cell *Cell

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

// Cell is a table cell's position in its table.
//
// It is a field on the cell block rather than a Table block holding rows holding
// cells, and that is the load-bearing decision here. A doc.Page is a flat list of
// blocks in reading order, and every stage after extraction — the space accounting,
// the character-conservation tests, the OKF sink, the unplaced-content report — walks
// that list. Nesting tables would make a block's text reachable by two different
// paths, and the invariant that a page's characters are the sum of its blocks' is what
// this repo's accounting tests rest on. A sink that wants the grid regroups on Table
// and Row, which is a scan of consecutive blocks because a tree's reading order puts
// one table's cells together.
//
// Spans are not modeled. Of 17482 cells on disk, 69 declare /ColSpan greater than 1
// and 43 declare /RowSpan — about 1%, concentrated in ISO/TS 32005 — and no Markdown
// table syntax can express either, so a sink pads the row instead. Reading them would
// let a sink place a spanning cell's text under only its first column, which is what
// GFM renders anyway; recording that they exist and are unrepresentable is the honest
// state, and tag.Elem does not read /A at all.
type Cell struct {
	// Table numbers the table within its document, from 1, so two adjacent tables do
	// not merge into one grid. A number rather than a pointer because doc is a data
	// model that has to survive being written to JSON and read back.
	Table int

	// Row is the 0-based row and Col the 0-based column, both counting the cells the
	// tree declares rather than any geometric position. A ragged row — 46 of 788
	// tables have one — therefore numbers its own cells consecutively and stops short,
	// which is what lets a sink pad it to the table's width.
	//
	// Col is an ordinal among the row's own kids, so a producer that put a cell
	// somewhere other than directly under its TR has no column and gets no Cell at all.
	// All 17482 cells on disk are direct children, and the spec does not require it.
	//
	// Row counts through THead, TBody and TFoot without restarting, because those group
	// rows rather than renumbering them — 4540 of the corpus's 4650 rows are inside one.
	Row int
	Col int

	// Header reports a TH rather than a TD. 773 of 788 tables on disk have an all-TH
	// first row, which is the shape a Markdown header row expresses; 598 also carry a
	// TH below row 0, which no Markdown syntax can mark, so a sink emits those as
	// ordinary cells and this field is what records that it had to.
	Header bool
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

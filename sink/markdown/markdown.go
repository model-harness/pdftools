// Package markdown writes a doc.Document as Markdown.
//
// This is a sink: it consumes doc and knows nothing about PDFs, fonts, or glyph
// positions. That separation is what lets a page recovered by OCR and a page
// recovered from a content stream produce identical output — by the time either
// reaches this package the difference is gone.
//
// It writes to an io.Writer and never touches the filesystem. Per-page splitting
// is a naming decision — where files go, what they are called, whether a directory
// is created — and that belongs to the command, which is also the only layer that
// can ask the user about it. Keeping it out means every function here is testable
// against a bytes.Buffer.
//
// The work that is actually difficult is escaping. Extracted text is prose that
// happens to contain every character Markdown reserves: a PDF specification is
// full of `<</Type /Page>>`, `*` footnote markers, and `snake_case` identifiers.
// Emitting it raw produces a document that renders wrong, and escaping it
// indiscriminately produces one that reads as backslashes. See escapeInto.
package markdown

import (
	"bufio"
	"io"
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// Options configures output.
type Options struct {
	// Frontmatter emits a YAML frontmatter block. Off by default, per
	// docs/DESIGN.md §2: frontmatter is what a knowledge bundle needs and what a
	// plain conversion does not, and a document that starts with a metadata block
	// is not what someone converting one file to read it asked for.
	Frontmatter bool

	// Artifacts emits blocks with doc.RoleArtifact — running headers, folios,
	// watermarks. Off by default, matching extract.Options.KeepArtifacts, so that
	// asking extract to keep them and asking this package to emit them are the same
	// decision made once. Without it the extractor's flag would silently do nothing.
	Artifacts bool
}

// DefaultOptions is conversion as the CLI runs it with no flags.
var DefaultOptions = Options{}

// Write emits the whole document, pages separated by a blank line.
//
// No page markers and no horizontal rules between pages. A paragraph continuing
// across a page break is one paragraph, and a document that announces every page
// boundary cannot be read as prose. Recovering the continuation is sectionize's
// job; asserting a boundary here would make that harder rather than easier.
func Write(w io.Writer, d *doc.Document, opt Options) error {
	bw := bufio.NewWriter(w)
	mw := &writer{w: bw}

	if opt.Frontmatter {
		mw.frontmatter(d.Meta, 0, len(d.Pages))
	}
	for i := range d.Pages {
		mw.page(d.Pages[i], opt)
	}
	if err := mw.err; err != nil {
		return err
	}
	return bw.Flush()
}

// WritePage emits one page, for --split.
//
// The metadata comes in separately because a page does not carry it and a split
// page still needs it: a directory of pages with no record of which document they
// came from cannot be checked against the original.
func WritePage(w io.Writer, meta doc.Metadata, p doc.Page, total int, opt Options) error {
	bw := bufio.NewWriter(w)
	mw := &writer{w: bw}

	if opt.Frontmatter {
		mw.frontmatter(meta, p.Number, total)
	}
	mw.page(p, opt)
	if err := mw.err; err != nil {
		return err
	}
	return bw.Flush()
}

// WriteBlocks emits a run of blocks and nothing else: no frontmatter, no page
// structure, no headings the caller did not put in the slice.
//
// It exists for sink/okf, which writes one file per clause and needs the body of that
// clause rendered with the same escaping policy as everything else. The alternative was
// a second implementation of escapeInto in that package, and two escaping policies
// diverge — the first document containing "<</Type /Page>>" would be escaped one way in
// the Markdown output and another in the bundle, from the same extraction.
func WriteBlocks(w io.Writer, blocks []doc.Block, opt Options) error {
	bw := bufio.NewWriter(w)
	mw := &writer{w: bw}
	mw.blocks(blocks, opt)
	if err := mw.err; err != nil {
		return err
	}
	return bw.Flush()
}

// YAMLString quotes a value for use as a YAML scalar, quoting only when the value needs
// it. Exported for sink/okf, whose frontmatter is nested where this package's is flat —
// so it cannot reuse the writer, but must not reimplement the quoting rule. See
// yamlString for what the rule is and why it is conservative.
func YAMLString(s string) string { return yamlString(s) }

// InlineText escapes a plain string as Markdown inline content — a heading a sink composed
// itself, a value that was a struct field rather than a span.
//
// Not treated as beginning a block, because the callers all prefix something: "# " before a
// heading, "* " before a list item. A "-" that follows either of those is a hyphen, and
// escaping it there would put a backslash in the middle of a rendered line.
func InlineText(s string) string {
	var sb strings.Builder
	escapeInto(&sb, s, false)
	return sb.String()
}

// LinkLabel escapes a plain string for use between the brackets of a Markdown link.
//
// Both brackets are escaped unconditionally here, where escapeInto escapes "[" only when it
// could open a link and "]" never — correct for prose, wrong inside a label, where the
// first unescaped "]" ends the label and turns the rest of the title into text followed by
// a bare URL. ISO 32000-2 has clause titles containing brackets, so this is a real case and
// not a defensive one.
func LinkLabel(s string) string {
	var sb strings.Builder
	start := 0
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '[' || c == ']' {
			escapeInto(&sb, s[start:i], false)
			sb.WriteByte('\\')
			sb.WriteByte(c)
			start = i + 1
		}
	}
	escapeInto(&sb, s[start:], false)
	return sb.String()
}

// String renders the document to a string, for callers that want the text rather
// than a stream — tests, and any future in-process consumer.
func String(d *doc.Document, opt Options) string {
	var sb strings.Builder
	// A strings.Builder cannot fail, so the error is structurally nil.
	_ = Write(&sb, d, opt)
	return sb.String()
}

// writer accumulates output and latches the first write error.
//
// Latching rather than returning per call: emitting a document is a few hundred
// writes to a bufio.Writer, which only fails once the underlying writer has, and
// threading an error return through every one of them would be more code than the
// rest of the package for no additional information.
type writer struct {
	w   *bufio.Writer
	err error

	// blank reports that the last thing written already ended with a blank line, so
	// the next block does not add a second one. Markdown treats one blank line as a
	// break and more as the same break, but a file full of double gaps is a file
	// someone will diff against a clean one.
	blank bool

	// started reports that something has been written, so the first block does not
	// open the document with a blank line.
	started bool

	// lastList is the kind of list the previous block was an item of, or notList, and
	// lastLevel is that item's depth. Consecutive items of one list are written on
	// adjacent lines: a blank line between them makes CommonMark treat the list as
	// loose and wrap every item in a paragraph. Two items that are not of one list
	// need the blank line — see sameList for when that is.
	lastList  listKind
	lastLevel int

	// indents is the column at which each list level's content begins, indexed by
	// depth: indents[0] is where a top-level item's text starts, and so a level-2
	// item's own marker. See listIndent — it is a running stack and not a fixed two
	// spaces per level because an ordered marker is wider than a bullet's.
	indents []int
}

// listKind is which of Markdown's list syntaxes a block is written with, or notList
// for a block that is not a list item.
//
// The distinction exists because CommonMark ends a list where the marker type
// changes: a bullet item followed immediately by an ordered one is already two
// lists, and writing them on adjacent lines only hides that from a reader of the
// Markdown. The blank line is the same one that separates any two blocks.
type listKind uint8

const (
	notList listKind = iota
	bulletList
	orderedList
)

// level normalizes a block's declared depth. Level 0 and level 1 both mean top
// level: a producer that declares a list item without a depth means the only depth
// there is.
func level(b doc.Block) int {
	if b.Level < 1 {
		return 1
	}
	return b.Level
}

// sameList reports whether an item at kind and depth continues the run the previous
// block began, and so is written on the adjacent line with no blank between.
//
// A blank line is emitted only where the marker type changes between two *top-level*
// items, which is where it costs nothing and says something: those are two sibling
// lists, CommonMark ends the first at the change regardless, and the blank line only
// makes the Markdown state what the renderer will already do.
//
// It is deliberately not emitted at a change of depth, nor at a kind change inside a
// nested run. A blank line between two items that are both inside an enclosing item
// makes that enclosing list loose — CommonMark then wraps every one of its items in a
// paragraph — which is a visible change to the whole list in exchange for honesty
// about a boundary the marker change already establishes. Cheap at top level, not
// cheap there.
func (w *writer) sameList(kind listKind, depth int) bool {
	if kind == notList || w.lastList == notList {
		return false
	}
	if kind == w.lastList {
		return true
	}
	return depth > 1 || w.lastLevel > 1
}

func (w *writer) str(s string) {
	if w.err != nil {
		return
	}
	_, w.err = w.w.WriteString(s)
}

func (w *writer) nl() { w.str("\n") }

// gap opens a block, emitting the blank line that separates it from the previous
// one.
func (w *writer) gap(kind listKind, depth int) {
	if !w.started {
		w.started = true
		return
	}
	if w.sameList(kind, depth) {
		return
	}
	if !w.blank {
		w.nl()
	}
}

func (w *writer) page(p doc.Page, opt Options) {
	w.blocks(p.Blocks, opt)
}

func (w *writer) block(b doc.Block) {
	kind, depth := notList, 0
	number := ""
	if b.Role == doc.RoleListItem {
		kind, depth = bulletList, level(b)
		if n, ok := arabicMarker(b.Marker); ok {
			kind, number = orderedList, n
		}
	}
	w.gap(kind, depth)

	switch b.Role {
	case doc.RoleHeading:
		w.str(strings.Repeat("#", headingLevel(b.Level)))
		w.str(" ")
		w.content(b, uniformEmphasis(b))
		w.nl()

	case doc.RoleListItem:
		// A nested item is indented to the column where its parent's content begins,
		// which is what makes it a child of that item rather than a sibling with
		// leading spaces. That column is the parent's own marker width, so it has to be
		// carried down rather than assumed: "- " is two, and "10. " is four.
		marker := "- "
		if number != "" {
			// The document's own number, in Markdown's own ordered syntax. Renumbering
			// from 1 would be a different claim than the page makes — a list starting at
			// 3 is continuing one something interrupted — and CommonMark only reads the
			// *first* item's number anyway, so emitting each item's own value costs
			// nothing and preserves what the page says. (Nothing on disk exercises this:
			// the corpus declares 13 ordered labels and none is arabic. The rule is
			// CommonMark's, not a measurement.)
			marker = number + ". "
		}
		w.str(strings.Repeat(" ", w.listIndent(depth, len(marker))))
		w.str(marker)
		if number != "" {
			w.content(b, false)
			w.nl()
			break
		}
		// A marker Markdown cannot say is written into the line instead, after the
		// bullet, escaped like any other text. That is every ordered label in the
		// corpus: extracting all 11 documents and counting doc.Block.Marker gives 13
		// enumerated labels over 2022 list items, and they are "[1]" through "[7]" once
		// each with "a." and "b." three times each. Markdown has no ordered syntax
		// for a bracketed or alphabetic label — only "1." and "1)" are markers to a
		// parser, so writing "a." as one would renumber it to 1 and lose what the page
		// says. Dropping it instead loses a reference the prose points at. A bullet
		// glyph is *not* written: the "- " above already is one, and restating it is
		// the doubling that this whole marker field exists to stop.
		if b.Enumerated() {
			var sb strings.Builder
			escapeInto(&sb, oneLine(b.Marker), true)
			w.str(sb.String())
			w.str(" ")
		}
		w.content(b, false)
		w.nl()

	case doc.RoleQuote:
		w.str("> ")
		w.content(b, false)
		w.nl()

	case doc.RoleCode:
		// Fenced, not indented: an indented code block cannot follow a list item
		// without being absorbed into it, and preformatted text in a specification is
		// usually inside one. No language tag — nothing in the model knows the
		// language, and guessing it would put syntax highlighting on prose.
		text := sanitize(b.Text())
		fence := codeFence(text)
		w.str(fence)
		w.nl()
		// Verbatim. Escaping inside a fence would emit the backslashes literally,
		// which is the one place the escaping policy must not run — sanitize adds no
		// backslashes and so is the one substitution that still applies here.
		w.str(strings.TrimRight(text, "\n"))
		w.nl()
		w.str(fence)
		w.nl()

	case doc.RoleCaption, doc.RoleFigure:
		// Markdown has no figure element, and a figure has no file to point at until
		// the images verb exists — `![alt]()` with an empty target is a broken image
		// reference, not a description. An emphasized line is the conventional
		// rendering of a caption and stays readable either way.
		w.str("*")
		w.content(b, true)
		w.str("*")
		w.nl()

	default:
		// Paragraph, artifact, and a table cell that has no position. A positioned cell
		// never reaches here — blocks routes it into a grid — so this is the untagged
		// case, where reconstructing the grid needs stroke paths nothing yet consumes.
		// One cell is not a table, and a row of one says less than the text does.
		w.content(b, false)
		w.nl()
	}

	// A block ends with the newline that terminates its last line and nothing more.
	// The blank line that separates it from whatever follows is gap's, written when
	// the next block opens — which is the only point at which it is known whether one
	// is wanted. Writing it here instead would put a blank line between consecutive
	// list items, making the list loose, and a trailing one at the end of every file.
	w.blank = false
	w.lastList = kind
	w.lastLevel = depth
}

// listIndent returns the number of spaces an item at depth is written with, and
// records where its own content begins so a child can be indented to that column.
//
// CommonMark makes a nested item a child of the one above it by indenting it to
// within the parent's content — which is the parent's marker width, not a fixed two
// spaces. Two is right under "- " and short under "1. ", where an item indented two
// lands in the parent's marker rather than its text and parses as a sibling; the
// document's nesting is silently flattened. So the column is carried in a stack, each
// level recording the width of the marker actually written there.
//
// A depth that skips a level — a level-3 item with no level-2 above it, which a
// producer can declare — indents to the deepest level seen rather than inventing a
// parent, since a stack with a hole in it has no column to name.
func (w *writer) listIndent(depth, width int) int {
	if w.lastList == notList {
		// A block that is not a list item is written at column zero after a blank line,
		// which ends every open list — so the columns recorded for them name parents
		// that no longer exist. A list resuming at depth 2 after a paragraph has no
		// parent to nest under and starts again at the left margin.
		w.indents = w.indents[:0]
	}
	if depth > len(w.indents)+1 {
		depth = len(w.indents) + 1
	}
	if depth < 2 {
		w.indents = append(w.indents[:0], width)
		return 0
	}
	indent := w.indents[depth-2]
	w.indents = append(w.indents[:depth-1], indent+width)
	return indent
}

// arabicMarker returns the digits of an ordered label Markdown can express, and
// whether the label is one.
//
// Markdown's ordered syntax is a run of digits then "." or ")", so that is exactly
// what converts: "1." becomes a real list marker and everything else cannot. The
// corpus's own ordered labels are all in the second group — "[1]" through "[7]" and
// "a."/"b." — which is why the caller still has a path that writes the label as text.
//
// The delimiter is normalized to "." rather than preserved, because it is syntax
// here and not content: a parser treats "1)" and "1." as the same marker, and a
// document that used one is not saying anything a reader of the Markdown can act on.
// The number is preserved for the opposite reason — it is the only part of an ordered
// label that carries information.
//
// CommonMark caps an ordered marker at 9 digits and requires the start value to fit
// in 32 bits, so a longer run is not a marker at all. Nothing on disk approaches it;
// the bound is here because a page number or a year extracted as a label would
// otherwise emit a list a parser refuses to open.
func arabicMarker(marker string) (string, bool) {
	if len(marker) < 2 || len(marker) > 10 {
		return "", false
	}
	if d := marker[len(marker)-1]; d != '.' && d != ')' {
		return "", false
	}
	digits := marker[:len(marker)-1]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", false
		}
	}
	return digits, true
}

// content writes a block's text.
//
// Alt takes precedence over the spans when present, because that is what it means:
// /ActualText and /Alt are the producer's statement of what the content says when
// the glyphs do not spell it — an image of a word, a ligature drawn as artwork, a
// decorative capital. Preferring the glyphs there would emit the thing the producer
// went out of its way to correct. Styling is dropped with it, since Alt is a string
// and has none.
//
// plain suppresses emphasis markers, for contexts already wrapped in them: nesting
// "*" inside "*" does not nest, it terminates.
func (w *writer) content(b doc.Block, plain bool) {
	if b.Alt != "" {
		var sb strings.Builder
		escapeInto(&sb, oneLine(b.Alt), true)
		w.str(sb.String())
		return
	}
	w.str(inline(b.Spans, plain))
}

// uniformEmphasis reports whether every visible span in a block carries the same
// bold-or-italic emphasis, which for a heading means the emphasis is the heading
// rather than something inside it.
//
// "# **1 First Section**" is wrong twice over: the weight is why the block was
// recognized as a heading in the first place — layout's typographic gate admits a
// body-size block only when it is bold — and "#" already says heading, so the
// asterisks restate it in a way no author would write. Emphasis *within* a heading is
// different and survives: "## The value of `Length`" and a heading with one italic
// term are both real, and both have spans that disagree.
//
// Monospace is not emphasis for this purpose. A heading is not promoted for being
// monospaced, so backticks in one are always about the text.
func uniformEmphasis(b doc.Block) bool {
	var first doc.Span
	seen := false
	for _, sp := range b.Spans {
		if strings.TrimSpace(sp.Text) == "" || sp.Style.Mono {
			continue
		}
		if !seen {
			first, seen = sp, true
			continue
		}
		if sp.Style.Bold != first.Style.Bold || sp.Style.Italic != first.Style.Italic {
			return false
		}
	}
	return seen && (first.Style.Bold || first.Style.Italic)
}

func headingLevel(n int) int {
	if n < 1 {
		return 1
	}
	if n > 6 {
		// Markdown has six. A deeper structure tree — ISO 32000-2 nests clauses past
		// six — flattens rather than emitting "#######", which is not a heading at all
		// and renders as literal hashes.
		return 6
	}
	return n
}

// codeFence returns a backtick fence long enough to open a fenced code block
// containing s.
//
// Three backticks is CommonMark's minimum for a block, but a code block quoting a
// fenced block needs more, and a specification that documents Markdown is exactly
// the document that contains one. A code span has a different minimum — see
// spanFence.
func codeFence(s string) string {
	n := backtickRun(s) + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// spanFence returns a backtick fence long enough to delimit s as a code span.
//
// The minimum is one, not three: a fenced block needs three but a span needs only
// enough to exceed the longest run inside it, and "```x```" is a three-backtick span
// where "`x`" is what a reader expects to see in the source.
func spanFence(s string) string {
	return strings.Repeat("`", backtickRun(s)+1)
}

// backtickRun returns the length of the longest unbroken run of backticks in s.
func backtickRun(s string) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	return longest
}

// oneLine collapses newlines to spaces.
//
// Extraction does not produce them, but Alt is a string a producer wrote and may
// contain anything. A literal newline is harmless inside a paragraph and breaks a
// list item out of its list, so it is removed at the point the text becomes
// Markdown rather than guarded against everywhere downstream.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
}

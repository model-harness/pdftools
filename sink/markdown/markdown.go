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

	"github.com/3rg0n/pdf-spec/doc"
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

	// lastList reports that the previous block was a list item. Consecutive items
	// are written on adjacent lines: a blank line between them makes CommonMark
	// treat the list as loose and wrap every item in a paragraph.
	lastList bool
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
func (w *writer) gap(list bool) {
	if !w.started {
		w.started = true
		return
	}
	if list && w.lastList {
		return
	}
	if !w.blank {
		w.nl()
	}
}

func (w *writer) page(p doc.Page, opt Options) {
	for i := range p.Blocks {
		b := p.Blocks[i]
		if b.Role == doc.RoleArtifact && !opt.Artifacts {
			continue
		}
		if b.IsEmpty() {
			continue
		}
		w.block(b)
	}
}

func (w *writer) block(b doc.Block) {
	list := b.Role == doc.RoleListItem
	w.gap(list)

	switch b.Role {
	case doc.RoleHeading:
		w.str(strings.Repeat("#", headingLevel(b.Level)))
		w.str(" ")
		w.content(b, false)
		w.nl()

	case doc.RoleListItem:
		// Two spaces per level is CommonMark's minimum for nesting under a "- "
		// marker. Level 1 and level 0 both mean top level: a producer that declares a
		// list item without a depth means the only depth there is.
		if n := b.Level - 1; n > 0 {
			w.str(strings.Repeat("  ", n))
		}
		w.str("- ")
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
		fence := codeFence(b.Text())
		w.str(fence)
		w.nl()
		// Verbatim. Escaping inside a fence would emit the backslashes literally,
		// which is the one place the escaping policy must not run.
		w.str(strings.TrimRight(b.Text(), "\n"))
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
		// Paragraph, artifact, and table cell. A cell is emitted as its own paragraph
		// because one cell is not a table: reconstructing a grid needs the row and
		// column structure that only the tagged path has, and it emits real tables
		// itself once sectionize reads them.
		w.content(b, false)
		w.nl()
	}

	// A block ends with the newline that terminates its last line and nothing more.
	// The blank line that separates it from whatever follows is gap's, written when
	// the next block opens — which is the only point at which it is known whether one
	// is wanted. Writing it here instead would put a blank line between consecutive
	// list items, making the list loose, and a trailing one at the end of every file.
	w.blank = false
	w.lastList = list
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

package markdown

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// The tests here are built from doc values directly rather than by extracting a
// PDF. This package's input is doc.Document and nothing else, so a fixture PDF
// would test extract as well and could not express the cases that matter — a span
// boundary landing on the space after a bold word, an /Alt that contains a newline,
// a title with a colon in it. Each is one value.

func span(text string, opts ...func(*doc.Span)) doc.Span {
	s := doc.Span{Text: text, Style: doc.Style{Font: "Helvetica", Size: 10}}
	for _, o := range opts {
		o(&s)
	}
	return s
}

func bold(s *doc.Span)   { s.Style.Bold = true }
func italic(s *doc.Span) { s.Style.Italic = true }
func mono(s *doc.Span)   { s.Style.Mono = true }
func hidden(s *doc.Span) { s.Style.Hidden = true }

func para(spans ...doc.Span) doc.Block {
	return doc.Block{Role: doc.RoleParagraph, Spans: spans, Box: geom.NewRect(0, 0, 100, 10)}
}

// render is the one-block case, which is most of what these tests need.
func render(t *testing.T, b doc.Block) string {
	t.Helper()
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{b}}}}
	return String(d, DefaultOptions)
}

func TestParagraph(t *testing.T) {
	got := render(t, para(span("Hello world.")))
	if got != "Hello world.\n" {
		t.Errorf("got %q", got)
	}
}

func TestBlocksSeparatedByBlankLine(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		para(span("First.")),
		para(span("Second.")),
	}}}}
	got := String(d, DefaultOptions)
	if got != "First.\n\nSecond.\n" {
		t.Errorf("got %q", got)
	}
}

// A paragraph continuing across a page break is one paragraph on the page, so the
// output must not announce the boundary. Blocks are already separated; a page adds
// nothing of its own.
func TestNoPageMarkers(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: []doc.Block{para(span("One."))}},
		{Number: 2, Blocks: []doc.Block{para(span("Two."))}},
	}}
	got := String(d, DefaultOptions)
	if strings.Contains(got, "---") || strings.Contains(got, "page") {
		t.Errorf("page boundary announced: %q", got)
	}
	if got != "One.\n\nTwo.\n" {
		t.Errorf("got %q", got)
	}
}

func TestHeadingLevels(t *testing.T) {
	for _, tc := range []struct {
		level int
		want  string
	}{
		{1, "# H\n"},
		{3, "### H\n"},
		{6, "###### H\n"},
		// Markdown has six levels. A seventh renders as literal hashes, not a
		// heading, and ISO 32000-2 nests clauses past six.
		{7, "###### H\n"},
		{9, "###### H\n"},
		// A heading with no declared depth is a heading.
		{0, "# H\n"},
	} {
		b := doc.Block{Role: doc.RoleHeading, Level: tc.level, Spans: []doc.Span{span("H")}}
		if got := render(t, b); got != tc.want {
			t.Errorf("level %d: got %q, want %q", tc.level, got, tc.want)
		}
	}
}

// TestHeadingWholeBlockEmphasisDropped: "#" already says heading, so the emphasis that
// covers the whole of one restates it — and on the untagged path that emphasis is
// *why* the block was recognized as a heading at all, since layout admits a body-size
// block only when it is bold. "# **1 First Section**" is what nobody writes by hand.
//
// Emphasis inside a heading is a different claim and survives: a heading with one
// italic term means it, and its spans disagree.
func TestHeadingWholeBlockEmphasisDropped(t *testing.T) {
	h := func(spans ...doc.Span) doc.Block {
		return doc.Block{Role: doc.RoleHeading, Level: 2, Spans: spans}
	}
	for _, tc := range []struct {
		name string
		in   doc.Block
		want string
	}{
		{"all bold", h(span("1.1 A Subsection", bold)), "## 1.1 A Subsection\n"},
		{"all italic", h(span("A Subsection", italic)), "## A Subsection\n"},
		{"all bold italic", h(span("A Subsection", bold, italic)), "## A Subsection\n"},
		{"plain unchanged", h(span("A Subsection")), "## A Subsection\n"},
		// Bold across the spans, with whitespace between them carrying no style. A
		// heading arrives split at a marked-content boundary routinely, and the
		// whitespace-only span must not be read as disagreement.
		{"bold across spans", h(span("1.1", bold), span(" "), span("A Subsection", bold)),
			"## 1.1 A Subsection\n"},
		// Genuine internal emphasis: kept, because the heading is not uniform.
		{"partial italic", h(span("The value of "), span("Length", italic)),
			"## The value of *Length*\n"},
		{"partial bold", h(span("Note ", bold), span("well")), "## **Note** well\n"},
		// Monospace is not emphasis here: nothing promotes a block for being
		// monospaced, so backticks in a heading are always about the text.
		{"all mono", h(span("Length", mono)), "## `Length`\n"},
		{"bold mono", h(span("Length", bold, mono)), "## `Length`\n"},
	} {
		if got := render(t, tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEmphasis(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   doc.Span
		want string
	}{
		{"bold", span("x", bold), "**x**\n"},
		{"italic", span("x", italic), "*x*\n"},
		{"both", span("x", bold, italic), "***x***\n"},
		// Mono wins: everything inside backticks is literal, so asterisks there
		// render as asterisks. The identifier is the half that carries meaning.
		{"mono", span("x", mono), "`x`\n"},
		{"mono bold", span("x", mono, bold), "`x`\n"},
		// An OCR text layer is drawn invisibly in a fixed-pitch font that exists only
		// to hold glyph codes, so its monospacing says nothing about the text. Left
		// in, every scanned document converts to one long code span.
		{"mono hidden", span("x", mono, hidden), "x\n"},
		{"hidden alone", span("x", hidden), "x\n"},
	} {
		if got := render(t, para(tc.in)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The delimiter run has to be one pair, not one per span. "**a****b**" is four
// consecutive asterisks, which CommonMark resolves differently than either pair
// alone — and the extractor splits at every style change including ones that
// produce identical Markdown.
func TestAdjacentSameStyleSpansShareOneDelimiter(t *testing.T) {
	got := render(t, para(span("ab", bold), span("cd", bold)))
	if got != "**abcd**\n" {
		t.Errorf("got %q, want %q", got, "**abcd**\n")
	}
}

// A size change inside one bold run is a style change the extractor reports and
// Markdown cannot express. It must not become two delimiter pairs.
func TestSizeChangeInsideBoldRun(t *testing.T) {
	a := span("ab", bold)
	b := span("cd", bold)
	b.Style.Size = 12
	if got := render(t, para(a, b)); got != "**abcd**\n" {
		t.Errorf("got %q", got)
	}
}

// CommonMark requires that an opening delimiter run not be followed by whitespace
// and a closing run not be preceded by it, so "**bold **next" emits literal
// asterisks. A span boundary landing on the space after a bold word is ordinary.
func TestWhitespaceMovesOutsideDelimiters(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"trailing", "bold ", "**bold** \n"},
		{"leading", " bold", " **bold**\n"},
		{"both", " bold ", " **bold** \n"},
	} {
		if got := render(t, para(span(tc.text, bold))); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A whitespace-only span is emitted so the word boundary survives, but never
// wrapped: "* *" is asterisks around a space, which is not emphasis.
func TestWhitespaceOnlySpanNotEmphasized(t *testing.T) {
	got := render(t, para(span("a", bold), span(" ", bold), span("b", italic)))
	if got != "**a** *b*\n" {
		t.Errorf("got %q", got)
	}
}

func TestListItems(t *testing.T) {
	item := func(level int, text string) doc.Block {
		return doc.Block{Role: doc.RoleListItem, Level: level, Spans: []doc.Span{span(text)}}
	}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		item(1, "one"),
		item(2, "nested"),
		item(1, "two"),
	}}}}
	// Adjacent items on adjacent lines: a blank line between them makes CommonMark
	// treat the list as loose and wrap every item in its own paragraph.
	want := "- one\n  - nested\n- two\n"
	if got := String(d, DefaultOptions); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An arabic ordered label becomes Markdown's own ordered syntax, keeping the
// document's number.
//
// The number is not renumbered from 1 because the page's value is what the document
// says — CommonMark reads only the first item's number anyway, so preserving each
// item's own costs nothing — and the delimiter *is* normalized, because "1)" and "1."
// are the same marker to a parser and the choice carries nothing a reader can act on.
func TestOrderedListUsesMarkdownSyntax(t *testing.T) {
	item := func(marker, text string) doc.Block {
		return doc.Block{Role: doc.RoleListItem, Level: 1, Marker: marker,
			Spans: []doc.Span{span(text)}}
	}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		item("3.", "three"),
		item("4)", "four"),
	}}}}
	want := "3. three\n4. four\n"
	if got := String(d, DefaultOptions); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An ordered label Markdown cannot express keeps the bullet and is written as text.
//
// This is every ordered label in the corpus: the 13 on disk are "[1]" through "[7]"
// and "a."/"b.", and neither a bracketed nor an alphabetic label is a marker to a
// parser. Writing "a." as one would renumber it to 1 and lose what the page says;
// dropping it would lose a reference the prose points at.
func TestUnexpressibleOrderedLabelStaysText(t *testing.T) {
	item := func(marker, text string) doc.Block {
		return doc.Block{Role: doc.RoleListItem, Level: 1, Marker: marker,
			Spans: []doc.Span{span(text)}}
	}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		item("[1]", "cited"),
		item("a.", "lettered"),
	}}}}
	// Neither label needs a backslash, and that is the escaping policy holding rather
	// than a gap in it: a bare "[1]" is literal text in CommonMark — which is why the
	// corpus's citations survive unescaped — and "a." is not an ordered marker because
	// only digits are. The one label that would have needed the escape is exactly the
	// one that no longer reaches this branch, since arabicMarker takes it.
	want := "- [1] cited\n- a. lettered\n"
	if got := String(d, DefaultOptions); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A bullet list and an ordered list are two lists, so they are separated.
//
// CommonMark ends a list where the marker type changes, so writing these on adjacent
// lines would render as two lists regardless — the blank line only makes the Markdown
// say what the renderer will do. testdata/reference/tagged-lists.pdf is this shape and
// its gold file carries the blank line.
func TestBulletAndOrderedListsAreSeparated(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		{Role: doc.RoleListItem, Level: 1, Marker: "•", Spans: []doc.Span{span("bullet")}},
		{Role: doc.RoleListItem, Level: 1, Marker: "1.", Spans: []doc.Span{span("first")}},
		{Role: doc.RoleListItem, Level: 1, Marker: "2.", Spans: []doc.Span{span("second")}},
	}}}}
	want := "- bullet\n\n1. first\n2. second\n"
	if got := String(d, DefaultOptions); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A nested item is indented to the column where its parent's content begins, which
// is the parent's marker width and not a fixed two spaces.
//
// Two is right under "- " and short under "1. ": an item indented two there lands in
// the parent's marker rather than its text, which CommonMark parses as a sibling, so
// the document's nesting is flattened without anything reporting it. The measurement
// that matters is the column, so each case states it.
func TestNestedItemIndentsToParentContent(t *testing.T) {
	item := func(depth int, marker, text string) doc.Block {
		return doc.Block{Role: doc.RoleListItem, Level: depth, Marker: marker,
			Spans: []doc.Span{span(text)}}
	}
	for _, tc := range []struct {
		name   string
		blocks []doc.Block
		want   string
	}{
		// "1. " is three columns wide, so the child is indented three.
		{"ordered under ordered", []doc.Block{
			item(1, "1.", "one"), item(2, "2.", "nested"), item(1, "2.", "two"),
		}, "1. one\n   2. nested\n2. two\n"},
		{"bullet under ordered", []doc.Block{
			item(1, "1.", "one"), item(2, "•", "nested"),
		}, "1. one\n   - nested\n"},
		// "- " is two, which is the case the fixed indent was written for.
		{"ordered under bullet", []doc.Block{
			item(1, "•", "bullet"), item(2, "1.", "nested"),
		}, "- bullet\n  1. nested\n"},
		// A two-digit marker is wider again, and the child follows it.
		{"wide marker", []doc.Block{
			item(1, "10.", "ten"), item(2, "•", "nested"),
		}, "10. ten\n    - nested\n"},
		// A depth that skips a level indents to the deepest level actually written
		// rather than inventing the parent it names: a stack with a hole in it has no
		// column to indent to. A producer can declare this.
		{"skipped level", []doc.Block{
			item(1, "•", "one"), item(3, "•", "deep"),
		}, "- one\n  - deep\n"},
	} {
		d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: tc.blocks}}}
		if got := String(d, DefaultOptions); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The blank line at a change of marker type is emitted between top-level items only.
//
// Between two items nested inside an enclosing one it would make that enclosing list
// loose — CommonMark then wraps every one of its items in a paragraph — so the cost is
// a visible change to the whole list, in exchange for stating a boundary the marker
// change already establishes. At top level there is no enclosing list to loosen and
// the blank line is free.
func TestKindChangeSeparatesOnlyAtTopLevel(t *testing.T) {
	item := func(depth int, marker, text string) doc.Block {
		return doc.Block{Role: doc.RoleListItem, Level: depth, Marker: marker,
			Spans: []doc.Span{span(text)}}
	}
	for _, tc := range []struct {
		name   string
		blocks []doc.Block
		want   string
	}{
		{"ordered then bullet", []doc.Block{
			item(1, "1.", "one"), item(1, "•", "bullet"),
		}, "1. one\n\n- bullet\n"},
		// Both nested under a top-level item: no blank line, or the outer list goes
		// loose.
		{"kind change while nested", []doc.Block{
			item(1, "•", "outer"), item(2, "•", "a"), item(2, "1.", "b"),
		}, "- outer\n  - a\n  1. b\n"},
		// Returning to top level from a nested item is a change of depth, not of list:
		// the run continues and a blank line would loosen it.
		{"depth change alone", []doc.Block{
			item(1, "•", "one"), item(2, "•", "nested"), item(1, "•", "two"),
		}, "- one\n  - nested\n- two\n"},
	} {
		d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: tc.blocks}}}
		if got := String(d, DefaultOptions); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A paragraph ends every open list, so a list resuming at depth 2 has no parent left
// to nest under and starts again at the left margin.
//
// The indent stack outlives the list that filled it, and the columns in it name items
// that are no longer open — indenting to one would emit two spaces before a marker
// with nothing above it, which CommonMark reads as a top-level item anyway. The
// nesting is not recoverable here; what is avoidable is claiming it.
func TestListResumingAfterParagraphStartsAtMargin(t *testing.T) {
	item := func(depth int, text string) doc.Block {
		return doc.Block{Role: doc.RoleListItem, Level: depth, Marker: "•",
			Spans: []doc.Span{span(text)}}
	}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		item(1, "one"), item(2, "nested"),
		para(span("Interrupting.")),
		item(2, "resumed"),
	}}}}
	want := "- one\n  - nested\n\nInterrupting.\n\n- resumed\n"
	if got := String(d, DefaultOptions); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A number's leading zeros are the page's, and CommonMark reads "007." as starting at
// seven, so writing it back is both faithful and correct. Stripping them would be a
// silent edit to what the document says with nothing gained.
func TestOrderedMarkerKeepsLeadingZeros(t *testing.T) {
	b := doc.Block{Role: doc.RoleListItem, Level: 1, Marker: "007.",
		Spans: []doc.Span{span("seven")}}
	if got := render(t, b); got != "007. seven\n" {
		t.Errorf("got %q", got)
	}
}

// arabicMarker's boundaries, which are CommonMark's: digits then "." or ")".
func TestArabicMarkerRecognition(t *testing.T) {
	for _, tc := range []struct {
		marker string
		digits string
		ok     bool
	}{
		{"1.", "1", true},
		{"1)", "1", true},
		{"42.", "42", true},
		{"123456789.", "123456789", true},
		// CommonMark caps an ordered marker at 9 digits, so a longer run is not one —
		// a page number or a year extracted as a label would otherwise emit a list a
		// parser refuses to open.
		{"1234567890.", "", false},
		// Not markers to a parser, and all of the corpus's own ordered labels.
		{"[1]", "", false},
		{"a.", "", false},
		// CommonMark's delimiter set is "." and ")" and nothing else, so a bracket is
		// not one even with digits in front of it. Nothing on disk is shaped this way —
		// the corpus's bracketed labels are "[1]", which the "[" already rejects — but
		// widening the set here is a mutation no other case catches, and converting
		// "1]" would silently drop the bracket the page drew.
		{"1]", "", false},
		{"1", "", false},
		{".", "", false},
		{"", "", false},
		{"•", "", false},
		{"1.2.", "", false},
	} {
		digits, ok := arabicMarker(tc.marker)
		if ok != tc.ok || digits != tc.digits {
			t.Errorf("arabicMarker(%q) = %q, %v; want %q, %v",
				tc.marker, digits, ok, tc.digits, tc.ok)
		}
	}
}

// A list followed by a paragraph does need the blank line, or the paragraph is
// absorbed into the last item as a lazy continuation.
func TestListThenParagraphSeparated(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		{Role: doc.RoleListItem, Level: 1, Spans: []doc.Span{span("item")}},
		para(span("After.")),
	}}}}
	if got := String(d, DefaultOptions); got != "- item\n\nAfter.\n" {
		t.Errorf("got %q", got)
	}
}

func TestQuote(t *testing.T) {
	b := doc.Block{Role: doc.RoleQuote, Spans: []doc.Span{span("quoted")}}
	if got := render(t, b); got != "> quoted\n" {
		t.Errorf("got %q", got)
	}
}

// Code is fenced, not indented: an indented block cannot follow a list item
// without being absorbed into it, and preformatted text in a specification usually
// is inside one.
func TestCodeBlockFenced(t *testing.T) {
	b := doc.Block{Role: doc.RoleCode, Spans: []doc.Span{span("<</Type /Page>>")}}
	want := "```\n<</Type /Page>>\n```\n"
	if got := render(t, b); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A code block quoting a fenced block needs a longer fence — and a specification
// documenting Markdown is exactly that document.
func TestCodeFenceLongerThanContent(t *testing.T) {
	b := doc.Block{Role: doc.RoleCode, Spans: []doc.Span{span("```\nx\n```")}}
	got := render(t, b)
	if !strings.HasPrefix(got, "````\n") {
		t.Errorf("fence not extended: %q", got)
	}
	if strings.Count(got, "````") != 2 {
		t.Errorf("want two four-backtick fences: %q", got)
	}
}

// Inside a fence everything is literal, so escaping would emit the backslashes.
func TestCodeBlockNotEscaped(t *testing.T) {
	b := doc.Block{Role: doc.RoleCode, Spans: []doc.Span{span("a *b* _c_ [d]")}}
	if got := render(t, b); !strings.Contains(got, "a *b* _c_ [d]") {
		t.Errorf("code was escaped: %q", got)
	}
}

func TestCaptionEmphasized(t *testing.T) {
	b := doc.Block{Role: doc.RoleCaption, Spans: []doc.Span{span("Figure 1")}}
	if got := render(t, b); got != "*Figure 1*\n" {
		t.Errorf("got %q", got)
	}
}

// Nesting "*" inside "*" terminates rather than nests, so a caption's own spans
// must not add emphasis of their own.
func TestCaptionSuppressesInnerEmphasis(t *testing.T) {
	b := doc.Block{Role: doc.RoleCaption, Spans: []doc.Span{span("Figure", bold), span(" 1")}}
	if got := render(t, b); got != "*Figure 1*\n" {
		t.Errorf("got %q", got)
	}
}

// /Alt and /ActualText are the producer's statement of what content says when the
// glyphs do not spell it. Preferring the glyphs would emit the thing the producer
// went out of its way to correct.
func TestAltPreferredOverSpans(t *testing.T) {
	b := para(span("gibberish"))
	b.Alt = "The actual text"
	if got := render(t, b); got != "The actual text\n" {
		t.Errorf("got %q", got)
	}
}

func TestFigureWithOnlyAlt(t *testing.T) {
	b := doc.Block{Role: doc.RoleFigure, Alt: "A diagram"}
	if got := render(t, b); got != "*A diagram*\n" {
		t.Errorf("got %q", got)
	}
}

// A newline in an /Alt breaks a list item out of its list. It is producer-supplied
// and may contain anything.
func TestAltNewlinesCollapsed(t *testing.T) {
	b := doc.Block{Role: doc.RoleListItem, Level: 1, Alt: "one\ntwo\r\nthree"}
	if got := render(t, b); got != "- one two three\n" {
		t.Errorf("got %q", got)
	}
}

// Artifacts are page furniture. Off by default, so a running header does not appear
// once per page in the middle of prose.
func TestArtifactsDroppedByDefault(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		{Role: doc.RoleArtifact, Spans: []doc.Span{span("ISO 32000-2:2020")}},
		para(span("Body.")),
	}}}}
	if got := String(d, DefaultOptions); got != "Body.\n" {
		t.Errorf("got %q", got)
	}
	if got := String(d, Options{Artifacts: true}); !strings.Contains(got, "ISO 32000-2:2020") {
		t.Errorf("Artifacts=true dropped it: %q", got)
	}
}

// A positioned rectangle a producer left behind must not emit a blank paragraph,
// which would show up as a double gap in the output.
func TestEmptyBlocksSkipped(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		para(span("One.")),
		para(span("   ")),
		doc.Block{Role: doc.RoleParagraph},
		para(span("Two.")),
	}}}}
	if got := String(d, DefaultOptions); got != "One.\n\nTwo.\n" {
		t.Errorf("got %q", got)
	}
}

func TestEmptyDocument(t *testing.T) {
	if got := String(&doc.Document{}, DefaultOptions); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// A page the extractor could not read is still a page. It contributes nothing and
// must not contribute a stray blank line either.
func TestBlankPagesContributeNothing(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: []doc.Block{para(span("One."))}},
		{Number: 2},
		{Number: 3, Blocks: []doc.Block{para(span("Three."))}},
	}}
	if got := String(d, DefaultOptions); got != "One.\n\nThree.\n" {
		t.Errorf("got %q", got)
	}
}

// A table cell is not a table: reconstructing a grid needs row and column
// structure that only the tagged path has.
func TestTableCellAsParagraph(t *testing.T) {
	b := doc.Block{Role: doc.RoleTableCell, Spans: []doc.Span{span("cell")}}
	if got := render(t, b); got != "cell\n" {
		t.Errorf("got %q", got)
	}
}

func TestWritePage(t *testing.T) {
	var sb strings.Builder
	p := doc.Page{Number: 7, Blocks: []doc.Block{para(span("Seven."))}}
	if err := WritePage(&sb, doc.Metadata{}, p, 20, Options{Frontmatter: true}); err != nil {
		t.Fatal(err)
	}
	got := sb.String()
	if !strings.Contains(got, "page: 7\n") {
		t.Errorf("page number missing: %q", got)
	}
	if !strings.Contains(got, "pages: 20\n") {
		t.Errorf("total missing: %q", got)
	}
	if !strings.HasSuffix(got, "Seven.\n") {
		t.Errorf("body missing: %q", got)
	}
}

// A whole-document conversion has no page number, and emitting "page: 1" there
// would be actively wrong.
func TestWholeDocumentHasNoPageField(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{para(span("x"))}}}}
	got := String(d, Options{Frontmatter: true})
	if strings.Contains(got, "page: ") {
		t.Errorf("page field present: %q", got)
	}
	if !strings.Contains(got, "pages: 1\n") {
		t.Errorf("page count missing: %q", got)
	}
}

// A write error has to surface. Latching the first one means a failure on the third
// of several hundred writes still reaches the caller.
type failWriter struct{ after int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errFail
	}
	f.after--
	return len(p), nil
}

type failErr struct{}

func (failErr) Error() string { return "write failed" }

var errFail = failErr{}

func TestWriteErrorSurfaces(t *testing.T) {
	blocks := make([]doc.Block, 200)
	for i := range blocks {
		blocks[i] = para(span("some text to fill the buffer past its capacity"))
	}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: blocks}}}
	if err := Write(&failWriter{}, d, DefaultOptions); err == nil {
		t.Error("no error from failing writer")
	}
}

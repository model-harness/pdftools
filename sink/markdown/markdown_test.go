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

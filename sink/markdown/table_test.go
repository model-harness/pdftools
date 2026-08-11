package markdown

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// cell is one positioned cell. The position is what makes a grid, so every test here
// states it rather than relying on order — that is the distinction between this sink and
// one that groups by adjacency, and a test built on order could not tell them apart.
func cell(table, row, col int, header bool, text string) doc.Block {
	return doc.Block{
		Role:  doc.RoleTableCell,
		Cell:  &doc.Cell{Table: table, Row: row, Col: col, Header: header},
		Spans: []doc.Span{span(text)},
	}
}

// monoCell is a cell whose text is monospaced, which routes it through writeCode rather
// than escapeInto — the path the pipe escape in cellText exists for.
func monoCell(table, row, col int, text string) doc.Block {
	b := cell(table, row, col, true, text)
	b.Spans = []doc.Span{span(text, mono)}
	return b
}

func renderBlocks(t *testing.T, blocks ...doc.Block) string {
	t.Helper()
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: blocks}}}
	return String(d, DefaultOptions)
}

func TestTable(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "Key"), cell(1, 0, 1, true, "Value"),
		cell(1, 1, 0, false, "Type"), cell(1, 1, 1, false, "Page"),
	)
	want := "| Key | Value |\n| --- | --- |\n| Type | Page |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// The defect this whole file exists to prevent: nine cells emitted one at a time are
// nine paragraphs, which is what every sink in this package did before grouping.
func TestCellsDoNotBecomeParagraphs(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "A"), cell(1, 0, 1, true, "B"),
		cell(1, 1, 0, false, "c"), cell(1, 1, 1, false, "d"),
	)
	if strings.Contains(got, "\n\nc") {
		t.Errorf("a cell was emitted as its own block:\n%s", got)
	}
	if n := strings.Count(got, "\n"); n != 3 {
		t.Errorf("got %d lines, want 3 (header, delimiter, one body row):\n%s", n, got)
	}
}

// Two tables in sequence must not merge, which is the reason Cell.Table is a number
// rather than the cells being grouped by adjacency alone.
func TestAdjacentTablesStaySeparate(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "First"),
		cell(2, 0, 0, true, "Second"),
	)
	want := "| First |\n| --- |\n\n| Second |\n| --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// A nested table's cells arrive inside the outer table's run — 13 tables on disk do
// this — and grouping by adjacency would cut the outer table in two at that point. The
// inner table follows the outer one, which is the only order GFM can express.
func TestNestedTableFollowsRatherThanSplits(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "Outer A"), cell(1, 0, 1, true, "Outer B"),
		cell(2, 0, 0, true, "Inner"),
		cell(1, 1, 0, false, "one"), cell(1, 1, 1, false, "two"),
	)
	want := "| Outer A | Outer B |\n| --- | --- |\n| one | two |\n" +
		"\n| Inner |\n| --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// 46 of 788 tables on disk are ragged. A short GFM row renders with the missing cells
// simply absent, which reads as a broken table, so the row is padded.
func TestRaggedRowIsPadded(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "A"), cell(1, 0, 1, true, "B"), cell(1, 0, 2, true, "C"),
		cell(1, 1, 0, false, "only"),
	)
	want := "| A | B | C |\n| --- | --- | --- |\n| only |  |  |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// The 11 tables on disk whose first row declares no header. Promoting a data row would
// relabel data as a column name, so the header comes out empty instead — GFM requires
// something in that position.
func TestNoHeaderRowEmitsEmptyHeader(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, false, "a"), cell(1, 0, 1, false, "b"),
		cell(1, 1, 0, false, "c"), cell(1, 1, 1, false, "d"),
	)
	want := "|  |  |\n| --- | --- |\n| a | b |\n| c | d |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// 598 tables mark a whole first column as TH to give each row a row-header. GFM cannot
// mark that, so those cells are ordinary — but no row may be dropped or promoted for it.
func TestHeaderBelowRowZeroIsAnOrdinaryCell(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "H"), cell(1, 0, 1, true, "V"),
		cell(1, 1, 0, true, "Row name"), cell(1, 1, 1, false, "value"),
	)
	want := "| H | V |\n| --- | --- |\n| Row name | value |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// Under Write a table spanning a page break reaches this sink in two groups, one page at
// a time, so the second group's rows start at a nonzero Row. Rows are indexed by their
// sorted distinct values for exactly this reason: indexing by Row would pad the table
// with empty rows up to the first number seen.
func TestPageSplitTableDoesNotPadWithEmptyRows(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{
		{Number: 1, Blocks: []doc.Block{
			cell(1, 0, 0, true, "A"), cell(1, 0, 1, true, "B"),
		}},
		{Number: 2, Blocks: []doc.Block{
			cell(1, 7, 0, false, "c"), cell(1, 7, 1, false, "d"),
		}},
	}}
	got := String(d, DefaultOptions)
	want := "| A | B |\n| --- | --- |\n\n|  |  |\n| --- | --- |\n| c | d |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// Rows come out in row order regardless of the order their cells arrive in. Reading
// order puts them in order and nothing on disk does otherwise, so this pins the sort
// that the page-split case above needs but cannot detect the absence of: without it that
// test still passes, because its two groups already arrive ascending.
func TestRowsAreOrderedByRowNotByArrival(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 2, 0, false, "third"),
		cell(1, 0, 0, true, "head"),
		cell(1, 1, 0, false, "second"),
	)
	want := "| head |\n| --- |\n| second |\n| third |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// A cell with no Cell at all — a TD outside any Table, which sectionize leaves nil — is
// the untagged case and emits as a paragraph. A grid of one unplaced cell says less than
// the text does.
func TestUnpositionedCellEmitsAsParagraph(t *testing.T) {
	got := renderBlocks(t,
		doc.Block{Role: doc.RoleTableCell, Spans: []doc.Span{span("loose")}},
	)
	if got != "loose\n" {
		t.Errorf("got %q", got)
	}
}

// Inline markup is legal inside a cell and a bold column heading is what these documents
// draw, so emphasis survives.
func TestCellKeepsEmphasis(t *testing.T) {
	c := cell(1, 0, 0, true, "")
	c.Spans = []doc.Span{span("Bold", bold)}
	got := renderBlocks(t, c)
	if got != "| **Bold** |\n| --- |\n" {
		t.Errorf("got %q", got)
	}
}

// A cell's own leading space is trimmed, and it has to be trimmed *here* because nothing
// upstream can.
//
// extract carries an inferred space on the fragment that follows it, deliberately, so that
// trimming stays the sink's decision — and splitAtRules cuts a ruled row into cells at
// those same gaps, so every cell after the first begins with the space that separated it
// from its neighbour. On disk that is 11310 of 16724 rendered cells, so this is the common
// case rather than an edge one.
//
// What makes it invisible is that row() writes its own padding space, so the leak reads as
// "|  Table 29 |" — a second space that GFM collapses. Until this test the only thing that
// caught removing the trim was TestReferenceExactMatch/table two packages away, as a
// whole-document byte diff naming no cause.
//
// Both paths are asserted because they trim in different places: the plain one escapes and
// then trims, while a code span is emitted verbatim by writeCode, which pads a body that
// begins with a space rather than dropping it — so "` x`" survives escaping and the trim is
// what removes the space outside the fence.
func TestCellLeadingSpaceIsTrimmed(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "Object type"), cell(1, 0, 1, true, " Table 29"),
	)
	want := "| Object type | Table 29 |\n| --- | --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}

	got = renderBlocks(t, monoCell(1, 0, 0, " /Metadata"))
	want = "| `/Metadata` |\n| --- |\n"
	if got != want {
		t.Errorf("monospaced: got\n%q\nwant\n%q", got, want)
	}

	// Alt returns before the spans are ever read, so its trim is a separate statement.
	// Nothing on disk reaches it: sectionize sets Alt on 218 blocks across the 50 documents
	// and none of them is a cell, so dropping this trim is a mutation only this assertion
	// can catch. (Alt arrives from the structure tree, not from the page — extract sets it
	// on 0 blocks — so the tagged path is the only one that could reach it at all.)
	c := cell(1, 0, 0, true, "drawn")
	c.Alt = " corrected"
	got = renderBlocks(t, c)
	want = "| corrected |\n| --- |\n"
	if got != want {
		t.Errorf("alt: got\n%q\nwant\n%q", got, want)
	}
}

// A pipe inside a cell's text would otherwise become a cell boundary. escapeInto already
// emits "\|", which is what GFM reads as a literal pipe inside a cell — asserted here
// because the escaping is in another file and a change there breaks tables silently.
func TestPipeInCellIsEscaped(t *testing.T) {
	got := renderBlocks(t, cell(1, 0, 0, true, "a|b"))
	if !strings.Contains(got, `a\|b`) {
		t.Errorf("pipe not escaped: %q", got)
	}
	// Three pipes, not four: the row's own two delimiters plus the escaped one. A raw
	// pipe here would make the header two columns wide and the delimiter row disagree.
	if n := strings.Count(strings.SplitN(got, "\n", 2)[0], "|"); n != 3 {
		t.Errorf("escaped pipe still split the row: %q", got)
	}
}

// The pipe escapeInto never reaches is one inside a code span, and GFM splits a row into
// cells before it parses inline content — so a raw pipe there ends the cell even though a
// code span is otherwise literal. Before this was fixed the cell below emitted
// "| `a|b` | `second` |": four pipes for a two-column row, which reads as three cells
// against a two-column delimiter and drops "second" outright.
//
// Unreachable from the corpus, which is why this test is the only thing that can catch it:
// 13 table rows on disk hold a code span and none of them holds a pipe.
func TestPipeInMonospaceCellIsEscaped(t *testing.T) {
	got := renderBlocks(t,
		monoCell(1, 0, 0, "a|b"),
		monoCell(1, 0, 1, "second"),
	)
	want := "| `a\\|b` | `second` |\n| --- | --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// A backslash the document itself draws before a pipe in a code span must survive as a
// backslash, which is what one escape per pipe gets right and a parity check on the
// rendered cell got wrong: by then an escape and a literal backslash are the same byte.
// Verified against pandoc 3.9 -f gfm — "`a\\|b`" in a cell renders "<code>a\|b</code>",
// so emitting one added backslash here is what puts the document's own backslash on screen.
func TestLiteralBackslashInMonospaceCellSurvives(t *testing.T) {
	got := renderBlocks(t,
		monoCell(1, 0, 0, `a\|b`),
		monoCell(1, 0, 1, "second"),
	)
	want := "| `a\\\\|b` | `second` |\n| --- | --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// The plain-text path needs nothing extra: escapeInto escapes both the backslash and the
// pipe, and "\\\|" is what renders "a\|b". Asserted so the code-span escape is never
// generalized to a pass over the whole cell, which would escape these a second time.
func TestPipeInCellIsNotDoubleEscaped(t *testing.T) {
	got := renderBlocks(t, cell(1, 0, 0, true, "a|b"))
	want := "| a\\|b |\n| --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// Monospace outside a table keeps a pipe verbatim, since only the table-row split reads a
// pipe before inline parsing. This is the negative half of the cell flag: without it the
// escape would put a visible backslash into every code span in the corpus that holds a
// pipe, and a PDF specification draws plenty of them.
func TestPipeInMonospaceParagraphIsNotEscaped(t *testing.T) {
	b := para(span("a|b", mono))
	got := renderBlocks(t, b)
	if got != "`a|b`\n" {
		t.Errorf("got %q, want %q", got, "`a|b`\n")
	}
}

// Alt is the producer's statement of what a cell says where the glyphs do not spell it, and
// it wins over the spans here exactly as it does in a paragraph. This walker was the one that
// ignored it. Its pipe is escaped too, since Alt is a plain string and reaches the row
// through escapeInto rather than through any code span.
func TestCellPrefersAltOverSpans(t *testing.T) {
	c := cell(1, 0, 0, true, "glyphs")
	c.Alt = "a|b"
	got := renderBlocks(t, c)
	want := "| a\\|b |\n| --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// A cell is inline context, so the line-start characters are not live in it: a cell opening
// with "-" is a dash, not a list marker. content passes atStart true because a block can
// begin a line; a cell never can, and escaping there would put a backslash in front of every
// cell that starts with a dash — the corpus is full of them.
func TestAltCellDoesNotEscapeLineStartCharacters(t *testing.T) {
	c := cell(1, 0, 0, true, "glyphs")
	c.Alt = "-5 to +5"
	got := renderBlocks(t, c)
	want := "| -5 to +5 |\n| --- |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// A cell cannot contain a newline in GFM — the pipe row is the row — so a multi-line cell
// is joined onto one line rather than breaking the table.
func TestMultiLineCellIsJoined(t *testing.T) {
	got := renderBlocks(t, cell(1, 0, 0, true, "first\nsecond"))
	if got != "| first second |\n| --- |\n" {
		t.Errorf("got %q", got)
	}
}

// The blank-line accounting a table participates in like any other block: a paragraph on
// either side is separated, and the table does not open the document with a gap.
func TestTableSeparatedFromSurroundingBlocks(t *testing.T) {
	got := renderBlocks(t,
		para(span("Before.")),
		cell(1, 0, 0, true, "H"),
		para(span("After.")),
	)
	want := "Before.\n\n| H |\n| --- |\n\n| After. |\n"
	if got == want {
		t.Fatal("the paragraph after the table was swallowed into it")
	}
	want = "Before.\n\n| H |\n| --- |\n\nAfter.\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// An empty cell is skipped as a block everywhere else in this package, and skipping it
// must not shift the cells after it: the position comes from Cell, not from the cell's
// ordinal place in the list.
func TestEmptyCellLeavesAHoleRatherThanShifting(t *testing.T) {
	got := renderBlocks(t,
		cell(1, 0, 0, true, "A"), cell(1, 0, 1, true, "B"),
		cell(1, 1, 0, false, ""), cell(1, 1, 1, false, "d"),
	)
	want := "| A | B |\n| --- | --- |\n|  | d |\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// WriteBlocks is sink/okf's entry point and the fourth walker over a block list. It goes
// through the same grouping, because an OKF bundle emitting cells as paragraphs while the
// Markdown sink emits a table from the same extraction is the divergence this package's
// single-policy exports exist to prevent.
func TestWriteBlocksEmitsTables(t *testing.T) {
	blocks := []doc.Block{
		cell(1, 0, 0, true, "Key"), cell(1, 0, 1, true, "Value"),
		cell(1, 1, 0, false, "Type"), cell(1, 1, 1, false, "Page"),
	}
	var sb strings.Builder
	if err := WriteBlocks(&sb, blocks, DefaultOptions); err != nil {
		t.Fatal(err)
	}
	want := "| Key | Value |\n| --- | --- |\n| Type | Page |\n"
	if sb.String() != want {
		t.Errorf("got\n%q\nwant\n%q", sb.String(), want)
	}
}

// Routing WriteBlocks through the grouping moved its artifact and empty-block skipping
// into blocks, so the skips are asserted here on a list that mixes all of it — an
// artifact, an empty block, a table, and prose either side.
func TestWriteBlocksMixedContent(t *testing.T) {
	blocks := []doc.Block{
		{Role: doc.RoleArtifact, Spans: []doc.Span{span("Page 412")}},
		para(span("Before.")),
		para(),
		cell(1, 0, 0, true, "H"), cell(1, 0, 1, true, "V"),
		cell(1, 1, 0, false, "a"), cell(1, 1, 1, false, "b"),
		para(span("After.")),
	}
	var sb strings.Builder
	if err := WriteBlocks(&sb, blocks, DefaultOptions); err != nil {
		t.Fatal(err)
	}
	want := "Before.\n\n| H | V |\n| --- | --- |\n| a | b |\n\nAfter.\n"
	if sb.String() != want {
		t.Errorf("got\n%q\nwant\n%q", sb.String(), want)
	}
}

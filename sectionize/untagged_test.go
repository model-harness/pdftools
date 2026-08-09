package sectionize

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// blocks builds a document from (page, role, level, text) tuples, one block each.
//
// One block per tuple rather than one per page, which is the opposite of docWith's
// choice and correct for this path: Untagged reads blocks and not marked content, so a
// fixture that fused a heading into the paragraph after it would be testing extract's
// segmentation rather than this function. The role is what a fused block would lack.
func blocks(bs ...blk) *doc.Document {
	d := &doc.Document{Meta: doc.Metadata{Path: "test.pdf"}}
	byPage := map[int]int{}
	for _, b := range bs {
		pi, ok := byPage[b.page]
		if !ok {
			d.Pages = append(d.Pages, doc.Page{Number: b.page})
			pi = len(d.Pages) - 1
			byPage[b.page] = pi
		}
		role := b.role
		if role == "" {
			role = doc.RoleParagraph
		}
		d.Pages[pi].Blocks = append(d.Pages[pi].Blocks, doc.Block{
			Role:  role,
			Level: b.level,
			Spans: []doc.Span{{Text: b.text, MCID: -1}},
		})
	}
	return d
}

type blk struct {
	page  int
	role  doc.Role
	level int
	text  string
}

// head is a heading block at the given level, which is what layout.Headings produces.
func head(page, level int, text string) blk {
	return blk{page: page, role: doc.RoleHeading, level: level, text: text}
}

// para is a body block, which is what extract leaves every block as.
func para(page int, text string) blk {
	return blk{page: page, role: doc.RoleParagraph, text: text}
}

// TestUntaggedNestsByBlockLevel is the claim the untagged path rests on: the identical level
// stack that ranks a tree's H1..H6 elements ranks layout's levelled blocks, so a document
// with no structure tree still yields a real tree of clauses.
func TestUntaggedNestsByBlockLevel(t *testing.T) {
	d := blocks(
		head(1, 1, "1 Scope"),
		para(1, "Scope body."),
		head(1, 2, "1.1 First"),
		para(1, "First body."),
		head(1, 2, "1.2 Second"),
		para(1, "Second body."),
		head(2, 1, "2 Terms"),
		para(2, "Terms body."),
	)

	out, st := Untagged(d, DefaultOptions)

	if len(out.Sections) != 2 {
		t.Fatalf("roots = %d, want 2: %v", len(out.Sections), titles(out.Sections))
	}
	scope := out.Sections[0]
	if scope.Title != "1 Scope" || scope.Level != 1 || scope.Number != "1" {
		t.Errorf("scope = %+v", scope)
	}
	if len(scope.Kids) != 2 {
		t.Fatalf("scope kids = %v, want 2", titles(scope.Kids))
	}
	// The second level-2 heading closes the first rather than nesting inside it, which is
	// the whole rule and the reason a levelled sequence is enough.
	if scope.Kids[0].Title != "1.1 First" || scope.Kids[1].Title != "1.2 Second" {
		t.Errorf("kids = %v", titles(scope.Kids))
	}
	if len(scope.Kids[0].Kids) != 0 {
		t.Errorf("1.1 should not contain 1.2: %v", titles(scope.Kids[0].Kids))
	}
	if scope.Kids[0].Parent != scope {
		t.Error("parent back-pointer not set")
	}
	if out.Sections[1].Title != "2 Terms" || out.Sections[1].Level != 1 {
		t.Errorf("second root = %+v", out.Sections[1])
	}

	// Each section holds its own body and not its children's.
	if got := scope.Text(); got != "Scope body." {
		t.Errorf("scope text = %q", got)
	}
	if got := scope.Kids[1].Text(); got != "Second body." {
		t.Errorf("1.2 text = %q", got)
	}

	if st.Sections != 4 || st.Titled != 4 || st.Numbered != 4 || st.MaxLevel != 2 {
		t.Errorf("stats = %+v", st)
	}
	if st.Blocks != 4 {
		t.Errorf("blocks = %d, want 4: a heading is a title, not a block", st.Blocks)
	}
}

// TestUntaggedHeadingIsNotAlsoContent pins the one way this path could double a document's
// text. Tagged consumes a heading's spans before opening the section, so the title cannot
// reappear as a paragraph; here the block is simply not placed, and forgetting that would
// emit every heading twice — once as the clause's name and once as its first line.
func TestUntaggedHeadingIsNotAlsoContent(t *testing.T) {
	d := blocks(head(1, 1, "1 Scope"), para(1, "Body."))

	out, _ := Untagged(d, DefaultOptions)

	sec := out.Sections[0]
	if got := sec.Text(); got != "Body." {
		t.Errorf("section text = %q, want %q: the heading was placed as content too", got, "Body.")
	}
	for i := range sec.Blocks {
		if sec.Blocks[i].Role == doc.RoleHeading {
			t.Error("a heading block reached the section's content")
		}
	}
}

// TestUntaggedPreambleHoldsContentBeforeFirstHeading covers the shape every scanned and
// unnumbered document takes: content with no heading in front of it belongs to no clause, and
// dropping it would lose a title page.
func TestUntaggedPreambleHoldsContentBeforeFirstHeading(t *testing.T) {
	d := blocks(
		para(1, "MuPDF Explored"),
		para(1, "Robin Watts"),
		head(1, 1, "1 Introduction"),
		para(1, "Body."),
	)

	out, st := Untagged(d, DefaultOptions)

	if len(out.Preamble) != 2 {
		t.Fatalf("preamble = %d blocks, want 2", len(out.Preamble))
	}
	if out.Preamble[0].Text() != "MuPDF Explored" {
		t.Errorf("preamble[0] = %q", out.Preamble[0].Text())
	}
	if st.Blocks != 3 {
		t.Errorf("blocks = %d, want 3: two preamble plus one body", st.Blocks)
	}
}

// TestUntaggedWithNoHeadingsYieldsNoSections is what the okf verb's guard reads. 25 of the
// documents on disk reach this — scans, invoices, single-table fixtures — and a bundle built
// from an outline with no sections would be one document claiming to be a knowledge base.
func TestUntaggedWithNoHeadingsYieldsNoSections(t *testing.T) {
	d := blocks(para(1, "Some text."), para(2, "More text."))

	out, st := Untagged(d, DefaultOptions)

	if len(out.Sections) != 0 {
		t.Errorf("sections = %d, want 0", len(out.Sections))
	}
	if len(out.Preamble) != 2 || st.Blocks != 2 {
		t.Errorf("preamble = %d blocks, stats = %+v", len(out.Preamble), st)
	}
	if out.Meta.Path != "test.pdf" {
		t.Errorf("metadata not carried: %+v", out.Meta)
	}
}

// TestUntaggedPlacesEveryBlock is the accounting this path can make exact where the tagged
// one cannot. Tagged joins on (page, MCID) and reports what the join missed; there is no
// join here, so every non-empty block is either a title or placed content and Unplaced is
// always empty. A block silently dropped would be text lost from a knowledge bundle.
func TestUntaggedPlacesEveryBlock(t *testing.T) {
	d := blocks(
		para(1, "Front matter."),
		head(1, 1, "1 One"),
		para(1, "One body."),
		blk{page: 1, role: doc.RoleListItem, text: "An item."},
		blk{page: 2, role: doc.RoleTableCell, text: "A cell."},
		head(2, 3, "1.1.1 Deep"),
		blk{page: 2, role: doc.RoleFigure, text: "A figure."},
	)

	out, st := Untagged(d, DefaultOptions)

	if st.UnplacedBlocks != 0 || st.UnplacedChars != 0 || len(out.Unplaced) != 0 {
		t.Errorf("unplaced on a path with no join: %+v %d pages", st, len(out.Unplaced))
	}
	// 7 blocks in, 2 of them headings, so 5 placed. Nothing is dropped for having a role
	// this path does not inspect.
	if st.Blocks != 5 {
		t.Errorf("blocks = %d, want 5", st.Blocks)
	}
	var text []string
	for _, b := range out.Preamble {
		text = append(text, b.Text())
	}
	out.Walk(func(s *doc.Section) bool {
		for i := range s.Blocks {
			text = append(text, s.Blocks[i].Text())
		}
		return true
	})
	got := strings.Join(text, "|")
	const want = "Front matter.|One body.|An item.|A cell.|A figure."
	if got != want {
		t.Errorf("placed text = %q, want %q", got, want)
	}
}

// TestUntaggedRolesSurvive checks that a block reaches its section unchanged. Untagged reads
// Role to find headings and must not otherwise touch it: a sink renders a list item as a
// bullet and a cell as a table row from exactly this field, so a role rewritten here would
// flatten inference's output right after layout produced it.
func TestUntaggedRolesSurvive(t *testing.T) {
	d := blocks(
		head(1, 1, "1 One"),
		blk{page: 1, role: doc.RoleListItem, level: 2, text: "An item."},
		blk{page: 1, role: doc.RoleCode, text: "code()"},
	)

	out, _ := Untagged(d, DefaultOptions)

	bs := out.Sections[0].Blocks
	if len(bs) != 2 {
		t.Fatalf("section blocks = %d, want 2", len(bs))
	}
	if bs[0].Role != doc.RoleListItem || bs[0].Level != 2 {
		t.Errorf("list item arrived as %s level %d", bs[0].Role, bs[0].Level)
	}
	if bs[1].Role != doc.RoleCode {
		t.Errorf("code block arrived as %s", bs[1].Role)
	}
}

// TestUntaggedSectionPagesSpanItsBody pins the page range, which is what a reader checking a
// conversion against the original uses. A clause opening on page 24 and running onto 25 must
// report both, and the heading's own page must anchor a clause whose body is empty.
func TestUntaggedSectionPagesSpanItsBody(t *testing.T) {
	d := blocks(
		head(24, 2, "5.2 Creation"),
		para(24, "First."),
		para(25, "Continues."),
		head(26, 2, "5.3 Empty"),
	)

	out, _ := Untagged(d, DefaultOptions)

	creation := out.Sections[0]
	if creation.FirstPage != 24 || creation.LastPage != 25 {
		t.Errorf("5.2 pages = %d-%d, want 24-25", creation.FirstPage, creation.LastPage)
	}
	// A clause with no body still says where its heading was, rather than reporting 0.
	empty := out.Sections[1]
	if empty.FirstPage != 26 || empty.LastPage != 26 {
		t.Errorf("5.3 pages = %d-%d, want 26-26", empty.FirstPage, empty.LastPage)
	}
}

// TestUntaggedTitleIsCleanedAndBounded covers the two transforms a title needs before it can
// be a filename or a YAML value, and they are the tagged path's own so that a clause is named
// identically whichever path found it.
func TestUntaggedTitleIsCleanedAndBounded(t *testing.T) {
	// A clause number separated from its text by a tab, with a trailing space — which is
	// how nearly every heading comes off a page.
	d := blocks(head(1, 1, "7.4\tFilters \n"), para(1, "Body."))
	out, _ := Untagged(d, DefaultOptions)
	if got := out.Sections[0].Title; got != "7.4 Filters" {
		t.Errorf("title = %q, want %q", got, "7.4 Filters")
	}
	if got := out.Sections[0].Number; got != "7.4" {
		t.Errorf("number = %q, want %q", got, "7.4")
	}

	// And the bound, which exists because a block layout fused into a heading is prose.
	long := "1 " + strings.Repeat("word ", 200)
	out, _ = Untagged(blocks(head(1, 1, long)), DefaultOptions)
	if got := out.Sections[0].Title; len(got) > DefaultOptions.MaxTitle {
		t.Errorf("title is %d bytes, over the %d bound", len(got), DefaultOptions.MaxTitle)
	}
}

// TestUntaggedUnrankedHeadingStaysInTheOutline covers a heading whose producer named it
// without ranking it. layout never emits one — it assigns a level or does not promote — but
// doctags reads a recognition model's output, and dropping such a heading would lose the
// clause and every block under it.
func TestUntaggedUnrankedHeadingStaysInTheOutline(t *testing.T) {
	d := blocks(blk{page: 1, role: doc.RoleHeading, level: 0, text: "Introduction"}, para(1, "Body."))

	out, st := Untagged(d, DefaultOptions)

	if len(out.Sections) != 1 {
		t.Fatalf("sections = %d, want 1: an unranked heading was dropped", len(out.Sections))
	}
	if out.Sections[0].Level != 1 {
		t.Errorf("level = %d, want 1", out.Sections[0].Level)
	}
	if got := out.Sections[0].Text(); got != "Body." {
		t.Errorf("body = %q: the clause lost its content", got)
	}
	if st.Titled != 1 || st.Numbered != 0 {
		t.Errorf("stats = %+v: an unnumbered heading is titled and not numbered", st)
	}
}

// TestUntaggedDeeperFirstHeadingStillRoots covers a document that opens at a level below 1,
// which mupdf_explored.pdf does — its first promoted heading is "1.1 What is MuPDF?" at level
// 2, because the chapter title above it is unnumbered. The clause must still be a root.
func TestUntaggedDeeperFirstHeadingStillRoots(t *testing.T) {
	d := blocks(head(1, 2, "1.1 What is MuPDF?"), para(1, "Body."), head(1, 3, "1.1.1 Detail"))

	out, _ := Untagged(d, DefaultOptions)

	if len(out.Sections) != 1 {
		t.Fatalf("roots = %d, want 1: %v", len(out.Sections), titles(out.Sections))
	}
	if out.Sections[0].Level != 2 {
		t.Errorf("root level = %d, want 2: the declared level is kept, not renumbered", out.Sections[0].Level)
	}
	if len(out.Sections[0].Kids) != 1 {
		t.Errorf("the level-3 heading did not nest: %v", titles(out.Sections[0].Kids))
	}
}

// TestUntaggedEmptyBlocksAreDropped matches the tagged path: a positioned rectangle a
// producer left behind must not become a block, and an empty heading block must not open a
// clause nothing can name.
func TestUntaggedEmptyBlocksAreDropped(t *testing.T) {
	d := blocks(
		head(1, 1, "1 Scope"),
		para(1, "   "),
		para(1, "Real body."),
		blk{page: 1, role: doc.RoleHeading, level: 1, text: "  "},
	)

	out, st := Untagged(d, DefaultOptions)

	if st.Blocks != 1 {
		t.Errorf("blocks = %d, want 1", st.Blocks)
	}
	if len(out.Sections) != 1 {
		t.Errorf("sections = %d, want 1: an empty heading opened a clause", len(out.Sections))
	}
}

// TestUntaggedOutlineDoesNotAliasTheDocument pins the independence a Tagged outline has for
// free and this one has to be given. emitItem builds a tagged block's spans from the elements'
// own and unplaced copies each survivor by value, so that outline shares no storage with the
// document; Untagged takes the extractor's blocks whole, which is what carries their roles and
// boxes across, and a struct copy shares the array behind Spans.
//
// The mutation used here is the one the repo actually performs: StripMarker edits Span.Text in
// place, and layout.Lists calls it. okf runs inference before sectionizing, so nothing hits
// this today — which is exactly why it is worth a test rather than a comment, since the
// ordering that makes it safe is one call site's and not the type's.
func TestUntaggedOutlineDoesNotAliasTheDocument(t *testing.T) {
	d := blocks(head(1, 1, "1 Scope"), para(1, "— an item."))

	out, _ := Untagged(d, DefaultOptions)

	// StripMarker rather than a bare assignment: this is the mutation on disk, and it edits
	// the span the outline would be sharing.
	if !d.Pages[0].Blocks[1].StripMarker() {
		t.Fatal("StripMarker did not fire on a block that opens with a marker glyph")
	}
	if got := out.Sections[0].Text(); got != "— an item." {
		t.Errorf("outline text = %q after the document was edited, want %q: the section's block "+
			"shares span storage with the document's", got, "— an item.")
	}
	if out.Sections[0].Blocks[0].Marker != "" {
		t.Error("the document's marker reached the outline's block")
	}
}

func TestUntaggedNilDocumentIsEmpty(t *testing.T) {
	out, st := Untagged(nil, DefaultOptions)
	if out == nil {
		t.Fatal("nil outline")
	}
	if len(out.Sections) != 0 || st.Sections != 0 {
		t.Errorf("outline = %+v stats = %+v", out, st)
	}
}

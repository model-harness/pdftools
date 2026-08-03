package sectionize

import (
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/doc"
	"github.com/3rg0n/pdf-spec/tag"
)

// sp is one extracted run: the page it was drawn on, the marked-content sequence it
// was drawn inside, and its text.
type sp struct {
	page int
	mcid int
	text string
}

// docWith builds a document from runs, putting every run on a page into a single
// block.
//
// One block per page rather than one per run, deliberately: that is the layout the
// extractor actually produces — its paragraph heuristic merges a heading line with the
// body line after it when they share style and spacing — and it is the shape that
// makes a block-level join over-capture. A test fixture with one block per run would
// pass under either join and prove nothing.
func docWith(runs ...sp) *doc.Document {
	d := &doc.Document{Meta: doc.Metadata{Path: "test.pdf", Tagged: true}}
	byPage := map[int]*doc.Page{}
	for _, r := range runs {
		p := byPage[r.page]
		if p == nil {
			d.Pages = append(d.Pages, doc.Page{Number: r.page, Blocks: []doc.Block{{Role: doc.RoleParagraph}}})
			p = &d.Pages[len(d.Pages)-1]
			byPage[r.page] = p
		}
		blk := &p.Blocks[0]
		blk.Spans = append(blk.Spans, doc.Span{Text: r.text, MCID: r.mcid})
		blk.MCIDs = append(blk.MCIDs, r.mcid)
	}
	return d
}

// el builds an element owning the given marked-content identifiers, all on one page.
func el(role tag.Role, page int, mcids ...int) *tag.Elem {
	e := &tag.Elem{Role: role, RawType: role, Page: page}
	for _, m := range mcids {
		e.Content = append(e.Content, tag.MCRef{MCID: m, Page: page})
	}
	return e
}

// kids attaches children to a parent, setting the back-pointers Elem.Depth and
// listDepth both walk.
func kids(parent *tag.Elem, cs ...*tag.Elem) *tag.Elem {
	for _, c := range cs {
		c.Parent = parent
		parent.Kids = append(parent.Kids, c)
	}
	return parent
}

// tree wraps roots in a structure tree root, which carries no role of its own.
func tree(cs ...*tag.Elem) *tag.Tree {
	return &tag.Tree{Root: kids(&tag.Elem{}, cs...)}
}

func TestLevelStackNestsByHeadingRank(t *testing.T) {
	// The core of the package: hierarchy comes from the *sequence* of heading levels,
	// not from containment. Every element below is a sibling in one flat list, which is
	// how ISO 32000-2 is actually tagged — one Part holding 13,442 direct children.
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "Scope body."},
		sp{1, 2, "1.1 First"},
		sp{1, 3, "First body."},
		sp{1, 4, "1.2 Second"},
		sp{1, 5, "Second body."},
		sp{1, 6, "2 Terms"},
		sp{1, 7, "Terms body."},
	)
	tr := tree(
		el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1),
		el(tag.RoleH2, 1, 2), el(tag.RoleP, 1, 3),
		el(tag.RoleH2, 1, 4), el(tag.RoleP, 1, 5),
		el(tag.RoleH1, 1, 6), el(tag.RoleP, 1, 7),
	)

	out, st := Tagged(d, tr, DefaultOptions)

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
	// The H2s nest under the preceding H1 even though nothing contains them, and the
	// second H2 closes the first rather than nesting inside it.
	if scope.Kids[0].Title != "1.1 First" || scope.Kids[1].Title != "1.2 Second" {
		t.Errorf("kids = %v", titles(scope.Kids))
	}
	if scope.Kids[0].Parent != scope {
		t.Error("parent back-pointer not set")
	}
	if len(scope.Kids[0].Kids) != 0 {
		t.Errorf("1.1 should not contain 1.2: %v", titles(scope.Kids[0].Kids))
	}
	// A following H1 closes both open levels, so "2 Terms" is a root.
	if out.Sections[1].Title != "2 Terms" || out.Sections[1].Level != 1 {
		t.Errorf("second root = %+v", out.Sections[1])
	}

	// Each section holds only its own body.
	if got := scope.Text(); got != "Scope body." {
		t.Errorf("scope text = %q", got)
	}
	if got := scope.Kids[0].Text(); got != "First body." {
		t.Errorf("1.1 text = %q", got)
	}

	if st.Sections != 4 || st.Titled != 4 || st.Numbered != 4 || st.MaxLevel != 2 {
		t.Errorf("stats = %+v", st)
	}
	if st.Blocks != 4 {
		t.Errorf("blocks = %d, want 4", st.Blocks)
	}
	if st.UnplacedBlocks != 0 || st.UnplacedChars != 0 {
		t.Errorf("unexpected unplaced: %+v", st)
	}
}

func TestContainersAreTransparent(t *testing.T) {
	// The failure this package exists to avoid. Treating a container as a section
	// would emit one section from a document with three clauses, because a Sect can
	// hold every clause beneath it. Containers contribute nesting only.
	d := docWith(
		sp{1, 0, "1 One"}, sp{1, 1, "One body."},
		sp{1, 2, "2 Two"}, sp{1, 3, "Two body."},
	)
	part := kids(el(tag.RolePart, 1),
		kids(el(tag.RoleSect, 1), el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1)),
		kids(el(tag.RoleDiv, 1), el(tag.RoleH1, 1, 2), el(tag.RoleP, 1, 3)),
	)
	tr := &tag.Tree{Root: kids(&tag.Elem{Role: tag.RoleDocument}, part)}

	out, st := Tagged(d, tr, DefaultOptions)

	if st.Sections != 2 {
		t.Fatalf("sections = %d, want 2 (one per heading, not per container)", st.Sections)
	}
	if out.Sections[0].Title != "1 One" || out.Sections[1].Title != "2 Two" {
		t.Errorf("sections = %v", titles(out.Sections))
	}
	// Crossing a container boundary does not reset the level stack: the second H1 is a
	// sibling of the first, not a child of its own Div.
	if len(out.Sections[0].Kids) != 0 {
		t.Errorf("container nesting leaked into the outline: %v", titles(out.Sections[0].Kids))
	}
}

func TestInlineElementsDoNotSplitAParagraph(t *testing.T) {
	// A Span or Link inside a P is inline. Emitting a block for it would split the
	// paragraph at every italic word and every cross-reference — on a specification,
	// at thousands of them.
	d := docWith(
		sp{1, 0, "H"},
		sp{1, 1, "Text with "},
		sp{1, 2, "emphasis"},
		sp{1, 3, " and a "},
		sp{1, 4, "link"},
		sp{1, 5, " inside."},
	)
	p := kids(el(tag.RoleP, 1, 1),
		el(tag.RoleSpan, 1, 2),
	)
	// The P's own runs are gathered before its children's, so build the tail as
	// further inline children rather than as more of the P's own content.
	kids(p, el(tag.RoleSpan, 1, 3), el(tag.RoleLink, 1, 4), el(tag.RoleSpan, 1, 5))
	tr := tree(el(tag.RoleH1, 1, 0), p)

	out, st := Tagged(d, tr, DefaultOptions)

	if st.Blocks != 1 {
		t.Fatalf("blocks = %d, want 1: inline elements split the paragraph", st.Blocks)
	}
	sec := out.Sections[0]
	if got := sec.Text(); got != "Text with emphasis and a link inside." {
		t.Errorf("text = %q", got)
	}
	// Style boundaries survive as spans even though the block is one unit.
	if n := len(sec.Blocks[0].Spans); n != 5 {
		t.Errorf("spans = %d, want 5", n)
	}
}

func TestTitleJoinIsSpanLevelNotBlockLevel(t *testing.T) {
	// The heading and the body share one extracted block, which is the 12%-of-headings
	// case on Well-Tagged-PDF-WTPDF-1.0.pdf. A join on doc.Block.MCIDs resolves the
	// title to the heading plus the definition after it; a join on doc.Span.MCID does
	// not, because the extractor starts a new span at every MCID change.
	d := docWith(
		sp{1, 0, "4.1 artifact marked content sequence"},
		sp{1, 1, "marked content sequence tagged as an artifact"},
	)
	tr := tree(el(tag.RoleH3, 1, 0), el(tag.RoleP, 1, 1))

	out, _ := Tagged(d, tr, DefaultOptions)

	sec := out.Sections[0]
	if sec.Title != "4.1 artifact marked content sequence" {
		t.Errorf("title over-captured: %q", sec.Title)
	}
	// And the heading text is claimed, so it does not reappear at the top of the body.
	if got := sec.Text(); got != "marked content sequence tagged as an artifact" {
		t.Errorf("body = %q", got)
	}
}

func TestContentSpanningPagesUsesPerReferencePages(t *testing.T) {
	// A paragraph continuing past a page break is one element whose marked-content
	// references name two pages. An MCID is unique only within a page, so joining on
	// the element's own page alone drops the continuation — and MCID 0 on page 2 is
	// different content entirely.
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "A sentence that begins here "},
		sp{2, 0, "and finishes on the next page."},
	)
	p := el(tag.RoleP, 1, 1)
	p.Content = append(p.Content, tag.MCRef{MCID: 0, Page: 2})
	tr := tree(el(tag.RoleH1, 1, 0), p)

	out, st := Tagged(d, tr, DefaultOptions)

	sec := out.Sections[0]
	if got := sec.Text(); got != "A sentence that begins here and finishes on the next page." {
		t.Errorf("text = %q: cross-page content lost", got)
	}
	if sec.FirstPage != 1 || sec.LastPage != 2 {
		t.Errorf("pages = %d-%d, want 1-2", sec.FirstPage, sec.LastPage)
	}
	if st.UnplacedChars != 0 {
		t.Errorf("unplaced = %d chars, want 0", st.UnplacedChars)
	}
}

func TestUnreferencedContentReachesUnplaced(t *testing.T) {
	// ISO 32000-2 draws the whole of clause 1 outside any marked-content sequence, so
	// no structure element names it. Dropping that loses a normative clause; attaching
	// it to the preceding section files the Scope under the wrong clause. Keeping it
	// unattributed is the only honest option.
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "Claimed body."},
		sp{2, 7, "Text no element references."},
		sp{2, -1, "Drawn outside any marked content."},
	)
	tr := tree(el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1))

	out, st := Tagged(d, tr, DefaultOptions)

	if len(out.Unplaced) != 1 {
		t.Fatalf("unplaced pages = %d, want 1", len(out.Unplaced))
	}
	pg := out.Unplaced[0]
	if pg.Number != 2 {
		t.Errorf("unplaced page number = %d, want 2", pg.Number)
	}
	if got := pg.Text(); got != "Text no element references.Drawn outside any marked content." {
		t.Errorf("unplaced text = %q", got)
	}
	if st.UnplacedBlocks != 1 {
		t.Errorf("unplaced blocks = %d, want 1", st.UnplacedBlocks)
	}
	// Nothing the tree did claim is repeated there. A block is rebuilt from its
	// unclaimed spans rather than taken whole for exactly this reason.
	for _, p := range out.Unplaced {
		if strings.Contains(p.Text(), "Claimed body.") {
			t.Error("unplaced repeats text a section already holds")
		}
	}
	// Page 1's block was partly claimed, so it must not appear at all: both of its
	// spans went to the outline.
	if len(out.Unplaced) > 0 && out.Unplaced[0].Number == 1 {
		t.Error("fully claimed page reported as unplaced")
	}
}

func TestNoTextIsLost(t *testing.T) {
	// The accounting invariant, and the reason Unplaced exists: every character the
	// extractor produced is either in a section, in the preamble, or in Unplaced. It
	// held at 0.000% on all four corpus files; this pins it without them.
	d := docWith(
		sp{1, 0, "Front matter."},
		sp{1, 1, "1 Scope"},
		sp{1, 2, "Body."},
		sp{2, 0, "Orphan."},
		sp{2, -1, "Untagged."},
	)
	tr := tree(el(tag.RoleP, 1, 0), el(tag.RoleH1, 1, 1), el(tag.RoleP, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	var got strings.Builder
	for _, b := range out.Preamble {
		got.WriteString(b.Text())
	}
	out.Walk(func(s *doc.Section) bool {
		got.WriteString(s.Title)
		got.WriteString(s.Text())
		return true
	})
	for _, p := range out.Unplaced {
		got.WriteString(p.Text())
	}

	var want strings.Builder
	for _, p := range d.Pages {
		want.WriteString(p.Text())
	}
	if missing := missingRunes(want.String(), got.String()); missing != "" {
		t.Errorf("text lost: %q", missing)
	}
}

func TestPreambleHoldsContentBeforeFirstHeading(t *testing.T) {
	d := docWith(
		sp{1, 0, "ISO 32000-2"},
		sp{1, 1, "Second edition"},
		sp{1, 2, "1 Scope"},
		sp{1, 3, "Body."},
	)
	tr := tree(el(tag.RoleP, 1, 0), el(tag.RoleP, 1, 1), el(tag.RoleH1, 1, 2), el(tag.RoleP, 1, 3))

	out, st := Tagged(d, tr, DefaultOptions)

	if len(out.Preamble) != 2 {
		t.Fatalf("preamble = %d blocks, want 2", len(out.Preamble))
	}
	if out.Preamble[0].Text() != "ISO 32000-2" {
		t.Errorf("preamble[0] = %q", out.Preamble[0].Text())
	}
	// Preamble blocks count toward Blocks: they are placed content, not lost content.
	// Three, not four — a heading becomes a section title, not a block.
	if st.Blocks != 3 {
		t.Errorf("blocks = %d, want 3", st.Blocks)
	}
}

func TestNilTreeYieldsWholeDocumentAsPreamble(t *testing.T) {
	// An untagged file is not an error here; it means this path does not apply, and
	// the layout path will produce the headings.
	d := docWith(sp{1, 0, "Some text."}, sp{2, 0, "More text."})

	out, st := Tagged(d, nil, DefaultOptions)

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

func TestNilDocumentIsEmpty(t *testing.T) {
	out, st := Tagged(nil, tree(el(tag.RoleH1, 1, 0)), DefaultOptions)
	if out == nil {
		t.Fatal("nil outline")
	}
	if len(out.Sections) != 0 || st.Sections != 0 {
		t.Errorf("outline = %+v stats = %+v", out, st)
	}
}

func TestEmptyBlocksAreDropped(t *testing.T) {
	// A positioned rectangle a producer left behind, or an element whose content
	// resolved to whitespace. Every stage from here on would have to skip it.
	d := docWith(sp{1, 0, "1 Scope"}, sp{1, 1, "   "}, sp{1, 2, "Real body."})
	tr := tree(el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 2))

	out, st := Tagged(d, tr, DefaultOptions)

	if st.Blocks != 1 {
		t.Errorf("blocks = %d, want 1", st.Blocks)
	}
	if n := len(out.Sections[0].Blocks); n != 1 {
		t.Errorf("section blocks = %d, want 1", n)
	}
}

func TestTitleSourcePrecedence(t *testing.T) {
	// /T first when a producer filled it in, then the glyphs, then /ActualText. The
	// order of the last two is deliberate: /ActualText substitutes for what the glyphs
	// spell, and where both exist the glyphs are what a reader checking the conversion
	// sees on the page.
	d := docWith(
		sp{1, 0, "glyphs for the first"},
		sp{1, 1, "glyphs for the second"},
	)
	fromT := el(tag.RoleH1, 1, 0)
	fromT.Title = "the /T value"
	fromGlyphs := el(tag.RoleH1, 1, 1)
	fromGlyphs.ActualText = "the /ActualText value"
	fromActual := el(tag.RoleH1, 1)
	fromActual.ActualText = "only /ActualText"
	tr := tree(fromT, fromGlyphs, fromActual)

	out, st := Tagged(d, tr, DefaultOptions)

	want := []string{"the /T value", "glyphs for the second", "only /ActualText"}
	if got := titles(out.Sections); !equal(got, want) {
		t.Errorf("titles = %v, want %v", got, want)
	}
	if st.Titled != 3 {
		t.Errorf("titled = %d, want 3", st.Titled)
	}
}

func TestTitleTruncatedAtWordBoundary(t *testing.T) {
	// A heading is short. A title in the hundreds of characters means the join picked
	// up something that is not the heading, most often a producer that put a whole
	// paragraph in one marked-content sequence. Truncating keeps the clause; dropping
	// the title would leave a section nothing can name.
	long := "7.5.8 " + strings.Repeat("word ", 40)
	d := docWith(sp{1, 0, long})
	tr := tree(el(tag.RoleH1, 1, 0))

	out, _ := Tagged(d, tr, Options{MaxTitle: 30})

	got := out.Sections[0].Title
	if len(got) > 30 {
		t.Errorf("title = %q (%d bytes), want <= 30", got, len(got))
	}
	if strings.HasSuffix(got, " ") || strings.HasSuffix(got, "wor") {
		t.Errorf("title not cut at a word boundary: %q", got)
	}
	// Truncation must not cost the clause number, which is what a cross-reference and
	// a stable URI are built from.
	if out.Sections[0].Number != "7.5.8" {
		t.Errorf("number = %q", out.Sections[0].Number)
	}
}

func TestTruncateKeepsRuneBoundaries(t *testing.T) {
	// Cutting at a byte offset inside a multi-byte rune produces invalid UTF-8, which
	// a YAML value and a filename both reject.
	d := docWith(sp{1, 0, strings.Repeat("é", 20)})
	tr := tree(el(tag.RoleH1, 1, 0))

	out, _ := Tagged(d, tr, Options{MaxTitle: 9})

	got := out.Sections[0].Title
	if !strings.HasPrefix(strings.Repeat("é", 20), got) || len(got) > 9 {
		t.Errorf("title = %q (%d bytes)", got, len(got))
	}
	for _, r := range got {
		if r != 'é' {
			t.Fatalf("title contains a broken rune: %q", got)
		}
	}
}

func TestZeroMaxTitleUsesDefault(t *testing.T) {
	d := docWith(sp{1, 0, strings.Repeat("x", 400)})
	tr := tree(el(tag.RoleH1, 1, 0))
	out, _ := Tagged(d, tr, Options{})
	if n := len(out.Sections[0].Title); n != DefaultOptions.MaxTitle {
		t.Errorf("title = %d bytes, want the default %d", n, DefaultOptions.MaxTitle)
	}
}

func TestBareHTakesLevelFromNesting(t *testing.T) {
	// ISO 32000-2 §14.8.4.4. Documents that use H throughout are otherwise a flat list
	// of same-level sections.
	d := docWith(sp{1, 0, "Outer"}, sp{1, 1, "Inner"}, sp{1, 2, "Inner body."})
	inner := kids(el(tag.RoleSect, 1), el(tag.RoleH, 1, 1), el(tag.RoleP, 1, 2))
	outer := kids(el(tag.RoleSect, 1), el(tag.RoleH, 1, 0), inner)
	tr := tree(outer)

	out, st := Tagged(d, tr, DefaultOptions)

	if len(out.Sections) != 1 {
		t.Fatalf("roots = %v, want 1", titles(out.Sections))
	}
	if out.Sections[0].Level != 1 {
		t.Errorf("outer level = %d, want 1", out.Sections[0].Level)
	}
	if len(out.Sections[0].Kids) != 1 || out.Sections[0].Kids[0].Level != 2 {
		t.Fatalf("inner not nested at level 2: %+v", out.Sections[0].Kids)
	}
	if st.MaxLevel != 2 {
		t.Errorf("maxlevel = %d, want 2", st.MaxLevel)
	}
}

func TestListItemsCarryNestingDepth(t *testing.T) {
	// A sink indents from Block.Level rather than reconstructing the nesting, so the
	// depth has to survive the flattening.
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "outer item"},
		sp{1, 2, "inner item"},
	)
	innerL := kids(el(tag.RoleL, 1), el(tag.RoleLI, 1, 2))
	outerLI := el(tag.RoleLI, 1, 1)
	outerL := kids(el(tag.RoleL, 1), outerLI, innerL)
	tr := tree(el(tag.RoleH1, 1, 0), outerL)

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if blocks[0].Role != doc.RoleListItem || blocks[0].Level != 1 {
		t.Errorf("outer item = role %s level %d", blocks[0].Role, blocks[0].Level)
	}
	if blocks[1].Role != doc.RoleListItem || blocks[1].Level != 2 {
		t.Errorf("inner item = role %s level %d", blocks[1].Role, blocks[1].Level)
	}
}

func TestTOCItemOutsideAListIsLevelOne(t *testing.T) {
	// TOCI appears under TOC, not under L, so counting enclosing L elements gives 0.
	// Level 0 would make a sink emit an unindented bullet at no depth at all.
	d := docWith(sp{1, 0, "Contents"}, sp{1, 1, "1 Scope 9"})
	toc := kids(el(tag.RoleTOC, 1), el(tag.RoleTOCI, 1, 1))
	tr := tree(el(tag.RoleH1, 1, 0), toc)

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if blk.Role != doc.RoleListItem || blk.Level != 1 {
		t.Errorf("TOCI = role %s level %d, want list_item level 1", blk.Role, blk.Level)
	}
}

func TestTableCellsBecomeBlocks(t *testing.T) {
	// Table, TR, THead and TBody are containers. The cells are the content, and a
	// nested P inside a cell must not duplicate it.
	d := docWith(
		sp{1, 0, "7.4 Filters"},
		sp{1, 1, "Key"}, sp{1, 2, "Value"},
		sp{1, 3, "/Filter"}, sp{1, 4, "name or array"},
	)
	head := kids(el(tag.RoleTHead, 1),
		kids(el(tag.RoleTR, 1), el(tag.RoleTH, 1, 1), el(tag.RoleTH, 1, 2)))
	body := kids(el(tag.RoleTBody, 1),
		kids(el(tag.RoleTR, 1),
			el(tag.RoleTD, 1, 3),
			kids(el(tag.RoleTD, 1), el(tag.RoleP, 1, 4)),
		))
	tbl := kids(el(tag.RoleTable, 1), head, body)
	tr := tree(el(tag.RoleH2, 1, 0), tbl)

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 4 {
		t.Fatalf("blocks = %d, want 4 cells: %v", len(blocks), texts(blocks))
	}
	for i, want := range []doc.Role{doc.RoleTableCell, doc.RoleTableCell, doc.RoleTableCell, doc.RoleParagraph} {
		if blocks[i].Role != want {
			t.Errorf("block %d role = %s, want %s", i, blocks[i].Role, want)
		}
	}
	if got := texts(blocks); !equal(got, []string{"Key", "Value", "/Filter", "name or array"}) {
		t.Errorf("cells = %v", got)
	}
}

func TestFigureKeepsAltAsItsOnlyText(t *testing.T) {
	// For an accessible figure /Alt is the only text there is, so a block with no
	// spans is still content. /ActualText wins where both are present: it says what the
	// content is, where /Alt describes it for a reader who cannot see it.
	d := docWith(sp{1, 0, "8.9 Images"}, sp{1, 1, "Figure 12 — Sampling"})
	fig := el(tag.RoleFigure, 1)
	fig.Alt = "A diagram of the sampling grid"
	both := el(tag.RoleFigure, 1)
	both.Alt = "described"
	both.ActualText = "the actual text"
	captioned := kids(el(tag.RoleFigure, 1), el(tag.RoleCaption, 1, 1))
	tr := tree(el(tag.RoleH2, 1, 0), fig, both, captioned)

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3: %v", len(blocks), blocks)
	}
	if blocks[0].Role != doc.RoleFigure || blocks[0].Alt != "A diagram of the sampling grid" {
		t.Errorf("figure = %+v", blocks[0])
	}
	if blocks[1].Alt != "the actual text" {
		t.Errorf("/ActualText not preferred: %q", blocks[1].Alt)
	}
	if blocks[2].Role != doc.RoleCaption || blocks[2].Text() != "Figure 12 — Sampling" {
		t.Errorf("caption = %+v", blocks[2])
	}
}

func TestArtifactsSurviveAsArtifacts(t *testing.T) {
	// Page furniture is kept rather than dropped: whether a running header is noise is
	// the sink's judgement, and the evidence for it is here.
	d := docWith(sp{1, 0, "1 Scope"}, sp{1, 1, "ISO 32000-2:2020(E)"}, sp{1, 2, "Body."})
	tr := tree(el(tag.RoleH1, 1, 0), el(tag.RoleArtifact, 1, 1), el(tag.RoleP, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 2 || blocks[0].Role != doc.RoleArtifact {
		t.Fatalf("blocks = %v", blocks)
	}
}

func TestUnknownRoleIsTransparentAndKeepsItsText(t *testing.T) {
	// An unmapped custom role could be a container or an inline element, and
	// transparency is safe either way: a container keeps its children, an inline
	// element keeps its text. Content directly under one still becomes a paragraph
	// rather than vanishing.
	d := docWith(sp{1, 0, "1 Scope"}, sp{1, 1, "direct text"}, sp{1, 2, "child text"})
	custom := el(tag.Role("CustomThing"), 1, 1)
	kids(custom, el(tag.RoleP, 1, 2))
	tr := tree(el(tag.RoleH1, 1, 0), custom)

	out, st := Tagged(d, tr, DefaultOptions)

	if st.Blocks != 2 {
		t.Fatalf("blocks = %d, want 2", st.Blocks)
	}
	if got := texts(out.Sections[0].Blocks); !equal(got, []string{"direct text", "child text"}) {
		t.Errorf("blocks = %v", got)
	}
}

func TestLangIsCarriedToBlocks(t *testing.T) {
	d := docWith(sp{1, 0, "1 Scope"}, sp{1, 1, "Texte français."})
	p := el(tag.RoleP, 1, 1)
	p.Lang = "fr-FR"
	tr := tree(el(tag.RoleH1, 1, 0), p)

	out, _ := Tagged(d, tr, DefaultOptions)

	if got := out.Sections[0].Blocks[0].Lang; got != "fr-FR" {
		t.Errorf("lang = %q", got)
	}
}

func TestBlockElementNestedInsideHeadingLandsInsideTheSection(t *testing.T) {
	// A Figure in a chapter title, or a producer that wrapped part of a heading in a
	// block role. The nested element is visited after the section is opened, so its
	// content belongs to the clause rather than to whatever preceded it.
	d := docWith(
		sp{1, 0, "Previous body."},
		sp{1, 1, "1 Scope"},
		sp{1, 2, "Figure in the title"},
	)
	h := el(tag.RoleH1, 1, 1)
	kids(h, el(tag.RoleFigure, 1, 2))
	tr := tree(el(tag.RoleP, 1, 0), h)

	out, _ := Tagged(d, tr, DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble = %d blocks, want 1", len(out.Preamble))
	}
	sec := out.Sections[0]
	if sec.Title != "1 Scope" {
		t.Errorf("title = %q: nested block text leaked into it", sec.Title)
	}
	if len(sec.Blocks) != 1 || sec.Blocks[0].Text() != "Figure in the title" {
		t.Errorf("nested block not in the section: %v", sec.Blocks)
	}
}

func TestSectionPagesSpanItsBody(t *testing.T) {
	// A clause running from page 412 to 414 is one section whose blocks came from
	// three pages. Reporting the heading's page alone would stop short of its own
	// final paragraph, which is what a reader checks the conversion against.
	d := docWith(
		sp{1, 0, "7.5 Filters"},
		sp{1, 1, "First."},
		sp{2, 0, "Second."},
		sp{3, 0, "Third."},
	)
	tr := tree(
		el(tag.RoleH2, 1, 0),
		el(tag.RoleP, 1, 1),
		el(tag.RoleP, 2, 0),
		el(tag.RoleP, 3, 0),
	)

	out, _ := Tagged(d, tr, DefaultOptions)

	sec := out.Sections[0]
	if sec.FirstPage != 1 || sec.LastPage != 3 {
		t.Errorf("pages = %d-%d, want 1-3", sec.FirstPage, sec.LastPage)
	}
	// A parent's range does not absorb its children's: the children are separate
	// sections and each reports its own.
	if got := sec.Text(); !strings.Contains(got, "Third.") {
		t.Errorf("body = %q", got)
	}
}

func TestUnanchoredContentIsNotJoined(t *testing.T) {
	// An MCID is unique only within a page. A reference whose element chain never named
	// one cannot be joined at all — joining on the identifier alone would attach page
	// 500's paragraph to page 1's heading — so it must reach Unplaced instead.
	d := docWith(sp{1, 0, "1 Scope"}, sp{1, 1, "Body."})
	orphan := &tag.Elem{Role: tag.RoleP, Content: []tag.MCRef{{MCID: 1}}}
	tr := tree(el(tag.RoleH1, 1, 0), orphan)

	out, st := Tagged(d, tr, DefaultOptions)

	if st.Blocks != 0 {
		t.Errorf("blocks = %d: unanchored reference was joined anyway", st.Blocks)
	}
	if st.UnplacedChars != len("Body.") {
		t.Errorf("unplaced = %d chars, want %d", st.UnplacedChars, len("Body."))
	}
	if len(out.Unplaced) != 1 || !strings.Contains(out.Unplaced[0].Text(), "Body.") {
		t.Errorf("unplaced = %v", out.Unplaced)
	}
}

func TestSpanClaimedOnlyOnce(t *testing.T) {
	// Two elements naming the same (page, MCID) is a tagging defect, but emitting the
	// text twice would put a duplicated sentence into a bundle a model reads as fact.
	d := docWith(sp{1, 0, "1 Scope"}, sp{1, 1, "Body."})
	tr := tree(el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 1))

	out, st := Tagged(d, tr, DefaultOptions)

	if st.Blocks != 1 {
		t.Errorf("blocks = %d, want 1: the same span was claimed twice", st.Blocks)
	}
	if got := out.Sections[0].Text(); got != "Body." {
		t.Errorf("text = %q", got)
	}
}

func TestDeeperFirstHeadingStillRoots(t *testing.T) {
	// A document whose first heading is an H3 — common where a producer numbers
	// headings by visual weight. There is no level-1 section to nest it under, and
	// inventing one would put a section in the tree that no heading corresponds to.
	d := docWith(sp{1, 0, "Deep"}, sp{1, 1, "Body."}, sp{1, 2, "Shallow"})
	tr := tree(el(tag.RoleH3, 1, 0), el(tag.RoleP, 1, 1), el(tag.RoleH1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if len(out.Sections) != 2 {
		t.Fatalf("roots = %v, want 2", titles(out.Sections))
	}
	if out.Sections[0].Level != 3 || out.Sections[1].Level != 1 {
		t.Errorf("levels = %d, %d", out.Sections[0].Level, out.Sections[1].Level)
	}
}

func TestClauseNumber(t *testing.T) {
	cases := []struct{ title, want string }{
		{"7.5.8 Filters", "7.5.8"},
		{"1 Scope", "1"},
		{"7.5.8. Filters", "7.5.8"},
		{"14 Document interchange", "14"},
		{"1.2.3.4.5 Deeply nested", "1.2.3.4.5"},
		{"7.5.8\tFilters", "7.5.8"},
		// An annex is lettered and its subsections are not in the same sequence as the
		// numbered clauses, so admitting "A.1" would sort it among them.
		{"Annex A", ""},
		{"A.1 General", ""},
		{"Foreword", ""},
		{"Introduction", ""},
		// A title that is nothing but a number is a heading whose text failed to
		// resolve, not a clause number worth recording.
		{"7.5.8", ""},
		{"", ""},
		{"7..8 Malformed", ""},
		{".5 Leading dot", ""},
		{"1a Mixed", ""},
		{"— Em dash", ""},
	}
	for _, c := range cases {
		if got := clauseNumber(c.title); got != c.want {
			t.Errorf("clauseNumber(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestClean(t *testing.T) {
	// Titles come out of the join with the page's own spacing. None of it survives
	// being a filename or a YAML value.
	cases := []struct{ in, want string }{
		{"7.5.8\tFilters", "7.5.8 Filters"},
		{"  leading and trailing  ", "leading and trailing"},
		{"collapse   several    spaces", "collapse several spaces"},
		{"line\nbreak", "line break"},
		{"non breaking", "non breaking"},
		{"en space", "en space"},
		{"", ""},
		{"   ", ""},
		{"unchanged", "unchanged"},
	}
	for _, c := range cases {
		if got := clean(c.in); got != c.want {
			t.Errorf("clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- helpers

func titles(secs []*doc.Section) []string {
	out := make([]string, 0, len(secs))
	for _, s := range secs {
		out = append(out, s.Title)
	}
	return out
}

func texts(blocks []doc.Block) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, b.Text())
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// missingRunes returns the runes present in want but not in got, ignoring whitespace.
// Comparing multisets rather than substrings, because the outline reorders content
// relative to the page and a lost character is what matters, not where it moved to.
func missingRunes(want, got string) string {
	have := map[rune]int{}
	for _, r := range got {
		have[r]++
	}
	var missing []rune
	for _, r := range want {
		if r == ' ' || r == '\t' || r == '\n' {
			continue
		}
		if have[r] == 0 {
			missing = append(missing, r)
			continue
		}
		have[r]--
	}
	return string(missing)
}

package sectionize

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/tag"
)

// The declared list marker: what ISO 32000-2 §14.8.4.5.3 calls a Lbl, and what this
// package used to fold into its item's text.
//
// There was no coverage of a tagged list's *marker* before these — TestListItemsCarryNesting
// Depth asserts the depth and TestTOCItemOutsideAListIsLevelOne the role, and neither
// looks at the text. That gap is why the defect these fix survived to be measured in the
// output rather than caught here: 1363 items across 6 corpus files emitting "- ■ text",
// 1242 of them in ISO 32000-2.

// TestDeclaredLabelLeavesTheItemsText is the defect itself, at the smallest scale that
// reproduces it.
//
// Before the fix both Lbl and LBody fell into gather's transparent default, so the label's
// span was appended to the item's spans and the block's text was "■ machine-readable
// text". The sink then wrote its own bullet in front of that.
//
// The label is not given a block role, which is the fix that suggests itself and is wrong:
// a role would emit the label as a block of its own and turn one item into two. It is a
// field of the item, which is what Docling's ListItem models and what Block.Marker is.
//
// The label's span carries the separator the producer drew after the glyph, which is the
// common shape on disk and is why the label is trimmed: a marker of "■ " would put the
// space inside the field, where a sink deciding whether to re-emit it cannot see it.
func TestDeclaredLabelLeavesTheItemsText(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "■ "},
		sp{1, 2, " machine-readable text"},
	)
	li := kids(el(tag.RoleLI, 1),
		el(tag.RoleLbl, 1, 1),
		el(tag.RoleLBody, 1, 2),
	)
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1: the label is a field of the item, not a block", len(blocks))
	}
	blk := blocks[0]
	if blk.Role != doc.RoleListItem {
		t.Errorf("role = %s, want list_item", blk.Role)
	}
	if got := blk.Text(); got != "machine-readable text" {
		t.Errorf("text = %q, want the marker and its space gone", got)
	}
	if blk.Marker != "■" {
		t.Errorf("marker = %q, want %q", blk.Marker, "■")
	}
}

// TestDeclaredLabelReachesAnOrderedList is what the declaration buys that no glyph rule
// could.
//
// 13 of the labels declared on disk are not bullets — "a.", "b." and "[1]" through "[7]",
// all in Well-Tagged-PDF-WTPDF-1.0.pdf. A leading number or letter is also what a heading
// and a table row open with, which is why ADR 0011 records ordered lists as unreachable
// from the glyph side; here the producer states it outright.
//
// Enumerated is the consequence a sink acts on: Markdown has no syntax that says "[1]",
// so the label goes back into the line rather than being dropped, and a bullet does not
// because "- " already is one.
func TestDeclaredLabelReachesAnOrderedList(t *testing.T) {
	d := docWith(
		sp{1, 0, "Bibliography"},
		sp{1, 1, "[1]"},
		sp{1, 2, " ISO 19005-4, Document management"},
	)
	li := kids(el(tag.RoleLI, 1),
		el(tag.RoleLbl, 1, 1),
		el(tag.RoleLBody, 1, 2),
	)
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if blk.Marker != "[1]" {
		t.Errorf("marker = %q, want %q", blk.Marker, "[1]")
	}
	if !blk.Enumerated() {
		t.Error("Enumerated = false, want true: a bracketed number is a label, not a bullet")
	}
	if got := blk.Text(); got != "ISO 19005-4, Document management" {
		t.Errorf("text = %q, want the label out of it", got)
	}
}

// TestUndeclaredMarkerIsStrippedFromADeclaredItem is the other 1291.
//
// Most producers declare the list item and draw the bullet without declaring a Lbl for
// it — 1291 of the 1415 marker-opening items on disk. Without this they emit "- ■ text"
// just as the declared ones did.
//
// It is not the same act as inferring a role. The block is already declared RoleListItem;
// the only question is which of its runes is the label it was declared to have. And the
// answer is checkable: of the items that open with a marker glyph *and* declare a Lbl, the
// label's first rune is that glyph in 124 of 124, with 0 disagreeing. So on every case
// where evidence exists to check the glyph against, the glyph is what the producer meant.
//
// The path this covers is wider than those 1291, because label() reads only the Lbl's own
// marked content and 100 of the 153 Lbl on disk hold their marker in a Span inside it. Those
// 100 are visible in the 124 above: reading the Lbl's whole subtree agrees 124 times, reading
// only its own content 24. So this fallback, not the declaration, is what supplies the marker
// for most of the items that declare one — recorded in label()'s own comment and in DESIGN.md.
func TestUndeclaredMarkerIsStrippedFromADeclaredItem(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "• Adobe Acrobat Reader"},
	)
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), el(tag.RoleLI, 1, 1)))

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if got := blk.Text(); got != "Adobe Acrobat Reader" {
		t.Errorf("text = %q, want the marker gone", got)
	}
	if blk.Marker != "•" {
		t.Errorf("marker = %q, want %q", blk.Marker, "•")
	}
}

// TestDeclaredItemsHyphenIsItsMarker is the 3 remaining items of those 1291, and the reason
// this package strips through StripDeclaredMarker rather than StripMarker.
//
// The shape is ISO 32000-2's own: a declared LI, no Lbl at all, and a hyphen followed by two
// spaces inside the italic run the item opens with. doc.listMarkers holds the hyphen out
// because on the untagged path it is a command-line flag or a C comment continuation in 12 of
// 13 occurrences; here the producer already said this is a list item, so the only question is
// which rune is its label. Before this the output was "- *-  Markup3D (PDF 1.7)*".
//
// It is a sectionize test and not a doc one because the claim is the *call site*: doc's own
// TestStripDeclaredMarkerReadsTheHyphen proves the method reads a hyphen, and every test in
// this package passed with markItem still calling StripMarker. Nothing but a case with a
// hyphen reaching the real path can tell the two apart, and there was none — which is how a
// vocabulary can be written, tested, and never wired in.
func TestDeclaredItemsHyphenIsItsMarker(t *testing.T) {
	d := docWith(
		sp{1, 0, "12.5.6.18 3D annotations"},
		sp{1, 1, "-  Markup3D (PDF 1.7) for a 3D comment."},
	)
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), el(tag.RoleLI, 1, 1)))

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if got := blk.Text(); got != "Markup3D (PDF 1.7) for a 3D comment." {
		t.Errorf("text = %q, want the hyphen and its spaces gone", got)
	}
	if blk.Marker != "-" {
		t.Errorf("marker = %q, want %q: the producer's own bullet", blk.Marker, "-")
	}
	// A bullet, so the sink writes its own "- " and does not put this one back into the
	// line. With Enumerated reading the narrower map it did, as "- \- Markup3D".
	if blk.Enumerated() {
		t.Error("Enumerated = true, want false: a hyphen bullet is not an ordered label")
	}
}

// TestDeclaredLabelOutranksTheGlyph pins the precedence between the two sources, which is
// the whole rule: a declaration is evidence and a glyph is a guess.
//
// The case is real rather than hypothetical — a producer that declares "a." as the label
// and also draws a bullet. Consulting the glyph would record the bullet and leave "a." in
// the text, inverting which of the two the output keeps. It is the same precedence md.go
// states for not running inferRoles over a structure tree.
func TestDeclaredLabelOutranksTheGlyph(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "a."},
		sp{1, 2, " • an alternate description"},
	)
	li := kids(el(tag.RoleLI, 1),
		el(tag.RoleLbl, 1, 1),
		el(tag.RoleLBody, 1, 2),
	)
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if blk.Marker != "a." {
		t.Errorf("marker = %q, want the declaration %q and not the glyph", blk.Marker, "a.")
	}
	if got := blk.Text(); got != "• an alternate description" {
		t.Errorf("text = %q: with a label declared, the drawn glyph is content", got)
	}
}

// TestEmptyLabelFallsBackToTheGlyph is the shape 2 of the 153 declared labels on disk
// have: a Lbl element that owns no marked content at all, because the producer drew the
// marker outside the element it declared for it.
//
// An empty declaration is not a declaration. Treating it as one — taking "" as the marker
// and stopping — would leave the drawn bullet in the text and re-emit the defect on
// exactly the documents that tried hardest to tag correctly.
//
// Those 2 are not the only empties label() returns; there are 102, and the other 100 own
// their marker in a Span one level down, which label() does not read. This test covers the
// producer's version of empty, not that one — the shape with a Span kid has no case here
// at all, which is what DESIGN.md's open question records.
func TestEmptyLabelFallsBackToTheGlyph(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "• One or more content items"},
	)
	li := kids(el(tag.RoleLI, 1),
		el(tag.RoleLbl, 1), // declared, owns no marked content
		el(tag.RoleLBody, 1, 1),
	)
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if got := blk.Text(); got != "One or more content items" {
		t.Errorf("text = %q, want the glyph stripped", got)
	}
	if blk.Marker != "•" {
		t.Errorf("marker = %q, want %q", blk.Marker, "•")
	}
}

// TestLabelIsNotTakenFromANestedItem is why label reads only direct children.
//
// Measured over every tagged list item on disk that declares one, the Lbl is a direct kid
// of its LI in 153 of 153 — so a recursive search buys nothing, and it would cost: the
// first Lbl below an outer item can belong to an inner list's first item, and taking it
// would label the outer item with the inner one's marker and strip that marker from a
// block it does not belong to.
func TestLabelIsNotTakenFromANestedItem(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "outer item"},
		sp{1, 2, "■"},
		sp{1, 3, " inner item"},
	)
	inner := kids(el(tag.RoleLI, 1),
		el(tag.RoleLbl, 1, 2),
		el(tag.RoleLBody, 1, 3),
	)
	outer := kids(el(tag.RoleLI, 1, 1), kids(el(tag.RoleL, 1), inner))
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), outer))

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if blocks[0].Marker != "" {
		t.Errorf("outer marker = %q, want empty: the nested label is not its", blocks[0].Marker)
	}
	if got := blocks[0].Text(); got != "outer item" {
		t.Errorf("outer text = %q, want it untouched", got)
	}
	if blocks[1].Marker != "■" {
		t.Errorf("inner marker = %q, want %q", blocks[1].Marker, "■")
	}
	if got := blocks[1].Text(); got != "inner item" {
		t.Errorf("inner text = %q", got)
	}
}

// TestWrappedBodyStaysInTheItem is the second defect testdata/reference/tagged-lists.pdf
// found, and the one that had to be fixed before the fixture could assert anything.
//
// ISO 32000-2 Table 364 lets an LBody hold any block-level element, and LaTeX's tagging
// backend uses that: it writes LI > LBody > Part > P, so an item's entire body is a wrapped
// paragraph. Detaching that P gave the item no spans at all — dropped by IsEmpty — and
// emitted the body as a paragraph of its own, so six list items came back as six bare
// paragraphs and the marker each had just been given went with the discarded block. Worse
// than the doubled marker it sits next to: a doubled glyph is ugly, a lost role is a list
// that is no longer a list.
//
// A Figure in an LBody still detaches, which is the one such case on disk. A picture beside
// an item is its own block; a paragraph *is* the item's text.
func TestWrappedBodyStaysInTheItem(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "•"},
		sp{1, 2, " First bulleted item."},
	)
	li := kids(el(tag.RoleLI, 1),
		el(tag.RoleLbl, 1, 1),
		kids(el(tag.RoleLBody, 1), kids(el(tag.RolePart, 1), el(tag.RoleP, 1, 2))),
	)
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1: the wrapped paragraph is the item, not a sibling", len(blocks))
	}
	if blocks[0].Role != doc.RoleListItem {
		t.Errorf("role = %s, want list_item", blocks[0].Role)
	}
	if got := blocks[0].Text(); got != "First bulleted item." {
		t.Errorf("text = %q, want the item's own text", got)
	}
	if blocks[0].Marker != "•" {
		t.Errorf("marker = %q, want %q", blocks[0].Marker, "•")
	}
}

// TestWrappedFigureStillDetaches is the boundary of the rule above: only a paragraph is
// transparent inside an item.
//
// ISO 32000-2 has one such item — a Figure inside an LBody — and it must stay its own
// block, because a sink emits an image where it emits a bullet's text otherwise. Folding it
// in would put the figure's alternate text into the item's line and lose the image.
func TestWrappedFigureStillDetaches(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "• An item with a picture."},
	)
	fig := el(tag.RoleFigure, 1)
	fig.Alt = "a diagram"
	li := kids(el(tag.RoleLI, 1, 1), kids(el(tag.RoleLBody, 1), fig))
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: the figure is its own block", len(blocks))
	}
	if blocks[0].Role != doc.RoleListItem || blocks[0].Text() != "An item with a picture." {
		t.Errorf("item = %s %q", blocks[0].Role, blocks[0].Text())
	}
	if blocks[1].Role != doc.RoleFigure || blocks[1].Alt != "a diagram" {
		t.Errorf("figure = %s alt=%q", blocks[1].Role, blocks[1].Alt)
	}
}

// TestNestedListStillDetaches is the other boundary, and the one that would be easy to
// break while fixing the paragraph case: a list inside an item stays separate items.
//
// It holds without a special case — an inner LI has a block role of its own, so the
// recursion reaches it through the transparent L and stops. Asserted because the paragraph
// rule is a hole in exactly that stop, and widening it to "any block role inside an item"
// would fuse a whole nested list into its parent's line.
func TestNestedListStillDetaches(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "• outer"},
		sp{1, 2, "• inner"},
	)
	inner := kids(el(tag.RoleL, 1), el(tag.RoleLI, 1, 2))
	li := kids(el(tag.RoleLI, 1, 1), kids(el(tag.RoleLBody, 1), inner))
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if got := blocks[0].Text(); got != "outer" {
		t.Errorf("outer text = %q, want the inner item out of it", got)
	}
	if blocks[1].Level != 2 {
		t.Errorf("inner level = %d, want 2", blocks[1].Level)
	}
}

// TestItemTransparencyStopsAtABlockBoundary is the third boundary, and the only one where
// the rule could reach somewhere it was never meant to.
//
// The paragraph-transparency flag is passed down gather's recursion, so the question is how
// far down it goes. It stops at any element with a block role: that element is detached and
// re-entered through block, where the flag is set from its own role, and a TD is not a list
// item. Without that reset a paragraph inside a table cell inside a list item would fold
// into the item's line and the cell would come out empty — a table quietly absorbed into a
// bullet.
//
// Asserted on a shape no corpus document has: 0 of the 1742 LI on ISO 32000-2 wrap a
// paragraph at all, so the corpus cannot witness this and neither can the fixture, whose
// lists hold no tables. The reset is a property of the code either way; this is what makes
// it a checked one.
//
// Phrased as an equivalence rather than a literal expectation, and that is the point: the
// same table inside a list item and outside one must produce the same blocks. A TD wrapping
// a P emits the paragraph and drops the empty cell — the case block's own comment names,
// pre-dating this rule — so pinning the literal role here would pin that behaviour to a
// list-item test and break this when tables are given a grid. What must hold is that the
// enclosing item changes nothing.
func TestItemTransparencyStopsAtABlockBoundary(t *testing.T) {
	table := func() *tag.Elem {
		cell := kids(el(tag.RoleTD, 1), el(tag.RoleP, 1, 2))
		return kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), cell))
	}
	blocksOf := func(t *testing.T, tr *tag.Tree) []doc.Block {
		t.Helper()
		d := docWith(
			sp{1, 0, "1 Scope"},
			sp{1, 1, "• An item with a table:"},
			sp{1, 2, "a cell's paragraph"},
		)
		out, _ := Tagged(d, tr, DefaultOptions)
		return out.Sections[0].Blocks
	}

	li := kids(el(tag.RoleLI, 1, 1), kids(el(tag.RoleLBody, 1), table()))
	inside := blocksOf(t, tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li)))
	outside := blocksOf(t, tree(el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1), table()))

	if len(inside) != 2 {
		t.Fatalf("blocks = %d, want 2: the cell's content is not part of the item", len(inside))
	}
	if got := inside[0].Text(); got != "An item with a table:" {
		t.Errorf("item text = %q, want the cell's paragraph out of it", got)
	}
	if inside[0].Marker != "•" {
		t.Errorf("marker = %q, want %q", inside[0].Marker, "•")
	}
	// The cell's content, whatever role it carries, is unchanged by the item around it.
	if len(outside) != len(inside) {
		t.Fatalf("%d blocks inside an item, %d outside: the item changed the table", len(inside), len(outside))
	}
	if inside[1].Role != outside[1].Role || inside[1].Text() != outside[1].Text() {
		t.Errorf("cell inside an item = %s %q, outside = %s %q",
			inside[1].Role, inside[1].Text(), outside[1].Role, outside[1].Text())
	}
	if got := inside[1].Text(); got != "a cell's paragraph" {
		t.Errorf("cell text = %q, want its paragraph", got)
	}
}

// TestLabelOutsideAListItemIsLeftAsText is the boundary on which content is at stake
// rather than formatting.
//
// A label is only taken from an element the tree declared a list item. Taking one from
// anything else would claim its spans without anywhere to put them — the marker is
// recorded only for RoleListItem — so the label's text would leave the block and reach no
// field, and "(a) prose after it" would emit as "prose after it".
//
// The role is a real one to guard against: a Lbl is legal wherever a producer puts it, and
// ISO 32000-2 §14.8.4.5.3 constrains it by convention rather than by the reader's
// obligation. The corpus has none outside an LI or TOCI, which is exactly why nothing else
// here would notice.
func TestLabelOutsideAListItemIsLeftAsText(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "(a)"},
		sp{1, 2, " prose after it"},
	)
	p := kids(el(tag.RoleP, 1), el(tag.RoleLbl, 1, 1), el(tag.RoleSpan, 1, 2))
	tr := tree(el(tag.RoleH1, 1, 0), p)

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if got := blk.Text(); got != "(a) prose after it" {
		t.Errorf("text = %q, want the label kept as text: nothing else holds it", got)
	}
	if blk.Marker != "" {
		t.Errorf("marker = %q, want empty on a paragraph", blk.Marker)
	}
}

// TestLabelOnlyItemIsDropped is the emptying case, which measurement says does not occur
// on disk and which must still not lose content silently.
//
// Of the 1415 marker-opening items, 0 would be emptied by removing the marker, and all 16
// ordered labels leave non-empty content. But a producer can declare an item that is only a
// label — a numbered placeholder whose body is elsewhere — and the block then has no spans.
// The honest answer is that a marker is not content: such an item *is* empty and IsEmpty
// drops it, which is asserted so that emitting an empty bullet instead is a decision
// someone takes rather than a side effect of moving the strip.
func TestLabelOnlyItemIsDropped(t *testing.T) {
	d := docWith(sp{1, 0, "1 Scope"}, sp{1, 1, "[1]"})
	li := kids(el(tag.RoleLI, 1), el(tag.RoleLbl, 1, 1))
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleL, 1), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	// Dropped, because a label with no item is not an item. Asserted so that a change
	// making it emit an empty bullet is a decision someone takes rather than a
	// side effect.
	if n := len(out.Sections[0].Blocks); n != 0 {
		t.Errorf("blocks = %d, want 0: a label alone is not content", n)
	}
}

// TestNonListRolesGetNoMarker keeps the strip off every block that is not a list item.
//
// A paragraph opening with an en dash is an ISO glyph-table row ("— 132 0x84 0204 U+2014
// EM DASH"), and those are exactly the five false positives ADR 0011 accepts on the
// untagged path *because* they are 5 in 1442 there. On this path they are not a cost worth
// paying at all: the producer said Table, so nothing has to guess.
func TestNonListRolesGetNoMarker(t *testing.T) {
	d := docWith(
		sp{1, 0, "Annex D"},
		sp{1, 1, "— 132 0x84 0204 U+2014 EM DASH"},
	)
	row := kids(el(tag.RoleTR, 1), el(tag.RoleTD, 1, 1))
	tr := tree(el(tag.RoleH1, 1, 0), kids(el(tag.RoleTable, 1), row))

	out, _ := Tagged(d, tr, DefaultOptions)

	blk := out.Sections[0].Blocks[0]
	if got := blk.Text(); got != "— 132 0x84 0204 U+2014 EM DASH" {
		t.Errorf("text = %q, want the dash kept: it is the row's subject", got)
	}
	if blk.Marker != "" {
		t.Errorf("marker = %q, want empty on a table cell", blk.Marker)
	}
}

// TestLabelPagesJoinTheItemsRange keeps a section's page range covering its labels.
//
// A label is content the page draws, so its pages belong to the item's range even though
// its text leaves the item's spans — an item whose label falls on the page before its body,
// a list broken across a page break, must not report a range starting after the marker a
// reader can see.
//
// It holds because Lbl has no block role and gather therefore descends into it, not because
// label does anything about it. Asserted here rather than assumed, because "the label's
// pages are somebody's job" is the kind of claim that stays true only until Lbl acquires a
// role for some other reason — and this fails the moment it does.
func TestLabelPagesJoinTheItemsRange(t *testing.T) {
	d := docWith(
		sp{2, 0, "1 Scope"},
		sp{1, 1, "■"},
		sp{2, 2, " body beside its label"},
	)
	li := kids(el(tag.RoleLI, 2),
		el(tag.RoleLbl, 1, 1),
		el(tag.RoleLBody, 2, 2),
	)
	tr := tree(el(tag.RoleH1, 2, 0), kids(el(tag.RoleL, 2), li))

	out, _ := Tagged(d, tr, DefaultOptions)

	// The heading is on page 2 and only the label is on page 1, so the label is the sole
	// reason FirstPage can be 1 — which is what makes this observe the mechanism rather
	// than the heading's own anchor.
	sec := out.Sections[0]
	if sec.FirstPage != 1 || sec.LastPage != 2 {
		t.Errorf("pages = %d-%d, want 1-2: the label's page is the item's too", sec.FirstPage, sec.LastPage)
	}
}

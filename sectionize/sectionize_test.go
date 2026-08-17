package sectionize

import (
	"slices"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/tag"
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

// atLines gives a document's spans a type size and a baseline each, so that a fixture can
// express where on the page its text was drawn.
//
// docWith leaves both zero, which is the right default for every rule keyed on the structure
// tree and useless for one keyed on geometry: at size 0 every threshold is 0, so any two spans
// are on different lines. The baselines are given in the order the spans were added and the
// heading's span is skipped, since a fixture's heading is not part of the listing being
// measured.
func atLines(d *doc.Document, size float64, baselines ...float64) {
	i := 0
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			spans := d.Pages[pi].Blocks[bi].Spans
			for si := range spans {
				spans[si].Style.Size = size
				if spans[si].MCID == 0 {
					continue
				}
				if i >= len(baselines) {
					panic("atLines: fewer baselines than spans")
				}
				spans[si].Box = geom.Rect{X0: 72, Y0: baselines[i], X1: 300, Y1: baselines[i] + size}
				i++
			}
		}
	}
	if i != len(baselines) {
		panic("atLines: more baselines than spans")
	}
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

// interleaved builds an element whose /K array mixes its own marked content with kids, in
// the order given: an int is an MCID the element owns, an *tag.Elem is a child. Both get
// the /K position tag.Read records, which is what inOrder reads.
//
// Separate from el and kids because those two build the two pure shapes and neither can
// express a kid *between* two runs of content — the state 767 elements on disk are in.
func interleaved(role tag.Role, page int, items ...any) *tag.Elem {
	e := &tag.Elem{Role: role, RawType: role, Page: page}
	for at, it := range items {
		switch v := it.(type) {
		case int:
			e.Content = append(e.Content, tag.MCRef{MCID: v, Page: page, Order: at})
		case *tag.Elem:
			v.Parent = e
			e.Kids = append(e.Kids, v)
			e.KidAt = append(e.KidAt, at)
		default:
			panic("interleaved: want int or *tag.Elem")
		}
	}
	return e
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
	// Every run is a child here, so this shape reads the same whether or not the walk
	// honours /K order. TestInlineKidReadsInItsKPosition is the one that needs the order,
	// because there the P's own content sits on both sides of a child.
	p := kids(el(tag.RoleP, 1, 1),
		el(tag.RoleSpan, 1, 2),
	)
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

// TestInlineKidReadsInItsKPosition: a paragraph's own marked content on both sides of an
// inline child. Reading all the content and then the child moves the child's glyph to the
// end, which is the shape 767 elements on disk have — and where the child is a Span holding
// one soft hyphen, the result is "constituent elements.--" in ISO/TS 32005's Table 1, with
// the hyphens of "exposi-tion" and "constitu-ent" trailing the cell.
func TestInlineKidReadsInItsKPosition(t *testing.T) {
	d := docWith(
		sp{1, 0, "exposi"},
		sp{1, 1, "-"},
		sp{1, 2, "tion and constitu"},
		sp{1, 3, "-"},
		sp{1, 4, "ent elements."},
	)
	p := interleaved(tag.RoleP, 1,
		0,
		el(tag.RoleSpan, 1, 1),
		2,
		el(tag.RoleSpan, 1, 3),
		4,
	)
	tr := tree(p)

	out, _ := Tagged(d, tr, DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble blocks = %d, want 1", len(out.Preamble))
	}
	const want = "exposi-tion and constitu-ent elements."
	if got := out.Preamble[0].Text(); got != want {
		t.Errorf("text = %q, want %q: an inline kid reads in its /K position", got, want)
	}
}

// TestTransparentContentEmitsInKOrder: the same rule one level up. A container holding
// text, then a section, then more text is three things in that order, and emitting both
// runs of its own text first puts the second before the heading it follows.
func TestTransparentContentEmitsInKOrder(t *testing.T) {
	d := docWith(
		sp{1, 0, "Before the heading."},
		sp{1, 1, "1 Scope"},
		sp{1, 2, "After the heading."},
	)
	div := interleaved(tag.RoleDiv, 1,
		0,
		el(tag.RoleH1, 1, 1),
		2,
	)
	tr := tree(div)

	out, _ := Tagged(d, tr, DefaultOptions)

	// The first run precedes the heading, so it is preamble; the second follows it and
	// belongs to the section. Ordering by slice instead puts both in the preamble.
	if len(out.Preamble) != 1 || out.Preamble[0].Text() != "Before the heading." {
		t.Fatalf("preamble = %v, want one block of the text before the heading", texts(out.Preamble))
	}
	if len(out.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(out.Sections))
	}
	if got := out.Sections[0].Text(); got != "After the heading." {
		t.Errorf("section text = %q, want the run that follows the heading", got)
	}
}

// TestDeclaredSoftHyphenJoinsTheWord is the corpus shape, one level of detail below
// TestInlineKidReadsInItsKPosition: the Span whose /K position that test recovered declares
// /ActualText U+00AD over the "-" it draws, so a reader that puts the hyphen back in the
// right place is still putting back a hyphen the producer disclaimed.
//
// A declared soft hyphen is *discretionary* — drawn only if the line breaks there — and
// nothing downstream of here has a line width, so it can never be exercised and the word
// joins. This is what the corpus's 16 declarations say, in four ISO documents, and each
// document's own spelling agrees: "digest" appears joined 30 times against the one break,
// "structure" 63.
//
// Both spans are asserted, not just the text: a substitution replaces a run of marked
// content, so the run's several spans become one, and the reference from the surviving span
// must be the declaring element's own rather than a neighbour's.
func TestDeclaredSoftHyphenJoinsTheWord(t *testing.T) {
	d := docWith(
		sp{1, 0, "The docu"},
		sp{1, 1, "-"},
		sp{1, 2, "ment digest."},
	)
	shy := el(tag.RoleSpan, 1, 1)
	shy.ActualText = "\u00ad"
	p := interleaved(tag.RoleP, 1, 0, shy, 2)

	out, _ := Tagged(d, tree(p), DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble blocks = %d, want 1", len(out.Preamble))
	}
	const want = "The document digest."
	if got := out.Preamble[0].Text(); got != want {
		t.Errorf("text = %q, want %q: a declared U+00AD is discretionary, so the word joins", got, want)
	}
}

// TestDeclaredActualTextReplacesTheGlyphs is the general rule the soft hyphen is one case
// of, per ISO 32000-2 §14.9.4: the declared value stands in for what was drawn.
//
// Asserted over a Span holding several references, which is the shape 92 of the corpus's
// declarations have — a Lbl whose /ActualText covers two marked-content sequences. One value
// replaces the whole run, so the spans collapse to one; replacing per reference would repeat
// the value once per sequence.
func TestDeclaredActualTextReplacesTheGlyphs(t *testing.T) {
	d := docWith(
		sp{1, 0, "before"},
		sp{1, 1, ""},
		sp{1, 2, " "},
		sp{1, 3, "after"},
	)
	span := el(tag.RoleSpan, 1, 1, 2)
	span.ActualText = " • "
	p := interleaved(tag.RoleP, 1, 0, span, 3)

	out, _ := Tagged(d, tree(p), DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble blocks = %d, want 1", len(out.Preamble))
	}
	blk := out.Preamble[0]
	const want = "before • after"
	if got := blk.Text(); got != want {
		t.Errorf("text = %q, want %q: /ActualText stands in for the glyphs", got, want)
	}
	// Three spans, not four: the declaring element's two references became one.
	if n := len(blk.Spans); n != 3 {
		t.Errorf("spans = %d, want 3: one value replaces the whole run it declares", n)
	}
}

// TestDeclaredLineBreakBecomesASpace: 4695 of the corpus's 4803 declarations are "\n" over a
// drawn space, and a doc.Span holds inline text, which has no line breaks in it.
//
// Left in, the break reaches the sink as span text and splits a line there. Measured before
// this rule existed: ISO/TS 32004's "**Technical Specification**" came out as "**Technical"
// and "Specification**" on two lines, no longer bold either, since a CommonMark emphasis run
// cannot span a line break.
//
// CRLF is one break and becomes one space, which is the case no corpus file has — every one
// of the 4695 is a bare "\n" — and the only one where a per-rune rule could double.
func TestDeclaredLineBreakBecomesASpace(t *testing.T) {
	for _, tc := range []struct{ name, decl, want string }{
		{"LF", "Technical\nSpecification", "Technical Specification"},
		{"CRLF", "Technical\r\nSpecification", "Technical Specification"},
		{"CR", "Technical\rSpecification", "Technical Specification"},
		{"both breaks and a soft hyphen", "Speci\u00ad\nfication", "Speci fication"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := docWith(sp{1, 0, "drawn on one line"})
			span := el(tag.RoleSpan, 1, 0)
			span.ActualText = tc.decl
			out, _ := Tagged(d, tree(kids(el(tag.RoleP, 1), span)), DefaultOptions)

			if len(out.Preamble) != 1 {
				t.Fatalf("preamble blocks = %d, want 1", len(out.Preamble))
			}
			if got := out.Preamble[0].Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeclarationOnABlockIsAdaptedToo: substituted is not the only consumer of a raw
// /ActualText. emitItem copies the value into doc.Block.Replacement, which every sink's
// substitute() prefers over the block's spans — so a declaration left unadapted there emits
// the invisible soft hyphen this rule exists to remove, one layer further on.
//
// No corpus file reaches it: all 4803 declarations are on a Span, which never becomes a
// block. That is why it is a test rather than a measurement, and why review found it rather
// than the corpus diff — a raw Replacement is a defect that nothing on disk can show.
func TestDeclarationOnABlockIsAdaptedToo(t *testing.T) {
	d := docWith(sp{1, 0, "drawn text"})
	// A Figure is the block-level shape §14.9.4 is written for: a word drawn as artwork,
	// with the producer stating what it says.
	fig := el(tag.RoleFigure, 1, 0)
	fig.ActualText = "di\u00adgest\nof the file"

	out, _ := Tagged(d, tree(fig), DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble blocks = %d, want 1", len(out.Preamble))
	}
	const want = "digest of the file"
	if got := out.Preamble[0].Replacement; got != want {
		t.Errorf("replacement = %q, want %q: a block's declaration is inline text too", got, want)
	}
}

// TestDeclaredTitleIsAdaptedToo is the third consumer. A heading whose marked content
// resolved to no spans has nothing for substituted to replace, so title reads the raw value
// — and clean is not enough on its own: it folds the declared line break, because a break is
// whitespace, and leaves U+00AD, because it is not.
func TestDeclaredTitleIsAdaptedToo(t *testing.T) {
	d := docWith(sp{1, 0, "body text"})
	h := el(tag.RoleH1, 1)
	h.ActualText = "7.1 Docu\u00adment\ndigest"
	tr := tree(h, el(tag.RoleP, 1, 0))

	out, _ := Tagged(d, tr, DefaultOptions)

	if len(out.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(out.Sections))
	}
	const want = "7.1 Document digest"
	if got := out.Sections[0].Title; got != want {
		t.Errorf("title = %q, want %q: a declared title is inline text too", got, want)
	}
}

// TestSubstitutionKeepsTheWholeRunsBox: a value covering several spans collapses them to one,
// and that one has to cover the area all of them did. The surviving span keeps the first's
// style, because a substitution is a string and has none of its own, but the box is the union:
// the block's own box is the union of its spans', so keeping only the first's would shrink a
// paragraph to the width of its opening glyph — and layout's column and table logic reads
// exactly those edges.
//
// Asserted here rather than through a block, because every other fixture in this file leaves
// its spans' boxes zero: dropping the union survived all eleven of the other mutations and the
// whole corpus, since geom.Rect.Union of two zero rects is zero either way.
func TestSubstitutionKeepsTheWholeRunsBox(t *testing.T) {
	first := &doc.Span{Text: "before", Box: geom.NewRect(10, 100, 30, 112)}
	second := &doc.Span{Text: "after", Box: geom.NewRect(40, 96, 90, 110)}
	e := &tag.Elem{Role: tag.RoleSpan, ActualText: "declared"}

	out := substituted(e, []*doc.Span{first, second})

	if len(out) != 1 {
		t.Fatalf("spans = %d, want 1", len(out))
	}
	if want := geom.NewRect(10, 96, 90, 112); out[0].Box != want {
		t.Errorf("box = %+v, want %+v: the surviving span covers the whole run", out[0].Box, want)
	}
}

// TestSubstitutionDoesNotEditTheDocument: index hands out pointers into the doc.Document, and
// the recovery pass reads the same ones. A substitution that edited a span in place would
// rewrite the page text of a document the caller also asked to extract — and would corrupt
// Unplaced, whose whole job is to report what the tree did not claim.
func TestSubstitutionDoesNotEditTheDocument(t *testing.T) {
	d := docWith(
		sp{1, 0, "claimed"},
		sp{1, 1, "unclaimed"},
	)
	span := el(tag.RoleSpan, 1, 0)
	span.ActualText = "declared"
	Tagged(d, tree(kids(el(tag.RoleP, 1), span)), DefaultOptions)

	if got := d.Pages[0].Blocks[0].Spans[0].Text; got != "claimed" {
		t.Errorf("document span = %q, want %q: the substitution edited the caller's document", got, "claimed")
	}
}

// TestEmptyActualTextIsNotASubstitution: tag.Read leaves the field "" when the key is absent,
// so an empty value cannot be told from no declaration at all. Substituting it would delete
// the glyphs of every element in the corpus that declares nothing — which is 90721 of 90737.
func TestEmptyActualTextIsNotASubstitution(t *testing.T) {
	d := docWith(sp{1, 0, "the drawn text"})
	span := el(tag.RoleSpan, 1, 0)
	span.ActualText = ""
	out, _ := Tagged(d, tree(kids(el(tag.RoleP, 1), span)), DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble blocks = %d, want 1", len(out.Preamble))
	}
	if got := out.Preamble[0].Text(); got != "the drawn text" {
		t.Errorf("text = %q, want the drawn text: an absent /ActualText is not a substitution", got)
	}
}

// TestDeclaredLabelIsSubstituted covers the second inline path. gather is not the only walker
// that claims spans — labelText reads a Lbl's, and that is where all 92 of the corpus's " • "
// declarations live, in LI>Lbl>Span. A rule wired into one walker and not the other is a rule
// the corpus's largest declared shape never reaches.
func TestDeclaredLabelIsSubstituted(t *testing.T) {
	d := docWith(
		sp{1, 0, ""},
		sp{1, 1, "The item's text."},
	)
	span := el(tag.RoleSpan, 1, 0)
	span.ActualText = "•"
	li := kids(el(tag.RoleLI, 1), kids(el(tag.RoleLbl, 1), span), el(tag.RoleLBody, 1, 1))

	out, _ := Tagged(d, tree(kids(el(tag.RoleL, 1), li)), DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble blocks = %d, want 1", len(out.Preamble))
	}
	if got := out.Preamble[0].Marker; got != "•" {
		t.Errorf("marker = %q, want U+2022: a Lbl's /ActualText is its declared label", got)
	}
}

// TestInOrderSurvivesEveryKidAtSkew: KidAt is an index into Kids, and inOrder reads one to
// decide whether to read the other. Every way the two can disagree is covered here, because
// tag.Read cannot produce any of them — it appends to both in one statement — so the only
// thing standing between a hand-built Elem and a panic is this test.
//
// The KidAt-longer-than-Kids row is the one that panicked: the loop bounds the kid branch by
// len(Kids) while kidBefore bounded it by len(KidAt), so a position naming a kid that does not
// exist sent inOrder to Kids[ki]. Every row asserts termination and that every item is handed
// over exactly once, which is the property a merge can lose in either direction.
func TestInOrderSurvivesEveryKidAtSkew(t *testing.T) {
	kid := func(page int) *tag.Elem { return &tag.Elem{Role: tag.RoleSpan, Page: page} }
	for _, tc := range []struct {
		name    string
		content []tag.MCRef
		kids    []*tag.Elem
		kidAt   []int
	}{
		{"KidAt longer than Kids", []tag.MCRef{{Order: 0}, {Order: 3}}, nil, []int{1, 2}},
		{"KidAt shorter than Kids", []tag.MCRef{{Order: 0}}, []*tag.Elem{kid(1), kid(1)}, []int{1}},
		{"Kids with no KidAt at all", []tag.MCRef{{Order: 0}}, []*tag.Elem{kid(1)}, nil},
		{"KidAt with no Kids at all", []tag.MCRef{{Order: 1}}, nil, []int{0}},
		{"negative positions", []tag.MCRef{{Order: -2}}, []*tag.Elem{kid(1)}, []int{-5}},
		{"duplicate positions", []tag.MCRef{{Order: 2}, {Order: 2}}, []*tag.Elem{kid(1)}, []int{2}},
		{"positions out of ascending order", []tag.MCRef{{Order: 5}, {Order: 1}}, []*tag.Elem{kid(1), kid(1)}, []int{4, 0}},
		{"content only", []tag.MCRef{{Order: 0}, {Order: 1}}, nil, nil},
		{"kids only", nil, []*tag.Elem{kid(1)}, []int{0}},
		{"nothing at all", nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &tag.Elem{Role: tag.RoleDiv, Content: tc.content, Kids: tc.kids, KidAt: tc.kidAt}
			refs, kids := 0, 0
			// A merge that fails to advance runs forever; bound the work at more than the
			// items available so a stall fails the test rather than hanging it.
			limit := 2*(len(tc.content)+len(tc.kids)) + 4
			seen := map[*tag.Elem]bool{}
			inOrder(e,
				func(r []tag.MCRef) {
					refs += len(r)
					if refs+kids > limit {
						t.Fatalf("inOrder is not making progress")
					}
				},
				func(k *tag.Elem) {
					if seen[k] {
						t.Fatalf("kid handed over twice")
					}
					seen[k] = true
					kids++
					if refs+kids > limit {
						t.Fatalf("inOrder is not making progress")
					}
				})
			if refs != len(tc.content) || kids != len(tc.kids) {
				t.Errorf("handed over %d refs and %d kids, want %d and %d",
					refs, kids, len(tc.content), len(tc.kids))
			}
		})
	}
}

// TestTransparentRunIsEveryReferenceUpToTheNextKid: what makes a run a run is the kid
// between them, not the /K position. Two references with nothing between them are one
// stretch of text and one paragraph; splitting per position emits a paragraph per MCID.
//
// The test above cannot see this, because each of its runs is a single reference.
func TestTransparentRunIsEveryReferenceUpToTheNextKid(t *testing.T) {
	d := docWith(
		sp{1, 0, "A container holding text, "},
		sp{1, 1, "drawn as two sequences."},
		sp{1, 2, "1 Scope"},
		sp{1, 3, "Then two more "},
		sp{1, 4, "after the heading."},
	)
	div := interleaved(tag.RoleDiv, 1,
		0, 1,
		el(tag.RoleH1, 1, 2),
		3, 4,
	)

	out, _ := Tagged(d, tree(div), DefaultOptions)

	if len(out.Preamble) != 1 {
		t.Fatalf("preamble = %v, want the two references before the heading as one block", texts(out.Preamble))
	}
	if got := out.Preamble[0].Text(); got != "A container holding text, drawn as two sequences." {
		t.Errorf("preamble text = %q", got)
	}
	if n := len(out.Sections[0].Blocks); n != 1 {
		t.Fatalf("section blocks = %d, want the two references after the heading as one block", n)
	}
	if got := out.Sections[0].Text(); got != "Then two more after the heading." {
		t.Errorf("section text = %q", got)
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
	// /T first when a producer filled it in, then the text, then /ActualText for a heading
	// that resolved to no spans at all.
	//
	// "the text" is already the substituted text, which is why the middle case wants the
	// declared value and not the glyphs: substituted applies /ActualText to a declaring
	// element's spans before title ever sees them, per ISO 32000-2 §14.9.4. This test
	// asserted the opposite until the substitution was implemented, on the reasoning that
	// a reader checking the conversion sees the glyphs on the page — which is a reason to
	// keep the glyphs everywhere or nowhere, not a reason for a heading to disagree with
	// the paragraph below it. §14.9.4, doc.Block.Replacement and markdown.substitute all
	// say the declared value stands in, so this is now one rule rather than two.
	//
	// /T still wins over both. It is the producer's own title for the element rather than
	// a statement about what its glyphs spell, so it does not compete with /ActualText.
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

	want := []string{"the /T value", "the /ActualText value", "only /ActualText"}
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

// A Code element that holds its listing as one P per line keeps it as one block, with the
// line breaks the structure declared.
//
// This is where the whole listing was lost. Of the 18 Code elements on disk, 11 hold no
// marked content of their own and 99 P kids between them — every one emitted no spans, was
// dropped by IsEmpty, and its lines escaped as ordinary paragraphs, so not one of
// Well-Tagged-PDF-WTPDF-1.0.pdf's eleven listings was fenced. The other 7 carry their own
// content and always worked, which is why a fence count alone looked healthy.
//
// The newline is the assertion, not the block count. Absorbing the paragraphs without
// restoring the breaks gives one block whose text is every line run together — the same
// collapsed line the fence exists to prevent, and a defect no role check or
// character-conservation check can see.
func TestCodeLinesBecomeOneBlock(t *testing.T) {
	d := docWith(
		sp{1, 0, "7.4 Filters"},
		sp{1, 1, "10 0 obj <<"}, sp{1, 2, "  /Metadata 11 0 R"}, sp{1, 3, ">>"},
	)
	code := kids(el(tag.RoleCode, 1),
		el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 2), el(tag.RoleP, 1, 3))
	tr := tree(el(tag.RoleH2, 1, 0), code)

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 listing: %v", len(blocks), texts(blocks))
	}
	if blocks[0].Role != doc.RoleCode {
		t.Errorf("role = %s, want %s", blocks[0].Role, doc.RoleCode)
	}
	if got, want := blocks[0].Text(), "10 0 obj <<\n  /Metadata 11 0 R\n>>"; got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

// The break goes between two lines and never before the first one.
//
// A leading newline opens the fenced block with a blank line, which is content the page does
// not draw. The guard is on there being spans already, so a Code element whose own marked
// content precedes its kids is the case that distinguishes it: without the guard the first
// kid's break would still be correct and only a listing that starts with a kid would show
// the defect.
func TestCodeLeadsWithNoBlankLine(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "first"}, sp{1, 2, "second"})
	code := interleaved(tag.RoleCode, 1, 1, el(tag.RoleP, 1, 2))
	tr := tree(el(tag.RoleH2, 1, 0), code)

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "first\nsecond"; got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

// A Span inside a line takes no break, because a Span is not a line.
//
// The break is written per absorbed *block*, not per absorbed kid, and the difference is
// reachable: 10 of the 109 descendants of the corpus's Code elements are Span, one per styled
// run inside a listing line. Dropping the blockRole guard breaks each of those lines in two.
//
// Its own test because the corpus is the only other thing that holds it, and the corpus tests
// skip when the sponsored PDFs are absent — so on a clean clone the guard would be covered by
// nothing at all. This is the shape the WTPDF listings are actually in: a P holding a Span
// holding the text, not a P holding text directly.
func TestCodeSpansDoNotSplitALine(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "/Filter "}, sp{1, 2, "/FlateDecode"})
	line := kids(el(tag.RoleP, 1), el(tag.RoleSpan, 1, 1), el(tag.RoleSpan, 1, 2))
	tr := tree(el(tag.RoleH2, 1, 0), kids(el(tag.RoleCode, 1), line))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "/Filter /FlateDecode"; got != want {
		t.Errorf("listing = %q, want %q: a Span is a styled run inside a line, not a line", got, want)
	}
}

// A Code element whose lines are marked content rather than paragraphs still gets them back,
// from the only place they are recorded: the baselines the spans were drawn at.
//
// This is the other half of what a listing's lines can be, and gather's rule cannot reach it —
// there is no paragraph to absorb. PDF-Declarations.pdf declares one Code holding 25 lines as
// 25 MCIDs under no P at all, and the sink fenced them as a single 892-character line. The
// text carries no clue either: the page draws a space at each line end, so extract finds a
// word boundary already written and infers nothing, and dropping this rule loses all 24 breaks
// while every character-conservation check still passes.
func TestCodeMarkedContentLinesBreakOnBaseline(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "10 0 obj << "}, sp{1, 2, "  /Metadata 11 0 R "},
		sp{1, 3, ">>"})
	// 10pt type on 12pt leading, which is the corpus's shape and well past LineFrac's half.
	atLines(d, 10, 700, 688, 676)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2, 3))

	out, _ := Tagged(d, tr, DefaultOptions)

	blocks := out.Sections[0].Blocks
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 listing: %v", len(blocks), texts(blocks))
	}
	if got, want := blocks[0].Text(), "10 0 obj << \n  /Metadata 11 0 R \n>>"; got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

// Two spans on one baseline are one line, so no break goes between them.
//
// A styled run inside a listing line is the ordinary case — 25 of PDF-Declarations' 50 spans
// are a second span on a line already open — and breaking on those splits the line they are
// part of. Any rule that fires per span rather than per baseline change fails this.
func TestCodeSpansOnOneBaselineStayOneLine(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "/Filter "}, sp{1, 2, "/FlateDecode"})
	atLines(d, 10, 700, 700)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "/Filter /FlateDecode"; got != want {
		t.Errorf("listing = %q, want %q: one baseline is one line", got, want)
	}
}

// A step smaller than half the type size is jitter, not a line.
//
// The threshold is the extractor's own LineFrac rather than an epsilon of this rule's
// invention, so a listing set in one size is measured against that size. A superscript or a
// baseline nudged by a fraction of a point is the shape that would otherwise break a line in
// two, and comparing against zero would break every one of them.
func TestCodeIgnoresBaselineJitter(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "x"}, sp{1, 2, "2"})
	atLines(d, 10, 700, 702)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "x2"; got != want {
		t.Errorf("listing = %q, want %q: 2pt is under half of 10pt type", got, want)
	}
}

// A listing that runs onto the next page steps up, and that is still a line.
//
// The corpus has exactly one: PDF-Declarations' XML sample crosses a page and rises 681pt into
// "<!-- Optional entries". A signed comparison reads a rise as a negative step, never exceeds
// the threshold, and joins the last line of one page to the first line of the next — the one
// place in the block where the collapse this rule exists to undo would survive it.
func TestCodeBreaksOnAnUpwardStep(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "endobj"}, sp{1, 2, "11 0 obj"})
	atLines(d, 10, 90, 700)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "endobj\n11 0 obj"; got != want {
		t.Errorf("listing = %q, want %q: a 610pt rise is a page break, not one line", got, want)
	}
}

// A raised or lowered run inside a line is measured against the line, not against itself.
//
// The threshold is LineFrac of the *larger* of the two sizes, which is the extractor's own line
// test (run.go's maxf(sy, prev.height)) rather than a second opinion about the same question.
// The three other readings of "the type size" each break this line, and in a different place,
// which is why one fixture settles all of them: a 5pt subscript dropped 3pt clears half of 5pt
// but not half of 10pt, so the smaller size sees a line break where the line sees jitter. Taking
// the previous span's size splits after the digit, the current span's size splits before it, and
// the smaller of the two splits both. 49 of the corpus's 179 adjacent pairs inside a Code block
// change size, so the population is real, but none of today's steps land in the window where the
// four readings differ — this fixture is the only thing holding the choice.
func TestCodeSubscriptStaysOnItsLine(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "H"}, sp{1, 2, "2"}, sp{1, 3, "O"})
	atLines(d, 10, 700, 697, 700)
	for pi := range d.Pages {
		for si := range d.Pages[pi].Blocks[0].Spans {
			if d.Pages[pi].Blocks[0].Spans[si].MCID == 2 {
				d.Pages[pi].Blocks[0].Spans[si].Style.Size = 5
			}
		}
	}
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2, 3))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "H2O"; got != want {
		t.Errorf("listing = %q, want %q: a 3pt drop is jitter for a 10pt line", got, want)
	}
}

// The break the structure declared is not written twice.
//
// Both rules see the same listing when a producer declares one P per line *and* draws them at
// descending baselines, which is what 5 of the corpus's 6 multi-line Code blocks do. gather's
// break is a fabricated span carrying no geometry, so a rule comparing it against the next
// real line reads its zero box as a several-hundred-point jump and writes a second newline
// after every first — a blank line between every pair of listing lines.
func TestCodeDeclaredAndDrawnLinesBreakOnce(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "<<"}, sp{1, 2, ">>"})
	atLines(d, 10, 700, 688)
	code := kids(el(tag.RoleCode, 1), el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 2))
	tr := tree(el(tag.RoleH2, 1, 0), code)

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "<<\n>>"; got != want {
		t.Errorf("listing = %q, want %q: one break per line boundary", got, want)
	}
}

// indented builds a listing whose lines each carry an untagged run of leading spaces, drawn
// as the page draws them: at the block's left edge, running up to the tagged text.
//
// Its own builder because docWith gives every span MCID >= 0 and atLines puts every span at
// X0 72, and the rule under test is about a span with neither — an artifact run whose right
// edge meets the next span's left. lines are (spaces, text) pairs, one per baseline, and
// spaces is the count drawn at half the type size, which is what spaceAdvance estimates.
func indented(size float64, lines ...[2]string) *doc.Document {
	d := &doc.Document{Meta: doc.Metadata{Path: "test.pdf", Tagged: true}}
	blk := doc.Block{Role: doc.RoleParagraph}
	add := func(sp doc.Span) {
		blk.Spans = append(blk.Spans, sp)
		blk.MCIDs = append(blk.MCIDs, sp.MCID)
		blk.Box = blk.Box.Union(sp.Box)
	}
	add(doc.Span{Text: "7.4 Filters", MCID: 0, Style: doc.Style{Size: size},
		Box: geom.Rect{X0: 72, Y0: 720, X1: 300, Y1: 720 + size}})
	y := 700.0
	for i, ln := range lines {
		x := 72.0
		if n := len(ln[0]); n > 0 {
			w := float64(n) * 0.5 * size
			add(doc.Span{Text: ln[0], MCID: -1, Style: doc.Style{Size: size},
				Box: geom.Rect{X0: x, Y0: y, X1: x + w, Y1: y + size}})
			x += w
		}
		add(doc.Span{Text: ln[1], MCID: i + 1, Style: doc.Style{Size: size},
			Box: geom.Rect{X0: x, Y0: y, X1: x + 200, Y1: y + size}})
		y -= 2 * size
	}
	d.Pages = append(d.Pages, doc.Page{Number: 1, Blocks: []doc.Block{blk}})
	return d
}

// A line's leading indent is drawn outside marked content, and the listing keeps it.
//
// Both of the corpus's listing producers draw nesting as real space glyphs, and both draw some
// of those runs outside the line's marked content, where take cannot claim them. 23 runs are
// that shape on disk: PDF-Declarations' whole XML sample, whose 25 lines came out flush-left,
// and one line of WTPDF's. The spaces are on the page with an advance each, so keeping them is
// reporting what was drawn rather than inferring an indent.
func TestCodeKeepsAnUntaggedLeadingIndent(t *testing.T) {
	d := indented(10, [2]string{"", "<rdf:RDF>"}, [2]string{"  ", "<rdf:Bag>"})
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "<rdf:RDF>\n  <rdf:Bag>"; got != want {
		t.Errorf("listing = %q, want %q: the indent is drawn, not inferred", got, want)
	}
}

// A recovered indent still breaks the line it opens.
//
// The reason the indexed span is a copy carrying the key's own MCID rather than the artifact
// itself: newLine skips MCID < 0, so an indent left at -1 answers false on both sides of
// itself, and every break in the listing disappears. Measured on PDF-Declarations, which
// collapsed from 25 nested lines to one 892-character line — the exact defect breakAtBaselines
// was written to undo, reintroduced by the fix for the indent.
func TestARecoveredIndentDoesNotSuppressTheLineBreak(t *testing.T) {
	d := indented(10, [2]string{"  ", "<a>"}, [2]string{"    ", "<b>"}, [2]string{"  ", "</a>"})
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2, 3))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "  <a>\n    <b>\n  </a>"; got != want {
		t.Errorf("listing = %q, want %q: an indent is part of its line, not a barrier", got, want)
	}
}

// A run of spaces standing away from the text after it is positioning, not an indent.
//
// This is the condition that separates the 23 indents from the 43 other untagged whitespace
// spans on disk — a dotted leader's trailing space, the space beside a bullet glyph, a TOC
// entry's padding. The band is empty for 8.2×: every indent meets its text within 0.243pt and
// every other run with a same-line successor stands at least 2.000pt off. Expressed as the
// negation of gapSpace's own space test, so what this calls attached is what that rule would
// refuse to put a space into.
//
// The gap is 1.6pt against a threshold of 1.5, which is 1.07× and deliberately that tight. The
// first version of this fixture stood a full 14pt clear — 9× the threshold — and three mutants
// of the threshold itself survived it while adopting 8 more corpus runs each: LineFrac for
// SpaceFrac, WideSpaceFrac for SpaceFrac, and the em in place of the space advance are all
// wider, and nothing between 1.5 and 14 could tell them apart. A fixture that clears a
// threshold by an order of magnitude tests the sign of the comparison, not its constant.
func TestADetachedWhitespaceRunIsNotAnIndent(t *testing.T) {
	d := indented(10, [2]string{"", "1 Scope"})
	blk := &d.Pages[0].Blocks[0]
	// A leader's trailing space, at the left edge but standing off the text after it.
	pad := doc.Span{Text: "  ", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 72, Y0: 680, X1: 82, Y1: 690}}
	num := doc.Span{Text: "7", MCID: 2, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 83.6, Y0: 680, X1: 88.6, Y1: 690}}
	blk.Spans = append(blk.Spans, pad, num)
	blk.MCIDs = append(blk.MCIDs, -1, 2)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "1 Scope\n7"; got != want {
		t.Errorf("listing = %q, want %q: 1.6pt on a 1.5pt threshold is positioning", got, want)
	}
}

// Whitespace in the middle of a line is not a leading indent.
//
// "Leading" is a position, not a character class, and 31 of the 43 non-indent runs on disk are
// mid-line, and the narrowest of them is what sets the lower edge of the 8.2× band: 2.000pt.
// Asserted on leadingIndent directly rather than through the output, because what an
// artifact run leaves behind when it is correctly rejected is a gap, and spaceAtGaps then puts
// a space there on its own — so the rendered text is the same either way and cannot tell the
// two rules apart. The distinction that matters is whose space it is: a gap-inferred one is a
// single space wherever the run was wide, an adopted one is the run itself.
func TestMidLineWhitespaceIsNotAnIndent(t *testing.T) {
	d := indented(10, [2]string{"", "<a>"})
	blk := &d.Pages[0].Blocks[0]
	// Same baseline as the line already open, attached to the span after it.
	mid := doc.Span{Text: "    ", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 272, Y0: 700, X1: 292, Y1: 710}}
	tail := doc.Span{Text: "<b>", MCID: 2, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 292, Y0: 700, X1: 315, Y1: 710}}
	blk.Spans = append(blk.Spans, mid, tail)
	blk.MCIDs = append(blk.MCIDs, -1, 2)

	if nx, ok := leadingIndent(blk, 2); ok {
		t.Errorf("leadingIndent = %q, want rejected: only a line's first run is its indent", nx.Text)
	}

	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))
	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "<a> <b>"; got != want {
		t.Errorf("listing = %q, want %q: the gap rule fills it with one space, not four", got, want)
	}
}

// A run of drawn text is not an indent, whatever its geometry.
//
// The condition that makes this rule about whitespace rather than about position: an untagged run
// can be anything the producer drew outside marked content, and the two the corpus has are both
// whitespace only by coincidence of what these files contain. 0 of the 66 untagged runs on disk
// are non-whitespace *and* first on a line *and* attached, so the corpus cannot fail this and a
// fixture has to. Adopting text would be worse than dropping a space: it would move real
// characters into a block the producer did not put them in.
func TestUntaggedTextIsNotAnIndent(t *testing.T) {
	d := indented(10, [2]string{"", "<a>"})
	blk := &d.Pages[0].Blocks[0]
	// A drawn page number at the line's left edge, running right up to the tagged text.
	num := doc.Span{Text: "42", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 40, Y0: 680, X1: 50, Y1: 690}}
	txt := doc.Span{Text: "<b>", MCID: 2, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 50, Y0: 680, X1: 73, Y1: 690}}
	blk.Spans = append(blk.Spans, num, txt)
	blk.MCIDs = append(blk.MCIDs, -1, 2)

	if nx, ok := leadingIndent(blk, 2); ok {
		t.Errorf("leadingIndent = %q, want rejected: an untagged run of text is not an indent", nx.Text)
	}

	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))
	out, _ := Tagged(d, tr, DefaultOptions)

	if got := out.Sections[0].Blocks[0].Text(); strings.Contains(got, "42") {
		t.Errorf("listing = %q: drawn text no element claims was adopted into the block", got)
	}
}

// An empty span is not an indent, and the guard that says so is not redundant.
//
// TrimFunc of the empty string is the empty string, so "no non-space characters left" is true of a
// span with no characters at all: dropping the sp.Text == "" half of the test adopts it. That
// mutant survived a full run, and the equivalence argument for it — the two halves overlap — is
// simply wrong on this one input. The corpus has 0 empty untagged spans (extract emits none), so
// this is a guard rather than a measured case, and the same one gapSpace makes on the same
// quantity for the same reason: an empty span has no width to indent by, and adopting it consumes
// an artifact and copies it into a block to no effect.
func TestAnEmptySpanIsNotAnIndent(t *testing.T) {
	d := indented(10, [2]string{"", "<a>"})
	blk := &d.Pages[0].Blocks[0]
	empty := doc.Span{Text: "", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 72, Y0: 680, X1: 72, Y1: 690}}
	txt := doc.Span{Text: "<b>", MCID: 2, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 72, Y0: 680, X1: 95, Y1: 690}}
	blk.Spans = append(blk.Spans, empty, txt)
	blk.MCIDs = append(blk.MCIDs, -1, 2)

	if nx, ok := leadingIndent(blk, 2); ok {
		t.Errorf("leadingIndent = %q, want rejected: an empty span has no width to indent by", nx.Text)
	}
}

// An indent is whitespace by rune, so a run of U+00A0 is one.
//
// The reason the test is TrimFunc(unicode.IsSpace) and not a byte scan for ' ' and '\t': a
// producer can indent with any space glyph its font has, and a listing indented with no-break or
// en spaces is indented. The corpus has 0 such runs — all 23 indents on disk are U+0020 — so this
// is the rule extract's own endsWithSpace makes on the same question (run.go:1143), pinned by a
// fixture because the files cannot pin it.
func TestANonASCIISpaceIsAnIndent(t *testing.T) {
	d := indented(10, [2]string{"  ", "<a>"})
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "  <a>"; got != want {
		t.Errorf("listing = %q, want %q: any space rune indents, not just ' '", got, want)
	}
}

// An indent needs a tagged span to attach to, and the last run on a block has none.
//
// Two guards, both unreachable from the corpus — every one of the 23 indents on disk has a tagged
// successor, and only 1 untagged run of the 66 is followed by another untagged one. Without the
// bounds check the rule indexes past the block; without the MCID check it keys an indent to a span
// that has no key, which is key{page, -1} and a bucket take never looks up, so the spaces would be
// consumed and then dropped — silently losing content rather than crashing.
func TestAnIndentWithNoTaggedSuccessorIsRejected(t *testing.T) {
	d := indented(10, [2]string{"", "<a>"})
	blk := &d.Pages[0].Blocks[0]
	trailing := doc.Span{Text: "  ", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 40, Y0: 680, X1: 50, Y1: 690}}
	blk.Spans = append(blk.Spans, trailing)
	blk.MCIDs = append(blk.MCIDs, -1)

	// Last span in the block: nothing follows it at all.
	if nx, ok := leadingIndent(blk, 2); ok {
		t.Errorf("leadingIndent = %q, want rejected: nothing follows the last span", nx.Text)
	}

	// A second untagged run after it: something follows, but nothing that owns a key.
	another := doc.Span{Text: "  ", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 50, Y0: 680, X1: 60, Y1: 690}}
	blk.Spans = append(blk.Spans, another)
	blk.MCIDs = append(blk.MCIDs, -1)

	if nx, ok := leadingIndent(blk, 2); ok {
		t.Errorf("leadingIndent = %q, want rejected: an untagged successor has no key to join", nx.Text)
	}
}

// An indent belongs to the line it was drawn on, not to the next one down.
//
// Attachment is measured in x alone, so without the same-line test a run at the end of one line
// and the first span of the line below it read as attached whenever their x-coordinates happen to
// meet — which is exactly what a listing's line ends look like, since every line starts at the
// same left margin. The corpus has no instance because its indents sit 0.243pt from their text at
// most, but the failure mode is a whole line's indent taken from the line above it.
//
// The run is alone on its baseline, and that detail is what makes the fixture reach the rule it is
// named for. A first version put it at the end of a line that already held text, and dropping the
// same-line guard still rejected it — the first-on-baseline loop got there first, on the span it
// shared a line with. Two guards that both decline leave neither one tested.
func TestAnIndentDoesNotReachTheLineBelow(t *testing.T) {
	d := &doc.Document{Meta: doc.Metadata{Path: "test.pdf", Tagged: true},
		Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{{
			Role:  doc.RoleParagraph,
			Box:   geom.Rect{X0: 62, Y0: 680, X1: 300, Y1: 730},
			MCIDs: []int{0, -1, 1},
			Spans: []doc.Span{
				{Text: "7.4 Filters", MCID: 0, Style: doc.Style{Size: 10},
					Box: geom.Rect{X0: 72, Y0: 720, X1: 300, Y1: 730}},
				// Alone on its baseline, ending exactly where the line below begins.
				{Text: "  ", MCID: -1, Style: doc.Style{Size: 10},
					Box: geom.Rect{X0: 62, Y0: 700, X1: 72, Y1: 710}},
				{Text: "<b>", MCID: 1, Style: doc.Style{Size: 10},
					Box: geom.Rect{X0: 72, Y0: 680, X1: 95, Y1: 690}},
			},
		}}}}}
	blk := &d.Pages[0].Blocks[0]

	if nx, ok := leadingIndent(blk, 1); ok {
		t.Errorf("leadingIndent = %q, want rejected: 20pt down is another line", nx.Text)
	}
}

// An indent that overlaps the text it indents is still attached to it.
//
// Attachment is |X0 - X1| and not X0 - X1, because the quantity that matters is how far apart the
// two runs are and a producer can draw them overlapping: 4 of the 23 indents on disk do, up to
// 0.243pt, which is where the band's upper edge comes from. A signed test reads every one of those
// as attached-by-a-mile and would pass here by accident, so the fixture overlaps by more than a
// space instead — 3pt on a 1.5pt threshold, which a signed comparison accepts and this rejects.
func TestAnOverlappingRunIsMeasuredByDistance(t *testing.T) {
	d := indented(10, [2]string{"", "<a>"})
	blk := &d.Pages[0].Blocks[0]
	wide := doc.Span{Text: "  ", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 72, Y0: 680, X1: 95, Y1: 690}}
	txt := doc.Span{Text: "<b>", MCID: 2, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 92, Y0: 680, X1: 115, Y1: 690}}
	blk.Spans = append(blk.Spans, wide, txt)
	blk.MCIDs = append(blk.MCIDs, -1, 2)

	if nx, ok := leadingIndent(blk, 2); ok {
		t.Errorf("leadingIndent = %q, want rejected: a 3pt overlap is not a 0.2pt one", nx.Text)
	}
}

// The gap is measured against the text's own space, not the indent's.
//
// Which span's type size sets the threshold is a real choice and the corpus cannot make it: all 23
// adopted pairs on disk are set in one size, so the two readings agree on every one of them. The
// answer here is the one gapSpace already documents for the same quantity — the *following* span's
// advance, matching run.go:448, where extract reads it per glyph as it draws. An indent's own size
// is the wrong instrument besides: a run of spaces set in a display size would license a gap wide
// enough to swallow a word.
//
// 6pt spaces ahead of 20pt text: 1.5pt of gap is under the following span's threshold of 3.0 and
// over its own of 0.9, so the two readings disagree and the fixture picks one.
func TestTheGapIsMeasuredAgainstTheIndentedText(t *testing.T) {
	d := &doc.Document{Meta: doc.Metadata{Path: "test.pdf", Tagged: true},
		Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{{
			Role:  doc.RoleParagraph,
			Box:   geom.Rect{X0: 72, Y0: 680, X1: 300, Y1: 740},
			MCIDs: []int{0, -1, 1},
			Spans: []doc.Span{
				{Text: "7.4 Filters", MCID: 0, Style: doc.Style{Size: 20},
					Box: geom.Rect{X0: 72, Y0: 720, X1: 300, Y1: 740}},
				{Text: "  ", MCID: -1, Style: doc.Style{Size: 6},
					Box: geom.Rect{X0: 72, Y0: 690, X1: 78, Y1: 696}},
				{Text: "<a>", MCID: 1, Style: doc.Style{Size: 20},
					Box: geom.Rect{X0: 79.5, Y0: 690, X1: 130, Y1: 710}},
			},
		}}}}}
	blk := &d.Pages[0].Blocks[0]

	if _, ok := leadingIndent(blk, 1); !ok {
		t.Error("leadingIndent rejected 1.5pt before 20pt text: the threshold is the text's space, not the indent's")
	}
}

// A gap narrower than a space is attached, and zero is not the threshold.
//
// 18 of the 23 indents on disk stop short of their text rather than meeting it, by up to 0.028pt,
// which is rounding in the producer's advance arithmetic and not a space; 4 overlap it and exactly
// 1 meets it. So an exact test — the whole tolerance replaced by zero — would keep that one and
// drop the 18, which is most of what this rule exists for. The fixture leaves a gap that is real
// but sub-space: 1pt against a 1.5pt threshold.
func TestASubSpaceGapIsStillAttached(t *testing.T) {
	d := indented(10, [2]string{"", "<a>"})
	blk := &d.Pages[0].Blocks[0]
	ind := doc.Span{Text: "  ", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 72, Y0: 680, X1: 82, Y1: 690}}
	txt := doc.Span{Text: "<b>", MCID: 2, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 83, Y0: 680, X1: 106, Y1: 690}}
	blk.Spans = append(blk.Spans, ind, txt)
	blk.MCIDs = append(blk.MCIDs, -1, 2)

	if _, ok := leadingIndent(blk, 2); !ok {
		t.Error("leadingIndent rejected a 1pt gap on a 1.5pt threshold: 19 of 23 indents are this shape")
	}
}

// A recovered indent is not also left in Unplaced.
//
// The artifact is marked consumed where its copy is indexed, so the recovery pass no longer
// reaches it. Without that the same spaces appear twice in one outline: once inside the listing
// and once in the unplaced text of the block they were drawn in.
//
// The block has to hold something else unclaimed for that to be observable, and this is the shape
// that took a mutation run to find. A block whose *only* survivor is the indent rebuilds as
// whitespace and unplaced drops it at the len(keep.Spans) == 0 guard, so the first version of this
// test — an indent alone in its block — asserted something that could not happen and the mutant
// that forgets to consume survived it. Here a drawn page number is unclaimed too, so the block
// reaches Unplaced on its own merits and the indent either rides along or does not.
func TestARecoveredIndentIsNotAlsoUnplaced(t *testing.T) {
	d := indented(10, [2]string{"  ", "<a>"})
	blk := &d.Pages[0].Blocks[0]
	// Drawn text no element claims, on its own line: enough to keep the rebuilt block.
	folio := doc.Span{Text: "42", MCID: -1, Style: doc.Style{Size: 10},
		Box: geom.Rect{X0: 300, Y0: 40, X1: 310, Y1: 50}}
	blk.Spans = append(blk.Spans, folio)
	blk.MCIDs = append(blk.MCIDs, -1)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "  <a>"; got != want {
		t.Fatalf("listing = %q, want %q", got, want)
	}
	found := false
	for i := range out.Unplaced {
		for _, b := range out.Unplaced[i].Blocks {
			found = true
			if strings.Contains(b.Text(), "  ") {
				t.Errorf("unplaced = %q: the indent is in the listing and here both", b.Text())
			}
		}
	}
	if !found {
		t.Error("no unplaced block: the fixture no longer reaches the recovery pass at all")
	}
}

// An indent whose line no element claims is not emitted on its own.
//
// The consequence of keying the indent to the following span rather than tracking document
// order, and the behaviour to want: spaces with no line to indent are not content. They stay
// unconsumed, so the recovery pass still accounts for them.
func TestAnIndentWithoutItsLineIsNotEmitted(t *testing.T) {
	d := indented(10, [2]string{"  ", "<a>"})
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1))

	out, _ := Tagged(d, tr, DefaultOptions)

	for _, b := range out.Sections[0].Blocks {
		if strings.Contains(b.Text(), "  ") {
			t.Errorf("block = %q: an unclaimed line's indent was emitted", b.Text())
		}
	}
}

// A paragraph drawn across several lines is still joined with a space, not broken.
//
// The baseline rule is confined to a role the producer declared, and this is what that buys:
// every wrapped prose paragraph in the corpus spans several baselines, so a rule keyed on
// geometry alone would put a hard break inside all of them.
func TestParagraphLinesDoNotBreakOnBaseline(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "a stream shall "}, sp{1, 2, "be indirect"})
	atLines(d, 10, 700, 688)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "a stream shall be indirect"; got != want {
		t.Errorf("paragraph = %q, want %q", got, want)
	}
}

// A cell's paragraphs still join with nothing, which is what keeps this rule off prose.
//
// 752 cells across the 18 tagged files hold more than one P, and their text is one run the
// producer broke across lines, so a newline between them would put a hard break inside a table
// cell. The two predicates are separate for exactly this reason, and widening linesText to
// every wrapping role is the mutation this kills.
func TestCellParagraphsJoinWithoutABreak(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "name or "}, sp{1, 2, "array"})
	cell := kids(el(tag.RoleTD, 1), el(tag.RoleP, 1, 1), el(tag.RoleP, 1, 2))
	tbl := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), cell))
	tr := tree(el(tag.RoleH2, 1, 0), tbl)

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "name or array"; got != want {
		t.Errorf("cell = %q, want %q", got, want)
	}
}

// atX gives the spans after the heading an X range each, so a fixture can express a horizontal
// gap. atLines fixes every span at 72..300, which is the right default for a rule about
// baselines and useless for one about the space between two runs on one of them.
//
// Pairs, in the order the spans were added: X0 then X1 per span.
func atX(d *doc.Document, xs ...float64) {
	i := 0
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			spans := d.Pages[pi].Blocks[bi].Spans
			for si := range spans {
				if spans[si].MCID == 0 {
					continue
				}
				if i+1 >= len(xs) {
					panic("atX: fewer bounds than spans")
				}
				spans[si].Box.X0, spans[si].Box.X1 = xs[i], xs[i+1]
				i += 2
			}
		}
	}
	if i != len(xs) {
		panic("atX: more bounds than spans")
	}
}

// A contents entry whose dotted leader the tree does not name still separates from its page
// number.
//
// The defect this fixes, at the width it has on disk: PDF-Declarations draws "2 Scope", a
// leader, then "1" at a 395.57pt gap — 219.76 times the space test — and the leader is an
// artifact, so newIndex never indexes it and take cannot return it. Without the rule the entry
// reads "2 Scope1", which is what three of that file's thirteen entries did.
func TestGapAcrossADroppedLeaderKeepsItsSpace(t *testing.T) {
	d := docWith(sp{1, 0, "Table of Contents"}, sp{1, 1, "2 Scope"}, sp{1, 2, "1"})
	atLines(d, 10.94, 682.8, 682.8)
	atX(d, 72, 121.2, 516.7, 522.7)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "2 Scope 1"; got != want {
		t.Errorf("entry = %q, want %q: a 395pt gap is a word boundary", got, want)
	}
}

// The narrowest real gap in the corpus is a space, and it is what pins the space advance.
//
// ISO 32000-2's L*a*b* definition draws "× (𝑥 −" at 11.04pt then "4 29" at 8.04pt, 2.313pt
// apart on one baseline. That is 1.918 times SpaceFrac of the 8.04pt span's space advance and
// only 0.959 of SpaceFrac of its em — so the first version of this rule, which measured against
// the em, left it joined as "(𝑥 −4 29)" while fixing the identical join earlier on the same
// output line. No fixture could have caught that: every other one in this file is either far
// above both thresholds or far below. This is the only geometry on disk that separates them,
// which is why it is stated in points from the file rather than in round numbers.
func TestNarrowGapAtARealFormulaIsASpace(t *testing.T) {
	prev := doc.Span{Text: "× (𝑥 −", MCID: 1,
		Box:   geom.Rect{X0: 300, Y0: 500, X1: 330, Y1: 511.04},
		Style: doc.Style{Size: 11.04}}
	cur := doc.Span{Text: "4 29", MCID: 2,
		Box:   geom.Rect{X0: 332.313, Y0: 500, X1: 350, Y1: 508.04},
		Style: doc.Style{Size: 8.04}}

	if !gapSpace(&prev, &cur) {
		t.Error("2.313pt on an 8.04pt span is 1.918 space widths and must space; " +
			"measuring against the em scores it 0.959 and joins it")
	}
}

// The widest gap the corpus joins is still a join, which is the other half of the boundary.
//
// A subscript returning to body size is the densest cluster in the distribution: 68 pairs
// between 0.404 and 0.435 of the threshold, all of them ISO 32000-2 mathematical variables
// meeting punctuation, and the widest is "5" at 6.96pt then ")" at 9.96pt with 0.650pt between.
// It has to stay joined — "(𝑥5)" is one token — so the rule's headroom is measured from here
// rather than asserted: 0.435 below against 1.918 above is the whole empty band, and a fixture
// on only one side of it would let the threshold drift into the other.
func TestWidestJoinedSubscriptGapStaysJoined(t *testing.T) {
	prev := doc.Span{Text: "5", MCID: 1,
		Box:   geom.Rect{X0: 300, Y0: 500, X1: 305, Y1: 506.96},
		Style: doc.Style{Size: 6.96}}
	cur := doc.Span{Text: ")", MCID: 2,
		Box:   geom.Rect{X0: 305.65, Y0: 500, X1: 310, Y1: 509.96},
		Style: doc.Style{Size: 9.96}}

	if gapSpace(&prev, &cur) {
		t.Error("0.650pt on a 9.96pt span is 0.435 of the threshold: a subscript touching " +
			"its bracket is one token, not two words")
	}
}

// Two runs the producer already separated get one space, not two.
//
// 25892 of the extractor's inferred spaces follow text that already ends in whitespace
// (run.go:499), so this is the common case rather than an edge: ten of PDF-Declarations' own
// thirteen entries have the space on the title span and must come out unchanged.
func TestGapAfterASpaceAddsNothing(t *testing.T) {
	d := docWith(sp{1, 0, "Table of Contents"}, sp{1, 1, "3 Normative references "}, sp{1, 2, "2"})
	atLines(d, 10.94, 661.4, 661.4)
	atX(d, 72, 200.4, 516.7, 522.7)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "3 Normative references 2"; got != want {
		t.Errorf("entry = %q, want %q: the space is already there", got, want)
	}
}

// A space that leads the following span counts too, since a fragment carries its own leading
// space (run.go:517) and either side can hold the boundary.
func TestGapBeforeASpaceAddsNothing(t *testing.T) {
	d := docWith(sp{1, 0, "Table of Contents"}, sp{1, 1, "4 Terms"}, sp{1, 2, " 2"})
	atLines(d, 10.94, 640, 640)
	atX(d, 72, 121.2, 516.7, 522.7)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "4 Terms 2"; got != want {
		t.Errorf("entry = %q, want %q", got, want)
	}
}

// A non-breaking space is a boundary, so a byte scan for ' ' would add a second one.
//
// The rule decodes a rune where a byte test would see only the last byte of U+00A0 and call it
// not-a-space. Nothing on the corpus has this shape, which is exactly why it needs a fixture.
func TestGapAfterANonBreakingSpaceAddsNothing(t *testing.T) {
	d := docWith(sp{1, 0, "Table of Contents"}, sp{1, 1, "Table 1"}, sp{1, 2, "5"})
	atLines(d, 10.94, 620, 620)
	atX(d, 72, 121.2, 516.7, 522.7)
	d.Pages[0].Blocks[0].Spans[1].Text = "Table 1 "
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "Table 1 5"; got != want {
		t.Errorf("entry = %q, want %q: U+00A0 is already a boundary", got, want)
	}
}

// A kerned pair on one line is not a gap.
//
// This is the whole corpus on the other side of the rule: 19223 same-line pairs join with no
// space, p50 at 0.014 of the space test and p99 at 0.198, and every one of them must stay
// joined. A word split across two spans by a style change is the shape — "PDF" then "2.0" — and
// a rule that spaced those would corrupt far more than it fixed.
func TestTightPairOnOneLineStaysJoined(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "PDF"}, sp{1, 2, "2.0"})
	atLines(d, 10, 700, 700)
	atX(d, 72, 90, 90.2, 108)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "PDF2.0"; got != want {
		t.Errorf("pair = %q, want %q: 0.2pt is kerning, not a space", got, want)
	}
}

// Overlapping runs are not a gap either. A negative distance must not read as a wide one, which
// is what dropping the comparison's direction would do.
func TestOverlappingPairStaysJoined(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "super"}, sp{1, 2, "script"})
	atLines(d, 10, 700, 700)
	atX(d, 72, 120, 100, 140)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "superscript"; got != want {
		t.Errorf("pair = %q, want %q: an overlap is not a gap", got, want)
	}
}

// A gap between a span and one on the line *above* it is not a space either.
//
// The same-line test takes the magnitude of the baseline step, so it declines in both
// directions. A signed comparison reads an upward step as negative and therefore as
// same-line, which would put a space where a paragraph's second line rejoins the first —
// or, on the corpus's one cross-page listing, inside a 681pt rise.
func TestGapFromTheLineAboveIsNotASpace(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "second line"}, sp{1, 2, "first line"})
	atLines(d, 10, 688, 700)
	atX(d, 72, 130, 400, 460)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "second linefirst line"; got != want {
		t.Errorf("pair = %q, want %q: a 12pt rise is another line in either direction", got, want)
	}
}

// The same-line test measures against the type size itself, not against the space advance.
//
// The two thresholds in this rule read the same field and scale it differently — LineFrac of the
// larger size, SpaceFrac of the following span's space advance — and nothing but a fixture at the
// boundary keeps the second scaling from creeping into the first. At LineFrac 0.50 a 10pt pair
// admits a 5pt step; passing the size through spaceAdvance would halve that to 2.5pt and split
// the line, which is why the step here is 4pt rather than the 4pt-of-24 the superscript fixture
// below uses: that one has enough headroom to survive the halving unnoticed.
func TestLineTestMeasuresTheSizeNotTheSpaceAdvance(t *testing.T) {
	prev := doc.Span{Text: "a", MCID: 1,
		Box:   geom.Rect{X0: 100, Y0: 500, X1: 110, Y1: 510},
		Style: doc.Style{Size: 10}}
	cur := doc.Span{Text: "b", MCID: 2,
		Box:   geom.Rect{X0: 120, Y0: 504, X1: 130, Y1: 514},
		Style: doc.Style{Size: 10}}

	if !gapSpace(&prev, &cur) {
		t.Error("a 4pt step on a 10pt pair is one line (LineFrac 0.50 allows 5pt), and the " +
			"10pt gap is a space: scaling the line test by the space advance would allow only 2.5pt")
	}
}

// The same-line test takes the larger of the two sizes, which is the extractor's own reading
// (maxf(sy, prev.height) at run.go:462) and the conservative one.
//
// Where the space test reads the following span, this one reads the pair, so the two thresholds
// need separate fixtures. A 24pt run and a 6pt one 4pt apart vertically: LineFrac is 0.50, so
// the larger size allows 12pt of step and calls it one line, while cur and the smaller allow 3pt
// and call it two. That is a superscript, and reading the small span would space every raised
// digit away from the word it belongs to — the same failure newLine's threshold avoids, in the
// other rule.
func TestSuperscriptIsOnItsLineForTheGapRule(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "Note"}, sp{1, 2, "1"})
	atLines(d, 24, 700, 704)
	atX(d, 72, 120, 130, 134)
	for si := range d.Pages[0].Blocks[0].Spans {
		if d.Pages[0].Blocks[0].Spans[si].MCID == 2 {
			d.Pages[0].Blocks[0].Spans[si].Style.Size = 6
		}
	}
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "Note 1"; got != want {
		t.Errorf("pair = %q, want %q: a 4pt rise is one line for a 24pt run", got, want)
	}
}

// The mirror of the fixture above, with the large span second, which is what makes the reading
// "the larger of the two" rather than "the first one".
//
// 6pt then 24pt, 4pt of step: prev allows 3pt and reads two lines, the larger allows 12 and
// reads one. Both halves are needed because either alone passes under prev-or-max.
func TestSuperscriptBeforeItsWordIsOnOneLine(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "1"}, sp{1, 2, "Note"})
	atLines(d, 6, 704, 700)
	atX(d, 72, 76, 86, 130)
	for si := range d.Pages[0].Blocks[0].Spans {
		if d.Pages[0].Blocks[0].Spans[si].MCID == 2 {
			d.Pages[0].Blocks[0].Spans[si].Style.Size = 24
		}
	}
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "1 Note"; got != want {
		t.Errorf("pair = %q, want %q: the larger size decides, whichever side it is on", got, want)
	}
}

// A span with no text is not one side of a join.
//
// take returns empty spans — 943 of them on the corpus, all in ISO_32000-2, where a marked
// content sequence draws no glyphs — and each sits at the left margin, so the gap to the text
// after it is the whole indent. Spacing that would put a leading space on the block, and the
// separator between two adjacent empties would be a space between nothing and nothing.
func TestGapBesideAnEmptySpanAddsNothing(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, ""}, sp{1, 2, "PDF/A (ISO 19005)"})
	atLines(d, 10, 700, 700)
	atX(d, 72, 72, 84.92, 200)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "PDF/A (ISO 19005)"; got != want {
		t.Errorf("block = %q, want %q: an empty side is not a boundary", got, want)
	}
}

// The two geometric rules run in a fixed order, and a listing that both could touch comes out
// with one break and no space in front of it.
//
// spaceAtGaps runs first so that it sees only spans the page drew: breakAtBaselines inserts a
// newline span, and running after it would leave that fabricated span as one side of every
// join. The MCID guard makes each rule skip the other's insertion, so the order is belt and
// braces — but the guard is what holds it, and a listing whose lines are also horizontally
// offset is where the two would collide.
func TestListingGetsABreakAndNoSpace(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "<< /Type /Page"}, sp{1, 2, "/Parent 2 0 R"})
	atLines(d, 10, 700, 688)
	atX(d, 72, 150, 400, 470)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "<< /Type /Page\n/Parent 2 0 R"; got != want {
		t.Errorf("listing = %q, want %q: one break, no space before it", got, want)
	}
}

// A gap exactly equal to the threshold is not a space.
//
// Strictly greater, matching needSpace (run.go:474), and the boundary is where the two could
// silently disagree: the threshold is SpaceFrac of the space advance, and at 0.30 of half a
// 20pt em that is exactly 3pt, which is representable, so a `>=` here would space a pair the
// extractor joins. Stated as 20pt rather than 10pt for that reason — the em is not the unit
// the rule measures in, and a fixture written in ems is what hid the 4× error this boundary
// now pins. Asserted through gapSpace rather than Tagged because the equality has to be
// exact, and a fixture's box arithmetic is the only place that can be guaranteed.
func TestGapExactlyAtTheThresholdIsNotASpace(t *testing.T) {
	prev := doc.Span{Text: "a", MCID: 1,
		Box:   geom.Rect{X0: 90, Y0: 100, X1: 100, Y1: 110},
		Style: doc.Style{Size: 20}}
	cur := doc.Span{Text: "b", MCID: 2,
		Box:   geom.Rect{X0: 103, Y0: 100, X1: 113, Y1: 110},
		Style: doc.Style{Size: 20}}

	if gapSpace(&prev, &cur) {
		t.Error("a 3pt gap is exactly 0.30 of a 20pt span's 10pt space, and the test is strictly greater")
	}
	// A hair over is, so the boundary is the boundary and not an off-by-one in the fixture.
	over := cur
	over.Box.X0 = 103.01
	if !gapSpace(&prev, &over) {
		t.Error("3.01pt is over the threshold and should space")
	}
}

// A span with no type size does not get a gap read into it.
//
// At size 0 both thresholds are 0, so every positive gap is a space and every unequal baseline is
// another line — the two questions decided the wrong way at once. doc.Style.Size is sy from the
// composed text matrix and nothing clamps it, so a Tf of 0 produces this; the corpus has 0 of
// 97452 such spans, which makes it a guard rather than a case, and unreachable from Tagged for
// the same reason the fabricated-span guard is.
func TestGapSpaceDeclinesWithoutATypeSize(t *testing.T) {
	sized := doc.Span{Text: "y", MCID: 7,
		Box:   geom.Rect{X0: 400, Y0: 0, X1: 440, Y1: 10},
		Style: doc.Style{Size: 10}}
	none := doc.Span{Text: "x", MCID: 6,
		Box: geom.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10}}

	if gapSpace(&none, &sized) {
		t.Error("prev has no size, so the pair has no line test")
	}
	// Placed *after* the sized span, so the gap is positive and the guard is the only thing
	// that can decline: with cur unsized the threshold is 0, which any gap clears.
	after := doc.Span{Text: "x", MCID: 6,
		Box: geom.Rect{X0: 460, Y0: 0, X1: 470, Y1: 10}}
	if gapSpace(&sized, &after) {
		t.Error("cur has no size, so the gap has nothing to be a fraction of")
	}
	negative := none
	negative.Style.Size = -10
	if gapSpace(&negative, &sized) {
		t.Error("a negative size is not a smaller threshold, it is no threshold")
	}
	// The same pair with a size is a gap, so the guard is declining on the size and not on
	// something else about these spans.
	withSize := none
	withSize.Style.Size = 10
	if !gapSpace(&withSize, &sized) {
		t.Error("two sized spans 390pt apart are a gap; the guard is doing nothing")
	}
}

// A fabricated span is never one side of a gap, whatever its text.
//
// Called directly rather than through Tagged, because the state cannot be built from a structure
// tree: every span the index returns has a real identifier, and every fabricated one this
// package inserts holds whitespace, which the space test rejects before the geometry is read. So
// the guard is unreachable from Tagged today and decisive in itself — the same inputs answer
// false with it and true without, since a zero box against a span at X0 400 reads as a 400pt
// gap. Pinning it here says the rule is geometric on purpose, rather than leaving it to be
// covered by a coincidence about the three insertions that exist now.
func TestGapSpaceIgnoresAFabricatedSpan(t *testing.T) {
	box := geom.Rect{X0: 400, Y0: 0, X1: 440, Y1: 10}
	// Sized, so that the zero-size guard is not what declines and this test still measures the
	// identifier. A fabricated span carries no style in practice, which is why the two guards
	// have to be told apart deliberately.
	fab := doc.Span{Text: "x", MCID: -1, Style: doc.Style{Size: 10}}
	real := doc.Span{Text: "y", MCID: 7, Box: box, Style: doc.Style{Size: 10}}

	if gapSpace(&fab, &real) {
		t.Error("a fabricated prev span has no geometry, so it cannot open a gap")
	}
	if gapSpace(&real, &fab) {
		t.Error("a fabricated cur span has no geometry, so it cannot close a gap")
	}
	// The same pair with identifiers is a gap, which is what makes the guard load-bearing
	// rather than a restatement of the space test.
	withID := fab
	withID.MCID = 5
	if !gapSpace(&withID, &real) {
		t.Error("two real spans 400pt apart are a gap; the guard is doing nothing")
	}
}

// A listing with both a wide gap on one line and a break to the next gets exactly one of each,
// and the inserted space is invisible to the rule that writes the break.
//
// This is where the fabricated span's MCID earns itself. A real identifier on it would make
// newLine read its zero box as a 700pt step from the line either side, so the one space would
// become two spurious breaks — and a listing is the only role where both rules run, so it is the
// only place the collision can be seen.
func TestListingSpacesAGapAndBreaksALine(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"},
		sp{1, 1, "/Type"}, sp{1, 2, "/Page"}, sp{1, 3, "/Parent 2 0 R"})
	atLines(d, 10, 700, 700, 688)
	atX(d, 72, 100, 140, 180, 72, 150)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2, 3))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "/Type /Page\n/Parent 2 0 R"; got != want {
		t.Errorf("listing = %q, want %q: one space, one break, nothing else", got, want)
	}
}

// The gap is measured against the size of the span that follows it, which is the size the
// extractor's own space test reads (run.go:448, per glyph and never maximised).
//
// The four readings of "the type size" — cur, prev, the larger, the smaller — put the threshold
// in four places, so one gap between two differently-sized spans settles all four. A 24pt run
// followed by a 6pt one, with a 2.5pt gap: SpaceFrac is 0.30, so cur wants 1.8 and spaces it,
// while prev and max want 7.2 and join it. The corpus cannot make this call — its three real
// cases are all 10.94pt on both sides — so this fixture is the only thing holding it.
func TestGapIsMeasuredAgainstTheFollowingSpan(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "Note"}, sp{1, 2, "1"})
	atLines(d, 24, 700, 700)
	atX(d, 72, 120, 122.5, 126)
	for si := range d.Pages[0].Blocks[0].Spans {
		if d.Pages[0].Blocks[0].Spans[si].MCID == 2 {
			d.Pages[0].Blocks[0].Spans[si].Style.Size = 6
		}
	}
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "Note 1"; got != want {
		t.Errorf("pair = %q, want %q: 2.5pt clears 0.30 of 6pt, not of 24pt", got, want)
	}
}

// The same gap under the same sizes reversed stays joined, which is what makes the reading a
// choice rather than a coincidence: 6pt then 24pt wants 7.2 and 2.5pt does not reach it. Without
// this half, "use the smaller size" would pass the test above.
func TestGapAgainstALargeFollowingSpanStaysJoined(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "H"}, sp{1, 2, "2"})
	atLines(d, 6, 700, 700)
	atX(d, 72, 120, 122.5, 126)
	for si := range d.Pages[0].Blocks[0].Spans {
		if d.Pages[0].Blocks[0].Spans[si].MCID == 2 {
			d.Pages[0].Blocks[0].Spans[si].Style.Size = 24
		}
	}
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "H2"; got != want {
		t.Errorf("pair = %q, want %q: 2.5pt is inside 0.30 of 24pt", got, want)
	}
}

// The threshold is SpaceFrac, not one of the other three fractions in the same struct.
//
// A gap of 0.40 of the type size is a space — it clears SpaceFrac's 0.30 — and is under
// LineFrac's 0.50 and far under WideSpaceFrac's 2.50, so it fails under any of those
// substitutions. That band is what the corpus cannot supply: its three cases are at 49.94, 76.35
// and 109.88 space widths, which clear every candidate at once.
func TestGapJustOverTheSpaceThresholdIsASpace(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "shall"}, sp{1, 2, "be"})
	atLines(d, 10, 700, 700)
	atX(d, 72, 100, 104, 120)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "shall be"; got != want {
		t.Errorf("pair = %q, want %q: 4pt is 0.40 of 10pt, over SpaceFrac and under LineFrac", got, want)
	}
}

// A wide gap between two lines is a line break, not a space. The two rules divide on the
// baseline test, and a listing is where both could fire: this one must decline so that
// breakAtBaselines writes the newline alone, with no space in front of it.
func TestGapOnAnotherLineIsNotASpace(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "endobj"}, sp{1, 2, "11 0 obj"})
	atLines(d, 10, 700, 688)
	atX(d, 72, 110, 400, 460)
	tr := tree(el(tag.RoleH2, 1, 0), el(tag.RoleCode, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	if got, want := out.Sections[0].Blocks[0].Text(), "endobj\n11 0 obj"; got != want {
		t.Errorf("listing = %q, want %q: the break carries the boundary", got, want)
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
	// All four are cells, the last one included. Its text is wrapped in a P, which is
	// what every cell on disk does — 0 of 17482 hold marked content directly — and this
	// case asserted RoleParagraph until wrapsText made that P transparent. A detached
	// paragraph there is the defect: the cell emits empty, IsEmpty drops it, and the
	// text reappears outside the table it belongs to.
	for i := range blocks {
		if blocks[i].Role != doc.RoleTableCell {
			t.Errorf("block %d role = %s, want %s", i, blocks[i].Role, doc.RoleTableCell)
		}
	}
	if got := texts(blocks); !equal(got, []string{"Key", "Value", "/Filter", "name or array"}) {
		t.Errorf("cells = %v", got)
	}

	// The positions THead and TBody must not disturb: a row group holds rows without
	// renumbering them, so the header is row 0 and the body row 1, not row 0 of each
	// group.
	for i, want := range []doc.Cell{
		{Table: 1, Row: 0, Col: 0, Header: true},
		{Table: 1, Row: 0, Col: 1, Header: true},
		{Table: 1, Row: 1, Col: 0},
		{Table: 1, Row: 1, Col: 1},
	} {
		got := blocks[i].Cell
		if got == nil {
			t.Errorf("block %d has no cell position", i)
			continue
		}
		if *got != want {
			t.Errorf("block %d cell = %+v, want %+v", i, *got, want)
		}
	}
}

// A cell that is not one of its row's own kids has no column, and cellAt declines rather
// than guessing. Nothing on disk needs this — all 17482 cells are direct children of
// their TR — but the spec does not require it, and the alternative is worse than no
// position: falling out of the ordinal scan without a match leaves Col at the row's cell
// count, placing the cell past the end of its own row. Declining emits the text as a
// paragraph, which loses the grid and nothing else.
//
// Two shapes, both nested one level deeper than a column can be counted at: a TD under a
// TD, and a TD under a non-table container inside the TR.
func TestCellNestedBelowItsRowHasNoPosition(t *testing.T) {
	d := docWith(sp{1, 0, "7.4 Filters"}, sp{1, 1, "outer"}, sp{1, 2, "inner"}, sp{1, 3, "wrapped"})
	inner := kids(el(tag.RoleTD, 1, 1), el(tag.RoleTD, 1, 2))
	wrapped := kids(el(tag.RoleDiv, 1), el(tag.RoleTD, 1, 3))
	tbl := kids(el(tag.RoleTable, 1), kids(el(tag.RoleTR, 1), inner, wrapped))
	tr := tree(el(tag.RoleH2, 1, 0), tbl)

	out, _ := Tagged(d, tr, DefaultOptions)

	// The outer TD is a direct kid and keeps its position; the two below it do not. Its
	// text includes the inner cell's, because a TD inside a TD has no block role of its
	// own and gather takes it — the position is the claim under test, not the split.
	blocks := out.Sections[0].Blocks
	for i := range blocks {
		if blocks[i].Role != doc.RoleTableCell {
			continue
		}
		c := blocks[i].Cell
		text := blocks[i].Text()
		if strings.Contains(text, "outer") {
			if c == nil {
				t.Errorf("the direct cell %q lost its position", text)
			}
			continue
		}
		if c != nil {
			t.Errorf("nested cell %q got position %+v, want none", text, *c)
		}
	}
}

func TestFigureKeepsAltAsItsOnlyText(t *testing.T) {
	// For an accessible figure /Alt is the only text there is, so a block with no
	// spans is still content. Both are carried, on separate fields, because §14.9.3 and
	// §14.9.4 are opposite operations — /ActualText replaces the glyphs, /Alt describes
	// them — and this package must not decide between them for the sinks. It used to,
	// preferring /ActualText and losing /Alt, which is what let a sink substitute a
	// description over real page text without being able to tell it was doing so.
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
	if blocks[0].Replacement != "" {
		t.Errorf("figure gained a replacement it never declared: %q", blocks[0].Replacement)
	}
	if blocks[1].Replacement != "the actual text" {
		t.Errorf("/ActualText not carried: %q", blocks[1].Replacement)
	}
	if blocks[1].Alt != "described" {
		t.Errorf("/Alt dropped where /ActualText is also present: %q", blocks[1].Alt)
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

// An element's block records each identifier once, not once per span.
//
// The union is what doc.Block.MCIDs is documented to be, and this site built something
// else: it appended every span's identifier as it copied the span, so an element whose
// marked content arrives as several spans per sequence — which is every element the
// extractor split at a style change — recorded a repeat for each. Nothing downstream
// reads the field, so the wrong set never reached a rendered document; it reached the
// diagnostic that exists to answer which MCIDs went where, which is worse than no
// answer. 34141 of the corpus's duplicate entries were on section blocks, against 0 on the
// extractor's own — the invariant held wherever it was written and nowhere else.
func TestAnEmittedBlockRecordsEachMCIDOnce(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "A sentence in "},
		sp{1, 1, "two styles"},
		sp{1, 2, " and a second sequence "},
		sp{1, 2, "in two more."},
	)
	tr := tree(el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1, 2))

	out, _ := Tagged(d, tr, DefaultOptions)

	blks := out.Sections[0].Blocks
	if len(blks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blks))
	}
	if want := []int{1, 2}; !slices.Equal(blks[0].MCIDs, want) {
		t.Errorf("MCIDs = %v, want %v: four spans across two sequences is a set of two", blks[0].MCIDs, want)
	}
}

// A recovered block's set is the union of the spans it kept, and only those.
//
// This site filtered the absent value and never deduplicated, so a page whose unclaimed
// text arrives as several spans of one sequence recorded that sequence once per span. The
// -1 in the fixture is the other half: it is drawn outside marked content, so it was
// assembled from no identifier at all and contributes nothing to the set — while still
// being kept as text, which is the whole reason Unplaced exists.
func TestAnUnplacedBlockRecordsTheUnionOfWhatItKept(t *testing.T) {
	d := docWith(
		sp{1, 0, "1 Scope"},
		sp{1, 1, "Claimed body."},
		sp{2, 7, "Text no element references, "},
		sp{2, 7, "continued in another style."},
		sp{2, -1, "Drawn outside any marked content."},
	)
	tr := tree(el(tag.RoleH1, 1, 0), el(tag.RoleP, 1, 1))

	out, _ := Tagged(d, tr, DefaultOptions)

	if len(out.Unplaced) != 1 || len(out.Unplaced[0].Blocks) != 1 {
		t.Fatalf("unplaced = %d pages, want 1 page of 1 block", len(out.Unplaced))
	}
	keep := out.Unplaced[0].Blocks[0]
	if want := []int{7}; !slices.Equal(keep.MCIDs, want) {
		t.Errorf("MCIDs = %v, want %v", keep.MCIDs, want)
	}
	// The untagged span is text the block keeps even though it is in no set.
	if !strings.Contains(keep.Text(), "outside any marked content") {
		t.Errorf("unplaced text = %q: the untagged span was dropped", keep.Text())
	}
}

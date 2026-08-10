package layout

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// TestOrderedListsPromotesARun is mupdf_explored.pdf's shape, which is the only untagged
// document on disk that holds ordered lists at all: a lettered run of algorithm steps at one
// left edge.
//
// The whole rule is here — the run is the evidence. ADR 0011 rejected this recognition
// because one numbered block is indistinguishable from a numbered heading or a table row,
// and three consecutive incrementing labels at one edge is a claim neither of those makes.
func TestOrderedListsPromotesARun(t *testing.T) {
	d := pageDoc(
		item("a) Accumulate the sequence.", 90),
		item("b) Sort it by the key.", 90),
		item("c) Emit the result.", 90),
	)
	st := OrderedLists(d, DefaultOptions)

	if st.Items != 3 || st.Runs != 1 || st.MaxLevel != 1 {
		t.Fatalf("stats = %+v, want 3 items in 1 run at level 1", st)
	}
	want := []string{"a)", "b)", "c)"}
	for i, b := range d.Pages[0].Blocks {
		if b.Role != doc.RoleListItem || b.Level != 1 {
			t.Errorf("block %d role/level = %v/%d, want list item at 1", i, b.Role, b.Level)
		}
		if b.Marker != want[i] {
			t.Errorf("block %d marker = %q, want %q", i, b.Marker, want[i])
		}
		if !b.Enumerated() {
			t.Errorf("block %d Enumerated = false: the sink cannot tell it from a bullet", i)
		}
	}
	if got := d.Pages[0].Blocks[0].Text(); got != "Accumulate the sequence." {
		t.Errorf("text = %q, want the label stripped", got)
	}
}

// TestOrderedListsRejectsASingleItem is the asymmetry with Lists, and the reason both rules
// can coexist without contradicting each other.
//
// Lists promotes one "• item" and measured a run minimum as costing 136 genuine promotions
// to catch 3 strays, so it refuses one. Here the opposite holds: without the run there is no
// evidence at all, because a lone numbered paragraph is exactly what ADR 0011 says cannot be
// told from a heading. A mutation dropping this check promotes every numbered clause title
// in the corpus.
func TestOrderedListsRejectsASingleItem(t *testing.T) {
	d := pageDoc(
		item("1. A lone numbered paragraph.", 90),
		body(10, false),
	)
	st := OrderedLists(d, DefaultOptions)

	if st.Items != 0 || st.Runs != 0 {
		t.Fatalf("stats = %+v, want nothing promoted", st)
	}
	if got := d.Pages[0].Blocks[0].Text(); got != "1. A lone numbered paragraph." {
		t.Errorf("text = %q, want it untouched: a block that is not promoted is not edited", got)
	}
	if d.Pages[0].Blocks[0].Marker != "" {
		t.Errorf("marker = %q, want empty", d.Pages[0].Blocks[0].Marker)
	}
}

// TestOrderedListsRequiresIncrementingValues is what makes the run a sequence rather than a
// coincidence: two numbered paragraphs are common, two that count are not.
//
// All three cases are corpus shapes. Repeated values are a table's first column of
// quantities; a gap is two unrelated clause numbers meeting across a page break; a descending
// pair is a reference cited twice.
func TestOrderedListsRequiresIncrementingValues(t *testing.T) {
	cases := []struct{ a, b string }{
		{"1. The same value twice.", "1. And again here."},
		{"1. Then a gap in the count.", "3. Skipping the second."},
		{"3. Counting downwards.", "2. Which no list does."},
	}
	for _, c := range cases {
		d := pageDoc(item(c.a, 90), item(c.b, 90))
		if st := OrderedLists(d, DefaultOptions); st.Items != 0 {
			t.Errorf("stats = %+v for %q then %q, want nothing promoted", st, c.a, c.b)
		}
	}
}

// TestOrderedListsRestartsAfterARejectedCandidate is the loop's advance, and the case a
// rejected run has to leave available: "1." "1." "2." is a stray numbered paragraph followed
// by a genuine two-item list, and the second block is both the failed run's second element
// and the real run's first.
//
// Advancing past the whole failed attempt would swallow it and promote nothing. Advancing one
// block promotes the last two, which is the right answer and is what the code does — asserted
// here because no other test distinguishes the two advances.
func TestOrderedListsRestartsAfterARejectedCandidate(t *testing.T) {
	d := pageDoc(
		item("1. A stray numbered paragraph.", 90),
		item("1. The list's first item.", 90),
		item("2. And its second.", 90),
	)
	st := OrderedLists(d, DefaultOptions)

	if st.Items != 2 || st.Runs != 1 {
		t.Fatalf("stats = %+v, want 2 items in 1 run", st)
	}
	if got := d.Pages[0].Blocks[0].Text(); got != "1. A stray numbered paragraph." {
		t.Errorf("block 0 text = %q, want it untouched", got)
	}
	if d.Pages[0].Blocks[1].Marker != "1." || d.Pages[0].Blocks[2].Marker != "2." {
		t.Errorf("markers = %q, %q, want %q, %q",
			d.Pages[0].Blocks[1].Marker, d.Pages[0].Blocks[2].Marker, "1.", "2.")
	}
}

// TestOrderedListsRequiresOneForm keeps two adjacent lists in different notations from
// fusing into one run, and keeps a numbered paragraph after a lettered list out of it.
//
// The values chain in every pair below — "a)" is 1 and "2." is 2 — so the increment check
// admits them all and only the form check rejects them. Each pair isolates one part of it:
// "1." against "b." shares its delimiters, so only the numeric-versus-alphabetic test
// separates them, and "1." against "2)" and "[1]" against "2]" have delimiters of the same
// length, so only comparing the delimiters themselves does. A form check that compared
// lengths would fuse the last two, and no other test here notices.
func TestOrderedListsRequiresOneForm(t *testing.T) {
	cases := []struct{ a, b string }{
		{"a) A lettered item.", "2. An arabic one."},
		{"1. An arabic item.", "b. A lettered one."},
		{"1. A period after it.", "2) A bracket after it."},
		{"[1] Square-bracketed.", "2) Round-bracketed."},
	}
	for _, c := range cases {
		d := pageDoc(item(c.a, 90), item(c.b, 90))
		if st := OrderedLists(d, DefaultOptions); st.Items != 0 {
			t.Errorf("stats = %+v for %q then %q, want nothing promoted: different forms",
				st, c.a, c.b)
		}
	}
}

// TestOrderedListsRequiresOneLeftEdge is the geometric half. A numbered paragraph indented
// differently from the run above it is not part of it, however well its number continues the
// count — the corpus case is a numbered clause body sitting under a numbered list.
//
// The tolerance is half a point, so the second pair below is inside it: two blocks whose
// measured X0 differs by a fraction of a point differ because of the glyph each starts with,
// not because a producer indented one.
func TestOrderedListsRequiresOneLeftEdge(t *testing.T) {
	d := pageDoc(
		item("1. At the margin.", 90),
		item("2. Indented well past it.", 120),
	)
	if st := OrderedLists(d, DefaultOptions); st.Items != 0 {
		t.Fatalf("stats = %+v, want nothing promoted: the edges differ by 30pt", st)
	}

	d = pageDoc(
		item("1. At the margin.", 90),
		item("2. A glyph-width away.", 90.2),
	)
	if st := OrderedLists(d, DefaultOptions); st.Items != 2 {
		t.Fatalf("stats = %+v, want 2 promoted: 0.2pt is measurement noise, not an indent", st)
	}
}

// TestOrderedListsLeavesDeclaredRolesAlone is the precedence inferRoles depends on, and the
// direct answer to ADR 0011's objection.
//
// The ADR's case is that a numbered item and a numbered heading are the same block. They are,
// on disk — so this rule never sees a heading, because Headings runs first and a promoted
// heading is no longer RoleParagraph. Same for a table cell, which Tables produces. Asserted
// rather than argued: a mutation dropping the role gate turns the corpus's clause titles into
// a list.
func TestOrderedListsLeavesDeclaredRolesAlone(t *testing.T) {
	d := pageDoc(
		item("1. Introduction", 90),
		item("2. Scope", 90),
		item("3. Terms", 90),
	)
	for i := range d.Pages[0].Blocks {
		d.Pages[0].Blocks[i].Role = doc.RoleHeading
		d.Pages[0].Blocks[i].Level = 1
	}
	if st := OrderedLists(d, DefaultOptions); st.Items != 0 {
		t.Fatalf("stats = %+v, want nothing promoted: these are headings", st)
	}
	if got := d.Pages[0].Blocks[0].Text(); got != "1. Introduction" {
		t.Errorf("text = %q, want a heading's own number kept", got)
	}
}

// TestOrderedListsRunsAreNotAdjacent asserts that a broken sequence starts a fresh run
// rather than swallowing the interruption or ending the pass.
//
// The corpus case is a bibliography interrupted by a note. Two runs is the honest answer, and
// each item carries its own label anyway, so a sink loses nothing by the split.
func TestOrderedListsRunsAreNotAdjacent(t *testing.T) {
	d := pageDoc(
		item("[1] The first reference.", 90),
		item("[2] The second.", 90),
		body(10, false),
		item("[1] A second list's first.", 90),
		item("[2] And its second.", 90),
	)
	st := OrderedLists(d, DefaultOptions)

	if st.Items != 4 || st.Runs != 2 {
		t.Fatalf("stats = %+v, want 4 items in 2 runs", st)
	}
	if d.Pages[0].Blocks[2].Role != doc.RoleParagraph {
		t.Error("the interrupting paragraph was promoted")
	}
}

// TestOrderedListsPromotesTwoAdjacentRuns is the accepted run's advance, which no other test
// pins: every other case here has a non-candidate after the run, so skipping one block would
// cost nothing.
//
// Two lists meeting with no paragraph between them do not chain — the second's "1." does not
// continue the first's "2." — so the first run must end and the second must begin at the very
// next block. An advance of j+1 instead of j swallows the second list's first item, which
// leaves its "2." a lone candidate and promotes 2 items where 4 belong.
func TestOrderedListsPromotesTwoAdjacentRuns(t *testing.T) {
	d := pageDoc(
		item("1. The first list's first.", 90),
		item("2. And its second.", 90),
		item("1. The second list's first.", 90),
		item("2. And its second.", 90),
	)
	st := OrderedLists(d, DefaultOptions)

	if st.Items != 4 || st.Runs != 2 {
		t.Fatalf("stats = %+v, want 4 items in 2 runs", st)
	}
	for i, b := range d.Pages[0].Blocks {
		if b.Role != doc.RoleListItem {
			t.Errorf("block %d role = %v, want list item", i, b.Role)
		}
	}
}

// TestOrderedListsIgnoresAClauseNumberRun is the collision ADR 0008 owns, checked from this
// side too rather than left to the role gate alone.
//
// A dotted clause number has no single value to increment, so "7.4" then "7.5" cannot chain
// under this rule even where Headings declined to promote them — which is why ADR 0008's rule
// and this one cannot both claim the same block. Measured over every document on disk: no
// dotted number forms a run.
func TestOrderedListsIgnoresAClauseNumberRun(t *testing.T) {
	d := pageDoc(
		item("7.4 Filters", 90),
		item("7.5 File Streams", 90),
		item("7.6 Objects", 90),
	)
	if st := OrderedLists(d, DefaultOptions); st.Items != 0 {
		t.Fatalf("stats = %+v, want nothing promoted: a dotted number is not a label", st)
	}
}

// TestOrderedListsIsNilSafe is the same contract the other passes keep, so a caller can run
// the inference chain over a document it did not check.
func TestOrderedListsIsNilSafe(t *testing.T) {
	if st := OrderedLists(nil, DefaultOptions); st.Items != 0 || st.Runs != 0 {
		t.Errorf("stats = %+v on a nil document, want zero", st)
	}
}

package layout

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// item builds a marker block at a left edge, one span, body size.
func item(text string, x0 float64) doc.Block {
	b := block(text, 10, false)
	b.Box = geom.Rect{X0: x0, Y0: 0, X1: x0 + 200, Y1: 10}
	return b
}

// TestListsNestsByLeftEdge is testdata/reference/lists.pdf's geometry: three items at one
// margin and two indented under the second, a 23.94pt step at 10pt type.
//
// The step is 2.39 type sizes against a 1.0 threshold, and it is the only genuine list
// nesting in anything on disk — corpus-wide the other seven distinct left-edge gaps
// inside a marker run sit at 0.011 and 0.241 type sizes. So this is the case that says
// the levels come from the edge at all.
func TestListsNestsByLeftEdge(t *testing.T) {
	d := pageDoc(
		item("• First item.", 145.94),
		item("• Second item.", 145.94),
		item("– Nested item under the second.", 169.88),
		item("– Another nested item.", 169.88),
		item("• Third item.", 145.94),
	)
	st := Lists(d, DefaultOptions)

	if st.Items != 5 || st.Runs != 1 || st.MaxLevel != 2 {
		t.Fatalf("stats = %+v, want 5 items in 1 run at max level 2", st)
	}
	want := []int{1, 1, 2, 2, 1}
	for i, b := range d.Pages[0].Blocks {
		if b.Role != doc.RoleListItem {
			t.Errorf("block %d role = %v, want list item", i, b.Role)
		}
		if b.Level != want[i] {
			t.Errorf("block %d level = %d, want %d", i, b.Level, want[i])
		}
	}
}

// TestListsStripsTheMarker is the other half of the promotion: the marker is structure,
// so it leaves the text with the role and lands in Block.Marker.
//
// Leaving it in the text would double it on the one sink that exists — markdown writes
// its own "- " — and would make every future sink re-derive the glyph allowlist to know
// which leading rune to drop.
//
// The strip itself is doc.Block.StripMarker's and is tested there, across spans and past a
// leading whitespace span, because sectionize needs the same rules for the list items
// whose producer declared the role without declaring the label. What this asserts is that
// promoting a block invokes it — a Lists that assigned the role and left the text alone
// would pass every other test in this file.
func TestListsStripsTheMarker(t *testing.T) {
	d := pageDoc(item("• First item.", 100))
	Lists(d, DefaultOptions)

	got := d.Pages[0].Blocks[0]
	if txt := got.Text(); txt != "First item." {
		t.Errorf("text = %q, want %q", txt, "First item.")
	}
	if got.Marker != "•" {
		t.Errorf("marker = %q, want %q: the glyph is kept, not discarded", got.Marker, "•")
	}
}

// TestListsRequiresASeparator is the gate that makes an allowlist of glyphs safe.
//
// A marker glued to its text is not a marker. Measured over the corpus, all 1302 blocks
// opening with U+2022 separate it with a space and none glue it, while the excluded "-"
// is glued in 12 of 13 — so requiring the separator is what distinguishes a bullet a
// producer set from a rune that happens to lead a word.
//
// The last two cases are the length requirement: mupdf_explored.pdf sets a lone Wingdings
// square as a page decoration, so a marker with nothing after it is not an item either.
// Both fail on length once the text is trimmed, which is why doc.ListMarker has no third
// "and content follows" condition — see its comment.
func TestListsRequiresASeparator(t *testing.T) {
	d := pageDoc(
		item("–glued to its text", 100),
		item("•", 100),
		item("•   ", 100),
	)
	if st := Lists(d, DefaultOptions); st.Items != 0 {
		t.Errorf("items = %d, want 0: no separator, and a marker with no content is decoration", st.Items)
	}
	if txt := d.Pages[0].Blocks[1].Text(); txt != "•" {
		t.Errorf("text = %q: a rejected block must keep its text", txt)
	}
}

// TestListsPromotesASingleItem records the guard that was measured and rejected.
//
// Requiring two consecutive marker blocks is the obvious defence against a stray table
// row, and it costs 136 promotions across the corpus — overwhelmingly genuine ones:
// single-item lists, and multi-item lists that extract fused into one block. It would
// reject 136 to catch about 3, so a run of one is a list.
//
// The fusion is real but reaches the output once on disk, and neither geometry nor the
// marked-content identifier separates a fused join from an ordinary wrap — see Lists'
// comment. The guard is rejected on its own arithmetic, so this test stands on that.
func TestListsPromotesASingleItem(t *testing.T) {
	d := pageDoc(
		body(10, false),
		item("■ The only item.", 100),
		body(10, false),
	)
	if st := Lists(d, DefaultOptions); st.Items != 1 || st.MaxLevel != 1 {
		t.Errorf("stats = %+v, want a single item at level 1", st)
	}
}

// TestListsRanksWithinTheRun keeps two unrelated lists from being read as one nested
// list, which is why the tier scope is the maximal run and not the page.
//
// The second list sits far right of the first — two columns, or a list inside an indented
// note. Ranking page-wide would make every item of it level 2.
func TestListsRanksWithinTheRun(t *testing.T) {
	d := pageDoc(
		item("• First list.", 100),
		item("• Still the first.", 100),
		body(10, false),
		item("• A different list.", 300),
		item("• Also the second.", 300),
	)
	st := Lists(d, DefaultOptions)

	if st.Runs != 2 || st.Items != 4 || st.MaxLevel != 1 {
		t.Fatalf("stats = %+v, want 4 items in 2 runs, none nested", st)
	}
}

// TestListsIgnoresSubThresholdGaps is the corpus's false-positive shape: ISO 32000-2's
// PDFDocEncoding table, whose adjacent rows open with an em dash and an en dash of
// different widths, so their left edges differ by 0.241 type sizes.
//
// Six more corpus gaps sit at 0.011, which is float noise. Nothing between 0.3 and 2.4
// changes any result, so ListStep is a statement — nesting indents by about a character —
// rather than a fitted threshold, and this pins the low end of that band.
func TestListsIgnoresSubThresholdGaps(t *testing.T) {
	d := pageDoc(
		item("— 132 0x84 0204 U+2014 EM DASH", 100),
		item("– 133 0x85 0205 U+2013 EN DASH", 102.41),
	)
	if st := Lists(d, DefaultOptions); st.MaxLevel != 1 {
		t.Errorf("max level = %d, want 1: 0.241 type sizes is a glyph width, not an indent", st.MaxLevel)
	}
}

// TestListsNegativeStepFlattens covers the one place in Options where zero and negative
// differ: zero is "unset, use the default" and negative disables nesting.
//
// It is a usable setting rather than a defensive branch — a caller that wants markers
// removed without trusting the geometry that assigns depth.
func TestListsNegativeStepFlattens(t *testing.T) {
	d := pageDoc(
		item("• First item.", 100),
		item("– Nested item.", 124),
	)
	st := Lists(d, Options{ListStep: -1})

	if st.Items != 2 || st.MaxLevel != 1 {
		t.Errorf("stats = %+v, want both items flattened to level 1", st)
	}
	if txt := d.Pages[0].Blocks[1].Text(); txt != "Nested item." {
		t.Errorf("text = %q: flattening must still strip the marker", txt)
	}
}

// TestListsLeavesDeclaredRolesAlone: only paragraphs are candidates, for the same reason
// Headings only considers them.
//
// A block with a role was given one by a structure tree, and sectionize's tagged output
// contains list items whose text opens with a glyph the producer meant — so inference
// must not edit either the role or the text. An artifact is the sharper case: a decorated
// folio has the exact shape of a candidate.
func TestListsLeavesDeclaredRolesAlone(t *testing.T) {
	declared := item("• Declared by the producer.", 100)
	declared.Role = doc.RoleListItem
	artifact := item("• A decorated folio.", 100)
	artifact.Role = doc.RoleArtifact
	d := pageDoc(declared, artifact)

	if st := Lists(d, DefaultOptions); st.Items != 0 {
		t.Errorf("items = %d, want 0", st.Items)
	}
	for i, b := range d.Pages[0].Blocks {
		if txt := b.Text(); !strings.HasPrefix(txt, "• ") {
			t.Errorf("block %d text = %q, want its marker untouched", i, txt)
		}
	}
}

// TestListsCapsAtMaxLevel clamps depth with the same option that clamps heading rank, so
// the two agree and the level a sink receives is always one it can render.
func TestListsCapsAtMaxLevel(t *testing.T) {
	var blocks []doc.Block
	for i := 0; i < 5; i++ {
		blocks = append(blocks, item("• Item.", 100+float64(i)*24))
	}
	d := pageDoc(blocks...)
	st := Lists(d, Options{MaxLevel: 3})

	if st.MaxLevel != 3 {
		t.Fatalf("max level = %d, want 3", st.MaxLevel)
	}
	want := []int{1, 2, 3, 3, 3}
	for i, b := range d.Pages[0].Blocks {
		if b.Level != want[i] {
			t.Errorf("block %d level = %d, want %d", i, b.Level, want[i])
		}
	}
}

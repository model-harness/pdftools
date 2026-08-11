package layout

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// block builds a one-span paragraph at a size and weight.
func block(text string, size float64, bold bool) doc.Block {
	return doc.Block{
		Role:  doc.RoleParagraph,
		Spans: []doc.Span{{Text: text, MCID: -1, Style: doc.Style{Size: size, Bold: bold}}},
	}
}

func pageDoc(blocks ...doc.Block) *doc.Document {
	return &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: blocks}}}
}

// body returns enough body text that the dominant cluster is unambiguous, since
// bodyCluster counts characters and a one-word body would lose to a long heading.
func body(size float64, bold bool) doc.Block {
	return block("The quick brown fox jumps over the lazy dog and keeps going for a while yet.", size, bold)
}

// TestHeadingsRanksByNumbering is the central assertion: the level comes from the
// document's own section number, not from where the size sits in a ladder.
//
// The third heading is at *body size*, so no size ladder has a rung for it, and it is
// admitted on weight alone. Getting level 3 there is the whole point — it is the case
// that decided the design, and testdata/reference/headings.pdf sets its deepest level
// exactly this way.
func TestHeadingsRanksByNumbering(t *testing.T) {
	d := pageDoc(
		block("1 First Section", 14.35, true),
		body(9.96, false),
		block("1.1 A Subsection", 11.96, true),
		body(9.96, false),
		block("1.1.1 A Sub-subsection", 9.96, true),
		body(9.96, false),
	)
	st := Headings(d, DefaultOptions)
	if st.Headings != 3 {
		t.Fatalf("headings = %d, want 3 (stats %+v)", st.Headings, st)
	}
	want := []int{1, 0, 2, 0, 3, 0}
	for i, b := range d.Pages[0].Blocks {
		if b.Level != want[i] {
			t.Errorf("block %d (%q) level = %d, want %d", i, b.Text(), b.Level, want[i])
		}
		wantRole := doc.RoleParagraph
		if want[i] > 0 {
			wantRole = doc.RoleHeading
		}
		if b.Role != wantRole {
			t.Errorf("block %d (%q) role = %v, want %v", i, b.Text(), b.Role, wantRole)
		}
	}
}

// TestHeadingsSizeLadderDoesNotSetLevel pins the rule that ranking by ladder position
// was rejected for.
//
// Both headings are numbered level 1 and set at different sizes — a book that sets its
// title larger than its chapters, which mupdf_explored.pdf does across five distinct
// above-body sizes. Ranking by ladder position would make the smaller one level 2. It
// disagreed with the document's own numbering on 296 of 296 numbered headings there.
func TestHeadingsSizeLadderDoesNotSetLevel(t *testing.T) {
	d := pageDoc(
		block("1 Introduction", 24.79, false),
		body(9.96, false),
		block("2 Interpreters", 14.35, false),
		body(9.96, false),
	)
	Headings(d, DefaultOptions)
	for _, i := range []int{0, 2} {
		b := d.Pages[0].Blocks[i]
		if b.Role != doc.RoleHeading || b.Level != 1 {
			t.Errorf("block %d (%q): role %v level %d, want heading level 1", i, b.Text(), b.Role, b.Level)
		}
	}
}

// TestHeadingsBodyBoldCarriesNoSignal is the v110-changes.pdf case: 8.04pt bold is
// 48.8% of that document's characters, so bold is the body face.
//
// A weight-implies-heading rule marks half the document. Nothing at body size may be
// promoted, including the numbered paragraph — it is body size and body weight, which is
// to say it is body text that happens to start with a number.
//
// The larger heading is the other half of the claim, and the half a "headings = 0"
// assertion alone would not pin: the rule must degrade to size *alone*, not to nothing.
// Making distinct() refuse everything whenever the body is bold would satisfy the first
// assertion and break this one.
func TestHeadingsBodyBoldCarriesNoSignal(t *testing.T) {
	d := pageDoc(
		body(8.04, true),
		block("4.2 A numbered paragraph, in the same bold face as everything else.", 8.04, true),
		body(8.04, true),
		block("5 A Real Heading", 12.5, true),
	)
	st := Headings(d, DefaultOptions)
	if !st.BodyBold {
		t.Fatalf("BodyBold = false, want true (stats %+v)", st)
	}
	if st.Headings != 1 {
		t.Errorf("headings = %d, want 1: only the larger block, on size alone (stats %+v)", st.Headings, st)
	}
	if got := d.Pages[0].Blocks[1].Role; got != doc.RoleParagraph {
		t.Errorf("body-weight numbered paragraph role = %v, want paragraph", got)
	}
	if b := d.Pages[0].Blocks[3]; b.Role != doc.RoleHeading || b.Level != 1 {
		t.Errorf("larger heading = %v level %d, want heading level 1", b.Role, b.Level)
	}
}

// TestHeadingsPlainHeadingAdmitted is the arXiv and Adobe case: headings set plain,
// larger, with bold reserved for inline emphasis (0.3% and 0.5% of characters).
//
// A rule requiring bold finds nothing in either document.
func TestHeadingsPlainHeadingAdmitted(t *testing.T) {
	d := pageDoc(
		block("2 Observations", 11.96, false),
		body(8.97, false),
	)
	if st := Headings(d, DefaultOptions); st.Headings != 1 {
		t.Errorf("headings = %d, want 1: a plain larger heading is still a heading (stats %+v)", st.Headings, st)
	}
}

// TestHeadingsRejectsUnnumbered records the limit rather than working around it.
//
// dotted-gridlines.pdf has a 41-character table row at body size in bold, which no
// typographic signal separates from a real heading — the space above it, 1.68 times
// the body size, sits inside the 1.60–1.96 range the reference headings occupy. So an
// unnumbered candidate stays a paragraph, and "Preface" is the cost of not promoting
// that row.
func TestHeadingsRejectsUnnumbered(t *testing.T) {
	d := pageDoc(
		block("ABRUZZO 98 1 3 95 2 2 424.264 537 405.785", 7.2, true),
		body(7.2, false),
		block("Preface", 7.2, true),
	)
	st := Headings(d, DefaultOptions)
	if st.Headings != 0 {
		t.Errorf("headings = %d, want 0", st.Headings)
	}
	if st.Candidates != 2 {
		t.Errorf("candidates = %d, want 2: both passed the typographic gate and neither the numbering", st.Candidates)
	}
}

// TestHeadingsRejectsFusedBlock covers what extract's continues() does not split.
//
// It tests only vertical step, so a heading whose following line falls at ordinary
// leading joins the paragraph after it — which is why autotagPDFInput.pdf and
// v110-changes.pdf produce zero style-uniform blocks above their body size. A fused
// block is not a heading, and splitting it is block segmentation, not classification.
func TestHeadingsRejectsFusedBlock(t *testing.T) {
	d := pageDoc(
		doc.Block{Role: doc.RoleParagraph, Spans: []doc.Span{
			{Text: "1 First Section ", MCID: -1, Style: doc.Style{Size: 14.35, Bold: true}},
			{Text: "and the body that follows it on the very next line.", MCID: -1, Style: doc.Style{Size: 9.96}},
		}},
		body(9.96, false),
	)
	if st := Headings(d, DefaultOptions); st.Headings != 0 {
		t.Errorf("headings = %d, want 0: the block holds a heading and its body", st.Headings)
	}
}

// TestHeadingsLeavesDeclaredRolesAlone: only paragraphs are candidates.
//
// A block that already has a role was given one by a structure tree, and inference
// must not overwrite a declaration. An artifact especially — a bold folio is the exact
// shape of a heading candidate.
func TestHeadingsLeavesDeclaredRolesAlone(t *testing.T) {
	d := pageDoc(
		doc.Block{Role: doc.RoleArtifact, Spans: []doc.Span{
			{Text: "4.2 Running header", MCID: -1, Style: doc.Style{Size: 14.35, Bold: true}}}},
		doc.Block{Role: doc.RoleListItem, Level: 2, Spans: []doc.Span{
			{Text: "1.1 A numbered list item", MCID: -1, Style: doc.Style{Size: 14.35, Bold: true}}}},
		body(9.96, false),
	)
	Headings(d, DefaultOptions)
	if got := d.Pages[0].Blocks[0].Role; got != doc.RoleArtifact {
		t.Errorf("artifact role = %v, want artifact", got)
	}
	if b := d.Pages[0].Blocks[1]; b.Role != doc.RoleListItem || b.Level != 2 {
		t.Errorf("list item = %v level %d, want list item level 2", b.Role, b.Level)
	}
}

// TestHeadingsLengthBound keeps numbered prose out. Specifications are full of it:
// "4.2.1 The value shall be…" is a clause body, not a title.
func TestHeadingsLengthBound(t *testing.T) {
	long := "4.2.1 The value of this key shall be a number that is greater than zero and " +
		"no greater than the number of pages in the document, and shall be interpreted."
	d := pageDoc(block(long, 14.35, true), body(9.96, false))
	if st := Headings(d, DefaultOptions); st.Headings != 0 {
		t.Errorf("headings = %d, want 0: %d runes is prose", st.Headings, len([]rune(long)))
	}
}

// TestHeadingsLevelCapped flattens past Markdown's six rather than emitting a level no
// dialect has. ISO numbering reaches five in the corpus and the cap is the sink's
// limit, not the document's.
func TestHeadingsLevelCapped(t *testing.T) {
	d := pageDoc(block("1.2.3.4.5.6.7.8 Deep", 14.35, true), body(9.96, false))
	Headings(d, DefaultOptions)
	if got := d.Pages[0].Blocks[0].Level; got != 6 {
		t.Errorf("level = %d, want 6", got)
	}
}

// TestHeadingsLevelCappedAnnex is TestHeadingsLevelCapped's other half: the cap belongs to
// the caller, so it must apply whichever function supplied the level.
//
// annexLevel counts components with no bound of its own — "A.1.2.3.4.5.6.7.8" is depth 9 —
// and the two rules reach the cap through the same line only because Headings takes one
// level from either. A fallback written as a second, separate promotion would not.
func TestHeadingsLevelCappedAnnex(t *testing.T) {
	d := pageDoc(block("A.1.2.3.4.5.6.7 Deep annex", 14.35, true), body(9.96, false))
	Headings(d, DefaultOptions)
	if got := d.Pages[0].Blocks[0].Level; got != 6 {
		t.Errorf("level = %d, want 6", got)
	}
}

func TestNumberedLevel(t *testing.T) {
	cases := []struct {
		in    string
		level int
		ok    bool
		why   string
	}{
		{"1 First Section", 1, true, "one component"},
		{"4.2.1 Nested subclause", 3, true, "three components"},
		{"4.2.1. Nested subclause", 3, true, "trailing dot is a style, not a fourth level"},
		{"12.34 Two digits each", 2, true, "components are not single digits"},
		{"1 Non-breaking space", 1, true, "a producer's nbsp separator is still a separator"},
		{"3.14 is pi", 2, true, "indistinguishable from a heading by text alone; the typographic gate is what excludes it"},
		{"3\t Terms and Definitions", 1, true, "a tab separator, which is not a stylistic nicety: 18 clause headings on disk use one, all in ISO TS 32001 through 32005, so unicode.IsSpace's breadth is load-bearing here"},
		{"3.14159", 0, false, "a bare number with no title is a folio or a cell"},
		{"Preface", 0, false, "no number"},
		{"", 0, false, "empty"},
		{"4.2.1:Nested", 0, false, "the separator must be whitespace"},
		{"1st Edition", 0, false, "not a section number"},
		{".2 Leading dot", 0, false, "no component before the dot"},
		{"4..2 Doubled dot", 0, false, "empty component"},
		{" 2 Leading space", 0, false, "no component before the separator: ok must imply a level of at least one, and Headings trims so only a direct call can reach this"},
		{"A.1 Annex", 0, false, "a lettered scheme is annexLevel's, and this function counts components a letter is not"},
		{"IV. Roman", 0, false, "roman schemes are matched by neither"},
		{"1 ", 1, true, "trailing space with nothing after it is still a separator; length and emptiness are the caller's checks"},
	}
	for _, c := range cases {
		lvl, ok := numberedLevel(c.in)
		if ok != c.ok || lvl != c.level {
			t.Errorf("numberedLevel(%q) = %d, %v; want %d, %v (%s)", c.in, lvl, ok, c.level, c.ok, c.why)
		}
	}
}

func TestAnnexLevel(t *testing.T) {
	cases := []struct {
		in    string
		level int
		ok    bool
		why   string
	}{
		{"A.1 Licensing", 2, true, "the letter is the level-one clause the numbering hangs off"},
		{"A.1.1 GNU AGPL", 3, true, "component count, as for a decimal number"},
		{"B.2.3  CMS MAC validation", 3, true, "the corpus's commonest shape, two spaces and all"},
		{"F.3.11  Main cross-reference and trailer (Part 11)", 3, true, "components are not single digits"},
		{"A.1. Licensing", 2, true, "a trailing dot on the number is a style, not a third level — the same call numberedLevel makes for \"4.2.1.\", and the dots alone already counted every component"},
		{"B.4  Attributes", 2, true, "Well-Tagged-PDF's own separator: U+2002 is three bytes in UTF-8, so testing s[i] would compare 0xE2 and reject every one of them"},
		{"A.1 Licensing", 2, true, "and a non-breaking space is two bytes, with the same consequence"},

		{"A Vocabulary Pruning", 0, false, "the bare letter, which is also how a sentence starts — the one form the corpus shows as prose"},
		{"A use of ISO 32000", 0, false, "PDF-Declarations.pdf's cover line, and the only bare-letter candidate on disk"},
		{"A. First item", 0, false, "an upper-case letter and a dot is how a sentence begins, which is why doc.OrderedLabel refuses it too"},
		{"A4 paper size", 0, false, "the digits must follow the dot, not the letter, so a letter-digit designation never parses"},
		{"a.1 lower case", 0, false, "an annex letter is upper case; the lower-case form is a list label"},
		{"A.1", 0, false, "a number with no title is a folio or a cell"},
		{"A.1:Annex", 0, false, "the separator must be whitespace"},
		{"A..1 Doubled", 0, false, "empty component"},
		{"A.. ", 0, false, "no digits at all"},
		{"1.1 Decimal", 0, false, "numberedLevel's, not this one's"},
		{"", 0, false, "empty"},
		{"É.1 Accented", 0, false, "A-Z only; the scheme is ASCII"},
	}
	for _, c := range cases {
		lvl, ok := annexLevel(c.in)
		if ok != c.ok || lvl != c.level {
			t.Errorf("annexLevel(%q) = %d, %v; want %d, %v (%s)", c.in, lvl, ok, c.level, c.ok, c.why)
		}
	}
}

// TestHeadingsRanksAnnexNumbering is the end-to-end pin: an appendix subclause reaches
// Headings, passes the typographic gate, and takes its level from its own numbering.
//
// This is mupdf_explored.pdf's Appendix A, which is where the untagged effect is — 5 of
// the 10 promotions the change makes. Level 2 rather than 1 for "A.1" is what the tagged
// corpus's declared H1..H6 ranks agree with, 107 times against 5.
func TestHeadingsRanksAnnexNumbering(t *testing.T) {
	d := pageDoc(
		block("A.1 Licensing", 14.35, false),
		body(9.96, false),
		block("A.1.1 GNU AGPL", 11.96, false),
		body(9.96, false),
	)
	st := Headings(d, DefaultOptions)
	if st.Headings != 2 {
		t.Fatalf("headings = %d, want 2 (stats %+v)", st.Headings, st)
	}
	if b := d.Pages[0].Blocks[0]; b.Role != doc.RoleHeading || b.Level != 2 {
		t.Errorf("A.1 = %v level %d, want heading level 2", b.Role, b.Level)
	}
	if b := d.Pages[0].Blocks[2]; b.Role != doc.RoleHeading || b.Level != 3 {
		t.Errorf("A.1.1 = %v level %d, want heading level 3", b.Role, b.Level)
	}
}

// TestHeadingsRejectsBareAnnexLetter keeps the half of numberedLevel's deferral that is
// still open, and it is the pin that makes the dot load-bearing.
//
// "A Vocabulary Pruning" is a real appendix title in the arXiv paper and stays a
// paragraph, because nothing in the block separates it from "A use of ISO 32000" — the one
// bare-letter candidate the tagged corpus has, which the producer does not declare a
// heading. Promoting the first means promoting the second.
func TestHeadingsRejectsBareAnnexLetter(t *testing.T) {
	d := pageDoc(
		block("A Vocabulary Pruning", 11.96, false),
		body(9.96, false),
		block("A use of ISO 32000", 11.96, false),
	)
	st := Headings(d, DefaultOptions)
	if st.Headings != 0 {
		t.Errorf("headings = %d, want 0: the bare letter is not evidence", st.Headings)
	}
	if st.Candidates != 2 {
		t.Errorf("candidates = %d, want 2: both passed the typographic gate and neither the numbering", st.Candidates)
	}
}

// TestHeadingsLeavesLowerCaseOrderedRunAlone is the collision check between this pass and
// the one after it: an "a)" run is a list, and Headings runs first.
//
// annexLevel requires an upper-case letter, so the run never reaches its scan — but the
// requirement is one character in a rune test, and the failure it prevents is invisible
// from here. A promoted item is a heading in the middle of a list, and it would take the
// whole run away from OrderedLists, which only ever sees paragraphs.
func TestHeadingsLeavesLowerCaseOrderedRunAlone(t *testing.T) {
	d := pageDoc(
		block("a) Accumulate a sequence", 11.96, false),
		block("b) Apply the transform", 11.96, false),
		body(9.96, false),
	)
	if st := Headings(d, DefaultOptions); st.Headings != 0 {
		t.Errorf("headings = %d, want 0: a lower-case ordered label is not an annex number", st.Headings)
	}
	if st := OrderedLists(d, DefaultOptions); st.Items != 2 {
		t.Errorf("ordered items = %d, want 2: the run must still reach OrderedLists", st.Items)
	}
}

// TestBodyClusterCountsCharacters: the body is what most of the *text* is set in, not
// what most of the blocks are. A page of one-line table rows must not outvote prose.
func TestBodyClusterCountsCharacters(t *testing.T) {
	blocks := []doc.Block{body(9.96, false)}
	for i := 0; i < 8; i++ {
		blocks = append(blocks, block("Row", 7.2, false))
	}
	size, bold, ok := bodyCluster(pageDoc(blocks...))
	if !ok || size != 9.96 || bold {
		t.Errorf("bodyCluster = %.2f bold=%v ok=%v, want 9.96 false true", size, bold, ok)
	}
}

// TestBodyClusterIgnoresArtifacts: a running header repeated on a thousand pages would
// otherwise become the body face.
func TestBodyClusterIgnoresArtifacts(t *testing.T) {
	art := doc.Block{Role: doc.RoleArtifact, Spans: []doc.Span{
		{Text: "A running header long enough to outweigh the prose on this page entirely, twice over.",
			MCID: -1, Style: doc.Style{Size: 7.2}}}}
	size, _, ok := bodyCluster(pageDoc(art, body(9.96, false)))
	if !ok || size != 9.96 {
		t.Errorf("bodyCluster = %.2f ok=%v, want 9.96 true", size, ok)
	}
}

// TestBodyClusterTieBreaksSmaller: an arbitrary body size makes every comparison
// against it arbitrary, so the tie is broken deterministically and toward the smaller
// size — leaving the larger as a candidate rather than the reverse.
func TestBodyClusterTieBreaksSmaller(t *testing.T) {
	d := pageDoc(block("aaaa", 9.96, false), block("bbbb", 14.35, false))
	if size, _, _ := bodyCluster(d); size != 9.96 {
		t.Errorf("bodyCluster = %.2f, want 9.96", size)
	}
}

// TestHeadingsEmptyDocument: a page holding one image has no headings, and that is an
// answer rather than an error.
func TestHeadingsEmptyDocument(t *testing.T) {
	for _, d := range []*doc.Document{nil, {}, pageDoc()} {
		if st := Headings(d, Options{}); st != (Stats{}) {
			t.Errorf("Headings(%v) = %+v, want zero", d, st)
		}
	}
}

// TestQuantizeGroupsEffectiveSizes: Style.Size is computed through the text matrix and
// the CTM, so nominally identical type differs in the far decimals. Without rounding,
// one body splits into several clusters and none dominates.
func TestQuantizeGroupsEffectiveSizes(t *testing.T) {
	d := pageDoc(
		block("The quick brown fox jumps over the lazy", 9.960001, false),
		block(" dog and keeps going for a while yet.", 9.959998, false),
		block("1 A Heading", 14.35, true),
	)
	st := Headings(d, DefaultOptions)
	if st.BodySize != 9.96 {
		t.Errorf("BodySize = %v, want 9.96", st.BodySize)
	}
	if st.Headings != 1 {
		t.Errorf("headings = %d, want 1 (stats %+v)", st.Headings, st)
	}
}

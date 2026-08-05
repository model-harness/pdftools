package doctags

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// fixtureDir holds docling's own DocTags documents and the Markdown docling renders
// them to. See docs/test.docs.md: MIT-licensed, pinned by SHA-256 in
// testdata/manifest.json, and always present, so these tests never skip. They are
// the reason this package could be finished before a model was ever loaded.
const fixtureDir = "../../testdata/docling"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("reference fixture missing: %s — run `pwsh testdata/fetch.ps1 -Download`: %v", name, err)
	}
	return string(b)
}

// letter is US Letter in points, the box the coordinate tests resolve against.
var letter = geom.NewRect(0, 0, 612, 792)

// TestParsePaper walks the widest fixture: nine pages of a real paper carrying every
// construct the parser has to handle. The counts are the parser's own output, but
// the page count and the tag inventory are checkable against the file, so a change
// in tokenization shows up as a number rather than as a diff nobody reads.
func TestParsePaper(t *testing.T) {
	src := fixture(t, "2206.01062.yaml.dt")

	// Derived from the input rather than from a previous run of this code. A page
	// break is a separator, not a terminator — this fixture's last page ends at
	// </doctag> with no break after it — so n breaks bound n+1 pages.
	wantPages := strings.Count(src, "<page_break>") + 1

	pages, err := Parse(src, letter)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pages) != wantPages {
		t.Errorf("pages = %d, want %d (page breaks are separators)", len(pages), wantPages)
	}

	roles := map[doc.Role]int{}
	var blocks int
	for i, p := range pages {
		if p.Number != i+1 {
			t.Errorf("page %d numbered %d", i+1, p.Number)
		}
		if !p.Rasterized {
			t.Errorf("page %d: Rasterized false — every DocTags page came from a model", p.Number)
		}
		if p.Box != letter {
			t.Errorf("page %d: box %v, want %v", p.Number, p.Box, letter)
		}
		blocks += len(p.Blocks)
		for _, b := range p.Blocks {
			roles[b.Role]++
		}
	}

	// Every role the paper exercises must be non-empty. A parser that silently
	// stopped recognizing <otsl> or <picture> would still produce plausible pages
	// made entirely of paragraphs, and that is the failure this catches.
	for _, want := range []doc.Role{
		doc.RoleParagraph, doc.RoleHeading, doc.RoleListItem,
		doc.RoleCaption, doc.RoleFigure, doc.RoleTableCell, doc.RoleArtifact,
	} {
		if roles[want] == 0 {
			t.Errorf("no %s blocks in a 9-page paper", want)
		}
	}
	t.Logf("%d pages, %d blocks: %v", len(pages), blocks, roles)

	// Every span carries text, and no span is whitespace only. A block with an empty
	// span reaches the Markdown sink as a blank line with a bullet or a hash in front
	// of it, which is worse than the block not being there.
	for _, p := range pages {
		for _, b := range p.Blocks {
			for _, s := range b.Spans {
				if strings.TrimSpace(s.Text) == "" {
					t.Fatalf("page %d: %s block with blank span", p.Number, b.Role)
				}
				if s.MCID != -1 {
					t.Fatalf("page %d: span MCID %d, want -1 — OCR has no marked content", p.Number, s.MCID)
				}
			}
		}
	}
}

// TestBarchart is the smallest complete document, small enough that every block can
// be asserted. It is also the only fixture with a <chart>: a picture-classification
// token followed by the OTSL grid of the chart's underlying data.
func TestBarchart(t *testing.T) {
	pages, err := Parse(fixture(t, "barchart.dt"), letter)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	got := pages[0].Blocks

	// The header, then 3 header cells, then six rows of three.
	if want := 1 + 3 + 18; len(got) != want {
		t.Fatalf("blocks = %d, want %d: %v", len(got), want, texts(got))
	}
	if got[0].Role != doc.RoleArtifact {
		t.Errorf("block 0 role = %s, want artifact (a <page_header> is page furniture)", got[0].Role)
	}
	if want := "Probability, Combinatorics and Control"; text(got[0]) != want {
		t.Errorf("block 0 = %q, want %q", text(got[0]), want)
	}
	for i, b := range got[1:] {
		if b.Role != doc.RoleTableCell {
			t.Fatalf("block %d role = %s, want table_cell", i+1, b.Role)
		}
	}
	if want := "Number of impellers"; text(got[1]) != want {
		t.Errorf("first cell = %q, want %q", text(got[1]), want)
	}
	// The last row: <fcel>6<fcel>0.24<fcel>0.24<nl>. The trailing cell is only
	// emitted because <nl> flushes, which is the case a parser that flushed on cell
	// *open* alone would drop.
	if want := "0.24"; text(got[len(got)-1]) != want {
		t.Errorf("last cell = %q, want %q", text(got[len(got)-1]), want)
	}
}

// TestNoLocations is upstream's deliberately degenerate document: tags with no
// <loc_> tokens anywhere. A model that stops emitting coordinates mid-generation is
// a real failure mode, and this is the repo's no-panics rule applied to model
// output — the text must still come through, with zero rectangles.
func TestNoLocations(t *testing.T) {
	src := fixture(t, "bad_doc.yaml.dt")
	if strings.Contains(src, "<loc_") {
		t.Fatalf("fixture is supposed to have no location tokens: %q", src)
	}

	pages, err := Parse(src, letter)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	got := pages[0].Blocks
	if len(got) != 2 {
		t.Fatalf("blocks = %v, want title + section header", texts(got))
	}
	for i, b := range got {
		if b.Role != doc.RoleHeading {
			t.Errorf("block %d role = %s, want heading", i, b.Role)
		}
		if !b.Box.IsZero() {
			t.Errorf("block %d box = %v, want zero — the input has no coordinates", i, b.Box)
		}
	}
	if got[0].Level != 1 {
		t.Errorf("<title> level = %d, want 1", got[0].Level)
	}
	// section_header_level_1 is level 2 here. docling's own Markdown for this file
	// renders it as `###`, because its serializer nests headings relative to the
	// <title> that precedes them. This package does not: a level that depends on
	// what came earlier cannot be assigned while parsing a single page, which is the
	// only thing the OCR router ever asks for. Pinned so the divergence is a decision
	// on record rather than a surprise.
	if got[1].Level != 2 {
		t.Errorf("section_header_level_1 = %d, want 2", got[1].Level)
	}
}

// TestSinglePage covers the router's actual entry point: one page as a model emits
// it, with no document wrapper around it.
func TestSinglePage(t *testing.T) {
	src := fixture(t, "01030000000083.dt")

	p, err := ParsePage(src, 7, letter)
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if p.Number != 7 {
		t.Errorf("Number = %d, want 7 — the model does not know it, so the caller supplies it", p.Number)
	}

	// Three tables, each with a caption nested inside its <otsl>. The nesting is the
	// thing being checked: a flat parser would attach the caption's text to the
	// table's trailing cell, where it reads as a data value.
	wantCaptions := strings.Count(src, "<otsl>")
	var captions int
	for _, b := range p.Blocks {
		if b.Role == doc.RoleCaption {
			captions++
			if !strings.HasPrefix(text(b), "TABLE 3") {
				t.Errorf("caption = %q, want a TABLE 3x title", text(b))
			}
		}
	}
	if captions != wantCaptions {
		t.Errorf("captions = %d, want %d (one nested in each <otsl>)", captions, wantCaptions)
	}

	// A page break in single-page input is an error rather than a second page:
	// returning another page's content under this page's number is worse than
	// failing, because nothing downstream could detect it.
	if _, err := ParsePage(src+"<page_break><text>next</text>", 7, letter); err == nil {
		t.Error("ParsePage accepted input containing a page break")
	}
}

// TestCoordinates is the arithmetic, and the Y flip is the reason it exists. DocTags
// counts down from the top of the page and PDF user space counts up from the bottom,
// so a document parsed without the flip is mirrored, reads perfectly, and no text
// comparison catches it.
func TestCoordinates(t *testing.T) {
	// A page header near the top: y from loc_14 to loc_20 of 500.
	pages, err := Parse("<doctag><page_header><loc_71><loc_14><loc_217><loc_20>hdr</page_header></doctag>", letter)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := pages[0].Blocks[0].Box

	tol := geom.Tolerance{Epsilon: 1e-9}
	want := geom.NewRect(
		71.0/500*612,
		792-20.0/500*792,
		217.0/500*612,
		792-14.0/500*792,
	)
	if !(tol.NearlyEqual(got.X0, want.X0) &&
		tol.NearlyEqual(got.Y0, want.Y0) &&
		tol.NearlyEqual(got.X1, want.X1) &&
		tol.NearlyEqual(got.Y1, want.Y1)) {
		t.Fatalf("box = %v, want %v", got, want)
	}
	// The header sits in the top decile of the page, not the bottom. This is the
	// assertion that fails if the flip is removed.
	if got.Y0 < 700 {
		t.Errorf("Y0 = %g: a page header is at the top of the page, not %g points up from the bottom", got.Y0, got.Y0)
	}

	// A box whose tags arrive reversed still normalizes, because geom.NewRect sorts.
	// tokens.py min/max-sorts on the way out, so this is defence against a model that
	// did not, not against the format.
	pages, err = Parse("<doctag><text><loc_400><loc_300><loc_100><loc_50>t</text></doctag>", letter)
	if err != nil {
		t.Fatalf("Parse reversed: %v", err)
	}
	if b := pages[0].Blocks[0].Box; b.Width() <= 0 || b.Height() <= 0 {
		t.Errorf("reversed coordinates gave a degenerate box: %v", b)
	}
}

// TestMalformed is the whole robustness contract in one place. Every input here is
// something a truncated, looping, or confused generation actually produces, and none
// of them may panic or lose the page's text.
func TestMalformed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // text that must survive, "" when only no-panic is required
	}{
		{"empty", "", ""},
		{"wrapper only", "<doctag></doctag>", ""},
		{"truncated mid-tag", "<doctag><text><loc_10><loc_10><loc_20><loc_20>hello<te", "hello"},
		{"unterminated element", "<doctag><text>hello", "hello"},
		{"unmatched close", "<doctag></text><text>hello</text>", "hello"},
		{"mismatched close", "<doctag><title>hello</text></doctag>", "hello"},
		{"text outside any element", "<doctag>bare words</doctag>", "bare words"},
		{"prose with angle brackets", "<doctag><text>if a < b then c > d</text></doctag>", "if a < b then c > d"},
		{"prose with a pdf dict", "<doctag><text>a << /Type /Page >> object</text></doctag>", "<< /Type /Page >>"},
		{"unknown token stays visible", "<doctag><text>a <future_tag> b</text></doctag>", "<future_tag>"},
		{"empty angle brackets", "<doctag><text>a <> b</text></doctag>", "a <> b"},
		{"loc out of range is text", "<doctag><text><loc_9999>x</text></doctag>", "<loc_9999>"},
		{"negative loc is text", "<doctag><text><loc_-5>x</text></doctag>", "<loc_-5>"},
		{"three locs, not four", "<doctag><text><loc_1><loc_2><loc_3>x</text></doctag>", "x"},
		{"five locs", "<doctag><text><loc_1><loc_2><loc_3><loc_4><loc_5>x</text></doctag>", "x"},
		{"locs before any element", "<doctag><loc_1><loc_2><text>x</text></doctag>", "x"},
		{"cell tokens with no table", "<doctag><fcel>a<fcel>b</doctag>", ""},
		{"marker with no element", "<doctag><bar_chart></doctag>", ""},
		{"self-closing forms", "<doctag><text><loc_1/><loc_2/><loc_3/><loc_4/>x</text></doctag>", "x"},
		{"heading level out of range", "<doctag><section_header_level_9>x</section_header_level_9></doctag>", "x"},
		{"only page breaks", "<doctag><page_break><page_break></doctag>", ""},
		{"repeated close tags", "<doctag><text>x</text></text></text></doctag>", "x"},
		{"nested lists", "<doctag><unordered_list><list_item>a</list_item><unordered_list><list_item>b</list_item></unordered_list></unordered_list></doctag>", "b"},
		{"unbalanced list open", "<doctag><unordered_list><list_item>a</list_item></doctag>", "a"},
		{"stray list close", "<doctag></unordered_list><list_item>a</list_item></doctag>", "a"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pages, err := Parse(tc.src, letter)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.src, err)
			}
			if len(pages) == 0 {
				t.Fatalf("Parse(%q) returned no pages; a blank result is still one page", tc.src)
			}
			if tc.want == "" {
				return
			}
			var all []string
			for _, p := range pages {
				for _, b := range p.Blocks {
					all = append(all, text(b))
				}
			}
			joined := strings.Join(all, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("Parse(%q) lost %q; got %q", tc.src, tc.want, joined)
			}
		})
	}
}

// TestNestingDepth checks the guard rather than the recursion. DocTags is model
// output, so nesting depth is generator-controlled in the same way a PDF's tag tree
// is producer-controlled, and ADR 0001 fixed the same class of bug there.
func TestNestingDepth(t *testing.T) {
	deep := "<doctag>" + strings.Repeat("<text>", 5000) + "x"
	if _, err := Parse(deep, letter); err == nil {
		t.Error("5000-deep nesting accepted; the depth guard is not firing")
	}
	// Just under the limit still parses, so the guard is a bound and not a
	// coincidence of where the test happened to aim.
	ok := "<doctag>" + strings.Repeat("<text>", 60) + "x"
	if _, err := Parse(ok, letter); err != nil {
		t.Errorf("60-deep nesting rejected: %v", err)
	}
}

// TestListLevels covers the one place in DocTags where the container holds
// information the child does not: a list item carries no depth, so the only record
// of nesting is the <ordered_list>/<unordered_list> around it.
func TestListLevels(t *testing.T) {
	src := "<doctag><unordered_list><list_item>a</list_item>" +
		"<unordered_list><list_item>b</list_item>" +
		"<unordered_list><list_item>c</list_item></unordered_list></unordered_list>" +
		"<list_item>d</list_item></unordered_list>" +
		"<list_item>e</list_item></doctag>"

	pages, err := Parse(src, letter)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]int{"a": 1, "b": 2, "c": 3, "d": 1, "e": 1}
	for _, b := range pages[0].Blocks {
		if b.Role != doc.RoleListItem {
			t.Fatalf("role = %s, want list_item", b.Role)
		}
		if got := b.Level; got != want[text(b)] {
			t.Errorf("item %q level = %d, want %d", text(b), got, want[text(b)])
		}
	}
	// "e" is outside every container: level 1, because the only depth a bare item
	// has is the one it is at.
}

// TestFigureWithoutText pins the one case where a block with no text is still worth
// emitting: a picture is the only record that something occupied that rectangle, and
// its classification token is the only description of it available.
func TestFigureWithoutText(t *testing.T) {
	pages, err := Parse("<doctag><picture><loc_10><loc_10><loc_100><loc_100><bar_chart></picture>"+
		"<text><loc_10><loc_200><loc_100><loc_210></text></doctag>", letter)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := pages[0].Blocks
	if len(got) != 1 {
		t.Fatalf("blocks = %d, want 1: a textless picture is kept, a textless <text> is not", len(got))
	}
	if got[0].Role != doc.RoleFigure {
		t.Errorf("role = %s, want figure", got[0].Role)
	}
	if got[0].Alt != "bar_chart" {
		t.Errorf("Alt = %q, want the classification token as the only description there is", got[0].Alt)
	}
	if got[0].Box.IsZero() {
		t.Error("figure box is zero; the rectangle is the whole of what a textless picture records")
	}
}

// TestZeroBox is the honest-result case: a caller with no page geometry gets blocks
// with no rectangles rather than rectangles measured against a zero page.
func TestZeroBox(t *testing.T) {
	pages, err := Parse("<doctag><text><loc_10><loc_20><loc_30><loc_40>x</text></doctag>", geom.Rect{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if b := pages[0].Blocks[0].Box; !b.IsZero() {
		t.Errorf("box = %v, want zero for a zero page box", b)
	}
	if text(pages[0].Blocks[0]) != "x" {
		t.Error("text lost when no page box was given")
	}
}

// TestSpanTokens guards against the mistake that would be least visible in output:
// a picture-classification or code-language token read as literal text. "bar_chart"
// appearing in a caption is a plausible-looking word, so it would survive review.
func TestSpanTokens(t *testing.T) {
	pages, err := Parse("<doctag><code><loc_1><loc_2><loc_3><loc_4><_Python_>print(1)</code></doctag>", letter)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	b := pages[0].Blocks[0]
	if b.Role != doc.RoleCode {
		t.Errorf("role = %s, want code", b.Role)
	}
	if got := text(b); got != "print(1)" {
		t.Errorf("code text = %q; the language token must not become content", got)
	}
}

func text(b doc.Block) string {
	var sb strings.Builder
	for _, s := range b.Spans {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

func texts(bs []doc.Block) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = string(bs[i].Role) + ":" + text(b)
	}
	return out
}

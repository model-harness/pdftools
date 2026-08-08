package doc

import "testing"

// item builds a list-item block from spans, one style, no geometry — the marker rules
// read text and nothing else.
func item(texts ...string) Block {
	b := Block{Role: RoleListItem}
	for _, t := range texts {
		b.Spans = append(b.Spans, Span{Text: t, MCID: -1})
	}
	return b
}

// TestStripMarkerMovesItToTheField is the property the field exists for: after a strip
// the marker is out of the text and still in the model.
//
// Both halves matter. Out of the text, because the one sink that exists writes its own
// "- " and leaving it emits "- • First item." Still in the model, because a sink that
// can render a label has to be able to find one — dropping it here would make Marker
// write-only for the declared path and empty for this one.
func TestStripMarkerMovesItToTheField(t *testing.T) {
	b := item("• First item.")
	if !b.StripMarker() {
		t.Fatal("StripMarker = false, want true")
	}
	if got := b.Text(); got != "First item." {
		t.Errorf("text = %q, want %q", got, "First item.")
	}
	if b.Marker != "•" {
		t.Errorf("marker = %q, want %q", b.Marker, "•")
	}
	if b.Enumerated() {
		t.Error("Enumerated = true, want false: a bullet is not an ordered label")
	}
}

// TestStripMarkerAcrossSpans is testdata/reference/lists.pdf's nested item, where the en
// dash is a bold span of its own and the separator opens the roman span after it.
//
// Text() joins spans with no separator, so that leading space is inside the block's text
// and reaches the output: stopping the strip at the marker's span emitted "-  Nested
// item", with two spaces, against the gold file's one.
func TestStripMarkerAcrossSpans(t *testing.T) {
	b := item("–", " Nested item.")
	if !b.StripMarker() {
		t.Fatal("StripMarker = false, want true")
	}
	if got := b.Text(); got != "Nested item." {
		t.Errorf("text = %q, want %q", got, "Nested item.")
	}
	// The emptied span stays: a caller's span indices stay valid and Span.MCID survives
	// for diagnosis, and an empty span emits nothing.
	if len(b.Spans) != 2 || b.Spans[0].Text != "" {
		t.Errorf("spans = %+v, want the marker's span kept and empty", b.Spans)
	}
}

// TestStripMarkerPastALeadingSpaceSpan is the case where the marker is not in the first
// non-empty span at all: a producer opens the block with a span holding only whitespace.
//
// ListMarker trims before reading, so such a block is admitted on a marker that lives
// further along, and a strip that gave up at the first non-empty span would leave the
// marker in the text — emitting "- • Item." A mutation removing that branch survives
// every other test here, which is how the case was found.
func TestStripMarkerPastALeadingSpaceSpan(t *testing.T) {
	b := item("  ", "• Item.")
	if !b.StripMarker() {
		t.Fatal("StripMarker = false, want true")
	}
	if got := b.Text(); got != "Item." {
		t.Errorf("text = %q, want %q", got, "Item.")
	}
}

// TestStripMarkerRequiresASeparator is the gate that makes an allowlist of glyphs safe,
// asserted here because it is the only thing standing between the allowlist and prose.
//
// A marker glued to its text is not a marker. Measured over the corpus, all 1302 blocks
// opening with U+2022 separate it with a space and none glue it, while the excluded "-"
// is glued in 12 of 13 — so requiring the separator is what distinguishes a bullet a
// producer set from a rune that happens to lead a word.
//
// The last two cases are the length requirement: mupdf_explored.pdf sets a lone Wingdings
// square as a page decoration, so a marker with nothing after it is not an item either.
func TestStripMarkerRequiresASeparator(t *testing.T) {
	for _, txt := range []string{"–glued to its text", "•", "•   "} {
		b := item(txt)
		if b.StripMarker() {
			t.Errorf("StripMarker(%q) = true, want false", txt)
		}
		if got := b.Text(); got != txt {
			t.Errorf("text = %q, want %q unchanged: a rejected block keeps its text", got, txt)
		}
		if b.Marker != "" {
			t.Errorf("marker = %q, want empty", b.Marker)
		}
	}
}

// TestSetMarkerClosesTheGap is the declared path's whole reason for existing separately.
//
// sectionize takes the label's spans out of the item before gathering it, so there is no
// marker left in the text to strip — but there is whitespace where it was. Measured over
// every tagged list item on disk that declares a /Lbl, dropping the label's spans leaves
// the item's text opening with whitespace in 133 of 147, which a sink writing its own
// "- " renders as two spaces.
func TestSetMarkerClosesTheGap(t *testing.T) {
	b := item("  Adobe Acrobat Reader")
	b.SetMarker("•")

	if got := b.Text(); got != "Adobe Acrobat Reader" {
		t.Errorf("text = %q, want the leading space gone", got)
	}
	if b.Marker != "•" {
		t.Errorf("marker = %q, want %q", b.Marker, "•")
	}
}

// TestSetMarkerStopsAtContent keeps the trim from reaching into the item's own prose.
//
// Only the whitespace the label's removal exposed goes. A block whose first span is
// entirely whitespace is the case that makes this non-trivial: the trim has to cross that
// span and then stop at the first one with text, rather than trimming every span it sees.
func TestSetMarkerStopsAtContent(t *testing.T) {
	b := item(" ", "  First.", "   Second.")
	b.SetMarker("■")

	if got := b.Text(); got != "First.   Second." {
		t.Errorf("text = %q, want the second span's space kept", got)
	}
}

// TestEnumeratedSeparatesLabelsFromBullets is what the sink switches on, and the reason
// the field is a string rather than a rune.
//
// The ordered cases are the corpus's, all from Well-Tagged-PDF-WTPDF-1.0.pdf: "a.", "b.",
// and "[1]" through "[7]". They are unreachable from the glyph side — ADR 0011 records
// why, a leading number being also what a heading and a table row open with — so they
// exist here only because a producer declared them, and they are exactly the labels a
// sink must re-emit rather than drop.
func TestEnumeratedSeparatesLabelsFromBullets(t *testing.T) {
	cases := []struct {
		marker string
		want   bool
	}{
		{"", false},
		{"•", false},
		{"■", false},
		{"\uf06e", false}, // Wingdings' square via the PUA is still a bullet
		{"a.", true},
		{"[1]", true},
		{"1.", true},
		{"••", true}, // two glyphs is not a bullet this package set
	}
	for _, c := range cases {
		b := Block{Role: RoleListItem, Marker: c.marker}
		if got := b.Enumerated(); got != c.want {
			t.Errorf("Enumerated(%q) = %v, want %v", c.marker, got, c.want)
		}
	}
}

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
// A marker glued to its text is not a marker. Measured over the corpus, every block opening
// with U+2022 separates it with a space and none glues it — 1305 of 1305 over all extracted
// blocks — while the excluded "-" is glued in 12 of its 13. So requiring the separator is what
// distinguishes a bullet a producer set from a rune that happens to lead a word.
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

// TestListMarkerExcludesTheAmbiguousGlyphs is the allowlist's negative half, and it is the
// only thing that holds the exclusion in place.
//
// listMarkers' comment names "*", "-", ">" and two dots as deliberately excluded and gives
// the measurement, and nothing asserted it. Admitting all five passes every test in this
// repo, including the corpus goldens, so the exclusion was documented and unpinned.
//
// The cases are the corpus's own, and which ones they are is the point. A glued occurrence —
// "-o - output file name", the flag listMarkers' comment quotes — proves nothing here: the
// separator gate rejects it whatever the allowlist says, so it is a case
// TestStripMarkerRequiresASeparator already covers and it kills no mutation. The cases below
// are the *separated* occurrences on disk, four of them rows of ISO 32000-2's D.3
// PDFDocEncoding character set and the last a row of D.2's glyph-name list, whose first
// column is in each case the glyph itself. They are the Block.Text() the extractor produces,
// which is the string ListMarker is asked about — not what the markdown sink emits, since
// that reformats these same rows into table cells (`| **\*** | 42 | 0x2a | … |`) and so could
// not be a case for this function at all. Each is a marker, whitespace, then content, so the
// vocabulary is the only thing rejecting it — verified per glyph by mutation, each case
// failing on its own glyph alone.
//
// The two dots are two codepoints, which is worth stating because the row naming one contains
// the other: "·  27  0x1b  0033  U+02D9  DOT ABOVE" opens with U+00B7 MIDDLE DOT and only
// *describes* U+02D9. Reading a case off that row is how a mutation adding U+02D9 came to
// survive while the test looked like it covered "·" — the case exercised a glyph the row does
// not name. Both are here now, each taken from a row whose leading glyph is its own subject,
// and each kills its own mutation. Block-initial and separated over every block on disk,
// U+00B7 occurs 3 times and U+02D9 twice, all in one tagged file.
//
// Two populations, and the counts differ enough that conflating them would mislead. Over
// every block on disk, block-initial and separated/glued: "*" 5/8, "-" 1/12, U+00B7 3/0,
// U+02D9 2/0, ">" 3/6. Over the untagged RoleParagraph blocks layout.Lists actually
// considers: "*" 3/8, "-" 0/11, both dots 0/0, ">" 0/0. So on the path this allowlist
// governs, "-" never appears separated at all and the dots and ">" never appear — their
// block-initial occurrences are all in a tagged file, where sectionize declares them table
// cells and inferRoles never runs.
//
// That is why this test is the whole pin for three of the five: a per-glyph A/B over all 50
// documents changes **0 files** for either dot and for ">". Nothing in the output could catch
// any of them being admitted, which is exactly the case a corpus golden cannot cover and a
// unit assertion can. For the other two the A/B is decisive and points opposite ways: "*" alone creates 3
// false items in mupdf_explored.pdf, which are C comment continuation lines, and "-" alone
// fixes 3 doubled markers in ISO_32000-2_sponsored_EC3.pdf, which are real. Those 3 are
// *declared* RoleListItem blocks with a hyphen bullet and no /Lbl — the tagged path's defect,
// recorded open in DESIGN.md's open questions, under the bullet "A producer that sets a hyphen
// as its bullet and declares no /Lbl still emits a doubled marker", and not this
// allowlist's. A glyph rule firing on inferred geometry has to weigh the C code; one firing on
// a producer's own declaration does not.
func TestListMarkerExcludesTheAmbiguousGlyphs(t *testing.T) {
	// Annex D rows, ISO_32000-2_sponsored_EC3.pdf, as Block.Text() yields them. Each opens
	// with the glyph its own first column names — the requirement the U+02D9 case exists to
	// enforce, since the D.3 row *naming* U+02D9 opens with U+00B7 and pins the wrong one.
	// The fourth is a D.2 glyph-name row, which pairs two entries per line, so what matters
	// is only that "Dotaccentsmall" is the first and U+02D9 is the rune it is spelled with.
	for _, txt := range []string{
		"*  42  0x2a  0052  U+002A  ASTERISK",
		"-  45  0x2d  0055  U+002D  HYPHEN-MINUS",
		"·  183  0xb7  0267  U+00B7  MIDDLE DOT",
		"˙  Dotaccentsmall  372    ᴘ  Psmall  160",
		">  62  0x3e  0076  U+003E  GREATER THAN SIGN (&gt;)",
	} {
		if got := ListMarker(txt); got != 0 {
			t.Errorf("ListMarker(%q) = %q, want 0: the glyph is excluded", txt, got)
		}
		// The StripMarker half is not extra coverage of the span walk — ListMarker gates
		// entry, so on these inputs the walk never runs. It asserts the composition: that
		// the mutating operation consults the vocabulary rather than carrying a second
		// copy of it, which is the split doc/marker.go's own header warns about. It is the
		// text assertion after it that earns the lines, since "returned false" and "left
		// the block alone" are different claims and only the second is what a caller sees.
		b := item(txt)
		if b.StripMarker() {
			t.Errorf("StripMarker(%q) = true, want false", txt)
		}
		if got := b.Text(); got != txt {
			t.Errorf("text = %q, want %q unchanged", got, txt)
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
// and "[1]" through "[7]". They arrive by either of two routes now — a producer's declared
// /Lbl, or OrderedLabel reading a run of them off an untagged page — and they are exactly
// the labels a sink must re-emit rather than drop.
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

// TestOrderedLabelReadsTheFormsOnDisk is the vocabulary itself: the five shapes the corpus
// contains, and the near-misses that must not read as labels.
//
// The negatives are the point. A leading number is what a numbered heading opens with and
// what a table's first column holds, which is ADR 0011's objection, so what keeps this from
// firing on those is the delimiter and the separator — "7.4 Filters" has no delimiter after
// its number, and "1.5" has no separator after its. Each negative below is a form that
// occurs in the corpus and must stay unrecognized.
func TestOrderedLabelReadsTheFormsOnDisk(t *testing.T) {
	cases := []struct {
		txt  string
		lbl  string
		val  int
		note string
	}{
		{"1. Introduction to it.", "1.", 1, "arabic with a period"},
		{"10) The tenth step.", "10)", 10, "two digits"},
		{"[7] ISO/IEC 8825-1, ASN.1.", "[7]", 7, "a bibliography entry"},
		{"100. The hundredth.", "100.", 100, "three digits is the cap, and it is inclusive"},
		{"a. The first alternative.", "a.", 1, "alphabetic starts the sequence at 1"},
		{"c) Accumulate the sequence.", "c)", 3, "the corpus's most common form"},
		{"  1. Leading space is trimmed.", "1.", 1, "Text() carries the page's own spacing"},
		{"1. A non-breaking separator.", "1.", 1, "producers use U+00A0 routinely"},

		{"7.4 Filters", "", 0, "a clause number: no delimiter after the number"},
		{"1 Scope", "", 0, "the same, single digit"},
		{"1.5 is a value.", "", 0, "no separator after the period"},
		{"Figure 1. The graph.", "", 0, "a label must open the block"},
		{"1.", "", 0, "no content: a table cell or a figure number"},
		{"1) ", "", 0, "whitespace is not content"},
		{"A. A sentence opening.", "", 0, "uppercase is not admitted: too much prose"},
		{"aa) Two letters.", "", 0, "not a form on disk"},
		{"(1) Parenthesized arabic.", "", 0, "measured at zero occurrences, so not admitted"},
		{"(b) Parenthesized letter.", "", 0, "the same"},
		{"2026. A year.", "", 0, "four digits is not a label"},
		{"- An item.", "", 0, "a bullet is ListMarker's, not this"},
		{"", "", 0, ""},
	}
	for _, c := range cases {
		lbl, val := OrderedLabel(c.txt)
		if lbl != c.lbl || val != c.val {
			t.Errorf("OrderedLabel(%q) = %q, %d, want %q, %d (%s)",
				c.txt, lbl, val, c.lbl, c.val, c.note)
		}
	}
}

// TestStripOrderedLabelMovesItToTheField is StripMarker's property for the ordered form:
// out of the text, still in the model, and the separator gone with it.
func TestStripOrderedLabelMovesItToTheField(t *testing.T) {
	b := item("[3] ISO 32000-2.")
	if !b.StripOrderedLabel() {
		t.Fatal("StripOrderedLabel = false, want true")
	}
	if got := b.Text(); got != "ISO 32000-2." {
		t.Errorf("text = %q, want %q", got, "ISO 32000-2.")
	}
	if b.Marker != "[3]" {
		t.Errorf("marker = %q, want %q", b.Marker, "[3]")
	}
	if !b.Enumerated() {
		t.Error("Enumerated = false, want true: the sink switches on this to write the label")
	}
}

// TestStripOrderedLabelAcrossSpans is the case that makes this a separate function rather
// than a branch in StripMarker: a label is several runes and a style change can split it.
//
// A producer setting the number bold and the delimiter roman writes "1" and ") text". A
// strip that decoded one rune like StripMarker's would leave ") text" behind, and one that
// edited only the first span would leave the delimiter.
func TestStripOrderedLabelAcrossSpans(t *testing.T) {
	b := item("1", ") The first step.")
	if !b.StripOrderedLabel() {
		t.Fatal("StripOrderedLabel = false, want true")
	}
	if got := b.Text(); got != "The first step." {
		t.Errorf("text = %q, want %q", got, "The first step.")
	}
	if b.Marker != "1)" {
		t.Errorf("marker = %q, want %q", b.Marker, "1)")
	}
	if len(b.Spans) != 2 || b.Spans[0].Text != "" {
		t.Errorf("spans = %+v, want the label's span kept and empty", b.Spans)
	}
}

// TestStripOrderedLabelPastALeadingSpaceSpan is StripMarker's whitespace-span case, which
// applies here for the same reason: OrderedLabel trims before matching, so a block can be
// admitted on a label that lives past a span holding nothing but spacing.
//
// The rune count this walks includes that leading whitespace, which is what makes the two
// halves agree — counting only the label's own runes would stop one span early and leave
// "[10]" in the text.
//
// It is also the case where the label ends exactly on a span boundary, which takes the
// len(rs) <= n branch with equality rather than the split branch: the count reaches zero with
// no runes of that span left over, so the following span has to supply the content.
func TestStripOrderedLabelPastALeadingSpaceSpan(t *testing.T) {
	b := item("  ", "[10]", " Item ten.")
	if !b.StripOrderedLabel() {
		t.Fatal("StripOrderedLabel = false, want true")
	}
	if got := b.Text(); got != "Item ten." {
		t.Errorf("text = %q, want %q", got, "Item ten.")
	}
	if b.Marker != "[10]" {
		t.Errorf("marker = %q, want %q", b.Marker, "[10]")
	}
}

// TestStripOrderedLabelLeavesUnlabelledTextAlone is the gate: a block that will not be
// promoted must not be edited. Without it a heading's clause number would be stripped by
// any caller that asked.
func TestStripOrderedLabelLeavesUnlabelledTextAlone(t *testing.T) {
	b := item("7.4 ", "Filters")
	if b.StripOrderedLabel() {
		t.Fatal("StripOrderedLabel = true on a clause number, want false")
	}
	if got := b.Text(); got != "7.4 Filters" {
		t.Errorf("text = %q, want it untouched", got)
	}
	if b.Marker != "" {
		t.Errorf("marker = %q, want empty", b.Marker)
	}
}

// TestStripOrderedLabelStopsAtContent is the trim's bound, and it is the mutation the other
// cases do not catch: a strip that kept trimming would eat the indentation of a continuation
// line inside the item.
func TestStripOrderedLabelStopsAtContent(t *testing.T) {
	b := item("a) First.", "  and its continuation.")
	if !b.StripOrderedLabel() {
		t.Fatal("StripOrderedLabel = false, want true")
	}
	if got := b.Text(); got != "First.  and its continuation." {
		t.Errorf("text = %q, want the second span's spacing kept", got)
	}
}

package layout

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
)

// mono is a span set in a monospaced face, at 10pt, its top at y.
func mono(text string, y float64) doc.Span {
	return doc.Span{Text: text, Page: 1, MCID: 1, Box: geom.NewRect(72, y-10, 300, y), Style: doc.Style{Size: 10, Mono: true}}
}

// TestAListingIsMonospacedOverLines: text set wholly in a monospaced face over more than one
// line is a listing, whether its lines are one span the extractor broke or spans one under
// another; one line of it is code in a sentence, a proportional span in it is prose, and a
// document whose body is set in a monospaced face has no listings by face alone.
func TestAListingIsMonospacedOverLines(t *testing.T) {
	prose := mono("is the file", 680)
	prose.Style.Mono = false
	for _, c := range []struct {
		name     string
		spans    []doc.Span
		monoBody bool
		want     bool
	}{
		{"lines broken in one span", []doc.Span{mono("# one\n# two", 700)}, false, true},
		// ISO 32000-2's "12 0 R": one line of code, and the blank line its span ran on into.
		{"one line and a blank one", []doc.Span{mono("12 0 R\n ", 700)}, false, false},
		{"one line, its break in a space", []doc.Span{mono("12 0 R", 700), {Text: "\n\n", Page: 1, Box: geom.NewRect(72, 670, 300, 700)}}, false, false},
		{"one span under another", []doc.Span{mono("feat: add it", 700), mono("Implements it", 676)}, false, true},
		// Line boxes that touch are two lines: the bound is inclusive.
		{"lines that touch", []doc.Span{mono("feat: add it", 700), mono("Implements it", 690)}, false, true},
		// The page's foot and the next page's head: a listing across a page break, as the
		// paper's JSON sample runs, whatever their heights.
		{"across a page", []doc.Span{mono("{", 80), func() doc.Span { s := mono(`"version": "1.0"`, 700); s.Page = 2; return s }()}, false, true},
		{"one line", []doc.Span{mono("main.go", 700)}, false, false},
		{"two spans on one line", []doc.Span{mono("main", 700), mono(".go", 700)}, false, false},
		{"with prose", []doc.Span{mono("main.go", 700), prose}, false, false},
		{"with only space between", []doc.Span{mono("a", 700), {Text: " ", Page: 1, Box: geom.NewRect(72, 678, 80, 688)}, mono("b", 676)}, false, true},
		{"a monospaced body", []doc.Span{mono("# one\n# two", 700)}, true, false},
	} {
		if got := Listing(c.spans, c.monoBody); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMonoBodyIsMostOfTheText: a document is set in a monospaced face when most of its text is.
func TestMonoBodyIsMostOfTheText(t *testing.T) {
	page := func(spans ...doc.Span) *doc.Document {
		return &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{{Spans: spans}}}}}
	}
	body := mono("The fields are kept for every sailing, and read once a day.", 700)
	body.Style.Mono = false
	if MonoBody(page(body, mono("x = 1", 680))) {
		t.Error("a document of prose with a line of code reads as a monospaced body")
	}
	if !MonoBody(page(mono("The fields are kept for every sailing, and read once a day.", 700), mono("x", 680))) {
		t.Error("a document set in a monospaced face does not read as one")
	}
	// Half is not most: a strict majority, so a document of equal parts is not a typescript.
	half := mono("abcd", 680)
	half.Style.Mono = false
	if MonoBody(page(mono("wxyz", 700), half)) {
		t.Error("a document half in a monospaced face reads as set in one")
	}
}

// TestListingsAreCode: on the untagged path, a paragraph that is a listing becomes code, and
// prose stays prose.
func TestListingsAreCode(t *testing.T) {
	prose := mono("The fields are kept for every sailing, and read once a day.", 740)
	prose.Style.Mono = false
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		{Role: doc.RoleParagraph, Spans: []doc.Span{prose}},
		{Role: doc.RoleParagraph, Spans: []doc.Span{mono("# one\n# two", 700)}},
	}}}}
	Listings(d)
	if got := d.Pages[0].Blocks; got[0].Role != doc.RoleParagraph || got[1].Role != doc.RoleCode {
		t.Errorf("roles %s, %s; want paragraph, code", got[0].Role, got[1].Role)
	}
}

// TestBreaksFoldOutsideAListing: the line breaks the extractor writes between monospaced lines
// are a listing's alone; anywhere else they are the wrap's space they stand for, so one line of
// code that the next line's element took the rest from reads as it did.
func TestBreaksFoldOutsideAListing(t *testing.T) {
	spans := []doc.Span{mono("(This is a string)\n", 700), {Text: "and\nmore"}}
	FoldBreaks(spans)
	if spans[0].Text != "(This is a string) " || spans[1].Text != "and more" {
		t.Errorf("%q %q, want each break a space", spans[0].Text, spans[1].Text)
	}
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		{Role: doc.RoleParagraph, Spans: []doc.Span{mono("(This is a string)\n", 700)}},
	}}}}
	Listings(d)
	if b := d.Pages[0].Blocks[0]; b.Role != doc.RoleParagraph || b.Spans[0].Text != "(This is a string) " {
		t.Errorf("%s %q, want a paragraph with its break folded", b.Role, b.Spans[0].Text)
	}
}

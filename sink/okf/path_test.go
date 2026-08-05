package okf

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

func TestSlug(t *testing.T) {
	for _, c := range []struct {
		number, title, parent string
		want                  string
	}{
		{"7.5.8", "7.5.8 Filters", "7.5", "8-filters"},
		{"7.5.8", "7.5.8 Filters", "", "7-5-8-filters"},
		// No number: the title carries the name alone.
		{"", "Foreword", "", "foreword"},
		// A number whose parent is not its prefix keeps the whole thing — Annex A.2 under
		// a clause numbered 8 is not "A.2 minus 8".
		{"A.2", "A.2 Deprecated", "8", "a-2-deprecated"},
		// Punctuation and case collapse; the dots inside a number become hyphens because a
		// dot in a filename reads as an extension.
		{"7.6.4.3", "7.6.4.3 Public-Key: Encryption", "7.6.4", "3-public-key-encryption"},
		// Truncated at a word boundary, not mid-word, and the number is never cut.
		{"1.2", "1.2 " + strings.Repeat("verylongword ", 12), "1", "2-" + strings.Repeat("verylongword-", 3) + "verylongword"},
	} {
		s := &doc.Section{Number: c.number, Title: c.title}
		if got := slug(s, c.parent, 0); got != c.want {
			t.Errorf("slug(%q, %q, parent %q) = %q, want %q", c.number, c.title, c.parent, got, c.want)
		}
	}

	// Neither a number nor an ASCII title: the position among siblings is the only thing
	// left that distinguishes it, and a name is better than a dropped clause.
	if got := slug(&doc.Section{Title: "日本語"}, "", 11); got != "section-12" {
		t.Errorf("non-ASCII title fell back to %q, want section-12", got)
	}
}

func TestLayoutBoundsPaths(t *testing.T) {
	// A chain of clauses with very long titles, deeper than the corpus goes. Every path must
	// come out inside the bound, which is only true because fit() falls back to the clause
	// number when the readable name will not fit.
	long := strings.Repeat("extremely-verbose-clause-title ", 6)
	root := &doc.Section{Number: "1", Title: "1 " + long}
	cur := root
	for i := 2; i <= 8; i++ {
		num := cur.Number + ".1"
		kid := &doc.Section{Number: num, Title: num + " " + long, Parent: cur}
		cur.Kids = []*doc.Section{kid}
		cur = kid
	}

	o := &doc.Outline{Sections: []*doc.Section{root}}
	l := newLayout(o)
	deepest := 0
	o.Walk(func(s *doc.Section) bool {
		p := l.file[s]
		if p == "" {
			t.Errorf("section %q got no path", s.Number)
		}
		if len(p) > deepest {
			deepest = len(p)
		}
		if len(p) > MaxPath {
			t.Errorf("path is %d characters, over the %d bound: %s", len(p), MaxPath, p)
		}
		return true
	})
	t.Logf("deepest path %d characters at 8 levels", deepest)
}

func TestISOID(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ISO 32000-2:2020(en), Document management", "iso32000-2:2020"},
		{"ISO/TS 32005", "iso32005"},
		{"ISO/IEC 19005-1:2005", "iso19005-1:2005"},
		{"ISO 32000-2-2020", "iso32000-2:2020"},
		// Not an ISO document: the caller gets the kebabbed title, via docID.
		{"Well-Tagged PDF (WTPDF)", ""},
		{"ISO", ""},
	} {
		if got := isoID(c.in); got != c.want {
			t.Errorf("isoID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDocID(t *testing.T) {
	for _, c := range []struct {
		meta doc.Metadata
		want string
	}{
		{doc.Metadata{Title: "ISO 32000-2:2020(en), Document management"}, "iso32000-2:2020"},
		{doc.Metadata{Title: "Well-Tagged PDF (WTPDF) 1.0"}, "well-tagged-pdf-wtpdf-1-0"},
		// No title: the filename, which is what the user typed and so is recognizable.
		{doc.Metadata{Path: `C:\corpus\PDF20_AN002-AF.pdf`}, "pdf20-an002-af"},
		{doc.Metadata{}, "document"},
	} {
		if got := docID(c.meta); got != c.want {
			t.Errorf("docID(%+v) = %q, want %q", c.meta, got, c.want)
		}
	}
}

func TestFmtPages(t *testing.T) {
	for _, c := range []struct {
		first, last int
		want        string
	}{
		{412, 414, "pages 412–414"},
		{412, 412, "page 412"},
		{412, 0, "page 412"},
		{0, 0, ""},
	} {
		s := &doc.Section{FirstPage: c.first, LastPage: c.last}
		if got := fmtPages(s); got != c.want {
			t.Errorf("fmtPages(%d, %d) = %q, want %q", c.first, c.last, got, c.want)
		}
	}
}

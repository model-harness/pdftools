package okf

import (
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/doc"
)

func testXref() *xref {
	o := &doc.Outline{Sections: []*doc.Section{
		{Number: "7.4", Title: "7.4 Filters"},
		{Number: "7.6", Title: "7.6 Encryption"},
		{Number: "A.2", Title: "A.2 Annex"},
	}}
	return newIndex(o, newLayout(o))
}

func TestXrefLink(t *testing.T) {
	x := testXref()
	for _, c := range []struct {
		in, want string
		n        int
	}{
		// The cue word is what makes a number a reference. Without one it is a quantity, a
		// version, or a table number, and a wrong link is worse than a missing one because
		// it looks authoritative.
		{"as described in clause 7.4 above", "as described in clause [7.4](/7-4-filters.md) above", 1},
		{"see 7.6 for details", "see [7.6](/7-6-encryption.md) for details", 1},
		{"see Annex A.2", "see Annex [A.2](/a-2-annex.md)", 1},
		{"§7.4 applies", "§7.4 applies", 0},
		{"§ 7.4 applies", "§ [7.4](/7-4-filters.md) applies", 1},
		// No cue: PDF 1.7 is a version and Table 7.4 is a table.
		{"conforms to PDF 7.4", "conforms to PDF 7.4", 0},
		{"Table 7.4 lists them", "Table 7.4 lists them", 0},
		// A clause the document does not contain stays as text rather than becoming a dead
		// link, which OKF §11 would have consumers silently accept.
		{"see clause 99.1", "see clause 99.1", 0},
		// A bare integer is a quantity far more often than a reference, and the index cannot
		// tell the difference.
		{"see clause 7", "see clause 7", 0},
		// The sentence's period is not the number's.
		{"described in clause 7.4.", "described in clause [7.4](/7-4-filters.md).", 1},
		// A code span is a literal the document is quoting.
		{"the value `see 7.4` is a string", "the value `see 7.4` is a string", 0},
		// A destination already written by the sink: a path is digits and dots and would
		// otherwise look exactly like a clause number.
		{"- [7.4 Filters](/7-4-filters.md)", "- [7.4 Filters](/7-4-filters.md)", 0},
	} {
		got, n := x.link(c.in, "/other.md")
		if got != c.want || n != c.n {
			t.Errorf("link(%q) = %q (%d links), want %q (%d)", c.in, got, n, c.want, c.n)
		}
	}
}

func TestXrefSkipsSelf(t *testing.T) {
	// A clause quoting its own number must not link to the file it is in: a self-link tells
	// a reader there is somewhere else to go and there is not.
	x := testXref()
	got, n := x.link("see clause 7.4 for this", "/7-4-filters.md")
	if n != 0 || strings.Contains(got, "](") {
		t.Errorf("linked to self: %q (%d links)", got, n)
	}
}

func TestXrefSkipsCodeFences(t *testing.T) {
	// A fenced block is verbatim by definition — sink/markdown does not even escape it — so
	// rewriting inside one would corrupt the one kind of content in a specification that
	// must survive byte for byte.
	x := testXref()
	md := "before, see clause 7.4\n\n```\nsee clause 7.4\n```\n\nafter, see clause 7.6\n"
	got, n := x.link(md, "/other.md")
	if n != 2 {
		t.Errorf("resolved %d links, want 2 (one on each side of the fence)", n)
	}
	if !strings.Contains(got, "```\nsee clause 7.4\n```") {
		t.Errorf("rewrote inside the fence:\n%s", got)
	}
}

func TestClauseNumber(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"7.4 and more", "7.4"},
		{"7.6.4.4.8, \"Algorithm 9\"", "7.6.4.4.8"},
		{"A.2)", "A.2"},
		{"7.4. Next sentence", "7.4"},
		// One component is not a reference this can distinguish from a quantity.
		{"7 bytes", ""},
		{"A ", ""},
		{"", ""},
	} {
		if got := clauseNumber(c.in); got != c.want {
			t.Errorf("clauseNumber(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstSentence(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"A filter transforms a stream. It shall be applied in order.", "A filter transforms a stream."},
		// A period inside a clause number or a decimal is not a sentence boundary, and
		// standards prose is denser in those than in sentences.
		{"Clause 7.5.8 of ISO 32000-2:2020 applies. Next.", "Clause 7.5.8 of ISO 32000-2:2020 applies."},
		// An initial or an enumerator.
		{"See A. Smith for details. Next.", "See A. Smith for details."},
		{"No terminator at all", "No terminator at all"},
		{"", ""},
	} {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// A sentence longer than the bound is truncated at a word boundary and marked, which is
	// the honest failure: a 300-character description has stopped being a summary either way.
	long := strings.Repeat("word ", 200) + "end. Next."
	got := firstSentence(long)
	if len(got) > maxDescription || !strings.HasSuffix(got, "…") {
		t.Errorf("long sentence truncated to %d characters ending %q", len(got), got[len(got)-4:])
	}
	if strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
		t.Errorf("truncation left a trailing space: %q", got)
	}
}

func TestISODate(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"D:20240131120000+01'00'", "2024-01-31"},
		{"20240131", "2024-01-31"},
		{"D:2024013", ""},
		// Digits but not a date. A wrong date in a provenance field is worse than an absent
		// one, and §11 makes the absence safe.
		{"D:20240000", ""},
		{"D:20241331", ""},
		{"D:00000101", ""},
		{"", ""},
	} {
		if got := isoDate(c.in); got != c.want {
			t.Errorf("isoDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTags(t *testing.T) {
	root := &doc.Section{Title: "7 Syntax"}
	mid := &doc.Section{Title: "7.4 Filters", Parent: root}
	leaf := &doc.Section{Title: "7.4.1 General", Parent: mid}

	if got := tags(leaf); len(got) != 2 || got[0] != "7 Syntax" || got[1] != "7.4 Filters" {
		t.Errorf("tags(leaf) = %v, want the two ancestors", got)
	}
	if got := tags(root); got != nil {
		t.Errorf("tags(root) = %v, want nil: a root clause has no ancestry", got)
	}
}

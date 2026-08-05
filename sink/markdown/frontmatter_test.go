package markdown

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

func frontmatterOf(t *testing.T, m doc.Metadata) string {
	t.Helper()
	d := &doc.Document{Meta: m, Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{para(span("body"))}}}}
	out := String(d, Options{Frontmatter: true})
	_, rest, ok := strings.Cut(out, "---\n")
	if !ok {
		t.Fatalf("no opening delimiter: %q", out)
	}
	block, _, ok := strings.Cut(rest, "---\n")
	if !ok {
		t.Fatalf("no closing delimiter: %q", out)
	}
	return block
}

func TestFrontmatterOffByDefault(t *testing.T) {
	d := &doc.Document{
		Meta:  doc.Metadata{Title: "T"},
		Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{para(span("body"))}}},
	}
	if got := String(d, DefaultOptions); got != "body\n" {
		t.Errorf("got %q", got)
	}
}

// An absent /Title and a title that is the empty string are the same thing in a
// PDF, and a consumer should not have to distinguish them.
func TestEmptyFieldsOmitted(t *testing.T) {
	got := frontmatterOf(t, doc.Metadata{Title: "Only this"})
	if strings.Contains(got, "author:") || strings.Contains(got, `""`) {
		t.Errorf("empty fields emitted: %q", got)
	}
	if !strings.Contains(got, "title: Only this\n") {
		t.Errorf("title missing: %q", got)
	}
}

// tagged and encrypted report which extraction path ran. An absent key cannot be
// told from a key the writer did not know about, so false is stated.
func TestBooleansAlwaysEmitted(t *testing.T) {
	got := frontmatterOf(t, doc.Metadata{})
	if !strings.Contains(got, "tagged: false\n") {
		t.Errorf("tagged missing: %q", got)
	}
	if !strings.Contains(got, "encrypted: false\n") {
		t.Errorf("encrypted missing: %q", got)
	}
	got = frontmatterOf(t, doc.Metadata{Tagged: true, Encrypted: true})
	if !strings.Contains(got, "tagged: true\n") || !strings.Contains(got, "encrypted: true\n") {
		t.Errorf("got %q", got)
	}
}

// Field order is fixed so two conversions of the same document diff cleanly.
func TestFieldOrderStable(t *testing.T) {
	m := doc.Metadata{
		Title: "T", Author: "A", Subject: "S", Lang: "en",
		Path: "in.pdf", Version: "2.0", Creator: "C", Producer: "P",
	}
	got := frontmatterOf(t, m)
	want := []string{"title:", "author:", "subject:", "lang:", "source:",
		"pdf_version:", "creator:", "producer:", "tagged:", "encrypted:"}
	at := 0
	for _, key := range want {
		i := strings.Index(got[at:], key)
		if i < 0 {
			t.Fatalf("%s missing or out of order in %q", key, got)
		}
		at += i
	}
}

// A PDF date is "D:20240131120000Z" and is frequently malformed. Converting it to a
// YAML timestamp would mean dropping the ones that do not parse or inventing a
// value; the string a producer wrote is checkable against the file.
//
// Both are unquoted, and the "D:" form is the reason to check: a colon needs
// quoting only when whitespace follows it, and here a digit does. Quoting it anyway
// would be harmless but the assertion would then say nothing about the rule.
func TestDatesEmittedAsWritten(t *testing.T) {
	got := frontmatterOf(t, doc.Metadata{Created: "D:20240131120000Z", Modified: "garbage"})
	if !strings.Contains(got, "created: D:20240131120000Z\n") {
		t.Errorf("created not verbatim: %q", got)
	}
	if !strings.Contains(got, "modified: garbage\n") {
		t.Errorf("modified not verbatim: %q", got)
	}
}

func TestYAMLQuoting(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"plain", "Simple title", "Simple title"},
		// ": " ends a key. This exact title is the corpus's own.
		{"colon space", "ISO 32000-2: Document management", `"ISO 32000-2: Document management"`},
		{"trailing colon", "Note:", `"Note:"`},
		// A colon inside a word does not, and a version string with one is common.
		{"colon in word", "iso32000-2:2020", "iso32000-2:2020"},
		{"leading dash", "- dash", `"- dash"`},
		{"leading quote", `"quoted"`, `"\"quoted\""`},
		{"leading brace", "{a}", `"{a}"`},
		{"comment", "a # b", `"a # b"`},
		{"hash in word", "a#b", "a#b"},
		{"backslash", `C:\path`, `"C:\\path"`},
		{"newline", "a\nb", `"a\nb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"control byte", "a\x01b", `"a\x01b"`},
		{"leading space", " a", `" a"`},
		// A loader resolves these to a non-string type, which is no longer the
		// title of the document.
		{"bool word", "true", `"true"`},
		{"y", "Y", `"Y"`},
		{"null", "null", `"null"`},
		{"number", "1.7", `"1.7"`},
		{"integer", "42", `"42"`},
		{"not a number", "1.7.2", "1.7.2"},
	} {
		if got := yamlString(tc.in); got != tc.want {
			t.Errorf("%s: yamlString(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// The version string is the case that matters most: a loader turning "1.7" into a
// float loses the distinction between "1.7" and "1.70".
func TestVersionQuoted(t *testing.T) {
	got := frontmatterOf(t, doc.Metadata{Version: "1.7"})
	if !strings.Contains(got, `pdf_version: "1.7"`) {
		t.Errorf("got %q", got)
	}
}

// A control byte in a metadata string would make the block unparseable, and
// dropping it would silently alter the value.
func TestControlBytesEscapedNotDropped(t *testing.T) {
	got := frontmatterOf(t, doc.Metadata{Title: "a\x00b"})
	if !strings.Contains(got, `"a\x00b"`) {
		t.Errorf("got %q", got)
	}
}

func TestFrontmatterFollowedByBlankLine(t *testing.T) {
	d := &doc.Document{
		Meta:  doc.Metadata{Title: "T"},
		Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{para(span("body"))}}},
	}
	got := String(d, Options{Frontmatter: true})
	if !strings.HasSuffix(got, "---\n\nbody\n") {
		t.Errorf("got %q", got)
	}
}

// Frontmatter on a document with no readable pages is still a valid document.
func TestFrontmatterWithNoPages(t *testing.T) {
	got := String(&doc.Document{Meta: doc.Metadata{Title: "T"}}, Options{Frontmatter: true})
	if got != "---\ntitle: T\ntagged: false\nencrypted: false\n---\n\n" {
		t.Errorf("got %q", got)
	}
}

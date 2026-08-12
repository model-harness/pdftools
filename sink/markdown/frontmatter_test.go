package markdown

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
	"gopkg.in/yaml.v2"
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
		// All four edge-whitespace positions, because one of them does not cover the
		// others and the gap was measured rather than guessed: with only " a" here, a
		// rule checking leading space alone survived every test in the repository, as
		// did one checking trailing only. A loader silently strips all four — "title: a "
		// loads as "a", with no error to notice — so this is a value-corruption class
		// rather than a parse failure, and TestQuotingRoundTrips below is the assertion
		// that generalizes it.
		//
		// The two tab cases are rejected by the control-byte rule below rather than by
		// the space rule, since a tab is 0x09. That is worth stating because it is why
		// they are not redundant with "a\tb" above — removing the control-byte rule
		// alone fails only the interior case, and these two pin the edges.
		//
		// It is not, however, enough to make narrowing TrimSpace to Trim(s, " ") an
		// equivalent mutant, which is what this comment claimed until the difference was
		// measured. TrimSpace trims every rune unicode.IsSpace accepts, and the ones
		// above 0x7f — U+00A0, U+2002, U+3000 and the three line breaks below — are
		// multi-byte, so no byte-wise rule in plainYAML sees them. The narrowed rule
		// emits a leading U+2028 unquoted, and the block then stops parsing. The tab
		// half of the claim was right and the general form of it was wrong.
		{"leading space", " a", `" a"`},
		{"trailing space", "a ", `"a "`},
		{"leading tab", "\ta", `"\ta"`},
		{"trailing tab", "a\t", `"a\t"`},
		// The line breaks a byte scan cannot see, and the defect they were found in.
		// YAML 1.2 §5.4 counts NEL, LS and PS as line breaks and yaml.v2 implements it,
		// so emitted raw one of these ends the line: the loader reads the rest of the
		// value as a new line of the block, fails on it, and every key below title —
		// pages, tagged, encrypted — is simply gone. Before this, plainYAML returned
		// true for all three, so that is what a /Title carrying one produced. 0 of 23816
		// frontmatter lines on disk carry one, which is why no corpus test caught it.
		//
		// Interior position on purpose: at an edge they are already rejected, because
		// TrimSpace trims Unicode space. Interior was the position with no rule at all.
		// The escapes are YAML's own N, L and P rather than \xNN, which is defined
		// only for 8-bit values — and escaping matters as much as quoting here, since a
		// quoted raw NEL loads back as a plain space.
		{"NEL", "a\u0085b", `"a\Nb"`},
		{"line separator", "a\u2028b", `"a\Lb"`},
		{"paragraph separator", "a\u2029b", `"a\Pb"`},
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

// TestQuotingRoundTrips is the property behind the table above: whatever yamlString
// emits, a loader must hand back the string it was given.
//
// The table asserts the *rendering* — that " a" is quoted, that "1.7" is quoted — and a
// rendering assertion is a point check on a rule with a large input space. This asserts
// the reason the rule exists, which is that the value survives. Both are worth having:
// the table says what the output looks like so a reader can see the convention, and this
// says that the convention is correct for every case in it, plus the ones nobody thought
// to tabulate.
//
// It is what makes the whitespace cases above more than three more points. Every kind of
// edge whitespace is silently *stripped* by a loader rather than rejected — "title: a "
// loads as "a" with no error — so the failure is a corrupted value, not a broken
// document, and nothing that only checks the block parses can see it. The same is true
// of the type coercions: "1.7" arriving as a float64 round-trips to "1.7" as far as YAML
// is concerned and is no longer the version string of the document.
//
// gopkg.in/yaml.v2 rather than v3, and rather than a hand-rolled parser, for the reason
// TestOKFFrontmatterLoads gives: it is already a direct dependency, so reading the output
// the way a consumer will costs nothing the build did not already carry.
func TestQuotingRoundTrips(t *testing.T) {
	for _, in := range []string{
		"Simple title",
		"ISO 32000-2: Document management",
		"Note:",
		"iso32000-2:2020",
		"- dash",
		`"quoted"`,
		"{a}",
		"a # b",
		"a#b",
		`C:\path`,
		"a\nb",
		"a\tb",
		"a\x01b",
		" a", "a ", "\ta", "a\t", "  a  ",
		"a\u0085b", "a\u2028b", "a\u2029b",
		"\u0085a", "\u2028a", "a\u2029",
		"true", "Y", "null", "~", "off",
		"1.7", "42", "1.7.2", "0",
		"D:20240131120000Z",
		// Not ASCII, and in the corpus: a title with a non-breaking space or an en dash
		// must survive a path whose escaping is written byte by byte.
		"PDF\u00a02.0 \u2014 Part\u00a01",
	} {
		var got yaml.MapSlice
		emitted := "title: " + yamlString(in) + "\n"
		if err := yaml.Unmarshal([]byte(emitted), &got); err != nil {
			t.Errorf("yamlString(%q) emitted %q, which does not load: %v", in, emitted, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("yamlString(%q) emitted %q, which loads as %d keys, want 1", in, emitted, len(got))
			continue
		}
		// A string, not an int or a bool: the type is half the assertion, since a value
		// that loads as 1.7 has lost the difference between "1.7" and "1.70" without
		// ever failing to parse.
		v, ok := got[0].Value.(string)
		if !ok {
			t.Errorf("yamlString(%q) emitted %q, which loads as %T (%v), want string", in, emitted, got[0].Value, got[0].Value)
			continue
		}
		if v != in {
			t.Errorf("yamlString(%q) emitted %q, which loads back as %q", in, emitted, v)
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

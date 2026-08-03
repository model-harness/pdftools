package okf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/doc"
)

// sec builds a section, wiring parents so Path and the layout work.
func sec(number, title string, kids ...*doc.Section) *doc.Section {
	s := &doc.Section{Number: number, Title: title, Kids: kids}
	for _, k := range kids {
		k.Parent = s
	}
	return s
}

func para(text string) doc.Block {
	return doc.Block{Role: doc.RoleParagraph, Spans: []doc.Span{{Text: text}}}
}

// find returns the file at path, or fails.
func find(t *testing.T, files []File, path string) File {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	var have []string
	for _, f := range files {
		have = append(have, f.Path)
	}
	t.Fatalf("no %s in bundle; have %v", path, have)
	return File{}
}

func testOptions() Options {
	// A fixed timestamp, because the assertions below are on bytes and a sink that read
	// the clock could not have any.
	return Options{
		Type:        "PDF Spec Clause",
		Generator:   "pdfspec/1.2.3",
		GeneratedAt: "2026-08-03T00:00:00Z",
		Unplaced:    true,
	}
}

func TestBundleShape(t *testing.T) {
	filters := sec("7.4", "7.4 Filters", sec("7.4.1", "7.4.1 General"))
	filters.Blocks = []doc.Block{para("This subclause describes the standard filters. They shall be applied in order.")}
	filters.Kids[0].Blocks = []doc.Block{para("A filter transforms a stream.")}
	filters.FirstPage, filters.LastPage = 40, 42

	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "ISO 32000-2:2020(en), Document management", Path: "corpus/iso.pdf"},
		Sections: []*doc.Section{filters},
	}

	files, st := Bundle(o, testOptions())
	if st.Concepts != 2 {
		t.Errorf("Concepts = %d, want 2", st.Concepts)
	}

	// A parent clause is a directory holding its own document plus its children's, and a
	// leaf is a file beside them. That shape is what makes the bundle navigable; a flat
	// directory of 981 files answers no question about which clause contains which.
	parent := find(t, files, "/7-4-filters/7-4-filters.md")
	find(t, files, "/7-4-filters/7-4-1-general.md")
	find(t, files, "/7-4-filters/index.md")
	find(t, files, "/index.md")
	find(t, files, "/log.md")

	for _, want := range []string{
		"type: PDF Spec Clause",
		"resource: iso32000-2:2020#7.4",
		"by: pdfspec/1.2.3",
		"status: draft",
		// Quoted, because a bare 7.4 is a YAML float and a bare 40 an integer, and this
		// field is neither. That is yamlString's rule doing its job through
		// markdown.YAMLString rather than a second copy of it here.
		`pdf_clause: "7.4"`,
		`pdf_page: "40"`,
		`pdf_page_last: "42"`,
		"# 7.4 Filters",
		"## Subclauses",
		"pages 40–42",
	} {
		if !strings.Contains(parent.Content, want) {
			t.Errorf("parent document missing %q:\n%s", want, parent.Content)
		}
	}

	// The description is the first sentence, not the first paragraph: a paragraph of
	// standards prose is not a summary.
	if !strings.Contains(parent.Content, "description: This subclause describes the standard filters.\n") {
		t.Errorf("description is not the first sentence:\n%s", parent.Content)
	}

	// No run of blank lines anywhere: the pieces of a document compose through join, and a
	// double gap is what a reader diffing a regenerated bundle would see instead of the
	// change they were looking for.
	if strings.Contains(parent.Content, "\n\n\n") {
		t.Errorf("parent document has a double blank line:\n%q", parent.Content)
	}
}

func TestBundleFrontmatterIsFirst(t *testing.T) {
	// Every non-reserved file must open with a frontmatter block carrying a type, which is
	// the one field OKF v0.2 requires. A file that fails this is non-conformant per §11 and
	// the whole point of the sink is lost.
	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{sec("1", "1 Scope"), sec("2", "2 Terms")},
	}
	o.Sections[0].Blocks = []doc.Block{para("Body.")}
	o.Sections[1].Blocks = []doc.Block{para("Body.")}

	files, _ := Bundle(o, testOptions())
	for _, f := range files {
		base := filepath.Base(f.Path)
		if base == "log.md" {
			// Reserved and carries no frontmatter per OKF §9.
			if strings.HasPrefix(f.Content, "---") {
				t.Errorf("%s has frontmatter; §9 reserves it without one", f.Path)
			}
			continue
		}
		if base == "index.md" {
			// Only the bundle root may carry frontmatter, and only okf_version (§8).
			if f.Path == "/index.md" {
				if !strings.HasPrefix(f.Content, "---\nokf_version: ") {
					t.Errorf("root index frontmatter is not just okf_version:\n%s", f.Content)
				}
			} else if strings.HasPrefix(f.Content, "---") {
				t.Errorf("%s has frontmatter; §8 reserves index.md without one", f.Path)
			}
			continue
		}
		if !strings.HasPrefix(f.Content, "---\ntype: ") {
			t.Errorf("%s does not open with a type field:\n%s", f.Path, f.Content)
		}
	}
}

func TestBundleTags(t *testing.T) {
	// Tags are ancestry, so a query about "Syntax" reaches 7.4.1 without that clause's own
	// title containing the word. The clause's own title is excluded: it is already the
	// title field.
	deep := sec("7.4.1", "7.4.1 General")
	deep.Blocks = []doc.Block{para("Text.")}
	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{sec("7", "7 Syntax", sec("7.4", "7.4 Filters", deep))},
	}

	files, _ := Bundle(o, testOptions())
	f := find(t, files, "/7-syntax/7-4-filters/7-4-1-general.md")
	if !strings.Contains(f.Content, "tags:\n  - 7 Syntax\n  - 7.4 Filters\n") {
		t.Errorf("tags are not the ancestry:\n%s", f.Content)
	}
	// A root clause has no ancestry and so no tags, rather than a one-element list
	// restating its own title.
	if root := find(t, files, "/7-syntax/7-syntax.md"); strings.Contains(root.Content, "tags:") {
		t.Errorf("root clause has tags:\n%s", root.Content)
	}
}

func TestBundleUnplaced(t *testing.T) {
	// Unattributed text is its own document, not an appendix to whichever clause happened
	// to precede it. On ISO 32000-2 this content is the whole of clause 1, and filing the
	// Scope of a standard under the wrong clause puts a confident falsehood into a bundle a
	// model reads as fact.
	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{sec("2", "2 Terms")},
		Unplaced: []doc.Page{{Number: 7, Blocks: []doc.Block{para("Orphan text.")}}},
	}
	o.Sections[0].Blocks = []doc.Block{para("Body.")}

	files, _ := Bundle(o, testOptions())
	f := find(t, files, "/unplaced/page-0007.md")
	if !strings.Contains(f.Content, "Orphan text.") {
		t.Errorf("unplaced text was not written:\n%s", f.Content)
	}
	if !strings.Contains(f.Content, "could not attribute") {
		t.Errorf("unplaced document does not say its placement is unknown:\n%s", f.Content)
	}
	find(t, files, "/unplaced/index.md")

	// Off means off: a caller who does not want it gets no file rather than an empty one.
	opt := testOptions()
	opt.Unplaced = false
	off, _ := Bundle(o, opt)
	for _, f := range off {
		if strings.HasPrefix(f.Path, "/unplaced") {
			t.Errorf("-unplaced=false still wrote %s", f.Path)
		}
	}
}

func TestBundleNoTextLost(t *testing.T) {
	// The accounting invariant, at the bundle level: every block the outline holds must
	// appear in some file. A sink that silently drops a clause is the failure mode this
	// whole pipeline exists to avoid, and it would look like success.
	body := sec("3", "3 Body")
	body.Blocks = []doc.Block{para("alpha"), para("bravo")}
	kid := sec("3.1", "3.1 Kid")
	kid.Blocks = []doc.Block{para("charlie")}
	body.Kids = []*doc.Section{kid}
	kid.Parent = body

	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{body},
		Preamble: []doc.Block{para("delta")},
		Unplaced: []doc.Page{{Number: 2, Blocks: []doc.Block{para("echo")}}},
	}

	opt := testOptions()
	opt.Preamble = true
	files, _ := Bundle(o, opt)

	var all strings.Builder
	for _, f := range files {
		all.WriteString(f.Content)
	}
	for _, want := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("bundle lost %q", want)
		}
	}
}

func TestBundleCollidingTitles(t *testing.T) {
	// Two siblings with the same slug must not overwrite each other, and two cousins with
	// the same slug must both keep their name: deduplication is per-directory, not global.
	a := sec("", "General")
	b := sec("", "General")
	a.Blocks = []doc.Block{para("first")}
	b.Blocks = []doc.Block{para("second")}
	sub := sec("", "Other", sec("", "General"))
	sub.Kids[0].Blocks = []doc.Block{para("cousin")}

	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{a, b, sub},
	}
	files, _ := Bundle(o, testOptions())

	seen := make(map[string]bool)
	for _, f := range files {
		if seen[f.Path] {
			t.Errorf("path collision: %s", f.Path)
		}
		seen[f.Path] = true
	}
	find(t, files, "/general.md")
	find(t, files, "/general-2.md")
	find(t, files, "/other/general.md")
}

func TestBundleLinksAreResolvable(t *testing.T) {
	// Every link the bundle writes must name a file the bundle contains. A broken link is
	// tolerated by OKF §11, which makes it exactly the kind of defect nothing else would
	// catch.
	a := sec("7.4", "7.4 Filters")
	a.Blocks = []doc.Block{para("Encryption is described in clause 7.6, and see 7.4.1.")}
	a.Kids = []*doc.Section{sec("7.4.1", "7.4.1 General")}
	a.Kids[0].Parent = a
	a.Kids[0].Blocks = []doc.Block{para("General text.")}
	b := sec("7.6", "7.6 Encryption")
	b.Blocks = []doc.Block{para("Encryption text.")}

	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{a, b},
	}
	files, st := Bundle(o, testOptions())
	if st.Links != 2 {
		t.Errorf("Links = %d, want 2 (clause 7.6 and see 7.4.1)", st.Links)
	}

	have := make(map[string]bool, len(files))
	for _, f := range files {
		have[f.Path] = true
	}
	for _, f := range files {
		for _, target := range linkTargets(f.Content) {
			if !have[target] {
				t.Errorf("%s links to %s, which the bundle does not contain", f.Path, target)
			}
		}
	}
}

// linkTargets pulls the destinations out of "](...)" occurrences.
func linkTargets(md string) []string {
	var out []string
	for i := 0; i+1 < len(md); i++ {
		if md[i] != ']' || md[i+1] != '(' {
			continue
		}
		j := strings.IndexByte(md[i+2:], ')')
		if j < 0 {
			continue
		}
		out = append(out, md[i+2:i+2+j])
		i += 2 + j
	}
	return out
}

func TestBundleDeterministic(t *testing.T) {
	// Two runs over one outline must produce identical bytes, which is what makes a
	// regenerated bundle diffable and is only true because the timestamp comes from the
	// caller.
	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{sec("1", "1 Scope", sec("1.1", "1.1 Sub"))},
	}
	o.Sections[0].Blocks = []doc.Block{para("Body.")}
	o.Sections[0].Kids[0].Blocks = []doc.Block{para("Sub body.")}

	first, _ := Bundle(o, testOptions())
	second, _ := Bundle(o, testOptions())
	if len(first) != len(second) {
		t.Fatalf("file counts differ: %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("file %d differs between runs:\n%q\n%q", i, first[i], second[i])
		}
	}
}

func TestWriteToDisk(t *testing.T) {
	o := &doc.Outline{
		Meta:     doc.Metadata{Title: "Doc", Path: "d.pdf"},
		Sections: []*doc.Section{sec("7.4", "7.4 Filters", sec("7.4.1", "7.4.1 General"))},
	}
	o.Sections[0].Blocks = []doc.Block{para("Body.")}
	o.Sections[0].Kids[0].Blocks = []doc.Block{para("Sub.")}

	dir := t.TempDir()
	st, err := Write(dir, o, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if st.Concepts != 2 {
		t.Errorf("Concepts = %d, want 2", st.Concepts)
	}

	// The bundle's paths are slash-separated by its own convention, and the files must land
	// in real directories on whatever platform this is.
	for _, rel := range []string{
		"index.md",
		"log.md",
		filepath.Join("7-4-filters", "7-4-filters.md"),
		filepath.Join("7-4-filters", "7-4-1-general.md"),
		filepath.Join("7-4-filters", "index.md"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
}

func TestBundleNilOutline(t *testing.T) {
	files, st := Bundle(nil, testOptions())
	if files != nil || st.Concepts != 0 {
		t.Errorf("nil outline produced %d files", len(files))
	}
}

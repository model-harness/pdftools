package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests run the md verb end to end — open, extract, render, write — and check
// that the docs/DESIGN.md §1 quality holds at the end of the pipeline rather than in
// the middle of it. extract_test.go asserts the metrics on extract.Document.Text();
// nothing there would notice a sink that dropped a block, joined two paragraphs, or
// escaped its way through the prose. The metric bars are repeated here for that
// reason, not duplicated by accident.

// mdOut runs the verb into a temp file and returns what it wrote.
func mdOut(t *testing.T, args ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := runMD(append([]string{"-o", out}, args...)); err != nil {
		t.Fatalf("runMD: %v", err)
	}
	b, err := os.ReadFile(out) // #nosec G304 -- path is the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// escapeRate is backslashes per thousand characters.
//
// It is the one measurement that catches over-escaping, and over-escaping is the
// failure this package is most likely to have: it renders correctly, passes every
// unit test built from a hand-written span, and turns a specification into prose full
// of backslashes. Nothing else in the suite would report it.
//
// Measured: 0.34 on the arXiv paper, 0.16 on ISO 32000-2 — roughly one backslash per
// 3,000 and 6,000 characters. The bar is 2, which is an order of magnitude below what
// a policy that escaped "<", "-", or "_" unconditionally would produce on a document
// made of PDF dictionaries and hyphenated compounds, and an order above what is
// measured.
func escapeRate(s string) float64 {
	if s == "" {
		return 0
	}
	return float64(strings.Count(s, `\`)) / float64(len(s)) * 1000
}

func TestMDPreservesExtractedText(t *testing.T) {
	path := corpusFile(t, "LightOnOCR-2601.14251v1.pdf")
	raw, _ := extractDoc(t, path)

	start := time.Now()
	md := mdOut(t, path)
	m := measure(md, time.Since(start))
	m.log(t, "arXiv markdown")

	rm := measure(raw, 0)
	t.Logf("escape rate %.2f per 1000 chars", escapeRate(md))

	// The sink adds blank lines between blocks and backslashes before live
	// metacharacters, and removes nothing. Non-space characters can therefore only
	// rise, and only by the escaping — so a floor at the extraction's own figure is
	// exact in one direction and the escape-rate bar below bounds the other.
	if m.nonSpace < rm.nonSpace {
		t.Errorf("non-space chars = %d, extraction had %d: the sink dropped text",
			m.nonSpace, rm.nonSpace)
	}
	// Words can rise slightly: a backslash-escaped character never splits a word, but
	// a block boundary that fell mid-line in the raw text becomes a paragraph break.
	// A fall means blocks were joined without a separator.
	if m.words < rm.words {
		t.Errorf("words = %d, extraction had %d: blocks were joined", m.words, rm.words)
	}
	// The §1 bars, restated at the end of the pipeline. Measured through md on this
	// file: 39,781 non-space characters, 6,343 words, 14.04% spaces, 21 words over 25
	// characters (0.33%), longest 71, 18ms. Against pdfplumber's 39,089 / 2,392 /
	// 15.8% / 110 on the same document — the character counts agree and the word
	// division does not, which is the §1 claim surviving the sink.
	if m.spaceRatio < 10 {
		t.Errorf("spaces = %.2f%%, want >= 10%%", m.spaceRatio)
	}
	if m.longFrac > 1.0 {
		t.Errorf("words >25ch = %.2f%%, want <= 1%% (pdfplumber: 15.8%%)", m.longFrac)
	}
	if m.longest > 130 {
		t.Errorf("longest word = %d, want <= 130", m.longest)
	}
	if r := escapeRate(md); r > 2 {
		t.Errorf("escape rate %.2f per 1000 chars, want <= 2 (measured 0.34): escaping is too broad", r)
	}
}

// The target document, which is what Phase 2 has to section. 1,023 pages of
// two-column standards prose, and the corpus's whole population of PDF dictionaries,
// clause numbers, and hex strings — every construct the escaping policy is narrow for.
func TestMDOnSpec(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	raw, _ := extractDoc(t, path)

	start := time.Now()
	md := mdOut(t, path)
	m := measure(md, time.Since(start))
	m.log(t, "spec markdown")

	rm := measure(raw, 0)
	t.Logf("escape rate %.2f per 1000 chars", escapeRate(md))

	if m.nonSpace < rm.nonSpace {
		t.Errorf("non-space chars = %d, extraction had %d: the sink dropped text",
			m.nonSpace, rm.nonSpace)
	}
	if m.words < rm.words {
		t.Errorf("words = %d, extraction had %d: blocks were joined", m.words, rm.words)
	}
	if m.longFrac > 0.5 {
		t.Errorf("words >25ch = %.2f%%, want <= 0.5%%", m.longFrac)
	}
	if r := escapeRate(md); r > 2 {
		t.Errorf("escape rate %.2f per 1000 chars, want <= 2 (measured 0.16)", r)
	}

	// A dictionary opener is never escaped. "<<" cannot begin an HTML tag, and the
	// spec contains thousands of them, so a policy that treated "<" as always live
	// would put a backslash in front of every one. An XMP fragment like
	// "<pdfd:conformsTo>" is a different case and is escaped on purpose, so this
	// checks the pair rather than the character.
	if !strings.Contains(md, "<<") {
		t.Error("no PDF dictionary in the output: the spec is full of them")
	}
	if strings.Contains(md, `\<<`) || strings.Contains(md, `<\<`) {
		t.Error("a PDF dictionary opener was escaped")
	}
}

// TestMDEmitsNoHeadingsYet records a Phase 1 limitation rather than a defect, and it
// is written as a test because the alternative is that nobody notices which of the two
// it is.
//
// extract.roleOf assigns doc.RoleParagraph to every non-artifact block on purpose:
// heading level is declared by the structure tree, and reading it is sectionize's job
// in Phase 2. So the Markdown of ISO 32000-2 today is flat prose — correct text, no
// document outline — even though tag.Read finds 981 headings in the same file
// (TestCorpusStructure). The sink renders headings correctly when it is given them;
// nothing gives it any yet.
//
// This test inverts when sectionize lands. A failure here means headings started being
// emitted, which is the Phase 2 goal, and the assertion below should then become the
// 981-heading bar from docs/DESIGN.md §Phases.
func TestMDEmitsNoHeadingsYet(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	md := mdOut(t, path)

	heads := 0
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			heads++
		}
	}
	if heads != 0 {
		t.Errorf("%d headings emitted: extract now classifies them, so this test and the "+
			"Phase 2 bar in docs/DESIGN.md should be reconciled", heads)
	}

	// The corollary is that a paragraph whose text happens to begin with "#" must be
	// escaped, or the flat output would sprout a heading the document does not have.
	// No line may start with an unescaped one.
	for i, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "#") {
			t.Errorf("line %d opens with an unescaped %q: %q", i+1, "#", first(line, 60))
			break
		}
	}
}

var pageFile = regexp.MustCompile(`^page-(\d+)\.md$`)

func TestMDSplitOnePerPage(t *testing.T) {
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	const pages = 57

	dir := filepath.Join(t.TempDir(), "pages")
	if err := runMD([]string{"-split", "-frontmatter", "-o", dir, path}); err != nil {
		t.Fatalf("runMD: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != pages {
		t.Fatalf("%d files, want %d", len(ents), pages)
	}

	// The names must sort in page order as strings, because that is the order a file
	// browser and a shell glob will present them in. Without zero padding page 10
	// sorts before page 2.
	names := make([]string, len(ents))
	for i, e := range ents {
		names[i] = e.Name()
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("ReadDir order is not sorted, which should be impossible: %v", names[:5])
	}
	for i, name := range names {
		mm := pageFile.FindStringSubmatch(name)
		if mm == nil {
			t.Fatalf("unexpected name %q", name)
		}
		n, err := strconv.Atoi(mm[1])
		if err != nil {
			t.Fatal(err)
		}
		if n != i+1 {
			t.Fatalf("lexical position %d holds page %d: names do not sort in page order", i+1, n)
		}
	}

	// Frontmatter has to say which page it is, or a directory of pages cannot be
	// checked against the original.
	b, err := os.ReadFile(filepath.Join(dir, "page-07.md")) // #nosec G304 -- test temp dir
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.HasPrefix(got, "---\n") {
		t.Errorf("no frontmatter: %q", first(got, 80))
	}
	if !strings.Contains(got, "page: 7\n") || !strings.Contains(got, "pages: 57\n") {
		t.Errorf("page identity missing: %q", first(got, 300))
	}
	// The source path is what makes a split page traceable, and the extractor cannot
	// know it — the command sets it.
	if !strings.Contains(got, "source:") {
		t.Errorf("source missing: %q", first(got, 300))
	}
}

// Splitting must not change the text. The pages concatenated are the whole document,
// and if they are not then one of the two paths is wrong.
func TestMDSplitTextMatchesWhole(t *testing.T) {
	path := corpusFile(t, "PDF20_AN002-AF.pdf")

	whole := mdOut(t, path)

	dir := filepath.Join(t.TempDir(), "pages")
	if err := runMD([]string{"-split", "-o", dir, path}); err != nil {
		t.Fatalf("runMD: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- test temp dir
		if err != nil {
			t.Fatal(err)
		}
		joined.Write(b)
	}

	// Compared on the non-space characters: the two differ in whitespace by
	// construction, because a page boundary that is a blank line in the whole document
	// is a file boundary in the split one.
	w, s := strip(whole), strip(joined.String())
	if w != s {
		t.Errorf("split text differs from whole: %d vs %d non-space chars", len(w), len(s))
		for i := 0; i < len(w) && i < len(s); i++ {
			if w[i] != s[i] {
				t.Errorf("first difference at %d: whole %q, split %q",
					i, first(w[i:], 60), first(s[i:], 60))
				break
			}
		}
	}
}

// A blank page still gets a file. A gap in the numbering reads as a conversion that
// failed on those pages; an empty file is the statement that the page had nothing on
// it.
func TestMDSplitWritesBlankPages(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	dir := filepath.Join(t.TempDir(), "pages")
	if err := runMD([]string{"-split", "-o", dir, path}); err != nil {
		t.Fatalf("runMD: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1023 {
		t.Errorf("%d files, want 1023: a page is missing", len(ents))
	}
	// Four digits, because 1023 has four. Three would sort page 1000 before page 2.
	if _, err := os.Stat(filepath.Join(dir, "page-0001.md")); err != nil {
		t.Errorf("page-0001.md: %v", err)
	}
}

func TestMDFrontmatterOffByDefault(t *testing.T) {
	path := corpusFile(t, "PDF20_AN001-BPC.pdf")
	if md := mdOut(t, path); strings.HasPrefix(md, "---\n") {
		t.Error("frontmatter emitted without the flag")
	}
	md := mdOut(t, "-frontmatter", path)
	if !strings.HasPrefix(md, "---\n") {
		t.Errorf("no frontmatter with the flag: %q", first(md, 80))
	}
	// The frontmatter must be loadable, and the corpus is where the values that break
	// a hand-written writer come from — a title with a colon, a producer with a
	// version number in it.
	block, _, ok := strings.Cut(strings.TrimPrefix(md, "---\n"), "---\n")
	if !ok {
		t.Fatal("frontmatter not closed")
	}
	for _, line := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
		key, val, ok := strings.Cut(line, ": ")
		if !ok {
			t.Errorf("not a key-value line: %q", line)
			continue
		}
		if strings.ContainsAny(key, " \t") {
			t.Errorf("key contains whitespace, so the value ran into it: %q", line)
		}
		if val == "" {
			t.Errorf("empty value emitted: %q", line)
		}
	}
}

// -split needs somewhere to put the files, and defaulting to the working directory
// would scatter a thousand of them into whatever the user happened to be in.
func TestMDSplitRequiresOutputDir(t *testing.T) {
	if err := runMD([]string{"-split", "x.pdf"}); err == nil {
		t.Error("no error for -split without -o")
	}
}

func TestMDTakesExactlyOneFile(t *testing.T) {
	if err := runMD(nil); err == nil {
		t.Error("no error for no input")
	}
	if err := runMD([]string{"a.pdf", "b.pdf"}); err == nil {
		t.Error("no error for two inputs")
	}
}

func TestMDMissingFile(t *testing.T) {
	if err := runMD([]string{filepath.Join(t.TempDir(), "nope.pdf")}); err == nil {
		t.Error("no error for a missing file")
	}
}

// strip removes whitespace, which is what makes two renderings of the same text
// comparable when only their block separation differs.
func strip(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

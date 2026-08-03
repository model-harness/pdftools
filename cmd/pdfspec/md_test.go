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
	"unicode"

	"github.com/3rg0n/pdf-spec/doc"
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

// No control byte reaches the output, across every file in the corpus.
//
// This is a whole-corpus assertion rather than a unit test because the unit tests are
// built from hand-written spans and would never contain one: the byte arrives from a
// /ToUnicode entry mapping a code to U+0000, which only a real file has. Three exist —
// all in PDF20_AN001-BPC.pdf — and before the substitution landed they were written
// raw into the Markdown and into every OKF bundle built from it.
//
// The consequence is not cosmetic. YAML rejects a raw control byte in a plain scalar,
// a NUL terminates a path in every C API the output later passes through, and a
// consumer that reads a bundle byte-for-byte gets a value it cannot round-trip. There
// is no correct escape for one either, since a code span renders "\x00" as those four
// characters — so replacement with U+FFFD, which is what CommonMark §2.3 requires of a
// parser reading U+0000, is the only option that keeps the output parseable and still
// tells a reader something was there.
func TestMDEmitsNoControlBytes(t *testing.T) {
	for _, name := range corpusFiles() {
		t.Run(name, func(t *testing.T) {
			md := mdOut(t, corpusFile(t, name))
			for i := 0; i < len(md); i++ {
				c := md[i]
				if c == '\n' || c == '\r' || c == '\t' || c >= 0x20 && c != 0x7f {
					continue
				}
				t.Fatalf("control byte 0x%02x at offset %d, in %q", c, i, context(md, i))
			}
		})
	}
}

// context is the text around an offset, for a failure message that names the sentence
// rather than the byte.
func context(s string, i int) string {
	lo, hi := max(0, i-40), min(len(s), i+40)
	return s[lo:hi]
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

// TestMDEmitsOutlineHeadings is the inverted form of a Phase 1 test that asserted the
// opposite. It recorded that ISO 32000-2 converted to flat prose — correct text, no
// document outline — because extract.roleOf marks every non-artifact block
// RoleParagraph on purpose and nothing gave the sink a heading. sectionize now does.
//
// The count is 981 sections but not 981 ATX headings, and the gap is Markdown's:
// docs/DESIGN.md §Phases puts the specification at ~981 clauses over five levels, and
// counting only "#" and "##" reaches the 193 roots and their immediate children. The
// bar below is on the total across all six depths, which is the number the outline
// actually carries.
func TestMDEmitsOutlineHeadings(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	md := mdOut(t, path)

	byLevel := map[int]int{}
	for _, line := range strings.Split(md, "\n") {
		n := 0
		for n < len(line) && line[n] == '#' {
			n++
		}
		if n == 0 || n >= len(line) || line[n] != ' ' {
			continue
		}
		byLevel[n]++
	}
	total := 0
	for _, n := range byLevel {
		total += n
	}
	// 981 clauses from docs/DESIGN.md §Phases, all of which resolved a title, so all of
	// which are emitted. Anything materially lower means the sink is dropping sections
	// or sectionize stopped finding them.
	if total != 981 {
		t.Errorf("%d headings emitted, want 981: %v", total, byLevel)
	}
	if byLevel[1] == 0 {
		t.Error("no level-1 headings: the outline has no roots")
	}
	// Markdown has six levels and the tree nests deeper, so headings flatten rather
	// than emitting "#######", which renders as literal hashes.
	if byLevel[7] != 0 {
		t.Errorf("%d headings past level 6", byLevel[7])
	}
	t.Logf("headings by level: %v (total %d)", byLevel, total)

	// A heading line is the only line allowed to open with "#". A paragraph whose text
	// happens to begin with one must still be escaped, or the output would sprout a
	// clause the document does not have.
	for i, line := range strings.Split(md, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		n := 0
		for n < len(line) && line[n] == '#' {
			n++
		}
		if n < len(line) && line[n] == ' ' {
			continue
		}
		t.Errorf("line %d opens with an unescaped %q outside a heading: %q",
			i+1, "#", first(line, 60))
		break
	}
}

// TestMDFlatEmitsNoHeadings covers the escape hatch: -flat is page-ordered prose even
// for a tagged file, which is what a reader converting one document to read it asked
// for and what a diff against the pre-outline output needs.
func TestMDFlatEmitsNoHeadings(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	md := mdOut(t, "-flat", path)

	for i, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "#") {
			t.Errorf("line %d opens with %q under -flat: %q", i+1, "#", first(line, 60))
			break
		}
	}
}

// TestMDOutlineConservesText is the pipeline-level form of the accounting invariant:
// every character the flat conversion drew must still be in the outline conversion.
//
// The outline reorders content relative to the page — unplaced text moves to the end,
// and a clause spanning pages is contiguous where the pages were not — so the two are
// compared as character multisets rather than as text.
//
// Letters and digits only, and one-directional. Markdown syntax differs between the
// two renderings by construction: the outline adds "#" per heading, "- " per list item
// recovered from an L element, "*" around a figure, and an HTML comment per unplaced
// page. Counting those would measure the sink's markup, which the other tests in this
// file already do. What no rendering difference can excuse is a letter the flat path
// drew and the outline did not, so that is what this asserts.
//
// The reverse direction is not zero and should not be: the tagged path recovers /Alt
// from structure elements, and /Alt is text a producer supplied for content the glyphs
// do not spell — a figure's description. Nothing draws it on the page, so the layout
// path cannot see it at all. Measured, letters and digits: 33 on PDF20_AN002-AF, 44 on
// WTPDF, 9,524 on ISO 32000-2, each equal to the outline's own /Alt total to the
// character. That equality is the assertion — an outline gaining letters from anywhere
// but /Alt would be inventing text.
func TestMDOutlineConservesText(t *testing.T) {
	for _, file := range []string{
		"PDF20_AN002-AF.pdf",
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
	} {
		t.Run(file, func(t *testing.T) {
			path := corpusFile(t, file)
			flat := alnumCounts(mdOut(t, "-flat", path))

			md := mdOut(t, path)
			// The sink's own notes are the one thing in the output that is neither the
			// document's text nor its markup, so they come off before counting. Matched
			// by their prefix rather than as any HTML comment: WTPDF prints XMP examples
			// that contain real comments, and stripping those would take 322 letters of
			// document content off one side of the comparison only.
			out := alnumCounts(sinkNote.ReplaceAllString(md, ""))

			missing := 0
			for r, n := range flat {
				if d := n - out[r]; d > 0 {
					missing += d
				}
			}
			if missing != 0 {
				t.Errorf("outline dropped %d letters and digits the flat conversion drew", missing)
			}

			gained := 0
			for r, n := range out {
				if d := n - flat[r]; d > 0 {
					gained += d
				}
			}
			_, o, _ := outlineOf(t, file)
			alt := 0
			forEachBlock(o, func(b doc.Block) {
				alt += alnumLen(b.Alt)
			})
			if gained != alt {
				t.Errorf("outline gained %d letters and digits but carries %d of /Alt: the difference is invented",
					gained, alt)
			}
			t.Logf("no letter lost, %d gained, all of it /Alt", gained)
		})
	}
}

// sinkNote matches the comment WriteOutline emits above unplaced text. Anchored on the
// "pdfspec:" prefix on purpose — see TestMDOutlineConservesText.
var sinkNote = regexp.MustCompile(`<!-- pdfspec:[^>]*-->`)

// forEachBlock visits every block an outline holds, wherever it sits.
func forEachBlock(o *doc.Outline, fn func(doc.Block)) {
	for _, b := range o.Preamble {
		fn(b)
	}
	o.Walk(func(s *doc.Section) bool {
		for _, b := range s.Blocks {
			fn(b)
		}
		return true
	})
	for i := range o.Unplaced {
		for _, b := range o.Unplaced[i].Blocks {
			fn(b)
		}
	}
}

// alnumCounts counts letters and digits, which is text in any script and never markup.
func alnumCounts(s string) map[rune]int {
	m := make(map[rune]int, 128)
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			m[r]++
		}
	}
	return m
}

func alnumLen(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
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
//
// Compared against -flat, because -split is page-scoped by definition and the outline
// is not: a clause running across three pages cannot be a heading in three files, so
// split output stays page-ordered and the whole-document comparison has to be too.
// Character conservation between flat and outline output is TestMDOutlineConservesText.
func TestMDSplitTextMatchesWhole(t *testing.T) {
	path := corpusFile(t, "PDF20_AN002-AF.pdf")

	whole := mdOut(t, "-flat", path)

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

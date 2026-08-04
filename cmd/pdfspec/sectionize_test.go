package main

import (
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/3rg0n/pdf-spec/doc"
	"github.com/3rg0n/pdf-spec/extract"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
	"github.com/3rg0n/pdf-spec/sectionize"
	"github.com/3rg0n/pdf-spec/tag"
)

// outlineOf runs the whole tagged pipeline over a corpus file, which is what these
// tests measure: an assertion about section counts is only meaningful against the real
// extractor and the real structure tree.
func outlineOf(t *testing.T, name string) (*doc.Document, *doc.Outline, sectionize.Stats) {
	t.Helper()
	path := corpusFile(t, name)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	d, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	tr, err := tag.Read(s)
	if err != nil || tr == nil {
		t.Fatalf("tag.Read: %v", err)
	}
	if err := tr.ResolvePages(s); err != nil {
		t.Fatalf("ResolvePages: %v", err)
	}
	out, st := sectionize.Tagged(d, tr, sectionize.DefaultOptions)
	return d, out, st
}

// TestSectionizeCorpus pins the reconstruction against the corpus.
//
// The counts are the acceptance bar from docs/DESIGN.md section 8 and they are
// load-bearing in one specific direction: a run that emits single digits from a
// 1,023-page standard has reverted to container-driven segmentation, which is a silent
// failure that produces a plausible-looking outline. ISO 32000-2 has 7 Sect elements
// against 981 headings, so the difference between the two algorithms is two orders of
// magnitude on the same file.
//
// Titles are required to resolve for every section, because a clause-per-file OKF sink
// cannot name a file for a section that has none, and because /T is empty on every
// heading in the corpus — so 100% titled is an assertion that the (page, MCID) join
// works, not that the producer was diligent.
func TestSectionizeCorpus(t *testing.T) {
	cases := []struct {
		file        string
		sections    int // exact: this is the measurement, and any movement is news
		minNumbered int
		maxLevel    int
		minBlocks   int
		maxUnplaced float64 // percent of document characters, see below
	}{
		{"Well-Tagged-PDF-WTPDF-1.0.pdf", 183, 173, 6, 900, 0.05},
		{"ISO_TS_32001-2022_sponsored_EC3.pdf", 14, 10, 3, 100, 0.01},
		{"ISO-TS-32005-2023-sponsored.pdf", 27, 23, 4, 650, 0.01},
		// 0.231% is not a defect in this package: ISO 32000-2 draws the whole of clause
		// 1 outside any marked-content sequence, so no structure element names it. The
		// bound is here to catch the join losing content, which is a different thing and
		// would show up as a much larger number.
		{"ISO_32000-2_sponsored_EC3.pdf", 981, 851, 5, 29000, 0.30},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			d, out, st := outlineOf(t, tc.file)

			if st.Sections != tc.sections {
				t.Errorf("sections = %d, want %d", st.Sections, tc.sections)
			}
			if st.Sections != out.Count() {
				t.Errorf("Stats.Sections = %d but Outline.Count = %d", st.Sections, out.Count())
			}
			// The failure mode DESIGN.md section 8 warns about, asserted directly rather
			// than left to be inferred from the exact count above.
			if st.Sections < 10 && tc.sections >= 10 {
				t.Errorf("only %d sections: container-driven segmentation has crept back in",
					st.Sections)
			}
			if st.Titled != st.Sections {
				t.Errorf("titled = %d of %d: the (page, MCID) join is not resolving titles",
					st.Titled, st.Sections)
			}
			if st.Numbered < tc.minNumbered {
				t.Errorf("numbered = %d, want >= %d", st.Numbered, tc.minNumbered)
			}
			if st.MaxLevel != tc.maxLevel {
				t.Errorf("max level = %d, want %d", st.MaxLevel, tc.maxLevel)
			}
			if st.Blocks < tc.minBlocks {
				t.Errorf("blocks = %d, want >= %d", st.Blocks, tc.minBlocks)
			}

			total := nonSpaceLen(documentText(d))
			pct := 100 * float64(st.UnplacedChars) / float64(total)
			if pct > tc.maxUnplaced {
				t.Errorf("unplaced = %d chars (%.3f%%), want <= %.3f%%",
					st.UnplacedChars, pct, tc.maxUnplaced)
			}
			t.Logf("sections=%d titled=%d numbered=%d maxlevel=%d blocks=%d roots=%d unplaced=%d (%.3f%%)",
				st.Sections, st.Titled, st.Numbered, st.MaxLevel, st.Blocks,
				len(out.Sections), st.UnplacedChars, pct)
		})
	}
}

// TestSectionizeLosesNoText is the accounting invariant, and the reason
// doc.Outline.Unplaced exists at all.
//
// Every character the extractor produced must be reachable from the outline — in a
// section, in the preamble, or in Unplaced. It measured 0.000% on all four files, and
// the two alternatives to keeping the unattributed remainder are both worse: dropping
// it loses a normative clause, and attaching it to the nearest preceding section files
// the Scope under "0.4 Changes introduced in ISO 32000-2:2020", which is a wrong
// attribution in a bundle a model will later read as fact.
//
// Character multisets rather than substrings, because the outline reorders content
// relative to the page and what matters is whether a character survived, not where it
// moved to.
func TestSectionizeLosesNoText(t *testing.T) {
	for _, file := range []string{
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_TS_32001-2022_sponsored_EC3.pdf",
		"ISO-TS-32005-2023-sponsored.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
	} {
		t.Run(file, func(t *testing.T) {
			d, out, _ := outlineOf(t, file)

			have := runeCounts(outlineText(out))
			var lost, total int
			for _, r := range documentText(d) {
				if unicode.IsSpace(r) {
					continue
				}
				total++
				if have[r] == 0 {
					lost++
					continue
				}
				have[r]--
			}
			pct := 100 * float64(lost) / float64(total)
			if lost != 0 {
				t.Errorf("lost %d of %d characters (%.3f%%)", lost, total, pct)
			}
			t.Logf("%d characters accounted for, %d lost (%.3f%%)", total, lost, pct)
		})
	}
}

// TestSectionTitlesAreHeadingSized guards the span-level join from the inside.
//
// A block-level join over-captures because the extractor's paragraph heuristic merges a
// heading line with the body line after it when they share style and spacing: it turned
// 12% of WTPDF's headings into heading-plus-definition, and the specification's worst
// case into a 518-character title. Titles staying heading-sized is the observable
// consequence of joining on doc.Span.MCID instead, so it is asserted rather than
// trusted.
func TestSectionTitlesAreHeadingSized(t *testing.T) {
	cases := []struct {
		file   string
		p90    int // bytes; measured p90 was 45 on the specification, 38 on WTPDF
		maxLen int
	}{
		{"Well-Tagged-PDF-WTPDF-1.0.pdf", 45, 90},
		// 148 is a real clause name — "7.6.4.4.8 Algorithm 9: Computing the encryption
		// dictionary's O (owner password) and OE (owner encryption) values (Security
		// handlers of revision 6)" — not an over-capture, so the bound is above it and
		// well below the 518 a block-level join produced.
		{"ISO_32000-2_sponsored_EC3.pdf", 50, 200},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			_, out, _ := outlineOf(t, tc.file)

			var lens []int
			var longest string
			out.Walk(func(s *doc.Section) bool {
				if s.Title == "" {
					t.Errorf("untitled section at level %d, pages %d-%d",
						s.Level, s.FirstPage, s.LastPage)
					return true
				}
				lens = append(lens, len(s.Title))
				if len(s.Title) > len(longest) {
					longest = s.Title
				}
				// A resolved title must be one line: it becomes a filename and a YAML
				// value, and neither survives an embedded newline or tab.
				if strings.ContainsAny(s.Title, "\n\r\t") {
					t.Errorf("title carries control whitespace: %q", s.Title)
				}
				if s.Title != strings.TrimSpace(s.Title) {
					t.Errorf("title not trimmed: %q", s.Title)
				}
				return true
			})
			if len(lens) == 0 {
				t.Fatal("no sections")
			}
			sort.Ints(lens)
			p90 := lens[len(lens)*9/10]
			if p90 > tc.p90 {
				t.Errorf("title length p90 = %d, want <= %d: the join is over-capturing",
					p90, tc.p90)
			}
			if len(longest) > tc.maxLen {
				t.Errorf("longest title %d bytes, want <= %d: %q",
					len(longest), tc.maxLen, longest)
			}
			t.Logf("titles: n=%d p50=%d p90=%d max=%d %q",
				len(lens), lens[len(lens)/2], p90, len(longest), longest)
		})
	}
}

// TestSectionHierarchyIsWellFormed checks the level stack produced a real tree, since
// the hierarchy is computed rather than read and a stack bug would show up as a
// plausible but wrong nesting.
func TestSectionHierarchyIsWellFormed(t *testing.T) {
	_, out, st := outlineOf(t, "ISO_32000-2_sponsored_EC3.pdf")

	var check func(*doc.Section)
	check = func(s *doc.Section) {
		for _, k := range s.Kids {
			// A child strictly deeper than its parent is the invariant the stack
			// maintains; equal levels nesting would mean a heading failed to close its
			// predecessor.
			if k.Level <= s.Level {
				t.Errorf("%q (level %d) nested under %q (level %d)",
					k.Title, k.Level, s.Title, s.Level)
			}
			if k.Parent != s {
				t.Errorf("%q has the wrong parent back-pointer", k.Title)
			}
			check(k)
		}
	}
	for _, s := range out.Sections {
		if s.Parent != nil {
			t.Errorf("root %q has a parent", s.Title)
		}
		check(s)
	}

	// Path is what OKF tags and a breadcrumb are built from, so it must reach the root
	// and end at the section itself.
	deepest := (*doc.Section)(nil)
	out.Walk(func(s *doc.Section) bool {
		if deepest == nil || s.Level > deepest.Level {
			deepest = s
		}
		return true
	})
	path := deepest.Path()
	if len(path) == 0 || path[len(path)-1] != deepest.Title {
		t.Errorf("Path() = %v, does not end at %q", path, deepest.Title)
	}
	t.Logf("max level %d, deepest path: %v", st.MaxLevel, path)
}

// TestSectionPagesAreOrdered checks the page ranges a reader uses to verify a
// conversion against the original.
func TestSectionPagesAreOrdered(t *testing.T) {
	d, out, _ := outlineOf(t, "ISO_32000-2_sponsored_EC3.pdf")
	pages := len(d.Pages)

	anchored, spanning := 0, 0
	out.Walk(func(s *doc.Section) bool {
		if s.FirstPage == 0 {
			return true
		}
		anchored++
		if s.LastPage < s.FirstPage {
			t.Errorf("%q: pages %d-%d are inverted", s.Title, s.FirstPage, s.LastPage)
		}
		if s.LastPage > pages {
			t.Errorf("%q: last page %d beyond the document's %d", s.Title, s.LastPage, pages)
		}
		if s.LastPage > s.FirstPage {
			spanning++
		}
		return true
	})
	if anchored == 0 {
		t.Fatal("no section anchored to a page")
	}
	// Cross-page clauses are the whole reason the tagged path exists. If none spanned,
	// the tree would be a per-page artifact.
	if spanning == 0 {
		t.Error("no section spans more than one page")
	}
	t.Logf("%d sections anchored, %d span more than one page", anchored, spanning)
}

// TestOutlineConservesCharacters is the other half of the accounting, and the one that
// catches the recovery pass re-emitting text a section already holds: the outline must
// contain exactly as many characters as the document, not merely at least as many.
//
// Blocks reaching Unplaced are rebuilt from their unclaimed spans rather than taken
// whole for precisely this reason — a partly-claimed block taken whole would repeat the
// half a section holds, and a duplicated normative sentence in a bundle a model reads as
// fact is worse than an unattributed one. Measured exact on all four files.
//
// An exact sum rather than a text comparison, because the specification genuinely says
// some sentences twice: page 1020 is the errata annex, which quotes clause 7.3.10
// verbatim under "Issue #379 — Change the first bulleted list of subclause 7.3.10 as
// follows". Any assertion phrased as "no unplaced block repeats a placed one" flags that
// quotation, which is a property of the document and not of this join.
func TestOutlineConservesCharacters(t *testing.T) {
	for _, file := range []string{
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_TS_32001-2022_sponsored_EC3.pdf",
		"ISO-TS-32005-2023-sponsored.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
	} {
		t.Run(file, func(t *testing.T) {
			d, out, st := outlineOf(t, file)

			// Titles and block spans only. Alt is excluded because /Alt text is not
			// drawn on the page, so counting it would exceed the document's own total
			// for a reason that has nothing to do with duplication.
			placed := 0
			for _, b := range out.Preamble {
				placed += nonSpaceLen(b.Text())
			}
			out.Walk(func(s *doc.Section) bool {
				placed += nonSpaceLen(s.Title)
				for _, b := range s.Blocks {
					placed += nonSpaceLen(b.Text())
				}
				return true
			})
			unplaced := 0
			for i := range out.Unplaced {
				unplaced += nonSpaceLen(out.Unplaced[i].Text())
			}

			total := nonSpaceLen(documentText(d))
			if placed+unplaced != total {
				t.Errorf("placed %d + unplaced %d = %d, want exactly %d (%+d)",
					placed, unplaced, placed+unplaced, total, placed+unplaced-total)
			}
			t.Logf("placed=%d unplaced=%d total=%d (%d unplaced blocks across %d pages)",
				placed, unplaced, total, st.UnplacedBlocks, len(out.Unplaced))
		})
	}
}

// TestUntaggedFileYieldsNoSections confirms the tagged path declines rather than
// guessing. The layout path produces headings for these files; sectionize.Tagged
// returning the document as preamble is the honest result, not an error.
func TestUntaggedFileYieldsNoSections(t *testing.T) {
	path := paperFile(t)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	d, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	tr, err := tag.Read(s)
	if err != nil {
		t.Fatalf("tag.Read: %v", err)
	}
	if tr != nil {
		t.Fatal("expected no structure tree")
	}

	out, st := sectionize.Tagged(d, tr, sectionize.DefaultOptions)
	if st.Sections != 0 {
		t.Errorf("sections = %d, want 0 from an untagged file", st.Sections)
	}
	if len(out.Preamble) == 0 {
		t.Error("untagged document produced no preamble: its text was dropped")
	}
	// And no text is lost on this path either.
	have := runeCounts(outlineText(out))
	lost := 0
	for _, r := range documentText(d) {
		if unicode.IsSpace(r) {
			continue
		}
		if have[r] == 0 {
			lost++
			continue
		}
		have[r]--
	}
	if lost != 0 {
		t.Errorf("lost %d characters on the untagged path", lost)
	}
	t.Logf("%d preamble blocks, no sections, no loss", len(out.Preamble))
}

// --- helpers

func documentText(d *doc.Document) string {
	var sb strings.Builder
	for i := range d.Pages {
		sb.WriteString(d.Pages[i].Text())
	}
	return sb.String()
}

// outlineText is everything reachable from an outline: titles, section bodies, the
// preamble, and the unplaced remainder.
func outlineText(o *doc.Outline) string {
	var sb strings.Builder
	for _, b := range o.Preamble {
		sb.WriteString(b.Text())
	}
	o.Walk(func(s *doc.Section) bool {
		sb.WriteString(s.Title)
		sb.WriteString(s.Text())
		for _, b := range s.Blocks {
			sb.WriteString(b.Alt)
		}
		return true
	})
	for i := range o.Unplaced {
		sb.WriteString(o.Unplaced[i].Text())
	}
	return sb.String()
}

func runeCounts(s string) map[rune]int {
	m := make(map[rune]int, len(s)/4)
	for _, r := range s {
		m[r]++
	}
	return m
}

func nonSpaceLen(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

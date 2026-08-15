package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/sectionize"
	"github.com/model-harness/pdftools/tag"
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
		// 840 was 900 until a Code element absorbed its own listing lines. The drop is 938
		// to 850, and it reconciles exactly: 11 Code elements hold 99 P between them, each
		// of which used to be a paragraph of its own beside an empty Code block that
		// IsEmpty then discarded, and the 11 are now blocks holding all 99 lines.
		// 99 - 11 = 88. A merge rather than a loss, and what proves that is
		// TestSectionizeLosesNoText and TestOutlineConservesCharacters holding across the
		// change, not this floor — a block count cannot tell the two apart.
		{"Well-Tagged-PDF-WTPDF-1.0.pdf", 183, 173, 6, 840, 0.05},
		{"ISO_TS_32001-2022_sponsored_EC3.pdf", 14, 10, 3, 100, 0.01},
		{"ISO-TS-32005-2023-sponsored.pdf", 27, 23, 4, 650, 0.01},
		// 0.231% is not a defect in this package: ISO 32000-2 draws the whole of clause
		// 1 outside any marked-content sequence, so no structure element names it. The
		// bound is here to catch the join losing content, which is a different thing and
		// would show up as a much larger number.
		//
		// 27500 was 29000 until a paragraph inside a table cell became transparent. The
		// drop is 29218 to 27517, and it is a merge rather than a loss: 1721 of this
		// file's cells hold a second or later P, each of which used to be a block of its
		// own beside an empty cell and is now part of the cell's own text. The 20 that
		// do not reconcile are cells whose extra paragraphs were empty and were dropped
		// by IsEmpty either way. What proves nothing was lost is not this floor but
		// TestSectionizeLosesNoText and TestOutlineConservesCharacters, which both hold
		// across the change — a block count cannot tell a merge from a deletion.
		{"ISO_32000-2_sponsored_EC3.pdf", 981, 851, 5, 27500, 0.30},
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
// section, in the preamble, or in Unplaced — unless the producer declared something else
// in its place. The two alternatives to keeping the unattributed remainder are both worse:
// dropping it loses a normative clause, and attaching it to the nearest preceding section
// files the Scope under "0.4 Changes introduced in ISO 32000-2:2020", which is a wrong
// attribution in a bundle a model will later read as fact.
//
// The exception is /ActualText, which is a deliberate loss: §14.9.4 makes the declared
// value stand in for the glyphs, so a substituted character does not arrive and must not.
// It is bounded per rune and exactly, in the table below, rather than by loosening the
// percentage — 102 characters of slack would hide a real loss of the same size, and a
// count that is exact in both directions also fails if a substitution stops happening.
//
// Character multisets rather than substrings, because the outline reorders content
// relative to the page and what matters is whether a character survived, not where it
// moved to.
//
// A list item's marker is one of those characters and it is no longer in the block's
// text: doc.Block.Marker holds it, because a marker kept in the text is re-derived by
// every sink and doubled by the one that writes its own "- ". So outlineText reads the
// field alongside the spans. That is conservation in the sense this test means it —
// every character the extractor drew is reachable from the outline — and it is a
// stronger statement than counting the text alone, since a marker both stripped from
// the text *and* left unrecorded would fail here rather than pass quietly.
func TestSectionizeLosesNoText(t *testing.T) {
	for _, tc := range []struct {
		file string
		// replaced is the multiset of drawn characters a declared /ActualText stands in
		// for, which is the one way a character may legitimately not reach the outline.
		// Per-rune and exact rather than a percentage bound, so a *different* character
		// going missing fails here even where a substitution already accounts for some.
		replaced map[rune]int
	}{
		// 92 Lbl spans declare " • " over a drawn U+25A0 BLACK SQUARE. The bullet is not
		// counted as arriving, because both glyphs are list markers and the sink strips
		// whichever it gets — so the square leaves and nothing visible takes its place.
		{"Well-Tagged-PDF-WTPDF-1.0.pdf", map[rune]int{'■': 92}},
		{"ISO_TS_32001-2022_sponsored_EC3.pdf", nil},
		// 10 of the corpus's 16 declared soft hyphens are in this file: each replaces a
		// drawn "-" with U+00AD, which is discretionary, so the word joins.
		{"ISO-TS-32005-2023-sponsored.pdf", map[rune]int{'-': 10}},
		{"ISO_32000-2_sponsored_EC3.pdf", nil},
	} {
		t.Run(tc.file, func(t *testing.T) {
			d, out, _ := outlineOf(t, tc.file)

			have := runeCounts(outlineText(out))
			lost := map[rune]int{}
			var n, total int
			for _, r := range documentText(d) {
				if unicode.IsSpace(r) {
					continue
				}
				total++
				if have[r] == 0 {
					lost[r]++
					n++
					continue
				}
				have[r]--
			}
			pct := 100 * float64(n) / float64(total)
			if !sameCounts(lost, tc.replaced) {
				t.Errorf("lost %v (%d of %d, %.3f%%), want exactly the declared replacements %v",
					codePoints(lost), n, total, pct, codePoints(tc.replaced))
			}
			t.Logf("%d characters accounted for, %d replaced by a declared /ActualText (%.3f%%)",
				total, n, pct)
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
//
// Markers count once, in the field. Being an exact sum rather than a floor, this is where
// the doubled marker that Block.Marker exists to stop would be caught from the other
// side: an item whose glyph stayed in its text *and* was recorded as its marker would
// come out one character over the document's own total, per item, and reading the marker
// nowhere would come out under it. Measured exact on all four files, over 1,350 markers.
//
// A declared /ActualText shifts the sum by whatever it replaces the glyphs with, so the
// expected difference is stated per file rather than the equality being relaxed. Its sign
// is the point: WTPDF's 92 declarations put one bullet where one square was drawn and so
// move the sum by nothing, while 32005's 10 declared soft hyphens are discretionary and
// drop, one character each. A substitution that quietly started *adding* characters would
// fail here even though it loses nothing, which the one-directional check in
// TestSectionizeLosesNoText cannot see.
func TestOutlineConservesCharacters(t *testing.T) {
	for _, tc := range []struct {
		file string
		// declared is placed+unplaced minus the document's own count: the net effect of
		// every /ActualText substitution in the file, in characters.
		declared int
	}{
		{"Well-Tagged-PDF-WTPDF-1.0.pdf", 0},
		{"ISO_TS_32001-2022_sponsored_EC3.pdf", 0},
		{"ISO-TS-32005-2023-sponsored.pdf", -10},
		{"ISO_32000-2_sponsored_EC3.pdf", 0},
	} {
		t.Run(tc.file, func(t *testing.T) {
			d, out, st := outlineOf(t, tc.file)

			// Titles, block spans, and markers. Alt is excluded because /Alt text is not
			// drawn on the page, so counting it would exceed the document's own total
			// for a reason that has nothing to do with duplication. A marker is the
			// opposite case: the page does draw it, so it is counted, once, wherever the
			// model now holds it.
			placed := 0
			for _, b := range out.Preamble {
				placed += nonSpaceLen(b.Marker) + nonSpaceLen(b.Text())
			}
			out.Walk(func(s *doc.Section) bool {
				placed += nonSpaceLen(s.Title)
				for _, b := range s.Blocks {
					placed += nonSpaceLen(b.Marker) + nonSpaceLen(b.Text())
				}
				return true
			})
			unplaced := 0
			for i := range out.Unplaced {
				for _, b := range out.Unplaced[i].Blocks {
					unplaced += nonSpaceLen(b.Marker)
				}
				unplaced += nonSpaceLen(out.Unplaced[i].Text())
			}

			total := nonSpaceLen(documentText(d))
			if placed+unplaced != total+tc.declared {
				t.Errorf("placed %d + unplaced %d = %d, want exactly %d (%+d, want %+d from declared /ActualText)",
					placed, unplaced, placed+unplaced, total+tc.declared,
					placed+unplaced-total, tc.declared)
			}
			t.Logf("placed=%d unplaced=%d total=%d (%d unplaced blocks across %d pages)",
				placed, unplaced, total, st.UnplacedBlocks, len(out.Unplaced))
		})
	}
}

// TestListingIndentsReachTheirListing pins the population the leading-indent rule runs on,
// and the band that separates it from the untagged whitespace that is not an indent.
//
// A corpus test because the rule's unit tests are fixtures and the figures in its comment are
// claims about these two files: change either one and the fixtures still pass. It is also the
// test the character-conservation check above cannot be, since that one counts non-space
// characters and so is blind to whitespace by construction — which is why 23 dropped runs
// survived it.
//
// The two counts move for different reasons. 23 is how many indent runs the producers draw
// outside marked content; 43 is how many other untagged whitespace runs the corpus holds, and
// a rule that started claiming one of those would move the second number without the first.
// The band between them is the evidence that no threshold is being tuned: measured, every
// indent meets its text within 0.243pt and every other run with a same-line successor stands at
// least 2.000pt clear, so the threshold — SpaceFrac of half the type size, 1.37pt at the 9.12pt
// the listing is set in — sits in an empty 8.2× gap.
//
// The indented-line count is what watches the rule rather than the data. indentGap below is a
// deliberate copy of leadingIndent's conditions, so on its own this test measures the population
// and cannot fail when the rule that reads it changes: three mutants of the attachment threshold
// — LineFrac for SpaceFrac, WideSpaceFrac for SpaceFrac, and the em in place of the space advance
// — survived it while each adopted 8 further runs standing 2.184pt off their text. Counting the
// output lines that begin with whitespace closes that, because an adopted run is a line's indent
// and a rejected one is at most a single inferred space.
//
// 3944 across the corpus, and most of it is not this rule's: ISO 32000-2 alone contributes 3396
// from table cells and declared listings, and only PDF-Declarations' 23 come from here. That is
// the point of asserting the total rather than the delta — the figure is sensitive to this rule in
// both directions while also pinning that the rule did not disturb the other 3921.
func TestListingIndentsReachTheirListing(t *testing.T) {
	// Every figure below is a count over the whole corpus, so an absent corpus does not make
	// them 0 — it makes them unmeasured, and asserting 23 against a clone that legitimately has
	// none of the sponsored PDFs would fail a test that had nothing to run. corpusFiles says as
	// much in its own comment; this is the guard that keeps the promise.
	files := corpusFiles()
	if len(files) == 0 {
		t.Skip("corpus absent")
	}

	indents, others, lines := 0, 0, 0
	var attached, detached []float64
	for _, name := range files {
		d, out, _ := outlineOf(t, name)
		for pi := range d.Pages {
			p := &d.Pages[pi]
			for bi := range p.Blocks {
				b := &p.Blocks[bi]
				for si := range b.Spans {
					sp := &b.Spans[si]
					if sp.MCID >= 0 || sp.Text == "" || strings.TrimSpace(sp.Text) != "" {
						continue
					}
					gap, ok := indentGap(b, si)
					if ok {
						indents++
						attached = append(attached, gap)
					} else {
						others++
						if gap >= 0 {
							detached = append(detached, gap)
						}
					}
				}
			}
		}
		// Every recovered run reaches a block, so no listing is left flush-left and no
		// whitespace-only block appears in unplaced text.
		for i := range out.Unplaced {
			for _, b := range out.Unplaced[i].Blocks {
				if strings.TrimSpace(b.Text()) == "" {
					t.Errorf("%s: unplaced whitespace-only block %q", name, b.Text())
				}
			}
			lines += indentedLines(out.Unplaced[i].Blocks)
		}
		lines += indentedLines(out.Preamble)
		for _, s := range out.Sections {
			lines += indentedLinesIn(s)
		}
	}
	if indents != 23 || others != 43 {
		t.Errorf("untagged whitespace runs: %d indents, %d others; want 23 and 43", indents, others)
	}
	if lines != 3944 {
		t.Errorf("indented output lines = %d, want 3944", lines)
	}
	sort.Float64s(attached)
	sort.Float64s(detached)
	if len(attached) == 0 || len(detached) == 0 {
		t.Fatalf("no band to measure: %d attached, %d detached", len(attached), len(detached))
	}
	hi, lo := attached[len(attached)-1], detached[0]
	if hi > 0.25 || lo < 2.0 {
		t.Errorf("band = (%.3f, %.3f), want an empty gap from under 0.25 to over 2.0", hi, lo)
	}
	t.Logf("indents=%d others=%d  widest attached=%.3f narrowest detached=%.3f (%.1fx)  indented lines=%d",
		indents, others, hi, lo, lo/hi, lines)
}

// indentedLines counts the blocks' lines that begin with whitespace, which is what an adopted
// indent produces and a rejected run does not: the gap a rejection leaves is filled by at most one
// inferred space, and never at the front of a line.
func indentedLines(bs []doc.Block) int {
	n := 0
	for i := range bs {
		for _, ln := range strings.Split(bs[i].Text(), "\n") {
			if strings.TrimSpace(ln) != "" && (ln[0] == ' ' || ln[0] == '\t') {
				n++
			}
		}
	}
	return n
}

// indentedLinesIn counts a section's indented lines and those of every subsection. Recursive
// because sections nest and the listing that motivated this rule is three levels down: a walk over
// out.Sections alone reported 0 indented lines in PDF-Declarations while its .md held 22.
func indentedLinesIn(s *doc.Section) int {
	n := indentedLines(s.Blocks)
	for _, k := range s.Kids {
		n += indentedLinesIn(k)
	}
	return n
}

// indentGap reports the distance from the whitespace run at i to the tagged span it precedes,
// and whether that run is the leading indent of a line. Negative when there is no same-line
// tagged span after it at all, so the caller can tell "stood too far off" from "had nothing to
// attach to". A copy of sectionize.leadingIndent's conditions rather than a call, since the
// point is to measure the population from outside the package that decides it.
func indentGap(b *doc.Block, i int) (float64, bool) {
	sp := &b.Spans[i]
	line := func(x, y *doc.Span) bool {
		return absf(x.Box.Y0-y.Box.Y0) <= 0.5*maxf(x.Style.Size, y.Style.Size)
	}
	if i+1 >= len(b.Spans) {
		return -1, false
	}
	nx := &b.Spans[i+1]
	if nx.MCID < 0 || !line(sp, nx) {
		return -1, false
	}
	gap := absf(nx.Box.X0 - sp.Box.X1)
	for k := 0; k < i; k++ {
		if line(&b.Spans[k], sp) {
			return gap, false
		}
	}
	return gap, gap <= 0.30*0.5*nx.Style.Size
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
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
// preamble, the unplaced remainder, and every block's marker.
//
// Markers are included because Block.Text() no longer holds them, and a conservation
// test that read only the text would report a stripped marker as a lost character. The
// marker is written beside the block rather than in place of it, since what is being
// counted is a multiset — where a character sits in this string is not part of the
// claim.
func outlineText(o *doc.Outline) string {
	var sb strings.Builder
	for _, b := range o.Preamble {
		sb.WriteString(b.Marker)
		sb.WriteString(b.Text())
	}
	o.Walk(func(s *doc.Section) bool {
		sb.WriteString(s.Title)
		sb.WriteString(s.Text())
		for _, b := range s.Blocks {
			sb.WriteString(b.Marker)
			sb.WriteString(b.Alt)
		}
		return true
	})
	for i := range o.Unplaced {
		for _, b := range o.Unplaced[i].Blocks {
			sb.WriteString(b.Marker)
		}
		sb.WriteString(o.Unplaced[i].Text())
	}
	return sb.String()
}

// sameCounts reports whether two rune multisets are equal, treating a zero count as
// absent so a nil map and an all-zero map agree.
func sameCounts(a, b map[rune]int) bool {
	for r, n := range a {
		if b[r] != n {
			return false
		}
	}
	for r, n := range b {
		if a[r] != n {
			return false
		}
	}
	return true
}

// codePoints renders a rune multiset by codepoint, in rune order. By codepoint because
// the characters this reports on are exactly the ones a terminal is least able to show:
// a soft hyphen is invisible and a black square is a box either way.
func codePoints(m map[rune]int) string {
	rs := make([]rune, 0, len(m))
	for r, n := range m {
		if n != 0 {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	var sb strings.Builder
	sb.WriteByte('{')
	for i, r := range rs {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "U+%04X: %d", r, m[r])
	}
	sb.WriteByte('}')
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

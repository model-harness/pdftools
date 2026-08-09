package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fidelity tests: our Markdown against a hand-written expectation of what each
// reference PDF says, rather than against our own previous output.
//
// Every check before these compared the pipeline to itself. Counts reconciled,
// bundles round-tripped, escape rates held steady — all of which detects drift and
// none of which detects being wrong from the start. Asked directly whether the
// Markdown matched what was in the PDF, the honest answer was that nothing had ever
// compared the two.
//
// The gold files in testdata/reference are written from the .tex source's intent,
// never from any reader's output — see that directory's README.md for why an
// Acrobat export would have been the wrong yardstick. The chain from intent to
// assertion does not pass through a PDF reader, including ours.
//
// Two tiers, because a single exact-match test over every fixture would have to be
// either aspirational and permanently red, or weakened until it asserts nothing:
//
//   - TestReferenceFidelity is the tier that must always pass. Every word of the
//     gold file's prose is present, in order, and nothing is invented. That is the
//     property a converter exists to have, and it is checkable today.
//   - TestReferenceExactMatch is the aspiration, and it is allowed to report
//     failures without failing the suite. What it prints is the roadmap: the gaps
//     between what we emit and what the document means. A gap that closes here
//     should be promoted into the tier above.
//
// Splitting them this way is the only honest arrangement. Deleting the exact-match
// tier would hide known defects; making it fail the build would gate every commit on
// a documented debt — the paragraph split inside a run of one-line paragraphs at one
// size, DESIGN.md §10, which is the last fixture still logging rather than asserting.

// referenceFixtures are the single-concern documents and what each one is for.
//
// Each PDF is generated from the .tex beside it and licensed MIT with the rest of
// the repo, so unlike the sponsored specifications in docs/ these are committed and
// every clone can run these tests.
var referenceFixtures = []struct {
	name string
	why  string
}{
	{"headings", "heading sequence at three depths, in order"},
	{"text-styles", "bold, italic, bold-italic, and monospace mid-sentence"},
	{"paragraphs", "paragraph breaks carried by the indent alone, at one size and one leading"},
	{"lists", "bulleted items including a nested level"},
	{"table", "every cell's text, rows kept together"},
	{"image", "a page with one image: no prose is the honest answer"},
	{"clauses", "numbered clause hierarchy from the structure tree"},
	{"tagged-lists", "list markers the structure tree declares, bulleted and numbered"},
	{"tagged-table", "a table grid the structure tree declares, with a header row"},
}

// TestReferenceFidelity is the assertion that must hold: the words the document
// says are the words we emit, in that order, and we add none of our own.
//
// Compared as a word sequence rather than byte-for-byte because the two differ on
// things that are formatting rather than fidelity — where a block break falls,
// whether a heading carries its "#" yet, how a table's cells are delimited. Those
// belong to the exact-match tier. Losing "Cell B2" or inventing a sentence does
// not, and this catches it.
//
// Emphasis markers are stripped before comparing for the same reason: this tier is
// about which words survive, and the styling that wraps them is asserted below.
func TestReferenceFidelity(t *testing.T) {
	for _, f := range referenceFixtures {
		t.Run(f.name, func(t *testing.T) {
			pdf, gold := fixturePaths(t, f.name)
			got := words(mdOut(t, pdf))
			want := words(gold)

			// Order matters as much as presence: a reader that returns every word of a
			// three-column table in column order has lost the table without losing a
			// word, and a set comparison would call that a pass.
			if i := firstDiff(got, want); i >= 0 {
				t.Errorf("word %d differs (%s)\n got: %s\nwant: %s",
					i, f.why, window(got, i), window(want, i))
			}
			if len(got) != len(want) {
				t.Errorf("word count = %d, want %d (%s): %s",
					len(got), len(want), f.why, countHint(got, want))
			}
		})
	}
}

// TestReferenceExactMatch reports the distance between what we emit and what the
// gold file says, without failing the suite.
//
// It logs rather than errors on purpose. What it finds is a documented debt — the
// unsplittable run of one-line paragraphs, DESIGN.md §10 — and gating commits on a
// debt we chose to defer would mean either paying it now or deleting the test that
// remembers it.
// Neither is what this is for: its output is a worklist, and its value is that the
// worklist is measured rather than remembered.
//
// Run with -v to read it. When a fixture starts matching exactly, move its name
// into exactFixtures below so a regression is caught rather than re-logged.
func TestReferenceExactMatch(t *testing.T) {
	// Fixtures that match their gold file exactly, and must keep doing so. A name
	// arrives here the first time the log below says it can — see that log for what
	// stands in the way of the others.
	exactFixtures := map[string]bool{
		// A page holding one image and one caption: no headings to level, no styling
		// to mark up, no cells to lay out. It matching first is the point — the plain
		// case is exact today, so every other fixture's gap is a specific missing
		// feature rather than a general inability to read a page.
		"image": true,

		// The tagged path, exact: five clauses at three depths, each with its body,
		// nothing unplaced. That is the path every document in the corpus takes, and
		// this is the first independent confirmation that it reads one correctly —
		// every earlier measurement compared it to its own output.
		//
		// Enforced rather than logged because the one remaining gap is in the untagged
		// layout path (table grid) — heading rank, paragraph breaks and the list role
		// have closed since. Nothing this fixture asserts is waiting on a deferred
		// debt, so a change that breaks it is a regression and not a known shortfall.
		"clauses": true,

		// The untagged path's heading rank, which this fixture was written to measure
		// and for which it logged "**1 First Section**" against "# 1 First Section"
		// until layout.Headings existed. Three depths, two of them distinguished from
		// the body by size and the third only by weight.
		//
		// Worth enforcing beyond the levels themselves: it also pins the two rules
		// that make them possible. Ranking by the document's own section numbering
		// rather than by position in the size ladder — the level-3 heading is *body
		// size*, so a ladder has no rung for it — and dropping the "**" that a
		// heading's own promoting emphasis would otherwise restate.
		"headings": true,

		// The paragraph break with no vertical evidence, which this fixture was written
		// to make measurable and which it logged as three paragraphs fused into one
		// until the indent rule existed. Three multi-line paragraphs, one size, one
		// leading, LaTeX's default \parskip of zero.
		//
		// Enforced because the fixture pins the measurement the rule rests on rather
		// than the rule's output: the vertical step here is 1.200 line heights whether
		// a pair wraps or breaks, so anything that makes this pass by reading the step
		// is reading noise. It is also the guard on the rule's own failure mode — the
		// centred-text case that split a table header mid-phrase — since a rule loose
		// enough to fire on jitter would break these three paragraphs into more.
		"paragraphs": true,

		// The untagged path's list role, which this fixture logged as a literal "•" and a
		// bold "**–**" until layout.Lists existed. Three items and two nested under the
		// second, so it pins both halves of the rule: that a marker glyph plus a
		// separator is what identifies an item, and that depth comes from the left edge.
		//
		// Enforced because its two levels are the only genuine list nesting in anything
		// on disk. Corpus-wide there are eight distinct left-edge gaps inside marker
		// runs, and exactly one of them — this fixture's 2.403 type sizes — is a real
		// indent; the rest are float noise at 0.011. A change that broke nesting would
		// therefore show up nowhere else, and the corpus tests would stay green.
		"lists": true,

		// The tagged path's list markers, which this fixture was built to guard and
		// which logged "- 1\\. First numbered item." against "1. First numbered item."
		// until the sink learned Markdown's ordered syntax. Three bulleted items and
		// three numbered ones, the numbered half being the reason the fixture is not
		// just a tagged copy of "lists": a dropped bullet reads correctly anyway
		// because Markdown writes one, where a mishandled "1." has lost or invented
		// text the document says.
		//
		// Enforced because it is the only thing on disk that pins the ordered path at
		// all. Counting doc.Block.Marker over all 11 corpus documents gives 13 ordered
		// labels against 2022 list items, and they are "[1]"–"[7]" and "a."/"b." — none
		// of which Markdown can express, so they all take the write-it-as-text branch.
		// This fixture holds the only arabic markers anywhere, and without it a change
		// to either branch would show up nowhere.
		"tagged-lists": true,

		// The tagged table grid: three declared columns, a declared header row, two body
		// rows, emitted as a GFM pipe table. Exact on its first build, because the
		// corpus's 788 tagged tables had already driven the sink — this fixture is what
		// pins it, since a corpus assertion is a count and a count cannot tell a correct
		// grid from a transposed one, nor a declared header from a promoted data row.
		//
		// It is the pair to "table", not a duplicate of it, and the two gold files are
		// byte-identical on purpose: same table, one declared and one drawn, so the day
		// stroke-path extraction lands the untagged path has an exact target that is known
		// to be reachable. Enforced because nothing it asserts waits on a deferred debt.
		"tagged-table": true,

		// The same table drawn rather than declared, and byte-identical to the gold file
		// above — which is what the two were built as a pair to prove. This fixture has
		// no Table element and no TH: everything the pipe table says about it was read
		// from the sixteen strokes the page draws, four horizontal and twelve vertical.
		//
		// Enforced because the two gold files being identical is the assertion, not a
		// coincidence. Nothing else on disk can catch the untagged grid diverging from
		// the declared one — the corpus is entirely tagged, so a corpus count would stay
		// green while this path emitted a transposed grid or lost a column.
		"table": true,

		// Not here, and measured rather than assumed: "text-styles" is not a styling gap
		// at all. Every emphasis
		// marker it emits is already byte-correct; its four one-line paragraphs arrive as
		// one block, and DESIGN.md §10 records why no rule can separate them: the only
		// signal left is that a line ends short of the measure, which fires on 57% of all
		// line pairs on disk because nothing on disk is justified.
	}

	for _, f := range referenceFixtures {
		t.Run(f.name, func(t *testing.T) {
			pdf, gold := fixturePaths(t, f.name)
			got := mdOut(t, pdf)

			if got == gold {
				if !exactFixtures[f.name] {
					t.Logf("%s now matches its gold file exactly — add it to exactFixtures so this is enforced", f.name)
				}
				return
			}
			if exactFixtures[f.name] {
				t.Errorf("%s no longer matches its gold file exactly:\n%s", f.name, diff(got, gold))
				return
			}
			t.Logf("%s does not match exactly yet (%s):\n%s", f.name, f.why, diff(got, gold))
		})
	}
}

// fixturePaths returns the PDF and the gold text, skipping when the fixture is
// absent.
//
// Skipped rather than failed only for a missing PDF, which is how the corpus tests
// treat the documents that cannot be committed. These fixtures are committed, so a
// missing one means an incomplete checkout rather than an unlicensed file — but a
// missing gold file is a real error, because a fixture with no expectation is a PDF
// nobody is checking.
func fixturePaths(t *testing.T, name string) (pdf, gold string) {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "reference")
	pdf = filepath.Join(dir, name+".pdf")
	if _, err := os.Stat(pdf); err != nil {
		t.Skipf("%s not built: %v", pdf, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, name+".gold.md")) // #nosec G304 -- fixed test path
	if err != nil {
		t.Fatalf("gold file: %v", err)
	}
	return pdf, string(b)
}

// words splits Markdown into the words it renders, dropping the syntax that carries
// styling rather than content.
//
// Emphasis markers, heading hashes, list bullets, table pipes and rules, and the
// backslashes of escapes are all removed: each is a rendering choice this tier does
// not judge. What remains is what a reader would read aloud, which is what "did the
// text survive" means.
func words(md string) []string {
	r := strings.NewReplacer(
		"***", " ", "**", " ", "*", " ", "`", " ",
		"|", " ", "#", " ", "\\", "",
	)
	var out []string
	for _, w := range strings.Fields(r.Replace(md)) {
		// A table's delimiter row is punctuation, not a word. Bare hyphens survive
		// the replacer above because "-" is also a list marker and, in this corpus, a
		// real character inside words.
		//
		// Bullet glyphs are dropped for the same reason and are worth naming: the
		// layout path has no list role, so LaTeX's markers arrive as ordinary text —
		// "•" for the outer level and a bold "–" for the nested one, which is what the
		// document really draws. Whether those become "- " belongs to the exact-match
		// tier; this tier asks whether "Nested item under the second." survived.
		if strings.Trim(w, "-•–—‣·") == "" {
			continue
		}
		out = append(out, w)
	}
	return out
}

// firstDiff returns the index of the first differing word, or -1 when one sequence
// is a prefix of the other.
//
// The index is what makes the failure readable: "word 14 differs" with the words on
// either side points at the sentence, where a whole-document dump of two Markdown
// files does not.
func firstDiff(got, want []string) int {
	for i := range got {
		if i >= len(want) {
			return -1
		}
		if got[i] != want[i] {
			return i
		}
	}
	return -1
}

// window renders the words around i, marking the one at i.
func window(w []string, i int) string {
	lo, hi := i-3, i+4
	if lo < 0 {
		lo = 0
	}
	if hi > len(w) {
		hi = len(w)
	}
	var sb strings.Builder
	for j := lo; j < hi; j++ {
		if j > lo {
			sb.WriteByte(' ')
		}
		if j == i {
			sb.WriteString(">>>")
			sb.WriteString(w[j])
			sb.WriteString("<<<")
			continue
		}
		sb.WriteString(w[j])
	}
	return sb.String()
}

// countHint names the extra or missing words, which is the difference between "one
// word short" and knowing which one.
func countHint(got, want []string) string {
	if len(got) > len(want) {
		return "extra: " + strings.Join(got[len(want):], " ")
	}
	return "missing: " + strings.Join(want[len(got):], " ")
}

// diff renders the two texts line by line, marking the lines that differ.
//
// Whole-line and not word-level: these documents are a handful of lines each, and a
// character-level diff of a five-line file is harder to read than both files.
func diff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	n := len(g)
	if len(w) > n {
		n = len(w)
	}
	var sb strings.Builder
	for i := 0; i < n; i++ {
		gl, wl := at(g, i), at(w, i)
		if gl == wl {
			sb.WriteString("      " + gl + "\n")
			continue
		}
		sb.WriteString("  got  " + gl + "\n")
		sb.WriteString("  want " + wl + "\n")
	}
	return sb.String()
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return strings.TrimRight(lines[i], "\r")
	}
	return ""
}

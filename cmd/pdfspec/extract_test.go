package main

import (
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/3rg0n/pdf-spec/extract"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
)

// metrics are the columns of the docs/DESIGN.md §1 table, which is the whole
// acceptance criterion for Phase 1. They are computed here rather than in a
// benchmark because "extraction improved" is not a claim without them — every
// library in §1 would have made it.
type metrics struct {
	chars      int
	spaceRatio float64
	longWords  int // word-like tokens over 25 characters
	longFrac   float64
	longest    int
	words      int
	elapsed    time.Duration

	// nonSpace is the character count with whitespace removed, and it is the only
	// cross-extractor comparison that means anything. Total characters measure the
	// producer's whitespace policy as much as the extraction: pdftotext pads lines
	// to preserve layout and reports 47,032 characters on the arXiv paper against
	// our 45,615, yet has 39,035 non-space to our 39,257. The padding was the whole
	// difference.
	nonSpace int

	// rawLongest and rawLongWords count every run of non-space characters, which is
	// what the §1 baselines counted, so the table stays comparable. The asserted
	// figures above exclude non-word tokens — see wordlike.
	rawLongest   int
	rawLongWords int

	// samples are the longest word-like tokens found, which is what makes a
	// regression diagnosable. A count alone cannot distinguish a genuine URL from
	// two words run together, and that is the only question the count is asked to
	// answer.
	samples []string
}

// wordlike reports whether a token is a word at all, as opposed to typographic
// furniture that happens to contain no spaces.
//
// The long-word metric exists to detect a dropped inter-word space, and it detects
// it by inference: nothing in prose is 200 characters long, so a 200-character run
// is two words joined. A table-of-contents dot leader —
// "Scope.................iv" — breaks that inference, because it is genuinely one
// unbroken run of glyphs on the page and every extractor reports it that way. Six
// of the corpus documents open with a contents page, so leaving them in means the
// metric reports a defect on the strength of a correctly extracted line.
//
// The test is whether letters and digits are the majority of the runes. A URL is
// word-like and stays in scope, which matters: a URL is exactly the kind of long
// token a real missing space could hide inside.
func wordlike(w string) bool {
	alnum, total := 0, 0
	for _, r := range w {
		total++
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			alnum++
		}
	}
	return total > 0 && alnum*2 > total
}

// measure computes the §1 metrics over extracted text.
//
// A "word" is a run of non-space characters, which is what the baselines counted.
// The long-word count is the signature measurement: a missing inter-word space
// concatenates two words, so 6.39% of words over 25 characters is pdfplumber
// telling on itself, and a 4,069-character word is a whole paragraph with every
// space dropped.
func measure(text string, elapsed time.Duration) metrics {
	m := metrics{chars: len(text), elapsed: elapsed}

	spaces := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			m.nonSpace++
		}
	}
	if m.chars > 0 {
		m.spaceRatio = float64(spaces) / float64(m.chars) * 100
	}

	var long []string
	for _, w := range strings.Fields(text) {
		m.words++
		n := len([]rune(w))
		if n > 25 {
			m.rawLongWords++
		}
		if n > m.rawLongest {
			m.rawLongest = n
		}
		if !wordlike(w) {
			continue
		}
		if n > 25 {
			m.longWords++
			long = append(long, w)
		}
		if n > m.longest {
			m.longest = n
		}
	}
	if m.words > 0 {
		m.longFrac = float64(m.longWords) / float64(m.words) * 100
	}

	sort.Slice(long, func(i, j int) bool { return len(long[i]) > len(long[j]) })
	if len(long) > 3 {
		long = long[:3]
	}
	m.samples = long
	return m
}

func (m metrics) log(t *testing.T, name string) {
	t.Helper()
	t.Logf("%s: %d chars (%d non-space), %d words, %.2f%% spaces, %d words >25ch (%.2f%%), longest %d, %v",
		name, m.chars, m.nonSpace, m.words, m.spaceRatio, m.longWords, m.longFrac, m.longest, m.elapsed)
	if m.rawLongest != m.longest || m.rawLongWords != m.longWords {
		t.Logf("  including non-word tokens: %d >25ch, longest %d", m.rawLongWords, m.rawLongest)
	}
	for _, w := range m.samples {
		t.Logf("  long: %q", w)
	}
}

func extractDoc(t *testing.T, path string) (string, time.Duration) {
	t.Helper()
	start := time.Now()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	d, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	text := d.Text()
	return text, time.Since(start)
}

// TestExtractionQualityOnArXiv is the §1 comparison, on the one arXiv paper this
// repo can legally carry. It is not the same 6.89 MB paper the baselines were
// measured on — that file belongs to a sibling project — so the bars below were
// established by running two other extractors over this file directly:
//
//	                    non-space   words   >25ch          longest   time
//	this package           39,257   6,343   19 (0.30%)           71   22ms
//	pdftotext (Poppler)    39,035       —   —                     —      —
//	pdfplumber 0.11.9      39,089   2,392   377 (15.8%)         110  660ms
//
// The three agree on the characters to within 0.6%, which is what rules out
// missing text. Where they disagree is how those characters divide into words:
// pdfplumber finds 2,392 where we find 6,343 from the same glyphs, and 15.8% of
// its words run past 25 characters. That is the §1 spaces problem measured on this
// document rather than quoted from the table.
//
// The floor is 38,000 non-space characters — below the two references, not below a
// guess. An earlier version of this test asserted 50,000 *total* characters and
// failed at 45,615; the figure was invented, and pdftotext's higher total turned
// out to be layout padding.
func TestExtractionQualityOnArXiv(t *testing.T) {
	path := paperFile(t)
	text, elapsed := extractDoc(t, path)
	m := measure(text, elapsed)
	m.log(t, "arXiv")

	if m.nonSpace < 38000 {
		t.Errorf("non-space chars = %d, want >= 38000: pdftotext finds 39,035 and pdfplumber 39,089", m.nonSpace)
	}
	// 6,343 words against pdfplumber's 2,392 is the point of the package. A drop
	// toward pdfplumber's figure means spaces stopped being inferred, and it would
	// not move the character count at all.
	if m.words < 5500 {
		t.Errorf("words = %d, want >= 5500: pdfplumber gets 2,392 from the same glyphs", m.words)
	}
	if m.spaceRatio < 10 {
		t.Errorf("spaces = %.2f%%, want >= 10%%: words are being concatenated", m.spaceRatio)
	}
	if m.longFrac > 1.0 {
		t.Errorf("words >25ch = %.2f%%, want <= 1%% (pdfplumber: 15.8%%)", m.longFrac)
	}
	if m.longest > 120 {
		t.Errorf("longest word = %d, want <= 120: the file's longest genuine token is a 71-character URL", m.longest)
	}
}

// TestExtractionQualityOnSpec runs the same bars against the target document, which
// is what Phase 2 has to section. 1,023 pages of two-column standards prose with
// tables, running headers, and a font set that exercises every path in font/.
func TestExtractionQualityOnSpec(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	text, elapsed := extractDoc(t, path)
	m := measure(text, elapsed)
	m.log(t, "ISO 32000-2")

	if m.nonSpace < 1_800_000 {
		t.Errorf("non-space chars = %d, want >= 1.8M: 1,023 pages of prose", m.nonSpace)
	}
	if m.words < 350_000 {
		t.Errorf("words = %d, want >= 350000", m.words)
	}
	if m.spaceRatio < 10 {
		t.Errorf("spaces = %.2f%%, want >= 10%%", m.spaceRatio)
	}
	// Measured 0.06%, against gopdf's 0.11% in the §1 table. A tenth of a percent is
	// the healthy figure; the bar sits above it with room for a producer this
	// document does not contain.
	if m.longFrac > 0.5 {
		t.Errorf("words >25ch = %.2f%%, want <= 0.5%% (measured 0.06%%)", m.longFrac)
	}
	// The longest word-like token is 73 characters. The document's raw longest is
	// 166, and that is a line of ASCII85 from the specification's own §7.4.3 filter
	// example — correctly extracted, and excluded by wordlike.
	if m.longest > 120 {
		t.Errorf("longest word = %d, want <= 120 (measured 73)", m.longest)
	}
}

// TestExtractionAcrossCorpus holds every tracked spec PDF to the same bars, because
// a threshold tuned on one document is a threshold tuned on one producer. Each of
// these files came out of a different toolchain.
func TestExtractionAcrossCorpus(t *testing.T) {
	files := []string{
		"PDF20_AN001-BPC.pdf",
		"PDF20_AN002-AF.pdf",
		"PDF20_AN003-ObjectMetadataLocations.pdf",
		"PDF-Declarations.pdf",
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_TS_32001-2022_sponsored_EC3.pdf",
		"ISO_TS_32002-2022_sponsored_EC3.pdf",
		"ISO_TS_32003-2023_sponsored.pdf",
		"ISO-TS-32004-2024_sponsored.pdf",
		"ISO-TS-32005-2023-sponsored.pdf",
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			path := corpusFile(t, f)
			text, elapsed := extractDoc(t, path)
			m := measure(text, elapsed)
			m.log(t, f)

			if m.words == 0 {
				t.Fatal("no text extracted")
			}
			// The observed band across all ten producers is 13.97%–17.76%. A ratio
			// below 10% is words running together; above 40% is the inverse defect,
			// a space inferred between every glyph, which this package shipped once
			// and which no character-count assertion would have caught.
			if m.spaceRatio < 10 || m.spaceRatio > 40 {
				t.Errorf("spaces = %.2f%%, want 10%%–40%%", m.spaceRatio)
			}
			// Observed 0.07%–0.79%, the high figure being PDF-Declarations.pdf, which
			// is mostly XMP markup rather than prose and so has genuinely long tokens.
			if m.longFrac > 1.0 {
				t.Errorf("words >25ch = %.2f%%, want <= 1%%", m.longFrac)
			}
			// Observed 37–83. Every token above that in these files is a dot leader or
			// a hex string, which wordlike excludes.
			if m.longest > 120 {
				t.Errorf("longest word = %d, want <= 120", m.longest)
			}
		})
	}
}

// TestEveryPageAccountedFor: a missing page shifts every page number after it, and
// page numbers are what a reader checks a conversion against.
func TestEveryPageAccountedFor(t *testing.T) {
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	d, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(d.Pages) != s.PageCount() {
		t.Fatalf("pages = %d, want %d", len(d.Pages), s.PageCount())
	}

	blank := 0
	for i, p := range d.Pages {
		if p.Number != i+1 {
			t.Fatalf("page %d numbered %d", i+1, p.Number)
		}
		if p.Box.IsZero() {
			t.Errorf("page %d has no box", p.Number)
		}
		if strings.TrimSpace(p.Text()) == "" {
			blank++
		}
	}
	// A specification has few genuinely blank pages. A large count means the
	// extractor is failing on a class of page rather than on one bad one.
	if blank > len(d.Pages)/10 {
		t.Errorf("%d of %d pages blank", blank, len(d.Pages))
	}
}

// TestArtifactsAreDropped: the same running header on 1,023 pages, interleaved with
// body prose at every page boundary, is the noise the artifact rule removes. Both
// modes are measured because the difference is the evidence that the rule fires.
func TestArtifactsAreDropped(t *testing.T) {
	path := corpusFile(t, "ISO_TS_32001-2022_sponsored_EC3.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	dropped, err := extract.New(s, extract.DefaultOptions).Document()
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	opt := extract.DefaultOptions
	opt.KeepArtifacts = true
	kept, err := extract.New(s, opt).Document()
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	a, b := len(dropped.Text()), len(kept.Text())
	t.Logf("artifacts dropped: %d chars, kept: %d chars", a, b)
	if b <= a {
		t.Error("KeepArtifacts changed nothing: the artifact rule is not firing")
	}
	if a == 0 {
		t.Error("dropping artifacts removed all text")
	}
}

// TestExtractionIsDeterministic: two runs over the same file must agree exactly.
// Map iteration order and a pointer into a reallocated slice both produce output
// that differs between runs, and both are defects this package has already had.
func TestExtractionIsDeterministic(t *testing.T) {
	path := corpusFile(t, "PDF20_AN002-AF.pdf")
	first, _ := extractDoc(t, path)
	second, _ := extractDoc(t, path)
	if first != second {
		t.Error("two runs disagree")
	}
}

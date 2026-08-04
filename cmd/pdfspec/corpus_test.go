package main

import (
	"os"
	"path/filepath"
	"testing"

	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
	"github.com/3rg0n/pdf-spec/tag"
)

// The spec PDFs are gitignored (paid ISO documents, not redistributable), so
// every test here skips when its file is absent. A clone without the corpus must
// still pass the suite.
const corpusDir = "../../docs"

func corpusFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(corpusDir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("corpus file absent: %s (see docs/DESIGN.md section 3)", name)
	}
	return p
}

// paperName is the arXiv paper the §1 extraction baselines in extract_test.go were
// measured on. It is not a corpus document and never was — it is the one untagged,
// non-standards file the early phases had — and it is no longer redistributed with this
// repo, so paperFile skips the same way corpusFile does. See .gitignore for where to get
// it. Every aggregate figure in the suite excludes it; corpus above is what those count.
const paperName = "LightOnOCR-2601.14251v1.pdf"

func paperFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(corpusDir, paperName)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("arXiv paper absent: %s (see .gitignore for the source)", paperName)
	}
	return p
}

// corpus names the eleven documents every aggregate baseline in this file was measured
// over. It is an explicit list rather than a glob of docs/*.pdf, and that distinction has
// already cost a measurement: docs/ is a working directory as well as the corpus, so a
// paper dropped there to be read joined the baseline silently, and every aggregate figure
// in the repo — image counts, soft-mask counts, the largest image — was six images and one
// mask high for two phases. A glob cannot tell a spec document from a stray download; this
// can.
var corpus = []string{
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
	"ISO_32000-2_sponsored_EC3.pdf",
}

// corpusFiles lists the corpus documents present on disk, for the tests that assert
// something of every file rather than of a named one. Empty when the corpus is absent,
// which leaves such a test with nothing to run rather than failing it.
func corpusFiles() []string {
	var out []string
	for _, name := range corpus {
		if _, err := os.Stat(filepath.Join(corpusDir, name)); err == nil {
			out = append(out, name)
		}
	}
	return out
}

// want captures the expectations that must not regress. These numbers came from
// running probe against the real files, so they are a baseline, not a guess: a
// change in any of them means parsing behavior moved.
type want struct {
	file     string
	pages    int
	tagged   bool
	marked   bool
	minElems int
	minHeads int
}

func TestCorpusStructure(t *testing.T) {
	cases := []want{
		{"PDF20_AN001-BPC.pdf", 5, true, true, 83, 8},
		{"PDF20_AN002-AF.pdf", 14, true, true, 259, 32},
		{"PDF20_AN003-ObjectMetadataLocations.pdf", 10, true, true, 333, 22},
		{"PDF-Declarations.pdf", 10, true, true, 311, 22},
		{"Well-Tagged-PDF-WTPDF-1.0.pdf", 57, true, true, 2061, 183},
		{"ISO_TS_32001-2022_sponsored_EC3.pdf", 14, true, true, 322, 14},
		{"ISO_TS_32002-2022_sponsored_EC3.pdf", 14, true, true, 645, 14},
		// These two carry a StructTreeRoot but no /MarkInfo /Marked. Technically
		// non-conformant per ISO 32000-2 14.7.1, yet substantively tagged, which
		// is why the tagged path must never gate on /Marked.
		{"ISO_TS_32003-2023_sponsored.pdf", 13, true, false, 394, 11},
		{"ISO-TS-32004-2024_sponsored.pdf", 25, true, false, 1149, 55},
		{"ISO-TS-32005-2023-sponsored.pdf", 49, true, true, 6266, 27},
		{"ISO_32000-2_sponsored_EC3.pdf", 1023, true, true, 78469, 981},
	}

	// The two lists must name the same files, or an aggregate baseline is being measured
	// over a population this table does not describe — which is how the image counts drifted.
	if len(cases) != len(corpus) {
		t.Fatalf("%d cases against %d corpus documents", len(cases), len(corpus))
	}
	named := map[string]bool{}
	for _, tc := range cases {
		named[tc.file] = true
	}
	for _, name := range corpus {
		if !named[name] {
			t.Errorf("%s is in corpus but has no structural baseline here", name)
		}
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := corpusFile(t, tc.file)
			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer s.Close()

			if got := s.PageCount(); got != tc.pages {
				t.Errorf("pages = %d, want %d", got, tc.pages)
			}
			if s.Encrypted() {
				t.Error("unexpectedly encrypted")
			}

			tr, err := tag.Read(s)
			if err != nil {
				t.Fatalf("tag.Read: %v", err)
			}
			if !tc.tagged {
				if tr != nil {
					t.Error("expected no structure tree")
				}
				return
			}
			if tr == nil {
				t.Fatal("expected a structure tree")
			}
			st := tr.Stats()
			if st.Elements < tc.minElems {
				t.Errorf("elements = %d, want >= %d", st.Elements, tc.minElems)
			}
			if st.Headings < tc.minHeads {
				t.Errorf("headings = %d, want >= %d", st.Headings, tc.minHeads)
			}
			if st.MCIDs == 0 {
				t.Error("no MCIDs: structure tree cannot be joined to page text")
			}
			// A tree deep enough to express hierarchy is the prerequisite for
			// section reconstruction.
			if st.MaxDepth < 3 {
				t.Errorf("max depth = %d, too flat to carry hierarchy", st.MaxDepth)
			}
		})
	}
}

func TestUntaggedCorpusTakesLayoutPath(t *testing.T) {
	// All eleven corpus documents are tagged, so the layout path has no witness among
	// them at all — this paper is the only untagged PDF the early phases had. testdata/
	// now covers the same ground with committed files (see testdata_test.go), which is
	// why purging this one cost coverage rather than removing it.
	path := paperFile(t)
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if got := s.PageCount(); got != 17 {
		t.Errorf("pages = %d, want 17", got)
	}
	tr, err := tag.Read(s)
	if err != nil {
		t.Fatalf("tag.Read: %v", err)
	}
	if tr != nil {
		t.Fatal("expected no structure tree")
	}
}

func TestResolvePagesAnchorsElements(t *testing.T) {
	// Cross-page sections are the point of the tagged path, so /Pg resolution
	// has to produce real page numbers within range.
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	tr, err := tag.Read(s)
	if err != nil || tr == nil {
		t.Fatalf("tag.Read: %v", err)
	}
	if err := tr.ResolvePages(s); err != nil {
		t.Fatalf("ResolvePages: %v", err)
	}

	anchored, maxPage := 0, 0
	tr.Walk(func(e *tag.Elem, _ int) bool {
		if e.Page > 0 {
			anchored++
			if e.Page > maxPage {
				maxPage = e.Page
			}
		}
		if e.Page < 0 || e.Page > s.PageCount() {
			t.Errorf("page %d out of range 1..%d", e.Page, s.PageCount())
		}
		return true
	})
	if anchored == 0 {
		t.Fatal("no elements anchored to a page")
	}
	// The tree should reach deep into the document, not just the first pages.
	if maxPage < s.PageCount()/2 {
		t.Errorf("highest anchored page %d of %d: page resolution looks wrong",
			maxPage, s.PageCount())
	}
	t.Logf("anchored %d elements, highest page %d of %d", anchored, maxPage, s.PageCount())
}

func TestCrossPageSectionsExist(t *testing.T) {
	// The structure tree is document-scoped, not page-scoped, so some elements
	// must reach across page boundaries. If none did, the tree would be a
	// per-page artifact and the tagged path would buy nothing over per-page
	// parsing. Which elements span, and whether they delimit clauses, is a
	// separate question — see TestSectionShapeOnTarget.
	path := corpusFile(t, "ISO_TS_32001-2022_sponsored_EC3.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	tr, err := tag.Read(s)
	if err != nil || tr == nil {
		t.Fatalf("tag.Read: %v", err)
	}
	if err := tr.ResolvePages(s); err != nil {
		t.Fatalf("ResolvePages: %v", err)
	}

	spanning := 0
	tr.Walk(func(e *tag.Elem, _ int) bool {
		lo, hi := 0, 0
		var scan func(*tag.Elem)
		scan = func(n *tag.Elem) {
			if n.Page > 0 {
				if lo == 0 || n.Page < lo {
					lo = n.Page
				}
				if n.Page > hi {
					hi = n.Page
				}
			}
			for _, k := range n.Kids {
				scan(k)
			}
		}
		scan(e)
		if lo > 0 && hi > lo {
			spanning++
		}
		return true
	})
	if spanning == 0 {
		t.Fatal("no element spans pages: the tagged-path premise does not hold here")
	}
	t.Logf("%d elements span more than one page", spanning)
}

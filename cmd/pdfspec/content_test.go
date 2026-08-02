package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/content"
	"github.com/3rg0n/pdf-spec/geom"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
)

// These tests run the real corpus through the scanner and state machine. Hand-written
// fixtures cover the paths I thought of; 30 years of producer output covers the ones
// I did not, and this is the only place the two packages meet a genuine content
// stream before extract/ depends on them both.

// streamStats is what a full pass over one document's content yields.
type streamStats struct {
	pages     int
	ops       int
	textOps   int
	unknown   map[string]int
	withMCID  int
	artifacts int
	inlineImg int
	fonts     map[string]bool
}

// scanDocument runs every page of a document through the pipeline. It reports the
// operator mix rather than asserting on it, because the point is that nothing
// panics, nothing hangs, and the operator vocabulary is one this package knows.
func scanDocument(t *testing.T, path string) streamStats {
	t.Helper()
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	st := streamStats{
		unknown: map[string]int{},
		fonts:   map[string]bool{},
	}
	st.pages = s.PageCount()

	for p := 1; p <= s.PageCount(); p++ {
		data, err := s.PageContent(p)
		if err != nil {
			// A page whose content cannot be decoded is a real condition in this
			// corpus; it must not fail the pass.
			t.Logf("page %d content: %v", p, err)
			continue
		}
		if len(data) == 0 {
			continue
		}

		m := content.NewMachine(geom.Identity)
		sc := content.NewScanner(data)
		for {
			op, ok := sc.Next()
			if !ok {
				break
			}
			st.ops++

			if !m.Apply(op) {
				switch op.Name {
				case "Tj", "TJ", "'", `"`:
					st.textOps++
					if m.MCID() >= 0 {
						st.withMCID++
					}
					if m.InArtifact() {
						st.artifacts++
					}
				case "INLINE_IMAGE":
					st.inlineImg++
				default:
					if !knownOperator(op.Name) {
						st.unknown[op.Name]++
					}
				}
			}
			if op.Name == "Tf" {
				st.fonts[string(op.NameAt(0))] = true
			}
		}
	}
	return st
}

// knownOperator reports whether name is an operator defined by ISO 32000-2
// Annex A. Anything else in a real stream is either a producer extension or
// damage, and is worth counting rather than ignoring: an operator this package
// has never seen is usually the reason text goes missing.
func knownOperator(name string) bool {
	switch name {
	// Path construction and painting.
	case "m", "l", "c", "v", "y", "h", "re",
		"S", "s", "f", "F", "f*", "B", "B*", "b", "b*", "n":
		return true
	// Colour.
	case "CS", "cs", "SC", "SCN", "sc", "scn", "G", "g", "RG", "rg", "K", "k":
		return true
	// General graphics state not affecting text placement.
	case "w", "J", "j", "M", "d", "ri", "i", "gs":
		return true
	// XObjects, shading, and compatibility.
	case "Do", "sh", "BX", "EX", "d0", "d1":
		return true
	// Marked content without a property list, and inline images.
	case "MP", "DP", "BI", "ID", "EI":
		return true
	// Type 3 glyph and text-showing operators are handled by the caller.
	case "Tj", "TJ", "'", `"`:
		return true
	}
	return false
}

func TestCorpusContentStreamsScan(t *testing.T) {
	// Every PDF in the corpus, tagged and untagged, spec and paper. A file that
	// hangs or panics here is a file the extractor could never process.
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
		"LightOnOCR-2601.14251v1.pdf",
	}

	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			path := corpusFile(t, name)
			st := scanDocument(t, path)

			if st.ops == 0 {
				t.Fatal("no operators scanned: the content streams did not decode")
			}
			if st.textOps == 0 {
				t.Fatal("no text-showing operators: extraction would yield nothing")
			}
			if len(st.fonts) == 0 {
				t.Error("no Tf seen: every text-showing operator would lack metrics")
			}

			// An unknown operator means either a producer extension or a lexer that
			// mis-split a token. Either way it is worth surfacing, but only a large
			// share suggests the latter.
			total := 0
			for _, n := range st.unknown {
				total += n
			}
			if total > st.ops/100 {
				t.Errorf("%d of %d operators unrecognized (%.1f%%): %v",
					total, st.ops, float64(total)*100/float64(st.ops), st.unknown)
			} else if total > 0 {
				t.Logf("unrecognized operators (%d of %d): %v", total, st.ops, st.unknown)
			}

			t.Logf("pages=%d ops=%d text=%d withMCID=%d artifacts=%d inline=%d fonts=%d",
				st.pages, st.ops, st.textOps, st.withMCID, st.artifacts,
				st.inlineImg, len(st.fonts))
		})
	}
}

func TestTargetSpecContentStreamsScan(t *testing.T) {
	// ISO 32000-2 on its own: 1,023 pages is the scale the OKF conversion has to
	// survive, and it is slow enough to be worth its own test so the others stay
	// fast.
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	st := scanDocument(t, path)

	if st.pages != 1023 {
		t.Errorf("pages = %d, want 1023", st.pages)
	}
	if st.textOps < 100000 {
		t.Errorf("text operators = %d, want many more from a 1023-page standard", st.textOps)
	}
	// The document is tagged, so nearly all of its text must carry an MCID. That
	// join is what section reconstruction depends on, and a low ratio here would
	// mean the tagged path cannot work regardless of what the tree says.
	if ratio := float64(st.withMCID) / float64(st.textOps); ratio < 0.9 {
		t.Errorf("only %.1f%% of text operators carry an MCID, want >= 90%%",
			ratio*100)
	}
	t.Logf("pages=%d ops=%d text=%d withMCID=%d artifacts=%d fonts=%d",
		st.pages, st.ops, st.textOps, st.withMCID, st.artifacts, len(st.fonts))
}

func TestCorpusContentStreamsSurviveTruncation(t *testing.T) {
	// Feed the scanner progressively truncated real content. Every prefix is a
	// malformed stream that a damaged file could contain, and none may panic or
	// hang. Synthetic fixtures cannot produce the variety of half-tokens this does.
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	data, err := s.PageContent(1)
	if err != nil || len(data) == 0 {
		t.Fatalf("page 1 content: %v", err)
	}

	// Every prefix length, capped so the test stays quick on a large page.
	step := 1
	if len(data) > 4000 {
		step = len(data) / 4000
	}
	for n := 0; n <= len(data); n += step {
		m := content.NewMachine(geom.Identity)
		sc := content.NewScanner(data[:n])
		for i := 0; ; i++ {
			if i > 1000000 {
				t.Fatalf("scanner did not terminate on a %d-byte prefix", n)
			}
			op, ok := sc.Next()
			if !ok {
				break
			}
			m.Apply(op)
			// Exercise the accessors a caller would use, since out-of-range reads
			// are exactly what a truncated operator produces.
			_ = m.RenderMatrix()
			_, _ = op.Num(0), op.Int(1)
			_, _ = op.NameAt(0), op.Str(0)
			_, _ = op.Arr(0), op.Dict(1)
			_ = m.MCID()
			_ = m.InArtifact()
		}
	}
}

func TestCorpusContentStreamsSurviveCorruption(t *testing.T) {
	// Flip bytes in real content and rescan. This reaches states no hand-written
	// fixture would: a length byte inside a string, a delimiter mid-operator, a
	// mangled inline-image header.
	path := corpusFile(t, "PDF20_AN001-BPC.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	orig, err := s.PageContent(1)
	if err != nil || len(orig) == 0 {
		t.Fatalf("page 1 content: %v", err)
	}

	// A fixed set of byte values chosen for their lexical significance, applied at
	// deterministic offsets. Deterministic so a failure reproduces exactly.
	poison := []byte{'(', ')', '<', '>', '[', ']', '/', '%', '\\', 0x00, 0xFF, 'E', 'I'}
	data := make([]byte, len(orig))

	for _, b := range poison {
		for off := 0; off < len(orig); off += 37 {
			copy(data, orig)
			data[off] = b

			m := content.NewMachine(geom.Identity)
			sc := content.NewScanner(data)
			for i := 0; ; i++ {
				if i > 1000000 {
					t.Fatalf("scanner did not terminate with %q at offset %d", b, off)
				}
				op, ok := sc.Next()
				if !ok {
					break
				}
				m.Apply(op)
				_ = m.RenderMatrix()
				_ = m.MCID()
			}
		}
	}
}

func TestMarkedContentBalanceOnCorpus(t *testing.T) {
	// A well-tagged document should balance its BDC/EMC pairs on every page. An
	// unbalanced page would leave the MCID stack dirty, misattributing text to the
	// wrong structure element for the rest of the page — which is silent, and
	// exactly the failure the OKF conversion cannot tolerate.
	path := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	unbalanced := 0
	for p := 1; p <= s.PageCount(); p++ {
		data, err := s.PageContent(p)
		if err != nil || len(data) == 0 {
			continue
		}
		depth := 0
		sc := content.NewScanner(data)
		for {
			op, ok := sc.Next()
			if !ok {
				break
			}
			switch op.Name {
			case "BDC", "BMC":
				depth++
			case "EMC":
				depth--
			}
		}
		if depth != 0 {
			unbalanced++
			t.Logf("page %d ends at marked-content depth %d", p, depth)
		}
	}
	if unbalanced > 0 {
		t.Errorf("%d of %d pages have unbalanced marked content",
			unbalanced, s.PageCount())
	}
}

func TestGraphicsStateBalanceOnCorpus(t *testing.T) {
	// q/Q should balance too. Reporting rather than failing: an unbalanced page is
	// a producer bug that the machine tolerates by design, and this records how
	// often the tolerance is actually needed.
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Skipf("corpus absent: %v", err)
	}

	checked := 0
	for _, e := range entries {
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			continue
		}
		s, err := pcstore.Open(filepath.Join(corpusDir, e.Name()))
		if err != nil {
			t.Logf("%s: open: %v", e.Name(), err)
			continue
		}
		checked++

		unbalanced := 0
		for p := 1; p <= s.PageCount(); p++ {
			data, err := s.PageContent(p)
			if err != nil || len(data) == 0 {
				continue
			}
			depth := 0
			sc := content.NewScanner(data)
			for {
				op, ok := sc.Next()
				if !ok {
					break
				}
				switch op.Name {
				case "q":
					depth++
				case "Q":
					depth--
				}
			}
			if depth != 0 {
				unbalanced++
			}
		}
		if unbalanced > 0 {
			t.Logf("%s: %d of %d pages unbalanced q/Q", e.Name(), unbalanced, s.PageCount())
		}
		s.Close()
	}
	if checked == 0 {
		t.Skip("no corpus files opened")
	}
	t.Logf("checked %d documents", checked)
}

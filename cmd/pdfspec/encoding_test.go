package main

import (
	"fmt"
	"sort"
	"testing"

	"github.com/model-harness/pdftools/font/encoding"
	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
)

// The glyph list in font/encoding is deliberately a reduced Adobe Glyph List,
// which is only defensible if it actually covers the names real documents use.
// This test measures that against the corpus rather than asserting it: it walks
// every /Encoding /Differences array in every corpus file and reports any glyph
// name that does not resolve.
//
// An unresolved name is a real defect, not a curiosity. The code maps to a glyph
// name, the name maps to nothing, and the character vanishes from the extracted
// text with no error anywhere — which is precisely the failure mode that makes
// existing extractors lose text. The test lives here rather than in
// font/encoding because it needs a Store, and font/encoding knows nothing about
// PDF objects on purpose.

func TestCorpusDifferencesResolve(t *testing.T) {
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
		"ISO_32000-2_sponsored_EC3.pdf",
	}

	// Counted across the whole corpus so the summary is one line rather than one
	// per file, and so a name that appears in several documents is reported once.
	unresolved := map[string]int{}
	names, differences, encodings := 0, 0, map[string]int{}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := corpusFile(t, file)
			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer s.Close()

			// Fonts are shared across pages, so each object is examined once.
			// Without this a 1,023-page document reports the same font thousands of
			// times and the counts describe page references, not fonts.
			seen := map[objects.Ref]bool{}

			for n := 1; n <= s.PageCount(); n++ {
				page, err := s.Page(n)
				if err != nil {
					continue
				}
				res, ok := objects.GetDict(s, page, "Resources")
				if !ok {
					continue
				}
				fonts, ok := objects.GetDict(s, res, "Font")
				if !ok {
					continue
				}
				for _, v := range fonts {
					if ref, isRef := v.(objects.Ref); isRef {
						if seen[ref] {
							continue
						}
						seen[ref] = true
					}
					f, err := s.Resolve(v)
					if err != nil {
						continue
					}
					fd, isDict := f.(objects.Dict)
					if !isDict {
						continue
					}
					// A Type0 font's /Encoding names a CMap — Identity-H throughout
					// this corpus — not a byte encoding, so font/encoding does not
					// apply and its absence from baseTables is correct. Composite
					// fonts belong to font/cmap.
					if sub, _ := objects.GetName(s, fd, "Subtype"); sub == "Type0" {
						continue
					}

					// /Encoding is a name for a bare base encoding, or a dictionary
					// when the font overrides individual codes. Both forms appear in
					// this corpus.
					enc, ok := objects.Get(s, fd, "Encoding")
					if !ok {
						continue
					}
					switch e := enc.(type) {
					case objects.Name:
						encodings[string(e)]++
						if _, known := encoding.Base(string(e)); !known {
							t.Errorf("page %d: font names /Encoding /%s, which this package does not implement", n, e)
						}
					case objects.Dict:
						if base, ok := objects.GetName(s, e, "BaseEncoding"); ok {
							encodings[string(base)]++
							if _, known := encoding.Base(string(base)); !known {
								t.Errorf("page %d: font names /BaseEncoding /%s, which this package does not implement", n, base)
							}
						}
						arr, ok := objects.GetArray(s, e, "Differences")
						if !ok {
							continue
						}
						differences++
						for _, item := range arr {
							o, err := s.Resolve(item)
							if err != nil {
								continue
							}
							glyph, isName := o.(objects.Name)
							if !isName {
								continue // a code, which starts the next run
							}
							names++
							if _, ok := encoding.GlyphText(string(glyph)); !ok {
								unresolved[string(glyph)]++
							}
						}
					}
				}
			}
		})
	}

	if names == 0 {
		return // corpus absent; the subtests already skipped
	}

	t.Logf("%d glyph names across %d /Differences arrays; base encodings %s",
		names, differences, sortedCounts(encodings))

	if len(unresolved) > 0 {
		t.Errorf("%d glyph names in the corpus resolve to no text: %s\n"+
			"each one is a character that silently disappears from extracted text",
			len(unresolved), sortedCounts(unresolved))
	}

	// Pinned so the coverage claim is an assertion, not a log line: if a change
	// stops finding /Differences arrays the test would otherwise pass by scanning
	// nothing. Measured, not chosen.
	if names != 82 || differences != 4 {
		t.Errorf("found %d glyph names across %d /Differences arrays, want 82 across 4; "+
			"the corpus did not change, so the traversal did",
			names, differences)
	}
	if got := len(encodings); got != 1 || encodings["WinAnsiEncoding"] == 0 {
		t.Errorf("base encodings = %s, want WinAnsiEncoding only; every simple font in "+
			"this corpus uses it, which is why it is the one that must be exactly right",
			sortedCounts(encodings))
	}
}

// TestCorpusFontsWithoutToUnicodeHaveAnEncoding is the measurement the whole
// package exists for. A substantial share of real fonts ship no /ToUnicode
// CMap, so an extractor that reads only /ToUnicode returns nothing for them.
// This asserts the fallback is actually available: every such font must name an
// encoding this package implements, or supply /Differences.
func TestCorpusFontsWithoutToUnicodeHaveAnEncoding(t *testing.T) {
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
		"ISO_32000-2_sponsored_EC3.pdf",
	}

	total, withoutToUnicode, covered := 0, 0, 0

	for _, file := range files {
		path := corpusFile(t, file)
		s, err := pcstore.Open(path)
		if err != nil {
			t.Fatalf("%s: open: %v", file, err)
		}
		// Font objects are shared across pages, so each is counted once. Scoped
		// per file because object numbers mean nothing across files.
		seen := map[objects.Ref]bool{}

		for n := 1; n <= s.PageCount(); n++ {
			page, err := s.Page(n)
			if err != nil {
				continue
			}
			res, ok := objects.GetDict(s, page, "Resources")
			if !ok {
				continue
			}
			fonts, ok := objects.GetDict(s, res, "Font")
			if !ok {
				continue
			}
			for _, v := range fonts {
				// Fonts are shared across pages, so count each object once or a
				// 1,023-page document reports thousands of fonts.
				if ref, isRef := v.(objects.Ref); isRef {
					if seen[ref] {
						continue
					}
					seen[ref] = true
				}
				f, err := s.Resolve(v)
				if err != nil {
					continue
				}
				fd, isDict := f.(objects.Dict)
				if !isDict {
					continue
				}
				sub, _ := objects.GetName(s, fd, "Subtype")
				if sub == "Type0" {
					// A composite font resolves codes through its CMap, not through
					// a byte encoding, so font/encoding does not apply.
					continue
				}
				total++
				if _, has := objects.Get(s, fd, "ToUnicode"); has {
					continue
				}
				withoutToUnicode++

				if hasUsableEncoding(s, fd) {
					covered++
					continue
				}
				name, _ := objects.GetName(s, fd, "BaseFont")
				t.Errorf("%s page %d: font %s has neither /ToUnicode nor a usable /Encoding, so its text is unrecoverable",
					file, n, name)
			}
		}
		s.Close()
	}

	if total == 0 {
		t.Skip("corpus absent")
	}
	t.Logf("%d simple fonts, %d without /ToUnicode, %d of those covered by an encoding",
		total, withoutToUnicode, covered)

	// The figure the encoding package's doc comment cites. Pinned so the doc and
	// the corpus cannot drift apart, and so a traversal that quietly stops
	// finding fonts fails instead of passing vacuously.
	if total != 134 || withoutToUnicode != 55 {
		t.Errorf("%d simple fonts and %d without /ToUnicode, want 134 and 55; "+
			"update font/encoding's package comment if this is an intended change",
			total, withoutToUnicode)
	}
}

// hasUsableEncoding reports whether a font dictionary names an encoding this
// package can resolve, directly or through /Differences.
func hasUsableEncoding(s objects.Store, fd objects.Dict) bool {
	enc, ok := objects.Get(s, fd, "Encoding")
	if !ok {
		return false
	}
	switch e := enc.(type) {
	case objects.Name:
		_, known := encoding.Base(string(e))
		return known
	case objects.Dict:
		if base, ok := objects.GetName(s, e, "BaseEncoding"); ok {
			if _, known := encoding.Base(string(base)); known {
				return true
			}
		}
		// No base encoding named: /Differences alone is usable only if it is
		// present, since the implicit base is the font's own built-in encoding,
		// which this package cannot see.
		arr, ok := objects.GetArray(s, e, "Differences")
		return ok && len(arr) > 0
	}
	return false
}

func sortedCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s=%d", k, m[k])
	}
	return out
}

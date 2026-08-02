package main

import (
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/3rg0n/pdf-spec/font/cmap"
	"github.com/3rg0n/pdf-spec/objects"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
)

// Hand-written CMap fixtures prove the parser handles the syntax its author
// thought of. Real /ToUnicode streams prove it handles the syntax producers
// actually emit, which is the part that decides whether text comes out right.
//
// The check is not "did it parse" — a parser this tolerant always parses. It is
// whether the mappings are plausible text: a /ToUnicode CMap that resolves every
// code to a control character or an unassigned code point parsed successfully and
// is still useless, and that failure would otherwise show up as garbled output
// far downstream.

func TestCorpusToUnicodeCMaps(t *testing.T) {
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

	streams, mappings, empty := 0, 0, 0
	// Counted rather than asserted per-code, because a CMap may legitimately map
	// a code to a space or a soft hyphen. A high proportion, though, means the
	// parser is misreading destinations.
	suspect := 0

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := corpusFile(t, file)
			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer s.Close()

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
					data, ok := objects.GetStreamData(s, fd, "ToUnicode")
					if !ok {
						continue
					}
					streams++

					c, err := cmap.Parse(data)
					if err != nil {
						name, _ := objects.GetName(s, fd, "BaseFont")
						t.Errorf("page %d font %s: /ToUnicode failed to parse: %v", n, name, err)
						continue
					}
					_, texts := c.Entries()
					mappings += texts
					if texts == 0 {
						// A /ToUnicode stream that yields nothing is the failure mode
						// this test exists to catch: the font declared a mapping and
						// the reader found none, so its text is silently lost.
						name, _ := objects.GetName(s, fd, "BaseFont")
						t.Errorf("page %d font %s: /ToUnicode parsed to zero mappings (%d bytes of stream)",
							n, name, len(data))
						empty++
						continue
					}

					// Every code the CMap claims must split and resolve to text that
					// is at least well-formed UTF-8 and not a control character.
					for code := uint32(0); code <= 0xFFFF; code++ {
						text, ok := c.Text(code)
						if !ok {
							continue
						}
						if !utf8.ValidString(text) {
							t.Errorf("page %d: code 0x%04X maps to invalid UTF-8 % x", n, code, text)
							continue
						}
						for _, r := range text {
							if isSuspectRune(r) {
								suspect++
								break
							}
						}
					}
				}
			}
		})
	}

	if streams == 0 {
		return // corpus absent
	}

	t.Logf("%d /ToUnicode streams, %d mappings, %d suspect destinations",
		streams, mappings, suspect)

	// Pinned so a traversal that stops finding streams fails rather than passing
	// by scanning nothing, and so a parser change that loses mappings shows up
	// here as a number rather than as garbled text later.
	if streams != 171 || mappings != 6541 {
		t.Errorf("%d streams and %d mappings, want 171 and 6541; the corpus did not change, so the reader did",
			streams, mappings)
	}
	if empty > 0 {
		t.Errorf("%d /ToUnicode streams yielded no mappings at all", empty)
	}
	// One in twenty is generous: a real CMap maps the odd code to a space or a
	// private-use glyph. Much beyond that means destinations are being misread —
	// a byte-order mistake, for instance, turns every 'A' (U+0041) into U+4100.
	if limit := mappings / 20; suspect > limit {
		t.Errorf("%d of %d destinations are control or unassigned characters, over the %d tolerated; "+
			"destinations are likely being decoded wrongly rather than the documents being unusual",
			suspect, mappings, limit)
	}
}

// isSuspectRune reports a character that a /ToUnicode destination should almost
// never name. Control characters other than the whitespace a document really can
// contain, and code points Unicode has not assigned, both indicate a
// misinterpreted destination rather than unusual content.
func isSuspectRune(r rune) bool {
	switch r {
	case '\t', '\n', '\r', ' ', 0x00A0, 0x00AD:
		return false
	}
	if unicode.IsControl(r) {
		return true
	}
	// Unassigned, but not private use: private-use code points are how fonts name
	// glyphs with no Unicode meaning, which is legitimate.
	if unicode.In(r, unicode.Co) {
		return false
	}
	return !unicode.IsGraphic(r)
}

func TestCorpusCompositeFontCMapNames(t *testing.T) {
	// Which encoding CMaps the corpus actually uses, asserted rather than assumed.
	// font/cmap synthesizes Identity-H and Identity-V and falls back to two-byte
	// codes for anything else, so a corpus using an embedded CMap stream would
	// silently take the fallback path and lose its CIDs.
	files := []string{
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
		"ISO-TS-32005-2023-sponsored.pdf",
	}

	names := map[string]int{}
	embedded := 0

	for _, file := range files {
		path := corpusFile(t, file)
		s, err := pcstore.Open(path)
		if err != nil {
			t.Fatalf("%s: open: %v", file, err)
		}

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
				if sub, _ := objects.GetName(s, fd, "Subtype"); sub != "Type0" {
					continue
				}
				enc, ok := objects.Get(s, fd, "Encoding")
				if !ok {
					t.Errorf("%s page %d: a Type0 font with no /Encoding, which is required", file, n)
					continue
				}
				switch e := enc.(type) {
				case objects.Name:
					names[string(e)]++
					if _, isIdentity := cmap.Identity(string(e)); !isIdentity {
						// Not an error: TwoByte handles it. Worth surfacing because
						// it means CIDs come from a predefined CMap this package
						// does not carry.
						t.Logf("%s page %d: predefined CMap /%s takes the two-byte fallback", file, n, e)
					}
				case *objects.Stream:
					embedded++
					if e.Decoded == nil {
						if err := s.Decode(e); err != nil {
							t.Errorf("%s page %d: embedded CMap failed to decode: %v", file, n, err)
							continue
						}
					}
					c, err := cmap.Parse(e.Decoded)
					if err != nil {
						t.Errorf("%s page %d: embedded CMap failed to parse: %v", file, n, err)
						continue
					}
					if cids, _ := c.Entries(); cids == 0 && !hasIdentityCodespace(c) {
						t.Errorf("%s page %d: embedded CMap parsed to zero CID mappings", file, n)
					}
				default:
					t.Errorf("%s page %d: /Encoding is %T, want a name or a stream", file, n, enc)
				}
			}
		}
		s.Close()
	}

	if len(names) == 0 && embedded == 0 {
		t.Skip("corpus absent")
	}
	t.Logf("composite font encodings: %s; %d embedded CMap streams", sortedCounts(names), embedded)

	// Identity-H only, which is what makes the synthesized path the one that has
	// to be right. If this changes, the two-byte fallback is carrying real
	// documents and a predefined-CMap table becomes worth its size.
	if len(names) != 1 || names["Identity-H"] == 0 {
		t.Errorf("composite encodings = %s, want Identity-H only", sortedCounts(names))
	}
}

// hasIdentityCodespace reports whether a CMap covers two-byte codes, which an
// identity-style embedded CMap does without listing any CID mapping.
func hasIdentityCodespace(c *cmap.CMap) bool {
	codes := c.Codes([]byte{0x00, 0x01})
	return len(codes) == 1 && codes[0].Bytes == 2
}

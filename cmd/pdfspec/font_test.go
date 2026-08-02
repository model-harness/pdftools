package main

import (
	"testing"

	"github.com/3rg0n/pdf-spec/font"
	"github.com/3rg0n/pdf-spec/objects"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
)

// The font package's unit tests prove it handles the dictionary shapes their
// author thought of. These prove it handles the ones the corpus contains, which
// is the part that decides whether text comes out right.
//
// Every count here was measured before it was pinned. A pin invented ahead of the
// measurement asserts an expectation rather than a fact, and when the two differ
// it is the code that gets bent to match.

// corpusFonts lists the files these tests walk. The whole corpus, because a font
// defect that only appears in one document is exactly the kind this package
// exists to catch.
var corpusFonts = []string{
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

// eachCorpusFont calls fn for every distinct font dictionary in every corpus
// file.
//
// Fonts are shared across pages, so each object is visited once: without that a
// 1,023-page document reports the same font thousands of times and every count
// describes page references rather than fonts. Dictionaries written inline rather
// than by reference cannot be deduplicated and are visited per page, which is why
// the counts below are of what the traversal reaches, not of objects in the file.
//
// The walk descends into Form XObject resources, which is not optional. A form
// carries its own /Resources, and the text drawn inside it names fonts from there.
// Measured on this corpus, descending finds 8 fonts that appear nowhere else; the
// AcroForm default resources below add another 28. A traversal that stops at the
// page dictionary silently omits all 36, and in an extractor that means the text
// inside every form comes out undecoded.
func eachCorpusFont(t *testing.T, fn func(s objects.Store, d objects.Dict)) {
	t.Helper()
	for _, file := range corpusFonts {
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
			if res, ok := objects.GetDict(s, page, "Resources"); ok {
				walkResourceFonts(s, res, seen, 0, fn)
			}
		}

		// The AcroForm default resources. A field's /DA string names a font from
		// here rather than from the page, so these are fonts the document draws with
		// and they appear nowhere else. They are also where the standard-14 fonts
		// without /Widths live in this corpus — a form default of "Helv 0 Tf" is the
		// canonical reason a reader needs built-in metrics at all.
		if cat, err := s.Catalog(); err == nil {
			if af, ok := objects.GetDict(s, cat, "AcroForm"); ok {
				if dr, ok := objects.GetDict(s, af, "DR"); ok {
					walkResourceFonts(s, dr, seen, 0, fn)
				}
			}
		}
		s.Close()
	}
}

// walkResourceFonts visits the fonts in a resource dictionary and in the
// resources of any Form XObject it names.
//
// The depth bound is what makes this safe on untrusted input: a form may
// reference itself, directly or through a cycle of forms, and the reference
// deduplication below does not stop that on its own because a cycle of inline
// dictionaries has no reference to deduplicate.
func walkResourceFonts(s objects.Store, res objects.Dict, seen map[objects.Ref]bool, depth int, fn func(objects.Store, objects.Dict)) {
	const maxDepth = 16
	if depth > maxDepth {
		return
	}

	if fonts, ok := objects.GetDict(s, res, "Font"); ok {
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
			if fd, isDict := f.(objects.Dict); isDict {
				fn(s, fd)
			}
		}
	}

	xobjs, ok := objects.GetDict(s, res, "XObject")
	if !ok {
		return
	}
	for _, v := range xobjs {
		if ref, isRef := v.(objects.Ref); isRef {
			if seen[ref] {
				continue
			}
			seen[ref] = true
		}
		xo, err := s.Resolve(v)
		if err != nil {
			continue
		}
		st, isStream := xo.(*objects.Stream)
		if !isStream {
			continue
		}
		if sub, _ := objects.GetName(s, st.Dict, "Subtype"); sub != "Form" {
			continue
		}
		if inner, ok := objects.GetDict(s, st.Dict, "Resources"); ok {
			walkResourceFonts(s, inner, seen, depth+1, fn)
		}
	}
}

func TestCorpusFontsLoad(t *testing.T) {
	// Load never returns an error by design, so what this measures is whether the
	// fonts it produces are usable: a Font that loads and then answers nothing is
	// the failure this catches, and it would otherwise appear far downstream as
	// missing text.
	var (
		total, simple, composite int
		withToUnicode            int
		withWidths               int
		usingStandardMetrics     int
		outOfScopeGlyphSet       int
		noSpaceWidth             int
		vertical                 int
	)
	subtypes := map[string]int{}

	eachCorpusFont(t, func(s objects.Store, d objects.Dict) {
		f := font.Load(s, d)
		total++
		subtypes[f.Subtype]++

		switch f.Kind {
		case font.Simple:
			simple++
		case font.Composite:
			composite++
		}
		if f.HasToUnicode() {
			withToUnicode++
		}
		if f.Vertical {
			vertical++
		}
		if _, ok := objects.GetArray(s, d, "Widths"); ok {
			withWidths++
		} else if f.Kind == font.Simple {
			switch {
			case font.IsStandard(f.BaseFont):
				// The only case where a simple font may legally omit /Widths: it
				// names one of the standard 14 and the reader supplies the metrics.
				// These fonts are the entire reason those tables exist.
				usingStandardMetrics++
			case f.BaseFont == "Symbol" || f.BaseFont == "ZapfDingbats":
				// Also standard-14, but their glyph sets are not WinAnsi's, so
				// neither this package's encoding tables nor its width tables can
				// answer for them. Recorded rather than tolerated silently: it is a
				// real limitation, and the assertion below is what keeps it from
				// growing to cover a font that draws body text.
				outOfScopeGlyphSet++
			default:
				t.Errorf("%s has no /Widths and is not a standard-14 font, so its "+
					"advances are unknown", f.BaseFont)
			}
		}
		if f.SpaceWidth() == 0 {
			noSpaceWidth++
		}
	})

	if total == 0 {
		return // corpus absent; corpusFile already skipped
	}

	t.Logf("%d fonts: %d simple, %d composite, %d vertical; %d with /ToUnicode, "+
		"%d with /Widths, %d on standard-14 metrics, %d with an out-of-scope glyph set, "+
		"%d with no space width; subtypes %s",
		total, simple, composite, vertical, withToUnicode, withWidths,
		usingStandardMetrics, outOfScopeGlyphSet, noSpaceWidth, sortedCounts(subtypes))

	// Pinned so a traversal that stops finding fonts fails rather than passing by
	// scanning nothing.
	if total != 262 {
		t.Errorf("reached %d fonts, want 262; the corpus did not change, so the traversal did", total)
	}
	// Every composite font must carry /ToUnicode: a CID says nothing about
	// characters, so without one its text is unrecoverable from the file and OCR is
	// the only route left. Every one in this corpus does, which is what makes the
	// composite path worth having at all.
	if withToUnicode < composite {
		t.Errorf("%d fonts have /ToUnicode but %d are composite; a composite font "+
			"without one has no recoverable text", withToUnicode, composite)
	}
	// If this reaches zero the standard-14 tables are dead code and the measurement
	// that justified them has changed.
	if usingStandardMetrics != 18 {
		t.Errorf("%d fonts rely on standard-14 metrics, want 18", usingStandardMetrics)
	}
	// All 11 are the "ZaDb" checkbox font in an AcroForm default resource, which
	// draws a checkmark rather than text. Pinned so that if a font whose glyph set
	// this package cannot read ever appears somewhere it matters, the count moves
	// and this fails.
	if outOfScopeGlyphSet != 11 {
		t.Errorf("%d fonts have a glyph set this package cannot read, want 11; if the "+
			"new ones draw text rather than form widgets, that text is lost",
			outOfScopeGlyphSet)
	}
}

func TestCorpusFontsHaveUsableSpaceWidth(t *testing.T) {
	// The space width is the denominator of every inter-word gap decision, so a
	// font without one costs the whole page its word boundaries — which is the
	// documented failure of the existing Go extractors, where "0.01% spaces" means
	// exactly this.
	total, missing := 0, 0
	var names []string

	eachCorpusFont(t, func(s objects.Store, d objects.Dict) {
		f := font.Load(s, d)
		total++
		if f.SpaceWidth() > 0 {
			return
		}
		missing++
		if len(names) < 10 {
			names = append(names, f.BaseFont)
		}
	})

	if total == 0 {
		return
	}
	t.Logf("%d of %d fonts have no space width; first few: %v", missing, total, names)

	// A font with no space glyph at all is legitimate — a symbol font has none —
	// so this is a proportion rather than an absolute. Most of the corpus must be
	// measurable or space inference has nothing to work with.
	if limit := total / 10; missing > limit {
		t.Errorf("%d of %d fonts report no space width, over the %d tolerated; "+
			"space inference has no threshold for those pages", missing, total, limit)
	}
}

func TestCorpusCompositeWidthsAreDeclared(t *testing.T) {
	// A composite font falling through to /DW for most of its CIDs would mean /W
	// was misparsed: producers declare real widths for the glyphs they subset, and
	// /DW is the fallback for the ones they do not use. Every CID the page actually
	// draws should therefore have a declared width.
	//
	// This is the assertion that would have caught a parseW that read only one of
	// the two entry forms — the defect the corpus survey found both forms mixed
	// inside single arrays to warn about.
	fonts, withW, declared, total := 0, 0, 0, 0

	eachCorpusFont(t, func(s objects.Store, d objects.Dict) {
		f := font.Load(s, d)
		if f.Kind != font.Composite {
			return
		}
		fonts++

		desc, ok := objects.GetArray(s, d, "DescendantFonts")
		if !ok || len(desc) == 0 {
			return
		}
		dv, err := s.Resolve(desc[0])
		if err != nil {
			return
		}
		dd, isDict := dv.(objects.Dict)
		if !isDict {
			return
		}
		arr, ok := objects.GetArray(s, dd, "W")
		if !ok {
			return
		}
		withW++

		// Walk the CIDs /W names and confirm each one comes back with a width that
		// is not the default. Reading the array here independently of parseW is the
		// point: agreement between two readings of the same bytes is evidence,
		// where parseW checking itself would not be.
		for _, cid := range cidsNamedByW(s, arr) {
			total++
			if w := f.Width(cid, cid); w != 1000 {
				declared++
			}
		}
	})

	if fonts == 0 {
		return
	}
	t.Logf("%d composite fonts, %d with /W; %d of %d CIDs named by /W resolve to a "+
		"non-default width", fonts, withW, declared, total)

	if fonts != 96 || withW != 96 {
		t.Errorf("%d composite fonts and %d with /W, want 96 and 96", fonts, withW)
	}
	// Not all of them: a /W entry may legitimately declare 1000, which is
	// indistinguishable from the default here. Most must resolve, or entries are
	// being dropped.
	if limit := total * 9 / 10; declared < limit {
		t.Errorf("only %d of %d CIDs named by /W resolve to a non-default width, under "+
			"the %d expected; /W entries are likely being dropped, and a dropped width "+
			"is a misplaced glyph", declared, total, limit)
	}
}

// cidsNamedByW lists the CIDs a /W array assigns widths to, reading the array
// independently of parseW so the two can be compared.
func cidsNamedByW(s objects.Store, arr objects.Array) []uint32 {
	var out []uint32
	for i := 0; i < len(arr); {
		first, ok := resolveInt(s, arr[i])
		if !ok {
			i++
			continue
		}
		i++
		if i >= len(arr) {
			break
		}
		next, err := s.Resolve(arr[i])
		if err != nil {
			i++
			continue
		}
		if items, isArray := next.(objects.Array); isArray {
			i++
			for j := range items {
				out = append(out, uint32(first)+uint32(j))
			}
			continue
		}
		last, ok := objects.AsNum(next)
		if !ok {
			i++
			continue
		}
		i += 2 // the high bound and the width
		for cid := first; cid <= int64(last) && cid-first < 1<<16; cid++ {
			out = append(out, uint32(cid))
		}
	}
	return out
}

func resolveInt(s objects.Store, o objects.Object) (int64, bool) {
	v, err := s.Resolve(o)
	if err != nil {
		return 0, false
	}
	n, ok := objects.AsNum(v)
	if !ok || n < 0 {
		return 0, false
	}
	return int64(n), true
}

func TestCorpusSimpleFontsDecodeToText(t *testing.T) {
	// Every code a simple font's encoding names must resolve to text. The name is
	// the encoding's claim that the code draws a glyph; if the glyph list then
	// resolves it to nothing, that character disappears from the extracted text
	// with no error anywhere, which is the defect this whole package is built
	// against.
	//
	// The test keys on the glyph name rather than on the width, because a nonzero
	// width does not mean a code is drawn. Two fonts in this corpus declare
	// /FirstChar 0 /LastChar 255 and pad all 256 entries — one of them writing 658
	// for every C0 control — so a width-driven test asks the C0 controls and the
	// Windows-1252 holes for text they were never going to have. Those codes are
	// unassigned in Annex D on purpose, and the encoding correctly names nothing
	// for them.
	fonts, named, resolved := 0, 0, 0
	unresolvedBy := map[string]int{}

	eachCorpusFont(t, func(s objects.Store, d objects.Dict) {
		f := font.Load(s, d)
		if f.Kind != font.Simple {
			return
		}
		if _, ok := objects.GetArray(s, d, "Widths"); !ok {
			return
		}
		fonts++

		for code := 0; code <= 0xFF; code++ {
			if f.GlyphName(byte(code)) == "" {
				continue
			}
			named++
			if f.Text(uint32(code)) != "" {
				resolved++
			} else {
				unresolvedBy[f.BaseFont]++
			}
		}
	})

	if fonts == 0 {
		return
	}
	t.Logf("%d simple fonts with /Widths; %d of %d encoded codes resolve to text",
		fonts, resolved, named)

	if len(unresolvedBy) > 0 {
		t.Errorf("codes with a glyph name but no text, by font: %s\n"+
			"each is a character that disappears from extracted text with no error anywhere",
			sortedCounts(unresolvedBy))
	}
	if fonts != 137 {
		t.Errorf("reached %d simple fonts with /Widths, want 137", fonts)
	}
}

func TestCorpusPaddedWidthsAreNotMistakenForDrawnCodes(t *testing.T) {
	// The padding above is worth pinning rather than only commenting, because it is
	// the reason a plausible test design is wrong. Producers write /FirstChar 0
	// /LastChar 255 and fill every entry, so a width says nothing about whether the
	// document draws that code — and any later check that infers "drawn" from
	// "nonzero width" will ask the C0 controls for text and report a defect that is
	// not there.
	padded, controlsWithWidth := 0, 0

	eachCorpusFont(t, func(s objects.Store, d objects.Dict) {
		f := font.Load(s, d)
		if f.Kind != font.Simple {
			return
		}
		arr, ok := objects.GetArray(s, d, "Widths")
		if !ok || len(arr) != 256 {
			return
		}
		firstChar, _ := objects.GetInt(s, d, "FirstChar")
		if firstChar != 0 {
			return
		}
		padded++
		for code := uint32(0); code < 0x20; code++ {
			if f.Width(code, code) != 0 {
				controlsWithWidth++
			}
		}
	})

	if padded == 0 {
		return
	}
	t.Logf("%d simple fonts declare all 256 codes; %d C0 control codes carry a "+
		"nonzero width", padded, controlsWithWidth)

	if padded != 3 || controlsWithWidth != 64 {
		t.Errorf("%d fonts declare all 256 codes with %d C0 controls carrying a width, "+
			"want 3 and 64", padded, controlsWithWidth)
	}
}

func TestCorpusDifferencesCount(t *testing.T) {
	// This exists to settle a discrepancy: the font survey counted 11 /Differences
	// arrays where TestCorpusDifferencesResolve pins 4. Both walk page resources
	// and both deduplicate by font reference, so the two cannot both be counting
	// the same thing.
	//
	// The answer is what "one array" means. Several fonts share a single /Encoding
	// object, so counting per font over-counts the arrays; and a font dictionary
	// written inline rather than by reference cannot be deduplicated at all, so it
	// is counted once per page that draws it. Both numbers are recorded here, each
	// under the name of what it actually measures, so neither can be mistaken for
	// the other again.
	perFont, distinctArrays, inlineFonts := 0, 0, 0
	seenEncoding := map[objects.Ref]bool{}

	eachCorpusFont(t, func(s objects.Store, d objects.Dict) {
		enc, ok := objects.Get(s, d, "Encoding")
		if !ok {
			return
		}
		if _, isDict := enc.(objects.Dict); !isDict {
			return
		}
		encDict := enc.(objects.Dict)
		if _, ok := objects.GetArray(s, encDict, "Differences"); !ok {
			return
		}
		perFont++

		// An /Encoding written by reference can be deduplicated; one written inline
		// cannot be told apart from another with the same contents.
		if ref, isRef := d["Encoding"].(objects.Ref); isRef {
			if !seenEncoding[ref] {
				seenEncoding[ref] = true
				distinctArrays++
			}
			return
		}
		inlineFonts++
		distinctArrays++
	})

	if perFont == 0 {
		return
	}
	t.Logf("%d fonts carry /Differences, over %d distinct /Encoding objects "+
		"(%d written inline)", perFont, distinctArrays, inlineFonts)

	if perFont != 17 || distinctArrays != 14 {
		t.Errorf("%d fonts carry /Differences over %d distinct /Encoding objects, "+
			"want 17 over 14", perFont, distinctArrays)
	}
}

// Package font reads font dictionaries and answers the two questions text
// extraction asks of a font: what does this character code mean, and how far does
// it advance the text position (ISO 32000-2 §9.5 through §9.7).
//
// Those two questions are why this package exists as a unit. They are usually
// treated separately — a decoder here, a metrics table there — and separating
// them is how extractors end up with correct characters in the wrong order, or
// with the 4,069-character "word" that a single dropped advance produces. Both
// answers come from the same dictionary and both must agree about which code is
// being discussed, so both live behind one Font.
//
// A Font is read once per font dictionary and used for every glyph on every page
// that references it, so the expensive work — parsing a /ToUnicode CMap, walking a
// /W array, applying /Differences — happens in Load and never again.
//
// This package resolves codes to text and widths. It does not position glyphs:
// composing the text matrix and inferring inter-word spaces from the gap between
// advances belongs to extract, because those depend on state this package cannot
// see. What this package guarantees is that the advance it reports is the one the
// font actually declares, which is the input that decision needs.
package font

import (
	"strings"

	"github.com/model-harness/pdftools/font/cmap"
	"github.com/model-harness/pdftools/font/encoding"
	"github.com/model-harness/pdftools/objects"
)

// Kind distinguishes the two ways a font addresses glyphs, which is the single
// most consequential fact about it: a simple font takes one byte per glyph, a
// composite font takes a variable number decided by its CMap. Assuming the wrong
// one does not produce an error, it produces a plausible-looking stream of wrong
// glyphs.
type Kind int

const (
	// Simple is a Type1, TrueType, Type3, or MMType1 font: single-byte codes
	// resolved through an encoding.
	Simple Kind = iota

	// Composite is a Type0 font: codes of one to four bytes resolved through a
	// CMap to CIDs.
	Composite
)

// Font is a loaded font dictionary, ready to decode codes and report advances.
//
// Its fields are read-only after Load. Nothing here holds a Store, so a Font
// outlives the document handle it was read from and is safe to share across
// goroutines.
type Font struct {
	// Kind decides how Decode splits bytes into codes.
	Kind Kind

	// BaseFont is the /BaseFont name, with any subset prefix intact. Extraction
	// reports it, and the standard-14 metrics match against it.
	BaseFont string

	// Subtype is the font dictionary's /Subtype.
	Subtype string

	// Vertical reports a vertical writing mode, from a CMap name ending in -V.
	// Advances then apply to the y axis, which is the difference between reading
	// a vertical Japanese document and stacking every glyph at one point.
	Vertical bool

	// enc resolves single-byte codes to text, for simple fonts.
	enc *encoding.Encoding

	// cmap splits codes and maps them to CIDs, for composite fonts.
	cmap *cmap.CMap

	// toUnicode maps codes to text, for either kind. It takes precedence over enc
	// because it is the font's own statement about what its codes mean, while an
	// encoding is this package's inference from a name.
	toUnicode *cmap.CMap

	// widths holds simple-font advances indexed from firstChar.
	widths    []float64
	firstChar int

	// cidWidths holds composite-font advances by CID, from /W. A map rather than a
	// slice because /W is sparse: a CID-keyed font may declare widths for a few
	// hundred CIDs out of 65,536.
	cidWidths map[uint32]float64

	// defaultWidth is /DW for a composite font, or /MissingWidth for a simple one.
	// The specification's defaults differ — 1000 for composite, 0 for simple — and
	// using one for the other is a visible layout error.
	defaultWidth float64

	// cidToGID is the /CIDToGIDMap stream, when one is present. Held because glyph
	// lookup in an embedded program needs it; not used for advances.
	cidToGID []byte

	// spaceWidth caches the advance of the space glyph, which extract needs for
	// every inter-word gap decision on the page.
	spaceWidth float64

	// bold, italic, and mono are the typographic identity a consumer needs to emit
	// emphasis or recognize a code span. They are derived at load time from the
	// descriptor and the name together, because the two disagree often enough that
	// either alone loses cases: see traits.
	bold   bool
	italic bool
	mono   bool
}

// Name returns the /BaseFont name with any subset prefix stripped.
//
// The prefix is stripped because it identifies the subset, not the typeface: the
// same font subset twice in one document gets two different prefixes, and a
// consumer grouping runs by font name would treat them as unrelated and break a
// paragraph at every subset boundary. BaseFont keeps the prefix for callers that
// want the name exactly as written.
func (f *Font) Name() string { return stripSubsetPrefix(f.BaseFont) }

// Bold, Italic, and Monospaced report the font's typographic traits.
func (f *Font) Bold() bool       { return f.bold }
func (f *Font) Italic() bool     { return f.italic }
func (f *Font) Monospaced() bool { return f.mono }

// traits derives bold, italic, and monospaced from the descriptor and the name.
//
// Both sources are consulted because each misses cases the other catches. The
// descriptor is authoritative when correct — /Flags bit 1 for fixed pitch, bit 7
// for italic, and /StemV or /FontWeight for weight — but producers routinely omit
// the weight and set no italic flag on a font whose name says "BoldItalic". Taking
// the name as corroborating evidence recovers those; taking it alone would fail on
// every subset font named by its foundry rather than its style.
//
// A composite font's traits live on the CIDFont's descriptor, not the Type0
// dictionary, which is why the descriptor is passed in rather than read here.
func (f *Font) traits(s objects.Store, fd objects.Dict) {
	name := strings.ToLower(stripSubsetPrefix(f.BaseFont))

	if fd != nil {
		flags, _ := objects.GetInt(s, fd, "Flags")
		f.mono = flags&(1<<0) != 0
		f.italic = flags&(1<<6) != 0

		// /FontWeight is the direct statement; 600 is the conventional threshold, and
		// semibold at 600 is bold enough for emphasis. /StemV is the fallback that
		// many producers write instead: a stem above 120 thousandths of an em is a
		// bold weight at text sizes.
		if w, ok := objects.GetNum(s, fd, "FontWeight"); ok && w >= 600 {
			f.bold = true
		} else if v, ok := objects.GetNum(s, fd, "StemV"); ok && v > 120 {
			f.bold = true
		}
		// /ItalicAngle is nonzero for an oblique face whose flag is unset, which is
		// the common producer omission.
		if a, ok := objects.GetNum(s, fd, "ItalicAngle"); ok && a != 0 {
			f.italic = true
		}
	}

	if strings.Contains(name, "bold") || strings.Contains(name, "black") ||
		strings.Contains(name, "heavy") || strings.Contains(name, "semibold") {
		f.bold = true
	}
	if strings.Contains(name, "italic") || strings.Contains(name, "oblique") {
		f.italic = true
	}
	if strings.Contains(name, "mono") || strings.Contains(name, "courier") ||
		strings.Contains(name, "consolas") {
		f.mono = true
	}
}

// Load reads a font dictionary.
//
// It never fails on a defective dictionary. A font with no /Widths, no
// /ToUnicode, and an unrecognized /Encoding still yields a usable Font: codes
// decode to nothing and advances fall back to the default, which loses text but
// keeps the rest of the page. Returning an error instead would abandon a whole
// document over one bad font, and defective font dictionaries are common in
// exactly the files that most need extracting.
func Load(s objects.Store, d objects.Dict) *Font {
	f := &Font{}
	if n, ok := objects.GetName(s, d, "BaseFont"); ok {
		f.BaseFont = string(n)
	}
	if n, ok := objects.GetName(s, d, "Subtype"); ok {
		f.Subtype = string(n)
	}

	// /ToUnicode applies to both kinds and is read first, so the branches below
	// only have to handle what is specific to them.
	if data, ok := objects.GetStreamData(s, d, "ToUnicode"); ok {
		if c, err := cmap.Parse(data); err == nil {
			if _, texts := c.Entries(); texts > 0 {
				f.toUnicode = c
			}
		}
	}

	if f.Subtype == "Type0" {
		f.Kind = Composite
		f.loadComposite(s, d)
	} else {
		f.Kind = Simple
		f.loadSimple(s, d)
	}

	f.spaceWidth = f.measureSpace()
	return f
}

// loadSimple reads the parts specific to a single-byte font: /Encoding with its
// /Differences, and /Widths with /FirstChar.
func (f *Font) loadSimple(s objects.Store, d objects.Dict) {
	f.enc = f.baseEncoding(s, d)
	f.applyDifferences(s, d)

	f.firstChar = int(getInt(s, d, "FirstChar", 0))
	if arr, ok := objects.GetArray(s, d, "Widths"); ok && len(arr) > 0 {
		f.widths = make([]float64, 0, len(arr))
		for _, v := range arr {
			r, err := s.Resolve(v)
			if err != nil {
				f.widths = append(f.widths, 0)
				continue
			}
			w, _ := objects.AsNum(r)
			f.widths = append(f.widths, w)
		}
	}

	// /MissingWidth defaults to 0 per Table 122, which means a code outside
	// /Widths does not advance at all. That is the specification's answer and it
	// is usually right: the codes outside the range are typically unused.
	fd, _ := objects.GetDict(s, d, "FontDescriptor")
	if fd != nil {
		f.defaultWidth, _ = objects.GetNum(s, fd, "MissingWidth")
	}
	f.traits(s, fd)
}

// baseEncoding decides which base encoding a simple font's codes start from,
// before /Differences.
//
// The rule in §9.6.5.1 is conditional on the descriptor's symbolic flag, and the
// condition matters: a symbolic font's codes mean whatever its built-in encoding
// says, which is inside the font program and not visible here. Assuming
// StandardEncoding for those produces confident wrong characters, so they start
// empty and rely on /Differences or /ToUnicode instead.
func (f *Font) baseEncoding(s objects.Store, d objects.Dict) *encoding.Encoding {
	named := ""
	if enc, ok := objects.Get(s, d, "Encoding"); ok {
		switch e := enc.(type) {
		case objects.Name:
			named = string(e)
		case objects.Dict:
			if b, ok := objects.GetName(s, e, "BaseEncoding"); ok {
				named = string(b)
			}
		}
	}
	if named != "" {
		if base, ok := encoding.Base(named); ok {
			return base
		}
		// A named encoding this package does not carry — MacExpertEncoding is the
		// realistic case. StandardEncoding is wrong for its glyphs but right for
		// the ASCII range they share, which recovers most of the text instead of
		// none.
		return encoding.Standard()
	}

	// No encoding named: the symbolic flag decides.
	if f.symbolic(s, d) {
		return encoding.Empty()
	}
	return encoding.Standard()
}

// symbolic reports the descriptor's symbolic flag (bit 3 of /Flags, Table 123).
//
// A font with no descriptor is one of the standard 14, which are non-symbolic
// except Symbol and ZapfDingbats — and those two are symbolic in the sense that
// matters here, since their codes mean what their built-in encodings say.
func (f *Font) symbolic(s objects.Store, d objects.Dict) bool {
	fd, ok := objects.GetDict(s, d, "FontDescriptor")
	if !ok {
		base := stripSubsetPrefix(f.BaseFont)
		return base == "Symbol" || base == "ZapfDingbats"
	}
	flags, _ := objects.GetInt(s, fd, "Flags")
	return flags&(1<<2) != 0
}

// applyDifferences overlays an /Encoding dictionary's /Differences array.
//
// The array is a sequence of runs: a starting code followed by the glyph names
// for consecutive codes from there (§9.6.5.1). A name before any number has no
// code to attach to and is skipped.
func (f *Font) applyDifferences(s objects.Store, d objects.Dict) {
	encDict, ok := objects.GetDict(s, d, "Encoding")
	if !ok {
		return
	}
	arr, ok := objects.GetArray(s, encDict, "Differences")
	if !ok {
		return
	}
	// Cloned so a shared base table is never mutated. Base already returns a copy,
	// but Empty and Standard may not, and a /Differences array writing through to
	// a package-level table would corrupt every other font in the document.
	f.enc = f.enc.Clone()

	code := -1
	for _, item := range arr {
		v, err := s.Resolve(item)
		if err != nil {
			continue
		}
		if n, ok := objects.AsNum(v); ok {
			// A code outside 0..255 cannot be assigned. Setting code to -1 rather
			// than clamping means the names that follow are skipped instead of
			// landing on the wrong byte.
			if n < 0 || n > 255 {
				code = -1
				continue
			}
			code = int(n)
			continue
		}
		name, ok := v.(objects.Name)
		if !ok || code < 0 {
			continue
		}
		f.enc.Set(byte(code), string(name))
		if code == 255 {
			// Further names would run past the encoding.
			code = -1
			continue
		}
		code++
	}
}

// loadComposite reads a Type0 font: its encoding CMap, then the CIDFont in
// /DescendantFonts that carries the metrics.
func (f *Font) loadComposite(s objects.Store, d objects.Dict) {
	f.cmap = f.encodingCMap(s, d)
	f.Vertical = strings.HasSuffix(f.cmap.Name, "-V")

	// /DW defaults to 1000, not 0 (Table 114). The difference is the whole width
	// of a glyph, applied to every CID the font does not list in /W.
	f.defaultWidth = 1000

	desc, ok := objects.GetArray(s, d, "DescendantFonts")
	if !ok || len(desc) == 0 {
		// No CIDFont: nothing declares traits, and the name is the only evidence
		// left. Calling traits with a nil descriptor is what makes the name-based
		// half apply on its own.
		f.traits(s, nil)
		return
	}
	dv, err := s.Resolve(desc[0])
	if err != nil {
		f.traits(s, nil)
		return
	}
	dd, ok := dv.(objects.Dict)
	if !ok {
		f.traits(s, nil)
		return
	}
	fd, _ := objects.GetDict(s, dd, "FontDescriptor")
	f.traits(s, fd)
	if dw, ok := objects.GetNum(s, dd, "DW"); ok {
		f.defaultWidth = dw
	}
	if arr, ok := objects.GetArray(s, dd, "W"); ok {
		f.cidWidths = parseW(s, arr)
	}
	// /CIDToGIDMap is a name or a stream. Identity is the only name defined, and
	// is what all 67 CIDFontType2 fonts in this repo's corpus use, so the stream
	// form is read but nothing here depends on it yet.
	if data, ok := objects.GetStreamData(s, dd, "CIDToGIDMap"); ok {
		f.cidToGID = data
	}
}

// encodingCMap resolves a Type0 font's /Encoding, which is either a predefined
// CMap name or an embedded CMap stream (§9.7.5.2).
//
// Never nil: a composite font with no readable encoding still has to split its
// string into codes, and two-byte codes are right for every predefined CMap that
// matters. Guessing one byte instead would double the glyph count and produce
// text that looks like interleaved garbage.
func (f *Font) encodingCMap(s objects.Store, d objects.Dict) *cmap.CMap {
	enc, ok := objects.Get(s, d, "Encoding")
	if !ok {
		return cmap.TwoByte("")
	}
	switch e := enc.(type) {
	case objects.Name:
		if c, ok := cmap.Identity(string(e)); ok {
			return c
		}
		return cmap.TwoByte(string(e))
	case *objects.Stream:
		data, ok := objects.GetStreamData(s, objects.Dict{"S": e}, "S")
		if !ok {
			return cmap.TwoByte("")
		}
		c, err := cmap.Parse(data)
		if err != nil {
			return cmap.TwoByte("")
		}
		return c
	}
	return cmap.TwoByte("")
}

// parseW reads a /W array into a CID-to-width map (§9.7.4.3).
//
// Two entry forms interleave freely in one array: "c [w1 w2 ...]" gives widths to
// consecutive CIDs from c, and "cFirst cLast w" gives one width to a whole range.
// The corpus survey found both forms mixed inside single arrays — shapes running
// N A N A N N N A — so the parser dispatches per entry on what it finds rather
// than deciding a form for the array. Reading it either way alone silently drops
// every entry of the other kind, and a dropped width is a misplaced glyph.
func parseW(s objects.Store, arr objects.Array) map[uint32]float64 {
	// A /W array wide enough to exhaust memory is a hostile document rather than a
	// real one; the bound is far above any real font's CID count.
	const maxCIDWidths = 1 << 20
	// A single range entry may legally span a large stretch of CIDs, but a range
	// covering the entire two-byte space is a claim no real font makes.
	const maxRangeSpan = 1 << 16

	out := map[uint32]float64{}
	for i := 0; i < len(arr); {
		first, ok := resolveNum(s, arr[i])
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
			cid := uint32(first)
			for _, it := range items {
				w, ok := resolveNum(s, it)
				if !ok {
					cid++
					continue
				}
				if len(out) >= maxCIDWidths {
					return out
				}
				out[cid] = w
				cid++
			}
			continue
		}

		// Range form: first, last, width.
		last, ok := objects.AsNum(next)
		if !ok {
			i++
			continue
		}
		i++
		if i >= len(arr) {
			break
		}
		w, ok := resolveNum(s, arr[i])
		i++
		if !ok || last < first || first < 0 {
			continue
		}
		if last-first >= maxRangeSpan {
			continue
		}
		for cid := uint32(first); cid <= uint32(last); cid++ {
			if len(out) >= maxCIDWidths {
				return out
			}
			out[cid] = w
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveNum resolves an object and reads it as a number.
func resolveNum(s objects.Store, o objects.Object) (float64, bool) {
	v, err := s.Resolve(o)
	if err != nil {
		return 0, false
	}
	return objects.AsNum(v)
}

func getInt(s objects.Store, d objects.Dict, key objects.Name, def int64) int64 {
	if v, ok := objects.GetInt(s, d, key); ok {
		return v
	}
	return def
}

// stripSubsetPrefix removes the "ABCDEF+" tag a subsetting tool prepends to
// /BaseFont (§9.6.4). The prefix is exactly six uppercase letters and a plus.
func stripSubsetPrefix(name string) string {
	if len(name) < 8 || name[6] != '+' {
		return name
	}
	for i := 0; i < 6; i++ {
		if name[i] < 'A' || name[i] > 'Z' {
			return name
		}
	}
	return name[7:]
}

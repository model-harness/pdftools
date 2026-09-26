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

	// baseName is the base encoding a simple font names, in /Encoding or its /BaseEncoding, as
	// written, and baseKnown is whether this package carries that table. differed marks the
	// codes /Differences assigns. Selecting a glyph from an embedded program needs to tell the
	// names the font stated from the ones enc inferred, and these are what tell them apart.
	baseName  string
	baseKnown bool
	differed  [4]uint64

	// hasDifferences is whether the /Encoding dictionary has a /Differences array, whether or not
	// it assigns any code. See HasDifferences.
	hasDifferences bool

	// encoded is whether /Encoding is a name or a dictionary. §9.6.5.4 gives a TrueType font with
	// neither a route of its own, so "no encoding" and "an encoding naming no base" differ.
	encoded bool

	// irregular names the first entry of the dictionary that Load read one way and pdfium reads
	// another, or "" when there is none. See Irregular.
	irregular string

	// flaggedSymbolic and flaggedNonsymbolic are what flags reports for a simple font, read once.
	flaggedSymbolic, flaggedNonsymbolic bool

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

	// serif is the fourth axis, and it exists for substitution rather than for
	// emphasis: choosing a face to stand in for a font with no embedded program
	// needs to know whether the original had serifs, and nothing else in this
	// package needed to. Derived the same way as the others, from /Flags bit 2
	// and the name together.
	serif bool
}

// Name returns the /BaseFont name with any subset prefix stripped.
//
// The prefix is stripped because it identifies the subset, not the typeface: the
// same font subset twice in one document gets two different prefixes, and a
// consumer grouping runs by font name would treat them as unrelated and break a
// paragraph at every subset boundary. BaseFont keeps the prefix for callers that
// want the name exactly as written.
func (f *Font) Name() string { return stripSubsetPrefix(f.BaseFont) }

// StatedGlyphName returns the glyph name a simple font's encoding states for code: the one
// /Differences assigns, or the one in the base encoding the font names when this package carries
// that table.
//
// It differs from GlyphName where the name is this package's inference rather than the font's
// statement — the StandardEncoding assumed for a font that names no base, most of all — and is
// empty there. Text extraction can live with that guess; selecting a glyph by it draws the wrong
// glyph wherever the program's built-in encoding differs, so a renderer has to know which it has.
func (f *Font) StatedGlyphName(code byte) string {
	if f.enc == nil || (!f.baseKnown && f.differed[code/64]&(1<<(code%64)) == 0) {
		return ""
	}
	return f.enc.Glyph(code)
}

// BaseEncoding returns the base encoding a simple font names, as written, or "" when it names
// none.
func (f *Font) BaseEncoding() string { return f.baseName }

// Symbolic reports a simple font's symbolic flag: bit 3 of the descriptor's /Flags, or for a font
// with no descriptor, whether it is Symbol or ZapfDingbats.
func (f *Font) Symbolic() bool { return f.flaggedSymbolic }

// Nonsymbolic reports a simple font's nonsymbolic flag: bit 6 of the descriptor's /Flags, set when
// /Flags is absent, or for a font with no descriptor, whether it is not Symbol or ZapfDingbats.
func (f *Font) Nonsymbolic() bool { return f.flaggedNonsymbolic }

// HasEncoding reports whether a simple font's /Encoding is a name or a dictionary.
func (f *Font) HasEncoding() bool { return f.encoded }

// HasDifferences reports whether a simple font's /Encoding dictionary has a /Differences array,
// whether or not it assigns any code.
//
// Assignment used to be the test, and it is wrong for §9.6.5.4's TrueType route (C21):
// CPDF_SimpleFont::LoadDifferences (cpdf_simplefont.cpp) resizes its char_names_ table to 256 for
// any /Differences array before reading it, and cpdf_truetypefont.cpp's by-name test reads
// char_names_.empty() — so an array that assigns nothing still moves pdfium off the by-code route
// this package would otherwise take. ttRoute reads this method to refuse that font instead of
// drawing it by the wrong route.
func (f *Font) HasDifferences() bool { return f.hasDifferences }

// Irregular names the first entry of a simple font's dictionaries that pdfium reads under a
// looser type or a wider range than this package does, or "" when there is none.
//
// Load stays lenient regardless: this only records the entry, and never fails Load over it. It
// exists because render/native's survey has to refuse a page wherever the two readers would
// select a different glyph, and a value pdfium reads that this package's stricter getters silently
// drop is exactly that case — the getters answer "what did the font state", not "did this and
// pdfium state the same thing".
func (f *Font) Irregular() string { return f.irregular }

// Bold, Italic, Monospaced, and Serif report the font's typographic traits.
func (f *Font) Bold() bool       { return f.bold }
func (f *Font) Italic() bool     { return f.italic }
func (f *Font) Monospaced() bool { return f.mono }
func (f *Font) Serif() bool      { return f.serif }

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
		f.serif = flags&(1<<1) != 0
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

	// Serif from the name, in both directions, because /Flags bit 2 is the one
	// producers most often leave at zero: 0 means "not stated" far more often than
	// it means "sans", so a name that says otherwise overrides it either way. The
	// sans list is checked second so that a face named for both — there is no such
	// face, but a subset prefix can put any letters in front — resolves to sans.
	for _, n := range []string{"times", "serif", "roman", "georgia", "garamond",
		"book", "minion", "cambria", "palatino", "century", "baskerville"} {
		if strings.Contains(name, n) {
			f.serif = true
		}
	}
	for _, n := range []string{"arial", "helvetica", "verdana", "tahoma", "calibri",
		"segoe", "futura", "gothic", "grotesk", "sansserif", "sans-serif"} {
		if strings.Contains(name, n) {
			f.serif = false
		}
	}
	// "sans" last and on its own, since it appears inside "sansserif" above and a
	// name containing it is never a serif face.
	if strings.Contains(name, "sans") {
		f.serif = false
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
	f.irregular = f.checkIrregular(s, d)
	f.flaggedSymbolic, f.flaggedNonsymbolic = f.flags(s, d)
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
	f.scaleType3(s, d)
}

// scaleType3 converts a Type 3 font's advances into the 1/1000 units every other
// font reports, so that Width means one thing to its callers.
//
// Type 3 is the only font kind whose glyph space is not fixed. §9.6.4 says its
// /Widths are in glyph space and that /FontMatrix maps glyph space to text space,
// where every other kind has that mapping fixed at 1/1000. The matrix is not
// decoration: pdfTeX writes [0.00836 0 0 0.00836 0 0] with widths near 60, and
// 60/1000 is 8.36 times smaller than the 60*0.00836 the file means.
//
// Measured on a pdfTeX document whose content stream states the answer: the five
// glyphs of "First" sum to 275.64 at /FontMatrix 0.00836 and 14.3462pt, which is
// 33.06 units of text space, and the stream's own Td moves 38.32 to clear the word
// and the space after it. Read as 1/1000 the same run advances 3.95, so the pen
// falls a word-width behind on every word — and since the space inference in
// extract compares measured gaps against these advances, a gap that is really one
// space reads as an enormous one. Every Type 3 run became its own text block.
//
// Only the horizontal scale is taken. A /FontMatrix may rotate or skew, and a
// caller wanting the glyph's shape needs the whole matrix — but Width answers how
// far the pen moves, which is the matrix applied to (w, 0), and the b component of
// that is a vertical displacement this field cannot express. Fonts that rotate
// their glyph space are rare enough that reporting the horizontal component is
// right for the corpus and honest about what it is.
func (f *Font) scaleType3(s objects.Store, d objects.Dict) {
	if f.Subtype != "Type3" {
		return
	}
	m, ok := objects.GetArray(s, d, "FontMatrix")
	if !ok || len(m) != 6 {
		// §9.6.4 requires /FontMatrix in a Type 3 font, so a missing one is a
		// defective dictionary. The specification's own default for the entry is
		// [0.001 0 0 0.001 0 0], which is exactly the 1/1000 convention already
		// assumed, so leaving the widths alone is both the documented default and
		// the no-op.
		//
		// Exactly six, not at least one: a matrix is an affine transform written
		// [a b c d e f], and a shorter array is not a matrix with a readable first
		// element. Scaling every advance by a number taken from a malformed array is
		// worse than declining to, because it is silent and it is confident.
		return
	}
	a, _ := objects.AsNum(mustResolve(s, m[0]))
	if a == 0 {
		// A zero horizontal scale collapses every advance to nothing, which would
		// stack a page of glyphs at one point. More likely a malformed matrix than a
		// font that means it, and the 1/1000 assumption at least keeps the run
		// legible.
		return
	}
	// Into 1/1000 units: a width of w means w*a text-space units, and the callers
	// divide by 1000, so w*a*1000 is the width that survives that division.
	scale := a * 1000
	for i := range f.widths {
		f.widths[i] *= scale
	}
	f.defaultWidth *= scale
}

// mustResolve resolves a reference, returning the unresolved object on failure.
// The callers ask for a number and treat a non-number as absent, so a failed
// resolution needs no separate branch.
func mustResolve(s objects.Store, o objects.Object) objects.Object {
	r, err := s.Resolve(o)
	if err != nil {
		return o
	}
	return r
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
			f.encoded = true
			named = string(e)
		case objects.Dict:
			f.encoded = true
			if b, ok := objects.GetName(s, e, "BaseEncoding"); ok {
				named = string(b)
			}
		}
	}
	if named != "" {
		f.baseName = named
		if base, ok := encoding.Base(named); ok {
			f.baseKnown = true
			return base
		}
		// A named encoding this package does not carry — MacExpertEncoding is the
		// realistic case. StandardEncoding is wrong for its glyphs but right for
		// the ASCII range they share, which recovers most of the text instead of
		// none.
		return encoding.Standard()
	}

	// No encoding named: the symbolic flag decides.
	if f.flaggedSymbolic {
		return encoding.Empty()
	}
	return encoding.Standard()
}

// flags reads the descriptor's symbolic and nonsymbolic flags (bits 3 and 6 of /Flags, Table 121).
//
// A descriptor with no /Flags reads as nonsymbolic, which is pdfium's default. Table 121 requires
// the entry, so what its absence means is a reader's choice, and the choice matters to a TrueType
// font with no /Encoding: the nonsymbolic flag is what makes §9.6.5.4's two routes both apply.
//
// A font with no descriptor is one of the standard 14, which are non-symbolic
// except Symbol and ZapfDingbats — and those two are symbolic in the sense that
// matters here, since their codes mean what their built-in encodings say.
func (f *Font) flags(s objects.Store, d objects.Dict) (symbolic, nonsymbolic bool) {
	fd, ok := objects.GetDict(s, d, "FontDescriptor")
	if !ok {
		base := stripSubsetPrefix(f.BaseFont)
		sym := base == "Symbol" || base == "ZapfDingbats"
		return sym, !sym
	}
	flags, ok := objects.GetInt(s, fd, "Flags")
	if !ok {
		if _, present := fd["Flags"]; present {
			// C21 / R#2: present but not a number GetInt accepts — a string, a name, an array,
			// null, or a reference to nothing. pdfium's GetIntegerFor (cpdf_font.cpp) reads a
			// present key as 0 in every one of those cases; its default argument applies only
			// when the key itself is absent. The raw map is read here rather than objects.Get,
			// because Get also treats a null value and a dangling reference as absent (ISO
			// 32000-2 §7.3.9, §7.3.10) and would push both into the branch below meant for a
			// key that is not there at all.
			flags = 0
		} else {
			// Absent: Table 121 requires the entry, so what it means is the reader's own choice,
			// and GetIntegerFor's default argument here is kFontStyleNonSymbolic.
			flags = 1 << 5
		}
	}
	return flags&(1<<2) != 0, flags&(1<<5) != 0
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
	// See HasDifferences: pdfium treats the array's presence, not what it assigns, as what moves
	// a TrueType font off the by-code route, so this is set here regardless of what the loop below
	// finds.
	f.hasDifferences = true

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
		f.differed[code/64] |= 1 << (code % 64)
		if code == 255 {
			// Further names would run past the encoding.
			code = -1
			continue
		}
		code++
	}
}

// checkIrregular finds the first entry of a simple font's dictionaries that pdfium reads under a
// looser type or a wider range than the getters above do. See Irregular for what this is for.
//
// The order matches the order Irregular documents: /BaseFont, then /Encoding's own shape, then
// its /BaseEncoding and /Differences, then the descriptor's /Flags. It is read independently of
// baseEncoding, applyDifferences, and flags above — those already tolerate every one of these
// shapes by reading past them — so this makes one more pass over the same keys with a stricter
// question: not "what does the font state", but "would pdfium state the same thing".
//
// One gap is not caught here: a reference to a reference. objects.Store's Resolve follows such a
// chain to its end (objects/pdfcpu/pdfcpu.go), where pdfium follows only the first level, and
// every check below inherits that gap along with every other dictionary the renderer reads
// through Resolve. Closing it needs a change to Store itself, not to this function.
func (f *Font) checkIrregular(s objects.Store, d objects.Dict) string {
	if why := checkIsName(s, d, "BaseFont"); why != "" {
		// C15: pdfium's cpdf_font.cpp GetByteStringFor reads a string here too, and
		// CPDF_Type1Font::Load then matches (Symbol) or (ZapfDingbats) against the base-14 faces.
		// GetName above returns nothing for a string, so nothing here ever notices.
		return why
	}
	encDict, why := checkEncodingShape(s, d)
	if why != "" {
		return why
	}
	if encDict != nil {
		if why := checkIsName(s, encDict, "BaseEncoding"); why != "" {
			// C16: pdfium's cpdf_simplefont.cpp reads /BaseEncoding with GetByteStringFor as
			// well, so a string here still names a base encoding to pdfium while baseEncoding
			// above leaves f.baseName empty.
			return why
		}
		if why := checkDifferences(s, encDict); why != "" {
			return why
		}
	}
	if fd, ok := objects.GetDict(s, d, "FontDescriptor"); ok {
		if why := checkFlags(s, fd); why != "" {
			return why
		}
	}
	return ""
}

// checkIsName reports whether d[key] is present and not a name.
func checkIsName(s objects.Store, d objects.Dict, key objects.Name) string {
	v, ok := objects.Get(s, d, key)
	if !ok {
		return ""
	}
	if _, ok := v.(objects.Name); ok {
		return ""
	}
	return "/" + string(key) + " present and not a name"
}

// checkEncodingShape reports d's /Encoding as a dictionary when it is one, or the reason it is
// irregular when it is present and neither a name nor a dictionary.
func checkEncodingShape(s objects.Store, d objects.Dict) (objects.Dict, string) {
	v, ok := objects.Get(s, d, "Encoding")
	if !ok {
		return nil, ""
	}
	switch e := v.(type) {
	case objects.Name:
		return nil, ""
	case objects.Dict:
		return e, ""
	}
	return nil, "/Encoding present and neither a name nor a dictionary"
}

// checkDifferences reports the first entry of encDict's /Differences that is outside §9.6.5.1's
// grammar — a code is a literal PDF integer, and a simple font's codes are 0..255 — plus a
// /Differences present and not an array at all.
//
// Every one of those is a grammar error, and pdfium's LoadDifferences (cpdf_simplefont.cpp)
// repairs it by a rule of its own rather than rejecting it: cur_code starts at 0, not -1; every
// entry that is not a name is read through GetInteger, so a string, an array, a null, or a real is
// read as whatever GetInteger returns for it (0 for the first three, the real truncated); and
// cur_code is declared uint32_t, so a negative GetInteger() result wraps to a large positive
// number rather than the drop applyDifferences performs by setting code to -1.
//
// A code of 256 or above is one grammar error whose repair still drops the same names as
// applyDifferences at first, but not for long, for a code below 2^32. char_names_ is sized to
// 256, so "if (cur_code < char_names_.size())" (cpdf_simplefont.cpp:56) is false for cur_code at
// 256 or above, and every name up to the next literal integer is silently unassigned there — the
// same drop applyDifferences' code = -1 performs. But cur_code (:47) is a uint32_t, and the
// cur_code++ that follows each dropped name (:59) wraps it back to 0 after exactly 2^32-c
// increments for a starting code c, at which point LoadDifferences resumes assigning names from
// code 0 while applyDifferences leaves every one of them dropped. The two repairs part as soon as
// more than 2^32-c names follow a code 256 ≤ c < 2^32, which for a code this close to 2^32 is not
// many: two names are the fewest that can do it, at c = 4294967295 ([4294967295 /A /B] gives
// pdfium the name B at code 0, where applyDifferences drops both), and three at c = 4294967294
// ([4294967294 /A /B /C] gives pdfium the name C at code 0, where applyDifferences drops all
// three).
//
// At c = 4294967296 (2^32) the first name lands at code 0 at once, with no names dropped waiting
// for a wrap: [4294967296 /A] gives pdfium the name A at code 0, where applyDifferences still
// drops it. That is measured at this one value, not modelled — GetInteger's parsing of the
// token (cur_code = element->GetInteger(), cpdf_simplefont.cpp:61) is not vendored here — so
// nothing below relies on what pdfium does with a larger code.
//
// It is refused all the same, on a 64-bit build, by the 0..255 check below: every code above 255
// is refused outright here, so neither repair ever has a chance to reach a page regardless of how
// many names follow it. On a 32-bit build, pdfcpu's own parser reads a code outside int32 as 0
// before this package ever sees it, the same overflow checkFlags' doc below describes for
// /Flags — no check here can see that either, and closing it is the renderer-wide follow-up this
// group deferred, not a gap this function closes. checkFlags' doc below names the same leading-'0'
// sign-strip pdfcpu performs before a sign as a gap here too, on any build — a code written
// "0+65" reaches this package as the integer 65 but pdfium as 0, landing the name at 65 here and
// at 0 there.
//
// R#1: a dangling reference reaches the default case below alongside a literal null, because
// Store.Resolve turns both into the same Null value with a nil error (ISO 32000-2 §7.3.10 already
// makes a dangling reference and a null the same object, and objects.Store's Resolve does not
// distinguish "resolves to nothing" from "resolves to a null" either). pdfium does distinguish
// them: LoadDifferences finds a dangling reference's GetDirectObjectAt nil and skips it in its
// own nil check on the resolved element, before ever calling GetInteger, so a later name lands
// on the code it would have taken anyway, while a literal null is a real CPDF_Null object that
// reaches GetInteger like any other non-name and reads back as 0. applyDifferences cannot tell
// the two apart and treats both the same way it treats any other non-number, non-name entry:
// leaving the code in progress unchanged and skipping to the next one — which happens to match
// pdfium for the dangling reference and not for the literal null. Both are refused regardless:
// neither is the literal integer or the name §9.6.5.1 requires, and an agreement pdfium reaches
// by a nil-pointer check is no more a rule this package can rely on than the uint32_t wrap above
// is.
func checkDifferences(s objects.Store, encDict objects.Dict) string {
	arr, ok := objects.GetArray(s, encDict, "Differences")
	if !ok {
		if _, present := objects.Get(s, encDict, "Differences"); present {
			return "/Differences present and not an array"
		}
		return ""
	}
	for i, item := range arr {
		r, err := s.Resolve(item)
		if err != nil {
			// Not a dangling reference: Resolve already turns that into a Null with a nil
			// error (see objects.Store), which the Null case in the default branch below
			// reaches directly. This fires only if the store adapter meets an object type its
			// own conv does not convert (objects/pdfcpu/pdfcpu.go) — an internal defect, not a
			// shape a /Differences entry can take — so fold it into the same default case
			// rather than invent a refusal for a condition no PDF content can cause.
			r = objects.Null{}
		}
		switch e := r.(type) {
		case objects.Name:
			if i == 0 {
				// LoadDifferences has a code — 0 — before it reads anything; applyDifferences
				// does not, and skips a name with none to attach to.
				return "/Differences starting with a name, not a code"
			}
		case objects.Int:
			if e < 0 || e > 255 {
				return "/Differences containing a code outside 0..255"
			}
		default:
			// A string, an array, or a dictionary is not the literal integer §9.6.5.1 requires,
			// and LoadDifferences reads each of them through GetInteger rather than skipping it.
			// A real is not that literal integer either, even though applyDifferences accepts
			// one anyway by truncating it through AsNum: this checks the grammar the array is
			// required to hold, not the wider shapes this package's own reader tolerates. A null
			// and a dangling reference land here too — see the doc above for how the two engines
			// read them apart, and why both are refused regardless.
			return "/Differences containing an entry that is neither an integer 0..255 nor a name"
		}
	}
	return ""
}

// checkFlags reports fd's /Flags as irregular when it is present and not a literal PDF integer in
// [-2^31, 2^31), the range this package can trust regardless of a '+' sign (R#0); when it is
// present but null or a reference to nothing (R#2); when it sets both or neither of the
// symbolic and nonsymbolic bits (R#0); or when the entry is absent from the descriptor at all
// (R#0) — Table 120 makes it required, and Table 121 makes exactly one of the two bits required
// within it, so a font descriptor Table 120 already calls malformed is refused rather than routed
// by flags() above's absent-key default.
//
// R#0: the range stops at INT_MAX, not 2^32-1, because objects.Store cannot tell a '+'-signed
// literal from an unsigned one once pdfcpu has parsed it, and pdfium reads the two differently
// above INT_MAX. pdfcpu's parseNumericOrIndRef (model/parse.go:805) calls strconv.Atoi on the
// token, which accepts a leading '+' the same as no sign at all, so "+2147483652" and
// "2147483652" both come back as the int64 2147483652 — the sign is gone before font.go ever sees
// the value. pdfium's own number parser is not vendored here (core/fxcrt's FX_atonum and
// CPDF_Number are absent from the sources this package can read), so what follows is a
// measurement, not a citation: a fixture with /Flags "+2147483652" draws as flags 0 in pdfium,
// while the same bits written unsigned, "2147483652", draw as their full uint32 value,
// 0x80000004. A range reaching 2^32-1 would call the '+'-signed literal regular and route it by a
// symbolic bit pdfium never forms; since the Store gives this package no way to tell which literal
// produced a given value above INT_MAX, every one of them is refused, including the unsigned
// literals pdfium and this package's int64 read would have agreed on.
//
// A leading '0' is a second, wider gap the range check cannot close either, and this package does
// not try: pdfcpu's startParseNumericOrIndRef (model/parse.go:705-715) strips a leading '0' — or a
// "0.000…" prefix — from in front of a '+' or '-' sign before strconv.Atoi ever runs, so "0+4",
// "0-2147483644", and "0.0+4" all reach this package as the plain integers 4, -2147483644, and 4,
// none of them anywhere near INT_MAX, so the range check above never has cause to fire. pdfium
// reads all three as 0 (measured against swappedFlagsCFFFontPDF in render/native: this package
// draws the diamond flags 4 draws for each, pdfium the square flags 0 draws). objects.Store keeps
// only the parsed int64, so no check on objects.Int can see the leading zero that produced it, and
// the same class reaches every integer this package reads from a renderer, not only /Flags —
// /Widths [0+600] advances the glyph 600 here and 0 in pdfium. Closing it means changing how the
// store parses a numeric literal, not adding a range here, so it is deferred as a renderer-wide
// pdfcpu-adapter follow-up alongside #407 (the out-of-int integer read as 0), the same follow-up
// checkDifferences' doc above names for its own leading-'0' gap.
//
// The range matters outside int32 at all (C20) because flags() above keeps the full int64 GetInt
// reads, and a bit that int64 sets may be one a 32-bit reader such as pdfium's GetIntegerFor
// (cpdf_font.cpp) never sees. A real is flagged unconditionally, even one that would read back as
// the same value: GetInt tolerates a real by truncating it, but publishing that tolerance through
// Irregular too would make its answer track a rounding rule rather than the literal type /Flags is
// defined to hold, for a producer that writes 4.0 rather than 4 and evidently means the same font
// either way.
//
// The null and dangling-reference case is an ISO-and-pdfium disagreement rather than a range or a
// type: ISO 32000-2 makes both the same as an absent key (§7.3.9, §7.3.10), but pdfium's
// GetIntegerFor applies its default only when the key is missing outright and reads a present
// null or a dangling reference as 0. objects.Get treats them as absent too, so the raw map is
// read directly to tell "not there" from "there, and resolves to nothing" apart.
//
// The both-bits check also closes a 32-bit hole the range check alone cannot (R#0): on GOARCH=386,
// pdfcpu's own integer parser overflows a /Flags outside int32 to Integer(0) before this package
// ever sees it (model/parse.go's parseNumericOrIndRef, the isRangeError branch — #407), so the
// range check above never runs against the value that was actually written. A truncated 0 has
// neither bit set, and the both-clear rule below catches that shape whether it arrived as a
// literal 0 or as pdfcpu's silent stand-in for one that overflowed.
func checkFlags(s objects.Store, fd objects.Dict) string {
	v, ok := objects.Get(s, fd, "Flags")
	if !ok {
		if _, present := fd["Flags"]; present {
			return "/Flags present and null, or a reference to nothing"
		}
		return "/Flags absent from the font descriptor, which Table 120 requires"
	}
	n, ok := v.(objects.Int)
	if !ok {
		return "/Flags present and not an integer"
	}
	const min, max = -1 << 31, 1<<31 - 1
	if int64(n) < min || int64(n) > max {
		return "/Flags present and outside the range a signed 32-bit value can take"
	}
	switch sym, non := n&(1<<2) != 0, n&(1<<5) != 0; {
	case sym && non:
		return "/Flags with both the symbolic and nonsymbolic bits set, which Table 121 forbids"
	case !sym && !non:
		return "/Flags with neither the symbolic nor the nonsymbolic bit set, which Table 121 forbids"
	}
	return ""
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

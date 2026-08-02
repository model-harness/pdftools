package encoding

// The base encodings of ISO 32000-2 Annex D, as code-to-glyph-name tables.
//
// These are data, and the risk with data is a silent transcription error: one
// wrong name produces one wrong character in every document using that
// encoding, with nothing to indicate it happened. The tables are therefore
// validated in tables_test.go against golang.org/x/text/encoding/charmap,
// which derives Windows-1252 and Mac OS Roman from the Unicode Consortium's
// files rather than from this table. Two independent sources agreeing is
// evidence; one source restating itself is not.
//
// That validation is also what documents the places PDF deliberately departs
// from the platform encoding, of which there are exactly three. They are listed
// with the tables below and asserted individually, so a future edit cannot
// quietly add a fourth.

// MacExpertEncoding is absent. It addresses an expert glyph set — small caps,
// oldstyle figures, fractions — that no font in this repo's corpus uses, and a
// 200-entry table for a case that does not arise is speculative. A font naming
// it falls back to StandardEncoding, which is wrong for those glyphs but no more
// wrong than having no encoding at all. The built-in encodings of Symbol and
// ZapfDingbats are absent for the same reason.

// MacExpertEncoding is absent from this map on purpose: Base reporting "not
// found" is the honest answer, and the font package's fallback handles it.
var baseTables = map[string]*[256]string{
	"StandardEncoding": &standardEncoding,
	"WinAnsiEncoding":  &winAnsiEncoding,
	"MacRomanEncoding": &macRomanEncoding,
	"PDFDocEncoding":   &pdfDocEncoding,
}

// asciiNames holds the glyph names for codes 0x20 through 0x7E, which the three
// Latin encodings share except for two codes in StandardEncoding.
var asciiNames = [95]string{
	"space", "exclam", "quotedbl", "numbersign", "dollar", "percent",
	"ampersand", "quotesingle", "parenleft", "parenright", "asterisk", "plus",
	"comma", "hyphen", "period", "slash",
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight",
	"nine",
	"colon", "semicolon", "less", "equal", "greater", "question", "at",
	"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M",
	"N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z",
	"bracketleft", "backslash", "bracketright", "asciicircum", "underscore",
	"grave",
	"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m",
	"n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
	"braceleft", "bar", "braceright", "asciitilde",
}

// fillASCII writes the shared ASCII range into a table.
func fillASCII(t *[256]string) {
	for i, n := range asciiNames {
		t[0x20+i] = n
	}
}

var standardEncoding = func() [256]string {
	var t [256]string
	fillASCII(&t)
	// StandardEncoding predates the ASCII conventions for these two codes: 0x27
	// is a right single quote and 0x60 a left one, not the vertical typewriter
	// marks. A font using StandardEncoding whose text is full of stray
	// apostrophes is usually this being ignored.
	t[0x27] = "quoteright"
	t[0x60] = "quoteleft"

	upper := map[byte]string{
		0xA1: "exclamdown", 0xA2: "cent", 0xA3: "sterling", 0xA4: "fraction",
		0xA5: "yen", 0xA6: "florin", 0xA7: "section", 0xA8: "currency",
		0xA9: "quotesingle", 0xAA: "quotedblleft", 0xAB: "guillemotleft",
		0xAC: "guilsinglleft", 0xAD: "guilsinglright", 0xAE: "fi", 0xAF: "fl",
		0xB1: "endash", 0xB2: "dagger", 0xB3: "daggerdbl",
		0xB4: "periodcentered", 0xB6: "paragraph", 0xB7: "bullet",
		0xB8: "quotesinglbase", 0xB9: "quotedblbase", 0xBA: "quotedblright",
		0xBB: "guillemotright", 0xBC: "ellipsis", 0xBD: "perthousand",
		0xBF: "questiondown",
		0xC1: "grave", 0xC2: "acute", 0xC3: "circumflex", 0xC4: "tilde",
		0xC5: "macron", 0xC6: "breve", 0xC7: "dotaccent", 0xC8: "dieresis",
		0xCA: "ring", 0xCB: "cedilla", 0xCD: "hungarumlaut", 0xCE: "ogonek",
		0xCF: "caron", 0xD0: "emdash",
		0xE1: "AE", 0xE3: "ordfeminine", 0xE8: "Lslash", 0xE9: "Oslash",
		0xEA: "OE", 0xEB: "ordmasculine",
		0xF1: "ae", 0xF5: "dotlessi", 0xF8: "lslash", 0xF9: "oslash",
		0xFA: "oe", 0xFB: "germandbls",
	}
	for c, n := range upper {
		t[c] = n
	}
	return t
}()

var winAnsiEncoding = func() [256]string {
	var t [256]string
	fillASCII(&t)

	// 0x80-0x9F is the Windows-1252 extension to Latin-1, which is where the
	// curly quotes and dashes live. A reader that treats this range as Latin-1
	// control characters is why extracted text loses every apostrophe and dash.
	upper := map[byte]string{
		0x80: "Euro", 0x82: "quotesinglbase", 0x83: "florin",
		0x84: "quotedblbase", 0x85: "ellipsis", 0x86: "dagger",
		0x87: "daggerdbl", 0x88: "circumflex", 0x89: "perthousand",
		0x8A: "Scaron", 0x8B: "guilsinglleft", 0x8C: "OE", 0x8E: "Zcaron",
		0x91: "quoteleft", 0x92: "quoteright", 0x93: "quotedblleft",
		0x94: "quotedblright", 0x95: "bullet", 0x96: "endash", 0x97: "emdash",
		0x98: "tilde", 0x99: "trademark", 0x9A: "scaron",
		0x9B: "guilsinglright", 0x9C: "oe", 0x9E: "zcaron", 0x9F: "Ydieresis",

		// Annex D specifies that code 0xA0 is SPACE and 0xAD is HYPHEN, not the
		// no-break space and soft hyphen Windows-1252 puts there. This is the
		// first of the three deliberate departures, and it is the right one for
		// extraction: a no-break space that arrives as U+00A0 breaks word
		// splitting in every downstream consumer.
		0xA0: "space",
		0xA1: "exclamdown", 0xA2: "cent", 0xA3: "sterling", 0xA4: "currency",
		0xA5: "yen", 0xA6: "brokenbar", 0xA7: "section", 0xA8: "dieresis",
		0xA9: "copyright", 0xAA: "ordfeminine", 0xAB: "guillemotleft",
		0xAC: "logicalnot", 0xAD: "hyphen", 0xAE: "registered",
		0xAF: "macron",
		0xB0: "degree", 0xB1: "plusminus", 0xB2: "twosuperior",
		0xB3: "threesuperior", 0xB4: "acute", 0xB5: "mu", 0xB6: "paragraph",
		0xB7: "periodcentered", 0xB8: "cedilla", 0xB9: "onesuperior",
		0xBA: "ordmasculine", 0xBB: "guillemotright", 0xBC: "onequarter",
		0xBD: "onehalf", 0xBE: "threequarters", 0xBF: "questiondown",
		0xC0: "Agrave", 0xC1: "Aacute", 0xC2: "Acircumflex", 0xC3: "Atilde",
		0xC4: "Adieresis", 0xC5: "Aring", 0xC6: "AE", 0xC7: "Ccedilla",
		0xC8: "Egrave", 0xC9: "Eacute", 0xCA: "Ecircumflex",
		0xCB: "Edieresis", 0xCC: "Igrave", 0xCD: "Iacute",
		0xCE: "Icircumflex", 0xCF: "Idieresis",
		0xD0: "Eth", 0xD1: "Ntilde", 0xD2: "Ograve", 0xD3: "Oacute",
		0xD4: "Ocircumflex", 0xD5: "Otilde", 0xD6: "Odieresis",
		0xD7: "multiply", 0xD8: "Oslash", 0xD9: "Ugrave", 0xDA: "Uacute",
		0xDB: "Ucircumflex", 0xDC: "Udieresis", 0xDD: "Yacute",
		0xDE: "Thorn", 0xDF: "germandbls",
		0xE0: "agrave", 0xE1: "aacute", 0xE2: "acircumflex", 0xE3: "atilde",
		0xE4: "adieresis", 0xE5: "aring", 0xE6: "ae", 0xE7: "ccedilla",
		0xE8: "egrave", 0xE9: "eacute", 0xEA: "ecircumflex",
		0xEB: "edieresis", 0xEC: "igrave", 0xED: "iacute",
		0xEE: "icircumflex", 0xEF: "idieresis",
		0xF0: "eth", 0xF1: "ntilde", 0xF2: "ograve", 0xF3: "oacute",
		0xF4: "ocircumflex", 0xF5: "otilde", 0xF6: "odieresis",
		0xF7: "divide", 0xF8: "oslash", 0xF9: "ugrave", 0xFA: "uacute",
		0xFB: "ucircumflex", 0xFC: "udieresis", 0xFD: "yacute",
		0xFE: "thorn", 0xFF: "ydieresis",
	}
	for c, n := range upper {
		t[c] = n
	}
	return t
}()

var macRomanEncoding = func() [256]string {
	var t [256]string
	fillASCII(&t)

	upper := map[byte]string{
		0x80: "Adieresis", 0x81: "Aring", 0x82: "Ccedilla", 0x83: "Eacute",
		0x84: "Ntilde", 0x85: "Odieresis", 0x86: "Udieresis", 0x87: "aacute",
		0x88: "agrave", 0x89: "acircumflex", 0x8A: "adieresis", 0x8B: "atilde",
		0x8C: "aring", 0x8D: "ccedilla", 0x8E: "eacute", 0x8F: "egrave",
		0x90: "ecircumflex", 0x91: "edieresis", 0x92: "iacute", 0x93: "igrave",
		0x94: "icircumflex", 0x95: "idieresis", 0x96: "ntilde", 0x97: "oacute",
		0x98: "ograve", 0x99: "ocircumflex", 0x9A: "odieresis", 0x9B: "otilde",
		0x9C: "uacute", 0x9D: "ugrave", 0x9E: "ucircumflex", 0x9F: "udieresis",
		0xA0: "dagger", 0xA1: "degree", 0xA2: "cent", 0xA3: "sterling",
		0xA4: "section", 0xA5: "bullet", 0xA6: "paragraph", 0xA7: "germandbls",
		0xA8: "registered", 0xA9: "copyright", 0xAA: "trademark",
		0xAB: "acute", 0xAC: "dieresis", 0xAD: "notequal", 0xAE: "AE",
		0xAF: "Oslash",
		0xB0: "infinity", 0xB1: "plusminus", 0xB2: "lessequal",
		0xB3: "greaterequal", 0xB4: "yen", 0xB5: "mu", 0xB6: "partialdiff",
		0xB7: "summation", 0xB8: "product", 0xB9: "pi", 0xBA: "integral",
		0xBB: "ordfeminine", 0xBC: "ordmasculine",
		// Annex D writes "Omega" here, but in the AGL that name is U+2126 OHM
		// SIGN, while Mac OS Roman has U+03A9 GREEK CAPITAL LETTER OMEGA at this
		// code. The AGL name for the letter is "Omegagreek", so that is what the
		// table must say to produce the character the encoding actually defines.
		// Code 0xC6 below is the same trap with Delta, where Annex D and the AGL
		// happen to agree because Mac OS Roman really does have INCREMENT there.
		0xBD: "Omegagreek",
		0xBE: "ae", 0xBF: "oslash",
		0xC0: "questiondown", 0xC1: "exclamdown", 0xC2: "logicalnot",
		0xC3: "radical", 0xC4: "florin", 0xC5: "approxequal", 0xC6: "Delta",
		0xC7: "guillemotleft", 0xC8: "guillemotright", 0xC9: "ellipsis",

		// The second deliberate departure: Annex D gives 0xCA as SPACE where Mac
		// OS Roman has a no-break space, for the same reason as WinAnsi 0xA0.
		0xCA: "space",
		0xCB: "Agrave", 0xCC: "Atilde", 0xCD: "Otilde", 0xCE: "OE",
		0xCF: "oe",
		0xD0: "endash", 0xD1: "emdash", 0xD2: "quotedblleft",
		0xD3: "quotedblright", 0xD4: "quoteleft", 0xD5: "quoteright",
		0xD6: "divide", 0xD7: "lozenge", 0xD8: "ydieresis", 0xD9: "Ydieresis",
		0xDA: "fraction",

		// The third and last: Annex D gives 0xDB as CURRENCY. Mac OS Roman put
		// a Euro sign here in later versions, and the PDF encoding did not
		// follow. A font actually containing a Euro at this code is relying on
		// its own /Differences, which override this table anyway.
		0xDB: "currency",
		0xDC: "guilsinglleft", 0xDD: "guilsinglright", 0xDE: "fi",
		0xDF: "fl",
		0xE0: "daggerdbl", 0xE1: "periodcentered", 0xE2: "quotesinglbase",
		0xE3: "quotedblbase", 0xE4: "perthousand", 0xE5: "Acircumflex",
		0xE6: "Ecircumflex", 0xE7: "Aacute", 0xE8: "Edieresis",
		0xE9: "Egrave", 0xEA: "Iacute", 0xEB: "Icircumflex",
		0xEC: "Idieresis", 0xED: "Igrave", 0xEE: "Oacute",
		0xEF: "Ocircumflex",
		0xF0: "apple", 0xF1: "Ograve", 0xF2: "Uacute", 0xF3: "Ucircumflex",
		0xF4: "Ugrave", 0xF5: "dotlessi", 0xF6: "circumflex", 0xF7: "tilde",
		0xF8: "macron", 0xF9: "breve", 0xFA: "dotaccent", 0xFB: "ring",
		0xFC: "cedilla", 0xFD: "hungarumlaut", 0xFE: "ogonek", 0xFF: "caron",
	}
	for c, n := range upper {
		t[c] = n
	}
	return t
}()

// pdfDocEncoding is the encoding of text strings in a document — /Title,
// /Lang, outline entries — not of glyphs in a content stream (Annex D.3).
//
// It is here because it is one of Annex D's tables and callers ask for it by
// name, but a font's /Encoding is never legally PDFDocEncoding. Text-string
// decoding lives in objects.DecodeTextString.
var pdfDocEncoding = func() [256]string {
	var t [256]string
	fillASCII(&t)
	// PDFDocEncoding agrees with WinAnsi across Latin-1 but fills 0x18-0x1F
	// with accents and moves the Windows-1252 extension block to 0x80-0x9F
	// with a slightly different assignment.
	low := map[byte]string{
		0x18: "breve", 0x19: "caron", 0x1A: "circumflex", 0x1B: "dotaccent",
		0x1C: "hungarumlaut", 0x1D: "ogonek", 0x1E: "ring", 0x1F: "tilde",
	}
	for c, n := range low {
		t[c] = n
	}
	upper := map[byte]string{
		0x80: "bullet", 0x81: "dagger", 0x82: "daggerdbl", 0x83: "ellipsis",
		0x84: "emdash", 0x85: "endash", 0x86: "florin", 0x87: "fraction",
		0x88: "guilsinglleft", 0x89: "guilsinglright", 0x8A: "minus",
		0x8B: "perthousand", 0x8C: "quotedblbase", 0x8D: "quotedblleft",
		0x8E: "quotedblright", 0x8F: "quoteleft", 0x90: "quoteright",
		0x91: "quotesinglbase", 0x92: "trademark", 0x93: "fi", 0x94: "fl",
		0x95: "Lslash", 0x96: "OE", 0x97: "Scaron", 0x98: "Ydieresis",
		0x99: "Zcaron", 0x9A: "dotlessi", 0x9B: "lslash", 0x9C: "oe",
		0x9D: "scaron", 0x9E: "zcaron", 0xA0: "Euro",
	}
	for c, n := range upper {
		t[c] = n
	}
	// 0xA1 through 0xFF match WinAnsiEncoding, which matches Latin-1.
	for c := 0xA1; c <= 0xFF; c++ {
		t[c] = winAnsiEncoding[c]
	}
	return t
}()

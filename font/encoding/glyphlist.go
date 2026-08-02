package encoding

// glyphList maps glyph names to Unicode values: the Adobe Glyph List, reduced
// to the names PDF text actually uses.
//
// The full AGL is roughly 4,300 entries, most of them for scripts and expert
// glyph sets that no PDF in this repo's corpus references. Embedding all of it
// by hand would be several thousand lines whose only correctness check is my own
// transcription, which is precisely the kind of data error that produces one
// wrong character per document with nothing to signal it.
//
// What is here instead is the closed set that matters, and every entry is
// checked by a test that does not read this table:
//
//   - The names of the four Annex D base encodings. glyphlist_test.go asserts
//     every one resolves, and tables_test.go independently confirms the
//     resulting code-to-rune mapping against golang.org/x/text/encoding/charmap.
//     A wrong value here contradicts the Unicode Consortium's own data and
//     fails.
//   - The ligatures, dashes, quotes, and mathematical symbols that appear in
//     /Differences arrays across the corpus, which the survey in
//     corpus_test.go enumerates.
//
// Names outside this set are not lost: GlyphRune resolves uniXXXX and uXXXX
// forms arithmetically and strips stylistic suffixes, which covers the
// synthesized names subsetting tools emit. A name that resolves to nothing
// returns false rather than a guess, so the caller can fall back to /ToUnicode
// instead of emitting a plausible wrong character.
var glyphList = map[string]rune{
	// C0 names that appear in encodings and property lists.
	"space": 0x0020, "exclam": 0x0021, "quotedbl": 0x0022,
	"numbersign": 0x0023, "dollar": 0x0024, "percent": 0x0025,
	"ampersand": 0x0026, "quotesingle": 0x0027, "parenleft": 0x0028,
	"parenright": 0x0029, "asterisk": 0x002A, "plus": 0x002B,
	"comma": 0x002C, "hyphen": 0x002D, "period": 0x002E, "slash": 0x002F,
	"zero": 0x0030, "one": 0x0031, "two": 0x0032, "three": 0x0033,
	"four": 0x0034, "five": 0x0035, "six": 0x0036, "seven": 0x0037,
	"eight": 0x0038, "nine": 0x0039,
	"colon": 0x003A, "semicolon": 0x003B, "less": 0x003C, "equal": 0x003D,
	"greater": 0x003E, "question": 0x003F, "at": 0x0040,
	"bracketleft": 0x005B, "backslash": 0x005C, "bracketright": 0x005D,
	"asciicircum": 0x005E, "underscore": 0x005F, "grave": 0x0060,
	"braceleft": 0x007B, "bar": 0x007C, "braceright": 0x007D,
	"asciitilde": 0x007E,

	// Latin-1 letters and symbols.
	"exclamdown": 0x00A1, "cent": 0x00A2, "sterling": 0x00A3,
	"currency": 0x00A4, "yen": 0x00A5, "brokenbar": 0x00A6,
	"section": 0x00A7, "dieresis": 0x00A8, "copyright": 0x00A9,
	"ordfeminine": 0x00AA, "guillemotleft": 0x00AB, "logicalnot": 0x00AC,
	"registered": 0x00AE, "macron": 0x00AF,
	"degree": 0x00B0, "plusminus": 0x00B1, "twosuperior": 0x00B2,
	"threesuperior": 0x00B3, "acute": 0x00B4, "mu": 0x00B5,
	"paragraph": 0x00B6, "periodcentered": 0x00B7, "cedilla": 0x00B8,
	"onesuperior": 0x00B9, "ordmasculine": 0x00BA, "guillemotright": 0x00BB,
	"onequarter": 0x00BC, "onehalf": 0x00BD, "threequarters": 0x00BE,
	"questiondown": 0x00BF,
	"Agrave":       0x00C0, "Aacute": 0x00C1, "Acircumflex": 0x00C2,
	"Atilde": 0x00C3, "Adieresis": 0x00C4, "Aring": 0x00C5, "AE": 0x00C6,
	"Ccedilla": 0x00C7, "Egrave": 0x00C8, "Eacute": 0x00C9,
	"Ecircumflex": 0x00CA, "Edieresis": 0x00CB, "Igrave": 0x00CC,
	"Iacute": 0x00CD, "Icircumflex": 0x00CE, "Idieresis": 0x00CF,
	"Eth": 0x00D0, "Ntilde": 0x00D1, "Ograve": 0x00D2, "Oacute": 0x00D3,
	"Ocircumflex": 0x00D4, "Otilde": 0x00D5, "Odieresis": 0x00D6,
	"multiply": 0x00D7, "Oslash": 0x00D8, "Ugrave": 0x00D9,
	"Uacute": 0x00DA, "Ucircumflex": 0x00DB, "Udieresis": 0x00DC,
	"Yacute": 0x00DD, "Thorn": 0x00DE, "germandbls": 0x00DF,
	"agrave": 0x00E0, "aacute": 0x00E1, "acircumflex": 0x00E2,
	"atilde": 0x00E3, "adieresis": 0x00E4, "aring": 0x00E5, "ae": 0x00E6,
	"ccedilla": 0x00E7, "egrave": 0x00E8, "eacute": 0x00E9,
	"ecircumflex": 0x00EA, "edieresis": 0x00EB, "igrave": 0x00EC,
	"iacute": 0x00ED, "icircumflex": 0x00EE, "idieresis": 0x00EF,
	"eth": 0x00F0, "ntilde": 0x00F1, "ograve": 0x00F2, "oacute": 0x00F3,
	"ocircumflex": 0x00F4, "otilde": 0x00F5, "odieresis": 0x00F6,
	"divide": 0x00F7, "oslash": 0x00F8, "ugrave": 0x00F9, "uacute": 0x00FA,
	"ucircumflex": 0x00FB, "udieresis": 0x00FC, "yacute": 0x00FD,
	"thorn": 0x00FE, "ydieresis": 0x00FF,

	// Latin Extended-A: the accented forms that appear in European text.
	"Amacron": 0x0100, "amacron": 0x0101, "Abreve": 0x0102, "abreve": 0x0103,
	"Aogonek": 0x0104, "aogonek": 0x0105, "Cacute": 0x0106, "cacute": 0x0107,
	"Ccircumflex": 0x0108, "ccircumflex": 0x0109, "Cdotaccent": 0x010A,
	"cdotaccent": 0x010B, "Ccaron": 0x010C, "ccaron": 0x010D,
	"Dcaron": 0x010E, "dcaron": 0x010F, "Dcroat": 0x0110, "dcroat": 0x0111,
	"Emacron": 0x0112, "emacron": 0x0113, "Ebreve": 0x0114, "ebreve": 0x0115,
	"Edotaccent": 0x0116, "edotaccent": 0x0117, "Eogonek": 0x0118,
	"eogonek": 0x0119, "Ecaron": 0x011A, "ecaron": 0x011B,
	"Gcircumflex": 0x011C, "gcircumflex": 0x011D, "Gbreve": 0x011E,
	"gbreve": 0x011F, "Gdotaccent": 0x0120, "gdotaccent": 0x0121,
	"Hcircumflex": 0x0124, "hcircumflex": 0x0125, "Hbar": 0x0126,
	"hbar": 0x0127, "Itilde": 0x0128, "itilde": 0x0129, "Imacron": 0x012A,
	"imacron": 0x012B, "Ibreve": 0x012C, "ibreve": 0x012D,
	"Iogonek": 0x012E, "iogonek": 0x012F, "Idotaccent": 0x0130,
	"dotlessi": 0x0131, "IJ": 0x0132, "ij": 0x0133, "Jcircumflex": 0x0134,
	"jcircumflex": 0x0135, "Kcommaaccent": 0x0136, "kcommaaccent": 0x0137,
	"kgreenlandic": 0x0138, "Lacute": 0x0139, "lacute": 0x013A,
	"Lcommaaccent": 0x013B, "lcommaaccent": 0x013C, "Lcaron": 0x013D,
	"lcaron": 0x013E, "Ldot": 0x013F, "ldot": 0x0140, "Lslash": 0x0141,
	"lslash": 0x0142, "Nacute": 0x0143, "nacute": 0x0144,
	"Ncommaaccent": 0x0145, "ncommaaccent": 0x0146, "Ncaron": 0x0147,
	"ncaron": 0x0148, "napostrophe": 0x0149, "Eng": 0x014A, "eng": 0x014B,
	"Omacron": 0x014C, "omacron": 0x014D, "Obreve": 0x014E, "obreve": 0x014F,
	"Ohungarumlaut": 0x0150, "ohungarumlaut": 0x0151, "OE": 0x0152,
	"oe": 0x0153, "Racute": 0x0154, "racute": 0x0155,
	"Rcommaaccent": 0x0156, "rcommaaccent": 0x0157, "Rcaron": 0x0158,
	"rcaron": 0x0159, "Sacute": 0x015A, "sacute": 0x015B,
	"Scircumflex": 0x015C, "scircumflex": 0x015D, "Scedilla": 0x015E,
	"scedilla": 0x015F, "Scaron": 0x0160, "scaron": 0x0161,
	"Tcommaaccent": 0x0162, "tcommaaccent": 0x0163, "Tcaron": 0x0164,
	"tcaron": 0x0165, "Tbar": 0x0166, "tbar": 0x0167, "Utilde": 0x0168,
	"utilde": 0x0169, "Umacron": 0x016A, "umacron": 0x016B,
	"Ubreve": 0x016C, "ubreve": 0x016D, "Uring": 0x016E, "uring": 0x016F,
	"Uhungarumlaut": 0x0170, "uhungarumlaut": 0x0171, "Uogonek": 0x0172,
	"uogonek": 0x0173, "Wcircumflex": 0x0174, "wcircumflex": 0x0175,
	"Ycircumflex": 0x0176, "ycircumflex": 0x0177, "Ydieresis": 0x0178,
	"Zacute": 0x0179, "zacute": 0x017A, "Zdotaccent": 0x017B,
	"zdotaccent": 0x017C, "Zcaron": 0x017D, "zcaron": 0x017E,
	"longs":  0x017F,
	"florin": 0x0192,

	// Spacing modifier letters used as standalone accent glyphs.
	"circumflex": 0x02C6, "caron": 0x02C7, "breve": 0x02D8,
	"dotaccent": 0x02D9, "ring": 0x02DA, "ogonek": 0x02DB, "tilde": 0x02DC,
	"hungarumlaut": 0x02DD,

	// Greek, which appears throughout mathematical text.
	//
	// Delta and Omega are the AGL's two traps. The unsuffixed names map to the
	// *technical symbols* — U+2206 INCREMENT and U+2126 OHM SIGN — because that
	// is what Adobe's original fonts drew at those names; the Greek letters carry
	// the "greek" suffix instead. Annex D uses both unsuffixed names in
	// MacRomanEncoding, and the two cases resolve differently: at 0xC6 Mac OS
	// Roman really does have INCREMENT, so "Delta" is right, but at 0xBD it has
	// GREEK CAPITAL LETTER OMEGA, so that table entry says "Omegagreek". The
	// disagreement exists because U+2126 canonically decomposes to U+03A9 — the
	// same character by Unicode's own equivalence, two code points by identity —
	// and the tests would not distinguish them if this table hid the difference.
	"Alpha": 0x0391, "Beta": 0x0392, "Gamma": 0x0393, "Delta": 0x2206,
	"Deltagreek": 0x0394,
	"Epsilon":    0x0395, "Zeta": 0x0396, "Eta": 0x0397, "Theta": 0x0398,
	"Iota": 0x0399, "Kappa": 0x039A, "Lambda": 0x039B, "Mu": 0x039C,
	"Nu": 0x039D, "Xi": 0x039E, "Omicron": 0x039F, "Pi": 0x03A0,
	"Rho": 0x03A1, "Sigma": 0x03A3, "Tau": 0x03A4, "Upsilon": 0x03A5,
	"Phi": 0x03A6, "Chi": 0x03A7, "Psi": 0x03A8, "Omega": 0x2126,
	"Omegagreek": 0x03A9, "mugreek": 0x03BC,
	"alpha": 0x03B1, "beta": 0x03B2, "gamma": 0x03B3, "delta": 0x03B4,
	"epsilon": 0x03B5, "zeta": 0x03B6, "eta": 0x03B7, "theta": 0x03B8,
	"iota": 0x03B9, "kappa": 0x03BA, "lambda": 0x03BB, "nu": 0x03BD,
	"xi": 0x03BE, "omicron": 0x03BF, "pi": 0x03C0, "rho": 0x03C1,
	"sigma1": 0x03C2, "sigma": 0x03C3, "tau": 0x03C4, "upsilon": 0x03C5,
	"phi": 0x03C6, "chi": 0x03C7, "psi": 0x03C8, "omega": 0x03C9,
	"theta1": 0x03D1, "phi1": 0x03D5, "omega1": 0x03D6,

	// General punctuation. These are the entries that decide whether extracted
	// prose reads correctly: an em dash, a curly quote, or an ellipsis lost here
	// is visible in every paragraph.
	"quoteright": 0x2019, "quoteleft": 0x2018, "quotedblleft": 0x201C,
	"quotedblright": 0x201D, "quotesinglbase": 0x201A,
	"quotedblbase": 0x201E, "endash": 0x2013, "emdash": 0x2014,
	"afii00208": 0x2015, "underscoredbl": 0x2017, "quotereversed": 0x201B,
	"dagger": 0x2020, "daggerdbl": 0x2021, "bullet": 0x2022,
	"onedotenleader": 0x2024, "twodotenleader": 0x2025, "ellipsis": 0x2026,
	"perthousand": 0x2030, "minute": 0x2032, "second": 0x2033,
	"guilsinglleft": 0x2039, "guilsinglright": 0x203A, "exclamdbl": 0x203C,
	"fraction": 0x2044, "zerosuperior": 0x2070, "foursuperior": 0x2074,
	"fivesuperior": 0x2075, "sixsuperior": 0x2076, "sevensuperior": 0x2077,
	"eightsuperior": 0x2078, "ninesuperior": 0x2079, "parenleftsuperior": 0x207D,
	"parenrightsuperior": 0x207E, "nsuperior": 0x207F, "zeroinferior": 0x2080,
	"oneinferior": 0x2081, "twoinferior": 0x2082, "threeinferior": 0x2083,
	"fourinferior": 0x2084, "fiveinferior": 0x2085, "sixinferior": 0x2086,
	"seveninferior": 0x2087, "eightinferior": 0x2088, "nineinferior": 0x2089,
	"parenleftinferior": 0x208D, "parenrightinferior": 0x208E,
	"Euro": 0x20AC,

	// Letterlike symbols and number forms.
	"Ifraktur": 0x2111, "weierstrass": 0x2118, "Rfraktur": 0x211C,
	"prescription": 0x211E, "trademark": 0x2122,
	"estimated": 0x212E, "aleph": 0x2135,
	"onethird": 0x2153, "twothirds": 0x2154, "oneeighth": 0x215B,
	"threeeighths": 0x215C, "fiveeighths": 0x215D, "seveneighths": 0x215E,

	// Arrows and mathematical operators. A spec document is full of these, and
	// they are the names most often synthesized rather than standard, which is
	// why uniXXXX fallback matters as much as the table.
	"arrowleft": 0x2190, "arrowup": 0x2191, "arrowright": 0x2192,
	"arrowdown": 0x2193, "arrowboth": 0x2194, "arrowupdn": 0x2195,
	"arrowdblleft": 0x21D0, "arrowdblup": 0x21D1, "arrowdblright": 0x21D2,
	"arrowdbldown": 0x21D3, "arrowdblboth": 0x21D4,
	"universal": 0x2200, "partialdiff": 0x2202, "existential": 0x2203,
	"emptyset": 0x2205, "gradient": 0x2207, "element": 0x2208,
	"notelement": 0x2209, "suchthat": 0x220B, "product": 0x220F,
	"summation": 0x2211, "minus": 0x2212, "fraction1": 0x2215,
	"asteriskmath": 0x2217, "periodcentered1": 0x2219, "radical": 0x221A,
	"proportional": 0x221D, "infinity": 0x221E, "orthogonal": 0x221F,
	"angle": 0x2220, "logicaland": 0x2227, "logicalor": 0x2228,
	"intersection": 0x2229, "union": 0x222A, "integral": 0x222B,
	"therefore": 0x2234, "similar": 0x223C, "congruent": 0x2245,
	"approxequal": 0x2248, "notequal": 0x2260, "equivalence": 0x2261,
	"lessequal": 0x2264, "greaterequal": 0x2265, "propersubset": 0x2282,
	"propersuperset": 0x2283, "notsubset": 0x2284, "reflexsubset": 0x2286,
	"reflexsuperset": 0x2287, "circleplus": 0x2295, "circlemultiply": 0x2297,
	"perpendicular": 0x22A5, "dotmath": 0x22C5,
	"house": 0x2302, "revlogicalnot": 0x2310,
	"integraltp": 0x2320, "integralbt": 0x2321,

	// Geometric shapes and dingbats that appear as list bullets.
	"filledbox": 0x25A0, "triagup": 0x25B2, "triagrt": 0x25BA,
	"triagdn": 0x25BC, "triaglf": 0x25C4, "lozenge": 0x25CA,
	"circle": 0x25CB, "invbullet": 0x25D8, "invcircle": 0x25D9,
	"openbullet": 0x25E6, "smileface": 0x263A, "invsmileface": 0x263B,
	"sun": 0x263C, "female": 0x2640, "male": 0x2642, "spade": 0x2660,
	"club": 0x2663, "heart": 0x2665, "diamond": 0x2666, "musicalnote": 0x266A,
	"musicalnotedbl": 0x266B,

	// Ligatures. These matter disproportionately: a lost "fi" turns "efficient"
	// into "ecient", which is the single most recognizable symptom of a broken
	// PDF text extractor.
	"ff": 0xFB00, "fi": 0xFB01, "fl": 0xFB02, "ffi": 0xFB03, "ffl": 0xFB04,
	"longst": 0xFB05, "st": 0xFB06,

	// Apple's logo has no Unicode assignment; U+F8FF is the private-use code
	// point Apple itself uses, and MacRomanEncoding names it at 0xF0.
	"apple": 0xF8FF,
}

func init() {
	// The Latin letters name themselves: glyph "A" is U+0041, "a" is U+0061.
	// Generated rather than written out because 52 hand-typed lines of the most
	// frequently used entries in the whole table is 52 chances to typo the most
	// damaging possible entry, and the rule is exact arithmetic.
	for r := 'A'; r <= 'Z'; r++ {
		glyphList[string(r)] = r
	}
	for r := 'a'; r <= 'z'; r++ {
		glyphList[string(r)] = r
	}
}

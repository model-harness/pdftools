package font

import "strings"

// Family is the broad shape of a typeface, which is as much as a substitute can honour.
//
// Three and not more, because three is what the evidence supports. A font with no embedded
// program offers a name, a descriptor and a width array; from those, "sans or serif or
// monospaced" is answerable and "Garamond rather than Caslon" is not. A fourth value for
// script or display faces would be a category nothing could be assigned to with confidence.
type Family uint8

const (
	// FamilySans is a face without serifs: Helvetica, Arial, Verdana.
	FamilySans Family = iota
	// FamilySerif is a face with them: Times, Georgia, Garamond.
	FamilySerif
	// FamilyMono is a fixed-pitch face: Courier, Consolas.
	FamilyMono
	// FamilySymbol is a face whose codes are not characters — Symbol, ZapfDingbats, a dingbat
	// or icon font. It is its own family because no text face can stand in for one: substituting
	// Liberation Sans for Symbol draws Latin letters where the page shows mathematics, which is
	// a page that looks like text and says something else entirely.
	FamilySymbol
)

func (f Family) String() string {
	switch f {
	case FamilySans:
		return "sans"
	case FamilySerif:
		return "serif"
	case FamilyMono:
		return "mono"
	case FamilySymbol:
		return "symbol"
	}
	return "unknown"
}

// Style is the canonical face to substitute for a font with no embedded program.
//
// The split of responsibility this type exists to draw: *this package* decides the style, from
// the font's own evidence, deterministically. A *caller* decides which actual face a style maps
// to, because that is policy — a proofing tool wants metric compatibility with Arial, an
// archival pipeline may want a face it has licensed, and neither answer belongs in a parser.
type Style struct {
	Family       Family
	Bold, Italic bool
}

func (s Style) String() string {
	out := s.Family.String()
	switch {
	case s.Bold && s.Italic:
		out += " bold italic"
	case s.Bold:
		out += " bold"
	case s.Italic:
		out += " italic"
	}
	return out
}

// SubstituteStyle is the style a face standing in for this font should have.
//
// # What the evidence is, and what it is not
//
// This is typeface inference and not character inference, and the distinction matters because
// the two have opposite inputs. A font needing substitution has *no outlines at all* — the
// program is absent, which is the whole reason a substitute is needed — so there is nothing to
// measure the shape of. What there is: the /BaseFont name, the descriptor's /Flags, /StemV,
// /FontWeight and /ItalicAngle, and the /Widths array. Load has already reduced those to the
// bold, italic, mono and serif traits, weighing name against descriptor where they disagree,
// which is why this function reads traits rather than dictionaries.
//
// # Why there is no confidence score
//
// Every branch below is a rule over stated evidence, so the answer is determined rather than
// estimated. A probability attached to a determined answer reads as humility and acts as noise:
// a caller cannot do anything different with 0.9 than with 1.0 here, because there is no second
// candidate to weigh it against. Confidence belongs on an inference that ranks candidates, which
// is character inference from an outline — a different problem, on a different population, and
// not this one.
func (f *Font) SubstituteStyle() Style {
	s := Style{Bold: f.bold, Italic: f.italic}
	switch {
	case f.isSymbolFace():
		// Checked first, because Symbol and ZapfDingbats are also serif-less and would
		// otherwise come out as sans — and a page of mathematics set in Liberation Sans is
		// worse than a page that refused to render.
		s.Family = FamilySymbol
		// A symbol face has no bold or italic to honour; claiming either would ask a caller
		// for a variant that does not exist.
		s.Bold, s.Italic = false, false
	case f.mono:
		s.Family = FamilyMono
	case f.serif:
		s.Family = FamilySerif
	default:
		// Sans is the default rather than serif, because /Flags bit 2 unset means "not stated"
		// far more often than it means "no serifs", and the corpus's own answer settles which
		// way to lean: Helvetica on 1,138 pages and Arial on 886 against Times on 148.
		s.Family = FamilySans
	}
	return s
}

// isSymbolFace reports a face whose codes are not characters.
//
// By name, because that is the only reliable evidence: /Flags bit 3 marks a font as symbolic,
// but it is set on a great many ordinary text subsets — a symbolic TrueType is simply one whose
// cmap is keyed by code, which is how most subsetters write Latin text — so reading it here
// would send half the corpus's text fonts to a dingbat substitute.
func (f *Font) isSymbolFace() bool {
	switch name := stripSubsetPrefix(f.BaseFont); name {
	case "Symbol", "ZapfDingbats":
		return true
	default:
		lower := strings.ToLower(name)
		for _, n := range []string{"dingbat", "wingding", "webding", "symbolmt", "mathematicalpi"} {
			if strings.Contains(lower, n) {
				return true
			}
		}
		return false
	}
}

// FaceRequest describes a font that needs a substitute.
//
// Both the name and the inferred style, because a caller may be able to do better than the style:
// one that happens to hold the actual Arial should use it for "ArialMT" rather than a
// metric-compatible stand-in, and only the name can tell it so. A caller with no opinion about
// names reads Style alone.
type FaceRequest struct {
	// Name is /BaseFont with any subset prefix stripped.
	Name string

	// Style is what SubstituteStyle concluded from the name and the descriptor.
	Style Style

	// Why is the reason a substitute is needed, for a caller that wants to log or decline by
	// case: a standard-14 font with no descriptor at all is a different situation from a
	// descriptor that declares a face and embeds no program.
	Why string
}

// FaceSource supplies a font program to stand in for one a document did not embed.
//
// # Why this is an interface, and why it is in this package
//
// Which face substitutes for Helvetica is policy, not parsing. A proofing tool wants metric
// compatibility with the original; an archival pipeline may be required to use a face it has
// licensed; a caller rendering for search does not care at all. A parser that chose for everyone
// would be making a typographic decision on evidence it does not have — and it would put the bytes
// of several faces into every binary that imports it, including the ones that never render.
//
// It lives here rather than in the renderer because a substitute font is font data, and because
// the implementation has to be able to depend on this package without depending on a rasterizer:
// when the interface was in render/native, the one implementation in this repo imported it and no
// test in render/native could import the implementation back.
type FaceSource interface {
	// Face returns a program for the request, or false to decline.
	//
	// Declining is a legitimate answer and not an error: a source carrying no symbol face should
	// decline a request for one rather than return a text face, because substituting Latin letters
	// for mathematics draws a page that looks like text and says something else.
	Face(FaceRequest) (*TrueType, bool)
}

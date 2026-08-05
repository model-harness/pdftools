package font

import "github.com/model-harness/pdftools/font/cmap"

// Glyph is one character code decoded: what it means, how far it advances, and
// what it was written as.
//
// All four fields travel together because the caller needs them together. A
// consumer inferring inter-word spaces compares Width against the gap it measures
// in device space, and one that cannot also see Bytes cannot tell a two-byte code
// 32 (which /Tw must not affect) from a single-byte space (which it must). Keeping
// them in one struct is what stops that rule from being lost.
type Glyph struct {
	// Code is the character code as written, big-endian.
	Code uint32

	// Bytes is how many bytes Code occupied in the string. Always 1 for a simple
	// font; one to four for a composite one.
	Bytes int

	// CID is the glyph identifier the code maps to, for a composite font. Equal to
	// Code for a simple font, where the two are the same thing.
	CID uint32

	// Text is what the glyph means, which may be several characters for a ligature
	// or empty when the font gives no way to tell. Empty is a real answer and not
	// an error: a symbolic font with no /ToUnicode has genuinely undecodable codes,
	// and substituting U+FFFD would put noise in the output.
	Text string

	// Width is the horizontal advance in 1/1000 em, whatever the font's kind. The
	// caller scales it by font size and any horizontal scaling.
	//
	// 1/1000 is the glyph space of every font kind except Type 3, whose glyph space
	// is whatever its /FontMatrix says (§9.6.4). Those advances are converted into
	// these units when the font loads, so a caller never asks what kind it holds —
	// and must not apply a /FontMatrix again.
	Width float64
}

// Decode splits a PDF string into glyphs.
//
// Splitting and decoding happen together because they cannot be separated
// correctly: how many bytes the next code occupies is a question only the font's
// CMap can answer, and a caller that splits first has already had to guess. This
// is the specific mistake behind extracted text that has plausible characters in
// an implausible order.
func (f *Font) Decode(s []byte) []Glyph {
	if len(s) == 0 {
		return nil
	}
	if f.Kind == Simple {
		out := make([]Glyph, len(s))
		for i, b := range s {
			code := uint32(b)
			out[i] = Glyph{
				Code:  code,
				Bytes: 1,
				CID:   code,
				Text:  f.simpleText(b),
				Width: f.Width(code, code),
			}
		}
		return out
	}

	codes := f.cmap.Codes(s)
	out := make([]Glyph, len(codes))
	for i, c := range codes {
		cid, ok := f.cmap.CID(c.Value)
		if !ok {
			// No CID for this code. Falling back to the code itself is what an
			// Identity mapping would give, which is right more often than 0 (the
			// notdef glyph) and never worse: a wrong CID misses a width, while
			// notdef guarantees one.
			cid = c.Value
		}
		out[i] = Glyph{
			Code:  c.Value,
			Bytes: c.Bytes,
			CID:   cid,
			Text:  f.compositeText(c.Value),
			Width: f.Width(c.Value, cid),
		}
	}
	return out
}

// Text returns what a character code means, without measuring it. Decode is the
// normal entry point; this exists for callers that already know the code.
func (f *Font) Text(code uint32) string {
	if f.Kind == Simple {
		if code > 0xFF {
			return ""
		}
		return f.simpleText(byte(code))
	}
	return f.compositeText(code)
}

// simpleText resolves a single-byte code.
//
// /ToUnicode wins over the encoding because it is the font's own statement about
// its codes, while an encoding is an inference from a name. They usually agree;
// where they do not, a subset font with a rearranged encoding is the likely reason
// and /ToUnicode is the one that was written for this document.
func (f *Font) simpleText(code byte) string {
	if f.toUnicode != nil {
		if s, ok := f.toUnicode.Text(uint32(code)); ok && s != "" {
			return s
		}
	}
	if f.enc != nil {
		return f.enc.Text(code)
	}
	return ""
}

// compositeText resolves a composite font's code.
//
// Only /ToUnicode can answer: a CID is an index into a glyph set, and nothing
// about it implies a character. This is why 92 of 92 composite fonts in this
// repo's corpus carry a /ToUnicode stream — without one the text is genuinely
// unrecoverable from the file, and OCR is the only remaining route.
func (f *Font) compositeText(code uint32) string {
	if f.toUnicode == nil {
		return ""
	}
	s, _ := f.toUnicode.Text(code)
	return s
}

// Width returns a glyph's horizontal advance in glyph space units.
//
// Both the code and the CID are taken because the two font kinds index their
// metrics differently — a simple font by code offset from /FirstChar, a composite
// font by CID — and a caller holding a Glyph has both.
func (f *Font) Width(code, cid uint32) float64 {
	if f.Kind == Composite {
		if w, ok := f.cidWidths[cid]; ok {
			return w
		}
		return f.defaultWidth
	}

	if i := int(code) - f.firstChar; i >= 0 && i < len(f.widths) {
		// A zero in /Widths is ambiguous: it is a legal advance for a combining
		// mark, and it is also what a producer writes for an unused code. Taken at
		// face value, because a font declaring zero is more likely to mean it than
		// to be wrong, and overriding it would break every accent that relies on it.
		return f.widths[i]
	}

	// Outside /Widths, or no /Widths at all. The standard-14 metrics are the
	// answer for the second case, which is the only reason a font may legally omit
	// /Widths in the first place.
	//
	// Not for Type 3, on two counts. Helvetica's advance for "A" says nothing about
	// a font whose "A" is a content stream in its own /CharProcs, so the number
	// would be a guess dressed as a metric. And it would be a guess in the wrong
	// unit: this branch returns 1/1000 directly while a Type 3 font's own widths
	// have been scaled out of its /FontMatrix glyph space, so one uncovered code in
	// a font with a 0.00836 matrix would advance 8.36x its neighbours. /MissingWidth
	// below is the entry the specification provides for this, and it scales.
	if f.Subtype != "Type3" && f.enc != nil && code <= 0xFF {
		if w, ok := StandardWidth(f.BaseFont, f.enc.Glyph(byte(code))); ok {
			return float64(w)
		}
	}
	return f.defaultWidth
}

// SpaceWidth returns the advance of the space glyph, or 0 when the font has none.
//
// Exposed because it is the denominator of the space-inference threshold, which is
// what turns glyph positions back into words. A consumer comparing gaps in
// absolute units gets a threshold that is wrong at every font size, so the
// comparison has to be relative to this.
func (f *Font) SpaceWidth() float64 { return f.spaceWidth }

// measureSpace finds the advance of the space glyph once, at load time.
//
// Simple fonts are asked for code 32 only when that code actually maps to the
// space glyph: in a symbolic font with a rearranged encoding, code 32 is often a
// visible glyph, and taking its width as the space width would set the threshold
// for the whole page from an arbitrary character.
func (f *Font) measureSpace() float64 {
	if f.Kind == Composite {
		// A composite font's space is whichever code maps to U+0020, which is only
		// discoverable through /ToUnicode: a CID by itself carries no character
		// meaning.
		if f.toUnicode != nil {
			if code, ok := f.toUnicode.CodeForText(" "); ok {
				cid, ok := f.cmap.CID(code)
				if !ok {
					cid = code
				}
				return f.Width(code, cid)
			}
		}
		return f.defaultWidth
	}

	if f.enc != nil && f.enc.Text(' ') == " " {
		return f.Width(' ', ' ')
	}
	// No code maps to a space. A width of 0 tells the caller to fall back to its
	// own policy rather than trusting a number this font does not support.
	return 0
}

// CMap returns the font's encoding CMap, or nil for a simple font. Exposed so a
// caller can ask how a string splits without decoding it.
func (f *Font) CMap() *cmap.CMap { return f.cmap }

// HasToUnicode reports whether the font carries a /ToUnicode CMap. This is the
// signal that decides whether a page's text is recoverable at all, which is what
// routes a page to OCR.
func (f *Font) HasToUnicode() bool { return f.toUnicode != nil }

// GlyphName returns the glyph name a simple font's code maps to, or "" for a
// composite font or an unmapped code. Diagnostic: it is what `probe` reports and
// what makes an encoding problem legible.
func (f *Font) GlyphName(code byte) string {
	if f.enc == nil {
		return ""
	}
	return f.enc.Glyph(code)
}

package doc

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The list marker vocabulary, and the two operations that move a marker out of a
// block's text into Block.Marker.
//
// It lives in this package rather than in layout because both producers need it and
// neither may depend on the other. layout infers a list item from the glyph a page
// draws; sectionize reads one the structure tree declares, and then has to handle the
// items whose producer declared the role without declaring the label — measured at
// 1286 of 1407 on disk. Two copies of a twelve-glyph allowlist is the split
// Block warns against for space inference, half the policy in one producer and half
// in the other, and the failure mode is that the two disagree about the same document.
//
// The marker leaves the text in whichever producer recognized it, and does not leave
// the model: it goes into Block.Marker rather than being dropped, so a sink that can
// render an ordered list has the label to render. That is Docling's arrangement —
// marker and text as separate fields on a list item — and it is the reason a numbered
// label is representable here at all.

// listMarkers are the glyphs a producer sets as an unordered list's bullet.
//
// An allowlist rather than a character class, because "starts with punctuation" is
// hopeless: 20125 untagged paragraph blocks across the corpus open with 190 distinct
// non-alphanumeric runes, and the common ones are not markers at all — 437 open with
// "/", 256 with "(", 134 with a quote. The glyphs below are what a survey of that tally
// leaves once each candidate's own occurrences are read.
//
// The two U+F0xx entries are Private Use Area codepoints, which look like a mistake and
// are not: Symbol and Wingdings have no Unicode mapping for their bullet, so a producer
// setting one emits a PUA codepoint and the extractor faithfully reports it. F0B7 is
// Symbol's bullet and F06E is Wingdings' filled square, both measured in the corpus.
// This is the same glyph-set debt DESIGN.md records for ZapfDingbats.
//
// Deliberately excluded: "*", "-", "·" and ">". Each occurs block-initially in the
// corpus and every occurrence read was something else — C code (`*/ fz_stream *…`),
// command-line flags (`-o - output file name`), and Annex D's glyph-name table rows
// (`*  asterisk  052  052`). A hyphen especially is a Markdown marker but not a PDF one;
// producers set a real bullet glyph.
//
// The allowlist is confirmed by declared evidence, which is the strongest check
// available for a heuristic like this. Of the corpus's tagged list items that both open
// with one of these glyphs and declare a /Lbl saying what their label is, the label's
// first rune is that glyph in 121 of 121 and disagrees in 0.
var listMarkers = map[rune]bool{
	'•':      true, // BULLET
	'‣':      true, // TRIANGULAR BULLET
	'⁃':      true, // HYPHEN BULLET
	'■':      true, // BLACK SQUARE
	'▪':      true, // BLACK SMALL SQUARE
	'○':      true, // WHITE CIRCLE
	'●':      true, // BLACK CIRCLE
	'◦':      true, // WHITE BULLET
	'–':      true, // EN DASH
	'—':      true, // EM DASH
	'\uf06e': true, // Wingdings filled square, via the PUA
	'\uf0b7': true, // Symbol bullet, via the PUA
}

// ListMarker returns the marker rune txt opens with, or zero.
//
// It trims the text itself rather than trusting a caller to, which is what makes the two
// conditions below sufficient: on trimmed text a marker followed by whitespace must have
// something after that whitespace, since the last rune is not one. A separate "and
// content follows" check reads like the third requirement and is unreachable — a
// mutation removing it survives every test, which is how it was found.
//
// The separator is what distinguishes a marker from the same glyph used as a character:
// every one of the 1302 bullet-initial blocks in the corpus has it and none is glued to
// its text, while the excluded "-" is glued in 12 of its 13 occurrences. The length
// requirement is the other measured case — mupdf_explored.pdf has blocks that are a lone
// Wingdings square with no text, which are decoration and not items.
//
// Exported because the decision to promote a block and the act of stripping it are
// separate steps in layout: a block that will not be promoted must not have its text
// edited, so the test has to be available without the mutation.
func ListMarker(txt string) rune {
	rs := []rune(strings.TrimSpace(txt))
	if len(rs) < 2 || !listMarkers[rs[0]] {
		return 0
	}
	if !unicode.IsSpace(rs[1]) {
		return 0
	}
	return rs[0]
}

// orderedForms are the ordered-label shapes a producer writes.
//
// A closed list of shapes rather than "a number then punctuation", because the risk here is
// not which glyph follows the number — it is what else in a document opens with one.
//
// These five are exactly the forms the corpus contains, and the tally is the whole
// justification: of the 260 items layout.OrderedLists promotes across all 50 documents, "a)"
// is 174, "n." 43, "[n]" 21, "n)" 17 and "a." 5. Parenthesized labels — "(1)" and "(a)" —
// look like obvious siblings of "[1]" and were written, measured at **zero occurrences**, and
// removed. Admitting a sixth form on the strength of it seeming plausible is the speculative
// code this repo does not keep: no fixture could reach it, so nothing would catch it going
// wrong. If one turns up, the evidence arrives with it.
//
// The delimiter is required and is what does the separating. A numbered *heading* is
// "7.4 Filters" or "1 Scope" — a clause number followed by space, no delimiter — and a
// dotted number has no single sequence value to increment, which is why "N.N" cannot form a
// run at all and needs no exclusion. Measured over every document on disk, no dotted clause
// number chains under layout.OrderedLists' rule.
var orderedForms = []struct {
	// prefix and suffix bracket the label: ("", ".") reads "1." and ("[", "]") reads "[1]".
	prefix, suffix string
	// alpha reads a single letter rather than digits.
	alpha bool
}{
	{"", ".", false},  // 1.
	{"", ")", false},  // 1)
	{"[", "]", false}, // [1]
	{"", ".", true},   // a.
	{"", ")", true},   // a)
}

// OrderedLabel returns the ordered label txt opens with and its sequence value, or "" and 0.
//
// The value is what makes a run checkable: 1 for "1." and for "a.", so a rule can require
// that consecutive items increment by one. Letters are single and lowercase only — "aa." is
// not a form on disk, and an uppercase letter opens far too much prose ("A. " begins a
// sentence) to admit on this evidence.
//
// The separator requirement is the same one ListMarker makes and it matters more here,
// because the label is far more common as ordinary text than a bullet glyph is: "1.5" and
// "Figure 1." must not read as labels, and neither does, the first for having no separator
// and the second for not being at the start.
//
// Exported for the same reason ListMarker is: deciding to promote a block and editing its
// text are separate steps, and a block that will not be promoted must not be edited.
func OrderedLabel(txt string) (string, int) {
	rs := []rune(strings.TrimLeftFunc(txt, unicode.IsSpace))
	for _, f := range orderedForms {
		lbl, val, ok := matchForm(rs, f.prefix, f.suffix, f.alpha)
		if ok {
			return lbl, val
		}
	}
	return "", 0
}

func matchForm(rs []rune, prefix, suffix string, alpha bool) (string, int, bool) {
	i := 0
	for _, p := range prefix {
		if i >= len(rs) || rs[i] != p {
			return "", 0, false
		}
		i++
	}
	start, val := i, 0
	if alpha {
		if i >= len(rs) || rs[i] < 'a' || rs[i] > 'z' {
			return "", 0, false
		}
		val = int(rs[i]-'a') + 1
		i++
	} else {
		// Three digits at most. A longer run of them is a year, a byte count or a code
		// point, not a list label, and the corpus's longest ordered label is two digits.
		for i < len(rs) && i-start < 3 && rs[i] >= '0' && rs[i] <= '9' {
			val = val*10 + int(rs[i]-'0')
			i++
		}
		if i == start {
			return "", 0, false
		}
	}
	for _, s := range suffix {
		if i >= len(rs) || rs[i] != s {
			return "", 0, false
		}
		i++
	}
	// A separator, then content. Both required, for ListMarker's reasons: without the
	// separator "1.5" is a label, and without content a lone "1." is a table cell or a
	// figure number rather than an item.
	if i >= len(rs) || !unicode.IsSpace(rs[i]) {
		return "", 0, false
	}
	if strings.TrimSpace(string(rs[i:])) == "" {
		return "", 0, false
	}
	return string(rs[:i]), val, true
}

// StripMarker moves a leading marker glyph out of the block's spans into Marker, and
// reports whether it found one.
//
// For the producer that has no declaration to read: the marker is only in the drawn
// text, so recognizing it and removing it are the same act. ListMarker gates it, so the
// separator and length requirements apply here too — a lone bullet with no text is
// page decoration and keeps its glyph.
//
// The span walk is not simply "edit the first span". A block's text is its spans
// concatenated with no separator, so the marker and the whitespace after it can be
// split across spans in three ways, each of which occurs on disk: the marker with its
// separator in one span, the marker alone with the separator opening the next (a bold
// en dash followed by a roman body, which is testdata/reference/lists.pdf's nested
// item), and a leading span holding nothing but whitespace with the marker in the one
// after it. Stopping at the first non-empty span leaves "- • Item." in the output.
//
// unicode.IsSpace rather than a byte cutset: producers separate a marker from its text
// with U+00A0 routinely, and ListMarker admits the block on that basis, so the strip
// must accept the same separators the gate did.
//
// A span the strip empties stays in place rather than being removed, so the span indices
// a caller holds stay valid and Span.MCID survives for diagnosis; an empty span writes
// nothing.
func (b *Block) StripMarker() bool {
	if ListMarker(b.Text()) == 0 {
		return false
	}
	found := rune(0)
	for i := range b.Spans {
		s := &b.Spans[i]
		if s.Text == "" {
			continue
		}
		if found == 0 {
			rest := strings.TrimLeftFunc(s.Text, unicode.IsSpace)
			if rest == "" {
				// All whitespace. ListMarker read the block's text with its leading
				// space trimmed, so the marker is in a later span and this one is
				// part of the separator: empty it and keep looking.
				s.Text = ""
				continue
			}
			r, n := utf8.DecodeRuneInString(rest)
			if !listMarkers[r] {
				// Unreachable while ListMarker gates entry above, since it matched
				// the first rune of this same text. Kept as the honest answer if the
				// two ever disagree: leave the text alone rather than edit a block
				// that is not what this was told it was.
				return false
			}
			found = r
			s.Text = strings.TrimLeftFunc(rest[n:], unicode.IsSpace)
		} else {
			s.Text = strings.TrimLeftFunc(s.Text, unicode.IsSpace)
		}
		if s.Text != "" {
			break
		}
	}
	if found == 0 {
		return false
	}
	b.Marker = string(found)
	return true
}

// StripOrderedLabel moves a leading ordered label out of the block's spans into Marker, and
// reports whether it found one.
//
// Separate from StripMarker rather than folded into it, because the two remove different
// shapes and only one of them is a single rune. A bullet is one glyph, so StripMarker can
// decode a rune and trim; a label is up to five ("[100]") and may be split across spans by a
// style change on the delimiter — a producer setting the number bold and the bracket roman
// gives "1" and ") text" — so this consumes a rune count across as many spans as it takes.
//
// Marker holds the label with its delimiter and without the separator ("1.", "[3]", "a)"),
// which is what a sink needs: Block.Enumerated reads it to tell an ordered item from a
// bullet, and the markdown sink reads the digits back out to write CommonMark's own syntax.
// The separator goes because it is spacing rather than content, exactly as in StripMarker.
//
// An empty span left behind stays in place rather than being removed, for StripMarker's
// reason: span indices a caller holds stay valid, and Span.MCID survives for diagnosis.
func (b *Block) StripOrderedLabel() bool {
	lbl, _ := OrderedLabel(b.Text())
	if lbl == "" {
		return false
	}
	// The count includes any whitespace before the label, which Text() carries and
	// OrderedLabel trimmed off before matching.
	n := len([]rune(lbl))
	txt := b.Text()
	n += len([]rune(txt)) - len([]rune(strings.TrimLeftFunc(txt, unicode.IsSpace)))

	for i := range b.Spans {
		s := &b.Spans[i]
		if n <= 0 {
			// The label is gone; close the gap it left, then stop at the first span with
			// content so the trim cannot reach into the item's own prose.
			if s.Text == "" {
				continue
			}
			s.Text = strings.TrimLeftFunc(s.Text, unicode.IsSpace)
			if s.Text != "" {
				break
			}
			continue
		}
		rs := []rune(s.Text)
		if len(rs) <= n {
			n -= len(rs)
			s.Text = ""
			continue
		}
		s.Text = strings.TrimLeftFunc(string(rs[n:]), unicode.IsSpace)
		n = 0
		if s.Text != "" {
			break
		}
	}
	b.Marker = lbl
	return true
}

// SetMarker records a marker the producer declared, and closes the gap its removal
// leaves in the text.
//
// For the producer that has a declaration to read: sectionize takes the label's spans
// out of the item before gathering it, so there is no marker left in the text to find —
// but there is usually whitespace where it was. Measured over every tagged list item on
// disk that declares a /Lbl, dropping the label's spans leaves the item's text opening
// with whitespace in 133 of 147 cases, which a sink writing its own "- " renders as two
// spaces.
//
// The trim is the same one StripMarker does after the marker, and stops at the first
// span with content left for the same reason: it must not reach into the item's own
// prose.
func (b *Block) SetMarker(marker string) {
	b.Marker = marker
	for i := range b.Spans {
		s := &b.Spans[i]
		if s.Text == "" {
			continue
		}
		s.Text = strings.TrimLeftFunc(s.Text, unicode.IsSpace)
		if s.Text != "" {
			return
		}
	}
}

// Enumerated reports whether the block's marker is an ordered label — a number or a
// letter — rather than a bullet.
//
// Derived from Marker rather than stored beside it, because the two cannot disagree:
// the marker either is a single glyph from the allowlist or it is something a producer
// counted. Docling carries this as its own field; here a second field could be set
// inconsistently with the first, and there is nothing it could express that this does
// not.
//
// It exists because a sink cannot render what it cannot distinguish. Markdown has no
// syntax for "[1]" as a list marker, so a sink emitting "- " has to put an ordered
// label back into the line, and dropping it instead would lose text the page draws.
func (b Block) Enumerated() bool {
	if b.Marker == "" {
		return false
	}
	r, n := utf8.DecodeRuneInString(b.Marker)
	return n != len(b.Marker) || !listMarkers[r]
}

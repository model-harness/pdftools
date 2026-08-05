package markdown

import (
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// inline renders a block's spans as Markdown inline content.
//
// Adjacent spans sharing emphasis are wrapped once rather than per span. The
// extractor splits a span at every style change it can see, including changes that
// produce identical Markdown — a size change inside one bold run, the same font
// re-selected — and wrapping each separately emits "**a****b**", where the four
// consecutive asterisks are a delimiter run CommonMark resolves differently than
// either pair alone.
//
// plain suppresses emphasis for contexts already inside markers.
func inline(spans []doc.Span, plain bool) string {
	var sb strings.Builder
	for i := 0; i < len(spans); {
		if strings.TrimSpace(spans[i].Text) == "" {
			// Whitespace-only: emitted as-is so word boundaries survive, but never
			// wrapped. Emphasis delimiters cannot be adjacent to the whitespace they
			// contain, so "* *" is asterisks and a space, not an emphasized space.
			sb.WriteString(spans[i].Text)
			i++
			continue
		}

		j, st := i+1, style(spans[i], plain)
		for j < len(spans) && style(spans[j], plain) == st {
			j++
		}
		writeRun(&sb, spans[i:j], st, sb.Len() == 0)
		i = j
	}
	return sb.String()
}

// mark is the Markdown emphasis a span's style maps to.
type mark int

const (
	markNone mark = iota
	markItalic
	markBold
	markBoldItalic
	markCode
)

// style maps a span's typography to a Markdown marker.
//
// Monospaced wins over bold and italic, because a code span cannot contain them:
// everything inside backticks is literal, so "`**x**`" is four asterisks on screen.
// A bold monospaced identifier loses its weight and keeps being an identifier,
// which is the half that carries meaning.
//
// Invisible text is the exception, and it is not a rare one: a scanned page's OCR
// layer is drawn in rendering mode 3, and the fonts OCR engines use for it are
// fixed-pitch by declaration — Tesseract's GlyphLessFont sets the descriptor's
// FixedPitch flag. That flag is a true statement about a font nobody ever sees, not
// a typographic choice about the text, so honouring it wraps an entire scanned
// document in backticks. Measured on the OCR fixtures: every monospaced span in
// them is hidden, and no visible span in a 285-page manual full of real code
// samples is monospaced — so suppressing this costs no genuine code span.
func style(s doc.Span, plain bool) mark {
	if plain {
		return markNone
	}
	if s.Style.Mono && !s.Style.Hidden {
		return markCode
	}
	switch {
	case s.Style.Bold && s.Style.Italic:
		return markBoldItalic
	case s.Style.Bold:
		return markBold
	case s.Style.Italic:
		return markItalic
	}
	return markNone
}

func (m mark) delim() string {
	switch m {
	case markItalic:
		return "*"
	case markBold:
		return "**"
	case markBoldItalic:
		return "***"
	}
	return ""
}

// writeRun emits one run of same-styled spans.
//
// atStart reports that this run opens the block, which is what decides whether
// line-start-sensitive characters need escaping. It is threaded through rather than
// recomputed because only the caller knows whether anything preceded it.
func writeRun(sb *strings.Builder, spans []doc.Span, m mark, atStart bool) {
	var text strings.Builder
	for i := range spans {
		text.WriteString(spans[i].Text)
	}
	s := text.String()

	if m == markCode {
		writeCode(sb, s)
		return
	}

	d := m.delim()
	if d == "" {
		escapeInto(sb, s, atStart)
		return
	}

	// Whitespace has to sit outside the delimiters. CommonMark requires the opening
	// run not be followed by whitespace and the closing run not be preceded by it,
	// so "**bold **next" emits literal asterisks — and a span boundary landing on
	// the space after a bold word is the ordinary case, not a rare one.
	lead := s[:len(s)-len(strings.TrimLeft(s, " \t"))]
	trail := s[len(strings.TrimRight(s, " \t")):]
	body := s[len(lead) : len(s)-len(trail)]
	if body == "" {
		escapeInto(sb, s, atStart)
		return
	}

	sb.WriteString(lead)
	sb.WriteString(d)
	escapeInto(sb, body, false)
	sb.WriteString(d)
	sb.WriteString(trail)
}

// writeCode emits s as a code span.
//
// The fence is one backtick longer than the longest run inside, per CommonMark's
// code-span rule, so an identifier containing a backtick does not terminate its own
// span. A body that begins or ends with a backtick or is entirely spaces is padded:
// without the padding "“ `x` “" would parse its delimiter run as four backticks.
//
// Nothing inside is escaped — a code span is literal by definition, and escaping it
// would emit the backslashes.
func writeCode(sb *strings.Builder, s string) {
	// The one substitution a verbatim context still makes — see sanitize.
	s = sanitize(s)
	lead := s[:len(s)-len(strings.TrimLeft(s, " \t"))]
	trail := s[len(strings.TrimRight(s, " \t")):]
	body := s[len(lead) : len(s)-len(trail)]
	if body == "" {
		sb.WriteString(s)
		return
	}

	fence := spanFence(body)
	pad := ""
	if strings.HasPrefix(body, "`") || strings.HasSuffix(body, "`") {
		pad = " "
	}
	sb.WriteString(lead)
	sb.WriteString(fence)
	sb.WriteString(pad)
	sb.WriteString(body)
	sb.WriteString(pad)
	sb.WriteString(fence)
	sb.WriteString(trail)
}

// escapeInto writes s with Markdown metacharacters escaped.
//
// The policy is deliberately narrow, and the reason is that extracted prose
// contains most of Markdown's syntax as ordinary text. A PDF specification is full
// of "<</Type /Page>>", "snake_case" identifiers, hyphenated compounds, and "*"
// footnote markers. Escaping every reserved character turns that into a document
// made of backslashes; escaping none of it produces one that renders wrong. So each
// character is escaped only in the positions where it actually means something:
//
//	always          \ ` * [ ] < |
//	word-external   _ ~
//	block start     # > - + digit-then-dot
//	before [        !
//
// The distinctions are not stylistic. "_" between two alphanumerics cannot open
// emphasis in CommonMark — that rule exists so identifiers survive — so escaping it
// there would corrupt every identifier in the corpus to no benefit. "-" is a list
// marker only at the start of a line, so escaping it mid-sentence would put a
// backslash in every hyphenated word on the page.
//
// atStart reports that s begins the block, and only then are the line-start
// characters live.
func escapeInto(sb *strings.Builder, s string, atStart bool) {
	// Grown once: escaping adds at most a byte per character and most text needs
	// none, so this is the common case's only allocation.
	sb.Grow(len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]
		if isControl(c) {
			sb.WriteString(replacement)
			continue
		}
		switch c {
		case '\\', '`', '*', '|':
			sb.WriteByte('\\')
			sb.WriteByte(c)
			continue

		case '[':
			// A bracket opens a link only if a "]" follows it and is itself followed by
			// "(", "[", or ":". A bare "[1]" is literal text in CommonMark, and "[1]" is
			// how every citation in every paper in the corpus is written — escaping all
			// of them would put a backslash on both sides of every reference for no
			// change in what renders.
			if linkOpen(s, i) {
				sb.WriteByte('\\')
			}
			sb.WriteByte(c)
			continue

		case ']':
			// Never escaped. A closing bracket with no live opener is literal, and the
			// opener is escaped when it is live, which already breaks the pair.
			sb.WriteByte(c)
			continue

		case '<':
			// Escaped only when it could start a tag or an autolink. "<<" cannot — and
			// "<</Type /Page>>" is what a PDF dictionary looks like, which this corpus
			// is made of. An XMP fragment like "<pdfd:conformsTo>" can, and would
			// otherwise be swallowed as raw HTML and vanish from the output.
			if tagOpen(s, i) {
				sb.WriteByte('\\')
			}
			sb.WriteByte(c)
			continue

		case '_', '~':
			// Intraword is safe and common; at a word edge it is a delimiter.
			if wordByte(prevByte(s, i)) && wordByte(nextByte(s, i)) {
				sb.WriteByte(c)
				continue
			}
			sb.WriteByte('\\')
			sb.WriteByte(c)
			continue

		case '!':
			// Only meaningful immediately before a link opener, and "[" is escaped
			// anyway — but the pair is escaped together so the intent survives if that
			// changes.
			if i+1 < len(s) && s[i+1] == '[' {
				sb.WriteByte('\\')
			}
			sb.WriteByte(c)
			continue

		case '&':
			// A bare ampersand is text in Markdown; only a well-formed entity
			// reference is consumed. "AT&T" stays as written, "&amp;" does not silently
			// become "&".
			if entityAt(s, i) {
				sb.WriteString("&amp;")
				continue
			}
			sb.WriteByte(c)
			continue

		case '#', '>', '-', '+', '=':
			// Block-level markers, live only where a block can begin. "=" is
			// setext-heading underlining, which applies to a whole line.
			if atStart && i == 0 {
				sb.WriteByte('\\')
			}
			sb.WriteByte(c)
			continue
		}

		// An ordered-list marker is a digit run then "." or ")" then whitespace.
		// Escaping the delimiter rather than the digits keeps the number readable.
		if atStart && (c == '.' || c == ')') && orderedMarker(s, i) {
			sb.WriteByte('\\')
			sb.WriteByte(c)
			continue
		}
		sb.WriteByte(c)
	}
}

// replacement is U+FFFD, which CommonMark §2.3 requires U+0000 be replaced with
// before parsing. Applied to the whole C0 set and to DEL rather than to NUL alone —
// see isControl.
const replacement = "�"

// isControl reports whether c is a control byte that must not reach the output.
//
// Tab, newline, and carriage return are excluded because they are structural: a tab
// is legal inline whitespace and the two line breaks are how the writer separates
// blocks. Everything else in C0, and DEL, is a byte with no textual meaning that a
// PDF should not have produced, and three of them do exist in this repo's corpus —
// PDF20_AN001-BPC.pdf draws a NUL between two sentences, from a /ToUnicode entry
// mapping a code to U+0000.
//
// Substituted rather than dropped, which is the difference between a reader seeing
// that something was there and the text silently closing over it. Substituted rather
// than escaped as "\x00", because that is a five-character lie about what the page
// says. The one place this must not run is inside a fenced code block, where the
// bytes are the content — and escapeInto is not called there.
func isControl(c byte) bool {
	return c < 0x20 && c != '\t' && c != '\n' && c != '\r' || c == 0x7F
}

// sanitize is isControl's substitution for the two paths escapeInto does not reach:
// a code span and a fenced code block, both of which are verbatim.
//
// Verbatim means no backslashes, and this adds none — a control byte has no escape in
// a code span anyway, since the whole point of one is that "\x00" inside it would
// render as those four characters. So the invariant the output holds is the same
// everywhere: no byte outside tab, newline, and carriage return that a Markdown parser
// is required to replace before it starts.
//
// Returns s unchanged and allocates nothing in the ordinary case, which is every code
// block in the corpus.
func sanitize(s string) string {
	i := 0
	for ; i < len(s); i++ {
		if isControl(s[i]) {
			break
		}
	}
	if i == len(s) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	sb.WriteString(s[:i])
	for ; i < len(s); i++ {
		if isControl(s[i]) {
			sb.WriteString(replacement)
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// linkOpen reports whether the "[" at s[i] could open a link, image, or reference
// definition.
//
// It needs a matching "]" on the same string followed by "(", "[", or ":". Nesting
// is not tracked: the first "]" is taken as the match, which over-reports on
// "[a [b](c)" and so escapes one bracket that did not need it. Under-reporting
// would emit a link where the text said none, which is the worse direction.
func linkOpen(s string, i int) bool {
	for j := i + 1; j < len(s); j++ {
		if s[j] != ']' {
			continue
		}
		if j+1 >= len(s) {
			return false
		}
		switch s[j+1] {
		case '(', '[', ':':
			return true
		}
		return false
	}
	return false
}

// tagOpen reports whether the "<" at s[i] could begin an HTML tag or an autolink.
//
// A tag starts with a letter or "/", a comment with "!", a processing instruction
// with "?". An autolink needs a scheme and a ":" before the closing ">". Anything
// else — "<<", "< ", "<3" — is literal text, and PDF syntax is full of it.
func tagOpen(s string, i int) bool {
	// The second "<" of a "<<" pair is a PDF dictionary opener, not a tag, even though
	// what follows it looks like one: "<</Type /Page>>" has "/Type" after the second
	// "<" and that is a name object, not an HTML closing tag. The corpus is made of
	// these, so getting it wrong puts a backslash in front of every dictionary in the
	// specification.
	if prevByte(s, i) == '<' {
		return false
	}

	j := i + 1
	if j >= len(s) {
		return false
	}
	switch c := s[j]; {
	case c == '!', c == '?', c == '/':
		return true
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	}
	return false
}

// wordByte reports whether b is part of a word for the purpose of the intraword
// underscore rule. Zero means no such byte, which is a word edge.
//
// Bytes above 0x7f count as word bytes: they are the continuation and lead bytes of
// a multi-byte rune, and every one of those runes is a letter in the text this
// package sees. Treating them as edges would escape the underscore in "größe_x".
func wordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b > 0x7f:
		return true
	}
	return false
}

func prevByte(s string, i int) byte {
	if i == 0 {
		return 0
	}
	return s[i-1]
}

func nextByte(s string, i int) byte {
	if i+1 >= len(s) {
		return 0
	}
	return s[i+1]
}

// entityAt reports whether an HTML entity reference starts at s[i], which must be
// "&".
func entityAt(s string, i int) bool {
	j := i + 1
	if j < len(s) && s[j] == '#' {
		j++
	}
	start := j
	for j < len(s) && isAlnum(s[j]) {
		j++
	}
	return j > start && j < len(s) && s[j] == ';'
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// orderedMarker reports whether s[i], which is "." or ")", is the delimiter of an
// ordered-list marker opening the line.
//
// Two conditions, and the second is what keeps clause numbers intact. Everything
// before the delimiter must be digits, and the delimiter must be followed by
// whitespace or end the line — CommonMark requires a space after the marker. Without
// that check "7.5.8 Filters" escapes its first period, and the corpus is a document
// whose every heading is numbered that way.
func orderedMarker(s string, i int) bool {
	if i == 0 {
		return false
	}
	for k := 0; k < i; k++ {
		if s[k] < '0' || s[k] > '9' {
			return false
		}
	}
	switch nextByte(s, i) {
	case 0, ' ', '\t':
		return true
	}
	return false
}

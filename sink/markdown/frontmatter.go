package markdown

import (
	"strconv"
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// frontmatter writes a YAML block delimited by "---".
//
// Written by hand rather than with a YAML library, and that is a considered choice
// rather than an avoided dependency. The output is a flat map of strings, integers,
// and booleans — there is no nesting, no anchors, and no custom types — so the whole
// of what a marshaller would provide is the quoting rule in yamlString, which is
// eighteen lines. Against that, gopkg.in/yaml.v3 sorts or reorders keys depending on
// the type it is given, and the field order here is meant to be stable so two
// conversions of the same document diff cleanly.
//
// page is 0 for a whole-document conversion and the 1-based number for a split page.
// The distinction is in the output because a reader of a single page file needs to
// know which page it is, and a reader of a whole document would find "page: 1"
// actively wrong.
func (w *writer) frontmatter(m doc.Metadata, page, total int) {
	w.str("---\n")

	// Order is fixed: identity, then provenance, then what the extraction did. Each
	// field is omitted when empty rather than emitted as "" — an absent /Title and a
	// title that is the empty string are the same thing in a PDF, and a consumer
	// should not have to distinguish them.
	w.field("title", m.Title)
	w.field("author", m.Author)
	w.field("subject", m.Subject)
	w.field("keywords", m.Keywords)
	w.field("lang", m.Lang)

	w.field("source", m.Path)
	w.field("pdf_version", m.Version)
	w.field("creator", m.Creator)
	w.field("producer", m.Producer)
	// Emitted as the strings the file contained. A PDF date is "D:20240131120000Z"
	// and is frequently malformed; converting it to a YAML timestamp would mean
	// either dropping the ones that do not parse or emitting an invented value, and
	// the string a producer wrote is checkable against the file.
	w.field("created", m.Created)
	w.field("modified", m.Modified)

	if page > 0 {
		w.str("page: ")
		w.str(strconv.Itoa(page))
		w.nl()
	}
	if total > 0 {
		w.str("pages: ")
		w.str(strconv.Itoa(total))
		w.nl()
	}

	// tagged and encrypted are always emitted, including when false. They report
	// which extraction path ran, and for that an absent key is ambiguous — it cannot
	// be told from a key the writer did not know about — where "tagged: false" is a
	// statement.
	w.str("tagged: ")
	w.str(strconv.FormatBool(m.Tagged))
	w.nl()
	w.str("encrypted: ")
	w.str(strconv.FormatBool(m.Encrypted))
	w.nl()

	w.str("---\n\n")
	w.started, w.blank = true, true
}

func (w *writer) field(key, val string) {
	if val == "" {
		return
	}
	w.str(key)
	w.str(": ")
	w.str(yamlString(val))
	w.nl()
}

// yamlString quotes a value for YAML when it needs quoting.
//
// Double quotes with backslash escapes, which YAML defines to work like C's, rather
// than single quotes: a single-quoted YAML scalar cannot express a control
// character, and PDF metadata strings contain them — a producer that wrote a raw
// tab into /Subject is not rare.
//
// The unquoted path exists because a frontmatter block of quoted strings is harder
// to read than one where only the values that need it are quoted, and the metadata
// worth reading is mostly plain words.
func yamlString(s string) string {
	if plainYAML(s) {
		return s
	}

	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			if c < 0x20 || c == 0x7f {
				// A raw control byte makes the block unparseable, and dropping it would
				// silently alter a value. \xNN is YAML's own escape for it.
				sb.WriteString("\\x")
				const hex = "0123456789abcdef"
				sb.WriteByte(hex[c>>4])
				sb.WriteByte(hex[c&0x0f])
				continue
			}
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// plainYAML reports whether s can be written unquoted as a YAML flow scalar.
//
// Conservative on purpose: every case it rejects is still emitted correctly, just
// quoted, so a false negative costs two characters while a false positive produces a
// file that will not parse. The rejected set is what the YAML 1.2 spec makes
// special in a plain scalar, plus the whole-string forms — "true", "null", a bare
// number — that a loader would type-convert into something that is no longer the
// title of the document.
func plainYAML(s string) bool {
	if s == "" {
		return false
	}
	// Leading or trailing space is not preserved in a plain scalar.
	if s != strings.TrimSpace(s) {
		return false
	}
	// An indicator in the first position starts a different construct — a sequence,
	// a mapping, an anchor, a block scalar.
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>',
		'\'', '"', '%', '@', '`':
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			return false
		}
		// A backslash is literal in a plain scalar and an escape introducer in a
		// double-quoted one, so "source: C:\path" is correct YAML that reads as though
		// it might not be. Quoting it — as "C:\\path" — costs two characters and removes
		// the question, and every source path on this platform contains one.
		if c == '\\' {
			return false
		}
		// ": " ends a key and " #" starts a comment, anywhere in the line. A colon at
		// the very end does too. Both appear in real titles: "ISO 32000-2: Document
		// management" is the exact case.
		if c == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			return false
		}
		if c == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			return false
		}
	}
	return !yamlReserved(s)
}

// yamlReserved reports whether the whole string is a form a YAML loader resolves to
// a non-string type. "Y" as a title becoming the boolean true is YAML 1.1 behavior
// that loaders still implement, and a number-shaped version string like "1.7"
// becoming a float loses the distinction between "1.7" and "1.70".
func yamlReserved(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "null", "~", "y", "n", "yes", "no", "on", "off":
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}

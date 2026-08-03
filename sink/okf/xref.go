package okf

import (
	"strings"

	"github.com/3rg0n/pdf-spec/doc"
)

// xref turns the specification's own cross-references into links inside the bundle.
//
// This is the part of the bundle that a flat conversion cannot have. "The filters shall be
// applied as described in 7.4" is a dead string in a 1,023-page markdown file and a
// traversable edge in a knowledge base — and traversal is what a model querying the bundle
// does instead of reading all of it. The specification cross-references itself several
// thousand times; each one is a retrieval hop that does not have to be a search.
//
// Textual, not from link annotations. docs/DESIGN.md §7 wants /Annots and /Dests resolved,
// which is strictly better — it is the producer's own statement of where a reference points
// — and it is not available here: doc.Outline carries text and structure, and annotations
// are neither. Adding them means a field on doc.Page threaded through extract and
// sectionize, which is a change to the model that should happen when the links verb needs
// it and not as a side effect of a sink. Until then this reads the prose, which is where
// ISO writes its references anyway.
type xref struct {
	// byNumber maps a clause number to the file holding that clause. Only numbers, not
	// titles: a title match would link the word "Filters" wherever it appeared, and a
	// clause number is unambiguous by construction.
	byNumber map[string]string
}

func newIndex(o *doc.Outline, l *layout) *xref {
	x := &xref{byNumber: make(map[string]string)}
	o.Walk(func(s *doc.Section) bool {
		if s.Number == "" {
			return true
		}
		// First wins. Two clauses with one number is a defect in the document or in the
		// heading parse, and the earlier of them is the likelier original.
		if _, dup := x.byNumber[s.Number]; !dup {
			x.byNumber[s.Number] = rel(l.file[s])
		}
		return true
	})
	return x
}

// link rewrites cross-references in rendered markdown and reports how many it resolved.
//
// self is the path of the file being written, so that a clause quoting its own number does
// not link to itself.
func (x *xref) link(md, self string) (string, int) {
	if len(x.byNumber) == 0 {
		return md, 0
	}
	lines := strings.Split(md, "\n")
	total := 0
	// fence is the length of the open code fence, 0 when outside one. Text inside a fence
	// is verbatim by definition — sink/markdown does not even escape it — so rewriting it
	// would corrupt the one kind of content in a specification that must survive
	// byte-for-byte.
	fence := 0
	for i, ln := range lines {
		if f := fenceLen(ln); f > 0 {
			if fence == 0 {
				fence = f
			} else if f >= fence {
				fence = 0
			}
			continue
		}
		if fence > 0 {
			continue
		}
		if out, n := x.linkLine(ln, self); n > 0 {
			lines[i], total = out, total+n
		}
	}
	if total == 0 {
		return md, 0
	}
	return strings.Join(lines, "\n"), total
}

// fenceLen returns the backtick count of a line that is nothing but backticks, else 0.
// sink/markdown writes fences with no info string, so this recognizes both ends of one.
func fenceLen(ln string) int {
	t := strings.TrimRight(ln, " ")
	if len(t) < 3 {
		return 0
	}
	for i := 0; i < len(t); i++ {
		if t[i] != '`' {
			return 0
		}
	}
	return len(t)
}

func (x *xref) linkLine(ln, self string) (string, int) {
	var sb strings.Builder
	n, i := 0, 0
	code := false
	for i < len(ln) {
		switch c := ln[i]; {
		case c == '`':
			// A code span holds an identifier or a dictionary fragment, and a number
			// inside one is a literal the document is quoting, not a reference to a
			// clause.
			j := i
			for j < len(ln) && ln[j] == '`' {
				j++
			}
			code = !code
			sb.WriteString(ln[i:j])
			i = j

		case c == '\\' && i+1 < len(ln):
			// An escape written by sink/markdown's escaping policy. Both bytes pass
			// through together so that the escaped character is never read as the start
			// of a candidate or as a cue word.
			sb.WriteString(ln[i : i+2])
			i += 2

		case c == '(' && i > 0 && ln[i-1] == ']':
			// A link destination that is already there — a subclause list, or a figure
			// reference. Copied through untouched: a path contains digits and dots and
			// would otherwise look exactly like a clause number.
			j := strings.IndexByte(ln[i:], ')')
			if j < 0 {
				j = len(ln) - i
			} else {
				j++
			}
			sb.WriteString(ln[i : i+j])
			i += j

		case code || !refStart(ln, i):
			sb.WriteByte(c)
			i++

		default:
			num := clauseNumber(ln[i:])
			p, ok := x.byNumber[num]
			if num == "" || !ok || p == self || !cued(ln[:i]) {
				if num == "" {
					sb.WriteByte(c)
					i++
					continue
				}
				sb.WriteString(num)
				i += len(num)
				continue
			}
			sb.WriteString("[")
			sb.WriteString(num)
			sb.WriteString("](")
			sb.WriteString(p)
			sb.WriteString(")")
			i += len(num)
			n++
		}
	}
	return sb.String(), n
}

// refStart reports whether position i could begin a clause number: a digit or a single
// uppercase letter that is not continuing a longer token. Without the second half, the "5"
// in "ISO32000-5" and the "2" in "1.2.3" would each be tried as a reference.
func refStart(ln string, i int) bool {
	c := ln[i]
	if !isDigit(c) && !(c >= 'A' && c <= 'Z') {
		return false
	}
	if i == 0 {
		return true
	}
	p := ln[i-1]
	return !isDigit(p) && !isAlpha(p) && p != '.' && p != '-'
}

// clauseNumber reads a clause number off the front of s, or returns "".
//
// The accepted shape is two or more dot-separated components where the first is digits or a
// single uppercase letter and the rest are digits: "7.4", "7.6.4.4.8", "A.2". Two components
// minimum, because a bare "7" appears in prose as a quantity far more often than as a
// reference and the index cannot tell the difference. A trailing dot is left behind — it is
// the sentence's, not the number's.
func clauseNumber(s string) string {
	i, parts := 0, 0
	for {
		start := i
		if parts == 0 && i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
			i++
		} else {
			for i < len(s) && isDigit(s[i]) {
				i++
			}
		}
		if i == start {
			// A trailing separator with no component after it: "7.4." at the end of a
			// sentence. The number is what came before the dot.
			break
		}
		parts++
		if i < len(s) && s[i] == '.' {
			i++
			continue
		}
		break
	}
	if parts < 2 {
		return ""
	}
	return strings.TrimSuffix(s[:i], ".")
}

// cued reports whether the text immediately before a candidate marks it as a reference.
//
// A clause number and a version number and a decimal quantity are the same string, so the
// number alone cannot decide: "PDF 1.7" and "clause 1.7" both parse. The cue is the word in
// front of it, and the list is deliberately short — "in" and "of" precede more version
// numbers than clause numbers, and a missed link costs a consumer one search where a wrong
// one sends it to the wrong clause and looks authoritative doing it.
//
// Cased loosely because a sentence may open with one: "Clause 7.4 defines …".
func cued(before string) bool {
	end := len(strings.TrimRight(before, " \t"))
	if end == len(before) {
		// No space between the cue and the number, so there is no cue: this is "v1.7" or
		// "Table7.4" or the tail of something longer.
		return false
	}
	start := end
	for start > 0 {
		c := before[start-1]
		if isAlpha(c) {
			start--
			continue
		}
		break
	}
	word := before[start:end]
	if word == "" {
		// The section symbol is a cue and is not a letter. Checked on the raw bytes of
		// its UTF-8 encoding to avoid decoding the whole prefix for one comparison.
		return strings.HasSuffix(before[:end], "§")
	}
	switch strings.ToLower(word) {
	case "clause", "clauses", "subclause", "subclauses", "annex", "annexes", "see":
		return true
	}
	return false
}

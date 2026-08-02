package markdown

import (
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/doc"
)

// Escaping is where this package can actually be wrong, in both directions: text
// that renders as something it is not, and prose turned into backslashes. Every
// case below is text the corpus actually contains, and each asserts the direction
// the CommonMark rule requires rather than a preferred style.

func escaped(t *testing.T, s string) string {
	t.Helper()
	var sb strings.Builder
	escapeInto(&sb, s, false)
	return sb.String()
}

func escapedAtStart(t *testing.T, s string) string {
	t.Helper()
	var sb strings.Builder
	escapeInto(&sb, s, true)
	return sb.String()
}

func TestEscapesEmphasisAndCode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a*b", `a\*b`},
		{"a`b", "a\\`b"},
		{`a\b`, `a\\b`},
		{"a|b", `a\|b`},
	} {
		if got := escaped(t, tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// "_" between two alphanumerics cannot open emphasis in CommonMark — that rule
// exists so identifiers survive. Escaping it there would corrupt every snake_case
// name in the corpus for no change in rendering.
func TestUnderscoreIntrawordNotEscaped(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"snake_case_name", "snake_case_name"},
		{"a_1", "a_1"},
		// Non-ASCII bytes are letters in this corpus; treating them as word edges
		// would escape the underscore in "größe_x".
		{"größe_x", "größe_x"},
		// At a word edge it is a live delimiter.
		{"_emphasis_", `\_emphasis\_`},
		{"a _b", `a \_b`},
		{"a_ b", `a\_ b`},
	} {
		if got := escaped(t, tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTildeIntrawordNotEscaped(t *testing.T) {
	if got := escaped(t, "a~b"); got != "a~b" {
		t.Errorf("got %q", got)
	}
	if got := escaped(t, "~strike~"); got != `\~strike\~` {
		t.Errorf("got %q", got)
	}
}

// "[1]" is how every citation in every paper in the corpus is written, and it is
// literal text in CommonMark. Escaping all of them would put backslashes around
// every reference on the page.
func TestBracketsEscapedOnlyWhenLinkLike(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"see [1] and [2, 3]", "see [1] and [2, 3]"},
		{"Tesseract [1],", "Tesseract [1],"},
		// A live link, image, or reference definition.
		{"[text](url)", `\[text](url)`},
		{"[text][ref]", `\[text][ref]`},
		{"[ref]: url", `\[ref]: url`},
	} {
		if got := escaped(t, tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A PDF dictionary is what this corpus is made of. "<<" cannot open a tag, so
// escaping it would put a backslash in front of every dictionary in the spec.
func TestAngleBracketsEscapedOnlyWhenTagLike(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<</Type /Page>>", "<</Type /Page>>"},
		{"a < b", "a < b"},
		{"1<2", "1<2"},
		// Raw HTML would be consumed and vanish from the output. XMP fragments in
		// the corpus look exactly like this.
		{"<pdfd:conformsTo>", `\<pdfd:conformsTo>`},
		{"</Span>", `\</Span>`},
		{"<!-- c -->", `\<!-- c -->`},
	} {
		if got := escaped(t, tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A hyphen is a list marker only at the start of a line. Escaping it mid-sentence
// would put a backslash in every hyphenated word on the page.
func TestBlockMarkersEscapedOnlyAtStart(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"well-formed text", "well-formed text"},
		{"a # b", "a # b"},
		{"x > y", "x > y"},
		{"1 + 2 = 3", "1 + 2 = 3"},
	} {
		if got := escaped(t, tc.in); got != tc.want {
			t.Errorf("mid-line %q: got %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"- not a list", `\- not a list`},
		{"# not a heading", `\# not a heading`},
		{"> not a quote", `\> not a quote`},
		{"+ not a list", `\+ not a list`},
		{"=== not a heading", `\=== not a heading`},
	} {
		if got := escapedAtStart(t, tc.in); got != tc.want {
			t.Errorf("at start %q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An ordered-list marker is digits then "." or ")". Escaping the delimiter rather
// than the digits keeps the number readable.
func TestOrderedListMarkerEscapedAtStart(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1. text", `1\. text`},
		{"12) text", `12\) text`},
		// Not a marker: the digits are not the whole prefix.
		{"7.5.8 Filters", "7.5.8 Filters"},
		{"v1. text", "v1. text"},
	} {
		if got := escapedAtStart(t, tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
	// Mid-paragraph a number followed by a period is a sentence.
	if got := escaped(t, "1. text"); got != "1. text" {
		t.Errorf("mid-line: got %q", got)
	}
}

// A bare ampersand is text in Markdown; only a well-formed entity reference is
// consumed. "AT&T" must survive, "&amp;" must not silently become "&".
func TestAmpersandOnlyEscapedForEntities(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AT&T", "AT&T"},
		{"a & b", "a & b"},
		{"&amp;", "&amp;amp;"},
		{"&#169;", "&amp;#169;"},
		{"&notanentity", "&notanentity"},
	} {
		if got := escaped(t, tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBangEscapedBeforeBracket(t *testing.T) {
	if got := escaped(t, "Wow!"); got != "Wow!" {
		t.Errorf("got %q", got)
	}
	if got := escaped(t, "![img](x)"); got != `\!\[img](x)` {
		t.Errorf("got %q", got)
	}
}

// A code span is literal by definition, so escaping inside it would emit the
// backslashes.
func TestCodeSpanNotEscaped(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		para(span("a_b*c", mono)),
	}}}}
	if got := String(d, DefaultOptions); got != "`a_b*c`\n" {
		t.Errorf("got %q", got)
	}
}

// An identifier containing a backtick must not terminate its own span.
func TestCodeSpanFenceExtended(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		para(span("a`b", mono)),
	}}}}
	if got := String(d, DefaultOptions); got != "``a`b``\n" {
		t.Errorf("got %q", got)
	}
}

// A body that begins or ends with a backtick needs padding, or the delimiter run
// merges with it.
func TestCodeSpanPaddedWhenEdgeBacktick(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"`x", "`` `x ``\n"},
		{"x`", "`` x` ``\n"},
	} {
		d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
			para(span(tc.in, mono)),
		}}}}
		if got := String(d, DefaultOptions); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCodeSpanWhitespaceOutsideFence(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		para(span("x ", mono), span("after")),
	}}}}
	if got := String(d, DefaultOptions); got != "`x` after\n" {
		t.Errorf("got %q", got)
	}
}

// Escaping runs once per block, so only the first span sees the line start.
func TestOnlyFirstSpanIsAtLineStart(t *testing.T) {
	d := &doc.Document{Pages: []doc.Page{{Number: 1, Blocks: []doc.Block{
		para(span("- first"), span("- second")),
	}}}}
	got := String(d, DefaultOptions)
	if got != "\\- first- second\n" {
		t.Errorf("got %q", got)
	}
}

func TestCodeFenceLength(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "```"},
		{"a`b", "```"},
		{"a``b", "```"},
		{"a```b", "````"},
		{"a`````b", "``````"},
	} {
		if got := codeFence(tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOneLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"a\nb", "a b"},
		{"a\r\nb", "a b"},
		{"a\rb", "a b"},
	} {
		if got := oneLine(tc.in); got != tc.want {
			t.Errorf("%q: got %q", tc.in, got)
		}
	}
}

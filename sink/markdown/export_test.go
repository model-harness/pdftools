package markdown

import (
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/doc"
)

// The three exports here exist for sink/okf, which composes markdown that is neither a whole
// document nor a page. They are tested in this package because the escaping policy is this
// package's — the reason they are exported at all is so that there is exactly one of it.

func TestWriteBlocks(t *testing.T) {
	blocks := []doc.Block{
		{Role: doc.RoleHeading, Level: 2, Spans: []doc.Span{span("Filters")}},
		para(span("A stream may have a filter.")),
		{Role: doc.RoleListItem, Spans: []doc.Span{span("FlateDecode")}},
		{Role: doc.RoleListItem, Spans: []doc.Span{span("DCTDecode")}},
	}
	var sb strings.Builder
	if err := WriteBlocks(&sb, blocks, DefaultOptions); err != nil {
		t.Fatal(err)
	}

	want := "## Filters\n\nA stream may have a filter.\n\n- FlateDecode\n- DCTDecode\n"
	if sb.String() != want {
		t.Errorf("WriteBlocks:\n%q\nwant\n%q", sb.String(), want)
	}
}

func TestWriteBlocksNoFrontmatter(t *testing.T) {
	// The caller writes its own frontmatter — sink/okf's is nested where this package's is
	// flat — so this must emit none regardless of the option, which it shares with Write for
	// the sake of one Options type.
	opt := DefaultOptions
	opt.Frontmatter = true
	var sb strings.Builder
	if err := WriteBlocks(&sb, []doc.Block{para(span("Body."))}, opt); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(sb.String(), "---") {
		t.Errorf("WriteBlocks emitted frontmatter:\n%s", sb.String())
	}
}

func TestWriteBlocksEscapesLikeWrite(t *testing.T) {
	// The whole reason this is exported rather than reimplemented in sink/okf: two escaping
	// policies diverge, and the first document containing a PDF dictionary would come out
	// one way in the Markdown output and another in the bundle, from the same extraction.
	b := para(span("A dictionary <</Type /Page>> and a *marker* and snake_case."))
	var sb strings.Builder
	if err := WriteBlocks(&sb, []doc.Block{b}, DefaultOptions); err != nil {
		t.Fatal(err)
	}
	if sb.String() != render(t, b) {
		t.Errorf("WriteBlocks and Write escape differently:\n%q\n%q", sb.String(), render(t, b))
	}
}

func TestWriteBlocksSkipsArtifactsAndEmpties(t *testing.T) {
	blocks := []doc.Block{
		{Role: doc.RoleArtifact, Spans: []doc.Span{span("Page 412")}},
		para(),
		para(span("Real text.")),
	}
	var sb strings.Builder
	if err := WriteBlocks(&sb, blocks, DefaultOptions); err != nil {
		t.Fatal(err)
	}
	if sb.String() != "Real text.\n" {
		t.Errorf("WriteBlocks kept an artifact or an empty block: %q", sb.String())
	}
}

func TestYAMLStringMatchesFrontmatter(t *testing.T) {
	// Quoted only where a bare scalar would parse as something else, which is the rule
	// sink/markdown's own frontmatter follows. A second copy of it in sink/okf would drift.
	for _, c := range []struct{ in, want string }{
		{"7.4 Filters", "7.4 Filters"},
		// A bare 7.4 is a float and a bare 40 an integer; neither field is a number.
		{"7.4", `"7.4"`},
		{"40", `"40"`},
		// Reserved words and indicators.
		{"true", `"true"`},
		{"- leading dash", `"- leading dash"`},
		{"", `""`},
		// A colon not followed by a space does not end a key, so the corpus's own titles
		// and the resource URIs built from them stay plain — which is the point of the
		// unquoted path.
		{"ISO 32000-2:2020(en), Document management", "ISO 32000-2:2020(en), Document management"},
		{"iso32000-2:2020#7.4", "iso32000-2:2020#7.4"},
		// A colon that does end a key is quoted.
		{"ISO 32000-2: Document management", `"ISO 32000-2: Document management"`},
	} {
		if got := YAMLString(c.in); got != c.want {
			t.Errorf("YAMLString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLinkLabel(t *testing.T) {
	// Both brackets are escaped unconditionally, where escapeInto escapes "[" only when it
	// could open a link and "]" never — correct for prose, wrong inside a label, where the
	// first unescaped "]" ends the label and turns the rest of the title into text followed
	// by a bare URL.
	for _, c := range []struct{ in, want string }{
		{"7.4 Filters", "7.4 Filters"},
		{"Annex A [normative]", `Annex A \[normative\]`},
		{"see [1]", `see \[1\]`},
		// The rest of the policy still applies inside a label.
		{"A *marker*", `A \*marker\*`},
		{"", ""},
	} {
		if got := LinkLabel(c.in); got != c.want {
			t.Errorf("LinkLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestInlineTextIsNotBlockStart(t *testing.T) {
	// The callers all prefix something — "# " before a heading, "* " before a list item — so
	// a "-" that follows is a hyphen. Escaping it there would put a backslash in the middle
	// of a rendered line.
	if got := InlineText("-1 to 5"); got != "-1 to 5" {
		t.Errorf("InlineText escaped a leading hyphen: %q", got)
	}
	if got := InlineText("A *marker*"); got != `A \*marker\*` {
		t.Errorf("InlineText(%q) = %q", "A *marker*", got)
	}
}

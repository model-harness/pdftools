package markdown

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// TestAnUnmappedRunIsOneErr: a run of glyphs the file gives no value to is written [err] once,
// in every context the sink writes text in (ADR 0042).
func TestAnUnmappedRunIsOneErr(t *testing.T) {
	u := doc.Unmapped
	for _, c := range []struct {
		name string
		b    doc.Block
		want string
	}{
		{"inside a word", para(span("a" + u + u + "b")), "a[err]b"},
		{"one per word", para(span(u + u + " " + u)), "[err] [err]"},
		{"before a parenthesis", para(span("x " + u + "(y)")), `x \[err](y)`},
		{"before a bracket", para(span("x " + u + "[y]")), `x \[err][y]`},
		{"before a colon", para(span(u + ": y")), `\[err]: y`},
		{"at the end", para(span("x" + u)), "x[err]"},
		{"emphasized", para(span("x "), span(u+u, bold)), "x **[err]**"},
		{"in a code span", para(span("x "), span("a"+u+"b", mono)), "x `a[err]b`"},
		{"in a code block", doc.Block{Role: doc.RoleCode, Spans: []doc.Span{span("a" + u + u + "\nb")}}, "```\na[err]\nb\n```"},
		{"as a link's text", para(span(u, linked("https://example.org"))), "[[err]](https://example.org)"},
		{"in a heading", doc.Block{Role: doc.RoleHeading, Level: 2, Spans: []doc.Span{span(u + " x")}}, "## [err] x"},
		// A control byte the file maps a code to is a value, if a meaningless one, and keeps
		// the replacement character CommonMark asks for.
		{"not a control byte", para(span("a\x00b")), "a" + replacement + "b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.TrimSpace(render(t, c.b)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestAnImageIsOneLine: "[image]" for an image nothing was read off, "[tag: description]" for
// one that was, on one line, with no bracket inside to end it early (ADR 0045).
func TestAnImageIsOneLine(t *testing.T) {
	for alt, want := range map[string]string{
		"":                                     "[image]",
		"photo: a geyser erupting":             "[photo: a geyser erupting]",
		"diagram: boxes [A] and [B]\njoined":   "[diagram: boxes (A) and (B) joined]",
		"photo: a *starred* and `coded` thing": `[photo: a \*starred\* and ` + "\\`coded\\`" + ` thing]`,
	} {
		if got := strings.TrimSpace(render(t, doc.Block{Role: doc.RoleImage, Alt: alt})); got != want {
			t.Errorf("Alt %q: got %q, want %q", alt, got, want)
		}
	}
}

package layout

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
)

// wrapped builds a paragraph as extract does: one span, its lines joined by a space unless a
// line ends in a dash, which holds the word and gets none, and each line's start recorded.
func wrapped(lines ...string) doc.Block {
	var b doc.Block
	text := ""
	for i, l := range lines {
		if r, _ := utf8.DecodeLastRuneInString(text); i > 0 && !strings.ContainsRune("-­‐–", r) {
			text += " "
		}
		b.Lines = append(b.Lines, doc.Line{Span: 0, Offset: len(text)})
		text += l
	}
	b.Role = doc.RoleParagraph
	b.Spans = []doc.Span{{Text: text, MCID: -1}}
	return b
}

func prose(text string) doc.Block {
	return doc.Block{Role: doc.RoleParagraph, Spans: []doc.Span{{Text: text, MCID: -1}}, Lines: []doc.Line{{}}}
}

// lineTexts returns where each of b's lines starts, as the text from there to the next.
func lineTexts(b doc.Block) []string {
	starts := lineStarts(&b)
	text := b.Text()
	var out []string
	for k, s := range starts {
		end := len(text)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		out = append(out, text[s:end])
	}
	return out
}

// TestHyphensReadTheDocumentAsItsDictionary takes the rule a case at a time: a hyphen ending a
// line after a letter, before a lower-case one, goes when the document spells the word whole
// and never hyphenated, or when neither half is a word in it, and otherwise stays.
func TestHyphensReadTheDocumentAsItsDictionary(t *testing.T) {
	for _, c := range []struct {
		name    string
		context []string
		lines   []string
		want    string
	}{
		{"spelled whole elsewhere", []string{"The configuration file."}, []string{"its configura-", "tion drifts"}, "its configuration drifts"},
		{"spelled whole in capitals", []string{"Configuration"}, []string{"its configura-", "tion drifts"}, "its configuration drifts"},
		{"spelled hyphenated elsewhere", []string{"a cross-reference table"}, []string{"the cross-", "reference stream"}, "the cross-reference stream"},
		{"spelled hyphenated elsewhere with U+2010", []string{"a crossHY2010reference table"}, []string{"the cross-", "reference stream"}, "the cross-reference stream"},
		{"spelled both ways elsewhere", []string{"a cross-reference and a crossreference"}, []string{"the cross-", "reference stream"}, "the cross-reference stream"},
		{"neither half a word", nil, []string{"with exten-", "sive notes"}, "with extensive notes"},
		{"the first half a word", []string{"turn it over now"}, []string{"they over-", "lap here"}, "they over-lap here"},
		{"the second half a word", []string{"one lap"}, []string{"they over-", "lap here"}, "they over-lap here"},
		{"capitals after it", nil, []string{"the GREATER-", "THAN sign"}, "the GREATER-THAN sign"},
		{"a capital after it", nil, []string{"the exten-", "Sive note"}, "the exten-Sive note"},
		{"a digit before it", nil, []string{"ISO 32000-", "two"}, "ISO 32000-two"},
		{"punctuation before it", nil, []string{"x (-", "y)"}, "x (-y)"},
		{"an en dash", nil, []string{"range aENDASH", "f"}, "range aENDASHf"},
		{"a hyphen U+2010", nil, []string{"with extenHY2010", "sive notes"}, "with extensive notes"},
		{"a soft hyphen", []string{"a cross-reference"}, []string{"the crossSOFTHY", "reference"}, "the crossreference"},
		{"a soft hyphen before a capital", nil, []string{"the abSOFTHY", "Cd"}, "the abCd"},
		{"twice, at line ends only", nil, []string{"exten-", "sive and exten-", "sive"}, "extensive and extensive"},
		{"a capital first half", []string{"no overlap", "one lap"}, []string{"Over-", "lap here"}, "Overlap here"},
		{"a capital inside the second half", []string{"macOS", "ma and pa"}, []string{"on ma-", "cOS here"}, "on macOS here"},
		{"evidence opening a line", nil, []string{"a", "cross-reference, and the cross-", "reference"}, "a cross-reference, and the cross-reference"},
		{"two in a line's reach", []string{"configuration"}, []string{"an exten-", "sive configura-", "tion here"}, "an extensive configuration here"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var bs []doc.Block
			for _, s := range c.context {
				bs = append(bs, prose(fix(s)))
			}
			var lines []string
			for _, l := range c.lines {
				lines = append(lines, fix(l))
			}
			bs = append(bs, wrapped(lines...))
			d := pages(bs)
			Hyphens(d)
			got := d.Pages[0].Blocks[len(bs)-1]
			if got.Text() != fix(c.want) {
				t.Errorf("text = %q, want %q", got.Text(), fix(c.want))
			}
			// Each line still starts with its own text, less any hyphen taken from its end.
			for k, l := range lineTexts(got) {
				if want := strings.TrimSuffix(lines[k], "-"); !strings.HasPrefix(l, want[:1]) {
					t.Errorf("line %d starts %q, want %q", k, l, want)
				}
			}
		})
	}
}

// fix spells the dashes the table names, so a reader of it can see which is which.
func fix(s string) string {
	return strings.NewReplacer("SOFTHY", "­", "HY2010", "‐", "ENDASH", "–").Replace(s)
}

// TestHyphensTakeAHyphenSpanOfItsOwn: a producer sets the hyphen in a span of its own often
// enough (extract.dashHoldsTheWord), and taking it leaves that span empty and the lines after
// it where they were.
func TestHyphensTakeAHyphenSpanOfItsOwn(t *testing.T) {
	b := doc.Block{Role: doc.RoleParagraph,
		Spans: []doc.Span{{Text: "with exten", MCID: -1}, {Text: "-", MCID: -1}, {Text: "sive notes", MCID: -1}},
		Lines: []doc.Line{{Span: 0}, {Span: 2}}}
	d := pages([]doc.Block{b})
	if n := Hyphens(d); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	got := d.Pages[0].Blocks[0]
	if got.Text() != "with extensive notes" || got.Spans[1].Text != "" || got.Lines[1] != (doc.Line{Span: 2}) {
		t.Errorf("block = %+v", got)
	}
}

// TestHyphensRunTwiceAsOnce: md runs the pass before pdfspec.InferRoles and again after PageBreaks,
// and the second run judges what the first left exactly as the first did.
func TestHyphensRunTwiceAsOnce(t *testing.T) {
	d := pages([]doc.Block{prose("turn it over now"), wrapped("they over-", "lap and exten-", "sive")})
	if n := Hyphens(d); n != 1 {
		t.Fatalf("first run removed %d, want 1", n)
	}
	if n := Hyphens(d); n != 0 {
		t.Errorf("second run removed %d, want 0", n)
	}
	if got := d.Pages[0].Blocks[1].Text(); got != "they over-lap and extensive" {
		t.Errorf("text = %q", got)
	}
}

// TestHyphensNeedLines: a block no extractor drew has no lines, so no line ends to judge.
func TestHyphensNeedLines(t *testing.T) {
	b := wrapped("with exten-", "sive")
	b.Lines = nil
	d := pages([]doc.Block{b})
	if n := Hyphens(d); n != 0 || d.Pages[0].Blocks[0].Text() != "with exten-sive" {
		t.Errorf("removed %d from a block with no lines: %q", n, d.Pages[0].Blocks[0].Text())
	}
	if Hyphens(nil) != 0 {
		t.Error("a nil document had hyphens")
	}
}

// TestHyphensLeaveLaterSpansAlone: taking a hyphen moves the starts of the lines after it in its
// own span, and no others.
func TestHyphensLeaveLaterSpansAlone(t *testing.T) {
	b := doc.Block{Role: doc.RoleParagraph,
		Spans: []doc.Span{{Text: "with exten-sive notes ", MCID: -1}, {Text: "set in a style of another kind", MCID: -1}},
		Lines: []doc.Line{{Span: 0}, {Span: 0, Offset: len("with exten-")}, {Span: 1, Offset: len("set in a style ")}}}
	d := pages([]doc.Block{b})
	Hyphens(d)
	got := d.Pages[0].Blocks[0]
	if want := []string{"with exten", "sive notes set in a style ", "of another kind"}; !slices.Equal(lineTexts(got), want) {
		t.Errorf("lines = %q, want %q", lineTexts(got), want)
	}
}

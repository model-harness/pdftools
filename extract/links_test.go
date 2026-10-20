package extract

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// linkPage draws "keep gone keep" in 12pt Helvetica on a baseline at y=700, the middle word from
// x=40 to 66.688, and gives the page the annotations given.
func linkPage(t *testing.T, annots ...objects.Dict) []string {
	t.Helper()
	s := onePage("BT /F1 12 Tf 10 700 Td (keep) Tj 30 0 Td (gone) Tj 40 0 Td (keep) Tj ET")
	var arr objects.Array
	for _, a := range annots {
		arr = append(arr, a)
	}
	s.pages[0]["Annots"] = arr
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range p.Blocks {
		for _, sp := range b.Spans {
			out = append(out, strings.TrimSpace(sp.Text)+"="+sp.Link)
		}
	}
	return out
}

func uriLink(uri string, rect ...float64) objects.Dict {
	r := objects.Array{}
	for _, v := range rect {
		r = append(r, objects.Real(v))
	}
	return objects.Dict{
		"Subtype": objects.Name("Link"),
		"Rect":    r,
		"A":       objects.Dict{"S": objects.Name("URI"), "URI": objects.String(uri)},
	}
}

// TestALinkCoversTheWordsInItsArea: a URI link's area gives the words half inside it its URI,
// and splits them from the words around.
func TestALinkCoversTheWordsInItsArea(t *testing.T) {
	for _, c := range []struct {
		name  string
		annot objects.Dict
		want  string
	}{
		{"around the word", uriLink("https://example.org/a", 39, 697, 67, 710), "keep=|gone=https://example.org/a|keep="},
		{"over half of it", uriLink("https://example.org/a", 40, 697, 54, 710), "keep=|gone=https://example.org/a|keep="},
		{"under half of it", uriLink("https://example.org/a", 40, 697, 53, 710), "keep gone keep="},
		{"above the line", uriLink("https://example.org/a", 39, 705, 67, 720), "keep gone keep="},
		{"below the line", uriLink("https://example.org/a", 39, 680, 67, 703), "keep gone keep="},
		{"a mail address", uriLink("mailto:a@example.org", 39, 697, 67, 710), "keep=|gone=mailto:a@example.org|keep="},
		{"a scheme in capitals", uriLink("HTTPS://example.org", 39, 697, 67, 710), "keep=|gone=HTTPS://example.org|keep="},
		{"a script", uriLink("javascript:alert(1)", 39, 697, 67, 710), "keep gone keep="},
		{"a local file", uriLink("file:///etc/passwd", 39, 697, 67, 710), "keep gone keep="},
		{"a scheme and nothing", uriLink("https://", 39, 697, 67, 710), "keep gone keep="},
		{"a place in the document", objects.Dict{"Subtype": objects.Name("Link"),
			"Rect": objects.Array{objects.Int(39), objects.Int(697), objects.Int(67), objects.Int(710)},
			"A":    objects.Dict{"S": objects.Name("GoTo"), "D": objects.Array{}}}, "keep gone keep="},
		{"not a link", objects.Dict{"Subtype": objects.Name("Widget"),
			"Rect": objects.Array{objects.Int(39), objects.Int(697), objects.Int(67), objects.Int(710)},
			"A":    objects.Dict{"S": objects.Name("URI"), "URI": objects.String("https://example.org/w")}}, "keep gone keep="},
		{"another action", objects.Dict{"Subtype": objects.Name("Link"),
			"Rect": objects.Array{objects.Int(39), objects.Int(697), objects.Int(67), objects.Int(710)},
			"A":    objects.Dict{"S": objects.Name("Launch"), "URI": objects.String("https://example.org/l")}}, "keep gone keep="},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(linkPage(t, c.annot), "|"); got != c.want {
				t.Errorf("spans = %s, want %s", got, c.want)
			}
		})
	}
}

// TestALinkReadsItsQuadPoints: a link that wraps names one quadrilateral per line, and its /Rect
// covers the space between, so the quadrilaterals decide.
func TestALinkReadsItsQuadPoints(t *testing.T) {
	quad := uriLink("https://example.org/q", 0, 690, 120, 720)
	// The second "keep" only, its corners anticlockwise from the bottom left.
	quad["QuadPoints"] = objects.Array{objects.Real(76), objects.Real(697), objects.Real(110), objects.Real(697),
		objects.Real(110), objects.Real(710), objects.Real(76), objects.Real(710)}
	if got, want := strings.Join(linkPage(t, quad), "|"), "keep gone=|keep=https://example.org/q"; got != want {
		t.Errorf("spans = %s, want %s", got, want)
	}
}

// TestTwoLinksSplitTheirWords: two links beside each other in one run are two spans.
func TestTwoLinksSplitTheirWords(t *testing.T) {
	got := strings.Join(linkPage(t, uriLink("https://example.org/a", 39, 697, 67, 710),
		uriLink("https://example.org/b", 76, 697, 110, 710)), "|")
	if want := "keep=|gone=https://example.org/a|keep=https://example.org/b"; got != want {
		t.Errorf("spans = %s, want %s", got, want)
	}
}

// TestALinkReadsHorizontalLinesOnly: a rotated line's coordinates are along its own axis, so a
// link's area means nothing against them.
func TestALinkReadsHorizontalLinesOnly(t *testing.T) {
	r := &run{
		lines: []line{{orient: 6, frags: []frag{{text: []byte("gone"), along0: 40, along1: 66, cross: 700, height: 12, space: 3}}}},
		links: []link{{areas: []geom.Rect{{X0: 39, Y0: 697, X1: 67, Y1: 710}}, uri: "https://example.org"}},
	}
	r.linkWords()
	if r.lines[0].frags[0].link != "" {
		t.Error("a rotated line was linked")
	}
}

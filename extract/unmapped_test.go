package extract

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/objects"
)

// unmappedPage draws stream with F1 a simple font whose codes 0x41 to 0x43 and 32 are named
// /g1 to /g4, names no glyph list knows, and F2 a composite font with no /ToUnicode and no
// program, so that neither says what any of its glyphs mean.
func unmappedPage(t *testing.T, stream string) string {
	t.Helper()
	s := onePage(stream)
	simple, composite := objects.Ref{Num: 20}, objects.Ref{Num: 21}
	widths := objects.Array{}
	for range 0x43 - 32 + 1 {
		widths = append(widths, objects.Int(500))
	}
	s.objs[simple] = objects.Dict{
		"Type":      objects.Name("Font"),
		"Subtype":   objects.Name("Type1"),
		"BaseFont":  objects.Name("Unknown"),
		"FirstChar": objects.Int(32),
		"LastChar":  objects.Int(0x43),
		"Widths":    widths,
		"Encoding": objects.Dict{
			"Type": objects.Name("Encoding"),
			"Differences": objects.Array{objects.Int(32), objects.Name("g4"),
				objects.Int(0x41), objects.Name("g1"), objects.Name("g2"), objects.Name("g3")},
		},
	}
	s.objs[composite] = objects.Dict{
		"Type":     objects.Name("Font"),
		"Subtype":  objects.Name("Type0"),
		"BaseFont": objects.Name("Unknown"),
		"Encoding": objects.Name("Identity-H"),
		"DescendantFonts": objects.Array{objects.Dict{
			"Type":     objects.Name("Font"),
			"Subtype":  objects.Name("CIDFontType2"),
			"BaseFont": objects.Name("Unknown"),
			"CIDSystemInfo": objects.Dict{"Registry": objects.String("Adobe"),
				"Ordering": objects.String("Identity"), "Supplement": objects.Int(0)},
			"DW": objects.Int(600),
		}},
	}
	fonts := s.pages[0]["Resources"].(objects.Dict)["Font"].(objects.Dict)
	fonts["F1"], fonts["F2"] = simple, composite
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatal(err)
	}
	return p.Text()
}

// TestAGlyphWithNoValueIsKept: a glyph the file gives no Unicode value to is one doc.Unmapped,
// where it used to be nothing at all, and a single-byte space is still read from its gap.
func TestAGlyphWithNoValueIsKept(t *testing.T) {
	u := doc.Unmapped
	for _, c := range []struct{ name, stream, want string }{
		{"a simple font", "BT /F1 12 Tf 10 700 Td (ABC) Tj ET", u + u + u},
		{"a space between", "BT /F1 12 Tf 10 700 Td (AB CA) Tj ET", u + u + " " + u + u},
		{"a composite font", "BT /F2 12 Tf 10 700 Td <00410042> Tj ET", u + u},
		// A two-byte code 32 is a glyph like any other: only a single byte says space.
		{"a composite code 32", "BT /F2 12 Tf 10 700 Td <0041002000420043> Tj ET", u + u + u + u},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := unmappedPage(t, c.stream); got != c.want {
				t.Errorf("text = %q, want %q", got, c.want)
			}
		})
	}
}

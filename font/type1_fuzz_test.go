package font

import (
	"testing"

	"github.com/model-harness/pdftools/internal/t1build"
)

// FuzzParseType1 feeds arbitrary bytes to ParseType1 and, when it succeeds, to Outline for every
// gid with a small budget. Nothing here may panic, and NumGlyphs must stay within the 16-bit range
// the format's own glyph-count refusal enforces.
func FuzzParseType1(f *testing.F) {
	f.Add(t1build.Square())

	// .notdef found at a nonzero index, so ParseType1's swap path runs.
	f.Add(func() []byte {
		b := t1build.Builder{Glyphs: []t1build.Glyph{
			{Name: "A", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
			{Name: ".notdef", CS: t1build.CS(0, 333, t1build.HSBW, t1build.EndChar)},
		}}
		data, _ := b.Build()
		return data
	}())

	// No .notdef at all, so ParseType1 synthesizes one.
	f.Add(func() []byte {
		b := t1build.Builder{Glyphs: []t1build.Glyph{
			{Name: "A", CS: t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 10, 0, t1build.RLineTo, t1build.EndChar)},
		}, Encoding: map[byte]string{65: "A"}}
		data, _ := b.Build()
		return data
	}())

	// A local subroutine, hint replacement, and a seac, all reachable from Outline.
	f.Add(func() []byte {
		square := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo,
			100, t1build.HLineTo, 100, t1build.VLineTo, -100, t1build.HLineTo, t1build.ClosePath, t1build.EndChar)
		b := t1build.Builder{
			Subrs: [][]byte{t1build.CS(10, 0, t1build.RLineTo, t1build.Return)},
			Glyphs: []t1build.Glyph{
				{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
				{Name: "A", CS: square},
				{Name: "B", CS: square},
				{Name: "C", CS: t1build.CS(5, 700, t1build.HSBW, 0, 0, t1build.RMoveTo, -107,
					t1build.CallSubr, t1build.EndChar)},
				{Name: "D", CS: t1build.CS(0, 500, t1build.HSBW, 3, 10, 20, 65, 66, t1build.Seac)},
			},
		}
		data, _ := b.Build()
		return data
	}())

	// A lenIV of -1 (unencrypted charstrings) and 0 (encrypted, nothing stripped).
	for _, iv := range []int{-1, 0} {
		f.Add(func() []byte {
			iv := iv
			b := t1build.Builder{LenIV: &iv, Glyphs: []t1build.Glyph{
				{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
			}}
			data, _ := b.Build()
			return data
		}())
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		tf, err := ParseType1(data)
		if err != nil {
			return
		}
		if tf.NumGlyphs() > 65535 {
			t.Fatalf("NumGlyphs = %d, want at most 65535", tf.NumGlyphs())
		}
		for gid := 0; gid < tf.NumGlyphs(); gid++ {
			_, _, _ = tf.Outline(uint16(gid), 1<<12) // #nosec G115 -- NumGlyphs is checked above
		}
	})
}

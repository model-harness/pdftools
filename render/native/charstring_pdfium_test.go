package native

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/cffbuild"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// manyStems returns n hstemhm stem pairs, 24 to an operator so the 48-slot argument stack never
// overflows -- the same shape font/charstring_test.go's stemOps builds.
func manyStems(n int) []any {
	var ops []any
	for left := n; left > 0; left -= 24 {
		for i := 0; i < min(left, 24); i++ {
			ops = append(ops, 10, 20)
		}
		ops = append(ops, cffbuild.HStemHM)
	}
	return ops
}

// TestSeacStemsAgreeWithPdfium pins R#5's seac case: a seac's own charstring runs as a separate
// interpretation with its own hint state (r.glyph allocates a fresh charState), and it never calls
// its own moveto, so cf2_hintmask_setCounts never runs for it and FreeType draws both components
// however many stems that charstring declared. 97 stems, past maxStems, must still draw both
// components and agree with pdfium, unlike the same count in a glyph that does call moveto.
func TestSeacStemsAgreeWithPdfium(t *testing.T) {
	cs := cffbuild.CS
	const one = "BT /F1 100 Tf 20 40 Td (A) Tj ET"
	seac := func(stems int) cffbuild.Builder {
		return cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			{SID: sidA, CharString: cs(append(manyStems(stems), 200, 450, 66, 67, cffbuild.EndChar)...)},
			{SID: sidB, CharString: cffbuild.Square()},
			{SID: sidC, CharString: cs(100, 100, cffbuild.RMoveTo, 200, 100, -200, cffbuild.HLineTo,
				cffbuild.EndChar)},
		}}
	}
	for _, n := range []int{96, 97} {
		t.Run(fmt.Sprintf("%d stems in the seac's own charstring", n), func(t *testing.T) {
			native, ref := drawBoth(t, one, cffFont(seac(n))...)
			if len(native) != len(ref) {
				t.Fatalf("size: pdfium %d pixels, native %d", len(ref), len(native))
			}
			var sum, inkRef, inkNative float64
			for i := range ref {
				sum += math.Abs(float64(ref[i]) - float64(native[i]))
				inkRef += float64(ref[i])
				inkNative += float64(native[i])
			}
			mean := sum / float64(len(ref))
			if inkRef == 0 {
				t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
			}
			if r := inkNative / inkRef; r < 0.9 || r > 1.1 {
				t.Errorf("ink ratio %.3f, want within 10%% -- both seac components should draw", r)
			}
			if mean > 1.5 {
				t.Errorf("mean ink difference %.3f, want <= 1.5", mean)
			}
		})
	}
}

// TestCntrmaskStemCapAgreesWithPdfium pins R#0: the mask case's stem cap (charState.checkStems)
// runs the same for cntrmask (op 20) as for hintmask, so a seac glyph whose own charstring -- not
// either component's -- carries stems past maxStems into a cntrmask, with no moveto afterward to
// re-check the count, must refuse in agreement with pdfium. FreeType reaches cf2_hintmask_read
// (psintrp.c:132) from either mask operator, and cf2_hintmask_setCounts (:109) fails the glyph --
// and so the whole charstring, since the loop exits on that error (:704-705) -- the same way for
// both.
func TestCntrmaskStemCapAgreesWithPdfium(t *testing.T) {
	cs := cffbuild.CS
	const one = "BT /F1 100 Tf 20 40 Td (A) Tj ET"
	seac := func(top []any) cffbuild.Builder {
		return cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			{SID: sidA, CharString: cs(top...)},
			{SID: sidB, CharString: cffbuild.Square()},
			{SID: sidC, CharString: cs(100, 100, cffbuild.RMoveTo, 200, 100, -200, cffbuild.HLineTo,
				cffbuild.EndChar)},
		}}
	}
	tail := []any{200, 450, 66, 67, cffbuild.EndChar}
	cat := func(parts ...[]any) []any {
		var o []any
		for _, p := range parts {
			o = append(o, p...)
		}
		return o
	}
	for _, c := range []struct {
		name       string
		top        []any
		wantRefuse bool
	}{
		{"96 stems then cntrmask (control)",
			cat(manyStems(96), []any{cffbuild.CntrMask, cffbuild.Raw(make([]byte, 12))}, tail), false},
		{"95 stems, cntrmask whose implied vstem is the 96th (accepted control)",
			cat(manyStems(95), []any{10, 20, cffbuild.CntrMask, cffbuild.Raw(make([]byte, 12))}, tail), false},
		{"97 stems then a bare cntrmask",
			cat(manyStems(97), []any{cffbuild.CntrMask, cffbuild.Raw(make([]byte, 13))}, tail), true},
		{"96 stems, cntrmask whose implied vstem is the 97th",
			cat(manyStems(96), []any{10, 20, cffbuild.CntrMask, cffbuild.Raw(make([]byte, 13))}, tail), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := textPDF(t, one, 200, cffFont(seac(c.top))...)
			o := render.DefaultOptions
			o.DPI = 72
			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()
			got, nerr := New(s).Page(1, o)
			p, err := pdfium.Open(path)
			if err != nil {
				t.Fatalf("pdfium: %v", err)
			}
			defer func() { _ = p.Close() }()
			want, err := p.Page(1, o)
			if err != nil {
				t.Fatalf("pdfium Page: %v", err)
			}
			refPx, _, _ := ink(want.Image)
			var refInk float64
			for _, v := range refPx {
				refInk += float64(v)
			}
			if c.wantRefuse {
				if refInk != 0 {
					t.Fatalf("pdfium drew ink %v, want 0: the fixture no longer exercises the cap", refInk)
				}
				if nerr == nil {
					t.Errorf("Page err = nil, want a refusal past the 96-stem limit, agreeing with pdfium's empty page")
				} else if !strings.Contains(nerr.Error(), "past the limit of 96") {
					t.Errorf("Page err = %v, want a refusal mentioning the 96-stem limit", nerr)
				}
				return
			}
			if refInk == 0 {
				t.Fatal("pdfium drew nothing, so this comparison asserts nothing")
			}
			if nerr != nil {
				t.Fatalf("Page: %v", nerr)
			}
			px, _, _ := ink(got.Image)
			var gotInk float64
			for _, v := range px {
				gotInk += float64(v)
			}
			if r := gotInk / refInk; r < 0.9 || r > 1.1 {
				t.Errorf("ink ratio %.3f, want within 10%%", r)
			}
		})
	}
}

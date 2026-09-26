package native

import (
	"errors"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/cffbuild"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/pdfium"
)

// TestROSRegistry65535DrawsTheGlyphItsCIDNames pins C2 against pdfium: a CIDFontType0C program
// whose Top DICT ROS names registry 65535 is name-keyed (font.rosCID: FreeType tests
// cid_registry != 0xFFFF, not merely ROS's presence), so CID 1 selects glyph 1 directly rather
// than through the inverted charset — matching pdfium, which passes the CID straight to
// FT_Load_Glyph for a CIDFontType0 program (cpdf_cidfont.cpp:785-787) and never reads
// cid_registry at all. Glyph 1 here is the square, at charset CID 5; a program that read this
// font as CID-keyed would draw the diamond (charset CID 1) instead.
func TestROSRegistry65535DrawsTheGlyphItsCIDNames(t *testing.T) {
	b := cffbuild.Builder{CID: true, Glyphs: []cffbuild.Glyph{
		{CID: 5, CharString: cffbuild.Square()},
		{CID: 1, CharString: cffbuild.Diamond()},
	}}
	var extra []byte
	extra = append(extra, cffbuild.DictInt(65535)...) // registry
	extra = append(extra, cffbuild.DictInt(0)...)     // ordering
	extra = append(extra, cffbuild.DictInt(0)...)     // supplement
	b.TopExtra = append(extra, 12, 30)                // ROS: escape 12, byte 30 (1230)

	got, gotRef := drawBoth(t, `BT /F1 48 Tf 20 100 Td <0001> Tj ET`, cffFont(b)...)
	want, wantRef := drawBoth(t, `BT /F1 48 Tf 20 100 Td (A) Tj ET`, cffFont(cffLetters())...)
	assertSameInk(t, got, gotRef, want, wantRef)
}

// TestC19EmptyCustomNameRefused pins C19's own end-to-end scenario: a nonsymbolic font with no
// /Encoding, whose built-in encoding sends code 0x80 to a glyph the charset names with an empty
// custom string. font.checkCharsetNames (C8) refuses that at parse, before text.go's no-base
// default branch — the GIDForName("") and GIDForCode comparison ADR 0022 documents — can ever
// run. Without that refusal, the review's own scratch harness showed the two engines disagree
// silently: pdfium's nonsymbolic loop gets a null name for the unmapped code and draws nothing,
// while this backend's GIDForName("") matched the empty-named glyph and drew it.
func TestC19EmptyCustomNameRefused(t *testing.T) {
	b := cffbuild.Builder{
		Codes:  []byte{0x80},
		Glyphs: []cffbuild.Glyph{{EmptyName: true, CharString: cffbuild.Square()}},
	}
	noBase := []func(*textPDFOpts){withEncoding("")}
	err := renderErr(t, textPDF(t, `BT /F1 24 Tf 20 100 Td (\200) Tj ET`, 200, cffFont(b, noBase...)...))
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want an *Unsupported naming the refusal", err)
	}
	const want = "the FontFile3 program did not parse"
	if joined := strings.Join(u.Ops, " | "); !strings.Contains(joined, want) {
		t.Errorf("refusal = %q, want it to mention %q", joined, want)
	}
}

// index1 builds a minimal offSize-1 INDEX (TN #5176 §5) from small objects. font.CFF's own index()
// type is unexported, so this rebuilds just enough of it to splice a real font's own prefix.
func index1(objs ...[]byte) []byte {
	b := []byte{byte(len(objs) >> 8), byte(len(objs))} // #nosec G115 -- test fixtures, small counts
	if len(objs) == 0 {
		return b
	}
	b = append(b, 1)
	off := 1
	for i := 0; i <= len(objs); i++ {
		b = append(b, byte(off)) // #nosec G115 -- test fixtures, small offsets
		if i < len(objs) {
			off += len(objs[i])
		}
	}
	for _, o := range objs {
		b = append(b, o...)
	}
	return b
}

// splitIndex1 reads a count/offSize-1 INDEX (the form cffbuild.Builder always writes for a
// one-name, one-Top-DICT font) at p, returning its first object and the position right after it.
func splitIndex1(data []byte, p int) (obj0 []byte, end int) {
	count := int(data[p])<<8 | int(data[p+1])
	offSize := int(data[p+2])
	offs := p + 3
	base := offs + (count+1)*offSize - 1
	readOff := func(i int) int {
		v := 0
		for k := 0; k < offSize; k++ {
			v = v<<8 | int(data[offs+i*offSize+k])
		}
		return v
	}
	obj0 = data[base+readOff(0) : base+readOff(1)]
	return obj0, base + readOff(count)
}

// TestNameIndexDataSizeGuardRefusedEndToEnd pins R#0 (review finding #0) end to end: cffLetters,
// rewritten so its Name INDEX holds "A" and an empty second name (data size 1, below its count of
// 2), is refused rather than drawn — font.TestNameIndexDataSizeGuard pins the guard itself; this
// pins that render/native's own font-loading path (parseFont, parseCFF) surfaces its refusal the
// same way C19's does, rather than drawing the embedded glyphs a font this malformed would give
// pdfium a substitute face for instead (the review's own scratch evidence: 467 pixels differ).
func TestNameIndexDataSizeGuardRefusedEndToEnd(t *testing.T) {
	data := cffLetters().Build()
	_, namesEnd := splitIndex1(data, int(data[2]))
	topBytes, topsEnd := splitIndex1(data, namesEnd)

	newNames := index1([]byte("A"), []byte{}) // count 2, offsets [1,2,2]: data size 1 < count 2
	newTops := index1(topBytes, nil)          // count 2: the real Top DICT, and an empty one
	hdrSize := topsEnd - len(newNames) - len(newTops)
	if hdrSize < 4 {
		t.Fatalf("computed hdrSize %d, want at least 4", hdrSize)
	}
	header := make([]byte, hdrSize)
	header[0], header[2], header[3] = 1, byte(hdrSize), 4 // #nosec G115 -- bounded by the fixture
	prog := append(header, newNames...)
	prog = append(prog, newTops...)
	prog = append(prog, data[topsEnd:]...)

	path := textPDF(t, `BT /F1 48 Tf 20 100 Td (A) Tj ET`, 200, cffFont(cffLetters(), withProgram(prog))...)
	err := renderErr(t, path)
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want an *Unsupported naming the refusal", err)
	}
	const want = "the FontFile3 program did not parse"
	if joined := strings.Join(u.Ops, " | "); !strings.Contains(joined, want) {
		t.Errorf("refusal = %q, want it to mention %q", joined, want)
	}
}

// TestFontBBoxDuplicatedOccurrenceRefusedEndToEnd pins R#0 (round-2 review finding #0) end to end:
// cffLetters, with a Top DICT FontBBox occurring twice — [0] (1 operand, refused on its own by
// TestFontBBoxNeedsFourOperands) and then [0 0 0 0] (4 operands, valid on its own) — is refused
// rather than drawn, the same way TestNameIndexDataSizeGuardRefusedEndToEnd pins R#0's other
// finding. font.TestDuplicatedOperatorOccurrenceIsRefused pins parseDict itself; this pins that
// render/native's own font-loading path surfaces the refusal too, rather than drawing the second
// occurrence's own glyph.
//
// Proved against pdfium: the same bytes make pdfium draw a substitute face rather than cffLetters'
// own square for "A", because cff_parser_run fails the whole Top DICT the moment the first
// FontBBox occurrence's reader underflows (cffparse.c, cff_parse_font_bbox), before it ever reaches
// the second, valid occurrence.
func TestFontBBoxDuplicatedOccurrenceRefusedEndToEnd(t *testing.T) {
	var extra []byte
	extra = append(extra, cffbuild.DictInt(0)...)
	extra = append(extra, 5) // FontBBox (op 5): [0], 1 operand
	for i := 0; i < 4; i++ {
		extra = append(extra, cffbuild.DictInt(0)...)
	}
	extra = append(extra, 5) // FontBBox again: [0 0 0 0], 4 operands

	bad := cffLetters()
	bad.TopExtra = extra
	const stream = `BT /F1 48 Tf 20 100 Td (A) Tj ET`

	t.Run("pdfium substitutes a face rather than draw the font's own glyph", func(t *testing.T) {
		o := render.DefaultOptions
		o.DPI = 72

		badPath := textPDF(t, stream, 200, cffFont(bad)...)
		p, err := pdfium.Open(badPath)
		if err != nil {
			t.Fatalf("pdfium.Open: %v", err)
		}
		defer func() { _ = p.Close() }()
		got, err := p.Page(1, o)
		if err != nil {
			t.Fatalf("pdfium Page: %v", err)
		}
		gotInk, _, _ := ink(got.Image)

		plainPath := textPDF(t, stream, 200, cffFont(cffLetters())...)
		wp, err := pdfium.Open(plainPath)
		if err != nil {
			t.Fatalf("pdfium.Open: %v", err)
		}
		defer func() { _ = wp.Close() }()
		want, err := wp.Page(1, o)
		if err != nil {
			t.Fatalf("pdfium Page: %v", err)
		}
		wantInk, _, _ := ink(want.Image)

		var diff int
		for i := range gotInk {
			if gotInk[i] != wantInk[i] {
				diff++
			}
		}
		if diff == 0 {
			t.Fatal("pdfium drew the duplicated-FontBBox font identically to the plain one, " +
				"want a substitution (a nonzero pixel difference)")
		}
	})

	t.Run("this backend refuses rather than draw the first occurrence's glyph", func(t *testing.T) {
		err := renderErr(t, textPDF(t, stream, 200, cffFont(bad)...))
		var u *Unsupported
		if !errors.As(err, &u) {
			t.Fatalf("err = %v, want an *Unsupported naming the refusal", err)
		}
		const want = "the FontFile3 program did not parse"
		if joined := strings.Join(u.Ops, " | "); !strings.Contains(joined, want) {
			t.Errorf("refusal = %q, want it to mention %q", joined, want)
		}
	})
}

package extract

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
)

// memStore is a Store over hand-written content streams.
//
// A synthetic store rather than a fixture file, because the questions this
// package has to answer are about specific operator sequences — a TJ with a wide
// negative adjustment, a Td that moves down by less than a line — and a real PDF
// cannot be edited to isolate one of them. The corpus tests further down cover
// what synthetic streams cannot: that the same logic survives real producers.
type memStore struct {
	objs    map[objects.Ref]objects.Object
	pages   []objects.Dict
	content [][]byte
}

func (m *memStore) Resolve(o objects.Object) (objects.Object, error) {
	for i := 0; i < 8; i++ {
		ref, isRef := o.(objects.Ref)
		if !isRef {
			return o, nil
		}
		v, ok := m.objs[ref]
		if !ok {
			return objects.Null{}, nil
		}
		o = v
	}
	return objects.Null{}, nil
}

func (m *memStore) Trailer() (objects.Dict, error) { return objects.Dict{}, nil }
func (m *memStore) Catalog() (objects.Dict, error) { return objects.Dict{}, nil }
func (m *memStore) PageCount() int                 { return len(m.pages) }

func (m *memStore) Page(n int) (objects.Dict, error) {
	if n < 1 || n > len(m.pages) {
		return nil, objects.ErrNotFound
	}
	return m.pages[n-1], nil
}

func (m *memStore) PageContent(n int) ([]byte, error) {
	if n < 1 || n > len(m.content) {
		return nil, objects.ErrNotFound
	}
	return m.content[n-1], nil
}

// Decode treats Raw as already decoded, which is what an unfiltered stream is.
func (m *memStore) Decode(s *objects.Stream) error {
	s.Decoded = s.Raw
	return nil
}

func (m *memStore) Version() string { return "2.0" }
func (m *memStore) Encrypted() bool { return false }
func (m *memStore) Close() error    { return nil }

// helvetica is a standard-14 font dictionary: no /Widths, no /FontDescriptor, so
// every advance comes from the built-in metrics. That is deliberate — it makes the
// space threshold depend on font/metrics.go rather than on numbers written into
// this test, so a regression there surfaces here.
func helvetica() objects.Dict {
	return objects.Dict{
		"Type":     objects.Name("Font"),
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Helvetica"),
	}
}

func courier() objects.Dict {
	return objects.Dict{
		"Type":     objects.Name("Font"),
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Courier"),
	}
}

// onePage builds a single-page store whose content is stream, with F1 Helvetica
// and F2 Courier in its resources.
func onePage(stream string) *memStore {
	fontRef := objects.Ref{Num: 10}
	monoRef := objects.Ref{Num: 11}
	page := objects.Dict{
		"Type":     objects.Name("Page"),
		"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
		"Resources": objects.Dict{
			"Font": objects.Dict{
				"F1": fontRef,
				"F2": monoRef,
			},
		},
	}
	return &memStore{
		objs: map[objects.Ref]objects.Object{
			fontRef: helvetica(),
			monoRef: courier(),
		},
		pages:   []objects.Dict{page},
		content: [][]byte{[]byte(stream)},
	}
}

func extractPage(t *testing.T, stream string) doc.Page {
	t.Helper()
	s := onePage(stream)
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	return p
}

func extractText(t *testing.T, stream string) string {
	t.Helper()
	return extractPage(t, stream).Text()
}

// TestSpaceFromGap is the defect the whole package exists to fix. A producer
// emits two words as two show operations with no space glyph anywhere, and the
// only evidence of the word boundary is that the second starts further right than
// the first one's advance accounts for.
func TestSpaceFromGap(t *testing.T) {
	// "Hello" at 12pt in Helvetica advances 28.008pt; the next Td puts "world" at
	// x=100, so the unexplained gap is about 62pt — far more than the 0.3 of a
	// space advance the threshold asks for.
	got := extractText(t, `BT /F1 12 Tf 10 700 Td (Hello) Tj 90 0 Td (world) Tj ET`)
	if got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
}

// TestNoSpaceWithinWord is the opposite failure and the reason the threshold is a
// fraction of a space advance rather than any positive gap: consecutive glyphs of
// one word are separated by their own widths, which the pen already accounts for,
// so nothing may be inferred between them.
func TestNoSpaceWithinWord(t *testing.T) {
	// Each glyph shown separately at its correct position. Helvetica at 12pt: H
	// 8.664, e 6.672, l 2.664, l 2.664.
	stream := `BT /F1 12 Tf 10 700 Td (H) Tj 8.664 0 Td (e) Tj 6.672 0 Td (l) Tj 2.664 0 Td (l) Tj 2.664 0 Td (o) Tj ET`
	got := extractText(t, stream)
	if got != "Hello" {
		t.Errorf("got %q, want %q", got, "Hello")
	}
}

// TestSpaceFromTJAdjustment covers the most common way a producer writes a space
// without a space glyph: one TJ array with a wide negative number between the
// words. The adjustment must reach the gap test as unexplained displacement, which
// is why showArray does not add it to the pen.
func TestSpaceFromTJAdjustment(t *testing.T) {
	// -1000 thousandths at 12pt is 12pt of displacement, well over the threshold.
	got := extractText(t, `BT /F1 12 Tf 10 700 Td [(Hello) -1000 (world)] TJ ET`)
	if got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
}

// TestNarrowKernIsNotASpace pins the other side of that decision. Ordinary
// kerning is a small negative adjustment between glyphs of one word, and treating
// it as a space is how a reader produces "H e l l o".
func TestNarrowKernIsNotASpace(t *testing.T) {
	// -20 thousandths at 12pt is 0.24pt. The threshold is 0.3 of a 12pt space
	// advance (3.336pt), about 1pt.
	got := extractText(t, `BT /F1 12 Tf 10 700 Td [(Hel) -20 (lo)] TJ ET`)
	if got != "Hello" {
		t.Errorf("got %q, want %q", got, "Hello")
	}
}

// TestExplicitSpaceGlyphNotDoubled: when the producer does emit a space glyph,
// its advance is accounted for by the pen, so no second space may be inferred on
// top of it.
func TestExplicitSpaceGlyphNotDoubled(t *testing.T) {
	got := extractText(t, `BT /F1 12 Tf 10 700 Td (Hello world) Tj ET`)
	if got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
	if strings.Contains(got, "  ") {
		t.Error("double space: an explicit space glyph was counted twice")
	}
}

// TestWordSpaceAppliesToCode32 checks that /Tw is accounted for in the pen. A
// producer setting Tw to widen spaces has explained that displacement, so no extra
// space may be inferred from it (§9.3.3).
func TestWordSpaceAppliesToCode32(t *testing.T) {
	got := extractText(t, `BT /F1 12 Tf 20 Tw 10 700 Td (a b) Tj ET`)
	if got != "a b" {
		t.Errorf("got %q, want %q", got, "a b")
	}
}

// TestCharSpaceAccountedFor is the same rule for /Tc, which is the setting most
// likely to produce one-space-per-glyph if the pen ignores it.
func TestCharSpaceAccountedFor(t *testing.T) {
	got := extractText(t, `BT /F1 12 Tf 3 Tc 10 700 Td (Hello) Tj ET`)
	if got != "Hello" {
		t.Errorf("got %q, want %q", got, "Hello")
	}
}

// TestLineBreakJoinsWithSpace: a paragraph's second line continues the first, and
// the join is a word boundary even though nothing in the stream says so.
func TestLineBreakJoinsWithSpace(t *testing.T) {
	stream := `BT /F1 12 Tf 10 700 Td (first) Tj 0 -14 Td (second) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1: a 14pt step at 12pt type is a line, not a paragraph", len(p.Blocks))
	}
	if got := p.Text(); got != "first second" {
		t.Errorf("got %q, want %q", got, "first second")
	}
}

// TestWrapNeedsSpace: the line-break space above is a Latin rule, and applying it to a
// script written without inter-word spaces splits words that were never divided.
//
// A CJK line has no word boundaries to break at — it fills and wraps wherever it runs out
// of measure — so the break carries no information and a space at the join is a claim the
// page does not make. Measured on chinese-tables.pdf: the company name
// 中诚信国际信用评级有限责任公司 wraps across three lines and was emitted with two spaces in
// it.
//
// Tested here as well as against that fixture because the fixture cannot express the
// mixed-script cases, and those are where a rule like this goes wrong: a document that
// sets its numbers in Latin and its prose in Chinese wraps from one into the other, and
// that join is a real boundary.
func TestWrapNeedsSpace(t *testing.T) {
	for _, tc := range []struct {
		name, prev, next string
		want             bool
	}{
		{"latin", "first", "second", true},
		{"han", "中诚信国际信", "用评级有限责", false},
		{"han after cjk comma", "很低，", "中诚信", false},
		{"kana", "こんにち", "は世界", false},
		{"halfwidth kana", "ｱｲｳ", "ｴｵ", false},
		{"fullwidth", "２０２３", "年度", false},
		// Hangul is CJK by every classification except the one that matters here: modern
		// Korean is written with spaces between words, so a Korean line wrap is an ordinary
		// word boundary and suppressing the space would run two words together.
		{"hangul", "안녕하세요", "세계", true},
		// Mixed: the break really is a boundary, because one side is a word in a script
		// that has them. Dropping the space here would run "AA+" into the next word.
		{"latin into han", "AA+", "偿还债务", true},
		{"han into latin", "评级", "AA+", true},
		{"digit into han", "2023", "年份", true},
		// Neither side has a character to inspect. A space is the safe answer: it is what
		// the Latin path did before, and every empty fragment is skipped upstream anyway.
		{"empty prev", "", "second", true},
		{"empty next", "first", "", true},
		// A broken /ToUnicode can produce either of these. U+FFFD is not spaceless, so the
		// join gets a space — the answer this function gave everywhere before it existed,
		// which is how a decode failure should fail.
		{"invalid utf8 prev", "\xff\xfe", "中诚信", true},
		{"invalid utf8 next", "中诚信", "\xff\xfe", true},
	} {
		if got := wrapNeedsSpace(tc.prev, tc.next); got != tc.want {
			t.Errorf("%s: wrapNeedsSpace(%q, %q) = %v, want %v", tc.name, tc.prev, tc.next, got, tc.want)
		}
	}
}

// TestParagraphBreakSplitsBlocks: a step much larger than the line height is a
// paragraph break, and the two blocks must not be joined by a space as though they
// were one paragraph.
func TestParagraphBreakSplitsBlocks(t *testing.T) {
	// ParaFrac is 1.5, so at 12pt type a step beyond 18pt starts a new block.
	stream := `BT /F1 12 Tf 10 700 Td (first) Tj 0 -40 Td (second) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(p.Blocks))
	}
	if got := p.Blocks[0].Text(); got != "first" {
		t.Errorf("block 0 = %q", got)
	}
	if got := p.Blocks[1].Text(); got != "second" {
		t.Errorf("block 1 = %q", got)
	}
}

// TestSizeChangeSplitsBlocks is the case the vertical step cannot see.
//
// A heading set at the same leading as the prose under it steps down by exactly one
// line, so the step test joins them and the heading is resolved as the first words of
// the following paragraph. adobe-samples/autotagPDFInput.pdf and
// pymupdf/v110-changes.pdf are both written this way, and before this neither yielded
// a single promotable heading — layout had no separate block to promote.
//
// 18pt against 12pt is a ratio of 1.5, well past SizeFrac, and the 14pt step is under
// ParaFrac*18 = 27 so nothing else would split them.
func TestSizeChangeSplitsBlocks(t *testing.T) {
	stream := `BT /F1 18 Tf 10 700 Td (Heading) Tj /F1 12 Tf 0 -14 Td (Body text.) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: the heading fused into the paragraph", len(p.Blocks))
	}
	if got := p.Blocks[0].Text(); got != "Heading" {
		t.Errorf("block 0 = %q, want %q", got, "Heading")
	}
	if got := p.Blocks[1].Text(); got != "Body text." {
		t.Errorf("block 1 = %q, want %q", got, "Body text.")
	}
}

// TestSmallSizeJitterDoesNotSplit is what stops the size test from splitting prose.
//
// OCR output reports one line of type at sizes differing by a few percent, and an ISO
// cover sets an 11.5pt address line against a 12pt URL. Measured over the corpus,
// that population tops out at a ratio of 1.057 while real structure starts at 1.067,
// so SizeFrac sits at 1.06 between them. 12.5 against 12 is 1.042 — jitter.
func TestSmallSizeJitterDoesNotSplit(t *testing.T) {
	stream := `BT /F1 12.5 Tf 10 700 Td (first line) Tj /F1 12 Tf 0 -14 Td (second line) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1: a 4%% size difference is jitter, not structure", len(p.Blocks))
	}
}

// TestSizeBreakUsesDominantSize: a footnote marker is a legitimately different size
// inside one line of prose, and taking the largest size per line would make every
// annotated line look like a heading meeting body text.
//
// The marker is set at 20pt — a ratio of 1.67 against the body, well past SizeFrac —
// so a largest-size-per-line rule breaks the paragraph at it. It is one character
// against thirty-two of 12pt type, so it loses the tally and the block holds together.
// The 20pt size is deliberately implausible for a real superscript: a marker at a
// plausible 7pt would leave 12 as the largest size on its line too, and the test would
// pass under either rule without distinguishing them.
func TestSizeBreakUsesDominantSize(t *testing.T) {
	stream := `BT /F1 12 Tf 10 700 Td (A claim needing a citation) Tj ` +
		`/F1 20 Tf 0 -14 Td (1) Tj /F1 12 Tf 8 0 Td (and the sentence continues here.) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1: the oversized marker split the paragraph", len(p.Blocks))
	}
}

// TestSizeFracOffJoins: a caller that fills some Tolerance fields and not others gets
// SizeFrac 0, which cannot be applied literally — a ratio at or below 1 splits on any
// difference at all, and no document survives that. It reads as "off".
func TestSizeFracOffJoins(t *testing.T) {
	tol := geom.DefaultTolerance
	tol.SizeFrac = 0
	s := onePage(`BT /F1 18 Tf 10 700 Td (Heading) Tj /F1 12 Tf 0 -14 Td (Body text.) Tj ET`)
	p, err := New(s, Options{Tol: tol, KeepHidden: true}).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(p.Blocks) != 1 {
		t.Errorf("blocks = %d, want 1: SizeFrac 0 must disable the test, not split everything", len(p.Blocks))
	}
}

// TestDomSizeCountsRunesNotBytes: "the size most of the line's characters are set in"
// has to mean the same thing here as in layout.bodyCluster, which counts runes.
//
// Weighting by byte length counts a CJK or mathematical character three or four times
// over a Latin one. The line below is 4 CJK characters at 12pt against 9 ASCII ones at
// 20pt: 4 runes against 9, so 20pt is dominant, but 12 bytes against 9, so a byte tally
// picks 12pt and every consumer's idea of which line is the larger inverts.
func TestDomSizeCountsRunesNotBytes(t *testing.T) {
	ln := &line{frags: []frag{
		{text: []byte("日本語文"), style: doc.Style{Size: 12}},
		{text: []byte("ABCDEFGHI"), style: doc.Style{Size: 20}},
	}}
	if got := domSize(ln); got != 20 {
		t.Errorf("domSize = %v, want 20: 9 ASCII runes outnumber 4 CJK ones, but 12 CJK bytes outnumber 9 ASCII", got)
	}
}

// TestDomSizeBreaksTieTowardSmaller pins the tie-break, which is otherwise at the mercy
// of Go's randomized map iteration: a line split evenly between two sizes would report
// one size on some runs and the other on the rest, and a block boundary that moves
// between runs is worse than one in the wrong place.
//
// Toward the smaller size, matching layout.bodyCluster, so an evenly split line is
// treated as body meeting emphasis rather than the reverse.
func TestDomSizeBreaksTieTowardSmaller(t *testing.T) {
	for i := 0; i < 64; i++ {
		ln := &line{frags: []frag{
			{text: []byte("abc"), style: doc.Style{Size: 18}},
			{text: []byte("xyz"), style: doc.Style{Size: 12}},
			{text: []byte("pqr"), style: doc.Style{Size: 24}},
		}}
		if got := domSize(ln); got != 12 {
			t.Fatalf("domSize = %v, want 12: a three-way tie must resolve to the smallest size, not to map order", got)
		}
	}
}

// TestSizeBreakDefersToMarkedContent: where the producer declared two lines to be one
// element, the size test must not split them.
//
// ISO/TS 32003:2023's cover is the case — a 36pt document number over a 17.5pt title,
// both inside /MCID 3. Splitting it drops the space that joined them, because the wrap
// space is written onto the second line's leading text and is trimmed when that line
// starts a block; sectionize then rejoins the two spans on their shared MCID with no
// separator and the title reads "32003:2023Document management".
//
// The sizes here are 36 and 17.5 — a ratio of 2.06, so nothing about the threshold is
// what holds the block together — and the 20pt step is under ParaFrac*36.
func TestSizeBreakDefersToMarkedContent(t *testing.T) {
	stream := `/H1 <</MCID 3>> BDC BT /F1 36 Tf 10 700 Td (ISO/TS 32003:2023) Tj ` +
		`/F1 17.5 Tf 0 -20 Td (Document management) Tj ET EMC`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1: the size test split one marked-content element", len(p.Blocks))
	}
	if got, want := p.Blocks[0].Text(), "ISO/TS 32003:2023 Document management"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

// TestSizeBreakSplitsAcrossMarkedContent is the other half: two elements is not a
// statement that the lines belong together, so the size test still applies. Without
// this, deferring to marked content would disable the rule on every tagged document.
func TestSizeBreakSplitsAcrossMarkedContent(t *testing.T) {
	stream := `/H1 <</MCID 0>> BDC BT /F1 18 Tf 10 700 Td (Heading) Tj ET EMC ` +
		`/P <</MCID 1>> BDC BT /F1 12 Tf 10 686 Td (Body text.) Tj ET EMC`
	p := extractPage(t, stream)
	if len(p.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: the heading fused into the paragraph", len(p.Blocks))
	}
}

// TestStyleChangeSplitsSpans: an italic term inside a paragraph is what Spans
// exist for. Two fonts, one line, one block, two spans.
func TestStyleChangeSplitsSpans(t *testing.T) {
	stream := `BT /F1 12 Tf 10 700 Td (code:) Tj /F2 12 Tf 40 0 Td (func) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(p.Blocks))
	}
	b := p.Blocks[0]
	if len(b.Spans) != 2 {
		t.Fatalf("spans = %d, want 2: a font change must not be merged", len(b.Spans))
	}
	if b.Spans[0].Style.Mono {
		t.Error("Helvetica reported as monospaced")
	}
	if !b.Spans[1].Style.Mono {
		t.Error("Courier not reported as monospaced: the code-block signal is lost")
	}
	if got := b.Text(); got != "code: func" {
		t.Errorf("got %q, want %q", got, "code: func")
	}
}

// TestEffectiveSizeFromMatrix: a Tf of 1 with a matrix scaling by 12 is how many
// producers set 12-point type. Reporting the operand instead of the composed size
// makes every size-based heading heuristic useless.
func TestEffectiveSizeFromMatrix(t *testing.T) {
	p := extractPage(t, `BT /F1 1 Tf 12 0 0 12 10 700 Tm (Big) Tj ET`)
	if len(p.Blocks) == 0 || len(p.Blocks[0].Spans) == 0 {
		t.Fatal("no spans")
	}
	if got := p.Blocks[0].Spans[0].Style.Size; got < 11.9 || got > 12.1 {
		t.Errorf("size = %v, want 12", got)
	}
}

// TestCTMScalesSize: the same rule through the CTM rather than the text matrix,
// which is where a scaled form or an imposed page puts it.
func TestCTMScalesSize(t *testing.T) {
	p := extractPage(t, `2 0 0 2 0 0 cm BT /F1 6 Tf 10 350 Td (Big) Tj ET`)
	if len(p.Blocks) == 0 || len(p.Blocks[0].Spans) == 0 {
		t.Fatal("no spans")
	}
	if got := p.Blocks[0].Spans[0].Style.Size; got < 11.9 || got > 12.1 {
		t.Errorf("size = %v, want 12", got)
	}
}

// TestArtifactDropped: a running header is the same text on every page, and
// keeping it interleaves it with prose at every page boundary.
func TestArtifactDropped(t *testing.T) {
	stream := `/Artifact <</Type /Pagination>> BDC BT /F1 9 Tf 10 760 Td (Page header) Tj ET EMC
BT /F1 12 Tf 10 700 Td (Body text) Tj ET`
	p := extractPage(t, stream)
	if got := p.Text(); got != "Body text" {
		t.Errorf("got %q, want %q", got, "Body text")
	}
}

// TestArtifactKept: the same page with KeepArtifacts, which is what probe uses to
// count what was dropped. The judgement stays inspectable rather than being made
// silently at read time.
func TestArtifactKept(t *testing.T) {
	stream := `/Artifact <</Type /Pagination>> BDC BT /F1 9 Tf 10 760 Td (Page header) Tj ET EMC
BT /F1 12 Tf 10 700 Td (Body text) Tj ET`
	s := onePage(stream)
	opt := DefaultOptions
	opt.KeepArtifacts = true
	p, err := New(s, opt).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	roles := map[doc.Role]int{}
	for _, b := range p.Blocks {
		roles[b.Role]++
	}
	if roles[doc.RoleArtifact] != 1 {
		t.Errorf("artifact blocks = %d, want 1", roles[doc.RoleArtifact])
	}
	if roles[doc.RoleParagraph] != 1 {
		t.Errorf("paragraph blocks = %d, want 1", roles[doc.RoleParagraph])
	}
}

// TestHiddenTextKept: invisible text is the layer under a scanned page, and it is
// exactly what an extractor wants. Filtering it by default would make every OCR'd
// PDF in the corpus extract as blank.
func TestHiddenTextKept(t *testing.T) {
	got := extractText(t, `BT /F1 12 Tf 3 Tr 10 700 Td (Hidden) Tj ET`)
	if got != "Hidden" {
		t.Errorf("got %q, want %q", got, "Hidden")
	}
	p := extractPage(t, `BT /F1 12 Tf 3 Tr 10 700 Td (Hidden) Tj ET`)
	if !p.Blocks[0].Spans[0].Style.Hidden {
		t.Error("hidden text not marked Hidden: a sink cannot report inferred text as inferred")
	}
}

// TestHiddenTextExcludedStillAdvances: with KeepHidden off, the skipped text must
// still move the text position, or everything after it lands in the wrong place —
// and the words either side of it must not be run together.
func TestHiddenTextExcludedStillAdvances(t *testing.T) {
	stream := `BT /F1 12 Tf 10 700 Td (visible) Tj 3 Tr (SKIPPED) Tj 0 Tr (tail) Tj ET`
	s := onePage(stream)
	opt := DefaultOptions
	opt.KeepHidden = false
	p, err := New(s, opt).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	got := p.Text()
	if strings.Contains(got, "SKIPPED") {
		t.Errorf("hidden text kept: %q", got)
	}
	if got != "visible tail" {
		t.Errorf("got %q, want %q", got, "visible tail")
	}
}

// TestMCIDRecorded: the marked-content identifier is the join key between page
// text and the structure tree, so losing it costs the whole tagged path.
func TestMCIDRecorded(t *testing.T) {
	stream := `/P <</MCID 7>> BDC BT /F1 12 Tf 10 700 Td (Clause) Tj ET EMC`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(p.Blocks))
	}
	if got := p.Blocks[0].MCIDs; len(got) != 1 || got[0] != 7 {
		t.Errorf("MCIDs = %v, want [7]", got)
	}
}

// TestMCIDFromProperties: a BDC whose property list is a name refers to the page's
// /Properties resource, which the content machine cannot resolve on its own. Left
// unresolved the region has no MCID and its text is unattributable.
func TestMCIDFromProperties(t *testing.T) {
	fontRef := objects.Ref{Num: 10}
	page := objects.Dict{
		"Type":     objects.Name("Page"),
		"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
		"Resources": objects.Dict{
			"Font":       objects.Dict{"F1": fontRef},
			"Properties": objects.Dict{"MC0": objects.Dict{"MCID": objects.Int(42)}},
		},
	}
	s := &memStore{
		objs:    map[objects.Ref]objects.Object{fontRef: helvetica()},
		pages:   []objects.Dict{page},
		content: [][]byte{[]byte(`/P /MC0 BDC BT /F1 12 Tf 10 700 Td (Clause) Tj ET EMC`)},
	}
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(p.Blocks))
	}
	if got := p.Blocks[0].MCIDs; len(got) != 1 || got[0] != 42 {
		t.Errorf("MCIDs = %v, want [42]", got)
	}
}

// TestFormXObjectText: text inside a form names fonts from the form's own
// /Resources, and a reader that stops at the page dictionary returns nothing for
// every form. The font package's corpus survey found 36 fonts reachable only this
// way.
func TestFormXObjectText(t *testing.T) {
	fontRef := objects.Ref{Num: 10}
	innerFont := objects.Ref{Num: 12}
	formRef := objects.Ref{Num: 20}

	form := &objects.Stream{
		Dict: objects.Dict{
			"Type":    objects.Name("XObject"),
			"Subtype": objects.Name("Form"),
			"Resources": objects.Dict{
				"Font": objects.Dict{"FA": innerFont},
			},
		},
		Raw: []byte(`BT /FA 12 Tf 0 0 Td (inside) Tj ET`),
	}
	page := objects.Dict{
		"Type":     objects.Name("Page"),
		"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
		"Resources": objects.Dict{
			"Font":    objects.Dict{"F1": fontRef},
			"XObject": objects.Dict{"X0": formRef},
		},
	}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			fontRef:   helvetica(),
			innerFont: courier(),
			formRef:   form,
		},
		pages: []objects.Dict{page},
		content: [][]byte{[]byte(
			`BT /F1 12 Tf 10 700 Td (outside) Tj ET q 1 0 0 1 10 600 cm /X0 Do Q`)},
	}
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	got := p.Text()
	if !strings.Contains(got, "inside") {
		t.Errorf("form text missing: %q", got)
	}
	if !strings.Contains(got, "outside") {
		t.Errorf("page text missing: %q", got)
	}
}

// TestFormInheritsResources: a form with no /Resources uses the invoking stream's
// (§8.10.1). Giving up instead loses the text of every form that relies on it.
func TestFormInheritsResources(t *testing.T) {
	fontRef := objects.Ref{Num: 10}
	formRef := objects.Ref{Num: 20}
	form := &objects.Stream{
		Dict: objects.Dict{
			"Type":    objects.Name("XObject"),
			"Subtype": objects.Name("Form"),
		},
		Raw: []byte(`BT /F1 12 Tf 0 0 Td (inherited) Tj ET`),
	}
	page := objects.Dict{
		"Type":     objects.Name("Page"),
		"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
		"Resources": objects.Dict{
			"Font":    objects.Dict{"F1": fontRef},
			"XObject": objects.Dict{"X0": formRef},
		},
	}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			fontRef: helvetica(),
			formRef: form,
		},
		pages:   []objects.Dict{page},
		content: [][]byte{[]byte(`q 1 0 0 1 10 600 cm /X0 Do Q`)},
	}
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if got := p.Text(); got != "inherited" {
		t.Errorf("got %q, want %q", got, "inherited")
	}
}

// TestFormMatrixPositions: a form's /Matrix maps form space to the invoking
// stream's space, so it composes before the current CTM. Composing it the other
// way puts the text somewhere else on the page, which shows up as a wrong block
// box rather than as missing text.
func TestFormMatrixPositions(t *testing.T) {
	fontRef := objects.Ref{Num: 10}
	formRef := objects.Ref{Num: 20}
	form := &objects.Stream{
		Dict: objects.Dict{
			"Type":    objects.Name("XObject"),
			"Subtype": objects.Name("Form"),
			// Translate by (100, 500) in the invoker's space.
			"Matrix": objects.Array{
				objects.Int(1), objects.Int(0), objects.Int(0), objects.Int(1),
				objects.Int(100), objects.Int(500),
			},
		},
		Raw: []byte(`BT /F1 12 Tf 0 0 Td (placed) Tj ET`),
	}
	page := objects.Dict{
		"Type":     objects.Name("Page"),
		"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
		"Resources": objects.Dict{
			"Font":    objects.Dict{"F1": fontRef},
			"XObject": objects.Dict{"X0": formRef},
		},
	}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			fontRef: helvetica(),
			formRef: form,
		},
		pages:   []objects.Dict{page},
		content: [][]byte{[]byte(`/X0 Do`)},
	}
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(p.Blocks))
	}
	box := p.Blocks[0].Box
	if box.X0 < 99 || box.X0 > 101 {
		t.Errorf("box X0 = %v, want ~100: the form matrix composed the wrong way", box.X0)
	}
	if box.Y0 < 495 || box.Y0 > 505 {
		t.Errorf("box Y0 = %v, want ~500", box.Y0)
	}
}

// TestRecursiveFormTerminates: a form that invokes itself is a hostile or damaged
// document, and the depth bound is what keeps it from exhausting the stack.
func TestRecursiveFormTerminates(t *testing.T) {
	fontRef := objects.Ref{Num: 10}
	formRef := objects.Ref{Num: 20}
	form := &objects.Stream{
		Dict: objects.Dict{
			"Type":    objects.Name("XObject"),
			"Subtype": objects.Name("Form"),
			"Resources": objects.Dict{
				"Font":    objects.Dict{"F1": fontRef},
				"XObject": objects.Dict{"X0": formRef},
			},
		},
		Raw: []byte(`BT /F1 12 Tf 0 0 Td (loop) Tj ET /X0 Do`),
	}
	page := objects.Dict{
		"Type":     objects.Name("Page"),
		"MediaBox": objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
		"Resources": objects.Dict{
			"Font":    objects.Dict{"F1": fontRef},
			"XObject": objects.Dict{"X0": formRef},
		},
	}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			fontRef: helvetica(),
			formRef: form,
		},
		pages:   []objects.Dict{page},
		content: [][]byte{[]byte(`/X0 Do`)},
	}
	// The assertion is that this returns at all.
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if n := strings.Count(p.Text(), "loop"); n > maxFormDepth+1 {
		t.Errorf("descended %d times, bound is %d", n, maxFormDepth)
	}
}

// TestCropBoxPreferred: the crop box is what a viewer shows and what coverage must
// measure against. A media box far larger than the crop box would report a page of
// prose as mostly empty and route it to OCR.
func TestCropBoxPreferred(t *testing.T) {
	s := onePage(`BT /F1 12 Tf 10 700 Td (x) Tj ET`)
	s.pages[0]["MediaBox"] = objects.Array{
		objects.Int(0), objects.Int(0), objects.Int(2000), objects.Int(2000),
	}
	s.pages[0]["CropBox"] = objects.Array{
		objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792),
	}
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if p.Box.X1 != 612 || p.Box.Y1 != 792 {
		t.Errorf("box = %v, want the crop box", p.Box)
	}
}

// TestMissingBoxFallsBackToLetter: a page with neither box is malformed, and a zero
// box would make every page report no coverage and send a readable document to the
// rasterizer.
func TestMissingBoxFallsBackToLetter(t *testing.T) {
	s := onePage(`BT /F1 12 Tf 10 700 Td (x) Tj ET`)
	delete(s.pages[0], "MediaBox")
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if p.Box.X1 != 612 || p.Box.Y1 != 792 {
		t.Errorf("box = %v, want US Letter", p.Box)
	}
}

// TestMissingFontLosesGlyphsNotPage: a resource dictionary naming a font it does
// not define is malformed and common. The glyphs are unrecoverable; the rest of the
// page is not.
func TestMissingFontLosesGlyphsNotPage(t *testing.T) {
	got := extractText(t, `BT /FMISSING 12 Tf 10 700 Td (lost) Tj ET
BT /F1 12 Tf 10 650 Td (kept) Tj ET`)
	if strings.Contains(got, "lost") {
		t.Errorf("decoded text with no font: %q", got)
	}
	if !strings.Contains(got, "kept") {
		t.Errorf("lost the whole page over one bad font: %q", got)
	}
}

// TestFailedPageIsEmptyNotFatal: one malformed page out of a thousand must not cost
// the other 999, and an empty page is visible where a missing one would silently
// renumber everything after it.
func TestFailedPageIsEmptyNotFatal(t *testing.T) {
	s := onePage(`BT /F1 12 Tf 10 700 Td (page one) Tj ET`)
	// A second page the store cannot produce.
	s.pages = append(s.pages, nil)

	d, err := New(s, DefaultOptions).Document()
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if len(d.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(d.Pages))
	}
	if d.Pages[0].Text() != "page one" {
		t.Errorf("page 1 = %q", d.Pages[0].Text())
	}
	if d.Pages[1].Number != 2 {
		t.Errorf("page 2 numbered %d: a failed page must not renumber the rest", d.Pages[1].Number)
	}
}

// TestBlankPageIsNotAnError: no content stream is a legitimately blank page, and
// also what a damaged one looks like. Either way the page exists.
func TestBlankPageIsNotAnError(t *testing.T) {
	s := onePage("")
	s.content = nil
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(p.Blocks) != 0 {
		t.Errorf("blocks = %d, want 0", len(p.Blocks))
	}
	if p.Box.IsZero() {
		t.Error("blank page lost its box")
	}
}

// TestZeroToleranceReplaced: a zero SpaceFrac would infer a space between every
// pair of glyphs, which is why the zero value is replaced rather than honored.
func TestZeroToleranceReplaced(t *testing.T) {
	s := onePage(`BT /F1 12 Tf 10 700 Td (Hello) Tj ET`)
	p, err := New(s, Options{}).Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if got := p.Text(); got != "Hello" {
		t.Errorf("got %q with a zero Options: thresholds were not defaulted", got)
	}
}

// TestEmptyBlocksDropped: a positioned rectangle with no text is something a
// producer left behind, and every stage from sectionize onward has to skip it.
func TestEmptyBlocksDropped(t *testing.T) {
	p := extractPage(t, `BT /F1 12 Tf 10 700 Td () Tj ET`)
	if len(p.Blocks) != 0 {
		t.Errorf("blocks = %d, want 0", len(p.Blocks))
	}
}

// TestCoverageReflectsText ties the extractor to the OCR router's input: a page
// with one word must report low coverage, and a page of prose a high one. Getting
// the box or the block bounds wrong shows up here as a routing decision, which is
// the consequence that matters.
func TestCoverageReflectsText(t *testing.T) {
	sparse := extractPage(t, `BT /F1 12 Tf 10 700 Td (word) Tj ET`)
	if c := sparse.Coverage(); c > 0.05 {
		t.Errorf("sparse coverage = %v, want small", c)
	}

	var sb strings.Builder
	sb.WriteString("BT /F1 12 Tf\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "1 0 0 1 40 %d Tm (the quick brown fox jumps over the lazy dog) Tj\n", 740-i*14)
	}
	sb.WriteString("ET\n")
	dense := extractPage(t, sb.String())
	if c := dense.Coverage(); c < 0.3 {
		t.Errorf("dense coverage = %v, want substantial: a full page would route to OCR", c)
	}
}

// TestRotatedTextFormsItsOwnLine: a sideways table header shares y values with the
// horizontal text beside it, and a reader comparing raw y groups them together.
// Baseline projection is what keeps them apart.
func TestRotatedTextFormsItsOwnLine(t *testing.T) {
	// Horizontal text, then 90-degree rotated text at a nearby position.
	stream := `BT /F1 12 Tf 1 0 0 1 100 400 Tm (across) Tj ET
BT /F1 12 Tf 0 1 -1 0 100 400 Tm (down) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: rotated text was grouped with horizontal", len(p.Blocks))
	}
}

// TestSpaceScalesWithFontSize: the threshold is a fraction of the font's own space
// advance at its own size, so a 7-point footnote and 24-point display type get the
// same treatment. A page-wide absolute threshold is wrong at every size but one.
//
// Both cases leave the same 1.312pt gap, and it means opposite things. At 7pt a
// space advances 1.946pt and the threshold is 0.584pt, so the gap is a word
// boundary. At 24pt a space advances 6.672pt and the threshold is 2.0pt, so the
// same gap is kerning inside a word. An absolute threshold cannot tell them apart.
func TestSpaceScalesWithFontSize(t *testing.T) {
	// "ab" in Helvetica is 1112/1000 em: 7.784pt at 7pt, 26.688pt at 24pt.
	small := extractText(t, `BT /F1 7 Tf 10 700 Td (ab) Tj 9.096 0 Td (cd) Tj ET`)
	if small != "ab cd" {
		t.Errorf("7pt: got %q, want %q", small, "ab cd")
	}
	large := extractText(t, `BT /F1 24 Tf 10 700 Td (ab) Tj 28 0 Td (cd) Tj ET`)
	if large != "abcd" {
		t.Errorf("24pt: got %q, want %q: the threshold did not scale", large, "abcd")
	}
}

// TestMetadataReadFromCatalog covers the frontmatter path: what the file says
// about itself, and which extraction path ran.
func TestMetadataReadFromCatalog(t *testing.T) {
	s := onePage(`BT /F1 12 Tf 10 700 Td (x) Tj ET`)
	d, err := New(s, DefaultOptions).Document()
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if d.Meta.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", d.Meta.Version)
	}
	if d.Meta.Tagged {
		t.Error("reported tagged with no StructTreeRoot")
	}
	if d.Meta.Encrypted {
		t.Error("reported encrypted")
	}
}

func TestProjectReducesToXYForHorizontalText(t *testing.T) {
	// An unrotated matrix must project to exactly x and y, or every box on every
	// ordinary page is subtly wrong.
	m := geom.Matrix{A: 12, D: 12, E: 100, F: 700}
	orient, along, cross := project(m, 100, 700)
	if orient != 0 {
		t.Errorf("orient = %d, want 0", orient)
	}
	if along != 100 || cross != 700 {
		t.Errorf("got (%v, %v), want (100, 700)", along, cross)
	}
}

func TestProjectDegenerateMatrix(t *testing.T) {
	// Tz 0 or a zero font size. The glyph paints nothing; it must still group
	// somewhere rather than forming a line of its own per glyph.
	orient, along, cross := project(geom.Matrix{}, 50, 60)
	if orient != 0 || along != 50 || cross != 60 {
		t.Errorf("got (%d, %v, %v), want (0, 50, 60)", orient, along, cross)
	}
}

// TestSizeBreakConservesText is the property that makes the size test safe to change.
//
// Where a block boundary falls is a judgement, and it is expected to move. Which
// characters are on the page is not. A segmentation rule that dropped a line, or
// duplicated one into both blocks, would still satisfy every threshold assertion above
// while quietly corrupting the output — so this compares the text with the rule on
// against the text with it off, over every fixture, ignoring whitespace because the
// join-with-a-space behavior is exactly what a block boundary changes.
//
// Measured when the rule landed: of the 34 committed fixtures, 8 have boundaries that
// move and none loses or gains a character.
func TestSizeBreakConservesText(t *testing.T) {
	var files []string
	for _, g := range []string{
		filepath.Join("..", "testdata", "*", "*.pdf"),
		filepath.Join("..", "testdata", "*.pdf"),
	} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Skip("no fixtures found")
	}

	off := DefaultOptions
	tol := geom.DefaultTolerance
	tol.SizeFrac = 0
	off.Tol = tol

	checked, moved := 0, 0
	for _, f := range files {
		on, ok1 := allText(f, DefaultOptions)
		was, ok2 := allText(f, off)
		if !ok1 || !ok2 {
			// An unreadable fixture is another test's failure, not this one's.
			continue
		}
		checked++
		if squeeze(on) != squeeze(was) {
			t.Errorf("%s: text changed when block boundaries moved (%d vs %d non-space chars)",
				filepath.Base(f), len(squeeze(on)), len(squeeze(was)))
			continue
		}
		if on != was {
			moved++
		}
	}
	if checked == 0 {
		t.Skip("no fixtures opened")
	}
	if moved == 0 {
		t.Errorf("no fixture's block boundaries moved over %d files: the size test is not reaching real documents", checked)
	}
	t.Logf("%d fixtures checked, %d with boundaries moved, none losing text", checked, moved)
}

func allText(path string, opt Options) (string, bool) {
	s, err := pcstore.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = s.Close() }()
	d, err := New(s, opt).Document()
	if err != nil {
		return "", false
	}
	var sb strings.Builder
	for pi := range d.Pages {
		for _, b := range d.Pages[pi].Blocks {
			sb.WriteString(b.Text())
			sb.WriteByte('\n')
		}
	}
	return sb.String(), true
}

// squeeze removes all whitespace. A moved block boundary changes where a newline falls
// and whether two lines are joined by a space, which is the change being permitted;
// everything else must be identical.
func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

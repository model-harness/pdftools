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

// TestWrapSpaceTrailsThePreviousSpan pins *which* span the wrap space lands on, which
// the test above cannot see: it concatenates the spans, and " second" and "first " read
// the same way once joined.
//
// It has to be the previous span's trailing end, because a consumer regroups spans. Both
// sectionize.title and doc.Block.Text join them with no separator, and sectionize joins
// them in the order a structure element lists its content rather than in page order — so
// a space riding on a span's leading edge travels away from the neighbour it belonged to
// and reappears somewhere it does not. Measured over the 11 tagged documents: leading
// placement ran "revision" out as "re" + "-" + " vision", and "surrounding", "structure",
// "digest", and 12 more the same way, while running clause numbers into the sentence
// before them ("...an ISO 32000-2 document.-5.5.2.3"). Trailing placement fixes all 29
// and breaks none.
func TestWrapSpaceTrailsThePreviousSpan(t *testing.T) {
	// Two lines in one paragraph, each a distinct style so they cannot merge into one
	// span: the join has to be visible as span text, not hidden by a concatenation.
	stream := `BT /F1 12 Tf 10 700 Td (first) Tj /F2 12 Tf 0 -14 Td (second) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(p.Blocks))
	}
	spans := p.Blocks[0].Spans
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2: the two lines are set in different fonts", len(spans))
	}
	if spans[0].Text != "first " {
		t.Errorf("spans[0].Text = %q, want %q: the wrap space belongs to the outgoing span", spans[0].Text, "first ")
	}
	if spans[1].Text != "second" {
		t.Errorf("spans[1].Text = %q, want %q: the incoming span must not carry a leading space", spans[1].Text, "second")
	}
}

// TestWrapSpaceNotDoubledWhereTheLineAlreadyEndsInOne is the endsWithSpace half of the
// guard above, which trailing placement made load-bearing in a way leading placement was
// not.
//
// A producer that ends a line with a space glyph has already stated the word boundary, so
// inferring another one doubles it. Under leading placement the guard tested the previous
// *span's* text; it now tests the string being appended to, and a line ending in its own
// space is the case that separates the two. Not a synthetic worry: 1,130 lines of the
// corpus's Markdown end in two or more spaces, which in Markdown is a hard line break.
func TestWrapSpaceNotDoubledWhereTheLineAlreadyEndsInOne(t *testing.T) {
	stream := `BT /F1 12 Tf 10 700 Td (one ) Tj 0 -14 Td (two) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(p.Blocks))
	}
	if got, want := p.Blocks[0].Text(), "one two"; got != want {
		t.Errorf("text = %q, want %q: the line's own trailing space is the word boundary", got, want)
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

// TestIndentBreaksParagraphAtOneLeading is the case neither of the other two block
// rules can see: a paragraph boundary where the vertical step and the type size are
// both identical to an ordinary line wrap.
//
// The stream is reference/paragraphs.pdf's geometry in miniature — a 9.963pt line
// height and an 11.955pt step throughout, so every pair has the same 1.200 ratio and
// no ParaFrac separates them, at any value. What marks the boundary is that line 3
// starts 15pt right of where lines 2 and 3 of the first paragraph sit, repeating the
// indent line 1 was set with.
func TestIndentBreaksParagraphAtOneLeading(t *testing.T) {
	// x=25 for both paragraphs' first lines, x=10 for the continuations: a 15pt
	// indent, which is three space widths of 12pt Helvetica.
	stream := `BT /F1 12 Tf 25 700 Td (first para line one) Tj ` +
		`1 0 0 1 10 688.045 Tm (continues here) Tj ` +
		`1 0 0 1 10 676.09 Tm (and here) Tj ` +
		`1 0 0 1 25 664.135 Tm (second para line one) Tj ` +
		`1 0 0 1 10 652.18 Tm (continues too) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 2 {
		var got []string
		for _, b := range p.Blocks {
			got = append(got, b.Text())
		}
		t.Fatalf("blocks = %d, want 2: the indent is the only evidence of the break\n%q", len(p.Blocks), got)
	}
	if got, want := p.Blocks[0].Text(), "first para line one continues here and here"; got != want {
		t.Errorf("blocks[0] = %q, want %q", got, want)
	}
	if got, want := p.Blocks[1].Text(), "second para line one continues too"; got != want {
		t.Errorf("blocks[1] = %q, want %q", got, want)
	}
}

// TestIndentIgnoredWhereLinesAreCentred is the spread guard, and it is load-bearing
// rather than defensive.
//
// Centred type has no left margin, so "indented past the margin" is not a question
// that can be asked of it — but the arithmetic still produces an answer, and without
// this guard it produced a wrong one. pymupdf/dotted-gridlines.pdf is the case: a
// centred table header whose lines start within about two points of each other, which
// against that document's 1.335pt space advance reads as 1.35 space widths and split
// "COMUNI SUPERIORI 15.000 abitanti (SUP)" in half mid-phrase.
//
// Reproduced here rather than asserted on that fixture so the failure is legible, and
// with the same shape the defect had: the fourth line repeats the first line's offset
// exactly, so every other part of the rule agrees it is a paragraph start and only the
// wandering left edge says otherwise. 12pt Helvetica's space advance is 3.336pt, so the
// 2pt jitter below is 0.6 space widths — just past the half-space the guard allows, and
// the offset of 20 against a 10 margin is 3.00 space widths, squarely inside the indent
// window.
func TestIndentIgnoredWhereLinesAreCentred(t *testing.T) {
	stream := `BT /F1 12 Tf 20 700 Td (centred one) Tj ` +
		`1 0 0 1 10 688.045 Tm (centred two) Tj ` +
		`1 0 0 1 12 676.09 Tm (centred three) Tj ` +
		`1 0 0 1 20 664.135 Tm (centred four) Tj ET`
	p := extractPage(t, stream)
	if len(p.Blocks) != 1 {
		var got []string
		for _, b := range p.Blocks {
			got = append(got, b.Text())
		}
		t.Fatalf("blocks = %d, want 1: centred lines have no margin to be indented past\n%q", len(p.Blocks), got)
	}
}

// TestIndentWindowBounds pins the two ends of the indent window, which no other test
// reaches — mutation-checked, and both bounds survived removal until this existed.
//
// Both cases are a line repeating its block's first-line offset exactly, so the rest of
// the rule agrees and only the width of the offset decides. 12pt Helvetica's space
// advance is 3.336pt.
func TestIndentWindowBounds(t *testing.T) {
	// A line's own left sidebearing and a producer's rounding both move a left edge by
	// a fraction of a point, so an offset under one space width is noise rather than an
	// indent. 2pt is 0.6 space widths: inside the half-space agreement the same-indent
	// check allows, but below the floor.
	t.Run("below the floor is not an indent", func(t *testing.T) {
		stream := `BT /F1 12 Tf 12 700 Td (para one line one) Tj ` +
			`1 0 0 1 10 688.045 Tm (continues here) Tj ` +
			`1 0 0 1 10 676.09 Tm (and here) Tj ` +
			`1 0 0 1 12 664.135 Tm (not a new para) Tj ET`
		if p := extractPage(t, stream); len(p.Blocks) != 1 {
			t.Errorf("blocks = %d, want 1: a 0.6 space-width offset is noise, not an indent", len(p.Blocks))
		}
	})

	// Past the ceiling the offset is column or cell placement. 30pt is 8.99 space
	// widths, against a corpus whose rejected offsets run to 17, 63 and 94.
	t.Run("past the ceiling is column placement", func(t *testing.T) {
		stream := `BT /F1 12 Tf 40 700 Td (para one line one) Tj ` +
			`1 0 0 1 10 688.045 Tm (continues here) Tj ` +
			`1 0 0 1 10 676.09 Tm (and here) Tj ` +
			`1 0 0 1 40 664.135 Tm (a second column) Tj ET`
		if p := extractPage(t, stream); len(p.Blocks) != 1 {
			t.Errorf("blocks = %d, want 1: a 9 space-width offset is a column, not a first line", len(p.Blocks))
		}
	})
}

// TestIndentMatchesTheBlocksOwnFirstLine pins the discriminator the rule rests on, and
// it exists because mutation testing showed nothing else did.
//
// Widening the half-space agreement to a vacuous 99 space widths left every other test
// in this file passing, which is the worst kind of gap: ADR 0010 calls matching the
// block's own first line "the whole design" — it is what takes the corpus from 441
// firings to 11 — and its magnitude was unconstrained. Made vacuous, the rule fires 226
// times over the corpus instead of 3, and only the conservation test's whitespace-blind
// comparison would have seen it, which by construction it forgives.
//
// 12pt Helvetica's space advance is 3.336pt throughout.
func TestIndentMatchesTheBlocksOwnFirstLine(t *testing.T) {
	// The population the rule was built to decline, and the reason the check is a
	// *match* rather than a threshold: a hanging-indented bullet, where the marker line
	// sits left of the margin its own continuations establish and a following line is
	// indented to that margin's right. ISO 32000-2's lists are full of these.
	//
	// Every other part of the rule agrees here — the offset is 3.00 space widths, inside
	// the window, and the continuation lines share an edge exactly — so this is the one
	// case where only the own-line comparison decides. The block opens at x=10 against a
	// margin of 20, so its own indent is -3.00 while the incoming line's is +3.00: six
	// space widths apart, which no tolerance worth having admits.
	t.Run("a hanging indent is not a first-line indent", func(t *testing.T) {
		stream := `BT /F1 12 Tf 10 700 Td (bullet marker line) Tj ` +
			`1 0 0 1 20 688.045 Tm (hanging continuation) Tj ` +
			`1 0 0 1 20 676.09 Tm (and another) Tj ` +
			`1 0 0 1 30 664.135 Tm (indented past the margin) Tj ET`
		p := extractPage(t, stream)
		if len(p.Blocks) != 1 {
			var got []string
			for _, b := range p.Blocks {
				got = append(got, b.Text())
			}
			t.Fatalf("blocks = %d, want 1: the block's own first line is left of its margin, so an indent past that margin does not repeat it\n%q",
				len(p.Blocks), got)
		}
	})

	// The other end: the agreement is a tolerance and not an equality, so a boundary
	// whose indent is *nearly* the first line's must still be found. 0.5pt is 0.15 space
	// widths, which is the scale a producer's rounded Tm operands disagree on — the
	// coordinates in reference/paragraphs.pdf are written to three decimals, and nothing
	// guarantees two paragraphs' first lines round identically.
	t.Run("a near-match still starts a paragraph", func(t *testing.T) {
		stream := `BT /F1 12 Tf 25 700 Td (first para line one) Tj ` +
			`1 0 0 1 10 688.045 Tm (continues here) Tj ` +
			`1 0 0 1 10 676.09 Tm (and here) Tj ` +
			`1 0 0 1 25.5 664.135 Tm (second para line one) Tj ET`
		if p := extractPage(t, stream); len(p.Blocks) != 2 {
			t.Errorf("blocks = %d, want 2: a 0.15 space-width disagreement is producer rounding, not a different indent", len(p.Blocks))
		}
	})

	// The spread guard is a tolerance for the same reason, and tightening it to exact
	// agreement was the third surviving mutation. Left-aligned continuations agree to
	// within float noise rather than exactly: 0.05pt here is 0.015 space widths, which
	// must not be read as the wandering edge of centred type.
	t.Run("float noise in the margin is not centred type", func(t *testing.T) {
		stream := `BT /F1 12 Tf 25 700 Td (first para line one) Tj ` +
			`1 0 0 1 10 688.045 Tm (continues here) Tj ` +
			`1 0 0 1 10.05 676.09 Tm (and here) Tj ` +
			`1 0 0 1 25 664.135 Tm (second para line one) Tj ET`
		if p := extractPage(t, stream); len(p.Blocks) != 2 {
			t.Errorf("blocks = %d, want 2: a 0.015 space-width margin disagreement is noise, not a wandering centre", len(p.Blocks))
		}
	})
}

// TestIndentBreakConservesText is TestSizeBreakConservesText for the indent rule, and
// exists for the same reason: where a boundary falls is a judgement that may move,
// which characters are on the page is not.
//
// Measured when the rule landed: 48 PDFs on this machine — 35 committed fixtures plus
// the 12 gitignored documents in docs/ and this rule's own new fixture — of which 47
// open and two have boundaries that move, with none losing or gaining a character. The
// count is logged rather than asserted because a fresh clone has only the committed
// ones; what is asserted is the property and the moved > 0 floor below.
func TestIndentBreakConservesText(t *testing.T) {
	files := corpusPDFs()
	if len(files) == 0 {
		t.Skip("no fixtures found")
	}

	off := DefaultOptions
	tol := geom.DefaultTolerance
	tol.IndentFrac = 0
	off.Tol = tol

	checked, moved := 0, 0
	for _, f := range files {
		on, ok1 := allText(f, DefaultOptions)
		was, ok2 := allText(f, off)
		if !ok1 || !ok2 {
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
	// Without this the test passes trivially if IndentFrac stops firing entirely,
	// which is the regression most likely to go unnoticed: the rule is off by default
	// nowhere and silent everywhere would look like success.
	if moved == 0 {
		t.Errorf("no fixture's block boundaries moved over %d files: the indent test is not reaching real documents", checked)
	}
	t.Logf("%d fixtures checked, %d with boundaries moved, none losing text", checked, moved)
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
// Measured when the rule landed: of the 34 committed fixtures then reachable, 8 had
// boundaries that move and none lost or gained a character. Sharing corpusPDFs with the
// indent test widened this to every PDF on the machine, and the same property now holds
// over 47 files with 20 moving — the rule was always reaching those documents, and the
// narrower glob was under-reporting rather than the rule under-firing.
func TestSizeBreakConservesText(t *testing.T) {
	files := corpusPDFs()
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

// corpusPDFs returns every PDF on this machine, committed fixture or not.
//
// The sponsored specifications in docs/ are gitignored and absent from a fresh clone,
// which is why every caller treats an empty result as a skip: these tests measure a
// property over whatever documents are present rather than asserting a count.
func corpusPDFs() []string {
	var files []string
	for _, g := range []string{
		filepath.Join("..", "testdata", "*", "*.pdf"),
		filepath.Join("..", "testdata", "*", "*", "*.pdf"),
		filepath.Join("..", "testdata", "*.pdf"),
		filepath.Join("..", "docs", "*.pdf"),
	} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	sort.Strings(files)
	return files
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

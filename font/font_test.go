package font

import (
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/objects"
)

// store is a minimal objects.Store over an in-memory table, so font dictionary
// shapes can be tested without a PDF file. Every shape below is one this repo's
// corpus survey actually found, which is what makes these fixtures worth having.
type store struct {
	objs map[objects.Ref]objects.Object
}

func (s *store) Resolve(o objects.Object) (objects.Object, error) {
	for i := 0; i < 8; i++ {
		ref, isRef := o.(objects.Ref)
		if !isRef {
			return o, nil
		}
		v, ok := s.objs[ref]
		if !ok {
			return objects.Null{}, nil
		}
		o = v
	}
	return objects.Null{}, nil
}
func (s *store) Trailer() (objects.Dict, error)  { return objects.Dict{}, nil }
func (s *store) Catalog() (objects.Dict, error)  { return objects.Dict{}, nil }
func (s *store) PageCount() int                  { return 1 }
func (s *store) Page(int) (objects.Dict, error)  { return objects.Dict{}, nil }
func (s *store) PageContent(int) ([]byte, error) { return nil, nil }
func (s *store) Version() string                 { return "2.0" }
func (s *store) Encrypted() bool                 { return false }
func (s *store) Close() error                    { return nil }

// Decode treats Raw as already-decoded, which is what an uncompressed stream in a
// fixture is.
func (s *store) Decode(st *objects.Stream) error {
	st.Decoded = st.Raw
	return nil
}

func newStore() *store { return &store{objs: map[objects.Ref]objects.Object{}} }

// toUnicodeStream builds a /ToUnicode CMap stream mapping the given codes.
func toUnicodeStream(pairs ...[2]string) *objects.Stream {
	var b strings.Builder
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")
	b.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	b.WriteString("1 beginbfchar\n")
	for _, p := range pairs {
		b.WriteString("<" + p[0] + "> <" + p[1] + ">\n")
	}
	b.WriteString("endbfchar\nendcmap\nend\nend\n")
	return &objects.Stream{Dict: objects.Dict{}, Raw: []byte(b.String())}
}

func TestSimpleFontWidthsIndexFromFirstChar(t *testing.T) {
	// /Widths is indexed from /FirstChar, not from zero. Getting this off by
	// FirstChar shifts every advance on the page, which reads as text that drifts
	// progressively out of position rather than as an obvious failure.
	s := newStore()
	f := Load(s, objects.Dict{
		"Type":      objects.Name("Font"),
		"Subtype":   objects.Name("TrueType"),
		"BaseFont":  objects.Name("SomeFont"),
		"Encoding":  objects.Name("WinAnsiEncoding"),
		"FirstChar": objects.Int('A'),
		"LastChar":  objects.Int('C'),
		"Widths":    objects.Array{objects.Int(100), objects.Int(200), objects.Int(300)},
	})

	for _, tc := range []struct {
		code uint32
		want float64
		why  string
	}{
		{'A', 100, "the first entry belongs to FirstChar"},
		{'B', 200, "consecutive codes take consecutive entries"},
		{'C', 300, "the last entry belongs to LastChar"},
		{'D', 0, "past LastChar falls back to MissingWidth, which defaults to 0"},
		{'@', 0, "before FirstChar must not index backwards into the array"},
	} {
		if got := f.Width(tc.code, tc.code); got != tc.want {
			t.Errorf("Width(%q) = %v, want %v: %s", rune(tc.code), got, tc.want, tc.why)
		}
	}
}

// TestType3WidthsScaleByFontMatrix is the regression for advances read as 1/1000 in
// the one font kind where that is not the unit.
//
// Type 3 is the exception in §9.6.4: its /Widths are in a glyph space whose mapping
// to text space is /FontMatrix, where every other kind has that mapping fixed at
// 1/1000. The numbers here are a real pdfTeX font — /FontMatrix 0.00836 with widths
// near 60 — because that is where the defect was measured: the five glyphs of
// "First" sum to 275.64, which the file means as 275.64*0.00836 = 2.30 text-space
// units per em rather than 0.276, an 8.36x error. Read as 1/1000 the pen falls a
// word behind on every word, and since extract infers spaces by comparing measured
// gaps against these advances, every Type 3 run became its own text block.
//
// Asserted in the 1/1000 units Width promises rather than in text space, because
// normalizing at load is what keeps that promise true for every caller: extract
// divides by 1000 and knows nothing about font kinds.
func TestType3WidthsScaleByFontMatrix(t *testing.T) {
	load := func(matrix objects.Object) *Font {
		d := objects.Dict{
			"Type":      objects.Name("Font"),
			"Subtype":   objects.Name("Type3"),
			"FirstChar": objects.Int('a'),
			"LastChar":  objects.Int('a'),
			"Widths":    objects.Array{objects.Real(60)},
			"CharProcs": objects.Dict{},
			"Encoding":  objects.Dict{"Differences": objects.Array{objects.Int('a'), objects.Name("a")}},
		}
		if matrix != nil {
			d["FontMatrix"] = matrix
		}
		return Load(newStore(), d)
	}

	m := func(a float64) objects.Array {
		return objects.Array{
			objects.Real(a), objects.Int(0), objects.Int(0),
			objects.Real(a), objects.Int(0), objects.Int(0),
		}
	}

	for _, tc := range []struct {
		name   string
		matrix objects.Object
		want   float64
		why    string
	}{
		{"pdfTeX", m(0.00836), 60 * 0.00836 * 1000, "the matrix's horizontal scale is the glyph space unit"},
		{"thousandth", m(0.001), 60, "0.001 is the convention every other kind fixes, so it is a no-op"},
		{"absent", nil, 60, "§9.6.4's default for a missing /FontMatrix is [0.001 0 0 0.001 0 0]"},
		{"short", objects.Array{objects.Real(0.01)}, 60, "a matrix too short to read is a defective dictionary, not a scale of 0.01"},
		{"five elements", m(0.01)[:5], 60, "an affine matrix is six numbers; five is malformed, not a readable first element"},
		{"zero scale", m(0), 60, "a zero scale would stack the whole page at one point"},
		// A negative horizontal scale is a legal affine transform — a mirrored glyph
		// space — and the advance it describes really does run leftwards. Carried
		// through as a negative width rather than clamped, because extract adds the
		// advance to the pen and a mirrored run that walks backwards is the file's
		// intent. Clamping to zero would stack the run instead.
		{"mirrored", m(-0.00836), -60 * 0.00836 * 1000, "a negative scale mirrors glyph space and the pen moves leftwards"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Compared with a tolerance because the assertion and the code reach the
			// same product by different multiplication orders, and 60*0.00836*1000
			// is not bit-identical to 60*(0.00836*1000). An exact comparison here
			// would test IEEE association, not the scaling.
			if got := load(tc.matrix).Width('a', 'a'); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Width = %v, want %v: %s", got, tc.want, tc.why)
			}
		})
	}

	// A non-Type3 font carrying a /FontMatrix must be left alone: the entry is
	// meaningless there, and scaling by it would corrupt a font that is correct.
	f := Load(newStore(), objects.Dict{
		"Subtype":    objects.Name("Type1"),
		"FirstChar":  objects.Int('a'),
		"Widths":     objects.Array{objects.Real(60)},
		"FontMatrix": m(0.00836),
	})
	if got := f.Width('a', 'a'); got != 60 {
		t.Errorf("Type1 Width = %v, want 60: /FontMatrix applies to Type 3 only", got)
	}
}

// TestType3MissingWidthScalesToo covers the fallback path, which is a separate
// field and so a separate chance to leave one unit behind. A Type 3 font whose
// /Widths does not cover a code falls back to /MissingWidth, and that number is in
// the same glyph space as the array it stands in for.
func TestType3MissingWidthScalesToo(t *testing.T) {
	f := Load(newStore(), objects.Dict{
		"Subtype":        objects.Name("Type3"),
		"FirstChar":      objects.Int('a'),
		"LastChar":       objects.Int('a'),
		"Widths":         objects.Array{objects.Real(60)},
		"FontDescriptor": objects.Dict{"MissingWidth": objects.Real(50)},
		"FontMatrix": objects.Array{
			objects.Real(0.01), objects.Int(0), objects.Int(0),
			objects.Real(0.01), objects.Int(0), objects.Int(0),
		},
	})
	// 'z' is outside /Widths, so it takes /MissingWidth: 50 * 0.01 * 1000.
	if got, want := f.Width('z', 'z'), 50*0.01*1000.0; got != want {
		t.Errorf("Width of an uncovered code = %v, want %v", got, want)
	}
}

// TestType3DoesNotBorrowStandard14Metrics pins the other fallback shut.
//
// An uncovered code in a simple font falls back to the standard-14 metrics, which
// is why a font may legally omit /Widths at all. A Type 3 font must not take that
// branch even when its /BaseFont names one of the fourteen, for two reasons that
// point the same way. Helvetica's advance for a glyph says nothing about a font
// whose glyph is a content stream in its own /CharProcs. And the branch returns
// 1/1000 directly, where this font's own widths have been scaled out of a 0.01
// glyph space — so the uncovered code would advance ten times its neighbours.
//
// /BaseFont /Helvetica on a Type 3 font is malformed, which is the point: the test
// constructs the one dictionary that reaches the branch, because a correct file
// never will and an unreachable inconsistency stops being unreachable the moment
// some producer writes it.
func TestType3DoesNotBorrowStandard14Metrics(t *testing.T) {
	d := func(subtype objects.Name) objects.Dict {
		return objects.Dict{
			"Subtype":   subtype,
			"BaseFont":  objects.Name("Helvetica"),
			"FirstChar": objects.Int('a'),
			"LastChar":  objects.Int('a'),
			"Widths":    objects.Array{objects.Real(60)},
			"Encoding":  objects.Name("WinAnsiEncoding"),
			"FontMatrix": objects.Array{
				objects.Real(0.01), objects.Int(0), objects.Int(0),
				objects.Real(0.01), objects.Int(0), objects.Int(0),
			},
		}
	}

	// The control: the same dictionary as a Type 1 font does take the metrics, so a
	// pass below means Type 3 was excluded rather than the branch being dead.
	if got := Load(newStore(), d("Type1")).Width('A', 'A'); got == 0 {
		t.Fatal("Type1 Width of an uncovered code = 0: the standard-14 branch is not being reached, so this test proves nothing")
	}

	// No /Widths entry for 'A' and no /MissingWidth, so the honest answer is the
	// zero default. Borrowing Helvetica's 667 here would be a number in the wrong
	// unit for this font.
	if got := Load(newStore(), d("Type3")).Width('A', 'A'); got != 0 {
		t.Errorf("Type3 Width of an uncovered code = %v, want 0: standard-14 metrics do not describe /CharProcs glyphs", got)
	}

	// A dictionary with no /Subtype at all is the fail-open case, and it is safe for
	// one reason worth pinning: both the /FontMatrix scaling and this exclusion test
	// f.Subtype, so neither fires and the font stays wholly in 1/1000 units. It is
	// the mixture that would corrupt a run — scaled /Widths beside an unscaled
	// fallback — and that cannot happen while one field gates both. Anyone splitting
	// the predicate should have to change this test to do it.
	noSubtype := d("Type3")
	delete(noSubtype, "Subtype")
	f := Load(newStore(), noSubtype)
	if got := f.Width('a', 'a'); got != 60 {
		t.Errorf("covered code with no /Subtype = %v, want 60 unscaled: the matrix must not apply when the exclusion does not", got)
	}
	if got := f.Width('A', 'A'); got == 0 {
		t.Error("uncovered code with no /Subtype = 0: the standard-14 fallback should apply, in the same 1/1000 unit as the widths beside it")
	}
}

func TestSimpleFontNegativeIndexIsNotRead(t *testing.T) {
	// A code below FirstChar produces a negative index. Without the guard this
	// reads outside the slice, and in Go that is a panic on a malformed document.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":   objects.Name("Type1"),
		"BaseFont":  objects.Name("Whatever"),
		"FirstChar": objects.Int(200),
		"Widths":    objects.Array{objects.Int(500)},
	})
	if got := f.Width(0, 0); got != 0 {
		t.Errorf("Width(0) = %v, want 0", got)
	}
}

func TestDifferencesApplyAsRuns(t *testing.T) {
	// /Differences is a sequence of runs: a code, then names for consecutive codes
	// from there. Treating it as code/name pairs — a common misreading — silently
	// drops every name after the first in each run.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Subset"),
		"Encoding": objects.Dict{
			"Type":         objects.Name("Encoding"),
			"BaseEncoding": objects.Name("WinAnsiEncoding"),
			"Differences": objects.Array{
				objects.Int(65), objects.Name("alpha"), objects.Name("beta"), objects.Name("gamma"),
				objects.Int(200), objects.Name("ffi"),
			},
		},
	})

	for _, tc := range []struct {
		code byte
		want string
		why  string
	}{
		{65, "α", "the first name takes the stated code"},
		{66, "β", "the second name continues the run"},
		{67, "γ", "the third name continues the run"},
		{68, "D", "the run ends; WinAnsi still applies past it"},
		{200, "ﬃ", "a second run starts at its own stated code"},
	} {
		if got := f.Text(uint32(tc.code)); got != tc.want {
			t.Errorf("Text(%d) = %q, want %q: %s", tc.code, got, tc.want, tc.why)
		}
	}
}

func TestDifferencesOutOfRangeCodeSkipsItsNames(t *testing.T) {
	// A code outside 0..255 cannot be assigned. Clamping it would write the names
	// that follow onto code 0 or 255, putting real glyph names on the wrong bytes;
	// skipping them loses nothing that was recoverable.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Subset"),
		"Encoding": objects.Dict{
			"Differences": objects.Array{
				objects.Int(999), objects.Name("alpha"),
				objects.Int(70), objects.Name("beta"),
			},
		},
	})
	if got := f.Text(0); got != "" {
		t.Errorf("Text(0) = %q, want empty: an out-of-range code must not land on byte 0", got)
	}
	if got := f.Text(70); got != "β" {
		t.Errorf("Text(70) = %q, want beta: the following run must still apply", got)
	}
}

func TestDifferencesAtCode255DoesNotWrap(t *testing.T) {
	// Incrementing past 255 wraps a byte to 0, which would write the next name
	// onto the code for a NUL. The name after the last one is dropped instead.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Subset"),
		"Encoding": objects.Dict{
			"Differences": objects.Array{
				objects.Int(255), objects.Name("alpha"), objects.Name("beta"),
			},
		},
	})
	if got := f.Text(255); got != "α" {
		t.Errorf("Text(255) = %q, want alpha", got)
	}
	if got := f.Text(0); got != "" {
		t.Errorf("Text(0) = %q, want empty: code 255 must not wrap to 0", got)
	}
}

func TestDifferencesDoNotMutateSharedTables(t *testing.T) {
	// Two fonts naming the same base encoding must not see each other's
	// /Differences. If they share a table, the second font's overrides appear in
	// the first, and the bug shows up as text that changes depending on page order.
	s := newStore()
	a := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("A"),
		"Encoding": objects.Dict{
			"BaseEncoding": objects.Name("WinAnsiEncoding"),
			"Differences":  objects.Array{objects.Int(65), objects.Name("alpha")},
		},
	})
	b := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("B"),
		"Encoding": objects.Name("WinAnsiEncoding"),
	})
	if got := a.Text('A'); got != "α" {
		t.Errorf("font A Text('A') = %q, want alpha", got)
	}
	if got := b.Text('A'); got != "A" {
		t.Errorf("font B Text('A') = %q, want \"A\": /Differences leaked into a shared table", got)
	}
}

func TestSymbolicFontWithNoEncodingResolvesNothing(t *testing.T) {
	// A symbolic font's codes mean what its built-in encoding says, which lives
	// inside the font program. Assuming StandardEncoding for it would produce
	// confident wrong characters — the failure mode that is worse than no output,
	// because nothing downstream can detect it.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":        objects.Name("Type1"),
		"BaseFont":       objects.Name("Symbolic"),
		"FontDescriptor": objects.Dict{"Flags": objects.Int(4)}, // bit 3: symbolic
	})
	if got := f.Text('A'); got != "" {
		t.Errorf("Text('A') = %q, want empty for a symbolic font with no encoding", got)
	}

	// Non-symbolic with no encoding falls back to StandardEncoding, where the
	// ASCII range is right.
	g := Load(s, objects.Dict{
		"Subtype":        objects.Name("Type1"),
		"BaseFont":       objects.Name("Plain"),
		"FontDescriptor": objects.Dict{"Flags": objects.Int(32)}, // nonsymbolic
	})
	if got := g.Text('A'); got != "A" {
		t.Errorf("Text('A') = %q, want \"A\" for a non-symbolic font", got)
	}
}

// TestSimpleFontReportsWhatRoutesItsGlyphs pins the inputs §9.6.5.4 reads to choose a TrueType
// font's route from code to glyph: the two flags, whether there is an /Encoding at all, the base it
// names, and whether /Differences assigns anything.
//
// Each is reported as the dictionary states it rather than folded into a verdict, because the
// routes turn on combinations — both flags set, or the nonsymbolic flag with no /Encoding, is a
// font both routes claim — and a combination folded away cannot be refused. A descriptor with no
// /Flags reads as nonsymbolic, as pdfium reads it.
//
// R#2: a /Flags that is null, a reference to nothing, or present and not a number reads as
// neither flag set — pdfium's GetIntegerFor (cpdf_font.cpp) reads all three as 0, and its
// nonsymbolic default applies only to a /Flags key that is missing outright.
func TestSimpleFontReportsWhatRoutesItsGlyphs(t *testing.T) {
	s := newStore()
	withBase := objects.Dict{"BaseEncoding": objects.Name("WinAnsiEncoding")}
	for _, c := range []struct {
		name          string
		dict          objects.Dict
		sym, non      bool
		encoded, diff bool
		base          string
	}{
		{"symbolic", objects.Dict{"FontDescriptor": objects.Dict{"Flags": objects.Int(4)}},
			true, false, false, false, ""},
		{"nonsymbolic", objects.Dict{"FontDescriptor": objects.Dict{"Flags": objects.Int(32)}},
			false, true, false, false, ""},
		{"both flags", objects.Dict{"FontDescriptor": objects.Dict{"Flags": objects.Int(36)}},
			true, true, false, false, ""},
		{"neither flag", objects.Dict{"FontDescriptor": objects.Dict{"Flags": objects.Int(0)}},
			false, false, false, false, ""},
		{"no /Flags", objects.Dict{"FontDescriptor": objects.Dict{}}, false, true, false, false, ""},
		{"/Flags null", objects.Dict{"FontDescriptor": objects.Dict{"Flags": objects.Null{}}},
			false, false, false, false, ""},
		{"/Flags a dangling reference", objects.Dict{
			"FontDescriptor": objects.Dict{"Flags": objects.Ref{Num: 999}}}, false, false, false, false, ""},
		{"/Flags present and not a number", objects.Dict{
			"FontDescriptor": objects.Dict{"Flags": objects.Name("X")}}, false, false, false, false, ""},
		{"no descriptor, Symbol", objects.Dict{"BaseFont": objects.Name("Symbol")},
			true, false, false, false, ""},
		{"no descriptor, subset ZapfDingbats", objects.Dict{
			"BaseFont": objects.Name("ABCDEF+ZapfDingbats")}, true, false, false, false, ""},
		{"no descriptor, Helvetica", objects.Dict{"BaseFont": objects.Name("Helvetica")},
			false, true, false, false, ""},
		{"a named encoding", objects.Dict{"Encoding": objects.Name("WinAnsiEncoding")},
			false, true, true, false, "WinAnsiEncoding"},
		{"a named encoding this package does not carry", objects.Dict{
			"Encoding": objects.Name("MacExpertEncoding")}, false, true, true, false,
			"MacExpertEncoding"},
		{"a dictionary with a base", objects.Dict{"Encoding": withBase},
			false, true, true, false, "WinAnsiEncoding"},
		{"a dictionary with no base", objects.Dict{"Encoding": objects.Dict{}},
			false, true, true, false, ""},
		{"empty differences", objects.Dict{"Encoding": objects.Dict{
			"Differences": objects.Array{}}}, false, true, true, true, ""},
		{"differences", objects.Dict{"Encoding": objects.Dict{
			"Differences": objects.Array{objects.Int(65), objects.Name("B")}}},
			false, true, true, true, ""},
		{"differences assigning nothing", objects.Dict{"Encoding": objects.Dict{
			"Differences": objects.Array{objects.Name("B")}}},
			false, true, true, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := objects.Dict{"Subtype": objects.Name("TrueType")}
			for k, v := range c.dict {
				d[k] = v
			}
			f := Load(s, d)
			if f.Symbolic() != c.sym || f.Nonsymbolic() != c.non {
				t.Errorf("Symbolic, Nonsymbolic = %v, %v; want %v, %v", f.Symbolic(),
					f.Nonsymbolic(), c.sym, c.non)
			}
			if f.HasEncoding() != c.encoded || f.HasDifferences() != c.diff ||
				f.BaseEncoding() != c.base {
				t.Errorf("HasEncoding, HasDifferences, BaseEncoding = %v, %v, %q; want %v, %v, %q",
					f.HasEncoding(), f.HasDifferences(), f.BaseEncoding(), c.encoded, c.diff, c.base)
			}
		})
	}
}

// TestStatedGlyphNameTracksTheAssignedCode pins the bit that records a /Differences assignment
// to the code it was made for, not to some other code that happens to share a smaller modulus.
//
// Code 48 sets bit 48 of word 0 (48/64 == 0, 48%64 == 48). A modulus of 32 instead would set bit
// 16 there, so code 48 would read back as unstated and code 16 — never touched by /Differences —
// would read back as stated.
func TestStatedGlyphNameTracksTheAssignedCode(t *testing.T) {
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype": objects.Name("TrueType"),
		"Encoding": objects.Dict{
			"Differences": objects.Array{objects.Int(48), objects.Name("zero")},
		},
	})
	if got := f.StatedGlyphName(48); got != "zero" {
		t.Errorf("StatedGlyphName(48) = %q, want %q", got, "zero")
	}
	if got := f.StatedGlyphName(16); got != "" {
		t.Errorf("StatedGlyphName(16) = %q, want empty: code 16 was never assigned", got)
	}

	// A named base encoding states every name in its table, and no /Encoding states none: the
	// StandardEncoding name is this package's assumption.
	named := Load(s, objects.Dict{"Subtype": objects.Name("TrueType"),
		"Encoding": objects.Name("WinAnsiEncoding")})
	if got := named.StatedGlyphName('A'); got != "A" {
		t.Errorf("over /WinAnsiEncoding, StatedGlyphName('A') = %q, want %q", got, "A")
	}
	none := Load(s, objects.Dict{"Subtype": objects.Name("TrueType")})
	if got := none.StatedGlyphName('A'); got != "" {
		t.Errorf("with no /Encoding, StatedGlyphName('A') = %q, want empty", got)
	}
}

func TestToUnicodeOverridesEncoding(t *testing.T) {
	// Where the two disagree, /ToUnicode is the font's own statement about this
	// document's codes, and a subset font with a rearranged encoding is the usual
	// reason they differ.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":   objects.Name("TrueType"),
		"BaseFont":  objects.Name("Subset"),
		"Encoding":  objects.Name("WinAnsiEncoding"),
		"ToUnicode": toUnicodeStream([2]string{"0041", "03B1"}),
	})
	if got := f.Text('A'); got != "α" {
		t.Errorf("Text('A') = %q, want alpha from /ToUnicode, not \"A\" from WinAnsi", got)
	}
	// A code /ToUnicode does not cover still falls through to the encoding, which
	// is the whole reason both are kept.
	if got := f.Text('B'); got != "B" {
		t.Errorf("Text('B') = %q, want \"B\" from the encoding", got)
	}
}

// TestGlyphSourceNamesTheEvidence pins where each glyph's meaning came from.
//
// Text says what a code means and not how that was known, and the two are different questions: a
// document whose characters all came from /ToUnicode and one where half came from a base encoding
// guessed off the symbolic flag are equally plausible on screen and not equally trustworthy. ADR
// 0018 records why this is recorded on the deterministic path before any inference exists.
//
// The invariant is asserted as well as the values, because it is the property a consumer will rely
// on: empty text always has SourceNone, and non-empty text never does.
func TestGlyphSourceNamesTheEvidence(t *testing.T) {
	s := newStore()

	// /ToUnicode covers 'A' only; WinAnsi covers the rest of Latin.
	both := Load(s, objects.Dict{
		"Subtype":   objects.Name("TrueType"),
		"BaseFont":  objects.Name("Subset"),
		"Encoding":  objects.Name("WinAnsiEncoding"),
		"ToUnicode": toUnicodeStream([2]string{"0041", "03B1"}),
	})
	// Symbolic with no encoding and no /ToUnicode resolves nothing at all.
	neither := Load(s, objects.Dict{
		"Subtype":        objects.Name("TrueType"),
		"BaseFont":       objects.Name("Dingbats"),
		"FontDescriptor": objects.Dict{"Flags": objects.Int(4)},
	})

	for _, c := range []struct {
		name string
		f    *Font
		code byte
		text string
		src  Source
	}{
		{"a code /ToUnicode maps", both, 'A', "α", SourceToUnicode},
		{"a code only the encoding maps", both, 'B', "B", SourceEncoding},
		{"a code neither maps", neither, 'A', "", SourceNone},
	} {
		t.Run(c.name, func(t *testing.T) {
			gs := c.f.Decode([]byte{c.code})
			if len(gs) != 1 {
				t.Fatalf("Decode gave %d glyphs, want 1", len(gs))
			}
			if gs[0].Text != c.text || gs[0].Source != c.src {
				t.Errorf("Decode: text %q source %v; want %q, %v",
					gs[0].Text, gs[0].Source, c.text, c.src)
			}
			// TextSource is the same lookup by another door, so the two must not disagree —
			// one of them being right is how a second traversal drifts from the first.
			text, src := c.f.TextSource(uint32(c.code))
			if text != c.text || src != c.src {
				t.Errorf("TextSource: %q, %v; want %q, %v", text, src, c.text, c.src)
			}
			if (gs[0].Text == "") != (gs[0].Source == SourceNone) {
				t.Errorf("text %q with source %v breaks the invariant that empty text means"+
					" SourceNone and nothing else does", gs[0].Text, gs[0].Source)
			}
		})
	}

	// A composite font's only route is /ToUnicode, so a CID it does not cover has no source —
	// nothing about a CID implies a character. Both shapes are here because they reach different
	// branches: a font with no /ToUnicode at all returns early, and one that *has* a /ToUnicode
	// which simply does not cover the code has to notice that the lookup came back empty. Only the
	// first was covered at first, and reinstating a source on the empty answer passed every test.
	descendant := objects.Array{objects.Dict{
		"Subtype":  objects.Name("CIDFontType2"),
		"BaseFont": objects.Name("Sub"),
	}}
	noToUni := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("Sub"),
		"Encoding":        objects.Name("Identity-H"),
		"DescendantFonts": descendant,
	})
	// Covers CID 0x0041 and nothing else, so 0x0042 is a mapped font with an unmapped code.
	partial := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("Sub"),
		"Encoding":        objects.Name("Identity-H"),
		"DescendantFonts": descendant,
		"ToUnicode":       toUnicodeStream([2]string{"0041", "03B1"}),
	})
	for _, c := range []struct {
		name string
		f    *Font
		code []byte
		text string
		src  Source
	}{
		{"no /ToUnicode at all", noToUni, []byte{0, 'A'}, "", SourceNone},
		{"a /ToUnicode that covers the CID", partial, []byte{0, 'A'}, "α", SourceToUnicode},
		{"a /ToUnicode that does not cover it", partial, []byte{0, 'B'}, "", SourceNone},
	} {
		t.Run("composite, "+c.name, func(t *testing.T) {
			gs := c.f.Decode(c.code)
			if len(gs) != 1 {
				t.Fatalf("Decode gave %d glyphs, want 1", len(gs))
			}
			if gs[0].Text != c.text || gs[0].Source != c.src {
				t.Errorf("Decode: %q, %v; want %q, %v", gs[0].Text, gs[0].Source, c.text, c.src)
			}
			// TextSource through the composite branch, which the simple cases above cannot
			// reach: without this, returning nothing from it at all went unnoticed.
			code := uint32(c.code[0])<<8 | uint32(c.code[1])
			text, src := c.f.TextSource(code)
			if text != c.text || src != c.src {
				t.Errorf("TextSource(%#x): %q, %v; want %q, %v", code, text, src, c.text, c.src)
			}
		})
	}
}

func TestCompositeFontSplitsTwoByteCodes(t *testing.T) {
	// Identity-H means two-byte codes. A reader that splits per byte doubles the
	// glyph count and produces text that looks like interleaved noise.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":   objects.Name("Type0"),
		"BaseFont":  objects.Name("Composite"),
		"Encoding":  objects.Name("Identity-H"),
		"ToUnicode": toUnicodeStream([2]string{"0003", "0020"}, [2]string{"0024", "0041"}),
		"DescendantFonts": objects.Array{objects.Dict{
			"Subtype": objects.Name("CIDFontType2"),
			"DW":      objects.Int(1000),
			"W":       objects.Array{objects.Int(3), objects.Array{objects.Int(278)}},
		}},
	})
	if f.Kind != Composite {
		t.Fatalf("Kind = %v, want Composite", f.Kind)
	}

	glyphs := f.Decode([]byte{0x00, 0x03, 0x00, 0x24})
	if len(glyphs) != 2 {
		t.Fatalf("got %d glyphs from 4 bytes, want 2: two-byte codes were not honored", len(glyphs))
	}
	if glyphs[0].Bytes != 2 || glyphs[0].Code != 3 || glyphs[0].Text != " " {
		t.Errorf("first glyph = %+v, want code 3, 2 bytes, text %q", glyphs[0], " ")
	}
	if glyphs[0].Width != 278 {
		t.Errorf("first glyph width = %v, want 278 from /W", glyphs[0].Width)
	}
	if glyphs[1].Code != 0x24 || glyphs[1].Text != "A" {
		t.Errorf("second glyph = %+v, want code 0x24 text \"A\"", glyphs[1])
	}
	if glyphs[1].Width != 1000 {
		t.Errorf("second glyph width = %v, want 1000 from /DW", glyphs[1].Width)
	}
}

func TestCompositeDefaultWidthIs1000NotZero(t *testing.T) {
	// /DW defaults to 1000 (Table 114) while a simple font's /MissingWidth
	// defaults to 0. Using 0 for a composite font collapses every unlisted glyph
	// onto the previous one, which is how a whole line becomes one long word.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("Composite"),
		"Encoding":        objects.Name("Identity-H"),
		"DescendantFonts": objects.Array{objects.Dict{"Subtype": objects.Name("CIDFontType0")}},
	})
	if got := f.Width(5, 5); got != 1000 {
		t.Errorf("Width with no /DW and no /W = %v, want 1000", got)
	}
}

func TestParseWHandlesBothFormsInOneArray(t *testing.T) {
	// The corpus survey found both /W forms mixed inside single arrays — shapes
	// running N A N A N N N A. A parser that decides a form for the whole array
	// drops every entry of the other kind, and each dropped entry is a glyph
	// placed at the wrong position.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type0"),
		"BaseFont": objects.Name("Composite"),
		"Encoding": objects.Name("Identity-H"),
		"DescendantFonts": objects.Array{objects.Dict{
			"DW": objects.Int(1000),
			"W": objects.Array{
				// Array form: consecutive CIDs from 1.
				objects.Int(1), objects.Array{objects.Int(111), objects.Int(222)},
				// Range form: CIDs 10 through 12 all take 333.
				objects.Int(10), objects.Int(12), objects.Int(333),
				// Array form again, after a range.
				objects.Int(20), objects.Array{objects.Int(444)},
			},
		}},
	})

	for _, tc := range []struct {
		cid  uint32
		want float64
		why  string
	}{
		{1, 111, "array form, first entry"},
		{2, 222, "array form, second entry"},
		{3, 1000, "array form does not extend past its contents"},
		{10, 333, "range form, low bound"},
		{11, 333, "range form, interior"},
		{12, 333, "range form, high bound inclusive"},
		{13, 1000, "range form does not extend past its high bound"},
		{20, 444, "array form still parses after a range form"},
	} {
		if got := f.Width(tc.cid, tc.cid); got != tc.want {
			t.Errorf("Width(cid %d) = %v, want %v: %s", tc.cid, got, tc.want, tc.why)
		}
	}
}

func TestParseWToleratesMalformedArrays(t *testing.T) {
	// A truncated or nonsensical /W must not panic or hang: it arrives as
	// untrusted document data.
	cases := []struct {
		name string
		w    objects.Array
	}{
		{"empty", objects.Array{}},
		{"lone cid", objects.Array{objects.Int(1)}},
		{"range missing width", objects.Array{objects.Int(1), objects.Int(5)}},
		{"array with non-numbers", objects.Array{objects.Int(1), objects.Array{objects.Name("x")}}},
		{"reversed range", objects.Array{objects.Int(9), objects.Int(1), objects.Int(500)}},
		{"negative cid", objects.Array{objects.Int(-5), objects.Int(-1), objects.Int(500)}},
		{"leading junk", objects.Array{objects.Name("junk"), objects.Int(1), objects.Array{objects.Int(100)}}},
		{"huge span", objects.Array{objects.Int(0), objects.Int(1 << 30), objects.Int(500)}},
		{"nested arrays", objects.Array{objects.Int(1), objects.Array{objects.Array{objects.Int(1)}}}},
	}
	s := newStore()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Load(s, objects.Dict{
				"Subtype":         objects.Name("Type0"),
				"BaseFont":        objects.Name("C"),
				"Encoding":        objects.Name("Identity-H"),
				"DescendantFonts": objects.Array{objects.Dict{"W": tc.w}},
			})
			// The only requirement is that it returned and answers sanely.
			if got := f.Width(1<<20, 1<<20); got != 1000 {
				t.Errorf("Width of an unlisted CID = %v, want the 1000 default", got)
			}
		})
	}
}

func TestParseWRangeIsBounded(t *testing.T) {
	// A range spanning the whole two-byte space is a claim no real font makes, and
	// honoring it would let a three-element array allocate a large table.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("C"),
		"Encoding":        objects.Name("Identity-H"),
		"DescendantFonts": objects.Array{objects.Dict{"W": objects.Array{objects.Int(0), objects.Int(1 << 24), objects.Int(500)}}},
	})
	if len(f.cidWidths) != 0 {
		t.Errorf("a %d-wide range produced %d entries, want it skipped", 1<<24, len(f.cidWidths))
	}
}

func TestStandardFontWithNoWidthsUsesBuiltInMetrics(t *testing.T) {
	// Seven fonts in this repo's corpus omit /Widths while naming a standard-14
	// font, which §9.6.2.2 permits. Without the built-in metrics their advances
	// are unknown, and an unknown advance is a misplaced space rather than a
	// missing character.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Helvetica"),
		"Encoding": objects.Name("WinAnsiEncoding"),
	})
	for _, tc := range []struct {
		code uint32
		want float64
	}{
		{'A', 667},
		{' ', 278},
		{'i', 222},
		{'W', 944},
	} {
		if got := f.Width(tc.code, tc.code); got != tc.want {
			t.Errorf("Helvetica Width(%q) = %v, want %v", rune(tc.code), got, tc.want)
		}
	}
	if got := f.SpaceWidth(); got != 278 {
		t.Errorf("SpaceWidth = %v, want 278", got)
	}
}

func TestExplicitWidthsBeatBuiltInMetrics(t *testing.T) {
	// A font naming Helvetica but supplying /Widths is not Helvetica: it is a
	// substituted or subset face. Its own numbers win, or the text lands where a
	// different font would have put it.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":   objects.Name("Type1"),
		"BaseFont":  objects.Name("Helvetica"),
		"Encoding":  objects.Name("WinAnsiEncoding"),
		"FirstChar": objects.Int('A'),
		"Widths":    objects.Array{objects.Int(999)},
	})
	if got := f.Width('A', 'A'); got != 999 {
		t.Errorf("Width('A') = %v, want the declared 999, not Helvetica's 667", got)
	}
}

func TestSubsetPrefixIsStrippedForMetrics(t *testing.T) {
	// A subsetting tool writes "ABCDEF+Arial,Bold". Matching the raw name finds
	// nothing and the font silently loses its metrics.
	for _, tc := range []struct {
		name string
		want string
	}{
		{"ABCDEF+Helvetica", "Helvetica"},
		{"AAAAAA+Arial,Bold", "Arial,Bold"},
		{"Helvetica", "Helvetica"},
		{"ABCDE+Helvetica", "ABCDE+Helvetica"},   // five letters: not a subset tag
		{"abcdef+Helvetica", "abcdef+Helvetica"}, // lowercase: not a subset tag
		{"ABCDEF+", "ABCDEF+"},                   // nothing after the plus
		{"+Helvetica", "+Helvetica"},
		{"", ""},
	} {
		if got := stripSubsetPrefix(tc.name); got != tc.want {
			t.Errorf("stripSubsetPrefix(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}

	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("ABCDEF+Arial,Bold"),
		"Encoding": objects.Name("WinAnsiEncoding"),
	})
	if got := f.Width('A', 'A'); got != 722 {
		t.Errorf("Arial,Bold Width('A') = %v, want Helvetica-Bold's 722", got)
	}
}

func TestSimpleFontDecodeIsOneGlyphPerByte(t *testing.T) {
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Helvetica"),
		"Encoding": objects.Name("WinAnsiEncoding"),
	})
	glyphs := f.Decode([]byte("Hi there"))
	if len(glyphs) != 8 {
		t.Fatalf("got %d glyphs from 8 bytes, want 8", len(glyphs))
	}
	var text strings.Builder
	for _, g := range glyphs {
		if g.Bytes != 1 {
			t.Errorf("glyph %+v: Bytes = %d, want 1 for a simple font", g, g.Bytes)
		}
		text.WriteString(g.Text)
	}
	if got := text.String(); got != "Hi there" {
		t.Errorf("decoded %q, want %q", got, "Hi there")
	}
}

func TestLigatureDecodesToSeveralCharacters(t *testing.T) {
	// A glyph is not always one character. "f_t" has no precomposed code point, so
	// a decoder that returns one rune per glyph drops it — which is how
	// "efficient" becomes "ecient". This is the defect the corpus found in
	// font/encoding, asserted here at the level extraction actually uses.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Subset"),
		"Encoding": objects.Dict{
			"BaseEncoding": objects.Name("WinAnsiEncoding"),
			"Differences": objects.Array{
				objects.Int(1), objects.Name("f_f"), objects.Name("f_t"), objects.Name("f_f_i"),
			},
		},
	})
	for _, tc := range []struct {
		code uint32
		want string
	}{
		{1, "ff"},
		{2, "ft"},
		{3, "ffi"},
	} {
		if got := f.Text(tc.code); got != tc.want {
			t.Errorf("Text(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestVerticalWritingModeDetected(t *testing.T) {
	// A -V CMap advances on the y axis. Missing it stacks every glyph of a
	// vertical document at one point.
	s := newStore()
	v := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("C"),
		"Encoding":        objects.Name("Identity-V"),
		"DescendantFonts": objects.Array{objects.Dict{}},
	})
	if !v.Vertical {
		t.Error("Identity-V not detected as vertical")
	}
	h := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("C"),
		"Encoding":        objects.Name("Identity-H"),
		"DescendantFonts": objects.Array{objects.Dict{}},
	})
	if h.Vertical {
		t.Error("Identity-H reported as vertical")
	}
}

func TestLoadNeverFailsOnDefectiveDictionaries(t *testing.T) {
	// A defective font must not abandon the document: the rest of the page is
	// still readable, and defective font dictionaries are common in exactly the
	// files that most need extracting.
	s := newStore()
	cases := []struct {
		name string
		d    objects.Dict
	}{
		{"empty", objects.Dict{}},
		{"no subtype", objects.Dict{"BaseFont": objects.Name("X")}},
		{"type0 with no descendants", objects.Dict{"Subtype": objects.Name("Type0")}},
		{"type0 with empty descendants", objects.Dict{
			"Subtype": objects.Name("Type0"), "DescendantFonts": objects.Array{}}},
		{"type0 descendant is not a dict", objects.Dict{
			"Subtype": objects.Name("Type0"), "DescendantFonts": objects.Array{objects.Int(1)}}},
		{"widths is not an array", objects.Dict{
			"Subtype": objects.Name("Type1"), "Widths": objects.Name("nope")}},
		{"encoding is a number", objects.Dict{
			"Subtype": objects.Name("Type1"), "Encoding": objects.Int(3)}},
		{"differences is not an array", objects.Dict{
			"Subtype":  objects.Name("Type1"),
			"Encoding": objects.Dict{"Differences": objects.Int(1)}}},
		{"tounicode is not a stream", objects.Dict{
			"Subtype": objects.Name("Type1"), "ToUnicode": objects.Name("nope")}},
		{"tounicode is unparseable", objects.Dict{
			"Subtype":   objects.Name("Type1"),
			"ToUnicode": &objects.Stream{Dict: objects.Dict{}, Raw: []byte("\x00\xff not a cmap")}}},
		{"dangling descendant ref", objects.Dict{
			"Subtype":         objects.Name("Type0"),
			"DescendantFonts": objects.Array{objects.Ref{Num: 999}}}},
		{"unknown encoding name", objects.Dict{
			"Subtype": objects.Name("Type1"), "Encoding": objects.Name("MacExpertEncoding")}},
		{"unknown predefined cmap", objects.Dict{
			"Subtype":         objects.Name("Type0"),
			"Encoding":        objects.Name("UniJIS-UCS2-H"),
			"DescendantFonts": objects.Array{objects.Dict{}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Load(s, tc.d)
			if f == nil {
				t.Fatal("Load returned nil, which callers are not required to handle")
			}
			// Must answer without panicking, whatever it answers.
			f.Decode([]byte{0x00, 0x41, 0x00})
			f.Text('A')
			f.Width('A', 'A')
			f.SpaceWidth()
			f.GlyphName('A')
			f.HasToUnicode()
		})
	}
}

func TestUnknownPredefinedCMapStillSplitsTwoByte(t *testing.T) {
	// A predefined CMap this package does not carry still has two-byte codes.
	// Splitting per byte instead would double the glyph count, so the fallback
	// preserves code boundaries even though it cannot supply CIDs.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("C"),
		"Encoding":        objects.Name("UniJIS-UCS2-H"),
		"DescendantFonts": objects.Array{objects.Dict{}},
	})
	glyphs := f.Decode([]byte{0x30, 0x42, 0x30, 0x44})
	if len(glyphs) != 2 {
		t.Fatalf("got %d glyphs, want 2 two-byte codes", len(glyphs))
	}
	if glyphs[0].Code != 0x3042 {
		t.Errorf("first code = %#x, want 0x3042", glyphs[0].Code)
	}
}

func TestSpaceWidthIgnoresCode32WhenItIsNotASpace(t *testing.T) {
	// In a symbolic font with a rearranged encoding, code 32 is often a visible
	// glyph. Taking its width as the space width sets the space threshold for the
	// whole page from an arbitrary character.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":  objects.Name("Type1"),
		"BaseFont": objects.Name("Subset"),
		"Encoding": objects.Dict{
			"Differences": objects.Array{objects.Int(32), objects.Name("bullet")},
		},
		"FirstChar": objects.Int(32),
		"Widths":    objects.Array{objects.Int(350)},
	})
	if got := f.SpaceWidth(); got != 0 {
		t.Errorf("SpaceWidth = %v, want 0: code 32 maps to a bullet, not a space", got)
	}
}

func TestDecodeEmptyStringYieldsNoGlyphs(t *testing.T) {
	s := newStore()
	for _, sub := range []objects.Name{"Type1", "Type0"} {
		f := Load(s, objects.Dict{
			"Subtype":         sub,
			"BaseFont":        objects.Name("X"),
			"Encoding":        objects.Name("Identity-H"),
			"DescendantFonts": objects.Array{objects.Dict{}},
		})
		if got := f.Decode(nil); got != nil {
			t.Errorf("%s: Decode(nil) = %v, want nil", sub, got)
		}
		if got := f.Decode([]byte{}); got != nil {
			t.Errorf("%s: Decode(empty) = %v, want nil", sub, got)
		}
	}
}

func TestOddTrailingByteInCompositeStringIsNotDropped(t *testing.T) {
	// A string whose length is not a multiple of the code width is malformed, but
	// its leading codes are real. Dropping the remainder is right; dropping the
	// whole string loses text that was there.
	s := newStore()
	f := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("C"),
		"Encoding":        objects.Name("Identity-H"),
		"DescendantFonts": objects.Array{objects.Dict{}},
	})
	glyphs := f.Decode([]byte{0x00, 0x41, 0x00, 0x42, 0x99})
	total := 0
	for _, g := range glyphs {
		total += g.Bytes
	}
	if total != 5 {
		t.Errorf("glyph bytes sum to %d, want 5: a code boundary was lost", total)
	}
	if len(glyphs) != 3 {
		t.Errorf("got %d glyphs, want 3 (two full codes and the odd byte)", len(glyphs))
	}
}

func TestIndirectReferencesAreResolvedThroughout(t *testing.T) {
	// Every one of these is an indirect reference in real files. A getter that
	// forgets to resolve reads a Ref where it expected a value and silently gets
	// nothing.
	s := newStore()
	s.objs[objects.Ref{Num: 1}] = objects.Array{objects.Int(100), objects.Int(200)}
	s.objs[objects.Ref{Num: 2}] = objects.Name("WinAnsiEncoding")
	s.objs[objects.Ref{Num: 3}] = objects.Int(65)
	s.objs[objects.Ref{Num: 4}] = objects.Dict{
		"DW": objects.Int(500),
		"W":  objects.Ref{Num: 5},
	}
	s.objs[objects.Ref{Num: 5}] = objects.Array{objects.Int(1), objects.Array{objects.Int(777)}}

	f := Load(s, objects.Dict{
		"Subtype":   objects.Name("Type1"),
		"BaseFont":  objects.Name("X"),
		"Encoding":  objects.Ref{Num: 2},
		"FirstChar": objects.Ref{Num: 3},
		"Widths":    objects.Ref{Num: 1},
	})
	if got := f.Width('A', 'A'); got != 100 {
		t.Errorf("Width('A') = %v, want 100 through indirect /Widths and /FirstChar", got)
	}
	if got := f.Text('B'); got != "B" {
		t.Errorf("Text('B') = %q, want \"B\" through an indirect /Encoding", got)
	}

	c := Load(s, objects.Dict{
		"Subtype":         objects.Name("Type0"),
		"BaseFont":        objects.Name("C"),
		"Encoding":        objects.Name("Identity-H"),
		"DescendantFonts": objects.Array{objects.Ref{Num: 4}},
	})
	if got := c.Width(1, 1); got != 777 {
		t.Errorf("Width(cid 1) = %v, want 777 through an indirect /W", got)
	}
	if got := c.Width(9, 9); got != 500 {
		t.Errorf("Width(cid 9) = %v, want the indirect /DW of 500", got)
	}
}

// TestIrregularBaseFont pins C15: a /BaseFont pdfium reads as a string even though this
// package's own /BaseFont field stays empty for it.
func TestIrregularBaseFont(t *testing.T) {
	s := newStore()
	for _, c := range []struct {
		name    string
		val     objects.Object
		regular bool
	}{
		{"a name", objects.Name("Symbol"), true},
		{"a string", objects.String("Symbol"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := Load(s, objects.Dict{"Subtype": objects.Name("Type1"), "BaseFont": c.val})
			if got := f.Irregular(); (got == "") != c.regular {
				t.Errorf("Irregular() = %q, want empty = %v", got, c.regular)
			}
		})
	}
}

// TestIrregularEncoding pins the /Encoding entry itself: a name or a dictionary is regular, and
// anything else — pdfium's LoadPDFEncoding wants one of those two shapes too — is not.
func TestIrregularEncoding(t *testing.T) {
	s := newStore()
	for _, c := range []struct {
		name    string
		val     objects.Object
		regular bool
	}{
		{"a name", objects.Name("WinAnsiEncoding"), true},
		{"a dictionary", objects.Dict{"BaseEncoding": objects.Name("WinAnsiEncoding")}, true},
		{"a string", objects.String("WinAnsiEncoding"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := Load(s, objects.Dict{"Subtype": objects.Name("Type1"), "Encoding": c.val})
			if got := f.Irregular(); (got == "") != c.regular {
				t.Errorf("Irregular() = %q, want empty = %v", got, c.regular)
			}
		})
	}
}

// TestIrregularBaseEncoding pins C16: a /BaseEncoding pdfium reads as a string even though
// baseEncoding leaves f.baseName empty for it.
func TestIrregularBaseEncoding(t *testing.T) {
	s := newStore()
	for _, c := range []struct {
		name    string
		val     objects.Object
		regular bool
	}{
		{"a name", objects.Name("WinAnsiEncoding"), true},
		{"a string", objects.String("WinAnsiEncoding"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := Load(s, objects.Dict{"Subtype": objects.Name("Type1"),
				"Encoding": objects.Dict{"BaseEncoding": c.val}})
			if got := f.Irregular(); (got == "") != c.regular {
				t.Errorf("Irregular() = %q, want empty = %v", got, c.regular)
			}
		})
	}
}

// TestIrregularDifferences pins C17's four ways LoadDifferences (cpdf_simplefont.cpp) reads an
// entry this package's applyDifferences does not: a real first code and a stray non-name entry
// among them, plus a dangling reference and a /Differences that is not an array at all.
//
// R#5 pins the code range's own edges, 0 and 255, as regular: they are the codes a simple font
// actually has, not the out-of-range shape checkDifferences refuses, so a mutant that narrows the
// accepted range by one at either edge — `e < 1` or `e > 254` — must not refuse them. The codes
// one past each edge, -1 and 256, are refused, so one that widens it — `e < -1` or `e > 256` —
// must not accept them.
func TestIrregularDifferences(t *testing.T) {
	s := newStore()
	for _, c := range []struct {
		name    string
		arr     objects.Array
		regular bool
	}{
		{"a plain run", objects.Array{objects.Int(65), objects.Name("A"), objects.Name("B")}, true},
		{"the code range's lower edge, 0", objects.Array{objects.Int(0), objects.Name("A")}, true},
		{"the code range's upper edge, 255", objects.Array{objects.Int(255), objects.Name("A")}, true},
		{"one past the upper edge, 256", objects.Array{objects.Int(256), objects.Name("A")}, false},
		{"a code out of range, name after it dropped", objects.Array{objects.Int(999), objects.Name("A")}, false},
		{"a non-name entry after the first code", objects.Array{
			objects.Int(65), objects.Name("A"), objects.String("x"), objects.Name("C")}, false},
		{"a leading name", objects.Array{objects.Name("C")}, false},
		{"a negative code", objects.Array{objects.Int(-1), objects.Name("X"), objects.Name("C")}, false},
		{"a negative real code", objects.Array{objects.Real(-0.5), objects.Name("C")}, false},
		{"a real code past 255", objects.Array{objects.Real(255.5), objects.Name("C")}, false},
		{"an in-range code too large by an integer", objects.Array{objects.Int(300), objects.Name("B")}, false},
		{"a null entry", objects.Array{objects.Int(65), objects.Null{}, objects.Name("C")}, false},
		{"a dangling reference", objects.Array{objects.Int(65), objects.Ref{Num: 999}, objects.Name("C")}, false},
		// checkDifferences' doc discusses both these codes: at 4294967295, the uint32_t cur_code
		// (cpdf_simplefont.cpp) is one increment from wrapping into range, so a second name here
		// would land on code 0 the way applyDifferences never would — this row's single name does
		// not reach that, and is simply dropped, same as applyDifferences drops it. At 4294967296,
		// the code itself is already outside what cur_code can hold, so this row's one name lands
		// on code 0 immediately in pdfium, with no second name needed — a real disagreement with
		// applyDifferences' drop, not a merely theoretical one. Both rows are refused regardless:
		// checkDifferences' 0..255 check catches any code at or above 256 outright, before either
		// engine's repair, or disagreement, ever has a chance to run — this store is in memory, so
		// both hold the same on a 32-bit build too.
		{"the wrap edge, 4294967295", objects.Array{objects.Int(4294967295), objects.Name("A")}, false},
		{"one past the wrap edge, 4294967296", objects.Array{objects.Int(4294967296), objects.Name("A")}, false},
		{"empty", objects.Array{}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := Load(s, objects.Dict{"Subtype": objects.Name("Type1"),
				"Encoding": objects.Dict{"Differences": c.arr}})
			if got := f.Irregular(); (got == "") != c.regular {
				t.Errorf("Irregular() = %q, want empty = %v", got, c.regular)
			}
		})
	}
	t.Run("not an array", func(t *testing.T) {
		f := Load(s, objects.Dict{"Subtype": objects.Name("Type1"),
			"Encoding": objects.Dict{"Differences": objects.Int(1)}})
		if got := f.Irregular(); got == "" {
			t.Errorf("Irregular() = %q, want non-empty: /Differences is not an array", got)
		}
	})
}

// TestIrregularFlags pins C20 and C21: /Flags must be a literal PDF integer, and within
// [-2^31, 2^31), the range this package can trust regardless of a '+' sign (R#0; a leading
// '0' before a sign is a gap no range closes, see checkFlags' doc), or pdfium's GetIntegerFor (cpdf_font.cpp) reads a different symbolic bit, or a default,
// than flags() does. It also pins R#0's three additions: both bits set, both bits
// clear, and the key absent from a present descriptor.
//
// R#1 pins both edges of the range exactly: the lower bound -2^31 and the value one below it, and
// the upper bound 2^31-1 (INT_MAX) and the value one above it — want is the reason substring
// rather than a bare pass/fail, because R#0's bit rule reaches every one of these four values too,
// just by a different route. The exact edges are the bit rule's own doing regardless of the range
// check: -2^31 clears both bits (32 zero bits but the sign bit), and 2^31-1 sets every one of its
// bits below the sign bit, both of Table 121's included. The edges one past them are the range
// check's own doing today, but a mutant that widens min to -2^31-1 or max to 2^31 stops the range
// check firing there, and the value falls through to the bit rule instead — which finds -2^31-1
// sets both bits, and 2^31 sets neither. Both are still refused, so neither mutant reaches a route
// or loses a page; the substring assertion catches each one by the reason it now reports, not by
// what the page draws. Narrowing either edge (`<= min` or `>= max`) is symmetric: it only moves
// the range check to fire one value earlier, on -2^31 or 2^31-1, which the bit rule was refusing
// anyway, so nothing is lost there either. The two rows below that sit inside the range near
// each edge and set only the symbolic bit — one near the upper edge, one near the lower — show
// the bit rule keeps working there undisturbed by how close the value is to a boundary the range
// check patrols instead.
//
// The range check still catches a shape the bit rule alone cannot: a value outside [-2^31, 2^31)
// that sets exactly one of the two bits, which the bit rule alone would call regular. "2^32+4,
// outside a 32-bit value either way" and "a large negative integer wrapping to 4 as uint32" below
// are both that shape, each setting only the symbolic bit, and deleting the range check hands
// either one to a route. render/native's TestFlagsOutsideTheRangeWithOneBitIsRefusedNotRouted
// (fontdict_test.go) measures the first of the two against pdfium, on a CFF fixture that swaps its
// by-name and built-in encodings: with the range check deleted (checked in a scratch copy, never
// in this tree), this package draws the diamond the bit rule alone would route it to, and pdfium
// draws the square — so the range check is load-bearing for that row, not refusal-only.
//
// R#0 also pins why the range stops at 2^31-1 rather than 2^32-1: "2^31+4, a '+'-signed literal
// pdfium reads as flags 0 (R#0)" below is the value objects.Store hands this package for both
// "+2147483652" and the unsigned "2147483652" — pdfcpu's strconv.Atoi drops the sign either way —
// but pdfium reads the two literals apart, at flags 0 and at their full uint32 value respectively
// (measured by render/native's TestFlagsSignedLiteralAboveIntMaxIsRefusedEitherWay). A range
// reaching 2^32-1 would call the value regular and read a symbolic bit off it that the '+'-signed
// literal never forms in pdfium, so both literals are refused here, the unsigned one more
// strictly than pdfium alone would need.
//
// R#2 pins /Flags null and a dangling reference as irregular: ISO 32000-2 makes both the same as
// an absent key (§7.3.9, §7.3.10), but pdfium's GetIntegerFor default applies only to a key that
// is missing outright, so this is an ISO-and-pdfium disagreement rather than agreement on absence.
func TestIrregularFlags(t *testing.T) {
	s := newStore()
	for _, c := range []struct {
		name string
		val  objects.Object
		want string // "" means regular; otherwise a substring Irregular() must contain
	}{
		{"a small integer", objects.Int(4), ""},
		{"2^31-33, inside the range near its upper edge, symbolic bit only", objects.Int(2147483615), ""},
		{"exactly the nonsymbolic bit", objects.Int(32), ""},
		{"both bits set", objects.Int(36), "both the symbolic and nonsymbolic bits set"},
		{"neither bit set", objects.Int(0), "neither the symbolic nor the nonsymbolic bit set"},
		{"2^31+4, a '+'-signed literal pdfium reads as flags 0 (R#0)", objects.Int(2147483652), "outside the range"},
		{"2^32+4, outside a 32-bit value either way", objects.Int(4294967300), "outside the range"},
		{"a large negative integer wrapping to 4 as uint32", objects.Int(-4294967292), "outside the range"},
		{"2^33+4", objects.Int(8589934596), "outside the range"},
		{"a real", objects.Real(4.0), "not an integer"},
		{"not a number", objects.Name("X"), "not an integer"},
		// The exact edges: -2^31 clears both bits (it is 32 zero bits but the sign bit), and
		// 2^31-1 sets every one of its bits below the sign bit, so R#0's bit rule catches each —
		// not the range check, which only fires strictly outside [min, max]. See the doc above
		// for why that still pins the range boundary exactly.
		{"the lower bound, -2^31", objects.Int(-2147483648), "neither the symbolic nor the nonsymbolic bit set"},
		{"one below the lower bound", objects.Int(-2147483649), "outside the range"},
		{"-2^31+4 as int32, inside the range near its lower edge, symbolic bit only", objects.Int(-2147483644), ""},
		{"the upper bound, 2^31-1 (INT_MAX)", objects.Int(2147483647), "both the symbolic and nonsymbolic bits set"},
		{"one above the upper bound", objects.Int(2147483648), "outside the range"},
		{"null", objects.Null{}, "null, or a reference to nothing"},
		{"a dangling reference", objects.Ref{Num: 999}, "null, or a reference to nothing"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := Load(s, objects.Dict{"Subtype": objects.Name("Type1"), "BaseFont": objects.Name("X"),
				"FontDescriptor": objects.Dict{"Flags": c.val}})
			got := f.Irregular()
			if c.want == "" {
				if got != "" {
					t.Errorf("Irregular() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("Irregular() = %q, want it to contain %q", got, c.want)
			}
		})
	}
	// R#0: the key absent from the descriptor entirely, distinct from present-and-null above.
	t.Run("Flags absent from the descriptor", func(t *testing.T) {
		f := Load(s, objects.Dict{"Subtype": objects.Name("Type1"), "BaseFont": objects.Name("X"),
			"FontDescriptor": objects.Dict{}})
		if got := f.Irregular(); !strings.Contains(got, "absent from the font descriptor") {
			t.Errorf("Irregular() = %q, want it to contain %q", got, "absent from the font descriptor")
		}
	})
}

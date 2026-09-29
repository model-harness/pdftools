package function

import (
	"math"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/objects"
)

// memStore is a synthetic Store, its shape copied from image/read_test.go's memStore: the
// questions this package's Parse has to answer -- a self-referencing /Functions entry, a shared
// reference parsed past the total budget, a stream standing in for a function dictionary -- are
// about specific object shapes that a real PDF cannot be edited to isolate.
type memStore struct {
	objs map[objects.Ref]objects.Object
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

func (m *memStore) Trailer() (objects.Dict, error)  { return objects.Dict{}, nil }
func (m *memStore) Catalog() (objects.Dict, error)  { return objects.Dict{}, nil }
func (m *memStore) PageCount() int                  { return 0 }
func (m *memStore) Page(int) (objects.Dict, error)  { return nil, objects.ErrNotFound }
func (m *memStore) PageContent(int) ([]byte, error) { return nil, nil }
func (m *memStore) Decode(s *objects.Stream) error  { s.Decoded = s.Raw; return nil }
func (m *memStore) Version() string                 { return "2.0" }
func (m *memStore) Encrypted() bool                 { return false }
func (m *memStore) Close() error                    { return nil }

func newStore() *memStore { return &memStore{objs: map[objects.Ref]objects.Object{}} }

func arr(vals ...float64) objects.Array {
	a := make(objects.Array, len(vals))
	for i, v := range vals {
		a[i] = objects.Real(v)
	}
	return a
}

// type2 builds a type 2 (exponential interpolation) function dictionary. A nil c0/c1 omits the
// key entirely, so a test can exercise the default.
func type2(domain [2]float64, n float64, c0, c1 []float64) objects.Dict {
	d := objects.Dict{
		"FunctionType": objects.Int(2),
		"Domain":       arr(domain[0], domain[1]),
		"N":            objects.Real(n),
	}
	if c0 != nil {
		d["C0"] = arr(c0...)
	}
	if c1 != nil {
		d["C1"] = arr(c1...)
	}
	return d
}

// type3 builds a type 3 (stitching) function dictionary. funcs holds the raw sub-function
// objects (a Dict, a *Stream, or a Ref to either), unwrapped so a test can pass a Ref.
func type3(domain [2]float64, funcs []objects.Object, bounds, encode []float64) objects.Dict {
	fa := make(objects.Array, len(funcs))
	copy(fa, funcs)
	return objects.Dict{
		"FunctionType": objects.Int(3),
		"Domain":       arr(domain[0], domain[1]),
		"Functions":    fa,
		"Bounds":       arr(bounds...),
		"Encode":       arr(encode...),
	}
}

func mustParse(t *testing.T, s objects.Store, o objects.Object) *Func {
	t.Helper()
	f, err := Parse(s, o)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	return f
}

func call1(f *Func, x float32) float32 {
	out := make([]float32, f.Outputs())
	f.Call(x, out)
	return out[0]
}

func closeEnough(got, want float32) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= 1e-6
}

// --- refusals ---

func TestParseRefusals(t *testing.T) {
	oobDomain := type2([2]float64{0, 1e40}, 1, nil, nil)
	nanDomain := objects.Dict{
		"FunctionType": objects.Int(2),
		"Domain":       objects.Array{objects.Real(math.NaN()), objects.Real(1)},
		"N":            objects.Real(1),
	}
	nonNumDomain := objects.Dict{
		"FunctionType": objects.Int(2),
		"Domain":       objects.Array{objects.Name("x"), objects.Real(1)},
		"N":            objects.Real(1),
	}
	domain3 := objects.Dict{
		"FunctionType": objects.Int(2),
		"Domain":       arr(0, 1, 2),
		"N":            objects.Real(1),
	}
	domainReversed := type2([2]float64{1, 0}, 1, nil, nil)

	rangeWrongLen := type2([2]float64{0, 1}, 1, nil, nil)
	rangeWrongLen["Range"] = arr(0, 1, 2)
	rangeReversed := type2([2]float64{0, 1}, 1, nil, nil)
	rangeReversed["Range"] = arr(1, 0)

	c0Empty := type2([2]float64{0, 1}, 1, []float64{}, nil)
	c1Empty := type2([2]float64{0, 1}, 1, nil, []float64{})
	c0c1Mismatch := type2([2]float64{0, 1}, 1, []float64{0, 0}, []float64{1})

	nFracNegDomain := type2([2]float64{-1, 1}, 0.5, nil, nil)
	nNegZeroInDomain := type2([2]float64{-1, 1}, -1, nil, nil)
	nOverflow := type2([2]float64{0, 10}, 1000, nil, nil)

	missingFunctions := objects.Dict{
		"FunctionType": objects.Int(3),
		"Domain":       arr(0, 1),
		"Bounds":       arr(),
		"Encode":       arr(0, 1),
	}
	emptyFunctions := type3([2]float64{0, 1}, []objects.Object{}, nil, nil)
	leaf := type2([2]float64{0, 1}, 1, nil, nil)
	outputsMismatch := type3([2]float64{0, 1},
		[]objects.Object{leaf, type2([2]float64{0, 1}, 1, []float64{0, 0}, []float64{1, 1})},
		[]float64{0.5}, []float64{0, 1, 0, 1})
	boundsWrongLen := type3([2]float64{0, 1}, []objects.Object{leaf, leaf}, []float64{0.3, 0.6}, []float64{0, 1, 0, 1})
	encodeWrongLen := type3([2]float64{0, 1}, []objects.Object{leaf, leaf}, []float64{0.5}, []float64{0, 1})
	encodeLong := type3([2]float64{0, 1}, []objects.Object{leaf, leaf}, []float64{0.5}, []float64{0, 1, 0, 1, 0, 1})

	cases := []struct {
		name string
		obj  objects.Object
		want string
	}{
		{"not a dict or stream", objects.Int(5), "dictionary or stream"},
		{"missing function type", objects.Dict{"Domain": arr(0, 1)}, "FunctionType"},
		{"type 0 sampled", objects.Dict{"FunctionType": objects.Int(0), "Domain": arr(0, 1)}, "type 0 (sampled)"},
		{"type 4 postscript", objects.Dict{"FunctionType": objects.Int(4), "Domain": arr(0, 1)}, "type 4 (PostScript calculator)"},
		{"unknown function type", objects.Dict{"FunctionType": objects.Int(9), "Domain": arr(0, 1)}, "FunctionType"},
		{"missing domain", objects.Dict{"FunctionType": objects.Int(2), "N": objects.Real(1)}, "Domain"},
		{"domain wrong length", domain3, "Domain"},
		{"domain reversed", domainReversed, "Domain"},
		{"domain non-number", nonNumDomain, "number"},
		{"domain NaN", nanDomain, "NaN"},
		{"domain past float32 range", oobDomain, "1e+40 is past the float range"},
		{"range wrong length", rangeWrongLen, "Range"},
		{"range reversed", rangeReversed, "Range"},
		{"c0 empty", c0Empty, "/C0 is empty"},
		{"c1 empty", c1Empty, "/C1 is empty"},
		{"c0/c1 length mismatch", c0c1Mismatch, "/C0"},
		{"missing N", objects.Dict{"FunctionType": objects.Int(2), "Domain": arr(0, 1)}, "/N"},
		{"N fractional, negative domain", nFracNegDomain, "not an integer"},
		{"N negative, 0 in domain", nNegZeroInDomain, "negative"},
		{"N overflow at endpoint", nOverflow, "10**1000 is past the float range"},
		{"type3 missing functions", missingFunctions, "Functions"},
		{"type3 empty functions", emptyFunctions, "Functions"},
		{"type3 outputs mismatch", outputsMismatch, "output"},
		{"type3 bounds wrong length", boundsWrongLen, "Bounds"},
		{"type3 encode wrong length", encodeWrongLen, "Encode"},
		{"type3 encode too long", encodeLong, "/Encode has 6 entries, want 4"},
	}
	s := newStore()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(s, c.obj)
			if err == nil {
				t.Fatalf("Parse: got nil error, want one containing %q", c.want)
			}
			if !strings.HasPrefix(err.Error(), "function: ") {
				t.Errorf("error %q does not start with %q", err.Error(), "function: ")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want a substring %q", err.Error(), c.want)
			}
		})
	}
}

func TestParseCycleRefused(t *testing.T) {
	s := newStore()
	ref := objects.Ref{Num: 1}
	s.objs[ref] = type3([2]float64{0, 1}, []objects.Object{ref}, nil, []float64{0, 1})
	_, err := Parse(s, ref)
	if err == nil || !strings.Contains(err.Error(), "nests more than") {
		t.Fatalf("Parse: got %v, want an error about nesting depth", err)
	}
}

func TestParseSharedRefsBeyondBudgetRefused(t *testing.T) {
	s := newStore()
	leafRef := objects.Ref{Num: 1}
	s.objs[leafRef] = type2([2]float64{0, 1}, 1, nil, nil)

	// One type 3 function whose /Functions array names the same shared leaf 300 times: a DAG,
	// not a tree, and reading each edge once puts the total over maxTotal.
	const wide = 300
	funcs := make([]objects.Object, wide)
	bounds := make([]float64, wide-1)
	encode := make([]float64, 2*wide)
	for i := range funcs {
		funcs[i] = leafRef
		if i < wide-1 {
			bounds[i] = float64(i+1) / float64(wide)
		}
		encode[2*i], encode[2*i+1] = 0, 1
	}
	rootRef := objects.Ref{Num: 2}
	s.objs[rootRef] = type3([2]float64{0, 1}, funcs, bounds, encode)

	_, err := Parse(s, rootRef)
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("Parse: got %v, want an error about the total function budget", err)
	}
}

// chainedType3 nests depth type3 functions, each stitching a single leaf placeholder into
// itself, to exercise the depth limit precisely.
func chainedType3(s *memStore, depth int) objects.Object {
	var cur objects.Object = type2([2]float64{0, 1}, 1, nil, nil)
	for i := 0; i < depth-1; i++ {
		ref := objects.Ref{Num: 100 + i}
		s.objs[ref] = cur
		cur = type3([2]float64{0, 1}, []objects.Object{ref}, nil, []float64{0, 1})
	}
	return cur
}

func TestParseDepthLimit(t *testing.T) {
	s := newStore()
	if _, err := Parse(s, chainedType3(s, maxDepth)); err != nil {
		t.Fatalf("Parse at depth %d: unexpected error: %v", maxDepth, err)
	}

	s2 := newStore()
	_, err := Parse(s2, chainedType3(s2, maxDepth+1))
	if err == nil || !strings.Contains(err.Error(), "nests more than") {
		t.Fatalf("Parse at depth %d: got %v, want an error about nesting depth", maxDepth+1, err)
	}
}

// --- type 2 evaluation ---

func TestType2Exponents(t *testing.T) {
	cases := []struct {
		name string
		n    float64
		x    float32
		want float32
	}{
		{"N=1 linear", 1, 0.5, 0.5},
		{"N=2 square", 2, 0.5, 0.25},
		{"N=0.5 sqrt", 0.5, 0.25, 0.5},
		{"N=0, pow(0,0)=1", 0, 0, 1},
	}
	s := newStore()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := mustParse(t, s, type2([2]float64{0, 1}, c.n, []float64{0}, []float64{1}))
			got := call1(f, c.x)
			if !closeEnough(got, c.want) {
				t.Errorf("Call(%v) = %v, want %v", c.x, got, c.want)
			}
		})
	}
}

func TestType2Defaults(t *testing.T) {
	s := newStore()
	f := mustParse(t, s, type2([2]float64{0, 1}, 1, nil, nil))
	if f.Outputs() != 1 {
		t.Fatalf("Outputs() = %d, want 1", f.Outputs())
	}
	if got := call1(f, 0.3); !closeEnough(got, 0.3) {
		t.Errorf("Call(0.3) = %v, want 0.3 (C0=[0], C1=[1] default)", got)
	}
}

func TestType2MultiOutput(t *testing.T) {
	s := newStore()
	f := mustParse(t, s, type2([2]float64{0, 1}, 1, []float64{0, 10}, []float64{1, 20}))
	if f.Outputs() != 2 {
		t.Fatalf("Outputs() = %d, want 2", f.Outputs())
	}
	out := make([]float32, 2)
	f.Call(0.5, out)
	if !closeEnough(out[0], 0.5) || !closeEnough(out[1], 15) {
		t.Errorf("Call(0.5) = %v, want [0.5 15]", out)
	}
}

func TestType2RangeClamps(t *testing.T) {
	s := newStore()
	d := type2([2]float64{0, 1}, 1, []float64{0}, []float64{1})
	d["Range"] = arr(0.2, 0.8)
	f := mustParse(t, s, d)

	if got := call1(f, 0); !closeEnough(got, 0.2) {
		t.Errorf("Call(0) = %v, want 0.2 (clamped to Range lo)", got)
	}
	if got := call1(f, 1); !closeEnough(got, 0.8) {
		t.Errorf("Call(1) = %v, want 0.8 (clamped to Range hi)", got)
	}
}

func TestCallClampsToDomain(t *testing.T) {
	s := newStore()
	f := mustParse(t, s, type2([2]float64{0.2, 0.8}, 1, []float64{0}, []float64{1}))

	if got := call1(f, -5); !closeEnough(got, call1(f, 0.2)) {
		t.Errorf("Call(-5) = %v, want the same as Call(Domain lo)", got)
	}
	if got := call1(f, 5); !closeEnough(got, call1(f, 0.8)) {
		t.Errorf("Call(5) = %v, want the same as Call(Domain hi)", got)
	}
}

func TestParseStreamAndRef(t *testing.T) {
	dict := type2([2]float64{0, 1}, 1, []float64{0}, []float64{1})

	t.Run("stream", func(t *testing.T) {
		s := newStore()
		st := &objects.Stream{Dict: dict}
		f := mustParse(t, s, st)
		if got := call1(f, 0.5); !closeEnough(got, 0.5) {
			t.Errorf("Call(0.5) = %v, want 0.5", got)
		}
	})

	t.Run("reference", func(t *testing.T) {
		s := newStore()
		ref := objects.Ref{Num: 7}
		s.objs[ref] = dict
		f := mustParse(t, s, ref)
		if got := call1(f, 0.5); !closeEnough(got, 0.5) {
			t.Errorf("Call(0.5) = %v, want 0.5", got)
		}
	})
}

// --- type 3 evaluation ---

// constFn is a type 2 function whose C0 and C1 are equal, so it returns a constant regardless of
// x -- a marker for which sub-function a stitch chose.
func constFn(v float64) objects.Dict {
	return type2([2]float64{0, 1}, 1, []float64{v}, []float64{v})
}

func TestType3ChoosesSubFunctionByBounds(t *testing.T) {
	s := newStore()
	f := mustParse(t, s, type3([2]float64{0, 1},
		[]objects.Object{constFn(10), constFn(20)},
		[]float64{0.5}, []float64{0, 1, 0, 1}))

	cases := []struct {
		x    float32
		want float32
	}{
		{0.4, 10}, // x < 0.5: first sub-function
		{0.5, 20}, // x == 0.5: second (pdfium's rule is strict "<", not "<=")
		{0.6, 20}, // x > 0.5: second
	}
	for _, c := range cases {
		if got := call1(f, c.x); !closeEnough(got, c.want) {
			t.Errorf("Call(%v) = %v, want %v", c.x, got, c.want)
		}
	}
}

func TestType3ReversedEncode(t *testing.T) {
	// A single sub-function that returns its input verbatim, so Call(x) equals whatever x' the
	// stitch's Encode maps x to. Encode [1 0] reverses the sub-domain.
	s := newStore()
	identity := type2([2]float64{0, 1}, 1, []float64{0}, []float64{1})
	f := mustParse(t, s, type3([2]float64{0, 1}, []objects.Object{identity}, nil, []float64{1, 0}))

	if got := call1(f, 0.3); !closeEnough(got, 0.7) {
		t.Errorf("Call(0.3) = %v, want 0.7 (Encode [1 0] reverses x)", got)
	}
}

func TestType3ZeroWidthSubDomainReturnsYMin(t *testing.T) {
	// A single sub-function stitched over a zero-width Domain (lo == hi, which Parse allows):
	// bounds[0] == bounds[1] == the Domain's one point, so interpolate's divisor is 0 and it
	// must return ymin (Encode's first value) regardless of ymax.
	s := newStore()
	identity := type2([2]float64{0, 1}, 1, []float64{0}, []float64{1})
	f := mustParse(t, s, type3([2]float64{0.5, 0.5}, []objects.Object{identity}, nil, []float64{0.25, 0.75}))

	if got := call1(f, 0.5); !closeEnough(got, 0.25) {
		t.Errorf("Call(0.5) = %v, want 0.25 (ymin, zero-width sub-domain)", got)
	}
}

// TestType3MapsEachSubDomainThroughItsEncode pins the arithmetic the selection tests cannot: both
// sub-domains are half the domain wide, so interpolate divides by 0.5 and not 1, and the second
// Encode pair is not the first's, so reading the wrong pair moves the answer.
func TestType3MapsEachSubDomainThroughItsEncode(t *testing.T) {
	s := newStore()
	identity := type2([2]float64{0, 1}, 1, []float64{0}, []float64{1})
	f := mustParse(t, s, type3([2]float64{0, 1}, []objects.Object{identity, identity},
		[]float64{0.5}, []float64{0, 1, 0.2, 0.6}))
	for _, c := range []struct{ x, want float32 }{
		{0.25, 0.5}, // [0 0.5] onto [0 1]
		{0.75, 0.4}, // [0.5 1] onto [0.2 0.6]
	} {
		if got := call1(f, c.x); !closeEnough(got, c.want) {
			t.Errorf("Call(%v) = %v, want %v", c.x, got, c.want)
		}
	}
}

// TestType3RangeClamps is TestType2RangeClamps for a stitching function's own /Range, which
// applies after its sub-function's.
func TestType3RangeClamps(t *testing.T) {
	s := newStore()
	identity := type2([2]float64{0, 1}, 1, []float64{0}, []float64{1})
	d := type3([2]float64{0, 1}, []objects.Object{identity}, nil, []float64{0, 1})
	d["Range"] = arr(0, 0.5)
	if got := call1(mustParse(t, s, d), 0.9); !closeEnough(got, 0.5) {
		t.Errorf("Call(0.9) = %v, want 0.5 (clamped to Range hi)", got)
	}
}

// TestADegenerateRangeIsAccepted holds a Range whose bounds are equal, which pins lo > hi as the
// refusal and not lo >= hi.
func TestADegenerateRangeIsAccepted(t *testing.T) {
	s := newStore()
	d := type2([2]float64{0, 1}, 1, []float64{0}, []float64{1})
	d["Range"] = arr(0.5, 0.5)
	if got := call1(mustParse(t, s, d), 0.1); !closeEnough(got, 0.5) {
		t.Errorf("Call(0.1) = %v, want 0.5", got)
	}
}

// TestTheFunctionBudgetIsExact parses a tree of exactly maxTotal functions and refuses one more.
func TestTheFunctionBudgetIsExact(t *testing.T) {
	for _, c := range []struct {
		leaves int
		ok     bool
	}{{maxTotal - 1, true}, {maxTotal, false}} {
		s := newStore()
		leaf := objects.Ref{Num: 1}
		s.objs[leaf] = type2([2]float64{0, 1}, 1, nil, nil)
		funcs := make([]objects.Object, c.leaves)
		bounds := make([]float64, c.leaves-1)
		encode := make([]float64, 2*c.leaves)
		for i := range funcs {
			funcs[i] = leaf
			if i < c.leaves-1 {
				bounds[i] = float64(i+1) / float64(c.leaves)
			}
			encode[2*i+1] = 1
		}
		_, err := Parse(s, type3([2]float64{0, 1}, funcs, bounds, encode))
		if (err == nil) != c.ok {
			t.Errorf("a root and %d leaves: error %v, want ok %v", c.leaves, err, c.ok)
		}
	}
}

// TestType2RefusalsAtTheirBoundaries holds each type 2 refusal at the input that separates it from
// acceptance: a Domain that only touches 0, an overflow at only one endpoint, and an overflow in
// the output rather than in the power.
func TestType2RefusalsAtTheirBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		d    objects.Dict
		want string
	}{
		{"N negative, Domain starting at 0", type2([2]float64{0, 1}, -1, nil, nil), "negative"},
		{"N negative, Domain ending at 0", type2([2]float64{-1, 0}, -1, nil, nil), "negative"},
		{"overflow at the low endpoint only", type2([2]float64{-10, 1}, 1000, nil, nil), "-10**1000 is past the float range"},
		{"overflow in the output", type2([2]float64{0, 2}, 1, []float64{0}, []float64{3e38}), "float range"},
		{"C1 of three and no C0", type2([2]float64{0, 1}, 1, nil, []float64{1, 0, 0}), "no /C0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(newStore(), c.d)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse: error %v, want one containing %q", err, c.want)
			}
		})
	}
	// A C1 of one entry and no C0 is §7.10.3's default C0 of [0], and pdfium's reading too.
	f := mustParse(t, newStore(), type2([2]float64{0, 1}, 1, nil, []float64{0.5}))
	if got := call1(f, 1); f.Outputs() != 1 || !closeEnough(got, 0.5) {
		t.Errorf("C1 [0.5] alone: %d outputs, Call(1) = %v; want 1 and 0.5", f.Outputs(), got)
	}
}

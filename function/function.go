// Package function evaluates the PDF functions (ISO 32000-2 §7.10) that an axial shading reads,
// exactly as pdfium does: cpdf_function.cpp for parsing and dispatch, cpdf_expintfunc.cpp for
// type 2 (exponential interpolation), cpdf_stitchfunc.cpp for type 3 (stitching). Every value
// pdfium holds is a C++ float, so this package computes in float32 throughout rather than Go's
// more precise float64 -- matching the wrong precision is what makes the output equal, and a
// shading samples its function 256 times per axis, where float64 would drift from what pdfium
// drew.
//
// Only a one-input function of type 2 or 3 is read. A shading's function always takes the single
// interpolation parameter t as its input (§8.7.4.5.3), so a function declared with any other
// /Domain length is refused rather than guessed at. Type 0 (sampled, a lookup table read from a
// stream) and type 4 (PostScript calculator, a tiny interpreted language) are real parts of the
// specification but neither has shown up on a shading function in this corpus; adding either
// without a measured case to check it against would be unverifiable, so both are refused by name
// instead of approximated.
//
// A type 3 stitching function nests through /Functions, and nothing stops a producer from
// writing a cycle (a function whose own /Functions eventually names itself again) or a DAG deep
// enough that unfolding it costs far more than the file justifies, since a shared reference
// parsed once per edge rather than once per node is exponential in the graph's size. Parse bounds
// both: a recursion depth limit, which also happens to be what stops a cycle, since a cycle
// recurses forever if nothing else does; and a total count of functions parsed by one top-level
// call, which bounds the DAG case directly.
//
// The caller (render/native) turns any error this package returns into a page refusal, so every
// message names what was wrong and starts "function: ".
package function

import (
	"errors"
	"fmt"
	"math"

	"github.com/model-harness/pdftools/objects"
)

const (
	// maxDepth bounds how many levels of /Functions nesting Parse will follow. A type 3
	// function whose /Functions entry resolves back to an ancestor -- a cycle -- recurses
	// without this, so the same constant that keeps a legitimate deep tree cheap is what
	// keeps a cyclic one from running forever.
	maxDepth = 8

	// maxTotal bounds how many functions one top-level Parse call will read in total, across
	// every level of nesting. A shared reference makes the object graph a DAG rather than a
	// tree, and reading it once per edge instead of once per node is exponential in its size;
	// this caps the work a single Parse can be made to do rather than trying to detect sharing.
	maxTotal = 256
)

// Func is a parsed one-input function: type 2 (exponential interpolation) or type 3
// (stitching). Every number it holds is a float32, matching the precision pdfium evaluates in.
type Func struct {
	domain [2]float32
	rng    [][2]float32 // nil if /Range was absent; else len(rng) == outputs.

	kind    int // 2 or 3.
	outputs int

	// type 2.
	n      float32
	c0, c1 []float32

	// type 3.
	funcs  []*Func
	bounds []float32 // len(funcs)+1: domain lo, then /Bounds, then domain hi.
	encode []float32 // len(funcs)*2.
}

// parseCtx carries the state that must survive across the recursive calls one Parse makes, since
// the depth and total limits apply to the whole tree rooted at that call, not to any one node.
type parseCtx struct {
	depth int
	total int
}

// Parse reads the function o (a dictionary, a stream, or a reference to either).
func Parse(s objects.Store, o objects.Object) (*Func, error) {
	return parse(s, o, &parseCtx{})
}

func parse(s objects.Store, o objects.Object, ctx *parseCtx) (*Func, error) {
	ctx.depth++
	defer func() { ctx.depth-- }()
	if ctx.depth > maxDepth {
		return nil, fmt.Errorf("function: /Functions nests more than %d levels deep (this is also what stops a function that names itself)", maxDepth)
	}
	ctx.total++
	if ctx.total > maxTotal {
		return nil, fmt.Errorf("function: more than %d functions were parsed from one function tree", maxTotal)
	}

	ro, err := s.Resolve(o)
	if err != nil {
		return nil, fmt.Errorf("function: %w", err)
	}
	var dict objects.Dict
	switch t := ro.(type) {
	case objects.Dict:
		dict = t
	case *objects.Stream:
		dict = t.Dict
	default:
		return nil, fmt.Errorf("function: expected a dictionary or stream, got %T", ro)
	}

	ft, ok := objects.GetInt(s, dict, "FunctionType")
	if !ok {
		return nil, errors.New("function: missing or non-numeric /FunctionType")
	}
	switch ft {
	case 2, 3:
	case 0:
		return nil, errors.New("function: type 0 (sampled) functions are not implemented")
	case 4:
		return nil, errors.New("function: type 4 (PostScript calculator) functions are not implemented")
	default:
		return nil, fmt.Errorf("function: /FunctionType %d is not 0, 2, 3 or 4", ft)
	}

	domArr, err := requiredArray(s, dict, "Domain")
	if err != nil {
		return nil, err
	}
	if len(domArr) != 2 {
		return nil, fmt.Errorf("function: /Domain has %d entries, but only a one-input function (exactly 2) is read here", len(domArr))
	}
	domLo, err := readNum(s, domArr[0])
	if err != nil {
		return nil, err
	}
	domHi, err := readNum(s, domArr[1])
	if err != nil {
		return nil, err
	}
	if domLo > domHi {
		return nil, fmt.Errorf("function: /Domain [%v %v] has its lower bound above its upper bound", domLo, domHi)
	}

	f := &Func{domain: [2]float32{domLo, domHi}, kind: int(ft)}

	switch ft {
	case 2:
		if err := parseType2(s, dict, f); err != nil {
			return nil, err
		}
	case 3:
		if err := parseType3(s, dict, f, ctx); err != nil {
			return nil, err
		}
	}

	rngArr, present, err := optionalArray(s, dict, "Range")
	if err != nil {
		return nil, err
	}
	if present {
		if len(rngArr) != 2*f.outputs {
			return nil, fmt.Errorf("function: /Range has %d entries, want %d for %d output(s)", len(rngArr), 2*f.outputs, f.outputs)
		}
		rng := make([][2]float32, f.outputs)
		for i := 0; i < f.outputs; i++ {
			lo, err := readNum(s, rngArr[2*i])
			if err != nil {
				return nil, err
			}
			hi, err := readNum(s, rngArr[2*i+1])
			if err != nil {
				return nil, err
			}
			if lo > hi {
				return nil, fmt.Errorf("function: /Range pair %d [%v %v] has its lower bound above its upper bound", i, lo, hi)
			}
			rng[i] = [2]float32{lo, hi}
		}
		f.rng = rng
	}

	return f, nil
}

// parseType2 reads the type 2 (exponential interpolation, cpdf_expintfunc.cpp) keys and fills in
// f's type-2 fields and Outputs. f.domain is already set.
func parseType2(s objects.Store, dict objects.Dict, f *Func) error {
	nVal, ok := objects.GetNum(s, dict, "N")
	if !ok {
		return errors.New("function: type 2 function is missing /N, or it is not a number")
	}
	n, err := asFloat32(nVal)
	if err != nil {
		return err
	}

	c0Arr, hasC0, err := optionalArray(s, dict, "C0")
	if err != nil {
		return err
	}
	c1Arr, hasC1, err := optionalArray(s, dict, "C1")
	if err != nil {
		return err
	}
	if hasC0 && len(c0Arr) == 0 {
		return errors.New("function: /C0 is empty")
	}
	if hasC1 && len(c1Arr) == 0 {
		return errors.New("function: /C1 is empty")
	}
	if hasC0 && hasC1 && len(c0Arr) != len(c1Arr) {
		return fmt.Errorf("function: /C0 has %d entries but /C1 has %d; a type 2 function's two ends must have the same number of outputs", len(c0Arr), len(c1Arr))
	}

	// pdfium counts outputs from /C0 alone, and without it reads one output whatever /C1's length:
	// a /C1 of three with no /C0 is a one-output function there, which a shading in three
	// components then draws nothing for. Refused, not read as three.
	if !hasC0 && hasC1 && len(c1Arr) != 1 {
		return fmt.Errorf("function: /C1 has %d entries and there is no /C0, which pdfium reads as one output", len(c1Arr))
	}
	outputs := 1
	if hasC0 {
		outputs = len(c0Arr)
	}

	c0 := make([]float32, outputs)
	c1 := make([]float32, outputs)
	for i := range c1 {
		c1[i] = 1 // /C1's default, per §7.10.3.
	}
	if hasC0 {
		for i, o := range c0Arr {
			if c0[i], err = readNum(s, o); err != nil {
				return err
			}
		}
	}
	if hasC1 {
		for i, o := range c1Arr {
			if c1[i], err = readNum(s, o); err != nil {
				return err
			}
		}
	}

	// pdfium computes powf(x, N) and c0+p*(c1-c0) with no domain check of its own, so any input
	// that makes either NaN or infinite has to be refused here instead. Two cases are worth
	// naming because they are the common ones a shading actually hits:
	//   - a fractional N raised to a negative base has no real result (x**0.5 for x < 0).
	//   - a negative N applied to x == 0 divides by zero (x**-1 for x == 0).
	domLo, domHi := f.domain[0], f.domain[1]
	isInt := n == float32(math.Trunc(float64(n)))
	if domLo < 0 && !isInt {
		return fmt.Errorf("function: /N %v is not an integer, and /Domain's lower bound %v is negative: a fractional power of a negative base is not a real number", n, domLo)
	}
	if n < 0 && domLo <= 0 && domHi >= 0 {
		return fmt.Errorf("function: /N %v is negative and /Domain [%v %v] includes 0: 0 to a negative power is not finite", n, domLo, domHi)
	}

	// For x >= 0, x**N is monotonic in x, and for x < 0 with integer N, |x**N| is largest at
	// whichever endpoint is furthest from zero -- so checking both Domain endpoints is enough
	// to catch any overflow the rest of Domain could produce, without evaluating every x.
	pLo := powf(domLo, n)
	pHi := powf(domHi, n)
	for _, e := range [2][2]float32{{domLo, pLo}, {domHi, pHi}} {
		if !isFinite32(e[1]) {
			return fmt.Errorf("function: %v**%v is past the float range pdfium holds it in", e[0], n)
		}
	}
	for i := range c0 {
		yLo := c0[i] + float32(pLo*(c1[i]-c0[i]))
		yHi := c0[i] + float32(pHi*(c1[i]-c0[i]))
		if !isFinite32(yLo) || !isFinite32(yHi) {
			return fmt.Errorf("function: output %d is past the float range pdfium holds it in at a Domain endpoint", i)
		}
	}

	f.n, f.c0, f.c1, f.outputs = n, c0, c1, outputs
	return nil
}

// parseType3 reads the type 3 (stitching, cpdf_stitchfunc.cpp) keys and fills in f's type-3
// fields and Outputs. f.domain is already set.
func parseType3(s objects.Store, dict objects.Dict, f *Func, ctx *parseCtx) error {
	fnArr, err := requiredArray(s, dict, "Functions")
	if err != nil {
		return err
	}
	if len(fnArr) == 0 {
		return errors.New("function: /Functions is empty")
	}
	k := len(fnArr)

	subs := make([]*Func, k)
	for i, sub := range fnArr {
		sf, err := parse(s, sub, ctx)
		if err != nil {
			return fmt.Errorf("function: /Functions[%d]: %w", i, err)
		}
		subs[i] = sf
	}
	outputs := subs[0].outputs
	for i := 1; i < k; i++ {
		if subs[i].outputs != outputs {
			return fmt.Errorf("function: /Functions[%d] has %d output(s), want %d to match /Functions[0]", i, subs[i].outputs, outputs)
		}
	}

	boundsArr, err := requiredArray(s, dict, "Bounds")
	if err != nil {
		return err
	}
	if len(boundsArr) != k-1 {
		return fmt.Errorf("function: /Bounds has %d entries, want %d for %d sub-function(s)", len(boundsArr), k-1, k)
	}
	// pdfium (CPDF_StitchFunc::v_Init) reads exactly k-1 bounds and 2k encode values and does
	// not require either array to be sorted or otherwise consistent; a longer array there is
	// read as a prefix. This package refuses any length but the one the spec names instead of
	// silently reading a prefix, since a caller passing the wrong array almost certainly wrote
	// the file wrong rather than deliberately padded it.
	encArr, err := requiredArray(s, dict, "Encode")
	if err != nil {
		return err
	}
	if len(encArr) != 2*k {
		return fmt.Errorf("function: /Encode has %d entries, want %d for %d sub-function(s)", len(encArr), 2*k, k)
	}

	bounds := make([]float32, k+1)
	bounds[0] = f.domain[0]
	bounds[k] = f.domain[1]
	for i, o := range boundsArr {
		v, err := readNum(s, o)
		if err != nil {
			return err
		}
		bounds[i+1] = v
	}
	encode := make([]float32, 2*k)
	for i, o := range encArr {
		v, err := readNum(s, o)
		if err != nil {
			return err
		}
		encode[i] = v
	}

	f.funcs, f.bounds, f.encode, f.outputs = subs, bounds, encode, outputs
	return nil
}

// Outputs is how many values Call writes.
func (f *Func) Outputs() int { return f.outputs }

// Call evaluates f at x and writes Outputs() values to out, which must be at least that long.
func (f *Func) Call(x float32, out []float32) {
	if x < f.domain[0] {
		x = f.domain[0]
	} else if x > f.domain[1] {
		x = f.domain[1]
	}

	switch f.kind {
	case 2:
		// The product is converted before the add so that no target fuses the two into one
		// rounding: pdfium's x86-64 build rounds each, and the Go specification makes an explicit
		// conversion the thing that forbids fusion.
		p := powf(x, f.n)
		for i := 0; i < f.outputs; i++ {
			out[i] = f.c0[i] + float32(p*(f.c1[i]-f.c0[i]))
		}
	case 3:
		// pdfium's rule (CPDF_StitchFunc::Call): the first sub-function whose upper bound x
		// falls short of, else the last one. Bounds need not be increasing for this to be
		// well defined, so it is not validated here -- following pdfium exactly, not a
		// tidier version of it, is the point.
		k := len(f.funcs)
		i := k - 1
		for j := 0; j < k-1; j++ {
			if x < f.bounds[j+1] {
				i = j
				break
			}
		}
		xp := interpolate(x, f.bounds[i], f.bounds[i+1], f.encode[2*i], f.encode[2*i+1])
		f.funcs[i].Call(xp, out)
	}

	if f.rng != nil {
		for i := 0; i < f.outputs; i++ {
			if out[i] < f.rng[i][0] {
				out[i] = f.rng[i][0]
			} else if out[i] > f.rng[i][1] {
				out[i] = f.rng[i][1]
			}
		}
	}
}

// interpolate is pdfium's Interpolate (fpdf_render_loadimage / cpdf_function.cpp): linear from
// [xmin,xmax] to [ymin,ymax], except that a zero-width input range returns ymin rather than
// dividing by zero -- pdfium's own guard, not one added here.
func interpolate(x, xmin, xmax, ymin, ymax float32) float32 {
	d := xmax - xmin
	if d == 0 {
		return ymin
	}
	return ymin + (x-xmin)*(ymax-ymin)/d
}

// powf is pdfium's use of powf: float32 in, float32 out, computed at float64 precision in
// between because Go has no float32 math.Pow.
func powf(x, n float32) float32 {
	return float32(math.Pow(float64(x), float64(n)))
}

func isFinite32(f float32) bool {
	return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0)
}

// asFloat32 converts a resolved PDF number to float32, refusing anything that is not finite
// before or after the conversion: a NaN or infinite float64, or a finite float64 that overflows
// float32's narrower range (pdfium's float).
func asFloat32(v float64) (float32, error) {
	if math.IsNaN(v) {
		return 0, errors.New("function: a value is NaN, not a number")
	}
	f := float32(v)
	if math.IsInf(float64(f), 0) {
		return 0, fmt.Errorf("function: %v is past the float range pdfium holds it in", v)
	}
	return f, nil
}

// readNum resolves o and reads it as a number, in float32.
func readNum(s objects.Store, o objects.Object) (float32, error) {
	ro, err := s.Resolve(o)
	if err != nil {
		return 0, fmt.Errorf("function: %w", err)
	}
	v, ok := objects.AsNum(ro)
	if !ok {
		return 0, fmt.Errorf("function: expected a number, got %T", ro)
	}
	return asFloat32(v)
}

// requiredArray resolves dict[key] and reads it as an array, distinguishing "absent" from
// "present but the wrong type" in the error, since the two point a caller at different mistakes.
func requiredArray(s objects.Store, dict objects.Dict, key objects.Name) (objects.Array, error) {
	v, ok := objects.Get(s, dict, key)
	if !ok {
		return nil, fmt.Errorf("function: missing /%s", key)
	}
	a, ok := v.(objects.Array)
	if !ok {
		return nil, fmt.Errorf("function: /%s is %T, not an array", key, v)
	}
	return a, nil
}

// optionalArray is requiredArray for a key the caller does not require to be present: the second
// return reports whether it was, and an error is only ever returned for the wrong-type case.
func optionalArray(s objects.Store, dict objects.Dict, key objects.Name) (objects.Array, bool, error) {
	v, ok := objects.Get(s, dict, key)
	if !ok {
		return nil, false, nil
	}
	a, ok := v.(objects.Array)
	if !ok {
		return nil, false, fmt.Errorf("function: /%s is %T, not an array", key, v)
	}
	return a, true, nil
}

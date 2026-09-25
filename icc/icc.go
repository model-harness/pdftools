// Package icc converts through the one family of ICC profile that a PDF actually carries in
// practice: a matrix/TRC profile, gray or RGB, with an XYZ connection space. §8.6.5.5 lets an
// ICCBased colour space fall back to its /Alternate, or to a device space chosen by /N, when the
// profile itself cannot be used — so Parse is deliberately narrow, and every profile it refuses is
// one the caller (render/native) is expected to draw through that fallback instead.
//
// A LUT-based profile (an A2B/D2B tag) is refused rather than approximated, because a CMM that has
// one uses it in preference to the matrix and curves, and drawing through the matrix instead would
// disagree with lcms — which is the CMM pdfium, and therefore the reference this package is measured
// against, actually runs.
//
// wtpt, chad and every tag besides the colorants and curves are read by nothing here. A matrix/TRC
// profile's colorants are already D50-relative XYZ, so no white-point adaptation is needed to reach
// the D50 this package works in. The rendering intent is not read either: pdfium converts every
// colour at the perceptual intent whatever the PDF asks for — /Perceptual, /RelativeColorimetric
// and /AbsoluteColorimetric were measured drawing identically — and lcms compensates the black
// point of every perceptual conversion into its own v4 sRGB profile, so this package compensates
// it too, always.
package icc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"sync"
)

// curveFn is a tone curve, evaluated once per call for Components and SRGB and sampled into a
// lookup table for Image. Every curveFn clamps its own result to [0,1] before returning, which is
// what keeps a hostile profile's fractional power of a negative base, an Inf, or a NaN from ever
// reaching a caller.
type curveFn func(x float64) float64

// Profile is a parsed matrix/TRC ICC profile: gray with one curve, or RGB with three curves and a
// colorant matrix, ready to convert into sRGB.
type Profile struct {
	gray  bool
	srgb  bool // the profile is sRGB itself, to within a level: SRGB and Image return their input.
	trc   [3]curveFn
	m     mat3    // inv(S)*K*M, K being black point compensation; unused for gray, where the matrix step is skipped entirely.
	off   vec3    // inv(S) applied to K's offset: zero unless the profile's black is lighter than black.
	black float64 // gray's black point, the Y that compensation carries to 0; unused for RGB.

	once             sync.Once
	grayLUT          [256]uint8
	linR, linG, linB [256]float64
}

// Parse reads an ICC profile and returns a Profile if it is a pure matrix/TRC gray or RGB profile
// with an XYZ connection space. Every other error message starts "icc: ".
func Parse(b []byte) (*Profile, error) {
	// The header's own size field is not trusted: every bound below is checked against len(b),
	// which is the same cap lcms applies to a profile it did not allocate itself.
	if len(b) < 132 {
		return nil, fmt.Errorf("icc: profile is %d bytes, less than the 132-byte header and tag count", len(b))
	}
	if string(b[36:40]) != "acsp" {
		return nil, errors.New("icc: missing the 'acsp' signature at offset 36")
	}

	class := string(b[12:16])
	switch class {
	case "scnr", "mntr", "prtr", "spac":
	default:
		return nil, fmt.Errorf("icc: profile class %q is a device link, abstract or named-colour profile, not an input profile", class)
	}

	space := string(b[16:20])
	var gray bool
	switch space {
	case "GRAY":
		gray = true
	case "RGB ":
		gray = false
	default:
		return nil, fmt.Errorf("icc: data colour space %q, only GRAY and RGB are supported", space)
	}
	if pcs := string(b[20:24]); pcs != "XYZ " {
		return nil, fmt.Errorf("icc: connection space %q, only XYZ is supported", pcs)
	}

	n := binary.BigEndian.Uint32(b[128:132])
	if 132+12*uint64(n) > uint64(len(b)) {
		return nil, fmt.Errorf("icc: tag count %d runs past the end of a %d-byte profile", n, len(b))
	}
	tags := make(map[string][]byte, n)
	for i := uint32(0); i < n; i++ {
		e := b[132+12*i:]
		sig := string(e[0:4])
		off := binary.BigEndian.Uint32(e[4:8])
		size := binary.BigEndian.Uint32(e[8:12])
		end := uint64(off) + uint64(size)
		if end > uint64(len(b)) {
			return nil, fmt.Errorf("icc: tag %q at offset %d size %d runs past the end of a %d-byte profile", sig, off, size, len(b))
		}
		// The first entry for a signature wins, which is what a CMM resolving the same table does.
		if _, dup := tags[sig]; !dup {
			tags[sig] = b[off:end]
		}
	}

	for _, lut := range [...]string{"A2B0", "A2B1", "A2B2", "D2B0", "D2B1", "D2B2", "D2B3"} {
		if _, ok := tags[lut]; ok {
			return nil, fmt.Errorf("icc: %s is a LUT tag, and a CMM uses it in preference to matrix/TRC", lut)
		}
	}

	p := &Profile{gray: gray}
	if gray {
		data, ok := tags["kTRC"]
		if !ok {
			return nil, errors.New("icc: gray profile has no kTRC tag")
		}
		fn, err := parseCurve(data)
		if err != nil {
			return nil, err
		}
		p.trc[0] = fn
		p.black = blackPoint(vec3{d50[0] * fn(0), fn(0), d50[2] * fn(0)})[1]
		p.srgb = p.isSRGB()
		return p, nil
	}

	var xyz [3]vec3
	for i, sig := range [...]string{"rXYZ", "gXYZ", "bXYZ"} {
		data, ok := tags[sig]
		if !ok {
			return nil, fmt.Errorf("icc: RGB profile has no %s tag", sig)
		}
		v, err := parseXYZ(data)
		if err != nil {
			return nil, err
		}
		xyz[i] = v
	}
	for i, sig := range [...]string{"rTRC", "gTRC", "bTRC"} {
		data, ok := tags[sig]
		if !ok {
			return nil, fmt.Errorf("icc: RGB profile has no %s tag", sig)
		}
		fn, err := parseCurve(data)
		if err != nil {
			return nil, err
		}
		p.trc[i] = fn
	}
	// Columns are the colorants; M maps device-linear RGB to D50-relative XYZ, which is what the
	// profile's own rXYZ/gXYZ/bXYZ tags already are.
	m := mat3{
		{xyz[0][0], xyz[1][0], xyz[2][0]},
		{xyz[0][1], xyz[1][1], xyz[2][1]},
		{xyz[0][2], xyz[1][2], xyz[2][2]},
	}
	// Black point compensation scales each XYZ axis so the profile's black lands on sRGB's, which
	// is 0, and D50 stays where it is: K*xyz = k*(xyz-black), k = D50/(D50-black), per axis.
	black := blackPoint(mulMV(m, vec3{p.trc[0](0), p.trc[1](0), p.trc[2](0)}))
	var k mat3
	for i := range k {
		k[i][i] = d50[i] / (d50[i] - black[i])
	}
	p.m = mulMM(invS, mulMM(k, m))
	p.off = mulMV(invS, vec3{-k[0][0] * black[0], -k[1][1] * black[1], -k[2][2] * black[2]})
	p.srgb = p.isSRGB()
	return p, nil
}

// isSRGB reports whether converting through p moves no colour by as much as one 8-bit level, at
// every point of a grid 1/16 apart in each component — which is to say whether p is an sRGB
// profile, whose exact conversion to sRGB is the identity and whose computed one differs from it
// only by the rounding of its s15Fixed16 colorants and sampled curves.
//
// pdfium draws the stock 3144-byte "sRGB IEC61966-2.1" profile as DeviceRGB, recognising it by its
// size and description rather than its content: measured, the same profile with its red colorant
// altered still drew as DeviceRGB, and the unaltered profile with four bytes appended went through
// lcms instead, 333 levels off DeviceRGB summed over 468 channels. That profile is 0.51 levels from the
// identity on this grid (0.53 on a grid 1/255 apart), and the nearest non-sRGB profile Windows
// ships (PAL/SECAM) is 48, so the decision is made on content here and lands where pdfium's does on
// the profile that PDFs actually carry.
func (p *Profile) isSRGB() bool {
	const steps, tol = 16, 1.0 / 255
	at := func(i int) float64 { return float64(i) / steps }
	if p.gray {
		for i := 0; i <= steps; i++ {
			if v, _, _ := p.convert([]float64{at(i)}); math.Abs(v-at(i)) >= tol {
				return false
			}
		}
		return true
	}
	for i := 0; i <= steps; i++ {
		for j := 0; j <= steps; j++ {
			for k := 0; k <= steps; k++ {
				r, g, b := p.convert([]float64{at(i), at(j), at(k)})
				if math.Abs(r-at(i)) >= tol || math.Abs(g-at(j)) >= tol || math.Abs(b-at(k)) >= tol {
					return false
				}
			}
		}
	}
	return true
}

// Components is 1 for a gray profile and 3 for RGB, the number of values SRGB and a pixel's colour
// channels are read from.
func (p *Profile) Components() int {
	if p.gray {
		return 1
	}
	return 3
}

// SRGB converts one colour, given as p.Components() values in the profile's own space, to sRGB.
//
// Unlike Image, the input is not quantized to 8 bits before conversion: pdfium truncates every
// component to a byte before it ever reaches lcms, and this does not, so the two agree to within a
// level for a mid-gamma gray profile and to within about six levels near black for a linear one.
// An sRGB profile (isSRGB) returns the colour it was given, clamped.
func (p *Profile) SRGB(c []float64) (r, g, b float64) {
	if !p.srgb {
		return p.convert(c)
	}
	in := component(c)
	if p.gray {
		return in(0), in(0), in(0)
	}
	return in(0), in(1), in(2)
}

// component reads c's i'th value clamped to 0..1, and a missing one as 0.
func component(c []float64) func(i int) float64 {
	return func(i int) float64 {
		if i >= len(c) {
			return 0
		}
		return clamp01(c[i])
	}
}

// convert is SRGB's conversion through the profile's own curves and matrix, which isSRGB measures.
func (p *Profile) convert(c []float64) (r, g, b float64) {
	in := component(c)
	if p.gray {
		v := encode(p.grayLinear(in(0)))
		return v, v, v
	}
	o := p.linear(vec3{p.trc[0](in(0)), p.trc[1](in(1)), p.trc[2](in(2))})
	return encode(clamp01(o[0])), encode(clamp01(o[1])), encode(clamp01(o[2]))
}

// grayLinear is gray's linear sRGB value for the component x: its curve, then black point
// compensation, which leaves a profile whose black is black as it is.
func (p *Profile) grayLinear(x float64) float64 {
	return clamp01((p.trc[0](x) - p.black) / (1 - p.black))
}

// linear carries RGB's colour, already through its curves, to linear sRGB, unclamped: m folds the
// profile's device-to-XYZ matrix, black point compensation and sRGB's own XYZ-to-device matrix
// together, so this is the whole conversion in one multiply and an add.
func (p *Profile) linear(lin vec3) vec3 {
	o := mulMV(p.m, lin)
	return vec3{o[0] + p.off[0], o[1] + p.off[1], o[2] + p.off[2]}
}

// Image converts img to sRGB in place. Alpha is untouched, and a sub-image converts only the
// rectangle it addresses. An sRGB profile (isSRGB) leaves img as it is.
func (p *Profile) Image(img *image.NRGBA) {
	if p.srgb {
		return
	}
	p.once.Do(p.buildLUTs)
	encTableOnce.Do(buildEncTable)

	r := img.Rect
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[(y-r.Min.Y)*img.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			i := (x - r.Min.X) * 4
			if p.gray {
				v := p.grayLUT[row[i]]
				row[i], row[i+1], row[i+2] = v, v, v
				continue
			}
			o := p.linear(vec3{p.linR[row[i]], p.linG[row[i+1]], p.linB[row[i+2]]})
			row[i] = encTable[int(clamp01(o[0])*65535+0.5)]
			row[i+1] = encTable[int(clamp01(o[1])*65535+0.5)]
			row[i+2] = encTable[int(clamp01(o[2])*65535+0.5)]
		}
	}
}

// buildLUTs samples the profile's own curves once, under sync.Once, into the 8-bit tables Image
// reads per pixel. It calls the same curveFn and encode used by SRGB, so the two never compute the
// tone response differently.
func (p *Profile) buildLUTs() {
	if p.gray {
		for i := range p.grayLUT {
			p.grayLUT[i] = uint8(math.Round(255 * encode(p.grayLinear(float64(i)/255))))
		}
		return
	}
	for i := 0; i < 256; i++ {
		x := float64(i) / 255
		p.linR[i] = p.trc[0](x)
		p.linG[i] = p.trc[1](x)
		p.linB[i] = p.trc[2](x)
	}
}

// encTable is the 16-bit-to-8-bit encoding step of Image's pipeline, shared by every Profile
// because it does not depend on one: encode is sRGB's own curve, not the source profile's.
var (
	encTable     [65536]uint8
	encTableOnce sync.Once
)

func buildEncTable() {
	for i := range encTable {
		encTable[i] = uint8(math.Round(255 * encode(float64(i)/65535)))
	}
}

// encode is sRGB's encoding transfer function (IEC 61966-2-1), the last stage every conversion path
// in this package runs before returning a value.
func encode(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// clamp01 is the clamp every curve and every matrix output in this package is passed through: a
// hostile profile can put a fractional power under a negative base, and float arithmetic answers
// that with NaN rather than a panic, so this is the one place a NaN is turned into a defined value
// instead of propagating.
func clamp01(y float64) float64 {
	if !(y >= 0) {
		y = 0
	} else if y > 1 {
		y = 1
	}
	return y
}

// blackPoint is the black that black point compensation carries to sRGB's own, taken as lcms takes
// it (cmssamp.c, BlackPointAsDarkerColorant): the profile's darkest colour, xyz, through CIELAB,
// its lightness set to 0 above L* 95, where lcms reads the profile as a negative, and below 0,
// and clipped to 50 above 50. pdfium converts every ICC colour at the perceptual intent into lcms's
// own v4 sRGB profile, and lcms compensates every such conversion whose two blacks differ; a
// profile whose curves start at 0 has a black of 0, the same as sRGB's, and is left alone.
func blackPoint(xyz vec3) vec3 {
	f := func(t float64) float64 {
		if t <= 24.0/116*(24.0/116)*(24.0/116) {
			return 841.0/108*t + 16.0/116
		}
		return math.Cbrt(t)
	}
	fx, fy, fz := f(xyz[0]/d50[0]), f(xyz[1]/d50[1]), f(xyz[2]/d50[2])
	l, a, b := 116*fy-16, 500*(fx-fy), 200*(fy-fz)
	switch {
	case l > 95, l < 0:
		l = 0
	case l > 50:
		l = 50
	}
	inv := func(t float64) float64 {
		if t <= 24.0/116 {
			return 108.0 / 841 * (t - 16.0/116)
		}
		return t * t * t
	}
	y := (l + 16) / 116
	return vec3{inv(y+0.002*a) * d50[0], inv(y) * d50[1], inv(y-0.005*b) * d50[2]}
}

// parseCurve reads a curveType or parametricCurveType tag.
func parseCurve(data []byte) (curveFn, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("icc: TRC tag is %d bytes, less than the 12-byte minimum", len(data))
	}
	switch sig := string(data[0:4]); sig {
	case "curv":
		return parseCurv(data)
	case "para":
		return parsePara(data)
	default:
		return nil, fmt.Errorf("icc: TRC type %q is neither curveType nor parametricCurveType", sig)
	}
}

// parseCurv reads a curveType tag: no entries is the identity, one entry is a u8Fixed8 gamma, and
// two or more is a sampled table, linearly interpolated between its count-1 breakpoints.
func parseCurv(data []byte) (curveFn, error) {
	count := binary.BigEndian.Uint32(data[8:12])
	if 12+2*uint64(count) > uint64(len(data)) {
		return nil, fmt.Errorf("icc: curv count %d runs past the end of a %d-byte tag", count, len(data))
	}
	switch count {
	case 0:
		return func(x float64) float64 { return clamp01(x) }, nil
	case 1:
		gamma := float64(binary.BigEndian.Uint16(data[12:14])) / 256
		return func(x float64) float64 { return clamp01(math.Pow(x, gamma)) }, nil
	default:
		table := make([]float64, count)
		for i := range table {
			table[i] = float64(binary.BigEndian.Uint16(data[12+2*i:14+2*i])) / 65535
		}
		last := len(table) - 1
		return func(x float64) float64 {
			pos := x * float64(last)
			i := int(math.Floor(pos))
			if i < 0 {
				i = 0
			}
			if i >= last {
				return clamp01(table[last])
			}
			frac := pos - float64(i)
			return clamp01((1-frac)*table[i] + frac*table[i+1])
		}, nil
	}
}

// parametricParamCount is the parameter count the ICC specification fixes for each parametric
// curve function type: g; g a b; g a b c; g a b c d; g a b c d e f.
var parametricParamCount = map[uint16]int{0: 1, 1: 3, 2: 4, 3: 5, 4: 7}

// parsePara reads a parametricCurveType tag. Each function type below reads its own parameters by
// name rather than by index, so the formula next to each case is the formula the specification gives
// for it. Where the specification leaves a case undefined, a divides by a zero a or a power of a
// negative base, each follows lcms (cmsgamma.c, DefaultEvalParametricFn), which is what pdfium draws
// with; the value is then clipped to [0,1], the range ICC.1 §10.18 gives a curve's output.
func parsePara(data []byte) (curveFn, error) {
	fn := binary.BigEndian.Uint16(data[8:10])
	count, ok := parametricParamCount[fn]
	if !ok {
		return nil, fmt.Errorf("icc: parametric curve function type %d is not 0-4", fn)
	}
	if 12+4*uint64(count) > uint64(len(data)) { // #nosec G115 -- count is one of 1,3,4,5,7, from the map above
		return nil, fmt.Errorf("icc: parametric curve type %d parameters run past the end of a %d-byte tag", fn, len(data))
	}
	p := make([]float64, count)
	for i := range p {
		p[i] = s15(binary.BigEndian.Uint32(data[12+4*i : 16+4*i]))
	}

	// flat is the |a| below which lcms reads a line as having no slope (MATRIX_DET_TOLERANCE), and
	// the curve as 0 everywhere, rather than divide by it for the break point -b/a.
	const flat = 1e-4
	switch fn {
	case 0:
		g := p[0]
		return func(x float64) float64 { return clamp01(math.Pow(x, g)) }, nil
	case 1:
		g, a, b := p[0], p[1], p[2]
		return func(x float64) float64 {
			if math.Abs(a) < flat || x < -b/a || a*x+b <= 0 {
				return 0
			}
			return clamp01(math.Pow(a*x+b, g))
		}, nil
	case 2:
		g, a, b, c := p[0], p[1], p[2], p[3]
		return func(x float64) float64 {
			switch {
			case math.Abs(a) < flat:
				return 0
			case x < -b/a: // lcms raises a negative break to 0, which no x here, always in [0,1], can tell apart.
				return clamp01(c)
			case a*x+b <= 0:
				return 0
			}
			return clamp01(math.Pow(a*x+b, g) + c)
		}, nil
	case 3:
		g, a, b, c, d := p[0], p[1], p[2], p[3], p[4]
		return func(x float64) float64 {
			switch {
			case x < d:
				return clamp01(c * x)
			case a*x+b <= 0:
				return 0
			}
			return clamp01(math.Pow(a*x+b, g))
		}, nil
	default: // 4, the only value parametricParamCount has not already excluded.
		g, a, b, c, d, e, f := p[0], p[1], p[2], p[3], p[4], p[5], p[6]
		return func(x float64) float64 {
			switch {
			case x < d:
				return clamp01(c*x + f)
			case a*x+b <= 0:
				return clamp01(e)
			}
			return clamp01(math.Pow(a*x+b, g) + e)
		}, nil
	}
}

// parseXYZ reads an XYZType tag's one triple.
func parseXYZ(data []byte) (vec3, error) {
	if len(data) < 20 {
		return vec3{}, fmt.Errorf("icc: XYZ tag is %d bytes, less than the 20-byte minimum", len(data))
	}
	return vec3{
		s15(binary.BigEndian.Uint32(data[8:12])),
		s15(binary.BigEndian.Uint32(data[12:16])),
		s15(binary.BigEndian.Uint32(data[16:20])),
	}, nil
}

// s15 decodes an s15Fixed16Number, the ICC's signed 16.16 fixed point.
func s15(u uint32) float64 { return float64(int32(u)) / 65536 } // #nosec G115 -- the field is defined signed; see internal/ttfbuild for the same pattern

// mat3 and vec3 are the linear algebra this package needs and nothing more: a 3x3 matrix in
// row-major order and a column vector, multiplied as a matrix acting on the right.
type mat3 [3][3]float64
type vec3 [3]float64

func mulMV(m mat3, v vec3) vec3 {
	return vec3{
		m[0][0]*v[0] + m[0][1]*v[1] + m[0][2]*v[2],
		m[1][0]*v[0] + m[1][1]*v[1] + m[1][2]*v[2],
		m[2][0]*v[0] + m[2][1]*v[1] + m[2][2]*v[2],
	}
}

func mulMM(a, b mat3) mat3 {
	var r mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = a[i][0]*b[0][j] + a[i][1]*b[1][j] + a[i][2]*b[2][j]
		}
	}
	return r
}

func invMat3(m mat3) mat3 {
	a, b, c := m[0][0], m[0][1], m[0][2]
	d, e, f := m[1][0], m[1][1], m[1][2]
	g, h, i := m[2][0], m[2][1], m[2][2]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	inv := 1 / det
	return mat3{
		{(e*i - f*h) * inv, (c*h - b*i) * inv, (b*f - c*e) * inv},
		{(f*g - d*i) * inv, (a*i - c*g) * inv, (c*d - a*f) * inv},
		{(d*h - e*g) * inv, (b*g - a*h) * inv, (a*e - b*d) * inv},
	}
}

// invS is inv(S), where S is the matrix lcms builds for its own sRGB profile: sRGB's Rec. 709
// colorants, scaled to its D65 white and Bradford-adapted to the D50 this package's XYZ connection
// space uses. It is computed here rather than hard-coded so the derivation is checkable, and it is
// computed once, at init, because it does not depend on the profile being parsed — every RGB
// Profile shares this same inverse, multiplying it by that profile's own colorant matrix.
//
// A gray profile never multiplies by invS at all: the D50 white run through inv(S) is (1,1,1)
// exactly, because S was built to carry (1,1,1) to that same white, so a gray value's three sRGB
// channels are equal by construction and are computed directly as three copies of one curve, not by
// carrying a gray value through a matrix that would only return it unchanged.
var invS mat3

// d50 is the ICC connection space's white, as lcms states it (cmsD50X, cmsD50Y, cmsD50Z).
var d50 = vec3{0.9642, 1, 0.8249}

func init() {
	// Rec. 709 primaries and the D65 white point sRGB is defined against (IEC 61966-2-1), each
	// converted from xy chromaticity to XYZ with Y normalized to 1: X = x/y, Z = (1-x-y)/y.
	chroma := func(x, y float64) vec3 { return vec3{x / y, 1, (1 - x - y) / y} }
	white := chroma(0.3127, 0.3290)
	primaries := [3]vec3{chroma(0.64, 0.33), chroma(0.30, 0.60), chroma(0.15, 0.06)}
	var p mat3
	for j, col := range primaries {
		for i := 0; i < 3; i++ {
			p[i][j] = col[i]
		}
	}

	// Scaling each primary's column so that the resulting matrix carries the reference white to
	// its own XYZ is the standard construction of a primaries-to-XYZ matrix (the same one that
	// turns three chromaticities and a white point into an RGB colour space's definition).
	k := mulMV(invMat3(p), white)
	var pPrime mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			pPrime[i][j] = p[i][j] * k[j]
		}
	}

	// Bradford chromatic adaptation from the D65 this matrix was just built against to the D50 an
	// ICC profile's PCS uses (ICC.1:2010 Annex E).
	bradford := mat3{{0.8951, 0.2664, -0.1614}, {-0.7502, 1.7135, 0.0367}, {0.0389, -0.0685, 1.0296}}
	bw := mulMV(bradford, white)
	bd := mulMV(bradford, d50)
	scale := mat3{{bd[0] / bw[0], 0, 0}, {0, bd[1] / bw[1], 0}, {0, 0, bd[2] / bw[2]}}
	adapt := mulMM(mulMM(invMat3(bradford), scale), bradford)

	s := mulMM(adapt, pPrime)
	invS = invMat3(s)
}

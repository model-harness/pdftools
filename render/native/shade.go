package native

import (
	"fmt"
	"math"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/function"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// maxPageAreas bounds the passes over the page's area one page may make: each sh, and each soft
// mask gs, is charged one.
//
// Neither is bounded by anything else. A path costs its own size, an image its pixels, and a
// glyph its outline, but sh fills whatever the clip admits — the whole page, with no clip — and a
// soft mask renders its group onto a page-sized canvas and keeps a page-sized mask while it is in
// force. At 200 dpi a Letter page is 3.7 million pixels, so this is 240 MB of masks at worst, or
// 240 million pixels shaded. The corpus's one page with either makes two passes.
const maxPageAreas = 64

// chargeArea charges one page-area pass to the page, against maxPageAreas.
func (w *walker) chargeArea(op string) {
	w.areas++
	if w.areas > maxPageAreas {
		w.unsup[fmt.Sprintf("%s: the page makes more than %d passes over its whole area", op, maxPageAreas)] = true
	}
}

// axial is an axial shading (§8.7.4.5.3) as pdfium draws one: 256 colours sampled along the axis,
// and the axis to project each pixel onto. Every number is a float32, because pdfium's are, and a
// pixel that lands on a step boundary is coloured by which side of it the float32 arithmetic puts
// it.
type axial struct {
	x0, y0, dx, dy, len2 float32
	extend               [2]bool
	steps                [256][3]uint8
}

// shading resolves /Shading[name] as an axial shading, or says why it cannot be drawn.
//
// The colour space is resolved without the resources' /ColorSpace names, as pdfium resolves it:
// §8.7.4.3 allows a name there only for a device or a Pattern space, and pdfium looks one up in
// nothing. What is refused for a fill is refused here, and for the same reasons.
func (w *walker) shading(name objects.Name) (*axial, string) {
	res, _ := objects.GetDict(w.s, w.res, "Shading")
	raw, ok := res[name]
	if !ok {
		return nil, fmt.Sprintf("/%s is not in the page's /Shading resources", name)
	}
	var d objects.Dict
	if v, err := w.s.Resolve(raw); err == nil {
		switch o := v.(type) {
		case objects.Dict:
			d = o
		case *objects.Stream:
			d = o.Dict
		}
	}
	if d == nil {
		return nil, fmt.Sprintf("/%s is not a shading dictionary", name)
	}
	if t, _ := objects.GetInt(w.s, d, "ShadingType"); t != 2 {
		return nil, fmt.Sprintf("/%s is shading type %d, and only axial shadings (type 2) are drawn", name, t)
	}
	csObj, ok := d["ColorSpace"]
	if !ok {
		return nil, fmt.Sprintf("/%s has no /ColorSpace", name)
	}
	sp := w.colourSpace(csObj, false)
	if r := w.refusal(sp); r != "" {
		return nil, r
	}
	if sp.profile == nil && sp.n == 4 {
		// pdfium converts a shading's CMYK by Adobe's table, AdobeCMYK_to_sRGB, which is 16 levels
		// from §8.6.4.4's formula on average over a ramp; a fill's CMYK is the formula here too, and
		// is as far off, but a fill is one colour and a shading every colour between two.
		return nil, fmt.Sprintf("/%s is in DeviceCMYK, which pdfium converts by Adobe's table rather than §8.6.4.4's formula", name)
	}

	a := &axial{}
	c, ok := nums32(w.s, d, "Coords", 4)
	switch {
	case !ok:
		return nil, fmt.Sprintf("/%s has no /Coords of four numbers", name)
	case c == nil:
		return nil, fmt.Sprintf("/%s has /Coords past the float range pdfium holds them in", name)
	}
	a.x0, a.y0 = c[0], c[1]
	a.dx, a.dy = c[2]-c[0], c[3]-c[1]
	a.len2 = float32(a.dx*a.dx) + float32(a.dy*a.dy)
	switch {
	case math.IsInf(float64(a.len2), 0) || math.IsInf(float64(a.dx), 0) || math.IsInf(float64(a.dy), 0):
		return nil, fmt.Sprintf("/%s has /Coords past the float range pdfium holds them in", name)
	case a.len2 == 0:
		// pdfium divides by it, and every pixel's projection is then NaN or infinite.
		return nil, fmt.Sprintf("/%s has an axis of zero length", name)
	}

	t := []float32{0, 1}
	if _, has := d["Domain"]; has {
		switch t, ok = nums32(w.s, d, "Domain", 2); {
		case !ok:
			return nil, fmt.Sprintf("/%s has a /Domain that is not two numbers", name)
		case t == nil:
			return nil, fmt.Sprintf("/%s has a /Domain past the float range pdfium holds it in", name)
		}
	}
	if _, has := d["Extend"]; has {
		e, _ := objects.GetArray(w.s, d, "Extend")
		if len(e) != 2 {
			return nil, fmt.Sprintf("/%s has an /Extend that is not two booleans", name)
		}
		for i, o := range e {
			v, err := w.s.Resolve(o)
			b, ok := v.(objects.Bool)
			if err != nil || !ok {
				return nil, fmt.Sprintf("/%s has an /Extend that is not two booleans", name)
			}
			a.extend[i] = bool(b)
		}
	}
	if _, has := d["BBox"]; has {
		// pdfium intersects the device clip with the box's outer rectangle in whole pixels, which
		// is neither the box nor a clip to it; no page in the corpus has one.
		return nil, fmt.Sprintf("/%s has a /BBox, which pdfium clips to in whole device pixels", name)
	}

	fns, why := w.shadingFunctions(d, sp.n)
	if why != "" {
		return nil, fmt.Sprintf("/%s%s", name, why)
	}

	// pdfium's GetShadingSteps: step i samples the functions at i/256 of the way along the
	// domain, each function filling the next of the colour's components, and each channel is
	// rounded from float32. The components are clamped to 0..1 as pdfium's colour spaces clamp
	// them.
	diff := t[1] - t[0]
	out := make([]float32, sp.n)
	v := make([]float64, sp.n)
	for i := range a.steps {
		x := float32(float32(diff*float32(i))/256) + t[0]
		k := 0
		for _, f := range fns {
			f.Call(x, out[k:k+f.Outputs()])
			k += f.Outputs()
		}
		for j := range v {
			v[j] = clamp01(float64(out[j]))
		}
		p := sp.colour(v, black)
		if sp.profile != nil {
			p.r, p.g, p.b = sp.profile.Translate(v)
		}
		a.steps[i] = [3]uint8{channel(p.r), channel(p.g), channel(p.b)}
	}
	return a, ""
}

// channel is one colour channel as pdfium encodes it: roundf of the float32 times 255.
func channel(v float64) uint8 {
	return uint8(math.Round(float64(float32(clamp01(v)) * 255)))
}

// shadingFunctions reads an axial shading's /Function: one function of n outputs, or an array of
// one such, or an array of n functions of one output each — the two shapes §8.7.4.5.3 allows, and
// the two pdfium's ValidateFunctions accepts. The reason, when there is one, follows the shading's
// name.
func (w *walker) shadingFunctions(d objects.Dict, n int) ([]*function.Func, string) {
	raw, ok := d["Function"]
	if !ok {
		return nil, " has no /Function"
	}
	v, err := w.s.Resolve(raw)
	if err != nil {
		return nil, " has no /Function"
	}
	arr, isArr := v.(objects.Array)
	if !isArr {
		arr = objects.Array{v}
	}
	if len(arr) != 1 && len(arr) != n {
		return nil, fmt.Sprintf("'s /Function is %d functions for a colour space of %d components", len(arr), n)
	}
	fns := make([]*function.Func, len(arr))
	for i, o := range arr {
		f, err := function.Parse(w.s, o)
		if err != nil {
			return nil, fmt.Sprintf("'s /Function: %v", err)
		}
		fns[i] = f
	}
	switch {
	case len(fns) == 1 && fns[0].Outputs() != n:
		return nil, fmt.Sprintf("'s /Function has %d outputs for a colour space of %d components", fns[0].Outputs(), n)
	case len(fns) > 1:
		for _, f := range fns {
			if f.Outputs() != 1 {
				return nil, fmt.Sprintf("'s /Function has a function of %d outputs where each component needs its own",
					f.Outputs())
			}
		}
	}
	return fns, ""
}

// nums32 reads an array of exactly n numbers as float32s. ok is false when it is not that; the
// slice is nil when it is, but a number is past float32's range.
func nums32(s objects.Store, d objects.Dict, key objects.Name, n int) (v []float32, ok bool) {
	f, ok := nums(s, d, key, n)
	if !ok {
		return nil, false
	}
	v = make([]float32, n)
	for i, x := range f {
		if math.Abs(x) > math.MaxFloat32 {
			return nil, true
		}
		v[i] = float32(x)
	}
	return v, true
}

// checkShading records why an sh cannot be drawn, and charges the page for the pass it makes.
func (w *walker) checkShading(m *content.Machine, name objects.Name) {
	if _, why := w.shading(name); why != "" {
		w.unsup["sh: "+why] = true
		return
	}
	if _, ok := inverse32(shadingMatrix(m.GS.CTM, w.base.m, 0, 0)); !ok {
		// pdfium's inverse of a singular matrix is the identity, which draws the shading in device
		// space as though no CTM applied.
		w.unsup[fmt.Sprintf("sh: /%s is drawn under a CTM that maps it to a line or a point", name)] = true
		return
	}
	w.chargeArea("sh")
}

// drawShading paints an sh the survey has already accepted.
func (w *walker) drawShading(m *content.Machine, name objects.Name) {
	a, why := w.shading(name)
	if why != "" {
		return
	}
	c := w.canvasFor()
	left, top := c.clip.box()
	c.shade(a, shadingMatrix(m.GS.CTM, w.base.m, left, top), left, top, w.alpha, w.soft)
}

// shadingMatrix is the matrix pdfium draws an sh through into a bitmap whose first pixel is the
// device's (left, top): the CTM times the page's matrix, each product and sum rounded to float32
// as CFX_Matrix's operator* rounds them, then CPDF_DeviceBuffer's translation to the bitmap. The
// rounding is where a pixel that lands on a step boundary is decided, so the order is pdfium's.
func shadingMatrix(ctm, page geom.Matrix, left, top int) [6]float32 {
	a := [6]float32{float32(ctm.A), float32(ctm.B), float32(ctm.C), float32(ctm.D), float32(ctm.E), float32(ctm.F)}
	b := [6]float32{float32(page.A), float32(page.B), float32(page.C), float32(page.D), float32(page.E), float32(page.F)}
	return [6]float32{
		float32(a[0]*b[0]) + float32(a[1]*b[2]),
		float32(a[0]*b[1]) + float32(a[1]*b[3]),
		float32(a[2]*b[0]) + float32(a[3]*b[2]),
		float32(a[2]*b[1]) + float32(a[3]*b[3]),
		float32(float32(a[4]*b[0])+float32(a[5]*b[2])) + b[4] - float32(left),
		float32(float32(a[4]*b[1])+float32(a[5]*b[3])) + b[5] - float32(top),
	}
}

// inverse32 is pdfium's CFX_Matrix::GetInverse, in its float32, for a matrix that has one.
//
// Every product is rounded to float32 on its own line, as the C++ rounds it. Go may fuse a
// multiply and an add into one instruction where the target has one, which rounds once instead
// of twice, and an explicit conversion is what the language specification says prevents that.
func inverse32(m [6]float32) ([6]float32, bool) {
	a, b, c, d, e, f := m[0], m[1], m[2], m[3], m[4], m[5]
	i := float32(a*d) - float32(b*c)
	if i == 0 || math.IsNaN(float64(i)) || math.IsInf(float64(i), 0) {
		return [6]float32{}, false
	}
	j := -i
	inv := [6]float32{d / i, b / j, c / j, a / i,
		(float32(c*f) - float32(d*e)) / i, (float32(a*f) - float32(b*e)) / j}
	for _, v := range inv {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return [6]float32{}, false
		}
	}
	return inv, true
}

// shade paints an axial shading over everything the clip admits, as pdfium's DrawAxialShading
// does.
//
// Each device pixel's corner — not its centre — is mapped back into shading space, projected onto
// the axis, and scaled by 255 to pick one of the 256 steps, truncated toward zero. A spike over a
// 256-pixel ramp found that rule agreeing with pdfium on every pixel, the centre disagreeing on
// 127 and steps of i/255 on 126. A projection before the start or past the end takes the end's
// colour if that end is extended and leaves the pixel alone if it is not. pdfium truncates with a
// cast to int32, and the build this backend is measured against, WebAssembly's, saturates it: a
// projection past 2³¹ is INT_MAX and takes the end's rule, and NaN is 0 and takes the first step
// whatever /Extend says. Measured on an axis 10⁻⁷ long, where every pixel past the first overflows
// and pdfium draws the extended end; an x86 build gives INT_MIN there instead, and draws nothing.
//
// alpha is the constant fill alpha, which pdfium rounds to a whole 255th first; soft, when not
// nil, is a soft mask's per-pixel alpha. The steps are composited as pdfium composites the ARGB
// bitmap it drew them into (CompositeRow_Argb2Rgb): in integers, the clip's coverage scaling the
// alpha and FXDIB_ALPHA_MERGE blending, each truncating. blend's rounding put half the pixels of a
// ramp at half alpha one level off.
func (c *canvas) shade(a *axial, m [6]float32, left, top int, alpha float64, soft *mask) {
	inv, ok := inverse32(m)
	if !ok {
		return
	}
	a8 := int(math.Round(255 * alpha))
	w := c.clip.w
	for py := c.clip.y0; py < c.clip.y1; py++ {
		y := float32(py - top)
		for px := 0; px < w; px++ {
			i := py*w + px
			cl := c.clip.a[i]
			if cl == 0 {
				continue
			}
			x := float32(px - left)
			ux := float32(float32(inv[0]*x)+float32(inv[2]*y)) + inv[4]
			uy := float32(float32(inv[1]*x)+float32(inv[3]*y)) + inv[5]
			scale := float32(float32((ux-a.x0)*a.dx)+float32((uy-a.y0)*a.dy)) / a.len2
			s := scale * 255
			var k int
			switch {
			case math.IsNaN(float64(s)):
				k = 0
			case s <= -1:
				if !a.extend[0] {
					continue
				}
				k = 0
			case s >= 256:
				if !a.extend[1] {
					continue
				}
				k = 255
			default:
				k = int(s)
			}
			c.merge(i, a.steps[k], a8, soft, cl)
		}
	}
}

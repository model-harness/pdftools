package native

import (
	"fmt"
	"image"
	"math"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/geom"
	pdfimage "github.com/model-harness/pdftools/image"
	"github.com/model-harness/pdftools/objects"
)

// maxFormDepth bounds how deeply forms may nest, which is also what stops a form that draws
// itself: the survey refuses at this depth, so the paint pass never recurses past it. 8 is the
// bound extract and image already use for the same walk.
const maxFormDepth = 8

// maxOps bounds the operators surveyed for one page, counting every form each time it is drawn.
//
// Depth alone does not bound the work. A form that draws another form ten times, eight deep, is
// 10⁸ operators from a file of a few hundred bytes, and every one of them is surveyed and then
// painted. The corpus's largest page, forms included, is 13,733 operators; this is over 360 times that.
const maxOps = 5_000_000

// maxPagePixels bounds the decoded image pixels one page may hold, at four bytes each — 256 MB.
//
// image caps a single image at 256 million samples, which is right for an extractor that decodes
// one image at a time and a gigabyte of RGBA for a renderer that holds every image on the page at
// once. The corpus's largest image is 24.7 million pixels and its largest page total is the same
// image; this is two and a half times that.
const maxPagePixels = 64 << 20

// xobject is one Do target, resolved.
type xobject struct {
	name    objects.Name
	ref     objects.Ref
	isRef   bool
	st      *objects.Stream
	subtype objects.Name
}

// xobject resolves a name in the current resources' /XObject dictionary.
//
// The reason it returns is a refusal: every case where it is non-empty is a Do that draws
// something this backend cannot see, and drawing nothing in its place is the silent omission the
// package refuses pages to avoid.
func (w *walker) xobject(name objects.Name) (xobject, string) {
	x := xobject{name: name}
	xo, ok := objects.GetDict(w.s, w.res, "XObject")
	if !ok {
		return x, fmt.Sprintf("an XObject not in the resources, /%s", name)
	}
	raw, ok := xo[name]
	if !ok {
		return x, fmt.Sprintf("an XObject not in the resources, /%s", name)
	}
	x.ref, x.isRef = raw.(objects.Ref)
	v, err := w.s.Resolve(raw)
	if err != nil {
		return x, fmt.Sprintf("an XObject that did not resolve, /%s", name)
	}
	st, ok := v.(*objects.Stream)
	if !ok {
		return x, fmt.Sprintf("an XObject that is not a stream, /%s", name)
	}
	x.st = st
	x.subtype, _ = objects.GetName(w.s, st.Dict, "Subtype")
	switch x.subtype {
	case "Image", "Form":
		return x, ""
	}
	// PostScript XObjects (§8.8.2) are the one other subtype, and a conforming reader ignores
	// them — but ignoring one is still drawing the page without something its producer put there,
	// and the page says so rather than a reader deciding it did not matter.
	return x, fmt.Sprintf("an XObject of subtype /%s, /%s", x.subtype, name)
}

// surveyXObject records why a Do cannot be drawn, and walks a form's content for the same.
func (w *walker) surveyXObject(m *content.Machine, name objects.Name, depth int) {
	x, why := w.xobject(name)
	if why != "" {
		w.unsup["Do: "+why] = true
		return
	}
	if x.subtype == "Image" {
		if _, err := w.imagePixels(x, true); err != nil {
			w.unsup[fmt.Sprintf("Do: an image that cannot be drawn, /%s: %v", name, err)] = true
		}
		return
	}
	if why := w.formRefusal(x, depth); why != "" {
		w.unsup["Do: "+why] = true
		return
	}
	body, _ := w.formContent(x)
	w.inForm(m, x, func(sub *content.Machine) { w.survey(sub, body, depth+1) })
}

// formRefusal is why a form cannot be drawn, before its content is looked at.
//
// A transparency group (§11.6.6) is drawn as though it were not one, and that is exact only in the
// cases this admits. With the normal blend mode — the only one the survey lets through — an
// isolated group composites to the same result as a non-isolated one, so /I changes nothing. What
// does change the result is a knockout group, where each element replaces rather than composites
// over the ones before it, and a group drawn at a constant alpha below 1, which §11.6.6 applies to
// the group's *result*: two overlapping half-transparent squares inside it are one 50% shape, and
// drawn one by one they are a 75% overlap. Both are refused.
func (w *walker) formRefusal(x xobject, depth int) string {
	if depth+1 > maxFormDepth {
		return fmt.Sprintf("a form nested more than %d deep, /%s", maxFormDepth, x.name)
	}
	if g, ok := objects.GetDict(w.s, x.st.Dict, "Group"); ok {
		if s, _ := objects.GetName(w.s, g, "S"); s == "Transparency" {
			if k, _ := objects.GetBool(w.s, g, "K"); k {
				return fmt.Sprintf("a knockout transparency group, /%s", x.name)
			}
			if w.alpha < 1 {
				return fmt.Sprintf("a transparency group drawn at constant alpha below 1, /%s", x.name)
			}
		}
	}
	if _, ok := w.formContent(x); !ok {
		return fmt.Sprintf("a form whose content did not decode, /%s", x.name)
	}
	return ""
}

// formContent is a form's decoded content, decoded once per indirect reference per page.
//
// Cached here rather than read off the stream, because a Store may return a new *Stream for every
// Resolve. The first version decoded in the survey and read x.st.Decoded in the paint pass, which
// was a different copy with nothing decoded on it: every form painted as nothing, and no page was
// refused for it, because refusing is the survey's and the survey had seen the content.
func (w *walker) formContent(x xobject) ([]byte, bool) {
	if x.isRef {
		if b, ok := w.forms[x.ref]; ok {
			return b, true
		}
	}
	if x.st.Decoded == nil {
		if err := w.s.Decode(x.st); err != nil || x.st.Decoded == nil {
			return nil, false
		}
	}
	if x.isRef {
		w.forms[x.ref] = x.st.Decoded
	}
	return x.st.Decoded, true
}

// inForm runs body as a form's content stream, in the state §8.10.1 gives it.
//
// That is: bracketed by q and Q, so nothing the form sets outlives it; with /Matrix applied before
// the CTM in force at the Do; inheriting the text state, since a Tf before the Do is still the font
// inside; and with the form's own /Resources when it has them and the invoking stream's when it
// does not. The name cache goes with the resources, because a form's /F1 is its own and need not
// be the page's.
func (w *walker) inForm(m *content.Machine, x xobject, body func(sub *content.Machine)) {
	ctm := m.GS.CTM
	if fm, ok := numMatrix(w.s, x.st.Dict, "Matrix"); ok {
		ctm = fm.Mul(ctm)
	}
	sub := content.NewMachine(ctm)
	sub.GS.Text = m.GS.Text
	sub.GS.LineWidth = m.GS.LineWidth
	sub.GS.ClipDepth = m.GS.ClipDepth

	res, fonts := w.res, w.fonts
	if d, ok := objects.GetDict(w.s, x.st.Dict, "Resources"); ok {
		w.res, w.fonts = d, map[string]*textFont{}
	}
	w.save()
	body(sub)
	w.restore()
	w.res, w.fonts = res, fonts
}

// drawXObject paints a Do the survey has already accepted.
func (w *walker) drawXObject(m *content.Machine, name objects.Name, depth int) {
	x, why := w.xobject(name)
	if why != "" {
		return
	}
	if x.subtype == "Image" {
		px, err := w.imagePixels(x, false)
		if err != nil {
			return
		}
		w.canvasFor().image(px, w.base.mul(m.GS.CTM), w.alpha)
		return
	}
	w.inForm(m, x, func(sub *content.Machine) {
		// The form is clipped to its /BBox, in form space (§8.10.1). Installed as a pending clip
		// ended by nothing, which is what "re W n" does.
		if bb, ok := numRect(w.s, x.st.Dict, "BBox"); ok {
			w.path.rect(bb.X0, bb.Y0, bb.Width(), bb.Height(), w.base.mul(sub.GS.CTM))
			w.pending, w.pendingEO = true, false
			w.endPath()
		}
		body, _ := w.formContent(x)
		w.paint(sub, body, depth+1)
	})
}

// imagePixels decodes an image XObject, once per indirect reference per page.
//
// The survey decodes rather than only reading the dictionary, because a JPEG that does not decode
// is known only by trying, and a refusal found in the paint pass would arrive after the page was
// half drawn. The cache is what makes that free for the paint pass. counted is true in the survey,
// which is where the page's pixel budget is spent: a direct image — no reference to cache by — is
// decoded again when painted, and charging it twice would refuse a page the survey had accepted.
func (w *walker) imagePixels(x xobject, counted bool) (*image.NRGBA, error) {
	if x.isRef {
		if px, ok := w.images[x.ref]; ok {
			return px, nil
		}
	}
	im, err := pdfimage.Read(w.s, x.st)
	if err != nil {
		return nil, err
	}
	if counted {
		// The mask is charged too, because Pixels decodes it at its own size before scaling it
		// to the base: a 1×1 image carrying a mask of 16384×16384 is a gigabyte, not a pixel.
		area := im.Width * im.Height
		if im.SMask != nil {
			area += im.SMask.Width * im.SMask.Height
		}
		if w.pixels+area > maxPagePixels {
			return nil, fmt.Errorf("the page's images exceed %d decoded pixels", maxPagePixels)
		}
		w.pixels += area
	}
	px, err := pdfimage.Pixels(im)
	if err != nil {
		return nil, err
	}
	if x.isRef {
		w.images[x.ref] = px
	}
	return px, nil
}

// numMatrix reads a six-number array as a matrix.
func numMatrix(s objects.Store, d objects.Dict, key objects.Name) (geom.Matrix, bool) {
	v, ok := nums(s, d, key, 6)
	if !ok {
		return geom.Matrix{}, false
	}
	return geom.Matrix{A: v[0], B: v[1], C: v[2], D: v[3], E: v[4], F: v[5]}, true
}

// numRect reads a four-number array as a rectangle, normalized so either corner order works.
func numRect(s objects.Store, d objects.Dict, key objects.Name) (geom.Rect, bool) {
	v, ok := nums(s, d, key, 4)
	if !ok {
		return geom.Rect{}, false
	}
	return geom.NewRect(v[0], v[1], v[2], v[3]), true
}

func nums(s objects.Store, d objects.Dict, key objects.Name, n int) ([]float64, bool) {
	a, ok := objects.GetArray(s, d, key)
	if !ok || len(a) != n {
		return nil, false
	}
	v := make([]float64, n)
	for i, o := range a {
		r, err := s.Resolve(o)
		if err != nil {
			return nil, false
		}
		f, ok := objects.AsNum(r)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		v[i] = f
	}
	return v, true
}

// maxSubsamples caps the samples per device pixel along each axis when an image is drawn smaller
// than its own resolution.
//
// One sample per pixel aliases a downscaled image — a 1-pixel rule in a screenshot drawn at a
// third of its size lands on every third device pixel or on none — so a pixel averages as many
// samples as the image has per pixel, up to this. 4 is 16 bilinear lookups per pixel at most,
// which bounds an image's cost at a small multiple of a fill's over the same area.
const maxSubsamples = 4

// image composites pixels mapped from the unit square by m (§8.9.4), with the clip applied.
//
// Image space puts sample row 0 at the top, which is unit-square y = 1, so the vertical index is
// flipped here and nowhere else. Each device pixel inverts m to find where it lands in the image
// and samples bilinearly there; the fraction of its subsamples that land inside the unit square is
// its coverage, which is what gives a rotated image an anti-aliased edge.
func (c *canvas) image(src *image.NRGBA, m matrix, alpha float64) {
	a, b, cc, d, e, f := m.m.A, m.m.B, m.m.C, m.m.D, m.m.E, m.m.F
	det := a*d - b*cc
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		// A degenerate CTM maps the image to a line or a point, which covers no area.
		return
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw == 0 || sh == 0 {
		return
	}

	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range [4]point{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		q := m.apply(p)
		x0, y0 = math.Min(x0, q.x), math.Min(y0, q.y)
		x1, y1 = math.Max(x1, q.x), math.Max(y1, q.y)
	}
	w, h := c.clip.w, c.clip.h
	px0, px1 := clampRange(x0, x1, w)
	py0, py1 := clampRange(y0, y1, h)

	// The inverse, and how far one device pixel moves in the image along each device axis — the
	// samples a pixel spans, which is how many it needs.
	ia, ib, ic, id := d/det, -b/det, -cc/det, a/det
	span := math.Max(
		math.Max(math.Abs(ia)*float64(sw), math.Abs(ib)*float64(sh)),
		math.Max(math.Abs(ic)*float64(sw), math.Abs(id)*float64(sh)))
	n := int(math.Ceil(span))
	if n < 1 {
		n = 1
	}
	if n > maxSubsamples {
		n = maxSubsamples
	}
	inv := 1 / float64(n*n)

	for py := py0; py < py1; py++ {
		for px := px0; px < px1; px++ {
			cl := c.clip.a[py*w+px]
			if cl == 0 {
				continue
			}
			var sr, sg, sb, sa float64
			for j := 0; j < n; j++ {
				dy := float64(py) + (float64(j)+0.5)/float64(n) - f
				for i := 0; i < n; i++ {
					dx := float64(px) + (float64(i)+0.5)/float64(n) - e
					u := ia*dx + ic*dy
					v := ib*dx + id*dy
					if u < 0 || u >= 1 || v < 0 || v >= 1 {
						continue
					}
					r, g, bl, al := bilinear(src, u*float64(sw)-0.5, (1-v)*float64(sh)-0.5)
					sr += r * al
					sg += g * al
					sb += bl * al
					sa += al
				}
			}
			if sa == 0 {
				continue
			}
			cov := sa * inv * alpha * float64(cl) / 255
			o := (py*w + px) * 4
			c.img.Pix[o] = blend(c.img.Pix[o], sr/sa, cov)
			c.img.Pix[o+1] = blend(c.img.Pix[o+1], sg/sa, cov)
			c.img.Pix[o+2] = blend(c.img.Pix[o+2], sb/sa, cov)
		}
	}
}

// bilinear samples src at a continuous position in sample coordinates, with sample centres at
// integers and the edges clamped. Colour is returned on 0..255 and alpha on 0..1, and the colour
// is weighted by alpha before it is interpolated so a transparent sample's colour — which the file
// may leave as anything — does not bleed into its opaque neighbour.
func bilinear(src *image.NRGBA, x, y float64) (r, g, b, a float64) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	fx, fy := math.Floor(x), math.Floor(y)
	tx, ty := x-fx, y-fy
	ix, iy := int(fx), int(fy)
	for _, s := range [4]struct {
		dx, dy int
		wt     float64
	}{
		{0, 0, (1 - tx) * (1 - ty)}, {1, 0, tx * (1 - ty)},
		{0, 1, (1 - tx) * ty}, {1, 1, tx * ty},
	} {
		if s.wt == 0 {
			continue
		}
		sx, sy := clampIndex(ix+s.dx, sw), clampIndex(iy+s.dy, sh)
		o := sy*src.Stride + sx*4
		al := float64(src.Pix[o+3]) / 255 * s.wt
		r += float64(src.Pix[o]) * al
		g += float64(src.Pix[o+1]) * al
		b += float64(src.Pix[o+2]) * al
		a += al
	}
	if a > 0 {
		r, g, b = r/a, g/a, b/a
	}
	return r, g, b, a
}

func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

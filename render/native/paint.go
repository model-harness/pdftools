package native

import (
	"image"
	"math"

	"github.com/model-harness/pdftools/geom"
)

// matrix is a PDF transformation matrix in the form this package needs it: a point mapper
// from user space to device space.
//
// geom.Matrix is the repo's matrix and this wraps rather than replaces it, because what a
// rasterizer needs is one method — apply a point — and geom.Matrix already has it under a
// different signature. Wrapping keeps the conversion in one place instead of at every call
// site, and keeps this package's point type out of geom, which has no business knowing that
// a rasterizer exists.
type matrix struct{ m geom.Matrix }

func (t matrix) apply(p point) point {
	x, y := t.m.Apply(p.x, p.y)
	return point{x, y}
}

func (t matrix) mul(n geom.Matrix) matrix { return matrix{n.Mul(t.m)} }

// pt32 is a point as pdfium holds it, in float32.
type pt32 struct{ x, y float32 }

func f32(p point) pt32 { return pt32{float32(p.x), float32(p.y)} }

// mat32 is a matrix as pdfium's CFX_Matrix holds it, in float32, for the few decisions pdfium
// takes on its own arithmetic (fillRect). Every product is rounded before it is summed, as the
// WebAssembly build computes it, so no platform fuses one.
type mat32 struct{ a, b, c, d, e, f float32 }

func f32m(m geom.Matrix) mat32 {
	return mat32{float32(m.A), float32(m.B), float32(m.C), float32(m.D), float32(m.E), float32(m.F)}
}

// then is t followed by n, CFX_Matrix's t * n.
func (t mat32) then(n mat32) mat32 {
	return mat32{
		float32(t.a*n.a) + float32(t.b*n.c), float32(t.a*n.b) + float32(t.b*n.d),
		float32(t.c*n.a) + float32(t.d*n.c), float32(t.c*n.b) + float32(t.d*n.d),
		float32(float32(t.e*n.a)+float32(t.f*n.c)) + n.e, float32(float32(t.e*n.b)+float32(t.f*n.d)) + n.f,
	}
}

func (t mat32) apply(x, y float32) pt32 {
	return pt32{float32(float32(t.a*x)+float32(t.c*y)) + t.e, float32(float32(t.b*x)+float32(t.d*y)) + t.f}
}

// display32 is pdfium's page-to-device matrix for a pw×ph bitmap, in float32: the page matrix
// CPDF_Page::UpdateDimensions makes of the box and /Rotate, then GetDisplayMatrixForFloatRect's
// scale onto the bitmap, which flips y.
func display32(box geom.Rect, pw, ph, rotate int) mat32 {
	l, b, r, t := float32(box.X0), float32(box.Y0), float32(box.X1), float32(box.Y1)
	w, h := r-l, t-b
	pm := mat32{1, 0, 0, 1, -l, -b}
	switch rotate {
	case 90:
		w, h, pm = h, w, mat32{0, -1, 1, 0, -b, r}
	case 180:
		pm = mat32{-1, 0, 0, -1, r, t}
	case 270:
		w, h, pm = h, w, mat32{0, 1, -1, 0, t, -l}
	}
	fw, fh := float32(pw), float32(ph)
	return pm.then(mat32{fw / w, 0, 0, -fh / h, 0, fh})
}

// pageMatrix maps a page's user space to device pixels, at a scale per axis.
//
// The y flip is the whole of it: PDF user space has y increasing up the page and an image has
// row 0 at the top, so the device matrix negates y and offsets by the box's top edge. Getting
// this wrong renders a page upside down, which is the one rasterizer defect that cannot hide.
func pageMatrix(box geom.Rect, sx, sy float64, rotate int) matrix {
	// Unrotated: x grows right from the box's left edge, y grows down from its top.
	m := geom.Matrix{
		A: sx, B: 0,
		C: 0, D: -sy,
		E: -box.X0 * sx,
		F: box.Y1 * sy,
	}
	// /Rotate turns the page clockwise when displayed (§7.7.3.3), so the device transform is
	// that rotation applied after the flip above, with the translation chosen to bring the
	// rotated box back to the origin. Written out per case rather than composed from a general
	// rotation, because only four are legal and the constants are checkable by eye.
	w, h := box.Width()*sx, box.Height()*sy
	switch rotate {
	case 90:
		// (x, y) -> (h - y, x): the top edge becomes the right edge.
		m = m.Mul(geom.Matrix{A: 0, B: 1, C: -1, D: 0, E: h, F: 0})
	case 180:
		m = m.Mul(geom.Matrix{A: -1, B: 0, C: 0, D: -1, E: w, F: h})
	case 270:
		m = m.Mul(geom.Matrix{A: 0, B: -1, C: 1, D: 0, E: 0, F: w})
	}
	return matrix{m}
}

// paint is a fill colour, in the sRGB the output image is written in.
//
// Held as three floats rather than a color.Color because a PDF colour operator sets
// components that have to be converted once, and because the alpha this package composites
// with is the path's coverage rather than a colour channel.
type paint struct{ r, g, b float64 }

var black = paint{0, 0, 0}

// gray, rgb and cmyk are §8.6.4's three device colour spaces, converted by formula rather than
// through a profile.
//
// An ICCBased colour goes through its own profile instead (colour.go's space.colour), because a
// device space has no profile to go through in the first place. Separation, DeviceN, Lab,
// CalGray, CalRGB and Indexed over any of those still need a colour-management decision this
// package cannot make on the caller's behalf — which alternate, which tint transform — and stay
// refused; colour.go's colourSpace is where the list of what stays refused actually lives.
func gray(v float64) paint { return paint{v, v, v} }

func rgb(r, g, b float64) paint { return paint{r, g, b} }

// cmyk converts by the naive subtractive formula §8.6.4.4 gives, which is what a device with
// no profile does and what every other reader does in the same position.
func cmyk(c, m, y, k float64) paint {
	return paint{
		r: clamp01(1 - math.Min(1, c+k)),
		g: clamp01(1 - math.Min(1, m+k)),
		b: clamp01(1 - math.Min(1, y+k)),
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// canvas is the image being built, plus the clip in force.
type canvas struct {
	img  *image.RGBA
	clip *mask

	// clipped reports that clip is something other than the opaque page, so q can skip the
	// page-area clone when there is nothing to save — which is every q on a page that never
	// clips, and most of them on a page that does.
	clipped bool
}

func newCanvas(w, h int) *canvas {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// White, because a PDF page is white where nothing is drawn: the page has no backdrop of
	// its own (§11.4.7), and a transparent one would composite as black in every viewer that
	// flattens it.
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 255, 255, 255, 255
	}
	return &canvas{img: img, clip: opaqueMask(w, h)}
}

// fill composites a path's coverage over the canvas, with the clip applied.
//
// Row by row, through path.scan, rather than through a page-sized coverage mask: a spec page
// draws hundreds of small paths and a mask each would cost the page's area every time. The
// clip stays a full mask because it has to outlive the operator that set it.
//
// Source-over with the coverage as alpha, which is §11.3.8's normal blend mode for an opaque
// colour. Nothing here implements the rest of §11: a page that sets a blend mode is refused,
// because compositing one wrongly produces an image that looks plausible and is not the page.
//
// alpha is the constant fill alpha an ExtGState's /ca sets (§11.6.4.4), multiplied into the
// coverage. Correct only over an opaque backdrop and under the normal blend mode, which is
// what this canvas has — a white page and no blend mode, since any other is refused. Passed
// rather than folded into col, because alpha is graphics state and a later rg must not clear it.
//
// soft, when not nil, is a soft mask's per-pixel alpha. pdfium draws a masked mark into a layer of
// its own and composites the layer in integers, so a masked fill is composited as merge composites
// it: the colour in whole 255ths, the alpha truncated to them as pdfium's GetFillArgb truncates it
// — a shading's is rounded — and the coverage the layer's alpha is drawn at.
func (c *canvas) fill(p *path, col paint, evenOdd bool, alpha float64, soft *mask) {
	pr, pg, pb := col.r*255, col.g*255, col.b*255
	src, a8 := [3]uint8{channel(col.r), channel(col.g), channel(col.b)}, int(float32(alpha)*255)
	w := c.clip.w
	p.scan(w, c.clip.h, evenOdd, func(py int, row []float64) {
		base := py * w
		for x, v := range row {
			if v <= 0 {
				continue
			}
			if v > 1 {
				v = 1
			}
			if soft != nil {
				if cl := c.clip.a[base+x]; cl != 0 {
					c.merge(base+x, src, a8*int(math.Round(v*255))/255, soft, cl)
				}
				continue
			}
			al := v * alpha
			if cl := c.clip.a[base+x]; cl != 255 {
				if cl == 0 {
					continue
				}
				al *= float64(cl) / 255
			}
			o := (base + x) * 4
			c.img.Pix[o] = blend(c.img.Pix[o], pr, al)
			c.img.Pix[o+1] = blend(c.img.Pix[o+1], pg, al)
			c.img.Pix[o+2] = blend(c.img.Pix[o+2], pb, al)
		}
	})
}

// fillRect fills the pixels [x0,x1)×[y0,y1) whole, as CFX_AggDeviceDriver::FillRect does:
// each one merged at the fill's alpha byte, times the clip's coverage and the mask's.
func (c *canvas) fillRect(x0, y0, x1, y1 int, col paint, alpha float64, soft *mask) {
	src, a8 := [3]uint8{channel(col.r), channel(col.g), channel(col.b)}, int(float32(alpha)*255)
	w := c.clip.w
	x0, y0, x1, y1 = max(x0, 0), max(y0, 0), min(x1, w), min(y1, c.clip.h)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if cl := c.clip.a[y*w+x]; cl != 0 {
				c.merge(y*w+x, src, a8, soft, cl)
			}
		}
	}
}

func blend(dst uint8, src, alpha float64) uint8 {
	return uint8(math.Round(float64(dst)*(1-alpha) + src*alpha))
}

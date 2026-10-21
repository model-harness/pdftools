package ocr

import (
	"image"
	"image/color"
	"math"
)

// deskewMax and deskewStep bound the skew Deskew looks for, in degrees: a flatbed scan placed by
// hand is off by a degree or two, and the gold set's scans by 0.6 and 0.8 (degrade.go).
const (
	deskewMax  = 3.0
	deskewStep = 0.1
	// deskewMin is the skew below which a page is left as it is: a tenth of a degree moves a rule
	// across a page's width by under 3 pixels at 200 dpi, which the grid's tolerance absorbs.
	deskewMin = 0.15
)

// Deskew estimates img's skew and returns it rotated back level, with the angle it found in
// degrees, the turn rotate undoes. The angle is the one whose projection of
// the page's ink onto rows is most peaked: lines of type and a table's rules lie along rows only
// when the page is level (ADR 0047). A page skewed less than deskewMin is returned as it is.
func Deskew(img image.Image) (image.Image, float64) {
	b := img.Bounds()
	// Ink, every row and every 4th column: a rule 3 pixels thick sampled every 4 rows can fall
	// between samples and leave nothing to profile.
	type pt struct{ x, y float64 }
	var ink []pt
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			if color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y < 128 {
				ink = append(ink, pt{float64(x - b.Min.X), float64(y - b.Min.Y)})
			}
		}
	}
	if len(ink) < 100 {
		return img, 0
	}
	best, bestScore := 0.0, -1.0
	rows := make([]float64, b.Dy()/2+b.Dx()/2+8)
	for a := -deskewMax; a <= deskewMax+1e-9; a += deskewStep {
		sin, cos := math.Sincos(a * math.Pi / 180)
		for i := range rows {
			rows[i] = 0
		}
		for _, p := range ink {
			// The row the point falls in once the page is turned back by a.
			r := int((p.y*cos-p.x*sin)/2) + b.Dx()/4 + 4
			if r >= 0 && r < len(rows) {
				rows[r]++
			}
		}
		score := 0.0
		for _, v := range rows {
			score += v * v
		}
		if score > bestScore {
			best, bestScore = a, score
		}
	}
	if math.Abs(best) < deskewMin {
		return img, best
	}
	return rotate(img, best), best
}

// rotate turns img by deg degrees about its centre, the reverse of a skew of deg, interpolating
// between the four nearest pixels and filling with white what comes from outside it. Nearest
// pixel was tried first and broke thin strokes: scan-table's "B1" and "C1" came back "BL" and
// "Ci" from a page Tesseract read right when it was still turned.
func rotate(img image.Image, deg float64) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	gray := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gray[y*w+x] = float64(color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray).Y)
		}
	}
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= w || y >= h {
			return 255
		}
		return gray[y*w+x]
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	sin, cos := math.Sincos(deg * math.Pi / 180)
	cx, cy := float64(w)/2, float64(h)/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// The source of output pixel (x, y): the point that turning by deg brings here.
			dx, dy := float64(x)-cx, float64(y)-cy
			sx, sy := cos*dx-sin*dy+cx, sin*dx+cos*dy+cy
			x0, y0 := int(math.Floor(sx)), int(math.Floor(sy))
			fx, fy := sx-float64(x0), sy-float64(y0)
			v := at(x0, y0)*(1-fx)*(1-fy) + at(x0+1, y0)*fx*(1-fy) + at(x0, y0+1)*(1-fx)*fy + at(x0+1, y0+1)*fx*fy
			out.Pix[y*out.Stride+x] = uint8(math.Round(v))
		}
	}
	return out
}

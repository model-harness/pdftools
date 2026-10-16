// degrade turns a clean page render into what a flatbed scan of it looks like: grey,
// a slight skew, sensor noise, and JPEG at the quality office scanners use.
//
//	go run degrade.go in.png out.jpg
package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- seeded so a rebuild writes the same bytes, which crypto/rand cannot be
	"os"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	src, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	g := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g.Pix[y*w+x] = color.GrayModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.Gray).Y
		}
	}
	// 0.6 degrees of skew about the centre, bilinear, paper outside; then noise. The seed is
	// fixed so a rebuild writes the same bytes.
	out := image.NewGray(g.Bounds())
	sn, cs := math.Sincos(0.6 * math.Pi / 180)
	cx, cy := float64(w)/2, float64(h)/2
	rng := rand.New(rand.NewSource(1))
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= w || y >= h {
			return 255
		}
		return float64(g.Pix[y*w+x])
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx := cs*(float64(x)-cx) + sn*(float64(y)-cy) + cx
			fy := -sn*(float64(x)-cx) + cs*(float64(y)-cy) + cy
			x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
			ax, ay := fx-float64(x0), fy-float64(y0)
			v := at(x0, y0)*(1-ax)*(1-ay) + at(x0+1, y0)*ax*(1-ay) + at(x0, y0+1)*(1-ax)*ay + at(x0+1, y0+1)*ax*ay
			v = 0.92*v + 8 + rng.NormFloat64()*6 // a paper tone, not pure white, and sensor noise
			out.Pix[y*w+x] = uint8(math.Max(0, math.Min(255, v)))
		}
	}
	o, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer o.Close()
	if err := jpeg.Encode(o, out, &jpeg.Options{Quality: 60}); err != nil {
		panic(err)
	}
}

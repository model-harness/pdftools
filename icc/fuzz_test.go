package icc

import (
	"image"
	"math"
	"testing"

	"github.com/model-harness/pdftools/internal/iccbuild"
)

// FuzzParse feeds Parse the bytes of an ICC profile untrusted the way a PDF's ICCBased colour
// space stream is untrusted. Whatever Parse accepts must go on to convert without panicking and
// without producing a value outside [0,1] or a NaN, since those are the two properties the render
// pipeline downstream of this package relies on without checking them again itself.
//
// Run longer with:
//
//	go test ./icc -run '^$' -fuzz FuzzParse -fuzztime 60s
func FuzzParse(f *testing.F) {
	seeds := [][]byte{
		iccbuild.Gray(2.2),
		iccbuild.Gray(1.0),
		iccbuild.RGB(2.2, [3]float64{0.64, 0.33, 0.03}, [3]float64{0.21, 0.71, 0.08}, [3]float64{0.15, 0.06, 0.79}),
	}
	for _, s := range seeds {
		f.Add(s)
		for _, n := range []int{0, 1, 100, 131, 132, len(s) - 1, len(s) / 2} {
			if n >= 0 && n <= len(s) {
				f.Add(s[:n])
			}
		}
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := Parse(b)
		if err != nil {
			return
		}
		n := p.Components()
		for _, v := range []float64{0, 0.5, 1} {
			c := make([]float64, n)
			for i := range c {
				c[i] = v
			}
			r, g, bl := p.SRGB(c)
			for _, ch := range []float64{r, g, bl} {
				if math.IsNaN(ch) || ch < 0 || ch > 1 {
					t.Fatalf("SRGB(%v) = %v,%v,%v, want finite values in [0,1]", c, r, g, bl)
				}
			}
		}

		img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
		p.Image(img)
	})
}

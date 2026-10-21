package ocr

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

// skewed draws three long horizontal rules, as a table's, turned by deg degrees.
func skewed(deg float64) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	sin, cos := math.Sincos(deg * math.Pi / 180)
	for _, y0 := range []float64{150, 300, 450} {
		// Half-pixel steps, so the turned rule is drawn without holes.
		for x := 100.0; x < 700; x += 0.5 {
			for t := 0.0; t < 3; t += 0.5 {
				dx, dy := x-400, y0+t-300
				px, py := cos*dx-sin*dy+400, sin*dx+cos*dy+300
				img.Set(int(px), int(py), color.Black)
			}
		}
	}
	return img
}

// TestDeskewFindsTheAngle: a page turned by 0.6 or -1.2 degrees is found at that angle within a
// step, and set level; a level page is left as it is.
func TestDeskewFindsTheAngle(t *testing.T) {
	for _, deg := range []float64{0.6, -1.2, 2.5} {
		out, got := Deskew(skewed(deg))
		if math.Abs(got-deg) > deskewStep+1e-9 {
			t.Errorf("skew %.1f: found %.2f", deg, got)
		}
		if _, ys, _ := Grid(out); len(ys) != 3 {
			t.Errorf("skew %.1f: %d rules level after, want 3", deg, len(ys))
		}
	}
	// Found within a step of level, and left as it is.
	level := skewed(0)
	if out, got := Deskew(level); math.Abs(got) >= deskewMin || out != level {
		t.Errorf("a level page: angle %.2f, rotated %v", got, out != level)
	}
	// A page with one short dash on it has too little ink to read an angle from: every angle near
	// level puts the dash in one row, and the first of them would turn the page.
	dash := image.NewRGBA(image.Rect(0, 0, 800, 600))
	draw.Draw(dash, dash.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dash, image.Rect(300, 300, 460, 301), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if out, got := Deskew(dash); got != 0 || out != image.Image(dash) {
		t.Errorf("a dash: angle %g, rotated %v", got, out != image.Image(dash))
	}
}

// TestRotateInterpolates: turned a little, a black edge on white falls between pixels and reads
// grey there; the nearest pixel would leave only black and white.
func TestRotateInterpolates(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 60, 60))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y := 0; y < 60; y++ {
		for x := 0; x < 30; x++ {
			img.Pix[y*60+x] = 0
		}
	}
	out := rotate(img, 1).(*image.Gray)
	grey := 0
	for _, v := range out.Pix {
		if v > 20 && v < 235 {
			grey++
		}
	}
	if grey < 20 {
		t.Errorf("%d grey pixels along a turned edge, want an interpolated edge", grey)
	}
	if out.Pix[30*60+5] != 0 || out.Pix[30*60+55] != 255 {
		t.Error("the turn moved the page, not just its edge")
	}
}

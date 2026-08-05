package render

import (
	"math"
	"testing"

	"github.com/model-harness/pdftools/geom"
)

// TestFit covers the whole of the resolution policy, which is the only part of
// rendering that can be tested without a rasterizer. Every adapter must apply it
// identically, so a native rasterizer arriving later inherits these cases.
func TestFit(t *testing.T) {
	a4 := geom.NewRect(0, 0, 595.28, 841.89)
	letter := geom.NewRect(0, 0, 612, 792)

	for _, tc := range []struct {
		name    string
		box     geom.Rect
		opt     Options
		wantDPI float64
		wantW   int
		wantH   int
	}{
		{
			// 72 DPI is 1:1 with user space, so the pixel count is the point count.
			name: "72 dpi is one to one", box: letter,
			opt: Options{DPI: 72}, wantDPI: 72, wantW: 612, wantH: 792,
		},
		{
			name: "200 dpi letter", box: letter,
			opt: Options{DPI: 200}, wantDPI: 200, wantW: 1700, wantH: 2200,
		},
		{
			// A4 is fractional in points, so both axes round up. Rounding down would
			// drop the last partial row and column of every A4 page.
			name: "a4 rounds up", box: a4,
			opt: Options{DPI: 200}, wantDPI: 200, wantW: 1654, wantH: 2339,
		},
		{
			name: "no cap set", box: letter,
			opt: Options{DPI: 600, MaxPixels: 0}, wantDPI: 600, wantW: 5100, wantH: 6600,
		},
		{
			name: "cap not reached", box: letter,
			opt: Options{DPI: 200, MaxPixels: 64 << 20}, wantDPI: 200, wantW: 1700, wantH: 2200,
		},
		{
			// The reduction case, and the one that found a defect. Letter at 200 DPI
			// is 3,740,000 px against a 2,097,152 cap, so the DPI scales by
			// sqrt(2097152/3740000) to 149.7646 and the exact dimensions land on the
			// cap at 1272.999 x 1647.411, whose product is 2,097,152 exactly.
			// Rounding those *up* gives 1273 x 1648 = 2,097,904 — past the bound —
			// which is why a capped page floors instead. 1272 x 1647 = 2,094,984.
			name: "cap reduces dpi", box: letter,
			opt:     Options{DPI: 200, MaxPixels: 2 << 20},
			wantDPI: 200 * math.Sqrt(float64(2<<20)/(1700.0*2200.0)), wantW: 1272, wantH: 1647,
		},
		{
			// A one-pixel floor. 72x72pt at 8 DPI is 8 px, but at 0.5 DPI it is 0.5,
			// and a zero-dimension image is not an image — the PNG encoder rejects it.
			name: "floors at one pixel", box: geom.NewRect(0, 0, 4, 4),
			opt: Options{DPI: 8}, wantDPI: 8, wantW: 1, wantH: 1,
		},
		{
			// The box's origin does not have to be zero: a crop box inset from the
			// media box is common, and only the extent matters.
			name: "offset box uses extent", box: geom.NewRect(8.8, 6.7, 603.9, 849),
			opt: Options{DPI: 72}, wantDPI: 72, wantW: 596, wantH: 843,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dpi, w, h, err := Fit(tc.box, tc.opt)
			if err != nil {
				t.Fatalf("Fit: %v", err)
			}
			if math.Abs(dpi-tc.wantDPI) > 1e-9 {
				t.Errorf("dpi = %v, want %v", dpi, tc.wantDPI)
			}
			if w != tc.wantW || h != tc.wantH {
				t.Errorf("size = %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
			// The cap is the whole reason the reduction exists, so it is checked
			// after rounding rather than before: rounding up twice could otherwise
			// carry a reduced page back over the limit.
			if tc.opt.MaxPixels > 0 && w*h > tc.opt.MaxPixels {
				t.Errorf("%dx%d = %d pixels exceeds MaxPixels %d", w, h, w*h, tc.opt.MaxPixels)
			}
		})
	}
}

// TestFitRejects covers the inputs that have no sensible answer. A degenerate box
// or a nonsense DPI must fail here, before an adapter allocates a bitmap from it —
// /MediaBox is producer-controlled and this is where a hostile one stops.
func TestFitRejects(t *testing.T) {
	inf := math.Inf(1)
	for _, tc := range []struct {
		name string
		box  geom.Rect
		opt  Options
	}{
		{"zero box", geom.Rect{}, Options{DPI: 200}},
		{"zero width", geom.NewRect(0, 0, 0, 792), Options{DPI: 200}},
		{"zero height", geom.NewRect(0, 0, 612, 0), Options{DPI: 200}},
		{"nan box", geom.NewRect(0, 0, math.NaN(), 792), Options{DPI: 200}},
		{"inf box", geom.NewRect(0, 0, inf, 792), Options{DPI: 200}},
		{"zero dpi", geom.NewRect(0, 0, 612, 792), Options{}},
		{"negative dpi", geom.NewRect(0, 0, 612, 792), Options{DPI: -200}},
		{"nan dpi", geom.NewRect(0, 0, 612, 792), Options{DPI: math.NaN()}},
		{"inf dpi", geom.NewRect(0, 0, 612, 792), Options{DPI: inf}},
		// With the cap disabled there is nothing left to bound the dimensions, and a
		// float64 past the int range converts to the *minimum* int on amd64, which the
		// one-pixel floor then reports as 1. This returned 8500000000000000000 x 1
		// before Fit checked the magnitude ahead of the conversion.
		{"huge dpi, no cap", geom.NewRect(0, 0, 612, 792), Options{DPI: 1e18}},
		{"huge dpi, cap disabled by a negative", geom.NewRect(0, 0, 612, 792), Options{DPI: 1e18, MaxPixels: -1}},
		{"huge box, no cap", geom.NewRect(0, 0, 1e15, 1e15), Options{DPI: 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, w, h, err := Fit(tc.box, tc.opt); err == nil {
				t.Errorf("Fit returned %dx%d and no error", w, h)
			}
		})
	}
}

// TestFitCapsHostileMediaBox is the case the cap exists for.
//
// /MediaBox is two producer-controlled numbers. A page declaring 200 by 200 inches
// costs nothing to write and asks for 1.6 gigapixels at 200 DPI, which at 4 bytes a
// pixel is 6.4 GB of RGBA. The cap must bring that inside its bound while still
// producing a usable image, because refusing would turn one bad page into a failed
// run.
func TestFitCapsHostileMediaBox(t *testing.T) {
	huge := geom.NewRect(0, 0, 200*72, 200*72)
	opt := DefaultOptions

	dpi, w, h, err := Fit(huge, opt)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	if w*h > opt.MaxPixels {
		t.Errorf("%dx%d = %d pixels exceeds the %d cap", w, h, w*h, opt.MaxPixels)
	}
	if dpi >= opt.DPI {
		t.Errorf("dpi %v was not reduced from %v", dpi, opt.DPI)
	}
	// Still a real image. A cap that produced a 1x1 thumbnail would satisfy the
	// bound and be useless.
	if w < 1000 || h < 1000 {
		t.Errorf("%dx%d is too small to be useful", w, h)
	}
	// Square in, square out.
	if w != h {
		t.Errorf("aspect ratio lost: %dx%d from a square page", w, h)
	}
	if r := (&Raster{DPI: dpi}); !r.Reduced(opt) {
		t.Error("Reduced reports false for a page the cap reduced")
	}
}

package extract

import (
	"testing"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// imagePage paints stream on a page whose resources hold /Im, a 64x48 image, /Mk, a stencil
// mask, and /Fm, a form that paints /Im through its own matrix.
func imagePage(t *testing.T, stream string) doc.Page {
	t.Helper()
	im, mk, fm := objects.Ref{Num: 30}, objects.Ref{Num: 31}, objects.Ref{Num: 32}
	xobjs := objects.Dict{"Im": im, "Mk": mk, "Fm": fm}
	s := &memStore{
		objs: map[objects.Ref]objects.Object{
			im: &objects.Stream{Dict: objects.Dict{"Type": objects.Name("XObject"), "Subtype": objects.Name("Image"),
				"Width": objects.Int(64), "Height": objects.Int(48), "BitsPerComponent": objects.Int(8),
				"ColorSpace": objects.Name("DeviceRGB")}},
			mk: &objects.Stream{Dict: objects.Dict{"Type": objects.Name("XObject"), "Subtype": objects.Name("Image"),
				"Width": objects.Int(8), "Height": objects.Int(8), "ImageMask": objects.Bool(true)}},
			fm: &objects.Stream{Dict: objects.Dict{"Type": objects.Name("XObject"), "Subtype": objects.Name("Form"),
				"Matrix":    objects.Array{objects.Int(2), objects.Int(0), objects.Int(0), objects.Int(2), objects.Int(5), objects.Int(5)},
				"Resources": objects.Dict{"XObject": objects.Dict{"Im": im}}},
				Raw: []byte("q 10 0 0 10 0 0 cm /Im Do Q")},
		},
		pages: []objects.Dict{{
			"Type":      objects.Name("Page"),
			"MediaBox":  objects.Array{objects.Int(0), objects.Int(0), objects.Int(612), objects.Int(792)},
			"Resources": objects.Dict{"XObject": xobjs},
		}},
		content: [][]byte{[]byte(stream)},
	}
	p, err := New(s, DefaultOptions).Page(1)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImagesArePlacedByTheCTM(t *testing.T) {
	for _, c := range []struct {
		name, stream string
		want         doc.Image
	}{
		{"an XObject", "q 128 0 0 96 100 600 cm /Im Do Q",
			doc.Image{Box: geom.Rect{X0: 100, Y0: 600, X1: 228, Y1: 696}, Width: 64, Height: 48, MCID: -1}},
		// A rotation swaps the box's axes: the unit square's corners are bounded, not its origin taken.
		{"rotated", "q 0 96 -128 0 300 100 cm /Im Do Q",
			doc.Image{Box: geom.Rect{X0: 172, Y0: 100, X1: 300, Y1: 196}, Width: 64, Height: 48, MCID: -1}},
		{"a stencil mask", "q 8 0 0 8 0 0 cm /Mk Do Q",
			doc.Image{Box: geom.Rect{X1: 8, Y1: 8}, Width: 8, Height: 8, Mask: true, MCID: -1}},
		// Inside a form, its matrix and the CTM both apply: 10 * 2 = 20 wide, at 5 + 2*0.
		{"inside a form", "q 1 0 0 1 100 100 cm /Fm Do Q",
			doc.Image{Box: geom.Rect{X0: 105, Y0: 105, X1: 125, Y1: 125}, Width: 64, Height: 48, MCID: -1}},
		{"inline, abbreviated", "q 30 0 0 20 10 10 cm BI /W 3 /H 2 /IM true ID abcdef EI Q",
			doc.Image{Box: geom.Rect{X0: 10, Y0: 10, X1: 40, Y1: 30}, Width: 3, Height: 2, Mask: true, MCID: -1}},
		{"inline, in full", "q 30 0 0 20 10 10 cm BI /Width 3 /Height 2 /BitsPerComponent 8 /ColorSpace /DeviceGray ID abcdef EI Q",
			doc.Image{Box: geom.Rect{X0: 10, Y0: 10, X1: 40, Y1: 30}, Width: 3, Height: 2, MCID: -1}},
		{"in marked content", "/Figure <</MCID 4>> BDC q 10 0 0 10 0 0 cm /Im Do Q EMC",
			doc.Image{Box: geom.Rect{X1: 10, Y1: 10}, Width: 64, Height: 48, MCID: 4}},
		{"an artifact", "/Artifact BMC q 10 0 0 10 0 0 cm /Im Do Q EMC",
			doc.Image{Box: geom.Rect{X1: 10, Y1: 10}, Width: 64, Height: 48, MCID: -1, Artifact: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := imagePage(t, c.stream)
			if len(p.Images) != 1 {
				t.Fatalf("%d images, want 1: %+v", len(p.Images), p.Images)
			}
			got := p.Images[0]
			tol := geom.Tolerance{Epsilon: 1e-9}
			if !tol.NearlyEqual(got.Box.X0, c.want.Box.X0) || !tol.NearlyEqual(got.Box.Y0, c.want.Box.Y0) ||
				!tol.NearlyEqual(got.Box.X1, c.want.Box.X1) || !tol.NearlyEqual(got.Box.Y1, c.want.Box.Y1) {
				t.Errorf("box = %+v, want %+v", got.Box, c.want.Box)
			}
			got.Box = c.want.Box
			if got != c.want {
				t.Errorf("image = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestImagesAreBounded: a page of more painted images than maxImages keeps the first maxImages.
func TestImagesAreBounded(t *testing.T) {
	var b []byte
	for i := 0; i < maxImages+3; i++ {
		b = append(b, "/Im Do "...)
	}
	if p := imagePage(t, string(b)); len(p.Images) != maxImages {
		t.Errorf("%d images, want %d", len(p.Images), maxImages)
	}
}

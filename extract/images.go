package extract

import (
	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// maxImages bounds the images kept from one page. A page of a few thousand painted images is a
// tiled scan or a pattern drawn by hand, and past this the page's images are not worth the
// memory a hostile file could make them cost.
const maxImages = 4096

// paintImage records an image painted with the machine's current state: an image is the unit
// square drawn through the CTM (§8.9.4), so its box on the page is that square's bound.
func (r *run) paintImage(m *content.Machine, d objects.Dict, inline bool) {
	if len(r.images) >= maxImages {
		return
	}
	w, h := r.imageInt(d, inline, "Width", "W"), r.imageInt(d, inline, "Height", "H")
	mask := r.imageBool(d, inline, "ImageMask", "IM")
	ctm := m.GS.CTM
	var box geom.Rect
	for i, c := range [4][2]float64{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		x, y := ctm.Apply(c[0], c[1])
		if i == 0 {
			box = geom.Rect{X0: x, Y0: y, X1: x, Y1: y}
			continue
		}
		box = geom.Rect{X0: min(box.X0, x), Y0: min(box.Y0, y), X1: max(box.X1, x), Y1: max(box.Y1, y)}
	}
	r.images = append(r.images, doc.Image{Box: box, Width: w, Height: h, Mask: mask,
		MCID: m.MCID(), Artifact: m.InArtifact()})
}

// imageInt reads an integer from an image dictionary, under its inline abbreviation for an
// inline image (§8.9.7, Table 92).
func (r *run) imageInt(d objects.Dict, inline bool, key, abbr objects.Name) int {
	if v, ok := objects.GetInt(r.ex.s, d, key); ok {
		return int(v)
	}
	if inline {
		if v, ok := objects.GetInt(r.ex.s, d, abbr); ok {
			return int(v)
		}
	}
	return 0
}

func (r *run) imageBool(d objects.Dict, inline bool, key, abbr objects.Name) bool {
	if v, ok := objects.GetBool(r.ex.s, d, key); ok {
		return v
	}
	if inline {
		v, _ := objects.GetBool(r.ex.s, d, abbr)
		return v
	}
	return false
}

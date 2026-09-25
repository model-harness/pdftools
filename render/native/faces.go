package native

import "github.com/model-harness/pdftools/font"

// Option configures a rasterizer.
type Option func(*rasterizer)

// WithFaces supplies the source of substitute font programs.
//
// Without it, a page whose font embeds no program is refused with the reason, which is the default
// because a refusal is honest where a silently wrong typeface is not. The cost of that choice,
// stated rather than discovered: **this backend draws 1,113 of the corpus's 1,251 pages only when a
// font.FaceSource is supplied, and 0 without one.** Every claim about what it renders carries that
// qualifier. cmd/pdfspec is where this repo supplies them, from internal/liberation.
func WithFaces(src font.FaceSource) Option {
	return func(r *rasterizer) { r.faces = src }
}

// substitute asks the face source for a program to stand in for f.
//
// Nothing is cached here because loadFont already caches per resource name for the page, and a
// FaceSource is expected to cache its own parsed programs — it holds a handful at most, where this
// walker is rebuilt for every page.
func (w *walker) substitute(f *font.Font, why string) (*font.TrueType, string) {
	if w.faces == nil {
		return nil, why + ", and no face source was supplied to draw one"
	}
	req := font.FaceRequest{Name: f.Name(), Style: f.SubstituteStyle(), Why: why}
	tt, ok := w.faces.Face(req)
	if !ok {
		return nil, why + ", and the face source has no " + req.Style.String() + " face for /" + req.Name
	}
	return tt, ""
}

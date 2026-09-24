package main

import (
	"errors"
	"fmt"

	"github.com/model-harness/pdftools/internal/liberation"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
	"github.com/model-harness/pdftools/render/native"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
)

// backendNames is what -backend accepts, and the order is the order they are documented in.
const backendNames = "pdfium, native, auto"

// openRasterizer builds the rasterizer a -backend value names.
//
// One constructor for every caller, which is the point of it: render and ocr both needed a
// Rasterizer and both had `renderpdfium.Open` written out, so adding a second backend to one of
// them would have left the other on the first with nothing to say so.
//
// pdfium stays the default. The two backends do not produce identical pixels — they disagree on
// CMYK by design, on glyph grid-fitting because this one does not hint, and on a substituted
// typeface because pdfium uses Foxit's faces and this uses Liberation — so making native the
// default would silently change every rendered page for existing callers. Opt-in is the honest
// shape until the native backend draws everything.
func openRasterizer(name, path string) (render.Rasterizer, error) {
	switch name {
	case "pdfium":
		return renderpdfium.Open(path)

	case "native":
		return openNative(path)

	case "auto":
		// Native first, pdfium for the pages it refuses. This is the fallback ADR 0015 described
		// and deliberately did not build — it said ErrUnsupported was "what a fallback will be
		// built on, not something one already uses" — and this is that being used.
		nat, err := openNative(path)
		if err != nil {
			return nil, err
		}
		pdf, err := renderpdfium.Open(path)
		if err != nil {
			_ = nat.Close()
			return nil, err
		}
		return &fallback{native: nat, borrowed: pdf}, nil
	}
	return nil, fmt.Errorf("unknown -backend %q, want one of: %s", name, backendNames)
}

// openNative opens a store and wraps the native rasterizer so closing one closes both.
func openNative(path string) (render.Rasterizer, error) {
	s, err := pcstore.Open(path)
	if err != nil {
		return nil, err
	}
	// The face source is supplied here and not inside render/native, because which face stands in
	// for Helvetica is policy rather than parsing — see native.FaceSource. This is where this repo
	// makes that choice, and it is why the backend draws a page of standard-14 text at all.
	return &nativeRasterizer{
		Rasterizer: native.New(s, native.WithFaces(liberation.New())),
		store:      s,
	}, nil
}

// nativeRasterizer ties the store's lifetime to the rasterizer's.
//
// render/native borrows its Store and documents that Close does not close it, because the caller
// usually opened it for text extraction and is still using it. Here nothing else holds it, so
// something has to — and a caller that had to remember two Closes would leak a file handle per
// document the first time one path returned early.
type nativeRasterizer struct {
	render.Rasterizer
	store interface{ Close() error }
}

func (n *nativeRasterizer) Close() error {
	err := n.Rasterizer.Close()
	if serr := n.store.Close(); err == nil {
		err = serr
	}
	return err
}

// fallback renders with the native backend and falls back to the borrowed one per page.
//
// Per page rather than per document, which is the whole value of it: the native backend refuses a
// page it cannot draw *in full*, so a document mixing pages it can and cannot draw gets the native
// rendering for the former and a correct rendering for the rest. A per-document choice would give
// up the moment one page used a feature.
//
// Only ErrUnsupported falls through. A malformed page is an error from either backend and
// retrying it on the second would replace a specific complaint with a vaguer one.
type fallback struct {
	native, borrowed render.Rasterizer
}

func (f *fallback) PageCount() int { return f.borrowed.PageCount() }

func (f *fallback) Page(n int, o render.Options) (*render.Raster, error) {
	ra, err := f.native.Page(n, o)
	if err == nil {
		return ra, nil
	}
	if !errors.Is(err, native.ErrUnsupported) {
		return nil, err
	}
	return f.borrowed.Page(n, o)
}

func (f *fallback) Close() error {
	err := f.native.Close()
	if berr := f.borrowed.Close(); err == nil {
		err = berr
	}
	return err
}

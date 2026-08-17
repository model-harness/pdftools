// Package extract turns a page's content stream into positioned text.
//
// This is the use case the rest of the repo exists to serve, and the reason it is
// its own package is the failure it has to avoid. Every column of the benchmark in
// docs/DESIGN.md §1 — 0.01% spaces, a 4,069-character "word", 6% of words over 25
// characters — is one bug: PDF does not record words, only glyphs and the
// positions they are painted at, so a reader that does not reconstruct the gaps
// between them produces text that is character-for-character correct and
// unreadable.
//
// Reconstruction needs three things at once, and no two of them live in the same
// package: the glyph's advance width (font), the composed text rendering matrix
// (content), and one threshold policy for deciding when a gap is a space
// (geom.Tolerance). This package is where they meet. It owns no parsing and no
// tables of its own.
//
// What it produces is doc.Page — the same type the OCR path produces — so a
// scanned page and a born-digital one reach the sinks the same way.
package extract

import (
	"fmt"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/font"
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// maxFormDepth bounds recursion into Form XObjects. A form may reference itself,
// directly or through a cycle, and the reference set below does not stop that on
// its own: a cycle of inline dictionaries has no reference to deduplicate.
const maxFormDepth = 12

// Options configures extraction.
type Options struct {
	// Tol is the threshold policy. The zero value is replaced by
	// geom.DefaultTolerance, because a zero SpaceFrac would infer a space between
	// every pair of glyphs.
	Tol geom.Tolerance

	// KeepArtifacts includes text inside Artifact marked-content regions — running
	// headers, folios, watermarks. Off by default: on a 1,023-page specification
	// that text is the same header a thousand times, and it interleaves with body
	// prose at every page boundary. The blocks are still produced and still
	// carry doc.RoleArtifact when this is on, so the judgement stays inspectable
	// rather than being made silently at read time.
	KeepArtifacts bool

	// KeepHidden includes text drawn in rendering mode 3 or 7. On by default,
	// because invisible text is the text layer under a scanned page and it is
	// exactly what an extractor wants; set false only when comparing against what
	// a viewer displays.
	KeepHidden bool
}

func (o Options) withDefaults() Options {
	if o.Tol == (geom.Tolerance{}) {
		o.Tol = geom.DefaultTolerance
	}
	return o
}

// DefaultOptions is extraction as the CLI runs it: artifacts dropped, hidden text
// kept, default tolerances.
var DefaultOptions = Options{Tol: geom.DefaultTolerance, KeepHidden: true}

// Extractor reads pages from one document.
//
// It is not safe for concurrent use, because objects.Store makes no such promise
// and the font cache is unsynchronized. Page-level parallelism uses one Extractor
// per worker over its own Store, which is what the --jobs flag will do.
type Extractor struct {
	s   objects.Store
	opt Options

	// fonts caches loaded fonts by the reference their dictionary was found under.
	// A font is loaded once and used for every glyph on every page that names it:
	// on a 1,023-page specification, re-parsing a /ToUnicode CMap per page is the
	// difference between a fast conversion and a slow one, and the whole point of
	// font.Load doing its work up front.
	fonts map[objects.Ref]*font.Font
}

// New returns an Extractor over s. The Store is borrowed, not owned: closing it
// remains the caller's job, because the caller opened it and may still be probing
// it.
func New(s objects.Store, opt Options) *Extractor {
	return &Extractor{
		s:     s,
		opt:   opt.withDefaults(),
		fonts: map[objects.Ref]*font.Font{},
	}
}

// Document extracts every page.
//
// A page that fails to extract yields an empty page rather than an error for the
// document. One malformed page out of a thousand must not cost the other 999, and
// an empty page in the output is visible where a missing one would silently
// renumber everything after it.
func (e *Extractor) Document() (*doc.Document, error) {
	d := &doc.Document{Meta: e.metadata()}
	n := e.s.PageCount()
	if n < 0 {
		return nil, fmt.Errorf("extract: page count %d", n)
	}
	d.Pages = make([]doc.Page, 0, n)
	for i := 1; i <= n; i++ {
		p, err := e.Page(i)
		if err != nil {
			p = doc.Page{Number: i}
		}
		d.Pages = append(d.Pages, p)
	}
	return d, nil
}

// Page extracts one 1-based page.
func (e *Extractor) Page(n int) (doc.Page, error) {
	page, err := e.s.Page(n)
	if err != nil {
		return doc.Page{Number: n}, fmt.Errorf("extract: page %d: %w", n, err)
	}

	p := doc.Page{Number: n, Box: e.pageBox(page)}
	if rot, ok := objects.GetInt(e.s, page, "Rotate"); ok {
		p.Rotate = int(rot)
	}

	data, err := e.s.PageContent(n)
	if err != nil {
		// No content stream is a legitimately blank page, and also what a damaged
		// one looks like. Either way the page exists and has no text.
		return p, nil
	}

	res, _ := objects.GetDict(e.s, page, "Resources")
	run := &run{ex: e, tol: e.opt.Tol, page: n}
	// The initial CTM is the identity: extraction reports positions in the page's
	// own user space, which is what the box and every downstream comparison are
	// expressed in. Flipping to a top-left origin is the rasterizer's job, and
	// doing it here would put the page's own coordinates out of agreement with the
	// /MediaBox they came from.
	run.walk(data, res, geom.Identity, 0)

	p.Blocks = run.blocks(e.opt)
	p.Rules = run.rules
	return p, nil
}

// pageBox returns the page's visible area: /CropBox when present, otherwise
// /MediaBox.
//
// The crop box is what a viewer shows and what the coverage rule must measure
// against — a media box far larger than the crop box would report a page of prose
// as mostly empty and route it to OCR. Both are already inherited from ancestors
// by objects.Store.Page.
func (e *Extractor) pageBox(page objects.Dict) geom.Rect {
	if r, ok := e.rect(page, "CropBox"); ok {
		return r
	}
	if r, ok := e.rect(page, "MediaBox"); ok {
		return r
	}
	// US Letter. A page with neither box is malformed; assuming the most common
	// size keeps coverage meaningful, where a zero box would make every page report
	// no coverage and send an entire readable document to the rasterizer.
	return geom.NewRect(0, 0, 612, 792)
}

func (e *Extractor) rect(d objects.Dict, key objects.Name) (geom.Rect, bool) {
	arr, ok := objects.GetArray(e.s, d, key)
	if !ok || len(arr) != 4 {
		return geom.Rect{}, false
	}
	var v [4]float64
	for i := range v {
		r, err := e.s.Resolve(arr[i])
		if err != nil {
			return geom.Rect{}, false
		}
		f, ok := objects.AsNum(r)
		if !ok {
			return geom.Rect{}, false
		}
		v[i] = f
	}
	out := geom.NewRect(v[0], v[1], v[2], v[3])
	if out.IsZero() {
		return geom.Rect{}, false
	}
	return out, true
}

// metadata reads the document information dictionary and the catalog.
func (e *Extractor) metadata() doc.Metadata {
	m := doc.Metadata{
		Version:   e.s.Version(),
		Encrypted: e.s.Encrypted(),
	}

	if tr, err := e.s.Trailer(); err == nil {
		if info, ok := objects.GetDict(e.s, tr, "Info"); ok {
			m.Title = text(e.s, info, "Title")
			m.Author = text(e.s, info, "Author")
			m.Subject = text(e.s, info, "Subject")
			m.Keywords = text(e.s, info, "Keywords")
			m.Creator = text(e.s, info, "Creator")
			m.Producer = text(e.s, info, "Producer")
			m.Created = text(e.s, info, "CreationDate")
			m.Modified = text(e.s, info, "ModDate")
		}
	}

	if cat, err := e.s.Catalog(); err == nil {
		m.Lang = text(e.s, cat, "Lang")
		// Tagged means a structure tree exists. Whether it is usable is a separate
		// question that probe answers by counting its elements; this records only
		// what the file claims, so a sink can report the path that ran.
		_, m.Tagged = objects.GetDict(e.s, cat, "StructTreeRoot")
	}
	return m
}

func text(s objects.Store, d objects.Dict, key objects.Name) string {
	v, ok := objects.Get(s, d, key)
	if !ok {
		return ""
	}
	return objects.DecodeTextString(v)
}

// loadFont returns the font a resource name refers to, loading it once.
//
// A resource dictionary that names a font it does not define is malformed and
// common; the nil return is handled by the caller, which skips the text rather
// than the page.
func (e *Extractor) loadFont(res objects.Dict, name content.Name) *font.Font {
	fonts, ok := objects.GetDict(e.s, res, "Font")
	if !ok {
		return nil
	}
	raw, ok := fonts[objects.Name(name)]
	if !ok {
		return nil
	}
	if ref, isRef := raw.(objects.Ref); isRef {
		if f, hit := e.fonts[ref]; hit {
			return f
		}
		v, err := e.s.Resolve(ref)
		if err != nil {
			return nil
		}
		d, isDict := v.(objects.Dict)
		if !isDict {
			return nil
		}
		f := font.Load(e.s, d)
		e.fonts[ref] = f
		return f
	}

	// An inline font dictionary cannot be cached: nothing identifies it, so two
	// identical ones are indistinguishable from the same one used twice. Loading
	// per reference is the cost of that, and inline font dictionaries are rare.
	d, isDict := raw.(objects.Dict)
	if !isDict {
		return nil
	}
	return font.Load(e.s, d)
}

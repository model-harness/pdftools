package extract

import (
	"math"
	"strings"

	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// link is one of a page's links to a URI: the areas it covers on the page and where it points.
type link struct {
	areas []geom.Rect
	uri   string
}

// links reads a page's /Link annotations whose action opens a URI (ADR 0041). Its areas are its
// /QuadPoints, one rectangle per quadrilateral, since a link that wraps covers two lines and its
// /Rect the space between them; or its /Rect where it gives none.
//
// Only a URI a reader can follow and a page can safely send them to: http, https, mailto and
// ftp. A javascript: or file: URI is a producer's instruction to a viewer, and written into
// Markdown it is a link a model or a renderer would act on. A link to a place in the document
// is left as its text, since a heading's anchor is the sink's to name.
func (e *Extractor) links(page objects.Dict) []link {
	annots, _ := objects.GetArray(e.s, page, "Annots")
	var out []link
	for _, a := range annots {
		o, err := e.s.Resolve(a)
		if err != nil {
			continue
		}
		d, ok := o.(objects.Dict)
		if !ok {
			continue
		}
		if st, _ := objects.GetName(e.s, d, "Subtype"); st != "Link" {
			continue
		}
		act, _ := objects.GetDict(e.s, d, "A")
		if kind, _ := objects.GetName(e.s, act, "S"); kind != "URI" {
			continue
		}
		uri := e.text(act["URI"])
		if !followable(uri) {
			continue
		}
		l := link{uri: uri}
		if q, ok := objects.GetArray(e.s, d, "QuadPoints"); ok && len(q) >= 8 && len(q)%8 == 0 {
			for i := 0; i < len(q); i += 8 {
				r := geom.Rect{X0: math.Inf(1), Y0: math.Inf(1), X1: math.Inf(-1), Y1: math.Inf(-1)}
				for j := 0; j < 8; j += 2 {
					x, y := e.num(q[i+j]), e.num(q[i+j+1])
					r = geom.Rect{X0: math.Min(r.X0, x), Y0: math.Min(r.Y0, y), X1: math.Max(r.X1, x), Y1: math.Max(r.Y1, y)}
				}
				l.areas = append(l.areas, r)
			}
		} else if rect, ok := objects.GetArray(e.s, d, "Rect"); ok && len(rect) == 4 {
			x0, y0, x1, y1 := e.num(rect[0]), e.num(rect[1]), e.num(rect[2]), e.num(rect[3])
			l.areas = append(l.areas, geom.NewRect(x0, y0, x1, y1))
		}
		out = append(out, l)
	}
	return out
}

// followable reports whether a URI is one this package writes: a scheme a reader follows to a
// page, mail or a file server, and nothing that runs.
func followable(uri string) bool {
	u := strings.ToLower(strings.TrimSpace(uri))
	for _, scheme := range []string{"http://", "https://", "mailto:", "ftp://"} {
		if strings.HasPrefix(u, scheme) && len(u) > len(scheme) {
			return true
		}
	}
	return false
}

func (e *Extractor) num(o objects.Object) float64 {
	o, _ = e.s.Resolve(o)
	switch v := o.(type) {
	case objects.Int:
		return float64(v)
	case objects.Real:
		return float64(v)
	}
	return 0
}

func (e *Extractor) text(o objects.Object) string {
	o, _ = e.s.Resolve(o)
	if s, ok := o.(objects.String); ok {
		return strings.TrimSpace(string(s))
	}
	return ""
}

// linkWords gives each word that half lies in a link's area that link, splitting a fragment
// where a word in one link meets a word in another or in none. A word's height is checked at
// its middle, a third of the type's size above the baseline, so a link drawn on the line above
// does not reach it.
func (r *run) linkWords() {
	for i := range r.lines {
		ln := &r.lines[i]
		if ln.orient != 0 {
			continue
		}
		var out []frag
		for f := range ln.frags {
			fr := &ln.frags[f]
			mid := fr.cross + fr.height/3
			out = append(out, splitWords(fr, func(x0, x1 float64) string {
				for _, l := range r.links {
					got := 0.0
					for _, a := range l.areas {
						if mid >= a.Y0 && mid <= a.Y1 {
							got += math.Max(0, math.Min(a.X1, x1)-math.Max(a.X0, x0))
						}
					}
					if x1 > x0 && got >= 0.5*(x1-x0) {
						return l.uri
					}
				}
				return ""
			}, func(p *frag, key string) { p.link = key })...)
		}
		ln.frags = out
	}
}

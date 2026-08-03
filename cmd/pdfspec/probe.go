package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/3rg0n/pdf-spec/objects"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
	"github.com/3rg0n/pdf-spec/tag"
)

// report is what probe knows about one file.
//
// probe exists because the first question about any PDF is which path it will
// take through the pipeline and why. Answering that without reading source is
// what makes the corpus measurable, and it is the harness the extraction
// benchmarks hang off.
type report struct {
	File   string  `json:"file"`
	SizeMB float64 `json:"size_mb"`
	Err    string  `json:"error,omitempty"`

	Version   string `json:"version"`
	Pages     int    `json:"pages"`
	Encrypted bool   `json:"encrypted"`

	ObjectStreams bool `json:"object_streams"`
	XRefStreams   bool `json:"xref_streams"`
	Linearized    bool `json:"linearized"`
	Hybrid        bool `json:"hybrid"`

	Tagged     bool        `json:"tagged"`
	MarkInfo   bool        `json:"mark_info"`
	StructRoot bool        `json:"struct_tree_root"`
	Tags       *tagSummary `json:"tags,omitempty"`

	Lang string `json:"lang,omitempty"`

	Filters []string `json:"filters,omitempty"`
	Fonts   []string `json:"fonts,omitempty"`
	Images  int      `json:"images"`

	// Path is the pipeline route this file will take.
	Path string `json:"path"`

	ProbeMS int64 `json:"probe_ms"`

	PageDetail []pageInfo `json:"page_detail,omitempty"`
}

type tagSummary struct {
	Elements int            `json:"elements"`
	Headings int            `json:"headings"`
	Paras    int            `json:"paragraphs"`
	Tables   int            `json:"tables"`
	Figures  int            `json:"figures"`
	Lists    int            `json:"lists"`
	MCIDs    int            `json:"mcids"`
	MaxDepth int            `json:"max_depth"`
	TopRoles map[string]int `json:"top_roles,omitempty"`
}

type pageInfo struct {
	Page       int     `json:"page"`
	WidthPt    float64 `json:"width_pt"`
	HeightPt   float64 `json:"height_pt"`
	Rotate     int     `json:"rotate"`
	ContentLen int     `json:"content_len"`
	Fonts      int     `json:"fonts"`
	XObjects   int     `json:"xobjects"`
}

func probe(files []string, asJSON, wantPages bool) error {
	reports := make([]report, 0, len(files))
	for _, f := range files {
		reports = append(reports, probeOne(f, wantPages))
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(reports)
	}
	printReports(reports)

	for _, r := range reports {
		if r.Err != "" {
			return fmt.Errorf("%d of %d file(s) failed", countErrs(reports), len(reports))
		}
	}
	return nil
}

func countErrs(rs []report) int {
	n := 0
	for _, r := range rs {
		if r.Err != "" {
			n++
		}
	}
	return n
}

func probeOne(path string, wantPages bool) report {
	start := time.Now()
	r := report{File: filepath.Base(path), Path: "unknown"}

	if fi, err := os.Stat(path); err == nil {
		r.SizeMB = float64(fi.Size()) / (1 << 20)
	}

	s, err := pcstore.Open(path)
	if err != nil {
		r.Err = err.Error()
		r.ProbeMS = time.Since(start).Milliseconds()
		return r
	}
	defer s.Close()

	r.Version = s.Version()
	r.Pages = s.PageCount()
	r.Encrypted = s.Encrypted()

	if st, ok := s.(pcstore.Statser); ok {
		x := st.Stats()
		r.ObjectStreams = x.UsingObjectStreams
		r.XRefStreams = x.UsingXRefStreams
		r.Linearized = x.Linearized
		r.Hybrid = x.Hybrid
	}

	if cat, err := s.Catalog(); err == nil {
		_, r.StructRoot = objects.GetDict(s, cat, "StructTreeRoot")
		if mi, ok := objects.GetDict(s, cat, "MarkInfo"); ok {
			r.MarkInfo, _ = objects.GetBool(s, mi, "Marked")
		}
		if v, ok := objects.Get(s, cat, "Lang"); ok {
			r.Lang = objects.DecodeTextString(v)
		}
	}

	if t, err := tag.Read(s); err == nil && t != nil {
		st := t.Stats()
		// A near-empty structure tree is a producer stub, not usable structure.
		// Requiring some headings or paragraphs keeps probe honest about which
		// path will actually work.
		r.Tagged = st.Headings > 0 || st.Paras > 0
		r.Tags = &tagSummary{
			Elements: st.Elements,
			Headings: st.Headings,
			Paras:    st.Paras,
			Tables:   st.Tables,
			Figures:  st.Figures,
			Lists:    st.Lists,
			MCIDs:    st.MCIDs,
			MaxDepth: st.MaxDepth,
			TopRoles: topRoles(st.Roles, 6),
		}
	}

	r.Filters, r.Fonts, r.Images, r.PageDetail = scanPages(s, wantPages)

	switch {
	case r.Encrypted:
		r.Path = "encrypted"
	case r.Tagged:
		r.Path = "tagged"
	case len(r.Fonts) > 0:
		r.Path = "layout"
	default:
		r.Path = "ocr"
	}

	r.ProbeMS = time.Since(start).Milliseconds()
	return r
}

// scanPages walks every page for the resources that decide the extraction path:
// which filters must be implemented, which font types must be handled, and
// whether there is any text at all.
func scanPages(s objects.Store, wantPages bool) (filters, fonts []string, images int, detail []pageInfo) {
	sc := &scanner{
		s:       s,
		filters: map[string]bool{},
		fonts:   map[string]bool{},
		seen:    map[objects.Ref]bool{},
	}

	for n := 1; n <= s.PageCount(); n++ {
		page, err := s.Page(n)
		if err != nil {
			continue
		}

		var pi pageInfo
		pi.Page = n
		if mb, ok := objects.GetArray(s, page, "MediaBox"); ok && len(mb) == 4 {
			x0, _ := objects.AsNum(mb[0])
			y0, _ := objects.AsNum(mb[1])
			x1, _ := objects.AsNum(mb[2])
			y1, _ := objects.AsNum(mb[3])
			pi.WidthPt, pi.HeightPt = abs(x1-x0), abs(y1-y0)
		}
		if rot, ok := objects.GetInt(s, page, "Rotate"); ok {
			pi.Rotate = int(rot)
		}
		if bb, err := s.PageContent(n); err == nil {
			pi.ContentLen = len(bb)
		}

		if res, ok := objects.GetDict(s, page, "Resources"); ok {
			sc.walk(res, &pi, 0)
		}

		if wantPages {
			detail = append(detail, pi)
		}
	}

	return sortedKeys(sc.filters), sortedKeys(sc.fonts), sc.images, detail
}

// scanner accumulates the resource facts across a document.
//
// It carries state across pages for one reason: images are deduplicated by indirect
// reference, so one XObject drawn on 1,023 pages counts once. That is the rule
// image.Reader applies, and without it probe would report thousands of images for
// the specification and disagree with the images verb about the same file.
type scanner struct {
	s       objects.Store
	filters map[string]bool
	fonts   map[string]bool
	seen    map[objects.Ref]bool
	images  int
}

// maxFormDepth bounds recursion into Form XObjects. Same value and same reason as in
// image and extract: the seen set stops any cycle reached by reference, and this is
// the backstop for a chain of direct objects, which no producer emits but a hostile
// file may.
const maxFormDepth = 8

// walk records a resource dictionary's fonts and XObjects, recursing into Form
// XObjects.
//
// The recursion is load-bearing rather than defensive. A form's resources are not the
// page's: 39 of ISO 32000-2's fonts and 7 of its images are reachable only through
// one, text-find-ligatures.pdf keeps its real font two forms deep, and
// test_delete_image.pdf keeps its only image there. A page-level scan reports those
// files as having no fonts at all, which routes them to the OCR path — and probe's
// entire output is that routing decision, so a shallow scan does not merely
// undercount, it answers the question wrongly.
//
// pi is only filled at depth 0: the per-page counts are "what this page's own
// resource dictionary names", which is what a reader comparing them against the file
// expects, and summing nested forms into them would make the column mean something
// different per row.
func (sc *scanner) walk(res objects.Dict, pi *pageInfo, depth int) {
	if res == nil || depth >= maxFormDepth {
		return
	}
	if fd, ok := objects.GetDict(sc.s, res, "Font"); ok {
		if depth == 0 {
			pi.Fonts = len(fd)
		}
		for _, v := range fd {
			f, err := sc.s.Resolve(v)
			if err != nil {
				continue
			}
			fdict, isDict := f.(objects.Dict)
			if !isDict {
				continue
			}
			if sub, ok := objects.GetName(sc.s, fdict, "Subtype"); ok {
				sc.fonts[string(sub)] = true
			}
		}
	}
	xo, ok := objects.GetDict(sc.s, res, "XObject")
	if !ok {
		return
	}
	if depth == 0 {
		pi.XObjects = len(xo)
	}
	for _, v := range xo {
		ref, isRef := v.(objects.Ref)
		if isRef {
			if sc.seen[ref] {
				continue
			}
			sc.seen[ref] = true
		}
		x, err := sc.s.Resolve(v)
		if err != nil {
			continue
		}
		st, isStream := x.(*objects.Stream)
		if !isStream {
			continue
		}
		for _, f := range st.Filters {
			sc.filters[string(f)] = true
		}
		switch sub, _ := objects.GetName(sc.s, st.Dict, "Subtype"); sub {
		case "Image":
			sc.images++
		case "Form":
			// A form with no /Resources inherits the invoking dictionary's
			// (§8.10.1), which is how anything inside such a form is reached at all.
			inner := res
			if d, ok := objects.GetDict(sc.s, st.Dict, "Resources"); ok {
				inner = d
			}
			sc.walk(inner, pi, depth+1)
		}
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func topRoles(m map[tag.Role]int, n int) map[string]int {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(m))
	for k, v := range m {
		all = append(all, kv{string(k), v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	if len(all) > n {
		all = all[:n]
	}
	out := make(map[string]int, len(all))
	for _, e := range all {
		out[e.k] = e.v
	}
	return out
}

func printReports(rs []report) {
	for i, r := range rs {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s\n", r.File)
		if r.Err != "" {
			fmt.Printf("  error       %s\n", r.Err)
			continue
		}
		fmt.Printf("  size        %.2f MB\n", r.SizeMB)
		fmt.Printf("  version     PDF %s\n", r.Version)
		fmt.Printf("  pages       %d\n", r.Pages)
		fmt.Printf("  encrypted   %v\n", r.Encrypted)
		fmt.Printf("  structure   %s\n", structureLine(r))
		if r.Tags != nil {
			t := r.Tags
			fmt.Printf("  tags        %d elements, depth %d, %d MCIDs\n", t.Elements, t.MaxDepth, t.MCIDs)
			fmt.Printf("              %d headings, %d paras, %d tables, %d figures, %d lists\n",
				t.Headings, t.Paras, t.Tables, t.Figures, t.Lists)
			if len(t.TopRoles) > 0 {
				fmt.Printf("  roles       %s\n", fmtRoles(t.TopRoles))
			}
		}
		if r.Lang != "" {
			fmt.Printf("  lang        %s\n", r.Lang)
		}
		fmt.Printf("  streams     %s\n", streamsLine(r))
		if len(r.Fonts) > 0 {
			fmt.Printf("  fonts       %s\n", strings.Join(r.Fonts, ", "))
		} else {
			fmt.Printf("  fonts       none\n")
		}
		if len(r.Filters) > 0 {
			fmt.Printf("  filters     %s\n", strings.Join(r.Filters, ", "))
		}
		fmt.Printf("  images      %d\n", r.Images)
		fmt.Printf("  path        %s\n", r.Path)
		fmt.Printf("  probed in   %d ms\n", r.ProbeMS)

		if len(r.PageDetail) > 0 {
			fmt.Printf("  %-6s %-9s %-7s %-9s %-6s %s\n", "page", "size(pt)", "rotate", "content", "fonts", "xobj")
			for _, p := range r.PageDetail {
				fmt.Printf("  %-6d %-9s %-7d %-9d %-6d %d\n",
					p.Page, fmt.Sprintf("%.0fx%.0f", p.WidthPt, p.HeightPt),
					p.Rotate, p.ContentLen, p.Fonts, p.XObjects)
			}
		}
	}
}

func structureLine(r report) string {
	var parts []string
	if r.StructRoot {
		parts = append(parts, "StructTreeRoot")
	}
	if r.MarkInfo {
		parts = append(parts, "Marked")
	}
	if len(parts) == 0 {
		return "untagged"
	}
	s := strings.Join(parts, " + ")
	if !r.Tagged {
		s += " (stub, unusable)"
	}
	return s
}

func streamsLine(r report) string {
	var parts []string
	if r.ObjectStreams {
		parts = append(parts, "ObjStm")
	}
	if r.XRefStreams {
		parts = append(parts, "XRefStm")
	}
	if r.Linearized {
		parts = append(parts, "linearized")
	}
	if r.Hybrid {
		parts = append(parts, "hybrid")
	}
	if len(parts) == 0 {
		return "plain xref table"
	}
	return strings.Join(parts, ", ")
}

func fmtRoles(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(m))
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	parts := make([]string, 0, len(all))
	for _, e := range all {
		parts = append(parts, fmt.Sprintf("%s=%d", e.k, e.v))
	}
	return strings.Join(parts, " ")
}

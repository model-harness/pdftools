// Package liberation supplies substitute font programs for documents that embed none.
//
// # What is here and why these faces
//
// Eight faces of Liberation 2.1.5 — Sans and Serif, each in regular, bold, italic and bold
// italic — which stand in for 8 of the standard 14 fonts and for any non-embedded Arial or Times.
// Liberation is *metric-compatible* with Arial, Times New Roman and Courier New by construction,
// which is the property that matters most here and is worth more than the glyph shapes: a
// standard-14 font may omit /Widths entirely, and where it does the advances come from the
// program, so a stand-in with different metrics would reflow the line. Where /Widths is present
// §9.2.4 makes it authoritative and the substitute's metrics are irrelevant.
//
// Monospaced is deliberately absent. Courier is requested on 0 of the corpus's 1,251 pages, and
// the three Mono faces cost 686 KB gzipped; a page asking for one is refused with a reason naming
// the missing family, which is honest and one line from being fixed when a document needs it.
// Symbol and ZapfDingbats are not here and will not be: no text face can stand in for a font
// whose codes are not characters, and font.FamilySymbol exists so that such a request is declined
// rather than answered with Latin letters.
//
// # Why gzipped, and why not subsetted
//
// The faces carry full Unicode coverage — Cyrillic, Greek, Hebrew, extended Latin — and weigh
// 3.17 MB raw against 1.69 MB gzipped, so they are stored compressed and inflated on first use.
// Subsetting them to Latin would be smaller still and is not done, because the OFL reserves the
// name: a modified Liberation may not be called Liberation, so subsetting would mean renaming the
// faces and shipping something no longer traceable to its upstream. Compression leaves the bytes
// identical.
//
// # Licensing
//
// SIL Open Font License 1.1, reproduced verbatim in faces/LICENSE along with faces/AUTHORS. The
// OFL is a redistribution licence and imposes nothing on this repository's own MIT terms; what it
// does require is that the licence travel with the fonts and that the Reserved Font Name not be
// applied to a modified version, both of which hold here.
//
// # Why internal
//
// So that no external importer pays for these bytes. render/native takes a FaceSource interface
// and refuses without one, because which face substitutes for Helvetica is policy rather than
// parsing; this package is the policy cmd/pdfspec chooses, and an internal package cannot become
// part of the module's surface by accident.
package liberation

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"sync"

	"github.com/model-harness/pdftools/font"
)

//go:embed faces/*.ttf.gz faces/LICENSE faces/AUTHORS
var faces embed.FS

// Notice is the SIL Open Font License and the AUTHORS file, verbatim.
//
// Embedded and exported rather than only committed beside the fonts, because the OFL requires the
// licence to accompany the Font Software wherever it is redistributed — and a single static binary
// with the faces compiled in *is* a redistribution. A licence file sitting in the source tree
// satisfies that for a clone and not for the binary someone downloads, so `pdfspec version` prints
// this. It is the one piece of compliance that cannot be met by a file in the repository.
func Notice() string {
	lic, _ := faces.ReadFile("faces/LICENSE")
	authors, _ := faces.ReadFile("faces/AUTHORS")
	return "Liberation fonts 2.1.5 (https://github.com/liberationfonts/liberation-fonts)\n" +
		"embedded in this binary to substitute for fonts a document does not embed.\n\n" +
		string(authors) + "\n" + string(lic)
}

// Source resolves a font.Style to one of the embedded faces.
//
// Safe for concurrent use, and it has to be: render.Rasterizer implementations are not, so a
// caller rendering pages in parallel holds one rasterizer per worker — and they would all share
// one of these.
type Source struct {
	mu     sync.Mutex
	parsed map[string]*font.TrueType
}

// New returns a Source. Nothing is read or parsed until a page asks for a face.
func New() *Source { return &Source{parsed: map[string]*font.TrueType{}} }

var _ font.FaceSource = (*Source)(nil)

// Face returns the embedded program for the requested style.
//
// The style decides, not the name: a request for "ArialMT" and one for "Helvetica" both resolve
// to Liberation Sans, because that is what metric compatibility means and the name adds nothing
// this Source can act on. A caller holding the genuine Arial would write its own FaceSource and
// use the name — which is why FaceRequest carries it.
func (s *Source) Face(req font.FaceRequest) (*font.TrueType, bool) {
	name, ok := fileFor(req.Style)
	if !ok {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if tt, ok := s.parsed[name]; ok {
		return tt, true
	}
	tt, err := load(name)
	if err != nil {
		// An embedded asset that does not inflate or parse is a build problem, not a document
		// problem, and there is no page-level recovery for it: declining leaves the page refused
		// with a reason that names the face, which is the same outcome as not carrying it.
		return nil, false
	}
	s.parsed[name] = tt
	return tt, true
}

// fileFor maps a style to an embedded file, or reports that none stands in for it.
func fileFor(st font.Style) (string, bool) {
	var fam string
	switch st.Family {
	case font.FamilySans:
		fam = "Sans"
	case font.FamilySerif:
		fam = "Serif"
	default:
		// Mono is not carried, and Symbol never will be. Both decline rather than fall back to
		// Sans: a fixed-pitch page set in a proportional face reflows every column, and a symbol
		// page set in a text face says something different from what it shows.
		return "", false
	}
	style := "Regular"
	switch {
	case st.Bold && st.Italic:
		style = "BoldItalic"
	case st.Bold:
		style = "Bold"
	case st.Italic:
		style = "Italic"
	}
	return fmt.Sprintf("faces/Liberation%s-%s.ttf.gz", fam, style), true
}

// load inflates and parses one embedded face.
func load(name string) (*font.TrueType, error) {
	gz, err := faces.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("liberation: %s: %w", name, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("liberation: %s: %w", name, err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("liberation: %s: %w", name, err)
	}
	if err := zr.Close(); err != nil {
		return nil, fmt.Errorf("liberation: %s: %w", name, err)
	}
	return font.ParseTrueType(raw)
}

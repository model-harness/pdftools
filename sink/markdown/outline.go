package markdown

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/model-harness/pdftools/doc"
)

// WriteOutline emits a reconstructed outline: the preamble, then every section as a
// heading followed by its own content, depth first.
//
// This is the same sink as Write with one thing added — the headings. Write emits page
// after page because that is all a doc.Document knows; a doc.Outline knows which text
// is a clause title and at what rank, so the output gains a document outline and loses
// the page boundaries, which were never meaningful in prose. A paragraph continuing
// across a page break is one paragraph.
//
// Section titles are emitted as they were resolved, clause number included. Splitting
// "7.5.8" back off into a separate construct would be a numbering scheme this package
// invented; the number is already in doc.Section.Number for a consumer that wants it
// structurally, and a reader wants to see it in the heading.
func WriteOutline(w io.Writer, o *doc.Outline, opt Options) error {
	bw := bufio.NewWriter(w)
	mw := &writer{w: bw}

	if opt.Frontmatter {
		mw.frontmatter(o.Meta, 0, pageSpan(o))
	}
	for _, b := range o.Preamble {
		mw.emit(b, opt)
	}
	o.Walk(func(s *doc.Section) bool {
		mw.section(s, opt)
		return true
	})
	mw.unplaced(o.Unplaced, opt)

	if err := mw.err; err != nil {
		return err
	}
	return bw.Flush()
}

// OutlineString renders an outline to a string, for tests and in-process consumers.
func OutlineString(o *doc.Outline, opt Options) string {
	var sb strings.Builder
	// A strings.Builder cannot fail, so the error is structurally nil.
	_ = WriteOutline(&sb, o, opt)
	return sb.String()
}

// section emits one section's heading and its own blocks, excluding its subsections —
// Outline.Walk visits those, so recursing here would emit every clause twice.
func (w *writer) section(s *doc.Section, opt Options) {
	if s.Title != "" {
		w.block(doc.Block{
			Role:  doc.RoleHeading,
			Level: s.Level,
			Spans: []doc.Span{{Text: s.Title, MCID: -1}},
		})
	}
	for _, b := range s.Blocks {
		w.emit(b, opt)
	}
}

// emit writes a block unless it would render as nothing.
func (w *writer) emit(b doc.Block, opt Options) {
	if b.Role == doc.RoleArtifact && !opt.Artifacts {
		return
	}
	if b.IsEmpty() {
		return
	}
	w.block(b)
}

// unplaced writes the text no section claimed, each page behind an HTML comment naming
// where it came from.
//
// It is emitted rather than dropped because dropping content is never the right default
// for an extractor: ISO 32000-2 draws the whole of clause 1 outside any marked-content
// sequence, so no structure element names it, and a conversion missing a standard's
// Scope clause is a conversion nobody can rely on.
//
// A comment rather than a heading, and last rather than interleaved. A heading would
// put a clause in the outline that no heading in the document corresponds to, and
// interleaving by page would file the text under whichever clause happened to be open
// there — attributing the Scope to "0.4 Changes introduced in ISO 32000-2:2020" is
// worse than leaving it unattributed, because a wrong attribution is one a later reader
// takes as fact. The comment renders as nothing and greps as something.
func (w *writer) unplaced(pages []doc.Page, opt Options) {
	for i := range pages {
		p := pages[i]
		var any bool
		for j := range p.Blocks {
			b := p.Blocks[j]
			if b.Role == doc.RoleArtifact && !opt.Artifacts {
				continue
			}
			if b.IsEmpty() {
				continue
			}
			if !any {
				w.gap(false)
				w.str(fmt.Sprintf(
					"<!-- pdfspec: text on page %d belongs to no clause in the structure tree -->",
					p.Number))
				w.nl()
				w.blank = false
				w.lastList = false
				any = true
			}
			w.block(b)
		}
	}
}

// pageSpan is the highest page any content reached, for the frontmatter's page count.
// Taken from the content rather than from the document because an Outline does not
// carry a page count, and a count that disagreed with the pages actually cited would be
// worse than one derived from them.
func pageSpan(o *doc.Outline) int {
	n := 0
	o.Walk(func(s *doc.Section) bool {
		if s.LastPage > n {
			n = s.LastPage
		}
		return true
	})
	for i := range o.Unplaced {
		if o.Unplaced[i].Number > n {
			n = o.Unplaced[i].Number
		}
	}
	return n
}

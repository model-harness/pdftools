package doc

import "strings"

// Section is one clause of a document: a heading, the content under it, and the
// subsections nested beneath.
//
// The shape here follows a measurement rather than an expectation. An earlier draft
// of docs/DESIGN.md had a clause as "one contiguous subtree", which would have made
// Section a thin view over the structure tree. ISO 32000-2 has 7 Sect elements
// against 981 headings, with a single Part holding 13,442 direct children in a flat
// H1 P P P … stream, so 966 of those headings have no element children at all: a
// clause's body is its heading's *following siblings*, not its descendants. Section
// is therefore a real tree that sectionize builds, not a projection of one that
// already exists.
//
// Two consequences visible in the fields. Blocks holds the content directly rather
// than referencing pages, because a clause has no page — clause 7.5.8 running from
// page 412 to 414 is one section whose blocks came from three pages, and there is
// nothing to stitch since the structure tree was never page-scoped to begin with.
// And Pages records where the content was found rather than where the section
// "is", because that is a range and a reader checking a conversion needs it.
type Section struct {
	// Title is the heading text. It is resolved from page content joined on
	// (page, MCID), not from the structure element's /T: every heading in ISO
	// 32000-2 has an empty /T, so /T is an optimization when present and never the
	// source of truth.
	Title string

	// Level is the heading depth, 1-based. It comes from the declared heading role
	// — H1..H6, or a bare H's nesting depth per ISO 32000-2 §14.8.4.4 — on the
	// tagged path, and from font-size clustering on the untagged one. It is the
	// hierarchy: a section's parent is the nearest preceding section of lower level.
	Level int

	// Number is the clause number parsed off the front of the title, "7.5.8" for
	// "7.5.8 Filters", or empty when the title does not begin with one. It is kept
	// separate because it is what a cross-reference names and what a stable URI is
	// built from, and recovering it from the title at every use would mean parsing
	// it in several places.
	Number string

	// Blocks are the section's own content in reading order, excluding the heading
	// itself and excluding everything belonging to a subsection.
	Blocks []Block

	// Kids are subsections in document order.
	Kids []*Section

	// Parent is the enclosing section, nil at the top level.
	Parent *Section

	// Pages is the 1-based page range the section's content was found on, inclusive.
	// Both are 0 for a section whose content could not be anchored to any page. This
	// is a range and not a number on purpose: the median clause fits on one page and
	// the ones that matter do not.
	FirstPage, LastPage int
}

// Text returns the section's own blocks as text, one block per line, excluding
// subsections. A blank line separates blocks, matching what a sink would emit, so
// that a description extracted from this reads the same as the rendered document.
func (s *Section) Text() string {
	var sb strings.Builder
	for i := range s.Blocks {
		if s.Blocks[i].IsEmpty() {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		s.Blocks[i].writeText(&sb)
	}
	return sb.String()
}

// Walk calls fn for every section in this subtree in document order, depth first,
// including the receiver. A fn returning false stops the traversal.
func (s *Section) Walk(fn func(*Section) bool) bool {
	if s == nil {
		return true
	}
	if !fn(s) {
		return false
	}
	for _, k := range s.Kids {
		if !k.Walk(fn) {
			return false
		}
	}
	return true
}

// Path returns the section's ancestry titles from the top level down to and
// including its own, which is what OKF tags and a breadcrumb both need.
func (s *Section) Path() []string {
	if s == nil {
		return nil
	}
	var up []*Section
	for n := s; n != nil; n = n.Parent {
		up = append(up, n)
	}
	out := make([]string, 0, len(up))
	for i := len(up) - 1; i >= 0; i-- {
		out = append(out, up[i].Title)
	}
	return out
}

// Outline is a document's sections: the top-level ones, in document order.
//
// A slice rather than a synthetic root, because a document does not have one
// heading at the top. ISO 32000-2 opens with Foreword and Introduction at the same
// level as clause 1, and inventing a root to hold them would put a section in the
// tree that no heading in the file corresponds to — which a clause-per-file sink
// would then have to emit or special-case.
type Outline struct {
	// Meta is the source document's metadata, carried so a sink emitting one file
	// per clause can attribute each of them without also being handed the Document.
	Meta Metadata

	// Sections are the top-level sections in document order.
	Sections []*Section

	// Preamble holds content that appeared before the first heading — a title page,
	// a copyright notice. It is kept rather than dropped because dropping content is
	// never the right default for an extractor, and it is separate from Sections
	// because it belongs to no clause.
	Preamble []Block

	// Unplaced holds text the reconstruction could not attribute to any section,
	// grouped by the page it was found on. It is Pages rather than Blocks because
	// without the page number this content cannot be checked against the original,
	// and Page is the type that already carries one.
	//
	// It exists because the alternative to keeping this is one of two worse things.
	// ISO 32000-2 draws the whole of clause 1 outside any marked-content sequence, so
	// no structure element names it: dropping it loses a normative clause, and
	// attaching it to the nearest preceding section files the Scope under "0.4
	// Changes introduced in ISO 32000-2:2020" — a wrong attribution in a bundle a
	// model will later read as fact, which is worse than an unattributed one.
	// Measured at 0.23% of the specification's text and 0% of ISO/TS 32005.
	//
	// Recovering such content as a *clause* needs a heading that the tree does not
	// contain, which is the layout path's job and not this one's.
	Unplaced []Page
}

// Walk calls fn for every section in the outline in document order, depth first.
func (o *Outline) Walk(fn func(*Section) bool) {
	if o == nil {
		return
	}
	for _, s := range o.Sections {
		if !s.Walk(fn) {
			return
		}
	}
}

// Count returns the total number of sections at every level, which is the
// acceptance measurement for the tagged path: docs/DESIGN.md §8 puts ISO 32000-2 at
// roughly 981, and a run producing single digits has reverted to container-driven
// segmentation.
func (o *Outline) Count() int {
	n := 0
	o.Walk(func(*Section) bool { n++; return true })
	return n
}

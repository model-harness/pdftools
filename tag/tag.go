// Package tag reads a PDF's logical structure tree: the /StructTreeRoot graph
// described in ISO 32000-2 §14.7.
//
// This package is why the toolkit can produce sectioned Markdown without layout
// heuristics. A structure tree states the document's heading hierarchy and
// reading order outright, and it is not page-scoped, so a section spanning
// several pages is one contiguous subtree. Where a tree exists, walking it
// beats inferring structure from font sizes and coordinates.
//
// Untagged files get nothing from this package; they take the layout path.
package tag

import (
	"fmt"

	"github.com/3rg0n/pdf-spec/objects"
)

// Role is a normalized structure element type.
//
// PDF lets a document define arbitrary element names and map them onto the
// standard set through the catalog's /RoleMap, so the raw /S value cannot be
// trusted. Callers see normalized roles; the original is kept in Elem.RawType.
type Role string

// Standard structure types from ISO 32000-2 §14.8.4. Only those meaningful to
// text extraction are named; anything else keeps its raw value.
const (
	RoleDocument   Role = "Document"
	RolePart       Role = "Part"
	RoleArt        Role = "Art"
	RoleSect       Role = "Sect"
	RoleDiv        Role = "Div"
	RoleH          Role = "H"
	RoleH1         Role = "H1"
	RoleH2         Role = "H2"
	RoleH3         Role = "H3"
	RoleH4         Role = "H4"
	RoleH5         Role = "H5"
	RoleH6         Role = "H6"
	RoleP          Role = "P"
	RoleL          Role = "L"
	RoleLI         Role = "LI"
	RoleLbl        Role = "Lbl"
	RoleLBody      Role = "LBody"
	RoleTable      Role = "Table"
	RoleTR         Role = "TR"
	RoleTH         Role = "TH"
	RoleTD         Role = "TD"
	RoleTHead      Role = "THead"
	RoleTBody      Role = "TBody"
	RoleTFoot      Role = "TFoot"
	RoleSpan       Role = "Span"
	RoleQuote      Role = "Quote"
	RoleNote       Role = "Note"
	RoleCode       Role = "Code"
	RoleFigure     Role = "Figure"
	RoleFormula    Role = "Formula"
	RoleCaption    Role = "Caption"
	RoleTOC        Role = "TOC"
	RoleTOCI       Role = "TOCI"
	RoleLink       Role = "Link"
	RoleArtifact   Role = "Artifact"
	RoleNonStruct  Role = "NonStruct"
	RoleBlockQuote Role = "BlockQuote"
)

// HeadingLevel returns the heading depth for H1..H6, or 0 for anything else.
//
// A bare H is level 0 here, not 1. ISO 32000-2 §14.8.4.4 defines H as a heading
// whose level comes from nesting depth in the structure hierarchy rather than
// from its name, so resolving it needs tree context the Role alone lacks.
// Depth handles it.
func (r Role) HeadingLevel() int {
	switch r {
	case RoleH1:
		return 1
	case RoleH2:
		return 2
	case RoleH3:
		return 3
	case RoleH4:
		return 4
	case RoleH5:
		return 5
	case RoleH6:
		return 6
	}
	return 0
}

// IsHeading reports whether the role denotes a heading, including a bare H.
func (r Role) IsHeading() bool { return r == RoleH || r.HeadingLevel() > 0 }

// Groups whose only job is nesting. They carry no text of their own and are
// transparent when computing a bare H's level.
func (r Role) isGrouping() bool {
	switch r {
	case RoleDocument, RolePart, RoleArt, RoleSect, RoleDiv, RoleNonStruct:
		return true
	}
	return false
}

// Elem is one node of the structure tree.
type Elem struct {
	// Role is the type after /RoleMap normalization.
	Role Role

	// RawType is the /S value as written, before normalization. Kept because a
	// custom role can carry document-specific meaning worth preserving.
	RawType Role

	// Title is /T, a human-readable label. Producers of long specifications
	// often put the clause number and heading here.
	Title string

	// Lang is /Lang if present on this element.
	Lang string

	// ActualText is /ActualText: replacement text for the element, used when
	// the glyphs do not spell what the content means (ligatures, artwork).
	ActualText string

	// Alt is /Alt, alternate description, typically on a Figure.
	Alt string

	// Page is the 1-based page this element is anchored to via /Pg, or 0 when
	// it does not name one. Structure elements need not be page-scoped, which
	// is exactly what lets a section span pages.
	Page int

	// MCIDs are the marked-content identifiers on Page that belong to this
	// element. They are the join key between the structure tree and the text
	// found in content streams.
	MCIDs []int

	// Kids are child elements in logical reading order.
	Kids []*Elem

	// Parent is the enclosing element, nil at the root.
	Parent *Elem

	// pageRef holds the unresolved /Pg reference until page numbers are known.
	// Resolving during the walk would need an object-number-to-page-number map,
	// which costs a full page-tree traversal, so it is deferred to ResolvePages.
	pageRef *objects.Ref
}

// Tree is a document's structure tree.
type Tree struct {
	Root *Elem

	// RoleMap is the catalog's /RoleMap, custom type to standard type.
	RoleMap map[Role]Role
}

// Read returns the structure tree, or nil with a nil error when the document
// has none.
//
// An absent tree is the normal case for most PDFs in the wild and is not a
// failure: it selects the layout path instead. Callers check for nil.
func Read(s objects.Store) (*Tree, error) {
	cat, err := s.Catalog()
	if err != nil {
		return nil, fmt.Errorf("tag: catalog: %w", err)
	}
	rootDict, ok := objects.GetDict(s, cat, "StructTreeRoot")
	if !ok {
		return nil, nil
	}

	t := &Tree{RoleMap: readRoleMap(s, rootDict)}

	root := &Elem{Role: RoleDocument, RawType: RoleDocument}
	seen := map[objects.Ref]bool{}
	kids, err := t.readKids(s, rootDict, root, seen, 0)
	if err != nil {
		return nil, err
	}
	root.Kids = kids
	t.Root = root
	return t, nil
}

func readRoleMap(s objects.Store, rootDict objects.Dict) map[Role]Role {
	rm, ok := objects.GetDict(s, rootDict, "RoleMap")
	if !ok {
		return nil
	}
	out := make(map[Role]Role, len(rm))
	for k, v := range rm {
		if n, isName := v.(objects.Name); isName {
			out[Role(k)] = Role(n)
		}
	}
	return out
}

// normalize resolves a raw type through /RoleMap. The map may chain, so follow
// it with a bound; a self-referential map is malformed but must not hang.
func (t *Tree) normalize(raw Role) Role {
	r := raw
	for i := 0; i < 8; i++ {
		next, ok := t.RoleMap[r]
		if !ok || next == r {
			return r
		}
		r = next
	}
	return r
}

// maxTreeDepth bounds structure-tree recursion.
//
// The visited-reference check stops a tree from looping back on itself, but it
// does not stop a chain of distinct references from being arbitrarily long, and
// these files are untrusted: a document nesting a million elements would exhaust
// the stack before any consumer saw a result. The observed maximum across the
// spec corpus is 13, so this bound cannot be reached by a real document. It
// bounds Walk and Stats transitively, since neither can descend past what
// construction built.
const maxTreeDepth = 512

// readKids reads the /K entry of a structure element or the tree root.
//
// /K is the most irregular part of the structure tree: it may be a single
// element dictionary, an integer MCID, a marked-content reference dictionary, an
// object reference dictionary, or an array mixing all of those. Each shape is
// handled explicitly because guessing produces silently wrong reading order.
func (t *Tree) readKids(s objects.Store, d objects.Dict, parent *Elem, seen map[objects.Ref]bool, depth int) ([]*Elem, error) {
	if depth > maxTreeDepth {
		return nil, nil
	}
	kObj, ok := d["K"]
	if !ok {
		return nil, nil
	}
	resolved, err := s.Resolve(kObj)
	if err != nil {
		return nil, nil
	}

	var out []*Elem
	for _, item := range objects.ArrayOrSingle(resolved) {
		// Track visited references, not visited dictionaries: a cycle can only
		// be formed through an indirect reference, and a document legitimately
		// reaches the same direct dictionary from nowhere else.
		if ref, isRef := item.(objects.Ref); isRef {
			if seen[ref] {
				continue
			}
			seen[ref] = true
		}
		res, err := s.Resolve(item)
		if err != nil {
			continue
		}
		switch v := res.(type) {
		case objects.Int:
			// A bare integer is an MCID on the parent's own page.
			parent.MCIDs = append(parent.MCIDs, int(v))
		case objects.Dict:
			kid, err := t.readKidDict(s, v, parent, seen, depth)
			if err != nil {
				return nil, err
			}
			if kid != nil {
				out = append(out, kid)
			}
		}
	}
	return out, nil
}

// readKidDict interprets one dictionary found in /K. It returns nil when the
// dictionary contributed MCIDs to the parent instead of a new element.
func (t *Tree) readKidDict(s objects.Store, d objects.Dict, parent *Elem, seen map[objects.Ref]bool, depth int) (*Elem, error) {
	switch typ, _ := objects.GetName(s, d, "Type"); typ {
	case "MCR":
		// Marked-content reference: an MCID plus the page it sits on. The page
		// may differ from the parent's, which is how one element spans pages.
		if mcid, ok := objects.GetInt(s, d, "MCID"); ok {
			parent.MCIDs = append(parent.MCIDs, int(mcid))
		}
		return nil, nil
	case "OBJR":
		// Object reference: points at an annotation or XObject rather than text.
		// Nothing to extract, and descending would leave the structure tree.
		return nil, nil
	}

	// Anything else is a structure element. /Type is optional on those, so
	// absence is normal and the presence of /S is the real signal.
	rawType, hasS := objects.GetName(s, d, "S")
	if !hasS {
		return nil, nil
	}

	e := &Elem{
		RawType: Role(rawType),
		Parent:  parent,
	}
	e.Role = t.normalize(e.RawType)

	if v, ok := objects.Get(s, d, "T"); ok {
		e.Title = objects.DecodeTextString(v)
	}
	if v, ok := objects.Get(s, d, "Lang"); ok {
		e.Lang = objects.DecodeTextString(v)
	}
	if v, ok := objects.Get(s, d, "ActualText"); ok {
		e.ActualText = objects.DecodeTextString(v)
	}
	if v, ok := objects.Get(s, d, "Alt"); ok {
		e.Alt = objects.DecodeTextString(v)
	}
	if pg, ok := d["Pg"]; ok {
		if ref, isRef := pg.(objects.Ref); isRef {
			e.pageRef = &ref
		}
	}

	kids, err := t.readKids(s, d, e, seen, depth+1)
	if err != nil {
		return nil, err
	}
	e.Kids = kids
	return e, nil
}

// ResolvePages fills in Elem.Page for every element carrying a /Pg reference.
//
// This is separate from Read because it costs a walk of the page tree to build
// the object-number-to-page-number map, and a caller that only wants the
// heading outline never needs page numbers. Reading a structure tree of a
// 1000-page document should not pay for page resolution it will not use.
func (t *Tree) ResolvePages(s objects.Store) error {
	if t == nil || t.Root == nil {
		return nil
	}

	// Build the reverse map by asking for each page dictionary and recording
	// which object number answered. Page() resolves through the page tree, so
	// this needs no tree walking of its own.
	byObjNum := make(map[int]int, s.PageCount())
	cat, err := s.Catalog()
	if err != nil {
		return fmt.Errorf("tag: catalog: %w", err)
	}
	pagesObj, ok := cat["Pages"]
	if !ok {
		return nil
	}
	if err := indexPages(s, pagesObj, byObjNum, new(int), map[int]bool{}, 0); err != nil {
		return err
	}

	t.Walk(func(e *Elem, _ int) bool {
		if e.pageRef != nil {
			if n, ok := byObjNum[e.pageRef.Num]; ok {
				e.Page = n
			}
		}
		return true
	})
	return nil
}

// indexPages walks the page tree in order, assigning 1-based page numbers to
// the object number of each leaf /Page node.
//
// Depth is bounded and visited nodes are tracked because a damaged page tree can
// contain a cycle, and this walk must terminate on hostile input.
func indexPages(s objects.Store, node objects.Object, out map[int]int, next *int, seen map[int]bool, depth int) error {
	if depth > 64 {
		return nil
	}
	objNum := -1
	if ref, isRef := node.(objects.Ref); isRef {
		if seen[ref.Num] {
			return nil
		}
		seen[ref.Num] = true
		objNum = ref.Num
	}
	res, err := s.Resolve(node)
	if err != nil {
		return nil
	}
	d, isDict := res.(objects.Dict)
	if !isDict {
		return nil
	}

	typ, _ := objects.GetName(s, d, "Type")
	kids, hasKids := objects.GetArray(s, d, "Kids")

	// Trust the shape over /Type: an intermediate node missing /Pages but
	// carrying /Kids is common in damaged files, and a leaf is anything without
	// children.
	if typ == "Pages" || hasKids {
		for _, k := range kids {
			if err := indexPages(s, k, out, next, seen, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	*next++
	if objNum >= 0 {
		out[objNum] = *next
	}
	return nil
}

// Walk calls fn for every element in logical reading order, depth first. A fn
// returning false stops the traversal.
func (t *Tree) Walk(fn func(e *Elem, depth int) bool) {
	if t == nil || t.Root == nil {
		return
	}
	walk(t.Root, 0, fn)
}

func walk(e *Elem, depth int, fn func(*Elem, int) bool) bool {
	if !fn(e, depth) {
		return false
	}
	for _, k := range e.Kids {
		if !walk(k, depth+1, fn) {
			return false
		}
	}
	return true
}

// Depth returns the heading level for a heading element.
//
// H1..H6 report their own level. A bare H per §14.8.4.4 takes its level from
// nesting: count enclosing grouping elements, since those are what express
// hierarchy for documents that use H throughout.
func (e *Elem) Depth() int {
	if lvl := e.Role.HeadingLevel(); lvl > 0 {
		return lvl
	}
	if e.Role != RoleH {
		return 0
	}
	level := 0
	for p := e.Parent; p != nil; p = p.Parent {
		if p.Role.isGrouping() && p.Role != RoleDocument {
			level++
		}
	}
	if level == 0 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	return level
}

// Stats summarizes a tree, for probe output and for confirming that a tree is
// substantive rather than a near-empty stub some producers emit.
type Stats struct {
	Elements int
	Headings int
	Paras    int
	Tables   int
	Figures  int
	Lists    int
	MCIDs    int
	MaxDepth int
	Roles    map[Role]int
}

// Stats walks the tree and counts what it contains.
func (t *Tree) Stats() Stats {
	st := Stats{Roles: map[Role]int{}}
	t.Walk(func(e *Elem, depth int) bool {
		st.Elements++
		st.Roles[e.Role]++
		st.MCIDs += len(e.MCIDs)
		if depth > st.MaxDepth {
			st.MaxDepth = depth
		}
		switch {
		case e.Role.IsHeading():
			st.Headings++
		case e.Role == RoleP:
			st.Paras++
		case e.Role == RoleTable:
			st.Tables++
		case e.Role == RoleFigure:
			st.Figures++
		case e.Role == RoleL:
			st.Lists++
		}
		return true
	})
	return st
}

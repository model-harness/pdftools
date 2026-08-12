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

	"github.com/model-harness/pdftools/objects"
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

// RoleStructTreeRoot is the role of Tree.Root, which stands in for /StructTreeRoot
// itself and is not a structure element of the document.
//
// Not a standard type, and deliberately not one: /StructTreeRoot has /K but no /S, so
// it has no role of its own, and the root needs one to be a node like any other. It
// held RoleDocument before, which made every count of a standard role wrong for any
// file whose own top element is a Document — 17 of the 18 tagged files on disk reported
// two Document elements where the file has one, including the figure probe emits as
// tags.top_roles.
//
// A name outside §14.8.4 makes the collision rare rather than impossible, and the
// difference is worth stating. A role comes from an element's /S or from a /RoleMap
// target, and nothing here rejects either one naming StructTreeRoot: a file that did
// would put the count back where it was. What the rename buys is that 0 elements across
// those 18 trees carry the name, against a Document in almost every one of them — a
// measured property of real producers, not an invariant. Enforcing it is not worth the
// cost, because the failure it would prevent is a wrong count and the enforcement would
// have to either drop a real element or rename it to something equally untrue.
//
// Behaviour is unchanged by the rename, though the mechanism differs and the two are
// worth keeping straight. Depth counts grouping ancestors and previously excluded the
// root by name, since isGrouping accepted RoleDocument: that name check is still there
// and still excludes the file's own Document elements. The root is now excluded one layer
// earlier, by isGrouping not accepting it at all. Both leave a bare H taking its level
// from the Sects that enclose it. sectionize treats it as transparent, since it is
// neither a heading nor a block role.
const RoleStructTreeRoot Role = "StructTreeRoot"

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

	// Page is the 1-based page this element is anchored to, or 0 when neither it
	// nor any ancestor names one. Structure elements need not be page-scoped,
	// which is exactly what lets a section span pages.
	//
	// Inherited, not just read: §14.7.4.3 says an element's page is its own /Pg
	// or, absent that, its nearest ancestor's. A producer that puts /Pg on a Sect
	// and omits it from every P inside is conformant, and reading only the
	// element's own entry leaves all of them anchored nowhere.
	Page int

	// Content is the marked-content this element owns, in order, each identifier
	// paired with the page it was drawn on. Together they are the join key
	// between the structure tree and the text found in content streams.
	//
	// The page is per reference rather than per element because a marked-content
	// reference may carry its own /Pg, and that is precisely how one paragraph
	// continues across a page break. Rare — 5 of 2,035 references in
	// Well-Tagged-PDF-WTPDF-1.0.pdf, none in ISO 32000-2 — but every one of those
	// 5 names a page other than its element's, so treating the element's page as
	// authoritative loses exactly the content that spans pages.
	Content []MCRef

	// Kids are child elements in logical reading order.
	Kids []*Elem

	// KidAt is the position in /K of each kid in Kids, so that a caller can
	// interleave an element's own Content with its Kids in the order the file
	// declares rather than reading all of one and then all of the other.
	//
	// Two slices and one index rather than a single ordered slice of "either a
	// reference or an element", because every caller but one wants exactly Content
	// or exactly Kids, and a sum type would make all of them switch. MCRef.Order is
	// the same position for a reference; both count positions in the same /K array,
	// so comparing one against the other is what orders them.
	//
	// This matters for the 767 elements on disk that hold both. Reading Content
	// first and Kids after moves the kids' text to the end of the parent's: 32022
	// runes across 13 documents, and where the kid is a one-glyph Span the effect is
	// a character torn out of the middle of a word and left at the end of the
	// paragraph — "constituent elements.--" in ISO/TS 32005's Table 1, where the two
	// soft hyphens of "exposi-tion" and "forma-ts" ended up.
	KidAt []int

	// Parent is the enclosing element, nil at the root.
	Parent *Elem

	// pageRef holds the unresolved /Pg reference until page numbers are known.
	// Resolving during the walk would need an object-number-to-page-number map,
	// which costs a full page-tree traversal, so it is deferred to ResolvePages.
	pageRef *objects.Ref
}

// MCRef is one marked-content identifier together with the page it was drawn on.
//
// Page is 0 until ResolvePages has run, and stays 0 for a reference whose element
// chain never names a page — that content cannot be joined to page text, because an
// MCID is unique only within a page.
type MCRef struct {
	MCID int
	Page int

	// Order is this reference's position in its element's /K array, which is what
	// lets a caller interleave it with that element's kids — see Elem.KidAt.
	Order int

	// pageRef is an MCR's own /Pg, unresolved. Nil when the reference inherits its
	// element's page, which is the common case.
	pageRef *objects.Ref
}

// MCIDs returns the identifiers in Content, for a caller that only needs to know
// which marked content an element owns and not where it sits.
func (e *Elem) MCIDs() []int {
	out := make([]int, 0, len(e.Content))
	for _, c := range e.Content {
		out = append(out, c.MCID)
	}
	return out
}

// Tree is a document's structure tree.
type Tree struct {
	// Root stands in for /StructTreeRoot and is not an element of the document. Its
	// role is RoleStructTreeRoot, its Kids are the document's own top-level elements,
	// and it carries no title, page or content of its own. A caller counting elements
	// or roles is counting one node the file does not contain — see RoleStructTreeRoot.
	Root *Elem

	// RoleMap is the catalog's /RoleMap, custom type to standard type.
	RoleMap map[Role]Role
}

// Read returns the structure tree, or nil with a nil error when the document
// has none.
//
// An absent tree is the normal case for most PDFs in the wild and is not a
// failure: it selects the layout path instead. Callers check for nil.
//
// Tree.Root is synthesized to stand in for /StructTreeRoot, which has /K but no /S
// and so is not an element. It is one node the document does not contain, and
// RoleStructTreeRoot says why it needs a role outside §14.8.4.
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

	root := &Elem{Role: RoleStructTreeRoot, RawType: RoleStructTreeRoot}
	seen := map[objects.Ref]bool{}
	if err := t.readKids(s, rootDict, root, seen, 0); err != nil {
		return nil, err
	}
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
//
// It writes parent.Kids and parent.KidAt itself rather than returning the kids, so
// that the two cannot be assigned by different statements and fall out of step: they
// are one sequence indexed twice, and a caller holding only one of them cannot restore
// the /K order the other encodes.
func (t *Tree) readKids(s objects.Store, d objects.Dict, parent *Elem, seen map[objects.Ref]bool, depth int) error {
	if depth > maxTreeDepth {
		return nil
	}
	kObj, ok := d["K"]
	if !ok {
		return nil
	}
	resolved, err := s.Resolve(kObj)
	if err != nil {
		return nil
	}

	// at is the position in /K, recorded on whichever of the two slices the item lands
	// in so that a caller can put them back in this order. It counts every item the
	// array holds, including the ones that contribute nothing — an OBJR, a dictionary
	// with no /S, a reference already seen — because it is only ever compared against
	// another position from the same array, and skipping a gap would cost a second
	// counter to no purpose.
	for at, item := range objects.ArrayOrSingle(resolved) {
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
			parent.Content = append(parent.Content, MCRef{MCID: int(v), Order: at})
		case objects.Dict:
			n := len(parent.Content)
			kid, err := t.readKidDict(s, v, parent, seen, depth)
			if err != nil {
				return err
			}
			// An MCR or MCID dictionary appends to parent.Content and returns no kid,
			// so its position is stamped here rather than in readKidDict, which does
			// not know it.
			for i := n; i < len(parent.Content); i++ {
				parent.Content[i].Order = at
			}
			if kid != nil {
				parent.Kids = append(parent.Kids, kid)
				parent.KidAt = append(parent.KidAt, at)
			}
		}
	}
	return nil
}

// readKidDict interprets one dictionary found in /K. It returns nil when the
// dictionary contributed MCIDs to the parent instead of a new element.
func (t *Tree) readKidDict(s objects.Store, d objects.Dict, parent *Elem, seen map[objects.Ref]bool, depth int) (*Elem, error) {
	switch typ, _ := objects.GetName(s, d, "Type"); typ {
	case "MCR":
		// Marked-content reference: an MCID plus, optionally, the page it sits on.
		// That page may differ from the parent's, and where it does it is the whole
		// reason the reference exists — a paragraph continuing past a page break is
		// one element whose content is on two pages. Keeping the /Pg is therefore
		// not a refinement: dropping it silently reassigns the continuation to the
		// element's own page, where the MCID means something else entirely.
		mcid, ok := objects.GetInt(s, d, "MCID")
		if !ok {
			return nil, nil
		}
		ref := MCRef{MCID: int(mcid)}
		if pg, has := d["Pg"]; has {
			if r, isRef := pg.(objects.Ref); isRef {
				ref.pageRef = &r
			}
		}
		parent.Content = append(parent.Content, ref)
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

	if err := t.readKids(s, d, e, seen, depth+1); err != nil {
		return nil, err
	}
	return e, nil
}

// ResolvePages fills in Elem.Page and the page of every MCRef.
//
// Separate from Read because it costs a walk of the page tree to build the
// object-number-to-page-number map, and a caller that only wants the heading
// outline never needs page numbers. Reading the structure tree of a 1000-page
// document should not pay for page resolution it will not use.
//
// Pages are inherited: an element with no /Pg takes its nearest ancestor's, and a
// marked-content reference with no /Pg takes its element's. That is §14.7.4.3, and
// it is what lets a producer put /Pg once on a Sect instead of on all 300 paragraphs
// inside it.
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

	assignPages(t.Root, 0, byObjNum)
	return nil
}

// assignPages walks the tree top-down, carrying the enclosing page so an element
// with no /Pg of its own inherits it.
//
// Recursive rather than a Tree.Walk closure because inheritance needs the parent's
// resolved value on the way down, and Walk hands out a depth rather than a path.
func assignPages(e *Elem, inherited int, byObjNum map[int]int) {
	page := inherited
	if e.pageRef != nil {
		if n, ok := byObjNum[e.pageRef.Num]; ok {
			page = n
		}
	}
	e.Page = page

	for i := range e.Content {
		c := &e.Content[i]
		c.Page = page
		if c.pageRef == nil {
			continue
		}
		if n, ok := byObjNum[c.pageRef.Num]; ok {
			c.Page = n
		}
	}
	for _, k := range e.Kids {
		assignPages(k, page, byObjNum)
	}
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
		st.MCIDs += len(e.Content)
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

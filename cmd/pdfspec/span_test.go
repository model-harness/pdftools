package main

import (
	"testing"

	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/tag"
)

// TestSectionShapeOnTarget pins down how the target document expresses clause
// structure, because sectionize has two plausible algorithms and they disagree by
// two orders of magnitude on the same file:
//
//	container-driven - a clause is a Sect/Div subtree; collect containers
//	heading-driven   - a clause is a heading plus its following siblings
//
// The measurements here select heading-driven. Container-driven would emit 7
// sections from a 1,023-page standard. This test exists so that choice stays
// justified by data rather than by memory, and fails loudly if a future document
// or parser change invalidates it.
func TestSectionShapeOnTarget(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	tr, err := tag.Read(s)
	if err != nil || tr == nil {
		t.Fatalf("tag.Read: %v", err)
	}
	if err := tr.ResolvePages(s); err != nil {
		t.Fatalf("ResolvePages: %v", err)
	}

	// Page anchoring must work before any span claim means anything.
	anchored := 0
	tr.Walk(func(e *tag.Elem, _ int) bool {
		if e.Page > 0 {
			anchored++
		}
		if e.Page < 0 || e.Page > s.PageCount() {
			t.Errorf("page %d out of range 1..%d", e.Page, s.PageCount())
		}
		return true
	})
	t.Logf("anchored %d of %d elements across %d pages",
		anchored, tr.Stats().Elements, s.PageCount())
	if anchored < tr.Stats().Elements/2 {
		t.Fatalf("only %d of %d elements anchored: /Pg resolution is broken",
			anchored, tr.Stats().Elements)
	}

	// Span extent per role. The design's original claim was about section
	// containers specifically, so counting all elements would have hidden this.
	type stat struct{ total, spanning, maxSpan int }
	byRole := map[tag.Role]*stat{}

	var extent func(*tag.Elem) (int, int)
	extent = func(e *tag.Elem) (int, int) {
		lo, hi := e.Page, e.Page
		for _, k := range e.Kids {
			klo, khi := extent(k)
			if klo > 0 && (lo == 0 || klo < lo) {
				lo = klo
			}
			if khi > hi {
				hi = khi
			}
		}
		return lo, hi
	}

	tr.Walk(func(e *tag.Elem, _ int) bool {
		lo, hi := extent(e)
		st := byRole[e.Role]
		if st == nil {
			st = &stat{}
			byRole[e.Role] = st
		}
		st.total++
		if lo > 0 && hi > lo {
			st.spanning++
			if n := hi - lo + 1; n > st.maxSpan {
				st.maxSpan = n
			}
		}
		return true
	})
	for _, r := range []tag.Role{tag.RoleSect, tag.RoleDiv, tag.RolePart, tag.RoleTable, tag.RoleL, tag.RoleP} {
		if st := byRole[r]; st != nil {
			t.Logf("%-6s total=%-6d spanning=%-5d widest=%d pages", r, st.total, st.spanning, st.maxSpan)
		}
	}

	// The flat-shape evidence: headings vastly outnumber containers, and almost
	// none of them own their body.
	headings, withKids, widestKids := 0, 0, 0
	parents := map[tag.Role]int{}
	tr.Walk(func(e *tag.Elem, _ int) bool {
		if n := len(e.Kids); n > widestKids {
			widestKids = n
		}
		if !e.Role.IsHeading() {
			return true
		}
		headings++
		if len(e.Kids) > 0 {
			withKids++
		}
		if e.Parent != nil {
			parents[e.Parent.Role]++
		}
		return true
	})
	// A role absent from the document has no entry, so read through nil.
	total := func(r tag.Role) int {
		if st := byRole[r]; st != nil {
			return st.total
		}
		return 0
	}
	containers := total(tag.RoleSect) + total(tag.RoleDiv)
	t.Logf("headings=%d (with element children: %d) sect+div=%d widest fan-out=%d parents=%v",
		headings, withKids, containers, widestKids, parents)

	if headings < 900 {
		t.Fatalf("headings = %d: expected ~981, structure parsing regressed", headings)
	}
	// The load-bearing assertion. If containers ever come to outnumber headings,
	// the container-driven algorithm becomes viable and this test should be
	// revisited deliberately rather than silently.
	if containers >= headings/10 {
		t.Errorf("sect+div = %d vs %d headings: shape is no longer flat, "+
			"revisit the heading-driven premise in docs/DESIGN.md", containers, headings)
	}
	if withKids > headings/10 {
		t.Errorf("%d of %d headings own element children: bodies may be "+
			"descendants after all, which changes sectionize", withKids, headings)
	}
	if widestKids < 1000 {
		t.Errorf("widest fan-out = %d: expected a flat multi-thousand-child "+
			"container, structure may have changed", widestKids)
	}
}

// TestHeadingTitlesAreEmptyOnTarget records why sectionize cannot lean on /T.
// Every heading in ISO 32000-2 has an empty title, so heading text has to come
// from joining MCIDs to page content. Discovering that after writing sink/okf
// would mean an OKF bundle full of untitled sections.
func TestHeadingTitlesAreEmptyOnTarget(t *testing.T) {
	path := corpusFile(t, "ISO_32000-2_sponsored_EC3.pdf")
	s, err := pcstore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	tr, err := tag.Read(s)
	if err != nil || tr == nil {
		t.Fatalf("tag.Read: %v", err)
	}

	headings, titled, withMCIDs := 0, 0, 0
	tr.Walk(func(e *tag.Elem, _ int) bool {
		if !e.Role.IsHeading() {
			return true
		}
		headings++
		if e.Title != "" {
			titled++
		}
		if len(e.Content) > 0 {
			withMCIDs++
		}
		return true
	})
	t.Logf("headings=%d titled=%d with MCIDs=%d", headings, titled, withMCIDs)

	// Heading text must be recoverable somehow. If neither /T nor MCIDs are
	// present, the tagged path cannot name a section and the premise fails.
	if titled == 0 && withMCIDs == 0 {
		t.Fatal("headings carry neither /T nor MCIDs: heading text is unrecoverable")
	}
	if titled == 0 && withMCIDs < headings/2 {
		t.Errorf("no /T and only %d of %d headings have MCIDs", withMCIDs, headings)
	}
}

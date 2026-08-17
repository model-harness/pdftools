package doc

import (
	"slices"
	"testing"
)

// spans builds a block from MCIDs alone, since SetMCIDs reads nothing else.
func spans(ids ...int) Block {
	var b Block
	for _, id := range ids {
		b.Spans = append(b.Spans, Span{Text: "x", MCID: id})
	}
	return b
}

// The field is documented as a union, and this is the assertion that makes that true of
// the code rather than only of the comment.
//
// Every case here was a live defect at some write site before SetMCIDs existed. sectionize's
// emitItem appended one entry per span with no filter at all, so a four-span element across
// two identifiers recorded four entries and the -1s among them; its unplaced filtered the
// -1s and still recorded a repeat per span. 34364 duplicate entries across 7406 of 46722 blocks
// corpus-wide, against an invariant stated on the field in doc/block.go and implemented four
// separate times.
func TestSetMCIDsBuildsTheUnion(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []int
		want []int
	}{
		{"a repeat collapses", []int{1, 1, 2, 2}, []int{1, 2}},
		{"the absent value is not an identifier", []int{-1, 3, -1}, []int{3}},
		{"first appearance sets the order", []int{5, 2, 5, 9, 2}, []int{5, 2, 9}},
		{"zero is a valid identifier", []int{0, 0}, []int{0}},
		// Not an empty non-nil slice: a block assembled from no marked content has no
		// set, and a reader asking len() cannot tell the two apart anyway.
		{"no spans is no set", nil, nil},
		{"only artifacts is no set", []int{-1, -1}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := spans(c.in...)
			b.SetMCIDs()
			if !slices.Equal(b.MCIDs, c.want) {
				t.Errorf("MCIDs = %v, want %v", b.MCIDs, c.want)
			}
		})
	}
}

// A rebuild writes a new slice, because a struct copy of a Block shares the old one.
//
// This is why SetMCIDs does not truncate and reuse. sectionize.detach exists precisely
// because `blk := *other` shares the arrays behind Spans and MCIDs, and layout's spanBlock
// starts from exactly that copy before calling this. Reusing the storage would edit the
// original's set in place through the array they share, and the original would keep its own
// length — so it would read back values computed for a different block. Invisible in the
// corpus render, which is what makes it worth a test rather than a comment: nothing
// downstream reads this field, so the wrong answer would only ever surface in a diagnostic.
func TestSetMCIDsDoesNotWriteThroughASharedArray(t *testing.T) {
	orig := spans(7, 8, 9)
	orig.SetMCIDs()

	copied := orig
	copied.Spans = []Span{{Text: "x", MCID: 1}, {Text: "x", MCID: 2}}
	copied.SetMCIDs()

	if want := []int{7, 8, 9}; !slices.Equal(orig.MCIDs, want) {
		t.Errorf("original MCIDs = %v, want %v: the copy wrote through the shared array", orig.MCIDs, want)
	}
	if want := []int{1, 2}; !slices.Equal(copied.MCIDs, want) {
		t.Errorf("copy MCIDs = %v, want %v", copied.MCIDs, want)
	}
}

// Calling it twice is calling it once, so a stage may rebuild after editing spans without
// having to know whether an earlier stage already did.
func TestSetMCIDsIsIdempotent(t *testing.T) {
	b := spans(4, 4, -1, 6)
	b.SetMCIDs()
	first := slices.Clone(b.MCIDs)
	b.SetMCIDs()
	if !slices.Equal(b.MCIDs, first) {
		t.Errorf("second call = %v, first = %v", b.MCIDs, first)
	}
}

// A rebuild reflects the spans as they are now, which is the property every caller relies
// on: extract calls it after merging fragments into fewer spans, sectionize after dropping
// the claimed ones, and layout after splitting a row into cells.
func TestSetMCIDsFollowsTheSpans(t *testing.T) {
	b := spans(1, 2, 3)
	b.SetMCIDs()
	b.Spans = b.Spans[:1]
	b.SetMCIDs()
	if want := []int{1}; !slices.Equal(b.MCIDs, want) {
		t.Errorf("MCIDs = %v, want %v: a stale entry survived a span removal", b.MCIDs, want)
	}
}

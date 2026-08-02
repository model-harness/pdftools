package tag

import (
	"testing"

	"github.com/3rg0n/pdf-spec/objects"
)

// store is a minimal objects.Store over an in-memory object table, so structure
// tree shapes can be tested without a PDF file. The irregular /K encodings below
// are the whole reason this fake exists: each one is a real shape producers emit.
type store struct {
	objs map[objects.Ref]objects.Object
	cat  objects.Dict
}

func (s *store) Resolve(o objects.Object) (objects.Object, error) {
	for i := 0; i < 8; i++ {
		ref, isRef := o.(objects.Ref)
		if !isRef {
			return o, nil
		}
		v, ok := s.objs[ref]
		if !ok {
			return objects.Null{}, nil
		}
		o = v
	}
	return objects.Null{}, nil
}
func (s *store) Trailer() (objects.Dict, error)  { return objects.Dict{}, nil }
func (s *store) Catalog() (objects.Dict, error)  { return s.cat, nil }
func (s *store) PageCount() int                  { return 1 }
func (s *store) Page(int) (objects.Dict, error)  { return objects.Dict{}, nil }
func (s *store) PageContent(int) ([]byte, error) { return nil, nil }
func (s *store) Version() string                 { return "2.0" }
func (s *store) Encrypted() bool                 { return false }
func (s *store) Close() error                    { return nil }

func TestReadNoStructTreeIsNotAnError(t *testing.T) {
	// Most PDFs are untagged. That selects the layout path; it is not a failure.
	s := &store{cat: objects.Dict{"Type": objects.Name("Catalog")}}
	tr, err := Read(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr != nil {
		t.Fatal("expected nil tree")
	}
}

func TestReadKidShapes(t *testing.T) {
	// /K may be a single dict, an integer MCID, an MCR dict, an OBJR dict, or an
	// array mixing all of them. Every shape appears in real files.
	h1 := objects.Dict{
		"S": objects.Name("H1"),
		"T": objects.String("Scope"),
		"K": objects.Int(0), // bare MCID
	}
	para := objects.Dict{
		"S": objects.Name("P"),
		"K": objects.Array{
			objects.Int(1),
			objects.Dict{"Type": objects.Name("MCR"), "MCID": objects.Int(2)},
			objects.Dict{"Type": objects.Name("OBJR")}, // contributes nothing
		},
	}
	root := objects.Dict{
		"Type": objects.Name("StructTreeRoot"),
		"K": objects.Array{
			objects.Ref{Num: 10},
			objects.Ref{Num: 11},
		},
	}
	s := &store{
		cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}},
		objs: map[objects.Ref]objects.Object{
			{Num: 1}:  root,
			{Num: 10}: h1,
			{Num: 11}: para,
		},
	}

	tr, err := Read(s)
	if err != nil {
		t.Fatal(err)
	}
	if tr == nil {
		t.Fatal("expected a tree")
	}
	if len(tr.Root.Kids) != 2 {
		t.Fatalf("want 2 kids, got %d", len(tr.Root.Kids))
	}

	gotH1 := tr.Root.Kids[0]
	if gotH1.Role != RoleH1 || gotH1.Title != "Scope" {
		t.Fatalf("h1 wrong: %+v", gotH1)
	}
	if len(gotH1.MCIDs) != 1 || gotH1.MCIDs[0] != 0 {
		t.Fatalf("bare int MCID not collected: %v", gotH1.MCIDs)
	}

	gotP := tr.Root.Kids[1]
	if len(gotP.MCIDs) != 2 || gotP.MCIDs[0] != 1 || gotP.MCIDs[1] != 2 {
		t.Fatalf("MCR not collected alongside bare int: %v", gotP.MCIDs)
	}
}

func TestRoleMapNormalization(t *testing.T) {
	// A document may name its own roles and map them to standard ones. Trusting
	// the raw /S value would silently misclassify every element.
	s := &store{
		cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}},
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: objects.Dict{
				"RoleMap": objects.Dict{
					"ClauseTitle": objects.Name("H2"),
					"BodyText":    objects.Name("P"),
				},
				"K": objects.Array{objects.Ref{Num: 2}},
			},
			{Num: 2}: objects.Dict{"S": objects.Name("ClauseTitle")},
		},
	}
	tr, err := Read(s)
	if err != nil {
		t.Fatal(err)
	}
	e := tr.Root.Kids[0]
	if e.Role != RoleH2 {
		t.Fatalf("role not normalized: %v", e.Role)
	}
	if e.RawType != Role("ClauseTitle") {
		t.Fatalf("raw type lost: %v", e.RawType)
	}
	if e.Role.HeadingLevel() != 2 {
		t.Fatalf("heading level %d", e.Role.HeadingLevel())
	}
}

func TestRoleMapChainTerminates(t *testing.T) {
	// A self-referential role map is malformed but must not hang.
	tr := &Tree{RoleMap: map[Role]Role{"A": "B", "B": "A"}}
	_ = tr.normalize("A") // must return
}

func TestCycleDoesNotHang(t *testing.T) {
	// A structure element whose /K points back at an ancestor is malformed. The
	// corpora that need this toolkit most are the malformed ones, so termination
	// is a requirement, not a nicety.
	s := &store{
		cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}},
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: objects.Dict{"K": objects.Array{objects.Ref{Num: 2}}},
			{Num: 2}: objects.Dict{"S": objects.Name("Sect"), "K": objects.Array{objects.Ref{Num: 3}}},
			{Num: 3}: objects.Dict{"S": objects.Name("Div"), "K": objects.Array{objects.Ref{Num: 2}}},
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := Read(s); err != nil {
			t.Error(err)
		}
	}()
	<-done
}

func TestDeepTreeIsBounded(t *testing.T) {
	// The visited-reference check stops a tree from looping back on itself, but a
	// chain of distinct references can be arbitrarily long. These files are
	// untrusted, so an attacker-supplied million-deep nest must not exhaust the
	// stack. The observed corpus maximum is 13, so the bound is unreachable by a
	// real document.
	const chain = maxTreeDepth * 3
	objs := make(map[objects.Ref]objects.Object, chain+1)
	objs[objects.Ref{Num: 1}] = objects.Dict{"K": objects.Array{objects.Ref{Num: 2}}}
	for i := 2; i <= chain; i++ {
		objs[objects.Ref{Num: i}] = objects.Dict{
			"S": objects.Name("Div"),
			"K": objects.Array{objects.Ref{Num: i + 1}},
		}
	}
	objs[objects.Ref{Num: chain + 1}] = objects.Dict{"S": objects.Name("P")}

	s := &store{cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}}, objs: objs}
	tr, err := Read(s)
	if err != nil {
		t.Fatal(err)
	}
	// Truncation is the correct outcome: return what is readable rather than
	// crashing or refusing the whole document.
	if got := tr.Stats().MaxDepth; got > maxTreeDepth+2 {
		t.Fatalf("MaxDepth = %d, want bounded near %d", got, maxTreeDepth)
	}
	if tr.Stats().Elements < 10 {
		t.Fatalf("bound discarded the readable prefix: %d elements", tr.Stats().Elements)
	}
}

func TestBareHDepthFromNesting(t *testing.T) {
	// ISO 32000-2 14.8.4.4: a bare H takes its level from nesting depth, not
	// from its name. Documents that use H throughout are otherwise unreadable.
	s := &store{
		cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}},
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: objects.Dict{"K": objects.Array{objects.Ref{Num: 2}}},
			{Num: 2}: objects.Dict{ // Sect -> level 1
				"S": objects.Name("Sect"),
				"K": objects.Array{
					objects.Dict{"S": objects.Name("H")},
					objects.Ref{Num: 3},
				},
			},
			{Num: 3}: objects.Dict{ // Sect > Sect -> level 2
				"S": objects.Name("Sect"),
				"K": objects.Array{objects.Dict{"S": objects.Name("H")}},
			},
		},
	}
	tr, err := Read(s)
	if err != nil {
		t.Fatal(err)
	}
	outer := tr.Root.Kids[0]
	if got := outer.Kids[0].Depth(); got != 1 {
		t.Fatalf("outer H depth = %d, want 1", got)
	}
	inner := outer.Kids[1]
	if got := inner.Kids[0].Depth(); got != 2 {
		t.Fatalf("inner H depth = %d, want 2", got)
	}
}

func TestHeadingLevelExplicit(t *testing.T) {
	for i, r := range []Role{RoleH1, RoleH2, RoleH3, RoleH4, RoleH5, RoleH6} {
		if got := r.HeadingLevel(); got != i+1 {
			t.Fatalf("%v level = %d, want %d", r, got, i+1)
		}
		if !r.IsHeading() {
			t.Fatalf("%v should be a heading", r)
		}
	}
	// A bare H is a heading with no intrinsic level.
	if RoleH.HeadingLevel() != 0 || !RoleH.IsHeading() {
		t.Fatal("bare H should be a heading of level 0")
	}
	if RoleP.IsHeading() {
		t.Fatal("P is not a heading")
	}
}

func TestUTF16TitleDecoded(t *testing.T) {
	s := &store{
		cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}},
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: objects.Dict{"K": objects.Array{objects.Ref{Num: 2}}},
			{Num: 2}: objects.Dict{
				"S": objects.Name("H1"),
				"T": objects.String{0xFE, 0xFF, 0x00, 'O', 0x00, 'K'},
			},
		},
	}
	tr, err := Read(s)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Root.Kids[0].Title; got != "OK" {
		t.Fatalf("title = %q, want %q", got, "OK")
	}
}

func TestStatsCounts(t *testing.T) {
	s := &store{
		cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}},
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: objects.Dict{"K": objects.Array{
				objects.Dict{"S": objects.Name("H1"), "K": objects.Int(0)},
				objects.Dict{"S": objects.Name("P"), "K": objects.Int(1)},
				objects.Dict{"S": objects.Name("Table")},
				objects.Dict{"S": objects.Name("Figure")},
			}},
		},
	}
	tr, err := Read(s)
	if err != nil {
		t.Fatal(err)
	}
	st := tr.Stats()
	if st.Headings != 1 || st.Paras != 1 || st.Tables != 1 || st.Figures != 1 {
		t.Fatalf("bad stats: %+v", st)
	}
	if st.MCIDs != 2 {
		t.Fatalf("MCIDs = %d, want 2", st.MCIDs)
	}
	// Root plus four children.
	if st.Elements != 5 {
		t.Fatalf("Elements = %d, want 5", st.Elements)
	}
}

func TestWalkStopsEarly(t *testing.T) {
	s := &store{
		cat: objects.Dict{"StructTreeRoot": objects.Ref{Num: 1}},
		objs: map[objects.Ref]objects.Object{
			{Num: 1}: objects.Dict{"K": objects.Array{
				objects.Dict{"S": objects.Name("P")},
				objects.Dict{"S": objects.Name("P")},
			}},
		},
	}
	tr, _ := Read(s)
	n := 0
	tr.Walk(func(*Elem, int) bool {
		n++
		return false
	})
	if n != 1 {
		t.Fatalf("walk visited %d, want 1", n)
	}
}

func TestNilTreeWalkIsSafe(t *testing.T) {
	var tr *Tree
	tr.Walk(func(*Elem, int) bool { return true })
	if st := (&Tree{}).Stats(); st.Elements != 0 {
		t.Fatalf("empty tree stats: %+v", st)
	}
}

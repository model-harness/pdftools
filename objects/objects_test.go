package objects

import "testing"

// fakeStore resolves refs from a fixed table, so the getters can be tested
// without a PDF file.
type fakeStore struct {
	objs map[Ref]Object
}

func (f *fakeStore) Resolve(o Object) (Object, error) {
	for i := 0; i < 8; i++ {
		ref, isRef := o.(Ref)
		if !isRef {
			return o, nil
		}
		v, ok := f.objs[ref]
		if !ok {
			return Null{}, nil
		}
		o = v
	}
	return Null{}, nil
}
func (f *fakeStore) Trailer() (Dict, error)          { return Dict{}, nil }
func (f *fakeStore) Catalog() (Dict, error)          { return Dict{}, nil }
func (f *fakeStore) PageCount() int                  { return 0 }
func (f *fakeStore) Page(int) (Dict, error)          { return nil, ErrNotFound }
func (f *fakeStore) PageContent(int) ([]byte, error) { return nil, nil }
func (f *fakeStore) Version() string                 { return "1.7" }
func (f *fakeStore) Encrypted() bool                 { return false }
func (f *fakeStore) Close() error                    { return nil }

func TestGetFollowsRefs(t *testing.T) {
	ref := Ref{Num: 5}
	s := &fakeStore{objs: map[Ref]Object{ref: Dict{"Type": Name("Page")}}}
	d, ok := GetDict(s, Dict{"Kid": ref}, "Kid")
	if !ok || d["Type"] != Name("Page") {
		t.Fatalf("did not follow ref: %v %v", d, ok)
	}
}

func TestGetTreatsNullAsAbsent(t *testing.T) {
	// A dangling reference resolves to null, and null must be indistinguishable
	// from a missing key. Otherwise callers get an empty non-nil value and treat
	// a broken file as if it had real data.
	s := &fakeStore{objs: map[Ref]Object{}}
	if _, ok := Get(s, Dict{"Gone": Ref{Num: 99}}, "Gone"); ok {
		t.Fatal("dangling ref should report absent")
	}
	if _, ok := Get(s, Dict{"Explicit": Null{}}, "Explicit"); ok {
		t.Fatal("explicit null should report absent")
	}
}

func TestGetDictAcceptsStream(t *testing.T) {
	// A stream is a dictionary with data attached; callers routinely want the
	// dictionary half, so a stream must satisfy GetDict.
	s := &fakeStore{}
	st := &Stream{Dict: Dict{"Subtype": Name("Image")}}
	d, ok := GetDict(s, Dict{"XO": st}, "XO")
	if !ok || d["Subtype"] != Name("Image") {
		t.Fatalf("stream not accepted as dict: %v %v", d, ok)
	}
}

func TestGetIntAcceptsReal(t *testing.T) {
	// Producers write integers as reals often enough that rejecting them would
	// discard real data.
	s := &fakeStore{}
	if v, ok := GetInt(s, Dict{"N": Real(42.0)}, "N"); !ok || v != 42 {
		t.Fatalf("got %v %v", v, ok)
	}
}

func TestGetWrongTypeReportsAbsent(t *testing.T) {
	s := &fakeStore{}
	if _, ok := GetArray(s, Dict{"A": Name("notAnArray")}, "A"); ok {
		t.Fatal("wrong type should report absent")
	}
}

func TestArrayOrSingle(t *testing.T) {
	if got := ArrayOrSingle(Array{Int(1), Int(2)}); len(got) != 2 {
		t.Fatalf("array should pass through, got %v", got)
	}
	if got := ArrayOrSingle(Name("X")); len(got) != 1 || got[0] != Name("X") {
		t.Fatalf("single should wrap, got %v", got)
	}
	if got := ArrayOrSingle(nil); got != nil {
		t.Fatalf("nil should stay nil, got %v", got)
	}
	if got := ArrayOrSingle(Null{}); got != nil {
		t.Fatalf("null should be empty, got %v", got)
	}
}

func TestDecodeTextString(t *testing.T) {
	tests := []struct {
		name string
		in   Object
		want string
	}{
		{"pdfdoc", String("EN-US"), "EN-US"},
		{"utf16be with BOM", String{0xFE, 0xFF, 0x00, 'E', 0x00, 'N'}, "EN"},
		{"empty", String{}, ""},
		{"bare BOM", String{0xFE, 0xFF}, ""},
		{"name", Name("en"), "en"},
		{"wrong type", Int(3), ""},
		// An odd trailing byte is malformed; drop it rather than emit U+FFFD.
		{"odd length utf16", String{0xFE, 0xFF, 0x00, 'E', 0x00}, "E"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecodeTextString(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDecodeTextStringSurrogatePair(t *testing.T) {
	// U+1F600, encoded as the surrogate pair D83D DE00.
	in := String{0xFE, 0xFF, 0xD8, 0x3D, 0xDE, 0x00}
	if got := DecodeTextString(in); got != "\U0001F600" {
		t.Fatalf("got %q (% x) want emoji", got, got)
	}
}

func TestRefString(t *testing.T) {
	if got := (Ref{Num: 12, Gen: 0}).String(); got != "12 0 R" {
		t.Fatalf("got %q", got)
	}
}

package objects

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeStore resolves refs from a fixed table, so the getters can be tested
// without a PDF file.
type fakeStore struct {
	objs      map[Ref]Object
	decodeErr error
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

// Decode stands in for a filter chain by uppercasing Raw, so a test can tell
// decoded bytes from raw ones. decodeErr makes it fail, for the path where a
// stream cannot be decoded at all.
func (f *fakeStore) Decode(s *Stream) error {
	if f.decodeErr != nil {
		return f.decodeErr
	}
	s.Decoded = []byte(strings.ToUpper(string(s.Raw)))
	return nil
}

func (f *fakeStore) Version() string { return "1.7" }
func (f *fakeStore) Encrypted() bool { return false }
func (f *fakeStore) Close() error    { return nil }

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

func TestGetStreamDataDecodesOnDemand(t *testing.T) {
	// The reason this helper exists: a stream from Resolve has Decoded nil, so
	// reading it without decoding looks like an empty stream rather than an
	// un-decoded one. Every /ToUnicode CMap in the corpus read as empty before
	// this existed.
	s := &fakeStore{}
	st := &Stream{Dict: Dict{}, Raw: []byte("cmap")}

	if got := string(st.Decoded); got != "" {
		t.Fatalf("a fresh stream should not be decoded, got %q", got)
	}
	data, ok := GetStreamData(s, Dict{"ToUnicode": st}, "ToUnicode")
	if !ok || string(data) != "CMAP" {
		t.Fatalf("got %q %v, want \"CMAP\" true", data, ok)
	}
}

func TestGetStreamDataReportsUndecodableAsAbsent(t *testing.T) {
	// An image codec is the usual reason a stream will not decode, which is
	// routine rather than an error the caller should distinguish here.
	s := &fakeStore{decodeErr: errors.New("unsupported filter")}
	if data, ok := GetStreamData(s, Dict{"S": &Stream{Dict: Dict{}, Raw: []byte("x")}}, "S"); ok {
		t.Fatalf("undecodable stream reported present with %q", data)
	}
}

func TestGetStreamDataKeepsAlreadyDecodedBytes(t *testing.T) {
	// Decoded already populated means the adapter had the bytes; decoding again
	// would be wasted work, and with a real filter chain it would fail.
	s := &fakeStore{decodeErr: errors.New("must not be called")}
	st := &Stream{Dict: Dict{}, Raw: []byte("raw"), Decoded: []byte("plain")}
	data, ok := GetStreamData(s, Dict{"S": st}, "S")
	if !ok || string(data) != "plain" {
		t.Fatalf("got %q %v, want \"plain\" true", data, ok)
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

// TestDecodeTextStringPDFDocEncoding pins Annex D.2 for the positions where PDFDocEncoding
// disagrees with a byte-for-byte reading, which is every one that matters: reinterpreting the
// bytes as UTF-8 got 137 strings in this repo's corpus wrong and made 4 of them invalid UTF-8.
func TestDecodeTextStringPDFDocEncoding(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   String
		want string
	}{
		// The three the corpus actually holds, with the text they name.
		{"bullet at 0x80", String("A \x80 B"), "A • B"},
		{"emdash at 0x84", String("Table 15 \x84 Entries"), "Table 15 — Entries"},
		{"quoteright at 0x90", String("PDF/A-3\x90s restrictions"), "PDF/A-3’s restrictions"},
		// Latin-1 above 0xA0, where a byte reading is not merely wrong but invalid UTF-8.
		{"eacute at 0xE9", String("B\xe9zier"), "Bézier"},
		// The accent block PDFDocEncoding puts where ASCII has control codes.
		{"breve at 0x18", String("\x18"), "˘"},
		// Annex D assigns these two away from where Latin-1 puts them. Neither occurs in the
		// corpus; the table is followed anyway, because the alternative is this code
		// overruling the specification on a case it cannot see.
		{"Euro at 0xA0, not NBSP", String("\xa0"), "€"},
		{"hyphen at 0xAD, not soft hyphen", String("\xad"), "-"},
		// Whitespace has no glyph name, so a table lookup alone would delete it — 192
		// carriage returns in the corpus.
		{"whitespace survives", String("a\tb\nc\rd"), "a\tb\nc\rd"},
		// A BOM still selects UTF-16BE, where 0x80 is a code unit and not a table index.
		{"utf16 is not remapped", String{0xFE, 0xFF, 0x00, 0x80}, "\u0080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecodeTextString(tc.in); got != tc.want {
				t.Fatalf("got %q (% x) want %q", got, got, tc.want)
			}
		})
	}
}

// TestDecodeTextStringIsAlwaysValidUTF8 is the property the table buys: a PDFDocEncoded string
// is a sequence of byte codes, so every one of the 256 decodes to text and no input can produce
// invalid UTF-8. Downstream code puts these in YAML values, where a raw byte is a parse error.
func TestDecodeTextStringIsAlwaysValidUTF8(t *testing.T) {
	all := make(String, 256)
	for i := range all {
		all[i] = byte(i)
	}
	got := DecodeTextString(all)
	if !utf8.ValidString(got) {
		t.Fatalf("every byte 0x00-0xFF decoded to invalid UTF-8: %q", got)
	}
	// And no byte is dropped: each one stands for at least one rune.
	if n := utf8.RuneCountInString(got); n != 256 {
		t.Errorf("got %d runes from 256 bytes, want 256 — a byte was dropped or fused", n)
	}
}

func TestRefString(t *testing.T) {
	if got := (Ref{Num: 12, Gen: 0}).String(); got != "12 0 R" {
		t.Fatalf("got %q", got)
	}
}

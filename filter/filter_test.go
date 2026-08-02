package filter

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/objects"
)

// nilStore satisfies objects.Store for the parameter lookups the filters make.
// The filters never dereference anything, so every method beyond Resolve is
// unreachable; they exist to satisfy the interface.
type nilStore struct{}

func (nilStore) Resolve(o objects.Object) (objects.Object, error) { return o, nil }
func (nilStore) Trailer() (objects.Dict, error)                   { return nil, nil }
func (nilStore) Catalog() (objects.Dict, error)                   { return nil, nil }
func (nilStore) PageCount() int                                   { return 0 }
func (nilStore) Page(int) (objects.Dict, error)                   { return nil, nil }
func (nilStore) PageContent(int) ([]byte, error)                  { return nil, nil }
func (nilStore) Version() string                                  { return "" }
func (nilStore) Encrypted() bool                                  { return false }
func (nilStore) Close() error                                     { return nil }

func TestFlateRoundTrip(t *testing.T) {
	want := []byte(strings.Repeat("BT /F1 12 Tf (hello world) Tj ET ", 200))
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(want); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	got, err := Decode("FlateDecode", buf.Bytes(), nil, nilStore{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: %d vs %d bytes", len(got), len(want))
	}
}

func TestFlateHeaderlessIsRecovered(t *testing.T) {
	// Producers emit raw deflate under a FlateDecode filter. Rejecting it loses
	// the whole page, so the decoder retries without the zlib wrapper.
	want := []byte("q 1 0 0 1 72 720 cm BT (raw deflate) Tj ET Q")
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(want)
	zw.Close()
	// Strip the 2-byte zlib header and 4-byte adler32 trailer to leave bare
	// deflate data.
	raw := buf.Bytes()[2 : buf.Len()-4]

	got, err := Decode("FlateDecode", raw, nil, nilStore{})
	if err != nil {
		t.Fatalf("headerless deflate not recovered: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFlateTruncatedKeepsPrefix(t *testing.T) {
	// A damaged file is the normal case for this toolkit. The bytes before the
	// break are usually the whole page and must survive.
	want := []byte(strings.Repeat("some recoverable content stream text ", 100))
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(want)
	zw.Close()
	truncated := buf.Bytes()[:buf.Len()/2]

	got, err := Decode("FlateDecode", truncated, nil, nilStore{})
	if err == nil {
		t.Fatal("expected an error for truncated input")
	}
	if len(got) == 0 {
		t.Fatal("truncated stream yielded nothing; the recovered prefix was discarded")
	}
	if !bytes.HasPrefix(want, got) {
		t.Fatal("recovered prefix does not match the original")
	}
}

func TestASCIIHex(t *testing.T) {
	cases := []struct{ in, want string }{
		{"48656C6C6F>", "Hello"},
		{"48 65 6C\n6C 6F >", "Hello"}, // whitespace is legal between digits
		{"48656C6C6F", "Hello"},        // missing terminator
		{"4>", "@"},                    // odd digit count pads with zero: 0x40
		{"", ""},
		{">", ""},
		{"414243>zzz", "ABC"}, // stops at '>', ignoring what follows
		// Non-hex bytes are skipped rather than aborting the stream. Note that
		// letters a-f are hex digits, so only genuinely out-of-range bytes are
		// dropped: "41!42" is "AB", but "41a42" is not.
		{"41!@#42", "AB"},
	}
	for _, c := range cases {
		got, err := Decode("ASCIIHexDecode", []byte(c.in), nil, nilStore{})
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%q -> %q, want %q", c.in, got, c.want)
		}
	}
}

func TestASCII85RoundTrip(t *testing.T) {
	want := []byte("The quick brown fox jumps over the lazy dog, 0123456789!")
	enc := make([]byte, ascii85.MaxEncodedLen(len(want)))
	n := ascii85.Encode(enc, want)
	in := append(enc[:n], '~', '>')

	got, err := Decode("ASCII85Decode", in, nil, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestASCII85ZeroGroupAndPartial(t *testing.T) {
	// 'z' abbreviates four zero bytes, and a partial final group decodes to
	// fewer than four. Both are places a hand-written decoder goes wrong.
	got, err := Decode("ASCII85Decode", []byte("z~>"), nil, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatalf("z decoded to %v, want four zero bytes", got)
	}

	// Encode a 1-byte payload: yields a 2-character group.
	one := []byte{'A'}
	enc := make([]byte, ascii85.MaxEncodedLen(len(one)))
	n := ascii85.Encode(enc, one)
	got, err = Decode("ASCII85Decode", append(enc[:n], '~', '>'), nil, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, one) {
		t.Fatalf("partial group decoded to %q, want %q", got, one)
	}
}

func TestRunLength(t *testing.T) {
	// 2 -> three literal bytes; 254 -> next byte repeated 3 times; 128 -> EOD.
	in := []byte{2, 'a', 'b', 'c', 254, 'x', 128, 'i', 'g', 'n', 'o', 'r', 'e', 'd'}
	got, err := Decode("RunLengthDecode", in, nil, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "abcxxx"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunLengthUnterminated(t *testing.T) {
	// Missing the 128 terminator is malformed, but the data is already decoded.
	got, err := Decode("RunLengthDecode", []byte{1, 'h', 'i'}, nil, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Fatalf("got %q, want %q", got, "hi")
	}
}

func TestImageFiltersAreLeftEncoded(t *testing.T) {
	// Passing image data through untouched is the design: an extractor writes a
	// JPEG out in its original encoding rather than re-encoding it.
	for _, name := range []objects.Name{"DCTDecode", "CCITTFaxDecode", "JBIG2Decode", "JPXDecode"} {
		if !IsImage(name) {
			t.Errorf("%s should be reported as an image filter", name)
		}
		data := []byte{0xFF, 0xD8, 0xFF, 0xE0}
		got, err := Decode(name, data, nil, nilStore{})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: error = %v, want ErrUnsupported", name, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("%s: data was altered", name)
		}
	}
}

func TestUnsupportedFilterReturnsInput(t *testing.T) {
	data := []byte("untouched")
	got, err := Decode("MadeUpDecode", data, nil, nilStore{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("input should be returned unchanged")
	}
}

func TestCryptIdentityIsPassThrough(t *testing.T) {
	data := []byte("plain")
	got, err := Decode("Crypt", data, nil, nilStore{})
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDecodeChainStopsAtImageFilter(t *testing.T) {
	// A Flate-then-DCT chain should yield a decompressed JPEG, not an error with
	// no data.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(jpeg)
	zw.Close()

	st := &objects.Stream{
		Dict:    objects.Dict{},
		Raw:     buf.Bytes(),
		Filters: []objects.Name{"FlateDecode", "DCTDecode"},
	}
	got, err := DecodeChain(st, nilStore{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
	if !bytes.Equal(got, jpeg) {
		t.Fatalf("chain did not apply Flate before stopping: got %v", got)
	}
}

func TestDecodeChainAppliesInOrder(t *testing.T) {
	// ASCIIHex then Flate: the outer encoding is listed first.
	payload := []byte("BT (chained) Tj ET")
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(payload)
	zw.Close()

	hex := make([]byte, 0, buf.Len()*2+1)
	const digits = "0123456789ABCDEF"
	for _, b := range buf.Bytes() {
		hex = append(hex, digits[b>>4], digits[b&0xF])
	}
	hex = append(hex, '>')

	st := &objects.Stream{
		Dict:    objects.Dict{},
		Raw:     hex,
		Filters: []objects.Name{"ASCIIHexDecode", "FlateDecode"},
	}
	got, err := DecodeChain(st, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestDecodeParmsAlignWithFilters(t *testing.T) {
	// /DecodeParms is positional against /Filter, and a null placeholder holds a
	// slot. Misaligning them applies a predictor to the wrong stage.
	st := &objects.Stream{
		Dict: objects.Dict{
			"DecodeParms": objects.Array{
				objects.Null{},
				objects.Dict{"Predictor": objects.Int(12), "Columns": objects.Int(5)},
			},
		},
		Filters: []objects.Name{"ASCIIHexDecode", "FlateDecode"},
	}
	parms := decodeParms(st, nilStore{})
	if len(parms) != 2 {
		t.Fatalf("len = %d, want 2", len(parms))
	}
	if parms[0] != nil {
		t.Error("null placeholder should yield a nil dict")
	}
	if parms[1] == nil || parms[1]["Predictor"] == nil {
		t.Error("second slot lost its predictor")
	}
}

func TestSingleDecodeParmsDictNormalizes(t *testing.T) {
	st := &objects.Stream{
		Dict:    objects.Dict{"DP": objects.Dict{"Predictor": objects.Int(2)}},
		Filters: []objects.Name{"LZWDecode"},
	}
	parms := decodeParms(st, nilStore{})
	if len(parms) != 1 || parms[0] == nil {
		t.Fatalf("abbreviated /DP not normalized: %v", parms)
	}
}

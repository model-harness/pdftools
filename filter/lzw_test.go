package filter

import (
	"bytes"
	"compress/lzw"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/objects"
)

// lzwEncode produces PDF-flavored LZW with EarlyChange=1, the default.
//
// It is written here rather than reused from the standard library because
// compress/lzw encodes with EarlyChange=0 semantics. Encoding with the library
// and decoding with the early-change path would test the two against each other
// and pass only if both were wrong the same way, so the encoder is written to the
// specification independently.
func lzwEncode(data []byte) []byte {
	type key struct {
		prev int
		b    byte
	}
	table := map[key]int{}
	next := lzwFirst
	width := 9

	var out bitWriter
	out.write(lzwClear, width)

	prev := -1
	for _, b := range data {
		if prev < 0 {
			prev = int(b)
			continue
		}
		k := key{prev, b}
		if code, ok := table[k]; ok {
			prev = code
			continue
		}
		out.write(prev, width)
		if next < lzwMax {
			table[k] = next
			next++
		}
		prev = int(b)

		// Early change: widen one code sooner than the table actually needs. Only
		// one offset applies on this side — an encoder defines each entry as it
		// consumes input, so it has none of the one-entry lag the decoder carries.
		switch {
		case next+1 > 4096:
			out.write(lzwClear, width)
			table = map[key]int{}
			next = lzwFirst
			width = 9
		case next+1 > 2048 && width < 12:
			width = 12
		case next+1 > 1024 && width < 11:
			width = 11
		case next+1 > 512 && width < 10:
			width = 10
		}
	}
	if prev >= 0 {
		out.write(prev, width)
	}
	out.write(lzwEOD, width)
	return out.bytes()
}

type bitWriter struct {
	buf  []byte
	cur  uint32
	nbit uint
}

func (w *bitWriter) write(v, n int) {
	w.cur = w.cur<<uint(n) | uint32(v)
	w.nbit += uint(n)
	for w.nbit >= 8 {
		w.nbit -= 8
		w.buf = append(w.buf, byte(w.cur>>w.nbit))
	}
}

func (w *bitWriter) bytes() []byte {
	if w.nbit > 0 {
		w.buf = append(w.buf, byte(w.cur<<(8-w.nbit)))
	}
	return w.buf
}

func TestLZWRoundTripEarlyChange(t *testing.T) {
	// Long enough to cross the 511-code boundary where early change diverges from
	// the base algorithm. Below that length both variants agree, so a short
	// fixture would pass even with the early-change logic removed.
	want := []byte(strings.Repeat("BT /F1 12 Tf (the quick brown fox) Tj ET ", 400) +
		strings.Repeat("distinct tail so the table keeps growing 0123456789 ", 200))

	got, err := lzwDecode(lzwEncode(want), true)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		// Report the divergence point: the failure mode this guards against is
		// correct output up to a code-width boundary and garbage after.
		n := 0
		for n < len(got) && n < len(want) && got[n] == want[n] {
			n++
		}
		t.Fatalf("mismatch at byte %d of %d (got %d bytes)", n, len(want), len(got))
	}
}

func TestLZWCrossesEveryCodeWidth(t *testing.T) {
	// Data with enough distinct sequences to fill past 2048 codes, exercising all
	// three width transitions rather than just the first.
	var sb strings.Builder
	for i := 0; i < 3000; i++ {
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteString(string(rune('a' + i%26)))
		sb.WriteByte(byte('0' + i%10))
	}
	want := []byte(sb.String())

	got, err := lzwDecode(lzwEncode(want), true)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		n := 0
		for n < len(got) && n < len(want) && got[n] == want[n] {
			n++
		}
		t.Fatalf("mismatch at byte %d of %d", n, len(want))
	}
}

func TestLZWStandardLibraryDiffersFromPDF(t *testing.T) {
	// The reason this decoder exists. compress/lzw implements EarlyChange=0; PDF
	// defaults to 1. If the two ever agreed on long input, this package could be
	// deleted in favor of the standard library, so the difference is asserted
	// rather than assumed.
	want := []byte(strings.Repeat("abcdefghijklmnopqrstuvwxyz0123456789", 100))

	var buf bytes.Buffer
	zw := lzw.NewWriter(&buf, lzw.MSB, 8)
	if _, err := zw.Write(want); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	// Decoding library-encoded data with the early-change path should NOT match.
	got, err := lzwDecode(buf.Bytes(), true)
	if err == nil && bytes.Equal(got, want) {
		t.Fatal("early-change decode matched EarlyChange=0 data: " +
			"the variants no longer differ, so this decoder may be unnecessary")
	}

	// With early change off, it must match, which proves the decoder is correct
	// against an independent implementation.
	got, err = lzwDecode(buf.Bytes(), false)
	if err != nil {
		t.Fatalf("EarlyChange=0 decode failed against compress/lzw output: %v", err)
	}
	if !bytes.Equal(got, want) {
		n := 0
		for n < len(got) && n < len(want) && got[n] == want[n] {
			n++
		}
		t.Fatalf("EarlyChange=0 mismatch at byte %d of %d", n, len(want))
	}
}

func TestLZWEarlyChangeParamIsRead(t *testing.T) {
	// EarlyChange comes from /DecodeParms and defaults to 1 when absent.
	want := []byte(strings.Repeat("parameter routing check ", 300))
	var buf bytes.Buffer
	zw := lzw.NewWriter(&buf, lzw.MSB, 8)
	zw.Write(want)
	zw.Close()

	params := objects.Dict{"EarlyChange": objects.Int(0)}
	got, err := Decode("LZWDecode", buf.Bytes(), params, nilStore{})
	if err != nil {
		t.Fatalf("decode with EarlyChange=0: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("EarlyChange=0 from DecodeParms was not honored")
	}
}

func TestLZWClearTableMidStream(t *testing.T) {
	// A clear code resets the dictionary. Mishandling it corrupts everything
	// after, and long streams do emit one.
	part := []byte(strings.Repeat("xy", 50))
	var w bitWriter
	w.write(lzwClear, 9)
	for _, b := range part {
		w.write(int(b), 9)
	}
	w.write(lzwClear, 9) // reset mid-stream
	for _, b := range part {
		w.write(int(b), 9)
	}
	w.write(lzwEOD, 9)

	got, err := lzwDecode(w.bytes(), true)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := append(append([]byte{}, part...), part...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %d bytes, want %d", len(got), len(want))
	}
}

func TestLZWTruncatedKeepsPrefix(t *testing.T) {
	want := []byte(strings.Repeat("recoverable prefix content ", 200))
	enc := lzwEncode(want)
	got, err := lzwDecode(enc[:len(enc)/2], true)
	// Either outcome is acceptable; discarding the recovered prefix is not.
	if len(got) == 0 {
		t.Fatalf("truncated LZW yielded nothing (err %v)", err)
	}
	if !bytes.HasPrefix(want, got) {
		t.Fatal("recovered prefix does not match the original")
	}
}

func TestLZWBadCodeStopsCleanly(t *testing.T) {
	// A code past the end of the table means the dictionary is unrecoverable.
	// It must return an error rather than panic on an out-of-range index.
	var w bitWriter
	w.write(lzwClear, 9)
	w.write(int('A'), 9)
	w.write(300, 9) // far beyond next (259)
	w.write(lzwEOD, 9)

	got, err := lzwDecode(w.bytes(), true)
	if err == nil {
		t.Fatal("expected an error for an out-of-table code")
	}
	if string(got) != "A" {
		t.Fatalf("got %q, want the decoded prefix %q", got, "A")
	}
}

func TestLZWEmptyInput(t *testing.T) {
	got, err := lzwDecode(nil, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %q, %v", got, err)
	}
}

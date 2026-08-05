package filter

import (
	"bytes"
	"compress/zlib"
	"testing"

	"github.com/model-harness/pdftools/objects"
)

// Stream data is untrusted, and every decoder here is a hand-written loop over
// attacker-controlled bytes. These targets assert termination without panicking,
// plus the size cap, which is the property that keeps a crafted stream from
// becoming a memory-exhaustion denial of service.
//
// Run longer with:
//
//	go test ./filter/ -run xxx -fuzz FuzzDecode -fuzztime 5m

func FuzzDecode(f *testing.F) {
	var flated bytes.Buffer
	zw := zlib.NewWriter(&flated)
	zw.Write([]byte("BT /F1 12 Tf (hello) Tj ET"))
	zw.Close()

	seeds := [][]byte{
		flated.Bytes(),
		[]byte("48656C6C6F>"),
		[]byte("87cURD]i,\"Ebo80~>"),
		{2, 'a', 'b', 'c', 254, 'x', 128},
		{0x80, 0x00, 0x40, 0x20}, // LZW-shaped bits
		{},
		{0xFF, 0xD8, 0xFF, 0xE0}, // JPEG header
	}
	names := []objects.Name{
		"FlateDecode", "LZWDecode", "ASCIIHexDecode", "ASCII85Decode",
		"RunLengthDecode", "DCTDecode", "Crypt", "NotAFilter",
	}
	for _, s := range seeds {
		for _, n := range names {
			f.Add(string(n), s)
		}
	}

	f.Fuzz(func(t *testing.T, name string, data []byte) {
		out, _ := Decode(objects.Name(name), data, nil, nilStore{})
		// A decoder must never exceed the cap, whatever the input claims. Partial
		// output on error is intentional, so the error itself is not asserted.
		if len(out) > maxDecoded {
			t.Fatalf("%s produced %d bytes, above the %d cap", name, len(out), maxDecoded)
		}
	})
}

func FuzzLZWDecode(f *testing.F) {
	// LZW is the one decoder written from scratch rather than wrapping the standard
	// library, and its table indexing is where an out-of-range code would panic.
	f.Add([]byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01}, true)
	f.Add([]byte{0x80, 0x00}, false)
	f.Add([]byte{}, true)
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, true)

	f.Fuzz(func(t *testing.T, data []byte, earlyChange bool) {
		out, _ := lzwDecode(data, earlyChange)
		if len(out) > maxDecoded {
			t.Fatalf("produced %d bytes, above the %d cap", len(out), maxDecoded)
		}
	})
}

func FuzzPredictor(f *testing.F) {
	// The predictor's geometry comes from the stream dictionary, so /Columns,
	// /Colors, and /BitsPerComponent are all attacker-controlled and all feed an
	// allocation size and several loop bounds.
	f.Add([]byte{0, 1, 2, 3, 4, 1, 5, 6, 7, 8}, 12, 1, 8, 4)
	f.Add([]byte{2, 9, 9, 9}, 15, 3, 8, 1)
	f.Add([]byte{1, 2, 3}, 2, 3, 8, 1)
	f.Add([]byte{4, 1, 2, 3, 4}, 14, 1, 1, 32)

	f.Fuzz(func(t *testing.T, data []byte, pred, colors, bpc, columns int) {
		params := objects.Dict{
			"Predictor":        objects.Int(pred),
			"Colors":           objects.Int(colors),
			"BitsPerComponent": objects.Int(bpc),
			"Columns":          objects.Int(columns),
		}
		out, _ := applyPredictor(data, params, nilStore{})
		if len(out) > maxDecoded {
			t.Fatalf("produced %d bytes, above the %d cap", len(out), maxDecoded)
		}
	})
}

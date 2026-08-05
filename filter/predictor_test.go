package filter

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/model-harness/pdftools/objects"
)

// pngEncodeRows applies PNG filtering to rows, producing the stride-prefixed
// layout applyPredictor reverses. ft selects the filter type for every row.
func pngEncodeRows(rows [][]byte, bpp int, ft byte) []byte {
	rowLen := len(rows[0])
	prev := make([]byte, rowLen)
	var out []byte

	for _, row := range rows {
		enc := make([]byte, rowLen)
		for i := 0; i < rowLen; i++ {
			var left, upLeft byte
			if i >= bpp {
				left = row[i-bpp]
				upLeft = prev[i-bpp]
			}
			switch ft {
			case 0:
				enc[i] = row[i]
			case 1:
				enc[i] = row[i] - left
			case 2:
				enc[i] = row[i] - prev[i]
			case 3:
				enc[i] = row[i] - byte((int(left)+int(prev[i]))/2)
			case 4:
				enc[i] = row[i] - paeth(left, prev[i], upLeft)
			}
		}
		out = append(out, ft)
		out = append(out, enc...)
		prev = row
	}
	return out
}

func flatten(rows [][]byte) []byte {
	var out []byte
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

func TestPNGPredictorAllFilterTypes(t *testing.T) {
	// Three rows of 3-byte RGB pixels, four pixels wide.
	rows := [][]byte{
		{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120},
		{15, 25, 35, 45, 55, 65, 75, 85, 95, 105, 115, 125},
		{200, 190, 180, 170, 160, 150, 140, 130, 120, 110, 100, 90},
	}
	want := flatten(rows)
	const bpp = 3

	for ft := byte(0); ft <= 4; ft++ {
		enc := pngEncodeRows(rows, bpp, ft)
		got, err := pngPredictor(enc, bpp, len(rows[0]))
		if err != nil {
			t.Errorf("filter type %d: %v", ft, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("filter type %d: got %v, want %v", ft, got, want)
		}
	}
}

func TestPNGPredictorAgainstImagePNG(t *testing.T) {
	// Decoding against the standard library's PNG encoder tests this predictor
	// against an independent implementation rather than only against the encoder
	// written above, which shares my assumptions and would hide a shared error.
	const w, h = 8, 6
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 31), G: uint8(y * 41), B: uint8(x*y + 7), A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	// Re-decode to recover the exact pixel bytes the encoder committed to.
	decoded, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	raw := pngIDAT(t, buf.Bytes())
	// The image is fully opaque, so png.Encode drops the alpha channel and writes
	// 8-bit truecolor: three bytes per pixel, not four. Getting this wrong is what
	// a wrong /Colors or /BitsPerComponent does to a real stream, and the symptom
	// is a plausible byte count rather than an error.
	const bpp = 3
	got, err := pngPredictor(raw, bpp, w*bpp)
	if err != nil {
		t.Fatalf("predictor: %v", err)
	}
	if len(got) != w*h*bpp {
		t.Fatalf("got %d bytes, want %d", len(got), w*h*bpp)
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * bpp
			r, g, b, _ := decoded.At(x, y).RGBA()
			if got[off] != uint8(r>>8) || got[off+1] != uint8(g>>8) || got[off+2] != uint8(b>>8) {
				t.Fatalf("pixel (%d,%d): got %v, want %d %d %d",
					x, y, got[off:off+3], r>>8, g>>8, b>>8)
			}
		}
	}
}

// pngIDAT concatenates and inflates a PNG's IDAT chunks, yielding the filtered
// scanlines.
func pngIDAT(t *testing.T, data []byte) []byte {
	t.Helper()
	var idat []byte
	// Skip the 8-byte signature, then walk length-type-data-crc chunks.
	for off := 8; off+8 <= len(data); {
		n := int(data[off])<<24 | int(data[off+1])<<16 | int(data[off+2])<<8 | int(data[off+3])
		typ := string(data[off+4 : off+8])
		start := off + 8
		if start+n > len(data) {
			break
		}
		if typ == "IDAT" {
			idat = append(idat, data[start:start+n]...)
		}
		off = start + n + 4
	}
	if len(idat) == 0 {
		t.Fatal("no IDAT chunks found")
	}
	zr, err := zlib.NewReader(bytes.NewReader(idat))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out, err := readCapped(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPNGPredictorSubByteDepth(t *testing.T) {
	// 1 bit per component: bpp floors to 1, so Sub predicts against the preceding
	// byte. A zero stride here would make the loop self-referential.
	params := objects.Dict{
		"Predictor":        objects.Int(15),
		"Colors":           objects.Int(1),
		"BitsPerComponent": objects.Int(1),
		"Columns":          objects.Int(16),
	}
	// Two rows of 2 bytes, both Sub-filtered.
	data := []byte{1, 0x0F, 0x10, 1, 0x01, 0x02}
	got, err := applyPredictor(data, params, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	// Sub: out[0]=0x0F, out[1]=0x10+0x0F=0x1F; row 2: 0x01, 0x02+0x01=0x03.
	want := []byte{0x0F, 0x1F, 0x01, 0x03}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestTIFFPredictor(t *testing.T) {
	// Horizontal differencing across 3 colors, no per-row filter byte.
	params := objects.Dict{
		"Predictor":        objects.Int(2),
		"Colors":           objects.Int(3),
		"BitsPerComponent": objects.Int(8),
		"Columns":          objects.Int(2),
	}
	// Row of two RGB pixels: (10,20,30) then delta (5,5,5) -> (15,25,35).
	data := []byte{10, 20, 30, 5, 5, 5}
	got, err := applyPredictor(data, params, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 20, 30, 15, 25, 35}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTIFFPredictorRejectsSubByteDepth(t *testing.T) {
	params := objects.Dict{
		"Predictor":        objects.Int(2),
		"BitsPerComponent": objects.Int(4),
		"Columns":          objects.Int(4),
	}
	data := []byte{1, 2, 3, 4}
	got, err := applyPredictor(data, params, nilStore{})
	if err == nil {
		t.Fatal("expected an error for 4-bit TIFF prediction")
	}
	if !bytes.Equal(got, data) {
		t.Fatal("unsupported depth should return the input unchanged")
	}
}

func TestPredictorAbsentOrIdentity(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5}
	// No params at all.
	got, err := applyPredictor(data, nil, nilStore{})
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("nil params: got %v, %v", got, err)
	}
	// Predictor 1 means no prediction.
	got, err = applyPredictor(data, objects.Dict{"Predictor": objects.Int(1)}, nilStore{})
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("predictor 1: got %v, %v", got, err)
	}
	// A params dict with no Predictor key.
	got, err = applyPredictor(data, objects.Dict{"Columns": objects.Int(4)}, nilStore{})
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("no Predictor key: got %v, %v", got, err)
	}
}

func TestPredictorRejectsImplausibleGeometry(t *testing.T) {
	// A crafted /Columns is an allocation size. Without the guard this is an
	// out-of-memory abort rather than an error, which is a denial of service on
	// any process that opens the file.
	cases := []objects.Dict{
		{"Predictor": objects.Int(12), "Columns": objects.Int(1 << 40)},
		{"Predictor": objects.Int(12), "Colors": objects.Int(1 << 30), "Columns": objects.Int(4)},
		{"Predictor": objects.Int(12), "BitsPerComponent": objects.Int(1 << 20)},
		{"Predictor": objects.Int(12), "Columns": objects.Int(-1)},
		{"Predictor": objects.Int(12), "Colors": objects.Int(0)},
	}
	data := []byte{1, 2, 3, 4}
	for i, params := range cases {
		got, err := applyPredictor(data, params, nilStore{})
		if err == nil {
			t.Errorf("case %d: expected an error, got %v", i, got)
		}
	}
}

func TestPredictorRejectsGeometryThatOnlyOverflowsWhenMultiplied(t *testing.T) {
	// Every factor here is inside its own bound — colors 32, bpc 32, and columns
	// exactly the permitted 2^24 — while the product is a 2 GiB row. Bounding the
	// factors individually let this through, so four bytes of input allocated three
	// multi-gigabyte buffers (out, prev, cur) and the process died on memory rather
	// than returning an error. The cases above all trip a per-factor bound and so
	// never exercised the product at all.
	params := objects.Dict{
		"Predictor":        objects.Int(12),
		"Colors":           objects.Int(32),
		"BitsPerComponent": objects.Int(32),
		"Columns":          objects.Int(1 << 24),
	}
	got, err := applyPredictor([]byte{0, 1, 2, 3}, params, nilStore{})
	if err == nil {
		t.Fatalf("expected an error for a 2 GiB row, got %d bytes", len(got))
	}
	if len(got) > len([]byte{0, 1, 2, 3}) {
		t.Fatalf("rejected geometry still allocated %d bytes", len(got))
	}

	// The bound must not reject real geometry. A CMYK scanline 10,000 pixels wide
	// is 40,000 bytes, and a cross-reference stream row is a handful.
	for _, ok := range []objects.Dict{
		{"Predictor": objects.Int(12), "Colors": objects.Int(4),
			"BitsPerComponent": objects.Int(8), "Columns": objects.Int(10000)},
		{"Predictor": objects.Int(12), "Colors": objects.Int(1),
			"BitsPerComponent": objects.Int(8), "Columns": objects.Int(5)},
	} {
		if _, err := applyPredictor([]byte{0, 1, 2, 3}, ok, nilStore{}); err != nil {
			t.Errorf("plausible geometry %v rejected: %v", ok, err)
		}
	}
}

func TestPNGPredictorUnknownFilterTypeKeepsRows(t *testing.T) {
	// Two good rows then a bad filter byte. The recovered rows must survive: this
	// is usually a truncated or mislabeled stream, not a total loss.
	rows := [][]byte{{1, 2, 3, 4}, {5, 6, 7, 8}}
	enc := pngEncodeRows(rows, 1, 0)
	enc = append(enc, 99, 0, 0, 0, 0) // filter type 99 does not exist

	got, err := pngPredictor(enc, 1, 4)
	if err == nil {
		t.Fatal("expected an error for an unknown filter type")
	}
	if !bytes.Equal(got, flatten(rows)) {
		t.Fatalf("recovered rows lost: got %v, want %v", got, flatten(rows))
	}
}

func TestPNGPredictorShortFinalRow(t *testing.T) {
	// A truncated final row must not read the previous row's leftovers, which
	// would produce plausible-looking bytes that are wrong.
	rows := [][]byte{{9, 9, 9, 9}}
	enc := pngEncodeRows(rows, 1, 0)
	enc = append(enc, 0, 1, 2) // filter byte + 2 of 4 bytes

	got, err := pngPredictor(enc, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{9, 9, 9, 9, 1, 2, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPredictorAppliesAfterFlate(t *testing.T) {
	// Decode must run the predictor on the inflated bytes. Skipping it yields
	// data that looks valid but is off by each byte's neighbor, and every
	// cross-reference stream in a modern PDF is predicted this way.
	rows := [][]byte{{1, 2, 3, 4, 5}, {6, 7, 8, 9, 10}}
	enc := pngEncodeRows(rows, 1, 1) // Sub

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(enc)
	zw.Close()

	params := objects.Dict{
		"Predictor": objects.Int(12),
		"Columns":   objects.Int(5),
	}
	got, err := Decode("FlateDecode", buf.Bytes(), params, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, flatten(rows)) {
		t.Fatalf("got %v, want %v", got, flatten(rows))
	}
}

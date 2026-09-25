// Package iccbuild assembles minimal ICC profiles, for tests.
//
// It exists for the reason internal/pdfbuild does: a tag table of offsets and sizes, each tag
// four-byte aligned, is the only real logic in a hand-built profile, and two packages need it — icc,
// which parses profiles, and render/native, which draws through them and is compared with pdfium.
// The corpus's own profiles cannot be committed as fixtures, because they come out of PDFs that
// cannot be redistributed, so every profile a test uses is built here.
//
// Nothing is validated. A fixture's purpose is often to be malformed in one specific way, and a
// caller that needs one patches the bytes this returns: the header's class is at offset 12, the data
// colour space at 16, the connection space at 20, and the 'acsp' signature at 36.
package iccbuild

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Tag is one tagged element: its signature and its bytes, type signature first.
type Tag struct {
	Sig  string
	Data []byte
}

// Profile returns a version 2 display profile for the data colour space given ("GRAY" or "RGB "),
// with an XYZ connection space and the tags given after a description, a D50 media white point and
// a copyright, which is what a real profile carries ahead of the tags that matter.
// #nosec G115 -- every count and offset is a test fixture's, bytes long and nowhere near 1<<32
func Profile(space string, tags ...Tag) []byte {
	var desc bytes.Buffer
	desc.WriteString("desc")
	be32(&desc, 0)
	be32(&desc, 5)
	desc.WriteString("test\x00")
	be32(&desc, 0)
	be32(&desc, 0)
	desc.Write(make([]byte, 2+1+67))
	var cprt bytes.Buffer
	cprt.WriteString("text")
	be32(&cprt, 0)
	cprt.WriteString("none\x00\x00\x00\x00")
	tags = append([]Tag{{"desc", desc.Bytes()}, {"wtpt", XYZ(0.9642, 1, 0.8249)}, {"cprt", cprt.Bytes()}}, tags...)

	off := 128 + 4 + 12*len(tags)
	var table, body bytes.Buffer
	be32(&table, uint32(len(tags)))
	for _, t := range tags {
		for body.Len()%4 != 0 {
			body.WriteByte(0)
		}
		table.WriteString(t.Sig)
		be32(&table, uint32(off+body.Len()))
		be32(&table, uint32(len(t.Data)))
		body.Write(t.Data)
	}

	var h bytes.Buffer
	be32(&h, uint32(128+table.Len()+body.Len()))
	h.WriteString("none")
	be32(&h, 0x02100000)
	h.WriteString("mntr" + space + "XYZ ")
	h.Write(make([]byte, 12))
	h.WriteString("acsp")
	h.Write(make([]byte, 4+4+4+4+8+4))
	// The header's PCS illuminant, D50, as s15Fixed16.
	be32(&h, 63190)
	be32(&h, 65536)
	be32(&h, 54061)
	h.Write(make([]byte, 128-h.Len()))
	return append(append(h.Bytes(), table.Bytes()...), body.Bytes()...)
}

// Gray is a gray profile whose tone curve is a pure gamma.
func Gray(gamma float64) []byte {
	return Profile("GRAY", Tag{"kTRC", Gamma(gamma)})
}

// RGB is a matrix/TRC profile with one gamma for all three channels, and colorants r, g and b given
// as D50-relative XYZ.
func RGB(gamma float64, r, g, b [3]float64) []byte {
	c := Gamma(gamma)
	return Profile("RGB ",
		Tag{"rXYZ", XYZ(r[0], r[1], r[2])}, Tag{"gXYZ", XYZ(g[0], g[1], g[2])}, Tag{"bXYZ", XYZ(b[0], b[1], b[2])},
		Tag{"rTRC", c}, Tag{"gTRC", c}, Tag{"bTRC", c})
}

// XYZ is an XYZType tag holding one value.
func XYZ(x, y, z float64) []byte {
	var b bytes.Buffer
	b.WriteString("XYZ ")
	be32(&b, 0)
	be32(&b, s15(x))
	be32(&b, s15(y))
	be32(&b, s15(z))
	return b.Bytes()
}

// Gamma is a curveType tag of one entry, which the ICC specification reads as a gamma in u8Fixed8.
func Gamma(g float64) []byte {
	var b bytes.Buffer
	b.WriteString("curv")
	be32(&b, 0)
	be32(&b, 1)
	be16(&b, uint16(math.Round(g*256)))
	b.Write([]byte{0, 0})
	return b.Bytes()
}

// Curve is a curveType tag holding a sampled table; no entries at all is the identity.
// #nosec G115 -- a test fixture's table, nowhere near 1<<32 entries
func Curve(table ...uint16) []byte {
	var b bytes.Buffer
	b.WriteString("curv")
	be32(&b, 0)
	be32(&b, uint32(len(table)))
	for _, v := range table {
		be16(&b, v)
	}
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

// Para is a parametricCurveType tag of the function type given, with its parameters in order.
func Para(fn uint16, params ...float64) []byte {
	var b bytes.Buffer
	b.WriteString("para")
	be32(&b, 0)
	be16(&b, fn)
	be16(&b, 0)
	for _, p := range params {
		be32(&b, s15(p))
	}
	return b.Bytes()
}

func be32(b *bytes.Buffer, v uint32) { _ = binary.Write(b, binary.BigEndian, v) }

func be16(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.BigEndian, v) }

// s15 is s15Fixed16Number, the ICC's signed 16.16 fixed point.
// #nosec G115 -- the field is defined signed, so the conversion reinterprets its bits, as icc's s15 reads them back
func s15(v float64) uint32 { return uint32(int32(math.Round(v * 65536))) }

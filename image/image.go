// Package image reads image XObjects out of a PDF (ISO 32000-2 §8.9).
//
// The governing rule is that the original codec survives wherever it can. 56 of
// the 245 images in this repo's corpus are DCTDecode, and the largest is
// 6049×4090; decoding one to pixels and re-encoding it would cost a generational
// quality loss and several seconds for a file that was already a perfectly good
// JPEG. So a DCT image is written as the .jpg it already is, byte for byte, and
// only the codecs with no standalone container — Flate and LZW packed samples,
// CCITT, and raw unfiltered data — are decoded and re-encoded as PNG.
//
// What this package does not do is composite. A soft mask stays a separate image
// with its own file, because flattening it against a background is a rendering
// decision and this is an extractor: 136 of the corpus's 143 soft masks carry
// /Matte [0 0 0], meaning their base samples are premultiplied against black, and
// the only honest thing an extractor can do with that is say so and hand over
// both layers. Compositing belongs above, in render.
//
// Positioning is also absent. An image XObject is placed by the CTM in force at
// its Do operator, and the same XObject may be drawn many times at different
// sizes. Extraction is per-object, not per-placement, which is why Images
// deduplicates by indirect reference: one logo drawn on all 1,023 pages of the
// specification is one image, not 1,023.
package image

import (
	"errors"
	"fmt"

	"github.com/3rg0n/pdf-spec/objects"
)

// ErrUnsupported reports an image whose codec this package cannot turn into a
// file. JPXDecode and JBIG2Decode are the two: both are real codecs with no Go
// decoder in the borrow table (docs/DESIGN.md §5), and neither appears anywhere
// in this repo's corpus.
var ErrUnsupported = errors.New("image: unsupported codec")

// Codec is how an image's samples are encoded in the file.
type Codec int

const (
	// CodecRaw is unfiltered samples, or samples behind byte-oriented filters
	// (Flate, LZW, ASCIIHex, ASCII85, RunLength) that filter already decodes.
	// The bytes are packed component samples and need a container.
	CodecRaw Codec = iota

	// CodecJPEG is DCTDecode. The bytes are a complete JFIF/JPEG stream.
	CodecJPEG

	// CodecCCITT is CCITTFaxDecode: a bilevel Group 3 or Group 4 fax stream.
	CodecCCITT

	// CodecJBIG2 is JBIG2Decode, and CodecJPX is JPXDecode. Both are recognized
	// and named so a report can say what a file contains, and neither can be
	// written out — see ErrUnsupported.
	CodecJBIG2
	CodecJPX
)

func (c Codec) String() string {
	switch c {
	case CodecRaw:
		return "raw"
	case CodecJPEG:
		return "jpeg"
	case CodecCCITT:
		return "ccitt"
	case CodecJBIG2:
		return "jbig2"
	case CodecJPX:
		return "jpx"
	}
	return "unknown"
}

// Image is one image XObject, described but not yet decoded.
//
// Data holds the stream's bytes with every byte-oriented filter already applied
// and the image codec, if any, still in place — the split filter.DecodeChain
// produces. For CodecJPEG that makes Data a complete JPEG file. For CodecRaw it
// makes Data packed samples that Encode must unpack.
type Image struct {
	// Ref identifies the XObject, and is the identity Images deduplicates on. The
	// zero Ref means the image was a direct object, which is legal and rare.
	Ref objects.Ref

	// Name is the resource name the image was reached under, and Page the 1-based
	// page it was first found on. Both are for naming the output file and for
	// telling a user where an image came from; neither is an identity, because the
	// same XObject appears under different names on different pages.
	Name objects.Name
	Page int

	Codec Codec
	Data  []byte

	// Width and Height are in samples, and are the only two fields §8.9.5 makes
	// mandatory.
	Width, Height int

	// BitsPerComponent is 1, 2, 4, 8, or 16 — and 1 for a stencil mask, where the
	// key may legally be absent.
	BitsPerComponent int

	// ColorSpaceFamily is the family name: DeviceRGB, DeviceGray, DeviceCMYK,
	// ICCBased, Indexed, and so on. The family is what decides how many components
	// a sample has, which is all this package needs.
	ColorSpaceFamily objects.Name

	// Components is the number of colour components per sample, derived from the
	// colour space. Zero when the colour space could not be understood, which
	// blocks re-encoding but not passthrough of an already-containerized codec.
	Components int

	// Palette is an /Indexed colour space's lookup table, and Base and HiVal its
	// base family and highest index. Non-nil only for CodecRaw indexed images.
	Palette []byte
	Base    objects.Name
	HiVal   int

	// Stencil reports /ImageMask true: a 1-bit mask that paints the current colour
	// through its unmasked samples rather than carrying colour of its own.
	Stencil bool

	// Decode is /Decode, the array that remaps sample values. Carried rather than
	// applied here because for a stencil mask [1 0] inverts the sense of the mask,
	// and that inversion has to reach the encoder.
	Decode []float64

	// SMask is the soft mask from /SMask: a DeviceGray image giving per-sample
	// alpha. Kept as a nested Image rather than merged, per the package comment.
	SMask *Image

	// CCITT holds the /DecodeParms a CCITT stream needs, with the spec's defaults
	// applied. Meaningful only for CodecCCITT: unlike a JPEG, a fax stream carries
	// no header stating its width or variant, so these are not recoverable from the
	// data.
	CCITT CCITTParams

	// Matte, when non-nil, is /Matte on a soft mask: the colour the *base* image's
	// samples are premultiplied against. Its presence means the base samples are
	// not the colours they appear to be, and any consumer compositing them must
	// un-premultiply first. Present on 136 of this corpus's 143 soft masks, all
	// [0 0 0].
	Matte []float64
}

// Alpha reports whether the image carries transparency, through a soft mask or a
// stencil.
func (im *Image) Alpha() bool { return im.SMask != nil || im.Stencil }

// Premultiplied reports whether the samples are premultiplied against a matte
// colour and so must be un-premultiplied before use.
func (im *Image) Premultiplied() bool { return im.SMask != nil && im.SMask.Matte != nil }

// Ext is the file extension Encode will produce, without the dot.
func (im *Image) Ext() (string, error) {
	switch im.Codec {
	case CodecJPEG:
		return "jpg", nil
	case CodecRaw, CodecCCITT:
		return "png", nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupported, im.Codec)
}

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
// decision and this is an extractor. Compositing belongs above, in render.
//
// Un-premultiplying is not compositing, though, and it does happen here. 136 of the
// corpus's 143 soft masks carry /Matte [0 0 0], meaning their base samples were
// pre-blended against black (§11.6.5.3), and writing those samples unchanged into a
// non-premultiplied PNG mislabels them. The inversion has to be in this package
// rather than above it because the spec orders it: "If a colour conversion is
// required, inversion of the pre-blending shall precede the colour conversion" — so
// it must run on samples still in the parent image's own colour space, which is a
// state that exists only inside the decoder. Recoverable reports the five images
// where it cannot be done.
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
	// samples are premultiplied against. Its presence means the samples as the file
	// holds them are not the colours they appear to be. Encode inverts that where it
	// can, so a consumer of the encoded output does not have to — Recoverable says
	// when it could not. A consumer reading Data directly still does.
	//
	// The components are in the parent image's colour space and in post-/Decode units:
	// Table 144 requires "valid colour components in that colour space", and the
	// pre-blending "shall use actual colour component values, with the effects of the
	// Filter and Decode transformations already performed". Present on 136 of this
	// corpus's 143 soft masks, all [0 0 0].
	Matte []float64
}

// Alpha reports whether the image carries transparency, through a soft mask or a
// stencil.
func (im *Image) Alpha() bool { return im.SMask != nil || im.Stencil }

// Premultiplied reports whether the file's samples are pre-blended against a matte
// colour (§11.6.5.3).
//
// This describes the stream, not the output. Encode inverts the blend for every image
// where the inversion is defined, so a PNG this package writes holds true colours — see
// Recoverable for the distinction, which is the one that tells a consumer whether it is
// looking at recovered colour or at samples that had to be passed through.
func (im *Image) Premultiplied() bool { return im.SMask != nil && im.SMask.Matte != nil }

// Recoverable reports whether Encode can invert the pre-blending on this image.
//
// False on a premultiplied image means the samples reach the output still blended
// against the matte, because inverting needs the per-pixel alpha as its divisor and this
// image's mask cannot supply it — a DCT-coded mask would have to go through the JPEG
// decoder to become alpha, and a DCT-coded base is never decoded at all. It is also
// false for an Indexed parent, whose pre-blending applies to the palette rather than to
// the index a sample carries (§11.6.5.3), and for Lab.
//
// Lab is excluded for a reason particular to this package rather than to the format. The
// matte is in real Lab units — L runs 0..100 — because that is Lab's default /Decode, and
// decodeSamples does not apply it: labToRGB takes a sample normalized to 0..1 and scales
// it itself. So the sample reaching the inversion is on a different scale from the matte,
// and dividing one by the other mixes them silently. Applying Lab's default /Decode would
// resolve it and would also change labToRGB's contract, which no corpus image needs.
//
// Always true when Premultiplied is false: an image that was never blended needs no
// recovery, so there is nothing to fail at.
func (im *Image) Recoverable() bool {
	if !im.Premultiplied() {
		return true
	}
	if im.Codec != CodecRaw || im.SMask.Codec != CodecRaw {
		return false
	}
	if len(im.SMask.Matte) != im.Components {
		return false
	}
	// Table 143 requires a mask carrying /Matte to have the same Width and Height as its
	// parent, and every one of this corpus's 136 does. A file that breaks the rule is one
	// where the α that would divide is not the α that multiplied: the mask can still be
	// scaled to supply per-pixel alpha for the output's transparency, which is a
	// resampling, but dividing a colour by a resampled weight is inventing the weight.
	// Refusing keeps a malformed file's samples as they are instead of corrupting them.
	if im.SMask.Width != im.Width || im.SMask.Height != im.Height {
		return false
	}
	return im.ColorSpaceFamily != "Indexed" && im.ColorSpaceFamily != "Lab"
}

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

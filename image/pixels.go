package image

import (
	"bytes"
	"fmt"
	stdimage "image"
	"image/color"
	"image/jpeg"

	"github.com/model-harness/pdftools/objects"
)

// Read describes one image XObject stream, the way a Reader describes the ones it finds by walking
// a page's resources.
//
// A renderer reaches an image through a Do operator rather than through a resource walk, and needs
// exactly the XObject that operator names — not the document-wide, deduplicated list a Reader
// yields, where an image drawn a second time is not reported at all.
func Read(s objects.Store, st *objects.Stream) (*Image, error) {
	return NewReader(s).image(st, objects.Ref{}, "", 0)
}

// Pixels decodes an image to non-premultiplied colour, with its soft mask as the alpha channel.
//
// This is the form a compositor wants, and it is still not compositing: nothing here knows where
// the image is placed or what it is drawn over. The package comment's line holds — flattening is
// render's — and what moves here is only the part the extractor already had to get right, the
// samples and the matte inversion, which a renderer would otherwise have to reimplement.
//
// It is stricter than Encode, and on purpose. Encode writes what it can and passes the rest through
// as a file, because a user holding the file can still look at it; a rasterizer that does the same
// puts a wrong picture on the page with nothing to say so. So every case Encode tolerates by
// degrading is an error here: a stencil, which has no colour until a fill colour is supplied; a
// premultiplied image whose blend cannot be inverted; a DCT image in CMYK, whose Adobe inversion
// convention the stdlib decoder does not follow; and a DCT image with a /Decode array, which the
// decoder would silently ignore.
func Pixels(im *Image) (*stdimage.NRGBA, error) {
	switch {
	case im.Stencil:
		return nil, fmt.Errorf("%w: a stencil mask paints the fill colour and has none of its own", ErrUnsupported)
	case im.Premultiplied() && !im.invertible():
		return nil, fmt.Errorf("%w: samples pre-blended against /Matte that cannot be inverted", ErrUnsupported)
	}

	var base stdimage.Image
	var err error
	switch im.Codec {
	case CodecRaw:
		base, err = decodeRaw(im)
	case CodecJPEG:
		base, err = decodeJPEG(im)
	case CodecCCITT:
		base, err = decodeCCITT(im)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, im.Codec)
	}
	if err != nil {
		return nil, err
	}
	dst := toNRGBA(base)

	// decodeSamples already applied a raw mask to a raw image, matte and all. Every other pairing
	// reaches here with the base opaque, and alphaAt cannot help, because it reads only packed
	// samples — so the mask is decoded as an image in its own right and its grey level taken as
	// alpha, scaled to the base by nearest neighbour exactly as alphaAt scales.
	if im.SMask != nil && (im.Codec != CodecRaw || im.SMask.Codec != CodecRaw) {
		m, err := Pixels(im.SMask)
		if err != nil {
			return nil, fmt.Errorf("image: soft mask: %w", err)
		}
		applyMask(dst, m)
		if im.Premultiplied() {
			unblendPixels(dst, im.SMask.Matte)
		}
	}
	return dst, nil
}

// unblendPixels inverts the pre-blending on decoded pixels, for the pairings decodeSamples could not
// invert because the alpha was not yet known there.
//
// Exact rather than an approximation of the sample-domain inversion, because invertible admits only
// the spaces where a decoded pixel *is* the sample: one or three components, not Indexed and not Lab,
// so decoding applied no colour conversion for §11.6.5.3's ordering to be violated by.
func unblendPixels(dst *stdimage.NRGBA, matte []float64) {
	sample := make([]float64, len(matte))
	for o := 0; o < len(dst.Pix); o += 4 {
		a := float64(dst.Pix[o+3]) / 255
		for i := range sample {
			sample[i] = float64(dst.Pix[o+i]) / 255
		}
		unblend(sample, matte, a)
		for c := 0; c < 3; c++ {
			dst.Pix[o+c] = clamp8(sample[c%len(sample)])
		}
	}
}

// decodeJPEG decodes a DCT stream, refusing the two cases the stdlib decoder gets silently wrong.
func decodeJPEG(im *Image) (stdimage.Image, error) {
	if im.Components == 4 {
		return nil, fmt.Errorf("%w: a DCT-coded CMYK image", ErrUnsupported)
	}
	if !identityDecode(im.Decode) {
		return nil, fmt.Errorf("%w: a /Decode array on a DCT-coded image", ErrUnsupported)
	}
	// The header decides what the decoder allocates, and it is a second statement of the size
	// that nothing ties to the dictionary's — which is the one read's cap checked. A header
	// claiming 65535 square behind a dictionary claiming 10 square would otherwise allocate four
	// billion samples past that cap. The two disagreeing at all means the file has two answers to
	// the image's size, and drawing either one would be choosing between them.
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(im.Data))
	if err != nil {
		return nil, fmt.Errorf("image: jpeg: %w", err)
	}
	if cfg.Width != im.Width || cfg.Height != im.Height {
		return nil, fmt.Errorf("image: jpeg header is %d×%d and the dictionary says %d×%d",
			cfg.Width, cfg.Height, im.Width, im.Height)
	}
	img, err := jpeg.Decode(bytes.NewReader(im.Data))
	if err != nil {
		return nil, fmt.Errorf("image: jpeg: %w", err)
	}
	return img, nil
}

// identityDecode reports whether a /Decode array leaves samples as they are: absent, or [0 1] for
// every component.
func identityDecode(dec []float64) bool {
	for i, v := range dec {
		if v != float64(i%2) {
			return false
		}
	}
	return true
}

// toNRGBA copies any decoded image into an NRGBA whose origin is (0, 0).
//
// A copy even when the input already is one, because decodeCCITT may return a SubImage whose Pix
// is shared with a wider buffer and whose Rect does not start at zero — indexing it as if it did
// reads the wrong row.
func toNRGBA(src stdimage.Image) *stdimage.NRGBA {
	b := src.Bounds()
	dst := stdimage.NewNRGBA(stdimage.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.SetNRGBA(x, y, color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA))
		}
	}
	return dst
}

// applyMask multiplies a soft mask's grey level into dst's alpha.
//
// The red channel is the grey level: a soft mask is DeviceGray by definition (§11.6.5.2), and
// every decoder here writes grey as three equal channels.
func applyMask(dst, m *stdimage.NRGBA) {
	bw, bh := dst.Rect.Dx(), dst.Rect.Dy()
	mw, mh := m.Rect.Dx(), m.Rect.Dy()
	if mw == 0 || mh == 0 {
		return
	}
	for y := 0; y < bh; y++ {
		my := y * mh / bh
		for x := 0; x < bw; x++ {
			mx := x * mw / bw
			a := m.Pix[my*m.Stride+mx*4]
			o := y*dst.Stride + x*4 + 3
			dst.Pix[o] = uint8((int(dst.Pix[o])*int(a) + 127) / 255) // #nosec G115 -- both factors are at most 255, so the quotient is too
		}
	}
}

package ocr

import "image"

// textCover is the share of an image its words must cover for the image to be text. The
// gold set's paragraph set as an image covers 39% of its crop; the most a figure did in the
// corpus is 20%, LightOnOCR's screenshot of a whole paper page, which is a figure of a page
// and not text of this one; a photo's few words of noise cover under 10% (ADR 0046).
const textCover = 0.30

// textMinWords is the fewest words a text image holds. exportPDFInput's icons read as one or
// two characters covering most of their crop.
const textMinWords = 5

// IsTextImage reports whether words, read from an image of the given bounds, make it an image
// of text: read as text at all (IsText), of textMinWords or more, covering textCover of it.
func IsTextImage(words []Word, bounds image.Rectangle) bool {
	return IsText(words) && len(words) >= textMinWords && Cover(words, bounds) >= textCover
}

// Cover is the share of bounds the words' boxes cover, overlaps counted once each word.
func Cover(words []Word, bounds image.Rectangle) float64 {
	a := bounds.Dx() * bounds.Dy()
	if a <= 0 {
		return 0
	}
	n := 0
	for _, w := range words {
		r := w.Box.Intersect(bounds)
		n += r.Dx() * r.Dy()
	}
	return float64(n) / float64(a)
}

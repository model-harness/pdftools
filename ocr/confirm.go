package ocr

import (
	"context"
	"errors"
	"image"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Reader gives a second reading of the text in a crop: inferd's vision model, in ocr/gemma.
// maxRunes bounds the reply; a reader refuses rather than answer longer.
type Reader interface {
	Read(ctx context.Context, img image.Image, maxRunes int) (string, error)
	Name() string
}

// SureConf is the confidence from which a word is taken on Tesseract's reading alone: at 90 and
// over, 99.4% of the spike's 6,769 words were right; under it, 79% of 712 (ADR 0043).
const SureConf = 90

// Confirmation is what Confirm did: the words it asked about, how many the reader agreed with,
// and how many of its answers it refused. Every word asked about and not agreed with is [err].
type Confirmation struct {
	Reader                 string
	Asked, Agreed, Refused int
}

// Confirm asks r for a second reading of each word under SureConf, cropped from img by its box,
// and marks the word Agreed where the two readings match (ADR 0044).
//
// Agreement is the rule because it is what the spike measured: of the 712 words Tesseract read
// under 90, it and Gemma agreed on 494, and 488 of those were right, 98.8%. Where they disagreed
// the right reading was either one's about as often as neither's, so neither is taken, and Page
// writes the word as [err]. A reading that fails its guard is a disagreement.
//
// A reader that fails outright, the daemon gone mid-page, stops the asking: the error is returned
// with the words as they are, and the caller reads the page without a second opinion.
func Confirm(ctx context.Context, img image.Image, words []Word, r Reader) ([]Word, Confirmation, error) {
	out := make([]Word, len(words))
	copy(out, words)
	c := Confirmation{Reader: r.Name()}
	for i := range out {
		w := &out[i]
		if w.Conf >= SureConf {
			continue
		}
		c.Asked++
		got, err := ReadAgain(ctx, img, *w, r)
		if err != nil {
			if !errors.Is(err, ErrRefused) {
				return out, c, err
			}
			c.Refused++
			continue
		}
		if Same(got, w.Text) {
			w.Agreed = true
			c.Agreed++
		}
	}
	return out, c, nil
}

// ErrRefused is a reader's refusal of one answer, its guard's verdict: a disagreement, where any
// other error is the reader failing.
var ErrRefused = errors.New("the reader refused its answer")

// ReadAgain is r's reading of the word w in img, cropped as Confirm crops it.
func ReadAgain(ctx context.Context, img image.Image, w Word, r Reader) (string, error) {
	return r.Read(ctx, crop(img, w.Box), maxRunes(w.Text))
}

// crop is the word's box padded by a third of its height, as the spike cropped it, within img.
func crop(img image.Image, box image.Rectangle) image.Image {
	pad := max(4, box.Dy()/3)
	r := image.Rect(box.Min.X-pad, box.Min.Y-pad, box.Max.X+pad, box.Max.Y+pad).Intersect(img.Bounds())
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	return img
}

// maxRunes bounds a second reading of a word: twice its length and some, so a word the first
// reader split or cut short can still be read whole, and a reply that runs on is refused.
func maxRunes(text string) int { return 2*utf8.RuneCountInString(text) + 8 }

var wordChars = regexp.MustCompile(`[\p{L}\p{N}]+`)

// Same reports whether two readings have the same letters and digits, in NFKC, the comparison
// the spike scored by: "(e.g.," and "e.g." are one reading, "IoU" and "loU" are two.
func Same(a, b string) bool {
	x := wordChars.FindAllString(norm.NFKC.String(a), -1)
	y := wordChars.FindAllString(norm.NFKC.String(b), -1)
	if len(x) == 0 || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// SameGlyph reports whether two readings of one glyph read alone are the same character, in
// NFKC. Not Same: a glyph alone is as often a symbol as a letter, "$" or "•", and Same compares
// letters and digits only (ADR 0048).
func SameGlyph(a, b string) bool {
	x, y := strings.TrimSpace(norm.NFKC.String(a)), strings.TrimSpace(norm.NFKC.String(b))
	return x != "" && x == y
}

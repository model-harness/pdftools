package ocr

import (
	"context"
	"errors"
	"fmt"
	"image"
	"testing"

	"github.com/model-harness/pdftools/doc"
)

// fakeReader answers by the crop it is given, keyed on the crop's left edge, and records the
// crops and bounds it was asked with.
type fakeReader struct {
	answers map[int]string
	errs    map[int]error
	crops   []image.Rectangle
	bounds  []int
}

func (f *fakeReader) Read(_ context.Context, img image.Image, maxRunes int) (string, error) {
	b := img.Bounds()
	f.crops = append(f.crops, b)
	f.bounds = append(f.bounds, maxRunes)
	if err := f.errs[b.Min.X]; err != nil {
		return "", err
	}
	return f.answers[b.Min.X], nil
}

func (f *fakeReader) Name() string { return "fake" }

func TestConfirmAsksOnlyUnderNinety(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 1000, 200))
	ws := []Word{
		{Text: "sure", Box: image.Rect(100, 50, 160, 80), Conf: SureConf},
		{Text: "agreed", Box: image.Rect(200, 50, 260, 80), Conf: 89.9},
		{Text: "loU", Box: image.Rect(300, 50, 360, 80), Conf: 40},
		{Text: "refused", Box: image.Rect(400, 50, 460, 80), Conf: 50},
	}
	// Crops are padded by a third of the height, 10 pixels.
	r := &fakeReader{
		answers: map[int]string{190: "agreed.", 290: "IoU"},
		errs:    map[int]error{390: fmt.Errorf("guard: %w", ErrRefused)},
	}
	out, c, err := Confirm(context.Background(), img, ws, r)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Confirmation{Reader: "fake", Asked: 3, Agreed: 1, Refused: 1}) {
		t.Errorf("confirmation = %+v", c)
	}
	for i, want := range []bool{false, true, false, false} {
		if out[i].Agreed != want {
			t.Errorf("word %q agreed %v, want %v", out[i].Text, out[i].Agreed, want)
		}
	}
	if ws[1].Agreed {
		t.Error("Confirm changed the words it was given")
	}
	if len(r.crops) != 3 || r.crops[0] != image.Rect(190, 40, 270, 90) {
		t.Errorf("crops = %v", r.crops)
	}
	if r.bounds[0] != 2*len("agreed")+8 {
		t.Errorf("bound = %d", r.bounds[0])
	}
	// A word agreed is written whatever its confidence, and one not agreed under the threshold is
	// [err].
	p := Page(out, 1, pageBox, imgBox, SureConf)
	if got, want := p.Blocks[0].Text(), "sure agreed "+doc.Unmapped+" "+doc.Unmapped; got != want {
		t.Errorf("page = %q, want %q", got, want)
	}
}

// TestConfirmStopsWhenTheReaderFails: a reader that is gone, not refusing, ends the asking.
func TestConfirmStopsWhenTheReaderFails(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 1000, 200))
	gone := errors.New("inferd: backend not ready")
	ws := []Word{
		{Text: "one", Box: image.Rect(100, 50, 160, 80), Conf: 50},
		{Text: "two", Box: image.Rect(200, 50, 260, 80), Conf: 50},
	}
	r := &fakeReader{errs: map[int]error{90: gone}}
	_, c, err := Confirm(context.Background(), img, ws, r)
	if !errors.Is(err, gone) || c.Asked != 1 || len(r.crops) != 1 {
		t.Errorf("err %v, asked %d, crops %d: want the first failure to stop it", err, c.Asked, len(r.crops))
	}
}

// TestCropStaysInTheImage: a word at the edge is cropped to the image, and the pad is at least 4.
func TestCropStaysInTheImage(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 100, 100))
	if got := crop(img, image.Rect(0, 0, 20, 6)).Bounds(); got != image.Rect(0, 0, 24, 10) {
		t.Errorf("crop = %v", got)
	}
	if got := crop(img, image.Rect(90, 90, 100, 100)).Bounds(); got != image.Rect(86, 86, 100, 100) {
		t.Errorf("crop = %v", got)
	}
}

func TestSameReadsLettersAndDigits(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"(e.g.,", "e.g.", true},
		{"ﬁle", "file", true},
		{"IoU", "loU", false},
		{"2", "2.", true},
		{"[cs.CV]", "1 [cs.CV] 2", false},
		{"", "", false},
		{"—", "—", false},
		{"Cavaill�s_", "Cavaillès", false},
	} {
		if got := Same(c.a, c.b); got != c.want {
			t.Errorf("Same(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestSameGlyphIsTheCharacter: a glyph read alone agrees only as the same character, a symbol
// as much as a letter, and nothing read is no agreement.
func TestSameGlyphIsTheCharacter(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"$", "$", true},
		{"$", " $ ", true},
		{"ﬁ", "fi", true},
		{"e", "•", false},
		{"1", "l", false},
		{"", "", false},
		{"—", "—", true},
	} {
		if got := SameGlyph(c.a, c.b); got != c.want {
			t.Errorf("SameGlyph(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

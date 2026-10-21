package inferd

import (
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

// TestLiveVision runs against the machine's inferd when PDFSPEC_TEST_INFERD is set: its status
// names a vision backend, and an image-first request reads the first line of the reference
// scan. CI has no daemon and skips it.
func TestLiveVision(t *testing.T) {
	if os.Getenv("PDFSPEC_TEST_INFERD") == "" {
		t.Skip("PDFSPEC_TEST_INFERD is not set")
	}
	st, err := ReadStatus(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := st.Vision()
	if !st.Ready() || !ok {
		t.Fatalf("status %+v: no ready vision backend", st)
	}
	f, err := os.Open("../../testdata/reference/scan-paragraphs.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	// The first line of type: the first band of rows with ink, padded.
	b := img.Bounds()
	inked := func(y int) bool {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			if r, _, _, _ := img.At(x, y).RGBA(); r>>8 < 100 {
				return true
			}
		}
		return false
	}
	y0 := b.Min.Y
	for y0 < b.Max.Y && !inked(y0) {
		y0++
	}
	y1 := y0
	for y1 < b.Max.Y && inked(y1) {
		y1++
	}
	crop := image.Rect(b.Min.X, max(b.Min.Y, y0-10), b.Max.X, min(b.Max.Y, y1+10))
	rgb := make([]byte, 0, crop.Dx()*crop.Dy()*3)
	for y := crop.Min.Y; y < crop.Max.Y; y++ {
		for x := crop.Min.X; x < crop.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			rgb = append(rgb, byte(r>>8), byte(g>>8), byte(bl>>8))
		}
	}
	zero, n := 0.0, 128
	res, err := (&Client{}).Post(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, ImagesFirst: true, ImageRefs: []string{"crop"},
			Content: "Transcribe the text in this image exactly as written. Output only the text."}},
		Attachments: []Attachment{{ID: "crop", Width: uint32(crop.Dx()), Height: uint32(crop.Dy()), RGB: rgb}},
		Temperature: &zero, MaxTokens: &n,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s read %q, stop %q, crop %v", c.Backend, res.Text, res.StopReason, crop)
	if out := os.Getenv("PDFSPEC_TEST_INFERD_CROP"); out != "" {
		w, _ := os.Create(out)
		_ = png.Encode(w, image.Image(img.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(crop)))
		_ = w.Close()
	}
	if !strings.Contains(res.Text, "paragraph") {
		t.Errorf("%s read %q from the scan's first line", c.Backend, res.Text)
	}
}

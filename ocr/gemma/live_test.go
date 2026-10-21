package gemma

import (
	"context"
	"os"
	"testing"

	"github.com/model-harness/pdftools/render"
	renderpdfium "github.com/model-harness/pdftools/render/pdfium"
)

// TestLiveDescribe asks the machine's inferd, when PDFSPEC_TEST_INFERD is set, to describe the
// photo pymupdf's img-regular.pdf draws, and holds the answer to its guard. CI skips it.
func TestLiveDescribe(t *testing.T) {
	if os.Getenv("PDFSPEC_TEST_INFERD") == "" {
		t.Skip("PDFSPEC_TEST_INFERD is not set")
	}
	m, err := Open(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := renderpdfium.Open("../../testdata/pymupdf/img-regular.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	ra, err := r.Page(1, render.Options{DPI: 100, MaxPixels: render.DefaultOptions.MaxPixels})
	if err != nil {
		t.Fatal(err)
	}
	d, err := m.Describe(context.Background(), ra.Image)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", d)
	if d.Tag == "" || len(d.Text) < 10 {
		t.Errorf("description = %+v", d)
	}
}

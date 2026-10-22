package tesseract

import (
	"context"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/ocr"
)

// tsv is tesseract 5.5.3's output for a line of two words, with the page, block, paragraph and
// line rows above them and an empty word row, which a blank box gives.
const tsv = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
	"1\t1\t0\t0\t0\t0\t0\t0\t2481\t877\t-1\t\n" +
	"2\t1\t1\t0\t0\t0\t1068\t163\t345\t37\t-1\t\n" +
	"3\t1\t1\t1\t0\t0\t1068\t163\t345\t37\t-1\t\n" +
	"4\t1\t1\t1\t1\t0\t1068\t163\t345\t37\t-1\t\n" +
	"5\t1\t1\t1\t1\t1\t1068\t166\t83\t33\t95.969734\tPDF\n" +
	"5\t1\t1\t1\t1\t2\t1162\t163\t251\t37\t95.774673\tAssociation\n" +
	"5\t1\t1\t1\t1\t3\t1420\t163\t20\t37\t95\t \n" +
	"5\t1\t2\t1\t1\t1\t300\t320\t34\t49\t12.5\t6\n"

func TestParseTSVKeepsWordRows(t *testing.T) {
	ws, err := ParseTSV(strings.NewReader(tsv))
	if err != nil {
		t.Fatal(err)
	}
	want := []ocr.Word{
		{Text: "PDF", Box: image.Rect(1068, 166, 1151, 199), Conf: 95.969734, Block: 1, Par: 1, Line: 1},
		{Text: "Association", Box: image.Rect(1162, 163, 1413, 200), Conf: 95.774673, Block: 1, Par: 1, Line: 1},
		{Text: "6", Box: image.Rect(300, 320, 334, 369), Conf: 12.5, Block: 2, Par: 1, Line: 1},
	}
	if len(ws) != len(want) {
		t.Fatalf("got %d words, want %d: %+v", len(ws), len(want), ws)
	}
	for i := range want {
		if ws[i] != want[i] {
			t.Errorf("word %d = %+v, want %+v", i, ws[i], want[i])
		}
	}
}

func TestParseTSVRejects(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{"not TSV", "Tesseract Open Source OCR Engine\n"},
		{"a bad number", "level\tpage_num\n5\t1\t1\t1\t1\t1\tx\t166\t83\t33\t95\tPDF\n"},
		{"a bad confidence", "level\tpage_num\n5\t1\t1\t1\t1\t1\t1\t166\t83\t33\thigh\tPDF\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseTSV(strings.NewReader(c.in)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestVersionReadsTheFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"tesseract 5.5.3\n leptonica-1.87.0\n": "5.5.3",
		"tesseract 5.5.3\r\n":                  "5.5.3",
		"tesseract v5.0.0-alpha":               "v5.0.0-alpha",
		"something else 1.0\n":                 "",
		"":                                     "",
	} {
		if got := version(in); got != want {
			t.Errorf("version(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPickNeedsTheMargin: single block replaces automatic segmentation only when it is more
// confident by more than singleBlockMargin, since it reads across columns.
func TestPickNeedsTheMargin(t *testing.T) {
	auto := []ocr.Word{{Text: "auto", Conf: 90}}
	for _, c := range []struct {
		block float64
		want  string
	}{
		{90 + singleBlockMargin + 0.01, "block"},
		{90 + singleBlockMargin, "auto"},
		{90 + singleBlockMargin - 0.01, "auto"},
		{80, "auto"},
	} {
		if got := pick(auto, []ocr.Word{{Text: "block", Conf: c.block}})[0].Text; got != c.want {
			t.Errorf("block at %g: picked %s, want %s", c.block, got, c.want)
		}
	}
}

// TestNewWithoutTesseract: a run with no executable says so with ErrNotFound, which md and ocr
// take as "keep the extracted text", and a path that is not tesseract is refused.
func TestNewWithoutTesseract(t *testing.T) {
	// git exits 0 for --version and is not tesseract; go exits 2. Both are on PATH here, before
	// it is emptied below.
	for _, exe := range []string{"git", "go"} {
		if _, err := exec.LookPath(exe); err != nil {
			t.Fatalf("test setup: %s is not on PATH", exe)
		}
		if _, err := New(context.Background(), Options{Exe: exe}); err == nil {
			t.Errorf("New accepted %s, which is not tesseract", exe)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := New(context.Background(), Options{}); err != ErrNotFound {
		t.Errorf("New with no tesseract on PATH = %v, want ErrNotFound", err)
	}
}

// TestReadsAPage runs the real engine, when PDFSPEC_TEST_TESSERACT names one, on text drawn
// into an image. CI has no Tesseract and skips it.
func TestReadsAPage(t *testing.T) {
	exe := os.Getenv("PDFSPEC_TEST_TESSERACT")
	if exe == "" {
		t.Skip("PDFSPEC_TEST_TESSERACT names no tesseract")
	}
	e, err := New(context.Background(), Options{Exe: exe, DPI: 300})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.Name(), "tesseract 5.") || !strings.HasSuffix(e.Name(), " eng") {
		t.Errorf("Name = %q", e.Name())
	}
	// The reference set's scan, a rendered page degraded as a flatbed would.
	f, err := os.Open("../../testdata/reference/scan-paragraphs.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	gold, err := os.ReadFile("../../testdata/reference/scan-paragraphs.gold.md")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := e.Read(context.Background(), img)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(gold))
	if len(ws) < len(want)*9/10 || len(ws) > len(want)*11/10 {
		t.Errorf("read %d words, want about the gold's %d", len(ws), len(want))
	}
	sure, stroked := 0, 0
	for _, w := range ws {
		if w.Conf >= 90 && strings.Contains(string(gold), w.Text) {
			sure++
		}
		if w.Stroke > 0 {
			stroked++
		}
	}
	if sure < len(ws)*9/10 {
		t.Errorf("%d of %d words read at 90 or more are in the gold", sure, len(ws))
	}
	// Every word is weighed, for bold (ADR 0047).
	if stroked != len(ws) {
		t.Errorf("%d of %d words have a stroke", stroked, len(ws))
	}
}

// TestReadsWithAModelDirectoryAlone: a tessdata directory holding the language model and nothing
// else, as pdfspec ships one and as a clone of tessdata_fast is, still reads as TSV. The output
// format is asked for by setting, not by the configs/tsv file such a directory does not have.
func TestReadsWithAModelDirectoryAlone(t *testing.T) {
	exe := os.Getenv("PDFSPEC_TEST_TESSERACT")
	if exe == "" {
		t.Skip("PDFSPEC_TEST_TESSERACT names no tesseract")
	}
	from := os.Getenv("TESSDATA_PREFIX")
	if from == "" {
		t.Skip("TESSDATA_PREFIX names no model directory to take eng.traineddata from")
	}
	model, err := os.ReadFile(filepath.Join(from, "eng.traineddata")) // #nosec G304 -- the test's own setting
	if err != nil {
		t.Fatal(err)
	}
	alone := t.TempDir()
	if err := os.WriteFile(filepath.Join(alone, "eng.traineddata"), model, 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := New(context.Background(), Options{Exe: exe, TessData: alone, DPI: 300})
	if err != nil {
		t.Fatal(err)
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
	ws, err := e.Read(context.Background(), img)
	if err != nil {
		t.Fatalf("with only the model: %v", err)
	}
	if len(ws) < 50 {
		t.Errorf("read %d words with only the model, want the page's", len(ws))
	}
}

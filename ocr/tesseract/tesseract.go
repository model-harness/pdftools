// Package tesseract reads page images with the tesseract CLI, run as a subprocess when it is
// installed (ADR 0043). No CGO: the binary is found on PATH or at a configured path, and a run
// without it falls back as the caller decides.
//
// Tesseract and not a vision model, from a spike on 8 pages, each clean at 300 dpi and
// scan-like at 150 (issue #9, re-run 2026-10-20 with Google's documented OCR prompt): Tesseract
// 5.5.3 read them at a median CER of 2.3% and 3.3%, Gemma 4 E4B at 5.9% and 5.2% in its best
// shape and 32% or worse with Google's question. Gemma's errors also carry no confidence, and
// Tesseract's do: at 90 or more, 99.4% of its words were right.
package tesseract

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/model-harness/pdftools/ocr"
)

// Options configure an Engine.
type Options struct {
	// Exe is the tesseract executable. Empty looks for "tesseract" on PATH.
	Exe string

	// TessData is the directory holding the language models. Empty leaves it to
	// TESSDATA_PREFIX or the installation's default.
	TessData string

	// Lang is the language model, "eng" when empty.
	Lang string

	// DPI is the resolution the images are rendered at. Zero lets Tesseract estimate it, which
	// it does from the text and gets wrong on a sparse page.
	DPI float64
}

// ErrNotFound reports that no tesseract executable was found.
var ErrNotFound = errors.New("tesseract not found: install it, or name it with -tesseract")

// Engine is an ocr.Engine backed by the tesseract CLI.
type Engine struct {
	exe, tessdata, lang, version string
	dpi                          float64
}

// New finds the executable and asks it its version, which is both the check that it runs and
// the name the page report gives the words it reads.
func New(ctx context.Context, opt Options) (*Engine, error) {
	exe := opt.Exe
	if exe == "" {
		p, err := exec.LookPath("tesseract")
		if err != nil {
			return nil, ErrNotFound
		}
		exe = p
	}
	e := &Engine{exe: exe, tessdata: opt.TessData, lang: opt.Lang, dpi: opt.DPI}
	if e.lang == "" {
		e.lang = "eng"
	}
	out, err := e.command(ctx, "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract at %s does not run: %w", exe, err)
	}
	e.version = version(string(out))
	if e.version == "" {
		return nil, fmt.Errorf("%s does not say it is tesseract", exe)
	}
	return e, nil
}

// version reads "tesseract 5.5.3" from the first line of --version.
func version(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	v, ok := strings.CutPrefix(strings.TrimSpace(first), "tesseract ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// Name is "tesseract <version> <lang>".
func (e *Engine) Name() string { return "tesseract " + e.version + " " + e.lang }

func (e *Engine) command(ctx context.Context, args ...string) *exec.Cmd {
	// #nosec G204 -- the executable is the user's own tesseract, found on PATH or named by them,
	// and the arguments are this package's constants and the configured language.
	cmd := exec.CommandContext(ctx, e.exe, args...)
	if e.tessdata != "" {
		cmd.Env = append(os.Environ(), "TESSDATA_PREFIX="+e.tessdata)
	}
	return cmd
}

// singleBlockMargin is how far single-block mode's mean confidence must exceed automatic
// segmentation's for its reading to be taken.
//
// Automatic segmentation finds columns, and single-block mode reads across them, so automatic
// is the default. Its one failure in the spike was a hex dump it split into columns of
// digits: iso209 scan-like, at 60.3% CER against single block's 4.5%, where single block's
// confidence was 1.5 higher. On the reference fixtures with two and three columns and on a
// two-column paper, automatic's confidence was the higher, by 0.5 to 5.3; 1 keeps all of them.
const singleBlockMargin = 1.0

// Read runs automatic segmentation and single-block mode and returns the reading with the
// higher confidence, single block only past singleBlockMargin.
func (e *Engine) Read(ctx context.Context, img image.Image) ([]ocr.Word, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	auto, err := e.read(ctx, buf.Bytes(), "3")
	if err != nil {
		return nil, err
	}
	block, err := e.read(ctx, buf.Bytes(), "6")
	if err != nil {
		return nil, err
	}
	return pick(auto, block), nil
}

// pick is single block's reading if its confidence exceeds automatic's by singleBlockMargin,
// and automatic's otherwise.
func pick(auto, block []ocr.Word) []ocr.Word {
	if ocr.MeanConf(block) > ocr.MeanConf(auto)+singleBlockMargin {
		return block
	}
	return auto
}

func (e *Engine) read(ctx context.Context, png []byte, psm string) ([]ocr.Word, error) {
	args := []string{"stdin", "stdout", "-l", e.lang, "--psm", psm}
	if e.dpi > 0 {
		args = append(args, "--dpi", strconv.Itoa(int(e.dpi+0.5)))
	}
	cmd := e.command(ctx, append(args, "tsv")...)
	cmd.Stdin = bytes.NewReader(png)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract --psm %s: %w: %s", psm, err, lastLine(stderr.String()))
	}
	return ParseTSV(bytes.NewReader(out))
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// ParseTSV reads tesseract's TSV output: a header, then one row per page, block, paragraph,
// line and word. The rows with text are kept, which are the word rows, level 5: the others
// carry an empty text field, and so does a word row for a blank box.
func ParseTSV(r io.Reader) ([]ocr.Word, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var out []ocr.Word
	header := true
	for sc.Scan() {
		if header {
			header = false
			if !strings.HasPrefix(sc.Text(), "level\t") {
				return nil, fmt.Errorf("not tesseract TSV: %q", sc.Text())
			}
			continue
		}
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 12 || strings.TrimSpace(f[11]) == "" {
			continue
		}
		var n [9]int
		for i := range n {
			v, err := strconv.Atoi(f[i+1])
			if err != nil {
				return nil, fmt.Errorf("TSV field %d: %w", i+2, err)
			}
			n[i] = v
		}
		conf, err := strconv.ParseFloat(f[10], 64)
		if err != nil {
			return nil, fmt.Errorf("TSV confidence: %w", err)
		}
		out = append(out, ocr.Word{
			Text: f[11], Conf: conf, Block: n[1], Par: n[2], Line: n[3],
			Box: image.Rect(n[5], n[6], n[5]+n[7], n[6]+n[8]),
		})
	}
	return out, sc.Err()
}

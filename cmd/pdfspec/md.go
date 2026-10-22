package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/pdfspec"
	"github.com/model-harness/pdftools/sink/markdown"
)

func runMD(args []string) error {
	fs := flag.NewFlagSet("md", flag.ExitOnError)
	out := fs.String("o", "", "output file, or directory with -split (default: stdout)")
	split := fs.Bool("split", false, "one .md per page; -o names a directory")
	frontmatter := fs.Bool("frontmatter", false, "emit YAML frontmatter")
	artifacts := fs.Bool("artifacts", false, "keep running headers, folios, and watermarks")
	flat := fs.Bool("flat", false, "page-ordered prose with no headings, even when the file is tagged")
	useOCR := fs.Bool("ocr", true, "read pages that carry no text with Tesseract, when it is installed")
	backend := fs.String("backend", "pdfium", "rasterizer for OCR: "+pdfspec.BackendNames)
	var eo engineFlags
	eo.register(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec md [-o out] [-split] [-frontmatter] [-artifacts] [-flat] [-ocr=false] <file.pdf>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		// One file, not a glob. Converting several documents means several outputs,
		// and there is no obvious naming for them that a shell loop does not express
		// better and more visibly.
		return fmt.Errorf("md takes exactly one input file")
	}
	if *split && *out == "" {
		return fmt.Errorf("-split writes one file per page and needs -o <dir>")
	}
	if err := eo.validate(); err != nil {
		return err
	}

	opt := eo.options()
	opt.KeepArtifacts = *artifacts
	opt.Flat = *flat
	opt.Frontmatter = *frontmatter
	opt.OCR = *useOCR
	opt.Backend = *backend
	d, err := pdfspec.Open(fs.Arg(0), opt)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Read(context.Background()); err != nil {
		return err
	}

	if *split {
		// Per-page output is page-scoped by definition, so the outline does not apply:
		// a clause running from page 412 to 414 cannot be a heading in three files.
		return writeSplit(d, *out)
	}
	if *out == "" {
		return d.Markdown(os.Stdout)
	}
	return writeFile(*out, d.Markdown)
}

// convertDefault converts path as md does with no flags, for bench.
func convertDefault(path string) ([]byte, error) {
	var buf bytes.Buffer
	if err := pdfspec.Convert(context.Background(), &buf, path, defaultEngines().options()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeWhole(d *doc.Document, out string, opt markdown.Options) error {
	if out == "" {
		return markdown.Write(os.Stdout, d, opt)
	}
	return writeFile(out, func(w io.Writer) error {
		return markdown.Write(w, d, opt)
	})
}

// writeSplit writes one file per page into dir.
//
// Names are zero-padded to the width of the highest page number, so a directory
// listing is in page order. Without the padding page 10 sorts before page 2, and on
// a 1,023-page specification that makes the output unusable in any file browser.
//
// Every page gets a file, including a blank one. A gap in the numbering reads as a
// conversion that failed on those pages, and the empty file is the honest statement
// that the page had nothing on it.
func writeSplit(d *pdfspec.Document, dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	total := len(d.Doc.Pages)
	width := len(fmt.Sprint(total))

	for i := range d.Doc.Pages {
		n := d.Doc.Pages[i].Number
		path := filepath.Join(dir, fmt.Sprintf("page-%0*d.md", width, n))
		err := writeFile(path, func(w io.Writer) error { return d.WritePage(w, i+1) })
		if err != nil {
			return fmt.Errorf("page %d: %w", n, err)
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %d pages to %s\n", total, dir)
	return nil
}

// writeFile creates path and hands the writer to fn.
//
// The close error is returned when fn succeeded, because a buffered write that
// failed on flush at close has lost data, and reporting success there would tell the
// user a truncated file is complete. When fn already failed, its error is the one
// worth having.
func writeFile(path string, fn func(io.Writer) error) error {
	// The path is the user's: this is a CLI whose argument is where to write.
	f, err := os.Create(path) // #nosec G304 -- writing to a caller-named file is the API
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

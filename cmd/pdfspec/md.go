package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/3rg0n/pdf-spec/doc"
	"github.com/3rg0n/pdf-spec/extract"
	pcstore "github.com/3rg0n/pdf-spec/objects/pdfcpu"
	"github.com/3rg0n/pdf-spec/sink/markdown"
)

func runMD(args []string) error {
	fs := flag.NewFlagSet("md", flag.ExitOnError)
	out := fs.String("o", "", "output file, or directory with -split (default: stdout)")
	split := fs.Bool("split", false, "one .md per page; -o names a directory")
	frontmatter := fs.Bool("frontmatter", false, "emit YAML frontmatter")
	artifacts := fs.Bool("artifacts", false, "keep running headers, folios, and watermarks")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec md [-o out] [-split] [-frontmatter] [-artifacts] <file.pdf>\n\n")
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

	in := fs.Arg(0)
	s, err := pcstore.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	opt := extract.DefaultOptions
	opt.KeepArtifacts = *artifacts
	d, err := extract.New(s, opt).Document()
	if err != nil {
		return err
	}
	// The extractor reads the file, so it cannot know what the user called it.
	d.Meta.Path = in

	mopt := markdown.Options{Frontmatter: *frontmatter, Artifacts: *artifacts}
	if *split {
		return writeSplit(d, *out, mopt)
	}
	return writeWhole(d, *out, mopt)
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
func writeSplit(d *doc.Document, dir string, opt markdown.Options) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	width := len(fmt.Sprint(len(d.Pages)))
	total := len(d.Pages)

	for i := range d.Pages {
		p := d.Pages[i]
		name := fmt.Sprintf("page-%0*d.md", width, p.Number)
		path := filepath.Join(dir, name)
		err := writeFile(path, func(w io.Writer) error {
			return markdown.WritePage(w, d.Meta, p, total, opt)
		})
		if err != nil {
			return fmt.Errorf("page %d: %w", p.Number, err)
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

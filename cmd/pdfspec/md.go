package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/layout"
	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/sectionize"
	"github.com/model-harness/pdftools/sink/markdown"
	"github.com/model-harness/pdftools/tag"
)

func runMD(args []string) error {
	fs := flag.NewFlagSet("md", flag.ExitOnError)
	out := fs.String("o", "", "output file, or directory with -split (default: stdout)")
	split := fs.Bool("split", false, "one .md per page; -o names a directory")
	frontmatter := fs.Bool("frontmatter", false, "emit YAML frontmatter")
	artifacts := fs.Bool("artifacts", false, "keep running headers, folios, and watermarks")
	flat := fs.Bool("flat", false, "page-ordered prose with no headings, even when the file is tagged")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec md [-o out] [-split] [-frontmatter] [-artifacts] [-flat] <file.pdf>\n\n")
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
	warnIfEmpty(d)

	mopt := markdown.Options{Frontmatter: *frontmatter, Artifacts: *artifacts}
	if *split {
		// Per-page output is page-scoped by definition, so the outline does not apply:
		// a clause running from page 412 to 414 cannot be a heading in three files.
		return writeSplit(d, *out, mopt)
	}
	if !*flat {
		o, err := readOutline(s, d)
		if err != nil {
			return err
		}
		if o != nil {
			return writeOutline(o, *out, mopt)
		}
		inferRoles(d)
	}
	return writeWhole(d, *out, mopt)
}

// warnIfEmpty reports on stderr that a document yielded no text, and why it might not
// have.
//
// A conversion that produces an empty file and exits 0 is the worst available outcome:
// the caller has a plausible artifact and no signal, and in a shell loop over a corpus
// nothing distinguishes it from a document that converted cleanly. This is the same
// failure the okf.Write duplicate guard replaced — a silently short result — and it was
// found the same way, by a file that extracted to nothing while every exit code said
// it had worked.
//
// A warning rather than an error, because empty is sometimes the honest answer: a page
// holding one image has no text, and TestFixtureNoTextIsNotAnError pins that down.
// Distinguishing "correctly empty" from "lost the text" needs the file's fonts, which
// is what the hint names — a font with no /ToUnicode and glyph names outside the Adobe
// glyph list has genuinely undecodable codes, so the text is not in the file to be
// found and OCR is the only route. Written to stderr so redirecting stdout to a .md
// still shows it.
func warnIfEmpty(d *doc.Document) {
	if len(d.Pages) == 0 || strings.TrimSpace(d.Text()) != "" {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %d page(s) yielded no text\n", len(d.Pages))
	fmt.Fprint(os.Stderr, "  the fonts may carry no /ToUnicode and no recognizable glyph names,\n"+
		"  in which case the characters are not recoverable from the file itself.\n"+
		"  run \"pdfspec probe\" to see the fonts, or \"pdfspec ocr\" to read the pages as images.\n")
}

// readOutline reconstructs the clause hierarchy, or returns nil when the file has no
// structure tree to reconstruct it from.
//
// nil rather than an error, and a silent fallback to page order rather than a warning:
// most PDFs are untagged, and for those the layout path is the correct answer, not a
// degraded one. The output still says which path ran — Metadata.Tagged is in the
// frontmatter for exactly this reason.
func readOutline(s objects.Store, d *doc.Document) (*doc.Outline, error) {
	tr, err := tag.Read(s)
	if err != nil {
		return nil, err
	}
	if tr == nil {
		return nil, nil
	}
	// Titles and bodies are joined on (page, MCID), so an unresolved tree yields an
	// outline of empty sections. This is not optional.
	if err := tr.ResolvePages(s); err != nil {
		return nil, err
	}
	o, _ := sectionize.Tagged(d, tr, sectionize.DefaultOptions)
	return o, nil
}

// inferRoles promotes table cells, headings, and list items in a document that declared
// none.
//
// Called on the untagged branch only: where a structure tree exists, sectionize has
// already assigned every role from what the producer declared, and guessing over a
// declaration would replace evidence with a heuristic.
//
// Tables first, and this order is load-bearing rather than a precedence claim. It runs on
// the page's own strokes, which the other two never consult, and it is the only pass that
// changes the block list rather than relabelling blocks in place — a table's row arrives
// as one paragraph and leaves as one block per cell. Running it second would hand
// Headings a table's header row as a short paragraph in a distinct style, which is what
// its size ranking promotes, and Lists a numeric first column, which is what its marker
// test reads. Running it first leaves both nothing to misread: they consider only
// RoleParagraph blocks, and a cell is RoleTableCell.
//
// Headings then Lists, on evidence and not on necessity. Both passes consider only
// RoleParagraph blocks, so whichever runs second cannot reclassify what the first
// promoted, and Headings has the stronger claim on the overlap — a section number the
// document states outright, against a marker glyph.
//
// Necessity was measured, because there is a route by which the order could matter for
// more than precedence: Lists edits span text, and Headings' bodyCluster ranks sizes by
// rune count, so stripping markers first perturbs the counts it reads. Run both ways over
// all 48 PDFs on disk, the two orders agree on every block's role and level, on every
// heading count, and on every measured body size — 0 files differ. So the order is a
// statement about which evidence outranks which, and nothing on disk depends on it.
//
// OrderedLists last, and here the order is a precedence claim that matters. Its evidence is
// a run of incrementing numbers, and a numbered *heading* is exactly the block that could be
// misread as one — which is ADR 0011's stated objection to recognizing ordered lists at all.
// Running Headings first answers it structurally rather than by argument: a heading ADR 0008
// promoted is no longer RoleParagraph, so it is not a candidate and cannot join a run. Tables
// first removes the other collision the ADR names, a numeric first column.
//
// Shared by md and ocr so the two agree. They have to — ocr on a born-digital document
// writes what md writes and consults no model, and
// TestOCRVerbWithoutModelOnDigitalDocument holds them to it. Recognized pages are
// unaffected either way: doctags assigns RoleHeading and RoleListItem itself, and all
// passes consider only paragraphs.
func inferRoles(d *doc.Document) {
	layout.Tables(d, layout.DefaultOptions)
	layout.Headings(d, layout.DefaultOptions)
	layout.Lists(d, layout.DefaultOptions)
	layout.OrderedLists(d, layout.DefaultOptions)
}

func writeOutline(o *doc.Outline, out string, opt markdown.Options) error {
	if out == "" {
		return markdown.WriteOutline(os.Stdout, o, opt)
	}
	return writeFile(out, func(w io.Writer) error {
		return markdown.WriteOutline(w, o, opt)
	})
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

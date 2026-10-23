// Package pdfspec converts a PDF to Markdown. It is the whole path from the file to its text,
// which the pdfspec command and any program embedding the converter share (ADR 0051).
//
// Open reads the file's own text and structure. Read then reads, with the engines Options name,
// what is only in pixels: pages that carry no text, the images worth reading, and glyphs the
// file gives no value. Markdown writes the result.
//
//	d, err := pdfspec.Open("paper.pdf", pdfspec.DefaultOptions)
//	if err != nil {
//		return err
//	}
//	defer d.Close()
//	if err := d.Read(ctx); err != nil {
//		return err
//	}
//	return d.Markdown(w)
//
// Convert does the three in one call.
package pdfspec

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/ocr"
	"github.com/model-harness/pdftools/sink/markdown"
)

// Options say how a document is read and written. DefaultOptions is what "pdfspec md" does with
// no flags. The package reads no environment variable: what the command takes from
// PDFSPEC_TESSERACT and PDFSPEC_INFERD, a caller states here.
type Options struct {
	// KeepArtifacts keeps running headers, folios and watermarks, in the reading and the output.
	KeepArtifacts bool
	// Flat writes page-ordered prose with no headings, even when the file is tagged.
	Flat bool
	// Frontmatter writes YAML frontmatter.
	Frontmatter bool

	// OCR reads what is only in pixels, with Tesseract and the second reader below.
	OCR bool
	// Pages are the pages OCR may read whole, ascending; nil is every page. Images and glyphs
	// with no value are read on every page.
	Pages []int
	// Threshold is the fraction of a page's area covered by text below which the page is read
	// whole (ocr.Route).
	Threshold float64
	// Force reads every page in Pages whole, whatever its text covers.
	Force bool
	// Strict makes a page OCR fails on an error from Read. Otherwise the page keeps its extracted
	// text, and the failure is a warning in the log.
	Strict bool
	// Backend is the rasterizer OCR reads from: one of BackendNames.
	Backend string
	// Tesseract is the tesseract executable, "" for the one on PATH, or "off" to run none.
	Tesseract string
	// TessData is the directory of Tesseract's language models, "" for TESSDATA_PREFIX or the
	// install's.
	TessData string
	// Lang is the Tesseract language model.
	Lang string
	// DPI is the resolution a page is rasterized at before OCR.
	DPI float64
	// MinConf is, without a second reader, the Tesseract confidence (0 to 100) below which a word
	// is written [err].
	MinConf float64
	// Inferd is inferd's generation endpoint for the second reader, "" for the platform's, or
	// "off" to ask none.
	Inferd string
	// InferdTimeout bounds each request to the model: zero is five minutes, enough for a CPU with
	// no accelerator, and negative waits as long as the model takes.
	InferdTimeout time.Duration

	// Log receives what a reader of the output should know and the output cannot say: pages
	// that could not be read, what OCR read and what it could not, and text left unread. nil
	// discards it.
	Log io.Writer
}

// DefaultOptions are "pdfspec md" with no flags: OCR on every page that needs it, with
// Tesseract from PATH and inferd at the platform's address when they are there.
var DefaultOptions = Options{
	OCR:       true,
	Threshold: ocr.DefaultThreshold,
	Backend:   "pdfium",
	Lang:      "eng",
	DPI:       ocrDPI,
	MinConf:   defaultMinConf,
}

// Document is one PDF being converted. Close releases the file.
type Document struct {
	// Doc is what was read: the pages, their blocks and images, and what OCR recovered.
	Doc *doc.Document
	// Failed are the pages the extractor could not read, which are blank in the output.
	Failed []extract.PageError
	// Options are those Open was given. A change before Read or Markdown applies to it, such as
	// Pages chosen once the page count is known.
	Options Options

	s    objects.Store
	path string
}

// Open reads the PDF at path: its own text and structure, with no engine run. A page the
// extractor cannot read is blank, named in Failed and in the log, and the rest are read.
func Open(path string, opt Options) (*Document, error) {
	s, err := pcstore.Open(path)
	if err != nil {
		return nil, err
	}
	eopt := extract.DefaultOptions
	eopt.KeepArtifacts = opt.KeepArtifacts
	ex := extract.New(s, eopt)
	d, err := ex.Document()
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	// The extractor reads the file, so it cannot know what the caller called it.
	d.Meta.Path = path
	warnIfPagesFailed(logTo(opt.Log), ex.Failed(), len(d.Pages))
	return &Document{Doc: d, Failed: ex.Failed(), Options: opt, s: s, path: path}, nil
}

// Close releases the file.
func (d *Document) Close() error { return d.s.Close() }

// Read reads what the file holds only in pixels, when Options.OCR is on: the pages Route picks,
// the images worth reading and the glyphs with no value, in that order, the file's own text
// first (ADR 0043). It then logs text the output leaves unread. Without Tesseract it reads
// nothing and says which pages needed it; with Strict off, an engine's failure is logged and
// the pages keep their extracted text, since the conversion is the file's and OCR only adds
// to it.
func (d *Document) Read(ctx context.Context) error {
	opt, log := d.Options, logTo(d.Options.Log)
	if opt.OCR {
		pages := opt.Pages
		if pages == nil {
			pages = allPages(len(d.Doc.Pages))
		}
		routed := Route(d.Doc, pages, opt.Threshold, opt.Force)
		if wantsOCR(d.Doc, routed) {
			err := readPages(ctx, d.Doc, d.path, routed, opt, opt.Force, describedImages(d.s, d.Doc))
			switch {
			case err != nil && opt.Strict:
				return err
			case err != nil:
				fmt.Fprintf(log, "warning: %v\n", err)
			}
		}
	}
	warnIfEmpty(log, d.Doc)
	warnIfUnmapped(log, Unmapped(d.Doc))
	return nil
}

// Markdown writes the document: the structure tree's outline when the file has one and Flat
// is off, and otherwise the page-ordered text with the roles layout infers.
func (d *Document) Markdown(w io.Writer) error {
	return writeMarkdown(w, logTo(d.Options.Log), d.s, d.Doc, d.Options.Flat, d.markdownOptions())
}

// WritePage writes page n alone, as "pdfspec md -split" writes each page: page-scoped, so with
// no outline, since a clause running across three pages cannot head three files.
func (d *Document) WritePage(w io.Writer, n int) error {
	return markdown.WritePage(w, d.Doc.Meta, d.Doc.Pages[n-1], len(d.Doc.Pages), d.markdownOptions())
}

// Outline is the clause hierarchy the file's structure tree declares, or nil when it has none.
func (d *Document) Outline() (*doc.Outline, error) { return readOutline(d.s, d.Doc) }

// Needs is what Read would ask an engine to read, page by page, by the rules it reads by.
func (d *Document) Needs() []PageNeed { return PageNeeds(d.s, d.Doc) }

func (d *Document) markdownOptions() markdown.Options {
	return markdown.Options{Frontmatter: d.Options.Frontmatter, Artifacts: d.Options.KeepArtifacts}
}

// Convert writes the PDF at path to w as Markdown: Open, Read and Markdown in one call.
func Convert(ctx context.Context, w io.Writer, path string, opt Options) error {
	d, err := Open(path, opt)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Read(ctx); err != nil {
		return err
	}
	return d.Markdown(w)
}

// PageNeeds is what Read would ask an engine to read in d, whose file s is, page by page.
func PageNeeds(s objects.Store, d *doc.Document) []PageNeed {
	return pageNeeds(d, describedImages(s, d))
}

// logTo is w, or a writer that discards when there is none.
func logTo(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

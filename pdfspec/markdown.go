package pdfspec

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/internal/oneline"
	"github.com/model-harness/pdftools/layout"
	"github.com/model-harness/pdftools/objects"
	"github.com/model-harness/pdftools/sectionize"
	"github.com/model-harness/pdftools/sink/markdown"
	"github.com/model-harness/pdftools/tag"
)

// warnIfEmpty reports to w that a document yielded no text, and why it might not
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
// A font that gives its glyphs no value no longer empties a page, since each glyph is
// written [err] and warnIfUnmapped counts them (ADR 0042). What is left is a page that
// draws no text at all, which is most often a scan, so the hint names OCR. Written to the
// log, which the command sends to stderr so redirecting stdout to a .md still shows it.
func warnIfEmpty(w io.Writer, d *doc.Document) {
	if len(d.Pages) == 0 || strings.TrimSpace(d.Text()) != "" || anyImageRead(d) {
		return
	}
	fmt.Fprintf(w, "warning: %d page(s) yielded no text\n", len(d.Pages))
	fmt.Fprint(w, "  the pages draw no text, and may be images of it.\n"+
		"  run \"pdfspec probe\" to see what they hold, or \"pdfspec ocr\" to read them as images.\n")
}

// warnIfPagesFailed reports to w that some pages could not be read.
//
// Named pages rather than a count, because the number is what a reader acts on: the
// output is a document with those pages blank, and nothing in the Markdown says which
// ones. Extractor.Document keeps the other pages on purpose — one malformed page must
// not cost the rest — and this is the half of that policy that was missing, since a
// conversion with a page dropped read exactly like a whole one and exited 0.
//
// To the log for the same reason warnIfEmpty writes there.
func warnIfPagesFailed(w io.Writer, failed []extract.PageError, pages int) {
	if len(failed) == 0 {
		return
	}
	nums := make([]string, 0, len(failed))
	for _, f := range failed {
		nums = append(nums, strconv.Itoa(f.Page))
	}
	fmt.Fprintf(w, "warning: %d of %d page(s) could not be read and are blank in the output: %s\n",
		len(failed), pages, strings.Join(nums, ", "))
	fmt.Fprintf(w, "  %s\n", oneline.Of(failed[0].Error()))
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

// InferRoles promotes table cells, headings, and list items in a document that declared
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
// writes what md writes and runs no engine, and TestOCRVerbOnDigitalDocument holds them to
// it. A page read by OCR is built of paragraphs with lines (ocr.Page), so every pass
// considers it as it does an extracted page.
func InferRoles(d *doc.Document) {
	layout.Images(d)
	layout.Furniture(d)
	layout.Tables(d, layout.DefaultOptions)
	layout.Headings(d, layout.DefaultOptions)
	layout.Lists(d, layout.DefaultOptions)
	layout.OrderedLists(d, layout.DefaultOptions)
	layout.Footnotes(d)
	layout.PageBreaks(d)
	// Again after PageBreaks, which makes line starts of its own; see writeMarkdown.
	layout.Hyphens(d)
}

// writeMarkdown writes the whole document as Markdown: the structure tree's outline when
// the file has one and flat is off, and otherwise the page-ordered text with the roles
// layout infers. It is the md verb's choice of output, and bench scores what it writes,
// so the two cannot drift apart.
func writeMarkdown(w, log io.Writer, s objects.Store, d *doc.Document, flat bool, opt markdown.Options) error {
	// Before the outline, which copies the spans it is built from, and on every path, since a
	// typesetter's hyphen is not a role. InferRoles runs it once more for the page breaks it joins.
	layout.Hyphens(d)
	if !flat {
		o, err := readOutline(s, d)
		if err != nil {
			return err
		}
		if o != nil {
			warnIfBareImages(log, bareImages(d, imageLines(d, o)))
			return markdown.WriteOutline(w, o, opt)
		}
		InferRoles(d)
	}
	warnIfBareImages(log, bareImages(d, imageLines(d, nil)))
	return markdown.Write(w, d, opt)
}

// anyImageRead reports an image something was read off (ADR 0046): a page that is one image,
// described or read as text, yielded what it holds, though no glyph of it is text.
func anyImageRead(d *doc.Document) bool {
	for _, p := range d.Pages {
		for _, im := range p.Images {
			if im.Read != nil {
				return true
			}
		}
	}
	return false
}

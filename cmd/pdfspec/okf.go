package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/model-harness/pdftools/extract"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/sectionize"
	"github.com/model-harness/pdftools/sink/okf"
)

func runOKF(args []string) error {
	fs := flag.NewFlagSet("okf", flag.ExitOnError)
	out := fs.String("o", "", "output directory for the bundle (required)")
	docID := fs.String("id", "", "document identifier for resource URIs (default: derived from the title)")
	typ := fs.String("type", okf.DefaultOptions.Type, "OKF type of each concept document")
	artifacts := fs.Bool("artifacts", false, "keep running headers, folios, and watermarks")
	preamble := fs.Bool("preamble", true, "write content preceding the first heading as a document")
	unplaced := fs.Bool("unplaced", true, "write text no clause claimed as documents")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pdfspec okf -o <dir> [-id name] [-type kind] [-artifacts] [-preamble=false] [-unplaced=false] <file.pdf>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("okf takes exactly one input file")
	}
	if *out == "" {
		return fmt.Errorf("okf writes a directory tree and needs -o <dir>")
	}

	in := fs.Arg(0)
	s, err := pcstore.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	eopt := extract.DefaultOptions
	eopt.KeepArtifacts = *artifacts
	d, err := extract.New(s, eopt).Document()
	if err != nil {
		return err
	}
	d.Meta.Path = in

	o, err := readOutline(s, d)
	if err != nil {
		return err
	}
	if o == nil {
		// No structure tree, so the clauses come from inference instead: inferRoles marks
		// the headings md already renders, and sectionize.Untagged runs the same level
		// stack over them that the tagged path runs over H1..H6 elements. 4 of the
		// untagged documents on disk yield a real outline this way — mupdf_explored.pdf
		// 296 clauses to 3 levels, LightOnOCR 21, and the two reference fixtures.
		inferRoles(d)
		o, _ = sectionize.Untagged(d, sectionize.DefaultOptions)
		if len(o.Sections) == 0 {
			// A bundle is one file per clause, and inference found no heading to name one
			// with. Reported rather than silently producing a bundle of one document, which
			// would look like a successful conversion of a specification into a knowledge
			// base and be the opposite.
			//
			// Checked on this branch only. A tagged file whose tree declares no heading
			// reaches Bundle with an empty outline too — 5 on disk do, all of them invoices
			// and single-table fixtures — and it has always written the preamble-only bundle
			// that produces. Extending this guard to cover them would be a second change
			// wearing the same justification, and their tree is a declaration that this
			// path has no better answer to offer.
			//
			// The limit that remains is layout's, recorded in its package comment and
			// DESIGN.md §10: a heading is promoted where the document numbers it, so an
			// unnumbered "Foreword" stays a paragraph and a file whose headings are all
			// unnumbered lands here.
			return fmt.Errorf("%s yielded no clauses: an OKF bundle is one document per clause, and the file has neither a structure tree nor a numbered heading, so convert it with `pdfspec md`", in)
		}
	}

	// The run time comes from here rather than from the sink, so that the sink renders
	// deterministically and a test can assert on its bytes. RFC 3339 is what OKF §5.2 asks
	// for; seconds resolution because a bundle is not an event log.
	opt := okf.Options{
		Type:        *typ,
		DocID:       *docID,
		Generator:   "pdfspec/" + buildVersion(),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Artifacts:   *artifacts,
		Preamble:    *preamble,
		Unplaced:    *unplaced,
	}
	st, err := okf.Write(*out, o, opt)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d concept documents, %d indexes, %d resolved cross-references to %s\n",
		st.Concepts, st.Indexes, st.Links, *out)
	return nil
}

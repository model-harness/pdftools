package main

import (
	"path/filepath"
	"testing"

	"github.com/model-harness/pdftools/extract"
	"github.com/model-harness/pdftools/objects"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
)

// TestDocumentInfoIsRead pins the document information dictionary, which nothing read for
// the whole life of the project.
//
// pdfcpu does not keep the raw trailer — it folds the entries into its xref table — so the
// pdfcpu store reconstructs the dictionary its callers use. It reconstructed Root, Encrypt
// and Size and dropped Info, which made the entire Info branch of extract.metadata()
// unreachable: Title, Author, Subject, Keywords, Creator, Producer, CreationDate and ModDate
// were empty for every file the tool had ever opened.
//
// Nothing failed, and that is the part worth keeping a test for. An absent title is
// indistinguishable from a document that has none, so `md -frontmatter` emitted a block with
// nine fields missing and looked correct; the OKF bundle titled its root index from the
// filename slug instead of the document, and its sources[] entry carried a resource with no
// title or last_modified — which in turn left okf/frontmatter.go's indented() unreachable, so
// two mutations of the sources nesting survived the whole suite. The measurement that found
// it: 0 of 11 corpus documents and 0 of 37 reference fixtures carried any Info field, which
// is not a plausible property of real PDFs. After the fix, 25 of 37 fixtures do, and all 11
// corpus documents recover a title.
//
// Asserted on the committed fixtures rather than the corpus so it runs on a clean clone, and
// on every one of them rather than a chosen few: TeX writes Creator, Producer and CreationDate
// into every file it produces, so "all of them" is a stronger claim than a list of names and
// one that does not go stale when a fixture is added. Title is not asserted, because none of
// them sets \title and asserting the field a document actually has is the point.
func TestDocumentInfoIsRead(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "reference", "*.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 5 {
		t.Fatalf("found %d reference fixtures, expected at least 5: run `pwsh testdata/fetch.ps1`", len(paths))
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			s, err := pcstore.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()

			// The store's own contract first: a caller reaching for the trailer must find
			// Info there, because that is the only route to the dictionary.
			tr, err := s.Trailer()
			if err != nil {
				t.Fatalf("trailer: %v", err)
			}
			if _, ok := objects.GetDict(s, tr, "Info"); !ok {
				t.Error("trailer has no Info entry: the store is dropping the document information dictionary")
			}

			d, err := extract.New(s, extract.DefaultOptions).Document()
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			// TeX and LuaTeX both write these three. Values are not asserted — the
			// producer string carries a version that changes with the toolchain — but a
			// creation date has to look like a PDF date, since "D:" is what distinguishes
			// a decoded text string from a byte soup.
			if d.Meta.Creator == "" {
				t.Error("Creator is empty")
			}
			if d.Meta.Producer == "" {
				t.Error("Producer is empty")
			}
			if len(d.Meta.Created) < 2 || d.Meta.Created[:2] != "D:" {
				t.Errorf("Created is %q, want a PDF date beginning D:", d.Meta.Created)
			}
			t.Logf("creator=%q producer=%q created=%q", d.Meta.Creator, d.Meta.Producer, d.Meta.Created)
		})
	}
}

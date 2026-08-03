package main

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/sink/okf"
)

// bundleOf converts a corpus file and returns the rendered bundle.
//
// Rendered rather than written: the assertions worth making are about the bundle's shape and
// its links, both of which are answerable from the files as values, and writing 981 files to
// a temp directory on every test run costs seconds for nothing. TestOKFVerb covers the
// writing.
func bundleOf(t *testing.T, name string) ([]okf.File, okf.Stats) {
	t.Helper()
	_, o, _ := outlineOf(t, name)
	opt := okf.DefaultOptions
	opt.Generator = "pdfspec/test"
	opt.GeneratedAt = "2026-08-03T00:00:00Z"
	return okf.Bundle(o, opt)
}

// TestOKFBundleConforms is the acceptance test for the bundle: every file in it must be
// something OKF v0.2 describes, and every link in it must resolve.
//
// The conformance rules being checked are the ones a consumer cannot recover from. §11
// forbids rejecting a bundle for a missing optional field, an unknown type, or a broken
// link — which means a consumer will accept a bundle with 981 dead links and report success,
// and nothing downstream will ever say otherwise. So it gets checked here.
func TestOKFBundleConforms(t *testing.T) {
	for _, file := range []string{
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
	} {
		t.Run(file, func(t *testing.T) {
			files, st := bundleOf(t, file)

			paths := make(map[string]bool, len(files))
			for _, f := range files {
				if paths[f.Path] {
					t.Errorf("two files at %s", f.Path)
				}
				paths[f.Path] = true
			}

			concepts, indexes := 0, 0
			for _, f := range files {
				base := path.Base(f.Path)
				switch base {
				case "index.md":
					indexes++
					// Reserved per §8, and frontmatter is permitted only at the bundle
					// root and only as okf_version.
					if strings.HasPrefix(f.Content, "---") && f.Path != "/index.md" {
						t.Errorf("%s carries frontmatter", f.Path)
					}
				case "log.md":
					if strings.HasPrefix(f.Content, "---") {
						t.Errorf("%s carries frontmatter", f.Path)
					}
				default:
					concepts++
					// type is the only field v0.2 requires. A concept without it is the
					// one way this output can be non-conformant rather than merely thin.
					if !strings.HasPrefix(f.Content, "---\ntype: ") {
						t.Errorf("%s does not open with a type field", f.Path)
					}
					if strings.Count(f.Content, "\n---\n") < 1 {
						t.Errorf("%s frontmatter is not closed", f.Path)
					}
				}

				for _, target := range bundleLinks(f.Content) {
					if !paths[target] {
						t.Errorf("%s links to %s, which is not in the bundle", f.Path, target)
					}
				}
			}

			if concepts != st.Concepts || indexes != st.Indexes {
				t.Errorf("counted %d concepts and %d indexes, Stats says %d and %d",
					concepts, indexes, st.Concepts, st.Indexes)
			}
			t.Logf("%d concepts, %d indexes, %d links, %d directories",
				st.Concepts, st.Indexes, st.Links, st.Dirs)
		})
	}
}

// TestOKFClauseCount pins the bundle at one document per clause.
//
// The number is the measurement docs/DESIGN.md §8 and ADR 0002 rest on: ISO 32000-2 has 981
// headings and 7 Sect elements, and a bundle of single digits means segmentation reverted to
// containers. The count is asserted against the outline rather than against a literal so
// this test says "one file per clause" and not "981", which is sectionize's number to change.
func TestOKFClauseCount(t *testing.T) {
	for _, file := range []string{
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
	} {
		t.Run(file, func(t *testing.T) {
			_, o, _ := outlineOf(t, file)
			files, st := bundleOf(t, file)

			clauses := o.Count()
			// Concepts is clauses plus one document per unplaced page that rendered to
			// anything, and DefaultOptions keeps those.
			if st.Concepts < clauses {
				t.Errorf("%d concept documents for %d clauses: %d clauses were dropped",
					st.Concepts, clauses, clauses-st.Concepts)
			}
			t.Logf("%d clauses, %d concept documents, %d files", clauses, st.Concepts, len(files))
		})
	}
}

// TestOKFConservesText is the accounting invariant at the bundle level: every letter and
// digit the flat conversion drew must be somewhere in the bundle.
//
// One-directional and alphanumeric only, for the reasons TestMDOutlineConservesText gives at
// length — the bundle adds markup the flat rendering has no reason to contain, and it adds
// text of its own (frontmatter values, the "Subclauses" heading, provenance lines), so the
// reverse direction is not zero and should not be. What no rendering difference can excuse is
// a letter the extraction produced and the bundle does not contain, because a bundle is what
// a model will read instead of the specification.
func TestOKFConservesText(t *testing.T) {
	for _, file := range []string{
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
	} {
		t.Run(file, func(t *testing.T) {
			flat := alnumCounts(mdOut(t, "-flat", corpusFile(t, file)))

			files, _ := bundleOf(t, file)
			var all strings.Builder
			for _, f := range files {
				all.WriteString(f.Content)
				all.WriteString("\n")
			}
			got := alnumCounts(all.String())

			missing := 0
			for r, n := range flat {
				if d := n - got[r]; d > 0 {
					missing += d
				}
			}
			if missing != 0 {
				t.Errorf("bundle is missing %d letters and digits the flat conversion drew", missing)
			}
		})
	}
}

// TestOKFPathsAreUsable checks the filenames, which is where a bundle fails on a real
// filesystem rather than on paper.
//
// Windows binds first: MAX_PATH is 260 characters and the corpus has clause titles of 148,
// so an untruncated slug at four levels of nesting exceeds it and the write fails with a
// error that names the path and not the cause. The reserved characters are checked for the
// same reason — a clause titled "7.6.4.3 Public-key: encryption" must not produce a filename
// with a colon in it, which is not creatable on Windows at all.
func TestOKFPathsAreUsable(t *testing.T) {
	for _, file := range []string{
		"Well-Tagged-PDF-WTPDF-1.0.pdf",
		"ISO_32000-2_sponsored_EC3.pdf",
	} {
		t.Run(file, func(t *testing.T) {
			files, _ := bundleOf(t, file)
			longest := 0
			for _, f := range files {
				if len(f.Path) > longest {
					longest = len(f.Path)
				}
				// A bundle path is slash-separated and rooted by its own convention, and
				// nothing else may appear in a segment.
				if strings.ContainsAny(f.Path, `\:*?"<>|`) {
					t.Errorf("path contains a character no filesystem accepts: %s", f.Path)
				}
				if !strings.HasPrefix(f.Path, "/") {
					t.Errorf("path is not rooted: %s", f.Path)
				}
				for _, seg := range strings.Split(strings.TrimPrefix(f.Path, "/"), "/") {
					if seg == "" || seg == "." || seg == ".." {
						t.Errorf("path has an empty or traversing segment: %s", f.Path)
					}
				}
			}
			// Asserted against the package's own constant rather than a second copy of the
			// number: the point is that the bound is enforced, and a literal here would
			// drift from the one fit() checks.
			if longest > okf.MaxPath {
				t.Errorf("longest bundle path is %d characters, over the %d budget: fit() is not enforcing it",
					longest, okf.MaxPath)
			}
			t.Logf("%d files, longest path %d characters", len(files), longest)
		})
	}
}

// TestOKFVerb runs the CLI end to end, which is the only thing that proves the bundle lands
// on a real filesystem in the right shape.
func TestOKFVerb(t *testing.T) {
	in := corpusFile(t, "Well-Tagged-PDF-WTPDF-1.0.pdf")
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := runOKF([]string{"-o", dir, in}); err != nil {
		t.Fatalf("runOKF: %v", err)
	}

	n := 0
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(p) != ".md" {
			t.Errorf("bundle contains a non-markdown file: %s", p)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 100 {
		t.Errorf("wrote %d files; WTPDF has 183 clauses", n)
	}

	root, err := os.ReadFile(filepath.Join(dir, "index.md")) // #nosec G304 -- the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(root), "---\nokf_version: ") {
		t.Errorf("root index does not declare okf_version:\n%s", root)
	}

	// -o is required: a bundle is a directory tree and there is no stdout form of one.
	if err := runOKF([]string{in}); err == nil {
		t.Error("okf without -o succeeded")
	}
}

// bundleLinks pulls the destinations out of "](...)" occurrences, skipping the ones that are
// not bundle paths.
func bundleLinks(md string) []string {
	var out []string
	for i := 0; i+1 < len(md); i++ {
		if md[i] != ']' || md[i+1] != '(' {
			continue
		}
		j := strings.IndexByte(md[i+2:], ')')
		if j < 0 {
			continue
		}
		if target := md[i+2 : i+2+j]; strings.HasPrefix(target, "/") {
			out = append(out, target)
		}
		i += 2 + j
	}
	return out
}

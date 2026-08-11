package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/sink/markdown"
	"gopkg.in/yaml.v2"
)

// frontmatterOf returns the YAML between a document's leading "---" fence and the next one.
//
// The first "\n---\n" is the closing fence and not a value that looks like one, because
// YAMLString escapes a newline to a two-character "\n" rather than emitting it: a title of
// "a\n---\nb" is written as the quoted scalar "a\n---\nb" and contains no line break at all.
// So a document whose metadata is chosen to end the block early cannot exist, which matters
// now that titles come from the file rather than from its name. TestFrontmatterCannotBeEscaped
// pins it.
func frontmatterOf(content string) (string, bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", false
	}
	rest := content[len("---\n"):]
	i := strings.Index(rest, "\n---\n")
	if i < 0 {
		return "", false
	}
	return rest[:i+1], true
}

// TestOKFFrontmatterLoads runs the emitted YAML through a real loader, which is the only
// thing that reads it the way a consumer will.
//
// sink/okf/frontmatter.go writes YAML by hand and says why — a library reorders keys, and a
// stable order is what makes two runs over the same document diff cleanly. The cost of that
// choice is that the file's correctness rests on its own string literals, and every
// assertion the bundle had was strings.Contains, which cannot tell a nested mapping from a
// flat one. Mutation testing measured the hole: changing indented()'s four spaces to two,
// dropping the "- " that opens a sources entry, or losing generated.by's indent each leaves
// all of sink/okf and sink/markdown green while emitting a block no loader will parse. This
// test kills all three, plus removing yamlReserved — which arrives here as a bool
// pdf_unattributed and an int pdf_page rather than as a parse error, and is the case a
// Contains assertion is least able to see. Two mutations were already caught elsewhere:
// dropping a tags item's "- " fails TestBundleTags, and emitting a raw control byte fails
// TestYAMLQuoting. One survives and is recorded rather than fixed: plainYAML's
// leading/trailing-space rejection, which no value in the corpus exercises.
//
// Finding the sources-nesting mutations required fixing the harness first. bundleOf did not
// set Meta.Path, so builder.source() returned nothing and the bundle had no sources: block —
// 0 entries where a real run has 1398 — which left indented() unreachable and both mutations
// alive. That in turn was masking a defect in the pdfcpu store, which dropped the trailer's
// Info entry and made every document's metadata empty; TestDocumentInfoIsRead pins it.
//
// Loading rather than comparing bytes, because the claim is about what a consumer receives.
// Types are asserted too: every scalar must load back as a string, since plainYAML's job is
// to keep "1.7" from arriving as the float 1.7 and a clause titled "0" from arriving as a
// number. On the corpus that is 1409 blocks and 19781 scalars with nothing coerced — and
// yamlReserved is what holds it there, since removing it coerces 2195 of them, including 29
// booleans and two clause titles that are bare numbers.
//
// gopkg.in/yaml.v2 rather than v3 because the module graph already contains it, through
// pdfcpu, so validating with a real loader adds no dependency the build did not have.
func TestOKFFrontmatterLoads(t *testing.T) {
	blocks, scalars := 0, 0
	coerced := map[string]int{}

	for _, file := range corpusFiles() {
		files, _ := bundleOf(t, file)
		for _, f := range files {
			fm, ok := frontmatterOf(f.Content)
			if !ok {
				continue
			}
			blocks++

			// MapSlice rather than a map, so a duplicate key stays visible instead of being
			// silently overwritten by the last one to load.
			var doc yaml.MapSlice
			if err := yaml.Unmarshal([]byte(fm), &doc); err != nil {
				t.Errorf("%s: frontmatter does not parse: %v\n%s", f.Path, err, fm)
				continue
			}

			seen := map[string]bool{}
			for _, item := range doc {
				key, ok := item.Key.(string)
				if !ok {
					t.Errorf("%s: key %v is not a string", f.Path, item.Key)
					continue
				}
				if seen[key] {
					t.Errorf("%s: duplicate key %q", f.Path, key)
				}
				seen[key] = true

				switch key {
				case "tags":
					// A sequence of strings, per OKF §5.3.
					s, ok := item.Value.([]interface{})
					if !ok {
						t.Errorf("%s: tags is %T, want a sequence", f.Path, item.Value)
						continue
					}
					for _, v := range s {
						if _, ok := v.(string); !ok {
							t.Errorf("%s: tag %v is %T, want a string", f.Path, v, v)
						}
						scalars++
					}
				case "sources":
					// A sequence of mappings, each with resource. The nesting is what M4
					// destroys: without the "- " the keys land in the parent mapping.
					s, ok := item.Value.([]interface{})
					if !ok {
						t.Errorf("%s: sources is %T, want a sequence", f.Path, item.Value)
						continue
					}
					for _, v := range s {
						entry, ok := v.(yaml.MapSlice)
						if !ok {
							t.Errorf("%s: sources entry is %T, want a mapping", f.Path, v)
							continue
						}
						n := scalarsIn(t, f.Path, "sources", entry, coerced)
						scalars += n
						if !hasKey(entry, "resource") {
							t.Errorf("%s: sources entry has no resource key: %v", f.Path, entry)
						}
					}
				case "generated":
					// A mapping with by, and at when it is set. M6 lands here: the mutated
					// output parses with generated nil and by at the top level, which is
					// exactly the failure a Contains assertion cannot see.
					entry, ok := item.Value.(yaml.MapSlice)
					if !ok {
						t.Errorf("%s: generated is %T (%v), want a mapping", f.Path, item.Value, item.Value)
						continue
					}
					scalars += scalarsIn(t, f.Path, "generated", entry, coerced)
					if !hasKey(entry, "by") {
						t.Errorf("%s: generated has no by key: %v", f.Path, entry)
					}
				default:
					if s, ok := item.Value.(string); !ok {
						coerced[fmt.Sprintf("%s:%T", key, item.Value)]++
						t.Errorf("%s: %s loaded as %T (%v), want a string", f.Path, key, item.Value, item.Value)
					} else if s == "" {
						// scalar() omits an empty value rather than writing one, so a key
						// that loads as "" means something wrote it anyway.
						t.Errorf("%s: %s is present but empty", f.Path, key)
					}
					scalars++
				}
			}

			// index.md carries okf_version and nothing else; every other document is a
			// concept and type is the one field v0.2 requires.
			if f.Path == "/index.md" {
				if !seen["okf_version"] {
					t.Errorf("%s: root index frontmatter has no okf_version", f.Path)
				}
			} else if !seen["type"] {
				t.Errorf("%s: concept frontmatter has no type", f.Path)
			}
		}
	}

	// Floors rather than equalities, because sectionize's clause count is the number that
	// moves and it is asserted where it belongs, in TestOKFClauseCount. Tight floors
	// though: a loose one passes a walk that has silently stopped reaching most of the
	// bundle, which is the failure this test is least able to notice from the inside. The
	// measured values are 1409 and 19781, so these sit just under, and a real drop in
	// clause count trips TestOKFClauseCount with a message that names the cause.
	//
	// The ratio matters more than either figure: every concept carries at least type,
	// title, resource, status, sources.resource and generated.by, so scalars per block
	// cannot fall near 1 unless whole documents are emitting a bare fence.
	if blocks < 1350 || scalars < 19000 {
		t.Errorf("walked %d frontmatter blocks and %d scalars, expected at least 1350 and 19000: the walk is not reaching the bundle",
			blocks, scalars)
	}
	if per := float64(scalars) / float64(blocks); per < 8 {
		t.Errorf("%.1f scalars per frontmatter block, expected at least 8: documents are emitting near-empty frontmatter", per)
	}
	if len(coerced) != 0 {
		t.Errorf("scalars loaded back as a non-string type: %v", coerced)
	}
	t.Logf("%d frontmatter blocks, %d scalars, %d coerced", blocks, scalars, len(coerced))
}

// TestFrontmatterCannotBeEscaped pins the one thing that became attacker-reachable when the
// document information dictionary started being read.
//
// Title, Author, Subject, Keywords, Creator and Producer are strings from an untrusted file,
// and they now reach the frontmatter of every document pdfspec writes, where before the Info
// fix they were always empty. A title of "x\n---\ntype: something-else" would, written raw,
// close the block early and put a second mapping into the body — so the guarantee worth
// asserting is not that the value is quoted but that the emitted bytes contain no line break
// at all, which is what makes the fence unreachable regardless of what the value says.
//
// The other thing a title now decides is a directory name, since docID falls back to the
// filename only when Title is empty. That is asserted in the package that owns the slugging,
// as TestDocIDIsASafeSegment in sink/okf.
func TestFrontmatterCannotBeEscaped(t *testing.T) {
	for _, hostile := range []string{
		"x\n---\ntype: something-else",
		"x\r\n---\r\ny",
		"trailing\n",
		"\n---\n",
		"plain: no",
	} {
		got := markdown.YAMLString(hostile)
		if strings.ContainsAny(got, "\n\r") {
			t.Errorf("YAMLString(%q) = %q, which contains a line break: the frontmatter fence is reachable", hostile, got)
		}
		// And it round-trips: escaping that loses the value is a different defect from
		// escaping that breaks the document, and both matter.
		var v string
		if err := yaml.Unmarshal([]byte("k: "+got+"\n"), &struct {
			K *string `yaml:"k"`
		}{K: &v}); err != nil {
			t.Errorf("YAMLString(%q) = %q, which does not parse: %v", hostile, got, err)
		} else if v != hostile {
			t.Errorf("YAMLString(%q) = %q, which loads back as %q", hostile, got, v)
		}
	}
}

func hasKey(m yaml.MapSlice, key string) bool {
	for _, item := range m {
		if k, ok := item.Key.(string); ok && k == key {
			return true
		}
	}
	return false
}

// scalarsIn asserts that every value in a nested mapping is a non-empty string and returns
// how many it checked.
func scalarsIn(t *testing.T, path, parent string, m yaml.MapSlice, coerced map[string]int) int {
	t.Helper()
	n := 0
	for _, item := range m {
		key, _ := item.Key.(string)
		if s, ok := item.Value.(string); !ok {
			coerced[fmt.Sprintf("%s.%s:%T", parent, key, item.Value)]++
			t.Errorf("%s: %s.%s loaded as %T (%v), want a string", path, parent, key, item.Value, item.Value)
		} else if s == "" {
			t.Errorf("%s: %s.%s is present but empty", path, parent, key)
		}
		n++
	}
	return n
}

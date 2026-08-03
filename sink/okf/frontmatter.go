package okf

import (
	"io"
	"strings"

	"github.com/3rg0n/pdf-spec/sink/markdown"
)

// frontmatter is an OKF concept document's YAML block, built as a value and written once.
//
// A struct rather than fields written directly to the writer, because the field order is
// fixed by this file and the values arrive from three places — the section, the outline's
// metadata, and the run's options. Assembling first also makes the "omit when empty" rule
// a property of the writer rather than a conditional at every call site.
//
// Not marshalled with a YAML library, for the reason sink/markdown/frontmatter.go gives at
// greater length: the shape is small and closed, gopkg.in/yaml.v3 reorders keys, and a
// stable order is what makes two runs over the same document diff cleanly. The nesting
// here is two levels deep and hand-written accordingly.
type frontmatter struct {
	// Type is the only field OKF v0.2 requires, and a bundle whose concept documents lack
	// it is non-conformant per §11.
	Type string

	Title       string
	Description string
	Resource    string
	Tags        []string

	Sources []source
	// GeneratedBy is an actor per OKF §7 — "<producer>/<version>" for an agent,
	// "human:<id>" for a person, "process:<id>" for an automated process. It is
	// "pdfspec/<version>", not "pdfspec v0.1.0": consumers classify trust by detecting
	// the "human:" prefix, so a producer that invents its own actor syntax is telling
	// them nothing.
	GeneratedBy string
	GeneratedAt string

	// Status is "draft" until something verifies the extraction. OKF §5.3 defaults an
	// absent status to "stable", which this output is not — a conversion no human has
	// checked is a draft, and saying so is the difference between a bundle a reader
	// trusts appropriately and one they trust too much.
	Status string

	// Extra carries the fields that are ours rather than OKF's: which pages a clause came
	// from, and its clause number. §11 requires consumers to tolerate unknown keys and to
	// preserve them when round-tripping, so this is the sanctioned place for them rather
	// than a deviation.
	Extra []kv
}

type source struct {
	Resource     string
	Title        string
	Author       string
	LastModified string
}

type kv struct {
	Key string
	Val string
}

func (f frontmatter) write(w io.Writer) error {
	var sb strings.Builder
	sb.WriteString("---\n")

	// type first, always, and never omitted: it is the required field and a reader
	// checking conformance by eye should not have to look for it.
	scalar(&sb, "type", f.Type)
	scalar(&sb, "title", f.Title)
	scalar(&sb, "description", f.Description)
	scalar(&sb, "resource", f.Resource)

	if len(f.Tags) > 0 {
		sb.WriteString("tags:\n")
		for _, t := range f.Tags {
			sb.WriteString("  - ")
			sb.WriteString(markdown.YAMLString(t))
			sb.WriteByte('\n')
		}
	}

	if len(f.Sources) > 0 {
		sb.WriteString("sources:\n")
		for _, s := range f.Sources {
			// resource is the only required key in a sources entry per OKF §5.1, so it
			// leads and carries the "- " that opens the item.
			sb.WriteString("  - resource: ")
			sb.WriteString(markdown.YAMLString(s.Resource))
			sb.WriteByte('\n')
			indented(&sb, "title", s.Title)
			indented(&sb, "author", s.Author)
			indented(&sb, "last_modified", s.LastModified)
		}
	}

	if f.GeneratedBy != "" {
		sb.WriteString("generated:\n")
		sb.WriteString("  by: ")
		sb.WriteString(markdown.YAMLString(f.GeneratedBy))
		sb.WriteByte('\n')
		if f.GeneratedAt != "" {
			sb.WriteString("  at: ")
			sb.WriteString(markdown.YAMLString(f.GeneratedAt))
			sb.WriteByte('\n')
		}
	}

	scalar(&sb, "status", f.Status)
	for _, e := range f.Extra {
		scalar(&sb, e.Key, e.Val)
	}

	sb.WriteString("---\n\n")
	_, err := io.WriteString(w, sb.String())
	return err
}

// scalar writes "key: value", omitting the line entirely when the value is empty.
//
// Omitting rather than emitting an empty string because OKF §11 forbids a consumer from
// rejecting a concept for a missing optional field, so an absent key is always safe, while
// `description: ""` asserts that the clause's first sentence is the empty string.
func scalar(sb *strings.Builder, key, val string) {
	if val == "" {
		return
	}
	sb.WriteString(key)
	sb.WriteString(": ")
	sb.WriteString(markdown.YAMLString(val))
	sb.WriteByte('\n')
}

func indented(sb *strings.Builder, key, val string) {
	if val == "" {
		return
	}
	sb.WriteString("    ")
	sb.WriteString(key)
	sb.WriteString(": ")
	sb.WriteString(markdown.YAMLString(val))
	sb.WriteByte('\n')
}

// Package okf writes a doc.Outline as an Open Knowledge Format bundle.
//
// OKF v0.2 is markdown files with YAML frontmatter in a directory tree, where only the
// `type` field is required and consumers must not reject a bundle for anything they do not
// recognize. That tolerance shapes this package: emit conservatively, add a field when it
// is trustworthy, and never invent a value to fill a slot.
//
// One clause becomes one concept document, which is the point of the exercise. The
// specification is going into a bundle so a model can query it clause by clause to build
// the native libraries this repo is for, and a query that returns "somewhere in these
// 1,023 pages" is the failure this replaces. A clause is the unit the specification itself
// cross-references, so it is the unit a retrieval hit should be.
//
// This is a sink, like sink/markdown: it consumes doc and knows nothing about PDFs. Unlike
// sink/markdown it does touch the filesystem, because a bundle *is* a directory tree —
// there is no io.Writer form of "981 files in a hierarchy". Rendering each file is still
// separable, and Bundle returns the files as values so a test never writes to disk.
package okf

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/sink/markdown"
)

// Options configures the bundle.
type Options struct {
	// Type is the OKF `type` of every concept document. It is a free string that
	// consumers must tolerate not recognizing (§4), so the value's job is to be
	// descriptive rather than registered.
	Type string

	// DocID is the identifier in each concept's resource URI — "iso32000-2:2020" gives
	// "iso32000-2:2020#7.5.8". Derived from the document's title when empty, which is a
	// heuristic tuned for standards; a caller converting anything else should set it.
	DocID string

	// Generator is the actor written to generated.by, in OKF §7 form. Defaults to
	// "pdfspec/dev" rather than to a bare name, because a consumer reads the
	// "<producer>/<version>" shape to decide the value is not a human.
	Generator string

	// GeneratedAt is the ISO 8601 timestamp for generated.at, omitted when empty. Passed
	// in rather than read from the clock here: a sink that stamps its own output cannot be
	// tested for byte-identical rendering, and two runs over one document should differ
	// only where the document did.
	GeneratedAt string

	// Artifacts emits blocks with doc.RoleArtifact, matching sink/markdown.Options.
	Artifacts bool

	// Preamble writes the outline's pre-heading content — a title page, a copyright
	// notice — as a concept document. On by default, and the reason is measured: the whole
	// of WTPDF's preamble is "This work is licensed under CC-BY-4.0". A bundle that drops
	// its source's license terms is the one omission with a consequence outside the
	// software, and an earlier draft of this defaulted it off on the theory that front
	// matter carries no knowledge.
	Preamble bool

	// Unplaced writes text no clause claimed as a concept document. On by default,
	// because on ISO 32000-2 that text is the whole of clause 1: the file draws its Scope
	// outside any marked-content sequence, so no structure element names it. Dropping the
	// Scope of a standard from a bundle a model will query about that standard is the
	// worst available outcome; the document says plainly that its attribution is unknown.
	Unplaced bool
}

// DefaultOptions is what the CLI emits with no flags: everything the outline holds, because
// a bundle that drops content by default is a bundle whose omissions nothing reports.
var DefaultOptions = Options{
	Type:      "PDF Spec Clause",
	Generator: "pdfspec/dev",
	Preamble:  true,
	Unplaced:  true,
}

// File is one rendered file, with a bundle-relative path that always begins with "/".
//
// Returned as values rather than written directly so that the rendering is testable
// without a filesystem — the assertions worth making about a bundle are about its shape
// and its links, and both are answerable from this.
type File struct {
	Path    string
	Content string
}

// Stats reports what a bundle contains.
type Stats struct {
	Concepts int // concept documents, one per clause plus any preamble or unplaced
	Indexes  int // index.md files
	Links    int // cross-clause links resolved into the bundle
	Dirs     int // directories, which is the count of parent clauses
}

// Write renders the bundle and writes it under dir.
func Write(dir string, o *doc.Outline, opt Options) (Stats, error) {
	files, st := Bundle(o, opt)
	// Two files with one path means the second overwrites the first and the Stats returned
	// still counts both, so the caller reports a document count the bundle does not contain.
	// That happened: ISO/TS 32004 reported 56 concept documents and wrote 54. A duplicate is
	// a layout defect and not a condition a caller can recover from, so it is worth an error
	// here rather than a silently short bundle — the failure it replaces was invisible.
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if seen[f.Path] {
			return st, fmt.Errorf("%s: two documents share this path, which would silently drop one", f.Path)
		}
		seen[f.Path] = true
	}
	for _, f := range files {
		// f.Path is "/"-rooted and slash-separated, which is the bundle's own convention
		// and not this platform's. filepath.FromSlash is what makes the same bundle come
		// out identical on Windows and Linux.
		full := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(f.Path, "/")))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			return st, err
		}
		// #nosec G306 -- a knowledge bundle is meant to be read; 0644 is what every other
		// file in a repo checkout has.
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			return st, fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return st, nil
}

// Bundle renders the outline as a set of files, in a deterministic order.
func Bundle(o *doc.Outline, opt Options) ([]File, Stats) {
	if o == nil {
		return nil, Stats{}
	}
	b := &builder{
		opt: opt,
		out: o,
		lay: newLayout(o),
		id:  opt.DocID,
	}
	if b.id == "" {
		b.id = docID(o.Meta)
	}
	if b.opt.Type == "" {
		b.opt.Type = DefaultOptions.Type
	}
	if b.opt.Generator == "" {
		b.opt.Generator = DefaultOptions.Generator
	}
	b.index = newIndex(o, b.lay)

	o.Walk(func(s *doc.Section) bool {
		b.concept(s)
		return true
	})
	b.loose()
	b.reserved()

	sortFiles(b.files)
	return b.files, b.st
}

type builder struct {
	opt   Options
	out   *doc.Outline
	lay   *layout
	id    string
	index *xref

	// root is what the bundle-root index.md lists. Built as entries rather than read back
	// off Outline.Sections because the preamble and unplaced documents belong in that
	// index and belong to no section, so there is nothing in the outline to read them
	// from.
	root []entry

	files []File
	st    Stats
}

// entry is one line of an index.md.
type entry struct {
	Path  string
	Title string
	Desc  string
}

// concept writes one clause as a concept document.
func (b *builder) concept(s *doc.Section) {
	title := oneLine(s.Title)
	desc := describe(s.Blocks)
	self := rel(b.lay.file[s])

	var sb strings.Builder
	fm := frontmatter{
		Type:        b.opt.Type,
		Title:       title,
		Description: desc,
		Resource:    b.lay.resource(b.id, s),
		Tags:        tags(s),
		GeneratedBy: b.opt.Generator,
		GeneratedAt: b.opt.GeneratedAt,
		Status:      "draft",
		Extra:       b.extra(s),
	}
	if src := b.source(); src.Resource != "" {
		fm.Sources = []source{src}
	}
	// A strings.Builder cannot fail, so the error is structurally nil.
	_ = fm.write(&sb)

	// The heading is repeated in the body as an H1. The title is already in the
	// frontmatter, but a concept document is also a markdown file someone will open, and
	// one that starts mid-sentence reads as a fragment.
	parts := []string{
		"# " + markdown.InlineText(title),
		b.body(s.Blocks, self),
	}
	if kids := b.childList(s); kids != "" {
		parts = append(parts, "## Subclauses\n\n"+kids)
	}
	parts = append(parts, b.provenance(fmtPages(s)))
	sb.WriteString(join(parts))

	b.add(self, sb.String())
	b.st.Concepts++
	if s.Parent == nil {
		b.root = append(b.root, entry{self, title, desc})
	}
}

// body renders a clause's blocks, resolving any cross-reference it contains into a link.
func (b *builder) body(blocks []doc.Block, self string) string {
	var sb strings.Builder
	// A strings.Builder cannot fail.
	_ = markdown.WriteBlocks(&sb, blocks, markdown.Options{Artifacts: b.opt.Artifacts})
	if sb.Len() == 0 {
		return ""
	}
	text, n := b.index.link(sb.String(), self)
	b.st.Links += n
	return text
}

// childList is the "Subclauses" list a parent clause carries, which is what makes the
// bundle navigable downward without reading index.md — a consumer that ignores reserved
// files still finds the children.
func (b *builder) childList(s *doc.Section) string {
	if len(s.Kids) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, k := range s.Kids {
		sb.WriteString("- [")
		sb.WriteString(markdown.LinkLabel(oneLine(k.Title)))
		sb.WriteString("](")
		sb.WriteString(rel(b.lay.file[k]))
		sb.WriteString(")\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// provenance is the trailing line saying where in the source document the clause came
// from.
//
// In the body rather than only in the frontmatter because it is the one thing a reader
// checking the conversion against the original PDF needs, and a reader is who the body is
// for. It is also what makes an extraction defect reportable: "clause 7.5.8, page 412" is
// actionable where "somewhere in the bundle" is not.
func (b *builder) provenance(pages string) string {
	if pages == "" {
		return ""
	}
	if name := b.docName(); name != "" {
		return fmt.Sprintf("---\n\nSource: %s, %s.\n", markdown.InlineText(name), pages)
	}
	return fmt.Sprintf("---\n\nSource: %s.\n", pages)
}

// join appends the non-empty parts with exactly one blank line between them, so that each
// piece of a document — heading, body, subclause list, provenance — is written without
// knowing what precedes or follows it, and an absent piece leaves no gap behind.
//
// Markdown treats one blank line and three as the same break, so this is legibility rather
// than correctness: a file full of double gaps is a file someone will diff against a clean
// one, and the diff will be the whitespace.
func join(parts []string) string {
	var sb strings.Builder
	for _, p := range parts {
		p = strings.Trim(p, "\n")
		if p == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	return sb.String()
}

// docName is the source document as a reader would name it: its title, or failing that the
// PDF's filename. Empty only when the outline carries neither, which happens when a caller
// built it by hand.
func (b *builder) docName() string {
	if t := oneLine(b.out.Meta.Title); t != "" {
		return t
	}
	if p := b.out.Meta.Path; p != "" {
		return path.Base(filepath.ToSlash(p))
	}
	return ""
}

// extra is the non-OKF frontmatter: the clause number and the page range. Permitted by
// §11, which requires consumers to tolerate and preserve unknown keys, and prefixed so
// they are legibly ours rather than fields a reader might look up in the spec.
func (b *builder) extra(s *doc.Section) []kv {
	var out []kv
	if s.Number != "" {
		out = append(out, kv{"pdf_clause", s.Number})
	}
	if s.FirstPage > 0 {
		out = append(out, kv{"pdf_page", strconv.Itoa(s.FirstPage)})
		if s.LastPage > s.FirstPage {
			out = append(out, kv{"pdf_page_last", strconv.Itoa(s.LastPage)})
		}
	}
	return out
}

// source is the sources[] entry every concept in the bundle shares: the PDF it came from.
//
// author is omitted rather than set to "ISO", which is what docs/DESIGN.md §7 proposed
// before the spec was read closely. OKF §7 makes author an *actor*, and consumers classify
// trust by its prefix — a bare organization name is neither "human:<id>" nor
// "<producer>/<version>", so writing one there would put an unclassifiable value in a
// field a consumer uses to decide how much to trust the content. The document's own
// /Author is equally unusable for the same reason: it is a person or a tool name, not an
// actor. Both are already in the source PDF, which resource points at.
func (b *builder) source() source {
	m := b.out.Meta
	p := m.Path
	if p == "" {
		return source{}
	}
	return source{
		Resource:     filepath.ToSlash(p),
		Title:        oneLine(m.Title),
		LastModified: isoDate(m.Modified),
	}
}

func (b *builder) add(p, content string) {
	b.files = append(b.files, File{Path: rel(p), Content: content})
}

// loose writes the content that belongs to no clause: the outline's preamble, and the text
// no section claimed.
//
// Both are concept documents rather than appendices to some clause's file, because
// attaching unattributed text to a clause asserts an attribution the extraction does not
// have. On ISO 32000-2 the unplaced text is the whole of clause 1, drawn outside any
// marked-content sequence — filing the Scope of a standard under whichever clause happened
// to precede it would put a confident falsehood into a bundle a model reads as fact. These
// documents say instead that their placement is unknown, which is true and is a thing a
// consumer can act on.
func (b *builder) loose() {
	if b.opt.Preamble && len(b.out.Preamble) > 0 {
		b.looseDoc("/front-matter.md", "Front matter",
			"Content preceding the first heading of the source document. Its placement in the document's structure is not known.",
			b.out.Preamble, "")
	}
	if !b.opt.Unplaced {
		return
	}
	for _, p := range b.out.Unplaced {
		var pages string
		if p.Number > 0 {
			pages = fmt.Sprintf("page %d", p.Number)
		}
		// Named for the page because that is the only identifier this content has, and a
		// reader checking it against the original PDF needs exactly that number.
		name := fmt.Sprintf("/unplaced/page-%04d.md", p.Number)
		b.looseDoc(name, fmt.Sprintf("Unplaced text, page %d", p.Number),
			"Text the reconstruction could not attribute to any clause. It is reproduced here rather than dropped, and rather than filed under a clause it may not belong to.",
			p.Blocks, pages)
	}
}

func (b *builder) looseDoc(file, title, note string, blocks []doc.Block, pages string) {
	body := b.body(blocks, rel(file))
	if body == "" {
		return
	}

	var sb strings.Builder
	fm := frontmatter{
		Type:        b.opt.Type,
		Title:       title,
		Description: note,
		Resource:    b.id + "#" + strings.TrimSuffix(strings.TrimPrefix(file, "/"), ".md"),
		GeneratedBy: b.opt.Generator,
		GeneratedAt: b.opt.GeneratedAt,
		// Status is draft for every document this package writes, and these are the ones
		// it is most true of: the content is real and its position is a known unknown.
		Status: "draft",
		Extra:  []kv{{"pdf_unattributed", "true"}},
	}
	if src := b.source(); src.Resource != "" {
		fm.Sources = []source{src}
	}
	_ = fm.write(&sb)
	sb.WriteString(join([]string{
		"# " + markdown.InlineText(title),
		body,
		b.provenance(pages),
	}))

	b.add(file, sb.String())
	b.st.Concepts++
	b.root = append(b.root, entry{rel(file), title, note})
}

// reserved writes the bundle's index.md files and its log.md.
//
// Both are reserved filenames per OKF §8 and §9, carry no frontmatter, and are optional —
// a consumer must not reject a bundle for their absence. They are emitted anyway because
// they are what progressive disclosure needs: a model querying the specification starts by
// reading the root index to find out that clause 7 is Syntax, rather than by embedding
// 981 files.
func (b *builder) reserved() {
	title := b.docName()
	if title == "" {
		title = b.id
	}
	// The one frontmatter key §8 permits in an index, and only at the bundle root.
	b.indexFile("/", "---\nokf_version: \"0.2\"\n---\n\n# "+markdown.InlineText(title)+"\n\n", b.root)

	b.out.Walk(func(s *doc.Section) bool {
		if len(s.Kids) == 0 {
			return true
		}
		b.st.Dirs++
		// A parent clause's own concept document is listed alongside its children:
		// index.md cannot hold the prose itself, and a reader who saw only the subclauses
		// would conclude the parent has no text of its own.
		kids := make([]entry, 0, len(s.Kids)+1)
		kids = append(kids, b.entryOf(s))
		for _, k := range s.Kids {
			kids = append(kids, b.entryOf(k))
		}
		b.indexFile(b.lay.dir[s], "# Contents\n\n", kids)
		return true
	})

	if n := b.unplacedEntries(); len(n) > 0 {
		b.st.Dirs++
		b.indexFile("/unplaced", "# Unplaced text\n\n", n)
	}
	b.logFile()
}

func (b *builder) entryOf(s *doc.Section) entry {
	return entry{rel(b.lay.file[s]), oneLine(s.Title), describe(s.Blocks)}
}

// unplacedEntries recovers the unplaced documents from what was actually written, rather
// than from Outline.Unplaced: a page whose blocks render to nothing produces no file, and an
// index listing it would be a broken link.
func (b *builder) unplacedEntries() []entry {
	var out []entry
	for _, e := range b.root {
		if strings.HasPrefix(e.Path, "/unplaced/") {
			out = append(out, e)
		}
	}
	return out
}

// indexFile writes one index.md: a heading, then the "* [Title](url) - description" list
// OKF §8 specifies.
func (b *builder) indexFile(dir, head string, entries []entry) {
	var sb strings.Builder
	sb.WriteString(head)
	for _, e := range entries {
		sb.WriteString("* [")
		sb.WriteString(markdown.LinkLabel(e.Title))
		sb.WriteString("](")
		sb.WriteString(e.Path)
		sb.WriteString(")")
		if e.Desc != "" {
			sb.WriteString(" - ")
			sb.WriteString(markdown.InlineText(oneLine(e.Desc)))
		}
		sb.WriteString("\n")
	}
	b.add(path.Join(dir, "index.md"), sb.String())
	b.st.Indexes++
}

// logFile records the conversion as the bundle's only history entry.
//
// One entry, because that is the truth: this bundle was generated in one pass from one
// PDF. §9 wants date headings newest-first, so a later run prepends rather than rewrites —
// which is a thing a caller regenerating a bundle would have to do, and is out of scope
// for a sink that writes files.
func (b *builder) logFile() {
	date := b.opt.GeneratedAt
	if i := strings.IndexByte(date, 'T'); i > 0 {
		date = date[:i]
	}
	if !isDate(date) {
		// No usable timestamp means no date heading, and §9's structure is date headings.
		// Emitting "unknown" as a heading would produce a log that does not conform for
		// the sake of having one.
		return
	}

	var sb strings.Builder
	sb.WriteString("# Update Log\n\n## ")
	sb.WriteString(date)
	sb.WriteString("\n\n* **Creation**: Generated ")
	sb.WriteString(strconv.Itoa(b.st.Concepts))
	sb.WriteString(" concept documents from ")
	name := b.docName()
	if name == "" {
		name = "a PDF"
	}
	sb.WriteString(markdown.InlineText(name))
	sb.WriteString(" by ")
	sb.WriteString(b.opt.Generator)
	sb.WriteString(".\n")

	b.add("/log.md", sb.String())
}

// describe is the concept's description: the first sentence of the clause body.
//
// First sentence rather than first block, because a block is a paragraph and a paragraph
// of standards prose is not a summary. Bounded, and truncated at a word boundary, since
// this becomes a one-line entry in an index.
//
// Empty when the clause has no prose — a parent clause whose entire content is its
// subclauses, which on ISO 32000-2 is common. Emitting the first subclause's text instead
// would describe a clause with something that is not in it.
func describe(blocks []doc.Block) string {
	for i := range blocks {
		b := blocks[i]
		if b.Role == doc.RoleArtifact || b.IsEmpty() {
			continue
		}
		// /ActualText replaces the glyphs; /Alt only stands in for a block that draws
		// nothing, since it describes content rather than restating it. The same rule
		// the markdown sink applies, for the same reason — a description substituted
		// over real text deletes that text from the index entry too.
		text := b.Text()
		if b.Replacement != "" {
			text = b.Replacement
		} else if b.Alt != "" && strings.TrimSpace(text) == "" {
			text = b.Alt
		}
		if t := firstSentence(collapse(text)); t != "" {
			return t
		}
	}
	return ""
}

const maxDescription = 300

// firstSentence cuts at the first sentence-ending punctuation followed by a space.
//
// The trailing-space requirement is what keeps "7.5.8 Filters" and "ISO 32000-2:2020"
// intact: a period inside a clause number or a decimal is not a sentence boundary, and
// standards prose is denser in those than in sentences. A sentence longer than the bound is
// truncated instead, which is the honest failure — a description is a summary and a
// 300-character one has stopped being one either way.
func firstSentence(s string) string {
	for i := 0; i < len(s)-1; i++ {
		switch s[i] {
		case '.', '!', '?':
			// The space after the terminator is matched by rune, for the same reason the
			// word boundary below is: a document that sets its gaps as U+2002 has a full
			// stop followed by U+2002, and a byte test finds no sentence end anywhere in
			// it — which is how WTPDF descriptions reached the truncation path at all.
			if !startsWithSpace(s[i+1:]) {
				continue
			}
			// A single letter before the period is an initial or an enumerator ("A. "),
			// not the end of a sentence.
			if i >= 2 && endsWithSpace(s[:i-1]) && isAlpha(s[i-1]) {
				continue
			}
			if i+1 <= maxDescription {
				return s[:i+1]
			}
		}
	}
	if len(s) <= maxDescription {
		return s
	}
	// The ellipsis counts against the bound, so what comes back is never longer than
	// maxDescription — a bound the returned value can exceed is not one.
	cut := s[:maxDescription-len(ellipsis)]
	// The word boundary is found by rune and not by byte, because a producer writes one with
	// whatever space character its typography calls for. Well-Tagged-PDF-WTPDF-1.0.pdf sets
	// every inter-word gap as U+2002 EN SPACE, so a search for ' ' found none and 4 of its
	// descriptions were cut mid-word — "font specifications referenced by thes…", where the
	// boundary was two characters away.
	if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > maxDescription/2 {
		cut = cut[:i]
	}
	// A byte-offset cut can land inside a rune, and where no word boundary was close enough to
	// replace it that partial rune is what gets returned — 300 bytes of multi-byte text with no
	// space in it emitted invalid UTF-8 into a YAML value, which a strict parser rejects. The
	// same backoff sectionize.truncate does, and for the same reason: stripping only
	// continuation bytes is not enough, since a cut can land just after a lead byte too. The
	// n > 1 test is what distinguishes a truncated rune from a U+FFFD the document itself
	// holds, which must survive.
	for len(cut) > 0 {
		r, n := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || n > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return strings.TrimRightFunc(cut, isSpaceOrPunct) + ellipsis
}

const ellipsis = "…"

// isSpaceOrPunct is the trailing set for a truncated description: whitespace, and the
// punctuation that reads as dangling once the text after it is gone.
func isSpaceOrPunct(r rune) bool {
	return unicode.IsSpace(r) || r == ',' || r == ';' || r == ':'
}

// startsWithSpace and endsWithSpace test the rune rather than the byte, so a word boundary
// the producer wrote as something other than an ASCII space still reads as one. The empty
// string decodes to U+FFFD, which is not a space, so both return false with no guard.
func startsWithSpace(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
}

func endsWithSpace(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(r)
}

func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// tags are the clause's ancestry, which is what OKF §4 wants from tags and what a
// retrieval filter can use: a query about encryption should reach 7.6.4.3 without having
// matched the word in that clause's own title.
//
// Ancestry only. docs/DESIGN.md §7 also proposed "detected topics", which would mean
// keyword extraction — a guess this package has no basis for, and a tag that is wrong is
// worse than a tag that is missing because a consumer filters on it.
//
// The clause's own title is excluded: it is already the title field, and a tag list whose
// last element restates it adds a token to every embedding for no signal. A root clause
// therefore has no tags at all, which is correct — it has no ancestry.
func tags(s *doc.Section) []string {
	path := s.Path()
	if len(path) < 2 {
		return nil
	}
	out := make([]string, 0, len(path)-1)
	for _, t := range path[:len(path)-1] {
		if t = collapse(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// oneLine collapses whitespace runs and trims, for a value that becomes a YAML scalar or a
// markdown link label. Titles are already clean when they came from sectionize; a caller
// building an Outline by hand may not have been so careful, and the cost of being sure is
// one pass over a short string.
func oneLine(s string) string { return collapse(s) }

func collapse(s string) string {
	if strings.IndexFunc(s, isSpaceByte) < 0 {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

func isSpaceByte(r rune) bool {
	return r == '\n' || r == '\r' || r == '\t' || r == ' '
}

// isoDate reports a PDF date string as "YYYY-MM-DD", which is the format OKF §5.1 requires
// of last_modified, or "" when it cannot.
//
// A PDF date is "D:20240131120000+01'00'" per ISO 32000-2 §7.9.4 and is frequently
// truncated or malformed. Only the leading "D:YYYYMMDD" is read, and anything that does not
// look like one yields nothing rather than a guess: a wrong date in a provenance field is
// worse than an absent one, and §11 makes the absence safe.
func isoDate(s string) string {
	s = strings.TrimPrefix(s, "D:")
	if len(s) < 8 {
		return ""
	}
	y, m, d := s[:4], s[4:6], s[6:8]
	if !isYear(y) || !isDigits(m) || !isDigits(d) {
		return ""
	}
	// Range-checked rather than merely digit-checked, because "0000-00-00" parses as
	// digits and is not a date. Not calendar-checked — February 30th is a producer's
	// error, and correcting it would be inventing a value.
	if mm, _ := strconv.Atoi(m); mm < 1 || mm > 12 {
		return ""
	}
	if dd, _ := strconv.Atoi(d); dd < 1 || dd > 31 {
		return ""
	}
	return y + "-" + m + "-" + d
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return len(s) > 0
}

func isDate(s string) bool {
	return len(s) == 10 && s[4] == '-' && s[7] == '-' &&
		isYear(s[:4]) && isDigits(s[5:7]) && isDigits(s[8:])
}

// sortFiles orders a bundle deterministically, which is what makes two runs diffable and a
// test able to assert on the whole thing.
func sortFiles(f []File) {
	sort.Slice(f, func(i, j int) bool { return f[i].Path < f[j].Path })
}

package okf

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/3rg0n/pdf-spec/doc"
)

// layout assigns every section in an outline a file path, and is the only place in this
// package that knows what a bundle looks like on disk.
//
// Computed for the whole outline up front rather than per section, because a
// cross-reference from clause 7.5.8 to clause 8.4 has to name 8.4's file, and that file's
// name depends on 8.4's ancestry — which the section itself carries but the *link* is
// written from somewhere else entirely. One pass produces the map both need.
type layout struct {
	// file maps a section to its bundle-relative path, always beginning with "/". OKF
	// §6.2 admits absolute URLs, "/"-rooted bundle paths, and relative paths; the rooted
	// form is the one the spec recommends, and it is the only one that stays correct when
	// a link crosses two levels of the tree.
	file map[*doc.Section]string

	// dir maps a section that has children to the directory holding them.
	dir map[*doc.Section]string
}

// newLayout walks the outline and assigns paths.
//
// A section with children becomes a directory plus an index.md inside it; a leaf becomes
// a single .md file beside its siblings. That is the shape OKF §8 describes and the
// shape progressive disclosure needs: a reader who opens 7.5/index.md sees clause 7.5's
// own prose and a list of its subclauses, where a flat directory of 981 files answers
// no question about which of them is inside which.
func newLayout(o *doc.Outline) *layout {
	l := &layout{
		file: make(map[*doc.Section]string),
		dir:  make(map[*doc.Section]string),
	}
	l.assign(o.Sections, "/", "")
	return l
}

func (l *layout) assign(sections []*doc.Section, dir, parentNum string) {
	// Names are deduplicated within a directory, not globally: two sibling clauses named
	// "General" would otherwise overwrite each other, while "7.5/general.md" and
	// "7.6/general.md" are distinct paths and must both keep their name.
	taken := make(map[string]bool, len(sections))
	for i, s := range sections {
		base := unique(taken, fit(dir, candidates(s, parentNum, i)))
		if len(s.Kids) == 0 {
			l.file[s] = path.Join(dir, base+".md")
			continue
		}
		sub := path.Join(dir, base)
		l.dir[s] = sub
		// index.md is a reserved filename per OKF §8 and carries no frontmatter, so a
		// parent clause's own prose cannot live there — it goes in a concept document
		// beside its children. Named for the clause rather than "_self" or "general" so
		// that a reader listing the directory sees what it is.
		l.file[s] = path.Join(sub, base+".md")
	}
	for _, s := range sections {
		if len(s.Kids) > 0 {
			l.assign(s.Kids, l.dir[s], s.Number)
		}
	}
}

// MaxPath is the length a bundle-relative path is kept within.
//
// Windows is the binding constraint: MAX_PATH is 260 characters for the absolute path, so
// what a bundle may spend is 260 less whatever destination directory the user names. 150
// leaves a little over 100 for that, which covers a home directory and a project folder
// without being so tight that clause names stop being readable. The bound is enforced rather
// than hoped for — see fit.
//
// Exported so the corpus test asserts against this rather than against a second copy of the
// number, which would drift.
const MaxPath = 150

// fit returns the first candidate name that keeps the path inside MaxPath, or the last one
// if none does.
//
// Enforced rather than assumed because the failure it prevents is not a long filename, it is
// a write that fails partway through a 1,193-file bundle with an error naming the path and
// not the cause. The last candidate is always the clause number alone, at most a dozen bytes,
// so a path made entirely of last resorts stays inside the bound at any depth the corpus
// contains — see TestLayoutBoundsPaths, which goes deeper than the corpus does.
//
// The two extra bytes are the separator this name sits behind and the worst case of the
// deduplication suffix; the four are ".md" plus the separator before it. The name is counted
// twice because a parent clause's own document repeats its directory's name one level down,
// which is the longest path any single name produces.
func fit(dir string, names []string) string {
	for _, n := range names {
		if len(dir)+2+2*len(n)+4 <= MaxPath {
			return n
		}
	}
	return names[len(names)-1]
}

// candidates are the names for a section in descending order of legibility.
//
// Three of them, and the ordering is the whole point. The full "7-4-1-general" is what a
// reader searching for clause 7.4.1 will find and so is preferred wherever it fits. Dropping
// the parent's number gives "1-general", which is shorter and still readable in context —
// every ancestor's number is already a directory above it. The number alone is the last
// resort, which is what the document's own cross-references say and never long.
//
// An earlier version trimmed the parent prefix unconditionally, on the reasoning that it is
// redundant with the directory. It is, and it also produced "/7-syntax/4-filters/1-general.md"
// for a clause numbered 7.4.1 — redundancy that costs 4 bytes at a depth nothing reaches is
// worth paying for a name that matches what the reader typed.
func candidates(s *doc.Section, parentNum string, index int) []string {
	full := slug(s, "", index)
	num := kebab(s.Number)
	if num == "" {
		// Nothing to shorten to: the title is the only name, so the fallback is its
		// position among its siblings.
		return []string{full, "section-" + strconv.Itoa(index+1)}
	}
	out := []string{full}
	if trimmed := slug(s, parentNum, index); trimmed != full {
		out = append(out, trimmed)
	}
	return append(out, num)
}

// slug turns a section into a filename stem.
//
// The clause number leads when there is one, because that is what the document's own
// cross-references say and what a reader looking for 7.5.8 will search for. The title
// follows it, truncated, to make the name legible — "7.5.8-filters" rather than "7.5.8".
//
// index is the fallback: a section with neither a number nor a usable title still needs a
// distinct name, and its position among its siblings is the only thing left that
// distinguishes it. Such a section exists — a heading whose glyphs are a decorative
// image with no /Alt — and emitting it as "section-12" is better than dropping a clause.
func slug(s *doc.Section, parentNum string, index int) string {
	num := kebab(s.Number)
	title := kebab(s.Title)
	// The title normally repeats the number, since it was parsed off the front of it.
	// Emitting "7-5-8-7-5-8-filters" would be the cost of not noticing.
	title = strings.TrimPrefix(title, num+"-")

	if len(title) > maxSlug {
		title = title[:maxSlug]
		if i := strings.LastIndexByte(title, '-'); i > maxSlug/2 {
			title = title[:i]
		}
	}
	title = strings.Trim(title, "-")

	// The parent's number is already every directory above this one, so repeating it makes
	// "/7-syntax/7-4-filters/7-4-1-general.md" out of a clause whose number is 7.4.1 — the
	// prefix costs bytes at every level and is the reason a five-deep clause overran
	// MAX_PATH. Trimmed to the part this clause adds, which is what distinguishes it from
	// its siblings and is the only part that is not already in the path.
	if p := kebab(parentNum); p != "" && num != p {
		num = strings.TrimPrefix(num, p+"-")
	}

	switch {
	case num != "" && title != "":
		return num + "-" + title
	case num != "":
		return num
	case title != "":
		return title
	}
	return "section-" + strconv.Itoa(index+1)
}

// maxSlug bounds the title part of a filename. 60 bytes keeps the longest real clause
// name in the corpus — 148 bytes, "7.6.4.4.8 Algorithm 9: Computing the encryption
// dictionary's O (owner password) and OE (owner encryption) values …" — inside a path
// that survives Windows' 260-character MAX_PATH at four levels of nesting, which is the
// constraint that actually binds. The clause number is never truncated, so the name stays
// unambiguous however short the prose part gets.
const maxSlug = 60

// kebab reduces a string to lowercase ASCII words joined by hyphens.
//
// Non-ASCII is transliterated no further than dropping it, which is a real limitation and
// deliberate: a clause titled only in Japanese would slug to empty and fall back to its
// number or its index. Romanizing it would mean shipping a transliteration table per
// script, and the alternative — percent-encoding — produces filenames that are unreadable
// in exactly the case where a human is trying to find one. Every corpus document titles
// its clauses in ASCII.
func kebab(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && sb.Len() > 0 {
				sb.WriteByte('-')
			}
			dash = false
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			if dash && sb.Len() > 0 {
				sb.WriteByte('-')
			}
			dash = false
			sb.WriteRune(unicode.ToLower(r))
		default:
			// Everything else is a separator, including the dots inside a clause number:
			// "7.5.8" becomes "7-5-8". A dot in a filename reads as an extension, and
			// "7.5.8.md" has three of them.
			dash = sb.Len() > 0
		}
	}
	return sb.String()
}

// unique appends a numeric suffix until the name is free within its directory.
func unique(taken map[string]bool, base string) string {
	name := base
	for n := 2; taken[name]; n++ {
		name = base + "-" + strconv.Itoa(n)
	}
	taken[name] = true
	return name
}

// resource is the stable clause URI that becomes the concept's resource field.
//
// The docs/DESIGN.md §7 form is "iso32000-2:2020#7.5.8": a document identifier, then the
// clause number as a fragment. Sections with no clause number get their file path as the
// fragment instead, which is stable for the same reason the filename is — it is derived
// from the ancestry, not from a counter — and is still a URI a consumer can dereference
// within the bundle.
func (l *layout) resource(id string, s *doc.Section) string {
	if s.Number != "" {
		return id + "#" + s.Number
	}
	return id + "#" + strings.TrimSuffix(strings.TrimPrefix(l.file[s], "/"), ".md")
}

// docID derives the document identifier for resource URIs from the source metadata.
//
// The corpus is ISO standards whose titles begin with their own designation, so the title
// is the best source available: "ISO 32000-2:2020(en), Document management — Portable
// document format — Part 2" gives "iso32000-2:2020". A caller who knows better passes
// Options.DocID, which is why that field exists — this is a heuristic over producer
// metadata and it will be wrong for documents that are not standards.
func docID(m doc.Metadata) string {
	src := m.Title
	if src == "" {
		src = path.Base(strings.ReplaceAll(m.Path, `\`, "/"))
		src = strings.TrimSuffix(src, ".pdf")
	}
	if id := isoID(src); id != "" {
		return id
	}
	if s := kebab(src); s != "" {
		if len(s) > maxSlug {
			s = s[:maxSlug]
			s = strings.Trim(s[:strings.LastIndexByte(s+"-", '-')], "-")
		}
		return s
	}
	return "document"
}

// isoID matches an ISO-style designation at the start of a string and returns it in the
// "iso32000-2:2020" form: the standard's number, then its year.
//
// Written as a scan rather than a regexp because the shapes to accept are "ISO 32000-2",
// "ISO/TS 32005", and "ISO/IEC 19005-1", optionally followed by ":2020" or "-2023", and a
// regexp covering those is less legible than the loop and no shorter.
func isoID(s string) string {
	rest, ok := cutPrefixFold(s, "ISO")
	if !ok {
		return ""
	}
	// An optional committee part: "/TS", "/IEC", "/DIS".
	if strings.HasPrefix(rest, "/") {
		if i := strings.IndexAny(rest[1:], " \t"); i >= 0 {
			rest = rest[1+i:]
		}
	}
	rest = strings.TrimLeft(rest, " \t")

	// The number, with an optional "-N" part suffix.
	i := 0
	for i < len(rest) && (isDigit(rest[i]) || (rest[i] == '-' && i > 0 && i+1 < len(rest) && isDigit(rest[i+1]))) {
		i++
	}
	if i == 0 {
		return ""
	}
	id := "iso" + rest[:i]
	rest = rest[i:]

	// The year, either ":2020" or "-2023". Both appear in the corpus's own titles.
	//
	// A "-2020" year was already consumed by the number scan above, since "-N" is also how
	// a part number is written and the two are indistinguishable at that point. So the
	// number is re-split here: a trailing "-YYYY" is the year, and "32000-2-2020" is part 2
	// of standard 32000 published in 2020 rather than a standard numbered "32000-2-2020".
	if len(rest) > 4 && (rest[0] == ':' || rest[0] == '-') && isYear(rest[1:5]) {
		return id + ":" + rest[1:5]
	}
	if i := strings.LastIndexByte(id, '-'); i > 0 && isYear(id[i+1:]) {
		return id[:i] + ":" + id[i+1:]
	}
	return id
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isYear(s string) bool {
	if len(s) != 4 || s[0] != '1' && s[0] != '2' {
		return false
	}
	for i := 0; i < 4; i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// rel returns a bundle-relative link target, which per OKF §6.1 is the recommended form.
// Kept as a function rather than inlined so that the leading slash is written in one
// place: a link that loses it becomes relative to whatever directory the reader is in.
func rel(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// fmtPages renders a section's page range for the body's provenance line.
func fmtPages(s *doc.Section) string {
	switch {
	case s.FirstPage == 0:
		return ""
	case s.LastPage > s.FirstPage:
		return fmt.Sprintf("pages %d–%d", s.FirstPage, s.LastPage)
	default:
		return fmt.Sprintf("page %d", s.FirstPage)
	}
}

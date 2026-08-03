// Package sectionize reconstructs a document's clause hierarchy.
//
// The input is a doc.Document and, when the file has one, its structure tree. The
// output is a doc.Outline: a real tree of sections, each with a title, a level, its
// own content, and its children.
//
// The package rests on one measured fact about how real specifications declare
// hierarchy. ISO 32000-2 contains 7 Sect elements against 981 headings, and a single
// Part holds 13,442 direct children in a flat H1 P P P … stream — 966 of those 981
// headings have no element children at all. So a clause's body is not its heading's
// subtree; it is the heading's *following siblings*, up to the next heading of equal
// or higher rank. Collecting container elements would emit 7 sections from a
// 1,023-page standard, and would look correct on any document that happened to nest
// properly.
//
// That makes the hierarchy a level stack over a linear sequence of headings rather
// than a subtree extraction, which is also why the same builder will serve the
// untagged path: a sequence of (level, title, content) triples is all it needs, and
// font-size clustering can produce those where a structure tree cannot.
//
// This is also where roles are assigned. extract deliberately marks every block
// RoleParagraph, because heading rank, list nesting and cell membership are declared
// by the tree and inferring them from geometry as well would mean two packages
// guessing at the same thing from less evidence. The declared role arrives here.
package sectionize

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/3rg0n/pdf-spec/doc"
	"github.com/3rg0n/pdf-spec/tag"
)

// Options configures reconstruction.
type Options struct {
	// MaxTitle bounds a resolved title's length in bytes. A heading is short, so a
	// title in the hundreds of characters means the join picked up something that is
	// not the heading — a producer that put a whole paragraph in one marked-content
	// sequence, most often. Truncating keeps the clause, where dropping the title
	// would leave a section nothing can name. Zero means the default.
	MaxTitle int
}

// DefaultOptions is reconstruction as the CLI runs it.
var DefaultOptions = Options{MaxTitle: 200}

func (o Options) maxTitle() int {
	if o.MaxTitle <= 0 {
		return DefaultOptions.MaxTitle
	}
	return o.MaxTitle
}

// Stats reports what a reconstruction did, and exists because the failure modes here
// are quiet ones. A run that emits 7 sections from a standard has silently reverted
// to container-driven segmentation; a run that leaves half the text unclaimed has
// silently dropped it. Neither is an error and both are visible in these numbers.
type Stats struct {
	// Sections is the total at every level. docs/DESIGN.md §8 puts ISO 32000-2 at
	// roughly 981.
	Sections int

	// Titled is the sections whose title resolved to something.
	Titled int

	// Numbered is the sections whose title began with a clause number.
	Numbered int

	// MaxLevel is the deepest heading level reached.
	MaxLevel int

	// Blocks is the content blocks attributed to a section or to the preamble.
	Blocks int

	// UnplacedBlocks and UnplacedChars are the content that reached
	// doc.Outline.Unplaced: text the extractor produced that no structure element
	// claimed. A tagged document should have little, and what it has is a property of
	// the file rather than of this join — ISO 32000-2 draws clause 1 outside any
	// marked content — but a large number means the join is losing content, so it is
	// reported rather than left to be noticed.
	//
	// Measured: 0 on ISO/TS 32005, 0.23% of characters on ISO 32000-2.
	UnplacedBlocks int
	UnplacedChars  int
}

// Tagged reconstructs the outline from a structure tree.
//
// The tree supplies heading rank and reading order — both declared, which is the
// expensive part to infer — and the document supplies the text. They are joined on
// (page, MCID), so tag.Tree.ResolvePages must have run first; without it no element
// knows its page and every title and body comes back empty.
//
// A nil tree yields an outline with no sections and the whole document as preamble.
// That is the honest result for an untagged file rather than an error: it means this
// path does not apply.
func Tagged(d *doc.Document, tr *tag.Tree, opt Options) (*doc.Outline, Stats) {
	if d == nil {
		return &doc.Outline{}, Stats{}
	}
	out := &doc.Outline{Meta: d.Meta}
	if tr == nil || tr.Root == nil {
		out.Preamble = allBlocks(d)
		return out, Stats{Blocks: len(out.Preamble)}
	}

	b := &builder{opt: opt, index: newIndex(d)}
	b.visit(tr.Root)

	out.Sections = b.roots
	out.Preamble = b.preamble
	out.Unplaced = b.index.unplaced(d)

	var st Stats
	st.Blocks = b.blocks
	for _, p := range out.Unplaced {
		st.UnplacedBlocks += len(p.Blocks)
		for _, blk := range p.Blocks {
			st.UnplacedChars += len(blk.Text())
		}
	}
	out.Walk(func(s *doc.Section) bool {
		st.Sections++
		if s.Title != "" {
			st.Titled++
		}
		if s.Number != "" {
			st.Numbered++
		}
		if s.Level > st.MaxLevel {
			st.MaxLevel = s.Level
		}
		return true
	})
	return out, st
}

// builder accumulates sections as the tree is descended.
type builder struct {
	opt   Options
	index *index

	roots    []*doc.Section
	preamble []doc.Block
	blocks   int

	// stack is the open sections from the outermost down to the innermost. It is the
	// entire hierarchy computation: headings arrive in reading order, so a new
	// section's parent is the deepest open section of lower level, and nothing has to
	// look ahead.
	stack []*doc.Section
}

// visit descends one element.
//
// Three cases, and the split is what keeps a paragraph whole. A heading opens a
// section. An element whose role names a content block emits exactly one block, made
// from its own text and that of its inline descendants. Everything else — Document,
// Part, Sect, Div, L, Table, TR, Span, Link — is transparent: it contributes nothing
// of its own and its children are visited in order.
func (b *builder) visit(e *tag.Elem) {
	if e == nil {
		return
	}
	if e.Role.IsHeading() {
		b.heading(e)
		return
	}
	if role, ok := blockRole(e.Role); ok {
		b.block(e, role)
		return
	}
	// A transparent element with marked content of its own is a producer putting text
	// directly under a container. It becomes a paragraph, emitted before the kids
	// rather than interleaved with them: tag.Elem keeps Content and Kids in separate
	// slices, so their relative order in the /K array is not recoverable here. It
	// matters only for a document that mixes both under one element, which is rare
	// and is a tagging defect where it happens.
	if len(e.Content) > 0 {
		var pg span
		pg.add(e.Page)
		for _, r := range e.Content {
			pg.add(r.Page)
		}
		b.emit(e, doc.RoleParagraph, b.index.take(e.Content), pg)
	}
	for _, k := range e.Kids {
		b.visit(k)
	}
}

// heading opens a section and closes every section it outranks.
func (b *builder) heading(e *tag.Elem) {
	level := e.Depth()
	if level < 1 {
		// A heading role tag.Depth cannot rank — a custom role mapped to H by /RoleMap
		// in a tree with no grouping ancestors. Level 1 keeps it in the outline;
		// dropping it would lose the clause and everything under it.
		level = 1
	}

	// Gathered before the section is opened, because the title has to be resolved
	// first and resolving it consumes the heading's spans — otherwise the heading text
	// would appear again at the top of the section's first paragraph.
	var spans []*doc.Span
	var nested []*tag.Elem
	var pg span
	b.gather(e, &spans, &nested, &pg)

	title := b.title(e, spans)
	sec := &doc.Section{
		Title:  title,
		Level:  level,
		Number: clauseNumber(title),
	}

	// A heading of equal or higher rank closes everything down to it. That is the
	// "runs until the next heading of equal-or-higher level" rule, as a stack rather
	// than as a lookahead.
	for len(b.stack) > 0 && b.stack[len(b.stack)-1].Level >= level {
		b.stack = b.stack[:len(b.stack)-1]
	}
	if n := len(b.stack); n > 0 {
		parent := b.stack[n-1]
		sec.Parent = parent
		parent.Kids = append(parent.Kids, sec)
	} else {
		b.roots = append(b.roots, sec)
	}
	b.stack = append(b.stack, sec)

	// The heading's own pages anchor the section straight away, so a clause whose body
	// could not be anchored still reports where its heading was.
	sec.FirstPage, sec.LastPage = pg.lo, pg.hi

	// A block-level element nested inside a heading — a Figure in a chapter title, a
	// Link element the producer wrapped in a block role. Visited now that the section
	// is open, so its content lands inside the clause it belongs to.
	for _, n := range nested {
		b.visit(n)
	}
}

// title resolves a heading's text.
//
// /T first when it is filled in, since a producer that wrote it stated the title
// outright. It is not the source of truth: 0 of 981 headings in ISO 32000-2 have a
// non-empty /T, nor 0 of 183 in Well-Tagged-PDF-WTPDF-1.0.pdf, so on the corpus this
// always falls through to the content join.
//
// The join is at span granularity, which is not an implementation detail. A block is
// a *layout* unit and a heading is a *logical* one: on WTPDF, 12% of headings share a
// block with the body text after them, because the extractor's paragraph heuristic
// saw one paragraph where the tree declares a heading and a definition. Joining on
// doc.Block.MCIDs turns "4.1 artifact marked content sequence" into "4.1 artifact
// marked content sequence artifact defined solely by a marked content sequence…", and
// the specification's worst case into a 518-character title. Joining on doc.Span.MCID
// does not, because the extractor starts a new span at every MCID change, so a span
// never straddles a marked-content boundary. Measured: max title 518 → 149 on the
// specification, p90 101 → 40 and max 709 → 74 on WTPDF.
//
// spans are the heading's own, already claimed by the caller so that the heading text
// does not also appear at the top of the section's first paragraph.
func (b *builder) title(e *tag.Elem, spans []*doc.Span) string {
	if t := clean(e.Title); t != "" {
		return b.truncate(t)
	}

	var sb strings.Builder
	for _, s := range spans {
		sb.WriteString(s.Text)
	}
	if t := clean(sb.String()); t != "" {
		return b.truncate(t)
	}
	// No glyphs. /ActualText last rather than first because it is a substitution for
	// what the glyphs spell, and where both exist the glyphs are what a reader
	// checking the conversion sees on the page.
	return b.truncate(clean(e.ActualText))
}

func (b *builder) truncate(s string) string {
	limit := b.opt.maxTitle()
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	// Back off any partial rune the byte-offset cut left behind. Stripping only
	// continuation bytes is not enough: a cut can also land immediately after a lead
	// byte, and the result is invalid UTF-8 either way — which both a YAML value and a
	// filename reject. Bounded at three iterations for valid input, since no encoded
	// rune is longer than four bytes.
	for len(cut) > 0 {
		r, n := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || n > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	// Then to the last word boundary, if one is close enough that the result still says
	// something.
	if i := strings.LastIndexByte(cut, ' '); i > limit/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ")
}

// block emits one content block for e, then descends into the block-level elements
// nested inside it — a P inside a TD, a Caption inside a Figure.
func (b *builder) block(e *tag.Elem, role doc.Role) {
	var spans []*doc.Span
	var nested []*tag.Elem
	var pg span
	b.gather(e, &spans, &nested, &pg)
	b.emit(e, role, spans, pg)
	for _, n := range nested {
		b.visit(n)
	}
}

// gather collects e's spans together with those of its transparent descendants,
// and separates out the descendants that start blocks of their own.
//
// The nested elements are visited after the gathered text rather than in place, for
// the same reason the transparent case flushes before its kids: /K order between an
// element's own marked content and its children is not preserved by tag.Elem. An
// element that has both is a tagging defect, and the ones that matter here — a TD
// wrapping a P, a Figure wrapping a Caption — have children only.
func (b *builder) gather(e *tag.Elem, spans *[]*doc.Span, nested *[]*tag.Elem, pg *span) {
	*spans = append(*spans, b.index.take(e.Content)...)
	pg.add(e.Page)
	for _, r := range e.Content {
		pg.add(r.Page)
	}
	for _, k := range e.Kids {
		if k.Role.IsHeading() {
			*nested = append(*nested, k)
			continue
		}
		if _, ok := blockRole(k.Role); ok {
			*nested = append(*nested, k)
			continue
		}
		b.gather(k, spans, nested, pg)
	}
}

// span is an inclusive page range being accumulated. Zero is "no page", so it is
// never a bound.
type span struct{ lo, hi int }

func (s *span) add(page int) {
	if page <= 0 {
		return
	}
	if s.lo == 0 || page < s.lo {
		s.lo = page
	}
	if page > s.hi {
		s.hi = page
	}
}

// emit attributes a block to the innermost open section, or to the preamble when no
// heading has been seen yet.
//
// pg is the range of pages the block's content was found on, which is a range and not
// a number because a paragraph continuing past a page break is one block on two
// pages. Taking e.Page instead would report only where the element was anchored, and
// a section's LastPage would then stop short of its own final paragraph.
func (b *builder) emit(e *tag.Elem, role doc.Role, spans []*doc.Span, pg span) {
	blk := doc.Block{
		Role: role,
		Lang: e.Lang,
		Alt:  alt(e),
	}
	if role == doc.RoleListItem {
		blk.Level = listDepth(e)
	}
	blk.Spans = make([]doc.Span, 0, len(spans))
	for _, s := range spans {
		blk.Spans = append(blk.Spans, *s)
		blk.Box = blk.Box.Union(s.Box)
		blk.MCIDs = append(blk.MCIDs, s.MCID)
	}
	if blk.IsEmpty() {
		return
	}
	b.blocks++

	if n := len(b.stack); n == 0 {
		b.preamble = append(b.preamble, blk)
		return
	}
	sec := b.stack[len(b.stack)-1]
	sec.Blocks = append(sec.Blocks, blk)
	if sec.FirstPage == 0 || (pg.lo > 0 && pg.lo < sec.FirstPage) {
		sec.FirstPage = pg.lo
	}
	if pg.hi > sec.LastPage {
		sec.LastPage = pg.hi
	}
}

// alt returns the element's replacement or alternate text, preferring /ActualText.
// /ActualText says what the content *is* where the glyphs do not spell it;
// /Alt describes it for a reader who cannot see it. For a Figure only /Alt exists,
// and it is the only text there is.
func alt(e *tag.Elem) string {
	if e.ActualText != "" {
		return e.ActualText
	}
	return e.Alt
}

// listDepth returns how deeply a list item is nested, 1-based, by counting enclosing
// L elements. A sink indents from this rather than reconstructing the nesting itself.
func listDepth(e *tag.Elem) int {
	n := 0
	for p := e; p != nil; p = p.Parent {
		if p.Role == tag.RoleL {
			n++
		}
	}
	if n == 0 {
		// A list item outside any list. Common in TOCs, where TOCI appears under TOC.
		return 1
	}
	return n
}

// blockRole maps a structure role to the block role it emits, and reports whether it
// starts a block at all.
//
// The false cases are the load-bearing ones and they divide into two kinds.
// Containers — Document, Part, Sect, Div, L, Table, TR, TOC — express nesting and
// hold no text; treating one as a block would swallow every clause beneath it, which
// is the 7-sections failure this package exists to avoid. Inline elements — Span,
// Link, Note, Quote's inline cousin — sit *inside* a paragraph, and treating one as a
// block would split that paragraph at every italic word and emphasized term.
//
// An unrecognized role is transparent. That is the safe default in both directions:
// a custom container keeps its children, and a custom inline element keeps its text
// in the surrounding paragraph. Its own marked content, if it has any, still becomes
// a paragraph via visit.
func blockRole(r tag.Role) (doc.Role, bool) {
	switch r {
	case tag.RoleP, tag.RoleFormula:
		return doc.RoleParagraph, true
	case tag.RoleLI, tag.RoleTOCI:
		return doc.RoleListItem, true
	case tag.RoleTD, tag.RoleTH:
		return doc.RoleTableCell, true
	case tag.RoleCode:
		return doc.RoleCode, true
	case tag.RoleBlockQuote:
		return doc.RoleQuote, true
	case tag.RoleCaption:
		return doc.RoleCaption, true
	case tag.RoleFigure:
		return doc.RoleFigure, true
	case tag.RoleArtifact:
		return doc.RoleArtifact, true
	}
	return "", false
}

// key is a (page, MCID) pair: the join between page text and a structure element.
type key struct {
	page int
	mcid int
}

// index maps marked-content identifiers to the spans the extractor produced from
// them, and records which have been claimed.
//
// Claiming is per span rather than per key because one key can only be claimed once
// while one *block* commonly carries several keys, so an index keyed by block would
// hand the same text to two elements. It is also what makes the recovery pass
// possible: a span is claimed by exactly one element or by none, so what is left
// afterwards is exactly what the tree did not account for.
type index struct {
	spans    map[key][]*doc.Span
	consumed map[*doc.Span]bool
}

func newIndex(d *doc.Document) *index {
	ix := &index{
		spans:    map[key][]*doc.Span{},
		consumed: map[*doc.Span]bool{},
	}
	for pi := range d.Pages {
		p := &d.Pages[pi]
		for bi := range p.Blocks {
			blk := &p.Blocks[bi]
			for si := range blk.Spans {
				sp := &blk.Spans[si]
				if sp.MCID < 0 {
					// Drawn outside any marked-content sequence, so no element can name
					// it and no key would ever be looked up. Not indexed, but still
					// unconsumed, so the recovery pass keeps it.
					continue
				}
				k := key{p.Number, sp.MCID}
				ix.spans[k] = append(ix.spans[k], sp)
			}
		}
	}
	return ix
}

// take returns the unclaimed spans for an element's marked content, in the order the
// element lists it, and marks them claimed.
//
// A reference with page 0 contributes nothing. An MCID is unique only within a page,
// so a reference whose element chain never named one cannot be joined at all —
// joining on the identifier alone would attach page 500's paragraph to page 1's
// heading. Such content is not lost: it stays unconsumed and reaches Unplaced.
func (ix *index) take(refs []tag.MCRef) []*doc.Span {
	var out []*doc.Span
	for _, r := range refs {
		if r.Page <= 0 {
			continue
		}
		for _, sp := range ix.spans[key{r.Page, r.MCID}] {
			if ix.consumed[sp] {
				continue
			}
			ix.consumed[sp] = true
			out = append(out, sp)
		}
	}
	return out
}

// unplaced returns the document's remaining text: every block whose spans no
// structure element claimed, grouped by page and in page order.
//
// A block is rebuilt from its unclaimed spans rather than taken whole, because a
// partly-claimed block would otherwise repeat in the outline the text a section
// already holds. Blocks are the extractor's, so their roles and boxes survive; only
// the spans an element took are removed.
func (ix *index) unplaced(d *doc.Document) []doc.Page {
	var out []doc.Page
	for pi := range d.Pages {
		p := &d.Pages[pi]
		var left doc.Page
		for bi := range p.Blocks {
			blk := &p.Blocks[bi]
			keep := doc.Block{
				Role:  blk.Role,
				Level: blk.Level,
				Lang:  blk.Lang,
				Alt:   blk.Alt,
			}
			for si := range blk.Spans {
				sp := &blk.Spans[si]
				if ix.consumed[sp] {
					continue
				}
				keep.Spans = append(keep.Spans, *sp)
				keep.Box = keep.Box.Union(sp.Box)
				if sp.MCID >= 0 {
					keep.MCIDs = append(keep.MCIDs, sp.MCID)
				}
			}
			if keep.IsEmpty() {
				continue
			}
			// An Alt on a block whose spans were all claimed would otherwise reappear
			// here as content with no text, since IsEmpty treats Alt as content.
			if len(keep.Spans) == 0 {
				continue
			}
			left.Blocks = append(left.Blocks, keep)
		}
		if len(left.Blocks) == 0 {
			continue
		}
		left.Number, left.Box = p.Number, p.Box
		left.Rotate, left.Rasterized = p.Rotate, p.Rasterized
		out = append(out, left)
	}
	return out
}

// allBlocks returns every non-empty block in the document, for the no-tree case.
func allBlocks(d *doc.Document) []doc.Block {
	var out []doc.Block
	for pi := range d.Pages {
		for _, blk := range d.Pages[pi].Blocks {
			if blk.IsEmpty() {
				continue
			}
			out = append(out, blk)
		}
	}
	return out
}

// clean collapses a resolved title to one line of single-spaced text.
//
// Titles come out of the join with the page's own spacing: a tab between the clause
// number and the text, a non-breaking or en space inside a term, and a trailing space
// almost always. None of that survives being a filename or a YAML value, and
// normalizing once here means no consumer has to.
func clean(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = sb.Len() > 0
			continue
		}
		if space {
			sb.WriteByte(' ')
			space = false
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// clauseNumber returns the leading clause number of a title, or "".
//
// "7.5.8 Filters" gives "7.5.8". "Annex A", "A.1 General" and "Foreword" give "": an
// annex is lettered and its subsections are not in the same sequence as the numbered
// clauses, so admitting "A.1" would sort it among them. A trailing dot is dropped,
// because "7.5.8." and "7.5.8" name the same clause and a stable identifier must not
// depend on which the typesetter used.
//
// A bare numeral counts, so "1 Scope" is clause 1. That admits a false positive on a
// heading that starts with a number for some other reason, which is why the number is
// a separate field rather than something recovered from the title at each use: a
// consumer that finds it implausible still has the whole title.
func clauseNumber(title string) string {
	i := strings.IndexAny(title, " \t")
	if i < 0 {
		// A title that is nothing but a number is a heading whose text failed to
		// resolve, not a clause number worth recording.
		return ""
	}
	tok := strings.TrimRight(title[:i], ".")
	digits := false
	prevDot := true
	for j := 0; j < len(tok); j++ {
		switch c := tok[j]; {
		case c >= '0' && c <= '9':
			digits = true
			prevDot = false
		case c == '.':
			if prevDot {
				// An empty component: "7..8", or a leading dot. Not a clause number.
				return ""
			}
			prevDot = true
		default:
			return ""
		}
	}
	if !digits {
		return ""
	}
	return tok
}

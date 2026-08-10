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

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/tag"
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

	return out, b.stats(out)
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

	// tables numbers each Table element the first time one of its cells is emitted.
	// See tableNum.
	tables map[*tag.Elem]int
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
	b.gather(e, &spans, &nested, &pg, false)

	b.open(b.title(e, spans), level, pg)

	// A block-level element nested inside a heading — a Figure in a chapter title, a
	// Link element the producer wrapped in a block role. Visited now that the section
	// is open, so its content lands inside the clause it belongs to.
	for _, n := range nested {
		b.visit(n)
	}
}

// open starts a section at level, closing every open section it outranks.
//
// This is the hierarchy computation in full, and it is separate from heading because it
// is the half that does not know where its input came from: a (level, title, pages)
// triple is all it reads. The package comment says a structure tree and font-size
// clustering both produce those, and Untagged is the second producer — a levelled
// sequence of blocks drives the same stack that a tree's H1..H6 elements do.
func (b *builder) open(title string, level int, pg span) {
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
	// something. Found by rune rather than by byte: a producer writes a word boundary with
	// whatever space character its typography calls for, and a document setting every gap as
	// U+2002 EN SPACE has no ' ' anywhere for a byte search to find — so the fallback would
	// be a mid-word cut. Nothing on disk reaches this, since no title in the corpus is 200
	// bytes long, but sink/okf's truncation has the same rule and 4 of its descriptions were
	// cut that way.
	if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > limit/2 {
		cut = cut[:i]
	}
	return strings.TrimRightFunc(cut, unicode.IsSpace)
}

// block emits one content block for e, then descends into the block-level elements
// nested inside it — a P inside a TD, a Caption inside a Figure.
func (b *builder) block(e *tag.Elem, role doc.Role) {
	var spans []*doc.Span
	var nested []*tag.Elem
	var pg span
	// A list item's label is read before its body is gathered, and taking its spans is
	// what keeps them out of the body — index.take marks a span claimed, so gathering
	// first would leave the label with nothing to take and fold it into the text.
	marker := ""
	if role == doc.RoleListItem {
		marker = b.label(e)
	}
	b.gather(e, &spans, &nested, &pg, wrapsText(role))
	b.emitItem(e, role, spans, pg, marker)
	for _, n := range nested {
		b.visit(n)
	}
}

// wrapsText reports whether a block of this role holds its text in a wrapping
// paragraph rather than directly, which makes that paragraph transparent.
//
// Both roles here were measured, not assumed. A list item's body is LI > LBody > Part
// > P in LaTeX's tagging backend, which gather documents at length. A table cell is
// the same shape and more absolute: of 17482 TD and TH elements on disk, **0** hold
// marked content of their own and all 17370 non-empty ones wrap it in a P. So a cell
// that is not transparent emits no spans, is dropped by IsEmpty, and its text detaches
// as a free paragraph — which is why a tagged table read as scattered one-cell
// paragraphs rather than as a table.
//
// A heading is deliberately absent. Its text is resolved by title against its own
// spans, and a P inside one is a producer wrapping a title it also drew directly;
// nothing on disk does it, and making it transparent would change what title reads
// with no case to measure against.
func wrapsText(role doc.Role) bool {
	return role == doc.RoleListItem || role == doc.RoleTableCell
}

// label takes a list item's declared /Lbl out of the item and returns it.
//
// PDF declares a list's marker as its own structure element — ISO 32000-2 §14.8.4.5.3,
// where an LI holds a Lbl for the label and an LBody for the content. sectionize used to
// map neither, so both fell into gather's transparent default and the label's spans were
// appended to the item's text indistinguishably from its content. The result reached the
// output as "- ■ text": the sink's own bullet, then the producer's, then the item.
//
// It is not handled by giving Lbl a block role, which is the fix that first suggests
// itself and is wrong — a role would emit the label as a block of its own, turning one
// item into two. The label is not a block; it is a field of one.
//
// Only a direct child, and only the first. Measured over every tagged list item on disk
// that declares one, the Lbl is a direct kid of its LI in 153 of 153 — so searching deeper
// for the *element* would buy nothing and would risk reaching into a nested list's items,
// whose labels belong to them. "The first" is unmeasurable rather than measured: no item on
// disk declares two, 153 declare exactly one and 1672 declare none of 1825 items, so taking
// the last instead is a change no input distinguishes. First, because a second Lbl is a
// producer error and the first is the one the item opens with.
//
// That 1825 is counted three ways because the earlier figure implied 2062 and no population
// reaches it: the structural walk, a pointer-identity check that no element is reached twice,
// and a store-wide count of indirect /S /LI dictionaries. The last disagrees on two files and
// is wrong on both — sampleInvoice.pdf's 17 LI objects are referenced only from the IDTree
// under /Names while its live /S /Document has no /K, and tagged-lists.pdf writes its elements
// direct rather than indirect so the count misses 6. CHANGELOG.md records the reconciliation.
//
// The Lbl's *text* is a different question from the Lbl's position, and this reads only the
// element's own marked content, which is not where most producers put it: 100 of the 153
// hold the marker in a Span inside the Lbl, so this returns "" for them and markItem falls
// through to the glyph. Benign on this corpus — StripMarker recovers the same "■" from the
// text, and every one of the 16 ordered labels, the case with no glyph fallback, is owned
// by the Lbl directly — but it means "a label exists" is not "a label was read", and the
// declared path is running on the glyph rule in two thirds of the cases where a
// declaration was available. DESIGN.md's open questions record it.
//
// The label's pages still reach the item's range, and not from here: Lbl has no block
// role, so the gather that follows recurses into it and adds its pages the way it does for
// any transparent element. Only the *spans* are claimed here. Adding the pages here as
// well would be a second copy of that bookkeeping which no input could distinguish from
// the first.
func (b *builder) label(e *tag.Elem) string {
	for _, k := range e.Kids {
		if k.Role != tag.RoleLbl {
			continue
		}
		var sb strings.Builder
		for _, sp := range b.index.take(k.Content) {
			sb.WriteString(sp.Text)
		}
		// Empty for 102 of the 153 Lbl on disk, and only 2 of those are the producer's
		// doing — the other 100 own their marker one level down, in a Span. So the empty
		// string is the honest answer about what this element *owns* and a misleading one
		// about what the producer declared; the glyph rule below is what covers the gap.
		return strings.TrimSpace(sb.String())
	}
	return ""
}

// gather collects e's spans together with those of its transparent descendants,
// and separates out the descendants that start blocks of their own.
//
// The nested elements are visited after the gathered text rather than in place, for
// the same reason the transparent case flushes before its kids: /K order between an
// element's own marked content and its children is not preserved by tag.Elem. An
// element that has both is a tagging defect, and the ones that matter here — a TD
// wrapping a P, a Figure wrapping a Caption — have children only.
//
// wraps says the block being built holds its text in a wrapping paragraph — a list
// item or a table cell, per wrapsText — which makes a paragraph inside it transparent.
// ISO 32000-2 Table 364 lets an LBody hold any block-level element, and LaTeX's tagging
// backend uses that: it writes LI > LBody > Part > P, so the item's whole body is a
// wrapped paragraph. Detaching it emits the item with no spans — dropped by IsEmpty —
// and the body as a paragraph of its own, which loses the list entirely: six items
// became six bare paragraphs, and the marker they had just been given went with the
// block that was thrown away.
//
// A cell is the same shape and is the more absolute case: 0 of 17482 TD and TH elements
// on disk hold marked content directly and all 17370 non-empty ones wrap it in a P, so
// without this every tagged table's cells detached into free paragraphs. 752 of those
// cells hold more than one P, which gather joins into the one cell rather than the
// several paragraphs a producer's line breaks would otherwise become.
//
// Only a paragraph. A Figure in an LBody is a picture and belongs in its own block,
// which is the one such case on disk; a nested list detaches because its LI has a block
// role of its own, so the recursion below reaches it through the transparent L and stops
// there. The same holds inside a cell, where it is what keeps the 42 cells containing a
// list and the 13 containing a nested table from being flattened into their cell's text.
//
// "A paragraph" is doc.RoleParagraph, which blockRole also gives a Formula — so a Formula
// wrapped in an item's LBody is transparent too, which is wider than this rule was written
// for and is unobservable: no corpus document declares a Formula at all, against 1742 LI
// and 29400 P on ISO 32000-2 alone. Left as it is rather than narrowed to tag.RoleP,
// because an item whose body is a formula is an item, and detaching it is the defect above
// with a different element name.
//
// wraps does not survive a block boundary, which is what keeps this narrow: every element
// with a block role is detached here and re-entered through block, where the flag is set
// from that element's own role. So a nested LI, or a TOCI inside a TOC inside a TOCI,
// gathers with the flag set from its own role and not its parent's.
func (b *builder) gather(e *tag.Elem, spans *[]*doc.Span, nested *[]*tag.Elem, pg *span, wraps bool) {
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
		if r, ok := blockRole(k.Role); ok && !(wraps && r == doc.RoleParagraph) {
			*nested = append(*nested, k)
			continue
		}
		b.gather(k, spans, nested, pg, wraps)
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
	b.emitItem(e, role, spans, pg, "")
}

// emitItem is emit with a list item's declared label. Separate so that emit's four other
// callers — a heading's body, a transparent element's own content, a table cell — cannot
// pass one by accident.
func (b *builder) emitItem(e *tag.Elem, role doc.Role, spans []*doc.Span, pg span, marker string) {
	blk := doc.Block{
		Role: role,
		Lang: e.Lang,
		Alt:  alt(e),
	}
	if role == doc.RoleListItem {
		blk.Level = listDepth(e)
	}
	if role == doc.RoleTableCell {
		blk.Cell = cellAt(e, b.tableNum(e))
	}
	blk.Spans = make([]doc.Span, 0, len(spans))
	for _, s := range spans {
		blk.Spans = append(blk.Spans, *s)
		blk.Box = blk.Box.Union(s.Box)
		blk.MCIDs = append(blk.MCIDs, s.MCID)
	}
	// Before IsEmpty, though on this corpus it makes no difference: ListMarker requires
	// content after the separator, so a strip cannot empty a block, and all 16 declared
	// ordered labels leave content behind. Ordered this way because the reverse asks
	// IsEmpty about text that still holds a marker, and a block whose only content is its
	// marker would then be emitted as a bullet with nothing after it.
	if role == doc.RoleListItem {
		markItem(&blk, marker)
	}
	b.place(blk, pg)
}

// place attributes a finished block to the innermost open section, or to the preamble
// when no heading has been seen yet, and widens that section's page range to cover it.
//
// Separate from emitItem for the same reason open is separate from heading: this half
// reads a doc.Block and a page range and nothing about where they came from, so Untagged
// places extract's own blocks through it rather than rebuilding them span by span.
func (b *builder) place(blk doc.Block, pg span) {
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

// markItem records a list item's marker, from the producer's declaration where there is
// one and from the glyph the item's text opens with where there is not.
//
// Two sources for one field, and the order between them is the whole rule: a declaration
// is evidence and a glyph is a guess, so the declaration is taken whenever it exists and
// the glyph is never consulted then. That is the same precedence md.go states for not
// running inferRoles over a structure tree.
//
// # Why the glyph rule runs here at all
//
// It is reached only for a block the producer already declared RoleListItem, which is
// what makes it a different act from inferring a role. Nothing is being guessed about
// what the block *is*; the question is only which of its runes is the label it was
// declared to have, and a marker glyph followed by whitespace at the start of a declared
// list item is not a coincidence anyone has to weigh.
//
// That is also why the strip is StripDeclaredMarker and not StripMarker: the declaration
// buys a wider vocabulary. layout, reading the same glyph off a page that declared nothing,
// has to weigh a hyphen against the command-line flags and C comments it is glued to in 12
// of 13 occurrences; here the role is settled before the glyph is read. doc.declaredMarkers
// records the one glyph that difference admits and the 3 items it fixes.
//
// The measurement says so directly. Of 1415 declared list items on disk whose text opens
// with a marker glyph, 124 also declare a /Lbl saying what their label is — and the
// label's first rune is that glyph in 124 of 124, with 0 disagreeing. So on every case
// where a declaration exists to check the glyph against, the glyph is right. The
// remaining 1291 declare no label, and without this they emit "- ■ text".
//
// The 3 that the hyphen added all landed in that second half: each declares no /Lbl element
// at all, so none can enter the agreement population, and 124 of 124 is unmoved rather than
// merely still passing. Each figure was re-walked rather than adjusted — 1288 + 3 = 1291 is
// arithmetic that assumes the conclusion, where a walk reporting 1412 and 1415 from the same
// pass says the +3 is the only difference.
//
// That 124 is a label read that *descends* into the Lbl, which is not the read label() does:
// 24 of the 124 own their marker directly and the other 100 hold it in a Span kid. So the
// agreement figure is about what the producer declared, and label() sees a quarter of it —
// the gap that comment records, arrived at from the other side.
//
// # What the declaration adds that no glyph could
//
// 16 of the declared labels are not bullets: "a.", "b." and "[1]" through "[7]" in
// Well-Tagged-PDF-WTPDF-1.0.pdf, and "1." through "3." in testdata/reference/tagged-lists.pdf.
// An ordered label is unreachable from the glyph side — ADR 0011 records why, a leading
// number being also what a heading and a table row open with — and it is exactly the case
// where dropping the marker instead of keeping it would lose text the page says. Stripping
// all 16 leaves the item's own content non-empty, so none is a label masquerading as content.
func markItem(blk *doc.Block, marker string) {
	if marker != "" {
		blk.SetMarker(marker)
		return
	}
	blk.StripDeclaredMarker()
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

// tableNum returns a stable number for the table a cell sits in, assigning the next
// one the first time that table is seen.
//
// Numbered on demand rather than counted during the walk because Table is transparent:
// visit never stops at one, so there is no point at which a table could be opened and
// closed. Keyed on the element pointer, which is what makes an inner table's cells all
// agree with each other and differ from the outer table's — the 13 nested tables on
// disk. Cells arrive in reading order, so the numbers come out in document order.
func (b *builder) tableNum(e *tag.Elem) int {
	var tbl *tag.Elem
	for p := e.Parent; p != nil; p = p.Parent {
		if p.Role == tag.RoleTable {
			tbl = p
			break
		}
	}
	if tbl == nil {
		return 0
	}
	if n, ok := b.tables[tbl]; ok {
		return n
	}
	if b.tables == nil {
		b.tables = map[*tag.Elem]int{}
	}
	// The count after insertion, so the first table is 1 and 0 stays reserved for a
	// cell with no enclosing table.
	n := len(b.tables) + 1
	b.tables[tbl] = n
	return n
}

// cellAt locates a cell in its table, from the tree rather than from geometry.
//
// The row and column are the cell's ordinal position among the cells its TR declares,
// and the row's position among the TRs its Table declares — counted here rather than
// tracked as the tree is walked, because Table and TR are transparent and a cell block
// is emitted without either of them on any stack. Counting from the element's own
// ancestry keeps the position a property of the cell, which is what makes it correct
// for the 13 nested tables: an inner cell's Table is the inner Table element.
//
// A THead, TBody or TFoot between the Table and its rows is skipped rather than
// counted, since it groups rows without renumbering them — a header row is row 0 of its
// table whether or not a THead wraps it. Absent that, every document that wraps its
// rows would report a table with one row per group.
//
// nil when the cell has no enclosing Table, which is a producer declaring a TD outside
// one. The cell still emits its text as a block; only its position is unknown, and
// guessing one would put it in a grid with nothing else in it.
func cellAt(e *tag.Elem, table int) *doc.Cell {
	// The TR is the nearest enclosing row and the Table the nearest enclosing table.
	// Nearest rather than outermost is what makes a nested table's cell belong to the
	// inner table: walking to the top would give every cell the outer one.
	var tr, tbl *tag.Elem
	for p := e.Parent; p != nil; p = p.Parent {
		if tr == nil && p.Role == tag.RoleTR {
			tr = p
			continue
		}
		if p.Role == tag.RoleTable {
			tbl = p
			break
		}
	}
	if tr == nil || tbl == nil {
		return nil
	}
	// The column is the cell's ordinal position among its row's cells, which requires the
	// cell to be one of that row's own kids. All 17482 TD and TH elements on disk are
	// direct children of their TR, but nothing in ISO 32000-2 §14.8.4.3.4 requires it —
	// so a cell that is not found is declined rather than assumed. Falling out of the
	// loop without a match would otherwise leave Col at the row's full cell count, which
	// places the cell past the end of its own row; a nil Cell emits the text as a
	// paragraph, which loses the grid and nothing else.
	col, found := 0, false
	for _, k := range tr.Kids {
		if k == e {
			found = true
			break
		}
		if k.Role == tag.RoleTD || k.Role == tag.RoleTH {
			col++
		}
	}
	if !found {
		return nil
	}
	return &doc.Cell{
		Table:  table,
		Row:    rowIndex(tbl, tr),
		Col:    col,
		Header: e.Role == tag.RoleTH,
	}
}

// rowIndex returns tr's 0-based position among tbl's rows, descending through the
// row-group elements that do not renumber them.
func rowIndex(tbl, tr *tag.Elem) int {
	n, found := 0, false
	var walk func(x *tag.Elem)
	walk = func(x *tag.Elem) {
		for _, k := range x.Kids {
			if found {
				return
			}
			switch {
			case k == tr:
				found = true
			case k.Role == tag.RoleTR:
				n++
			case k.Role == tag.RoleTHead || k.Role == tag.RoleTBody || k.Role == tag.RoleTFoot:
				walk(k)
			}
		}
	}
	walk(tbl)
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

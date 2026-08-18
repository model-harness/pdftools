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
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
	"github.com/model-harness/pdftools/geom"
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
	// directly under a container. It becomes a paragraph, and it is emitted in its /K
	// position among the kids rather than before all of them: a container holding text,
	// then a Sect, then more text is three things in that order, and emitting both runs
	// of text first would put the second one before the section it follows.
	//
	// Each run of content becomes its own paragraph, which is what separating them by a
	// kid already means — the alternative is one block whose halves were drawn on either
	// side of a section boundary.
	inOrder(e,
		func(refs []tag.MCRef) {
			var pg span
			pg.add(e.Page)
			for _, r := range refs {
				pg.add(r.Page)
			}
			b.emit(e, doc.RoleParagraph, b.index.take(refs), pg)
		},
		b.visit)
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
	b.gather(e, &spans, &nested, &pg, false, false)

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
	// No glyphs at all, so /ActualText is the only text there is. It is not consulted
	// ahead of the glyphs because it has already been applied to them: substituted
	// replaces a declaring element's spans on the way in, so a heading with both arrives
	// here holding the declared value as its span text. What is left for this branch is a
	// declaration whose marked content resolved to no spans, which substituted cannot
	// apply to because there is nothing to replace. 3 of the corpus's 4803 declarations
	// are that shape — a reference naming an MCID the page drew no text for — though all
	// 3 are on a P and reach the paragraph path rather than this one.
	//
	// Through inlineText as well, not clean alone: clean folds the declared line break,
	// because a break is whitespace, and leaves U+00AD, because it is not. A title is
	// inline text like a span's, so all three consumers of the raw value adapt it — here,
	// substituted, and emitItem's Replacement.
	return b.truncate(clean(inlineText(e.ActualText)))
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
	b.gather(e, &spans, &nested, &pg, wrapsText(role), linesText(role))
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
	return role == doc.RoleListItem || role == doc.RoleTableCell || role == doc.RoleCode
}

// linesText reports whether a block of this role holds its text as one paragraph per line,
// so that absorbing those paragraphs has to put the line breaks back.
//
// Only RoleCode, and the distinction from the two roles above it is the whole reason this is
// a second predicate rather than a wider wrapsText. A cell holding several P is one run of
// prose the producer happened to break across lines, and joining it with nothing is right —
// 752 cells across the 18 tagged files do exactly that. A code listing's P *is* a line, and
// the newline between two of them is content: it is what a fence exists to preserve, and
// concatenating them yields the single collapsed line this rule was written to stop.
//
// Measured rather than assumed, and the measurement is what named the defect. Of the 18 Code
// elements on disk, 7 carry their own marked content and already fenced correctly; the other
// 11 are Well-Tagged-PDF-WTPDF-1.0.pdf's, which hold no content of their own and 99 P kids
// between them. Every one of those 11 emitted nothing and was dropped by IsEmpty, so all 99
// lines escaped as ordinary paragraphs and not one of the listings was fenced.
//
// The membership requirement, since two rules now depend on it and neither can check it: a
// role belongs here only if every sink renders it somewhere a newline survives — a fence, not
// prose and not a scalar. That is a property of the sinks, not of this package, so adding a
// role here is a change to sink/markdown and sink/okf as much as to this line. A newline in a
// paragraph is folded to a space by every renderer, so the failure would be silent rather than
// loud; TestParagraphLinesDoNotBreakOnBaseline pins the one role that is deliberately out.
func linesText(role doc.Role) bool {
	return role == doc.RoleCode
}

// breakAtBaselines restores the line breaks of a lines block whose lines the producer
// declared as neither paragraphs nor /ActualText — they exist only as the fact that two
// consecutive spans were drawn at different heights.
//
// This is the second of the two things a listing's lines can be, and it needs its own rule
// because gather's cannot see it. gather writes a break where it absorbs a *paragraph*, so a
// producer who declares one P per line is served; PDF-Declarations.pdf declares a single Code
// holding 25 lines as 25 MCIDs under no P at all, and the sink then fenced a 25-line XML
// sample as one 892-character line. Nothing in the text marks those breaks: the page draws a
// space at each line end, so extract's wrap rule finds a boundary already written and infers
// nothing, and the newline is recoverable only from the geometry.
//
// Geometric where the rest of this file is declaration-driven, which is the reason it is
// confined to a role the producer declared. It is not a heuristic about what a block is —
// RoleCode is the producer's own statement — only about where its lines end, and a code
// listing is the one role for which that answer must survive to the sink at all: every other
// role either folds a newline to a space or, for a table cell, cannot hold one.
//
// LineFrac of the type size rather than a fixed epsilon, so a listing set in 6pt is measured
// against 6pt, and of the larger of the two sizes — which is the extractor's own line test
// (run.go's maxf(sy, prev.height)) rather than a second opinion about the same question. The
// larger size is also the conservative direction: a superscript half the height of its line
// clears half of its own size long before it clears half of the line's, so measuring against
// the small span would break a line in two at every raised digit. 49 of the corpus's 179
// adjacent pairs inside a Code block change size, and 0 of them are far enough apart for the
// four readings of "the type size" — this one, prev, cur, and the smaller — to disagree, so the
// fixtures rather than the corpus are what pin the choice.
//
// Idempotent with gather's rule rather than layered on it: a break is written only where there
// is not one already, so the 5 WTPDF listings that gather breaks correctly are untouched. On
// the corpus the two rules agree on 5 of the 6 multi-line Code blocks and this one is strictly
// wider — it supplies PDF-Declarations' 24 missing breaks, plus one WTPDF break gather cannot
// write because both sides of it are Spans inside the same P ("…report67890" wrapping into
// "</ pdfd:claimReport>").
func breakAtBaselines(blk *doc.Block) {
	out := make([]doc.Span, 0, len(blk.Spans))
	for i := range blk.Spans {
		sp := &blk.Spans[i]
		if n := len(out); n > 0 && newLine(&out[n-1], sp) {
			out = append(out, doc.Span{Text: "\n", MCID: -1})
		}
		out = append(out, *sp)
	}
	blk.Spans = out
}

// spaceAtGaps restores the space at a join the tagged path closed by dropping the span that
// carried it.
//
// The extractor already writes this space, and correctly: needSpace (run.go:474) infers one at
// every gap wider than SpaceFrac of the nominal space advance, and a leader's own trailing
// space is a real glyph besides. What loses it is the rebuild. take reads the MCID index, and
// newIndex skips MCID < 0, so a span drawn outside marked content cannot be claimed by any
// element — it reaches Unplaced instead. Decoration is exactly what producers leave unmarked,
// so the two words the decoration stood between are concatenated: PDF-Declarations' contents
// list draws "2 Scope", a dotted leader ending in a space, then "1", and the leader is an
// artifact, so the entry came out "2 Scope1".
//
// Not a threshold question, though it is expressed as one. Of the corpus's 14538 same-line
// adjacent pairs that are joined with no space on either side, the ratio of the gap to this
// test is p50 0.007, p90 0.073, p99 0.355 — a kerned pair touches — and the distribution then
// stops: a dense cluster at 0.404 to 0.435 (69 pairs, all of them ISO 32000-2 mathematical
// variables abutting punctuation), and nothing until 1.918. Above that lie exactly six: 1.918,
// 2.515, 2.596 in ISO 32000-2's L*a*b* definition and 99.873, 152.694, 219.760 in
// PDF-Declarations' contents list. So the band the threshold sits in is empty for 4.4×, and
// what separates a dropped span from tight typesetting is the order of magnitude rather than
// the constant.
//
// Two sizes, not one, because the extractor uses two. Its line test takes the larger of the
// pair — maxf(sy, prev.height) at run.go:462, and newLine matches it for the same reason — while
// its space test takes the incoming glyph's own advance (run.go:448, read per glyph and never
// maximised). So the same split is kept here: the larger size decides "one line", the following
// span's size decides "one space". Collapsing both onto one reading would be a second opinion
// about a question extract has already answered twice.
//
// The space advance is estimated rather than read, because doc.Style carries Size and no
// advance (block.go:267) and widening the type every sink reads to serve one rule is the wrong
// trade. See spaceAdvance for the estimate and for the 4× error the first version of this rule
// made by skipping it.
func spaceAtGaps(blk *doc.Block) {
	out := make([]doc.Span, 0, len(blk.Spans))
	for i := range blk.Spans {
		sp := &blk.Spans[i]
		if n := len(out); n > 0 && gapSpace(&out[n-1], sp) {
			out = append(out, doc.Span{Text: " ", MCID: -1})
		}
		out = append(out, *sp)
	}
	blk.Spans = out
}

// gapSpace reports whether prev and cur were drawn on one line with a gap between them that
// no character accounts for.
//
// Ordered before breakAtBaselines at the call site, so the two rules cannot both fire on one
// join: this one requires the pair to be on the same line and that one requires it not to be.
// Both skip MCID < 0, so neither reads the other's fabricated span as geometry.
//
// The order is documentation rather than a constraint, and mutation testing is what established
// that: swapping the two calls survives every test in the package. The predicates are exclusive
// per pair, so an insertion between one pair never changes another's answer, and all three
// fabricated spans in this package hold whitespace ("\n" here and at gather, " " above), which
// the space test below rejects before any geometry is read. That mutant is equivalent rather
// than uncovered, and a test asserting a difference would be asserting something untrue.
//
// The MCID guard is a different matter: it survives mutation from Tagged for the same reason,
// and it is still decisive in itself. The same pair answers false with it and true without —
// a zero box against a span at X0 400 is a 400pt gap — so what makes it unreachable is only
// that today's three insertions happen to be whitespace. That is a coincidence about this
// package's current contents, not a property of the rule, so TestGapSpaceIgnoresAFabricatedSpan
// pins it by calling gapSpace directly. Leaving a geometric rule to be saved by a text rule is
// what would make the next fabricated span — a marker, a separator — a silent defect.
//
// An empty side is not a join. take can return a span whose text is empty — the corpus has 1380
// such adjacent pairs, 1311 of them in ISO 32000-2 and the rest spread over six more files — and
// a space against nothing is a leading or trailing one, which is the sink's to trim rather than
// a boundary that was lost.
//
// Any space rune on either side, not the ' ' byte: a span ending in U+00A0 or U+2002 has a
// boundary already, and a byte scan cannot see one. This is the rune test extract's own
// endsWithSpace makes (run.go:1143) for the same reason, kept as a decode here rather than a
// third exported helper.
//
// That rune test is also what holds this rule apart from leadingIndent, and the MCID guard above
// is not — worth stating because the MCID guard is the one that looks like it would. leadingIndent
// adopts a whitespace run drawn outside marked content by indexing a copy that carries the
// following span's MCID (newIndex, :1367), and that copy is what take returns and emitItem
// assembles into the block this walks (:1064), so unlike this package's three fabricated spans it
// arrives here non-negative and its geometry does get read. What declines it is the whitespace
// test below, since leadingIndent adopts nothing else (:1460).
//
// Which matters because the two rules no longer answer one gap by construction. leadingIndent's
// threshold was SpaceFrac, so attached meant exactly not-a-space and one gap could not be both;
// split out as IndentAttachFrac it is 0.15 against this rule's 0.40, and the two now leave a band
// from 0.15 to 0.40 where a gap is neither. Nothing structural keeps the numbers in that order
// either — an IndentAttachFrac raised past SpaceFrac would make a gap attached and a word boundary
// at once, and no test would state it. The whitespace test is what makes that safe at any two
// values, which is why it is named here rather than left to be inferred from the constants.
func gapSpace(prev, cur *doc.Span) bool {
	if prev.MCID < 0 || cur.MCID < 0 {
		return false
	}
	if prev.Text == "" || cur.Text == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(prev.Text)
	first, _ := utf8.DecodeRuneInString(cur.Text)
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return false
	}
	// A size of zero makes both thresholds zero, which decides both questions the wrong way at
	// once: every positive gap becomes a space and every baseline that is not exactly equal
	// becomes another line. doc.Style.Size is sy from the composed text matrix and nothing
	// clamps it (extract/run.go:635), so a Tf of 0 or a degenerate matrix produces one; the
	// corpus has 0 of 96569 spans the extractor emits, so this is a guard rather than a measured
	// case — the 119 zero-size spans in a finished outline are all fabricated by this package,
	// which is a different quantity and the one a naive count reaches first. Declining is the
	// conservative answer — the join is left as the page drew it — and it is the same guard
	// extract makes on the same quantity one line from where it reads it (run.go:449).
	if prev.Style.Size <= 0 || cur.Style.Size <= 0 {
		return false
	}
	if !sameLine(prev, cur) {
		return false
	}
	tol := geom.DefaultTolerance
	return cur.Box.X0-prev.Box.X1 > tol.SpaceFrac*spaceAdvance(cur.Style.Size)
}

// sameLine reports whether two spans were drawn on the same line.
//
// The one reading of "the same line" this package has, shared by the three rules that ask —
// gapSpace, newLine, and leadingIndent. It was written three times before it was written once,
// and mutation testing is what found that: six mutants of the third copy survived, because
// every fixture that would have killed one of them was already killing its twin in the first.
// Duplicated tolerance arithmetic cannot be tested, only tested somewhere.
//
// LineFrac of the larger of the two sizes, which is the extractor's own line test
// (run.go's maxf(sy, prev.height)) rather than a second opinion about the same question; see
// breakAtBaselines for why the larger size is also the conservative direction.
//
// Unsigned, and the page guard below is why that is now a fixture's claim rather than a measured
// one. The witness used to be the cross-page rise — a listing continuing onto the next page steps
// *up* by most of a page — and that pair no longer reaches the arithmetic at all. Within a page
// the corpus has 0 upward steps in 3357 adjacent same-page pairs, so a signed comparison would
// pass every corpus assertion in this repo; TestCodeBreaksOnAnUpwardStep is what holds it, and a
// signed reading would silently take a column break, or a table continued in a header band, for
// one long line.
//
// Two pages are never the same line, whatever the arithmetic says, because a Y0 is only a
// position within its own page's user space. This package is where that matters: it joins spans in
// the order a structure element lists its content, and a paragraph continuing past a page break is
// one element naming two pages, so the comparison below was reading page n+1's coordinates as if
// they were page n's. Corpus-wide there are 7 such adjacent pairs across the 11 tagged documents,
// and all 7 survive the arithmetic by a wide margin — the smallest step is 107.7× its threshold,
// because a continuation starts at the top of the next page and the page before it ended at the
// bottom, so the two baselines are most of a page apart. What makes this a guard rather than a fix
// is that the margin is an artifact of where those paragraphs happen to break: a listing whose
// last line on page n sits at the same height as its first line on page n+1 — a short page, a
// two-line footnote, a table continued in a header band — steps by nothing at all and reads as one
// line, and there is no threshold that can tell that case from a real one.
//
// A span this package fabricated has Page 0 and an empty box, so it is a different page from every
// drawn span and is refused here too. That is the same answer the callers already give it on
// MCID < 0, except for a recovered leading indent, which is fabricated *from* a drawn span and so
// keeps its box and its page along with the MCID it is given; see leadingIndent.
func sameLine(a, b *doc.Span) bool {
	if a.Page != b.Page {
		return false
	}
	tol := geom.DefaultTolerance
	return math.Abs(a.Box.Y0-b.Box.Y0) <= tol.LineFrac*math.Max(a.Style.Size, b.Style.Size)
}

// spaceAdvance estimates the nominal space advance at a type size.
//
// extract measures the real thing from the font (run.go:448) and doc.Style does not carry it,
// so this is the one quantity gapSpace cannot read. Half an em is not a guess: it is the
// fallback extract itself uses when a font reports no space glyph (run.go:449), so a caller
// without an advance already has a documented answer and this is it.
//
// Using the em instead — SpaceFrac of Size — was the first version of this rule and it was
// wrong by the ratio between an em and a space, roughly 4×, which is a whole threshold. It
// left ISO 32000-2's "× (𝑥 −4 29)" joined at a gap of 2.313pt on an 8.04pt span: the same
// defect class as the three contents entries, on the same output line as one of the two
// formula joins this rule does fix, and the em proxy scored it 0.959 of the threshold. Review
// found the discrepancy that led here, so the band the first version claimed was empty for
// 53× was not empty at all.
func spaceAdvance(size float64) float64 { return 0.5 * size }

// newLine reports whether cur was drawn on a different line than prev, with no break already
// written between them.
//
// A fabricated span — MCID -1, which is gather's break and this rule's own — has no geometry,
// so it can be neither side of the comparison: its zero box would read as a 700pt jump from
// every line and put a second break after every first one. Skipping it also makes the two
// rules compose, since prev is then the last span that was really drawn.
//
// Exactly the negation of sameLine and not a comparison of its own, so a listing cannot be broken
// at a boundary gapSpace reads as one line, and so both rules inherit that one's page guard. The
// corpus's only listing that continues onto another page is PDF-Declarations', a 681pt rise into
// "<!-- Optional entries", and it is now decided by the page rather than by the rise: the two are
// the same answer there, and they are not the same answer where a continuation resumes at the
// height it left off at. See sameLine.
func newLine(prev, cur *doc.Span) bool {
	if prev.MCID < 0 || cur.MCID < 0 {
		return false
	}
	return !sameLine(prev, cur)
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
// The Lbl's *text* is a different question from the Lbl's position, and reading only the
// element's own marked content is not where most producers put it: 100 of the 153 Lbl on disk
// hold the marker in a Span one level down, so a shallow read returned "" for them and
// markItem fell through to the glyph. Hence the descent, and hence its bound.
//
// It stopped at the element itself for two thirds of the corpus, and the reason that was
// benign is exactly the reason it was still wrong. take() consumes only what it is given, so
// a marker left in a Span kid stayed unclaimed, gather folded it into the item's text ahead of
// the body, and StripDeclaredMarker read it back off the front — measured, all 100 recovered,
// 0 stranded and 0 of them an ordered label. What that arrangement cannot survive is the
// intersection it happens not to contain: an ordered label held in a Span kid has no glyph
// rule to fall through to, because a leading number is what a heading and a table row also
// open with. Both halves are the common shape here — 100 of 108 Lbl kids are a Span, and 16
// labels are ordered — and only their overlap is absent, which makes "no document does both"
// a fact about these 50 files rather than about producers.
//
// The bound is gather's, not a second rule: descend through a kid with no block role and stop
// at one that has one. A Lbl is not supposed to contain a block at all, and the boundary that
// matters is a nested list — an L inside a Lbl would otherwise put a sub-item's marker into
// its parent's label. Reusing blockRole means that boundary cannot drift from the one the rest
// of the walk enforces. Measured over every Lbl on disk: the first text sits at depth 1 in all
// 100 cases and Span is the only kid role that occurs, 108 times, so no corpus document
// exercises the stop — it is there for the shape ISO 32000-2 permits, not one on disk, and
// TestLabelStopsAtANestedList is what holds it.
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
		b.labelText(k, &sb)
		// Empty for 2 of the 153 Lbl on disk, and both are the producer's doing: the
		// element declares no marked content anywhere below it, so there is no label to
		// read and markItem's glyph rule is what covers them.
		return strings.TrimSpace(sb.String())
	}
	return ""
}

// labelText appends the text of e and of its descendants that do not start blocks of their
// own, claiming the spans as it goes.
//
// The stop is gather's, both halves of it, so the two cannot disagree about where a label
// ends: a kid with a block role is a block and its text is its own, and a heading kid is
// worse than a block — gather hands it to visit, which opens a section from it, so consuming
// its spans here would open that section with no title at all. Neither shape occurs on disk
// inside a Lbl. See label above for what the corpus does and does not exercise.
func (b *builder) labelText(e *tag.Elem, sb *strings.Builder) {
	inOrder(e,
		func(refs []tag.MCRef) {
			for _, sp := range substituted(e, b.index.take(refs)) {
				sb.WriteString(sp.Text)
			}
		},
		func(k *tag.Elem) {
			if _, ok := blockRole(k.Role); ok || k.Role.IsHeading() {
				return
			}
			b.labelText(k, sb)
		})
}

// inOrder calls content and kid in the order e's /K array declares them, interleaving the
// element's own marked content with its children.
//
// tag.Elem keeps the two in separate slices, so walking Content and then Kids reads all of
// one before any of the other. That is the right answer for the 89813 elements on disk that
// hold only one of them and wrong for the 767 that hold both: it moves every rune a child
// drew to the end of the parent's own text, 32022 of them across 13 documents. The visible
// damage is worst where the child is small — a Span wrapping a single soft hyphen is torn
// out of the middle of a word and left at the end of the paragraph, which is what put
// "constituent elements.--" in ISO/TS 32005's Table 1 with the hyphens of "exposi-tion" and
// "forma-ts" trailing behind it.
//
// The order is recovered from tag.MCRef.Order and tag.Elem.KidAt, both positions in the same
// /K array. Merged rather than sorted into one slice, because both are already in ascending
// order by construction.
//
// A tie between the two is unreachable, so the comparison below being < rather than <= is an
// equivalent mutation and no fixture can kill it. Each /K item takes exactly one branch of
// tag.readKids, and each branch grows exactly one of the two slices: an MCR dictionary
// appends one reference and yields no kid, a structure element yields a kid and appends
// nothing. Measured over the corpus to confirm the reading rather than assert it: of 90721
// elements, 0 have a kid and a reference at the same /K position, and 0 have Order or KidAt
// out of ascending order.
//
// len(KidAt) == len(Kids) is a fact about tag.Read, which appends to both in one statement,
// and not an invariant anything enforces: tag.Elem is exported with both fields settable, so
// an Elem built by hand can hold any combination. Handled rather than asserted, because the
// cost is two comparisons and the alternative is a panic in a library reading untrusted files.
// A kid with no position of its own sorts after all content, which is the pre-existing
// behaviour and keeps a hand-built fixture meaning what it meant; a position naming a kid
// that does not exist is ignored. TestInOrderSurvivesEveryKidAtSkew covers each skew.
func inOrder(e *tag.Elem, content func([]tag.MCRef), kid func(*tag.Elem)) {
	ci, ki := 0, 0
	for ci < len(e.Content) || ki < len(e.Kids) {
		// A run is every reference up to the next kid, not one per /K position. Two MCIDs
		// with no kid between them are one stretch of text, and handing them over
		// separately makes visit emit a paragraph per reference where the file draws one:
		// 286 extra paragraphs across 205 transparent elements in 9 documents, the worst
		// being Well-Tagged-PDF-WTPDF-1.0.pdf's 118. A Div holding two MCIDs and then a
		// Sect is the smallest shape that shows it.
		if ci < len(e.Content) && !kidBefore(e, ki, e.Content[ci].Order) {
			j := ci
			for j < len(e.Content) && !kidBefore(e, ki, e.Content[j].Order) {
				j++
			}
			content(e.Content[ci:j])
			ci = j
			continue
		}
		kid(e.Kids[ki])
		ki++
	}
}

// substituted applies e's /ActualText to the spans it declares, per ISO 32000-2 §14.9.4:
// the value replaces what the glyphs spell, because it is the producer saying what the
// content *is* where the drawing does not spell it.
//
// It is here, on the inline path, because that is the only place the corpus's /ActualText
// can be reached. All 4803 values across the 51 PDFs on disk are on a Span, and a Span is
// transparent — never a block — so every one arrives through gather or labelText and none
// through emitItem's Replacement field. Three distinct values account for all 4803, and
// each was wrong in output in its own way:
//
//   - 4695 declare "\n" over a drawn space, in TD>P>Span (4481), Sect>P>Span (192),
//     TH>P>Span (4), Document>P>Span (9) and Sect>H1>Span (9). A newline is the producer
//     saying "this is a line break", and substituting it is not a no-op even though the
//     sink flattens one to a space: it says so in the model rather than by coincidence.
//   - 92 declare " • " over a drawn U+25A0 BLACK SQUARE, all in LI>Lbl>Span in
//     Well-Tagged-PDF-WTPDF-1.0.pdf. Both glyphs are in listMarkers, so both strip and
//     the output is unchanged — the value is honoured rather than the square being read
//     as the label the producer disclaimed.
//   - 16 declare U+00AD SOFT HYPHEN over a drawn "-", in TD>P>Span across four ISO
//     documents. This is the one that changes text: a declared soft hyphen is
//     discretionary, so the word joins, and "di-gest" becomes "digest".
//
// The declaring element's own value covers its whole run of marked content, so a value
// spanning several spans replaces them all with one span. Style and box are the first
// span's: a substitution is a string and has neither, and dropping the box would cost the
// block its geometry. The spans are copied rather than edited, because index hands out
// pointers into the document and the recovery pass reads the same ones — editing here
// would rewrite the page text a caller asked to extract.
//
// The union stops at the first page, because a rectangle is only a position within one page's
// user space and a union across two is not a rectangle anywhere. An element's marked content
// can name two pages — 92 of the corpus's 4803 declaring elements list more than one
// reference, though 0 of them cross a page today — and unioning across the break would produce
// a box bounding two coordinate spaces at once while the span named a single page. Every rule
// that reads a box then reads a fiction, and doc.Span.Page would be the thing asserting it.
// Keeping the first page's spans and their box is the conservative answer: the text is the
// declared value either way, so what is lost is a box that was never meaningful, and the span
// still says which page the box it does carry belongs to.
//
// A value of "" is not a substitution. tag.Read leaves the field empty when the key is
// absent, so an empty string cannot be distinguished from no declaration at all, and
// treating it as one would delete the glyphs of every element that has none.
func substituted(e *tag.Elem, spans []*doc.Span) []*doc.Span {
	if e.ActualText == "" || len(spans) == 0 {
		return spans
	}
	sp := *spans[0]
	sp.Text = inlineText(e.ActualText)
	for _, s := range spans[1:] {
		if s.Page != sp.Page {
			continue
		}
		sp.Box = sp.Box.Union(s.Box)
	}
	return []*doc.Span{&sp}
}

// inlineText adapts a declared string to what a doc.Span holds: a run of inline text.
//
// A /ActualText value is a string in a dictionary and can say things a run of drawn glyphs
// cannot, so substituting it verbatim puts characters into the model that no extractor ever
// produces and that every sink downstream is entitled to assume are absent. Both cases in
// the corpus were visible in the output before this existed, and each is a rune whose only
// meaning is about line breaking — which is the one thing a stream of inline text does not
// have.
//
// A line break becomes a space. All 4695 of the corpus's "\n" declarations stand in for a
// drawn space, so this is what the producer meant in every measured case; it is also the
// rule doc.Block.Replacement already gets from markdown.oneLine, applied one layer earlier
// so that every sink agrees rather than each flattening its own. Left in, it splits inline
// text across lines: "**Technical Specification**" became "**Technical" and
// "Specification**" — two lines, and no longer bold, since a CommonMark emphasis run cannot
// span a line break. Inside a code block the break survived and looked like an improvement,
// restoring the line structure of ISO/TS 32004's ASN.1 listings, and it is not one: those
// are code *spans*, and pandoc -f gfm renders a blank line inside one as a paragraph break
// with the backticks left literal. Line structure in a code block is worth having and is a
// separate change — it needs the block to be fenced, which is doc.Block.Role's business and
// not a side effect of one dictionary key.
//
// A soft hyphen is dropped. U+00AD is a *discretionary* break: a hyphen to be drawn only if
// the line breaks there, and nothing otherwise. So a producer declaring one over a drawn "-"
// is saying the hyphen belongs to its own line breaking and not to the word — which is what
// each of the corpus's 16 says, and each document's own spelling agrees, "digest" appearing
// joined 30 times against this one break and "structure" 63. Dropping it joins the word,
// and keeping it emitted an invisible character in the middle of one: "di<U+00AD>gest" is
// worse than the "di-gest" it replaced, since a reader cannot see what went wrong.
//
// Per rune rather than per value, so "co<U+00AD>operate" joins the same way the 16 bare
// declarations do. Nothing on disk has that shape — all 16 values are the soft hyphen alone
// — and a rule that reads the value's runes cannot be surprised by one that isn't.
//
// Every consumer of a raw /ActualText goes through here, which is three: substituted, for a
// declaration over spans; emitItem's Replacement, for one on a block-level element; and
// title, for one on a heading that drew no glyphs. Only the first has a corpus population, so
// the other two are held by unit tests alone — and leaving either raw reinstates exactly this
// defect one layer up, because the sinks' substitute() prefers Replacement over the spans and
// clean() folds the break but not the soft hyphen, which is not whitespace.
func inlineText(s string) string {
	if !strings.ContainsAny(s, "\r\n\u00ad") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i, r := range s {
		switch r {
		case '\u00ad':
		case '\n':
			// A CRLF is one break, not two spaces.
			if i > 0 && s[i-1] == '\r' {
				continue
			}
			sb.WriteByte(' ')
		case '\r':
			sb.WriteByte(' ')
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// kidBefore reports whether the kid at index ki precedes /K position order. A kid with no
// recorded position is treated as last, so a hand-built Elem keeps content-then-kids.
//
// Bounded by both slices, not just KidAt. KidAt is an index into Kids, so a position past the
// end of Kids describes a kid that does not exist — and answering "before" for one sends
// inOrder into the branch that reads Kids[ki], which panics. tag.Read cannot build that state,
// since readKids appends to both slices in one statement, but an Elem assembled by hand can,
// and a reader of an exported type is not entitled to assume otherwise.
func kidBefore(e *tag.Elem, ki, order int) bool {
	return ki < len(e.Kids) && ki < len(e.KidAt) && e.KidAt[ki] < order
}

// gather collects e's spans together with those of its transparent descendants,
// and separates out the descendants that start blocks of their own.
//
// The gathered text is collected in /K order via inOrder, so an inline child's runes land
// where the file draws them and not after the parent's own — see inOrder for why that
// matters. The elements that start blocks of their own are still deferred rather than
// visited in place: they are handed back to the caller, which emits the block being built
// before descending into them, so a Figure inside a paragraph does not split it in two.
//
// wraps says the block being built holds its text in a wrapping paragraph — a list
// item, a table cell, or a code listing, per wrapsText — which makes a paragraph inside it
// transparent. lines says those absorbed paragraphs are lines rather than prose, per
// linesText, which is the only thing that separates a listing from a cell here.
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
func (b *builder) gather(e *tag.Elem, spans *[]*doc.Span, nested *[]*tag.Elem, pg *span, wraps, lines bool) {
	pg.add(e.Page)
	inOrder(e,
		func(refs []tag.MCRef) {
			*spans = append(*spans, substituted(e, b.index.take(refs))...)
			for _, r := range refs {
				pg.add(r.Page)
			}
		},
		func(k *tag.Elem) {
			if k.Role.IsHeading() {
				*nested = append(*nested, k)
				return
			}
			if r, ok := blockRole(k.Role); ok && !(wraps && r == doc.RoleParagraph) {
				*nested = append(*nested, k)
				return
			}
			// An absorbed paragraph in a lines block is a line, so the break before it is
			// restored here — the producer drew it as a structure boundary and there is no
			// glyph anywhere for it. Guarded on there being text already, so the first line
			// does not open the block with a blank one, and written before recursing so it
			// lands between two paragraphs rather than after the last.
			//
			// Per absorbed *block*, not per absorbed kid: 10 of the 109 descendants of the
			// corpus's Code elements are Span, one per styled run inside a line, and breaking
			// on those splits the line they style. MCID -1 keeps the fabricated span out of
			// the join — newIndex indexes only non-negative identifiers — so it can be neither
			// claimed by an element nor recovered as unplaced text.
			//
			// The len guard fires on every Code holding no content of its own, which is all 11
			// on disk. It would have to hold a second time for a first paragraph resolving to
			// no spans, and 0 of the 99 do; a break would be wrong there anyway, since an
			// empty first line is not a line to separate from.
			if lines && len(*spans) > 0 {
				if _, ok := blockRole(k.Role); ok {
					*spans = append(*spans, &doc.Span{Text: "\n", MCID: -1})
				}
			}
			b.gather(k, spans, nested, pg, wraps, lines)
		})
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
		Role:        role,
		Lang:        e.Lang,
		Alt:         e.Alt,
		Replacement: inlineText(e.ActualText),
	}
	if role == doc.RoleListItem {
		blk.Level = listDepth(e)
	}
	if role == doc.RoleTableCell {
		blk.Cell = cellAt(e, b.tableNum(e))
	}
	// The box bounds one page's spans, not every page's. A doc.Block has a Box and no page
	// range, so its rectangle is a position in one page's user space by construction — where a
	// doc.Section carries FirstPage and LastPage precisely because it can span a break. This
	// block routinely does too: 7 of the 30301 blocks the tagged path rebuilds for the corpus
	// hold spans from two pages, one paragraph or listing each continuing past a break, and
	// unioning those gives a rectangle that bounds two coordinate spaces and locates nothing in
	// either. Taking the first positioned span's page is the same answer substituted gives, for
	// the same reason: what is dropped is a box that was never meaningful, and the spans keep
	// their own Page so nothing that needs the later pages has lost them.
	//
	// Nothing on disk changes. No sink reads Block.Box — doc.Page.TextBounds and Coverage are
	// its only consumers and both walk the extractor's own per-page blocks, which are the pg > 0
	// case that cannot mix pages. So this corrects a field whose documented contract was false
	// for 7 blocks rather than an output, which is why it is stated here instead of pinned by a
	// corpus assertion on a number that would move with the corpus.
	blk.Spans = make([]doc.Span, 0, len(spans))
	box := 0
	for _, s := range spans {
		blk.Spans = append(blk.Spans, *s)
		if s.Page > 0 && box == 0 {
			box = s.Page
		}
		if s.Page == box {
			blk.Box = blk.Box.Union(s.Box)
		}
	}
	// The union, not one entry per span: this loop used to append every span's identifier
	// including the -1s, so an element listing four spans across two MCIDs recorded four
	// entries. The spans the two calls below insert carry -1 and so cannot change the set,
	// which is why this reads correctly here rather than after them.
	blk.SetMCIDs()
	spaceAtGaps(&blk)
	if linesText(role) {
		breakAtBaselines(&blk)
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
// That 124 is a label read that descends into the Lbl, and label() now reads the same way, so
// the two figures are about one population again. They were not: 24 of the 124 own their marker
// directly and the other 100 hold it in a Span kid, which the shallow read did not reach, and
// the agreement figure was therefore about what the producer declared where label() saw a
// quarter of it. That gap is what the descent closed.
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
					// it and no key would ever be looked up. Indexed anyway when it is a
					// line's leading indent, under the key of the span it is attached to;
					// see leadingIndent. Otherwise not indexed, and still unconsumed, so
					// the recovery pass keeps it.
					//
					// A copy carrying that key as its own MCID, not the span itself, for
					// two reasons. Indexing under a key is already the statement that this
					// span joins that marked content, so the join key it exposes has to
					// agree — and the geometric rules downstream read it: newLine skips
					// MCID < 0, so an indent that kept -1 would suppress the line break
					// both before and after itself, collapsing the very listing this
					// recovers. Marked consumed here rather than in take, because the
					// original is what unplaced walks and it is accounted for the moment
					// a copy of it is indexed; leaving it unconsumed would put the same
					// spaces in a section and in Unplaced both.
					if nx, ok := leadingIndent(blk, si); ok {
						adopted := *sp
						adopted.MCID = nx.MCID
						k := key{p.Number, nx.MCID}
						ix.spans[k] = append(ix.spans[k], &adopted)
						ix.consumed[sp] = true
					}
					continue
				}
				k := key{p.Number, sp.MCID}
				ix.spans[k] = append(ix.spans[k], sp)
			}
		}
	}
	return ix
}

// leadingIndent reports whether the span at i is a line's leading indent drawn outside
// marked content, and returns the tagged span it indents.
//
// A producer has two ways to indent a listing's line, and only one of them survives the
// rebuild on its own. PDF-Declarations.pdf's XML sample and Well-Tagged-PDF-WTPDF-1.0.pdf's
// both draw their nesting as real space glyphs; WTPDF puts most of them inside the line's
// own marked content, where take claims them with the rest of the text, and both producers
// also draw some as a separate run outside it. That run is what this finds. It is not
// inference: the spaces are on the page, with an advance each, and dropping them is the
// defect — 23 runs corpus-wide, 22 in PDF-Declarations' listing and 1 in WTPDF's, which is
// why one listing came out entirely flush-left and the other lost a single line's indent.
//
// Attached to the *following* span's key rather than tracked in document order, so take
// returns the indent immediately before the text it belongs to without the index having to
// model position. The consequence is that an indent whose line the tree never claims is
// never emitted either, which is the behaviour to want: the spaces are meaningless without
// the line.
//
// Three conditions, and each one is load-bearing against the 43 untagged whitespace spans
// on disk that are *not* indents — a dotted leader's trailing space, the space after a
// bullet glyph, a TOC entry's padding:
//
//   - Whitespace only, and by rune rather than by byte, for the reason gapSpace decodes:
//     a run of U+00A0 is an indent too.
//
//   - First on its baseline within the block, which is what "leading" means. This is what
//     rejects 31 of the 43, all of them mid-line. The other 12 are line-start and are
//     rejected by attachment below: a bullet's space, whose glyph an element claims while
//     the space beside it stays an artifact.
//
//   - Attached to the next span, meaning the two runs are touching. A separate run of spaces
//     the producer drew as positioning stands *away* from the text after it; an indent runs
//     right up to it. Measured in space advances, the gap is ≤ 0.0532 for all 23 indents and
//     ≥ 0.364 for every other run that has a same-line successor at all — a 6.8× empty band,
//     so this is an order-of-magnitude test like spaceAtGaps' and not a tuned constant.
//
//     It was first written as the negation of that rule's own space test, sharing SpaceFrac
//     so the two could not disagree about one gap, and that is the half of the rationale
//     this rule no longer claims. The composition was never the reason the constant was
//     right: raising SpaceFrac to 0.40 on its own measurement moved 8 spaces beside a
//     bullet glyph from positioning to indentation, because 0.364 is under 0.40 and was
//     over 0.30. What the two rules share is the population and the units, not the number —
//     see geom.IndentAttachFrac. The adopted copy does reach gapSpace with a tagged span's
//     MCID, so what keeps the two from ever calling one gap both attached and a word boundary
//     is that the copy holds whitespace and gapSpace declines on that before reading any
//     geometry (:534) — not the ordering of the two constants, and not the MCID guard.
func leadingIndent(blk *doc.Block, i int) (*doc.Span, bool) {
	sp := &blk.Spans[i]
	if sp.Text == "" || strings.TrimFunc(sp.Text, unicode.IsSpace) != "" {
		return nil, false
	}
	if i+1 >= len(blk.Spans) {
		return nil, false
	}
	nx := &blk.Spans[i+1]
	if nx.MCID < 0 {
		return nil, false
	}
	if !sameLine(sp, nx) {
		return nil, false
	}
	for k := 0; k < i; k++ {
		if sameLine(&blk.Spans[k], sp) {
			return nil, false
		}
	}
	tol := geom.DefaultTolerance
	if math.Abs(nx.Box.X0-sp.Box.X1) > tol.IndentAttachFrac*spaceAdvance(nx.Style.Size) {
		return nil, false
	}
	return nx, true
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
				Role:        blk.Role,
				Level:       blk.Level,
				Lang:        blk.Lang,
				Alt:         blk.Alt,
				Replacement: blk.Replacement,
			}
			for si := range blk.Spans {
				sp := &blk.Spans[si]
				if ix.consumed[sp] {
					continue
				}
				keep.Spans = append(keep.Spans, *sp)
				keep.Box = keep.Box.Union(sp.Box)
			}
			// Filtered the -1s but never deduplicated, so a block keeping several
			// unclaimed spans from one marked-content sequence recorded it once per
			// span.
			keep.SetMCIDs()
			if keep.IsEmpty() {
				continue
			}
			// An Alt or Replacement on a block whose spans were all claimed would
			// otherwise reappear here as content with no text, since IsEmpty treats
			// both as content.
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

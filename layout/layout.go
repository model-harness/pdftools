// Package layout infers roles from geometry and typography, for the documents that
// declare none.
//
// Most PDFs are untagged, so for most PDFs there is no structure tree to read a
// heading rank out of. extract deliberately stops short of guessing: it marks every
// non-artifact block RoleParagraph and leaves rank to whichever package has the most
// evidence. Where a tree exists that is sectionize. Where none does it is here.
//
// # Why not "bold and larger means heading"
//
// The obvious rule is falsified by the corpus, and this package is shaped by how it
// fails. Measured over the untagged fixtures:
//
//   - pymupdf/v110-changes.pdf sets 8.04pt *bold* as 48.8% of its characters, against
//     49.4% at 9.96pt — so the body wins on size by half a point of margin while bold is
//     not emphasis in that document at all, and a weight-implies-heading rule marks half
//     of it. (Measured on the character tally rather than reached through this package:
//     the file's headings fuse into the paragraphs after them, so nothing there is a
//     candidate — see uniformStyle.)
//   - pymupdf/2201.00069.pdf (arXiv) and adobe-samples/autotagPDFInput.pdf set their
//     headings *plain* — 11.96pt and 28pt respectively — and use bold only as inline
//     emphasis, 0.3% and 0.5% of characters. A rule that requires bold finds nothing.
//   - pymupdf/dotted-gridlines.pdf has a table row at body size in bold, 41
//     characters of it. It is typographically indistinguishable from a real heading.
//   - testdata/reference/headings.pdf sets its third level at *body size* in bold, so
//     a rule that requires a larger size loses the deepest level of the one fixture
//     written to test heading depth.
//
// So size and weight together cannot separate a heading from a bold table row, and
// neither can the space above it: on dotted-gridlines the gap above that row is 1.68
// times the body size, inside the 1.60–1.96 range the reference headings occupy.
//
// # What the document states rather than implies
//
// A numbered heading carries its own rank. "4.2.1 Nested subclause" is level three
// because the document says so, not because 9.96pt happens to sit third in a size
// ladder. That inverts the usual arrangement: typography is the *gate* that admits a
// block as a candidate, and the section number is what assigns the level. An annex
// number does the same job with a letter for its first component — "B.2.3" is level
// three — which annexLevel reads and numberedLevel cannot.
//
// Ranking by position in the size ladder was measured and rejected. pymupdf's
// mupdf_explored.pdf has five distinct above-body sizes — 24.79 chapter titles, 20.66
// "Chapter 1" labels, 17.22 the book title, 14.35 sections, 11.96 the author line and
// part entries — of which only some are heading levels at all. Ranking by ladder
// position disagreed with the document's own numbering on 296 of 296 numbered
// headings, because it counts rungs that are not levels. Numbering is not a heuristic
// about the document; it is the document's statement, so it wins.
//
// The cost is that unnumbered headings stay paragraphs. That is deliberate for now:
// the fixtures show no signal that admits "Preface" without also admitting a bold
// table row, and a rule that promotes one and not the other would be tuned to a
// document rather than derived from one. Recording the limit is better than guessing
// past it — DESIGN.md §10 carries it as measured debt.
//
// Two candidate signals for that limit have since been measured and rejected, which is
// worth stating so neither is proposed again as though it were untried. *Recurrence* —
// promote a style that heads a block on many pages, not on one — was ADR 0008's own
// suggestion, and the corpus falsifies it in both directions: of the 151 unnumbered
// above-body candidates on the untagged path, 9 occur once and include real titles
// ("MARKET SUMMARY & PLAN", chinese-tables.pdf's "第七章 企业资信状况"), while the
// repeated ones include mupdf_explored.pdf's "Robin Watts" and "September 5, 2022" at 9
// occurrences over 7 pages. Rank and repetition are independent of each other here. A
// *size ratio* threshold fails the same way: joined to what producers declare, the 287
// unnumbered above-body candidates on the tagged corpus peak at 73.2% precision around
// 1.17 and fall to 6% by 1.63, because a title page's 3.4x masthead is not a heading and
// a 1.08x subclause is. There is no gap in that distribution to put a threshold in.
package layout

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
)

// Options configures inference.
type Options struct {
	// ListStep is how far a list item's left edge must sit right of the tier above it
	// to count as nested, as a multiple of the run's type size. Zero means the default;
	// a negative value disables nesting, leaving every item at level 1.
	ListStep float64

	// MaxHeading bounds a heading's length in runes. A heading is short; a numbered
	// paragraph is not, and specifications are full of them ("4.2.1 The value shall
	// be…"). Without a bound, every numbered clause body in a standard becomes a
	// heading. Measured against the fixtures: the longest true heading found is 44
	// runes, so 80 leaves room without admitting prose. Zero means the default.
	MaxHeading int

	// MaxLevel bounds the depth a section number may assign. Markdown has six
	// heading levels and deeper numbering exists — ISO clause 7.11.4.2.1 is five —
	// so the cap is where the dialect's, not the document's. Zero means the default.
	MaxLevel int
}

// DefaultOptions is inference as the CLI runs it.
//
// ListStep of 1.0 sits in the middle of an empty band rather than at a tuned point.
// Measured over every PDF on disk, a marker run contains only eight distinct left-edge
// gaps in total: six at 0.011 type sizes, one at 0.241, and one at 2.403. The first
// seven are the same tier — float noise in a shared margin, and ISO 32000-2's
// PDFDocEncoding table where two adjacent rows open with an em dash and an en dash of
// different widths — while the last is reference/lists.pdf's genuine `itemize` nesting.
// Anything from roughly 0.3 to 2.4 yields the same result, so 1.0 is a statement that
// nesting indents by about a character, not a threshold fitted to a trough.
var DefaultOptions = Options{MaxHeading: 80, MaxLevel: 6, ListStep: 1.0}

func (o Options) maxHeading() int {
	if o.MaxHeading <= 0 {
		return DefaultOptions.MaxHeading
	}
	return o.MaxHeading
}

func (o Options) maxLevel() int {
	if o.MaxLevel <= 0 {
		return DefaultOptions.MaxLevel
	}
	return o.MaxLevel
}

// listStep returns the nesting step, and whether nesting is enabled at all. Unlike the
// two above, zero and negative differ here: zero is "unset, use the default" and
// negative is "flatten", which is a usable setting for a caller that wants list markers
// removed without trusting the geometry that assigns depth.
func (o Options) listStep() (float64, bool) {
	if o.ListStep == 0 {
		return DefaultOptions.ListStep, true
	}
	if o.ListStep < 0 {
		return 0, false
	}
	return o.ListStep, true
}

// Stats reports what inference did. The failure modes here are quiet — a run that
// promotes half a document has not errored, and neither has one that promotes
// nothing — so the numbers are the only way to see it.
type Stats struct {
	// BodySize is the size, in points, of the dominant text cluster.
	BodySize float64
	// BodyBold reports whether that dominant cluster is bold — that is, whether
	// weight carries any heading signal in this document. False on every fixture in
	// the corpus; v110-changes.pdf comes closest and still misses, its 8.04pt bold
	// holding 48.8% of characters against a 9.96pt plain body that wins on size.
	BodyBold bool
	// Candidates counts blocks that passed the typographic gate.
	Candidates int
	// Headings counts blocks promoted to RoleHeading.
	Headings int
}

// Headings promotes untagged blocks to RoleHeading in place, and reports what it did.
//
// In place rather than returning a new document because the caller already owns one
// and the change is per-block: a Role and a Level, on blocks that extract left as
// paragraphs. Building a doc.Outline here would duplicate sectionize, whose level
// stack already turns a linear (level, title, content) sequence into a tree — that is
// the next step on this path, not this one.
//
// A document with no text, or one whose blocks are all artifacts, is left alone and
// reports zero. That is not an error: a page holding one image has no headings.
func Headings(d *doc.Document, opt Options) Stats {
	if d == nil {
		return Stats{}
	}
	size, bold, ok := bodyCluster(d)
	if !ok {
		return Stats{}
	}
	st := Stats{BodySize: size, BodyBold: bold}

	for pi := range d.Pages {
		blocks := d.Pages[pi].Blocks
		for bi := range blocks {
			b := &blocks[bi]
			if b.Role != doc.RoleParagraph {
				continue
			}
			style, uniform := uniformStyle(b)
			if !uniform {
				// A block mixing sizes or weights is a heading fused to the paragraph
				// after it, or prose with emphasis in it. Neither is a heading, and
				// splitting the fused case is block segmentation rather than
				// classification. extract's continues() now breaks on a dominant-size
				// ratio as well as on the vertical step, which is what recovered the
				// headings of autotagPDFInput.pdf and v110-changes.pdf (ADR 0009); a
				// same-size fused block is the case that remains, per DESIGN.md §10.
				continue
			}
			if !distinct(style, size, bold) {
				continue
			}
			text := strings.TrimSpace(b.Text())
			if len([]rune(text)) > opt.maxHeading() {
				continue
			}
			level, ok := numberedLevel(text)
			if !ok {
				level, ok = annexLevel(text)
			}
			if !ok {
				st.Candidates++
				continue
			}
			st.Candidates++
			if level > opt.maxLevel() {
				level = opt.maxLevel()
			}
			b.Role = doc.RoleHeading
			b.Level = level
			st.Headings++
		}
	}
	return st
}

// Lists promotes untagged blocks to RoleListItem in place, and reports what it did.
//
// A block is a list item when it opens with a marker glyph as its own token. That is a
// weaker signal than the section number Headings runs on, and the reason it is usable
// anyway is that a bullet is not a character anyone sets in prose: measured over the
// corpus, of 1442 blocks opening with one of doc's marker glyphs, reading every ambiguous case
// leaves 5 that are not list items — all of them rows of ISO 32000-2's glyph tables in
// Annex A and D, where an em dash or en dash *is* the row's subject ("— 132 0x84 0204
// U+2014 EM DASH"). At 5 in 1442 the population is 288:1 in favour of promoting, which is
// the inverse of the ratio that made DESIGN.md's lost-space defect not worth a rule.
//
// # The marker is removed, here and not in the sink
//
// Promoting a block to RoleListItem is the statement that its marker is structure
// rather than text, so the marker leaves the spans with it — into Block.Marker, which is
// where a sink that can render a label finds it. Leaving it in the text would mean every
// sink re-deriving the glyph allowlist to know which leading rune was the marker — the
// same split doc.Block warns against for space inference, half the policy in the model
// and half in the producer. It would also double the marker on the one sink that exists:
// markdown writes its own "- ".
//
// Block.StripMarker does it, in doc rather than here, because this is no longer the only
// producer of a list item's marker: sectionize reads one the structure tree declares, and
// two copies of the allowlist is the same split one package further out. This is still
// the only place in *this* package that edits a block's text rather than its role.
//
// # Levels, and what was rejected
//
// Depth comes from the left edge, ranked within a maximal run of consecutive marker
// blocks. Ranking within the run rather than the page is what keeps two unrelated lists
// in different columns from being read as one nested list.
//
// A minimum run length was measured and rejected. Requiring two consecutive marker
// blocks looks like the obvious guard against a stray table row, and it costs far too
// much: it drops 136 promotions across the corpus, and reading them shows they are
// overwhelmingly genuine — single-item lists, and multi-item lists that extract fused
// into one block ("■ machine-readable text presented in a declared language; ■
// appropriate…"). It would have rejected 136 to catch about 3.
//
// The fusion in that second group is a segmentation defect in extract rather than
// anything a role rule should paper over, and it was investigated rather than left as a
// suspicion: 98 line pairs across 6 files, of which exactly one reaches the emitted
// output, because every other affected file is tagged and sectionize splits its items
// from the structure tree before a sink sees them. Both fixes that suggest themselves are
// dead — the vertical step before a bullet line (1.220 to 1.486 line heights) overlaps
// ordinary wraps (1.100 to 1.500) completely, the bullet sits flush with the block margin
// rather than outdented from it at the 25th through 90th percentile, and breaking a block
// where the marked-content identifier changes would cost 6911 splits to buy 8. So the run
// minimum stays rejected on the 136-to-3 arithmetic and not pending a segmentation fix.
// DESIGN.md §10 carries the numbers and the larger defect the measurement did find, which
// is on the tagged path.
func Lists(d *doc.Document, opt Options) ListStats {
	if d == nil {
		return ListStats{}
	}
	step, nest := opt.listStep()
	var st ListStats

	for pi := range d.Pages {
		blocks := d.Pages[pi].Blocks
		for i := 0; i < len(blocks); {
			if !isListItem(&blocks[i]) {
				i++
				continue
			}
			// The maximal run of consecutive marker blocks, which is the scope a left
			// edge is ranked within.
			j := i
			for j < len(blocks) && isListItem(&blocks[j]) {
				j++
			}
			run := blocks[i:j]
			st.Runs++

			tiers := listTiers(run, step, nest)
			for k := range run {
				b := &run[k]
				// Exact, with no tolerance. A tier value is a copy of some block's
				// own Box.X0 with no arithmetic applied — listTiers sorts and
				// selects, it does not compute — so a block either defined the tier
				// it is being compared against or sits a real distance from it.
				// Review asked whether an epsilon was needed here anyway; measured
				// over the corpus's 1447 tier comparisons, 1404 are exact equality
				// and 0 fall within 0.01pt below a tier, so the 0.01 tolerance this
				// carried was guarding nothing. Removing it changes no test and no
				// fixture.
				level := 1
				for r, x := range tiers {
					if b.Box.X0 >= x {
						level = r + 1
					}
				}
				if level > opt.maxLevel() {
					level = opt.maxLevel()
				}
				b.Role = doc.RoleListItem
				b.Level = level
				b.StripMarker()
				st.Items++
				if level > st.MaxLevel {
					st.MaxLevel = level
				}
			}
			i = j
		}
	}
	return st
}

// OrderedLists promotes a run of consecutively numbered paragraphs to list items.
//
// # Why this needs a run where Lists does not
//
// ADR 0011 rejected ordered lists, and its objection was exact: a numbered item is a
// paragraph opening with a number, which is also what a numbered heading is and what a table
// row is, and nothing distinguishes them. That is true of one block, which is all Lists ever
// looks at — a bullet glyph is evidence by itself, so a single "• item" is promotable. A
// number carries no such evidence, so the evidence has to come from somewhere else, and the
// only place left is the neighbours: "1." then "2." then "3.", consecutive blocks at one left
// edge, incrementing by one. That is a claim about a sequence, and no heading or table row
// makes it accidentally.
//
// This is why the run minimum Lists rejected on a 136-to-3 count is *required* here. The two
// rules are asymmetric because their evidence is: Lists pays 136 genuine single-item lists to
// catch 3 stray rows and refuses; here there is no promotion at all without the run, because
// without it there is nothing to separate an item from prose that starts with a number.
//
// # What the delimiter separates, and what the corpus contains
//
// The label must carry a delimiter — "1." or "[1]", never a bare "1" — and doc.OrderedLabel
// requires it. That is what keeps a numbered heading out: "7.4 Filters" and "1 Scope" are a
// clause number followed by space. A dotted number cannot form a run at all, since it has no
// single value to increment, so ADR 0008's rule and this one cannot collide over the same
// block.
//
// Measured over all 50 documents on disk, the rule promotes 70 runs of 260 items, and reading
// every one shows what they are: algorithm steps ("a) Accumulate a sequence…"), bibliography
// entries ("[1] ISO/IEC 8825-1…"), and RFC reference lists. Forms are "a)" 174, "n." 43,
// "[n]" 21, "n)" 17, "a." 5; lengths 2 to 11, with 25 runs of exactly 2. 4 runs are tables of
// contents ("1. Introduction   1"), which are the only false positives on disk — and all 4
// are in *tagged* documents, where inferRoles never runs because sectionize read the structure
// tree instead. On the untagged path this pass actually serves, the effect is 5 runs of 25
// items, all in mupdf_explored.pdf, all genuine. A TOC guard was measured anyway and would
// catch 3 of the 4 at zero cost to a genuine item; it is not here, because a guard that cannot
// fire on any file this code runs on is untested code, and the honest place for the
// measurement is this comment.
//
// The strongest check available is a producer's own declaration, the same one ADR 0011's glyph
// allowlist rests on. Of the tagged list items that declare a /Lbl holding an ordered label,
// doc.OrderedLabel reads the same form off the item's own text in 16 of 16, disagreeing in 0.
//
// # What is not attempted
//
// Nesting. Lists ranks depth by left edge within a run; here every promoted item is level 1,
// because an indented ordered sub-list is not on disk and a tier rule fitted to no positive
// case is fitted to noise — ADR 0011's own reason for stating ListStep rather than tuning it.
//
// That is also why opt is accepted and not read, which a reviewer should see as deliberate
// rather than forgotten. Options carries three settings and none applies: MaxHeading and
// MaxLevel bound a heading, and ListStep is the *nesting* step, which needs tiers this pass
// does not produce — its negative "flatten" case is already what every item gets. sameEdge's
// tolerance is not ListStep under another name: one asks how far apart two edges must be to
// mean different depths, the other how close they must be to mean the same margin. The
// parameter stays for the signature the other three passes share, so inferRoles calls them
// uniformly; adding a knob for the tolerance instead would be configurability no caller asked
// for and no measurement wants, since the value sits in an empty band.
//
// A run that crosses a page break. The loop is per page, so "a)…d)" ending one page and
// "e)…g)" opening the next promote as two runs rather than one. That is a real limitation and
// not a preference: it was measured. 5 such continuations exist in the corpus — all of them in
// *tagged* documents, so 0 reach this pass, and there is nothing on the untagged path to fix.
// It also costs a sink nothing today, because each item carries its own label and the markdown
// sink emits every label rather than counting; a sink that renumbered from the first item would
// need this closed first. Joining across the break would mean carrying run state between
// pages, which is state no other pass in this package keeps, bought for no measured case.
func OrderedLists(d *doc.Document, opt Options) ListStats {
	if d == nil {
		return ListStats{}
	}
	var st ListStats

	for pi := range d.Pages {
		blocks := d.Pages[pi].Blocks
		for i := 0; i < len(blocks); {
			lbl, val := orderedItem(&blocks[i])
			if lbl == "" {
				i++
				continue
			}
			// The maximal run of consecutive blocks whose labels share a form, sit at one
			// left edge, and increment by one. Every condition is load-bearing: without the
			// form check "1." and "2)" chain, without the edge check a numbered paragraph
			// after a list joins it, and without the increment check any two numbered
			// blocks do.
			j, want := i+1, val+1
			for j < len(blocks) {
				l2, v2 := orderedItem(&blocks[j])
				if l2 == "" || v2 != want || !sameForm(lbl, l2) ||
					!sameEdge(blocks[i].Box.X0, blocks[j].Box.X0) {
					break
				}
				j++
				want++
			}
			if j-i < 2 {
				// One numbered paragraph is not evidence of a list. This is the whole
				// difference from Lists, and the reason ADR 0011's objection does not
				// apply to a run.
				i++
				continue
			}
			st.Runs++
			for k := i; k < j; k++ {
				b := &blocks[k]
				b.Role = doc.RoleListItem
				b.Level = 1
				b.StripOrderedLabel()
				st.Items++
			}
			if st.MaxLevel < 1 {
				st.MaxLevel = 1
			}
			i = j
		}
	}
	return st
}

// orderedItem returns an unpromoted paragraph's ordered label and its sequence value.
//
// The paragraph gate is Headings' and Lists' precedence, arriving here as a consequence
// rather than a rule: inferRoles runs those first, so a block this sees as a paragraph is one
// they both declined. A heading that ADR 0008 promoted is therefore never reconsidered, which
// is the collision that ADR 0011 warned about, prevented by ordering rather than by argument.
func orderedItem(b *doc.Block) (string, int) {
	if b.Role != doc.RoleParagraph {
		return "", 0
	}
	return doc.OrderedLabel(b.Text())
}

// sameForm reports whether two labels are the same shape — same delimiters, same kind of
// sequence value.
//
// Compared structurally rather than by remembering which orderedForms entry matched, because
// the labels are what the blocks carry and a form index would be a second representation of
// the same fact. "1." and "10." are the same form; "1." and "1)" are not, and neither are
// "1." and "a.".
func sameForm(a, b string) bool {
	da, db := digits(a), digits(b)
	if da != db {
		return false
	}
	return strip(a) == strip(b)
}

// digits reports whether a label's sequence value is numeric rather than alphabetic.
func digits(lbl string) bool {
	for _, r := range lbl {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// strip removes a label's sequence value, leaving its delimiters: "10." and "1." both give
// ".", "[7]" gives "[]".
func strip(lbl string) string {
	var sb strings.Builder
	for _, r := range lbl {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// sameEdge reports whether two blocks start at the same left edge.
//
// A tolerance rather than exact equality, unlike listTiers' comparison: those values are
// copies of one block's own Box.X0 and this compares two different blocks' measured extents,
// which differ by the glyph each happens to start with.
//
// Half a point sits in an empty band, which is the only reason to state a number rather than
// measure one. Censused over the corpus, the 192 adjacent block pairs that agree on label form
// and increment by one have left-edge gaps of: 180 at exactly 0, 10 below 0.1pt, then nothing
// at all until 2.18pt, and one at 33pt. So every value in [0.1, 2.1) separates the two
// populations identically and 0.5 is the middle of the gap — the same shape as ADR 0011's
// ListStep, chosen from an empty band rather than fitted to a boundary case.
func sameEdge(a, b float64) bool {
	d := a - b
	return d > -0.5 && d < 0.5
}

// ListStats reports what Lists did. Quiet failure is the same risk as for Headings: a
// run that promotes nothing has not errored, and neither has one that promotes a table.
type ListStats struct {
	// Runs counts maximal sequences of consecutive list items.
	Runs int
	// Items counts blocks promoted to RoleListItem.
	Items int
	// MaxLevel is the deepest nesting assigned. One on every corpus fixture except
	// reference/lists.pdf, which is the only document on disk with a nested list.
	MaxLevel int
}

// isListItem reports whether a block is an unpromoted paragraph opening with a marker.
//
// Reading Text() and not Alt is why StripMarker can edit the spans alone. A block with
// Alt set emits that instead of its spans, so stripping a marker out of spans nothing
// reads would promote the block and leave the marker in the output. It cannot happen
// today: extract never sets Alt, and doctags sets it only on RoleFigure, which the
// paragraph gate here already excludes. It is worth stating because the two facts live in
// other packages — a producer that starts setting Alt on a paragraph breaks this, and the
// symptom would be a "- • item" in the output rather than a test failure.
func isListItem(b *doc.Block) bool {
	return b.Role == doc.RoleParagraph && doc.ListMarker(b.Text()) != 0
}

// listTiers returns the run's distinct left edges, ascending, each at least step type
// sizes right of the one before it.
//
// The type size is the run's largest, and it is a denominator at all for the same reason
// every threshold in geom is: a nested list in 7pt footnote type indents by less than one
// in 11pt body type, and a step in points would read the first as flat.
//
// Largest rather than smallest or dominant is a choice with no consequence on anything
// measured. 52 of the corpus's 524 marker runs do mix sizes, by up to 2.5x — a producer
// that sets nested items smaller, which is the shape that could suppress a genuine
// indent, since a 14pt-derived threshold is wider than a 9pt one. Ranking those 52 runs
// by their smallest size instead of their largest changes the tier count on 0 of them.
// Largest is kept because it is the conservative end: it under-reports nesting rather
// than inventing it, and inventing a level is the error that reaches the output.
//
// A run whose blocks carry no size at all yields one tier, so everything in it is level
// one. That is the honest answer rather than a fallback: with no size there is no scale
// to judge an indent against.
func listTiers(run []doc.Block, step float64, nest bool) []float64 {
	xs := make([]float64, 0, len(run))
	size := 0.0
	for i := range run {
		xs = append(xs, run[i].Box.X0)
		for _, sp := range run[i].Spans {
			if sp.Style.Size > size {
				size = sp.Style.Size
			}
		}
	}
	sort.Float64s(xs)
	if !nest || size <= 0 {
		return xs[:1]
	}

	tiers := xs[:1:1]
	for _, x := range xs[1:] {
		if x-tiers[len(tiers)-1] >= step*size {
			tiers = append(tiers, x)
		}
	}
	return tiers
}

// bodyCluster returns the size and weight of the dominant text cluster, by character
// count.
//
// Characters, not blocks: a document's body is what most of its text is set in, and
// counting blocks would let a page of one-line table rows outvote the prose. Size
// alone keys the tally, with the weight of the winning size reported alongside,
// because the two questions are different — "what size is the body" decides which
// blocks are larger than it, while "is the body bold" only says whether weight
// carries any signal in this document at all.
//
// Ties break toward the smaller size, so a document split evenly between two sizes
// treats the smaller as body and the larger as candidates rather than the reverse.
// Ties are otherwise arbitrary, and an arbitrary body size makes the whole result
// arbitrary with it.
func bodyCluster(d *doc.Document) (size float64, bold bool, ok bool) {
	type cluster struct {
		size float64
		bold bool
	}
	bySize := map[float64]int{}
	byCluster := map[cluster]int{}

	for pi := range d.Pages {
		for _, b := range d.Pages[pi].Blocks {
			if b.Role == doc.RoleArtifact {
				continue
			}
			for _, sp := range b.Spans {
				n := len([]rune(sp.Text))
				if n == 0 {
					continue
				}
				sz := quantize(sp.Style.Size)
				if sz <= 0 {
					continue
				}
				bySize[sz] += n
				byCluster[cluster{sz, sp.Style.Bold}] += n
			}
		}
	}
	if len(bySize) == 0 {
		return 0, false, false
	}

	best, bestN := 0.0, -1
	for sz, n := range bySize {
		if n > bestN || (n == bestN && sz < best) {
			best, bestN = sz, n
		}
	}
	return best, byCluster[cluster{best, true}] > byCluster[cluster{best, false}], true
}

// distinct reports whether a style stands out from the body enough to be considered.
//
// Larger than the body, or bold where the body is not. Both halves are needed and
// neither is sufficient: requiring a larger size loses headings.pdf's third level,
// which is body size in bold, and requiring bold loses arXiv and Adobe headings,
// which are plain.
//
// The second clause degrades to nothing where the body is already bold, which is the
// point: v110-changes.pdf sets 8.04pt bold as 48.8% of its characters, and a rule that
// read weight as a heading signal there would promote its body text. No fixture
// exercises that degradation end to end — v110's own headings fuse into the paragraphs
// below them, so nothing in it reaches this function at all — so it is pinned by
// TestHeadingsBodyBoldCarriesNoSignal rather than by a file.
func distinct(s doc.Style, bodySize float64, bodyBold bool) bool {
	if quantize(s.Size) > bodySize {
		return true
	}
	return s.Bold && !bodyBold && quantize(s.Size) == bodySize
}

// uniformStyle reports the block's style when every span shares one size and weight.
//
// A heading is set in a single face. The check is what keeps a paragraph with a bold
// lead-in from being read as a heading, and it is also what makes the fused-block
// case visible rather than silently misclassified.
func uniformStyle(b *doc.Block) (doc.Style, bool) {
	var first doc.Style
	seen := false
	for _, sp := range b.Spans {
		if len(sp.Text) == 0 {
			continue
		}
		if !seen {
			first, seen = sp.Style, true
			continue
		}
		if quantize(sp.Style.Size) != quantize(first.Size) || sp.Style.Bold != first.Bold {
			return doc.Style{}, false
		}
	}
	return first, seen
}

// numberedLevel returns the heading level a leading section number declares.
//
// "4.2.1 Nested subclause" is level three. The separator after the number must be
// whitespace, so a decimal quantity is not a heading: "3.14 is pi" would otherwise
// read as level two, and a table of measurements would read as an outline. A trailing
// dot on the number is allowed ("4.2.1. Title"), which is the other common style.
//
// Deliberately not matched: roman ("IV.") and parenthesised ("(a)") schemes, and a
// *bare* annex letter ("A Vocabulary Pruning"). "A" is also a word, and admitting a
// single letter followed by a space means admitting every line that starts with one.
// Lifting that needs a document-level pass that sees the sequence — an A.1 after an A is
// evidence where a lone A is not — which is more than this closes.
//
// The *dotted* lettered form is matched, by annexLevel rather than here, and the split is
// the point: "A.1" cannot be a sentence, so it needed none of the sequence evidence the
// bare letter does. See annexLevel for the measurement.
func numberedLevel(s string) (int, bool) {
	depth, digits, i := 0, 0, 0
scan:
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			if digits == 0 {
				// ".2" or "4..2": not a section number.
				return 0, false
			}
			depth++
			digits = 0
		default:
			break scan
		}
	}
	// Each dot closed the component before it, so the dots alone count every component
	// of "4.2.1." — the trailing-dot style needs nothing added. A number not ending in
	// a dot has one component the dots did not count.
	if digits > 0 {
		depth++
	}
	if depth == 0 {
		// No leading digits at all.
		return 0, false
	}
	// Decoded rather than read as a byte: producers separate a clause number from its
	// title with a non-breaking space routinely, and U+00A0 is two bytes in UTF-8, so
	// rune(s[i]) would test 0xC2 and reject every one of them. unicode.IsSpace covers
	// U+00A0 itself.
	//
	// The breadth of unicode.IsSpace is deliberate and is load-bearing on a *tab*: 18
	// clause headings on disk separate the number from the title with one, all in the ISO
	// TS documents ("3\t Terms and Definitions" in 32001 through 32005), and a rule that
	// insisted on U+0020 or U+00A0 would lose every one of them. A newline would be
	// accepted too and is the one shape here with no occurrence on disk — 0 spans of 50
	// documents contain one, extract's line assembly being what makes that true — so it is
	// admitted by the same call rather than by its own evidence.
	r, _ := utf8.DecodeRuneInString(s[i:])
	if !unicode.IsSpace(r) {
		// Something other than whitespace follows the number: "4.2.1:" or "1st" or
		// "3.14159". A heading's number is followed by its title.
		//
		// A number with *nothing* after it — a folio or a table cell — lands here too,
		// and needs no separate length test: DecodeRuneInString of the empty tail returns
		// RuneError, which is not a space. An explicit i >= len(s) above was unreachable,
		// which mutation testing is what found.
		return 0, false
	}
	return depth, true
}

// annexLevel returns the heading level a leading annex number declares: "A.1 Licensing"
// is level two, "B.2.3 CMS MAC validation" is level three.
//
// This is numberedLevel's rule with a letter where the first component's digits would be,
// and it exists because the letter form is not a variant a producer chooses freely — it is
// how both ISO and LaTeX number an annex, so it is where a document's *appendices* live and
// nowhere else. numberedLevel cannot be widened to cover it: it counts components, and a
// letter is not a component it can count.
//
// # The bare letter stays out, which is the whole reason this is safe
//
// numberedLevel's comment rejected lettered schemes outright, on the grounds that "A" is
// also a word and admitting a single letter followed by a space means admitting every line
// that starts with one. That objection is exactly right about the *bare* form and says
// nothing about the dotted one, which is why this closes half of what that comment
// deferred and leaves the other half open. Measured against what producers declare, the
// two halves are not close:
//
//   - Dotted ("A.1", "B.2.3", "J.3.7", "F.3.11"): of 112 such blocks that pass the
//     typographic gate on the tagged corpus, the structure tree calls 112 a heading and 0
//     not. There are no false positives to trade away.
//   - Bare ("A Vocabulary Pruning"): 0 of 1 declared, and the one is
//     PDF-Declarations.pdf's cover line "A use of ISO 32000". A sentence.
//
// So the dot is load-bearing twice over. It is what separates the annex scheme from an
// English sentence, and it is what keeps "A4 paper size" out — the digits must follow a
// dot, not the letter, so a letter-digit designation never parses. The corpus does not
// separate the two forms (requiring the dot admits the same 112), so the requirement rests
// on that construction rather than on a measurement, which is the honest way round: a
// guard that no file exercises is still right when it excludes a shape by shape.
//
// # Levels agree with the producer more often than the decimal rule does
//
// The component count is the level, so "A.1" is two rather than one — the annex letter is
// the level-one clause the numbering hangs off, whether or not the document sets a block
// for it. Cross-checked against the declared H1..H6 rank on the tagged corpus, that agrees
// 107 times and disagrees 5, all 5 in Well-Tagged-PDF-WTPDF-1.0.pdf, which tags its
// appendices one level deeper than it numbers them. The shipped decimal rule scores 931
// agree / 88 disagree over the same join, and admits 10 blocks the producer does not call
// a heading at all, so this is the better-behaved of the two rules and not a relaxation of
// the standard the package already holds.
//
// # What it does on the path it serves
//
// Untagged files are what this pass runs on, and there the effect is 10 promotions: 5 in
// mupdf_explored.pdf's Appendix A (A.1 through A.3 and two A.1.x) and 5 in the arXiv
// paper's appendices (C.1 through C.4, D.1). The 7 bare candidates beside them — "A
// Vocabulary Pruning", "I The MuPDF C API 5" and the other part entries — stay paragraphs,
// which is the limit DESIGN.md §10 still carries.
func annexLevel(s string) (int, bool) {
	r, n := utf8.DecodeRuneInString(s)
	if r < 'A' || r > 'Z' {
		return 0, false
	}
	i := n
	if i >= len(s) || s[i] != '.' {
		// A bare letter, or "A4". Both are rejected above by construction rather than
		// by measurement; see the comment.
		return 0, false
	}
	i++
	// The letter is the first component and the dot just consumed closed it, so the scan
	// below counts the components after it.
	depth, digits := 1, 0
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			if digits == 0 {
				// "A..1" or "A.." — not a number.
				return 0, false
			}
			depth++
			digits = 0
		default:
			goto done
		}
	}
done:
	if digits > 0 {
		depth++
	}
	if depth < 2 {
		// depth 1 is "A." with no digits after it, which carries no more evidence than the
		// bare letter does: doc.OrderedLabel's comment makes the same call for the same
		// reason, admitting "a." and refusing "A." because an upper-case letter and a dot
		// is how a sentence begins. Nothing promotes that shape, here or in OrderedLists.
		return 0, false
	}
	// Decoded rather than indexed, for numberedLevel's reason: a non-breaking space or a
	// U+2002 between the number and the title is routine, and both are multi-byte. It is
	// also what rejects "A.1" with nothing after it — a folio or a cell — since the empty
	// tail decodes to RuneError; see numberedLevel for why there is no length test here.
	rr, _ := utf8.DecodeRuneInString(s[i:])
	if !unicode.IsSpace(rr) {
		return 0, false
	}
	return depth, true
}

// quantize rounds a size to hundredths of a point.
//
// Style.Size is the effective on-page size, computed through the text matrix and the
// CTM, so two glyphs of the same nominal type can differ in the far decimals. Without
// this, one document's body splits into several clusters and none of them dominates.
func quantize(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

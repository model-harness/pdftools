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
// block as a candidate, and the section number is what assigns the level.
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
// corpus, of 1442 blocks opening with one of listMarkers, reading every ambiguous case
// leaves 5 that are not list items — all of them rows of ISO 32000-2's glyph tables in
// Annex A and D, where an em dash or en dash *is* the row's subject ("— 132 0x84 0204
// U+2014 EM DASH"). At 5 in 1442 the population is 288:1 in favour of promoting, which is
// the inverse of the ratio that made DESIGN.md's lost-space defect not worth a rule.
//
// # The marker is removed, here and not in the sink
//
// Promoting a block to RoleListItem is the statement that its marker is structure
// rather than text, so the marker leaves the spans with it. Leaving it for the sink
// would mean every sink re-deriving this package's allowlist to know which leading rune
// was the marker — the same split doc.Block warns against for space inference, half the
// policy in the model and half in the producer. It would also double the marker on the
// one sink that exists: markdown writes its own "- ".
//
// This is the only place in the package that edits a block's text rather than its role,
// which is why it is confined to removing exactly the rune listMarker matched and the
// whitespace after it.
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
				stripMarker(b)
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
// Reading Text() and not Alt is why stripMarker can edit the spans alone. A block with
// Alt set emits that instead of its spans, so stripping a marker out of spans nothing
// reads would promote the block and leave the marker in the output. It cannot happen
// today: extract never sets Alt, and doctags sets it only on RoleFigure, which the
// paragraph gate here already excludes. It is worth stating because the two facts live in
// other packages — a producer that starts setting Alt on a paragraph breaks this, and the
// symptom would be a "- • item" in the output rather than a test failure.
func isListItem(b *doc.Block) bool {
	return b.Role == doc.RoleParagraph && listMarker(b.Text()) != 0
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

// stripMarker removes the leading marker and the whitespace after it, which is not
// necessarily all in one span.
//
// It starts at the first non-empty span rather than the first, because a producer can
// open a block with an empty span and Text() skips those — the rune listMarker matched
// may not be in Spans[0] at all.
//
// It then keeps trimming leading whitespace across spans until it reaches text, because
// a producer that sets the marker in a span of its own puts the separator in the *next*
// one. lists.pdf does exactly that at its nested level, where the en dash is a bold span
// and " Nested item…" is the roman span after it; stopping at the marker's span left the
// separator behind and the sink wrote "-  Nested", with two spaces. Text() joins spans
// with no separator, so the leading space of a later span is inside the block's text and
// has to go the same way.
//
// Only the marker and that whitespace. A span the strip empties stays in place rather
// than being removed, so the span indices a caller holds stay valid and Span.MCID
// survives for diagnosis; an empty span writes nothing.
func stripMarker(b *doc.Block) {
	found := false
	for i := range b.Spans {
		s := &b.Spans[i]
		if s.Text == "" {
			continue
		}
		if !found {
			rest := strings.TrimLeftFunc(s.Text, unicode.IsSpace)
			if rest == "" {
				// All whitespace. listMarker read the block's text with its leading
				// space trimmed, so the marker is in a later span and this one is
				// part of the separator: empty it and keep looking.
				s.Text = ""
				continue
			}
			r, n := utf8.DecodeRuneInString(rest)
			if !listMarkers[r] {
				// Not this span's job, and no later span's either: listMarker
				// matched the first rune of the block's text, so if that rune is
				// not here the block's text is not what this function was told
				// it was. Leave it alone.
				return
			}
			// unicode.IsSpace rather than a byte cutset: producers separate a
			// marker from its text with U+00A0 routinely, and listMarker admitted
			// the block on that basis, so the strip must accept the same
			// separators the gate did.
			s.Text = strings.TrimLeftFunc(rest[n:], unicode.IsSpace)
			found = true
		} else {
			s.Text = strings.TrimLeftFunc(s.Text, unicode.IsSpace)
		}
		if s.Text != "" {
			return
		}
	}
}

// listMarkers are the glyphs a producer sets as an unordered list's bullet.
//
// An allowlist rather than a character class, because "starts with punctuation" is
// hopeless: 20125 untagged paragraph blocks across the corpus open with 190 distinct
// non-alphanumeric runes, and the common ones are not markers at all — 437 open with
// "/", 256 with "(", 134 with a quote. The glyphs below are what a survey of that tally
// leaves once each candidate's own occurrences are read.
//
// The two U+F0xx entries are Private Use Area codepoints, which look like a mistake and
// are not: Symbol and Wingdings have no Unicode mapping for their bullet, so a producer
// setting one emits a PUA codepoint and the extractor faithfully reports it. F0B7 is
// Symbol's bullet and F06E is Wingdings' filled square, both measured in the corpus.
// This is the same glyph-set debt DESIGN.md records for ZapfDingbats.
//
// Deliberately excluded: "*", "-", "·" and ">". Each occurs block-initially in the
// corpus and every occurrence read was something else — C code (`*/ fz_stream *…`),
// command-line flags (`-o - output file name`), and Annex D's glyph-name table rows
// (`*  asterisk  052  052`). A hyphen especially is a Markdown marker but not a PDF one;
// producers set a real bullet glyph.
var listMarkers = map[rune]bool{
	'•':      true, // BULLET
	'‣':      true, // TRIANGULAR BULLET
	'⁃':      true, // HYPHEN BULLET
	'■':      true, // BLACK SQUARE
	'▪':      true, // BLACK SMALL SQUARE
	'○':      true, // WHITE CIRCLE
	'●':      true, // BLACK CIRCLE
	'◦':      true, // WHITE BULLET
	'–':      true, // EN DASH
	'—':      true, // EM DASH
	'\uf06e': true, // Wingdings filled square, via the PUA
	'\uf0b7': true, // Symbol bullet, via the PUA
}

// listMarker returns the marker rune a block opens with, or zero.
//
// It trims the text itself rather than trusting a caller to, which is what makes the two
// conditions below sufficient: on trimmed text a marker followed by whitespace must have
// something after that whitespace, since the last rune is not one. A separate "and
// content follows" check reads like the third requirement and is unreachable — a
// mutation removing it survives every test, which is how it was found.
//
// The separator is what distinguishes a marker from the same glyph used as a character:
// every one of the 1302 bullet-initial blocks in the corpus has it and none is glued to
// its text, while the excluded "-" is glued in 12 of its 13 occurrences. The length
// requirement is the other measured case — mupdf_explored.pdf has blocks that are a lone
// Wingdings square with no text, which are decoration and not items.
func listMarker(txt string) rune {
	rs := []rune(strings.TrimSpace(txt))
	if len(rs) < 2 || !listMarkers[rs[0]] {
		return 0
	}
	if !unicode.IsSpace(rs[1]) {
		return 0
	}
	return rs[0]
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
// Deliberately not matched: lettered ("A.1"), roman ("IV."), and parenthesised
// ("(a)") schemes. Annex letters especially would be worth having, but "A" is also a
// word, and admitting a single letter followed by a space means admitting every line
// that starts with one. That needs a document-level pass that sees the sequence — an
// A.1 after an A is evidence; an A alone is not — which is more than this closes.
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
	if depth == 0 || i >= len(s) {
		// depth 0 is no leading digits at all; i at the end is a bare number with no
		// title after it, which is a folio or a table cell.
		return 0, false
	}
	// Decoded rather than read as a byte: producers separate a clause number from its
	// title with a non-breaking space routinely, and U+00A0 is two bytes in UTF-8, so
	// rune(s[i]) would test 0xC2 and reject every one of them. unicode.IsSpace covers
	// U+00A0 itself.
	r, _ := utf8.DecodeRuneInString(s[i:])
	if !unicode.IsSpace(r) {
		// Something other than whitespace follows the number: "4.2.1:" or "1st" or
		// "3.14159". A heading's number is followed by its title.
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

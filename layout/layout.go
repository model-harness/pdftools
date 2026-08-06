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
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
)

// Options configures inference.
type Options struct {
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
var DefaultOptions = Options{MaxHeading: 80, MaxLevel: 6}

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
				// splitting the fused case is block segmentation — extract's continues()
				// tests only vertical step — not classification. DESIGN.md §10 records it.
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

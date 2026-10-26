package layout

import (
	"strings"
	"unicode/utf8"

	"github.com/model-harness/pdftools/doc"
)

// Listing reports whether spans are a code listing: text set wholly in a monospaced face, over
// more than one line, in a document whose body is not set in one (ADR 0054).
//
// The face is the producer's statement, as a stroke is, where a role it wrote may not be: Word
// tags a code sample as a P, one paragraph whose lines it broke by hand, and the sinks then fold
// its line ends to spaces, which is the one thing a listing cannot survive. One line of code is
// code in a sentence and stays inline; a proportional span among them is prose quoting code. A
// document set in a monospaced face throughout, a typescript or a plain-text page, says nothing
// by its face, so monoBody turns the rule off.
//
// The lines are either broken already, by the extractor, which ends a monospaced line with a
// line break where a wrap gets a space, or one span lies under the one before it, where the
// producer marked each line separately.
func Listing(spans []doc.Span, monoBody bool) bool {
	if monoBody {
		return false
	}
	// Lines with ink: a blank line is a listing's only between two of them, and ISO 32000-2's
	// "12 0 R" is one line whose span ran on into the blank one under it.
	lines := 0
	open := false
	var prev *doc.Span
	for i := range spans {
		sp := &spans[i]
		if strings.TrimSpace(sp.Text) != "" {
			if !sp.Style.Mono {
				return false
			}
			if prev != nil && (prev.Page != sp.Page || sp.Box.Y1 <= prev.Box.Y0) {
				open = false
			}
			prev = sp
		}
		for k, part := range strings.Split(sp.Text, "\n") {
			if k > 0 {
				open = false
			}
			if strings.TrimSpace(part) != "" && !open {
				lines++
				open = true
			}
		}
	}
	return lines >= 2
}

// MonoBody reports whether most of d's text is set in a monospaced face.
func MonoBody(d *doc.Document) bool {
	mono, all := 0, 0
	for _, p := range d.Pages {
		for _, b := range p.Blocks {
			for _, sp := range b.Spans {
				n := utf8.RuneCountInString(strings.TrimSpace(sp.Text))
				all += n
				if sp.Style.Mono {
					mono += n
				}
			}
		}
	}
	return mono*2 > all
}

// Listings makes each paragraph that is a listing code, on the untagged path, and folds the line
// breaks of every other block; the tagged path asks Listing of each paragraph it builds, and
// FoldBreaks of the rest.
func Listings(d *doc.Document) {
	body := MonoBody(d)
	for pi := range d.Pages {
		for bi := range d.Pages[pi].Blocks {
			b := &d.Pages[pi].Blocks[bi]
			switch {
			case b.Role == doc.RoleParagraph && Listing(b.Spans, body):
				b.Role = doc.RoleCode
			case b.Role != doc.RoleCode:
				FoldBreaks(b.Spans)
			}
		}
	}
}

// FoldBreaks writes each line break in spans as the space a wrap gets. The extractor ends a
// monospaced line with a break where the next line is monospaced too, which is a listing's line
// end; in anything else, such as one line of code whose next line went to another element, it
// stands for the wrap's space.
func FoldBreaks(spans []doc.Span) {
	for i := range spans {
		spans[i].Text = strings.ReplaceAll(spans[i].Text, "\n", " ")
	}
}

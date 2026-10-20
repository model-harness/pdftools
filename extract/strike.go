package extract

import (
	"math"

	"github.com/model-harness/pdftools/doc"
)

// strikeThrough marks the words a horizontal rule is drawn through as struck out, splitting a
// fragment where a struck word meets one that is not (ADR 0040).
//
// A line drawn through text is a rule like any other, and the page says nothing more about it:
// ulem's \sout draws one per word, LaTeX's own and a browser's draw one across the run. What
// makes it a strike is where it runs. It sits between strikeLow and strikeHigh of the type's
// size above the baseline, through the letters' lower body, and it is no thicker than
// strikeWidth of the size. An underline runs below the baseline, and a table rule or a box
// edge runs between lines, so neither is read.
//
// And it is drawn for the text, so it starts and ends within the line's text, give or take a
// space. A line that only crosses the text runs on past it: Well-Tagged PDF draws one across
// its whole measure through a list item, and ISO 32000-2 a figure's connector through
// "arrowhead". The strike ISO 32000-2's errata draw through a caption they move runs from the
// caption's first letter to its last.
//
// The unit is a word, a stretch between two of the fragment's recorded gaps, struck when the
// rules on its line cover half of it. A fragment is one run of text in one style, and a strike
// is usually a few words of a sentence, so the split is what keeps the rest of the sentence
// from being struck with them.
func (r *run) strikeThrough() {
	for i := range r.lines {
		ln := &r.lines[i]
		if ln.orient != 0 {
			continue
		}
		lo, hi := lineExtent(ln)
		var out []frag
		for f := range ln.frags {
			out = append(out, struck(&ln.frags[f], r.rules, lo, hi)...)
		}
		ln.frags = out
	}
}

const (
	// strikeLow and strikeHigh bound a strike's height above the baseline, in the type's size.
	// reference/strikethrough.pdf's \sout runs at 0.26.
	strikeLow, strikeHigh = 0.15, 0.6
	// strikeWidth is the thickest a strike is, in the type's size: \sout's is 0.04.
	strikeWidth = 0.15
)

// struck returns f split into runs of words struck and not, the struck ones' style marked.
// lo and hi are the extent of f's line.
func struck(f *frag, rules []doc.Rule, lo, hi float64) []frag {
	h := f.height
	var cover [][2]float64
	for _, ru := range rules {
		if ru.Vertical || ru.Length() <= 0 || ru.From < lo-f.space || ru.To > hi+f.space {
			continue
		}
		up := ru.Pos - f.cross
		if up < strikeLow*h || up > strikeHigh*h || ru.Width > strikeWidth*h {
			continue
		}
		if lo, hi := math.Max(ru.From, f.along0), math.Min(ru.To, f.along1); hi > lo {
			cover = append(cover, [2]float64{lo, hi})
		}
	}
	if len(cover) == 0 {
		return []frag{*f}
	}
	cover = merged(cover)
	// The words, each its text's byte range and its extent.
	type word struct {
		lo, hi int
		x0, x1 float64
		cut    int // the cut that ends it, or -1 for the last
	}
	var words []word
	at, x0 := 0, f.along0
	for ci, c := range f.cuts {
		words = append(words, word{at, c.off, x0, c.x0, ci})
		at, x0 = c.off, c.x1
	}
	words = append(words, word{at, len(f.text), x0, f.along1, -1})
	struckWord := func(w word) bool {
		got := 0.0
		for _, c := range cover {
			got += math.Max(0, math.Min(c[1], w.x1)-math.Max(c[0], w.x0))
		}
		return w.x1 > w.x0 && got >= 0.5*(w.x1-w.x0)
	}
	var out []frag
	for i := 0; i < len(words); {
		s := struckWord(words[i])
		j := i
		for j+1 < len(words) && struckWord(words[j+1]) == s {
			j++
		}
		p := *f
		p.text = append([]byte(nil), f.text[words[i].lo:words[j].hi]...)
		p.along0, p.along1 = words[i].x0, words[j].x1
		p.style.Strike = s
		p.cuts = nil
		for k := i; k < j; k++ {
			c := f.cuts[words[k].cut]
			c.off -= words[i].lo
			p.cuts = append(p.cuts, c)
		}
		out = append(out, p)
		i = j + 1
	}
	return out
}

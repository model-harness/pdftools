// Package doctags parses DocTags, the output format of the granite-docling
// vision-language models, into this repo's doc model.
//
// DocTags is why granite-docling was chosen over an OCR model that emits prose.
// A model that writes Markdown has already thrown away what it knew: which run of
// text was a heading, which was a caption, where the table's row boundaries were.
// Re-deriving that from the Markdown means running heading heuristics over
// generated text, which is the layout problem again with a worse input. DocTags
// carries the structure explicitly, so this package is a parser rather than a
// second round of inference — and that is the whole argument for the format.
//
// It is also the reason this package can be finished and tested before any model
// runs. Nothing here talks to an engine, loads weights, or opens a socket: the
// input is a string. docling publishes DocTags documents and the Markdown it
// renders them to as MIT-licensed fixtures, and testdata/docling holds seven of
// them, so the grammar is measured against its author's own expectations rather
// than against what a local model happened to emit on a given day.
//
// # The grammar, as it actually appears
//
// The vocabulary is pinned from docling_core/types/doc/tokens.py at the commit in
// testdata/manifest.json, not from prose documentation — the model card documents
// neither the tag set nor the coordinate system. A document is:
//
//	<doctag>
//	<page_header><loc_15><loc_104><loc_30><loc_350>arXiv:2206.01062v1</page_header>
//	<section_header_level_1><loc_88><loc_53><loc_413><loc_75>DocLayNet</section_header_level_1>
//	<page_break>
//	...
//	</doctag>
//
// Every element may carry exactly four location tokens immediately after its open
// tag, and may carry none at all. Elements nest: a <picture> holds its <caption>,
// and an <otsl> table holds both its cells and its caption. <page_break> is a bare
// marker with no closing tag, and it is what separates pages.
//
// # Coordinates are a 500-unit grid, top-down
//
// A <loc_N> is not pixels and not points. tokens.py's get_location_token computes
// round(500*val) clamped to [0,499] from a fraction of the page, and get_location
// emits exactly four of them in x0,y0,x1,y1 order, min/max-sorted. So the numbers
// are twelfths-of-a-percent of the page in each axis, independent of the DPI the
// page was rasterized at — which is the property that makes them usable at all,
// since the raster this text came from was resolution-capped by render.Fit and the
// model resized it again.
//
// The Y axis runs downward, as it does in every raster convention and in none of
// PDF: geom's package comment is explicit that user space has its origin at the
// lower left. Measured rather than assumed — the page_header in barchart.dt is at
// loc_14 to loc_20, i.e. at the very top of its page, and a page header is not at
// the bottom. So this package flips it, and the flip is the one piece of arithmetic
// here that would silently produce a plausible wrong answer: a document parsed
// without it reads correctly and has every block's rectangle mirrored, which no
// text comparison would catch.
package doctags

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/3rg0n/pdf-spec/doc"
	"github.com/3rg0n/pdf-spec/geom"
)

// locGrid is the normalization constant from tokens.py: get_location divides by the
// page dimension and get_location_token multiplies by rnorm=500, clamping to
// rnorm-1. A coordinate is therefore in [0,499] and 499 means "the far edge",
// not "499/500 of the way there" — which is why the divisor below is this constant
// and the clamp is applied against it.
const locGrid = 500

// maxLocs is how many location tokens one element may carry. get_location emits
// exactly four, in x0,y0,x1,y1 order. More than four is a malformed element rather
// than a bounding box, and reading the first four of them would silently place the
// block somewhere plausible.
const maxLocs = 4

// Parse reads a whole DocTags document into pages.
//
// box is the page rectangle every block's coordinates are resolved against, in
// PDF user space. One box for every page rather than one per page: DocTags
// locations are fractions of their own page, so a document whose pages differ in
// size cannot be reconstructed from the tags alone — the information is not in the
// input. The per-page path, which is the one the OCR router uses, has the box from
// the rasterizer and calls ParsePage.
//
// A zero box is allowed and yields blocks with zero rectangles. That is the honest
// result for a caller that has no page geometry, and it is what bad_doc.yaml.dt
// needs: upstream's own degenerate fixture carries no location tokens at all.
func Parse(src string, box geom.Rect) ([]doc.Page, error) {
	toks, err := scan(src)
	if err != nil {
		return nil, err
	}

	// Split on page breaks first, so a malformed element on page 4 cannot shift the
	// page numbering of everything after it.
	var pages []doc.Page
	start := 0
	emit := func(end int) error {
		p, err := build(toks[start:end], len(pages)+1, box)
		if err != nil {
			return err
		}
		pages = append(pages, p)
		return nil
	}
	for i, t := range toks {
		if t.kind == tokPageBreak {
			if err := emit(i); err != nil {
				return nil, err
			}
			start = i + 1
		}
	}
	if err := emit(len(toks)); err != nil {
		return nil, err
	}

	// A trailing <page_break> before </doctag> would otherwise produce an empty last
	// page. Dropped only when it is empty and only when it is not the sole page: a
	// document whose single page yielded nothing is a real result — a blank scan —
	// and reporting zero pages for it would make "the model returned nothing" and
	// "the page was blank" indistinguishable.
	if n := len(pages); n > 1 && len(pages[n-1].Blocks) == 0 {
		pages = pages[:n-1]
	}
	return pages, nil
}

// ParsePage reads one page's DocTags, as a model emits it for a single image.
//
// This is the OCR router's entry point. n is the 1-based page number in the source
// document, which the model does not know and cannot be recovered from the tags.
// A <page_break> in the input is an error rather than a second page: the caller
// asked about one page and silently returning another page's content under this
// page's number is worse than failing.
func ParsePage(src string, n int, box geom.Rect) (doc.Page, error) {
	toks, err := scan(src)
	if err != nil {
		return doc.Page{}, err
	}
	for _, t := range toks {
		if t.kind == tokPageBreak {
			return doc.Page{}, fmt.Errorf("doctags: page %d: input contains a page break, so it is more than one page", n)
		}
	}
	return build(toks, n, box)
}

// ---------------------------------------------------------------------------
// Scanning
// ---------------------------------------------------------------------------

type tokKind int

const (
	tokText tokKind = iota
	tokOpen
	tokClose
	tokLoc
	tokPageBreak
	// tokMarker is a self-contained annotation rather than a container: a picture
	// classification (<bar_chart>), a code language (<_Python_>), or an OTSL cell
	// label (<data>). It has no closing tag and no text of its own.
	tokMarker
	// tokCell is an OTSL grid token. Held apart from tokMarker because these are the
	// only tokens whose *position in the sequence* is the information — a <lcel> is
	// meaningful only as the cell to the right of another one.
	tokCell
)

type token struct {
	kind tokKind
	name string // tag or marker name, without angle brackets
	text string // for tokText
	loc  int    // for tokLoc
}

// scan turns DocTags into a token sequence.
//
// The rule that matters: only a *known* token name is a tag, and anything else
// between angle brackets is literal text. DocTags carries prose, and prose in a
// specification or a paper contains "a < b", "<<Type /Page>>", and "->". Treating
// every <word> as a tag would silently swallow those; treating an unknown one as
// text means a future DocTags token shows up visibly in the output instead of
// deleting the content it wrapped. Neither choice is free, and this is the one
// where the failure is legible.
func scan(src string) ([]token, error) {
	var out []token
	var text strings.Builder

	flush := func() {
		if text.Len() > 0 {
			out = append(out, token{kind: tokText, text: text.String()})
			text.Reset()
		}
	}

	for i := 0; i < len(src); {
		if src[i] != '<' {
			text.WriteByte(src[i])
			i++
			continue
		}
		end := strings.IndexByte(src[i:], '>')
		if end < 0 {
			// An unterminated '<' is the last of the text, not an error. A model whose
			// generation hit its token limit mid-tag is common, and the text before the
			// stub is still the page's content.
			text.WriteString(src[i:])
			break
		}
		inner := src[i+1 : i+end]
		tok, ok := classify(inner)
		if !ok {
			text.WriteString(src[i : i+end+1])
			i += end + 1
			continue
		}
		flush()
		out = append(out, tok)
		i += end + 1
	}
	flush()
	return out, nil
}

// classify maps the inside of a <...> to a token, reporting whether it is one.
func classify(inner string) (token, bool) {
	if inner == "" {
		return token{}, false
	}
	// Self-closing forms exist for every token: get_location_token and
	// get_code_language_token both take a self_closing flag that appends a slash.
	// Stripped here so the rest of the parser sees one spelling.
	inner = strings.TrimSuffix(inner, "/")
	if inner == "" {
		return token{}, false
	}

	if name, closing := strings.CutPrefix(inner, "/"); closing {
		if !isElement(name) {
			return token{}, false
		}
		return token{kind: tokClose, name: name}, true
	}

	if digits, ok := strings.CutPrefix(inner, "loc_"); ok {
		n, err := strconv.Atoi(digits)
		// Out-of-range is rejected rather than clamped. get_location_token already
		// clamps to [0,499] on the way out, so a larger number did not come from that
		// function and is not a coordinate on this grid — clamping it would place the
		// block at the page edge and call it a reading.
		if err != nil || n < 0 || n >= locGrid {
			return token{}, false
		}
		return token{kind: tokLoc, loc: n}, true
	}

	switch {
	case inner == "page_break":
		return token{kind: tokPageBreak}, true
	case isCell(inner):
		return token{kind: tokCell, name: inner}, true
	// Elements are tested before classifications, because two names are in both
	// sets — see the classifications map. An element that is also a classification
	// resolves in the builder, which knows what it is nested inside; classify does
	// not, and guessing here made every element name a classification.
	case isElement(inner):
		return token{kind: tokOpen, name: inner}, true
	case isClassification(inner) || isCodeLanguage(inner):
		return token{kind: tokMarker, name: inner}, true
	}
	return token{}, false
}

// ---------------------------------------------------------------------------
// Building
// ---------------------------------------------------------------------------

// build turns one page's tokens into a doc.Page.
func build(toks []token, n int, box geom.Rect) (doc.Page, error) {
	b := builder{page: doc.Page{Number: n, Box: box, Rasterized: true}, box: box}
	if err := b.run(toks); err != nil {
		return doc.Page{}, fmt.Errorf("doctags: page %d: %w", n, err)
	}
	return b.page, nil
}

type builder struct {
	page doc.Page
	box  geom.Rect

	// listDepth is the current nesting of <ordered_list>/<unordered_list>, which is
	// where a list item's Level comes from. DocTags does not put a depth on the item
	// itself, so a nested list is only recoverable from the containers — which is the
	// one place in this format where the enclosing element carries information the
	// child does not.
	listDepth int
}

// element is one open tag being accumulated.
type element struct {
	name  string
	locs  []int
	text  strings.Builder
	extra string // picture classification or code language, when one was given
}

func (b *builder) run(toks []token) error {
	// Explicit stack rather than recursion: DocTags is model output, so nesting depth
	// is attacker-controlled in the same sense a PDF's tag tree is, and the depth
	// limit is the same defence ADR 0001 applies there. 64 is far past anything the
	// format uses — the deepest real nesting in the fixtures is 2, an otsl holding a
	// caption.
	const maxDepth = 64
	var stack []*element

	for _, t := range toks {
		switch t.kind {
		case tokOpen:
			switch t.name {
			case "doctag":
				// The document wrapper carries nothing. Not pushed, so an unbalanced
				// </doctag> at the end of a truncated generation is not an error.
				continue
			case "ordered_list", "unordered_list":
				b.listDepth++
				continue
			}
			// The <table>/<chart> collision, resolved here because this is the first
			// point that knows the context. Both names are an element and a picture
			// classification, and a classification only ever appears directly inside a
			// figure — so inside one, that is what it is. Read as an element instead, a
			// <picture><table> would open a table that never closes and swallow the rest
			// of the page into its cells.
			if len(stack) > 0 && isClassification(t.name) && isFigure(stack[len(stack)-1].name) {
				if el := stack[len(stack)-1]; el.extra == "" {
					el.extra = t.name
				}
				continue
			}
			if len(stack) >= maxDepth {
				return fmt.Errorf("elements nested more than %d deep", maxDepth)
			}
			stack = append(stack, &element{name: t.name})

		case tokClose:
			switch t.name {
			case "doctag":
				continue
			case "ordered_list", "unordered_list":
				if b.listDepth > 0 {
					b.listDepth--
				}
				continue
			}
			// A close tag that does not match the open one closes the open one anyway.
			// Model output is not guaranteed balanced, and the text collected so far is
			// real content: discarding it to enforce a grammar the generator does not
			// itself enforce would lose the page.
			if len(stack) == 0 {
				continue
			}
			el := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			b.emit(el)

		case tokLoc:
			if len(stack) == 0 {
				continue
			}
			el := stack[len(stack)-1]
			if len(el.locs) < maxLocs {
				el.locs = append(el.locs, t.loc)
			}

		case tokMarker:
			if len(stack) == 0 {
				continue
			}
			// First one wins. A picture has one classification, and a second marker is
			// the generator repeating itself rather than a second meaning.
			if el := stack[len(stack)-1]; el.extra == "" {
				el.extra = t.name
			}

		case tokCell:
			if len(stack) == 0 {
				continue
			}
			b.cell(stack[len(stack)-1], t.name)

		case tokText:
			if len(stack) == 0 {
				// Text outside any element. Kept as a paragraph rather than dropped: a
				// generation that stopped emitting tags is still producing the page's
				// words, and those are the thing being extracted.
				if s := strings.TrimSpace(t.text); s != "" {
					b.append(doc.Block{Role: doc.RoleParagraph, Spans: []doc.Span{{Text: s, MCID: -1}}})
				}
				continue
			}
			stack[len(stack)-1].text.WriteString(t.text)
		}
	}

	// Anything still open at the end is emitted rather than discarded, for the same
	// reason an unmatched close tag does not fail: a truncated generation's last
	// element holds text that is otherwise lost. Innermost first, so a caption
	// precedes the picture it was inside, matching the order a complete document
	// would have produced.
	for i := len(stack) - 1; i >= 0; i-- {
		b.emit(stack[i])
	}
	return nil
}

// cell records one OTSL grid token.
//
// OTSL is a grid serialization, not a tree: <fcel> is a filled cell, <ecel> an
// empty one, <lcel>/<ucel>/<xcel> are continuations of a cell that spans from the
// left, from above, or both, and <nl> ends a row. <ched>/<rhed>/<srow> are filled
// cells that are also column headers, row headers, and section rows.
//
// Continuations produce no block. That is the whole of the span handling, and it is
// correct rather than a simplification: the spanning cell's text was already
// emitted at its origin, so emitting anything here would duplicate it — which in a
// knowledge bundle reads as the document having said it twice.
func (b *builder) cell(el *element, name string) {
	switch name {
	case "lcel", "ucel", "xcel", "nl", "ecel":
		// A row boundary and a span continuation both end whatever cell was being
		// accumulated; an empty cell has nothing to accumulate. All three flush.
		b.flushCell(el)
	case "fcel", "ched", "rhed", "srow":
		b.flushCell(el)
	default:
		// <column_header>, <row_header>, <shed>, <data> — the CELL_LABEL_* tokens.
		// They annotate rather than delimit, so they are dropped here; the ched/rhed
		// forms are what the models actually emit and those are handled above.
	}
}

// flushCell emits the text accumulated since the previous cell token.
func (b *builder) flushCell(el *element) {
	s := strings.TrimSpace(el.text.String())
	el.text.Reset()
	if s == "" {
		return
	}
	b.append(doc.Block{
		Role:  doc.RoleTableCell,
		Spans: []doc.Span{{Text: s, MCID: -1}},
		Box:   b.rect(el.locs),
	})
}

// emit converts a finished element into blocks.
func (b *builder) emit(el *element) {
	// A table's cells were emitted as they were scanned, so what remains in the text
	// buffer is the trailing cell of the last row.
	if isTable(el.name) {
		b.flushCell(el)
		return
	}

	text := strings.TrimSpace(el.text.String())
	role, level := b.roleOf(el.name)

	// A figure with no text is still content: it is the only record that something
	// was on the page at those coordinates, and its classification is the only
	// description of it there is. Everything else with no text is a positioned
	// rectangle the generator left behind.
	if text == "" {
		if role != doc.RoleFigure {
			return
		}
		blk := doc.Block{Role: role, Box: b.rect(el.locs)}
		if el.extra != "" {
			blk.Alt = el.extra
		}
		b.append(blk)
		return
	}

	blk := doc.Block{
		Role:  role,
		Level: level,
		Spans: []doc.Span{{Text: text, MCID: -1}},
		Box:   b.rect(el.locs),
	}
	if role == doc.RoleFigure && el.extra != "" {
		blk.Alt = el.extra
	}
	b.append(blk)
}

func (b *builder) append(blk doc.Block) {
	b.page.Blocks = append(b.page.Blocks, blk)
}

// roleOf maps a DocTags element name onto the doc vocabulary.
//
// The mapping loses distinctions, and the losses are the interesting part.
// doc.Role is deliberately smaller than any structure vocabulary — its comment says
// so — because it is the set both the tagged path and this one have to be able to
// fill. So <footnote>, <page_footer>, and <page_header> all become artifacts,
// <formula> and <smiles> become paragraphs, and a <checkbox_selected> becomes the
// paragraph its label was in. Widening doc.Role to hold them would mean every sink
// growing an opinion about each, which is the thing that package's design rejects.
func (b *builder) roleOf(name string) (doc.Role, int) {
	if level, ok := headingLevel(name); ok {
		return doc.RoleHeading, level
	}
	switch name {
	case "title":
		return doc.RoleHeading, 1
	case "list_item":
		// Level 1 when the item appeared outside any list container, which happens in
		// model output: the depth is the container's and a bare item has none.
		return doc.RoleListItem, max(b.listDepth, 1)
	case "caption":
		return doc.RoleCaption, 0
	case "picture", "chart":
		return doc.RoleFigure, 0
	case "code":
		return doc.RoleCode, 0
	case "page_header", "page_footer", "footnote":
		// Page furniture, matching what the extractor marks as an artifact from
		// /Artifact marked content. The md sink omits these by default and -artifacts
		// keeps them, so the two paths behave the same way for the same content.
		return doc.RoleArtifact, 0
	}
	// text, paragraph, formula, reference, document_index, form, key_value_region,
	// handwritten_text, smiles, inline, checkbox_*, and any element this switch has
	// not been taught.
	return doc.RoleParagraph, 0
}

// headingLevel reads section_header_level_N.
//
// N runs 0..5 in tokens.py, and doc.Block.Level is 1..6 for a heading, so the
// mapping is N+1. That puts section_header_level_1 at level 2, which is what
// docling's own Markdown does for the paper in testdata/docling — `##`. It is not
// what docling does for bad_doc.yaml.dt, where the same tag renders as `###`
// because a <title> precedes it and docling's serializer nests relative to it. This
// package does not reproduce that: a heading's level is what the tag says, because
// a level that depends on what came earlier cannot be assigned while parsing one
// page of a document, which is exactly the case the OCR router produces.
func headingLevel(name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, "section_header_level_")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || n > 5 {
		return 0, false
	}
	return n + 1, true
}

// rect converts four location tokens into a rectangle in PDF user space.
//
// Returns the zero Rect for anything other than exactly four, which is the honest
// answer for an element the model gave no coordinates for — and for a whole
// document of them, which upstream ships as a fixture.
func (b *builder) rect(locs []int) geom.Rect {
	if len(locs) != maxLocs || b.box.IsZero() {
		return geom.Rect{}
	}
	w, h := b.box.Width(), b.box.Height()
	x0 := b.box.X0 + float64(locs[0])/locGrid*w
	x1 := b.box.X0 + float64(locs[2])/locGrid*w
	// Y is flipped. DocTags counts down from the top of the page and PDF user space
	// counts up from the bottom, so the tag's y0 — the *upper* edge — becomes the
	// rectangle's Y1. Getting this backwards mirrors every block on the page and
	// changes no text, which is why it is spelled out rather than inlined.
	y1 := b.box.Y1 - float64(locs[1])/locGrid*h
	y0 := b.box.Y1 - float64(locs[3])/locGrid*h
	return geom.NewRect(x0, y0, x1, y1)
}

// ---------------------------------------------------------------------------
// Vocabulary
// ---------------------------------------------------------------------------

// The token sets below are transcribed from docling_core/types/doc/tokens.py at the
// commit pinned in testdata/manifest.json — DocumentToken, TableToken,
// _PictureClassificationToken, and _CodeLanguageToken. They are data, not policy:
// scan consults them only to decide whether a <...> is a tag or is prose that
// happens to contain an angle bracket.

// elements are DocumentToken's values, which appear as <x> and </x>.
var elements = map[string]bool{
	"doctag": true, "otsl": true, "chart": true,
	"ordered_list": true, "unordered_list": true,
	"smiles": true, "inline": true, "caption": true, "footnote": true,
	"formula": true, "list_item": true, "page_footer": true, "page_header": true,
	"picture": true, "table": true, "text": true, "title": true,
	"document_index": true, "code": true,
	"checkbox_selected": true, "checkbox_unselected": true,
	"form": true, "key_value_region": true, "paragraph": true,
	"reference": true, "handwritten_text": true,
}

func isElement(name string) bool {
	if elements[name] {
		return true
	}
	_, ok := headingLevel(name)
	return ok
}

// isTable reports whether an element's children are an OTSL grid.
//
// <chart> is one of these. barchart.dt is the fixture that shows why: a chart holds
// a classification token and then a full OTSL grid of the chart's underlying data,
// which is the useful part — "Convert chart to table." is one of granite-docling's
// documented prompts.
func isTable(name string) bool {
	return name == "otsl" || name == "table" || name == "chart"
}

// isFigure reports the elements a picture classification can appear inside. It is
// the disambiguator for the two names that are both an element and a classification.
func isFigure(name string) bool {
	return name == "picture" || name == "chart"
}

// cells are TableToken's grid and label values.
var cells = map[string]bool{
	"ecel": true, "fcel": true, "lcel": true, "ucel": true, "xcel": true,
	"nl": true, "ched": true, "rhed": true, "srow": true,
	"column_header": true, "row_header": true, "shed": true, "data": true,
}

func isCell(name string) bool { return cells[name] }

// classifications are _PictureClassificationToken's values: what a vision model says
// a picture *is*, from the DocumentFigureClassifier taxonomy.
//
// Enumerated rather than recognized by shape. The first draft matched any lower-case
// identifier, which is the shape every one of these has — and also the shape every
// element name has, so it classified <text> and <caption> as picture types and
// produced a document of 561 undifferentiated paragraphs that looked entirely
// plausible. The list is a maintenance cost; a model that gains a class emits a
// token this map lacks and it surfaces as visible text, which is the same legible
// degradation scan gives every unknown token.
//
// Two of these names — table and chart — are also element names. That collision is
// real in the format and is resolved in the builder by what the token is nested
// inside, since a classification only ever appears directly within a figure.
var classifications = map[string]bool{
	// v2 model tokens: charts
	"bar_chart": true, "box_plot": true, "flow_chart": true, "line_chart": true,
	"pie_chart": true, "scatter_plot": true, "table": true,
	// images
	"full_page_image": true, "page_thumbnail": true, "photograph": true,
	// chemistry
	"chemistry_structure": true,
	// company and document
	"bar_code": true, "icon": true, "logo": true, "qr_code": true,
	"signature": true, "stamp": true,
	// engineering
	"engineering_drawing": true,
	// screenshots
	"screenshot_from_computer": true, "screenshot_from_manual": true,
	// geography
	"geographical_map": true, "topographical_map": true,
	// other
	"calendar": true, "crossword_puzzle": true, "music": true, "other": true,
	// legacy tokens, kept because a checkpoint older than the current classifier
	// still emits them and dropping them would silently lose the description
	"cad_drawing": true, "chart": true, "decoration": true,
	"electrical_diagram": true, "map": true, "heatmap": true,
	"illustration": true, "infographic": true,
	"chemistry_markush_structure": true, "chemistry_molecular_structure": true,
	"natural_image": true, "person": true, "picture_group": true,
	"remote_sensing": true, "scatter_chart": true, "screenshot": true,
	"stacked_bar_chart": true, "stratigraphic_chart": true, "ui_element": true,
}

func isClassification(name string) bool { return classifications[name] }

// isCodeLanguage reports a _CodeLanguageToken: <_Python_>, <_C++_>, <_unknown_>.
//
// Matched by shape, unlike the classifications above, because the shape is
// unambiguous — the leading and trailing underscores are exactly what distinguishes
// these from every other token in the format, and nothing else in DocTags uses them.
// Enumerating fifty-odd language names would buy nothing but a list to update.
func isCodeLanguage(name string) bool {
	return len(name) > 2 && name[0] == '_' && name[len(name)-1] == '_'
}

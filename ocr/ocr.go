// Package ocr recovers text from pages whose content stream does not carry it.
//
// It is the fallback path, and naming it that is the whole design. A scanned page
// has an image and no text; a born-digital page has text and the extractor already
// read it. Running a vision model over the second kind costs a GPU-second to produce
// a worse answer than the one already in hand — the content stream states its
// glyphs, while a model infers them — so the decision of *which* pages to send is
// worth more than any model choice. That decision is Route, and it is the only part
// of this package that runs without a model.
//
// # The interface is the point
//
// Engine is three methods this repo declares, and no type belonging to any backend
// appears in them. That follows the pattern objects/pdfcpu and render/pdfium
// established: the consuming package owns the interface, adapters live in a
// subpackage, and swapping a backend is a wiring change in cmd rather than a
// refactor. Here it matters more than usual, because the backend question is
// genuinely open — a local daemon, a subprocess, a hosted model, or a native
// implementation are all plausible, and this package is finished either way.
//
// # DocTags, not Markdown
//
// An Engine returns DocTags: the structured output format of the granite-docling
// models, parsed by ocr/doctags into the same doc.Page the extractor produces. That
// is the reason for choosing a model that emits structure at all. A model that
// writes Markdown has already discarded what it knew — which line was a heading,
// where the table's cells were — and recovering it means running layout heuristics
// over generated prose, which is the original problem with a worse input.
//
// So both paths converge on doc.Page, every sink downstream is unchanged, and a
// document can be part scanned and part digital without the caller knowing which
// pages were which. doc.Page.Rasterized is the record of that, and it is the only
// difference the model's involvement leaves behind.
package ocr

import (
	"context"
	"image"

	"github.com/3rg0n/pdf-spec/doc"
)

// Engine turns one page image into DocTags.
//
// One page per call, not one document. A page is the unit a vision model accepts,
// the unit the router decides about, and the unit a failure should be confined to:
// a fifty-page scan with one page the model chokes on should yield forty-nine.
//
// Implementations are not required to be safe for concurrent use, matching
// render.Rasterizer — an engine typically wraps one connection, and parallelism is
// several engines rather than one shared. Callers that fan out open one per worker.
type Engine interface {
	// Recognize sends img and returns the DocTags the model produced.
	//
	// The image is *image.RGBA because that is what render produces; the alpha
	// channel is ignored, and an adapter whose wire wants packed RGB drops it.
	// Returns the text generated so far along with the error when generation
	// failed partway, because a truncated page is worth more than nothing and
	// ocr/doctags parses truncated input by design.
	Recognize(ctx context.Context, img *image.RGBA, opt Options) (string, error)

	// Close releases the engine's connection or process. Safe to call twice.
	Close() error
}

// Options are the per-page knobs an Engine honours.
type Options struct {
	// Prompt is what the model is asked to do. Empty means PromptPage.
	//
	// Exposed because granite-docling is prompted for a task rather than being a
	// single-purpose OCR model: the same weights convert a page, a chart, a
	// formula, or a table, and the prompt is the whole difference. See the Prompt
	// constants.
	Prompt string

	// MaxTokens bounds the generation. Zero means the backend's default.
	//
	// Worth setting for OCR specifically, because the failure mode of a vision
	// model on a dense page is not silence — it is a repetition loop that emits
	// the same table row until something stops it. The bound is what turns that
	// into a truncated page ocr/doctags still parses.
	MaxTokens int

	// OnDelta, when set, is called with each incremental chunk of generated text.
	//
	// A page of dense DocTags takes tens of seconds on modest hardware, so a run
	// with no output at all is indistinguishable from a hang. This is how the verb
	// reports progress; it is not how output is collected, since Recognize returns
	// the whole string regardless.
	OnDelta func(string)
}

// Prompts granite-docling recognizes. Taken from the model's own documentation
// rather than invented: the model was instruction-tuned on these exact strings, and
// a paraphrase measurably degrades the output — this is prompt-as-API, so they are
// constants rather than a format string a caller assembles.
const (
	// PromptPage converts a whole page. The default and the only one the router
	// uses; the rest are for a caller targeting a region it already located.
	PromptPage = "Convert this page to docling."

	PromptChart   = "Convert chart to table."
	PromptFormula = "Convert formula to LaTeX."
	PromptCode    = "Convert code to text."
	PromptTable   = "Convert table to OTSL."
)

// Route reports whether a page should be sent to an Engine.
//
// The rule is coverage: the fraction of the page's crop box covered by the text
// blocks the extractor found. A scanned page has an image spanning the page and no
// text blocks at all, so its coverage is zero; a born-digital page has text spread
// across most of it. The threshold separates them, and the reason it is a coverage
// ratio rather than a character count is the case that motivates the whole router —
// a scanned page carrying a single line of digital text in a header. Counting
// characters says "this page has text, skip it" and loses the entire scan. Coverage
// says a page whose text occupies 2% of it has essentially none.
//
// doc.Page.Coverage was written for this and documents itself as "the numerator of
// the OCR router's coverage rule", including the two defensive cases that matter
// here: a block outside the crop box clamps rather than pushing coverage above 1,
// and a zero-area page box yields 0, so a defective page routes to the model rather
// than being silently accepted as complete.
//
// A page already marked Rasterized is never routed again — it came from a model, so
// sending it back would ask the model to read its own output.
func Route(p doc.Page, threshold float64) bool {
	if p.Rasterized {
		return false
	}
	return p.Coverage() < threshold
}

// DefaultThreshold is the coverage below which a page is sent to the model.
//
// 5% rather than 0. A pure scan is exactly zero and any positive threshold catches
// it, so the number is really about the mixed page: a scan whose only digital text
// is a stamped page number, a header, or a Bates number occupies a percent or two,
// and at a threshold of zero every one of those pages silently yields one line
// instead of the page. Set well below what a page of real prose reaches — a
// single-column page of body text covers a third of its box or more — so the two
// populations are not close to each other and the exact value is not load-bearing.
const DefaultThreshold = 0.05

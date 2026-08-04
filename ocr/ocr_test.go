package ocr

import (
	"testing"

	"github.com/3rg0n/pdf-spec/doc"
	"github.com/3rg0n/pdf-spec/geom"
)

// letter is US Letter in points.
var letter = geom.NewRect(0, 0, 612, 792)

// block builds a text block covering a fraction of the page's height across its full
// width, which is what a band of body text looks like to the router.
func block(fracTop, fracBottom float64) doc.Block {
	h := letter.Height()
	return doc.Block{
		Role:  doc.RoleParagraph,
		Spans: []doc.Span{{Text: "x", MCID: -1}},
		Box:   geom.NewRect(0, letter.Y1-fracBottom*h, letter.X1, letter.Y1-fracTop*h),
	}
}

// TestRoute is the decision the whole package hangs on: which pages cost a
// GPU-second and which are already done.
func TestRoute(t *testing.T) {
	cases := []struct {
		name string
		page doc.Page
		want bool
		why  string
	}{
		{
			name: "a pure scan",
			page: doc.Page{Number: 1, Box: letter},
			want: true,
			why:  "no text blocks at all: coverage 0, which is the case that motivates the package",
		},
		{
			name: "a page of body text",
			page: doc.Page{Number: 1, Box: letter, Blocks: []doc.Block{block(0.1, 0.9)}},
			want: false,
			why:  "80% of the page is text, so the content stream already states it",
		},
		{
			// The case the rule exists for. Counting characters says "this page has
			// text, skip it" and loses the entire scan behind one stamped line.
			name: "a scan with a stamped header",
			page: doc.Page{Number: 1, Box: letter, Blocks: []doc.Block{block(0.02, 0.04)}},
			want: true,
			why:  "2% coverage is a page number, not a page",
		},
		{
			name: "already rasterized",
			page: doc.Page{Number: 1, Box: letter, Rasterized: true},
			want: false,
			why:  "it came from a model; sending it back asks the model to read its own output",
		},
		{
			// doc.Page.Coverage documents this: a zero-area box yields 0, so a defective
			// page goes to the model rather than being accepted as complete.
			name: "a page with no box",
			page: doc.Page{Number: 1, Blocks: []doc.Block{block(0.1, 0.9)}},
			want: true,
			why:  "a zero-area page box cannot be shown to be covered",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Route(tc.page, DefaultThreshold); got != tc.want {
				t.Errorf("Route = %v, want %v: %s (coverage %.3f)", got, tc.want, tc.why, tc.page.Coverage())
			}
		})
	}
}

// TestRouteThreshold checks that the threshold is the only knob and that it behaves
// monotonically — a page routed at a low threshold must also route at a higher one.
// Not obvious from the expression alone, and a router that inverted somewhere in the
// middle would send exactly the wrong pages.
func TestRouteThreshold(t *testing.T) {
	page := doc.Page{Number: 1, Box: letter, Blocks: []doc.Block{block(0.0, 0.2)}}
	cov := page.Coverage()
	if cov <= 0 || cov >= 1 {
		t.Fatalf("test setup: coverage %.3f is not in the interesting range", cov)
	}

	var lastRouted bool
	for _, th := range []float64{0, 0.05, 0.1, 0.2, 0.3, 0.5, 1} {
		routed := Route(page, th)
		if lastRouted && !routed {
			t.Errorf("threshold %.2f did not route a page that a lower threshold did", th)
		}
		lastRouted = routed
	}
	// Threshold 0 routes nothing, which is how a caller disables OCR without a second
	// flag meaning the same thing.
	if Route(page, 0) {
		t.Error("threshold 0 routed a page; it must be the off switch")
	}
	// Threshold 1 routes everything short of a fully covered page, which is how a
	// caller forces OCR on a document they know is bad.
	if !Route(page, 1) {
		t.Error("threshold 1 did not route a partially covered page")
	}
}

// TestDefaultThresholdSeparatesPopulations checks that the default sits in the gap
// between the two kinds of page rather than close to either. That is the property that
// makes the exact number not load-bearing.
func TestDefaultThresholdSeparatesPopulations(t *testing.T) {
	scanWithHeader := doc.Page{Number: 1, Box: letter, Blocks: []doc.Block{block(0.02, 0.04)}}
	bodyText := doc.Page{Number: 2, Box: letter, Blocks: []doc.Block{block(0.08, 0.92)}}

	if c := scanWithHeader.Coverage(); c >= DefaultThreshold {
		t.Errorf("a stamped header covers %.3f, at or above the %.2f default", c, DefaultThreshold)
	}
	if c := bodyText.Coverage(); c <= DefaultThreshold*3 {
		t.Errorf("a page of body text covers only %.3f, too close to the %.2f default", c, DefaultThreshold)
	}
}

// TestPromptsAreExact pins the prompt strings. granite-docling was instruction-tuned
// on these exact sentences and a paraphrase measurably degrades the output, so this is
// prompt-as-API: a "harmless" reword here is a silent quality regression with no test
// anywhere else to catch it.
func TestPromptsAreExact(t *testing.T) {
	for got, want := range map[string]string{
		PromptPage:    "Convert this page to docling.",
		PromptChart:   "Convert chart to table.",
		PromptFormula: "Convert formula to LaTeX.",
		PromptCode:    "Convert code to text.",
		PromptTable:   "Convert table to OTSL.",
	} {
		if got != want {
			t.Errorf("prompt = %q, want %q", got, want)
		}
	}
}

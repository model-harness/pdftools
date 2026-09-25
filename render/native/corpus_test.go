package native

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/liberation"
	pcstore "github.com/model-harness/pdftools/objects/pdfcpu"
	"github.com/model-harness/pdftools/render"
)

// TestCorpusCoverage measures how much of the corpus this backend draws, and asserts a floor.
//
// # Why a count is worth asserting
//
// Because the count is the claim. Every ADR in this phase states how many pages render, and a
// figure in prose drifts: a refusal added for a good reason elsewhere can take a hundred pages
// with it and no other test would notice, since every other test here renders a fixture this
// package wrote. This one renders documents nobody wrote for it.
//
// The floor is stated as a number rather than a proportion so that it fails when the *count*
// falls even if the corpus grows, and it is set just under the measured figure rather than far
// under: a loose floor is a figure that can drift for phases before anything complains.
//
// # Why it skips rather than fails without the corpus
//
// The documents are sponsored ISO releases, paid for and not redistributable, so they are
// gitignored and absent from every clone. A test that failed there would fail for everyone but
// me. The skip is the compromise, and it is a real cost: this assertion does not run in CI, which
// is why the per-feature refusal reasons below are logged — a run that skips says so, and a run
// that happens prints the whole census rather than just a verdict.
//
// This is also the only test in the package that supplies a FaceSource, and so the only one that
// exercises what cmd/pdfspec actually does: without one the backend refuses every page whose font
// embeds no program, which is 1,140 of these 1,251.
func TestCorpusCoverage(t *testing.T) {
	// Skipped under -short, which is how a mutation run stays affordable: this is 137 seconds
	// against the rest of the package's two, so thirty mutations through it is seventy minutes of
	// mostly re-measuring a number none of them change. A mutation that *only* this test kills
	// therefore shows up as a survivor under -short and has to be re-checked without it, which is
	// a real cost of the skip and the reason it is noted here rather than just used.
	if testing.Short() {
		t.Skip("corpus coverage takes 137s; run without -short")
	}

	dir := filepath.Join("..", "..", "docs")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no corpus: %v", err)
	}
	var names []string
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".pdf" {
			names = append(names, e.Name())
		}
	}
	if len(names) < 12 {
		t.Skipf("corpus has %d PDFs, want the full 12 — they are gitignored", len(names))
	}
	sort.Strings(names)

	// 20 dpi, which is not a resolution anyone would render at and is the right one here: whether
	// a page is refused is decided by the survey, which never looks at a pixel, so the *count* this
	// test asserts is resolution-independent while the painting it pays for is not. At the default
	// 200 dpi the 1,113 pages that draw cost a Letter page's 3.7 million pixels each and the run
	// exceeded go test's ten-minute timeout; at 20 dpi it is a hundredth of that and measures
	// exactly the same set.
	o := render.DefaultOptions
	o.DPI = 20

	faces := liberation.New()
	blockers := map[string]int{}
	drawn, total := 0, 0
	for _, n := range names {
		s, err := pcstore.Open(filepath.Join(dir, n))
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		r := New(s, WithFaces(faces))
		for pg := 1; pg <= s.PageCount(); pg++ {
			total++
			_, err := r.Page(pg, o)
			if err == nil {
				drawn++
				continue
			}
			var u *Unsupported
			if !errors.As(err, &u) {
				t.Errorf("%s page %d: %v", n, pg, err)
				continue
			}
			// Deduplicated per page, because two refusal reasons can bucket to one feature —
			// cs and scn always appear together — and counting each would report 194 pages for
			// the 97 that have them. A count that double-counts is worse than no count: it
			// reads as a bigger problem than it is and ranks the worklist wrongly.
			seen := map[string]bool{}
			for _, op := range u.Ops {
				seen[feature(op)] = true
			}
			for f := range seen {
				blockers[f]++
			}
		}
		if err := s.Close(); err != nil {
			t.Errorf("%s close: %v", n, err)
		}
	}

	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(blockers))
	for k, v := range blockers {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	t.Logf("%d of %d pages drawn natively", drawn, total)
	for _, e := range list {
		t.Logf("  blocked by %-34s %4d pages", e.k, e.v)
	}

	// Measured at 1,113 with Liberation supplying Sans and Serif, XObjects drawn, and strokes
	// drawn. Stated tight, and a *rise* is not a failure — only a fall is, because the only way
	// this number goes down is a feature regressing or a refusal widening.
	const floor = 1113
	if drawn < floor {
		t.Errorf("drew %d of %d pages, want at least %d — a refusal widened or a feature regressed",
			drawn, total, floor)
	}
	if total != 1251 {
		t.Errorf("corpus has %d pages, want 1251 — the floor above was measured against that", total)
	}
}

// feature buckets a refusal reason into the increment that would answer it, so the log above is a
// worklist rather than a thousand strings.
func feature(op string) string {
	switch {
	case strings.HasPrefix(op, "gs: "), strings.HasPrefix(op, "text: "):
		if i := strings.Index(op, ","); i > 0 {
			return op[:i]
		}
		return op
	case strings.HasPrefix(op, "Do: "):
		// Every Do reason names its XObject after a ", /"; an image's then says why it cannot be
		// drawn, and that part is the bucket, so only the name is cut.
		i := strings.Index(op, ", /")
		if i < 0 {
			return op
		}
		if j := strings.Index(op[i:], ": "); j > 0 {
			return op[:i] + op[i+j:]
		}
		return op[:i]
	case op == "cs", op == "scn":
		return "cs/scn (non-device colour)"
	case strings.HasPrefix(op, "stroke: the colour space is /"):
		// Named by its resource, /CS0 or /CS1, which is the page's choice and not the feature.
		return "stroke: non-device colour"
	case op == "sh":
		return "sh (shadings)"
	}
	return op
}

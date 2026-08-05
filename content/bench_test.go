package content

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/geom"
)

// The benchmark exists because the §1 target is a throughput number, and the
// scanner sits on the hot path for every page. Allocations per operator are the
// figure that matters: a 1,000-page document runs millions of operators, so one
// allocation each is the difference between milliseconds and seconds.

// benchStream is shaped like real page content: a marked-content wrapper, text
// state, positioning, and a TJ array per line.
var benchStream = []byte(strings.Repeat(
	`/P <</MCID 0>> BDC
BT /F1 11 Tf 13.5 TL 1 0 0 1 72 700 Tm
[(The quick brown fox jumps over the lazy dog.) -250 (Again.)] TJ T*
[(Second line with kerning) 20 (adjustments) -15 (between runs.)] TJ T*
ET
EMC
`, 200))

func BenchmarkScanner(b *testing.B) {
	b.SetBytes(int64(len(benchStream)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sc := NewScanner(benchStream)
		n := 0
		for {
			_, ok := sc.Next()
			if !ok {
				break
			}
			n++
		}
		if n == 0 {
			b.Fatal("no operators")
		}
	}
}

func BenchmarkScannerWithMachine(b *testing.B) {
	// The combination an extractor actually runs, including a RenderMatrix per
	// text-showing operator.
	b.SetBytes(int64(len(benchStream)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := NewMachine(geom.Identity)
		sc := NewScanner(benchStream)
		for {
			op, ok := sc.Next()
			if !ok {
				break
			}
			if !m.Apply(op) && op.Name == "TJ" {
				_ = m.RenderMatrix()
				_ = m.MCID()
			}
		}
	}
}

func BenchmarkLexer(b *testing.B) {
	b.SetBytes(int64(len(benchStream)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := NewLexer(benchStream)
		for {
			if l.Next().Kind == KindEOF {
				break
			}
		}
	}
}

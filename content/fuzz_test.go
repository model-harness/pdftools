package content

import (
	"testing"

	"github.com/3rg0n/pdf-spec/geom"
)

// Content streams are untrusted input, and the failure modes that matter here are
// the ones a table of cases will not find: a lexer that fails to advance on some
// byte pair, an assembler that recurses on nesting, a state machine that indexes
// an operand that is not there. These targets assert only that the pipeline
// terminates without panicking, which is the property that must hold for every
// possible input rather than for the inputs I anticipated.
//
// Run longer with:
//
//	go test ./content/ -run xxx -fuzz FuzzScanner -fuzztime 5m

func FuzzLexer(f *testing.F) {
	seeds := []string{
		"BT /F1 12 Tf (hi) Tj ET",
		"[(a) -20 (b)] TJ",
		"<</MCID 0>> BDC",
		`(\777\0\)\\) Tj`,
		"<48656C6C6F> Tj",
		"1 0 0 1 72 720 cm",
		"BI /W 2 /H 2 ID \x00\xFF EI",
		"--5 1-2 1.2.3 .5 4.",
		"% comment\n>>> ]]] {{{",
		"/Name#20Escaped#zz",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		l := NewLexer(data)
		last := -1
		for i := 0; ; i++ {
			// Every token must consume at least one byte, or a caller loops forever.
			// The bound is generous: one token per byte is the theoretical maximum.
			if i > len(data)+16 {
				t.Fatalf("lexer produced more tokens than bytes at pos %d", l.Pos())
			}
			tok := l.Next()
			if tok.Kind == KindEOF {
				break
			}
			if tok.Pos <= last {
				t.Fatalf("token at Pos %d did not advance past %d", tok.Pos, last)
			}
			last = tok.Pos
		}
		if l.Pos() > len(data) {
			t.Fatalf("final Pos %d exceeds input length %d", l.Pos(), len(data))
		}
	})
}

func FuzzScanner(f *testing.F) {
	seeds := []string{
		"BT /F1 12 Tf 100 700 Td (hello) Tj ET",
		"q 1 0 0 1 10 10 cm /P <</MCID 3>> BDC (x) Tj EMC Q",
		"[[[[[[1]]]]]] TJ",
		"<< /A << /B [1 2 <</C 3>>] >> >> BDC",
		"BI /W 1 /H 1 /F /Fl ID \x00\x01 EI Q",
		"[(unclosed TJ",
		"1 2 3 4 5 6 7 8 9 Tm",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		m := NewMachine(geom.Identity)
		sc := NewScanner(data)
		for i := 0; ; i++ {
			if i > len(data)+16 {
				t.Fatal("scanner produced more operators than bytes")
			}
			op, ok := sc.Next()
			if !ok {
				break
			}
			m.Apply(op)

			// Accessors must tolerate any operand count and type. A malformed stream
			// supplies the wrong shape constantly, so these are the calls an
			// extractor makes on every operator.
			_, _, _ = op.Num(0), op.Int(1), op.NameAt(2)
			_, _, _ = op.Str(0), op.Arr(1), op.Dict(2)
			_, _ = op.Num(-1), op.Int(1<<20)

			_ = m.RenderMatrix()
			_ = m.MCID()
			_ = m.MarkedTag()
			_ = m.InArtifact()
			_ = m.Visible()
			_ = sc.InlineData()
		}

		// Bounds must hold no matter what the input asked for.
		if len(m.stack) > maxStateDepth {
			t.Fatalf("graphics state stack reached %d, above %d", len(m.stack), maxStateDepth)
		}
		if len(m.mcStack) > maxStateDepth {
			t.Fatalf("marked-content stack reached %d, above %d", len(m.mcStack), maxStateDepth)
		}
	})
}

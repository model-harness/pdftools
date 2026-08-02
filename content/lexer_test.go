package content

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/3rg0n/pdf-spec/objects"
)

// lexAll drains a lexer, returning every token before EOF. A bound guards
// against a lexer that fails to advance, which would otherwise hang the test
// rather than fail it.
func lexAll(t *testing.T, src string) []Token {
	t.Helper()
	l := NewLexer([]byte(src))
	var out []Token
	for i := 0; ; i++ {
		if i > 100000 {
			t.Fatalf("lexer did not terminate at pos %d", l.Pos())
		}
		tok := l.Next()
		if tok.Kind == KindEOF {
			return out
		}
		out = append(out, tok)
	}
}

// ops returns the operator names in order, which is the property most operator
// tests care about.
func ops(toks []Token) []string {
	var out []string
	for _, t := range toks {
		if t.Kind == KindOperator {
			out = append(out, t.Op)
		}
	}
	return out
}

func TestLexBasicOperators(t *testing.T) {
	toks := lexAll(t, "BT /F1 12 Tf 100 700 Td (Hello) Tj ET")
	got := ops(toks)
	want := []string{"BT", "Tf", "Td", "Tj", "ET"}
	if len(got) != len(want) {
		t.Fatalf("operators = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("operators = %v, want %v", got, want)
		}
	}
}

func TestLexOperatorNamesAreInterned(t *testing.T) {
	// Interning is invisible: a lexer that allocated a fresh string per operator
	// would pass every other test in this file while costing an allocation on the
	// hottest path in extraction. This checks the observable consequence — the
	// returned string shares its backing array with the interned one — so that
	// removing the table fails here rather than silently in a benchmark nobody ran.
	toks := lexAll(t, "BT ET q Q Tj TJ BDC EMC Tf cm re")
	for _, tok := range toks {
		if tok.Kind != KindOperator {
			continue
		}
		want, ok := operators[tok.Op]
		if !ok {
			t.Fatalf("operator %q is missing from the intern table", tok.Op)
		}
		if unsafe.StringData(tok.Op) != unsafe.StringData(want) {
			t.Errorf("operator %q was copied rather than interned", tok.Op)
		}
	}

	// An operator outside Annex A still has to lex, just without interning.
	toks = lexAll(t, "NotAnOperator")
	if len(toks) != 1 || toks[0].Kind != KindOperator || toks[0].Op != "NotAnOperator" {
		t.Fatalf("unrecognized operator = %+v, want a KindOperator token", toks)
	}
}

func TestLexNumbers(t *testing.T) {
	cases := []struct {
		in   string
		want objects.Object
	}{
		{"0", objects.Int(0)},
		{"42", objects.Int(42)},
		{"-17", objects.Int(-17)},
		{"+8", objects.Int(8)},
		{"3.14", objects.Real(3.14)},
		{"-0.5", objects.Real(-0.5)},
		{".5", objects.Real(0.5)},
		{"4.", objects.Real(4)},
		// Integer-valued reals stay Real: the token had a decimal point, and a
		// caller asking for an int can still convert.
		{"12.0", objects.Real(12)},
		// Malformed forms real producers emit. Dropping these would shift every
		// following operand by one position, which is worse than a wrong value.
		{"--5", objects.Int(-5)},
		{"1-2", objects.Int(1)},
		{"1.2.3", objects.Real(1.2)},
		{"-", objects.Int(0)},
		{".", objects.Int(0)},
	}
	for _, c := range cases {
		toks := lexAll(t, c.in)
		if len(toks) != 1 {
			t.Errorf("%q: %d tokens, want 1", c.in, len(toks))
			continue
		}
		if toks[0].Kind != KindObject {
			t.Errorf("%q: kind = %v, want KindObject", c.in, toks[0].Kind)
			continue
		}
		if toks[0].Val != c.want {
			t.Errorf("%q -> %#v, want %#v", c.in, toks[0].Val, c.want)
		}
	}
}

func TestLexKeywordObjects(t *testing.T) {
	toks := lexAll(t, "true false null")
	if len(toks) != 3 {
		t.Fatalf("%d tokens, want 3", len(toks))
	}
	if toks[0].Val != objects.Bool(true) {
		t.Errorf("true -> %#v", toks[0].Val)
	}
	if toks[1].Val != objects.Bool(false) {
		t.Errorf("false -> %#v", toks[1].Val)
	}
	if _, ok := toks[2].Val.(objects.Null); !ok {
		t.Errorf("null -> %#v", toks[2].Val)
	}
	for i, tok := range toks {
		if tok.Kind != KindObject {
			t.Errorf("token %d is %v, want KindObject", i, tok.Kind)
		}
	}
}

func TestLexNames(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/F1", "F1"},
		{"/Name#20With#20Spaces", "Name With Spaces"},
		{"/A#42C", "ABC"},
		{"/", ""},          // an empty name is legal
		{"/a#zz", "a#zz"},  // invalid escape is kept literally
		{"/x#4", "x#4"},    // truncated escape is kept literally
		{"/Tj", "Tj"},      // an operator name is a name, not an operator
		{"/F1/F2", "F1"},   // a delimiter ends the name
		{"/Sub(x)", "Sub"}, // ditto
	}
	for _, c := range cases {
		toks := lexAll(t, c.in)
		if len(toks) == 0 {
			t.Errorf("%q: no tokens", c.in)
			continue
		}
		n, ok := toks[0].Val.(objects.Name)
		if !ok {
			t.Errorf("%q -> %#v, want a Name", c.in, toks[0].Val)
			continue
		}
		if string(n) != c.want {
			t.Errorf("%q -> %q, want %q", c.in, n, c.want)
		}
	}
}

func TestLexLiteralStrings(t *testing.T) {
	cases := []struct{ in, want string }{
		{"(simple)", "simple"},
		{"()", ""},
		{"(nested (parens) here)", "nested (parens) here"},
		{"(deep ((a)) end)", "deep ((a)) end"},
		{`(esc \n\r\t\b\f)`, "esc \n\r\t\b\f"},
		{`(\(\)\\)`, `()\`},
		{`(\101\102\103)`, "ABC"}, // three-digit octal
		{`(\5)`, "\x05"},          // one-digit octal
		{`(\53)`, "+"},            // two-digit octal
		{`(\0053)`, "\x053"},      // stops at three digits
		// High-order overflow is discarded, per the note to §7.3.4.2 Table 3:
		// \777 is 511, which keeps only the low eight bits.
		{`(\777)`, "\xFF"},
		{`(\400)`, "\x00"},
		{`(\q)`, "q"},        // unknown escape drops the backslash
		{"(a\\\nb)", "ab"},   // line continuation, LF
		{"(a\\\r\nb)", "ab"}, // line continuation, CRLF
		{"(a\rb)", "a\nb"},   // raw CR becomes LF
		{"(a\r\nb)", "a\nb"}, // raw CRLF becomes one LF
		{"(a\nb)", "a\nb"},   // raw LF is kept
		{"(unterminated", "unterminated"},
		{`(trailing backslash \`, "trailing backslash "},
	}
	for _, c := range cases {
		toks := lexAll(t, c.in)
		if len(toks) == 0 {
			t.Errorf("%q: no tokens", c.in)
			continue
		}
		s, ok := toks[0].Val.(objects.String)
		if !ok {
			t.Errorf("%q -> %#v, want a String", c.in, toks[0].Val)
			continue
		}
		if string(s) != c.want {
			t.Errorf("%q -> %q, want %q", c.in, s, c.want)
		}
	}
}

func TestLexHexStrings(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<48656C6C6F>", "Hello"},
		{"<48 65 6c\n6C 6F>", "Hello"}, // whitespace between digits is legal
		{"<4>", "@"},                   // odd digit pads with zero
		{"<>", ""},
		{"<48!!65>", "He"},       // junk is skipped
		{"<48656C6C6F", "Hello"}, // unterminated
	}
	for _, c := range cases {
		toks := lexAll(t, c.in)
		if len(toks) == 0 {
			t.Errorf("%q: no tokens", c.in)
			continue
		}
		s, ok := toks[0].Val.(objects.String)
		if !ok {
			t.Errorf("%q -> %#v, want a String", c.in, toks[0].Val)
			continue
		}
		if string(s) != c.want {
			t.Errorf("%q -> %q, want %q", c.in, s, c.want)
		}
	}
}

func TestLexDictAndArrayPunctuation(t *testing.T) {
	toks := lexAll(t, "<< /A [1 2] >> BDC")
	kinds := []Kind{
		KindDictOpen, KindObject, KindArrayOpen, KindObject, KindObject,
		KindArrayClose, KindDictClose, KindOperator,
	}
	if len(toks) != len(kinds) {
		t.Fatalf("%d tokens, want %d: %#v", len(toks), len(kinds), toks)
	}
	for i, want := range kinds {
		if toks[i].Kind != want {
			t.Errorf("token %d kind = %v, want %v", i, toks[i].Kind, want)
		}
	}
}

func TestLexHexStringVersusDictOpen(t *testing.T) {
	// "<<" is a dict, "<" starts a hex string. Confusing them turns a BDC
	// property list into a garbage string.
	toks := lexAll(t, "<</MCID 3>>")
	if toks[0].Kind != KindDictOpen {
		t.Fatalf("first token = %v, want KindDictOpen", toks[0].Kind)
	}
	if toks[len(toks)-1].Kind != KindDictClose {
		t.Fatalf("last token = %v, want KindDictClose", toks[len(toks)-1].Kind)
	}
}

func TestLexComments(t *testing.T) {
	toks := ops(lexAll(t, "BT % this is a comment with (parens) and /names\nET"))
	if len(toks) != 2 || toks[0] != "BT" || toks[1] != "ET" {
		t.Fatalf("operators = %v, want [BT ET]", toks)
	}

	// A comment at the very end with no newline must not hang.
	toks = ops(lexAll(t, "ET % trailing"))
	if len(toks) != 1 || toks[0] != "ET" {
		t.Fatalf("operators = %v, want [ET]", toks)
	}
}

func TestLexSkipsStrayDelimiters(t *testing.T) {
	// A lone '>' and PostScript braces are malformed here. They must be skipped
	// without consuming the operators around them.
	toks := ops(lexAll(t, "BT > } { > ET"))
	if len(toks) != 2 || toks[0] != "BT" || toks[1] != "ET" {
		t.Fatalf("operators = %v, want [BT ET]", toks)
	}
}

func TestLexAlwaysTerminates(t *testing.T) {
	// Every byte value, alone and in pairs, must produce a lexer that advances.
	// A byte that neither matches a case nor is consumed would spin forever, and
	// the input here is untrusted.
	for i := 0; i < 256; i++ {
		src := []byte{byte(i)}
		l := NewLexer(src)
		for n := 0; ; n++ {
			if n > 8 {
				t.Fatalf("byte %#02x: lexer did not terminate", i)
			}
			if l.Next().Kind == KindEOF {
				break
			}
		}

		for j := 0; j < 256; j++ {
			pair := []byte{byte(i), byte(j)}
			l := NewLexer(pair)
			for n := 0; ; n++ {
				if n > 16 {
					t.Fatalf("bytes %#02x %#02x: lexer did not terminate", i, j)
				}
				if l.Next().Kind == KindEOF {
					break
				}
			}
		}
	}
}

func TestLexPosIsMonotonic(t *testing.T) {
	// Pos must advance across every token so a caller can record and resume, and
	// so no token is zero-width.
	src := "BT /F1 12 Tf [ (a) -20 (b) ] TJ <</MCID 0>> BDC 1.5 -2 Td ET"
	l := NewLexer([]byte(src))
	last := -1
	for {
		tok := l.Next()
		if tok.Kind == KindEOF {
			break
		}
		if tok.Pos <= last {
			t.Fatalf("token at Pos %d did not advance past %d", tok.Pos, last)
		}
		last = tok.Pos
	}
	if l.Pos() != len(src) {
		t.Fatalf("final Pos = %d, want %d", l.Pos(), len(src))
	}
}

func TestLexBinaryStringContent(t *testing.T) {
	// Text strings for a CID font hold arbitrary bytes, including delimiters and
	// NULs, escaped octally. They must survive verbatim.
	src := `(\000\050\051\134\377)`
	toks := lexAll(t, src)
	s, ok := toks[0].Val.(objects.String)
	if !ok {
		t.Fatalf("got %#v, want a String", toks[0].Val)
	}
	want := []byte{0x00, '(', ')', '\\', 0xFF}
	if string(s) != string(want) {
		t.Fatalf("got % x, want % x", s, want)
	}
}

func TestLexLongStreamDoesNotRecurse(t *testing.T) {
	// A stream of stray delimiters is skipped in a loop, not by recursion. With
	// recursion this many would exhaust the stack.
	src := strings.Repeat("> ", 200000) + "ET"
	toks := ops(lexAll(t, src))
	if len(toks) != 1 || toks[0] != "ET" {
		t.Fatalf("operators = %v, want [ET]", toks)
	}
}

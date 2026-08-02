package content

import "github.com/3rg0n/pdf-spec/objects"

// maxOperands caps the operand stack.
//
// The largest legitimate operator is a dash array or an inline-image dictionary,
// both small. A stream that pushes without ever emitting an operator is either
// corrupt or hostile, and without a cap it would grow until the process died.
// Dropping the oldest operands keeps the most recent, which is what an operator
// consumes.
const maxOperands = 512

// maxNestDepth bounds array and dictionary nesting inside a content stream.
const maxNestDepth = 64

// Op is one operator with its operands, in the order they appeared.
//
// Operands are only valid until the next call to Scanner.Next, which reuses the
// backing array. An operator that needs to retain them copies. This saves one
// slice allocation per operator, which is worth having across the millions an
// entire document runs, but it is not the whole cost: each operand value is
// still boxed into an objects.Object, and strings and names each own a buffer.
// See the note on Scanner.operands.
type Op struct {
	Name     string
	Operands []objects.Object
}

// Num returns operand i as a float, or 0 if absent or not numeric.
func (o Op) Num(i int) float64 {
	if i < 0 || i >= len(o.Operands) {
		return 0
	}
	f, _ := objects.AsNum(o.Operands[i])
	return f
}

// Int returns operand i as an int, or 0 if absent or not numeric.
func (o Op) Int(i int) int {
	return int(o.Num(i))
}

// Name returns operand i as a name, or "" if absent or not a name.
func (o Op) NameAt(i int) objects.Name {
	if i < 0 || i >= len(o.Operands) {
		return ""
	}
	n, _ := o.Operands[i].(objects.Name)
	return n
}

// Str returns operand i as a string, or nil if absent or not a string.
func (o Op) Str(i int) objects.String {
	if i < 0 || i >= len(o.Operands) {
		return nil
	}
	s, _ := o.Operands[i].(objects.String)
	return s
}

// Arr returns operand i as an array, or nil if absent or not an array.
func (o Op) Arr(i int) objects.Array {
	if i < 0 || i >= len(o.Operands) {
		return nil
	}
	a, _ := o.Operands[i].(objects.Array)
	return a
}

// Dict returns operand i as a dictionary, or nil if absent or not a dictionary.
func (o Op) Dict(i int) objects.Dict {
	if i < 0 || i >= len(o.Operands) {
		return nil
	}
	d, _ := o.Operands[i].(objects.Dict)
	return d
}

// Scanner groups a content stream's tokens into operators with their operands.
//
// It assembles nested arrays and dictionaries iteratively with an explicit
// stack, so a stream nesting a million arrays hits maxNestDepth instead of the
// call stack. Content streams are untrusted input.
type Scanner struct {
	lex *Lexer

	// operands is reused across operators: its capacity is retained and only the
	// length is reset, so the slice itself is allocated once per stream rather
	// than once per operator.
	//
	// Scanning is not allocation-free even so, and the residue is structural
	// rather than an oversight. Every operand is boxed into an objects.Object,
	// and a non-pointer value larger than a word costs a heap allocation to box;
	// literal strings and names each own their decoded bytes, which cannot be
	// shared because sibling operands coexist in one list — the two strings in
	// [(a) (b)] TJ are both live when TJ runs. Removing the boxing means changing
	// how objects.Object represents scalars, which is a decision for that package
	// and not something to work around here. The benchmark in bench_test.go is
	// the record of where this stands.
	operands []objects.Object

	// stack holds partially built containers during assembly.
	stack []container

	// inline holds the data of the most recent inline image, valid until the next
	// Next call.
	inline []byte
}

// container is one open array or dictionary being assembled.
type container struct {
	isDict bool
	items  []objects.Object
}

// NewScanner returns a Scanner over a decoded content stream.
func NewScanner(data []byte) *Scanner {
	return &Scanner{
		lex:      NewLexer(data),
		operands: make([]objects.Object, 0, 16),
	}
}

// InlineData returns the image data of the last BI/ID/EI operator scanned. It is
// valid until the next call to Next.
func (s *Scanner) InlineData() []byte { return s.inline }

// Next returns the next operator, or false at the end of the stream.
//
// Operands that appear without a following operator are discarded at EOF, which
// is what a truncated stream looks like.
func (s *Scanner) Next() (Op, bool) {
	s.operands = s.operands[:0]
	s.stack = s.stack[:0]
	s.inline = nil

	for {
		tok := s.lex.Next()
		switch tok.Kind {
		case KindEOF:
			return Op{}, false

		case KindObject:
			s.push(tok.Val)

		case KindArrayOpen:
			s.open(false)

		case KindDictOpen:
			s.open(true)

		case KindArrayClose:
			s.close(false)

		case KindDictClose:
			s.close(true)

		case KindOperator:
			// An operator arriving with containers still open means the stream is
			// malformed. Close them so their contents are not lost, then run the
			// operator: a missing ']' should not discard the operator's operands.
			for len(s.stack) > 0 {
				s.close(s.stack[len(s.stack)-1].isDict)
			}
			if tok.Op == "BI" {
				if op, ok := s.inlineImage(); ok {
					return op, true
				}
				continue
			}
			return Op{Name: tok.Op, Operands: s.operands}, true
		}
	}
}

// push adds a completed object to the innermost open container, or to the
// operand stack when nothing is open.
func (s *Scanner) push(o objects.Object) {
	if n := len(s.stack); n > 0 {
		top := &s.stack[n-1]
		if len(top.items) < maxOperands {
			top.items = append(top.items, o)
		}
		return
	}
	if len(s.operands) >= maxOperands {
		// Drop the oldest. The operator about to run reads the most recent.
		copy(s.operands, s.operands[1:])
		s.operands = s.operands[:len(s.operands)-1]
	}
	s.operands = append(s.operands, o)
}

func (s *Scanner) open(isDict bool) {
	if len(s.stack) >= maxNestDepth {
		// At the limit, treat further opens as content rather than growing. The
		// contents still reach the operand stack, just unnested.
		return
	}
	s.stack = append(s.stack, container{isDict: isDict})
}

// close finalizes the innermost container. A mismatched closer (']' for a dict)
// still closes, because the alternative is discarding everything after it.
func (s *Scanner) close(isDict bool) {
	n := len(s.stack)
	if n == 0 {
		return
	}
	top := s.stack[n-1]
	s.stack = s.stack[:n-1]

	if top.isDict && isDict {
		s.push(toDict(top.items))
		return
	}
	if !top.isDict && !isDict {
		s.push(objects.Array(top.items))
		return
	}
	// Mismatched: keep the data in whichever shape the opener declared.
	if top.isDict {
		s.push(toDict(top.items))
	} else {
		s.push(objects.Array(top.items))
	}
}

// toDict pairs a flat item list into a dictionary. A trailing unpaired key and
// any non-name key are dropped, which is the only sane reading of a malformed
// dictionary.
func toDict(items []objects.Object) objects.Dict {
	d := make(objects.Dict, len(items)/2)
	for i := 0; i+1 < len(items); i += 2 {
		if k, ok := items[i].(objects.Name); ok {
			d[k] = items[i+1]
		}
	}
	return d
}

// inlineImage handles BI ... ID <data> EI (§8.9.7).
//
// This needs custom scanning because the bytes between ID and EI are raw image
// data, not tokens: they routinely contain sequences that would otherwise lex as
// operators and corrupt everything downstream.
func (s *Scanner) inlineImage() (Op, bool) {
	dict := objects.Dict{}
	var pending objects.Name
	havePending := false

	for {
		tok := s.lex.Next()
		switch tok.Kind {
		case KindEOF:
			return Op{}, false
		case KindObject:
			if !havePending {
				if n, ok := tok.Val.(objects.Name); ok {
					pending, havePending = n, true
				}
				continue
			}
			dict[pending] = tok.Val
			havePending = false
		case KindArrayOpen, KindDictOpen:
			// A value that is a container: assemble it with the normal machinery.
			s.stack = s.stack[:0]
			s.operands = s.operands[:0]
			s.open(tok.Kind == KindDictOpen)
			for len(s.stack) > 0 {
				t2 := s.lex.Next()
				switch t2.Kind {
				case KindEOF:
					return Op{}, false
				case KindObject:
					s.push(t2.Val)
				case KindArrayOpen:
					s.open(false)
				case KindDictOpen:
					s.open(true)
				case KindArrayClose:
					s.close(false)
				case KindDictClose:
					s.close(true)
				}
			}
			if havePending && len(s.operands) > 0 {
				dict[pending] = s.operands[len(s.operands)-1]
				havePending = false
			}
		case KindOperator:
			if tok.Op != "ID" {
				// Anything else before ID is malformed; ignore it.
				continue
			}
			s.inline = s.readInlineData()
			s.operands = s.operands[:0]
			s.operands = append(s.operands, dict)
			return Op{Name: "INLINE_IMAGE", Operands: s.operands}, true
		}
	}
}

// readInlineData consumes raw bytes from just after ID to the matching EI.
//
// Exactly one whitespace byte follows ID and is not data. Finding the end means
// searching for "EI" at a token boundary, because the data can contain those two
// bytes; requiring surrounding whitespace and a plausible continuation is the
// same heuristic viewers use.
func (s *Scanner) readInlineData() []byte {
	if s.lex.pos < len(s.lex.data) && isSpace[s.lex.data[s.lex.pos]] {
		s.lex.pos++
	}
	start := s.lex.pos
	d := s.lex.data

	for i := start; i+1 < len(d); i++ {
		if d[i] != 'E' || d[i+1] != 'I' {
			continue
		}
		// Must be preceded by whitespace.
		if i == start || !isSpace[d[i-1]] {
			continue
		}
		// Must be followed by whitespace or end of stream, so that a byte pair
		// inside compressed data is not mistaken for the terminator.
		if i+2 < len(d) && !isSpace[d[i+2]] && !isDelim[d[i+2]] {
			continue
		}
		data := d[start : i-1]
		s.lex.pos = i + 2
		return data
	}
	// No terminator: the rest of the stream is data.
	s.lex.pos = len(d)
	return d[start:]
}

package content

import (
	"bytes"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/objects"
)

// scanned is a copy of one Op, since Scanner reuses its operand backing array.
type scanned struct {
	name     string
	operands []objects.Object
	inline   []byte
}

// scanAll drains a scanner, copying each operator's operands out. The copy is the
// point: retaining the slices would silently alias the reused array, so a test
// that forgot to copy would pass or fail unpredictably.
func scanAll(t *testing.T, src string) []scanned {
	t.Helper()
	sc := NewScanner([]byte(src))
	var out []scanned
	for i := 0; ; i++ {
		if i > 100000 {
			t.Fatal("scanner did not terminate")
		}
		op, ok := sc.Next()
		if !ok {
			return out
		}
		s := scanned{name: op.Name, operands: append([]objects.Object(nil), op.Operands...)}
		if d := sc.InlineData(); d != nil {
			s.inline = append([]byte(nil), d...)
		}
		out = append(out, s)
	}
}

func TestScanOperandsThenOperator(t *testing.T) {
	got := scanAll(t, "1 0 0 1 72 720 cm BT /F1 12 Tf (hi) Tj ET")
	want := []struct {
		name  string
		nargs int
	}{
		{"cm", 6}, {"BT", 0}, {"Tf", 2}, {"Tj", 1}, {"ET", 0},
	}
	if len(got) != len(want) {
		t.Fatalf("%d operators, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].name != w.name || len(got[i].operands) != w.nargs {
			t.Errorf("op %d = %s/%d, want %s/%d",
				i, got[i].name, len(got[i].operands), w.name, w.nargs)
		}
	}
}

func TestScanOperandsResetBetweenOperators(t *testing.T) {
	// Leftover operands from a previous operator would corrupt the next one's
	// argument positions, which is a silent misplacement of every glyph after.
	got := scanAll(t, "1 2 3 4 5 6 cm BT ET 7 8 Td")
	if len(got) != 4 {
		t.Fatalf("%d operators, want 4", len(got))
	}
	if len(got[1].operands) != 0 {
		t.Errorf("BT kept %d operands", len(got[1].operands))
	}
	if len(got[2].operands) != 0 {
		t.Errorf("ET kept %d operands", len(got[2].operands))
	}
	if len(got[3].operands) != 2 {
		t.Errorf("Td got %d operands, want 2", len(got[3].operands))
	}
}

func TestScanAccessors(t *testing.T) {
	sc := NewScanner([]byte("3.5 -2 /Name (str) [1 2] <</K (v)>> XX"))
	op, ok := sc.Next()
	if !ok {
		t.Fatal("no operator")
	}
	if op.Name != "XX" {
		t.Fatalf("name = %q", op.Name)
	}
	if op.Num(0) != 3.5 {
		t.Errorf("Num(0) = %v, want 3.5", op.Num(0))
	}
	if op.Int(1) != -2 {
		t.Errorf("Int(1) = %v, want -2", op.Int(1))
	}
	if op.NameAt(2) != "Name" {
		t.Errorf("NameAt(2) = %q", op.NameAt(2))
	}
	if string(op.Str(3)) != "str" {
		t.Errorf("Str(3) = %q", op.Str(3))
	}
	if len(op.Arr(4)) != 2 {
		t.Errorf("Arr(4) = %v", op.Arr(4))
	}
	if d := op.Dict(5); d == nil || string(d["K"].(objects.String)) != "v" {
		t.Errorf("Dict(5) = %v", d)
	}

	// Out-of-range and wrong-type reads return zero values rather than panicking:
	// a malformed stream supplies the wrong operand count constantly, and the
	// alternative is a bounds check at every call site.
	if op.Num(99) != 0 || op.Int(-1) != 0 || op.NameAt(99) != "" ||
		op.Str(99) != nil || op.Arr(99) != nil || op.Dict(99) != nil {
		t.Error("out-of-range accessors should return zero values")
	}
	if op.Num(2) != 0 {
		t.Error("Num on a name should be 0")
	}
	if op.NameAt(0) != "" {
		t.Error("NameAt on a number should be empty")
	}
}

func TestScanNestedContainers(t *testing.T) {
	got := scanAll(t, "[(a) -20 (b) [1 [2 [3]]]] TJ")
	if len(got) != 1 {
		t.Fatalf("%d operators, want 1", len(got))
	}
	arr, ok := got[0].operands[0].(objects.Array)
	if !ok {
		t.Fatalf("operand 0 = %#v, want an Array", got[0].operands[0])
	}
	if len(arr) != 4 {
		t.Fatalf("outer array has %d items, want 4", len(arr))
	}
	inner, ok := arr[3].(objects.Array)
	if !ok || len(inner) != 2 {
		t.Fatalf("nested array = %#v", arr[3])
	}
}

func TestScanDictOperand(t *testing.T) {
	got := scanAll(t, "/Span <</MCID 3 /Lang (en) /Nested <</A 1>> >> BDC")
	if len(got) != 1 || got[0].name != "BDC" {
		t.Fatalf("got %v", got)
	}
	d, ok := got[0].operands[1].(objects.Dict)
	if !ok {
		t.Fatalf("operand 1 = %#v, want a Dict", got[0].operands[1])
	}
	if d["MCID"] != objects.Int(3) {
		t.Errorf("MCID = %#v", d["MCID"])
	}
	if string(d["Lang"].(objects.String)) != "en" {
		t.Errorf("Lang = %#v", d["Lang"])
	}
	if _, ok := d["Nested"].(objects.Dict); !ok {
		t.Errorf("Nested = %#v", d["Nested"])
	}
}

func TestScanMalformedDictDropsBadKeys(t *testing.T) {
	// A non-name key and a trailing unpaired key are the only readings that make
	// sense; the alternative is discarding the whole property list, which loses
	// the MCID that joins this content to the structure tree.
	got := scanAll(t, "<< /MCID 5 (notakey) 1 /Trailing >> BDC")
	d, ok := got[0].operands[0].(objects.Dict)
	if !ok {
		t.Fatalf("operand 0 = %#v", got[0].operands[0])
	}
	if d["MCID"] != objects.Int(5) {
		t.Errorf("MCID lost: %#v", d)
	}
	if _, present := d["Trailing"]; present {
		t.Error("unpaired trailing key should be dropped")
	}
}

func TestScanUnclosedContainerStillRunsOperator(t *testing.T) {
	// A missing ']' must not discard the operator or its operands. Losing a TJ
	// here loses a whole line of text.
	got := scanAll(t, "[(a) (b) TJ")
	if len(got) != 1 {
		t.Fatalf("%d operators, want 1: %v", len(got), got)
	}
	if got[0].name != "TJ" {
		t.Fatalf("name = %q, want TJ", got[0].name)
	}
	arr, ok := got[0].operands[0].(objects.Array)
	if !ok || len(arr) != 2 {
		t.Fatalf("operand 0 = %#v, want a 2-item Array", got[0].operands[0])
	}
}

func TestScanMismatchedCloserKeepsOpenerShape(t *testing.T) {
	// '<<' closed by ']' still yields a dict, because the opener declared the
	// shape and the contents are key-value pairs.
	got := scanAll(t, "<< /MCID 1 ] BDC")
	if len(got) != 1 {
		t.Fatalf("%d operators, want 1", len(got))
	}
	if _, ok := got[0].operands[0].(objects.Dict); !ok {
		t.Fatalf("operand 0 = %#v, want a Dict", got[0].operands[0])
	}
}

func TestScanStrayCloserIsIgnored(t *testing.T) {
	got := scanAll(t, "] >> 1 2 Td")
	if len(got) != 1 || got[0].name != "Td" || len(got[0].operands) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestScanDeepNestingIsBounded(t *testing.T) {
	// Past maxNestDepth further opens stop nesting rather than growing. With
	// recursive assembly this input would exhaust the stack instead.
	src := strings.Repeat("[", 100000) + " 1 Tj"
	got := scanAll(t, src)
	if len(got) != 1 || got[0].name != "Tj" {
		t.Fatalf("got %d operators: %v", len(got), got)
	}
}

func TestScanManyOperandsAreBounded(t *testing.T) {
	// A stream that pushes without emitting an operator must not grow without
	// limit. The most recent operands are the ones an operator reads, so the
	// oldest are dropped.
	src := strings.Repeat("1 ", maxOperands*4) + "Td"
	got := scanAll(t, src)
	if len(got) != 1 {
		t.Fatalf("%d operators, want 1", len(got))
	}
	if n := len(got[0].operands); n > maxOperands {
		t.Fatalf("%d operands retained, want at most %d", n, maxOperands)
	}
}

func TestScanTrailingOperandsDiscardedAtEOF(t *testing.T) {
	got := scanAll(t, "BT ET 1 2 3")
	if len(got) != 2 {
		t.Fatalf("%d operators, want 2: %v", len(got), got)
	}
}

func TestScanInlineImage(t *testing.T) {
	// Raw image data between ID and EI is not tokenized. Data containing "BT",
	// "(", and a bare "EI" without surrounding whitespace must not end it early
	// or leak operators into the stream.
	data := "\x00\xFFBT (\x01EIx\x02 \x03"
	src := "BI /W 4 /H 4 /BPC 8 /CS /G /F /Fl ID " + data + " EI Q"

	got := scanAll(t, src)
	if len(got) != 2 {
		t.Fatalf("%d operators, want 2 (INLINE_IMAGE, Q): %v", len(got), got)
	}
	if got[0].name != "INLINE_IMAGE" {
		t.Fatalf("op 0 = %q, want INLINE_IMAGE", got[0].name)
	}
	if got[1].name != "Q" {
		t.Fatalf("op 1 = %q, want Q", got[1].name)
	}
	if !bytes.Equal(got[0].inline, []byte(data)) {
		t.Errorf("inline data = %q, want %q", got[0].inline, data)
	}

	d, ok := got[0].operands[0].(objects.Dict)
	if !ok {
		t.Fatalf("operand 0 = %#v, want a Dict", got[0].operands[0])
	}
	if d["W"] != objects.Int(4) || d["H"] != objects.Int(4) {
		t.Errorf("dimensions lost: %#v", d)
	}
	if d["CS"] != objects.Name("G") {
		t.Errorf("CS = %#v", d["CS"])
	}
}

func TestScanInlineImageWithArrayValue(t *testing.T) {
	// /D and /F can be arrays. The dictionary must survive with the array intact.
	src := "BI /W 2 /H 2 /D [1 0] /F [/AHx /Fl] ID 00 EI"
	got := scanAll(t, src)
	if len(got) != 1 || got[0].name != "INLINE_IMAGE" {
		t.Fatalf("got %v", got)
	}
	d := got[0].operands[0].(objects.Dict)
	if a, ok := d["D"].(objects.Array); !ok || len(a) != 2 {
		t.Errorf("D = %#v", d["D"])
	}
	if a, ok := d["F"].(objects.Array); !ok || len(a) != 2 {
		t.Errorf("F = %#v", d["F"])
	}
	if d["W"] != objects.Int(2) {
		t.Errorf("W = %#v", d["W"])
	}
}

func TestScanInlineImageUnterminated(t *testing.T) {
	// No EI: the rest of the stream is data. It must not hang or panic.
	got := scanAll(t, "BI /W 1 /H 1 ID \x00\x01\x02")
	if len(got) != 1 || got[0].name != "INLINE_IMAGE" {
		t.Fatalf("got %v", got)
	}
}

func TestScanInlineImageTruncatedBeforeID(t *testing.T) {
	// EOF inside the dictionary yields no operator rather than a hang.
	got := scanAll(t, "BI /W 1 /H")
	if len(got) != 0 {
		t.Fatalf("got %v, want no operators", got)
	}
}

func TestScanInlineImageDataIsNotAliasedAcrossOps(t *testing.T) {
	// InlineData is documented as valid only until the next Next. Verify it is
	// actually cleared, so a caller that ignores the contract gets nil rather
	// than a stale image.
	sc := NewScanner([]byte("BI /W 1 /H 1 ID \x00 EI Q"))
	if _, ok := sc.Next(); !ok {
		t.Fatal("no inline image")
	}
	if sc.InlineData() == nil {
		t.Fatal("inline data missing")
	}
	if _, ok := sc.Next(); !ok {
		t.Fatal("no Q")
	}
	if sc.InlineData() != nil {
		t.Error("inline data should be cleared by the next operator")
	}
}

func TestScanEmptyAndWhitespaceOnly(t *testing.T) {
	for _, src := range []string{"", "   ", "\n\r\t", "% comment only"} {
		if got := scanAll(t, src); len(got) != 0 {
			t.Errorf("%q yielded %v", src, got)
		}
	}
}

func TestScanOperandsAliasAcrossNext(t *testing.T) {
	// Document the aliasing contract as a test: two operators' operand slices
	// share a backing array, so a caller that retains the first sees the second's
	// data. This is intentional and load-bearing for allocation-free scanning; if
	// it ever stops being true the doc comment on Op should change with it.
	sc := NewScanner([]byte("1 Tj 2 Tj"))
	first, _ := sc.Next()
	retained := first.Operands
	sc.Next()
	if retained[0] != objects.Int(2) {
		t.Skip("Scanner no longer reuses its operand array; update Op's doc comment")
	}
}

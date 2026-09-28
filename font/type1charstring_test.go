package font

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/t1build"
)

// t1Run returns a charRun with subrs available for callsubr, for tests that drive t1Glyph or
// t1State.exec directly and never need a full parsed font.
func t1Run(budget int, subrs map[int][]byte) *charRun {
	return &charRun{budget: budget, t1: &Type1{subrs: subrs}}
}

// t1Exec runs cs from a fresh t1State with no font behind it, for tests that pin the interpreter's
// own mechanics -- the way charstring_test.go drives charState.exec directly for CFF.
func t1Exec(budget int, cs []byte) (bool, error) {
	s := &t1State{charState: charState{r: newCharRun(budget)}}
	return s.exec(cs, 0)
}

// TestT1PathOperators pins each accepted operator's geometry by hand from chapter 6 of the Adobe
// Type 1 Font Format, the way charstring_test.go pins CFF's. Every charstring begins with hsbw,
// since every path operator but hsbw/sbw/div requires it to have run.
func TestT1PathOperators(t *testing.T) {
	for _, c := range []struct {
		name string
		cs   []byte
		want Outline
	}{
		{
			"rlineto",
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 10, 20, t1build.RLineTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{10, 20}}},
				{Op: SegClose},
			},
		},
		{
			"hlineto",
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 50, t1build.HLineTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{50, 0}}},
				{Op: SegClose},
			},
		},
		{
			"vlineto",
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 50, t1build.VLineTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{0, 50}}},
				{Op: SegClose},
			},
		},
		{
			"rrcurveto",
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo,
				10, 0, 10, 10, 0, 10, t1build.RRCurveTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{10, 0}, {20, 10}, {20, 20}}},
				{Op: SegClose},
			},
		},
		{
			// vhcurveto: 0 dy1 dx2 dy2 dx3 0 -- starts vertical, ends horizontal.
			"vhcurveto",
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo,
				6, 2, 3, 7, t1build.VHCurveTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{0, 6}, {2, 9}, {9, 9}}},
				{Op: SegClose},
			},
		},
		{
			// hvcurveto: dx1 0 dx2 dy2 0 dy3 -- starts horizontal, ends vertical.
			"hvcurveto",
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo,
				6, 2, 3, 7, t1build.HVCurveTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{6, 0}, {8, 3}, {8, 10}}},
				{Op: SegClose},
			},
		},
		{
			"rmoveto then rlineto",
			t1build.CS(0, 500, t1build.HSBW, 5, 7, t1build.RMoveTo, 1, 1, t1build.RLineTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{5, 7}}},
				{Op: SegLine, P: [3]Point{{6, 8}}},
				{Op: SegClose},
			},
		},
		{
			"hmoveto then rlineto",
			t1build.CS(0, 500, t1build.HSBW, 5, t1build.HMoveTo, 1, 1, t1build.RLineTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{5, 0}}},
				{Op: SegLine, P: [3]Point{{6, 1}}},
				{Op: SegClose},
			},
		},
		{
			"vmoveto then rlineto",
			t1build.CS(0, 500, t1build.HSBW, 5, t1build.VMoveTo, 1, 1, t1build.RLineTo, t1build.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 5}}},
				{Op: SegLine, P: [3]Point{{1, 6}}},
				{Op: SegClose},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newCharRun(1000)
			if err := r.t1Glyph(c.cs, 0, 0); err != nil {
				t.Fatalf("t1Glyph: %v", err)
			}
			if !sameOutline(r.out, c.want) {
				t.Errorf("outline = %v\nwant     %v", r.out, c.want)
			}
		})
	}
}

// TestT1OperandOneByteUpperBound pins §6.2's one-byte operand form up to its own top: byte 246 is
// the last one-byte code and decodes to 107 (246-139), not to the first byte of a two-byte operand.
func TestT1OperandOneByteUpperBound(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, t1build.Raw{246}, t1build.VMoveTo, 10, 0, t1build.RLineTo, t1build.EndChar)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{0, 107}}},
		{Op: SegLine, P: [3]Point{{10, 107}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1OperandTwoBytePositiveBoundary pins §6.2's positive two-byte form at its own top: lead byte
// 250 is still positive (247..250 add, 251..254 negate), decoding to 876, not to -148.
func TestT1OperandTwoBytePositiveBoundary(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, 876, t1build.VMoveTo, 10, 0, t1build.RLineTo, t1build.EndChar)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{0, 876}}},
		{Op: SegLine, P: [3]Point{{10, 876}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1OperandFourByteAtEnd pins that the four-byte (255) operand form is read in full even when
// its last byte is the charstring's own last byte: p+4 == len(cs) is enough room, not one more. The
// charstring still ends without endchar or return right after, since nothing follows the operand,
// but that is errNoEnd -- not the truncation refusal a short read would give.
func TestT1OperandFourByteAtEnd(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, t1build.Raw{255, 0, 0, 0, 10})
	r := newCharRun(1000)
	err := r.t1Glyph(cs, 0, 0)
	if !errors.Is(err, errNoEnd) {
		t.Errorf("t1Glyph = %v, want errNoEnd (the operand decoded; nothing closed the charstring)", err)
	}
}

// TestT1EscapedOperatorAtEnd pins that an escaped operator's lead byte (12) with no second byte
// following is refused cleanly, rather than reading one past the charstring's own end.
func TestT1EscapedOperatorAtEnd(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, t1build.Raw{12})
	r := newCharRun(1000)
	err := r.t1Glyph(cs, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "charstring ends inside an escaped operator") {
		t.Errorf("t1Glyph = %v, want a refusal for an escaped operator with no second byte", err)
	}
}

// TestT1LargeIntBoundaryIsUsable pins that an operand of exactly maxT1Int (32000) is not "large":
// §6.2 refuses only an integer *past* the limit as a non-div operand, so 32000 itself must draw like
// any ordinary coordinate.
func TestT1LargeIntBoundaryIsUsable(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 32000, 0, t1build.RLineTo, t1build.EndChar)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{0, 0}}},
		{Op: SegLine, P: [3]Point{{32000, 0}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1LargeIntPastBoundaryIsRefused pins the other side of maxT1Int: 32001, one past the limit,
// is refused as a non-div operand.
func TestT1LargeIntPastBoundaryIsRefused(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 32001, 0, t1build.RLineTo, t1build.EndChar)
	r := newCharRun(1000)
	err := r.t1Glyph(cs, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "an integer past ±32000") {
		t.Errorf("t1Glyph = %v, want a refusal for an operand past ±32000 used by a non-div operator", err)
	}
}

// TestT1LargeIntDoesNotLeakIntoSubroutine pins that calling a subroutine clears the pending
// large-integer flag (FreeType's large_int): a div-of-div-result inside the subroutine is judged by
// the subroutine's own history, not by a large integer the calling context had just divided down.
//
// The caller's own div leaves its result (fixed, 40000) sitting under the subroutine number on the
// shared argument stack -- callsubr only pops the number -- so the subroutine sees it as its own
// stack[0]. The subroutine divides that inherited value by a fresh div result of its own, which is
// exactly a div of a div result: refused if largeInt leaked in, fine if the call reset it.
func TestT1LargeIntDoesNotLeakIntoSubroutine(t *testing.T) {
	subrs := map[int][]byte{
		0: t1build.CS(100, 5, t1build.Div, t1build.Div, t1build.VMoveTo, t1build.Return),
	}
	cs := t1build.CS(0, 500, t1build.HSBW, 40000, 1, t1build.Div, 0, t1build.CallSubr, t1build.EndChar)
	r := t1Run(1000, subrs)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Errorf("t1Glyph = %v, want no error: the subroutine's own div-of-div-results has nothing "+
			"pending from the caller", err)
	}
}

// TestT1SubrDepthBoundary pins maxT1SubrDepth's own edge with every level defined and able to draw,
// unlike TestT1CallSubrAndReturn's "more than 8 deep" case, whose final callsubr targets an
// undefined subroutine: a mutant that checks the wrong depth still refuses there, one level later,
// with the same message, so it takes a chain where the extra level would otherwise succeed to tell
// the depths apart.
func TestT1SubrDepthBoundary(t *testing.T) {
	const n = maxT1SubrDepth + 1 // one past the limit, every level defined
	subrs := map[int][]byte{}
	for i := 0; i < n-1; i++ {
		subrs[i] = t1build.CS(i+1, t1build.CallSubr, t1build.Return)
	}
	subrs[n-1] = t1build.CS(10, 0, t1build.RLineTo, t1build.Return)
	cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, t1build.CallSubr, t1build.EndChar)
	r := t1Run(1000, subrs)
	err := r.t1Glyph(cs, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "nest more than 8 deep") {
		t.Errorf("t1Glyph = %v, want a refusal for the ninth nested call, at exactly the eighth level", err)
	}
}

// TestT1StemsAtLimitIsAccepted pins the other side of TestT1CharstringRefusals's "more than 96
// stems": exactly maxStems hints is not yet "past the limit" and must draw.
func TestT1StemsAtLimitIsAccepted(t *testing.T) {
	var ops []any
	ops = append(ops, 0, 500, t1build.HSBW)
	for i := 0; i < maxStems; i++ {
		ops = append(ops, 10, 20, t1build.HStem)
	}
	cs := t1build.CS(append(ops, t1build.EndChar)...)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Errorf("t1Glyph = %v, want no error: exactly maxStems hints is not \"past the limit\"", err)
	}
}

// TestT1DotsectionDuringFlex pins that dotsection, like the moves and the calls, is allowed between
// a flex's othersubr calls: FreeType's own flex-allowed set includes it.
func TestT1DotsectionDuringFlex(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo,
		0, 1, t1build.CallOtherSubr, // start the flex
		t1build.DotSection)
	r := newCharRun(1000)
	err := r.t1Glyph(cs, 0, 0)
	if !errors.Is(err, errNoEnd) {
		t.Errorf("t1Glyph = %v, want errNoEnd (dotsection is allowed during a flex, not refused)", err)
	}
}

// TestT1DivDuringFlex pins that div, like the moves and the calls, is allowed between a flex's
// othersubr calls: a producer may compute a move operand with it mid-flex.
func TestT1DivDuringFlex(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo,
		0, 1, t1build.CallOtherSubr, // start the flex
		100, 4, t1build.Div)
	r := newCharRun(1000)
	err := r.t1Glyph(cs, 0, 0)
	if !errors.Is(err, errNoEnd) {
		t.Errorf("t1Glyph = %v, want errNoEnd (div is allowed during a flex, not refused)", err)
	}
}

// TestT1FlexStartRefusesBeforeMoveto pins that starting a flex is a path operator like any other:
// othersubr 1 needs a defined current point just as rlineto does, before any moveto has run.
func TestT1FlexStartRefusesBeforeMoveto(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW, 0, 1, t1build.CallOtherSubr)
	r := newCharRun(1000)
	err := r.t1Glyph(cs, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "path operator before the first moveto") {
		t.Errorf("t1Glyph = %v, want a refusal for starting a flex before any moveto", err)
	}
}

// TestT1MovetoClosesThePreviousContour pins that an ordinary moveto closes whatever contour is open,
// the way closepath does explicitly: a multi-contour glyph's subpaths are each closed on their own,
// not left as one continuous unclosed path.
func TestT1MovetoClosesThePreviousContour(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW,
		0, 0, t1build.RMoveTo, 50, 0, t1build.RLineTo,
		200, 200, t1build.RMoveTo, 10, 10, t1build.RLineTo,
		t1build.EndChar)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{0, 0}}},
		{Op: SegLine, P: [3]Point{{50, 0}}},
		{Op: SegClose},
		// The second rmoveto's (200,200) is a delta from the pen the first contour left at (50,0).
		{Op: SegMove, P: [3]Point{{250, 200}}},
		{Op: SegLine, P: [3]Point{{260, 210}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1ClosepathResetAfterDrawingFromStart pins that drawing from the point closepath left the pen
// at clears "closed" for the segment that follows it: a second draw op in the same contour must not
// be checked against the old start point again.
func TestT1ClosepathResetAfterDrawingFromStart(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW,
		100, 100, t1build.RMoveTo,
		100, 0, t1build.RLineTo, 0, 100, t1build.RLineTo, -100, 0, t1build.RLineTo, 0, -100, t1build.RLineTo,
		t1build.ClosePath,
		50, 0, t1build.RLineTo,
		0, 50, t1build.RLineTo,
		t1build.EndChar)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{100, 100}}},
		{Op: SegLine, P: [3]Point{{200, 100}}},
		{Op: SegLine, P: [3]Point{{200, 200}}},
		{Op: SegLine, P: [3]Point{{100, 200}}},
		{Op: SegLine, P: [3]Point{{100, 100}}},
		{Op: SegClose},
		{Op: SegMove, P: [3]Point{{100, 100}}},
		{Op: SegLine, P: [3]Point{{150, 100}}},
		{Op: SegLine, P: [3]Point{{150, 150}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1OutlineGIDAtBoundary pins Outline's bounds check at gid == len(glyphs), one past the last
// valid index, distinct from TestT1OutlineGIDOutOfRange's gid clearly beyond the font.
func TestT1OutlineGIDAtBoundary(t *testing.T) {
	tf := &Type1{glyphs: [][]byte{t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)}}
	_, _, err := tf.Outline(1, 100)
	if err == nil || !strings.Contains(err.Error(), "Type 1 glyph 1 of 1") {
		t.Errorf("Outline(1) = %v, want a refusal naming \"Type 1 glyph 1 of 1\"", err)
	}
}

// TestT1SeacResetsInSeacForTheNextGlyph pins that seac's inSeac flag is cleared once its two
// components have drawn, so a later glyph run through the same charRun -- as a page's glyphs share
// no charRun today, but the flag is charRun-scoped and this is what keeps it that way -- can itself
// use seac rather than being refused by a flag an earlier glyph left set.
//
// The components draw nothing (hsbw then endchar, no moveto), so r.out stays empty after the first
// seac: the "seac after the glyph has drawn" guard, which a fresh t1State also passes since moved
// starts false again, cannot be what refuses a second seac on the same run. Only a leaked inSeac can.
func TestT1SeacResetsInSeacForTheNextGlyph(t *testing.T) {
	empty := t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)
	b := t1build.Builder{Glyphs: []t1build.Glyph{
		{Name: ".notdef", CS: empty},
		{Name: "A", CS: empty},
		{Name: "B", CS: empty},
	}}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	seacCS := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, 66, t1build.Seac)

	r := &charRun{budget: 10000, t1: tf}
	if err := r.t1Glyph(seacCS, 0, 0); err != nil {
		t.Fatalf("first t1Glyph (seac): %v", err)
	}
	if err := r.t1Glyph(seacCS, 0, 0); err != nil {
		t.Errorf("second t1Glyph (seac) on the same run = %v, want success: inSeac must reset after "+
			"the first", err)
	}
}

// TestT1StemsAndDotsection pins that hstem, vstem, vstem3, hstem3 and dotsection all count stems (3
// each for the *3 forms) and draw nothing.
func TestT1StemsAndDotsection(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW,
		10, 20, t1build.HStem,
		10, 20, t1build.VStem,
		10, 20, 10, 20, 10, 20, t1build.VStem3,
		10, 20, 10, 20, 10, 20, t1build.HStem3,
		t1build.DotSection,
		t1build.EndChar)
	s := &t1State{charState: charState{r: newCharRun(1000)}}
	if _, err := s.exec(cs, 0); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if s.stems != 1+1+3+3 {
		t.Errorf("stems = %d, want 8 (1+1+3+3)", s.stems)
	}
	if len(s.r.out) != 0 {
		t.Errorf("out = %v, want nothing drawn", s.r.out)
	}
}

// TestT1ClosepathThenMoveto pins that a moveto after closepath just repositions the pen: no second
// close is emitted, and the new moveto draws nothing of its own until something else does.
func TestT1ClosepathThenMoveto(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW,
		0, 0, t1build.RMoveTo, 50, 0, t1build.RLineTo, t1build.ClosePath,
		10, 10, t1build.RMoveTo, t1build.EndChar)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{0, 0}}},
		{Op: SegLine, P: [3]Point{{50, 0}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1ClosepathThenDrawFromStart pins that a path operator right after closepath is allowed when
// the pen is still exactly where the contour started -- closepath does not move the pen -- and
// reopens the contour there rather than treating the closed point as unreachable.
func TestT1ClosepathThenDrawFromStart(t *testing.T) {
	cs := t1build.CS(0, 500, t1build.HSBW,
		100, 100, t1build.RMoveTo,
		100, 0, t1build.RLineTo, 0, 100, t1build.RLineTo, -100, 0, t1build.RLineTo, 0, -100, t1build.RLineTo,
		t1build.ClosePath,
		50, 0, t1build.RLineTo,
		t1build.EndChar)
	r := newCharRun(1000)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{100, 100}}},
		{Op: SegLine, P: [3]Point{{200, 100}}},
		{Op: SegLine, P: [3]Point{{200, 200}}},
		{Op: SegLine, P: [3]Point{{100, 200}}},
		{Op: SegLine, P: [3]Point{{100, 100}}},
		{Op: SegClose},
		{Op: SegMove, P: [3]Point{{100, 100}}},
		{Op: SegLine, P: [3]Point{{150, 100}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1SBW pins that sbw places the pen at (sbx, sby), unlike hsbw which only ever moves it in x.
func TestT1SBW(t *testing.T) {
	cs := t1build.CS(20, 30, 700, 0, t1build.SBW, t1build.EndChar)
	s := &t1State{charState: charState{r: newCharRun(1000)}}
	if _, err := s.exec(cs, 0); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if s.x != 20 || s.y != 30 {
		t.Errorf("pen = (%g, %g), want (20, 30)", s.x, s.y)
	}
}

// TestT1CallSubrAndReturn pins subroutine nesting, exactly to the 8-deep limit maxT1SubrDepth
// names, and a subroutine that itself ends the glyph with endchar.
func TestT1CallSubrAndReturn(t *testing.T) {
	t.Run("one level of nesting", func(t *testing.T) {
		subrs := map[int][]byte{0: t1build.CS(10, 0, t1build.RLineTo, t1build.Return)}
		cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, t1build.CallSubr, t1build.EndChar)
		r := t1Run(1000, subrs)
		if err := r.t1Glyph(cs, 0, 0); err != nil {
			t.Fatalf("t1Glyph: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(r.out, want) {
			t.Errorf("outline = %v\nwant     %v", r.out, want)
		}
	})

	t.Run("nesting exactly 8 deep draws", func(t *testing.T) {
		const n = maxT1SubrDepth
		subrs := map[int][]byte{}
		for i := 0; i < n-1; i++ {
			subrs[i] = t1build.CS(i+1, t1build.CallSubr, t1build.Return)
		}
		subrs[n-1] = t1build.CS(10, 0, t1build.RLineTo, t1build.Return)
		cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, t1build.CallSubr, t1build.EndChar)
		r := t1Run(1000, subrs)
		if err := r.t1Glyph(cs, 0, 0); err != nil {
			t.Fatalf("t1Glyph: %v", err)
		}
		if len(r.out) == 0 {
			t.Error("outline is empty, want the innermost subroutine's line drawn")
		}
	})

	t.Run("nesting more than 8 deep is refused", func(t *testing.T) {
		const n = maxT1SubrDepth + 1
		subrs := map[int][]byte{}
		for i := 0; i < n; i++ {
			subrs[i] = t1build.CS(i+1, t1build.CallSubr, t1build.Return)
		}
		cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, t1build.CallSubr, t1build.EndChar)
		r := t1Run(1000, subrs)
		err := r.t1Glyph(cs, 0, 0)
		if err == nil || !strings.Contains(err.Error(), "nest more than 8 deep") {
			t.Errorf("t1Glyph = %v, want a refusal for nesting past 8 deep", err)
		}
	})

	t.Run("a subroutine that ends the glyph with endchar", func(t *testing.T) {
		subrs := map[int][]byte{0: t1build.CS(t1build.EndChar)}
		cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 10, 0, t1build.RLineTo, 0, t1build.CallSubr)
		r := t1Run(1000, subrs)
		if err := r.t1Glyph(cs, 0, 0); err != nil {
			t.Fatalf("t1Glyph: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(r.out, want) {
			t.Errorf("outline = %v\nwant     %v", r.out, want)
		}
	})
}

// TestT1Div pins div's own mechanics: an ordinary division, computing hsbw's own operands before it
// has run ("0 1000 3 div hsbw", §6.2's own example of a fractional width), and a large integer
// (past ±32000) divided down into an ordinary coordinate.
func TestT1Div(t *testing.T) {
	t.Run("an ordinary division as a line delta", func(t *testing.T) {
		cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 100, 4, t1build.Div, 0, t1build.RLineTo, t1build.EndChar)
		r := newCharRun(1000)
		if err := r.t1Glyph(cs, 0, 0); err != nil {
			t.Fatalf("t1Glyph: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{25, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(r.out, want) {
			t.Errorf("outline = %v\nwant     %v", r.out, want)
		}
	})

	t.Run("div before hsbw computes the side bearing", func(t *testing.T) {
		cs := t1build.CS(0, 1000, 3, t1build.Div, t1build.HSBW, t1build.EndChar)
		s := &t1State{charState: charState{r: newCharRun(1000)}}
		if _, err := s.exec(cs, 0); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if s.sbx != 0 {
			t.Errorf("sbx = %g, want 0", s.sbx)
		}
	})

	t.Run("a large integer divided down", func(t *testing.T) {
		cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 64000, 2, t1build.Div, 0, t1build.RLineTo, t1build.EndChar)
		r := newCharRun(1000)
		if err := r.t1Glyph(cs, 0, 0); err != nil {
			t.Fatalf("t1Glyph: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{32000, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(r.out, want) {
			t.Errorf("outline = %v\nwant     %v", r.out, want)
		}
	})
}

// TestT1Flex pins a full flex drawn the standard way a Type 1 producer writes one: Subrs 0, 1, and
// 2 do the format's own work (starting the flex, registering a point, and ending it), and the glyph
// itself just calls them. The two cubics are computed by hand from the seven rmoveto deltas below.
func TestT1Flex(t *testing.T) {
	subrs := map[int][]byte{
		0: t1build.CS(3, 0, t1build.CallOtherSubr, t1build.Pop, t1build.Pop, t1build.SetCurrentPoint, t1build.Return),
		1: t1build.CS(0, 1, t1build.CallOtherSubr, t1build.Return),
		2: t1build.CS(0, 2, t1build.CallOtherSubr, t1build.Return),
	}
	cs := t1build.CS(0, 500, t1build.HSBW,
		100, 100, t1build.RMoveTo,
		1, t1build.CallSubr, // start the flex (subr 1 -> othersubr 1)
		5, 5, t1build.RMoveTo, 2, t1build.CallSubr, // point 1: the reference point
		10, 0, t1build.RMoveTo, 2, t1build.CallSubr, // point 2
		10, 10, t1build.RMoveTo, 2, t1build.CallSubr, // point 3
		10, 10, t1build.RMoveTo, 2, t1build.CallSubr, // point 4: ends the first curve
		10, 0, t1build.RMoveTo, 2, t1build.CallSubr, // point 5
		10, 10, t1build.RMoveTo, 2, t1build.CallSubr, // point 6
		10, 10, t1build.RMoveTo, 2, t1build.CallSubr, // point 7: ends the second curve
		50, 165, 145, 0, t1build.CallSubr, // end the flex (subr 0 -> othersubr 0)
		t1build.EndChar)
	r := t1Run(10000, subrs)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{100, 100}}},
		{Op: SegCubic, P: [3]Point{{115, 105}, {125, 115}, {135, 125}}},
		{Op: SegCubic, P: [3]Point{{145, 125}, {155, 135}, {165, 145}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// TestT1HintReplacement pins the standard hint-replacement idiom, "subr# 1 3 callothersubr pop
// callsubr": othersubr 3 leaves the replacement subroutine's own number on the stack (this
// interpreter never runs a real othersubr, so there is nothing else for it to leave), the "pop"
// after it is consumed as part of the recognized byte sequence rather than read as a free-standing
// operator, and the callsubr that follows actually reaches the replacement subroutine.
func TestT1HintReplacement(t *testing.T) {
	subrs := map[int][]byte{3: t1build.CS(10, 0, t1build.RLineTo, t1build.Return)}
	cs := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo,
		3, 1, 3, t1build.CallOtherSubr, t1build.Pop, t1build.CallSubr, t1build.EndChar)
	r := t1Run(1000, subrs)
	if err := r.t1Glyph(cs, 0, 0); err != nil {
		t.Fatalf("t1Glyph: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{0, 0}}},
		{Op: SegLine, P: [3]Point{{10, 0}}},
		{Op: SegClose},
	}
	if !sameOutline(r.out, want) {
		t.Errorf("outline = %v\nwant     %v", r.out, want)
	}
}

// t1SeacFont builds a font with a base "A" (code 65), an accent "B" (code 66), both StandardEncoding
// codes (TN #5176 Appendix B), and a third glyph "C" whose own charstring is cs -- typically a seac.
func t1SeacFont(t *testing.T, cs []byte) *Type1 {
	t.Helper()
	base := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 50, 0, t1build.RLineTo, t1build.EndChar)
	accent := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 20, 30, t1build.RLineTo, t1build.EndChar)
	b := t1build.Builder{Glyphs: []t1build.Glyph{
		{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
		{Name: "A", CS: base},
		{Name: "B", CS: accent},
		{Name: "C", CS: cs},
	}}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	return tf
}

// TestT1Seac pins seac's composition, (adx + sbx - asb, ady): the calling glyph C's own hsbw sets
// sbx to 5, and asb 3, adx 10 make the accent's offset 10 + 5 - 3 = 12.
func TestT1Seac(t *testing.T) {
	cs := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, 66, t1build.Seac)
	tf := t1SeacFont(t, cs)
	gid, ok := tf.GIDForName("C")
	if !ok {
		t.Fatalf("GIDForName(C) = false")
	}
	out, _, err := tf.Outline(gid, 10000)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	want := Outline{
		{Op: SegMove, P: [3]Point{{0, 0}}},
		{Op: SegLine, P: [3]Point{{50, 0}}},
		{Op: SegClose},
		{Op: SegMove, P: [3]Point{{12, 20}}},
		{Op: SegLine, P: [3]Point{{32, 50}}},
		{Op: SegClose},
	}
	if !sameOutline(out, want) {
		t.Errorf("outline = %v\nwant     %v", out, want)
	}
}

// TestT1SeacRefusals pins seac's own refusals: the wrong operand count, nesting inside another
// seac's component, running after the calling glyph has already drawn, a character with no glyph in
// the font, and a character value that is not an ordinary integer StandardEncoding code.
func TestT1SeacRefusals(t *testing.T) {
	t.Run("wrong operand count", func(t *testing.T) {
		cs := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "operator 1206 with 4 operands") {
			t.Errorf("Outline = %v, want a refusal naming the wrong operand count", err)
		}
	})

	t.Run("too many operands is refused, not silently truncated to five", func(t *testing.T) {
		cs := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, 66, 0, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "operator 1206 with 6 operands") {
			t.Errorf("Outline = %v, want a refusal naming the wrong operand count", err)
		}
	})

	t.Run("refused after just a moveto, even with nothing drawn", func(t *testing.T) {
		cs := t1build.CS(5, 700, t1build.HSBW, 0, 0, t1build.RMoveTo, 3, 10, 20, 65, 66, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac after the glyph has drawn") {
			t.Errorf("Outline = %v, want a refusal after a moveto even with no segment yet", err)
		}
	})

	t.Run("character 255 is in range, but StandardEncoding has no glyph there", func(t *testing.T) {
		cs := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, 255, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "255 has no glyph in the font") {
			t.Errorf("Outline = %v, want a refusal for a missing glyph, not an out-of-range character", err)
		}
	})

	t.Run("achar as a fractional div result is refused, not truncated", func(t *testing.T) {
		cs := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, 131, 2, t1build.Div, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac character 65.5") {
			t.Errorf("Outline = %v, want a refusal naming the div-result character, not truncated to a glyph", err)
		}
	})

	t.Run("nested seac is refused", func(t *testing.T) {
		// "A" (bchar's own target) is itself a seac: reaching it must be refused before it recurses.
		nestedSeac := t1build.CS(0, 500, t1build.HSBW, 0, 10, 20, 65, 66, t1build.Seac)
		base := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 50, 0, t1build.RLineTo, t1build.EndChar)
		accent := t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 20, 30, t1build.RLineTo, t1build.EndChar)
		outerSeac := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, 66, t1build.Seac)
		b := t1build.Builder{Glyphs: []t1build.Glyph{
			{Name: ".notdef", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
			{Name: "A", CS: nestedSeac},
			{Name: "B", CS: accent},
			{Name: "Z", CS: base}, // unreferenced, just to keep "A" from being GID 0's swap target
			{Name: "C", CS: outerSeac},
		}}
		data, _ := b.Build()
		tf, err := ParseType1(data)
		if err != nil {
			t.Fatalf("ParseType1: %v", err)
		}
		gid, _ := tf.GIDForName("C")
		_, _, err = tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac inside a seac component") {
			t.Errorf("Outline = %v, want a refusal for nested seac", err)
		}
	})

	t.Run("seac after the glyph has drawn is refused", func(t *testing.T) {
		cs := t1build.CS(5, 700, t1build.HSBW, 0, 0, t1build.RMoveTo, 1, 1, t1build.RLineTo, 3, 10, 20, 65, 66, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac after the glyph has drawn") {
			t.Errorf("Outline = %v, want a refusal for seac after drawing", err)
		}
	})

	t.Run("a component the font lacks is refused", func(t *testing.T) {
		// Code 90 ('Z') is not one of this font's glyph names.
		cs := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 90, 66, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "no glyph in the font") {
			t.Errorf("Outline = %v, want a refusal naming the missing component", err)
		}
	})

	t.Run("a character value out of range is refused", func(t *testing.T) {
		cs := t1build.CS(5, 700, t1build.HSBW, 3, 10, 20, 65, 300, t1build.Seac)
		tf := t1SeacFont(t, cs)
		gid, _ := tf.GIDForName("C")
		_, _, err := tf.Outline(gid, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac character 300") {
			t.Errorf("Outline = %v, want a refusal naming the out-of-range character", err)
		}
	})
}

// TestT1CharstringRefusals pins every other error branch in the Type 1 interpreter: one case each,
// with a distinctive substring, so a mutant that refuses on the wrong condition still fails.
func TestT1CharstringRefusals(t *testing.T) {
	stems := func(n int) []byte {
		var ops []any
		ops = append(ops, 0, 500, t1build.HSBW)
		for i := 0; i < n; i++ {
			ops = append(ops, 10, 20, t1build.HStem)
		}
		return t1build.CS(append(ops, t1build.EndChar)...)
	}

	deepSubrs := func(n int) map[int][]byte {
		subrs := map[int][]byte{}
		for i := 0; i < n; i++ {
			subrs[i] = t1build.CS(i+1, t1build.CallSubr, t1build.Return)
		}
		return subrs
	}

	for _, c := range []struct {
		name  string
		subrs map[int][]byte
		cs    []byte
		want  string
	}{
		{
			"operator before hsbw or sbw",
			nil,
			t1build.CS(10, 0, t1build.RLineTo, t1build.EndChar),
			"operator 5 before hsbw or sbw",
		},
		{
			"a second hsbw or sbw",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 500, t1build.HSBW, t1build.EndChar),
			"a second hsbw or sbw",
		},
		{
			"wrong operand count",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 10, t1build.HStem, t1build.EndChar),
			"operator 1 with 1 operands",
		},
		{
			"too many operands, not silently accepted with the extra ignored",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 10, 20, 30, t1build.HStem, t1build.EndChar),
			"operator 1 with 3 operands",
		},
		{
			"othersubr 3 (hint replacement) with the wrong argument count",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 5, 6, 2, 3, t1build.CallOtherSubr, t1build.Pop, t1build.EndChar),
			"othersubr 3 without its one argument",
		},
		{
			"truncated operand, two-byte form",
			nil,
			t1build.Raw{251},
			"charstring operand is truncated",
		},
		{
			"truncated operand, five-byte form",
			nil,
			t1build.Raw{255, 0, 0},
			"charstring operand is truncated",
		},
		{
			"subroutine call without a number",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, t1build.CallSubr, t1build.EndChar),
			"subroutine call without a number",
		},
		{
			"a subroutine number that is a div result",
			map[int][]byte{0: t1build.CS(t1build.Return)},
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, 1000, 1000, t1build.Div, t1build.CallSubr, t1build.EndChar),
			"a subroutine number that is a div result",
		},
		{
			"subroutines nest more than 8 deep",
			deepSubrs(maxT1SubrDepth + 1),
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, t1build.CallSubr, t1build.EndChar),
			"nest more than 8 deep",
		},
		{
			"an undefined subroutine",
			map[int][]byte{},
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 5, t1build.CallSubr, t1build.EndChar),
			"subroutine 5 is not defined",
		},
		{
			"return outside a subroutine",
			nil,
			t1build.CS(0, 500, t1build.HSBW, t1build.Return),
			"return outside a subroutine",
		},
		{
			"div: too few operands",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 5, t1build.Div, t1build.EndChar),
			"operator 1212 with 1 operands",
		},
		{
			"div: a div result while a large integer is pending",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 40000, 1000, 3, t1build.Div, t1build.Div, t1build.EndChar),
			"div of a div result while an integer past",
		},
		{
			"div by zero",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 10, 0, t1build.Div, t1build.EndChar),
			"div by zero",
		},
		{
			"endchar with leftover operands",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 1, t1build.EndChar),
			"operator 14 with 1 operands",
		},
		{
			"more than 96 stems at endchar",
			nil,
			stems(97),
			"past the limit of 96",
		},
		{
			"an integer past +-32000 as a non-div operand",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 40000, 0, t1build.RLineTo, t1build.EndChar),
			"an integer past",
		},
		{
			"pop without an othersubr result to take",
			nil,
			t1build.CS(0, 500, t1build.HSBW, t1build.Pop, t1build.EndChar),
			"without the othersubr whose result it takes",
		},
		{
			"setcurrentpoint without an othersubr result to take",
			nil,
			t1build.CS(0, 500, t1build.HSBW, t1build.SetCurrentPoint, t1build.EndChar),
			"without the othersubr whose result it takes",
		},
		{
			"path operator before the first moveto",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 10, 0, t1build.RLineTo, t1build.EndChar),
			"path operator before the first moveto",
		},
		{
			"a path drawn after closepath from elsewhere",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 50, 0, t1build.RLineTo, t1build.ClosePath,
				10, 0, t1build.RLineTo, t1build.EndChar),
			"a path drawn after closepath from a point no moveto set",
		},
		{
			"callothersubr without a number and an argument count",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 5, t1build.CallOtherSubr, t1build.EndChar),
			"callothersubr without a number and an argument count",
		},
		{
			"callothersubr with a div result for its number or count",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, 1000, 3, t1build.Div, t1build.CallOtherSubr, t1build.EndChar),
			"callothersubr with a div result",
		},
		{
			"othersubr with extra stack items",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 1, 2, 3, 0, 1, t1build.CallOtherSubr, t1build.EndChar),
			"othersubr 1 with 0 arguments over a stack of 3",
		},
		{
			"an unknown othersubr",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 0, 99, t1build.CallOtherSubr, t1build.EndChar),
			"othersubr 99, which is refused",
		},
		{
			"flex: othersubr 1 called twice",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo,
				0, 1, t1build.CallOtherSubr, 0, 1, t1build.CallOtherSubr, t1build.EndChar),
			"othersubr 1 inside a flex, or with arguments",
		},
		{
			"flex: othersubr 2 outside a flex",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 2, t1build.CallOtherSubr, t1build.EndChar),
			"othersubr 2 outside a flex, or with arguments",
		},
		{
			"flex: more than seven points",
			nil,
			t1build.CS(append([]any{0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 1, t1build.CallOtherSubr},
				flexPoints2(8)...)...),
			"a flex of more than seven points",
		},
		{
			"flex: othersubr 0 without seven points",
			nil,
			t1build.CS(append(append([]any{0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 1, t1build.CallOtherSubr},
				flexPoints2(3)...), 50, 5, 5, 3, 0, t1build.CallOtherSubr, t1build.EndChar)...),
			"othersubr 0 without the seven points of a flex",
		},
		{
			"flex: a non-flex operator inside a flex",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 1, t1build.CallOtherSubr,
				10, 0, t1build.RLineTo, t1build.EndChar),
			"operator 5 inside a flex",
		},
		{
			"hint replacement: othersubr 3 not followed by pop",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo, 5, 1, 3, t1build.CallOtherSubr, t1build.EndChar),
			"othersubr 3 not followed by pop",
		},
		{
			"coordinates leaving the 16.16 range",
			nil,
			t1build.CS(0, 500, t1build.HSBW, 20000, 0, t1build.RMoveTo, 15000, 0, t1build.RLineTo, t1build.EndChar),
			"leave the 16.16 range",
		},
		{
			"an unknown one-byte operator",
			nil,
			t1build.CS(0, 500, t1build.HSBW, t1build.Op(28), t1build.EndChar),
			"charstring operator 28, which Type 1 does not define",
		},
		{
			"an unknown escaped operator",
			nil,
			t1build.CS(0, 500, t1build.HSBW, t1build.Op(1299), t1build.EndChar),
			"charstring operator 1299, which Type 1 does not define",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := t1Run(1000, c.subrs)
			err := r.t1Glyph(c.cs, 0, 0)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("t1Glyph = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

// flexPoints2 returns n repetitions of "0 0 rmoveto 2 callothersubr" -- enough to drive
// othersubr 2's own point count without needing real geometry, for refusal fixtures that only care
// about the count.
func flexPoints2(n int) []any {
	var ops []any
	for i := 0; i < n; i++ {
		ops = append(ops, 0, 0, t1build.RMoveTo, 0, 2, t1build.CallOtherSubr)
	}
	return ops
}

// TestT1FlexEndpointMismatchAndNoTrailer exercises the two "flex: othersubr 0" refusals that need
// seven real points (not just a count) to reach: the endpoint check compares the pen to the exact
// stack values othersubr 0 was called with, and the trailer check looks at the bytes right after
// callothersubr in the charstring, not merely at whether the flex is otherwise well-formed.
func TestT1FlexEndpointMismatchAndNoTrailer(t *testing.T) {
	t.Run("endpoint does not match the pen", func(t *testing.T) {
		ops := append([]any{0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 1, t1build.CallOtherSubr}, flexPoints2(7)...)
		ops = append(ops, 50, 999, 999, 3, 0, t1build.CallOtherSubr, t1build.EndChar)
		r := newCharRun(1000)
		err := r.t1Glyph(t1build.CS(ops...), 0, 0)
		if err == nil || !strings.Contains(err.Error(), "a flex ending at") {
			t.Errorf("t1Glyph = %v, want a refusal naming the endpoint mismatch", err)
		}
	})

	t.Run("not followed by pop pop setcurrentpoint", func(t *testing.T) {
		ops := append([]any{0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 1, t1build.CallOtherSubr}, flexPoints2(7)...)
		// The pen never moved (every flexPoints2 delta is 0,0), so the endpoint (5,5) matches; only
		// the missing pop pop setcurrentpoint trailer should be refused.
		ops = append(ops, 50, 5, 5, 3, 0, t1build.CallOtherSubr, t1build.EndChar)
		r := newCharRun(1000)
		err := r.t1Glyph(t1build.CS(ops...), 0, 0)
		if err == nil || !strings.Contains(err.Error(), "othersubr 0 not followed by pop pop setcurrentpoint") {
			t.Errorf("t1Glyph = %v, want a refusal naming the missing trailer", err)
		}
	})

	t.Run("more than seven points is refused", func(t *testing.T) {
		ops := append([]any{0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 1, t1build.CallOtherSubr}, flexPoints2(8)...)
		ops = append(ops, t1build.EndChar)
		r := newCharRun(1000)
		err := r.t1Glyph(t1build.CS(ops...), 0, 0)
		if err == nil || !strings.Contains(err.Error(), "a flex of more than seven points") {
			t.Errorf("t1Glyph = %v, want a refusal for an eighth flex point", err)
		}
	})

	t.Run("othersubr 0 without seven points", func(t *testing.T) {
		ops := append([]any{0, 500, t1build.HSBW, 5, 5, t1build.RMoveTo, 0, 1, t1build.CallOtherSubr}, flexPoints2(3)...)
		ops = append(ops, 50, 5, 5, 3, 0, t1build.CallOtherSubr, t1build.EndChar)
		r := newCharRun(1000)
		err := r.t1Glyph(t1build.CS(ops...), 0, 0)
		if err == nil || !strings.Contains(err.Error(), "othersubr 0 without the seven points of a flex") {
			t.Errorf("t1Glyph = %v, want a refusal for ending a flex early", err)
		}
	})
}

// TestT1ArgumentStackOverflow pins the 48-slot argument stack limit shared with CFF (maxCharStack):
// the 48th push is taken, and the 49th is refused.
func TestT1ArgumentStackOverflow(t *testing.T) {
	ops := make([]byte, maxCharStack)
	for i := range ops {
		ops[i] = 139 // value 0, one byte each
	}
	if _, err := t1Exec(1000, ops); !errors.Is(err, errNoEnd) {
		t.Errorf("exec of %d operands: err = %v, want errNoEnd (no operator ran yet)", maxCharStack, err)
	}
	ops = append(ops, 139)
	_, err := t1Exec(1000, ops)
	if err == nil || !strings.Contains(err.Error(), "argument stack overflows") {
		t.Errorf("exec err = %v, want a stack overflow refusal at the 49th operand", err)
	}
}

// TestT1NoEndcharOrReturn pins that a charstring running out of bytes without reaching endchar,
// return, or seac is refused with errNoEnd, the same sentinel CFF's interpreter uses.
func TestT1NoEndcharOrReturn(t *testing.T) {
	r := newCharRun(1000)
	err := r.t1Glyph(t1build.CS(0, 500, t1build.HSBW), 0, 0)
	if !errors.Is(err, errNoEnd) {
		t.Errorf("t1Glyph = %v, want errNoEnd", err)
	}
}

// TestT1GlyphSegmentLimit pins the shared maxGlyphSegments cap through the Type 1 interpreter:
// exactly at the cap succeeds, one more is refused.
func TestT1GlyphSegmentLimit(t *testing.T) {
	// Two subroutines, called alternately, so the pen oscillates between (0,0) and (1,0) and never
	// nears the 16.16 range limit however many times they are called.
	manyLines := func(n int) []byte {
		var ops []any
		ops = append(ops, 0, 500, t1build.HSBW, 0, 0, t1build.RMoveTo)
		for i := 0; i < n; i++ {
			if i%2 == 0 {
				ops = append(ops, 0, t1build.CallSubr)
			} else {
				ops = append(ops, 1, t1build.CallSubr)
			}
		}
		return t1build.CS(append(ops, t1build.EndChar)...)
	}
	subrs := map[int][]byte{
		0: t1build.CS(1, 0, t1build.RLineTo, t1build.Return),
		1: t1build.CS(-1, 0, t1build.RLineTo, t1build.Return),
	}

	t.Run("exactly maxGlyphSegments is accepted", func(t *testing.T) {
		r := t1Run(1<<24, subrs)
		if err := r.t1Glyph(manyLines(maxGlyphSegments-2), 0, 0); err != nil {
			t.Fatalf("t1Glyph: %v", err)
		}
		if len(r.out) != maxGlyphSegments {
			t.Errorf("segments = %d, want %d", len(r.out), maxGlyphSegments)
		}
	})

	t.Run("one more than maxGlyphSegments is refused", func(t *testing.T) {
		r := t1Run(1<<24, subrs)
		err := r.t1Glyph(manyLines(maxGlyphSegments-1), 0, 0)
		want := fmt.Sprintf("more than %d segments", maxGlyphSegments)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("t1Glyph = %v, want a refusal containing %q", err, want)
		}
	})
}

// TestT1OutlineBudget pins that Outline's op count includes every operand and operator: "0 500
// hsbw endchar" is exactly 4 tokens, a budget of 4 is enough, and one fewer fails with
// ErrGlyphBudget.
func TestT1OutlineBudget(t *testing.T) {
	b := t1build.Builder{Glyphs: []t1build.Glyph{
		{Name: "A", CS: t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)},
	}}
	data, _ := b.Build()
	tf, err := ParseType1(data)
	if err != nil {
		t.Fatalf("ParseType1: %v", err)
	}
	gid, _ := tf.GIDForName("A")

	t.Run("a budget of the exact op count is enough", func(t *testing.T) {
		_, ops, err := tf.Outline(gid, 4)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		if ops != 4 {
			t.Errorf("ops = %d, want 4 (0, 500, hsbw, endchar)", ops)
		}
	})

	t.Run("one op too few fails with ErrGlyphBudget", func(t *testing.T) {
		_, _, err := tf.Outline(gid, 3)
		if !errors.Is(err, ErrGlyphBudget) {
			t.Errorf("err = %v, want ErrGlyphBudget", err)
		}
	})
}

// TestT1OutlineGIDOutOfRange pins Outline's own bounds check.
func TestT1OutlineGIDOutOfRange(t *testing.T) {
	tf := &Type1{glyphs: [][]byte{t1build.CS(0, 500, t1build.HSBW, t1build.EndChar)}}
	_, _, err := tf.Outline(5, 100)
	if err == nil || !strings.Contains(err.Error(), "Type 1 glyph 5 of 1") {
		t.Errorf("Outline(5) = %v, want a refusal naming \"Type 1 glyph 5 of 1\"", err)
	}
}

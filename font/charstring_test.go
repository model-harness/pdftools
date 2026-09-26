package font

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/cffbuild"
)

// newCharRun is a charRun with no font behind it, for tests that drive charState.exec directly and
// never call a subroutine or seac — the mechanics this file pins point-for-point, which a finished
// Outline cannot show once the width or a stem count has been folded away.
func newCharRun(budget int) *charRun {
	return &charRun{budget: budget}
}

// outlineOfBuilder parses b and returns gid's outline.
func outlineOfBuilder(t *testing.T, b cffbuild.Builder, gid uint16, budget int) (Outline, int, error) {
	t.Helper()
	cff, err := ParseCFF(b.Build())
	if err != nil {
		t.Fatalf("ParseCFF: %v", err)
	}
	return cff.Outline(gid, budget)
}

// outlineOf parses a single-glyph font whose one glyph is cs and returns its outline.
func outlineOf(t *testing.T, cs []byte, budget int) (Outline, int, error) {
	t.Helper()
	return outlineOfBuilder(t, cffbuild.Builder{
		Glyphs: []cffbuild.Glyph{{CharString: cs}},
	}, 1, budget)
}

// TestPathOperators pins each path operator's geometry against outlines computed by hand from
// TN #5177 §4.1's operand lists — not from reading charstring.go, since a test copied from the
// implementation would agree with a wrong implementation just as readily as a right one. Every
// charstring starts "0 0 rmoveto" so the operator under test draws from a plain origin.
func TestPathOperators(t *testing.T) {
	for _, c := range []struct {
		name string
		cs   []byte
		want Outline
	}{
		{
			"rlineto, several pairs",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 0, 0, 10, -10, 0, cffbuild.RLineTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{10, 0}}},
				{Op: SegLine, P: [3]Point{{10, 10}}},
				{Op: SegLine, P: [3]Point{{0, 10}}},
				{Op: SegClose},
			},
		},
		{
			"hlineto, odd count starts horizontal",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 5, -10, cffbuild.HLineTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{10, 0}}},
				{Op: SegLine, P: [3]Point{{10, 5}}},
				{Op: SegLine, P: [3]Point{{0, 5}}},
				{Op: SegClose},
			},
		},
		{
			"hlineto, even count",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 5, -10, -5, cffbuild.HLineTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{10, 0}}},
				{Op: SegLine, P: [3]Point{{10, 5}}},
				{Op: SegLine, P: [3]Point{{0, 5}}},
				{Op: SegLine, P: [3]Point{{0, 0}}},
				{Op: SegClose},
			},
		},
		{
			"vlineto, odd count starts vertical",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 5, -10, cffbuild.VLineTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{0, 10}}},
				{Op: SegLine, P: [3]Point{{5, 10}}},
				{Op: SegLine, P: [3]Point{{5, 0}}},
				{Op: SegClose},
			},
		},
		{
			"vlineto, even count",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 5, -10, -5, cffbuild.VLineTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{0, 10}}},
				{Op: SegLine, P: [3]Point{{5, 10}}},
				{Op: SegLine, P: [3]Point{{5, 0}}},
				{Op: SegLine, P: [3]Point{{0, 0}}},
				{Op: SegClose},
			},
		},
		{
			"rrcurveto, two curves",
			cffbuild.CS(0, 0, cffbuild.RMoveTo,
				10, 0, 10, 10, 0, 10,
				0, 10, -10, 10, -10, 0,
				cffbuild.RRCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{10, 0}, {20, 10}, {20, 20}}},
				{Op: SegCubic, P: [3]Point{{20, 30}, {10, 40}, {0, 40}}},
				{Op: SegClose},
			},
		},
		{
			"rcurveline",
			cffbuild.CS(0, 0, cffbuild.RMoveTo,
				10, 0, 10, 10, 0, 10, 5, 5,
				cffbuild.RCurveLine, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{10, 0}, {20, 10}, {20, 20}}},
				{Op: SegLine, P: [3]Point{{25, 25}}},
				{Op: SegClose},
			},
		},
		{
			"rlinecurve",
			cffbuild.CS(0, 0, cffbuild.RMoveTo,
				5, 5, 10, 0, 10, 10, 0, 10,
				cffbuild.RLineCurve, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegLine, P: [3]Point{{5, 5}}},
				{Op: SegCubic, P: [3]Point{{15, 5}, {25, 15}, {25, 25}}},
				{Op: SegClose},
			},
		},
		{
			// dx1 omitted (0): {dya dxb dyb dyc}, TN #5177 §4.1.
			"vvcurveto, no leading dx1",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 3, 4, 10, cffbuild.VVCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{0, 10}, {3, 14}, {3, 24}}},
				{Op: SegClose},
			},
		},
		{
			"vvcurveto, with leading dx1",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 2, 10, 3, 4, 10, cffbuild.VVCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{2, 10}, {5, 14}, {5, 24}}},
				{Op: SegClose},
			},
		},
		{
			// dy1 omitted (0): {dxa dxb dyb dxc}, TN #5177 §4.1.
			"hhcurveto, no leading dy1",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 3, 4, 10, cffbuild.HHCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{10, 0}, {13, 4}, {23, 4}}},
				{Op: SegClose},
			},
		},
		{
			"hhcurveto, with leading dy1",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 2, 10, 3, 4, 10, cffbuild.HHCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{10, 2}, {13, 6}, {23, 6}}},
				{Op: SegClose},
			},
		},
		{
			// hvcurveto, 4 operands: one curve, starts horizontal, ends vertical (dx3=0 implicit).
			"hvcurveto, 4 operands",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 6, 2, 3, 7, cffbuild.HVCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{6, 0}, {8, 3}, {8, 10}}},
				{Op: SegClose},
			},
		},
		{
			// vhcurveto, 5 operands: one curve, starts vertical; the 5th operand is the final
			// point's x (replacing the implicit 0), so it does not end horizontal.
			"vhcurveto, 5 operands (the final delta)",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 6, 2, 3, 7, 4, cffbuild.VHCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{0, 6}, {2, 9}, {9, 13}}},
				{Op: SegClose},
			},
		},
		{
			"hvcurveto, 8 operands: two curves, no final delta",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 6, 2, 3, 7, 4, 1, 2, 5, cffbuild.HVCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{6, 0}, {8, 3}, {8, 10}}},
				{Op: SegCubic, P: [3]Point{{8, 14}, {9, 16}, {14, 16}}},
				{Op: SegClose},
			},
		},
		{
			"vhcurveto, 9 operands: two curves, the final delta on the second",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 6, 2, 3, 7, 4, 1, 2, 5, 9, cffbuild.VHCurveTo, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{0, 6}, {2, 9}, {9, 9}}},
				{Op: SegCubic, P: [3]Point{{13, 9}, {14, 11}, {23, 16}}},
				{Op: SegClose},
			},
		},
		{
			"flex: two curves, fd ignored",
			cffbuild.CS(0, 0, cffbuild.RMoveTo,
				5, 0, 5, 5, 0, 5, 5, 0, 5, -5, 0, -5, 50,
				cffbuild.Flex, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{5, 0}, {10, 5}, {10, 10}}},
				{Op: SegCubic, P: [3]Point{{15, 10}, {20, 5}, {20, 0}}},
				{Op: SegClose},
			},
		},
		{
			// hflex: dx1 dx2 dy2 dx3 dx4 dx5 dx6. The joining point (end of curve 1) rises by dy2;
			// curve 2 falls by the same dy2, so the glyph ends at the starting y (here 0).
			"hflex ends at the starting y",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 6, 4, 10, 6, 6, 4, 6, cffbuild.HFlex, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{6, 0}, {10, 10}, {16, 10}}},
				{Op: SegCubic, P: [3]Point{{22, 10}, {26, 0}, {32, 0}}},
				{Op: SegClose},
			},
		},
		{
			"hflex1 ends at the starting y",
			cffbuild.CS(0, 0, cffbuild.RMoveTo, 4, 3, 5, 7, 6, 6, 5, -7, 4, cffbuild.HFlex1, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{4, 3}, {9, 10}, {15, 10}}},
				{Op: SegCubic, P: [3]Point{{21, 10}, {26, 3}, {30, 0}}},
				{Op: SegClose},
			},
		},
		{
			// flex1, |dx|>|dy| branch: summed dx (50) exceeds summed dy (0), so d6 is the final
			// dx and the final y returns to the starting y (TN #5177 §4.1's flex1 description).
			"flex1, |dx| > |dy| branch",
			cffbuild.CS(0, 0, cffbuild.RMoveTo,
				10, 0, 10, 5, 10, -5, 10, 0, 10, 0, 5,
				cffbuild.Flex1, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{10, 0}, {20, 5}, {30, 0}}},
				{Op: SegCubic, P: [3]Point{{40, 0}, {50, 0}, {55, 0}}},
				{Op: SegClose},
			},
		},
		{
			// flex1 with summed dx and dy equal (50 and 50): the tie goes to the dy branch, as in
			// FreeType's cf2_doFlex, so d6 is the final dy and the final x returns to the start.
			"flex1, |dx| == |dy| takes the dy branch",
			cffbuild.CS(0, 0, cffbuild.RMoveTo,
				10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 5,
				cffbuild.Flex1, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{10, 10}, {20, 20}, {30, 30}}},
				{Op: SegCubic, P: [3]Point{{40, 40}, {50, 50}, {0, 55}}},
				{Op: SegClose},
			},
		},
		{
			// flex1, the other branch: summed dy (50) exceeds summed dx (0), so d6 is the final dy
			// and the final x returns to the starting x.
			"flex1, |dy| >= |dx| branch",
			cffbuild.CS(0, 0, cffbuild.RMoveTo,
				0, 10, 5, 10, -5, 10, 0, 10, 0, 10, 5,
				cffbuild.Flex1, cffbuild.EndChar),
			Outline{
				{Op: SegMove, P: [3]Point{{0, 0}}},
				{Op: SegCubic, P: [3]Point{{0, 10}, {5, 20}, {0, 30}}},
				{Op: SegCubic, P: [3]Point{{0, 40}, {0, 50}, {0, 55}}},
				{Op: SegClose},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, _, err := outlineOf(t, c.cs, 1000)
			if err != nil {
				t.Fatalf("Outline: %v", err)
			}
			if !sameOutline(out, c.want) {
				t.Errorf("outline = %v\nwant     %v", out, c.want)
			}
		})
	}
}

// TestContourRules pins how a contour opens and closes: a moveto closes whatever contour is open,
// two movetos in a row draw nothing, and endchar closes the last open contour.
func TestContourRules(t *testing.T) {
	t.Run("moveto closes the open contour", func(t *testing.T) {
		cs := cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 0, cffbuild.RLineTo, 5, 5, cffbuild.RMoveTo, cffbuild.EndChar)
		out, _, err := outlineOf(t, cs, 1000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v: the second rmoveto should close the first contour"+
				" and draw nothing of its own", out, want)
		}
	})

	t.Run("moveto then moveto draws nothing", func(t *testing.T) {
		cs := cffbuild.CS(0, 0, cffbuild.RMoveTo, 5, 5, cffbuild.RMoveTo, cffbuild.EndChar)
		out, _, err := outlineOf(t, cs, 1000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		if out != nil {
			t.Errorf("outline = %v, want nil: no drawing operator ever ran", out)
		}
	})

	t.Run("endchar closes the open contour", func(t *testing.T) {
		cs := cffbuild.CS(0, 0, cffbuild.RMoveTo, 10, 0, cffbuild.RLineTo, cffbuild.EndChar)
		out, _, err := outlineOf(t, cs, 1000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})
}

// TestWidthStripping pins that the optional width is stripped only from the first stack-clearing
// operator a charstring executes, and only when the operand count says one is there (TN #5177
// §4.2's rule for rmoveto, hmoveto, vmoveto, endchar, and the stem/mask operators).
func TestWidthStripping(t *testing.T) {
	t.Run("hstem with an odd count strips the first as width", func(t *testing.T) {
		// 10 20 30 hstem: 3 is odd, so 10 is the width and (20,30) is the one stem pair left.
		s := &charState{r: newCharRun(1000)}
		cs := cffbuild.CS(10, 20, 30, cffbuild.HStem, cffbuild.EndChar)
		if _, err := s.exec(cs, 0); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if s.stems != 1 {
			t.Errorf("stems = %d, want 1", s.stems)
		}
	})

	t.Run("rmoveto with 3 strips the first as width", func(t *testing.T) {
		s := &charState{r: newCharRun(1000)}
		cs := cffbuild.CS(999, 5, 7, cffbuild.RMoveTo, cffbuild.EndChar)
		if _, err := s.exec(cs, 0); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if s.x != 5 || s.y != 7 {
			t.Errorf("pen = (%g,%g), want (5,7): 999 should be the width dropped, not a delta", s.x, s.y)
		}
	})

	t.Run("hmoveto with 2 strips the first as width", func(t *testing.T) {
		s := &charState{r: newCharRun(1000)}
		cs := cffbuild.CS(999, 5, cffbuild.HMoveTo, cffbuild.EndChar)
		if _, err := s.exec(cs, 0); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if s.x != 5 || s.y != 0 {
			t.Errorf("pen = (%g,%g), want (5,0)", s.x, s.y)
		}
	})

	t.Run("vmoveto with 2 strips the first as width", func(t *testing.T) {
		s := &charState{r: newCharRun(1000)}
		cs := cffbuild.CS(999, 5, cffbuild.VMoveTo, cffbuild.EndChar)
		if _, err := s.exec(cs, 0); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if s.x != 0 || s.y != 5 {
			t.Errorf("pen = (%g,%g), want (0,5)", s.x, s.y)
		}
	})

	t.Run("endchar with 1 operand is a width, not seac", func(t *testing.T) {
		s := &charState{r: newCharRun(1000)}
		cs := cffbuild.CS(999, cffbuild.EndChar)
		ended, err := s.exec(cs, 0)
		if err != nil || !ended {
			t.Fatalf("exec: ended=%v err=%v, want a clean endchar (999 stripped as the width)", ended, err)
		}
	})

	t.Run("hintmask with an odd count strips the first as width", func(t *testing.T) {
		// No stems declared beforehand, so hintmask's own operands are the only source of stems:
		// 999 5 5 hintmask is odd, 999 is the width, and the pair (5,5) is one implicit vstem,
		// needing ceil(1/8) = 1 mask byte.
		s := &charState{r: newCharRun(1000)}
		cs := cffbuild.CS(999, 5, 5, cffbuild.HintMask, cffbuild.Raw{0x80}, cffbuild.EndChar)
		if _, err := s.exec(cs, 0); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if s.stems != 1 {
			t.Errorf("stems = %d, want 1", s.stems)
		}
	})

	t.Run("a width is not stripped on a later operator", func(t *testing.T) {
		// hstem (2 operands, even: no width there either way) sets haveWidth. The vstemhm that
		// follows has 1 operand — odd — but must NOT be treated as a width now that haveWidth is
		// already set, so it should fail as "1 operand", not silently drop to 0.
		s := &charState{r: newCharRun(1000)}
		cs := cffbuild.CS(5, 10, cffbuild.HStem, 999, cffbuild.VStemHM, cffbuild.EndChar)
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "not 1 operands") {
			t.Errorf("exec err = %v, want an error citing the 1 leftover operand", err)
		}
	})
}

// TestHintMaskByteCount pins that a mask's byte count is ceil(stems/8), counting the implicit
// vstem from any operands on the stack when hintmask or cntrmask runs (TN #5177 §4.3).
func TestHintMaskByteCount(t *testing.T) {
	for _, c := range []struct {
		name  string
		stems int
		bytes int
	}{
		{"1 stem needs 1 byte", 1, 1},
		{"8 stems need 1 byte", 8, 1},
		{"9 stems need 2 bytes", 9, 2},
		{"16 stems need 2 bytes", 16, 2},
		{"17 stems need 3 bytes", 17, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			var ops []any
			for i := 0; i < c.stems; i++ {
				ops = append(ops, 10, 20)
			}
			ops = append(ops, cffbuild.HStemHM)
			mask := make(cffbuild.Raw, c.bytes)
			for i := range mask {
				mask[i] = 0xFF
			}
			ops = append(ops, cffbuild.HintMask, mask, cffbuild.EndChar)
			s := &charState{r: newCharRun(10000)}
			if _, err := s.exec(cffbuild.CS(ops...), 0); err != nil {
				t.Fatalf("exec: %v", err)
			}
			if s.stems != c.stems {
				t.Errorf("stems = %d, want %d", s.stems, c.stems)
			}
		})
	}
}

// TestHintMaskMisreadRegression is the case a one-byte mask reader gets wrong: 9 stems need two
// mask bytes, and if only one were consumed, the second (0x80, an ordinary operand byte) would be
// pushed as an operand instead, leaving a stray value on the stack that turns the endchar below —
// clean under a correct reader — into "endchar with 1 operands".
func TestHintMaskMisreadRegression(t *testing.T) {
	cs := cffbuild.CS(
		10, 20, 10, 20, 10, 20, 10, 20, cffbuild.HStemHM, // 4 stems
		10, 20, 10, 20, 10, 20, 10, 20, cffbuild.VStemHM, // 4 more
		10, 20, cffbuild.HintMask, cffbuild.Raw{0xFF, 0x80}, // +1 implicit vstem = 9; 2 mask bytes
		cffbuild.EndChar,
	)
	s := &charState{r: newCharRun(10000)}
	ended, err := s.exec(cs, 0)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !ended {
		t.Fatal("did not reach endchar")
	}
	if s.stems != 9 {
		t.Errorf("stems = %d, want 9", s.stems)
	}
}

// TestCntrmaskMisreadRegression is TestHintMaskMisreadRegression's cntrmask counterpart: cntrmask's
// own leftover pair is an implicit vstem counted once, not twice, so 7 declared stems plus that one
// implicit vstem need ceil(8/8)=1 mask byte. A reader that counted the implicit vstem twice would
// think 9 stems need 2 mask bytes, consume endchar's own opcode as the second one, and run on past
// it into the square appended below -- a glyph this charstring never draws.
func TestCntrmaskMisreadRegression(t *testing.T) {
	cs := append(stemOps(7),
		cffbuild.CS(10, 20, cffbuild.CntrMask, cffbuild.Raw{0x00}, cffbuild.EndChar)...)
	cs = append(cs, cffbuild.Square()...)
	s := &charState{r: newCharRun(10000)}
	ended, err := s.exec(cs, 0)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !ended {
		t.Fatal("did not reach endchar")
	}
	if s.stems != 8 {
		t.Errorf("stems = %d, want 8 (7 declared plus cntrmask's own implicit vstem)", s.stems)
	}
	if len(s.r.out) != 0 {
		t.Errorf("out = %d segments, want 0: the appended square is never reached", len(s.r.out))
	}
}

// TestStemLimits pins the two refusals around stems: none may be declared after a hintmask, and
// the total may not pass TN #5177 Appendix B's limit of 96.
func TestStemLimits(t *testing.T) {
	t.Run("a stem declared after a hintmask is refused", func(t *testing.T) {
		cs := cffbuild.CS(10, 20, cffbuild.HStemHM,
			cffbuild.HintMask, cffbuild.Raw{0x80},
			10, 20, cffbuild.VStemHM, cffbuild.EndChar)
		s := &charState{r: newCharRun(1000)}
		if _, err := s.exec(cs, 0); err == nil {
			t.Error("exec succeeded, want a refusal for a stem declared after a hintmask")
		}
	})

	// stems declares n stems, 24 to an operator so the 48-operand stack never overflows, then a
	// hintmask with its one bit per stem.
	stems := func(n int) []byte {
		var ops []any
		for left := n; left > 0; left -= 24 {
			for i := 0; i < min(left, 24); i++ {
				ops = append(ops, 10, 20)
			}
			ops = append(ops, cffbuild.HStemHM)
		}
		return cffbuild.CS(append(ops, cffbuild.HintMask, cffbuild.Raw(make([]byte, (n+7)/8)), cffbuild.EndChar)...)
	}

	t.Run("96 stems are taken", func(t *testing.T) {
		s := &charState{r: newCharRun(10000)}
		if _, err := s.exec(stems(96), 0); err != nil {
			t.Errorf("exec of 96 stems: %v, want them taken", err)
		}
	})

	t.Run("more than 96 stems is refused", func(t *testing.T) {
		s := &charState{r: newCharRun(10000)}
		_, err := s.exec(stems(97), 0)
		if err == nil || !strings.Contains(err.Error(), "past the limit of 96") {
			t.Errorf("exec err = %v, want a refusal past the 96-stem limit", err)
		}
	})
}

// TestSubroutines pins the subroutine bias formula of TN #5177 §4.7 at both thresholds, and the
// refusals around calling and returning.
func TestSubroutines(t *testing.T) {
	t.Run("local subroutine, bias 107 (count under 1240)", func(t *testing.T) {
		b := cffbuild.Builder{
			Subrs: [][]byte{cffbuild.CS(10, 0, cffbuild.RLineTo, cffbuild.Return)},
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				-107, cffbuild.CallSubr, // subr number 0 = -107 + bias(107)
				cffbuild.EndChar)}},
		}
		out, _, err := outlineOfBuilder(t, b, 1, 1000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})

	t.Run("global subroutine, bias 107", func(t *testing.T) {
		b := cffbuild.Builder{
			GSubrs: [][]byte{cffbuild.CS(10, 0, cffbuild.RLineTo, cffbuild.Return)},
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				-107, cffbuild.CallGSubr,
				cffbuild.EndChar)}},
		}
		out, _, err := outlineOfBuilder(t, b, 1, 1000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})

	t.Run("bias 1131 once the count reaches 1240", func(t *testing.T) {
		subrs := make([][]byte, 1240)
		subrs[0] = cffbuild.CS(10, 0, cffbuild.RLineTo, cffbuild.Return)
		for i := 1; i < len(subrs); i++ {
			subrs[i] = cffbuild.CS(cffbuild.Return)
		}
		b := cffbuild.Builder{
			Subrs: subrs,
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				-1131, cffbuild.CallSubr, // subr number 0 = -1131 + bias(1131)
				cffbuild.EndChar)}},
		}
		out, _, err := outlineOfBuilder(t, b, 1, 10000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})

	t.Run("bias 32768 once the count reaches 33900", func(t *testing.T) {
		subrs := make([][]byte, 33900)
		subrs[0] = cffbuild.CS(10, 0, cffbuild.RLineTo, cffbuild.Return)
		for i := 1; i < len(subrs); i++ {
			subrs[i] = cffbuild.CS(cffbuild.Return)
		}
		b := cffbuild.Builder{
			Subrs: subrs,
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				-32768, cffbuild.CallSubr, // subr number 0 = -32768 + bias(32768)
				cffbuild.EndChar)}},
		}
		out, _, err := outlineOfBuilder(t, b, 1, 10000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})

	t.Run("subroutines nesting exactly 10 deep draw", func(t *testing.T) {
		const n = 10
		subrs := make([][]byte, n)
		for i := 0; i < n-1; i++ {
			subrs[i] = cffbuild.CS(i+1-107, cffbuild.CallSubr, cffbuild.Return)
		}
		subrs[n-1] = cffbuild.CS(10, 0, cffbuild.RLineTo, cffbuild.Return) // runs at depth 10
		b := cffbuild.Builder{
			Subrs: subrs,
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				-107, cffbuild.CallSubr,
				cffbuild.EndChar)}},
		}
		out, _, err := outlineOfBuilder(t, b, 1, 10000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})

	t.Run("subroutines nesting more than 10 deep is refused", func(t *testing.T) {
		const n = 10
		subrs := make([][]byte, n)
		for i := 0; i < n; i++ {
			// Each calls the next by physical index i+1 (bias 107, since count 10 < 1240); the
			// last calls index 10, which does not need to exist because the depth check at
			// depth 10 -> 11 fires before the subr is ever fetched.
			subrs[i] = cffbuild.CS(i+1-107, cffbuild.CallSubr, cffbuild.Return)
		}
		b := cffbuild.Builder{
			Subrs: subrs,
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				-107, cffbuild.CallSubr,
				cffbuild.EndChar)}},
		}
		_, _, err := outlineOfBuilder(t, b, 1, 10000)
		if err == nil || !strings.Contains(err.Error(), "nest more than ten deep") {
			t.Errorf("Outline err = %v, want a refusal for nesting past 10 deep", err)
		}
	})

	t.Run("return outside a subroutine is refused", func(t *testing.T) {
		s := &charState{r: newCharRun(100)}
		_, err := s.exec(cffbuild.CS(cffbuild.Return), 0)
		if err == nil || !strings.Contains(err.Error(), "return outside a subroutine") {
			t.Errorf("exec err = %v, want a refusal for return at depth 0", err)
		}
	})

	t.Run("a charstring with no endchar or return is refused", func(t *testing.T) {
		s := &charState{r: newCharRun(100)}
		_, err := s.exec(cffbuild.CS(0, 0, cffbuild.RMoveTo), 0)
		if !errors.Is(err, errNoEnd) {
			t.Errorf("exec err = %v, want errNoEnd", err)
		}
	})

	t.Run("a subroutine that ends the glyph with endchar", func(t *testing.T) {
		b := cffbuild.Builder{
			Subrs: [][]byte{cffbuild.CS(cffbuild.EndChar)},
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				10, 0, cffbuild.RLineTo,
				-107, cffbuild.CallSubr)}}, // the subroutine's own endchar finishes the glyph
		}
		out, _, err := outlineOfBuilder(t, b, 1, 1000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{0, 0}}},
			{Op: SegLine, P: [3]Point{{10, 0}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})
}

// TestSeac pins the deprecated seac form of endchar (TN #5177 Appendix C): a base drawn at the
// origin and an accent offset by (adx,ady), both found through the charset by the SID
// StandardEncoding gives their code (TN #5176 Appendix A/B).
func TestSeac(t *testing.T) {
	square, diamond := cffbuild.Square(), cffbuild.Diamond()

	// The shifted diamond: every point of Diamond() offset by (10,20), the seac accent's adx,ady.
	shiftedDiamond := Outline{
		{Op: SegMove, P: [3]Point{{510, 120}}},
		{Op: SegCubic, P: [3]Point{{710, 120}, {910, 320}, {910, 520}}},
		{Op: SegCubic, P: [3]Point{{910, 720}, {710, 920}, {510, 920}}},
		{Op: SegCubic, P: [3]Point{{310, 920}, {110, 720}, {110, 520}}},
		{Op: SegCubic, P: [3]Point{{110, 320}, {310, 120}, {510, 120}}},
		{Op: SegClose},
	}
	squareOutline := Outline{
		{Op: SegMove, P: [3]Point{{100, 100}}},
		{Op: SegLine, P: [3]Point{{400, 100}}},
		{Op: SegLine, P: [3]Point{{400, 400}}},
		{Op: SegLine, P: [3]Point{{100, 400}}},
		{Op: SegLine, P: [3]Point{{100, 100}}},
		{Op: SegClose},
	}
	want := append(append(Outline{}, squareOutline...), shiftedDiamond...)

	for _, c := range []struct {
		name string
		cs   []byte // the seac glyph's own charstring
	}{
		{
			"base at origin, accent offset",
			// adx=10 ady=20 bchar=65 ('A', SID 34, TN #5176 Appendix A/B) achar=66 ('B', SID 35).
			cffbuild.CS(10, 20, 65, 66, cffbuild.EndChar),
		},
		{
			"endchar with 5 operands: a width ahead of the same seac",
			cffbuild.CS(999, 10, 20, 65, 66, cffbuild.EndChar),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{
				{SID: 34, CharString: square},  // "A", TN #5176 Appendix A: the standard SID seac's
				{SID: 35, CharString: diamond}, // "B" -- StandardEncoding lookup must land on.
				{Name: "C", CharString: c.cs},
			}}
			out, _, err := outlineOfBuilder(t, b, 3, 10000)
			if err != nil {
				t.Fatalf("Outline: %v", err)
			}
			if !sameOutline(out, want) {
				t.Errorf("outline = %v\nwant     %v", out, want)
			}
		})
	}

	t.Run("a component the font lacks is refused", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			// No glyph named "A" (SID 34): bchar 65 resolves to no glyph.
			{Name: "Z", CharString: square},
			{Name: "C", CharString: cffbuild.CS(0, 0, 65, 66, cffbuild.EndChar)},
		}}
		_, _, err := outlineOfBuilder(t, b, 2, 10000)
		if err == nil || !strings.Contains(err.Error(), "no glyph in the font") {
			t.Errorf("Outline err = %v, want a refusal naming the missing component", err)
		}
	})

	t.Run("seac in a CID-keyed font is refused", func(t *testing.T) {
		b := cffbuild.Builder{
			CID: true,
			Glyphs: []cffbuild.Glyph{
				{CID: 1, CharString: cffbuild.CS(0, 0, 65, 66, cffbuild.EndChar)},
			},
		}
		_, _, err := outlineOfBuilder(t, b, 1, 10000)
		if err == nil || !strings.Contains(err.Error(), "CID-keyed font") {
			t.Errorf("Outline err = %v, want a refusal naming the CID-keyed font", err)
		}
	})

	t.Run("seac inside a seac component is refused", func(t *testing.T) {
		b := cffbuild.Builder{Glyphs: []cffbuild.Glyph{
			// "A" (bchar's target, SID 34) is itself a seac: reaching it from inside another
			// seac must be refused before it recurses.
			{SID: 34, CharString: cffbuild.CS(0, 0, 65, 66, cffbuild.EndChar)},
			{SID: 35, CharString: diamond},
			{Name: "C", CharString: cffbuild.CS(10, 20, 65, 66, cffbuild.EndChar)},
		}}
		_, _, err := outlineOfBuilder(t, b, 3, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac inside a seac component") {
			t.Errorf("Outline err = %v, want a refusal for nested seac", err)
		}
	})
}

// TestRefusedOperatorsAreNamed pins that the arithmetic/storage/conditional operators this
// interpreter declines, and a reserved operator code, are refused with a message naming which.
func TestRefusedOperatorsAreNamed(t *testing.T) {
	for _, c := range []struct {
		name string
		cs   []byte
		want string
	}{
		{"add", cffbuild.CS(1, 2, cffbuild.Add, cffbuild.EndChar), "add"},
		{"random", cffbuild.CS(cffbuild.Random, cffbuild.EndChar), "random"},
		{"a reserved two-byte operator, 12 38", cffbuild.CS(cffbuild.Op(1238), cffbuild.EndChar), "reserved charstring operator 1238"},
		{"a reserved one-byte operator, 2", cffbuild.CS(cffbuild.Op(2), cffbuild.EndChar), "reserved charstring operator 2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &charState{r: newCharRun(1000)}
			_, err := s.exec(c.cs, 0)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("exec err = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// TestDotSection pins the deprecated dotsection operator (TN #5177 Appendix C): a no-op that
// still enforces its own operand count of zero.
func TestDotSection(t *testing.T) {
	t.Run("with operands is refused", func(t *testing.T) {
		s := &charState{r: newCharRun(1000)}
		_, err := s.exec(cffbuild.CS(1, cffbuild.DotSection, cffbuild.EndChar), 0)
		if err == nil {
			t.Error("exec succeeded, want a refusal: dotsection takes no operands")
		}
	})
	t.Run("without operands is a no-op", func(t *testing.T) {
		s := &charState{r: newCharRun(1000)}
		if _, err := s.exec(cffbuild.CS(cffbuild.DotSection, cffbuild.EndChar), 0); err != nil {
			t.Errorf("exec: %v, want dotsection to be silent", err)
		}
	})
}

// TestPathOperatorBeforeMoveto pins that a path operator with no moveto to start from is refused,
// rather than drawing from an implicit (0,0).
func TestPathOperatorBeforeMoveto(t *testing.T) {
	s := &charState{r: newCharRun(1000)}
	_, err := s.exec(cffbuild.CS(5, 5, cffbuild.RLineTo, cffbuild.EndChar), 0)
	if err == nil || !strings.Contains(err.Error(), "before the first moveto") {
		t.Errorf("exec err = %v, want a refusal for a path operator before any moveto", err)
	}
}

// TestOperandCountErrors pins that every operator family reports an operand-count error naming
// the operator and the count it got, for one representative bad count per family.
func TestOperandCountErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		cs   []byte
		want string
	}{
		{"rlineto, odd count", cffbuild.CS(0, 0, cffbuild.RMoveTo, 5, cffbuild.RLineTo, cffbuild.EndChar), "operator 5 with 1 operands"},
		{"hlineto, zero count", cffbuild.CS(0, 0, cffbuild.RMoveTo, cffbuild.HLineTo, cffbuild.EndChar), "operator 6 with 0 operands"},
		{"rrcurveto, not a multiple of 6", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, cffbuild.RRCurveTo, cffbuild.EndChar), "operator 8 with 5 operands"},
		{"rcurveline, too short", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, 7, cffbuild.RCurveLine, cffbuild.EndChar), "operator 24 with 7 operands"},
		{"rlinecurve, too short", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, 7, cffbuild.RLineCurve, cffbuild.EndChar), "operator 25 with 7 operands"},
		{"vvcurveto, too few", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, cffbuild.VVCurveTo, cffbuild.EndChar), "operator 26 with 3 operands"},
		{"hhcurveto, bad remainder", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, cffbuild.HHCurveTo, cffbuild.EndChar), "operator 27 with 6 operands"},
		{"vhcurveto, too few", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, cffbuild.VHCurveTo, cffbuild.EndChar), "operator 30 with 3 operands"},
		{"hvcurveto, bad remainder", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, cffbuild.HVCurveTo, cffbuild.EndChar), "operator 31 with 6 operands"},
		{"flex, wrong count", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, cffbuild.Flex, cffbuild.EndChar), "operator 1235 with 12 operands"},
		{"hflex, wrong count", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, cffbuild.HFlex, cffbuild.EndChar), "operator 1234 with 6 operands"},
		{"hflex1, wrong count", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, 7, 8, cffbuild.HFlex1, cffbuild.EndChar), "operator 1236 with 8 operands"},
		{"flex1, wrong count", cffbuild.CS(0, 0, cffbuild.RMoveTo, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, cffbuild.Flex1, cffbuild.EndChar), "operator 1237 with 10 operands"},
		{"rmoveto, wrong count", cffbuild.CS(1, cffbuild.RMoveTo, cffbuild.EndChar), "takes 2 operands, not 1"},
		{"hmoveto, wrong count", cffbuild.CS(cffbuild.HMoveTo, cffbuild.EndChar), "takes 1 operands, not 0"},
		{"hstem, zero operands", cffbuild.CS(cffbuild.HStem, cffbuild.EndChar), "stem operator 1 takes pairs, not 0 operands"},
		{
			"hintmask, odd operands once a width is already settled",
			cffbuild.CS(5, 10, cffbuild.HStemHM, 7, cffbuild.HintMask, cffbuild.Raw{0}, cffbuild.EndChar),
			"mask operator 19 with 1 operands",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &charState{r: newCharRun(1000)}
			_, err := s.exec(c.cs, 0)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("exec err = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// TestArgumentStackOverflow pins the 48-operand argument stack limit of TN #5177 Appendix B: the
// 48th push is taken and the 49th refused.
func TestArgumentStackOverflow(t *testing.T) {
	ops := make([]any, 48)
	for i := range ops {
		ops[i] = 1
	}
	s := &charState{r: newCharRun(1000)}
	if _, err := s.exec(cffbuild.CS(ops...), 0); !errors.Is(err, errNoEnd) || s.n != 48 {
		t.Errorf("exec of 48 operands: err = %v and %d on the stack, want all 48 and no endchar", err, s.n)
	}
	ops = append(ops, 1, cffbuild.EndChar)
	s = &charState{r: newCharRun(1000)}
	_, err := s.exec(cffbuild.CS(ops...), 0)
	if err == nil || !strings.Contains(err.Error(), "stack overflows") {
		t.Errorf("exec err = %v, want a stack overflow refusal at the 49th operand", err)
	}
}

// TestTruncatedOperand pins that an operand form cut off by the end of the charstring is refused,
// each form at the last byte it would read past.
func TestTruncatedOperand(t *testing.T) {
	for _, cs := range [][]byte{{247}, {250}, {251}, {254}, {28, 0}, {255, 0, 0, 0}} {
		s := &charState{r: newCharRun(1000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "charstring operand is truncated") {
			t.Errorf("exec(% x) err = %v, want a truncated operand refusal", cs, err)
		}
	}
}

// TestOperandForms pins the value each of Table 1's operand forms in TN #5177 decodes to, at both
// ends of its range. cffbuild.CS picks the form from the value, so each value here is written in
// the form it names. A misread form moves a point rather than failing, and the 16.16 form's
// fraction is too small for an outline comparison to see at a small value.
func TestOperandForms(t *testing.T) {
	for _, c := range []struct {
		form string
		v    any
		want float64
	}{
		{"one byte", -107, -107},
		{"one byte", 107, 107},
		{"two bytes, positive", 108, 108},
		{"two bytes, positive", 1131, 1131},
		{"two bytes, negative", -108, -108},
		{"two bytes, negative", -1131, -1131},
		{"28 and a short", 1132, 1132},
		{"28 and a short", -1132, -1132},
		{"28 and a short", -32768, -32768},
		{"255 and a 16.16 fixed", 30000.5, 30000.5},
		{"255 and a 16.16 fixed", -0.25, -0.25},
	} {
		s := &charState{r: newCharRun(1000)}
		if _, err := s.exec(cffbuild.CS(c.v), 0); !errors.Is(err, errNoEnd) || s.n != 1 || s.stack[0] != c.want {
			t.Errorf("%s: %v decodes to %v (err %v), want %v", c.form, c.v, s.stack[:s.n], err, c.want)
		}
	}
}

// TestCoordinateRangeLimit pins that a point leaving the 16.16 fixed-point range FreeType computes
// in is refused, since FreeType's sums wrap there (ADD_INT32) and a float's do not. No single
// operand can encode past ~32767 (see cffbuild.CS), so a point gets there across two: across two
// operators, within one operator's lines, or at a control point the curve then comes back from.
// flex1 compares its spans as 16.16 differences, so a span of 32768 is refused as well. Each
// boundary is pinned from both sides.
func TestCoordinateRangeLimit(t *testing.T) {
	cs := cffbuild.CS
	for _, c := range []struct {
		name   string
		cs     []byte
		refuse bool
	}{
		{"the pen, across two operators", cs(20000, 0, cffbuild.RMoveTo,
			15000, 0, cffbuild.RLineTo, cffbuild.EndChar), true},
		{"a moveto's pen, below", cs(0, -20000, cffbuild.RMoveTo, 0, -12769, cffbuild.RMoveTo,
			cffbuild.EndChar), true},
		{"a line out and back in one rlineto", cs(0, 20000, cffbuild.RMoveTo,
			0, 12768, 100, -12768, cffbuild.RLineTo, cffbuild.EndChar), true},
		{"a line to 32767 and back", cs(0, 20000, cffbuild.RMoveTo,
			0, 12767, 100, -12767, cffbuild.RLineTo, cffbuild.EndChar), false},
		{"a line out and back, below", cs(-20000, 0, cffbuild.RMoveTo,
			-12769, 0, 12769, 100, cffbuild.RLineTo, cffbuild.EndChar), true},
		{"a curve's first control point", cs(0, 20000, cffbuild.RMoveTo,
			0, 12768, 0, -12768, 100, 0, cffbuild.RRCurveTo, cffbuild.EndChar), true},
		{"a curve's first control point at 32767", cs(0, 20000, cffbuild.RMoveTo,
			0, 12767, 0, -12767, 100, 0, cffbuild.RRCurveTo, cffbuild.EndChar), false},
		{"a curve's second control point", cs(20000, 0, cffbuild.RMoveTo,
			5000, 0, 7768, 0, -12768, 100, cffbuild.RRCurveTo, cffbuild.EndChar), true},
		{"a curve's second control point at 32767", cs(20000, 0, cffbuild.RMoveTo,
			5000, 0, 7767, 0, -12767, 100, cffbuild.RRCurveTo, cffbuild.EndChar), false},
		{"flex1 spanning 32768 across", cs(-16000, -16000, cffbuild.RMoveTo,
			8000, 0, 8000, 0, 8000, 0, 8000, 0, 768, 0, 0, cffbuild.Flex1, cffbuild.EndChar), true},
		{"flex1 spanning 32767 across", cs(-16000, -16000, cffbuild.RMoveTo,
			8000, 0, 8000, 0, 8000, 0, 8000, 0, 767, 0, 0, cffbuild.Flex1, cffbuild.EndChar), false},
		{"flex1 spanning 32768 up", cs(-16000, -16000, cffbuild.RMoveTo,
			0, 8000, 0, 8000, 0, 8000, 0, 8000, 0, 768, 0, cffbuild.Flex1, cffbuild.EndChar), true},
		{"flex1 spanning 32767 up", cs(-16000, -16000, cffbuild.RMoveTo,
			0, 8000, 0, 8000, 0, 8000, 0, 8000, 0, 767, 0, cffbuild.Flex1, cffbuild.EndChar), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &charState{r: newCharRun(1000)}
			_, err := s.exec(c.cs, 0)
			switch {
			case c.refuse && (err == nil || !strings.Contains(err.Error(), "16.16 range")):
				t.Errorf("exec err = %v, want a refusal for leaving the 16.16 range", err)
			case !c.refuse && err != nil:
				t.Errorf("exec err = %v, want the glyph drawn", err)
			}
		})
	}
}

// TestOutlineBudget pins that the op count Outline returns includes every operand and operator —
// Square() is exactly 9 tokens (100 100 rmoveto 300 300 -300 -300 hlineto endchar) — that a budget
// of exactly that count is enough, and that one fewer fails with ErrGlyphBudget.
func TestOutlineBudget(t *testing.T) {
	square := cffbuild.Square()

	t.Run("a budget of the exact op count is enough", func(t *testing.T) {
		_, ops, err := outlineOf(t, square, 9)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		if ops != 9 {
			t.Errorf("ops = %d, want 9 (2 operands + rmoveto + 4 operands + hlineto + endchar)", ops)
		}
	})

	t.Run("one op too few fails with ErrGlyphBudget", func(t *testing.T) {
		out, _, err := outlineOf(t, square, 8)
		if !errors.Is(err, ErrGlyphBudget) {
			t.Errorf("err = %v, want ErrGlyphBudget", err)
		}
		if out != nil {
			t.Errorf("outline = %v, want nil on budget failure", out)
		}
	})
}

// TestEmptyGlyph pins that a glyph which is just endchar — a space — is nil with no error.
func TestEmptyGlyph(t *testing.T) {
	out, ops, err := outlineOf(t, cffbuild.CS(cffbuild.EndChar), 100)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if out != nil {
		t.Errorf("outline = %v, want nil", out)
	}
	if ops != 1 {
		t.Errorf("ops = %d, want 1 (just endchar)", ops)
	}
}

// TestFixedFormOperandRefusals pins that a subroutine number or a seac achar/bchar written in the
// 255 (16.16 fixed) operand form is refused even when its value has no fraction: FreeType's
// cf2_stack_popInt (psintrp.c:1006 for callsubr/callgsubr, :2534-2535 for seac's implied form)
// fails the glyph on a Fixed-typed stack entry, regardless of its value. The same charstring with
// the operand in an ordinary integer form, pinned alongside, is accepted -- adx and ady are
// popFixed'd either form and so get no such check.
func TestFixedFormOperandRefusals(t *testing.T) {
	square, diamond := cffbuild.Square(), cffbuild.Diamond()
	line := cffbuild.CS(10, 0, cffbuild.RLineTo, cffbuild.Return)

	t.Run("callsubr", func(t *testing.T) {
		glyph := func(num any) cffbuild.Builder {
			return cffbuild.Builder{
				Subrs: [][]byte{line},
				Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
					0, 0, cffbuild.RMoveTo, num, cffbuild.CallSubr, cffbuild.EndChar)}},
			}
		}
		if _, _, err := outlineOfBuilder(t, glyph(-107), 1, 1000); err != nil {
			t.Errorf("integer form: Outline err = %v, want it accepted", err)
		}
		_, _, err := outlineOfBuilder(t, glyph(-107.0), 1, 1000)
		if err == nil || !strings.Contains(err.Error(), "255 (16.16") {
			t.Errorf("255 form: Outline err = %v, want a refusal naming the fixed-point form", err)
		}
	})

	t.Run("callgsubr", func(t *testing.T) {
		glyph := func(num any) cffbuild.Builder {
			return cffbuild.Builder{
				GSubrs: [][]byte{line},
				Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(
					0, 0, cffbuild.RMoveTo, num, cffbuild.CallGSubr, cffbuild.EndChar)}},
			}
		}
		if _, _, err := outlineOfBuilder(t, glyph(-107), 1, 1000); err != nil {
			t.Errorf("integer form: Outline err = %v, want it accepted", err)
		}
		_, _, err := outlineOfBuilder(t, glyph(-107.0), 1, 1000)
		if err == nil || !strings.Contains(err.Error(), "255 (16.16") {
			t.Errorf("255 form: Outline err = %v, want a refusal naming the fixed-point form", err)
		}
	})

	t.Run("seac achar", func(t *testing.T) {
		glyph := func(achar any) cffbuild.Builder {
			return cffbuild.Builder{Glyphs: []cffbuild.Glyph{
				{SID: 34, CharString: square},
				{SID: 35, CharString: diamond},
				{Name: "C", CharString: cffbuild.CS(10, 20, 65, achar, cffbuild.EndChar)},
			}}
		}
		if _, _, err := outlineOfBuilder(t, glyph(66), 3, 10000); err != nil {
			t.Errorf("integer form: Outline err = %v, want it accepted", err)
		}
		_, _, err := outlineOfBuilder(t, glyph(66.0), 3, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac character 66") {
			t.Errorf("255 form: Outline err = %v, want a refusal naming achar 66", err)
		}
	})

	t.Run("seac bchar", func(t *testing.T) {
		glyph := func(bchar any) cffbuild.Builder {
			return cffbuild.Builder{Glyphs: []cffbuild.Glyph{
				{SID: 34, CharString: square},
				{SID: 35, CharString: diamond},
				{Name: "C", CharString: cffbuild.CS(10, 20, bchar, 66, cffbuild.EndChar)},
			}}
		}
		if _, _, err := outlineOfBuilder(t, glyph(65), 3, 10000); err != nil {
			t.Errorf("integer form: Outline err = %v, want it accepted", err)
		}
		_, _, err := outlineOfBuilder(t, glyph(65.0), 3, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac character 65") {
			t.Errorf("255 form: Outline err = %v, want a refusal naming bchar 65", err)
		}
	})

	// R#1 (M4): dropWidth shifts the fixed-form flags along with the stack, so a seac carrying a
	// width still checks each operand's own flag rather than the flag of the slot it used to sit
	// in. Without that shift, achar's 255-form flag would be read from ady's old slot instead.
	t.Run("seac achar, with a width", func(t *testing.T) {
		glyph := func(achar any) cffbuild.Builder {
			return cffbuild.Builder{Glyphs: []cffbuild.Glyph{
				{SID: 34, CharString: square},
				{SID: 35, CharString: diamond},
				{Name: "C", CharString: cffbuild.CS(250, 200, 450, 65, achar, cffbuild.EndChar)},
			}}
		}
		out, _, err := outlineOfBuilder(t, glyph(66), 3, 10000)
		if err != nil {
			t.Errorf("integer form: Outline err = %v, want it accepted", err)
		}
		if len(out) == 0 {
			t.Error("integer form: Outline is empty, want both seac components drawn")
		}
		_, _, err = outlineOfBuilder(t, glyph(66.0), 3, 10000)
		if err == nil || !strings.Contains(err.Error(), "seac character 66") {
			t.Errorf("255 form: Outline err = %v, want a refusal naming achar 66", err)
		}
	})
}

// stemOps returns n hstemhm stem declarations, spread across ceil(n/24) operators so the 48-slot
// argument stack never overflows (24 pairs = 48 operands per operator).
func stemOps(n int) []byte {
	var ops []any
	for left := n; left > 0; left -= 24 {
		for i := 0; i < min(left, 24); i++ {
			ops = append(ops, 10, 20)
		}
		ops = append(ops, cffbuild.HStemHM)
	}
	return cffbuild.CS(ops...)
}

// bareMask returns a hintmask operator with no operands of its own, carrying the ceil(n/8) mask
// bytes n declared stems need.
func bareMask(n int) []any {
	return []any{cffbuild.HintMask, cffbuild.Raw(make([]byte, (n+7)/8))}
}

// TestStemLimitWithoutMask pins R#5: a stem operator only pushes to the stem arrays, never
// refusing on its own (psintrp.c's stem cases). FreeType counts and caps stems only where it
// actually reads the total: a hintmask or cntrmask's cf2_hintmask_read, and the first moveto's
// cf2_hintmap_build. A charstring that reaches endchar without running either draws however many
// stems it declared.
func TestStemLimitWithoutMask(t *testing.T) {
	t.Run("97 stems then endchar, with no mask and no moveto, is accepted", func(t *testing.T) {
		cs := append(stemOps(97), cffbuild.CS(cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want 97 stems with no mask or moveto accepted", err)
		}
	})

	t.Run("97 stems then rmoveto is refused", func(t *testing.T) {
		cs := append(stemOps(97), cffbuild.CS(100, 100, cffbuild.RMoveTo, cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "past the limit of 96") {
			t.Errorf("exec err = %v, want a refusal past the 96-stem limit", err)
		}
	})

	t.Run("96 stems then rmoveto is accepted", func(t *testing.T) {
		cs := append(stemOps(96), cffbuild.CS(100, 100, cffbuild.RMoveTo, cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want 96 stems then a moveto accepted", err)
		}
	})

	t.Run("97 stems then a bare hintmask is refused", func(t *testing.T) {
		cs := append(stemOps(97), cffbuild.CS(append(bareMask(97), cffbuild.EndChar)...)...)
		s := &charState{r: newCharRun(10000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "past the limit of 96") {
			t.Errorf("exec err = %v, want a refusal past the 96-stem limit", err)
		}
	})

	t.Run("96 stems then a bare hintmask is accepted", func(t *testing.T) {
		cs := append(stemOps(96), cffbuild.CS(append(bareMask(96), cffbuild.EndChar)...)...)
		s := &charState{r: newCharRun(10000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want 96 stems then a bare hintmask accepted", err)
		}
	})

	// R#0: every case above uses hintmask (op 19); checkStems runs the same for cntrmask (op 20),
	// but nothing pinned that half of the shared case, and no moveto follows here to re-check
	// the count on its own.
	t.Run("97 stems then a bare cntrmask is refused", func(t *testing.T) {
		cs := append(stemOps(97),
			cffbuild.CS(cffbuild.CntrMask, cffbuild.Raw(make([]byte, 13)), cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "past the limit of 96") {
			t.Errorf("exec err = %v, want a refusal past the 96-stem limit", err)
		}
	})

	t.Run("96 stems then a bare cntrmask is accepted", func(t *testing.T) {
		cs := append(stemOps(96),
			cffbuild.CS(cffbuild.CntrMask, cffbuild.Raw(make([]byte, 12)), cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want 96 stems then a bare cntrmask accepted", err)
		}
	})
}

// TestMaskImplicitVstemLimit pins R#0: a hintmask's own leftover operand pair is an implicit
// vstem, added to the running total before checkStems runs, so it can itself push the total past
// maxStems even though the mask carries no explicit stem operator of its own.
func TestMaskImplicitVstemLimit(t *testing.T) {
	t.Run("96 stems, then a hintmask whose implied vstem is the 97th, is refused", func(t *testing.T) {
		cs := append(stemOps(96),
			cffbuild.CS(10, 20, cffbuild.HintMask, cffbuild.Raw(make([]byte, 13)), cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "past the limit of 96") {
			t.Errorf("exec err = %v, want a refusal past the 96-stem limit", err)
		}
	})

	t.Run("95 stems, then a hintmask whose implied vstem is the 96th, is accepted", func(t *testing.T) {
		cs := append(stemOps(95),
			cffbuild.CS(10, 20, cffbuild.HintMask, cffbuild.Raw(make([]byte, 12)), cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want 95 stems plus an implied vstem accepted", err)
		}
	})

	// R#0: the same implicit-vstem overflow, but through cntrmask rather than hintmask.
	t.Run("96 stems, then a cntrmask whose implied vstem is the 97th, is refused", func(t *testing.T) {
		cs := append(stemOps(96),
			cffbuild.CS(10, 20, cffbuild.CntrMask, cffbuild.Raw(make([]byte, 13)), cffbuild.EndChar)...)
		s := &charState{r: newCharRun(10000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "past the limit of 96") {
			t.Errorf("exec err = %v, want a refusal past the 96-stem limit", err)
		}
	})

	// R#0: the accept side of the same cntrmask arithmetic, with a square drawn afterward and its
	// outline checked point-for-point -- nothing above draws past the mask. A mutant that counts
	// cntrmask's implied vstem twice pushes this case's total to 97 and refuses what should draw.
	t.Run("95 stems, then a cntrmask whose implied vstem is the 96th, then a square, is accepted", func(t *testing.T) {
		cs := append(stemOps(95),
			cffbuild.CS(10, 20, cffbuild.CntrMask, cffbuild.Raw(make([]byte, 12)),
				100, 100, cffbuild.RMoveTo, 300, 300, -300, -300, cffbuild.HLineTo, cffbuild.EndChar)...)
		out, _, err := outlineOf(t, cs, 10000)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		want := Outline{
			{Op: SegMove, P: [3]Point{{100, 100}}},
			{Op: SegLine, P: [3]Point{{400, 100}}},
			{Op: SegLine, P: [3]Point{{400, 400}}},
			{Op: SegLine, P: [3]Point{{100, 400}}},
			{Op: SegLine, P: [3]Point{{100, 100}}},
			{Op: SegClose},
		}
		if !sameOutline(out, want) {
			t.Errorf("outline = %v\nwant     %v", out, want)
		}
	})
}

// TestMaskOperandsWithoutMoveto pins R#2: the mask condition's s.masked half, separate from
// s.moved -- a second hintmask carrying operands is refused once a mask has already run, even
// before any moveto, which TestStemAndMaskAfterMoveto cannot show since every case there follows
// an rmoveto.
func TestMaskOperandsWithoutMoveto(t *testing.T) {
	t.Run("a second hintmask with operands, before any moveto, is refused", func(t *testing.T) {
		cs := cffbuild.CS(
			0, 50, cffbuild.HStem,
			cffbuild.HintMask, cffbuild.Raw{0x80},
			10, 20, cffbuild.HintMask, cffbuild.Raw{0x0E},
			cffbuild.EndChar)
		s := &charState{r: newCharRun(1000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "mask operator 19 with 2 operands") {
			t.Errorf("exec err = %v, want a refusal for a second hintmask carrying operands", err)
		}
	})

	t.Run("the same without the first hintmask is accepted", func(t *testing.T) {
		cs := cffbuild.CS(
			0, 50, cffbuild.HStem,
			10, 20, cffbuild.HintMask, cffbuild.Raw{0xC0},
			cffbuild.EndChar)
		s := &charState{r: newCharRun(1000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want a single hintmask with an implicit vstem accepted", err)
		}
	})
}

// TestStemAndMaskAfterMoveto pins C14: once the first moveto has built the hint map, FreeType
// ignores a stem operator but still clears the stack -- the stem case breaks before cf2_doStems
// once the hint mask is valid (psintrp.c:800 for hstem/hstemhm, :831 for vstem/vstemhm), falls
// out of the switch, and reaches cf2_stack_clear (:3025). A hintmask or cntrmask carrying more than one leftover operand
// breaks without reading its mask bytes instead, when the mask is already valid (psintrp.c's
// hint-mask case, :2581-2586). This code refuses any operand there, one included: stricter than
// FreeType for exactly one, and the safe direction, since a producer that leaves one operand
// behind is already off the grammar. A bare hintmask (no operands) still reads its mask bytes, and
// a cntrmask never sets the flag a later stem is checked against.
func TestStemAndMaskAfterMoveto(t *testing.T) {
	t.Run("a stem after an rmoveto is refused", func(t *testing.T) {
		cs := cffbuild.CS(5, 5, cffbuild.RMoveTo, 10, 20, cffbuild.HStem, cffbuild.EndChar)
		s := &charState{r: newCharRun(1000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "after a hintmask or the first moveto") {
			t.Errorf("exec err = %v, want a refusal for a stem declared after the first moveto", err)
		}
	})

	t.Run("a hintmask with stem operands after an rmoveto is refused", func(t *testing.T) {
		cs := cffbuild.CS(5, 5, cffbuild.RMoveTo, 10, 20, cffbuild.HintMask, cffbuild.Raw{0x80}, cffbuild.EndChar)
		s := &charState{r: newCharRun(1000)}
		_, err := s.exec(cs, 0)
		if err == nil || !strings.Contains(err.Error(), "mask operator 19 with 2 operands") {
			t.Errorf("exec err = %v, want a refusal for a hintmask carrying stems after a moveto", err)
		}
	})

	t.Run("a hintmask with no operands after an rmoveto is accepted", func(t *testing.T) {
		// 3 stems declared before the moveto: the mask reads its ceil(3/8)=1 byte, the same count
		// FreeType uses, since it comes only from stems declared before the hint map was built.
		cs := cffbuild.CS(
			10, 20, 10, 20, 10, 20, cffbuild.HStemHM,
			5, 5, cffbuild.RMoveTo,
			cffbuild.HintMask, cffbuild.Raw{0x80},
			cffbuild.EndChar)
		s := &charState{r: newCharRun(1000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want a bare hintmask after a moveto accepted", err)
		}
	})

	t.Run("a cntrmask, then stems, then rmoveto is accepted", func(t *testing.T) {
		cs := cffbuild.CS(
			10, 20, cffbuild.CntrMask, cffbuild.Raw{0x80},
			10, 20, cffbuild.HStem,
			5, 5, cffbuild.RMoveTo,
			cffbuild.EndChar)
		s := &charState{r: newCharRun(1000)}
		if _, err := s.exec(cs, 0); err != nil {
			t.Errorf("exec err = %v, want a cntrmask before any moveto, then stems, accepted", err)
		}
	})
}

// TestGlyphSegmentLimit pins C9's per-glyph part: a CFF outline is capped at maxGlyphSegments, the
// same bound truetype.go charges a composite. manyLines calls a one-segment subroutine n times
// after an rmoveto, so the outline grows by exactly n+2 segments: the SegMove line() opens lazily,
// the n SegLines, and endchar's SegClose. The fan-out keeps the charstring's own op count far below
// the budget passed, so the boundary is on segments, not on ErrGlyphBudget.
func TestGlyphSegmentLimit(t *testing.T) {
	// Two subroutines, called alternately, so the pen oscillates between (0,0) and (1,0) and never
	// nears the 16.16 range limit however many times they are called.
	manyLines := func(n int) cffbuild.Builder {
		var ops []any
		ops = append(ops, 0, 0, cffbuild.RMoveTo)
		for i := 0; i < n; i++ {
			if i%2 == 0 {
				ops = append(ops, -107, cffbuild.CallSubr) // subr 0, bias 107: +1,0
			} else {
				ops = append(ops, -106, cffbuild.CallSubr) // subr 1, bias 107: -1,0
			}
		}
		ops = append(ops, cffbuild.EndChar)
		return cffbuild.Builder{
			Subrs: [][]byte{
				cffbuild.CS(1, 0, cffbuild.RLineTo, cffbuild.Return),
				cffbuild.CS(-1, 0, cffbuild.RLineTo, cffbuild.Return),
			},
			Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(ops...)}},
		}
	}
	const budget = 2000000

	t.Run("exactly maxGlyphSegments is accepted", func(t *testing.T) {
		out, ops, err := outlineOfBuilder(t, manyLines(maxGlyphSegments-2), 1, budget)
		if err != nil {
			t.Fatalf("Outline: %v", err)
		}
		if len(out) != maxGlyphSegments {
			t.Errorf("segments = %d, want %d", len(out), maxGlyphSegments)
		}
		if ops > budget/2 {
			t.Errorf("ops = %d, want it far under the %d budget", ops, budget)
		}
	})

	t.Run("one more than maxGlyphSegments is refused", func(t *testing.T) {
		_, _, err := outlineOfBuilder(t, manyLines(maxGlyphSegments-1), 1, budget)
		want := fmt.Sprintf("more than %d segments", maxGlyphSegments)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Outline err = %v, want a refusal containing %q", err, want)
		}
	})
}

// TestMovetoClosePastSegmentLimit pins R#3 (M3): a moveto's own close, not only a line or a curve,
// can be the segment that first passes maxGlyphSegments, and moveto must return that error rather
// than drop it. manyLines (as TestGlyphSegmentLimit builds it) is driven to exactly
// maxGlyphSegments segments -- a lazily-opened move plus maxGlyphSegments-1 lines -- and then one
// more rmoveto, whose close is the segment that crosses the cap.
func TestMovetoClosePastSegmentLimit(t *testing.T) {
	var ops []any
	ops = append(ops, 0, 0, cffbuild.RMoveTo)
	for i := 0; i < maxGlyphSegments-1; i++ {
		if i%2 == 0 {
			ops = append(ops, -107, cffbuild.CallSubr) // subr 0, bias 107: +1,0
		} else {
			ops = append(ops, -106, cffbuild.CallSubr) // subr 1, bias 107: -1,0
		}
	}
	ops = append(ops, 5, 5, cffbuild.RMoveTo, cffbuild.EndChar) // the close is segment maxGlyphSegments+1
	b := cffbuild.Builder{
		Subrs: [][]byte{
			cffbuild.CS(1, 0, cffbuild.RLineTo, cffbuild.Return),
			cffbuild.CS(-1, 0, cffbuild.RLineTo, cffbuild.Return),
		},
		Glyphs: []cffbuild.Glyph{{CharString: cffbuild.CS(ops...)}},
	}
	out, _, err := outlineOfBuilder(t, b, 1, 2000000)
	want := fmt.Sprintf("more than %d segments", maxGlyphSegments)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Outline err = %v, want a refusal containing %q", err, want)
	}
	if len(out) != 0 {
		t.Errorf("segments = %d, want none returned once the moveto's close is refused", len(out))
	}
}

// fanOutGlyph builds a charstring whose top calls one subroutine twice, that subroutine calls
// another eight times, levels deep, and the bottom subroutine is leaf -- a fan-out whose segment
// count is (leaf's segments) * 8^levels * 2, chosen far past maxGlyphSegments so a refusal that
// stops early is distinguishable from one that runs the whole fan-out first.
func fanOutGlyph(leaf []byte, levels int) cffbuild.Builder {
	subrs := [][]byte{leaf}
	for lvl := 1; lvl <= levels; lvl++ {
		var items []any
		for i := 0; i < 8; i++ {
			items = append(items, lvl-1-107, cffbuild.CallSubr) // subr lvl-1, bias 107
		}
		subrs = append(subrs, cffbuild.CS(append(items, cffbuild.Return)...))
	}
	top := cffbuild.CS(0, 0, cffbuild.RMoveTo,
		levels-107, cffbuild.CallSubr, levels-107, cffbuild.CallSubr, cffbuild.EndChar)
	return cffbuild.Builder{Subrs: subrs, Glyphs: []cffbuild.Glyph{{CharString: top}}}
}

// TestPathErrorPropagationCost pins R#3 (M5, M6, M7): once a line or a curve passes
// maxGlyphSegments, path's and line's/curve's own error returns must stop the fan-out there, not
// let it keep interpreting subroutines it will refuse anyway. Both a line leaf and a curve leaf
// are fanned out to many times maxGlyphSegments; a correct early stop spends only as many ops as
// reaching the cap takes (measured at 143,186 for lines and 415,449 for curves), not the several
// hundred thousand more that dropping the check lets the fan-out spend before it finally
// refuses at endchar.
func TestPathErrorPropagationCost(t *testing.T) {
	const budget = 4194304

	t.Run("lines", func(t *testing.T) {
		var leaf []any
		for i := 0; i < 24; i++ { // 24 lines = 48 operands, the argument stack limit
			if i%2 == 0 {
				leaf = append(leaf, 1, 0)
			} else {
				leaf = append(leaf, -1, 0)
			}
		}
		b := fanOutGlyph(cffbuild.CS(append(leaf, cffbuild.RLineTo, cffbuild.Return)...), 4)
		// 24 * 8^4 * 2 = 196,608 lines fanned out; far past maxGlyphSegments (65,536).
		const wantUnder = 250000 // comfortably above the 143,186 ops reaching the cap takes
		_, ops, err := outlineOfBuilder(t, b, 1, budget)
		want := fmt.Sprintf("more than %d segments", maxGlyphSegments)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Outline err = %v, want a refusal containing %q", err, want)
		}
		if ops > wantUnder {
			t.Errorf("ops = %d, want the refusal to stop near the segment cap, not run the whole fan-out", ops)
		}
	})

	t.Run("curves", func(t *testing.T) {
		var leaf []any
		for block := 0; block < 2; block++ {
			for i := 0; i < 8; i++ { // 8 curves = 48 operands, the argument stack limit
				if i%2 == 0 {
					leaf = append(leaf, 1, 0, 1, 0, 1, 0)
				} else {
					leaf = append(leaf, -1, 0, -1, 0, -1, 0)
				}
			}
			leaf = append(leaf, cffbuild.RRCurveTo)
		}
		b := fanOutGlyph(cffbuild.CS(append(leaf, cffbuild.Return)...), 4)
		// 16 * 8^4 * 2 = 131,072 curves fanned out; far past maxGlyphSegments (65,536).
		const wantUnder = 600000 // comfortably above the 415,449 ops reaching the cap takes
		_, ops, err := outlineOfBuilder(t, b, 1, budget)
		want := fmt.Sprintf("more than %d segments", maxGlyphSegments)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Outline err = %v, want a refusal containing %q", err, want)
		}
		if ops > wantUnder {
			t.Errorf("ops = %d, want the refusal to stop near the segment cap, not run the whole fan-out", ops)
		}
	})
}

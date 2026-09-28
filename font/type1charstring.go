package font

import (
	"bytes"
	"errors"
	"fmt"
	"math"
)

// The Type 1 charstring interpreter: chapter 6 of the Adobe Type 1 Font Format, read the way
// FreeType's Adobe engine reads it in its Type 1 mode (psaux/psintrp.c with isT1), since that is
// what pdfium draws with.
//
// # What it draws
//
// The path operators, hsbw and sbw, closepath, callsubr and return, div, seac, and the four
// othersubrs every Type 1 font carries: 0, 1 and 2, which draw a flex as two curves, and 3, hint
// replacement, which draws nothing. Hints are counted, for FreeType's limit, and not applied:
// pdfium loads glyphs unhinted.
//
// # What it refuses
//
// Wherever FreeType's reading depends on how its engine happens to be built rather than on the
// format. An othersubr is a PostScript procedure, and FreeType runs none of them: it knows what
// 0 to 3 do and pretends the rest were never called. So this takes each only in the byte sequence
// the format prints for it — 0 followed by exactly pop pop setcurrentpoint, 3 followed by pop — and
// refuses a pop or setcurrentpoint anywhere else, since there is no result for it to take. It
// refuses an operator count other than the one an operator takes, where FreeType would read
// whatever the stack holds; an integer past ±32000 that is not a div operand, which FreeType
// carries through as an unscaled integer; a path drawn before the first moveto, or after
// closepath from a point no moveto set, where FreeType's current point and the format's part
// ways; an operator during a flex that is not a move, where FreeType would draw through the
// reference point; a seac after the glyph has drawn, whose earlier path FreeType drops; and the
// operators Type 1 does not define.

const (
	maxT1SubrDepth = 8     // FreeType fails a callsubr from the eighth nesting level
	maxT1Int       = 32000 // past this an integer is only a div operand, per §6.2 of the format
)

// Outline returns a glyph's contours in 1/1000 em, and how many charstring operations it took;
// budget bounds them as it does for CFF. An empty outline — a space — is nil with no error.
func (t *Type1) Outline(gid uint16, budget int) (Outline, int, error) {
	if int(gid) >= len(t.glyphs) {
		return nil, 0, fmt.Errorf("font: Type 1 glyph %d of %d", gid, len(t.glyphs))
	}
	r := &charRun{t1: t, budget: budget}
	if err := r.t1Glyph(t.glyphs[gid], 0, 0); err != nil {
		return nil, r.ops, fmt.Errorf("font: Type 1 glyph %d: %w", gid, err)
	}
	if len(r.out) == 0 {
		return nil, r.ops, nil
	}
	return r.out, r.ops, nil
}

// t1State is one Type 1 charstring's interpreter state, on top of the pen and outline a Type 2
// charstring keeps. charState.fixed marks a div result, the one operand FreeType holds as a
// fraction.
type t1State struct {
	charState
	large    [maxCharStack]bool // the operand is an integer past ±maxT1Int
	largeInt bool               // FreeType's large_int: set by such an integer, reset by a non-escape operator

	begun          bool // hsbw or sbw has run
	sbx            float64
	startX, startY float64 // where the last moveto left the pen: the contour's start
	closed         bool    // closepath has run since the last moveto or segment

	inFlex     bool
	flexN      int        // the othersubr 2 calls so far
	flex       [6]float64 // the three points of the curve being collected
	preX, preY float64    // the pen when the flex began
	midX, midY float64    // where the first flex curve ends
}

// t1Glyph interprets one charstring with its origin at (x, y).
func (r *charRun) t1Glyph(cs []byte, x, y float64) error {
	s := &t1State{charState: charState{r: r, x: x, y: y}}
	ended, err := s.exec(cs, 0)
	if err == nil && !ended {
		err = errNoEnd
	}
	return err
}

// exec runs a charstring or subroutine, reporting whether it reached endchar or seac.
func (s *t1State) exec(cs []byte, depth int) (bool, error) {
	for p := 0; p < len(cs); {
		if err := s.spend(1); err != nil {
			return false, err
		}
		b := cs[p]
		p++

		// Operands, per §6.2 of the format.
		if b >= 32 {
			var v float64
			switch {
			case b <= 246:
				v = float64(int(b) - 139)
			case b <= 254:
				if p >= len(cs) {
					return false, errors.New("charstring operand is truncated")
				}
				v = float64((int(b)-247)*256 + int(cs[p]) + 108)
				if b >= 251 {
					v = float64(-(int(b)-251)*256 - int(cs[p]) - 108)
				}
				p++
			default:
				if p+4 > len(cs) {
					return false, errors.New("charstring operand is truncated")
				}
				v = float64(int32(be32(cs, p))) // #nosec G115 -- the form is a signed integer
				p += 4
			}
			if s.n == maxCharStack {
				return false, errors.New("charstring argument stack overflows")
			}
			s.stack[s.n], s.fixed[s.n] = v, false
			s.large[s.n] = math.Abs(v) > maxT1Int
			s.largeInt = s.largeInt || s.large[s.n]
			s.n++
			continue
		}

		op := int(b)
		if b == 12 {
			if p >= len(cs) {
				return false, errors.New("charstring ends inside an escaped operator")
			}
			op = 1200 + int(cs[p])
			p++
		}
		if op != 1212 {
			for _, l := range s.large[:s.n] {
				if l {
					return false, fmt.Errorf("an integer past ±%d as an operand of operator %d, not of div", maxT1Int, op)
				}
			}
		}
		// div may compute hsbw's operands: a width of 1000/3 em is written 0 1000 3 div hsbw.
		if !s.begun && op != 13 && op != 1207 && op != 1212 {
			return false, fmt.Errorf("operator %d before hsbw or sbw", op)
		}
		if s.inFlex && !flexOp(op) {
			return false, fmt.Errorf("operator %d inside a flex", op)
		}

		switch op {
		case 10: // callsubr
			if s.n == 0 {
				return false, errors.New("subroutine call without a number")
			}
			s.n--
			if s.fixed[s.n] {
				return false, errors.New("a subroutine number that is a div result")
			}
			if depth >= maxT1SubrDepth {
				return false, fmt.Errorf("subroutines nest more than %d deep", maxT1SubrDepth)
			}
			sub, ok := s.r.t1.subrs[int(s.stack[s.n])]
			if !ok {
				return false, fmt.Errorf("subroutine %v is not defined", s.stack[s.n])
			}
			s.largeInt = false
			ended, err := s.exec(sub, depth+1)
			if err != nil || ended {
				return ended, err
			}
			continue

		case 11: // return
			if depth == 0 {
				return false, errors.New("return outside a subroutine")
			}
			s.largeInt = false
			return false, nil

		case 1212: // div
			if s.n < 2 {
				return false, countErr(op, s.n)
			}
			a, d := s.stack[s.n-2], s.stack[s.n-1]
			if s.largeInt && (s.fixed[s.n-2] || s.fixed[s.n-1]) {
				return false, errors.New("div of a div result while an integer past ±32000 is pending")
			}
			if d == 0 {
				return false, errors.New("div by zero")
			}
			s.n--
			s.stack[s.n-1], s.fixed[s.n-1], s.large[s.n-1] = a/d, true, false
			continue

		case 1216: // callothersubr
			var err error
			if p, err = s.othersubr(cs, p); err != nil {
				return false, err
			}
			continue

		case 14: // endchar
			if s.n != 0 {
				return false, countErr(op, s.n)
			}
			if s.stems > maxStems {
				return false, fmt.Errorf("%d stem hints, past the limit of %d", s.stems, maxStems)
			}
			return true, s.close()

		case 1206: // seac
			return true, s.seac()

		default:
			if err := s.op(op); err != nil {
				return false, err
			}
		}
		s.largeInt = false
		s.n = 0
	}
	return false, errNoEnd
}

// spend charges n operations to the glyph's budget.
func (s *t1State) spend(n int) error {
	s.r.ops += n
	if s.r.ops > s.r.budget {
		return ErrGlyphBudget
	}
	return nil
}

// flexOp reports the operators a flex may run between its othersubr calls: the moves that place
// its points, and the calls that reach the othersubrs.
func flexOp(op int) bool {
	switch op {
	case 4, 21, 22, 10, 11, 1200, 1212, 1216:
		return true
	}
	return false
}

// op runs one of the stack-clearing operators other than endchar and seac.
func (s *t1State) op(op int) error {
	a, n := s.stack[:s.n], s.n
	want := map[int]int{1: 2, 3: 2, 1201: 6, 1202: 6, 1200: 0, 13: 2, 1207: 4, 9: 0,
		21: 2, 22: 1, 4: 1, 5: 2, 6: 1, 7: 1, 8: 6, 30: 4, 31: 4}
	w, ok := want[op]
	switch {
	case op == 1217 || op == 1233:
		return fmt.Errorf("operator %d without the othersubr whose result it takes", op)
	case !ok:
		return fmt.Errorf("charstring operator %d, which Type 1 does not define or is refused", op)
	case n != w:
		return countErr(op, n)
	}
	switch op {
	case 13, 1207: // hsbw, sbw
		if s.begun {
			return errors.New("a second hsbw or sbw")
		}
		s.begun = true
		s.sbx = a[0]
		s.x += a[0]
		if op == 1207 {
			s.y += a[1]
		}
		return s.inRange(len(s.r.out))
	case 1, 3: // hstem, vstem
		s.stems++
	case 1201, 1202: // vstem3, hstem3
		s.stems += 3
	case 1200: // dotsection
	case 9: // closepath: the pen stays where it is
		s.closed = true
		return s.close()
	case 21:
		return s.moveto(a[0], a[1])
	case 22:
		return s.moveto(a[0], 0)
	case 4:
		return s.moveto(0, a[0])
	default:
		if err := s.canDraw(); err != nil {
			return err
		}
		s.closed = false
		from := len(s.r.out)
		var err error
		switch op {
		case 5: // rlineto
			err = s.line(a[0], a[1])
		case 6: // hlineto
			err = s.line(a[0], 0)
		case 7: // vlineto
			err = s.line(0, a[0])
		case 8: // rrcurveto
			err = s.curve(a[0], a[1], a[2], a[3], a[4], a[5])
		case 30: // vhcurveto
			err = s.curve(0, a[0], a[1], a[2], a[3], 0)
		case 31: // hvcurveto
			err = s.curve(a[0], 0, a[1], a[2], 0, a[3])
		}
		if err != nil {
			return err
		}
		return s.inRange(from)
	}
	return nil
}

// canDraw refuses a segment with no start the format and FreeType agree on: before the first
// moveto, or after closepath from anywhere but the point the contour started at.
func (s *t1State) canDraw() error {
	if !s.moved {
		return errors.New("path operator before the first moveto")
	}
	if s.closed && (s.x != s.startX || s.y != s.startY) {
		return errors.New("a path drawn after closepath from a point no moveto set")
	}
	return nil
}

// moveto closes any open contour and moves the pen, except inside a flex, where a move only
// places the next flex point.
func (s *t1State) moveto(dx, dy float64) error {
	if !s.inFlex {
		if err := s.close(); err != nil {
			return err
		}
	}
	s.x += dx
	s.y += dy
	if !s.inFlex {
		s.moved, s.closed = true, false
		s.startX, s.startY = s.x, s.y
	}
	return s.inRange(len(s.r.out))
}

// othersubr runs callothersubr: its number and argument count on top of the stack, and exactly
// that many arguments beneath them. It returns where the charstring continues, past the pops a
// result was taken with.
func (s *t1State) othersubr(cs []byte, p int) (int, error) {
	if s.n < 2 {
		return p, errors.New("callothersubr without a number and an argument count")
	}
	num, cnt := s.stack[s.n-1], s.stack[s.n-2]
	if s.fixed[s.n-1] || s.fixed[s.n-2] {
		return p, errors.New("callothersubr with a div result for its number or count")
	}
	s.n -= 2
	if cnt < 0 || int(cnt) != s.n {
		return p, fmt.Errorf("othersubr %v with %v arguments over a stack of %d", num, cnt, s.n)
	}
	switch {
	case num == 1: // start a flex
		if cnt != 0 || s.inFlex {
			return p, errors.New("othersubr 1 inside a flex, or with arguments")
		}
		if err := s.canDraw(); err != nil {
			return p, err
		}
		s.inFlex, s.flexN = true, 0
		s.preX, s.preY = s.x, s.y
	case num == 2: // a flex point: the reference point, then three for each curve
		if cnt != 0 || !s.inFlex {
			return p, errors.New("othersubr 2 outside a flex, or with arguments")
		}
		idx := s.flexN
		s.flexN++
		if idx > 6 {
			return p, errors.New("a flex of more than seven points")
		}
		if idx > 0 {
			o := (idx - 1) % 3 * 2
			s.flex[o], s.flex[o+1] = s.x, s.y
		}
		switch idx {
		case 3:
			s.midX, s.midY = s.x, s.y
			return p, s.flexCurve(s.preX, s.preY)
		case 6:
			return p, s.flexCurve(s.midX, s.midY)
		}
	case num == 0: // end a flex
		if cnt != 3 || !s.inFlex || s.flexN != 7 {
			return p, errors.New("othersubr 0 without the seven points of a flex")
		}
		if s.stack[1] != s.x || s.stack[2] != s.y {
			return p, fmt.Errorf("a flex ending at (%g, %g) with the pen at (%g, %g)", s.stack[1], s.stack[2], s.x, s.y)
		}
		if !bytes.HasPrefix(cs[p:], []byte{12, 17, 12, 17, 12, 33}) {
			return p, errors.New("othersubr 0 not followed by pop pop setcurrentpoint")
		}
		if err := s.spend(3); err != nil {
			return p, err
		}
		// pop pop takes the end point back and setcurrentpoint puts the pen where it already is.
		s.inFlex = false
		s.n = 0
		s.largeInt = false
		return p + 6, nil
	case num == 3: // hint replacement: its argument comes back, the subroutine to call
		if cnt != 1 {
			return p, errors.New("othersubr 3 without its one argument")
		}
		if !bytes.HasPrefix(cs[p:], []byte{12, 17}) {
			return p, errors.New("othersubr 3 not followed by pop")
		}
		if err := s.spend(1); err != nil {
			return p, err
		}
		return p + 2, nil
	default:
		return p, fmt.Errorf("othersubr %v, which is refused", num)
	}
	return p, nil
}

// flexCurve draws the curve through the three collected flex points from (x, y). The pen is
// already at the last of them.
func (s *t1State) flexCurve(x, y float64) error {
	s.closed = false
	px, py := s.x, s.y
	s.x, s.y = x, y
	err := s.start()
	s.x, s.y = px, py
	if err != nil {
		return err
	}
	f := s.flex
	return s.r.appendSeg(Seg{Op: SegCubic, P: [3]Point{{f[0], f[1]}, {f[2], f[3]}, {f[4], f[5]}}})
}

// seac draws the accented glyph its five operands describe, asb adx ady bchar achar: the base at
// the origin and the accent where the format puts it, adx - asb from the base's origin, moved by
// the calling glyph's own side bearing as FreeType moves it. The characters are StandardEncoding
// codes, found by glyph name.
func (s *t1State) seac() error {
	r := s.r
	if s.n != 5 {
		return countErr(1206, s.n)
	}
	if r.inSeac {
		return errors.New("seac inside a seac component")
	}
	if s.moved || len(r.out) > 0 {
		return errors.New("seac after the glyph has drawn")
	}
	asb, adx, ady := s.stack[0], s.stack[1], s.stack[2]
	var parts [2]uint16
	for i := range parts {
		v := s.stack[3+i]
		if s.fixed[3+i] || v < 0 || v > 255 {
			return fmt.Errorf("seac character %v", v)
		}
		name := stdEncoding.Glyph(byte(v))
		gid, ok := r.t1.GIDForName(name)
		if name == "" || !ok {
			return fmt.Errorf("seac character %v has no glyph in the font", v)
		}
		parts[i] = gid
	}
	r.inSeac = true
	defer func() { r.inSeac = false }()
	for i, at := range [2][2]float64{{0, 0}, {adx + s.sbx - asb, ady}} {
		if err := r.t1Glyph(r.t1.glyphs[parts[i]], at[0], at[1]); err != nil {
			return fmt.Errorf("seac component: %w", err)
		}
	}
	return nil
}

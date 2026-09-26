package font

import (
	"errors"
	"fmt"
	"math"
)

// The Type 2 charstring interpreter: Adobe Technical Note #5177, read the way FreeType's Adobe
// engine (psaux/psintrp.c) reads it, since that is what pdfium draws with.
//
// # What it refuses
//
// It draws the path operators — every one, flex and its three shorthands included — and it
// follows local and global subroutines and the seac form of endchar. It refuses the arithmetic,
// storage, and conditional operators, and random. None occurs in the corpus, and random is not
// even deterministic: two renderers disagree by design. It refuses too wherever the note's grammar
// is broken in a way FreeType repairs silently — an operand count that is not one the operator
// takes, a stem or a mask's implied vstem declared after a hintmask or the first moveto (FreeType
// ignores the stem but still clears the stack, and a mask with more than one leftover operand
// breaks without reading its mask bytes: psintrp.c's hint-mask case; this code refuses one leftover
// operand too, stricter than FreeType there, since refusal is the safe direction), more than 96
// stems once a hintmask or cntrmask reads its mask bytes or the first moveto builds the hint map
// (cf2_hintmask_setCounts fails the glyph either way; a charstring that reaches endchar without
// ever running a mask or a moveto draws however many stems it declared, since the stem operators
// themselves only push), a subroutine number or a seac achar/bchar written in the 255 (16.16
// fixed) operand form (cf2_stack_popInt fails on a Fixed-typed entry), a path drawn before the
// first moveto, a charstring that runs off its end, and a glyph whose outline passes
// maxGlyphSegments. FreeType's repairs are a reading of bytes the format does not define, and
// matching them one by one would be guesswork about bytes no producer should write.
//
// # Why hints are counted but not applied
//
// pdfium loads CFF glyphs with FT_LOAD_NO_HINTING, so the outline it fills is the unhinted one and
// stems change nothing drawn. They still have to be counted: hintmask and cntrmask are followed by
// one bit per stem, and reading the wrong number of mask bytes turns the rest of the glyph into
// garbage operators.

const (
	maxCharStack = 48 // the argument stack limit of TN #5177 Appendix B
	maxSubrDepth = 10 // the subroutine nesting limit of the same appendix
	maxStems     = 96 // FreeType's CF2_MAX_HINTS, and the note's limit
)

// Outline returns a glyph's contours in 1/1000 em, and how many charstring operations it took.
//
// budget bounds those operations, operands and operators both. A charstring cannot loop, but
// subroutines nest ten deep and each can call others many times, so the work one glyph asks for
// is exponential in its size; past the budget the answer is ErrGlyphBudget. An empty outline —
// a space — is nil with no error.
func (c *CFF) Outline(gid uint16, budget int) (Outline, int, error) {
	cs, err := c.charStrings.get(int(gid))
	if err != nil {
		return nil, 0, fmt.Errorf("font: CFF glyph %d: %w", gid, err)
	}
	fd := c.fdFor(gid)
	r := &charRun{cff: c, local: fd.subrs, budget: budget,
		gsubrBias: subrBias(c.gsubrs.count), localBias: subrBias(fd.subrs.count)}
	if err := r.glyph(cs, 0, 0); err != nil {
		return nil, r.ops, fmt.Errorf("font: CFF glyph %d: %w", gid, err)
	}
	if len(r.out) == 0 {
		return nil, r.ops, nil
	}
	m := fd.matrix
	for i := range r.out {
		for k := range r.out[i].P {
			x, y := r.out[i].P[k].X, r.out[i].P[k].Y
			r.out[i].P[k] = Point{m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]}
		}
	}
	return r.out, r.ops, nil
}

// charRun is the state one glyph shares across its seac components: the outline being built and
// the operations spent on it.
type charRun struct {
	cff                  *CFF
	local                cffIndex
	gsubrBias, localBias int
	out                  Outline
	ops, budget          int
	inSeac               bool
}

// charState is one charstring's interpreter state. A seac component gets its own, as FreeType's
// recursive call does, so its stems and width do not leak into the glyph that called it.
type charState struct {
	r     *charRun
	stack [maxCharStack]float64
	fixed [maxCharStack]bool // stack[i] was pushed in the 255 (16.16 fixed) form: FreeType's
	// cf2_stack_popInt fails the glyph on a Fixed-typed entry, even one with no fraction, so a
	// popInt caller (callsubr, callgsubr, seac's achar/bchar) must refuse the form, not the value.
	n int

	x, y  float64
	moved bool // a moveto has been seen, so path operators have a start
	open  bool // a contour has segments and has not been closed

	haveWidth bool // the first stack-clearing operator has been seen
	stems     int
	masked    bool // a hintmask has been seen, after which no stem may be declared
}

var errNoEnd = errors.New("charstring ends without endchar or return")

// glyph interprets one charstring starting from (x, y).
func (r *charRun) glyph(cs []byte, x, y float64) error {
	s := &charState{r: r, x: x, y: y}
	ended, err := s.exec(cs, 0)
	if err == nil && !ended {
		err = errNoEnd
	}
	return err
}

// subrBias is the bias §4.7 of TN #5177 adds to a subroutine number.
func subrBias(n int) int {
	switch {
	case n < 1240:
		return 107
	case n < 33900:
		return 1131
	}
	return 32768
}

// exec runs a charstring or subroutine, reporting whether it reached endchar.
func (s *charState) exec(cs []byte, depth int) (bool, error) {
	for p := 0; p < len(cs); {
		s.r.ops++
		if s.r.ops > s.r.budget {
			return false, ErrGlyphBudget
		}
		b := cs[p]
		p++

		// Operands, per Table 1 of TN #5177.
		if b >= 32 || b == 28 {
			var v float64
			switch {
			case b <= 246 && b >= 32:
				v = float64(int(b) - 139)
			case b >= 247 && b <= 250:
				if p >= len(cs) {
					return false, errors.New("charstring operand is truncated")
				}
				v = float64((int(b)-247)*256 + int(cs[p]) + 108)
				p++
			case b >= 251 && b <= 254:
				if p >= len(cs) {
					return false, errors.New("charstring operand is truncated")
				}
				v = float64(-(int(b)-251)*256 - int(cs[p]) - 108)
				p++
			case b == 255:
				if p+4 > len(cs) {
					return false, errors.New("charstring operand is truncated")
				}
				v = float64(int32(be32(cs, p))) / 65536 // #nosec G115 -- 16.16 fixed is signed
				p += 4
			default: // 28
				if p+2 > len(cs) {
					return false, errors.New("charstring operand is truncated")
				}
				v = float64(int16(be16(cs, p))) // #nosec G115 -- the form is signed
				p += 2
			}
			if s.n == maxCharStack {
				return false, errors.New("charstring argument stack overflows")
			}
			s.stack[s.n] = v
			s.fixed[s.n] = b == 255
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

		switch op {
		case 10, 29: // callsubr, callgsubr
			if s.n == 0 {
				return false, errors.New("subroutine call without a number")
			}
			s.n--
			num := s.stack[s.n]
			if s.fixed[s.n] {
				return false, fmt.Errorf("subroutine number %v in the 255 (16.16 fixed) operand form", num)
			}
			subrs, bias := s.r.local, s.r.localBias
			if op == 29 {
				subrs, bias = s.r.cff.gsubrs, s.r.gsubrBias
			}
			if depth+1 > maxSubrDepth {
				return false, errors.New("subroutines nest more than ten deep")
			}
			sub, err := subrs.get(int(num) + bias)
			if err != nil {
				return false, fmt.Errorf("subroutine %v: %w", num, err)
			}
			ended, err := s.exec(sub, depth+1)
			if err != nil || ended {
				return ended, err
			}
			continue

		case 11: // return
			if depth == 0 {
				return false, errors.New("return outside a subroutine")
			}
			return false, nil

		case 14: // endchar
			if err := s.endchar(); err != nil {
				return false, err
			}
			return true, nil

		case 1, 3, 18, 23: // hstem, vstem, hstemhm, vstemhm
			args := s.width()
			if s.masked || s.moved {
				return false, errors.New("stem hints declared after a hintmask or the first moveto")
			}
			if args == 0 || args%2 != 0 {
				return false, fmt.Errorf("stem operator %d takes pairs, not %d operands", op, args)
			}
			s.addStems(args / 2)

		case 19, 20: // hintmask, cntrmask
			args := s.width()
			if args%2 != 0 || (args > 0 && (s.masked || s.moved)) {
				return false, fmt.Errorf("mask operator %d with %d operands", op, args)
			}
			s.addStems(args / 2) // an implicit vstem
			if err := s.checkStems(); err != nil {
				return false, err
			}
			if op == 19 {
				s.masked = true
			}
			p += (s.stems + 7) / 8
			if p > len(cs) {
				return false, errors.New("charstring ends inside a hint mask")
			}

		case 21: // rmoveto
			if err := s.moveto(2, 1, 1); err != nil {
				return false, err
			}
		case 22: // hmoveto
			if err := s.moveto(1, 1, 0); err != nil {
				return false, err
			}
		case 4: // vmoveto
			if err := s.moveto(1, 0, 1); err != nil {
				return false, err
			}

		default:
			if err := s.path(op); err != nil {
				return false, err
			}
		}
		s.n = 0
	}
	return false, errNoEnd
}

// dropWidth removes the leading (width) operand, shifting the fixed-form flags along with the
// stack so each remaining slot's flag still names the operand now sitting in it.
func (s *charState) dropWidth() {
	copy(s.stack[:], s.stack[1:s.n])
	copy(s.fixed[:], s.fixed[1:s.n])
	s.n--
}

// width strips the optional width argument the first stack-clearing operator may carry — present
// when the operand count exceeds what the operator takes by one, which for the stem and mask
// operators, taking pairs, is an odd count — and returns the operands left.
func (s *charState) width() int {
	if !s.haveWidth && s.n%2 == 1 {
		s.dropWidth()
	}
	s.haveWidth = true
	return s.n
}

// exactWidth strips a width from an operator that takes exactly want operands.
func (s *charState) exactWidth(want int) error {
	if !s.haveWidth && s.n == want+1 {
		s.dropWidth()
	}
	s.haveWidth = true
	if s.n != want {
		return fmt.Errorf("operator takes %d operands, not %d", want, s.n)
	}
	return nil
}

// addStems adds n stems to the running total. A stem operator only pushes to FreeType's
// hStemHintArray/vStemHintArray; nothing there checks maxStems (psintrp.c's stem cases, which end
// in a plain break). The total is capped only where FreeType itself counts it: checkStems, called
// from the mask case and from the first moveto.
func (s *charState) addStems(n int) {
	s.stems += n
}

// checkStems refuses once the running stem total passes maxStems (96, FreeType's CF2_MAX_HINTS).
// FreeType counts stems in cf2_hintmask_setCounts (psintrp.c:109), called from cf2_hintmask_read
// (psintrp.c:132) when a hintmask or cntrmask reads its mask bytes, and from cf2_hintmask_setAll
// (psintrp.c:174) when the first moveto builds the hint map with no mask yet valid
// (cf2_glyphpath_moveTo, pshints.c:1694, calling cf2_hintmap_build, pshints.c:814). A charstring
// that reaches endchar without ever running a mask or a moveto draws however many stems it
// declared, since neither path to cf2_hintmask_setCounts ever runs.
func (s *charState) checkStems() error {
	if s.stems > maxStems {
		return fmt.Errorf("%d stem hints, past the limit of %d", s.stems, maxStems)
	}
	return nil
}

// moveto closes any open contour and moves the pen. dx and dy say which operands are deltas:
// rmoveto takes both, hmoveto and vmoveto one each. The first moveto is also where FreeType builds
// the hint map (cf2_glyphpath_moveTo), so it is the other place, besides a mask, that checkStems
// runs.
func (s *charState) moveto(want int, dx, dy int) error {
	if err := s.exactWidth(want); err != nil {
		return err
	}
	if !s.moved {
		if err := s.checkStems(); err != nil {
			return err
		}
	}
	if err := s.close(); err != nil {
		return err
	}
	switch {
	case dx == 1 && dy == 1:
		s.x += s.stack[0]
		s.y += s.stack[1]
	case dx == 1:
		s.x += s.stack[0]
	default:
		s.y += s.stack[0]
	}
	s.moved = true
	return s.inRange(len(s.r.out))
}

// endchar finishes the glyph, drawing a seac's two components when it carries four operands.
func (s *charState) endchar() error {
	if !s.haveWidth && (s.n == 1 || s.n == 5) {
		s.dropWidth()
	}
	s.haveWidth = true
	if err := s.close(); err != nil {
		return err
	}
	switch s.n {
	case 0:
		return nil
	case 4:
		return s.seac()
	}
	return fmt.Errorf("endchar with %d operands", s.n)
}

// seac draws the accented glyph endchar's four operands describe: adx ady bchar achar, the base
// at the origin and the accent offset by (adx, ady). The characters are StandardEncoding codes,
// found through the charset by name — which a CID-keyed font has none of — and a component the
// subset does not carry refuses the glyph, as FreeType fails it.
func (s *charState) seac() error {
	r := s.r
	if r.cff.cid {
		return errors.New("seac in a CID-keyed font, which has no glyph names")
	}
	if r.inSeac {
		return errors.New("seac inside a seac component")
	}
	adx, ady := s.stack[0], s.stack[1]
	var parts [2]uint16
	for i, v := range [2]float64{s.stack[2], s.stack[3]} {
		// achar and bchar are popInt'd (psintrp.c:2534-2535), unlike adx and ady, which are
		// popFixed'd either form: a 255-form operand fails the glyph even when integer-valued.
		if s.fixed[2+i] || v < 0 || v > 255 {
			return fmt.Errorf("seac character %v", v)
		}
		sid := stdSID(byte(v))
		gid := r.cff.gidForSID(sid)
		if sid == 0 || gid == 0 {
			return fmt.Errorf("seac character %v has no glyph in the font", v)
		}
		parts[i] = gid
	}
	r.inSeac = true
	defer func() { r.inSeac = false }()
	for i, at := range [2][2]float64{{0, 0}, {adx, ady}} {
		cs, err := r.cff.charStrings.get(int(parts[i]))
		if err != nil {
			return fmt.Errorf("seac component: %w", err)
		}
		if err := r.glyph(cs, at[0], at[1]); err != nil {
			return fmt.Errorf("seac component: %w", err)
		}
	}
	return nil
}

// path runs a drawing operator, or refuses one this interpreter does not implement.
func (s *charState) path(op int) error {
	if name, ok := refusedOps[op]; ok {
		return fmt.Errorf("the %s operator, which is refused", name)
	}
	if !knownPathOp(op) {
		return fmt.Errorf("reserved charstring operator %d", op)
	}
	a := s.stack[:s.n]
	n := s.n
	if op == 1200 {
		// dotsection: a deprecated hint and a no-op, and not one of the operators that may carry
		// the width, so it leaves the width question open.
		if n != 0 {
			return countErr(op, n)
		}
		return nil
	}
	// A moveto is also what settles the width, so no drawing operator can carry one.
	if !s.moved {
		return fmt.Errorf("path operator %d before the first moveto", op)
	}
	from := len(s.r.out)
	var err error
	switch op {
	case 5: // rlineto
		if n == 0 || n%2 != 0 {
			return countErr(op, n)
		}
		for i := 0; i < n && err == nil; i += 2 {
			err = s.line(a[i], a[i+1])
		}
	case 6, 7: // hlineto, vlineto: one line per operand, alternating
		if n == 0 {
			return countErr(op, n)
		}
		horiz := op == 6
		for _, d := range a {
			if err != nil {
				break
			}
			if horiz {
				err = s.line(d, 0)
			} else {
				err = s.line(0, d)
			}
			horiz = !horiz
		}
	case 8: // rrcurveto
		if n == 0 || n%6 != 0 {
			return countErr(op, n)
		}
		for i := 0; i < n && err == nil; i += 6 {
			err = s.curve(a[i], a[i+1], a[i+2], a[i+3], a[i+4], a[i+5])
		}
	case 24: // rcurveline
		if n < 8 || (n-2)%6 != 0 {
			return countErr(op, n)
		}
		for i := 0; i < n-2 && err == nil; i += 6 {
			err = s.curve(a[i], a[i+1], a[i+2], a[i+3], a[i+4], a[i+5])
		}
		if err == nil {
			err = s.line(a[n-2], a[n-1])
		}
	case 25: // rlinecurve
		if n < 8 || (n-6)%2 != 0 {
			return countErr(op, n)
		}
		for i := 0; i < n-6 && err == nil; i += 2 {
			err = s.line(a[i], a[i+1])
		}
		if err == nil {
			err = s.curve(a[n-6], a[n-5], a[n-4], a[n-3], a[n-2], a[n-1])
		}
	case 26: // vvcurveto: dx1? {dya dxb dyb dyc}+
		if n < 4 || n%4 > 1 {
			return countErr(op, n)
		}
		i, dx1 := 0, 0.0
		if n%4 == 1 {
			dx1, i = a[0], 1
		}
		for ; i < n && err == nil; i += 4 {
			err = s.curve(dx1, a[i], a[i+1], a[i+2], 0, a[i+3])
			dx1 = 0
		}
	case 27: // hhcurveto: dy1? {dxa dxb dyb dxc}+
		if n < 4 || n%4 > 1 {
			return countErr(op, n)
		}
		i, dy1 := 0, 0.0
		if n%4 == 1 {
			dy1, i = a[0], 1
		}
		for ; i < n && err == nil; i += 4 {
			err = s.curve(a[i], dy1, a[i+1], a[i+2], a[i+3], 0)
			dy1 = 0
		}
	case 30, 31: // vhcurveto, hvcurveto: alternating tangents, an optional last delta
		if n < 4 || n%4 > 1 {
			return countErr(op, n)
		}
		horiz := op == 31
		for i := 0; i+4 <= n && err == nil; i += 4 {
			last := 0.0
			if n-i == 5 {
				last = a[i+4]
			}
			if horiz {
				err = s.curve(a[i], 0, a[i+1], a[i+2], last, a[i+3])
			} else {
				err = s.curve(0, a[i], a[i+1], a[i+2], a[i+3], last)
			}
			horiz = !horiz
		}
	case 1235: // flex: two curves, then the flex depth, which only matters to a hinter
		if n != 13 {
			return countErr(op, n)
		}
		if err = s.curve(a[0], a[1], a[2], a[3], a[4], a[5]); err == nil {
			err = s.curve(a[6], a[7], a[8], a[9], a[10], a[11])
		}
	case 1234: // hflex: dx1 dx2 dy2 dx3 dx4 dx5 dx6, returning to the starting y
		if n != 7 {
			return countErr(op, n)
		}
		if err = s.curve(a[0], 0, a[1], a[2], a[3], 0); err == nil {
			err = s.curve(a[4], 0, a[5], -a[2], a[6], 0)
		}
	case 1236: // hflex1: dx1 dy1 dx2 dy2 dx3 dx4 dx5 dy5 dx6, returning to the starting y
		if n != 9 {
			return countErr(op, n)
		}
		y0 := s.y
		if err = s.curve(a[0], a[1], a[2], a[3], a[4], 0); err == nil {
			err = s.curve(a[5], 0, a[6], a[7], a[8], y0-(s.y+a[7]))
		}
	case 1237: // flex1: five points, and the sixth along whichever axis moved further
		if n != 11 {
			return countErr(op, n)
		}
		x0, y0 := s.x, s.y
		dx, dy := a[0]+a[2]+a[4]+a[6]+a[8], a[1]+a[3]+a[5]+a[7]+a[9]
		// FreeType compares the spans as 16.16 differences, which wrap as its sums do.
		if math.Abs(dx) >= 32768 || math.Abs(dy) >= 32768 {
			return fmt.Errorf("flex1 spans (%g, %g), which leave the 16.16 range", dx, dy)
		}
		if err = s.curve(a[0], a[1], a[2], a[3], a[4], a[5]); err == nil {
			p5x, p5y := s.x+a[6]+a[8], s.y+a[7]+a[9]
			var d6x, d6y float64
			if math.Abs(dx) > math.Abs(dy) {
				d6x, d6y = a[10], y0-p5y
			} else {
				d6x, d6y = x0-p5x, a[10]
			}
			err = s.curve(a[6], a[7], a[8], a[9], d6x, d6y)
		}
	}
	if err != nil {
		return err
	}
	return s.inRange(from)
}

// refusedOps are the Type 2 operators that compute rather than draw, named for the refusal.
var refusedOps = map[int]string{
	1203: "and", 1204: "or", 1205: "not", 1209: "abs", 1210: "add", 1211: "sub", 1212: "div",
	1214: "neg", 1215: "eq", 1218: "drop", 1220: "put", 1221: "get", 1222: "ifelse",
	1223: "random", 1224: "mul", 1226: "sqrt", 1227: "dup", 1228: "exch", 1229: "index",
	1230: "roll",
}

func knownPathOp(op int) bool {
	switch op {
	case 5, 6, 7, 8, 24, 25, 26, 27, 30, 31, 1200, 1234, 1235, 1236, 1237:
		return true
	}
	return false
}

func countErr(op, n int) error {
	return fmt.Errorf("operator %d with %d operands", op, n)
}

// line draws to the point (dx, dy) from the pen.
func (s *charState) line(dx, dy float64) error {
	if err := s.start(); err != nil {
		return err
	}
	s.x += dx
	s.y += dy
	return s.r.appendSeg(Seg{Op: SegLine, P: [3]Point{{s.x, s.y}}})
}

// curve draws a cubic whose three points are successive deltas from the pen.
func (s *charState) curve(dx1, dy1, dx2, dy2, dx3, dy3 float64) error {
	if err := s.start(); err != nil {
		return err
	}
	p1 := Point{s.x + dx1, s.y + dy1}
	p2 := Point{p1.X + dx2, p1.Y + dy2}
	p3 := Point{p2.X + dx3, p2.Y + dy3}
	s.x, s.y = p3.X, p3.Y
	return s.r.appendSeg(Seg{Op: SegCubic, P: [3]Point{p1, p2, p3}})
}

// start opens a contour at the pen when the first segment after a moveto is drawn. A moveto
// followed by another draws nothing, which is FreeType's reading and the only sensible one: an
// empty contour is not ink.
func (s *charState) start() error {
	if !s.open {
		if err := s.r.appendSeg(Seg{Op: SegMove, P: [3]Point{{s.x, s.y}}}); err != nil {
			return err
		}
		s.open = true
	}
	return nil
}

func (s *charState) close() error {
	if s.open {
		s.open = false
		return s.r.appendSeg(Seg{Op: SegClose})
	}
	return nil
}

// appendSeg appends one segment and refuses once the glyph passes maxGlyphSegments, the same
// per-glyph cap TrueType composites are charged (truetype.go). r.out is shared across a seac's
// components, so the cap holds for the composed glyph, not each component alone.
func (r *charRun) appendSeg(seg Seg) error {
	r.out = append(r.out, seg)
	if len(r.out) > maxGlyphSegments {
		return fmt.Errorf("the glyph draws more than %d segments", maxGlyphSegments)
	}
	return nil
}

// inRange refuses a point that has left the 16.16 fixed-point range FreeType computes in, where
// its arithmetic wraps and a float's does not. FreeType wraps every sum, so the check covers each
// point drawn from segment from on, control points included, and not only where the pen ends.
func (s *charState) inRange(from int) error {
	check := func(p Point) error {
		if math.Abs(p.X) >= 32768 || math.Abs(p.Y) >= 32768 {
			return fmt.Errorf("charstring coordinates (%g, %g) leave the 16.16 range", p.X, p.Y)
		}
		return nil
	}
	for _, g := range s.r.out[from:] {
		for _, p := range g.P {
			if err := check(p); err != nil {
				return err
			}
		}
	}
	return check(Point{s.x, s.y})
}

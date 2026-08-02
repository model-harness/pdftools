package content

import (
	"math"
	"strings"
	"testing"

	"github.com/3rg0n/pdf-spec/geom"
)

// run drives a machine over a content stream, applying every state operator and
// invoking each hook for its named operator. This mirrors how extract/ will use
// the two packages together.
func run(t *testing.T, m *Machine, src string, hooks map[string]func(Op)) {
	t.Helper()
	sc := NewScanner([]byte(src))
	for {
		op, ok := sc.Next()
		if !ok {
			return
		}
		m.Apply(op)
		if h := hooks[op.Name]; h != nil {
			h(op)
		}
	}
}

func matrixNear(a, b geom.Matrix) bool {
	const eps = 1e-9
	return math.Abs(a.A-b.A) < eps && math.Abs(a.B-b.B) < eps &&
		math.Abs(a.C-b.C) < eps && math.Abs(a.D-b.D) < eps &&
		math.Abs(a.E-b.E) < eps && math.Abs(a.F-b.F) < eps
}

func TestMachineInitialState(t *testing.T) {
	m := NewMachine(geom.Identity)
	// Scale must default to 100, not 0: a zero horizontal scale would collapse
	// every advance to nothing and produce one run-together word.
	if m.GS.Text.Scale != 100 {
		t.Errorf("initial Scale = %v, want 100", m.GS.Text.Scale)
	}
	if m.Tm != geom.Identity || m.Tlm != geom.Identity {
		t.Error("text matrices should start as identity")
	}
	if m.InText {
		t.Error("should not start inside a text object")
	}
	if m.MCID() != -1 {
		t.Errorf("initial MCID = %d, want -1", m.MCID())
	}
	if !m.Visible() {
		t.Error("render mode 0 should be visible")
	}
}

func TestMachineTextStateOperators(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "/F3 14.5 Tf 1.5 Tc 3 Tw 50 Tz 16 TL 4 Ts 2 Tr", nil)

	ts := m.GS.Text
	if ts.Font != "F3" || ts.Size != 14.5 {
		t.Errorf("Tf -> %q %v", ts.Font, ts.Size)
	}
	if ts.CharSpace != 1.5 {
		t.Errorf("Tc -> %v", ts.CharSpace)
	}
	if ts.WordSpace != 3 {
		t.Errorf("Tw -> %v", ts.WordSpace)
	}
	if ts.Scale != 50 {
		t.Errorf("Tz -> %v", ts.Scale)
	}
	if ts.Leading != 16 {
		t.Errorf("TL -> %v", ts.Leading)
	}
	if ts.Rise != 4 {
		t.Errorf("Ts -> %v", ts.Rise)
	}
	if ts.Render != 2 {
		t.Errorf("Tr -> %v", ts.Render)
	}
}

func TestMachineTextStateSurvivesTextObject(t *testing.T) {
	// Tf outside BT/ET applies to the next text object, and text state is not
	// reset by BT. An extractor that reset it would lose the font on every page
	// whose producer sets state once at the top.
	m := NewMachine(geom.Identity)
	run(t, m, "/F1 12 Tf 2 Tc BT ET", nil)
	if m.GS.Text.Font != "F1" || m.GS.Text.Size != 12 || m.GS.Text.CharSpace != 2 {
		t.Errorf("text state lost across BT/ET: %+v", m.GS.Text)
	}
}

func TestMachineBTResetsTextMatrices(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "BT 100 700 Td", nil)
	if m.Tm.E != 100 || m.Tm.F != 700 {
		t.Fatalf("Td -> Tm %+v", m.Tm)
	}
	// A second BT must reset, not accumulate. Failing this stacks every text
	// object's offset onto the last, walking text off the page.
	run(t, m, "BT", nil)
	if m.Tm != geom.Identity || m.Tlm != geom.Identity {
		t.Errorf("BT did not reset: Tm %+v Tlm %+v", m.Tm, m.Tlm)
	}
	if !m.InText {
		t.Error("BT should set InText")
	}
	run(t, m, "ET", nil)
	if m.InText {
		t.Error("ET should clear InText")
	}
}

func TestMachineTdIsRelativeToLineMatrix(t *testing.T) {
	// Td translates relative to the line matrix, not absolutely. Two Tds must
	// accumulate.
	m := NewMachine(geom.Identity)
	run(t, m, "BT 10 20 Td 5 -3 Td", nil)
	if m.Tm.E != 15 || m.Tm.F != 17 {
		t.Fatalf("Tm = (%v,%v), want (15,17)", m.Tm.E, m.Tm.F)
	}
	if m.Tlm != m.Tm {
		t.Error("Td should set Tm from Tlm")
	}
}

func TestMachineTDSetsLeading(t *testing.T) {
	// TD sets leading to the negated y operand, so a following T* moves by the
	// same amount.
	m := NewMachine(geom.Identity)
	run(t, m, "BT 0 -14 TD", nil)
	if m.GS.Text.Leading != 14 {
		t.Fatalf("leading = %v, want 14", m.GS.Text.Leading)
	}
	if m.Tm.F != -14 {
		t.Fatalf("Tm.F = %v, want -14", m.Tm.F)
	}
	run(t, m, "T*", nil)
	if m.Tm.F != -28 {
		t.Fatalf("after T*, Tm.F = %v, want -28", m.Tm.F)
	}
}

func TestMachineNextLineMovesDown(t *testing.T) {
	// Positive leading moves down the page, which in PDF user space means
	// decreasing y. Getting the sign wrong reverses line order.
	m := NewMachine(geom.Identity)
	run(t, m, "BT 0 700 Td 12 TL T*", nil)
	if m.Tm.F != 688 {
		t.Fatalf("Tm.F = %v, want 688", m.Tm.F)
	}
}

func TestMachineTmIsAbsolute(t *testing.T) {
	// Unlike Td, Tm replaces both matrices outright.
	m := NewMachine(geom.Identity)
	run(t, m, "BT 100 200 Td 2 0 0 2 50 60 Tm", nil)
	want := geom.Matrix{A: 2, D: 2, E: 50, F: 60}
	if m.Tm != want || m.Tlm != want {
		t.Fatalf("Tm = %+v, Tlm = %+v, want %+v", m.Tm, m.Tlm, want)
	}
}

func TestMachineTmIgnoresShortOperandList(t *testing.T) {
	// A truncated Tm must leave the matrix alone rather than build one from zeros,
	// which would collapse all following text to the origin.
	m := NewMachine(geom.Identity)
	run(t, m, "BT 1 0 0 1 10 20 Tm 1 0 0 Tm", nil)
	if m.Tm.E != 10 || m.Tm.F != 20 {
		t.Fatalf("Tm = %+v, want the earlier value preserved", m.Tm)
	}
}

func TestMachineCMConcatenates(t *testing.T) {
	// cm concatenates onto the CTM: the new matrix applies first. Two translates
	// must accumulate, and a scale must compose with a preceding translate in the
	// right order.
	m := NewMachine(geom.Identity)
	run(t, m, "1 0 0 1 10 10 cm 1 0 0 1 5 5 cm", nil)
	if m.GS.CTM.E != 15 || m.GS.CTM.F != 15 {
		t.Fatalf("CTM = %+v, want translation (15,15)", m.GS.CTM)
	}

	// Scale then translate: the translate is in the scaled space, so the origin
	// lands at (20,20), not (10,10).
	m = NewMachine(geom.Identity)
	run(t, m, "1 0 0 1 10 10 cm 2 0 0 2 0 0 cm", nil)
	x, y := m.GS.CTM.Apply(0, 0)
	if x != 10 || y != 10 {
		t.Fatalf("origin -> (%v,%v), want (10,10)", x, y)
	}
	x, y = m.GS.CTM.Apply(5, 5)
	if x != 20 || y != 20 {
		t.Fatalf("(5,5) -> (%v,%v), want (20,20)", x, y)
	}
}

func TestMachineQRestoresState(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "/F1 10 Tf 1 0 0 1 5 5 cm q /F2 20 Tf 1 0 0 1 100 100 cm 5 Tc", nil)
	if m.GS.Text.Font != "F2" || m.GS.CTM.E != 105 {
		t.Fatalf("state inside q: %+v CTM %+v", m.GS.Text, m.GS.CTM)
	}
	run(t, m, "Q", nil)
	if m.GS.Text.Font != "F1" || m.GS.Text.Size != 10 {
		t.Errorf("Q did not restore text state: %+v", m.GS.Text)
	}
	if m.GS.CTM.E != 5 || m.GS.CTM.F != 5 {
		t.Errorf("Q did not restore CTM: %+v", m.GS.CTM)
	}
	if m.GS.Text.CharSpace != 0 {
		t.Errorf("Q did not restore CharSpace: %v", m.GS.Text.CharSpace)
	}
}

func TestMachineUnbalancedQIsIgnored(t *testing.T) {
	// More Q than q is malformed and common. It must not pop past the bottom.
	m := NewMachine(geom.Identity)
	run(t, m, "/F1 10 Tf Q Q Q", nil)
	if m.GS.Text.Font != "F1" {
		t.Errorf("state lost to a stray Q: %+v", m.GS.Text)
	}
}

func TestMachineStateStackIsBounded(t *testing.T) {
	// An unbalanced stream that pushes forever must not grow without limit.
	m := NewMachine(geom.Identity)
	run(t, m, strings.Repeat("q ", maxStateDepth*4), nil)
	if len(m.stack) > maxStateDepth {
		t.Fatalf("stack depth %d, want at most %d", len(m.stack), maxStateDepth)
	}
	// Depth is capped, but the state must still be usable.
	run(t, m, "/F9 8 Tf", nil)
	if m.GS.Text.Font != "F9" {
		t.Error("machine unusable after hitting the depth cap")
	}
}

func TestMachineTextMatricesAreNotSavedByQ(t *testing.T) {
	// Tm is not part of the graphics state (§9.4.1): q/Q must not restore it.
	// Treating it as saved would rewind the text position at every Q.
	m := NewMachine(geom.Identity)
	run(t, m, "BT 10 20 Td q 100 200 Td Q", nil)
	if m.Tm.E != 110 || m.Tm.F != 220 {
		t.Fatalf("Tm = (%v,%v), want (110,220): Q should not restore the text matrix",
			m.Tm.E, m.Tm.F)
	}
}

func TestMachineClipDepth(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "W n W* n", nil)
	if m.GS.ClipDepth != 2 {
		t.Fatalf("ClipDepth = %d, want 2", m.GS.ClipDepth)
	}
	// Clipping is part of the graphics state, so Q restores it.
	m = NewMachine(geom.Identity)
	run(t, m, "q W n Q", nil)
	if m.GS.ClipDepth != 0 {
		t.Fatalf("ClipDepth = %d after Q, want 0", m.GS.ClipDepth)
	}
}

func TestMachineMarkedContentMCID(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "/P <</MCID 7>> BDC", nil)
	if got := m.MCID(); got != 7 {
		t.Fatalf("MCID = %d, want 7", got)
	}
	if m.MarkedTag() != "P" {
		t.Errorf("MarkedTag = %q, want P", m.MarkedTag())
	}
	run(t, m, "EMC", nil)
	if got := m.MCID(); got != -1 {
		t.Fatalf("MCID = %d after EMC, want -1", got)
	}
}

func TestMachineMCIDZeroIsDistinctFromAbsent(t *testing.T) {
	// MCID 0 is a real identifier. Conflating it with "no MCID" misattributes the
	// first marked region on every page, which is usually the first heading.
	m := NewMachine(geom.Identity)
	run(t, m, "/P <</MCID 0>> BDC", nil)
	if got := m.MCID(); got != 0 {
		t.Fatalf("MCID = %d, want 0", got)
	}

	m = NewMachine(geom.Identity)
	run(t, m, "/P <</Lang (en)>> BDC", nil)
	if got := m.MCID(); got != -1 {
		t.Fatalf("MCID = %d for a BDC without /MCID, want -1", got)
	}
}

func TestMachineNestedMCIDUsesInnermost(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "/Sect <</MCID 1>> BDC /Span <</MCID 2>> BDC", nil)
	if got := m.MCID(); got != 2 {
		t.Fatalf("MCID = %d, want the innermost, 2", got)
	}
	// A BMC with no MCID inside a BDC that has one: the enclosing MCID still
	// applies, because the inner region declares no identifier of its own.
	run(t, m, "/Artifact BMC", nil)
	if got := m.MCID(); got != 2 {
		t.Fatalf("MCID = %d inside a bare BMC, want 2", got)
	}
	run(t, m, "EMC", nil)
	if got := m.MCID(); got != 2 {
		t.Fatalf("MCID = %d, want 2", got)
	}
	run(t, m, "EMC", nil)
	if got := m.MCID(); got != 1 {
		t.Fatalf("MCID = %d, want the outer 1", got)
	}
}

func TestMachineInArtifactChecksWholeStack(t *testing.T) {
	// An Artifact region can contain nested marked content. Checking only the
	// innermost tag would let a running header's text through as prose.
	m := NewMachine(geom.Identity)
	run(t, m, "/Artifact <</Type /Pagination>> BDC", nil)
	if !m.InArtifact() {
		t.Fatal("should be in an artifact")
	}
	run(t, m, "/Span <</MCID 4>> BDC", nil)
	if !m.InArtifact() {
		t.Fatal("nested content inside an artifact is still an artifact")
	}
	if m.MarkedTag() != "Span" {
		t.Errorf("MarkedTag = %q, want the innermost, Span", m.MarkedTag())
	}
	run(t, m, "EMC EMC", nil)
	if m.InArtifact() {
		t.Fatal("should have left the artifact")
	}
}

func TestMachineUnbalancedEMCIsIgnored(t *testing.T) {
	// A stray EMC must not clear the stack below it: the rest of the page's text
	// would be attributed to the wrong structure element.
	m := NewMachine(geom.Identity)
	run(t, m, "/P <</MCID 3>> BDC EMC EMC EMC", nil)
	if got := m.MCID(); got != -1 {
		t.Fatalf("MCID = %d, want -1", got)
	}
	// Still usable afterward.
	run(t, m, "/P <</MCID 9>> BDC", nil)
	if got := m.MCID(); got != 9 {
		t.Fatalf("MCID = %d, want 9", got)
	}
}

func TestMachineMarkedContentStackIsBounded(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, strings.Repeat("/P <</MCID 1>> BDC ", maxStateDepth*3), nil)
	if len(m.mcStack) > maxStateDepth {
		t.Fatalf("mcStack depth %d, want at most %d", len(m.mcStack), maxStateDepth)
	}
}

func TestMachineSetMCID(t *testing.T) {
	// A BDC whose property list is a named resource carries no inline MCID; the
	// caller resolves the name and corrects it.
	m := NewMachine(geom.Identity)
	run(t, m, "/P /MyProps BDC", nil)
	if got := m.MCID(); got != -1 {
		t.Fatalf("MCID = %d before SetMCID, want -1", got)
	}
	m.SetMCID(11)
	if got := m.MCID(); got != 11 {
		t.Fatalf("MCID = %d after SetMCID, want 11", got)
	}
}

func TestMachineSetMCIDWithNoRegionIsSafe(t *testing.T) {
	m := NewMachine(geom.Identity)
	m.SetMCID(5) // must not panic
	if got := m.MCID(); got != -1 {
		t.Fatalf("MCID = %d, want -1", got)
	}
}

func TestMachineBDCFloatMCID(t *testing.T) {
	// Some producers write the MCID as a real. It is still an identifier.
	m := NewMachine(geom.Identity)
	run(t, m, "/P <</MCID 12.0>> BDC", nil)
	if got := m.MCID(); got != 12 {
		t.Fatalf("MCID = %d, want 12", got)
	}
}

func TestMachineBDCNonNumericMCIDIsRejected(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "/P <</MCID (nope)>> BDC", nil)
	if got := m.MCID(); got != -1 {
		t.Fatalf("MCID = %d, want -1 for a non-numeric MCID", got)
	}
}

func TestRenderMatrixComposition(t *testing.T) {
	// The parameter matrix folds size, horizontal scale, and rise; it composes
	// with Tm and then the CTM. This is the calculation every glyph position
	// derives from, so it is checked against a hand-computed result rather than
	// against itself.
	m := NewMachine(geom.Identity)
	run(t, m, "BT /F1 12 Tf 50 Tz 3 Ts 1 0 0 1 100 700 Tm", nil)

	// param = [12*0.5 0 0 12 0 3] = [6 0 0 12 0 3]
	// Tm translates by (100,700), CTM is identity, so:
	//   A=6, D=12, E=100, F=703
	want := geom.Matrix{A: 6, D: 12, E: 100, F: 703}
	if got := m.RenderMatrix(); !matrixNear(got, want) {
		t.Fatalf("RenderMatrix = %+v, want %+v", got, want)
	}
}

func TestRenderMatrixIncludesCTM(t *testing.T) {
	// A page CTM flipping y, as a rasterizer supplies, must apply after Tm.
	// Composing in the other order puts every glyph in the wrong place.
	flip := geom.Matrix{A: 1, D: -1, F: 792}
	m := NewMachine(flip)
	run(t, m, "BT /F1 10 Tf 1 0 0 1 72 700 Tm", nil)

	rm := m.RenderMatrix()
	x, y := rm.Apply(0, 0)
	if x != 72 || y != 92 {
		t.Fatalf("glyph origin -> (%v,%v), want (72,92)", x, y)
	}
	// The y axis is inverted, so D is negative.
	if rm.D != -10 {
		t.Errorf("rm.D = %v, want -10", rm.D)
	}
	if rm.A != 10 {
		t.Errorf("rm.A = %v, want 10", rm.A)
	}
}

func TestRenderMatrixWithRotation(t *testing.T) {
	// A 90-degree rotation in Tm. ScaleFactors must still recover the nominal
	// font size, which is how an effective size is read off a rotated matrix.
	m := NewMachine(geom.Identity)
	run(t, m, "BT /F1 20 Tf 0 1 -1 0 100 100 Tm", nil)

	rm := m.RenderMatrix()
	sx, sy := rm.ScaleFactors()
	if math.Abs(sx-20) > 1e-9 || math.Abs(sy-20) > 1e-9 {
		t.Fatalf("scale factors = (%v,%v), want (20,20)", sx, sy)
	}
	// An advance of 1 text-space unit moves in +y after rotation.
	dx, dy := rm.ApplyVec(1, 0)
	if math.Abs(dx) > 1e-9 || math.Abs(dy-20) > 1e-9 {
		t.Fatalf("advance -> (%v,%v), want (0,20)", dx, dy)
	}
}

func TestRenderMatrixNegativeSizeMirrors(t *testing.T) {
	// A negative Tf size is legal and mirrors the glyph. It must not be clamped:
	// the text is real and appears in the output of some producers.
	m := NewMachine(geom.Identity)
	run(t, m, "BT /F1 -12 Tf 1 0 0 1 0 0 Tm", nil)
	rm := m.RenderMatrix()
	if rm.A != -12 || rm.D != -12 {
		t.Fatalf("RenderMatrix = %+v, want -12 on both axes", rm)
	}
}

func TestRenderMatrixRiseIsInTextSpace(t *testing.T) {
	// Rise is an offset in unscaled text space, so it is not multiplied by size,
	// and it is transformed by Tm along with everything else.
	m := NewMachine(geom.Identity)
	run(t, m, "BT /F1 10 Tf 5 Ts 2 0 0 2 0 0 Tm", nil)
	rm := m.RenderMatrix()
	// param.F = 5, scaled by Tm's D of 2 -> 10.
	if rm.F != 10 {
		t.Fatalf("rm.F = %v, want 10", rm.F)
	}
}

func TestMachineAdvance(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "BT 1 0 0 1 100 700 Tm", nil)
	m.Advance(15)
	if m.Tm.E != 115 || m.Tm.F != 700 {
		t.Fatalf("Tm = (%v,%v), want (115,700)", m.Tm.E, m.Tm.F)
	}
	// Advance moves Tm but not Tlm, so a following T* returns to the line start.
	// Advancing Tlm too would cascade every glyph's advance into the next line.
	if m.Tlm.E != 100 {
		t.Fatalf("Tlm.E = %v, want 100: Advance must not move the line matrix", m.Tlm.E)
	}
	run(t, m, "T*", nil)
	if m.Tm.E != 100 {
		t.Fatalf("after T*, Tm.E = %v, want 100", m.Tm.E)
	}
}

func TestMachineAdvanceUnderRotatedMatrix(t *testing.T) {
	// Advance is in text space, so under a rotated Tm it must move along the
	// rotated axis. Applying it in device space instead would scatter glyphs.
	m := NewMachine(geom.Identity)
	run(t, m, "BT 0 1 -1 0 100 100 Tm", nil)
	m.Advance(10)
	x, y := m.Tm.Apply(0, 0)
	if math.Abs(x-100) > 1e-9 || math.Abs(y-110) > 1e-9 {
		t.Fatalf("origin after Advance -> (%v,%v), want (100,110)", x, y)
	}
}

func TestMachineAdvanceVertical(t *testing.T) {
	m := NewMachine(geom.Identity)
	run(t, m, "BT 1 0 0 1 100 700 Tm", nil)
	m.AdvanceVertical(-14)
	if m.Tm.E != 100 || m.Tm.F != 686 {
		t.Fatalf("Tm = (%v,%v), want (100,686)", m.Tm.E, m.Tm.F)
	}
}

func TestMachineVisibleRenderModes(t *testing.T) {
	// Modes 3 and 7 paint nothing but still advance, and are how a hidden OCR
	// layer sits under a scanned image. Reporting them is informational: that
	// text is exactly what an extractor wants.
	for mode := 0; mode <= 7; mode++ {
		m := NewMachine(geom.Identity)
		run(t, m, string(rune('0'+mode))+" Tr", nil)
		want := mode != 3 && mode != 7
		if m.Visible() != want {
			t.Errorf("mode %d: Visible = %v, want %v", mode, m.Visible(), want)
		}
	}
}

func TestMachineApplyReportsStateOperators(t *testing.T) {
	m := NewMachine(geom.Identity)
	stateOps := []string{
		"q", "Q", "cm", "W", "W*", "BT", "ET", "Tf", "Tc", "Tw", "Tz", "TL",
		"Ts", "Tr", "Td", "TD", "Tm", "T*", "BMC", "BDC", "EMC",
	}
	for _, name := range stateOps {
		if !m.Apply(Op{Name: name}) {
			t.Errorf("Apply(%s) = false, want true", name)
		}
	}
	// Text-showing and painting operators are the caller's business: Apply must
	// report false so a caller can tell what it still has to handle.
	for _, name := range []string{"Tj", "TJ", "'", `"`, "Do", "re", "f", "S", "n", "gs"} {
		if m.Apply(Op{Name: name}) {
			t.Errorf("Apply(%s) = true, want false", name)
		}
	}
}

func TestMachineShortOperandListsAreSafe(t *testing.T) {
	// Every state operator with no operands at all. A malformed stream supplies
	// these constantly, and a panic here loses the whole document.
	m := NewMachine(geom.Identity)
	for _, name := range []string{
		"cm", "Tf", "Tc", "Tw", "Tz", "TL", "Ts", "Tr", "Td", "TD", "Tm",
		"BMC", "BDC",
	} {
		m.Apply(Op{Name: name})
	}
	// Tz with no operand leaves Scale at 0, which is what the stream said. What
	// matters is that the machine is intact and still tracking.
	run(t, m, "/F1 10 Tf 100 Tz", nil)
	if m.GS.Text.Font != "F1" || m.GS.Text.Scale != 100 {
		t.Fatalf("machine damaged by empty operands: %+v", m.GS.Text)
	}
}

func TestMachineOverFullPageSequence(t *testing.T) {
	// An end-to-end sequence in the shape real producers emit, checking that
	// state, marked content, and position all track together.
	m := NewMachine(geom.Matrix{A: 1, D: -1, F: 792})
	var mcids []int
	var positions [][2]float64

	src := `
q 1 0 0 1 0 0 cm
/Artifact <</Type /Pagination /Subtype /Header>> BDC
  BT /F1 8 Tf 72 760 Td (running header) Tj ET
EMC
/P <</MCID 0>> BDC
  BT /F2 11 Tf 14 TL 72 700 Td
    (first line) Tj T*
    (second line) Tj
  ET
EMC
/H1 <</MCID 1>> BDC
  BT /F3 18 Tf 72 640 Td (Heading) Tj ET
EMC
Q`
	hooks := map[string]func(Op){
		"Tj": func(op Op) {
			if m.InArtifact() {
				return // headers and page numbers are not prose
			}
			mcids = append(mcids, m.MCID())
			rm := m.RenderMatrix()
			x, y := rm.Apply(0, 0)
			positions = append(positions, [2]float64{x, y})
		},
	}
	run(t, m, src, hooks)

	// Three Tj outside the artifact, one inside and skipped.
	wantMCIDs := []int{0, 0, 1}
	if len(mcids) != len(wantMCIDs) {
		t.Fatalf("MCIDs = %v, want %v", mcids, wantMCIDs)
	}
	for i := range wantMCIDs {
		if mcids[i] != wantMCIDs[i] {
			t.Fatalf("MCIDs = %v, want %v", mcids, wantMCIDs)
		}
	}

	// The CTM flips y, so device y = 792 - user y.
	want := [][2]float64{{72, 92}, {72, 106}, {72, 152}}
	for i, w := range want {
		if math.Abs(positions[i][0]-w[0]) > 1e-9 || math.Abs(positions[i][1]-w[1]) > 1e-9 {
			t.Errorf("position %d = %v, want %v", i, positions[i], w)
		}
	}

	// The trailing Q must restore the state saved by the leading q.
	if m.GS.Text.Font != "" {
		t.Errorf("Q did not restore the initial font: %q", m.GS.Text.Font)
	}
	if len(m.mcStack) != 0 {
		t.Errorf("marked-content stack not empty: %v", m.mcStack)
	}
}

func TestMachineBDCWithNamedPropertiesKeepsTag(t *testing.T) {
	// The named form carries the tag inline even though the properties are a
	// reference, so artifact detection works without resolving resources.
	m := NewMachine(geom.Identity)
	run(t, m, "/Artifact /Pr1 BDC", nil)
	if !m.InArtifact() {
		t.Fatal("named-property BDC should still register its tag")
	}
}

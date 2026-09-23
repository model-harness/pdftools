package content

import (
	"github.com/model-harness/pdftools/geom"
	"github.com/model-harness/pdftools/objects"
)

// maxStateDepth bounds the q/Q graphics state stack. Real content nests a few
// levels; an unbalanced stream can push without popping forever.
const maxStateDepth = 256

// TextState holds the text-related parameters of the graphics state
// (ISO 32000-2 §9.3). These persist across BT/ET blocks: a Tf outside any text
// object still applies to the next one.
type TextState struct {
	// Font is the resource name from Tf, and Size its operand. Size may be
	// negative, which mirrors the glyph, and may be zero, which makes text
	// invisible but still advancing.
	Font Name
	Size float64

	// CharSpace is Tc, added to every glyph's advance in unscaled text units.
	CharSpace float64

	// WordSpace is Tw, added to the advance of single-byte code 32 only. The
	// single-byte restriction is why a CID font with a two-byte code 32 is
	// unaffected, a rule that silently breaks naive extractors.
	WordSpace float64

	// Scale is Th from Tz, as a percentage. It scales horizontal advances and
	// glyph widths but not vertical movement.
	Scale float64

	// Leading is TL, the baseline-to-baseline distance used by T*, ', and ".
	// Positive leading moves down the page.
	Leading float64

	// Rise is Ts, the baseline offset for superscripts and subscripts.
	Rise float64

	// Render is Tr, the rendering mode. Mode 3 is invisible, and mode 7 adds to
	// a clipping path without painting. Both still advance the text position,
	// and both are how OCR layers hide text under a scanned image.
	Render int
}

// Name is a resource name, kept as a distinct type so a font resource key is not
// confused with a font's PostScript name.
type Name string

// GraphicsState is the part of the graphics state that affects where ink lands.
// The full state also includes color, dash patterns, line joins and caps, none of
// which move anything, so none of them are tracked.
type GraphicsState struct {
	CTM  geom.Matrix
	Text TextState

	// LineWidth is w, in unscaled user units. It moves no glyph, but a stroked
	// line has no area of its own — its whole extent perpendicular to itself is
	// this number — so a consumer reading path geometry cannot tell a hairline
	// from a 3pt border without it. Zero is legal and means the thinnest line the
	// device can render (§8.4.3.2), which is not the same as absent.
	//
	// Set by "w" and by nothing else, which is a known gap rather than the whole
	// story: an ExtGState may carry /LW, so "/GS0 gs" can change the line width
	// without a "w" anywhere, and this field then reports the stale value — 1 if
	// nothing set one. Reading it needs the page's resource dictionary, which this
	// package does not take on purpose: it interprets a stream and resolves no
	// references. Apply reports false for "gs" so a caller that does have the
	// resources can see the operator go by, and a caller that needs the width to
	// be right must handle it. Both directions are wrong in a way that matters to
	// a consumer measuring a mark: a 3pt border set through an ExtGState reports 1,
	// and a hairline set that way after an unrelated "3 w" reports 3.
	LineWidth float64

	// ClipDepth counts how many clipping paths are active. It is carried so a
	// consumer can tell that text may be clipped away, without this package
	// having to evaluate path geometry.
	ClipDepth int
}

// Machine tracks graphics and text state across a content stream.
//
// It is separate from Scanner because the same operator sequence drives both
// text extraction and image extraction, and because state tracking is where the
// subtle spec rules live. Keeping it apart from tokenizing means each can be
// tested against its own failure modes.
type Machine struct {
	// GS is the current graphics state.
	GS GraphicsState

	// Tm is the text matrix, Tlm the text line matrix. Both are only meaningful
	// between BT and ET, and both are reset by BT (§9.4.1).
	Tm  geom.Matrix
	Tlm geom.Matrix

	// InText reports whether the machine is inside a BT/ET pair. Text-showing
	// operators outside one are malformed but common, and are still processed
	// because the text is real.
	InText bool

	stack []GraphicsState

	// mcStack tracks marked-content nesting so the current MCID is known. This is
	// the join key between the structure tree and page text, so it has to be
	// exact: a mismatched EMC that silently cleared the whole stack would
	// misattribute the rest of the page.
	mcStack []markedContent
}

// markedContent is one open BMC/BDC region.
type markedContent struct {
	Tag  Name
	MCID int
	// HasMCID distinguishes a BDC with no /MCID from one with MCID 0.
	HasMCID bool
}

// NewMachine returns a Machine initialized for a page whose media box maps to
// base with the given CTM.
func NewMachine(ctm geom.Matrix) *Machine {
	return &Machine{
		GS: GraphicsState{
			CTM: ctm,
			// 1.0 is the initial line width (§8.4.3.2), not a guess: a stream that
			// strokes without setting w gets a 1-unit line, and defaulting to zero
			// would report it as a hairline.
			LineWidth: 1,
			Text:      TextState{Scale: 100},
		},
		Tm:  geom.Identity,
		Tlm: geom.Identity,
	}
}

// MCID returns the innermost marked-content identifier, or -1 when none is
// active. Innermost rather than outermost: nested BDCs occur, and the innermost
// is the one the structure tree references.
func (m *Machine) MCID() int {
	for i := len(m.mcStack) - 1; i >= 0; i-- {
		if m.mcStack[i].HasMCID {
			return m.mcStack[i].MCID
		}
	}
	return -1
}

// MarkedTag returns the innermost marked-content tag, or "" when none is active.
// Artifact regions are tagged this way, and are what must be dropped to keep
// running headers and page numbers out of extracted prose.
func (m *Machine) MarkedTag() Name {
	if n := len(m.mcStack); n > 0 {
		return m.mcStack[n-1].Tag
	}
	return ""
}

// InArtifact reports whether any enclosing marked-content region is an Artifact.
// Checking the whole stack, not just the innermost, because an Artifact region
// can contain nested marked content.
func (m *Machine) InArtifact() bool {
	for i := range m.mcStack {
		if m.mcStack[i].Tag == "Artifact" {
			return true
		}
	}
	return false
}

// Apply updates state for op and reports whether op was *fully* handled.
//
// Text-showing operators (Tj, TJ, ', ") are never fully handled: they need font
// metrics to advance the text matrix, which this package does not own. A caller
// handles them and calls Advance with the displacement it computed.
//
// ' and " are the two that need care, because each is a state change *and* a
// show. §9.4.3 defines ' as T* followed by Tj, and " as setting word and
// character spacing and then behaving as '. The line move and the spacing are
// pure state and need no metrics, so they happen here — and the report is still
// false, because the caller has a string left to draw. A caller that treats
// false as "nothing happened" is wrong about Tj too.
//
// The alternative was to leave both to the caller, and that is what this package
// did: the state half was spelled out in extract's walker and nowhere else, so
// render/native — written later, against this comment — assumed the machine had
// moved the line and drew every ' on the line above. §9.4.3 is one rule and two
// readings of it is one too many, which is the same argument this machine exists
// on.
func (m *Machine) Apply(op Op) bool {
	switch op.Name {
	// Graphics state.
	case "q":
		if len(m.stack) < maxStateDepth {
			m.stack = append(m.stack, m.GS)
		}
	case "Q":
		if n := len(m.stack); n > 0 {
			m.GS = m.stack[n-1]
			m.stack = m.stack[:n-1]
		}
	case "cm":
		if len(op.Operands) >= 6 {
			cm := geom.Matrix{
				A: op.Num(0), B: op.Num(1),
				C: op.Num(2), D: op.Num(3),
				E: op.Num(4), F: op.Num(5),
			}
			// cm concatenates: the new matrix applies before the existing CTM.
			m.GS.CTM = cm.Mul(m.GS.CTM)
		}
	case "w":
		// Taken as written, including a negative operand, which §8.4.3.2 does not allow. This
		// package reports what the stream says and leaves the judgement to a consumer — the
		// same reason Op.Num does not range-check anything else — and clamping here would make
		// a malformed width indistinguishable from a hairline.
		if len(op.Operands) >= 1 {
			m.GS.LineWidth = op.Num(0)
		}
	case "W", "W*":
		m.GS.ClipDepth++

	// Text object.
	case "BT":
		m.InText = true
		m.Tm = geom.Identity
		m.Tlm = geom.Identity
	case "ET":
		m.InText = false

	// Text state. These are legal outside BT/ET and persist across text objects.
	case "Tf":
		m.GS.Text.Font = Name(op.NameAt(0))
		m.GS.Text.Size = op.Num(1)
	case "Tc":
		m.GS.Text.CharSpace = op.Num(0)
	case "Tw":
		m.GS.Text.WordSpace = op.Num(0)
	case "Tz":
		m.GS.Text.Scale = op.Num(0)
	case "TL":
		m.GS.Text.Leading = op.Num(0)
	case "Ts":
		m.GS.Text.Rise = op.Num(0)
	case "Tr":
		m.GS.Text.Render = op.Int(0)

	// Text positioning.
	case "Td":
		m.Tlm = geom.Translate(op.Num(0), op.Num(1)).Mul(m.Tlm)
		m.Tm = m.Tlm
	case "TD":
		// TD sets leading to the negated y operand, then behaves as Td.
		m.GS.Text.Leading = -op.Num(1)
		m.Tlm = geom.Translate(op.Num(0), op.Num(1)).Mul(m.Tlm)
		m.Tm = m.Tlm
	case "Tm":
		if len(op.Operands) >= 6 {
			m.Tlm = geom.Matrix{
				A: op.Num(0), B: op.Num(1),
				C: op.Num(2), D: op.Num(3),
				E: op.Num(4), F: op.Num(5),
			}
			m.Tm = m.Tlm
		}
	case "T*":
		m.NextLine()
	case "'":
		// The state half of a quote, as this function's comment explains. Falls through to
		// the false report because the string still has to be drawn by the caller.
		m.NextLine()
		return false
	case `"`:
		if len(op.Operands) >= 2 {
			// The spacing operands persist after the string (§9.4.3), which is why they are
			// state and not arguments to this one show.
			m.GS.Text.WordSpace = op.Num(0)
			m.GS.Text.CharSpace = op.Num(1)
		}
		m.NextLine()
		return false

	// Marked content.
	case "BMC":
		m.mcStack = append(m.mcStack, markedContent{Tag: Name(op.NameAt(0))})
	case "BDC":
		mc := markedContent{Tag: Name(op.NameAt(0))}
		// The property list is either an inline dictionary or a name referring to
		// the page's /Properties resource. Only the inline form can be read here;
		// the caller resolves the named form and can correct the MCID.
		if d := op.Dict(1); d != nil {
			if v, ok := d["MCID"]; ok {
				if f, isNum := objects.AsNum(v); isNum {
					mc.MCID, mc.HasMCID = int(f), true
				}
			}
		}
		if len(m.mcStack) < maxStateDepth {
			m.mcStack = append(m.mcStack, mc)
		}
	case "EMC":
		if n := len(m.mcStack); n > 0 {
			m.mcStack = m.mcStack[:n-1]
		}

	default:
		return false
	}
	return true
}

// SetMCID overrides the innermost region's MCID. A BDC whose properties are a
// named resource rather than an inline dictionary needs this, since resolving the
// name requires the page resources that this package does not hold.
func (m *Machine) SetMCID(id int) {
	if n := len(m.mcStack); n > 0 {
		m.mcStack[n-1].MCID = id
		m.mcStack[n-1].HasMCID = true
	}
}

// NextLine moves to the next line using the current leading, as T* does.
func (m *Machine) NextLine() {
	m.Tlm = geom.Translate(0, -m.GS.Text.Leading).Mul(m.Tlm)
	m.Tm = m.Tlm
}

// Advance moves the text matrix horizontally by tx unscaled text-space units,
// as showing a glyph does. Callers compute tx from font metrics.
func (m *Machine) Advance(tx float64) {
	m.Tm = geom.Translate(tx, 0).Mul(m.Tm)
}

// AdvanceVertical moves the text matrix by ty, for vertical writing mode.
func (m *Machine) AdvanceVertical(ty float64) {
	m.Tm = geom.Translate(0, ty).Mul(m.Tm)
}

// RenderMatrix returns the composed matrix mapping text space to device space,
// per ISO 32000-2 §9.4.4.
//
// The parameter matrix folds in font size, horizontal scaling, and rise, and is
// composed with the text matrix and then the CTM. Getting this composition wrong
// is the usual cause of extracted text with plausible characters in an
// implausible order, because every position and every width derives from it.
func (m *Machine) RenderMatrix() geom.Matrix {
	ts := m.GS.Text
	th := ts.Scale / 100
	param := geom.Matrix{
		A: ts.Size * th,
		D: ts.Size,
		F: ts.Rise,
	}
	return param.Mul(m.Tm).Mul(m.GS.CTM)
}

// Visible reports whether text in the current state paints pixels. Mode 3 is
// invisible and mode 7 is clip-only; both are used for the hidden text layer
// under a scanned page, which is exactly the text an extractor wants, so this is
// informational rather than a filter.
func (m *Machine) Visible() bool {
	return m.GS.Text.Render != 3 && m.GS.Text.Render != 7
}

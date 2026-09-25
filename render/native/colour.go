package native

import (
	"fmt"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/icc"
	"github.com/model-harness/pdftools/objects"
)

// space is a resolved colour space (§8.6.3): how many components sc and scn read, and what
// colour they produce.
type space struct {
	// n is how many components sc and scn read. 0 for a refused space, where nothing is ever
	// painted in it anyway.
	n int

	// profile is nil for a space converted by paint.go's device formulas for n, and set for an
	// ICCBased space this backend reads.
	profile *icc.Profile

	// init is the colour cs and CS set.
	init paint

	// refused is why nothing painted in this space can be drawn; "" when it can.
	refused string
}

// deviceGray is DeviceGray with no resource behind it — the page's initial fill and stroke space
// (§8.6.3) — painted black, which is paint's zero value.
var deviceGray = space{n: 1}

// deviceOperands is how many operands each device colour operator reads, which is also the
// component count of the device space it selects.
var deviceOperands = map[string]int{"g": 1, "rg": 3, "k": 4, "G": 1, "RG": 3, "K": 4}

// deviceSpaces is the same count for the three device spaces by name.
var deviceSpaces = map[objects.Name]int{"DeviceGray": 1, "DeviceRGB": 3, "DeviceCMYK": 4}

// colour converts v, the components an sc, scn, SC or SCN read, to a paint in this space.
//
// Fewer than n components is a malformed operator with no better reading than the colour that
// was already there — pdfium ignores it too: measured, "/DeviceRGB cs 1 0 0 sc 0.5 sc" stays
// red. More than n uses the first n, which is the same tolerance the device colour operators
// already had.
func (sp space) colour(v []float64, was paint) paint {
	if len(v) < sp.n {
		return was
	}
	v = v[:sp.n]
	switch {
	case sp.profile != nil:
		r, g, b := sp.profile.SRGB(v)
		return paint{r, g, b}
	case sp.n == 1:
		return gray(v[0])
	case sp.n == 3:
		return rgb(v[0], v[1], v[2])
	case sp.n == 4:
		return cmyk(v[0], v[1], v[2], v[3])
	}
	return was
}

// setColour applies a colour operator, in both passes: the survey needs the resulting space to
// know what checkFill and checkStroke will later refuse, and the paint pass needs the colour
// itself. This is the one place every colour operator is applied. g rg k cs sc scn set the fill
// (w.fill, w.fillSpace); G RG K CS SC SCN the stroke (w.stroke, w.strokeSpace).
//
// Every component is clamped to 0..1 first, which unifies k — which read its raw components
// straight into cmyk until now — with K, which already clamped.
func (w *walker) setColour(op content.Op) {
	v := make([]float64, len(op.Operands))
	for i := range v {
		v[i] = clamp01(op.Num(i))
	}
	// Table 73 spells every fill operator in lower case and its stroke counterpart in upper case.
	sp, col := &w.fillSpace, &w.fill
	if op.Name[0] < 'a' {
		sp, col = &w.strokeSpace, &w.stroke
	}
	switch op.Name {
	case "g", "rg", "k", "G", "RG", "K":
		// With fewer than n operands the operator is malformed and changes nothing: there is no
		// colour to read and no better space to select than the one already in force.
		n := deviceOperands[op.Name]
		if len(v) < n {
			return
		}
		*sp = space{n: n}
		*col = sp.colour(v, *col)
	case "cs", "CS":
		if op.NameAt(0) == "" {
			return
		}
		*sp = w.colourSpace(op.NameAt(0), true)
		*col = sp.init
	default: // sc, scn, SC, SCN
		// A name operand, as in "/P0 scn", reads as 0 through op.Num — AsNum has no reading for a
		// name — and the Pattern space it names is refused anyway, so nothing here has to notice.
		*col = sp.colour(v, *col)
		return
	}
	// §8.6.5.6 consults the Default* resources "when a device colour space is selected", so a
	// space is checked against the resources in force here as well as where it is painted in.
	sp.refused = w.refusal(*sp)
}

// colourSpace resolves a colour space object (§8.6.3): a device name, a named resource when
// lookup is true, or a family array.
func (w *walker) colourSpace(o objects.Object, lookup bool) space {
	return w.colourSpaceAt(o, lookup, 0)
}

// colourSpaceAt is colourSpace with the recursion depth that only /ICCBased /Alternate grows.
// Indexed, Separation and DeviceN name a base or alternate space too, but they are refused before
// it is resolved, so the /Alternate chain is the one place a producer could nest spaces here
// without bound.
func (w *walker) colourSpaceAt(o objects.Object, lookup bool, depth int) space {
	if depth > 4 {
		return space{refused: "a colour space nested more than 4 deep"}
	}
	v, err := w.s.Resolve(o)
	if err != nil {
		return space{refused: "a colour space that did not resolve"}
	}
	switch cs := v.(type) {
	case objects.Name:
		if n, ok := deviceSpaces[cs]; ok {
			return space{n: n}
		}
		if cs == "Pattern" {
			return space{refused: "a Pattern colour"}
		}
		if !lookup {
			return space{refused: fmt.Sprintf("the colour space is /%s", cs)}
		}
		// No /ColorSpace dictionary at all reads as a nil map, where the name is just as missing.
		res, _ := objects.GetDict(w.s, w.res, "ColorSpace")
		entry, ok := res[cs]
		if !ok {
			return space{refused: fmt.Sprintf("a colour space not in the resources, /%s", cs)}
		}
		return w.colourSpaceAt(entry, false, depth)
	case objects.Array:
		if len(cs) == 0 {
			return space{refused: "a malformed colour space"}
		}
		fam, ok := cs[0].(objects.Name)
		if !ok {
			return space{refused: "a malformed colour space"}
		}
		switch fam {
		case "DeviceGray", "DeviceRGB", "DeviceCMYK":
			// A one-element array is the device space under another spelling — pdfium was
			// measured drawing it that way — so it goes through the same case as the bare name.
			return w.colourSpaceAt(fam, lookup, depth)
		case "ICCBased":
			return w.iccSpace(cs, depth)
		case "Pattern":
			return space{refused: "a Pattern colour"}
		default:
			// Indexed, I, Separation, DeviceN, Lab, CalGray, CalRGB, and anything this backend has
			// never heard of: every one needs a colour-management or palette decision this
			// package does not make, and none appears often enough in the corpus to be worth
			// building — see xobject.go's imageSpace for the images this also refuses.
			return space{refused: fmt.Sprintf("the colour space is /%s", fam)}
		}
	}
	return space{refused: "a malformed colour space"}
}

// iccSpace resolves an [/ICCBased stream] array (§8.6.5.5), including its §8.6.5.5 fallback when
// the profile itself cannot be read.
//
// Cached by the profile stream's reference when it has one, because a page selects the same
// space at every cs and parsing a profile each time would be the fonts cache's problem again —
// and because a resolved stream is a copy that a decode does not persist on, so the parse really
// would repeat.
func (w *walker) iccSpace(a objects.Array, depth int) space {
	if len(a) < 2 {
		return space{refused: "an ICCBased space without a profile"}
	}
	ref, isRef := a[1].(objects.Ref)
	if isRef {
		if sp, ok := w.iccSpaces[ref]; ok {
			return sp
		}
	}

	v, err := w.s.Resolve(a[1])
	st, isStream := v.(*objects.Stream)
	if err != nil || !isStream {
		return space{refused: "an ICCBased profile that is not a stream"}
	}
	n, _ := objects.GetInt(w.s, st.Dict, "N")
	if n != 1 && n != 3 && n != 4 {
		return space{refused: fmt.Sprintf("an ICCBased space with /N %d", n)}
	}
	if st.Decoded == nil {
		_ = w.s.Decode(st) // an error leaves Decoded nil, which icc.Parse below then refuses on its own
	}
	p, perr := icc.Parse(st.Decoded)

	var sp space
	switch {
	case perr == nil && p.Components() == int(n):
		sp = space{n: int(n), profile: p}
	default:
		alt, hasAlt := objects.Get(w.s, st.Dict, "Alternate")
		switch {
		case !hasAlt:
			// The device formula by N — pdfium was measured doing the same: garbage profile
			// bytes with /N 1 draw as DeviceGray and with /N 3 as DeviceRGB, and a gray profile
			// declared /N 3 draws as DeviceRGB.
			sp = space{n: int(n)}
		default:
			alternate := w.colourSpaceAt(alt, false, depth+1)
			switch {
			case alternate.refused != "":
				sp = space{refused: fmt.Sprintf(
					"an ICC profile this backend does not read, and its /Alternate: %s", alternate.refused)}
			case alternate.n != int(n):
				sp = space{refused: fmt.Sprintf(
					"an ICCBased /Alternate of %d components for /N %d", alternate.n, n)}
			default:
				sp = alternate
			}
		}
	}
	// §8.6.5.5 gives an ICCBased space an initial colour of all zeros, which through the CMYK
	// fallback is white and not black. (pdfium drew black for a garbage /N 4 profile, probably
	// because it failed to load the space at all; that is a documented divergence from pdfium, not
	// something copied here.) A refused space has n 0, so this leaves it black, as nothing ever
	// paints in it anyway.
	sp.init = sp.colour(make([]float64, n), black)
	if isRef {
		w.iccSpaces[ref] = sp
	}
	return sp
}

// defaultRefusal reports why the resources' /DefaultGray, /DefaultRGB or /DefaultCMYK for n
// components is not honoured, or "" when the resources define none.
//
// §8.6.5.6 and Table 73 have a Default* resource replace a device space "when a device colour
// space is selected". pdfium was measured honouring /DefaultGray for "/DeviceGray cs" fills and
// for a DeviceGray image, but not for g or G — where the specification's own reading applies it
// to g as well. No page in this corpus defines a Default* space, so nothing could settle which
// reading to follow, and refusing says so rather than silently picking one.
func (w *walker) defaultRefusal(n int) string {
	key := defaultSpaces[n]
	res, ok := objects.GetDict(w.s, w.res, "ColorSpace")
	if !ok {
		return ""
	}
	if _, ok := res[key]; ok {
		return fmt.Sprintf("the resources define /%s, which is not implemented", key)
	}
	return ""
}

var defaultSpaces = map[int]objects.Name{1: "DefaultGray", 3: "DefaultRGB", 4: "DefaultCMYK"}

// refusal is why nothing can be painted in sp, checked against the resources in force *where it
// is painted* as well as where it was selected — which is what lets a form's own /DefaultRGB
// refuse a colour chosen outside it, and the page's initial DeviceGray answer to a page-level
// /DefaultGray with nothing ever having set a colour space at all.
//
// A Default* resource refuses every colour a device formula would convert, however the space was
// reached: by a device operator or name, as an image's space or an Indexed base, or as an
// ICCBased space's /Alternate or its fallback by /N. Which of those a Default* replaces is
// exactly what the specification and pdfium disagree about, and with no population to settle
// it, one rule for all of them is what keeps the answer out of iccSpaces' cache — a space cached
// by reference must not depend on the resources in force where it was first resolved.
func (w *walker) refusal(sp space) string {
	if sp.refused != "" {
		return sp.refused
	}
	if sp.profile == nil {
		return w.defaultRefusal(sp.n)
	}
	return ""
}

// checkFill refuses a fill this backend cannot draw, by reason.
//
// Checked at the operator that paints, for the same reason a stroke is checked at checkStroke and
// not where its state was set: a colour space nothing paints in changes no pixel.
func (w *walker) checkFill() {
	if r := w.refusal(w.fillSpace); r != "" {
		w.unsup["fill: "+r] = true
	}
}

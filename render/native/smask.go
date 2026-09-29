package native

import (
	"fmt"

	"github.com/model-harness/pdftools/content"
	"github.com/model-harness/pdftools/objects"
)

// surveyedMask stands in for a soft mask in the survey, which computes none: what the survey
// refuses under a mask it only needs to know that one is in force.
var surveyedMask = &mask{}

// softMask is an ExtGState's luminosity soft mask (§11.6.5.2), read but not yet drawn: the group
// to draw, and the backdrop colour it is drawn over.
type softMask struct {
	g  xobject
	bc [3]uint8
}

// softMask reads an ExtGState's /SMask. has is false when there is no /SMask key; sm is nil when
// the key is /None, which ends a mask; why is the reason the mask cannot be drawn.
//
// Read as pdfium's LoadSMask reads it, with a refusal wherever pdfium would draw something this
// backend does not: an alpha mask, a transfer function, a backdrop in a space whose conversion is
// not the device formula.
func (w *walker) softMask(g objects.Dict) (sm *softMask, has bool, why string) {
	raw, has := g["SMask"]
	if !has {
		return nil, false, ""
	}
	v, err := w.s.Resolve(raw)
	if err != nil {
		return nil, true, "/SMask is neither /None nor a dictionary"
	}
	if nm, ok := v.(objects.Name); ok && nm == "None" {
		return nil, true, ""
	}
	d, ok := v.(objects.Dict)
	if !ok {
		return nil, true, "/SMask is neither /None nor a dictionary"
	}
	for k := range d {
		switch k {
		case "Type", "S", "G", "BC", "TR":
		default:
			return nil, true, fmt.Sprintf("/SMask /%s is not a key this backend reads", k)
		}
	}
	switch s, ok := objects.GetName(w.s, d, "S"); {
	case !ok:
		return nil, true, "/SMask /S is missing, and only /Luminosity is implemented"
	case s != "Luminosity":
		return nil, true, fmt.Sprintf("/SMask /S is /%s, and only /Luminosity is implemented", s)
	}
	sm = &softMask{g: xobject{name: "SMask /G", subtype: "Form"}}
	sm.g.ref, sm.g.isRef = d["G"].(objects.Ref)
	if o, err := w.s.Resolve(d["G"]); err == nil {
		sm.g.st, _ = o.(*objects.Stream)
	}
	if sm.g.st == nil {
		return nil, true, "/SMask has no /G group"
	}
	if st, _ := objects.GetName(w.s, sm.g.st.Dict, "Subtype"); st != "Form" {
		return nil, true, "/SMask has no /G group"
	}
	// pdfium uses /TR only when it is a function — a dictionary or a stream — and takes anything
	// else, /Identity included, as the identity.
	if o, err := w.s.Resolve(d["TR"]); err == nil {
		switch o.(type) {
		case objects.Dict, *objects.Stream:
			return nil, true, "/SMask /TR is a transfer function, which is not implemented"
		}
	}
	if _, ok := d["BC"]; ok {
		if why := w.backdrop(d, sm); why != "" {
			return nil, true, why
		}
	}
	return sm, true, ""
}

// backdrop reads /BC into sm.bc, as pdfium's GetBackgroundColor does: in the group's /CS, each
// channel the float32 value times 255 truncated. Only the two device spaces whose conversion is
// the component itself are read; a group with no /BC keeps black, sm.bc's zero value.
func (w *walker) backdrop(d objects.Dict, sm *softMask) string {
	grp, _ := objects.GetDict(w.s, sm.g.st.Dict, "Group")
	cs, ok := objects.GetName(w.s, grp, "CS")
	if _, has := grp["CS"]; !has {
		return "/SMask /BC has no group colour space to be read in"
	}
	n := map[objects.Name]int{"DeviceGray": 1, "DeviceRGB": 3}[cs]
	if !ok || n == 0 {
		shown := string(cs)
		if !ok {
			shown = "(not a name)"
		}
		return fmt.Sprintf("/SMask /BC is in /%s, and only DeviceGray and DeviceRGB backdrops are implemented", shown)
	}
	arr, _ := objects.GetArray(w.s, d, "BC")
	if len(arr) != n {
		return fmt.Sprintf("/SMask /BC has %d components for a space of %d", len(arr), n)
	}
	var v [3]float32
	for i, o := range arr {
		r, err := w.s.Resolve(o)
		f, isNum := objects.AsNum(r)
		if err != nil || !isNum || !(f >= 0 && f <= 1) {
			return "/SMask /BC has a component outside 0..1"
		}
		v[i] = float32(f)
	}
	if n == 1 {
		v[1], v[2] = v[0], v[0]
	}
	for i, c := range v {
		// #nosec G115 -- c is in 0..1, so this is 0..255.
		sm.bc[i] = uint8(int(c * 255))
	}
	return ""
}

// surveySoftMask records why a gs's soft mask cannot be drawn, and surveys its group.
func (w *walker) surveySoftMask(m *content.Machine, g objects.Dict, depth int) {
	sm, has, why := w.softMask(g)
	switch {
	case !has:
		return
	case why != "":
		w.unsup["gs: "+why] = true
		return
	case sm == nil:
		w.soft = nil
		return
	case w.inGroup:
		// A mask's group is drawn as pdfium draws it, onto a canvas of its own with nothing
		// under it; a second mask in there is a second canvas, and nothing in the corpus has one.
		w.unsup["gs: a soft mask inside a soft mask's group"] = true
		return
	}
	w.chargeArea("gs")
	w.inGroup = true
	w.inSoftMask(m, sm, func(sub *content.Machine) {
		// Asked inside, where the alpha is the group's initial 1 and not the one the gs found.
		if why := w.formRefusal(sm.g, depth); why != "" {
			w.unsup["gs: "+why] = true
			return
		}
		body, _ := w.formContent(sm.g)
		w.survey(sub, body, depth+1)
	})
	w.inGroup = false
	w.soft = surveyedMask
}

// paintSoftMask draws a gs's soft mask, which the survey has accepted, and puts it in force.
//
// pdfium's LoadSMask: the group drawn onto a canvas of its own, cleared to the backdrop colour
// and unclipped but for the group's /BBox, in the CTM in force at the gs and from the initial
// graphics state; then each pixel's luminosity, (30r + 59g + 11b)/100 in integers, is the mask.
// Drawn here, at the gs, rather than per object as pdfium draws it, because the mask is the same
// for every object it applies to: pdfium draws the part each object's box covers, and the parts
// agree.
func (w *walker) paintSoftMask(m *content.Machine, g objects.Dict, depth int) {
	sm, has, _ := w.softMask(g)
	if !has {
		return
	}
	if sm == nil {
		w.soft = nil
		return
	}
	page := w.canvas
	w.canvas = newCanvas(w.w, w.h)
	pix := w.canvas.img.Pix
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+1], pix[i+2] = sm.bc[0], sm.bc[1], sm.bc[2]
	}
	w.inSoftMask(m, sm, func(sub *content.Machine) {
		if bb, ok := numRect(w.s, sm.g.st.Dict, "BBox"); ok {
			w.path.rect(bb.X0, bb.Y0, bb.Width(), bb.Height(), w.base.mul(sub.GS.CTM), w.matrix32(sub))
			w.pending, w.pendingEO = true, false
			w.endPath()
		}
		body, _ := w.formContent(sm.g)
		w.paint(sub, body, depth+1)
	})
	lum := newMask(w.w, w.h)
	lum.y0, lum.y1 = 0, w.h
	for i := range lum.a {
		r, g, b := int(pix[i*4]), int(pix[i*4+1]), int(pix[i*4+2])
		// #nosec G115 -- a weighted mean of three bytes by weights summing to 100, so at most 255.
		lum.a[i] = uint8((b*11 + g*59 + r*30) / 100)
	}
	w.canvas = page
	w.soft = lum
}

// inSoftMask runs body as a soft mask's group, in the state pdfium draws one in: the group's
// /Matrix after the CTM in force at the gs, the initial graphics state otherwise, and the group's
// own /Resources or else the page's. The path being built, and everything q saves, is the page's
// again afterwards.
func (w *walker) inSoftMask(m *content.Machine, sm *softMask, body func(sub *content.Machine)) {
	p, pending, pendingEO := w.path, w.pending, w.pendingEO
	w.save()
	w.fill, w.stroke, w.fillSpace, w.strokeSpace = black, black, deviceGray, deviceGray
	w.alpha, w.strokeAlpha, w.pen, w.font, w.soft = 1, 1, defaultPen, nil, nil
	w.path, w.pending, w.pendingEO = path{}, false, false
	res, fonts := w.res, w.fonts
	w.res, w.fonts = w.pageRes, map[string]*textFont{}
	w.inForm(content.NewMachine(m.GS.CTM), sm.g, body)
	w.res, w.fonts = res, fonts
	w.restore()
	w.path, w.pending, w.pendingEO = p, pending, pendingEO
}

// merge composites one pixel of colour col drawn at alpha a8 in 255ths, as pdfium composites the
// ARGB bitmap it drew a mark into (CompositeRow_Argb2Rgb): the soft mask, when there is one,
// multiplied into the alpha first (MultiplyAlphaMask), then the clip's coverage cl, then
// FXDIB_ALPHA_MERGE, each step in integers and truncating.
func (c *canvas) merge(i int, col [3]uint8, a8 int, soft *mask, cl uint8) {
	sa := a8
	if soft != nil {
		sa = sa * int(soft.a[i]) / 255
	}
	sa = sa * int(cl) / 255
	o := i * 4
	for j := range 3 {
		// #nosec G115 -- a weighted mean of two bytes by weights summing to 255, so at most 255.
		c.img.Pix[o+j] = uint8((int(c.img.Pix[o+j])*(255-sa) + int(col[j])*sa) / 255)
	}
}

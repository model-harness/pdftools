package liberation

import (
	"strings"
	"testing"

	"github.com/model-harness/pdftools/font"
)

// TestFaceForStyle pins which embedded file each style resolves to.
//
// The mapping is three lines of switch and it was the least tested code in this increment: a style
// resolving to the wrong face substitutes a *real typeface* at the wrong weight or the wrong
// family, which renders a page that is entirely legible and set in the wrong type. That is harder
// to notice than a blank page and it is what this table exists for.
//
// Asserted on the file name rather than on rendered pixels, because the file name is the decision.
// Whether the bytes then draw correctly is render/native's acceptance test, and mixing the two
// would make this test fail for reasons that are not about this mapping.
func TestFaceForStyle(t *testing.T) {
	for _, c := range []struct {
		name string
		st   font.Style
		want string // empty means the request must be declined
	}{
		{"sans regular", font.Style{Family: font.FamilySans}, "faces/LiberationSans-Regular.ttf.gz"},
		{"sans bold", font.Style{Family: font.FamilySans, Bold: true}, "faces/LiberationSans-Bold.ttf.gz"},
		{"sans italic", font.Style{Family: font.FamilySans, Italic: true}, "faces/LiberationSans-Italic.ttf.gz"},
		{"sans bold italic", font.Style{Family: font.FamilySans, Bold: true, Italic: true},
			"faces/LiberationSans-BoldItalic.ttf.gz"},
		{"serif regular", font.Style{Family: font.FamilySerif}, "faces/LiberationSerif-Regular.ttf.gz"},
		{"serif bold", font.Style{Family: font.FamilySerif, Bold: true}, "faces/LiberationSerif-Bold.ttf.gz"},
		{"serif italic", font.Style{Family: font.FamilySerif, Italic: true}, "faces/LiberationSerif-Italic.ttf.gz"},
		{"serif bold italic", font.Style{Family: font.FamilySerif, Bold: true, Italic: true},
			"faces/LiberationSerif-BoldItalic.ttf.gz"},
		// Declined rather than answered with the nearest thing. A fixed-pitch page set in a
		// proportional face reflows every column it has, and a symbol page set in a text face
		// draws Latin letters where the page shows operators — both worse than not drawing it.
		{"mono is not carried", font.Style{Family: font.FamilyMono}, ""},
		{"mono bold is not carried either", font.Style{Family: font.FamilyMono, Bold: true}, ""},
		{"symbol never will be", font.Style{Family: font.FamilySymbol}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := fileFor(c.st)
			if c.want == "" {
				if ok {
					t.Errorf("fileFor(%v) = %q, want it declined", c.st, got)
				}
				return
			}
			if !ok {
				t.Fatalf("fileFor(%v) declined, want %q", c.st, c.want)
			}
			if got != c.want {
				t.Errorf("fileFor(%v) = %q, want %q", c.st, got, c.want)
			}
		})
	}
}

// TestEveryCarriedFaceLoadsAndParses reads all eight, because an embedded asset that does not
// inflate is a build problem that only shows at render time.
//
// Source.Face swallows a load error and declines, which is right for a page — there is no
// page-level recovery — and wrong as the only check, because it turns a broken build into a
// refusal nobody traces. This is where that would be caught instead.
func TestEveryCarriedFaceLoadsAndParses(t *testing.T) {
	seen := map[string]bool{}
	for _, fam := range []font.Family{font.FamilySans, font.FamilySerif} {
		for _, st := range []font.Style{
			{Family: fam},
			{Family: fam, Bold: true},
			{Family: fam, Italic: true},
			{Family: fam, Bold: true, Italic: true},
		} {
			name, ok := fileFor(st)
			if !ok {
				t.Fatalf("fileFor(%v) declined", st)
			}
			if seen[name] {
				t.Errorf("%v resolves to %s, which another style already claimed — two styles"+
					" sharing a file means one of them is served the wrong face", st, name)
			}
			seen[name] = true

			tt, err := load(name)
			if err != nil {
				t.Errorf("load(%s): %v", name, err)
				continue
			}
			// A face that parses but carries no Latin is an asset swapped for the wrong thing.
			// 'A' and 'z' rather than a glyph count, because a count cannot tell a text face from
			// a dingbat font of the same size.
			for _, r := range []rune{'A', 'z', '0'} {
				if _, ok := tt.GIDForRune(r); !ok {
					t.Errorf("%s has no glyph for %q", name, r)
				}
			}
		}
	}
	if len(seen) != 8 {
		t.Errorf("%d distinct faces, want 8", len(seen))
	}
}

// TestSourceCachesAndAnswersByStyle pins that the name is not consulted and the parse is not
// repeated.
//
// Two claims in one test because they are the same claim from either side: the cache is keyed by
// the resolved file, so two requests with different names and one style must return the *same*
// pointer. If it were keyed by name instead, a document with forty subset names would parse the
// same 410 KB face forty times.
func TestSourceCachesAndAnswersByStyle(t *testing.T) {
	s := New()
	a, ok := s.Face(font.FaceRequest{Name: "Helvetica", Style: font.Style{Family: font.FamilySans}})
	if !ok {
		t.Fatal("Helvetica declined")
	}
	b, ok := s.Face(font.FaceRequest{Name: "ArialMT", Style: font.Style{Family: font.FamilySans}})
	if !ok {
		t.Fatal("ArialMT declined")
	}
	if a != b {
		t.Error("two sans requests returned different programs — the style decides, and the" +
			" parse should be cached by the face rather than by the name")
	}
	c, ok := s.Face(font.FaceRequest{Name: "Times", Style: font.Style{Family: font.FamilySerif}})
	if !ok {
		t.Fatal("Times declined")
	}
	if c == a {
		t.Error("a serif request returned the sans program")
	}
}

// TestNoticeCarriesTheLicence pins the OFL obligation that cannot be met by a file in the repo.
//
// The licence must accompany the Font Software wherever it is redistributed, and a static binary
// with eight faces compiled into it is a redistribution. So the text has to be reachable *from the
// binary* — `pdfspec licenses` — and a LICENSE sitting in the source tree does nothing for someone
// who downloaded only the executable. This asserts the text is actually embedded rather than
// silently dropped by a change to the //go:embed pattern, which would be a licensing defect that
// no other test could see.
func TestNoticeCarriesTheLicence(t *testing.T) {
	n := Notice()
	for _, want := range []string{
		"SIL OPEN FONT LICENSE",
		"Reserved Font Name",
		"Liberation",
		"2.1.5",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("the notice does not mention %q", want)
		}
	}
	if len(n) < 3000 {
		t.Errorf("the notice is %d bytes, which is too short to be the OFL text", len(n))
	}
}

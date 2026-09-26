package font

import (
	"math"
	"testing"

	"github.com/model-harness/pdftools/internal/cffbuild"
)

// FuzzParseCFF feeds arbitrary bytes to ParseCFF and, when it succeeds, to Outline for every GID
// and a few name/code/CID lookups. Nothing here may panic, and no outline may carry a non-finite
// coordinate — a NaN or an infinity would be a rasterizer's problem, not a parse error's.
func FuzzParseCFF(f *testing.F) {
	square, diamond := cffbuild.Square(), cffbuild.Diamond()

	// A plain name-keyed font.
	f.Add(cffbuild.Builder{
		Glyphs: []cffbuild.Glyph{
			{Name: "A", CharString: square},
			{Name: "B", CharString: diamond},
		},
	}.Build())

	// A CID-keyed font, FDSelect format 3, two Font DICTs with different FontMatrixes.
	f.Add(cffbuild.Builder{
		CID:            true,
		FDSelectFormat: 3,
		FDs: []cffbuild.FontDict{
			{},
			{FontMatrix: []float64{0.002, 0, 0, 0.002, 0, 0}},
		},
		Glyphs: []cffbuild.Glyph{
			{CID: 1, FD: 0, CharString: square},
			{CID: 2, FD: 1, CharString: diamond},
		},
	}.Build())

	// Local and global subroutines, both called.
	f.Add(cffbuild.Builder{
		Subrs:  [][]byte{cffbuild.CS(10, 0, cffbuild.RLineTo, cffbuild.Return)},
		GSubrs: [][]byte{cffbuild.CS(0, 10, cffbuild.RLineTo, cffbuild.Return)},
		Glyphs: []cffbuild.Glyph{
			{Name: "A", CharString: cffbuild.CS(
				0, 0, cffbuild.RMoveTo,
				-107, cffbuild.CallSubr,
				-107, cffbuild.CallGSubr,
				cffbuild.EndChar)},
		},
	}.Build())

	// A seac: base "A" (SID 34) and accent "B" (SID 35), TN #5176 Appendix A.
	f.Add(cffbuild.Builder{
		Glyphs: []cffbuild.Glyph{
			{SID: 34, CharString: square},
			{SID: 35, CharString: diamond},
			{Name: "C", CharString: cffbuild.CS(10, 20, 65, 66, cffbuild.EndChar)},
		},
	}.Build())

	f.Fuzz(func(t *testing.T, data []byte) {
		cff, err := ParseCFF(data)
		if err != nil {
			return
		}
		for gid := 0; gid < cff.NumGlyphs(); gid++ {
			out, _, err := cff.Outline(uint16(gid), 1<<16) // #nosec G115 -- an INDEX count is 16-bit
			if err != nil {
				continue
			}
			for _, seg := range out {
				for _, p := range seg.P {
					if math.IsInf(p.X, 0) || math.IsNaN(p.X) || math.IsInf(p.Y, 0) || math.IsNaN(p.Y) {
						t.Fatalf("glyph %d has a non-finite coordinate: %v", gid, p)
					}
				}
			}
		}
		cff.GIDForName("A")
		cff.GIDForCode(65)
		cff.GIDForCID(0)
		cff.GIDForCID(1)
	})
}

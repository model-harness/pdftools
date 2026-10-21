package ocr

import (
	"image"
	"image/color"
	"image/draw"
	"sort"
)

// Table is a ruled table found on a page image: its box, the lines of its grid in the page
// image's pixels, its cells' text, and which words went into it.
type Table struct {
	Box    image.Rectangle
	XS, YS []int
	Cells  [][]string
	Words  []int
}

// tableMinRule is the shortest horizontal run of ink, in inches, that may be a table's rule:
// longer than a word, and a ruled table's rules run its width.
const tableMinRule = 0.5

// tableEdge is how far apart, in pixels, two rules' ends may be and be one table's rules.
const tableEdge = 6

// Tables finds the ruled tables on a page image read at dpi: rules of the same extent stacked
// three deep or more, whose region Grid confirms and whose cells words fill in two rows and two
// columns. Each word inside a table is placed in its cells (Cells) and named in Words, so that the
// caller writes it there and not again as text (ADR 0047).
func Tables(img image.Image, words []Word, dpi float64) []Table {
	segs := hrules(img, int(tableMinRule*dpi))
	var out []Table
	used := make([]bool, len(segs))
	for i := range segs {
		if used[i] {
			continue
		}
		group := []int{i}
		for j := i + 1; j < len(segs); j++ {
			if !used[j] && abs(segs[j].x0-segs[i].x0) <= tableEdge && abs(segs[j].x1-segs[i].x1) <= tableEdge {
				group = append(group, j)
			}
		}
		if len(group) < 3 {
			continue
		}
		top, bottom := segs[group[0]].y, segs[group[len(group)-1]].y
		box := image.Rect(segs[i].x0-2, top-2, segs[i].x1+3, bottom+3).Intersect(img.Bounds())
		crop := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
		draw.Draw(crop, crop.Bounds(), img, box.Min, draw.Src)
		// Grid fails only with fewer than three lines one way, which leaves one row or column at
		// most, and Filled rejects that below.
		xs, ys, _ := Grid(crop)
		var inside []Word
		var idx []int
		for wi, w := range words {
			c := image.Pt((w.Box.Min.X+w.Box.Max.X)/2, (w.Box.Min.Y+w.Box.Max.Y)/2)
			if c.In(box) {
				w.Box = w.Box.Sub(box.Min)
				inside = append(inside, w)
				idx = append(idx, wi)
			}
		}
		cells := Cells(inside, xs, ys)
		if !Filled(cells) {
			continue
		}
		for k := range xs {
			xs[k] += box.Min.X
		}
		for k := range ys {
			ys[k] += box.Min.Y
		}
		for _, g := range group {
			used[g] = true
		}
		out = append(out, Table{Box: box, XS: xs, YS: ys, Cells: cells, Words: idx})
	}
	return out
}

// Filled reports a grid with words in two rows and two columns of its cells, which a table is
// and a photograph with lines in it, read as a few words, is not: test-rewrite-images.pdf's
// "Never look back!" fell in one column of a grid its photograph's edges drew (ADR 0046).
func Filled(cells [][]string) bool {
	rows, cols := map[int]bool{}, map[int]bool{}
	for r, row := range cells {
		for c, text := range row {
			for _, ch := range text {
				if ch != ' ' {
					rows[r], cols[c] = true, true
					break
				}
			}
		}
	}
	return len(rows) >= 2 && len(cols) >= 2
}

// seg is one horizontal rule: the row of its centre and its extent.
type seg struct{ y, x0, x1 int }

// hrules finds the horizontal runs of ink at least minLen pixels long, a rule drawn several
// pixels thick being one, top to bottom.
func hrules(img image.Image, minLen int) []seg {
	b := img.Bounds()
	var rows [][2]int // per row, the longest qualifying run
	var at []int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		best, start, n := [2]int{}, 0, 0
		for x := b.Min.X; x <= b.Max.X; x++ {
			if x < b.Max.X && color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y < gridDark {
				if n == 0 {
					start = x
				}
				n++
				continue
			}
			if n >= minLen && n > best[1]-best[0] {
				best = [2]int{start, start + n}
			}
			n = 0
		}
		if best[1] > best[0] {
			rows = append(rows, best)
			at = append(at, y)
		}
	}
	var out []seg
	for i := 0; i < len(rows); {
		j := i
		for j+1 < len(rows) && at[j+1] == at[j]+1 && abs(rows[j+1][0]-rows[i][0]) <= tableEdge {
			j++
		}
		out = append(out, seg{y: (at[i] + at[j]) / 2, x0: rows[i][0], x1: rows[i][1]})
		i = j + 1
	}
	sort.Slice(out, func(i, j int) bool { return out[i].y < out[j].y })
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

package ocr

import (
	"image"
	"image/color"
	"sort"
	"strings"
)

// gridDark is the luma under which a pixel is ink, for finding rules: anything clearly not
// paper. A rule that falls between two pixel columns is drawn half in each, grey, and at 128
// image-kinds-table.png's frame broke into runs of half its height.
const gridDark = 200

// gridReach is the fraction of the image's width (or height) a dark run must cross to be a
// rule. A ruled table's frame crosses nearly all of its crop: image-kinds-table.png's runs
// span 96% of it, the six points of border aside. A chart's bars stop short: the tallest of
// image-kinds-chart.png's is under half its height.
const gridReach = 0.6

// Grid finds a ruled table's lines in img: the rows of pixels a horizontal rule runs along and
// the columns a vertical one does, each rule's centre once. ok is false unless there are three
// of each, a frame with a line inside it both ways, which a chart's frame alone is not.
func Grid(img image.Image) (xs, ys []int, ok bool) {
	b := img.Bounds()
	if b.Dx() < 8 || b.Dy() < 8 {
		return nil, nil, false
	}
	dark := func(x, y int) bool {
		return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y < gridDark
	}
	run := func(n int, at func(i int) bool) int {
		best, cur := 0, 0
		for i := 0; i < n; i++ {
			if at(i) {
				cur++
				best = max(best, cur)
			} else {
				cur = 0
			}
		}
		return best
	}
	var rows, cols []bool
	for y := b.Min.Y; y < b.Max.Y; y++ {
		rows = append(rows, float64(run(b.Dx(), func(i int) bool { return dark(b.Min.X+i, y) })) >= gridReach*float64(b.Dx()))
	}
	for x := b.Min.X; x < b.Max.X; x++ {
		cols = append(cols, float64(run(b.Dy(), func(i int) bool { return dark(x, b.Min.Y+i) })) >= gridReach*float64(b.Dy()))
	}
	ys, xs = centres(rows, b.Min.Y), centres(cols, b.Min.X)
	return xs, ys, len(xs) >= 3 && len(ys) >= 3
}

// centres returns the middle of each run of true, offset by base: one rule drawn two pixels
// thick is one line.
func centres(on []bool, base int) []int {
	var out []int
	for i := 0; i < len(on); {
		if !on[i] {
			i++
			continue
		}
		j := i
		for j < len(on) && on[j] {
			j++
		}
		out = append(out, base+(i+j-1)/2)
		i = j
	}
	return out
}

// Cells places words in the grid xs and ys bound: each in the cell its box's centre falls in,
// joined in reading order. A word of nothing but "|" is a rule read as text and is dropped, as is
// a word outside the grid.
func Cells(words []Word, xs, ys []int) [][]string {
	sort.Ints(xs)
	sort.Ints(ys)
	if len(xs) < 2 || len(ys) < 2 {
		return nil
	}
	at := func(lines []int, v int) int {
		for i := 0; i+1 < len(lines); i++ {
			if v >= lines[i] && v < lines[i+1] {
				return i
			}
		}
		return -1
	}
	cells := make([][][]Word, len(ys)-1)
	for r := range cells {
		cells[r] = make([][]Word, len(xs)-1)
	}
	for _, w := range words {
		if strings.Trim(w.Text, "|") == "" {
			continue
		}
		cx, cy := (w.Box.Min.X+w.Box.Max.X)/2, (w.Box.Min.Y+w.Box.Max.Y)/2
		r, c := at(ys, cy), at(xs, cx)
		if r < 0 || c < 0 {
			continue
		}
		cells[r][c] = append(cells[r][c], w)
	}
	out := make([][]string, len(cells))
	for r := range cells {
		out[r] = make([]string, len(cells[r]))
		for c, ws := range cells[r] {
			sort.SliceStable(ws, func(i, j int) bool {
				if ws[i].Line != ws[j].Line || ws[i].Block != ws[j].Block || ws[i].Par != ws[j].Par {
					return ws[i].Box.Min.Y < ws[j].Box.Min.Y
				}
				return ws[i].Box.Min.X < ws[j].Box.Min.X
			})
			parts := make([]string, len(ws))
			for i, w := range ws {
				parts[i] = w.Text
			}
			out[r][c] = strings.Join(parts, " ")
		}
	}
	return out
}

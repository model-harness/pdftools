package ocr

import (
	"image"
	"image/png"
	"os"
	"testing"
)

func pngAt(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestGridFindsTheRuledTable: the table image's five rows of rules and four columns are found;
// the chart's frame and bars, and a paragraph, are no grid.
func TestGridFindsTheRuledTable(t *testing.T) {
	xs, ys, ok := Grid(pngAt(t, "../testdata/reference/image-kinds-table.png"))
	if !ok || len(xs) != 4 || len(ys) != 5 {
		t.Errorf("table: %d columns of rules, %d rows, ok %v; want 4 and 5", len(xs), len(ys), ok)
	}
	for _, name := range []string{"chart", "text"} {
		if xs, ys, ok := Grid(pngAt(t, "../testdata/reference/image-kinds-"+name+".png")); ok {
			t.Errorf("%s: a grid of %d by %d", name, len(xs), len(ys))
		}
	}
}

// TestCellsPlaceWordsByTheirCentres: each word goes in the cell its centre is in, in reading
// order; a rule read as "|" and a word outside the grid are dropped.
func TestCellsPlaceWordsByTheirCentres(t *testing.T) {
	xs, ys := []int{100, 10, 190}, []int{10, 100, 190}
	words := []Word{
		{Text: "two", Box: image.Rect(50, 30, 80, 50), Line: 1},
		{Text: "one", Box: image.Rect(15, 30, 45, 50), Line: 1},
		{Text: "under", Box: image.Rect(15, 60, 60, 80), Line: 2},
		{Text: "|", Box: image.Rect(98, 30, 102, 50), Line: 1},
		{Text: "||", Box: image.Rect(98, 130, 102, 150), Line: 3},
		{Text: "right", Box: image.Rect(110, 120, 180, 140), Line: 3},
		{Text: "outside", Box: image.Rect(200, 120, 260, 140), Line: 3},
	}
	got := Cells(words, xs, ys)
	want := [][]string{{"one two under", ""}, {"", "right"}}
	if len(got) != 2 || len(got[0]) != 2 || got[0][0] != want[0][0] || got[0][1] != "" || got[1][1] != "right" || got[1][0] != "" {
		t.Errorf("cells = %q, want %q", got, want)
	}
	if Cells(words, []int{10}, ys) != nil {
		t.Error("one column line made cells")
	}
}

// TestIsTextImageNeedsWordsCoveringIt: a text image reads as text, has five words and more, and
// they cover 30% of it.
func TestIsTextImageNeedsWordsCoveringIt(t *testing.T) {
	bounds := image.Rect(0, 0, 100, 100)
	line := func(n, h int, conf float64) []Word {
		var ws []Word
		for i := 0; i < n; i++ {
			ws = append(ws, Word{Text: "word", Conf: conf, Box: image.Rect(0, i*20, 100, i*20+h)})
		}
		return ws
	}
	for _, c := range []struct {
		name  string
		words []Word
		want  bool
	}{
		{"five lines covering 30%", line(5, 6, 95), true},
		{"covering 25%", line(5, 5, 95), false},
		{"four words", line(4, 10, 95), false},
		{"read as noise", line(5, 10, 79), false},
	} {
		if got := IsTextImage(c.words, bounds); got != c.want {
			t.Errorf("%s: %v, want %v (cover %.2f)", c.name, got, c.want, Cover(c.words, bounds))
		}
	}
	if Cover(nil, image.Rectangle{}) != 0 {
		t.Error("an empty image has cover")
	}
	// A box that overhangs the image counts for what is inside it.
	if got := Cover([]Word{{Box: image.Rect(-50, 0, 50, 100)}}, bounds); got != 0.5 {
		t.Errorf("an overhanging box covers %.2f, want 0.50", got)
	}
}

// TestGridNeedsRulesAcrossTheImage: lines that reach half way are not rules, a rule's centre is
// its middle row, and an image under 8 pixels is too small to hold a table.
func TestGridNeedsRulesAcrossTheImage(t *testing.T) {
	lined := func(w, h, reach int, at ...int) image.Image {
		img := image.NewGray(image.Rect(0, 0, w, h))
		for i := range img.Pix {
			img.Pix[i] = 255
		}
		for _, a := range at {
			for i := 0; i < reach; i++ {
				img.Pix[a*w+i] = 0 // a row
				img.Pix[i*w+a] = 0 // a column
			}
		}
		return img
	}
	if _, _, ok := Grid(lined(100, 100, 50, 10, 50, 90)); ok {
		t.Error("lines across half the image made a grid")
	}
	if xs, ys, ok := Grid(lined(100, 100, 100, 10, 50, 90)); !ok || len(xs) != 3 || len(ys) != 3 {
		t.Errorf("rules across the image: %v %v %v", xs, ys, ok)
	}
	if _, _, ok := Grid(lined(7, 7, 7, 0, 3, 6)); ok {
		t.Error("a 7-pixel image made a grid")
	}
	if got := centres([]bool{false, true, true, true, true, false, true}, 10); len(got) != 2 || got[0] != 12 || got[1] != 16 {
		t.Errorf("centres = %v, want [12 16]", got)
	}
}

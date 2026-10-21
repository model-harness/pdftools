package gemma

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/model-harness/pdftools/internal/inferd"
)

func TestCleanStripsControlTokens(t *testing.T) {
	for in, want := range map[string]string{
		"It's a scenic photograph.<turn|>":             "It's a scenic photograph.",
		"<start_of_turn>model\nword<end_of_turn>":      "model\nword",
		"  Association <|end|>":                        "Association",
		"a < b and c > d":                              "a < b and c > d",
		"x <|tool_call>{}<tool_call|> y":               "x {} y",
		"keep <Type /Page> literal, not a turn marker": "keep <Type /Page> literal, not a turn marker",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoopsFindsRepetition(t *testing.T) {
	line := "| 0x00 | 0x01 | 0x02 |"
	for _, c := range []struct {
		name string
		in   string
		want bool
	}{
		{"three lines repeated", strings.Repeat("a\nrow one\nrow two\n", 3), true},
		{"a line three times in four", line + "\n" + line + "\nother\n" + line, true},
		{"three lines, one repeated", line + "\n" + line + "\n" + line, false},
		{"short lines may repeat", "|\n|\n|\n|", false},
		{"a word eight times", strings.Repeat("the ", 8), true},
		{"a word seven times", strings.Repeat("the ", 7), false},
		{"ordinary prose", "The first paragraph runs long enough to wrap across three lines of type.", false},
	} {
		if got := Loops(c.in); got != c.want {
			t.Errorf("%s: Loops = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGuardRead(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		max  int
		ok   bool
	}{
		{"a word", "Association", 20, true},
		{"at the bound", "abcde", 5, true},
		{"overlong", "abcdef", 5, false},
		{"empty", "", 5, false},
		{"two lines", "IoU-based\nreward", 30, false},
		{"bold", "**Association**", 30, false},
		{"code", "`linkDest`", 30, false},
		{"LaTeX", "$\\mathcal{O}$", 30, false},
		{"a loop", strings.Repeat("ab ", 9), 40, false},
	} {
		err := GuardRead(c.in, c.max)
		if (err == nil) != c.ok {
			t.Errorf("%s: GuardRead(%q, %d) = %v", c.name, c.in, c.max, err)
		}
		if err != nil && !errors.Is(err, ErrRefused) {
			t.Errorf("%s: error %v is not ErrRefused", c.name, err)
		}
	}
}

func TestParseDescription(t *testing.T) {
	ok := map[string]Description{
		"photo: a geyser erupting, surrounded by spectators": {"photo", "a geyser erupting, surrounded by spectators"},
		"`diagram: three boxes joined by arrows`":            {"diagram", "three boxes joined by arrows"},
		"bar chart: monthly sales for 2024":                  {"bar chart", "monthly sales for 2024"},
	}
	for in, want := range ok {
		got, err := ParseDescription(in)
		if err != nil || got != want {
			t.Errorf("ParseDescription(%q) = %+v, %v; want %+v", in, got, err, want)
		}
		if got.String() != want.Tag+": "+want.Text {
			t.Errorf("String() = %q", got.String())
		}
	}
	for _, in := range []string{
		// Google's captioning template, answered as the user saw it answered.
		"This image shows the **Golden Gate Bridge** in San Francisco, California.\n\nKey elements",
		"Photo: a bridge over water at dusk",
		"photo: short",
		"photo:",
		"a photo of a bridge",
		"photo: " + strings.Repeat("the ", 10),
		"photo: " + strings.Repeat("x", 401),
	} {
		if _, err := ParseDescription(in); !errors.Is(err, ErrRefused) {
			t.Errorf("ParseDescription(%.40q) = %v, want ErrRefused", in, err)
		}
	}
}

func TestParseTable(t *testing.T) {
	rows, err := ParseTable("| Month | Sales |\n| --- | ---: |\n| Jan | 10 |\n| Feb | 12 |\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][0] != "Month" || rows[2][1] != "12" {
		t.Errorf("rows = %v", rows)
	}
	for name, in := range map[string]string{
		"prose":            "The chart shows sales rising.",
		"no separator":     "| a | b |\n| 1 | 2 |",
		"header only":      "| a | b |\n|---|---|",
		"a ragged row":     "| a | b |\n|---|---|\n| 1 |",
		"text after":       "| a | b |\n|---|---|\n| 1 | 2 |\nThat is the data.",
		"short separator":  "| a | b |\n|--|--|\n| 1 | 2 |",
		"a looping table":  "| a | b |\n|---|---|\n" + strings.Repeat("| 1 | 2 |\n", 4),
		"an empty message": "",
	} {
		if _, err := ParseTable(in); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: ParseTable = %v, want ErrRefused", name, err)
		}
	}
}

func TestRGBPacksPixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(5, 7, 7, 8))
	img.Set(5, 7, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	img.Set(6, 7, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	if got := RGB(img); string(got) != string([]byte{10, 20, 30, 200, 100, 50}) {
		t.Errorf("RGB = %v", got)
	}
}

func TestReadTokensBoundsTheReply(t *testing.T) {
	if readTokens(1) != 16 || readTokens(40) != 48 {
		t.Errorf("readTokens = %d, %d", readTokens(1), readTokens(40))
	}
}

// TestOpenWithoutADaemon: no daemon is ErrUnavailable, the caller's signal to go on without it.
func TestOpenWithoutADaemon(t *testing.T) {
	if _, err := Open(context.Background(), "", `\\.\pipe\pdfspec-test-no-such-admin`); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Open = %v, want ErrUnavailable", err)
	}
}

func TestCheckStopRefusesACutOffReply(t *testing.T) {
	if _, err := checkStop(inferd.Result{Text: "row row row", StopReason: "max_tokens"}, 16); !errors.Is(err, ErrRefused) {
		t.Errorf("a reply cut off: %v, want ErrRefused", err)
	}
	if s, err := checkStop(inferd.Result{Text: " word<turn|> ", StopReason: "end_turn"}, 16); err != nil || s != "word" {
		t.Errorf("a whole reply: %q, %v", s, err)
	}
}

func TestChooseNeedsAReadyVisionBackend(t *testing.T) {
	vision := inferd.Capability{Backend: "gemma-4-e4b", V2: true, Vision: true}
	for _, c := range []struct {
		name string
		st   inferd.Status
		want string
	}{
		{"ready", inferd.Status{State: "ready", Capabilities: []inferd.Capability{{Backend: "e"}, vision}}, "gemma-4-e4b"},
		{"loading", inferd.Status{State: "loading_model", Capabilities: []inferd.Capability{vision}}, ""},
		{"no vision", inferd.Status{State: "ready", Capabilities: []inferd.Capability{{Backend: "e", V2: true}}}, ""},
	} {
		got, err := choose(c.st)
		if got != c.want || (err == nil) != (c.want != "") {
			t.Errorf("%s: choose = %q, %v", c.name, got, err)
		}
		if err != nil && !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: %v is not ErrUnavailable", c.name, err)
		}
	}
}

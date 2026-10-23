// Package gemma asks inferd's vision model what only a model can answer: a second reading of a
// word Tesseract is unsure of, a description of a picture, and the values of a chart (ADR 0044).
//
// The converter runs without it. Open reads inferd's admin socket and returns ErrUnavailable
// when no daemon is running, it is still loading, or its model sees no images; the caller then
// goes on as if it had never asked. Every answer passes a guard before it is used. A model's
// failures read as fluently as its successes, which is what the guards are for: a loop of
// repeated lines, a reply longer than its image could hold, a reply cut off by the token bound,
// or the wrong shape is refused, and the caller falls back.
package gemma

import (
	"context"
	"errors"
	"fmt"
	"image"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/model-harness/pdftools/internal/inferd"
	"github.com/model-harness/pdftools/ocr"
)

// ErrUnavailable reports that inferd has no ready model that sees images.
var ErrUnavailable = errors.New("inferd has no ready vision model")

// ErrRefused reports that an answer failed its guard. It is ocr.ErrRefused, so ocr.Confirm
// counts a refusal as a disagreement and any other error as the model failing.
var ErrRefused = ocr.ErrRefused

// Model is inferd's vision backend.
type Model struct {
	client  *inferd.Client
	backend string
}

// Open finds inferd's vision backend from its admin socket. addr and admin are the generation
// and admin endpoints, empty for the platform defaults, and timeout bounds each request to the
// model: zero is inferd.DefaultTimeout, and negative waits as long as the model takes.
func Open(ctx context.Context, addr, admin string, timeout time.Duration) (*Model, error) {
	st, err := inferd.ReadStatus(ctx, admin)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	backend, err := choose(st)
	if err != nil {
		return nil, err
	}
	return &Model{client: &inferd.Client{Address: addr, Timeout: timeout}, backend: backend}, nil
}

// choose is the backend Open uses: a ready daemon's v2 backend that sees images.
func choose(st inferd.Status) (string, error) {
	c, ok := st.Vision()
	if !st.Ready() || !ok {
		return "", ErrUnavailable
	}
	return c.Backend, nil
}

// Name identifies the model for the page report: "gemma-4-e4b via inferd".
func (m *Model) Name() string { return m.backend + " via inferd" }

// Prompts, each with the shape of its answer stated, since a vision model left to choose writes
// Markdown, LaTeX and a preamble. ReadPrompt is the one the word-reading spike measured; Google's
// documented question, "What does the text in this image say?", was answered with a summary and
// matched 2 of 712 words (ADR 0043).
const (
	ReadPrompt     = "Transcribe the text in this image exactly as written. Output only the text."
	DescribePrompt = "Describe this image for someone who cannot see it, in one line of the form " +
		"`tag: description`, where tag is one lowercase word for what the image is (photo, diagram, " +
		"logo, screenshot, drawing, map) and description is one sentence of what it shows. " +
		"Output only that line."
	ChartPrompt = "This image is a chart. Write its data as a Markdown table: a header row naming " +
		"each column, a separator row, then one row per data point, with the values as the chart " +
		"shows them. Output only the table."
)

// ask sends img image-first, as Google's examples do and the spike measured, and returns the
// cleaned text.
func (m *Model) ask(ctx context.Context, img image.Image, prompt string, maxTokens int) (string, error) {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return "", fmt.Errorf("%w: an empty image", ErrRefused)
	}
	zero := 0.0
	res, err := m.client.Post(ctx, inferd.Request{
		Messages: []inferd.Message{{Role: inferd.RoleUser, Content: prompt, ImageRefs: []string{"img"}, ImagesFirst: true}},
		Attachments: []inferd.Attachment{{ID: "img", Width: uint32(b.Dx()), Height: uint32(b.Dy()), // #nosec G115 -- bounds of a rendered page, positive and far below 2^32
			RGB: RGB(img)}},
		Temperature: &zero,
		MaxTokens:   &maxTokens,
	})
	if err != nil {
		return "", err
	}
	return checkStop(res, maxTokens)
}

// checkStop refuses a reply the token bound cut off, which is how a loop ends, and cleans the
// rest.
func checkStop(res inferd.Result, maxTokens int) (string, error) {
	if res.StopReason == "max_tokens" {
		return "", fmt.Errorf("%w: cut off at %d tokens", ErrRefused, maxTokens)
	}
	return Clean(res.Text), nil
}

// RGB packs img as width*height*3 interleaved octets, the daemon's image format (inferd ADR
// 0016): it links no codec, so the bytes go as decoded pixels. Alpha is dropped.
func RGB(img image.Image) []byte {
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			out = append(out, byte(r>>8), byte(g>>8), byte(bl>>8))
		}
	}
	return out
}

// Read is a second reading of the text in a crop, a word or a line, of at most maxRunes
// characters. Its guard refuses a loop, a reply longer than maxRunes, a reply on more lines than
// the crop could hold, and Markdown or LaTeX, which a transcription has no use for.
func (m *Model) Read(ctx context.Context, img image.Image, maxRunes int) (string, error) {
	s, err := m.ask(ctx, img, ReadPrompt, readTokens(maxRunes))
	if err != nil {
		return "", err
	}
	return s, GuardRead(s, maxRunes)
}

// readTokens bounds a reading's generation: a token is at least a character in the worst case,
// and the bound is what turns a loop into a refusal in bounded time.
func readTokens(maxRunes int) int { return max(16, maxRunes+8) }

// GuardRead is Read's guard.
func GuardRead(s string, maxRunes int) error {
	switch {
	case s == "":
		return fmt.Errorf("%w: no text", ErrRefused)
	case utf8.RuneCountInString(s) > maxRunes:
		return fmt.Errorf("%w: %d characters where the crop holds at most %d", ErrRefused, utf8.RuneCountInString(s), maxRunes)
	case strings.Contains(s, "\n"):
		return fmt.Errorf("%w: more than one line", ErrRefused)
	// LaTeX's math is between two dollars; one is a price, or a shell's prompt (ADR 0048).
	case strings.Contains(s, "**") || strings.Contains(s, "`") || strings.Count(s, "$") >= 2:
		return fmt.Errorf("%w: markup in a transcription", ErrRefused)
	case Loops(s):
		return fmt.Errorf("%w: a repetition loop", ErrRefused)
	}
	return nil
}

// Description is a picture described: what kind of image it is, and what it shows.
type Description struct{ Tag, Text string }

// String is the Markdown form, "tag: description".
func (d Description) String() string { return d.Tag + ": " + d.Text }

// describeTokens bounds a description, one sentence.
const describeTokens = 160

// Describe describes a picture. Its guard refuses any reply not of the form "tag: description".
func (m *Model) Describe(ctx context.Context, img image.Image) (Description, error) {
	s, err := m.ask(ctx, img, DescribePrompt, describeTokens)
	if err != nil {
		return Description{}, err
	}
	return ParseDescription(s)
}

var description = regexp.MustCompile("^`?([a-z][a-z -]{0,23}):\\s+(\\S.{8,399}?)`?$")

// ParseDescription reads "tag: description" from s.
func ParseDescription(s string) (Description, error) {
	if Loops(s) {
		return Description{}, fmt.Errorf("%w: a repetition loop", ErrRefused)
	}
	m := description.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Description{}, fmt.Errorf("%w: not of the form tag: description: %.60q", ErrRefused, s)
	}
	return Description{Tag: strings.TrimSpace(m[1]), Text: strings.TrimSpace(m[2])}, nil
}

// chartTokens bounds a chart's table.
const chartTokens = 1024

// Chart reads a chart's values as a table, header first. Its guard refuses a reply that is not
// a GFM table of at least one data row, with every row as wide as the header.
func (m *Model) Chart(ctx context.Context, img image.Image) ([][]string, error) {
	s, err := m.ask(ctx, img, ChartPrompt, chartTokens)
	if err != nil {
		return nil, err
	}
	return ParseTable(s)
}

// ParseTable reads a GFM table from s: a header row, a separator row, then data rows.
func ParseTable(s string) ([][]string, error) {
	if Loops(s) {
		return nil, fmt.Errorf("%w: a repetition loop", ErrRefused)
	}
	var rows [][]string
	for i, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") || len(line) < 2 {
			return nil, fmt.Errorf("%w: line %d is not a table row", ErrRefused, i+1)
		}
		cells := strings.Split(line[1:len(line)-1], "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		if i == 1 {
			if !separator(cells) {
				return nil, fmt.Errorf("%w: the second row is not a separator", ErrRefused)
			}
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%w: a table needs a header and a row", ErrRefused)
	}
	for i, r := range rows {
		if len(r) != len(rows[0]) {
			return nil, fmt.Errorf("%w: row %d has %d cells, the header %d", ErrRefused, i, len(r), len(rows[0]))
		}
	}
	return rows, nil
}

var separatorCell = regexp.MustCompile(`^:?-{3,}:?$`)

func separator(cells []string) bool {
	for _, c := range cells {
		if !separatorCell.MatchString(c) {
			return false
		}
	}
	return true
}

// controlToken matches the chat template's markers when they leak into the text: Gemma's
// <start_of_turn>, <end_of_turn>, and the <turn|> and <|...|> forms.
var controlToken = regexp.MustCompile(`<\|[^<>|]{0,32}\|>|<[a-z_]{0,32}\|>|<\|[a-z_]{0,32}>|</?(start|end)_of_turn>`)

// Clean removes leaked control tokens and the space around the answer. A captioning reply the
// user ran through transformers ended in "<turn|>".
func Clean(s string) string {
	return strings.TrimSpace(controlToken.ReplaceAllString(s, ""))
}

// Loops reports a repetition loop, the failure a vision model ends in on dense input: a line
// said three times or more where there are four lines or more, or a run of one word eight times.
// The spike saw three lines repeated fifty times on one page.
func Loops(s string) bool {
	lines := strings.Split(s, "\n")
	if len(lines) >= 4 {
		seen := map[string]int{}
		for _, l := range lines {
			if l = strings.TrimSpace(l); len(l) >= 3 {
				seen[l]++
				if seen[l] >= 3 {
					return true
				}
			}
		}
	}
	run, prev := 0, ""
	for _, w := range strings.Fields(s) {
		if w == prev {
			run++
			if run >= 8 {
				return true
			}
			continue
		}
		run, prev = 1, w
	}
	return false
}

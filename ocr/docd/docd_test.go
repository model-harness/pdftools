package docd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// The subprocess half of this package is not unit-tested, deliberately: exercising it
// needs a real llama-server, a model download, and a GPU decision, which is an
// integration concern and lives with the verb. What is tested here is everything that
// runs on the response — the SSE reader and the PNG encoder — because those are pure,
// they are where a page silently loses content, and neither needs a model to be wrong.

// sse builds a server-sent event stream from raw event bodies, the way llama-server
// frames them: "data: " prefix, blank line between events.
func sse(events ...string) string {
	var sb strings.Builder
	for _, e := range events {
		sb.WriteString("data: ")
		sb.WriteString(e)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func delta(text string) string {
	return `{"choices":[{"delta":{"content":"` + text + `"}}]}`
}

func collect(t *testing.T, body string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := stream(strings.NewReader(body), func(s string) { sb.WriteString(s) })
	return sb.String(), err
}

func TestStreamConcatenatesDeltas(t *testing.T) {
	got, err := collect(t, sse(delta("<doctag>"), delta("<text>hi</text>"), delta("</doctag>"), "[DONE]"))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if want := "<doctag><text>hi</text></doctag>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A stream that ends without [DONE] is the 90%-complete page. It must be reported,
// because DocTags parses a truncated document happily and the caller has no other way
// to tell a finished page from a dropped connection.
func TestStreamWithoutDoneMarkerIsAnError(t *testing.T) {
	got, err := collect(t, sse(delta("<doctag><text>half a pa")))
	if err == nil {
		t.Fatal("expected an error for a stream that ended without [DONE]")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error %q does not say the page may be truncated", err)
	}
	// The partial text still comes back: the caller keeps what was generated.
	if want := "<doctag><text>half a pa"; got != want {
		t.Errorf("got %q, want the partial text %q", got, want)
	}
}

// An error object in the stream is fatal and carries the server's message, rather than
// surfacing as a truncated page with no explanation.
func TestStreamReportsServerError(t *testing.T) {
	body := sse(delta("<doctag>"), `{"error":{"message":"context shift disabled"}}`)
	got, err := collect(t, body)
	if err == nil {
		t.Fatal("expected the stream's error to be returned")
	}
	if !strings.Contains(err.Error(), "context shift disabled") {
		t.Errorf("error %q does not carry the server's message", err)
	}
	if got != "<doctag>" {
		t.Errorf("got %q, want the text emitted before the error", got)
	}
}

// Non-data lines are the SSE frame, not content. Concatenating them would put "event:"
// and comment text into the document.
func TestStreamIgnoresNonDataLines(t *testing.T) {
	body := ": ping\nevent: message\n" + sse(delta("a")) + "id: 7\nretry: 100\n" + sse(delta("b"), "[DONE]")
	got, err := collect(t, body)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got != "ab" {
		t.Errorf("got %q, want %q", got, "ab")
	}
}

// One malformed chunk does not discard a nearly complete page. This is the documented
// behaviour, and the assertion that would fail if the skip became a fatal error.
func TestStreamSkipsMalformedChunk(t *testing.T) {
	body := sse(delta("a"), "{not json", delta("b"), "[DONE]")
	got, err := collect(t, body)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got != "ab" {
		t.Errorf("got %q, want %q — a malformed chunk should be skipped, not fatal", got, "ab")
	}
}

// A delta longer than bufio.Scanner's 64 KiB default. The failure this guards is the
// worst kind: a page that comes back truncated with no error at all, because Scanner
// stops on a long line and reports it as end-of-input.
func TestStreamHandlesLineOverDefaultScannerLimit(t *testing.T) {
	long := strings.Repeat("x", 200<<10)
	got, err := collect(t, sse(delta(long), "[DONE]"))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got != long {
		t.Errorf("got %d bytes, want %d: a long SSE line was truncated", len(got), len(long))
	}
}

// A reader that fails mid-stream is an error, not a quiet short page.
func TestStreamReportsReadError(t *testing.T) {
	boom := errors.New("connection reset")
	r := readerFunc(func(p []byte) (int, error) {
		n := copy(p, "data: "+delta("a")+"\n\n")
		return n, boom
	})
	var sb strings.Builder
	if err := stream(r, func(s string) { sb.WriteString(s) }); err == nil {
		t.Fatal("expected the read error to be reported")
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// dataURI must produce something a chat endpoint accepts and a model can decode: the
// documented prefix, valid base64, and a PNG that round-trips to the same pixels. A
// silently wrong encoding here reads as a model that cannot see the page.
func TestDataURIRoundTrips(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(2, 1, color.RGBA{G: 128, B: 64, A: 255})

	uri, err := dataURI(img)
	if err != nil {
		t.Fatalf("dataURI: %v", err)
	}
	const prefix = "data:image/png;base64,"
	b64, ok := strings.CutPrefix(uri, prefix)
	if !ok {
		t.Fatalf("uri does not start with %q: %.40q", prefix, uri)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	back, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("payload is not a decodable PNG: %v", err)
	}
	if back.Bounds() != img.Bounds() {
		t.Fatalf("bounds %v, want %v", back.Bounds(), img.Bounds())
	}
	// Compared as resolved components, not as color.Color values: png.Decode returns an
	// NRGBA image for an opaque-alpha source, so the interface values differ by dynamic
	// type even when every channel matches.
	for _, p := range []image.Point{{X: 0, Y: 0}, {X: 2, Y: 1}, {X: 1, Y: 1}} {
		gr, gg, gb, ga := back.At(p.X, p.Y).RGBA()
		wr, wg, wb, wa := img.At(p.X, p.Y).RGBA()
		if gr != wr || gg != wg || gb != wb || ga != wa {
			t.Errorf("pixel %v = (%d,%d,%d,%d), want (%d,%d,%d,%d)", p, gr, gg, gb, ga, wr, wg, wb, wa)
		}
	}
}

// locate reports how to install llama-server rather than failing bare, because the
// alternative to a working instruction is the user finding a random binary.
func TestLocateMissingExplainsInstall(t *testing.T) {
	_, err := locate("this-executable-does-not-exist-on-any-machine")
	if err == nil {
		t.Fatal("expected an error for an -exe that does not exist")
	}
	if !strings.Contains(err.Error(), "this-executable-does-not-exist-on-any-machine") {
		t.Errorf("error %q does not name the path it tried", err)
	}
	if got := install(); !strings.Contains(got, "-addr") || !strings.Contains(got, "-exe") {
		t.Errorf("install() = %q; it must mention the -exe and -addr escape hatches", got)
	}
}

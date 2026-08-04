package ipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"io"
	"log/slog"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/3rg0n/pdf-spec/ocr"
)

// attach is ImageAttachment for a test that has already established the image is a
// sane size. The error it drops has its own test, TestImageAttachmentBounds. Not
// called "att": TestRequestJSONShape has a local of that name, and a function here
// would shadow it for every file in the package.
func attach(t *testing.T, id string, img *image.RGBA) Attachment {
	t.Helper()
	a, err := ImageAttachment(id, img)
	if err != nil {
		t.Fatalf("ImageAttachment(%q): %v", id, err)
	}
	return a
}

// pipe joins a client Conn and a server Conn over an in-memory connection, so the
// whole protocol is exercised without a socket and without a platform split.
func pipe(t *testing.T) (client, server *Conn) {
	t.Helper()
	c, s := net.Pipe()
	t.Cleanup(func() { _ = c.Close(); _ = s.Close() })
	return NewConn(c), NewConn(s)
}

// TestFraming is the codec, checked byte for byte rather than round-tripped.
//
// A round-trip test passes for any self-consistent framing, including a wrong one, so
// it would not catch the thing that actually matters here: that these bytes are the
// bytes inferd writes. The layout is asserted directly against the spec —
// [uvarint len][1 type byte][payload], no delimiter.
func TestFraming(t *testing.T) {
	var buf bytes.Buffer
	c := NewConn(nopCloser{&buf})

	payload := []byte(`{"a":1}`)
	if err := c.WriteFrame(FrameJSON, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got := buf.Bytes()
	n, read := binary.Uvarint(got)
	if read <= 0 {
		t.Fatalf("no uvarint length prefix in % x", got)
	}
	if n != uint64(len(payload)) {
		t.Errorf("length prefix = %d, want %d", n, len(payload))
	}
	if got[read] != FrameJSON {
		t.Errorf("type byte = 0x%02x, want 0x%02x", got[read], FrameJSON)
	}
	if !bytes.Equal(got[read+1:], payload) {
		t.Errorf("payload = %q, want %q", got[read+1:], payload)
	}
	// No trailing delimiter. A newline here would make the frame parse as NDJSON to a
	// lenient reader and desynchronize a strict one by exactly one byte per frame.
	if len(got) != read+1+len(payload) {
		t.Errorf("frame is %d bytes, want %d — something extra was written", len(got), read+1+len(payload))
	}
}

// TestWireVersionIsInferd pins the constants that define compatibility.
//
// These are not arbitrary numbers this package chose; they are inferd's, and the whole
// value of reimplementing the protocol rather than importing it depends on them
// staying equal. If upstream moves, this test is where that has to be a decision.
func TestWireVersionIsInferd(t *testing.T) {
	if Version != 1 {
		t.Errorf("Version = %d; inferd's generation wire is version 1", Version)
	}
	if MaxFrame != 64<<20 {
		t.Errorf("MaxFrame = %d, want inferd's 64 MiB cap", MaxFrame)
	}
	if FrameJSON != 0x01 || FrameBlob != 0x02 {
		t.Errorf("frame tags = 0x%02x/0x%02x, want 0x01/0x02", FrameJSON, FrameBlob)
	}
}

// TestRequestJSONShape pins the request's field names against inferd's RequestV2.
//
// Spelled out as a literal rather than compared to a struct, because the failure this
// guards is a rename that still compiles: `wire_version` becoming `wireVersion` breaks
// compatibility silently, and Go's own round-trip would not notice.
func TestRequestJSONShape(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	req := Request{
		ID:          "page-1",
		WireVersion: Version,
		Messages: []Message{{
			Role:    "user",
			Content: []Block{ImageBlock("page-1"), TextBlock(ocr.PromptPage)},
		}},
		Attachments: []Attachment{attach(t, "page-1", img)},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"wire_version", "id", "messages", "attachments"} {
		if _, ok := m[key]; !ok {
			t.Errorf("request JSON has no %q field; inferd's RequestV2 does", key)
		}
	}
	// The bytes must not be in the JSON. They ride in a blob frame, and a base64 blob
	// inline would be both incompatible and 33% larger on the hot path.
	if strings.Contains(string(b), "Bytes") || strings.Contains(string(b), "bytes") {
		t.Errorf("attachment bytes leaked into the request JSON: %s", b)
	}

	atts := m["attachments"].([]any)
	att := atts[0].(map[string]any)
	for _, key := range []string{"kind", "id", "width", "height"} {
		if _, ok := att[key]; !ok {
			t.Errorf("attachment JSON has no %q field", key)
		}
	}
	if att["kind"] != "image" {
		t.Errorf("attachment kind = %v, want \"image\"", att["kind"])
	}
}

// TestRequestRoundTrip walks a request with an attachment through the wire and back,
// checking that the blob is reunited with its metadata.
func TestRequestRoundTrip(t *testing.T) {
	client, server := pipe(t)

	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for x := 0; x < 3; x++ {
		for y := 0; y < 2; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 40), uint8(y * 80), 7, 255})
		}
	}
	sent := Request{
		ID:          "page-9",
		Messages:    []Message{{Role: "user", Content: []Block{ImageBlock("page-9"), TextBlock("go")}}},
		Attachments: []Attachment{attach(t, "page-9", img)},
	}

	errc := make(chan error, 1)
	go func() { errc <- client.WriteRequest(sent) }()

	got, err := server.ReadRequest()
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	if got.WireVersion != Version {
		t.Errorf("WireVersion = %d, want %d — WriteRequest must set it", got.WireVersion, Version)
	}
	if len(got.Attachments) != 1 || len(got.Attachments[0].Bytes) == 0 {
		t.Fatalf("attachment bytes did not arrive: %+v", got.Attachments)
	}

	back, err := DecodeImage(got.Attachments[0])
	if err != nil {
		t.Fatalf("DecodeImage: %v", err)
	}
	if back.Bounds() != img.Bounds() {
		t.Fatalf("bounds = %v, want %v", back.Bounds(), img.Bounds())
	}
	for x := 0; x < 3; x++ {
		for y := 0; y < 2; y++ {
			wr, wg, wb, _ := img.At(x, y).RGBA()
			gr, gg, gb, ga := back.At(x, y).RGBA()
			if gr != wr || gg != wg || gb != wb {
				t.Errorf("pixel (%d,%d) = %d,%d,%d want %d,%d,%d", x, y, gr, gg, gb, wr, wg, wb)
			}
			if ga != 0xffff {
				t.Errorf("pixel (%d,%d) alpha = %d, want opaque", x, y, ga)
			}
		}
	}
}

// TestImagePadding is the trap ADR 0005 documents at the other end of these pixels: an
// *image.RGBA's Stride may exceed 4*width, so a flat copy of Pix skews every row after
// the first. A sub-image of a larger raster is how that happens in practice.
func TestImagePadding(t *testing.T) {
	full := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for x := 0; x < 8; x++ {
		for y := 0; y < 4; y++ {
			full.Set(x, y, color.RGBA{uint8(x*30 + y), 0, 0, 255})
		}
	}
	sub := full.SubImage(image.Rect(2, 1, 6, 3)).(*image.RGBA)
	if sub.Stride == 4*sub.Bounds().Dx() {
		t.Fatal("test setup: the sub-image is not padded, so it proves nothing")
	}

	a := attach(t, "s", sub)
	if want := 4 * 2 * 3; len(a.Bytes) != want {
		t.Fatalf("packed %d bytes, want %d (width*height*3, padding removed)", len(a.Bytes), want)
	}
	back, err := DecodeImage(a)
	if err != nil {
		t.Fatalf("DecodeImage: %v", err)
	}
	for x := 0; x < 4; x++ {
		for y := 0; y < 2; y++ {
			wr, _, _, _ := sub.At(x+2, y+1).RGBA()
			gr, _, _, _ := back.At(x, y).RGBA()
			if gr != wr {
				t.Errorf("pixel (%d,%d) red = %d, want %d — rows are skewed", x, y, gr, wr)
			}
		}
	}
}

// TestFrameCap is the allocation bound: a declared length past the cap must cost an
// error, not a buffer.
func TestFrameCap(t *testing.T) {
	// A uvarint declaring 2^40 bytes, followed by nothing. A reader that allocated
	// before checking would try for a terabyte on 5 bytes of input.
	var buf bytes.Buffer
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], 1<<40)
	buf.Write(prefix[:n])
	buf.WriteByte(FrameJSON)

	c := NewConn(nopCloser{&buf})
	if _, _, err := c.ReadFrame(); err == nil {
		t.Fatal("a 2^40-byte frame length was accepted")
	} else if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error = %v, want it to name the cap", err)
	}

	// The write side too, so an oversized payload cannot be put on the wire and
	// become the peer's problem.
	if err := c.WriteFrame(FrameJSON, make([]byte, MaxFrame+1)); err == nil {
		t.Error("WriteFrame accepted a payload past the cap")
	}
}

// TestMalformedFrames is the no-panics gate for the wire, matching the one
// ocr/doctags has for model output. Every case is something a wrong-version peer, a
// killed process, or a port collision actually produces.
func TestMalformedFrames(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"length only", []byte{0x05}},
		{"length and type, no payload", []byte{0x05, FrameJSON}},
		{"truncated payload", []byte{0x05, FrameJSON, 'a', 'b'}},
		{"unknown frame type", []byte{0x01, 0x7f, 'x'}},
		{"zero length json", []byte{0x00, FrameJSON}},
		// An HTTP request, which is what arrives when something points a browser or a
		// health checker at the socket. 'G' is 0x47, a plausible uvarint, so this gets
		// read as a 71-byte frame of nonsense rather than being rejected outright —
		// the point is that it fails as an error and not as a panic.
		{"http request", []byte("GET / HTTP/1.1\r\n\r\n")},
		{"unterminated varint", bytes.Repeat([]byte{0x80}, 12)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewConn(nopCloser{&readOnly{bytes.NewReader(tc.raw)}})
			// Drained rather than read once: a malformed stream must terminate, and a
			// reader that returned a nil error forever on garbage would spin.
			for i := 0; i < 100; i++ {
				if _, _, err := c.ReadFrame(); err != nil {
					return
				}
			}
			t.Error("100 frames read from garbage without an error")
		})
	}
}

// TestBlobMismatch covers the two ways a blob can fail to match its descriptor. Both
// would otherwise hand the model one page's pixels under another page's dimensions,
// which produces a confident wrong answer rather than a failure.
func TestBlobMismatch(t *testing.T) {
	t.Run("length disagrees", func(t *testing.T) {
		client, server := pipe(t)
		go func() {
			_ = client.WriteJSON(Request{
				WireVersion: Version,
				ID:          "p",
				Messages:    []Message{{Role: "user", Content: []Block{ImageBlock("p")}}},
				Attachments: []Attachment{{Kind: "image", ID: "p", Width: 1, Height: 1}},
			})
			_ = client.WriteJSON(blobDescriptor{Type: "attachment_blob", AttachmentID: "p", Len: 99})
			_ = client.WriteFrame(FrameBlob, []byte{1, 2, 3})
		}()
		if _, err := server.ReadRequest(); err == nil {
			t.Error("a blob whose length disagreed with its descriptor was accepted")
		}
	})

	t.Run("unknown attachment id", func(t *testing.T) {
		client, server := pipe(t)
		go func() {
			_ = client.WriteJSON(Request{
				WireVersion: Version,
				ID:          "p",
				Messages:    []Message{{Role: "user", Content: []Block{ImageBlock("p")}}},
				Attachments: []Attachment{{Kind: "image", ID: "p", Width: 1, Height: 1}},
			})
			_ = client.WriteJSON(blobDescriptor{Type: "attachment_blob", AttachmentID: "other", Len: 3})
			_ = client.WriteFrame(FrameBlob, []byte{1, 2, 3})
		}()
		if _, err := server.ReadRequest(); err == nil {
			t.Error("a blob naming an undeclared attachment was accepted")
		}
	})
}

// TestWireVersionRejected checks that a mismatch is named rather than silently
// tolerated. A peer speaking a different protocol must fail on its first request with
// both numbers in the message, not misparse a frame.
func TestWireVersionRejected(t *testing.T) {
	client, server := pipe(t)
	go func() {
		_ = client.WriteJSON(Request{
			WireVersion: Version + 1,
			ID:          "p",
			Messages:    []Message{{Role: "user", Content: []Block{TextBlock("x")}}},
		})
	}()
	_, err := server.ReadRequest()
	if err == nil {
		t.Fatal("a mismatched wire version was accepted")
	}
	if !strings.Contains(err.Error(), "wire version") {
		t.Errorf("error = %v, want it to name the wire version", err)
	}
}

// TestDecodeImageValidation covers the dimension checks. A blob a byte short of
// width*height*3 either panics or reads the page diagonally, and neither says why.
func TestDecodeImageValidation(t *testing.T) {
	cases := []struct {
		name string
		a    Attachment
	}{
		{"wrong kind", Attachment{Kind: "audio", ID: "a", Width: 1, Height: 1, Bytes: []byte{1, 2, 3}}},
		{"zero width", Attachment{Kind: "image", ID: "a", Width: 0, Height: 1}},
		{"zero height", Attachment{Kind: "image", ID: "a", Width: 1, Height: 0}},
		{"short by one", Attachment{Kind: "image", ID: "a", Width: 2, Height: 2, Bytes: make([]byte, 11)}},
		{"long by one", Attachment{Kind: "image", ID: "a", Width: 2, Height: 2, Bytes: make([]byte, 13)}},
		// Dimensions whose product overflows int on 32-bit and is absurd on 64-bit.
		// Bounded before the multiply so the product cannot wrap into a small number
		// that then passes the length check.
		{"implausible dimensions", Attachment{Kind: "image", ID: "a", Width: 1 << 20, Height: 1 << 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeImage(tc.a); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestImageAttachmentBounds is DecodeImage's check applied at the sending end, and it
// is there so the two ends agree. A sender that could not be refused would put a frame
// on the wire that the receiver must refuse, which is a worse place to find out — and
// the bound is what makes the uint32 conversion of the dimensions unable to wrap.
func TestImageAttachmentBounds(t *testing.T) {
	// A raster wider than the axis bound, without allocating one: a sub-image of a huge
	// zero-size rectangle has the bounds and none of the pixels.
	wide := image.NewRGBA(image.Rect(0, 0, 1, 1))
	wide.Rect = image.Rect(0, 0, MaxImageAxis+1, 1)
	if _, err := ImageAttachment("wide", wide); err == nil {
		t.Errorf("accepted a %d-pixel width", MaxImageAxis+1)
	}

	tall := image.NewRGBA(image.Rect(0, 0, 1, 1))
	tall.Rect = image.Rect(0, 0, 1, MaxImageAxis+1)
	if _, err := ImageAttachment("tall", tall); err == nil {
		t.Errorf("accepted a %d-pixel height", MaxImageAxis+1)
	}

	if _, err := ImageAttachment("empty", image.NewRGBA(image.Rect(0, 0, 0, 0))); err == nil {
		t.Error("accepted a zero-size image")
	}

	// And the bound is not so tight that a real page fails it. 1200 DPI on US Letter is
	// 9504 pixels on the long edge, four times inside it.
	page := image.NewRGBA(image.Rect(0, 0, 9504, 7344))
	if _, err := ImageAttachment("page", page); err != nil {
		t.Errorf("rejected a US Letter page at 1200 DPI: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Client and server together
// ---------------------------------------------------------------------------

// fake is a Handler that returns canned DocTags, so the client, the server, and the
// framing are all exercised with no model anywhere.
type fake struct {
	tags   string
	err    error
	chunks int
	// seen records what the handler was given, which is how the test checks that the
	// prompt and the pixels survived the round trip.
	seenPrompt string
	seenBounds image.Rectangle
	seenMax    int
}

func (f *fake) Name() string { return "fake" }

func (f *fake) Generate(ctx context.Context, img *image.RGBA, prompt string, maxTokens int, emit func(string)) error {
	f.seenPrompt = prompt
	f.seenBounds = img.Bounds()
	f.seenMax = maxTokens
	// Emitted in pieces, because a single-chunk handler would not exercise the
	// streaming path the progress indicator depends on.
	n := f.chunks
	if n < 1 {
		n = 1
	}
	step := (len(f.tags) + n - 1) / n
	for i := 0; i < len(f.tags); i += step {
		end := min(i+step, len(f.tags))
		emit(f.tags[i:end])
	}
	return f.err
}

// serveOne wires a fake handler behind an Engine over an in-memory pipe.
func serveOne(t *testing.T, h Handler) *Engine {
	t.Helper()
	c, s := net.Pipe()
	t.Cleanup(func() { _ = c.Close() })
	go func() {
		_ = Serve(context.Background(), NewConn(s), h, slog.New(slog.DiscardHandler))
	}()
	return &Engine{conn: NewConn(c), addr: "pipe"}
}

func TestClientServer(t *testing.T) {
	const tags = "<doctag><text><loc_1><loc_2><loc_3><loc_4>hello</text></doctag>"
	h := &fake{tags: tags, chunks: 7}
	e := serveOne(t, h)

	img := image.NewRGBA(image.Rect(0, 0, 5, 4))
	var deltas int
	got, err := e.Recognize(context.Background(), img, ocr.Options{
		MaxTokens: 512,
		OnDelta:   func(string) { deltas++ },
	})
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	if got != tags {
		t.Errorf("got %q, want %q", got, tags)
	}
	if deltas < 2 {
		t.Errorf("OnDelta fired %d times; streaming is not reaching the caller", deltas)
	}
	if h.seenPrompt != ocr.PromptPage {
		t.Errorf("handler saw prompt %q, want the default %q", h.seenPrompt, ocr.PromptPage)
	}
	if h.seenBounds != img.Bounds() {
		t.Errorf("handler saw bounds %v, want %v", h.seenBounds, img.Bounds())
	}
	if h.seenMax != 512 {
		t.Errorf("handler saw max_tokens %d, want 512", h.seenMax)
	}
}

// TestMaxTokensClamp walks a bound through both conversions on the MaxTokens path —
// int to uint32 in the client, uint32 back to int in the server — for the values where
// a plain conversion misbehaves.
//
// The invariant is that MaxTokens is a *ceiling*, so every arrival must be a large
// number. The failure this guards is the opposite: a caller asking for the largest
// bound they can express and the handler receiving a small or negative one, which
// truncates every page in a run and looks like a model that stops early.
func TestMaxTokensClamp(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))

	// ask is uint64 rather than int so the table itself compiles on a 32-bit build,
	// where these values do not fit an int. Cases above math.MaxInt are skipped there
	// instead — a caller on that platform cannot express them, so there is nothing to
	// clamp; the clamp that matters on 32-bit is the server's, on the way back in.
	cases := []struct {
		name string
		ask  uint64
	}{
		{name: "an ordinary bound", ask: 8192},
		{name: "just below the wire's width", ask: math.MaxUint32 - 1},
		{name: "exactly the wire's width", ask: math.MaxUint32},
		{name: "wider than the wire", ask: math.MaxUint64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ask > uint64(math.MaxInt) {
				t.Skipf("%d is not expressible as an int on this build", tc.ask)
			}
			h := &fake{tags: "<doctag></doctag>"}
			e := serveOne(t, h)
			if _, err := e.Recognize(context.Background(), img, ocr.Options{MaxTokens: int(tc.ask)}); err != nil {
				t.Fatalf("Recognize: %v", err)
			}
			// Not compared to tc.ask directly: a bound above what the wire can carry is
			// expected to arrive reduced. What must hold is that it stayed a ceiling.
			if h.seenMax <= 0 {
				t.Fatalf("handler saw max_tokens %d for a request asking %d; a ceiling became no bound or a negative one", h.seenMax, tc.ask)
			}
			want := min(tc.ask, math.MaxUint32)
			if uint64(h.seenMax) != want {
				t.Errorf("handler saw max_tokens %d, want %d", h.seenMax, want)
			}
		})
	}

	// The server's own clamp, exercised by writing the frame directly rather than
	// through Recognize. Necessary because the client clamps first: on a 32-bit build a
	// caller cannot express MaxUint32 at all, so the cases above skip and the server's
	// conversion — the one that turns a peer's large bound negative — goes untested on
	// exactly the platform where it is the bug.
	t.Run("a peer's bound wider than the server's int", func(t *testing.T) {
		h := &fake{tags: "<doctag></doctag>"}
		c, s := net.Pipe()
		t.Cleanup(func() { _ = c.Close() })
		go func() { _ = Serve(context.Background(), NewConn(s), h, slog.New(slog.DiscardHandler)) }()
		client := NewConn(c)

		n := uint32(math.MaxUint32)
		if err := client.WriteRequest(Request{
			ID:          "big",
			Messages:    []Message{{Role: "user", Content: []Block{ImageBlock("big"), TextBlock("convert")}}},
			Attachments: []Attachment{attach(t, "big", img)},
			MaxTokens:   &n,
		}); err != nil {
			t.Fatalf("WriteRequest: %v", err)
		}
		for {
			_, payload, err := client.ReadFrame()
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			var r Response
			if err := json.Unmarshal(payload, &r); err != nil {
				t.Fatal(err)
			}
			if r.IsTerminal() {
				if r.Type != RespDone {
					t.Fatalf("resp = %+v, want done", r)
				}
				break
			}
		}
		if h.seenMax <= 0 {
			t.Errorf("handler saw max_tokens %d for a peer asking %d; a ceiling became no bound or a negative one", h.seenMax, n)
		}
	})

	// Zero and negative mean "no bound", and the field must be absent rather than sent
	// as a clamped huge number — the server distinguishes the two by the pointer.
	for _, ask := range []int{0, -1} {
		h := &fake{tags: "<doctag></doctag>"}
		e := serveOne(t, h)
		if _, err := e.Recognize(context.Background(), img, ocr.Options{MaxTokens: ask}); err != nil {
			t.Fatalf("Recognize(MaxTokens: %d): %v", ask, err)
		}
		if h.seenMax != 0 {
			t.Errorf("MaxTokens %d reached the handler as %d, want 0 (unbounded)", ask, h.seenMax)
		}
	}
}

// TestPartialOnError is the contract that makes a truncated page useful: text
// generated before a failure comes back alongside the error, because ocr/doctags
// parses partial DocTags and an empty string is strictly worse.
func TestPartialOnError(t *testing.T) {
	const partial = "<doctag><text>half a p"
	e := serveOne(t, &fake{tags: partial, chunks: 3, err: errors.New("model fell over")})

	got, err := e.Recognize(context.Background(), image.NewRGBA(image.Rect(0, 0, 2, 2)), ocr.Options{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if got != partial {
		t.Errorf("got %q, want the partial text %q returned with the error", got, partial)
	}
}

// TestServerRejectsBadRequest checks that an unusable request is answered and the
// connection kept alive. Dropping it would cost the client the model load that made
// the host worth connecting to.
func TestServerRejectsBadRequest(t *testing.T) {
	c, s := net.Pipe()
	t.Cleanup(func() { _ = c.Close() })
	go func() { _ = Serve(context.Background(), NewConn(s), &fake{tags: "ok"}, slog.New(slog.DiscardHandler)) }()
	client := NewConn(c)

	// No image block: there is nothing to read.
	if err := client.WriteRequest(Request{
		ID:       "bad",
		Messages: []Message{{Role: "user", Content: []Block{TextBlock("convert")}}},
	}); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}
	ftype, payload, err := client.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if ftype != FrameJSON {
		t.Fatalf("frame type 0x%02x", ftype)
	}
	var resp Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Type != RespError || resp.Code != ErrInvalidRequest {
		t.Fatalf("resp = %+v, want an invalid_request error", resp)
	}

	// The connection must still work. This is the assertion that fails if the server
	// closes on a bad request.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := client.WriteRequest(Request{
		ID:          "good",
		Messages:    []Message{{Role: "user", Content: []Block{ImageBlock("good"), TextBlock("convert")}}},
		Attachments: []Attachment{attach(t, "good", img)},
	}); err != nil {
		t.Fatalf("second WriteRequest: %v", err)
	}
	for {
		_, payload, err := client.ReadFrame()
		if err != nil {
			t.Fatalf("the server closed after rejecting a bad request: %v", err)
		}
		var r Response
		if err := json.Unmarshal(payload, &r); err != nil {
			t.Fatal(err)
		}
		if r.IsTerminal() {
			if r.Type != RespDone {
				t.Fatalf("second request failed: %+v", r)
			}
			return
		}
	}
}

// TestServerRejectsMultipleImages pins the narrowness of the request shape. A host
// that quietly used only the first of two images would produce a page of the wrong
// content with nothing indicating which.
func TestServerRejectsMultipleImages(t *testing.T) {
	c, s := net.Pipe()
	t.Cleanup(func() { _ = c.Close() })
	go func() { _ = Serve(context.Background(), NewConn(s), &fake{tags: "ok"}, slog.New(slog.DiscardHandler)) }()
	client := NewConn(c)

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	err := client.WriteRequest(Request{
		ID: "two",
		Messages: []Message{{Role: "user", Content: []Block{
			ImageBlock("a"), ImageBlock("b"), TextBlock("convert"),
		}}},
		Attachments: []Attachment{attach(t, "a", img), attach(t, "b", img)},
	})
	if err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}
	_, payload, err := client.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != RespError {
		t.Errorf("resp = %+v, want an error naming the second image", resp)
	}
}

// TestCancellation checks that a cancelled context ends a generation rather than
// leaving it to run to completion into a closed connection.
func TestCancellation(t *testing.T) {
	c, s := net.Pipe()
	t.Cleanup(func() { _ = c.Close() })
	// A handler that blocks until its context is cancelled, which is what a real
	// generation looks like from the outside.
	blocking := &blocker{started: make(chan struct{})}
	go func() { _ = Serve(context.Background(), NewConn(s), blocking, slog.New(slog.DiscardHandler)) }()
	e := &Engine{conn: NewConn(c), addr: "pipe"}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-blocking.started
		cancel()
	}()

	_, err := e.Recognize(ctx, image.NewRGBA(image.Rect(0, 0, 2, 2)), ocr.Options{})
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
}

type blocker struct {
	started chan struct{}
}

func (b *blocker) Name() string { return "blocker" }

func (b *blocker) Generate(ctx context.Context, _ *image.RGBA, _ string, _ int, emit func(string)) error {
	close(b.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("handler was never cancelled")
	}
}

// TestLocal covers the in-process path, which is what the CLI uses by default: the
// same Handler reached without a socket.
func TestLocal(t *testing.T) {
	const tags = "<doctag><text>x</text></doctag>"
	h := &fake{tags: tags, chunks: 4}
	l := NewLocal(h)

	var deltas int
	got, err := l.Recognize(context.Background(), image.NewRGBA(image.Rect(0, 0, 3, 3)), ocr.Options{
		Prompt:  ocr.PromptTable,
		OnDelta: func(string) { deltas++ },
	})
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	if got != tags {
		t.Errorf("got %q, want %q", got, tags)
	}
	if deltas < 2 {
		t.Errorf("OnDelta fired %d times", deltas)
	}
	if h.seenPrompt != ocr.PromptTable {
		t.Errorf("prompt = %q, want the caller's %q", h.seenPrompt, ocr.PromptTable)
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// The zero value must report a missing constructor rather than nil-panicking, so
	// the error names the real fault.
	var zero Local
	if _, err := zero.Recognize(context.Background(), image.NewRGBA(image.Rect(0, 0, 1, 1)), ocr.Options{}); err == nil {
		t.Error("the zero Local accepted a page")
	}
}

// TestLocalMatchesIPC is the equivalence the two paths are supposed to have: the same
// handler must produce the same text whether it is reached in-process or over the
// wire. If they diverge, one of them is not the protocol.
func TestLocalMatchesIPC(t *testing.T) {
	const tags = "<doctag><otsl><fcel>a<fcel>b<nl></otsl></doctag>"
	img := image.NewRGBA(image.Rect(0, 0, 6, 5))

	local, err := NewLocal(&fake{tags: tags, chunks: 5}).
		Recognize(context.Background(), img, ocr.Options{})
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	remote, err := serveOne(t, &fake{tags: tags, chunks: 5}).
		Recognize(context.Background(), img, ocr.Options{})
	if err != nil {
		t.Fatalf("ipc: %v", err)
	}
	if local != remote {
		t.Errorf("local = %q but ipc = %q; the two paths must agree", local, remote)
	}
}

// TestDefaultAddrIsInferd checks the socket path, since compatibility means nothing if
// the two halves look in different places.
func TestDefaultAddrIsInferd(t *testing.T) {
	addr := DefaultAddr()
	if addr == "" {
		t.Fatal("DefaultAddr is empty")
	}
	if !strings.Contains(addr, "inferd") {
		t.Errorf("DefaultAddr = %q; it must name inferd's socket so a running daemon is found with no configuration", addr)
	}
}

// readOnly makes a reader satisfy io.ReadWriter for the malformed-input cases, which
// never write. Writes fail rather than being discarded, so a test that started writing
// would say so instead of passing for the wrong reason.
type readOnly struct{ r io.Reader }

func (r *readOnly) Read(p []byte) (int, error) { return r.r.Read(p) }
func (r *readOnly) Write([]byte) (int, error)  { return 0, errors.New("read-only") }

type nopCloser struct{ rw io.ReadWriter }

func (n nopCloser) Read(p []byte) (int, error)  { return n.rw.Read(p) }
func (n nopCloser) Write(p []byte) (int, error) { return n.rw.Write(p) }
func (n nopCloser) Close() error                { return nil }

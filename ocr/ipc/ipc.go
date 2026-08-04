// Package ipc is the wire between a pdf-spec CLI and whatever holds the model.
//
// It exists so the parser and the model are separate processes. Loading a vision
// model costs seconds and gigabytes and is worth paying once for a thousand pages,
// while a PDF parse costs milliseconds and should not be entangled with it. Two
// processes over a local socket gets that, and it gets three more things: the CLI
// stays a small static binary with no GPU dependency, the model host can be replaced
// without recompiling anything, and a model that crashes on a malformed page takes
// down a subprocess rather than the run.
//
// # Byte-compatible with inferd
//
// The framing, the JSON shapes, and the socket paths are inferd's generation
// protocol v2 (its ADR 0015/0016/0021), reimplemented here rather than imported.
// That is deliberate and the reasons are specific: this package needs the *server*
// side, which inferd's Go client does not provide; pdf-spec stays dependency-free at
// its own boundary, as objects and render do; and a shared module would couple this
// repo's release cadence to a daemon's. The cost is that a protocol change upstream
// is a change here too, which is what Version and the conformance test in
// ipc_test.go exist to catch.
//
// What compatibility buys is concrete: point the CLI at a running inferd and it
// works, with no adapter in between, because the daemon cannot tell the difference.
// Until inferd carries granite-docling, ocr/docd serves the same wire itself.
//
// # The framing, and why it is not NDJSON
//
// Each frame is a uvarint payload length, one type byte, then exactly that many
// bytes:
//
//	[uvarint len][1 byte type][payload]
//
// Type 0x01 is JSON, 0x02 is a raw binary blob. There is no delimiter, so a payload
// may contain any byte — which is the point. A page of raw RGB at 200 DPI is about
// 12 MB of arbitrary bytes; NDJSON would need base64, costing a third more bytes and
// a full copy in each direction on the hot path. inferd's embeddings and admin
// surfaces are NDJSON because their payloads are small and text; generation is
// framed binary because its payloads are neither.
//
// The length is read before anything is allocated, and a length past MaxFrame is an
// error rather than a large allocation — F-1 in inferd's threat model, and the same
// reasoning as the tag-depth limit in ADR 0001. A peer that sends one is not resynced
// with: the stream position is no longer known, so the only safe move is to close.
//
// # Images arrive decoded
//
// An image attachment carries interleaved RGB, width*height*3 bytes, no alpha, in a
// blob frame. No PNG, no JPEG. inferd's ADR 0016 puts the decode on the consumer
// because the daemon links no image codec, and the same posture is right here for a
// different reason: the pages come straight from the rasterizer as pixels, so
// encoding them to PNG for transport and decoding on the far side would be work
// performed purely to undo itself.
package ipc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
	"sync"
)

// Version is the wire version carried in every request and checked by the server.
//
// In-band rather than negotiated at connect. A version mismatch is then a named
// error on the first request instead of a hang or a misparse, and the check costs one
// integer comparison. 1 is inferd's current generation wire version; this constant
// moving means this package is no longer compatible with a daemon that speaks 1, so
// it moves only alongside upstream.
const Version uint32 = 1

// MaxFrame bounds one frame's payload at 64 MiB, matching inferd.
//
// Sized for the payload that actually reaches it: a raster page. A 300 DPI US Letter
// page is 2550x3300, which as RGB is 25 MB, so the cap is roughly 2.5x the largest
// realistic attachment and far below anything that threatens a process. It is checked
// against the declared length before a buffer is allocated, which is the property
// that makes it a bound rather than a check.
const MaxFrame = 64 << 20

// Frame type tags.
const (
	FrameJSON byte = 0x01
	FrameBlob byte = 0x02
)

// ---------------------------------------------------------------------------
// Addresses
// ---------------------------------------------------------------------------

// DefaultAddr returns the platform default socket path, resolved exactly as inferd
// resolves it, so a CLI with no configuration finds a running daemon.
//
// The Unix chain is XDG_RUNTIME_DIR, then ~/.inferd/run, then /tmp — a per-user
// runtime directory first because a socket in a world-writable /tmp is a
// pre-creation target for another user, and XDG_RUNTIME_DIR is mode 0700 and owned by
// the session. Windows has one answer, a named pipe, since it has no filesystem
// socket to place.
func DefaultAddr() string {
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\inferd`
	case "darwin":
		return tempDir() + "/inferd/inferd.sock"
	default:
		if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
			return xdg + "/inferd/inferd.sock"
		}
		if home := os.Getenv("HOME"); home != "" {
			return home + "/.inferd/run/inferd.sock"
		}
		return "/tmp/inferd/inferd.sock"
	}
}

func tempDir() string {
	if d := os.Getenv("TMPDIR"); d != "" {
		return d
	}
	return "/tmp"
}

// ---------------------------------------------------------------------------
// Wire messages
// ---------------------------------------------------------------------------

// Request is one generation request. Field names and JSON tags match inferd's
// RequestV2 byte for byte; a field this package does not use is still spelled the
// same, because a rename would make the two incompatible for no gain.
type Request struct {
	WireVersion uint32       `json:"wire_version"`
	ID          string       `json:"id,omitempty"`
	Messages    []Message    `json:"messages"`
	Attachments []Attachment `json:"attachments,omitempty"`
	MaxTokens   *uint32      `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

// Message is one conversation turn.
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// Block is one piece of a message's content: text, or a reference to an attachment.
type Block struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
}

// TextBlock builds a text content block.
func TextBlock(text string) Block { return Block{Type: "text", Text: text} }

// ImageBlock references an image attachment by id.
//
// The image is a reference rather than inline content because the bytes travel in a
// separate blob frame. That indirection is what keeps the request JSON small enough
// to log and to read in a test.
func ImageBlock(id string) Block { return Block{Type: "image", AttachmentID: id} }

// Attachment is one binary payload's metadata. The bytes themselves are `json:"-"`
// and ride in a blob frame keyed by ID, so this struct is what a reader sees when it
// decodes the request JSON and the blob has not arrived yet.
type Attachment struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Width  uint32 `json:"width,omitempty"`
	Height uint32 `json:"height,omitempty"`
	Bytes  []byte `json:"-"`
}

// blobDescriptor is the JSON control frame that precedes each blob.
//
// It carries the length the blob frame will declare for itself, which looks
// redundant and is not: the descriptor names *which* attachment the next frame
// belongs to, and a reader that finds the two lengths disagreeing knows the stream is
// corrupt before it uses the bytes.
type blobDescriptor struct {
	Type         string `json:"type"`
	AttachmentID string `json:"attachment_id"`
	Len          uint64 `json:"len"`
}

// Response is one frame off the response stream: a text delta, a terminal done, or a
// terminal error.
type Response struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Block      *OutBlock `json:"block,omitempty"`
	Usage      *Usage    `json:"usage,omitempty"`
	StopReason string    `json:"stop_reason,omitempty"`
	Backend    string    `json:"backend,omitempty"`
	Code       string    `json:"code,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// OutBlock is a streaming output delta.
type OutBlock struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}

// Usage is the token report on a done frame.
type Usage struct {
	InputTokens  uint32 `json:"input_tokens"`
	OutputTokens uint32 `json:"output_tokens"`
}

// Response types and stop reasons.
const (
	RespFrame = "frame"
	RespDone  = "done"
	RespError = "error"

	StopEndTurn   = "end_turn"
	StopMaxTokens = "max_tokens"
	StopError     = "error"
)

// Error codes, matching inferd's ErrorCodeV2. Enumerated rather than free-text
// because a caller has to distinguish "retry later" from "this request is wrong" and
// parsing a message string for that is how brittle retry loops get written.
const (
	ErrInvalidRequest        = "invalid_request"
	ErrBackendUnavailable    = "backend_unavailable"
	ErrFrameTooLarge         = "frame_too_large"
	ErrAttachmentUnsupported = "attachment_unsupported"
	ErrWireVersion           = "wire_version_unsupported"
	ErrInternal              = "internal"
)

// IsTerminal reports whether this frame ends a request's stream.
func (r Response) IsTerminal() bool { return r.Type == RespDone || r.Type == RespError }

// ---------------------------------------------------------------------------
// Framing
// ---------------------------------------------------------------------------

// Conn is a framed connection. Both directions of the protocol use it, so the codec
// exists once and a client and a server cannot disagree about it.
//
// Writes are mutex-guarded because a frame is three separate writes — length, type,
// payload — and two goroutines interleaving those produces a stream that is not
// merely wrong but unrecoverable. Reads are not guarded: one connection carries one
// in-flight request, so there is one reader by construction.
type Conn struct {
	rw io.ReadWriteCloser
	r  *bufio.Reader
	w  *bufio.Writer

	mu     sync.Mutex
	closed bool
}

// NewConn wraps a transport. The buffer sizes are ordinary; the writer is flushed at
// the end of every frame, so a peer never waits on bytes sitting in a buffer.
func NewConn(rw io.ReadWriteCloser) *Conn {
	return &Conn{
		rw: rw,
		r:  bufio.NewReaderSize(rw, 64*1024),
		w:  bufio.NewWriterSize(rw, 64*1024),
	}
}

// Close closes the transport. Safe to call more than once.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.rw.Close()
}

// WriteFrame writes one length-prefixed, type-tagged frame and flushes.
func (c *Conn) WriteFrame(ftype byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("ipc: frame of %d bytes exceeds the %d byte cap", len(payload), MaxFrame)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("ipc: connection closed")
	}

	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(len(payload)))
	if _, err := c.w.Write(prefix[:n]); err != nil {
		return fmt.Errorf("ipc: write frame length: %w", err)
	}
	if err := c.w.WriteByte(ftype); err != nil {
		return fmt.Errorf("ipc: write frame type: %w", err)
	}
	if _, err := c.w.Write(payload); err != nil {
		return fmt.Errorf("ipc: write frame payload: %w", err)
	}
	// Flushed per frame rather than per batch. A response delta held in a buffer is
	// a stalled progress indicator, and the syscall is not the cost here.
	if err := c.w.Flush(); err != nil {
		return fmt.Errorf("ipc: flush frame: %w", err)
	}
	return nil
}

// WriteJSON marshals v and writes it as a JSON frame.
func (c *Conn) WriteJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("ipc: marshal: %w", err)
	}
	return c.WriteFrame(FrameJSON, b)
}

// ReadFrame reads one frame.
//
// io.EOF before the first length byte means the peer closed cleanly between frames,
// which is normal and is how a server learns a client is done. An EOF after that is
// io.ErrUnexpectedEOF from ReadFull — a truncated frame, which is not normal. The two
// are distinguishable by design, because "the client went away" and "the client was
// killed mid-page" call for different handling.
func (c *Conn) ReadFrame() (byte, []byte, error) {
	length, err := binary.ReadUvarint(c.r)
	if err != nil {
		return 0, nil, err
	}
	// Checked before the allocation below, which is the whole point: a peer
	// declaring 2^60 bytes must cost nothing but an error.
	if length > MaxFrame {
		return 0, nil, fmt.Errorf("ipc: declared frame length %d exceeds the %d byte cap", length, MaxFrame)
	}
	ftype, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if ftype != FrameJSON && ftype != FrameBlob {
		return 0, nil, fmt.Errorf("ipc: unknown frame type 0x%02x", ftype)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return 0, nil, err
	}
	return ftype, payload, nil
}

// ---------------------------------------------------------------------------
// Requests with attachments
// ---------------------------------------------------------------------------

// WriteRequest writes a request and the blob frames its attachments need.
//
// Order is fixed and load-bearing: the request JSON first, then per attachment a
// descriptor frame and its blob, in the order the attachments appear in the request.
// A reader can therefore allocate once it has the JSON and knows how many blobs are
// coming, without buffering the whole request.
func (c *Conn) WriteRequest(req Request) error {
	req.WireVersion = Version
	if err := c.WriteJSON(req); err != nil {
		return err
	}
	for _, a := range req.Attachments {
		if len(a.Bytes) == 0 {
			continue
		}
		desc := blobDescriptor{Type: "attachment_blob", AttachmentID: a.ID, Len: uint64(len(a.Bytes))}
		if err := c.WriteJSON(desc); err != nil {
			return err
		}
		if err := c.WriteFrame(FrameBlob, a.Bytes); err != nil {
			return err
		}
	}
	return nil
}

// ReadRequest reads a request and fills in its attachments' bytes from the blob
// frames that follow.
//
// Rejects a wire-version mismatch here rather than deeper in, so the error names both
// versions while the numbers are still in scope. Also rejects a blob whose declared
// length disagrees with its frame: the two are written from the same slice, so a
// disagreement means the stream is not what it claims and the bytes must not be used
// as pixels.
func (c *Conn) ReadRequest() (Request, error) {
	ftype, payload, err := c.ReadFrame()
	if err != nil {
		return Request{}, err
	}
	if ftype != FrameJSON {
		return Request{}, fmt.Errorf("ipc: expected a JSON request frame, got type 0x%02x", ftype)
	}
	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return Request{}, fmt.Errorf("ipc: decode request: %w", err)
	}
	if req.WireVersion != Version {
		return req, fmt.Errorf("ipc: wire version %d unsupported, this build speaks %d", req.WireVersion, Version)
	}

	for i := range req.Attachments {
		ftype, payload, err := c.ReadFrame()
		if err != nil {
			return req, fmt.Errorf("ipc: reading attachment %d of %d: %w", i+1, len(req.Attachments), err)
		}
		if ftype != FrameJSON {
			return req, fmt.Errorf("ipc: expected a blob descriptor, got type 0x%02x", ftype)
		}
		var desc blobDescriptor
		if err := json.Unmarshal(payload, &desc); err != nil {
			return req, fmt.Errorf("ipc: decode blob descriptor: %w", err)
		}

		ftype, blob, err := c.ReadFrame()
		if err != nil {
			return req, fmt.Errorf("ipc: reading attachment %d blob: %w", i+1, err)
		}
		if ftype != FrameBlob {
			return req, fmt.Errorf("ipc: expected a blob frame, got type 0x%02x", ftype)
		}
		if uint64(len(blob)) != desc.Len {
			return req, fmt.Errorf("ipc: attachment %q: descriptor says %d bytes, frame has %d", desc.AttachmentID, desc.Len, len(blob))
		}

		// Matched by id, not by position. Nothing in the protocol promises the
		// descriptors arrive in the attachments' order, and a mismatched pairing here
		// would hand the model one page's pixels under another page's dimensions.
		idx := -1
		for j := range req.Attachments {
			if req.Attachments[j].ID == desc.AttachmentID {
				idx = j
				break
			}
		}
		if idx < 0 {
			return req, fmt.Errorf("ipc: blob names attachment %q, which the request does not declare", desc.AttachmentID)
		}
		req.Attachments[idx].Bytes = blob
	}
	return req, nil
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

// MaxImageAxis bounds an attachment's width and height, on both sides of the wire.
//
// 65,536 is four times the longest edge of a US Letter page at 1200 DPI, so no page
// anything here rasterizes comes near it. The bound exists to be checked *before*
// width*height*3 is multiplied: without it a hostile pair of dimensions can overflow
// the product into a small number that then agrees with a short payload, and the
// receiver reads the page diagonally instead of failing.
const MaxImageAxis = 1 << 16

// ImageAttachment builds an image attachment from an RGBA raster.
//
// Drops the alpha channel and the row padding: the wire format is exactly
// width*height*3 interleaved RGB octets. Both drops are correct rather than lossy —
// a rasterized page is opaque, and an *image.RGBA's Stride may exceed 4*width, so
// copying Pix flat would skew every row after the first. This is the same padding
// trap ADR 0005 documents on the pdfium side, at the other end of the same pixels.
//
// Returns an error on the dimensions DecodeImage would reject, so the check is the
// same at both ends. A sender that could not be refused would put a frame on the wire
// that the receiver must refuse, which is a worse place to find out.
func ImageAttachment(id string, img *image.RGBA) (Attachment, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return Attachment{}, fmt.Errorf("ipc: attachment %q: image is %dx%d", id, w, h)
	}
	if w > MaxImageAxis || h > MaxImageAxis {
		return Attachment{}, fmt.Errorf("ipc: attachment %q: %dx%d exceeds the %d-pixel axis bound", id, w, h, MaxImageAxis)
	}

	rgb := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		row := img.Pix[(y+b.Min.Y-img.Rect.Min.Y)*img.Stride:]
		out := rgb[y*w*3:]
		for x := 0; x < w; x++ {
			i := (x + b.Min.X - img.Rect.Min.X) * 4
			out[x*3+0] = row[i+0]
			out[x*3+1] = row[i+1]
			out[x*3+2] = row[i+2]
		}
	}
	// The conversion cannot wrap: both axes are bounded above by MaxImageAxis and below
	// by 1 a few lines up, which is the reason that check is here rather than only in
	// DecodeImage.
	return Attachment{
		Kind: "image", ID: id,
		Width: uint32(w), Height: uint32(h), // #nosec G115 -- bounded by MaxImageAxis above
		Bytes: rgb,
	}, nil
}

// DecodeImage turns an image attachment back into an RGBA raster, opaque.
//
// The server side of ImageAttachment. Validates that the byte count matches the
// declared dimensions, because everything downstream indexes by row: a blob one byte
// short of width*height*3 either panics or reads the page diagonally, and neither
// says what went wrong.
func DecodeImage(a Attachment) (*image.RGBA, error) {
	if a.Kind != "image" {
		return nil, fmt.Errorf("ipc: attachment %q is kind %q, not an image", a.ID, a.Kind)
	}
	w, h := int(a.Width), int(a.Height)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("ipc: attachment %q has dimensions %dx%d", a.ID, w, h)
	}
	// Bounded before multiplying, so a 2^32-1 by 2^32-1 attachment cannot overflow
	// the product into a small number that then passes the length check below.
	if w > MaxImageAxis || h > MaxImageAxis {
		return nil, fmt.Errorf("ipc: attachment %q dimensions %dx%d are implausible", a.ID, w, h)
	}
	if want := w * h * 3; len(a.Bytes) != want {
		return nil, fmt.Errorf("ipc: attachment %q declares %dx%d (%d bytes of RGB) but carries %d", a.ID, w, h, want, len(a.Bytes))
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := a.Bytes[y*w*3:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			dst[x*4+0] = src[x*3+0]
			dst[x*4+1] = src[x*3+1]
			dst[x*4+2] = src[x*3+2]
			dst[x*4+3] = 0xff
		}
	}
	return img, nil
}

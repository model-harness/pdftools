package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"math"
	"strings"
)

// Handler generates a response for one request.
//
// It receives the page already decoded to an RGBA raster and the prompt already
// extracted from the message blocks, because every handler would otherwise do that
// unpacking itself and a handler that got it subtly wrong would fail as a model
// quality problem rather than as a protocol bug.
//
// emit is called with each chunk of generated text as it becomes available. Streaming
// is not optional: a page takes tens of seconds, and a client that receives nothing
// until the end cannot distinguish slow from hung. A handler returning an error after
// emitting text still delivers that text — the client keeps partial output by design.
type Handler interface {
	Generate(ctx context.Context, img *image.RGBA, prompt string, maxTokens int, emit func(string)) error
	// Name identifies the backend on the done frame. Diagnostic only, and worth
	// having: "which model produced this" is the first question about a bad page.
	Name() string
}

// Serve handles requests on one connection until the peer closes it or the context is
// cancelled.
//
// One request at a time, serially. That matches the protocol — one in-flight request
// per connection — and it matches the resource: a model host has one model, and
// interleaving two pages through it would only add queueing without adding
// throughput. Concurrency is several connections.
//
// A protocol violation closes the connection after an error frame. A framing error
// cannot be resynced from, because the stream position is no longer known and the
// next bytes read would be the middle of someone's payload.
func Serve(ctx context.Context, conn *Conn, h Handler, log *slog.Logger) error {
	defer conn.Close()

	// Cancellation closes the connection, which is what unblocks the read below.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	for {
		req, err := conn.ReadRequest()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// EOF before a request began is the client leaving, which is how every
			// well-behaved session ends. Not an error.
			if errors.Is(err, io.EOF) {
				return nil
			}
			// A version mismatch gets its own code, because it is the one failure with
			// an action attached: the peer needs a different build, and telling it
			// "invalid request" would send someone looking at their own JSON.
			code := ErrInvalidRequest
			if strings.Contains(err.Error(), "wire version") {
				code = ErrWireVersion
			}
			_ = conn.WriteJSON(Response{ID: req.ID, Type: RespError, Code: code, Message: err.Error()})
			return err
		}

		if err := handle(ctx, conn, req, h, log); err != nil {
			// Already reported to the client as an error frame. Returned so the caller
			// stops using a connection whose stream may be mid-frame.
			return err
		}
	}
}

// handle runs one request and writes its response frames.
func handle(ctx context.Context, conn *Conn, req Request, h Handler, log *slog.Logger) error {
	fail := func(code, msg string) error {
		if err := conn.WriteJSON(Response{ID: req.ID, Type: RespError, Code: code, Message: msg}); err != nil {
			return err
		}
		return errors.New(msg)
	}

	img, prompt, err := unpack(req)
	if err != nil {
		// An unusable request is answered and the connection kept: the client's next
		// page may be fine, and dropping the connection would cost it the model-load
		// that made this host worth having.
		if werr := conn.WriteJSON(Response{ID: req.ID, Type: RespError, Code: ErrInvalidRequest, Message: err.Error()}); werr != nil {
			return werr
		}
		log.Warn("rejected request", "id", req.ID, "err", err)
		return nil
	}

	// Clamped on the way in, the mirror of the client's clamp on the way out. int is
	// 32 bits on a 32-bit build, so a peer's bound above 2^31 arrives negative — and a
	// negative maxTokens means "no bound" to every handler here, which is the opposite
	// of a peer asking for a very large one. Saturating keeps it a ceiling.
	var maxTokens int
	if req.MaxTokens != nil {
		// Widened to uint64 for the comparison: math.MaxInt as an untyped constant
		// against a uint32 operand does not compile on a 64-bit build, because the
		// constant does not fit the operand's type.
		if uint64(*req.MaxTokens) > uint64(math.MaxInt) {
			maxTokens = math.MaxInt
		} else {
			maxTokens = int(*req.MaxTokens)
		}
	}

	// Cancelled as soon as a delta fails to write, which is how the server learns its
	// client went away — the protocol has no cancel frame in the other direction
	// either. Without this, a client that disconnects mid-page leaves the model
	// generating to nobody: on a single-slot host that is the GPU held for the rest of
	// the page, and the next client waits behind output that will be discarded.
	genCtx, stop := context.WithCancel(ctx)
	defer stop()

	// Written by the emit closure and read after Generate returns. Safe without a lock
	// only because Generate is documented to call emit synchronously from the calling
	// goroutine; a handler that spawned its own would need one, which is why the
	// interface says so rather than leaving it to be discovered.
	var out int
	var writeErr error
	emit := func(delta string) {
		if delta == "" || writeErr != nil {
			return
		}
		out++
		if err := conn.WriteJSON(Response{
			ID:    req.ID,
			Type:  RespFrame,
			Block: &OutBlock{Type: "text", Delta: delta},
		}); err != nil {
			writeErr = err
			stop()
		}
	}

	genErr := h.Generate(genCtx, img, prompt, maxTokens, emit)
	if writeErr != nil {
		// The client went away mid-generation. Nothing useful to send it.
		return writeErr
	}
	if genErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Error("generation failed", "id", req.ID, "err", genErr)
		return fail(ErrInternal, genErr.Error())
	}

	// Saturating rather than wrapping. out counts deltas, so reaching 2^32 needs a
	// generation four billion chunks long — but a usage number that wrapped to a small
	// one would misreport the runaway page this is most useful for spotting.
	//
	// The upper comparison is in uint64 because math.MaxUint32 does not fit an int on a
	// 32-bit build, where `out < math.MaxUint32` fails to compile rather than comparing.
	tokens := uint32(math.MaxUint32)
	if out >= 0 && uint64(out) < math.MaxUint32 {
		tokens = uint32(out) // #nosec G115 -- guarded above
	}
	return conn.WriteJSON(Response{
		ID:         req.ID,
		Type:       RespDone,
		StopReason: StopEndTurn,
		Backend:    h.Name(),
		Usage:      &Usage{OutputTokens: tokens},
	})
}

// unpack pulls the one image and the prompt out of a request's messages.
//
// Deliberately narrow: this server accepts exactly one image and one text block in a
// single user turn, because that is what a page-OCR request is. A conversation, a
// second image, or a tool definition is rejected by name rather than partially
// honoured — a host that quietly used only the first of two images would produce a
// page of the wrong content with no indication which.
func unpack(req Request) (*image.RGBA, string, error) {
	if len(req.Messages) != 1 {
		return nil, "", fmt.Errorf("expected exactly 1 message, got %d: this host serves single-page OCR, not conversation", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Role != "user" {
		return nil, "", fmt.Errorf("message role %q, want \"user\"", msg.Role)
	}

	var prompt string
	var attachID string
	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			if prompt != "" {
				return nil, "", errors.New("more than one text block; the prompt must be a single block")
			}
			prompt = b.Text
		case "image":
			if attachID != "" {
				return nil, "", errors.New("more than one image; this host converts one page per request")
			}
			attachID = b.AttachmentID
		default:
			return nil, "", fmt.Errorf("unsupported content block %q", b.Type)
		}
	}
	if attachID == "" {
		return nil, "", errors.New("no image block; there is nothing to read")
	}
	if prompt == "" {
		return nil, "", errors.New("no text block; the prompt is what selects the task")
	}

	for _, a := range req.Attachments {
		if a.ID != attachID {
			continue
		}
		img, err := DecodeImage(a)
		if err != nil {
			return nil, "", err
		}
		return img, prompt, nil
	}
	return nil, "", fmt.Errorf("image block references attachment %q, which the request does not carry", attachID)
}

// decodeInto is json.Unmarshal with the error wrapped where it is read. Kept here
// rather than inlined so the client and server report a malformed frame identically.
func decodeInto(payload []byte, v any) error {
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("malformed JSON frame: %w", err)
	}
	return nil
}

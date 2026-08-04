package ipc

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/3rg0n/pdf-spec/ocr"
)

// Engine is an ocr.Engine that sends pages over this wire.
//
// It holds one connection and serves one request at a time, matching both the
// protocol — one in-flight request per connection, no multiplexing — and
// ocr.Engine's contract. A caller rendering pages in parallel opens one Engine per
// worker, the way the render verb opens one Rasterizer per worker.
type Engine struct {
	conn *Conn
	seq  atomic.Uint64
	// addr is kept only for error messages. A failure to reach a socket is the most
	// common failure this package has, and one that does not say which path it tried
	// makes the user guess between a daemon that is not running, a stale path, and a
	// permissions problem.
	addr string
}

// Dial connects to a model host at addr, or at DefaultAddr when addr is empty.
//
// A successful connect is the readiness signal, not a heartbeat: the server binds its
// socket only once its model is loaded, so connect-refused means "not ready yet" and
// there is nothing further to ask. That is inferd's posture (its threat model F-13)
// and this package matches it, which is why there is no Ping.
func Dial(ctx context.Context, addr string) (*Engine, error) {
	if addr == "" {
		addr = DefaultAddr()
	}
	rw, err := dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &Engine{conn: NewConn(rw), addr: addr}, nil
}

// Close closes the connection.
func (e *Engine) Close() error { return e.conn.Close() }

// Recognize sends one page and collects the DocTags the model streams back.
//
// Returns whatever was generated alongside any error, rather than discarding it.
// That is not politeness: the dominant failure on a dense page is a generation that
// runs into its token bound mid-table, and ocr/doctags parses a truncated document by
// design — so a partial page is worth strictly more than an empty one, and the
// caller decides.
func (e *Engine) Recognize(ctx context.Context, img *image.RGBA, opt ocr.Options) (string, error) {
	prompt := opt.Prompt
	if prompt == "" {
		prompt = ocr.PromptPage
	}

	id := "page-" + strconv.FormatUint(e.seq.Add(1), 10)
	att, err := ImageAttachment(id, img)
	if err != nil {
		return "", err
	}
	req := Request{
		ID: id,
		Messages: []Message{{
			Role: "user",
			// The image precedes the instruction. granite-docling is instruction-tuned
			// with the page first, and the order is part of the prompt: a model asked
			// to convert a page it has not been shown yet answers differently.
			Content: []Block{ImageBlock(id), TextBlock(prompt)},
		}},
		Attachments: []Attachment{att},
	}
	if opt.MaxTokens > 0 {
		// Clamped rather than converted. The field is uint32 because inferd's is, and
		// ocr.Options.MaxTokens is an int a caller sets — so on a 64-bit build a bound
		// above 2^32 wraps to a small number, which is the opposite of what the caller
		// asked for and silently truncates every page. MaxTokens is a ceiling, so
		// clamping to the largest expressible one honours the intent exactly.
		//
		// Compared in uint64, not against the untyped constant: math.MaxUint32 does not
		// fit an int on a 32-bit build, so `opt.MaxTokens < math.MaxUint32` is a compile
		// error there rather than a comparison. The conversion is safe because the outer
		// branch has already established MaxTokens is positive.
		n := uint32(math.MaxUint32)
		if u := uint64(opt.MaxTokens); u < math.MaxUint32 {
			n = uint32(u) // #nosec G115 -- guarded above, and MaxTokens > 0
		}
		req.MaxTokens = &n
	}
	// Temperature 0. OCR is transcription, and sampling variation on a transcription
	// task is not creativity but a different reading of the same pixels — two runs
	// over one document should not disagree about what it says.
	zero := 0.0
	req.Temperature = &zero

	if err := e.conn.WriteRequest(req); err != nil {
		return "", fmt.Errorf("ocr/ipc: %s: %w", e.addr, err)
	}

	// Cancellation closes the connection, which is the only mechanism available: the
	// protocol has no cancel frame, and a half-generated page left in the server's
	// queue would otherwise be delivered to whatever request came next. Closing means
	// the read below fails and ctx.Err is reported instead.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = e.conn.Close()
		case <-done:
		}
	}()

	var sb strings.Builder
	for {
		ftype, payload, err := e.conn.ReadFrame()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return sb.String(), fmt.Errorf("ocr/ipc: %w", ctxErr)
			}
			if errors.Is(err, io.EOF) {
				return sb.String(), fmt.Errorf("ocr/ipc: %s closed the connection before finishing the page", e.addr)
			}
			return sb.String(), fmt.Errorf("ocr/ipc: %s: %w", e.addr, err)
		}
		if ftype != FrameJSON {
			return sb.String(), fmt.Errorf("ocr/ipc: %s sent a binary frame on the response stream", e.addr)
		}

		var resp Response
		if err := decodeInto(payload, &resp); err != nil {
			return sb.String(), fmt.Errorf("ocr/ipc: decode response: %w", err)
		}
		switch resp.Type {
		case RespFrame:
			// A thinking or tool_use block is not this page's text. Skipped rather than
			// concatenated, because a reasoning trace appended to the DocTags would
			// parse as the document's own content.
			if resp.Block != nil && resp.Block.Type == "text" && resp.Block.Delta != "" {
				sb.WriteString(resp.Block.Delta)
				if opt.OnDelta != nil {
					opt.OnDelta(resp.Block.Delta)
				}
			}
		case RespDone:
			// A generation stopped by its token bound is reported, with the text. It is
			// not an error — the page is usable and often complete enough — but a run
			// where many pages hit the bound is a misconfiguration, and silence here is
			// what makes that look like a model quality problem instead.
			if resp.StopReason == StopMaxTokens {
				return sb.String(), fmt.Errorf("ocr/ipc: generation hit the token limit; the page is truncated")
			}
			return sb.String(), nil
		case RespError:
			return sb.String(), fmt.Errorf("ocr/ipc: %s: %s: %s", e.addr, resp.Code, resp.Message)
		default:
			// An unknown frame type from a newer server is ignored rather than fatal,
			// which is the forward-compatibility this wire's in-band version is for:
			// a daemon that gains a frame kind should not break an older client that
			// does not need it.
		}
	}
}

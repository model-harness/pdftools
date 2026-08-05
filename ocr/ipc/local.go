package ipc

import (
	"context"
	"errors"
	"image"
	"strings"
	"sync"

	"github.com/model-harness/pdftools/ocr"
)

// Local adapts a Handler to ocr.Engine without a socket.
//
// The reason this exists is that the common case is one CLI invocation over one
// document, and there the process boundary buys nothing: the model is loaded and
// discarded by the same run either way, so a socket would add serialization,
// base64-free framing, and a platform-specific listener to move pixels between two
// halves of the same process. Local skips all of it.
//
// The IPC path earns its keep in the other case — a warm host serving many
// invocations, or a host on a machine with the GPU — and that case is exactly what a
// running inferd is. So this is not a shortcut around the protocol; it is the same
// Handler reached without one, which is why the protocol lives behind an interface
// rather than being the only way in.
type Local struct {
	h Handler
	// Serialized because a Handler wraps one model and Serve is documented to run one
	// request at a time. Without this, an ocr verb fanning out across pages would
	// issue concurrent generations against a single-slot llama-server and get
	// queueing plus interleaved streams.
	mu sync.Mutex
}

// NewLocal wraps a Handler as an in-process Engine.
func NewLocal(h Handler) *Local { return &Local{h: h} }

// Recognize runs one page through the handler.
func (l *Local) Recognize(ctx context.Context, img *image.RGBA, opt ocr.Options) (string, error) {
	if l.h == nil {
		return "", errNoHandler
	}
	prompt := opt.Prompt
	if prompt == "" {
		prompt = ocr.PromptPage
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var sb strings.Builder
	err := l.h.Generate(ctx, img, prompt, opt.MaxTokens, func(delta string) {
		sb.WriteString(delta)
		if opt.OnDelta != nil {
			opt.OnDelta(delta)
		}
	})
	// Partial text is returned with the error, matching the Engine contract and for
	// the same reason: a page truncated by a token limit still parses, and discarding
	// it loses the only work the model did.
	return sb.String(), err
}

// Close is a no-op: the caller owns the handler's lifetime, since it owns the model
// load that is the expensive part. A Close here that killed llama-server would make
// an Engine's scope silently shorter than the host's.
func (l *Local) Close() error { return nil }

// Closer is a Handler that also holds resources, which docd.Host does.
type Closer interface {
	Handler
	Close() error
}

var _ ocr.Engine = (*Local)(nil)
var _ ocr.Engine = (*Engine)(nil)

// errNoHandler guards the zero value, which would otherwise nil-panic inside
// Generate and report a crash where the real fault is a missing constructor call.
var errNoHandler = errors.New("ipc: Local has no handler; construct it with NewLocal")

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

// Listener accepts framed connections.
type Listener struct {
	ln   net.Listener
	addr string
}

// Listen binds addr, or DefaultAddr when addr is empty.
//
// Bind late. A caller should load its model first and call this only once it can
// serve, because a bound socket is this protocol's readiness signal — there is no
// health frame, so a socket that exists before the model does turns every early
// client's wait into a failure.
func Listen(addr string) (*Listener, error) {
	if addr == "" {
		addr = DefaultAddr()
	}
	ln, err := listen(addr)
	if err != nil {
		return nil, err
	}
	return &Listener{ln: ln, addr: addr}, nil
}

// Addr reports the bound address.
func (l *Listener) Addr() string { return l.addr }

// Close stops accepting and removes the socket file where there is one.
func (l *Listener) Close() error { return l.ln.Close() }

// Accept serves connections until ctx is cancelled or Close is called.
//
// Each connection gets a goroutine and is served serially within it: one model, one
// page at a time, but several clients may queue without any of them being refused.
// A per-connection failure is logged and the connection dropped, never propagated —
// one client sending a malformed frame must not take down a host that took ten
// minutes to warm up.
func (l *Listener) Accept(ctx context.Context, h Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	// Close on cancellation, which is what unblocks the Accept below.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.ln.Close()
		case <-done:
		}
	}()

	var wg sync.WaitGroup
	// Waited on before returning, so a caller that shuts the host down does not kill
	// llama-server while a page is still being written back to a client.
	defer wg.Wait()

	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("ipc: accept on %s: %w", l.addr, err)
		}
		wg.Add(1)
		go func(rw io.ReadWriteCloser) {
			defer wg.Done()
			if err := Serve(ctx, NewConn(rw), h, log); err != nil && ctx.Err() == nil {
				log.Warn("connection ended", "err", err)
			}
		}(conn)
	}
}

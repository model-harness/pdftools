//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

// dial opens a Unix-domain socket.
//
// Retries the two errors that mean "the host is not listening yet" — ENOENT, no
// socket file, and ECONNREFUSED, a stale file with nothing behind it — because a host
// binds its socket only after its model is loaded, which takes seconds. EACCES is not
// retried: a socket owned by another user will not become accessible by waiting, and
// looping on it would turn a permissions problem into a ten-second hang with no
// explanation.
func dial(ctx context.Context, addr string) (io.ReadWriteCloser, error) {
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var d net.Dialer
	for {
		conn, err := d.DialContext(ctx, "unix", addr)
		if err == nil {
			return conn, nil
		}
		if transient(err) && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		return nil, fmt.Errorf("ocr/ipc: dial %s: %w (is a model host running?)", addr, err)
	}
}

func transient(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, os.ErrNotExist)
}

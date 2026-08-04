//go:build windows

package ipc

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// dial opens a Windows named pipe.
//
// os.OpenFile rather than a pipe library, because pdf-spec has no dependency here and
// the semantics a synchronous request/response client needs are the ones a plain file
// handle already has. inferd's Go client makes the same call for the same reason.
func dial(ctx context.Context, addr string) (io.ReadWriteCloser, error) {
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for {
		// The path is the pipe address — the whole point of the parameter — and os.Root
		// cannot scope it, because \\.\pipe is not a directory. A caller who can choose
		// the address is choosing which host to talk to, which is what -addr is for.
		f, err := os.OpenFile(addr, os.O_RDWR, 0) // #nosec G304 -- addr is the pipe to open
		if err == nil {
			return f, nil
		}
		// ERROR_PIPE_BUSY is the window between a server accepting one client and
		// binding its next instance — a routine race on every connect, not a failure,
		// so it is retried. Matched case-insensitively on the message text: Windows
		// capitalises it, and inferd's issue #49 was exactly a lowercase-only match
		// that never fired, which turned a 20 ms retry into an immediate failure.
		if isBusy(err) && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		return nil, fmt.Errorf("ocr/ipc: open %s: %w (is a model host running?)", addr, err)
	}
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		// The pipe does not exist yet. Retried because a host still loading its model
		// has not bound it, and connect-refused is this protocol's not-ready signal.
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "pipe instances are busy") ||
		strings.Contains(s, "the system cannot find the file")
}

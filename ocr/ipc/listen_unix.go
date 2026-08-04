//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// listen binds a Unix-domain socket.
//
// Two things here are security decisions rather than plumbing. The parent directory is
// created 0700, so the socket sits somewhere only its owner can traverse — the
// resolution chain in DefaultAddr prefers XDG_RUNTIME_DIR for the same reason, and a
// world-traversable /tmp fallback would otherwise undo it. And a stale socket file is
// removed before binding, because a host killed with SIGKILL leaves one behind and
// every subsequent start would fail on EADDRINUSE with nothing listening.
//
// Removing it is safe *because* of the 0700 directory: an attacker who could plant a
// file at that path could already do worse. Without that, unlinking a path before
// binding it is the classic race, so the two are one decision and not two.
func listen(addr string) (net.Listener, error) {
	dir := filepath.Dir(addr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ipc: create %s: %w", dir, err)
	}
	// Tightened even when the directory already existed, since MkdirAll leaves an
	// existing directory's mode alone.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ipc: secure %s: %w", dir, err)
	}
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("ipc: remove stale socket %s: %w", addr, err)
	}

	ln, err := net.Listen("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s: %w", addr, err)
	}
	// Belt to the directory's braces. The socket's own mode is the only thing standing
	// between a misconfigured runtime directory and an unauthenticated local
	// inference endpoint.
	if err := os.Chmod(addr, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("ipc: secure %s: %w", addr, err)
	}
	return ln, nil
}

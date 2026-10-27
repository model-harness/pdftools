//go:build !windows

package inferd

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// TestReadStatusWaitsForABusyDaemon: a daemon busy with another request, as inferd on a CPU is,
// can take seconds to write its opening frames, and is still ready. inferd.yml's runner took more
// than three, and the converter read the chart and the picture without the model.
func TestReadStatusWaitsForABusyDaemon(t *testing.T) {
	addr := filepath.Join(t.TempDir(), "admin.sock")
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Skipf("no unix socket here: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		time.Sleep(4 * time.Second)
		_, _ = conn.Write([]byte(adminOpening))
	}()
	st, err := ReadStatus(context.Background(), addr)
	if err != nil || !st.Ready() {
		t.Fatalf("ReadStatus = %+v, %v; want the busy daemon's ready status", st, err)
	}
	if _, ok := st.Vision(); !ok {
		t.Errorf("no vision backend in %+v", st)
	}
}

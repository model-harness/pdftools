package main

import (
	"os"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns what was written.
//
// A pipe rather than swapping in a bytes.Buffer, because the code under test writes to
// os.Stderr directly — which is the behavior worth testing, since a warning that goes
// to stdout would land inside the Markdown it is warning about.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	// Read concurrently: a pipe's buffer is finite, and a writer that fills it before
	// anyone reads would deadlock rather than fail.
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 512)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()

	fn()

	os.Stderr = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := <-done
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

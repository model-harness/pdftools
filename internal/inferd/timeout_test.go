package inferd

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// TestPostBoundedAgainstWedgedDaemon is the regression test for the
// unbounded-context hang: a daemon that accepts the connection and then
// never answers used to stall Post forever, because every hot-path
// subcommand passes context.Background(). The model is optional, and
// an unbounded wait is a hang, not a fallback.
func TestPostBoundedAgainstWedgedDaemon(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	// Server reads the request and deliberately never replies.
	go func() {
		buf := make([]byte, 4096)
		_, _ = c2.Read(buf)
	}()

	cl := &Client{
		Timeout:  150 * time.Millisecond,
		dialFunc: func(context.Context) (net.Conn, error) { return c1, nil },
	}

	done := make(chan error, 1)
	go func() {
		_, err := cl.Post(context.Background(), Request{ID: "wedged"})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want context.DeadlineExceeded, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Post did not return: a wedged daemon still stalls the conversion")
	}
}

// A caller's own shorter deadline must survive — WithTimeout only ever
// tightens, so the client's bound must not extend a tighter caller ctx.
func TestPostRespectsTighterCallerDeadline(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })
	go func() {
		buf := make([]byte, 4096)
		_, _ = c2.Read(buf)
	}()

	cl := &Client{
		Timeout:  30 * time.Second, // generous client bound
		dialFunc: func(context.Context) (net.Conn, error) { return c1, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := cl.Post(ctx, Request{ID: "tight"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("caller deadline ignored: took %s", elapsed)
	}
}

// Timeout < 0 restores unbounded behaviour for operators debugging a
// slow local model. Verified by observing that the call is still running
// after a bound that would have fired.
func TestNegativeTimeoutDisablesDeadline(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })
	go func() {
		buf := make([]byte, 4096)
		_, _ = c2.Read(buf)
	}()

	cl := &Client{
		Timeout:  -1,
		dialFunc: func(context.Context) (net.Conn, error) { return c1, nil },
	}

	done := make(chan struct{})
	go func() {
		_, _ = cl.Post(context.Background(), Request{ID: "unbounded"})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Timeout=-1 should not impose a deadline, but Post returned")
	case <-time.After(300 * time.Millisecond):
		// Still blocked, as requested.
	}
}

// A dead daemon plus an expired context is both an unreachable backend
// and a timeout. ErrBackendNotReady must survive: it is the fail-open
// signal callers switch on and the more actionable of the two
// classifications. Relabelling it as a timeout would report a
// daemon-down event as slowness.
func TestBackendNotReadySurvivesExpiredContext(t *testing.T) {
	cl := &Client{
		Timeout: 50 * time.Millisecond,
		dialFunc: func(ctx context.Context) (net.Conn, error) {
			// Outlive the deadline, then fail the way a down daemon does.
			<-ctx.Done()
			return nil, errors.New("connect: connection refused")
		},
	}
	_, err := cl.Post(context.Background(), Request{ID: "down"})
	if !errors.Is(err, ErrBackendNotReady) {
		t.Fatalf("want ErrBackendNotReady preserved, got %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("daemon-down must not be relabelled a timeout: %v", err)
	}
}

// TestDefaultTimeoutOutlastsACPURequest: a client given no timeout waits DefaultTimeout, which
// must outlast one request on a CPU with no accelerator. On a 4-vCPU CI runner one description
// of a page image took 58.6 s, against the 60 s bound this replaced, and the gold set's page of
// images then failed when one ran over. The bound is for a daemon that never answers, so it can
// afford a slower machine than that runner several times over.
func TestDefaultTimeoutOutlastsACPURequest(t *testing.T) {
	const measured = 58600 * time.Millisecond
	if got := (&Client{}).timeout(); got != DefaultTimeout {
		t.Errorf("a client with no timeout waits %v, want DefaultTimeout %v", got, DefaultTimeout)
	}
	if DefaultTimeout < 4*measured {
		t.Errorf("DefaultTimeout %v is under four times the %v one CPU request took", DefaultTimeout, measured)
	}
	if got := (&Client{Timeout: 3 * time.Second}).timeout(); got != 3*time.Second {
		t.Errorf("an explicit timeout: %v", got)
	}
}

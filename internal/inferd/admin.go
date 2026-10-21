package inferd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Capability is one backend's advertisement on the admin socket: the "capabilities" frame
// (protocol-v2.md §1). An absent key means not supported, never unknown.
type Capability struct {
	Backend string `json:"backend"`
	V2      bool   `json:"v2"`
	Vision  bool   `json:"vision"`
	Embed   bool   `json:"embed"`
}

// Status is what the admin socket says on connect: a capability frame per backend, then the
// daemon's state.
type Status struct {
	Capabilities []Capability
	// State is the snapshot frame's status: "ready" when generation is served, "loading" or
	// another phase while it is not.
	State string
}

// Ready reports whether the daemon is ready.
func (s Status) Ready() bool { return s.State == "ready" }

// Vision returns the generation backend that takes images, and whether there is one.
func (s Status) Vision() (Capability, bool) {
	for _, c := range s.Capabilities {
		if c.V2 && c.Vision {
			return c, true
		}
	}
	return Capability{}, false
}

// adminTimeout bounds reading the admin socket's opening frames, which the daemon writes at
// once on connect.
const adminTimeout = 3 * time.Second

// adminDial bounds the dial. A Windows pipe can be briefly absent between instances, so the
// dialer retries a missing one, but a run without inferd asks once per document and should not
// wait out the read bound to learn there is no daemon.
const adminDial = 250 * time.Millisecond

// ReadStatus dials the admin socket at addr, empty for the platform default, and reads the
// frames the daemon writes on connect. A daemon that is not running returns
// ErrBackendNotReady. The socket is read-only, so nothing is sent.
func ReadStatus(ctx context.Context, addr string) (Status, error) {
	if addr == "" {
		addr = DefaultAdminAddress()
	}
	ctx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	dctx, dcancel := context.WithTimeout(ctx, adminDial)
	defer dcancel()
	conn, err := dialNative(dctx, addr)
	if err != nil {
		return Status{}, fmt.Errorf("%w: %v", ErrBackendNotReady, err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl)
	}
	return readStatus(conn)
}

// maxAdminFrames bounds the frames read before the snapshot: one per backend, and one warm model
// per inferd process.
const maxAdminFrames = 64

// readStatus reads capability frames up to the first frame that is not one, the snapshot.
func readStatus(r io.Reader) (Status, error) {
	br := bufio.NewReader(io.LimitReader(r, 1<<20))
	var st Status
	for i := 0; i < maxAdminFrames; i++ {
		line, err := br.ReadBytes('\n')
		if err != nil && (len(line) == 0 || !errors.Is(err, io.EOF)) {
			return st, fmt.Errorf("inferd admin: %w", err)
		}
		var f struct {
			Status string `json:"status"`
			Capability
		}
		if jerr := json.Unmarshal(line, &f); jerr != nil {
			return st, fmt.Errorf("inferd admin frame: %w", jerr)
		}
		if f.Status != "capabilities" {
			st.State = f.Status
			return st, nil
		}
		st.Capabilities = append(st.Capabilities, f.Capability)
	}
	return st, fmt.Errorf("inferd admin: more than %d capability frames", maxAdminFrames)
}

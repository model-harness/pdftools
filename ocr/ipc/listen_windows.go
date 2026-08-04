//go:build windows

package ipc

import (
	"errors"
	"net"
)

// listen is unimplemented on Windows.
//
// A named-pipe *server* needs CreateNamedPipe with an explicit security descriptor,
// which the standard library does not expose — os.OpenFile covers the client side and
// nothing more. Doing it properly means either golang.org/x/sys/windows or
// Microsoft/go-winio, and taking a dependency to serve a socket that the in-process
// path already makes unnecessary is not a trade worth making yet.
//
// It is not a gap in practice. On Windows the ocr verb runs its host in-process, so a
// listener is only needed to share one warm model between several CLI invocations —
// and the platform where that matters is a Linux box with a GPU, which does have one.
// A pipe server here would also need a deliberate ACL: the default descriptor on a
// named pipe grants more than a loopback inference endpoint should, and getting that
// wrong silently is worse than not offering it.
func listen(addr string) (net.Listener, error) {
	return nil, errors.New("ipc: serving a named pipe is not implemented on Windows; " +
		"run the model host in-process (the ocr verb's default) or point -addr at a host on another machine's socket")
}

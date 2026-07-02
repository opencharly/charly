package vmshared

// SSH local-forward helper built on golang.org/x/crypto/ssh. Used by
// `charly check libvirt|vnc --uri qemu+ssh://…` auto-tunneling, by the `spice:`
// check verb's host-side endpoint pre-resolution (spice_preresolve.go, which forwards
// a remote VM's SPICE socket/TCP to a local address for the out-of-process plugin),
// and by the user-facing `charly ssh tunnel` command (charly/ssh.go).
//
// Two forward modes:
//   - ForwardTCP  — local TCP listener on 127.0.0.1:0 → remote TCP
//     endpoint. Used for legacy VNC / TCP-exposed SPICE deployments.
//   - ForwardUnix — local UNIX socket under /tmp → remote UNIX
//     socket. Used for SPICE sockets (the arch default
//     after the Part 1 cutover).
//
// Each forward runs one goroutine per accepted connection that
// io.Copy's bytes bidirectionally. Cleanup closes the local
// listener; the SSH client itself stays alive until (t *SSHTunnel)
// Close is called, which is the right shape for multiple concurrent
// forwards on one ssh connection.

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	cryptorand "crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/ssh"
)

// SSHTunnel is a live SSH connection plus any forwards opened against
// it. Call Close to tear everything down.
type SSHTunnel struct {
	client *ssh.Client

	mu      sync.Mutex
	closers []func() // per-forward cleanup
}

// NewSSHTunnel dials the target and returns a tunnel handle. The
// underlying ssh.Client can host any number of forwards.
func NewSSHTunnel(t SSHTarget) (*SSHTunnel, error) {
	client, err := DialSSH(t)
	if err != nil {
		return nil, err
	}
	return &SSHTunnel{client: client}, nil
}

// Client returns the underlying SSH client, for consumers that open their own
// channels over the same connection (e.g. dialing a remote unix socket). The
// client stays owned by the tunnel — use Close to disconnect.
func (t *SSHTunnel) Client() *ssh.Client { return t.client }

// Close tears down all open forwards, then disconnects. Idempotent.
func (t *SSHTunnel) Close() error {
	t.mu.Lock()
	closers := t.closers
	t.closers = nil
	t.mu.Unlock()
	for _, c := range closers {
		c()
	}
	if t.client != nil {
		return t.client.Close()
	}
	return nil
}

// ForwardTCP opens a local 127.0.0.1:<random> listener that forwards
// each accepted connection to <remoteHost>:<remotePort> via the SSH
// connection. Returns the local "127.0.0.1:<port>" address plus a
// cleanup func that closes the listener (but not the SSH client —
// use Close for that).
func (t *SSHTunnel) ForwardTCP(ctx context.Context, remoteHost string, remotePort int) (string, func(), error) {
	if remoteHost == "" {
		remoteHost = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("local listen: %w", err)
	}
	localAddr := ln.Addr().String()
	go t.acceptLoop(ctx, ln, func() (net.Conn, error) {
		return t.client.Dial("tcp", fmt.Sprintf("%s:%d", remoteHost, remotePort))
	})
	cleanup := func() { _ = ln.Close() }
	t.mu.Lock()
	t.closers = append(t.closers, cleanup)
	t.mu.Unlock()
	return localAddr, cleanup, nil
}

// ForwardUnix opens a local UNIX socket under /tmp/charly-tunnel-<uuid>.sock
// that forwards each accepted connection to <remoteSocket> on the
// other side of the SSH connection. Returns the local socket path
// plus a cleanup func.
func (t *SSHTunnel) ForwardUnix(ctx context.Context, remoteSocket string) (string, func(), error) {
	if remoteSocket == "" {
		return "", nil, fmt.Errorf("remote socket path is empty")
	}
	tok, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	localPath := filepath.Join(os.TempDir(), "charly-tunnel-"+tok+".sock")
	// Clean any stale file at that path first — unlikely given the
	// random token, but defensive against re-use.
	_ = os.Remove(localPath)
	ln, err := net.Listen("unix", localPath)
	if err != nil {
		return "", nil, fmt.Errorf("local unix listen %s: %w", localPath, err)
	}
	go t.acceptLoop(ctx, ln, func() (net.Conn, error) {
		return t.client.Dial("unix", remoteSocket)
	})
	cleanup := func() {
		_ = ln.Close()
		_ = os.Remove(localPath)
	}
	t.mu.Lock()
	t.closers = append(t.closers, cleanup)
	t.mu.Unlock()
	return localPath, cleanup, nil
}

// acceptLoop runs one accept/dial/pipe cycle per connection until
// the listener is closed. Each pair of bytes-copy goroutines ends
// naturally when either side closes.
func (t *SSHTunnel) acceptLoop(ctx context.Context, ln net.Listener, dial func() (net.Conn, error)) {
	for {
		local, err := ln.Accept()
		if err != nil {
			// Listener closed (normal) or context-cancelled.
			return
		}
		go func() {
			defer local.Close() //nolint:errcheck
			remote, err := dial()
			if err != nil {
				return
			}
			defer remote.Close() //nolint:errcheck
			// Two halves of the pipe. Close whichever side finishes
			// first so the other io.Copy returns promptly.
			done := make(chan struct{}, 2)
			go func() {
				_, _ = io.Copy(remote, local)
				done <- struct{}{}
			}()
			go func() {
				_, _ = io.Copy(local, remote)
				done <- struct{}{}
			}()
			select {
			case <-done:
			case <-ctx.Done():
			}
		}()
	}
}

// randomToken returns 8 hex chars. Enough entropy to avoid collisions
// with concurrent charly processes sharing /tmp.
func randomToken() (string, error) {
	var b [4]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

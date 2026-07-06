package main

import (
	"fmt"
	"io"
	"net"
	"time"
)

// vnc_helpers.go holds the host-side VNC support the generic graphics reverse-leg
// (resolveVerbGraphics, check_endpoint_resolve.go) needs but the out-of-process
// candy/plugin-vnc cannot reach: the credential-store VNC ticket, and the UNIX-socket→TCP
// bridge a TCP-only RFB client requires. The former host-side vnc endpoint preresolvers moved
// into resolveVerbGraphics (the ONE venue-aware vnc/spice resolution).

// resolveVNCPassword resolves a deployment's VNC ticket from the credential store (the
// VNC_PASSWORD env override first, then the instance-specific then image-level key). It stays
// HOST-side — the out-of-process plugin cannot reach the credential store; resolveVerbGraphics
// hands the resolved password to the plugin via the reverse-leg reply. wayvnc auth itself is
// provisioned at DEPLOY time (the wayvnc / sway-desktop-vnc candy), not by the check verb.
func resolveVNCPassword(boxName, instance string) string {
	if instance != "" {
		key := boxName + "-" + instance
		val, _ := ResolveCredential("VNC_PASSWORD", CredServiceVNC, key, "")
		if val != "" {
			return val
		}
	}
	val, _ := ResolveCredential("VNC_PASSWORD", CredServiceVNC, boxName, "")
	return val
}

// unixToTcpBridge starts a TCP listener on 127.0.0.1:0 that pipes each accepted connection
// to the named UNIX socket. The returned listener owns a goroutine that exits when the
// listener is closed. Used by the VM-VNC endpoint resolution (resolveVerbGraphics) AND the
// libvirt SSH path (ssh.go) — a shared host-side networking helper (R3), kept host-side because
// the RFB client (candy/plugin-vnc) speaks over a plain TCP net.Conn only.
func unixToTcpBridge(socketPath string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bridge listen: %w", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close() //nolint:errcheck
				u, err := net.DialTimeout("unix", socketPath, 5*time.Second)
				if err != nil {
					return
				}
				defer u.Close() //nolint:errcheck
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(u, conn); done <- struct{}{} }()
				go func() { _, _ = io.Copy(conn, u); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	return ln, nil
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/opencharly/sdk/kit"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// vnc_preresolve.go is the HOST-side residue of the externalized `vnc` verb. The
// out-of-process candy/plugin-vnc provider speaks the RFB protocol on the wire (the
// stdlib-only RFC 6143 VNC client) but owns NONE of charly's venue / podman / libvirt /
// port-mapping machinery — so the host does the deployment → venue → host-reachable-RFB
// resolution and hands the plugin a plain DIALABLE "host:port" (+ the resolved VNC
// password) via the CheckEnv. The plugin
// cannot reach core's podman engine / project loader / go-libvirt, so the host
// pre-resolves before marshaling.
//
// The resolution is DUAL — a `vnc:` op targets EITHER a container/host deployment (the
// published VNC port 5900) OR a kind:vm deployment (the libvirt-discovered
// <graphics type='vnc'> listener, possibly a UNIX socket and/or a remote qemu+ssh://
// hypervisor — bridged/tunneled to a host-reachable TCP address). The venue kind picks
// the leg; both produce one dialable address, exactly as the (now-removed) in-core
// container path + `charly check vnc vm` path did.

// VncEnv is the host-resolved, DIALABLE RFB endpoint shipped to the out-of-process
// candy/plugin-vnc provider via CheckEnv.Substrate. Addr is the host-reachable "host:port"
// the plugin dials over TCP (a container's published 5900, or a VM's bridged/forwarded
// RFB address); Password is the resolved VNC ticket ("" = no auth / VeNCrypt-None). The
// plugin just dials this; it needs no podman, no venue resolution, no libvirt.
type VncEnv struct {
	Addr     string `json:"addr"`
	Password string `json:"password"`
}

// preresolveVncEndpoint resolves a `vnc:` op's target deployment (r.Box) to the dialable
// RFB endpoint host-side. Returns:
//   - env:     the resolved endpoint (nil for a non-vnc op or a box-mode / no-box run —
//     the plugin's own no-endpoint skip then fires);
//   - cleanup: closes any opened bridge listener + ssh tunnel/forward (ALWAYS non-nil —
//     defer it unconditionally); it must outlive the plugin's Invoke (it carries the live
//     RFB connection), so invokeVerbProvider defers it across Invoke;
//   - early:   a pre-dispatch CheckResult to return immediately — a SKIP when a VM
//     declares no VNC display device (the host-side analogue of the former in-proc
//     subprocess-stderr no-display-device skip) or a FAIL when the endpoint cannot
//     be resolved; nil to proceed to dispatch.
//
// Early-FAILs on a resolution error, SKIPs on no-display-device; it also returns a cleanup because the
// container leg opens a CheckEndpoint (an ssh -L forward for a host/ssh venue) and the VM
// leg opens a bridge listener / SSH tunnel the host must release after Invoke.
func (r *Runner) preresolveVncEndpoint(c *Op) (env *VncEnv, cleanup func(), early *CheckResult) {
	noop := func() {}
	// Non-vnc op, or no live container context (box-mode / empty box) → nothing to
	// resolve; the plugin's own box-mode / no-endpoint skip handles the degenerate cases.
	if c.Plugin != "vnc" || r.Mode == RunModeBox || r.Box == "" {
		return nil, noop, nil
	}
	venue, err := resolveCheckVenue(r.Box, r.Instance)
	if err != nil {
		res := failf(c, "vnc: %s: %v", kit.InputStr(c, "method"), err)
		return nil, noop, &res
	}
	if venue.Kind == "vm" {
		return r.preresolveVncVmEndpoint(c)
	}
	// Container / host venue → the published (or ssh-forwarded) VNC port 5900 + the
	// credential-store-resolved password.
	ep, err := resolveCheckEndpoint(venue, 5900)
	if err != nil {
		res := failf(c, "vnc: %s: VNC server not reachable (port 5900): %v", kit.InputStr(c, "method"), err)
		return nil, noop, &res
	}
	password := resolveVNCPassword(resolveBoxName(r.Box), r.Instance)
	return &VncEnv{Addr: ep.Addr, Password: password}, ep.Close, nil
}

// preresolveVncVmEndpoint is the VM leg: it resolves the kind:vm deployment's
// <graphics type='vnc'> listener via the out-of-process vm plugin (the go-libvirt
// resolution moved there), then builds a host-reachable TCP address — bridging a UNIX
// socket and/or SSH-tunneling a remote qemu+ssh:// listener — the SAME 4-case switch the
// former in-core VM-VNC CLI path used, but returning a dialable ADDR + a cleanup instead
// of a *VNCClient (the RFB client now lives in candy/plugin-vnc and dials this).
func (r *Runner) preresolveVncVmEndpoint(c *Op) (env *VncEnv, cleanup func(), early *CheckResult) {
	noop := func() {}
	uri := os.Getenv("CHARLY_LIBVIRT_URI")
	raw, ok := invokeVmPlugin("resolve-vnc", r.vmTargetName(), uri)
	if !ok {
		res := failf(c, "vnc: %s: vm plugin unavailable (go-libvirt resolution is out-of-process)", kit.InputStr(c, "method"))
		return nil, noop, &res
	}
	var rr vmResolveResult
	if err := json.Unmarshal(raw, &rr); err != nil {
		res := failf(c, "vnc: %s: decode resolve: %v", kit.InputStr(c, "method"), err)
		return nil, noop, &res
	}
	if rr.Error != "" {
		// "VM <name> has no VNC graphics device declared in vm.yml" → N/A SKIP (the
		// VNC-less GPU-passthrough operator vs the VNC-having check bed); any other
		// resolver error is a real FAIL.
		if strings.Contains(rr.Error, noVmDisplayDeviceErr) {
			res := skipf(c, fmt.Sprintf("vnc %s — N/A: deployment has no VNC graphics device", kit.InputStr(c, "method")))
			return nil, noop, &res
		}
		res := failf(c, "vnc: %s: %s", kit.InputStr(c, "method"), rr.Error)
		return nil, noop, &res
	}
	ep := rr.Endpoint
	tunnelTarget := rr.TunnelTarget

	switch {
	case !ep.TunnelNeeded && !ep.IsSocket:
		// Local TCP — straight dial.
		return &VncEnv{Addr: fmt.Sprintf("%s:%d", ep.Host, ep.Port), Password: ep.Password}, noop, nil

	case !ep.TunnelNeeded && ep.IsSocket:
		// Local UNIX socket — bridge it to a TCP listener the RFB client (TCP-only) dials.
		bridge, berr := unixToTcpBridge(ep.SocketPath)
		if berr != nil {
			res := failf(c, "vnc: %s: %v", kit.InputStr(c, "method"), berr)
			return nil, noop, &res
		}
		return &VncEnv{Addr: bridge.Addr().String(), Password: ep.Password}, func() { _ = bridge.Close() }, nil

	case ep.TunnelNeeded && ep.IsSocket:
		// Remote UNIX socket — SSH-forward it to a local socket, then bridge to TCP.
		parsed, perr := ParseLibvirtURI(tunnelTarget)
		if perr != nil {
			res := failf(c, "vnc: %s: %v", kit.InputStr(c, "method"), perr)
			return nil, noop, &res
		}
		tun, terr := NewSSHTunnel(parsed.Remote)
		if terr != nil {
			res := failf(c, "vnc: %s: ssh tunnel to %s: %v", kit.InputStr(c, "method"), parsed.Remote, terr)
			return nil, noop, &res
		}
		localSock, _, ferr := tun.ForwardUnix(context.Background(), ep.SocketPath)
		if ferr != nil {
			_ = tun.Close()
			res := failf(c, "vnc: %s: forwarding remote socket %s: %v", kit.InputStr(c, "method"), ep.SocketPath, ferr)
			return nil, noop, &res
		}
		bridge, berr := unixToTcpBridge(localSock)
		if berr != nil {
			_ = tun.Close()
			res := failf(c, "vnc: %s: %v", kit.InputStr(c, "method"), berr)
			return nil, noop, &res
		}
		return &VncEnv{Addr: bridge.Addr().String(), Password: ep.Password}, func() { _ = bridge.Close(); _ = tun.Close() }, nil

	case ep.TunnelNeeded && !ep.IsSocket:
		// Remote TCP — SSH-forward it to a local TCP port, dial that.
		parsed, perr := ParseLibvirtURI(tunnelTarget)
		if perr != nil {
			res := failf(c, "vnc: %s: %v", kit.InputStr(c, "method"), perr)
			return nil, noop, &res
		}
		tun, terr := NewSSHTunnel(parsed.Remote)
		if terr != nil {
			res := failf(c, "vnc: %s: ssh tunnel to %s: %v", kit.InputStr(c, "method"), parsed.Remote, terr)
			return nil, noop, &res
		}
		localAddr, _, ferr := tun.ForwardTCP(context.Background(), ep.Host, ep.Port)
		if ferr != nil {
			_ = tun.Close()
			res := failf(c, "vnc: %s: forwarding remote TCP %s:%d: %v", kit.InputStr(c, "method"), ep.Host, ep.Port, ferr)
			return nil, noop, &res
		}
		return &VncEnv{Addr: localAddr, Password: ep.Password}, func() { _ = tun.Close() }, nil
	}
	res := failf(c, "vnc: %s: VNC endpoint resolution produced no dial target", kit.InputStr(c, "method"))
	return nil, noop, &res
}

// resolveVNCPassword resolves a deployment's VNC ticket from the credential store (the
// VNC_PASSWORD env override first, then the instance-specific then image-level key). It
// stays HOST-side — the out-of-process plugin cannot reach the credential store; the host
// hands the resolved password to the plugin via VncEnv. wayvnc auth itself is provisioned
// at DEPLOY time (the wayvnc / sway-desktop-vnc candy), not by the check verb.
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
// listener is closed. Used by the VM-VNC endpoint resolution above AND the libvirt SSH
// path (ssh.go) — a shared host-side networking helper (R3), kept host-side because the
// RFB client (candy/plugin-vnc) speaks over a plain TCP net.Conn only.
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

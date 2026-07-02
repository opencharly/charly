package main

// `charly ssh tunnel …` — opens an SSH-forwarded local endpoint pointing
// at a VM's SPICE/VNC display on a remote libvirt host, for clients
// that don't natively understand qemu+ssh:// (standalone
// remote-viewer with TCP addr, TigerVNC, Spicy, etc.).
//
// Note: virt-manager and `remote-viewer --connect qemu+ssh://…` do
// NOT need this command — they auto-forward UNIX sockets over
// libvirt's own RPC channel. This is strictly for clients that
// insist on a bare TCP/UNIX socket URL.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// SshCmd is the top-level `charly ssh` command group.
type SshCmd struct {
	Tunnel SshTunnelCmd `cmd:"" help:"Forward a VM's SPICE/VNC endpoint from a remote libvirt host to the local machine"`
}

// SshTunnelCmd groups the two flavors of display-channel forwarding.
type SshTunnelCmd struct {
	Spice SshTunnelSpiceCmd `cmd:"" help:"Forward the VM's SPICE endpoint (default: UNIX socket)"`
	Vnc   SshTunnelVncCmd   `cmd:"" help:"Forward the VM's VNC endpoint (default: UNIX socket if available, else TCP)"`
}

// sshTunnelFlags is the shared flag surface.
type sshTunnelFlags struct {
	Uri string `name:"uri" env:"CHARLY_LIBVIRT_URI" help:"Libvirt URI (default: qemu:///session). For a non-local hypervisor, use qemu+ssh://[user@]host/session."`
	Tcp bool   `name:"tcp" help:"Force a 127.0.0.1:<random> TCP forward even when the VM listens on a UNIX socket — for clients that don't speak spice+unix:// or vnc+unix://"`
}

// ---------------- tunnel spice ----------------

type SshTunnelSpiceCmd struct {
	Vm string `arg:"" help:"VM name (vm.yml entity)"`
	sshTunnelFlags
}

func (c *SshTunnelSpiceCmd) Run() error {
	return runSshTunnel(c.Vm, c.Uri, c.Tcp, "spice")
}

// ---------------- tunnel vnc ----------------

type SshTunnelVncCmd struct {
	Vm string `arg:"" help:"VM name (vm.yml entity)"`
	sshTunnelFlags
}

func (c *SshTunnelVncCmd) Run() error {
	return runSshTunnel(c.Vm, c.Uri, c.Tcp, "vnc")
}

// runSshTunnel resolves the VM's display endpoint, opens the
// appropriate forward, prints a connect URL, and blocks until
// SIGINT/SIGTERM.
func runSshTunnel(vmName, uri string, forceTCP bool, kind string) error {
	// Resolve the display endpoint via the out-of-process vm plugin (go-libvirt moved there).
	resolveOp := "resolve-spice"
	if kind == "vnc" {
		resolveOp = "resolve-vnc"
	}
	raw, ok := invokeVmPlugin(resolveOp, vmName, uri)
	if !ok {
		return fmt.Errorf("vm plugin unavailable (go-libvirt resolution is out-of-process)")
	}
	var rr vmResolveResult
	if err := json.Unmarshal(raw, &rr); err != nil {
		return err
	}
	if rr.Error != "" {
		return fmt.Errorf("%s", rr.Error)
	}
	ep := rr.Endpoint
	tunnelTarget := rr.TunnelTarget

	// Decide transport. If the VM uses a UNIX socket and --tcp is not
	// set, we preserve socket-ness (and print a spice+unix:// /
	// vnc+unix:// URL). If --tcp is set or the VM listens on TCP,
	// we open a 127.0.0.1:<random> listener locally.
	var tunnel *SSHTunnel
	var cleanup func()
	var connectURL string

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if ep.TunnelNeeded {
		parsed, err := ParseLibvirtURI(tunnelTarget)
		if err != nil {
			return err
		}
		tunnel, err = NewSSHTunnel(parsed.Remote)
		if err != nil {
			return err
		}
	}

	switch {
	case ep.IsSocket && !forceTCP:
		if tunnel != nil {
			localSock, cu, err := tunnel.ForwardUnix(ctx, ep.SocketPath)
			if err != nil {
				_ = tunnel.Close()
				return err
			}
			cleanup = cu
			connectURL = fmt.Sprintf("%s+unix://%s", kind, localSock)
		} else {
			// Local UNIX socket — nothing to forward; just print it.
			connectURL = fmt.Sprintf("%s+unix://%s", kind, ep.SocketPath)
		}
	case ep.IsSocket && forceTCP:
		// Need to bridge: UNIX (possibly remote) → local TCP.
		// Reuse the VNC bridge helper for this — it bridges UNIX to
		// TCP unconditionally.
		var sockPath string
		if tunnel != nil {
			localSock, cu, err := tunnel.ForwardUnix(ctx, ep.SocketPath)
			if err != nil {
				_ = tunnel.Close()
				return err
			}
			cleanup = cu
			sockPath = localSock
		} else {
			sockPath = ep.SocketPath
		}
		ln, err := unixToTcpBridge(sockPath)
		if err != nil {
			if tunnel != nil {
				_ = tunnel.Close()
			}
			return err
		}
		prev := cleanup
		cleanup = func() {
			_ = ln.Close()
			if prev != nil {
				prev()
			}
		}
		connectURL = fmt.Sprintf("%s://%s", kind, ln.Addr().String())
	default:
		// TCP endpoint. Local → no tunnel; remote → SSH forward.
		if tunnel != nil {
			localAddr, cu, err := tunnel.ForwardTCP(ctx, ep.Host, ep.Port)
			if err != nil {
				_ = tunnel.Close()
				return err
			}
			cleanup = cu
			connectURL = fmt.Sprintf("%s://%s", kind, localAddr)
		} else {
			connectURL = fmt.Sprintf("%s://%s:%d", kind, ep.Host, ep.Port)
		}
	}

	fmt.Printf("%s tunnel: %s\n", kind, connectURL)
	switch kind {
	case "spice":
		fmt.Printf("Connect with: remote-viewer %s\n", connectURL)
	case "vnc":
		fmt.Printf("Connect with: remote-viewer %s\n", connectURL)
	}
	fmt.Println("Press Ctrl-C to close the tunnel.")

	// Block on signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Fprintln(os.Stderr, "closing tunnel.")
	if cleanup != nil {
		cleanup()
	}
	if tunnel != nil {
		_ = tunnel.Close()
	}
	return nil
}

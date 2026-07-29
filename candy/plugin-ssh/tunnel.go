package ssh

// tunnel.go — the `charly ssh tunnel …` handler (relocated verbatim from charly/ssh.go in the
// #118 loader+check-tail cone). It opens an SSH-forwarded local endpoint pointing at a VM's
// SPICE/VNC display on a remote libvirt host, for clients that don't natively understand
// qemu+ssh:// (standalone remote-viewer with a TCP addr, TigerVNC, Spicy, …). The ONLY thing it
// reached in charly core was invokeVmPlugin (the display-endpoint resolve); the plugin reaches
// verb:libvirt DIRECTLY over its in-proc reverse channel (InvokeProvider), so nothing crosses into
// core. Everything else — sshx tunnels, vmshared URI parse, kit.UnixToTCPBridge — is sdk.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/sshx"
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

// vmPluginCandyRef is the canonical candy ref for the out-of-process vm plugin (verb:libvirt),
// supplied as the InvokeProvider canonical-ref fallback so `charly ssh tunnel` connects go-libvirt
// even from a project whose candy closure never references plugin-vm directly.
func vmPluginCandyRef() string { return "@" + spec.DefaultProjectRepo + "/candy/plugin-vm" }

// invokeVmResolve reaches the compiled-in/out-of-process verb:libvirt for a display-endpoint
// resolve (the go-libvirt resolution the former core invokeVmPlugin performed), over the in-proc
// reverse channel the command dispatch threads.
func invokeVmResolve(vmOp, vmName, uri string) (json.RawMessage, bool) {
	if cmdExec == nil {
		return nil, false
	}
	envJSON, err := json.Marshal(spec.VmPluginEnv{VmOp: vmOp, VmName: vmName, URI: uri})
	if err != nil {
		return nil, false
	}
	out, err := cmdExec.InvokeProvider(cmdCtx, "verb", "libvirt", sdk.OpRun, nil, envJSON, sdk.InvokeProviderOpts{ExtraRef: vmPluginCandyRef()})
	if err != nil || out == nil {
		return nil, false
	}
	return out, true
}

// runSshTunnel resolves the VM's display endpoint, opens the appropriate forward, prints a connect
// URL, and blocks until SIGINT/SIGTERM.
func runSshTunnel(vmName, uri string, forceTCP bool, kind string) error {
	resolveOp := "resolve-spice"
	if kind == "vnc" {
		resolveOp = "resolve-vnc"
	}
	raw, ok := invokeVmResolve(resolveOp, vmName, uri)
	if !ok {
		return fmt.Errorf("vm plugin unavailable (go-libvirt resolution is out-of-process)")
	}
	var rr spec.VmResolveResult
	if err := json.Unmarshal(raw, &rr); err != nil {
		return err
	}
	if rr.Error != "" {
		return fmt.Errorf("%s", rr.Error)
	}
	ep := rr.Endpoint
	tunnelTarget := rr.TunnelTarget

	var tunnel *sshx.SSHTunnel
	var cleanup func()
	var connectURL string

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if ep.TunnelNeeded {
		parsed, err := vmshared.ParseLibvirtURI(tunnelTarget)
		if err != nil {
			return err
		}
		tunnel, err = sshx.NewSSHTunnel(parsed.Remote)
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
			connectURL = fmt.Sprintf("%s+unix://%s", kind, ep.SocketPath)
		}
	case ep.IsSocket && forceTCP:
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
		ln, err := kit.UnixToTCPBridge(sockPath)
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
	fmt.Printf("Connect with: remote-viewer %s\n", connectURL)
	fmt.Println("Press Ctrl-C to close the tunnel.")

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

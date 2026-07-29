package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
	"github.com/opencharly/sdk/sshx"
	"github.com/opencharly/sdk/vmshared"
)

// check_graphics_endpoint.go — the VM-graphics host-endpoint reverse-leg (resolveVerbGraphics), the
// IDENTICAL SIBLING of the floor check_endpoint_resolve.go's resolveVerbEndpoint: both are
// hostVerbResolver methods served over the CheckContext reverse channel (resolveGfx / resolveEp) that
// register their live forward's teardown on the host check Runner (h.endpointCleanups). The SSH tunnel
// is RUNNER-LIFECYCLE-BOUND — the RFB/spice client connects THROUGH it for the whole check — so a plugin
// Invoke could never hold it (it would close before the client connects). It is genuine HOST FABRIC (FLOOR),
// not a plugin capability.
//
// It imports sdk/sshx (golang.org/x/crypto/ssh) DIRECTLY, and that is CORRECT dependency CONTAINMENT, not
// a violation: relocating the tunnel to any plugin-shared kit (e.g. kit) spreads x/crypto/ssh to every
// plugin — RCA-proven, ~18 plugin builds break on the missing go.sum entry — so keeping it here confines
// x/crypto/ssh to this ONE host-fabric file, the analogue of the GPU host-legs using hardware libs. This is
// the SINGLE contained x/crypto/ssh boundary in charly core (once coneA moves vm_backend_lifecycle out).
// The libvirt-URI parse rides the floor-legal vmshared.ParseLibvirtURI (no charly alias).

// resolveVerbGraphics resolves a deployment's <kind> display (kind = "vnc" | "spice") to a
// dialable endpoint. It is venue-aware and REPLACES the former per-verb vnc + spice host
// preresolvers (R3):
//   - vnc + a NON-VM venue → the container/host's published RFB port 5900 + the credential-
//     store VNC password (spice has no container leg);
//   - vnc/spice + a VM venue → the VM's <graphics type='kind'> via the vm plugin (resolve-
//     <kind>), opening any qemu+ssh:// tunnel. The RFB client is TCP-only, so a vnc UNIX
//     socket is bridged to a local TCP listener; spice hands back the socket directly.
//
// Any bridge listener / ssh -L forward it opens is registered for post-Invoke teardown. Empty
// endpoint + nil err = no live venue (box-mode / no-box); Skip=true = the VM declares no
// graphics device of that kind (an N/A skip).
func (h *hostVerbResolver) resolveVerbGraphics(kind string) (graphicsEndpoint, error) {
	if h.kr.Box() == "" || h.kr.Mode() == RunModeBox {
		return graphicsEndpoint{}, nil
	}

	// vnc CONTAINER leg: a non-VM venue publishes RFB on 5900; the ticket comes from the
	// credential store. spice is VM-only (no container leg), so it skips straight to the vm plugin.
	if kind == "vnc" {
		reply, err := resolveCheckVenueReply(h.kr.Box(), h.kr.Instance())
		if err != nil {
			return graphicsEndpoint{}, err
		}
		if reply.Kind != "vm" {
			ep, err := kit.EndpointForVenue(reply.Descriptor, 5900)
			if err != nil {
				return graphicsEndpoint{}, fmt.Errorf("VNC server not reachable (port 5900): %w", err)
			}
			h.endpointCleanups = append(h.endpointCleanups, ep.Close)
			return graphicsEndpoint{Addr: ep.Addr, Password: resolveVNCPassword(kit.ResolveBoxName(h.kr.Box()), h.kr.Instance())}, nil
		}
	}

	// VM leg (vnc + spice): resolve the VM's <graphics type='kind'> via the out-of-process vm
	// plugin. CHARLY_LIBVIRT_URI selects a remote hypervisor.
	raw, ok := invokeVmPlugin("resolve-"+kind, h.kr.VmTargetName(), os.Getenv("CHARLY_LIBVIRT_URI"))
	if !ok {
		return graphicsEndpoint{}, fmt.Errorf("vm plugin unavailable (go-libvirt resolution is out-of-process)")
	}
	var rr spec.VmResolveResult
	if err := json.Unmarshal(raw, &rr); err != nil {
		return graphicsEndpoint{}, fmt.Errorf("decode resolve: %w", err)
	}
	if rr.Error != "" {
		// "VM <name> has no <KIND> graphics device declared in vm.yml" → N/A skip (the
		// display-less GPU operator vs the display-having check bed); else a real error.
		if strings.Contains(rr.Error, noVmDisplayDeviceErr) {
			return graphicsEndpoint{Skip: true, SkipMessage: fmt.Sprintf("deployment has no %s graphics device", strings.ToUpper(kind))}, nil
		}
		return graphicsEndpoint{}, errors.New(rr.Error)
	}
	ep := rr.Endpoint

	// bridgeSocket exposes a UNIX socket as a local TCP listener for the TCP-only RFB client
	// (vnc), registering the listener for teardown. Only vnc needs it; spice dials the socket.
	bridgeSocket := func(socketPath string) (string, error) {
		br, berr := kit.UnixToTCPBridge(socketPath)
		if berr != nil {
			return "", berr
		}
		h.endpointCleanups = append(h.endpointCleanups, func() { _ = br.Close() })
		return br.Addr().String(), nil
	}

	// Local endpoint — no tunnel.
	if !ep.TunnelNeeded {
		if ep.IsSocket {
			if kind == "vnc" {
				addr, berr := bridgeSocket(ep.SocketPath)
				if berr != nil {
					return graphicsEndpoint{}, berr
				}
				return graphicsEndpoint{Addr: addr, Password: ep.Password}, nil
			}
			return graphicsEndpoint{Socket: ep.SocketPath, Password: ep.Password}, nil
		}
		return graphicsEndpoint{Addr: fmt.Sprintf("%s:%d", ep.Host, ep.Port), Password: ep.Password}, nil
	}

	// Remote (qemu+ssh://) — open an SSH tunnel forwarding the endpoint to a local address;
	// register the teardown on the Runner (the tunnel carries the live connection).
	parsed, perr := vmshared.ParseLibvirtURI(rr.TunnelTarget)
	if perr != nil {
		return graphicsEndpoint{}, perr
	}
	tunnel, terr := sshx.NewSSHTunnel(parsed.Remote)
	if terr != nil {
		return graphicsEndpoint{}, fmt.Errorf("ssh tunnel to %s: %w", parsed.Remote, terr)
	}
	h.endpointCleanups = append(h.endpointCleanups, func() { _ = tunnel.Close() })
	if ep.IsSocket {
		localSock, _, ferr := tunnel.ForwardUnix(context.Background(), ep.SocketPath)
		if ferr != nil {
			return graphicsEndpoint{}, fmt.Errorf("forwarding remote socket %s: %w", ep.SocketPath, ferr)
		}
		if kind == "vnc" {
			addr, berr := bridgeSocket(localSock)
			if berr != nil {
				return graphicsEndpoint{}, berr
			}
			return graphicsEndpoint{Addr: addr, Password: ep.Password}, nil
		}
		return graphicsEndpoint{Socket: localSock, Password: ep.Password}, nil
	}
	localAddr, _, ferr := tunnel.ForwardTCP(context.Background(), ep.Host, ep.Port)
	if ferr != nil {
		return graphicsEndpoint{}, fmt.Errorf("forwarding remote TCP %s:%d: %w", ep.Host, ep.Port, ferr)
	}
	return graphicsEndpoint{Addr: localAddr, Password: ep.Password}, nil
}

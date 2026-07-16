package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// check_endpoint_resolve.go — the generic host-endpoint reverse-legs (H part 2). Class-generic
// venue→dialable-endpoint resolutions serve BOTH the in-process hostCheckContext and the
// out-of-process CheckContextService, REPLACING the per-verb host preresolvers (cdp/vnc/spice
// each declared their port / graphics kind and wrapped this SAME host machinery). The plugin
// decides WHAT to resolve (its port / graphics kind); the host holds the venue / podman /
// go-libvirt / tunnel machinery the out-of-process plugin cannot reach. The legs are methods on
// the host verb resolver (which reads the kit.Runner engine state through accessors and owns the
// per-Invoke endpoint cleanups).

// resolveVerbEndpoint resolves the current check target's venue (container / VM / ssh /
// local) and returns a host-reachable "host:port" for an in-venue TCP port, registering
// any opened ssh -L forward (VM/ssh venue) for post-Invoke teardown. Empty addr + nil err
// = no live venue (box-mode / no-box) — the verb's own no-endpoint skip then fires.
func (h *hostVerbResolver) resolveVerbEndpoint(port int) (string, error) {
	if h.kr.Box() == "" || h.kr.Mode() == RunModeBox {
		return "", nil
	}
	venue, err := resolveCheckVenue(h.kr.Box(), h.kr.Instance())
	if err != nil {
		return "", err
	}
	ep, err := resolveCheckEndpoint(venue, port)
	if err != nil {
		return "", err
	}
	h.endpointCleanups = append(h.endpointCleanups, ep.Close)
	return ep.Addr, nil
}

// graphicsEndpoint is the host-resolved dialable VM graphics endpoint (the in-proc twin of
// kit.GraphicsEndpoint). Exactly one of Addr / Socket is set; Skip=true means no such device.
type graphicsEndpoint struct {
	Addr, Socket, Password string
	Skip                   bool
	SkipMessage            string
}

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
		venue, err := resolveCheckVenue(h.kr.Box(), h.kr.Instance())
		if err != nil {
			return graphicsEndpoint{}, err
		}
		if venue.Kind != "vm" {
			ep, err := resolveCheckEndpoint(venue, 5900)
			if err != nil {
				return graphicsEndpoint{}, fmt.Errorf("VNC server not reachable (port 5900): %w", err)
			}
			h.endpointCleanups = append(h.endpointCleanups, ep.Close)
			return graphicsEndpoint{Addr: ep.Addr, Password: resolveVNCPassword(resolveBoxName(h.kr.Box()), h.kr.Instance())}, nil
		}
	}

	// VM leg (vnc + spice): resolve the VM's <graphics type='kind'> via the out-of-process vm
	// plugin. CHARLY_LIBVIRT_URI selects a remote hypervisor.
	raw, ok := invokeVmPlugin("resolve-"+kind, h.kr.VmTargetName(), os.Getenv("CHARLY_LIBVIRT_URI"))
	if !ok {
		return graphicsEndpoint{}, fmt.Errorf("vm plugin unavailable (go-libvirt resolution is out-of-process)")
	}
	var rr vmResolveResult
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
	parsed, perr := ParseLibvirtURI(rr.TunnelTarget)
	if perr != nil {
		return graphicsEndpoint{}, perr
	}
	tunnel, terr := NewSSHTunnel(parsed.Remote)
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

// runEndpointCleanups closes every forward opened during the verb's Invoke (LIFO) and
// resets the list. Called by invokeVerbProvider after the Invoke returns — the forward must
// outlive the plugin's dial, so it is released only once the Invoke completes.
func (h *hostVerbResolver) runEndpointCleanups() {
	for i := len(h.endpointCleanups) - 1; i >= 0; i-- {
		if h.endpointCleanups[i] != nil {
			h.endpointCleanups[i]()
		}
	}
	h.endpointCleanups = nil
}

// ResolveEndpoint is the in-process CheckContext leg — a compiled-in kit verb reaches the
// SAME generic endpoint resolution an out-of-process one gets over CheckContextService.
func (c hostCheckContext) ResolveEndpoint(_ context.Context, port int) (string, error) {
	return c.h.resolveVerbEndpoint(port)
}

// ResolveGraphicsEndpoint is the in-process CheckContext leg for VM graphics endpoints.
func (c hostCheckContext) ResolveGraphicsEndpoint(_ context.Context, kind string) (kit.GraphicsEndpoint, error) {
	ge, err := c.h.resolveVerbGraphics(kind)
	if err != nil {
		return kit.GraphicsEndpoint{}, err
	}
	return kit.GraphicsEndpoint{Addr: ge.Addr, Socket: ge.Socket, Password: ge.Password, Skip: ge.Skip, SkipMessage: ge.SkipMessage}, nil
}

// ResolveClusterContext is the in-process CheckContext leg for a k8s cluster-profile context.
func (c hostCheckContext) ResolveClusterContext(_ context.Context, cluster string) (string, error) {
	return c.h.resolveClusterContext(cluster)
}

// resolveImageLabel reads one raw OCI label off the deployment-under-test's image. It is the
// host-side leg for CheckContext.ResolveImageLabel — the out-of-process mcp verb needs the
// baked ai.opencharly.mcp_provide label but cannot reach the podman engine / OCI metadata.
// Empty value (no live deployment, or the label absent) is a valid result.
func (h *hostVerbResolver) resolveImageLabel(label string) (string, error) {
	if h.kr.Box() == "" || h.kr.Mode() == RunModeBox {
		return "", nil
	}
	engine, containerName, err := resolveContainer(h.kr.Box(), h.kr.Instance())
	if err != nil {
		return "", err
	}
	imageRef, err := containerImageRef(engine, containerName)
	if err != nil {
		return "", err
	}
	labels, err := InspectLabels(engine, imageRef)
	if err != nil {
		return "", err
	}
	return labels[label], nil
}

// ResolveImageLabel is the in-process CheckContext leg for a baked OCI label.
func (c hostCheckContext) ResolveImageLabel(_ context.Context, label string) (string, error) {
	return c.h.resolveImageLabel(label)
}

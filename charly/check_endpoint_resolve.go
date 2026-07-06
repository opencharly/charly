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
// venue→dialable-endpoint resolutions serve BOTH the in-process runnerCheckContext and the
// out-of-process CheckContextService, REPLACING the per-verb host preresolvers (cdp/vnc/spice
// each declared their port / graphics kind and wrapped this SAME host machinery). The plugin
// decides WHAT to resolve (its port / graphics kind); the host holds the venue / podman /
// go-libvirt / tunnel machinery the out-of-process plugin cannot reach.

// resolveVerbEndpoint resolves the current check target's venue (container / VM / ssh /
// local) and returns a host-reachable "host:port" for an in-venue TCP port, registering
// any opened ssh -L forward (VM/ssh venue) for post-Invoke teardown. Empty addr + nil err
// = no live venue (box-mode / no-box) — the verb's own no-endpoint skip then fires.
func (r *Runner) resolveVerbEndpoint(port int) (string, error) {
	if r.Box == "" || r.Mode == RunModeBox {
		return "", nil
	}
	venue, err := resolveCheckVenue(r.Box, r.Instance)
	if err != nil {
		return "", err
	}
	ep, err := resolveCheckEndpoint(venue, port)
	if err != nil {
		return "", err
	}
	r.endpointCleanups = append(r.endpointCleanups, ep.Close)
	return ep.Addr, nil
}

// graphicsEndpoint is the host-resolved dialable VM graphics endpoint (the in-proc twin of
// kit.GraphicsEndpoint). Exactly one of Addr / Socket is set; Skip=true means no such device.
type graphicsEndpoint struct {
	Addr, Socket, Password string
	Skip                   bool
	SkipMessage            string
}

// resolveVerbGraphics resolves a VM's <graphics type='<kind>'> listener (kind = "vnc" |
// "spice") to a dialable endpoint via the out-of-process vm plugin (resolve-<kind>), opening
// (and registering for post-Invoke teardown) any qemu+ssh:// tunnel. REPLACES the per-verb
// spice host preresolver. Empty endpoint + nil err = no live VM context (box-mode /
// no-box); Skip=true = the deployment declares no graphics device of that kind (an N/A skip).
func (r *Runner) resolveVerbGraphics(kind string) (graphicsEndpoint, error) {
	if r.Box == "" || r.Mode == RunModeBox {
		return graphicsEndpoint{}, nil
	}
	// CHARLY_LIBVIRT_URI selects a remote hypervisor, exactly as the former host preresolver did.
	raw, ok := invokeVmPlugin("resolve-"+kind, r.vmTargetName(), os.Getenv("CHARLY_LIBVIRT_URI"))
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

	// Local endpoint — hand back the direct socket/address, no tunnel.
	if !ep.TunnelNeeded {
		ge := graphicsEndpoint{Password: ep.Password}
		if ep.IsSocket {
			ge.Socket = ep.SocketPath
		} else {
			ge.Addr = fmt.Sprintf("%s:%d", ep.Host, ep.Port)
		}
		return ge, nil
	}

	// Remote (qemu+ssh://) — open an SSH tunnel forwarding the endpoint to a local address;
	// register the teardown on the Runner (drained after the verb's Invoke, since the tunnel
	// carries the live connection).
	parsed, perr := ParseLibvirtURI(rr.TunnelTarget)
	if perr != nil {
		return graphicsEndpoint{}, perr
	}
	tunnel, terr := NewSSHTunnel(parsed.Remote)
	if terr != nil {
		return graphicsEndpoint{}, fmt.Errorf("ssh tunnel to %s: %w", parsed.Remote, terr)
	}
	r.endpointCleanups = append(r.endpointCleanups, func() { _ = tunnel.Close() })
	if ep.IsSocket {
		localSock, _, ferr := tunnel.ForwardUnix(context.Background(), ep.SocketPath)
		if ferr != nil {
			return graphicsEndpoint{}, fmt.Errorf("forwarding remote socket %s: %w", ep.SocketPath, ferr)
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
func (r *Runner) runEndpointCleanups() {
	for i := len(r.endpointCleanups) - 1; i >= 0; i-- {
		if r.endpointCleanups[i] != nil {
			r.endpointCleanups[i]()
		}
	}
	r.endpointCleanups = nil
}

// ResolveEndpoint is the in-process CheckContext leg — a compiled-in kit verb reaches the
// SAME generic endpoint resolution an out-of-process one gets over CheckContextService.
func (c runnerCheckContext) ResolveEndpoint(_ context.Context, port int) (string, error) {
	return c.r.resolveVerbEndpoint(port)
}

// ResolveGraphicsEndpoint is the in-process CheckContext leg for VM graphics endpoints.
func (c runnerCheckContext) ResolveGraphicsEndpoint(_ context.Context, kind string) (kit.GraphicsEndpoint, error) {
	ge, err := c.r.resolveVerbGraphics(kind)
	if err != nil {
		return kit.GraphicsEndpoint{}, err
	}
	return kit.GraphicsEndpoint{Addr: ge.Addr, Socket: ge.Socket, Password: ge.Password, Skip: ge.Skip, SkipMessage: ge.SkipMessage}, nil
}

package main

import (
	"context"

	"github.com/opencharly/sdk/deploykit"
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
	addr, cleanup, err := resolveVerbEndpointFor(h.kr.Box(), h.kr.Instance(), h.kr.Mode(), port)
	if cleanup != nil {
		h.endpointCleanups = append(h.endpointCleanups, cleanup)
	}
	return addr, err
}

// resolveVerbEndpointFor is resolveVerbEndpoint's box/instance/mode-parameterized core (K1-unblock
// W3 Unit B, R3 extraction): the SAME resolution, usable by any caller that has box/instance/mode
// scalars but no live *kit.Runner — specifically charly/plugin_dispatch_reverse.go's InvokeProvider
// detached CheckContext for a CheckVerbProvider target dispatched from a plugin. Returns the
// opened endpoint's cleanup func (nil when none was opened) for the caller's OWN cleanup-list
// bookkeeping — hostVerbResolver's list, or the detached context's own.
func resolveVerbEndpointFor(box, instance string, mode RunMode, port int) (addr string, cleanup func(), err error) {
	if box == "" || mode == RunModeBox {
		return "", nil, nil
	}
	venue, err := resolveCheckVenue(box, instance)
	if err != nil {
		return "", nil, err
	}
	ep, err := resolveCheckEndpoint(venue, port)
	if err != nil {
		return "", nil, err
	}
	return ep.Addr, ep.Close, nil
}

// graphicsEndpoint is the host-resolved dialable VM graphics endpoint (the in-proc twin of
// kit.GraphicsEndpoint). Exactly one of Addr / Socket is set; Skip=true means no such device.
type graphicsEndpoint struct {
	Addr, Socket, Password string
	Skip                   bool
	SkipMessage            string
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
	return resolveImageLabelFor(h.kr.Box(), h.kr.Instance(), h.kr.Mode(), label)
}

// resolveImageLabelFor is resolveImageLabel's box/instance/mode-parameterized core (K1-unblock W3
// Unit B, R3 extraction) — see resolveVerbEndpointFor's doc comment for the shared rationale.
func resolveImageLabelFor(box, instance string, mode RunMode, label string) (string, error) {
	if box == "" || mode == RunModeBox {
		return "", nil
	}
	engine, containerName, err := deploykit.ResolveContainer(box, instance)
	if err != nil {
		return "", err
	}
	imageRef, err := kit.ContainerImageRef(engine, containerName)
	if err != nil {
		return "", err
	}
	labels, err := kit.InspectImageLabels(engine, imageRef)
	if err != nil {
		return "", err
	}
	return labels[label], nil
}

// ResolveImageLabel is the in-process CheckContext leg for a baked OCI label.
func (c hostCheckContext) ResolveImageLabel(_ context.Context, label string) (string, error) {
	return c.h.resolveImageLabel(label)
}

// resolveVNCPassword resolves a deployment's VNC ticket from the credential store (the
// VNC_PASSWORD env override first, then the instance-specific then image-level key). It stays
// HOST-side (folded from vnc_helpers.go, FLOOR-SLIM Unit 4) — the out-of-process plugin cannot
// reach the credential store; resolveVerbGraphics hands the resolved password to the plugin via
// the reverse-leg reply. wayvnc auth itself is provisioned at DEPLOY time (the wayvnc /
// sway-desktop-vnc candy), not by the check verb.
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

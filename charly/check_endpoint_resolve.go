package main

import (
	"context"
	"fmt"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// check_endpoint_resolve.go — the generic host-endpoint reverse-legs (H part 2), SHRUNK to the
// fixed reverse-RPC SERVICE surface (#55 W3 B7): the ACTUAL resolution work (venue-classify +
// checkhost.EndpointForVenue / container image-label read) relocated to candy/plugin-check
// (resolve_endpoint.go), compiled-in-REQUIRED placement class — see that file's header for the
// full placement rationale. The one genuine constraint that used to make this file's resolution
// bodies core-only (a live ssh -L forward's cleanup closure) turned out not to actually depend on
// core placement at all: a live closure cannot cross ANY Invoke wire, compiled-in or not (the wire
// is JSON bytes only — charly/provider.go's Operation.Params/Result), so the plugin now tracks its
// OWN pending-cleanup state and closes it on a separate drain signal, rather than the closure ever
// needing to cross back to core.
//
// What remains here:
//   - resolveVerbEndpointFor/resolveImageLabelFor — now thin wire forwards to verb:check-resolve's
//     OpResolveEndpoint/OpResolveImageLabel, mirroring resolveCheckVenueReply's (already-existing)
//     resolve+invoke shape.
//   - the CheckContext interface methods (ResolveEndpoint/ResolveGraphicsEndpoint/
//     ResolveImageLabel) — the fixed reverse-RPC service surface every out-of-process
//     live-container verb (cdp/wl/vnc/dbus/mcp) dials back into; genuinely core-only, since
//     hostVerbResolver.RunVerb wraps providerRegistry.ResolveVerb, a core-private M-mechanism no
//     out-of-process dispatch can bypass.
//   - runEndpointCleanups — now drains hostVerbResolver's OWN list (still populated by
//     check_graphics_endpoint.go's resolveVerbGraphics, UNCHANGED permanent STAY — a genuinely
//     different live-handle constraint, x/crypto/ssh containment, see that file's header) AND
//     signals candy/plugin-check to drain ITS OWN pending list (OpDrainEndpointCleanups) —
//     the SAME per-Invoke ordering guarantee (open during resolve, drained after the verb's
//     Invoke returns) the former single-list design carried, now split across two owners that
//     both fire from this ONE call site.

// resolveVerbEndpointFor resolves a check target's venue-relative TCP port into a host-reachable
// "host:port" via verb:check-resolve's OpResolveEndpoint leg (#55 W3 B7 — the resolution WORK
// moved to candy/plugin-check/resolve_endpoint.go; this is a thin wire forward). Any opened
// forward is tracked plugin-side, closed on the LATER drainVerbEndpointCleanups call this same
// Invoke's runEndpointCleanups makes — never returned here.
func resolveVerbEndpointFor(box, instance string, mode spec.CheckRunMode, port int) (addr string, err error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "check-resolve")
	if !ok {
		return "", fmt.Errorf("check-resolve: plugin-check (verb:check-resolve) not registered — charly built without the plugin-check candy")
	}
	ctx := specexec.ContextWithExecutor(context.Background(), specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	reply, err := invokeTyped[spec.CheckEndpointResolveRequest, spec.CheckEndpointResolveReply](
		ctx, prov, "check-resolve", ops.OpResolveEndpoint,
		spec.CheckEndpointResolveRequest{Box: box, Instance: instance, Mode: runModeName(mode), Port: port})
	if err != nil {
		return "", err
	}
	return reply.Addr, nil
}

// resolveImageLabelFor reads one raw OCI label off a check target's live image via
// verb:check-resolve's OpResolveImageLabel leg (#55 W3 B7 — thin wire forward, the resolution
// WORK moved to candy/plugin-check/resolve_endpoint.go).
func resolveImageLabelFor(box, instance string, mode spec.CheckRunMode, label string) (string, error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "check-resolve")
	if !ok {
		return "", fmt.Errorf("check-resolve: plugin-check (verb:check-resolve) not registered — charly built without the plugin-check candy")
	}
	ctx := specexec.ContextWithExecutor(context.Background(), specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	reply, err := invokeTyped[spec.CheckImageLabelResolveRequest, spec.CheckImageLabelResolveReply](
		ctx, prov, "check-resolve", ops.OpResolveImageLabel,
		spec.CheckImageLabelResolveRequest{Box: box, Instance: instance, Mode: runModeName(mode), Label: label})
	if err != nil {
		return "", err
	}
	return reply.Value, nil
}

// drainVerbEndpointCleanups signals candy/plugin-check to close every forward its
// OpResolveEndpoint leg opened since the last drain (#55 W3 B7) — the plugin-side twin of the
// FORMER whole-list core-side drain, now scoped to only the endpoint leg's own tracked forwards
// (check_graphics_endpoint.go's own entries stay in hostVerbResolver.endpointCleanups, drained
// separately by runEndpointCleanups in the SAME call).
func drainVerbEndpointCleanups() {
	prov, ok := providerRegistry.resolve(ClassVerb, "check-resolve")
	if !ok {
		return
	}
	ctx := specexec.ContextWithExecutor(context.Background(), specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	_, _ = invokeTyped[spec.CheckDrainEndpointCleanupsRequest, struct{}](
		ctx, prov, "check-resolve", ops.OpDrainEndpointCleanups, spec.CheckDrainEndpointCleanupsRequest{})
}

// resolveVerbEndpoint resolves the current check target's venue (container / VM / ssh /
// local) and returns a host-reachable "host:port" for an in-venue TCP port. Empty addr + nil err
// = no live venue (box-mode / no-box) — the verb's own no-endpoint skip then fires.
func (h *hostVerbResolver) resolveVerbEndpoint(port int) (string, error) {
	return resolveVerbEndpointFor(h.cc.Box(), h.cc.Instance(), h.cc.Mode(), port)
}

// graphicsEndpoint is the host-resolved dialable VM graphics endpoint (the in-proc twin of
// spec.CheckGraphicsEndpoint). Exactly one of Addr / Socket is set; Skip=true means no such device.
type graphicsEndpoint struct {
	Addr, Socket, Password string
	Skip                   bool
	SkipMessage            string
}

// runEndpointCleanups closes every forward opened during the verb's Invoke: hostVerbResolver's
// OWN list (LIFO — check_graphics_endpoint.go's resolveVerbGraphics entries, unchanged) AND
// candy/plugin-check's tracked endpoint-leg forwards (via the drain signal, #55 W3 B7). Called by
// invokeVerbProvider after the Invoke returns — the forward must outlive the plugin's dial.
func (h *hostVerbResolver) runEndpointCleanups() {
	for i := len(h.endpointCleanups) - 1; i >= 0; i-- {
		if h.endpointCleanups[i] != nil {
			h.endpointCleanups[i]()
		}
	}
	h.endpointCleanups = nil
	drainVerbEndpointCleanups()
}

// ResolveEndpoint is the in-process CheckContext leg — a compiled-in kit verb reaches the
// SAME generic endpoint resolution an out-of-process one gets over CheckContextService.
func (c hostCheckContext) ResolveEndpoint(_ context.Context, port int) (string, error) {
	return c.h.resolveVerbEndpoint(port)
}

// ResolveGraphicsEndpoint is the in-process CheckContext leg for VM graphics endpoints.
func (c hostCheckContext) ResolveGraphicsEndpoint(_ context.Context, kind string) (spec.CheckGraphicsEndpoint, error) {
	ge, err := c.h.resolveVerbGraphics(kind)
	if err != nil {
		return spec.CheckGraphicsEndpoint{}, err
	}
	return spec.CheckGraphicsEndpoint{Addr: ge.Addr, Socket: ge.Socket, Password: ge.Password, Skip: ge.Skip, SkipMessage: ge.SkipMessage}, nil
}

// resolveImageLabel reads one raw OCI label off the deployment-under-test's image. It is the
// host-side leg for CheckContext.ResolveImageLabel — the out-of-process mcp verb needs the
// baked ai.opencharly.mcp_provide label but cannot reach the podman engine / OCI metadata.
// Empty value (no live deployment, or the label absent) is a valid result.
func (h *hostVerbResolver) resolveImageLabel(label string) (string, error) {
	return resolveImageLabelFor(h.cc.Box(), h.cc.Instance(), h.cc.Mode(), label)
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

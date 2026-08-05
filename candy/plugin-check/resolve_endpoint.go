package check

// resolve_endpoint.go — the resolution BODY behind charly core's fixed CheckContext.ResolveEndpoint
// / CheckContext.ResolveImageLabel reverse-RPC surface (#55 W3 B7). The reverse-RPC SERVICE itself
// stays core (charly/check_endpoint_resolve.go's hostVerbResolver wraps the core-private
// verb-dispatch registry no out-of-process live-container verb — cdp/wl/vnc/dbus/mcp — can bypass);
// only the RESOLUTION WORK relocates here, compiled-in-REQUIRED placement class (bed_session.go's
// precedent, #55 W3 B2-full): venue.go's resolveCheckVenue/resolveCheckEndpoint already carry this
// package's OWN venue-classification + endpoint-resolution logic (built for the check-live gather
// engine), reused here directly (R3 — one resolver, not two) rather than round-tripping back out to
// this SAME provider's own OpResolve leg over the wire.
//
// The one genuine constraint driving this file's shape: a live ssh -L forward's cleanup closure
// (checkhost.EndpointForVenue's ssh-venue branch) cannot cross ANY Invoke boundary — compiled-in or
// not, the wire is JSON bytes only (spec.Operation.Params/Result — see charly/provider.go). So the
// closure is tracked HERE, in this package's own per-Invoke pending-cleanup state, never returned on
// the wire; charly core signals "close them now" (after the verb's own Invoke completes, the SAME
// point the former core-side h.runEndpointCleanups() fired) via #CheckDrainEndpointCleanupsRequest.
// Sequential per-Invoke lifecycle: the host resets/drains around exactly ONE verb dispatch at a
// time (provider_checkenv.go's h.endpointCleanups = nil / defer h.runEndpointCleanups() bracket,
// unchanged core-side), so a single package-level pending list here carries the same guarantee
// without needing a session id.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/container"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// pendingEndpointCleanups holds the forwards opened by resolveEndpointForHost DURING the current
// verb's Invoke; drainEndpointCleanupsForHost closes them (LIFO) when the host signals the verb's
// Invoke has completed. Guarded by a mutex defensively (compiled-in placement means this runs in
// charly's own process, but Invoke handlers are not otherwise guaranteed single-goroutine).
var (
	pendingEndpointCleanupsMu sync.Mutex
	pendingEndpointCleanups   []func()
)

// resolveEndpointForHost serves one verb:check-resolve OpResolveEndpoint: resolve box/instance's
// venue and return a host-reachable "host:port" for an in-venue TCP port, tracking any opened
// forward for the LATER drain signal. Empty addr + nil err = no live venue (box-mode / no-box) —
// the calling verb's own no-endpoint skip then fires, mirroring the former core-side
// resolveVerbEndpointFor exactly.
func resolveEndpointForHost(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in spec.CheckEndpointResolveRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-check: decode check-endpoint-resolve request: %w", err)
		}
	}
	if in.Box == "" || in.Mode == "box" {
		return marshalEndpointReply(spec.CheckEndpointResolveReply{})
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-check: check-endpoint-resolve reverse-channel executor: %w", err)
	}
	dir, _ := os.Getwd()
	venue, err := resolveCheckVenue(exec, ctx, dir, in.Box, in.Instance)
	if err != nil {
		return nil, err
	}
	ep, err := resolveCheckEndpoint(venue, in.Port)
	if err != nil {
		return nil, err
	}
	// ep.Close is a nil-safe method (a no-op when the endpoint opened no live forward) — always
	// track it, matching the former core-side unconditional h.endpointCleanups append.
	pendingEndpointCleanupsMu.Lock()
	pendingEndpointCleanups = append(pendingEndpointCleanups, ep.Close)
	pendingEndpointCleanupsMu.Unlock()
	return marshalEndpointReply(spec.CheckEndpointResolveReply{Addr: ep.Addr})
}

func marshalEndpointReply(reply spec.CheckEndpointResolveReply) (*pb.InvokeReply, error) {
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("plugin-check: marshal check-endpoint-resolve reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// resolveImageLabelForHost serves one verb:check-resolve OpResolveImageLabel: read one raw OCI
// label off box/instance's live image. Mirrors the former core-side resolveImageLabelFor exactly —
// guarded to the plain (non-nested) container venue, since a non-container or dotted-nested name
// has no podman-inspectable image label. Empty value (no live deployment, or the label absent) is a
// valid result, not an error.
func resolveImageLabelForHost(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in spec.CheckImageLabelResolveRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-check: decode check-image-label-resolve request: %w", err)
		}
	}
	if in.Box == "" || in.Mode == "box" {
		return marshalImageLabelReply(spec.CheckImageLabelResolveReply{})
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-check: check-image-label-resolve reverse-channel executor: %w", err)
	}
	dir, _ := os.Getwd()
	venue, err := resolveCheckVenue(exec, ctx, dir, in.Box, in.Instance)
	if err != nil {
		return nil, err
	}
	if !venue.IsContainer() || strings.Contains(in.Box, ".") {
		return nil, fmt.Errorf("container for %s is not running", in.Box)
	}
	imageRef, err := container.ContainerImageRef(venue.Engine, venue.Name)
	if err != nil {
		return nil, err
	}
	labels, err := container.InspectImageLabels(venue.Engine, imageRef)
	if err != nil {
		return nil, err
	}
	return marshalImageLabelReply(spec.CheckImageLabelResolveReply{Value: labels[in.Label]})
}

func marshalImageLabelReply(reply spec.CheckImageLabelResolveReply) (*pb.InvokeReply, error) {
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("plugin-check: marshal check-image-label-resolve reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// drainEndpointCleanupsForHost serves one verb:check-resolve OpDrainEndpointCleanups: close every
// forward tracked since the last drain (LIFO) and reset the pending list — the plugin-side twin of
// the former core-side hostVerbResolver.runEndpointCleanups.
func drainEndpointCleanupsForHost(context.Context, *pb.InvokeRequest) (*pb.InvokeReply, error) {
	pendingEndpointCleanupsMu.Lock()
	toClose := pendingEndpointCleanups
	pendingEndpointCleanups = nil
	pendingEndpointCleanupsMu.Unlock()
	for i := len(toClose) - 1; i >= 0; i-- {
		if toClose[i] != nil {
			toClose[i]()
		}
	}
	return &pb.InvokeReply{}, nil
}

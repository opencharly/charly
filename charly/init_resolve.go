package main

// init_resolve.go — the HOST side of the `init` kind's config-resolve leg, colocated here from
// the deleted charly/service_render.go (K-wave 2 cone R2). candy/plugin-init's ops.OpResolve
// projects one opaque init body into a *spec.ResolvedInit (legs 2–4 value envelope); the build
// engine consumes it, never the raw body. Same kind-dispatch callback wrapper class as
// resource_resolve.go/distro_resolve.go — the SAME hostInvoke/registry kind-dispatch mechanism
// (leg 2), reached by loader_threaded.go's spec.ProjectInitConfig accessor.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/spec/ops"

	"github.com/opencharly/spec/spec"
)

// resolveInitConfigViaPlugin invokes candy/plugin-init's ops.OpResolve config leg, projecting one
// opaque init body into a *spec.ResolvedInit (legs 2–4 value envelope).
func resolveInitConfigViaPlugin(body json.RawMessage) (*spec.ResolvedInit, error) {
	out, err := invokeInitResolve(spec.InitResolveRequest{Config: &spec.InitResolveInput{Init: body}})
	if err != nil {
		return nil, err
	}
	var reply spec.InitResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("init resolve config: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// invokeInitResolve dispatches an ops.OpResolve request to the compiled-in init kind provider
// (both legs share it).
func invokeInitResolve(req spec.InitResolveRequest) ([]byte, error) {
	prov, ok := providerRegistry.ResolveKind("init")
	if !ok {
		return nil, fmt.Errorf("init resolve: kind provider not registered")
	}
	return invokeTyped[spec.InitResolveRequest, json.RawMessage](context.Background(), prov, "init", ops.OpResolve, req)
}

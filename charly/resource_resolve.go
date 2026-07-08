package main

// resource_resolve.go — the HOST side of the `resource` kind after the resource
// de-type (Cutover G). candy/plugin-resource's OpResolve projects an authored
// resource into a ResolvedResource; the GPU arbiter consumes it, never spec.Resource.

import (
	"encoding/json"

	"github.com/opencharly/sdk/spec"
)

// ResolvedResource / ResolvedGpuSelector are the resource de-type's value envelopes.
type (
	ResolvedResource    = spec.ResolvedResource
	ResolvedGpuSelector = spec.ResolvedGpuSelector
)

// resolveResources projects uf.PluginKinds["resource"] (opaque bodies) into
// *ResolvedResource envelopes via candy/plugin-resource's OpResolve leg. A bad
// entry is skipped rather than poisoning the vocabulary (cf. decodePluginKindMap).
func (uf *UnifiedFile) resolveResources() map[string]*ResolvedResource {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds["resource"]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*ResolvedResource, len(bodies))
	for name, body := range bodies {
		rr, err := resolveResourceViaPlugin(body)
		if err != nil || rr == nil {
			continue
		}
		out[name] = rr
	}
	return out
}

func resolveResourceViaPlugin(body json.RawMessage) (*ResolvedResource, error) {
	reply, err := hostInvoke[spec.ResourceResolveInput, spec.ResourceResolveReply](ClassKind, "resource", OpResolve, spec.ResourceResolveInput{Resource: body})
	if err != nil {
		return nil, err
	}
	return reply.Resolved, nil
}

package main

// resource_resolve.go — the HOST side of the `resource` kind after the resource
// de-type (Cutover G). candy/plugin-resource's OpResolve projects an authored
// resource into a ResolvedResource; the GPU arbiter consumes it, never spec.Resource.

import (
	"context"
	"encoding/json"
	"fmt"

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
	prov, ok := providerRegistry.ResolveKind("resource")
	if !ok {
		return nil, fmt.Errorf("resource resolve: kind provider not registered")
	}
	paramsJSON, err := json.Marshal(spec.ResourceResolveInput{Resource: body})
	if err != nil {
		return nil, fmt.Errorf("resource resolve: marshal input: %w", err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: "resource", Op: OpResolve, Params: json.RawMessage(paramsJSON)})
	if err != nil {
		return nil, err
	}
	var reply spec.ResourceResolveReply
	if out != nil && len(out.JSON) > 0 {
		if err := json.Unmarshal(out.JSON, &reply); err != nil {
			return nil, fmt.Errorf("resource resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

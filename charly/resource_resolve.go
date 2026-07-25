package main

// resource_resolve.go — the HOST side of the `resource` kind after the resource
// de-type (Cutover G). candy/plugin-resource's OpResolve projects an authored
// resource into a ResolvedResource; the GPU arbiter consumes it, never spec.Resource.

import (
	"encoding/json"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// ResolvedResource / ResolvedGpuSelector are the resource de-type's value envelopes.
type (
	ResolvedResource    = spec.ResolvedResource
	ResolvedGpuSelector = spec.ResolvedGpuSelector
)

// resolveResources projects uf.PluginKinds["resource"] (opaque bodies) into
// *ResolvedResource envelopes via candy/plugin-resource's OpResolve leg
// (loaderkit.ResolvePluginKindViaPlugin — the shared loop every plugin-resolved kind
// accessor uses).
func resolveResources(uf *loaderkit.UnifiedFile) map[string]*ResolvedResource {
	return loaderkit.ResolvePluginKindViaPlugin(uf, "resource", resolveResourceViaPlugin)
}

func resolveResourceViaPlugin(body json.RawMessage) (*ResolvedResource, error) {
	reply, err := hostInvoke[spec.ResourceResolveInput, spec.ResourceResolveReply](ClassKind, "resource", OpResolve, spec.ResourceResolveInput{Resource: body})
	if err != nil {
		return nil, err
	}
	return reply.Resolved, nil
}

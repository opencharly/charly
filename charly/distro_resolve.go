package main

// distro_resolve.go — the HOST side of the `distro` build vocabulary after the distro
// de-type (Cutover M, the long pole). candy/plugin-distro's OpResolve projects an
// authored distro into a ResolvedDistro (= DistroDef); the build engine consumes it,
// never spec.Distro. The host keeps RenderTemplate + the cache-mount vocab.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// resolveDistroViaPlugin projects one opaque distro body into a *DistroDef
// (= *spec.ResolvedDistro) via candy/plugin-distro's OpResolve leg.
func resolveDistroViaPlugin(body json.RawMessage) (*DistroDef, error) {
	prov, ok := providerRegistry.ResolveKind("distro")
	if !ok {
		return nil, fmt.Errorf("distro resolve: kind provider not registered")
	}
	paramsJSON, err := json.Marshal(spec.DistroResolveInput{Distro: body})
	if err != nil {
		return nil, fmt.Errorf("distro resolve: marshal input: %w", err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: "distro", Op: OpResolve, Params: json.RawMessage(paramsJSON)})
	if err != nil {
		return nil, err
	}
	var reply spec.DistroResolveReply
	if out != nil && len(out.JSON) > 0 {
		if err := json.Unmarshal(out.JSON, &reply); err != nil {
			return nil, fmt.Errorf("distro resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// resolveDistros projects uf.PluginKinds["distro"] (opaque bodies) into *DistroDef
// envelopes via the plugin. A bad entry is skipped rather than poisoning the
// vocabulary (cf. decodePluginKindMap).
func (uf *UnifiedFile) resolveDistros() map[string]*DistroDef {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds["distro"]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*DistroDef, len(bodies))
	for name, body := range bodies {
		d, err := resolveDistroViaPlugin(body)
		if err != nil || d == nil {
			continue
		}
		out[name] = d
	}
	return out
}

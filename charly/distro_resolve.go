package main

// distro_resolve.go — the HOST side of the `distro` build vocabulary after the distro
// de-type (Cutover M, the long pole). candy/plugin-distro's OpResolve projects an
// authored distro into a ResolvedDistro (= DistroDef); the build engine consumes it,
// never spec.Distro. The host keeps RenderTemplate + the cache-mount vocab.

import (
	"encoding/json"

	"github.com/opencharly/sdk/spec"
)

// resolveDistroViaPlugin projects one opaque distro body into a *DistroDef
// (= *spec.ResolvedDistro) via candy/plugin-distro's OpResolve leg.
func resolveDistroViaPlugin(body json.RawMessage) (*spec.ResolvedDistro, error) {
	reply, err := hostInvoke[spec.DistroResolveInput, spec.DistroResolveReply](ClassKind, "distro", OpResolve, spec.DistroResolveInput{Distro: body})
	if err != nil {
		return nil, err
	}
	return reply.Resolved, nil
}

// resolveDistros projects uf.PluginKinds["distro"] (opaque bodies) into *DistroDef
// envelopes via the plugin. A bad entry is skipped rather than poisoning the
// vocabulary (cf. decodePluginKindMap).
func (uf *UnifiedFile) resolveDistros() map[string]*spec.ResolvedDistro {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds["distro"]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*spec.ResolvedDistro, len(bodies))
	for name, body := range bodies {
		d, err := resolveDistroViaPlugin(body)
		if err != nil || d == nil {
			continue
		}
		out[name] = d
	}
	return out
}

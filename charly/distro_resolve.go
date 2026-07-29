package main

// distro_resolve.go — the HOST side of the `distro` build vocabulary after the distro
// de-type (Cutover M, the long pole). candy/plugin-distro's OpResolve projects an
// authored distro into a ResolvedDistro (= DistroDef); the build engine consumes it,
// never spec.Distro. The host keeps RenderTemplate + the cache-mount vocab.

import (
	"encoding/json"

	"github.com/opencharly/spec/spec"
)

// resolveDistroViaPlugin projects one opaque distro body into a *DistroDef
// (= *spec.ResolvedDistro) via candy/plugin-distro's OpResolve leg. Consumed by
// Distros(uf) (unified.go) via spec.ResolvePluginKindViaPlugin — the shared loop every
// plugin-resolved kind accessor uses.
func resolveDistroViaPlugin(body json.RawMessage) (*spec.ResolvedDistro, error) {
	reply, err := hostInvoke[spec.DistroResolveInput, spec.DistroResolveReply](ClassKind, "distro", OpResolve, spec.DistroResolveInput{Distro: body})
	if err != nil {
		return nil, err
	}
	return reply.Resolved, nil
}

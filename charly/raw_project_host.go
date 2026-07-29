package main

import (
	"context"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// raw_project_host.go — the CHEAP raw-loader HostBuild seam ("raw-project"), the endgame keystone
// that lets the loader-coupled deploy files MOVE to their plugins (the ruling: loader-coupling is the
// work, not a defer reason). It is the generic kind-blind host-resident half of a broker seam serving
// ANY consumer — exactly the class as resolved_project_host.go, but WITHOUT the expensive
// ResolveBox-per-box cost. A plugin that needs only the RAW loader reads (kind templates, the folded
// deploy tree with stamped Descent, the plugin-primaries D-fact) fetches this via
// Executor.HostBuild("raw-project") instead of paying the full box resolution resolved-project pays.
//
// It is a pure DATA PROJECTION over LoadUnified — NOT a new engine: loaderkit.ProjectTemplates (a
// cheap raw-byte template copy), the folded uf.Bundle deploy tree (already Descent-stamped by
// LoadUnified), and loaderThreaded().Primaries. Kind-blind throughout: templates/deploy carry OPAQUE
// bytes the consuming PLUGIN decodes into concrete kinds itself (a plugin may know kinds, the kernel
// may not). Additive — later fields (config defaults, etc.) join as the consumer unit that first
// needs them lands, the SAME pattern resolved-project uses.

// rawProjectBuilderKind is the F11 hostBuilders key — a generic action noun, never a provider word.
const rawProjectBuilderKind = "raw-project"

func hostBuildRawProject(_ context.Context, req spec.RawProjectRequest, _ buildEngineContext) (spec.RawProject, error) {
	dir := req.Dir
	if dir == "" {
		dir = "."
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return spec.RawProject{}, err
	}
	rp := spec.RawProject{Primaries: loaderThreaded().Primaries}
	if uf != nil {
		rp.Templates = loaderkit.ProjectTemplates(uf)
		if len(uf.Bundle) > 0 {
			rp.Deploy = make(map[string]*spec.Deploy, len(uf.Bundle))
			for k, v := range uf.Bundle {
				vv := v
				rp.Deploy[k] = &vv
			}
		}
	}
	return rp, nil
}

func init() {
	registerHostBuilder(rawProjectBuilderKind, typedHostBuilder(rawProjectBuilderKind, hostBuildRawProject))
}

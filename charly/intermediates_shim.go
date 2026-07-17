package main

import (
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// intermediates_shim.go — W3 (K3 build-engine move). The auto-intermediate-image
// subsystem is now ENTIRELY in sdk/deploykit: deploykit/intermediates.go carries
// the pure candy-graph/trie half (moved in P8b), and
// deploykit/intermediates_compute.go now ALSO carries the former HOST-COUPLED
// half (ComputeIntermediates/processSiblingGroup/createIntermediate/
// walkTrieScoped/pickAutoName/resolvePlatforms/distroBuilderMap — formerly
// charly/intermediates.go, which read *Config directly). This file keeps ONLY
// the thin core entry point that lifts cfg.Defaults into a
// deploykit.IntermediateDefaults value + the GlobalCandyOrder wrapper still
// used by generate.go/resolved_project_host.go. Mirrors graph_shim.go.

// ComputeIntermediates computes auto-generated intermediate images. It lifts
// cfg.Defaults into a deploykit.IntermediateDefaults (the scalar fields the
// relocated ComputeIntermediates needs) and delegates entirely to
// deploykit.ComputeIntermediates — no host callback remains.
func ComputeIntermediates(boxes map[string]*buildkit.ResolvedBox, layers map[string]spec.CandyReader, cfg *Config, tag string) (map[string]*buildkit.ResolvedBox, error) {
	defaults := deploykit.IntermediateDefaults{
		Builder:   buildkit.BuilderMap(cfg.Defaults.Builder),
		UID:       cfg.Defaults.UID,
		User:      cfg.Defaults.User,
		GID:       cfg.Defaults.GID,
		Merge:     cfg.Defaults.Merge,
		Registry:  cfg.Defaults.Registry,
		Platforms: cfg.Defaults.Platforms,
		Distro:    cfg.Defaults.Distro,
		Build:     cfg.Defaults.Build,
	}
	return deploykit.ComputeIntermediates(boxes, layers, defaults, tag)
}

// GlobalCandyOrder computes the global topological candy order (deploykit) over
// the scanned candy map held by generate.go / validate.go.
func GlobalCandyOrder(boxes map[string]*buildkit.ResolvedBox, layers map[string]spec.CandyReader) ([]string, error) {
	return deploykit.GlobalCandyOrder(boxes, layers)
}

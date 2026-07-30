package main

import (
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/spec/spec"
)

// resolveBoxTest / resolveAllBoxTest mirror the former config.go ResolveBox / ResolveAllBox free-function
// wrappers (DELETED in K3 U7). #55 Cluster-B: production callers reach the PURE buildkit resolve via the
// deploykit box-resolve bridge (deploykit.ResolveSpecBox/ResolveAllSpecBoxes, spec-typed); tests keep this
// buildkit-typed one-line convenience over the test-local vocab projector testBkOpts.
func resolveBoxTest(cfg *Config, name, calver, dir string, opts spec.ResolveOpts) (*buildkit.ResolvedBox, error) {
	bkopts, err := testBkOpts(dir, opts)
	if err != nil {
		return nil, err
	}
	return buildkit.ResolveBox(cfg, name, calver, dir, bkopts)
}

func resolveAllBoxTest(cfg *Config, dir string, opts spec.ResolveOpts) (map[string]*buildkit.ResolvedBox, error) {
	bkopts, err := testBkOpts(dir, opts)
	if err != nil {
		return nil, err
	}
	return buildkit.ResolveAllBox(cfg, "test", dir, bkopts)
}

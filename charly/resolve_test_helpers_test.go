package main

import "github.com/opencharly/sdk/buildkit"

// resolveBoxTest / resolveAllBoxTest mirror the former config.go ResolveBox / ResolveAllBox free-function
// wrappers (DELETED in K3 U7 — production callers now reach the PURE buildkit.ResolveBox /
// buildkit.ResolveAllBox directly over buildkitOptsWithVocab). Tests keep the one-line convenience here.
func resolveBoxTest(cfg *Config, name, calver, dir string, opts ResolveOpts) (*buildkit.ResolvedBox, error) {
	bkopts, err := buildkitOptsWithVocab(dir, opts)
	if err != nil {
		return nil, err
	}
	return buildkit.ResolveBox(cfg, name, calver, dir, bkopts)
}

func resolveAllBoxTest(cfg *Config, calver, dir string, opts ResolveOpts) (map[string]*buildkit.ResolvedBox, error) {
	bkopts, err := buildkitOptsWithVocab(dir, opts)
	if err != nil {
		return nil, err
	}
	return buildkit.ResolveAllBox(cfg, calver, dir, bkopts)
}

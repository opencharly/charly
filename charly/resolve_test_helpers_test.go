package main

import (
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/spec/spec"
)

// resolveBoxTest mirrors the former config.go ResolveBox free-function wrapper (DELETED in K3 U7).
// #55 Cluster-B: production callers reach the PURE buildkit resolve via the deploykit box-resolve
// bridge (deploykit.ResolveSpecBox, spec-typed); tests keep this buildkit-typed one-line convenience
// over the test-local vocab projector testBkOpts.
//
// resolveBoxTest drops the opts parameter the pre-decoupling-cone signature carried (#55 Batch B):
// every remaining caller passed spec.ResolveOpts{} (an unparam finding) once the varied-opts
// callers in config_test.go moved to candy/plugin-build/box_resolve_test.go, which bypasses
// testBkOpts's dir-based vocab lookup entirely. install_build_test.go is its only remaining charly
// caller (a different batch's file) — resolveAllBoxTest's own last charly callers moved to
// candy/plugin-build/namespace_resolve_test.go in this same cutover, so it was DELETED here (R5:
// zero callers = zero definition); this file stays present-but-minimal pending the terminus merge,
// when resolveBoxTest's last caller moves too and the file deletes entirely.
func resolveBoxTest(cfg *Config, name, calver, dir string) (*buildkit.ResolvedBox, error) {
	bkopts, err := testBkOpts(dir, spec.ResolveOpts{})
	if err != nil {
		return nil, err
	}
	return buildkit.ResolveBox(cfg, name, calver, dir, bkopts)
}

// testBkOpts reproduces the former core build-vocab resolve-opts projection (removed in #55
// Cluster-B — charly core no longer names buildkit.ResolveOpts): fill the build vocabulary via
// resolveVocabOpts, then project onto buildkit.ResolveOpts (a test MAY import buildkit; only
// non-test charly may not). Relocated here from the deleted resolved_project_host_test.go (#55
// decoupling cone, Batch B) — resolveBoxTest/resolveAllBoxTest above are its only remaining callers.
func testBkOpts(dir string, opts spec.ResolveOpts) (buildkit.ResolveOpts, error) {
	vopts, err := resolveVocabOpts(dir, opts)
	if err != nil {
		return buildkit.ResolveOpts{}, err
	}
	return buildkit.ResolveOpts{
		IncludeDisabled:      vopts.IncludeDisabled,
		IncludeDisabledNames: vopts.IncludeDisabledNames,
		RequestedBoxes:       vopts.RequestedBoxes,
		DistroCfg:            vopts.DistroCfg,
		BuilderCfg:           vopts.BuilderCfg,
	}, nil
}

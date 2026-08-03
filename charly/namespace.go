package main

import (
	"github.com/opencharly/spec/spec"
)

// namespace.go — the Go-inspired hierarchical-namespace resolver's LAST remaining charly-core
// piece (FLOOR-SLIM Unit 5, ownership transferred from fs-mech's K1 scope): resolveLocalRef.
//
// The `import:` statement (see unified.go) mounts another project under a child
// namespace (`import: [{cachyos: '@github.com/opencharly/distro-cachyos:vTAG'}]`).
// Entries in that project are then referenced QUALIFIED — `base: cachyos.cachyos`,
// `builder: {pixi: charly.arch-builder}` — rather than flat-merged into the importing
// project's global per-kind maps.
//
// Everything ELSE that used to live here (ResolveBoxRef, FindBoxByLeaf — spec-typed;
// resolveNamespacedBases, pullNamespacedBox — buildkit-typed) moved to sdk/spec
// (spec/config.go) and sdk/buildkit (buildkit/config_resolve.go) respectively, since
// spec.Config is a spec type — package main can never add another method to it (W0 deleted
// the former in-core `Config = spec.Config` alias too — every consumer reads spec.Config
// directly). Only resolveLocalRef stays: it calls resolveLocalViaPlugin
// (substrate_template_resolve.go), a registry-coupled, host-only dispatch to the substrate
// plugin — genuinely core, not a relocation candidate — so it becomes a FREE FUNCTION here
// instead of a Config method.

// resolveLocalRefFor resolves a (possibly qualified) kind:local template ref against cfg. A free
// function (not a spec.Config method — package main can add no methods to a spec type): callers
// changed from `cfg.resolveLocalRef(ref)` to `resolveLocalRefFor(cfg, ref)`.
func resolveLocalRefFor(cfg *spec.Config, ref string) (*spec.ResolvedLocal, bool) {
	if ns, rest, ok := spec.SplitNamespaceRef(ref); ok {
		sub, ok := cfg.Namespaces[ns]
		if !ok {
			return nil, false
		}
		return resolveLocalRefFor(sub, rest)
	}
	body, ok := cfg.Local[ref]
	if !ok {
		return nil, false
	}
	r, err := resolveLocalViaPlugin(body)
	if err != nil || r == nil {
		return nil, false
	}
	return r, true
}

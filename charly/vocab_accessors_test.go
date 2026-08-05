package main

// vocab_accessors_test.go — the four map-shaped spec.UnifiedFile accessors that had no production
// caller left, kept HERE as the test helpers they had already become.
//
// Distros / Builders / resolveInits read the per-kind build vocabulary back out of
// uf.PluginKinds; ProjectCandies is the finalize-wrapped form of projectCandiesScanned. Every
// production consumer moved on: the build-vocab CONFIG projections are loaderkit's
// Project{Distro,Builder,Init}Config (the ONE home charly core and candy/plugin-build both call,
// R3), reached via LoadBuildConfigForBox, and the candy scan runs through
// ScanAllCandyWithConfigOpts. charly/unified.go's own header already recorded these as "the
// map-shaped accessors the tests read" — a production file holding test-only code, which the
// residue ledger's radical dead-code rule says to move rather than keep.
//
// They are NOT deleted, because the tests that read them assert real behaviour (that the embedded
// vocabulary decodes, that discovery synthesizes the right candies). Moving them here keeps that
// coverage exercising the SAME mechanisms — spec.ResolvePluginKindViaPlugin /
// spec.DecodePluginKindMap with charly's in-proc registry OpResolve callbacks — while charly/
// production carries none of it.

import "github.com/opencharly/spec/spec"

// Distros reconstructs the name-keyed per-distro build vocabulary from uf.PluginKinds. The `distro`
// kind is a plugin kind (candy/plugin-distro), so a `distro:` node (including the binary-embedded
// vocabulary) lands in uf.PluginKinds["distro"][<name>] as an OPAQUE canonical body; this RESOLVES
// each body via that plugin's ops.OpResolve leg into a *spec.ResolvedDistro. A bad entry is skipped
// rather than poisoning the whole vocabulary.
func Distros(uf *spec.UnifiedFile) map[string]*spec.ResolvedDistro {
	return spec.ResolvePluginKindViaPlugin(uf, "distro", resolveDistroViaPlugin)
}

// Builders reconstructs the name-keyed multi-stage builder vocabulary from
// uf.PluginKinds["builder"] (the `builder` plugin kind, candy/plugin-builder). Builder bodies decode
// purely — no registry resolve leg.
func Builders(uf *spec.UnifiedFile) map[string]*spec.BuilderDef {
	return spec.DecodePluginKindMap[spec.BuilderDef](uf, "builder")
}

// resolveInits projects the name-keyed init-system vocabulary from uf.PluginKinds["init"] (opaque
// bodies) into *spec.ResolvedInit value envelopes via candy/plugin-init's ops.OpResolve config leg.
func resolveInits(uf *spec.UnifiedFile) map[string]*spec.ResolvedInit {
	return spec.ResolvePluginKindViaPlugin(uf, "init", resolveInitConfigViaPlugin)
}

// ProjectCandies scans or synthesizes a candy per entry in uf.Candy into its FINAL
// spec.CandyReader form — projectCandiesScanned plus the ONE completion choke point
// (FinalizeScannedCandies, with no InitCfg in scope for a standalone call).
func ProjectCandies(uf *spec.UnifiedFile, rootDir string) (map[string]spec.CandyReader, error) {
	scanned, err := projectCandiesScanned(uf, rootDir)
	if err != nil {
		return nil, err
	}
	return requireProjectLoader().FinalizeScannedCandies(scanned, nil), nil
}

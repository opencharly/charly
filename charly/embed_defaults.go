package main

import (
	_ "embed"
	"fmt"
)

// embeddedCharlyDefaults is the binary's DEFAULT config, compiled into the charly
// CLI. It is a complete UNIFIED NODE-FORM charly config carrying the default build
// vocabulary (resource / builder / distro / init) AND the default sidecar-template
// library, authored in the SAME node-form (`<name>: {<discriminator>: …}`) every
// project charly.yml uses. A project needs to ship NONE of it: the binary fills any
// vocabulary or sidecar the project did not declare (project-wins), and a project
// EXTENDS or OVERRIDES it by declaring its own node entries.
//
//go:embed charly.yml
var embeddedCharlyDefaults []byte

// embeddedDefaults parses the binary-embedded node-form defaults into a UnifiedFile
// through the SAME per-document routing (kit.ClassifyDoc → #NodeDoc gate → parse →
// materialize) that every on-disk charly.yml document flows through — via
// materializeDocStream (materialize.go), the in-memory counterpart of the sdk/loaderkit Walk
// mechanism (the embedded vocab has no imports/discover/namespaces, so it needs no file walk).
// The embedded default is just another node-form config that happens to live in the
// binary. Parsed fresh on each call so no mutable state is shared across loads.
func embeddedDefaults() (*UnifiedFile, error) {
	var uf UnifiedFile
	if err := materializeDocStream(embeddedCharlyDefaults, "charly defaults (embedded)", &uf); err != nil {
		return nil, fmt.Errorf("parsing embedded defaults: %w", err)
	}
	return &uf, nil
}

// applyEmbeddedDefaults merges the binary-embedded build vocabulary AND sidecar
// templates UNDER a project's own entries — the project always wins.
//
// The embedded set is the BASE; the project's entries are the overlay that wins.
// The build vocabulary (distro/builder/init/resource) AND the sidecar template library
// are ALL plugin kinds now (candy/plugin-distro / candy/plugin-builder / candy/plugin-init /
// candy/plugin-resource / candy/plugin-sidecar): the embedded entries land in
// def.PluginKinds, merged UNDER the project's own entries by the generic name-keyed
// root-wins mergePluginKindsMap (copy a name only when ABSENT). So a project's
// `distro: fedora` / `sidecar: tailscale` overrides the embedded one. Calling this AFTER
// all project sources are merged fills only what the project did not define —
// project-wins is structural, not order-dependent. Called by materializeLoadedProject
// (materialize.go) for the root AND every namespace child it materializes, so each
// project/namespace inherits the default vocabulary + sidecar templates. (Replaces the
// former explicit mergeDistroMap/mergeBuilderMap/mergeInitMap/mergeResourceMap/mergeSidecarMap calls.)
func applyEmbeddedDefaults(uf *UnifiedFile) error {
	def, err := embeddedDefaults()
	if err != nil {
		return err
	}
	mergePluginKindsMap(&uf.PluginKinds, def.PluginKinds)
	return nil
}

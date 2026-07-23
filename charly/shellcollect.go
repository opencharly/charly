package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// CollectShell walks the base-image chain for boxName and gathers
// per-(origin, shell) shell-init contributions into a three-section
// spec.LabelShellSet. Mirrors CollectDescriptions / CollectHooks shape — dedupe by
// candy name, walk internal bases until an external image, terminate
// on visited-image cycle.
//
// Section assignment:
//   - Each candy's `shell:` (intrinsic + per-shell sub-blocks) → Candy.
//   - Box-level `shell:` → Box.
//   - Deploy is never populated: the deploy-scope `shell:` overlay authoring
//     field (#Deploy.shell / DeployShellOverlay) was RETIRED outright by the
//     validation-correctness batch — its would-be merge (MergeDeployShell/
//     shellOverlayToEntry) never had a production caller anywhere in this
//     repo's history (RDD-verified via full git-log archaeology, not just
//     the dead-code-radical-removal-batch deletion that finally deleted the
//     unreachable helper), so the honest fix was removing the never-wired
//     authoring surface rather than finishing a half-built feature. The
//     LabelShellSet.Deploy wire section itself stays (a stable three-section
//     label shape a future, properly-designed feature could populate), just
//     permanently empty until one exists.
//
// Returns nil if every section is empty.
func CollectShell(cfg *Config, layers map[string]spec.CandyReader, boxName string) *spec.LabelShellSet {
	set := &spec.LabelShellSet{}

	allCandyNames, _ := deploykit.BoxCandyChain(cfg, layers, boxName)
	for _, candyName := range allCandyNames {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		entry := shellConfigToEntry(layer.Shell(), candyName)
		if entry == nil {
			continue
		}
		entry.ID = candyName
		set.Candy = append(set.Candy, *entry)
	}

	if img, ok := cfg.BoxConfig(boxName); ok {
		if img.Shell != nil {
			entry := shellConfigToEntry(img.Shell, "box:"+boxName)
			if entry != nil {
				entry.ID = "box:" + boxName
				set.Box = append(set.Box, *entry)
			}
		}
	}

	if len(set.Candy) == 0 && len(set.Box) == 0 && len(set.Deploy) == 0 {
		return nil
	}
	return set
}

// shellConfigToEntry projects an in-memory ShellConfig into the
// label-emission spec.ShellEntry shape. Returns nil when the config is
// effectively empty (no Init, no PathAppend, no per-shell overrides).
func shellConfigToEntry(cfg *spec.Shell, origin string) *spec.ShellEntry {
	if cfg == nil {
		return nil
	}
	hasGeneric := cfg.Init != "" || len(cfg.PathAppend) > 0 || cfg.Path != ""
	if !hasGeneric && len(cfg.ByShell()) == 0 {
		return nil
	}
	entry := &spec.ShellEntry{
		Origin:   origin,
		Priority: cfg.Priority,
	}
	if hasGeneric {
		entry.Generic = &ShellSpec{
			Init:       cfg.Init,
			PathAppend: append([]string(nil), cfg.PathAppend...),
			Path:       cfg.Path,
		}
	}
	if len(cfg.ByShell()) > 0 {
		entry.ByShell = make(map[string]*ShellSpec, len(cfg.ByShell()))
		for k, v := range cfg.ByShell() {
			if v == nil {
				continue
			}
			entry.ByShell[k] = &ShellSpec{
				Init:       v.Init,
				PathAppend: append([]string(nil), v.PathAppend...),
				Path:       v.Path,
			}
		}
	}
	return entry
}

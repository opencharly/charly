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
//   - Deploy-scope defaults from charly.yml are not yet expressed —
//     reserved for future use; today the Deploy section is filled at
//     deploy time by MergeDeployShell from charly.yml entries.
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

// MergeDeployShell applies a charly.yml `shell:` overlay onto a label-
// baked spec.LabelShellSet, returning a new merged set. Mirrors
// MergeDeployDescriptions semantics:
//   - Entry with matching ID and skip:true → drop the matched entry.
//   - Entry with matching ID and skip:false → replace the matched
//     entry wholesale.
//   - Entry with no matching ID (or no ID) → append into the deploy
//     section with Origin "deploy" if not set.
func MergeDeployShell(baked *spec.LabelShellSet, overlay []spec.ShellEntry) *spec.LabelShellSet {
	if baked == nil {
		baked = &spec.LabelShellSet{}
	}
	out := &spec.LabelShellSet{
		Candy:  append([]spec.ShellEntry(nil), baked.Candy...),
		Box:    append([]spec.ShellEntry(nil), baked.Box...),
		Deploy: append([]spec.ShellEntry(nil), baked.Deploy...),
	}
	if len(overlay) == 0 {
		return out
	}
	for _, e := range overlay {
		if e.ID != "" {
			if replaced := replaceShellEntryByID(out, e); replaced {
				continue
			}
		}
		// Unmatched ID or no ID — append to Deploy.
		if e.Origin == "" {
			e.Origin = "deploy"
		}
		out.Deploy = append(out.Deploy, e)
	}
	return out
}

// replaceShellEntryByID looks up entry.ID across the three sections of
// `set` and either replaces (skip=false) or removes (skip=true). The
// `skip` field on spec.ShellEntry is encoded as zero priority + nil
// Generic + nil ByShell when stored on disk; charly.yml-side parsing
// consumes a separate ShellOverlayEntry struct that carries Skip
// explicitly. Here we treat any incoming entry whose Generic/ByShell
// are both nil AND whose Origin is "deploy" or "" as a skip-by-id
// signal — see BundleNode.Shell parsing in deploy.go.
func replaceShellEntryByID(set *spec.LabelShellSet, e spec.ShellEntry) bool {
	skip := e.Generic == nil && len(e.ByShell) == 0
	for _, bucket := range [...]*[]spec.ShellEntry{&set.Candy, &set.Box, &set.Deploy} {
		for i, b := range *bucket {
			if b.ID != e.ID {
				continue
			}
			if skip {
				*bucket = append((*bucket)[:i], (*bucket)[i+1:]...)
			} else {
				(*bucket)[i] = e
			}
			return true
		}
	}
	return false
}

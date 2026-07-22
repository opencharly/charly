package main

import (
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

// deploy.go — the deploy KEY→image RESOLVERS + the DeployConfigPath/Env seam pointers.
// (The former shellOverlayToEntry was a dead-code-radical-removal-batch deletion — zero
// real callers; MergeDeployShell, its only consumer, was deleted alongside it.) The deploy
// STATE-MODEL body (LoadBundleConfig / SaveBundleConfig /
// LoadDeployConfigForRead / LoadDeployConfigForWrite / MergeDeployOntoMetadata /
// CleanDeployEntry / SaveDeployState / ExportAllBox + the deploykit.SaveDeployStateInput type +
// the pure helpers scopeVolumesToDeployKey / descriptionInfo / isSameBaseBox / removeBySource /
// removeByExactSource) MOVED to sdk/deploykit in K5-Unit-1 (the S-K5 keystone that unblocks
// P13). charly/ calls the deploykit path directly (IMPORT-PURITY: no new charly/*_aliases.go;
// the 1 kind-blind K1-gated op — LoadUnified — reaches core through the DeployStateHost seam
// charly fills at init — RegisterDeployStateHost; the deploy-kind-specific marshal
// marshalBundleNode lives in charly/deploy_nodeform.go, supplied as a callback to the
// kind-blind SaveBundleConfig shell — tracked K4-exit inventory).

// resolveDeployKeyToBox maps a deploy-key name to the `box:` field of
// its deploy entry. User (~/.config/charly/charly.yml) wins over project
// (charly.yml/check.yml) — the same precedence the check runner and
// `charly config` use. Returns "" when no entry declares a box for the key
// (caller decides the fallback). Implements the Pattern-B (arbitrary
// deploy-key + version-pin) and kind:check-bed (key != box) lookups.
// See /charly-core:deploy "Two supported deploy patterns".
func resolveDeployKeyToBox(key, instance string) string {
	if key == "" {
		return ""
	}
	// User-side first.
	if dc := deploykit.LoadDeployConfigForRead("resolveDeployKeyToBox"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(key, instance)]; ok && entry.Image != "" {
			return entry.Image
		}
		if entry, ok := dc.Bundle[key]; ok && entry.Image != "" {
			return entry.Image
		}
	}
	// Project-level fallback.
	if dir, err := os.Getwd(); err == nil {
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
			if pc := uf.ProjectBundleConfig(); pc != nil {
				if entry, ok := pc.Bundle[key]; ok && entry.Image != "" {
					return entry.Image
				}
			}
		}
	}
	return ""
}

// resolveDeployResolvedImage returns the concrete overlay image ref a pod
// deploy's add_candy: overlay build persisted (BundleNode.ResolvedImage), or ""
// when none is recorded. This is charly-written PER-HOST runtime state (written
// by PrepareVenue via deploykit.SaveDeployState, like resolved_port), so it is read ONLY
// from the per-host charly.yml — never the project config, which carries no
// resolved_image. When set, resolveDeployRef deploys THIS exact overlay (with
// the add_candy: layers) instead of re-resolving the base image: short-name by
// a CalVer sort the overlay alias can lose to the base on a same-minute build.
func resolveDeployResolvedImage(key, instance string) string {
	if key == "" {
		return ""
	}
	if dc := deploykit.LoadDeployConfigForRead("resolveDeployResolvedImage"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(key, instance)]; ok && entry.ResolvedImage != "" {
			return entry.ResolvedImage
		}
		if entry, ok := dc.Bundle[key]; ok && entry.ResolvedImage != "" {
			return entry.ResolvedImage
		}
	}
	return ""
}

// resolveDeployBoxName is THE single deploy-key→image-name resolver used
// by every deploy-mode command that starts from a deploy key (charly config /
// start / shell / check live). It returns the deploy entry's declared
// `box:` (resolveDeployKeyToBox), falling back to the key itself when
// no entry declares one (the key==image convention). Before this was
// shared, `charly config` resolved key→image but `charly start`/`charly shell`/
// `charly check live` treated the key AS the image — so a kind:check bed
// (check-jupyter-pod → jupyter) or any Pattern-B deploy resolved a
// different (wrong/unresolvable) image per command. `charly update` reaches the
// same value via its already-resolved merged-tree node (node.Image), so it
// reads that directly rather than re-loading config here.
func resolveDeployBoxName(key, instance string) string {
	if img := resolveDeployKeyToBox(key, instance); img != "" {
		return img
	}
	return key
}

// DeployConfigPath returns the path to the deploy overlay file. Package-level var for
// testability (tests inject a temp path, same pattern as RuntimeConfigPath). The resolver
// body lives in kit.DefaultDeployConfigPath — ONE definition shared with the out-of-module
// candy/plugin-migrate (R3).
var DeployConfigPath = kit.DefaultDeployConfigPath

// DeployConfigEnv overrides the per-host deploy-config PATH. A check bed sets it (via the
// bed runner) to a PER-BED isolated file so CONCURRENT beds never share — and corrupt —
// the operator's ~/.config/charly/charly.yml, and a disposable bed's transient
// resolved_port/quadlet state never pollutes the operator's persistent config. The 2026-07
// maxjobs-load corruption (`node "…": kind:group: #GroupInput.resolved_port: field not
// allowed`) was concurrent beds racing the shared read-modify-write of this one file.
const DeployConfigEnv = kit.DeployConfigEnv

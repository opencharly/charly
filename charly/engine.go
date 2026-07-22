package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// engine.go — per-box container-engine (podman/docker) RESOLUTION that needs *Config/*Candy
// (charly.yml-coupled, so it cannot move without the loader — K4).
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K4 (deploy + config
// resolution → deploykit + the deploy/bundle plugins). ResolveBoxEngine (needs
// *Config/layers via cfg.BoxConfig/ResolveCandyOrder) is consumed from
// resolved_project_host.go — not movable in isolation without also relocating the loader
// dependency. Stays core until the K4 deploy-cone wave moves the whole consumer set
// together. (Its former siblings ImageRuntime and ResolveBoxEngineFromDir, and their
// claimed resolved_project_host.go/start.go call sites, were dead-code-radical-removal-batch
// deletions — zero real callers anywhere; start.go no longer exists, and
// resolved_project_host.go never called either.)
//
// The per-deploy (no-charly.yml) twins ResolveBoxEngineForDeploy/ResolveBoxEngineFromMeta
// used to be duplicated HERE too — a bare copy of sdk/deploykit's own versions
// (deploykit/box_engine.go) — dissolved in the CHECK-wave container-resolve dedup: every
// caller (commands.go, config_image.go, preempt.go, service.go, pod_lifecycle_resolve.go)
// now calls deploykit.ResolveBoxEngineForDeploy/FromMeta directly. The deploykit file's own
// header comment claiming "preempt.go, resolved_project_host.go, status_collector.go —
// direct deploykit.ResolveBoxEngineForDeploy calls" was FALSE for preempt.go before this fix
// (grep-verified: preempt.go:309,365 called the bare core version) — corrected there too.

// ResolveBoxEngine returns the run engine for a specific box.
// Schema v4: BoxConfig.Engine removed (deploy-only choice). Priority is
// now: candy engine requirements > global default. Deploy-time overrides
// come from BundleNode.Engine via deploykit.ResolveBoxEngineForDeploy /
// deploykit.ResolveBoxEngineFromMeta.
func ResolveBoxEngine(cfg *Config, layers map[string]spec.CandyReader, boxName string, globalRunEngine string) string {
	img, ok := cfg.BoxConfig(boxName)
	if !ok {
		return globalRunEngine
	}

	// Candy-level engine requirements (transitive closure)
	resolved, err := deploykit.ResolveCandyOrder(img.Candy, layers, nil)
	if err == nil {
		for _, candyName := range resolved {
			if layer, ok := layers[candyName]; ok && layer.Engine() != "" {
				return layer.Engine()
			}
		}
	}

	return globalRunEngine
}


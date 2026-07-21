package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// engine.go — per-box container-engine (podman/docker) RESOLUTION that needs *Config/*Candy
// (charly.yml-coupled, so it cannot move without the loader — K4).
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K4 (deploy + config
// resolution → deploykit + the deploy/bundle plugins). ResolveBoxEngine (needs
// *Config/layers via cfg.BoxConfig/ResolveCandyOrder) / ResolveBoxEngineFromDir (needs
// LoadConfig) / ImageRuntime (a pure *kit.ResolvedRuntime copy, kept alongside its sibling
// resolvers) are consumed from resolved_project_host.go and start.go — none movable in
// isolation without also relocating the loader dependency. Stays core until the K4
// deploy-cone wave moves the whole consumer set together.
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

// ImageRuntime returns a copy of rt with RunEngine adjusted for the given image.
// If imageEngine is empty or matches the existing RunEngine, returns the original runtime.
func ImageRuntime(rt *kit.ResolvedRuntime, imageEngine string) *kit.ResolvedRuntime {
	if imageEngine == "" || imageEngine == rt.RunEngine {
		return rt
	}
	rtCopy := *rt
	rtCopy.RunEngine = imageEngine
	return &rtCopy
}

// ResolveBoxEngineFromDir resolves the run engine for an image using charly.yml
// from the given directory. Falls back to globalEngine if no config is available.
func ResolveBoxEngineFromDir(dir, boxName, globalEngine string) string {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return globalEngine
	}
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return globalEngine
	}
	return ResolveBoxEngine(cfg, layers, boxName, globalEngine)
}

package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

// engine.go — per-box/per-deploy container-engine (podman/docker) RESOLUTION.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K4 (deploy + config
// resolution → deploykit + the deploy/bundle plugins). It is heavily cross-cone
// coupled today — ResolveBoxEngine* / ImageRuntime are consumed from
// commands.go, container.go, pod_lifecycle_resolve.go, preempt.go,
// resolved_project_host.go, service.go, start.go, config_image.go,
// status_collector.go, volume_cp_tags_cmd.go (P14-rest trace, 2026-07) — none
// of which are movable in isolation without also relocating those consumers.
// Stays core until the K4 deploy-cone wave moves the whole consumer set
// together; a unilateral move of this file alone would strand its callers.

// EngineBinary returns the binary name for the given engine.
// The "auto" case should not normally be reached (resolved earlier by kit.DetectEngine),
// but is handled defensively.

// ResolveBoxEngine returns the run engine for a specific box.
// Schema v4: BoxConfig.Engine removed (deploy-only choice). Priority is
// now: candy engine requirements > global default. Deploy-time overrides
// come from BundleNode.Engine via ResolveBoxEngineForDeploy /
// ResolveBoxEngineFromMeta.
func ResolveBoxEngine(cfg *Config, layers map[string]*Candy, boxName string, globalRunEngine string) string {
	img, ok := cfg.BoxConfig(boxName)
	if !ok {
		return globalRunEngine
	}

	// Candy-level engine requirements (transitive closure)
	resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
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

// ResolveBoxEngineForDeploy resolves the run engine from the per-host deploy config,
// falling back to globalEngine. No charly.yml (project) dependency. Shared by
// commands.go/container.go/preempt.go/service.go/start.go/config_image.go/
// status_collector.go/volume_cp_tags_cmd.go/pod_lifecycle_resolve.go (the pod-deploy
// subsystem, K4).
func ResolveBoxEngineForDeploy(boxName, instance, globalEngine string) string {
	if entry, ok := deploykit.LoadDeployConfigForRead("ResolveBoxEngineForDeploy").Lookup(boxName, instance); ok && entry.Engine != "" {
		return entry.Engine
	}
	return globalEngine
}

// ResolveBoxEngineFromMeta returns the engine from image metadata labels,
// falling back to globalEngine if not set.
func ResolveBoxEngineFromMeta(meta *BoxMetadata, globalEngine string) string {
	if meta != nil && meta.Engine != "" {
		return meta.Engine
	}
	return globalEngine
}

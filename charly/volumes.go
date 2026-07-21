package main

import (
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// VolumeMount + ResolvedBindMount moved to sdk/deploykit (P13/C15); referenced
// directly here as deploykit.VolumeMount / deploykit.ResolvedBindMount.

// CollectBoxVolume resolves all volumes for a box by traversing the
// full box chain (box → base → base's base) and collecting volume
// declarations from all candies. Volumes are deduplicated by name (first
// declaration wins — outermost box takes priority).
func CollectBoxVolume(cfg *Config, layers map[string]spec.CandyReader, boxName string, home string, excludeNames map[string]bool) ([]deploykit.VolumeMount, error) {
	// Collect all candy names from the box chain (outermost first) via the
	// shared base-chain walk; propagate a resolution error as before.
	allCandyNames, err := deploykit.BoxCandyChain(cfg, layers, boxName)
	if err != nil {
		return nil, err
	}

	// Collect volumes, dedup by name (first wins), skip excluded names
	seen := make(map[string]bool)
	var mounts []deploykit.VolumeMount
	for _, candyName := range allCandyNames {
		layer, ok := layers[candyName]
		if !ok || !layer.HasVolumes() {
			continue
		}
		for _, vol := range layer.Volume() {
			if seen[vol.Name] || excludeNames[vol.Name] {
				continue
			}
			seen[vol.Name] = true
			mounts = append(mounts, deploykit.VolumeMount{
				VolumeName:    "charly-" + boxName + "-" + vol.Name,
				ContainerPath: expandHome(vol.Path, home),
			})
		}
	}

	// Sort by volume name for deterministic output
	sortVolumeMounts(mounts)
	return mounts, nil
}

// expandHome replaces ~ and $HOME with the resolved home directory
func expandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return home + path[1:]
	}
	if path == "~" {
		return home
	}
	path = strings.ReplaceAll(path, "$HOME", home)
	return path
}

// Per-deploy volume naming (base / instance / Pattern-B / kind:check bed) is
// handled centrally by scopeVolumesToDeployKey + deployVolumePrefix in deploy.go
// (keyed by the deploy's container name), so a dedicated instance-only renamer is
// no longer needed.

// workspaceBindHost, parseVolumeFlagsStandalone, and mergeVolumeConfigs (the
// BoxConfigSetupCmd.parseVolumeFlags extraction for shell/start reuse) moved along with
// BoxConfigSetupCmd/BoxConfigRemoveCmd's orchestration to candy/plugin-deploy-pod
// (P13-KERNEL direction-flip) — the plugin now owns its own workspaceBindHost +
// parseVolumeFlags + mergeVolumeConfigsLocal, working on the wire spec.DeployVolume type.

// sortVolumeMounts sorts volume mounts by name for deterministic output
func sortVolumeMounts(mounts []deploykit.VolumeMount) {
	for i := 0; i < len(mounts)-1; i++ {
		for j := i + 1; j < len(mounts); j++ {
			if mounts[i].VolumeName > mounts[j].VolumeName {
				mounts[i], mounts[j] = mounts[j], mounts[i]
			}
		}
	}
}

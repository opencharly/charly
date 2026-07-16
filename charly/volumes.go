package main

import (
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/deploykit"
)

// VolumeMount + ResolvedBindMount moved to sdk/deploykit (P13/C15); referenced
// directly here as deploykit.VolumeMount / deploykit.ResolvedBindMount.

// CollectBoxVolume resolves all volumes for a box by traversing the
// full box chain (box → base → base's base) and collecting volume
// declarations from all candies. Volumes are deduplicated by name (first
// declaration wins — outermost box takes priority).
func CollectBoxVolume(cfg *Config, layers map[string]*Candy, boxName string, home string, excludeNames map[string]bool) ([]deploykit.VolumeMount, error) {
	// Collect all candy names from the box chain (outermost first) via the
	// shared base-chain walk; propagate a resolution error as before.
	allCandyNames, err := cfg.boxCandyChain(layers, boxName)
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

// workspaceBindHost returns the host path of the "workspace" bind mount, or "".
func workspaceBindHost(bindMounts []deploykit.ResolvedBindMount) string {
	for _, bm := range bindMounts {
		if bm.Name == "workspace" {
			return bm.HostPath
		}
	}
	return ""
}

// parseVolumeFlagsStandalone converts --volume and --bind CLI flags into DeployVolumeConfig.
// Extracted from BoxConfigSetupCmd.parseVolumeFlags for reuse in shell/start.
func parseVolumeFlagsStandalone(volumeFlags, bindFlags []string) []DeployVolumeConfig {
	var configs []DeployVolumeConfig
	seen := make(map[string]bool)

	for _, v := range volumeFlags {
		parts := strings.SplitN(v, ":", 3)
		dv := DeployVolumeConfig{Name: parts[0]}
		if len(parts) >= 2 {
			dv.Type = parts[1]
		}
		if len(parts) >= 3 {
			dv.Host = parts[2]
		}
		if dv.Type == "" {
			dv.Type = "volume"
		}
		if dv.Type == "encrypt" {
			dv.Type = "encrypted"
		}
		if !seen[dv.Name] {
			configs = append(configs, dv)
			seen[dv.Name] = true
		}
	}

	for _, b := range bindFlags {
		if seen[b] || seen[strings.SplitN(b, "=", 2)[0]] {
			continue
		}
		if before, after, ok := strings.Cut(b, "="); ok {
			name := before
			host := after
			// Resolve "." to absolute path
			if host == "." {
				if abs, err := filepath.Abs(host); err == nil {
					host = abs
				}
			}
			configs = append(configs, DeployVolumeConfig{Name: name, Type: "bind", Host: host})
			seen[name] = true
		} else {
			configs = append(configs, DeployVolumeConfig{Name: b, Type: "bind"})
			seen[b] = true
		}
	}

	return configs
}

// mergeVolumeConfigs merges CLI overrides onto charly.yml volume configs.
// CLI overrides win by name.
func mergeVolumeConfigs(base, overrides []DeployVolumeConfig) []DeployVolumeConfig {
	if len(overrides) == 0 {
		return base
	}
	var result []DeployVolumeConfig
	seen := make(map[string]bool)
	// Overrides first (highest priority)
	for _, o := range overrides {
		result = append(result, o)
		seen[o.Name] = true
	}
	// Base configs that weren't overridden
	for _, b := range base {
		if !seen[b.Name] {
			result = append(result, b)
		}
	}
	return result
}

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

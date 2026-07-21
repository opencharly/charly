package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// CollectSecurity merges security configs from all candies in an image, then applies
// image-level overrides. Returns a merged SecurityConfig. The host-side half: resolve WHICH
// candies compose the box (a *Config/candy-tree walk — genuinely core, unchanged from before the
// W9 split). The MERGE ITSELF (candy-order fold + override application — biggest/smallest-wins
// rules) is deploykit.MergeCandySecurity, the pure R-item every OCI-label-collector build-render
// consumer can share (host today, an out-of-process build/deploy plugin tomorrow).
func CollectSecurity(cfg *Config, layers map[string]spec.CandyReader, boxName string) SecurityConfig {
	img, ok := cfg.BoxConfig(boxName)
	if !ok {
		return SecurityConfig{}
	}

	// Resolve the box's own candy tree (leaf-specific — security does NOT
	// inherit from a base box; the shared boxDirectCandies walk). Fall back to
	// the raw direct refs on a resolution error, as before.
	allCandies, err := deploykit.BoxDirectCandies(cfg, layers, boxName)
	if err != nil {
		allCandies = img.Candy
	}

	candies := make([]spec.CandyReader, 0, len(allCandies))
	for _, name := range allCandies {
		if ly, ok := layers[name]; ok {
			candies = append(candies, ly)
		}
	}
	return deploykit.MergeCandySecurity(candies, img.Security)
}

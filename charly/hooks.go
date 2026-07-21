package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// CollectHooks collects and concatenates hooks from all candies in a box's candy chain.
// Hooks from multiple candies are concatenated in candy order. The host-side half: resolve the
// box's FULL candy chain (base-inheriting — a *Config/walkBaseChain concern, genuinely core,
// unchanged from before the W9 split). The concatenation itself is deploykit.MergeCandyHooks, the
// pure R-item every OCI-label-collector build-render consumer can share (host today, an
// out-of-process build/deploy plugin tomorrow).
func CollectHooks(cfg *Config, layers map[string]spec.CandyReader, boxName string) *HooksConfig {
	allCandyNames, _ := cfg.boxCandyChain(layers, boxName)

	candies := make([]spec.CandyReader, 0, len(allCandyNames))
	for _, name := range allCandyNames {
		if layer, ok := layers[name]; ok {
			candies = append(candies, layer)
		}
	}
	return deploykit.MergeCandyHooks(candies)
}

// RunHook/removeVolumes DELETED (Cutover B unit 2 remove-verb completion) — both relocated
// verbatim to candy/plugin-pod/remove_orchestration.go (runHook/removeVolumes); their only caller,
// the former podRemoveCmd, moved there too. Confirmed portable (pure os/exec + sdk/deploykit, zero
// core-registry coupling).

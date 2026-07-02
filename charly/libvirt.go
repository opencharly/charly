package main

import (
	"strings"
)

// CollectLibvirtSnippets gathers libvirt XML snippets from all candies in a box
// plus box-level snippets, deduplicating by exact string match.
func CollectLibvirtSnippets(cfg *Config, layers map[string]*Candy, boxName string) []string {
	seen := make(map[string]bool)
	var snippets []string

	addSnippet := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		snippets = append(snippets, s)
	}

	// Collect from the box's own candies (leaf-specific — VM snippets do NOT
	// inherit from a base box; the shared boxDirectCandies walk). Box-level
	// `libvirt:` was removed in the VM hard-cutover — raw XML snippets now live
	// on the paired `kind: vm` entity's `spec.libvirt.snippets:` in vm.yml.
	resolved, err := cfg.boxDirectCandies(layers, boxName)
	if err != nil {
		return nil
	}
	for _, candyName := range resolved {
		layer, ok := layers[candyName]
		if !ok || !layer.HasLibvirt() {
			continue
		}
		for _, s := range layer.Libvirt() {
			addSnippet(s)
		}
	}

	return snippets
}

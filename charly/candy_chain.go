package main

import "fmt"

// boxCandyChain returns the ordered, de-duplicated candy map-keys for boxName
// across its FULL base-image chain (box → base → base's base), candy-order per
// level. This is the ONE walk every BASE-CHAIN field collector shares —
// CollectHooks, CollectShell, CollectDescriptions,
// CollectBoxVolume, and CollectBoxPorts — so a contribution a base box makes
// (a volume, an check check, a published port) is inherited by every box built
// on it. De-duplication is first-occurrence-wins by candy key, matching the
// per-collector `seen` maps it replaces.
//
// On a ResolveCandyOrder error at a level the walk stops there (matching the
// prior per-collector `break`), returning what was collected so far PLUS the
// error — callers that propagated it (CollectBoxVolume) keep doing so; callers
// that swallowed it and used the partial result (CollectDescriptions et al.) keep doing
// that by ignoring the returned error.
func (c *Config) boxCandyChain(layers map[string]*Candy, boxName string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, node := range c.walkBaseChain(boxName) {
		resolved, err := ResolveCandyOrder(node.Img.Candy, layers, nil)
		if err != nil {
			return out, err
		}
		for _, name := range resolved {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

// boxDirectCandies returns the ordered, transitively-resolved candy map-keys
// for boxName's OWN candies only — NO base-chain traversal. The shared walk for
// LEAF-SPECIFIC fields (CollectSecurity, CollectBoxAlias,
// CollectLibvirtSnippets) that intentionally do NOT inherit from a base box.
func (c *Config) boxDirectCandies(layers map[string]*Candy, boxName string) ([]string, error) {
	img, ok := c.Box[boxName]
	if !ok {
		return nil, fmt.Errorf("box %q not found in charly.yml", boxName)
	}
	return ResolveCandyOrder(img.Candy, layers, nil)
}

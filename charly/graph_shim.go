package main

// graph_shim.go — P8 transitional. The candy/box dependency-graph subsystem moved
// to sdk/deploykit (deploykit/graph.go, byte-identical logic over CandyModel +
// buildkit.ResolvedBox). These thin package-main wrappers keep the existing call
// sites (which hold map[string]*Candy) compiling unchanged by converting to
// map[string]deploykit.CandyModel and delegating. They shrink and delete as their
// callers relocate to deploykit / candy/plugin-build — the end state has ZERO graph
// code in charly core. CycleError is aliased so step_topo.go / validate.go /
// step_validate.go keep working after charly/graph.go is deleted.

import "github.com/opencharly/sdk/deploykit"

// CycleError is the shared circular-dependency error, homed in deploykit now.
type CycleError = deploykit.CycleError

// candyModelMap adapts the charly *Candy map to the deploykit.CandyModel interface
// map the relocated graph/render functions consume (*Candy satisfies CandyModel).
func candyModelMap(m map[string]*Candy) map[string]deploykit.CandyModel {
	out := make(map[string]deploykit.CandyModel, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ExpandCandy(requested []string, layers map[string]*Candy) ([]string, error) {
	return deploykit.ExpandCandy(requested, candyModelMap(layers))
}

func ResolveCandyOrder(requested []string, layers map[string]*Candy, parentCandies map[string]bool) ([]string, error) {
	return deploykit.ResolveCandyOrder(requested, candyModelMap(layers), parentCandies)
}

func BoxNeedsBuilder(img *ResolvedBox, boxes map[string]*ResolvedBox, layers map[string]*Candy) bool {
	return deploykit.BoxNeedsBuilder(img, boxes, candyModelMap(layers))
}

func boxDirectDeps(name string, img *ResolvedBox, boxes map[string]*ResolvedBox, includeFormatBuilders bool) []string {
	return deploykit.BoxDirectDeps(name, img, boxes, includeFormatBuilders)
}

func ResolveBoxOrder(boxes map[string]*ResolvedBox, layers map[string]*Candy) ([]string, error) {
	return deploykit.ResolveBoxOrder(boxes, candyModelMap(layers))
}

func ResolveBoxLevels(boxes map[string]*ResolvedBox, layers map[string]*Candy) ([][]string, error) {
	return deploykit.ResolveBoxLevels(boxes, candyModelMap(layers))
}

func CandyProvidedByBox(boxName string, boxes map[string]*ResolvedBox, layers map[string]*Candy) (map[string]bool, error) {
	return deploykit.CandyProvidedByBox(boxName, boxes, candyModelMap(layers))
}

func collectAllBoxCandies(boxName string, boxes map[string]*ResolvedBox, layers map[string]*Candy) []string {
	return deploykit.CollectAllBoxCandies(boxName, boxes, candyModelMap(layers))
}

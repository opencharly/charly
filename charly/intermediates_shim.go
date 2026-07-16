package main

import (
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
)

// intermediates_shim.go — P8b transitional shims. The PURE-compute half of the
// auto-intermediate subsystem moved to sdk/deploykit (deploykit/intermediates.go,
// byte-identical logic over CandyModel + buildkit.ResolvedBox). These thin
// package-main wrappers keep the HOST-COUPLED half (charly/intermediates.go —
// ComputeIntermediates / processSiblingGroup / createIntermediate / walkTrieScoped,
// which read *Config cfg.Defaults) + the tests compiling unchanged, converting
// map[string]*Candy → map[string]deploykit.CandyModel via candyModelMap
// (graph_shim.go) where a caller still holds the concrete candy map. Mirrors
// graph_shim.go; shrinks as the host-coupled half relocates too.

// trieNode / siblingKey — the intermediate-computation data structures, homed in
// deploykit now (aliased so the host-coupled walker reads them unchanged).
type (
	trieNode   = deploykit.TrieNode
	siblingKey = deploykit.SiblingKey
)

func newTrieNode(layer string) *trieNode { return deploykit.NewTrieNode(layer) }

func sortedKeys(m map[string]*trieNode) []string { return deploykit.SortedKeys(m) }

func sortedSiblingKeys(m map[siblingKey][]string) []siblingKey {
	return deploykit.SortedSiblingKeys(m)
}

func collectSubtreeBoxes(node *trieNode) []string { return deploykit.CollectSubtreeBoxes(node) }

func intersectPlatforms(parent, defaults []string) []string {
	return deploykit.IntersectPlatforms(parent, defaults)
}

func updateBoxBase(imgName, parentName string, result map[string]*buildkit.ResolvedBox) {
	deploykit.UpdateBoxBase(imgName, parentName, result)
}

func pixiBoundCandies(layers map[string]*Candy) map[string]bool {
	return deploykit.PixiBoundCandies(candyModelMap(layers))
}

// GlobalCandyOrder computes the global topological candy order (deploykit) over
// the concrete candy map held by generate.go / validate.go / ComputeIntermediates.
func GlobalCandyOrder(boxes map[string]*buildkit.ResolvedBox, layers map[string]*Candy) ([]string, error) {
	return deploykit.GlobalCandyOrder(boxes, candyModelMap(layers))
}

// AbsoluteCandySequence returns an image's complete candy set as a subsequence of
// the global order (deploykit).
func AbsoluteCandySequence(boxName string, boxes map[string]*buildkit.ResolvedBox, layers map[string]*Candy, globalOrder []string) []string {
	return deploykit.AbsoluteCandySequence(boxName, boxes, candyModelMap(layers), globalOrder)
}

func relativeCandySequence(boxName string, parentProvided map[string]bool, boxes map[string]*buildkit.ResolvedBox, layers map[string]*Candy, globalOrder []string, pixiBound map[string]bool) []string {
	return deploykit.RelativeCandySequence(boxName, parentProvided, boxes, candyModelMap(layers), globalOrder, pixiBound)
}

func computeOwnCandies(parentName string, pathCandies []string, result map[string]*buildkit.ResolvedBox, layers map[string]*Candy, globalOrder []string, pixiBound map[string]bool) []string {
	return deploykit.ComputeOwnCandies(parentName, pathCandies, result, candyModelMap(layers), globalOrder, pixiBound)
}

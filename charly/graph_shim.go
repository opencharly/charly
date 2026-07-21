package main

// graph_shim.go — P8 transitional. The candy/box dependency-graph subsystem moved
// to sdk/deploykit (deploykit/graph.go, byte-identical logic over CandyModel +
// buildkit.ResolvedBox). These thin package-main wrappers keep the existing call
// sites compiling unchanged while delegating. They shrink and delete as their
// callers relocate to deploykit / candy/plugin-build — the end state has ZERO graph
// code in charly core. CycleError is aliased so step_topo.go / validate.go
// keep working after charly/graph.go is deleted.
//
// W9: the candyModelMap conversion this file used to carry is GONE — since
// deploykit.CandyModel = spec.CandyReader (a true alias), map[string]spec.CandyReader
// and map[string]deploykit.CandyModel are the IDENTICAL Go type now that core holds
// no concrete *Candy to convert FROM. Every wrapper below passes layers straight
// through.

import (
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// CycleError is the shared circular-dependency error, homed in deploykit now.
type CycleError = deploykit.CycleError

func ResolveCandyOrder(requested []string, layers map[string]spec.CandyReader, parentCandies map[string]bool) ([]string, error) {
	return deploykit.ResolveCandyOrder(requested, layers, parentCandies)
}

func boxDirectDeps(name string, img *buildkit.ResolvedBox, boxes map[string]*buildkit.ResolvedBox, includeFormatBuilders bool) []string {
	return deploykit.BoxDirectDeps(name, img, boxes, includeFormatBuilders)
}

func ResolveBoxOrder(boxes map[string]*buildkit.ResolvedBox, layers map[string]spec.CandyReader) ([]string, error) {
	return deploykit.ResolveBoxOrder(boxes, layers)
}

func ResolveBoxLevels(boxes map[string]*buildkit.ResolvedBox, layers map[string]spec.CandyReader) ([][]string, error) {
	return deploykit.ResolveBoxLevels(boxes, layers)
}

func CandyProvidedByBox(boxName string, boxes map[string]*buildkit.ResolvedBox, layers map[string]spec.CandyReader) (map[string]bool, error) {
	return deploykit.CandyProvidedByBox(boxName, boxes, layers)
}

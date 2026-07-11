package main

import "github.com/opencharly/sdk/deploykit"

// computeEffectiveVersions delegates to deploykit.ComputeEffectiveVersions (the
// build render/version-compute machinery relocated to sdk/deploykit in P8). It
// assigns ResolvedBox.EffectiveVersion (the ai.opencharly.version OCI label) for
// every image in the build graph. TRANSITIONAL shim — the caller (NewGenerator)
// moves to constructing a deploykit.Generator directly as the render relocation
// completes.
func (g *Generator) computeEffectiveVersions() error {
	return deploykit.ComputeEffectiveVersions(g.Boxes, candyModelMap(g.Candies))
}

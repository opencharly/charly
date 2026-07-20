package main

import (
	"fmt"
)

// render_prep.go — the HOST-SIDE render-prep pass (#67 render-DRIVE move). Fills the
// build-render caches on *ResolvedBox by reading the scanned candy set (spec.CandyReader)
// + *Config/*InitConfig — the EXACT computations generateContainerfile previously inlined. Called by
// hostBuildBuildResolve (production) AND by the parity test (live path). The deploykit
// Generator.Generate reads these caches WITHOUT the live graph.
//
// The render-prep runs BEFORE the projector (projectResolvedProject), so the caches
// are on the ResolvedBoxes and the projector copies them into the envelope
// (ResolvedBoxView.BakedMetadata/RenderCandyOrder/InitSystem/InitDef/ActiveInits/Caps).

// renderPrepBox fills the build-render caches on img := g.Boxes[boxName] by reading
// the live graph. Returns the first error (caps missing etc.).
func renderPrepBox(g *Generator, boxName string) error {
	img := g.Boxes[boxName]
	if img == nil {
		return fmt.Errorf("render-prep: box %q not found", boxName)
	}

	// 1. parentCandies (for globalOrderForBox).
	var parentCandies map[string]bool
	if !img.IsExternalBase {
		var err error
		parentCandies, err = CandyProvidedByBox(img.Base, g.Boxes, g.Candies)
		if err != nil {
			return err
		}
	}

	// 2. candyOrder — the per-box cache-optimal candy order.
	candyOrder, err := g.globalOrderForBox(img.Candy, parentCandies)
	if err != nil {
		return err
	}
	img.RenderCandyOrder = candyOrder

	// 3. caps — the candy-contributed capability surface.
	caps, capsErr := AggregateCandyCapabilities(g.Candies, candyOrder)
	if capsErr != nil {
		return capsErr
	}
	img.CandyCaps = caps
	if missing := CheckRequiredCapabilities(g.Candies, candyOrder, caps); len(missing) > 0 {
		return CandyCapabilitiesError(g.Candies, candyOrder, missing)
	}

	// 4. activeInits — the active init systems for this composition.
	if g.InitConfig != nil {
		img.ActiveInits = g.InitConfig.ActiveInit(g.Candies, candyOrder)
	}

	// 5. initSystem + initDef — the resolved init system name + definition.
	if g.InitConfig != nil {
		img.InitSystem, img.InitDef = g.InitConfig.ResolveInitSystem(g.Candies, candyOrder, "")
	}

	// 6. BakedMetadata — the fully-baked OCI-label wire set.
	img.BakedMetadata = buildBakedMetadata(g, boxName, candyOrder)

	return nil
}

// renderPrepAll runs renderPrepBox for every box in the generator's box set.
// Called by hostBuildBuildResolve before projecting the envelope.
func renderPrepAll(g *Generator) error {
	for name := range g.Boxes {
		if err := renderPrepBox(g, name); err != nil {
			return fmt.Errorf("render-prep %s: %w", name, err)
		}
	}
	return nil
}

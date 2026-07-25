package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/kit"
)

// transfer.go — cross-engine image transfer + the generic "ensure present" fallback chain.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K4 (deploy + config
// resolution → deploykit + the deploy/bundle plugins). Its consumer
// config_image.go is a deploy-cone file (P14-rest trace, 2026-07); EnsureImage
// moves together with it.

// EnsureImage ensures the image is available in the run engine's local store.
// Three-tier fallback (each step independent):
//
//  1. Already-present short-circuit (LocalImageExists in run engine).
//  2. Cross-engine transfer (`docker save | podman load`) when build
//     engine != run engine AND the image is present in the build
//     engine's storage.
//  3. dispatchBuildEnsure — the compiled-in candy/plugin-build build:ensure
//     word: pulls from the registry and falls back to a local `charly box
//     build <name>` when the ref maps to a project charly.yml entry (the
//     project itself is re-resolved from cwd inside the "box-ref-resolve"
//     HostBuild seam when dir is empty — the former opportunistic cwd-based
//     project load, core-min wave 3, moved into the seam so every tier-3
//     caller resolves the project the SAME way). This is the same
//     code path BuilderRun, the check preflight, and `charly box pull` all
//     go through (see charly/dispatch_build_ensure.go).
//
// Returns kit.ErrImageNotLocal (wrapped with the ref) only when ALL three
// tiers fail.
func EnsureImage(imageRef string, rt *kit.ResolvedRuntime) error {
	if kit.LocalImageExists(rt.RunEngine, imageRef) {
		return nil
	}

	// Cross-engine transfer first when applicable: it's faster than a
	// network pull and works offline.
	if rt.BuildEngine != rt.RunEngine && kit.LocalImageExists(rt.BuildEngine, imageRef) {
		return kit.TransferImage(rt.BuildEngine, rt.RunEngine, imageRef)
	}

	// Generic ensure: pull, fall back to local build for project images.
	if err := dispatchBuildEnsure(context.Background(), imageRef, "", rt.BuildEngine, rt.RunEngine); err == nil {
		return nil
	}

	return fmt.Errorf("%w: %s", kit.ErrImageNotLocal, imageRef)
}

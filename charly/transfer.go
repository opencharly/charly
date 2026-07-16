package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/kit"
)

// transfer.go — cross-engine image transfer + the generic "ensure present" fallback chain.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K4 (deploy + config
// resolution → deploykit + the deploy/bundle plugins). Both consumers —
// pod_lifecycle_resolve.go and config_image.go — are deploy-cone files (P14-rest
// trace, 2026-07); EnsureImage/loadProjectCfgFromCwd move together with them.

// EnsureImage ensures the image is available in the run engine's local store.
// Three-tier fallback (each step independent):
//
//  1. Already-present short-circuit (LocalImageExists in run engine).
//  2. Cross-engine transfer (`docker save | podman load`) when build
//     engine != run engine AND the image is present in the build
//     engine's storage.
//  3. Canonical `EnsureImagePresent` — pulls from the registry and
//     falls back to a local `charly box build <name>` when the ref maps
//     to a project charly.yml entry. This is the same code path
//     BuilderRun, the check preflight, and `charly box pull` all go
//     through (see charly/ensure_image.go).
//
// Returns kit.ErrImageNotLocal (wrapped with the ref) only when ALL three
// tiers fail.
func EnsureImage(imageRef string, rt *ResolvedRuntime) error {
	if LocalImageExists(rt.RunEngine, imageRef) {
		return nil
	}

	// Cross-engine transfer first when applicable: it's faster than a
	// network pull and works offline.
	if rt.BuildEngine != rt.RunEngine && LocalImageExists(rt.BuildEngine, imageRef) {
		return TransferImage(rt.BuildEngine, rt.RunEngine, imageRef)
	}

	// Generic ensure: pull, fall back to local build for project
	// images. Loads the project cfg if cwd has one; gracefully
	// degrades to pull-only when no project is reachable.
	cfg, projectDir := loadProjectCfgFromCwd()
	if err := EnsureImagePresent(context.Background(), imageRef, cfg, projectDir); err == nil {
		return nil
	}

	return fmt.Errorf("%w: %s", kit.ErrImageNotLocal, imageRef)
}

// loadProjectCfgFromCwd returns the project config + dir when the
// caller's cwd is inside an charly project; (nil, "") otherwise. EnsureImage
// (and any caller of EnsureImagePresent that doesn't carry project
// state) uses this to opportunistically opt into the build-fallback
// path.
func loadProjectCfgFromCwd() (*Config, string) {
	dir, err := os.Getwd()
	if err != nil || dir == "" {
		return nil, ""
	}
	cfg, err := LoadConfig(dir)
	if err != nil || cfg == nil {
		return nil, dir
	}
	return cfg, dir
}

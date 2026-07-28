package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// host_build_loader.go — the TRANSITIONAL loader reverse legs (K1-LOADER RELOCATION, Unit B).
// Split from the PERMANENT legs (host_build_loader_floor.go) so the P16 gate is HONEST: a leg is
// FLOOR only if it dispatches to a permanent in-core M/D mechanism a plugin-side loader will ALWAYS
// call back for; a leg is RESIDUE only if it DISSOLVES as the loader capability finishes moving into
// its owning plugin (so residue→0 GREEN stays reachable). This file holds the three legs that
// dissolve:
//   - loader-materialize     → drives the RELOCATED loaderkit.MaterializeLoadedProject orchestration
//                              (#48 done) over the host leaf seams (hostMaterializeProjectSeams);
//                              TRANSITIONAL host leg (the per-node kind-DECODE M stays host in
//                              provider_kind_invoke.go, a SEPARATE floor file).
//   - loader-android-validate → loaderkit.ValidateAndroidDevices: the kind:android box⊻adb XOR
//                              validator — the capability LOGIC now lives in loaderkit
//                              (validate_capabilities.go, the way ValidateCheckBeds / ValidateEphemeral
//                              relocated); this leg supplies ONLY the host registry-resolve callback
//                              (resolveAndroidViaPlugin) and dissolves as the loader capability
//                              finishes moving into its owning plugin.
//   - loader-preempt-validate → loaderkit.ValidatePreemptible: the preemptible / requires_exclusive /
//                              requires_shared validator — same, LOGIC now in loaderkit; this leg
//                              supplies ONLY the host registry-resolve callbacks
//                              (resolveResourceViaPlugin / resolveVmViaPlugin).
// Each wraps the SAME host function charly.LoadUnified's compiled-in hostLoaderExecutor calls
// DIRECTLY (Unit C); only a genuine out-of-module plugin (execLoaderExecutor, Unit D) pays the
// marshal. The compiled-in TYPED placement skips it (U3).

// hostBuildLoaderMaterialize replays the host MATERIALIZE + root-wins MERGE over the walk envelope
// (spec.LoadedProject → the merged loaderkit.UnifiedFile). The host leg OWNS its own byID scratch
// map (RULING 1) — the plugin passes none; it unmarshals the reply into its own `merged`.
func hostBuildLoaderMaterialize(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var lp spec.LoadedProject
	if err := json.Unmarshal(specJSON, &lp); err != nil {
		return nil, fmt.Errorf("loader-materialize host-build: decode request: %w", err)
	}
	merged := &loaderkit.UnifiedFile{}
	if err := loaderkit.MaterializeLoadedProject(&lp, merged, map[int64]*loaderkit.UnifiedFile{}, hostMaterializeProjectSeams()); err != nil {
		return nil, err
	}
	// MarshalMaterialized (NOT marshalJSON): UnifiedFile.PluginKinds is json:"-", so a plain marshal
	// would DROP every standalone-template / plugin-kind entity plugin-side (the R10 bed regression).
	return loaderkit.MarshalMaterialized(merged)
}

// hostBuildLoaderAndroidValidate runs the kind:android box⊻adb XOR validator (loaderkit.UnifiedFile
// → in-band error). An empty reply means OK.
func hostBuildLoaderAndroidValidate(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var uf loaderkit.UnifiedFile
	// UnmarshalMaterialized (NOT json.Unmarshal): the validator reads uf.Android() = PluginKinds
	// ["android"], which a plain unmarshal would drop.
	if err := loaderkit.UnmarshalMaterialized(specJSON, &uf); err != nil {
		return nil, fmt.Errorf("loader-android-validate host-build: decode request: %w", err)
	}
	if err := loaderkit.ValidateAndroidDevices(&uf, resolveAndroidViaPlugin); err != nil {
		return nil, err
	}
	return nil, nil
}

// hostBuildLoaderPreemptValidate runs the preemptible/requires_exclusive/requires_shared validator
// (loaderkit.UnifiedFile → in-band error). An empty reply means OK.
func hostBuildLoaderPreemptValidate(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var uf loaderkit.UnifiedFile
	// UnmarshalMaterialized (NOT json.Unmarshal): the validator reads uf.VM() = PluginKinds["vm"],
	// which a plain unmarshal would drop.
	if err := loaderkit.UnmarshalMaterialized(specJSON, &uf); err != nil {
		return nil, fmt.Errorf("loader-preempt-validate host-build: decode request: %w", err)
	}
	if err := loaderkit.ValidatePreemptible(&uf, resolveResourceViaPlugin, resolveVmViaPlugin); err != nil {
		return nil, err
	}
	return nil, nil
}

// Register the TRANSITIONAL loader legs at package-var init (before any init()).
var _ = func() bool {
	registerHostBuilder("loader-materialize", hostBuildLoaderMaterialize)
	registerHostBuilder("loader-android-validate", hostBuildLoaderAndroidValidate)
	registerHostBuilder("loader-preempt-validate", hostBuildLoaderPreemptValidate)
	return true
}()

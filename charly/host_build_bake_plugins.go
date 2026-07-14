package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// host_build_bake_plugins.go — the "bake-plugins" host-builder (#67 render-DRIVE move).
// plugin-build's deploykit.Generator.EmitBakedPlugins seam calls back to the host via
// HostBuild("bake-plugins") to build + stage each bake_plugin binary + emit the COPY/chmod
// Containerfile fragment. The host loads the project (NewGenerator — needs the live *Candy
// graph for SourceDir + buildPluginBinary), runs the EXISTING emitBakedPlugins method, and
// returns the fragment string. The deploykit render writes it into the Containerfile buffer.

// hostBuildBakePlugins is the "bake-plugins" host-builder: it loads the project, finds the
// box, runs the host's emitBakedPlugins (build + stage plugin binaries), and returns the
// fragment string.
func hostBuildBakePlugins(_ context.Context, req spec.BakePluginsRequest, _ buildEngineContext) (spec.BakePluginsReply, error) {
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return spec.BakePluginsReply{}, err
		}
		dir = cwd
	}

	// Reconstruct the Generator to access the live *Candy graph (SourceDir + buildPluginBinary).
	// The box name + candy order ride the request. The Generator is disposable — built per call.
	gen, err := NewGenerator(dir, "", ResolveOpts{})
	if err != nil {
		return spec.BakePluginsReply{Error: errString(err)}, nil
	}

	// Run render-prep for the target box so the caches are filled (emitBakedPlugins reads
	// g.Candies which is already populated by NewGenerator, but the render-prep ensures
	// the box is in the generator's box set). Actually emitBakedPlugins reads g.Candies +
	// g.BuildDir — both set by NewGenerator. emitBakedPlugins reads g.Candies (the candy
	// graph) + g.BuildDir — both present; boxName is only the staging-dir path (the box need
	// not be in the filtered box set).
	boxName := req.BoxName

	// Run the host's emitBakedPlugins — the EXISTING method on *Generator (generate.go).
	// It reads g.Candies (live *Candy graph) + g.BuildDir + builds + stages each bake_plugin.
	var b strings.Builder
	if err := gen.emitBakedPlugins(&b, boxName, req.CandyOrder); err != nil {
		return spec.BakePluginsReply{Error: errString(fmt.Errorf("bake-plugins: %w", err))}, nil
	}

	return spec.BakePluginsReply{Fragment: b.String()}, nil
}

// Register the bake-plugins host-builder at package-var init.
// "bake-plugins" is a CLASS-GENERIC action noun (never a provider word — F11).
var _ = func() bool {
	registerHostBuilder("bake-plugins", typedHostBuilder("bake-plugins", hostBuildBakePlugins))
	return true
}()

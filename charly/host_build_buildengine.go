package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/spec/spec"
)

// host_build_buildengine.go — the two `buildengine-*` reverse legs the PLUGIN-SIDE build-engine
// RESOLVE (candy/plugin-build/resolve.go) reaches the host for. The resolve ORCHESTRATION, the
// drive model, the loader, the scans, the refs fetch, and the namespace walk all run plugin-side;
// what is left here is exactly what a separate Go module structurally cannot do:
//
//   - "buildengine-connect-plugins" — registers the project's build-time plugin candies into
//     providerRegistry (clause M, the plugin-loading mechanism itself);
//   - "buildengine-context-ignore-baseline" — serves a directive read out of charly's OWN
//     //go:embed charly.yml (clause B/D — a static fact of THIS binary, unreachable from another
//     module).
//
// Class-generic action-noun kinds (never provider words — the F11 uniform-API gate), mirroring
// host_build_loader_floor.go's loader-* legs one level up the stack. The family was seven legs
// before K-wave 2 cone R1; the five that died (prep, scan-local, scan-remote, collect-remote-refs,
// ensure-repo, namespaced) were each core CALLING a mechanism on the plugin's behalf rather than
// defining one — R-items by the defines-vs-calls test. Per-leg rationale: CHANGELOG/.

// hostBuildConnectPlugins connects the project's build-time (out-of-tree) plugin candies into the host
// registry so the plugin's subsequent InvokeProvider/render dispatch reaches them (registry M). A
// plugin candy that fails to COMPILE is a FATAL error here, naming the plugin and the go build
// error — the build must not continue to a downstream 'no provider registered' failure that names
// the wrong cause (charly#326). opts is built via spec.BoxResolveOpts (R3, the shared box-selection
// rule) so req.RequestedBoxes reaches this scan's own CollectRemoteRefsOpts call too (task #17 fix)
// — an on-demand namespace-qualified build/generate target otherwise never gets its own build-time
// plugin candies discovered/connected here either, the sibling of the "unknown candy" gap this task
// closes.
func hostBuildConnectPlugins(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) (map[string]string, error) {
	dir := reqDirOrCwd(req.Dir)
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	opts := spec.BoxResolveOpts(req.RequestedBoxes, req.IncludeDisabled)
	opts.ExtraCandyRefs = req.ExtraCandyRefs
	if _, _, initCfg, derr := LoadDefaultBuildConfig(dir); derr == nil {
		opts.InitCfg = initCfg
	}
	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		return nil, err
	}
	buildRefs := collectReferencedPluginWords(layers, cfg.Box, req.ExtraCandyRefs)
	if perr := loadProjectPlugins(context.Background(), layers, buildRefs); perr != nil {
		// A plugin candy that fails to compile must stop the build with the actionable
		// error (the plugin name + the go build error are in perr via loadPluginUnit's
		// `plugin %q (source %s): %w` wrap) — NOT continue to a later 'no provider
		// registered' failure that names the wrong cause (charly#326).
		return nil, fmt.Errorf("build-time plugin load failed: %w", perr)
	}
	return map[string]string{}, nil
}

// hostBuildContextIgnoreBaseline returns the bootstrap-embedded context_ignore_baseline patterns (a
// D-category static fact baked into the charly binary's embedded charly.yml) — the ONE piece of
// writeContextIgnore's former host dependency a plugin genuinely cannot read itself (a SEPARATE Go
// module/binary has no access to charly/charly.yml's //go:embed). Everything else writeContextIgnore
// needs (cfg.Defaults.ContextIgnore, the write targets) is already plugin-side.
func hostBuildContextIgnoreBaseline(_ context.Context, _ struct{}, _ buildEngineContext) ([]string, error) {
	return baselineContextIgnore, nil
}

// baselineContextIgnore is the parsed directive the leg above serves. It lives HERE, with its only
// reader. Parsed once at package-var init; a malformed or empty embed PANICS — this is a build-time
// invariant of the binary's own embedded charly.yml, never a runtime input a project can influence.
var baselineContextIgnore = parseEmbeddedContextIgnoreBaseline()

func parseEmbeddedContextIgnoreBaseline() []string {
	var doc struct {
		ContextIgnoreBaseline []string `yaml:"context_ignore_baseline"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.ContextIgnoreBaseline) == 0 {
		panic("host build: embedded charly.yml has no context_ignore_baseline: directive")
	}
	return doc.ContextIgnoreBaseline
}

// reqDirOrCwd resolves an empty request dir to the process cwd. It was the fallback shared by every
// buildengine leg; with the family down to two it now has a single caller (hostBuildConnectPlugins),
// and stays a named helper only because it is the request-dir contract, not an inline convenience.
func reqDirOrCwd(dir string) string {
	if dir != "" {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return dir
}

// Register the buildengine-* legs at package-var init (before any init()).
var _ = func() bool {
	registerHostBuilder("buildengine-connect-plugins", typedHostBuilder("buildengine-connect-plugins", hostBuildConnectPlugins))
	registerHostBuilder("buildengine-context-ignore-baseline", typedHostBuilder("buildengine-context-ignore-baseline", hostBuildContextIgnoreBaseline))
	return true
}()
// B12 regression gate: see host_build_buildengine_test.go

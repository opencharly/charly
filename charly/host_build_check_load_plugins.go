package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// host_build_check_load_plugins.go — the THIN "check-load-plugins" host seam (K1-unblock wave, arm
// 1/live). Verb dispatch itself crosses the wire generically via InvokeProvider (S1 —
// candy/plugin-check's pluginVerbResolver), but InvokeProvider only resolves an ALREADY-CONNECTED
// provider (or a compiled-in one) — connecting an out-of-process candy is the plugin-loading
// M-mechanism (the kernel/plugin boundary law's clause M: plugin discovery/loading/connect stays
// core), so this seam is the entry point the plugin calls BEFORE dispatching a live check plan
// whose verbs may need an out-of-process candy connected. It runs the UNCHANGED core engine
// (resolveCheckRunnerContext: ScanAllCandyWithConfigOpts + collectReferencedPluginWords +
// loadProjectPlugins, charly/check_cmd.go) as a pure SIDE EFFECT on this process's own
// providerRegistry — every subsequent InvokeProvider call in this same `charly check run`
// invocation then resolves. Class-generic action noun "check-load-plugins" (F11 — never a
// substrate/provider word).
const checkLoadPluginsBuilderKind = "check-load-plugins"

func hostBuildCheckLoadPlugins(_ context.Context, req spec.CheckLoadPluginsRequest, _ buildEngineContext) (spec.CheckLoadPluginsReply, error) {
	dir := req.Dir
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return spec.CheckLoadPluginsReply{}, err
		}
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		// Best-effort, mirroring resolveCheckRunnerContext's own graceful degrade: an unresolvable
		// plugin fails loudly later, at actual verb dispatch, never here.
		return spec.CheckLoadPluginsReply{}, fmt.Errorf("check-load-plugins: %w", err)
	}
	resolveCheckRunnerContext(req.Name, dir, cfg)
	return spec.CheckLoadPluginsReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(checkLoadPluginsBuilderKind, typedHostBuilder(checkLoadPluginsBuilderKind, hostBuildCheckLoadPlugins))
	return true
}()

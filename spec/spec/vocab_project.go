package spec

import "encoding/json"

// vocab_project.go — the SHARED build-vocabulary projections. A project's `distro:`/`builder:`/`init:`
// build vocabulary lands in uf.PluginKinds["distro"|"builder"|"init"] as OPAQUE canonical bodies
// (the distro/builder/init plugin kinds); these three functions reconstruct the name-keyed build-vocab
// CONFIGS (DistroConfig/BuilderConfig/InitConfig) the build engine consumes. Pure over spec types +
// spec's own plugin-kind decoders (ResolvePluginKindViaPlugin / DecodePluginKindMap), so they live in
// the dedicated spec module (#55 2b Class A) — charly core (format_config.go) + candy/plugin-build
// both reach them without importing loaderkit, which re-exports them as forwarders.
//
// The distro/init bodies must be RESOLVED via a plugin's OpResolve leg (they are opaque post-de-type);
// that resolve is registry-coupled, so it rides in as a CALLBACK the caller supplies (charly core its
// in-proc registry invokers; plugin-build its InvokeProvider-backed callbacks — same wrapper, either
// placement). The builder bodies decode PURELY (DecodePluginKindMap), so ProjectBuilderConfig needs no
// callback.

// ProjectDistroConfig reconstructs the *DistroConfig (distro: section) from uf, resolving each opaque
// `distro` body via the caller-supplied resolveDistro callback (the distro plugin's OpResolve leg).
// Nil when no distros are configured.
func ProjectDistroConfig(uf *UnifiedFile, resolveDistro func(json.RawMessage) (*ResolvedDistro, error)) *DistroConfig {
	distros := ResolvePluginKindViaPlugin(uf, "distro", resolveDistro)
	if len(distros) == 0 {
		return nil
	}
	return &DistroConfig{Distro: distros}
}

// ProjectBuilderConfig reconstructs the *BuilderConfig (builder: section) from uf. The builder bodies
// decode purely (DecodePluginKindMap) — no OpResolve callback needed. Nil when no builders are
// configured.
func ProjectBuilderConfig(uf *UnifiedFile) *BuilderConfig {
	builders := DecodePluginKindMap[BuilderDef](uf, "builder")
	if len(builders) == 0 {
		return nil
	}
	return &BuilderConfig{Builder: builders}
}

// ProjectInitConfig reconstructs the *InitConfig (init: section) from uf, resolving each opaque `init`
// body via the caller-supplied resolveInit callback (the init plugin's OpResolve config leg). Nil when
// no init systems are configured.
func ProjectInitConfig(uf *UnifiedFile, resolveInit func(json.RawMessage) (*ResolvedInit, error)) *InitConfig {
	inits := ResolvePluginKindViaPlugin(uf, "init", resolveInit)
	if len(inits) == 0 {
		return nil
	}
	return &InitConfig{Init: inits}
}

// buildkit_aliases.go — package-main bindings onto github.com/opencharly/sdk/buildkit,
// the SDK library holding the pure Containerfile render/compute machinery (the
// format/builder TEMPLATE render surface, extracted to sdk/buildkit in P3). These thin aliases
// keep every package-main call site (generate.go, tasks.go, install_build.go,
// build_target_oci.go, distro_resolve.go, init_config.go, privileged_runner.go,
// vm_disk_builder.go, …) compiling unchanged — the build engine's render calls now
// route into the importable library. InstallContext/BuildStageContext themselves are
// CUE-sourced in sdk/schema/buildctx.cue (P2/S1) and aliased via vmshared_aliases.go.
package main

import "github.com/opencharly/sdk/buildkit"

// CacheMount is the render-time cache-mount value (distinct from the authoring
// spec.CacheMount / CacheMountDef); it lives in sdk/buildkit now.
type CacheMount = buildkit.CacheMount

// DistroConfig / BuilderConfig — the embedded build-vocabulary resolved-config
// types + their resolution methods live in sdk/buildkit (P3); loader glue
// (LoadBuildConfigForBox) + the Phase/Venue phase-template resolvers stay in charly.
type (
	DistroConfig  = buildkit.DistroConfig
	BuilderConfig = buildkit.BuilderConfig
)

var (
	SharedCacheMount      = buildkit.SharedCacheMount
	OwnedCacheMount       = buildkit.OwnedCacheMount
	RenderCacheMounts     = buildkit.RenderCacheMounts
	RenderCacheMountsAuto = buildkit.RenderCacheMountsAuto
	RenderTemplate        = buildkit.RenderTemplate
	NewInstallContext     = buildkit.NewInstallContext
	wrapDistroDef         = buildkit.WrapDistroDef
	distroTagChain        = buildkit.DistroTagChain
	templateFuncs         = buildkit.TemplateFuncs
	toStringSlice         = buildkit.ToStringSlice
	toMapSlice            = buildkit.ToMapSlice
)

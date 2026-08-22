package exec

// host_step_deps.go — the in-proc ctx-threading channel for the deploy-leg host-engine
// step bodies the wire broker (charly/plugin_executor_reverse.go's executorReverseServer)
// dispatches to a compiled-in class:step plugin (candy/plugin-installstep) over InvokeProvider
// (OpExecute). The deploy-leg bodies (deploykit.RunVenueBuilderStep / ExecLocalPkgInstall /
// RenderHostPackageCommand) need the TYPED spec.DeployExecutor (the live venue executor the
// broker holds — s.exec) plus the image-resolve/ensure closures (which close over the host
// *Config + the build-ensure dispatch — charly-internal, coneK1b's #8) and the resolved
// DistroCfg + EmitOpts. None of these cross the []byte wire: a typed interface, two Go closures,
// and a charly-internal *Config capture are all live, in-process-only values. So they ride the
// ctx the SAME in-proc reverse channel threads the venue executor on (ContextWithExecutor),
// mirroring charly/build_overlay.go's overlayBuildInputs (live plans + parent venue that "ride
// the ctx, never serialized"). The plugin recovers them via HostStepDepsFromCtx and runs the
// SAME deploykit bodies the broker used to run host-side (R3 — one body, relocated, not
// duplicated); a nil deps (an out-of-process placement, or a test that threads none) fails
// loudly at the one host-engine step that needs them, never a silent wrong result.
//
// Relocated from the github.com/opencharly/sdk root package (sdk/host_step_deps.go) as a fabric
// slice of the spec contract module (#55 import-purity). Pure Go — context + spec/spec types.

import (
	"context"

	"github.com/opencharly/spec/spec"
)

// HostStepDeps carries the live, non-serializable inputs a compiled-in class:step plugin needs
// to run a deploy-leg host-engine step body (Builder / LocalPkgInstall / SystemPackages) on the
// host venue. Set by the wire broker before InvokeProvider(ClassStep, <word>, OpExecute);
// recovered by the plugin's OpExecute handler. IN-PROC-ONLY (the typed executor + closures
// cannot cross the wire — candy/plugin-installstep is compiled-in).
type HostStepDeps struct {
	// Exec is the live typed venue executor (the broker's s.exec). deploykit.RunVenueBuilderStep
	// / ExecLocalPkgInstall / RenderHostPackageCommand's RunSystem all drive it.
	Exec spec.DeployExecutor
	// ResolveImage / EnsureImage are the image-resolve/ensure closures the BuilderStep deploy
	// leg injects into deploykit.RunVenueBuilderStep. They close over the host *Config +
	// ProjectDir (charly-internal — coneK1b's #8 keeps resolveImageRefForEnsure's definition;
	// the broker constructs the closures, the plugin only invokes them).
	ResolveImage func(string) (string, error)
	EnsureImage  func(context.Context, string) error
	// DistroCfg is the resolved distro: vocabulary the SystemPackagesStep host render
	// (deploykit.RenderHostPackageCommand) looks up the format's phase.install.host template in.
	DistroCfg *spec.DistroConfig
	// Opts are the deploy EmitOpts (DryRun / AllowRepoChanges / SkipIncompatible / …) the
	// deploy-leg bodies take.
	Opts spec.EmitOpts
}

type hostStepDepsKey struct{}

// ContextWithHostStepDeps threads the live host-step deps onto ctx for a compiled-in
// class:step plugin's OpExecute handler to recover via HostStepDepsFromCtx.
func ContextWithHostStepDeps(ctx context.Context, deps *HostStepDeps) context.Context {
	return context.WithValue(ctx, hostStepDepsKey{}, deps)
}

// HostStepDepsFromCtx recovers the threaded host-step deps (nil when absent — an out-of-process
// placement, or a ctx that never carried them).
func HostStepDepsFromCtx(ctx context.Context) *HostStepDeps {
	deps, _ := ctx.Value(hostStepDepsKey{}).(*HostStepDeps)
	return deps
}

// deploykit_aliases.go — package-main bindings onto github.com/opencharly/sdk/deploykit,
// the SDK library holding the InstallPlan step VOCABULARY (the 13 concrete InstallStep
// implementations + their field-structs + the pure classification helpers, moved out of
// install_plan.go in P4). These aliases keep every package-main call site (install_build.go
// compiler, step_view.go, the deploy targets) compiling unchanged. The InstallStep interface
// + IR enums + InstallPlan container they build on live in sdk/spec (aliased via install_plan.go).
package main

import "github.com/opencharly/sdk/deploykit"

type (
	SystemPackagesStep  = deploykit.SystemPackagesStep
	BuilderStep         = deploykit.BuilderStep
	OpStep              = deploykit.OpStep
	FileStep            = deploykit.FileStep
	ServicePackagedStep = deploykit.ServicePackagedStep
	ServiceCustomStep   = deploykit.ServiceCustomStep
	ShellHookStep       = deploykit.ShellHookStep
	ShellSnippetStep    = deploykit.ShellSnippetStep
	ApkInstallStep      = deploykit.ApkInstallStep
	LocalPkgInstallStep = deploykit.LocalPkgInstallStep
	RebootStep          = deploykit.RebootStep
	ExternalPluginStep  = deploykit.ExternalPluginStep
	externalStep        = deploykit.ExternalStep
)

var (
	opStepScope        = deploykit.OpStepScope
	isExternalStepKind = deploykit.IsExternalStepKind
	stepToView         = deploykit.StepToView
	stepFromView       = deploykit.StepFromView
	allStepKinds       = deploykit.AllStepKinds
)

// InstallPlan IR container + deploy-target/executor surface (P4): the InstallPlan
// struct + its methods (wireView/ResolveHome/StepsByVenue), EmitOpts, DeployTarget
// iface, DeployExecutor iface, BuilderRunOpts, and the HomeToken/scopeFromName/
// extractStringSlice helpers live in sdk/deploykit now. StepBatch and GateEnabled
// also live there but have no charly/ non-test consumer — read as
// deploykit.StepBatch / deploykit.GateEnabled where needed (install_plan_test.go
// uses the latter).
type (
	InstallPlan    = deploykit.InstallPlan
	EmitOpts       = deploykit.EmitOpts
	DeployTarget   = deploykit.DeployTarget
	DeployExecutor = deploykit.DeployExecutor
	BuilderRunOpts = deploykit.BuilderRunOpts
)

const HomeToken = deploykit.HomeToken

var (
	scopeFromName      = deploykit.ScopeFromName
	extractStringSlice = deploykit.ExtractStringSlice

	// InstallPlan's home-resolution + wire-projection are deploykit FREE FUNCTIONS
	// (not methods on spec.InstallPlan) because they type-switch the concrete step
	// vocabulary. These package-main aliases keep the call sites terse.
	planResolveHome = deploykit.ResolveHome
	planWireView    = deploykit.WireView
)

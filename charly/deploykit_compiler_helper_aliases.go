package main

// deploykit_compiler_helper_aliases.go — compiler helpers shared with the charly
// build path (generate.go, builder_preresolve.go), moved to deploykit with the
// compiler in P4. Charly binds onto them (P8 later moves the build path itself).

import "github.com/opencharly/sdk/deploykit"

var (
	candyNeedsBuilderStep       = deploykit.CandyNeedsBuilderStep
	compileLocalPkgStep         = deploykit.CompileLocalPkgStep
	serviceEntryAppliesToDistro = deploykit.ServiceEntryAppliesToDistro
	serviceRenderDistros        = deploykit.ServiceRenderDistros
	stringSliceFromYAML         = deploykit.StringSliceFromYAML
)
var ensureServiceSuffix = deploykit.EnsureServiceSuffix
var cascadeTagChain = deploykit.CascadeTagChain
var compileApkStep = deploykit.CompileApkStep
var compileShellHookStep = deploykit.CompileShellHookStep
var compileSystemPackageSteps = deploykit.CompileSystemPackageSteps
var buildSystemPackagesStep = deploykit.BuildSystemPackagesStep
var compileOpSteps = deploykit.CompileOpSteps
var primaryDistroTag = deploykit.PrimaryDistroTag
var resolveShellSpec = deploykit.ResolveShellSpec
var appendShellPathLines = deploykit.AppendShellPathLines

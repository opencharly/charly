package main

// deploykit_state_aliases.go — package-main bindings onto the deploy STATE-MODEL
// pure helpers (node navigation, deploy-key parsing, env/preempt/install-opts
// helpers) moved out of deploy.go to sdk/deploykit in P4. The BundleConfig struct,
// its load/save, and the loader/enc/generate-coupled glue STAY in charly/deploy.go.

import "github.com/opencharly/sdk/deploykit"

type (
	EnvProvideEntry = deploykit.EnvProvideEntry
	ProvidesConfig  = deploykit.ProvidesConfig
	BundleConfig    = deploykit.BundleConfig
	PackageSection  = deploykit.PackageSection
	TagPkgConfig    = deploykit.TagPkgConfig
	RouteConfig     = deploykit.RouteConfig
)

var (
	bundleWalkPreOrder         = deploykit.BundleWalkPreOrder
	bundleWalkPostOrder        = deploykit.BundleWalkPostOrder
	ResolveNodePath            = deploykit.ResolveNodePath
	splitDottedPath            = deploykit.SplitDottedPath
	bedCheckLiveRefs           = deploykit.BedCheckLiveRefs
	preemptEffectiveStop       = deploykit.PreemptEffectiveStop
	preemptEffectiveRestore    = deploykit.PreemptEffectiveRestore
	installOptsApplyTo         = deploykit.InstallOptsApplyTo
	deployKey                  = deploykit.DeployKey
	canonicalizeDeployArg      = deploykit.CanonicalizeDeployArg
	rejectImageRefAsDeployName = deploykit.RejectImageRefAsDeployName
	parseDeployKey             = deploykit.ParseDeployKey
	findVmDeployNode           = deploykit.FindVmDeployNode
	dropMappingKey             = deploykit.DropMappingKey
	MergeBundleNode            = deploykit.MergeBundleNode
	isAutoVmDeployEntry        = deploykit.IsAutoVmDeployEntry
	envKey                     = deploykit.EnvKey
	stripSecretEnvNames        = deploykit.StripSecretEnvNames
	mergeEnvVars               = deploykit.MergeEnvVars
	MergeDeployConfigs         = deploykit.MergeDeployConfigs
)
var podAwareEnvProvides = deploykit.PodAwareEnvProvides

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

	// Volume slice (P13/C15): VolumeMount is RESOLVED RUNTIME STATE — never marshaled (the
	// ai.opencharly.volume label is []LabelVolumeEntry; VolumeMount is built from it at
	// decode), so it is plain-Go in sdk/deploykit, NOT a spec wire type — aliased here as
	// part of the volume-slice move (10 package-main files reference it). ResolvedBindMount
	// (the same resolved-state category) stays in charly/enc.go — another cutover's
	// single-owner file (C6); it relocates to deploykit with that cutover's enc move.
	VolumeMount = deploykit.VolumeMount
)

var (
	bundleWalkPreOrder         = deploykit.BundleWalkPreOrder
	ResolveNodePath            = deploykit.ResolveNodePath
	splitDottedPath            = deploykit.SplitDottedPath
	bedCheckLiveRefs           = deploykit.BedCheckLiveRefs
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
	LoadDeployFile             = deploykit.LoadDeployFile
	RemoveBoxDeploy            = deploykit.RemoveBoxDeploy
	deployVolumePrefix         = deploykit.DeployVolumePrefix
	deployStorageDir           = deploykit.DeployStorageDir

	// P11 enc-model move: the enc-coupled volume RESOLVER + the pure enc-path cluster
	// (deploykit/deploy_volume.go). ResolveVolumeBacking is called host-side (config_image/
	// start/shell + the config-resolve seam); encryptedVolumeName feeds resolveEncVolumeDir —
	// stays core (Ruling C: deploy state). EncryptedPlainDir is consumed directly via deploykit.
	ResolveVolumeBacking = deploykit.ResolveVolumeBacking
	encryptedVolumeName  = deploykit.EncryptedVolumeName
)
var podAwareEnvProvides = deploykit.PodAwareEnvProvides

// vmshared_aliases.go — package-main bindings onto the shared VM/cloud-init
// package github.com/opencharly/sdk/vmshared. The 17 self-contained
// VM/libvirt/cloud-init source files moved into that importable package (they were
// byte-for-byte duplicated with candy/plugin-vm before — an R3 violation across the
// module boundary). These thin aliases/bindings keep every package-main reference
// compiling unchanged; the init() wires the host-side implementations of the
// package's injection seams (see vmshared/hooks.go).
package main

import "github.com/opencharly/sdk/vmshared"

type (
	AliasConfig              = vmshared.AliasConfig
	AliasYAML                = vmshared.AliasYAML
	AlpineBootstrapDef       = vmshared.AlpineBootstrapDef
	AndroidAdbEndpoint       = vmshared.AndroidAdbEndpoint
	AndroidGoogleAccount     = vmshared.AndroidGoogleAccount
	AndroidSpec              = vmshared.AndroidSpec
	ApkPackageSpec           = vmshared.ApkPackageSpec
	BaseUserDef              = vmshared.BaseUserDef
	BootloaderDef            = vmshared.BootloaderDef
	BootstrapDef             = vmshared.BootstrapDef
	BoxConfig                = vmshared.BoxConfig
	BuilderDef               = vmshared.BuilderDef
	BundleNode               = vmshared.BundleNode
	CacheMountDef            = vmshared.CacheMountDef
	CandyArtifact            = vmshared.CandyArtifact
	CandyCapabilities        = vmshared.CandyCapabilities
	CandyPluginDecl          = vmshared.CandyPluginDecl
	CandyYAML                = vmshared.CandyYAML
	CloudInitRuntimeParams   = vmshared.CloudInitRuntimeParams
	CredentialMount          = vmshared.CredentialMount
	DataYAML                 = vmshared.DataYAML
	DebootstrapDef           = vmshared.DebootstrapDef
	DeployExpose             = vmshared.DeployExpose
	DeployProbes             = vmshared.DeployProbes
	DeployResources          = vmshared.DeployResources
	DeploySecretConfig       = vmshared.DeploySecretConfig
	DeployShellOverlay       = vmshared.DeployShellOverlay
	DeployStorage            = vmshared.DeployStorage
	DeployVolumeConfig       = vmshared.DeployVolumeConfig
	DistroDef                = vmshared.DistroDef
	DistroPackages           = vmshared.DistroPackages
	DnfConfig                = vmshared.DnfConfig
	EnvDependency            = vmshared.EnvDependency
	EphemeralLifetime        = vmshared.EphemeralLifetime
	EphemeralRuntime         = vmshared.EphemeralRuntime
	ExtractYAML              = vmshared.ExtractYAML
	FormatDef                = vmshared.FormatDef
	GpuSelector              = vmshared.GpuSelector
	HooksConfig              = vmshared.HooksConfig
	HostDistro               = vmshared.HostDistro
	InitDef                  = vmshared.InitDef
	InstallOptsConfig        = vmshared.InstallOptsConfig
	IterateConfig            = vmshared.IterateConfig
	K8sDeployConfig          = vmshared.K8sDeployConfig
	K8sSpec                  = vmshared.K8sSpec
	LibvirtDevices           = vmshared.LibvirtDevices
	LibvirtDomain            = vmshared.LibvirtDomain
	LibvirtFilesystem        = vmshared.LibvirtFilesystem
	LibvirtGraphicsListeners = vmshared.LibvirtGraphicsListeners
	LibvirtHostdev           = vmshared.LibvirtHostdev
	LocalPkgDef              = vmshared.LocalPkgDef
	LocalSpec                = vmshared.LocalSpec
	Matcher                  = vmshared.Matcher
	MatcherList              = vmshared.MatcherList
	MCPServerYAML            = vmshared.MCPServerYAML
	MergeConfig              = vmshared.MergeConfig
	Op                       = vmshared.Op
	OvmfPaths                = vmshared.OvmfPaths
	PackageItem              = vmshared.PackageItem
	PacstrapDef              = vmshared.PacstrapDef
	PacstrapRepo             = vmshared.PacstrapRepo
	PhaseSet                 = vmshared.PhaseSet
	PhaseTemplates           = vmshared.PhaseTemplates
	PodSpec                  = vmshared.PodSpec
	PollClass                = vmshared.PollClass
	PortScope                = vmshared.PortScope
	PortSpec                 = vmshared.PortSpec
	PreemptibleConfig        = vmshared.PreemptibleConfig
	ReadinessConfig          = vmshared.ReadinessConfig
	ResolvedReadiness        = vmshared.ResolvedReadiness
	PollCondition            = vmshared.PollCondition
	ResourceDef              = vmshared.ResourceDef
	SecretYAML               = vmshared.SecretYAML
	SecurityConfig           = vmshared.SecurityConfig
	ServiceEntry             = vmshared.ServiceEntry
	ServiceOverrides         = vmshared.ServiceOverrides
	ServiceSchemaDef         = vmshared.ServiceSchemaDef
	ShellConfig              = vmshared.ShellConfig
	ShellSpec                = vmshared.ShellSpec
	SnapshotCreateOpts       = vmshared.SnapshotCreateOpts
	SnapshotDeleteOpts       = vmshared.SnapshotDeleteOpts
	SnapshotEntry            = vmshared.SnapshotEntry
	SnapshotRegistry         = vmshared.SnapshotRegistry
	SSHTunnel                = vmshared.SSHTunnel
	Step                     = vmshared.Step
	StepKeyword              = vmshared.StepKeyword
	TunnelYAML               = vmshared.TunnelYAML
	VmCharlyInstall          = vmshared.VmCharlyInstall
	VmCloudInit              = vmshared.VmCloudInit
	VmDeployState            = vmshared.VmDeployState
	VmKeyInjectionResolved   = vmshared.VmKeyInjectionResolved
	VmNetwork                = vmshared.VmNetwork
	VmRuntimeParams          = vmshared.VmRuntimeParams
	VmSource                 = vmshared.VmSource
	VmSpec                   = vmshared.VmSpec
	VmSSH                    = vmshared.VmSSH
	VolumeYAML               = vmshared.VolumeYAML
)

// readinessResolve aliases the shared config→resolved readiness resolver — the logic + the
// CHARLY_READINESS_* field table live ONCE in vmshared (FU-9), shared with the out-of-process
// plugins; loadedReadiness (readiness_config.go) feeds it the project's defaults.readiness.
var readinessResolve = vmshared.ResolveReadiness

var (
	CompareGlibc                = vmshared.CompareGlibc
	CreateSnapshot              = vmshared.CreateSnapshot
	currentUsername             = vmshared.CurrentUsername
	DecrementSnapshotRefcount   = vmshared.DecrementSnapshotRefcount
	DeleteSnapshot              = vmshared.DeleteSnapshot
	DetectHostDistro            = vmshared.DetectHostDistro
	DetectHostGlibc             = vmshared.DetectHostGlibc
	ErrPollFatal                = vmshared.ErrPollFatal
	formatForDistroID           = vmshared.FormatForDistroID
	IncrementSnapshotRefcount   = vmshared.IncrementSnapshotRefcount
	InstallSignalHandler        = vmshared.InstallSignalHandler
	ListSnapshots               = vmshared.ListSnapshots
	loadRegistry                = vmshared.LoadRegistry
	LookupSnapshot              = vmshared.LookupSnapshot
	NewSSHTunnel                = vmshared.NewSSHTunnel
	ovmfCandidatesForDistro     = vmshared.OvmfCandidatesForDistro
	parseGlibcVersion           = vmshared.ParseGlibcVersion
	ParseLibvirtURI             = vmshared.ParseLibvirtURI
	ParseSSHTarget              = vmshared.ParseSSHTarget
	pollUntil                   = vmshared.PollUntil
	PromoteSnapshot             = vmshared.PromoteSnapshot
	RegisterShutdownHook        = vmshared.RegisterShutdownHook
	RegisterTempCleanup         = vmshared.RegisterTempCleanup
	registryPath                = vmshared.RegistryPath
	RenderCloudInit             = vmshared.RenderCloudInit
	RenderQemuArgv              = vmshared.RenderQemuArgv
	ResolveKeyInjectionChannels = vmshared.ResolveKeyInjectionChannels
	ResolveOvmfForSpec          = vmshared.ResolveOvmfForSpec
	ResolveOvmfPaths            = vmshared.ResolveOvmfPaths
	RevertSnapshot              = vmshared.RevertSnapshot
	saveRegistry                = vmshared.SaveRegistry
	SmbiosCredForSSH            = vmshared.SmbiosCredForSSH
	snapshotsDir                = vmshared.SnapshotsDir
	splitOsReleaseLine          = vmshared.SplitOsReleaseLine
	SweepStaleTemps             = vmshared.SweepStaleTemps
	UnregisterTempCleanup       = vmshared.UnregisterTempCleanup
	WriteSeedISO                = vmshared.WriteSeedISO
)

// Pure VM helper functions consolidated into vmshared (vm_helpers.go) — the
// former core↔plugin byte-for-byte duplication (FU-10). These aliases keep the
// package-main call sites unchanged.
var (
	resolveVmRam                   = vmshared.ResolveVmRam
	resolveVmCpus                  = vmshared.ResolveVmCpus
	detectRuntimeHostVendor        = vmshared.DetectRuntimeHostVendor
	qemuSystemBinary               = vmshared.QemuSystemBinary
	vmDiskDir                      = vmshared.VmDiskDir
	killQemuByPID                  = vmshared.KillQemuByPID
	libvirtSessionSocket           = vmshared.LibvirtSessionSocket
	libvirtSessionSocketWithProbes = vmshared.LibvirtSessionSocketWithProbes
	writeJSON                      = vmshared.WriteJSON
	isDeviceElement                = vmshared.IsDeviceElement
	ValidateLibvirtSnippet         = vmshared.ValidateLibvirtSnippet
)

const (
	PollHeavy                        = vmshared.PollHeavy
	PollLocal                        = vmshared.PollLocal
	PollRemote                       = vmshared.PollRemote
	readinessAbsoluteCapFallback     = vmshared.ReadinessAbsoluteCapFallback
	readinessNoProgressFallback      = vmshared.ReadinessNoProgressFallback
	readinessPerAttemptFallback      = vmshared.ReadinessPerAttemptFallback
	readinessPerAttemptHeavyFallback = vmshared.ReadinessPerAttemptHeavyFallback
	readinessStopGraceFallback       = vmshared.ReadinessStopGraceFallback
)

func init() {
	vmshared.ValidateEgress = ValidateEgress
	vmshared.UnmarshalEmbeddedDefaults = unmarshalEmbeddedDefaults
	vmshared.CreateInternalSnapshot = createInternalSnapshot
	vmshared.DeleteInternalSnapshot = deleteInternalSnapshot
	vmshared.RevertInternalSnapshot = revertInternalSnapshot
	vmshared.PromoteInternalToExternal = promoteInternalToExternal
	vmshared.CreateExternalSnapshot = createExternalSnapshot
	vmshared.DeleteExternalSnapshot = deleteExternalSnapshot
	vmshared.RevertExternalSnapshot = revertExternalSnapshot
}

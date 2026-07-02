// Code-assisted alias surface for the CUE-single-source cutover (WF-B THE REPOINT).
//
// Every charly hand param struct that has a spec counterpart is now a zero-churn
// Go type alias onto charly/spec — the generated (cue exp gengotypes) types plus
// the hand-written union/state types, exposed under their charly names via
// spec/charly_names.go. The hand struct DEFINITIONS were deleted; these aliases
// keep every existing reference (`BoxConfig{…}`, `[]ServiceEntry`, …) compiling
// against the single CUE source of truth. Pure methods moved INTO package spec;
// methods that reach package-main internals are free functions here taking a
// *spec.X. See CHANGELOG.
//
// NOT aliased (name collisions where the package-main type is a DIFFERENT
// concept, kept hand-written): CalVer (version.go computation struct, vs the
// spec.CalVer scalar), CandyRef (refs.go ref struct, vs the spec.CandyRef
// scalar), Candy (layers.go resolved-candy runtime struct, vs spec.Candy the
// param — aliased as CandyYAML instead).
package vmshared

import "github.com/opencharly/charly/charly/spec"

type (
	AliasConfig              = spec.AliasConfig
	AliasYAML                = spec.AliasYAML
	AndroidSpec              = spec.AndroidSpec
	ApkPackageSpec           = spec.ApkPackageSpec
	BoxConfig                = spec.BoxConfig
	BuilderDef               = spec.BuilderDef
	BundleNode               = spec.BundleNode
	CacheMountDef            = spec.CacheMount
	CandyArtifact            = spec.CandyArtifact
	CandyArtifactRewrite     = spec.CandyArtifactRewrite
	CandyCapabilities        = spec.CandyCapabilities
	CandyPluginDecl          = spec.Plugin
	CandyYAML                = spec.CandyYAML
	CredentialMount          = spec.CredentialMount
	DataYAML                 = spec.DataYAML
	DebootstrapRepo          = spec.DebootstrapRepo
	DeployExpose             = spec.DeployExpose
	DeployProbes             = spec.DeployProbes
	DeployResources          = spec.DeployResources
	DeployShellOverlay       = spec.DeployShellOverlay
	DeployStorage            = spec.DeployStorage
	DistroDef                = spec.DistroDef
	DistroPackages           = spec.DistroPackages
	EnvDependency            = spec.EnvDependency
	EphemeralLifetime        = spec.EphemeralLifetime
	EphemeralRuntime         = spec.EphemeralRuntime
	ExtractYAML              = spec.ExtractYAML
	FormatRule               = spec.FormatRule
	GpuSelector              = spec.GpuSelector
	HooksConfig              = spec.HooksConfig
	InitDef                  = spec.InitDef
	InstallOptsConfig        = spec.InstallOptsConfig
	IterateConfig            = spec.IterateConfig
	K8sGatewayAPI            = spec.K8sGatewayAPI
	K8sHostname              = spec.K8sHostname
	K8sImagesDefaults        = spec.K8sImagesDefaults
	K8sIngressDefaults       = spec.K8sIngressDefaults
	K8sObservability         = spec.K8sObservability
	K8sPatch                 = spec.K8sPatch
	K8sPodDefaults           = spec.K8sPodDefaults
	K8sResourceDefaults      = spec.K8sResourceDefaults
	K8sResources             = spec.K8sResources
	K8sResourceValues        = spec.K8sResourceValues
	K8sSecretsBackend        = spec.K8sSecretsBackend
	K8sSpec                  = spec.K8sSpec
	K8sStorage               = spec.K8sStorage
	LibvirtAudio             = spec.LibvirtAudio
	LibvirtChannel           = spec.LibvirtChannel
	LibvirtClock             = spec.LibvirtClock
	LibvirtConsole           = spec.LibvirtConsole
	LibvirtCPU               = spec.LibvirtCPU
	LibvirtCPUCache          = spec.LibvirtCPUCache
	LibvirtCPUFeature        = spec.LibvirtCPUFeature
	LibvirtCPUTopology       = spec.LibvirtCPUTopology
	LibvirtCPUTune           = spec.LibvirtCPUTune
	LibvirtDevices           = spec.LibvirtDevices
	LibvirtDisk              = spec.LibvirtDisk
	LibvirtDomain            = spec.LibvirtDomain
	LibvirtEmulatorPin       = spec.LibvirtEmulatorPin
	LibvirtFeatures          = spec.LibvirtFeatures
	LibvirtFilesystem        = spec.LibvirtFilesystem
	LibvirtGraphics          = spec.LibvirtGraphics
	LibvirtGraphicsListen    = spec.LibvirtGraphicsListen
	LibvirtGraphicsListeners = spec.LibvirtGraphicsListeners
	LibvirtHostdev           = spec.LibvirtHostdev
	LibvirtHub               = spec.LibvirtHub
	LibvirtHugepages         = spec.LibvirtHugepages
	LibvirtHyperV            = spec.LibvirtHyperV
	LibvirtInput             = spec.LibvirtInput
	LibvirtInterface         = spec.LibvirtInterface
	LibvirtIOMMU             = spec.LibvirtIOMMU
	LibvirtIOThreadPin       = spec.LibvirtIOThreadPin
	LibvirtKVM               = spec.LibvirtKVM
	LibvirtLaunchSecurity    = spec.LibvirtLaunchSecurity
	LibvirtMemBalloon        = spec.LibvirtMemBalloon
	LibvirtMemnode           = spec.LibvirtMemnode
	LibvirtMemoryBacking     = spec.LibvirtMemoryBacking
	LibvirtMemTune           = spec.LibvirtMemTune
	LibvirtNUMACell          = spec.LibvirtNUMACell
	LibvirtNUMAMemory        = spec.LibvirtNUMAMemory
	LibvirtNUMATune          = spec.LibvirtNUMATune
	LibvirtPanic             = spec.LibvirtPanic
	LibvirtParallel          = spec.LibvirtParallel
	LibvirtPortForward       = spec.LibvirtPortForward
	LibvirtRedirDev          = spec.LibvirtRedirDev
	LibvirtResource          = spec.LibvirtResource
	LibvirtRNG               = spec.LibvirtRNG
	LibvirtSecLabel          = spec.LibvirtSecLabel
	LibvirtSerial            = spec.LibvirtSerial
	LibvirtShmem             = spec.LibvirtShmem
	LibvirtSmartcard         = spec.LibvirtSmartcard
	LibvirtSound             = spec.LibvirtSound
	LibvirtSpinlocks         = spec.LibvirtSpinlocks
	LibvirtSysInfo           = spec.LibvirtSysInfo
	LibvirtTimer             = spec.LibvirtTimer
	LibvirtTPM               = spec.LibvirtTPM
	LibvirtUSB               = spec.LibvirtUSB
	LibvirtVCPUPin           = spec.LibvirtVCPUPin
	LibvirtVendorID          = spec.LibvirtVendorID
	LibvirtVideo             = spec.LibvirtVideo
	LibvirtVsock             = spec.LibvirtVsock
	LibvirtWatchdog          = spec.LibvirtWatchdog
	LocalSpec                = spec.LocalSpec
	Matcher                  = spec.Matcher
	MatcherList              = spec.MatcherList
	MCPServerYAML            = spec.MCPServerYAML
	MergeConfig              = spec.MergeConfig
	Op                       = spec.Op
	PackageItem              = spec.PackageItem
	PacstrapRepo             = spec.PacstrapRepo
	PhaseSet                 = spec.PhaseSet
	PhaseTemplates           = spec.PhaseTemplates
	PodSpec                  = spec.PodSpec
	PortScope                = spec.PortScope
	PortSpec                 = spec.PortSpec
	PreemptibleConfig        = spec.PreemptibleConfig
	ReadinessConfig          = spec.ReadinessConfig
	ResourceDef              = spec.ResourceDef
	RouteYAML                = spec.RouteYAML
	SecretYAML               = spec.SecretYAML
	SecurityConfig           = spec.SecurityConfig
	ServiceEntry             = spec.ServiceEntry
	ShellSpec                = spec.ShellSpec
	SidecarDef               = spec.SidecarDef
	SidecarSecret            = spec.SidecarSecret
	SidecarVolume            = spec.SidecarVolume
	Step                     = spec.Step
	StepKeyword              = spec.StepKeyword
	TargetInstance           = spec.TargetInstance
	TargetSequence           = spec.TargetSequence
	TargetSpec               = spec.TargetSpec
	TunnelYAML               = spec.TunnelYAML
	VmCharlyInstall          = spec.VmCharlyInstall
	VmChecksum               = spec.VmChecksum
	VmCloudInit              = spec.VmCloudInit
	VmCloudInitFile          = spec.VmCloudInitFile
	VmCloudInitMirrors       = spec.VmCloudInitMirrors
	VmCloudInitNetwork       = spec.VmCloudInitNetwork
	VmCloudInitUser          = spec.VmCloudInitUser
	VmDeployState            = spec.VmDeployState
	VmKeyInjection           = spec.VmKeyInjection
	VmKeyInjectionResolved   = spec.VmKeyInjectionResolved
	VmNetwork                = spec.VmNetwork
	VmSnapshotDecl           = spec.VmSnapshotDecl
	VmSnapshotState          = spec.VmSnapshotState
	VmSource                 = spec.VmSource
	VmSpec                   = spec.VmSpec
	VmSSH                    = spec.VmSSH
	VolumeYAML               = spec.VolumeYAML

	// --- nested types renamed in spec (charly name != cue-def name) ---
	AndroidAdbEndpoint   = spec.AdbEndpoint
	AgentConfig          = spec.Agent
	AlpineBootstrapDef   = spec.AlpineBootstrap
	AURPackages          = spec.AUR
	BaseUserDef          = spec.BaseUser
	BootloaderDef        = spec.Bootloader
	BootstrapDef         = spec.Bootstrap
	ServiceOverrides     = spec.CandyServiceOverrides
	CopyDef              = spec.Copy
	DebootstrapDef       = spec.Debootstrap
	DeploySecretConfig   = spec.DeploySecret
	DeployVolumeConfig   = spec.DeployVolume
	DnfConfig            = spec.Dnf
	FormatDef            = spec.Format
	AndroidGoogleAccount = spec.GoogleAccount
	K8sDeployConfig      = spec.K8sDeploy
	LocalPkgDef          = spec.LocalPkg
	PacstrapDef          = spec.Pacstrap
	SidecarConfig        = spec.PodSidecar
	ShellConfig          = spec.Shell
	ServiceSchemaDef     = spec.InitServiceSchema
)

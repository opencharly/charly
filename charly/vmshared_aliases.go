// vmshared_aliases.go — package-main bindings onto the shared VM/cloud-init
// package github.com/opencharly/sdk/vmshared. The 17 self-contained
// VM/libvirt/cloud-init source files moved into that importable package (they were
// byte-for-byte duplicated with candy/plugin-vm before — an R3 violation across the
// module boundary). These thin aliases/bindings keep every package-main reference
// compiling unchanged; the init() wires the host-side implementations of the
// package's injection seams (see vmshared/hooks.go).
package main

import (
	"github.com/opencharly/sdk/sshx"
	"github.com/opencharly/sdk/vmshared"
)

type (
	AliasConfig            = vmshared.AliasConfig
	AliasYAML              = vmshared.AliasYAML
	AlpineBootstrapDef     = vmshared.AlpineBootstrapDef
	AndroidAdbEndpoint     = vmshared.AndroidAdbEndpoint
	AndroidGoogleAccount   = vmshared.AndroidGoogleAccount
	ApkPackageSpec         = vmshared.ApkPackageSpec
	BaseUserDef            = vmshared.BaseUserDef
	BootloaderDef          = vmshared.BootloaderDef
	BootstrapDef           = vmshared.BootstrapDef
	BuilderDef             = vmshared.BuilderDef
	CacheMountDef          = vmshared.CacheMountDef
	CandyArtifact          = vmshared.CandyArtifact
	CandyCapabilities      = vmshared.CandyCapabilities
	CloudInitRuntimeParams = vmshared.CloudInitRuntimeParams
	CredentialMount        = vmshared.CredentialMount
	DataYAML               = vmshared.DataYAML
	DebootstrapDef         = vmshared.DebootstrapDef
	DeployExpose           = vmshared.DeployExpose
	DeployProbes           = vmshared.DeployProbes
	DeployResources        = vmshared.DeployResources
	DeploySecretConfig     = vmshared.DeploySecretConfig
	DeployShellOverlay     = vmshared.DeployShellOverlay
	DeployStorage          = vmshared.DeployStorage
	DeployVolumeConfig     = vmshared.DeployVolumeConfig
	DistroPackages         = vmshared.DistroPackages
	DnfConfig              = vmshared.DnfConfig
	EphemeralLifetime      = vmshared.EphemeralLifetime
	EphemeralRuntime       = vmshared.EphemeralRuntime
	ExtractYAML            = vmshared.ExtractYAML
	FormatDef              = vmshared.FormatDef
	HooksConfig            = vmshared.HooksConfig
	HostDistro             = vmshared.HostDistro
	InstallOptsConfig      = vmshared.InstallOptsConfig
	IterateConfig          = vmshared.IterateConfig
	K8sDeployConfig        = vmshared.K8sDeployConfig
	LibvirtDevices         = vmshared.LibvirtDevices
	LibvirtFilesystem      = vmshared.LibvirtFilesystem
	LibvirtHostdev         = vmshared.LibvirtHostdev
	LocalPkgDef            = vmshared.LocalPkgDef
	MergeConfig            = vmshared.MergeConfig
	Op                     = vmshared.Op
	OvmfPaths              = vmshared.OvmfPaths
	PacstrapDef            = vmshared.PacstrapDef
	PacstrapRepo           = vmshared.PacstrapRepo
	PhaseSet               = vmshared.PhaseSet
	PhaseTemplates         = vmshared.PhaseTemplates
	PollClass              = vmshared.PollClass
	ReadinessConfig        = vmshared.ReadinessConfig
	ResolvedReadiness      = vmshared.ResolvedReadiness
	PollCondition          = vmshared.PollCondition
	SecretYAML             = vmshared.SecretYAML
	SecurityConfig         = vmshared.SecurityConfig
	ServiceOverrides       = vmshared.ServiceOverrides
	ServiceSchemaDef       = vmshared.ServiceSchemaDef
	ShellSpec              = vmshared.ShellSpec
	SnapshotCreateOpts     = vmshared.SnapshotCreateOpts
	SnapshotDeleteOpts     = vmshared.SnapshotDeleteOpts
	SnapshotEntry          = vmshared.SnapshotEntry
	SnapshotRegistry       = vmshared.SnapshotRegistry
	SSHTunnel              = sshx.SSHTunnel
	StepKeyword            = vmshared.StepKeyword
	VmCharlyInstall        = vmshared.VmCharlyInstall
	VmCloudInit            = vmshared.VmCloudInit
	VmKeyInjectionResolved = vmshared.VmKeyInjectionResolved
	VmNetwork              = vmshared.VmNetwork
	VmRuntimeParams        = vmshared.VmRuntimeParams
	VmSource               = vmshared.VmSource
	VmSpec                 = vmshared.VmSpec
	VmSSH                  = vmshared.VmSSH
	VolumeYAML             = vmshared.VolumeYAML
)

// readinessResolve aliases the shared config→resolved readiness resolver — the logic + the
// CHARLY_READINESS_* field table live ONCE in vmshared (FU-9), shared with the out-of-process
// plugins; loadedReadiness (readiness_config.go) feeds it the project's defaults.readiness.
var readinessResolve = vmshared.ResolveReadiness

var (
	CompareGlibc                = vmshared.CompareGlibc
	CreateSnapshot              = vmshared.CreateSnapshot
	DecrementSnapshotRefcount   = vmshared.DecrementSnapshotRefcount
	DeleteSnapshot              = vmshared.DeleteSnapshot
	DetectHostDistro            = vmshared.DetectHostDistro
	DetectHostGlibc             = vmshared.DetectHostGlibc
	ErrPollFatal                = vmshared.ErrPollFatal
	formatForDistroID           = vmshared.FormatForDistroID
	IncrementSnapshotRefcount   = vmshared.IncrementSnapshotRefcount
	InstallSignalHandler        = vmshared.InstallSignalHandler
	ListSnapshots               = vmshared.ListSnapshots
	LookupSnapshot              = vmshared.LookupSnapshot
	NewSSHTunnel                = sshx.NewSSHTunnel
	ovmfCandidatesForDistro     = vmshared.OvmfCandidatesForDistro
	parseGlibcVersion           = vmshared.ParseGlibcVersion
	ParseLibvirtURI             = vmshared.ParseLibvirtURI
	ParseSSHTarget              = vmshared.ParseSSHTarget
	pollUntil                   = vmshared.PollUntil
	PromoteSnapshot             = vmshared.PromoteSnapshot
	RegisterShutdownHook        = vmshared.RegisterShutdownHook
	RegisterTempCleanup         = vmshared.RegisterTempCleanup
	RenderCloudInit             = vmshared.RenderCloudInit
	RenderQemuArgv              = vmshared.RenderQemuArgv
	ResolveKeyInjectionChannels = vmshared.ResolveKeyInjectionChannels
	ResolveOvmfForSpec          = vmshared.ResolveOvmfForSpec
	ResolveOvmfPaths            = vmshared.ResolveOvmfPaths
	RevertSnapshot              = vmshared.RevertSnapshot
	SmbiosCredForSSH            = vmshared.SmbiosCredForSSH
	splitOsReleaseLine          = vmshared.SplitOsReleaseLine
	SweepStaleTemps             = vmshared.SweepStaleTemps
	UnregisterTempCleanup       = vmshared.UnregisterTempCleanup
	WriteSeedISO                = vmshared.WriteSeedISO
)

// Pure VM helper functions consolidated into vmshared (vm_helpers.go) — the
// former core↔plugin byte-for-byte duplication (FU-10). These aliases keep the
// package-main call sites unchanged.
var (
	qemuSystemBinary               = vmshared.QemuSystemBinary
	vmDiskDir                      = vmshared.VmDiskDir
	vmDomainIdentity               = vmshared.VmDomainIdentity
	killQemuByPID                  = vmshared.KillQemuByPID
	libvirtSessionSocket           = vmshared.LibvirtSessionSocket
	libvirtSessionSocketWithProbes = vmshared.LibvirtSessionSocketWithProbes
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

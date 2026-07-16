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
	AliasYAML                = vmshared.AliasYAML
	AndroidGoogleAccount     = vmshared.AndroidGoogleAccount
	ApkPackageSpec           = vmshared.ApkPackageSpec
	BaseUserDef              = vmshared.BaseUserDef
	BoxConfig                = vmshared.BoxConfig
	BuildStageContext        = vmshared.BuildStageContext
	BuilderDef               = vmshared.BuilderDef
	BundleNode               = vmshared.BundleNode
	InstallContext           = vmshared.InstallContext
	CacheMountDef            = vmshared.CacheMountDef
	CandyArtifact            = vmshared.CandyArtifact
	CandyCapabilities        = vmshared.CandyCapabilities
	CandyPluginDecl          = vmshared.CandyPluginDecl
	CandyYAML                = vmshared.CandyYAML
	CloudInitRuntimeParams   = vmshared.CloudInitRuntimeParams
	DataYAML                 = vmshared.DataYAML
	DeployShellOverlay       = vmshared.DeployShellOverlay
	DeployVolumeConfig       = vmshared.DeployVolumeConfig
	DistroDef                = vmshared.DistroDef
	EnvDependency            = vmshared.EnvDependency
	EphemeralRuntime         = vmshared.EphemeralRuntime
	ExtractYAML              = vmshared.ExtractYAML
	FormatDef                = vmshared.FormatDef
	HooksConfig              = vmshared.HooksConfig
	InstallOptsConfig        = vmshared.InstallOptsConfig
	K8sDeployConfig          = vmshared.K8sDeployConfig
	LibvirtDomain            = vmshared.LibvirtDomain
	LibvirtGraphicsListeners = vmshared.LibvirtGraphicsListeners
	LocalPkgDef              = vmshared.LocalPkgDef
	Matcher                  = vmshared.Matcher
	MatcherList              = vmshared.MatcherList
	MCPServerYAML            = vmshared.MCPServerYAML
	Op                       = vmshared.Op
	PackageItem              = vmshared.PackageItem
	PacstrapDef              = vmshared.PacstrapDef
	PortScope                = vmshared.PortScope
	PortSpec                 = vmshared.PortSpec
	PreemptibleConfig        = vmshared.PreemptibleConfig
	ReadinessConfig          = vmshared.ReadinessConfig
	ResolvedReadiness        = vmshared.ResolvedReadiness
	SecretYAML               = vmshared.SecretYAML
	SecurityConfig           = vmshared.SecurityConfig
	ServiceEntry             = vmshared.ServiceEntry
	ServiceOverrides         = vmshared.ServiceOverrides
	ShellConfig              = vmshared.ShellConfig
	ShellSpec                = vmshared.ShellSpec
	SnapshotCreateOpts       = vmshared.SnapshotCreateOpts
	SnapshotEntry            = vmshared.SnapshotEntry
	SSHTunnel                = sshx.SSHTunnel
	Step                     = vmshared.Step
	StepKeyword              = vmshared.StepKeyword
	TunnelYAML               = vmshared.TunnelYAML
	VmDeployState            = vmshared.VmDeployState
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
	DecrementSnapshotRefcount   = vmshared.DecrementSnapshotRefcount
	DetectHostDistro            = vmshared.DetectHostDistro
	DetectHostGlibc             = vmshared.DetectHostGlibc
	ErrPollFatal                = vmshared.ErrPollFatal
	IncrementSnapshotRefcount   = vmshared.IncrementSnapshotRefcount
	InstallSignalHandler        = vmshared.InstallSignalHandler
	NewSSHTunnel                = sshx.NewSSHTunnel
	ParseLibvirtURI             = vmshared.ParseLibvirtURI
	pollUntil                   = vmshared.PollUntil
	RegisterShutdownHook        = vmshared.RegisterShutdownHook
	RegisterTempCleanup         = vmshared.RegisterTempCleanup
	RenderCloudInit             = vmshared.RenderCloudInit
	ResolveKeyInjectionChannels = vmshared.ResolveKeyInjectionChannels
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
)

const (
	PollLocal  = vmshared.PollLocal
	PollRemote = vmshared.PollRemote
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

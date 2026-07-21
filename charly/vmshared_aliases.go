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
	AndroidGoogleAccount   = vmshared.AndroidGoogleAccount
	ApkPackageSpec         = vmshared.ApkPackageSpec
	BaseUserDef            = vmshared.BaseUserDef
	BuilderDef             = vmshared.BuilderDef
	CacheMountDef          = vmshared.CacheMountDef
	CandyArtifact          = vmshared.CandyArtifact
	CloudInitRuntimeParams = vmshared.CloudInitRuntimeParams
	DeployShellOverlay     = vmshared.DeployShellOverlay
	DeployVolumeConfig     = vmshared.DeployVolumeConfig
	EphemeralRuntime       = vmshared.EphemeralRuntime
	FormatDef              = vmshared.FormatDef
	HooksConfig            = vmshared.HooksConfig
	InstallOptsConfig      = vmshared.InstallOptsConfig
	K8sDeployConfig        = vmshared.K8sDeployConfig
	LocalPkgDef            = vmshared.LocalPkgDef
	PacstrapDef            = vmshared.PacstrapDef
	ReadinessConfig        = vmshared.ReadinessConfig
	ResolvedReadiness      = vmshared.ResolvedReadiness
	SecurityConfig         = vmshared.SecurityConfig
	ServiceOverrides       = vmshared.ServiceOverrides
	ShellSpec              = vmshared.ShellSpec
	SSHTunnel              = sshx.SSHTunnel
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
	DetectHostDistro      = vmshared.DetectHostDistro
	DetectHostGlibc       = vmshared.DetectHostGlibc
	ErrPollFatal          = vmshared.ErrPollFatal
	InstallSignalHandler  = vmshared.InstallSignalHandler
	NewSSHTunnel          = sshx.NewSSHTunnel
	ParseLibvirtURI       = vmshared.ParseLibvirtURI
	pollUntil             = vmshared.PollUntil
	RegisterShutdownHook  = vmshared.RegisterShutdownHook
	RegisterTempCleanup   = vmshared.RegisterTempCleanup
	RenderCloudInit       = vmshared.RenderCloudInit
	SweepStaleTemps       = vmshared.SweepStaleTemps
	UnregisterTempCleanup = vmshared.UnregisterTempCleanup
)

// Pure VM helper functions consolidated into vmshared (vm_helpers.go) — the
// former core↔plugin byte-for-byte duplication (FU-10). These aliases keep the
// package-main call sites unchanged.
var (
	qemuSystemBinary               = vmshared.QemuSystemBinary
	vmDiskDir                      = vmshared.VmDiskDir
	vmDomainIdentity               = vmshared.VmDomainIdentity
	killQemuByPID                  = vmshared.KillQemuByPID
	libvirtSessionSocketWithProbes = vmshared.LibvirtSessionSocketWithProbes
)

const (
	PollLocal  = vmshared.PollLocal
	PollRemote = vmshared.PollRemote
)

func init() {
	vmshared.ValidateEgress = ValidateEgress
	vmshared.UnmarshalEmbeddedDefaults = unmarshalEmbeddedDefaults
}

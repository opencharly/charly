package vm

import (
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// vm_move_aliases.go — package-vm bindings for the shared types the VM CLI handlers (moved out of
// charly core, P10) reference by their core short names. Same identity core used via its own alias
// surface (charly/vmshared_aliases.go / buildkit_aliases.go): the shared model lives ONCE in
// sdk/vmshared + sdk/buildkit; these keep the moved handlers compiling unchanged.
type (
	VmDeployState = vmshared.VmDeployState
	DistroDef     = vmshared.DistroDef
	BuilderDef    = vmshared.BuilderDef
	FormatDef     = vmshared.FormatDef
	BaseUserDef   = vmshared.BaseUserDef
	DistroConfig  = buildkit.DistroConfig
	BuilderConfig = buildkit.BuilderConfig
	ResolvedBox   = buildkit.ResolvedBox

	ResolvedRuntime = kit.ResolvedRuntime
	SSHExecutor     = kit.SSHExecutor
	DeployExecutor  = deploykit.DeployExecutor
	EmitOpts        = deploykit.EmitOpts
	VFIOGpu         = spec.VFIOGpu

	CloudInitRuntimeParams = vmshared.CloudInitRuntimeParams
	VmCloudInit            = vmshared.VmCloudInit
	SnapshotDeleteOpts     = vmshared.SnapshotDeleteOpts
	VmSshStanza            = kit.VmSshStanza
)

// Function/value aliases the moved handlers reference — the shared impls live once in vmshared/kit/deploykit.
var (
	RenderCloudInit             = vmshared.RenderCloudInit
	ResolveOvmfForSpec          = vmshared.ResolveOvmfForSpec
	ResolveKeyInjectionChannels = vmshared.ResolveKeyInjectionChannels
	WriteSeedISO                = vmshared.WriteSeedISO
	resolveVmRam                = vmshared.ResolveVmRam
	resolveVmCpus               = vmshared.ResolveVmCpus
	detectRuntimeHostVendor     = vmshared.DetectRuntimeHostVendor
	killQemuByPID               = vmshared.KillQemuByPID
	CreateSnapshot              = vmshared.CreateSnapshot
	ListSnapshots               = vmshared.ListSnapshots
	LookupSnapshot              = vmshared.LookupSnapshot
	PromoteSnapshot             = vmshared.PromoteSnapshot
	RevertSnapshot              = vmshared.RevertSnapshot
	IncrementSnapshotRefcount   = vmshared.IncrementSnapshotRefcount
	deployShellQuote            = kit.ShellQuote
	EnsureSshConfigInclude      = kit.EnsureSshConfigInclude
	RemoveSshConfigInclude      = kit.RemoveSshConfigInclude
	RemoveVmSshStanza           = kit.RemoveVmSshStanza
	ListVmSshAliases            = kit.ListVmSshAliases
	VmSshAlias                  = kit.VmSshAlias
	WriteVmSshStanza            = kit.WriteVmSshStanza
	deployKey                   = deploykit.DeployKey
	sshParamsForVm              = deploykit.SSHParamsForVm
	vmDiskDir                   = vmshared.VmDiskDir
	parseTaskMode               = kit.ParseTaskMode
)

// AutoDetectFlags is the plugin-local copy of core's --no-autodetect Kong flag struct. Core keeps its
// own (shell/start/config_image commands embed it too), so it is NOT moved; this trivial one-field
// CLI-flags struct is below the bar for cross-module export (R3 — the vm_phaseA_shims trivia line).
type AutoDetectFlags struct {
	NoAutoDetect bool `long:"no-autodetect" help:"Disable automatic device detection"`
}

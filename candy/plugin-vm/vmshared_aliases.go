// vmshared_aliases.go — package-main bindings onto the shared VM/cloud-init
// package github.com/opencharly/sdk/vmshared, imported via the go.work
// replace of the charly module. The 17 shared VM/libvirt/cloud-init files used to be
// byte-for-byte duplicated here; they now live ONCE in vmshared. These bindings keep
// the plugin's package-main references compiling; the init() wires the plugin-side
// (in-process go-libvirt) implementations of the shared package's seams.
package vm

import (
	"github.com/opencharly/sdk/proclifecycle"
	"github.com/opencharly/sdk/sshx"
	"github.com/opencharly/sdk/vmshared"
)

type (
	LibvirtAudio             = vmshared.LibvirtAudio
	LibvirtChannel           = vmshared.LibvirtChannel
	LibvirtClock             = vmshared.LibvirtClock
	LibvirtConsole           = vmshared.LibvirtConsole
	LibvirtCPUTune           = vmshared.LibvirtCPUTune
	LibvirtDevices           = vmshared.LibvirtDevices
	LibvirtDisk              = vmshared.LibvirtDisk
	LibvirtDomain            = vmshared.LibvirtDomain
	LibvirtFeatures          = vmshared.LibvirtFeatures
	LibvirtFilesystem        = vmshared.LibvirtFilesystem
	LibvirtGraphics          = vmshared.LibvirtGraphics
	LibvirtGraphicsListen    = vmshared.LibvirtGraphicsListen
	LibvirtGraphicsListeners = vmshared.LibvirtGraphicsListeners
	LibvirtHostdev           = vmshared.LibvirtHostdev
	LibvirtHyperV            = vmshared.LibvirtHyperV
	LibvirtInterface         = vmshared.LibvirtInterface
	LibvirtIOMMU             = vmshared.LibvirtIOMMU
	LibvirtKVM               = vmshared.LibvirtKVM
	LibvirtLaunchSecurity    = vmshared.LibvirtLaunchSecurity
	LibvirtMemBalloon        = vmshared.LibvirtMemBalloon
	LibvirtMemoryBacking     = vmshared.LibvirtMemoryBacking
	LibvirtMemTune           = vmshared.LibvirtMemTune
	LibvirtNUMATune          = vmshared.LibvirtNUMATune
	LibvirtParallel          = vmshared.LibvirtParallel
	LibvirtRedirDev          = vmshared.LibvirtRedirDev
	LibvirtResource          = vmshared.LibvirtResource
	LibvirtRNG               = vmshared.LibvirtRNG
	LibvirtSecLabel          = vmshared.LibvirtSecLabel
	LibvirtSerial            = vmshared.LibvirtSerial
	LibvirtShmem             = vmshared.LibvirtShmem
	LibvirtSmartcard         = vmshared.LibvirtSmartcard
	LibvirtSysInfo           = vmshared.LibvirtSysInfo
	LibvirtTPM               = vmshared.LibvirtTPM
	LibvirtURI               = vmshared.LibvirtURI
	LibvirtVendorID          = vmshared.LibvirtVendorID
	LibvirtVideo             = vmshared.LibvirtVideo
	LibvirtVsock             = vmshared.LibvirtVsock
	Op                       = vmshared.Op
	QemuRuntimePaths         = vmshared.QemuRuntimePaths
	ReadinessConfig          = vmshared.ReadinessConfig
	ResolvedReadiness        = vmshared.ResolvedReadiness
	SnapshotCreateOpts       = vmshared.SnapshotCreateOpts
	SnapshotEntry            = vmshared.SnapshotEntry
	SSHTunnel                = sshx.SSHTunnel
	VmNetwork                = vmshared.VmNetwork
	VmRuntimeParams          = vmshared.VmRuntimeParams
	VmSource                 = vmshared.VmSource
	VmSpec                   = vmshared.VmSpec
)

// readinessResolve aliases the shared config→resolved readiness resolver — the logic + the
// CHARLY_READINESS_* field table live ONCE in vmshared (FU-9). The out-of-process plugin passes
// nil (no project loader) and inherits the host-resolved bounds via the CHARLY_READINESS_* env
// the host threads into its spawn (charly's LocalTransport.Connect → ResolvedReadiness.PluginEnv).
var readinessResolve = vmshared.ResolveReadiness

var (
	boolPtrDefaultTrue    = vmshared.BoolPtrDefaultTrue
	boolPtrToYesNo        = vmshared.BoolPtrToYesNo
	boolPtrTrue           = vmshared.BoolPtrTrue
	defaultMachineForArch = vmshared.DefaultMachineForArch
	DeleteSnapshot        = vmshared.DeleteSnapshot
	ErrPollFatal          = vmshared.ErrPollFatal
	KeyToUserTmpfilesD    = vmshared.KeyToUserTmpfilesD
	NewSSHTunnel          = sshx.NewSSHTunnel
	openOutputPath        = vmshared.OpenOutputPath
	ParseLibvirtURI       = vmshared.ParseLibvirtURI
	pollUntil             = vmshared.PollUntil
	RegisterTempCleanup   = proclifecycle.RegisterTempCleanup
	RenderQemuArgv        = vmshared.RenderQemuArgv
	resolveCPUDefaults    = vmshared.ResolveCPUDefaults
	SmbiosCredForSSH      = vmshared.SmbiosCredForSSH
	splitPortForward      = vmshared.SplitPortForward
	UnregisterTempCleanup = proclifecycle.UnregisterTempCleanup
	vmDiskPath            = vmshared.VmDiskPath
)

// Pure VM helper functions consolidated into vmshared (vm_helpers.go) — the
// former core↔plugin byte-for-byte duplication (FU-10). These aliases keep the
// package-main call sites unchanged.
var (
	qemuSystemBinary       = vmshared.QemuSystemBinary
	libvirtSessionSocket   = vmshared.LibvirtSessionSocket
	writeJSON              = vmshared.WriteJSON
	isDeviceElement        = vmshared.IsDeviceElement
	ValidateLibvirtSnippet = vmshared.ValidateLibvirtSnippet
)

const (
	PollRemote                       = vmshared.PollRemote
	readinessAbsoluteCapFallback     = vmshared.ReadinessAbsoluteCapFallback
	readinessIntervalHeavyFallback   = vmshared.ReadinessIntervalHeavyFallback
	readinessIntervalLocalFallback   = vmshared.ReadinessIntervalLocalFallback
	readinessIntervalRemoteFallback  = vmshared.ReadinessIntervalRemoteFallback
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

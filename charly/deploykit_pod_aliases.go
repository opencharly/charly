// deploykit_pod_aliases.go — package-main bindings onto the POD config-WRITE
// mechanism in github.com/opencharly/sdk/deploykit (P11a: GenerateQuadlet + its
// resolved-runtime input types + pure size/port/tunnel helpers, relocated out of
// charly core). These aliases keep every package-main call site compiling unchanged:
// config_image.go's quadlet writer, the tunnel-unit filename in
// commands.go/config_image.go, the enc/secrets/sidecar resolvers, and
// shell.go/start.go's direct-mode working-dir + port localization. The pod/sidecar
// quadlet generators and the cloudflare tunnel-unit emitter live in deploykit too
// (config_image.go's write phase resolves them via the deploy:pod plugin, no longer
// through a package-main alias). The mechanism is pure + host-I/O-free; the host still
// owns config-RESOLVE (building the QuadletConfig) and the file WRITE.
package main

import "github.com/opencharly/sdk/deploykit"

// Resolved-runtime input types for the quadlet writer (ruling C: built + consumed
// in-process from config-resolve data, so not wire types / no CUE-sourcing).
type (
	QuadletConfig     = deploykit.QuadletConfig
	CollectedSecret   = deploykit.CollectedSecret
	ResolvedBindMount = deploykit.ResolvedBindMount
	ResolvedSidecar   = deploykit.ResolvedSidecar
)

// The pod config-WRITE mechanism + the pure helpers it shares with the host's
// remaining pod-config consumers (config_image.go's quadlet writer, the tunnel-unit
// filename, SecurityArgs direct-mode, shell/start working-dir + port localization).
var (
	generateQuadlet       = deploykit.GenerateQuadlet
	tunnelServiceFilename = deploykit.TunnelServiceFilename
	ipcModeBlocksShmSize  = deploykit.IpcModeBlocksShmSize
	localizePort          = deploykit.LocalizePort
	resolveWorkingDir     = deploykit.ResolveWorkingDir

	// SecurityArgs/provisionData/buildStartArgs — K4 lane B: relocated to deploykit
	// (pod_lifecycle_resolve.go's move); config_image.go/start.go are the remaining core
	// callers (group 3, not moving yet).
	SecurityArgs   = deploykit.SecurityArgs
	provisionData  = deploykit.ProvisionData
	buildStartArgs = deploykit.BuildStartArgs

	// ResolveBoxEngineForDeploy/ResolveBoxEngineFromMeta — K4 lane B: relocated to deploykit
	// (pod_lifecycle_resolve.go's quadlet-mode move); ResolveBoxEngine/ResolveBoxEngineFromDir
	// (which need *Config/*Candy/LoadConfig) stay in charly/engine.go.
	ResolveBoxEngineForDeploy = deploykit.ResolveBoxEngineForDeploy
	ResolveBoxEngineFromMeta  = deploykit.ResolveBoxEngineFromMeta

	// The on-disk quadlet/systemd path + filename helpers — K4 lane B: relocated to deploykit
	// (pod_lifecycle_resolve.go's quadlet-mode move); quadlet.go documents the residual split.
	quadletDir              = deploykit.QuadletDir
	systemdUserDir          = deploykit.SystemdUserDir
	quadletFilename         = deploykit.QuadletFilename
	quadletFilenameInstance = deploykit.QuadletFilenameInstance
	serviceName             = deploykit.ServiceName
	serviceNameInstance     = deploykit.ServiceNameInstance
	quadletExists           = deploykit.QuadletExists
	quadletExistsInstance   = deploykit.QuadletExistsInstance
)

const (
	DataProvisionInitial = deploykit.DataProvisionInitial
	DataProvisionForce   = deploykit.DataProvisionForce
)

package vm

import (
	"sync"
)

// readiness_config.go — the out-of-process plugin's readiness ENTRY. The config→resolved resolver
// AND the CHARLY_READINESS_* field table live ONCE in sdk/vmshared (ResolveReadiness, aliased
// here as readinessResolve via vmshared_aliases.go), shared with charly core.

// loadedReadiness resolves this plugin process's readiness bounds ONCE. The out-of-process plugin
// cannot LoadUnified to read the project's defaults.readiness, so it passes nil — but the host
// threads its RESOLVED project readiness into this process's env as CHARLY_READINESS_* at spawn
// (charly's LocalTransport.Connect → ResolvedReadiness.PluginEnv), which readinessResolve re-reads.
// So the plugin's poll-gates honor the project's defaults.readiness (FU-7) without a project loader.
var (
	readinessOnce   sync.Once
	readinessCached ResolvedReadiness
)

func loadedReadiness() ResolvedReadiness {
	readinessOnce.Do(func() {
		rr, _ := readinessResolve(nil)
		readinessCached = rr
	})
	return readinessCached
}

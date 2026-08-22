// Package phase (github.com/opencharly/spec/phase, #55 import-purity) holds the plugin
// LIFECYCLE phase vocabulary (F9): the ordered points at which a plugin participates
// in charly's lifecycle. A plugin DECLARES its phase via ProvidedCapability.Phase
// (default PhaseRuntime); the kernel loads/invokes plugins in phase order. The
// BOOTSTRAP phase runs BEFORE config validation/migration, so an early-running
// capability can itself be a plugin loaded at the right time (today only the no-op
// candy/plugin-example-bootstrap registers here — neither migrate nor egress is a
// bootstrap plugin; both are verb plugins invoked the normal way). The PREFLIGHT phase
// (K5 seam-death) runs even earlier — before Kong dispatches to ANY command, regardless
// of whether a project even loads (main.go, right after kong.Parse): candy/plugin-doctor's
// freshness-guard capability is the first (and so far only) preflight-phase plugin,
// replacing the former charly/main_freshness.go core-only guard.
//
// This is the LIFECYCLE phase set (preflight/bootstrap/schema/load/build/runtime),
// DISTINCT from the step-phase enum (spec/spec/ir_enums.go's
// PhasePrepare/PhaseInstall/PhaseCleanup) — different concepts sharing the "phase" noun.
// It is a fabric slice of the spec contract module — pure Go string constants, no heavy
// deps (#55 Rule 2) — relocated from the github.com/opencharly/sdk root package, the
// SINGLE SOURCE for the phase vocabulary (R3): charly's package main aliases these, and a
// plugin's Describe declares its phase against them. charly core imports this slice
// INSTEAD of the sdk root; the sdk root keeps a thin re-export during cutover then is
// deleted.
package phase

const (
	PhasePreflight = "preflight" // before ANY command dispatch, before Kong even parses a project — compiled-in only (no project, no validated config, sometimes no cwd project at all).
	PhaseBootstrap = "bootstrap" // before config validation/migration; compiled-in only (no validated config exists yet to discover an out-of-process source).
	PhaseSchema    = "schema"    // schema / migration phase
	PhaseLoad      = "load"      // config-load phase (kind decode, etc.)
	PhaseBuild     = "build"     // image-build phase (OpEmit / OpResolve)
	PhaseRuntime   = "runtime"   // deploy / runtime phase (OpExecute / OpRun) — the DEFAULT
)

// PhaseOrder lists the phases in ascending load order; the kernel iterates plugins phase-ascending
// (preflight first). It is the authority for ordering + membership.
var PhaseOrder = []string{PhasePreflight, PhaseBootstrap, PhaseSchema, PhaseLoad, PhaseBuild, PhaseRuntime}

// NormalizePhase maps an empty or unrecognized declared phase to the default (PhaseRuntime), so a
// plugin that declares no phase participates at the normal (runtime) time.
func NormalizePhase(p string) string {
	for _, known := range PhaseOrder {
		if p == known {
			return p
		}
	}
	return PhaseRuntime
}

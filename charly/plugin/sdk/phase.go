package sdk

// Plugin lifecycle PHASES (F9) — the ordered points at which a plugin participates in charly's
// lifecycle. A plugin DECLARES its phase via ProvidedCapability.Phase (default PhaseRuntime); the
// kernel loads/invokes plugins in phase order. The BOOTSTRAP phase runs BEFORE config
// validation/migration, so an early-running capability can itself be a plugin loaded at the right
// time (today only the no-op candy/plugin-example-bootstrap registers here — neither migrate nor
// egress is a bootstrap plugin; both are verb plugins invoked the normal way). This is the SINGLE
// SOURCE for the phase vocabulary (R3): charly's package main aliases these, and a plugin's
// Describe declares its phase against them.
const (
	PhaseBootstrap = "bootstrap" // before config validation/migration; compiled-in only (no validated config exists yet to discover an out-of-process source).
	PhaseSchema    = "schema"    // schema / migration phase
	PhaseLoad      = "load"      // config-load phase (kind decode, etc.)
	PhaseBuild     = "build"     // image-build phase (OpEmit / OpResolve)
	PhaseRuntime   = "runtime"   // deploy / runtime phase (OpExecute / OpRun) — the DEFAULT
)

// PhaseOrder lists the phases in ascending load order; the kernel iterates plugins phase-ascending
// (bootstrap first). It is the authority for ordering + membership.
var PhaseOrder = []string{PhaseBootstrap, PhaseSchema, PhaseLoad, PhaseBuild, PhaseRuntime}

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

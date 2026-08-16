package fleet

// reaper_identity.go — the PRIMARY incarnation gate for the ephemeral TTL reaper.
//
// The reaper's argv names the ENTITY (`fleet del <entity> --assume-yes`), and bed entity names are
// REUSED run over run. Registration overwrites the single recorded EphemeralRuntime.TimerUnit, so
// every earlier run's timer stays armed and permanently un-cancellable. A stale timer's `fleet del`
// is byte-identical to a legitimate one and would delete whichever incarnation currently holds the
// name.
//
// Observed 2026-08-15: nine stale timers fired at 00:19:26 while a live incarnation held the entity
// name. Only a missing executable (203/EXEC) stopped them — the co-occurring path defect, not any
// guard. Fixing the path without this gate would have converted a silent leak into deletion of live
// work, and it would have looked like a successful fix because the reaper would finally be running.

// timerDrivenDelRefusal decides whether a TIMER-DRIVEN `fleet del` may proceed, and FAILS CLOSED.
//
// Called only when --require-timer-unit is present, so a human invocation never reaches it and
// never pays the overlay read.
//
// The lookup needs NO node resolution: the overlay key is a pure function of the deploy name
// ("vm:" + VmDomainIdentity, three string replacements), and the reaper already holds that name in
// its own argv. That is what lets identity be verified BEFORE resolution — the property the whole
// design rests on, since resolution via the "vm:" fallback is what would otherwise synthesise a
// deletable node out of an address alone.
//
//   - record absent      -> REFUSE. A bed session removes its per-run overlay at teardown, so an
//     absent record means the registration is spent (or the state was never written). Unverifiable
//     is not permission — this is the branch that keeps the "vm:" address form safe.
//   - token mismatch     -> REFUSE. Superseded by a later registration for the same entity.
//   - token match        -> PROCEED.
func timerDrivenDelRefusal(deployName, firingUnit string) (refuse bool, reason string) {
	// NOT timer-driven -> allow, and read NOTHING. This early return is the whole protection of the
	// CLI's primary destructive verb: a human `charly fleet del` must not gain an overlay read, and
	// therefore must not gain a failure mode, in service of a machine caller. It is placed here
	// rather than at the call site so the property is BEHAVIOURAL — a test can poison
	// CHARLY_DEPLOY_CONFIG and assert this path still proceeds, which turns "does not read" from an
	// absence into something observable.
	if firingUnit == "" {
		return false, ""
	}
	recorded, found := recordedEphemeralTimerUnit(deployName)
	if !found {
		return true, "no ephemeral registration recorded for this deployment — refusing to reap on an unverifiable identity"
	}
	if recorded != firingUnit {
		return true, "superseded by " + recorded + " — a later run re-registered this entity"
	}
	return false, ""
}

// recordedEphemeralTimerUnit reads the deploy overlay and returns the TimerUnit recorded for
// deployName. Split out for testability: the caller is reached only from the CLI path.
//
// Reads the overlay named by CHARLY_DEPLOY_CONFIG when set — the reaper unit carries the path of
// the overlay its registration was written to, so a bed-scoped registration verifies against its
// OWN state rather than the operator's. An absent or unreadable overlay yields found=false, which
// the caller treats as a refusal.
// reaperFleetConfig is the overlay read, as a package-level var for testability — the same seam
// shape plugin-clean uses for liveBuildFloor/listDanglingImages. A test overrides it with a
// *FleetConfig VALUE, which is the loader's own OUTPUT TYPE, so no test can encode a guess about
// the on-disk serialization ever again. The serialization belongs to the loader and is exercised
// by the bed, not by a fixture.
var reaperFleetConfig = loadFleetConfig

func recordedEphemeralTimerUnit(deployName string) (unit string, found bool) {
	// Goes through the package's OWN paired loader — the same read registration writes against —
	// rather than opening the file. A hand-rolled yaml.Unmarshal into FleetConfig CANNOT work here
	// and fails SILENTLY: FleetConfig.Fleet is tagged `yaml:"deploy"`, but SaveFleetConfig writes
	// entity-name keys carrying node-form bodies and LoadFleetConfig does not parse YAML at all —
	// it delegates to the unified loader. So the tag describes an IN-MEMORY shape no file on disk
	// uses, and unmarshalling any real overlay succeeds while returning an EMPTY map.
	//
	// That is not hypothetical: this function did exactly that, so the gate found no registration
	// for any deployment and refused every reap — fail-closed, therefore safe, but permanently
	// inert. Its unit tests passed because their fixtures were written in the `deploy:` shape the
	// parser expected, proving only that the parser parses its own invention. Using the writer's
	// counterpart is what makes the class impossible: a schema change then breaks both sides
	// together instead of silently splitting them.
	dc, err := reaperFleetConfig()
	if err != nil || dc == nil {
		return "", false
	}
	node, ok := dc.Fleet[ephemeralOverlayKey(deployName)]
	if !ok || node.VmState == nil || node.VmState.Ephemeral == nil {
		return "", false
	}
	return node.VmState.Ephemeral.TimerUnit, true
}

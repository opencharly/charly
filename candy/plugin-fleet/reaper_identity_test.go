package fleet

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// stubOverlay replaces the reaper's overlay read with an in-memory FleetConfig — the loader's own
// OUTPUT TYPE. Deliberately not a YAML fixture: the previous version of these tests hand-wrote the
// file in a `deploy:` shape the hand-rolled parser expected, so they proved the parser parses its
// own invention while the real overlay (entity-name keys, node-form bodies) always yielded an empty
// map and the gate refused every reap. Serialization belongs to the paired loader; a test that
// restates it is a test of an assumption.
func stubOverlay(t *testing.T, entityKey, timerUnit string) {
	t.Helper()
	orig := reaperFleetConfig
	t.Cleanup(func() { reaperFleetConfig = orig })
	reaperFleetConfig = func() (*deploykit.FleetConfig, error) {
		dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{}}
		if entityKey != "" {
			dc.Fleet[entityKey] = spec.FleetNode{
				VmState: &spec.VmDeployState{Ephemeral: &spec.EphemeralRuntime{TimerUnit: timerUnit}},
			}
		}
		return dc, nil
	}
}

// TestTimerDrivenDelRefusal covers the PRIMARY incarnation gate: FleetDelCmd.Run calls it BEFORE
// resolution, keyed by ephemeralOverlayKey(deployName) — a pure function of the name, so no node
// resolution is needed and the check can precede the "vm:" fallback that would otherwise synthesise
// a deletable node from an address alone.
//
// It must FAIL CLOSED: the unit carries CHARLY_SKIP_FRESHNESS_CHECK=1, so a possibly very stale
// binary performs a deletion and this gate is what makes that safe.
//
// Hazard status: mechanism-derived, conditions OBSERVED 2026-08-15 — nine stale timers fired at
// 00:19:26 while a live incarnation held the entity name; only a missing executable stopped them.
// The deletion itself was never witnessed, because the co-occurring path defect masked it.
func TestTimerDrivenDelRefusal(t *testing.T) {
	const entity = "bed.ephvm"
	key := ephemeralOverlayKey(entity) // the SAME derivation production uses, not a literal
	const current = "charly-fleet-del-bed-ephvm-1786830381"
	const superseded = "charly-fleet-del-bed-ephvm-1786825676"

	t.Run("current registration proceeds", func(t *testing.T) {
		stubOverlay(t, key, current)
		if refuse, reason := timerDrivenDelRefusal(entity, current); refuse {
			t.Errorf("refused the CURRENT registration: %s", reason)
		}
	})
	t.Run("superseded refuses", func(t *testing.T) {
		stubOverlay(t, key, current)
		refuse, reason := timerDrivenDelRefusal(entity, superseded)
		if !refuse {
			t.Error("an earlier run's armed timer must not reap a later incarnation")
		}
		if reason == "" {
			t.Error("refused without a reason")
		}
	})
	t.Run("no registration refuses (FAIL CLOSED)", func(t *testing.T) {
		stubOverlay(t, "", "")
		if refuse, _ := timerDrivenDelRefusal(entity, superseded); !refuse {
			t.Error("unverifiable identity must refuse — this is what keeps the vm: address form safe")
		}
	})
	t.Run("human path reads nothing", func(t *testing.T) {
		orig := reaperFleetConfig
		t.Cleanup(func() { reaperFleetConfig = orig })
		read := false
		reaperFleetConfig = func() (*deploykit.FleetConfig, error) { read = true; return nil, nil }
		if refuse, _ := timerDrivenDelRefusal(entity, ""); refuse {
			t.Error("a human `fleet del` must not be refused")
		}
		if read {
			t.Error("a human `fleet del` must not read the overlay at all — no new failure mode in the CLI's primary destructive verb")
		}
	})
}

// TestPersistEphemeralInto_CancelsOrphanedTimer proves registration cancels the timer its own
// record is about to orphan.
//
// EphemeralRuntime holds ONE TimerUnit, so overwriting it strands the previous registration's
// timer — armed, unreferenced, un-cancellable by any later teardown, which reads only the recorded
// unit. Measured before this fix: one bed run left 3 armed (it registers per deploy/rebuild phase,
// and teardown could cancel only the last), and ten accumulated across runs.
//
// Cancelling at registration keeps at most ONE armed per entity, which also stops a stale timer
// from surviving into a later run that reuses the name — the cross-run hazard, removed at the point
// of creation rather than refused at the point of firing.
func TestPersistEphemeralInto_CancelsOrphanedTimer(t *testing.T) {
	var cancelled []string
	orig := cancelTransientTimerFn
	t.Cleanup(func() { cancelTransientTimerFn = orig })
	cancelTransientTimerFn = func(unit string) { cancelled = append(cancelled, unit) }

	const entity = "bed.ephvm"
	key := ephemeralOverlayKey(entity)
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		key: {VmState: &spec.VmDeployState{Ephemeral: &spec.EphemeralRuntime{TimerUnit: "charly-fleet-del-bed-ephvm-OLD"}}},
	}}

	persistEphemeralInto(dc, &spec.Deploy{}, entity, &ephemeralHandle{timerUnit: "charly-fleet-del-bed-ephvm-NEW"})

	if len(cancelled) != 1 || cancelled[0] != "charly-fleet-del-bed-ephvm-OLD" {
		t.Fatalf("cancelled = %v, want exactly the orphaned OLD unit — an overwritten TimerUnit is un-cancellable afterwards", cancelled)
	}
	if got := dc.Fleet[key].VmState.Ephemeral.TimerUnit; got != "charly-fleet-del-bed-ephvm-NEW" {
		t.Errorf("recorded TimerUnit = %q, want the NEW one", got)
	}
	// Re-registering the SAME unit must not cancel it — that would disarm the live registration.
	cancelled = nil
	persistEphemeralInto(dc, &spec.Deploy{}, entity, &ephemeralHandle{timerUnit: "charly-fleet-del-bed-ephvm-NEW"})
	if len(cancelled) != 0 {
		t.Errorf("cancelled %v on an identical re-registration — that disarms the LIVE timer", cancelled)
	}
}

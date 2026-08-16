package check

// bed_ephemeral_timers.go — cancelling the TTL reaper timers a bed session registered.
//
// An ephemeral registration is TWO artifacts: the EphemeralRuntime block in this session's
// per-bed deploy overlay, and an armed systemd transient timer whose ExecStart is
// `charly fleet del <entity> --assume-yes`. The session's teardown destroyed only the first
// (RemoveAll on the temp overlay dir), leaving the second armed against state that no longer
// existed — so the timer could neither resolve the entity nor verify its identity, the VM it was
// registered to reap leaked permanently, and the unit accumulated as a systemd failure that no
// charly verb surfaces. Measured 2026-08-15: ten armed units for ONE reused entity name, all
// firing at once, none able to reap.
//
// This is the missing half of that teardown, not a new feature.

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
)

// cancelBedEphemeralTimers stops every TTL reaper timer recorded in the bed's OWN overlay.
//
// Scoped deliberately to this session's overlay rather than to a unit-name pattern: a pattern
// match over `charly-fleet-del-*` would reach a CONCURRENT bed's armed timers and cancel the
// reaper for a VM still in use. Peers share this host, and their registrations are
// indistinguishable from ours by name alone — only the overlay says which registrations are ours.
//
// Best-effort by design: a cancellation failure must not fail the bed, and the caller runs this
// BEFORE removing the overlay so a failure here leaves the state intact and the timer still able
// to do its job.
func cancelBedEphemeralTimers(ctx context.Context, ex *sdk.Executor) {
	for _, unit := range bedEphemeralTimerUnits(ctx, ex) {
		// `stop` on a transient timer both disarms it and releases the unit; --user matches the
		// manager systemd-run registered it with.
		if out, err := exec.Command("systemctl", "--user", "stop", unit).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cancelling ephemeral TTL timer %q: %v: %s\n", unit, err, out)
		}
	}
}

// bedEphemeralTimerUnits returns every ephemeral timer unit recorded in the session's overlay.
//
// Reads through the PAIRED LOADER (loaderkit.LoadHostFleetConfigViaExecutor), the counterpart of
// the writer that persists these registrations — never a hand-rolled yaml.Unmarshal. That is not a
// style preference: FleetConfig.Fleet is tagged `yaml:"deploy"`, but SaveFleetConfig writes
// entity-name keys carrying node-form bodies and LoadFleetConfig delegates to the unified loader
// rather than parsing YAML at all. The tag therefore describes an IN-MEMORY shape that no file on
// disk uses, and unmarshalling a real overlay SUCCEEDS while returning an empty map. This function
// did exactly that, so the cancellation was a silent no-op on every run — measured on the bed:
// three registrations, three still armed after teardown. Going through the writer's counterpart
// makes the class impossible, because a schema change then breaks both sides together.
//
// The loader honours CHARLY_DEPLOY_CONFIG, which the session sets to its own per-bed overlay, so
// this sees exactly this bed's registrations and no peer's.
func bedEphemeralTimerUnits(ctx context.Context, ex *sdk.Executor) []string {
	dc, err := loaderkit.LoadHostFleetConfigViaExecutor(ctx, ex)
	if err != nil || dc == nil {
		return nil
	}
	return ephemeralTimerUnitsIn(dc)
}

// ephemeralTimerUnitsIn is the pure selection over a loaded overlay — split out so a test works on
// the loader's OUTPUT TYPE rather than restating a serialization it should not know about.
func ephemeralTimerUnitsIn(dc *deploykit.FleetConfig) []string {
	if dc == nil {
		return nil
	}
	var units []string
	for _, node := range dc.Fleet {
		if node.VmState == nil || node.VmState.Ephemeral == nil {
			continue
		}
		if u := node.VmState.Ephemeral.TimerUnit; u != "" {
			units = append(units, u)
		}
	}
	return units
}

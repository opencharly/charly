package check

import (
	"sort"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestEphemeralTimerUnitsIn proves the cancellation selects THIS bed's recorded units.
//
// It works on the loader's OUTPUT TYPE, never a YAML fixture. The version this replaces hand-wrote
// an overlay in a `deploy:` shape that no writer emits, so it passed while the real cancellation
// was a silent no-op on every run — a test of its own assumption. Serialization belongs to the
// paired loader (loaderkit.LoadHostFleetConfigViaExecutor) and is exercised by the bed.
//
// The scoping is the safety property: selecting by unit-NAME pattern instead would reach a
// concurrent bed's armed timers and cancel the reaper for a VM still in use.
func TestEphemeralTimerUnitsIn(t *testing.T) {
	eph := func(unit string) spec.FleetNode {
		return spec.FleetNode{VmState: &spec.VmDeployState{Ephemeral: &spec.EphemeralRuntime{TimerUnit: unit}}}
	}
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"vm:bed-a":  eph("charly-fleet-del-bed-a-111"),
		"vm:bed-b":  eph("charly-fleet-del-bed-b-222"),
		"vm:no-tmr": {VmState: &spec.VmDeployState{Ephemeral: &spec.EphemeralRuntime{}}}, // registered, no unit
		"plain-pod": {Target: "pod"},                                                     // not ephemeral at all
	}}
	got := ephemeralTimerUnitsIn(dc)
	sort.Strings(got)
	want := []string{"charly-fleet-del-bed-a-111", "charly-fleet-del-bed-b-222"}
	if len(got) != len(want) {
		t.Fatalf("ephemeralTimerUnitsIn() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if u := ephemeralTimerUnitsIn(nil); u != nil {
		t.Errorf("nil overlay = %v, want nil — teardown must not fail when nothing was registered", u)
	}
}

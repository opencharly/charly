package fleet

import (
	"testing"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// deploy_save_test.go — relocated (in part) from charly/deploy_save_test.go (#55 decoupling,
// Batch A): these 2 tests are pure in-memory *deploykit.FleetConfig.Lookup/LookupKey
// fixtures, zero charly dep. The original file's remaining SaveDeployState-based tests were
// LATER converted to drive charly's real production seam instead of calling deploykit directly
// (#55 final-tail seam-drive spike, team-lead directive 2026-08-03) and stayed in charly; its
// SaveFleetConfig-based tests (TestSaveFleetConfig_AtomicWriteLeavesNoTempLeftover /
// _RefusesToClobberUnloadableConfig / TestFleetNode_DisposableFalseRoundTrip) and
// TestCharlyUpdatePreservesPerHostDeployFields / TestVmDestroyRemovesPureAutoEntry (from
// charly/deploy_preserve_test.go) split-by-assertion into this package's own
// deploy_state_writer_test.go (#55 final-tail split-by-assertion round, same directive) — the
// "stays in charly, ruling 1" framing predated the gate that forced that split question.

// TestDeployConfigLookup_NilSafe pins the post-2026-05-16 cleanup of
// the call sites that previously wrote
//
//	dc := deploykit.LoadDeployConfigForRead("...")
//	if dc != nil {
//	    if entry, ok := dc.Deploy[deployKey(image, instance)]; ok { ... }
//	}
//
// using nil-safe Lookup/LookupKey methods. The contract: nil receiver
// returns (zero, false) so callers can chain
// `deploykit.LoadDeployConfigForRead(...).Lookup(image, instance)` without a
// separate nil check.
func TestDeployConfigLookup_NilSafe(t *testing.T) {
	var dc *deploykit.FleetConfig // nil
	if entry, ok := dc.Lookup("foo", ""); ok {
		t.Errorf("Lookup on nil dc returned ok=true entry=%+v; want (zero, false)", entry)
	}
	if entry, ok := dc.LookupKey("foo"); ok {
		t.Errorf("LookupKey on nil dc returned ok=true entry=%+v; want (zero, false)", entry)
	}
}

// TestDeployConfigLookup_PresentAndAbsent pins the basic Lookup
// contract: present entries return (entry, true); absent entries and
// nil deploy map return (zero, false). Instance form is keyed via
// deployKey (image/instance); LookupKey takes the raw deploy.yml key.
func TestDeployConfigLookup_PresentAndAbsent(t *testing.T) {
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"foo":       {Target: "pod", Image: "foo"},
		"foo/inst1": {Target: "pod", Image: "foo"},
		"vm:arch":   {Target: "vm"},
	}}

	// Lookup (image, instance) form.
	if entry, ok := dc.Lookup("foo", ""); !ok || entry.Image != "foo" {
		t.Errorf("Lookup(foo, \"\") = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.Lookup("foo", "inst1"); !ok || entry.Image != "foo" {
		t.Errorf("Lookup(foo, inst1) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.Lookup("missing", ""); ok {
		t.Errorf("Lookup(missing, \"\") = (%+v, %v); want absent", entry, ok)
	}

	// LookupKey (raw deploy.yml key) form.
	if entry, ok := dc.LookupKey("foo/inst1"); !ok || entry.Image != "foo" {
		t.Errorf("LookupKey(foo/inst1) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.LookupKey("vm:arch"); !ok || entry.Target != "vm" {
		t.Errorf("LookupKey(vm:arch) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.LookupKey("missing"); ok {
		t.Errorf("LookupKey(missing) = (%+v, %v); want absent", entry, ok)
	}

	// Empty / nil-map dc returns (zero, false).
	emptyDc := &deploykit.FleetConfig{}
	if entry, ok := emptyDc.Lookup("foo", ""); ok {
		t.Errorf("Lookup on empty dc returned ok=true entry=%+v", entry)
	}
}

package bundle

import (
	"testing"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// deploy_save_test.go — relocated (in part) from charly/deploy_save_test.go (#55 decoupling,
// Batch A): these 2 tests are pure in-memory *deploykit.BundleConfig.Lookup/LookupKey
// fixtures, zero charly dep. The remaining tests in the original file
// (TestSaveDeployState_*/TestSaveBundleConfig_*/TestBundleNode_DisposableFalseRoundTrip/
// TestRemoveVmDeployEntry_SelectiveAndIdempotent) all route through testLoadBundleConfig/
// testSaveDeployConfig (which call charly's own LoadUnified()) — the AMBIGUOUS bed-persist /
// deploy-state integration cluster the orchestrator ruled STAYS in charly as loader
// integration coverage (ruling 1); they are NOT moved here.

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
	var dc *deploykit.BundleConfig // nil
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
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
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
	emptyDc := &deploykit.BundleConfig{}
	if entry, ok := emptyDc.Lookup("foo", ""); ok {
		t.Errorf("Lookup on empty dc returned ok=true entry=%+v", entry)
	}
}

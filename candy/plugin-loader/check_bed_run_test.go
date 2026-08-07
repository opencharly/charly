package loader

// Relocated from charly/check_bed_run_test.go (#55 decoupling cone, Batch C):
// TestValidateCheckBeds_TargetEnum / _VmRefMustResolve / _LocalRefMustResolve
// assert loaderkit.ValidateCheckBeds directly against spec.UnifiedFile fixtures
// — zero charly coupling (using a plain testThreaded() value in place of
// charly's registry-derived loaderThreaded()). TestCheckBeds_DerivesFromDisposableFleets
// (a pure spec.UnifiedFile method) and the bed-persist / cross-deployment tests
// (TestPersistBedDeployOverrides_SeedsPortBeforeConfig, TestBedCheckLiveRefs —
// genuine charly-loader integration coverage) STAY in charly/check_bed_run_test.go.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// TestValidateCheckBeds_TargetEnum asserts an unsupported target is rejected.
func TestValidateCheckBeds_TargetEnum(t *testing.T) {
	uf := &spec.UnifiedFile{
		Fleet: map[string]spec.FleetNode{
			"check-weird": {Target: "k8s", Disposable: new(true)},
		},
	}
	err := loaderkit.ValidateCheckBeds(uf, testThreaded())
	if err == nil || !strings.Contains(err.Error(), "unsupported target") {
		t.Fatalf("expected target-enum error, got %v", err)
	}
}

// TestValidateCheckBeds_VmRefMustResolve asserts a vm-target bed whose vm:
// entity is undefined is rejected, and that a defined entity passes.
func TestValidateCheckBeds_VmRefMustResolve(t *testing.T) {
	missing := &spec.UnifiedFile{
		Fleet: map[string]spec.FleetNode{
			"check-k3s-vm": {Target: "vm", From: "k3s-vm", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(missing, testThreaded()); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-vm-ref error, got %v", err)
	}
	ok := &spec.UnifiedFile{
		PluginKinds: map[string]map[string]json.RawMessage{
			"vm": rawTemplateMap(map[string]*vmshared.VmSpec{"k3s-vm": {}}),
		},
		Fleet: map[string]spec.FleetNode{
			"check-k3s-vm": {Target: "vm", From: "k3s-vm", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(ok, testThreaded()); err != nil {
		t.Fatalf("defined vm ref should pass, got %v", err)
	}
}

// TestValidateCheckBeds_LocalRefMustResolve asserts a local-target bed whose
// local: template is undefined is rejected, and that a defined one passes.
func TestValidateCheckBeds_LocalRefMustResolve(t *testing.T) {
	missing := &spec.UnifiedFile{
		Fleet: map[string]spec.FleetNode{
			"check-local": {Target: "local", From: "check-local", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(missing, testThreaded()); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-local-ref error, got %v", err)
	}
	ok := &spec.UnifiedFile{
		PluginKinds: map[string]map[string]json.RawMessage{
			"local": rawTemplateMap(map[string]*spec.LocalSpec{"check-local": {}}),
		},
		Fleet: map[string]spec.FleetNode{
			"check-local": {Target: "local", From: "check-local", Disposable: new(true)},
		},
	}
	if err := loaderkit.ValidateCheckBeds(ok, testThreaded()); err != nil {
		t.Fatalf("defined local ref should pass, got %v", err)
	}
}

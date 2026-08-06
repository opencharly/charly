package fleet

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"github.com/opencharly/sdk"
)

// funcPointer returns a comparable identity for a func value (funcs are not comparable in Go
// except to nil) — used to assert artifactRegisterHandlers wires "kubeconfig" to
// k3sPostProvision specifically, not merely to some handler. Relocated from the deleted
// charly/deploy_add_shared_test.go (Cone A shape 3).
func funcPointer(fn func(context.Context, *sdk.Executor, string, string, string) error) uintptr {
	return reflect.ValueOf(fn).Pointer()
}

// TestArtifactRegisterHandlers_KubeconfigWired proves the production wiring: the "kubeconfig"
// register hint maps to k3sPostProvision specifically (not merely SOME handler) — a regression
// guard against a future edit silently rewiring the map.
func TestArtifactRegisterHandlers_KubeconfigWired(t *testing.T) {
	handler, ok := artifactRegisterHandlers["kubeconfig"]
	if !ok {
		t.Fatal("expected a \"kubeconfig\" entry in artifactRegisterHandlers")
	}
	if funcPointer(handler) != funcPointer(k3sPostProvision) {
		t.Error("artifactRegisterHandlers[\"kubeconfig\"] is not wired to k3sPostProvision")
	}
}

// TestDispatchRegisterHints_DispatchesByDeclarationNotName is the plugin-side twin of the deleted
// charly/deploy_add_shared_test.go's TestRetrieveArtifactsAndK3s_DispatchesByDeclarationNotName:
// dispatchRegisterHints dispatches to the registered handler for a register hint present in the
// resolved set, and does NOT dispatch when the hint is absent — the register-hint set itself
// (deploykit.CandyArtifactRegisters, computed PLUGIN-SIDE over the envelope candy set — #55 K4) is
// what carries the declaration-not-name behavior; this test proves the plugin-side dispatch loop
// consuming that set is itself correct and word-keyed (never a hardcoded candy name).
func TestDispatchRegisterHints_DispatchesByDeclarationNotName(t *testing.T) {
	orig := maps.Clone(artifactRegisterHandlers)
	t.Cleanup(func() {
		for k := range artifactRegisterHandlers {
			delete(artifactRegisterHandlers, k)
		}
		maps.Copy(artifactRegisterHandlers, orig)
	})

	var calls []string
	artifactRegisterHandlers["kubeconfig"] = func(_ context.Context, _ *sdk.Executor, artifactKey, deployName, vmEntity string) error { //nolint:unparam // error return required to match the map's func-type; this mock never fails
		calls = append(calls, artifactKey+"/"+deployName+"/"+vmEntity)
		return nil
	}

	t.Run("no register hints present never dispatches", func(t *testing.T) {
		calls = nil
		if err := dispatchRegisterHints(context.Background(), nil, "myentity", "mydeploy", "myvm", nil); err != nil {
			t.Fatalf("dispatchRegisterHints: %v", err)
		}
		if len(calls) != 0 {
			t.Fatalf("expected zero dispatches for an empty hint set, got %v", calls)
		}
	})

	t.Run("a \"kubeconfig\" hint dispatches regardless of the candy's own name", func(t *testing.T) {
		calls = nil
		if err := dispatchRegisterHints(context.Background(), nil, "myentity", "mydeploy", "myvm", []string{"kubeconfig"}); err != nil {
			t.Fatalf("dispatchRegisterHints: %v", err)
		}
		if len(calls) != 1 || calls[0] != "myentity/mydeploy/myvm" {
			t.Fatalf("expected exactly one dispatch keyed \"myentity/mydeploy/myvm\", got %v", calls)
		}
	})

	t.Run("an unregistered hint is silently skipped", func(t *testing.T) {
		calls = nil
		if err := dispatchRegisterHints(context.Background(), nil, "myentity", "mydeploy", "myvm", []string{"some-other-hint"}); err != nil {
			t.Fatalf("dispatchRegisterHints: %v", err)
		}
		if len(calls) != 0 {
			t.Fatalf("expected zero dispatches for an unregistered hint, got %v", calls)
		}
	})
}

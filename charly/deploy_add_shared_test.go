package main

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// funcPointer returns a comparable identity for a func value (funcs are not
// comparable in Go except to nil) — used to assert artifactRegisterHandlers wires
// "kubeconfig" to K3sPostProvision specifically, not merely to some handler.
func funcPointer(fn func(string, string) error) uintptr {
	return reflect.ValueOf(fn).Pointer()
}

// deploy_add_shared_test.go — coverage for the P13-KERNEL step-3 k3s-server fix: the
// artifact-declaration-driven dispatch that replaced the hardcoded
// deployHasCandy(candyList, "k3s-server") special-case in retrieveArtifactsAndK3s.

// TestCandyArtifactRegisters_NameBlind proves the collector reads each candy's OWN
// artifact declaration, never the candy's NAME — the exact axis the bug fix targets. A
// candy literally named "k3s-server" contributes NOTHING unless it actually declares a
// `register:` hint on an artifact; a candy with an entirely different name contributes
// "kubeconfig" when it does declare one.
func TestCandyArtifactRegisters_NameBlind(t *testing.T) {
	unhinted := testCandy("k3s-server", spec.CandyModel{Artifact: []spec.CandyArtifact{
		{Name: "kubeconfig", Path: "/etc/rancher/k3s/k3s.yaml", RetrieveTo: "/tmp/x"},
	}}, spec.CandyView{})
	hinted := testCandy("totally-different-name", spec.CandyModel{Artifact: []spec.CandyArtifact{
		{Name: "kubeconfig", Path: "/etc/rancher/k3s/k3s.yaml", RetrieveTo: "/tmp/x", Register: "kubeconfig"},
	}}, spec.CandyView{})
	other := testCandy("other-candy", spec.CandyModel{Artifact: []spec.CandyArtifact{
		{Name: "state", Path: "/var/lib/other/state.json", RetrieveTo: "/tmp/y"},
	}}, spec.CandyView{})

	t.Run("name k3s-server alone triggers nothing", func(t *testing.T) {
		got := candyArtifactRegisters([]spec.CandyReader{unhinted})
		if len(got) != 0 {
			t.Fatalf("expected no register hints for a candy with no register: declaration, got %v", got)
		}
	})

	t.Run("declared register: kubeconfig triggers regardless of candy name", func(t *testing.T) {
		got := candyArtifactRegisters([]spec.CandyReader{hinted, other})
		if !got["kubeconfig"] || len(got) != 1 {
			t.Fatalf("expected exactly {kubeconfig: true}, got %v", got)
		}
	})

	t.Run("nil/empty layers -> empty set", func(t *testing.T) {
		if got := candyArtifactRegisters(nil); len(got) != 0 {
			t.Fatalf("expected empty set for nil layers, got %v", got)
		}
	})
}

// TestArtifactRegisterHandlers_KubeconfigWired proves the production wiring: the
// "kubeconfig" register hint maps to K3sPostProvision specifically (not merely SOME
// handler) — a regression guard against a future edit silently rewiring the map.
func TestArtifactRegisterHandlers_KubeconfigWired(t *testing.T) {
	handler, ok := artifactRegisterHandlers["kubeconfig"]
	if !ok {
		t.Fatal("expected a \"kubeconfig\" entry in artifactRegisterHandlers")
	}
	if funcPointer(handler) != funcPointer(K3sPostProvision) {
		t.Error("artifactRegisterHandlers[\"kubeconfig\"] is not wired to K3sPostProvision")
	}
}

// TestRetrieveArtifactsAndK3s_DispatchesByDeclarationNotName is the end-to-end seam
// proof: retrieveArtifactsAndK3s dispatches to the registered handler for a candy
// declaring `register: kubeconfig` under an ARBITRARY name, and does NOT dispatch for a
// candy literally named "k3s-server" that declares no such hint — flipping both halves
// of the original bug (a hardcoded name check that both under- and over-fires relative
// to the actual declared behavior).
func TestRetrieveArtifactsAndK3s_DispatchesByDeclarationNotName(t *testing.T) {
	orig := maps.Clone(artifactRegisterHandlers)
	t.Cleanup(func() {
		for k := range artifactRegisterHandlers {
			delete(artifactRegisterHandlers, k)
		}
		maps.Copy(artifactRegisterHandlers, orig)
	})

	var calls []string
	artifactRegisterHandlers["kubeconfig"] = func(artifactKey, deployName string) error {
		calls = append(calls, artifactKey+"/"+deployName)
		return nil
	}

	exec := &recordingExec{}
	opts := deploykit.EmitOpts{}

	t.Run("candy named k3s-server WITHOUT the declaration never dispatches", func(t *testing.T) {
		calls = nil
		unhinted := testCandy("k3s-server", spec.CandyModel{Artifact: []spec.CandyArtifact{
			{Name: "kubeconfig", Path: "/etc/rancher/k3s/k3s.yaml", RetrieveTo: "/tmp/x", Optional: true},
		}}, spec.CandyView{})
		if err := retrieveArtifactsAndK3s(context.Background(), exec, []spec.CandyReader{unhinted}, "myentity", "mydeploy", nil, opts); err != nil {
			t.Fatalf("retrieveArtifactsAndK3s: %v", err)
		}
		if len(calls) != 0 {
			t.Fatalf("expected zero dispatches for an undeclared candy, got %v", calls)
		}
	})

	t.Run("candy with ANY name declaring register:kubeconfig dispatches", func(t *testing.T) {
		calls = nil
		hinted := testCandy("some-other-candy", spec.CandyModel{Artifact: []spec.CandyArtifact{
			{Name: "kubeconfig", Path: "/etc/rancher/k3s/k3s.yaml", RetrieveTo: "/tmp/x", Optional: true, Register: "kubeconfig"},
		}}, spec.CandyView{})
		if err := retrieveArtifactsAndK3s(context.Background(), exec, []spec.CandyReader{hinted}, "myentity", "mydeploy", nil, opts); err != nil {
			t.Fatalf("retrieveArtifactsAndK3s: %v", err)
		}
		if len(calls) != 1 || calls[0] != "myentity/mydeploy" {
			t.Fatalf("expected exactly one dispatch keyed \"myentity/mydeploy\", got %v", calls)
		}
	})

	t.Run("DryRun short-circuits before any retrieve or dispatch", func(t *testing.T) {
		calls = nil
		hinted := testCandy("some-other-candy", spec.CandyModel{Artifact: []spec.CandyArtifact{
			{Name: "kubeconfig", Path: "/etc/rancher/k3s/k3s.yaml", RetrieveTo: "/tmp/x", Optional: true, Register: "kubeconfig"},
		}}, spec.CandyView{})
		if err := retrieveArtifactsAndK3s(context.Background(), exec, []spec.CandyReader{hinted}, "myentity", "mydeploy", nil, deploykit.EmitOpts{DryRun: true}); err != nil {
			t.Fatalf("retrieveArtifactsAndK3s (dry-run): %v", err)
		}
		if len(calls) != 0 {
			t.Fatalf("expected zero dispatches under DryRun, got %v", calls)
		}
	})
}

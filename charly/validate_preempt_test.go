package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// validate_preempt_test.go — tests for the preempt capability validators, now relocated to
// sdk/loaderkit (validate_capabilities.go: loaderkit.ValidatePreemptibleOnNode / ValidatePreemptible).
// These tests drive loaderkit's logic through the host registry-resolve callbacks
// (resolveResourceViaPlugin / resolveVmViaPlugin) — the genuine host coupling the compiled-in
// placement threads. The arbiter's own tests relocated to candy/plugin-preempt. (The former
// deployNodeSharesGPU, gpu_imply.go, and its dedicated test here were a dead-code-radical-removal-batch
// deletion — zero real callers.)

// preemptDiagHasErr / preemptDiagText are the loaderkit.ValidationError.HasErrors / .Error analogues over the
// spec.Diagnostics loaderkit.ValidatePreemptibleOnNode accumulates into (shared with
// preempt_schema_test.go, same package).
func preemptDiagHasErr(d spec.Diagnostics) bool {
	for _, it := range d.Items {
		if it.Severity == "error" {
			return true
		}
	}
	return false
}

func preemptDiagText(d spec.Diagnostics) string {
	var msgs []string
	for _, it := range d.Items {
		if it.Severity == "error" {
			msgs = append(msgs, it.Message)
		}
	}
	return strings.Join(msgs, "\n")
}

// A node may not claim a resource BOTH exclusively and shared (the arbiter dispatches on one or
// the other; the driver modes are mutually exclusive).
func TestValidate_BothExclusiveAndShared_Errors(t *testing.T) {
	node := spec.BundleNode{
		RequiresExclusive: []string{"nvidia-gpu"},
		RequiresShared:    []string{"nvidia-gpu"},
	}
	var d spec.Diagnostics
	loaderkit.ValidatePreemptibleOnNode("bad", &node, &d)
	if !preemptDiagHasErr(d) || !strings.Contains(preemptDiagText(d), "both") {
		t.Fatalf("expected a both-exclusive-and-shared validation error, got: %q", preemptDiagText(d))
	}
}

// TestValidateResourceDefs_ExclusiveVenueTrait proves the resource-defs cross-check (now inside
// loaderkit.ValidatePreemptible) consults the ExclusiveVenue TRAIT (the stamped node.Descent), not a
// `node.Target != "vm"` kind-word string-compare — the boundary-law fix for the incomplete seam ruled
// 2026-07-20. A vm-targeted node stamped with its registry-declared descent (kit.StampDescent, exactly
// as the loader does) requiring a GPU resource while its VM entity pins `backend: qemu` must still be
// flagged, and a non-exclusive-venue node (pod) making the same claim must NOT be — proving the check
// fires on the TRAIT, not on a hardcoded "vm" string a future exclusive-venue substrate wouldn't match.
func TestValidateResourceDefs_ExclusiveVenueTrait(t *testing.T) {
	resources := map[string]json.RawMessage{
		"nvidia-gpu": json.RawMessage(`{"gpu":{"vendor":"0x10de"}}`),
	}
	vmEntities := map[string]json.RawMessage{
		"myvm": json.RawMessage(`{"backend":"qemu","source":{"kind":"cloud_image","url":"http://x"}}`),
	}

	mkNode := func(target string) spec.BundleNode {
		n := spec.BundleNode{Target: target, From: "myvm", RequiresExclusive: []string{"nvidia-gpu"}}
		kit.StampDescent(&n, deployTraitsFor)
		return n
	}

	t.Run("vm (exclusive venue) qemu backend flagged", func(t *testing.T) {
		uf := &loaderkit.UnifiedFile{
			PluginKinds: map[string]map[string]json.RawMessage{"resource": resources, "vm": vmEntities},
			Bundle:      map[string]spec.BundleNode{"mydeploy": mkNode("vm")},
		}
		err := loaderkit.ValidatePreemptible(uf, resolveResourceViaPlugin, resolveVmViaPlugin)
		if err == nil || !strings.Contains(err.Error(), "backend: libvirt") {
			t.Fatalf("expected a qemu-backend GPU-passthrough error, got: %v", err)
		}
	})

	t.Run("pod (non-exclusive venue) never flagged", func(t *testing.T) {
		uf := &loaderkit.UnifiedFile{
			PluginKinds: map[string]map[string]json.RawMessage{"resource": resources, "vm": vmEntities},
			Bundle:      map[string]spec.BundleNode{"mydeploy": mkNode("pod")},
		}
		if err := loaderkit.ValidatePreemptible(uf, resolveResourceViaPlugin, resolveVmViaPlugin); err != nil {
			t.Fatalf("pod node must never trigger the exclusive-venue GPU check, got: %v", err)
		}
	})
}

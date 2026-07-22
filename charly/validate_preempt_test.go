package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// validate_preempt_test.go — core-side tests for the preempt helpers that STAY in core after the
// arbiter's C9 move: the node validator (ValidatePreemptibleOnNode). The arbiter's own tests
// relocated to candy/plugin-preempt. (The former deployNodeSharesGPU, gpu_imply.go, and its
// dedicated test here were a dead-code-radical-removal-batch deletion — zero real callers.)

// A node may not claim a resource BOTH exclusively and shared (the arbiter dispatches on one or
// the other; the driver modes are mutually exclusive).
func TestValidate_BothExclusiveAndShared_Errors(t *testing.T) {
	node := spec.BundleNode{
		RequiresExclusive: []string{"nvidia-gpu"},
		RequiresShared:    []string{"nvidia-gpu"},
	}
	errs := &ValidationError{}
	ValidatePreemptibleOnNode("bad", &node, errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "both") {
		t.Fatalf("expected a both-exclusive-and-shared validation error, got: %q", errs.Error())
	}
}

// TestValidateResourceDefs_ExclusiveVenueTrait proves validateResourceDefs consults the
// ExclusiveVenue TRAIT (nodeTraits), not a `node.Target != "vm"` kind-word string-compare — the
// boundary-law fix for the incomplete seam ruled 2026-07-20. A vm-targeted node stamped with its
// registry-declared descent (kit.StampDescent, exactly as the loader does) requiring a GPU
// resource while its VM entity pins `backend: qemu` must still be flagged, and a non-exclusive-
// venue node (pod) making the same claim must NOT be — proving the check fires on the TRAIT, not
// on a hardcoded "vm" string a future exclusive-venue substrate wouldn't match.
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
		uf := &UnifiedFile{
			PluginKinds: map[string]map[string]json.RawMessage{"resource": resources},
			VM:          vmEntities,
			Bundle:      map[string]spec.BundleNode{"mydeploy": mkNode("vm")},
		}
		errs := &ValidationError{}
		validateResourceDefs(uf, errs)
		if !errs.HasErrors() || !strings.Contains(errs.Error(), "backend: libvirt") {
			t.Fatalf("expected a qemu-backend GPU-passthrough error, got: %q", errs.Error())
		}
	})

	t.Run("pod (non-exclusive venue) never flagged", func(t *testing.T) {
		uf := &UnifiedFile{
			PluginKinds: map[string]map[string]json.RawMessage{"resource": resources},
			VM:          vmEntities,
			Bundle:      map[string]spec.BundleNode{"mydeploy": mkNode("pod")},
		}
		errs := &ValidationError{}
		validateResourceDefs(uf, errs)
		if errs.HasErrors() {
			t.Fatalf("pod node must never trigger the exclusive-venue GPU check, got: %q", errs.Error())
		}
	})
}

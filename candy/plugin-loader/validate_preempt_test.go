package loader

// Relocated from charly/validate_preempt_test.go (#55 decoupling cone, Batch
// C): tests for the preempt capability validators (loaderkit.ValidatePreemptibleOnNode /
// ValidatePreemptible). These drive loaderkit's logic through STUB resolve
// callbacks standing in for charly's registry-resolve callbacks
// (resolveResourceViaPlugin / resolveVmViaPlugin) and a stub traits-lookup
// standing in for charly's deployTraitsFor — the genuine host coupling the
// compiled-in placement threads via the live provider registry, which a
// plugin's own unit test cannot construct. The stubs decode the wire-identical
// JSON body directly / return a literal trait, which is equivalent for these
// validators' purposes (they consult only the resolved envelope's fields).
// preempt_schema_test.go's own TestValidatePreemptibleOnNode (owned by Batch
// A, per the binding file-ownership matrix) shares this file's
// preemptDiagHasErr/preemptDiagText helper shape — see that batch's own
// relocation for its copy.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// preemptDiagHasErr / preemptDiagText are the spec.ValidationError.HasErrors /
// .Error analogues over the spec.Diagnostics loaderkit.ValidatePreemptibleOnNode
// accumulates into.
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

// stubResolveResource is a test-local stand-in for charly's resolveResourceViaPlugin.
func stubResolveResource(body json.RawMessage) (*spec.ResolvedResource, error) {
	var out spec.ResolvedResource
	if len(body) == 0 {
		return &out, nil
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// stubResolveVm is a test-local stand-in for charly's resolveVmViaPlugin.
func stubResolveVm(body json.RawMessage) (*spec.ResolvedVm, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var out spec.ResolvedVm
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// stubTraitsFor is a test-local stand-in for charly's deployTraitsFor (which
// resolves a substrate word's declared #DeployTraits from the live provider
// registry): "vm" is the one ExclusiveVenue substrate this validator cares
// about; every other word gets the zero-value traits.
func stubTraitsFor(word string) *spec.DeployTraits {
	if word == "vm" {
		return &spec.DeployTraits{Venue: "ssh", ExclusiveVenue: true}
	}
	return &spec.DeployTraits{}
}

// A node may not claim a resource BOTH exclusively and shared (the arbiter
// dispatches on one or the other; the driver modes are mutually exclusive).
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

// TestValidateResourceDefs_ExclusiveVenueTrait proves the resource-defs
// cross-check (inside loaderkit.ValidatePreemptible) consults the
// ExclusiveVenue TRAIT (the stamped node.Descent), not a `node.Target != "vm"`
// kind-word string-compare. A vm-targeted node stamped with its declared
// descent requiring a GPU resource while its VM entity pins `backend: qemu`
// must still be flagged, and a non-exclusive-venue node (pod) making the same
// claim must NOT be.
func TestValidateResourceDefs_ExclusiveVenueTrait(t *testing.T) {
	resources := map[string]json.RawMessage{
		"nvidia-gpu": json.RawMessage(`{"gpu":{"vendor":"0x10de"}}`),
	}
	vmEntities := map[string]json.RawMessage{
		"myvm": json.RawMessage(`{"backend":"qemu","source":{"kind":"cloud_image","url":"http://x"}}`),
	}

	mkNode := func(target string) spec.BundleNode {
		n := spec.BundleNode{Target: target, From: "myvm", RequiresExclusive: []string{"nvidia-gpu"}}
		spec.StampDescent(&n, stubTraitsFor)
		return n
	}

	t.Run("vm (exclusive venue) qemu backend flagged", func(t *testing.T) {
		uf := &spec.UnifiedFile{
			PluginKinds: map[string]map[string]json.RawMessage{"resource": resources, "vm": vmEntities},
			Bundle:      map[string]spec.BundleNode{"mydeploy": mkNode("vm")},
		}
		err := loaderkit.ValidatePreemptible(uf, stubResolveResource, stubResolveVm)
		if err == nil || !strings.Contains(err.Error(), "backend: libvirt") {
			t.Fatalf("expected a qemu-backend GPU-passthrough error, got: %v", err)
		}
	})

	t.Run("pod (non-exclusive venue) never flagged", func(t *testing.T) {
		uf := &spec.UnifiedFile{
			PluginKinds: map[string]map[string]json.RawMessage{"resource": resources, "vm": vmEntities},
			Bundle:      map[string]spec.BundleNode{"mydeploy": mkNode("pod")},
		}
		if err := loaderkit.ValidatePreemptible(uf, stubResolveResource, stubResolveVm); err != nil {
			t.Fatalf("pod node must never trigger the exclusive-venue GPU check, got: %v", err)
		}
	})
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// hvgTestProv is a minimal HOST-VALUE-GATED kind provider for `task`: it echoes the
// OpLoad body and honours OpValidate. Registered into the test registry ONLY when the
// REAL compiled-in candy/plugin-task provider is absent (a charly build without the
// plugin pin), so this test is placement-tolerant: it exercises the generic seam
// against whichever `task` provider the build carries.
type hvgTestProv struct{ reject bool }

func (p *hvgTestProv) Reserved() string     { return "task" }
func (p *hvgTestProv) Class() ProviderClass { return ClassKind }

// IsValidatingKind makes the double declare the deep OpValidate capability the host
// dispatches at load (the real candy/plugin-task declares Validates:true too).
func (p *hvgTestProv) IsValidatingKind() bool { return true }

func (p *hvgTestProv) Invoke(_ context.Context, op *Operation) (*Result, error) {
	switch op.Op {
	case ops.OpLoad:
		return &Result{JSON: op.Params}, nil
	case ops.OpValidate:
		var diags spec.Diagnostics
		if p.reject {
			diags.Items = append(diags.Items, spec.Diagnostic{Severity: "error", Path: "description", Message: "description is required"})
		}
		b, _ := json.Marshal(diags)
		return &Result{JSON: b}, nil
	default:
		return nil, fmt.Errorf("unexpected op %q", op.Op)
	}
}

// TaskProvider resolves the `task` kind provider, registering a test double when the
// real compiled-in plugin is not present in the build.
func taskProvider(t *testing.T) Provider {
	t.Helper()
	if prov, ok := providerRegistry.ResolveKind("task"); ok {
		return prov
	}
	t.Cleanup(snapshotProviderState())
	prov := &hvgTestProv{}
	RegisterBuiltinProvider(prov)
	return prov
}

// TestFoldHostValueGatedKind_Task proves the generic host-value-gated kind arm
// (foldHostValueGatedKind) for the `task` kind whose value is closedness-gated against
// the kept #TaskValue def:
//
//   - a valid `task:` node folds opaquely into acc.PluginKinds["task"] (NOT acc.Deploy
//     — a host-value-gated kind is not a deploy substrate);
//   - a typo'd field FAILS the closedness gate (the kept def is CLOSED);
//   - a task missing its required `description` FAILS via the deep OpValidate (the
//     CONCRETE gate the closedness-only host check cannot express).
//
// The whole test FAILS without the generic seam (`task` would not resolve to this arm).
func TestFoldHostValueGatedKind_Task(t *testing.T) {
	prov := taskProvider(t)

	good := singleParsedNode(t, `mytask:
  task:
    description: a task
    plan:
      - check: x
        command: "true"
        context: [deploy]
`)
	var acc spec.MaterializedProject
	if err := foldHostValueGatedKind(prov, good, &acc); err != nil {
		t.Fatalf("valid task must fold: %v", err)
	}
	if acc.PluginKinds["task"]["mytask"] == nil {
		t.Fatalf("task not folded into PluginKinds[task]; got %v", acc.PluginKinds)
	}
	if _, isDeploy := acc.Deploy["mytask"]; isDeploy {
		t.Fatal("a host-value-gated kind must NOT fold into acc.Deploy")
	}

	// closedness: a typo'd task field is rejected by the kept #TaskValue def.
	typo := singleParsedNode(t, "badtask:\n  task:\n    description: x\n    bogus: 1\n")
	var acc2 spec.MaterializedProject
	if err := foldHostValueGatedKind(prov, typo, &acc2); err == nil {
		t.Fatal("a typo'd task field must be rejected by the host value gate")
	}

	// concreteness: a missing required description fails the deep OpValidate. The real
	// plugin rejects it unconditionally; the test double is told to (mirroring it).
	if p, ok := prov.(*hvgTestProv); ok {
		p.reject = true
	}
	missing := singleParsedNode(t, "t:\n  task:\n    plan:\n      - check: x\n        command: \"true\"\n")
	var acc3 spec.MaterializedProject
	if err := foldHostValueGatedKind(prov, missing, &acc3); err == nil {
		t.Fatal("a task missing its required description must fail the deep OpValidate")
	}
}

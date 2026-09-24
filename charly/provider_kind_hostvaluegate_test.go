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
// OpLoad body and honours OpValidate. It stands in for the not-yet-pinned
// candy/plugin-task provider so this test exercises the DISPATCH ARM in runPluginKind.
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

// TestRunPluginKind_HostValueGatedTask drives the DISPATCH ARM itself — a `task:` node
// through runPluginKind — so deleting the generic arm's branch would leave this test
// RED (the node would fall to the flat op.Params path and fail "no input def
// registered"). It proves:
//
//   - a valid `task:` node dispatches through the arm and folds opaquely into
//     acc.PluginKinds["task"] (NOT acc.Deploy — it is not a deploy substrate);
//   - a typo'd field FAILS the closedness gate (the kept #TaskValue def is CLOSED);
//   - a task missing its required `description` FAILS via the deep OpValidate (the
//     CONCRETE gate the closedness-only host check cannot express).
func TestRunPluginKind_HostValueGatedTask(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	// The double is passed to runPluginKind DIRECTLY, so it need not be (and must
	// not be) registered: candy/plugin-task is now compiled in via charly.yml
	// `compiled_plugins:`, so its real kind:task provider already owns the registry
	// key — a RegisterBuiltinProvider of the double would panic on the duplicate.
	prov := &hvgTestProv{}

	good := singleParsedNode(t, `mytask:
  task:
    description: a task
    plan:
      - check: x
        command: "true"
        context: [deploy]
`)
	var acc spec.MaterializedProject
	if err := runPluginKind(prov, good, &acc); err != nil {
		t.Fatalf("valid task must dispatch through the arm and fold: %v", err)
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
	if err := runPluginKind(prov, typo, &acc2); err == nil {
		t.Fatal("a typo'd task field must be rejected by the host value gate")
	}

	// concreteness: a missing required description fails the deep OpValidate.
	prov.reject = true
	missing := singleParsedNode(t, "t:\n  task:\n    plan:\n      - check: x\n        command: \"true\"\n")
	var acc3 spec.MaterializedProject
	if err := runPluginKind(prov, missing, &acc3); err == nil {
		t.Fatal("a task missing its required description must fail the deep OpValidate")
	}
}

package main

import (
	"context"
	"encoding/json"
	"testing"
)

// TestRelocatedMatchingVerb_DispatchesViaRegistry proves the `matching` check verb —
// relocated to candy/plugin-matching (a compiled-in pb-shape candy) — resolves through
// the providerRegistry and runs its goss-style matcher evaluation via the pb Invoke
// envelope (the in-proc path registerCompiledPlugin wires). Deterministic: a value
// satisfying its matchers passes; one that doesn't fails.
func TestRelocatedMatchingVerb_DispatchesViaRegistry(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("matching")
	if !ok {
		t.Fatal("matching verb not registered — compiled-in pb candy (candy/plugin-matching) failed")
	}

	invoke := func(value string, contains []any) (status, message string) {
		paramsJSON, err := json.Marshal(map[string]any{
			"plugin_input": map[string]any{"matching": value, "contains": contains},
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := prov.Invoke(context.Background(), &Operation{Params: paramsJSON})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		var out struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(res.JSON, &out); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		return out.Status, out.Message
	}

	// value satisfies a bare-scalar equals AND two substring-contains matchers → pass.
	if status, msg := invoke("charly-candy-factory", []any{"charly-candy-factory", map[string]any{"contains": "charly"}, map[string]any{"contains": "candy"}}); status != "pass" {
		t.Fatalf("matching value: want pass, got %s: %s", status, msg)
	}
	// value fails a contains matcher → fail.
	if status, _ := invoke("nope", []any{map[string]any{"contains": "charly"}}); status != "fail" {
		t.Fatalf("non-matching value: want fail, got %s", status)
	}
}

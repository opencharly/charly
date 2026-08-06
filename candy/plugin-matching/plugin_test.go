package matching

import (
	"context"
	"encoding/json"
	"testing"

	pb "github.com/opencharly/spec/proto"
)

// TestMatchingVerb: pure in-process value matching. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_MatchingPlugin (#55 decoupling cone, Batch D) —
// the verb has no kit.CheckContext at all (STATELESS provider), so the test drives its raw
// Invoke directly instead of via a fake CheckContext.
func TestMatchingVerb(t *testing.T) {
	params, err := json.Marshal(map[string]any{
		"plugin_input": map[string]any{
			"matching": "hello world",
			"contains": map[string]any{"contains": "world"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := (provider{}).Invoke(context.Background(), &pb.InvokeRequest{ParamsJson: params})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var res struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(reply.GetResultJson(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Status != "pass" {
		t.Errorf("got %+v", res)
	}
}

// invokeMatching drives the provider's raw Invoke and returns the decoded status.
func invokeMatching(t *testing.T, value string, contains []any) string {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"plugin_input": map[string]any{"matching": value, "contains": contains},
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := (provider{}).Invoke(context.Background(), &pb.InvokeRequest{ParamsJson: params})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var res struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(reply.GetResultJson(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return res.Status
}

// TestMatchingVerb_MultiMatcher: a value satisfying a bare-scalar equals AND two
// substring-contains matchers passes. Relocated from
// charly/plugin_matching_relocated_test.go's TestRelocatedMatchingVerb_DispatchesViaRegistry
// (the check-role behavior half; the dispatch wiring stays in charly).
func TestMatchingVerb_MultiMatcher(t *testing.T) {
	if status := invokeMatching(t, "charly-candy-factory",
		[]any{"charly-candy-factory", map[string]any{"contains": "charly"}, map[string]any{"contains": "candy"}}); status != "pass" {
		t.Fatalf("matching value: want pass, got %s", status)
	}
}

// TestMatchingVerb_Fail: a value failing a contains matcher must FAIL.
func TestMatchingVerb_Fail(t *testing.T) {
	if status := invokeMatching(t, "nope", []any{map[string]any{"contains": "charly"}}); status != "fail" {
		t.Fatalf("non-matching value: want fail, got %s", status)
	}
}

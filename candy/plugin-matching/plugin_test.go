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

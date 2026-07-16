package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// fakeExternalVerb is an OUT-OF-PROCESS-style verb Provider: a real Provider for a live
// verb word that is NOT a CheckVerbProvider (it has no typed in-proc RunVerb dispatch
// path), exactly the shape a verb takes once its implementation moves out-of-tree. It
// records the Operation it is handed so the test can prove the FULL Op crossed.
type fakeExternalVerb struct {
	reply     string
	gotWord   string
	gotParams []byte
}

func (f *fakeExternalVerb) Reserved() string     { return "kube" }
func (f *fakeExternalVerb) Class() ProviderClass { return ClassVerb }
func (f *fakeExternalVerb) Invoke(_ context.Context, op *Operation) (*Result, error) {
	f.gotWord = op.Reserved
	f.gotParams = op.Params
	return &Result{JSON: []byte(f.reply)}, nil
}

// TestInvokeVerbProvider_ExternalCharlyVerb proves the external-charly-verb dispatch
// (the `else` branch in checkrun.go routes here): a live verb word whose provider is
// OUT-OF-PROCESS (not a CheckVerbProvider) is dispatched via invokeVerbProvider, which
// hands the plugin the FULL Op as params_json: the desugared internal envelope
// (Plugin + PluginInput) reaches the provider verbatim, so the plugin decodes its
// own input def from PluginInput.
func TestInvokeVerbProvider_ExternalCharlyVerb(t *testing.T) {
	r := hostVerbResolverFor(nil, RunModeBox)
	fake := &fakeExternalVerb{reply: `{"status":"pass","message":"saw-op"}`}

	op := &spec.Op{Plugin: "kube", PluginInput: map[string]any{"method": "apply", "namespace": "demo"}}
	res := r.invokeVerbProvider(context.Background(), fake, "kube", op)
	if res.Status != TestPass {
		t.Fatalf("status=%v msg=%q, want pass", res.Status, res.Message)
	}
	if res.Message != "saw-op" {
		t.Fatalf("message=%q, want saw-op (pluginCheckResult decode)", res.Message)
	}
	if fake.gotWord != "kube" {
		t.Fatalf("provider saw word %q, want kube", fake.gotWord)
	}

	// The provider received the FULL Op as params_json — the proof a verb's #Op
	// authoring needs no migration to externalize.
	var seen spec.Op
	if err := json.Unmarshal(fake.gotParams, &seen); err != nil {
		t.Fatalf("params_json is not the Op: %v", err)
	}
	if seen.PluginInput["method"] != "apply" || seen.PluginInput["namespace"] != "demo" {
		t.Fatalf("plugin saw PluginInput %v, want method=apply namespace=demo — the full Op did not reach it",
			seen.PluginInput)
	}
}

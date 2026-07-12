package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestCompiledInPlugin_ExternalprobeDispatches proves the "one provider, two
// placements" in-proc path end-to-end: the externalprobe verb — authored as an
// out-of-tree plugin candy (candy/plugin-example-external, its provider in an
// IMPORTABLE package) — is COMPILED INTO charly via plugins_generated.go's
// registerCompiledPlugin (resolved through go.work) and dispatches through the
// SAME providerRegistry.ResolveVerb path a built-in or an out-of-process plugin
// uses, with plugin_input round-tripping author -> in-proc provider -> result.
func TestCompiledInPlugin_ExternalprobeDispatches(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("externalprobe")
	if !ok {
		t.Fatal("externalprobe verb not registered — compiled-in plugin registration failed")
	}
	params, _ := json.Marshal(map[string]any{"plugin_input": map[string]string{"marker": "compiled-in-ok"}})
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: "externalprobe", Op: "run", Params: params})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		t.Fatalf("unmarshal result %q: %v", res.JSON, err)
	}
	if out.Status != "pass" || out.Message != "compiled-in-ok" {
		t.Fatalf("got status=%q message=%q, want pass/compiled-in-ok", out.Status, out.Message)
	}
}

// TestCompiledInPlugin_SchemaGated proves the compiled-in candy's schema reached
// the SAME load gate a builtin/external schema does: loadBuiltinPluginUnits must
// accept it (the candy's Describe-served #ExternalprobeInput splices onto base).
func TestCompiledInPlugin_SchemaGated(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if err := loadBuiltinPluginUnits(); err != nil {
		t.Fatalf("loadBuiltinPluginUnits (gates the compiled-in externalprobe schema): %v", err)
	}
}

// TestCoexistSwitch_CompiledInSkipsOutOfProcess proves the placement-coexist path:
// a word compiled in (origin "builtin", via registerCompiledPlugin in
// plugins_generated.go) makes an out-of-tree candy declaring the SAME word a SKIP
// (connected=true, no error) in pluginAlreadyConnected — the in-proc placement wins
// and the redundant host build+connect is avoided, NOT a collision error.
func TestCoexistSwitch_CompiledInSkipsOutOfProcess(t *testing.T) {
	if _, ok := providerRegistry.ResolveVerb("externalprobe"); !ok {
		t.Fatal("externalprobe must be compiled in (plugins_generated.go) for this test")
	}
	decl := &CandyPluginDecl{
		Source:    "github.com/opencharly/charly/candy/plugin-example-external",
		Providers: []spec.PluginCapability{"verb:externalprobe"},
	}
	connected, err := pluginAlreadyConnected("plugin-example-external", decl)
	if err != nil {
		t.Fatalf("coexist switch must SKIP a compiled-in word, got collision error: %v", err)
	}
	if !connected {
		t.Fatal("coexist switch must report connected=true (skip) for a compiled-in word")
	}
}

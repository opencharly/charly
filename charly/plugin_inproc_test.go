package main

import (
	"context"
	"encoding/json"
	"testing"
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
	connected, err := pluginAlreadyConnected("plugin-example-external",
		"github.com/opencharly/charly/candy/plugin-example-external", []string{"verb:externalprobe"})
	if err != nil {
		t.Fatalf("coexist switch must SKIP a compiled-in word, got collision error: %v", err)
	}
	if !connected {
		t.Fatal("coexist switch must report connected=true (skip) for a compiled-in word")
	}
}

// TestCoexistSwitch_PartialBuiltinStillLoads pins the PER-WORD placement-coexist rule:
// a candy that declares SOME compiled-in words and SOME of its own must NOT be skipped
// as "already connected" — its own words still need the out-of-process load. The former
// whole-candy rule ("any one builtin word ⇒ skip the candy") silently dropped
// candy/plugin-kubevirt's `verb:kubevirt` (its `kind:kubevirt` is compiled into
// candy/plugin-substrate), so `kubevirt:` resolved "no provider registered for plugin
// verb" in the check-kubevirt-operator R10 bed. The sibling
// TestCoexistSwitch_CompiledInSkipsOutOfProcess pins the all-builtin (skip) half.
func TestCoexistSwitch_PartialBuiltinStillLoads(t *testing.T) {
	if _, ok := providerRegistry.ResolveVerb("externalprobe"); !ok {
		t.Fatal("externalprobe must be compiled in (plugins_generated.go) for this test")
	}
	// One compiled-in word + one this candy owns that nothing serves yet.
	connected, err := pluginAlreadyConnected("plugin-kubevirt-shaped",
		"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt",
		[]string{"verb:externalprobe", "verb:neverregistered-kubevirt"})
	if err != nil {
		t.Fatalf("a partial-builtin candy is not a collision: %v", err)
	}
	if connected {
		t.Fatal("a candy with an unserved own word must NOT be reported connected (it must load)")
	}
}

// TestRegister_BuiltinVsBuiltinStillErrors pins the boundary of the per-word coexist
// skip: a BUILTIN registration colliding with an existing builtin is still a hard error
// (init() panics on it), so the skip cannot silently swallow a genuine builtin duplicate.
// Only an OUT-OF-PROCESS registration may coexist with — and be skipped by — a builtin,
// and a same-origin out-of-process re-registration is idempotent.
func TestRegister_BuiltinVsBuiltinStillErrors(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if err := providerRegistry.register(stubIdemVerb{}, originBuiltin); err != nil {
		t.Fatalf("first builtin register: %v", err)
	}
	if err := providerRegistry.register(stubIdemVerb{}, originBuiltin); err == nil {
		t.Fatal("a builtin-vs-builtin duplicate must still error (init() fail-fast invariant)")
	}
	// An out-of-process registration on the same builtin-owned word is the sanctioned
	// coexist skip — no error.
	if err := providerRegistry.register(stubIdemVerb{}, "github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt"); err != nil {
		t.Fatalf("an out-of-process register on a builtin word must be a skip, got: %v", err)
	}
	// A SAME-ORIGIN out-of-process re-registration is idempotent (the same unit loaded
	// twice) — skipped, no error.
	const src = "github.com/test/same-origin-idem"
	if err := providerRegistry.register(testVerbProvider{word: "samesrcverb"}, src); err != nil {
		t.Fatalf("first out-of-process register: %v", err)
	}
	if err := providerRegistry.register(testVerbProvider{word: "samesrcverb"}, src); err != nil {
		t.Fatalf("a same-origin re-register must be idempotent, got: %v", err)
	}
	// A DIFFERENT out-of-process origin on the same word is still a real collision.
	if err := providerRegistry.register(testVerbProvider{word: "samesrcverb"}, "github.com/other/repo"); err == nil {
		t.Fatal("a different-origin collision on the same word must still error")
	}
}

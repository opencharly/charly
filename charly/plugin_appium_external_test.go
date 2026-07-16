package main

import (
	"context"
	"encoding/json"
	"github.com/opencharly/sdk/spec"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
)

// TestAppiumExternalPluginLoads is the RDD load proof for the appium → external-plugin
// dep-shed: it builds the out-of-tree candy/plugin-appium module on the host, connects
// to it OUT-OF-PROCESS over go-plugin gRPC via LocalTransport (the loadPluginUnit path),
// and proves the registry resolves `appium` as an EXTERNAL (non-CheckVerbProvider) verb
// that dispatches over the wire — the exact path runOne takes for the `appium:` verb
// (else-branch → invokeVerbProvider). A box-mode Invoke round-trips author → wire →
// external process → result WITHOUT a live emulator (the verb skips in box mode), which
// is enough to prove load + dispatch; the live W3C behaviour is proved by the
// check-android-emulator-pod R10 bed.
func TestAppiumExternalPluginLoads(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds + execs the external appium plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-appium")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("external appium plugin module not found at %s: %v", srcDir, err)
	}

	// 1. Host-build the provider binary (the loader's buildPluginBinary step).
	bin, err := buildPluginBinary(ctx, srcDir, "plugin-appium-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}

	// 2. Connect OUT-OF-PROCESS via LocalTransport — providers AND schema arrive on the
	//    unit, both lifted from the Describe channel (the served schema travels the wire,
	//    NOT read from the candy dir).
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassVerb || unit.Providers[0].Reserved() != "appium" {
		t.Fatalf("providers = %+v, want exactly one verb:appium", unit.Providers)
	}
	// Post schema-compaction, appium's per-verb modifiers live in the plugin's OWN
	// served input def (#AppiumInput) — the plugin serves a non-empty schema AND
	// declares the input def its plugin_input validates against.
	if unit.Schema.CueSource == "" {
		t.Fatalf("unit schema CueSource empty — the plugin must ship a non-empty served schema")
	}
	if d := unit.Schema.InputDefs["verb:appium"]; d != "#AppiumInput" {
		t.Fatalf("verb:appium input def = %q, want #AppiumInput (per-verb modifiers moved into the plugin's served schema)", d)
	}

	// 3. Register the served schema through the SAME gate a builtin runs (proves it
	//    splices onto charly's base), then the providers.
	if err := registerPluginUnitSchema("plugin-appium-test", unit.Schema); err != nil {
		t.Fatalf("registerPluginUnitSchema (served schema must splice onto base): %v", err)
	}
	reg := newRegistry()
	if err := reg.RegisterPluginProviders(unit.Providers, "test:external-appium", nil); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	prov, ok := reg.ResolveVerb("appium")
	if !ok {
		t.Fatal("appium not resolvable after registration")
	}
	// The whole point of the dep-shed: appium is resolved as an EXTERNAL verb. It must
	// NOT be a CheckVerbProvider (the in-proc path) — runOne dispatches it via the
	// else-branch (invokeVerbProvider), the external-charly-verb path.
	if _, isInProc := prov.(CheckVerbProvider); isInProc {
		t.Fatal("resolved appium provider is a CheckVerbProvider (in-proc) — must be the out-of-process grpcProvider")
	}

	// 4. Invoke across the process boundary in BOX mode: the full #Op marshals over the
	//    wire and the verb skips (no running container) — proving load + dispatch
	//    round-trip end to end without a live emulator.
	params, err := json.Marshal(&spec.Op{Plugin: "appium", PluginInput: map[string]any{"method": "status"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := prov.Invoke(ctx, &Operation{
		Reserved: "appium", Op: OpRun,
		Params: params, Env: []byte(`{"box":"check-android-emulator-pod","mode":"box"}`),
	})
	if err != nil {
		t.Fatalf("Invoke (out-of-proc): %v", err)
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		t.Fatalf("decode result: %v (%s)", err, out.JSON)
	}
	if pr.Status != "skip" {
		t.Fatalf("box-mode appium status result = %+v, want skip (live-container verb skips under check box)", pr)
	}
	// The skip message names the method, proving the desugared input crossed the
	// wire intact — the plugin read it from the marshaled Op's PluginInput.
	if !strings.Contains(pr.Message, "status") {
		t.Fatalf("skip message %q does not name the method — the plugin input did not cross the wire", pr.Message)
	}
}

// TestAppiumOpCrossesWireWithMatchers proves the load-bearing assumption that the FULL
// #Op — including the stdout/stderr MatcherList an `appium:` step authors — survives the
// params_json marshal the host (invokeVerbProvider) does and the spec.Op unmarshal the
// external plugin does. Because the external dispatch path does NOT run a host-side
// matcher pipeline, the plugin must receive the matchers to self-evaluate
// them; this round-trip is exactly that wire path (the Op type is identical on both ends).
func TestAppiumOpCrossesWireWithMatchers(t *testing.T) {
	op := &spec.Op{
		Plugin:      "appium",
		PluginInput: map[string]any{"method": "status"},
		Stdout:      spec.MatcherList{{Op: "contains", Value: `"ready":true`}},
	}
	params, err := json.Marshal(op) // the host's marshalJSON(c)
	if err != nil {
		t.Fatalf("marshal op: %v", err)
	}
	var got spec.Op // the plugin's json.Unmarshal(params_json, &spec.Op)
	if err := json.Unmarshal(params, &got); err != nil {
		t.Fatalf("unmarshal op: %v", err)
	}
	if got.PluginInput["method"] != "status" {
		t.Fatalf("the plugin input method did not survive: %v", got.PluginInput)
	}
	if len(got.Stdout) != 1 || got.Stdout[0].Op != "contains" {
		t.Fatalf("stdout MatcherList did not survive the wire: %+v", got.Stdout)
	}
	// The round-tripped matcher must still FUNCTION through the SAME sdk.MatchAll the
	// plugin's provider.go calls — proving the plugin can self-evaluate the authored
	// matcher against the verb's captured output.
	if err := sdk.MatchAll(`{"value":{"ready":true}}`, got.Stdout); err != nil {
		t.Fatalf("round-tripped matcher does not match a ready status body: %v", err)
	}
	if err := sdk.MatchAll(`{"value":{"ready":false}}`, got.Stdout); err == nil {
		t.Fatal("round-tripped matcher should NOT match a not-ready body")
	}
}

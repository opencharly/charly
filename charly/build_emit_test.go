package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// stubActKitVerb is a spec.CheckVerbProvider that ALSO implements checkstep.ProvisionActor — a
// state-provision verb (the file/user/mount/… family). Wrapped in a kitVerbActAdapter, its
// build-context ops.OpEmit must return the RenderProvisionScript act shell with ActScript=true
// (the render then RUN-wraps it via EmitCmd — the former IsScript=true path).
type stubActKitVerb struct{}

func (stubActKitVerb) Reserved() string { return "stubact" }
func (stubActKitVerb) RunVerb(context.Context, spec.CheckContext, *spec.Op) spec.CheckVerbResult {
	return spec.CheckVerbResult{Status: spec.StatusPass}
}
func (stubActKitVerb) RenderProvisionScript(op *spec.Op, distros []string) (string, bool) {
	return "install -m " + op.Mode + " /dev/null /opt/stubact-provisioned", true
}

// TestKitVerbActAdapter_OpEmitActScript is the check-coverage gate for the P8b seam-death:
// a state-provision verb serves ops.OpEmit UNIFORMLY and self-declares its act shell via
// EmitReply.ActScript=true (the fragment is the raw RenderProvisionScript output, NOT a
// verbatim Containerfile fragment) — so the render dispatches it through the SAME
// Invoke(ops.OpEmit) as any other verb, with no package-main concrete-type assert. This test
// FAILS without the kitVerbActAdapter.Invoke(ops.OpEmit) path.
func TestKitVerbActAdapter_OpEmitActScript(t *testing.T) {
	kv := stubActKitVerb{}
	adapter := kitVerbActAdapter{kitVerbAdapter: kitVerbAdapter{kv: kv}, pa: kv}
	op := &spec.Op{Plugin: "stubact", Mode: "0644"}
	params, err := marshalJSON(op)
	if err != nil {
		t.Fatalf("marshal op: %v", err)
	}
	env, err := marshalJSON(spec.BuildEnv{Distros: []string{"fedora:43", "fedora"}})
	if err != nil {
		t.Fatalf("marshal build env: %v", err)
	}
	res, err := adapter.Invoke(context.Background(), &Operation{Reserved: "stubact", Op: ops.OpEmit, Params: params, Env: env})
	if err != nil {
		t.Fatalf("kitVerbActAdapter.Invoke(ops.OpEmit): %v", err)
	}
	var reply spec.EmitReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil {
		t.Fatalf("decode EmitReply: %v", err)
	}
	if !reply.ActScript {
		t.Fatalf("a state-provision verb's ops.OpEmit must set ActScript=true (got false)")
	}
	if !strings.Contains(reply.Fragment, "/opt/stubact-provisioned") {
		t.Fatalf("Fragment = %q, want the RenderProvisionScript act shell", reply.Fragment)
	}
}

// stubBuildEmitterVerb is the BUILTIN placement of a build-emit verb: an in-proc
// provider that BOTH marks the capability (BuildEmits) AND renders the fragment
// (Invoke ops.OpEmit). opActsInBuildDeploy must accept it in a build context.
type stubBuildEmitterVerb struct{}

func (stubBuildEmitterVerb) Reserved() string     { return "stubbuildemit" }
func (stubBuildEmitterVerb) Class() ProviderClass { return ClassVerb }
func (stubBuildEmitterVerb) BuildEmits() bool     { return true }
func (stubBuildEmitterVerb) Invoke(_ context.Context, _ *Operation) (*Result, error) {
	return &Result{JSON: []byte(`{"fragment":"RUN : > /opt/stubbuildemit-baked"}`)}, nil
}

// TestOpActsInBuildDeploy_PlacementAgnosticBuildEmit gates the VALIDATE/recognition
// half of build-time plugin execution — placement-agnostic, both placements of a
// build-emit verb validate as build-act-capable, and an unknown verb does not:
//   - BUILTIN (connected in-proc BuildEmitter),
//   - EXTERNAL standalone-validate (a prescan-declared, not-yet-connected verb — the
//     gap that blocked authoring a build-context external plugin step in `charly box validate`).
func TestOpActsInBuildDeploy_PlacementAgnosticBuildEmit(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	// 1. BUILTIN placement: a connected in-proc BuildEmitter acts in build/deploy.
	if err := providerRegistry.register(stubBuildEmitterVerb{}, "test:stubbuildemit"); err != nil {
		t.Fatalf("register stub BuildEmitter: %v", err)
	}
	if !opActsInBuildDeploy(&spec.Op{Plugin: "stubbuildemit"}) {
		t.Fatalf("a connected builtin BuildEmitter must act in build/deploy")
	}
	// 2. EXTERNAL placement, standalone validate (provider NOT connected): a
	// prescan-declared external verb is trusted build-emit-capable.
	registerDeclaredExternalVerb("stubextemit")
	if !opActsInBuildDeploy(&spec.Op{Plugin: "stubextemit"}) {
		t.Fatalf("a prescan-declared external verb must validate as build-emit-capable")
	}
	// 3. An unknown, undeclared verb is NOT trusted (no blanket accept — a runtime-only
	// verb mistakenly used in a build step must still be caught).
	if opActsInBuildDeploy(&spec.Op{Plugin: "totally-unknown-verb-xyz"}) {
		t.Fatalf("an unknown, undeclared verb must NOT act in build/deploy")
	}
}

// TestValidateWordSets_DeclaredExternalRecognition gates the HOST half of the external-word
// recognition that lets a build-context plugin verb/step served by an out-of-tree (incl.
// @github-composed) candy validate while its provider is NOT connected — the recognition core's
// deleted registerExternalVerbsFromCandies used to obtain by re-scanning the whole project. It now
// arrives as DATA on the "validate-word-sets" seam: candy/plugin-box reads IsPlugin / PluginSource /
// PluginProviders off its OWN envelope and sends the capability strings; the host registers them by
// class and answers act-capability. The CANDY-FILTERING half (a `source: builtin` candy's providers
// are never sent, because a builtin registers at init and resolves through the registry) is gated
// plugin-side by TestFetchValidateWordSets_ExternalProvidersOnly in candy/plugin-box.
func TestValidateWordSets_DeclaredExternalRecognition(t *testing.T) {
	reply, err := hostBuildValidateWordSets(context.Background(), spec.ValidateWordSetsRequest{
		ExternalProviders: []string{"verb:extverbfromcandy", "step:extstepfromcandy", "malformed-no-colon"},
		PluginWords:       []string{"extverbfromcandy", "extstepfromcandy", "totally-unknown-verb-xyz", ""},
	}, buildEngineContext{})
	if err != nil {
		t.Fatalf("validate-word-sets seam failed: %v", err)
	}
	if !isDeclaredExternalVerb("extverbfromcandy") {
		t.Fatalf("an out-of-tree plugin candy's declared verb must be registered for build-context validation")
	}
	if !isDeclaredExternalStep("extstepfromcandy") {
		t.Fatalf("an out-of-tree plugin candy's declared class:step word must be registered too")
	}
	if !slices.Contains(reply.ActCapableVerbs, "extverbfromcandy") {
		t.Fatalf("a declared external verb must come back act-capable; got %v", reply.ActCapableVerbs)
	}
	if !slices.Contains(reply.ActCapableVerbs, "extstepfromcandy") {
		t.Fatalf("a declared external step must come back act-capable; got %v", reply.ActCapableVerbs)
	}
	if slices.Contains(reply.ActCapableVerbs, "totally-unknown-verb-xyz") {
		t.Fatalf("an unknown, undeclared word must NOT come back act-capable; got %v", reply.ActCapableVerbs)
	}
	if slices.Contains(reply.ActCapableVerbs, "") {
		t.Fatalf("the empty word must never enter the reply; got %v", reply.ActCapableVerbs)
	}
	if len(reply.ProviderCapabilities) == 0 {
		t.Fatalf("ProviderCapabilities must enumerate the compiled-in providers (the validatePluginCandy target set)")
	}
}

type stubIdemVerb struct{}

func (stubIdemVerb) Reserved() string     { return "idemverb" }
func (stubIdemVerb) Class() ProviderClass { return ClassVerb }
func (stubIdemVerb) Invoke(context.Context, *Operation) (*Result, error) {
	return &Result{JSON: []byte(`{}`)}, nil
}

// TestPluginAlreadyConnected_Idempotent gates the loader idempotency that makes
// loadProjectPlugins safe to call on every connect path (build + deploy + check) in one
// process without the duplicate-registration warning: a same-source re-load is a no-op
// (skip), while a different-source collision on the same word still errors (the bijection
// backstop). Without the guard, a single process reaching multiple connect paths in one run
// (e.g. `charly bundle add`'s loadDeployPlugins, then a pod-overlay build reaching
// candy/plugin-build's resolveBuildEngine's own hostBuildConnectPlugins) warns on the second load.
func TestPluginAlreadyConnected_Idempotent(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	const src = "github.com/test/idem-plugin"
	if err := providerRegistry.register(stubIdemVerb{}, src); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	// 1. Same source → already connected (the second load is skipped, no duplicate).
	connected, err := pluginAlreadyConnected("idem-plugin", src, []string{"verb:idemverb"})
	if err != nil || !connected {
		t.Fatalf("a same-source re-load must be idempotent: connected=%v err=%v", connected, err)
	}
	// 2. Different source, same word → genuine bijection collision (errors, not skipped).
	if _, err := pluginAlreadyConnected("other-plugin", "github.com/other/repo", []string{"verb:idemverb"}); err == nil {
		t.Fatalf("a different-source collision on the same word must error, not skip")
	}
	// 3. Unregistered word → not connected (proceeds to a real load).
	if connected, err := pluginAlreadyConnected("fresh-plugin", src, []string{"verb:neverregistered-idem"}); err != nil || connected {
		t.Fatalf("an unregistered plugin must not be reported connected: connected=%v err=%v", connected, err)
	}
}

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// stubEmitVerb is an in-proc Provider that emits a build-time Containerfile
// fragment via OpEmit — the same Provider.Invoke interface a real OUT-OF-PROCESS
// grpcProvider satisfies, so this exercises the placement-agnostic build-emit
// dispatch without a subprocess (the gRPC path is covered by the plugin transport
// round-trip tests).
type stubEmitVerb struct{}

func (stubEmitVerb) Reserved() string     { return "stubemit" }
func (stubEmitVerb) Class() ProviderClass { return ClassVerb }
func (stubEmitVerb) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpEmit {
		return &Result{JSON: []byte(`{}`)}, nil
	}
	// A real plugin would tailor the fragment from op.Params (plugin_input) + op.Env
	// (spec.BuildEnv); the stub returns a deterministic RUN proving it ran at build.
	return &Result{JSON: []byte(`{"fragment":"RUN : > /opt/stubemit-baked"}`)}, nil
}

// TestEmitPluginFragment_BuildTimeOpEmit is the build-time-plugin-execution core
// gate: a plugin verb that is NOT a builtin ProvisionActor renders its build-context
// Containerfile fragment via Invoke(OpEmit), which emitTasks splices verbatim into the
// generated Containerfile. This proves the dispatch (the operator-authorized build-time
// plugin execution) extracts the fragment — placement-agnostic, since the stub is reached
// through the SAME Provider.Invoke an external grpcProvider implements.
func TestEmitPluginFragment_BuildTimeOpEmit(t *testing.T) {
	op := &spec.Op{Plugin: "stubemit", PluginInput: map[string]any{"marker": "stubemit-baked"}}
	img := &ResolvedBox{Tags: []string{"fedora:43", "fedora"}}
	frag, err := emitPluginFragment(stubEmitVerb{}, op, img)
	if err != nil {
		t.Fatalf("emitPluginFragment: %v", err)
	}
	if !strings.Contains(frag, "RUN : > /opt/stubemit-baked") {
		t.Fatalf("fragment = %q, want the plugin's baked RUN directive", frag)
	}
}

// stubResolveBuilder is an in-proc Provider that resolves a build-time BUILDER stage
// via OpResolve — the same Provider.Invoke interface a real OUT-OF-PROCESS grpcProvider
// satisfies, so this exercises the placement-agnostic build-prep dispatch (the
// BUILDER leg) without a subprocess (the gRPC path is covered by the plugin transport
// round-trip tests).
type stubResolveBuilder struct{}

func (stubResolveBuilder) Reserved() string     { return "stubbuilder" }
func (stubResolveBuilder) Class() ProviderClass { return ClassBuilder }
func (stubResolveBuilder) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpResolve {
		return &Result{JSON: []byte(`{}`)}, nil
	}
	// A real plugin would tailor the stage from op.Params (the requesting candy) +
	// op.Env (spec.BuildEnv); the stub returns a deterministic multi-stage block + a
	// COPY --from proving it ran at build.
	return &Result{JSON: []byte(`{"stage":"FROM scratch AS stubbuilder-stage\nRUN : > /built\n","copy_artifacts":["COPY --from=stubbuilder-stage /built /opt/stubbuilder-artifact"]}`)}, nil
}

// TestResolveExternalBuilder_BuildTimeOpResolve is the build-time-plugin-execution
// BUILDER-leg gate: an external builder provider renders its build-context multi-stage
// block via Invoke(OpResolve), which deploykit.EmitExternalBuilderStages splices pre-main-FROM
// (the Stage) and deploykit.EmitExternalBuilderArtifacts splices post-main-FROM (the
// CopyArtifacts). This proves the resolve helper extracts both — placement-agnostic,
// since the stub is reached through the SAME Provider.Invoke an external grpcProvider
// implements.
func TestResolveExternalBuilder_BuildTimeOpResolve(t *testing.T) {
	img := &ResolvedBox{Name: "fedora", Tags: []string{"fedora:43", "fedora"}}
	reply, err := resolveExternalBuilder(stubResolveBuilder{}, "stubbuilder", "stubbuilder-consumer", img)
	if err != nil {
		t.Fatalf("resolveExternalBuilder: %v", err)
	}
	if !strings.Contains(reply.Stage, "FROM scratch AS stubbuilder-stage") {
		t.Fatalf("Stage = %q, want the plugin's multi-stage FROM…AS block", reply.Stage)
	}
	if len(reply.CopyArtifacts) != 1 || !strings.Contains(reply.CopyArtifacts[0], "COPY --from=stubbuilder-stage /built /opt/stubbuilder-artifact") {
		t.Fatalf("CopyArtifacts = %v, want the plugin's single COPY --from directive", reply.CopyArtifacts)
	}
}

// stubBuildEmitterVerb is the BUILTIN placement of a build-emit verb: an in-proc
// provider that BOTH marks the capability (BuildEmits) AND renders the fragment
// (Invoke OpEmit). opActsInBuildDeploy must accept it in a build context.
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

// TestRegisterExternalVerbsFromCandies gates the post-scan external-verb recognition that
// lets a build-context plugin verb served by a @github-COMPOSED candy validate (the parse-
// time prescan sees only locally-discovered dirs; the scanned candy map sees the fetched
// @github candy too). An EXTERNAL plugin candy's verb is registered; a BUILTIN one is NOT
// (builtins resolve through the registry, not this not-connected map).
func TestRegisterExternalVerbsFromCandies(t *testing.T) {
	candies := map[string]*Candy{
		"ext-plugin": {Plugin: &spec.Plugin{
			Source:    "github.com/opencharly/charly/candy/ext-plugin",
			Providers: []spec.PluginCapability{"verb:extverbfromcandy"},
		}},
		"builtin-plugin": {Plugin: &spec.Plugin{
			Source:    "builtin",
			Providers: []spec.PluginCapability{"verb:builtinverbfromcandy"},
		}},
		"ordinary": {}, // no plugin block
	}
	registerExternalVerbsFromCandies(candies)
	if !isDeclaredExternalVerb("extverbfromcandy") {
		t.Fatalf("an external (incl. @github) plugin candy's verb must be registered for build-context validation")
	}
	if isDeclaredExternalVerb("builtinverbfromcandy") {
		t.Fatalf("a builtin plugin's verb must NOT enter the declared-external map (it resolves via the registry)")
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
// backstop). Without the guard, `charly bundle add` (loadDeployPlugins then the pod-overlay
// NewGenerator connect seam) warns on the second load.
func TestPluginAlreadyConnected_Idempotent(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	const src = "github.com/test/idem-plugin"
	if err := providerRegistry.register(stubIdemVerb{}, src); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	// 1. Same source → already connected (the second load is skipped, no duplicate).
	connected, err := pluginAlreadyConnected("idem-plugin", &spec.Plugin{
		Source: src, Providers: []spec.PluginCapability{"verb:idemverb"},
	})
	if err != nil || !connected {
		t.Fatalf("a same-source re-load must be idempotent: connected=%v err=%v", connected, err)
	}
	// 2. Different source, same word → genuine bijection collision (errors, not skipped).
	if _, err := pluginAlreadyConnected("other-plugin", &spec.Plugin{
		Source: "github.com/other/repo", Providers: []spec.PluginCapability{"verb:idemverb"},
	}); err == nil {
		t.Fatalf("a different-source collision on the same word must error, not skip")
	}
	// 3. Unregistered word → not connected (proceeds to a real load).
	if connected, err := pluginAlreadyConnected("fresh-plugin", &spec.Plugin{
		Source: src, Providers: []spec.PluginCapability{"verb:neverregistered-idem"},
	}); err != nil || connected {
		t.Fatalf("an unregistered plugin must not be reported connected: connected=%v err=%v", connected, err)
	}
}

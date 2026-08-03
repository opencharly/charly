package main

import (
	"context"
	"strings"

	"github.com/opencharly/spec/spec"
)

// checkrun_test.go — fakeExecutor / fakeResponse, the shared spec.DeployExecutor fixture for
// the compiled-in-kit-candy REGISTRY-DISPATCH integration tests (plugin_relocated_kit_test.go's
// assertRelocatedVerbDispatch + the plugin_*_relocated_test.go family + checkrun_act_test.go):
// these prove providerRegistry.ResolveVerb resolves a compiled-in kit candy's CheckVerbProvider
// and dispatches it via hostVerbResolverFor — charly's OWN registry wiring, not a verb's own
// RunVerb logic (that coverage now lives beside each verb in its own candy/plugin-<verb> module,
// #55 decoupling cone Batch D — see e.g. candy/plugin-file/plugin_test.go).
//
// TestRunner_FileVerb / TestRunner_CommandVerb (the verb-logic tests) relocated to
// candy/plugin-file/plugin_test.go / candy/plugin-command/plugin_test.go.
// TestRunner_VariableExpansion / TestRunner_SkipFlag / TestRunner_EmptyCheck (kit.RunOne's own
// wrapper semantics, "command" used only as an incidental payload) relocated to
// sdk/kit/planrun_dispatch_test.go. newCheckRunner + newFakeRunner (the charly-constructed
// kit.Runner path production no longer uses) are DELETED — every remaining fakeExecutor consumer
// dispatches through hostVerbResolverFor directly, never a kit.Runner.

// fakeExecutor records calls and returns canned results by command-prefix.
// Keeps tests hermetic — no actual podman/docker exec happens.
type fakeExecutor struct {
	responses []fakeResponse
	calls     []string
}

type fakeResponse struct {
	matchPrefix string
	stdout      string
	stderr      string
	exit        int
	err         error
}

// fakeExecutor implements DeployExecutor (post-2026-04 cutover). Only
// RunCapture is exercised by the tests below; the other interface methods
// return zero values so the type still satisfies the interface contract.
func (f *fakeExecutor) RunInteractive(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (f *fakeExecutor) RunStream(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (f *fakeExecutor) RunCapture(ctx context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return r.stdout, r.stderr, r.exit, r.err
		}
	}
	return "", "no fake response registered for: " + cmd, 127, nil
}

func (f *fakeExecutor) Kind() string  { return "fake" }
func (f *fakeExecutor) Venue() string { return "fake" }
func (f *fakeExecutor) ResolveHome(_ context.Context, _ string) (string, error) {
	return "/home/fake", nil
}

// Stubs satisfying the rest of DeployExecutor — never called by these tests.
func (f *fakeExecutor) RunSystem(_ context.Context, _ string, _ spec.EmitOpts) error { return nil }
func (f *fakeExecutor) RunUser(_ context.Context, _ string, _ spec.EmitOpts) error   { return nil }
func (f *fakeExecutor) RunBuilder(_ context.Context, _ spec.BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (f *fakeExecutor) PutFile(_ context.Context, _, _ string, _ uint32, _ bool, _ spec.EmitOpts) error {
	return nil
}
func (f *fakeExecutor) GetFile(_ context.Context, _ string, _ bool, _ spec.EmitOpts) ([]byte, error) {
	return nil, nil
}

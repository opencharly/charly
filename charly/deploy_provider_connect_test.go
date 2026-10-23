package main

// deploy_provider_connect_test.go — the regression controls for opencharly/charly#1038.
//
// THE DEFECT: `ResolveTarget` looked the deploy provider up in the registry with NO lazy connect,
// and the only connect on the deploy-add path was `deployNodePluginContext`'s node-keyed
// canonical-ref auto-inject. A deploy name that was NOT already a node in the merged project tree
// therefore never connected its substrate, and `charly deploy add <new-name>` failed with
// `target "pod" is a known substrate but its deploy provider is not connected` — while
// `charly config` on the SAME binary succeeded, because the lifecycle path called
// `connectPluginByWord(ClassDeployTarget, …)` explicitly.
//
// THE FIX: `ResolveTarget` now calls `ensureDeployProviderConnected` before its registry lookup —
// the ONE connect step every deploy path shares. These tests drive the REAL connect chain from the
// resolution boundary, on both legs it can take:
//   - BAKED: a plugin binary + `<name>.providers` manifest under $CHARLY_PLUGIN_DIR.
//   - PROJECT SCAN: the project's candy closure, reaching the generated provider-ref index.
//
// Every case is a NEW deploy name (`brand-new-…`) that appears nowhere in a project tree, so it is
// exactly the shape that failed pre-fix. `example-deploy` (deploy:exampledeploy) is the existing
// out-of-tree plugin fixture the repo already builds for TestExternalDeployPlugin_ReverseChannelEndToEnd;
// its word is in the generated index, so both legs are real, not stubbed.
//
// Both connect legs build/exec a real plugin, so they are gated behind -short exactly like the
// existing external-plugin e2e suites. The full `go test ./charly/...` gate (CI) runs them.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/opencharly/spec/spec"
)

// closeClosersFrom closes only the plugin connections registered AFTER `before` was sampled (the
// delta), so a test can reap the plugin process it started without disturbing a closer that
// belonged to an earlier test in the same process. Cleanups are LIFO, so the caller registers it
// AFTER snapshotProviderState for this to run FIRST.
func registeredCloserCount() int {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()
	return len(providerRegistry.closers)
}

func closeClosersFrom(before int) error {
	providerRegistry.mu.Lock()
	if before > len(providerRegistry.closers) {
		before = len(providerRegistry.closers)
	}
	extra := append([]io.Closer(nil), providerRegistry.closers[before:]...)
	providerRegistry.mu.Unlock()
	var firstErr error
	for _, c := range extra {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// TestResolveTarget_NewDeployNameConnectsBakedSubstrate is the BAKED leg of #1038: a substrate
// provider shipped as a baked binary must be connected by ResolveTarget for a deploy name that is
// not in any project tree. Pre-fix ResolveTarget's registry lookup missed and returned
// "known substrate but its deploy provider is not connected"; the baked binary was recorded (the
// #831 fix) but never asked to connect.
func TestResolveTarget_NewDeployNameConnectsBakedSubstrate(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + execs the external example-deploy plugin binary (slow); runs in the full gate")
	}
	t.Cleanup(snapshotProviderState())
	before := registeredCloserCount()
	t.Cleanup(func() { _ = closeClosersFrom(before) })

	ctx := context.Background()
	srcDir := pluginModuleDir(t, "example-deploy")
	built := hostBuildPluginForTest(t, ctx, srcDir, "plugin-example-deploy")

	// Arrange the DOCUMENTED baked shape: a binary beside a `<name>.providers` manifest, found via
	// $CHARLY_PLUGIN_DIR with the FHS path excluded (CHARLY_PLUGIN_ONLY=1).
	bakedDir := t.TempDir()
	bin := filepath.Join(bakedDir, "plugin-example-deploy")
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatalf("read built plugin %s: %v", built, err)
	}
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatalf("write baked plugin %s: %v", bin, err)
	}
	if err := os.WriteFile(bin+".providers", []byte("deploy:exampledeploy\n"), 0o644); err != nil {
		t.Fatalf("write providers manifest: %v", err)
	}
	t.Setenv("CHARLY_PLUGIN_DIR", bakedDir)
	t.Setenv("CHARLY_PLUGIN_ONLY", "1")

	key := providerKey(ClassDeployTarget, "exampledeploy", "")
	delete(bakedPluginBinaries, key)
	t.Cleanup(func() { delete(bakedPluginBinaries, key) })
	discoverBakedPluginWords()
	if _, ok := bakedPluginBinaries[key]; !ok {
		t.Fatalf("the baked manifest's deploy:exampledeploy was not recorded — the fixture is not arranged as the documented baked shape")
	}

	target, err := ResolveTarget(&spec.DeployNode{Target: "exampledeploy"}, "brand-new-baked-deploy")
	if err != nil {
		t.Fatalf("ResolveTarget(new name, baked substrate provider) = %v\n"+
			"  This is opencharly/charly#1038: the deploy provider is baked and loadable, but the "+
			"resolution boundary never connected it for a name that is not already a project node.", err)
	}
	if _, ok := target.(*pluginDeployTarget); !ok {
		t.Fatalf("ResolveTarget(new name) routed to %T, want *pluginDeployTarget", target)
	}
}

// TestResolveTarget_NewDeployNameConnectsProjectScanSubstrate is the PROJECT-SCAN leg of #1038: a
// project that vendors the substrate plugin candy (no baked binary) must connect it for a new
// deploy name too. This is the leg the issue measured on opencharly/plugin-pod (`verify-scan-pod`).
func TestResolveTarget_NewDeployNameConnectsProjectScanSubstrate(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + execs the external example-deploy plugin binary (slow); runs in the full gate")
	}
	t.Cleanup(snapshotProviderState())
	before := registeredCloserCount()
	t.Cleanup(func() { _ = closeClosersFrom(before) })

	srcDir := pluginModuleDir(t, "example-deploy")

	// A minimal project that VENDORS the plugin candy under candy/ (no baked binary, so the only
	// possible connect source is the project scan).
	project := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(project, "charly.yml"), []byte("discover:\n    - path: candy\n      recursive: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyDirAll(srcDir, filepath.Join(project, "candy", "plugin-example-deploy")); err != nil {
		t.Fatalf("vendor plugin candy into the fixture project: %v", err)
	}
	isolatePluginSearch(t, "") // no baked hit: $CHARLY_PLUGIN_DIR is an empty dir

	// Prove this is the scan leg: the generated index knows the word (pass 2's ref) and nothing is
	// baked for it.
	if got := canonicalProviderRef(ClassDeployTarget, "exampledeploy", "", ""); got == "" {
		t.Fatal("the generated provider-ref index does not know deploy:exampledeploy — the project-scan fixture cannot exercise #1038")
	}
	if got := bakedPluginBinaries[providerKey(ClassDeployTarget, "exampledeploy", "")]; got != "" {
		t.Fatalf("a baked plugin is recorded at %q; this test must exercise the PROJECT-SCAN leg only", got)
	}

	target, err := ResolveTarget(&spec.DeployNode{Target: "exampledeploy"}, "brand-new-scan-deploy")
	if err != nil {
		t.Fatalf("ResolveTarget(new name, project-scan substrate provider) = %v\n"+
			"  This is opencharly/charly#1038 on the project-scan leg: the candy is vendored and "+
			"buildable, but the resolution boundary never connected it for a new deploy name.", err)
	}
	if _, ok := target.(*pluginDeployTarget); !ok {
		t.Fatalf("ResolveTarget(new name) routed to %T, want *pluginDeployTarget", target)
	}
}

// TestResolveTarget_AlreadyConnectedSubstrateStillResolves is the no-regression control for the
// EXISTING-node path (plugin-pod#26's bed) AND the `config`/lifecycle path: a substrate already in
// the registry must resolve without any connect attempt, and dispatchLifecycleTarget must still
// return a lifecycle target. ensureDeployProviderConnected's registry hit is what keeps the new
// step from turning a working path into a second connect.
func TestResolveTarget_AlreadyConnectedSubstrateStillResolves(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	const word = "examplealreadyconnected"
	gp := &grpcProvider{capMeta: capMeta{class: ClassDeployTarget, word: word}, lifecycle: true}
	if err := providerRegistry.RegisterPluginProviders([]Provider{gp}, "already-connected-test", nil); err != nil {
		t.Fatalf("register fixture provider: %v", err)
	}
	// No baked binary and no project candy: any connect attempt would find nothing, so a
	// regression that drops the registry-hit short-circuit would surface as a resolution error.
	// A minimal root manifest is still required because the lifecycle path loads it first.
	isolatePluginSearch(t, "")
	project := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(project, "charly.yml"), []byte("discover:\n    - path: candy\n      recursive: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "candy"), 0o755); err != nil {
		t.Fatal(err)
	}

	node := &spec.DeployNode{Target: word}
	target, err := ResolveTarget(node, "existing-node")
	if err != nil {
		t.Fatalf("ResolveTarget(existing-connected substrate) = %v — the already-registered path regressed", err)
	}
	if _, ok := target.(*pluginDeployTarget); !ok {
		t.Fatalf("ResolveTarget(existing-connected substrate) routed to %T, want *pluginDeployTarget", target)
	}

	lt, err := dispatchLifecycleTarget("config", &spec.DeployNode{Target: word}, "existing-node")
	if err != nil {
		t.Fatalf("dispatchLifecycleTarget(config) = %v — the config/start/stop path regressed", err)
	}
	if lt == nil {
		t.Fatal("dispatchLifecycleTarget(config) returned a nil LifecycleTarget")
	}
}

// copyDirAll copies a directory tree, used to vendor the fetched plugin candy into the fixture
// project. Files keep their mode (the plugin source may carry shell fixtures).
func copyDirAll(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	// Deterministic walk order keeps the vendored tree identical across runs.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		if e.IsDir() {
			if err := copyDirAll(s, d); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

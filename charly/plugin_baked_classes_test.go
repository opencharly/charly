package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverBakedPluginWordsRecordsEveryClass is the regression guard for
// opencharly/charly#831: a bed could not witness a local DEPLOY-class plugin.
//
// The baked route (`CHARLY_PLUGIN_DIR` + a `<binary>.providers` manifest) is the documented way to
// point charly at a plugin built from a working tree, and it worked for a VERB word while silently
// doing nothing for a `deploy:` word: `discoverBakedPluginWords`'s class switch skipped every class
// other than command/verb with a `default: continue`, and the `continue` fired BEFORE the
// `bakedPluginBinaries` record below it — so the binary was not merely unregistered, it was never
// even recorded, and `connectPluginByWordRef` (which consults that map FIRST, class-agnostically,
// before the project scan) could not answer. An R10 bed then graded the RELEASED plugin while the
// author believed it was grading their branch: `grep -c "<worktree>"` on the bed log returned 0 and
// the log said `Resolved … -> main (default branch)`.
//
// Both halves are pinned here: a deploy word must be RECORDED (and a class that needs no eager
// surface must not gain one), and a command/verb line must keep working exactly as before.
func TestDiscoverBakedPluginWordsRecordsEveryClass(t *testing.T) {
	dir := t.TempDir()
	// Discovery never executes the binary — it records the path beside the manifest — so a plain
	// file is enough to prove the wiring, and no plugin is launched by this test.
	bin := filepath.Join(dir, "plugin-deploy-pod")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".providers", []byte("deploy:pod\nverb:mise\ncommand:settings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHARLY_PLUGIN_DIR", dir)
	// CHARLY_PLUGIN_ONLY drops the FHS path, so the search is exactly this directory: the test
	// asserts what the FIX does, not what an installed package happens to bake on this host.
	t.Setenv("CHARLY_PLUGIN_ONLY", "1")

	deployKey := providerKey(ClassDeployTarget, "pod", "")
	verbKey := providerKey(ClassVerb, "mise", "")
	cmdKey := providerKey(ClassCommand, "settings", "")
	defer func() {
		for _, k := range []string{deployKey, verbKey, cmdKey} {
			delete(bakedPluginBinaries, k)
		}
	}()

	discoverBakedPluginWords()

	// THE FIX: a deploy-class word is recorded, which is what makes connectPluginByWordRef able to
	// serve `deploy:pod` from this binary instead of scanning the project closure.
	if got, ok := bakedPluginBinaries[deployKey]; !ok || got != bin {
		t.Errorf("the baked manifest's `deploy:pod` was not recorded (got %q, present=%v, want %q).\n"+
			"  Without it a bed cannot witness a local deploy-class plugin: the same directory works\n"+
			"  for `verb:` and is silently inert for `deploy:` (opencharly/charly#831).", got, ok, bin)
	}

	// The eager surfaces are unchanged: a verb and a command still register, and the deploy word
	// must NOT have been registered as either (recording is not registering).
	if got, ok := bakedPluginBinaries[verbKey]; !ok || got != bin {
		t.Errorf("the baked manifest's `verb:mise` stopped being recorded (got %q, present=%v)", got, ok)
	}
	if got, ok := bakedPluginBinaries[cmdKey]; !ok || got != bin {
		t.Errorf("the baked manifest's `command:settings` stopped being recorded (got %q, present=%v)", got, ok)
	}
	declaredDeployMu.Lock()
	verbRegistered := declaredExternalVerb["pod"]
	declaredDeployMu.Unlock()
	if verbRegistered {
		t.Error("the deploy word was registered as an external VERB: classes must not gain an eager " +
			"surface they did not have — a `deploy:pod` is resolved on demand, never from the CLI grammar")
	}
}

// TestBakedLookupPrecedesTheProjectScan states the ORDER the fix depends on, without needing a real
// project: connectPluginByWordRef asks the baked map FIRST, so a recorded deploy word answers before
// any closure scan happens. The scan leg is what makes this expensive and network-touching, which is
// precisely why the baked hit must short-circuit it (`/charly-internals:go`).
func TestBakedLookupPrecedesTheProjectScan(t *testing.T) {
	key := providerKey(ClassDeployTarget, "pod", "")
	bakedPluginBinaries[key] = "/nonexistent/plugin-deploy-pod"
	defer delete(bakedPluginBinaries, key)

	// The connect itself will fail (the binary does not exist), and THAT is the assertion: a
	// recorded word is consumed by connectBakedPlugin's own attempt rather than falling through to
	// the project scan — which, in a module with no project config, cannot produce a provider at all.
	if _, ok := connectBakedPlugin(ClassDeployTarget, "pod", ""); ok {
		t.Fatal("connectBakedPlugin reported a provider from a nonexistent binary")
	}
	if _, still := bakedPluginBinaries[key]; !still {
		t.Error("connectBakedPlugin consumed the recorded entry instead of leaving it for the next resolve")
	}
}

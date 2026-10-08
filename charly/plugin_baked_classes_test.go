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

// TestBakedLookupPrecedesTheProjectScan exercises the REAL entry point
// (connectPluginByWordRef) and proves the ORDER it documents: the baked lookup runs BEFORE the
// project scan, so a recorded word answers without any project config at all.
//
// The proof is a side effect, not a claim: the recorded binary is a script that touches a marker and
// exits, and the test runs in a directory with NO charly.yml. connectPluginByWordRef returns at its
// `LoadConfig` failure for a project it cannot read — so a marker that exists when it returns false
// can only have been created by the baked leg, which therefore ran first. (The connect itself fails,
// as it must: a script is not a plugin. That is why the assertion is "was it TRIED, and before the
// scan", not "did it succeed" — a successful connect is what the live acceptance run proves.)
func TestBakedLookupPrecedesTheProjectScan(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "baked-leg-ran")
	bin := filepath.Join(dir, "plugin-deploy-pod")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	key := providerKey(ClassDeployTarget, "pod", "")
	bakedPluginBinaries[key] = bin
	defer delete(bakedPluginBinaries, key)

	// No charly.yml here, so the project-scan leg cannot succeed: every answer this call can give
	// comes from the baked leg.
	dir2 := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir2); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	if _, ok := connectPluginByWordRef(ClassDeployTarget, "pod", "", ""); ok {
		t.Fatal("connectPluginByWordRef reported a provider from a script that is not a plugin")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the baked leg never ran: connectPluginByWordRef consulted the project scan without "+
			"trying the recorded baked binary first (marker %s absent: %v).\n"+
			"  The baked lookup must precede the scan — that order is what makes a local plugin "+
			"reachable on a bed (opencharly/charly#831).", marker, err)
	}
}

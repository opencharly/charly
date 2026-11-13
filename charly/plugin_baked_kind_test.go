package main

import (
	"os"
	"path/filepath"
	"testing"
)

// plugin_baked_kind_test.go — the gate on the KIND half of opencharly/charly#1006.
//
// A plugin's `kind:` word is not merely "resolved on demand" like a `deploy:`/`step:`/`builder:`
// word: the kind word IS an authored entity's discriminator, so the project's own charly.yml cannot
// be PARSED without it (loaderkit's classifyKind reads spec.Threaded.Kinds, which loaderThreaded
// builds from the registered providers plus `declaredKind`).
//
// discoverBakedPluginWords' class switch registered a command's kong word and a verb's lazy hook
// and RECORDED every class's binary — but never called registerDeclaredKind, so a kind baked beside
// the charly binary never reached the parse-time set. Consequences measured on the base tree: a
// valid `pipeline:` entity was rejected with `no kind discriminator — none of its keys ("pipeline")
// is a recognized kind` in every project that did not re-declare the plugin the image already
// serves, and connectDeclaredKindPlugins (the connect pre-pass) never learned the word it must
// connect, so the entity would have decode-skipped even once parseable.
//
// A COMPILED-IN kind reaches the same set through providerRegistry.allProviders(), which is what a
// baked one must be the peer of.
func TestDiscoverBakedPluginWordsThreadsKindWords(t *testing.T) {
	dir := t.TempDir()
	// Discovery never executes the binary, so a plain file proves the wiring.
	bin := filepath.Join(dir, "plugin-zzfront")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The real manifest shape plugin-pipeline ships: a command, a verb and a kind from one plugin.
	if err := os.WriteFile(bin+".providers", []byte("command:zzfront\nverb:zzfront\nkind:zzfront\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHARLY_PLUGIN_DIR", dir)
	// CHARLY_PLUGIN_ONLY drops the FHS path so the search is exactly this directory: the assertion
	// is about what THIS fix does, not about what an installed package happens to bake on this host.
	t.Setenv("CHARLY_PLUGIN_ONLY", "1")

	defer func() {
		declaredDeployMu.Lock()
		delete(declaredKind, "zzfront")
		declaredDeployMu.Unlock()

		for _, k := range []string{
			providerKey(ClassCommand, "zzfront", ""),
			providerKey(ClassVerb, "zzfront", ""),
			providerKey(ClassKind, "zzfront", ""),
		} {
			delete(bakedPluginBinaries, k)
		}
	}()

	discoverBakedPluginWords()

	// THE FIX: the kind word reaches the parse-time set, so an authored `zzfront:` entity parses.
	if !isDeclaredExternalKind("zzfront") {
		t.Fatalf("the baked manifest's `kind:zzfront` never reached `declaredKind`.\n" +
			"  Without it a project authoring a `zzfront:` entity fails to parse with\n" +
			"  `no kind discriminator … not a recognized kind` in EVERY project that does not\n" +
			"  re-declare the plugin the image already serves (opencharly/charly#1006).")
	}
	// …and it is in the DATA snapshot the loader actually hands the parser.
	if !loaderThreaded().Kinds["zzfront"] {
		t.Fatal("kind:zzfront reached declaredKind but not spec.Threaded.Kinds — the parser reads the snapshot, not the map")
	}

	// The pre-existing eager surfaces are unchanged: recording is not registering for the other
	// classes, and neither must a kind word leak into them.
	if _, registered := declaredExternalCommand[providerKey(ClassCommand, "zzfront", "")]; !registered {
		t.Error("the baked `command:zzfront` word stopped being registered in the kong grammar")
	}
	if !isDeclaredExternalVerb("zzfront") {
		t.Error("the baked `verb:zzfront` word stopped being registered")
	}
	declaredDeployMu.RLock()
	leakedDeploy := declaredDeploySubstrate["zzfront"]
	declaredDeployMu.RUnlock()
	if leakedDeploy {
		t.Error("the baked `kind:zzfront` word was registered as a DEPLOY SUBSTRATE — a kind must not gain that surface")
	}
}

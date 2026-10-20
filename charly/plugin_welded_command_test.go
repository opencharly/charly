package main

// plugin_welded_command_test.go — the dev/worktree half of the WELDED command contract.
//
// A welded command word (scripts/host-command-plugins.txt) is served by a plugin every
// native package bakes beside the binary. A package install therefore has the word in its
// CLI grammar; a dev/worktree build has no baked .providers manifest at all, so before
// registerWeldedCommandWords the word entered NO grammar and `charly secrets` died as an
// unknown verb. These tests hold the two halves that make the fix real: the word reaches the
// grammar, and the ref it resolves from is the pinned org candy ref the welded set names.

import (
	"maps"
	"strings"
	"testing"
)

// TestWeldedCommandWordsReachGrammar proves every welded word the corpus serves becomes a
// parseable command, and that the seed cannot double-register a word a COMPILED-IN provider
// already owns (which would make Kong reject the grammar outright).
func TestWeldedCommandWordsReachGrammar(t *testing.T) {
	if len(weldedCommandRefs) == 0 {
		t.Fatal("weldedCommandRefs is empty — the welded index was not generated (regenerate with scripts/bootstrap-charly.sh)")
	}

	// Snapshot the process-global prescan state so this test cannot leak a grammar word into a
	// sibling test. The map is reassigned (never mutated in place by the seed) so restoring the
	// snapshot is enough.
	declaredDeployMu.Lock()
	saved := maps.Clone(declaredExternalCommand)
	declaredDeployMu.Unlock()
	t.Cleanup(func() {
		declaredDeployMu.Lock()
		declaredExternalCommand = saved
		declaredDeployMu.Unlock()
	})

	registerWeldedCommandWords()
	_, _, table := collectExternalCommandPlugins()

	for word := range weldedCommandRefs {
		d, ok := table[word]
		if !ok {
			t.Errorf("welded word %q has no external command dispatch entry — the grammar would reject `charly %s`", word, word)
			continue
		}
		if d.word != word || d.parent != "" {
			t.Errorf("welded word %q dispatch entry = {%q, parent %q}, want a top-level %q", word, d.word, d.parent, word)
		}
		// A welded word a compiled-in provider also serves (clean, vm, doctor, …) must stay
		// owned by that provider: dispatchCommand picks it from the registry first, so the
		// welded source fallback is never reached for it.
		if p, inProc := providerRegistry.resolveCommand(word, ""); inProc {
			if _, grpc := p.(*grpcProvider); !grpc && d.word != word {
				t.Errorf("welded word %q: compiled-in provider does not own the word it dispatches", word)
			}
		}
	}
}

// TestWeldedCommandRefsArePinnedCommandRefs proves the welded index is a projection of the
// welded set's OWN pins and of the plugin manifests' own declarations — never a hand-written
// map: each ref is the "@github.com/opencharly/<name>/candy/<name>:<pin>" form, its pin is
// the one scripts/host-command-plugins.txt carries, and the word it is filed under is the
// command capability that same candy declares in the generated provider-ref index.
func TestWeldedCommandRefsArePinnedCommandRefs(t *testing.T) {
	for word, ref := range weldedCommandRefs {
		bare, tag, cut := strings.Cut(strings.TrimPrefix(ref, "@"), ":")
		if !strings.HasPrefix(ref, "@github.com/opencharly/") || !cut || tag == "" {
			t.Errorf("welded word %q: ref %q is not an @github org candy ref with a pin", word, ref)
			continue
		}
		if got, want := pluginProviderRefs["command:"+word], bare; got != want {
			t.Errorf("welded word %q: ref repo = %q, but the provider index files command:%s under %q", word, want, word, got)
		}
		if !strings.Contains(bare, "/candy/plugin-") {
			t.Errorf("welded word %q: ref %q is not a candy/plugin-* module path", word, bare)
		}
	}
}

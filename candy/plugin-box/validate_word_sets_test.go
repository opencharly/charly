package box

// validate_word_sets_test.go — the PLUGIN half of the "validate-word-sets" seam contract.
//
// The host half (registering the sent capability strings by class, then answering act-capability
// off the provider registry) is gated by TestValidateWordSets_DeclaredExternalRecognition in
// charly/build_emit_test.go. What this file gates is the DERIVATION that feeds it: which words and
// which capability strings the plugin extracts from its OWN resolved-project envelope. That
// derivation is what replaced core's deleted registerExternalVerbsFromCandies + fillValidateWordSets
// project re-scan (K-wave 2 cone R1 unit B), so its filters have to be pinned here.

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestFetchValidateWordSets_ExternalProvidersOnly pins the candy filter: only a candy declaring a
// plugin block with a REAL out-of-tree source contributes capability strings. A `source: builtin`
// candy must not — a builtin registers its providers at init(), so the registry already classifies
// it, and sending it would put a builtin word into the declared-external map that exists precisely
// for words with no provider to resolve.
func TestFetchValidateWordSets_ExternalProvidersOnly(t *testing.T) {
	project := &spec.ResolvedProject{Candies: map[string]spec.CandyView{
		"ext-plugin": {
			Name: "ext-plugin", IsPlugin: true,
			PluginSource:    "github.com/opencharly/charly/candy/ext-plugin",
			PluginProviders: []string{"verb:extverbfromcandy", "step:extstepfromcandy"},
		},
		"builtin-plugin": {
			Name: "builtin-plugin", IsPlugin: true,
			PluginSource:    "builtin",
			PluginProviders: []string{"verb:builtinverbfromcandy"},
		},
		"sourceless-plugin": {
			Name: "sourceless-plugin", IsPlugin: true,
			PluginProviders: []string{"verb:sourcelessverb"},
		},
		"ordinary": {Name: "ordinary"},
	}}

	req := buildValidateWordSetsRequest(project)
	want := map[string]bool{"verb:extverbfromcandy": true, "step:extstepfromcandy": true}
	for _, capability := range req.ExternalProviders {
		if !want[capability] {
			t.Errorf("capability %q must NOT be sent to the host (only a real out-of-tree source contributes)", capability)
		}
		delete(want, capability)
	}
	for capability := range want {
		t.Errorf("capability %q from an out-of-tree plugin candy was not sent", capability)
	}
}

// TestFetchValidateWordSets_PluginWordsFromBothPlans pins the word inventory: every DISTINCT
// `plugin:` verb word reachable from a candy model's plan OR a box plan is sent, and nothing else —
// a non-plugin step contributes no word, and a word appearing in both plans is sent once.
func TestFetchValidateWordSets_PluginWordsFromBothPlans(t *testing.T) {
	pluginStep := func(word string) spec.Step { return spec.Step{Op: spec.Op{Plugin: word}} }
	project := &spec.ResolvedProject{
		CandyModels: map[string]spec.CandyModel{
			"c1": {Name: "c1", Plan: []spec.Step{pluginStep("candyword"), pluginStep("sharedword"), {Op: spec.Op{Command: "true"}}}},
		},
		BoxPlans: map[string][]spec.Step{
			"b1": {pluginStep("boxword"), pluginStep("sharedword")},
		},
	}

	req := buildValidateWordSetsRequest(project)
	counts := map[string]int{}
	for _, w := range req.PluginWords {
		counts[w]++
	}
	for _, w := range []string{"candyword", "boxword", "sharedword"} {
		if counts[w] != 1 {
			t.Errorf("plugin word %q sent %d time(s), want exactly 1; got %v", w, counts[w], req.PluginWords)
		}
	}
	if len(req.PluginWords) != 3 {
		t.Errorf("only `plugin:` words belong in the inventory; got %v", req.PluginWords)
	}
}

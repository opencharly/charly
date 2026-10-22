package main

// engine_word_cutover_test.go — the engine-word cutover (spec#266, charly#1023):
// `containerd` is the ENGINE WORD and `nerdctl` is the CLI BINARY it drives. The
// negative arms here fail on the pre-cutover tree (spec v0.2026294.929, whose
// vocabulary is podman/docker/nerdctl and whose generated refs index serves
// engine:nerdctl); the control arms prove the replacement is ACCEPTED, not merely
// that the old word is gone.

import (
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/opencharly/spec/container"
	"gopkg.in/yaml.v3"
)

// TestEngineVocabularyIsContainerd — the pinned spec's closed engine vocabulary
// carries `containerd`, rejects the retired `nerdctl` as an engine WORD, and maps
// the word to the nerdctl CLI (the binary is legitimate; the word is not).
func TestEngineVocabularyIsContainerd(t *testing.T) {
	names := container.EngineNames()
	if !slices.Contains(names, "containerd") {
		t.Errorf("engine vocabulary %v omits containerd", names)
	}
	if slices.Contains(names, "nerdctl") {
		t.Errorf("engine vocabulary %v still accepts the retired engine word nerdctl", names)
	}
	if !container.IsEngineName("containerd") {
		t.Error("IsEngineName(containerd) = false, want true")
	}
	if container.IsEngineName("nerdctl") {
		t.Error("IsEngineName(nerdctl) = true, want false — nerdctl is the CLI binary, not an engine word")
	}
	if got := container.EngineBinary("containerd"); got != "nerdctl" {
		t.Errorf("EngineBinary(containerd) = %q, want the nerdctl CLI", got)
	}
}

// engineWordAt is one authored `engine:` field found in the project document.
type engineWordAt struct {
	path string
	word string
}

// collectEngineWords walks the parsed project document and records every
// `engine:` field, wherever it is nested.
func collectEngineWords(node any, path string, out *[]engineWordAt) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "engine" {
				if word, ok := child.(string); ok {
					*out = append(*out, engineWordAt{path: path, word: word})
				}
			}
			collectEngineWords(child, path+"/"+key, out)
		}
	case []any:
		for i, child := range v {
			collectEngineWords(child, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// TestProjectDeclaresOnlySpecEngineWords — every authored `engine:` field in the
// repo's charly.yml is either the `auto` selector or a word in the pinned spec's
// closed vocabulary. This is the claim-keyed absence arm: the retired word fails
// even on a spec that still lists it, and a future reintroduction fails here.
func TestProjectDeclaresOnlySpecEngineWords(t *testing.T) {
	data, err := os.ReadFile(rootCharlyYML)
	if err != nil {
		t.Fatalf("read %s: %v", rootCharlyYML, err)
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", rootCharlyYML, err)
	}

	var found []engineWordAt
	collectEngineWords(doc, "", &found)
	if len(found) == 0 {
		t.Fatalf("%s declares no engine: field — the walk never reached the document", rootCharlyYML)
	}
	for _, e := range found {
		if e.word == "auto" {
			continue // a selector, not an engine word
		}
		if e.word == "nerdctl" {
			t.Errorf("%s: engine = %q is the retired engine word (nerdctl is the CLI binary)", e.path, e.word)
			continue
		}
		if !container.IsEngineName(e.word) {
			t.Errorf("%s: engine = %q is not one of %v", e.path, e.word, container.EngineNames())
		}
	}
	t.Logf("checked %d engine: fields in %s", len(found), rootCharlyYML)
}

// TestPluginRefsIndexUsesContainerdWords — the generated word->provider index is
// the boundary where a plugin's provider words reach the host. It must serve the
// new words from the canonical plugin repo and carry none of the retired ones.
func TestPluginRefsIndexUsesContainerdWords(t *testing.T) {
	const wantRef = "github.com/opencharly/plugin-nerdctl/candy/plugin-nerdctl"

	for _, word := range []string{"engine:containerd", "verb:containerd", "command:containerd"} {
		ref, ok := pluginProviderRefs[word]
		if !ok {
			t.Errorf("pluginProviderRefs[%q] is missing — the regenerated index does not serve the containerd word", word)
			continue
		}
		if ref != wantRef {
			t.Errorf("pluginProviderRefs[%q] = %q, want the canonical plugin ref %q", word, ref, wantRef)
		}
	}
	for _, word := range []string{"engine:nerdctl", "verb:nerdctl", "command:nerdctl"} {
		if ref, ok := pluginProviderRefs[word]; ok {
			t.Errorf("pluginProviderRefs[%q] = %q — the retired provider word is still indexed", word, ref)
		}
	}
}

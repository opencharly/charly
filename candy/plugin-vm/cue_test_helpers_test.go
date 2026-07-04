package vm

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeViaCUEForTest is the plugin-local test helper for the relocated VM-rendering tests. In
// production the HOST applies CUE schema defaults (applyCueDefaults) before passing the fully
// resolved VmSpec to the plugin's create op, so the plugin never decodes raw entity YAML at
// runtime; its rendering tests therefore decode the (self-contained) test fixtures directly into
// the target struct via yaml.v3 — there is no CUE engine in the plugin module.
func decodeViaCUEForTest(t *testing.T, body string, out any) error {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return err
	}
	node := &doc
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	return node.Decode(out)
}

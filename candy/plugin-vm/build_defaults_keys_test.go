package vm

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// build_defaults_keys_test.go — the guard that keeps the plugin's embedded vocabulary
// from re-accumulating a copy of charly core's charly.yml.
//
// The file exists only so the out-of-process plugin can resolve OVMF firmware paths
// without reaching core's //go:embed. Its single consumer is
// unmarshalEmbeddedDefaults, wired to the vmshared.UnmarshalEmbeddedDefaults seam and
// called from exactly two places in sdk/vmshared/ovmf_paths.go, which decode exactly
// two keys.
//
// It had grown to 28 top-level keys and 1273 lines — a near-complete copy of
// charly/charly.yml, of which 26 keys were read by nothing. The cost was not the bytes:
// it read as a vocabulary requiring sync with core's, so adding a distro to core looked
// like it needed adding here too, and doing so changed no behaviour at all. This test
// makes that regrowth fail loudly instead of looking like maintenance.

var embeddedTopLevelKey = regexp.MustCompile(`(?m)^([a-z_][a-z0-9_-]*):$`)

func TestEmbeddedDefaultsCarryOnlyTheKeysTheResolverReads(t *testing.T) {
	want := map[string]bool{"ovmf_paths": true, "ovmf_distro_aliases": true}

	var got []string
	for _, m := range embeddedTopLevelKey.FindAllStringSubmatch(string(embeddedCharlyDefaults), -1) {
		got = append(got, m[1])
	}
	if len(got) == 0 {
		t.Fatal("no top-level keys found in the embedded build_defaults.yml — the embed or the pattern is wrong")
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("build_defaults.yml carries top-level key %q, which NOTHING reads.\n"+
				"Its only consumer decodes ovmf_paths and ovmf_distro_aliases (sdk/vmshared/ovmf_paths.go).\n"+
				"Do not mirror charly/charly.yml here: a key added for symmetry changes no behaviour and\n"+
				"creates a false sync obligation. Add it only alongside code that actually decodes it.", k)
		}
	}
	for k := range want {
		if !strings.Contains(string(embeddedCharlyDefaults), k+":") {
			t.Errorf("build_defaults.yml is MISSING %q, which the OVMF resolver requires (it panics without it)", k)
		}
	}
}

// The resolver panics on an empty table, so the embedded YAML must still decode into
// both shapes it expects. This is the presence control for the test above: a file
// trimmed to zero keys would satisfy "no unread keys" perfectly.
func TestEmbeddedDefaultsStillDecodeForTheResolver(t *testing.T) {
	var doc struct {
		OvmfPaths map[string]struct {
			Secure    []map[string]string `yaml:"secure"`
			Nonsecure []map[string]string `yaml:"nonsecure"`
		} `yaml:"ovmf_paths"`
		OvmfDistroAliases map[string]string `yaml:"ovmf_distro_aliases"`
	}
	if err := yaml.Unmarshal(embeddedCharlyDefaults, &doc); err != nil {
		t.Fatalf("embedded build_defaults.yml does not parse: %v", err)
	}
	if len(doc.OvmfPaths) == 0 {
		t.Error("ovmf_paths decoded empty — parseEmbeddedOvmfPaths panics on this")
	}
	if len(doc.OvmfDistroAliases) == 0 {
		t.Error("ovmf_distro_aliases decoded empty — parseEmbeddedOvmfDistroAliases panics on this")
	}
	for fam, fp := range doc.OvmfPaths {
		if len(fp.Secure) == 0 && len(fp.Nonsecure) == 0 {
			t.Errorf("ovmf_paths[%q] has neither secure nor nonsecure candidates", fam)
		}
	}
}

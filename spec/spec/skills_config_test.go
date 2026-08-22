package spec

import (
	"encoding/json"
	"testing"
)

// skills_config_test.go — coverage for the Config.Skills threading (the plugins->candies
// migration): projectConfigCached threads uf.PluginKinds["skill"] (the raw skill-kind bodies)
// into Config.Skills, mirroring the Sidecar precedent — so CollectSkills (deploykit) can project
// the composed candies' skills into the ai.opencharly.skill OCI label.

// TestConfig_SkillsThreading proves the raw skill bodies thread verbatim.
func TestConfig_SkillsThreading(t *testing.T) {
	uf := &UnifiedFile{
		RootDir: "/tmp",
		PluginKinds: map[string]map[string]json.RawMessage{
			"skill": {"postgresql-skill": json.RawMessage(`{"name":"postgresql","family":"charly-infrastructure","owner":"postgresql"}`)},
		},
	}
	cfg := uf.ProjectConfig()
	if cfg == nil {
		t.Fatal("ProjectConfig() = nil")
	}
	raw, ok := cfg.Skills["postgresql-skill"]
	if !ok {
		t.Fatalf("Config.Skills missing the threaded skill body: %+v", cfg.Skills)
	}
	if string(raw) != `{"name":"postgresql","family":"charly-infrastructure","owner":"postgresql"}` {
		t.Fatalf("Config.Skills body not threaded verbatim: %s", raw)
	}
}

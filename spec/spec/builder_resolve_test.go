package spec

import "testing"

// builder_resolve_test.go — coverage for the builder-map value-primitive cluster relocated
// from sdk/buildkit (config_resolve.go + distro_builder_map.go). Proves the moved code LIVES in
// the spec gate (each test would FAIL without it).

// boxMapOf is the test-local Config.Box populator (mirrors sdk/buildkit's test helper of the same
// name — test helpers are package-local, not importable, so each test package redeclares its own).
func boxMapOf(m map[string]BoxConfig) BoxMap {
	out := make(BoxMap, len(m))
	for k, v := range m {
		out[k] = EncodeBox(v)
	}
	return out
}

func TestResolveEffectiveBuilder_Precedence(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{Builder: BuilderMap{"pixi": "default-builder"}},
		Box: boxMapOf(map[string]BoxConfig{
			"default-builder": {Candy: []string{}},
			"custom-builder":  {Candy: []string{}},
		}),
	}
	out := ResolveEffectiveBuilder(cfg, "app", nil, "scratch", true, BuilderMap{"npm": "custom-builder"})
	if out.BuilderFor("pixi") != "default-builder" {
		t.Errorf("pixi = %q, want default-builder (inherited)", out.BuilderFor("pixi"))
	}
	if out.BuilderFor("npm") != "custom-builder" {
		t.Errorf("npm = %q, want custom-builder (per-image override)", out.BuilderFor("npm"))
	}
	// Self-reference filtered.
	self := ResolveEffectiveBuilder(cfg, "default-builder", nil, "scratch", true, nil)
	if self.HasBuilder("pixi") {
		t.Errorf("self-referencing builder should be filtered, got %v", self)
	}
}

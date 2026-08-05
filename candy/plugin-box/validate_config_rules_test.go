package box

// validate_config_rules_test.go — tests relocated VERBATIM from charly core alongside their
// subjects (K3-W2, task #13, "tests move with subjects"): TestBoxBaseFromXOR_RejectsConflict (was
// charly/cue_entity_xor_test.go), TestValidateBuildTunables (was charly/validate_test.go), and the
// HOST-NATURAL rule block's validateBuildAndDistro/validateBuilderRefs cases (was the tail of
// charly/validate_fixture_test.go). boxMapOf/testDistroConfig/testBuilderCfg are minimal
// plugin-local re-derivations of the charly-core test helpers of the same name (a small fixture
// duplicated across the module boundary is fine, R3 — the two modules cannot share test-only
// helpers, same class as findSimilarName/levenshteinDistance in validate.go) — covering exactly the
// vocabulary these tests exercise (pac/rpm/zypper formats, pixi/npm/cargo/aur builders), not the
// full charly/testdata/build.yml fixture the core helpers loaded.

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// boxMapOf folds typed BoxConfig test literals into the generic image map (spec.BoxMap), mirroring
// charly/node_loader_test.go's helper of the same name.
func boxMapOf(m map[string]spec.BoxConfig) spec.BoxMap {
	out := make(spec.BoxMap, len(m))
	for k, v := range m {
		out[k] = spec.EncodeBox(v)
	}
	return out
}

// testDistroConfig returns a minimal DistroConfig covering exactly the format vocabulary
// validateBuildAndDistro's moved test cases exercise: "pac" valid, everything else ("invalid",
// "zypper", "rpm") not.
func testDistroConfig() *spec.DistroConfig {
	return &spec.DistroConfig{
		Distro: map[string]*spec.ResolvedDistro{
			"arch": {Format: map[string]*spec.Format{"pac": {}}},
		},
	}
}

// testBuilderCfg returns a minimal BuilderConfig covering exactly the builder-type vocabulary
// validateBuilderRefs's moved test cases exercise.
func testBuilderCfg() *spec.BuilderConfig {
	return &spec.BuilderConfig{
		Builder: map[string]*spec.Builder{
			"pixi": {}, "npm": {}, "cargo": {}, "aur": {},
		},
	}
}

// TestBoxBaseFromXOR_RejectsConflict proves a box authoring BOTH base: and from:
// is rejected (the former `#Box & ({from?: _|_} | {base?: _|_})` disjunction),
// while base-only, from-only, and NEITHER (a scratch box — the disjunction's
// "at most one" semantics) are all accepted.
func TestBoxBaseFromXOR_RejectsConflict(t *testing.T) {
	cases := []struct {
		name   string
		box    spec.BoxConfig
		reject bool
	}{
		{"base+from conflict", spec.BoxConfig{Base: "fedora", From: "builder:scratch-builder"}, true},
		{"base only", spec.BoxConfig{Base: "fedora"}, false},
		{"from only", spec.BoxConfig{From: "builder:scratch-builder"}, false},
		{"neither (scratch box)", spec.BoxConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Unit: the shared predicate (one rule, two seams — R3).
			if got := tc.box.HasBaseFromConflict(); got != tc.reject {
				t.Fatalf("HasBaseFromConflict()=%v, want %v", got, tc.reject)
			}
			// Integration: the validate-time surface that collects the error.
			cfg := &spec.Config{Box: boxMapOf(map[string]spec.BoxConfig{"b": tc.box})}
			errs := &spec.ValidationError{}
			validateBoxBaseFrom(cfg, spec.ResolveOpts{}, errs)
			if tc.reject && !errs.HasErrors() {
				t.Errorf("validateBoxBaseFrom accepted a base+from box (should reject)")
			}
			if !tc.reject && errs.HasErrors() {
				t.Errorf("validateBoxBaseFrom rejected a valid box: %v", errs.Error())
			}
		})
	}
}

// TestValidateBuildTunables calls validateBuildTunables directly (defaults + per-box
// jobs/podman_jobs/cache/keep_* range checks — raw config a projection does not carry).
func TestValidateBuildTunables(t *testing.T) {
	cases := []struct {
		name    string
		ic      spec.BoxConfig
		wantErr string // substring; "" = expect no error
	}{
		{"all unset is valid", spec.BoxConfig{}, ""},
		{"valid full set", spec.BoxConfig{Jobs: new(4), PodmanJobs: new(0), PodmanJobsCap: new(8), Cache: "image", ContextIgnore: []string{"image", ".check"}}, ""},
		{"jobs zero rejected", spec.BoxConfig{Jobs: new(0)}, "jobs must be >= 1"},
		{"jobs negative rejected", spec.BoxConfig{Jobs: new(-2)}, "jobs must be >= 1"},
		{"podman_jobs negative rejected", spec.BoxConfig{PodmanJobs: new(-1)}, "podman_jobs must be >= 0"},
		{"podman_jobs zero allowed (auto)", spec.BoxConfig{PodmanJobs: new(0)}, ""},
		{"podman_jobs_cap zero rejected", spec.BoxConfig{PodmanJobsCap: new(0)}, "podman_jobs_cap must be >= 1"},
		{"bad cache mode rejected", spec.BoxConfig{Cache: "bogus"}, "cache must be one of"},
		{"cache none allowed", spec.BoxConfig{Cache: "none"}, ""},
		{"empty context_ignore entry rejected", spec.BoxConfig{ContextIgnore: []string{"image", "  "}}, "context_ignore[1] must not be empty"},
		{"keep_images zero allowed (disabled)", spec.BoxConfig{KeepImages: new(0)}, ""},
		{"keep_images negative rejected", spec.BoxConfig{KeepImages: new(-1)}, "keep_images must be >= 0"},
		{"keep_check_runs valid", spec.BoxConfig{KeepCheckRuns: new(10)}, ""},
		{"keep_check_runs negative rejected", spec.BoxConfig{KeepCheckRuns: new(-3)}, "keep_check_runs must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &spec.Config{Defaults: tc.ic, Box: boxMapOf(map[string]spec.BoxConfig{})}
			errs := &spec.ValidationError{}
			validateBuildTunables(cfg, errs)
			if tc.wantErr == "" {
				if errs.HasErrors() {
					t.Errorf("expected no error, got: %v", errs.Errors)
				}
				return
			}
			if !errs.HasErrors() {
				t.Fatalf("expected error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(strings.Join(errs.Errors, "\n"), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, errs.Errors)
			}
		})
	}
}

// TestValidateBuildAndDistro_InvalidPkg. A build format not in the vocabulary.
func TestValidateBuildAndDistro_InvalidPkg(t *testing.T) {
	cfg := &spec.Config{Defaults: spec.BoxConfig{Build: []string{"invalid"}}, Box: boxMapOf(map[string]spec.BoxConfig{})}
	errs := &spec.ValidationError{}
	validateBuildAndDistro(cfg, testDistroConfig(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "is not valid") {
		t.Errorf("want 'is not valid', got: %v", errs.Errors)
	}
}

// TestValidateBuildAndDistro_InvalidPkgValue.
func TestValidateBuildAndDistro_InvalidPkgValue(t *testing.T) {
	cfg := &spec.Config{Defaults: spec.BoxConfig{Build: []string{"zypper"}}, Box: boxMapOf(map[string]spec.BoxConfig{})}
	errs := &spec.ValidationError{}
	validateBuildAndDistro(cfg, testDistroConfig(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "is not valid") {
		t.Errorf("want 'is not valid', got: %v", errs.Errors)
	}
}

// TestValidateBuildAndDistro_PacValid. `pac` is a valid vocabulary format.
func TestValidateBuildAndDistro_PacValid(t *testing.T) {
	cfg := &spec.Config{Defaults: spec.BoxConfig{Build: []string{"pac"}}, Box: boxMapOf(map[string]spec.BoxConfig{})}
	errs := &spec.ValidationError{}
	validateBuildAndDistro(cfg, testDistroConfig(), errs)
	if errs.HasErrors() {
		t.Errorf("pac should be valid, got: %v", errs.Errors)
	}
}

// TestValidateBuilderRefs_SelfBuilder. A per-image builder referencing self.
func TestValidateBuilderRefs_SelfBuilder(t *testing.T) {
	cfg := &spec.Config{
		Defaults: spec.BoxConfig{Build: []string{"rpm"}},
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myimg": {Candy: []string{"pixi"}, Builder: spec.BuilderMap{"pixi": "myimg"}},
		}),
	}
	errs := &spec.ValidationError{}
	validateBuilderRefs(cfg, testBuilderCfg(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "cannot reference self") {
		t.Errorf("want 'cannot reference self', got: %v", errs.Errors)
	}
}

// TestValidateBuilderRefs_InheritedSelfNotError. A builder image inheriting defaults.builder that
// points to itself is NOT an error.
func TestValidateBuilderRefs_InheritedSelfNotError(t *testing.T) {
	cfg := &spec.Config{
		Defaults: spec.BoxConfig{Build: []string{"rpm"}, Builder: spec.BuilderMap{"pixi": "builder", "npm": "builder"}},
		Box: boxMapOf(map[string]spec.BoxConfig{
			"builder": {Candy: []string{"pixi"}},
		}),
	}
	errs := &spec.ValidationError{}
	validateBuilderRefs(cfg, testBuilderCfg(), errs)
	if errs.HasErrors() {
		t.Errorf("inherited self-builder should not error, got: %v", errs.Errors)
	}
}

// TestValidateBuilderRefs_PerImageNotFound.
func TestValidateBuilderRefs_PerImageNotFound(t *testing.T) {
	cfg := &spec.Config{
		Defaults: spec.BoxConfig{Build: []string{"rpm"}},
		Box: boxMapOf(map[string]spec.BoxConfig{
			"app": {Candy: []string{"pixi"}, Builder: spec.BuilderMap{"pixi": "nonexistent"}},
		}),
	}
	errs := &spec.ValidationError{}
	validateBuilderRefs(cfg, testBuilderCfg(), errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "is not found") {
		t.Errorf("want 'is not found', got: %v", errs.Errors)
	}
}

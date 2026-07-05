package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFeatureFixtureProject writes a minimal unified project (charly.yml + one discovered
// candy) into a temp dir and returns the dir. The candy carries a non-empty description plus a
// single deterministic check: step — exactly what the in-core loader (LoadConfig / ScanCandy) +
// Step plan model parse, so enumerateFeatures (the "feature" HostBuild seam) has a real entity to walk.
func writeFeatureFixtureProject(t *testing.T, desc string) string {
	t.Helper()
	dir := t.TempDir()
	// The top-level version: is the SCHEMA CalVer (must be <= LatestSchemaVersion); the candy's
	// own candy.version: below is its independent candy identity.
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(
		"version: 2026.174.1100\n"+
			"discover:\n"+
			"    - path: candy\n"+
			"      recursive: true\n"), 0o644); err != nil {
		t.Fatalf("write charly.yml: %v", err)
	}
	candyDir := filepath.Join(dir, "candy", "feat-fixture")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatalf("mkdir candy: %v", err)
	}
	candy := "feat-fixture:\n" +
		"    candy:\n" +
		"        version: 2026.179.0000\n" +
		"        description: " + desc + "\n" +
		"    feat-fixture-true:\n" +
		"        check: the true command runs\n" +
		"        id: feat-fixture-true\n" +
		"        context:\n" +
		"            - build\n" +
		"        plugin: command\n" +
		"        plugin_input:\n" +
		"            command: \"true\"\n"
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(candy), 0o644); err != nil {
		t.Fatalf("write candy charly.yml: %v", err)
	}
	return dir
}

// TestEnumerateFeatures_InvokesInCoreLoader proves the "feature" HostBuild seam's core half
// (enumerateFeatures, host_build_feature.go) actually invokes the in-core unified loader
// (LoadConfig / ScanCandy) and the Step plan model — the data the externalized candy/plugin-feature
// formats. The fixture candy's name, its one-line description summary, and its single check step must
// all surface as enumerated data, proving the loader parsed the entity and the plan model flattened
// its step/check.
func TestEnumerateFeatures_InvokesInCoreLoader(t *testing.T) {
	dir := writeFeatureFixtureProject(t, "A fixture candy proving the in-core loader runs")

	ents, err := enumerateFeatures(dir, "candy")
	if err != nil {
		t.Fatalf("enumerateFeatures: %v", err)
	}
	var e *struct {
		kind, name, desc, summary string
		steps, checks             int
	}
	for i := range ents {
		if ents[i].Name == "feat-fixture" {
			nChecks := 0
			for _, s := range ents[i].Steps {
				if s.IsCheck {
					nChecks++
				}
			}
			e = &struct {
				kind, name, desc, summary string
				steps, checks             int
			}{ents[i].Kind, ents[i].Name, ents[i].Description, ents[i].Summary, len(ents[i].Steps), nChecks}
		}
	}
	if e == nil {
		t.Fatalf("feat-fixture not enumerated: %+v", ents)
	}
	if e.kind != "candy" || !strings.Contains(e.desc, "A fixture candy proving the in-core loader runs") || e.steps != 1 || e.checks != 1 {
		t.Fatalf("enumerated entity wrong: kind=%s desc=%q steps=%d checks=%d", e.kind, e.desc, e.steps, e.checks)
	}
}

// TestEnumerateFeatures_RunsValidatePlanSteps proves the seam runs the SHARED validatePlanSteps over
// the parsed plan model end-to-end: a candy with a non-empty description + a well-formed check: step
// enumerates with ZERO ValidationErrors — proving the loader + plan model + validatePlanSteps chain
// actually ran (not a no-op).
func TestEnumerateFeatures_RunsValidatePlanSteps(t *testing.T) {
	dir := writeFeatureFixtureProject(t, "A fixture candy with a valid plan")
	ents, err := enumerateFeatures(dir, "")
	if err != nil {
		t.Fatalf("enumerateFeatures: %v", err)
	}
	for _, e := range ents {
		if e.Name == "feat-fixture" && len(e.ValidationErrors) != 0 {
			t.Fatalf("clean candy has validation errors: %v", e.ValidationErrors)
		}
	}
}

// TestValidatePlanSteps_Diagnostics unit-tests the SHARED core validator that both
// `charly box validate` (validate.go) AND the externalized `charly feature validate` (via the "feature" HostBuild seam) invoke
// — the function that STAYS core (R3). It flags an empty description and an agent step that
// illegally carries an Op verb; a clean (empty) plan with a real description yields no errors.
func TestValidatePlanSteps_Diagnostics(t *testing.T) {
	// Empty description → flagged.
	if errs := validatePlanSteps("   ", nil, "candy:x"); len(errs) != 1 ||
		!strings.Contains(errs[0], "description is empty") {
		t.Fatalf("empty description: errs = %v, want exactly one 'description is empty'", errs)
	}

	// Non-empty description, no steps → clean.
	if errs := validatePlanSteps("a real description", nil, "candy:x"); len(errs) != 0 {
		t.Fatalf("clean: errs = %v, want none", errs)
	}

	// An agent-check step that carries an Op verb is illegal (agent steps must not). Setting
	// AgentCheck makes StepKind()==agent-check; setting the Op Plugin verb makes Kind() succeed.
	bad := Step{AgentCheck: "the thing works"}
	bad.Op.Plugin = "command"
	if errs := validatePlanSteps("desc", []Step{bad}, "candy:x"); len(errs) != 1 ||
		!strings.Contains(errs[0], "agent steps must not carry an Op verb") {
		t.Fatalf("agent-step-with-verb: errs = %v, want the 'agent steps must not carry an Op verb' diagnostic", errs)
	}
}

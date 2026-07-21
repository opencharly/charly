package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
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
		"version: 2026.202.0105\n"+
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
		"        plan:\n" +
		"            - check: the true command runs\n" +
		"              id: feat-fixture-true\n" +
		"              context:\n" +
		"                  - build\n" +
		"              command: \"true\"\n"
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(candy), 0o644); err != nil {
		t.Fatalf("write candy charly.yml: %v", err)
	}
	return dir
}

// TestEnumerateFeatures_InvokesInCoreLoader proves the "feature" HostBuild seam's core half
// (enumerateFeatures, host_build_feature.go) actually invokes the in-core unified loader
// (LoadConfig / ScanCandy) and returns the Step plan model RAW (K3 moved the summary/keyword/check
// transform to candy/plugin-feature) — the fixture candy's name, description, and its single check
// step must all surface as enumerated RAW data, proving the loader parsed the entity and the plan
// model carried its step/check through untransformed.
func TestEnumerateFeatures_InvokesInCoreLoader(t *testing.T) {
	dir := writeFeatureFixtureProject(t, "A fixture candy proving the in-core loader runs")

	ents, err := enumerateFeatures(dir, "candy")
	if err != nil {
		t.Fatalf("enumerateFeatures: %v", err)
	}
	var e *spec.FeatureEntity
	for i := range ents {
		if ents[i].Name == "feat-fixture" {
			e = &ents[i]
		}
	}
	if e == nil {
		t.Fatalf("feat-fixture not enumerated: %+v", ents)
	}
	nChecks := 0
	for _, s := range e.Plan {
		if s.Check != "" || s.AgentCheck != "" {
			nChecks++
		}
	}
	if e.Kind != "candy" || !strings.Contains(e.Description, "A fixture candy proving the in-core loader runs") || len(e.Plan) != 1 || nChecks != 1 {
		t.Fatalf("enumerated entity wrong: kind=%s desc=%q plan=%d checks=%d", e.Kind, e.Description, len(e.Plan), nChecks)
	}
}

// TestEnumerateFeatures_RunsValidatePlanSteps proves the seam's RAW plan + description survive
// enumeration well-formed enough that the SHARED kit.ValidatePlanSteps (now run in candy/plugin-feature)
// would find zero errors — i.e. the loader + plan model chain produced a clean plan (not a no-op or a
// corrupted transform).
func TestEnumerateFeatures_RunsValidatePlanSteps(t *testing.T) {
	dir := writeFeatureFixtureProject(t, "A fixture candy with a valid plan")
	ents, err := enumerateFeatures(dir, "")
	if err != nil {
		t.Fatalf("enumerateFeatures: %v", err)
	}
	for _, e := range ents {
		if e.Name == "feat-fixture" {
			if errs := kit.ValidatePlanSteps(e.Description, e.Plan, e.Kind+":"+e.Name); len(errs) != 0 {
				t.Fatalf("clean candy has validation errors: %v", errs)
			}
		}
	}
}

// TestValidatePlanSteps_Diagnostics unit-tests the SHARED kit.ValidatePlanSteps validator that
// both `charly box validate` (validate.go) AND the externalized `charly feature validate` (via
// the "feature" HostBuild seam) invoke — P12a relocated it to sdk/kit (R3, one copy reachable
// from both call sites without a core→plugin import). It flags an empty description and an
// agent step that illegally carries an Op verb; a clean (empty) plan with a real description
// yields no errors.
func TestValidatePlanSteps_Diagnostics(t *testing.T) {
	// Empty description → flagged.
	if errs := kit.ValidatePlanSteps("   ", nil, "candy:x"); len(errs) != 1 ||
		!strings.Contains(errs[0], "description is empty") {
		t.Fatalf("empty description: errs = %v, want exactly one 'description is empty'", errs)
	}

	// Non-empty description, no steps → clean.
	if errs := kit.ValidatePlanSteps("a real description", nil, "candy:x"); len(errs) != 0 {
		t.Fatalf("clean: errs = %v, want none", errs)
	}

	// An agent-check step that carries an Op verb is illegal (agent steps must not). Setting
	// AgentCheck makes StepKind()==agent-check; setting the Op Plugin verb makes Kind() succeed.
	bad := spec.Step{AgentCheck: "the thing works"}
	bad.Plugin = "command"
	if errs := kit.ValidatePlanSteps("desc", []spec.Step{bad}, "candy:x"); len(errs) != 1 ||
		!strings.Contains(errs[0], "agent steps must not carry an Op verb") {
		t.Fatalf("agent-step-with-verb: errs = %v, want the 'agent steps must not carry an Op verb' diagnostic", errs)
	}
}

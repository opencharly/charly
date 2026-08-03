package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// testValidatePlanSteps is a thin local port of sdk/kit.ValidatePlanSteps (planvalidate.go) —
// small, pure, and already directly unit-tested in sdk/kit/planvalidate_test.go
// (TestValidatePlanSteps_Diagnostics, per this file's own note above). This file's OWN test
// asserts that the REAL in-core loader's produced plan model passes a clean validator run — the
// validator's own correctness is not what's under test here.
func testValidatePlanSteps(desc string, plan []spec.Step, eid string) []string {
	var errs []string
	if strings.TrimSpace(desc) == "" {
		errs = append(errs, fmt.Sprintf("%s: description is empty", eid))
	}
	for i := range plan {
		step := plan[i]
		kw, err := step.StepKind()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: step %d: %v", eid, i, err))
			continue
		}
		switch kw {
		case spec.StepKeyword("run"), spec.StepKeyword("check"):
			if _, verbErr := step.Kind(); verbErr != nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): %v", eid, i, kw, verbErr))
			}
		case spec.StepKeyword("agent-run"), spec.StepKeyword("agent-check"):
			if _, verbErr := step.Kind(); verbErr == nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): agent steps must not carry an Op verb", eid, i, kw))
			}
		}
	}
	return errs
}

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
		"version: 2026.204.1223\n"+
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
			if errs := testValidatePlanSteps(e.Description, e.Plan, e.Kind+":"+e.Name); len(errs) != 0 {
				t.Fatalf("clean candy has validation errors: %v", errs)
			}
		}
	}
}

// TestValidatePlanSteps_Diagnostics relocated to sdk/kit/planvalidate_test.go
// (K3 cone2 test closure): a pure kit.ValidatePlanSteps unit test with zero
// charly-core dependency.

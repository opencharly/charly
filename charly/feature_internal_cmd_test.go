package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFeatureFixtureProject writes a minimal unified project (charly.yml + one discovered
// candy) into a temp dir and returns the dir. The candy carries a non-empty description plus a
// single deterministic check: step — exactly what the in-core loader (LoadConfig / ScanCandy) +
// Step plan model parse, so the hidden `__feature-*` render helpers have a real entity to walk.
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

// TestRenderFeatureList_InvokesInCoreLoader proves the hidden `charly __feature-list` path
// (FeatureListInternalCmd.Run → renderFeatureList) actually invokes the in-core unified loader
// (LoadConfig / ScanCandy) and the Step plan model — the seam the externalized
// candy/plugin-feature shells back to. The fixture candy's name, its one-line description
// summary, and its single check step must all surface in the rendered output, proving the
// loader parsed the entity and the plan model counted its step/check.
func TestRenderFeatureList_InvokesInCoreLoader(t *testing.T) {
	dir := writeFeatureFixtureProject(t, "A fixture candy proving the in-core loader runs")

	var buf bytes.Buffer
	if err := renderFeatureList(dir, "candy", &buf); err != nil {
		t.Fatalf("renderFeatureList: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"candy feat-fixture:",
		"A fixture candy proving the in-core loader runs",
		"1 step",
		"1 check",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderFeatureList output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderFeatureValidate_InvokesValidatePlanSteps proves the hidden `charly __feature-validate`
// path (FeatureValidateInternalCmd.Run → renderFeatureValidate) loads the real project (LoadConfig
// / ScanCandy) and runs the SHARED validatePlanSteps over the parsed plan model end-to-end: a
// candy with a non-empty description + a well-formed check: step validates clean — the success
// line is printed ONLY after the validate loop walks every loaded entity, so its presence proves
// the loader + plan model + validatePlanSteps chain actually ran (not a no-op).
func TestRenderFeatureValidate_InvokesValidatePlanSteps(t *testing.T) {
	dir := writeFeatureFixtureProject(t, "A fixture candy with a valid plan")
	var out, errOut bytes.Buffer
	if err := renderFeatureValidate(dir, "", &out, &errOut); err != nil {
		t.Fatalf("renderFeatureValidate (clean): unexpected error %v (stderr: %s)", err, errOut.String())
	}
	if got := out.String(); !strings.Contains(got, "All plan blocks validated successfully.") {
		t.Fatalf("clean validate = %q, want the success line", got)
	}
}

// TestValidatePlanSteps_Diagnostics unit-tests the SHARED core validator that both
// `charly box validate` (validate.go) AND the hidden `charly __feature-validate` command invoke
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

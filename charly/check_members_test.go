package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// TestSplitHostKey / TestCollectHostRefs relocated to sdk/kit/hostrefs_test.go
// (P12a follow-up: splitHostKey/collectHostRefs moved to kit.SplitHostKey/
// kit.CollectHostRefs).

// TestEffectiveEnv_HostVarsOverlay: ${HOST:…} addresses overlay onto the active
// env in kit.Runner.EffectiveEnv — the single injection point that makes
// cross-member addressing work for the primary AND on:-swapped venues.
func TestEffectiveEnv_HostVarsOverlay(t *testing.T) {
	base := map[string]string{"USER": "user"}
	kr := kit.NewRunner(kit.RunnerConfig{
		Env:      base,
		HostVars: map[string]string{"HOST:web": "charly-web"},
	})
	env := kr.EffectiveEnv()
	if env["USER"] != "user" {
		t.Errorf("base var lost: %v", env)
	}
	if env["HOST:web"] != "charly-web" {
		t.Errorf("host var not overlaid: %v", env)
	}
	// The base env map must stay clean (copy-on-overlay).
	if _, leaked := base["HOST:web"]; leaked {
		t.Errorf("EffectiveEnv mutated the shared base Env")
	}
}

// TestEffectiveEnv_NoHostVarsReturnsBase: with no HostVars and no Scenario,
// EffectiveEnv returns the base map directly (behaviour unchanged).
func TestEffectiveEnv_NoHostVarsReturnsBase(t *testing.T) {
	base := map[string]string{"USER": "user"}
	kr := kit.NewRunner(kit.RunnerConfig{Env: base})
	if got := kr.EffectiveEnv(); !reflect.DeepEqual(got, base) {
		t.Errorf("EffectiveEnv = %v, want the base map %v", got, base)
	}
}

// TestIsRuntimeOnlyVar_Host: the cross-member ${HOST:…} var is runtime-only, so a
// build-scope check can't reference it.
func TestIsRuntimeOnlyVar_Host(t *testing.T) {
	for _, key := range []string{"HOST:web", "HOST:web:8080"} {
		if !kit.IsRuntimeOnlyVar(key) {
			t.Errorf("%q should be runtime-only", key)
		}
	}
}

// TestFilterHostVars: only ${HOST:…} keys are selected — the ones whose
// unresolution must FAIL (not skip) a check. ${HOST_PORT} (a distinct var) is NOT.
func TestFilterHostVars(t *testing.T) {
	got := kit.FilterHostVars([]string{"HOST:web:8080", "HOST_PORT:8080", "HOST:web", "USER"})
	want := []string{"HOST:web:8080", "HOST:web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterHostVars = %v, want %v", got, want)
	}
	if got := kit.FilterHostVars([]string{"HOST_PORT:8080", "USER"}); len(got) != 0 {
		t.Errorf("filterHostVars with no host vars = %v, want empty", got)
	}
}

// TestRunOne_UnresolvedHostVarFails: an unresolvable ${HOST:…} (member
// unreachable) FAILS the check — a SKIP there would be a fake pass on an
// unreachable dependency. A non-host unresolved var stays a legitimate SKIP.
func TestRunOne_UnresolvedHostVarFails(t *testing.T) {
	r := newCheckRunner(kit.RunnerConfig{Env: map[string]string{}})
	// ${HOST:absent:80} can't be resolved → the cross-member probe's premise
	// failed → FAIL (never reaches the curl; returns at the var-resolution gate).
	// The per-check walk is kit.RunOne now (planrun.go); kit.Runner drives it
	// directly (it implements kit.PlanContext), so a one-op Run exercises the same
	// var-resolution gate.
	hostCheck := cmdOpP("curl -fsS http://${HOST:absent:80}/")
	if res := r.Run(context.Background(), []spec.Op{*hostCheck})[0]; res.Status != TestFail {
		t.Errorf("unresolved ${HOST:…} → status %v (%q), want TestFail", res.Status, res.Message)
	}
	// A non-host unresolved var is a legitimate SKIP (input genuinely N/A here).
	otherCheck := cmdOpP("echo ${SOME_UNSET_VAR}")
	if res := r.Run(context.Background(), []spec.Op{*otherCheck})[0]; res.Status != TestSkip {
		t.Errorf("unresolved non-host var → status %v (%q), want TestSkip", res.Status, res.Message)
	}
}

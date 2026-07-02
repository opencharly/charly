package main

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestSplitHostKey(t *testing.T) {
	cases := []struct {
		key, name, arg string
		ok             bool
	}{
		{"HOST:web", "HOST", "web", true},
		{"HOST:web:8080", "HOST", "web:8080", true},
		{"HOST", "HOST", "", false},
	}
	for _, c := range cases {
		name, arg, ok := splitHostKey(c.key)
		if name != c.name || arg != c.arg || ok != c.ok {
			t.Errorf("splitHostKey(%q) = (%q,%q,%v), want (%q,%q,%v)", c.key, name, arg, ok, c.name, c.arg, c.ok)
		}
	}
}

// TestCollectHostRefs scans every check string field for ${HOST:…} refs and
// returns exactly those (not other parameterized vars like ${HOST_PORT}).
func TestCollectHostRefs(t *testing.T) {
	checks := []Op{
		{Cdp: "open", URL: "http://${HOST:web}:8080"},
		{Plugin: "command", PluginInput: map[string]any{"command": "curl http://${HOST:web:8080}/health"}},
		// addr/http are plugin verbs now — their refs live in plugin_input (collectHostRefs
		// scans it via collectAnyStrings). The addr HOST_PORT is NOT a cross-member ref; the
		// http ${HOST:web} is a duplicate of the cdp one.
		{Plugin: "addr", PluginInput: map[string]any{"addr": "127.0.0.1:${HOST_PORT:8080}"}},
		{Plugin: "http", PluginInput: map[string]any{"http": "http://${HOST:web}/"}},
	}
	got := collectHostRefs(checks)
	sort.Strings(got)
	want := []string{"HOST:web", "HOST:web:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectHostRefs = %v, want %v", got, want)
	}
}

// TestEffectiveEnv_HostVarsOverlay: ${HOST:…} addresses overlay onto the active
// resolver's env in effectiveEnv — the single injection point that makes
// cross-member addressing work for the primary AND on:-swapped resolvers.
func TestEffectiveEnv_HostVarsOverlay(t *testing.T) {
	r := &Runner{
		Resolver: &CheckVarResolver{Env: map[string]string{"USER": "user"}},
		HostVars: map[string]string{"HOST:web": "charly-web"},
	}
	env := r.effectiveEnv()
	if env["USER"] != "user" {
		t.Errorf("base resolver var lost: %v", env)
	}
	if env["HOST:web"] != "charly-web" {
		t.Errorf("host var not overlaid: %v", env)
	}
	// The base resolver's own map must stay clean (copy-on-overlay).
	if _, leaked := r.Resolver.Env["HOST:web"]; leaked {
		t.Errorf("effectiveEnv mutated the shared resolver Env")
	}
}

// TestEffectiveEnv_NoHostVarsReturnsBase: with no HostVars and no Scenario,
// effectiveEnv returns the resolver's map directly (behaviour unchanged).
func TestEffectiveEnv_NoHostVarsReturnsBase(t *testing.T) {
	base := map[string]string{"USER": "user"}
	r := &Runner{Resolver: &CheckVarResolver{Env: base}}
	if got := r.effectiveEnv(); !reflect.DeepEqual(got, base) {
		t.Errorf("effectiveEnv = %v, want the base map %v", got, base)
	}
}

// TestIsRuntimeOnlyVar_Host: the cross-member ${HOST:…} var is runtime-only, so a
// build-scope check can't reference it.
func TestIsRuntimeOnlyVar_Host(t *testing.T) {
	for _, key := range []string{"HOST:web", "HOST:web:8080"} {
		if !IsRuntimeOnlyVar(key) {
			t.Errorf("%q should be runtime-only", key)
		}
	}
}

// TestFilterHostVars: only ${HOST:…} keys are selected — the ones whose
// unresolution must FAIL (not skip) a check. ${HOST_PORT} (a distinct var) is NOT.
func TestFilterHostVars(t *testing.T) {
	got := filterHostVars([]string{"HOST:web:8080", "HOST_PORT:8080", "HOST:web", "USER"})
	want := []string{"HOST:web:8080", "HOST:web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterHostVars = %v, want %v", got, want)
	}
	if got := filterHostVars([]string{"HOST_PORT:8080", "USER"}); len(got) != 0 {
		t.Errorf("filterHostVars with no host vars = %v, want empty", got)
	}
}

// TestRunOne_UnresolvedHostVarFails: an unresolvable ${HOST:…} (member
// unreachable) FAILS the check — a SKIP there would be a fake pass on an
// unreachable dependency. A non-host unresolved var stays a legitimate SKIP.
func TestRunOne_UnresolvedHostVarFails(t *testing.T) {
	r := &Runner{Resolver: &CheckVarResolver{Env: map[string]string{}}}
	// ${HOST:absent:80} can't be resolved → the cross-member probe's premise
	// failed → FAIL (never reaches the curl; returns at the var-resolution gate).
	hostCheck := cmdOpP("curl -fsS http://${HOST:absent:80}/")
	if res := r.runOne(context.Background(), hostCheck); res.Status != TestFail {
		t.Errorf("unresolved ${HOST:…} → status %v (%q), want TestFail", res.Status, res.Message)
	}
	// A non-host unresolved var is a legitimate SKIP (input genuinely N/A here).
	otherCheck := cmdOpP("echo ${SOME_UNSET_VAR}")
	if res := r.runOne(context.Background(), otherCheck); res.Status != TestSkip {
		t.Errorf("unresolved non-host var → status %v (%q), want TestSkip", res.Status, res.Message)
	}
}

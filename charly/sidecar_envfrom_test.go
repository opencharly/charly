package main

import (
	"strings"
	"testing"
)

// TestRenderSidecarEnvFrom exercises the host-side env-var name
// resolution that the tailscale sidecar uses for multi-tailnet
// .secrets storage. Regression guard against the 2026-05-12 incident
// where a single flat TS_AUTHKEY conflated two tailnets.
func TestRenderSidecarEnvFrom(t *testing.T) {
	for _, tc := range []struct {
		name        string
		envFrom     string
		envFallback string
		params      map[string]string
		wantHostEnv string
		wantErr     bool
		errContains string
	}{
		{
			name:        "legacy_default_no_envfrom_falls_back_to_env",
			envFrom:     "",
			envFallback: "TS_AUTHKEY",
			params:      nil,
			wantHostEnv: "TS_AUTHKEY",
		},
		{
			name:        "tailnet_armadillo_quail",
			envFrom:     "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}",
			envFallback: "TS_AUTHKEY",
			params:      map[string]string{"tailnet": "armadillo-quail.ts.net"},
			wantHostEnv: "TS_AUTHKEY_ARMADILLO_QUAIL_TS_NET",
		},
		{
			name:        "tailnet_tail297eca",
			envFrom:     "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}",
			envFallback: "TS_AUTHKEY",
			params:      map[string]string{"tailnet": "tail297eca.ts.net"},
			wantHostEnv: "TS_AUTHKEY_TAIL297ECA_TS_NET",
		},
		{
			name:        "missing_required_parameter_errors_with_hint",
			envFrom:     "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}",
			envFallback: "TS_AUTHKEY",
			params:      nil,
			wantErr:     true,
			errContains: "parameter \"tailnet\" which is unset",
		},
		{
			name:        "empty_string_parameter_treated_as_missing",
			envFrom:     "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}",
			envFallback: "TS_AUTHKEY",
			params:      map[string]string{"tailnet": ""},
			wantErr:     true,
			errContains: "which is unset",
		},
		{
			name:        "non_tailnet_special_chars_normalized",
			envFrom:     "PREFIX_{{.Parameter.x | tailnetEnvSuffix}}",
			envFallback: "PREFIX",
			params:      map[string]string{"x": "weird/name.with-stuff:and stuff"},
			wantHostEnv: "PREFIX_WEIRD_NAME_WITH_STUFF_AND_STUFF",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := SidecarSecret{Name: "test", Env: tc.envFallback, EnvFrom: tc.envFrom}
			gotHostEnv, err := renderSidecarEnvFrom(s, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("renderSidecarEnvFrom: expected error containing %q, got nil; result was %q", tc.errContains, gotHostEnv)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("renderSidecarEnvFrom: error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("renderSidecarEnvFrom: unexpected error: %v", err)
			}
			if gotHostEnv != tc.wantHostEnv {
				t.Errorf("renderSidecarEnvFrom: got %q, want %q", gotHostEnv, tc.wantHostEnv)
			}
		})
	}
}

// TestMergeSidecar_Parameter guards the Parameter map merge in
// MergeSidecar. Regression for the 2026-05-13 bug where deploy.yml's
// `parameter.tailnet: armadillo-quail.ts.net` was silently dropped
// during merge (because Parameter was absent from the merge body),
// causing charly config to fail with "parameter tailnet is unset" despite
// the operator setting it.
func TestMergeSidecar_Parameter(t *testing.T) {
	base := map[string]SidecarDef{
		"tailscale": {Parameter: map[string]string{"tailnet": ""}},
	}
	overlay := map[string]SidecarDef{
		"tailscale": {Parameter: map[string]string{"tailnet": "armadillo-quail.ts.net"}},
	}
	merged := MergeSidecar(base, overlay)
	got := merged["tailscale"].Parameter["tailnet"]
	if got != "armadillo-quail.ts.net" {
		t.Errorf("MergeSidecar dropped deploy parameter: got %q, want %q", got, "armadillo-quail.ts.net")
	}
	// Sanity: an unset deploy override must NOT clobber a template default with a non-empty value.
	base2 := map[string]SidecarDef{"x": {Parameter: map[string]string{"a": "default-a"}}}
	overlay2 := map[string]SidecarDef{"x": {Parameter: map[string]string{"b": "deploy-b"}}}
	merged2 := MergeSidecar(base2, overlay2)
	if merged2["x"].Parameter["a"] != "default-a" {
		t.Errorf("template default lost: got %q", merged2["x"].Parameter["a"])
	}
	if merged2["x"].Parameter["b"] != "deploy-b" {
		t.Errorf("deploy value lost: got %q", merged2["x"].Parameter["b"])
	}
}

// TestExtractParameterRefs guards the helper that produces error
// messages naming WHICH parameter is unset when a template references
// {{.Parameter.X}}. Used by renderSidecarEnvFrom's error path.
func TestExtractParameterRefs(t *testing.T) {
	for _, tc := range []struct {
		name string
		tmpl string
		want []string
	}{
		{"single_ref", "TS_AUTHKEY_{{.Parameter.tailnet | tailnetEnvSuffix}}", []string{"tailnet"}},
		{"two_refs", "{{.Parameter.a}}_{{.Parameter.b}}", []string{"a", "b"}},
		{"no_refs", "static-string", []string{}},
		{"empty", "", []string{}},
		{"ref_then_func", "{{.Parameter.tailnet | tailnetEnvSuffix}}_suffix", []string{"tailnet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := extractParameterRefs(tc.tmpl)
			if len(got) != len(tc.want) {
				t.Fatalf("extractParameterRefs(%q): got %d refs (%v), want %d (%v)", tc.tmpl, len(got), got, len(tc.want), tc.want)
			}
			for _, name := range tc.want {
				if _, ok := got[name]; !ok {
					t.Errorf("extractParameterRefs(%q): missing %q in result %v", tc.tmpl, name, got)
				}
			}
		})
	}
}

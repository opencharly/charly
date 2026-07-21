package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// validate_test.go — after the validate ENGINE moved to candy/plugin-box (task #60), this file keeps
// ONLY the two host-natural rule tests that call a KEPT host function directly. Every former
// synthetic `Validate(cfg, layers, dir, opts)` test was re-expressed as an on-disk fixture (or a
// host-direct / plugin-box envelope-unit test) in validate_fixture_test.go — see the no-net-loss
// scenario map in scratchpad/k3d-scenario-mapping.md.

// TestValidateBuildTunables calls the host-kept validateBuildTunables directly (defaults + per-box
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
			cfg := &Config{Defaults: tc.ic, Box: boxMapOf(map[string]spec.BoxConfig{})}
			errs := &ValidationError{}
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

package main

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// Tests for install_plan.go — the InstallPlan IR.
//
// These exercise the scope/venue/gate/reverse derivations on each step
// kind, plus the StepsByVenue batching logic and GateEnabled matrix.
// They're pure unit tests; no compiler, no emitter, no filesystem.

func TestSystemPackagesStepScopeVenueGate(t *testing.T) {
	tests := []struct {
		name     string
		step     *deploykit.SystemPackagesStep
		wantGate spec.Gate
	}{
		{
			name:     "install phase is ungated",
			step:     &deploykit.SystemPackagesStep{Format: "rpm", Phase: spec.PhaseInstall, Packages: []string{"ripgrep"}},
			wantGate: spec.GateNone,
		},
		{
			name:     "prepare phase with repos needs allow-repo-changes",
			step:     &deploykit.SystemPackagesStep{Format: "rpm", Phase: spec.PhasePrepare, Repos: []deploykit.RepoSpec{{}}},
			wantGate: spec.GateAllowRepoChanges,
		},
		{
			name:     "prepare phase with copr needs allow-repo-changes",
			step:     &deploykit.SystemPackagesStep{Format: "rpm", Phase: spec.PhasePrepare, Copr: []string{"che/nerd-fonts"}},
			wantGate: spec.GateAllowRepoChanges,
		},
		{
			name:     "prepare phase without repo/copr/modules is ungated",
			step:     &deploykit.SystemPackagesStep{Format: "rpm", Phase: spec.PhasePrepare},
			wantGate: spec.GateNone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.step.Scope(); got != spec.ScopeSystem {
				t.Errorf("Scope = %v, want %v", got, spec.ScopeSystem)
			}
			if got := tc.step.Venue(); got != spec.VenueHostNative {
				t.Errorf("Venue = %v, want %v", got, spec.VenueHostNative)
			}
			if got := tc.step.RequiresGate(); got != tc.wantGate {
				t.Errorf("RequiresGate = %v, want %v", got, tc.wantGate)
			}
		})
	}
}

func TestSystemPackagesStepReverse(t *testing.T) {
	// Install phase → one package-remove op with the tracked packages.
	s := &deploykit.SystemPackagesStep{
		Format:   "rpm",
		Phase:    spec.PhaseInstall,
		Packages: []string{"ripgrep", "fd-find"},
	}
	ops := s.Reverse()
	if len(ops) != 1 {
		t.Fatalf("Reverse() len = %d, want 1; got %+v", len(ops), ops)
	}
	if ops[0].Kind != spec.ReverseOpPackageRemove {
		t.Errorf("op kind = %v, want %v", ops[0].Kind, spec.ReverseOpPackageRemove)
	}
	if ops[0].Format != "rpm" {
		t.Errorf("op format = %q, want rpm", ops[0].Format)
	}
	if len(ops[0].Targets) != 2 {
		t.Errorf("op targets len = %d, want 2", len(ops[0].Targets))
	}

	// Prepare phase with copr → one copr-disable per copr entry.
	s2 := &deploykit.SystemPackagesStep{
		Format: "rpm",
		Phase:  spec.PhasePrepare,
		Copr:   []string{"coolercontrol/coolercontrol", "che/nerd-fonts"},
	}
	ops2 := s2.Reverse()
	if len(ops2) != 2 {
		t.Fatalf("Reverse() len = %d, want 2 (one per copr); got %+v", len(ops2), ops2)
	}
	for _, op := range ops2 {
		if op.Kind != spec.ReverseOpCoprDisable {
			t.Errorf("op kind = %v, want %v", op.Kind, spec.ReverseOpCoprDisable)
		}
	}
}

func TestBuilderStepScopeByBuilder(t *testing.T) {
	tests := []struct {
		builder string
		want    spec.Scope
	}{
		{"aur", spec.ScopeSystem},
		{"pixi", spec.ScopeUser},
		{"npm", spec.ScopeUser},
		{"cargo", spec.ScopeUser},
	}
	for _, tc := range tests {
		t.Run(tc.builder, func(t *testing.T) {
			s := &deploykit.BuilderStep{Builder: tc.builder}
			if got := s.Scope(); got != tc.want {
				t.Errorf("Scope() = %v, want %v", got, tc.want)
			}
			if got := s.Venue(); got != spec.VenueContainerBuilder {
				t.Errorf("Venue() = %v, want container-builder", got)
			}
		})
	}
}

// TestBuilderStepReverse proves BuilderStep.Reverse() is a PURE getter of PreResolvedReverse
// (the externalization invariant): the per-builder reverse-op DERIVATION moved out-of-process to
// kit.BuilderReverse (the build pre-pass invokes it via OpReverse and stashes the result here), so
// Reverse() must NOT recompute — it echoes the stashed ops with no registry/RPC. The derivation
// itself is covered by plugin/kit/builder_test.go.
func TestBuilderStepReverse(t *testing.T) {
	want := []spec.ReverseOp{{Kind: spec.ReverseOpPixiEnvRemove, Targets: []string{"default"}, Scope: spec.ScopeUser, Extra: map[string]string{"layer": "pre-commit"}}}
	s := &deploykit.BuilderStep{
		Builder:            "pixi",
		CandyName:          "pre-commit",
		RawStageContext:    map[string]any{"env_name": "default"},
		PreResolvedReverse: want,
	}
	ops := s.Reverse()
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpPixiEnvRemove || ops[0].Scope != spec.ScopeUser {
		t.Fatalf("Reverse() = %+v, want the stashed PreResolvedReverse verbatim", ops)
	}

	// A step with NO pre-resolved reverse (a custom candy builder, or a direct compile with no
	// pre-pass) returns nil — Reverse() never derives from RawStageContext anymore.
	bare := &deploykit.BuilderStep{Builder: "aur", RawStageContext: map[string]any{"packages": []string{"x"}}}
	if got := bare.Reverse(); got != nil {
		t.Fatalf("Reverse() with no PreResolvedReverse = %+v, want nil (no in-proc derivation)", got)
	}
}

func TestTaskStepScopeFromResolvedUser(t *testing.T) {
	tests := []struct {
		user string
		want spec.Scope
	}{
		{"", spec.ScopeSystem},
		{"root", spec.ScopeSystem},
		{"0", spec.ScopeSystem},
		{"0:0", spec.ScopeSystem},
		{"1000:1000", spec.ScopeUser},
		{"ubuntu", spec.ScopeUser},
	}
	for _, tc := range tests {
		t.Run(tc.user, func(t *testing.T) {
			s := &deploykit.OpStep{ResolvedUser: tc.user, Op: cmdOpP("true")}
			if got := s.Scope(); got != tc.want {
				t.Errorf("Scope() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTaskStepCmdGate(t *testing.T) {
	// root cmd task is gated
	s := &deploykit.OpStep{ResolvedUser: "root", Op: cmdOpP("dnf install -y foo")}
	if got := s.RequiresGate(); got != spec.GateAllowRootTasks {
		t.Errorf("root cmd gate = %v, want allow-root-tasks", got)
	}
	// root structured task (mkdir) is NOT gated
	s = &deploykit.OpStep{ResolvedUser: "root", Op: &spec.Op{Mkdir: "/etc/foo"}}
	if got := s.RequiresGate(); got != spec.GateNone {
		t.Errorf("root mkdir gate = %v, want none", got)
	}
	// user cmd task is NOT gated
	s = &deploykit.OpStep{ResolvedUser: "1000:1000", Op: cmdOpP("pixi install")}
	if got := s.RequiresGate(); got != spec.GateNone {
		t.Errorf("user cmd gate = %v, want none", got)
	}
}

func TestPathIsSystemScoped(t *testing.T) {
	tests := map[string]bool{
		"/etc/passwd":             true,
		"/usr/local/bin/tool":     true,
		"/var/log/foo":            true,
		"/opt/foo/bin/bar":        true,
		"/home/user/.cargo/bin/x": false,
		"/home/user/.pixi/envs/":  false,
		"./relative":              false,
		"":                        false,
	}
	for path, want := range tests {
		if got := deploykit.PathIsSystemScoped(path); got != want {
			t.Errorf("pathIsSystemScoped(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestServicePackagedStepReverse(t *testing.T) {
	// With enable + overrides + priorEnabled → all three ops.
	s := &deploykit.ServicePackagedStep{
		Unit:          "postgresql.service",
		TargetScope:   spec.ScopeSystem,
		Enable:        true,
		OverridesText: "[Service]\nEnvironment=FOO=bar\n",
		OverridesPath: "/etc/systemd/system/postgresql.service.d/charly-pg.conf",
		PriorEnabled:  true,
	}
	ops := s.Reverse()
	wantKinds := []spec.ReverseOpKind{spec.ReverseOpServiceDisable, spec.ReverseOpRestoreEnabled, spec.ReverseOpRemoveDropin}
	if len(ops) != len(wantKinds) {
		t.Fatalf("Reverse() len = %d, want %d; got %+v", len(ops), len(wantKinds), ops)
	}
	for i, op := range ops {
		if op.Kind != wantKinds[i] {
			t.Errorf("op[%d].Kind = %v, want %v", i, op.Kind, wantKinds[i])
		}
	}
}

func TestServiceCustomStepRequiresGate(t *testing.T) {
	s := &deploykit.ServiceCustomStep{Name: "charly-x-y", UnitText: "[Unit]\n", UnitPath: "/etc/systemd/system/charly-x-y.service", TargetScope: spec.ScopeSystem, Enable: true}
	if got := s.RequiresGate(); got != spec.GateWithServices {
		t.Errorf("RequiresGate() = %v, want with-services", got)
	}
}

func TestShellHookStep(t *testing.T) {
	s := &deploykit.ShellHookStep{
		CandyName: "pre-commit",
		EnvVars:   map[string]string{"PIXI_CACHE_DIR": "$HOME/.cache/pixi"},
		PathAdd:   []string{"/home/user/.pixi/envs/default/bin"},
		EnvFile:   "/home/user/.config/opencharly/env.d/pre-commit.env",
	}
	if s.Scope() != spec.ScopeUserProfile {
		t.Errorf("Scope() = %v, want user-profile", s.Scope())
	}
	ops := s.Reverse()
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpRemoveEnvdFile {
		t.Errorf("Reverse() = %+v, want [remove-envd-file]", ops)
	}
}

func TestRepoChangeStep(t *testing.T) {
	s := &deploykit.RepoChangeStep{
		Format:    "rpm",
		File:      "/etc/yum.repos.d/rpmfusion-free.repo",
		Content:   "[rpmfusion-free]\n...",
		Checksum:  "abc123",
		CandyName: "rpmfusion",
	}
	if s.RequiresGate() != spec.GateAllowRepoChanges {
		t.Errorf("RepoChangeStep requires %v, want allow-repo-changes", s.RequiresGate())
	}
	ops := s.Reverse()
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpRemoveRepoFile {
		t.Errorf("Reverse() = %+v, want [remove-repo-file]", ops)
	}
}

func TestInstallPlanStepsByVenue(t *testing.T) {
	// Three contiguous system steps, then a user step, then a builder step
	// → three batches.
	plan := &deploykit.InstallPlan{
		Steps: []spec.InstallStep{
			&deploykit.SystemPackagesStep{Format: "rpm", Phase: spec.PhaseInstall, Packages: []string{"a"}},
			&deploykit.SystemPackagesStep{Format: "rpm", Phase: spec.PhaseInstall, Packages: []string{"b"}},
			&deploykit.OpStep{ResolvedUser: "root", Op: &spec.Op{Mkdir: "/etc/foo"}},
			&deploykit.OpStep{ResolvedUser: "1000:1000", Op: &spec.Op{Mkdir: "$HOME/bin"}},
			&deploykit.BuilderStep{Builder: "pixi"},
		},
	}
	batches := plan.StepsByVenue()
	// Expected: [system+host-native × 3, user+host-native × 1, user+container-builder × 1]
	if len(batches) != 3 {
		t.Fatalf("batch count = %d, want 3; got %+v", len(batches), batches)
	}
	if batches[0].Scope != spec.ScopeSystem || batches[0].Venue != spec.VenueHostNative || len(batches[0].Steps) != 3 {
		t.Errorf("batch 0: scope=%v venue=%v steps=%d; want system/host-native/3",
			batches[0].Scope, batches[0].Venue, len(batches[0].Steps))
	}
	if batches[1].Scope != spec.ScopeUser || batches[1].Venue != spec.VenueHostNative || len(batches[1].Steps) != 1 {
		t.Errorf("batch 1: scope=%v venue=%v steps=%d; want user/host-native/1",
			batches[1].Scope, batches[1].Venue, len(batches[1].Steps))
	}
	if batches[2].Scope != spec.ScopeUser || batches[2].Venue != spec.VenueContainerBuilder || len(batches[2].Steps) != 1 {
		t.Errorf("batch 2: scope=%v venue=%v steps=%d; want user/container-builder/1",
			batches[2].Scope, batches[2].Venue, len(batches[2].Steps))
	}
}

func TestGateEnabledMatrix(t *testing.T) {
	tests := []struct {
		name string
		gate spec.Gate
		opts deploykit.EmitOpts
		want bool
	}{
		{"none is always enabled", spec.GateNone, deploykit.EmitOpts{}, true},
		{"repo-changes disabled by default", spec.GateAllowRepoChanges, deploykit.EmitOpts{}, false},
		{"repo-changes enabled via flag", spec.GateAllowRepoChanges, deploykit.EmitOpts{AllowRepoChanges: true}, true},
		{"repo-changes enabled via --yes", spec.GateAllowRepoChanges, deploykit.EmitOpts{AssumeYes: true}, true},
		{"root-tasks disabled by default", spec.GateAllowRootTasks, deploykit.EmitOpts{}, false},
		{"root-tasks enabled via flag", spec.GateAllowRootTasks, deploykit.EmitOpts{AllowRootTasks: true}, true},
		{"services disabled by default", spec.GateWithServices, deploykit.EmitOpts{}, false},
		{"services enabled via flag", spec.GateWithServices, deploykit.EmitOpts{WithServices: true}, true},
		{"services enabled via --yes", spec.GateWithServices, deploykit.EmitOpts{AssumeYes: true}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deploykit.GateEnabled(tc.gate, tc.opts); got != tc.want {
				t.Errorf("GateEnabled(%v, %+v) = %v, want %v", tc.gate, tc.opts, got, tc.want)
			}
		})
	}
}

func TestExtractStringSliceHandlesBothShapes(t *testing.T) {
	// []string
	m1 := map[string]any{"k": []string{"a", "b"}}
	if got := deploykit.ExtractStringSlice(m1, "k"); len(got) != 2 || got[0] != "a" {
		t.Errorf("extractStringSlice([]string) = %v, want [a b]", got)
	}
	// []interface{} (as produced by yaml.v3 when unmarshaling into map[string]interface{})
	m2 := map[string]any{"k": []any{"a", "b"}}
	if got := deploykit.ExtractStringSlice(m2, "k"); len(got) != 2 || got[0] != "a" {
		t.Errorf("extractStringSlice([]interface{}) = %v, want [a b]", got)
	}
	// Missing key → nil
	if got := deploykit.ExtractStringSlice(m1, "missing"); got != nil {
		t.Errorf("missing key returned %v, want nil", got)
	}
	// Nil map → nil
	if got := deploykit.ExtractStringSlice(nil, "k"); got != nil {
		t.Errorf("nil map returned %v, want nil", got)
	}
}

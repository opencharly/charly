package build

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// host_infra_builder_run_test.go — relocated from charly/host_infra_test.go (#55 decoupling,
// Batch A, cross-batch file-ownership matrix: Batch A executes this move on Batch B's behalf).
// These 3 tests (the "builder_run.go" section) assert kit.BuildBuilderRunArgs/BuilderRun
// directly, zero charly coupling.

func TestBuildBuilderRunArgs(t *testing.T) {
	opts := spec.BuilderRunOpts{
		BuilderImage: "fedora-builder:latest",
		CandyDir:     "/home/user/layers/pre-commit",
		HostHome:     "/home/user",
		BindMounts: map[string]string{
			"/home/user/.pixi": "/home/user/.pixi",
		},
		Env: map[string]string{
			"PIXI_CACHE_DIR": "/home/user/.cache/charly/pixi",
		},
	}
	args := kit.BuildBuilderRunArgs(opts)
	want := []string{
		"run", "--rm",
		"--pull=never", // the injected EnsureImage closure has already handled the pull/build; suppress podman's auto-pull.
		"--user",       // we don't check the exact uid because it varies
	}
	if len(args) < len(want) {
		t.Fatalf("args too short: %v", args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, args[i], w, args)
		}
	}
	// Verify critical pieces are present.
	fullCmd := strings.Join(args, " ")
	mustContain := []string{
		"fedora-builder:latest",
		"-v /home/user/.pixi:/home/user/.pixi:rw",
		"-v /home/user/layers/pre-commit:/work:ro",
		"-e HOME=/home/user",
		"-e PIXI_CACHE_DIR=/home/user/.cache/charly/pixi",
		"-w /work",
		"bash -s",
	}
	for _, m := range mustContain {
		if !strings.Contains(fullCmd, m) {
			t.Errorf("missing %q in args: %s", m, fullCmd)
		}
	}
}

func TestBuilderRunDryRun(t *testing.T) {
	// DryRun should return nil, nil without actually exec'ing.
	out, err := kit.BuilderRun(context.Background(), spec.BuilderRunOpts{
		BuilderImage: "fedora-builder",
		DryRun:       true,
		ScriptBody:   "echo hi",
	})
	if err != nil {
		t.Errorf("dry-run should not error: %v", err)
	}
	if out != nil {
		t.Errorf("dry-run should return nil output; got %q", out)
	}
}

// TestBuildBuilderRunArgsRunAsRoot asserts the RunAsRoot path emits
// `--user 0:0`. the local deploy target.execBuilder always sets RunAsRoot=true
// because rootless podman maps in-container uid 0 to the operator's host
// uid; bind-mounts of $HOME/.cargo / $HOME/.npm-global / etc. are then
// writable. Without this flag the in-container user is mapped to a
// subordinate uid that doesn't match the bind-mount owner and writes
// fail with EACCES.
func TestBuildBuilderRunArgsRunAsRoot(t *testing.T) {
	opts := spec.BuilderRunOpts{
		BuilderImage: "arch-builder:latest",
		CandyDir:     "/home/user/layers/pre-commit",
		HostHome:     "/home/user",
		RunAsRoot:    true,
	}
	args := kit.BuildBuilderRunArgs(opts)
	full := strings.Join(args, " ")
	if !strings.Contains(full, "--user 0:0") {
		t.Errorf("RunAsRoot did not emit --user 0:0; got: %s", full)
	}
}

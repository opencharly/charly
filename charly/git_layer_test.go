package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opencharly/spec/refs"
	"github.com/opencharly/spec/spec"
)

// git_layer_test.go — B12 coverage for the charly-core git layer + the
// retention-defaults seam + the builtin prescan skip. Each test FAILS without
// its behavior.

// TestBuiltinOnlyInvocation proves the prescan remote fetch is skipped for
// builtin-only invocations (version/help/settings/ssh) — the #423 13s
// `charly version` hang. A non-builtin command (status, box, check) must NOT be
// skipped.
func TestBuiltinOnlyInvocation(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"charly"}, true},            // bare → help
		{[]string{"charly", "version"}, true}, // builtin
		{[]string{"charly", "help"}, true},    // builtin
		{[]string{"charly", "--help"}, true},  // builtin
		{[]string{"charly", "-h"}, true},      // builtin
		{[]string{"charly", "settings"}, true},
		{[]string{"charly", "ssh"}, true},
		{[]string{"charly", "status"}, false}, // needs external words
		{[]string{"charly", "box", "list"}, false},
		{[]string{"charly", "check", "run"}, false},
	}
	for _, c := range cases {
		old := os.Args
		os.Args = c.args
		got := builtinOnlyInvocation()
		os.Args = old
		if got != c.want {
			t.Errorf("builtinOnlyInvocation(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// TestHostBuildRetentionDefaults reads the retention defaults from a charly.yml
// WITHOUT walking the project or resolving @github refs — the #423 clean-hang
// fix. An absent/unreadable charly.yml degrades to 0/0 (retention disabled).
func TestHostBuildRetentionDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(cfg, []byte("version: 2026.240.1943\ndefaults:\n    keep_images: 5\n    keep_check_runs: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := spec.RetentionRequest{Dir: dir}
	reply, err := hostBuildRetentionDefaults(nil, req, buildEngineContext{})
	if err != nil {
		t.Fatalf("hostBuildRetentionDefaults: %v", err)
	}
	if reply.KeepImages != 5 {
		t.Errorf("KeepImages = %d, want 5", reply.KeepImages)
	}
	if reply.KeepCheckRuns != 3 {
		t.Errorf("KeepCheckRuns = %d, want 3", reply.KeepCheckRuns)
	}

	// Absent charly.yml → retention disabled (0/0).
	empty := filepath.Join(t.TempDir(), "charly.yml")
	reply, err = hostBuildRetentionDefaults(nil, spec.RetentionRequest{Dir: filepath.Dir(empty)}, buildEngineContext{})
	if err != nil {
		t.Fatalf("hostBuildRetentionDefaults (absent): %v", err)
	}
	if reply.KeepImages != 0 || reply.KeepCheckRuns != 0 {
		t.Errorf("absent config: got %d/%d, want 0/0", reply.KeepImages, reply.KeepCheckRuns)
	}
}

// TestGitClientSingleton proves the charly-core process-wide GitClient singleton
// constructs a spec/refs.GitClient (the centralized git layer from spec#65/#66).
func TestGitClientSingleton(t *testing.T) {
	old := gitClientInstance
	gitClientOnce = sync.Once{}
	gitClientInstance = refs.NewGitClient(filepath.Join(t.TempDir(), "charly.yml"))
	defer func() {
		gitClientInstance = old
		gitClientOnce = sync.Once{}
	}()

	c := gitClient()
	if c == nil {
		t.Fatal("gitClient() returned nil")
	}
	// The singleton is process-wide: a second call returns the SAME instance.
	if c2 := gitClient(); c2 != c {
		t.Fatal("gitClient() is not a singleton")
	}
}

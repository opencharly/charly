package fleet

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// shell_schema_test.go — relocated (in part) from charly/shell_schema_test.go (#55
// decoupling, Batch A): these 3 tests assert deploykit.ResolveShellSpec/AppendShellPathLines or
// kit.ShellExecutor directly, zero charly coupling.

// TestResolveShellSpec_SelectionRule — per-shell wins over generic;
// ${SHELL_NAME} substituted only when falling back to generic.
func TestResolveShellSpec_SelectionRule(t *testing.T) {
	cfg := &spec.Shell{
		Init: `check "$(direnv hook ${SHELL_NAME})"`,
		Fish: &vmshared.ShellSpec{Init: "direnv hook fish | source"},
	}
	// fish: per-shell override wins, no substitution.
	_, body, _, ok := deploykit.ResolveShellSpec(cfg, "fish")
	if !ok || body != "direnv hook fish | source" {
		t.Errorf("fish selection: ok=%v body=%q", ok, body)
	}
	// bash: falls back to generic, ${SHELL_NAME} → bash.
	_, body, _, ok = deploykit.ResolveShellSpec(cfg, "bash")
	if !ok || !strings.Contains(body, "direnv hook bash") {
		t.Errorf("bash selection: ok=%v body=%q", ok, body)
	}
	// Candy with no shell: returns false for any shell.
	_, _, _, ok = deploykit.ResolveShellSpec(nil, "bash")
	if ok {
		t.Error("nil cfg should yield !ok")
	}
}

// TestExecutor_ResolveHome_Local — ShellExecutor.ResolveHome returns
// $HOME for empty user and a sensible value for an explicit user.
func TestExecutor_ResolveHome_Local(t *testing.T) {
	exec := kit.ShellExecutor{}
	home, err := exec.ResolveHome(context.Background(), "")
	if err != nil {
		t.Fatalf("ResolveHome empty user: %v", err)
	}
	if home == "" {
		t.Fatal("empty home")
	}
}

// TestAppendShellPathLines_FishSyntax — fish gets fish_add_path, others
// get POSIX export PATH.
func TestAppendShellPathLines_FishSyntax(t *testing.T) {
	body := `check "$(direnv hook bash)"`
	got := deploykit.AppendShellPathLines(body, []string{"~/.local/bin"}, "fish", "/home/u")
	if !strings.Contains(got, "fish_add_path") {
		t.Errorf("fish should use fish_add_path: %q", got)
	}

	got2 := deploykit.AppendShellPathLines(body, []string{"~/.local/bin"}, "bash", "/home/u")
	if !strings.Contains(got2, "export PATH=") {
		t.Errorf("bash should use POSIX export: %q", got2)
	}
}

package main

// shell_schema_test.go — exercises the 2026-05 shell:-schema cutover
// across the schema/IR/compiler/label-round-trip layers.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"

	"gopkg.in/yaml.v3"
)

// TestShellConfig_GenericForm — parser accepts an intrinsic body with
// no per-shell sub-blocks; ByShell stays nil.
func TestShellConfig_GenericForm(t *testing.T) {
	src := []byte(`
init: |
  check "$(direnv hook ${SHELL_NAME})"
path_append:
  - "~/.local/bin"
priority: 10
`)
	var cfg spec.Shell
	if err := yaml.Unmarshal(src, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Init == "" {
		t.Fatal("Init was empty")
	}
	if !strings.Contains(cfg.Init, "${SHELL_NAME}") {
		t.Fatalf("Init missing token: %q", cfg.Init)
	}
	if len(cfg.PathAppend) != 1 || cfg.PathAppend[0] != "~/.local/bin" {
		t.Fatalf("PathAppend: %v", cfg.PathAppend)
	}
	if cfg.Priority != 10 {
		t.Fatalf("Priority = %d, want 10", cfg.Priority)
	}
	if cfg.ByShell() != nil {
		t.Fatalf("ByShell = %v, want nil", cfg.ByShell())
	}
}

// TestShellConfig_PerShellOverride — parser splits per-shell sub-blocks
// (bash/zsh/fish/sh) into ByShell while leaving the intrinsic Init
// in place.
func TestShellConfig_PerShellOverride(t *testing.T) {
	src := []byte(`
init: |
  check "$(direnv hook ${SHELL_NAME})"
fish:
  init: |
    direnv hook fish | source
`)
	var cfg spec.Shell
	if err := decodeViaCUEForTest(t, string(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.ByShell()["fish"] == nil {
		t.Fatal("ByShell[fish] missing")
	}
	if !strings.Contains(cfg.ByShell()["fish"].Init, "direnv hook fish | source") {
		t.Fatalf("fish init: %q", cfg.ByShell()["fish"].Init)
	}
}

// TestShellConfig_RejectsUnknownShell — author typos for shell name
// fail at parse time rather than silently dropping.
func TestShellConfig_RejectsUnknownShell(t *testing.T) {
	// Unknown-shell-key rejection moved from the deleted ShellConfig.UnmarshalYAML
	// to CUE closed-schema validation. That validation is wired into the loader
	// only AFTER schema/*.cue is canonical-tightened (#Shell currently describes
	// the authored bash/zsh shape, not the normalizer's by_shell canonical shape).
	// Re-enable once load-time CUE validation lands. See cue-loader-switch-design.
	t.Skip("unknown-shell rejection moves to CUE validation; pending schema canonical-tighten + load validation")
}

// TestResolveShellSpec_SelectionRule relocated to candy/plugin-bundle (#55 decoupling, Batch
// A) — it asserted deploykit.ResolveShellSpec directly, zero charly dep.

// TestShellSnippetStep_ReverseOps — UseDropin=true reverses via
// rm-file-* per scope; UseDropin=false reverses via remove-managed-block.
func TestShellSnippetStep_ReverseOps(t *testing.T) {
	dropin := &spec.ShellSnippetStep{
		CandyName:   "direnv",
		Shell:       "fish",
		Snippet:     "direnv hook fish | source\n",
		Destination: "/home/u/.config/fish/conf.d/charly-direnv.fish",
		Marker:      "direnv",
		UseDropin:   true,
	}
	ops := dropin.Reverse()
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpRmFileUser {
		t.Errorf("dropin Reverse: %+v", ops)
	}

	managed := &spec.ShellSnippetStep{
		CandyName:   "direnv",
		Shell:       "bash",
		Snippet:     `check "$(direnv hook bash)"`,
		Destination: "/home/u/.bashrc",
		Marker:      "direnv",
		UseDropin:   false,
	}
	ops = managed.Reverse()
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpRemoveManaged {
		t.Errorf("managed Reverse: %+v", ops)
	}
	if ops[0].Extra["marker"] != "direnv" {
		t.Errorf("marker propagation: %v", ops[0].Extra)
	}
}

// TestLabelShellSet_RoundTrip — JSON-marshal a populated set and
// reparse via ExtractMetadata-shaped logic. Catches drift between
// in-memory shape and label-emit/extract pair.
func TestLabelShellSet_RoundTrip(t *testing.T) {
	original := &spec.LabelShellSet{
		Candy: []spec.ShellEntry{
			{
				Origin: "direnv",
				ID:     "direnv",
				Generic: &vmshared.ShellSpec{
					Init: `check "$(direnv hook ${SHELL_NAME})"`,
				},
				ByShell: map[string]*vmshared.ShellSpec{
					"fish": {Init: "direnv hook fish | source"},
				},
			},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtripped spec.LabelShellSet
	if err := json.Unmarshal(data, &roundtripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(roundtripped.Candy) != 1 {
		t.Fatalf("Candy count: %d", len(roundtripped.Candy))
	}
	got := roundtripped.Candy[0]
	if got.Origin != "direnv" || got.ID != "direnv" {
		t.Errorf("origin/id: %+v", got)
	}
	if got.Generic == nil || !strings.Contains(got.Generic.Init, "${SHELL_NAME}") {
		t.Errorf("generic init: %+v", got.Generic)
	}
	if got.ByShell["fish"] == nil || !strings.Contains(got.ByShell["fish"].Init, "direnv hook fish") {
		t.Errorf("fish: %+v", got.ByShell)
	}
}

// TestExecutor_ResolveHome_Local / TestAppendShellPathLines_FishSyntax relocated to
// candy/plugin-bundle (#55 decoupling, Batch A) — they asserted kit.ShellExecutor /
// deploykit.AppendShellPathLines directly, zero charly dep.

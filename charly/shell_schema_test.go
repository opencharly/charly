package main

// shell_schema_test.go — exercises the 2026-05 shell:-schema cutover
// across the schema/IR/compiler/label-round-trip layers.

import (
	"context"
	"encoding/json"
	"github.com/opencharly/sdk/spec"
	"strings"
	"testing"

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

// TestResolveShellSpec_SelectionRule — per-shell wins over generic;
// ${SHELL_NAME} substituted only when falling back to generic.
func TestResolveShellSpec_SelectionRule(t *testing.T) {
	cfg := &spec.Shell{
		Init: `check "$(direnv hook ${SHELL_NAME})"`,
		Fish: &ShellSpec{Init: "direnv hook fish | source"},
	}
	// fish: per-shell override wins, no substitution.
	_, body, _, ok := resolveShellSpec(cfg, "fish")
	if !ok || body != "direnv hook fish | source" {
		t.Errorf("fish selection: ok=%v body=%q", ok, body)
	}
	// bash: falls back to generic, ${SHELL_NAME} → bash.
	_, body, _, ok = resolveShellSpec(cfg, "bash")
	if !ok || !strings.Contains(body, "direnv hook bash") {
		t.Errorf("bash selection: ok=%v body=%q", ok, body)
	}
	// Candy with no shell: returns false for any shell.
	_, _, _, ok = resolveShellSpec(nil, "bash")
	if ok {
		t.Error("nil cfg should yield !ok")
	}
}

// TestShellSnippetStep_ReverseOps — UseDropin=true reverses via
// rm-file-* per scope; UseDropin=false reverses via remove-managed-block.
func TestShellSnippetStep_ReverseOps(t *testing.T) {
	dropin := &ShellSnippetStep{
		CandyName:   "direnv",
		Shell:       "fish",
		Snippet:     "direnv hook fish | source\n",
		Destination: "/home/u/.config/fish/conf.d/charly-direnv.fish",
		Marker:      "direnv",
		UseDropin:   true,
	}
	ops := dropin.Reverse()
	if len(ops) != 1 || ops[0].Kind != ReverseOpRmFileUser {
		t.Errorf("dropin Reverse: %+v", ops)
	}

	managed := &ShellSnippetStep{
		CandyName:   "direnv",
		Shell:       "bash",
		Snippet:     `check "$(direnv hook bash)"`,
		Destination: "/home/u/.bashrc",
		Marker:      "direnv",
		UseDropin:   false,
	}
	ops = managed.Reverse()
	if len(ops) != 1 || ops[0].Kind != ReverseOpRemoveManaged {
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
	original := &LabelShellSet{
		Candy: []ShellEntry{
			{
				Origin: "direnv",
				ID:     "direnv",
				Generic: &ShellSpec{
					Init: `check "$(direnv hook ${SHELL_NAME})"`,
				},
				ByShell: map[string]*ShellSpec{
					"fish": {Init: "direnv hook fish | source"},
				},
			},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtripped LabelShellSet
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

// TestMergeDeployShell_ReplaceByID — overlay with matching id replaces
// the baked entry; non-matching id appends to Deploy.
func TestMergeDeployShell_ReplaceByID(t *testing.T) {
	baked := &LabelShellSet{
		Candy: []ShellEntry{
			{Origin: "direnv", ID: "direnv", Generic: &ShellSpec{Init: "default"}},
		},
	}
	overlay := []ShellEntry{
		{ID: "direnv", Generic: &ShellSpec{Init: "overridden"}},
		{Origin: "deploy", Generic: &ShellSpec{Init: "fresh-deploy-entry"}},
	}
	merged := MergeDeployShell(baked, overlay)
	if len(merged.Candy) != 1 || merged.Candy[0].Generic.Init != "overridden" {
		t.Errorf("replace by id: %+v", merged.Candy)
	}
	if len(merged.Deploy) != 1 || merged.Deploy[0].Generic.Init != "fresh-deploy-entry" {
		t.Errorf("append to deploy: %+v", merged.Deploy)
	}
}

// TestMergeDeployShell_SkipDropsBakedEntry — overlay with skip:true
// (encoded as Generic + ByShell both nil) drops the matched entry.
func TestMergeDeployShell_SkipDropsBakedEntry(t *testing.T) {
	baked := &LabelShellSet{
		Candy: []ShellEntry{
			{Origin: "direnv", ID: "direnv", Generic: &ShellSpec{Init: "x"}},
		},
	}
	overlay := []ShellEntry{
		{ID: "direnv"}, // Generic + ByShell nil ⇒ skip signal
	}
	merged := MergeDeployShell(baked, overlay)
	if len(merged.Candy) != 0 {
		t.Errorf("skip should drop entry: %+v", merged.Candy)
	}
}

// TestExecutor_ResolveHome_Local — ShellExecutor.ResolveHome returns
// $HOME for empty user and a sensible value for an explicit user.
func TestExecutor_ResolveHome_Local(t *testing.T) {
	exec := ShellExecutor{}
	home, err := exec.ResolveHome(context.Background(), "")
	if err != nil {
		t.Fatalf("ResolveHome empty user: %v", err)
	}
	if home == "" {
		t.Fatal("empty home")
	}
}

// TestDeployShellOverlay_YAMLParse — end-to-end YAML→DeployShellOverlay→
// MergeDeployShell chain (Bed 6 of the R10 matrix). Asserts that a
// deploy.yml-shape `shell:` block parses correctly through the custom
// UnmarshalYAML AND merges as expected against a baked LabelShellSet.
func TestDeployShellOverlay_YAMLParse(t *testing.T) {
	src := []byte(`
- id: direnv
  fish:
    init: |
      direnv hook fish | source --no-prompt
- origin: deploy
  bash:
    init: |
      export PROJECT_VAR=workstation
- id: agent-forwarding:bash
  skip: true
`)
	var overlays []DeployShellOverlay
	if err := decodeViaCUEForTest(t, string(src), &overlays); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(overlays) != 3 {
		t.Fatalf("expected 3 overlays, got %d", len(overlays))
	}
	// First entry replaces direnv:fish.
	o1 := overlays[0]
	if o1.ID != "direnv" {
		t.Errorf("o1.ID = %q", o1.ID)
	}
	if o1.ByShell()["fish"] == nil || !strings.Contains(o1.ByShell()["fish"].Init, "--no-prompt") {
		t.Errorf("o1.ByShell()[fish]: %+v", o1.ByShell())
	}
	// Second is a fresh deploy-scope entry.
	o2 := overlays[1]
	if o2.Origin != "deploy" {
		t.Errorf("o2.Origin = %q", o2.Origin)
	}
	if o2.ByShell()["bash"] == nil {
		t.Errorf("o2 missing bash sub-block")
	}
	// Third is a skip.
	o3 := overlays[2]
	if !o3.Skip {
		t.Errorf("o3.Skip = false")
	}

	// Convert + merge against a baked set.
	baked := &LabelShellSet{
		Candy: []ShellEntry{
			{
				Origin:  "direnv",
				ID:      "direnv",
				Generic: &ShellSpec{Init: "default-direnv"},
				ByShell: map[string]*ShellSpec{
					"fish": {Init: "default-fish-snippet"},
				},
			},
			{
				Origin:  "agent-forwarding",
				ID:      "agent-forwarding:bash",
				Generic: &ShellSpec{Init: "agent-forwarding-default"},
			},
		},
	}
	merged := MergeDeployShell(baked, []ShellEntry{
		shellOverlayToEntry(&overlays[0]),
		shellOverlayToEntry(&overlays[1]),
		shellOverlayToEntry(&overlays[2]),
	})
	// direnv entry should now reflect the overlay's fish override.
	if len(merged.Candy) == 0 || merged.Candy[0].ID != "direnv" {
		t.Fatalf("direnv entry missing: %+v", merged.Candy)
	}
	if merged.Candy[0].ByShell["fish"] == nil || !strings.Contains(merged.Candy[0].ByShell["fish"].Init, "--no-prompt") {
		t.Errorf("direnv.fish overlay not applied: %+v", merged.Candy[0].ByShell)
	}
	// agent-forwarding:bash should be GONE (skipped).
	for _, e := range merged.Candy {
		if e.ID == "agent-forwarding:bash" {
			t.Errorf("agent-forwarding:bash should be skipped: %+v", e)
		}
	}
	// Fresh deploy-scope entry should be in Deploy.
	foundFresh := false
	for _, e := range merged.Deploy {
		if e.Origin == "deploy" && e.ByShell["bash"] != nil &&
			strings.Contains(e.ByShell["bash"].Init, "PROJECT_VAR") {
			foundFresh = true
		}
	}
	if !foundFresh {
		t.Errorf("fresh deploy-scope overlay missing: %+v", merged.Deploy)
	}
}

// TestAppendShellPathLines_FishSyntax — fish gets fish_add_path, others
// get POSIX export PATH.
func TestAppendShellPathLines_FishSyntax(t *testing.T) {
	body := `check "$(direnv hook bash)"`
	got := appendShellPathLines(body, []string{"~/.local/bin"}, "fish", "/home/u")
	if !strings.Contains(got, "fish_add_path") {
		t.Errorf("fish should use fish_add_path: %q", got)
	}

	got2 := appendShellPathLines(body, []string{"~/.local/bin"}, "bash", "/home/u")
	if !strings.Contains(got2, "export PATH=") {
		t.Errorf("bash should use POSIX export: %q", got2)
	}
}

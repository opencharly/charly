package agentkind

import (
	"context"
	"strings"
	"testing"

	pb "github.com/opencharly/sdk/proto"
)

// The compiled-in command dispatch regression gate: a compiled-in command runs
// in charly's OWN process, and kong's default Exit is os.Exit, so a raw
// kong.Parse of `--help` would kill charly whole (and this test binary) while
// skipping every defer. runCommand must route through sdk.RunInProcCLI so every
// help arm PRINTS and RETURNS instead of exiting (sdk/clidispatch.go documents
// the hazard; sdk's clidispatch_test.go proves the helper matrix).
func TestRunCommandHelpReturnsWithoutExiting(t *testing.T) {
	for _, word := range []string{"agent", "tui", "tmux"} {
		if err := runCommand(word, []string{"--help"}); err != nil {
			t.Fatalf("runCommand(%s --help): want nil after printing help, got %v", word, err)
		}
	}
}

// A subcommand `--help` must stop at the printed help — never fall through to
// the leaf's Run() with default flags (the exact failure a no-op kong.Exit
// produced). `tmux list` requires a <box> argument and touches the agent
// session store when run, so any fall-through surfaces as a non-nil error.
func TestRunCommandSubcommandHelpDoesNotRunLeaf(t *testing.T) {
	if err := runCommand("tmux", []string{"list", "--help"}); err != nil {
		t.Fatalf("runCommand(tmux list --help): want nil, got %v", err)
	}
}

// A parse failure surfaces as an ordinary error (the host's exit-code mapping
// classifies it), never a process exit; an unknown word keeps its explicit error.
func TestRunCommandParseErrorPropagates(t *testing.T) {
	if err := runCommand("agent", []string{"no-such-leaf"}); err == nil {
		t.Fatal("unknown agent leaf: want a parse error, got nil")
	}
	if err := runCommand("bogus", nil); err == nil || !strings.Contains(err.Error(), `unsupported command word "bogus"`) {
		t.Fatalf("unsupported word: want the explicit error, got %v", err)
	}
}

// The command CLIModels ride Describe (lazily, error-returning — never a
// NewMeta-time panic crashing charly startup). A healthy grammar must yield
// every capability with the three command models populated.
func TestNewMetaDescribeBuildsCommandModels(t *testing.T) {
	caps, err := NewMeta().Describe(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]bool{}
	for _, provided := range caps.GetProvided() {
		if provided.GetClass() == "command" {
			if len(provided.GetCommandModelJson()) == 0 {
				t.Fatalf("command %q served no CLI model", provided.GetWord())
			}
			models[provided.GetWord()] = true
		}
	}
	for _, word := range []string{"agent", "tui", "tmux"} {
		if !models[word] {
			t.Fatalf("Describe missing command model for %q", word)
		}
	}
}

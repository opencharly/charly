package agentkind

import (
	"strings"
	"testing"
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

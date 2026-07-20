package check

import "testing"

// Ported from the now-deleted charly/agent_config_test.go (P12a dead-code sweep):
// charly/agent_config.go's VersionResult/ParseAgentTimeout were a byte-identical,
// zero-production-caller duplicate of this package's own copies (agent.go) and were
// deleted outright rather than folded — this is the one live copy's test coverage,
// mirroring the aliasNameRe port pattern this same wave-2 cutover already used for
// candy/plugin-box/validate_rules.go.

func TestVersionResultString(t *testing.T) {
	v := VersionResult{Stdout: "claude 0.1.2"}
	if v.String() != "claude 0.1.2" {
		t.Errorf("got %q", v.String())
	}
	v2 := VersionResult{Error: "command not found"}
	if v2.String() != "error: command not found" {
		t.Errorf("got %q", v2.String())
	}
}

func TestParseAgentTimeout(t *testing.T) {
	// Empty timeout → 0 (no wall-clock cap). check_loop branches on `dur == 0` and
	// uses context.WithCancel instead of WithTimeout, honoring the "Take all the
	// time you need" prompt promise; plateau detection is the loop bound.
	d, err := ParseAgentTimeout("")
	if err != nil {
		t.Fatalf("default (empty) timeout failed: %v", err)
	}
	if d != 0 {
		t.Errorf("default timeout should be 0 (no cap); got %v", d)
	}
	// Explicit cap still works for authors who want one.
	d2, err := ParseAgentTimeout("5m")
	if err != nil || d2.Minutes() != 5 {
		t.Errorf("explicit timeout: got %v, err=%v", d2, err)
	}
	if _, err := ParseAgentTimeout("nope"); err == nil {
		t.Error("invalid duration should error")
	}
}

package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestFirstCommandWord(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   string
		wantOK bool
	}{
		{"bare", nil, "", false},
		{"simple", []string{"check", "run", "x"}, "check", true},
		{"flag-before", []string{"--host", "o.example.org", "status"}, "status", true},
		{"flag-eq-value", []string{"--dir=/x/y", "deploy", "add"}, "deploy", true},
		{"value-flag-no-value", []string{"--host=", "version"}, "version", true},
		{"short-C", []string{"-C", "/p", "box", "build"}, "box", true},
		{"host-option", []string{"--host-option", "K=V", "logs"}, "logs", true},
		{"double-dash", []string{"--", "fleet", "add"}, "fleet", true},
		{"retired-word", []string{"fleet", "add", "x"}, "fleet", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := firstCommandWord(c.args)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("firstCommandWord(%v) = (%q, %v), want (%q, %v)", c.args, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestRetiredCommandWord_EndToEnd is the B12 (pr-validator) gate for the CC-2
// retired-word intercept: a REAL charly binary (buildCharlyBinary, the same
// helper main_dir_test.go uses) must exit 80 on a stray `charly fleet …` with
// the pointed message, and still parse the new `charly deploy …` surface
// (exit 0). Fails without the main() wiring (the guard would be dead code).
func TestRetiredCommandWord_EndToEnd(t *testing.T) {
	bin := buildCharlyBinary(t)

	// The retired word: hard error + exit 80 (kong's usage-error code).
	cmd := exec.Command(bin, "fleet", "add", "x")
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("charly fleet exit: %v (want exit 80) — output: %s", err, out)
	}
	if code := ee.ExitCode(); code != retiredCommandExit {
		t.Fatalf("charly fleet exit code = %d, want %d — output: %s", code, retiredCommandExit, out)
	}
	if !strings.Contains(string(out), "is retired in the CC-2 vocabulary cutover") {
		t.Errorf("retired-word error missing the pointed message; output: %s", out)
	}

	// The new surface still parses.
	cmd2 := exec.Command(bin, "deploy", "--help")
	if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
		t.Fatalf("charly deploy --help failed: %v — output: %s", err2, out2)
	}
}

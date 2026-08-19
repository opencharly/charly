package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestRunCliSubcommandAbsoluteExecutableSurvivesChdir(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "charly-helper")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'nested:%s' \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	reply := runCliSubcommand(executable, spec.CliRequest{Argv: []string{"ok"}, Capture: true, Combined: true})
	if reply.ExitCode != 0 || reply.Error != "" || reply.Stdout != "nested:ok" {
		t.Fatalf("runCliSubcommand() = %#v", reply)
	}
}

func TestRunCliSubcommandPreservesSpawnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-charly")
	reply := runCliSubcommand(missing, spec.CliRequest{Capture: true, Combined: true})
	if reply.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", reply.ExitCode)
	}
	if !strings.Contains(reply.Error, "missing-charly") || !strings.Contains(reply.Error, "no such file") {
		t.Fatalf("Error = %q, want executable path and OS error", reply.Error)
	}
}

// writeShellHelper drops a /bin/sh script that stands in for the nested `charly` binary, so a test
// can drive runCliSubcommand's capture wiring with an exactly-known stdout/stderr write sequence.
func writeShellHelper(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "charly-helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func capturedLines(t *testing.T, reply spec.CliReply) []string {
	t.Helper()
	if reply.ExitCode != 0 || reply.Error != "" {
		t.Fatalf("helper failed: %#v", reply)
	}
	return strings.Split(strings.TrimSuffix(reply.Stdout, "\n"), "\n")
}

// A combined capture must stay LINE-reliable: the check-bed .log files it produces are scanned
// line-by-line for warnings, so a stderr fragment must never be spliced into the middle of a
// stdout line. The helper writes a PARTIAL stdout line, then a whole stderr line, then finishes
// the stdout line — the exact shape that produced a phantom warning in a real build log.
//
// Wiring one *bytes.Buffer to both cmd.Stdout and cmd.Stderr fails this deterministically: exec's
// interfaceEqual collapses them onto a single child fd, yielding the two lines
// "Starting full system upgradewarning: config file changed" and "..." — the real warning line is
// gone and a phantom one has taken its place.
func TestRunCliSubcommandCombinedKeepsLinesIntact(t *testing.T) {
	helper := writeShellHelper(t, `printf 'Starting full system upgrade' >&1
printf 'warning: config file changed\n' >&2
printf '...\n' >&1`)

	reply := runCliSubcommand(helper, spec.CliRequest{Capture: true, Combined: true})
	got := capturedLines(t, reply)

	want := []string{"Starting full system upgrade...", "warning: config file changed"}
	sorted := slices.Clone(got)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Fatalf("captured lines = %q, want the two intact lines %q (in either order)\nraw: %q",
			got, want, reply.Stdout)
	}
	// The property the project actually gates on: a warning line is countable as a whole line.
	if !slices.Contains(got, "warning: config file changed") {
		t.Fatalf("no intact warning line in %q", got)
	}
}

// Splitting the fds must not reorder a single stream: within stdout (and within stderr) the
// captured order is exactly the emission order.
func TestRunCliSubcommandCombinedPreservesPerStreamOrder(t *testing.T) {
	helper := writeShellHelper(t, `i=1
while [ $i -le 40 ]; do printf 'out-%s\n' "$i" >&1; printf 'err-%s\n' "$i" >&2; i=$((i+1)); done`)

	reply := runCliSubcommand(helper, spec.CliRequest{Capture: true, Combined: true})
	got := capturedLines(t, reply)

	var out, errs []string
	for _, line := range got {
		switch {
		case strings.HasPrefix(line, "out-"):
			out = append(out, line)
		case strings.HasPrefix(line, "err-"):
			errs = append(errs, line)
		default:
			t.Fatalf("spliced line %q in %q", line, got)
		}
	}
	for i := range 40 {
		if want := fmt.Sprintf("out-%d", i+1); out[i] != want {
			t.Fatalf("stdout[%d] = %q, want %q", i, out[i], want)
		}
		if want := fmt.Sprintf("err-%d", i+1); errs[i] != want {
			t.Fatalf("stderr[%d] = %q, want %q", i, errs[i], want)
		}
	}
}

// Two genuinely concurrent writers, each emitting many lines in small pieces, is the shape that
// corrupts a real build log (a process inside the container and the engine around it). Every
// captured line must still be whole.
func TestRunCliSubcommandCombinedSurvivesConcurrentWriters(t *testing.T) {
	helper := writeShellHelper(t, `( i=1; while [ $i -le 60 ]; do printf 'out-'; printf '%s\n' "$i"; i=$((i+1)); done ) >&1 &
( i=1; while [ $i -le 60 ]; do printf 'err-' >&2; printf '%s\n' "$i" >&2; i=$((i+1)); done ) &
wait`)

	reply := runCliSubcommand(helper, spec.CliRequest{Capture: true, Combined: true})
	got := capturedLines(t, reply)

	intact := regexp.MustCompile(`^(out|err)-\d+$`)
	for _, line := range got {
		if !intact.MatchString(line) {
			t.Fatalf("spliced line %q among %d captured lines", line, len(got))
		}
	}
	if len(got) != 120 {
		t.Fatalf("captured %d lines, want 120", len(got))
	}
}

// Combined=false keeps stderr on the host's stderr rather than folding it into the reply, so the
// non-combined leg must capture stdout ALONE.
func TestRunCliSubcommandNonCombinedCapturesStdoutOnly(t *testing.T) {
	helper := writeShellHelper(t, `printf 'to-stdout\n' >&1
printf 'to-stderr\n' >&2`)

	reply := runCliSubcommand(helper, spec.CliRequest{Capture: true})
	if reply.ExitCode != 0 || reply.Error != "" {
		t.Fatalf("helper failed: %#v", reply)
	}
	if reply.Stdout != "to-stdout\n" {
		t.Fatalf("Stdout = %q, want only the stdout line", reply.Stdout)
	}
}

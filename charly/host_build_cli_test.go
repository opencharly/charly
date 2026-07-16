package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
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

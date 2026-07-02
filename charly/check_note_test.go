package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNote_Empty(t *testing.T) {
	dir := t.TempDir()
	body, err := ReadNote(dir, "myscore")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestAppendNote_HeaderAndOrdering(t *testing.T) {
	dir := t.TempDir()
	notesPath := filepath.Join(dir, ".check", "rec", "note", "run-1.md")
	t.Setenv("CHARLY_EVAL_NOTES_FILE", notesPath)

	if err := AppendNote(dir, "rec", "run-1", "1", "claude", "first note"); err != nil {
		t.Fatal(err)
	}
	if err := AppendNote(dir, "rec", "run-1", "2", "claude", "second note"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatal(err)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "first note") || !strings.Contains(bodyStr, "second note") {
		t.Errorf("missing notes in output:\n%s", bodyStr)
	}
	idx1 := strings.Index(bodyStr, "first note")
	idx2 := strings.Index(bodyStr, "second note")
	if idx1 == -1 || idx2 == -1 || idx1 >= idx2 {
		t.Errorf("ordering wrong: idx1=%d idx2=%d", idx1, idx2)
	}
	if !strings.Contains(bodyStr, "iter=1") || !strings.Contains(bodyStr, "iter=2") {
		t.Errorf("headers should record iter=N; got:\n%s", bodyStr)
	}
}

func TestAppendNote_RequiresEnvVar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHARLY_EVAL_NOTES_FILE", "")
	if err := AppendNote(dir, "r", "id", "1", "claude", "text"); err == nil {
		t.Error("expected error when CHARLY_EVAL_NOTES_FILE is unset")
	}
}

func TestAppendNote_RequiresText(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHARLY_EVAL_NOTES_FILE", filepath.Join(dir, "notes.md"))
	if err := AppendNote(dir, "r", "id", "1", "claude", ""); err == nil {
		t.Error("expected error for empty text")
	}
	if err := AppendNote(dir, "r", "id", "1", "claude", "   \n  "); err == nil {
		t.Error("expected error for whitespace-only text")
	}
}

func TestAppendNote_RequiresScore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHARLY_EVAL_NOTES_FILE", filepath.Join(dir, "notes.md"))
	if err := AppendNote(dir, "", "id", "1", "claude", "text"); err == nil {
		t.Error("expected error for empty score name")
	}
}

func TestNotePath_DefaultsToScratchpad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHARLY_EVAL_NOTES_FILE", "")
	got := NotePath(dir, "bench")
	want := filepath.Join(dir, ".check", "bench", "note", "scratchpad.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNotePath_RespectsEnvOverride(t *testing.T) {
	override := "/tmp/custom-notes.md"
	t.Setenv("CHARLY_EVAL_NOTES_FILE", override)
	if got := NotePath("/proj", "bench"); got != override {
		t.Errorf("env override ignored: got %q, want %q", got, override)
	}
}

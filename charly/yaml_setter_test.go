package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetByDotPath_ScalarReplacement covers the common case: change a
// nested scalar and verify both the new value AND surrounding comments
// are preserved.
func TestSetByDotPath_ScalarReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "box.yml")
	original := `# Top-of-file authoring comment.
defaults:
  # tag is the CalVer tag baked into images
  tag: nightly
  registry: ghcr.io/old
box:
  hello:
    base: fedora
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := SetByDotPath(path, "defaults.tag", "auto"); err != nil {
		t.Fatalf("SetByDotPath: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	gotS := string(got)
	if !strings.Contains(gotS, "tag: auto") {
		t.Errorf("expected 'tag: auto'; got:\n%s", gotS)
	}
	if !strings.Contains(gotS, "Top-of-file authoring comment.") {
		t.Errorf("top comment was destroyed; got:\n%s", gotS)
	}
	if !strings.Contains(gotS, "# tag is the CalVer tag baked into images") {
		t.Errorf("inline comment was destroyed; got:\n%s", gotS)
	}
	if !strings.Contains(gotS, "registry: ghcr.io/old") {
		t.Errorf("sibling key was destroyed; got:\n%s", gotS)
	}
}

// TestSetByDotPath_ListValue verifies that callers can set a list by
// passing a YAML-encoded value.
func TestSetByDotPath_ListValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "box.yml")
	original := `box:
  hello:
    base: fedora
    candy:
      - sshd
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := SetByDotPath(path, "box.hello.candy", "[supervisord, traefik]"); err != nil {
		t.Fatalf("SetByDotPath: %v", err)
	}
	got, _ := os.ReadFile(path)
	gotS := string(got)
	if strings.Contains(gotS, "sshd") {
		t.Errorf("old layers list was not replaced; got:\n%s", gotS)
	}
	if !strings.Contains(gotS, "supervisord") || !strings.Contains(gotS, "traefik") {
		t.Errorf("new layers list missing; got:\n%s", gotS)
	}
}

// TestSetByDotPath_CreatesIntermediateMapping verifies that descending
// into a missing key creates the intermediate mapping rather than
// erroring out.
func TestSetByDotPath_CreatesIntermediateMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "box.yml")
	if err := os.WriteFile(path, []byte("defaults:\n  tag: nightly\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := SetByDotPath(path, "images.hello.base", "fedora"); err != nil {
		t.Fatalf("SetByDotPath: %v", err)
	}
	got, _ := os.ReadFile(path)
	gotS := string(got)
	if !strings.Contains(gotS, "hello:") || !strings.Contains(gotS, "base: fedora") {
		t.Errorf("intermediate mapping not created; got:\n%s", gotS)
	}
}

// TestSetByDotPath_RejectsScalarDescend verifies the error path: trying
// to descend into a scalar should fail loudly.
func TestSetByDotPath_RejectsScalarDescend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "box.yml")
	if err := os.WriteFile(path, []byte("defaults:\n  tag: nightly\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	err := SetByDotPath(path, "defaults.tag.bad", "x")
	if err == nil {
		t.Fatal("expected error descending into scalar; got nil")
	}
	if !strings.Contains(err.Error(), "expected mapping") {
		t.Errorf("expected 'expected mapping' error; got: %v", err)
	}
}

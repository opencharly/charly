package main

import (
	"strings"
	"testing"
)

func disposableTrue() *bool { b := true; return &b }

// The harness restarts-but-never-creates its sandbox pod, so a score whose
// pod target has no per-host deploy entry must fail fast with the
// remediation — never reach a raw `podman exec` against a container that
// cannot exist.
func TestScorePodTargetEntry_NilConfigFailsFastWithRemediation(t *testing.T) {
	_, err := scorePodTargetEntry(nil, "scaffolding-selftest", "check-sandbox")
	if err == nil {
		t.Fatal("expected an error for a nil per-host deploy config, got nil")
	}
	for _, want := range []string{
		`score "scaffolding-selftest"`,
		`pod "check-sandbox"`,
		"charly bundle add check-sandbox <ref> --disposable",
		"charly start check-sandbox",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing remediation fragment %q", err, want)
		}
	}
}

func TestScorePodTargetEntry_MissingEntryFailsFast(t *testing.T) {
	cfg := &BundleConfig{Bundle: map[string]BundleNode{
		"unrelated": {Disposable: disposableTrue()},
	}}
	_, err := scorePodTargetEntry(cfg, "default", "check-sandbox")
	if err == nil {
		t.Fatal("expected an error for a missing check-sandbox entry, got nil")
	}
	if !strings.Contains(err.Error(), "no deploy entry exists on this host") {
		t.Errorf("error %q does not name the missing-entry precondition", err)
	}
}

func TestScorePodTargetEntry_PresentEntryIsReturned(t *testing.T) {
	cfg := &BundleConfig{Bundle: map[string]BundleNode{
		"check-sandbox": {Disposable: disposableTrue()},
	}}
	entry, err := scorePodTargetEntry(cfg, "default", "check-sandbox")
	if err != nil {
		t.Fatalf("unexpected error for a present entry: %v", err)
	}
	if !entry.IsDisposable() {
		t.Error("returned entry lost its disposable flag")
	}
}

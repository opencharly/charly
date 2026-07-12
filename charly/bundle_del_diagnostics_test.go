package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnresolvedDeployTargetError distinguishes an UNKNOWN target word (a typo) from a KNOWN
// substrate whose out-of-process provider is merely not connected — the conflation that misdirected
// the check-k3s-vm RCA (both used to read "unknown target %q").
func TestUnresolvedDeployTargetError(t *testing.T) {
	// A known substrate word whose provider isn't connected → the not-connected text.
	known := unresolvedDeployTargetError("my-vm", "vm").Error()
	if !strings.Contains(known, "known substrate") || !strings.Contains(known, "not connected") {
		t.Fatalf("a known substrate must report a not-connected provider, got: %s", known)
	}
	if strings.Contains(known, "unknown target") {
		t.Fatalf("a known substrate must NOT be reported as an unknown target, got: %s", known)
	}

	// A genuinely unknown word → the unknown-target text.
	unknown := unresolvedDeployTargetError("my-thing", "poddd").Error()
	if !strings.Contains(unknown, "unknown target") {
		t.Fatalf("a typo target must report unknown target, got: %s", unknown)
	}
	if strings.Contains(unknown, "known substrate") {
		t.Fatalf("a typo target must NOT be reported as a known substrate, got: %s", unknown)
	}
}

// TestPodDeploymentArtifactExists proves the ref-based-del discriminator: a quadlet unit OR a live
// container makes a pod deploy "real"; absence makes a name a typo.
func TestPodDeploymentArtifactExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	prev := containerExists
	containerExists = func(engine, name string) bool { return false } // no container by default
	t.Cleanup(func() { containerExists = prev })

	// No artifact anywhere → not a real deployment.
	if podDeploymentArtifactExists("ghost") {
		t.Fatal("a name with no quadlet and no container must NOT be a real pod deployment")
	}

	// A quadlet unit present → real.
	qdir := filepath.Join(home, ".config", "containers", "systemd")
	if err := os.MkdirAll(qdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qdir, "charly-realpod.container"), []byte("[Container]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !podDeploymentArtifactExists("realpod") {
		t.Fatal("a name with a quadlet unit MUST be a real pod deployment")
	}

	// A live container (no quadlet — e.g. engine.run=direct) → real.
	containerExists = func(engine, name string) bool { return name == "charly-directpod" }
	if !podDeploymentArtifactExists("directpod") {
		t.Fatal("a name with a live container MUST be a real pod deployment (direct-mode, no quadlet)")
	}
}

// TestResolveDelNode_TypoRejected proves the top-level fix: a mistyped name with no charly.yml entry
// and no pod artifact is rejected with "no such deployment", not silently synthesized into a pod del
// that tears down nothing and then fails with a misleading "unknown target pod".
func TestResolveDelNode_TypoRejected(t *testing.T) {
	t.Chdir(t.TempDir()) // an empty project dir → resolveTreeRoot finds no entry
	t.Setenv("HOME", t.TempDir())
	prev := containerExists
	containerExists = func(engine, name string) bool { return false }
	t.Cleanup(func() { containerExists = prev })

	if _, _, err := (&deployDelCmd{Name: "zzz-mistyped-name"}).resolveDelNode(); err == nil {
		t.Fatal("a mistyped name must be rejected, not synthesized into a pod del")
	} else if !strings.Contains(err.Error(), "no such deployment") {
		t.Fatalf("error must say 'no such deployment', got: %v", err)
	}

	// The legacy prefixes still resolve without an artifact.
	if _, kind, err := (&deployDelCmd{Name: "host"}).resolveDelNode(); err != nil || kind != "local" {
		t.Fatalf(`"host" must resolve to local, got kind=%q err=%v`, kind, err)
	}
	if _, kind, err := (&deployDelCmd{Name: "vm:arch"}).resolveDelNode(); err != nil || kind != "vm" {
		t.Fatalf(`"vm:arch" must resolve to vm, got kind=%q err=%v`, kind, err)
	}
}

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// del_resolve_test.go — the ref-based-del discriminator tests, relocated from the deleted
// charly/fleet_del_diagnostics_test.go's TestPodDeploymentArtifactExists + TestResolveDelNode
// (K-wave 2 cone R2 bank C): the del resolution moved here with the "deploy-del-resolve" seam.

// TestPodDeploymentArtifactExists proves the ref-based-del discriminator: a quadlet unit OR a live
// container makes a pod deploy "real"; absence makes a name a typo.
func TestPodDeploymentArtifactExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	prev := kit.ContainerExists
	kit.ContainerExists = func(engine, name string) bool { return false } // no container by default
	t.Cleanup(func() { kit.ContainerExists = prev })

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
	kit.ContainerExists = func(engine, name string) bool { return name == "charly-directpod" }
	if !podDeploymentArtifactExists("directpod") {
		t.Fatal("a name with a live container MUST be a real pod deployment (direct-mode, no quadlet)")
	}
}

// TestResolveDelNode_TypoRejected proves the top-level fix: a mistyped name with no charly.yml entry
// and no pod artifact is rejected with "no such deployment", not silently synthesized into a pod del
// that tears down nothing and then fails with a misleading "unknown target pod".
func TestResolveDelNode_TypoRejected(t *testing.T) {
	t.Chdir(t.TempDir()) // a nil threaded tree → resolveDelNode finds no entry (empty-project equivalent)
	t.Setenv("HOME", t.TempDir())
	prev := kit.ContainerExists
	kit.ContainerExists = func(engine, name string) bool { return false }
	t.Cleanup(func() { kit.ContainerExists = prev })

	if _, _, err := resolveDelNode("zzz-mistyped-name", nil); err == nil {
		t.Fatal("a mistyped name must be rejected, not synthesized into a pod del")
	} else if !strings.Contains(err.Error(), "no such deployment") {
		t.Fatalf("error must say 'no such deployment', got: %v", err)
	}

	// The legacy prefixes still resolve without an artifact.
	if _, kind, err := resolveDelNode("host", nil); err != nil || kind != "local" {
		t.Fatalf(`"host" must resolve to local, got kind=%q err=%v`, kind, err)
	}
	if _, kind, err := resolveDelNode("vm:arch", nil); err != nil || kind != "vm" {
		t.Fatalf(`"vm:arch" must resolve to vm, got kind=%q err=%v`, kind, err)
	}
}

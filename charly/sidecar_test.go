package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestEmbeddedSidecarTemplates verifies the binary-embedded tailscale template
// (charly.yml `sidecar:`) is well-formed. The kernel stores it as an opaque body;
// this test decodes it purely to assert the embedded vocab is correct.
func TestEmbeddedSidecarTemplates(t *testing.T) {
	bodies, err := embeddedSidecarBodies()
	if err != nil {
		t.Fatal(err)
	}
	if bodies == nil {
		t.Fatal("expected non-nil templates")
	}
	body, ok := bodies["tailscale"]
	if !ok {
		t.Fatal("expected tailscale sidecar in embedded templates")
	}
	var ts spec.Sidecar
	if err := json.Unmarshal(body, &ts); err != nil {
		t.Fatalf("decode tailscale template: %v", err)
	}
	if ts.Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("image = %q, want ghcr.io/tailscale/tailscale:latest", ts.Image)
	}
	if ts.Env["TS_USERSPACE"] != "false" {
		t.Errorf("TS_USERSPACE = %q, want false", ts.Env["TS_USERSPACE"])
	}
	if ts.Env["TS_DEBUG_FIREWALL_MODE"] != "nftables" {
		t.Errorf("TS_DEBUG_FIREWALL_MODE = %q, want nftables", ts.Env["TS_DEBUG_FIREWALL_MODE"])
	}
	if len(ts.Volume) != 1 || ts.Volume[0].Name != "state" {
		t.Errorf("volumes = %v, want [{state /var/lib/tailscale}]", ts.Volume)
	}
	if len(ts.Security.CapAdd) != 2 {
		t.Errorf("cap_add = %v, want [NET_ADMIN SYS_MODULE]", ts.Security.CapAdd)
	}
	if len(ts.Secret) != 1 || ts.Secret[0].Env != "TS_AUTHKEY" {
		t.Errorf("secrets = %v, want [{ts-authkey TS_AUTHKEY}]", ts.Secret)
	}
}

func TestHasTailscaleSidecar(t *testing.T) {
	if HasTailscaleSidecar(nil) {
		t.Error("nil should return false")
	}
	if !HasTailscaleSidecar(map[string]json.RawMessage{"tailscale": json.RawMessage("{}")}) {
		t.Error("tailscale should return true")
	}
}

// TestFindPodSidecarQuadlets_ExcludesSiblingInstance is the regression test
// for the charly config remove sidecar-sweep bug: the prior implementation matched
// `<podPrefix>` as a bare filename prefix, which swept up sibling instances of
// the same image (e.g. running `charly config remove versa` stopped the unrelated
// production `charly-versa-ecovoyage.service`). The fix requires the candidate
// quadlet to declare `Pod=<podname>.pod` in its content — the load-bearing
// invariant that distinguishes true sidecars from sibling instances.
func TestFindPodSidecarQuadlets_ExcludesSiblingInstance(t *testing.T) {
	qdir := t.TempDir()

	// Main pod container — caller excludes this from the returned list.
	mainQuadlet := "[Unit]\nDescription=main\n\n[Container]\nPod=charly-versa.pod\nContainerName=charly-versa\nImage=ghcr.io/x/versa:latest\n"
	writeQuadlet(t, qdir, "charly-versa.container", mainQuadlet)

	// True sidecar — has Pod=charly-versa.pod, should match.
	sidecarQuadlet := "[Unit]\nDescription=sidecar\n\n[Container]\nPod=charly-versa.pod\nContainerName=charly-versa-tailscale\nImage=ghcr.io/tailscale/tailscale:latest\n"
	writeQuadlet(t, qdir, "charly-versa-tailscale.container", sidecarQuadlet)

	// Sibling instance — no Pod= directive, must NOT match even though the
	// filename shares the charly-versa- prefix. This is the regression scenario.
	siblingQuadlet := "[Unit]\nDescription=sibling instance\n\n[Container]\nContainerName=charly-versa-ecovoyage\nImage=ghcr.io/x/versa:2026.135.1326\n"
	writeQuadlet(t, qdir, "charly-versa-ecovoyage.container", siblingQuadlet)

	// Sibling instance with its OWN pod — also must NOT match (its Pod=
	// directive references a different pod).
	siblingPodQuadlet := "[Unit]\nDescription=sibling pod instance\n\n[Container]\nPod=charly-versa-canary.pod\nContainerName=charly-versa-canary\nImage=ghcr.io/x/versa:latest\n"
	writeQuadlet(t, qdir, "charly-versa-canary.container", siblingPodQuadlet)

	// Unrelated image whose filename happens to start with charly-versa-something
	// but is NOT in our pod.
	unrelatedQuadlet := "[Unit]\n\n[Container]\nPod=charly-different.pod\nContainerName=charly-versa-something\n"
	writeQuadlet(t, qdir, "charly-versa-something.container", unrelatedQuadlet)

	// Pod file (.pod, not .container) — must be ignored by the sweep.
	writeQuadlet(t, qdir, "charly-versa.pod", "[Pod]\nPodName=charly-versa\n")

	got, err := findPodSidecarQuadlets(qdir, "charly-versa", "charly-versa.container")
	if err != nil {
		t.Fatalf("findPodSidecarQuadlets: %v", err)
	}
	want := []string{"charly-versa-tailscale.container"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sidecars = %v, want %v", got, want)
	}
}

// TestFindPodSidecarQuadlets_InstanceScoping covers the instance variant: a
// removal of `versa -i ecovoyage` (pod name charly-versa-ecovoyage) must NOT pick
// up the BASE versa's quadlets, and must pick up ecovoyage-scoped sidecars.
func TestFindPodSidecarQuadlets_InstanceScoping(t *testing.T) {
	qdir := t.TempDir()

	// Base versa pod members (different pod name — must be excluded).
	writeQuadlet(t, qdir, "charly-versa.container", "[Container]\nPod=charly-versa.pod\n")
	writeQuadlet(t, qdir, "charly-versa-tailscale.container", "[Container]\nPod=charly-versa.pod\n")

	// Ecovoyage instance + its sidecar.
	writeQuadlet(t, qdir, "charly-versa-ecovoyage.container", "[Container]\nPod=charly-versa-ecovoyage.pod\n")
	writeQuadlet(t, qdir, "charly-versa-ecovoyage-tailscale.container", "[Container]\nPod=charly-versa-ecovoyage.pod\n")

	got, err := findPodSidecarQuadlets(qdir, "charly-versa-ecovoyage", "charly-versa-ecovoyage.container")
	if err != nil {
		t.Fatalf("findPodSidecarQuadlets: %v", err)
	}
	want := []string{"charly-versa-ecovoyage-tailscale.container"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sidecars = %v, want %v", got, want)
	}
}

// TestFindPodSidecarQuadlets_EmptyDir handles the no-quadlets case (a
// just-installed system or a fully-cleaned host).
func TestFindPodSidecarQuadlets_EmptyDir(t *testing.T) {
	qdir := t.TempDir()
	got, err := findPodSidecarQuadlets(qdir, "charly-versa", "charly-versa.container")
	if err != nil {
		t.Fatalf("findPodSidecarQuadlets: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func writeQuadlet(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

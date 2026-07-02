package main

import (
	"context"
	"strings"
	"testing"
)

// nestedRecordingExec is a DeployExecutor that records the RunUser scripts
// deployNestedPodsInGuest issues (the in-guest `charly bundle from-box` calls) and
// the PutFile destinations (the host-charly delivery), so the test can assert the
// nested-pod-in-VM orchestration without a real guest.
type nestedRecordingExec struct {
	userScripts []string
	putDests    []string // remote paths the host charly binary was delivered to
}

func (e *nestedRecordingExec) Venue() string                                     { return "nested-rec://test" }
func (e *nestedRecordingExec) RunSystem(context.Context, string, EmitOpts) error { return nil }
func (e *nestedRecordingExec) RunUser(_ context.Context, script string, _ EmitOpts) error {
	e.userScripts = append(e.userScripts, script)
	return nil
}
func (e *nestedRecordingExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *nestedRecordingExec) PutFile(_ context.Context, _, remotePath string, _ uint32, _ bool, _ EmitOpts) error {
	e.putDests = append(e.putDests, remotePath)
	return nil
}
func (e *nestedRecordingExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *nestedRecordingExec) RunCapture(context.Context, string) (string, string, int, error) {
	return "", "", 0, nil
}
func (e *nestedRecordingExec) Kind() string { return "nested-rec" }
func (e *nestedRecordingExec) ResolveHome(context.Context, string) (string, error) {
	return "/home/guest", nil
}

// TestDeployNestedPodsInGuest_DeploysOnlyPodChildren proves the nested-pod-in-VM
// capability's deploy orchestration: each nested target:pod child is built on the
// host, cp-box'd into the guest as localhost/charly-<key>:latest, and brought up via
// the guest's own project-free `charly bundle from-box <ref> <key>` as a PERSISTENT
// (lingering) quadlet — in sorted order — while non-pod children (android/k8s) and
// image-less entries are skipped. Without the capability the helper does not exist
// / does nothing and these assertions fail; this is the check-coverage gate for the
// Go side of Cutover 2 (the live bed proves it end-to-end on the GPU VM).
func TestDeployNestedPodsInGuest_DeploysOnlyPodChildren(t *testing.T) {
	// Stub the child-process boundary: record build / vm-cp-box argv, no real charly.
	var charlyCalls [][]string
	orig := runCharlySubcommand
	runCharlySubcommand = func(args ...string) error {
		charlyCalls = append(charlyCalls, append([]string(nil), args...))
		return nil
	}
	defer func() { runCharlySubcommand = orig }()

	// Stamp the host charly identity. The nested from-box delegation ALWAYS runs
	// the HOST's own charly (delivered to /tmp/charly-<calver> via putHostCharlyInVenue),
	// never the guest's PATH charly — the host binary is the from-box authority,
	// and a /tmp path can't shadow the guest's pacman /usr/bin/charly.
	savedVer := BuildCalVer
	defer func() { BuildCalVer = savedVer }()
	BuildCalVer = "2026.154.0943"

	exec := &nestedRecordingExec{}
	node := &BundleNode{
		Children: map[string]*BundleNode{
			"selkies-kde": {Target: "pod", Image: "selkies-kde-nvidia"},
			"alpha-pod":   {Target: "", Image: "alpha-img"},               // default target == pod
			"droid":       {Target: "android", Image: "android-emulator"}, // skipped (not in-guest)
			"empty":       {Target: "pod"},                                // skipped (no image)
		},
	}

	if err := deployNestedPodsInGuest("cachyos-gpu-vm", node, exec, EmitOpts{}); err != nil {
		t.Fatalf("deployNestedPodsInGuest: %v", err)
	}

	// Two pod children processed (alpha-pod, selkies-kde — sorted); each issues an
	// image-build + a vm-cp-box → 4 charly subcommands, in this exact order. The
	// cp-box carries --rootless so the image lands in the guest USER's podman
	// storage, which the --user from-box quadlet below reads.
	wantCharly := [][]string{
		{"box", "build", "alpha-img"},
		{"vm", "cp-box", "cachyos-gpu-vm", "alpha-img", "--as", "localhost/charly-alpha-pod:latest", "--rootless"},
		{"box", "build", "selkies-kde-nvidia"},
		{"vm", "cp-box", "cachyos-gpu-vm", "selkies-kde-nvidia", "--as", "localhost/charly-selkies-kde:latest", "--rootless"},
	}
	if len(charlyCalls) != len(wantCharly) {
		t.Fatalf("expected %d charly subcommands (build+cp-box × 2 pod children), got %d: %v",
			len(wantCharly), len(charlyCalls), charlyCalls)
	}
	for i, want := range wantCharly {
		if strings.Join(charlyCalls[i], " ") != strings.Join(want, " ") {
			t.Errorf("charly call %d = %v, want %v", i, charlyCalls[i], want)
		}
	}

	// The host charly was delivered into the guest at the explicit /tmp/charly-<calver>
	// path (NOT shadowing the guest's pacman /usr/bin/charly) before any from-box
	// deploy. One delivery for the whole batch (same guest venue).
	wantCharlyPath := "/tmp/charly-2026.154.0943"
	if len(exec.putDests) == 0 {
		t.Fatalf("host charly was never delivered into the guest (no PutFile)")
	}
	deliveredHostCharly := false
	for _, d := range exec.putDests {
		if d == wantCharlyPath {
			deliveredHostCharly = true
		}
	}
	if !deliveredHostCharly {
		t.Errorf("host charly not delivered to %s; PutFile dests = %v", wantCharlyPath, exec.putDests)
	}

	// Two in-guest from-box deploys (the persistent quadlets), sorted. Each
	// invokes the delivered /tmp host charly by EXPLICIT PATH (the from-box
	// authority), never the guest's PATH charly.
	if len(exec.userScripts) != 2 {
		t.Fatalf("expected 2 in-guest from-box deploys, got %d: %v", len(exec.userScripts), exec.userScripts)
	}
	if !strings.Contains(exec.userScripts[0], wantCharlyPath+" bundle from-box localhost/charly-alpha-pod:latest alpha-pod") {
		t.Errorf("script[0] missing alpha-pod from-box deploy via host /tmp charly: %q", exec.userScripts[0])
	}
	if !strings.Contains(exec.userScripts[1], wantCharlyPath+" bundle from-box localhost/charly-selkies-kde:latest selkies-kde") {
		t.Errorf("script[1] missing selkies-kde from-box deploy via host /tmp charly: %q", exec.userScripts[1])
	}
	// Lingering is enabled so the --user quadlet auto-starts at boot — the
	// persistence property the bed's fresh-rebuild leg (guest reboot) proves.
	for i, s := range exec.userScripts {
		if !strings.Contains(s, "loginctl enable-linger") {
			t.Errorf("script[%d] missing enable-linger (persistence): %q", i, s)
		}
	}

	// The skipped children leave no trace anywhere.
	var allCharly string
	for _, c := range charlyCalls {
		allCharly += strings.Join(c, " ") + "\n"
	}
	if strings.Contains(allCharly, "android-emulator") {
		t.Error("android child must NOT be built/loaded as an in-guest pod")
	}
	joinedScripts := strings.Join(exec.userScripts, "\n")
	if strings.Contains(joinedScripts, "droid") || strings.Contains(joinedScripts, "empty") {
		t.Error("non-pod / image-less children must be skipped")
	}
}

// TestDeployNestedPodsInGuest_NoNested is the no-op guard: a nil node or a node
// with no nested children must touch nothing (no build, no cp-box, no deploy).
func TestDeployNestedPodsInGuest_NoNested(t *testing.T) {
	charlyCalls := 0
	orig := runCharlySubcommand
	runCharlySubcommand = func(args ...string) error { charlyCalls++; return nil }
	defer func() { runCharlySubcommand = orig }()

	exec := &nestedRecordingExec{}
	if err := deployNestedPodsInGuest("vm", nil, exec, EmitOpts{}); err != nil {
		t.Fatalf("nil node: %v", err)
	}
	if err := deployNestedPodsInGuest("vm", &BundleNode{}, exec, EmitOpts{}); err != nil {
		t.Fatalf("empty nested: %v", err)
	}
	if charlyCalls != 0 || len(exec.userScripts) != 0 {
		t.Errorf("no-op expected, got %d charly calls + %d guest deploys", charlyCalls, len(exec.userScripts))
	}
}

// TestDeriveDeploymentName covers the default-name derivation the source-less
// `charly bundle from-box <ref>` (pod + k8s) uses when no explicit name is given:
// strip the tag, take the last path component.
func TestDeriveDeploymentName(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"ghcr.io/opencharly/selkies-kde-nvidia:2026.153.1026", "selkies-kde-nvidia"},
		{"localhost/charly-selkies-kde:latest", "charly-selkies-kde"},
		{"selkies-kde-nvidia", "selkies-kde-nvidia"},
		{"docker.io/library/redis:7", "redis"},
	}
	for _, c := range cases {
		if got := deriveDeploymentName(c.ref); got != c.want {
			t.Errorf("deriveDeploymentName(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

// TestMergeDeployConfigs_VMNestedSurvivesNestedlessOverlay locks the merge
// invariant the VM target's nested-pod deploy relies on: a project VM deploy
// that declares a `nested:` target:pod child, overlaid by a per-host operator
// entry that carries its OWN per-host fields but NO `nested:` block, MUST keep
// the project's nested child after merge. This is exactly the operator
// workstation shape (~/.config/charly/deploy.yml's cachyos-gpu has
// target/vm/preemptible but no nested:) that surfaced the failure: a whole-node
// re-read of the operator deploy.yml (operator clobbering project) would drop
// nested: and silently skip deployNestedPodsInGuest. The vm lifecycle hook PostApply
// consumes this merged node directly. The check-bed keys (no operator overlay)
// were never affected — which is why the bug hid behind a green pod bed. The
// end-to-end consumption proof is the live `charly check live cachyos-gpu.selkies-kde`
// R10.
func TestMergeDeployConfigs_VMNestedSurvivesNestedlessOverlay(t *testing.T) {
	project := &BundleConfig{Bundle: map[string]BundleNode{
		"cachyos-gpu": {
			Target: "vm",
			From:   "cachyos-gpu",
			Children: map[string]*BundleNode{
				"selkies-kde": {Target: "pod", Image: "selkies-kde-nvidia"},
			},
		},
	}}
	// Operator per-host overlay: per-host field set, NO nested: block.
	operator := &BundleConfig{Bundle: map[string]BundleNode{
		"cachyos-gpu": {
			Target:    "vm",
			From:      "cachyos-gpu",
			Lifecycle: "prod",
		},
	}}

	merged := MergeDeployConfigs(project, operator)
	node := merged.Bundle["cachyos-gpu"]

	// The operator overlay's non-zero field won (proves the overlay DID merge,
	// not that we merely read the project node)...
	if node.Lifecycle != "prod" {
		t.Errorf("operator Lifecycle not merged: got %q, want prod", node.Lifecycle)
	}
	// ...AND the project's nested child PASSED THROUGH the nestedless overlay.
	// A whole-node replace (the old re-read bug shape) would drop it here.
	if len(node.Children) != 1 || node.Children["selkies-kde"] == nil {
		t.Fatalf("project nested: dropped by nestedless operator overlay: %#v", node.Children)
	}
	if got := node.Children["selkies-kde"].Image; got != "selkies-kde-nvidia" {
		t.Errorf("nested child box: got %q, want selkies-kde-nvidia", got)
	}
}

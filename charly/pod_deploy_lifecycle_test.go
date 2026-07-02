package main

import (
	"context"
	"strings"
	"testing"
)

// TestPodSubstrateLifecycleRegistered proves the pod substrate registers a lifecycle hook
// (the host-side overlay-build + container config/start/remove lifecycle) — pod is the
// externalized substrate whose plugin walks nothing, so the host hook owns everything.
func TestPodSubstrateLifecycleRegistered(t *testing.T) {
	if _, ok := substrateLifecycleFor("pod"); !ok {
		t.Fatal("pod must register a substrateLifecycle")
	}
}

// TestPodLifecycleArtifactKey proves pod keys candy artifacts under the deploy name (the
// generic default — it returns "", so externalDeployTarget.Add falls back to t.name). pod
// has no shared-cluster artifact naming like vm's k3s ClusterProfile.
func TestPodLifecycleArtifactKey(t *testing.T) {
	if got := (podSubstrateLifecycle{}).ArtifactKey("check-pod", &BundleNode{Image: "check-pod"}); got != "" {
		t.Errorf("ArtifactKey = %q, want \"\" (default to deploy name)", got)
	}
}

// TestPodLifecyclePostApply_NoOp proves PostApply is a no-op for pod (its candies bake into
// the image; nested children deploy via the bed-runner tree walk, not in-substrate).
func TestPodLifecyclePostApply_NoOp(t *testing.T) {
	if err := (podSubstrateLifecycle{}).PostApply(context.Background(), "check-pod", ".", &BundleNode{}, ShellExecutor{}, EmitOpts{}); err != nil {
		t.Errorf("PostApply must be a no-op, got %v", err)
	}
}

// TestPodLifecycleTeardownExecutor_Nil proves pod returns no teardown executor (the generic
// Del keeps the host ShellExecutor; pod records no reverse ops, so the replay is a host-side
// no-op and the real teardown is PostTeardown).
func TestPodLifecycleTeardownExecutor_Nil(t *testing.T) {
	exec, err := (podSubstrateLifecycle{}).TeardownExecutor("check-pod", &BundleNode{})
	if err != nil {
		t.Fatalf("TeardownExecutor: %v", err)
	}
	if exec != nil {
		t.Errorf("TeardownExecutor = %T, want nil (keep the ResolveTarget host executor)", exec)
	}
}

// TestPodDeployEngine proves the engine fallback: node.Engine when set, else "podman".
func TestPodDeployEngine(t *testing.T) {
	if got := podDeployEngine(nil); got != "podman" {
		t.Errorf("podDeployEngine(nil) = %q, want podman", got)
	}
	if got := podDeployEngine(&BundleNode{}); got != "podman" {
		t.Errorf("podDeployEngine(empty) = %q, want podman", got)
	}
	if got := podDeployEngine(&BundleNode{Engine: "docker"}); got != "docker" {
		t.Errorf("podDeployEngine(docker) = %q, want docker", got)
	}
}

// TestPodLifecycle_Rebuild_RealInvocations is the regression guard for the
// stale-internal-verb class (ported from the deleted PodUnifiedTarget test): the pod rebuild
// path — now the lifecycle hook's Rebuild, the path `charly update <pod-bed>` routes through
// — must invoke the CURRENT verb names. Stubs runCharlySubcommand and asserts the ACTUAL
// argv (NOT just the dry-run print). That gap is exactly how `check image` survived the
// image→box rebrand. baseRef comes from node.Image.
func TestPodLifecycle_Rebuild_RealInvocations(t *testing.T) {
	var calls [][]string
	orig := runCharlySubcommand
	runCharlySubcommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	defer func() { runCharlySubcommand = orig }()

	life := podSubstrateLifecycle{}
	if err := life.Rebuild(context.Background(), "check-x-pod", &BundleNode{Image: "x"}, RebuildOpts{RebuildImage: true}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	want := [][]string{
		{"box", "build", "x"},
		{"check", "box", "x"}, // NOT "check image" — the verb is registered as `check box`
		{"bundle", "add", "check-x-pod"},
		{"stop", "check-x-pod"},
		{"config", "check-x-pod"},
		{"start", "check-x-pod"},
	}
	if len(calls) != len(want) {
		t.Fatalf("got %d charly subcommands, want %d: %v", len(calls), len(want), calls)
	}
	for i, w := range want {
		if strings.Join(calls[i], " ") != strings.Join(w, " ") {
			t.Errorf("charly call %d = %v, want %v", i, calls[i], w)
		}
	}
}

// TestPodLifecycle_PostTeardown_DelegatesToRemove is the regression guard for the
// record-free pod teardown (ported): PostTeardown delegates the container + quadlet +
// charly.yml cleanup to `charly remove <name>`. keepImage=true isolates the delegation by
// skipping the engine-shelling overlay-image drop.
func TestPodLifecycle_PostTeardown_DelegatesToRemove(t *testing.T) {
	var calls [][]string
	orig := runCharlySubcommand
	runCharlySubcommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	defer func() { runCharlySubcommand = orig }()

	if err := (podSubstrateLifecycle{}).PostTeardown("check-x-pod", &BundleNode{}, true); err != nil {
		t.Fatalf("PostTeardown: %v", err)
	}
	want := [][]string{{"remove", "check-x-pod"}}
	if len(calls) != 1 || strings.Join(calls[0], " ") != strings.Join(want[0], " ") {
		t.Fatalf("PostTeardown charly subcommands = %v, want %v", calls, want)
	}
}

// TestPodLifecycle_Rebuild_DryRun verifies the dry-run path returns nil without invoking any
// subcommand — with RebuildImage on/off, and with an empty node.Image (the NodeName-as-ref
// fallback prevents an empty-ref panic / shell-out).
func TestPodLifecycle_Rebuild_DryRun(t *testing.T) {
	orig := runCharlySubcommand
	runCharlySubcommand = func(args ...string) error {
		t.Fatalf("dry-run must NOT shell out, got %v", args)
		return nil
	}
	defer func() { runCharlySubcommand = orig }()

	life := podSubstrateLifecycle{}
	for _, tc := range []struct {
		name string
		node *BundleNode
		opts RebuildOpts
	}{
		{"rebuild-image", &BundleNode{Image: "sway-browser-vnc"}, RebuildOpts{DryRun: true, RebuildImage: true}},
		{"no-image-rebuild", &BundleNode{Image: "sway-browser-vnc"}, RebuildOpts{DryRun: true}},
		{"baseref-fallback", &BundleNode{ /* Image unset → NodeName ref */ }, RebuildOpts{DryRun: true, RebuildImage: true}},
	} {
		if err := life.Rebuild(context.Background(), "check-sway-browser-vnc-pod", tc.node, tc.opts); err != nil {
			t.Errorf("Rebuild dry-run %s: %v", tc.name, err)
		}
	}
}

// Start/Stop/Status/Logs/Shell are exercised by the R10 live verification (the check-pod
// bed) — not by unit tests, because they shell out via runCharlySubcommand /
// captureCharlyStdout (os.Args[0] = the test binary inside `go test`, which doesn't
// understand the CLI verbs). The dry-run-able Rebuild + the stubbed-subcommand paths above
// cover the deterministic logic.

// TestOverlayHostBuilderRegistered proves PrepareVenue's overlay build goes through the
// uniform F10 hostBuilders registry: the "overlay" kind (the pod-substrate sibling of
// "image"/"containerfiles") is registered at package-var init. Without the registration
// PrepareVenue's hostBuilderFor(overlayBuilderKind) lookup fails hard — this is the C14.3
// build-dispatch-unification invariant (the pod overlay build is no longer an inline
// PodDeployTarget construction).
func TestOverlayHostBuilderRegistered(t *testing.T) {
	if _, ok := hostBuilderFor(overlayBuilderKind); !ok {
		t.Fatalf("the %q host-builder must be registered on the F10 hostBuilders registry", overlayBuilderKind)
	}
	// The kind must be a generic action noun, not a provider WORD (the F11 uniform-API gate
	// TestNoSinglePluginAPISurface scans hostBuilders kinds against the provider-word universe).
	if universe := buildProviderWordUniverse(); universe[overlayBuilderKind] {
		t.Fatalf("hostBuilders kind %q is a provider word — the F11 uniform-API gate forbids one on this surface", overlayBuilderKind)
	}
}

// TestOverlayBuildInputsCtxRoundTrip proves the live-input carrier: PrepareVenue threads the
// compiled plans + the nested-venue ParentExec/ParentNode on the ctx (a live executor cannot
// ride the serializable []byte OverlayBuildRequest), and the host-builder reads them back
// unchanged. A missing value reads back nil (the empty-plans / no-parent probe).
func TestOverlayBuildInputsCtxRoundTrip(t *testing.T) {
	if got := overlayBuildInputsFrom(context.Background()); got != nil {
		t.Fatalf("overlayBuildInputsFrom on a bare ctx = %v, want nil", got)
	}
	plans := []*InstallPlan{{Candy: "marker", AddCandies: []string{"marker"}}}
	node := &BundleNode{Image: "base"}
	exec := ShellExecutor{}
	ctx := withOverlayBuildInputs(context.Background(), &overlayBuildInputs{plans: plans, parentExec: exec, parentNode: node})
	got := overlayBuildInputsFrom(ctx)
	if got == nil {
		t.Fatal("overlayBuildInputsFrom returned nil after withOverlayBuildInputs")
	}
	if len(got.plans) != 1 || got.plans[0].Candy != "marker" {
		t.Errorf("plans not round-tripped: %v", got.plans)
	}
	if got.parentExec == nil {
		t.Error("parentExec not round-tripped")
	}
	if got.parentNode != node {
		t.Error("parentNode not round-tripped")
	}
}

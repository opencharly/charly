package substratekind

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// androidTemplateBody builds an authored kind:android template RawBody — the
// shape resolveSubstrateTemplate decodes.
func androidTemplateBody(t *testing.T, box, adbHost, serial string) spec.RawBody {
	t.Helper()
	a := spec.Android{Box: box, Serial: serial}
	if adbHost != "" {
		a.Adb = &spec.AdbEndpoint{Host: adbHost}
	}
	body, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal android template body: %v", err)
	}
	return body
}

// androidBedResolvedProjectPodKey is a TEST-ONLY deploy name (never a real committed bed) — a
// synthetic fixture must never reuse a real bed name like check-android-emulator-pod: on a host
// where that bed is actually deployed, a non-stubbed live probe keyed by the real name would
// observe the REAL container and silently corrupt the test's "absent" expectation (the exact
// defect TestCollectAndroidStatus_Rows hit: it also stubs containerRunning below, so this rename
// is belt-and-suspenders, not the sole fix).
const androidBedResolvedProjectPodKey = "test-fixture-android"

// androidBedResolvedProject builds a synthetic resolved-project mirroring the shape of a real
// android bed (check-android-emulator-pod): a target:pod root with two nested target:android
// children (an in-pod image device + a remote adb endpoint device), plus the matching
// kind:android templates. Used to drive the pure enumeration paths hermetically (no live adb /
// podman / reverse channel) — its deploy key is deliberately TEST-ONLY (see
// androidBedResolvedProjectPodKey), never a real bed name.
func androidBedResolvedProject(t *testing.T) *spec.ResolvedProject {
	t.Helper()
	return &spec.ResolvedProject{
		Deploy: map[string]*spec.Deploy{
			androidBedResolvedProjectPodKey: {
				Target: "pod",
				Image:  "android-emulator",
				Children: map[string]*spec.Deploy{
					"device":     {Target: "android", From: "pixel9a-36"},
					"device-net": {Target: "android", From: "pixel9a-endpoint"},
				},
			},
			"some-pod": {Target: "pod", Image: "whatever"}, // must not contribute
		},
		Templates: &spec.ProjectTemplates{
			Android: map[string]spec.RawBody{
				"pixel9a-36":       androidTemplateBody(t, "android-emulator", "", ""),
				"pixel9a-endpoint": androidTemplateBody(t, "", "127.0.0.1:1", "emulator-5554"),
			},
		},
	}
}

// withStubContainerRunning swaps the package-level containerRunning var for the duration of fn,
// forcing every call to return `running` and restoring the original afterwards — mirrors
// status_vm_test.go's withMockLibvirtDomains pattern. So a test never touches the real
// podman/docker socket and never depends on host state (the root cause of the
// TestCollectAndroidStatus_Rows nondeterminism: it called the REAL containerRunning, so a live
// check-android-emulator-pod deployment on the host flipped its "absent" expectation to "running").
func withStubContainerRunning(t *testing.T, running bool, fn func()) {
	t.Helper()
	prev := containerRunning
	containerRunning = func(string, string) bool { return running }
	defer func() { containerRunning = prev }()
	fn()
}

func TestCollectAndroidDeployNodes_EnumeratesNestedByDottedPath(t *testing.T) {
	rp := androidBedResolvedProject(t)
	nodes := collectAndroidDeployNodes(rp, nil)
	if len(nodes) != 2 {
		t.Fatalf("collectAndroidDeployNodes() = %d nodes, want 2: %+v", len(nodes), nodes)
	}
	paths := []string{nodes[0].path, nodes[1].path}
	sort.Strings(paths)
	want := []string{
		androidBedResolvedProjectPodKey + ".device",
		androidBedResolvedProjectPodKey + ".device-net",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestCollectAndroidDeployNodes_TopLevel(t *testing.T) {
	rp := &spec.ResolvedProject{
		Deploy: map[string]*spec.Deploy{"phone": {Target: "android", From: "dev"}},
		Templates: &spec.ProjectTemplates{
			Android: map[string]spec.RawBody{"dev": androidTemplateBody(t, "", "h:1", "")},
		},
	}
	nodes := collectAndroidDeployNodes(rp, nil)
	if len(nodes) != 1 || nodes[0].path != "phone" {
		t.Fatalf("collectAndroidDeployNodes() = %#v, want one node path=phone", nodes)
	}
}

// Per-machine overlay overrides the project projection per key (mirrors
// resolveTreeRoot's MergeDeployConfigs(projectDC, localDC) precedence).
func TestCollectAndroidDeployNodes_PerMachineWinsPerKey(t *testing.T) {
	rp := &spec.ResolvedProject{
		Deploy: map[string]*spec.Deploy{"phone": {Target: "android", From: "dev"}},
		Templates: &spec.ProjectTemplates{
			Android: map[string]spec.RawBody{"dev": androidTemplateBody(t, "", "h:1", "")},
		},
	}
	// Per-machine overlay flips "phone" to a pod target — the android node must disappear.
	perMachine := &deploykit.BundleConfig{Bundle: map[string]deploykit.BundleNode{
		"phone": {Target: "pod", Image: "x"},
	}}
	nodes := collectAndroidDeployNodes(rp, perMachine)
	if len(nodes) != 0 {
		t.Fatalf("collectAndroidDeployNodes() = %d nodes, want 0 (per-machine overrode phone to pod)", len(nodes))
	}
}

// collectAndroidOne against a (resolvable) endpoint device reports "declared":
// with no live goadb probe, an endpoint's live state is only assertable via
// the `adb:` check verb, so the collector enumerates it as declared rather
// than probing it. Runs fully hermetically — no live adb, no reverse channel
// (the endpoint path's credential lookup degrades gracefully with no executor
// in ctx, and its host-port substitution short-circuits on a literal addr).
func TestCollectAndroidOne_EndpointDeclared(t *testing.T) {
	rp := &spec.ResolvedProject{
		Templates: &spec.ProjectTemplates{
			Android: map[string]spec.RawBody{"dev": androidTemplateBody(t, "", "127.0.0.1:1", "emulator-5554")},
		},
	}
	dn := androidDeployNode{path: "phone", node: &spec.Deploy{Target: "android", From: "dev"}}
	row := collectAndroidOne(context.Background(), rp, dn, "quadlet")
	if row.Kind != spec.SubstrateAndroid {
		t.Errorf("Kind = %q, want %q", row.Kind, spec.SubstrateAndroid)
	}
	if row.Source != "adb" {
		t.Errorf("Source = %q, want adb", row.Source)
	}
	if row.Image != "phone" {
		t.Errorf("Image (path) = %q, want phone", row.Image)
	}
	if row.Status != "declared" {
		t.Errorf("Status = %q, want declared (endpoint, no in-core probe)", row.Status)
	}
	if row.Container != "emulator-5554" {
		t.Errorf("Container (serial) = %q, want emulator-5554", row.Container)
	}
	if row.Network != "endpoint 127.0.0.1:1" {
		t.Errorf("Network = %q, want endpoint 127.0.0.1:1", row.Network)
	}
	if row.RunMode != "quadlet" {
		t.Errorf("RunMode = %q, want quadlet", row.RunMode)
	}
}

// A node referencing an undeclared kind:android device yields an absent row
// naming the missing reference, not a panic.
func TestCollectAndroidOne_UndeclaredDevice(t *testing.T) {
	rp := &spec.ResolvedProject{Templates: &spec.ProjectTemplates{}}
	dn := androidDeployNode{path: "phone", node: &spec.Deploy{Target: "android", From: "ghost"}}
	row := collectAndroidOne(context.Background(), rp, dn, "")
	if row.Status != "absent" {
		t.Errorf("Status = %q, want absent for undeclared device", row.Status)
	}
	if row.Container != "ghost" {
		t.Errorf("Container = %q, want ghost (the undeclared ref)", row.Container)
	}
}

// collectAndroidStatus over the bed resolved-project produces one row per nested device: the
// in-pod device is "absent" (containerRunning is stubbed false), and the endpoint device is
// "declared". Runs fully hermetically: containerRunning is stubbed (withStubContainerRunning) so
// this NEVER touches the real podman/docker socket regardless of host state, and the fixture's
// deploy key is TEST-ONLY (androidBedResolvedProjectPodKey — never a real committed bed name), so
// it can never collide with an actually-deployed check-android-emulator-pod bed. Prior to this fix
// the test called the REAL containerRunning against the REAL bed name, so it nondeterministically
// failed whenever that bed was actually running on the host (got "running" instead of "absent") —
// a genuine test-isolation defect, not a flake (R1). No reverse channel needed either —
// resolved-project is passed directly rather than fetched via HostBuild in this unit test; the
// HostBuild fetch itself is proven by the k8s live-smoke-test precedent and re-exercised by the
// R10 bed roster.
func TestCollectAndroidStatus_Rows(t *testing.T) {
	rp := androidBedResolvedProject(t)
	nodes := collectAndroidDeployNodes(rp, nil)
	if len(nodes) != 2 {
		t.Fatalf("setup: got %d nodes, want 2", len(nodes))
	}
	wantStatus := map[string]string{
		androidBedResolvedProjectPodKey + ".device":     "absent",
		androidBedResolvedProjectPodKey + ".device-net": "declared",
	}
	withStubContainerRunning(t, false, func() {
		for _, n := range nodes {
			row := collectAndroidOne(context.Background(), rp, n, "")
			if row.Kind != spec.SubstrateAndroid || row.Source != "adb" {
				t.Errorf("row kind/source = %q/%q, want android/adb", row.Kind, row.Source)
			}
			want, ok := wantStatus[row.Image]
			if !ok {
				t.Errorf("unexpected row path %q", row.Image)
				continue
			}
			if row.Status != want {
				t.Errorf("row %q Status = %q, want %q", row.Image, row.Status, want)
			}
		}
	})
}

func TestRemoteRefName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"android-emulator", "android-emulator"},
		{"@github.com/org/repo/box/name:v1.0.0", "name"},
		{"@github.com/org/repo/box/name", "name"},
		{"https://github.com/org/repo/box/name", "https://github.com/org/repo/box/name"}, // no @ → passthrough
	}
	for _, tc := range cases {
		if got := remoteRefName(tc.in); got != tc.want {
			t.Errorf("remoteRefName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

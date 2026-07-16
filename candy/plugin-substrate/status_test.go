package substratekind

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// status_test.go — the relocated tests for the P14a substrate-collector move.
// The golden (collectPodLive live-row transform), the probe Parse tests, the
// live-mounts renderer, the quadlet-description parser, and the local
// install-ledger collector all moved with the code from charly/*.go into this
// plugin; their tests moved with them. Hermeticity is preserved: probes are
// emptied (zero subprocesses), the engine is a no-shell-out NewEngineClient,
// and the ledger is a temp dir.

// withTempLedger mirrors charly/host_infra_test.go's helper — a kit.LedgerPaths
// pointing at a fresh temp dir. Replicated here (R3 — the helper is test-only,
// 4 lines, and lives in the charly module's test package the plugin cannot
// import; the canonical construction is kit.LedgerPaths).
func withTempLedger(t *testing.T) *kit.LedgerPaths {
	t.Helper()
	root := t.TempDir()
	return &kit.LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Candies:  filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}
}

// redirectLocalLedger points the plugin's localLedgerPaths at a fresh temp ledger
// for the test's duration, optionally ensuring the deploys/ dir exists.
func redirectLocalLedger(t *testing.T, ensure bool) *kit.LedgerPaths {
	t.Helper()
	paths := withTempLedger(t)
	if ensure {
		if err := paths.Ensure(); err != nil {
			t.Fatalf("ledger ensure: %v", err)
		}
	}
	prev := localLedgerPaths
	localLedgerPaths = func() (*kit.LedgerPaths, error) { return paths, nil }
	t.Cleanup(func() { localLedgerPaths = prev })
	return paths
}

func collectLocal(t *testing.T) []spec.DeploymentStatus {
	t.Helper()
	reply, err := collectLocalStatus(context.Background(), spec.SubstrateStatusRequest{RunMode: "quadlet"})
	if err != nil {
		t.Fatalf("collectLocalStatus: %v", err)
	}
	return reply.Rows
}

// --- collectPodLive golden (the live-row transform, P14a parity pin) ---

// TestCollectPodLiveGolden captures the snapshot→DeploymentStatus→JSON transform
// as a STABLE golden so a later cutover proves the relocated status-collection
// code produces byte-identical output. Hermetic: probes emptied (zero
// subprocesses), every fixture fills Ports/Volumes/Network so the image-label
// fallback (which would shell out) is skipped, engine = NewEngineClient("podman")
// (no shell-out). Pinned to collectPodLive's OUTPUT SHAPE.
func TestCollectPodLiveGolden(t *testing.T) {
	savedHost, savedGuest := hostProbes, guestProbes
	hostProbes, guestProbes = nil, nil
	defer func() { hostProbes, guestProbes = savedHost, savedGuest }()

	engine := enginekit.NewEngineClient("podman")
	cases := []struct {
		name string
		snap *enginekit.ContainerSnapshot
		want string
	}{
		{
			name: "running-pod",
			snap: &enginekit.ContainerSnapshot{
				Name: "charly-jupyter", State: "running", Status: "Up 3 hours",
				Box: "jupyter", ImageRef: "ghcr.io/opencharly/jupyter:2026.162.1319",
				NetworkMode: "host",
				Ports:       []spec.PortMapping{{HostIP: "127.0.0.1", HostPort: 8888, CtrPort: 8888, Proto: "tcp"}},
				Devices:     []string{"/dev/dri/renderD128"},
				Mounts: []enginekit.MountInfo{
					{Type: "bind", Source: "/home/user/notebooks", Destination: "/workspace", Name: ""},
					{Type: "volume", Source: "charly-jupyter-data", Destination: "/data", Name: "charly-jupyter-data"},
				},
			},
			want: `{
  "kind": "pod",
  "image": "jupyter",
  "image_ref": "ghcr.io/opencharly/jupyter:2026.162.1319",
  "status": "running",
  "uptime": "Up 3 hours",
  "container": "charly-jupyter",
  "ports": [
    {
      "host_ip": "127.0.0.1",
      "host_port": 8888,
      "container_port": 8888,
      "protocol": "tcp"
    }
  ],
  "devices": [
    "/dev/dri/renderD128"
  ],
  "volumes": [
    "bind: /home/user/notebooks -\u003e /workspace",
    "charly-jupyter-data: charly-jupyter-data -\u003e /data"
  ],
  "network": "host",
  "run_mode": "quadlet",
  "source": "podman"
}
`,
		},
		{
			name: "running-pod-instance",
			snap: &enginekit.ContainerSnapshot{
				Name: "charly-jupyter-gpu", State: "running", Status: "Up 12 minutes",
				Box: "jupyter", Instance: "gpu", ImageRef: "ghcr.io/opencharly/jupyter:2026.162.1319",
				NetworkMode: "host",
				Ports: []spec.PortMapping{
					{HostIP: "127.0.0.1", HostPort: 8888, CtrPort: 8888, Proto: "tcp"},
					{HostIP: "0.0.0.0", HostPort: 9443, CtrPort: 9443, Proto: "tcp"},
				},
				Devices: []string{"nvidia.com/gpu=all"},
				Mounts: []enginekit.MountInfo{
					{Type: "bind", Source: "/home/user/notebooks", Destination: "/workspace", Name: ""},
					{Type: "volume", Source: "charly-jupyter-data", Destination: "/data", Name: "charly-jupyter-data"},
				},
			},
			want: `{
  "kind": "pod",
  "image": "jupyter",
  "image_ref": "ghcr.io/opencharly/jupyter:2026.162.1319",
  "instance": "gpu",
  "status": "running",
  "uptime": "Up 12 minutes",
  "container": "charly-jupyter-gpu",
  "ports": [
    {
      "host_ip": "127.0.0.1",
      "host_port": 8888,
      "container_port": 8888,
      "protocol": "tcp"
    },
    {
      "host_ip": "0.0.0.0",
      "host_port": 9443,
      "container_port": 9443,
      "protocol": "tcp"
    }
  ],
  "devices": [
    "nvidia.com/gpu=all"
  ],
  "volumes": [
    "bind: /home/user/notebooks -\u003e /workspace",
    "charly-jupyter-data: charly-jupyter-data -\u003e /data"
  ],
  "network": "host",
  "run_mode": "quadlet",
  "source": "podman"
}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := collectPodLive(context.Background(), tc.snap, "quadlet", engine)
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			if err := enc.Encode(cs); err != nil {
				t.Fatalf("encode: %v", err)
			}
			got := buf.String()
			if got != tc.want {
				t.Errorf("collectPodLive JSON golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// TestCollectPodLive_UsesBaseImageForLabels pins the data-flow invariant: a
// snapshot with Box set populates Ports from the runtime snapshot + preserves
// image/instance. (The image-label fallback itself is host-side enrichOne,
// covered by R10.)
func TestCollectPodLive_UsesBaseImageForLabels(t *testing.T) {
	savedHost, savedGuest := hostProbes, guestProbes
	hostProbes, guestProbes = nil, nil
	defer func() { hostProbes, guestProbes = savedHost, savedGuest }()
	engine := enginekit.NewEngineClient("podman")
	snap := &enginekit.ContainerSnapshot{
		Name: "charly-selkies-desktop-w", Box: "selkies-desktop", Instance: "w",
		State: "running", NetworkMode: "charly",
		Ports: []spec.PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
	}
	cs := collectPodLive(context.Background(), snap, "quadlet", engine)
	if cs.Image != "selkies-desktop" || cs.Instance != "w" {
		t.Errorf("image/instance not preserved: %q/%q", cs.Image, cs.Instance)
	}
	if len(cs.Ports) != 1 || cs.Ports[0].HostPort != 9240 {
		t.Errorf("runtime ports not surfaced: %+v", cs.Ports)
	}
}

// --- statusFromState (the shared mapper, single-sourced in spec) ---

func TestStatusFromState(t *testing.T) {
	cases := map[string]string{
		"running": "running",
		"exited":  "stopped",
		"created": "stopped",
		"paused":  "paused",
		"dead":    "dead",
		"":        "stopped",
		"weird":   "weird",
	}
	for in, want := range cases {
		if got := spec.StatusFromState(in); got != want {
			t.Errorf("StatusFromState(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- probes (Parse + splitProbeSections) ---

// TestDevToolsTabWebSocketField covers the CDP /json decode shape the cdpProbe
// uses (moved from charly/cdp_preresolve_test.go when devToolsTab relocated
// here with the cdp probe).
func TestDevToolsTabWebSocketField(t *testing.T) {
	jsonData := `{"id":"ABC123","title":"Test Page","url":"https://example.com","type":"page","webSocketDebuggerUrl":"ws://localhost:9222/devtools/page/ABC123"}`
	var tab devToolsTab
	if err := json.Unmarshal([]byte(jsonData), &tab); err != nil {
		t.Fatalf("failed to unmarshal devToolsTab: %v", err)
	}
	if tab.ID != "ABC123" {
		t.Errorf("ID = %q, want ABC123", tab.ID)
	}
	if tab.WebSocketDebuggerURL != "ws://localhost:9222/devtools/page/ABC123" {
		t.Errorf("WebSocketDebuggerURL = %q", tab.WebSocketDebuggerURL)
	}
}

func TestSupervisordProbe_Parse(t *testing.T) {
	ts := supervisordProbe{}.Parse("PRESENT=1\nfoo RUNNING\nbar RUNNING\n")
	if ts.Status != "ok" || ts.Detail != "2/2 running" {
		t.Errorf("got %+v", ts)
	}
}

func TestSupervisordProbe_NotInstalled(t *testing.T) {
	ts := supervisordProbe{}.Parse("")
	if ts.Status != "-" {
		t.Errorf("want -, got %q", ts.Status)
	}
}

func TestDbusProbe_WithDaemons(t *testing.T) {
	ts := dbusProbe{}.Parse("DBUS=1\nDAEMON=swaync\nDAEMON=mako\n")
	if ts.Status != "ok" || ts.Detail != "notify:swaync,mako" {
		t.Errorf("got %+v", ts)
	}
}

func TestDbusProbe_NotPresent(t *testing.T) {
	ts := dbusProbe{}.Parse("")
	if ts.Status != "-" {
		t.Error("want -")
	}
}

func TestCharlyProbe_Present(t *testing.T) {
	ts := charlyProbe{}.Parse("CHARLY=1\n2026.100.0000\n")
	if ts.Status != "ok" || ts.Detail != "2026.100.0000" {
		t.Errorf("got %+v", ts)
	}
}

func TestWlProbe_Mixed(t *testing.T) {
	ts := wlProbe{}.Parse("WL=wtype\nWL=grim\n")
	if ts.Status != "ok" || ts.Detail != "wtype,grim" {
		t.Errorf("got %+v", ts)
	}
}

func TestWlProbe_OnlyOneScreenshot(t *testing.T) {
	ts := wlProbe{}.Parse("WL=grim\nWL=pixelflux-screenshot\n")
	if !strings.Contains(ts.Detail, "grim") || strings.Contains(ts.Detail, "pixelflux") {
		t.Errorf("expected one screenshot tool, got %q", ts.Detail)
	}
}

func TestSwayProbe_Outputs(t *testing.T) {
	body := "SWAY=1\n[{\"name\":\"eDP-1\",\"current_mode\":{\"width\":1920,\"height\":1080}}]\n"
	ts := swayProbe{}.Parse(body)
	if ts.Status != "ok" || ts.Detail != "eDP-1 1920x1080" {
		t.Errorf("got %+v", ts)
	}
}

func TestSplitProbeSections(t *testing.T) {
	in := "\n===PROBE:foo===\nFOO\n===PROBE_END:foo===\n===PROBE:bar===\nBAR\n===PROBE_END:bar===\n"
	s := splitProbeSections(in)
	if s["foo"] != "FOO" || s["bar"] != "BAR" {
		t.Errorf("got %+v", s)
	}
}

// --- live mounts ---

func TestIsEncryptedPlainPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/home/user/.local/share/charly/encrypted/charly-immich-library/plain", true},
		{"/mnt/nas/encrypted/charly-app-data/plain", true},
		{"/home/user/project", false},
		{"/var/lib/containers/storage/volumes/charly-immich-cache/_data", false},
		{"/var/lib/myapp/data/plain", false},
		{"/home/user/.local/share/charly/encrypted/charly-foo/cipher", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isEncryptedPlainPath(tc.path); got != tc.want {
			t.Errorf("isEncryptedPlainPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestFormatLiveMounts(t *testing.T) {
	cases := []struct {
		name   string
		mounts []enginekit.MountInfo
		want   []string
	}{
		{"empty", nil, []string{}},
		{"named volume", []enginekit.MountInfo{{Type: "volume", Name: "charly-immich-import", Source: "/v/charly-immich-import/_data", Destination: "/home/user/.immich/import"}}, []string{"charly-immich-import: /v/charly-immich-import/_data -> /home/user/.immich/import"}},
		{"plain bind", []enginekit.MountInfo{{Type: "bind", Name: "", Source: "/home/user/charly", Destination: "/workspace"}}, []string{"bind: /home/user/charly -> /workspace"}},
		{"encrypted FUSE bind", []enginekit.MountInfo{{Type: "bind", Name: "", Source: "/home/user/.local/share/charly/encrypted/charly-immich-library/plain", Destination: "/home/user/.immich/library"}}, []string{"bind: /home/user/.local/share/charly/encrypted/charly-immich-library/plain -> /home/user/.immich/library (enc)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLiveMounts(tc.mounts)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

// --- quadlet description parser ---

func TestParseQuadletDescription_ImageOnly(t *testing.T) {
	dir := t.TempDir()
	unitPath := filepath.Join(dir, "charly-redis.container")
	body := `[Unit]
Description=OpenCharly redis
`
	if err := os.WriteFile(unitPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	image, instance := parseQuadletDescription(unitPath)
	if image != "redis" || instance != "" {
		t.Errorf("got %q/%q want redis/", image, instance)
	}
}

func TestParseQuadletDescription_ImageAndInstance(t *testing.T) {
	dir := t.TempDir()
	unitPath := filepath.Join(dir, "charly-jupyter-concurrency-test.container")
	body := `[Unit]
Description=OpenCharly jupyter (concurrency-test)
`
	if err := os.WriteFile(unitPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	image, instance := parseQuadletDescription(unitPath)
	if image != "jupyter" || instance != "concurrency-test" {
		t.Errorf("got %q/%q want jupyter/concurrency-test", image, instance)
	}
}

func TestParseQuadletDescription_HyphenInImage(t *testing.T) {
	dir := t.TempDir()
	unitPath := filepath.Join(dir, "charly-selkies-desktop-tailnet1.container")
	body := `[Unit]
Description=OpenCharly selkies-desktop (tailnet1)
`
	if err := os.WriteFile(unitPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	image, instance := parseQuadletDescription(unitPath)
	if image != "selkies-desktop" || instance != "tailnet1" {
		t.Errorf("got %q/%q want selkies-desktop/tailnet1", image, instance)
	}
}

func TestParseQuadletDescription_Missing(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
	}{
		{"no Description line", "[Unit]\nAfter=network-online.target\n"},
		{"non-OpenCharly Description", "[Unit]\nDescription=Some other service\n"},
		{"missing file", "DOES_NOT_EXIST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".container")
			if tc.body != "DOES_NOT_EXIST" {
				if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			} else {
				path = filepath.Join(dir, "no-such.container")
			}
			image, instance := parseQuadletDescription(path)
			if image != "" || instance != "" {
				t.Errorf("expected empty, got %q/%q", image, instance)
			}
		})
	}
}

// --- local install-ledger collector ---

func TestCollectLocalStatus_EmptyLedgerNoRows(t *testing.T) {
	redirectLocalLedger(t, true)
	rows := collectLocal(t)
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 for an empty ledger", len(rows))
	}
}

func TestCollectLocalStatus_NoLedgerNoRows(t *testing.T) {
	// ensure=false → deploys/ absent → collectLocalStatus returns no rows
	// (the availability gate is internal now — no separate Available method).
	redirectLocalLedger(t, false)
	rows := collectLocal(t)
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 when the ledger is absent", len(rows))
	}
}

func TestCollectLocalStatus_SynthesizesFromCandyRecords(t *testing.T) {
	paths := redirectLocalLedger(t, true)
	writeCandy(t, paths, &kit.CandyRecord{Candy: "ripgrep", DeployedBy: []string{"deploy-A"}, DeployedAt: "2026-05-30T10:00:00Z"})
	writeCandy(t, paths, &kit.CandyRecord{Candy: "uv", DeployedBy: []string{"deploy-A", "deploy-B"}, DeployedAt: "2026-05-31T12:00:00Z"})
	rows := collectLocal(t)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Container < rows[j].Container })
	a := rows[0]
	if a.Container != "deploy-A" || a.Kind != spec.SubstrateLocal || a.Source != "ledger" || a.Status != "applied" {
		t.Errorf("row[0] = %+v", a)
	}
	if a.RunMode != "quadlet" {
		t.Errorf("RunMode = %q, want quadlet", a.RunMode)
	}
	if a.Image != "local (2 candies)" {
		t.Errorf("Image = %q, want local (2 candies)", a.Image)
	}
	if a.Uptime != "deployed 2026-05-31 12:00 UTC" {
		t.Errorf("Uptime = %q", a.Uptime)
	}
	if rows[1].Image != "local (1 candy)" {
		t.Errorf("row[1].Image = %q, want local (1 candy)", rows[1].Image)
	}
}

func TestCollectLocalStatus_DeployRecordUnionNoDoubleCount(t *testing.T) {
	paths := redirectLocalLedger(t, true)
	if err := kit.WriteDeployRecord(paths, &kit.DeployRecord{
		DeployID: "deploy-X", Target: "vm:check-arch-vm",
		Candy: []string{"base", "charly"}, AddCandy: []string{"sshkeys"},
		DeployedAt: "2026-05-29T08:00:00Z",
	}); err != nil {
		t.Fatalf("WriteDeployRecord: %v", err)
	}
	writeCandy(t, paths, &kit.CandyRecord{Candy: "extra-layer", DeployedBy: []string{"deploy-X"}, DeployedAt: "2026-05-29T09:00:00Z"})
	rows := collectLocal(t)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.Container != "deploy-X" || r.Image != "local (4 candies)" {
		t.Errorf("got %+v", r)
	}
	if r.Uptime != "deployed 2026-05-29 09:00 UTC" {
		t.Errorf("Uptime = %q", r.Uptime)
	}
}

func TestNewerTimestamp(t *testing.T) {
	cases := []struct{ name, a, b, want string }{
		{"a empty", "", "2026-05-31T00:00:00Z", "2026-05-31T00:00:00Z"},
		{"b empty", "2026-05-31T00:00:00Z", "", "2026-05-31T00:00:00Z"},
		{"b newer", "2026-05-30T00:00:00Z", "2026-05-31T00:00:00Z", "2026-05-31T00:00:00Z"},
		{"a newer", "2026-05-31T00:00:00Z", "2026-05-30T00:00:00Z", "2026-05-31T00:00:00Z"},
		{"a malformed", "garbage", "2026-05-30T00:00:00Z", "2026-05-30T00:00:00Z"},
		{"b malformed", "2026-05-30T00:00:00Z", "garbage", "2026-05-30T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := newerTimestamp(c.a, c.b); got != c.want {
				t.Errorf("newerTimestamp(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestFormatLedgerTimestamp(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"not-a-time", ""},
		{"2026-05-31T12:34:56Z", "deployed 2026-05-31 12:34 UTC"},
	}
	for _, c := range cases {
		if got := formatLedgerTimestamp(c.in); got != c.want {
			t.Errorf("formatLedgerTimestamp(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLocalDeployLabel(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{0, "local (0 candies)"}, {1, "local (1 candy)"}, {3, "local (3 candies)"}} {
		if got := localDeployLabel(c.n); got != c.want {
			t.Errorf("localDeployLabel(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func writeCandy(t *testing.T, paths *kit.LedgerPaths, rec *kit.CandyRecord) {
	t.Helper()
	if err := kit.WriteCandyRecord(paths, rec); err != nil {
		t.Fatalf("WriteCandyRecord(%s): %v", rec.Candy, err)
	}
}

// silence unused-import guards for the hermetic engine path (runtime referenced
// by the worker pool in status_pod.go; kept here so the test binary links the
// same symbols the production path does).
var _ = runtime.NumCPU

package spec

import (
	"encoding/json"
	"testing"
)

// TestDeploymentStatusJSONParity locks the `charly status --json` wire shape: the
// generated spec.DeploymentStatus (with the RECURSIVE nested pointer slice) must
// marshal to the EXACT bytes the charly-status skill documents — the spaced
// `"kind": "pod"` form the `command` check probes assert on, correct field order,
// and a nested child carrying its own real `source`. A drift in the CUE def (a
// dropped/renamed field, a lost omitempty, a reordered field) fails here before it
// ever reaches a live `status --json` golden.
func TestDeploymentStatusJSONParity(t *testing.T) {
	child := &DeploymentStatus{
		Kind: SubstrateAndroid, Image: "device", Status: "online",
		Container: "emulator-5554", RunMode: "quadlet", Source: "adb",
	}
	s := DeploymentStatus{
		Kind: SubstratePod, Image: "android-emulator", Status: "running",
		Container: "charly-android-emulator", RunMode: "quadlet", Source: "podman",
		Ports:  []PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
		Tools:  []ToolStatus{{Name: "cdp", Status: "ok", Port: 9240, Detail: "3 tabs"}},
		Nested: []*DeploymentStatus{child},
	}

	got, err := json.MarshalIndent([]DeploymentStatus{s}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `[
  {
    "kind": "pod",
    "image": "android-emulator",
    "status": "running",
    "container": "charly-android-emulator",
    "ports": [
      {
        "host_ip": "127.0.0.1",
        "host_port": 9240,
        "container_port": 9222,
        "protocol": "tcp"
      }
    ],
    "tools": [
      {
        "name": "cdp",
        "status": "ok",
        "port": 9240,
        "detail": "3 tabs"
      }
    ],
    "run_mode": "quadlet",
    "nested": [
      {
        "kind": "android",
        "image": "device",
        "status": "online",
        "container": "emulator-5554",
        "run_mode": "quadlet",
        "source": "adb"
      }
    ],
    "source": "podman"
  }
]`
	if string(got) != want {
		t.Fatalf("DeploymentStatus JSON drift:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestDeploymentStatusOmitemptyBoundary proves the required-vs-optional split: a
// zero-value row still emits the five REQUIRED keys (kind/image/status/container/
// run_mode, no omitempty) so a consumer can always key on them, while every optional
// field drops out. This is the field-presence contract the table/JSON renderers rely on.
func TestDeploymentStatusOmitemptyBoundary(t *testing.T) {
	got, err := json.Marshal(DeploymentStatus{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"kind":"","image":"","status":"","container":"","run_mode":""}`
	if string(got) != want {
		t.Fatalf("zero-value DeploymentStatus JSON drift:\n got: %s\nwant: %s", got, want)
	}
}

// TestOCIWireRoundTrip locks the OCI plugin's host<->plugin envelope shapes.
func TestOCIWireRoundTrip(t *testing.T) {
	req := MergeRequest{ImageRef: "ghcr.io/opencharly/x:1", MaxMB: 128, MaxTotalMB: 0, Engine: "podman", DryRun: true}
	var back MergeRequest
	b, _ := json.Marshal(req)
	if err := json.Unmarshal(b, &back); err != nil || back != req {
		t.Fatalf("MergeRequest round-trip: err=%v got=%+v want=%+v", err, back, req)
	}
	u := UserInfo{Found: true, Name: "user", UID: 1000, GID: 1000, Home: "/home/user"}
	var ub UserInfo
	json.Unmarshal(mustJSON(t, u), &ub) //nolint:errcheck
	if ub != u {
		t.Fatalf("UserInfo round-trip: got=%+v want=%+v", ub, u)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

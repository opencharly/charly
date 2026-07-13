package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/spec"
)

// TestStatusCollectGoldenParity is the P14a "S5 parity golden" for the STATUS
// collect engine — Collector.collectOne (status_collector.go), the pod-substrate
// row builder. It captures the CURRENT snapshot→DeploymentStatus→JSON transform
// as a STABLE golden so a later cutover can prove the relocated status-collection
// code produces byte-identical output.
//
// Hermeticity (no podman, no network, no live container):
//
//   - collectOne only shells out in TWO places, both avoided here:
//     (1) the image-label fallback (status_collector.go:231) calls
//     ResolveNewestLocalCalVer + ExtractMetadata, but ONLY when Ports OR Volumes
//     OR Network is empty. Each fixture fills all three, so the block is skipped.
//     (2) runProbes (status_collector.go:254) runs for a "running" row over the
//     package-level hostProbes/guestProbes sets; we save/empty/restore them, so
//     runProbes returns an empty (omitted) Tools set with zero subprocesses.
//   - c.deploy / c.unified are nulled so the charly.yml enrichment
//     (status_collector.go:211) is deterministic and independent of the host's
//     ~/.config/charly deploy state — collectOne is documented as a pure function
//     over (snapshot, deploy, engine).
//   - The runtime is a synthetic ResolvedRuntime literal (no ResolveRuntime()
//     probe); enginekit.NewEngineClient("podman") does no shell-out.
func TestStatusCollectGoldenParity(t *testing.T) {
	// Empty the probe registries so a "running" row collects zero tools without
	// any podman exec. Restored after the test.
	savedHost, savedGuest := hostProbes, guestProbes
	hostProbes, guestProbes = nil, nil
	defer func() { hostProbes, guestProbes = savedHost, savedGuest }()

	// Synthetic runtime — no ResolveRuntime() host probe. RunMode flows into the
	// DeploymentStatus.RunMode field verbatim.
	rt := &ResolvedRuntime{RunMode: "quadlet", RunEngine: "podman"}
	c, err := NewCollector(rt)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	// Pin the transform to the snapshot alone: no host ~/.config/charly deploy
	// state, no cwd charly.yml projection. collectOne is pure over the snapshot.
	c.deploy = nil
	c.unified = nil

	cases := []struct {
		name string
		snap *enginekit.ContainerSnapshot
		want string
	}{
		{
			name: "running-pod",
			snap: &enginekit.ContainerSnapshot{
				Name:        "charly-jupyter",
				State:       "running",
				Status:      "Up 3 hours",
				Box:         "jupyter",
				Instance:    "",
				ImageRef:    "ghcr.io/opencharly/jupyter:2026.162.1319",
				NetworkMode: "host",
				Ports: []spec.PortMapping{
					{HostIP: "127.0.0.1", HostPort: 8888, CtrPort: 8888, Proto: "tcp"},
				},
				Devices: []string{"/dev/dri/renderD128"},
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
				Name:        "charly-jupyter-gpu",
				State:       "running",
				Status:      "Up 12 minutes",
				Box:         "jupyter",
				Instance:    "gpu",
				ImageRef:    "ghcr.io/opencharly/jupyter:2026.162.1319",
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
			cs := c.collectOne(context.Background(), tc.snap)

			// RenderJSONOne itself relocated to candy/plugin-status/render.go
			// (P14a chunk 2b) — inline the identical encoding (indent "  ",
			// trailing newline via json.Encoder) so this golden stays pinned to
			// collectOne's OUTPUT SHAPE, not to the (now-external) renderer.
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			if err := enc.Encode(cs); err != nil {
				t.Fatalf("encode: %v", err)
			}
			got := buf.String()

			t.Logf("collectOne golden JSON:\n%s", got)
			if got != tc.want {
				t.Errorf("collectOne JSON golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

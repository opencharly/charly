package main

import (
	"strings"
	"testing"
)

func TestGeneratePodQuadlet(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:     "my-app",
		Ports:       []string{"443:18789", "8080:8080"},
		BindAddress: "127.0.0.1",
		Network:     "charly",
	}

	content := generatePodQuadlet(cfg)

	// Check pod name
	if !strings.Contains(content, "PodName=charly-my-app") {
		t.Error("missing PodName")
	}

	// Dual networking: pod stays on bridge network
	if !strings.Contains(content, "Network=charly") {
		t.Error("missing Network=charly (dual networking: pod must stay on bridge)")
	}

	// Check port mappings moved to pod
	if !strings.Contains(content, "PodmanArgs=-p 127.0.0.1:443:18789") {
		t.Error("missing port mapping 443:18789")
	}
	if !strings.Contains(content, "PodmanArgs=-p 127.0.0.1:8080:8080") {
		t.Error("missing port mapping 8080:8080")
	}

	// Check install section
	if !strings.Contains(content, "WantedBy=default.target") {
		t.Error("missing WantedBy")
	}
}

func TestGeneratePodQuadlet_Instance(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:  "my-app",
		Instance: "staging",
		Ports:    []string{"443:18789"},
	}

	content := generatePodQuadlet(cfg)
	if !strings.Contains(content, "PodName=charly-my-app-staging") {
		t.Error("missing instance in PodName")
	}
}

func TestGeneratePodQuadlet_ShmSize(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:  "my-app",
		Network:  "charly",
		Security: SecurityConfig{ShmSize: "1g"},
	}
	content := generatePodQuadlet(cfg)
	if !strings.Contains(content, "PodmanArgs=--shm-size=1g") {
		t.Error("pod must have --shm-size when ShmSize is set (infra container owns /dev/shm)")
	}
}

func TestGenerateQuadlet_PodMode_NoShmSize(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:    "my-app",
		ImageRef:   "ghcr.io/opencharly/my-app:latest",
		Home:       "/home/user",
		PodName:    "charly-my-app",
		Security:   SecurityConfig{ShmSize: "1g"},
		Entrypoint: []string{"sleep", "infinity"},
	}
	content := generateQuadlet(cfg)
	if strings.Contains(content, "ShmSize=") {
		t.Error("ShmSize must NOT be on container in pod mode (infra container owns /dev/shm)")
	}
}

func TestGenerateSidecarQuadlet(t *testing.T) {
	sc := ResolvedSidecar{
		Name:  "tailscale",
		Image: "ghcr.io/tailscale/tailscale:latest",
		Env: map[string]string{
			"TS_STATE_DIR":  "/var/lib/tailscale",
			"TS_USERSPACE":  "false",
			"TS_ACCEPT_DNS": "true",
		},
		Volume: []VolumeMount{
			{VolumeName: "charly-my-app-tailscale-state", ContainerPath: "/var/lib/tailscale"},
		},
		Secret: []CollectedSecret{
			{Name: "charly-my-app-tailscale-ts-authkey", Env: "TS_AUTHKEY"},
		},
		Security: SecurityConfig{
			CapAdd:  []string{"NET_ADMIN", "SYS_MODULE"},
			Devices: []string{"/dev/net/tun:/dev/net/tun"},
		},
	}

	content := generateSidecarQuadlet(sc, "charly-my-app")

	// Check pod reference
	if !strings.Contains(content, "Pod=charly-my-app.pod") {
		t.Error("missing Pod directive")
	}

	// Check container name
	if !strings.Contains(content, "ContainerName=charly-my-app-tailscale") {
		t.Error("missing ContainerName")
	}

	// Check image
	if !strings.Contains(content, "Image=ghcr.io/tailscale/tailscale:latest") {
		t.Error("missing Image")
	}

	// Check volume
	if !strings.Contains(content, "Volume=charly-my-app-tailscale-state:/var/lib/tailscale") {
		t.Error("missing volume")
	}

	// Check env vars (sorted)
	if !strings.Contains(content, "Environment=TS_ACCEPT_DNS=true") {
		t.Error("missing TS_ACCEPT_DNS")
	}
	if !strings.Contains(content, "Environment=TS_STATE_DIR=/var/lib/tailscale") {
		t.Error("missing TS_STATE_DIR")
	}
	if !strings.Contains(content, "Environment=TS_USERSPACE=false") {
		t.Error("missing TS_USERSPACE")
	}

	// Check secret
	if !strings.Contains(content, "Secret=charly-my-app-tailscale-ts-authkey,type=env,target=TS_AUTHKEY") {
		t.Error("missing secret")
	}

	// Check capabilities
	if !strings.Contains(content, "AddCapability=NET_ADMIN") {
		t.Error("missing NET_ADMIN capability")
	}
	if !strings.Contains(content, "AddCapability=SYS_MODULE") {
		t.Error("missing SYS_MODULE capability")
	}

	// Check device
	if !strings.Contains(content, "AddDevice=/dev/net/tun:/dev/net/tun") {
		t.Error("missing tun device")
	}

	// No TS_SERVE_CONFIG when tunnel is nil
	if strings.Contains(content, "TS_SERVE_CONFIG") {
		t.Error("should not have TS_SERVE_CONFIG without tunnel")
	}
}

func TestGenerateSidecarQuadlet_NoAutoServeConfig(t *testing.T) {
	// Sidecar should NOT auto-inject TS_SERVE_CONFIG even when tunnel is configured.
	// The host tunnel handles port exposure; the sidecar handles routing only.
	sc := ResolvedSidecar{
		Name:  "tailscale",
		Image: "ts:latest",
	}

	content := generateSidecarQuadlet(sc, "charly-my-app")

	if strings.Contains(content, "TS_SERVE_CONFIG") {
		t.Error("sidecar should NOT have TS_SERVE_CONFIG (host tunnel handles port exposure)")
	}
}

func TestGenerateQuadlet_PodMode(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:  "my-app",
		ImageRef: "ghcr.io/opencharly/my-app:latest",
		Home:     "/home/user",
		Ports:    []string{"443:18789"},
		Volumes: []VolumeMount{
			{VolumeName: "charly-my-app-data", ContainerPath: "/opt/data"},
		},
		BindAddress: "127.0.0.1",
		Network:     "charly",
		PodName:     "charly-my-app",
		Entrypoint:  []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"},
	}

	content := generateQuadlet(cfg)

	// Should have Pod= directive
	if !strings.Contains(content, "Pod=charly-my-app.pod") {
		t.Error("missing Pod directive in pod mode")
	}

	// Should NOT have PublishPort (moved to pod)
	if strings.Contains(content, "PublishPort=") {
		t.Error("PublishPort should not be present in pod mode (ports go on the pod)")
	}

	// Should NOT have Network (pod handles networking)
	if strings.Contains(content, "Network=") {
		t.Error("Network should not be present in pod mode (pod handles networking)")
	}

	// Should still have volumes and other directives
	if !strings.Contains(content, "Volume=charly-my-app-data:/opt/data") {
		t.Error("missing volume in pod mode")
	}
}

func TestGenerateQuadlet_NonPodMode(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:     "my-app",
		ImageRef:    "ghcr.io/opencharly/my-app:latest",
		Home:        "/home/user",
		Ports:       []string{"443:18789"},
		BindAddress: "127.0.0.1",
		Network:     "charly",
		Entrypoint:  []string{"sleep", "infinity"},
	}

	content := generateQuadlet(cfg)

	// Should NOT have Pod= directive
	if strings.Contains(content, "Pod=") {
		t.Error("Pod should not be present in non-pod mode")
	}

	// Should have PublishPort
	if !strings.Contains(content, "PublishPort=") {
		t.Error("missing PublishPort in non-pod mode")
	}

	// Should have Network
	if !strings.Contains(content, "Network=charly") {
		t.Error("missing Network in non-pod mode")
	}
}

func TestGenerateQuadlet_HostTunnelWithSidecar(t *testing.T) {
	// Host-based tailscale serve should be present EVEN when a sidecar exists.
	// The host tunnel handles port exposure on the host's tailnet;
	// the sidecar handles exit node routing — they are independent.
	cfg := QuadletConfig{
		BoxName:     "my-app",
		ImageRef:    "ghcr.io/opencharly/my-app:latest",
		Home:        "/home/user",
		Ports:       []string{"443:18789"},
		BindAddress: "127.0.0.1",
		PodName:     "charly-my-app",
		Entrypoint:  []string{"sleep", "infinity"},
		Tunnel: &TunnelConfig{
			Provider: "tailscale",
			Ports: []TunnelPort{
				{Port: 443, Protocol: "http", Public: false},
			},
		},
		Sidecar: []ResolvedSidecar{
			{Name: "tailscale", Image: "ts:latest"},
		},
	}

	content := generateQuadlet(cfg)

	// Host tunnel commands MUST be present (dual networking)
	if !strings.Contains(content, "ExecStartPost=tailscale serve") {
		t.Error("host tailscale serve should be present even with sidecar (dual networking)")
	}
	if !strings.Contains(content, "ExecStopPost=-tailscale serve") {
		t.Error("host tailscale serve cleanup should be present")
	}
}

func TestPodQuadletFilename(t *testing.T) {
	if got := podQuadletFilename("my-app"); got != "charly-my-app.pod" {
		t.Errorf("got %q, want charly-my-app.pod", got)
	}
}

func TestSidecarQuadletFilename(t *testing.T) {
	if got := sidecarQuadletFilename("my-app", "tailscale"); got != "charly-my-app-tailscale.container" {
		t.Errorf("got %q, want charly-my-app-tailscale.container", got)
	}
}

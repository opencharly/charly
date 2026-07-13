package deploypod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// config_write_test.go — the BYTE-PARITY harness for the pod config-WRITE (P11). It locks that
// podConfigWrite writes each file kind with content byte-identical to the deploykit generator AND at
// the exact modes the former in-core write phase used (.container/.pod/sidecar 0600, tunnel .service
// 0644), to exactly the host-resolved paths the request carries — so moving the write into the
// (out-of-process, same-host) plugin changes no bytes. The generators' own goldens (deploykit
// quadlet_test.go / quadlet_pod_test.go) lock the content; this locks the WRITE (paths + modes +
// the host-owned per-kind write conditionals).

func TestPodConfigWrite_ByteParity(t *testing.T) {
	dir := t.TempDir()
	cfg := deploykit.QuadletConfig{
		BoxName:  "immich",
		ImageRef: "ghcr.io/test/immich:latest",
		Home:     "/home/user",
		Ports:    []string{"2283:2283"},
		PodName:  "charly-immich",
		Sidecar: []deploykit.ResolvedSidecar{
			{Name: "tailscale", Image: "tailscale/tailscale:latest", Env: map[string]string{"TS_HOSTNAME": "immich"}},
		},
		Tunnel: &spec.TunnelConfig{
			Provider:   "cloudflare",
			TunnelName: "charly-immich",
			Ports:      []spec.TunnelPort{{Port: 2283, Protocol: "http", Public: true}},
		},
	}
	containerPath := filepath.Join(dir, "charly-immich.container")
	podPath := filepath.Join(dir, "charly-immich.pod")
	scPath := filepath.Join(dir, "charly-immich-tailscale.container")
	tunnelPath := filepath.Join(dir, "charly-immich-tunnel.service")
	cfgPath := "/home/user/.config/charly/tunnels/charly-immich.yml"

	reply, err := invokeConfigWrite(t, spec.PodConfigWriteRequest{
		PodConfigJSON:      mustJSON(t, cfg),
		ContainerPath:      containerPath,
		PodPath:            podPath,
		SidecarPaths:       map[string]string{"tailscale": scPath},
		TunnelPath:         tunnelPath,
		CloudflaredCfgPath: cfgPath,
	})
	if err != nil {
		t.Fatalf("podConfigWrite: %v", err)
	}

	// Content byte-parity + exact modes for each written file.
	assertFile(t, containerPath, deploykit.GenerateQuadlet(cfg), 0o600)
	assertFile(t, podPath, deploykit.GeneratePodQuadlet(cfg), 0o600)
	assertFile(t, scPath, deploykit.GenerateSidecarQuadlet(cfg.Sidecar[0], cfg.PodName), 0o600)
	assertFile(t, tunnelPath, deploykit.GenerateTunnelUnit(cfg, cfgPath), 0o644)

	// The reply lists exactly the 4 written paths, in write order (container, pod, sidecar, tunnel).
	want := []string{containerPath, podPath, scPath, tunnelPath}
	if len(reply.WrittenPaths) != len(want) {
		t.Fatalf("WrittenPaths = %v, want %v", reply.WrittenPaths, want)
	}
	for i, p := range want {
		if reply.WrittenPaths[i] != p {
			t.Errorf("WrittenPaths[%d] = %q, want %q", i, reply.WrittenPaths[i], p)
		}
	}
}

// TestPodConfigWrite_MinimalOnlyContainer: with no optional paths set (no sidecars, no cloudflare
// tunnel), ONLY the .container is written — the host owns the per-kind write conditionals.
func TestPodConfigWrite_MinimalOnlyContainer(t *testing.T) {
	dir := t.TempDir()
	cfg := deploykit.QuadletConfig{BoxName: "app", ImageRef: "ghcr.io/test/app:latest", Home: "/home/user"}
	containerPath := filepath.Join(dir, "charly-app.container")

	reply, err := invokeConfigWrite(t, spec.PodConfigWriteRequest{
		PodConfigJSON: mustJSON(t, cfg),
		ContainerPath: containerPath,
	})
	if err != nil {
		t.Fatalf("podConfigWrite: %v", err)
	}
	assertFile(t, containerPath, deploykit.GenerateQuadlet(cfg), 0o600)
	if len(reply.WrittenPaths) != 1 || reply.WrittenPaths[0] != containerPath {
		t.Fatalf("WrittenPaths = %v, want just %q", reply.WrittenPaths, containerPath)
	}
	for _, name := range []string{"charly-app.pod", "charly-app-x.container", "charly-app-tunnel.service"} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(statErr) {
			t.Errorf("unexpected file %s written (host set no path for it)", name)
		}
	}
}

// --- helpers ---

func invokeConfigWrite(t *testing.T, req spec.PodConfigWriteRequest) (spec.PodConfigWriteReply, error) {
	t.Helper()
	params, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// podConfigWrite reads only ParamsJson (the Invoke dispatch already routed by Op).
	replyMsg, err := podConfigWrite(&pb.InvokeRequest{ParamsJson: params})
	if err != nil {
		return spec.PodConfigWriteReply{}, err
	}
	var reply spec.PodConfigWriteReply
	if uerr := json.Unmarshal(replyMsg.GetResultJson(), &reply); uerr != nil {
		t.Fatalf("decode reply: %v", uerr)
	}
	return reply, nil
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertFile(t *testing.T, path, wantContent string, wantMode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != wantContent {
		t.Errorf("%s content mismatch:\n got:\n%s\nwant:\n%s", filepath.Base(path), got, wantContent)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != wantMode {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), info.Mode().Perm(), wantMode)
	}
}

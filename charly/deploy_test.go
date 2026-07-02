package main

import (
	"path/filepath"
	"testing"
)

// TestResolveVolumeBacking_HostPath locks the bind/encrypted host-path strategy
// (resolveVolumeHostPath) — the single helper that ResolveVolumeBacking's two
// passes (label-matched + deploy-only) share. Each case asserts the helper routes
// to the correct branch. This is the coverage the enc_test.go comments promised.
func TestResolveVolumeBacking_HostPath(t *testing.T) {
	const storageDir, encPath, volsPath = "charly-app-data", "/enc", "/vols"
	cases := []struct {
		name string
		dv   DeployVolumeConfig
		want string
	}{
		{"bind-default", DeployVolumeConfig{Type: "bind"}, filepath.Join(volsPath, storageDir, "data")},
		{"bind-host", DeployVolumeConfig{Type: "bind", Host: "/srv/data"}, expandHostHome("/srv/data")},
		{"encrypted-default", DeployVolumeConfig{Type: "encrypted"}, encryptedPlainDir(encPath, storageDir, "data")},
		{"encrypted-host", DeployVolumeConfig{Type: "encrypted", Host: "/srv/sec"}, filepath.Join(expandHostHome("/srv/sec"), "plain")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveVolumeHostPath(c.dv, "data", storageDir, encPath, volsPath)
			if got != c.want {
				t.Errorf("resolveVolumeHostPath = %q, want %q", got, c.want)
			}
		})
	}
}

// TestResolveVolumeBacking exercises the full backing resolution: a named volume
// with no deploy override stays a named volume; a bind/encrypted override becomes a
// bind mount; a deploy-only volume (no matching label, carries Path) is added as a
// bind. Proves the two passes (label-matched + deploy-only) both route through the
// shared resolveVolumeHostPath after the R3 dedup.
func TestResolveVolumeBacking(t *testing.T) {
	const boxName, instance = "app", ""
	labelVols := []VolumeMount{
		{VolumeName: deployVolumePrefix(boxName, instance) + "data", ContainerPath: "/data"},
		{VolumeName: deployVolumePrefix(boxName, instance) + "cache", ContainerPath: "/cache"},
	}
	deployVols := []DeployVolumeConfig{
		{Name: "data", Type: "bind", Host: "/srv/data"},    // override → bind mount
		{Name: "logs", Type: "bind", Path: "/var/log/app"}, // deploy-only → bind mount
		// "cache" has no deploy override → stays a named volume
	}
	volumes, binds := ResolveVolumeBacking(boxName, instance, labelVols, deployVols, "/home/user", "/enc", "/vols")

	if len(volumes) != 1 || volumes[0].VolumeName != deployVolumePrefix(boxName, instance)+"cache" {
		t.Fatalf("named volumes = %+v, want only the un-overridden cache", volumes)
	}
	got := map[string]ResolvedBindMount{}
	for _, b := range binds {
		got[b.Name] = b
	}
	if b, ok := got["data"]; !ok || b.HostPath != expandHostHome("/srv/data") || b.ContPath != "/data" {
		t.Errorf("label-matched bind 'data' = %+v, want host=%q cont=/data", got["data"], expandHostHome("/srv/data"))
	}
	if b, ok := got["logs"]; !ok || b.HostPath != filepath.Join("/vols", deployStorageDir(boxName, instance), "logs") {
		t.Errorf("deploy-only bind 'logs' = %+v, want default per-deploy host path", got["logs"])
	}
}

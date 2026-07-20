package deploypod

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// translate_test.go — the C10 pod-in-pod build-context path translator tests, MOVED from charly
// core (the former charly/deploy_target_pod_translate_test.go) when translateHostPathToVenue
// dissolved out of core into the candy (P11c). The translator walks the parent deploy node's
// bind-mount volumes, finds a bind-mount that contains the host path, and returns the equivalent
// venue-side path so a nested podman build can reach the same files. The candy variant takes the
// []spec.DeployVolume list (the host prep copies parentNode.Volume into the envelope as
// ParentVolumes) instead of the package-main *BundleNode.

func TestTranslateHostPathToVenue(t *testing.T) {
	vols := []spec.DeployVolume{
		{Name: "project", Type: "bind", Host: "/home/user/repo", Path: "/workspace"},
		{Name: "cache", Type: "bind", Host: "/home/user/.cache", Path: "/cache"},
		// Non-bind volume: ignored.
		{Name: "data", Type: "volume"},
		// Bind without Path: ignored (no venue side to map to).
		{Name: "tmp", Type: "bind", Host: "/tmp/x"},
	}

	tests := []struct {
		name      string
		hostPath  string
		wantPath  string
		wantFound bool
	}{
		{"exact match", "/home/user/repo", "/workspace", true},
		{"subpath match", "/home/user/repo/layers/x", "/workspace/layers/x", true},
		{"deep subpath", "/home/user/repo/a/b/c.txt", "/workspace/a/b/c.txt", true},
		{"alternate bind", "/home/user/.cache/foo", "/cache/foo", true},
		{"unrelated host path", "/etc/passwd", "", false},
		{"prefix-only match (not subpath)", "/home/user/repository", "", false},
		{"empty hostPath", "", "", false},
		{"trailing slash", "/home/user/repo/", "/workspace", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := translateHostPathToVenue(tt.hostPath, vols)
			if ok != tt.wantFound {
				t.Errorf("found = %v, want %v (path=%q)", ok, tt.wantFound, got)
			}
			if got != tt.wantPath {
				t.Errorf("path = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

// TestTranslateHostPathToVenue_NilVolumes: no volumes returns (false). Important because the
// production path passes reply.ParentVolumes which is nil for the common non-nested case.
func TestTranslateHostPathToVenue_NilVolumes(t *testing.T) {
	got, ok := translateHostPathToVenue("/home/user/repo", nil)
	if ok {
		t.Errorf("nil volumes: ok=true, want false (got=%q)", got)
	}
}

// TestTranslateHostPathToVenue_EmptyVolumes: an empty volume list also returns (false).
func TestTranslateHostPathToVenue_EmptyVolumes(t *testing.T) {
	got, ok := translateHostPathToVenue("/home/user/repo", []spec.DeployVolume{})
	if ok {
		t.Errorf("empty volumes: ok=true, want false (got=%q)", got)
	}
}

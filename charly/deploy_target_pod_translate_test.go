package main

import "testing"

// TestTranslateHostPathToVenue covers the C10 pod-in-pod build-context
// path translator. The translator walks the parent BundleNode's
// `volumes:` block, finds a bind-mount that contains the host path, and
// returns the equivalent venue-side path so a nested podman build can
// reach the same files.
func TestTranslateHostPathToVenue(t *testing.T) {
	parent := &BundleNode{
		Volume: []DeployVolumeConfig{
			{Name: "project", Type: "bind", Host: "/home/user/repo", Path: "/workspace"},
			{Name: "cache", Type: "bind", Host: "/home/user/.cache", Path: "/cache"},
			// Non-bind volume: ignored.
			{Name: "data", Type: "volume"},
			// Bind without Path: ignored (no venue side to map to).
			{Name: "tmp", Type: "bind", Host: "/tmp/x"},
		},
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
			got, ok := translateHostPathToVenue(tt.hostPath, parent)
			if ok != tt.wantFound {
				t.Errorf("found = %v, want %v (path=%q)", ok, tt.wantFound, got)
			}
			if got != tt.wantPath {
				t.Errorf("path = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

// TestTranslateHostPathToVenue_NilParent: nil parent returns (false).
// Important because the production code passes opts.ParentNode which
// is nil at the deployment-tree root.
func TestTranslateHostPathToVenue_NilParent(t *testing.T) {
	got, ok := translateHostPathToVenue("/home/user/repo", nil)
	if ok {
		t.Errorf("nil parent: ok=true, want false (got=%q)", got)
	}
}

// TestTranslateHostPathToVenue_EmptyVolumes: parent with no volumes
// also returns (false).
func TestTranslateHostPathToVenue_EmptyVolumes(t *testing.T) {
	got, ok := translateHostPathToVenue("/home/user/repo", &BundleNode{})
	if ok {
		t.Errorf("empty volumes: ok=true, want false (got=%q)", got)
	}
}

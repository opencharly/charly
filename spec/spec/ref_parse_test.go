package spec

import "testing"

func TestParseRemoteRef(t *testing.T) {
	tests := []struct {
		ref      string
		wantRepo string
		wantSub  string
		wantName string
		wantVer  string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo", "layers/cuda", "cuda", "v1.0.0"},
		{"@github.com/org/repo/layers/image:main", "github.com/org/repo", "layers/image", "image", "main"},
		{"@github.com/org/repo/layers/name", "github.com/org/repo", "layers/name", "name", ""},
		{"@github.com/org/repo/image", "github.com/org/repo", "image", "image", ""},
		{"pixi", "", "", "pixi", ""},
	}

	for _, tt := range tests {
		got := ParseRemoteRef(tt.ref)
		if got.RepoPath != tt.wantRepo || got.SubPath != tt.wantSub || got.Name != tt.wantName || got.Version != tt.wantVer {
			t.Errorf("ParseRemoteRef(%q) = {Repo: %q, SubPath: %q, Name: %q, Version: %q}, want {%q, %q, %q, %q}",
				tt.ref, got.RepoPath, got.SubPath, got.Name, got.Version, tt.wantRepo, tt.wantSub, tt.wantName, tt.wantVer)
		}
		if got.Raw != tt.ref {
			t.Errorf("ParseRemoteRef(%q).Raw = %q", tt.ref, got.Raw)
		}
	}
}

func TestIsRemoteImageRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"ollama", false},
		{"@github.com/org/repo/image", true},
		{"@github.com/org/repo/box:v1.0.0", true},
		{"github.com/org/repo/image", false}, // no @ prefix
	}

	for _, tt := range tests {
		got := IsRemoteImageRef(tt.ref)
		if got != tt.want {
			t.Errorf("IsRemoteImageRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

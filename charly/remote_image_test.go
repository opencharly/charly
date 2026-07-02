package main

import "testing"

func TestStripURLScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/org/repo/image", "github.com/org/repo/image"},
		{"http://github.com/org/repo/image", "github.com/org/repo/image"},
		{"github.com/org/repo/image", "github.com/org/repo/image"},
		{"@github.com/org/repo/box:v1.0.0", "@github.com/org/repo/box:v1.0.0"},
		{"myimage", "myimage"},
	}

	for _, tt := range tests {
		got := StripURLScheme(tt.input)
		if got != tt.want {
			t.Errorf("StripURLScheme(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRemoteContainerName(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"@github.com/org/repo/myapp:v1.0.0", "charly-myapp"},
		{"@github.com/org/repo/myapp", "charly-myapp"},
		{"@github.com/opencharly/charly/openclaw-browser:main", "charly-openclaw-browser"},
	}

	for _, tt := range tests {
		got := RemoteContainerName(tt.ref)
		if got != tt.want {
			t.Errorf("RemoteContainerName(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestResolveImageName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"myapp", "myapp"},
		{"@github.com/org/repo/myapp:v1.0.0", "myapp"},
		{"simple-image", "simple-image"},
	}

	for _, tt := range tests {
		got := resolveBoxName(tt.input)
		if got != tt.want {
			t.Errorf("resolveBoxName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

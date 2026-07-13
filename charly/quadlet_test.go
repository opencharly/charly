package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceName(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"fedora", "charly-fedora.service"},
		{"fedora-test", "charly-fedora-test.service"},
		{"ubuntu", "charly-ubuntu.service"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := serviceName(tt.image)
			if got != tt.want {
				t.Errorf("serviceName(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestServiceNameInstance(t *testing.T) {
	tests := []struct {
		image    string
		instance string
		want     string
	}{
		{"fedora", "", "charly-fedora.service"},
		{"githubrunner", "runner-1", "charly-githubrunner-runner-1.service"},
	}
	for _, tt := range tests {
		got := serviceNameInstance(tt.image, tt.instance)
		if got != tt.want {
			t.Errorf("serviceNameInstance(%q, %q) = %q, want %q", tt.image, tt.instance, got, tt.want)
		}
	}
}

func TestQuadletFilename(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"fedora", "charly-fedora.container"},
		{"fedora-test", "charly-fedora-test.container"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := quadletFilename(tt.image)
			if got != tt.want {
				t.Errorf("quadletFilename(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestQuadletFilenameInstance(t *testing.T) {
	tests := []struct {
		image    string
		instance string
		want     string
	}{
		{"fedora", "", "charly-fedora.container"},
		{"githubrunner", "runner-2", "charly-githubrunner-runner-2.container"},
	}
	for _, tt := range tests {
		got := quadletFilenameInstance(tt.image, tt.instance)
		if got != tt.want {
			t.Errorf("quadletFilenameInstance(%q, %q) = %q, want %q", tt.image, tt.instance, got, tt.want)
		}
	}
}

func TestQuadletDir(t *testing.T) {
	got, err := quadletDir()
	if err != nil {
		t.Fatalf("quadletDir() error: %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "containers", "systemd")
	if got != want {
		t.Errorf("quadletDir() = %q, want %q", got, want)
	}
}

func TestSystemdUserDir(t *testing.T) {
	got, err := systemdUserDir()
	if err != nil {
		t.Fatalf("systemdUserDir() error: %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "systemd", "user")
	if got != want {
		t.Errorf("systemdUserDir() = %q, want %q", got, want)
	}
}

func TestQuadletExists(t *testing.T) {
	tmpDir := t.TempDir()
	// Write a fake .container file
	systemdDir := filepath.Join(tmpDir, ".config", "containers", "systemd")
	if err := os.MkdirAll(systemdDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemdDir, "charly-testimg.container"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Override HOME so quadletDir resolves to our temp dir
	origHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome) //nolint:errcheck

	exists, err := quadletExists("testimg")
	if err != nil {
		t.Fatalf("quadletExists() error: %v", err)
	}
	if !exists {
		t.Error("expected quadletExists to return true for existing file")
	}

	exists, err = quadletExists("nonexistent")
	if err != nil {
		t.Fatalf("quadletExists() error: %v", err)
	}
	if exists {
		t.Error("expected quadletExists to return false for nonexistent file")
	}
}

package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// container_name_test.go — spec.ContainerName/spec.ContainerNameInstance regression coverage,
// relocated from the deleted start_test.go (Cutover B unit 2: buildStartArgs/resolveEntrypointFromMeta
// were dead code — zero non-test callers — and deleted with the rest of start_test.go; these two
// cases test unrelated, still-live sdk/kit helpers and are preserved here).

func TestContainerName(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"fedora-test", "charly-fedora-test"},
		{"fedora", "charly-fedora"},
		{"ubuntu", "charly-ubuntu"},
	}
	for _, tt := range tests {
		got := spec.ContainerName(tt.image)
		if got != tt.want {
			t.Errorf("containerName(%q) = %q, want %q", tt.image, got, tt.want)
		}
	}
}

func TestContainerNameInstance(t *testing.T) {
	tests := []struct {
		image    string
		instance string
		want     string
	}{
		{"githubrunner", "", "charly-githubrunner"},
		{"githubrunner", "runner-1", "charly-githubrunner-runner-1"},
		{"ollama", "gpu2", "charly-ollama-gpu2"},
	}
	for _, tt := range tests {
		got := spec.ContainerNameInstance(tt.image, tt.instance)
		if got != tt.want {
			t.Errorf("containerNameInstance(%q, %q) = %q, want %q", tt.image, tt.instance, got, tt.want)
		}
	}
}

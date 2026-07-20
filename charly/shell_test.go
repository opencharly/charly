package main

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// shell_test.go — buildShellArgs/buildExecArgs's own tests relocated 1:1 to
// candy/plugin-deploy-pod/resolve_f12_test.go (P13-KERNEL step-4(ii): those functions moved).
// What stays: TestLocalizePort (tests deploykit.LocalizePort directly) and
// TestResolveShellImageRef (resolveShellImageRef did NOT move — still core-side, used by the
// "pod-config-resolve-ref" seam handler and other core callers).

func TestLocalizePort(t *testing.T) {
	tests := []struct {
		input    string
		bindAddr string
		want     string
	}{
		{"80:8000", "127.0.0.1", "127.0.0.1:80:8000"},
		{"8080:8080", "127.0.0.1", "127.0.0.1:8080:8080"},
		{"8080", "127.0.0.1", "127.0.0.1:8080:8080"},
		{"9090", "127.0.0.1", "127.0.0.1:9090:9090"},
		{"80:8000", "0.0.0.0", "0.0.0.0:80:8000"},
		{"8080", "0.0.0.0", "0.0.0.0:8080:8080"},
		{"47998:47998/udp", "127.0.0.1", "127.0.0.1:47998:47998/udp"},
		{"48000/udp", "127.0.0.1", "127.0.0.1:48000:48000/udp"},
		{"47990:47990/tcp", "127.0.0.1", "127.0.0.1:47990:47990/tcp"},
	}
	for _, tt := range tests {
		t.Run(tt.bindAddr+"/"+tt.input, func(t *testing.T) {
			got := deploykit.LocalizePort(tt.input, tt.bindAddr)
			if got != tt.want {
				t.Errorf("localizePort(%q, %q) = %q, want %q", tt.input, tt.bindAddr, got, tt.want)
			}
		})
	}
}

func TestResolveShellImageRef(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		image    string
		tag      string
		want     string
	}{
		{
			name:     "with registry",
			registry: "ghcr.io/opencharly",
			image:    "fedora",
			tag:      "latest",
			want:     "ghcr.io/opencharly/fedora:latest",
		},
		{
			name:     "without registry",
			registry: "",
			image:    "fedora",
			tag:      "latest",
			want:     "fedora:latest",
		},
		{
			name:     "custom tag",
			registry: "ghcr.io/opencharly",
			image:    "ubuntu",
			tag:      "2026.046.1415",
			want:     "ghcr.io/opencharly/ubuntu:2026.046.1415",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveShellImageRef(tt.registry, tt.image, tt.tag)
			if got != tt.want {
				t.Errorf("resolveShellImageRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

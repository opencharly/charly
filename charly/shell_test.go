package main

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// shell_test.go — buildShellArgs/buildExecArgs's own tests relocated 1:1 to
// candy/plugin-deploy-pod/resolve_f12_test.go (P13-KERNEL step-4(ii): those functions moved).
// What stays: TestLocalizePort (tests deploykit.LocalizePort directly). TestResolveShellImageRef
// DELETED (Cutover B unit 2): shell.go's resolveShellImageRef was a bare 1-line delegate to
// kit.ResolveShellImageRef — every call site now calls kit.ResolveShellImageRef directly (R3, no
// value in the pass-through wrapper); kit.ResolveShellImageRef carries its own sdk/kit test.

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

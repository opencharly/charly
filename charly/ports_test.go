package main

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"8000:8000", 8000},
		{"8000", 8000},
		{"5901:5900", 5901},
		{"443:8443", 443},
		{"47998:47998/udp", 47998},
		{"48000/udp", 48000},
		// 3-segment IP:Host:Container form — added 2026-04 alongside the
		// silent-tunnel-skip fix. Pre-fix this errored.
		{"127.0.0.1:8888:8888", 8888},
		{"127.0.0.1:5901:5900", 5901},
		{"[::1]:8080:80", 8080},
		{"127.0.0.1:47998:47998/udp", 47998},
	}
	for _, tt := range tests {
		got, err := ParseHostPort(tt.input)
		if err != nil {
			t.Errorf("ParseHostPort(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseHostPort(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseContainerPort(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"8000:9000", 9000},
		{"8000", 8000},
		{"5901:5900", 5900},
		{"47998:47998/udp", 47998},
		{"48000/udp", 48000},
		// 3-segment IP:Host:Container form (regression).
		{"127.0.0.1:8888:8888", 8888},
		{"127.0.0.1:5901:5900", 5900},
		{"[::1]:8080:80", 80},
	}
	for _, tt := range tests {
		got, err := ParseContainerPort(tt.input)
		if err != nil {
			t.Errorf("ParseContainerPort(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseContainerPort(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParsePortMapping(t *testing.T) {
	tests := []struct {
		input        string
		wantBindAddr string
		wantHost     int
		wantCont     int
		wantProto    string
		wantOK       bool
	}{
		{"8888", "", 8888, 8888, "", true},
		{"8080:80", "", 8080, 80, "", true},
		{"127.0.0.1:8888:8888", "127.0.0.1", 8888, 8888, "", true},
		{"127.0.0.1:5901:5900", "127.0.0.1", 5901, 5900, "", true},
		{"[::1]:8080:80", "[::1]", 8080, 80, "", true},
		{"[fd7a:115c:a1e0::9435:8d3b]:8080:80", "[fd7a:115c:a1e0::9435:8d3b]", 8080, 80, "", true},
		{"47998:47998/udp", "", 47998, 47998, "udp", true},
		{"127.0.0.1:53:53/udp", "127.0.0.1", 53, 53, "udp", true},
		{"8080/tcp", "", 8080, 8080, "tcp", true},
		// Invalid shapes — must return ok=false (loud-skip path).
		{"not-a-port", "", 0, 0, "", false},
		{"127.0.0.1", "", 0, 0, "", false},
		{"a:b:c:d", "", 0, 0, "", false},
		{"-1:8888", "", 0, 0, "", false},
		{"99999:8888", "", 0, 0, "", false},
		{"", "", 0, 0, "", false},
	}
	for _, tt := range tests {
		got, ok := ParsePortMapping(tt.input)
		if ok != tt.wantOK {
			t.Errorf("ParsePortMapping(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got.BindAddr != tt.wantBindAddr || got.Host != tt.wantHost || got.Container != tt.wantCont || got.Protocol != tt.wantProto {
			t.Errorf("ParsePortMapping(%q) = {bind=%q host=%d cont=%d proto=%q}, want {bind=%q host=%d cont=%d proto=%q}",
				tt.input, got.BindAddr, got.Host, got.Container, got.Protocol,
				tt.wantBindAddr, tt.wantHost, tt.wantCont, tt.wantProto)
		}
	}
}

func TestFormatPortMapping(t *testing.T) {
	tests := []struct {
		in   ParsedPortMapping
		want string
	}{
		{ParsedPortMapping{Host: 8888, Container: 8888}, "8888:8888"},
		{ParsedPortMapping{Host: 8080, Container: 80}, "8080:80"},
		{ParsedPortMapping{BindAddr: "127.0.0.1", Host: 8888, Container: 8888}, "127.0.0.1:8888:8888"},
		{ParsedPortMapping{BindAddr: "[::1]", Host: 8080, Container: 80}, "[::1]:8080:80"},
		{ParsedPortMapping{Host: 53, Container: 53, Protocol: "udp"}, "53:53/udp"},
		{ParsedPortMapping{BindAddr: "127.0.0.1", Host: 53, Container: 53, Protocol: "udp"}, "127.0.0.1:53:53/udp"},
	}
	for _, tt := range tests {
		got := FormatPortMapping(tt.in)
		if got != tt.want {
			t.Errorf("FormatPortMapping(%+v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParsePortMapping_RoundTripsFormatPortMapping(t *testing.T) {
	inputs := []string{
		"8888",
		"8080:80",
		"127.0.0.1:8888:8888",
		"[::1]:8080:80",
		"47998:47998/udp",
		"127.0.0.1:53:53/udp",
	}
	for _, in := range inputs {
		p, ok := ParsePortMapping(in)
		if !ok {
			t.Errorf("ParsePortMapping(%q) failed", in)
			continue
		}
		// Bare "8888" canonicalizes to "8888:8888" — that's expected.
		got := FormatPortMapping(p)
		// Re-parse the formatted form; both must produce the same struct.
		p2, ok := ParsePortMapping(got)
		if !ok || p2 != p {
			t.Errorf("round-trip diverged: %q -> %+v -> %q -> %+v", in, p, got, p2)
		}
	}
}

// TestLocalizePort_PreservesIPv4Prefix is the regression test for the bug that
// produced PublishPort=127.0.0.1:127.0.0.1:8888:8888 in the wild — see the
// 2026-04-29 fix-port-tunnel cutover.
func TestLocalizePort_PreservesIPv4Prefix(t *testing.T) {
	tests := []struct {
		input    string
		bindAddr string
		want     string
	}{
		// Explicit IPv4 prefix is preserved verbatim, not double-prepended.
		{"127.0.0.1:8888:8888", "127.0.0.1", "127.0.0.1:8888:8888"},
		{"127.0.0.1:5901:5900", "127.0.0.1", "127.0.0.1:5901:5900"},
		// A different bindAddr also doesn't leak in when one is explicit.
		{"10.0.0.1:8080:80", "127.0.0.1", "10.0.0.1:8080:80"},
		// IPv6 bracket form, same rule.
		{"[::1]:8080:80", "127.0.0.1", "[::1]:8080:80"},
		// Bare forms still get the bind address prepended (existing behavior).
		{"8888:8888", "127.0.0.1", "127.0.0.1:8888:8888"},
		{"8888", "127.0.0.1", "127.0.0.1:8888:8888"},
		// Protocol suffix preserved in both branches.
		{"127.0.0.1:53:53/udp", "127.0.0.1", "127.0.0.1:53:53/udp"},
		{"53:53/udp", "127.0.0.1", "127.0.0.1:53:53/udp"},
	}
	for _, tt := range tests {
		got := deploykit.LocalizePort(tt.input, tt.bindAddr)
		if got != tt.want {
			t.Errorf("localizePort(%q, %q) = %q, want %q", tt.input, tt.bindAddr, got, tt.want)
		}
	}
}

func TestApplyPortOverrides(t *testing.T) {
	ports := []string{"5900:5900", "18789:18789", "9222:9222", "11434:11434"}
	overrides := []string{"5901:5900", "11435:11434"}

	result, err := ApplyPortOverrides(ports, overrides)
	if err != nil {
		t.Fatalf("ApplyPortOverrides error: %v", err)
	}

	expected := []string{"5901:5900", "18789:18789", "9222:9222", "11435:11434"}
	if len(result) != len(expected) {
		t.Fatalf("got %d ports, want %d", len(result), len(expected))
	}
	for i, got := range result {
		if got != expected[i] {
			t.Errorf("port[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestApplyPortOverridesUDP(t *testing.T) {
	ports := []string{"47990:47990", "47998:47998/udp", "47999:47999/udp", "48000:48000/udp"}
	overrides := []string{"47991:47990", "48001:48000"}

	result, err := ApplyPortOverrides(ports, overrides)
	if err != nil {
		t.Fatalf("ApplyPortOverrides error: %v", err)
	}

	expected := []string{"47991:47990", "47998:47998/udp", "47999:47999/udp", "48001:48000/udp"}
	if len(result) != len(expected) {
		t.Fatalf("got %d ports, want %d", len(result), len(expected))
	}
	for i, got := range result {
		if got != expected[i] {
			t.Errorf("port[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestStripPortSuffix(t *testing.T) {
	tests := []struct {
		input     string
		wantPort  string
		wantProto string
	}{
		{"47998/udp", "47998", "udp"},
		{"5900/tcp", "5900", "tcp"},
		{"8000", "8000", ""},
		{"47998:47998/udp", "47998:47998", "udp"},
	}
	for _, tt := range tests {
		port, proto := stripPortSuffix(tt.input)
		if port != tt.wantPort || proto != tt.wantProto {
			t.Errorf("stripPortSuffix(%q) = (%q, %q), want (%q, %q)", tt.input, port, proto, tt.wantPort, tt.wantProto)
		}
	}
}

func TestCheckPortAvailabilityUDP(t *testing.T) {
	// Bind a UDP port, then check it
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test UDP port: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	port := conn.LocalAddr().(*net.UDPAddr).Port
	ports := []string{strconv.Itoa(port) + ":" + strconv.Itoa(port) + "/udp"}

	conflicts := CheckPortAvailability(ports, "127.0.0.1", "podman")
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 UDP conflict, got %d", len(conflicts))
	}
	if conflicts[0].HostPort != port {
		t.Errorf("conflict port = %d, want %d", conflicts[0].HostPort, port)
	}
}

func TestApplyPortOverridesNoMatch(t *testing.T) {
	ports := []string{"8000:8000", "9000:9000"}
	overrides := []string{"5901:5900"} // 5900 not in ports

	result, err := ApplyPortOverrides(ports, overrides)
	if err != nil {
		t.Fatalf("ApplyPortOverrides error: %v", err)
	}

	// No change — override doesn't match any container port
	for i, got := range result {
		if got != ports[i] {
			t.Errorf("port[%d] = %q, want %q (unchanged)", i, got, ports[i])
		}
	}
}

func TestApplyPortOverridesInvalid(t *testing.T) {
	_, err := ApplyPortOverrides([]string{"8000:8000"}, []string{"notaport"})
	if err == nil {
		t.Error("expected error for invalid override format")
	}
}

func TestCheckPortAvailabilityOpen(t *testing.T) {
	// All ports should be available on random high ports
	ports := []string{"0:0"} // port 0 is always available
	conflicts := CheckPortAvailability(ports, "127.0.0.1", "podman")
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts for port 0, got %d", len(conflicts))
	}
}

func TestCheckPortAvailabilityInUse(t *testing.T) {
	// Bind a port, then check it
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test port: %v", err)
	}
	defer ln.Close() //nolint:errcheck

	port := ln.Addr().(*net.TCPAddr).Port
	ports := []string{strconv.Itoa(port) + ":" + strconv.Itoa(port)}

	conflicts := CheckPortAvailability(ports, "127.0.0.1", "podman")
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].HostPort != port {
		t.Errorf("conflict port = %d, want %d", conflicts[0].HostPort, port)
	}
}

func TestFormatPortConflicts(t *testing.T) {
	conflicts := []PortConflict{
		{HostPort: 5900, ContPort: 5900, Owner: "charly-sway-browser-vnc", OwnerType: "charly-container"},
		{HostPort: 11434, ContPort: 11434, Owner: "ollama-1", OwnerType: "container"},
	}

	output := FormatPortConflicts(conflicts, "my-image")

	if !containsAll(output, "Port 5900", "charly stop", "Port 11434", "podman stop", "--port") {
		t.Errorf("FormatPortConflicts output missing expected content:\n%s", output)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
